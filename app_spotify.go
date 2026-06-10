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
//   - SpotifySearchAndPlay  Web API search + AppleScript play (macOS)
//                           or Web API search + Web API play (Windows,
//                           TASK-028). Returns a spoken-friendly
//                           "Playing <track> by <artist>" string the
//                           daemon's tool layer can voice.
//   - SpotifyPause          AppleScript on macOS, Web API on Windows.
//   - SpotifyResume         AppleScript on macOS, Web API on Windows.
//   - SpotifySkip           AppleScript on macOS, Web API on Windows
//                           (TASK-028).
//
// TASK-028 — Windows port: AppleScript only exists on macOS; the
// `spotifyIsWindowsFn` seam routes all playback intents through the
// Spotify Web API (`PUT /v1/me/player/{play,pause}`, `POST /v1/me/player/next`)
// when running on Windows. The Web API requires an OAuth bearer token,
// which the existing PKCE flow already mints. Latency is higher
// (~500ms Web API vs ~100ms local AppleScript) — documented user-facing
// in the v0.4.0 release notes.
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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	goruntime "runtime"
	"strings"
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

// ---------------------------------------------------------------------------
// Platform driver selection — TASK-028.
//
// The AppleScript driver (internal/spotify/applescript.go) shells out to
// `osascript`, which only exists on macOS. On Windows the same intents
// (play/pause/resume/skip) route through the Spotify Web API's playback
// endpoints (PUT /v1/me/player/play|pause, POST /v1/me/player/next), which
// are cross-platform and require nothing more than an OAuth bearer token —
// which the existing PKCE flow already provides.
//
// spotifyIsWindowsFn is the seam tests use to flip the driver at runtime
// without rebuilding for Windows. Production simply returns
// runtime.GOOS == "windows"; tests override with a closure returning true
// to exercise the Web API path on a macOS dev box.
//
// Why a func var instead of a const built-tagged sentinel?
//   - Keeps the entire Windows-specific routing in this single file, per
//     TASK-028 plan (filesToCreate is empty).
//   - Tests can flip platforms without an OS reboot.
//
// The hot path is one extra function call per binding invocation —
// negligible compared with a network round-trip.
var spotifyIsWindowsFn = func() bool {
	return goruntime.GOOS == "windows"
}

// spotifyWebHTTPClient is the HTTP client used by the inline Web API
// playback helpers below. A package-level var (not a const) so tests can
// inject an httptest.Server-backed http.Client to capture the outbound
// PUT/POST and assert auth headers / paths / bodies.
//
// Production uses a 15s timeout to match internal/spotify/web.go's
// doGetWithStatus contract — playback endpoints are typically <200ms but
// a 15s ceiling protects against network stalls.
var spotifyWebHTTPClient = &http.Client{Timeout: 15 * time.Second}

// spotifyWebAPIBase returns the Web API base URL the inline playback
// helpers below should hit. Honours spotifyWebBaseURLOverride (test seam)
// so app_spotify_test.go can redirect at an httptest.Server, otherwise
// returns the real api.spotify.com.
func spotifyWebAPIBase() string {
	if spotifyWebBaseURLOverride != "" {
		return spotifyWebBaseURLOverride
	}
	return "https://api.spotify.com"
}

// spotifyWebRequest issues an authenticated request against the Spotify
// Web API's playback endpoints. Centralises bearer-token plumbing,
// no-active-device translation, and consistent error wrapping so the
// per-binding code below stays tight.
//
// Method is typically PUT (pause/resume/play/play-uri) or POST (next).
// body may be nil for the empty-payload endpoints (pause/next).
//
// Failure modes (all returned as errors):
//   - empty access token              -> spotify.ErrNotAuthenticated
//   - 401 from Spotify                -> spotify.ErrNotAuthenticated
//   - 404 NO_ACTIVE_DEVICE            -> ErrNoActiveDevice (defined below)
//   - 5xx / network errors            -> wrapped errors from net/http
//
// Refresh is delegated to the existing spotify.Client.refreshIfNeeded —
// we construct a transient Client just to bring the token current, then
// re-load the on-disk config so we hold the freshest access token. This
// reuses the well-tested refresh path instead of duplicating it here.
func (a *App) spotifyWebRequest(method, endpoint string, body []byte) error {
	cfg, _ := spotify.LoadConfig(spotify.ConfigPath())

	// Force a refresh-if-needed via the existing Web API client. The
	// trivial call (Search on an obviously-empty string would short-
	// circuit before any HTTP) doesn't work here — instead, lean on the
	// client's refresh gate by constructing it and calling a no-op path:
	// we mirror the saveCfg closure used elsewhere so a fresh token
	// persists across daemon restarts.
	saveCfg := func(sc *model.SpotifyConfig) error {
		return spotify.SaveConfig(spotify.ConfigPath(), *sc)
	}
	client := spotify.NewClient(&cfg, saveCfg)
	// WhatIsPlaying is the cheapest authenticated endpoint we know
	// pre-validates the token (it returns 204 when nothing's playing,
	// 200 when something is — never a 4xx for an authenticated user).
	// We discard the result; we only care that refreshIfNeeded fires.
	// Note: when no token exists at all, this returns
	// spotify.ErrNotAuthenticated which we propagate verbatim.
	if _, err := client.WhatIsPlaying(); err != nil {
		// 204-no-content is masked as (nil, nil) by WhatIsPlaying so
		// any non-nil err here is genuinely a problem. Surface the
		// auth error verbatim so callers can errors.Is-match it.
		if strings.Contains(err.Error(), spotify.ErrNotAuthenticated.Error()) {
			return spotify.ErrNotAuthenticated
		}
		// Other failures (network, 5xx) shouldn't block the playback
		// attempt outright — Spotify might still answer the playback
		// endpoint cleanly. Log and continue.
		slog.Debug("spotifyWebRequest: pre-call WhatIsPlaying probe failed", "err", err)
	}

	// Re-load after refresh so we use the freshest token.
	cfg, _ = spotify.LoadConfig(spotify.ConfigPath())
	if cfg.AccessToken == "" {
		return spotify.ErrNotAuthenticated
	}

	url := spotifyWebAPIBase() + endpoint

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+cfg.AccessToken)
	req.Header.Set("Accept", "application/json")
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := spotifyWebHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	// 2xx is success — Spotify returns 204 No Content for most playback
	// endpoints on the happy path.
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	respText := strings.TrimSpace(string(respBody))

	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return fmt.Errorf("%w: 401 from %s", spotify.ErrNotAuthenticated, endpoint)
	case http.StatusNotFound:
		// Spotify uses 404 NO_ACTIVE_DEVICE when the user has no
		// device currently selected for playback (Spotify app closed,
		// or user signed out of all clients). Surface a friendly
		// sentinel so the daemon can voice "Open Spotify on a device
		// first" rather than a generic 404.
		if strings.Contains(strings.ToLower(respText), "no active device") {
			return ErrSpotifyNoActiveDevice
		}
		return fmt.Errorf("spotify %s %s: 404: %s", method, endpoint, respText)
	default:
		return fmt.Errorf("spotify %s %s: status %d: %s", method, endpoint, resp.StatusCode, respText)
	}
}

// ErrSpotifyNoActiveDevice is returned when Spotify's playback endpoints
// answer with NO_ACTIVE_DEVICE — i.e. the user has no client open to
// receive playback commands. The daemon's tool layer maps this to a
// voice-friendly "Open Spotify on a device first" reply rather than a
// generic 404. Exported so other Wails bindings (and the Python tool
// bridge) can errors.Is-match it.
var ErrSpotifyNoActiveDevice = fmt.Errorf("spotify: no active device")

// spotifyWebPlayURI starts playback of the given Spotify URI via the
// Web API's PUT /v1/me/player/play endpoint. Body is a JSON object
// {"uris": ["spotify:track:..."]} per Spotify's API contract.
func (a *App) spotifyWebPlayURI(uri string) error {
	body, _ := json.Marshal(map[string]any{"uris": []string{uri}})
	return a.spotifyWebRequest(http.MethodPut, "/v1/me/player/play", body)
}

// spotifyWebPause hits PUT /v1/me/player/pause.
func (a *App) spotifyWebPause() error {
	return a.spotifyWebRequest(http.MethodPut, "/v1/me/player/pause", nil)
}

// spotifyWebResume hits PUT /v1/me/player/play with no body (resume
// current track rather than start a new one).
func (a *App) spotifyWebResume() error {
	return a.spotifyWebRequest(http.MethodPut, "/v1/me/player/play", nil)
}

// spotifyWebSkip hits POST /v1/me/player/next.
func (a *App) spotifyWebSkip() error {
	return a.spotifyWebRequest(http.MethodPost, "/v1/me/player/next", nil)
}

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

	// TASK-028: Windows has no AppleScript; route the play through the
	// Spotify Web API's PUT /v1/me/player/play endpoint instead. Latency
	// is higher (~500ms Web API vs ~100ms local AppleScript) but the
	// caller-visible contract is identical.
	if spotifyIsWindowsFn() {
		if err := a.spotifyWebPlayURI(top.URI); err != nil {
			return "", fmt.Errorf("SpotifySearchAndPlay: %w", err)
		}
		return fmt.Sprintf("Playing %s by %s", top.Name, top.Artist), nil
	}

	if err := spotifyAppleScriptPlayFn(top.URI); err != nil {
		return "", fmt.Errorf("SpotifySearchAndPlay: applescript play: %w", err)
	}
	return fmt.Sprintf("Playing %s by %s", top.Name, top.Artist), nil
}

// SpotifyPause pauses Spotify playback.
//
// macOS:  local Spotify.app via AppleScript (~100ms, no auth required).
// Windows (TASK-028): PUT /v1/me/player/pause via the Web API. Requires
// the user to have signed in via SpotifySignIn; surfaces
// ErrNotAuthenticated when no token is on disk, ErrSpotifyNoActiveDevice
// when Spotify isn't open on any device.
//
// No-op if already paused — Spotify treats the call as idempotent on
// both transports.
func (a *App) SpotifyPause() error {
	if spotifyIsWindowsFn() {
		if err := a.spotifyWebPause(); err != nil {
			return fmt.Errorf("SpotifyPause: %w", err)
		}
		return nil
	}
	if err := spotify.NewAppleScript().Pause(); err != nil {
		return fmt.Errorf("SpotifyPause: %w", err)
	}
	return nil
}

// SpotifyResume resumes Spotify playback.
//
// macOS:  local Spotify.app via AppleScript (~100ms, no auth required).
// Windows (TASK-028): PUT /v1/me/player/play with no body via the Web API.
// Spotify treats the empty-body play as "resume current track" rather
// than start a new one. Requires the user to have signed in.
//
// AppleScript uses "play" with no arguments to continue the current
// track (there is no separate "resume" verb in Spotify's scripting
// dictionary).
func (a *App) SpotifyResume() error {
	if spotifyIsWindowsFn() {
		if err := a.spotifyWebResume(); err != nil {
			return fmt.Errorf("SpotifyResume: %w", err)
		}
		return nil
	}
	if err := spotify.NewAppleScript().Resume(); err != nil {
		return fmt.Errorf("SpotifyResume: %w", err)
	}
	return nil
}

// SpotifySkip advances Spotify to the next track in the queue.
//
// macOS:  local Spotify.app via AppleScript (~100ms, no auth required).
// Windows (TASK-028): POST /v1/me/player/next via the Web API. Requires
// the user to have signed in via SpotifySignIn; surfaces
// ErrNotAuthenticated when no token is on disk, ErrSpotifyNoActiveDevice
// when Spotify isn't open on any device.
//
// Added in TASK-028 to round out the play/pause/resume/skip quartet the
// daemon's tool layer voices ("skip this song"). Idempotent for the
// caller — repeated calls just advance further through the queue.
func (a *App) SpotifySkip() error {
	if spotifyIsWindowsFn() {
		if err := a.spotifyWebSkip(); err != nil {
			return fmt.Errorf("SpotifySkip: %w", err)
		}
		return nil
	}
	if err := spotify.NewAppleScript().Next(); err != nil {
		return fmt.Errorf("SpotifySkip: %w", err)
	}
	return nil
}
