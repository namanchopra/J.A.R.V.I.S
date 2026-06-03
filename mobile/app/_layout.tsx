// v0.3.0: rebuilt orb-first. Real entry point lands in TASK-019.
//
// TASK-006: wire expo-font for the HUD design tokens (mobile/lib/hud-tokens.ts).
// SF Mono OTFs are user-supplied (see mobile/assets/fonts/README.md). Until
// the user drops the OTFs in, useFonts() is given an empty map and the app
// falls through to platform mono via hud-tokens.ts. Splash screen stays up
// until font load resolves (success or error) so we never flash unstyled text.
//
// TASK-020: on mount, check SecureStore for a pairing payload. If missing
// (first launch), redirect to /pair. The check runs in parallel with the
// font loader so we never block on it -- both resolve into the same
// `null` short-circuit until they're ready.
//
// TASK-028: once we know pairing exists, fire `setupPushNotifications()` in
// a fire-and-forget effect so the Mac learns this device's Expo push token.
// Intentionally NOT awaited -- the orb must not be blocked on permissions
// prompts. See mobile/lib/push.ts for the full registration flow.
import { useFonts } from 'expo-font';
import * as SplashScreen from 'expo-splash-screen';
import { Redirect, Stack } from 'expo-router';
import { StatusBar } from 'expo-status-bar';
import { useEffect, useState } from 'react';
import { SafeAreaProvider } from 'react-native-safe-area-context';

import { colors } from '../lib/hud-tokens';
import { loadPairing } from '../lib/pairing';
import { setupPushNotifications } from '../lib/push';

export { ErrorBoundary } from 'expo-router';

// Keep splash up while fonts (or their absence) resolve. Catch is a no-op
// because preventAutoHideAsync rejects on web -- we don't want to log that.
SplashScreen.preventAutoHideAsync().catch(() => {});

/**
 * Tri-state pairing flag:
 *   * `null`     -- SecureStore read is still in flight (first frame after mount)
 *   * `false`    -- SecureStore returned no payload; redirect to /pair
 *   * `true`     -- pairing payload exists; render the orb stack
 *
 * We collapse the payload itself to a boolean here because the layout
 * doesn't need the host/token/room values -- those are consumed by the WS
 * client in TASK-023. Keeping the layout state minimal also means a future
 * "re-pair" action just toggles this flag back to false (TASK-029).
 */
type PairingState = boolean | null;

export default function RootLayout() {
  const [fontsLoaded, fontError] = useFonts({
    // Once SF Mono OTFs land in mobile/assets/fonts/, uncomment these two:
    // 'SF Mono': require('../assets/fonts/SFMono-Regular.otf'),
    // 'SF Mono Bold': require('../assets/fonts/SFMono-Bold.otf'),
  });

  const [paired, setPaired] = useState<PairingState>(null);

  // Pairing check runs in parallel with font loading. A SecureStore read is
  // ~5-15ms on a modern phone, so this usually resolves before the font
  // loader does. If it rejects (Keychain error), default to "not paired" so
  // the user lands on the QR scanner where they can retry.
  useEffect(() => {
    let cancelled = false;
    loadPairing()
      .then((p) => {
        if (!cancelled) setPaired(p !== null);
      })
      .catch(() => {
        if (!cancelled) setPaired(false);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    if (fontsLoaded || fontError) {
      SplashScreen.hideAsync().catch(() => {});
    }
  }, [fontsLoaded, fontError]);

  // TASK-028: once we transition to "paired", fire the push-notification
  // registration in the background. Re-running on every launch is cheap and
  // resilient against rotated Expo push tokens or a Mac that wasn't yet
  // reachable on the first attempt. Fire-and-forget on purpose -- any thrown
  // error is swallowed inside push.ts so the orb is never blocked on it.
  useEffect(() => {
    if (paired !== true) return;
    void setupPushNotifications();
  }, [paired]);

  // Render nothing while EITHER fonts or pairing is still resolving -- splash
  // is up via the preventAutoHideAsync above. If fontError fires we still
  // proceed so the app degrades gracefully to platform mono.
  if (!fontsLoaded && !fontError) return null;
  if (paired === null) return null;

  // First launch (no token stored) lands on the pairing screen. We use
  // <Redirect> rather than router.replace() so the redirect happens during
  // render -- avoids a flash of the orb stack before the navigation fires.
  // We still render <Stack /> after the redirect because Stack must be the
  // root for expo-router; declaring the pair route inside the stack is what
  // lets the redirect target resolve correctly.
  if (!paired) {
    return (
      <SafeAreaProvider>
        <StatusBar style="light" />
        <Stack
          screenOptions={{
            headerShown: false,
            contentStyle: { backgroundColor: colors.bg },
          }}
        />
        <Redirect href="/pair" />
      </SafeAreaProvider>
    );
  }

  return (
    <SafeAreaProvider>
      <StatusBar style="light" />
      <Stack
        screenOptions={{
          headerShown: false,
          contentStyle: { backgroundColor: colors.bg },
        }}
      />
    </SafeAreaProvider>
  );
}
