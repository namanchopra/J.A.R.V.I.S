import { useCallback, useEffect, useState } from 'react'
import { AnimatePresence, motion } from 'framer-motion'
import { EventsOn } from '../wailsjs/runtime/runtime'
import { NavRail, type ViewId } from './components/NavRail'
import { SearchBar } from './components/SearchBar'
import { TasksView } from './views/TasksView'
import { SessionsView } from './views/SessionsView'
import { WorkflowsView } from './views/WorkflowsView'
import { SettingsView } from './views/SettingsView'
import { HistoryView } from './views/HistoryView'
import { SessionGroups } from './components/SessionGroups'
import { ErrorBoundary } from './components/ErrorBoundary'
import { JarvisView } from './components/JarvisView'
import { JarvisHudView } from './components/JarvisHudView'
import { Onboarding } from './components/Onboarding'
// TASK-024: IsFirstRun is a new Wails binding (app_onboarding.go) that may
// not be in the generated wailsjs/ declarations yet. ts-expect-error keeps
// the panel compiling until `wails generate module` runs.
import { IsFirstRun } from '../wailsjs/go/main/App'

// ---------------------------------------------------------------------------
// App
// ---------------------------------------------------------------------------

function App(): React.ReactElement {
  const [activeView, setActiveView] = useState<ViewId>('jarvis')
  const [pendingTaskId, setPendingTaskId] = useState<string | null>(null)
  const [pendingSessionId, setPendingSessionId] = useState<string | null>(null)
  // TASK-024: first-run modal gate. `null` while we're still waiting on the
  // backend; the HUD render is suppressed during that brief window so users
  // never see a flash of unconfigured UI.
  const [firstRun, setFirstRun] = useState<boolean | null>(null)
  useEffect(() => {
    IsFirstRun()
      .then((v: boolean) => setFirstRun(Boolean(v)))
      .catch(() => setFirstRun(false))
  }, [])

  // Navigate to tasks view by task ID (from search or activity)
  const handleSelectTaskById = useCallback((taskId: string) => {
    setPendingTaskId(taskId)
    setActiveView('tasks')
  }, [])

  // Clear pending task once the tasks view picks it up
  const handleTaskSelected = useCallback(() => {
    setPendingTaskId(null)
  }, [])

  // Navigate to sessions view
  const handleNavigateSessions = useCallback(() => {
    setActiveView('sessions')
  }, [])

  // Navigate to sessions view with a specific session selected
  const handleSelectSession = useCallback((id: string) => {
    setPendingSessionId(id)
    setActiveView('sessions')
  }, [])

  // Clear pending session once sessions view picks it up
  const handleSessionSelected = useCallback(() => {
    setPendingSessionId(null)
  }, [])

  // Global hotkey: Cmd+Shift+V (macOS) / Ctrl+Shift+V (others) navigates to Jarvis
  useEffect(() => {
    const handler = (e: KeyboardEvent): void => {
      if ((e.metaKey || e.ctrlKey) && e.shiftKey && e.key === 'v') {
        e.preventDefault()
        setActiveView('jarvis')
      }
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [])

  // Listen for Jarvis navigation commands from the Go backend.
  useEffect(() => {
    const cancel = EventsOn('jarvis', (event: any) => {
      if (event?.type === 'navigate' && event?.text) {
        const validViews: ViewId[] = ['jarvis', 'tasks', 'sessions', 'workflows', 'history', 'groups', 'settings']
        if (validViews.includes(event.text as ViewId)) {
          setActiveView(event.text as ViewId)
        }
      }
    })
    return () => { cancel() }
  }, [])

  // TASK-024: hold rendering until IsFirstRun resolves so we don't flash
  // the HUD behind a modal that's about to mount.
  if (firstRun === null) {
    return <div className="flex h-screen bg-app text-primary" />
  }
  if (firstRun) {
    return <Onboarding onComplete={() => setFirstRun(false)} />
  }

  return (
    <div className="flex h-screen bg-app text-primary">
      {/* Navigation rail */}
      <NavRail
        activeView={activeView}
        onNavigate={setActiveView}
      />

      {/* Active view */}
      <main className="flex-1 flex flex-col min-w-0 min-h-0 relative">
        {/* Global search bar -- positioned to not overlap header controls */}
        <div className="absolute top-1.5 right-52 z-40">
          <SearchBar onSelectTask={handleSelectTaskById} />
        </div>

        <ErrorBoundary>
          <AnimatePresence mode="wait">
            <motion.div
              key={activeView}
              initial={{ opacity: 0, y: 6 }}
              animate={{ opacity: 1, y: 0 }}
              exit={{ opacity: 0, y: -6 }}
              transition={{ duration: 0.15 }}
              className="flex-1 flex flex-col min-w-0 min-h-0"
            >
              <ErrorBoundary>
                {activeView === 'jarvis' && <JarvisHudView />}
                {activeView === 'tasks' && (
                  <TasksView
                    initialTaskId={pendingTaskId}
                    onTaskSelected={handleTaskSelected}
                  />
                )}
                {activeView === 'sessions' && (
                  <SessionsView
                    initialSessionId={pendingSessionId}
                    onSessionSelected={handleSessionSelected}
                  />
                )}
                {activeView === 'workflows' && <WorkflowsView />}
                {activeView === 'history' && <HistoryView />}
                {activeView === 'groups' && (
                  <div className="flex-1 flex flex-col min-h-0">
                    <header className="h-12 flex-shrink-0 flex items-center px-5 border-b border-border bg-surface">
                      <h1 className="text-base font-bold tracking-wide text-primary">Session Groups</h1>
                    </header>
                    <div className="flex-1 overflow-y-auto p-5">
                      <SessionGroups />
                    </div>
                  </div>
                )}
                {activeView === 'settings' && <SettingsView />}
              </ErrorBoundary>
            </motion.div>
          </AnimatePresence>
        </ErrorBoundary>

      </main>
    </div>
  )
}

export default App
