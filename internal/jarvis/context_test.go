package jarvis

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/namanchopra/jarvis/internal/claude"
	"github.com/namanchopra/jarvis/internal/model"
)

// ---------------------------------------------------------------------------
// Fake provider
// ---------------------------------------------------------------------------

// fakeProvider implements ContextProvider with configurable return values
// and injectable errors for testing partial-failure behaviour.
type fakeProvider struct {
	indicators []claude.SessionIndicator
	indicErr   error

	approvals  []model.ApprovalRequest
	approveErr error

	stats    model.DashboardStats
	statsErr error

	spend    model.TotalSpend
	spendErr error

	sessions   []model.Session
	sessionErr error
}

func (f *fakeProvider) GetSessionIndicators() ([]claude.SessionIndicator, error) {
	return f.indicators, f.indicErr
}

func (f *fakeProvider) GetPendingApprovals() ([]model.ApprovalRequest, error) {
	return f.approvals, f.approveErr
}

func (f *fakeProvider) GetDashboardStats() (model.DashboardStats, error) {
	return f.stats, f.statsErr
}

func (f *fakeProvider) GetTotalSpend() (model.TotalSpend, error) {
	return f.spend, f.spendErr
}

func (f *fakeProvider) GetActiveSessions() ([]model.Session, error) {
	return f.sessions, f.sessionErr
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestAssembleContext_FullData(t *testing.T) {
	t.Parallel()

	now := time.Now()
	fp := &fakeProvider{
		indicators: []claude.SessionIndicator{
			{
				PID:          12345,
				Name:         "maya-web",
				StartedAt:    now.Add(-12 * time.Minute).Unix(),
				HasQuestion:  false,
				LastActivity: "typing",
			},
			{
				PID:          67890,
				Name:         "auth-service",
				StartedAt:    now.Add(-5 * time.Minute).Unix(),
				HasQuestion:  false,
				LastActivity: "tool_use",
			},
			{
				PID:          11111,
				Name:         "mumz-cosmos",
				StartedAt:    now.Add(-30 * time.Minute).Unix(),
				HasQuestion:  true,
				LastActivity: "waiting",
			},
		},
		stats: model.DashboardStats{
			Total:      12,
			Running:    3,
			Pending:    2,
			Done:       5,
			Failed:     1,
			NeedsInput: 1,
		},
		approvals: []model.ApprovalRequest{
			{
				PID:         12345,
				SessionName: "maya-web",
				PromptText:  "Allow tool use: Edit file src/checkout.tsx?",
			},
			{
				PID:         67890,
				SessionName: "mumz-cosmos",
				PromptText:  "Allow bash: npm install lodash?",
			},
		},
		spend: model.TotalSpend{
			AllTime:   123.45,
			ThisMonth: 45.67,
			Today:     1.23,
		},
	}

	result := AssembleContext(fp)

	// Verify header.
	if !strings.Contains(result, "## Current State") {
		t.Error("missing header")
	}

	// Verify sessions section.
	if !strings.Contains(result, "### Sessions") {
		t.Error("missing Sessions section")
	}
	if !strings.Contains(result, "3 active sessions") {
		t.Error("missing session count")
	}
	if !strings.Contains(result, "maya-web") {
		t.Error("missing maya-web session")
	}
	if !strings.Contains(result, "mumz-cosmos") {
		t.Error("missing mumz-cosmos session")
	}
	if !strings.Contains(result, "waiting for input") {
		t.Error("missing 'waiting for input' for session with question")
	}

	// Verify tasks section.
	if !strings.Contains(result, "### Tasks") {
		t.Error("missing Tasks section")
	}
	if !strings.Contains(result, "12 total") {
		t.Error("missing task total")
	}
	if !strings.Contains(result, "3 running") {
		t.Error("missing running count")
	}
	if !strings.Contains(result, "1 needs-input") {
		t.Error("missing needs-input count")
	}

	// Verify approvals section.
	if !strings.Contains(result, "### Approvals") {
		t.Error("missing Approvals section")
	}
	if !strings.Contains(result, "2 approvals waiting") {
		t.Error("missing approval count")
	}
	if !strings.Contains(result, "PID 12345 (maya-web)") {
		t.Error("missing maya-web approval")
	}
	if !strings.Contains(result, "Allow bash: npm install lodash?") {
		t.Error("missing mumz-cosmos approval text")
	}

	// Verify cost section.
	if !strings.Contains(result, "### Cost") {
		t.Error("missing Cost section")
	}
	if !strings.Contains(result, "$1.23") {
		t.Error("missing today cost")
	}
	if !strings.Contains(result, "$45.67") {
		t.Error("missing this month cost")
	}
	if !strings.Contains(result, "$123.45") {
		t.Error("missing all time cost")
	}
}

func TestAssembleContext_Empty(t *testing.T) {
	t.Parallel()

	fp := &fakeProvider{
		indicators: []claude.SessionIndicator{},
		approvals:  []model.ApprovalRequest{},
		stats:      model.DashboardStats{},
		spend:      model.TotalSpend{},
	}

	result := AssembleContext(fp)

	if !strings.Contains(result, "No active sessions") {
		t.Error("expected 'No active sessions' for empty indicators")
	}
	if !strings.Contains(result, "No tasks") {
		t.Error("expected 'No tasks' for zero stats")
	}
	if !strings.Contains(result, "No approvals waiting") {
		t.Error("expected 'No approvals waiting' for empty approvals")
	}
	if !strings.Contains(result, "$0.00") {
		t.Error("expected zero cost values")
	}
}

func TestAssembleContext_PartialFailure_SessionsError(t *testing.T) {
	t.Parallel()

	fp := &fakeProvider{
		indicErr: errors.New("sessions dir unreadable"),
		stats:    model.DashboardStats{Total: 5, Running: 2, Pending: 1, Done: 2},
		spend:    model.TotalSpend{Today: 0.50},
	}

	result := AssembleContext(fp)

	// Sessions should show unavailable.
	if !strings.Contains(result, "unavailable") {
		t.Error("expected unavailable marker for sessions error")
	}
	// Tasks and cost should still be present.
	if !strings.Contains(result, "5 total") {
		t.Error("tasks should still render despite session error")
	}
	if !strings.Contains(result, "$0.50") {
		t.Error("cost should still render despite session error")
	}
}

func TestAssembleContext_PartialFailure_CostError(t *testing.T) {
	t.Parallel()

	fp := &fakeProvider{
		indicators: []claude.SessionIndicator{},
		stats:      model.DashboardStats{Total: 1, Done: 1},
		spendErr:   errors.New("cost file corrupt"),
	}

	result := AssembleContext(fp)

	// Cost should show unavailable.
	if !strings.Contains(result, "failed to load cost data") {
		t.Error("expected cost unavailable message")
	}
	// Other sections should render.
	if !strings.Contains(result, "1 total") {
		t.Error("tasks should still render despite cost error")
	}
}

func TestAssembleContext_PartialFailure_AllErrors(t *testing.T) {
	t.Parallel()

	fp := &fakeProvider{
		indicErr:   errors.New("fail"),
		approveErr: errors.New("fail"),
		statsErr:   errors.New("fail"),
		spendErr:   errors.New("fail"),
	}

	result := AssembleContext(fp)

	// Should still produce a valid snapshot with all sections marked unavailable.
	if !strings.Contains(result, "## Current State") {
		t.Error("header should always be present")
	}
	unavailableCount := strings.Count(result, "unavailable")
	if unavailableCount != 4 {
		t.Errorf("expected 4 unavailable markers, got %d", unavailableCount)
	}
}

func TestAssembleContext_SingleApproval(t *testing.T) {
	t.Parallel()

	fp := &fakeProvider{
		indicators: []claude.SessionIndicator{},
		approvals: []model.ApprovalRequest{
			{PID: 999, SessionName: "solo", PromptText: "Allow edit?"},
		},
		stats: model.DashboardStats{},
		spend: model.TotalSpend{},
	}

	result := AssembleContext(fp)

	// Singular "approval" not "approvals".
	if !strings.Contains(result, "1 approval waiting") {
		t.Error("expected singular 'approval' for count of 1")
	}
}

func TestFormatDuration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		d    time.Duration
		want string
	}{
		{"zero", 0, "0s"},
		{"negative", -5 * time.Second, "0s"},
		{"seconds", 45 * time.Second, "45s"},
		{"one minute", 1 * time.Minute, "1m"},
		{"minutes", 12 * time.Minute, "12m"},
		{"minutes with seconds", 12*time.Minute + 30*time.Second, "12m"},
		{"one hour", 1 * time.Hour, "1h"},
		{"hours and minutes", 2*time.Hour + 12*time.Minute, "2h12m"},
		{"one day", 24 * time.Hour, "1d"},
		{"days and hours", 3*24*time.Hour + 1*time.Hour, "3d1h"},
		{"days only", 7 * 24 * time.Hour, "7d"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := formatDuration(tt.d)
			if got != tt.want {
				t.Errorf("formatDuration(%v) = %q, want %q", tt.d, got, tt.want)
			}
		})
	}
}

func TestPlural(t *testing.T) {
	t.Parallel()

	tests := []struct {
		n    int
		want string
	}{
		{0, "s"},
		{1, ""},
		{2, "s"},
		{100, "s"},
	}

	for _, tt := range tests {
		got := plural(tt.n)
		if got != tt.want {
			t.Errorf("plural(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestActivityLabel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ind  claude.SessionIndicator
		want string
	}{
		{
			name: "has question overrides activity",
			ind:  claude.SessionIndicator{HasQuestion: true, LastActivity: "typing"},
			want: "waiting for input",
		},
		{
			name: "typing maps to running",
			ind:  claude.SessionIndicator{HasQuestion: false, LastActivity: "typing"},
			want: "running",
		},
		{
			name: "tool_use maps to running",
			ind:  claude.SessionIndicator{HasQuestion: false, LastActivity: "tool_use"},
			want: "running",
		},
		{
			name: "waiting maps to waiting for input",
			ind:  claude.SessionIndicator{HasQuestion: false, LastActivity: "waiting"},
			want: "waiting for input",
		},
		{
			name: "unknown passes through",
			ind:  claude.SessionIndicator{HasQuestion: false, LastActivity: "unknown"},
			want: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := activityLabel(tt.ind)
			if got != tt.want {
				t.Errorf("activityLabel() = %q, want %q", got, tt.want)
			}
		})
	}
}
