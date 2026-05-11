package api

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/namanchopra/jarvis/internal/jarvis"
)

// ---------------------------------------------------------------------------
// Compact JSON types for daemon context pushes
// ---------------------------------------------------------------------------

// jarvisContextMsg is the top-level message pushed to the Python daemon.
type jarvisContextMsg struct {
	Type      string           `json:"type"`
	Sessions  []jarvisSession  `json:"sessions"`
	Costs     jarvisCosts      `json:"costs"`
	Approvals []jarvisApproval `json:"approvals"`
	Stats     jarvisStats      `json:"stats"`
	Warnings  []JarvisWarning  `json:"warnings"`
}

// JarvisWarning is a compact representation of a cross-session conflict.
// Exported so callers (app.go) can construct values to pass via WarningsFn.
type JarvisWarning struct {
	Type    string `json:"type"`    // "shared-file", "shared-dependency", "api-contract"
	Message string `json:"message"` // human-readable description
}

// jarvisSession is a compact representation of an active Claude session.
type jarvisSession struct {
	Name        string `json:"name"`
	Workspace   string `json:"workspace,omitempty"` // CMux workspace title (user-assigned name)
	Status      string `json:"status"`              // "running", "needs-input", "idle"
	Duration    string `json:"duration"`             // "2h 30m"
	HasQuestion bool   `json:"hasQuestion"`
}

// jarvisCosts is a compact cost summary across three time horizons.
type jarvisCosts struct {
	Today   float64 `json:"today"`
	Month   float64 `json:"month"`
	AllTime float64 `json:"allTime"`
}

// jarvisApproval is a compact representation of a pending approval prompt.
type jarvisApproval struct {
	Name string `json:"name"`
	Text string `json:"text"` // first 80 chars of prompt
}

// jarvisStats is a compact representation of task counts.
type jarvisStats struct {
	Running    int `json:"running"`
	NeedsInput int `json:"needsInput"`
	Total      int `json:"total"`
}

// ---------------------------------------------------------------------------
// Context pusher
// ---------------------------------------------------------------------------

// WorkspaceNameFn returns a map of CWD path -> workspace title for CMux
// workspace name enrichment. Nil is safe (no enrichment).
type WorkspaceNameFn func() map[string]string

// WarningsFn returns cross-session impact warnings to include in the context
// push. Nil is safe (no warnings section).
type WarningsFn func() []JarvisWarning

// ContextPusherOpts holds the optional enrichment functions passed to
// StartContextPusher. Using a struct avoids ambiguous variadic parameters
// when multiple optional callbacks are needed.
type ContextPusherOpts struct {
	WorkspaceNames WorkspaceNameFn // CWD -> workspace title lookup
	Warnings       WarningsFn      // cross-session impact warnings
}

// StartContextPusher runs a goroutine that pushes environment context to the
// Python Jarvis daemon every interval via the WebSocket connection. The optional
// opts provide enrichment callbacks for workspace names and impact warnings.
func StartContextPusher(ctx context.Context, conn *JarvisDaemonConn, provider jarvis.ContextProvider, interval time.Duration, opts ...ContextPusherOpts) {
	var o ContextPusherOpts
	if len(opts) > 0 {
		o = opts[0]
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		logger := slog.With("component", "jarvis-context-pusher")
		logger.Info("context pusher started", "interval", interval)

		for {
			select {
			case <-ctx.Done():
				logger.Info("context pusher stopped")
				return
			case <-ticker.C:
				if conn == nil || !conn.Connected() {
					continue
				}

				msg := buildJarvisContext(provider, o)
				if err := conn.Send(msg); err != nil {
					logger.Debug("context push failed", "err", err)
				}
			}
		}
	}()
}

// buildJarvisContext assembles a compact JSON-friendly struct from the provider.
// Each data source is fetched independently -- failures are logged and the
// corresponding field is left empty/zero rather than aborting the entire push.
func buildJarvisContext(provider jarvis.ContextProvider, opts ContextPusherOpts) jarvisContextMsg {
	msg := jarvisContextMsg{Type: "context"}
	now := time.Now()

	// Build workspace name lookup (CWD -> title).
	var wsNames map[string]string
	if opts.WorkspaceNames != nil {
		wsNames = opts.WorkspaceNames()
	}

	// Sessions.
	indicators, err := provider.GetSessionIndicators()
	if err != nil {
		slog.Debug("context: failed to load session indicators", "err", err)
	} else {
		sessions := make([]jarvisSession, 0, len(indicators))
		for _, ind := range indicators {
			sess := jarvisSession{
				Name:        basename(ind.Name),
				Status:      mapSessionStatus(ind.LastActivity, ind.HasQuestion),
				Duration:    compactDuration(now.Sub(time.Unix(ind.StartedAt, 0))),
				HasQuestion: ind.HasQuestion,
			}
			// Enrich with CMux workspace title if available.
			if wsNames != nil {
				if title, ok := wsNames[ind.CWD]; ok {
					sess.Workspace = title
				}
			}
			sessions = append(sessions, sess)
		}
		msg.Sessions = sessions
	}
	if msg.Sessions == nil {
		msg.Sessions = []jarvisSession{}
	}

	// Costs.
	spend, err := provider.GetTotalSpend()
	if err != nil {
		slog.Debug("context: failed to load cost data", "err", err)
	} else {
		msg.Costs = jarvisCosts{
			Today:   spend.Today,
			Month:   spend.ThisMonth,
			AllTime: spend.AllTime,
		}
	}

	// Approvals.
	approvals, err := provider.GetPendingApprovals()
	if err != nil {
		slog.Debug("context: failed to load pending approvals", "err", err)
	} else {
		compact := make([]jarvisApproval, 0, len(approvals))
		for _, a := range approvals {
			compact = append(compact, jarvisApproval{
				Name: basename(a.SessionName),
				Text: truncateText(a.PromptText, 80),
			})
		}
		msg.Approvals = compact
	}
	if msg.Approvals == nil {
		msg.Approvals = []jarvisApproval{}
	}

	// Stats.
	stats, err := provider.GetDashboardStats()
	if err != nil {
		slog.Debug("context: failed to load dashboard stats", "err", err)
	} else {
		msg.Stats = jarvisStats{
			Running:    stats.Running,
			NeedsInput: stats.NeedsInput,
			Total:      stats.Total,
		}
	}

	// Warnings (cross-session impact conflicts).
	if opts.Warnings != nil {
		msg.Warnings = opts.Warnings()
	}
	if msg.Warnings == nil {
		msg.Warnings = []JarvisWarning{}
	}

	return msg
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// basename returns the last path segment, used for compact session names.
// Falls back to the input string if it contains no separators.
func basename(name string) string {
	b := filepath.Base(name)
	if b == "." || b == "/" {
		return name
	}
	return b
}

// mapSessionStatus converts a SessionIndicator's activity + question state
// into a compact status string for the daemon.
func mapSessionStatus(lastActivity string, hasQuestion bool) string {
	if hasQuestion {
		return "needs-input"
	}
	switch lastActivity {
	case "typing", "tool_use":
		return "running"
	case "waiting":
		return "needs-input"
	case "idle":
		return "idle"
	default:
		return "idle"
	}
}

// compactDuration formats a duration as a concise human-readable string
// like "5m", "2h 30m", or "3d 1h".
func compactDuration(d time.Duration) string {
	if d < 0 {
		return "0s"
	}

	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60

	switch {
	case days > 0 && hours > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case days > 0:
		return fmt.Sprintf("%dd", days)
	case hours > 0 && minutes > 0:
		return fmt.Sprintf("%dh %dm", hours, minutes)
	case hours > 0:
		return fmt.Sprintf("%dh", hours)
	case minutes > 0:
		return fmt.Sprintf("%dm", minutes)
	default:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
}

// truncateText returns at most n characters of s. If truncated, it appends
// an ellipsis. This is used to keep approval prompt text compact.
func truncateText(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}
