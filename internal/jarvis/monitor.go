package jarvis

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/namanchopra/jarvis/internal/claude"
	"github.com/namanchopra/jarvis/internal/jarvis/audio"
)

// ---------------------------------------------------------------------------
// SessionMonitor — proactive session alerts (Jarvis-style)
// ---------------------------------------------------------------------------

// SessionMonitor polls session indicators and pending approvals at a fixed
// interval, detects meaningful changes (session completed, failed, new
// approval), and speaks alerts when Jarvis is idle. If Jarvis is busy (listening,
// thinking, or speaking), alerts are queued and delivered once Jarvis returns
// to idle.
type SessionMonitor struct {
	provider ContextProvider
	speaker  *audio.Speaker
	vad      *audio.VAD
	emitFn   func(JarvisEvent)
	getState func() JarvisState

	// lastIndicators tracks the most recent LastActivity per PID so the
	// monitor can detect when a session disappears (completed/failed) or
	// a new one appears.
	lastIndicators map[int]indicatorSnapshot

	// lastApprovalPIDs tracks PIDs that already had a pending approval so
	// the monitor only alerts on genuinely new approvals.
	lastApprovalPIDs map[int]struct{}

	// pending collects alerts that arrived while Jarvis was not idle.
	pending []string
	mu      sync.Mutex
}

// indicatorSnapshot stores the fields we need to detect changes between
// successive polls.
type indicatorSnapshot struct {
	Name         string
	LastActivity string
	HasQuestion  bool
}

// NewSessionMonitor creates a monitor wired to the given subsystems.
func NewSessionMonitor(
	provider ContextProvider,
	speaker *audio.Speaker,
	vad *audio.VAD,
	emitFn func(JarvisEvent),
	getState func() JarvisState,
) *SessionMonitor {
	return &SessionMonitor{
		provider:         provider,
		speaker:          speaker,
		vad:              vad,
		emitFn:           emitFn,
		getState:         getState,
		lastIndicators:   make(map[int]indicatorSnapshot),
		lastApprovalPIDs: make(map[int]struct{}),
	}
}

// pollInterval controls how often the monitor checks for changes.
const pollInterval = 10 * time.Second

// Start runs the monitor loop until ctx is cancelled. It is intended to be
// launched as a goroutine: go m.Start(ctx).
func (m *SessionMonitor) Start(ctx context.Context) {
	// Let the app settle before the first poll — avoids noisy alerts from
	// the initial state snapshot arriving right after the auto-greet.
	select {
	case <-time.After(pollInterval):
	case <-ctx.Done():
		return
	}

	// Seed lastIndicators and lastApprovalPIDs with the current state so
	// we only alert on future changes, not the existing baseline.
	m.seed()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.poll(ctx)
		}
	}
}

// seed captures the current state as baseline without generating alerts.
func (m *SessionMonitor) seed() {
	indicators, err := m.provider.GetSessionIndicators()
	if err != nil {
		slog.Warn("monitor: failed to seed indicators", "err", err)
	} else {
		for _, ind := range indicators {
			m.lastIndicators[ind.PID] = indicatorSnapshot{
				Name:         ind.Name,
				LastActivity: ind.LastActivity,
				HasQuestion:  ind.HasQuestion,
			}
		}
	}

	approvals, err := m.provider.GetPendingApprovals()
	if err != nil {
		slog.Warn("monitor: failed to seed approvals", "err", err)
	} else {
		for _, a := range approvals {
			m.lastApprovalPIDs[a.PID] = struct{}{}
		}
	}

	slog.Debug("monitor: seeded",
		"sessions", len(m.lastIndicators),
		"approvals", len(m.lastApprovalPIDs),
	)
}

// poll runs one check cycle: fetch indicators + approvals, diff against
// the previous snapshot, and generate alerts for meaningful changes.
func (m *SessionMonitor) poll(ctx context.Context) {
	alerts := m.detectChanges()
	if len(alerts) == 0 {
		return
	}

	slog.Info("monitor: detected changes", "alerts", len(alerts))

	m.mu.Lock()
	m.pending = append(m.pending, alerts...)
	m.mu.Unlock()

	m.drainIfIdle(ctx)
}

// detectChanges compares the current indicators and approvals against the
// last-known state, updates the snapshots, and returns any alert messages.
func (m *SessionMonitor) detectChanges() []string {
	var alerts []string

	// --- Session indicators ---
	currentIndicators, err := m.provider.GetSessionIndicators()
	if err != nil {
		slog.Warn("monitor: failed to fetch indicators", "err", err)
		// Don't clear lastIndicators on transient errors — that would
		// cause false "completed" alerts on the next successful poll.
		return nil
	}

	currentPIDs := make(map[int]claude.SessionIndicator, len(currentIndicators))
	for _, ind := range currentIndicators {
		currentPIDs[ind.PID] = ind
	}

	// Detect sessions that disappeared (completed or failed).
	for pid, prev := range m.lastIndicators {
		if _, exists := currentPIDs[pid]; !exists {
			// PID gone — session ended. We don't have a reliable way
			// to know completed vs. failed from indicators alone (the
			// process simply exits). Use a neutral "finished" phrasing,
			// but check managed sessions for a more specific status.
			alert := m.classifyEnded(pid, prev.Name)
			alerts = append(alerts, alert)
			delete(m.lastIndicators, pid)
		}
	}

	// Detect new sessions that appeared.
	for _, ind := range currentIndicators {
		if _, existed := m.lastIndicators[ind.PID]; !existed {
			// Only alert for genuinely new sessions, not the seeded ones.
			alerts = append(alerts, fmt.Sprintf("%s just started.", ind.Name))
		}
		// Update the snapshot regardless.
		m.lastIndicators[ind.PID] = indicatorSnapshot{
			Name:         ind.Name,
			LastActivity: ind.LastActivity,
			HasQuestion:  ind.HasQuestion,
		}
	}

	// --- Pending approvals ---
	approvals, err := m.provider.GetPendingApprovals()
	if err != nil {
		slog.Warn("monitor: failed to fetch approvals", "err", err)
	} else {
		newApprovalPIDs := make(map[int]struct{}, len(approvals))
		for _, a := range approvals {
			newApprovalPIDs[a.PID] = struct{}{}
			if _, existed := m.lastApprovalPIDs[a.PID]; !existed {
				alerts = append(alerts, fmt.Sprintf("New approval waiting in %s.", a.SessionName))
			}
		}
		m.lastApprovalPIDs = newApprovalPIDs
	}

	return alerts
}

// classifyEnded checks the managed sessions to determine whether a
// disappeared PID completed or failed. Falls back to a neutral message
// if the session cannot be found (e.g. an unmanaged Claude Code session).
func (m *SessionMonitor) classifyEnded(pid int, name string) string {
	sessions, err := m.provider.GetActiveSessions()
	if err != nil {
		slog.Debug("monitor: could not look up managed sessions", "err", err)
		return fmt.Sprintf("Heads up \u2014 %s just finished.", name)
	}

	for _, s := range sessions {
		if s.PID == pid {
			switch s.Status {
			case "completed":
				return fmt.Sprintf("Heads up \u2014 %s just finished.", name)
			case "failed":
				return fmt.Sprintf("%s failed. Want me to check the error?", name)
			default:
				return fmt.Sprintf("Heads up \u2014 %s just finished.", name)
			}
		}
	}

	// PID not found in managed sessions — probably an unmanaged Claude Code
	// session that the user started outside AWM. Still worth mentioning.
	return fmt.Sprintf("Heads up \u2014 %s just finished.", name)
}

// drainIfIdle speaks all queued alerts if Jarvis is currently idle. If Jarvis
// is busy, the alerts stay queued and will be drained on the next poll
// when Jarvis is idle.
func (m *SessionMonitor) drainIfIdle(ctx context.Context) {
	if m.getState() != JarvisIdle {
		slog.Debug("monitor: jarvis busy, deferring alerts")
		return
	}

	m.mu.Lock()
	if len(m.pending) == 0 {
		m.mu.Unlock()
		return
	}
	alerts := m.pending
	m.pending = nil
	m.mu.Unlock()

	// Batch multiple alerts into a single spoken sentence.
	message := batchAlerts(alerts)

	slog.Info("monitor: speaking alert", "message", message)

	// Emit to frontend chat so the user sees it in the conversation panel.
	m.emitFn(JarvisEvent{
		Type:      "message",
		State:     JarvisSpeaking,
		Text:      message,
		Role:      "jarvis",
		Timestamp: time.Now(),
	})

	// Mute VAD to prevent Jarvis from hearing its own speech, speak, unmute.
	if m.vad != nil {
		m.vad.Mute()
	}
	if err := m.speaker.Speak(message); err != nil {
		slog.Error("monitor: TTS failed", "err", err)
	}
	if m.vad != nil {
		m.vad.Unmute()
	}

	// Check context in case shutdown happened during speech.
	if ctx.Err() != nil {
		return
	}
}

// batchAlerts combines multiple alert strings into a single natural
// sentence. A single alert is returned as-is; multiple alerts are joined
// with "Also, " connectors for a conversational feel.
func batchAlerts(alerts []string) string {
	if len(alerts) == 0 {
		return ""
	}
	if len(alerts) == 1 {
		return alerts[0]
	}

	// Build: "Heads up — maya-web just finished. Also, new approval waiting
	// in auth-service."
	var sb strings.Builder
	for i, a := range alerts {
		if i == 0 {
			sb.WriteString(a)
		} else {
			sb.WriteString(" Also, ")
			// Lowercase the first letter of subsequent alerts for flow.
			if len(a) > 0 {
				sb.WriteString(strings.ToLower(a[:1]))
				sb.WriteString(a[1:])
			}
		}
	}
	return sb.String()
}
