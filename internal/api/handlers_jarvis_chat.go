package api

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
)

// ---------------------------------------------------------------------------
// Jarvis chat endpoint — POST /jarvis/chat
//
// Sends a text command to the Jarvis daemon over its WebSocket and waits
// (up to 30 seconds) for the response message. This lets mobile clients
// interact with Jarvis without needing their own persistent WebSocket.
// ---------------------------------------------------------------------------

// chatRequestTimeout is the maximum time the handler waits for the daemon
// to produce a response before returning a 504 Gateway Timeout.
const chatRequestTimeout = 30 * time.Second

// chatResponseChSize is the buffer size for the per-request response channel.
// A buffer of 1 ensures the WebSocket read loop never blocks when posting a
// response, even if the HTTP handler hasn't started reading yet.
const chatResponseChSize = 1

// chatRequest is the JSON body accepted by POST /jarvis/chat.
type chatRequest struct {
	Message string `json:"message"`
}

// chatResponse is the JSON body returned on success.
type chatResponse struct {
	Response string `json:"response"`
}

// RegisterJarvisChatRoute mounts the POST /jarvis/chat endpoint on the given
// Echo group. It requires the JarvisDaemonConn that was created when the
// Jarvis WS route was registered.
func RegisterJarvisChatRoute(g *echo.Group, conn *JarvisDaemonConn) {
	g.POST("/jarvis/chat", handleJarvisChat(conn))
}

// handleJarvisChat returns an Echo handler that sends a command to the Jarvis
// daemon and waits for the response.
func handleJarvisChat(conn *JarvisDaemonConn) echo.HandlerFunc {
	return func(c echo.Context) error {
		// Verify daemon is connected.
		if conn == nil || !conn.Connected() {
			return c.JSON(http.StatusServiceUnavailable, map[string]string{
				"error": "jarvis daemon not connected",
			})
		}

		// Parse and validate request body.
		var req chatRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"error": "invalid request body",
			})
		}
		req.Message = strings.TrimSpace(req.Message)
		if req.Message == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"error": "message is required",
			})
		}

		// Create a one-shot channel for this request's response.
		ch := make(chan string, chatResponseChSize)
		conn.registerChatWaiter(ch)
		defer conn.unregisterChatWaiter(ch)

		// Send the command to the daemon.
		outgoing := JarvisOutgoing{
			Type: "command",
			Text: req.Message,
		}
		if err := conn.Send(outgoing); err != nil {
			slog.Error("jarvis chat: failed to send command", "err", err)
			return c.JSON(http.StatusBadGateway, map[string]string{
				"error": "failed to send command to jarvis daemon",
			})
		}

		slog.Info("jarvis chat: command sent, waiting for response", "message", truncate(req.Message, 80))

		// Block until we get a response or time out.
		select {
		case resp := <-ch:
			return c.JSON(http.StatusOK, chatResponse{Response: resp})
		case <-time.After(chatRequestTimeout):
			slog.Warn("jarvis chat: timeout waiting for daemon response", "message", truncate(req.Message, 80))
			return c.JSON(http.StatusGatewayTimeout, map[string]string{
				"error": "jarvis daemon did not respond within 30 seconds",
			})
		case <-c.Request().Context().Done():
			return c.JSON(http.StatusRequestTimeout, map[string]string{
				"error": "request cancelled",
			})
		}
	}
}

// ---------------------------------------------------------------------------
// Chat waiter plumbing on JarvisDaemonConn
//
// These methods manage a set of channels that are waiting for a "response"
// message from the daemon. When the WS read loop receives a response-type
// message, it fans it out to all registered waiters (non-blocking).
// ---------------------------------------------------------------------------

// registerChatWaiter adds a channel that will receive the next response text.
func (d *JarvisDaemonConn) registerChatWaiter(ch chan string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.chatWaiters = append(d.chatWaiters, ch)
}

// unregisterChatWaiter removes a channel from the waiter set.
func (d *JarvisDaemonConn) unregisterChatWaiter(ch chan string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i, w := range d.chatWaiters {
		if w == ch {
			d.chatWaiters = append(d.chatWaiters[:i], d.chatWaiters[i+1:]...)
			return
		}
	}
}

// notifyChatWaiters sends the response text to all registered waiters
// (non-blocking) and clears the waiter list.
func (d *JarvisDaemonConn) notifyChatWaiters(text string) {
	d.mu.Lock()
	waiters := make([]chan string, len(d.chatWaiters))
	copy(waiters, d.chatWaiters)
	d.chatWaiters = nil
	d.mu.Unlock()

	for _, ch := range waiters {
		select {
		case ch <- text:
		default:
			// Channel is full or closed — drop silently.
		}
	}
}

