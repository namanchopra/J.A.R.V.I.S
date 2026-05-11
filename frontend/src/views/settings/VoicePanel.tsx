import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { JarvisSettings } from '../../components/JarvisSettings'
import type { SettingsPanelProps } from './types'
import type { config as cfgModels } from '../../../wailsjs/go/models'

// ---------------------------------------------------------------------------
// VoicePanel — speech / voice configuration surface.
//
// Owns (post v0.1.2):
//   - TTS provider dropdown                          (TASK-018)  → cfg.ttsProvider
//   - STT model dropdown                             (TASK-018)  → cfg.sttModel
//   - Voice preset dropdown + Preview button         (TASK-019)  → cfg.voicePreset
//   - Mic input device dropdown                      (TASK-020)  → cfg.micInputDevice
//   - Wake-word toggle + sensitivity slider          (TASK-020)  → cfg.wakeWordEnabled
//     (sensitivity continues to live on cfg.jarvisWakeSensitivity)
//   - <JarvisSettings/> shim (jarvisEnabled, jarvisAPIKey, jarvisVoice,
//     jarvisVerbosity, jarvisAmbientEnabled)
//
// Persistence (v0.1.2):
//   All five v0.1.2 fields now persist via cfg + SaveConfig. The Go agent's
//   parallel track adds these slots onto internal/config/config.go.Config and
//   the python daemon honours them on next load. The companion change in
//   SettingsView surfaces a daemon-restart-required banner when fields with
//   `daemonRestartNeeded === true` are saved, and exposes an Apply now
//   button that triggers RestartJarvis().
//
// Bindings note:
//   The generated wailsjs/go/models.ts has not been regenerated in this
//   sandbox (no `wails generate module`), so `Config` does not yet declare
//   the new fields. We read/write them through a small `VoiceConfig`
//   superset type — once `wails dev` runs against the Go agent's PR, the
//   superset becomes a no-op and the casts vanish at the next regen.
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
  value: 'vibevoice' | 'kokoro' | 'cartesia'
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

// ---------------------------------------------------------------------------
// VoiceConfig — superset of the generated `config.Config` that knows about
// the v0.1.2 fields. Once `wails generate module` runs against the Go
// agent's PR, these become declared properties on `config.Config` itself and
// this superset is redundant. We keep it here so the rest of the file uses
// proper field accesses (`vcfg.ttsProvider`) instead of dynamic string
// indexing — which keeps `noUncheckedIndexedAccess` happy.
// ---------------------------------------------------------------------------
type VoiceConfig = cfgModels.Config & {
  ttsProvider?: TTSOption['value']
  sttModel?: STTOption['value']
  voicePreset?: string
  micInputDevice?: string
  wakeWordEnabled?: boolean
}

export type VoicePanelProps = SettingsPanelProps

export function VoicePanel({ cfg, setCfg, activeTab }: VoicePanelProps): React.ReactElement {
  // -------------------------------------------------------------------
  // Cast cfg into the VoiceConfig superset so the new fields type-check
  // before `wails generate module` runs. The cast is type-only; at
  // runtime cfg is the same object.
  // -------------------------------------------------------------------
  const vcfg = cfg as VoiceConfig

  // Provider / model / preset / mic device / wake-word-enabled are now
  // sourced from cfg with sensible defaults when undefined.
  const ttsProvider: TTSOption['value'] = vcfg.ttsProvider ?? 'vibevoice'
  const sttModel: STTOption['value'] = vcfg.sttModel ?? 'whisper-small.en'
  const firstPreset = TTS_OPTIONS[0]?.presets[0]?.value ?? ''
  const voicePreset: string = vcfg.voicePreset ?? firstPreset
  const micInputDevice: string = vcfg.micInputDevice ?? ''
  // wakeWordEnabled defaults to true when undefined (the keyword behaviour
  // ships on by default — see CLAUDE.md / Config defaults).
  const wakeWordEnabled: boolean = vcfg.wakeWordEnabled ?? true

  const [audioDevices, setAudioDevices] = useState<AudioDevice[]>([])
  const [previewing, setPreviewing] = useState<boolean>(false)
  const [previewError, setPreviewError] = useState<string>('')

  // Single-call setters that merge one field into cfg via the parent's
  // setCfg. Keeping these named makes the test assertions readable and
  // makes the wiring resilient to property-order churn.
  const setTtsProvider = useCallback(
    (next: TTSOption['value']) => {
      setCfg({ ...(cfg as VoiceConfig), ttsProvider: next } as cfgModels.Config)
    },
    [cfg, setCfg],
  )
  const setSttModel = useCallback(
    (next: STTOption['value']) => {
      setCfg({ ...(cfg as VoiceConfig), sttModel: next } as cfgModels.Config)
    },
    [cfg, setCfg],
  )
  const setVoicePreset = useCallback(
    (next: string) => {
      setCfg({ ...(cfg as VoiceConfig), voicePreset: next } as cfgModels.Config)
    },
    [cfg, setCfg],
  )
  const setMicInputDevice = useCallback(
    (next: string) => {
      setCfg({ ...(cfg as VoiceConfig), micInputDevice: next } as cfgModels.Config)
    },
    [cfg, setCfg],
  )
  const setWakeWordEnabled = useCallback(
    (next: boolean) => {
      setCfg({ ...(cfg as VoiceConfig), wakeWordEnabled: next } as cfgModels.Config)
    },
    [cfg, setCfg],
  )

  // -------------------------------------------------------------------
  // Currently-selected TTS option (drives the dependent preset dropdown).
  // -------------------------------------------------------------------
  const selectedTTS = useMemo<TTSOption>(
    () => TTS_OPTIONS.find((o) => o.value === ttsProvider) ?? (TTS_OPTIONS[0] as TTSOption),
    [ttsProvider],
  )

  // -------------------------------------------------------------------
  // When the user changes TTS provider, snap voicePreset to the first
  // preset of the new provider so we never show a stale option.
  // Writes through cfg now (not local state).
  // -------------------------------------------------------------------
  useEffect(() => {
    if (!selectedTTS.presets.find((p) => p.value === voicePreset)) {
      setVoicePreset(selectedTTS.presets[0]?.value ?? '')
    }
  }, [selectedTTS, voicePreset, setVoicePreset])

  // -------------------------------------------------------------------
  // Mic device enumeration on mount. If cfg already has a stored
  // micInputDevice we keep it; otherwise we pick the system default.
  // -------------------------------------------------------------------
  useEffect(() => {
    const app = wailsApp()
    if (!app?.GetAudioInputDevices) {
      // Binding not yet generated — fall through to a single default entry
      // so the dropdown is still functional.
      const fallback: AudioDevice[] = [{ id: 'default', name: 'Default', isDefault: true }]
      setAudioDevices(fallback)
      if (!vcfg.micInputDevice) {
        const first = fallback[0]
        if (first) setMicInputDevice(first.id)
      }
      return
    }
    let cancelled = false
    app
      .GetAudioInputDevices()
      .then((devs) => {
        if (cancelled) return
        const list: AudioDevice[] = Array.isArray(devs) && devs.length > 0
          ? devs
          : [{ id: 'default', name: 'Default', isDefault: true }]
        setAudioDevices(list)
        if (!vcfg.micInputDevice) {
          const def = list.find((d) => d.isDefault) ?? list[0]
          if (def) setMicInputDevice(def.id)
        }
      })
      .catch(() => {
        if (cancelled) return
        const fallback: AudioDevice[] = [{ id: 'default', name: 'Default', isDefault: true }]
        setAudioDevices(fallback)
        if (!vcfg.micInputDevice) {
          const first = fallback[0]
          if (first) setMicInputDevice(first.id)
        }
      })
    return () => {
      cancelled = true
    }
    // We intentionally run this effect once at mount; subsequent cfg
    // changes shouldn't re-enumerate devices.
    // eslint-disable-next-line react-hooks/exhaustive-deps
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
          {audioDevices.length === 1 && audioDevices[0]?.id === 'default'
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
