import * as SecureStore from 'expo-secure-store';

const KEYS = {
  serverUrl: 'awm_server_url',
  token: 'awm_token',
} as const;

const DEFAULT_SERVER_URL = 'http://192.168.1.100:4422';

export const storage = {
  getServerUrl: async (): Promise<string> => {
    const url = await SecureStore.getItemAsync(KEYS.serverUrl);
    return url ?? DEFAULT_SERVER_URL;
  },

  setServerUrl: (url: string): Promise<void> =>
    SecureStore.setItemAsync(KEYS.serverUrl, url),

  getToken: (): Promise<string | null> =>
    SecureStore.getItemAsync(KEYS.token),

  setToken: (token: string): Promise<void> =>
    SecureStore.setItemAsync(KEYS.token, token),

  clearToken: (): Promise<void> =>
    SecureStore.deleteItemAsync(KEYS.token),
};
