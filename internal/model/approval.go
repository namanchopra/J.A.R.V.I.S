package model

import (
	"time"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// ApprovalRequest
// ---------------------------------------------------------------------------

// ApprovalRequest represents a detected approval/permission prompt in an
// active Claude Code session. The agent appears to be blocked waiting for
// user input (e.g. tool-use confirmation, yes/no question, or interactive
// dialog).
type ApprovalRequest struct {
	PID         int       `json:"pid"`
	SessionName string    `json:"sessionName"`
	CWD         string    `json:"cwd"`
	PromptText  string    `json:"promptText"` // last 5 non-empty lines of terminal output
	DetectedAt  time.Time `json:"detectedAt"`
}

// ---------------------------------------------------------------------------
// ApprovalRule
// ---------------------------------------------------------------------------

// ApprovalRule defines an automatic approval or denial rule for tool-use
// prompts. When a prompt matches the Pattern regex, the configured Action is
// taken automatically instead of requiring manual user input.
type ApprovalRule struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Pattern     string    `json:"pattern"`     // regex pattern to match against prompt text
	Action      string    `json:"action"`      // "approve" or "deny"
	Scope       string    `json:"scope"`       // "all" or "project"
	ProjectPath string    `json:"projectPath"` // only used when scope="project"
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"createdAt"`
}

// NewApprovalRule constructs an ApprovalRule with a generated UUID and
// CreatedAt set to the current time.
func NewApprovalRule(name, pattern, action, scope, projectPath string) ApprovalRule {
	return ApprovalRule{
		ID:          uuid.New().String(),
		Name:        name,
		Pattern:     pattern,
		Action:      action,
		Scope:       scope,
		ProjectPath: projectPath,
		Enabled:     true,
		CreatedAt:   time.Now(),
	}
}

// ValidApprovalAction reports whether a is a recognised approval action.
func ValidApprovalAction(a string) bool {
	return a == "approve" || a == "deny"
}

// ValidApprovalScope reports whether s is a recognised approval scope.
func ValidApprovalScope(s string) bool {
	return s == "all" || s == "project"
}
