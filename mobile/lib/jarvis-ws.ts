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

// ---- Base64 helper -------------------------------------------------------
// Hermes exposes a global `atob`, but tests run under Vitest where `atob`
// is also present (Node 20+). Falling back to a manual decode keeps the
// module portable to any environment we might lift it into. Internal-only;
// not exported.

const B64_ALPHABET =
  'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/'

function decodeBase64ToUint8(b64: string): Uint8Array {
  const g = globalThis as { atob?: (s: string) => string }
  if (typeof g.atob === 'function') {
    const bin = g.atob(b64)
    const out = new Uint8Array(bin.length)
    for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i) & 0xff
    return out
  }
  // Manual decode (vanilla loop, no Buffer dep -- RN doesn't ship Buffer).
  const cleaned = b64.replace(/[^A-Za-z0-9+/=]/g, '')
  const padded = cleaned.replace(/=+$/, '')
  const outLen = (padded.length * 3) >> 2
  const out = new Uint8Array(outLen)
  let oi = 0
  for (let i = 0; i < padded.length; i += 4) {
    const c0 = B64_ALPHABET.indexOf(padded.charAt(i))
    const c1 = B64_ALPHABET.indexOf(padded.charAt(i + 1))
    const c2 = B64_ALPHABET.indexOf(padded.charAt(i + 2))
    const c3 = B64_ALPHABET.indexOf(padded.charAt(i + 3))
    const triplet = (c0 << 18) | (c1 << 12) | ((c2 & 63) << 6) | (c3 & 63)
    out[oi++] = (triplet >> 16) & 0xff
    if (i + 2 < padded.length) out[oi++] = (triplet >> 8) & 0xff
    if (i + 3 < padded.length) out[oi++] = triplet & 0xff
  }
  return out
}

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
  /**
   * A single TTS audio chunk. `sampleRate` is the daemon-advertised rate
   * for the chunk (currently 16kHz from MacOSSayTTSService); when absent
   * the chunk came in as a raw binary frame and callers should assume the
   * default 16kHz. Subscribers must adapt their player's sample rate to
   * match, otherwise playback will be slowed/sped.
   */
  ttsAudioChunk: (chunk: Uint8Array, sampleRate?: number) => void
  /**
   * Periodic dashboard snapshot pushed by the Go server. Replaces REST
   * polling because Expo Go on the latest SDK no longer auto-bypasses iOS
   * App Transport Security for plain http:// fetches.
   */
  statsSnapshot: (stats: StatsSnapshot) => void
  /** Low-level transport error -- logged + surfaced to the connection UI. */
  error: (err: Error) => void
}

/**
 * Shape of the ``stats_snapshot`` WS message payload. Mirrors MobileStats in
 * internal/api/handlers_jarvis_mobile_ws.go -- keep field names in lockstep.
 */
export interface StatsSnapshot {
  activeSessions: number
  pendingApprovals: number
  runningTasks: number
  eventsToday: number
  latestActivity: string
  /**
   * Next upcoming Google Calendar event, or null when none is queued
   * (calendar empty, not connected, or upstream error). The server
   * omits the field from the payload in those cases; we coerce missing
   * to `null` so consumers can use a single null-check.
   */
  nextEvent: NextEventSnapshot | null
}

export interface NextEventSnapshot {
  title: string
  startIso: string
  relativeTime: string
}

function numberOr(value: unknown, fallback: number): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : fallback
}

function coerceNextEvent(raw: unknown): NextEventSnapshot | null {
  if (!raw || typeof raw !== 'object') return null
  const obj = raw as Record<string, unknown>
  const title = typeof obj.title === 'string' ? obj.title : ''
  const startIso = typeof obj.startIso === 'string' ? obj.startIso : ''
  const relativeTime = typeof obj.relativeTime === 'string' ? obj.relativeTime : ''
  // If every field is empty after coercion the payload was malformed --
  // treat as no-event rather than rendering an empty tile.
  if (!title && !startIso && !relativeTime) return null
  return { title, startIso, relativeTime }
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
    // Idempotent: if a socket is already open or being opened, don't start
    // a second one. The FridayRoot effect re-runs under React StrictMode
    // and Expo's Fast Refresh -- without this guard each remount would
    // race a second WebSocket onto the daemon, producing the duplicate
    // `mobile client connected` lines + backoff flapping observed on Mac.
    if (this.ws) {
      if (
        this.ws.readyState === WebSocket.OPEN ||
        this.ws.readyState === WebSocket.CONNECTING
      ) {
        // Re-arm in case a prior explicit disconnect set it; the open
        // socket is still fine.
        this.explicitlyDisconnected = false
        return
      }
    }
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
    const rs = this.ws ? this.ws.readyState : -1
    console.log('[JarvisWS] sendAudio called', {
      bytes: chunk.byteLength,
      readyState: rs,
      open: rs === WebSocket.OPEN,
    })
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
      console.warn('[JarvisWS] sendAudio DROPPED -- socket not OPEN', { readyState: rs })
      return
    }
    // RN's WebSocket.send accepts ArrayBufferView directly; passing the
    // Uint8Array as-is is the canonical form.
    this.ws.send(chunk)
    console.log('[JarvisWS] sendAudio: bytes sent to ws')
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
      if (type === 'stats_snapshot') {
        const raw = msg.stats as Record<string, unknown> | undefined
        console.log('[JarvisWS] stats_snapshot received', { raw })
        if (raw && typeof raw === 'object') {
          // Coerce defensively -- bad JSON from a future server version
          // shouldn't break the home screen.
          const snap: StatsSnapshot = {
            activeSessions: numberOr(raw.activeSessions, 0),
            pendingApprovals: numberOr(raw.pendingApprovals, 0),
            runningTasks: numberOr(raw.runningTasks, 0),
            eventsToday: numberOr(raw.eventsToday, 0),
            latestActivity:
              typeof raw.latestActivity === 'string' ? raw.latestActivity : '',
            nextEvent: coerceNextEvent(raw.nextEvent),
          }
          this.emit('statsSnapshot', snap)
        }
        return
      }
      if (type === 'mobile_tts') {
        // The daemon ships PCM as base64 inside a JSON envelope (not a raw
        // binary frame) so the Go bridge can broadcast through its
        // text-message path. Decode here and fan out as ttsAudioChunk so
        // the AudioPlayer subscriber doesn't need to know the wire format.
        const dataB64 = msg.data
        if (typeof dataB64 !== 'string' || dataB64.length === 0) return
        let bytes: Uint8Array
        try {
          bytes = decodeBase64ToUint8(dataB64)
        } catch {
          // Malformed base64 -- drop the chunk silently. The next chunk
          // will likely be fine; spamming `error` would mask real issues.
          return
        }
        const sr = typeof msg.sampleRate === 'number' ? msg.sampleRate : undefined
        this.emit('ttsAudioChunk', bytes, sr)
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
