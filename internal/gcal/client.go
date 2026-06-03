// Google Calendar v3 API client — sibling to oauth.go and store.go.
//
// The Client wraps *calendar.Service with two pieces of plumbing the SDK
// does not give us for free:
//
//  1. A persisting token source. The SDK's oauth2.TokenSource silently
//     refreshes an expired access token on the next API call, but it has
//     no hook to write the new token back to disk. Without persistence,
//     the next launch of Jarvis would start from a stale AccessToken /
//     ExpiresAt and pay another refresh round-trip on the very first
//     call — and worse, if Google rotated the refresh token (rare but
//     happens when grants are re-issued), we'd lose it. persistingTokenSource
//     diffs against the last-seen AccessToken and invokes the supplied
//     Reloader exactly once per actual refresh so the App layer can write
//     the updated GCalConfig back through SaveConfig.
//
//  2. Boundary validation. The SDK happily accepts events with End ≤
//     Start (Google returns a 400 from the API) and Move/Delete with an
//     empty ID (which Google interprets as "list" semantics on some
//     endpoints). We fail fast with ErrInvalidConfig before the network
//     hop so callers get a stable sentinel they can branch on.
package gcal

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/namanchopra/jarvis/internal/model"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
)

// primaryCalendarID is the well-known alias Google Calendar exposes for the
// signed-in user's default calendar. Hard-coded because Jarvis intentionally
// scopes to one calendar — multi-calendar support is out of scope for v0.3.0.
const primaryCalendarID = "primary"

// Reloader persists a refreshed GCalConfig back to disk (typically through
// the App layer's SaveConfig wrapper). It is invoked by the client's
// persistingTokenSource exactly once per actual access-token refresh — never
// on cached reads of an unchanged token. Returning an error from Reloader
// does NOT fail the underlying API call; it only logs at the App layer,
// because losing a token-rotation write is recoverable (the next refresh
// will retry) whereas failing the user-visible call is not.
type Reloader func(model.GCalConfig) error

// Client is a thin wrapper over *calendar.Service that handles token
// refresh + persistence transparently. Constructed via NewClient.
type Client struct {
	svc *calendar.Service
}

// NewClient builds a Client from the persisted config + a Reloader.
// Returns ErrInvalidConfig if ClientID or ClientSecret is missing, and
// ErrNotAuthenticated if RefreshToken is empty (no token to refresh —
// the user has never completed the OAuth flow).
func NewClient(ctx context.Context, cfg model.GCalConfig, reload Reloader) (*Client, error) {
	if cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil, ErrInvalidConfig
	}
	if cfg.RefreshToken == "" {
		return nil, ErrNotAuthenticated
	}

	oauthCfg := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		Endpoint:     google.Endpoint,
		Scopes:       []string{calendar.CalendarEventsScope},
	}
	tok := &oauth2.Token{
		AccessToken:  cfg.AccessToken,
		RefreshToken: cfg.RefreshToken,
		Expiry:       cfg.ExpiresAt,
	}

	src := oauthCfg.TokenSource(ctx, tok)
	persistingSrc := &persistingTokenSource{
		inner:      src,
		reload:     reload,
		base:       cfg,
		lastAccess: cfg.AccessToken,
	}

	svc, err := calendar.NewService(ctx, option.WithTokenSource(persistingSrc))
	if err != nil {
		return nil, ErrCalendarAPI(fmt.Errorf("NewClient: calendar.NewService: %w", err))
	}
	return &Client{svc: svc}, nil
}

// ListUpcoming returns the next n upcoming events on the user's primary
// calendar, sorted by start time ascending. Excludes events that have
// already started (TimeMin = now). Returns []model.CalendarEvent{} (never
// nil) on an empty calendar so JSON serialization yields `[]` rather than
// `null` for Wails consumers.
func (c *Client) ListUpcoming(ctx context.Context, n int) ([]model.CalendarEvent, error) {
	if n <= 0 {
		return []model.CalendarEvent{}, nil
	}
	call := c.svc.Events.List(primaryCalendarID).
		Context(ctx).
		SingleEvents(true).
		OrderBy("startTime").
		TimeMin(time.Now().Format(time.RFC3339)).
		MaxResults(int64(n))

	resp, err := call.Do()
	if err != nil {
		return nil, ErrCalendarAPI(err)
	}
	if resp == nil || len(resp.Items) == 0 {
		return []model.CalendarEvent{}, nil
	}

	out := make([]model.CalendarEvent, 0, len(resp.Items))
	for _, item := range resp.Items {
		out = append(out, toModelEvent(item))
	}
	// Belt-and-braces: OrderBy("startTime") already returns ascending, but
	// we re-sort defensively so callers can rely on the contract without
	// trusting the upstream order on edge cases (e.g. recurring expansions).
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out, nil
}

// NextEvent returns the very next upcoming event, or (nil, nil) when the
// calendar has no upcoming events. The (nil, nil) sentinel is the explicit
// API contract — callers branch on `evt == nil` rather than on a sentinel
// error so "empty calendar" is a normal state, not an error.
func (c *Client) NextEvent(ctx context.Context) (*model.CalendarEvent, error) {
	events, err := c.ListUpcoming(ctx, 1)
	if err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return nil, nil
	}
	return &events[0], nil
}

// CreateEvent inserts a new event on the user's primary calendar and
// returns the event populated with its server-assigned ID and HTMLLink.
// Validates Start < End before the API call — callers get ErrInvalidConfig
// without paying a network round-trip on obviously-wrong input.
func (c *Client) CreateEvent(ctx context.Context, evt model.CalendarEvent) (model.CalendarEvent, error) {
	if evt.Start.IsZero() || evt.End.IsZero() || !evt.Start.Before(evt.End) {
		return model.CalendarEvent{}, ErrInvalidConfig
	}

	apiEvt := &calendar.Event{
		Summary:  evt.Title,
		Location: evt.Location,
		Start: &calendar.EventDateTime{
			DateTime: evt.Start.Format(time.RFC3339),
			TimeZone: evt.TimeZone,
		},
		End: &calendar.EventDateTime{
			DateTime: evt.End.Format(time.RFC3339),
			TimeZone: evt.TimeZone,
		},
	}
	if len(evt.Attendees) > 0 {
		apiEvt.Attendees = make([]*calendar.EventAttendee, 0, len(evt.Attendees))
		for _, email := range evt.Attendees {
			if email == "" {
				continue
			}
			apiEvt.Attendees = append(apiEvt.Attendees, &calendar.EventAttendee{Email: email})
		}
	}

	created, err := c.svc.Events.Insert(primaryCalendarID, apiEvt).Context(ctx).Do()
	if err != nil {
		return model.CalendarEvent{}, ErrCalendarAPI(err)
	}
	return toModelEvent(created), nil
}

// MoveEvent updates the start + end times of an existing event identified
// by id. Empty id returns ErrInvalidConfig before any network call.
// Other event fields (title, attendees, location) are preserved by issuing
// a Patch rather than an Update. timeZone is the IANA zone the new
// timestamps are anchored to (e.g. "Asia/Dubai"); empty string preserves
// the event's existing timezone.
func (c *Client) MoveEvent(ctx context.Context, id string, newStart, newEnd time.Time, timeZone string) (model.CalendarEvent, error) {
	if id == "" {
		return model.CalendarEvent{}, ErrInvalidConfig
	}
	if newStart.IsZero() || newEnd.IsZero() || !newStart.Before(newEnd) {
		return model.CalendarEvent{}, ErrInvalidConfig
	}

	patch := &calendar.Event{
		Start: &calendar.EventDateTime{DateTime: newStart.Format(time.RFC3339), TimeZone: timeZone},
		End:   &calendar.EventDateTime{DateTime: newEnd.Format(time.RFC3339), TimeZone: timeZone},
	}
	updated, err := c.svc.Events.Patch(primaryCalendarID, id, patch).Context(ctx).Do()
	if err != nil {
		return model.CalendarEvent{}, ErrCalendarAPI(err)
	}
	return toModelEvent(updated), nil
}

// DeleteEvent deletes an event by ID. Empty id returns ErrInvalidConfig.
// Google returns 204 No Content on success — the SDK collapses that to a
// nil error here.
func (c *Client) DeleteEvent(ctx context.Context, id string) error {
	if id == "" {
		return ErrInvalidConfig
	}
	if err := c.svc.Events.Delete(primaryCalendarID, id).Context(ctx).Do(); err != nil {
		return ErrCalendarAPI(err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// persistingTokenSource — refreshes write back to disk
// ---------------------------------------------------------------------------

// persistingTokenSource wraps an inner oauth2.TokenSource and invokes a
// Reloader callback when the inner source returns a token whose AccessToken
// differs from the last one we saw. The diff (rather than e.g. an
// "always reload" approach) is what guarantees the success criterion of
// "reload called exactly once per actual refresh" — the inner source caches
// unexpired tokens in memory and returns the same struct repeatedly, so
// AccessToken equality is a reliable signal that no refresh happened.
type persistingTokenSource struct {
	inner  oauth2.TokenSource
	reload Reloader
	mu     sync.Mutex // guards lastAccess + base, since SDK may call Token() from any goroutine.
	// base is the persisted config we mutate-and-forward to reload.
	// We keep the original ClientID/ClientSecret pinned across refreshes
	// so the reload payload is a complete, save-ready GCalConfig.
	base model.GCalConfig
	// lastAccess is the AccessToken we most recently observed. A diff
	// against this value is the trigger for invoking reload.
	lastAccess string
}

// Token implements oauth2.TokenSource. Delegates to the inner source, and
// when the returned access token differs from the previously seen one,
// invokes the Reloader with an updated GCalConfig. Errors from Reloader
// are intentionally swallowed at this layer — losing a token-rotation
// write is recoverable on the next refresh, but failing the user-visible
// API call is not.
func (p *persistingTokenSource) Token() (*oauth2.Token, error) {
	tok, err := p.inner.Token()
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if tok.AccessToken == p.lastAccess {
		// No refresh occurred — same token cached in memory.
		return tok, nil
	}

	// A refresh happened. Build the updated config:
	//   - AccessToken / Expiry come from the new token.
	//   - RefreshToken: Google omits this field on refresh responses when
	//     the existing refresh token remains valid (the common case), so
	//     we preserve the prior value unless the response actually carries
	//     a new one (the rare rotation case).
	updated := p.base
	updated.AccessToken = tok.AccessToken
	updated.ExpiresAt = tok.Expiry
	if tok.RefreshToken != "" {
		updated.RefreshToken = tok.RefreshToken
	}

	if p.reload != nil {
		_ = p.reload(updated)
	}
	p.base = updated
	p.lastAccess = tok.AccessToken
	return tok, nil
}

// ---------------------------------------------------------------------------
// SDK → domain conversion
// ---------------------------------------------------------------------------

// toModelEvent maps a Calendar v3 SDK event to Jarvis's domain
// CalendarEvent. Times come from the SDK as RFC3339 strings under either
// DateTime (timed events) or Date (all-day events); we parse DateTime when
// present and fall back to Date for all-day events. Unparseable times are
// silently dropped to time.Time{} so a single malformed event does not
// break the whole list — the caller's UI can render "TBD" for zero times.
func toModelEvent(e *calendar.Event) model.CalendarEvent {
	if e == nil {
		return model.CalendarEvent{}
	}
	out := model.CalendarEvent{
		ID:       e.Id,
		Title:    e.Summary,
		Location: e.Location,
		HTMLLink: e.HtmlLink,
	}
	if e.Start != nil {
		out.Start = parseEventTime(e.Start)
	}
	if e.End != nil {
		out.End = parseEventTime(e.End)
	}
	if len(e.Attendees) > 0 {
		out.Attendees = make([]string, 0, len(e.Attendees))
		for _, a := range e.Attendees {
			if a == nil || a.Email == "" {
				continue
			}
			out.Attendees = append(out.Attendees, a.Email)
		}
	}
	return out
}

// parseEventTime extracts a time.Time from a calendar.EventDateTime,
// preferring DateTime (RFC3339 with timezone) over Date (YYYY-MM-DD for
// all-day events). Returns the zero time on parse failure rather than an
// error so a single bad event doesn't poison the surrounding list.
func parseEventTime(edt *calendar.EventDateTime) time.Time {
	if edt == nil {
		return time.Time{}
	}
	if edt.DateTime != "" {
		if t, err := time.Parse(time.RFC3339, edt.DateTime); err == nil {
			return t
		}
	}
	if edt.Date != "" {
		// All-day events use YYYY-MM-DD with no timezone. Parse as local
		// midnight so duration math on the consumer side stays in the
		// user's wall clock.
		if t, err := time.ParseInLocation("2006-01-02", edt.Date, time.Local); err == nil {
			return t
		}
	}
	return time.Time{}
}
