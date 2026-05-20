// ---------------------------------------------------------------------------
// Unit tests for `mobile/lib/jarvis-ws.ts` (TASK-023 acceptance criteria).
// ---------------------------------------------------------------------------
// React Native's `WebSocket` is a global at runtime. In Jest (node env) we
// stub it with a FakeWebSocket that exposes the same surface the module
// uses (`new`, `binaryType`, `readyState`, `onopen/onmessage/onerror/onclose`,
// `send`, `close`, and the static `OPEN` constant). Each test reaches into
// the fake to drive lifecycle events synchronously so the test suite stays
// deterministic without `await new Promise(setImmediate)` dances.
// ---------------------------------------------------------------------------

// ---- pairing mock --------------------------------------------------------
// `loadPairing` is the only external dep -- swap it for a controllable
// jest.fn so we can simulate paired vs un-paired states per test.

jest.mock('./pairing', () => ({
  loadPairing: jest.fn(),
}))

import { loadPairing } from './pairing'
import { JarvisWS, type WSState } from './jarvis-ws'

const mockedLoadPairing = loadPairing as jest.MockedFunction<typeof loadPairing>

// ---- WebSocket fake ------------------------------------------------------
// Minimal stand-in for the runtime `WebSocket` global. Tracks every instance
// so a test can grab the latest one via `FakeWebSocket.last`.

const OPEN = 1
const CLOSED = 3

class FakeWebSocket {
  static OPEN = OPEN
  static last: FakeWebSocket | null = null
  static instances: FakeWebSocket[] = []

  url: string
  binaryType: 'arraybuffer' | 'blob' = 'blob'
  readyState: number = 0 // CONNECTING
  onopen: (() => void) | null = null
  onmessage: ((ev: { data: ArrayBuffer | string }) => void) | null = null
  onerror: ((ev: unknown) => void) | null = null
  onclose: (() => void) | null = null

  /** Records of `send` calls -- inspected by tests. */
  sent: Array<string | Uint8Array | ArrayBuffer> = []
  closed = false

  constructor(url: string) {
    this.url = url
    FakeWebSocket.last = this
    FakeWebSocket.instances.push(this)
  }

  send(data: string | Uint8Array | ArrayBuffer): void {
    this.sent.push(data)
  }

  close(): void {
    this.closed = true
    this.readyState = CLOSED
  }

  // ---- test driver helpers --------------------------------------------
  /** Simulate a successful handshake. */
  fireOpen(): void {
    this.readyState = OPEN
    this.onopen?.()
  }
  fireMessage(data: ArrayBuffer | string): void {
    this.onmessage?.({ data })
  }
  fireError(): void {
    this.onerror?.({})
  }
  fireClose(): void {
    this.readyState = CLOSED
    this.onclose?.()
  }
}

// Install fake on the global object before any tests run.
beforeAll(() => {
  ;(globalThis as unknown as { WebSocket: typeof FakeWebSocket }).WebSocket =
    FakeWebSocket
})

beforeEach(() => {
  jest.useFakeTimers()
  FakeWebSocket.last = null
  FakeWebSocket.instances = []
  mockedLoadPairing.mockReset()
  mockedLoadPairing.mockResolvedValue({
    host: '192.168.1.5:4422',
    token: 'tok-abc',
    room: 'jarvis',
  })
})

afterEach(() => {
  jest.useRealTimers()
})

// Tiny helper to drain the microtask queue between an `await connect()` and
// the test's first state-event assertion.
async function flush(): Promise<void> {
  await Promise.resolve()
  await Promise.resolve()
}

// ---------------------------------------------------------------------------
// Connection lifecycle
// ---------------------------------------------------------------------------

describe('JarvisWS.connect', () => {
  it('opens the WS with the bearer token in the query string', async () => {
    const client = new JarvisWS()
    await client.connect()
    await flush()
    expect(FakeWebSocket.last).not.toBeNull()
    expect(FakeWebSocket.last!.url).toBe(
      'ws://192.168.1.5:4422/ws/jarvis-mobile?token=tok-abc',
    )
    expect(FakeWebSocket.last!.binaryType).toBe('arraybuffer')
  })

  it('transitions disconnected -> connecting -> connected on open', async () => {
    const client = new JarvisWS()
    const states: WSState[] = []
    client.on('state', (s) => states.push(s))
    await client.connect()
    await flush()
    expect(states).toEqual(['connecting'])
    FakeWebSocket.last!.fireOpen()
    expect(states).toEqual(['connecting', 'connected'])
    expect(client.getState()).toBe('connected')
  })

  it('sends a hello JSON frame on open', async () => {
    const client = new JarvisWS()
    await client.connect()
    await flush()
    FakeWebSocket.last!.fireOpen()
    expect(FakeWebSocket.last!.sent).toHaveLength(1)
    expect(JSON.parse(FakeWebSocket.last!.sent[0] as string)).toEqual({
      type: 'hello',
      version: 'v0.3.0',
    })
  })

  it('emits error + throws if not paired', async () => {
    mockedLoadPairing.mockResolvedValueOnce(null)
    const client = new JarvisWS()
    const errors: Error[] = []
    client.on('error', (e) => errors.push(e))
    await expect(client.connect()).rejects.toThrow('Not paired')
    expect(errors).toHaveLength(1)
    expect(errors[0].message).toBe('Not paired')
  })
})

// ---------------------------------------------------------------------------
// Inbound message routing
// ---------------------------------------------------------------------------

describe('JarvisWS message routing', () => {
  it('routes binary frames to ttsAudioChunk listeners', async () => {
    const client = new JarvisWS()
    const chunks: Uint8Array[] = []
    client.on('ttsAudioChunk', (c) => chunks.push(c))
    await client.connect()
    await flush()
    FakeWebSocket.last!.fireOpen()

    const buf = new ArrayBuffer(4)
    new Uint8Array(buf).set([1, 2, 3, 4])
    FakeWebSocket.last!.fireMessage(buf)

    expect(chunks).toHaveLength(1)
    expect(Array.from(chunks[0])).toEqual([1, 2, 3, 4])
  })

  it('routes state_change JSON to stateChange listener', async () => {
    const client = new JarvisWS()
    const phases: Array<string | undefined> = []
    client.on('stateChange', (p) => phases.push(p.phase))
    await client.connect()
    await flush()
    FakeWebSocket.last!.fireOpen()
    FakeWebSocket.last!.fireMessage(
      JSON.stringify({ type: 'state_change', phase: 'listening' }),
    )
    expect(phases).toEqual(['listening'])
  })

  it('routes transcript JSON to transcript listener', async () => {
    const client = new JarvisWS()
    const turns: Array<{ role: 'user' | 'assistant'; text: string }> = []
    client.on('transcript', (t) => turns.push(t))
    await client.connect()
    await flush()
    FakeWebSocket.last!.fireOpen()
    FakeWebSocket.last!.fireMessage(
      JSON.stringify({ type: 'transcript', role: 'user', text: 'hello' }),
    )
    FakeWebSocket.last!.fireMessage(
      JSON.stringify({ type: 'transcript', role: 'assistant', text: 'hi back' }),
    )
    expect(turns).toEqual([
      { role: 'user', text: 'hello' },
      { role: 'assistant', text: 'hi back' },
    ])
  })

  it('drops transcript JSON missing role or text', async () => {
    const client = new JarvisWS()
    const turns: unknown[] = []
    client.on('transcript', (t) => turns.push(t))
    await client.connect()
    await flush()
    FakeWebSocket.last!.fireOpen()
    FakeWebSocket.last!.fireMessage(
      JSON.stringify({ type: 'transcript', role: 'user' }),
    )
    FakeWebSocket.last!.fireMessage(
      JSON.stringify({ type: 'transcript', text: 'orphan' }),
    )
    expect(turns).toHaveLength(0)
  })

  it('routes tts_audio_level JSON to ttsAudioLevel listener', async () => {
    const client = new JarvisWS()
    const levels: number[] = []
    client.on('ttsAudioLevel', (l) => levels.push(l))
    await client.connect()
    await flush()
    FakeWebSocket.last!.fireOpen()
    FakeWebSocket.last!.fireMessage(
      JSON.stringify({ type: 'tts_audio_level', level: 0.42 }),
    )
    FakeWebSocket.last!.fireMessage(
      JSON.stringify({ type: 'tts_audio_level', level: 'bad' }),
    )
    // Only the numeric level is forwarded.
    expect(levels).toEqual([0.42])
  })

  it('does not crash on malformed JSON', async () => {
    const client = new JarvisWS()
    await client.connect()
    await flush()
    FakeWebSocket.last!.fireOpen()
    expect(() => FakeWebSocket.last!.fireMessage('not-json {{{')).not.toThrow()
  })

  it('ignores unknown JSON message types', async () => {
    const client = new JarvisWS()
    const phases: unknown[] = []
    client.on('stateChange', (p) => phases.push(p))
    await client.connect()
    await flush()
    FakeWebSocket.last!.fireOpen()
    FakeWebSocket.last!.fireMessage(
      JSON.stringify({ type: 'something_new', foo: 'bar' }),
    )
    expect(phases).toHaveLength(0)
  })
})

// ---------------------------------------------------------------------------
// Outbound sends
// ---------------------------------------------------------------------------

describe('JarvisWS outbound', () => {
  it('sendAudio forwards bytes once the socket is open', async () => {
    const client = new JarvisWS()
    await client.connect()
    await flush()
    const sock = FakeWebSocket.last!

    // Drop while CONNECTING.
    const chunk = new Uint8Array([9, 9])
    client.sendAudio(chunk)
    expect(sock.sent).toHaveLength(0)

    sock.fireOpen() // hello JSON is sent here -> 1 frame
    client.sendAudio(chunk)
    expect(sock.sent).toHaveLength(2)
    expect(sock.sent[1]).toBe(chunk)
  })

  it('sendJSON forwards a JSON-encoded frame once open', async () => {
    const client = new JarvisWS()
    await client.connect()
    await flush()
    const sock = FakeWebSocket.last!
    sock.fireOpen()
    client.sendJSON({ type: 'text', message: 'hi' })
    const lastFrame = sock.sent[sock.sent.length - 1] as string
    expect(JSON.parse(lastFrame)).toEqual({ type: 'text', message: 'hi' })
  })

  it('sendAudio/sendJSON no-op when socket is closed', () => {
    const client = new JarvisWS()
    expect(() => client.sendAudio(new Uint8Array([1]))).not.toThrow()
    expect(() => client.sendJSON({ type: 'noop' })).not.toThrow()
  })
})

// ---------------------------------------------------------------------------
// Reconnect / disconnect
// ---------------------------------------------------------------------------

describe('JarvisWS reconnect', () => {
  it('schedules reconnect ~5s after first drop, then 10s, capped at 30s', async () => {
    const client = new JarvisWS()
    await client.connect()
    await flush()
    const first = FakeWebSocket.last!
    first.fireOpen()
    first.fireClose() // unexpected drop #1

    // attempt #0 -> 5s
    expect(FakeWebSocket.instances).toHaveLength(1)
    jest.advanceTimersByTime(4999)
    expect(FakeWebSocket.instances).toHaveLength(1)
    jest.advanceTimersByTime(1)
    expect(FakeWebSocket.instances).toHaveLength(2)

    // drop #2 without ever opening -> attempt #1 -> 10s
    FakeWebSocket.last!.fireClose()
    jest.advanceTimersByTime(9999)
    expect(FakeWebSocket.instances).toHaveLength(2)
    jest.advanceTimersByTime(1)
    expect(FakeWebSocket.instances).toHaveLength(3)

    // attempt #2 -> 20s
    FakeWebSocket.last!.fireClose()
    jest.advanceTimersByTime(19999)
    expect(FakeWebSocket.instances).toHaveLength(3)
    jest.advanceTimersByTime(1)
    expect(FakeWebSocket.instances).toHaveLength(4)

    // attempt #3 should be capped at 30s (not 40s).
    FakeWebSocket.last!.fireClose()
    jest.advanceTimersByTime(29999)
    expect(FakeWebSocket.instances).toHaveLength(4)
    jest.advanceTimersByTime(1)
    expect(FakeWebSocket.instances).toHaveLength(5)

    // attempt #4 should also be capped at 30s (not 80s).
    FakeWebSocket.last!.fireClose()
    jest.advanceTimersByTime(30000)
    expect(FakeWebSocket.instances).toHaveLength(6)
  })

  it('resets backoff after a successful open', async () => {
    const client = new JarvisWS()
    await client.connect()
    await flush()
    FakeWebSocket.last!.fireOpen()
    FakeWebSocket.last!.fireClose()
    jest.advanceTimersByTime(5000)
    expect(FakeWebSocket.instances).toHaveLength(2)
    // Connect succeeds again -> next drop should also be 5s, not 10s.
    FakeWebSocket.last!.fireOpen()
    FakeWebSocket.last!.fireClose()
    jest.advanceTimersByTime(4999)
    expect(FakeWebSocket.instances).toHaveLength(2)
    jest.advanceTimersByTime(1)
    expect(FakeWebSocket.instances).toHaveLength(3)
  })

  it('disconnect() prevents reconnect', async () => {
    const client = new JarvisWS()
    await client.connect()
    await flush()
    FakeWebSocket.last!.fireOpen()
    client.disconnect()
    // close fires naturally after disconnect()
    FakeWebSocket.last?.fireClose()
    jest.advanceTimersByTime(60000)
    expect(FakeWebSocket.instances).toHaveLength(1)
    expect(client.getState()).toBe('disconnected')
  })

  it('emits error + transitions to error state on socket error', async () => {
    const client = new JarvisWS()
    const errors: Error[] = []
    const states: WSState[] = []
    client.on('error', (e) => errors.push(e))
    client.on('state', (s) => states.push(s))
    await client.connect()
    await flush()
    FakeWebSocket.last!.fireError()
    expect(errors).toHaveLength(1)
    expect(errors[0]).toBeInstanceOf(Error)
    expect(states).toEqual(['connecting', 'error'])
  })
})

// ---------------------------------------------------------------------------
// Listener bus
// ---------------------------------------------------------------------------

describe('JarvisWS event bus', () => {
  it('on() returns a disposer that removes the listener', async () => {
    const client = new JarvisWS()
    const calls: WSState[] = []
    const off = client.on('state', (s) => calls.push(s))
    await client.connect()
    await flush()
    expect(calls).toEqual(['connecting'])
    off()
    FakeWebSocket.last!.fireOpen()
    expect(calls).toEqual(['connecting']) // no further updates
  })

  it('listener errors are swallowed so other subscribers still fire', async () => {
    const client = new JarvisWS()
    const ok: WSState[] = []
    client.on('state', () => {
      throw new Error('bad subscriber')
    })
    client.on('state', (s) => ok.push(s))
    await client.connect()
    await flush()
    expect(ok).toEqual(['connecting'])
  })
})
