package agent

import (
	"context"
	"os/exec"
	"time"

	"github.com/namanchopra/jarvis/internal/model"
)

// AgentAdapter defines the contract for interacting with an AI coding tool.
type AgentAdapter interface {
	// Name returns the agent type this adapter handles.
	Name() model.AgentType

	// Launch starts a new agent session in the given repo with the given prompt.
	Launch(ctx context.Context, opts LaunchOptions) (*RunningSession, error)

	// SendMessage sends a follow-up message to a running session.
	// For tools that don't support stdin (e.g., Claude Code), this may
	// spawn a new process that resumes the session.
	SendMessage(ctx context.Context, session *RunningSession, message string) error

	// Stop gracefully terminates a running session.
	Stop(ctx context.Context, session *RunningSession) error

	// IsAvailable checks if the agent's CLI tool is installed and accessible.
	IsAvailable() bool
}

// LaunchOptions configures how an agent session is launched.
type LaunchOptions struct {
	RepoPath  string   // working directory
	Prompt    string   // initial prompt
	SessionID string   // agent-specific session ID for resume (empty = new session)
	ExtraArgs []string // additional CLI flags
	Env       []string // additional environment variables (KEY=VALUE format)
}

// RunningSession represents a currently active agent process.
type RunningSession struct {
	PID       int              // OS process ID
	SessionID string           // agent-specific session ID (for resume later)
	Output    <-chan OutputLine // stream of output lines from the agent
	SendInput func(msg string) error // write to process stdin (nil if not supported)
	Done      <-chan error      // closed when process exits; sends exit error or nil
	Cmd       *exec.Cmd        // underlying process (for cleanup)
	cancel    context.CancelFunc // to stop the session
}

// Cancel stops the running session's context.
func (rs *RunningSession) Cancel() {
	if rs.cancel != nil {
		rs.cancel()
	}
}

// OutputLine represents a single line of output from an agent.
type OutputLine struct {
	Text      string    `json:"text"`
	Timestamp time.Time `json:"timestamp"`
	IsError   bool      `json:"isError"`  // true if from stderr
	IsSystem  bool      `json:"isSystem"` // true if from Jarvis (not the agent)
}

// AgentInfo describes an available agent adapter.
type AgentInfo struct {
	AgentType model.AgentType `json:"agentType"`
	Name      string          `json:"name"`      // human-readable name
	Available bool            `json:"available"`  // whether CLI is installed
	Version   string          `json:"version"`    // CLI version if available
}
