import { useCallback, useEffect, useRef, useState } from 'react'
import { GetTaskDiff, GetRepoDiff, GitStageFiles } from '../../wailsjs/go/main/App'
import { git } from '../../wailsjs/go/models'

// ---------------------------------------------------------------------------
// Props
// ---------------------------------------------------------------------------

interface DiffViewerPropsWithData {
  diff: git.DiffResult
  taskId?: undefined
  repoPath?: string
}

interface DiffViewerPropsWithTaskId {
  diff?: undefined
  taskId: string
  repoPath?: string
}

interface DiffViewerPropsWithRepoPath {
  diff?: undefined
  taskId?: undefined
  repoPath: string
}

type DiffViewerProps =
  | DiffViewerPropsWithData
  | DiffViewerPropsWithTaskId
  | DiffViewerPropsWithRepoPath

// ---------------------------------------------------------------------------
// Status badge colors
// ---------------------------------------------------------------------------

const STATUS_BADGE: Record<string, { bg: string; text: string; label: string }> = {
  Added:    { bg: 'bg-green-500/15', text: 'text-green-400', label: 'Added' },
  Modified: { bg: 'bg-amber-500/15', text: 'text-amber-400', label: 'Modified' },
  Deleted:  { bg: 'bg-red-500/15',   text: 'text-red-400',   label: 'Deleted' },
  Renamed:  { bg: 'bg-blue-500/15',  text: 'text-blue-400',  label: 'Renamed' },
}

function getStatusBadge(status: string): { bg: string; text: string; label: string } {
  return STATUS_BADGE[status] ?? { bg: 'bg-border/15', text: 'text-secondary', label: status }
}

// ---------------------------------------------------------------------------
// Per-file stats helper
// ---------------------------------------------------------------------------

function computeFileStats(file: git.FileDiff): { additions: number; deletions: number } {
  let additions = 0
  let deletions = 0
  const hunks = file.hunks ?? []
  for (const hunk of hunks) {
    const lines = hunk.lines ?? []
    for (const line of lines) {
      if (line.type === 'add') additions++
      else if (line.type === 'delete') deletions++
    }
  }
  return { additions, deletions }
}

// ---------------------------------------------------------------------------
// Icons
// ---------------------------------------------------------------------------

function ChevronIcon({ expanded }: { expanded: boolean }): React.ReactElement {
  return (
    <svg
      className={`w-4 h-4 text-secondary transition-transform flex-shrink-0 ${expanded ? 'rotate-90' : ''}`}
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
  )
}

function SpinnerIcon(): React.ReactElement {
  return (
    <svg
      className="w-4 h-4 animate-spin text-secondary"
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

// ---------------------------------------------------------------------------
// FileDiffSection sub-component
// ---------------------------------------------------------------------------

interface FileDiffSectionProps {
  file: git.FileDiff
  repoPath: string | undefined
  isStaged: boolean
  onStage: (filePath: string) => void
  onDiscard: (filePath: string) => void
}

function FileDiffSection({
  file,
  repoPath,
  isStaged,
  onStage,
  onDiscard,
}: FileDiffSectionProps): React.ReactElement {
  const [expanded, setExpanded] = useState(false)
  const [staging, setStaging] = useState(false)
  const badge = getStatusBadge(file.status)
  const { additions, deletions } = computeFileStats(file)
  const hunks = file.hunks ?? []

  const displayPath =
    file.status === 'Renamed' && file.oldPath !== ''
      ? `${file.oldPath} -> ${file.path}`
      : file.path

  const canStageOrDiscard = repoPath !== undefined && repoPath !== ''

  function handleCheckboxChange(): void {
    if (isStaged || staging || !canStageOrDiscard) return
    setStaging(true)
    onStage(file.path)
    // staging state is cosmetic; the parent updates isStaged via callback
    setStaging(false)
  }

  function handleDiscard(e: React.MouseEvent): void {
    e.stopPropagation()
    if (!canStageOrDiscard) return
    onDiscard(file.path)
  }

  const outerBorder = isStaged
    ? 'border border-border rounded-lg overflow-hidden border-l-2 border-l-green-500'
    : 'border border-border rounded-lg overflow-hidden'

  return (
    <div className={outerBorder}>
      {/* File header */}
      <div
        className={`flex items-center gap-2 px-3 py-2 bg-surface transition-colors ${
          isStaged ? 'bg-green-500/5' : ''
        }`}
      >
        {/* Stage checkbox */}
        {canStageOrDiscard && (
          <input
            type="checkbox"
            checked={isStaged}
            disabled={isStaged || staging}
            onChange={handleCheckboxChange}
            onClick={(e) => e.stopPropagation()}
            className="h-3.5 w-3.5 rounded border-border text-green-500 focus:ring-green-500
                       focus:ring-offset-0 cursor-pointer disabled:opacity-50 disabled:cursor-default
                       flex-shrink-0 accent-green-500"
            title={isStaged ? 'File is staged' : 'Stage this file'}
            aria-label={isStaged ? `${file.path} is staged` : `Stage ${file.path}`}
          />
        )}

        {/* Expand/collapse toggle */}
        <button
          type="button"
          onClick={() => setExpanded((prev) => !prev)}
          className="flex items-center gap-2 flex-1 min-w-0 hover:bg-elevated
                     rounded px-1 py-0.5 transition-colors text-left focus:outline-none
                     focus-visible:ring-2 focus-visible:ring-blue-500"
          aria-expanded={expanded}
        >
          <ChevronIcon expanded={expanded} />
          <span className={`text-[10px] font-medium rounded px-1.5 py-0.5 ${badge.bg} ${badge.text}`}>
            {badge.label}
          </span>
          <span className="font-mono text-xs text-primary truncate flex-1">
            {displayPath}
          </span>
          {isStaged && (
            <span className="bg-green-500/20 text-green-400 text-xs px-1.5 rounded flex-shrink-0">
              Staged
            </span>
          )}
          {file.binary ? (
            <span className="text-[10px] text-muted flex-shrink-0">Binary</span>
          ) : (
            <span className="flex items-center gap-2 flex-shrink-0 text-xs">
              {additions > 0 && (
                <span className="text-green-400 font-medium">+{additions}</span>
              )}
              {deletions > 0 && (
                <span className="text-red-400 font-medium">-{deletions}</span>
              )}
            </span>
          )}
        </button>

        {/* Discard button */}
        {canStageOrDiscard && !isStaged && (
          <button
            type="button"
            onClick={handleDiscard}
            className="text-xs text-red-400 hover:text-red-300 font-medium flex-shrink-0
                       px-1.5 py-0.5 rounded hover:bg-red-500/10 transition-colors
                       focus:outline-none focus-visible:ring-2 focus-visible:ring-red-500"
            title={`Discard changes to ${file.path}`}
            aria-label={`Discard changes to ${file.path}`}
          >
            Discard
          </button>
        )}
      </div>

      {/* Hunks */}
      {expanded && (
        <div className="border-t border-border">
          {file.binary ? (
            <p className="px-3 py-4 text-xs text-secondary italic">
              Binary file not shown
            </p>
          ) : hunks.length === 0 ? (
            <p className="px-3 py-4 text-xs text-secondary italic">
              No changes to display
            </p>
          ) : (
            hunks.map((hunk, hunkIdx) => (
              <HunkSection key={hunkIdx} hunk={hunk} />
            ))
          )}
        </div>
      )}
    </div>
  )
}

// ---------------------------------------------------------------------------
// HunkSection sub-component
// ---------------------------------------------------------------------------

function HunkSection({ hunk }: { hunk: git.DiffHunk }): React.ReactElement {
  const lines = hunk.lines ?? []

  return (
    <div>
      {/* Hunk header */}
      <div className="px-3 py-1 bg-surface border-b border-border">
        <span className="font-mono text-xs text-blue-400/80">
          {hunk.header !== ''
            ? hunk.header
            : `@@ -${hunk.oldStart},${hunk.oldCount} +${hunk.newStart},${hunk.newCount} @@`}
        </span>
      </div>

      {/* Lines */}
      <div className="overflow-x-auto">
        <table className="w-full border-collapse">
          <tbody>
            {lines.map((line, lineIdx) => {
              const isAdd = line.type === 'add'
              const isDel = line.type === 'delete'

              const rowBg = isAdd
                ? 'bg-green-900/30'
                : isDel
                  ? 'bg-red-900/30'
                  : 'bg-transparent'

              const textColor = isAdd
                ? 'text-green-300'
                : isDel
                  ? 'text-red-300'
                  : 'text-secondary'

              const prefix = isAdd ? '+' : isDel ? '-' : ' '

              return (
                <tr key={lineIdx} className={rowBg}>
                  {/* Old line number */}
                  <td className="text-muted text-xs w-10 text-right select-none px-1 py-0 font-mono align-top border-r border-border/50">
                    {isDel || (!isAdd && !isDel) ? (line.oldNum > 0 ? line.oldNum : '') : ''}
                  </td>
                  {/* New line number */}
                  <td className="text-muted text-xs w-10 text-right select-none px-1 py-0 font-mono align-top border-r border-border/50">
                    {isAdd || (!isAdd && !isDel) ? (line.newNum > 0 ? line.newNum : '') : ''}
                  </td>
                  {/* Prefix */}
                  <td className={`w-4 text-center select-none font-mono text-xs ${textColor} py-0 px-0`}>
                    {prefix}
                  </td>
                  {/* Content */}
                  <td className={`font-mono text-xs ${textColor} py-0 pr-3 whitespace-pre`}>
                    {line.content}
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Main DiffViewer component
// ---------------------------------------------------------------------------

export function DiffViewer(props: DiffViewerProps): React.ReactElement {
  const [diffData, setDiffData] = useState<git.DiffResult | null>(
    props.diff ?? null,
  )
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const mountedRef = useRef(true)
  const [stagedFiles, setStagedFiles] = useState<Set<string>>(() => new Set())
  const fetchSeqRef = useRef(0)
  const lastResetSeqRef = useRef(0)

  // Resolve repoPath from all prop variants
  const resolvedRepoPath = props.repoPath

  const fetchDiff = useCallback(async () => {
    if (props.diff !== undefined) return

    setLoading(true)
    setError(null)

    try {
      let result: git.DiffResult
      if (props.taskId !== undefined) {
        result = await GetTaskDiff(props.taskId)
      } else {
        result = await GetRepoDiff(props.repoPath)
      }
      if (mountedRef.current) {
        fetchSeqRef.current += 1
        setDiffData(result)
      }
    } catch (err) {
      if (mountedRef.current) {
        setError(err instanceof Error ? err.message : 'Failed to load diff')
      }
    } finally {
      if (mountedRef.current) {
        setLoading(false)
      }
    }
  }, [props.diff, props.taskId, props.repoPath])

  useEffect(() => {
    mountedRef.current = true
    void fetchDiff()
    return () => {
      mountedRef.current = false
    }
  }, [fetchDiff])

  // Sync if diff prop changes externally
  useEffect(() => {
    if (props.diff !== undefined) {
      fetchSeqRef.current += 1
      setDiffData(props.diff)
    }
  }, [props.diff])

  // Reset staged files when diff is fetched fresh (not on local mutations).
  useEffect(() => {
    if (fetchSeqRef.current !== lastResetSeqRef.current) {
      setStagedFiles(new Set())
      lastResetSeqRef.current = fetchSeqRef.current
    }
  }, [diffData])

  // -------------------------------------------------------------------------
  // Staging & discard handlers
  // -------------------------------------------------------------------------

  const handleStageFile = useCallback(
    async (filePath: string) => {
      if (!resolvedRepoPath) return
      try {
        await GitStageFiles(resolvedRepoPath, [filePath])
        setStagedFiles((prev) => {
          const next = new Set(prev)
          next.add(filePath)
          return next
        })
      } catch (err) {
        console.error('Failed to stage file:', err)
      }
    },
    [resolvedRepoPath],
  )

  const handleDiscardFile = useCallback(
    async (filePath: string) => {
      if (!resolvedRepoPath) return
      const confirmed = window.confirm(
        `Discard changes to ${filePath}? This cannot be undone.`,
      )
      if (!confirmed) return
      try {
        // GitDiscardFile may not exist yet (Go binding being added separately).
        // Access via window.go to avoid import errors if the binding is absent.
        const goBindings = (window as unknown as Record<string, unknown>)?.go as
          | { main?: { App?: { GitDiscardFile?: (repoPath: string, filePath: string) => Promise<void> } } }
          | undefined
        const discardFn = goBindings?.main?.App?.GitDiscardFile
        if (discardFn) {
          await discardFn(resolvedRepoPath, filePath)
          // Remove discarded file from diff data locally
          if (mountedRef.current) {
            setDiffData((prev) => {
              if (!prev) return prev
              const remainingFiles = (prev.files ?? []).filter((f) => f.path !== filePath)
              const removedFile = (prev.files ?? []).find((f) => f.path === filePath)
              const removedStats = removedFile
                ? computeFileStats(removedFile)
                : { additions: 0, deletions: 0 }
              const updated = git.DiffResult.createFrom({
                files: remainingFiles,
                stats: {
                  filesChanged: remainingFiles.length,
                  insertions: prev.stats.insertions - removedStats.additions,
                  deletions: prev.stats.deletions - removedStats.deletions,
                },
              })
              return updated
            })
          }
        } else {
          console.warn('GitDiscardFile binding not available yet')
        }
      } catch (err) {
        console.error('Failed to discard file:', err)
      }
    },
    [resolvedRepoPath],
  )

  // -------------------------------------------------------------------------
  // Render
  // -------------------------------------------------------------------------

  if (loading) {
    return (
      <div className="flex items-center gap-2 px-4 py-6 text-secondary">
        <SpinnerIcon />
        <span className="text-xs">Loading diff...</span>
      </div>
    )
  }

  if (error !== null) {
    return (
      <div className="px-4 py-4 text-xs text-red-400">
        {error}
      </div>
    )
  }

  if (diffData === null) {
    return (
      <div className="px-4 py-4 text-xs text-secondary italic">
        No diff data available
      </div>
    )
  }

  const files = diffData.files ?? []
  const stats = diffData.stats

  if (files.length === 0) {
    return (
      <div className="px-4 py-4 text-xs text-secondary italic">
        No changes detected
      </div>
    )
  }

  return (
    <div className="space-y-2">
      {/* Stats header */}
      <div className="flex items-center gap-3 px-1 text-sm">
        <span className="text-secondary">
          {stats.filesChanged} file{stats.filesChanged !== 1 ? 's' : ''} changed
        </span>
        <span className="text-green-400 font-medium">+{stats.insertions}</span>
        <span className="text-red-400 font-medium">-{stats.deletions}</span>
      </div>

      {/* File list */}
      <div className="space-y-1">
        {files.map((file, idx) => (
          <FileDiffSection
            key={`${file.path}-${idx}`}
            file={file}
            repoPath={resolvedRepoPath}
            isStaged={stagedFiles.has(file.path)}
            onStage={handleStageFile}
            onDiscard={handleDiscardFile}
          />
        ))}
      </div>
    </div>
  )
}
