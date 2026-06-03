package main

// ---------------------------------------------------------------------------
// Spotify Wails bindings — TASK-009 real implementations.
//
// Replaces the TASK-001 stubs with the real surface area the React UI and
// the Python daemon's tool registry (TASK-004) bind against:
//
//   - SpotifySignIn         drives the OAuth Authorization Code + PKCE flow
//                           via internal/spotify.RunPKCEFlow, opens the
//                           system browser, runs a one-shot localhost
//                           callback server, persists tokens to config.
//   - SpotifySignOut        zeros AccessToken/RefreshToken/ExpiresAt and
//                           saves the config so a subsequent
//                           SpotifyIsConnected returns false.
//   - SpotifyIsConnected    reports whether a non-empty AccessToken is on
//                           disk. NOT a freshness check — the refresh gate
//                           in internal/spotify.Client.refreshIfNeeded
//                           handles expiry transparently on the next API
//                           call.
//   - SpotifySearchAndPlay  Web API search + AppleScript play. Returns a
//                           spoken-friendly "Playing <track> by <artist>"
//                           string the daemon's tool layer can voice.
//   - SpotifyPause          local AppleScript pause — no auth required.
//   - SpotifyResume         local AppleScript resume — no auth required.
//
// The Wails-generated TypeScript bindings produced by `wails generate
// module` after this file lands give the React side a strongly-typed
// surface to call from the Settings view (sign-in/out) and the daemon
// tool-bridge can JSON-RPC the play/pause/resume methods directly.
//
// Public Spotify OAuth client id is hardcoded below (PKCE doesn't need a
// secret). Real value is injected in a follow-up; for now the
// placeholder lets the build pass and lets unit tests exercise the
// "not authenticated" error path without burning quota.
// ---------------------------------------------------------------------------

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/namanchopra/jarvis/internal/model"
	"github.com/namanchopra/jarvis/internal/spotify"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// defaultSpotifyClientID is the public Spotify Web API client ID Jarvis
// ships with. Hardcoded here because the Authorization Code + PKCE flow
// (RFC 7636) doesn't require a client secret — only the client id, which
// is safe to expose to a desktop binary. Users who register their own
// Spotify Developer app can override this by setting
// config.Config.Spotify.ClientID.
const defaultSpotifyClientID = "ba17d6fb406749ee9aa97bec6841687a"

// ---------------------------------------------------------------------------
// Test seams — TASK-030.
//
// These indirections let app_spotify_test.go exercise the full
// SpotifySearchAndPlay roundtrip without burning a real osascript subprocess
// or hitting the real Spotify Web API. Production code paths are unchanged:
// both vars default to the real implementations and are mutated only by
// tests (via t.Cleanup-restored swaps).
//
// Why two seams instead of one giant injectable client?
//   - spotifyAppleScriptPlayFn   — substituting at the Play() boundary keeps
//                                  the test surface tiny: a single func
//                                  signature, easy to record.
//   - spotifyWebBaseURLOverride  — non-empty string redirects the Web API
//                                  client to an httptest.Server URL. Empty
//                                  in production means "use the package
//                                  default api.spotify.com" — i.e. the
//                                  hot path is one extra string compare.
// ---------------------------------------------------------------------------

// spotifyAppleScriptPlayFn is the indirection SpotifySearchAndPlay calls so
// tests can capture the URI without invoking the real `osascript` binary.
// Production code path is unchanged: this dispatches to spotify.NewAppleScript().Play.
var spotifyAppleScriptPlayFn = func(uri string) error {
	return spotify.NewAppleScript().Play(uri)
}

// spotifyWebBaseURLOverride, when non-empty, redirects the Spotify Web API
// HTTP client at the given URL via spotify.Client.WithBaseURL. Tests set
// this to an httptest.Server URL; production leaves it empty so the client
// hits the real api.spotify.com. Set inside tests; restored via t.Cleanup.
var spotifyWebBaseURLOverride = ""

// spotifyClientID returns the client id to use for OAuth. Prefers a
// user-supplied id in ~/.jarvis/spotify.json (for self-hosters who want
// their own quota) and falls back to the Jarvis-bundled default.
func (a *App) spotifyClientID() string {
	cfg, _ := spotify.LoadConfig(spotify.ConfigPath())
	if cfg.ClientID != "" {
		return cfg.ClientID
	}
	return defaultSpotifyClientID
}

// SpotifySignIn runs the OAuth Authorization Code + PKCE flow end to end.
//
// Steps (all delegated to spotify.RunPKCEFlow):
//  1. Pick a free localhost port.
//  2. Start a one-shot callback server on that port.
//  3. Build the authorize URL with PKCE challenge + state nonce.
//  4. Open the user's browser at the authorize URL (via runtime.BrowserOpenURL).
//  5. Wait for the callback (timeout: 5 min — see spotify.pkceFlowTimeout).
//  6. Validate state, exchange code for tokens.
//  7. Mutate the in-memory Spotify config with tokens + expiry.
//
// On success we persist the mutated config to disk so the tokens survive
// app restart, then return "ok" — a non-empty string so the JS-side
// promise resolves with a truthy value the UI can branch on.
//
// On failure the config on disk is left untouched (RunPKCEFlow only
// mutates cfg.Spotify when ExchangeCode succeeds), so a botched sign-in
// doesn't half-clobber a previously valid token set.
func (a *App) SpotifySignIn() (string, error) {
	cfg, _ := spotify.LoadConfig(spotify.ConfigPath())

	// Wrap runtime.BrowserOpenURL to match the
	// `func(url string) error` signature spotify.RunPKCEFlow demands.
	// The Wails runtime helper returns no error, so we always return nil.
	openBrowser := func(url string) error {
		runtime.BrowserOpenURL(a.ctx, url)
		return nil
	}

	if err := spotify.RunPKCEFlow(a.spotifyClientID(), openBrowser, &cfg); err != nil {
		slog.Warn("SpotifySignIn: PKCE flow failed", "err", err)
		return "", fmt.Errorf("SpotifySignIn: %w", err)
	}

	if err := spotify.SaveConfig(spotify.ConfigPath(), cfg); err != nil {
		return "", fmt.Errorf("SpotifySignIn: save: %w", err)
	}

	slog.Info("SpotifySignIn: connected", "expiresAt", cfg.ExpiresAt)
	return "ok", nil
}

// SpotifySignOut clears the persisted Spotify credentials.
//
// Zeroes AccessToken, RefreshToken, and ExpiresAt — ClientID is left in
// place so a subsequent SpotifySignIn doesn't need to re-pick the user's
// preferred client id. After Save returns, a fresh SpotifyIsConnected
// will report false.
func (a *App) SpotifySignOut() error {
	cfg, _ := spotify.LoadConfig(spotify.ConfigPath())
	cfg.AccessToken = ""
	cfg.RefreshToken = ""
	cfg.ExpiresAt = time.Time{}
	if err := spotify.SaveConfig(spotify.ConfigPath(), cfg); err != nil {
		return fmt.Errorf("SpotifySignOut: %w", err)
	}
	return nil
}

// SpotifyIsConnected reports whether the user has signed in to Spotify.
//
// This is a presence check — "do we have a token?" — not a freshness
// check. Token expiry is handled transparently by the Web API client's
// refresh gate (spotify.Client.refreshIfNeeded), so the UI doesn't need
// to chase ExpiresAt. A user whose token has expired but whose refresh
// token is still valid still counts as "connected" — the next API call
// will silently mint a new access token.
func (a *App) SpotifyIsConnected() bool {
	cfg, _ := spotify.LoadConfig(spotify.ConfigPath())
	return cfg.AccessToken != ""
}

// SpotifySearchAndPlay searches the Spotify Web API for the query and
// kicks off playback of the top hit via the local Spotify.app AppleScript
// driver.
//
// Returns a spoken-friendly status string like
//
//	"Playing Blinding Lights by The Weeknd"
//
// that the Python daemon's tool layer can pass straight to the TTS
// pipeline. Errors are wrapped so the caller can inspect via errors.Is
// (e.g. errors.Is(err, spotify.ErrNotAuthenticated) to surface a
// "reconnect Spotify" prompt rather than a generic failure).
//
// Failure modes:
//   - empty query                         -> spotify.ErrInvalidQuery
//   - not authenticated                   -> spotify.ErrNotAuthenticated
//   - search succeeds but zero hits       -> "no tracks found for ..."
//   - AppleScript play fails (URI invalid -> wrapped error with Spotify's
//     or Spotify.app rejects it)            diagnostic text preserved
func (a *App) SpotifySearchAndPlay(query string) (string, error) {
	if query == "" {
		return "", fmt.Errorf("SpotifySearchAndPlay: %w", spotify.ErrInvalidQuery)
	}
	if !a.SpotifyIsConnected() {
		return "", spotify.ErrNotAuthenticated
	}

	cfg, _ := spotify.LoadConfig(spotify.ConfigPath())

	// saveCfg persists token refreshes that happen mid-call so a daemon
	// restart between refresh-and-play doesn't lose the new tokens. The
	// sibling-file design (~/.jarvis/spotify.json) means a concurrent
	// SaveConfig from the Settings view can't clobber unrelated config
	// keys — only spotify creds round-trip through here.
	saveCfg := func(sc *model.SpotifyConfig) error {
		return spotify.SaveConfig(spotify.ConfigPath(), *sc)
	}

	client := spotify.NewClient(&cfg, saveCfg)
	// In tests, spotifyWebBaseURLOverride points at an httptest.Server URL
	// so client.Search hits a mock instead of api.spotify.com. Empty in
	// production — the package default in internal/spotify/web.go wins.
	if spotifyWebBaseURLOverride != "" {
		client = client.WithBaseURL(spotifyWebBaseURLOverride)
	}
	tracks, err := client.Search(query, []string{"track"})
	if err != nil {
		return "", fmt.Errorf("SpotifySearchAndPlay: %w", err)
	}
	if len(tracks) == 0 {
		return "", fmt.Errorf("SpotifySearchAndPlay: no tracks found for %q", query)
	}

	top := tracks[0]
	if err := spotifyAppleScriptPlayFn(top.URI); err != nil {
		return "", fmt.Errorf("SpotifySearchAndPlay: applescript play: %w", err)
	}
	return fmt.Sprintf("Playing %s by %s", top.Name, top.Artist), nil
}

// SpotifyPause pauses the local Spotify.app via AppleScript.
//
// No-op if already paused — the Spotify scripting dictionary treats
// "pause" as idempotent. No auth required because this path doesn't
// touch the Web API.
func (a *App) SpotifyPause() error {
	if err := spotify.NewAppleScript().Pause(); err != nil {
		return fmt.Errorf("SpotifyPause: %w", err)
	}
	return nil
}

// SpotifyResume resumes the local Spotify.app via AppleScript.
//
// AppleScript uses "play" with no arguments to continue the current
// track (there is no separate "resume" verb in Spotify's scripting
// dictionary). No auth required because this path doesn't touch the
// Web API.
func (a *App) SpotifyResume() error {
	if err := spotify.NewAppleScript().Resume(); err != nil {
		return fmt.Errorf("SpotifyResume: %w", err)
	}
	return nil
}
