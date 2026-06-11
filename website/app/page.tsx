import StarButton from '@/components/StarButton'
import DemoTranscript from '@/components/DemoTranscript'
import OrbClient from '@/components/OrbClient'
import { QRCodeSVG } from 'qrcode.react'
import { headers } from 'next/headers'

const EAS_URL = 'https://u.expo.dev/4ec82a4b-3506-48da-ba60-114dae1ce9ba?channel=production'
const EXPO_GO_IOS = 'https://apps.apple.com/app/expo-go/id982107779'
const EXPO_GO_ANDROID = 'https://play.google.com/store/apps/details?id=host.exp.exponent'

const FRIDAY_STEPS = [
  {
    num: '01',
    title: 'Make sure Jarvis is running on your Mac.',
    body: 'Friday relays your voice to the Mac; the Mac does the thinking. Both devices need to be on the same Wi-Fi network.',
  },
  {
    num: '02',
    title: 'Install Expo Go on your phone.',
    body: 'Free, official Expo Inc. app from the App Store and Google Play. It\'s the runtime that Friday loads inside — no Friday-specific install needed.',
  },
  {
    num: '03',
    title: 'Scan the project QR with Expo Go.',
    body: 'Open Expo Go, tap "Scan QR code", point at the QR below. Friday opens inside Expo Go.',
  },
  {
    num: '04',
    title: 'Pair Friday with your Mac.',
    body: 'On the Mac: Jarvis → Settings → Connections → "Connect Friday phone". Scan THAT QR with Friday. Persists to SecureStore.',
  },
  {
    num: '05',
    title: 'Press the orb. Talk.',
    body: 'Hold the orb on Friday\'s home screen to record. Release to send. Jarvis responds through Friday\'s speaker.',
  },
]

// Render this route on every request. Without this the Next.js cache
// pinned the version banner to whatever was current at the last build
// or 1-hour revalidation window -- v0.2.12 shipping in the morning
// would not show up until ~5pm. Setting force-dynamic + revalidate: 60
// on the GitHub fetch below gives us "new tag visible within 60s of
// being published" without spamming GitHub's API.
export const dynamic = 'force-dynamic'

const REPO_URL = 'https://github.com/namanchopra/J.A.R.V.I.S'
const RELEASES_URL = `${REPO_URL}/releases/latest`

// Hardcoded fallback — kept in sync with the latest released DMG so the
// site never serves a 404 even if the GitHub API call below fails at
// build time (e.g. rate-limited or offline). Bump this whenever a new
// tag ships; `fetchLatestVersion` will override it on every cache miss.
const FALLBACK_VERSION = '0.3.1'

// v0.3.1 is the current release — hotfix on top of v0.3.0 (overlay +
// Google Calendar + meeting mode + recall tools + Friday dashboard
// redesign) that installs portaudio before pyaudio's source build so
// first-launch setup doesn't fail with `portaudio.h: file not found`
// on machines without portaudio already present.

/**
 * Compare two semver strings (no pre-release tags). Returns 1 if a > b,
 * -1 if a < b, 0 if equal. Kept simple — the codebase only ships
 * MAJOR.MINOR.PATCH tags, no -beta / -rc suffixes.
 */
function semverCompare(a: string, b: string): number {
  const pa = a.split('.').map((n) => parseInt(n, 10) || 0)
  const pb = b.split('.').map((n) => parseInt(n, 10) || 0)
  for (let i = 0; i < 3; i++) {
    if ((pa[i] ?? 0) > (pb[i] ?? 0)) return 1
    if ((pa[i] ?? 0) < (pb[i] ?? 0)) return -1
  }
  return 0
}

/**
 * Coarse OS detection from a User-Agent string. Returns:
 *   - 'macos'   for any Mac UA (we ship only Apple-Silicon DMG, but a
 *               UA-only sniff can't tell Intel from arm64 — so we show
 *               the DMG to all macOS visitors and let the install screen
 *               surface the arch warning)
 *   - 'windows' for any Win32 UA
 *   - 'unknown' for everything else (Linux, *BSD, crawlers, opaque UAs,
 *               or an empty header) — caller surfaces BOTH downloads in
 *               that case rather than guessing wrong
 *
 * Deliberately conservative: a missing or unrecognisable UA returns
 * 'unknown' so we never hide a download a real user wants. The Friday
 * page on iOS/Android also lands on 'unknown' and gets both, which is
 * fine — mobile users don't install the desktop binary anyway.
 */
type DetectedOS = 'macos' | 'windows' | 'unknown'

function detectOS(userAgent: string | null | undefined): DetectedOS {
  if (!userAgent) return 'unknown'
  const ua = userAgent.toLowerCase()
  // Windows: Win32, Win64, Windows NT, WOW64. ARM64 Windows still
  // identifies as "Windows NT" — we don't need to branch on arch here
  // because the installer .exe is multi-arch (separate amd64 / arm64
  // installers are shipped under the same Setup-<version>.exe naming
  // on Releases; the UA-sniff just needs to pick "Windows" vs "macOS").
  if (ua.includes('windows nt') || ua.includes('win32') || ua.includes('win64') || ua.includes('wow64')) {
    return 'windows'
  }
  // macOS: "Macintosh" and "Mac OS X" are the canonical tokens. Mobile
  // Safari on iPad in desktop-mode also matches "Macintosh" — that's
  // fine; iPad users tapping "Download" land on the GitHub releases
  // page and pick from the asset list.
  if (ua.includes('macintosh') || ua.includes('mac os x') || ua.includes('macos')) {
    return 'macos'
  }
  return 'unknown'
}

/**
 * Latest visible version. Prefers FALLBACK_VERSION when GitHub's
 * /releases/latest endpoint returns an OLDER tag — this happens during
 * the window between a tag push and `gh release create` finishing
 * inside release.yml (~30-40 min for the notarize + DMG build). Without
 * this guard the homepage shows the previous version until the workflow
 * completes; with it, bumping FALLBACK_VERSION before tagging makes the
 * site show the new version immediately.
 *
 * Once release.yml publishes and the GitHub API returns >= FALLBACK,
 * the live GitHub answer wins (and continues to win on future patch
 * releases until someone bumps FALLBACK again).
 */
async function fetchLatestVersion(): Promise<string> {
  try {
    const res = await fetch(
      'https://api.github.com/repos/namanchopra/J.A.R.V.I.S/releases/latest',
      {
        headers: { Accept: 'application/vnd.github+json' },
        next: { revalidate: 60 },
      },
    )
    if (!res.ok) return FALLBACK_VERSION
    const data = (await res.json()) as { tag_name?: string }
    const apiVersion = data.tag_name?.replace(/^v/, '')
    if (!apiVersion) return FALLBACK_VERSION
    // Whichever is newer wins. During the release.yml build window the
    // FALLBACK is ahead; once published the API catches up and stays so.
    return semverCompare(apiVersion, FALLBACK_VERSION) >= 0 ? apiVersion : FALLBACK_VERSION
  } catch {
    return FALLBACK_VERSION
  }
}

export default async function Page() {
  const version = await fetchLatestVersion()
  const dmgUrl = `${REPO_URL}/releases/download/v${version}/Jarvis-${version}.dmg`
  // Inno Setup installer naming from TASK-054: `Jarvis-Setup-<version>.exe`.
  // Single installer covers both x64 + arm64 (the Inno Setup script picks
  // the matching arch payload at install time). UA-sniff only needs to
  // distinguish macOS vs Windows, not the Windows architecture.
  const exeUrl = `${REPO_URL}/releases/download/v${version}/Jarvis-Setup-${version}.exe`
  // headers() is server-only and async in Next 15. Reading it here keeps
  // the page a Server Component (no client JS for UA detection) and means
  // bots / crawlers that don't send a UA gracefully fall through to
  // 'unknown' → both downloads visible.
  const hdrs = await headers()
  const detectedOS = detectOS(hdrs.get('user-agent'))
  return (
    <main className="relative">
      {/* ===================== NAV ===================== */}
      <nav className="fixed top-0 left-0 right-0 z-40 backdrop-blur-md bg-jarvis-bg/60 border-b border-jarvis-cyan-dark/40">
        <div className="mx-auto max-w-6xl px-6 h-14 flex items-center justify-between">
          <div className="flex items-baseline gap-3">
            <div className="h-1.5 w-1.5 rounded-full bg-jarvis-cyan-bright shadow-[0_0_8px_rgba(34,211,238,0.8)] animate-pulse-soft" />
            <span className="font-mono font-bold tracking-[0.25em] text-jarvis-cyan glow-text">
              J.A.R.V.I.S
            </span>
            <span className="label-mono">v{version}</span>
          </div>
          <div className="flex items-center gap-3">
            <a
              href={`${REPO_URL}#install`}
              className="hidden sm:inline label-mono hover:text-jarvis-cyan-bright transition-colors"
            >
              Docs
            </a>
            <a
              href="#friday"
              className="hidden sm:inline label-mono hover:text-jarvis-cyan-bright transition-colors"
            >
              Friday
            </a>
            <StarButton />
            <a href={RELEASES_URL} className="jarvis-btn-primary">
              <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                <path d="M12 3v12" />
                <path d="m6 9 6 6 6-6" />
                <path d="M5 21h14" />
              </svg>
              <span>Download</span>
            </a>
          </div>
        </div>
      </nav>

      {/* ===================== HERO ===================== */}
      <section className="relative min-h-screen flex items-center pt-14">
        <div className="mx-auto max-w-6xl w-full px-6 grid grid-cols-1 lg:grid-cols-2 gap-12 items-center">
          {/* Left: copy */}
          <div className="relative z-10">
            <div className="flex items-center gap-2 mb-6">
              <span className="label-mono text-jarvis-cyan/60">◆ SYSTEM ONLINE</span>
              <span className="h-px flex-1 bg-gradient-to-r from-jarvis-cyan/40 to-transparent" />
            </div>

            <h1 className="font-sans font-bold text-5xl md:text-6xl lg:text-7xl text-cyan-50 leading-[1.05] tracking-tight text-balance">
              Talk to your <span className="text-jarvis-cyan glow-text-strong">AI coding agents.</span>
            </h1>

            <p className="mt-6 font-sans text-lg text-jarvis-cyan/65 max-w-xl text-balance">
              J.A.R.V.I.S. is a voice-driven orchestrator for AI coding agents. Launch Claude Code, Aider, Codex, Gemini, or Kiro sessions across multiple repositories with a single sentence — your hands stay on the keyboard, your agents stay coordinated.
            </p>

            <div className="mt-8 flex flex-wrap items-center gap-3">
              {/* Primary CTA: UA-matched installer. On 'unknown' (crawlers,
                  Linux, empty UA, opaque proxies) we render BOTH macOS +
                  Windows buttons so the visitor can't end up with a
                  one-platform homepage that doesn't match their machine. */}
              {detectedOS === 'windows' ? (
                <a href={exeUrl} className="jarvis-btn-primary" data-testid="cta-download-windows">
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                    <path d="M12 3v12" />
                    <path d="m6 9 6 6 6-6" />
                    <path d="M5 21h14" />
                  </svg>
                  <span>Download for Windows</span>
                </a>
              ) : detectedOS === 'macos' ? (
                <a href={dmgUrl} className="jarvis-btn-primary" data-testid="cta-download-macos">
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                    <path d="M12 3v12" />
                    <path d="m6 9 6 6 6-6" />
                    <path d="M5 21h14" />
                  </svg>
                  <span>Download for macOS</span>
                </a>
              ) : (
                <>
                  <a href={dmgUrl} className="jarvis-btn-primary" data-testid="cta-download-macos">
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                      <path d="M12 3v12" />
                      <path d="m6 9 6 6 6-6" />
                      <path d="M5 21h14" />
                    </svg>
                    <span>Download for macOS</span>
                  </a>
                  <a href={exeUrl} className="jarvis-btn-primary" data-testid="cta-download-windows">
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                      <path d="M12 3v12" />
                      <path d="m6 9 6 6 6-6" />
                      <path d="M5 21h14" />
                    </svg>
                    <span>Download for Windows</span>
                  </a>
                </>
              )}
              <a
                href={REPO_URL}
                target="_blank"
                rel="noreferrer noopener"
                className="jarvis-btn-secondary"
              >
                <svg width="13" height="13" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
                  <path d="M12 .5a12 12 0 0 0-3.79 23.4c.6.11.82-.26.82-.58v-2.02c-3.34.73-4.04-1.41-4.04-1.41-.55-1.39-1.34-1.76-1.34-1.76-1.09-.74.08-.73.08-.73 1.2.09 1.83 1.24 1.83 1.24 1.07 1.84 2.81 1.31 3.5 1 .1-.78.42-1.31.76-1.61-2.66-.3-5.47-1.34-5.47-5.95 0-1.32.47-2.39 1.24-3.23-.12-.31-.54-1.55.12-3.22 0 0 1.01-.33 3.3 1.23a11.46 11.46 0 0 1 6 0c2.29-1.56 3.3-1.23 3.3-1.23.66 1.67.24 2.91.12 3.22.77.84 1.24 1.91 1.24 3.23 0 4.62-2.81 5.64-5.49 5.94.43.37.81 1.1.81 2.22v3.29c0 .32.21.7.83.58A12 12 0 0 0 12 .5Z" />
                </svg>
                <span>View on GitHub</span>
              </a>
            </div>

            <ul className="mt-10 grid grid-cols-2 gap-x-6 gap-y-3 label-mono text-jarvis-cyan/55 max-w-sm">
              <li className="flex items-center gap-2"><span className="text-jarvis-cyan-bright">✓</span> Local STT + TTS</li>
              <li className="flex items-center gap-2"><span className="text-jarvis-cyan-bright">✓</span> Offline after setup</li>
              <li className="flex items-center gap-2"><span className="text-jarvis-cyan-bright">✓</span> macOS + Windows</li>
              <li className="flex items-center gap-2"><span className="text-jarvis-cyan-bright">✓</span> Open source · Apache-2.0</li>
            </ul>
          </div>

          {/* Right: 3D orb */}
          <div className="relative h-[400px] md:h-[520px] lg:h-[600px] -mx-6 lg:mx-0">
            <OrbClient />
            {/* Floating data readouts around the orb */}
            <div className="pointer-events-none absolute inset-0 hidden md:block">
              <span className="absolute top-8 left-2 label-mono animate-data-flicker">
                ▸ AGENT::CLAUDE-CODE
              </span>
              <span className="absolute top-16 right-4 label-mono animate-data-flicker [animation-delay:1.2s]">
                STT::WHISPER-SMALL.EN
              </span>
              <span className="absolute bottom-12 left-2 label-mono animate-data-flicker [animation-delay:2.4s]">
                TTS::VIBEVOICE-RT
              </span>
              <span className="absolute bottom-20 right-2 label-mono animate-data-flicker [animation-delay:3.1s]">
                SESSIONS::3
              </span>
            </div>
          </div>
        </div>

        {/* Decorative grid corners */}
        <span className="pointer-events-none absolute top-20 left-4 label-mono text-jarvis-cyan/30">▸ J.A.R.V.I.S.//SYS</span>
        <span className="pointer-events-none absolute top-20 right-4 label-mono text-jarvis-cyan/30">v{version} // STABLE</span>
        <span className="pointer-events-none absolute bottom-4 left-4 label-mono text-jarvis-cyan/30">⏚ MACOS · WINDOWS</span>
        <span className="pointer-events-none absolute bottom-4 right-4 label-mono text-jarvis-cyan/30 animate-pulse-soft">◉ READY</span>
      </section>

      {/* ===================== DEMO ===================== */}
      <section className="relative py-24 px-6">
        <div className="mx-auto max-w-5xl">
          <div className="mb-12 text-center">
            <span className="label-mono text-jarvis-cyan/55">SECTION_02 · LIVE_LOOP</span>
            <h2 className="mt-3 font-sans font-bold text-3xl md:text-4xl text-cyan-50 text-balance">
              Voice in. Sessions out.
            </h2>
            <p className="mt-3 font-sans text-jarvis-cyan/55 max-w-xl mx-auto text-balance">
              Wake word triggers the loop; Whisper transcribes locally; the LLM picks a tool; Jarvis speaks the reply. ~1.5s end-to-end on M2.
            </p>
          </div>

          <div className="jarvis-card max-w-3xl mx-auto">
            <span className="corner-bracket-tl" />
            <span className="corner-bracket-tr" />
            <span className="corner-bracket-bl" />
            <span className="corner-bracket-br" />
            <DemoTranscript />
          </div>
        </div>
      </section>

      {/* ===================== FEATURES ===================== */}
      <section className="relative py-24 px-6">
        <div className="mx-auto max-w-6xl">
          <div className="mb-12">
            <span className="label-mono text-jarvis-cyan/55">SECTION_03 · CAPABILITIES</span>
            <h2 className="mt-3 font-sans font-bold text-3xl md:text-4xl text-cyan-50 text-balance">
              Built for orchestrating a swarm of agents.
            </h2>
          </div>

          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-5">
            {FEATURES.map((f, i) => (
              <FeatureCard key={i} {...f} />
            ))}
          </div>
        </div>
      </section>

      {/* ===================== INSTALL ===================== */}
      <section id="install" className="relative py-24 px-6">
        <div className="mx-auto max-w-4xl">
          <div className="mb-10">
            <span className="label-mono text-jarvis-cyan/55">SECTION_04 · INSTALL</span>
            <h2 className="mt-3 font-sans font-bold text-3xl md:text-4xl text-cyan-50 text-balance">
              Five minutes to first reply.
            </h2>
          </div>

          <ol className="space-y-5">
            {INSTALL_STEPS.map((step, i) => (
              <li key={i} className="jarvis-card flex gap-5 items-start">
                <span className="corner-bracket-tl" />
                <span className="corner-bracket-br" />
                <div className="shrink-0 h-9 w-9 grid place-items-center border border-jarvis-cyan/50 text-jarvis-cyan-bright glow-text font-bold">
                  {String(i + 1).padStart(2, '0')}
                </div>
                <div className="flex-1">
                  <h3 className="font-sans font-semibold text-cyan-50">{step.title}</h3>
                  <p className="mt-1 font-mono text-sm text-jarvis-cyan/55 leading-relaxed">{step.body}</p>
                </div>
              </li>
            ))}
          </ol>

          <div className="mt-10 flex flex-wrap items-center justify-between gap-4 border-t border-jarvis-cyan-dark/40 pt-8">
            <div>
              <p className="label-mono text-jarvis-cyan/45 mb-2">FIRST-LAUNCH SETUP</p>
              <p className="font-mono text-sm text-jarvis-cyan/65 max-w-md">
                The DMG ships at ~35 MB. On first launch a full-screen progress UI installs a portable Python runtime + daemon venv into <code className="text-jarvis-cyan">~/.jarvis/</code> and fetches ~2.4 GB of VibeVoice + Whisper weights to <code className="text-jarvis-cyan">~/.cache/huggingface/</code> — ~10–15 min, one time. After first launch, Jarvis runs fully offline except your chosen cloud LLM.
              </p>
            </div>
            <div className="flex flex-wrap gap-3">
              {/* Mirror the hero CTA logic: UA-matched primary, both on
                  unknown. This block lives at the bottom of the install
                  section, so it's the last download offer a visitor sees
                  if they scroll past the hero. */}
              {detectedOS === 'windows' ? (
                <a href={exeUrl} className="jarvis-btn-primary">
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                    <path d="M12 3v12" />
                    <path d="m6 9 6 6 6-6" />
                    <path d="M5 21h14" />
                  </svg>
                  <span>Get the installer (.exe)</span>
                </a>
              ) : detectedOS === 'macos' ? (
                <a href={dmgUrl} className="jarvis-btn-primary">
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                    <path d="M12 3v12" />
                    <path d="m6 9 6 6 6-6" />
                    <path d="M5 21h14" />
                  </svg>
                  <span>Get the DMG</span>
                </a>
              ) : (
                <>
                  <a href={dmgUrl} className="jarvis-btn-primary">
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                      <path d="M12 3v12" />
                      <path d="m6 9 6 6 6-6" />
                      <path d="M5 21h14" />
                    </svg>
                    <span>Get the DMG</span>
                  </a>
                  <a href={exeUrl} className="jarvis-btn-primary">
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                      <path d="M12 3v12" />
                      <path d="m6 9 6 6 6-6" />
                      <path d="M5 21h14" />
                    </svg>
                    <span>Get the installer (.exe)</span>
                  </a>
                </>
              )}
              <a href="#friday" className="jarvis-btn-primary">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                  <rect x="7" y="2" width="10" height="20" rx="2" />
                  <path d="M11 18h2" />
                </svg>
                <span>Get Friday (phone)</span>
              </a>
            </div>
          </div>
        </div>
      </section>

      {/* ===================== FRIDAY (phone companion) ===================== */}
      <section id="friday" className="relative py-24 px-6 border-t border-jarvis-cyan-dark/40">
        <div className="mx-auto max-w-6xl">
          <div className="flex flex-col items-center mb-12">
            <span className="label-mono text-jarvis-cyan/45 mb-2">// PHONE COMPANION</span>
            <h2 className="font-mono text-3xl font-bold text-jarvis-cyan tracking-[0.25em] mb-3">FRIDAY</h2>
            <p className="font-mono text-sm text-jarvis-cyan/60 max-w-2xl text-center">
              Press-and-hold the orb on your phone to talk to your Mac&apos;s Jarvis from anywhere on the same Wi-Fi. Audio streams over WebSocket; the brain stays on your Mac.
            </p>
          </div>

          {/* QR + Expo Go buttons */}
          <div className="flex flex-col items-center mb-16">
            <div className="bg-white p-6 rounded">
              <QRCodeSVG value={EAS_URL} size={224} fgColor="#0a0a0a" bgColor="#ffffff" />
            </div>
            <p className="mt-5 font-mono text-xs text-jarvis-cyan/65 max-w-md text-center">
              Scan with Expo Go on your phone. Friday loads inside Expo Go — no App Store install needed.
            </p>
            <div className="mt-6 flex gap-4 flex-wrap justify-center">
              <a href={EXPO_GO_IOS} target="_blank" rel="noreferrer noopener" className="jarvis-btn-primary">Install Expo Go (iOS)</a>
              <a href={EXPO_GO_ANDROID} target="_blank" rel="noreferrer noopener" className="jarvis-btn-primary">Install Expo Go (Android)</a>
            </div>
          </div>

          {/* 5-step setup */}
          <p className="label-mono text-jarvis-cyan/45 mb-6 text-center">SETUP — 5 STEPS, ~3 MINUTES</p>
          <ol className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-5 gap-3">
            {FRIDAY_STEPS.map((step) => (
              <li key={step.num} className="jarvis-card">
                <span className="corner-bracket-tl" />
                <span className="corner-bracket-tr" />
                <span className="corner-bracket-bl" />
                <span className="corner-bracket-br" />
                <div className="font-mono text-lg text-jarvis-cyan-bright glow-text mb-2">{step.num}</div>
                <h3 className="font-sans font-semibold text-cyan-50 text-sm mb-2 leading-snug">{step.title}</h3>
                <p className="font-mono text-xs text-jarvis-cyan/55 leading-relaxed">{step.body}</p>
              </li>
            ))}
          </ol>

          {/* Use from anywhere — Tailscale */}
          <div className="mt-16 jarvis-card">
            <span className="corner-bracket-tl" />
            <span className="corner-bracket-tr" />
            <span className="corner-bracket-bl" />
            <span className="corner-bracket-br" />
            <div className="flex items-center gap-3 mb-3">
              <span className="text-2xl text-jarvis-cyan-bright glow-text">⌘</span>
              <h3 className="font-sans font-semibold text-cyan-50">USE FROM ANYWHERE (CELLULAR, COFFEE-SHOP WI-FI, ETC.)</h3>
            </div>
            <p className="font-mono text-sm text-jarvis-cyan/60 leading-relaxed mb-5">
              By default Friday only works when your phone and Mac are on the same Wi-Fi. To use Friday from cellular or any other network, run <a href="https://tailscale.com" target="_blank" rel="noreferrer noopener" className="text-jarvis-cyan underline underline-offset-2 hover:text-jarvis-cyan-bright">Tailscale</a> — a free personal-use mesh VPN. Zero changes to Jarvis itself; both devices get a virtual IP that works as if they were on the same LAN.
            </p>
            <ol className="grid grid-cols-1 md:grid-cols-3 gap-3">
              <li className="bg-jarvis-bg/40 border border-jarvis-cyan-dark/30 p-4 rounded">
                <div className="font-mono text-sm text-jarvis-cyan-bright glow-text mb-2">01 · MAC</div>
                <p className="font-mono text-xs text-jarvis-cyan/60 leading-relaxed mb-2">
                  Install Tailscale + sign in:
                </p>
                <code className="block font-mono text-[11px] text-jarvis-cyan/80 bg-black/40 p-2 rounded leading-relaxed">brew install tailscale<br />sudo tailscale up</code>
              </li>
              <li className="bg-jarvis-bg/40 border border-jarvis-cyan-dark/30 p-4 rounded">
                <div className="font-mono text-sm text-jarvis-cyan-bright glow-text mb-2">02 · PHONE</div>
                <p className="font-mono text-xs text-jarvis-cyan/60 leading-relaxed">
                  Install the Tailscale app from the App Store / Play Store. Sign in with the same account. Toggle it on.
                </p>
              </li>
              <li className="bg-jarvis-bg/40 border border-jarvis-cyan-dark/30 p-4 rounded">
                <div className="font-mono text-sm text-jarvis-cyan-bright glow-text mb-2">03 · PAIR</div>
                <p className="font-mono text-xs text-jarvis-cyan/60 leading-relaxed mb-2">
                  Get your Mac&apos;s Tailscale IP:
                </p>
                <code className="block font-mono text-[11px] text-jarvis-cyan/80 bg-black/40 p-2 rounded leading-relaxed mb-2">tailscale ip -4</code>
                <p className="font-mono text-xs text-jarvis-cyan/60 leading-relaxed">
                  Use that IP when generating the pairing QR (Settings → Connections → host field). Done.
                </p>
              </li>
            </ol>
            <p className="mt-4 font-mono text-[11px] text-jarvis-cyan/40 leading-relaxed">
              Tailscale free tier: 100 devices, 3 users per account — more than enough for a personal Mac + phone setup. Adds ~5-15 ms of latency vs LAN; voice still feels instant.
            </p>
          </div>

          {/* Android standalone APK + iOS note */}
          <div className="mt-12 grid grid-cols-1 md:grid-cols-2 gap-4">
            <div className="jarvis-card">
              <span className="corner-bracket-tl" />
              <span className="corner-bracket-tr" />
              <span className="corner-bracket-bl" />
              <span className="corner-bracket-br" />
              <p className="label-mono text-jarvis-cyan/45 mb-2">ANDROID · STANDALONE APP (OPTIONAL)</p>
              <p className="font-mono text-sm text-jarvis-cyan/60 leading-relaxed">
                Prefer a real home-screen icon? Download <a href={`${REPO_URL}/releases/latest`} className="text-jarvis-cyan underline underline-offset-2 hover:text-jarvis-cyan-bright">Friday-v{version}.apk</a> from the latest GitHub release. Enable <code className="text-jarvis-cyan/80">Install unknown apps</code> once, tap the APK, Friday installs as a standalone app.
              </p>
            </div>
            <div className="jarvis-card">
              <span className="corner-bracket-tl" />
              <span className="corner-bracket-tr" />
              <span className="corner-bracket-bl" />
              <span className="corner-bracket-br" />
              <p className="label-mono text-jarvis-cyan/45 mb-2">IOS · STANDALONE APP</p>
              <p className="font-mono text-sm text-jarvis-cyan/60 leading-relaxed">
                Apple doesn&apos;t offer a free standalone path. Friday runs inside Expo Go on iOS at no cost. A TestFlight build lands when iOS demand justifies the $99/yr Apple Developer fee.
              </p>
            </div>
          </div>
        </div>
      </section>

      {/* ===================== FOOTER ===================== */}
      <footer className="relative px-6 py-12 border-t border-jarvis-cyan-dark/40">
        <div className="mx-auto max-w-6xl flex flex-wrap items-center justify-between gap-4 label-mono text-jarvis-cyan/40">
          <div className="flex items-center gap-3">
            <span className="h-1.5 w-1.5 rounded-full bg-jarvis-cyan-bright shadow-[0_0_8px_rgba(34,211,238,0.8)] animate-pulse-soft" />
            <span>J.A.R.V.I.S. · APACHE-2.0 · STATUS: PUBLIC ALPHA</span>
          </div>
          <div className="flex items-center gap-5">
            <a href="#friday" className="hover:text-jarvis-cyan transition-colors">FRIDAY</a>
            <a href={REPO_URL} target="_blank" rel="noreferrer noopener" className="hover:text-jarvis-cyan transition-colors">GITHUB</a>
            <a href={RELEASES_URL} target="_blank" rel="noreferrer noopener" className="hover:text-jarvis-cyan transition-colors">RELEASES</a>
            <a href={`${REPO_URL}/issues`} target="_blank" rel="noreferrer noopener" className="hover:text-jarvis-cyan transition-colors">ISSUES</a>
          </div>
        </div>
      </footer>
    </main>
  )
}

// ---------------------------------------------------------------------------
// Feature card
// ---------------------------------------------------------------------------

interface Feature {
  glyph: string
  title: string
  body: string
}

const FEATURES: Feature[] = [
  {
    glyph: '◍',
    title: 'Multi-repo, multi-agent.',
    body: 'Orchestrate Claude Code, Aider, Codex, Gemini, and Kiro sessions across an arbitrary number of repos. Cross-session conflict detection warns you when two agents are about to touch the same file.',
  },
  {
    glyph: '⏚',
    title: 'Voice-first, GUI-second.',
    body: 'The HUD orb is the only screen. Everything else — start a session, create a workspace, approve a prompt — is a voice command. Hands stay on the keyboard, eyes stay on your editor.',
  },
  {
    glyph: '◈',
    title: 'Local-first by default.',
    body: 'Whisper-small.en runs on MLX. VibeVoice synthesizes locally. Wake-word detection runs on-device. Cloud LLMs are optional; Ollama works out of the box.',
  },
  {
    glyph: '⌬',
    title: 'Workspace mode.',
    body: 'Say "create a workspace with auth-service and payments" and Jarvis spins up a virtual monorepo with symlinks to both, auto-generates a CLAUDE.md, and launches the agent with cross-repo context.',
  },
  {
    glyph: '⊜',
    title: 'Streaming everywhere.',
    body: 'Pipecat-powered pipeline streams audio in, partial transcripts, LLM tokens, and TTS chunks in parallel. End-to-end latency from "Hey Jarvis" to first spoken syllable: ~1.5s on M2.',
  },
  {
    glyph: '♪',
    title: 'Spotify control.',
    body: 'Voice-driven playback. "Play Blinding Lights" routes through Spotify Web API search → AppleScript hands the URI to the Spotify desktop client. Pause, skip, queue, like, set volume — 9 tools in total. Works with Spotify Free for local playback.',
  },
  {
    glyph: '◰',
    title: 'Mac control.',
    body: 'Open and quit apps, focus windows, set volume and brightness, take screenshots, copy and paste, run any Shortcuts.app shortcut — all by voice. 15 tools gated by per-tool allow/ask/deny permissions. Destructive actions trigger a spoken "are you sure?" confirmation.',
  },
  {
    glyph: '◎',
    title: 'Friday phone companion.',
    body: 'Press-and-hold the orb on your phone to talk to the Mac\'s Jarvis. Audio streams over WebSocket; the brain stays on your Mac. Calendar chip + next-event tile on the home screen. Distributed via Expo Go — install the app, scan a QR, done. No App Store, no developer signing.',
  },
  {
    glyph: '◐',
    title: 'Overlay widget.',
    body: 'Frameless 320×420 always-on-top panel. Toggle with ⌥+Space from anywhere; talk via push-to-talk without leaving your editor. Dedicated ⌃+Space global PTT also works without the overlay visible. Five-icon control row: mute, PTT, interrupt, meeting record, back-to-main.',
  },
  {
    glyph: '⌖',
    title: 'Google Calendar.',
    body: 'Next event in the HUD, on Friday, and in the bottom stat row. 2 minutes before a meeting on your calendar matching keywords like "sync" or "1:1", a banner asks if you want Jarvis to start taking notes. One tap, no setup.',
  },
  {
    glyph: '●',
    title: 'Meeting mode.',
    body: 'Captures both your mic and your Mac\'s system audio via ScreenCaptureKit. Suppresses Jarvis\'s voice so it doesn\'t talk over the room. On stop, writes a Markdown file with summary, key points, action items, and raw transcript — and speaks a 2-sentence recap. Ask "Jarvis, what were the action items?" and it reads them back to you.',
  },
  {
    glyph: '✦',
    title: 'Open source.',
    body: 'Apache-2.0. ~50K LoC of Go (Wails backend) + TypeScript (React HUD) + Python (Pipecat daemon). Fork it, mod it, ship a derivative.',
  },
]

function FeatureCard({ glyph, title, body }: Feature) {
  return (
    <div className="jarvis-card group h-full">
      <span className="corner-bracket-tl" />
      <span className="corner-bracket-tr" />
      <span className="corner-bracket-bl" />
      <span className="corner-bracket-br" />
      <div className="flex items-start gap-3 mb-3">
        <span className="text-2xl text-jarvis-cyan-bright glow-text group-hover:scale-110 transition-transform">{glyph}</span>
        <h3 className="font-sans font-semibold text-cyan-50">{title}</h3>
      </div>
      <p className="font-mono text-sm text-jarvis-cyan/55 leading-relaxed">{body}</p>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Install steps
// ---------------------------------------------------------------------------

const INSTALL_STEPS = [
  {
    title: 'Download the installer for your platform.',
    body: 'macOS: ~35 MB DMG, Apple Silicon (M1+) on macOS 12 or newer, signed with a Developer ID and notarized — no Gatekeeper warnings. Windows: ~40 MB Inno Setup .exe, Windows 10 / 11 on x64 or arm64, code-signed — no SmartScreen "Unknown publisher" warning.',
  },
  {
    title: 'Run the installer.',
    body: 'macOS: open the DMG and drag Jarvis onto Applications. Windows: double-click Jarvis-Setup-<version>.exe and follow the wizard — Start Menu shortcut is created, desktop shortcut is optional. WebView2 runtime auto-installs on Win10 if missing.',
  },
  {
    title: 'First-launch setup runs automatically.',
    body: 'A full-screen progress UI walks through four phases — Python runtime, voice pipeline venv, VibeVoice (~1.9 GB), Whisper (~460 MB). ~10–15 min first-launch setup (Python + venv + ~2.4 GB of voice + speech models). You can keep using your Mac while it runs. After first launch, Jarvis runs fully offline except your chosen cloud LLM.',
  },
  {
    title: 'Grant microphone permission and finish onboarding.',
    body: 'A short modal walks you through choosing an LLM provider (or local Ollama), pasting an API key if applicable, and previewing a voice. Say "Hey Jarvis" to begin.',
  },
  {
    title: 'Install Friday on your phone (optional).',
    body: 'Friday is the v0.3 phone companion — press-and-hold the orb to talk to your Mac\'s Jarvis from anywhere on the same Wi-Fi. Open https://jarvis.namanchopra.dev/friday for the full setup walkthrough: install Expo Go, scan the project QR, then pair via Settings → Connections → "Connect Friday phone" in Jarvis. Android users can also sideload a signed APK from the GitHub release.',
  },
  {
    title: 'Grant Screen Recording (only if you want meeting mode).',
    body: 'System Settings → Privacy & Security → Screen Recording → enable Jarvis. Required for the meeting mode feature to capture system audio (the people on the call). Mic-only recording works without it on macOS 12 and below.',
  },
]
