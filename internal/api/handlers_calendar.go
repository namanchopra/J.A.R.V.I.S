package api

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/namanchopra/jarvis/internal/model"

	"github.com/labstack/echo/v4"
)

// CalendarProvider is the App-facing surface the calendar HTTP handlers
// depend on. It mirrors the existing Wails bindings on *App so the mobile
// HTTP API exposes the same Google Calendar reads the Mac frontend gets
// — keeping a single source of truth for "next event" / "upcoming events"
// regardless of which surface is asking.
//
// Authentication failures (ErrNotAuthenticated) are surfaced as 200 +
// empty payload by the handlers below rather than as 4xx, matching the
// existing pattern: a disconnected Google account is a normal "no data
// yet" state, not an API error the mobile client should retry on.
type CalendarProvider interface {
	// Returns a compact pre-formatted projection (with RelativeTime like
	// "in 14m") so mobile clients don't ship their own time-math.
	GoogleCalendarGetNextEvent() (*model.NextEventSnapshot, error)
	GoogleCalendarGetUpcomingEvents(limit int) ([]model.CalendarEvent, error)
	GoogleCalendarIsConnected() bool
}

// RegisterCalendarRoutes mounts two read-only GET endpoints onto the Echo
// route group:
//
//	GET /calendar/next        — the very next upcoming event, or null when
//	                            the calendar is empty / not connected.
//	GET /calendar/upcoming    — up to ?limit=N events ordered by start time
//	                            (default 10, max 50). Always [] when not
//	                            connected — never null, so mobile clients
//	                            can render a stable empty state.
func RegisterCalendarRoutes(g *echo.Group, app CalendarProvider) {
	h := &calendarHandler{app: app}

	g.GET("/calendar/next", h.handleNext)
	g.GET("/calendar/upcoming", h.handleUpcoming)
}

type calendarHandler struct {
	app CalendarProvider
}

// nextResponse is the JSON shape of GET /calendar/next. Connected is the
// authoritative "is Google Calendar wired up?" boolean so the mobile UI
// can swap to a "Connect Google Calendar" CTA without inferring from a
// null event.
type nextResponse struct {
	Connected bool                       `json:"connected"`
	Event     *model.NextEventSnapshot   `json:"event"` // nil when calendar empty
}

func (h *calendarHandler) handleNext(c echo.Context) error {
	connected := h.app.GoogleCalendarIsConnected()
	if !connected {
		return c.JSON(http.StatusOK, nextResponse{Connected: false, Event: nil})
	}

	evt, err := h.app.GoogleCalendarGetNextEvent()
	if err != nil {
		// The Wails binding already collapses ErrNotAuthenticated into
		// (nil, nil) so any remaining error is a genuine API failure
		// (network, parse, etc). Log + 500.
		slog.Error("calendar: GetNextEvent failed", "err", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "failed to read next calendar event",
		})
	}
	return c.JSON(http.StatusOK, nextResponse{Connected: true, Event: evt})
}

// upcomingResponse is the JSON shape of GET /calendar/upcoming. Events is
// always non-nil so mobile clients can `.map` without a null guard.
type upcomingResponse struct {
	Connected bool                  `json:"connected"`
	Events    []model.CalendarEvent `json:"events"`
}

func (h *calendarHandler) handleUpcoming(c echo.Context) error {
	connected := h.app.GoogleCalendarIsConnected()
	if !connected {
		return c.JSON(http.StatusOK, upcomingResponse{Connected: false, Events: []model.CalendarEvent{}})
	}

	limit := 10
	if raw := c.QueryParam("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if limit > 50 {
		limit = 50
	}

	events, err := h.app.GoogleCalendarGetUpcomingEvents(limit)
	if err != nil {
		slog.Error("calendar: GetUpcomingEvents failed", "err", err, "limit", limit)
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "failed to read upcoming calendar events",
		})
	}
	if events == nil {
		events = []model.CalendarEvent{}
	}
	return c.JSON(http.StatusOK, upcomingResponse{Connected: true, Events: events})
}
