import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  FocusSession,
  GetSessionTerminalOutput,
  GetRepoInfo,
} from '../../wailsjs/go/main/App'
import { git } from '../../wailsjs/go/models'
import { DiffViewer } from './DiffViewer'
import { GitActionsPanel } from './GitActionsPanel'
import { SessionTodoPanel } from './SessionTodoPanel'
import { useDuration } from '../lib/hooks'
import { parseTerminalOutput } from '../lib/terminal-parser'
import { BlockRenderer } from './terminal/BlockRenderers'
import { ActivityTimeline } from './terminal/ActivityTimeline'
import type { claude } from '../../wailsjs/go/models'
import type { TerminalBlock } from '../lib/terminal-parser'

// ---------------------------------------------------------------------------
// Block filter types
// ---------------------------------------------------------------------------

type BlockFilter = 'all' | 'tools' | 'text' | 'agents'

const BLOCK_FILTER_MAP: Record<BlockFilter, ReadonlySet<TerminalBlock['type']>> = {
  all: new Set(['tool-call', 'tool-output', 'text', 'completion', 'prompt', 'collapsed', 'summary', 'separator', 'agent-spawn']),
  tools: new Set(['tool-call', 'tool-output']),
  text: new Set(['text', 'completion', 'prompt']),
  agents: new Set(['agent-spawn']),
}

const FILTER_BUTTONS: { id: BlockFilter; label: string }[] = [
  { id: 'all', label: 'All' },
  { id: 'tools', label: 'Tools' },
  { id: 'text', label: 'Text' },
  { id: 'agents', label: 'Agents' },
]

/** Max blocks rendered at once to prevent lag on long sessions. */
const MAX_VISIBLE_BLOCKS = 200
/** Threshold at which truncation kicks in. */
const TRUNCATION_THRESHOLD = 500

// ---------------------------------------------------------------------------
// Animated waiting dots
// ---------------------------------------------------------------------------

function WaitingDots(): React.ReactElement {
  return (
    <div className="flex items-center justify-center py-12 gap-1">
      <span className="text-sm text-muted">Waiting for output</span>
      <span className="inline-flex gap-0.5">
        <span className="w-1 h-1 rounded-full bg-muted animate-[pulse_1.4s_ease-in-out_infinite]" />
        <span className="w-1 h-1 rounded-full bg-muted animate-[pulse_1.4s_ease-in-out_0.2s_infinite]" />
        <span className="w-1 h-1 rounded-full bg-muted animate-[pulse_1.4s_ease-in-out_0.4s_infinite]" />
      </span>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Info card
// ---------------------------------------------------------------------------

function InfoCard({ label, value, mono }: { label: string; value: string; mono?: boolean }): React.ReactElement {
  return (
    <div className="p-2.5 rounded-lg bg-surface border border-border-m">
      <p className="text-[10px] text-muted uppercase tracking-wider">{label}</p>
      <p className={`text-sm text-primary mt-0.5 ${mono ? 'font-mono' : ''}`}>{value}</p>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Session detail panel (used as inline panel OR modal overlay)
// ---------------------------------------------------------------------------

export function SessionDetailPanel({
  session,
  indicator,
  onClose,
}: {
  session?: claude.Session
  indicator?: claude.SessionIndicator
  onClose?: () => void
}): React.ReactElement {
  const [terminalOutput, setTerminalOutput] = useState('')
  const [gitInfo, setGitInfo] = useState<git.RepoInfo | null>(null)
  const [activeTab, setActiveTab] = useState<'terminal' | 'overview' | 'diff' | 'git'>('terminal')

  // Unify session vs indicator data
  const pid = indicator?.pid ?? session?.pid ?? 0
  const cwd = indicator?.cwd ?? session?.cwd ?? ''
  const sessionId = indicator?.sessionId ?? session?.sessionId ?? ''
  const startedAt = indicator?.startedAt ?? session?.startedAt ?? Date.now()
  const displayName = indicator?.name ?? session?.name ?? cwd.split('/').pop() ?? 'Session'

  const duration = useDuration(startedAt)
  const overlayRef = useRef<HTMLDivElement>(null)

  // Parsed terminal blocks
  const parsedBlocks = useMemo(() => parseTerminalOutput(terminalOutput), [terminalOutput])

  // Terminal block filter
  const [blockFilter, setBlockFilter] = useState<BlockFilter>('all')

  // Scroll-lock toggle (when true, auto-scroll is disabled)
  const [scrollLocked, setScrollLocked] = useState(false)
  const blockListEndRef = useRef<HTMLDivElement>(null)

  // Auto-scroll to bottom when new blocks arrive (unless user locked scroll)
  useEffect(() => {
    if (!scrollLocked && parsedBlocks.length > 0) {
      blockListEndRef.current?.scrollIntoView({ behavior: 'smooth' })
    }
  }, [parsedBlocks.length, scrollLocked])

  // Pre-computed index map: block reference -> index in parsedBlocks (O(n) build, O(1) lookup)
  const blockIndexMap = useMemo(() => {
    const map = new Map<TerminalBlock, number>()
    for (let i = 0; i < parsedBlocks.length; i++) {
      map.set(parsedBlocks[i]!, i)
    }
    return map
  }, [parsedBlocks])

  // Filtered + truncated blocks for display
  const { visibleBlocks, isTruncated, totalCount } = useMemo(() => {
    const allowedTypes = BLOCK_FILTER_MAP[blockFilter]
    const filtered = parsedBlocks.filter((b) => allowedTypes.has(b.type))
    const total = filtered.length
    const shouldTruncate = total >= TRUNCATION_THRESHOLD
    const visible = shouldTruncate ? filtered.slice(-MAX_VISIBLE_BLOCKS) : filtered
    return { visibleBlocks: visible, isTruncated: shouldTruncate, totalCount: total }
  }, [parsedBlocks, blockFilter])

  // Fetch git info
  useEffect(() => {
    if (!cwd) return
    GetRepoInfo(cwd).then(setGitInfo).catch((err) => console.warn('Failed to fetch repo info:', err))
  }, [cwd])

  // Read terminal output periodically via the new PID-based API
  useEffect(() => {
    if (!pid) return
    const fetchOutput = (): void => {
      GetSessionTerminalOutput(pid).then(setTerminalOutput).catch((err) => console.warn('Failed to fetch terminal output:', err))
    }
    fetchOutput()
    const id = setInterval(fetchOutput, 2000)
    return () => clearInterval(id)
  }, [pid])

  const handleFocus = useCallback(async (): Promise<void> => {
    if (pid) await FocusSession(pid)
  }, [pid])

  // Close on Escape
  useEffect(() => {
    if (!onClose) return
    const handler = (e: KeyboardEvent): void => {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [onClose])

  // Close on backdrop click
  const handleBackdropClick = useCallback(
    (e: React.MouseEvent): void => {
      if (onClose && e.target === overlayRef.current) onClose()
    },
    [onClose],
  )

  const terminalLabel = parsedBlocks.length > 0 ? `Terminal (${parsedBlocks.length})` : 'Terminal'
  const tabs = [
    { id: 'terminal' as const, label: terminalLabel },
    { id: 'overview' as const, label: 'Overview' },
    { id: 'diff' as const, label: 'Changes' },
    { id: 'git' as const, label: 'Git' },
  ]

  const content = (
    <div className="flex flex-col h-full min-h-0">
      {/* Action bar at top */}
      <div className="px-5 py-2.5 bg-app border-b border-border-m flex items-center gap-2">
        <button
          type="button"
          onClick={() => void handleFocus()}
          className="flex-1 px-4 py-2 text-sm rounded-lg bg-acc-teal hover:bg-acc-teal/80 text-white transition-colors font-semibold flex items-center justify-center gap-2"
        >
          <svg className="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
            <rect x="2" y="3" width="20" height="18" rx="2" />
            <path d="M7 8l3 3-3 3" />
            <path d="M13 16h4" />
          </svg>
          Focus in Terminal
        </button>
        <button
          type="button"
          onClick={async () => {
            const w = window as unknown as Record<string, unknown>
            const goNs = w?.go as Record<string, unknown> | undefined
            const appObj = (goNs?.main as Record<string, unknown>)?.App as Record<string, unknown> | undefined
            const forkFn = appObj?.ForkSession as ((id: string) => Promise<unknown>) | undefined
            if (forkFn && sessionId) {
              try {
                await forkFn(sessionId)
              } catch (e) {
                console.warn('Fork failed:', e)
              }
            }
          }}
          className="px-3 py-2 text-sm rounded-lg bg-indigo-500/20 text-indigo-400 hover:bg-indigo-500/30 transition-colors font-medium flex items-center gap-1.5"
          title="Fork this session"
        >
          <svg className="w-3.5 h-3.5" viewBox="0 0 16 16" fill="currentColor">
            <path fillRule="evenodd" d="M5 3.25a.75.75 0 11-1.5 0 .75.75 0 011.5 0zm0 2.122a2.25 2.25 0 10-1.5 0v.878A2.25 2.25 0 005.75 8.5h1.5v2.128a2.251 2.251 0 101.5 0V8.5h1.5a2.25 2.25 0 002.25-2.25v-.878a2.25 2.25 0 10-1.5 0v.878a.75.75 0 01-.75.75h-4.5A.75.75 0 015 6.25v-.878zm3.75 7.378a.75.75 0 11-1.5 0 .75.75 0 011.5 0zm3-8.75a.75.75 0 100-1.5.75.75 0 000 1.5z" clipRule="evenodd" />
          </svg>
          Fork
        </button>
      </div>

      {/* Header */}
      <div className="px-5 py-3 border-b border-border bg-surface">
        <div className="flex items-center justify-between">
          <div>
            <h2 className="text-base font-semibold text-primary">{displayName}</h2>
            <p className="text-xs text-muted font-mono mt-0.5">{cwd}</p>
            {/* Fork indicator -- show if this session was forked from another */}
            {session && Boolean((session as unknown as Record<string, unknown>).parentSessionId) && (
              <span className="text-xs text-gray-400 mt-0.5 inline-block">
                {'\u2442'} Forked from {String((session as unknown as Record<string, unknown>).parentSessionId).slice(0, 8)}...
              </span>
            )}
          </div>
          <div className="flex items-center gap-2">
            <span className="text-xs text-secondary">{duration}</span>
            {indicator?.hasQuestion ? (
              <span className="text-[10px] px-1.5 py-0.5 rounded bg-teal-500/15 text-acc-teal">needs input</span>
            ) : (
              <span className="text-[10px] px-1.5 py-0.5 rounded bg-green-500/15 text-green-400">running</span>
            )}
            {onClose && (
              <button
                type="button"
                onClick={onClose}
                className="ml-2 text-muted hover:text-primary transition-colors"
                aria-label="Close panel"
              >
                <svg className="w-5 h-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                  <path d="M18 6L6 18M6 6l12 12" />
                </svg>
              </button>
            )}
          </div>
        </div>

        {/* Tabs */}
        <div className="flex gap-4 mt-3">
          {tabs.map((tab) => (
            <button
              key={tab.id}
              type="button"
              onClick={() => setActiveTab(tab.id)}
              className={`text-xs pb-1 transition-colors border-b-2 ${
                activeTab === tab.id
                  ? 'border-acc-teal text-acc-teal'
                  : 'border-transparent text-muted hover:text-secondary'
              }`}
            >
              {tab.label}
            </button>
          ))}
        </div>
      </div>

      {/* Per-session todos */}
      {sessionId && <SessionTodoPanel sessionId={sessionId} />}

      {/* Tab content */}
      <div className="flex-1 overflow-y-auto">
        {activeTab === 'terminal' && (
          <div className="flex flex-1 min-h-0">
            {/* Left column — block list (70%) */}
            <div className="flex flex-col w-[70%] min-h-0 border-r border-border-m">
              {/* Filter bar */}
              <div className="flex items-center gap-1.5 px-3 py-2 border-b border-border-m bg-surface shrink-0">
                {FILTER_BUTTONS.map((fb) => (
                  <button
                    key={fb.id}
                    type="button"
                    onClick={() => setBlockFilter(fb.id)}
                    className={`px-2 py-0.5 text-[10px] rounded transition-colors ${
                      blockFilter === fb.id
                        ? 'bg-acc-teal/20 text-acc-teal border border-acc-teal/30'
                        : 'text-muted hover:text-secondary border border-transparent'
                    }`}
                  >
                    {fb.label}
                  </button>
                ))}

                {/* Scroll-lock toggle */}
                <button
                  type="button"
                  onClick={() => setScrollLocked((v) => !v)}
                  className={`ml-auto px-1.5 py-0.5 text-[10px] rounded transition-colors ${
                    scrollLocked
                      ? 'bg-amber-600/20 text-amber-400 border border-amber-500/30'
                      : 'text-muted hover:text-secondary border border-transparent'
                  }`}
                  title={scrollLocked ? 'Auto-scroll paused' : 'Auto-scroll active'}
                  aria-label={scrollLocked ? 'Resume auto-scroll' : 'Pause auto-scroll'}
                >
                  {scrollLocked ? (
                    <svg className="w-3 h-3" viewBox="0 0 16 16" fill="currentColor">
                      <path d="M8 1a5 5 0 0 0-5 5v2H2a1 1 0 0 0-1 1v5a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1V9a1 1 0 0 0-1-1h-1V6a5 5 0 0 0-5-5zm3 7H5V6a3 3 0 1 1 6 0v2z" />
                    </svg>
                  ) : (
                    <svg className="w-3 h-3" viewBox="0 0 16 16" fill="currentColor">
                      <path d="M8 13.5a.5.5 0 0 1-.5-.5V4.707L5.354 6.854a.5.5 0 1 1-.708-.708l3-3a.5.5 0 0 1 .708 0l3 3a.5.5 0 0 1-.708.708L8.5 4.707V13a.5.5 0 0 1-.5.5z" />
                    </svg>
                  )}
                </button>
              </div>

              {/* Block list */}
              <div className="flex-1 overflow-y-auto px-3 py-2 space-y-1">
                {parsedBlocks.length === 0 ? (
                  <WaitingDots />
                ) : (
                  <>
                    {isTruncated && (
                      <div className="text-[10px] text-muted text-center py-1 border-b border-border-m mb-1">
                        Showing last {MAX_VISIBLE_BLOCKS} of {totalCount} blocks
                      </div>
                    )}
                    {visibleBlocks.map((block, idx) => {
                      // For tool-call / agent-spawn blocks, check if the next block in the
                      // full parsedBlocks array is a tool-output so we can pair them.
                      let outputBlock: TerminalBlock | undefined
                      if (block.type === 'tool-call' || block.type === 'agent-spawn') {
                        // O(1) lookup via pre-computed index map instead of O(n) indexOf
                        const fullIdx = blockIndexMap.get(block) ?? -1
                        if (fullIdx !== -1 && fullIdx + 1 < parsedBlocks.length) {
                          const next = parsedBlocks[fullIdx + 1]
                          if (next && next.type === 'tool-output') {
                            outputBlock = next
                          }
                        }
                      }

                      return (
                        <BlockRenderer
                          key={idx}
                          block={block}
                          output={outputBlock}
                        />
                      )
                    })}
                    <div ref={blockListEndRef} />
                  </>
                )}
              </div>
            </div>

            {/* Right column — activity timeline (30%) */}
            <div className="w-[30%] overflow-y-auto p-3 bg-app sticky top-0">
              <h3 className="text-[10px] text-muted uppercase tracking-wider font-semibold mb-2">Activity</h3>
              <ActivityTimeline blocks={parsedBlocks} />
            </div>
          </div>
        )}

        {activeTab === 'overview' && (
          <div className="p-5 space-y-4">
            <div className="grid grid-cols-2 gap-3">
              <InfoCard label="Agent" value="Claude Code" />
              <InfoCard label="PID" value={String(pid)} />
              <InfoCard label="Session ID" value={sessionId ? sessionId.slice(0, 8) + '...' : 'N/A'} mono />
              <InfoCard label="Duration" value={duration} />
              {indicator && (
                <>
                  <InfoCard label="Activity" value={indicator.lastActivity || 'idle'} />
                  <InfoCard label="Tokens" value={indicator.tokensUsed > 0 ? `${(indicator.tokensUsed / 1000).toFixed(1)}k` : '0'} />
                </>
              )}
              {session && (
                <>
                  <InfoCard label="Kind" value={session.kind} />
                  <InfoCard label="Entrypoint" value={session.entrypoint} />
                </>
              )}
            </div>

            {/* Git info */}
            {gitInfo && (
              <div className="p-3 rounded-lg bg-surface border border-border">
                <h3 className="text-xs font-semibold text-secondary uppercase tracking-wider mb-2">Git</h3>
                <div className="flex flex-wrap gap-2">
                  <span className="text-xs px-2 py-0.5 rounded bg-elevated text-primary font-mono">{gitInfo.branch}</span>
                  {gitInfo.filesChanged > 0 && (
                    <span className="text-xs">
                      <span className="text-green-400">+{gitInfo.insertions}</span>
                      {' / '}
                      <span className="text-red-400">-{gitInfo.deletions}</span>
                    </span>
                  )}
                  {gitInfo.isClean && <span className="text-xs text-green-400">Clean</span>}
                  {gitInfo.hasUnpushed && <span className="text-xs text-amber-400">Unpushed</span>}
                </div>
              </div>
            )}
          </div>
        )}

        {activeTab === 'diff' && (
          <div className="p-5">
            <DiffViewer repoPath={cwd} />
          </div>
        )}

        {activeTab === 'git' && (
          <div className="p-5">
            <GitActionsPanel repoPath={cwd} gitInfo={gitInfo ?? undefined} />
          </div>
        )}
      </div>
    </div>
  )

  // If onClose is provided, render as modal overlay
  if (onClose) {
    return (
      <div
        ref={overlayRef}
        onClick={handleBackdropClick}
        className="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
      >
        <div className="w-full max-w-3xl max-h-[85vh] bg-app border border-border rounded-xl shadow-2xl overflow-hidden flex flex-col">
          {content}
        </div>
      </div>
    )
  }

  // Otherwise render inline
  return <div className="flex-1 flex flex-col min-h-0">{content}</div>
}
