import * as Notifications from 'expo-notifications';
import { Platform } from 'react-native';
import { awmApi } from './api';

// Configure how notifications appear when the app is in the foreground
Notifications.setNotificationHandler({
  handleNotification: async () => ({
    shouldShowBanner: true,
    shouldShowList: true,
    shouldPlaySound: true,
    shouldSetBadge: false,
  }),
});

/**
 * Request permission and register for push notifications.
 * Sends the resulting Expo push token to the AWM server.
 * Call this once from the root layout on app launch.
 */
export async function registerForPushNotifications(): Promise<void> {
  // Android requires a notification channel
  if (Platform.OS === 'android') {
    await Notifications.setNotificationChannelAsync('awm', {
      name: 'AWM Notifications',
      importance: Notifications.AndroidImportance.HIGH,
      vibrationPattern: [0, 250, 250, 250],
    });
  }

  const { status: existingStatus } = await Notifications.getPermissionsAsync();
  let finalStatus = existingStatus;

  if (existingStatus !== 'granted') {
    const { status } = await Notifications.requestPermissionsAsync();
    finalStatus = status;
  }

  if (finalStatus !== 'granted') {
    // User denied -- silent failure, don't crash
    return;
  }

  try {
    const tokenData = await Notifications.getExpoPushTokenAsync();
    const token = tokenData.data;
    // Send to AWM server (fire-and-forget -- don't await, don't crash on failure)
    awmApi.registerPushToken(token).catch(() => {
      /* server may not be running yet */
    });
  } catch {
    // Simulator or no network -- silent failure
  }
}
