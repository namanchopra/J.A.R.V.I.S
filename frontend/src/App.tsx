import { useCallback, useEffect, useRef, useState } from 'react'
import { SettingsView } from './views/SettingsView'
import { ErrorBoundary } from './components/ErrorBoundary'
import { JarvisHudView } from './components/JarvisHudView'
import { Onboarding } from './components/Onboarding'
import { SetupScreen } from './components/setup/SetupScreen'
import { isSetupStateEvent } from './lib/use-setup-state'
import { IsFirstRun } from '../wailsjs/go/main/App'
import { EventsOn, BrowserOpenURL } from '../wailsjs/runtime/runtime'

// v0.2.0: IsSetupComplete is a Wails binding added by TASK-006. `wails
// generate module` hasn't run in this sandbox, so the TS declaration in
// wailsjs/go/main/App.d.ts doesn't include it yet. Resolve at call time
// via window.go.main.App, matching the pattern OpenDaemonLog uses for the
// same not-yet-regenerated case in JarvisHudView.tsx + DiagnosticsPanel.
interface SetupBindings {
  IsSetupComplete?: () => Promise<boolean>
  OpenDaemonLog?: () => Promise<void>
  // RunSetup kicks off install-daemon.sh. v0.2.0..v0.2.3 shipped this binding
  // but never wired up the trigger -- the SetupScreen would mount and just sit
  // forever because nothing actually started the install. v0.2.4 fires it
  // from the App.tsx setup gate the moment isSetupComplete resolves to false.
  RunSetup?: () => Promise<unknown>
}

function setupBindings(): SetupBindings | null {
  const w = window as unknown as {
    go?: { main?: { App?: SetupBindings } }
  }
  return w.go?.main?.App ?? null
}

// Amber banner shown when the daemon fails to launch AFTER setup is complete
// (covers the v0.2.0 founder-review failure mode "sentinel write succeeds but
// daemon then fails to launch"). The user gets a clear next action via the
// "View daemon log" link.
function DaemonFailedBanner({ onViewLog }: { onViewLog: () => void }): React.ReactElement {
  return (
    <div
      role="alert"
      className="relative flex items-center justify-between gap-3 px-4 py-2 flex-shrink-0"
      style={{
        zIndex: 95,
        background: 'rgba(255, 184, 0, 0.18)',
        borderBottom: '1px solid rgb(255, 184, 0)',
        color: 'rgb(255, 215, 80)',
      }}
    >
      <span
        className="text-sm"
        style={{
          fontFamily: "'SF Mono', 'Menlo', monospace",
          fontWeight: 500,
          letterSpacing: '0.02em',
        }}
      >
        ▸ Daemon failed to launch — view the daemon log for details.
      </span>
      <button
        type="button"
        onClick={onViewLog}
        className="text-xs px-3 py-1 rounded text-black"
        style={{
          background: 'rgb(255, 184, 0)',
          fontFamily: "'SF Mono', 'Menlo', monospace",
          fontWeight: 600,
          letterSpacing: '0.05em',
        }}
      >
        View daemon log
      </button>
    </div>
  )
}

function App(): React.ReactElement {
  // v0.2.0 setup gate. null while we're waiting on IsSetupComplete to resolve;
  // false means mount <SetupScreen> instead of the orb HUD; true means
  // proceed to the existing Onboarding + HUD flow.
  const [isSetupComplete, setIsSetupComplete] = useState<boolean | null>(null)
  const [daemonLaunchFailed, setDaemonLaunchFailed] = useState<boolean>(false)
  const [firstRun, setFirstRun] = useState<boolean | null>(null)
  const [showSettings, setShowSettings] = useState(false)

  // Guard against firing RunSetup more than once per session. RunSetup
  // itself is dedup'd by sync.Mutex on the Go side, but we still don't want
  // to spam it -- a React re-render that flips isSetupComplete null->false
  // a second time (shouldn't happen but defensively guarded) must not
  // re-fire.
  const setupRunFiredRef = useRef(false)

  // Initial setup check on mount. If the binding isn't generated yet (dev
  // mode pre-wails-build), default to true so we don't trap the maintainer
  // behind a SetupScreen they don't need.
  useEffect(() => {
    const bindings = setupBindings()
    if (typeof bindings?.IsSetupComplete !== 'function') {
      setIsSetupComplete(true)
      return
    }
    bindings
      .IsSetupComplete()
      .then((v: boolean) => setIsSetupComplete(Boolean(v)))
      .catch(() => setIsSetupComplete(true))
  }, [])

  // v0.2.4: kick off the install the moment isSetupComplete resolves to
  // false. v0.2.0..v0.2.3 shipped the SetupScreen and the RunSetup binding
  // but never wired this trigger, so users saw an empty SetupScreen forever
  // because install-daemon.sh was never spawned. The Go-side mutex makes
  // calling RunSetup a no-op if a previous attempt is already in flight, so
  // the ref guard here is belt-and-braces.
  useEffect(() => {
    if (isSetupComplete !== false) return
    if (setupRunFiredRef.current) return
    const bindings = setupBindings()
    if (typeof bindings?.RunSetup !== 'function') {
      // Binding not regenerated yet (dev mode). Setup will fall through to
      // manual `wails generate module` + relaunch path; no auto-recovery
      // possible here because the runtime literally cannot reach Go.
      console.warn('App: window.go.main.App.RunSetup not available; install will not auto-start')
      return
    }
    setupRunFiredRef.current = true
    bindings.RunSetup().catch((err: unknown) => {
      // Don't unset setupRunFiredRef on error -- the Go side already
      // surfaces the error via emitErrorEvent → setup_progress {state:error},
      // and the SetupScreen renders a retry button per phase. Re-firing
      // from here would spam Go without giving the user a chance to act.
      console.warn('App: RunSetup rejected', err)
    })
  }, [isSetupComplete])

  // Subscribe to 'setup' channel — when setup completes mid-session (user
  // clicks Apply Now or installs finish), flip the gate without page reload.
  useEffect(() => {
    const cancel = EventsOn('setup', (event: unknown) => {
      try {
        if (isSetupStateEvent(event) && event.complete) {
          setIsSetupComplete(true)
        }
      } catch (err) {
        // Edge case: malformed setup_state event payload — drop silently
        // so the gate doesn't crash. isSetupComplete stays at its current
        // value; the next valid event resyncs it.
        console.warn('App: rejected malformed setup_state event', event, err)
      }
    })
    return () => {
      cancel()
    }
  }, [])

  // First-run flag check runs AFTER setup is complete, so Onboarding never
  // mounts in front of an unfinished install.
  useEffect(() => {
    if (isSetupComplete !== true) return
    IsFirstRun()
      .then((v: boolean) => setFirstRun(Boolean(v)))
      .catch(() => setFirstRun(false))
  }, [isSetupComplete])

  // Cmd/Ctrl + , — open/close Settings overlay (existing behavior).
  useEffect(() => {
    const handler = (e: KeyboardEvent): void => {
      if ((e.metaKey || e.ctrlKey) && e.key === ',') {
        e.preventDefault()
        setShowSettings((v) => !v)
      } else if (e.key === 'Escape' && showSettings) {
        setShowSettings(false)
      }
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [showSettings])

  const handleViewDaemonLog = useCallback((): void => {
    const bindings = setupBindings()
    if (typeof bindings?.OpenDaemonLog === 'function') {
      void bindings.OpenDaemonLog()
      return
    }
    // Fallback when the binding isn't available — open the file path in the
    // user's default file viewer. Won't render the contents but at least
    // lets them know where to look.
    BrowserOpenURL('file:///' + '~/.jarvis/logs/daemon.log')
  }, [])

  // ------------------------------------------------------------------
  // Render
  // ------------------------------------------------------------------

  // Brief splash while we resolve IsSetupComplete.
  if (isSetupComplete === null) {
    return <div className="flex h-screen bg-app text-primary" />
  }

  // v0.2.0 setup hasn't run yet — block the HUD behind the install UI.
  if (isSetupComplete === false) {
    return <SetupScreen />
  }

  // Setup is complete; check first-run.
  if (firstRun === null) {
    return <div className="flex h-screen bg-app text-primary" />
  }
  if (firstRun) {
    return <Onboarding onComplete={() => setFirstRun(false)} />
  }

  return (
    <div className="flex h-screen bg-app text-primary">
      <main className="flex-1 flex flex-col min-w-0 min-h-0 relative">
        {daemonLaunchFailed && <DaemonFailedBanner onViewLog={handleViewDaemonLog} />}
        <ErrorBoundary>
          <JarvisHudView />
        </ErrorBoundary>

        {/* Floating gear -- only visible when settings is closed */}
        {!showSettings && (
          <button
            type="button"
            onClick={() => setShowSettings(true)}
            aria-label="Open settings"
            title="Settings (Cmd+,)"
            className="jarvis-iconbtn absolute top-3 right-3 z-[80]"
            style={{ backdropFilter: 'blur(6px)' }}
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <circle cx="12" cy="12" r="3" />
              <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 2 2 2 2 0 0 1-2 2h-.09a1.65 1.65 0 0 0-1.51 1z" />
            </svg>
          </button>
        )}

        {/* Settings overlay -- full screen flex column so its scroll works */}
        {showSettings && (
          <div className="absolute inset-0 z-[90] flex flex-col">
            <ErrorBoundary>
              <SettingsView onClose={() => setShowSettings(false)} />
            </ErrorBoundary>
          </div>
        )}
      </main>
    </div>
  )
}

export default App
