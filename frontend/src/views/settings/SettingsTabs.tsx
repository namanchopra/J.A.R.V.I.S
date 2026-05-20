export type SettingsTabId =
  | 'connections'
  | 'voice'
  | 'permissions'
  | 'behavior'
  | 'diagnostics'
  | 'advanced'

export interface SettingsTabDef {
  id: SettingsTabId
  label: string
  glyph: string
}

export const SETTINGS_TABS: ReadonlyArray<SettingsTabDef> = [
  { id: 'connections', label: 'Connections', glyph: '⏚' },
  { id: 'voice',       label: 'Voice',       glyph: '◍' },
  { id: 'permissions', label: 'Permissions', glyph: '◈' },
  { id: 'behavior',    label: 'Behavior',    glyph: '⌬' },
  { id: 'diagnostics', label: 'Diagnostics', glyph: '⊜' },
  { id: 'advanced',    label: 'Advanced',    glyph: '✦' },
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
      className="flex items-stretch gap-2 px-6 pt-3 border-b border-[rgba(0,229,255,0.15)]"
      style={{
        background:
          'linear-gradient(180deg, rgba(2,18,16,0.9), rgba(2,12,10,0.6))',
      }}
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
            className="group relative flex items-center gap-2 px-4 pt-2 pb-2.5 focus:outline-none transition-all"
            style={{
              fontFamily: "'SF Mono', 'Menlo', monospace",
              fontSize: 11,
              letterSpacing: '0.18em',
              textTransform: 'uppercase',
              fontWeight: 700,
              color: isActive ? 'var(--accent-blue)' : 'rgba(207, 231, 255, 0.35)',
              textShadow: isActive ? '0 0 10px rgba(0,229,255,0.55)' : 'none',
              borderRadius: '2px 2px 0 0',
              background: isActive
                ? 'linear-gradient(180deg, rgba(0,229,255,0.12), rgba(0,229,255,0.02))'
                : 'transparent',
              borderTop: isActive
                ? '1px solid rgba(0, 229, 255, 0.45)'
                : '1px solid transparent',
              borderLeft: isActive
                ? '1px solid rgba(0, 229, 255, 0.25)'
                : '1px solid transparent',
              borderRight: isActive
                ? '1px solid rgba(0, 229, 255, 0.25)'
                : '1px solid transparent',
              marginBottom: '-1px',
              boxShadow: isActive
                ? '0 -6px 18px rgba(0, 229, 255, 0.12), inset 0 0 12px rgba(0,229,255,0.05)'
                : 'none',
            }}
            onMouseEnter={(e) => {
              if (!isActive) {
                e.currentTarget.style.color = 'rgba(0, 229, 255, 0.75)'
              }
            }}
            onMouseLeave={(e) => {
              if (!isActive) {
                e.currentTarget.style.color = 'rgba(207, 231, 255, 0.35)'
              }
            }}
          >
            {/* Active-state corner brackets */}
            {isActive && (
              <>
                <span
                  aria-hidden="true"
                  className="absolute top-0 left-0 w-2 h-2"
                  style={{
                    borderTop: '2px solid var(--accent-blue)',
                    borderLeft: '2px solid var(--accent-blue)',
                  }}
                />
                <span
                  aria-hidden="true"
                  className="absolute top-0 right-0 w-2 h-2"
                  style={{
                    borderTop: '2px solid var(--accent-blue)',
                    borderRight: '2px solid var(--accent-blue)',
                  }}
                />
              </>
            )}
            <span
              aria-hidden="true"
              style={{
                fontSize: 13,
                opacity: isActive ? 1 : 0.5,
                textShadow: isActive ? '0 0 8px rgba(0,229,255,0.6)' : 'none',
              }}
            >
              {tab.glyph}
            </span>
            <span>{tab.label}</span>
            {/* Active underline -- glowing */}
            {isActive && (
              <span
                aria-hidden="true"
                className="absolute bottom-[-1px] left-2 right-2"
                style={{
                  height: 2,
                  background:
                    'linear-gradient(90deg, transparent, var(--accent-blue) 20%, var(--accent-blue) 80%, transparent)',
                  boxShadow: '0 0 8px rgba(0, 229, 255, 0.6)',
                }}
              />
            )}
          </button>
        )
      })}
    </div>
  )
}
