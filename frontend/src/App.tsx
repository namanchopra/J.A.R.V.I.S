import { useEffect, useState } from 'react'
import { SettingsView } from './views/SettingsView'
import { ErrorBoundary } from './components/ErrorBoundary'
import { JarvisHudView } from './components/JarvisHudView'
import { Onboarding } from './components/Onboarding'
import { IsFirstRun } from '../wailsjs/go/main/App'

function App(): React.ReactElement {
  const [firstRun, setFirstRun] = useState<boolean | null>(null)
  const [showSettings, setShowSettings] = useState(false)

  useEffect(() => {
    IsFirstRun()
      .then((v: boolean) => setFirstRun(Boolean(v)))
      .catch(() => setFirstRun(false))
  }, [])

  // Cmd/Ctrl + , -- conventional shortcut for opening preferences.
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

  if (firstRun === null) {
    return <div className="flex h-screen bg-app text-primary" />
  }
  if (firstRun) {
    return <Onboarding onComplete={() => setFirstRun(false)} />
  }

  return (
    <div className="flex h-screen bg-app text-primary">
      <main className="flex-1 flex flex-col min-w-0 min-h-0 relative">
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
