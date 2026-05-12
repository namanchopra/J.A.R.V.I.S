// ---------------------------------------------------------------------------
// ConnectionsPanel source-level contract test (TASK-017 + TASK-018 LLM
// dropdown portion).
//
// Same `?raw` pattern as SettingsView.test.tsx — the frontend doesn't ship
// jsdom / @testing-library/react in this environment, so behavioural tests
// run against the panel source text. This catches the regression patterns
// the task acceptance criteria call out:
//   1. All 6 provider names appear in the panel.
//   2. ValidateAPIKey is referenced.
//   3. IsOllamaRunning is referenced.
//   4. The show/hide eye-toggle pattern is wired (input type flips between
//      'password' and 'text').
//   5. The LLM dropdown has all 4 advertised models.
//   6. Pill states (Not validated / Valid / Invalid) are all present.
// ---------------------------------------------------------------------------

import { describe, it, expect } from 'vitest'
import SOURCE from './ConnectionsPanel.tsx?raw'

describe('ConnectionsPanel API key cluster (TASK-017)', () => {
  it('references all 6 provider names', () => {
    // Provider ids are the lowercased switch cases in app_validators.go.
    expect(SOURCE).toMatch(/['"]openrouter['"]/)
    expect(SOURCE).toMatch(/['"]google['"]/)
    expect(SOURCE).toMatch(/['"]anthropic['"]/)
    expect(SOURCE).toMatch(/['"]cartesia['"]/)
    expect(SOURCE).toMatch(/['"]elevenlabs['"]/)
    expect(SOURCE).toMatch(/['"]picovoice['"]/)
  })

  it('mentions every human-readable provider label', () => {
    expect(SOURCE).toMatch(/OpenRouter/)
    expect(SOURCE).toMatch(/Google AI Studio/)
    expect(SOURCE).toMatch(/Anthropic/)
    expect(SOURCE).toMatch(/Cartesia/)
    expect(SOURCE).toMatch(/ElevenLabs/)
    expect(SOURCE).toMatch(/Picovoice/)
  })

  it('calls the ValidateAPIKey Wails binding', () => {
    // The import line plus at least one call site.
    expect(SOURCE).toMatch(/ValidateAPIKey/)
    expect(SOURCE).toMatch(/await\s+ValidateAPIKey\s*\(/)
  })

  it('implements the show/hide eye toggle (input type flips between password and text)', () => {
    // The pattern of interest is `type={revealed ? 'text' : 'password'}`
    // — we accept either quoting and a bit of formatting drift.
    expect(SOURCE).toMatch(/revealed\s*\?\s*['"]text['"]\s*:\s*['"]password['"]/)
    // And the toggle button must call setRevealed.
    expect(SOURCE).toMatch(/setRevealed/)
  })

  it('renders all 3 validation pill states (idle / valid / invalid)', () => {
    expect(SOURCE).toMatch(/Not validated/)
    expect(SOURCE).toMatch(/>\s*Valid\s*</)
    expect(SOURCE).toMatch(/Invalid:/)
  })

  it('binds the OpenRouter / ElevenLabs / Picovoice keys to cfg (persistent storage)', () => {
    // These three have config slots today; the panel must read/write them
    // through cfg/setCfg, not local state.
    expect(SOURCE).toMatch(/cfg\.jarvisAPIKey/)
    expect(SOURCE).toMatch(/cfg\.jarvisElevenLabsKey/)
    expect(SOURCE).toMatch(/cfg\.jarvisPicovoiceKey/)
  })

  it('binds the Google / Anthropic / Cartesia keys to cfg (v0.1.2 migration)', () => {
    // v0.1.2 moves these three out of component-local useState and into
    // cfg.googleAPIKey / cfg.anthropicAPIKey / cfg.cartesiaAPIKey. The
    // ConnectionsConfig superset cast bridges the gap until `wails
    // generate module` re-emits models.ts. We pin on the read sites + the
    // write sites independently.
    expect(SOURCE).toMatch(/googleAPIKey/)
    expect(SOURCE).toMatch(/anthropicAPIKey/)
    expect(SOURCE).toMatch(/cartesiaAPIKey/)
    // The setCfg writes must include each new field name as a property
    // assignment (not as a key on local state).
    expect(SOURCE).toMatch(/googleAPIKey:\s*next/)
    expect(SOURCE).toMatch(/anthropicAPIKey:\s*next/)
    expect(SOURCE).toMatch(/cartesiaAPIKey:\s*next/)
  })

  it('no longer keeps Google/Anthropic/Cartesia keys in local useState (v0.1.2)', () => {
    // Pre-v0.1.2 lines we want gone:
    //   const [googleKey, setGoogleKey]       = useState<string>('')
    //   const [anthropicKey, setAnthropicKey] = useState<string>('')
    //   const [cartesiaKey, setCartesiaKey]   = useState<string>('')
    expect(SOURCE).not.toMatch(/useState<string>\(\s*['"]['"]\s*\)\s*\/\/[^\n]*google/i)
    expect(SOURCE).not.toMatch(/\[\s*googleKey\s*,/)
    expect(SOURCE).not.toMatch(/\[\s*anthropicKey\s*,/)
    expect(SOURCE).not.toMatch(/\[\s*cartesiaKey\s*,/)
    expect(SOURCE).not.toMatch(/setGoogleKey\(/)
    expect(SOURCE).not.toMatch(/setAnthropicKey\(/)
    expect(SOURCE).not.toMatch(/setCartesiaKey\(/)
  })

  it('declares a ConnectionsConfig superset cast for the new fields', () => {
    expect(SOURCE).toMatch(/type\s+ConnectionsConfig\s*=/)
    expect(SOURCE).toMatch(/googleAPIKey\?:/)
    expect(SOURCE).toMatch(/anthropicAPIKey\?:/)
    expect(SOURCE).toMatch(/cartesiaAPIKey\?:/)
  })

  it('does not write the key value to any console / log surface', () => {
    // Defence-in-depth: TASK-017 mandates no key logging. The frontend has
    // no log surface that ships to disk, but we still pin the panel
    // against console.log / console.warn / console.error containing the
    // word "key" — any logger call with the literal `key` arg fails this.
    // Grep deliberately matches `console.<method>(...key...)` patterns;
    // the panel uses Wails events and DOM state instead.
    expect(SOURCE).not.toMatch(/console\.(log|warn|error|info|debug)\([^)]*\bkey\b/)
  })
})

describe('ConnectionsPanel LLM model dropdown (TASK-018)', () => {
  it('calls the IsOllamaRunning Wails binding', () => {
    expect(SOURCE).toMatch(/IsOllamaRunning/)
    // Must be called somewhere (not just imported).
    expect(SOURCE).toMatch(/IsOllamaRunning\s*\(\s*\)/)
  })

  it('lists all 4 advertised LLM models', () => {
    expect(SOURCE).toMatch(/google\/gemini-2\.5-flash/)
    expect(SOURCE).toMatch(/anthropic\/claude-haiku-4-5/)
    expect(SOURCE).toMatch(/openai\/gpt-4o-mini/)
    expect(SOURCE).toMatch(/ollama:qwen3:4b/)
  })

  it('shows availability indicators (✓ / ⚠) per option', () => {
    // Both glyphs must appear somewhere in the panel.
    expect(SOURCE).toMatch(/✓/)
    expect(SOURCE).toMatch(/⚠/)
  })

  it('surfaces a dedicated "Ollama not running" indicator', () => {
    expect(SOURCE).toMatch(/Ollama not running/)
  })

  it('renders a <select> for model selection', () => {
    expect(SOURCE).toMatch(/<select\b/)
  })
})

describe('ConnectionsPanel LLM model persistence (v0.1.5)', () => {
  // v0.1.5 promotes the LLM dropdown out of component-local useState and
  // into cfg.llmModel, mirroring the v0.1.2 cfg migration for the three
  // API keys. These assertions pin the shape of that migration at the
  // source level so the regression cannot silently reappear.

  it('reads the LLM selection from cfg (not local state)', () => {
    // The panel must reach into cfg / ccfg for the llmModel slot. We accept
    // either `cfg.llmModel` or `cfg?.llmModel` (and the ccfg alias used
    // inside the body, same as the v0.1.2 keys).
    expect(SOURCE).toMatch(/(?:cfg|ccfg)\??\.llmModel/)
  })

  it('removes the setSelectedLLM local-state setter', () => {
    // Pre-v0.1.5 line we want gone:
    //   const [selectedLLM, setSelectedLLM] = useState<string>(LLM_OPTIONS[0]!.value)
    // and its call sites onChange={(e) => setSelectedLLM(e.target.value)}.
    expect(SOURCE).not.toMatch(/setSelectedLLM/)
  })

  it('writes the LLM selection through setCfg with the llmModel field', () => {
    // The setCfg call inside the <select> onChange must carry an
    // `llmModel:` property. Whitespace / newline drift is tolerated by
    // the `[\s\S]*?` non-greedy spanner.
    expect(SOURCE).toMatch(/setCfg\([\s\S]*?llmModel/)
  })

  it('falls back to LLM_OPTIONS[0]!.value when cfg.llmModel is empty/undefined', () => {
    // Today's default behaviour is preserved by reading
    //   ccfg.llmModel && ccfg.llmModel !== '' ? ccfg.llmModel : LLM_OPTIONS[0]!.value
    // We pin the fallback expression so the default cannot silently drift.
    expect(SOURCE).toMatch(/LLM_OPTIONS\[0\]!\.value/)
  })

  it('extends the ConnectionsConfig superset cast with llmModel (v0.1.5)', () => {
    // Until `wails generate module` re-emits models.ts with LlmModel
    // included, the superset cast bridges the gap — same pattern as the
    // v0.1.2 keys.
    expect(SOURCE).toMatch(/llmModel\?:/)
  })
})
