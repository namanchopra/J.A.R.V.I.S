package jarvis

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/namanchopra/jarvis/internal/claude"
	"github.com/namanchopra/jarvis/internal/model"
)

// ---------------------------------------------------------------------------
// ContextProvider
// ---------------------------------------------------------------------------

// ContextProvider defines the data methods Jarvis needs to assemble a context
// snapshot. The App struct in app.go already satisfies this interface.
type ContextProvider interface {
	GetSessionIndicators() ([]claude.SessionIndicator, error)
	GetPendingApprovals() ([]model.ApprovalRequest, error)
	GetDashboardStats() (model.DashboardStats, error)
	GetTotalSpend() (model.TotalSpend, error)
	GetActiveSessions() ([]model.Session, error)
}

// ---------------------------------------------------------------------------
// AssembleContext
// ---------------------------------------------------------------------------

// AssembleContext gathers data from the provider and formats it as a
// structured text block suitable for inclusion in a Claude API system prompt.
// If any individual data source fails, the snapshot still includes whatever
// succeeded and marks the failed section as unavailable.
func AssembleContext(provider ContextProvider) string {
	var sb strings.Builder

	now := time.Now()
	fmt.Fprintf(&sb, "## Current State (as of %s)\n", now.Format("2006-01-02 15:04"))

	writeSessions(&sb, provider, now)
	writeTasks(&sb, provider)
	writeApprovals(&sb, provider)
	writeCost(&sb, provider)

	return sb.String()
}

// ---------------------------------------------------------------------------
// Section writers
// ---------------------------------------------------------------------------

// writeSessions adds the Sessions section to the snapshot. It uses
// SessionIndicators for activity heuristics and falls back to managed
// sessions from the store if indicators are unavailable.
func writeSessions(sb *strings.Builder, p ContextProvider, now time.Time) {
	sb.WriteString("\n### Sessions\n")

	indicators, err := p.GetSessionIndicators()
	if err != nil {
		slog.Error("context: failed to load session indicators", "err", err)
		sb.WriteString("- (unavailable -- failed to load session data)\n")
		return
	}

	if len(indicators) == 0 {
		sb.WriteString("- No active sessions\n")
		return
	}

	// Summary line: count and names with status.
	names := make([]string, 0, len(indicators))
	for _, ind := range indicators {
		names = append(names, fmt.Sprintf("%s (%s)", ind.Name, activityLabel(ind)))
	}
	fmt.Fprintf(sb, "- %d active session%s: %s\n",
		len(indicators), plural(len(indicators)), strings.Join(names, ", "))

	// Per-session detail.
	for _, ind := range indicators {
		duration := formatDuration(now.Sub(time.Unix(ind.StartedAt, 0)))
		label := activityLabel(ind)

		detail := fmt.Sprintf("running for %s, no issues", duration)
		if ind.HasQuestion {
			detail = fmt.Sprintf("waiting for input (has pending question)")
		}

		fmt.Fprintf(sb, "- %s: %s for %s, %s\n", ind.Name, label, duration, detail)
	}
}

// writeTasks adds the Tasks section to the snapshot.
func writeTasks(sb *strings.Builder, p ContextProvider) {
	sb.WriteString("\n### Tasks\n")

	stats, err := p.GetDashboardStats()
	if err != nil {
		slog.Error("context: failed to load dashboard stats", "err", err)
		sb.WriteString("- (unavailable -- failed to load task data)\n")
		return
	}

	if stats.Total == 0 {
		sb.WriteString("- No tasks\n")
		return
	}

	parts := make([]string, 0, 5)
	if stats.Running > 0 {
		parts = append(parts, fmt.Sprintf("%d running", stats.Running))
	}
	if stats.Pending > 0 {
		parts = append(parts, fmt.Sprintf("%d pending", stats.Pending))
	}
	if stats.Done > 0 {
		parts = append(parts, fmt.Sprintf("%d done", stats.Done))
	}
	if stats.Failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", stats.Failed))
	}
	if stats.NeedsInput > 0 {
		parts = append(parts, fmt.Sprintf("%d needs-input", stats.NeedsInput))
	}

	fmt.Fprintf(sb, "- %d total: %s\n", stats.Total, strings.Join(parts, ", "))
}

// writeApprovals adds the Approvals section to the snapshot.
func writeApprovals(sb *strings.Builder, p ContextProvider) {
	sb.WriteString("\n### Approvals\n")

	approvals, err := p.GetPendingApprovals()
	if err != nil {
		slog.Error("context: failed to load pending approvals", "err", err)
		sb.WriteString("- (unavailable -- failed to load approval data)\n")
		return
	}

	if len(approvals) == 0 {
		sb.WriteString("- No approvals waiting\n")
		return
	}

	fmt.Fprintf(sb, "- %d approval%s waiting:\n", len(approvals), plural(len(approvals)))
	for _, a := range approvals {
		fmt.Fprintf(sb, "  - PID %d (%s): %q\n", a.PID, a.SessionName, a.PromptText)
	}
}

// writeCost adds the Cost section to the snapshot.
func writeCost(sb *strings.Builder, p ContextProvider) {
	sb.WriteString("\n### Cost\n")

	spend, err := p.GetTotalSpend()
	if err != nil {
		slog.Error("context: failed to load cost data", "err", err)
		sb.WriteString("- (unavailable -- failed to load cost data)\n")
		return
	}

	fmt.Fprintf(sb, "- Today: $%.2f | This month: $%.2f | All time: $%.2f\n",
		spend.Today, spend.ThisMonth, spend.AllTime)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// activityLabel returns a human-readable label for a session indicator's
// current activity. HasQuestion takes priority over the raw LastActivity
// field because it represents a blocking state the user should know about.
func activityLabel(ind claude.SessionIndicator) string {
	if ind.HasQuestion {
		return "waiting for input"
	}
	switch ind.LastActivity {
	case "typing":
		return "running"
	case "tool_use":
		return "running"
	case "waiting":
		return "waiting for input"
	default:
		return ind.LastActivity
	}
}

// formatDuration converts a time.Duration into a concise human-readable
// string like "5m", "2h12m", or "3d1h". Seconds are only shown when the
// duration is under one minute.
func formatDuration(d time.Duration) string {
	if d < 0 {
		return "0s"
	}

	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60

	switch {
	case days > 0:
		if hours > 0 {
			return fmt.Sprintf("%dd%dh", days, hours)
		}
		return fmt.Sprintf("%dd", days)
	case hours > 0:
		if minutes > 0 {
			return fmt.Sprintf("%dh%dm", hours, minutes)
		}
		return fmt.Sprintf("%dh", hours)
	case minutes > 0:
		return fmt.Sprintf("%dm", minutes)
	default:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
}

// plural returns "s" when n != 1, for simple English pluralisation.
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
