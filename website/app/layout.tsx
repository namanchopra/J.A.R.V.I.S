import type { Metadata, Viewport } from 'next'
import './globals.css'

export const metadata: Metadata = {
  title: 'J.A.R.V.I.S. — A voice-driven AI agent orchestrator for macOS',
  description:
    'Drive Claude Code, Aider, Codex, Gemini, and Kiro sessions across multiple repos by talking to them. Local STT + TTS, runs offline after first launch, Apple Silicon native.',
  metadataBase: new URL('https://github.com/namanchopra/J.A.R.V.I.S'),
  openGraph: {
    title: 'J.A.R.V.I.S. — voice-driven AI agent orchestrator',
    description:
      'Talk to your coding agents. Multi-repo, multi-agent, offline-first. Open source.',
    type: 'website',
  },
}

export const viewport: Viewport = {
  themeColor: '#020508',
}

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body className="min-h-screen overflow-x-hidden">{children}</body>
    </html>
  )
}
