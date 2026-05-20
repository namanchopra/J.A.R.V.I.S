package model

import "time"

// ---------------------------------------------------------------------------
// SpotifyConfig
// ---------------------------------------------------------------------------

// SpotifyConfig holds the persisted credentials Jarvis needs to talk to the
// Spotify Web API on behalf of the user. It is embedded under the
// `spotify` key of the top-level Jarvis config (~/.jarvis/config.json) by
// `internal/config.Config.Spotify`.
//
// v0.3.0 TASK-001 ships this as a skeleton only — the real OAuth flow that
// populates AccessToken/RefreshToken/ExpiresAt lands in TASK-008, and the
// real Wails bindings that read/write it land in TASK-009. ClientID is the
// Spotify Developer app's client ID; left as a struct field (rather than a
// global constant) so users with their own Spotify Developer app can bring
// their own ID instead of relying on a Jarvis-distributed one.
//
// All `omitempty` so a default config file does not leak empty-string
// credential placeholders to disk — the keys only appear once the user has
// actually signed in.
type SpotifyConfig struct {
	// AccessToken is the short-lived OAuth bearer token. Refreshed via
	// RefreshToken when ExpiresAt is in the past.
	AccessToken string `json:"accessToken,omitempty"`

	// RefreshToken is the long-lived token used to mint new access tokens
	// without a fresh user-facing OAuth flow.
	RefreshToken string `json:"refreshToken,omitempty"`

	// ExpiresAt is the wall-clock time at which AccessToken stops being
	// valid. The token-refresh gate (TASK-008) compares against time.Now().
	ExpiresAt time.Time `json:"expiresAt,omitempty"`

	// ClientID is the Spotify Developer app client ID this install is
	// configured against. Empty means "use the Jarvis-bundled default"
	// (set by TASK-008).
	ClientID string `json:"clientId,omitempty"`
}

// IsConnected reports whether the config currently holds a non-empty access
// token. It does NOT consult ExpiresAt — that's the responsibility of the
// token-refresh layer in TASK-008. Callers wanting "is the token also fresh?"
// must check ExpiresAt separately.
//
// Derived helper rather than a stored field so we never have a stale
// `connected: true` left on disk after a sign-out.
func (c SpotifyConfig) IsConnected() bool {
	return c.AccessToken != ""
}

// NewSpotifyConfig returns a zero-value-safe SpotifyConfig. Every field is at
// its Go zero value, which marshals to an empty JSON object thanks to the
// `omitempty` tags — safe to persist into config.json without leaking
// credential placeholders.
func NewSpotifyConfig() SpotifyConfig {
	return SpotifyConfig{}
}
