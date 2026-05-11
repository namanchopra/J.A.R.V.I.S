// ---------------------------------------------------------------------------
// SettingsTabs — top-of-view tab bar for SettingsView (TASK-016).
//
// Pure presentational component. Renders a row of 5 buttons, highlights the
// active one with the same #00e5ff cyan accent used throughout the app, and
// fires onChange when the user clicks a tab. Tab content rendering and state
// retention is the parent's responsibility (parent renders all 5 panels and
// hides inactive ones with `display: none` — see SettingsView).
// ---------------------------------------------------------------------------

export type SettingsTabId =
  | 'connections'
  | 'voice'
  | 'behavior'
  | 'diagnostics'
  | 'advanced'

export interface SettingsTabDef {
  id: SettingsTabId
  label: string
}

export const SETTINGS_TABS: ReadonlyArray<SettingsTabDef> = [
  { id: 'connections', label: 'Connections' },
  { id: 'voice', label: 'Voice' },
  { id: 'behavior', label: 'Behavior' },
  { id: 'diagnostics', label: 'Diagnostics' },
  { id: 'advanced', label: 'Advanced' },
] as const

interface SettingsTabsProps {
  active: SettingsTabId
  onChange: (id: SettingsTabId) => void
}

export function SettingsTabs({ active, onChange }: SettingsTabsProps): React.ReactElement {
  return (
    <div
      role="tablist"
      aria-label="Settings sections"
      className="flex items-end gap-1 px-5 border-b border-[rgba(0,229,255,0.15)] bg-[#0a0e1a]"
    >
      {SETTINGS_TABS.map((tab) => {
        const isActive = tab.id === active
        return (
          <button
            key={tab.id}
            type="button"
            role="tab"
            aria-selected={isActive}
            aria-controls={`settings-tab-panel-${tab.id}`}
            id={`settings-tab-${tab.id}`}
            onClick={() => onChange(tab.id)}
            className={[
              'relative px-4 py-2 text-sm font-medium tracking-wide transition-colors',
              'focus:outline-none focus-visible:ring-2 focus-visible:ring-[#00e5ff]',
              isActive
                ? 'text-[#00e5ff]'
                : 'text-[#4a6278] hover:text-[#8ba4b8]',
            ].join(' ')}
            style={
              isActive
                ? {
                    borderBottom: '2px solid #00e5ff',
                    marginBottom: '-1px',
                    textShadow: '0 0 8px rgba(0, 229, 255, 0.4)',
                  }
                : { borderBottom: '2px solid transparent', marginBottom: '-1px' }
            }
          >
            {tab.label}
          </button>
        )
      })}
    </div>
  )
}
