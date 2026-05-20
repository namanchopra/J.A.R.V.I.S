package main

import (
	"net"
	"strings"
	"testing"

	"github.com/namanchopra/jarvis/internal/config"
)

// ---------------------------------------------------------------------------
// app_pairing_test.go — TASK-025 acceptance tests.
//
// Coverage:
//   - localLANIP() returns either a real IP or 127.0.0.1 — never an error
//     under normal conditions, never the empty string.
//   - GenerateMobilePairingQR() with an empty MobileAPIToken returns a
//     human-readable error mentioning "token" so the React side can surface
//     a "regenerate token" CTA.
//   - GenerateMobilePairingQR() with a valid token returns a `data:image/
//     png;base64,...` data URL — the prefix is the contract the modal
//     relies on for <img src=...> rendering.
//
// We intentionally don't decode the QR back to URL — that would require a
// QR decoder dep and the encoder's output is well-known. We pin only the
// data-URL prefix + non-empty payload, which is enough to catch encoder
// regressions and base64 wrapper bugs.
// ---------------------------------------------------------------------------

// TestLocalLANIP_ReturnsValidAddress verifies localLANIP() never returns the
// empty string. Either a real LAN IPv4 (when an interface is up) OR the
// 127.0.0.1 fallback is acceptable. Pinning this stops a regression where a
// future refactor might return ("", nil) and silently produce a malformed
// pairing URL like jarvis://pair?host=:4422&...
func TestLocalLANIP_ReturnsValidAddress(t *testing.T) {
	ip, err := localLANIP()
	if err != nil {
		t.Fatalf("localLANIP(): unexpected error: %v", err)
	}
	if ip == "" {
		t.Fatalf("localLANIP() returned empty IP")
	}
	// The result must parse as a valid IPv4 address — either a LAN address
	// or the 127.0.0.1 fallback.
	parsed := net.ParseIP(ip)
	if parsed == nil {
		t.Fatalf("localLANIP() = %q; not a valid IP", ip)
	}
	if parsed.To4() == nil {
		t.Fatalf("localLANIP() = %q; not an IPv4 address", ip)
	}
}

// TestGenerateMobilePairingQR_MissingTokenReturnsError pins the
// empty-token failure mode. The error message must include "token" so the
// frontend can match on it (loose contract — we don't require an exact
// string) and present the "Regenerate token" affordance.
func TestGenerateMobilePairingQR_MissingTokenReturnsError(t *testing.T) {
	// Redirect HOME to a tmp dir so we don't clobber the user's real
	// config, and ensure config.Get() returns a fresh Config with no token.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	// Save an empty-token config so config.Get() reflects that state.
	cfg := config.DefaultConfig()
	cfg.MobileAPIToken = ""
	if err := config.Save(cfg); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	a := &App{}
	out, err := a.GenerateMobilePairingQR()
	if err == nil {
		t.Fatalf("GenerateMobilePairingQR() with empty token: got nil error, want error; out=%q", out)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "token") {
		t.Errorf("GenerateMobilePairingQR() error message %q does not mention 'token'", err.Error())
	}
	if out != "" {
		t.Errorf("GenerateMobilePairingQR() error path returned non-empty string %q", out)
	}
}

// TestGenerateMobilePairingQR_ValidTokenReturnsDataURL pins the happy path:
// with a non-empty token, the function returns a data:image/png;base64,...
// payload that the React modal can drop into an <img src=...>.
//
// We don't decode the QR (that would require a decoder dep), only the
// surrounding contract: prefix is correct, base64 payload is non-empty.
func TestGenerateMobilePairingQR_ValidTokenReturnsDataURL(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	cfg := config.DefaultConfig()
	cfg.MobileAPIToken = "test-bearer-token-abc123"
	cfg.MobileAPIPort = 4422
	if err := config.Save(cfg); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	a := &App{}
	out, err := a.GenerateMobilePairingQR()
	if err != nil {
		t.Fatalf("GenerateMobilePairingQR(): unexpected error: %v", err)
	}

	const prefix = "data:image/png;base64,"
	if !strings.HasPrefix(out, prefix) {
		// Truncate to avoid spewing the entire PNG into the test log on
		// failure.
		preview := out
		if len(preview) > 60 {
			preview = preview[:60] + "..."
		}
		t.Fatalf("GenerateMobilePairingQR() = %q; want prefix %q", preview, prefix)
	}

	// Base64 payload itself must be non-empty (a real PNG comes out to
	// several hundred bytes for a QR of this size).
	payload := strings.TrimPrefix(out, prefix)
	if len(payload) == 0 {
		t.Fatalf("GenerateMobilePairingQR() base64 payload is empty")
	}
	// Sanity check: a 256x256 QR PNG base64-encodes to well over 100 bytes.
	// Anything smaller suggests the PNG render failed silently.
	if len(payload) < 100 {
		t.Errorf("GenerateMobilePairingQR() payload suspiciously short: %d bytes", len(payload))
	}
}
