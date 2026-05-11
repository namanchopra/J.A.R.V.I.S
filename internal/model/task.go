package model

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Status
// ---------------------------------------------------------------------------

// Status represents the lifecycle state of a task.
type Status string

const (
	StatusPending    Status = "pending"
	StatusRunning    Status = "running"
	StatusDone       Status = "done"
	StatusFailed     Status = "failed"
	StatusNeedsInput Status = "needs-input"
)

// allStatuses is the canonical list, kept in one place to avoid duplication.
var allStatuses = []Status{
	StatusPending,
	StatusRunning,
	StatusDone,
	StatusFailed,
	StatusNeedsInput,
}

// AllStatuses returns every defined Status value.
func AllStatuses() []Status {
	out := make([]Status, len(allStatuses))
	copy(out, allStatuses)
	return out
}

// ValidStatus reports whether s is a recognised status.
func ValidStatus(s Status) bool {
	for _, v := range allStatuses {
		if v == s {
			return true
		}
	}
	return false
}

// String implements fmt.Stringer.
func (s Status) String() string { return string(s) }

// ---------------------------------------------------------------------------
// AgentType
// ---------------------------------------------------------------------------

// AgentType identifies the AI coding agent that executes a task.
type AgentType string

const (
	AgentClaudeCode AgentType = "claude-code"
	AgentKiro       AgentType = "kiro"
	AgentGemini     AgentType = "gemini"
	AgentCodex      AgentType = "codex"
	AgentAider      AgentType = "aider"
	AgentOther      AgentType = "other"
)

// allAgentTypes is the canonical list, kept in one place to avoid duplication.
var allAgentTypes = []AgentType{
	AgentClaudeCode,
	AgentKiro,
	AgentGemini,
	AgentCodex,
	AgentAider,
	AgentOther,
}

// AllAgentTypes returns every defined AgentType value.
func AllAgentTypes() []AgentType {
	out := make([]AgentType, len(allAgentTypes))
	copy(out, allAgentTypes)
	return out
}

// ValidAgentType reports whether a is a recognised agent type.
func ValidAgentType(a AgentType) bool {
	for _, v := range allAgentTypes {
		if v == a {
			return true
		}
	}
	return false
}

// String implements fmt.Stringer.
func (a AgentType) String() string { return string(a) }

// ---------------------------------------------------------------------------
// Task
// ---------------------------------------------------------------------------

// Task is the core domain object representing a unit of work delegated to an
// AI coding agent.
type Task struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	RepoPath    string    `json:"repoPath"`
	AgentType   AgentType `json:"agentType"`
	Status      Status    `json:"status"`
	OutputPath  string    `json:"outputPath"`  // optional — path to a log file
	WorkflowID  string    `json:"workflowId"`  // optional — links task to a workflow
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// ---------------------------------------------------------------------------
// Workflow
// ---------------------------------------------------------------------------

// Workflow groups related tasks into a logical unit of work.
type Workflow struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// NewWorkflow constructs a Workflow with a generated UUID and timestamps
// initialised to the current time.
func NewWorkflow(name, description string) Workflow {
	now := time.Now()
	return Workflow{
		ID:          uuid.New().String(),
		Name:        name,
		Description: description,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

// ---------------------------------------------------------------------------
// DashboardStats
// ---------------------------------------------------------------------------

// DashboardStats holds aggregate counts of tasks grouped by status.
type DashboardStats struct {
	Total      int `json:"total"`
	Running    int `json:"running"`
	Pending    int `json:"pending"`
	Done       int `json:"done"`
	Failed     int `json:"failed"`
	NeedsInput int `json:"needsInput"`
}

// ---------------------------------------------------------------------------
// ActivityEvent
// ---------------------------------------------------------------------------

// ActivityEvent records a significant lifecycle event for a task (or the
// system). Events power the activity feed and are stored chronologically.
type ActivityEvent struct {
	ID        string    `json:"id"`
	TaskID    string    `json:"taskId"`
	TaskName  string    `json:"taskName"`
	EventType string    `json:"eventType"` // "created", "status_changed", "output_attached", "workflow_assigned", "auto_detected", "completed", "failed", "needs_input", "deleted"
	Message   string    `json:"message"`   // human-readable description
	Metadata  string    `json:"metadata"`  // JSON string for extra data (e.g., {"from": "running", "to": "done"})
	CreatedAt time.Time `json:"createdAt"`
}

// NewActivityEvent constructs an ActivityEvent with a generated UUID and
// CreatedAt set to the current time.
func NewActivityEvent(taskID, taskName, eventType, message, metadata string) ActivityEvent {
	return ActivityEvent{
		ID:        uuid.New().String(),
		TaskID:    taskID,
		TaskName:  taskName,
		EventType: eventType,
		Message:   message,
		Metadata:  metadata,
		CreatedAt: time.Now(),
	}
}

// NewTask constructs a Task with a generated UUID, status set to pending, and
// timestamps initialised to the current time.
func NewTask(name, description, repoPath string, agentType AgentType) Task {
	now := time.Now()
	return Task{
		ID:          uuid.New().String(),
		Name:        name,
		Description: description,
		RepoPath:    repoPath,
		AgentType:   agentType,
		Status:      StatusPending,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

// ---------------------------------------------------------------------------
// Status transition validation
// ---------------------------------------------------------------------------

// ErrInvalidStatusTransition is returned when a status change violates the
// lifecycle rules.
var ErrInvalidStatusTransition = errors.New("invalid status transition")

// ValidateStatusTransition checks whether moving from one status to another is
// allowed. The rules are:
//
//   - "done" cannot transition back to "pending" or "running".
//   - "failed" may transition to "pending" or "running" (retry is permitted).
//   - All other transitions are allowed.
func ValidateStatusTransition(from, to Status) error {
	if from == StatusDone && (to == StatusPending || to == StatusRunning) {
		return fmt.Errorf("%w: cannot move from %q to %q", ErrInvalidStatusTransition, from, to)
	}
	return nil
}
