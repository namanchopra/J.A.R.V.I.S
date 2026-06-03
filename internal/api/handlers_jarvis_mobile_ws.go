package api

import (
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/namanchopra/jarvis/internal/model"

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
//
// Recognised types (see handleMobileTextMessage):
//   - "text"        : free-text command to forward to the daemon.
//   - "hello"       : client handshake on connect; logged, not forwarded.
//   - "audio_start" : push-to-talk begin marker; logged, not forwarded.
//                     (Audio bytes follow as binary frames.)
//   - "audio_end"   : push-to-talk end marker; logged, not forwarded.
type MobileIncoming struct {
	Type    string `json:"type"`              // "text" | "hello" | "audio_start" | "audio_end"
	Message string `json:"message,omitempty"` // text content for "text" type
	Version string `json:"version,omitempty"` // client version for "hello"
}

// MobileStats is the dashboard snapshot pushed to mobile clients periodically
// over the WS so they don't need to make REST calls. Expo Go on the latest
// SDK no longer auto-bypasses iOS App Transport Security for plain http://,
// so REST fetches fail even when WS to the same host works. Pushing stats
// over the already-authorised WS sidesteps the whole ATS problem.
type MobileStats struct {
	ActiveSessions   int    `json:"activeSessions"`
	PendingApprovals int    `json:"pendingApprovals"`
	RunningTasks     int    `json:"runningTasks"`
	EventsToday      int    `json:"eventsToday"`
	LatestActivity   string `json:"latestActivity"`
	// NextEvent is the user's upcoming calendar event, or nil when the
	// calendar is empty, not connected, or the underlying API failed.
	// Mobile renders Title + RelativeTime; the StartISO is included so
	// the mobile side can do its own staleness checks if needed.
	NextEvent *NextEventInfo `json:"nextEvent,omitempty"`
}

// NextEventInfo is the wire-format for the next upcoming calendar event
// pushed to mobile clients in the periodic stats_snapshot WS message.
// Mirrors model.NextEventSnapshot — duplicated here so the wire schema
// doesn't shift if the domain model gains internal fields later.
type NextEventInfo struct {
	Title        string `json:"title"`
	StartISO     string `json:"startIso"`
	RelativeTime string `json:"relativeTime"`
}

// MobileOutgoing represents a JSON message sent from the server to the mobile
// app over the Jarvis mobile WebSocket. Field names match what
// mobile/lib/jarvis-ws.ts expects — do NOT rename without updating that
// client in lockstep.
type MobileOutgoing struct {
	Type       string       `json:"type"`                 // "state_change" | "transcript" | "tts_audio_level" | "mobile_tts" | "response" | "error" | "stats_snapshot"
	Text       string       `json:"text,omitempty"`       // response/transcript text
	Role       string       `json:"role,omitempty"`       // "user" | "assistant" for transcript
	Phase      string       `json:"phase,omitempty"`      // state_change phase name (idle, listening, thinking, speaking)
	Level      float64      `json:"level,omitempty"`      // tts_audio_level 0..1
	Partial    bool         `json:"partial,omitempty"`    // true for partial transcripts
	Error      string       `json:"error,omitempty"`      // error description
	Data       string       `json:"data,omitempty"`       // base64 audio data (mobile_tts)
	SampleRate int          `json:"sampleRate,omitempty"` // audio sample rate (mobile_tts)
	Stats      *MobileStats `json:"stats,omitempty"`      // dashboard snapshot (stats_snapshot)
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
		// Daemon `response` events carry the assistant's spoken text. The
		// mobile WS client only listens for `transcript` with a `role` —
		// remap so TranscriptChip's assistantText slot gets populated.
		text, _ := m["text"].(string)
		role, _ := m["role"].(string)
		if role == "" {
			role = "assistant"
		}
		b.Broadcast(MobileOutgoing{
			Type: "transcript",
			Text: text,
			Role: role,
		})

	case "state_change":
		// Mobile expects {type:"state_change", phase:"..."} — the daemon
		// upstream sends the value under "state", which the daemon WS
		// handler in handlers_jarvis_ws.go forwards as state_change with
		// a `state` field. Rename to `phase` here so the wire matches.
		state, _ := m["state"].(string)
		b.Broadcast(MobileOutgoing{
			Type:  "state_change",
			Phase: state,
		})

	case "transcript":
		// Daemon transcripts are user-mic STT output — flag the role so
		// the mobile TranscriptChip routes them to the userText slot.
		text, _ := m["text"].(string)
		partial, _ := m["partial"].(bool)
		b.Broadcast(MobileOutgoing{
			Type:    "transcript",
			Text:    text,
			Role:    "user",
			Partial: partial,
		})

	case "audio_level":
		// Forward TTS audio level so OrbView can pulse its sphere on
		// speaking turns. The daemon already filters to TTS playback
		// levels only (no mic-side levels), so volume here is bounded.
		level, _ := m["level"].(float64)
		b.Broadcast(MobileOutgoing{
			Type:  "tts_audio_level",
			Level: level,
		})

		case "mobile_tts":
			// Forward TTS audio chunk to mobile clients for remote playback.
			data, _ := m["data"].(string)
			// The map value can be either int (when the upstream Jarvis WS
			// handler forwards it via a typed struct) or float64 (when it
			// arrives through raw JSON unmarshal into map[string]any). We
			// must try both: a single float64-only assertion silently falls
			// through to the default and produces chipmunk playback when
			// the real PCM is 16kHz but the WAV header gets built at 24kHz.
			sampleRate := 16000
			switch sr := m["sampleRate"].(type) {
			case float64:
				if sr > 0 {
					sampleRate = int(sr)
				}
			case int:
				if sr > 0 {
					sampleRate = sr
				}
			case int64:
				if sr > 0 {
					sampleRate = int(sr)
				}
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

// StatsProvider exposes the App methods needed to build a MobileStats
// snapshot. We declare it locally rather than reusing DashboardProvider +
// ApprovalProvider as separate args because the periodic broadcaster only
// needs three reads — keeping the dependency footprint minimal makes the
// goroutine easier to reason about and to mock in tests.
type StatsProvider interface {
	GetActiveSessions() ([]model.Session, error)
	GetDashboardStats() (model.DashboardStats, error)
	GetActivityFeed(limit int, beforeID string) ([]model.ActivityEvent, error)
	GetPendingApprovals() ([]model.ApprovalRequest, error)
	// GetNextCalendarEvent returns the next upcoming event or nil. Returning
	// gcal.ErrNotAuthenticated (or any error) results in the WS push leaving
	// NextEvent nil — the broadcaster degrades gracefully and never spams
	// error logs when the user simply hasn't connected the calendar.
	GetNextCalendarEvent() (*model.NextEventSnapshot, error)
}

// StartStatsBroadcaster launches a goroutine that polls the App provider
// every ``interval`` and broadcasts a ``stats_snapshot`` event to all
// currently-connected mobile WS clients. Returns a stop function the caller
// can defer for clean shutdown.
//
// Why goroutine in the Go server (not the daemon): the App struct is
// in-process and already exposes the data. Daemon would have to round-trip
// over the daemon WS bridge, which is needless complexity. The interval
// matches the Mac HUD's 5s cadence so phone parity is automatic.
//
// If ``ClientCount()`` is zero we skip the four reads entirely — no point
// hitting the SQLite store when nobody's listening.
func (b *MobileBroadcaster) StartStatsBroadcaster(
	provider StatsProvider,
	interval time.Duration,
) func() {
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		// Fire once immediately so the first stat refresh isn't gated on
		// the full interval after server start.
		b.broadcastStatsOnce(provider)
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				b.broadcastStatsOnce(provider)
			}
		}
	}()
	return func() { close(stop) }
}

// broadcastStatsOnce performs one snapshot + broadcast. Errors on any of
// the four reads degrade gracefully -- a partial snapshot is better than
// none. Skips entirely when there are no connected clients to avoid
// noise.
func (b *MobileBroadcaster) broadcastStatsOnce(provider StatsProvider) {
	clientCount := b.ClientCount()
	if clientCount == 0 {
		return
	}

	snapshot := MobileStats{}

	if sessions, err := provider.GetActiveSessions(); err == nil {
		snapshot.ActiveSessions = len(sessions)
	} else {
		slog.Warn("stats: GetActiveSessions failed", "err", err)
	}
	if stats, err := provider.GetDashboardStats(); err == nil {
		snapshot.RunningTasks = stats.Running
	} else {
		slog.Warn("stats: GetDashboardStats failed", "err", err)
	}
	if approvals, err := provider.GetPendingApprovals(); err == nil {
		snapshot.PendingApprovals = len(approvals)
	} else {
		slog.Warn("stats: GetPendingApprovals failed", "err", err)
	}
	if events, err := provider.GetActivityFeed(20, ""); err == nil {
		// Latest activity = first non-empty message, truncated.
		for _, ev := range events {
			d := strings.TrimSpace(ev.Message)
			if d != "" {
				if len(d) > 28 {
					d = d[:27] + "…"
				}
				snapshot.LatestActivity = d
				break
			}
		}
		// Events today = count of CreatedAt matching today's date prefix.
		today := time.Now().Format("2006-01-02")
		for _, ev := range events {
			if strings.HasPrefix(ev.CreatedAt.Format(time.RFC3339), today) {
				snapshot.EventsToday++
			}
		}
	} else {
		slog.Warn("stats: GetActivityFeed failed", "err", err)
	}

	if snap, err := provider.GetNextCalendarEvent(); err == nil && snap != nil {
		snapshot.NextEvent = &NextEventInfo{
			Title:        snap.Title,
			StartISO:     snap.StartISO,
			RelativeTime: snap.RelativeTime,
		}
	}
	// Silently degrade on error — gcal.ErrNotAuthenticated is the common
	// case (calendar not configured) and would otherwise spam the log
	// every 5s. Other errors are still suppressed because next-event is
	// non-critical UI; a missing tile is better than a stuck broadcaster.

	slog.Info("stats: broadcasting snapshot",
		"clients", clientCount,
		"activeSessions", snapshot.ActiveSessions,
		"runningTasks", snapshot.RunningTasks,
		"pendingApprovals", snapshot.PendingApprovals,
		"eventsToday", snapshot.EventsToday,
		"latestActivity", snapshot.LatestActivity,
	)

	b.Broadcast(MobileOutgoing{
		Type:  "stats_snapshot",
		Stats: &snapshot,
	})
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
	// Uses the same wire schema as the broadcaster: type=state_change with a
	// `phase` field — matches mobile/lib/jarvis-ws.ts expectations.
	if daemonConn.Connected() {
		initial := MobileOutgoing{
			Type:  "state_change",
			Phase: "idle",
		}
		if data, err := json.Marshal(initial); err == nil {
			sc.WriteMessage(websocket.TextMessage, data)
		}
	} else {
		initial := MobileOutgoing{
			Type:  "state_change",
			Phase: "disconnected",
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

	case "hello":
		// Client handshake -- the mobile WS client sends this once per
		// connection so we can log the client version. No daemon-side
		// action required today.
		logger.Info("jarvis-mobile ws: hello", "version", msg.Version)

	case "audio_start", "audio_end":
		// Push-to-talk lifecycle markers. The actual audio bytes ride
		// on binary frames; these are book-ends for the daemon's STT
		// to optionally use as utterance boundaries. Logged at debug
		// only -- they fire once per press.
		logger.Debug("jarvis-mobile ws: ptt marker", "type", msg.Type)

	default:
		logger.Warn("jarvis-mobile ws: unknown message type", "type", msg.Type)
		sendMobileError(sc, "unknown message type: "+msg.Type)
	}
}

// handleMobileAudioChunk processes a binary audio frame from the mobile client.
// Audio is base64-encoded and forwarded to the daemon as a JSON message with
// type "mobile_audio".
//
// Path A active-client routing (v0.3.0): after forwarding the audio chunk we
// also send a small "mobile_active" control frame so the daemon can flip its
// active-interlocutor state to "mobile" for the upcoming turn. This drives
// TTS provider selection (Friday voice via macOS say -v Serena), the LLM
// persona overlay, and the per-client audio output gating. Sent on every
// chunk so any in-flight grace window keeps refreshing for the duration of
// the mobile turn.
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

	// Notify the daemon that the mobile client is the active interlocutor.
	// Failure here is non-fatal: the audio chunk above is the load-bearing
	// frame; if the control frame drops, the daemon falls back to the Mac
	// voice for the upcoming turn -- annoying but not broken.
	if err := daemonConn.Send(map[string]interface{}{"type": "mobile_active"}); err != nil {
		logger.Debug("jarvis-mobile ws: failed to forward mobile_active control frame", "err", err)
	}
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
