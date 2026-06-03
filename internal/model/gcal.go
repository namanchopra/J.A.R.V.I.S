package model

import "time"

// ---------------------------------------------------------------------------
// GCalConfig
// ---------------------------------------------------------------------------

// GCalConfig holds the persisted credentials Jarvis needs to talk to the
// Google Calendar API on behalf of the user. It is embedded under the
// `gcal` key of the top-level Jarvis config (~/.jarvis/config.json) by
// `internal/config.Config.GCal`.
//
// v0.3.0 TASK-001 ships this as a skeleton only — the real OAuth flow that
// populates AccessToken/RefreshToken/ExpiresAt lands in later tasks, and the
// real Wails bindings that read/write it land alongside them. ClientID and
// ClientSecret are the Google Cloud OAuth client credentials; left as struct
// fields (rather than global constants) so users with their own Google Cloud
// project can bring their own credentials instead of relying on a
// Jarvis-distributed pair.
//
// All `omitempty` so a default config file does not leak empty-string
// credential placeholders to disk — the keys only appear once the user has
// actually signed in.
type GCalConfig struct {
	// AccessToken is the short-lived OAuth bearer token. Refreshed via
	// RefreshToken when ExpiresAt is in the past.
	AccessToken string `json:"accessToken,omitempty"`

	// RefreshToken is the long-lived token used to mint new access tokens
	// without a fresh user-facing OAuth flow. This is the load-bearing
	// signal for `IsConnected()` — access tokens cycle, refresh tokens
	// persist across the lifetime of the grant.
	RefreshToken string `json:"refreshToken,omitempty"`

	// ExpiresAt is the wall-clock time at which AccessToken stops being
	// valid. The token-refresh gate compares against time.Now().
	ExpiresAt time.Time `json:"expiresAt,omitempty"`

	// ClientID is the Google Cloud OAuth client ID this install is
	// configured against. Empty means "use the Jarvis-bundled default".
	ClientID string `json:"clientId,omitempty"`

	// ClientSecret is the Google Cloud OAuth client secret paired with
	// ClientID. Empty means "use the Jarvis-bundled default".
	ClientSecret string `json:"clientSecret,omitempty"`
}

// IsConnected reports whether the config currently holds a non-empty refresh
// token. The refresh token — not the access token — is the load-bearing
// signal here: access tokens cycle on a sub-hour cadence, but the presence
// of a refresh token means we can mint a new access token without bouncing
// the user back through OAuth. It does NOT consult ExpiresAt — that's the
// responsibility of the token-refresh layer. Callers wanting "is the
// access token also fresh?" must check ExpiresAt separately.
//
// Derived helper rather than a stored field so we never have a stale
// `connected: true` left on disk after a sign-out.
func (c GCalConfig) IsConnected() bool {
	return c.RefreshToken != ""
}

// NewGCalConfig returns a zero-value-safe GCalConfig. Every field is at
// its Go zero value, which marshals to an empty JSON object thanks to the
// `omitempty` tags — safe to persist into config.json without leaking
// credential placeholders.
func NewGCalConfig() GCalConfig {
	return GCalConfig{}
}

// ---------------------------------------------------------------------------
// CalendarEvent
// ---------------------------------------------------------------------------

// CalendarEvent is the Jarvis-internal representation of a single Google
// Calendar event. It is decoupled from the upstream
// google.golang.org/api/calendar/v3 types so callers (Wails bindings, the
// mobile API, the daemon tool bridge) can consume a stable, JSON-friendly
// shape without pulling the Google SDK into every layer.
//
// Times are stored as `time.Time` rather than RFC3339 strings so callers can
// do duration math without re-parsing. JSON-serialized output will use the
// default time.Time RFC3339 encoding.
type CalendarEvent struct {
	// ID is the Google Calendar event ID, stable across edits.
	ID string `json:"id,omitempty"`

	// Title is the event summary (Google's `summary` field).
	Title string `json:"title,omitempty"`

	// Start is the event start time. For all-day events this is the
	// midnight boundary in the event's timezone.
	Start time.Time `json:"start,omitempty"`

	// End is the event end time.
	End time.Time `json:"end,omitempty"`

	// Attendees is the list of attendee email addresses. Empty/nil when
	// the event has no attendees or the user lacks read access to them.
	Attendees []string `json:"attendees,omitempty"`

	// Location is the free-form location string (Google's `location`).
	Location string `json:"location,omitempty"`

	// HTMLLink is the URL to view the event in the Google Calendar web UI.
	HTMLLink string `json:"htmlLink,omitempty"`

	// TimeZone is the IANA timezone name (e.g. "Asia/Dubai") the event
	// is anchored to. Stored separately from Start/End so DST shifts after
	// creation move the event with the user's wall clock rather than
	// drifting against UTC.
	TimeZone string `json:"timeZone,omitempty"`
}

// ---------------------------------------------------------------------------
// NextEventSnapshot
// ---------------------------------------------------------------------------

// NextEventSnapshot is the compact, mobile-friendly projection of "what's
// next on the user's calendar" that the HUD renders verbatim. The server
// formats RelativeTime ("in 14m" / "now" / "in 2h") so the mobile client
// doesn't have to ship its own time-math — keeping the rendering rules in
// one place (Go) and the mobile app a thin renderer.
//
// All `omitempty` so an empty snapshot (no upcoming events) marshals to
// `{}` rather than a payload of empty strings.
type NextEventSnapshot struct {
	// Title is the event summary, copied from CalendarEvent.Title.
	Title string `json:"title,omitempty"`

	// StartISO is the event start time formatted as RFC3339. Mobile may
	// re-parse this for tooltips, but the primary display field is
	// RelativeTime.
	StartISO string `json:"startISO,omitempty"`

	// RelativeTime is a human-readable, server-rendered countdown such as
	// "in 14m", "in 2h", or "now". The mobile HUD renders this verbatim
	// without further formatting.
	RelativeTime string `json:"relativeTime,omitempty"`
}
