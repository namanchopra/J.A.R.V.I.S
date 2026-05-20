package main

// ---------------------------------------------------------------------------
// app_spotify_test.go — TASK-030 integration tests.
//
// These tests exercise the cross-package roundtrip that lands in
// app_spotify.go::SpotifySearchAndPlay:
//
//     SpotifySearchAndPlay(query)
//         ├── config.Get()             ← per-test HOME-rooted config.json
//         ├── spotify.Client.Search()  ← redirected at httptest.Server
//         └── spotify.AppleScript.Play ← captured via test seam
//
// TASK-007 (applescript_test.go) and TASK-008 (oauth_test.go / web_test.go)
// already pin the Web API client + AppleScript driver at the unit level
// inside internal/spotify/. This file adds the higher-level wiring test
// the plan calls out under "Spotify OAuth flow round-trip" / "Spotify
// AppleScript Play with valid URI" in the Test Coverage Map.
//
// Why three tests and not one big one?
//   - Happy path covers the wiring (search → play, response formatting).
//   - NotAuthenticated covers the "no token" short-circuit before any I/O.
//   - EmptyQuery covers the "no query" short-circuit BEFORE the auth check
//     (the order matters — empty query is a programmer/voice misfire bug,
//     auth state is a user state; surfacing them in the right order makes
//     the daemon's spoken reply match the actual failure).
//
// Test seams used (declared in app_spotify.go):
//   - spotifyAppleScriptPlayFn    — captures the URI a real Spotify.app would receive
//   - spotifyWebBaseURLOverride   — points the Web API client at httptest.NewServer
//
// HOME isolation: every test sets t.Setenv("HOME", t.TempDir()) AND
// explicitly Saves a fresh config so the in-memory cache in
// internal/config doesn't leak between tests. config.Save mutates the
// package-level `current` pointer, so the order here is deliberate: set
// HOME, then Save, then construct App, then call the binding.
// ---------------------------------------------------------------------------

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/namanchopra/jarvis/internal/config"
	"github.com/namanchopra/jarvis/internal/model"
	"github.com/namanchopra/jarvis/internal/spotify"
)

// TestSpotifySearchAndPlay_HappyPath verifies the v0.3.0 contract: a voice
// query reaches Spotify by way of Web API search → AppleScript play.
//
// Strategy:
//  1. Stand up an httptest server that returns ONE canned /v1/search hit.
//  2. Redirect the Web API client at that server via
//     spotifyWebBaseURLOverride (cleaned up after the test).
//  3. Substitute spotifyAppleScriptPlayFn with a recorder so we capture
//     the URI rather than burning a real `osascript` subprocess.
//  4. Persist a SpotifyConfig with a valid token + future expiry to the
//     per-test HOME-rooted config.json.
//  5. Call a.SpotifySearchAndPlay("Blinding Lights").
//
// Assertions:
//   - response string is the daemon-friendly "Playing <name> by <artist>"
//   - AppleScript recorder captured EXACTLY one Play call
//   - The captured URI matches what the search server returned
func TestSpotifySearchAndPlay_HappyPath(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	// --- 1. Mock Spotify Web API server.
	var searchHits int32
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/search" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		atomic.AddInt32(&searchHits, 1)

		if got := r.URL.Query().Get("q"); got != "Blinding Lights" {
			t.Errorf("q: got %q, want %q", got, "Blinding Lights")
		}
		if got := r.URL.Query().Get("type"); got != "track" {
			t.Errorf("type: got %q, want track", got)
		}
		// Authorization must come from cfg.Spotify.AccessToken — bearer
		// scheme. Any other prefix means the wiring is broken.
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization: got %q, want %q", got, "Bearer test-token")
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"tracks": map[string]interface{}{
				"items": []map[string]interface{}{
					{
						"uri":     "spotify:track:0VjIjW4GlUZAMYd2vXMi3b",
						"name":    "Blinding Lights",
						"artists": []map[string]interface{}{{"name": "The Weeknd"}},
						"album":   map[string]interface{}{"name": "After Hours"},
					},
				},
			},
		})
	}))
	t.Cleanup(apiSrv.Close)

	// --- 2. Redirect the Web API client at the mock server.
	prevBase := spotifyWebBaseURLOverride
	spotifyWebBaseURLOverride = apiSrv.URL
	t.Cleanup(func() { spotifyWebBaseURLOverride = prevBase })

	// --- 3. Substitute the AppleScript Play function with a recorder.
	var playCalls int32
	var playedURI string
	prevPlay := spotifyAppleScriptPlayFn
	spotifyAppleScriptPlayFn = func(uri string) error {
		atomic.AddInt32(&playCalls, 1)
		playedURI = uri
		return nil
	}
	t.Cleanup(func() { spotifyAppleScriptPlayFn = prevPlay })

	// --- 4. Persist a connected SpotifyConfig.
	cfg := config.DefaultConfig()
	cfg.Spotify = model.SpotifyConfig{
		AccessToken: "test-token",
		// RefreshToken empty: with a non-empty access token whose
		// ExpiresAt is well in the future, refreshIfNeeded must not
		// trip — so we don't need a refresh-token endpoint for the
		// happy path.
		ExpiresAt: time.Now().Add(1 * time.Hour),
		ClientID:  "test-client",
	}
	if err := config.Save(cfg); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	// --- 5. Drive the binding.
	a := &App{}
	msg, err := a.SpotifySearchAndPlay("Blinding Lights")
	if err != nil {
		t.Fatalf("SpotifySearchAndPlay: unexpected error: %v", err)
	}

	// --- Assertions: response string + recorder state.
	//
	// Why two substring checks rather than full equality? The format
	// string in app_spotify.go is "Playing %s by %s" — pinning the
	// exact output to "Playing Blinding Lights by The Weeknd" would
	// be even tighter, but the contract the daemon depends on is
	// "track name AND artist appear in the spoken reply". Two
	// substring checks express that without over-constraining the
	// surrounding punctuation.
	if !strings.Contains(msg, "Blinding Lights") {
		t.Errorf("response %q missing track name", msg)
	}
	if !strings.Contains(msg, "Weeknd") {
		t.Errorf("response %q missing artist name", msg)
	}

	if got := atomic.LoadInt32(&searchHits); got != 1 {
		t.Errorf("search endpoint hits: got %d, want 1", got)
	}
	if got := atomic.LoadInt32(&playCalls); got != 1 {
		t.Errorf("AppleScript Play calls: got %d, want 1", got)
	}
	if playedURI != "spotify:track:0VjIjW4GlUZAMYd2vXMi3b" {
		t.Errorf("AppleScript Play URI: got %q, want spotify:track:0VjIjW4GlUZAMYd2vXMi3b", playedURI)
	}
}

// TestSpotifySearchAndPlay_NotAuthenticated pins the no-token failure mode.
//
// With an empty AccessToken in config, SpotifySearchAndPlay must return
// ErrNotAuthenticated (so the daemon can surface "I need you to connect
// Spotify first" rather than a generic failure). The check happens AFTER
// the empty-query guard but BEFORE any HTTP call — verified here by NOT
// installing a mock server.
//
// We use a trip-wire AppleScript seam to catch a regression where the
// auth check moves below the Play call. Any invocation fails the test.
func TestSpotifySearchAndPlay_NotAuthenticated(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	// Trip-wire: any AppleScript call before the auth check is a bug.
	prevPlay := spotifyAppleScriptPlayFn
	spotifyAppleScriptPlayFn = func(uri string) error {
		t.Errorf("AppleScript Play called before auth check (uri=%s)", uri)
		return nil
	}
	t.Cleanup(func() { spotifyAppleScriptPlayFn = prevPlay })

	// Persist a fresh config with NO Spotify access token. Important to
	// Save explicitly so the in-memory `current` pointer in
	// internal/config doesn't carry over a token from a previous test.
	cfg := config.DefaultConfig()
	cfg.Spotify = model.SpotifyConfig{} // zero value: empty token
	if err := config.Save(cfg); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	a := &App{}
	out, err := a.SpotifySearchAndPlay("anything")
	if err == nil {
		t.Fatalf("expected error, got nil; out=%q", out)
	}
	if !errors.Is(err, spotify.ErrNotAuthenticated) {
		t.Errorf("err = %v; want errors.Is(ErrNotAuthenticated)", err)
	}
	if out != "" {
		t.Errorf("expected empty out on error path, got %q", out)
	}
}

// TestSpotifySearchAndPlay_EmptyQuery pins the empty-query short-circuit.
//
// An empty or whitespace-only query is a voice misfire ("Hey Jarvis, play
// — uh — never mind"); we surface ErrInvalidQuery WITHOUT touching the
// network or AppleScript so the daemon can voice "What would you like me
// to play?" and burn no quota.
//
// IMPORTANT: this guard fires BEFORE the auth check — verified here by
// the test passing even with NO Spotify token configured. Trip-wires on
// both the AppleScript seam and a never-installed HTTP server catch any
// future re-ordering that would call out before validating the query.
func TestSpotifySearchAndPlay_EmptyQuery(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	// Trip-wire: AppleScript must never be invoked for an empty query.
	prevPlay := spotifyAppleScriptPlayFn
	spotifyAppleScriptPlayFn = func(uri string) error {
		t.Errorf("AppleScript Play called for empty query (uri=%s)", uri)
		return nil
	}
	t.Cleanup(func() { spotifyAppleScriptPlayFn = prevPlay })

	// Reset config to defaults so prior tests don't leak in-memory state.
	if err := config.Save(config.DefaultConfig()); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	a := &App{}
	out, err := a.SpotifySearchAndPlay("")
	if err == nil {
		t.Fatalf("expected error for empty query, got nil; out=%q", out)
	}
	if !errors.Is(err, spotify.ErrInvalidQuery) {
		t.Errorf("err = %v; want errors.Is(ErrInvalidQuery)", err)
	}
	if out != "" {
		t.Errorf("expected empty out on error path, got %q", out)
	}
}
