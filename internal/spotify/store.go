// Spotify credential storage — split out of internal/config in v0.3.0.
//
// Why a sibling file (~/.jarvis/spotify.json) rather than nesting under
// config.Config? The Wails TypeScript binding generator emits a
// `convertValues` method on any class whose Go struct holds a nested
// struct field. Adding `Spotify model.SpotifyConfig` to config.Config
// caused convertValues to appear on the generated Config class, which
// broke every Settings panel that constructed a config via spread
// (`{ ...cfg, foo: bar }`) — those plain object literals lack the new
// method.
//
// Storing creds in their own file mirrors the macctl Policy pattern
// already established in internal/macctl/policy.go and keeps config.json
// exportable without leaking OAuth tokens through ExportConfig.
package spotify

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/namanchopra/jarvis/internal/model"
	"github.com/namanchopra/jarvis/internal/paths"
)

// configFilename is the on-disk name under paths.JarvisHome().
const configFilename = "spotify.json"

// ConfigPath returns the canonical path Jarvis reads and writes Spotify
// credentials at — ~/.jarvis/spotify.json. Routed through internal/paths
// so HOME redirection in tests is honored.
func ConfigPath() string {
	return filepath.Join(paths.JarvisHome(), configFilename)
}

// LoadConfig reads and parses the persisted SpotifyConfig at path. Returns
// a zero-value config with nil error when the file is missing (first-run
// case) so callers can treat first launch and subsequent launches
// uniformly. Malformed JSON returns a non-nil error so a broken file isn't
// silently overwritten with defaults on the next Save.
func LoadConfig(path string) (model.SpotifyConfig, error) {
	if path == "" {
		return model.SpotifyConfig{}, errors.New("LoadConfig: path is required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return model.SpotifyConfig{}, nil
		}
		return model.SpotifyConfig{}, fmt.Errorf("LoadConfig: read %s: %w", path, err)
	}
	var cfg model.SpotifyConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return model.SpotifyConfig{}, fmt.Errorf("LoadConfig: parse %s: %w", path, err)
	}
	return cfg, nil
}

// SaveConfig writes cfg to path atomically (tmp + rename) so a crashed
// mid-write never leaves a half-written file. Parent directory is created
// if missing (0o700 — narrower than policy.json because this file holds
// OAuth refresh tokens). Resulting file mode is 0o600 for the same reason.
func SaveConfig(path string, cfg model.SpotifyConfig) error {
	if path == "" {
		return errors.New("SaveConfig: path is required")
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("SaveConfig: marshal: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("SaveConfig: mkdir %s: %w", dir, err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("SaveConfig: write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("SaveConfig: rename %s -> %s: %w", tmp, path, err)
	}
	return nil
}
