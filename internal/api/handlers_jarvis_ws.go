package api

import (
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
)

// ---------------------------------------------------------------------------
// Jarvis daemon WebSocket endpoint
//
// The Python voice daemon connects to /ws/jarvis for bidirectional JSON
// communication. No authentication is required because the daemon runs on
// the same machine.
// ---------------------------------------------------------------------------

// JarvisEventEmitter is called whenever the daemon sends state, transcript,
// response, or audio_level messages. The implementation should forward these
// to the Wails frontend via runtime.EventsEmit.
type JarvisEventEmitter func(event interface{})

// JarvisDaemonConn holds the active WebSocket connection to the Jarvis Python
// daemon. It is safe for concurrent use.
type JarvisDaemonConn struct {
	mu          sync.Mutex
	conn        *websocket.Conn
	chatWaiters []chan string // channels waiting for a "response" message (see handlers_jarvis_chat.go)
}

// Send marshals msg as JSON and writes it to the daemon WebSocket. Returns
// an error if the connection is nil (daemon not connected) or the write fails.
func (d *JarvisDaemonConn) Send(msg interface{}) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.conn == nil {
		return ErrJarvisDaemonNotConnected
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return d.conn.WriteMessage(websocket.TextMessage, data)
}

// Close cleanly closes the underlying WebSocket connection if one exists.
func (d *JarvisDaemonConn) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.conn != nil {
		d.conn.Close()
		d.conn = nil
	}
}

// Connected reports whether a daemon WebSocket is currently connected.
func (d *JarvisDaemonConn) Connected() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.conn != nil
}

// set replaces the active connection. Any previously held connection is
// closed first.
func (d *JarvisDaemonConn) set(c *websocket.Conn) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.conn != nil {
		d.conn.Close()
	}
	d.conn = c
}

// clear sets the connection to nil without closing (caller already closed).
func (d *JarvisDaemonConn) clear() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.conn = nil
}

// sentinel error for when the daemon is not connected.
var ErrJarvisDaemonNotConnected = errJarvisNotConnected{}

type errJarvisNotConnected struct{}

func (errJarvisNotConnected) Error() string { return "jarvis daemon not connected" }

// ---------------------------------------------------------------------------
// JSON message types exchanged over the WebSocket
// ---------------------------------------------------------------------------

// JarvisIncoming represents a message sent from the Python daemon to Go.
type JarvisIncoming struct {
	Type       string          `json:"type"`              // "transcript", "response", "state", "audio_level", "tool_call", "mobile_tts"
	Text       string          `json:"text,omitempty"`    // transcript/response text
	Role       string          `json:"role,omitempty"`    // "user" or "jarvis" (for response)
	Partial    bool            `json:"partial,omitempty"` // true for partial transcripts
	State      string          `json:"state,omitempty"`   // "idle", "listening", "thinking", "speaking"
	Level      float64         `json:"level,omitempty"`   // 0.0-1.0 audio level
	ID         string          `json:"id,omitempty"`      // tool_call ID
	Name       string          `json:"name,omitempty"`    // tool_call function name
	Args       json.RawMessage `json:"args,omitempty"`    // tool_call arguments
	Data       string          `json:"data,omitempty"`    // base64 audio data (mobile_tts)
	SampleRate int             `json:"sampleRate,omitempty"` // audio sample rate (mobile_tts)
}

// JarvisOutgoing represents a message sent from Go to the Python daemon.
type JarvisOutgoing struct {
	Type    string      `json:"type"`              // "context", "tool_result", "command"
	Text    string      `json:"text,omitempty"`    // command text
	ID      string      `json:"id,omitempty"`      // tool_result ID
	Result  interface{} `json:"result,omitempty"`  // tool_result payload
	Context interface{} `json:"context,omitempty"` // context payload (sessions, costs, approvals)
}

// ---------------------------------------------------------------------------
// Route registration
// ---------------------------------------------------------------------------

// RegisterJarvisWSRoute mounts the /ws/jarvis WebSocket endpoint on the provided
// Echo group. The emitFn callback forwards daemon events to the Wails
// frontend. The returned *JarvisDaemonConn can be used by other Go code to
// send messages to the daemon.
func RegisterJarvisWSRoute(g *echo.Group, emitFn JarvisEventEmitter) *JarvisDaemonConn {
	dc := &JarvisDaemonConn{}

	g.GET("/ws/jarvis", func(c echo.Context) error {
		return handleJarvisWS(c, dc, emitFn)
	})

	return dc
}

// ---------------------------------------------------------------------------
// WebSocket handler
// ---------------------------------------------------------------------------

// handleJarvisWS upgrades the connection and runs the bidirectional read loop.
// It blocks until the client disconnects or the request context is cancelled.
func handleJarvisWS(c echo.Context, dc *JarvisDaemonConn, emitFn JarvisEventEmitter) error {
	ws, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
	if err != nil {
		slog.Error("jarvis ws: upgrade failed", "err", err, "remote", c.RealIP())
		return nil
	}

	logger := slog.With("component", "jarvis-ws", "remote", c.RealIP())
	logger.Info("jarvis daemon connected")

	// Store the connection so other Go code can send messages to the daemon.
	dc.set(ws)

	defer func() {
		dc.clear()
		ws.Close()
		logger.Warn("jarvis daemon disconnected")
	}()

	// Read loop: process incoming messages from the daemon.
	for {
		_, raw, err := ws.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				logger.Error("jarvis ws: read error", "err", err)
			} else {
				logger.Info("jarvis ws: connection closed", "err", err)
			}
			return nil
		}

		var msg JarvisIncoming
		if err := json.Unmarshal(raw, &msg); err != nil {
			logger.Error("jarvis ws: invalid JSON", "err", err, "raw", string(raw))
			continue
		}

		logger.Debug("jarvis ws: received", "type", msg.Type, "text", truncate(msg.Text, 80))

		switch msg.Type {
		case "transcript":
			emitJarvisEvent(emitFn, map[string]interface{}{
				"type":    "transcript",
				"text":    msg.Text,
				"partial": msg.Partial,
			})

		case "response":
			emitJarvisEvent(emitFn, map[string]interface{}{
				"type": "response",
				"text": msg.Text,
				"role": msg.Role,
			})
			// Fan out to any HTTP /jarvis/chat handlers waiting for a response.
			dc.notifyChatWaiters(msg.Text)

		case "state":
			emitJarvisEvent(emitFn, map[string]interface{}{
				"type":  "state_change",
				"state": msg.State,
			})

		case "audio_level":
			emitJarvisEvent(emitFn, map[string]interface{}{
				"type":  "audio_level",
				"level": msg.Level,
			})

		case "tool_call":
			handleJarvisToolCall(ws, msg, logger)

		case "mobile_tts":
			// Forward TTS audio chunk to mobile clients (not to Wails frontend).
			emitJarvisEvent(emitFn, map[string]interface{}{
				"type":       "mobile_tts",
				"data":       msg.Data,
				"sampleRate": msg.SampleRate,
			})

		case "model_download", "model_setup":
			// First-run model download/setup progress events. The full JSON
			// payload is forwarded verbatim to the React HUD so the download
			// progress overlay can render status, percent, bytes, etc.
			// without us having to enumerate every field here.
			var payload map[string]interface{}
			if err := json.Unmarshal(raw, &payload); err != nil {
				// Fall back to a minimal event with only the type so the
				// overlay still knows something arrived.
				payload = map[string]interface{}{"type": msg.Type}
			}
			emitJarvisEvent(emitFn, payload)

		default:
			logger.Warn("jarvis ws: unknown message type", "type", msg.Type)
		}
	}
}

// emitJarvisEvent safely calls the emitter callback, catching panics.
func emitJarvisEvent(emitFn JarvisEventEmitter, event interface{}) {
	if emitFn == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			slog.Error("jarvis ws: emit panic", "recover", r)
		}
	}()
	emitFn(event)
}

// ToolDispatcher executes tool calls from the Python daemon.
// The App struct satisfies this via a wrapper set up in WireRoutes.
type ToolDispatcher func(name string, args map[string]interface{}) map[string]interface{}

// jarvisToolDispatcher is set during WireRoutes so handleJarvisToolCall can use it.
var jarvisToolDispatcher ToolDispatcher

// SetJarvisToolDispatcher registers the tool dispatcher. Called from app.go.
func SetJarvisToolDispatcher(d ToolDispatcher) {
	jarvisToolDispatcher = d
}

// handleJarvisToolCall dispatches tool calls to actual App methods.
func handleJarvisToolCall(ws *websocket.Conn, msg JarvisIncoming, logger *slog.Logger) {
	logger.Info("jarvis ws: tool_call", "id", msg.ID, "name", msg.Name, "args", string(msg.Args))

	var args map[string]interface{}
	if len(msg.Args) > 0 {
		if err := json.Unmarshal(msg.Args, &args); err != nil {
			args = map[string]interface{}{}
		}
	} else {
		args = map[string]interface{}{}
	}

	var resultData map[string]interface{}
	if jarvisToolDispatcher != nil {
		resultData = jarvisToolDispatcher(msg.Name, args)
	} else {
		resultData = map[string]interface{}{"ok": false, "message": "tool dispatcher not initialized"}
	}

	result := JarvisOutgoing{
		Type:   "tool_result",
		ID:     msg.ID,
		Result: resultData,
	}

	data, err := json.Marshal(result)
	if err != nil {
		logger.Error("jarvis ws: failed to marshal tool_result", "err", err)
		return
	}

	if err := ws.WriteMessage(websocket.TextMessage, data); err != nil {
		logger.Error("jarvis ws: failed to send tool_result", "err", err)
	}
}

// truncate returns at most n characters of s, appending "..." if truncated.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// jarvisWSPath is the path used by the Jarvis daemon WebSocket endpoint. This is
// exported so the auth middleware can skip it.
const jarvisWSPath = "/ws/jarvis"

// IsJarvisWSPath reports whether the given request path is the Jarvis daemon
// WebSocket endpoint, which should bypass Bearer token auth.
func IsJarvisWSPath(path string) bool {
	return path == jarvisWSPath
}
