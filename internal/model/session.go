package model

import (
	"time"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// SessionStatus
// ---------------------------------------------------------------------------

// SessionStatus represents the lifecycle state of a managed session.
type SessionStatus string

const (
	SessionQueued     SessionStatus = "queued"      // waiting for dependencies to complete
	SessionLaunching  SessionStatus = "launching"
	SessionRunning    SessionStatus = "running"
	SessionPaused     SessionStatus = "paused"
	SessionCompleted  SessionStatus = "completed"
	SessionFailed     SessionStatus = "failed"
	SessionNeedsInput SessionStatus = "needs-input"
)

// allSessionStatuses is the canonical list, kept in one place to avoid duplication.
var allSessionStatuses = []SessionStatus{
	SessionQueued,
	SessionLaunching,
	SessionRunning,
	SessionPaused,
	SessionCompleted,
	SessionFailed,
	SessionNeedsInput,
}

// ValidSessionStatus reports whether s is a recognised session status.
func ValidSessionStatus(s SessionStatus) bool {
	for _, v := range allSessionStatuses {
		if v == s {
			return true
		}
	}
	return false
}

// AllSessionStatuses returns every defined SessionStatus value.
func AllSessionStatuses() []SessionStatus {
	out := make([]SessionStatus, len(allSessionStatuses))
	copy(out, allSessionStatuses)
	return out
}

// String implements fmt.Stringer.
func (s SessionStatus) String() string { return string(s) }

// IsTerminal returns true if the session is in a final state.
func (s SessionStatus) IsTerminal() bool {
	return s == SessionCompleted || s == SessionFailed
}

// ---------------------------------------------------------------------------
// Session
// ---------------------------------------------------------------------------

// Session represents a managed AI agent session.
type Session struct {
	ID              string        `json:"id"`
	TaskID          string        `json:"taskId"`          // optional link to a task
	AgentType       AgentType     `json:"agentType"`
	RepoPath        string        `json:"repoPath"`
	Prompt          string        `json:"prompt"`
	AgentSessionID  string        `json:"agentSessionId"`  // tool-specific session ID for resume
	Status          SessionStatus `json:"status"`
	PID             int           `json:"pid"`
	OutputPath      string        `json:"outputPath"`      // log file path
	ExitCode        int           `json:"exitCode"`
	ErrorMessage    string        `json:"errorMessage"`
	ParentSessionID string        `json:"parentSessionId"` // optional — links to forked-from session
	DependsOn       []string      `json:"dependsOn"`       // session IDs that must complete first (stored as JSON)
	Phase           string        `json:"phase"`            // "plan", "build", "review", "test", or ""
	CreatedAt       time.Time     `json:"createdAt"`
	UpdatedAt       time.Time     `json:"updatedAt"`
}

// NewSession constructs a Session with a generated UUID and timestamps
// initialised to the current time. Status is set to launching.
func NewSession(agentType AgentType, repoPath, prompt string) Session {
	now := time.Now()
	return Session{
		ID:        uuid.New().String(),
		AgentType: agentType,
		RepoPath:  repoPath,
		Prompt:    prompt,
		Status:    SessionLaunching,
		DependsOn: []string{},
		CreatedAt: now,
		UpdatedAt: now,
	}
}
