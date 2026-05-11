// Package terminal provides a unified interface for interacting with different
// terminal applications (CMux, iTerm2, Terminal.app). Each terminal app is
// wrapped as a TerminalProvider, and the TerminalManager aggregates them to
// present a single API for listing windows, sending text, reading output, and
// focusing panes across all available terminals.
package terminal

// TerminalProvider abstracts interaction with different terminal apps.
type TerminalProvider interface {
	// Name returns the human-readable name of the terminal provider
	// (e.g. "CMux", "iTerm2", "Terminal").
	Name() string

	// IsAvailable reports whether the terminal application is currently
	// running and accessible.
	IsAvailable() bool

	// ListWindows returns all terminal sessions/panes/tabs exposed by this
	// provider. The returned TerminalWindow.App field will match Name().
	ListWindows() ([]TerminalWindow, error)

	// SendText sends text (typically a command followed by a newline) to the
	// terminal session identified by windowID.
	SendText(windowID, text string) error

	// ReadText reads the visible text content from the terminal session
	// identified by windowID.
	ReadText(windowID string) (string, error)

	// FocusWindow brings the terminal session identified by windowID to the
	// foreground.
	FocusWindow(windowID string) error
}

// TerminalWindow is the unified representation of a terminal session across all
// supported terminal applications.
type TerminalWindow struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	CWD      string `json:"cwd"`
	TTY      string `json:"tty"`
	App      string `json:"app"` // "CMux", "iTerm2", "Terminal"
	Selected bool   `json:"selected"`
}
