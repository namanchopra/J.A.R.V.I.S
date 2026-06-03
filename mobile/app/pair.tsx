// ---------------------------------------------------------------------------
// Pairing screen -- TASK-020 first-launch QR scan.
// ---------------------------------------------------------------------------
// First launch (empty SecureStore) routes here from the root layout. The user
// holds the phone up to the Mac's "Connect Friday phone" panel (TASK-025); the
// camera reads the embedded `jarvis://pair?...` URL, we persist the fields,
// and redirect to `/` so the orb takes over.
//
// Aesthetic notes:
//   * Dark bg (#000c0a) + cyan #00ffcc -- matches the Mac HUD source-of-truth.
//   * Four corner brackets frame the scanner box (no fill, just stroke).
//   * Monospace footer "POINT CAMERA AT QR FROM JARVIS SETTINGS" sits below
//     the brackets in dim cyan -- same family/letter-spacing as OrbView
//     corner labels for visual continuity.
//   * On invalid scan we show an inline cyan/amber error toast that
//     auto-dismisses after 2.5s; storage is NOT mutated.
// ---------------------------------------------------------------------------

import { CameraView, useCameraPermissions } from 'expo-camera'
import { useRouter } from 'expo-router'
import { useCallback, useRef, useState } from 'react'
import {
  Pressable,
  StyleSheet,
  Text,
  View,
} from 'react-native'
import { useSafeAreaInsets } from 'react-native-safe-area-context'

import { colors, fontFamilies, spacing } from '../lib/hud-tokens'
import { parsePairingQR, savePairing } from '../lib/pairing'

// ---- Constants -----------------------------------------------------------
// Corner bracket size + the visible viewfinder box are derived from spacing
// so they scale with the design tokens rather than being magic numbers.

const VIEWFINDER_SIZE = 260
const BRACKET_SIZE = 32
const BRACKET_THICKNESS = 2
/** Hide the toast after this many milliseconds. Matches the iOS toast feel. */
const TOAST_TIMEOUT_MS = 2500
/** Cooldown between accepted scans -- prevents the camera firing the same QR
 *  five times in quick succession (BarcodeScanningResult emits per frame). */
const SCAN_COOLDOWN_MS = 1500

// ---- Screen --------------------------------------------------------------

export default function PairScreen(): React.ReactElement {
  const router = useRouter()
  const [permission, requestPermission] = useCameraPermissions()
  // Safe-area insets: pair screen renders full-bleed camera + chrome over
  // it (header, viewfinder, footer, toast). Without insets the header
  // sits under the iPhone notch / Dynamic Island. We apply them via
  // padding on the root container so the absolutely-positioned chrome
  // children inherit the safe area through their own offsets.
  const insets = useSafeAreaInsets()

  // Toast state: `null` = no toast. Stored as a string so we can change the
  // message on subsequent failures (e.g. "Invalid QR" vs "Save failed").
  const [toast, setToast] = useState<string | null>(null)
  // Track when we last accepted a scan -- raw ref so the camera callback
  // doesn't trigger re-renders just to debounce.
  const lastScanAtRef = useRef<number>(0)
  // Once we've accepted a valid QR + saved, lock the camera so a second QR in
  // frame can't double-fire while we're navigating.
  const lockedRef = useRef<boolean>(false)

  // ---- Scan handler --------------------------------------------------
  // Called once per detected QR frame. We:
  //   1. Throttle to one accepted scan per SCAN_COOLDOWN_MS.
  //   2. Parse the payload; bail with toast on null.
  //   3. Persist; bail with toast on SecureStore failure (e.g. Keychain locked).
  //   4. Lock the camera and route to "/" (orb).
  //
  // The hook order matters: this must be declared above the early-return
  // branches below so React's rules-of-hooks lint stays happy.
  const handleBarcodeScanned = useCallback(
    ({ data }: { data: string }) => {
      if (lockedRef.current) return
      const now = Date.now()
      if (now - lastScanAtRef.current < SCAN_COOLDOWN_MS) return
      lastScanAtRef.current = now

      const payload = parsePairingQR(data)
      if (!payload) {
        showToast(setToast, 'INVALID QR :: EXPECTED jarvis://pair?...')
        return
      }

      // Lock immediately so a subsequent in-frame scan doesn't race the
      // async save. We unlock on save failure so the user can retry.
      lockedRef.current = true
      savePairing(payload)
        .then(() => {
          router.replace('/')
        })
        .catch(() => {
          lockedRef.current = false
          showToast(setToast, 'STORAGE FAILED :: TRY AGAIN')
        })
    },
    [router],
  )

  // ---- Permission rendering branch -----------------------------------
  // `permission` is null on the first render before the hook resolves; we
  // render the "REQUESTING" footer and let the hook re-render. Once it
  // resolves to denied/granted we route accordingly.
  if (!permission) {
    return (
      <View
        style={[
          styles.container,
          { paddingTop: insets.top, paddingBottom: insets.bottom },
        ]}
        testID="pair-loading"
      >
        <Text style={styles.footerDim}>REQUESTING CAMERA…</Text>
      </View>
    )
  }

  if (!permission.granted) {
    return (
      <View
        style={[
          styles.container,
          { paddingTop: insets.top, paddingBottom: insets.bottom },
        ]}
        testID="pair-permission-gate"
      >
        <Text style={styles.title}>FRIDAY :: PAIRING</Text>
        <Text style={styles.bodyDim}>
          Camera access is required to scan the pairing QR from your Mac.
        </Text>
        <Pressable
          onPress={requestPermission}
          style={({ pressed }) => [
            styles.grantButton,
            pressed && styles.grantButtonPressed,
          ]}
          testID="pair-grant-button"
        >
          <Text style={styles.grantButtonText}>GRANT CAMERA ACCESS</Text>
        </Pressable>
      </View>
    )
  }

  // ---- Render --------------------------------------------------------
  // The camera scan branch is full-bleed (CameraView fills the screen),
  // so we can NOT apply paddingTop/paddingBottom on the root -- that
  // would shrink the camera viewport and break the QR scan. Instead we
  // shift the chrome (header, footer, toast) by the safe-area inset
  // values via inline offsets so they clear the notch / home indicator
  // without affecting the camera feed.

  return (
    <View style={styles.container} testID="pair-screen">
      <CameraView
        style={StyleSheet.absoluteFill}
        facing="back"
        barcodeScannerSettings={{ barcodeTypes: ['qr'] }}
        onBarcodeScanned={handleBarcodeScanned}
        testID="pair-camera"
      />

      {/* Scrim -- dims everything outside the viewfinder so the QR target is
          the only fully-bright region of the screen. Implemented as four
          absolutely-positioned panels around the centered viewfinder. */}
      <Scrim />

      {/* Viewfinder + cyan corner brackets */}
      <View style={styles.viewfinderWrap} pointerEvents="none">
        <View style={styles.viewfinder}>
          <View style={[styles.bracket, styles.bracketTL]} />
          <View style={[styles.bracket, styles.bracketTR]} />
          <View style={[styles.bracket, styles.bracketBL]} />
          <View style={[styles.bracket, styles.bracketBR]} />
        </View>
      </View>

      {/* Top header label -- matches OrbView corner label style. The
          base top offset (spacing.xxl + spacing.lg = 48) is added to
          insets.top so the header clears the iPhone notch. */}
      <View
        style={[
          styles.header,
          { top: insets.top + spacing.lg },
        ]}
        pointerEvents="none"
      >
        <Text style={styles.headerTitle}>FRIDAY :: PAIRING</Text>
      </View>

      {/* Footer instruction in dim cyan monospace, lifted above the
          home indicator. */}
      <View
        style={[
          styles.footer,
          { bottom: insets.bottom + spacing.xl },
        ]}
        pointerEvents="none"
      >
        <Text style={styles.footerDim}>
          POINT CAMERA AT QR FROM JARVIS SETTINGS
        </Text>
      </View>

      {/* Error toast (inline, not a Modal -- avoids stealing focus).
          Bottom offset is lifted further so the toast sits ABOVE the
          footer regardless of inset depth. */}
      {toast ? (
        <View
          style={[
            styles.toast,
            { bottom: insets.bottom + spacing.xxl * 2 },
          ]}
          testID="pair-toast"
        >
          <Text style={styles.toastText}>{toast}</Text>
        </View>
      ) : null}
    </View>
  )
}

// ---- Toast helper ---------------------------------------------------------
// Shared between the parse-failure and save-failure paths. Uses a single
// setTimeout to clear the message; if a second toast fires while one is up,
// the message updates and the timeout is reset by React re-render -- which
// for this UX is acceptable (last error wins).

function showToast(
  setToast: (msg: string | null) => void,
  message: string,
): void {
  setToast(message)
  setTimeout(() => setToast(null), TOAST_TIMEOUT_MS)
}

// ---- Scrim sub-component -------------------------------------------------
// Four panels around the centered viewfinder + a 1-pixel hairline border on
// the inner edge for a subtle cyan glow. Implemented inline rather than via
// LinearGradient to keep the Friday bundle Skia-free (TASK-026: Expo Go).

function Scrim(): React.ReactElement {
  return (
    <View style={StyleSheet.absoluteFill} pointerEvents="none">
      <View style={[styles.scrim, styles.scrimTop]} />
      <View style={[styles.scrim, styles.scrimBottom]} />
      <View style={[styles.scrim, styles.scrimLeft]} />
      <View style={[styles.scrim, styles.scrimRight]} />
    </View>
  )
}

// ---- Styles ---------------------------------------------------------------

const styles = StyleSheet.create({
  container: {
    flex: 1,
    backgroundColor: colors.bg,
    alignItems: 'center',
    justifyContent: 'center',
    padding: spacing.xl,
  },

  // ---- Permission gate ---------------------------------------------------
  title: {
    color: colors.cyan,
    fontFamily: fontFamilies.mono,
    fontSize: 14,
    letterSpacing: 2,
    marginBottom: spacing.xl,
  },
  bodyDim: {
    color: colors.textDim,
    fontFamily: fontFamilies.mono,
    fontSize: 12,
    letterSpacing: 1,
    textAlign: 'center',
    marginBottom: spacing.xxl,
    paddingHorizontal: spacing.lg,
  },
  grantButton: {
    borderColor: colors.cyan,
    borderWidth: 1,
    paddingHorizontal: spacing.xl,
    paddingVertical: spacing.md,
    borderRadius: 2,
    backgroundColor: colors.cyanDark,
  },
  grantButtonPressed: {
    backgroundColor: 'rgba(0, 255, 204, 0.3)',
  },
  grantButtonText: {
    color: colors.cyan,
    fontFamily: fontFamilies.mono,
    fontSize: 11,
    letterSpacing: 1.5,
  },

  // ---- Viewfinder + brackets ---------------------------------------------
  viewfinderWrap: {
    ...StyleSheet.absoluteFillObject,
    alignItems: 'center',
    justifyContent: 'center',
  },
  viewfinder: {
    width: VIEWFINDER_SIZE,
    height: VIEWFINDER_SIZE,
    position: 'relative',
  },
  bracket: {
    position: 'absolute',
    width: BRACKET_SIZE,
    height: BRACKET_SIZE,
    borderColor: colors.cyan,
  },
  bracketTL: {
    top: 0,
    left: 0,
    borderTopWidth: BRACKET_THICKNESS,
    borderLeftWidth: BRACKET_THICKNESS,
  },
  bracketTR: {
    top: 0,
    right: 0,
    borderTopWidth: BRACKET_THICKNESS,
    borderRightWidth: BRACKET_THICKNESS,
  },
  bracketBL: {
    bottom: 0,
    left: 0,
    borderBottomWidth: BRACKET_THICKNESS,
    borderLeftWidth: BRACKET_THICKNESS,
  },
  bracketBR: {
    bottom: 0,
    right: 0,
    borderBottomWidth: BRACKET_THICKNESS,
    borderRightWidth: BRACKET_THICKNESS,
  },

  // ---- Scrim (4 panels around viewfinder) -------------------------------
  // Each panel covers ~half the screen with a transform that shifts it so a
  // VIEWFINDER_SIZE square in the center stays uncovered. The math assumes
  // the viewfinder is centered (which it is via flex alignItems/justify).
  scrim: {
    position: 'absolute',
    backgroundColor: 'rgba(0, 12, 10, 0.75)',
  },
  scrimTop: {
    top: 0,
    left: 0,
    right: 0,
    height: '50%',
    transform: [{ translateY: -VIEWFINDER_SIZE / 2 }],
  },
  scrimBottom: {
    bottom: 0,
    left: 0,
    right: 0,
    height: '50%',
    transform: [{ translateY: VIEWFINDER_SIZE / 2 }],
  },
  scrimLeft: {
    top: '50%',
    left: 0,
    width: '50%',
    height: VIEWFINDER_SIZE,
    transform: [
      { translateX: -VIEWFINDER_SIZE / 2 },
      { translateY: -VIEWFINDER_SIZE / 2 },
    ],
  },
  scrimRight: {
    top: '50%',
    right: 0,
    width: '50%',
    height: VIEWFINDER_SIZE,
    transform: [
      { translateX: VIEWFINDER_SIZE / 2 },
      { translateY: -VIEWFINDER_SIZE / 2 },
    ],
  },

  // ---- Header / footer ---------------------------------------------------
  header: {
    position: 'absolute',
    top: spacing.xxl + spacing.lg,
    left: 0,
    right: 0,
    alignItems: 'center',
  },
  headerTitle: {
    color: colors.cyan,
    fontFamily: fontFamilies.mono,
    fontSize: 12,
    letterSpacing: 2,
  },
  footer: {
    position: 'absolute',
    bottom: spacing.xxl + spacing.xl,
    left: 0,
    right: 0,
    alignItems: 'center',
    paddingHorizontal: spacing.xl,
  },
  footerDim: {
    color: colors.textDim,
    fontFamily: fontFamilies.mono,
    fontSize: 10,
    letterSpacing: 1.5,
    textAlign: 'center',
  },

  // ---- Toast --------------------------------------------------------------
  toast: {
    position: 'absolute',
    bottom: spacing.xxl * 3,
    left: spacing.xl,
    right: spacing.xl,
    backgroundColor: colors.bgPanel,
    borderColor: colors.cyan,
    borderWidth: 1,
    paddingHorizontal: spacing.lg,
    paddingVertical: spacing.md,
    borderRadius: 2,
    alignItems: 'center',
  },
  toastText: {
    color: colors.cyan,
    fontFamily: fontFamilies.mono,
    fontSize: 11,
    letterSpacing: 1.5,
    textAlign: 'center',
  },
})
