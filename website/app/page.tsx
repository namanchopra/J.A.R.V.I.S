import StarButton from '@/components/StarButton'
import DemoTranscript from '@/components/DemoTranscript'
import OrbClient from '@/components/OrbClient'

const REPO_URL = 'https://github.com/namanchopra/J.A.R.V.I.S'
const RELEASES_URL = `${REPO_URL}/releases/latest`

// Hardcoded fallback — kept in sync with the latest released DMG so the
// site never serves a 404 even if the GitHub API call below fails at
// build time (e.g. rate-limited or offline). Bump this whenever a new
// tag ships; `fetchLatestVersion` will override it on every cache miss.
const FALLBACK_VERSION = '0.1.6'

/** Latest release tag (e.g. "0.1.6"), fetched once per hour at build time. */
async function fetchLatestVersion(): Promise<string> {
  try {
    const res = await fetch(
      'https://api.github.com/repos/namanchopra/J.A.R.V.I.S/releases/latest',
      {
        headers: { Accept: 'application/vnd.github+json' },
        // Revalidate every hour so a fresh tag is picked up automatically
        // without needing a manual website redeploy.
        next: { revalidate: 3600 },
      },
    )
    if (!res.ok) return FALLBACK_VERSION
    const data = (await res.json()) as { tag_name?: string }
    // GitHub returns tag like "v0.1.6"; strip the leading v.
    return data.tag_name?.replace(/^v/, '') ?? FALLBACK_VERSION
  } catch {
    return FALLBACK_VERSION
  }
}

export default async function Page() {
  const version = await fetchLatestVersion()
  const dmgUrl = `${REPO_URL}/releases/download/v${version}/Jarvis-${version}.dmg`
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
              <a href={dmgUrl} className="jarvis-btn-primary">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                  <path d="M12 3v12" />
                  <path d="m6 9 6 6 6-6" />
                  <path d="M5 21h14" />
                </svg>
                <span>Download for macOS</span>
              </a>
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
              <li className="flex items-center gap-2"><span className="text-jarvis-cyan-bright">✓</span> Apple Silicon native</li>
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
        <span className="pointer-events-none absolute bottom-4 left-4 label-mono text-jarvis-cyan/30">⏚ APPLE SILICON ONLY</span>
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
                The DMG ships at ~80 MB. On first launch a full-screen progress UI installs a portable Python runtime + daemon venv into <code className="text-jarvis-cyan">~/.jarvis/</code> and fetches ~2.4 GB of VibeVoice + Whisper weights to <code className="text-jarvis-cyan">~/.cache/huggingface/</code> — ~10–15 min, one time. After first launch, Jarvis runs fully offline except your chosen cloud LLM.
              </p>
            </div>
            <a href={dmgUrl} className="jarvis-btn-primary">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                <path d="M12 3v12" />
                <path d="m6 9 6 6 6-6" />
                <path d="M5 21h14" />
              </svg>
              <span>Get the DMG</span>
            </a>
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
    title: 'Download the DMG.',
    body: 'About 80 MB. Apple Silicon Macs (M1 / M2 / M3 / M4) on macOS 12 or newer.',
  },
  {
    title: 'Mount, drag Jarvis.app to /Applications.',
    body: 'Standard macOS install.',
  },
  {
    title: 'Right-click → Open the first time.',
    body: 'The build is ad-hoc signed. Double-clicking shows "developer cannot be verified". Right-clicking and choosing Open bypasses the prompt; macOS remembers the choice forever after.',
  },
  {
    title: 'First-launch setup runs automatically.',
    body: 'A full-screen progress UI walks through four phases — Python runtime, voice pipeline venv, VibeVoice (~1.9 GB), Whisper (~460 MB). ~10–15 min first-launch setup (Python + venv + ~2.4 GB of voice + speech models). You can keep using your Mac while it runs. After first launch, Jarvis runs fully offline except your chosen cloud LLM.',
  },
  {
    title: 'Grant microphone permission and finish onboarding.',
    body: 'A short modal walks you through choosing an LLM provider (or local Ollama), pasting an API key if applicable, and previewing a voice. Say "Hey Jarvis" to begin.',
  },
]
