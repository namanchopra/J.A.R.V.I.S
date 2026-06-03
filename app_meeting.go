package main

// app_meeting.go — Wails-bound meeting-mode lifecycle (TASK-005).
//
// This file owns the audio-forwarding plumbing between the macOS
// ScreenCaptureKit bridge (internal/screencapture) and the daemon's
// WS. TASK-009 lands the user-facing StartMeeting / StopMeeting Wails
// bindings on top of the helpers exported here.
//
// Threading: the SCK Capturer invokes its onAudio callback from a
// serial dispatch queue managed by the macOS bridge, NOT from the Go
// main goroutine. The callback below copies bytes (already an
// allocation per TASK-004's contract), reads meetingActive under the
// mutex to drop a tail of post-stop frames, base64-encodes, and ships
// the result to the daemon WS. base64+JSON marshal is fast enough to
// run inline on the dispatch queue (~0.1 ms per ~640-byte chunk).
//
// Failure modes (and their handling):
//   - ErrPermissionDenied: emit "meeting:permission_error" Wails event
//     so the Settings panel (TASK-012) renders the "grant Screen
//     Recording" CTA. Return the error from startMeetingCapture.
//   - ErrUnsupportedOS: emit "meeting:permission_error" with a
//     macOS-13-required message. Return the error.
//   - WS send failure mid-capture: logged at WARN (we DO NOT stop the
//     capture — a transient WS hiccup shouldn't end the meeting).
//   - Capturer.Start error from a second call: surface via the
//     binding's return value; TASK-009 turns this into a user-facing
//     "meeting already in progress" message.

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/namanchopra/jarvis/internal/config"
	"github.com/namanchopra/jarvis/internal/screencapture"
	rt "github.com/wailsapp/wails/v2/pkg/runtime"
)

// stopMeetingTimeout bounds the wait inside StopMeeting() for the
// daemon's ``meeting_notes_written`` notification. Extracted to a
// package-level var (instead of an inline constant) so unit tests can
// inject a shorter value without altering production behaviour. 30s
// is generous enough for the LLM summarisation + file write on a slow
// network; beyond that we'd rather surface a timeout error than
// indefinitely block the Wails JS caller.
var stopMeetingTimeout = 30 * time.Second

// ErrMeetingAlreadyActive is returned by startMeetingCapture when a
// meeting is already in progress. Unexported because TASK-009 maps it
// to a user-facing error via the StartMeeting binding.
var ErrMeetingAlreadyActive = errors.New("meeting capture: already active")

// startMeetingCapture begins ScreenCaptureKit audio capture and wires
// the resulting PCM stream into the daemon WS as base64-encoded
// system_audio frames. Returns nil on success; ErrPermissionDenied /
// ErrUnsupportedOS / ErrMeetingAlreadyActive on the known failure paths.
//
// The caller (TASK-009) is responsible for sending the corresponding
// __meeting_start__ HUD command to the daemon AFTER this returns
// successfully, so the daemon's transcript-buffer state is in place
// before the first audio frame lands.
func (a *App) startMeetingCapture() error {
	a.meetingMu.Lock()
	if a.meetingActive {
		a.meetingMu.Unlock()
		return ErrMeetingAlreadyActive
	}
	if a.meetingCapturer == nil {
		a.meetingCapturer = screencapture.New()
	}
	cap := a.meetingCapturer
	a.meetingMu.Unlock()

	onAudio := func(pcm []byte) {
		// Drop frames that drain after Stop() returns -- see TASK-004
		// contract note about post-stop tail.
		a.meetingMu.Lock()
		active := a.meetingActive
		a.meetingMu.Unlock()
		if !active {
			return
		}
		conn := a.jarvisDaemonConn()
		if conn == nil {
			// Daemon disconnected mid-meeting. Log once-per-N rather
			// than spamming -- but for v1 a plain Warn is fine; the
			// user-visible state is what matters.
			slog.Warn("meeting: daemon disconnected, dropping audio frame", "bytes", len(pcm))
			return
		}
		b64 := base64.StdEncoding.EncodeToString(pcm)
		if err := conn.SendSystemAudioFrame(b64); err != nil {
			slog.Warn("meeting: SendSystemAudioFrame failed", "err", err)
			// Don't stop the capture on a transient send failure.
			// A persistent failure will surface as an empty transcript
			// and the user can stop the meeting manually.
		}
	}

	if err := cap.Start(onAudio); err != nil {
		switch {
		case errors.Is(err, screencapture.ErrPermissionDenied):
			a.emitMeetingError("Screen Recording permission required for meeting mode")
		case errors.Is(err, screencapture.ErrUnsupportedOS):
			a.emitMeetingError("Meeting mode requires macOS 13 or newer")
		case errors.Is(err, screencapture.ErrUnsupportedPlatform):
			a.emitMeetingError("Meeting mode is macOS only")
		default:
			slog.Warn("meeting: capturer.Start failed", "err", err)
		}
		return fmt.Errorf("startMeetingCapture: %w", err)
	}

	a.meetingMu.Lock()
	a.meetingActive = true
	a.meetingMu.Unlock()
	slog.Info("meeting capture started")
	return nil
}

// stopMeetingCapture halts ScreenCaptureKit capture. Idempotent: a
// second call returns nil. Some audio frames may continue to arrive
// briefly after this returns (TASK-004 contract); the callback drops
// them via the meetingActive guard.
//
// Caller (TASK-009) is responsible for sending the corresponding
// __meeting_stop__ HUD command to the daemon AFTER this returns.
func (a *App) stopMeetingCapture() error {
	a.meetingMu.Lock()
	if !a.meetingActive {
		a.meetingMu.Unlock()
		return nil
	}
	a.meetingActive = false
	cap := a.meetingCapturer
	a.meetingMu.Unlock()

	if cap == nil {
		return nil
	}
	if err := cap.Stop(); err != nil {
		slog.Warn("meeting: capturer.Stop failed", "err", err)
		return fmt.Errorf("stopMeetingCapture: %w", err)
	}
	slog.Info("meeting capture stopped")
	return nil
}

// emitMeetingError publishes "meeting:permission_error" on the Wails
// event bus so the Settings panel (TASK-012) renders the "grant Screen
// Recording" CTA. Mirrors emitHotkeyError in app_hotkey.go.
//
// Safe to call before a.ctx has been assigned: a nil context falls
// through to the runtime as a no-op rather than panicking, but we
// guard explicitly to keep the slog trail clean.
func (a *App) emitMeetingError(msg string) {
	if a == nil || a.ctx == nil {
		return
	}
	rt.EventsEmit(a.ctx, "meeting:permission_error", msg)
}

// emitMeetingState publishes "meeting:state" on the Wails event bus so
// the overlay (TASK-010) can sync its local meetingActive flag with
// the Go source of truth. payload is either "active" or "idle". Safe
// to call before a.ctx is assigned -- guard is identical to
// emitMeetingError above.
func (a *App) emitMeetingState(state string) {
	if a == nil || a.ctx == nil {
		return
	}
	rt.EventsEmit(a.ctx, "meeting:state", state)
}

// ---------------------------------------------------------------------------
// TASK-009: Wails-bound bindings — StartMeeting / StopMeeting /
// IsMeetingActive / TriggerMeetingRecap.
//
// These four methods are the user-facing surface for meeting mode.
// They sit on top of the unexported startMeetingCapture /
// stopMeetingCapture helpers (TASK-005) and the daemon HUD command
// dispatcher (TASK-006). The frontend (TASK-010) calls them via the
// auto-generated Wails JS bindings (window.go.main.App.StartMeeting,
// etc.).
//
// Coordination with the daemon's ``meeting_notes_written`` event:
// StopMeeting() awaits a path on a.meetingNotesCh which is fed by the
// jarvisEmitFn registered in app.go:startMobileAPI. That emitter peeks
// every daemon event and routes the ``meeting_notes_written`` payload
// onto the channel. The buffered-1 channel + drop-on-full pattern
// ensures the daemon read loop never blocks even if no one is
// awaiting.
// ---------------------------------------------------------------------------

// StartMeeting begins a meeting recording session. Activates the
// ScreenCaptureKit audio capture (TASK-004) and sends the
// __meeting_start__ HUD command to the daemon (TASK-006) which
// switches the daemon into note-taking mode.
//
// title is the user-visible name for the resulting markdown file
// (e.g. "Daily standup" from the calendar event). Empty / whitespace
// falls back to "untitled" in the daemon's filename slugify.
//
// Returns an error wrapped from screencapture
// (ErrPermissionDenied / ErrUnsupportedOS / ErrUnsupportedPlatform),
// the meeting-state guard (ErrMeetingAlreadyActive), or the daemon
// WS layer (ErrJarvisDaemonNotConnected). On error the meeting
// state is rolled back to inactive so callers can safely retry.
//
// On success a "meeting:state" Wails event is emitted with payload
// "active" so the overlay (TASK-010) flips its UI immediately.
func (a *App) StartMeeting(title string) error {
	title = strings.TrimSpace(title)

	if err := a.startMeetingCapture(); err != nil {
		if errors.Is(err, ErrMeetingAlreadyActive) {
			return fmt.Errorf("StartMeeting: meeting already in progress: %w", err)
		}
		return fmt.Errorf("StartMeeting: %w", err)
	}

	// Drain any stale path left on the channel from a prior run (e.g.
	// daemon emitted notes_written after a previous StopMeeting timed
	// out). Doing this here -- not in StopMeeting -- means the await
	// inside StopMeeting only ever sees a path written during the
	// current meeting's lifecycle.
	select {
	case <-a.meetingNotesCh:
	default:
	}

	// Send the HUD command. If this fails, roll back the capture so
	// we don't leave SCK running while the daemon thinks meeting mode
	// is off (otherwise system_audio frames would be silently dropped).
	conn := a.jarvisDaemonConn()
	if conn == nil {
		_ = a.stopMeetingCapture()
		return fmt.Errorf("StartMeeting: daemon not connected")
	}
	// The daemon's _handle_command extracts text and title fields from
	// the payload (see TASK-006 / _command_loop normalisation path).
	if err := conn.Send(map[string]string{
		"type":  "command",
		"text":  "__meeting_start__",
		"title": title,
	}); err != nil {
		_ = a.stopMeetingCapture()
		return fmt.Errorf("StartMeeting: send: %w", err)
	}

	// Notify the frontend that meeting mode is now active. The overlay
	// (TASK-010) subscribes to this event to render the red ring +
	// RECORDING MEETING state label.
	a.emitMeetingState("active")

	slog.Info("StartMeeting succeeded", "title", title)
	return nil
}

// StopMeeting ends the active meeting and returns the path to the
// markdown notes file the daemon wrote. The stopMeetingTimeout (30s
// default; tunable for tests) is generous enough for the LLM
// summarisation pass + file write under normal network conditions;
// if exceeded we return a timeout error and the frontend should
// advise the user to check ~/.jarvis/meetings/ manually -- the
// daemon may still complete the write asynchronously.
//
// Idempotent only in the "no meeting in progress" sense: calling
// twice rapidly when a meeting was active will succeed once and then
// return "no meeting in progress" on the second call.
//
// Emits "meeting:state" with payload "idle" immediately after sending
// the daemon stop signal -- BEFORE awaiting notes_written -- so the
// UI flips to idle while the (potentially slow) summary completes.
//
// Implementation notes: we await the daemon's ``meeting_notes_written``
// event via a buffered channel on *App (a.meetingNotesCh). The channel
// is fed by the jarvisEmitFn registered in app.go:startMobileAPI,
// which peeks every daemon WS event and routes the payload's "path"
// field onto the channel. See internal/api/handlers_jarvis_ws.go's
// "meeting_notes_written" case for the forwarding path.
func (a *App) StopMeeting() (string, error) {
	a.meetingMu.Lock()
	active := a.meetingActive
	a.meetingMu.Unlock()
	if !active {
		return "", fmt.Errorf("StopMeeting: no meeting in progress")
	}

	// Send the daemon-side stop FIRST. The daemon will schedule the
	// finalisation task and (eventually) emit meeting_notes_written.
	conn := a.jarvisDaemonConn()
	if conn == nil {
		_ = a.stopMeetingCapture()
		a.emitMeetingState("idle")
		return "", fmt.Errorf("StopMeeting: daemon not connected")
	}
	if err := conn.Send(map[string]string{
		"type": "command",
		"text": "__meeting_stop__",
	}); err != nil {
		_ = a.stopMeetingCapture()
		a.emitMeetingState("idle")
		return "", fmt.Errorf("StopMeeting: send: %w", err)
	}

	// Stop SCK capture. Daemon-side already has _MEETING_ACTIVE=false
	// so any straggling system_audio frames are dropped harmlessly.
	if err := a.stopMeetingCapture(); err != nil {
		// Logged but non-fatal -- the daemon may still complete the
		// summary even if we couldn't cleanly stop SCK. Continue to
		// await the notes-written event.
		slog.Warn("StopMeeting: stopMeetingCapture", "err", err)
	}

	// Emit state event BEFORE awaiting; the UI flips to idle immediately
	// even if the summary takes 20+ seconds.
	a.emitMeetingState("idle")

	// Await meeting_notes_written from the daemon.
	select {
	case path := <-a.meetingNotesCh:
		slog.Info("StopMeeting: notes_written received", "path", path)
		return path, nil
	case <-time.After(stopMeetingTimeout):
		return "", fmt.Errorf("StopMeeting: timed out waiting for daemon notes_written")
	}
}

// IsMeetingActive returns whether a meeting is currently being
// recorded. Wails JS callers use this to initialise their local UI
// state on mount (e.g. the overlay's recording-ring visual).
func (a *App) IsMeetingActive() bool {
	a.meetingMu.Lock()
	defer a.meetingMu.Unlock()
	return a.meetingActive
}

// ---------------------------------------------------------------------------
// TASK-015: ScreenCaptureKit permission denial UX hardening.
//
// ProbeMeetingPermission gives the UI an explicit first-launch hook to
// surface the macOS Screen Recording permission dialog BEFORE the user
// expects audio capture to be running. The overlay's record-meeting icon
// (TASK-010) calls this exactly once per user-profile (gated via
// localStorage on the React side) so a fresh install pops the system
// prompt rather than silently failing the first real StartMeeting call.
// ---------------------------------------------------------------------------

// ProbeMeetingPermission triggers the macOS Screen Recording
// permission prompt by attempting a minimal SCK Start+Stop cycle. On
// macOS, the first call to a screen-capture API surfaces the system
// permission dialog. Subsequent calls return immediately with
// ErrPermissionDenied if the user denied.
//
// Wired in TASK-015 so the overlay can prompt once on first-ever
// click of the record-meeting icon, rather than waiting until the
// user expects audio capture to be running.
//
// Returns:
//   - nil on success (permission granted, probe started+stopped cleanly)
//   - ErrPermissionDenied if the user denies
//   - ErrUnsupportedOS / ErrUnsupportedPlatform if SCK isn't available
//   - a wrapped error for any other failure
//
// Emits "meeting:permission_error" on a denial so the UI surfaces the
// CTA even if the caller swallows the error.
func (a *App) ProbeMeetingPermission() error {
	cap := screencapture.New()
	onAudio := func(pcm []byte) { /* noop — discard during probe */ }
	if err := cap.Start(onAudio); err != nil {
		switch {
		case errors.Is(err, screencapture.ErrPermissionDenied):
			a.emitMeetingError("Screen Recording permission required for meeting mode")
		case errors.Is(err, screencapture.ErrUnsupportedOS):
			a.emitMeetingError("Meeting mode requires macOS 13 or newer")
		case errors.Is(err, screencapture.ErrUnsupportedPlatform):
			a.emitMeetingError("Meeting mode is macOS only")
		default:
			slog.Warn("meeting: probe Start failed", "err", err)
		}
		return fmt.Errorf("ProbeMeetingPermission: %w", err)
	}
	if err := cap.Stop(); err != nil {
		slog.Warn("meeting: probe Stop failed", "err", err)
		// Don't fail the probe on a Stop error — the permission IS granted
		// (Start succeeded). The Stop failure is a transient cleanup issue.
	}
	return nil
}

// OpenMeetingNotesFolder reveals the configured MeetingNotesDir in
// Finder. Resolves '~' to the user's home, creates the directory if
// it doesn't exist yet (mkdir -p semantics), then shells out to the
// macOS `open` command.
//
// Returns nil on success; a wrapped error on failure (home lookup
// failed, mkdir failed, `open` failed). Errors are also slog-Warn'd
// so the user has a record even if the caller swallows the return.
//
// Wails-bound: the Settings → Meeting panel's "OPEN FOLDER ↗" button
// invokes this via the auto-generated JS binding so the panel can
// reveal the resolved (expanded) notes directory without having to do
// tilde-expansion on the JS side.
func (a *App) OpenMeetingNotesFolder() error {
	cfg := config.Get()
	dir := ""
	if cfg != nil {
		dir = strings.TrimSpace(cfg.MeetingNotesDir)
	}
	if dir == "" {
		dir = "~/.jarvis/meetings"
	}
	if strings.HasPrefix(dir, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			slog.Warn("OpenMeetingNotesFolder: UserHomeDir failed", "err", err)
			return fmt.Errorf("OpenMeetingNotesFolder: %w", err)
		}
		dir = filepath.Join(home, strings.TrimPrefix(dir, "~"))
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		slog.Warn("OpenMeetingNotesFolder: MkdirAll failed", "dir", dir, "err", err)
		return fmt.Errorf("OpenMeetingNotesFolder: %w", err)
	}
	cmd := exec.Command("open", dir)
	if err := cmd.Run(); err != nil {
		slog.Warn("OpenMeetingNotesFolder: open command failed", "dir", dir, "err", err)
		return fmt.Errorf("OpenMeetingNotesFolder: %w", err)
	}
	return nil
}

// TriggerMeetingRecap asks the daemon to replay the last spoken recap
// via the cached _LAST_MEETING_RECAP (TASK-007 + TASK-008). Useful
// when the user missed the recap audio. No-op on the daemon side if
// no meeting has finalised this session.
//
// Does NOT touch meeting state on the Go side -- this is purely a
// daemon-side replay request. The TTS arrives via the existing
// RouterTTS pipeline and surfaces as a normal speaking-state event.
func (a *App) TriggerMeetingRecap() error {
	conn := a.jarvisDaemonConn()
	if conn == nil {
		return fmt.Errorf("TriggerMeetingRecap: daemon not connected")
	}
	if err := conn.Send(map[string]string{
		"type": "command",
		"text": "__meeting_recap__",
	}); err != nil {
		return fmt.Errorf("TriggerMeetingRecap: %w", err)
	}
	slog.Info("TriggerMeetingRecap sent to daemon")
	return nil
}
