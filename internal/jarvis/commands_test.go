package jarvis

import (
	"errors"
	"strings"
	"testing"

	"github.com/namanchopra/jarvis/internal/model"
)

// ---------------------------------------------------------------------------
// Mock ActionProvider
// ---------------------------------------------------------------------------

type mockActionProvider struct {
	resumeCalled  bool
	resumeID      string
	resumeSession model.Session
	resumeErr     error

	stopCalled bool
	stopID     string
	stopErr    error

	launchCalled    bool
	launchAgentType string
	launchRepoPath  string
	launchPrompt    string
	launchSession   model.Session
	launchErr       error

	respondCalled   bool
	respondPID      int
	respondPIDs     []int // tracks all PIDs across multiple calls
	respondResponse string
	respondErr      error

	focusCalled bool
	focusPID    int
	focusErr    error

	broadcastCalled  bool
	broadcastCommand string
	broadcastResults map[int]string
	broadcastErr     error

	pendingApprovals []model.ApprovalRequest
	pendingErr       error

	// Git tracking fields
	gitStageCalled   bool
	gitStageRepoPath string
	gitStageErr      error

	gitCommitCalled   bool
	gitCommitRepoPath string
	gitCommitMessage  string
	gitCommitErr      error

	gitPushCalled   bool
	gitPushRepoPath string
	gitPushErr      error

	// Session list override — when nil, returns default test sessions.
	activeSessions []model.Session
	activeErr      error

	// CMux / terminal / system tracking fields
	sendToCMuxCalls        []string
	focusCMuxSurfaceCalls  []string
	sendToTerminalCalls    []string
	focusTerminalCalls     []string
	openAppCalls           []string
	openURLCalls           []string
	cmuxAvailable          bool
	sendToCMuxErr          error
	focusCMuxSurfaceErr    error
	openAppErr             error
	openURLErr             error
}

func (m *mockActionProvider) ResumeSession(sessionID string) (model.Session, error) {
	m.resumeCalled = true
	m.resumeID = sessionID
	return m.resumeSession, m.resumeErr
}

func (m *mockActionProvider) StopSession(sessionID string) error {
	m.stopCalled = true
	m.stopID = sessionID
	return m.stopErr
}

func (m *mockActionProvider) LaunchSession(agentType, repoPath, prompt string) (model.Session, error) {
	m.launchCalled = true
	m.launchAgentType = agentType
	m.launchRepoPath = repoPath
	m.launchPrompt = prompt
	return m.launchSession, m.launchErr
}

func (m *mockActionProvider) RespondToApproval(pid int, response string) error {
	m.respondCalled = true
	m.respondPID = pid
	m.respondPIDs = append(m.respondPIDs, pid)
	m.respondResponse = response
	return m.respondErr
}

func (m *mockActionProvider) GetPendingApprovals() ([]model.ApprovalRequest, error) {
	return m.pendingApprovals, m.pendingErr
}

func (m *mockActionProvider) GetActiveSessions() ([]model.Session, error) {
	if m.activeErr != nil {
		return nil, m.activeErr
	}
	if m.activeSessions != nil {
		return m.activeSessions, nil
	}
	// Default sessions for tests that don't configure their own.
	return []model.Session{
		{RepoPath: "/Users/test/projects/maya-web"},
		{RepoPath: "/Users/test/projects/auth-service"},
	}, nil
}

func (m *mockActionProvider) GitStageAll(repoPath string) error {
	m.gitStageCalled = true
	m.gitStageRepoPath = repoPath
	return m.gitStageErr
}

func (m *mockActionProvider) GitCommit(repoPath, message string) error {
	m.gitCommitCalled = true
	m.gitCommitRepoPath = repoPath
	m.gitCommitMessage = message
	return m.gitCommitErr
}

func (m *mockActionProvider) GitPush(repoPath string) error {
	m.gitPushCalled = true
	m.gitPushRepoPath = repoPath
	return m.gitPushErr
}

func (m *mockActionProvider) FocusSession(pid int) error {
	m.focusCalled = true
	m.focusPID = pid
	return m.focusErr
}

func (m *mockActionProvider) BroadcastToAll(command string) (map[int]string, error) {
	m.broadcastCalled = true
	m.broadcastCommand = command
	return m.broadcastResults, m.broadcastErr
}

func (m *mockActionProvider) SendToCMux(surfaceRef string, text string) error {
	m.sendToCMuxCalls = append(m.sendToCMuxCalls, surfaceRef+":"+text)
	return m.sendToCMuxErr
}
func (m *mockActionProvider) ReadFromCMux(surfaceRef string) (string, error) { return "", nil }
func (m *mockActionProvider) FocusCMuxSurface(surfaceRef string) error {
	m.focusCMuxSurfaceCalls = append(m.focusCMuxSurfaceCalls, surfaceRef)
	return m.focusCMuxSurfaceErr
}
func (m *mockActionProvider) SendToTerminal(windowID string, text string) error {
	m.sendToTerminalCalls = append(m.sendToTerminalCalls, windowID+":"+text)
	return nil
}
func (m *mockActionProvider) FocusTerminalWindow(windowID string) error {
	m.focusTerminalCalls = append(m.focusTerminalCalls, windowID)
	return nil
}
func (m *mockActionProvider) IsCMuxAvailable() bool { return m.cmuxAvailable }
func (m *mockActionProvider) OpenApp(appName string) error {
	m.openAppCalls = append(m.openAppCalls, appName)
	return m.openAppErr
}
func (m *mockActionProvider) OpenURL(url string) error {
	m.openURLCalls = append(m.openURLCalls, url)
	return m.openURLErr
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestExecute_ResumeSession(t *testing.T) {
	t.Parallel()

	mock := &mockActionProvider{
		resumeSession: model.Session{ID: "sess-123"},
	}
	router := NewCommandRouter(mock)

	cmd := &ActionCommand{
		Action:    ActionResumeSession,
		SessionID: "sess-123",
	}

	text, err := router.Execute(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !mock.resumeCalled {
		t.Error("ResumeSession was not called")
	}
	if mock.resumeID != "sess-123" {
		t.Errorf("ResumeSession called with ID %q, want %q", mock.resumeID, "sess-123")
	}
	if !strings.Contains(text, "Resuming") {
		t.Errorf("expected confirmation text containing 'Resuming', got %q", text)
	}
}

func TestExecute_StopSession(t *testing.T) {
	t.Parallel()

	mock := &mockActionProvider{}
	router := NewCommandRouter(mock)

	cmd := &ActionCommand{
		Action:    ActionStopSession,
		SessionID: "sess-456",
	}

	text, err := router.Execute(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !mock.stopCalled {
		t.Error("StopSession was not called")
	}
	if mock.stopID != "sess-456" {
		t.Errorf("StopSession called with ID %q, want %q", mock.stopID, "sess-456")
	}
	if !strings.Contains(text, "stopped") {
		t.Errorf("expected confirmation text containing 'stopped', got %q", text)
	}
}

func TestExecute_LaunchSession(t *testing.T) {
	t.Parallel()

	mock := &mockActionProvider{
		launchSession: model.Session{ID: "new-sess"},
	}
	router := NewCommandRouter(mock)

	cmd := &ActionCommand{
		Action:  ActionLaunchSession,
		Project: "/repos/maya-web",
		Prompt:  "implement auth flow",
	}

	text, err := router.Execute(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !mock.launchCalled {
		t.Error("LaunchSession was not called")
	}
	if mock.launchAgentType != string(model.AgentClaudeCode) {
		t.Errorf("LaunchSession agentType = %q, want %q", mock.launchAgentType, model.AgentClaudeCode)
	}
	if mock.launchRepoPath != "/repos/maya-web" {
		t.Errorf("LaunchSession repoPath = %q, want %q", mock.launchRepoPath, "/repos/maya-web")
	}
	if mock.launchPrompt != "implement auth flow" {
		t.Errorf("LaunchSession prompt = %q, want %q", mock.launchPrompt, "implement auth flow")
	}
	if !strings.Contains(text, "Launching") {
		t.Errorf("expected confirmation text containing 'Launching', got %q", text)
	}
}

func TestExecute_RespondApproval(t *testing.T) {
	t.Parallel()

	t.Run("approve", func(t *testing.T) {
		t.Parallel()

		mock := &mockActionProvider{}
		router := NewCommandRouter(mock)

		cmd := &ActionCommand{
			Action:   ActionRespondApproval,
			PID:      12345,
			Response: "approve",
		}

		text, err := router.Execute(cmd)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !mock.respondCalled {
			t.Error("RespondToApproval was not called")
		}
		if mock.respondPID != 12345 {
			t.Errorf("RespondToApproval PID = %d, want %d", mock.respondPID, 12345)
		}
		if mock.respondResponse != "approve" {
			t.Errorf("RespondToApproval response = %q, want %q", mock.respondResponse, "approve")
		}
		if !strings.Contains(text, "responded") {
			t.Errorf("expected confirmation text containing 'responded', got %q", text)
		}
	})

	t.Run("deny", func(t *testing.T) {
		t.Parallel()

		mock := &mockActionProvider{}
		router := NewCommandRouter(mock)

		cmd := &ActionCommand{
			Action:   ActionRespondApproval,
			PID:      67890,
			Response: "deny",
		}

		text, err := router.Execute(cmd)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !mock.respondCalled {
			t.Error("RespondToApproval was not called")
		}
		if mock.respondPID != 67890 {
			t.Errorf("RespondToApproval PID = %d, want %d", mock.respondPID, 67890)
		}
		if !strings.Contains(text, "denied") {
			t.Errorf("expected confirmation text containing 'denied', got %q", text)
		}
	})
}

func TestExecute_FocusSession(t *testing.T) {
	t.Parallel()

	mock := &mockActionProvider{}
	router := NewCommandRouter(mock)

	cmd := &ActionCommand{
		Action: ActionFocusSession,
		PID:    99999,
	}

	text, err := router.Execute(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !mock.focusCalled {
		t.Error("FocusSession was not called")
	}
	if mock.focusPID != 99999 {
		t.Errorf("FocusSession PID = %d, want %d", mock.focusPID, 99999)
	}
	if !strings.Contains(text, "Focused") {
		t.Errorf("expected confirmation text containing 'Focused', got %q", text)
	}
}

func TestExecute_Broadcast(t *testing.T) {
	t.Parallel()

	mock := &mockActionProvider{
		broadcastResults: map[int]string{
			100: "ok",
			200: "ok",
			300: "ok",
		},
	}
	router := NewCommandRouter(mock)

	cmd := &ActionCommand{
		Action:  ActionBroadcast,
		Command: "git status",
	}

	text, err := router.Execute(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !mock.broadcastCalled {
		t.Error("BroadcastToAll was not called")
	}
	if mock.broadcastCommand != "git status" {
		t.Errorf("BroadcastToAll command = %q, want %q", mock.broadcastCommand, "git status")
	}
	if !strings.Contains(text, "3 sessions") {
		t.Errorf("expected text containing '3 sessions', got %q", text)
	}
}

func TestExecute_UnknownAction(t *testing.T) {
	t.Parallel()

	mock := &mockActionProvider{}
	router := NewCommandRouter(mock)

	cmd := &ActionCommand{
		Action: "quantum_teleport",
	}

	text, err := router.Execute(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if text != "I can't do that yet." {
		t.Errorf("got %q, want %q", text, "I can't do that yet.")
	}

	// Verify no provider methods were called.
	if mock.resumeCalled || mock.stopCalled || mock.launchCalled ||
		mock.respondCalled || mock.focusCalled || mock.broadcastCalled {
		t.Error("provider method called for unknown action")
	}
}

func TestExecute_NilCommand(t *testing.T) {
	t.Parallel()

	mock := &mockActionProvider{}
	router := NewCommandRouter(mock)

	text, err := router.Execute(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if text != "No action to execute." {
		t.Errorf("got %q, want %q", text, "No action to execute.")
	}

	// Verify no provider methods were called.
	if mock.resumeCalled || mock.stopCalled || mock.launchCalled ||
		mock.respondCalled || mock.focusCalled || mock.broadcastCalled {
		t.Error("provider method called for nil command")
	}
}

func TestExecute_ProviderError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		cmd        *ActionCommand
		setupMock  func(m *mockActionProvider)
		wantSubstr string
	}{
		{
			name: "resume error",
			cmd:  &ActionCommand{Action: ActionResumeSession, SessionID: "bad"},
			setupMock: func(m *mockActionProvider) {
				m.resumeErr = errors.New("session not found")
			},
			wantSubstr: "couldn't resume",
		},
		{
			name: "stop error",
			cmd:  &ActionCommand{Action: ActionStopSession, SessionID: "bad"},
			setupMock: func(m *mockActionProvider) {
				m.stopErr = errors.New("process already exited")
			},
			wantSubstr: "couldn't stop",
		},
		{
			name: "launch error",
			cmd:  &ActionCommand{Action: ActionLaunchSession, Project: "/nope"},
			setupMock: func(m *mockActionProvider) {
				m.launchErr = errors.New("repo not found")
			},
			wantSubstr: "couldn't launch",
		},
		{
			name: "respond error",
			cmd:  &ActionCommand{Action: ActionRespondApproval, PID: 1, Response: "approve"},
			setupMock: func(m *mockActionProvider) {
				m.respondErr = errors.New("approval expired")
			},
			wantSubstr: "couldn't respond",
		},
		{
			name: "focus error",
			cmd:  &ActionCommand{Action: ActionFocusSession, PID: 1},
			setupMock: func(m *mockActionProvider) {
				m.focusErr = errors.New("window not found")
			},
			wantSubstr: "couldn't focus",
		},
		{
			name: "broadcast error",
			cmd:  &ActionCommand{Action: ActionBroadcast, Command: "halt"},
			setupMock: func(m *mockActionProvider) {
				m.broadcastErr = errors.New("no sessions running")
			},
			wantSubstr: "couldn't broadcast",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mock := &mockActionProvider{}
			tt.setupMock(mock)
			router := NewCommandRouter(mock)

			text, err := router.Execute(tt.cmd)

			// Provider errors are returned as friendly TTS text, not Go errors.
			if err != nil {
				t.Fatalf("unexpected Go error: %v", err)
			}
			if !strings.Contains(strings.ToLower(text), tt.wantSubstr) {
				t.Errorf("expected text containing %q, got %q", tt.wantSubstr, text)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// approve_all / deny_all
// ---------------------------------------------------------------------------

func TestExecute_ApproveAll(t *testing.T) {
	t.Parallel()

	t.Run("approves all pending", func(t *testing.T) {
		t.Parallel()

		mock := &mockActionProvider{
			pendingApprovals: []model.ApprovalRequest{
				{PID: 100, SessionName: "maya-web"},
				{PID: 200, SessionName: "maya-api"},
				{PID: 300, SessionName: "docs"},
			},
		}
		router := NewCommandRouter(mock)

		text, err := router.Execute(&ActionCommand{Action: ActionApproveAll})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(mock.respondPIDs) != 3 {
			t.Fatalf("expected 3 RespondToApproval calls, got %d", len(mock.respondPIDs))
		}
		// Verify each PID was called with "y".
		if mock.respondResponse != "y" {
			t.Errorf("expected response %q, got %q", "y", mock.respondResponse)
		}
		if !strings.Contains(text, "Approved 3") {
			t.Errorf("expected text containing 'Approved 3', got %q", text)
		}
	})

	t.Run("no pending approvals", func(t *testing.T) {
		t.Parallel()

		mock := &mockActionProvider{}
		router := NewCommandRouter(mock)

		text, err := router.Execute(&ActionCommand{Action: ActionApproveAll})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(text, "No pending") {
			t.Errorf("expected text containing 'No pending', got %q", text)
		}
		if mock.respondCalled {
			t.Error("RespondToApproval should not be called when there are no approvals")
		}
	})

	t.Run("fetch error", func(t *testing.T) {
		t.Parallel()

		mock := &mockActionProvider{
			pendingErr: errors.New("connection refused"),
		}
		router := NewCommandRouter(mock)

		text, err := router.Execute(&ActionCommand{Action: ActionApproveAll})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(strings.ToLower(text), "couldn't fetch") {
			t.Errorf("expected fetch-error text, got %q", text)
		}
	})
}

func TestExecute_DenyAll(t *testing.T) {
	t.Parallel()

	t.Run("denies all pending", func(t *testing.T) {
		t.Parallel()

		mock := &mockActionProvider{
			pendingApprovals: []model.ApprovalRequest{
				{PID: 100, SessionName: "maya-web"},
				{PID: 200, SessionName: "maya-api"},
			},
		}
		router := NewCommandRouter(mock)

		text, err := router.Execute(&ActionCommand{Action: ActionDenyAll})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(mock.respondPIDs) != 2 {
			t.Fatalf("expected 2 RespondToApproval calls, got %d", len(mock.respondPIDs))
		}
		if mock.respondResponse != "n" {
			t.Errorf("expected response %q, got %q", "n", mock.respondResponse)
		}
		if !strings.Contains(text, "Denied 2") {
			t.Errorf("expected text containing 'Denied 2', got %q", text)
		}
	})

	t.Run("no pending approvals", func(t *testing.T) {
		t.Parallel()

		mock := &mockActionProvider{}
		router := NewCommandRouter(mock)

		text, err := router.Execute(&ActionCommand{Action: ActionDenyAll})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(text, "No pending") {
			t.Errorf("expected text containing 'No pending', got %q", text)
		}
	})
}

// ---------------------------------------------------------------------------
// respond_approval with session name lookup
// ---------------------------------------------------------------------------

func TestExecute_RespondApproval_BySessionName(t *testing.T) {
	t.Parallel()

	t.Run("resolves PID from session name", func(t *testing.T) {
		t.Parallel()

		mock := &mockActionProvider{
			pendingApprovals: []model.ApprovalRequest{
				{PID: 111, SessionName: "maya-web"},
				{PID: 222, SessionName: "maya-api"},
			},
		}
		router := NewCommandRouter(mock)

		cmd := &ActionCommand{
			Action:    ActionRespondApproval,
			PID:       0,         // no PID
			SessionID: "maya-web", // session name instead
			Response:  "approve",
		}

		text, err := router.Execute(cmd)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !mock.respondCalled {
			t.Fatal("RespondToApproval was not called")
		}
		if mock.respondPID != 111 {
			t.Errorf("expected PID 111 (maya-web), got %d", mock.respondPID)
		}
		if !strings.Contains(text, "responded") {
			t.Errorf("expected text containing 'responded', got %q", text)
		}
	})

	t.Run("case-insensitive substring match", func(t *testing.T) {
		t.Parallel()

		mock := &mockActionProvider{
			pendingApprovals: []model.ApprovalRequest{
				{PID: 333, SessionName: "Maya-Web-Frontend"},
			},
		}
		router := NewCommandRouter(mock)

		cmd := &ActionCommand{
			Action:    ActionRespondApproval,
			PID:       0,
			SessionID: "maya-web",
			Response:  "approve",
		}

		text, err := router.Execute(cmd)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if mock.respondPID != 333 {
			t.Errorf("expected PID 333 (case-insensitive match), got %d", mock.respondPID)
		}
		if !strings.Contains(text, "responded") {
			t.Errorf("expected text containing 'responded', got %q", text)
		}
	})

	t.Run("unknown session name", func(t *testing.T) {
		t.Parallel()

		mock := &mockActionProvider{
			pendingApprovals: []model.ApprovalRequest{
				{PID: 111, SessionName: "maya-web"},
			},
		}
		router := NewCommandRouter(mock)

		cmd := &ActionCommand{
			Action:    ActionRespondApproval,
			PID:       0,
			SessionID: "does-not-exist",
			Response:  "approve",
		}

		text, err := router.Execute(cmd)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if mock.respondCalled {
			t.Error("RespondToApproval should not be called for unknown session")
		}
		if !strings.Contains(text, "No pending approval") {
			t.Errorf("expected text containing 'No pending approval', got %q", text)
		}
	})

	t.Run("lookup error", func(t *testing.T) {
		t.Parallel()

		mock := &mockActionProvider{
			pendingErr: errors.New("db down"),
		}
		router := NewCommandRouter(mock)

		cmd := &ActionCommand{
			Action:    ActionRespondApproval,
			PID:       0,
			SessionID: "maya-web",
			Response:  "approve",
		}

		text, err := router.Execute(cmd)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(strings.ToLower(text), "couldn't look up") {
			t.Errorf("expected lookup-error text, got %q", text)
		}
	})

	t.Run("PID provided directly still works", func(t *testing.T) {
		t.Parallel()

		mock := &mockActionProvider{}
		router := NewCommandRouter(mock)

		cmd := &ActionCommand{
			Action:   ActionRespondApproval,
			PID:      12345,
			Response: "approve",
		}

		_, err := router.Execute(cmd)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !mock.respondCalled {
			t.Error("RespondToApproval was not called")
		}
		if mock.respondPID != 12345 {
			t.Errorf("expected PID 12345, got %d", mock.respondPID)
		}
	})
}

// ---------------------------------------------------------------------------
// Git operations (TASK-016)
// ---------------------------------------------------------------------------

func TestExecute_GitCommit(t *testing.T) {
	t.Parallel()

	mock := &mockActionProvider{}
	router := NewCommandRouter(mock)

	cmd := &ActionCommand{
		Action:  ActionGitCommit,
		Project: "maya-web",
		Command: "feat: add login page",
	}

	text, err := router.Execute(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// git_commit stages first, then commits.
	if !mock.gitStageCalled {
		t.Error("GitStageAll was not called (git_commit should stage first)")
	}
	if mock.gitStageRepoPath != "/Users/test/projects/maya-web" {
		t.Errorf("GitStageAll repoPath = %q, want %q", mock.gitStageRepoPath, "/Users/test/projects/maya-web")
	}
	if !mock.gitCommitCalled {
		t.Error("GitCommit was not called")
	}
	if mock.gitCommitRepoPath != "/Users/test/projects/maya-web" {
		t.Errorf("GitCommit repoPath = %q, want %q", mock.gitCommitRepoPath, "/Users/test/projects/maya-web")
	}
	if mock.gitCommitMessage != "feat: add login page" {
		t.Errorf("GitCommit message = %q, want %q", mock.gitCommitMessage, "feat: add login page")
	}
	if !strings.Contains(text, "Committed to maya-web") {
		t.Errorf("expected text containing 'Committed to maya-web', got %q", text)
	}
}

func TestExecute_GitPush(t *testing.T) {
	t.Parallel()

	mock := &mockActionProvider{}
	router := NewCommandRouter(mock)

	cmd := &ActionCommand{
		Action:  ActionGitPush,
		Project: "auth-service",
	}

	text, err := router.Execute(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !mock.gitPushCalled {
		t.Error("GitPush was not called")
	}
	if mock.gitPushRepoPath != "/Users/test/projects/auth-service" {
		t.Errorf("GitPush repoPath = %q, want %q", mock.gitPushRepoPath, "/Users/test/projects/auth-service")
	}
	if !strings.Contains(text, "Pushed auth-service") {
		t.Errorf("expected text containing 'Pushed auth-service', got %q", text)
	}
}

func TestExecute_GitStage(t *testing.T) {
	t.Parallel()

	mock := &mockActionProvider{}
	router := NewCommandRouter(mock)

	cmd := &ActionCommand{
		Action:  ActionGitStage,
		Project: "maya-web",
	}

	text, err := router.Execute(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !mock.gitStageCalled {
		t.Error("GitStageAll was not called")
	}
	if mock.gitStageRepoPath != "/Users/test/projects/maya-web" {
		t.Errorf("GitStageAll repoPath = %q, want %q", mock.gitStageRepoPath, "/Users/test/projects/maya-web")
	}
	if !strings.Contains(text, "Staged all changes in maya-web") {
		t.Errorf("expected text containing 'Staged all changes in maya-web', got %q", text)
	}
}

func TestExecute_GitUnknownProject(t *testing.T) {
	t.Parallel()

	mock := &mockActionProvider{}
	router := NewCommandRouter(mock)

	cmd := &ActionCommand{
		Action:  ActionGitCommit,
		Project: "nonexistent-project",
		Command: "some commit message",
	}

	text, err := router.Execute(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should NOT have called any git methods.
	if mock.gitStageCalled || mock.gitCommitCalled || mock.gitPushCalled {
		t.Error("git methods should not be called for unknown project")
	}
	if !strings.Contains(text, "nonexistent-project") {
		t.Errorf("expected error text mentioning the project name, got %q", text)
	}
}

// ---------------------------------------------------------------------------
// Navigate view (TASK-016)
// ---------------------------------------------------------------------------

func TestExecute_NavigateView(t *testing.T) {
	t.Parallel()

	mock := &mockActionProvider{}
	router := NewCommandRouter(mock)

	cmd := &ActionCommand{
		Action:  ActionNavigateView,
		Command: "sessions panel",
	}

	text, err := router.Execute(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(text, "sessions panel") {
		t.Errorf("expected text containing 'sessions panel', got %q", text)
	}
	if !strings.Contains(text, "Showing you") {
		t.Errorf("expected text containing 'Showing you', got %q", text)
	}
}

// ---------------------------------------------------------------------------
// Git error scenarios (TASK-016)
// ---------------------------------------------------------------------------

func TestExecute_GitCommit_StageError(t *testing.T) {
	t.Parallel()

	mock := &mockActionProvider{
		gitStageErr: errors.New("nothing to stage"),
	}
	router := NewCommandRouter(mock)

	cmd := &ActionCommand{
		Action:  ActionGitCommit,
		Project: "maya-web",
		Command: "wip",
	}

	text, err := router.Execute(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(strings.ToLower(text), "couldn't stage") {
		t.Errorf("expected stage-error text, got %q", text)
	}
	// Commit should NOT be called if staging failed.
	if mock.gitCommitCalled {
		t.Error("GitCommit should not be called when staging fails")
	}
}

func TestExecute_GitCommit_CommitError(t *testing.T) {
	t.Parallel()

	mock := &mockActionProvider{
		gitCommitErr: errors.New("nothing to commit"),
	}
	router := NewCommandRouter(mock)

	cmd := &ActionCommand{
		Action:  ActionGitCommit,
		Project: "maya-web",
		Command: "wip",
	}

	text, err := router.Execute(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(strings.ToLower(text), "couldn't commit") {
		t.Errorf("expected commit-error text, got %q", text)
	}
}

func TestExecute_GitPush_Error(t *testing.T) {
	t.Parallel()

	mock := &mockActionProvider{
		gitPushErr: errors.New("remote rejected"),
	}
	router := NewCommandRouter(mock)

	cmd := &ActionCommand{
		Action:  ActionGitPush,
		Project: "maya-web",
	}

	text, err := router.Execute(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(strings.ToLower(text), "couldn't push") {
		t.Errorf("expected push-error text, got %q", text)
	}
}

func TestExecute_GitCommit_DefaultMessage(t *testing.T) {
	t.Parallel()

	mock := &mockActionProvider{}
	router := NewCommandRouter(mock)

	cmd := &ActionCommand{
		Action:  ActionGitCommit,
		Project: "maya-web",
		Command: "", // empty — should default to "Update from Jarvis"
	}

	text, err := router.Execute(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mock.gitCommitMessage != "Update from Jarvis" {
		t.Errorf("expected default message %q, got %q", "Update from Jarvis", mock.gitCommitMessage)
	}
	if !strings.Contains(text, "Update from Jarvis") {
		t.Errorf("expected text containing default message, got %q", text)
	}
}

func TestExecute_GitStage_EmptyProject(t *testing.T) {
	t.Parallel()

	mock := &mockActionProvider{}
	router := NewCommandRouter(mock)

	cmd := &ActionCommand{
		Action:  ActionGitStage,
		Project: "", // empty project name
	}

	text, err := router.Execute(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mock.gitStageCalled {
		t.Error("GitStageAll should not be called when project is empty")
	}
	if !strings.Contains(text, "need a project name") {
		t.Errorf("expected text about needing project name, got %q", text)
	}
}

// ---------------------------------------------------------------------------
// CMux / Terminal / System operations (TASK-014)
// ---------------------------------------------------------------------------

func TestExecute_CMuxFocus_Unavailable(t *testing.T) {
	t.Parallel()

	mock := &mockActionProvider{
		cmuxAvailable: false, // CMux not running
	}
	router := NewCommandRouter(mock)

	cmd := &ActionCommand{
		Action:  ActionCMuxFocus,
		Project: "maya-web",
	}

	text, err := router.Execute(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(text, "CMux isn't running") {
		t.Errorf("expected text containing 'CMux isn't running', got %q", text)
	}
	if len(mock.focusCMuxSurfaceCalls) != 0 {
		t.Error("FocusCMuxSurface should not be called when CMux is unavailable")
	}
}

func TestExecute_CMuxSend_ValidProject(t *testing.T) {
	t.Parallel()

	mock := &mockActionProvider{
		cmuxAvailable: true,
		activeSessions: []model.Session{
			{PID: 42, RepoPath: "/Users/test/projects/maya-web"},
		},
	}
	router := NewCommandRouter(mock)

	cmd := &ActionCommand{
		Action:  ActionCMuxSend,
		Project: "maya-web",
		Command: "npm test",
	}

	text, err := router.Execute(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mock.sendToCMuxCalls) != 1 {
		t.Fatalf("expected 1 SendToCMux call, got %d", len(mock.sendToCMuxCalls))
	}
	if mock.sendToCMuxCalls[0] != "42:npm test" {
		t.Errorf("SendToCMux called with %q, want %q", mock.sendToCMuxCalls[0], "42:npm test")
	}
	if !strings.Contains(text, "Sent to maya-web") {
		t.Errorf("expected text containing 'Sent to maya-web', got %q", text)
	}
}

func TestExecute_SystemFocusApp(t *testing.T) {
	t.Parallel()

	mock := &mockActionProvider{}
	router := NewCommandRouter(mock)

	cmd := &ActionCommand{
		Action:  ActionSystemFocusApp,
		Command: "Slack",
	}

	text, err := router.Execute(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mock.openAppCalls) != 1 {
		t.Fatalf("expected 1 OpenApp call, got %d", len(mock.openAppCalls))
	}
	if mock.openAppCalls[0] != "Slack" {
		t.Errorf("OpenApp called with %q, want %q", mock.openAppCalls[0], "Slack")
	}
	if !strings.Contains(text, "Opening Slack") {
		t.Errorf("expected text containing 'Opening Slack', got %q", text)
	}
}

func TestExecute_SystemOpenURL(t *testing.T) {
	t.Parallel()

	mock := &mockActionProvider{}
	router := NewCommandRouter(mock)

	cmd := &ActionCommand{
		Action:  ActionSystemOpenURL,
		Command: "https://github.com/example/repo",
	}

	text, err := router.Execute(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mock.openURLCalls) != 1 {
		t.Fatalf("expected 1 OpenURL call, got %d", len(mock.openURLCalls))
	}
	if mock.openURLCalls[0] != "https://github.com/example/repo" {
		t.Errorf("OpenURL called with %q, want %q", mock.openURLCalls[0], "https://github.com/example/repo")
	}
	if !strings.Contains(text, "Opening the link") {
		t.Errorf("expected text containing 'Opening the link', got %q", text)
	}
}

func TestExecute_TerminalFocus_EmptySessionID(t *testing.T) {
	t.Parallel()

	mock := &mockActionProvider{}
	router := NewCommandRouter(mock)

	cmd := &ActionCommand{
		Action:    ActionTerminalFocus,
		SessionID: "", // empty — should return error message
	}

	text, err := router.Execute(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(text, "need a terminal window ID") {
		t.Errorf("expected text about needing terminal window ID, got %q", text)
	}
	if len(mock.focusTerminalCalls) != 0 {
		t.Error("FocusTerminalWindow should not be called with empty session ID")
	}
}

func TestExecute_CMuxFocus_UnknownProject(t *testing.T) {
	t.Parallel()

	mock := &mockActionProvider{
		cmuxAvailable: true,
		activeSessions: []model.Session{
			{PID: 42, RepoPath: "/Users/test/projects/maya-web"},
		},
	}
	router := NewCommandRouter(mock)

	cmd := &ActionCommand{
		Action:  ActionCMuxFocus,
		Project: "nonexistent-repo",
	}

	text, err := router.Execute(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(text, "couldn't find") {
		t.Errorf("expected text containing 'couldn't find', got %q", text)
	}
	if len(mock.focusCMuxSurfaceCalls) != 0 {
		t.Error("FocusCMuxSurface should not be called for unknown project")
	}
}

func TestExecute_SystemOpenApp_ViaOpenAppAction(t *testing.T) {
	t.Parallel()

	mock := &mockActionProvider{}
	router := NewCommandRouter(mock)

	// system_open_app is an alias for system_focus_app.
	cmd := &ActionCommand{
		Action:  ActionSystemOpenApp,
		Command: "Terminal",
	}

	text, err := router.Execute(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mock.openAppCalls) != 1 {
		t.Fatalf("expected 1 OpenApp call, got %d", len(mock.openAppCalls))
	}
	if mock.openAppCalls[0] != "Terminal" {
		t.Errorf("OpenApp called with %q, want %q", mock.openAppCalls[0], "Terminal")
	}
	if !strings.Contains(text, "Opening Terminal") {
		t.Errorf("expected text containing 'Opening Terminal', got %q", text)
	}
}

func TestExecute_CMuxSend_Unavailable(t *testing.T) {
	t.Parallel()

	mock := &mockActionProvider{
		cmuxAvailable: false,
	}
	router := NewCommandRouter(mock)

	cmd := &ActionCommand{
		Action:  ActionCMuxSend,
		Project: "maya-web",
		Command: "echo hi",
	}

	text, err := router.Execute(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(text, "CMux isn't running") {
		t.Errorf("expected text containing 'CMux isn't running', got %q", text)
	}
	if len(mock.sendToCMuxCalls) != 0 {
		t.Error("SendToCMux should not be called when CMux is unavailable")
	}
}
