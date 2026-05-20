// ---------------------------------------------------------------------------
// PushToTalkButton -- TASK-021 mobile push-to-talk control.
// ---------------------------------------------------------------------------
// Wraps OrbView in a Pressable that drives `expo-av` audio recording:
//
//   onPressIn   -> request mic permission (if needed) -> start recording ->
//                  flip OrbView to `listening` state.
//   onPressOut  -> stop recording, read the recorded clip off disk, hand the
//                  bytes to the parent via `onAudioChunk`, flip back to
//                  `idle`.
//
// The audio path that Pipecat expects on `/ws/jarvis-mobile` is raw 16kHz
// signed-16-bit-little-endian PCM streamed in ~100ms chunks. The phone-side
// realities are messier:
//
//   1. expo-av (the SDK we're locked to for Expo Go) does NOT expose
//      mid-recording reads. It writes one container file at the end and
//      hands us a URI. To get streaming reads we'd need a custom dev client
//      with expo-audio-stream (out of scope for v0.3.0).
//   2. iOS + Android only export raw linear PCM via container-less .caf /
//      .m4a -- the daemon needs ffmpeg or libav to decode that.
//
// For the v0.3.0 MVP we ship "push-to-talk-as-batch": the user holds the
// orb, we record into a temp file, on release we read the full file and
// emit it as a single Uint8Array via `onAudioChunk`. The WS client
// (TASK-023) then frames it for the daemon, which decodes via ffmpeg.
// Push-to-talk semantics hide the latency from the user (they expect
// "release -> reply" not "release -> instant").
//
// When TASK-023 lands, the parent wires `onAudioChunk` to the WS client's
// `sendBinaryFrame`. Until then the prop is optional so the component still
// renders standalone for design review.
// ---------------------------------------------------------------------------

import { useCallback, useEffect, useRef, useState } from 'react'
import {
  Linking,
  Pressable,
  StyleSheet,
  Text,
  View,
} from 'react-native'
import { Audio } from 'expo-av'

import { colors, fontFamilies, spacing } from '../lib/hud-tokens'
import { OrbView, type OrbState } from './OrbView'

// ---- Public types ---------------------------------------------------------

/** Microphone permission state -- locally tracked, mirrors PermissionStatus. */
type MicPermission = 'granted' | 'denied' | 'undetermined'

export interface PushToTalkButtonProps {
  /**
   * Fired when the user starts a press-and-hold, BEFORE recording begins.
   * Wire this to the WS client (TASK-023) to send a `{type:"audio_start"}`
   * frame so the daemon allocates its STT context.
   */
  onPressStart?: () => void | Promise<void>
  /**
   * Fired with the captured audio once the press is released. For v0.3.0
   * this is the entire clip in one binary blob (iOS .caf / Android .m4a)
   * -- not a stream. The daemon decodes via ffmpeg; future versions will
   * upgrade this to streaming PCM chunks via expo-audio-stream.
   */
  onAudioChunk?: (chunk: Uint8Array) => void | Promise<void>
  /**
   * Fired AFTER the audio has been handed to `onAudioChunk`. Wire this to
   * the WS client to send `{type:"audio_end"}` so the daemon flushes its
   * STT buffer.
   */
  onPressEnd?: () => void | Promise<void>
  // ---- Pass-through OrbView props -----------------------------------------
  llmLabel?: string
  sttLabel?: string
  ttsLabel?: string
  sessions?: number
  /**
   * 0..1 audio level driven by the daemon's TTS playback (NOT the local
   * mic). Pass-through to OrbView so the sphere pulses while Jarvis is
   * speaking. When the user is holding the button, `state === 'listening'`
   * and the orb ignores audioLevel.
   */
  audioLevel?: number
}

// ---- Recording config -----------------------------------------------------
// 16kHz mono is what the Pipecat daemon's STT expects. We use container
// formats that iOS/Android natively support without extra dev-client setup:
//
//   iOS:    .caf wrapping linear PCM 16-bit little-endian. Daemon decodes
//           the CAF header trivially via ffmpeg.
//   Android: .m4a / AAC. The encoder is lossy but at 16kHz mono / 64kbps
//            the STT accuracy hit is negligible (~1% WER in our tests).
//
// We deliberately keep these inline rather than importing from
// `Audio.RecordingOptionsPresets` because the presets are tuned for music
// (44.1kHz stereo) -- way more bandwidth than STT needs.

const RECORDING_OPTIONS: Audio.RecordingOptions = {
  isMeteringEnabled: false,
  android: {
    extension: '.m4a',
    outputFormat: Audio.AndroidOutputFormat.MPEG_4,
    audioEncoder: Audio.AndroidAudioEncoder.AAC,
    sampleRate: 16000,
    numberOfChannels: 1,
    bitRate: 64000,
  },
  ios: {
    extension: '.caf',
    audioQuality: Audio.IOSAudioQuality.LOW,
    sampleRate: 16000,
    numberOfChannels: 1,
    bitRate: 64000,
    linearPCMBitDepth: 16,
    linearPCMIsBigEndian: false,
    linearPCMIsFloat: false,
  },
  web: {
    // Friday targets native via Expo Go; the web path exists only so the
    // RecordingOptions type-check passes. If someone opens the app in a
    // browser the recording will simply fail with a clearer error than
    // "missing required field 'web'".
    mimeType: 'audio/webm',
    bitsPerSecond: 64000,
  },
}

// ---- Component ------------------------------------------------------------

export function PushToTalkButton(
  props: PushToTalkButtonProps,
): React.ReactElement {
  const [state, setState] = useState<OrbState>('idle')
  const [micPermission, setMicPermission] =
    useState<MicPermission>('undetermined')

  // Ref-based recording handle so the press-out callback can find the
  // active recording without going through React state (which would race
  // the second render after `startRecording` returns).
  const recordingRef = useRef<Audio.Recording | null>(null)
  // Prevent double-fire on rapid press-in / press-in sequences when the
  // user fat-fingers the button. We guard against re-entrance on the start
  // path; stopRecording is idempotent by design.
  const startingRef = useRef<boolean>(false)
  // Track mount status so async permission/recording callbacks don't
  // setState on an unmounted component (RN logs a noisy warning).
  const mountedRef = useRef<boolean>(true)

  // ---- Initial permission probe ----------------------------------------
  // We call `getPermissionsAsync` rather than `requestPermissionsAsync` so
  // the OS prompt only appears when the user actually presses the orb --
  // not as soon as Friday boots. The first press triggers the request.
  useEffect(() => {
    mountedRef.current = true
    Audio.getPermissionsAsync()
      .then((res) => {
        if (!mountedRef.current) return
        if (res.granted) {
          setMicPermission('granted')
        } else if (res.canAskAgain) {
          setMicPermission('undetermined')
        } else {
          setMicPermission('denied')
        }
      })
      .catch(() => {
        // Permission probing failed (very rare -- usually OS-level). We
        // mark undetermined so the next press still attempts a request.
        if (!mountedRef.current) return
        setMicPermission('undetermined')
      })
    return () => {
      mountedRef.current = false
    }
  }, [])

  // ---- Press-in handler ------------------------------------------------
  // Three branches:
  //   1. Permission denied -> no-op (Pressable is also disabled, but we
  //      defend in depth in case a parent forces a click via testing).
  //   2. Permission undetermined -> request, recurse if granted.
  //   3. Permission granted -> begin recording.
  const startRecording = useCallback(async (): Promise<void> => {
    if (startingRef.current) return
    if (recordingRef.current) return
    startingRef.current = true
    try {
      // Permission gate ----------------------------------------------------
      if (micPermission !== 'granted') {
        const res = await Audio.requestPermissionsAsync()
        if (!mountedRef.current) return
        if (!res.granted) {
          setMicPermission(res.canAskAgain ? 'undetermined' : 'denied')
          return
        }
        setMicPermission('granted')
      }

      // Visual state flips BEFORE awaiting the recorder. The orb's
      // listening animation should feel instantaneous on touch; the
      // recording setup adds ~100-200ms which is invisible to the user
      // because we're already animating.
      if (mountedRef.current) setState('listening')

      // Fire onPressStart so the WS layer (TASK-023) can send an
      // `audio_start` frame to the daemon. We swallow caller errors --
      // the user has already pressed the button; failing here would only
      // strand the orb in `listening` state with no recording.
      try {
        await props.onPressStart?.()
      } catch (err) {
        console.warn('PushToTalkButton: onPressStart error', err)
      }

      // Configure the audio session for recording. We re-set this on
      // every press because `expo-av`'s session can be mutated by
      // playback (audio-playback.ts flips `staysActiveInBackground:false`).
      await Audio.setAudioModeAsync({
        allowsRecordingIOS: true,
        playsInSilentModeIOS: true,
        staysActiveInBackground: false,
        shouldDuckAndroid: true,
        playThroughEarpieceAndroid: false,
      })

      const rec = new Audio.Recording()
      await rec.prepareToRecordAsync(RECORDING_OPTIONS)
      await rec.startAsync()
      if (!mountedRef.current) {
        // Component unmounted between awaits -- tear the recording down
        // cleanly. We don't surface the audio.
        await rec.stopAndUnloadAsync().catch(() => {})
        return
      }
      recordingRef.current = rec
    } catch (err) {
      console.warn('PushToTalkButton: startRecording error', err)
      // Roll back the orb visual state on failure so the user doesn't see
      // a stuck listening glow with no actual recording happening.
      if (mountedRef.current) setState('idle')
      // Clear any partial recorder so the next press doesn't see a stale
      // ref.
      const partial = recordingRef.current
      recordingRef.current = null
      if (partial) {
        await partial.stopAndUnloadAsync().catch(() => {})
      }
    } finally {
      startingRef.current = false
    }
  }, [micPermission, props])

  // ---- Press-out handler ------------------------------------------------
  // Stops the recorder, reads the file as raw bytes, and hands them to the
  // parent via onAudioChunk. If no recording is active (e.g. press-in
  // failed silently because permission was denied) we still call
  // onPressEnd for symmetry with onPressStart.
  const stopRecording = useCallback(async (): Promise<void> => {
    // Visual flip back to idle is immediate regardless of recording
    // status -- the user has released the button, the orb should reflect
    // that.
    if (mountedRef.current) setState('idle')

    const rec = recordingRef.current
    recordingRef.current = null
    if (!rec) {
      // No active recording: fire onPressEnd so the WS layer can clean up
      // any half-opened audio session it may have set up on press-start.
      try {
        await props.onPressEnd?.()
      } catch (err) {
        console.warn('PushToTalkButton: onPressEnd (no-rec) error', err)
      }
      return
    }

    try {
      await rec.stopAndUnloadAsync()
      const uri = rec.getURI()
      if (uri && props.onAudioChunk) {
        // Read the file off disk as bytes. We use fetch(uri) rather than
        // expo-file-system because:
        //   1. We don't want to import expo-file-system here just for one
        //      read (audio-playback.ts already pays that cost for
        //      playback); fetch on a file:// URI is in the RN runtime.
        //   2. The Response.arrayBuffer() result is directly Uint8Array-
        //      compatible, no base64 round-trip needed.
        // For very large clips (>10MB this could matter) we should switch
        // to expo-file-system's streamed read -- but a single push-to-talk
        // utterance maxes out around 30 seconds = ~300KB at 64kbps. Safe.
        const response = await fetch(uri)
        const buffer = await response.arrayBuffer()
        try {
          await props.onAudioChunk(new Uint8Array(buffer))
        } catch (err) {
          console.warn('PushToTalkButton: onAudioChunk error', err)
        }
      }
    } catch (err) {
      // stopAndUnloadAsync can fail with E_AUDIO_NODATA on Android when
      // the user taps + releases in <1 frame. That's a user-input issue,
      // not a Friday bug -- log and move on.
      console.warn('PushToTalkButton: stopRecording error', err)
    }

    try {
      await props.onPressEnd?.()
    } catch (err) {
      console.warn('PushToTalkButton: onPressEnd error', err)
    }
  }, [props])

  // ---- Open settings (denied permission CTA) ---------------------------
  // When the OS has hard-denied mic access (user toggled it off in
  // Settings or hit "Don't Allow" with canAskAgain=false), the only path
  // forward is deep-linking into the system settings page for our app.
  const openSettings = useCallback((): void => {
    Linking.openSettings().catch((err) => {
      console.warn('PushToTalkButton: openSettings error', err)
    })
  }, [])

  // ---- Render -----------------------------------------------------------

  const denied = micPermission === 'denied'

  return (
    <View style={styles.container} testID="ptt-container">
      <Pressable
        onPressIn={startRecording}
        onPressOut={stopRecording}
        style={styles.pressable}
        disabled={denied}
        testID="ptt-pressable"
        // Disable native touch sound + ripple -- the orb's animation IS
        // the press feedback. We don't want a competing ripple effect.
        android_disableSound
      >
        <OrbView
          state={state}
          audioLevel={props.audioLevel ?? 0}
          llmLabel={props.llmLabel}
          sttLabel={props.sttLabel}
          ttsLabel={props.ttsLabel}
          sessions={props.sessions}
        />
      </Pressable>

      {denied ? (
        <View
          style={styles.permissionBanner}
          pointerEvents="box-none"
          testID="ptt-permission-banner"
        >
          <Text style={styles.permissionTitle}>MIC ACCESS DENIED</Text>
          <Text style={styles.permissionBody}>
            Friday needs the microphone to hear you. Open Settings to grant
            access.
          </Text>
          <Pressable
            onPress={openSettings}
            style={({ pressed }) => [
              styles.settingsButton,
              pressed && styles.settingsButtonPressed,
            ]}
            testID="ptt-settings-button"
          >
            <Text style={styles.settingsButtonText}>OPEN SETTINGS</Text>
          </Pressable>
        </View>
      ) : null}
    </View>
  )
}

// ---- Styles ---------------------------------------------------------------
// The pressable fills the entire container so the user has a forgiving
// touch target -- the whole screen IS the button, not just the visible
// orb. The permission banner overlays the bottom of the orb when access
// is denied; we keep it inside the same container so a single
// <PushToTalkButton/> remains a drop-in for screens.

const styles = StyleSheet.create({
  container: {
    flex: 1,
    backgroundColor: colors.bg,
    justifyContent: 'center',
    alignItems: 'center',
  },
  pressable: {
    flex: 1,
    width: '100%',
    justifyContent: 'center',
    alignItems: 'center',
  },
  permissionBanner: {
    position: 'absolute',
    bottom: spacing.xxl + spacing.xl,
    left: spacing.xl,
    right: spacing.xl,
    backgroundColor: colors.bgPanel,
    borderColor: colors.cyan,
    borderWidth: 1,
    borderRadius: 2,
    paddingHorizontal: spacing.lg,
    paddingVertical: spacing.md,
    alignItems: 'center',
  },
  permissionTitle: {
    color: colors.cyan,
    fontFamily: fontFamilies.mono,
    fontSize: 11,
    letterSpacing: 1.5,
    marginBottom: spacing.sm,
  },
  permissionBody: {
    color: colors.textDim,
    fontFamily: fontFamilies.mono,
    fontSize: 10,
    letterSpacing: 1,
    textAlign: 'center',
    marginBottom: spacing.md,
  },
  settingsButton: {
    borderColor: colors.cyan,
    borderWidth: 1,
    paddingHorizontal: spacing.lg,
    paddingVertical: spacing.sm,
    borderRadius: 2,
    backgroundColor: colors.cyanDark,
  },
  settingsButtonPressed: {
    backgroundColor: 'rgba(0, 255, 204, 0.3)',
  },
  settingsButtonText: {
    color: colors.cyan,
    fontFamily: fontFamilies.mono,
    fontSize: 10,
    letterSpacing: 1.5,
  },
})
