package arch

import (
	"errors"
	"strings"
	"testing"
)

func TestCheck(t *testing.T) {
	tests := []struct {
		name      string
		goarch    string
		wantErr   bool
		wantSubs  []string // substrings the error message must contain
	}{
		{
			name:    "arm64 is supported",
			goarch:  "arm64",
			wantErr: false,
		},
		{
			name:     "amd64 is rejected",
			goarch:   "amd64",
			wantErr:  true,
			wantSubs: []string{"Jarvis requires Apple Silicon", "amd64"},
		},
		{
			name:     "386 is rejected",
			goarch:   "386",
			wantErr:  true,
			wantSubs: []string{"Jarvis requires Apple Silicon", "386"},
		},
		{
			name:     "empty arch is rejected",
			goarch:   "",
			wantErr:  true,
			wantSubs: []string{"Jarvis requires Apple Silicon"},
		},
	}

	// Save and restore the package-level variable so tests do not leak state.
	orig := currentGOARCH
	t.Cleanup(func() { currentGOARCH = orig })

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			currentGOARCH = tt.goarch
			err := Check()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Check() returned nil, want error for GOARCH=%q", tt.goarch)
				}
				var ua *ErrUnsupportedArch
				if !errors.As(err, &ua) {
					t.Fatalf("Check() error type = %T, want *ErrUnsupportedArch", err)
				}
				if ua.GOARCH != tt.goarch {
					t.Errorf("ErrUnsupportedArch.GOARCH = %q, want %q", ua.GOARCH, tt.goarch)
				}
				msg := err.Error()
				for _, sub := range tt.wantSubs {
					if !strings.Contains(msg, sub) {
						t.Errorf("error message %q missing substring %q", msg, sub)
					}
				}
			} else {
				if err != nil {
					t.Fatalf("Check() returned %v, want nil for GOARCH=%q", err, tt.goarch)
				}
			}
		})
	}
}
