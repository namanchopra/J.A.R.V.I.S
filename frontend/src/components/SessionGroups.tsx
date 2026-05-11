import { useCallback, useEffect, useRef, useState } from 'react'
import {
  CreateSessionGroup,
  ListSessionGroups,
  DeleteSessionGroup,
  AddToSessionGroup,
  RemoveFromSessionGroup,
  GetSessionGroupMembers,
  BroadcastCommand,
  GetSessionIndicators,
} from '../../wailsjs/go/main/App'
import { model } from '../../wailsjs/go/models'
import type { claude } from '../../wailsjs/go/models'

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const COLOR_PRESETS: readonly string[] = [
  '#3b82f6', // blue
  '#10b981', // green
  '#f59e0b', // amber
  '#ef4444', // red
  '#8b5cf6', // purple
  '#06b6d4', // cyan
]

// ---------------------------------------------------------------------------
// Color Picker
// ---------------------------------------------------------------------------

function ColorPicker({
  selected,
  onSelect,
}: {
  selected: string
  onSelect: (color: string) => void
}): React.ReactElement {
  return (
    <div className="flex items-center gap-1.5">
      {COLOR_PRESETS.map((color) => (
        <button
          key={color}
          type="button"
          onClick={() => onSelect(color)}
          className="w-5 h-5 rounded-full flex-shrink-0 transition-all"
          style={{
            backgroundColor: color,
            outline: selected === color ? `2px solid ${color}` : 'none',
            outlineOffset: '2px',
            opacity: selected === color ? 1 : 0.6,
          }}
          aria-label={`Select color ${color}`}
        />
      ))}
    </div>
  )
}

// ---------------------------------------------------------------------------
// Create Group Form
// ---------------------------------------------------------------------------

function CreateGroupForm({
  onCreated,
}: {
  onCreated: () => void
}): React.ReactElement {
  const [name, setName] = useState('')
  const [color, setColor] = useState(COLOR_PRESETS[0]!)
  const [creating, setCreating] = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)

  const handleCreate = useCallback(async (): Promise<void> => {
    const trimmed = name.trim()
    if (!trimmed || creating) return
    setCreating(true)
    try {
      await CreateSessionGroup(trimmed, '', color)
      setName('')
      setColor(COLOR_PRESETS[0]!)
      onCreated()
    } catch (err) {
      console.warn('Failed to create session group:', err)
    } finally {
      setCreating(false)
    }
  }, [name, color, creating, onCreated])

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent): void => {
      if (e.key === 'Enter') {
        e.preventDefault()
        void handleCreate()
      }
    },
    [handleCreate],
  )

  return (
    <div className="rounded-lg border border-border bg-surface p-4">
      <h3 className="text-xs font-semibold text-secondary uppercase tracking-wide mb-3">
        Create Group
      </h3>
      <div className="flex items-center gap-3">
        <input
          ref={inputRef}
          type="text"
          value={name}
          onChange={(e) => setName(e.target.value)}
          onKeyDown={handleKeyDown}
          placeholder="Group name..."
          className="flex-1 min-w-0 bg-app border border-border rounded-md px-3 py-1.5 text-sm text-primary placeholder:text-muted focus:outline-none focus:border-acc-blue transition-colors"
        />
        <ColorPicker selected={color} onSelect={setColor} />
        <button
          type="button"
          onClick={() => void handleCreate()}
          disabled={!name.trim() || creating}
          className="px-3 py-1.5 text-xs rounded-md bg-acc-green hover:bg-acc-green/80 text-white font-medium transition-colors disabled:opacity-40 disabled:cursor-not-allowed flex-shrink-0"
        >
          {creating ? 'Creating...' : 'Create'}
        </button>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Member Row
// ---------------------------------------------------------------------------

function MemberRow({
  member,
  groupId,
  onRemoved,
}: {
  member: model.GroupMember
  groupId: string
  onRemoved: () => void
}): React.ReactElement {
  const [removing, setRemoving] = useState(false)

  const handleRemove = useCallback(async (): Promise<void> => {
    if (removing) return
    setRemoving(true)
    try {
      await RemoveFromSessionGroup(groupId, member.repoPath)
      onRemoved()
    } catch (err) {
      console.warn('Failed to remove member from group:', err)
    } finally {
      setRemoving(false)
    }
  }, [groupId, member.repoPath, removing, onRemoved])

  return (
    <div className="flex items-center gap-2 group py-1">
      <span className="text-[11px] font-mono text-secondary truncate flex-1 min-w-0">
        {member.repoPath}
      </span>
      <button
        type="button"
        onClick={() => void handleRemove()}
        disabled={removing}
        className="text-[10px] text-acc-red hover:text-acc-red/80 opacity-0 group-hover:opacity-100 focus:opacity-100 focus-visible:opacity-100 transition-opacity flex-shrink-0 disabled:opacity-40"
      >
        {removing ? '...' : 'Remove'}
      </button>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Add Repo Input
// ---------------------------------------------------------------------------

function AddRepoInput({
  groupId,
  onAdded,
}: {
  groupId: string
  onAdded: () => void
}): React.ReactElement {
  const [path, setPath] = useState('')
  const [adding, setAdding] = useState(false)

  const handleAdd = useCallback(async (): Promise<void> => {
    const trimmed = path.trim()
    if (!trimmed || adding) return
    setAdding(true)
    try {
      await AddToSessionGroup(groupId, trimmed)
      setPath('')
      onAdded()
    } catch (err) {
      console.warn('Failed to add repo to group:', err)
    } finally {
      setAdding(false)
    }
  }, [groupId, path, adding, onAdded])

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent): void => {
      if (e.key === 'Enter') {
        e.preventDefault()
        void handleAdd()
      }
    },
    [handleAdd],
  )

  return (
    <div className="flex items-center gap-2 mt-2">
      <input
        type="text"
        value={path}
        onChange={(e) => setPath(e.target.value)}
        onKeyDown={handleKeyDown}
        placeholder="Add repo path..."
        className="flex-1 min-w-0 bg-app border border-border-m rounded px-2 py-1 text-[11px] font-mono text-primary placeholder:text-muted focus:outline-none focus:border-acc-blue transition-colors"
      />
      <button
        type="button"
        onClick={() => void handleAdd()}
        disabled={!path.trim() || adding}
        className="px-2 py-1 text-[10px] rounded bg-border-m hover:bg-border text-secondary hover:text-primary transition-colors disabled:opacity-40 disabled:cursor-not-allowed flex-shrink-0"
      >
        {adding ? '...' : 'Add'}
      </button>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Broadcast Panel
// ---------------------------------------------------------------------------

function BroadcastPanel({
  groupId,
  members,
  indicators,
  onClose,
}: {
  groupId: string
  members: model.GroupMember[]
  indicators: claude.SessionIndicator[]
  onClose: () => void
}): React.ReactElement {
  const [command, setCommand] = useState('')
  const [sending, setSending] = useState(false)
  const [results, setResults] = useState<Record<number, string> | null>(null)

  // Map member repo paths to active session PIDs
  const memberPids: number[] = members.reduce<number[]>((pids, m) => {
    const matching = indicators.filter(
      (ind) => ind.cwd === m.repoPath || ind.cwd.startsWith(m.repoPath + '/'),
    )
    for (const ind of matching) {
      if (!pids.includes(ind.pid)) {
        pids.push(ind.pid)
      }
    }
    return pids
  }, [])

  const handleBroadcast = useCallback(async (): Promise<void> => {
    const trimmed = command.trim()
    if (!trimmed || sending || memberPids.length === 0) return
    setSending(true)
    try {
      const res = await BroadcastCommand(memberPids, trimmed)
      setResults(res)
      setCommand('')
    } catch (err) {
      console.warn('Failed to broadcast command:', err)
    } finally {
      setSending(false)
    }
  }, [command, sending, memberPids])

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent): void => {
      if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault()
        void handleBroadcast()
      }
      if (e.key === 'Escape') {
        onClose()
      }
    },
    [handleBroadcast, onClose],
  )

  return (
    <div className="mt-3 rounded border border-border bg-app p-3">
      <div className="flex items-center justify-between mb-2">
        <span className="text-[10px] font-semibold text-secondary uppercase tracking-wide">
          Broadcast to {memberPids.length} active session{memberPids.length !== 1 ? 's' : ''}
        </span>
        <button
          type="button"
          onClick={onClose}
          className="text-[10px] text-muted hover:text-secondary transition-colors"
        >
          Close
        </button>
      </div>
      {memberPids.length === 0 ? (
        <p className="text-[11px] text-muted">
          No active sessions found for this group's repos.
        </p>
      ) : (
        <>
          <div className="flex items-center gap-2">
            <input
              type="text"
              value={command}
              onChange={(e) => setCommand(e.target.value)}
              onKeyDown={handleKeyDown}
              placeholder="Enter command to broadcast..."
              className="flex-1 min-w-0 bg-surface border border-border-m rounded px-2 py-1.5 text-[11px] font-mono text-primary placeholder:text-muted focus:outline-none focus:border-acc-blue transition-colors"
              autoFocus
            />
            <button
              type="button"
              onClick={() => void handleBroadcast()}
              disabled={!command.trim() || sending}
              className="px-3 py-1.5 text-[10px] rounded bg-acc-blue hover:bg-acc-blue/80 text-white font-medium transition-colors disabled:opacity-40 disabled:cursor-not-allowed flex-shrink-0"
            >
              {sending ? 'Sending...' : 'Send'}
            </button>
          </div>
          {results !== null && (
            <div className="mt-2 space-y-1">
              {Object.entries(results).map(([pid, result]) => (
                <div
                  key={pid}
                  className="text-[10px] font-mono text-muted truncate"
                >
                  <span className="text-secondary">PID {pid}:</span> {result || 'sent'}
                </div>
              ))}
            </div>
          )}
        </>
      )}
    </div>
  )
}

// ---------------------------------------------------------------------------
// Delete Confirmation
// ---------------------------------------------------------------------------

function DeleteConfirmation({
  groupName,
  onConfirm,
  onCancel,
}: {
  groupName: string
  onConfirm: () => void
  onCancel: () => void
}): React.ReactElement {
  return (
    <div className="mt-3 rounded border border-[#f8514926] bg-[#f851490d] p-3">
      <p className="text-[11px] text-acc-red mb-2">
        Delete group "{groupName}"? This cannot be undone.
      </p>
      <div className="flex items-center gap-2">
        <button
          type="button"
          onClick={onConfirm}
          className="px-3 py-1 text-[10px] rounded bg-acc-red hover:bg-acc-red/80 text-white font-medium transition-colors"
        >
          Delete
        </button>
        <button
          type="button"
          onClick={onCancel}
          className="px-3 py-1 text-[10px] rounded bg-border-m hover:bg-border text-secondary transition-colors"
        >
          Cancel
        </button>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Group Card
// ---------------------------------------------------------------------------

function GroupCard({
  group,
  indicators,
  onDeleted,
}: {
  group: model.SessionGroup
  indicators: claude.SessionIndicator[]
  onDeleted: () => void
}): React.ReactElement {
  const [expanded, setExpanded] = useState(false)
  const [members, setMembers] = useState<model.GroupMember[]>([])
  const [loadingMembers, setLoadingMembers] = useState(false)
  const [showBroadcast, setShowBroadcast] = useState(false)
  const [showDeleteConfirm, setShowDeleteConfirm] = useState(false)
  const [deleting, setDeleting] = useState(false)

  const fetchMembers = useCallback(async (): Promise<void> => {
    setLoadingMembers(true)
    try {
      const result = await GetSessionGroupMembers(group.id)
      setMembers(result ?? [])
    } catch (err) {
      console.warn('Failed to fetch group members:', err)
      setMembers([])
    } finally {
      setLoadingMembers(false)
    }
  }, [group.id])

  const handleToggleExpand = useCallback((): void => {
    const next = !expanded
    setExpanded(next)
    if (next) {
      void fetchMembers()
    } else {
      setShowBroadcast(false)
      setShowDeleteConfirm(false)
    }
  }, [expanded, fetchMembers])

  const handleDelete = useCallback(async (): Promise<void> => {
    if (deleting) return
    setDeleting(true)
    try {
      await DeleteSessionGroup(group.id)
      onDeleted()
    } catch (err) {
      console.warn('Failed to delete session group:', err)
    } finally {
      setDeleting(false)
    }
  }, [group.id, deleting, onDeleted])

  const handleMemberChanged = useCallback((): void => {
    void fetchMembers()
  }, [fetchMembers])

  const memberCount = members.length

  return (
    <div
      className="rounded-lg border border-border bg-surface overflow-hidden transition-colors"
      style={{ borderLeftColor: group.color, borderLeftWidth: '3px' }}
    >
      {/* Header */}
      <button
        type="button"
        onClick={handleToggleExpand}
        className="w-full flex items-center gap-3 p-4 text-left hover:bg-elevated transition-colors"
      >
        {/* Expand chevron */}
        <span
          className="text-muted text-xs flex-shrink-0 transition-transform"
          style={{ transform: expanded ? 'rotate(90deg)' : 'rotate(0deg)' }}
        >
          {'\u25B6'}
        </span>

        {/* Group info */}
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2">
            <h4 className="text-sm font-semibold text-primary truncate">
              {group.name}
            </h4>
            {expanded && (
              <span className="text-[10px] px-1.5 py-0.5 rounded-full bg-border-m text-secondary flex-shrink-0">
                {memberCount} member{memberCount !== 1 ? 's' : ''}
              </span>
            )}
          </div>
          {group.description && (
            <p className="text-[11px] text-muted mt-0.5 truncate">
              {group.description}
            </p>
          )}
        </div>

        {/* Color dot indicator */}
        <span
          className="w-2.5 h-2.5 rounded-full flex-shrink-0"
          style={{ backgroundColor: group.color }}
        />
      </button>

      {/* Expanded content */}
      {expanded && (
        <div className="px-4 pb-4 border-t border-border-m">
          {/* Action buttons */}
          <div className="flex items-center gap-2 mt-3 mb-2">
            <button
              type="button"
              onClick={() => setShowBroadcast((v) => !v)}
              className="px-2.5 py-1 text-[10px] rounded bg-acc-blue/15 hover:bg-acc-blue/25 text-acc-blue font-medium transition-colors"
            >
              {showBroadcast ? 'Hide Broadcast' : 'Broadcast'}
            </button>
            <button
              type="button"
              onClick={() => setShowDeleteConfirm(true)}
              disabled={deleting}
              className="px-2.5 py-1 text-[10px] rounded bg-[#f851490d] hover:bg-[#f8514926] text-acc-red font-medium transition-colors disabled:opacity-40 ml-auto"
            >
              {deleting ? 'Deleting...' : 'Delete Group'}
            </button>
          </div>

          {/* Broadcast panel */}
          {showBroadcast && (
            <BroadcastPanel
              groupId={group.id}
              members={members}
              indicators={indicators}
              onClose={() => setShowBroadcast(false)}
            />
          )}

          {/* Delete confirmation */}
          {showDeleteConfirm && (
            <DeleteConfirmation
              groupName={group.name}
              onConfirm={() => void handleDelete()}
              onCancel={() => setShowDeleteConfirm(false)}
            />
          )}

          {/* Members list */}
          <div className="mt-3">
            <h5 className="text-[10px] font-semibold text-muted uppercase tracking-wide mb-1">
              Members
            </h5>
            {loadingMembers ? (
              <p className="text-[11px] text-muted py-1">Loading...</p>
            ) : members.length === 0 ? (
              <p className="text-[11px] text-muted py-1">
                No repos in this group yet.
              </p>
            ) : (
              <div className="divide-y divide-border-m">
                {members.map((m) => (
                  <MemberRow
                    key={m.repoPath}
                    member={m}
                    groupId={group.id}
                    onRemoved={handleMemberChanged}
                  />
                ))}
              </div>
            )}

            {/* Add repo input */}
            <AddRepoInput groupId={group.id} onAdded={handleMemberChanged} />
          </div>
        </div>
      )}
    </div>
  )
}

// ---------------------------------------------------------------------------
// Session Groups (main export)
// ---------------------------------------------------------------------------

export function SessionGroups(): React.ReactElement {
  const [groups, setGroups] = useState<model.SessionGroup[]>([])
  const [indicators, setIndicators] = useState<claude.SessionIndicator[]>([])
  const [loading, setLoading] = useState(true)
  const mountedRef = useRef(true)

  const fetchGroups = useCallback(async (): Promise<void> => {
    try {
      const [groupsResult, indicatorsResult] = await Promise.all([
        ListSessionGroups(),
        GetSessionIndicators(),
      ])
      if (!mountedRef.current) return
      setGroups(groupsResult ?? [])
      setIndicators(indicatorsResult ?? [])
    } catch (err) {
      console.warn('Failed to fetch session groups:', err)
    } finally {
      if (mountedRef.current) {
        setLoading(false)
      }
    }
  }, [])

  useEffect(() => {
    mountedRef.current = true
    void fetchGroups()
    const id = setInterval(() => void fetchGroups(), 5000)
    return () => {
      mountedRef.current = false
      clearInterval(id)
    }
  }, [fetchGroups])

  const handleGroupChanged = useCallback((): void => {
    void fetchGroups()
  }, [fetchGroups])

  if (loading) {
    return (
      <section className="px-5 py-6">
        <h2 className="text-sm font-semibold text-primary mb-3">Session Groups</h2>
        <p className="text-xs text-muted">Loading groups...</p>
      </section>
    )
  }

  return (
    <section className="px-5 py-4">
      <h2 className="text-sm font-semibold text-primary mb-3">Session Groups</h2>

      {/* Create form */}
      <div className="mb-4">
        <CreateGroupForm onCreated={handleGroupChanged} />
      </div>

      {/* Groups list */}
      {groups.length === 0 ? (
        <div className="rounded-lg border border-border-m bg-surface p-6 text-center">
          <p className="text-xs text-muted">
            No groups yet. Create one to organize your sessions.
          </p>
        </div>
      ) : (
        <div className="space-y-3">
          {groups.map((group) => (
            <GroupCard
              key={group.id}
              group={group}
              indicators={indicators}
              onDeleted={handleGroupChanged}
            />
          ))}
        </div>
      )}
    </section>
  )
}
