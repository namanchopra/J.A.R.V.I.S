package terminal

import (
	"fmt"
	"strings"
)

// ITerm2Provider implements TerminalProvider for iTerm2 using AppleScript.
type ITerm2Provider struct{}

// NewITerm2Provider creates a new iTerm2 terminal provider.
func NewITerm2Provider() *ITerm2Provider {
	return &ITerm2Provider{}
}

func (p *ITerm2Provider) Name() string { return "iTerm2" }

func (p *ITerm2Provider) IsAvailable() bool {
	out, err := runAppleScript(`tell application "System Events" to (name of processes) contains "iTerm2"`)
	if err != nil {
		return false
	}
	return strings.TrimSpace(out) == "true"
}

func (p *ITerm2Provider) ListWindows() ([]TerminalWindow, error) {
	script := `
tell application "iTerm2"
	set windowList to {}
	repeat with w in windows
		repeat with t in tabs of w
			repeat with s in sessions of t
				set sessionName to name of s
				set sessionTTY to tty of s
				set end of windowList to sessionTTY & "|" & sessionName
			end repeat
		end repeat
	end repeat
	return windowList
end tell`

	out, err := runAppleScript(script)
	if err != nil {
		return nil, fmt.Errorf("iTerm2 ListWindows: %w", err)
	}

	if out == "" {
		return []TerminalWindow{}, nil
	}

	// AppleScript returns a comma-separated list of "tty|name" entries.
	entries := strings.Split(out, ", ")
	windows := make([]TerminalWindow, 0, len(entries))
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parts := strings.SplitN(entry, "|", 2)
		tty := parts[0]
		name := ""
		if len(parts) > 1 {
			name = parts[1]
		}
		windows = append(windows, TerminalWindow{
			ID:   tty,
			Name: name,
			TTY:  tty,
			App:  "iTerm2",
		})
	}
	return windows, nil
}

func (p *ITerm2Provider) SendText(windowID, text string) error {
	if windowID == "" {
		return fmt.Errorf("iTerm2 SendText: windowID is required")
	}
	script := fmt.Sprintf(`
tell application "iTerm2"
	repeat with w in windows
		repeat with t in tabs of w
			repeat with s in sessions of t
				if tty of s is "%s" then
					tell s to write text "%s"
				end if
			end repeat
		end repeat
	end repeat
end tell`, escapeAppleScript(windowID), escapeAppleScript(text))

	_, err := runAppleScript(script)
	if err != nil {
		return fmt.Errorf("iTerm2 SendText: %w", err)
	}
	return nil
}

func (p *ITerm2Provider) ReadText(windowID string) (string, error) {
	if windowID == "" {
		return "", fmt.Errorf("iTerm2 ReadText: windowID is required")
	}
	script := fmt.Sprintf(`
tell application "iTerm2"
	repeat with w in windows
		repeat with t in tabs of w
			repeat with s in sessions of t
				if tty of s is "%s" then
					return contents of s
				end if
			end repeat
		end repeat
	end repeat
end tell`, escapeAppleScript(windowID))

	out, err := runAppleScript(script)
	if err != nil {
		return "", fmt.Errorf("iTerm2 ReadText: %w", err)
	}
	return out, nil
}

func (p *ITerm2Provider) FocusWindow(windowID string) error {
	if windowID == "" {
		return fmt.Errorf("iTerm2 FocusWindow: windowID is required")
	}
	script := fmt.Sprintf(`
tell application "iTerm2"
	activate
	repeat with w in windows
		repeat with t in tabs of w
			repeat with s in sessions of t
				if tty of s is "%s" then
					select s
				end if
			end repeat
		end repeat
	end repeat
end tell`, escapeAppleScript(windowID))

	_, err := runAppleScript(script)
	if err != nil {
		return fmt.Errorf("iTerm2 FocusWindow: %w", err)
	}
	return nil
}
