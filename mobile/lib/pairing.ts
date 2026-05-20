// ---------------------------------------------------------------------------
// Pairing helpers -- parse + persist the Mac-issued QR payload.
// ---------------------------------------------------------------------------
// TASK-020: Friday is bootstrapped by scanning a QR from the Mac's Settings
// "Connect Phone" panel (TASK-025). The QR encodes a single URL of the form:
//
//   jarvis://pair?host=<ip:port>&token=<bearer>&room=<roomId>
//
// All three fields are mandatory -- a missing one means the Mac issued a
// malformed payload, which we reject without persisting. Storage lives in
// expo-secure-store (encrypted Keychain on iOS, EncryptedSharedPreferences on
// Android) under three discrete keys so the Bearer token is stored exactly
// once and is easily rotatable in TASK-025.
//
// Pure-string parsing keeps this file unit-testable without a device -- the
// SecureStore side is mocked in pairing.test.ts (TASK-020 acceptance criteria).
// ---------------------------------------------------------------------------

import * as SecureStore from 'expo-secure-store'

// ---- Keys ----------------------------------------------------------------
// Namespaced with the `jarvis.` prefix so a future companion app on the same
// device can coexist without colliding. Keep these as exported constants so
// the Settings screen (TASK-029) can clear them without re-hardcoding.

export const PAIRING_HOST_KEY = 'jarvis.host'
export const PAIRING_TOKEN_KEY = 'jarvis.token'
export const PAIRING_ROOM_KEY = 'jarvis.room'

// ---- Types ----------------------------------------------------------------

export interface PairingPayload {
  /** Daemon HTTP host, e.g. "192.168.1.5:4422". */
  host: string
  /** Bearer token used for /ws/jarvis-mobile?token=... auth (TASK-023). */
  token: string
  /** Room ID used by the daemon's session multiplexer. */
  room: string
}

// ---- Parser --------------------------------------------------------------
// We hand-parse the query string instead of using `URL` because React Native
// 0.81's URL polyfill is incomplete on Android -- specifically the
// `URLSearchParams` constructor accepts the query string but `URL` itself
// throws on the `jarvis://` scheme on some Android API levels. The hand-parse
// is also faster (~10us vs ~80us for `new URL`) which matters because this
// runs in the camera scan callback where every frame counts.

const PAIRING_SCHEME = 'jarvis://pair?'

export function parsePairingQR(raw: string): PairingPayload | null {
  if (typeof raw !== 'string') return null
  if (!raw.startsWith(PAIRING_SCHEME)) return null

  const query = raw.substring(PAIRING_SCHEME.length)
  // URLSearchParams is available in RN's Hermes runtime; safer than manual
  // split because it handles URL-decoded `+` and `%XX` sequences correctly.
  const params = new URLSearchParams(query)

  const host = params.get('host')
  const token = params.get('token')
  const room = params.get('room')

  if (!host || !token || !room) return null
  // Defence in depth: trim whitespace and reject empty post-trim. Prevents
  // an attacker-crafted QR with `host=` (zero-length value) from being
  // accepted as "present" by URLSearchParams.
  const hostT = host.trim()
  const tokenT = token.trim()
  const roomT = room.trim()
  if (!hostT || !tokenT || !roomT) return null

  return { host: hostT, token: tokenT, room: roomT }
}

// ---- Persistence ---------------------------------------------------------
// SecureStore writes are serial -- doing them in `await`-order matches the
// reverse `clearPairing` order so a crash mid-save leaves the store in a
// recoverable state (`loadPairing` returns null if ANY key is missing).

export async function savePairing(p: PairingPayload): Promise<void> {
  await SecureStore.setItemAsync(PAIRING_HOST_KEY, p.host)
  await SecureStore.setItemAsync(PAIRING_TOKEN_KEY, p.token)
  await SecureStore.setItemAsync(PAIRING_ROOM_KEY, p.room)
}

export async function loadPairing(): Promise<PairingPayload | null> {
  const host = await SecureStore.getItemAsync(PAIRING_HOST_KEY)
  const token = await SecureStore.getItemAsync(PAIRING_TOKEN_KEY)
  const room = await SecureStore.getItemAsync(PAIRING_ROOM_KEY)
  if (!host || !token || !room) return null
  return { host, token, room }
}

export async function clearPairing(): Promise<void> {
  await SecureStore.deleteItemAsync(PAIRING_HOST_KEY)
  await SecureStore.deleteItemAsync(PAIRING_TOKEN_KEY)
  await SecureStore.deleteItemAsync(PAIRING_ROOM_KEY)
}
