package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestNewSpotifyConfigIsZeroValueSafe verifies that the constructor returns
// a SpotifyConfig with every field at its Go zero value — so a never-signed-in
// user's config marshals to an empty JSON object (no credential placeholders
// on disk).
func TestNewSpotifyConfigIsZeroValueSafe(t *testing.T) {
	c := NewSpotifyConfig()
	if c.AccessToken != "" {
		t.Errorf("AccessToken: got %q, want empty", c.AccessToken)
	}
	if c.RefreshToken != "" {
		t.Errorf("RefreshToken: got %q, want empty", c.RefreshToken)
	}
	if !c.ExpiresAt.IsZero() {
		t.Errorf("ExpiresAt: got %v, want zero time", c.ExpiresAt)
	}
	if c.ClientID != "" {
		t.Errorf("ClientID: got %q, want empty", c.ClientID)
	}
	if c.IsConnected() {
		t.Errorf("IsConnected: got true, want false for zero-value config")
	}
}

// TestSpotifyConfigIsConnected verifies the derived helper flips to true the
// moment an AccessToken is populated (without regard to ExpiresAt — that's
// the refresh layer's job).
func TestSpotifyConfigIsConnected(t *testing.T) {
	cases := []struct {
		name string
		c    SpotifyConfig
		want bool
	}{
		{"zero value", SpotifyConfig{}, false},
		{"empty access token", SpotifyConfig{RefreshToken: "r"}, false},
		{"populated access token", SpotifyConfig{AccessToken: "tok"}, true},
		{
			"populated but expired (refresh layer's problem)",
			SpotifyConfig{AccessToken: "tok", ExpiresAt: time.Unix(0, 0)},
			true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.c.IsConnected(); got != tc.want {
				t.Errorf("IsConnected() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestSpotifyConfigRoundTrip pins the serialisation contract: a populated
// SpotifyConfig must round-trip through json.Marshal -> json.Unmarshal
// without losing any field, and the JSON keys must be the expected
// camelCase shape (so the Wails-bound config consumer on the frontend
// reads the same field names this task ships with).
func TestSpotifyConfigRoundTrip(t *testing.T) {
	orig := SpotifyConfig{
		AccessToken:  "BQA-access-token-abc",
		RefreshToken: "AQA-refresh-token-xyz",
		ExpiresAt:    time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC),
		ClientID:     "client-id-123",
	}

	data, err := json.Marshal(&orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Sanity-check the on-the-wire camelCase keys.
	outStr := string(data)
	wantSubstrings := []string{
		`"accessToken":"BQA-access-token-abc"`,
		`"refreshToken":"AQA-refresh-token-xyz"`,
		`"expiresAt":"2026-05-18T12:00:00Z"`,
		`"clientId":"client-id-123"`,
	}
	for _, sub := range wantSubstrings {
		if !strings.Contains(outStr, sub) {
			t.Errorf("marshaled JSON missing %q:\n%s", sub, outStr)
		}
	}

	var got SpotifyConfig
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.AccessToken != orig.AccessToken {
		t.Errorf("AccessToken: got %q, want %q", got.AccessToken, orig.AccessToken)
	}
	if got.RefreshToken != orig.RefreshToken {
		t.Errorf("RefreshToken: got %q, want %q", got.RefreshToken, orig.RefreshToken)
	}
	if !got.ExpiresAt.Equal(orig.ExpiresAt) {
		t.Errorf("ExpiresAt: got %v, want %v", got.ExpiresAt, orig.ExpiresAt)
	}
	if got.ClientID != orig.ClientID {
		t.Errorf("ClientID: got %q, want %q", got.ClientID, orig.ClientID)
	}
	if !got.IsConnected() {
		t.Errorf("IsConnected: got false, want true after round-trip with non-empty access token")
	}
}

// TestSpotifyConfigZeroValueOmitsEmptyKeys verifies the omitempty contract:
// a brand-new install must not leak empty-string credential placeholders
// to disk. The marshaled zero-value SpotifyConfig should be the empty
// JSON object `{}`.
func TestSpotifyConfigZeroValueOmitsEmptyKeys(t *testing.T) {
	data, err := json.Marshal(NewSpotifyConfig())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(data)
	// Note: time.Time's zero value does NOT marshal as the empty string —
	// the standard library emits "0001-01-01T00:00:00Z". Go's encoding/json
	// `omitempty` does not treat zero time as empty (since IsZero is not
	// the test it uses). So we accept either `{}` (preferred — if a future
	// change adds a custom MarshalJSON) or a JSON object that contains
	// ONLY the expiresAt key.
	for _, leaked := range []string{
		`"accessToken"`,
		`"refreshToken"`,
		`"clientId"`,
	} {
		if strings.Contains(got, leaked) {
			t.Errorf("zero-value JSON leaks key %q (should be omitted):\n%s", leaked, got)
		}
	}
}
