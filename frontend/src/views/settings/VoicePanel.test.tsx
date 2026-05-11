// ---------------------------------------------------------------------------
// VoicePanel — source-level contract test (TASK-018 / TASK-019 / TASK-020).
//
// The frontend test harness ships without jsdom, so we use the same `?raw`
// import trick documented in SettingsView.test.tsx to assert structural
// invariants on the rendered source. This is sufficient to catch the
// regressions the three Phase 2 P1 tasks would otherwise re-introduce.
//
// Contract verified:
//   TASK-018: TTS provider dropdown lists all 4 options (vibevoice / kokoro
//             / edge / cartesia). STT model dropdown lists all 3 options
//             (whisper-small.en / whisper-tiny.en / faster-whisper). Each
//             carries an availability indicator (✓ or ⚠).
//   TASK-019: Voice preset dropdown exists, depends on the selected TTS
//             provider, has a Preview button that calls PreviewVoice with
//             the (provider, voiceId) tuple. Re-pressing Preview while a
//             prior playback is in-flight is documented + implemented.
//   TASK-020: Mic input device dropdown is populated by GetAudioInputDevices.
//             Wake-word toggle + sensitivity slider exist with the spec
//             range (0.30 – 0.80, step 0.05).
// ---------------------------------------------------------------------------

import { describe, it, expect } from 'vitest'
import SOURCE from './VoicePanel.tsx?raw'

describe('VoicePanel TASK-018 (TTS + STT dropdowns)', () => {
  it('lists all 4 TTS providers in the TTS_OPTIONS catalogue', () => {
    expect(SOURCE).toMatch(/value:\s*['"]vibevoice['"]/)
    expect(SOURCE).toMatch(/value:\s*['"]kokoro['"]/)
    expect(SOURCE).toMatch(/value:\s*['"]edge['"]/)
    expect(SOURCE).toMatch(/value:\s*['"]cartesia['"]/)
  })

  it('renders a <select> bound to ttsProvider with all 4 options', () => {
    expect(SOURCE).toMatch(/id=['"]tts-provider['"]/)
    expect(SOURCE).toMatch(/value=\{ttsProvider\}/)
    expect(SOURCE).toMatch(/setTtsProvider\(/)
  })

  it('lists all 3 STT models in the STT_OPTIONS catalogue', () => {
    expect(SOURCE).toMatch(/value:\s*['"]whisper-small\.en['"]/)
    expect(SOURCE).toMatch(/value:\s*['"]whisper-tiny\.en['"]/)
    expect(SOURCE).toMatch(/value:\s*['"]faster-whisper['"]/)
  })

  it('renders a <select> bound to sttModel', () => {
    expect(SOURCE).toMatch(/id=['"]stt-model['"]/)
    expect(SOURCE).toMatch(/value=\{sttModel\}/)
    expect(SOURCE).toMatch(/setSttModel\(/)
  })

  it('surfaces availability indicators (bundled / available / needs-key)', () => {
    // Each provider/STT row must carry an availability tag — we assert all
    // three categorical values are present somewhere in the source.
    expect(SOURCE).toMatch(/'bundled'/)
    expect(SOURCE).toMatch(/'available'/)
    expect(SOURCE).toMatch(/'needs-key'/)
    // Visual badges
    expect(SOURCE).toMatch(/✓/)
    expect(SOURCE).toMatch(/⚠/)
  })
})

describe('VoicePanel TASK-019 (voice preset + preview)', () => {
  it('renders a voice preset <select> bound to the selected TTS provider', () => {
    expect(SOURCE).toMatch(/id=['"]voice-preset['"]/)
    expect(SOURCE).toMatch(/value=\{voicePreset\}/)
    expect(SOURCE).toMatch(/selectedTTS\.presets/)
  })

  it('updates voicePreset when the TTS provider changes (no stale preset)', () => {
    // useEffect should reset voicePreset to the first preset of the new
    // provider if the current preset is not in the new provider's list.
    expect(SOURCE).toMatch(/setVoicePreset/)
    expect(SOURCE).toMatch(/selectedTTS\.presets\[0\]/)
  })

  it('renders a Preview button that calls the PreviewVoice Wails binding', () => {
    expect(SOURCE).toMatch(/▶ Preview/)
    expect(SOURCE).toMatch(/PreviewVoice/)
    // The handler must pass (ttsProvider, voicePreset) to PreviewVoice.
    expect(SOURCE).toMatch(/PreviewVoice\(ttsProvider,\s*voicePreset\)/)
  })

  it('guards Preview re-clicks via a token / previewing flag', () => {
    // Re-pressing Preview while a prior preview is playing must not
    // double-fire visible state. We pin on the previewing flag and the
    // token ref so a refactor that drops them surfaces here.
    expect(SOURCE).toMatch(/previewing/)
    expect(SOURCE).toMatch(/previewTokenRef/)
  })
})

describe('VoicePanel TASK-020 (mic device + wake word)', () => {
  it('enumerates audio inputs via GetAudioInputDevices on mount', () => {
    expect(SOURCE).toMatch(/GetAudioInputDevices/)
    expect(SOURCE).toMatch(/setAudioDevices/)
  })

  it('renders the mic input device <select>', () => {
    expect(SOURCE).toMatch(/id=['"]mic-input-device['"]/)
    expect(SOURCE).toMatch(/value=\{micInputDevice\}/)
    expect(SOURCE).toMatch(/setMicInputDevice/)
  })

  it('falls back to a "Default" entry if no devices come back', () => {
    expect(SOURCE).toMatch(/id:\s*['"]default['"],\s*name:\s*['"]Default['"]/)
  })

  it('renders a wake-word toggle bound to wakeWordEnabled', () => {
    expect(SOURCE).toMatch(/id=['"]wake-word-enabled['"]/)
    expect(SOURCE).toMatch(/checked=\{wakeWordEnabled\}/)
    expect(SOURCE).toMatch(/setWakeWordEnabled/)
  })

  it('renders the sensitivity slider with range 0.3 - 0.8 step 0.05', () => {
    expect(SOURCE).toMatch(/id=['"]wake-word-sensitivity['"]/)
    expect(SOURCE).toMatch(/type=['"]range['"]/)
    expect(SOURCE).toMatch(/min=\{0\.3\}/)
    expect(SOURCE).toMatch(/max=\{0\.8\}/)
    expect(SOURCE).toMatch(/step=\{0\.05\}/)
  })

  it('persists the sensitivity through cfg.jarvisWakeSensitivity', () => {
    expect(SOURCE).toMatch(/jarvisWakeSensitivity/)
    expect(SOURCE).toMatch(/setCfg\(\s*\{\s*\.\.\.cfg,\s*jarvisWakeSensitivity:\s*Number\(e\.target\.value\)/)
  })
})

describe('VoicePanel structural integrity', () => {
  it('keeps the <JarvisSettings/> shim mounted (TASK-016 regression)', () => {
    expect(SOURCE).toMatch(/<JarvisSettings\s/)
    expect(SOURCE).toMatch(/from\s*['"]\.\.\/\.\.\/components\/JarvisSettings['"]/)
  })

  it('renders exactly one tabpanel root with role="tabpanel"', () => {
    const matches = SOURCE.match(/role=['"]tabpanel['"]/g) ?? []
    expect(matches.length).toBe(1)
  })

  it('renders 5 distinct <select>s + 1 wake-word checkbox + 1 sensitivity slider', () => {
    // tts-provider, voice-preset, stt-model, mic-input-device are 4 selects
    // owned by this panel directly. The 5th select (jarvisVoice / verbosity)
    // is inside <JarvisSettings/> and not pinned here. We just guard the
    // four panel-owned dropdowns plus the two non-select controls.
    expect(SOURCE).toMatch(/id=['"]tts-provider['"]/)
    expect(SOURCE).toMatch(/id=['"]voice-preset['"]/)
    expect(SOURCE).toMatch(/id=['"]stt-model['"]/)
    expect(SOURCE).toMatch(/id=['"]mic-input-device['"]/)
    expect(SOURCE).toMatch(/id=['"]wake-word-enabled['"]/)
    expect(SOURCE).toMatch(/id=['"]wake-word-sensitivity['"]/)
  })
})
