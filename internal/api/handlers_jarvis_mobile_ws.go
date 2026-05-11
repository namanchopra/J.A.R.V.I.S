package api

import (
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
)

// ---------------------------------------------------------------------------
// Jarvis Mobile WebSocket endpoint
//
// The mobile app connects to /ws/jarvis-mobile?token=BEARER_TOKEN for
// bidirectional communication with the Jarvis daemon. Auth is performed via
// the ?token= query parameter (same pattern as ws_session.go) since WebSocket
// clients cannot reliably set HTTP headers.
//
// Protocol (mobile -> server):
//   - Text message (JSON): {"type":"text","message":"what's happening?"}
//   - Binary frame: raw PCM audio (16-bit, 16kHz, mono) forwarded to daemon
//
// Protocol (server -> mobile):
//   - Text message (JSON): {"type":"response","text":"All quiet, sir."}
//   - Text message (JSON): {"type":"state","state":"speaking"}
//   - Text message (JSON): {"type":"transcript","text":"...","partial":true}
//   - Binary frame: TTS PCM audio for playback (future)
// ---------------------------------------------------------------------------

// MobileIncoming represents a JSON message sent from the mobile app to the
// server over the Jarvis mobile WebSocket.
type MobileIncoming struct {
	Type    string `json:"type"`              // "text" or "audio_control"
	Message string `json:"message,omitempty"` // text content for "text" type
}

// MobileOutgoing represents a JSON message sent from the server to the mobile
// app over the Jarvis mobile WebSocket.
type MobileOutgoing struct {
	Type       string `json:"type"`                    // "response", "state", "transcript", "error", "mobile_tts"
	Text       string `json:"text,omitempty"`          // response/transcript text
	State      string `json:"state,omitempty"`         // state name (idle, listening, thinking, speaking)
	Partial    bool   `json:"partial,omitempty"`       // true for partial transcripts
	Error      string `json:"error,omitempty"`         // error description
	Data       string `json:"data,omitempty"`          // base64 audio data (mobile_tts)
	SampleRate int    `json:"sampleRate,omitempty"`    // audio sample rate (mobile_tts)
}

// ---------------------------------------------------------------------------
// Thread-safe WebSocket connection wrapper
//
// gorilla/websocket is NOT safe for concurrent writes. safeConn serialises all
// writes behind a mutex so the broadcast goroutine and the per-client read
// loop can both write without races.
// ---------------------------------------------------------------------------

// safeConn wraps a *websocket.Conn with a mutex to serialise writes.
type safeConn struct {
	mu sync.Mutex
	ws *websocket.Conn
}

// WriteJSON marshals v as JSON and writes it to the WebSocket under the write
// mutex.
func (sc *safeConn) WriteJSON(v interface{}) error {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return sc.ws.WriteJSON(v)
}

// WriteMessage writes a WebSocket message under the write mutex.
func (sc *safeConn) WriteMessage(msgType int, data []byte) error {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return sc.ws.WriteMessage(msgType, data)
}

// ---------------------------------------------------------------------------
// Mobile client broadcaster
//
// MobileBroadcaster maintains the set of connected mobile WebSocket clients
// and forwards Jarvis daemon events to all of them. It is safe for concurrent
// use.
// ---------------------------------------------------------------------------

// MobileBroadcaster fans out Jarvis daemon events to all connected mobile
// WebSocket clients.
type MobileBroadcaster struct {
	mu      sync.RWMutex
	clients map[*safeConn]struct{}
}

// NewMobileBroadcaster creates a broadcaster with no connected clients.
func NewMobileBroadcaster() *MobileBroadcaster {
	return &MobileBroadcaster{
		clients: make(map[*safeConn]struct{}),
	}
}

// Add registers a mobile WebSocket connection for event broadcasting.
func (b *MobileBroadcaster) Add(conn *safeConn) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.clients[conn] = struct{}{}
}

// Remove unregisters a mobile WebSocket connection.
func (b *MobileBroadcaster) Remove(conn *safeConn) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.clients, conn)
}

// Broadcast sends a JSON message to all connected mobile clients. Clients
// that fail to receive the message are silently removed.
func (b *MobileBroadcaster) Broadcast(msg MobileOutgoing) {
	data, err := json.Marshal(msg)
	if err != nil {
		slog.Error("jarvis-mobile: failed to marshal broadcast", "err", err)
		return
	}

	b.mu.RLock()
	targets := make([]*safeConn, 0, len(b.clients))
	for c := range b.clients {
		targets = append(targets, c)
	}
	b.mu.RUnlock()

	var failed []*safeConn
	for _, c := range targets {
		if err := c.WriteMessage(websocket.TextMessage, data); err != nil {
			failed = append(failed, c)
		}
	}

	if len(failed) > 0 {
		b.mu.Lock()
		for _, c := range failed {
			delete(b.clients, c)
		}
		b.mu.Unlock()
	}
}

// HandleDaemonEvent converts a Jarvis daemon event (as emitted by the daemon
// read loop in handlers_jarvis_ws.go) into a MobileOutgoing message and
// broadcasts it to all connected mobile clients.
//
// This is designed to be called from a JarvisEventEmitter wrapper. The event
// parameter has the same shape as the maps emitted by handleJarvisWS:
//
//	{"type":"response","text":"...","role":"jarvis"}
//	{"type":"state_change","state":"speaking"}
//	{"type":"transcript","text":"...","partial":true}
//	{"type":"audio_level","level":0.5}
//
// Audio level events are intentionally not forwarded to mobile to reduce
// bandwidth.
func (b *MobileBroadcaster) HandleDaemonEvent(event interface{}) {
	m, ok := event.(map[string]interface{})
	if !ok {
		return
	}

	eventType, _ := m["type"].(string)

	switch eventType {
	case "response":
		text, _ := m["text"].(string)
		b.Broadcast(MobileOutgoing{
			Type: "response",
			Text: text,
		})

	case "state_change":
		state, _ := m["state"].(string)
		b.Broadcast(MobileOutgoing{
			Type:  "state",
			State: state,
		})

	case "transcript":
		text, _ := m["text"].(string)
		partial, _ := m["partial"].(bool)
		b.Broadcast(MobileOutgoing{
			Type:    "transcript",
			Text:    text,
			Partial: partial,
		})

	case "audio_level":
		// Intentionally not forwarded to mobile — too chatty for network.

	case "mobile_tts":
		// Forward TTS audio chunk to mobile clients for remote playback.
		data, _ := m["data"].(string)
		sampleRate := 24000
		if sr, ok := m["sampleRate"].(float64); ok && sr > 0 {
			sampleRate = int(sr)
		}
		if data != "" {
			b.Broadcast(MobileOutgoing{
				Type:       "mobile_tts",
				Data:       data,
				SampleRate: sampleRate,
			})
		}

	default:
		// Unknown event type — skip silently.
	}
}

// ClientCount returns the number of currently connected mobile clients.
func (b *MobileBroadcaster) ClientCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.clients)
}

// ---------------------------------------------------------------------------
// Route registration
// ---------------------------------------------------------------------------

// RegisterJarvisMobileWSRoute mounts the /ws/jarvis-mobile WebSocket endpoint
// on the provided Echo group. The getToken function returns the current Bearer
// token for authentication. The daemonConn is used to forward commands to the
// Jarvis Python daemon.
//
// Returns a *MobileBroadcaster that should be used to forward daemon events to
// connected mobile clients. Wire it into the JarvisEventEmitter chain:
//
//	mb := RegisterJarvisMobileWSRoute(g, tokenFn, daemonConn)
//	// In your emitter wrapper:
//	emitFn(event)          // forward to Wails frontend
//	mb.HandleDaemonEvent(event) // forward to mobile clients
func RegisterJarvisMobileWSRoute(g *echo.Group, getToken func() string, daemonConn *JarvisDaemonConn) *MobileBroadcaster {
	mb := NewMobileBroadcaster()

	g.GET("/ws/jarvis-mobile", func(c echo.Context) error {
		return handleJarvisMobileWS(c, getToken, daemonConn, mb)
	})

	return mb
}

// ---------------------------------------------------------------------------
// WebSocket handler
// ---------------------------------------------------------------------------

// handleJarvisMobileWS upgrades the connection, authenticates via query param,
// and runs the bidirectional message loop. It blocks until the client
// disconnects or the request context is cancelled.
func handleJarvisMobileWS(c echo.Context, getToken func() string, daemonConn *JarvisDaemonConn, mb *MobileBroadcaster) error {
	// Upgrade to WebSocket first, then validate token. The WebSocket spec
	// requires the upgrade before we can send close frames.
	rawWS, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
	if err != nil {
		slog.Error("jarvis-mobile ws: upgrade failed", "err", err, "remote", c.RealIP())
		return nil
	}
	defer rawWS.Close()

	// Wrap the raw connection for thread-safe writes.
	sc := &safeConn{ws: rawWS}

	// Authenticate via ?token= query parameter.
	provided := c.QueryParam("token")
	expected := getToken()
	if expected == "" || provided != expected {
		slog.Warn("jarvis-mobile ws: auth failed", "remote", c.RealIP())
		sc.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(closeCodeUnauthorized, "unauthorized"))
		return nil
	}

	logger := slog.With("component", "jarvis-mobile-ws", "remote", c.RealIP())
	logger.Info("mobile client connected")

	// Register this connection to receive daemon event broadcasts.
	mb.Add(sc)
	defer func() {
		mb.Remove(sc)
		logger.Info("mobile client disconnected")
	}()

	// Send initial state so the mobile client knows the current daemon status.
	if daemonConn.Connected() {
		initial := MobileOutgoing{
			Type:  "state",
			State: "idle",
		}
		if data, err := json.Marshal(initial); err == nil {
			sc.WriteMessage(websocket.TextMessage, data)
		}
	} else {
		initial := MobileOutgoing{
			Type:  "state",
			State: "disconnected",
		}
		if data, err := json.Marshal(initial); err == nil {
			sc.WriteMessage(websocket.TextMessage, data)
		}
	}

	// Read loop: process incoming messages from the mobile client.
	// Reads are only performed by this goroutine, so no mutex is needed for
	// reads. All writes go through the safeConn wrapper.
	for {
		msgType, raw, err := rawWS.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				logger.Error("jarvis-mobile ws: read error", "err", err)
			}
			return nil
		}

		switch msgType {
		case websocket.TextMessage:
			handleMobileTextMessage(sc, raw, daemonConn, logger)

		case websocket.BinaryMessage:
			handleMobileAudioChunk(sc, raw, daemonConn, logger)

		default:
			// Control frames (ping/pong/close) are handled by gorilla/websocket
			// automatically. Any other frame type is unexpected.
			logger.Debug("jarvis-mobile ws: unexpected frame type", "type", msgType)
		}
	}
}

// handleMobileTextMessage processes a JSON text message from the mobile client.
func handleMobileTextMessage(sc *safeConn, raw []byte, daemonConn *JarvisDaemonConn, logger *slog.Logger) {
	var msg MobileIncoming
	if err := json.Unmarshal(raw, &msg); err != nil {
		logger.Error("jarvis-mobile ws: invalid JSON", "err", err, "raw", string(raw))
		sendMobileError(sc, "invalid JSON message")
		return
	}

	logger.Debug("jarvis-mobile ws: received", "type", msg.Type, "message", truncate(msg.Message, 80))

	switch msg.Type {
	case "text":
		if msg.Message == "" {
			sendMobileError(sc, "empty message")
			return
		}

		// Forward to the Jarvis daemon as a command.
		cmd := JarvisOutgoing{
			Type: "command",
			Text: msg.Message,
		}

		if err := daemonConn.Send(cmd); err != nil {
			logger.Error("jarvis-mobile ws: failed to forward to daemon", "err", err)
			if _, ok := err.(errJarvisNotConnected); ok {
				sendMobileError(sc, "jarvis daemon not connected")
			} else {
				sendMobileError(sc, "failed to send command to jarvis")
			}
			return
		}

		logger.Info("jarvis-mobile ws: forwarded command to daemon", "text", truncate(msg.Message, 80))

	default:
		logger.Warn("jarvis-mobile ws: unknown message type", "type", msg.Type)
		sendMobileError(sc, "unknown message type: "+msg.Type)
	}
}

// handleMobileAudioChunk processes a binary audio frame from the mobile client.
// Audio is base64-encoded and forwarded to the daemon as a JSON message with
// type "mobile_audio".
func handleMobileAudioChunk(sc *safeConn, data []byte, daemonConn *JarvisDaemonConn, logger *slog.Logger) {
	if len(data) == 0 {
		return
	}

	// Encode the raw PCM audio as base64 for JSON transport to the daemon.
	encoded := base64.StdEncoding.EncodeToString(data)

	audioMsg := map[string]interface{}{
		"type": "mobile_audio",
		"data": encoded,
		"size": len(data),
	}

	if err := daemonConn.Send(audioMsg); err != nil {
		logger.Error("jarvis-mobile ws: failed to forward audio to daemon", "err", err, "bytes", len(data))
		if _, ok := err.(errJarvisNotConnected); ok {
			sendMobileError(sc, "jarvis daemon not connected")
		}
		return
	}

	logger.Debug("jarvis-mobile ws: forwarded audio chunk", "bytes", len(data))
}

// sendMobileError sends a JSON error message to the mobile client.
func sendMobileError(sc *safeConn, message string) {
	resp := MobileOutgoing{
		Type:  "error",
		Error: message,
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	sc.WriteMessage(websocket.TextMessage, data)
}

// IsJarvisMobileWSPath reports whether the given request path is the Jarvis
// mobile WebSocket endpoint. This can be used by auth middleware if the
// endpoint needs special handling (though it does require auth via ?token=).
func IsJarvisMobileWSPath(path string) bool {
	return path == "/ws/jarvis-mobile"
}
