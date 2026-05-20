import { useEffect, useMemo, useState } from 'react'
import type { SettingsPanelProps } from './types'
import type { config as cfgModels } from '../../../wailsjs/go/models'
import { ValidateAPIKey, IsOllamaRunning } from '../../../wailsjs/go/main/App'
import { FridayPairingModal } from './FridayPairingModal'

// ---------------------------------------------------------------------------
// ConnectionsPanel — "what does Jarvis hook up to" surface.
//
// Owns: defaultAgent, defaultCommand, dotClaudeSourcePath (with sync button).
//
// Wave 3 additions:
//   - TASK-017: 6 password-masked API key fields (OpenRouter, Google,
//               Anthropic, Cartesia, ElevenLabs, Picovoice) with show/hide
//               eye toggles + Validate buttons + gray/green/red result pills.
//   - TASK-018 (LLM-dropdown portion only): LLM model dropdown with per-
//               option availability indicators (✓ available, ⚠ needs key,
//               ⚠ Ollama not running) driven by IsOllamaRunning() + the
//               same key presence the API-key rows display.
//
// Persistence notes (v0.1.2):
//   - OpenRouter / ElevenLabs / Picovoice keys live in cfg today
//     (jarvisAPIKey, jarvisElevenLabsKey, jarvisPicovoiceKey).
//   - Google / Anthropic / Cartesia were previously local-only state; in
//     v0.1.2 they migrate to cfg.googleAPIKey / cfg.anthropicAPIKey /
//     cfg.cartesiaAPIKey. The Go agent's parallel track adds those slots
//     onto internal/config/config.go.Config. Until `wails generate module`
//     re-emits models.ts, the new fields are accessed via a typed
//     ConnectionsConfig superset cast (see below).
// ---------------------------------------------------------------------------

// ConnectionsConfig — superset of the generated config.Config that knows
// about the v0.1.2 fields plus v0.1.5's llmModel slot. Once
// `wails generate module` runs against the Go agent's PR, these become
// declared properties on config.Config itself and this superset is
// redundant.
type ConnectionsConfig = cfgModels.Config & {
  googleAPIKey?: string
  anthropicAPIKey?: string
  cartesiaAPIKey?: string
  // v0.1.5: LLM model selection. The union here mirrors the LLM_OPTIONS
  // values below; the empty string represents "not yet chosen" and falls
  // back to LLM_OPTIONS[0]!.value at read time.
  llmModel?:
    | 'google/gemini-2.5-flash'
    | 'anthropic/claude-haiku-4-5'
    | 'openai/gpt-4o-mini'
    | 'ollama:qwen3:4b'
    | ''
}

export interface ConnectionsPanelProps extends SettingsPanelProps {
  /** True while a .claude sync is in flight (drives button disabled state). */
  syncing: boolean
  /** Triggers SyncDotClaude() against the Wails backend. */
  onSync: () => Promise<void>
}

// Validation state per provider key — drives the gray/green/red pill.
type ValidationState =
  | { status: 'idle' }
  | { status: 'validating' }
  | { status: 'valid' }
  | { status: 'invalid'; error: string }

// Provider identifiers MUST match the switch cases in app_validators.go
// (case-insensitive, but we lower-case in the binding).
type ProviderId =
  | 'openrouter'
  | 'google'
  | 'anthropic'
  | 'cartesia'
  | 'elevenlabs'
  | 'picovoice'

interface KeyRowSpec {
  /** Backend provider id passed to ValidateAPIKey. */
  provider: ProviderId
  /** Visible label above the field. */
  label: string
  /** Optional human help text under the field. */
  hint?: string
  /** Placeholder shown in the empty input. */
  placeholder: string
}

// v0.1.6: surfaced API keys collapse from 6 fields to 2.
//   - OpenRouter is the single cloud-LLM auth surface; one key unlocks every
//     model in the dropdown (Gemini, Claude, GPT, etc.).
//   - Cartesia stays as an optional row because the Cartesia TTS provider
//     is still in the Voice dropdown and is the only paid voice option.
// Removed from the UI: google + anthropic (dead since OpenRouter routing),
// elevenlabs + picovoice (legacy from earlier designs — not wired to any
// current code path). Their Config fields stay in the struct so old configs
// load cleanly, they're just no longer surfaced.
const KEY_ROWS: readonly KeyRowSpec[] = [
  { provider: 'openrouter', label: 'OpenRouter API Key', hint: 'One key for every cloud LLM (Gemini, Claude, GPT, etc.). Paste, validate, done.', placeholder: 'sk-or-...' },
  { provider: 'cartesia',   label: 'Cartesia API Key',  hint: 'Only needed if you pick Cartesia in the Voice tab. Skip if you stick with VibeVoice or Kokoro.', placeholder: 'sk_car_...' },
] as const

// LLM dropdown options. Each option declares which provider key (if any)
// must be present for it to be considered available. Local options (Ollama)
// instead point at the runtime IsOllamaRunning() check.
type LLMOption = {
  value: string
  label: string
  /** If set, the option needs this provider key to be filled in to be available. */
  requiresProvider?: ProviderId
  /** If true, availability hinges on IsOllamaRunning() rather than a key. */
  requiresOllama?: boolean
}

// v0.1.6: every cloud model is routed through OpenRouter (one key unlocks
// every provider). Picking a cloud option only requires the OpenRouter key
// set on `jarvisAPIKey` (must start with sk-or-). Local Ollama stays separate.
const LLM_OPTIONS: readonly LLMOption[] = [
  { value: 'google/gemini-2.5-flash',  label: 'google/gemini-2.5-flash (via OpenRouter)',  requiresProvider: 'openrouter' },
  { value: 'anthropic/claude-haiku-4-5', label: 'anthropic/claude-haiku-4-5 (via OpenRouter)', requiresProvider: 'openrouter' },
  { value: 'openai/gpt-4o-mini',       label: 'openai/gpt-4o-mini (via OpenRouter)', requiresProvider: 'openrouter' },
  { value: 'ollama:qwen3:4b',          label: 'qwen3:4b (Ollama, local)', requiresOllama: true },
] as const

// Eye icon (open/closed) — inline SVG keeps this dep-free. The two glyphs
// are the same 16x16 viewbox so swapping them doesn't reflow the row.
function EyeIcon({ open }: { open: boolean }): React.ReactElement {
  return open ? (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M1 12s4-7 11-7 11 7 11 7-4 7-11 7S1 12 1 12z" />
      <circle cx="12" cy="12" r="3" />
    </svg>
  ) : (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M17.94 17.94A10.94 10.94 0 0 1 12 19c-7 0-11-7-11-7a18.45 18.45 0 0 1 5.06-5.94" />
      <path d="M9.9 4.24A10.94 10.94 0 0 1 12 4c7 0 11 7 11 7a18.45 18.45 0 0 1-3.17 4.19" />
      <path d="M14.12 14.12A3 3 0 1 1 9.88 9.88" />
      <line x1="1" y1="1" x2="23" y2="23" />
    </svg>
  )
}

// Reusable "show/hide + validate + pill" row. Kept private to this file —
// no other panel needs this exact pattern yet. The pill colours pin to the
// existing settings palette: cyan accent is reserved for headings, so we
// use neutral gray for idle, green-400 for valid, red-400 for invalid.
function ApiKeyRow({
  spec,
  value,
  onChange,
  validation,
  onValidate,
}: {
  spec: KeyRowSpec
  value: string
  onChange: (next: string) => void
  validation: ValidationState
  onValidate: () => void
}): React.ReactElement {
  const [revealed, setRevealed] = useState<boolean>(false)
  const inputId = `apikey-${spec.provider}`
  const inputType = revealed ? 'text' : 'password'

  return (
    <div className="space-y-1">
      <label htmlFor={inputId} className="block text-sm text-[#8ba4b8]">
        {spec.label}
      </label>
      <div className="flex items-center gap-2">
        <div className="relative flex-1">
          <input
            id={inputId}
            data-testid={`apikey-input-${spec.provider}`}
            type={inputType}
            value={value}
            onChange={(e) => onChange(e.target.value)}
            placeholder={spec.placeholder}
            autoComplete="off"
            spellCheck={false}
            className="sci-fi w-full text-sm font-mono pr-9"
          />
          <button
            type="button"
            onClick={() => setRevealed((v) => !v)}
            aria-label={revealed ? `Hide ${spec.label}` : `Show ${spec.label}`}
            aria-pressed={revealed}
            data-testid={`apikey-toggle-${spec.provider}`}
            className="absolute inset-y-0 right-1 my-auto h-7 w-7 flex items-center justify-center text-[#8ba4b8] hover:text-[#00e5ff] transition-colors"
          >
            <EyeIcon open={revealed} />
          </button>
        </div>
        <button
          type="button"
          onClick={onValidate}
          disabled={validation.status === 'validating' || value.trim() === ''}
          data-testid={`apikey-validate-${spec.provider}`}
          className="text-xs px-3 py-1.5 rounded bg-[#0d9488] hover:bg-[#0d9488]/80 text-white disabled:opacity-40 disabled:cursor-not-allowed transition-colors whitespace-nowrap"
        >
          {validation.status === 'validating' ? 'Validating…' : 'Validate'}
        </button>
        <ValidationPill state={validation} />
      </div>
      {spec.hint && <p className="text-[10px] text-[#4a6278] mt-0.5">{spec.hint}</p>}
    </div>
  )
}

// Gray / green / red status pill driven by ValidationState.
function ValidationPill({ state }: { state: ValidationState }): React.ReactElement {
  switch (state.status) {
    case 'valid':
      return (
        <span className="text-[10px] px-2 py-1 rounded bg-green-500/15 text-green-400 border border-green-500/30 whitespace-nowrap">
          Valid
        </span>
      )
    case 'invalid':
      return (
        <span
          className="text-[10px] px-2 py-1 rounded bg-red-500/15 text-red-400 border border-red-500/30 max-w-[14rem] truncate"
          title={state.error}
        >
          Invalid: {state.error}
        </span>
      )
    case 'validating':
      return (
        <span className="text-[10px] px-2 py-1 rounded bg-[#1a2632] text-[#8ba4b8] border border-[#2d3f52] whitespace-nowrap">
          …
        </span>
      )
    case 'idle':
    default:
      return (
        <span className="text-[10px] px-2 py-1 rounded bg-[#1a2632] text-[#4a6278] border border-[#2d3f52] whitespace-nowrap">
          Not validated
        </span>
      )
  }
}

export function ConnectionsPanel({
  cfg,
  setCfg,
  activeTab,
  syncing,
  onSync,
}: ConnectionsPanelProps): React.ReactElement {
  // -------------------------------------------------------------------------
  // v0.1.2: google / anthropic / cartesia keys now persist via cfg
  // (cfg.googleAPIKey / cfg.anthropicAPIKey / cfg.cartesiaAPIKey). We read
  // and write them through the ConnectionsConfig superset cast — once
  // `wails generate module` regenerates the bindings, the cast vanishes.
  // -------------------------------------------------------------------------
  const ccfg = cfg as ConnectionsConfig

  // Per-provider validation state. Keyed by ProviderId so adding a row is
  // a one-liner above + the lookup here.
  const [validations, setValidations] = useState<Record<ProviderId, ValidationState>>({
    openrouter: { status: 'idle' },
    google:     { status: 'idle' },
    anthropic:  { status: 'idle' },
    cartesia:   { status: 'idle' },
    elevenlabs: { status: 'idle' },
    picovoice:  { status: 'idle' },
  })

  // Ollama reachability — polled once on mount. The dropdown's "⚠ Ollama
  // not running" indicator depends on this; the user can re-check by
  // hitting "Re-check" in the dropdown footer.
  const [ollamaRunning, setOllamaRunning] = useState<boolean>(false)
  const [ollamaCheckedAt, setOllamaCheckedAt] = useState<number>(0)
  useEffect(() => {
    let cancelled = false
    void (async () => {
      try {
        const ok = await IsOllamaRunning()
        if (!cancelled) {
          setOllamaRunning(Boolean(ok))
          setOllamaCheckedAt(Date.now())
        }
      } catch {
        if (!cancelled) setOllamaRunning(false)
      }
    })()
    return () => {
      cancelled = true
    }
  }, [])

  // Reads the current value of any provider's key from cfg. All six keys
  // are now persisted via cfg (v0.1.2 migration).
  function readKey(provider: ProviderId): string {
    switch (provider) {
      case 'openrouter': return cfg.jarvisAPIKey ?? ''
      case 'google':     return ccfg.googleAPIKey ?? ''
      case 'anthropic':  return ccfg.anthropicAPIKey ?? ''
      case 'cartesia':   return ccfg.cartesiaAPIKey ?? ''
      case 'elevenlabs': return cfg.jarvisElevenLabsKey ?? ''
      case 'picovoice':  return cfg.jarvisPicovoiceKey ?? ''
    }
  }

  function writeKey(provider: ProviderId, next: string): void {
    switch (provider) {
      case 'openrouter':
        setCfg({ ...cfg, jarvisAPIKey: next })
        break
      case 'google':
        setCfg({ ...(cfg as ConnectionsConfig), googleAPIKey: next } as cfgModels.Config)
        break
      case 'anthropic':
        setCfg({ ...(cfg as ConnectionsConfig), anthropicAPIKey: next } as cfgModels.Config)
        break
      case 'cartesia':
        setCfg({ ...(cfg as ConnectionsConfig), cartesiaAPIKey: next } as cfgModels.Config)
        break
      case 'elevenlabs':
        setCfg({ ...cfg, jarvisElevenLabsKey: next })
        break
      case 'picovoice':
        setCfg({ ...cfg, jarvisPicovoiceKey: next })
        break
    }
    // Mutating the key invalidates any prior result. Reset the pill to
    // idle so the user re-validates the new value before trusting it.
    setValidations((s) => ({ ...s, [provider]: { status: 'idle' } }))
  }

  // Run the Wails binding and stash the result. We never throw — every
  // failure path collapses into a red pill so the UI stays readable.
  async function runValidate(provider: ProviderId): Promise<void> {
    const key = readKey(provider)
    if (key.trim() === '') {
      setValidations((s) => ({ ...s, [provider]: { status: 'invalid', error: 'key is empty' } }))
      return
    }
    setValidations((s) => ({ ...s, [provider]: { status: 'validating' } }))
    try {
      const result = (await ValidateAPIKey(provider, key)) as { valid: boolean; error?: string }
      if (result?.valid) {
        setValidations((s) => ({ ...s, [provider]: { status: 'valid' } }))
      } else {
        setValidations((s) => ({
          ...s,
          [provider]: { status: 'invalid', error: result?.error ?? 'unknown error' },
        }))
      }
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e)
      setValidations((s) => ({ ...s, [provider]: { status: 'invalid', error: msg } }))
    }
  }

  async function recheckOllama(): Promise<void> {
    try {
      const ok = await IsOllamaRunning()
      setOllamaRunning(Boolean(ok))
      setOllamaCheckedAt(Date.now())
    } catch {
      setOllamaRunning(false)
      setOllamaCheckedAt(Date.now())
    }
  }

  // Per-option availability flag for the LLM dropdown. Reads the same
  // sources as the API-key rows so flipping a key on flips the indicator
  // immediately without an extra fetch.
  const llmAvailability = useMemo(() => {
    return LLM_OPTIONS.map((opt) => {
      if (opt.requiresOllama) {
        return {
          option: opt,
          available: ollamaRunning,
          reason: ollamaRunning ? 'available' : 'Ollama not running',
        }
      }
      if (opt.requiresProvider) {
        const hasKey = readKey(opt.requiresProvider).trim() !== ''
        return {
          option: opt,
          available: hasKey,
          reason: hasKey ? 'available' : `needs ${opt.requiresProvider} key`,
        }
      }
      return { option: opt, available: true, reason: 'available' }
    })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    ollamaRunning,
    cfg.jarvisAPIKey,
    ccfg.googleAPIKey,
    ccfg.anthropicAPIKey,
    ccfg.cartesiaAPIKey,
  ])

  // v0.3.0 / TASK-025: "Connect Friday phone" modal state. Local to the
  // panel because the modal is purely transient UI — open/close doesn't
  // need to persist across reloads. The modal itself owns the QR fetch
  // lifecycle (loading / ready / error) so this parent only tracks
  // visibility.
  const [fridayPairOpen, setFridayPairOpen] = useState<boolean>(false)

  // v0.1.5: LLM model selection now persists via cfg.llmModel (same
  // ConnectionsConfig superset cast pattern as the v0.1.2 key migration).
  // The default when cfg.llmModel is empty/undefined stays at
  // LLM_OPTIONS[0]!.value so today's behaviour is preserved for users who
  // never touched the dropdown. The Go agent's parallel track adds
  // LlmModel to internal/config/config.go.Config and flags it in
  // daemonRestartNeeded so the existing amber banner triggers on save.
  // `ccfg.llmModel` is `'<model>' | '' | undefined`; the falsy `&&` filters
  // out both `''` and `undefined` so the result is a non-empty model string.
  const selectedLLM: string = ccfg.llmModel
    ? ccfg.llmModel
    : LLM_OPTIONS[0]!.value
  const selectedAvailability = llmAvailability.find((a) => a.option.value === selectedLLM)

  return (
    <div
      role="tabpanel"
      id="settings-tab-panel-connections"
      aria-labelledby="settings-tab-connections"
      hidden={activeTab !== 'connections'}
      className="space-y-6"
    >
      {/* OpenRouter is the single cloud-LLM auth surface. Cartesia is the
          one optional paid voice. Everything else runs locally. */}
      <section className="holo-panel p-4" data-testid="api-keys-section">
        <h2 className="text-sm font-semibold text-[#00e5ff] mb-1">API Keys</h2>
        <p className="text-xs text-[#8ba4b8] mb-3">
          Stored locally in <code className="text-[#00e5ff]">~/.jarvis/config.json</code>. Click
          Validate to send a 1-token test request to the provider.
        </p>
        <div className="space-y-4">
          {KEY_ROWS.map((spec) => (
            <ApiKeyRow
              key={spec.provider}
              spec={spec}
              value={readKey(spec.provider)}
              onChange={(v) => writeKey(spec.provider, v)}
              validation={validations[spec.provider]}
              onValidate={() => void runValidate(spec.provider)}
            />
          ))}
        </div>
      </section>

      {/* TASK-018 (LLM-dropdown portion): model selector with availability
          indicators. The indicator next to each option reflects either a
          present API key or the live Ollama probe. */}
      <section className="holo-panel p-4" data-testid="llm-model-section">
        <h2 className="text-sm font-semibold text-[#00e5ff] mb-1">LLM Model</h2>
        <p className="text-xs text-[#8ba4b8] mb-3">
          Model used for Jarvis-generated responses. Ollama runs locally; the others
          need the corresponding API key above.
        </p>
        <div className="space-y-2">
          <select
            value={selectedLLM}
            onChange={(e) =>
              setCfg({
                ...(cfg as ConnectionsConfig),
                llmModel: e.target.value as ConnectionsConfig['llmModel'],
              } as cfgModels.Config)
            }
            data-testid="llm-model-dropdown"
            aria-label="LLM model"
            className="sci-fi text-sm w-full"
          >
            {llmAvailability.map(({ option, available, reason }) => {
              const indicator = available
                ? '✓'
                : '⚠'
              const suffix = available
                ? 'available'
                : option.requiresOllama
                  ? 'Ollama not running'
                  : `needs ${option.requiresProvider} key`
              return (
                <option
                  key={option.value}
                  value={option.value}
                  data-available={available}
                  data-reason={reason}
                >
                  {indicator} {option.label} — {suffix}
                </option>
              )
            })}
          </select>
          {selectedAvailability && !selectedAvailability.available && (
            <p className="text-[11px] text-red-400" data-testid="llm-unavailable-warning">
              {selectedAvailability.option.requiresOllama
                ? '⚠ Ollama not running — start it with `ollama serve` then re-check below.'
                : `⚠ This model needs the ${selectedAvailability.option.requiresProvider} key above. Paste it in and validate.`}
            </p>
          )}
          <div className="flex items-center gap-3 text-[10px] text-[#4a6278]">
            <button
              type="button"
              onClick={() => void recheckOllama()}
              className="px-2 py-0.5 rounded border border-[#2d3f52] hover:border-[#00e5ff] hover:text-[#00e5ff] transition-colors"
              data-testid="ollama-recheck"
            >
              Re-check Ollama
            </button>
            <span>
              Ollama:{' '}
              <span className={ollamaRunning ? 'text-green-400' : 'text-red-400'}>
                {ollamaRunning ? 'running' : 'not running'}
              </span>
              {ollamaCheckedAt > 0 && (
                <span className="ml-1 text-[#4a6278]">
                  (checked {new Date(ollamaCheckedAt).toLocaleTimeString()})
                </span>
              )}
            </span>
          </div>
        </div>
      </section>

      {/* Default Agent */}
      <section className="holo-panel p-4">
        <h2 className="text-sm font-semibold text-[#00e5ff] mb-1">Default Agent</h2>
        <p className="text-xs text-[#8ba4b8] mb-3">
          The default AI agent for new sessions and divide-and-conquer operations.
        </p>
        <select
          value={cfg.defaultAgent}
          onChange={(e) => setCfg({ ...cfg, defaultAgent: e.target.value })}
          className="sci-fi text-sm"
        >
          <option value="claude-code">Claude Code</option>
          <option value="kiro">Kiro CLI</option>
          <option value="gemini">Gemini CLI</option>
          <option value="codex">Codex CLI</option>
          <option value="aider">Aider</option>
        </select>
      </section>

      {/* Launch Defaults (default command) */}
      <section className="holo-panel p-4">
        <h2 className="text-sm font-semibold text-[#00e5ff] mb-1">Launch Defaults</h2>
        <p className="text-xs text-[#8ba4b8] mb-3">
          Default command and arguments used when launching new agent sessions.
        </p>
        <div>
          <label htmlFor="default-command" className="block text-sm text-[#8ba4b8] mb-1">
            Default command
          </label>
          <input
            id="default-command"
            type="text"
            value={cfg.defaultCommand}
            onChange={(e) => setCfg({ ...cfg, defaultCommand: e.target.value })}
            placeholder="claude"
            className="sci-fi w-full text-sm font-mono"
          />
        </div>
      </section>

      {/* .claude Source Path
          (TASK-023 will additionally add a "Browse..." button in Advanced;
           for now the field stays here because it's the closest semantic
           home in the Connections-style "what does Jarvis hook up to"
           category.) */}
      <section className="holo-panel p-4">
        <h2 className="text-sm font-semibold text-[#00e5ff] mb-1">.claude Source Path</h2>
        <p className="text-xs text-[#8ba4b8] mb-3">
          Path to your dotAiAgent repo (or any folder containing a .claude directory with agents,
          skills, etc). This gets copied into every workspace you create.
        </p>
        <input
          type="text"
          value={cfg.dotClaudeSourcePath}
          onChange={(e) => setCfg({ ...cfg, dotClaudeSourcePath: e.target.value })}
          placeholder="Auto-detect (leave empty) or /path/to/dotAiAgent"
          className="sci-fi w-full text-sm font-mono"
        />
        <div className="flex items-center gap-2 mt-2">
          <button
            type="button"
            onClick={() => void onSync()}
            disabled={syncing}
            className="text-xs px-3 py-1.5 rounded bg-[#0d9488] hover:bg-[#0d9488]/80 text-white disabled:opacity-50 transition-colors"
          >
            {syncing ? 'Syncing...' : 'Pull & Sync to All Workspaces'}
          </button>
          <span className="text-[10px] text-[#4a6278]">
            Runs git pull then copies .claude/ to all workspaces
          </span>
        </div>
      </section>

      {/* v0.3.0 / TASK-025 — Friday mobile companion pairing.
          Click the button → modal opens with a QR encoding
          jarvis://pair?host=<LAN>:<port>&token=<bearer>&room=jarvis. Friday's
          pair.tsx (TASK-020) scans it, stores the bearer in secure-store, and
          connects to the existing /ws/jarvis-mobile WebSocket. The QR is
          rendered server-side (Go) via GenerateMobilePairingQR so the bearer
          token never crosses a React render boundary as a plain string. */}
      <section className="holo-panel p-4" data-testid="friday-mobile-section">
        <h2 className="text-sm font-semibold text-[#00e5ff] mb-1">Friday mobile</h2>
        <p className="text-xs text-[#8ba4b8] mb-3">
          Pair the Friday phone companion (Expo Go) with this Mac. Scanning the QR
          stores a bearer token on the phone and points it at this Mac's local IP.
        </p>
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={() => setFridayPairOpen(true)}
            data-testid="friday-pair-open"
            className="text-xs px-3 py-1.5 rounded bg-[#0d9488] hover:bg-[#0d9488]/80 text-white transition-colors"
          >
            Connect Friday phone
          </button>
          <span className="text-[10px] text-[#4a6278] font-mono">
            Opens a QR code Friday scans
          </span>
        </div>
      </section>

      <FridayPairingModal
        open={fridayPairOpen}
        onClose={() => setFridayPairOpen(false)}
      />
    </div>
  )
}
