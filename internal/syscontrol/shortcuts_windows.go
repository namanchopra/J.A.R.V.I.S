//go:build windows

// shortcuts_windows.go — Windows substitute for macOS's Shortcuts.app
// bridge (see internal/macctl/shortcuts.go for the macOS reference).
//
// Windows has no first-class equivalent of Shortcuts.app, so per the
// Phase 2 plan (TASK-034) we expose user-authored PowerShell scripts
// living under "%USERPROFILE%\.jarvis\powershell-scripts\*.ps1" as if
// they were Shortcuts. The Wails / daemon tool surface keeps the same
// `mac_run_shortcut(name)` shape; only the backing store and execution
// driver differ.
//
// Layout:
//
//	%USERPROFILE%\.jarvis\powershell-scripts\
//	  Open-Notes.ps1          → ListShortcuts() returns "Open-Notes"
//	  Lock-Screen.ps1         → ListShortcuts() returns "Lock-Screen"
//	  ...
//
// Execution model (RunShortcut):
//
//	powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass \
//	  -File <scripts dir>\<name>.ps1
//
// `-ExecutionPolicy Bypass` is scoped to this single invocation only
// (it does NOT mutate the user's machine-wide policy). This lets users
// drop unsigned scripts into the directory without first running
// Set-ExecutionPolicy themselves — the daemon does the right thing
// out of the box. If a group-policy override forbids even per-call
// bypass, the resulting stderr surface includes the canonical phrase
// "execution of scripts is disabled on this system", which we detect
// and rewrite into a user-actionable remediation message.
//
// Input handling: when `input` is non-empty it is piped to the script's
// stdin (so `param($stdin = $input | Out-String)` style scripts work).
// Empty input means "no stdin at all" — we deliberately do NOT pipe an
// empty string, because some scripts treat an empty $input differently
// from a closed stdin handle (matches the macOS shortcuts.go rationale).
//
// Naming: ListShortcuts returns base names WITHOUT the ".ps1"
// extension, matching the macOS contract (which returns the shortcut
// display name, not the filename). RunShortcut accepts either with or
// without the extension to be friendly to voice / typing input —
// "lock screen" and "lock screen.ps1" both resolve to "Lock-Screen.ps1"
// when case-insensitively matched against the directory listing.
//
// Per project convention, []string slices are returned as []string{}
// (never nil) so the Wails JSON serializer emits "[]" instead of "null".

package syscontrol

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"os/exec"

	"github.com/namanchopra/jarvis/internal/paths"
)

// powerShellScriptsDirName is the leaf directory under JarvisHome where
// user-authored .ps1 files live. Kept as a constant (rather than baked
// into the helper inline) so tests can inspect it without grepping the
// implementation, and so a future migration can rename in one place.
const powerShellScriptsDirName = "powershell-scripts"

// ErrPowerShellExecutionPolicy is returned by RunShortcut when the
// host's effective execution policy blocks the script even with
// `-ExecutionPolicy Bypass`. This typically indicates a group-policy
// (Computer\AllSigned or Restricted) override that supersedes the
// per-call switch. Callers can errors.Is against this sentinel to
// surface the remediation CTA ("Ask your admin to allow PowerShell
// scripts, or run Set-ExecutionPolicy -Scope CurrentUser RemoteSigned
// from an elevated shell").
//
// Distinct sentinel rather than just a wrapped exec error so the UI
// can decide between "transient failure, retry" and "policy issue,
// show docs link" without parsing error strings.
var ErrPowerShellExecutionPolicy = errors.New("syscontrol: PowerShell execution policy blocks script (run `Set-ExecutionPolicy -Scope CurrentUser RemoteSigned` to allow)")

// WindowsShortcuts is the PowerShell-script-backed substitute for the
// macOS Shortcuts.app bridge. Construct via NewWindowsShortcuts; the
// zero value is intentionally NOT usable because the scripts directory
// is resolved at construction time so tests can inject a temp dir.
type WindowsShortcuts struct {
	// scriptsDir is the absolute path to the directory holding .ps1
	// files. Production callers leave this resolved via
	// paths.JarvisHome(); tests override to point at a temp dir.
	scriptsDir string
}

// NewWindowsShortcuts returns a controller rooted at
// "%USERPROFILE%\.jarvis\powershell-scripts". Does NOT create the
// directory — callers (or the user) populate it themselves. A missing
// directory surfaces as "no shortcuts" from ListShortcuts rather than
// an error, matching the macOS behaviour when Shortcuts.app has no
// imported shortcuts.
func NewWindowsShortcuts() *WindowsShortcuts {
	return &WindowsShortcuts{
		scriptsDir: filepath.Join(paths.JarvisHome(), powerShellScriptsDirName),
	}
}

// ScriptsDir returns the absolute scripts directory path. Exposed for
// the Settings UI ("Open scripts folder" button) and for tests.
func (w *WindowsShortcuts) ScriptsDir() string { return w.scriptsDir }

// ListShortcuts returns the base names (sans ".ps1") of every PowerShell
// script in the scripts directory. The directory is allowed to not
// exist — that's an empty list, not an error, because a fresh install
// has no user scripts yet. Returns []string{} (never nil) so the Wails
// JSON serializer emits "[]" instead of "null".
//
// File order matches the filesystem's natural ordering for the user's
// locale (os.ReadDir already returns entries sorted by filename), which
// keeps the UI listing stable across reads.
func (w *WindowsShortcuts) ListShortcuts() ([]string, error) {
	entries, err := os.ReadDir(w.scriptsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Fresh install / user hasn't created the dir yet — that's
			// "no shortcuts", not a failure. Matches the macOS behaviour
			// of Shortcuts.app returning an empty list before any
			// shortcut has been imported.
			return []string{}, nil
		}
		return []string{}, fmt.Errorf("ListShortcuts: %w", err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		// Case-insensitive ".ps1" match — Windows filesystems are
		// case-preserving but case-insensitive, so "Foo.PS1" and
		// "foo.ps1" should both surface. EqualFold avoids a separate
		// ToLower allocation per entry.
		if !strings.EqualFold(filepath.Ext(name), ".ps1") {
			continue
		}
		out = append(out, strings.TrimSuffix(name, filepath.Ext(name)))
	}
	return out, nil
}

// resolveScriptPath maps a user-supplied shortcut `name` to an absolute
// path on disk. Accepts the name with or without the ".ps1" extension,
// case-insensitively against the directory listing. Returns
// os.ErrNotExist (unwrapped via errors.Is at the call site) when no
// match is found so RunShortcut can produce a clean "no such shortcut"
// error rather than a Windows-flavoured "the system cannot find the
// file specified" exec error.
func (w *WindowsShortcuts) resolveScriptPath(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", fmt.Errorf("resolveScriptPath: name is required")
	}
	// Strip an optional ".ps1" suffix the caller may have included.
	base := trimmed
	if strings.EqualFold(filepath.Ext(base), ".ps1") {
		base = strings.TrimSuffix(base, filepath.Ext(base))
	}
	// Reject directory traversal — names like "..\..\Windows\System32\..."
	// must not escape the scripts dir. This is defence in depth: the
	// daemon's policy layer is the first line of defence; this is the
	// second.
	if strings.ContainsAny(base, `/\`) || base == ".." || base == "." {
		return "", fmt.Errorf("resolveScriptPath: invalid script name %q", name)
	}
	entries, err := os.ReadDir(w.scriptsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// No scripts dir means no shortcuts exist at all — surface
			// the same "no such shortcut" path the empty-dir case takes.
			return "", fmt.Errorf("resolveScriptPath: shortcut %q not found: %w", trimmed, os.ErrNotExist)
		}
		return "", fmt.Errorf("resolveScriptPath: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		fname := e.Name()
		if !strings.EqualFold(filepath.Ext(fname), ".ps1") {
			continue
		}
		stem := strings.TrimSuffix(fname, filepath.Ext(fname))
		if strings.EqualFold(stem, base) {
			return filepath.Join(w.scriptsDir, fname), nil
		}
	}
	return "", fmt.Errorf("resolveScriptPath: shortcut %q not found: %w", trimmed, os.ErrNotExist)
}

// RunShortcut executes "<scriptsDir>\<name>.ps1" via powershell.exe and
// returns the script's stdout (trimmed of trailing whitespace). Empty
// `name` is rejected with a wrapped error rather than silently picking
// the first script in the directory — a stray voice misfire shouldn't
// run arbitrary code.
//
// When `input` is non-empty it is piped to the script's stdin; when
// empty, stdin is left closed (NOT piped as ""). Some scripts read
// `$input` differently for closed-vs-empty stdin, and the macOS
// shortcuts CLI has the same distinction (see shortcuts.go), so this
// preserves cross-platform parity.
//
// PowerShell flags:
//
//	-NoProfile          Skip $PROFILE — fast startup, no user config
//	                    side-effects on the executed script.
//	-NonInteractive     Refuse to prompt; surfaces missing inputs as
//	                    errors instead of hanging the daemon.
//	-ExecutionPolicy Bypass
//	                    Per-call only — does NOT mutate the machine
//	                    policy. When a group policy supersedes this,
//	                    we detect the canonical error text and return
//	                    ErrPowerShellExecutionPolicy with a remediation
//	                    hint baked in.
//	-File <path>        Execute the script verbatim (no need to
//	                    quote-and-escape into -Command).
//
// We use CombinedOutput so stderr is included in the failure message —
// PowerShell writes execution-policy errors to stderr, and we need to
// match against that text to choose the right sentinel.
func (w *WindowsShortcuts) RunShortcut(name, input string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("RunShortcut: name is required")
	}
	scriptPath, err := w.resolveScriptPath(name)
	if err != nil {
		return "", fmt.Errorf("RunShortcut: %w", err)
	}
	cmd := exec.Command(
		"powershell.exe",
		"-NoProfile",
		"-NonInteractive",
		"-ExecutionPolicy", "Bypass",
		"-File", scriptPath,
	)
	if input != "" {
		// Pipe via stdin so scripts can read `$input` / `Read-Host`.
		// We deliberately skip this branch when input is empty so the
		// stdin handle is *closed* (not an empty stream), matching the
		// macOS shortcuts.go behaviour.
		cmd.Stdin = strings.NewReader(input)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Detect the canonical execution-policy error string. PowerShell
		// emits this verbatim across versions 5.1, 7.x and on group-
		// policy-restricted hosts, so a substring match is robust
		// without us having to parse a localised error code. The
		// remediation text in ErrPowerShellExecutionPolicy points at
		// the per-user fix, which is what most home users actually need.
		combined := string(out)
		if isExecutionPolicyError(combined) {
			return "", fmt.Errorf("RunShortcut(%q): %w: %s",
				name, ErrPowerShellExecutionPolicy, strings.TrimSpace(combined))
		}
		return "", fmt.Errorf("RunShortcut(%q): %s: %w",
			name, strings.TrimSpace(combined), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// isExecutionPolicyError returns true when the combined stdout/stderr
// from a powershell.exe invocation contains the canonical execution-
// policy block error. PowerShell emits a stable phrase across versions
// 5.1 / 7.x for this case, so a case-insensitive substring match is
// stable AND robust to localisation tweaks (the English phrase is also
// what appears on most localised builds for this specific error).
//
// We match on "execution of scripts is disabled" (the irreducible part)
// and "UnauthorizedAccess" (the FullyQualifiedErrorId) as belt-and-
// braces — either substring is sufficient. Kept as a package-level
// helper so the test can exercise the detector independently of the
// powershell.exe invocation.
func isExecutionPolicyError(s string) bool {
	lower := strings.ToLower(s)
	if strings.Contains(lower, "execution of scripts is disabled") {
		return true
	}
	if strings.Contains(lower, "unauthorizedaccess") &&
		strings.Contains(lower, "executionpolicy") {
		return true
	}
	return false
}
