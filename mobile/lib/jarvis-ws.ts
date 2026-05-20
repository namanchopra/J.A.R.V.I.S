// ---------------------------------------------------------------------------
// Jarvis mobile WebSocket client (TASK-023, v0.3.0 P1).
// ---------------------------------------------------------------------------
// This is the single connection manager the Friday app uses to talk to the
// Mac daemon. The daemon exposes `/ws/jarvis-mobile?token=<bearer>` (see
// internal/api/handlers_jarvis_mobile_ws.go) which accepts:
//
//   mobile -> Mac: binary frames (PCM/M4A audio from push-to-talk, TASK-021)
//                  JSON frames  (hello / text / audio_control)
//
//   Mac -> mobile: binary frames (TTS audio chunks, TASK-022)
//                  JSON frames  (state_change / transcript / tts_audio_level)
//
// This module is intentionally a *pure connection manager + event emitter*:
// it MUST NOT directly wire into PushToTalkButton (TASK-024) or AudioPlayer
// (future TASK-026). The orb subscribes via `on(...)` and feeds audio in via
// `sendAudio(...)`. Keeping this seam thin makes the 4 parallel agents safe
// to land on `feat/v0.3.0-music-mac-friday` without merge collisions.
//
// Reconnect policy: exponential backoff capped at 30s, starting at 5s. We
// reset the attempt counter on every successful `onopen` so a long-lived
// connection that drops once doesn't wait 30s on the next blip.
//
// `WebSocket` is a global in React Native (Hermes runtime); no `ws` package
// dep is added. `WebSocketMessageEvent` is the RN-typed message event shape.
// ---------------------------------------------------------------------------

import { loadPairing } from './pairing'

// ---- Public types --------------------------------------------------------

export type WSState = 'disconnected' | 'connecting' | 'connected' | 'error'

/**
 * Event names + payload signatures emitted by the connection.
 *
 * The orb subscribes via `on(event, listener)` and receives a disposer back.
 * All listeners are invoked synchronously inside the WebSocket callbacks --
 * keep them cheap. Errors thrown by listeners are swallowed so one bad
 * subscriber can't poison the rest of the bus.
 */
export interface JarvisWSEvents {
  /** Connection lifecycle (disconnected | connecting | connected | error). */
  state: (s: WSState) => void
  /** Daemon-side phase change, e.g. {phase:'listening'|'thinking'|'speaking'}. */
  stateChange: (payload: { phase?: string }) => void
  /** Live transcript snippet from STT (user) or LLM (assistant). */
  transcript: (payload: { role: 'user' | 'assistant'; text: string }) => void
  /** TTS RMS level in [0..1] for the orb's ring animation. */
  ttsAudioLevel: (level: number) => void
  /** A single binary TTS chunk. The AudioPlayer (TASK-026) will append these. */
  ttsAudioChunk: (chunk: Uint8Array) => void
  /** Low-level transport error -- logged + surfaced to the connection UI. */
  error: (err: Error) => void
}

// ---- Implementation ------------------------------------------------------

/**
 * Single-instance WebSocket client. Construct once at app start; call
 * `connect()` once pairing exists, then subscribe via `on(...)`.
 */
export class JarvisWS {
  // --- transport
  private ws: WebSocket | null = null
  private state: WSState = 'disconnected'

  // --- listener bus
  // Use a Map<string, Set<Function>> instead of a strongly-typed shape so
  // `on` can store mixed listener signatures behind one indirection. Type
  // safety is preserved at the call-site via the generic on/emit signatures.
  private listeners: Map<keyof JarvisWSEvents, Set<Function>> = new Map()

  // --- reconnect state
  private reconnectAttempt = 0
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null
  /**
   * When `disconnect()` is called explicitly we MUST NOT schedule a reconnect
   * even if `onclose` fires. This flag is reset by `connect()`.
   */
  private explicitlyDisconnected = false

  /**
   * Subscribe to an event. Returns a disposer the caller stores in their
   * effect cleanup. Calling the disposer twice is safe (idempotent).
   */
  on<K extends keyof JarvisWSEvents>(
    event: K,
    listener: JarvisWSEvents[K],
  ): () => void {
    let set = this.listeners.get(event)
    if (!set) {
      set = new Set()
      this.listeners.set(event, set)
    }
    set.add(listener as unknown as Function)
    return () => {
      set!.delete(listener as unknown as Function)
    }
  }

  private emit<K extends keyof JarvisWSEvents>(
    event: K,
    ...args: Parameters<JarvisWSEvents[K]>
  ): void {
    const set = this.listeners.get(event)
    if (!set) return
    for (const fn of set) {
      try {
        ;(fn as Function)(...args)
      } catch {
        // Listener threw -- swallow so other subscribers still get the event.
        // The UI's ErrorBoundary will catch any state-derived crashes.
      }
    }
  }

  /** Current transport state -- exposed for the connection banner UI. */
  getState(): WSState {
    return this.state
  }

  /**
   * Open the WebSocket using whatever pairing values are currently in
   * SecureStore. Resolves once the socket has been *requested*; actual
   * readiness is signalled via the `state` event reaching `'connected'`.
   *
   * Throws (and emits `error`) if the device is not yet paired.
   */
  async connect(): Promise<void> {
    const pairing = await loadPairing()
    if (!pairing) {
      const err = new Error('Not paired')
      this.emit('error', err)
      throw err
    }
    // Re-arm the auto-reconnect path -- a previous explicit disconnect would
    // otherwise short-circuit `onclose` and leave us stuck.
    this.explicitlyDisconnected = false
    this.openSocket(pairing.host, pairing.token)
  }

  /**
   * Tear down the socket and prevent auto-reconnect. Call this on logout or
   * when the user explicitly unpairs from Settings (TASK-029).
   */
  disconnect(): void {
    this.explicitlyDisconnected = true
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer)
      this.reconnectTimer = null
    }
    if (this.ws) {
      // Close synchronously -- `onclose` will fire but the `explicitlyDisconnected`
      // flag short-circuits the reconnect branch.
      this.ws.close()
      this.ws = null
    }
    this.setState('disconnected')
  }

  /**
   * Send a binary audio frame to the Mac daemon. Used by the push-to-talk
   * pipeline (TASK-021). Drops silently if the socket isn't OPEN -- the
   * push-to-talk button should observe `state` to gate recording, so this
   * branch only fires on race conditions during reconnect.
   */
  sendAudio(chunk: Uint8Array): void {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return
    // RN's WebSocket.send accepts ArrayBufferView directly; passing the
    // Uint8Array as-is is the canonical form.
    this.ws.send(chunk)
  }

  /**
   * Send a JSON control frame. The protocol-level shape is enforced by the
   * caller (see handlers_jarvis_mobile_ws.go for the server-side schema).
   */
  sendJSON(msg: Record<string, unknown>): void {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return
    this.ws.send(JSON.stringify(msg))
  }

  // ---- private -----------------------------------------------------------

  private openSocket(host: string, token: string): void {
    this.setState('connecting')
    // The Mac daemon currently terminates plain HTTP (mobileAPIPort defaults
    // to 4422 -- see internal/api/server.go). Using `ws://` keeps us aligned
    // with that listener; the eventual TLS upgrade (TASK-???) will swap the
    // scheme without touching this module's logic. Token is encoded so a
    // future token containing `&` or `?` can't break the URL.
    const url = `ws://${host}/ws/jarvis-mobile?token=${encodeURIComponent(token)}`
    const ws = new WebSocket(url)
    // Required so `event.data` for binary frames is an ArrayBuffer rather
    // than a Blob (Blob is awkward in RN -- no synchronous `.arrayBuffer()`).
    ws.binaryType = 'arraybuffer'

    ws.onopen = () => {
      // Successful handshake -- reset backoff so the *next* drop starts from
      // 5s again, not from wherever we left off.
      this.reconnectAttempt = 0
      this.setState('connected')
      // The daemon expects a hello on every fresh connection (see comments
      // at the top of handlers_jarvis_mobile_ws.go). Version is informational
      // -- the server logs it but doesn't gate behavior on it yet.
      this.sendJSON({ type: 'hello', version: 'v0.3.0' })
    }

    ws.onmessage = (event: WebSocketMessageEvent) => {
      // Binary frame -- TTS audio chunk. Fan out the raw bytes; the future
      // AudioPlayer subscriber owns RIFF wrapping / playback.
      if (event.data instanceof ArrayBuffer) {
        this.emit('ttsAudioChunk', new Uint8Array(event.data))
        return
      }

      // Text frame -- JSON control message. Parse defensively; malformed
      // JSON is dropped silently because spamming the error channel on
      // every bad frame would mask real transport errors.
      if (typeof event.data !== 'string') return
      let msg: Record<string, unknown>
      try {
        msg = JSON.parse(event.data) as Record<string, unknown>
      } catch {
        return
      }

      const type = msg.type as string | undefined
      if (type === 'state_change') {
        this.emit('stateChange', { phase: msg.phase as string | undefined })
        return
      }
      if (type === 'transcript') {
        const role = msg.role as 'user' | 'assistant' | undefined
        const text = msg.text as string | undefined
        // Both fields are required for a usable bubble -- drop incomplete
        // transcripts rather than rendering an empty one.
        if ((role === 'user' || role === 'assistant') && typeof text === 'string') {
          this.emit('transcript', { role, text })
        }
        return
      }
      if (type === 'tts_audio_level') {
        const level = msg.level
        if (typeof level === 'number') {
          this.emit('ttsAudioLevel', level)
        }
        return
      }
      // Unknown message types are intentionally ignored so adding new
      // daemon -> mobile events doesn't break older clients.
    }

    ws.onerror = () => {
      // RN's onerror payload is opaque -- the underlying error string isn't
      // reliably exposed cross-platform. Surface a generic Error so the UI
      // can log/display *something* rather than nothing.
      this.emit('error', new Error('WebSocket error'))
      this.setState('error')
      // No reconnect here -- `onclose` always fires after `onerror` and is
      // the canonical place to schedule retries.
    }

    ws.onclose = () => {
      this.ws = null
      if (this.explicitlyDisconnected) {
        // User initiated -- stay disconnected. `disconnect()` already set
        // state to 'disconnected'; re-setting is a no-op via setState's
        // dedupe.
        this.setState('disconnected')
        return
      }
      this.scheduleReconnect(host, token)
    }

    this.ws = ws
  }

  private scheduleReconnect(host: string, token: string): void {
    this.setState('disconnected')
    // Exponential schedule: 5s, 10s, 20s, then capped at 30s.
    // We compute *before* incrementing so attempt #0 -> 5s, attempt #1 -> 10s.
    const delay = Math.min(5000 * Math.pow(2, this.reconnectAttempt), 30000)
    this.reconnectAttempt++
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null
      this.openSocket(host, token)
    }, delay)
  }

  private setState(s: WSState): void {
    if (this.state === s) return
    this.state = s
    this.emit('state', s)
  }
}
