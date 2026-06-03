package main

// ---------------------------------------------------------------------------
// Google Calendar Wails bindings — TASK-007.
//
// Mirrors app_spotify.go in structure and conventions. Provides the surface
// area the React Settings view and the Python daemon's tool bridge bind
// against:
//
//   - GoogleCalendarSetCredentials   persists user-supplied Client ID +
//                                    Secret to ~/.jarvis/gcal.json. Empty
//                                    values clear the field so a future
//                                    re-sign-in can fall back to the
//                                    JARVIS_GCAL_CLIENT_ID / SECRET env
//                                    vars (no bundled default — Google
//                                    requires per-app verification).
//   - GoogleCalendarSignIn           drives the OAuth Authorization Code
//                                    flow via internal/gcal.RunOAuthFlow,
//                                    opens the system browser, runs a
//                                    one-shot localhost callback server,
//                                    persists tokens to config.
//   - GoogleCalendarSignOut          zeroes AccessToken / RefreshToken /
//                                    ExpiresAt but leaves ClientID +
//                                    ClientSecret in place so a fresh
//                                    SignIn doesn't need re-entry.
//   - GoogleCalendarIsConnected      reports whether a RefreshToken is
//                                    on disk. NOT a freshness check — the
//                                    persistingTokenSource in
//                                    internal/gcal.Client handles expiry
//                                    transparently on the next API call.
//   - GoogleCalendarGetUpcomingEvents
//                                    returns up to N upcoming events.
//   - GoogleCalendarGetNextEvent     returns the next event (or nil if
//                                    empty), cached in-memory for 60s to
//                                    protect against the mobile stats
//                                    broadcaster's 5s tick.
//   - GoogleCalendarCreateEvent      creates an event on the primary cal.
//   - GoogleCalendarMoveEvent        updates the start/end of an event.
//
// The cache (nextEventCacheState) is a package-level var rather than an
// App field because we may not modify app.go in this task. Cache misses on
// ErrNotAuthenticated short-circuit fresh API calls so a flapping unauth
// state doesn't get re-hammered every 5s.
// ---------------------------------------------------------------------------

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/namanchopra/jarvis/internal/gcal"
	"github.com/namanchopra/jarvis/internal/model"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// nextEventCacheTTL bounds how long GoogleCalendarGetNextEvent reuses a
// previously fetched snapshot before consulting the Calendar API again.
// 60s is chosen against the mobile stats broadcaster's 5s tick: at most
// one Calendar API call per minute per active client even if the HUD
// re-renders every 5 seconds. Errors are cached on the same TTL so a
// flapping API can't be hammered.
const nextEventCacheTTL = 60 * time.Second

// nextEventCache is the in-memory snapshot cache GoogleCalendarGetNextEvent
// reads from. Lives at package scope (rather than as an *App field) so
// this file is self-contained and does not require touching app.go.
//
// All access goes through nextEventCacheState.mu — the cache is shared
// across goroutines (mobile stats broadcaster + Wails main thread + Jarvis
// daemon tool dispatcher all call GetNextEvent independently).
type nextEventCache struct {
	mu       sync.Mutex
	snapshot *model.NextEventSnapshot
	cachedAt time.Time
	// err, when non-nil, was the result of the last underlying call.
	// Cached on the same TTL so a flapping Calendar API isn't hammered.
	// ErrNotAuthenticated specifically is returned without retry — the
	// user hasn't connected; no point making fresh API calls.
	err error
}

// nextEventCacheState is the singleton cache. Lazily populated on first
// GoogleCalendarGetNextEvent call.
var nextEventCacheState = &nextEventCache{}

// ---------------------------------------------------------------------------
// Credential resolution helpers
// ---------------------------------------------------------------------------

// googleCalendarClientID returns the Client ID to use. Prefers the value
// in ~/.jarvis/gcal.json (set via GoogleCalendarSetCredentials); falls
// back to the JARVIS_GCAL_CLIENT_ID env var. No bundled default — Google
// requires per-app verification, so we can't ship one safely.
//
// Resolution order: file → env → empty string.
func (a *App) googleCalendarClientID() string {
	cfg, _ := gcal.LoadConfig(gcal.ConfigPath())
	if cfg.ClientID != "" {
		return cfg.ClientID
	}
	return os.Getenv("JARVIS_GCAL_CLIENT_ID")
}

// googleCalendarClientSecret returns the Client Secret to use. Same
// resolution pattern as googleCalendarClientID; env fallback is
// JARVIS_GCAL_CLIENT_SECRET.
//
// Resolution order: file → env → empty string.
func (a *App) googleCalendarClientSecret() string {
	cfg, _ := gcal.LoadConfig(gcal.ConfigPath())
	if cfg.ClientSecret != "" {
		return cfg.ClientSecret
	}
	return os.Getenv("JARVIS_GCAL_CLIENT_SECRET")
}

// ---------------------------------------------------------------------------
// Bindings — credential management
// ---------------------------------------------------------------------------

// GoogleCalendarSetCredentials persists user-supplied Client ID + Secret
// to ~/.jarvis/gcal.json. Empty values clear the field (allowing fallback
// to the JARVIS_GCAL_CLIENT_ID / JARVIS_GCAL_CLIENT_SECRET env vars).
//
// Trimming is applied to both inputs at the top so a stray newline pasted
// from a credentials JSON download doesn't break the OAuth handshake
// downstream.
func (a *App) GoogleCalendarSetCredentials(clientID, clientSecret string) error {
	clientID = strings.TrimSpace(clientID)
	clientSecret = strings.TrimSpace(clientSecret)

	cfg, _ := gcal.LoadConfig(gcal.ConfigPath())
	cfg.ClientID = clientID
	cfg.ClientSecret = clientSecret

	if err := gcal.SaveConfig(gcal.ConfigPath(), cfg); err != nil {
		return fmt.Errorf("GoogleCalendarSetCredentials: %w", err)
	}
	slog.Info("GoogleCalendarSetCredentials: persisted",
		"hasClientID", clientID != "",
		"hasClientSecret", clientSecret != "",
	)
	return nil
}

// GoogleCalendarSignIn runs the Google Calendar OAuth flow end to end.
//
// Steps (all delegated to gcal.RunOAuthFlow):
//  1. Pick a free localhost port.
//  2. Start a one-shot callback server on that port.
//  3. Build the authorize URL with state-nonce + offline-access prompt.
//  4. Open the user's browser at the authorize URL (via runtime.BrowserOpenURL).
//  5. Wait for the callback (timeout: 5 min — see gcal.oauthFlowTimeout).
//  6. Validate state, exchange code for tokens.
//  7. Mutate the in-memory GCalConfig with tokens + expiry + creds.
//
// On success we persist the mutated config to disk and return "ok" — a
// non-empty string so the JS-side promise resolves with a truthy value
// the UI can branch on.
//
// Fails fast with ErrInvalidConfig when neither the on-disk config nor
// the env vars provide a Client ID + Secret — we don't want to open a
// browser tab to a guaranteed-error URL.
func (a *App) GoogleCalendarSignIn() (string, error) {
	clientID := a.googleCalendarClientID()
	clientSecret := a.googleCalendarClientSecret()

	if clientID == "" || clientSecret == "" {
		return "", fmt.Errorf("GoogleCalendarSignIn: %w", gcal.ErrInvalidConfig)
	}

	cfg, _ := gcal.LoadConfig(gcal.ConfigPath())

	// Wrap runtime.BrowserOpenURL to match the
	// `func(url string) error` signature gcal.RunOAuthFlow demands.
	// The Wails runtime helper returns no error, so we always return nil.
	openBrowser := func(url string) error {
		runtime.BrowserOpenURL(a.ctx, url)
		return nil
	}

	if err := gcal.RunOAuthFlow(clientID, clientSecret, openBrowser, &cfg); err != nil {
		slog.Warn("GoogleCalendarSignIn: OAuth flow failed", "err", err)
		return "", fmt.Errorf("GoogleCalendarSignIn: %w", err)
	}

	if err := gcal.SaveConfig(gcal.ConfigPath(), cfg); err != nil {
		return "", fmt.Errorf("GoogleCalendarSignIn: save: %w", err)
	}

	// Invalidate the next-event cache so a fresh sign-in immediately
	// reflects the user's calendar instead of a stale (often error-cached)
	// snapshot from before the OAuth flow.
	invalidateNextEventCache()

	slog.Info("GoogleCalendarSignIn: connected", "expiresAt", cfg.ExpiresAt)
	return "ok", nil
}

// GoogleCalendarSignOut clears the persisted Google Calendar tokens.
//
// Zeroes AccessToken, RefreshToken, and ExpiresAt — ClientID and
// ClientSecret are left in place so a subsequent GoogleCalendarSignIn
// doesn't need to re-enter credentials. After Save returns, a fresh
// GoogleCalendarIsConnected will report false.
func (a *App) GoogleCalendarSignOut() error {
	cfg, _ := gcal.LoadConfig(gcal.ConfigPath())
	cfg.AccessToken = ""
	cfg.RefreshToken = ""
	cfg.ExpiresAt = time.Time{}
	if err := gcal.SaveConfig(gcal.ConfigPath(), cfg); err != nil {
		return fmt.Errorf("GoogleCalendarSignOut: %w", err)
	}

	// Invalidate the next-event cache so a UI that polls GetNextEvent
	// after sign-out immediately sees the ErrNotAuthenticated state
	// (or rather: the corresponding empty/cached error) instead of a
	// stale snapshot from while the user was still connected.
	invalidateNextEventCache()
	return nil
}

// GoogleCalendarIsConnected reports whether the user has signed in.
//
// This is a presence check — "do we have a refresh token?" — not a
// freshness check. Access-token expiry is handled transparently by the
// persistingTokenSource in internal/gcal.Client, so the UI doesn't need
// to chase ExpiresAt. The refresh token (not the access token) is the
// load-bearing signal because access tokens cycle, refresh tokens persist.
func (a *App) GoogleCalendarIsConnected() bool {
	cfg, _ := gcal.LoadConfig(gcal.ConfigPath())
	return cfg.RefreshToken != ""
}

// ---------------------------------------------------------------------------
// Bindings — calendar reads
// ---------------------------------------------------------------------------

// GoogleCalendarGetUpcomingEvents returns up to `limit` upcoming events on
// the user's primary calendar, ordered by start time. limit <= 0 defaults
// to 10 so the UI can pass 0 and get a sensible default.
//
// Returns []model.CalendarEvent{} (not nil) on empty calendar — Wails
// serializes nil as JSON null, which the React side would have to defend
// against; the empty slice serializes as `[]` instead.
func (a *App) GoogleCalendarGetUpcomingEvents(limit int) ([]model.CalendarEvent, error) {
	if limit <= 0 {
		limit = 10
	}

	cfg, _ := gcal.LoadConfig(gcal.ConfigPath())

	// reload persists token refreshes that happen mid-call so a daemon
	// restart between refresh-and-read doesn't lose the new tokens. The
	// sibling-file design (~/.jarvis/gcal.json) means a concurrent
	// SaveConfig from the Settings view can't clobber unrelated keys —
	// only Google Calendar creds round-trip through here.
	reload := func(updated model.GCalConfig) error {
		return gcal.SaveConfig(gcal.ConfigPath(), updated)
	}

	client, err := gcal.NewClient(a.ctx, cfg, reload)
	if err != nil {
		return []model.CalendarEvent{}, fmt.Errorf("GoogleCalendarGetUpcomingEvents: %w", err)
	}

	events, err := client.ListUpcoming(a.ctx, limit)
	if err != nil {
		return []model.CalendarEvent{}, fmt.Errorf("GoogleCalendarGetUpcomingEvents: %w", err)
	}
	return events, nil
}

// GoogleCalendarGetNextEvent returns the next upcoming event on the user's
// primary calendar, or (nil, nil) when the calendar has no upcoming events.
// Cached in-memory for 60s (nextEventCacheTTL) so the mobile stats
// broadcaster's 5s tick doesn't burn Calendar API quota.
//
// The (nil, nil) sentinel is the explicit API contract — Wails serializes
// nil as JSON null so the mobile HUD renders the dash placeholder without
// branching on a sentinel error. Empty calendar is a normal state, not an
// error.
//
// Error caching: when the cached error is ErrNotAuthenticated we return it
// without re-trying — the user hasn't connected and re-attempting won't
// change the outcome until they sign in (which invalidates the cache).
// Other errors are also cached on the same TTL to protect against a
// flapping API, but only the unauth case is treated as "don't even
// consider the TTL window".
func (a *App) GoogleCalendarGetNextEvent() (*model.NextEventSnapshot, error) {
	nextEventCacheState.mu.Lock()
	defer nextEventCacheState.mu.Unlock()

	// Fast path: cache hit. If the last call errored with
	// ErrNotAuthenticated we keep returning that even past the TTL —
	// no point hammering the API for a user who hasn't signed in. The
	// cache is invalidated on SignIn so reconnection takes effect
	// immediately.
	if nextEventCacheState.err != nil && errors.Is(nextEventCacheState.err, gcal.ErrNotAuthenticated) {
		return nil, nextEventCacheState.err
	}
	if !nextEventCacheState.cachedAt.IsZero() && time.Since(nextEventCacheState.cachedAt) < nextEventCacheTTL {
		return nextEventCacheState.snapshot, nextEventCacheState.err
	}

	// Cache miss — make a fresh call and update the cache.
	snapshot, err := a.fetchNextEventSnapshot(a.ctx)
	nextEventCacheState.snapshot = snapshot
	nextEventCacheState.err = err
	nextEventCacheState.cachedAt = time.Now()
	return snapshot, err
}

// fetchNextEventSnapshot performs the uncached Calendar API call and
// formats the result as a NextEventSnapshot. Split out so the cache logic
// above stays focused on caching semantics.
func (a *App) fetchNextEventSnapshot(ctx context.Context) (*model.NextEventSnapshot, error) {
	cfg, _ := gcal.LoadConfig(gcal.ConfigPath())

	reload := func(updated model.GCalConfig) error {
		return gcal.SaveConfig(gcal.ConfigPath(), updated)
	}

	client, err := gcal.NewClient(ctx, cfg, reload)
	if err != nil {
		return nil, fmt.Errorf("GoogleCalendarGetNextEvent: %w", err)
	}

	evt, err := client.NextEvent(ctx)
	if err != nil {
		return nil, fmt.Errorf("GoogleCalendarGetNextEvent: %w", err)
	}
	if evt == nil {
		// Empty calendar is a normal state — return (nil, nil) so the
		// mobile HUD renders the dash placeholder.
		return nil, nil
	}

	snapshot := &model.NextEventSnapshot{
		Title:        evt.Title,
		StartISO:     evt.Start.UTC().Format(time.RFC3339),
		RelativeTime: formatRelativeTime(evt.Start, time.Now()),
	}
	return snapshot, nil
}

// formatRelativeTime renders a human-friendly countdown string for the
// HUD. Server-rendered so the mobile client doesn't have to ship its own
// time-math and the rendering rules live in exactly one place.
//
// Buckets (matching the TASK-007 spec verbatim):
//
//	delta < 0                    -> "now"
//	delta < 60s                  -> "now" (sub-minute resolution is below
//	                                Calendar's tick)
//	delta < 1h                   -> "in 14m"
//	delta < 24h                  -> "in 2h"
//	delta < 7d                   -> "in 3d"
//	otherwise                    -> "Mon Jan 2"
//
// `now` is passed in (rather than calling time.Now() directly) so tests
// can pin the wall clock without monkey-patching.
func formatRelativeTime(start, now time.Time) string {
	delta := start.Sub(now)
	switch {
	case delta < 0:
		return "now"
	case delta < 60*time.Second:
		return "now"
	case delta < time.Hour:
		return fmt.Sprintf("in %dm", int(delta.Minutes()))
	case delta < 24*time.Hour:
		return fmt.Sprintf("in %dh", int(delta.Hours()))
	case delta < 7*24*time.Hour:
		return fmt.Sprintf("in %dd", int(delta.Hours()/24))
	default:
		return start.Format("Mon Jan 2")
	}
}

// invalidateNextEventCache zeroes the cache so the next call to
// GoogleCalendarGetNextEvent makes a fresh API call. Called on SignIn /
// SignOut so the UI sees the connection-state transition immediately
// rather than waiting up to nextEventCacheTTL for a stale snapshot to
// expire.
func invalidateNextEventCache() {
	nextEventCacheState.mu.Lock()
	defer nextEventCacheState.mu.Unlock()
	nextEventCacheState.snapshot = nil
	nextEventCacheState.err = nil
	nextEventCacheState.cachedAt = time.Time{}
}

// ---------------------------------------------------------------------------
// Bindings — calendar writes
// ---------------------------------------------------------------------------

// GoogleCalendarCreateEvent creates an event on the user's primary
// calendar. startISO and endISO must be RFC3339 strings (the format
// emitted by JavaScript's `Date#toISOString()` and Go's time.Time
// RFC3339 formatter). Empty title returns ErrInvalidConfig — Google
// accepts blank-titled events but Jarvis's UX assumes a label.
//
// timeZone is the IANA zone the event is anchored to (e.g. "Asia/Dubai");
// empty falls back to the offset embedded in startISO/endISO. Storing
// the IANA name is what lets Google move the event with the user's wall
// clock across DST boundaries rather than against UTC.
//
// attendees may be nil or empty; empty entries are silently dropped by
// the SDK conversion in internal/gcal.toModelEvent.
func (a *App) GoogleCalendarCreateEvent(title, startISO, endISO string, attendees []string, timeZone string) (model.CalendarEvent, error) {
	title = strings.TrimSpace(title)
	startISO = strings.TrimSpace(startISO)
	endISO = strings.TrimSpace(endISO)
	timeZone = strings.TrimSpace(timeZone)

	if title == "" {
		return model.CalendarEvent{}, fmt.Errorf("GoogleCalendarCreateEvent: %w", gcal.ErrInvalidConfig)
	}

	start, err := time.Parse(time.RFC3339, startISO)
	if err != nil {
		return model.CalendarEvent{}, fmt.Errorf("GoogleCalendarCreateEvent: parse start: %w", err)
	}
	end, err := time.Parse(time.RFC3339, endISO)
	if err != nil {
		return model.CalendarEvent{}, fmt.Errorf("GoogleCalendarCreateEvent: parse end: %w", err)
	}

	cfg, _ := gcal.LoadConfig(gcal.ConfigPath())
	reload := func(updated model.GCalConfig) error {
		return gcal.SaveConfig(gcal.ConfigPath(), updated)
	}

	client, err := gcal.NewClient(a.ctx, cfg, reload)
	if err != nil {
		return model.CalendarEvent{}, fmt.Errorf("GoogleCalendarCreateEvent: %w", err)
	}

	evt := model.CalendarEvent{
		Title:     title,
		Start:     start,
		End:       end,
		Attendees: attendees,
		TimeZone:  timeZone,
	}
	created, err := client.CreateEvent(a.ctx, evt)
	if err != nil {
		return model.CalendarEvent{}, fmt.Errorf("GoogleCalendarCreateEvent: %w", err)
	}

	// Creating an event can change "what's next" — invalidate so the
	// HUD reflects the new event on the next poll.
	invalidateNextEventCache()
	slog.Info("GoogleCalendarCreateEvent: created", "id", created.ID, "title", created.Title)
	return created, nil
}

// GoogleCalendarMoveEvent updates the start/end of an existing event.
// Empty id returns ErrInvalidConfig before any network call. Other
// fields (title, attendees, location) are preserved by the underlying
// Patch — see internal/gcal.Client.MoveEvent.
func (a *App) GoogleCalendarMoveEvent(id, newStartISO, newEndISO, timeZone string) (model.CalendarEvent, error) {
	id = strings.TrimSpace(id)
	newStartISO = strings.TrimSpace(newStartISO)
	newEndISO = strings.TrimSpace(newEndISO)
	timeZone = strings.TrimSpace(timeZone)

	if id == "" {
		return model.CalendarEvent{}, fmt.Errorf("GoogleCalendarMoveEvent: %w", gcal.ErrInvalidConfig)
	}

	newStart, err := time.Parse(time.RFC3339, newStartISO)
	if err != nil {
		return model.CalendarEvent{}, fmt.Errorf("GoogleCalendarMoveEvent: parse start: %w", err)
	}
	newEnd, err := time.Parse(time.RFC3339, newEndISO)
	if err != nil {
		return model.CalendarEvent{}, fmt.Errorf("GoogleCalendarMoveEvent: parse end: %w", err)
	}

	cfg, _ := gcal.LoadConfig(gcal.ConfigPath())
	reload := func(updated model.GCalConfig) error {
		return gcal.SaveConfig(gcal.ConfigPath(), updated)
	}

	client, err := gcal.NewClient(a.ctx, cfg, reload)
	if err != nil {
		return model.CalendarEvent{}, fmt.Errorf("GoogleCalendarMoveEvent: %w", err)
	}

	moved, err := client.MoveEvent(a.ctx, id, newStart, newEnd, timeZone)
	if err != nil {
		return model.CalendarEvent{}, fmt.Errorf("GoogleCalendarMoveEvent: %w", err)
	}

	// Moving an event can change "what's next" — invalidate so the
	// HUD reflects the new ordering on the next poll.
	invalidateNextEventCache()
	slog.Info("GoogleCalendarMoveEvent: moved", "id", moved.ID, "start", moved.Start)
	return moved, nil
}
