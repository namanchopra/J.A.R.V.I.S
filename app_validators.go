// app_validators.go — Wails bindings for TASK-017 (API key validation) and
// TASK-018 (LLM provider availability checks).
//
// Each ValidateAPIKey call makes the cheapest possible authenticated request
// against the provider's API and reports whether the key is accepted.
// IsOllamaRunning is a 1-second probe against the local Ollama HTTP server,
// used by the LLM dropdown to mark the ollama:qwen3:4b option as available.
//
// SECURITY CONTRACT (TASK-017 acceptance criterion):
// The key value MUST NEVER be written to any log surface (slog, stderr,
// structured logs, stdout). Only the provider name and the outcome
// (valid/invalid + sanitized provider error message) may be logged.
// The helpers in this file deliberately avoid `slog`/`fmt.Printf(key)` —
// all observability is provider-name + status only.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// APIKeyValidationResult is what ValidateAPIKey returns to the frontend.
// Fields are JSON-tagged so Wails serialises them with stable lowercase keys
// the React panel can read directly.
type APIKeyValidationResult struct {
	// Valid is true iff the provider accepted the key.
	Valid bool `json:"valid"`
	// Error is a short human-readable reason when Valid==false. Empty when
	// Valid==true. NEVER contains the key itself.
	Error string `json:"error,omitempty"`
}

// ValidateAPIKey makes a 1-token (or equivalently cheap) authenticated test
// request against the named provider's API and reports whether the key is
// accepted.
//
// Supported providers: "openrouter", "google", "anthropic", "cartesia",
// "elevenlabs", "picovoice". Provider matching is case-insensitive.
//
// Behaviour:
//   - Empty/whitespace key → {Valid:false, Error:"key is empty"}
//   - Unknown provider     → {Valid:false, Error:"unknown provider: …"}
//   - Network failure      → {Valid:false, Error:"network error: …"}
//   - HTTP 2xx             → {Valid:true}
//   - HTTP 4xx/5xx         → {Valid:false, Error:<provider message or HTTP NNN>}
//
// SECURITY: this function MUST NEVER log the key value at any level.
// Verified by the TASK-017 acceptance criterion (grep ~/.jarvis/logs/ and
// daemon stderr for the test key — must return zero matches).
func (a *App) ValidateAPIKey(provider, key string) APIKeyValidationResult {
	key = strings.TrimSpace(key)
	if key == "" {
		return APIKeyValidationResult{Valid: false, Error: "key is empty"}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "openrouter":
		return validateOpenRouterKey(ctx, key)
	case "google":
		return validateGoogleKey(ctx, key)
	case "anthropic":
		return validateAnthropicKey(ctx, key)
	case "cartesia":
		return validateCartesiaKey(ctx, key)
	case "elevenlabs":
		return validateElevenLabsKey(ctx, key)
	case "picovoice":
		return validatePicovoiceKey(ctx, key)
	default:
		return APIKeyValidationResult{Valid: false, Error: "unknown provider: " + provider}
	}
}

// IsOllamaRunning returns true if a local Ollama server is reachable at
// http://localhost:11434/api/tags within 1 second. Used by the LLM dropdown
// in ConnectionsPanel to mark the qwen3:4b local option as available.
//
// We deliberately use a short timeout — this binding is called on panel
// mount and should not delay first paint.
func (a *App) IsOllamaRunning() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost:11434/api/tags", nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode == http.StatusOK
}

// ---------------------------------------------------------------------------
// Per-provider validators.
//
// Each builds the cheapest possible authenticated request and delegates
// response interpretation to doValidation (HTTP 2xx → valid, anything else →
// invalid with the provider's own message extracted when possible).
//
// Endpoint choices:
//   openrouter:  GET /api/v1/auth/key                      → 200 iff valid
//   google:      GET /v1beta/models?key=K (top-1 page)     → 200 iff valid
//   anthropic:   POST /v1/messages, max_tokens=1           → 200 iff valid
//   cartesia:    GET /voices (no body, smallest list call) → 200 iff valid
//   elevenlabs:  GET /v1/user                              → 200 iff valid
//   picovoice:   GET /v1/voice/key/info                    → 200 iff valid
// ---------------------------------------------------------------------------

func validateOpenRouterKey(ctx context.Context, key string) APIKeyValidationResult {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://openrouter.ai/api/v1/auth/key", nil)
	if err != nil {
		return APIKeyValidationResult{Valid: false, Error: fmt.Sprintf("request build failed: %s", err)}
	}
	req.Header.Set("Authorization", "Bearer "+key)
	return doValidation("openrouter", req)
}

func validateGoogleKey(ctx context.Context, key string) APIKeyValidationResult {
	// Google AI Studio (Generative Language API) takes the key via the
	// `key=` query parameter. Listing models is a no-quota / very low-cost
	// endpoint and only succeeds when the key is valid + enabled.
	url := "https://generativelanguage.googleapis.com/v1beta/models?pageSize=1&key=" + key
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return APIKeyValidationResult{Valid: false, Error: fmt.Sprintf("request build failed: %s", err)}
	}
	return doValidation("google", req)
}

func validateAnthropicKey(ctx context.Context, key string) APIKeyValidationResult {
	// Smallest possible Anthropic call: a 1-token message with the cheapest
	// Haiku-class model. We use the public messages endpoint with
	// max_tokens=1 — Anthropic returns 200 on success or 401/4xx on a bad
	// key, and the body's `error.message` is propagated by doValidation.
	body := []byte(`{"model":"claude-haiku-4-5","max_tokens":1,"messages":[{"role":"user","content":"ping"}]}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	if err != nil {
		return APIKeyValidationResult{Valid: false, Error: fmt.Sprintf("request build failed: %s", err)}
	}
	req.Header.Set("x-api-key", key)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")
	return doValidation("anthropic", req)
}

func validateCartesiaKey(ctx context.Context, key string) APIKeyValidationResult {
	// Cartesia voices list is the cheapest authenticated GET. Requires the
	// X-API-Key header and a Cartesia-Version pinned date.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.cartesia.ai/voices/", nil)
	if err != nil {
		return APIKeyValidationResult{Valid: false, Error: fmt.Sprintf("request build failed: %s", err)}
	}
	req.Header.Set("X-API-Key", key)
	req.Header.Set("Cartesia-Version", "2024-06-10")
	return doValidation("cartesia", req)
}

func validateElevenLabsKey(ctx context.Context, key string) APIKeyValidationResult {
	// ElevenLabs /v1/user is the canonical "is this key live" endpoint.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.elevenlabs.io/v1/user", nil)
	if err != nil {
		return APIKeyValidationResult{Valid: false, Error: fmt.Sprintf("request build failed: %s", err)}
	}
	req.Header.Set("xi-api-key", key)
	return doValidation("elevenlabs", req)
}

func validatePicovoiceKey(ctx context.Context, key string) APIKeyValidationResult {
	// Picovoice doesn't expose a thin public auth-check endpoint. The
	// closest equivalent is the license/account info endpoint, which
	// returns 200 for a valid AccessKey and 401/403 otherwise.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.picovoice.ai/v1/voice/key/info", nil)
	if err != nil {
		return APIKeyValidationResult{Valid: false, Error: fmt.Sprintf("request build failed: %s", err)}
	}
	req.Header.Set("Authorization", key)
	return doValidation("picovoice", req)
}

// doValidation executes req, interprets the response, and returns a
// canonical APIKeyValidationResult. The provider name is only used for
// observability (currently no log line is emitted — see SECURITY contract
// at top of file). When the provider returns a JSON error body, we try to
// surface its `error.message` so the user sees "invalid_api_key" etc.
func doValidation(provider string, req *http.Request) APIKeyValidationResult {
	_ = provider // intentionally unused — kept for future structured logging
	// of provider-name-only outcomes. Do not add a log line that includes
	// the request's auth header value.

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return APIKeyValidationResult{Valid: false, Error: fmt.Sprintf("network error: %s", err)}
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return APIKeyValidationResult{Valid: true}
	}

	// Try to extract a useful error message from the JSON body. Several
	// providers use {"error":{"message":"..."}}; Google and a few others
	// nest the message slightly differently — we accept either shape.
	msg := fmt.Sprintf("HTTP %d", resp.StatusCode)
	var nested struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &nested) == nil && nested.Error.Message != "" {
		msg = nested.Error.Message
	} else {
		var flat struct {
			Detail  string `json:"detail"`
			Message string `json:"message"`
		}
		if json.Unmarshal(body, &flat) == nil {
			if flat.Message != "" {
				msg = flat.Message
			} else if flat.Detail != "" {
				msg = flat.Detail
			}
		}
	}
	return APIKeyValidationResult{Valid: false, Error: msg}
}
