'use client'

import { useEffect, useState } from 'react'

interface TranscriptLine {
  role: 'user' | 'jarvis' | 'system'
  text: string
  delay?: number
}

const SCRIPT: TranscriptLine[] = [
  { role: 'system', text: 'WAKE WORD DETECTED · "HEY JARVIS"', delay: 600 },
  { role: 'user', text: 'Start a session in the auth service.', delay: 900 },
  { role: 'system', text: '⏵ tool_call: launch_session(agent="claude-code", repo="auth-service")', delay: 1100 },
  { role: 'jarvis', text: 'Claude is launching in auth-service, sir.', delay: 1400 },
  { role: 'user', text: "What's running right now?", delay: 1500 },
  { role: 'system', text: '⏵ tool_call: get_status()', delay: 900 },
  { role: 'jarvis', text: 'Three sessions active. Auth-service just started; payments is running tests; web-app needs your attention on an approval prompt.', delay: 2000 },
  { role: 'user', text: 'Approve it.', delay: 1100 },
  { role: 'system', text: '⏵ tool_call: approve_session(name="web-app")', delay: 800 },
  { role: 'jarvis', text: 'Approved. Web-app is back to work.', delay: 1200 },
]

export default function DemoTranscript() {
  const [visible, setVisible] = useState<number>(0)
  const [typing, setTyping] = useState<boolean>(false)

  useEffect(() => {
    if (visible >= SCRIPT.length) {
      // Loop after a pause.
      const id = setTimeout(() => setVisible(0), 3500)
      return () => clearTimeout(id)
    }
    setTyping(true)
    const line = SCRIPT[visible]!
    const id = setTimeout(() => {
      setVisible((v) => v + 1)
      setTyping(false)
    }, line.delay ?? 1000)
    return () => clearTimeout(id)
  }, [visible])

  return (
    <div className="font-mono text-sm leading-relaxed">
      <div className="mb-3 flex items-center justify-between">
        <span className="label-mono">▸ Voice Loop · LIVE TRANSCRIPT</span>
        <span className="flex items-center gap-2 label-mono">
          <span className="h-1.5 w-1.5 rounded-full bg-red-500 shadow-[0_0_8px_rgba(239,68,68,0.7)] animate-pulse-soft" />
          REC
        </span>
      </div>

      <div className="space-y-2.5 min-h-[300px]">
        {SCRIPT.slice(0, visible).map((line, i) => (
          <TranscriptRow key={i} line={line} />
        ))}
        {typing && visible < SCRIPT.length && (
          <div className="flex items-center gap-2 text-jarvis-cyan/40">
            <span className="label-mono">{labelFor(SCRIPT[visible]!.role)}</span>
            <span className="h-2 w-2 bg-jarvis-cyan animate-blink" />
          </div>
        )}
      </div>
    </div>
  )
}

function labelFor(role: TranscriptLine['role']): string {
  switch (role) {
    case 'user': return 'YOU      ::'
    case 'jarvis': return 'JARVIS   ::'
    case 'system': return 'SYS      ::'
  }
}

function colorFor(role: TranscriptLine['role']): string {
  switch (role) {
    case 'user': return 'text-cyan-100'
    case 'jarvis': return 'text-jarvis-cyan glow-text'
    case 'system': return 'text-jarvis-cyan/45'
  }
}

function TranscriptRow({ line }: { line: TranscriptLine }) {
  return (
    <div className="flex gap-3 animate-float-up">
      <span className="label-mono pt-0.5 shrink-0">{labelFor(line.role)}</span>
      <span className={`${colorFor(line.role)} text-balance`}>{line.text}</span>
    </div>
  )
}
