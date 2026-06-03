package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/namanchopra/jarvis/internal/claude"
	"github.com/namanchopra/jarvis/internal/impact"
	"github.com/namanchopra/jarvis/internal/model"

	"github.com/labstack/echo/v4"
)

// PushProvider abstracts the App methods needed by the push notification
// handler. This avoids importing the main package and prevents circular
// dependencies.
type PushProvider interface {
	GetSessionIndicators() ([]claude.SessionIndicator, error)
	ListSessions(statusFilter string) ([]model.Session, error)
	GetImpactWarnings() ([]impact.ImpactWarning, error)
}

// PushHandler manages Expo push token storage and background polling for
// sessions that need user input, session completion/failure, and cross-session
// conflicts. It is safe for concurrent use.
type PushHandler struct {
	app        PushProvider
	mu         sync.RWMutex
	pushToken  string   // Latest registered token (kept for backward compatibility).
	pushTokens []string // All distinct tokens we've seen (TASK-028 — multi-device fan-out).
	httpClient *http.Client

	// prevSessionStates tracks the last-seen status for each managed session
	// (by session ID) so we can detect running->completed and running->failed
	// transitions without re-notifying.
	prevSessionStates map[string]model.SessionStatus

	// notifiedWarnings tracks impact warning IDs that have already been sent
	// as push notifications. Entries are removed when the warning disappears.
	notifiedWarnings map[string]bool
}

// pushTokenRequest is the expected JSON body for POST /push-token.
//
// `Platform` is optional and informational (TASK-028 — mobile/lib/push.ts
// sends "ios" / "android" so a future dedupe-by-platform path doesn't need a
// schema bump). The handler treats it as a hint, not a key.
type pushTokenRequest struct {
	Token    string `json:"token"`
	Platform string `json:"platform,omitempty"`
}

// RegisterPushRoutes mounts push-notification endpoints on the provided Echo
// route group and returns the handler so the caller can start the background
// poller.
func RegisterPushRoutes(g *echo.Group, app PushProvider) *PushHandler {
	h := &PushHandler{
		app:               app,
		httpClient:         &http.Client{Timeout: 10 * time.Second},
		prevSessionStates: make(map[string]model.SessionStatus),
		notifiedWarnings:  make(map[string]bool),
	}

	g.POST("/push-token", h.registerToken)

	return h
}

// registerToken handles POST /push-token.
func (h *PushHandler) registerToken(c echo.Context) error {
	var req pushTokenRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "invalid JSON body",
		})
	}

	if req.Token == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "token is required",
		})
	}

	h.mu.Lock()
	h.pushToken = req.Token
	// TASK-028: maintain a de-duped list of all known tokens so the test-push
	// binding (JarvisSendTestPush) and any future fan-out logic can target
	// every registered device, not just the most recent one. Linear scan is
	// fine — token cardinality stays in the single digits (one per paired
	// phone) and the registration path is cold.
	found := false
	for _, t := range h.pushTokens {
		if t == req.Token {
			found = true
			break
		}
	}
	if !found {
		h.pushTokens = append(h.pushTokens, req.Token)
	}
	h.mu.Unlock()

	slog.Info("push token registered",
		"token_prefix", truncateToken(req.Token),
		"platform", req.Platform,
	)
	return c.JSON(http.StatusOK, map[string]bool{"ok": true})
}

// PushTokens returns a snapshot of all distinct Expo push tokens currently
// registered with this handler. Used by the Wails-bound JarvisSendTestPush
// binding (app_push.go) to fan a test notification out to every paired
// device. Returns an empty slice (never nil) when no tokens have been
// registered, so the caller can length-check without a nil guard.
func (h *PushHandler) PushTokens() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if len(h.pushTokens) == 0 {
		return []string{}
	}
	out := make([]string, len(h.pushTokens))
	copy(out, h.pushTokens)
	return out
}

// SendExpoPushToAll fans out a notification with the same payload to every
// registered push token. Returns the count of sends that landed with a 2xx
// or non-5xx response from the Expo push service. Used by the
// JarvisSendTestPush Wails binding (app_push.go) to surface a manual
// "Test push" button in Settings -> Permissions. Errors per-token are
// logged but do not abort the fan-out.
func (h *PushHandler) SendExpoPushToAll(title, body string, data map[string]interface{}) int {
	tokens := h.PushTokens()
	sent := 0
	for _, t := range tokens {
		if err := h.sendExpoPush(t, title, body, data); err != nil {
			slog.Warn("push fan-out: send failed", "err", err, "token_prefix", truncateToken(t))
			continue
		}
		sent++
	}
	return sent
}

// StartPushPoller begins a background goroutine that polls for sessions needing
// user input every 10 seconds and sends Expo push notifications. It returns
// immediately. The poller stops when ctx is cancelled.
func (h *PushHandler) StartPushPoller(ctx context.Context) {
	go h.pollLoop(ctx)
}

// pollLoop is the main polling loop. It runs until ctx is cancelled.
func (h *PushHandler) pollLoop(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	// notifiedPIDs tracks which PIDs have already been notified so we don't
	// spam the user. A PID is removed when it disappears from the indicators
	// (session ended), allowing re-notification if the same PID shows up again.
	notifiedPIDs := make(map[int]bool)

	slog.Info("push notification poller started")

	for {
		select {
		case <-ctx.Done():
			slog.Info("push notification poller stopped")
			return
		case <-ticker.C:
			h.pollOnce(notifiedPIDs)
		}
	}
}

// pollOnce performs a single poll cycle: fetch indicators, send notifications
// for new sessions needing input, and clean up stale PIDs. It also checks for
// managed session completion/failure transitions and cross-session conflicts.
func (h *PushHandler) pollOnce(notifiedPIDs map[int]bool) {
	h.mu.RLock()
	token := h.pushToken
	h.mu.RUnlock()

	if token == "" {
		return // no device registered yet
	}

	// -----------------------------------------------------------------------
	// 1. Approval notifications (existing behaviour)
	// -----------------------------------------------------------------------
	indicators, err := h.app.GetSessionIndicators()
	if err != nil {
		slog.Warn("push poller: failed to get session indicators", "err", err)
		return
	}

	// Build a set of currently active PIDs for cleanup.
	activePIDs := make(map[int]bool, len(indicators))

	for _, ind := range indicators {
		activePIDs[ind.PID] = true

		if ind.HasQuestion && !notifiedPIDs[ind.PID] {
			err := h.sendExpoPush(token, "AWM: Action Required", "A Claude Code session needs your approval", map[string]interface{}{
				"screen": "approvals",
			})
			if err != nil {
				slog.Warn("push poller: failed to send notification", "err", err, "pid", ind.PID)
				continue
			}
			notifiedPIDs[ind.PID] = true
			slog.Info("push notification sent", "pid", ind.PID)
		}
	}

	// Remove PIDs that are no longer active so they can be re-notified if they
	// reappear.
	for pid := range notifiedPIDs {
		if !activePIDs[pid] {
			delete(notifiedPIDs, pid)
		}
	}

	// -----------------------------------------------------------------------
	// 2. Session completion / failure notifications
	// -----------------------------------------------------------------------
	h.pollSessionTransitions(token)

	// -----------------------------------------------------------------------
	// 3. Cross-session conflict notifications
	// -----------------------------------------------------------------------
	h.pollImpactWarnings(token)
}

// pollSessionTransitions checks managed sessions for running->completed or
// running->failed transitions and sends a push notification for each.
func (h *PushHandler) pollSessionTransitions(token string) {
	sessions, err := h.app.ListSessions("")
	if err != nil {
		slog.Warn("push poller: failed to list sessions", "err", err)
		return
	}

	// Build a set of current session IDs so we can prune stale entries.
	currentIDs := make(map[string]bool, len(sessions))

	for _, sess := range sessions {
		currentIDs[sess.ID] = true

		prev, known := h.prevSessionStates[sess.ID]
		h.prevSessionStates[sess.ID] = sess.Status

		if !known {
			// First time seeing this session — record state, don't notify.
			continue
		}

		if prev == sess.Status {
			continue // no transition
		}

		name := sessionDisplayName(sess)

		switch {
		case isRunningState(prev) && sess.Status == model.SessionCompleted:
			err := h.sendExpoPush(token, "AWM: Session Completed", fmt.Sprintf("Session %s completed successfully.", name), map[string]interface{}{
				"screen":    "sessions",
				"sessionId": sess.ID,
			})
			if err != nil {
				slog.Warn("push poller: failed to send completion notification", "err", err, "session", sess.ID)
			} else {
				slog.Info("push notification sent: session completed", "session", sess.ID, "name", name)
			}

		case isRunningState(prev) && sess.Status == model.SessionFailed:
			err := h.sendExpoPush(token, "AWM: Session Failed", fmt.Sprintf("Session %s failed.", name), map[string]interface{}{
				"screen":    "sessions",
				"sessionId": sess.ID,
			})
			if err != nil {
				slog.Warn("push poller: failed to send failure notification", "err", err, "session", sess.ID)
			} else {
				slog.Info("push notification sent: session failed", "session", sess.ID, "name", name)
			}
		}
	}

	// Prune sessions that no longer exist from the state map.
	for id := range h.prevSessionStates {
		if !currentIDs[id] {
			delete(h.prevSessionStates, id)
		}
	}
}

// pollImpactWarnings checks for cross-session conflicts and sends a push
// notification for each new warning.
func (h *PushHandler) pollImpactWarnings(token string) {
	warnings, err := h.app.GetImpactWarnings()
	if err != nil {
		slog.Warn("push poller: failed to get impact warnings", "err", err)
		return
	}

	// Build a set of current warning IDs for cleanup.
	currentIDs := make(map[string]bool, len(warnings))

	for _, w := range warnings {
		currentIDs[w.ID] = true

		if h.notifiedWarnings[w.ID] {
			continue // already notified
		}

		body := fmt.Sprintf("Conflict detected between %s and %s.", w.SessionA, w.SessionB)
		err := h.sendExpoPush(token, "AWM: Cross-Session Conflict", body, map[string]interface{}{
			"screen":       "sessions",
			"conflictType": w.ConflictType,
		})
		if err != nil {
			slog.Warn("push poller: failed to send conflict notification", "err", err, "warning", w.ID)
			continue
		}
		h.notifiedWarnings[w.ID] = true
		slog.Info("push notification sent: impact warning", "warning", w.ID, "sessionA", w.SessionA, "sessionB", w.SessionB)
	}

	// Remove warnings that have been resolved so they can be re-notified if
	// the same conflict reappears.
	for id := range h.notifiedWarnings {
		if !currentIDs[id] {
			delete(h.notifiedWarnings, id)
		}
	}
}

// isRunningState returns true if the status represents an active/in-progress
// session that could transition to completed or failed.
func isRunningState(s model.SessionStatus) bool {
	return s == model.SessionRunning || s == model.SessionLaunching || s == model.SessionNeedsInput
}

// sessionDisplayName returns a human-readable label for a session, derived
// from the repo directory name. Falls back to the session ID if RepoPath is
// empty.
func sessionDisplayName(s model.Session) string {
	if s.RepoPath != "" {
		return filepath.Base(s.RepoPath)
	}
	if len(s.ID) > 8 {
		return s.ID[:8]
	}
	return s.ID
}

// sendExpoPush sends a push notification via the Expo push service using the
// handler's shared HTTP client.
func (h *PushHandler) sendExpoPush(token, title, body string, data map[string]interface{}) error {
	payload := map[string]interface{}{
		"to":    token,
		"title": title,
		"body":  body,
		"data":  data,
	}

	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal push payload: %w", err)
	}

	resp, err := h.httpClient.Post(
		"https://exp.host/--/api/v2/push/send",
		"application/json",
		bytes.NewReader(jsonBytes),
	)
	if err != nil {
		return fmt.Errorf("expo push request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("expo push returned status %d", resp.StatusCode)
	}

	slog.Debug("expo push sent successfully", "status", resp.StatusCode)
	return nil
}

// truncateToken returns a safe prefix of the push token for logging, avoiding
// exposure of the full token in logs.
func truncateToken(token string) string {
	if len(token) <= 20 {
		return token
	}
	return token[:20] + "..."
}
