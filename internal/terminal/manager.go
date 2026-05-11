package terminal

import (
	"fmt"
	"log/slog"

	"github.com/namanchopra/jarvis/internal/cmux"
)

// TerminalManager aggregates multiple TerminalProvider implementations and
// routes operations to the correct provider based on the window's App field.
type TerminalManager struct {
	providers    []TerminalProvider
	errSuppressed map[string]bool // suppress repeated error logs per provider
}

// NewTerminalManager creates a manager that auto-detects available terminal
// applications. CMux is checked first (preferred), then iTerm2, then
// Terminal.app. Only providers that are available at construction time are
// registered, but availability is re-checked on each operation.
func NewTerminalManager(cmuxClient *cmux.Client) *TerminalManager {
	tm := &TerminalManager{errSuppressed: make(map[string]bool)}

	// Register providers in priority order.
	cmuxProvider := NewCMuxProvider(cmuxClient)
	tm.providers = append(tm.providers, cmuxProvider)
	if cmuxProvider.IsAvailable() {
		slog.Info("terminal provider registered", "provider", cmuxProvider.Name())
	}

	iterm2Provider := NewITerm2Provider()
	tm.providers = append(tm.providers, iterm2Provider)
	if iterm2Provider.IsAvailable() {
		slog.Info("terminal provider registered", "provider", iterm2Provider.Name())
	}

	macTermProvider := NewMacOSTerminalProvider()
	tm.providers = append(tm.providers, macTermProvider)
	if macTermProvider.IsAvailable() {
		slog.Info("terminal provider registered", "provider", macTermProvider.Name())
	}

	return tm
}

// GetAvailableTerminals returns the names of terminal applications that are
// currently running and accessible.
func (tm *TerminalManager) GetAvailableTerminals() []string {
	var names []string
	for _, p := range tm.providers {
		if p.IsAvailable() {
			names = append(names, p.Name())
		}
	}
	return names
}

// ListAllWindows returns terminal windows from all available providers. If a
// provider fails, its error is logged and the other providers are still queried.
func (tm *TerminalManager) ListAllWindows() ([]TerminalWindow, error) {
	var all []TerminalWindow
	var lastErr error

	for _, p := range tm.providers {
		if !p.IsAvailable() {
			slog.Info("ListAllWindows: provider not available", "provider", p.Name())
			continue
		}
		windows, err := p.ListWindows()
		if err != nil {
			slog.Warn("ListAllWindows: provider failed", "provider", p.Name(), "err", err)
			lastErr = err
			continue
		}
		slog.Info("ListAllWindows: provider returned windows",
			"provider", p.Name(), "count", len(windows))
		for i, w := range windows {
			slog.Info("ListAllWindows: window",
				"provider", p.Name(), "index", i,
				"id", w.ID, "tty", w.TTY, "name", w.Name)
		}
		all = append(all, windows...)
	}

	// If we got no windows and there was an error, surface it.
	if len(all) == 0 && lastErr != nil {
		slog.Warn("ListAllWindows: no windows from any provider", "lastErr", lastErr)
		return nil, lastErr
	}

	if all == nil {
		all = []TerminalWindow{}
	}
	return all, nil
}

// SendText routes a send-text command to the provider that owns the given
// windowID. It tries each available provider in order; the correct provider
// is the one whose ListWindows output contains a matching ID.
func (tm *TerminalManager) SendText(windowID, text string) error {
	if windowID == "" {
		return fmt.Errorf("SendText: windowID is required")
	}
	p, err := tm.findProvider(windowID)
	if err != nil {
		return err
	}
	return p.SendText(windowID, text)
}

// ReadText routes a read-text command to the provider that owns the given
// windowID.
func (tm *TerminalManager) ReadText(windowID string) (string, error) {
	if windowID == "" {
		return "", fmt.Errorf("ReadText: windowID is required")
	}
	p, err := tm.findProvider(windowID)
	if err != nil {
		return "", err
	}
	return p.ReadText(windowID)
}

// FocusWindow routes a focus command to the provider that owns the given
// windowID.
func (tm *TerminalManager) FocusWindow(windowID string) error {
	if windowID == "" {
		return fmt.Errorf("FocusWindow: windowID is required")
	}
	p, err := tm.findProvider(windowID)
	if err != nil {
		return err
	}
	return p.FocusWindow(windowID)
}

// findProvider determines which provider owns the given windowID by listing
// each provider's windows and checking for a match. This is slightly
// expensive but ensures correct routing even when IDs overlap between apps.
func (tm *TerminalManager) findProvider(windowID string) (TerminalProvider, error) {
	for _, p := range tm.providers {
		if !p.IsAvailable() {
			continue
		}
		windows, err := p.ListWindows()
		if err != nil {
			continue
		}
		for _, w := range windows {
			if w.ID == windowID {
				return p, nil
			}
		}
	}
	return nil, fmt.Errorf("no terminal provider found for window %q", windowID)
}
