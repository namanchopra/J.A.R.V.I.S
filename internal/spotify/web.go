package spotify

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/namanchopra/jarvis/internal/model"
)

// ---------------------------------------------------------------------------
// Public data types — Track, PlayingState
// ---------------------------------------------------------------------------

// Track is the trimmed-down view of a Spotify track Jarvis cares about for
// search + playback. The Web API returns much more (album art URLs,
// popularity, available_markets) but Jarvis only needs URI + display fields.
type Track struct {
	// URI is the Spotify URI ("spotify:track:6rqhFgbbKwnb9MLmUQDhG6").
	// This is what AppleScript "play track" wants — bridging Web API
	// search to AppleScript playback is the whole point of TASK-009.
	URI string `json:"uri"`

	// Name is the human-visible track title.
	Name string `json:"name"`

	// Artist is the PRIMARY artist's name (first entry in the artists
	// array). Spotify tracks can have multiple artists; we surface only
	// the first because that's what the LLM-routed voice replies need.
	Artist string `json:"artist"`

	// Album is the album name.
	Album string `json:"album"`
}

// PlayingState mirrors the slice of `GET /v1/me/player/currently-playing`
// Jarvis uses for the "what is playing?" voice query.
//
// A nil *PlayingState means "nothing is playing" (Spotify returns 204 No
// Content in that case — handled in WhatIsPlaying).
type PlayingState struct {
	Track      Track `json:"track"`
	IsPlaying  bool  `json:"isPlaying"`
	ProgressMs int   `json:"progressMs"`
}

// ---------------------------------------------------------------------------
// Client — the Web API HTTP client
// ---------------------------------------------------------------------------

// Client is a thin Spotify Web API wrapper that auto-refreshes the access
// token when it has expired. Construct via NewClient.
//
// Concurrency: Search, WhatIsPlaying, and refreshIfNeeded acquire a single
// internal mutex around the cfg / token mutation path, so concurrent callers
// (the Wails App method goroutine + the daemon tool-bridge goroutine) won't
// double-refresh or read a half-mutated config.
type Client struct {
	// httpClient is the HTTP client used for Web API calls. Defaults to
	// http.DefaultClient when no override is supplied. Tests inject an
	// httptest.Server-backed client via WithHTTPClient.
	httpClient *http.Client

	// cfg is the live token state. Mutated on every successful refresh
	// (AccessToken / RefreshToken / ExpiresAt are rewritten). Pointer so
	// that the App-level config singleton sees the refresh.
	cfg *model.SpotifyConfig

	// saveCfg persists cfg after a refresh. Called inside the refresh
	// path so a daemon restart between refresh-and-search doesn't lose
	// the new tokens. Nil-safe: a nil saveCfg means "don't persist"
	// (handy for unit tests).
	saveCfg func(*model.SpotifyConfig) error

	// baseURL is the Web API base — overridable for tests via the
	// package-level webBaseURL var or via WithBaseURL.
	baseURL string

	// refreshSkew is how early before ExpiresAt we consider the token
	// "expired enough to refresh." Defaults to 60s, which buys us a full
	// minute of latency between the refresh and the actual API call.
	refreshSkew time.Duration

	// mu guards refreshes — see comment on Client.
	mu sync.Mutex
}

// webBaseURL is the Web API base. Var (not const) so tests can point at an
// httptest.Server.
var webBaseURL = "https://api.spotify.com"

// NewClient constructs a Client. cfg MUST be non-nil — Jarvis always loads
// a SpotifyConfig (even an empty one) before constructing this client.
//
// saveCfg may be nil — in which case refreshed tokens stay in memory only
// (useful for tests). In production, callers pass a closure that wraps
// internal/config.Save so a refresh persists across restarts.
func NewClient(cfg *model.SpotifyConfig, saveCfg func(*model.SpotifyConfig) error) *Client {
	if cfg == nil {
		// Defensive: a nil cfg is a programmer bug, but rather than panic
		// in production we fall back to a fresh empty config. ALL methods
		// will then return ErrNotAuthenticated until the user signs in.
		cfg = &model.SpotifyConfig{}
	}
	return &Client{
		httpClient:  http.DefaultClient,
		cfg:         cfg,
		saveCfg:     saveCfg,
		baseURL:     webBaseURL,
		refreshSkew: 60 * time.Second,
	}
}

// WithHTTPClient is an option-style setter used by tests to inject an
// httptest.Server-backed client. Production code should not call this.
func (c *Client) WithHTTPClient(h *http.Client) *Client {
	if h != nil {
		c.httpClient = h
	}
	return c
}

// WithBaseURL is an option-style setter used by tests to point the client
// at an httptest.Server URL. Production code should not call this.
func (c *Client) WithBaseURL(u string) *Client {
	if u != "" {
		c.baseURL = u
	}
	return c
}

// WithRefreshSkew is an option-style setter used by tests to control how
// early before expiry the refresh fires. Production code should not call.
func (c *Client) WithRefreshSkew(d time.Duration) *Client {
	c.refreshSkew = d
	return c
}

// ---------------------------------------------------------------------------
// Token refresh gate
// ---------------------------------------------------------------------------

// refreshIfNeeded checks whether the access token is missing or within
// refreshSkew of expiry, and if so refreshes it via the OAuth refresh-token
// grant. Returns ErrNotAuthenticated when there's no refresh token to use
// (i.e. user has never signed in).
//
// Holds c.mu for the duration so concurrent callers serialize. In the
// common case where the token is fresh, we don't hit the network at all —
// the time check is microseconds.
func (c *Client) refreshIfNeeded() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cfg.AccessToken == "" && c.cfg.RefreshToken == "" {
		return ErrNotAuthenticated
	}

	now := time.Now()
	needsRefresh := c.cfg.AccessToken == "" ||
		(!c.cfg.ExpiresAt.IsZero() && now.Add(c.refreshSkew).After(c.cfg.ExpiresAt))

	if !needsRefresh {
		return nil
	}

	if c.cfg.RefreshToken == "" {
		// We have an access token but no refresh token — once it expires,
		// we're done. Surface ErrNotAuthenticated so the caller re-prompts.
		return ErrNotAuthenticated
	}
	if c.cfg.ClientID == "" {
		return fmt.Errorf("spotify: refreshIfNeeded: cfg.ClientID is empty (corrupt config?)")
	}

	tok, err := RefreshToken(c.cfg.ClientID, c.cfg.RefreshToken)
	if err != nil {
		return err
	}

	// Mutate cfg with the new tokens. Spotify omits refresh_token when the
	// old one is still valid — preserve the existing value in that case.
	c.cfg.AccessToken = tok.AccessToken
	if tok.RefreshToken != "" {
		c.cfg.RefreshToken = tok.RefreshToken
	}
	c.cfg.ExpiresAt = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)

	if c.saveCfg != nil {
		if err := c.saveCfg(c.cfg); err != nil {
			return fmt.Errorf("spotify: refreshIfNeeded: saveCfg: %w", err)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Search
// ---------------------------------------------------------------------------

// Search calls `GET /v1/search?q=<query>&type=<types>&limit=5` and returns
// the parsed track list (when "track" is in types). Empty / whitespace-only
// queries short-circuit to ErrInvalidQuery WITHOUT issuing an HTTP request.
//
// types defaults to ["track"] when nil/empty. Spotify accepts a comma-
// separated list but Jarvis only uses "track" in practice; passing other
// types is supported but the returned slice will still only contain tracks
// from `response.tracks.items`.
//
// Surfaces ErrNotAuthenticated cleanly if the user hasn't signed in.
func (c *Client) Search(query string, types []string) ([]Track, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, ErrInvalidQuery
	}

	if err := c.refreshIfNeeded(); err != nil {
		return nil, err
	}

	if len(types) == 0 {
		types = []string{"track"}
	}

	u := url.Values{}
	u.Set("q", q)
	u.Set("type", strings.Join(types, ","))
	u.Set("limit", "5")

	endpoint := c.baseURL + "/v1/search?" + u.Encode()

	body, err := c.doGet(endpoint)
	if err != nil {
		return nil, fmt.Errorf("spotify: Search: %w", err)
	}

	// Parse only the slice we care about — tracks.items[].
	var resp struct {
		Tracks struct {
			Items []struct {
				URI     string `json:"uri"`
				Name    string `json:"name"`
				Artists []struct {
					Name string `json:"name"`
				} `json:"artists"`
				Album struct {
					Name string `json:"name"`
				} `json:"album"`
			} `json:"items"`
		} `json:"tracks"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("spotify: Search: decode: %w", err)
	}

	out := make([]Track, 0, len(resp.Tracks.Items))
	for _, it := range resp.Tracks.Items {
		t := Track{
			URI:   it.URI,
			Name:  it.Name,
			Album: it.Album.Name,
		}
		if len(it.Artists) > 0 {
			t.Artist = it.Artists[0].Name
		}
		out = append(out, t)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// WhatIsPlaying
// ---------------------------------------------------------------------------

// WhatIsPlaying calls `GET /v1/me/player/currently-playing` and returns the
// current track + play state.
//
// Returns (nil, nil) when Spotify returns 204 No Content (no active playback)
// — callers map this to a "Nothing is playing right now." voice reply.
// Returns ErrNotAuthenticated when the user hasn't signed in.
func (c *Client) WhatIsPlaying() (*PlayingState, error) {
	if err := c.refreshIfNeeded(); err != nil {
		return nil, err
	}

	endpoint := c.baseURL + "/v1/me/player/currently-playing"

	body, status, err := c.doGetWithStatus(endpoint)
	if err != nil {
		return nil, fmt.Errorf("spotify: WhatIsPlaying: %w", err)
	}
	if status == http.StatusNoContent {
		return nil, nil
	}

	var resp struct {
		IsPlaying  bool `json:"is_playing"`
		ProgressMs int  `json:"progress_ms"`
		Item       *struct {
			URI     string `json:"uri"`
			Name    string `json:"name"`
			Artists []struct {
				Name string `json:"name"`
			} `json:"artists"`
			Album struct {
				Name string `json:"name"`
			} `json:"album"`
		} `json:"item"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("spotify: WhatIsPlaying: decode: %w", err)
	}
	if resp.Item == nil {
		return nil, nil
	}

	t := Track{
		URI:   resp.Item.URI,
		Name:  resp.Item.Name,
		Album: resp.Item.Album.Name,
	}
	if len(resp.Item.Artists) > 0 {
		t.Artist = resp.Item.Artists[0].Name
	}
	return &PlayingState{
		Track:      t,
		IsPlaying:  resp.IsPlaying,
		ProgressMs: resp.ProgressMs,
	}, nil
}

// ---------------------------------------------------------------------------
// HTTP helpers
// ---------------------------------------------------------------------------

// doGet issues an authenticated GET and returns the response body. Wraps
// doGetWithStatus and treats any non-2xx as an error.
func (c *Client) doGet(endpoint string) ([]byte, error) {
	body, status, err := c.doGetWithStatus(endpoint)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("spotify: GET %s: status %d: %s", endpoint, status, strings.TrimSpace(string(body)))
	}
	return body, nil
}

// doGetWithStatus is doGet that also returns the HTTP status — used by
// WhatIsPlaying so it can distinguish 200 (have track) from 204 (no track).
//
// Holds c.mu only long enough to read the current access token; the
// outbound HTTP call is unlocked. This keeps the lock window tight so
// concurrent Search + WhatIsPlaying calls don't serialize on the network
// round-trip — a refresh that happens to fire from another goroutine
// blocks this one only for the duration of the token-cache update.
func (c *Client) doGetWithStatus(endpoint string) ([]byte, int, error) {
	c.mu.Lock()
	tok := c.cfg.AccessToken
	c.mu.Unlock()
	if tok == "" {
		return nil, 0, ErrNotAuthenticated
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		return nil, http.StatusNoContent, nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MiB cap
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read body: %w", err)
	}

	// 401 typically means the access token expired between refresh-skew
	// check and the actual call (rare but possible with clock drift).
	// Bubble up as ErrNotAuthenticated so the caller can re-prompt.
	if resp.StatusCode == http.StatusUnauthorized {
		return body, resp.StatusCode, fmt.Errorf("%w: 401 from %s", ErrNotAuthenticated, endpoint)
	}
	return body, resp.StatusCode, nil
}
