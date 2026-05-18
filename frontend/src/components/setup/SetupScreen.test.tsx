// ---------------------------------------------------------------------------
// SetupScreen source-level contract test (v0.2.0 / TASK-011).
//
// The frontend test harness does not ship jsdom / @testing-library/react,
// so following the same pattern as FirstRunDownloadOverlay.test.tsx and
// JarvisHudView.test.tsx, we assert source-level invariants that pin the
// visual + event contract. If/when DOM testing infrastructure is added,
// supplement with a mount-time spec.
//
// Covered:
//   1. All 4 phase labels appear in source
//   2. All 4 state glyphs (◌, ◉, ✕, ◯) appear in source
//   3. useSetupState is imported + called
//   4. sendJarvisCommand fires the retry_setup_phase payload
//   5. window.go.main.App.OpenSetupLog is typeof-guarded
//   6. role="dialog", aria-modal="true", aria-live="polite" all present
//   7. zIndex 95 set in styles
//   8. Phase ordering matches canonical PHASE_ORDER
// ---------------------------------------------------------------------------

import { describe, it, expect } from 'vitest'
import SOURCE from './SetupScreen.tsx?raw'
import {
  __SETUP_SCREEN_PHASE_LABEL,
  __SETUP_SCREEN_PHASE_ORDER,
} from './SetupScreen'

describe('SetupScreen — structural contract', () => {
  it('declares the dialog with role="dialog" and aria-modal="true"', () => {
    expect(SOURCE).toMatch(/role=['"]dialog['"]/)
    expect(SOURCE).toMatch(/aria-modal=['"]true['"]/)
  })

  it('labels the dialog via aria-labelledby pointing at the header element', () => {
    expect(SOURCE).toMatch(/aria-labelledby=['"]setup-screen-title['"]/)
    expect(SOURCE).toMatch(/id=['"]setup-screen-title['"]/)
  })

  it('exposes an aria-live="polite" readout for screen-reader progress', () => {
    expect(SOURCE).toMatch(/aria-live=['"]polite['"]/)
  })

  it('uses the cyan accent token (#00e5ff or --accent-blue)', () => {
    // Either the variable reference or a hex fallback is acceptable.
    expect(
      /var\(--accent-blue/.test(SOURCE) || /#00e5ff/i.test(SOURCE),
    ).toBe(true)
  })

  it('uses the monospace label vocabulary (SF Mono, uppercase, letter-spacing)', () => {
    expect(SOURCE).toMatch(/'SF Mono'\s*,\s*'Menlo'\s*,\s*monospace/)
    expect(SOURCE).toMatch(/textTransform:\s*['"]uppercase['"]/)
  })

  it('renders corner brackets via the CornerBrackets sub-component', () => {
    expect(SOURCE).toMatch(/function\s+CornerBrackets/)
    expect(SOURCE).toMatch(/<CornerBrackets/)
    expect(SOURCE).toMatch(/borderTop:\s*`?2px solid/)
    expect(SOURCE).toMatch(/borderLeft:\s*`?2px solid/)
  })

  it('renders the per-phase row via a reusable PhaseRowView sub-component', () => {
    expect(SOURCE).toMatch(/function\s+PhaseRowView/)
    expect(SOURCE).toMatch(/<PhaseRowView\b/)
  })

  it('uses the ▸ marker and → in the header and view-setup-log link', () => {
    expect(SOURCE).toContain('▸')
    expect(SOURCE).toContain('SETTING UP JARVIS')
    expect(SOURCE).toContain('▸ VIEW SETUP LOG →')
  })
})

describe('SetupScreen — 4 phase labels in source', () => {
  it('contains the canonical Python runtime label', () => {
    expect(SOURCE).toContain('Installing Python runtime')
  })

  it('contains the canonical voice pipeline label', () => {
    expect(SOURCE).toContain('Installing voice pipeline')
  })

  it('contains the canonical VibeVoice download label (with size)', () => {
    expect(SOURCE).toContain('Downloading VibeVoice (~1.9 GB)')
  })

  it('contains the canonical Whisper download label (with size)', () => {
    expect(SOURCE).toContain('Downloading Whisper (~460 MB)')
  })
})

describe('SetupScreen — state glyphs in source', () => {
  it('renders the in-progress glyph ◌', () => {
    expect(SOURCE).toContain('◌')
  })

  it('renders the done glyph ◉', () => {
    expect(SOURCE).toContain('◉')
  })

  it('renders the error glyph ✕', () => {
    expect(SOURCE).toContain('✕')
  })

  it('renders the pending glyph ◯', () => {
    expect(SOURCE).toContain('◯')
  })
})

describe('SetupScreen — useSetupState wiring', () => {
  it('imports useSetupState from the v0.2.0 hook module', () => {
    expect(SOURCE).toMatch(
      /import\s*\{[^}]*useSetupState[^}]*\}\s*from\s+['"][^'"]*use-setup-state['"]/,
    )
  })

  it('calls useSetupState() inside the component body', () => {
    expect(SOURCE).toMatch(/useSetupState\s*\(\s*\)/)
  })

  it('destructures `phases` from the hook result', () => {
    expect(SOURCE).toMatch(/\{\s*phases\s*\}\s*=\s*useSetupState/)
  })
})

describe('SetupScreen — retry wiring', () => {
  it('imports sendJarvisCommand from jarvis-api', () => {
    expect(SOURCE).toMatch(
      /import\s*\{[^}]*sendJarvisCommand[^}]*\}\s*from\s+['"][^'"]*jarvis-api['"]/,
    )
  })

  it('fires the retry_setup_phase payload with phase via sendJarvisCommand', () => {
    // Source-level: the JSON.stringify call with type='retry_setup_phase'
    // and a phase field must appear.
    expect(SOURCE).toMatch(
      /sendJarvisCommand\(\s*JSON\.stringify\(\s*\{\s*type:\s*['"]retry_setup_phase['"]/,
    )
    expect(SOURCE).toMatch(/phase\s*\}/) // shorthand property in payload
  })

  it('renders a RETRY button bound to the per-phase retry handler', () => {
    expect(SOURCE).toMatch(/>\s*RETRY\s*</)
    expect(SOURCE).toMatch(/onClick=\{[^}]*onRetry\(\s*phase\s*\)/)
  })

  it('shows the error banner with role="alert" when state === "error"', () => {
    expect(SOURCE).toMatch(/row\.state\s*===\s*['"]error['"]/)
    expect(SOURCE).toMatch(/role=['"]alert['"]/)
  })
})

describe('SetupScreen — view-setup-log link', () => {
  it('guards OpenSetupLog call with a typeof check', () => {
    expect(SOURCE).toMatch(/OpenSetupLog/)
    expect(SOURCE).toMatch(/window\?\.go\?\.main\?\.App\?\.OpenSetupLog/)
    expect(SOURCE).toMatch(/typeof\s+fn\s*===\s*['"]function['"]/)
  })
})

describe('SetupScreen — z-index discipline', () => {
  it('uses zIndex 95 — above orb (50), below mic-perm banner (100)', () => {
    expect(SOURCE).toMatch(/zIndex:\s*95\b/)
  })

  it('uses position: fixed and inset: 0 for full-viewport coverage', () => {
    expect(SOURCE).toMatch(/position:\s*['"]fixed['"]/)
    expect(SOURCE).toMatch(/inset:\s*0/)
  })
})

describe('SetupScreen — animation', () => {
  it('spins the active glyph via a 1.4s linear infinite keyframe', () => {
    expect(SOURCE).toMatch(/@keyframes\s+setup-spin/)
    expect(SOURCE).toMatch(/setup-spin\s+1\.4s\s+linear\s+infinite/)
  })

  it('fades in on mount via a 250ms ease-out animation', () => {
    expect(SOURCE).toMatch(/setup-fade-in/)
    expect(SOURCE).toMatch(/@keyframes\s+setup-fade-in/)
    expect(SOURCE).toMatch(/250ms/)
  })
})

describe('SetupScreen — canonical phase ordering', () => {
  it('exports PHASE_ORDER matching the canonical 4-phase enum', () => {
    expect(__SETUP_SCREEN_PHASE_ORDER).toEqual([
      'python_install',
      'venv_install',
      'vibevoice_download',
      'whisper_download',
    ])
  })

  it('PHASE_ORDER appears in source as a const literal in the canonical order', () => {
    // The literal array MUST appear in source so the rendering loop is
    // statically auditable.
    expect(SOURCE).toMatch(
      /const\s+PHASE_ORDER[\s\S]*?'python_install'[\s\S]*?'venv_install'[\s\S]*?'vibevoice_download'[\s\S]*?'whisper_download'/,
    )
  })

  it('exports PHASE_LABEL covering all 4 phases', () => {
    expect(Object.keys(__SETUP_SCREEN_PHASE_LABEL).sort()).toEqual(
      [
        'python_install',
        'venv_install',
        'vibevoice_download',
        'whisper_download',
      ].sort(),
    )
  })

  it('PHASE_LABEL entries match the canonical UI copy', () => {
    expect(__SETUP_SCREEN_PHASE_LABEL.python_install).toBe(
      'Installing Python runtime',
    )
    expect(__SETUP_SCREEN_PHASE_LABEL.venv_install).toBe(
      'Installing voice pipeline',
    )
    expect(__SETUP_SCREEN_PHASE_LABEL.vibevoice_download).toBe(
      'Downloading VibeVoice (~1.9 GB)',
    )
    expect(__SETUP_SCREEN_PHASE_LABEL.whisper_download).toBe(
      'Downloading Whisper (~460 MB)',
    )
  })

  it('renders all 4 phase rows in a single map over PHASE_ORDER', () => {
    // Statically auditable: the loop renders one PhaseRowView per phase
    // with a 1-indexed display number.
    expect(SOURCE).toMatch(
      /PHASE_ORDER\.map\(\s*\(phase,\s*i\)\s*=>\s*\(\s*<PhaseRowView/,
    )
    expect(SOURCE).toMatch(/index=\{\s*i\s*\+\s*1\s*\}/)
  })
})
