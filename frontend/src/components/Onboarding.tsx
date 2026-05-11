// ---------------------------------------------------------------------------
// Onboarding.tsx — TASK-024 first-run blocking modal.
//
// Mounted by App.tsx iff IsFirstRun() returns true. Walks the user through
// three steps and blocks the HUD until either an LLM key validates OR a
// local Ollama server is detected, plus a microphone permission step.
//
//   1. welcome  — "Welcome to Jarvis, sir." intro + Get started.
//   2. key      — Pick a provider (OpenRouter / Google / Anthropic), paste a
//                 key, hit Validate (uses ValidateAPIKey from TASK-017). On a
//                 valid pill, "Next →" is enabled. Alternatively, if a local
//                 Ollama server is reachable (IsOllamaRunning, TASK-017), the
//                 user may click "Skip — I have Ollama running" to bypass the
//                 key field entirely.
//   3. mic      — Briefly explains why microphone access is needed and surfaces
//                 a "Grant permission" CTA that calls RequestMicPermission
//                 (TASK-025). Shows the live GetMicPermissionStatus result.
//
// On completion the parent (App.tsx) calls MarkFirstRunComplete via the
// onComplete callback so the modal never re-appears.
//
// Accessibility:
//   - Backdrop covers the full viewport (fixed inset-0) so nothing behind it
//     can receive a click.
//   - The modal panel is role="dialog" + aria-modal="true".
//   - The primary CTA on each step receives autoFocus so keyboard users land
//     somewhere sensible.
//   - Escape is intentionally NOT wired — the modal cannot be dismissed
//     without finishing the flow. (This is by design; the task says first
//     run must complete before HUD access.)
//
// Security contract (mirrors TASK-017):
//   - No console.log / warn / error / info / debug call with the key value.
//   - Validation goes through ValidateAPIKey which has its own no-log
//     contract.
// ---------------------------------------------------------------------------

import { useEffect, useState } from 'react'
// The auto-generated Wails declarations don't include the new TASK-024 bindings
// (IsFirstRun / MarkFirstRunComplete) until `wails generate module` runs, and
// TASK-017/018/025 bindings may not have shipped to wailsjs/ in the dev branch
// either. We ts-expect-error the import sites so the panel still compiles.
// @ts-expect-error -- new bindings, wails generate pending
import {
  ValidateAPIKey,
  IsOllamaRunning,
  RequestMicPermission,
  GetMicPermissionStatus,
  MarkFirstRunComplete,
} from '../../wailsjs/go/main/App'

export interface OnboardingProps {
  /** Called once the user finishes step 3. Parent should unmount the modal. */
  onComplete: () => void
}

type Step = 'welcome' | 'key' | 'mic'

type ProviderId = 'openrouter' | 'google' | 'anthropic'

interface ProviderSpec {
  id: ProviderId
  label: string
  placeholder: string
}

const PROVIDERS: readonly ProviderSpec[] = [
  { id: 'openrouter', label: 'OpenRouter',       placeholder: 'sk-or-...' },
  { id: 'google',     label: 'Google AI Studio', placeholder: 'AIza...'   },
  { id: 'anthropic',  label: 'Anthropic',        placeholder: 'sk-ant-...' },
] as const

type ValidationState =
  | { status: 'idle' }
  | { status: 'validating' }
  | { status: 'valid' }
  | { status: 'invalid'; error: string }

type MicStatus = 'granted' | 'denied' | 'not_determined' | 'restricted' | 'unknown'

export function Onboarding({ onComplete }: OnboardingProps): React.ReactElement {
  const [step, setStep] = useState<Step>('welcome')

  // Step 2 — pick LLM state.
  const [provider, setProvider] = useState<ProviderId>('openrouter')
  const [keyValue, setKeyValue] = useState<string>('')
  const [validation, setValidation] = useState<ValidationState>({ status: 'idle' })
  const [ollamaRunning, setOllamaRunning] = useState<boolean>(false)

  // Step 3 — mic state.
  const [micStatus, setMicStatus] = useState<MicStatus>('unknown')

  // Probe Ollama on entry to step 2 so the "Skip — I have Ollama running"
  // button can enable/disable correctly without an extra user click.
  useEffect(() => {
    if (step !== 'key') return
    let cancelled = false
    void (async () => {
      try {
        const ok = await IsOllamaRunning()
        if (!cancelled) setOllamaRunning(Boolean(ok))
      } catch {
        if (!cancelled) setOllamaRunning(false)
      }
    })()
    return () => {
      cancelled = true
    }
  }, [step])

  // Poll mic permission status when on step 3. The macOS dialog completes
  // asynchronously after RequestMicPermission returns, so we re-read every
  // 500ms while on this step.
  useEffect(() => {
    if (step !== 'mic') return
    let cancelled = false
    const tick = async (): Promise<void> => {
      try {
        const s = await GetMicPermissionStatus()
        if (!cancelled) setMicStatus((s as MicStatus) ?? 'unknown')
      } catch {
        if (!cancelled) setMicStatus('unknown')
      }
    }
    void tick()
    const id = window.setInterval(() => void tick(), 500)
    return () => {
      cancelled = true
      window.clearInterval(id)
    }
  }, [step])

  // Reset validation whenever the user types a new key or switches provider.
  function updateKey(next: string): void {
    setKeyValue(next)
    setValidation({ status: 'idle' })
  }

  function selectProvider(next: ProviderId): void {
    setProvider(next)
    setValidation({ status: 'idle' })
  }

  async function runValidate(): Promise<void> {
    if (keyValue.trim() === '') {
      setValidation({ status: 'invalid', error: 'key is empty' })
      return
    }
    setValidation({ status: 'validating' })
    try {
      const result = (await ValidateAPIKey(provider, keyValue)) as { valid: boolean; error?: string }
      if (result?.valid) {
        setValidation({ status: 'valid' })
      } else {
        setValidation({ status: 'invalid', error: result?.error ?? 'unknown error' })
      }
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e)
      setValidation({ status: 'invalid', error: msg })
    }
  }

  function requestMic(): void {
    try {
      RequestMicPermission()
    } catch {
      // The binding is fire-and-forget; user grants or denies via the OS
      // dialog and the polling effect picks up the new status.
    }
  }

  async function finish(): Promise<void> {
    try {
      await MarkFirstRunComplete()
    } catch {
      // If the sentinel write fails (extremely unlikely — write to ~/.jarvis),
      // we still hand control back to the parent so the user isn't trapped.
    }
    onComplete()
  }

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-labelledby="onboarding-title"
      className="fixed inset-0 z-[100] flex items-center justify-center bg-black/80 backdrop-blur-sm"
    >
      <div className="w-full max-w-[500px] mx-4 rounded-lg border border-[#00e5ff]/40 bg-[#0a1420] shadow-[0_0_40px_rgba(0,229,255,0.15)] p-6">
        {/* Step indicator */}
        <div className="flex items-center justify-center gap-2 mb-5" aria-hidden="true">
          {(['welcome', 'key', 'mic'] as const).map((s, i) => (
            <span
              key={s}
              className={`h-1.5 w-8 rounded-full transition-colors ${
                step === s
                  ? 'bg-[#00e5ff]'
                  : (step === 'key' && i === 0) || (step === 'mic' && i <= 1)
                  ? 'bg-[#00e5ff]/40'
                  : 'bg-[#2d3f52]'
              }`}
            />
          ))}
        </div>

        {step === 'welcome' && (
          <div className="space-y-4">
            <h1 id="onboarding-title" className="text-xl font-bold text-[#00e5ff] tracking-wide">
              Welcome to Jarvis, sir.
            </h1>
            <p className="text-sm text-[#8ba4b8] leading-relaxed">
              Jarvis is your local AI voice companion. Before we begin, we'll set up an LLM
              connection and request microphone access. Everything stays on your machine —
              keys and recordings never leave <code className="text-[#00e5ff]">~/.jarvis/</code>.
            </p>
            <div className="pt-2 flex justify-end">
              <button
                type="button"
                autoFocus
                onClick={() => setStep('key')}
                className="text-sm px-4 py-2 rounded bg-[#0d9488] hover:bg-[#0d9488]/80 text-white transition-colors"
              >
                Get started →
              </button>
            </div>
          </div>
        )}

        {step === 'key' && (
          <div className="space-y-4">
            <h1 id="onboarding-title" className="text-xl font-bold text-[#00e5ff] tracking-wide">
              Pick an LLM
            </h1>
            <p className="text-sm text-[#8ba4b8] leading-relaxed">
              Paste a key from one provider, validate it, then click Next. Or skip this step if
              you have a local Ollama server running.
            </p>

            {/* Provider radio group */}
            <fieldset className="space-y-2">
              <legend className="text-xs text-[#8ba4b8] uppercase tracking-wider">Provider</legend>
              <div className="flex flex-wrap gap-3">
                {PROVIDERS.map((p) => (
                  <label
                    key={p.id}
                    className={`flex items-center gap-2 text-sm cursor-pointer px-3 py-1.5 rounded border transition-colors ${
                      provider === p.id
                        ? 'border-[#00e5ff] bg-[#00e5ff]/10 text-[#00e5ff]'
                        : 'border-[#2d3f52] text-[#8ba4b8] hover:border-[#00e5ff]/40'
                    }`}
                  >
                    <input
                      type="radio"
                      name="onboarding-provider"
                      value={p.id}
                      checked={provider === p.id}
                      onChange={() => selectProvider(p.id)}
                      className="sr-only"
                    />
                    {p.label}
                  </label>
                ))}
              </div>
            </fieldset>

            {/* Key paste field + Validate button */}
            <div className="space-y-1">
              <label htmlFor="onboarding-key" className="block text-xs text-[#8ba4b8] uppercase tracking-wider">
                API Key
              </label>
              <div className="flex items-center gap-2">
                <input
                  id="onboarding-key"
                  data-testid="onboarding-key-input"
                  type="password"
                  value={keyValue}
                  onChange={(e) => updateKey(e.target.value)}
                  placeholder={PROVIDERS.find((p) => p.id === provider)?.placeholder ?? ''}
                  autoComplete="off"
                  spellCheck={false}
                  className="sci-fi flex-1 text-sm font-mono"
                />
                <button
                  type="button"
                  onClick={() => void runValidate()}
                  disabled={validation.status === 'validating' || keyValue.trim() === ''}
                  data-testid="onboarding-validate"
                  className="text-xs px-3 py-1.5 rounded bg-[#0d9488] hover:bg-[#0d9488]/80 text-white disabled:opacity-40 disabled:cursor-not-allowed transition-colors whitespace-nowrap"
                >
                  {validation.status === 'validating' ? 'Validating…' : 'Validate'}
                </button>
              </div>

              {/* Pill: idle / valid / invalid */}
              {validation.status === 'valid' && (
                <span className="inline-block text-[10px] px-2 py-1 rounded bg-green-500/15 text-green-400 border border-green-500/30">
                  Valid
                </span>
              )}
              {validation.status === 'invalid' && (
                <span
                  className="inline-block text-[10px] px-2 py-1 rounded bg-red-500/15 text-red-400 border border-red-500/30 max-w-full truncate"
                  title={validation.error}
                >
                  Invalid: {validation.error}
                </span>
              )}
              {validation.status === 'idle' && (
                <span className="inline-block text-[10px] px-2 py-1 rounded bg-[#1a2632] text-[#4a6278] border border-[#2d3f52]">
                  Not validated
                </span>
              )}
            </div>

            {/* Ollama escape hatch */}
            <div className="pt-2 border-t border-[#2d3f52]">
              <button
                type="button"
                onClick={() => setStep('mic')}
                disabled={!ollamaRunning}
                data-testid="onboarding-skip-ollama"
                title={ollamaRunning ? 'Ollama detected on localhost:11434' : 'Ollama not running'}
                className="text-xs text-[#8ba4b8] hover:text-[#00e5ff] disabled:opacity-40 disabled:cursor-not-allowed transition-colors underline underline-offset-2"
              >
                {ollamaRunning
                  ? 'Skip — I have Ollama running'
                  : 'Skip — I have Ollama running (Ollama not detected)'}
              </button>
            </div>

            <div className="pt-2 flex justify-between">
              <button
                type="button"
                onClick={() => setStep('welcome')}
                className="text-sm px-3 py-2 rounded text-[#8ba4b8] hover:text-[#00e5ff] transition-colors"
              >
                ← Back
              </button>
              <button
                type="button"
                autoFocus
                onClick={() => setStep('mic')}
                disabled={validation.status !== 'valid'}
                data-testid="onboarding-key-next"
                className="text-sm px-4 py-2 rounded bg-[#0d9488] hover:bg-[#0d9488]/80 text-white disabled:opacity-40 disabled:cursor-not-allowed transition-colors"
              >
                Next →
              </button>
            </div>
          </div>
        )}

        {step === 'mic' && (
          <div className="space-y-4">
            <h1 id="onboarding-title" className="text-xl font-bold text-[#00e5ff] tracking-wide">
              Grant Mic Permission
            </h1>
            <p className="text-sm text-[#8ba4b8] leading-relaxed">
              Jarvis listens for the wake word "Hey Jarvis" via your microphone. Audio is
              processed locally and never uploaded. You can revoke this at any time in
              System Settings → Privacy & Security → Microphone.
            </p>

            <div className="rounded border border-[#2d3f52] bg-[#0a1420]/60 p-3 text-xs">
              <span className="text-[#8ba4b8]">Current status: </span>
              <span
                data-testid="onboarding-mic-status"
                className={
                  micStatus === 'granted'
                    ? 'text-green-400'
                    : micStatus === 'denied' || micStatus === 'restricted'
                    ? 'text-red-400'
                    : 'text-[#8ba4b8]'
                }
              >
                {micStatus}
              </span>
            </div>

            <div className="pt-2 flex justify-between items-center">
              <button
                type="button"
                onClick={() => setStep('key')}
                className="text-sm px-3 py-2 rounded text-[#8ba4b8] hover:text-[#00e5ff] transition-colors"
              >
                ← Back
              </button>
              <div className="flex items-center gap-2">
                <button
                  type="button"
                  autoFocus
                  onClick={requestMic}
                  disabled={micStatus === 'granted'}
                  data-testid="onboarding-grant-mic"
                  className="text-sm px-4 py-2 rounded bg-[#0d9488] hover:bg-[#0d9488]/80 text-white disabled:opacity-40 disabled:cursor-not-allowed transition-colors"
                >
                  {micStatus === 'granted' ? 'Granted' : 'Grant permission'}
                </button>
                <button
                  type="button"
                  onClick={() => void finish()}
                  data-testid="onboarding-finish"
                  className="text-sm px-4 py-2 rounded bg-[#00e5ff] hover:bg-[#00e5ff]/80 text-[#0a1420] font-semibold transition-colors"
                >
                  Finish
                </button>
              </div>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}

export default Onboarding
