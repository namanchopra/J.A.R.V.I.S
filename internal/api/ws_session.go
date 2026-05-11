package api

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
)

// TerminalProvider abstracts the App method needed to read live terminal
// output for a Claude Code session identified by PID.
type TerminalProvider interface {
	GetSessionTerminalOutput(pid int) (string, error)
}

// WSHandler serves WebSocket endpoints for streaming terminal output.
type WSHandler struct {
	app   TerminalProvider
	token func() string // returns the server's current auth token
}

// NewWSHandler creates a WSHandler that reads terminal output from app and
// validates WebSocket connections against the token returned by tokenFn.
func NewWSHandler(app TerminalProvider, tokenFn func() string) *WSHandler {
	return &WSHandler{
		app:   app,
		token: tokenFn,
	}
}

// upgrader allows all origins to match the CORS policy configured in
// server.go (AllowOrigins: ["*"]).
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// pollInterval is how often we check for new terminal output.
const pollInterval = 500 * time.Millisecond

// Custom WebSocket close codes.
const (
	closeCodeUnauthorized = 4001
)

// RegisterWSRoutes mounts WebSocket endpoints on the provided Echo group.
// The tokenFn is typically wired to the Server's atomic token value so that
// WebSocket connections can be authenticated via query parameter.
func RegisterWSRoutes(g *echo.Group, app TerminalProvider, tokenFn func() string) {
	h := NewWSHandler(app, tokenFn)

	g.GET("/ws/sessions/:id/output", h.HandleSessionOutput)
}

// HandleSessionOutput upgrades the HTTP connection to a WebSocket and streams
// live terminal output for the session identified by :id (a PID). Auth is
// performed via the ?token= query parameter since WebSocket clients cannot
// set HTTP headers reliably.
//
// Protocol:
//   - On connect: sends the full current terminal buffer as a single text message.
//   - Every 500ms: polls for new output and sends only the delta (newly appended text).
//   - On client disconnect or context cancellation: stops cleanly.
func (h *WSHandler) HandleSessionOutput(c echo.Context) error {
	// Parse PID from path parameter.
	pidStr := c.Param("id")
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "id must be a valid integer PID",
		})
	}

	// Upgrade to WebSocket first, then validate token. The WebSocket spec
	// requires the upgrade to happen before we can send close frames.
	ws, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
	if err != nil {
		slog.Error("websocket upgrade failed", "err", err, "pid", pid)
		// Upgrade already wrote an HTTP error response; return nil so Echo
		// does not attempt to write another response.
		return nil
	}
	defer ws.Close()

	// Authenticate via ?token= query parameter.
	provided := c.QueryParam("token")
	expected := h.token()
	if expected == "" || provided != expected {
		slog.Warn("websocket auth failed", "pid", pid)
		ws.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(closeCodeUnauthorized, "unauthorized"))
		return nil
	}

	logger := slog.With("pid", pid, "remote", c.RealIP())
	logger.Info("websocket connected for session output")

	// Send initial full output.
	output, err := h.app.GetSessionTerminalOutput(pid)
	if err != nil {
		logger.Warn("session not found or unavailable", "err", err)
		ws.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "session not found"))
		return nil
	}

	if err := ws.WriteMessage(websocket.TextMessage, []byte(output)); err != nil {
		logger.Debug("failed to send initial output", "err", err)
		return nil
	}

	lastOutput := output

	// Start a goroutine to detect client disconnect by reading control frames.
	// When the client closes the connection, ReadMessage returns an error and
	// we cancel the context to stop the polling loop.
	ctx := c.Request().Context()
	clientGone := make(chan struct{})
	go func() {
		defer close(clientGone)
		for {
			_, _, err := ws.ReadMessage()
			if err != nil {
				return
			}
		}
	}()

	// Polling loop: check for new output every pollInterval.
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("websocket closing: context cancelled")
			return nil

		case <-clientGone:
			logger.Info("websocket closing: client disconnected")
			return nil

		case <-ticker.C:
			newOutput, err := h.app.GetSessionTerminalOutput(pid)
			if err != nil {
				logger.Warn("terminal read error, closing websocket", "err", err)
				ws.WriteMessage(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, "session unavailable"))
				return nil
			}

			// Only send the delta if there is new content appended.
			if len(newOutput) > len(lastOutput) {
				delta := newOutput[len(lastOutput):]
				if err := ws.WriteMessage(websocket.TextMessage, []byte(delta)); err != nil {
					logger.Debug("failed to send delta, client likely disconnected", "err", err)
					return nil
				}
				lastOutput = newOutput
			} else if newOutput != lastOutput {
				// Content changed but didn't grow (e.g. terminal cleared/scrolled).
				// Send the full new content so the client can re-render.
				if err := ws.WriteMessage(websocket.TextMessage, []byte(newOutput)); err != nil {
					logger.Debug("failed to send full refresh", "err", err)
					return nil
				}
				lastOutput = newOutput
			}
		}
	}
}
