package terminal

import (
	"github.com/namanchopra/jarvis/internal/cmux"
)

// CMuxProvider wraps an existing cmux.Client as a TerminalProvider.
type CMuxProvider struct {
	client *cmux.Client
}

// NewCMuxProvider creates a TerminalProvider backed by the CMux terminal
// multiplexer. The client may be nil, in which case IsAvailable returns false.
func NewCMuxProvider(client *cmux.Client) *CMuxProvider {
	return &CMuxProvider{client: client}
}

func (p *CMuxProvider) Name() string { return "CMux" }

func (p *CMuxProvider) IsAvailable() bool {
	return p.client != nil && p.client.IsAvailable()
}

func (p *CMuxProvider) ListWindows() ([]TerminalWindow, error) {
	surfaces, err := p.client.ListSurfaces()
	if err != nil {
		return nil, err
	}

	windows := make([]TerminalWindow, 0, len(surfaces))
	for _, s := range surfaces {
		windows = append(windows, TerminalWindow{
			ID:       s.Ref,
			Name:     s.Title,
			TTY:      s.TTY,
			App:      "CMux",
			Selected: s.Selected,
		})
	}
	return windows, nil
}

func (p *CMuxProvider) SendText(windowID, text string) error {
	return p.client.SendText(windowID, text)
}

func (p *CMuxProvider) ReadText(windowID string) (string, error) {
	return p.client.ReadText(windowID)
}

func (p *CMuxProvider) FocusWindow(windowID string) error {
	if err := p.client.FocusSurface(windowID); err != nil {
		return err
	}
	// Also bring CMux to the foreground.
	return p.client.ActivateAndSwitchTab()
}
