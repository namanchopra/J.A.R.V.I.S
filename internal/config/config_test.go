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

// TestConfigRoundTripV012Fields asserts that the 8 voice/API-key fields
// promoted in v0.1.2 (ttsProvider, sttModel, voicePreset, micInputDevice,
// wakeWordEnabled, googleAPIKey, anthropicAPIKey, cartesiaAPIKey) marshal to
// JSON and unmarshal back into an equivalent Config with all values intact.
// This is the v0.1.2 acceptance: "SaveConfig actually persists them".
func TestConfigRoundTripV012Fields(t *testing.T) {
	wakeOn := true

	orig := Config{
		TtsProvider:     "vibevoice",
		SttModel:        "whisper-small.en",
		VoicePreset:     "en-Carter_man",
		MicInputDevice:  "AppleHDA:1",
		WakeWordEnabled: &wakeOn,
		GoogleAPIKey:    "google-key-123",
		AnthropicAPIKey: "sk-ant-anthropic-456",
		CartesiaAPIKey:  "cartesia-key-789",
	}

	data, err := json.Marshal(&orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got Config
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.TtsProvider != orig.TtsProvider {
		t.Errorf("TtsProvider: got %q, want %q", got.TtsProvider, orig.TtsProvider)
	}
	if got.SttModel != orig.SttModel {
		t.Errorf("SttModel: got %q, want %q", got.SttModel, orig.SttModel)
	}
	if got.VoicePreset != orig.VoicePreset {
		t.Errorf("VoicePreset: got %q, want %q", got.VoicePreset, orig.VoicePreset)
	}
	if got.MicInputDevice != orig.MicInputDevice {
		t.Errorf("MicInputDevice: got %q, want %q", got.MicInputDevice, orig.MicInputDevice)
	}
	if got.WakeWordEnabled == nil {
		t.Errorf("WakeWordEnabled: got nil, want pointer to true")
	} else if *got.WakeWordEnabled != true {
		t.Errorf("WakeWordEnabled: got %v, want true", *got.WakeWordEnabled)
	}
	if got.GoogleAPIKey != orig.GoogleAPIKey {
		t.Errorf("GoogleAPIKey: got %q, want %q", got.GoogleAPIKey, orig.GoogleAPIKey)
	}
	if got.AnthropicAPIKey != orig.AnthropicAPIKey {
		t.Errorf("AnthropicAPIKey: got %q, want %q", got.AnthropicAPIKey, orig.AnthropicAPIKey)
	}
	if got.CartesiaAPIKey != orig.CartesiaAPIKey {
		t.Errorf("CartesiaAPIKey: got %q, want %q", got.CartesiaAPIKey, orig.CartesiaAPIKey)
	}

	// Sanity-check that the JSON contains the expected lowercase camelCase keys.
	outStr := string(data)
	for _, want := range []string{
		`"ttsProvider":"vibevoice"`,
		`"sttModel":"whisper-small.en"`,
		`"voicePreset":"en-Carter_man"`,
		`"micInputDevice":"AppleHDA:1"`,
		`"wakeWordEnabled":true`,
		`"googleAPIKey":"google-key-123"`,
		`"anthropicAPIKey":"sk-ant-anthropic-456"`,
		`"cartesiaAPIKey":"cartesia-key-789"`,
	} {
		if !strings.Contains(outStr, want) {
			t.Errorf("marshaled JSON missing %q:\n%s", want, outStr)
		}
	}
}

// TestConfigRoundTripWakeWordExplicitlyFalse verifies the tri-state pointer
// pattern for WakeWordEnabled: explicitly setting it to false must
// round-trip as a non-nil pointer to false (distinguishable from "unset").
func TestConfigRoundTripWakeWordExplicitlyFalse(t *testing.T) {
	wakeOff := false
	orig := Config{WakeWordEnabled: &wakeOff}

	data, err := json.Marshal(&orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), `"wakeWordEnabled":false`) {
		t.Errorf("marshaled JSON missing wakeWordEnabled:false:\n%s", string(data))
	}

	var got Config
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.WakeWordEnabled == nil {
		t.Fatalf("WakeWordEnabled: got nil, want pointer to false")
	}
	if *got.WakeWordEnabled != false {
		t.Errorf("WakeWordEnabled: got %v, want false", *got.WakeWordEnabled)
	}
}

// TestConfigBackwardCompatUnmarshalsOldShape verifies that an old config
// file from v0.1.1 or earlier (which has none of the 8 new v0.1.2 fields)
// unmarshals cleanly with all new fields at their zero values. This is
// the backward-compat acceptance: "old configs without these fields must
// continue to load".
func TestConfigBackwardCompatUnmarshalsOldShape(t *testing.T) {
	oldJSON := `{
		"defaultAgent": "claude-code",
		"scanIntervalSeconds": 5,
		"defaultCommand": "claude",
		"mobileAPIPort": 4422,
		"jarvisEnabled": true,
		"jarvisAPIKey": "sk-ant-old-key",
		"jarvisVoice": "Daniel"
	}`

	var cfg Config
	if err := json.Unmarshal([]byte(oldJSON), &cfg); err != nil {
		t.Fatalf("unmarshal old-shape config: %v", err)
	}

	// Existing fields preserved.
	if cfg.DefaultAgent != "claude-code" {
		t.Errorf("DefaultAgent: got %q, want %q", cfg.DefaultAgent, "claude-code")
	}
	if cfg.JarvisAPIKey != "sk-ant-old-key" {
		t.Errorf("JarvisAPIKey: got %q, want %q", cfg.JarvisAPIKey, "sk-ant-old-key")
	}

	// New v0.1.2 fields default to zero values.
	if cfg.TtsProvider != "" {
		t.Errorf("TtsProvider: got %q, want empty (unset in old config)", cfg.TtsProvider)
	}
	if cfg.SttModel != "" {
		t.Errorf("SttModel: got %q, want empty (unset in old config)", cfg.SttModel)
	}
	if cfg.VoicePreset != "" {
		t.Errorf("VoicePreset: got %q, want empty (unset in old config)", cfg.VoicePreset)
	}
	if cfg.MicInputDevice != "" {
		t.Errorf("MicInputDevice: got %q, want empty (unset in old config)", cfg.MicInputDevice)
	}
	if cfg.WakeWordEnabled != nil {
		t.Errorf("WakeWordEnabled: got pointer to %v, want nil (unset in old config)", *cfg.WakeWordEnabled)
	}
	if cfg.GoogleAPIKey != "" {
		t.Errorf("GoogleAPIKey: got %q, want empty (unset in old config)", cfg.GoogleAPIKey)
	}
	if cfg.AnthropicAPIKey != "" {
		t.Errorf("AnthropicAPIKey: got %q, want empty (unset in old config)", cfg.AnthropicAPIKey)
	}
	if cfg.CartesiaAPIKey != "" {
		t.Errorf("CartesiaAPIKey: got %q, want empty (unset in old config)", cfg.CartesiaAPIKey)
	}

	// Re-marshal must omit the unset v0.1.2 keys (omitempty contract).
	out, err := json.Marshal(&cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	outStr := string(out)
	for _, key := range []string{
		"ttsProvider",
		"sttModel",
		"voicePreset",
		"micInputDevice",
		"wakeWordEnabled",
		"googleAPIKey",
		"anthropicAPIKey",
		"cartesiaAPIKey",
	} {
		if strings.Contains(outStr, `"`+key+`"`) {
			t.Errorf("marshaled JSON should omit unset v0.1.2 key %q:\n%s", key, outStr)
		}
	}
}

// TestConfigRoundTripLlmModel asserts that the v0.1.5 LlmModel field marshals
// to JSON and unmarshals back into an equivalent Config with the value intact.
// This is the v0.1.5 acceptance for the LLM-model-dropdown bug: "SaveConfig
// actually persists the selected LLM model so it survives reloads".
func TestConfigRoundTripLlmModel(t *testing.T) {
	cases := []string{
		"google/gemini-2.5-flash",
		"anthropic/claude-haiku-4-5",
		"openai/gpt-4o-mini",
		"ollama:qwen3:4b",
	}

	for _, want := range cases {
		t.Run(want, func(t *testing.T) {
			orig := Config{LlmModel: want}

			data, err := json.Marshal(&orig)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}

			var got Config
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}

			if got.LlmModel != orig.LlmModel {
				t.Errorf("LlmModel: got %q, want %q", got.LlmModel, orig.LlmModel)
			}

			// Sanity-check the on-the-wire camelCase key.
			outStr := string(data)
			wantSub := `"llmModel":"` + want + `"`
			if !strings.Contains(outStr, wantSub) {
				t.Errorf("marshaled JSON missing %q:\n%s", wantSub, outStr)
			}
		})
	}
}

// TestConfigBackwardCompatNoLlmModel verifies that an old config file from
// v0.1.4 or earlier (which has no llmModel field) unmarshals cleanly with
// LlmModel at its zero value, and that re-marshaling a cleared LlmModel
// omits the key entirely (omitempty contract).
func TestConfigBackwardCompatNoLlmModel(t *testing.T) {
	oldJSON := `{
		"defaultAgent": "claude-code",
		"scanIntervalSeconds": 5,
		"defaultCommand": "claude",
		"mobileAPIPort": 4422,
		"jarvisEnabled": true,
		"ttsProvider": "vibevoice",
		"sttModel": "whisper-small.en"
	}`

	var cfg Config
	if err := json.Unmarshal([]byte(oldJSON), &cfg); err != nil {
		t.Fatalf("unmarshal old-shape config: %v", err)
	}

	// Existing fields preserved.
	if cfg.DefaultAgent != "claude-code" {
		t.Errorf("DefaultAgent: got %q, want %q", cfg.DefaultAgent, "claude-code")
	}
	if cfg.TtsProvider != "vibevoice" {
		t.Errorf("TtsProvider: got %q, want %q", cfg.TtsProvider, "vibevoice")
	}

	// New v0.1.5 field defaults to empty (= use legacy key-driven detection).
	if cfg.LlmModel != "" {
		t.Errorf("LlmModel: got %q, want empty (unset in old config)", cfg.LlmModel)
	}

	// Re-marshal must omit the unset llmModel key.
	out, err := json.Marshal(&cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(out), `"llmModel"`) {
		t.Errorf("marshaled JSON should omit unset llmModel key:\n%s", string(out))
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

// Spotify config round-trip / default / absent-key tests moved to
// internal/spotify/store_test.go in v0.3.0 when Spotify creds were split
// out of config.Config into ~/.jarvis/spotify.json. See the package doc
// in internal/spotify/store.go for the rationale.
