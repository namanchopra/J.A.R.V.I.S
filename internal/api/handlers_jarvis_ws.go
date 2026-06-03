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
//
// PTT (push-to-talk) control frames — added by TASK-006 for the Mac overlay
// hotkey flow (TASK-005).
//
// The Go bridge sends these frames to the daemon WS in response to global
// hotkey events from internal/hotkey:
//
//	{ "type": "ptt_active"  } — sent on hotkey press;   opens daemon STT gate.
//	{ "type": "ptt_release" } — sent on hotkey release; finalizes the turn.
//
// Both frames travel on the existing /ws/jarvis connection (no new endpoint).
// The daemon side is implemented in scripts/jarvis-daemon/main.py
// (_handle_ptt_active / _handle_ptt_release); it injects pipecat
// UserStartedSpeakingFrame / UserStoppedSpeakingFrame into the live pipeline
// so the LLM dispatch path is reused, not forked.
//
// Out-of-order frames (ptt_release without a prior ptt_active) are logged
// and dropped by the daemon — they do NOT raise. A 5-second safety timeout
// in the daemon force-closes a stuck "active" gate if no release arrives
// (e.g. the user quits the app mid-hold).
//
// Callers: internal/hotkey/Manager invokes SendPTTActive / SendPTTRelease
// via the App struct's overlay bindings. See app_overlay.go (TASK-005).
//
// ---------------------------------------------------------------------------
// Meeting-mode system_audio frames — added by v0.3.0 meeting-mode TASK-005
// alongside the overlay+PTT plumbing above.
//
// The Go bridge captures speakers / system-audio output via the macOS
// ScreenCaptureKit wrapper (internal/screencapture, TASK-002/TASK-004) and
// pushes each PCM chunk to the daemon WS as:
//
//	{ "type": "system_audio", "data": "<base64 PCM>" }
//
// data is base64-encoded 16-bit mono 16 kHz PCM matching
// screencapture.CanonicalAudioFormat. The daemon handler
// (scripts/jarvis-daemon/main.py:_handle_system_audio, TASK-006) decodes the
// payload and injects it as a MobileAudioRawFrame-style frame into the
// existing STT pipeline so meeting-mode transcripts include both the user's
// mic AND the system audio output. Frames received while meeting mode is
// inactive are silently dropped by the daemon — the Go side never enforces
// that gate; the daemon is the source of truth for meeting lifecycle.
//
// Callers: app_meeting.go (TASK-005) wires the screencapture AudioCallback
// directly to SendSystemAudioFrame; that callback fires on a serial dispatch
// queue from the macOS bridge, NOT the Go main goroutine. SendSystemAudioFrame
// inherits Send's per-conn mutex, so concurrent invocation from the dispatch
// queue and other goroutines is safe.
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

// SendPTTActive pushes a ``ptt_active`` control frame to the daemon WS,
// signalling that the user pressed and is holding the global PTT hotkey.
// The daemon opens its STT gate, transitions to the "listening" state, and
// injects a UserStartedSpeakingFrame into the live pipecat pipeline.
//
// Returns an error only if the WS is disconnected (ErrJarvisDaemonNotConnected)
// or the underlying JSON marshal / WriteMessage fails. Calling this on a stuck
// "active" gate is safe — the daemon de-duplicates on its side and logs a
// warning. See the contract comment block at the top of this file.
func (d *JarvisDaemonConn) SendPTTActive() error {
	return d.Send(map[string]string{"type": "ptt_active"})
}

// SendPTTRelease pushes a ``ptt_release`` control frame to the daemon WS,
// signalling that the user released the global PTT hotkey. The daemon
// closes its STT gate and injects a UserStoppedSpeakingFrame, which the
// existing LLM-aggregator path picks up to dispatch the turn.
//
// Returns an error only if the WS is disconnected (ErrJarvisDaemonNotConnected)
// or the underlying JSON marshal / WriteMessage fails. A release with no prior
// active is logged and dropped by the daemon (failure case documented in
// TASK-006); this helper does not attempt to track that locally — the daemon
// is the source of truth for PTT lifecycle state.
func (d *JarvisDaemonConn) SendPTTRelease() error {
	return d.Send(map[string]string{"type": "ptt_release"})
}

// SendSystemAudioFrame pushes a system_audio control frame to the daemon
// WS carrying base64-encoded PCM audio captured by the macOS
// ScreenCaptureKit bridge (internal/screencapture). The daemon handler
// (scripts/jarvis-daemon/main.py:_handle_system_audio) decodes the data
// field and injects it as a MobileAudioRawFrame-style frame into the
// existing STT pipeline so meeting-mode transcripts include both the
// user's mic AND system audio output.
//
// data must be base64-encoded 16-bit mono 16 kHz PCM, matching
// screencapture.CanonicalAudioFormat. The daemon will silently drop
// frames received while meeting mode is not active.
//
// Returns ErrJarvisDaemonNotConnected if the WS is down or
// the underlying Send fails (JSON marshal / WriteMessage).
func (d *JarvisDaemonConn) SendSystemAudioFrame(data string) error {
	return d.Send(map[string]string{"type": "system_audio", "data": data})
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

		case "model_download", "model_setup", "pipeline_status":
			// First-run model download/setup progress events and the v0.1.5
			// ``pipeline_status`` event (resolved TTS / STT / LLM choices).
			// The full JSON payload is forwarded verbatim to the React HUD
			// so each consumer can render its own fields without us having
			// to enumerate every key here.
			var payload map[string]interface{}
			if err := json.Unmarshal(raw, &payload); err != nil {
				// Fall back to a minimal event with only the type so the
				// overlay still knows something arrived.
				payload = map[string]interface{}{"type": msg.Type}
			}
			emitJarvisEvent(emitFn, payload)

		case "meeting_notes_written":
			// v0.3.0 meeting-mode TASK-009: the daemon emits this event
			// once _dispatch_meeting_finalisation has written the markdown
			// file. Forward the full payload (type / path / title /
			// buffer_entries) to the Wails frontend AND to mobile clients
			// via the wrapped emitter chain. The Wails-side App listens
			// via the jarvisEmitFn registered in app.go:startMobileAPI to
			// resolve the StopMeeting() binding's pending await.
			var payload map[string]interface{}
			if err := json.Unmarshal(raw, &payload); err != nil {
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

	// The Python daemon sends args in two shapes:
	//   - Jarvis intent tools (snake_case names like "approve_session"): JSON object {"name":"maya"}
	//   - Wails-direct calls (CamelCase names from _call_wails): JSON array ["query string"]
	// Try map first; on failure, decode as positional array and stuff into "_positional".
	args := map[string]interface{}{}
	if len(msg.Args) > 0 {
		if err := json.Unmarshal(msg.Args, &args); err != nil {
			var posArgs []interface{}
			if posErr := json.Unmarshal(msg.Args, &posArgs); posErr == nil {
				args = map[string]interface{}{"_positional": posArgs}
			}
		}
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
