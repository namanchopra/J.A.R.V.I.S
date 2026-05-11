import { useCallback, useEffect, useState } from 'react'
import {
  GitStageAll,
  GitCommit,
  GitPush,
  GitCreateBranch,
  OpenPRInBrowser,
} from '../../wailsjs/go/main/App'
import { git } from '../../wailsjs/go/models'

// ---------------------------------------------------------------------------
// Props
// ---------------------------------------------------------------------------

interface GitActionsPanelProps {
  repoPath: string
  gitInfo?: git.RepoInfo | null
}

// ---------------------------------------------------------------------------
// Shared types
// ---------------------------------------------------------------------------

type ActionState = 'idle' | 'loading' | 'success' | 'error'

// ---------------------------------------------------------------------------
// Icons
// ---------------------------------------------------------------------------

function SpinnerIcon({ className }: { className?: string }): React.ReactElement {
  return (
    <svg
      className={`animate-spin ${className ?? 'w-3.5 h-3.5'}`}
      viewBox="0 0 16 16"
      fill="none"
      aria-hidden="true"
    >
      <circle cx="8" cy="8" r="6" stroke="currentColor" strokeWidth="2" opacity="0.25" />
      <path
        d="M14 8a6 6 0 00-6-6"
        stroke="currentColor"
        strokeWidth="2"
        strokeLinecap="round"
      />
    </svg>
  )
}

function CheckIcon(): React.ReactElement {
  return (
    <svg
      className="w-3.5 h-3.5 text-green-400"
      viewBox="0 0 20 20"
      fill="currentColor"
      aria-hidden="true"
    >
      <path
        fillRule="evenodd"
        d="M16.707 5.293a1 1 0 010 1.414l-8 8a1 1 0
           01-1.414 0l-4-4a1 1 0 011.414-1.414L8 12.586l7.293-7.293a1
           1 0 011.414 0z"
        clipRule="evenodd"
      />
    </svg>
  )
}

function WarningIcon(): React.ReactElement {
  return (
    <svg
      className="w-3.5 h-3.5 text-amber-400"
      viewBox="0 0 20 20"
      fill="currentColor"
      aria-hidden="true"
    >
      <path
        fillRule="evenodd"
        d="M8.257 3.099c.765-1.36 2.722-1.36 3.486 0l5.58 9.92c.75 1.334-.213
           2.98-1.742 2.98H4.42c-1.53 0-2.493-1.646-1.743-2.98l5.58-9.92zM11
           13a1 1 0 11-2 0 1 1 0 012 0zm-1-8a1 1 0 00-1 1v3a1 1 0 002 0V6a1
           1 0 00-1-1z"
        clipRule="evenodd"
      />
    </svg>
  )
}

// ---------------------------------------------------------------------------
// Branch name validation
// ---------------------------------------------------------------------------

const INVALID_BRANCH_PATTERNS = /[\s~^:?*\[\\]|\.\.|\.$|^\/|\/$|\.lock$|@\{/

function isValidBranchName(name: string): boolean {
  if (name.length === 0) return false
  if (INVALID_BRANCH_PATTERNS.test(name)) return false
  return true
}

// ---------------------------------------------------------------------------
// Stash / Checkpoint types & helpers
// ---------------------------------------------------------------------------

interface StashEntry {
  index: number
  name: string
  date: string
}

/** Format a date string into a compact relative time (e.g. "2h ago"). */
function relativeTime(dateStr: string): string {
  if (!dateStr) return ''
  const ts = new Date(dateStr).getTime()
  if (Number.isNaN(ts)) return dateStr

  const diffMs = Date.now() - ts
  if (diffMs < 0) return 'just now'

  const seconds = Math.floor(diffMs / 1000)
  if (seconds < 60) return 'just now'
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ago`
  const days = Math.floor(hours / 24)
  return `${days}d ago`
}

// ---------------------------------------------------------------------------
// Safe Wails binding accessors for stash operations
// ---------------------------------------------------------------------------

async function gitStash(repoPath: string, name: string): Promise<void> {
  const fn = window?.go?.main?.App?.GitStash as
    | ((rp: string, n: string) => Promise<void>)
    | undefined
  if (!fn) throw new Error('GitStash binding not available')
  return fn(repoPath, name)
}

async function gitStashList(repoPath: string): Promise<StashEntry[]> {
  const fn = window?.go?.main?.App?.GitStashList as
    | ((rp: string) => Promise<StashEntry[]>)
    | undefined
  if (!fn) return []
  return fn(repoPath)
}

async function gitStashApply(repoPath: string, index: number): Promise<void> {
  const fn = window?.go?.main?.App?.GitStashApply as
    | ((rp: string, idx: number) => Promise<void>)
    | undefined
  if (!fn) throw new Error('GitStashApply binding not available')
  return fn(repoPath, index)
}

async function gitStashDrop(repoPath: string, index: number): Promise<void> {
  const fn = window?.go?.main?.App?.GitStashDrop as
    | ((rp: string, idx: number) => Promise<void>)
    | undefined
  if (!fn) throw new Error('GitStashDrop binding not available')
  return fn(repoPath, index)
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export function GitActionsPanel({
  repoPath,
  gitInfo,
}: GitActionsPanelProps): React.ReactElement {
  // Stage All
  const [stageState, setStageState] = useState<ActionState>('idle')
  const [stageError, setStageError] = useState<string | null>(null)

  // Commit
  const [commitMsg, setCommitMsg] = useState('')
  const [commitState, setCommitState] = useState<ActionState>('idle')
  const [commitError, setCommitError] = useState<string | null>(null)

  // Push
  const [pushState, setPushState] = useState<ActionState>('idle')
  const [pushError, setPushError] = useState<string | null>(null)

  // New Branch
  const [branchName, setBranchName] = useState('')
  const [branchState, setBranchState] = useState<ActionState>('idle')
  const [branchError, setBranchError] = useState<string | null>(null)

  // PR
  const [prState, setPrState] = useState<ActionState>('idle')
  const [prError, setPrError] = useState<string | null>(null)

  // Checkpoints (stash)
  const [checkpointName, setCheckpointName] = useState('')
  const [checkpoints, setCheckpoints] = useState<StashEntry[]>([])
  const [saveCheckpointState, setSaveCheckpointState] = useState<ActionState>('idle')
  const [saveCheckpointError, setSaveCheckpointError] = useState<string | null>(null)
  const [restoreIdx, setRestoreIdx] = useState<number | null>(null)
  const [deleteIdx, setDeleteIdx] = useState<number | null>(null)

  // -------------------------------------------------------------------------
  // Handlers
  // -------------------------------------------------------------------------

  const handleStageAll = useCallback(async () => {
    setStageState('loading')
    setStageError(null)
    try {
      await GitStageAll(repoPath)
      setStageState('success')
      setTimeout(() => setStageState('idle'), 2000)
    } catch (err) {
      setStageState('error')
      setStageError(err instanceof Error ? err.message : 'Stage failed')
    }
  }, [repoPath])

  const handleCommit = useCallback(async () => {
    if (commitMsg.trim() === '') return
    setCommitState('loading')
    setCommitError(null)
    try {
      await GitCommit(repoPath, commitMsg.trim())
      setCommitState('success')
      setCommitMsg('')
      setTimeout(() => setCommitState('idle'), 2000)
    } catch (err) {
      setCommitState('error')
      setCommitError(err instanceof Error ? err.message : 'Commit failed')
    }
  }, [repoPath, commitMsg])

  const handlePush = useCallback(async () => {
    setPushState('loading')
    setPushError(null)
    try {
      await GitPush(repoPath)
      setPushState('success')
      setTimeout(() => setPushState('idle'), 2000)
    } catch (err) {
      setPushState('error')
      setPushError(err instanceof Error ? err.message : 'Push failed')
    }
  }, [repoPath])

  const handleCreateBranch = useCallback(async () => {
    if (!isValidBranchName(branchName)) return
    setBranchState('loading')
    setBranchError(null)
    try {
      await GitCreateBranch(repoPath, branchName.trim())
      setBranchState('success')
      setBranchName('')
      setTimeout(() => setBranchState('idle'), 2000)
    } catch (err) {
      setBranchState('error')
      setBranchError(err instanceof Error ? err.message : 'Branch creation failed')
    }
  }, [repoPath, branchName])

  const handleCreatePR = useCallback(async () => {
    setPrState('loading')
    setPrError(null)
    try {
      await OpenPRInBrowser(repoPath)
      setPrState('success')
      setTimeout(() => setPrState('idle'), 2000)
    } catch (err) {
      setPrState('error')
      setPrError(err instanceof Error ? err.message : 'Failed to open PR')
    }
  }, [repoPath])

  // -- Checkpoint handlers --------------------------------------------------

  const fetchCheckpoints = useCallback(async () => {
    try {
      const list = await gitStashList(repoPath)
      setCheckpoints(list)
    } catch {
      setCheckpoints([])
    }
  }, [repoPath])

  // Fetch checkpoints on mount and when repoPath changes
  useEffect(() => {
    void fetchCheckpoints()
  }, [fetchCheckpoints])

  const handleSaveCheckpoint = useCallback(async () => {
    const name = checkpointName.trim()
    if (name === '') return
    setSaveCheckpointState('loading')
    setSaveCheckpointError(null)
    try {
      await gitStash(repoPath, name)
      setSaveCheckpointState('success')
      setCheckpointName('')
      setTimeout(() => setSaveCheckpointState('idle'), 2000)
      await fetchCheckpoints()
    } catch (err) {
      setSaveCheckpointState('error')
      setSaveCheckpointError(err instanceof Error ? err.message : 'Save failed')
    }
  }, [repoPath, checkpointName, fetchCheckpoints])

  const handleRestoreCheckpoint = useCallback(async (index: number) => {
    setRestoreIdx(index)
    try {
      await gitStashApply(repoPath, index)
    } catch {
      // Silently handle -- user sees restored state in git diff
    } finally {
      setRestoreIdx(null)
    }
  }, [repoPath])

  const handleDeleteCheckpoint = useCallback(async (index: number) => {
    if (!window.confirm('Delete this checkpoint? This cannot be undone.')) return
    setDeleteIdx(index)
    try {
      await gitStashDrop(repoPath, index)
      await fetchCheckpoints()
    } catch {
      // Silently handle
    } finally {
      setDeleteIdx(null)
    }
  }, [repoPath, fetchCheckpoints])

  // -------------------------------------------------------------------------
  // Render helpers
  // -------------------------------------------------------------------------

  function renderButtonContent(
    state: ActionState,
    label: string,
    icon?: React.ReactNode,
  ): React.ReactNode {
    if (state === 'loading') return <SpinnerIcon />
    if (state === 'success') return <CheckIcon />
    return (
      <span className="flex items-center gap-1.5">
        {icon}
        {label}
      </span>
    )
  }

  const hasUnpushed = gitInfo?.hasUnpushed ?? false
  const branchValid = branchName.length > 0 && isValidBranchName(branchName)

  // -------------------------------------------------------------------------
  // Render
  // -------------------------------------------------------------------------

  return (
    <div className="bg-surface border border-border rounded-xl p-4 space-y-4">
      {/* Action buttons row */}
      <div className="flex items-start gap-3 flex-wrap">
        {/* Stage All */}
        <div className="flex flex-col gap-1">
          <button
            type="button"
            onClick={() => void handleStageAll()}
            disabled={stageState === 'loading'}
            className="inline-flex items-center justify-center gap-1.5 px-3 py-1.5 rounded-lg
                       text-xs font-medium transition-colors
                       bg-border-m text-primary hover:bg-border
                       border border-border hover:border-muted
                       disabled:opacity-50 disabled:cursor-not-allowed
                       focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500"
          >
            {renderButtonContent(stageState, 'Stage All')}
          </button>
          {stageError !== null && (
            <span className="text-[10px] text-red-400 max-w-[120px] truncate">{stageError}</span>
          )}
        </div>

        {/* Commit */}
        <div className="flex flex-col gap-1 flex-1 min-w-[200px]">
          <div className="flex items-center gap-1.5">
            <input
              type="text"
              value={commitMsg}
              onChange={(e) => setCommitMsg(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && commitMsg.trim() !== '') {
                  void handleCommit()
                }
              }}
              placeholder="Commit message..."
              className="flex-1 px-2.5 py-1.5 rounded-lg text-xs font-mono
                         bg-app text-primary placeholder-muted
                         border border-border focus:border-blue-500
                         focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
            <button
              type="button"
              onClick={() => void handleCommit()}
              disabled={commitMsg.trim() === '' || commitState === 'loading'}
              className="inline-flex items-center justify-center gap-1.5 px-3 py-1.5 rounded-lg
                         text-xs font-medium transition-colors
                         bg-green-600/80 text-white hover:bg-green-600
                         disabled:opacity-40 disabled:cursor-not-allowed
                         focus:outline-none focus-visible:ring-2 focus-visible:ring-green-500"
            >
              {renderButtonContent(commitState, 'Commit')}
            </button>
          </div>
          {commitError !== null && (
            <span className="text-[10px] text-red-400 truncate">{commitError}</span>
          )}
        </div>

        {/* Push */}
        <div className="flex flex-col gap-1">
          <button
            type="button"
            onClick={() => void handlePush()}
            disabled={pushState === 'loading'}
            className="inline-flex items-center justify-center gap-1.5 px-3 py-1.5 rounded-lg
                       text-xs font-medium transition-colors
                       bg-border-m text-primary hover:bg-border
                       border border-border hover:border-muted
                       disabled:opacity-50 disabled:cursor-not-allowed
                       focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500"
          >
            {renderButtonContent(
              pushState,
              'Push',
              hasUnpushed ? <WarningIcon /> : undefined,
            )}
          </button>
          {pushError !== null && (
            <span className="text-[10px] text-red-400 max-w-[120px] truncate">{pushError}</span>
          )}
        </div>

        {/* New Branch */}
        <div className="flex flex-col gap-1">
          <div className="flex items-center gap-1.5">
            <input
              type="text"
              value={branchName}
              onChange={(e) => setBranchName(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && branchValid) {
                  void handleCreateBranch()
                }
              }}
              placeholder="branch-name"
              className="w-32 px-2.5 py-1.5 rounded-lg text-xs font-mono
                         bg-app text-primary placeholder-muted
                         border border-border focus:border-blue-500
                         focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
            <button
              type="button"
              onClick={() => void handleCreateBranch()}
              disabled={!branchValid || branchState === 'loading'}
              className="inline-flex items-center justify-center gap-1.5 px-3 py-1.5 rounded-lg
                         text-xs font-medium transition-colors
                         bg-border-m text-primary hover:bg-border
                         border border-border hover:border-muted
                         disabled:opacity-40 disabled:cursor-not-allowed
                         focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500"
            >
              {renderButtonContent(branchState, 'Branch')}
            </button>
          </div>
          {branchName.length > 0 && !isValidBranchName(branchName) && (
            <span className="text-[10px] text-amber-400">Invalid branch name</span>
          )}
          {branchError !== null && (
            <span className="text-[10px] text-red-400 max-w-[180px] truncate">{branchError}</span>
          )}
        </div>

        {/* Create PR */}
        <div className="flex flex-col gap-1">
          <button
            type="button"
            onClick={() => void handleCreatePR()}
            disabled={prState === 'loading'}
            className="inline-flex items-center justify-center gap-1.5 px-3 py-1.5 rounded-lg
                       text-xs font-medium transition-colors
                       bg-blue-600/80 text-white hover:bg-blue-600
                       disabled:opacity-50 disabled:cursor-not-allowed
                       focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500"
          >
            {renderButtonContent(prState, 'Create PR')}
          </button>
          {prError !== null && (
            <span className="text-[10px] text-red-400 max-w-[120px] truncate">{prError}</span>
          )}
        </div>
      </div>

      {/* ---- Checkpoints section ---- */}
      <div className="border-t border-border pt-3 space-y-2">
        <h3 className="text-xs font-semibold text-secondary tracking-wide uppercase">
          Checkpoints
        </h3>

        {/* Save checkpoint: name input + save button */}
        <div className="flex items-center gap-1.5">
          <input
            type="text"
            value={checkpointName}
            onChange={(e) => setCheckpointName(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && checkpointName.trim() !== '') {
                void handleSaveCheckpoint()
              }
            }}
            placeholder="Checkpoint name..."
            className="flex-1 px-2.5 py-1.5 rounded-lg text-xs font-mono
                       bg-app text-primary placeholder-muted
                       border border-border focus:border-blue-500
                       focus:outline-none focus:ring-1 focus:ring-blue-500"
          />
          <button
            type="button"
            onClick={() => void handleSaveCheckpoint()}
            disabled={checkpointName.trim() === '' || saveCheckpointState === 'loading'}
            className="inline-flex items-center justify-center gap-1.5 px-3 py-1.5 rounded-lg
                       text-xs font-medium transition-colors
                       bg-cyan-600/80 text-white hover:bg-cyan-600
                       disabled:opacity-40 disabled:cursor-not-allowed
                       focus:outline-none focus-visible:ring-2 focus-visible:ring-cyan-500"
          >
            {renderButtonContent(saveCheckpointState, 'Save')}
          </button>
        </div>
        {saveCheckpointError !== null && (
          <span className="text-[10px] text-red-400 truncate block">{saveCheckpointError}</span>
        )}

        {/* Checkpoint list */}
        {checkpoints.length === 0 ? (
          <p className="text-xs text-muted text-center py-2">No checkpoints saved</p>
        ) : (
          <ul className="space-y-1">
            {checkpoints.map((entry) => (
              <li
                key={entry.index}
                className="flex items-center gap-2 px-2 py-1.5 rounded-lg
                           bg-app/50 border border-border"
              >
                {/* Name */}
                <span className="text-xs text-primary truncate flex-1" title={entry.name}>
                  {entry.name}
                </span>

                {/* Relative time */}
                {entry.date && (
                  <span className="text-[10px] text-muted flex-shrink-0 tabular-nums">
                    {relativeTime(entry.date)}
                  </span>
                )}

                {/* Restore */}
                <button
                  type="button"
                  onClick={() => void handleRestoreCheckpoint(entry.index)}
                  disabled={restoreIdx === entry.index}
                  className="inline-flex items-center justify-center px-2 py-0.5 rounded text-[10px]
                             font-medium transition-colors
                             bg-cyan-600/20 text-cyan-400 hover:bg-cyan-600/40
                             disabled:opacity-50 disabled:cursor-not-allowed
                             focus:outline-none focus-visible:ring-1 focus-visible:ring-cyan-500"
                >
                  {restoreIdx === entry.index ? <SpinnerIcon className="w-3 h-3" /> : 'Restore'}
                </button>

                {/* Delete */}
                <button
                  type="button"
                  onClick={() => void handleDeleteCheckpoint(entry.index)}
                  disabled={deleteIdx === entry.index}
                  className="inline-flex items-center justify-center px-2 py-0.5 rounded text-[10px]
                             font-medium transition-colors
                             text-red-400 hover:bg-red-600/20
                             disabled:opacity-50 disabled:cursor-not-allowed
                             focus:outline-none focus-visible:ring-1 focus-visible:ring-red-500"
                >
                  {deleteIdx === entry.index ? <SpinnerIcon className="w-3 h-3" /> : 'Delete'}
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  )
}
