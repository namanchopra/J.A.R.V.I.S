import * as SecureStore from 'expo-secure-store';

const KEYS = {
  serverUrl: 'awm_server_url',
  token: 'awm_token',
} as const;

// Keys the pairing flow (lib/pairing.ts) writes to. We re-read them here as
// a fallback because the REST API in lib/api.ts goes through storage but
// the pair QR flow never calls ``setServerUrl`` / ``setToken`` on this
// module -- it writes its own ``awm_pairing_*`` triple. Without this
// bridge, REST fetches hit the hard-coded DEFAULT_SERVER_URL on a network
// the phone isn't on, and every /dashboard / /approvals / /activity poll
// fails with "Network request failed" while WS (which reads loadPairing()
// directly) works fine.
// Must stay in sync with PAIRING_HOST_KEY / PAIRING_TOKEN_KEY in lib/pairing.ts
// (we hard-code instead of importing so storage.ts has zero circular deps).
const PAIRING_HOST_KEY = 'jarvis.host';
const PAIRING_TOKEN_KEY = 'jarvis.token';

const DEFAULT_SERVER_URL = 'http://192.168.1.100:4422';

async function readPairingServerUrl(): Promise<string | null> {
  const host = await SecureStore.getItemAsync(PAIRING_HOST_KEY);
  if (!host) return null;
  // Pairing stores bare ``host:port``. REST needs an ``http://`` scheme.
  // ``https://`` would require a cert on the Mac side, which we don't ship.
  if (host.startsWith('http://') || host.startsWith('https://')) return host;
  return `http://${host}`;
}

export const storage = {
  getServerUrl: async (): Promise<string> => {
    // Order: explicit override > pair record > hard-coded default. The
    // ``setServerUrl`` path stays available for power users who want to
    // point at a non-paired daemon (dev/local testing).
    const explicit = await SecureStore.getItemAsync(KEYS.serverUrl);
    if (explicit) return explicit;
    const fromPair = await readPairingServerUrl();
    if (fromPair) return fromPair;
    return DEFAULT_SERVER_URL;
  },

  setServerUrl: (url: string): Promise<void> =>
    SecureStore.setItemAsync(KEYS.serverUrl, url),

  getToken: async (): Promise<string | null> => {
    // Same dual-source story: prefer the explicit awm_token (legacy),
    // otherwise fall back to the pair token so REST requests get a valid
    // Authorization header.
    const explicit = await SecureStore.getItemAsync(KEYS.token);
    if (explicit) return explicit;
    return SecureStore.getItemAsync(PAIRING_TOKEN_KEY);
  },

  setToken: (token: string): Promise<void> =>
    SecureStore.setItemAsync(KEYS.token, token),

  clearToken: (): Promise<void> =>
    SecureStore.deleteItemAsync(KEYS.token),
};
