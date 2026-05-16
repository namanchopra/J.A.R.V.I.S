package setup

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// TestSetupStateJSONRoundTrip asserts that marshalling a fully-populated
// SetupState and unmarshalling it back yields an equal value. This guards
// against future refactors silently renaming a JSON tag that the React
// SetupScreen depends on.
func TestSetupStateJSONRoundTrip(t *testing.T) {
	original := SetupState{
		Complete:       false,
		Phase:          PhaseVibeVoice,
		PhaseProgress:  42,
		PhaseDoneCount: 2,
		SetupVersion:   SetupExpectedVersion,
		LastError:      "network timeout",
	}

	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got SetupState
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got != original {
		t.Errorf("round-trip mismatch\n  got:  %+v\n  want: %+v", got, original)
	}

	// Sanity-check that the JSON contains the expected wire keys (camelCase,
	// matching the TS SetupStateEvent shape).
	str := string(raw)
	for _, key := range []string{
		`"complete"`,
		`"phase"`,
		`"phaseProgress"`,
		`"phaseDoneCount"`,
		`"setupVersion"`,
		`"lastError"`,
	} {
		if !strings.Contains(str, key) {
			t.Errorf("JSON missing key %s: %s", key, str)
		}
	}
}

// TestSetupStateOmitEmpty asserts that when Phase is the zero value, the
// JSON output omits the "phase" and "lastError" keys. The React layer
// distinguishes "no current phase" from "empty-string phase" so the omit
// behaviour is part of the contract.
func TestSetupStateOmitEmpty(t *testing.T) {
	st := SetupState{
		Complete:     true,
		SetupVersion: SetupExpectedVersion,
	}

	raw, err := json.Marshal(st)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	str := string(raw)

	if strings.Contains(str, `"phase"`) {
		t.Errorf("expected zero Phase to be omitted; got: %s", str)
	}
	if strings.Contains(str, `"lastError"`) {
		t.Errorf("expected empty LastError to be omitted; got: %s", str)
	}
	// Non-omitempty fields should still be present even when at the zero value.
	for _, key := range []string{`"complete"`, `"phaseProgress"`, `"phaseDoneCount"`, `"setupVersion"`} {
		if !strings.Contains(str, key) {
			t.Errorf("expected key %s to be present even when zero; got: %s", key, str)
		}
	}
}

// TestSetupPhaseConstants pins the four SetupPhase string values. If a future
// refactor mistypes one of these, this test breaks loudly instead of letting
// the React SetupScreen render an "unknown phase" placeholder at runtime.
func TestSetupPhaseConstants(t *testing.T) {
	cases := map[SetupPhase]string{
		PhasePython:    "python_install",
		PhaseVenv:      "venv_install",
		PhaseVibeVoice: "vibevoice_download",
		PhaseWhisper:   "whisper_download",
	}
	for got, want := range cases {
		if string(got) != want {
			t.Errorf("phase constant = %q; want %q", string(got), want)
		}
	}
}

// TestSetupExpectedVersion pins the sentinel version string so a bump becomes
// an explicit code review event (the bump is intentional in lockstep with a
// major install-flow change — see SetupExpectedVersion docs).
func TestSetupExpectedVersion(t *testing.T) {
	if SetupExpectedVersion != "0.2.0" {
		t.Errorf("SetupExpectedVersion = %q; want %q", SetupExpectedVersion, "0.2.0")
	}
}

// TestSentinelErrorsAreDistinct asserts the two exported sentinel errors are
// not aliases of each other and have non-empty messages. Callers in TASK-009
// and TASK-012 distinguish them with errors.Is, so identity matters.
func TestSentinelErrorsAreDistinct(t *testing.T) {
	if ErrSetupRequired == nil || ErrDaemonLaunchFailed == nil {
		t.Fatal("sentinel errors must not be nil")
	}
	if errors.Is(ErrSetupRequired, ErrDaemonLaunchFailed) {
		t.Error("ErrSetupRequired must not match ErrDaemonLaunchFailed")
	}
	if errors.Is(ErrDaemonLaunchFailed, ErrSetupRequired) {
		t.Error("ErrDaemonLaunchFailed must not match ErrSetupRequired")
	}
	if ErrSetupRequired.Error() == "" || ErrDaemonLaunchFailed.Error() == "" {
		t.Error("sentinel errors must have non-empty messages")
	}
}
