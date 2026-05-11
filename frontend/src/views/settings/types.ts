// ---------------------------------------------------------------------------
// SettingsPanelProps — shared prop contract for the five SettingsView panels
// (Connections, Voice, Behavior, Diagnostics, Advanced).
//
// Each panel file (ConnectionsPanel.tsx, VoicePanel.tsx, BehaviorPanel.tsx,
// DiagnosticsPanel.tsx, AdvancedPanel.tsx) extends this base. SettingsView
// owns the underlying state and passes the same `cfg`/`setCfg`/`onSave`/
// `saving` quartet down to every panel; panel-specific extras (terminals,
// mobile token cluster, sync helpers, etc.) are layered on per-panel via
// dedicated interfaces.
//
// This is the contract Wave 3 (TASK-017 … TASK-023) targets — adding new
// fields means extending the per-panel interface, not this base.
// ---------------------------------------------------------------------------

import { config } from '../../../wailsjs/go/models'

export interface SettingsPanelProps {
  /** Current loaded Config object. SettingsView guarantees this is non-null
   *  before any panel renders (the parent gates on a loading state first). */
  cfg: config.Config
  /** Replace the entire Config object. Panels call this with a spread copy
   *  to mutate a single field, mirroring the existing per-field handlers. */
  setCfg: (next: config.Config) => void
  /** Invokes SaveConfig() against the Wails backend. Shared Save button at
   *  the bottom of SettingsView calls this; panels can also wire it to
   *  inline Save buttons (e.g. "Save and Restart"). */
  onSave: () => Promise<void>
  /** True while a Save is in flight. Used to disable Save buttons. */
  saving: boolean
  /** Active tab id — panels use this to drive the `hidden` attribute so
   *  controlled-input state survives tab switches without remounts. */
  activeTab: string
}
