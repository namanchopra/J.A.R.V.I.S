// app_config_io.go — Wails bindings for Settings → Advanced config IO
// (TASK-023, Phase 2 P2).
//
// Three primary bindings are exposed to the frontend:
//
//   ExportConfig         — Save current config to a user-chosen JSON file.
//   ImportConfig         — Replace current config with a JSON file's contents.
//   ResetConfig          — Wipe config back to defaults (optional preserve keys).
//   OpenFileForImport    — Thin wrapper around runtime.OpenFileDialog so the
//                          frontend can split picking + confirmation into two
//                          steps (pick first, confirm overwrite, then call
//                          ImportConfig with the path).
//
// Concurrency: this file does NOT introduce any new locks on the App struct.
// All config state is owned by package internal/config, which already
// serializes Load/Save/Get via its own sync.RWMutex. Calling config.Save()
// updates that package's in-memory `current` pointer atomically; subsequent
// config.Get() calls return the new value.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/namanchopra/jarvis/internal/config"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// ExportConfig writes the current in-memory config to a user-chosen JSON
// file via a native save dialog. Returns the path written, or "" if the
// user cancels. Any error (dialog failure, marshal failure, write failure)
// is propagated to the frontend.
//
// The dump is the same shape the disk file uses (json.MarshalIndent), so a
// round-trip via ImportConfig produces an equivalent config.
func (a *App) ExportConfig() (string, error) {
	cfg := config.Get()

	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "Export Jarvis config",
		DefaultFilename: "jarvis-config.json",
		Filters: []runtime.FileFilter{
			{DisplayName: "JSON", Pattern: "*.json"},
		},
	})
	if err != nil {
		return "", fmt.Errorf("ExportConfig: %w", err)
	}
	if path == "" {
		// User cancelled — communicate via empty string, no error.
		return "", nil
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", fmt.Errorf("ExportConfig: marshal: %w", err)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("ExportConfig: write %s: %w", path, err)
	}
	return path, nil
}

// OpenFileForImport opens a native file picker filtered to JSON and returns
// the chosen absolute path. Returns "" if the user cancels. The frontend
// uses this to drive a confirmation modal before calling ImportConfig.
func (a *App) OpenFileForImport() (string, error) {
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Import Jarvis config",
		Filters: []runtime.FileFilter{
			{DisplayName: "JSON", Pattern: "*.json"},
		},
	})
	if err != nil {
		return "", fmt.Errorf("OpenFileForImport: %w", err)
	}
	return path, nil
}

// ImportConfig reads the file at filePath, validates it parses as a
// config.Config (the package's UnmarshalJSON also handles legacy dex* keys
// for free — see TASK-032), and persists it as the new active config. If
// preserveAPIKeys is true, the imported config's secret-bearing fields are
// overridden with the values from the currently-active config so a user
// can import a stripped/sanitized file without losing their existing
// credentials.
//
// If the JSON is malformed, the active config is NOT overwritten — the
// error is returned and the caller (frontend) shows it as a toast.
func (a *App) ImportConfig(filePath string, preserveAPIKeys bool) error {
	if filePath == "" {
		return errors.New("ImportConfig: no file path provided")
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("ImportConfig: read %s: %w", filePath, err)
	}

	var imported config.Config
	if err := json.Unmarshal(data, &imported); err != nil {
		return fmt.Errorf("ImportConfig: invalid config JSON: %w", err)
	}

	if preserveAPIKeys {
		preserveSecrets(&imported, config.Get())
	}

	if err := config.Save(&imported); err != nil {
		return fmt.Errorf("ImportConfig: save: %w", err)
	}

	// No App-side cache to reload — config package's in-memory `current`
	// is updated by Save() itself. Subsequent config.Get() calls see the
	// new value.
	return nil
}

// ResetConfig wipes the config back to defaults. If preserveAPIKeys is
// true, retains all secret-bearing fields from the currently-active config
// so users can "factory reset" without losing API keys / tokens.
func (a *App) ResetConfig(preserveAPIKeys bool) error {
	fresh := config.DefaultConfig()
	if preserveAPIKeys {
		preserveSecrets(fresh, config.Get())
	}
	if err := config.Save(fresh); err != nil {
		return fmt.Errorf("ResetConfig: %w", err)
	}
	return nil
}

// preserveSecrets copies all secret-bearing / API-key fields from `src`
// (typically the current in-memory config) into `dst`. Used by Import
// (when the user opts to keep their existing keys instead of taking the
// imported file's values) and by Reset (when the user un-checks the
// "wipe API keys too" flow). Centralized here so adding a new secret
// field anywhere in config.Config has exactly one place to update.
func preserveSecrets(dst *config.Config, src *config.Config) {
	if dst == nil || src == nil {
		return
	}
	dst.JarvisAPIKey = src.JarvisAPIKey
	dst.JarvisPicovoiceKey = src.JarvisPicovoiceKey
	dst.JarvisElevenLabsKey = src.JarvisElevenLabsKey
	dst.LiveKitAPIKey = src.LiveKitAPIKey
	dst.LiveKitAPISecret = src.LiveKitAPISecret
	dst.MobileAPIToken = src.MobileAPIToken
}
