import { model } from '../../wailsjs/go/models'
import {
  agentLabel,
  formatDuration,
  repoBasename,
  sessionStatusBg,
  truncate,
} from '../lib/session-helpers'

// ---------------------------------------------------------------------------
// Props
// ---------------------------------------------------------------------------

interface SessionRowProps {
  session: model.Session
  isSelected: boolean
  onSelect: (id: string) => void
  onStop: (id: string) => Promise<void>
  onResume: (id: string) => Promise<void>
  onDelete: (id: string) => Promise<void>
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export function SessionRow({
  session,
  isSelected,
  onSelect,
  onStop,
  onResume,
  onDelete,
}: SessionRowProps): React.ReactElement {
  const dotClass = sessionStatusBg(session.status)
  const isRunning = session.status === 'running'
  const showChatBubble = session.status === 'running' || session.status === 'needs-input'

  return (
    <button
      type="button"
      onClick={() => onSelect(session.id)}
      className={`w-full text-left px-4 py-3 border-b border-border-m
                  transition-all duration-150 focus:outline-none
                  focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-blue-500
                  ${isSelected ? 'bg-elevated' : 'hover:bg-surface'}`}
    >
      <div className="flex items-start gap-2.5">
        {/* Status dot with glow ring for running */}
        <span className="mt-1.5 flex-shrink-0 relative">
          {isRunning && (
            <span
              className="absolute inset-0 rounded-full bg-amber-400 animate-ping opacity-40"
              aria-hidden="true"
            />
          )}
          <span
            className={`relative block w-2.5 h-2.5 rounded-full ${dotClass}`}
            aria-label={`Status: ${session.status}`}
          />
        </span>

        <div className="min-w-0 flex-1">
          {/* Repo name + agent badge + chat bubble */}
          <div className="flex items-center gap-2">
            <p className="text-sm font-medium text-primary truncate">
              {repoBasename(session.repoPath)}
            </p>
            <span
              className="flex-shrink-0 inline-block px-1.5 py-0.5 text-[10px] font-medium
                         rounded bg-border-m text-secondary"
            >
              {agentLabel(session.agentType)}
            </span>
            {showChatBubble && (
              <span
                className="flex-shrink-0 text-acc-indigo"
                title="Chat available"
                aria-label="Chat available"
              >
                <ChatBubbleIcon />
              </span>
            )}
          </div>

          {/* Prompt preview */}
          <p className="text-xs text-secondary truncate mt-0.5">
            {truncate(session.prompt, 80)}
          </p>

          {/* Duration + actions */}
          <div className="flex items-center gap-2 mt-1.5">
            <span className="text-[10px] text-muted">
              {formatDuration(session.createdAt, session.updatedAt)}
            </span>

            {/* Action buttons */}
            <div className="flex items-center gap-1 ml-auto">
              {session.status === 'running' && (
                <ActionButton
                  label="Stop"
                  onClick={(e) => {
                    e.stopPropagation()
                    void onStop(session.id)
                  }}
                  className="text-acc-red hover:text-red-300"
                >
                  <StopIcon />
                </ActionButton>
              )}
              {session.status === 'stopped' && (
                <ActionButton
                  label="Resume"
                  onClick={(e) => {
                    e.stopPropagation()
                    void onResume(session.id)
                  }}
                  className="text-acc-green hover:text-green-300"
                >
                  <PlayIcon />
                </ActionButton>
              )}
              <ActionButton
                label="Delete"
                onClick={(e) => {
                  e.stopPropagation()
                  void onDelete(session.id)
                }}
                className="text-muted hover:text-acc-red"
              >
                <TrashIcon />
              </ActionButton>
            </div>
          </div>
        </div>
      </div>
    </button>
  )
}

// ---------------------------------------------------------------------------
// ActionButton
// ---------------------------------------------------------------------------

function ActionButton({
  label,
  onClick,
  className,
  children,
}: {
  label: string
  onClick: (e: React.MouseEvent<HTMLButtonElement>) => void
  className: string
  children: React.ReactNode
}): React.ReactElement {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-label={label}
      title={label}
      className={`p-1 rounded transition-colors focus:outline-none
                  focus-visible:ring-2 focus-visible:ring-blue-500 ${className}`}
    >
      {children}
    </button>
  )
}

// ---------------------------------------------------------------------------
// Icons
// ---------------------------------------------------------------------------

function ChatBubbleIcon(): React.ReactElement {
  return (
    <svg
      className="w-3.5 h-3.5"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d="M21 15a2 2 0 01-2 2H7l-4 4V5a2 2 0 012-2h14a2 2 0 012 2z" />
    </svg>
  )
}

function StopIcon(): React.ReactElement {
  return (
    <svg
      className="w-3.5 h-3.5"
      viewBox="0 0 24 24"
      fill="currentColor"
      aria-hidden="true"
    >
      <rect x="6" y="6" width="12" height="12" rx="1" />
    </svg>
  )
}

function PlayIcon(): React.ReactElement {
  return (
    <svg
      className="w-3.5 h-3.5"
      viewBox="0 0 24 24"
      fill="currentColor"
      aria-hidden="true"
    >
      <polygon points="6 3 20 12 6 21 6 3" />
    </svg>
  )
}

function TrashIcon(): React.ReactElement {
  return (
    <svg
      className="w-3.5 h-3.5"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <polyline points="3 6 5 6 21 6" />
      <path d="M19 6l-1 14a2 2 0 01-2 2H8a2 2 0 01-2-2L5 6" />
      <path d="M10 11v6" />
      <path d="M14 11v6" />
      <path d="M9 6V4a1 1 0 011-1h4a1 1 0 011 1v2" />
    </svg>
  )
}
