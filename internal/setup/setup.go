// Package setup defines the runtime state model for J.A.R.V.I.S.'s first-launch
// setup flow (v0.2.0). Other tasks in the v0.2.0 plan consume this package:
//   - app_setup.go (TASK-006) imports SetupState for its Wails bindings
//   - StartJarvis (TASK-009) returns ErrSetupRequired from this package
//   - the bridge (TASK-007) updates SetupState as model_download events arrive
package setup

import (
	"bufio"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/namanchopra/jarvis/internal/paths"
)

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

// ---------------------------------------------------------------------------
// Sentinel file (v0.2.0 setup-on-launch, TASK-008)
// ---------------------------------------------------------------------------
//
// The sentinel file at ~/.jarvis/.setup-version-<version> records that a
// successful setup run completed for a given Jarvis version against a given
// bundled requirements.txt. StartJarvis (TASK-009) consults IsSetupComplete on
// every launch; a false result kicks off the SetupScreen.
//
// The file is intentionally NOT JSON. It is a tiny line-oriented `key: value`
// document so that a curious user can `cat ~/.jarvis/.setup-version-0.2.0`
// and read it without any tooling. Unknown keys, blank lines, and `#` comments
// are ignored by the parser for forward-compatibility.

// SentinelData mirrors the line-oriented `key: value` file at
// paths.SetupSentinelPath(SetupExpectedVersion). Field names match the keys
// used on disk: `version`, `timestamp`, `requirements_sha256`, `python_pbs_tag`.
//
// Intentionally NOT JSON so the user can `cat` it for diagnostics.
type SentinelData struct {
	// Version is the Jarvis setup-flow version. Must equal SetupExpectedVersion
	// for ReadSentinel to consider the file valid; older versions trigger a
	// re-install.
	Version string

	// Timestamp is when setup completed. Serialized as RFC 3339 / ISO 8601 UTC.
	Timestamp time.Time

	// RequirementsSHA256 is the SHA-256 of the bundled requirements.txt at the
	// time of install. If the bundled file's hash changes (i.e. the user
	// upgraded the .app to a build with different Python deps), IsSetupComplete
	// returns false and the SetupScreen re-runs to install the new deps.
	RequirementsSHA256 string

	// PythonPBSTag is the python-build-standalone release tag (e.g. "20260510")
	// used to download the bundled CPython interpreter. Surfaced for diagnostics
	// only — version skew here does not currently invalidate the sentinel.
	PythonPBSTag string
}

// Canonical key names used by serializeSentinel and parseSentinel. Keep these
// stable: the user reads the on-disk file by hand for diagnostics.
const (
	sentinelKeyVersion         = "version"
	sentinelKeyTimestamp       = "timestamp"
	sentinelKeyRequirementsSHA = "requirements_sha256"
	sentinelKeyPythonPBSTag    = "python_pbs_tag"
)

// serializeSentinel renders SentinelData to the line-oriented `key: value`
// format. A leading comment block explains the file's role to humans who `cat`
// it. Single space after colon, RFC 3339 UTC timestamps.
func serializeSentinel(data SentinelData) string {
	var b strings.Builder
	b.WriteString("# Jarvis setup sentinel — written by setup.WriteSentinel.\n")
	b.WriteString("# Lines are `key: value`. Unknown keys, blank lines, and\n")
	b.WriteString("# `#` comments are ignored by the parser.\n")
	fmt.Fprintf(&b, "%s: %s\n", sentinelKeyVersion, data.Version)
	fmt.Fprintf(&b, "%s: %s\n", sentinelKeyTimestamp, data.Timestamp.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "%s: %s\n", sentinelKeyRequirementsSHA, data.RequirementsSHA256)
	fmt.Fprintf(&b, "%s: %s\n", sentinelKeyPythonPBSTag, data.PythonPBSTag)
	return b.String()
}

// parseSentinel walks the line-oriented sentinel format. Returns an error only
// when the file is so malformed that no useful data can be recovered (zero
// recognized keys). Unknown keys, comments, and blank lines are ignored so a
// future version can add fields without breaking older readers.
//
// The caller (ReadSentinel) handles higher-level validations (version match,
// sha match); parseSentinel only handles the byte-to-struct layer.
func parseSentinel(r io.Reader) (SentinelData, error) {
	var data SentinelData
	scanner := bufio.NewScanner(r)
	// Allow lines up to 1 MiB so a stray giant blob doesn't crash us silently.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	recognized := 0
	for scanner.Scan() {
		raw := scanner.Text()
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			// No colon = not the documented format; ignore for forward compat.
			continue
		}
		key := strings.TrimSpace(line[:colon])
		val := strings.TrimSpace(line[colon+1:])
		switch key {
		case sentinelKeyVersion:
			data.Version = val
			recognized++
		case sentinelKeyTimestamp:
			// Tolerate missing/blank timestamp — only Version+SHA gate validity.
			if val != "" {
				if t, err := time.Parse(time.RFC3339, val); err == nil {
					data.Timestamp = t
				}
			}
			recognized++
		case sentinelKeyRequirementsSHA:
			data.RequirementsSHA256 = val
			recognized++
		case sentinelKeyPythonPBSTag:
			data.PythonPBSTag = val
			recognized++
		default:
			// Unknown key — forward-compat, ignore silently.
		}
	}
	if err := scanner.Err(); err != nil {
		return SentinelData{}, fmt.Errorf("parseSentinel: %w", err)
	}
	if recognized == 0 {
		return SentinelData{}, fmt.Errorf("parseSentinel: no recognized keys")
	}
	return data, nil
}

// hashFile returns the lowercase hex SHA-256 of the file at path. Used by both
// WriteSentinel (to record the bundled requirements hash) and ReadSentinel (to
// verify it on every launch).
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("hashFile: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hashFile: %w", err)
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// WriteSentinel writes the sentinel atomically (tmp + rename) so a crashed
// mid-write never leaves partial state. The target path is derived from
// paths.SetupSentinelPath(data.Version); the parent directory (~/.jarvis/) is
// created if missing.
//
// On any failure the .tmp file is best-effort removed so the home dir isn't
// littered with junk after a failed install.
func WriteSentinel(data SentinelData) error {
	if data.Version == "" {
		return fmt.Errorf("WriteSentinel: Version is required")
	}
	target := paths.SetupSentinelPath(data.Version)
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("WriteSentinel: mkdir %s: %w", dir, err)
	}

	tmp := target + ".tmp"
	serialized := serializeSentinel(data)
	if err := os.WriteFile(tmp, []byte(serialized), 0o644); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("WriteSentinel: write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("WriteSentinel: rename %s -> %s: %w", tmp, target, err)
	}
	return nil
}

// ReadSentinel reads and validates the sentinel at
// paths.SetupSentinelPath(SetupExpectedVersion). Returns ErrSetupRequired
// (wrapped, so errors.Is works) when:
//   - the file is missing
//   - the file is unparseable / has no recognized keys
//   - data.Version != SetupExpectedVersion (older or mismatched install)
//   - data.RequirementsSHA256 != sha256(bundledRequirementsPath)
//
// Any other I/O error (e.g. permission denied) is returned as-is so callers
// can distinguish "needs setup" from "filesystem broken".
func ReadSentinel(bundledRequirementsPath string) (SentinelData, error) {
	path := paths.SetupSentinelPath(SetupExpectedVersion)
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return SentinelData{}, fmt.Errorf("ReadSentinel: %s: %w", path, ErrSetupRequired)
		}
		return SentinelData{}, fmt.Errorf("ReadSentinel: open %s: %w", path, err)
	}
	defer f.Close()

	data, err := parseSentinel(f)
	if err != nil {
		// Malformed file — treat as "needs setup" rather than crashing.
		return SentinelData{}, fmt.Errorf("ReadSentinel: parse %s: %w", path, ErrSetupRequired)
	}

	if data.Version != SetupExpectedVersion {
		return SentinelData{}, fmt.Errorf(
			"ReadSentinel: version %q != expected %q: %w",
			data.Version, SetupExpectedVersion, ErrSetupRequired,
		)
	}

	wantSHA, err := hashFile(bundledRequirementsPath)
	if err != nil {
		// Couldn't hash the bundled file — surface the I/O error rather than
		// pretending setup is incomplete.
		return SentinelData{}, fmt.Errorf("ReadSentinel: hash bundled requirements: %w", err)
	}
	if !strings.EqualFold(data.RequirementsSHA256, wantSHA) {
		return SentinelData{}, fmt.Errorf(
			"ReadSentinel: requirements sha mismatch (sentinel=%s bundled=%s): %w",
			data.RequirementsSHA256, wantSHA, ErrSetupRequired,
		)
	}

	return data, nil
}

// IsSetupComplete is the cheap boolean wrapper StartJarvis (TASK-009) uses on
// every launch. Any error from ReadSentinel — missing file, malformed file,
// version skew, sha mismatch, even a hash I/O error — collapses to false so
// the SetupScreen reliably surfaces whenever we can't prove setup is good.
// Callers that need to distinguish causes should call ReadSentinel directly.
func IsSetupComplete(bundledRequirementsPath string) bool {
	_, err := ReadSentinel(bundledRequirementsPath)
	return err == nil
}
