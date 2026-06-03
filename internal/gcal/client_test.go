package gcal

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/namanchopra/jarvis/internal/model"
)

// TestNewClientRequiresRefreshToken pins the documented contract: a config
// with no refresh token cannot mint new access tokens, so NewClient returns
// ErrNotAuthenticated rather than constructing a Client that would fail on
// the first API call.
func TestNewClientRequiresRefreshToken(t *testing.T) {
	cfg := model.GCalConfig{
		ClientID:     "cid.apps.googleusercontent.com",
		ClientSecret: "GOCSPX-secret",
		// RefreshToken intentionally empty.
	}
	_, err := NewClient(context.Background(), cfg, nil)
	if err == nil {
		t.Fatal("NewClient: expected error for empty RefreshToken, got nil")
	}
	if !errors.Is(err, ErrNotAuthenticated) {
		t.Errorf("expected errors.Is(err, ErrNotAuthenticated); got: %v", err)
	}
}

// TestNewClientRequiresClientID verifies missing OAuth client credentials
// surface as ErrInvalidConfig — distinct from "user hasn't signed in"
// (ErrNotAuthenticated) so Settings can show different remediation paths.
// Refresh token is non-empty here so we're isolating the client-id guard.
func TestNewClientRequiresClientID(t *testing.T) {
	cfg := model.GCalConfig{
		// ClientID empty.
		ClientSecret: "GOCSPX-secret",
		RefreshToken: "1//0g-refresh",
	}
	_, err := NewClient(context.Background(), cfg, nil)
	if err == nil {
		t.Fatal("NewClient: expected error for empty ClientID, got nil")
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("expected errors.Is(err, ErrInvalidConfig); got: %v", err)
	}
}

// TestNewClientRequiresClientSecret mirrors ClientID — both halves of the
// confidential-client credential pair are required.
func TestNewClientRequiresClientSecret(t *testing.T) {
	cfg := model.GCalConfig{
		ClientID: "cid.apps.googleusercontent.com",
		// ClientSecret empty.
		RefreshToken: "1//0g-refresh",
	}
	_, err := NewClient(context.Background(), cfg, nil)
	if err == nil {
		t.Fatal("NewClient: expected error for empty ClientSecret, got nil")
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("expected errors.Is(err, ErrInvalidConfig); got: %v", err)
	}
}

// newAuthedClient builds a real *Client whose underlying token source is a
// passthrough that returns the supplied static token. Used by the validation
// tests that need a non-nil Client but never make a real API call (the
// validation guard short-circuits before any network hop).
func newAuthedClient(t *testing.T) *Client {
	t.Helper()
	cfg := model.GCalConfig{
		ClientID:     "cid.apps.googleusercontent.com",
		ClientSecret: "GOCSPX-secret",
		RefreshToken: "1//0g-refresh",
		AccessToken:  "ya29.access-current",
		ExpiresAt:    time.Now().Add(1 * time.Hour),
	}
	c, err := NewClient(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("newAuthedClient: NewClient: %v", err)
	}
	return c
}

// TestCreateEventRejectsInvertedRange verifies the pre-flight validation
// fires BEFORE any network call: Start >= End is nonsensical and Google
// would 400 us anyway, so we fail fast with ErrInvalidConfig and save the
// round-trip. No httptest plumbing needed — validation runs first.
func TestCreateEventRejectsInvertedRange(t *testing.T) {
	c := newAuthedClient(t)
	start := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	end := start.Add(-1 * time.Hour) // before start
	evt := model.CalendarEvent{
		Title: "broken",
		Start: start,
		End:   end,
	}
	_, err := c.CreateEvent(context.Background(), evt)
	if err == nil {
		t.Fatal("CreateEvent: expected error for inverted range, got nil")
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("expected errors.Is(err, ErrInvalidConfig); got: %v", err)
	}
}

// TestCreateEventRejectsZeroTimes verifies a zero-value Start or End is
// caught by the same pre-flight guard. A zero time is the documented
// "unset" sentinel — passing it through to Google would yield an opaque
// 400; we surface ErrInvalidConfig instead.
func TestCreateEventRejectsZeroTimes(t *testing.T) {
	c := newAuthedClient(t)

	cases := []struct {
		name  string
		start time.Time
		end   time.Time
	}{
		{
			name:  "zero start",
			start: time.Time{},
			end:   time.Date(2026, 5, 24, 13, 0, 0, 0, time.UTC),
		},
		{
			name:  "zero end",
			start: time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC),
			end:   time.Time{},
		},
		{
			name:  "both zero",
			start: time.Time{},
			end:   time.Time{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := c.CreateEvent(context.Background(), model.CalendarEvent{
				Title: "x",
				Start: tc.start,
				End:   tc.end,
			})
			if err == nil {
				t.Fatal("CreateEvent: expected error for zero times, got nil")
			}
			if !errors.Is(err, ErrInvalidConfig) {
				t.Errorf("expected errors.Is(err, ErrInvalidConfig); got: %v", err)
			}
		})
	}
}

// TestMoveEventRequiresID verifies the empty-id guard. Google interprets
// an empty event id as a list-semantics call on some endpoints, which
// would have surprising side effects; we fail fast.
func TestMoveEventRequiresID(t *testing.T) {
	c := newAuthedClient(t)
	start := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	end := start.Add(1 * time.Hour)
	_, err := c.MoveEvent(context.Background(), "", start, end, "")
	if err == nil {
		t.Fatal("MoveEvent: expected error for empty id, got nil")
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("expected errors.Is(err, ErrInvalidConfig); got: %v", err)
	}
}

// TestDeleteEventRequiresID — same empty-id guard for the delete path.
func TestDeleteEventRequiresID(t *testing.T) {
	c := newAuthedClient(t)
	err := c.DeleteEvent(context.Background(), "")
	if err == nil {
		t.Fatal("DeleteEvent: expected error for empty id, got nil")
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("expected errors.Is(err, ErrInvalidConfig); got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// persistingTokenSource — refresh persistence regression
// ---------------------------------------------------------------------------

// stubTokenSource is a minimal oauth2.TokenSource implementation we can
// script with a sequence of tokens. Each call to Token() pops the next
// element of the queue; if the queue is empty the LAST element is
// returned indefinitely (matching the real oauth2 inner-source behavior
// of caching the most recent unexpired token in memory).
type stubTokenSource struct {
	queue []*oauth2.Token
	calls int
}

func (s *stubTokenSource) Token() (*oauth2.Token, error) {
	s.calls++
	if len(s.queue) == 0 {
		return nil, errors.New("stubTokenSource: queue exhausted (test bug)")
	}
	tok := s.queue[0]
	if len(s.queue) > 1 {
		s.queue = s.queue[1:]
	}
	// If only one entry remains, keep returning it (cached-token model).
	return tok, nil
}

// TestClientRefreshesAndPersists is the regression test for the
// persistingTokenSource contract: Reloader must fire EXACTLY ONCE on a
// real refresh (AccessToken changes) and ZERO times on cached reads (same
// AccessToken returned). This is the load-bearing invariant — calling
// Reloader on every Token() invocation would beat ~/.jarvis/gcal.json with
// pointless writes on every Calendar API call.
//
// We construct the persistingTokenSource directly rather than going through
// NewClient + a real Calendar.Service so the test isolates the wrapper
// logic from Google SDK plumbing.
func TestClientRefreshesAndPersists(t *testing.T) {
	base := model.GCalConfig{
		ClientID:     "cid.apps.googleusercontent.com",
		ClientSecret: "GOCSPX-secret",
		RefreshToken: "1//0g-refresh-original",
		AccessToken:  "access-OLD",
		ExpiresAt:    time.Now().Add(-1 * time.Hour), // expired
	}

	// First Token() returns the original (cached, pre-refresh) token.
	// Second Token() returns a NEW access token — simulates an actual
	// refresh happening inside the inner source.
	// Third Token() returns the same new token — simulates a cached read
	// after the refresh; reload must NOT fire again.
	tokOld := &oauth2.Token{
		AccessToken: "access-OLD",
		Expiry:      time.Now().Add(-1 * time.Hour),
	}
	tokNew := &oauth2.Token{
		AccessToken: "access-NEW",
		Expiry:      time.Now().Add(1 * time.Hour),
		// RefreshToken intentionally empty — Google's documented behavior
		// on refresh responses when the existing refresh token remains
		// valid. The wrapper must PRESERVE base.RefreshToken in that case.
	}
	stub := &stubTokenSource{queue: []*oauth2.Token{tokOld, tokNew, tokNew}}

	// Capture every reload invocation through a buffered channel so we can
	// assert the exact count + payload after the fact.
	reloads := make(chan model.GCalConfig, 4)
	reload := func(cfg model.GCalConfig) error {
		reloads <- cfg
		return nil
	}

	pts := &persistingTokenSource{
		inner:      stub,
		reload:     reload,
		base:       base,
		lastAccess: base.AccessToken, // "access-OLD"
	}

	// Call 1: inner returns tokOld == lastAccess => NO refresh => NO reload.
	got1, err := pts.Token()
	if err != nil {
		t.Fatalf("Token() #1: %v", err)
	}
	if got1.AccessToken != "access-OLD" {
		t.Errorf("Token() #1 AccessToken: got %q, want access-OLD", got1.AccessToken)
	}

	// Call 2: inner returns tokNew (different access token) => REFRESH detected
	// => reload MUST fire exactly once with the updated config.
	got2, err := pts.Token()
	if err != nil {
		t.Fatalf("Token() #2: %v", err)
	}
	if got2.AccessToken != "access-NEW" {
		t.Errorf("Token() #2 AccessToken: got %q, want access-NEW", got2.AccessToken)
	}

	// Call 3: inner returns tokNew again (== new lastAccess) => cached read
	// => reload must NOT fire again.
	got3, err := pts.Token()
	if err != nil {
		t.Fatalf("Token() #3: %v", err)
	}
	if got3.AccessToken != "access-NEW" {
		t.Errorf("Token() #3 AccessToken: got %q, want access-NEW", got3.AccessToken)
	}

	// Drain the reload channel and assert exactly one entry.
	close(reloads)
	var captured []model.GCalConfig
	for c := range reloads {
		captured = append(captured, c)
	}
	if len(captured) != 1 {
		t.Fatalf("reload invocation count: got %d, want 1 (one on refresh, zero on cached reads)", len(captured))
	}

	got := captured[0]
	if got.AccessToken != "access-NEW" {
		t.Errorf("reloaded cfg.AccessToken: got %q, want access-NEW", got.AccessToken)
	}
	// RefreshToken: the refresh response omitted it, so the wrapper must
	// preserve the prior value rather than blanking it out.
	if got.RefreshToken != "1//0g-refresh-original" {
		t.Errorf("reloaded cfg.RefreshToken: got %q, want preserved %q", got.RefreshToken, "1//0g-refresh-original")
	}
	// ClientID/Secret: must be pinned across refresh so the reload payload
	// is a complete, save-ready GCalConfig (Save would otherwise blank the
	// creds out and brick the next launch).
	if got.ClientID != base.ClientID {
		t.Errorf("reloaded cfg.ClientID: got %q, want %q", got.ClientID, base.ClientID)
	}
	if got.ClientSecret != base.ClientSecret {
		t.Errorf("reloaded cfg.ClientSecret: got %q, want %q", got.ClientSecret, base.ClientSecret)
	}
	if !got.ExpiresAt.Equal(tokNew.Expiry) {
		t.Errorf("reloaded cfg.ExpiresAt: got %v, want %v", got.ExpiresAt, tokNew.Expiry)
	}
}

// TestPersistingTokenSourcePreservesRotatedRefreshToken verifies the rare
// path where Google rotates the refresh token (the response carries a NEW
// RefreshToken). The wrapper must adopt the new value rather than
// stubbornly preserving the old one — otherwise the next refresh after
// the rotation would fail with invalid_grant.
func TestPersistingTokenSourcePreservesRotatedRefreshToken(t *testing.T) {
	base := model.GCalConfig{
		ClientID:     "cid",
		ClientSecret: "secret",
		RefreshToken: "refresh-OLD",
		AccessToken:  "access-OLD",
	}
	tokOld := &oauth2.Token{AccessToken: "access-OLD"}
	tokRotated := &oauth2.Token{
		AccessToken:  "access-NEW",
		RefreshToken: "refresh-NEW", // Google rotated the refresh token
		Expiry:       time.Now().Add(1 * time.Hour),
	}
	stub := &stubTokenSource{queue: []*oauth2.Token{tokOld, tokRotated}}

	reloads := make(chan model.GCalConfig, 2)
	pts := &persistingTokenSource{
		inner:      stub,
		reload:     func(c model.GCalConfig) error { reloads <- c; return nil },
		base:       base,
		lastAccess: base.AccessToken,
	}

	if _, err := pts.Token(); err != nil { // cached read
		t.Fatalf("Token() #1: %v", err)
	}
	if _, err := pts.Token(); err != nil { // refresh w/ rotation
		t.Fatalf("Token() #2: %v", err)
	}

	close(reloads)
	var got model.GCalConfig
	count := 0
	for c := range reloads {
		got = c
		count++
	}
	if count != 1 {
		t.Fatalf("reload count: got %d, want 1", count)
	}
	if got.RefreshToken != "refresh-NEW" {
		t.Errorf("rotated RefreshToken: got %q, want refresh-NEW", got.RefreshToken)
	}
}
