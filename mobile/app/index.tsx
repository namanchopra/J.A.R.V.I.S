// v0.3.0: orb-first root route. TASK-023+ wires the press-to-talk loop
// through the Mac daemon via the existing JarvisWS singleton.
//
// Flow (this file is the single source of truth for the wiring):
//
//   1. On mount we lazily build ONE JarvisWS instance, ask it to `.connect()`,
//      and keep its event subscriptions live for the screen's lifetime.
//      `loadPairing()` is consulted indirectly by `JarvisWS.connect()` -- if
//      no pairing exists the WS no-ops cleanly so the orb still renders.
//
//   2. PushToTalkButton:
//        onPressStart  -> ws.sendJSON({type:'audio_start'})
//        onAudioChunk  -> ws.sendAudio(bytes)              (binary frame)
//        onPressEnd    -> ws.sendJSON({type:'audio_end'})
//      The daemon's /ws/jarvis-mobile handler accepts these and forwards to
//      Pipecat (see internal/api/handlers_jarvis_mobile_ws.go).
//
//   3. Incoming frames:
//        'state'       (transport)  -> drives the OFFLINE chrome badge
//        'stateChange' (daemon)     -> drives HudStateBar phase
//        'transcript'              -> drives TranscriptChip user/assistant text
//        'ttsAudioChunk'           -> appended to AudioPlayer playback queue
//        'ttsAudioLevel'           -> drives OrbView.audioLevel (speaking pulse)
//
//   4. HUD chrome panels (HudStateBar, TranscriptChip, SessionBadge) are
//      rendered around the orb. Their full visual treatment lives in
//      mobile/components/{HudStateBar,TranscriptChip,SessionBadge}.tsx --
//      this file feeds them the live data extracted from the WS event bus.
//
//   5. Old gear (top-right) stays for now -- the SessionBadge top-left is a
//      stats chip, the gear is the Settings entry point. Both coexist until
//      a future task collapses them.
//
//   6. Session count / hasApprovals: the daemon does NOT currently emit a
//      'context' frame containing sessions[] (see handlers_jarvis_mobile_ws.go
//      -- only response/state/transcript/mobile_tts are forwarded). The
//      sessionCount/hasApprovals state is wired through to SessionBadge so a
//      future daemon-side enhancement that broadcasts session context maps
//      directly into this UI without touching the render tree.

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  Pressable,
  StyleSheet,
  Text,
  View,
} from 'react-native';
import { useRouter } from 'expo-router';
import { useSafeAreaInsets } from 'react-native-safe-area-context';

import { AudioPlayer } from '../lib/audio-playback';
import { CalendarChip } from '../components/CalendarChip';
import { HudStateBar } from '../components/HudStateBar';
import { PushToTalkButton } from '../components/PushToTalkButton';
import { SessionBadge } from '../components/SessionBadge';
import { StatCard } from '../components/StatCard';
import { TranscriptChip } from '../components/TranscriptChip';
import { colors, fontFamilies, spacing } from '../lib/hud-tokens';
import { JarvisWS, type WSState } from '../lib/jarvis-ws';
import { useFridayDashboard } from '../lib/use-friday-dashboard';

// ---- HUD daemon phases ----------------------------------------------------
// The Pipecat daemon emits one of these in `state_change.phase`. We default
// to 'idle' until the first event arrives so the HUD bar never shows an
// empty string.

type HudPhase = 'idle' | 'listening' | 'thinking' | 'speaking';

const KNOWN_PHASES: ReadonlyArray<HudPhase> = [
  'idle',
  'listening',
  'thinking',
  'speaking',
];

function coercePhase(raw: string | undefined): HudPhase {
  if (raw && (KNOWN_PHASES as ReadonlyArray<string>).includes(raw)) {
    return raw as HudPhase;
  }
  return 'idle';
}

function isImminent(relativeTime: string): boolean {
  // Server formats: "now" | "in 14m" | "in 2h" | "in 1d" | "Mon Jan 2".
  // We treat <5 minutes as imminent, so "warn" accent fires on "now" or
  // "in 1m"-"in 4m". String parsing only -- no Date math, since the
  // server already computed the relative time.
  if (relativeTime === 'now') return true;
  const match = /^in (\d+)m$/.exec(relativeTime);
  if (!match) return false;
  const minutes = parseInt(match[1], 10);
  return Number.isFinite(minutes) && minutes < 5;
}

// MODULE-LEVEL SINGLETONS
// React StrictMode (and Expo's fast refresh) intentionally double-invokes
// useEffect callbacks during development to catch unsafe side effects. If
// we new'd JarvisWS / AudioPlayer inside the effect, the double-mount
// would open two WebSocket connections to the daemon AND the cleanup
// (ws.close()) would race the second connect, leaving both sockets alive
// for a few hundred ms before each tears down -- which is exactly what
// the duplicate `mobile client connected` lines in the wails log were
// showing. We promote both to module-level lazy singletons so all mounts
// share the same instance; the effect now just adds/removes listeners.
let sharedWs: JarvisWS | null = null;
let sharedPlayer: AudioPlayer | null = null;
let sharedPlayerSampleRate = 16000;

function getSharedWs(): JarvisWS {
  if (sharedWs === null) sharedWs = new JarvisWS();
  return sharedWs;
}

function getSharedPlayer(sampleRate?: number): AudioPlayer {
  const sr = sampleRate ?? sharedPlayerSampleRate;
  if (sharedPlayer === null) {
    sharedPlayer = new AudioPlayer({ sampleRate: sr });
    sharedPlayerSampleRate = sr;
  }
  return sharedPlayer;
}

export default function FridayRoot() {
  const router = useRouter();
  const insets = useSafeAreaInsets();

  // wsRef / playerRef now just point at the module-level singletons so
  // press-handler callbacks have an identical API to before. The actual
  // construction happens at most once per page-load lifecycle, regardless
  // of how many times this effect re-runs under StrictMode.
  const wsRef = useRef<JarvisWS | null>(null);
  const playerRef = useRef<AudioPlayer | null>(null);
  const playerSampleRateRef = useRef<number>(sharedPlayerSampleRate);

  // ---- Live state fed to HUD panels -----------------------------------
  const [wsState, setWsState] = useState<WSState>('disconnected');
  const [phase, setPhase] = useState<HudPhase>('idle');
  const [userText, setUserText] = useState<string | null>(null);
  const [assistantText, setAssistantText] = useState<string | null>(null);
  const [sessionCount, setSessionCount] = useState<number>(0);
  const [hasApprovals, setHasApprovals] = useState<boolean>(false);
  const [ttsLevel, setTtsLevel] = useState<number>(0);

  // ---- Mac HUD parity stats ------------------------------------------
  // The Go server pushes a stats_snapshot WS event every ~5s; we subscribe
  // here rather than REST-poll because Expo Go's iOS ATS blocks plain
  // http:// fetches even when WS to the same host is fine. Defaults to
  // zeros on first render, fills in within ~5s of connect.
  const dashboard = useFridayDashboard({ ws: getSharedWs() });

  useEffect(() => {
    const ws = getSharedWs();
    wsRef.current = ws;
    const player = getSharedPlayer(playerSampleRateRef.current);
    playerRef.current = player;

    // Wire all subscribers BEFORE connect() so the very first 'state'
    // emission (transitioning to 'connecting') is captured.
    const unsubs: Array<() => void> = [];

    unsubs.push(
      ws.on('state', (s) => {
        setWsState(s);
      }),
    );

    unsubs.push(
      ws.on('stateChange', (payload) => {
        setPhase(coercePhase(payload.phase));
      }),
    );

    unsubs.push(
      ws.on('transcript', (payload) => {
        if (payload.role === 'user') {
          setUserText(payload.text);
        } else {
          setAssistantText(payload.text);
        }
      }),
    );

    unsubs.push(
      ws.on('ttsAudioLevel', (level) => {
        // Clamp into [0..1] -- the daemon should already do this but we
        // belt-and-brace the OrbView so a bad emit can't break layout.
        setTtsLevel(Math.max(0, Math.min(1, level)));
      }),
    );

    let ttsChunkCount = 0;
    unsubs.push(
      ws.on('ttsAudioChunk', (chunk, sampleRate) => {
        ttsChunkCount++;
        if (ttsChunkCount === 1 || ttsChunkCount === 25 || ttsChunkCount === 100) {
          console.log('[FridayRoot] ttsAudioChunk', {
            count: ttsChunkCount,
            bytes: chunk.byteLength,
            sampleRate,
          });
        }
        // If the daemon advertises a sample rate that differs from our
        // current player, rebuild the player so WAV headers match the
        // wire bytes. Pitch is otherwise wrong (e.g. 16k bytes played
        // by a 24k-configured WAV header sound chipmunk-y). We discard
        // any in-flight buffer on the swap -- the new rate marks a new
        // TTS session boundary in practice.
        const currentSr = playerSampleRateRef.current;
        const wireSr = typeof sampleRate === 'number' && sampleRate > 0
          ? sampleRate
          : currentSr;
        let activePlayer = playerRef.current;
        if (wireSr !== currentSr && activePlayer) {
          activePlayer.stop().catch(() => {});
          const next = new AudioPlayer({ sampleRate: wireSr });
          // Update both refs and the module-level singleton so the next
          // mount picks up the new player at the new rate.
          sharedPlayer = next;
          sharedPlayerSampleRate = wireSr;
          playerRef.current = next;
          playerSampleRateRef.current = wireSr;
          next.start().catch((err) => {
            console.warn('FridayRoot: AudioPlayer.start (rate swap) failed', err);
          });
          activePlayer = next;
        }
        if (!activePlayer) return;
        // AudioPlayer.append is fire-and-forget from our perspective --
        // it queues onto an internal playback chain. Swallow errors so
        // one bad chunk can't blow up the WS callback (which would tear
        // down the connection on next message).
        activePlayer.append(chunk).catch((err) => {
          console.warn('FridayRoot: AudioPlayer.append failed', err);
        });
      }),
    );

    unsubs.push(
      ws.on('error', (err) => {
        console.warn('FridayRoot: WS error', err.message);
      }),
    );

    // Kick the audio session into a recording-friendly mode so the orb's
    // mic and the daemon's TTS can coexist. start() is idempotent.
    player.start().catch((err) => {
      console.warn('FridayRoot: AudioPlayer.start failed', err);
    });

    // connect() is idempotent w.r.t. an already-open socket -- it only
    // opens a new one if we're disconnected. Safe to call on every mount
    // (including StrictMode's double-mount).
    ws.connect().catch((err) => {
      console.warn('FridayRoot: WS connect failed', err.message);
    });

    return () => {
      // CRITICAL: do NOT disconnect the shared ws or stop the shared
      // player on cleanup. StrictMode/Fast Refresh fires this between
      // back-to-back mounts; calling ws.disconnect() here would race the
      // remount's ws.connect() and produce the dual-connection +
      // backoff-flapping we saw in the wails log. The connection lives
      // for the lifetime of the JS bundle; navigating away from the orb
      // screen still keeps the WS alive so reconnect costs aren't paid
      // per-navigation. Just drop subscriptions.
      for (const u of unsubs) u();
      // Refs are owned by this render; the singleton stays.
      wsRef.current = null;
      playerRef.current = null;
    };
  }, []);

  // ---- PushToTalkButton wiring ----------------------------------------
  // Use refs in callbacks so the closures don't capture a stale `ws` if
  // the singleton ever gets recreated. Practically the singleton is built
  // once per screen mount, but the indirection is essentially free.

  const onPressStart = useCallback(async () => {
    const ws = wsRef.current;
    if (!ws) return;
    // Interrupt any TTS that's still in flight -- if the user is talking
    // over Jarvis, Jarvis should shut up immediately. Read from the ref
    // every time so a rate-swapped player still gets cycled correctly.
    const player = playerRef.current;
    if (player) {
      await player.stop().catch(() => {});
      await player.start().catch(() => {});
    }
    ws.sendJSON({ type: 'audio_start' });
  }, []);

  const onAudioChunk = useCallback((chunk: Uint8Array) => {
    const ws = wsRef.current;
    console.log('[FridayRoot] onAudioChunk', {
      bytes: chunk.byteLength,
      wsRefSet: Boolean(ws),
    });
    if (!ws) {
      console.warn('[FridayRoot] onAudioChunk dropped -- wsRef.current is null');
      return;
    }
    ws.sendAudio(chunk);
  }, []);

  const onPressEnd = useCallback(async () => {
    const ws = wsRef.current;
    if (!ws) return;
    ws.sendJSON({ type: 'audio_end' });
  }, []);

  // ---- Derived ----------------------------------------------------------
  // Surface the WS state as a single boolean for the OFFLINE badge. We
  // treat 'connecting' as offline (no frames flowing) but distinguish it
  // from 'error' for the badge label.
  const showOfflineBadge = wsState !== 'connected';
  const offlineLabel = useMemo(() => {
    switch (wsState) {
      case 'connecting':
        return 'CONNECTING…';
      case 'error':
        return 'OFFLINE · ERROR';
      case 'disconnected':
      default:
        return 'OFFLINE';
    }
  }, [wsState]);

  // ---- Render -----------------------------------------------------------
  // Stacking order (bottom -> top):
  //   1. PushToTalkButton (fills the screen, contains OrbView)
  //   2. HudStateBar      (top, below safe-area top)
  //   3. SessionBadge     (top-left, below HudStateBar)
  //   4. Gear             (top-right, settings entry)
  //   5. TranscriptChip   (bottom, above safe-area bottom)
  //   6. Offline badge    (top-center, only when disconnected)
  return (
    <View
      style={[
        styles.root,
        { paddingTop: insets.top, paddingBottom: insets.bottom },
      ]}
    >
      {/* PushToTalkButton fills the whole container -- it wraps OrbView and
          owns the press hit area. We blank out the four corner labels
          (LLM/STT/TTS/SESSIONS) because the new HUD panels carry that
          information more usefully. sessions={0} keeps the existing prop
          contract; CornerLabel inside OrbView renders the prefix + em-dash
          regardless, which Agent C's OrbView refresh will address. */}
      <PushToTalkButton
        sessions={0}
        llmLabel=""
        sttLabel=""
        ttsLabel=""
        audioLevel={ttsLevel}
        onPressStart={onPressStart}
        onAudioChunk={onAudioChunk}
        onPressEnd={onPressEnd}
      />

      {/* FRIDAY name banner -- prominent so the device identity is obvious
          (Mac side says "Jarvis", phone side says "Friday"). pointerEvents
          off so it never intercepts the orb tap area. */}
      <View style={[styles.nameBannerWrap, { top: insets.top + 6 }]} pointerEvents="none">
        <Text style={styles.nameBanner}>FRIDAY</Text>
      </View>

      {/* HUD daemon-phase bar pulled slightly down from the name banner. */}
      <View style={[styles.hudStateBarWrap, { top: insets.top + 36 }]} pointerEvents="none">
        <HudStateBar state={phase} />
      </View>

      {/* Calendar "next event" chip -- polls /calendar/next every 60s and
          renders null when no upcoming events, when Google Calendar is
          not connected on the Mac side, or when the network is down. */}
      <View style={[styles.calendarChipWrap, { top: insets.top + 60 }]} pointerEvents="none">
        <CalendarChip />
      </View>

      {/* Top stat row -- running tasks + approvals.
          ``runningTasks`` covers everything the scanner can see (sessions
          launched through Jarvis plus external Claude/codex/etc windows),
          which is the number a glance at the phone actually wants. The
          earlier "active sessions" card was always 0 on dev machines
          because nothing gets launched via Jarvis itself. */}
      <View
        style={[styles.statsRowTop, { top: insets.top + 76 }]}
        pointerEvents="box-none"
      >
        <StatCard
          value={dashboard.runningTasks}
          label="running"
          onPress={() => router.push('/settings')}
          testID="stat-running-tasks"
        />
        <View style={styles.statsGap} />
        <StatCard
          value={dashboard.pendingApprovals}
          label="pending"
          accent={dashboard.pendingApprovals > 0 ? 'warn' : 'default'}
          onPress={() => router.push('/settings')}
          testID="stat-pending-approvals"
        />
      </View>

      {/* Bottom stat row -- recent activity + today's event count. Sits just
          above the TranscriptChip with enough breathing room that the chip
          doesn't overlap on tall phones. */}
      <View
        style={[styles.statsRowBottom, { bottom: insets.bottom + 64 }]}
        pointerEvents="box-none"
      >
        <StatCard
          value={dashboard.latestActivity ? '•' : '—'}
          label="recent"
          caption={dashboard.latestActivity || 'no activity yet'}
          onPress={() => router.push('/settings')}
          testID="stat-latest-activity"
        />
        <View style={styles.statsGap} />
        {dashboard.nextEvent === null ? (
          <StatCard
            value="—"
            label="next event"
            caption="nothing soon"
            testID="stat-next-event"
          />
        ) : (
          <StatCard
            value={dashboard.nextEvent.relativeTime || '—'}
            label="next event"
            caption={dashboard.nextEvent.title}
            accent={isImminent(dashboard.nextEvent.relativeTime) ? 'warn' : 'default'}
            onPress={() => router.push('/settings')}
            testID="stat-next-event"
          />
        )}
      </View>

      {/* Session badge kept hidden for now -- the stat tiles carry the same
          info more usefully. Comment out rather than delete so we can bring
          it back if user feedback wants the compact corner pill again. */}
      {false ? (
        <View style={[styles.sessionBadgeWrap, { top: insets.top + 40 }]}>
          <SessionBadge
            count={sessionCount}
            hasApprovals={hasApprovals}
            onPress={() => router.push('/settings')}
          />
        </View>
      ) : null}

      {/* Gear settings entry (kept alongside SessionBadge for now) */}
      <Pressable
        onPress={() => router.push('/settings')}
        style={[styles.gear, { top: insets.top + 12 }]}
        hitSlop={8}
        testID="orb-settings-gear"
        accessibilityLabel="Open settings"
        accessibilityRole="button"
      >
        <Text style={styles.gearText}>⚙</Text>
      </Pressable>

      {/* Transcript chip at bottom */}
      <View
        style={[styles.transcriptChipWrap, { bottom: insets.bottom + 12 }]}
        pointerEvents="none"
      >
        <TranscriptChip userText={userText} assistantText={assistantText} />
      </View>

      {/* Offline badge -- only renders when WS is not connected. Sits
          centered just below the HudStateBar so it never overlaps the
          orb's hit area. */}
      {showOfflineBadge ? (
        <View
          style={[styles.offlineBadge, { top: insets.top + 76 }]}
          pointerEvents="none"
          testID="ws-offline-badge"
        >
          <Text style={styles.offlineText}>{offlineLabel}</Text>
        </View>
      ) : null}

      {/* Silence helper for sessionCount/hasApprovals setters that are
          currently never invoked -- once a 'context' frame is added to
          jarvis-ws.ts, these will be driven from there. Keeping them in
          state shape now means the prop wiring is already in place. */}
      <NoopSetters
        setSessionCount={setSessionCount}
        setHasApprovals={setHasApprovals}
      />
    </View>
  );
}

// NoopSetters is a microscopic helper that keeps the `setSessionCount` /
// `setHasApprovals` setters from being flagged as "unused" by strict TS,
// while still parking the wiring for when a `context` frame lands. It
// renders nothing.
function NoopSetters({
  setSessionCount: _setSessionCount,
  setHasApprovals: _setHasApprovals,
}: {
  setSessionCount: (n: number) => void;
  setHasApprovals: (b: boolean) => void;
}): null {
  return null;
}

// ---- Styles ---------------------------------------------------------------

const styles = StyleSheet.create({
  root: {
    flex: 1,
    backgroundColor: colors.bg,
  },
  gear: {
    position: 'absolute',
    right: 20,
    width: 32,
    height: 32,
    justifyContent: 'center',
    alignItems: 'center',
    zIndex: 30,
  },
  gearText: {
    fontFamily: fontFamilies.mono,
    fontSize: 18,
    color: colors.cyan,
    opacity: 0.3,
  },
  hudStateBarWrap: {
    position: 'absolute',
    left: 0,
    right: 0,
    alignItems: 'center',
    zIndex: 20,
  },
  calendarChipWrap: {
    position: 'absolute',
    left: 0,
    right: 0,
    alignItems: 'center',
    zIndex: 19,
  },
  nameBannerWrap: {
    position: 'absolute',
    left: 0,
    right: 0,
    alignItems: 'center',
    zIndex: 22,
  },
  nameBanner: {
    fontFamily: fontFamilies.mono,
    fontSize: 18,
    fontWeight: '700',
    letterSpacing: 6,
    color: colors.textPrimary,
  },
  // Stat-card rows. ``box-none`` on the wrapper lets taps fall through the
  // empty space between/around the cards to the orb beneath, while the
  // cards themselves still receive their own onPress.
  statsRowTop: {
    position: 'absolute',
    left: spacing.lg,
    right: spacing.lg,
    flexDirection: 'row',
    alignItems: 'stretch',
    zIndex: 25,
  },
  statsRowBottom: {
    position: 'absolute',
    left: spacing.lg,
    right: spacing.lg,
    flexDirection: 'row',
    alignItems: 'stretch',
    zIndex: 25,
  },
  statsGap: {
    width: spacing.md,
  },
  sessionBadgeWrap: {
    position: 'absolute',
    left: spacing.lg,
    zIndex: 25,
  },
  transcriptChipWrap: {
    position: 'absolute',
    left: spacing.xl,
    right: spacing.xl,
    alignItems: 'center',
    zIndex: 20,
  },
  offlineBadge: {
    position: 'absolute',
    alignSelf: 'center',
    left: 0,
    right: 0,
    alignItems: 'center',
    zIndex: 40,
  },
  offlineText: {
    fontFamily: fontFamilies.mono,
    fontSize: 10,
    letterSpacing: 1.5,
    color: colors.amber,
    backgroundColor: colors.bgPanel,
    borderColor: colors.amber,
    borderWidth: 1,
    paddingHorizontal: spacing.md,
    paddingVertical: spacing.xs,
    borderRadius: 2,
    overflow: 'hidden',
  },
});

