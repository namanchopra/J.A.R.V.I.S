// Package setup defines the runtime state model for J.A.R.V.I.S.'s first-launch
// setup flow (v0.2.0). Other tasks in the v0.2.0 plan consume this package:
//   - app_setup.go (TASK-006) imports SetupState for its Wails bindings
//   - StartJarvis (TASK-009) returns ErrSetupRequired from this package
//   - the bridge (TASK-007) updates SetupState as model_download events arrive
package setup

import "errors"

// SetupPhase enumerates the four sequential install phases shown to the user
// on the SetupScreen. Order matters — the SetupScreen renders them in this
// declaration order.
type SetupPhase string

const (
	PhasePython    SetupPhase = "python_install"
	PhaseVenv      SetupPhase = "venv_install"
	PhaseVibeVoice SetupPhase = "vibevoice_download"
	PhaseWhisper   SetupPhase = "whisper_download"
)

// SetupState is the snapshot returned by App.GetSetupState() and persisted in
// the sentinel file. Field names are JSON-tagged to match the TypeScript
// SetupStateEvent shape that the React HUD consumes (see docs/setup-events.md
// from TASK-003).
type SetupState struct {
	Complete       bool       `json:"complete"`
	Phase          SetupPhase `json:"phase,omitempty"`
	PhaseProgress  int        `json:"phaseProgress"`
	PhaseDoneCount int        `json:"phaseDoneCount"`
	SetupVersion   string     `json:"setupVersion"`
	LastError      string     `json:"lastError,omitempty"`
}

// SetupExpectedVersion is the version string this code base expects to see in
// the sentinel file. Bump in lockstep with major install-flow changes; older
// sentinels are treated as invalid and trigger a re-install.
const SetupExpectedVersion = "0.2.0"

// ErrSetupRequired is returned by StartJarvis when ~/.jarvis/.setup-version-0.2.0
// is absent or invalid. The caller (the Wails App) detects this and signals
// the React layer to mount <SetupScreen> instead of the orb HUD.
var ErrSetupRequired = errors.New("setup not complete: run setup-on-launch flow first")

// ErrDaemonLaunchFailed is returned by StartJarvis when the sentinel exists
// AND the bundled binaries are in place, but the daemon process still fails
// to exec (e.g. corrupt venv, unexpected permissions). The Wails App surfaces
// this distinct case via an amber banner — see TASK-012 acceptance criteria.
var ErrDaemonLaunchFailed = errors.New("daemon process failed to launch")
