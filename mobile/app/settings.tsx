// ---------------------------------------------------------------------------
// Settings screen -- TASK-029 minimal Friday settings.
// ---------------------------------------------------------------------------
// Reached via the gear button on the orb screen (mobile/app/index.tsx).
// Three pieces of UX:
//
//   1. STATUS section
//      * HOST            -- the paired Mac's host:port from SecureStore.
//      * CONNECTION      -- result of the most recent "Test connection" press
//                           (GREEN with latency, RED with reason, or idle).
//
//   2. ACTIONS section
//      * Test connection -- HTTP GET /ping with the bearer token. Mac's
//                           Echo mobile API exposes /ping (see internal/api).
//                           Success flips state to 'reachable' + records the
//                           round-trip latency. Failure flips to 'unreachable'.
//      * Re-pair         -- nukes the SecureStore pairing payload via
//                           clearPairing() and navigates to /pair. This is
//                           the only "destructive" path; we render it in
//                           amber + the destructive button style.
//
//   3. Back button       -- explicit back-to-orb affordance so users without
//                           the iOS edge-swipe habit can return reliably.
//
// We intentionally keep this screen synchronous / no WS subscription. TASK-029
// originally hinted at a live daemon state row, but the prompt for this
// implementation pinned scope to: host + ping + re-pair. Adding a WS state
// subscription here would also re-open the JarvisWS singleton solely for a
// settings screen, which is wasteful when the orb screen already holds one.
// A follow-up task can hoist the daemon state into a context if needed.
// ---------------------------------------------------------------------------

import { useEffect, useState } from 'react'
import {
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  View,
} from 'react-native'
import { useRouter } from 'expo-router'
import { useSafeAreaInsets } from 'react-native-safe-area-context'

import { colors, fontFamilies, spacing } from '../lib/hud-tokens'
import {
  clearPairing,
  loadPairing,
  type PairingPayload,
} from '../lib/pairing'

// ---- Types ---------------------------------------------------------------

/**
 * Connection-test result lifecycle. `unknown` is the initial idle state
 * before the user has pressed the test button; `testing` shows the inflight
 * label so multiple rapid presses don't visually no-op.
 */
type ConnState = 'unknown' | 'reachable' | 'unreachable' | 'testing'

// ---- Constants -----------------------------------------------------------
// Ping timeout: the Mac's Echo /ping handler is sub-millisecond on local
// network, so 3s is generous. We use AbortController instead of letting
// fetch hang forever -- a slow Wi-Fi roam can otherwise leave the button
// in 'PINGING…' indefinitely.

const PING_TIMEOUT_MS = 3000
const PING_PATH = '/ping'

// ---- Screen --------------------------------------------------------------

export default function SettingsScreen(): React.ReactElement {
  const router = useRouter()
  // Safe-area insets: settings is a regular scroll view, so we feed
  // paddingTop/paddingBottom directly into the contentContainerStyle so
  // the first row clears the Dynamic Island and the back button clears
  // the home indicator.
  const insets = useSafeAreaInsets()
  const [conn, setConn] = useState<ConnState>('unknown')
  const [latencyMs, setLatencyMs] = useState<number | null>(null)
  // Pairing payload loaded from SecureStore. `undefined` = still loading,
  // `null` = no pairing stored (shouldn't happen because root layout
  // redirects to /pair when missing, but we defend in depth).
  const [pairing, setPairing] = useState<PairingPayload | null | undefined>(
    undefined,
  )

  // ---- Initial SecureStore load --------------------------------------------
  // Mirrors the root layout's pattern. We don't block the render -- the
  // STATUS row simply shows "—" until the load resolves, which on a modern
  // phone is sub-frame anyway. Errors fall through to null so the user can
  // still hit Re-pair to recover.
  useEffect(() => {
    let cancelled = false
    loadPairing()
      .then((p) => {
        if (!cancelled) setPairing(p)
      })
      .catch(() => {
        if (!cancelled) setPairing(null)
      })
    return () => {
      cancelled = true
    }
  }, [])

  // ---- Actions ------------------------------------------------------------

  const onRepair = async (): Promise<void> => {
    // We clear BEFORE navigating so a fast user can't race back to /settings
    // after the navigation lands with stale data still in SecureStore. If
    // clearPairing throws (Keychain locked? extremely rare) we surface a
    // best-effort log and still navigate -- the worst case is the user lands
    // on /pair which itself overwrites the stored values on next scan.
    try {
      await clearPairing()
    } catch (err) {
      console.warn('SettingsScreen: clearPairing failed', err)
    }
    router.replace('/pair')
  }

  const onTestConnection = async (): Promise<void> => {
    if (!pairing) {
      // Nothing to ping against. This branch is mostly defensive --
      // unpaired users shouldn't be able to reach this screen.
      setConn('unreachable')
      setLatencyMs(null)
      return
    }
    setConn('testing')
    setLatencyMs(null)

    // AbortController for the timeout. fetch() in RN does not honour a
    // `timeout` option, so we wire it through `signal`.
    const controller = new AbortController()
    const timer = setTimeout(() => controller.abort(), PING_TIMEOUT_MS)

    const t0 = Date.now()
    try {
      const res = await fetch(`http://${pairing.host}${PING_PATH}`, {
        method: 'GET',
        headers: { Authorization: `Bearer ${pairing.token}` },
        signal: controller.signal,
      })
      if (res.ok) {
        setConn('reachable')
        setLatencyMs(Date.now() - t0)
      } else {
        // Non-2xx response (e.g. 401 if the bearer was rotated) is treated
        // as "unreachable" for UX purposes -- the user just needs to re-pair
        // either way. We could split this into a third state, but the
        // current 2-state UI keeps the spec minimal.
        setConn('unreachable')
        setLatencyMs(null)
      }
    } catch {
      // Network error, DNS failure, timeout (AbortError), bad host -- all
      // collapse into the same UX: red dot, no latency.
      setConn('unreachable')
      setLatencyMs(null)
    } finally {
      clearTimeout(timer)
    }
  }

  // ---- Render -------------------------------------------------------------

  return (
    <ScrollView
      style={styles.container}
      contentContainerStyle={[
        styles.content,
        // Inline overrides win over styles.content.paddingTop /
        // paddingBottom. The original `content.paddingTop` had a
        // hard-coded `+20` fudge for the status bar; useSafeAreaInsets
        // replaces that with the accurate per-device value (Dynamic
        // Island vs notch vs no notch all differ).
        {
          paddingTop: insets.top + spacing.lg,
          paddingBottom: insets.bottom + spacing.lg,
        },
      ]}
      testID="settings-scroll"
    >
      <Text style={styles.title}>FRIDAY ↗ SETTINGS</Text>

      <Section title="STATUS">
        <Row label="HOST" value={pairing?.host ?? '—'} testID="settings-host" />
        <Row
          label="CONNECTION"
          value={connLabel(conn, latencyMs)}
          testID="settings-conn"
        />
      </Section>

      <Section title="ACTIONS">
        <Button
          label="Test connection"
          onPress={onTestConnection}
          testID="settings-test-button"
        />
        <Button
          label="Re-pair"
          onPress={onRepair}
          destructive
          testID="settings-repair-button"
        />
      </Section>

      <Pressable
        onPress={() => router.back()}
        style={styles.backButton}
        testID="settings-back"
      >
        <Text style={styles.backText}>← BACK TO ORB</Text>
      </Pressable>
    </ScrollView>
  )
}

// ---- Helpers -------------------------------------------------------------

function connLabel(state: ConnState, latencyMs: number | null): string {
  switch (state) {
    case 'reachable':
      return latencyMs != null ? `GREEN · ${latencyMs}ms` : 'GREEN'
    case 'unreachable':
      return 'RED · UNREACHABLE'
    case 'testing':
      return 'PINGING…'
    case 'unknown':
    default:
      return '—'
  }
}

// ---- Sub-components ------------------------------------------------------
// Kept inline rather than separate files because they're only ever used here
// and add ~30 lines combined. Splitting into mobile/components/settings/*
// would add three new files for negative benefit.

function Section({
  title,
  children,
}: {
  title: string
  children: React.ReactNode
}): React.ReactElement {
  return (
    <View style={styles.section}>
      <Text style={styles.sectionTitle}>{title}</Text>
      {children}
    </View>
  )
}

function Row({
  label,
  value,
  testID,
}: {
  label: string
  value: string
  testID?: string
}): React.ReactElement {
  return (
    <View style={styles.row}>
      <Text style={styles.rowLabel}>{label}</Text>
      <Text style={styles.rowValue} testID={testID}>
        {value}
      </Text>
    </View>
  )
}

function Button({
  label,
  onPress,
  destructive,
  testID,
}: {
  label: string
  onPress: () => void | Promise<void>
  destructive?: boolean
  testID?: string
}): React.ReactElement {
  return (
    <Pressable
      onPress={onPress}
      style={[styles.button, destructive && styles.destructiveButton]}
      testID={testID}
    >
      <Text
        style={[styles.buttonText, destructive && styles.destructiveText]}
      >
        {label}
      </Text>
    </Pressable>
  )
}

// ---- Styles --------------------------------------------------------------
// All values pulled from hud-tokens so the screen stays visually in sync
// with the orb. Border opacity for row separators matches the OrbView
// corner labels' alpha-0.1 strokes.

const styles = StyleSheet.create({
  container: {
    flex: 1,
    backgroundColor: colors.bg,
  },
  content: {
    padding: spacing.xl,
    // Push past the status bar -- the screen has no header bar of its own.
    paddingTop: spacing.xxl + 20,
  },
  title: {
    fontFamily: fontFamilies.mono,
    fontSize: 11,
    color: colors.cyan,
    letterSpacing: 2,
    marginBottom: spacing.xl,
  },
  section: {
    marginBottom: spacing.xl,
  },
  sectionTitle: {
    fontFamily: fontFamilies.mono,
    fontSize: 10,
    color: colors.cyan,
    opacity: 0.6,
    letterSpacing: 1.5,
    marginBottom: spacing.md,
  },
  row: {
    flexDirection: 'row',
    justifyContent: 'space-between',
    paddingVertical: spacing.sm,
    borderBottomWidth: 1,
    borderBottomColor: 'rgba(0,255,204,0.1)',
  },
  rowLabel: {
    fontFamily: fontFamilies.mono,
    fontSize: 11,
    color: colors.cyan,
    opacity: 0.7,
  },
  rowValue: {
    fontFamily: fontFamilies.mono,
    fontSize: 11,
    color: colors.cyan,
  },
  button: {
    padding: spacing.md,
    borderWidth: 1,
    borderColor: colors.cyan,
    borderRadius: 4,
    marginVertical: spacing.xs,
  },
  destructiveButton: {
    borderColor: colors.amber,
  },
  buttonText: {
    fontFamily: fontFamilies.mono,
    fontSize: 12,
    color: colors.cyan,
    textAlign: 'center',
    letterSpacing: 1.5,
  },
  destructiveText: {
    color: colors.amber,
  },
  backButton: {
    padding: spacing.lg,
    marginTop: spacing.xl,
    alignItems: 'center',
  },
  backText: {
    fontFamily: fontFamilies.mono,
    fontSize: 11,
    color: colors.cyan,
    opacity: 0.6,
    letterSpacing: 1.5,
  },
})
