package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
)

// ---------------------------------------------------------------------------
// TASK-010: Scripted integration test for PTT frame forwarding.
//
// These tests exercise the WS bridge layer that TASK-006 introduced — they
// stand up a real Echo+httptest server, dial the /ws/jarvis endpoint with a
// gorilla/websocket client, call SendPTTActive() / SendPTTRelease() on the
// resulting JarvisDaemonConn, and assert the frames arrive on the wire in
// the right shape and order.
//
// The daemon-side out-of-order handling (ptt_release without a prior
// ptt_active) is covered by scripts/jarvis-daemon/tests/test_ptt_handlers.py.
// The analogous failure surface on the Go side is the nil/closed-conn case:
// callers must get ErrJarvisDaemonNotConnected rather than a panic.
// ---------------------------------------------------------------------------

// jarvisWSTestRig wraps a running httptest server hosting the /ws/jarvis
// endpoint plus the dialed client connection. The dc field is populated once
// the test client has connected (the server stores the upgraded socket
// inside the JarvisDaemonConn).
type jarvisWSTestRig struct {
	srv    *httptest.Server
	client *websocket.Conn
	dc     *JarvisDaemonConn
}

// startJarvisWSRig boots an Echo instance with RegisterJarvisWSRoute mounted,
// wraps it in httptest, dials the WS endpoint, and waits until the server-
// side has recorded the connection. The caller must defer rig.close().
func startJarvisWSRig(t *testing.T) *jarvisWSTestRig {
	t.Helper()

	e := echo.New()
	g := e.Group("")
	dc := RegisterJarvisWSRoute(g, func(event interface{}) {
		// No-op emitter — these tests don't read events from the daemon.
	})

	srv := httptest.NewServer(e)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/jarvis"
	dialer := websocket.DefaultDialer
	client, resp, err := dialer.Dial(wsURL, nil)
	if err != nil {
		srv.Close()
		t.Fatalf("dial /ws/jarvis: %v (status=%v)", err, resp)
	}

	// Wait briefly for the server-side handler to register the connection
	// inside dc. This avoids a race where the test calls SendPTTActive
	// before handleJarvisWS has had a chance to run dc.set(ws).
	deadline := time.Now().Add(2 * time.Second)
	for !dc.Connected() {
		if time.Now().After(deadline) {
			client.Close()
			srv.Close()
			t.Fatal("daemon connection was never recorded server-side")
		}
		time.Sleep(5 * time.Millisecond)
	}

	return &jarvisWSTestRig{
		srv:    srv,
		client: client,
		dc:     dc,
	}
}

func (r *jarvisWSTestRig) close() {
	if r.client != nil {
		_ = r.client.Close()
	}
	if r.srv != nil {
		r.srv.Close()
	}
}

// readFrame reads one text frame from the client side of the WS and decodes
// it into a map. It enforces a short deadline so a missing frame fails fast
// rather than hanging the test.
func readFrame(t *testing.T, c *websocket.Conn) map[string]interface{} {
	t.Helper()

	if err := c.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	mt, raw, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if mt != websocket.TextMessage {
		t.Fatalf("expected text message, got message type %d", mt)
	}

	var got map[string]interface{}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal frame: %v (raw=%s)", err, raw)
	}
	return got
}

// ---------------------------------------------------------------------------
// Happy path: send PTT-active then PTT-release, assert wire shape + order.
// ---------------------------------------------------------------------------

func TestSendPTTActiveAndReleaseProducesOrderedFrames(t *testing.T) {
	rig := startJarvisWSRig(t)
	defer rig.close()

	if err := rig.dc.SendPTTActive(); err != nil {
		t.Fatalf("SendPTTActive returned error: %v", err)
	}

	first := readFrame(t, rig.client)
	if first["type"] != "ptt_active" {
		t.Fatalf("first frame type: want %q, got %q (full=%v)", "ptt_active", first["type"], first)
	}

	if err := rig.dc.SendPTTRelease(); err != nil {
		t.Fatalf("SendPTTRelease returned error: %v", err)
	}

	second := readFrame(t, rig.client)
	if second["type"] != "ptt_release" {
		t.Fatalf("second frame type: want %q, got %q (full=%v)", "ptt_release", second["type"], second)
	}
}

// ---------------------------------------------------------------------------
// Wire-shape: the JSON payload is exactly {"type":"..."} — no leaked fields.
// (We assert it round-trips cleanly and contains only the "type" key, which
// makes regressions like accidentally embedding internal struct fields show
// up here.)
// ---------------------------------------------------------------------------

func TestSendPTTFrameShapeIsMinimal(t *testing.T) {
	rig := startJarvisWSRig(t)
	defer rig.close()

	if err := rig.dc.SendPTTActive(); err != nil {
		t.Fatalf("SendPTTActive: %v", err)
	}
	frame := readFrame(t, rig.client)

	if len(frame) != 1 {
		t.Errorf("ptt_active frame should have exactly one field, got %d (%v)", len(frame), frame)
	}
	if frame["type"] != "ptt_active" {
		t.Errorf("ptt_active frame type wrong: got %v", frame["type"])
	}

	if err := rig.dc.SendPTTRelease(); err != nil {
		t.Fatalf("SendPTTRelease: %v", err)
	}
	frame = readFrame(t, rig.client)

	if len(frame) != 1 {
		t.Errorf("ptt_release frame should have exactly one field, got %d (%v)", len(frame), frame)
	}
	if frame["type"] != "ptt_release" {
		t.Errorf("ptt_release frame type wrong: got %v", frame["type"])
	}
}

// ---------------------------------------------------------------------------
// Failure case the TASK-010 acceptance criteria call out: "scripted handler
// test fails cleanly if ptt_release arrives without a prior ptt_active."
//
// The Go side is a thin bridge; the daemon owns lifecycle state (and the
// daemon-side test in scripts/jarvis-daemon/tests/test_ptt_handlers.py
// covers that path). On the Go side, the analogous failure surface is
// sending PTT frames when no daemon is connected: the helpers must return
// ErrJarvisDaemonNotConnected cleanly, without panicking, regardless of
// which one comes first.
// ---------------------------------------------------------------------------

func TestSendPTTOnDisconnectedConnReturnsSentinelError(t *testing.T) {
	// Case 1: brand-new conn struct — never connected. SendPTTRelease "first"
	// (out-of-order at the protocol level) must not panic and must surface
	// the sentinel.
	dc := &JarvisDaemonConn{}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("SendPTTRelease panicked on never-connected conn: %v", r)
		}
	}()

	if err := dc.SendPTTRelease(); !errors.Is(err, ErrJarvisDaemonNotConnected) {
		t.Errorf("SendPTTRelease on never-connected conn: want ErrJarvisDaemonNotConnected, got %v", err)
	}
	if err := dc.SendPTTActive(); !errors.Is(err, ErrJarvisDaemonNotConnected) {
		t.Errorf("SendPTTActive on never-connected conn: want ErrJarvisDaemonNotConnected, got %v", err)
	}
}

func TestSendPTTAfterCloseReturnsSentinelError(t *testing.T) {
	// Case 2: conn was open, then explicitly closed via Close(). Subsequent
	// SendPTTActive / SendPTTRelease calls must return the sentinel without
	// panicking (the underlying *websocket.Conn has been nil'd).
	rig := startJarvisWSRig(t)
	defer rig.close()

	// Sanity check: ensure the rig really did open the conn.
	if !rig.dc.Connected() {
		t.Fatal("rig.dc reports not connected after dial — test setup is broken")
	}

	rig.dc.Close()

	if rig.dc.Connected() {
		t.Fatal("dc.Connected() still true after Close()")
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("PTT send panicked after Close(): %v", r)
		}
	}()

	if err := rig.dc.SendPTTActive(); !errors.Is(err, ErrJarvisDaemonNotConnected) {
		t.Errorf("SendPTTActive after Close: want ErrJarvisDaemonNotConnected, got %v", err)
	}
	if err := rig.dc.SendPTTRelease(); !errors.Is(err, ErrJarvisDaemonNotConnected) {
		t.Errorf("SendPTTRelease after Close: want ErrJarvisDaemonNotConnected, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Meeting-mode (TASK-005) — SendSystemAudioFrame wire shape + disconnect test.
//
// Mirrors the PTT tests above. The Go-side helper is a thin pass-through to
// Send(); the daemon-side decoding/injection is covered by
// scripts/jarvis-daemon/tests/test_meeting_handlers.py (TASK-013).
//
// The disconnected-conn test is the analogous Go-side failure case to the
// PTT tests: callers must get ErrJarvisDaemonNotConnected rather than a panic
// when the WS is down. base64 payload validity is the daemon's problem; the
// Go helper just forwards what it's given.
// ---------------------------------------------------------------------------

func TestSendSystemAudioFrameWireShape(t *testing.T) {
	rig := startJarvisWSRig(t)
	defer rig.close()

	const payload = "aGVsbG8=" // base64("hello")

	if err := rig.dc.SendSystemAudioFrame(payload); err != nil {
		t.Fatalf("SendSystemAudioFrame returned error: %v", err)
	}

	frame := readFrame(t, rig.client)

	if len(frame) != 2 {
		t.Errorf("system_audio frame should have exactly two fields (type+data), got %d (%v)", len(frame), frame)
	}
	if frame["type"] != "system_audio" {
		t.Errorf("system_audio frame type wrong: want %q, got %v (full=%v)", "system_audio", frame["type"], frame)
	}
	if frame["data"] != payload {
		t.Errorf("system_audio frame data wrong: want %q, got %v (full=%v)", payload, frame["data"], frame)
	}
}

func TestSendSystemAudioFrameDisconnectedConnReturnsSentinelError(t *testing.T) {
	// Case 1: brand-new conn struct — never connected. Must surface the
	// sentinel rather than panicking on a nil *websocket.Conn.
	dc := &JarvisDaemonConn{}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("SendSystemAudioFrame panicked on never-connected conn: %v", r)
		}
	}()

	if err := dc.SendSystemAudioFrame("aGVsbG8="); !errors.Is(err, ErrJarvisDaemonNotConnected) {
		t.Errorf("SendSystemAudioFrame on never-connected conn: want ErrJarvisDaemonNotConnected, got %v", err)
	}

	// Case 2: conn was open, then explicitly closed. Same sentinel
	// requirement applies.
	rig := startJarvisWSRig(t)
	defer rig.close()

	if !rig.dc.Connected() {
		t.Fatal("rig.dc reports not connected after dial — test setup is broken")
	}
	rig.dc.Close()

	if err := rig.dc.SendSystemAudioFrame("aGVsbG8="); !errors.Is(err, ErrJarvisDaemonNotConnected) {
		t.Errorf("SendSystemAudioFrame after Close: want ErrJarvisDaemonNotConnected, got %v", err)
	}
}

// guard against the http import being shaved off if the test file evolves.
var _ = http.StatusOK
