// ---------------------------------------------------------------------------
// usePipelineStatus — source-level + runtime contract test (v0.1.5).
//
// We split coverage into:
//   1. Source-level assertions — same pattern the other suites use, since the
//      frontend test harness does not ship jsdom / @testing-library/react.
//   2. Runtime assertions for the exported `isPipelineStatusEvent` type guard,
//      which is pure and can be exercised without a React tree.
// ---------------------------------------------------------------------------

import { describe, it, expect } from 'vitest'
import SOURCE from './use-pipeline-status.ts?raw'
import { isPipelineStatusEvent } from './use-pipeline-status'

// ---------------------------------------------------------------------------
// 1. Source-level contract
// ---------------------------------------------------------------------------

describe('usePipelineStatus source contract (v0.1.5)', () => {
  it('subscribes to the jarvis Wails event channel via EventsOn', () => {
    // The hook MUST listen on the existing `'jarvis'` channel — the daemon
    // multiplexes pipeline_status onto that channel alongside other events.
    expect(SOURCE).toMatch(
      /import\s*\{[^}]*EventsOn[^}]*\}\s*from\s+['"][^'"]*wailsjs\/runtime\/runtime['"]/,
    )
    expect(SOURCE).toMatch(/EventsOn\(\s*['"]jarvis['"]/)
  })

  it('refresh() dispatches a request_pipeline_status message', () => {
    // The refresh contract is documented in the v0.1.5 spec: send
    // {type: "request_pipeline_status"} via sendJarvisCommand.
    expect(SOURCE).toMatch(/sendJarvisCommand/)
    expect(SOURCE).toMatch(/['"]request_pipeline_status['"]/)
    // The payload must be JSON.stringify-ed so the daemon parses it the
    // same way it parses every other command on this channel.
    expect(SOURCE).toMatch(
      /JSON\.stringify\(\s*\{\s*type:\s*['"]request_pipeline_status['"]/,
    )
  })

  it('kicks off a refresh on mount so late-connecting clients repopulate', () => {
    // The mount-time `useEffect` MUST call `refresh()` so a HUD mounted
    // after a daemon restart doesn't sit on `status: null` forever.
    // Match the broader shape: a useEffect that wires EventsOn AND calls
    // refresh() before returning the cleanup.
    const useEffectBlock = SOURCE.match(/useEffect\(\(\)\s*=>\s*\{[\s\S]*?\},\s*\[[^\]]*\]\)/)
    expect(useEffectBlock).not.toBeNull()
    if (useEffectBlock) {
      expect(useEffectBlock[0]).toMatch(/refresh\(\)/)
    }
  })

  it('tears down the EventsOn subscription on unmount', () => {
    // Pattern: const cancel = EventsOn(...); return () => { cancel() }.
    // Without this we'd leak a listener per HUD mount — very visible during
    // HMR / route swaps.
    expect(SOURCE).toMatch(/const\s+cancel\s*=\s*EventsOn\(/)
    expect(SOURCE).toMatch(/return\s*\(\)\s*=>\s*\{\s*cancel\(\)/)
  })

  it('exports the PipelineStatusEvent type for downstream consumers', () => {
    // Both the HUD and DiagnosticsPanel import this type. Keep it exported
    // so the contract is co-located with the runtime guard.
    expect(SOURCE).toMatch(/export\s+interface\s+PipelineStatusEvent\b/)
  })
})

// ---------------------------------------------------------------------------
// 2. Runtime type-guard behavior
// ---------------------------------------------------------------------------

describe('isPipelineStatusEvent (type guard)', () => {
  const validEvent = {
    type: 'pipeline_status',
    tts: { provider: 'kokoro', voice: 'af_bella' },
    stt: { model: 'whisper-small.en' },
    llm: {
      provider: 'openrouter',
      model: 'google/gemini-2.5-flash',
      source: 'user-pick',
    },
    wake_word: { enabled: true, sensitivity: 0.5 },
  }

  it('accepts a well-formed pipeline_status payload', () => {
    expect(isPipelineStatusEvent(validEvent)).toBe(true)
  })

  it('rejects null / non-objects', () => {
    expect(isPipelineStatusEvent(null)).toBe(false)
    expect(isPipelineStatusEvent(undefined)).toBe(false)
    expect(isPipelineStatusEvent('pipeline_status')).toBe(false)
    expect(isPipelineStatusEvent(42)).toBe(false)
    expect(isPipelineStatusEvent([])).toBe(false)
  })

  it('rejects events with the wrong `type` discriminator', () => {
    expect(isPipelineStatusEvent({ ...validEvent, type: 'state_change' })).toBe(false)
    expect(isPipelineStatusEvent({ ...validEvent, type: 'pipeline_statuss' })).toBe(false)
  })

  it('rejects unknown tts.provider values', () => {
    expect(
      isPipelineStatusEvent({
        ...validEvent,
        tts: { provider: 'elevenlabs', voice: 'rachel' },
      }),
    ).toBe(false)
  })

  it('rejects unknown stt.model values', () => {
    expect(
      isPipelineStatusEvent({ ...validEvent, stt: { model: 'whisper-large' } }),
    ).toBe(false)
  })

  it('rejects unknown llm.provider values', () => {
    expect(
      isPipelineStatusEvent({
        ...validEvent,
        llm: { ...validEvent.llm, provider: 'openai' },
      }),
    ).toBe(false)
  })

  it('rejects unknown llm.source values', () => {
    expect(
      isPipelineStatusEvent({
        ...validEvent,
        llm: { ...validEvent.llm, source: 'default' },
      }),
    ).toBe(false)
  })

  it('rejects wake_word with wrong types', () => {
    expect(
      isPipelineStatusEvent({
        ...validEvent,
        wake_word: { enabled: 'true', sensitivity: 0.5 },
      }),
    ).toBe(false)
    expect(
      isPipelineStatusEvent({
        ...validEvent,
        wake_word: { enabled: true, sensitivity: '0.5' },
      }),
    ).toBe(false)
  })

  it('rejects events missing a top-level branch', () => {
    const { tts: _tts, ...noTTS } = validEvent
    void _tts
    expect(isPipelineStatusEvent(noTTS)).toBe(false)

    const { stt: _stt, ...noSTT } = validEvent
    void _stt
    expect(isPipelineStatusEvent(noSTT)).toBe(false)

    const { llm: _llm, ...noLLM } = validEvent
    void _llm
    expect(isPipelineStatusEvent(noLLM)).toBe(false)

    const { wake_word: _wake, ...noWake } = validEvent
    void _wake
    expect(isPipelineStatusEvent(noWake)).toBe(false)
  })

  it('rejects events with non-string llm.model / tts.voice', () => {
    expect(
      isPipelineStatusEvent({
        ...validEvent,
        llm: { ...validEvent.llm, model: 42 },
      }),
    ).toBe(false)
    expect(
      isPipelineStatusEvent({
        ...validEvent,
        tts: { provider: 'kokoro', voice: null },
      }),
    ).toBe(false)
  })
})
