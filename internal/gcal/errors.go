// Package gcal provides the Google Calendar API client + OAuth (Authorization
// Code with PKCE) flow Jarvis uses to read/write events on behalf of the user.
//
// Layout:
//   - oauth.go    — PKCE flow + token endpoint exchange/refresh + one-shot
//                   localhost callback server.
//   - client.go   — Calendar API HTTP client (list/insert events) with
//                   auto-refresh of expired access tokens.
//   - store.go    — Encrypted on-disk token persistence.
//   - errors.go   — Sentinel errors callers branch on with errors.Is.
//
// The daemon tool layer (scripts/jarvis-daemon/tools.py via the tool bridge)
// branches on these sentinels to surface user-friendly messages like
// "connect Google Calendar first" instead of leaking SDK stack traces.
package gcal

import (
	"errors"
	"fmt"
)

// calendarAPIError is the concrete type returned by ErrCalendarAPI. It carries
// the underlying SDK error so errors.Unwrap recovers the cause, and reports
// itself as errCalendarAPI via Is so callers can branch on a single sentinel.
type calendarAPIError struct {
	wrap error
}

func (e *calendarAPIError) Error() string {
	if e.wrap == nil {
		return errCalendarAPI.Error()
	}
	return fmt.Sprintf("%s: %s", errCalendarAPI.Error(), e.wrap.Error())
}

// Unwrap exposes the wrapped underlying error so callers can recover the
// SDK-specific cause via errors.Unwrap or errors.As.
func (e *calendarAPIError) Unwrap() error { return e.wrap }

// Is lets callers detect the family with either of:
//
//	errors.Is(err, gcal.ErrCalendarAPI(nil))   // common path
//	errors.Is(err, sentinel)                   // where sentinel is the internal
//	                                           // errCalendarAPI value
//
// Any *calendarAPIError matches any other *calendarAPIError because they all
// share the same logical sentinel identity.
func (e *calendarAPIError) Is(target error) bool {
	if target == errCalendarAPI {
		return true
	}
	_, ok := target.(*calendarAPIError)
	return ok
}

// ErrNotAuthenticated is returned by Calendar API calls when no access token
// is available (user has never signed in, or signed out, or the encrypted
// token store is empty). Callers map this to a user-facing prompt to
// reconnect Google Calendar in Settings.
var ErrNotAuthenticated = errors.New("gcal: not authenticated")

// ErrInvalidConfig is returned when the Google OAuth client credentials are
// malformed or empty (missing client ID / client secret). We surface this
// distinctly from ErrNotAuthenticated so Settings can show "configure
// credentials" vs. "sign in" as different remediation paths.
var ErrInvalidConfig = errors.New("gcal: invalid config")

// errCalendarAPI is the internal sentinel that every error returned by
// ErrCalendarAPI satisfies via errors.Is. Kept unexported so callers go
// through the constructor — that way the wrapped underlying error is always
// preserved and we never lose the cause to a bare sentinel comparison.
var errCalendarAPI = errors.New("gcal: calendar api error")

// ErrCalendarAPI wraps an underlying Google Calendar SDK error so callers can
// branch on it via errors.Is(err, ErrCalendarAPI(nil)) while still recovering
// the cause via errors.Unwrap(err) — which returns the original wrapped err,
// not the sentinel.
//
// Defensive: if wrap is nil we still return a non-nil sentinel-only error so
// calling code can errors.Is(...) the result without nil-pointer panics.
func ErrCalendarAPI(wrap error) error {
	return &calendarAPIError{wrap: wrap}
}
