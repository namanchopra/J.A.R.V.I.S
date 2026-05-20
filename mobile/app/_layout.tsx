// v0.3.0: rebuilt orb-first. Real entry point lands in TASK-019.
//
// TASK-006: wire expo-font for the HUD design tokens (mobile/lib/hud-tokens.ts).
// SF Mono OTFs are user-supplied (see mobile/assets/fonts/README.md). Until
// the user drops the OTFs in, useFonts() is given an empty map and the app
// falls through to platform mono via hud-tokens.ts. Splash screen stays up
// until font load resolves (success or error) so we never flash unstyled text.
import { useFonts } from 'expo-font';
import * as SplashScreen from 'expo-splash-screen';
import { Stack } from 'expo-router';
import { StatusBar } from 'expo-status-bar';
import { useEffect } from 'react';

import { colors } from '../lib/hud-tokens';

export { ErrorBoundary } from 'expo-router';

// Keep splash up while fonts (or their absence) resolve. Catch is a no-op
// because preventAutoHideAsync rejects on web -- we don't want to log that.
SplashScreen.preventAutoHideAsync().catch(() => {});

export default function RootLayout() {
  const [fontsLoaded, fontError] = useFonts({
    // Once SF Mono OTFs land in mobile/assets/fonts/, uncomment these two:
    // 'SF Mono': require('../assets/fonts/SFMono-Regular.otf'),
    // 'SF Mono Bold': require('../assets/fonts/SFMono-Bold.otf'),
  });

  useEffect(() => {
    if (fontsLoaded || fontError) {
      SplashScreen.hideAsync().catch(() => {});
    }
  }, [fontsLoaded, fontError]);

  // Render nothing while fonts are still resolving -- splash is up via the
  // preventAutoHideAsync above. If fontError fires we still proceed so the
  // app degrades gracefully to platform mono.
  if (!fontsLoaded && !fontError) return null;

  return (
    <>
      <StatusBar style="light" />
      <Stack
        screenOptions={{
          headerShown: false,
          contentStyle: { backgroundColor: colors.bg },
        }}
      />
    </>
  );
}
