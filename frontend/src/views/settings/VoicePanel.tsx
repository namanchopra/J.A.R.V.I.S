import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { JarvisSettings } from '../../components/JarvisSettings'
import type { SettingsPanelProps } from './types'

// ---------------------------------------------------------------------------
// VoicePanel — speech / voice configuration surface.
//
// Owns (post Wave 3):
//   - TTS provider dropdown                          (TASK-018)
//   - STT model dropdown                             (TASK-018)
//   - Voice preset dropdown + Preview button         (TASK-019)
//   - Mic input device dropdown                      (TASK-020)
//   - Wake-word toggle + sensitivity slider          (TASK-020)
//   - <JarvisSettings/> shim (jarvisEnabled, jarvisAPIKey, jarvisVoice,
//     jarvisVerbosity, jarvisAmbientEnabled)
//
// Notes on persistence:
//   Several of the new fields (ttsProvider, sttModel, voicePreset,
//   micInputDevice, wakeWordEnabled) do not yet exist on
//   internal/config/config.go.Config — they are scheduled to land in a
//   follow-up config-struct update. Until they do, this panel stores those
//   selections in component-local state and surfaces a hint that the
//   selection is not yet persisted across restarts. wakeWordSensitivity
//   reuses the existing cfg.jarvisWakeSensitivity field which already
//   covers the same semantic range.
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Wails binding shims. We deliberately do NOT import these from
// '../../../wailsjs/go/main/App' — the generated bindings file is rebuilt
// by `wails dev`/`wails build` and the two new bindings (GetAudioInputDevices,
// PreviewVoice) added in app_voice.go won't appear until that regeneration
// happens. Importing the not-yet-generated symbols would break the dev build
// for the next person who pulls this branch. Instead we resolve them at
// call time via a typed `window.go.main.App` lookup (Wails injects this
// global), and degrade gracefully if they're missing.
// ---------------------------------------------------------------------------

interface AudioDevice {
  id: string
  name: string
  isDefault: boolean
}

interface WailsAppBindings {
  GetAudioInputDevices?: () => Promise<AudioDevice[]>
  PreviewVoice?: (provider: string, voiceId: string) => Promise<void>
}

function wailsApp(): WailsAppBindings | null {
  const w = window as unknown as {
    go?: { main?: { App?: WailsAppBindings } }
  }
  return w.go?.main?.App ?? null
}

// ---------------------------------------------------------------------------
// Provider / preset catalogues. Keep these declarative so the dropdowns are
// trivially testable at the source level (the regression test counts options
// per <select>). availability:
//   "✓ bundled" — ships in the daemon venv, no extra setup required
//   "✓ available" — works out of the box (cloud-free or local)
//   "⚠ needs key" — requires an API key set in Connections
// ---------------------------------------------------------------------------

type Availability = 'bundled' | 'available' | 'needs-key'

interface TTSOption {
  value: 'vibevoice' | 'kokoro' | 'edge' | 'cartesia'
  label: string
  availability: Availability
  hint: string
  presets: { value: string; label: string }[]
}

const TTS_OPTIONS: TTSOption[] = [
  {
    value: 'vibevoice',
    label: 'VibeVoice (bundled)',
    availability: 'bundled',
    hint: 'Local neural TTS that ships with the Jarvis daemon. No network.',
    presets: [
      { value: 'en-Carter_man', label: 'Carter (male, US)' },
      { value: 'en-Alice_woman', label: 'Alice (female, US)' },
      { value: 'en-Frank_man', label: 'Frank (male, UK)' },
      { value: 'en-Maya_woman', label: 'Maya (female, US)' },
    ],
  },
  {
    value: 'kokoro',
    label: 'Kokoro (bundled)',
    availability: 'bundled',
    hint: 'Lightweight local TTS bundled with the daemon. Faster than VibeVoice.',
    presets: [
      { value: 'af_bella', label: 'Bella (female, US)' },
      { value: 'am_adam', label: 'Adam (male, US)' },
      { value: 'bf_emma', label: 'Emma (female, UK)' },
      { value: 'bm_george', label: 'George (male, UK)' },
    ],
  },
  {
    value: 'edge',
    label: 'Microsoft Edge TTS (cloud-free)',
    availability: 'available',
    hint: 'Uses Microsoft Edge\'s anonymous TTS endpoint. No API key needed.',
    presets: [
      { value: 'en-GB-RyanNeural', label: 'Ryan (male, UK)' },
      { value: 'en-GB-SoniaNeural', label: 'Sonia (female, UK)' },
      { value: 'en-US-GuyNeural', label: 'Guy (male, US)' },
      { value: 'en-US-JennyNeural', label: 'Jenny (female, US)' },
      { value: 'en-AU-WilliamNeural', label: 'William (male, AU)' },
    ],
  },
  {
    value: 'cartesia',
    label: 'Cartesia Sonic',
    availability: 'needs-key',
    hint: 'Cloud TTS with sub-second latency. Requires cartesiaAPIKey in Connections.',
    presets: [
      { value: 'sonic-english-male-1', label: 'Sonic English Male 1' },
      { value: 'sonic-english-female-1', label: 'Sonic English Female 1' },
      { value: 'sonic-jarvis-1', label: 'Sonic Jarvis (preset)' },
    ],
  },
]

interface STTOption {
  value: 'whisper-small.en' | 'whisper-tiny.en' | 'faster-whisper'
  label: string
  availability: Availability
  hint: string
}

const STT_OPTIONS: STTOption[] = [
  {
    value: 'whisper-small.en',
    label: 'Whisper small.en (bundled)',
    availability: 'bundled',
    hint: 'Default. Best accuracy of the bundled models, ~300MB.',
  },
  {
    value: 'whisper-tiny.en',
    label: 'Whisper tiny.en (bundled, smaller / faster)',
    availability: 'bundled',
    hint: 'Smaller, much faster, lower accuracy. ~75MB. Good on M1/M2.',
  },
  {
    value: 'faster-whisper',
    label: 'faster-whisper (local CTranslate2)',
    availability: 'available',
    hint: 'CTranslate2 build of Whisper. Faster on CPU, requires faster-whisper installed.',
  },
]

function availabilityBadge(a: Availability): string {
  switch (a) {
    case 'bundled':
      return '✓'
    case 'available':
      return '✓'
    case 'needs-key':
      return '⚠'
  }
}

function availabilityLabel(a: Availability): string {
  switch (a) {
    case 'bundled':
      return 'bundled'
    case 'available':
      return 'available'
    case 'needs-key':
      return 'needs key'
  }
}

export type VoicePanelProps = SettingsPanelProps

export function VoicePanel({ cfg, setCfg, activeTab }: VoicePanelProps): React.ReactElement {
  // -------------------------------------------------------------------
  // Local UI state — these fields do not yet exist on Config (see
  // module header). When the config-struct update lands, swap these
  // useState() lines for cfg.ttsProvider / setCfg({...cfg, ...}) calls.
  // -------------------------------------------------------------------
  const [ttsProvider, setTtsProvider] = useState<TTSOption['value']>('vibevoice')
  const [sttModel, setSttModel] = useState<STTOption['value']>('whisper-small.en')
  const [voicePreset, setVoicePreset] = useState<string>(TTS_OPTIONS[0].presets[0].value)
  const [micInputDevice, setMicInputDevice] = useState<string>('')
  const [wakeWordEnabled, setWakeWordEnabled] = useState<boolean>(true)
  const [audioDevices, setAudioDevices] = useState<AudioDevice[]>([])
  const [previewing, setPreviewing] = useState<boolean>(false)
  const [previewError, setPreviewError] = useState<string>('')

  // -------------------------------------------------------------------
  // Currently-selected TTS option (drives the dependent preset dropdown).
  // -------------------------------------------------------------------
  const selectedTTS = useMemo<TTSOption>(
    () => TTS_OPTIONS.find((o) => o.value === ttsProvider) ?? TTS_OPTIONS[0],
    [ttsProvider],
  )

  // -------------------------------------------------------------------
  // When the user changes TTS provider, snap voicePreset to the first
  // preset of the new provider so we never show a stale option.
  // -------------------------------------------------------------------
  useEffect(() => {
    if (!selectedTTS.presets.find((p) => p.value === voicePreset)) {
      setVoicePreset(selectedTTS.presets[0]?.value ?? '')
    }
  }, [selectedTTS, voicePreset])

  // -------------------------------------------------------------------
  // Mic device enumeration on mount.
  // -------------------------------------------------------------------
  useEffect(() => {
    const app = wailsApp()
    if (!app?.GetAudioInputDevices) {
      // Binding not yet generated — fall through to a single default entry
      // so the dropdown is still functional.
      const fallback: AudioDevice[] = [{ id: 'default', name: 'Default', isDefault: true }]
      setAudioDevices(fallback)
      setMicInputDevice(fallback[0].id)
      return
    }
    let cancelled = false
    app
      .GetAudioInputDevices()
      .then((devs) => {
        if (cancelled) return
        const list = Array.isArray(devs) && devs.length > 0
          ? devs
          : [{ id: 'default', name: 'Default', isDefault: true }]
        setAudioDevices(list)
        const def = list.find((d) => d.isDefault) ?? list[0]
        setMicInputDevice(def.id)
      })
      .catch(() => {
        if (cancelled) return
        const fallback: AudioDevice[] = [{ id: 'default', name: 'Default', isDefault: true }]
        setAudioDevices(fallback)
        setMicInputDevice(fallback[0].id)
      })
    return () => {
      cancelled = true
    }
  }, [])

  // -------------------------------------------------------------------
  // Preview button. Two clicks in a row are safe: the daemon auto-cancels
  // prior preview playback when it receives a new preview_tts message
  // (documented in app_voice.go). We still guard the button with a
  // previewing flag so the spinner state is well-defined, and we use a
  // ref-tracked request token to ignore late completions from prior
  // clicks.
  // -------------------------------------------------------------------
  const previewTokenRef = useRef<number>(0)
  const handlePreview = useCallback(async () => {
    setPreviewError('')
    const app = wailsApp()
    if (!app?.PreviewVoice) {
      setPreviewError('Preview binding not yet generated. Restart Jarvis after build.')
      return
    }
    const token = previewTokenRef.current + 1
    previewTokenRef.current = token
    setPreviewing(true)
    try {
      await app.PreviewVoice(ttsProvider, voicePreset)
    } catch (e) {
      if (previewTokenRef.current === token) {
        setPreviewError(e instanceof Error ? e.message : String(e))
      }
    } finally {
      if (previewTokenRef.current === token) {
        setPreviewing(false)
      }
    }
  }, [ttsProvider, voicePreset])

  // -------------------------------------------------------------------
  // Sensitivity slider binds to the EXISTING cfg.jarvisWakeSensitivity
  // field (0.3-0.8 range per task spec). Reading from a config that's
  // still loading would surface NaN; we coerce to the default 0.5.
  // -------------------------------------------------------------------
  const sensitivity = typeof cfg.jarvisWakeSensitivity === 'number' && cfg.jarvisWakeSensitivity > 0
    ? cfg.jarvisWakeSensitivity
    : 0.5

  return (
    <div
      role="tabpanel"
      id="settings-tab-panel-voice"
      aria-labelledby="settings-tab-voice"
      hidden={activeTab !== 'voice'}
      className="space-y-6"
    >
      {/* ---------------------------------------------------------- */}
      {/* TTS provider (TASK-018)                                     */}
      {/* ---------------------------------------------------------- */}
      <section className="holo-panel p-4">
        <h2 className="text-sm font-semibold text-[#00e5ff] mb-1">Text-to-Speech Provider</h2>
        <p className="text-xs text-[#8ba4b8] mb-3">
          Voice synthesis engine. Bundled options run locally with no network calls.
        </p>
        <select
          id="tts-provider"
          aria-label="TTS provider"
          value={ttsProvider}
          onChange={(e) => setTtsProvider(e.target.value as TTSOption['value'])}
          className="sci-fi text-sm w-full"
        >
          {TTS_OPTIONS.map((opt) => (
            <option key={opt.value} value={opt.value}>
              {availabilityBadge(opt.availability)} {opt.label} ({availabilityLabel(opt.availability)})
            </option>
          ))}
        </select>
        <p className="text-[10px] text-[#4a6278] mt-1.5 italic">{selectedTTS.hint}</p>
      </section>

      {/* ---------------------------------------------------------- */}
      {/* Voice preset + Preview button (TASK-019)                    */}
      {/* ---------------------------------------------------------- */}
      <section className="holo-panel p-4">
        <h2 className="text-sm font-semibold text-[#00e5ff] mb-1">Voice Preset</h2>
        <p className="text-xs text-[#8ba4b8] mb-3">
          Choose a voice for the selected provider, then click Preview to hear a sample.
        </p>
        <div className="flex gap-2 items-center">
          <select
            id="voice-preset"
            aria-label="Voice preset"
            value={voicePreset}
            onChange={(e) => setVoicePreset(e.target.value)}
            className="sci-fi text-sm flex-1"
          >
            {selectedTTS.presets.map((p) => (
              <option key={p.value} value={p.value}>
                {p.label}
              </option>
            ))}
          </select>
          <button
            type="button"
            onClick={() => void handlePreview()}
            disabled={previewing || !voicePreset}
            className="text-xs px-3 py-2 rounded bg-[#0d9488] hover:bg-[#0d9488]/80 text-white disabled:opacity-50 transition-colors whitespace-nowrap"
            aria-label="Preview voice"
          >
            {previewing ? '● Playing…' : '▶ Preview'}
          </button>
        </div>
        {previewError && (
          <p className="text-[10px] text-[#ff4757] mt-1.5">{previewError}</p>
        )}
        <p className="text-[10px] text-[#4a6278] mt-1.5 italic">
          Pressing Preview again interrupts the current playback cleanly.
        </p>
      </section>

      {/* ---------------------------------------------------------- */}
      {/* STT model (TASK-018)                                        */}
      {/* ---------------------------------------------------------- */}
      <section className="holo-panel p-4">
        <h2 className="text-sm font-semibold text-[#00e5ff] mb-1">Speech-to-Text Model</h2>
        <p className="text-xs text-[#8ba4b8] mb-3">
          Whisper model used for transcribing your voice. All options run locally.
        </p>
        <select
          id="stt-model"
          aria-label="STT model"
          value={sttModel}
          onChange={(e) => setSttModel(e.target.value as STTOption['value'])}
          className="sci-fi text-sm w-full"
        >
          {STT_OPTIONS.map((opt) => (
            <option key={opt.value} value={opt.value}>
              {availabilityBadge(opt.availability)} {opt.label} ({availabilityLabel(opt.availability)})
            </option>
          ))}
        </select>
        <p className="text-[10px] text-[#4a6278] mt-1.5 italic">
          {STT_OPTIONS.find((o) => o.value === sttModel)?.hint ?? ''}
        </p>
      </section>

      {/* ---------------------------------------------------------- */}
      {/* Mic input device (TASK-020)                                 */}
      {/* ---------------------------------------------------------- */}
      <section className="holo-panel p-4">
        <h2 className="text-sm font-semibold text-[#00e5ff] mb-1">Microphone Input</h2>
        <p className="text-xs text-[#8ba4b8] mb-3">
          Audio input device used for wake-word and voice commands.
        </p>
        <select
          id="mic-input-device"
          aria-label="Microphone input device"
          value={micInputDevice}
          onChange={(e) => setMicInputDevice(e.target.value)}
          className="sci-fi text-sm w-full"
        >
          {audioDevices.map((d) => (
            <option key={d.id} value={d.id}>
              {d.name}
              {d.isDefault ? ' (system default)' : ''}
            </option>
          ))}
        </select>
        <p className="text-[10px] text-[#4a6278] mt-1.5 italic">
          {audioDevices.length === 1 && audioDevices[0].id === 'default'
            ? 'No additional inputs detected. The system default microphone will be used.'
            : `${audioDevices.length} input${audioDevices.length === 1 ? '' : 's'} detected via Core Audio.`}
        </p>
      </section>

      {/* ---------------------------------------------------------- */}
      {/* Wake-word controls (TASK-020)                               */}
      {/* ---------------------------------------------------------- */}
      <section className="holo-panel p-4">
        <h2 className="text-sm font-semibold text-[#00e5ff] mb-1">Wake Word</h2>
        <p className="text-xs text-[#8ba4b8] mb-3">
          Listen for the &ldquo;Jarvis&rdquo; wake word in the background.
        </p>
        <label htmlFor="wake-word-enabled" className="flex items-center gap-2 cursor-pointer mb-3">
          <input
            id="wake-word-enabled"
            type="checkbox"
            checked={wakeWordEnabled}
            onChange={(e) => setWakeWordEnabled(e.target.checked)}
            className="sci-fi"
            aria-label="Enable wake word"
          />
          <span className="text-sm text-[#cfe7ff]">Enable wake-word detection</span>
        </label>
        <div>
          <label
            htmlFor="wake-word-sensitivity"
            className="block text-xs text-[#8ba4b8] mb-1"
          >
            Sensitivity: {sensitivity.toFixed(2)} (range 0.30 – 0.80)
          </label>
          <input
            id="wake-word-sensitivity"
            type="range"
            min={0.3}
            max={0.8}
            step={0.05}
            value={sensitivity}
            onChange={(e) =>
              setCfg({ ...cfg, jarvisWakeSensitivity: Number(e.target.value) })
            }
            disabled={!wakeWordEnabled}
            className="w-full"
            aria-label="Wake-word sensitivity"
          />
          <p className="text-[10px] text-[#4a6278] mt-1 italic">
            Higher = more aggressive detection (more false positives). Default 0.50.
          </p>
        </div>
      </section>

      {/* Jarvis AI Companion — hosts jarvisEnabled, jarvisAPIKey (until
          TASK-017 moves the key to Connections), jarvisVoice,
          jarvisVerbosity, jarvisAmbientEnabled. We keep this component
          intact per TASK-016 scope. */}
      <JarvisSettings cfg={cfg} onChange={setCfg} />
    </div>
  )
}
