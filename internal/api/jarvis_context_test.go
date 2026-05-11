package api

import (
	"testing"
	"time"

	"github.com/namanchopra/jarvis/internal/claude"
	"github.com/namanchopra/jarvis/internal/model"
)

// ---------------------------------------------------------------------------
// Fake provider
// ---------------------------------------------------------------------------

type fakeCtxProvider struct {
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

func (f *fakeCtxProvider) GetSessionIndicators() ([]claude.SessionIndicator, error) {
	return f.indicators, f.indicErr
}
func (f *fakeCtxProvider) GetPendingApprovals() ([]model.ApprovalRequest, error) {
	return f.approvals, f.approveErr
}
func (f *fakeCtxProvider) GetDashboardStats() (model.DashboardStats, error) {
	return f.stats, f.statsErr
}
func (f *fakeCtxProvider) GetTotalSpend() (model.TotalSpend, error) {
	return f.spend, f.spendErr
}
func (f *fakeCtxProvider) GetActiveSessions() ([]model.Session, error) {
	return f.sessions, f.sessionErr
}

// ---------------------------------------------------------------------------
// buildJarvisContext tests
// ---------------------------------------------------------------------------

func TestBuildJarvisContext_FullData(t *testing.T) {
	t.Parallel()

	now := time.Now()
	fp := &fakeCtxProvider{
		indicators: []claude.SessionIndicator{
			{
				PID:          12345,
				Name:         "maya-web",
				StartedAt:    now.Add(-2*time.Hour - 30*time.Minute).Unix(),
				HasQuestion:  false,
				LastActivity: "typing",
			},
			{
				PID:          67890,
				Name:         "auth-service",
				StartedAt:    now.Add(-5 * time.Minute).Unix(),
				HasQuestion:  true,
				LastActivity: "waiting",
			},
		},
		stats: model.DashboardStats{
			Total:      10,
			Running:    3,
			NeedsInput: 1,
		},
		approvals: []model.ApprovalRequest{
			{
				PID:         12345,
				SessionName: "maya-web",
				PromptText:  "Allow tool use: Edit file src/checkout.tsx?",
			},
		},
		spend: model.TotalSpend{
			AllTime:   100.00,
			ThisMonth: 25.50,
			Today:     1.23,
		},
	}

	msg := buildJarvisContext(fp, ContextPusherOpts{})

	// Type.
	if msg.Type != "context" {
		t.Errorf("Type = %q, want %q", msg.Type, "context")
	}

	// Sessions.
	if len(msg.Sessions) != 2 {
		t.Fatalf("Sessions count = %d, want 2", len(msg.Sessions))
	}
	if msg.Sessions[0].Name != "maya-web" {
		t.Errorf("Sessions[0].Name = %q, want %q", msg.Sessions[0].Name, "maya-web")
	}
	if msg.Sessions[0].Status != "running" {
		t.Errorf("Sessions[0].Status = %q, want %q", msg.Sessions[0].Status, "running")
	}
	if msg.Sessions[0].HasQuestion {
		t.Error("Sessions[0].HasQuestion should be false")
	}
	if msg.Sessions[1].Status != "needs-input" {
		t.Errorf("Sessions[1].Status = %q, want %q", msg.Sessions[1].Status, "needs-input")
	}
	if !msg.Sessions[1].HasQuestion {
		t.Error("Sessions[1].HasQuestion should be true")
	}

	// Costs.
	if msg.Costs.Today != 1.23 {
		t.Errorf("Costs.Today = %f, want 1.23", msg.Costs.Today)
	}
	if msg.Costs.Month != 25.50 {
		t.Errorf("Costs.Month = %f, want 25.50", msg.Costs.Month)
	}
	if msg.Costs.AllTime != 100.00 {
		t.Errorf("Costs.AllTime = %f, want 100.00", msg.Costs.AllTime)
	}

	// Approvals.
	if len(msg.Approvals) != 1 {
		t.Fatalf("Approvals count = %d, want 1", len(msg.Approvals))
	}
	if msg.Approvals[0].Name != "maya-web" {
		t.Errorf("Approvals[0].Name = %q, want %q", msg.Approvals[0].Name, "maya-web")
	}

	// Stats.
	if msg.Stats.Total != 10 {
		t.Errorf("Stats.Total = %d, want 10", msg.Stats.Total)
	}
	if msg.Stats.Running != 3 {
		t.Errorf("Stats.Running = %d, want 3", msg.Stats.Running)
	}
	if msg.Stats.NeedsInput != 1 {
		t.Errorf("Stats.NeedsInput = %d, want 1", msg.Stats.NeedsInput)
	}
}

func TestBuildJarvisContext_Empty(t *testing.T) {
	t.Parallel()

	fp := &fakeCtxProvider{
		indicators: []claude.SessionIndicator{},
		approvals:  []model.ApprovalRequest{},
		stats:      model.DashboardStats{},
		spend:      model.TotalSpend{},
	}

	msg := buildJarvisContext(fp, ContextPusherOpts{})

	if msg.Type != "context" {
		t.Errorf("Type = %q, want %q", msg.Type, "context")
	}
	if len(msg.Sessions) != 0 {
		t.Errorf("Sessions count = %d, want 0", len(msg.Sessions))
	}
	if msg.Sessions == nil {
		t.Error("Sessions should be empty slice, not nil")
	}
	if len(msg.Approvals) != 0 {
		t.Errorf("Approvals count = %d, want 0", len(msg.Approvals))
	}
	if msg.Approvals == nil {
		t.Error("Approvals should be empty slice, not nil")
	}
}

func TestBuildJarvisContext_PartialFailure(t *testing.T) {
	t.Parallel()

	fp := &fakeCtxProvider{
		indicErr:   errForTest("sessions dir unreadable"),
		stats:      model.DashboardStats{Total: 5, Running: 2},
		spend:      model.TotalSpend{Today: 0.50},
		approveErr: errForTest("approvals unavailable"),
	}

	msg := buildJarvisContext(fp, ContextPusherOpts{})

	// Sessions should be empty (not nil) despite error.
	if len(msg.Sessions) != 0 {
		t.Errorf("Sessions count = %d, want 0 (error path)", len(msg.Sessions))
	}
	if msg.Sessions == nil {
		t.Error("Sessions should be empty slice, not nil on error")
	}

	// Stats should still be populated.
	if msg.Stats.Total != 5 {
		t.Errorf("Stats.Total = %d, want 5", msg.Stats.Total)
	}
	if msg.Stats.Running != 2 {
		t.Errorf("Stats.Running = %d, want 2", msg.Stats.Running)
	}

	// Costs should still be populated.
	if msg.Costs.Today != 0.50 {
		t.Errorf("Costs.Today = %f, want 0.50", msg.Costs.Today)
	}

	// Approvals should be empty (not nil) despite error.
	if len(msg.Approvals) != 0 {
		t.Errorf("Approvals count = %d, want 0 (error path)", len(msg.Approvals))
	}
	if msg.Approvals == nil {
		t.Error("Approvals should be empty slice, not nil on error")
	}
}

// ---------------------------------------------------------------------------
// Helper tests
// ---------------------------------------------------------------------------

func TestMapSessionStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		activity    string
		hasQuestion bool
		want        string
	}{
		{"typing is running", "typing", false, "running"},
		{"tool_use is running", "tool_use", false, "running"},
		{"waiting is needs-input", "waiting", false, "needs-input"},
		{"idle is idle", "idle", false, "idle"},
		{"unknown maps to idle", "unknown", false, "idle"},
		{"hasQuestion overrides typing", "typing", true, "needs-input"},
		{"hasQuestion overrides idle", "idle", true, "needs-input"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := mapSessionStatus(tt.activity, tt.hasQuestion)
			if got != tt.want {
				t.Errorf("mapSessionStatus(%q, %v) = %q, want %q", tt.activity, tt.hasQuestion, got, tt.want)
			}
		})
	}
}

func TestCompactDuration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		d    time.Duration
		want string
	}{
		{"zero", 0, "0s"},
		{"negative", -5 * time.Second, "0s"},
		{"seconds", 45 * time.Second, "45s"},
		{"minutes", 12 * time.Minute, "12m"},
		{"hours and minutes", 2*time.Hour + 30*time.Minute, "2h 30m"},
		{"hours only", 3 * time.Hour, "3h"},
		{"days and hours", 2*24*time.Hour + 5*time.Hour, "2d 5h"},
		{"days only", 7 * 24 * time.Hour, "7d"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := compactDuration(tt.d)
			if got != tt.want {
				t.Errorf("compactDuration(%v) = %q, want %q", tt.d, got, tt.want)
			}
		})
	}
}

func TestTruncateText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		s    string
		n    int
		want string
	}{
		{"short string unchanged", "hello", 80, "hello"},
		{"exact length unchanged", "abc", 3, "abc"},
		{"truncated with ellipsis", "Allow tool use: Edit file src/checkout.tsx?", 20, "Allow tool use: Edit..."},
		{"empty string", "", 80, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := truncateText(tt.s, tt.n)
			if got != tt.want {
				t.Errorf("truncateText(%q, %d) = %q, want %q", tt.s, tt.n, got, tt.want)
			}
		})
	}
}

func TestBasename(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"simple name", "maya-web", "maya-web"},
		{"path with slashes", "/Users/naman/projects/maya-web", "maya-web"},
		{"single segment", "auth-service", "auth-service"},
		{"empty string", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := basename(tt.in)
			if got != tt.want {
				t.Errorf("basename(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestBuildJarvisContext_SessionNameUsesBasename(t *testing.T) {
	t.Parallel()

	now := time.Now()
	fp := &fakeCtxProvider{
		indicators: []claude.SessionIndicator{
			{
				PID:          123,
				Name:         "/Users/naman/projects/maya-web",
				StartedAt:    now.Add(-5 * time.Minute).Unix(),
				LastActivity: "typing",
			},
		},
		approvals: []model.ApprovalRequest{},
		stats:     model.DashboardStats{},
		spend:     model.TotalSpend{},
	}

	msg := buildJarvisContext(fp, ContextPusherOpts{})

	if len(msg.Sessions) != 1 {
		t.Fatalf("Sessions count = %d, want 1", len(msg.Sessions))
	}
	if msg.Sessions[0].Name != "maya-web" {
		t.Errorf("Sessions[0].Name = %q, want %q (should use basename)", msg.Sessions[0].Name, "maya-web")
	}
}

func TestBuildJarvisContext_ApprovalTextTruncation(t *testing.T) {
	t.Parallel()

	longText := "This is a very long approval prompt text that definitely exceeds eighty characters and should be truncated with an ellipsis appended at the end"
	fp := &fakeCtxProvider{
		indicators: []claude.SessionIndicator{},
		approvals: []model.ApprovalRequest{
			{PID: 1, SessionName: "test", PromptText: longText},
		},
		stats: model.DashboardStats{},
		spend: model.TotalSpend{},
	}

	msg := buildJarvisContext(fp, ContextPusherOpts{})

	if len(msg.Approvals) != 1 {
		t.Fatalf("Approvals count = %d, want 1", len(msg.Approvals))
	}
	// 80 chars + "..." = 83 total
	if len(msg.Approvals[0].Text) > 83 {
		t.Errorf("Approval text length = %d, want <= 83 (80 + ellipsis)", len(msg.Approvals[0].Text))
	}
}

func TestBuildJarvisContext_WithWarnings(t *testing.T) {
	t.Parallel()

	fp := &fakeCtxProvider{
		indicators: []claude.SessionIndicator{},
		approvals:  []model.ApprovalRequest{},
		stats:      model.DashboardStats{},
		spend:      model.TotalSpend{},
	}

	warnFn := func() []JarvisWarning {
		return []JarvisWarning{
			{Type: "shared-dependency", Message: "Both sessions modify package.json"},
			{Type: "shared-file", Message: "Both sessions modify files in shared package \"common\""},
		}
	}

	msg := buildJarvisContext(fp, ContextPusherOpts{Warnings: warnFn})

	if len(msg.Warnings) != 2 {
		t.Fatalf("Warnings count = %d, want 2", len(msg.Warnings))
	}
	if msg.Warnings[0].Type != "shared-dependency" {
		t.Errorf("Warnings[0].Type = %q, want %q", msg.Warnings[0].Type, "shared-dependency")
	}
	if msg.Warnings[1].Type != "shared-file" {
		t.Errorf("Warnings[1].Type = %q, want %q", msg.Warnings[1].Type, "shared-file")
	}
}

func TestBuildJarvisContext_WarningsNilFn(t *testing.T) {
	t.Parallel()

	fp := &fakeCtxProvider{
		indicators: []claude.SessionIndicator{},
		approvals:  []model.ApprovalRequest{},
		stats:      model.DashboardStats{},
		spend:      model.TotalSpend{},
	}

	msg := buildJarvisContext(fp, ContextPusherOpts{})

	// Warnings should be empty slice, not nil (Wails serialises nil as null).
	if msg.Warnings == nil {
		t.Error("Warnings should be empty slice, not nil")
	}
	if len(msg.Warnings) != 0 {
		t.Errorf("Warnings count = %d, want 0", len(msg.Warnings))
	}
}

func TestBuildJarvisContext_WarningsFnReturnsNil(t *testing.T) {
	t.Parallel()

	fp := &fakeCtxProvider{
		indicators: []claude.SessionIndicator{},
		approvals:  []model.ApprovalRequest{},
		stats:      model.DashboardStats{},
		spend:      model.TotalSpend{},
	}

	// Simulate GetImpactWarnings returning no conflicts.
	warnFn := func() []JarvisWarning { return nil }

	msg := buildJarvisContext(fp, ContextPusherOpts{Warnings: warnFn})

	// Even when the func returns nil, the message should contain an empty slice.
	if msg.Warnings == nil {
		t.Error("Warnings should be empty slice, not nil when func returns nil")
	}
	if len(msg.Warnings) != 0 {
		t.Errorf("Warnings count = %d, want 0", len(msg.Warnings))
	}
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

type errForTest string

func (e errForTest) Error() string { return string(e) }
