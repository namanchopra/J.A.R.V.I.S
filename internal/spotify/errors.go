// Package spotify provides the Web API client + OAuth (Authorization Code
// with PKCE) flow Jarvis uses to talk to Spotify on behalf of the user.
//
// Layout:
//   - oauth.go    — PKCE flow + token endpoint exchange/refresh + one-shot
//                   localhost callback server.
//   - web.go      — Web API HTTP client (search, currently-playing) with
//                   auto-refresh of expired access tokens.
//   - errors.go   — Sentinel errors callers branch on with errors.Is.
//
// This package never touches Spotify.app via AppleScript — that's the job
// of internal/spotify/applescript.go (TASK-007). The two halves are wired
// together in app_spotify.go (TASK-009): the Web API is used for search +
// "what's playing", AppleScript is used to actually play.
package spotify

import "errors"

// ErrNotAuthenticated is returned by Web API calls when no access token is
// available (user has never signed in, or signed out). Callers map this to
// a user-facing prompt to reconnect.
var ErrNotAuthenticated = errors.New("spotify: not authenticated")

// ErrInvalidQuery is returned by Search when the query is empty or
// whitespace-only. We surface this without issuing an HTTP request so a
// stray voice misfire ("Play …") doesn't burn rate-limit quota against the
// Spotify API.
var ErrInvalidQuery = errors.New("spotify: invalid query")

// ErrTokenRefreshFailed is returned when the refresh-token grant against
// Spotify's /api/token endpoint comes back non-2xx. Wraps the underlying
// HTTP error via fmt.Errorf("%w: ...") so callers can detect via
// errors.Is(err, ErrTokenRefreshFailed).
var ErrTokenRefreshFailed = errors.New("spotify: token refresh failed")
