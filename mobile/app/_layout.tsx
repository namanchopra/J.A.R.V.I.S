import FontAwesome from '@expo/vector-icons/FontAwesome';
import { DarkTheme, ThemeProvider } from '@react-navigation/native';
import { useFonts } from 'expo-font';
import * as Notifications from 'expo-notifications';
import { router, Stack } from 'expo-router';
import * as SplashScreen from 'expo-splash-screen';
import { StatusBar } from 'expo-status-bar';
import { useEffect } from 'react';
import 'react-native-reanimated';

import { registerForPushNotifications } from '../lib/notifications';
import { JARVIS, jarvisHeaderTheme } from '@/constants/Colors';

export {
  // Catch any errors thrown by the Layout component.
  ErrorBoundary,
} from 'expo-router';

export const unstable_settings = {
  initialRouteName: '(tabs)',
};

// Prevent the splash screen from auto-hiding before asset loading is complete.
SplashScreen.preventAutoHideAsync();

export default function RootLayout() {
  const [loaded, error] = useFonts({
    SpaceMono: require('../assets/fonts/SpaceMono-Regular.ttf'),
    ...FontAwesome.font,
  });

  useEffect(() => {
    if (error) throw error;
  }, [error]);

  useEffect(() => {
    if (loaded) {
      SplashScreen.hideAsync();
    }
  }, [loaded]);

  useEffect(() => {
    // Register for push notifications on every launch (re-registers token in case it expired)
    registerForPushNotifications();

    // Deep-link: tapping a notification opens the approvals screen
    const subscription = Notifications.addNotificationResponseReceivedListener(
      (response) => {
        const screen = response.notification.request.content.data?.screen;
        if (screen === 'approvals') {
          router.push('/approvals');
        }
      },
    );

    return () => subscription.remove();
  }, []);

  if (!loaded) {
    return null;
  }

  return <RootLayoutNav />;
}

function RootLayoutNav() {
  return (
    <ThemeProvider value={DarkTheme}>
      <StatusBar style="light" />
      <Stack
        screenOptions={{
          ...jarvisHeaderTheme,
          contentStyle: { backgroundColor: JARVIS.bg },
        }}
      >
        <Stack.Screen name="(tabs)" options={{ headerShown: false }} />
        <Stack.Screen
          name="approvals"
          options={{
            title: 'Approvals',
            presentation: 'modal',
            ...jarvisHeaderTheme,
          }}
        />
        <Stack.Screen
          name="sessions/[id]"
          options={{
            headerShown: true,
            ...jarvisHeaderTheme,
          }}
        />
        <Stack.Screen
          name="tasks/[id]"
          options={{
            headerShown: true,
            ...jarvisHeaderTheme,
          }}
        />
        <Stack.Screen
          name="repos/[name]"
          options={{
            headerShown: true,
            ...jarvisHeaderTheme,
          }}
        />
        <Stack.Screen
          name="launch"
          options={{
            title: 'Launch Session',
            headerShown: true,
            presentation: 'modal',
            ...jarvisHeaderTheme,
          }}
        />
      </Stack>
    </ThemeProvider>
  );
}
