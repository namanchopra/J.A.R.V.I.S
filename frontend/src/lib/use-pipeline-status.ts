// ---------------------------------------------------------------------------
// usePipelineStatus — subscribes to `pipeline_status` events emitted by the
// Python daemon over the existing `'jarvis'` Wails channel. Returns the
// latest event (narrowed via a runtime type guard) plus a `refresh()` helper
// that asks the daemon to re-emit its current state.
//
// Why a hook (not a global store): two consumers (HUD + Diagnostics panel)
// share the same shape and the same refresh contract, but neither needs
// cross-tree mutation. Each consumer mounts its own subscription. Wails'
// `EventsOn` fan-out is cheap — see FirstRunDownloadOverlay for the same
// pattern.
//
// Schema is FIXED by the daemon contract. Keep the type guard tight so a
// malformed payload (old daemon, partial release, etc.) never lights up the
// orb labels with garbage.
// ---------------------------------------------------------------------------

import { useCallback, useEffect, useState } from 'react'
import { EventsOn } from '../../wailsjs/runtime/runtime'
import { sendJarvisCommand } from './jarvis-api'

// ---------------------------------------------------------------------------
// Event schema — mirror of the daemon contract (see v0.1.5 plan).
// ---------------------------------------------------------------------------

export type TTSProvider = 'vibevoice' | 'kokoro' | 'cartesia'
export type STTModel = 'whisper-small.en' | 'whisper-tiny.en' | 'faster-whisper'
export type LLMProvider =
  | 'openrouter'
  | 'anthropic'
  | 'google'
  | 'ollama'
  | 'nvidia'
export type LLMSource = 'user-pick' | 'key-detected'

export interface PipelineStatusEvent {
  type: 'pipeline_status'
  tts: {
    provider: TTSProvider
    voice: string
  }
  stt: {
    model: STTModel
  }
  llm: {
    provider: LLMProvider
    model: string
    source: LLMSource
  }
  wake_word: {
    enabled: boolean
    sensitivity: number
  }
}

// ---------------------------------------------------------------------------
// Type guards — defensive narrowing because raw Wails payloads are unknown.
// ---------------------------------------------------------------------------

function isTTSProvider(v: unknown): v is TTSProvider {
  return v === 'vibevoice' || v === 'kokoro' || v === 'cartesia'
}

function isSTTModel(v: unknown): v is STTModel {
  return (
    v === 'whisper-small.en' ||
    v === 'whisper-tiny.en' ||
    v === 'faster-whisper'
  )
}

function isLLMProvider(v: unknown): v is LLMProvider {
  return (
    v === 'openrouter' ||
    v === 'anthropic' ||
    v === 'google' ||
    v === 'ollama' ||
    v === 'nvidia'
  )
}

function isLLMSource(v: unknown): v is LLMSource {
  return v === 'user-pick' || v === 'key-detected'
}

function asRecord(v: unknown): Record<string, unknown> | null {
  if (v === null || typeof v !== 'object') return null
  return v as Record<string, unknown>
}

export function isPipelineStatusEvent(v: unknown): v is PipelineStatusEvent {
  const o = asRecord(v)
  if (!o) return false
  if (o.type !== 'pipeline_status') return false

  const tts = asRecord(o.tts)
  if (!tts) return false
  if (!isTTSProvider(tts.provider)) return false
  if (typeof tts.voice !== 'string') return false

  const stt = asRecord(o.stt)
  if (!stt) return false
  if (!isSTTModel(stt.model)) return false

  const llm = asRecord(o.llm)
  if (!llm) return false
  if (!isLLMProvider(llm.provider)) return false
  if (typeof llm.model !== 'string') return false
  if (!isLLMSource(llm.source)) return false

  const wake = asRecord(o.wake_word)
  if (!wake) return false
  if (typeof wake.enabled !== 'boolean') return false
  if (typeof wake.sensitivity !== 'number') return false

  return true
}

// ---------------------------------------------------------------------------
// Hook
// ---------------------------------------------------------------------------

export interface UsePipelineStatusResult {
  /** Latest `pipeline_status` event the daemon has emitted, or null if none
   *  has arrived yet on this mount. */
  status: PipelineStatusEvent | null
  /** Epoch ms when the current `status` was received. 0 when none. */
  receivedAt: number
  /** Ask the daemon to re-emit its current pipeline status. Useful on mount
   *  for late-connecting clients, or as a manual "Request now" button. */
  refresh: () => void
}

/**
 * Subscribe to `pipeline_status` events on the `'jarvis'` Wails channel.
 *
 * - On mount, sends `{type: 'request_pipeline_status'}` so a late-mounted
 *   client doesn't sit forever with `status === null`.
 * - The subscription is torn down on unmount via the `EventsOn` return
 *   handle (same idiom FirstRunDownloadOverlay uses).
 */
export function usePipelineStatus(): UsePipelineStatusResult {
  const [status, setStatus] = useState<PipelineStatusEvent | null>(null)
  const [receivedAt, setReceivedAt] = useState<number>(0)

  const refresh = useCallback((): void => {
    void sendJarvisCommand(
      JSON.stringify({ type: 'request_pipeline_status' }),
    )
  }, [])

  useEffect(() => {
    const cancel = EventsOn('jarvis', (event: unknown) => {
      if (!isPipelineStatusEvent(event)) return
      setStatus(event)
      setReceivedAt(Date.now())
    })

    // Kick the daemon once on mount so a late-mounting HUD repopulates.
    refresh()

    return () => {
      cancel()
    }
  }, [refresh])

  return { status, receivedAt, refresh }
}
