//go:build windows

package terminal

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// WindowsTerminalProvider implements TerminalProvider for the Microsoft Windows
// Terminal application (wt.exe). Unlike iTerm2 and Terminal.app on macOS,
// Windows Terminal does not ship an AppleScript-equivalent introspection or
// remote-control API — there is no scripting bridge to enumerate panes or read
// back terminal contents in-process. As a result this provider implements the
// "launch" half of the TerminalProvider contract (which is what
// LaunchReposInTerminal needs to satisfy TASK-029's acceptance criteria) and
// returns empty results / clear errors from the read/send/focus methods.
//
// The full bi-directional control path on Windows is intended to live in the
// CMux provider, which talks to its helper over a socket and is portable
// across platforms. Anything more sophisticated (e.g. injecting keystrokes
// into a wt.exe pane from a parent process) requires ConPTY + a custom
// pseudo-terminal host, which is out of scope for this task.
type WindowsTerminalProvider struct {
	// wtPath caches the resolved path to wt.exe so we don't run exec.LookPath
	// on every IsAvailable / Launch call. Empty string means "not resolved
	// yet"; a non-empty value with the sentinel "__missing__" means we have
	// looked and it is not on PATH.
	wtPath string
}

const wtMissingSentinel = "__missing__"

// NewWindowsTerminalProvider constructs a TerminalProvider that opens repo
// tabs in Windows Terminal (wt.exe) when available and gracefully falls back
// to cmd.exe otherwise.
func NewWindowsTerminalProvider() *WindowsTerminalProvider {
	return &WindowsTerminalProvider{}
}

// Name implements TerminalProvider. It returns "WindowsTerminal" when wt.exe
// is on PATH and "cmd" when we have to fall back, so the user-visible
// terminal name in the UI reflects what is actually opening.
func (p *WindowsTerminalProvider) Name() string {
	if p.resolveWTPath() != "" {
		return "WindowsTerminal"
	}
	return "cmd"
}

// IsAvailable reports whether either Windows Terminal or cmd.exe is reachable.
// On any supported Windows SKU cmd.exe is always present, so this provider is
// effectively always available — but we still verify because Jarvis can run
// inside locked-down container/CI images where cmd.exe has been stripped from
// PATH and the manager should skip the provider entirely in that case.
func (p *WindowsTerminalProvider) IsAvailable() bool {
	if p.resolveWTPath() != "" {
		return true
	}
	// cmd.exe is the universal fallback on Windows.
	if _, err := exec.LookPath("cmd.exe"); err == nil {
		return true
	}
	return false
}

// ListWindows returns an empty slice because Windows Terminal does not expose
// a programmatic way to enumerate its open tabs/panes from an external
// process. Callers that need pane introspection on Windows should use the
// CMux provider, which talks to a co-operating helper. Returning []T{} (not
// nil) keeps the Wails JSON serialisation consistent with the other
// providers — see "Nil slices" rule in CLAUDE.md.
func (p *WindowsTerminalProvider) ListWindows() ([]TerminalWindow, error) {
	return []TerminalWindow{}, nil
}

// SendText returns an error: pushing text into an existing wt.exe tab is not
// supported without a ConPTY-backed helper. Callers that need this on Windows
// should route through CMux.
func (p *WindowsTerminalProvider) SendText(windowID, text string) error {
	if windowID == "" {
		return fmt.Errorf("WindowsTerminal SendText: windowID is required")
	}
	return fmt.Errorf("WindowsTerminal SendText: not supported (use CMux provider for bi-directional control on Windows)")
}

// ReadText returns an error for the same reason as SendText — there is no
// public API to read the visible buffer of a wt.exe pane.
func (p *WindowsTerminalProvider) ReadText(windowID string) (string, error) {
	if windowID == "" {
		return "", fmt.Errorf("WindowsTerminal ReadText: windowID is required")
	}
	return "", fmt.Errorf("WindowsTerminal ReadText: not supported (use CMux provider for bi-directional control on Windows)")
}

// FocusWindow returns an error: Windows Terminal does not expose a per-tab
// focus API to external processes.
func (p *WindowsTerminalProvider) FocusWindow(windowID string) error {
	if windowID == "" {
		return fmt.Errorf("WindowsTerminal FocusWindow: windowID is required")
	}
	return fmt.Errorf("WindowsTerminal FocusWindow: not supported (use CMux provider for bi-directional control on Windows)")
}

// LaunchInWindowsTerminal opens a new tab in Windows Terminal pointed at
// repoPath and, if command is non-empty, executes it. When wt.exe is not on
// PATH this falls back to launching cmd.exe with the same working directory
// and command. This is the entry point app.go's LaunchReposInTerminal should
// call from its Windows branch (acceptance criterion #1: "opens wt.exe tabs
// per repo").
//
// Behaviour summary:
//
//	wt.exe present  : wt.exe -w 0 nt -d <repoPath> [cmd.exe /K <command>]
//	wt.exe missing  : cmd.exe /c start "" cmd.exe /K "cd /D <repoPath> && <command>"
//	                  + slog.Warn so the user sees the degraded experience
func (p *WindowsTerminalProvider) LaunchInWindowsTerminal(repoPath, command string) error {
	repoPath = strings.TrimSpace(repoPath)
	if repoPath == "" {
		return fmt.Errorf("LaunchInWindowsTerminal: repoPath is required")
	}
	command = strings.TrimSpace(command)

	if wt := p.resolveWTPath(); wt != "" {
		return p.launchInWT(wt, repoPath, command)
	}

	slog.Warn("LaunchInWindowsTerminal: wt.exe not found on PATH; falling back to cmd.exe",
		"repo", repoPath)
	return p.launchInCmd(repoPath, command)
}

// launchInWT shells out to wt.exe with arguments that:
//   - target window 0 (-w 0): adds a new tab to the most-recently-active
//     Windows Terminal window, opening a new window if none exists. This
//     gives us a single growing window across repeated calls rather than one
//     window per repo.
//   - open a new tab (nt) with a starting directory (-d): satisfies
//     acceptance criterion #2 ("each tab starts in repo dir").
//   - optionally invoke cmd.exe /K with the user's command so the shell
//     stays open after the command exits (matches iTerm2/Terminal.app
//     behaviour).
//
// We use exec.Command with positional arguments rather than building a
// single command line, so Go's syscall layer handles quoting per Windows
// CommandLineToArgvW rules and our repoPath / command can contain spaces.
func (p *WindowsTerminalProvider) launchInWT(wtPath, repoPath, command string) error {
	args := []string{"-w", "0", "nt", "-d", repoPath}
	if command != "" {
		// Pass the command through cmd.exe /K so the window doesn't close
		// the moment the command exits — this matches the macOS providers
		// (do script in Terminal.app, write text in iTerm2) which run the
		// command inside the persistent shell.
		args = append(args, "cmd.exe", "/K", command)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, wtPath, args...)
	// Hide the transient cmd.exe console used to invoke wt.exe so wails dev
	// sessions don't flash a black window per repo. The wt.exe process
	// itself is GUI-owned and is unaffected by this flag.
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("launchInWT: starting wt.exe: %w", err)
	}
	// We deliberately do not Wait() on wt.exe — it forks the actual window
	// into a long-running session host process and returns quickly, but the
	// foreground binary may stay alive for the lifetime of the new tab on
	// some Windows Terminal builds. Detaching here keeps LaunchReposInTerminal
	// non-blocking across many repos.
	go func() { _ = cmd.Wait() }()
	return nil
}

// launchInCmd opens a new cmd.exe window via `start` so the parent process
// returns immediately. `start ""` provides an empty window title (otherwise
// the first quoted argument is consumed as the title and the command is
// misinterpreted). `/K` keeps the shell open after the command exits.
//
// This path is taken when wt.exe is not on PATH — typically older Windows 10
// LTSC, Server SKUs without Microsoft Store, or stripped-down CI images.
// It satisfies the "falls back to cmd.exe + warns" acceptance criterion.
func (p *WindowsTerminalProvider) launchInCmd(repoPath, command string) error {
	cmdline := fmt.Sprintf(`cd /D %s`, quoteForCmd(repoPath))
	if command != "" {
		cmdline = fmt.Sprintf(`cd /D %s && %s`, quoteForCmd(repoPath), command)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "cmd.exe", "/c", "start", "", "cmd.exe", "/K", cmdline)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("launchInCmd: starting cmd.exe: %w", err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// resolveWTPath caches the result of exec.LookPath("wt.exe") so repeated
// calls don't hit the filesystem. We treat a missing wt.exe as a persistent
// condition for the lifetime of the provider instance — Windows Terminal
// install/uninstall events are rare and a Jarvis restart picks up the new
// state.
func (p *WindowsTerminalProvider) resolveWTPath() string {
	if p.wtPath == wtMissingSentinel {
		return ""
	}
	if p.wtPath != "" {
		return p.wtPath
	}
	path, err := exec.LookPath("wt.exe")
	if err != nil {
		p.wtPath = wtMissingSentinel
		return ""
	}
	p.wtPath = path
	return path
}

// quoteForCmd wraps a path in double quotes if it contains characters cmd.exe
// would otherwise misinterpret (space, ampersand, parenthesis, percent, caret).
// Internal double quotes are doubled per cmd's de-facto escaping rule. This
// is intentionally narrower than shellquote-style logic — we only ever quote
// filesystem paths here, never arbitrary command lines.
func quoteForCmd(s string) string {
	needs := strings.ContainsAny(s, ` &()%^"`)
	if !needs {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}
