package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/namanchopra/jarvis/internal/proc"
)

// Session represents a Claude Code session read from ~/.claude/sessions/<pid>.json.
type Session struct {
	PID        int    `json:"pid"`
	SessionID  string `json:"sessionId"`
	CWD        string `json:"cwd"`
	StartedAt  int64  `json:"startedAt"` // unix millis
	Kind       string `json:"kind"`      // "interactive"
	Entrypoint string `json:"entrypoint"`
	Name       string `json:"name"`
}

// StartedAtTime returns the StartedAt field as a time.Time.
func (s Session) StartedAtTime() time.Time {
	return time.UnixMilli(s.StartedAt)
}

// RepoBasename returns the last path segment of CWD.
func (s Session) RepoBasename() string {
	return filepath.Base(s.CWD)
}

// DisplayName returns the session name if set, otherwise "claude-code: <repo>".
func (s Session) DisplayName() string {
	if s.Name != "" {
		return s.Name
	}
	return "claude-code: " + s.RepoBasename()
}

// SessionsDir returns the Claude Code sessions directory.
func SessionsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "sessions")
}

// GetActiveSessions reads all session files and returns only those with live processes.
func GetActiveSessions() ([]Session, error) {
	dir := SessionsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read sessions dir: %w", err)
	}

	var sessions []Session
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		// Extract PID from filename.
		pidStr := strings.TrimSuffix(entry.Name(), ".json")
		pid, err := strconv.Atoi(pidStr)
		if err != nil {
			continue
		}

		// Check if process is alive.
		if !proc.IsAlive(pid) {
			continue
		}

		// Read and parse session file.
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}

		var sess Session
		if err := json.Unmarshal(data, &sess); err != nil {
			continue
		}

		sessions = append(sessions, sess)
	}

	return sessions, nil
}

// GetSession reads a specific session file by PID.
func GetSession(pid int) (Session, error) {
	path := filepath.Join(SessionsDir(), fmt.Sprintf("%d.json", pid))
	data, err := os.ReadFile(path)
	if err != nil {
		return Session{}, fmt.Errorf("read session %d: %w", pid, err)
	}

	var sess Session
	if err := json.Unmarshal(data, &sess); err != nil {
		return Session{}, fmt.Errorf("parse session %d: %w", pid, err)
	}
	return sess, nil
}

// ---------------------------------------------------------------------------
// Session indicators
// ---------------------------------------------------------------------------

// SessionIndicator extends a Session with heuristic state detection derived
// from the session file's modification time and process liveness.
type SessionIndicator struct {
	PID          int    `json:"pid"`
	SessionID    string `json:"sessionId"`
	CWD          string `json:"cwd"`
	Name         string `json:"name"`
	StartedAt    int64  `json:"startedAt"`
	HasQuestion  bool   `json:"hasQuestion"`  // heuristic: idle > 2min with live process
	LastActivity string `json:"lastActivity"` // "typing", "idle", "waiting", "tool_use"
	TokensUsed   int    `json:"tokensUsed"`   // reserved for future use; 0 if unavailable
}

// GetSessionIndicators reads all active Claude Code sessions and enriches
// them with heuristic state indicators based on the session file's
// modification time:
//   - Modified < 5s ago   => "typing" (agent is actively working)
//   - Modified 5-30s ago  => "tool_use" (likely executing a tool)
//   - Modified 30s-2m ago => "idle" (may be between operations)
//   - Modified > 2m ago   => "waiting" (probably waiting for user input)
//   - Process dead         => skipped
//
// HasQuestion is set to true when the session appears to be waiting
// (idle > 2 minutes with the process still alive). The previous 30-second
// threshold caused false positives when the agent was compiling, running
// tests, or thinking between operations.
func GetSessionIndicators() ([]SessionIndicator, error) {
	dir := SessionsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []SessionIndicator{}, nil
		}
		return nil, fmt.Errorf("read sessions dir: %w", err)
	}

	now := time.Now()
	var indicators []SessionIndicator

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		// Extract PID from filename.
		pidStr := strings.TrimSuffix(entry.Name(), ".json")
		pid, err := strconv.Atoi(pidStr)
		if err != nil {
			continue
		}

		// Only include sessions with live processes.
		if !proc.IsAlive(pid) {
			continue
		}

		filePath := filepath.Join(dir, entry.Name())

		// Read and parse the session file.
		data, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}

		var sess Session
		if err := json.Unmarshal(data, &sess); err != nil {
			continue
		}

		// Get the file's modification time for activity heuristics.
		fileInfo, err := os.Stat(filePath)
		if err != nil {
			continue
		}

		sinceModified := now.Sub(fileInfo.ModTime())
		activity := classifyActivity(sinceModified)

		indicators = append(indicators, SessionIndicator{
			PID:          pid,
			SessionID:    sess.SessionID,
			CWD:          sess.CWD,
			Name:         sess.DisplayName(),
			StartedAt:    sess.StartedAt,
			HasQuestion:  sinceModified > 2*time.Minute,
			LastActivity: activity,
			TokensUsed:   0, // not available from session file; reserved
		})
	}

	if indicators == nil {
		indicators = []SessionIndicator{}
	}

	return indicators, nil
}

// classifyActivity maps the time since the session file was last modified to
// a human-readable activity label.
func classifyActivity(sinceModified time.Duration) string {
	switch {
	case sinceModified < 5*time.Second:
		return "typing"
	case sinceModified < 30*time.Second:
		return "tool_use"
	case sinceModified < 2*time.Minute:
		return "idle"
	default:
		return "waiting"
	}
}

// ---------------------------------------------------------------------------
// Approval prompt detection
// ---------------------------------------------------------------------------

// approvalPatterns are substrings that appear in Claude Code's interactive
// approval prompts (tool-use confirmations, yes/no dialogs, etc.). A match
// against any of these is a positive signal that the session is blocked on
// user approval.
var approvalPatterns = []string{
	"Allow",
	"Deny",
	"allow this action",
	"Do you want to",
	"Would you like to",
	"(y/n)",
	"(Y/n)",
	"yes/no",
	"Yes/No",
	"approve",
	"permit",
	"grant access",
	"tool use",
	"execute command",
	"run this",
}

// idlePromptPatterns are substrings that indicate the terminal is showing
// Claude Code's normal idle prompt or an actively-working indicator. A match
// against any of these is a strong negative signal — the session is NOT
// blocked on approval even if it appears idle.
var idlePromptPatterns = []string{
	"? for shortcuts",
	"? for help",
	"esc to interrupt",
	"waiting for",
	"Watching for file changes",
	"press enter to continue",
	"Type a message",
}

// IsLikelyApprovalPrompt examines the tail of terminal output and returns
// true only when the output looks like a real approval prompt. It requires
// at least one line to match an approval pattern AND no lines to match a
// known idle/working pattern. This prevents false positives from sessions
// that are simply idle at the input prompt or actively processing.
//
// terminalOutput should be the raw terminal text; the function extracts the
// last 5 non-empty lines for analysis.
func IsLikelyApprovalPrompt(terminalOutput string) bool {
	tail := lastNonEmptyLines(terminalOutput, 5)
	if len(tail) == 0 {
		return false
	}

	joined := strings.Join(tail, "\n")
	lower := strings.ToLower(joined)

	// Check for idle/working patterns first — if any match, it is NOT an
	// approval prompt regardless of other content.
	for _, pat := range idlePromptPatterns {
		if strings.Contains(lower, strings.ToLower(pat)) {
			return false
		}
	}

	// Check for at least one approval pattern.
	for _, pat := range approvalPatterns {
		if strings.Contains(lower, strings.ToLower(pat)) {
			return true
		}
	}

	return false
}

// lastNonEmptyLines returns up to n non-empty (trimmed) lines from the end
// of the given text.
func lastNonEmptyLines(text string, n int) []string {
	raw := strings.Split(text, "\n")
	var lines []string
	for i := len(raw) - 1; i >= 0 && len(lines) < n; i-- {
		trimmed := strings.TrimSpace(raw[i])
		if trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	// Reverse so they appear in chronological order.
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}
	return lines
}

