import { QRCodeSVG } from 'qrcode.react'

const EAS_URL = 'https://u.expo.dev/REPLACE_WITH_EAS_PROJECT_ID?channel=production'
const EXPO_GO_IOS = 'https://apps.apple.com/app/expo-go/id982107779'
const EXPO_GO_ANDROID = 'https://play.google.com/store/apps/details?id=host.exp.exponent'

// Match the existing /page.tsx convention so the page is rendered per-request
// rather than baked into the build (the EAS URL will be patched in once the
// real project ID is wired up after manual `eas init`).
export const dynamic = 'force-dynamic'

export default function FridayPage() {
  return (
    <main className="min-h-screen flex flex-col items-center justify-center px-6 py-16 bg-jarvis-bg">
      <h1 className="font-mono text-3xl font-bold text-jarvis-cyan tracking-[0.25em] mb-2">FRIDAY</h1>
      <p className="font-mono text-sm text-jarvis-cyan/60 tracking-widest mb-12">JARVIS MOBILE COMPANION</p>

      <div className="bg-white p-6 rounded">
        <QRCodeSVG value={EAS_URL} size={256} fgColor="#0a0a0a" bgColor="#ffffff" />
      </div>

      <p className="mt-6 font-mono text-xs text-jarvis-cyan/65 max-w-md text-center">
        Scan with Expo Go on your phone. Friday loads inside Expo Go — no App Store install needed.
      </p>

      <div className="mt-10 flex gap-4">
        <a href={EXPO_GO_IOS} className="jarvis-btn-primary">Install Expo Go (iOS)</a>
        <a href={EXPO_GO_ANDROID} className="jarvis-btn-primary">Install Expo Go (Android)</a>
      </div>

      <p className="mt-12 font-mono text-[10px] text-jarvis-cyan/40 max-w-md text-center">
        After opening Friday in Expo Go, go to Jarvis Settings → Connections → "Connect Friday phone" to scan the pairing QR. Friday relays your voice to the Mac; the Mac does the thinking.
      </p>
    </main>
  )
}
