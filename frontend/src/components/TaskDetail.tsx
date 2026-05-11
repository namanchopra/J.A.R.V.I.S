import { useCallback, useEffect, useRef, useState } from 'react'
import { GetTask, GetTaskGitInfo, UpdateTaskStatus } from '../../wailsjs/go/main/App'
import { ClipboardSetText } from '../../wailsjs/runtime/runtime'
import { git } from '../../wailsjs/go/models'
import { model } from '../../wailsjs/go/models'
import { type TaskStatus, statusBgClass, statusTextClass } from '../lib/colors'
import { isAutoDetected, cleanDescription } from '../lib/utils'
import { OutputViewer } from './OutputViewer'
import { DiffViewer } from './DiffViewer'
import { GitActionsPanel } from './GitActionsPanel'

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const GIT_REFRESH_MS = 15_000

const STATUS_OPTIONS: Array<{ label: string; value: TaskStatus }> = [
  { label: 'Pending', value: 'pending' },
  { label: 'Running', value: 'running' },
  { label: 'Done', value: 'done' },
  { label: 'Failed', value: 'failed' },
  { label: 'Needs Input', value: 'needs-input' },
]

const AGENT_LABELS: Record<string, string> = {
  'claude-code': 'Claude Code',
  kiro: 'Kiro',
  gemini: 'Gemini',
  codex: 'Codex',
  aider: 'Aider',
  other: 'Other',
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function agentLabel(agentType: string): string {
  return AGENT_LABELS[agentType] ?? agentType
}

/** Format a Go time.Time (ISO string) to a readable date string. */
function formatTimestamp(value: unknown): string {
  if (value === null || value === undefined || value === '') return '--'
  const date = new Date(String(value))
  if (isNaN(date.getTime())) return '--'
  return date.toLocaleString('en-US', {
    month: 'short',
    day: 'numeric',
    year: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
    hour12: true,
  })
}

// ---------------------------------------------------------------------------
// Icons
// ---------------------------------------------------------------------------

function BranchIcon(): React.ReactElement {
  return (
    <svg
      className="w-4 h-4"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <line x1="6" y1="3" x2="6" y2="15" />
      <circle cx="18" cy="6" r="3" />
      <circle cx="6" cy="18" r="3" />
      <path d="M18 9a9 9 0 0 1-9 9" />
    </svg>
  )
}

// ---------------------------------------------------------------------------
// Props
// ---------------------------------------------------------------------------

interface TaskDetailProps {
  task: model.Task
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export function TaskDetail({ task }: TaskDetailProps): React.ReactElement {
  const [currentTask, setCurrentTask] = useState<model.Task>(task)
  const [statusDropdownOpen, setStatusDropdownOpen] = useState(false)
  const [updatingStatus, setUpdatingStatus] = useState(false)
  const [copied, setCopied] = useState(false)
  const [gitInfo, setGitInfo] = useState<git.RepoInfo | null>(null)
  const [gitFilesExpanded, setGitFilesExpanded] = useState(false)
  const [diffVisible, setDiffVisible] = useState(false)

  const dropdownRef = useRef<HTMLDivElement>(null)
  const gitIntervalRef = useRef<ReturnType<typeof setInterval> | null>(null)
  const mountedRef = useRef(true)

  // -------------------------------------------------------------------------
  // Re-fetch task when task.id changes (to get latest status)
  // -------------------------------------------------------------------------

  useEffect(() => {
    let cancelled = false

    setCurrentTask(task)
    setStatusDropdownOpen(false)
    setGitInfo(null)
    setGitFilesExpanded(false)
    setDiffVisible(false)

    GetTask(task.id)
      .then((freshTask) => {
        if (!cancelled) {
          setCurrentTask(freshTask)
        }
      })
      .catch(() => {
        // If re-fetch fails, keep the passed-in task
      })

    return () => {
      cancelled = true
    }
  }, [task.id]) // eslint-disable-line react-hooks/exhaustive-deps

  // Also sync when the parent passes a new task object (e.g. from polling)
  useEffect(() => {
    setCurrentTask(task)
  }, [task])

  // -------------------------------------------------------------------------
  // Fetch git info for this task
  // -------------------------------------------------------------------------

  useEffect(() => {
    mountedRef.current = true

    async function fetchGit(): Promise<void> {
      try {
        const info = await GetTaskGitInfo(task.id)
        if (mountedRef.current) {
          setGitInfo(info)
        }
      } catch (err) {
        console.warn('Failed to fetch task git info:', err)
      }
    }

    void fetchGit()

    gitIntervalRef.current = setInterval(() => {
      void fetchGit()
    }, GIT_REFRESH_MS)

    return () => {
      mountedRef.current = false
      if (gitIntervalRef.current !== null) {
        clearInterval(gitIntervalRef.current)
      }
    }
  }, [task.id])

  // -------------------------------------------------------------------------
  // Close dropdown on outside click
  // -------------------------------------------------------------------------

  useEffect(() => {
    function handleClick(e: MouseEvent): void {
      if (
        dropdownRef.current !== null &&
        !dropdownRef.current.contains(e.target as Node)
      ) {
        setStatusDropdownOpen(false)
      }
    }

    if (statusDropdownOpen) {
      document.addEventListener('mousedown', handleClick)
    }
    return () => document.removeEventListener('mousedown', handleClick)
  }, [statusDropdownOpen])

  // Close dropdown on Escape
  useEffect(() => {
    function handleKeyDown(e: KeyboardEvent): void {
      if (e.key === 'Escape') {
        setStatusDropdownOpen(false)
      }
    }
    if (statusDropdownOpen) {
      window.addEventListener('keydown', handleKeyDown)
    }
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [statusDropdownOpen])

  // -------------------------------------------------------------------------
  // Status change handler
  // -------------------------------------------------------------------------

  const handleStatusChange = useCallback(
    async (newStatus: TaskStatus) => {
      setStatusDropdownOpen(false)

      if (newStatus === currentTask.status) return

      setUpdatingStatus(true)
      try {
        const updated = await UpdateTaskStatus(currentTask.id, newStatus)
        setCurrentTask(updated)
      } catch (err) {
        console.warn('Failed to update task status:', err)
      } finally {
        setUpdatingStatus(false)
      }
    },
    [currentTask.id, currentTask.status],
  )

  // -------------------------------------------------------------------------
  // Copy to clipboard
  // -------------------------------------------------------------------------

  const handleCopyRepoPath = useCallback(async () => {
    try {
      await ClipboardSetText(currentTask.repoPath)
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    } catch (err) {
      console.warn('Wails clipboard failed, trying navigator.clipboard:', err)
      try {
        await navigator.clipboard.writeText(currentTask.repoPath)
        setCopied(true)
        setTimeout(() => setCopied(false), 2000)
      } catch (navErr) {
        console.warn('Navigator clipboard also failed:', navErr)
      }
    }
  }, [currentTask.repoPath])

  // -------------------------------------------------------------------------
  // Derived values
  // -------------------------------------------------------------------------

  const status = currentTask.status as TaskStatus
  const bgClass = statusBgClass(status)
  const textClass = statusTextClass(status)
  const autoDetected = isAutoDetected(currentTask)
  const displayDescription = autoDetected
    ? cleanDescription(currentTask)
    : currentTask.description

  // -------------------------------------------------------------------------
  // Render
  // -------------------------------------------------------------------------

  return (
    <>
      {/* Header bar */}
      <header className="h-12 flex-shrink-0 flex items-center px-5 border-b border-border bg-elevated">
        <h1 className="text-base font-bold tracking-wide text-primary">Jarvis</h1>
      </header>

      {/* Task detail content */}
      <div className="flex-1 flex flex-col min-h-0 overflow-y-auto">
        {/* Auto-detected banner */}
        {autoDetected && (
          <div className="mx-5 mt-4 px-4 py-3 rounded-lg bg-blue-900/30 border border-blue-700/50 flex-shrink-0">
            <p className="text-sm text-blue-300">
              This task was auto-detected from a running{' '}
              <span className="font-medium text-blue-200">
                {agentLabel(currentTask.agentType)}
              </span>{' '}
              process
            </p>
            <p className="text-xs text-blue-400/70 mt-1">
              Working directory: {currentTask.repoPath}
            </p>
          </div>
        )}

        {/* Metadata section */}
        <div className="px-5 py-5 space-y-4 flex-shrink-0">
          {/* Name */}
          <h2 className="text-xl font-semibold text-primary">
            {currentTask.name}
          </h2>

          {/* Description */}
          {displayDescription !== '' && (
            <p className="text-sm text-secondary leading-relaxed">
              {displayDescription}
            </p>
          )}

          {/* Badges row: status + agent */}
          <div className="flex items-center gap-3 flex-wrap">
            {/* Status badge with dropdown */}
            <div ref={dropdownRef} className="relative">
              <button
                type="button"
                onClick={() => setStatusDropdownOpen((prev) => !prev)}
                disabled={updatingStatus}
                className={`inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md text-xs
                           font-medium transition-colors focus:outline-none
                           focus-visible:ring-2 focus-visible:ring-blue-500
                           bg-border-m hover:bg-border disabled:opacity-50
                           ${textClass}`}
                aria-haspopup="listbox"
                aria-expanded={statusDropdownOpen}
                aria-label={`Status: ${currentTask.status}. Click to change.`}
              >
                <span className={`w-2 h-2 rounded-full ${bgClass}`} />
                {currentTask.status}
                {/* Dropdown caret */}
                <svg
                  className="w-3 h-3 opacity-60"
                  viewBox="0 0 12 12"
                  fill="none"
                  aria-hidden="true"
                >
                  <path
                    d="M3 5l3 3 3-3"
                    stroke="currentColor"
                    strokeWidth="1.5"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                  />
                </svg>
              </button>

              {statusDropdownOpen && (
                <div
                  role="listbox"
                  aria-label="Select status"
                  className="absolute top-full left-0 mt-1 w-40 bg-elevated border
                             border-border rounded-lg shadow-xl z-20 py-1"
                >
                  {STATUS_OPTIONS.map((opt) => {
                    const isActive = opt.value === status
                    const optBg = statusBgClass(opt.value)
                    const optText = statusTextClass(opt.value)
                    return (
                      <button
                        key={opt.value}
                        type="button"
                        role="option"
                        aria-selected={isActive}
                        onClick={() => void handleStatusChange(opt.value)}
                        className={`w-full text-left px-3 py-1.5 text-xs flex items-center gap-2
                                   transition-colors hover:bg-border-m focus:outline-none
                                   focus-visible:bg-border-m
                                   ${isActive ? 'bg-border-m/50' : ''} ${optText}`}
                      >
                        <span className={`w-2 h-2 rounded-full ${optBg}`} />
                        {opt.label}
                      </button>
                    )
                  })}
                </div>
              )}
            </div>

            {/* Agent type badge */}
            <span className="inline-flex items-center px-2.5 py-1 rounded-md text-xs font-medium bg-border-m text-primary">
              {agentLabel(currentTask.agentType)}
            </span>
          </div>

          {/* Metadata grid */}
          <div className="grid grid-cols-1 gap-3 text-sm">
            {/* Repo path */}
            <div className="flex items-center gap-2">
              <span className="text-muted flex-shrink-0">Repo:</span>
              <code className="font-mono text-xs text-primary bg-elevated px-2 py-1 rounded truncate">
                {currentTask.repoPath}
              </code>
              <button
                type="button"
                onClick={() => void handleCopyRepoPath()}
                className="flex-shrink-0 text-muted hover:text-primary transition-colors
                           focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500
                           rounded p-0.5"
                aria-label="Copy repo path to clipboard"
              >
                {copied ? (
                  <svg
                    className="w-4 h-4 text-green-400"
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
                ) : (
                  <svg
                    className="w-4 h-4"
                    viewBox="0 0 20 20"
                    fill="currentColor"
                    aria-hidden="true"
                  >
                    <path d="M8 2a1 1 0 000 2h2a1 1 0 100-2H8z" />
                    <path
                      d="M3 5a2 2 0 012-2 3 3 0 003 3h2a3 3 0 003-3 2 2 0
                         012 2v6h-4.586l1.293-1.293a1 1 0
                         00-1.414-1.414l-3 3a1 1 0 000 1.414l3 3a1 1 0
                         001.414-1.414L10.414 13H15v3a2 2 0 01-2 2H5a2
                         2 0 01-2-2V5zM15 11h2a1 1 0 110 2h-2v-2z"
                    />
                  </svg>
                )}
              </button>
            </div>

            {/* Output path */}
            <div className="flex items-center gap-2">
              <span className="text-muted flex-shrink-0">Output:</span>
              {currentTask.outputPath !== '' ? (
                <code className="font-mono text-xs text-primary bg-elevated px-2 py-1 rounded truncate">
                  {currentTask.outputPath}
                </code>
              ) : (
                <span className="text-xs text-muted italic">
                  No output path set
                </span>
              )}
            </div>

            {/* Timestamps */}
            <div className="flex items-center gap-4 text-xs text-muted">
              <span>Created: {formatTimestamp(currentTask.createdAt)}</span>
              <span>Updated: {formatTimestamp(currentTask.updatedAt)}</span>
            </div>
          </div>
        </div>

        {/* Divider */}
        <div className="border-t border-border mx-5" />

        {/* Git section */}
        {gitInfo !== null && (
          <>
            <GitSection
              gitInfo={gitInfo}
              filesExpanded={gitFilesExpanded}
              onToggleFiles={() => setGitFilesExpanded((prev) => !prev)}
            />
            {/* Divider */}
            <div className="border-t border-border mx-5" />
          </>
        )}

        {/* Diff viewer (collapsible) */}
        <div className="px-5 py-3 flex-shrink-0 space-y-3">
          <button
            type="button"
            onClick={() => setDiffVisible((prev) => !prev)}
            className="inline-flex items-center gap-2 px-3 py-1.5 rounded-lg text-xs
                       font-medium transition-colors
                       bg-border-m hover:bg-border text-primary
                       focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500"
            aria-expanded={diffVisible}
          >
            <svg
              className={`w-3.5 h-3.5 text-secondary transition-transform ${diffVisible ? 'rotate-90' : ''}`}
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
              strokeLinecap="round"
              strokeLinejoin="round"
              aria-hidden="true"
            >
              <polyline points="9 18 15 12 9 6" />
            </svg>
            {diffVisible ? 'Hide Changes' : 'View Changes'}
          </button>

          {diffVisible && (
            <DiffViewer taskId={currentTask.id} repoPath={currentTask.repoPath} />
          )}
        </div>

        {/* Divider */}
        <div className="border-t border-border mx-5" />

        {/* Git actions panel */}
        <div className="px-5 py-3 flex-shrink-0">
          <h3 className="text-xs font-semibold uppercase tracking-wider text-secondary mb-3">
            Git Actions
          </h3>
          <GitActionsPanel repoPath={currentTask.repoPath} gitInfo={gitInfo} />
        </div>

        {/* Divider */}
        <div className="border-t border-border mx-5" />

        {/* Output section label */}
        <div className="px-5 pt-4 pb-2 flex-shrink-0">
          <h3 className="text-xs font-semibold uppercase tracking-wider text-secondary">
            Output
          </h3>
        </div>

        {/* Output viewer */}
        <OutputViewer
          taskId={currentTask.id}
          outputPath={currentTask.outputPath}
        />
      </div>
    </>
  )
}

// ---------------------------------------------------------------------------
// GitSection sub-component
// ---------------------------------------------------------------------------

interface GitSectionProps {
  gitInfo: git.RepoInfo
  filesExpanded: boolean
  onToggleFiles: () => void
}

function GitSection({ gitInfo, filesExpanded, onToggleFiles }: GitSectionProps): React.ReactElement {
  const changedFiles = gitInfo.changedFiles ?? []
  const showExpander = changedFiles.length > 5

  return (
    <div className="px-5 py-4 space-y-3 flex-shrink-0">
      <h3 className="text-xs font-semibold uppercase tracking-wider text-secondary">
        Git
      </h3>

      <div className="space-y-2">
        {/* Branch */}
        {gitInfo.branch !== '' && (
          <div className="flex items-center gap-2">
            <span className="text-muted"><BranchIcon /></span>
            <span className="text-sm font-medium text-primary">{gitInfo.branch}</span>
            {/* Working tree indicator */}
            {gitInfo.isClean ? (
              <span className="text-[10px] font-medium bg-green-500/15 text-green-400 rounded px-1.5 py-0.5">
                Clean
              </span>
            ) : (
              <span className="text-[10px] font-medium bg-amber-500/15 text-amber-400 rounded px-1.5 py-0.5">
                Modified
              </span>
            )}
            {/* Unpushed warning */}
            {gitInfo.hasUnpushed && (
              <span className="text-[10px] font-medium bg-red-500/15 text-red-400 rounded px-1.5 py-0.5 animate-pulse">
                Unpushed commits
              </span>
            )}
          </div>
        )}

        {/* Diff stats */}
        {gitInfo.filesChanged > 0 && (
          <div className="flex items-center gap-3 text-sm">
            <span className="text-secondary">
              {gitInfo.filesChanged} file{gitInfo.filesChanged !== 1 ? 's' : ''} changed
            </span>
            <span className="text-green-400 font-medium">+{gitInfo.insertions}</span>
            <span className="text-red-400 font-medium">-{gitInfo.deletions}</span>
          </div>
        )}

        {/* Changed files */}
        {changedFiles.length > 0 && (
          <div>
            <div className="space-y-0.5">
              {(showExpander && !filesExpanded ? changedFiles.slice(0, 5) : changedFiles).map(
                (file, idx) => (
                  <p
                    key={idx}
                    className="font-mono text-xs text-secondary truncate pl-2 border-l-2 border-border"
                  >
                    {file}
                  </p>
                ),
              )}
            </div>
            {showExpander && (
              <button
                type="button"
                onClick={onToggleFiles}
                className="mt-1 text-xs text-blue-400 hover:text-blue-300 transition-colors
                           focus:outline-none focus-visible:underline"
              >
                {filesExpanded
                  ? 'Show less'
                  : `Show all ${changedFiles.length} files`}
              </button>
            )}
          </div>
        )}

        {/* Last commit */}
        {gitInfo.lastCommitMsg !== '' && (
          <div className="flex items-center gap-2 text-sm">
            <span className="text-muted">Last commit:</span>
            <span className="text-primary truncate">{gitInfo.lastCommitMsg}</span>
            {gitInfo.lastCommitAge !== '' && (
              <span className="text-[10px] text-muted flex-shrink-0">
                {gitInfo.lastCommitAge}
              </span>
            )}
          </div>
        )}
      </div>
    </div>
  )
}
