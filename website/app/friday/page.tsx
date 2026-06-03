import { QRCodeSVG } from 'qrcode.react'

// -----------------------------------------------------------------------------
// Friday — mobile companion setup page
//
// v0.3.0: full setup walkthrough rather than the QR-and-go minimal version.
// Friday lives inside Expo Go (zero App Store friction, no Apple Developer
// Program fee). Android users can optionally sideload a signed APK from the
// GitHub release.
// -----------------------------------------------------------------------------

const EAS_URL = 'https://u.expo.dev/REPLACE_WITH_EAS_PROJECT_ID?channel=production'
const EXPO_GO_IOS = 'https://apps.apple.com/app/expo-go/id982107779'
const EXPO_GO_ANDROID = 'https://play.google.com/store/apps/details?id=host.exp.exponent'
const GITHUB_RELEASES = 'https://github.com/namanchopra/J.A.R.V.I.S/releases/latest'

// Render per-request so the EAS URL stays current with however the project
// is provisioned. Same convention as /page.tsx.
export const dynamic = 'force-dynamic'

interface Step {
  num: string
  title: string
  body: React.ReactNode
}

const SETUP_STEPS: Step[] = [
  {
    num: '01',
    title: 'Make sure Jarvis is running on your Mac.',
    body: (
      <>
        Friday is a remote — it relays your voice to the Mac, which does the
        thinking. If Jarvis isn&apos;t running, Friday has nothing to talk to.
        Both devices need to be on the same Wi-Fi network (or VPN).
      </>
    ),
  },
  {
    num: '02',
    title: 'Install Expo Go on your phone.',
    body: (
      <>
        Expo Go is a free, official Expo Inc. app from the iOS App Store and
        Google Play. It&apos;s the runtime that Friday loads inside —
        there&apos;s no Friday-specific app to install and nothing on Apple or
        Google&apos;s review queues. Two install links below.
      </>
    ),
  },
  {
    num: '03',
    title: 'Scan the project QR with Expo Go.',
    body: (
      <>
        Open Expo Go, tap <code className="font-mono text-jarvis-cyan/80">Scan QR code</code>,
        and point your camera at the QR below. Friday opens inside Expo Go.
        First load takes ~10 seconds; subsequent opens are instant from the
        cache.
      </>
    ),
  },
  {
    num: '04',
    title: 'Pair Friday with your Mac.',
    body: (
      <>
        On the Mac, open <code className="font-mono text-jarvis-cyan/80">Jarvis → Settings → Connections → &ldquo;Connect Friday phone&rdquo;</code>.
        A second QR appears containing your Mac&apos;s IP + a one-time
        bearer token. On Friday, tap <code className="font-mono text-jarvis-cyan/80">Pair</code> and
        scan that QR. Friday persists the credentials to SecureStore and
        connects.
      </>
    ),
  },
  {
    num: '05',
    title: 'Press the orb. Talk.',
    body: (
      <>
        Press-and-hold the orb on Friday&apos;s home screen. Your voice
        streams to the Mac over WebSocket. Release to send. Jarvis responds
        through Friday&apos;s speaker. State labels (LLM / STT / TTS /
        SESSIONS) and the next calendar event sit just above the orb.
      </>
    ),
  },
]

export default function FridayPage() {
  return (
    <main className="min-h-screen flex flex-col items-center px-6 py-12 bg-jarvis-bg">
      {/* Header */}
      <h1 className="font-mono text-3xl font-bold text-jarvis-cyan tracking-[0.25em] mb-2">FRIDAY</h1>
      <p className="font-mono text-sm text-jarvis-cyan/60 tracking-widest mb-2">JARVIS MOBILE COMPANION</p>
      <p className="font-mono text-[10px] text-jarvis-cyan/40 tracking-widest mb-10">v0.3.0 // RELEASE</p>

      {/* QR + Expo Go buttons */}
      <div className="bg-white p-6 rounded">
        <QRCodeSVG value={EAS_URL} size={240} fgColor="#0a0a0a" bgColor="#ffffff" />
      </div>

      <p className="mt-5 font-mono text-xs text-jarvis-cyan/65 max-w-md text-center">
        Scan with Expo Go on your phone. Friday loads inside Expo Go — no App Store install needed.
      </p>

      <div className="mt-8 flex gap-4 flex-wrap justify-center">
        <a href={EXPO_GO_IOS} className="jarvis-btn-primary">Install Expo Go (iOS)</a>
        <a href={EXPO_GO_ANDROID} className="jarvis-btn-primary">Install Expo Go (Android)</a>
      </div>

      {/* Step-by-step setup */}
      <section className="mt-20 w-full max-w-2xl">
        <h2 className="font-mono text-sm text-jarvis-cyan tracking-[0.25em] mb-6 text-center">
          SETUP — 5 STEPS, ~3 MINUTES
        </h2>
        <ol className="flex flex-col gap-4">
          {SETUP_STEPS.map((step) => (
            <li key={step.num} className="jarvis-card">
              <span className="corner-bracket-tl" />
              <span className="corner-bracket-tr" />
              <span className="corner-bracket-bl" />
              <span className="corner-bracket-br" />
              <div className="flex items-start gap-4">
                <span className="font-mono text-2xl text-jarvis-cyan-bright glow-text leading-none">
                  {step.num}
                </span>
                <div>
                  <h3 className="font-sans font-semibold text-cyan-50 mb-2">{step.title}</h3>
                  <p className="font-mono text-sm text-jarvis-cyan/60 leading-relaxed">{step.body}</p>
                </div>
              </div>
            </li>
          ))}
        </ol>
      </section>

      {/* Android sideload option */}
      <section className="mt-16 w-full max-w-2xl">
        <h2 className="font-mono text-sm text-jarvis-cyan tracking-[0.25em] mb-6 text-center">
          ANDROID: STANDALONE APP (OPTIONAL)
        </h2>
        <div className="jarvis-card">
          <span className="corner-bracket-tl" />
          <span className="corner-bracket-tr" />
          <span className="corner-bracket-bl" />
          <span className="corner-bracket-br" />
          <p className="font-mono text-sm text-jarvis-cyan/60 leading-relaxed">
            Prefer a real home-screen icon instead of opening Friday from
            inside Expo Go? Download the signed{' '}
            <a
              href={GITHUB_RELEASES}
              className="text-jarvis-cyan underline underline-offset-2 hover:text-jarvis-cyan-bright"
            >
              Friday-v0.3.0.apk
            </a>{' '}
            from the latest GitHub release. Enable{' '}
            <code className="font-mono text-jarvis-cyan/80">Install unknown apps</code>{' '}
            once for your browser, tap the APK, and Friday installs as a
            standalone app with its own icon and splash screen.
          </p>
          <p className="font-mono text-[10px] text-jarvis-cyan/40 leading-relaxed mt-3">
            Pairing flow (step 04) is identical for both Expo Go and APK installs.
          </p>
        </div>
      </section>

      {/* iOS standalone note */}
      <section className="mt-12 w-full max-w-2xl">
        <h2 className="font-mono text-sm text-jarvis-cyan tracking-[0.25em] mb-6 text-center">
          IOS: STANDALONE APP
        </h2>
        <div className="jarvis-card">
          <span className="corner-bracket-tl" />
          <span className="corner-bracket-tr" />
          <span className="corner-bracket-bl" />
          <span className="corner-bracket-br" />
          <p className="font-mono text-sm text-jarvis-cyan/60 leading-relaxed">
            Apple doesn&apos;t offer a free standalone-app path. Friday runs
            inside Expo Go on iOS at no cost. A TestFlight build will land in
            a future release once iOS demand warrants the $99/yr Apple
            Developer Program fee.
          </p>
        </div>
      </section>

      {/* Troubleshooting */}
      <section className="mt-12 w-full max-w-2xl">
        <h2 className="font-mono text-sm text-jarvis-cyan tracking-[0.25em] mb-6 text-center">
          TROUBLESHOOTING
        </h2>
        <div className="jarvis-card">
          <span className="corner-bracket-tl" />
          <span className="corner-bracket-tr" />
          <span className="corner-bracket-bl" />
          <span className="corner-bracket-br" />
          <dl className="flex flex-col gap-3 font-mono text-sm">
            <div>
              <dt className="text-cyan-50 font-semibold">Friday loads but says &ldquo;not connected&rdquo;.</dt>
              <dd className="text-jarvis-cyan/60 mt-1">
                The pairing token expired or your Mac changed IP. Open
                Jarvis → Settings → Connections, regenerate the QR, and
                re-scan from Friday&apos;s Pair screen.
              </dd>
            </div>
            <div>
              <dt className="text-cyan-50 font-semibold">QR scan fails inside Expo Go.</dt>
              <dd className="text-jarvis-cyan/60 mt-1">
                Make sure Expo Go is up to date. The Friday bundle requires
                Expo SDK 54+.
              </dd>
            </div>
            <div>
              <dt className="text-cyan-50 font-semibold">No audio plays through Friday.</dt>
              <dd className="text-jarvis-cyan/60 mt-1">
                Check your phone&apos;s ringer/silent switch (iOS). Pull
                down the Friday screen to reveal the diagnostic strip — STT
                and TTS labels should light up green when audio is flowing.
              </dd>
            </div>
            <div>
              <dt className="text-cyan-50 font-semibold">Friday&apos;s push-to-talk button doesn&apos;t respond.</dt>
              <dd className="text-jarvis-cyan/60 mt-1">
                Grant microphone permission to Expo Go (Settings → Expo Go →
                Microphone). On first PTT press, the permission prompt
                appears once.
              </dd>
            </div>
          </dl>
        </div>
      </section>

      {/* Footer */}
      <footer className="mt-20 mb-6 font-mono text-[10px] text-jarvis-cyan/40 max-w-md text-center">
        Friday relays your voice to the Mac; the Mac does the thinking. Audio
        is encrypted end-to-end over WebSocket with a bearer token. Nothing
        is recorded server-side — the Mac is the server.
      </footer>
    </main>
  )
}
