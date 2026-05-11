package notify

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/namanchopra/jarvis/internal/claude"
	"github.com/namanchopra/jarvis/internal/config"
)

const rateLimitWindow = 30 * time.Second

// NotificationManager polls active Claude Code sessions and sends desktop
// notifications when a session needs user input (approval). It rate-limits
// notifications per PID so the same session doesn't spam the user.
type NotificationManager struct {
	lastNotified map[int]time.Time // PID -> last notification time
	mu           sync.Mutex
}

// NewNotificationManager returns a ready-to-use NotificationManager.
func NewNotificationManager() *NotificationManager {
	return &NotificationManager{
		lastNotified: make(map[int]time.Time),
	}
}

// CheckAndNotify inspects each indicator and sends desktop notifications for
// sessions that appear to be waiting for user input, subject to the current
// config toggles and a per-PID rate limit.
func (nm *NotificationManager) CheckAndNotify(indicators []claude.SessionIndicator, cfg *config.Config) {
	if !cfg.NotificationsEnabled {
		return
	}

	now := time.Now()

	// Build a set of active PIDs for cleanup later.
	activePIDs := make(map[int]struct{}, len(indicators))

	for _, ind := range indicators {
		activePIDs[ind.PID] = struct{}{}

		if !ind.HasQuestion || !cfg.NotifyOnApproval {
			continue
		}

		nm.mu.Lock()
		last, seen := nm.lastNotified[ind.PID]
		if seen && now.Sub(last) < rateLimitWindow {
			nm.mu.Unlock()
			continue
		}
		nm.lastNotified[ind.PID] = now
		nm.mu.Unlock()

		title := "Jarvis — Needs Approval"
		body := ind.Name + " is waiting for input"

		if err := Send(title, body); err != nil {
			slog.Error("failed to send notification", "pid", ind.PID, "err", err)
		}
	}

	// Remove stale entries for PIDs that are no longer active.
	nm.mu.Lock()
	for pid := range nm.lastNotified {
		if _, alive := activePIDs[pid]; !alive {
			delete(nm.lastNotified, pid)
		}
	}
	nm.mu.Unlock()
}

// Start begins a background loop that polls GetSessionIndicators every 5
// seconds and calls CheckAndNotify with the current config. It blocks until
// ctx is cancelled.
func (nm *NotificationManager) Start(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cfg := config.Get()

			indicators, err := claude.GetSessionIndicators()
			if err != nil {
				slog.Error("notification poll: failed to get session indicators", "err", err)
				continue
			}

			nm.CheckAndNotify(indicators, cfg)
		}
	}
}
