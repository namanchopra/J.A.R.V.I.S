// ---------------------------------------------------------------------------
// Onboarding source-level contract test (TASK-024 first-run modal).
//
// Same `?raw` pattern used by SettingsView.test.tsx and ConnectionsPanel.test
// — the frontend doesn't ship jsdom / @testing-library/react in this
// environment, so behavioural tests run against the panel source text. This
// catches the regression patterns the task acceptance criteria call out:
//   1. All 3 step labels appear (Welcome / Pick LLM / Grant Mic Permission).
//   2. ValidateAPIKey is referenced and called.
//   3. RequestMicPermission is referenced and called.
//   4. MarkFirstRunComplete is referenced and called.
//   5. IsOllamaRunning is referenced (skip path).
//   6. ARIA dialog markup is present (role="dialog", aria-modal="true").
//   7. No console.<method>(...key...) — the pasted key value must never be
//      logged.
//   8. Escape key handler is NOT wired (intentional — the modal cannot be
//      dismissed without finishing).
// ---------------------------------------------------------------------------

import { describe, it, expect } from 'vitest'
import SOURCE from './Onboarding.tsx?raw'

describe('Onboarding modal contract (TASK-024)', () => {
  it('renders all 3 step labels', () => {
    // Step 1: Welcome
    expect(SOURCE).toMatch(/Welcome to Jarvis, sir\./)
    // Step 2: Pick LLM
    expect(SOURCE).toMatch(/Pick an LLM/)
    // Step 3: Grant Mic Permission
    expect(SOURCE).toMatch(/Grant Mic Permission/)
  })

  it('walks through 3 step state values', () => {
    expect(SOURCE).toMatch(/['"]welcome['"]/)
    expect(SOURCE).toMatch(/['"]key['"]/)
    expect(SOURCE).toMatch(/['"]mic['"]/)
  })

  it('calls the ValidateAPIKey Wails binding', () => {
    expect(SOURCE).toMatch(/ValidateAPIKey/)
    expect(SOURCE).toMatch(/await\s+ValidateAPIKey\s*\(/)
  })

  it('calls the IsOllamaRunning Wails binding for the skip path', () => {
    expect(SOURCE).toMatch(/IsOllamaRunning/)
    expect(SOURCE).toMatch(/IsOllamaRunning\s*\(\s*\)/)
  })

  it('calls the RequestMicPermission Wails binding', () => {
    expect(SOURCE).toMatch(/RequestMicPermission/)
    expect(SOURCE).toMatch(/RequestMicPermission\s*\(\s*\)/)
  })

  it('reads GetMicPermissionStatus to surface the current state', () => {
    expect(SOURCE).toMatch(/GetMicPermissionStatus/)
    expect(SOURCE).toMatch(/GetMicPermissionStatus\s*\(\s*\)/)
  })

  it('calls MarkFirstRunComplete when the user finishes', () => {
    expect(SOURCE).toMatch(/MarkFirstRunComplete/)
    expect(SOURCE).toMatch(/await\s+MarkFirstRunComplete\s*\(/)
  })

  it('lists the three supported LLM providers in the radio group', () => {
    // Provider ids must match the switch cases in app_validators.go.
    expect(SOURCE).toMatch(/['"]openrouter['"]/)
    expect(SOURCE).toMatch(/['"]google['"]/)
    expect(SOURCE).toMatch(/['"]anthropic['"]/)
    // Human-readable labels.
    expect(SOURCE).toMatch(/OpenRouter/)
    expect(SOURCE).toMatch(/Google AI Studio/)
    expect(SOURCE).toMatch(/Anthropic/)
  })

  it('renders all 3 validation pill states (idle / valid / invalid)', () => {
    expect(SOURCE).toMatch(/Not validated/)
    expect(SOURCE).toMatch(/>\s*Valid\s*</)
    expect(SOURCE).toMatch(/Invalid:/)
  })

  it('exposes an "Ollama running" skip button (disabled when not detected)', () => {
    expect(SOURCE).toMatch(/Skip — I have Ollama running/)
    // The disabled state must hinge on ollamaRunning so users without a
    // local Ollama can't bypass the key step trivially.
    expect(SOURCE).toMatch(/disabled=\{!ollamaRunning\}/)
  })

  it('uses dialog role + aria-modal for accessibility', () => {
    expect(SOURCE).toMatch(/role=['"]dialog['"]/)
    expect(SOURCE).toMatch(/aria-modal=['"]true['"]/)
    expect(SOURCE).toMatch(/aria-labelledby=['"]onboarding-title['"]/)
  })

  it('autofocuses the primary CTA on each step', () => {
    // Multiple autoFocus declarations — one per step's primary button.
    const matches = SOURCE.match(/autoFocus/g) ?? []
    expect(matches.length).toBeGreaterThanOrEqual(3)
  })

  it('does NOT wire an Escape key handler (modal must not be dismissable)', () => {
    // Defence-in-depth: any keydown handler that listens for "Escape" would
    // give the user an out before they finish the flow. The contract is that
    // first-run cannot be bypassed.
    expect(SOURCE).not.toMatch(/key\s*===?\s*['"]Escape['"]/)
    expect(SOURCE).not.toMatch(/['"]keydown['"]/)
  })

  it('does not write the key value to any console / log surface', () => {
    // Same security contract as ConnectionsPanel.tsx (TASK-017).
    // Any console.<method>(... key ...) call would leak the pasted secret.
    expect(SOURCE).not.toMatch(/console\.(log|warn|error|info|debug)\([^)]*\bkey\b/)
    // Defence-in-depth: pin against ANY console.* call landing in this file.
    // The component is short enough that legitimate logging is unnecessary.
    expect(SOURCE).not.toMatch(/console\.(log|warn|error|info|debug)\s*\(/)
  })

  it('invokes the onComplete callback after MarkFirstRunComplete', () => {
    // The parent uses the callback to unmount the modal — without it the
    // modal would never go away.
    expect(SOURCE).toMatch(/onComplete\s*\(\s*\)/)
  })
})
