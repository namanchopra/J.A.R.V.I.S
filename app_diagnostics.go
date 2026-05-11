// app_diagnostics.go — Wails bindings for TASK-022 (Diagnostics panel).
//
// Exposes a single GetDiagnostics binding that returns a snapshot of the
// live system health surface used by frontend/src/views/settings/
// DiagnosticsPanel.tsx (polled every 2 seconds).
//
// Design constraints:
//   - Cheap: the panel polls every 2s, so the whole call must stay
//     well under ~50ms in the happy path. The only outbound network call
//     is IsOllamaRunning, which has its own 1s timeout (see app_validators.go).
//   - Side-effect-free: this binding only reads existing app state and
//     filesystem metadata. It never mutates a.jarvis*, config, or disk.
//   - Bounded disk walk: the ~/.jarvis disk-usage walker is capped at
//     30,000 file entries to keep pathological scans (corrupted home dirs,
//     symlink loops) from stalling the panel.
//
// Per TASK-022 scope, this file deliberately does NOT add accessor methods
// to app.go / app_jarvis.go. It reads the existing unexported fields
// (a.jarvisProcess, a.jarvisRestarts, a.jarvisMu) directly because both
// files live in package `main`.
package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/namanchopra/jarvis/internal/config"
	"github.com/namanchopra/jarvis/internal/paths"
	"github.com/namanchopra/jarvis/internal/permissions"
)

// DiagnosticsSnapshot is one frame of the live health panel, returned by
// GetDiagnostics. All fields are populated on every call (no nilable
// sub-structs) so the frontend can render a stable table layout.
type DiagnosticsSnapshot struct {
	Daemon    DaemonStatus    `json:"daemon"`
	MicPerm   string          `json:"micPermission"`
	MobileAPI MobileAPIStatus `json:"mobileApi"`
	LLMChain  LLMChainStatus  `json:"llmChain"`
	Models    []ModelStatus   `json:"models"`
	Ollama    bool            `json:"ollamaRunning"`
	DiskUsage DiskUsageStatus `json:"diskUsage"`
}

// DaemonStatus reports the live state of the Python Jarvis daemon process
// spawned by StartJarvis (app_jarvis.go).
type DaemonStatus struct {
	// Running is true when a.jarvisProcess is non-nil under jarvisMu.
	Running bool `json:"running"`
	// Restarts mirrors a.jarvisRestarts. NOTE: the current StartJarvis
	// implementation uses this as a "stop sentinel" rather than a true
	// counter (see app_jarvis.go) — it gets set to maxJarvisRestarts on
	// StopJarvis but is never incremented per restart attempt. Treat this
	// value as a coarse indicator until a follow-up converts it into a
	// proper counter.
	Restarts int `json:"restarts"`
	// LastErr is currently always empty — the daemon monitor goroutine
	// logs errors via slog but does not retain them on the App struct.
	// Reserved for a future TODO that wires up a last-error field on App.
	LastErr string `json:"lastError,omitempty"`
}

// MobileAPIStatus carries port + masked token preview for the mobile API
// server. The token preview is always the last 4 chars prefixed with "..."
// (e.g. "...a3f9") so it can be displayed safely in a diagnostics dump.
type MobileAPIStatus struct {
	Port  int    `json:"port"`
	Token string `json:"tokenPreview"`
}

// LLMChainStatus surfaces the configured LLM provider priority. Because the
// running daemon's *actual* provider selection state is not currently
// surfaced to the App struct, we infer the active provider from the same
// configured priority order the daemon uses:
//
//	openrouter > google > anthropic > ollama
//
// TODO: replace inference with the daemon's runtime state once an IPC
// channel is added to app_jarvis.go.
type LLMChainStatus struct {
	Active  string `json:"active"`
	LastErr string `json:"lastError,omitempty"`
}

// ModelStatus describes one bundled (or ~/.jarvis/models/) model file:
// where it should live, whether the file exists, and its size in MB.
type ModelStatus struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Loaded bool   `json:"loaded"`
	SizeMB int    `json:"sizeMb,omitempty"`
}

// DiskUsageStatus reports the total size (in megabytes) of the Jarvis data
// directory at ~/.jarvis/. Computed by a bounded WalkDir (30k file cap).
type DiskUsageStatus struct {
	JarvisHome string `json:"jarvisHome"`
	SizeMB     int    `json:"sizeMb"`
}

// GetDiagnostics returns a snapshot of system health. Called every 2s by the
// Diagnostics panel. Cheap (< 50ms in the happy path); the only network
// call (IsOllamaRunning) carries its own 1s timeout.
func (a *App) GetDiagnostics() DiagnosticsSnapshot {
	return DiagnosticsSnapshot{
		Daemon:    a.daemonStatus(),
		MicPerm:   permissions.MicStatus(),
		MobileAPI: a.mobileAPIStatus(),
		LLMChain:  a.llmChainStatus(),
		Models:    a.modelsStatus(),
		Ollama:    a.IsOllamaRunning(),
		DiskUsage: a.diskUsage(),
	}
}

// daemonStatus reads the live daemon process state from the App struct.
// Takes jarvisMu briefly to protect against StartJarvis/StopJarvis writers.
func (a *App) daemonStatus() DaemonStatus {
	a.jarvisMu.Lock()
	defer a.jarvisMu.Unlock()
	return DaemonStatus{
		Running:  a.jarvisProcess != nil,
		Restarts: a.jarvisRestarts,
		// LastErr intentionally empty — no app-level field exists yet.
	}
}

// mobileAPIStatus reads the current mobile API port + token from config and
// masks the token to its last 4 chars. If the token is shorter than 4 chars
// (which only happens before the auto-generated token is written on first
// startup) we return "(none)" instead.
func (a *App) mobileAPIStatus() MobileAPIStatus {
	cfg := config.Get()
	token := cfg.MobileAPIToken
	preview := "(none)"
	if len(token) >= 4 {
		preview = "..." + token[len(token)-4:]
	}
	return MobileAPIStatus{
		Port:  cfg.MobileAPIPort,
		Token: preview,
	}
}

// llmChainStatus infers the active LLM provider from configured keys using
// the same priority order the daemon uses (openrouter > google > anthropic
// > ollama). Returns the first available provider as
// "<provider>:<default-model>" or "none" if nothing is configured.
//
// This is inference, not runtime state — the actual provider the daemon
// selects at request time may differ if a provider returns an error and
// the chain falls through. See the LLMChainStatus type doc for the TODO
// to surface real runtime state.
func (a *App) llmChainStatus() LLMChainStatus {
	cfg := config.Get()

	// Priority order mirrors the daemon's jarvis_* fallback chain.
	switch {
	case strings.TrimSpace(cfg.JarvisAPIKey) != "" && strings.EqualFold(cfg.JarvisProvider, "openrouter"):
		return LLMChainStatus{Active: "openrouter:gemini-2.5-flash"}
	case strings.TrimSpace(cfg.JarvisAPIKey) != "" && strings.EqualFold(cfg.JarvisProvider, "google"):
		return LLMChainStatus{Active: "google:gemini-2.5-flash"}
	case strings.TrimSpace(cfg.JarvisAPIKey) != "" && strings.EqualFold(cfg.JarvisProvider, "anthropic"):
		return LLMChainStatus{Active: "anthropic:claude-haiku-4-5"}
	case strings.TrimSpace(cfg.JarvisAPIKey) != "":
		// Provider not explicitly set; trust the daemon's default chain
		// and report the highest-priority option.
		return LLMChainStatus{Active: "openrouter:gemini-2.5-flash"}
	case a.IsOllamaRunning():
		return LLMChainStatus{Active: "ollama:qwen3:4b"}
	default:
		return LLMChainStatus{Active: "none", LastErr: "no provider key configured and ollama not running"}
	}
}

// modelsStatus checks for the existence of the two bundled model files
// (VibeVoice + whisper-small) and reports their size in MB if present.
// Falls back to ~/.jarvis/models/ when the .app-bundled location is
// unavailable (i.e. in dev mode via `wails dev`).
func (a *App) modelsStatus() []ModelStatus {
	bundled := paths.BundledModelsDir()
	if bundled == "" {
		bundled = paths.ModelsDir()
	}

	checks := []struct {
		Name string
		File string
	}{
		{"vibevoice", filepath.Join(bundled, "vibevoice", "model.safetensors")},
		{"whisper-small", filepath.Join(bundled, "whisper-small", "weights.npz")},
	}

	out := make([]ModelStatus, 0, len(checks))
	for _, c := range checks {
		st, err := os.Stat(c.File)
		status := ModelStatus{Name: c.Name, Path: c.File, Loaded: err == nil}
		if err == nil && !st.IsDir() {
			status.SizeMB = int(st.Size() / 1024 / 1024)
		}
		out = append(out, status)
	}
	return out
}

// diskUsage walks ~/.jarvis/ and sums file sizes. Capped at 30,000 file
// entries to avoid pathological scans (corrupted home dirs, symlink loops).
// On any walk error, returns whatever was tallied so far rather than zero.
func (a *App) diskUsage() DiskUsageStatus {
	home := paths.JarvisHome()
	var total int64
	count := 0
	_ = filepath.WalkDir(home, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Skip unreadable entries (permission denied, broken symlinks).
			// Returning nil keeps the walk going; returning the error would
			// abort and we prefer a partial total.
			return nil
		}
		count++
		if count > 30000 {
			return filepath.SkipAll
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		total += info.Size()
		return nil
	})
	return DiskUsageStatus{
		JarvisHome: home,
		SizeMB:     int(total / 1024 / 1024),
	}
}
