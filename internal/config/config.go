// Package config manages Jarvis's user-configurable settings.
// Settings are stored at ~/.jarvis/config.json and can be modified from the UI.
package config

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/namanchopra/jarvis/internal/paths"
)

// Config holds all user-configurable settings.
type Config struct {
	// DotClaudeSourcePath is the path to the .claude folder that should be
	// copied into workspaces. Typically points to a dotAiAgent repo's .claude/.
	// If empty, Jarvis auto-detects by scanning common locations.
	DotClaudeSourcePath string `json:"dotClaudeSourcePath"`

	// DefaultAgent is the default AI agent type for new sessions (e.g., "claude-code").
	DefaultAgent string `json:"defaultAgent"`

	// ScanIntervalSeconds is how often the session scanner runs (default 5).
	ScanIntervalSeconds int `json:"scanIntervalSeconds"`

	// PreferredTerminal overrides terminal auto-detection. Options: "cmux", "iterm2", "terminal".
	// If empty, Jarvis auto-detects all available terminals.
	PreferredTerminal string `json:"preferredTerminal"`

	// ProjectRootPaths are directories Jarvis scans for projects during discovery.
	// If empty, Jarvis uses common defaults (~/Desktop, ~/Projects, etc.).
	ProjectRootPaths []string `json:"projectRootPaths"`

	// NotificationsEnabled toggles all desktop notifications (default true).
	NotificationsEnabled bool `json:"notificationsEnabled"`

	// NotifyOnApproval sends a notification when a session needs user approval (default true).
	NotifyOnApproval bool `json:"notifyOnApproval"`

	// NotifyOnCompletion sends a notification when a session finishes (default true).
	NotifyOnCompletion bool `json:"notifyOnCompletion"`

	// CIWatchEnabled enables background monitoring of CI pipelines (default false).
	CIWatchEnabled bool `json:"ciWatchEnabled"`

	// CIProvider is the CI system to watch. Options: "github-actions", "gitlab-ci".
	// If empty, CI watch is disabled regardless of CIWatchEnabled.
	CIProvider string `json:"ciProvider"`

	// DefaultCommand is the CLI command used to launch agent sessions (default "claude").
	DefaultCommand string `json:"defaultCommand"`

	// MobileAPIPort is the TCP port the embedded mobile API server listens on.
	MobileAPIPort int `json:"mobileAPIPort"`

	// MobileAPIToken is the Bearer token required to authenticate mobile API requests.
	// If empty on startup, a token is auto-generated and persisted.
	MobileAPIToken string `json:"mobileAPIToken"`

	// Jarvis AI companion settings.
	JarvisEnabled         bool    `json:"jarvisEnabled"`
	JarvisProvider        string  `json:"jarvisProvider"` // "cli" (default, uses claude CLI) or "api" (uses Anthropic API key)
	JarvisAPIKey          string  `json:"jarvisAPIKey"`
	JarvisVoice           string  `json:"jarvisVoice"`
	JarvisAmbientEnabled  bool    `json:"jarvisAmbientEnabled"`
	JarvisVerbosity       string  `json:"jarvisVerbosity"`
	JarvisPicovoiceKey    string  `json:"jarvisPicovoiceKey"`
	JarvisWakeWordModel   string  `json:"jarvisWakeWordModel"`
	JarvisWakeSensitivity float32 `json:"jarvisWakeSensitivity"`
	JarvisElevenLabsKey   string  `json:"jarvisElevenLabsKey"`   // ElevenLabs API key for high-quality voice
	JarvisElevenLabsVoice string  `json:"jarvisElevenLabsVoice"` // ElevenLabs voice ID (empty = default Daniel)

	// LiveKit voice transport settings (spike — opt-in via UseLiveKitTransport).
	// When enabled, the daemon joins a LiveKit room as audio I/O instead of
	// using the local Mac mic+speaker. Mobile clients connect to the same
	// room to talk to Jarvis from anywhere over Tailscale or LiveKit Cloud.
	UseLiveKitTransport bool   `json:"useLiveKitTransport"`
	LiveKitURL          string `json:"livekitUrl"`          // wss://<project>.livekit.cloud or self-host URL
	LiveKitAPIKey       string `json:"livekitApiKey"`       // LiveKit API key (used to mint tokens server-side)
	LiveKitAPISecret    string `json:"livekitApiSecret"`    // LiveKit API secret (server-only, never sent to clients)
	LiveKitRoomName     string `json:"livekitRoomName"`     // Default room (e.g. "jarvis")
}

// UnmarshalJSON reads jarvis* keys preferentially. If a jarvis* key is absent
// but its legacy dex* counterpart is present, the legacy value is used.
// This provides one-release backward compatibility for existing installs that
// have dex*-keyed config files. After the next Save(), only jarvis* keys are
// written, completing the migration.
func (c *Config) UnmarshalJSON(data []byte) error {
	// Shadow type avoids infinite recursion. Embeds Config (via Alias) to
	// inherit the new jarvis* tags, plus explicit pointer fields with legacy
	// dex* tags so we can distinguish "key absent" (nil) from "key set to
	// zero value" (non-nil pointer to zero).
	type Alias Config
	shadow := struct {
		*Alias
		LegacyEnabled         *bool    `json:"dexEnabled"`
		LegacyProvider        *string  `json:"dexProvider"`
		LegacyAPIKey          *string  `json:"dexAPIKey"`
		LegacyVoice           *string  `json:"dexVoice"`
		LegacyAmbientEnabled  *bool    `json:"dexAmbientEnabled"`
		LegacyVerbosity       *string  `json:"dexVerbosity"`
		LegacyPicovoiceKey    *string  `json:"dexPicovoiceKey"`
		LegacyWakeWordModel   *string  `json:"dexWakeWordModel"`
		LegacyWakeSensitivity *float32 `json:"dexWakeSensitivity"`
		LegacyElevenLabsKey   *string  `json:"dexElevenLabsKey"`
		LegacyElevenLabsVoice *string  `json:"dexElevenLabsVoice"`
	}{Alias: (*Alias)(c)}

	if err := json.Unmarshal(data, &shadow); err != nil {
		return err
	}

	// For each pair, if the jarvis* field has its zero value AND a legacy
	// field is present in the JSON (non-nil pointer), use the legacy value.
	if !c.JarvisEnabled && shadow.LegacyEnabled != nil {
		c.JarvisEnabled = *shadow.LegacyEnabled
	}
	if c.JarvisProvider == "" && shadow.LegacyProvider != nil {
		c.JarvisProvider = *shadow.LegacyProvider
	}
	if c.JarvisAPIKey == "" && shadow.LegacyAPIKey != nil {
		c.JarvisAPIKey = *shadow.LegacyAPIKey
	}
	if c.JarvisVoice == "" && shadow.LegacyVoice != nil {
		c.JarvisVoice = *shadow.LegacyVoice
	}
	if !c.JarvisAmbientEnabled && shadow.LegacyAmbientEnabled != nil {
		c.JarvisAmbientEnabled = *shadow.LegacyAmbientEnabled
	}
	if c.JarvisVerbosity == "" && shadow.LegacyVerbosity != nil {
		c.JarvisVerbosity = *shadow.LegacyVerbosity
	}
	if c.JarvisPicovoiceKey == "" && shadow.LegacyPicovoiceKey != nil {
		c.JarvisPicovoiceKey = *shadow.LegacyPicovoiceKey
	}
	if c.JarvisWakeWordModel == "" && shadow.LegacyWakeWordModel != nil {
		c.JarvisWakeWordModel = *shadow.LegacyWakeWordModel
	}
	if c.JarvisWakeSensitivity == 0 && shadow.LegacyWakeSensitivity != nil {
		c.JarvisWakeSensitivity = *shadow.LegacyWakeSensitivity
	}
	if c.JarvisElevenLabsKey == "" && shadow.LegacyElevenLabsKey != nil {
		c.JarvisElevenLabsKey = *shadow.LegacyElevenLabsKey
	}
	if c.JarvisElevenLabsVoice == "" && shadow.LegacyElevenLabsVoice != nil {
		c.JarvisElevenLabsVoice = *shadow.LegacyElevenLabsVoice
	}

	return nil
}

var (
	current *Config
	mu      sync.RWMutex
)

// DefaultConfig returns the config with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		DotClaudeSourcePath:  "",
		DefaultAgent:         "claude-code",
		ScanIntervalSeconds:  5,
		PreferredTerminal:    "",
		ProjectRootPaths:     nil,
		NotificationsEnabled: true,
		NotifyOnApproval:     true,
		NotifyOnCompletion:   true,
		CIWatchEnabled:       false,
		CIProvider:           "",
		DefaultCommand:       "claude",
		MobileAPIPort:        4422,
		MobileAPIToken:       "",

		JarvisEnabled:         true,
		JarvisAmbientEnabled:  true,
		JarvisVoice:           "Daniel",
		JarvisVerbosity:       "concise",
		JarvisWakeSensitivity: 0.5,
	}
}

func configPath() string {
	return paths.ConfigPath()
}

// Load reads the config from disk. If the file doesn't exist, returns defaults.
func Load() (*Config, error) {
	mu.Lock()
	defer mu.Unlock()

	cfg := DefaultConfig()

	data, err := os.ReadFile(configPath())
	if err != nil {
		if os.IsNotExist(err) {
			current = cfg
			return cfg, nil
		}
		return nil, err
	}

	if err := json.Unmarshal(data, cfg); err != nil {
		slog.Warn("config file corrupt, using defaults", "path", configPath(), "err", err)
		current = DefaultConfig()
		return current, nil
	}

	// Apply defaults for zero values.
	if cfg.ScanIntervalSeconds <= 0 {
		cfg.ScanIntervalSeconds = 5
	}
	if cfg.DefaultAgent == "" {
		cfg.DefaultAgent = "claude-code"
	}
	if cfg.DefaultCommand == "" {
		cfg.DefaultCommand = "claude"
	}
	if cfg.MobileAPIPort <= 0 {
		cfg.MobileAPIPort = 4422
	}

	current = cfg
	return cfg, nil
}

// Save persists the config to disk.
func Save(cfg *Config) error {
	mu.Lock()
	defer mu.Unlock()

	dir := filepath.Dir(configPath())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	current = cfg
	return os.WriteFile(configPath(), data, 0o644)
}

// Get returns a copy of the current in-memory config. The caller may read or
// modify the returned value without racing against concurrent Load/Save calls.
// Call Load() at least once before using Get().
func Get() *Config {
	mu.RLock()
	defer mu.RUnlock()
	if current == nil {
		return DefaultConfig()
	}
	cp := *current
	return &cp
}
