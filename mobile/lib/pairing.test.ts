// ---------------------------------------------------------------------------
// Unit tests for `mobile/lib/pairing.ts` (TASK-020 acceptance criteria).
// ---------------------------------------------------------------------------
// These tests exercise the pure parser (parsePairingQR) directly. The
// SecureStore-backed save/load helpers are covered indirectly via the
// pair.tsx screen; mocking expo-secure-store here would only verify our own
// mock. Pure parser coverage is what the spec asks for.
// ---------------------------------------------------------------------------

// Mock expo-secure-store so the module under test imports cleanly under
// jest-expo without dragging in the native module that requires a device.
jest.mock('expo-secure-store', () => ({
  setItemAsync: jest.fn(async () => undefined),
  getItemAsync: jest.fn(async () => null),
  deleteItemAsync: jest.fn(async () => undefined),
}))

import { parsePairingQR } from './pairing'

describe('parsePairingQR', () => {
  it('returns the three fields for a valid jarvis://pair URL', () => {
    const raw = 'jarvis://pair?host=192.168.1.5:4422&token=abc&room=jarvis'
    const out = parsePairingQR(raw)
    expect(out).toEqual({
      host: '192.168.1.5:4422',
      token: 'abc',
      room: 'jarvis',
    })
  })

  it('returns null when the scheme is wrong', () => {
    expect(
      parsePairingQR('https://pair?host=192.168.1.5:4422&token=abc&room=r'),
    ).toBeNull()
    expect(
      parsePairingQR('jarvis://other?host=192.168.1.5:4422&token=abc&room=r'),
    ).toBeNull()
  })

  it('returns null when token is missing', () => {
    expect(
      parsePairingQR('jarvis://pair?host=192.168.1.5:4422&room=jarvis'),
    ).toBeNull()
  })

  it('returns null when host is missing', () => {
    expect(parsePairingQR('jarvis://pair?token=abc&room=jarvis')).toBeNull()
  })

  it('returns null when room is missing', () => {
    expect(
      parsePairingQR('jarvis://pair?host=192.168.1.5:4422&token=abc'),
    ).toBeNull()
  })

  it('returns null on empty string', () => {
    expect(parsePairingQR('')).toBeNull()
  })

  it('returns null when any field is empty after trim', () => {
    // host=  (empty value) -- URLSearchParams reads as "" which we reject.
    expect(
      parsePairingQR('jarvis://pair?host=&token=abc&room=r'),
    ).toBeNull()
    // whitespace-only token: URL-encoded space.
    expect(
      parsePairingQR('jarvis://pair?host=h&token=%20&room=r'),
    ).toBeNull()
  })

  it('decodes URL-encoded values', () => {
    // Token with `+` should decode to space via URLSearchParams.
    const raw = 'jarvis://pair?host=h.example%3A4422&token=ab%2Bcd&room=r'
    const out = parsePairingQR(raw)
    expect(out).toEqual({
      host: 'h.example:4422',
      token: 'ab+cd',
      room: 'r',
    })
  })
})
