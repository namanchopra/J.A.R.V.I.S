import { useState } from 'react'
import { model } from '../../wailsjs/go/models'
import {
  agentLabel,
  formatDuration,
  formatTimestamp,
  repoBasename,
  sessionStatusBadgeBg,
  sessionStatusBg,
} from '../lib/session-helpers'

// ---------------------------------------------------------------------------
// Props
// ---------------------------------------------------------------------------

interface SessionDetailProps {
  session: model.Session
  onStop: (id: string) => Promise<void>
  onResume: (id: string) => Promise<void>
  onDelete: (id: string) => Promise<void>
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export function SessionDetail({
  session,
  onStop,
  onResume,
  onDelete,
}: SessionDetailProps): React.ReactElement {
  return (
    <div className="flex-1 flex flex-col min-h-0">
      {/* Header */}
      <header className="flex-shrink-0 px-5 py-4 border-b border-border bg-surface">
        <div className="flex items-center justify-between">
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-3">
              <h1 className="text-base font-bold text-primary truncate">
                {repoBasename(session.repoPath)}
              </h1>
              <span
                className={`flex-shrink-0 inline-block px-2 py-0.5 text-xs font-medium rounded
                            ${sessionStatusBadgeBg(session.status)}`}
              >
                {session.status}
              </span>
            </div>
            <p className="text-xs text-muted mt-1 truncate">{session.repoPath}</p>
          </div>

          {/* Action buttons */}
          <div className="flex items-center gap-2 ml-4 flex-shrink-0">
            {session.status === 'running' && (
              <button
                type="button"
                onClick={() => void onStop(session.id)}
                className="px-3 py-1.5 text-xs font-medium rounded-md
                           bg-red-600/20 text-red-400 hover:bg-red-600/30
                           transition-colors focus:outline-none
                           focus-visible:ring-2 focus-visible:ring-red-500"
              >
                Stop
              </button>
            )}
            {session.status === 'stopped' && (
              <button
                type="button"
                onClick={() => void onResume(session.id)}
                className="px-3 py-1.5 text-xs font-medium rounded-md
                           bg-green-600/20 text-green-400 hover:bg-green-600/30
                           transition-colors focus:outline-none
                           focus-visible:ring-2 focus-visible:ring-green-500"
              >
                Resume
              </button>
            )}
            <button
              type="button"
              onClick={() => void onDelete(session.id)}
              className="px-3 py-1.5 text-xs font-medium rounded-md
                         bg-border-m text-secondary hover:text-red-400
                         hover:bg-red-600/10 transition-colors focus:outline-none
                         focus-visible:ring-2 focus-visible:ring-red-500"
            >
              Delete
            </button>
          </div>
        </div>
      </header>

      {/* Session info */}
      <div className="flex-1 flex flex-col min-h-0 overflow-y-auto">
        {/* Session info grid */}
        <div className="flex-shrink-0 px-5 py-4 border-b border-border-m">
          <div className="grid grid-cols-2 sm:grid-cols-4 gap-4">
            <InfoCell label="Agent" value={agentLabel(session.agentType)} />
            <InfoCell label="Status" value={session.status}>
              <span
                className={`inline-block w-2 h-2 rounded-full mr-1.5 ${sessionStatusBg(session.status)}`}
              />
            </InfoCell>
            <InfoCell label="Started" value={formatTimestamp(session.createdAt)} />
            <InfoCell
              label="Duration"
              value={formatDuration(session.createdAt, session.updatedAt)}
            />
          </div>

          {session.pid > 0 && (
            <div className="mt-3">
              <InfoCell label="PID" value={String(session.pid)} />
            </div>
          )}

          {session.errorMessage !== '' && (
            <div className="mt-3 text-sm text-acc-red bg-red-400/10 rounded-md px-3 py-2">
              {session.errorMessage}
            </div>
          )}
        </div>

        {/* Prompt section */}
        <div className="flex-shrink-0 px-5 py-4">
          <h3 className="text-xs font-semibold uppercase tracking-wider text-secondary mb-2">
            Prompt
          </h3>
          <div className="bg-app rounded-md border border-border px-3 py-2 max-h-40 overflow-y-auto">
            <p className="text-sm text-primary whitespace-pre-wrap">{session.prompt}</p>
          </div>
        </div>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// InfoCell
// ---------------------------------------------------------------------------

function InfoCell({
  label,
  value,
  children,
}: {
  label: string
  value: string
  children?: React.ReactNode
}): React.ReactElement {
  return (
    <div>
      <p className="text-[10px] font-semibold uppercase tracking-wider text-muted mb-0.5">
        {label}
      </p>
      <p className="text-sm text-primary flex items-center">
        {children}
        {value}
      </p>
    </div>
  )
}
