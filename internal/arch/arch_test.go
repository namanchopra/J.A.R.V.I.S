package arch

import (
	"errors"
	"strings"
	"testing"
)

func TestCheck(t *testing.T) {
	tests := []struct {
		name     string
		goos     string
		goarch   string
		wantErr  bool
		wantSubs []string // substrings the error message must contain
	}{
		// macOS — only arm64 is allowed.
		{
			name:    "darwin arm64 is supported",
			goos:    "darwin",
			goarch:  "arm64",
			wantErr: false,
		},
		{
			name:     "darwin amd64 is rejected",
			goos:     "darwin",
			goarch:   "amd64",
			wantErr:  true,
			wantSubs: []string{"Jarvis requires Apple Silicon", "amd64"},
		},
		{
			name:     "darwin 386 is rejected",
			goos:     "darwin",
			goarch:   "386",
			wantErr:  true,
			wantSubs: []string{"Jarvis requires Apple Silicon", "386"},
		},
		{
			name:     "darwin empty arch is rejected",
			goos:     "darwin",
			goarch:   "",
			wantErr:  true,
			wantSubs: []string{"Jarvis requires Apple Silicon"},
		},

		// Windows — both amd64 and arm64 are allowed.
		{
			name:    "windows amd64 is supported",
			goos:    "windows",
			goarch:  "amd64",
			wantErr: false,
		},
		{
			name:    "windows arm64 is supported",
			goos:    "windows",
			goarch:  "arm64",
			wantErr: false,
		},
		{
			name:     "windows 386 is rejected",
			goos:     "windows",
			goarch:   "386",
			wantErr:  true,
			wantSubs: []string{"x64 or arm64", "386"},
		},
		{
			name:     "windows empty arch is rejected",
			goos:     "windows",
			goarch:   "",
			wantErr:  true,
			wantSubs: []string{"x64 or arm64"},
		},

		// Other platforms are unsupported regardless of architecture.
		{
			name:     "linux arm64 is rejected",
			goos:     "linux",
			goarch:   "arm64",
			wantErr:  true,
			wantSubs: []string{"linux", "arm64"},
		},
	}

	// Save and restore the package-level variables so tests do not leak state.
	origArch := currentGOARCH
	origOS := currentGOOS
	t.Cleanup(func() {
		currentGOARCH = origArch
		currentGOOS = origOS
	})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			currentGOOS = tt.goos
			currentGOARCH = tt.goarch
			err := Check()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Check() returned nil, want error for GOOS=%q GOARCH=%q", tt.goos, tt.goarch)
				}
				var ua *ErrUnsupportedArch
				if !errors.As(err, &ua) {
					t.Fatalf("Check() error type = %T, want *ErrUnsupportedArch", err)
				}
				if ua.GOARCH != tt.goarch {
					t.Errorf("ErrUnsupportedArch.GOARCH = %q, want %q", ua.GOARCH, tt.goarch)
				}
				if ua.GOOS != tt.goos {
					t.Errorf("ErrUnsupportedArch.GOOS = %q, want %q", ua.GOOS, tt.goos)
				}
				msg := err.Error()
				for _, sub := range tt.wantSubs {
					if !strings.Contains(msg, sub) {
						t.Errorf("error message %q missing substring %q", msg, sub)
					}
				}
			} else {
				if err != nil {
					t.Fatalf("Check() returned %v, want nil for GOOS=%q GOARCH=%q", err, tt.goos, tt.goarch)
				}
			}
		})
	}
}
