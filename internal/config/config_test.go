package config

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestUnmarshalLegacyDexKeys verifies that a config file containing only the
// legacy dex* JSON keys loads successfully and populates the canonical jarvis*
// Go fields. This is the Phase-1-era config file path (TASK-032 acceptance:
// "legacy-only file loads with values populated").
func TestUnmarshalLegacyDexKeys(t *testing.T) {
	legacyJSON := `{
		"dexEnabled": true,
		"dexProvider": "api",
		"dexAPIKey": "sk-ant-legacy-key",
		"dexVoice": "Daniel",
		"dexAmbientEnabled": true,
		"dexVerbosity": "verbose",
		"dexPicovoiceKey": "pv-legacy",
		"dexWakeWordModel": "/path/to/jarvis.ppn",
		"dexWakeSensitivity": 0.75,
		"dexElevenLabsKey": "el-legacy",
		"dexElevenLabsVoice": "voice-id-legacy"
	}`

	var cfg Config
	if err := json.Unmarshal([]byte(legacyJSON), &cfg); err != nil {
		t.Fatalf("unmarshal legacy config: %v", err)
	}

	if !cfg.JarvisEnabled {
		t.Errorf("JarvisEnabled: got false, want true (from dexEnabled)")
	}
	if cfg.JarvisProvider != "api" {
		t.Errorf("JarvisProvider: got %q, want %q (from dexProvider)", cfg.JarvisProvider, "api")
	}
	if cfg.JarvisAPIKey != "sk-ant-legacy-key" {
		t.Errorf("JarvisAPIKey: got %q, want %q (from dexAPIKey)", cfg.JarvisAPIKey, "sk-ant-legacy-key")
	}
	if cfg.JarvisVoice != "Daniel" {
		t.Errorf("JarvisVoice: got %q, want %q (from dexVoice)", cfg.JarvisVoice, "Daniel")
	}
	if !cfg.JarvisAmbientEnabled {
		t.Errorf("JarvisAmbientEnabled: got false, want true (from dexAmbientEnabled)")
	}
	if cfg.JarvisVerbosity != "verbose" {
		t.Errorf("JarvisVerbosity: got %q, want %q (from dexVerbosity)", cfg.JarvisVerbosity, "verbose")
	}
	if cfg.JarvisPicovoiceKey != "pv-legacy" {
		t.Errorf("JarvisPicovoiceKey: got %q, want %q (from dexPicovoiceKey)", cfg.JarvisPicovoiceKey, "pv-legacy")
	}
	if cfg.JarvisWakeWordModel != "/path/to/jarvis.ppn" {
		t.Errorf("JarvisWakeWordModel: got %q, want %q (from dexWakeWordModel)", cfg.JarvisWakeWordModel, "/path/to/jarvis.ppn")
	}
	if cfg.JarvisWakeSensitivity != 0.75 {
		t.Errorf("JarvisWakeSensitivity: got %v, want %v (from dexWakeSensitivity)", cfg.JarvisWakeSensitivity, 0.75)
	}
	if cfg.JarvisElevenLabsKey != "el-legacy" {
		t.Errorf("JarvisElevenLabsKey: got %q, want %q (from dexElevenLabsKey)", cfg.JarvisElevenLabsKey, "el-legacy")
	}
	if cfg.JarvisElevenLabsVoice != "voice-id-legacy" {
		t.Errorf("JarvisElevenLabsVoice: got %q, want %q (from dexElevenLabsVoice)", cfg.JarvisElevenLabsVoice, "voice-id-legacy")
	}
}

// TestUnmarshalMixedKeysPrefersJarvis verifies that when BOTH legacy dex* and
// canonical jarvis* keys are present in the JSON, the jarvis* value wins.
// This is the upgrade-in-progress scenario (TASK-032 acceptance: "mixed file
// prefers jarvis*").
func TestUnmarshalMixedKeysPrefersJarvis(t *testing.T) {
	mixedJSON := `{
		"dexAPIKey": "sk-ant-legacy",
		"jarvisAPIKey": "sk-ant-new",
		"dexEnabled": false,
		"jarvisEnabled": true,
		"dexVoice": "Daniel",
		"jarvisVoice": "Rachel",
		"dexWakeSensitivity": 0.25,
		"jarvisWakeSensitivity": 0.9
	}`

	var cfg Config
	if err := json.Unmarshal([]byte(mixedJSON), &cfg); err != nil {
		t.Fatalf("unmarshal mixed config: %v", err)
	}

	if cfg.JarvisAPIKey != "sk-ant-new" {
		t.Errorf("JarvisAPIKey: got %q, want %q (jarvis* should win over dex*)", cfg.JarvisAPIKey, "sk-ant-new")
	}
	if !cfg.JarvisEnabled {
		t.Errorf("JarvisEnabled: got false, want true (jarvis* should win over dex*)")
	}
	if cfg.JarvisVoice != "Rachel" {
		t.Errorf("JarvisVoice: got %q, want %q (jarvis* should win over dex*)", cfg.JarvisVoice, "Rachel")
	}
	if cfg.JarvisWakeSensitivity != 0.9 {
		t.Errorf("JarvisWakeSensitivity: got %v, want %v (jarvis* should win over dex*)", cfg.JarvisWakeSensitivity, 0.9)
	}
}

// TestRoundTripLegacyDropsDexKeys verifies that after loading a legacy dex*
// config and re-marshaling it, the resulting JSON contains ONLY jarvis* keys
// (no dex* entries). This proves the migration is one-way: legacy keys are
// silently dropped on first save. (TASK-032 acceptance: "round-trip a legacy
// file → save → assert no dex* keys remain".)
func TestRoundTripLegacyDropsDexKeys(t *testing.T) {
	legacyJSON := `{
		"dexEnabled": true,
		"dexAPIKey": "sk-ant-legacy",
		"dexProvider": "api",
		"dexVoice": "Daniel",
		"dexAmbientEnabled": true,
		"dexVerbosity": "concise",
		"dexPicovoiceKey": "pv-legacy",
		"dexWakeWordModel": "/path/to/model.ppn",
		"dexWakeSensitivity": 0.5,
		"dexElevenLabsKey": "el-legacy",
		"dexElevenLabsVoice": "voice-legacy"
	}`

	var cfg Config
	if err := json.Unmarshal([]byte(legacyJSON), &cfg); err != nil {
		t.Fatalf("unmarshal legacy config: %v", err)
	}

	out, err := json.MarshalIndent(&cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}

	outStr := string(out)

	// No legacy dex* keys may appear in the marshaled output.
	legacyKeys := []string{
		"dexEnabled", "dexProvider", "dexAPIKey", "dexVoice",
		"dexAmbientEnabled", "dexVerbosity", "dexPicovoiceKey",
		"dexWakeWordModel", "dexWakeSensitivity", "dexElevenLabsKey",
		"dexElevenLabsVoice",
	}
	for _, key := range legacyKeys {
		if strings.Contains(outStr, `"`+key+`"`) {
			t.Errorf("marshaled JSON still contains legacy key %q:\n%s", key, outStr)
		}
	}

	// And the canonical jarvis* keys carry the legacy values forward.
	wantSubstrings := []string{
		`"jarvisEnabled": true`,
		`"jarvisAPIKey": "sk-ant-legacy"`,
		`"jarvisProvider": "api"`,
		`"jarvisVoice": "Daniel"`,
	}
	for _, sub := range wantSubstrings {
		if !strings.Contains(outStr, sub) {
			t.Errorf("marshaled JSON missing expected substring %q:\n%s", sub, outStr)
		}
	}
}

// TestDefaultConfigNoLiveKit verifies TASK-001 acceptance: a fresh install's
// default-generated config does NOT enable LiveKit and does NOT contain any
// LiveKit credential placeholders. The daemon should pick LocalAudioTransport.
func TestDefaultConfigNoLiveKit(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.UseLiveKitTransport {
		t.Errorf("DefaultConfig().UseLiveKitTransport = true; want false")
	}
	if cfg.LiveKitURL != "" {
		t.Errorf("DefaultConfig().LiveKitURL = %q; want empty", cfg.LiveKitURL)
	}
	if cfg.LiveKitAPIKey != "" {
		t.Errorf("DefaultConfig().LiveKitAPIKey = %q; want empty", cfg.LiveKitAPIKey)
	}
	if cfg.LiveKitAPISecret != "" {
		t.Errorf("DefaultConfig().LiveKitAPISecret = %q; want empty", cfg.LiveKitAPISecret)
	}

	// The marshaled default config must not contain any LiveKit keys
	// (omitempty drops them when zero-valued).
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal default config: %v", err)
	}
	outStr := string(out)
	for _, key := range []string{
		"useLiveKitTransport",
		"livekitUrl",
		"livekitApiKey",
		"livekitApiSecret",
		"livekitRoomName",
	} {
		if strings.Contains(outStr, `"`+key+`"`) {
			t.Errorf("default config JSON contains LiveKit key %q (should be omitted):\n%s", key, outStr)
		}
	}
}

// TestExistingLiveKitConfigPreserved verifies TASK-001 acceptance: an existing
// user who has useLiveKitTransport=true with credentials is NOT downgraded by
// the load path. Their settings round-trip through unmarshal+marshal intact.
func TestExistingLiveKitConfigPreserved(t *testing.T) {
	existingJSON := `{
		"useLiveKitTransport": true,
		"livekitUrl": "wss://example.livekit.cloud",
		"livekitApiKey": "lk-api-key",
		"livekitApiSecret": "lk-api-secret",
		"livekitRoomName": "jarvis"
	}`

	cfg := DefaultConfig()
	if err := json.Unmarshal([]byte(existingJSON), cfg); err != nil {
		t.Fatalf("unmarshal existing LiveKit config: %v", err)
	}

	if !cfg.UseLiveKitTransport {
		t.Errorf("UseLiveKitTransport: got false, want true (existing user should not be downgraded)")
	}
	if cfg.LiveKitURL != "wss://example.livekit.cloud" {
		t.Errorf("LiveKitURL: got %q, want %q", cfg.LiveKitURL, "wss://example.livekit.cloud")
	}
	if cfg.LiveKitAPIKey != "lk-api-key" {
		t.Errorf("LiveKitAPIKey: got %q, want %q", cfg.LiveKitAPIKey, "lk-api-key")
	}
	if cfg.LiveKitAPISecret != "lk-api-secret" {
		t.Errorf("LiveKitAPISecret: got %q, want %q", cfg.LiveKitAPISecret, "lk-api-secret")
	}

	// Round-trip: marshaling must preserve the non-empty LiveKit keys.
	out, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	outStr := string(out)
	if !strings.Contains(outStr, `"useLiveKitTransport":true`) {
		t.Errorf("marshaled JSON missing useLiveKitTransport:true:\n%s", outStr)
	}
	if !strings.Contains(outStr, `"livekitUrl":"wss://example.livekit.cloud"`) {
		t.Errorf("marshaled JSON missing livekitUrl:\n%s", outStr)
	}
}
