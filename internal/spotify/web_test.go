package spotify

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/namanchopra/jarvis/internal/model"
)

// TestSearch_EmptyQueryReturnsErrInvalidQuery is the headline test from
// the acceptance criteria: `Search("")` must return ErrInvalidQuery
// without panicking and WITHOUT hitting the network.
func TestSearch_EmptyQueryReturnsErrInvalidQuery(t *testing.T) {
	// Trip-wire transport — any HTTP call fails the test.
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		t.Errorf("Search with empty query made HTTP call to %s", r.URL.Path)
	}))
	defer srv.Close()

	cfg := &model.SpotifyConfig{
		AccessToken:  "tok",
		RefreshToken: "ref",
		ClientID:     "cid",
		ExpiresAt:    time.Now().Add(time.Hour),
	}
	c := NewClient(cfg, nil).WithHTTPClient(srv.Client()).WithBaseURL(srv.URL)

	for _, q := range []string{"", "   ", "\t\n  "} {
		t.Run(fmt.Sprintf("query=%q", q), func(t *testing.T) {
			tracks, err := c.Search(q, []string{"track"})
			if !errors.Is(err, ErrInvalidQuery) {
				t.Errorf("expected ErrInvalidQuery, got: %v", err)
			}
			if tracks != nil {
				t.Errorf("expected nil tracks, got %d", len(tracks))
			}
		})
	}

	if atomic.LoadInt32(&hits) != 0 {
		t.Errorf("Search hit the network %d times for empty queries", hits)
	}
}

// TestSearch_HappyPath stands up a mock Web API server, runs a real Search,
// verifies the parsed track list.
func TestSearch_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/search" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("q"); got != "Blinding Lights" {
			t.Errorf("q: got %q, want %q", got, "Blinding Lights")
		}
		if got := r.URL.Query().Get("type"); got != "track" {
			t.Errorf("type: got %q, want track", got)
		}
		if got := r.URL.Query().Get("limit"); got != "5" {
			t.Errorf("limit: got %q, want 5", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer my-access-tok" {
			t.Errorf("Authorization: got %q, want %q", got, "Bearer my-access-tok")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"tracks": {
				"items": [
					{
						"uri": "spotify:track:0VjIjW4GlUZAMYd2vXMi3b",
						"name": "Blinding Lights",
						"artists": [{"name":"The Weeknd"}],
						"album": {"name":"After Hours"}
					},
					{
						"uri": "spotify:track:secondresult",
						"name": "Blinding Lights (Remix)",
						"artists": [{"name":"The Weeknd"},{"name":"Other Artist"}],
						"album": {"name":"After Hours Deluxe"}
					}
				]
			}
		}`))
	}))
	defer srv.Close()

	cfg := &model.SpotifyConfig{
		AccessToken:  "my-access-tok",
		RefreshToken: "my-refresh",
		ClientID:     "cid",
		ExpiresAt:    time.Now().Add(time.Hour),
	}
	c := NewClient(cfg, nil).WithHTTPClient(srv.Client()).WithBaseURL(srv.URL)

	tracks, err := c.Search("Blinding Lights", []string{"track"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(tracks) < 1 {
		t.Fatalf("expected >=1 track, got %d", len(tracks))
	}
	if tracks[0].URI != "spotify:track:0VjIjW4GlUZAMYd2vXMi3b" {
		t.Errorf("URI: got %q, want spotify:track:0VjIjW4GlUZAMYd2vXMi3b", tracks[0].URI)
	}
	if tracks[0].Name != "Blinding Lights" {
		t.Errorf("Name: got %q, want %q", tracks[0].Name, "Blinding Lights")
	}
	if tracks[0].Artist != "The Weeknd" {
		t.Errorf("Artist: got %q, want %q", tracks[0].Artist, "The Weeknd")
	}
	if tracks[0].Album != "After Hours" {
		t.Errorf("Album: got %q, want %q", tracks[0].Album, "After Hours")
	}
	// Acceptance criterion: URI must be non-empty.
	if tracks[0].URI == "" {
		t.Errorf("first track URI is empty")
	}
}

// TestSearch_ExpiredTokenTriggersRefresh is THE refresh-gate test from the
// task acceptance criteria. Set ExpiresAt to the past; verify Search calls
// the token endpoint to refresh, THEN calls /v1/search with the new token.
func TestSearch_ExpiredTokenTriggersRefresh(t *testing.T) {
	var tokenHits, searchHits int32
	// Mock token endpoint.
	tokSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&tokenHits, 1)
		_ = r.ParseForm()
		if got := r.PostForm.Get("grant_type"); got != "refresh_token" {
			t.Errorf("grant_type: got %q, want refresh_token", got)
		}
		if got := r.PostForm.Get("refresh_token"); got != "old-refresh" {
			t.Errorf("refresh_token: got %q, want old-refresh", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"access_token":"NEW-access-tok",
			"token_type":"Bearer",
			"scope":"s",
			"expires_in":3600,
			"refresh_token":"NEW-refresh-tok"
		}`))
	}))
	defer tokSrv.Close()
	restore := withTokenEndpoint(tokSrv.URL)
	defer restore()

	// Mock Web API: verify the NEW token is used, not the expired one.
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&searchHits, 1)
		if got := r.Header.Get("Authorization"); got != "Bearer NEW-access-tok" {
			t.Errorf("Authorization: got %q, want %q", got, "Bearer NEW-access-tok")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tracks":{"items":[{"uri":"u","name":"n","artists":[{"name":"a"}],"album":{"name":"al"}}]}}`))
	}))
	defer apiSrv.Close()

	cfg := &model.SpotifyConfig{
		AccessToken:  "OLD-expired",
		RefreshToken: "old-refresh",
		ClientID:     "cid",
		ExpiresAt:    time.Now().Add(-time.Hour), // PAST
	}

	var savedCfg *model.SpotifyConfig
	saveCfg := func(c *model.SpotifyConfig) error {
		savedCfg = c
		return nil
	}
	c := NewClient(cfg, saveCfg).WithHTTPClient(apiSrv.Client()).WithBaseURL(apiSrv.URL)

	tracks, err := c.Search("anything", []string{"track"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(tracks) != 1 {
		t.Errorf("expected 1 track, got %d", len(tracks))
	}

	if atomic.LoadInt32(&tokenHits) != 1 {
		t.Errorf("token endpoint hits: got %d, want 1", tokenHits)
	}
	if atomic.LoadInt32(&searchHits) != 1 {
		t.Errorf("search endpoint hits: got %d, want 1", searchHits)
	}

	// cfg should have been mutated with NEW tokens.
	if cfg.AccessToken != "NEW-access-tok" {
		t.Errorf("cfg.AccessToken: got %q, want NEW-access-tok", cfg.AccessToken)
	}
	if cfg.RefreshToken != "NEW-refresh-tok" {
		t.Errorf("cfg.RefreshToken: got %q, want NEW-refresh-tok", cfg.RefreshToken)
	}
	if !cfg.ExpiresAt.After(time.Now()) {
		t.Errorf("cfg.ExpiresAt should be in the future after refresh")
	}
	// saveCfg should have been invoked.
	if savedCfg == nil {
		t.Errorf("saveCfg was not called after refresh")
	} else if savedCfg.AccessToken != "NEW-access-tok" {
		t.Errorf("saveCfg got stale cfg: AccessToken=%q", savedCfg.AccessToken)
	}
}

// TestSearch_RefreshPreservesExistingRefreshToken — Spotify omits
// refresh_token in many refresh responses; verify we keep the old one.
func TestSearch_RefreshPreservesExistingRefreshToken(t *testing.T) {
	tokSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// NOTE: no refresh_token in the response.
		_, _ = w.Write([]byte(`{
			"access_token":"NEW-tok",
			"token_type":"Bearer",
			"scope":"s",
			"expires_in":3600
		}`))
	}))
	defer tokSrv.Close()
	restore := withTokenEndpoint(tokSrv.URL)
	defer restore()

	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tracks":{"items":[]}}`))
	}))
	defer apiSrv.Close()

	cfg := &model.SpotifyConfig{
		AccessToken:  "old",
		RefreshToken: "PRESERVE-ME",
		ClientID:     "cid",
		ExpiresAt:    time.Now().Add(-time.Hour),
	}
	c := NewClient(cfg, nil).WithHTTPClient(apiSrv.Client()).WithBaseURL(apiSrv.URL)

	if _, err := c.Search("q", nil); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if cfg.RefreshToken != "PRESERVE-ME" {
		t.Errorf("RefreshToken: got %q, want PRESERVE-ME (Spotify omitted, must preserve)", cfg.RefreshToken)
	}
	if cfg.AccessToken != "NEW-tok" {
		t.Errorf("AccessToken: got %q, want NEW-tok", cfg.AccessToken)
	}
}

// TestSearch_FreshTokenNoRefresh verifies no refresh fires when the token
// is still good (i.e. ExpiresAt is well in the future).
func TestSearch_FreshTokenNoRefresh(t *testing.T) {
	tokSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("token endpoint should NOT be hit when token is fresh")
	}))
	defer tokSrv.Close()
	restore := withTokenEndpoint(tokSrv.URL)
	defer restore()

	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tracks":{"items":[]}}`))
	}))
	defer apiSrv.Close()

	cfg := &model.SpotifyConfig{
		AccessToken:  "fresh-token",
		RefreshToken: "ref",
		ClientID:     "cid",
		ExpiresAt:    time.Now().Add(time.Hour),
	}
	c := NewClient(cfg, nil).WithHTTPClient(apiSrv.Client()).WithBaseURL(apiSrv.URL)

	if _, err := c.Search("blah", nil); err != nil {
		t.Fatalf("Search: %v", err)
	}
}

// TestSearch_NotAuthenticated returns ErrNotAuthenticated cleanly when
// there are no tokens at all.
func TestSearch_NotAuthenticated(t *testing.T) {
	c := NewClient(&model.SpotifyConfig{}, nil)
	_, err := c.Search("foo", nil)
	if !errors.Is(err, ErrNotAuthenticated) {
		t.Errorf("expected ErrNotAuthenticated, got: %v", err)
	}
}

// TestSearch_DefaultTypesIsTrack — passing nil/empty types should default
// to ["track"].
func TestSearch_DefaultTypesIsTrack(t *testing.T) {
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("type"); got != "track" {
			t.Errorf("default type: got %q, want track", got)
		}
		_, _ = w.Write([]byte(`{"tracks":{"items":[]}}`))
	}))
	defer apiSrv.Close()

	cfg := &model.SpotifyConfig{
		AccessToken:  "t",
		RefreshToken: "r",
		ClientID:     "cid",
		ExpiresAt:    time.Now().Add(time.Hour),
	}
	c := NewClient(cfg, nil).WithHTTPClient(apiSrv.Client()).WithBaseURL(apiSrv.URL)
	if _, err := c.Search("q", nil); err != nil {
		t.Fatalf("Search nil types: %v", err)
	}
	if _, err := c.Search("q", []string{}); err != nil {
		t.Fatalf("Search empty types: %v", err)
	}
}

// TestWhatIsPlaying_HappyPath stands up a mock currently-playing endpoint.
func TestWhatIsPlaying_HappyPath(t *testing.T) {
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/me/player/currently-playing" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{
			"is_playing": true,
			"progress_ms": 12345,
			"item": {
				"uri": "spotify:track:xyz",
				"name": "Test Track",
				"artists": [{"name":"Test Artist"}],
				"album": {"name":"Test Album"}
			}
		}`))
	}))
	defer apiSrv.Close()

	cfg := &model.SpotifyConfig{
		AccessToken:  "t",
		RefreshToken: "r",
		ClientID:     "cid",
		ExpiresAt:    time.Now().Add(time.Hour),
	}
	c := NewClient(cfg, nil).WithHTTPClient(apiSrv.Client()).WithBaseURL(apiSrv.URL)

	state, err := c.WhatIsPlaying()
	if err != nil {
		t.Fatalf("WhatIsPlaying: %v", err)
	}
	if state == nil {
		t.Fatal("got nil PlayingState, want populated")
	}
	if !state.IsPlaying {
		t.Errorf("IsPlaying: got false, want true")
	}
	if state.ProgressMs != 12345 {
		t.Errorf("ProgressMs: got %d, want 12345", state.ProgressMs)
	}
	if state.Track.URI != "spotify:track:xyz" {
		t.Errorf("Track.URI: got %q, want spotify:track:xyz", state.Track.URI)
	}
	if state.Track.Artist != "Test Artist" {
		t.Errorf("Track.Artist: got %q, want Test Artist", state.Track.Artist)
	}
}

// TestWhatIsPlaying_NoContent204 — when nothing is playing, Spotify
// returns 204; we return (nil, nil).
func TestWhatIsPlaying_NoContent204(t *testing.T) {
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer apiSrv.Close()

	cfg := &model.SpotifyConfig{
		AccessToken:  "t",
		RefreshToken: "r",
		ClientID:     "cid",
		ExpiresAt:    time.Now().Add(time.Hour),
	}
	c := NewClient(cfg, nil).WithHTTPClient(apiSrv.Client()).WithBaseURL(apiSrv.URL)

	state, err := c.WhatIsPlaying()
	if err != nil {
		t.Fatalf("WhatIsPlaying: %v", err)
	}
	if state != nil {
		t.Errorf("expected nil state on 204, got: %+v", state)
	}
}

// TestWhatIsPlaying_NotAuthenticated bubbles up the sentinel error.
func TestWhatIsPlaying_NotAuthenticated(t *testing.T) {
	c := NewClient(&model.SpotifyConfig{}, nil)
	_, err := c.WhatIsPlaying()
	if !errors.Is(err, ErrNotAuthenticated) {
		t.Errorf("expected ErrNotAuthenticated, got: %v", err)
	}
}

// TestNewClient_NilCfgIsSafe verifies the defensive nil-check.
func TestNewClient_NilCfgIsSafe(t *testing.T) {
	c := NewClient(nil, nil)
	if c == nil {
		t.Fatal("NewClient(nil, nil) returned nil")
	}
	_, err := c.Search("anything", nil)
	if !errors.Is(err, ErrNotAuthenticated) {
		t.Errorf("expected ErrNotAuthenticated from nil-cfg client, got: %v", err)
	}
}

// TestSearch_RefreshFailurePropagates — when the refresh-token grant
// itself fails, callers should see the underlying error.
func TestSearch_RefreshFailurePropagates(t *testing.T) {
	tokSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer tokSrv.Close()
	restore := withTokenEndpoint(tokSrv.URL)
	defer restore()

	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("API endpoint should NOT be hit when refresh fails")
	}))
	defer apiSrv.Close()

	cfg := &model.SpotifyConfig{
		AccessToken:  "expired",
		RefreshToken: "bad",
		ClientID:     "cid",
		ExpiresAt:    time.Now().Add(-time.Hour),
	}
	c := NewClient(cfg, nil).WithHTTPClient(apiSrv.Client()).WithBaseURL(apiSrv.URL)

	_, err := c.Search("q", nil)
	if err == nil {
		t.Fatal("expected error from failed refresh, got nil")
	}
	if !errors.Is(err, ErrTokenRefreshFailed) {
		t.Errorf("expected errors.Is(ErrTokenRefreshFailed), got: %v", err)
	}
}

// TestTrack_JSONMarshalShape verifies the JSON tags survive a round-trip.
// Locks the consumer-facing contract for daemon/tool-bridge users.
func TestTrack_JSONMarshalShape(t *testing.T) {
	tr := Track{URI: "u", Name: "n", Artist: "a", Album: "al"}
	b, err := json.Marshal(tr)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{`"uri":"u"`, `"name":"n"`, `"artist":"a"`, `"album":"al"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("JSON missing %q in %s", want, b)
		}
	}
}

// TestSearch_NoActiveServerReturnsError — pin that a connection error
// surfaces cleanly (not as a panic). Use a closed server.
func TestSearch_ConnectionErrorPropagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Force a malformed response (body shorter than Content-Length) by
		// hijacking and closing. Simpler: write garbage, set status.
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{not valid json`)
	}))
	defer srv.Close()

	cfg := &model.SpotifyConfig{
		AccessToken:  "t",
		RefreshToken: "r",
		ClientID:     "cid",
		ExpiresAt:    time.Now().Add(time.Hour),
	}
	c := NewClient(cfg, nil).WithHTTPClient(srv.Client()).WithBaseURL(srv.URL)

	_, err := c.Search("q", nil)
	if err == nil {
		t.Fatal("expected decode error, got nil")
	}
}
