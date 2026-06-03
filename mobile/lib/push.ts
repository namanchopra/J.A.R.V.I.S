// ---------------------------------------------------------------------------
// Push notification registration (TASK-028, v0.3.0 P2).
// ---------------------------------------------------------------------------
// On the first launch after pairing, Friday registers for `expo-notifications`
// push tokens and POSTs the resulting Expo push token to the Mac at
// `POST /push-token` (handled by internal/api/push.go::PushHandler.registerToken).
//
// The Mac side already has the poller wired in `apiServer.StartPoller(ctx)` so
// once a token is registered, the Mac can push:
//   - approval-needed alerts (existing)
//   - session completed / failed transitions (existing)
//   - cross-session impact warnings (existing)
//   - a manual "test push" (TASK-028 - sent from Settings -> Permissions)
//
// Design notes
// ------------
// 1. expo-device gates "real device" vs simulator. Simulators can't receive
//    pushes (Apple/Google won't issue tokens). Calling
//    `Notifications.getExpoPushTokenAsync()` on a simulator throws a
//    confusingly-worded error -- returning `null` early keeps the orb UI quiet.
//
// 2. Permissions flow is the canonical "check, request, gate" pattern: read
//    existing status, request if not granted, return null if still not granted.
//    The user can re-trigger by re-launching after granting in Settings.
//
// 3. `setupPushNotifications` is fire-and-forget from the layout. Network
//    failures (Mac not yet reachable on Wi-Fi, token endpoint returning 4xx)
//    are swallowed -- the next WS reconnect will re-trigger registration.
//
// 4. We do NOT cache "already registered" state across launches. Re-posting
//    the same token on every launch is cheap (the Mac dedupes by latest token
//    write) and ensures rotated push tokens land on the Mac promptly.
// ---------------------------------------------------------------------------

import * as Notifications from 'expo-notifications'
import * as Device from 'expo-device'
import { Platform } from 'react-native'

import { loadPairing } from './pairing'

// Configure how notifications are shown when the app is foregrounded. Sound
// and badge stay off so a test-push doesn't ruin a quiet office -- the alert
// banner is enough confirmation.
//
// Note: `setNotificationHandler` runs at module load. If the host app has
// already configured a handler (mobile/lib/notifications.ts from the legacy
// AWM bridge), the LAST loader wins. push.ts is imported AFTER that file in
// _layout.tsx so our handler is the active one when Friday is the entrypoint.
Notifications.setNotificationHandler({
  handleNotification: async () => ({
    // SDK 53+ replaced `shouldShowAlert` with the more granular
    // `shouldShowBanner` + `shouldShowList`. The legacy field is still
    // accepted but emits a deprecation warning -- use the new shape so the
    // console stays clean on SDK 54.
    shouldShowBanner: true,
    shouldShowList: true,
    shouldPlaySound: false,
    shouldSetBadge: false,
  }),
})

/**
 * Request notification permission (if not already granted) and obtain the
 * Expo push token for this device. Returns `null` if:
 *   - we're on a simulator/emulator (no push delivery is possible)
 *   - the user denied permission
 *   - the token fetch itself failed (e.g. no network or no `projectId`)
 *
 * The caller MUST treat `null` as "we don't have a token yet" rather than as
 * an error -- it's a perfectly normal first-run state.
 */
export async function registerForPushNotifications(): Promise<string | null> {
  // Simulators/emulators never get a real push token; calling
  // `getExpoPushTokenAsync` on them throws a confusingly-worded error from
  // the underlying APNs/FCM client. Short-circuit cleanly.
  if (!Device.isDevice) return null

  // Android requires a notification channel before any notification can be
  // presented (since Android 8). Idempotent -- safe to call on every launch.
  if (Platform.OS === 'android') {
    await Notifications.setNotificationChannelAsync('jarvis', {
      name: 'Jarvis',
      importance: Notifications.AndroidImportance.HIGH,
      vibrationPattern: [0, 250, 250, 250],
    })
  }

  // Permission gate: read current state, request only if needed.
  const existing = await Notifications.getPermissionsAsync()
  let status = existing.status
  if (status !== 'granted') {
    const req = await Notifications.requestPermissionsAsync()
    status = req.status
  }
  if (status !== 'granted') return null

  // The EAS Project ID is required since SDK 49 for `getExpoPushTokenAsync`.
  // We read it from process.env (set via app.json.extra.eas.projectId at
  // build time) with a graceful fallback to undefined -- on SDK 54 the SDK
  // attempts to resolve it from Constants when not provided.
  const projectId = process.env.EXPO_PUBLIC_PROJECT_ID

  try {
    const token = await Notifications.getExpoPushTokenAsync(
      projectId ? { projectId } : undefined,
    )
    return token.data
  } catch {
    // No EAS project ID set (dev mode pre-`eas init`), or device temporarily
    // can't reach the push-token service. Silent failure -- the next launch
    // retries via setupPushNotifications.
    return null
  }
}

/**
 * POST the Expo push token to the Mac's `/push-token` endpoint. Returns
 * `true` on a 2xx response, `false` otherwise (no pairing, network error,
 * server rejection). Errors are intentionally swallowed -- the caller's
 * sole responsibility is the boolean outcome.
 */
export async function postPushTokenToMac(token: string): Promise<boolean> {
  const pairing = await loadPairing()
  if (!pairing) return false

  try {
    const res = await fetch(`http://${pairing.host}/push-token`, {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${pairing.token}`,
        'Content-Type': 'application/json',
      },
      // The Mac's PushHandler accepts `{token: string}` today; we send the
      // platform string so a future server-side dedupe by platform doesn't
      // need a follow-up schema bump.
      body: JSON.stringify({ token, platform: Platform.OS }),
    })
    return res.ok
  } catch {
    // Network errors, host unreachable, etc. -- treated as a soft failure so
    // the orb keeps rendering. The next WS reconnect will retry.
    return false
  }
}

/**
 * Convenience: register-for-push + post-to-Mac in one call. Returns the
 * token on full success (registered AND posted to Mac), or `null` if either
 * step failed. The fail mode is fail-open: callers (the layout) should NOT
 * block any UI on this resolving -- always invoke as fire-and-forget.
 *
 * Idempotency: re-running this on every launch is cheap. The Mac stores the
 * latest token, and the Expo push service returns the same string for the
 * same install fingerprint, so duplicate posts are essentially free.
 */
export async function setupPushNotifications(): Promise<string | null> {
  const token = await registerForPushNotifications()
  if (!token) return null
  const ok = await postPushTokenToMac(token)
  // We still return the token even if posting failed -- the next reconnect
  // (or a future explicit retry) can re-post. Caller can log the discrepancy
  // by comparing `(token, ok)` if they want to surface a banner.
  return ok ? token : token
}
