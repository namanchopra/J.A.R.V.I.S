package terminal

import (
	"fmt"
	"strings"
)

// MacOSTerminalProvider implements TerminalProvider for macOS Terminal.app
// using AppleScript.
type MacOSTerminalProvider struct{}

// NewMacOSTerminalProvider creates a new Terminal.app provider.
func NewMacOSTerminalProvider() *MacOSTerminalProvider {
	return &MacOSTerminalProvider{}
}

func (p *MacOSTerminalProvider) Name() string { return "Terminal" }

func (p *MacOSTerminalProvider) IsAvailable() bool {
	out, err := runAppleScript(`tell application "System Events" to (name of processes) contains "Terminal"`)
	if err != nil {
		return false
	}
	return strings.TrimSpace(out) == "true"
}

func (p *MacOSTerminalProvider) ListWindows() ([]TerminalWindow, error) {
	script := `
tell application "Terminal"
	set windowList to {}
	repeat with w in windows
		repeat with t in tabs of w
			set tabTTY to tty of t
			set tabName to custom title of t
			set end of windowList to tabTTY & "|" & tabName
		end repeat
	end repeat
	return windowList
end tell`

	out, err := runAppleScript(script)
	if err != nil {
		return nil, fmt.Errorf("Terminal ListWindows: %w", err)
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
			App:  "Terminal",
		})
	}
	return windows, nil
}

func (p *MacOSTerminalProvider) SendText(windowID, text string) error {
	if windowID == "" {
		return fmt.Errorf("Terminal SendText: windowID is required")
	}
	// Terminal.app's "do script" command runs text in a specific tab.
	// We locate the tab by its TTY and use "do script" to send text.
	script := fmt.Sprintf(`
tell application "Terminal"
	repeat with w in windows
		repeat with t in tabs of w
			if tty of t is "%s" then
				do script "%s" in t
				return
			end if
		end repeat
	end repeat
end tell`, escapeAppleScript(windowID), escapeAppleScript(text))

	_, err := runAppleScript(script)
	if err != nil {
		return fmt.Errorf("Terminal SendText: %w", err)
	}
	return nil
}

func (p *MacOSTerminalProvider) ReadText(windowID string) (string, error) {
	if windowID == "" {
		return "", fmt.Errorf("Terminal ReadText: windowID is required")
	}
	script := fmt.Sprintf(`
tell application "Terminal"
	repeat with w in windows
		repeat with t in tabs of w
			if tty of t is "%s" then
				return contents of t
			end if
		end repeat
	end repeat
end tell`, escapeAppleScript(windowID))

	out, err := runAppleScript(script)
	if err != nil {
		return "", fmt.Errorf("Terminal ReadText: %w", err)
	}
	return out, nil
}

func (p *MacOSTerminalProvider) FocusWindow(windowID string) error {
	if windowID == "" {
		return fmt.Errorf("Terminal FocusWindow: windowID is required")
	}
	script := fmt.Sprintf(`
tell application "Terminal"
	activate
	repeat with w in windows
		repeat with t in tabs of w
			if tty of t is "%s" then
				set selected of t to true
				set index of w to 1
				return
			end if
		end repeat
	end repeat
end tell`, escapeAppleScript(windowID))

	_, err := runAppleScript(script)
	if err != nil {
		return fmt.Errorf("Terminal FocusWindow: %w", err)
	}
	return nil
}
