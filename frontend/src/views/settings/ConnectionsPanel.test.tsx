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
