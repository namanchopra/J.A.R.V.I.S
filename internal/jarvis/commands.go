package jarvis

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/namanchopra/jarvis/internal/model"
)

// Action constants for bulk approval operations. Kept here (not in
// personality.go) to avoid conflicts with other in-flight tasks.
const (
	ActionApproveAll = "approve_all"
	ActionDenyAll    = "deny_all"
)

// ActionProvider defines the execution methods that the command router
// dispatches to. The main App struct satisfies this interface, allowing
// voice commands parsed from Claude's [ACTION] blocks to be executed
// against Jarvis's session management layer.
type ActionProvider interface {
	ResumeSession(sessionID string) (model.Session, error)
	StopSession(sessionID string) error
	LaunchSession(agentType, repoPath, prompt string) (model.Session, error)
	RespondToApproval(pid int, response string) error
	FocusSession(pid int) error
	BroadcastToAll(command string) (map[int]string, error)
	GetPendingApprovals() ([]model.ApprovalRequest, error)
	GetActiveSessions() ([]model.Session, error)
	GitStageAll(repoPath string) error
	GitCommit(repoPath, message string) error
	GitPush(repoPath string) error

	// CMux terminal control
	SendToCMux(surfaceRef string, text string) error
	ReadFromCMux(surfaceRef string) (string, error)
	FocusCMuxSurface(surfaceRef string) error

	// Terminal window control
	SendToTerminal(windowID string, text string) error
	FocusTerminalWindow(windowID string) error

	// System
	IsCMuxAvailable() bool
	OpenApp(appName string) error
	OpenURL(url string) error
}

// CommandRouter takes parsed ActionCommands and dispatches them to the
// appropriate ActionProvider method. It returns human-readable confirmation
// text suitable for TTS output.
type CommandRouter struct {
	provider ActionProvider
}

// NewCommandRouter creates a CommandRouter wired to the given provider.
func NewCommandRouter(provider ActionProvider) *CommandRouter {
	return &CommandRouter{provider: provider}
}

// Execute dispatches cmd to the appropriate provider method and returns
// human-readable confirmation text for TTS. If cmd is nil, it returns a
// safe no-op message. Unknown actions return a friendly message without
// an error -- they are simply unsupported, not failures.
func (cr *CommandRouter) Execute(cmd *ActionCommand) (string, error) {
	if cmd == nil {
		return "No action to execute.", nil
	}

	slog.Info("executing command", "action", cmd.Action, "sessionID", cmd.SessionID, "pid", cmd.PID)

	switch cmd.Action {
	case ActionResumeSession:
		return cr.resumeSession(cmd)
	case ActionStopSession:
		return cr.stopSession(cmd)
	case ActionLaunchSession:
		return cr.launchSession(cmd)
	case ActionRespondApproval:
		return cr.respondApproval(cmd)
	case ActionFocusSession:
		return cr.focusSession(cmd)
	case ActionBroadcast:
		return cr.broadcast(cmd)
	case ActionApproveAll:
		return cr.approveAll(cmd)
	case ActionDenyAll:
		return cr.denyAll(cmd)
	case ActionGitStage:
		return cr.gitStage(cmd)
	case ActionGitCommit:
		return cr.gitCommit(cmd)
	case ActionGitPush:
		return cr.gitPush(cmd)
	case ActionNavigateView:
		// Navigation is handled by the orchestrator via Wails events, not here.
		// Return confirmation text for TTS.
		return fmt.Sprintf("Showing you the %s.", cmd.Command), nil
	case ActionCMuxSend:
		return cr.cmuxSend(cmd)
	case ActionCMuxFocus:
		return cr.cmuxFocus(cmd)
	case ActionCMuxRead:
		return cr.cmuxRead(cmd)
	case ActionTerminalSend:
		return cr.terminalSend(cmd)
	case ActionTerminalFocus:
		return cr.terminalFocus(cmd)
	case ActionSystemFocusApp, ActionSystemOpenApp:
		return cr.systemFocusApp(cmd)
	case ActionSystemOpenURL:
		return cr.systemOpenURL(cmd)
	default:
		slog.Warn("unknown action type", "action", cmd.Action)
		return "I can't do that yet.", nil
	}
}

func (cr *CommandRouter) resumeSession(cmd *ActionCommand) (string, error) {
	_, err := cr.provider.ResumeSession(cmd.SessionID)
	if err != nil {
		return fmt.Sprintf("Sorry, I couldn't resume that session: %s", err), nil
	}
	return "Resuming session.", nil
}

func (cr *CommandRouter) stopSession(cmd *ActionCommand) (string, error) {
	err := cr.provider.StopSession(cmd.SessionID)
	if err != nil {
		return fmt.Sprintf("Sorry, I couldn't stop that session: %s", err), nil
	}
	return "Session stopped.", nil
}

func (cr *CommandRouter) launchSession(cmd *ActionCommand) (string, error) {
	// Voice commands default to claude-code when no agent type is specified.
	agentType := string(model.AgentClaudeCode)

	_, err := cr.provider.LaunchSession(agentType, cmd.Project, cmd.Prompt)
	if err != nil {
		return fmt.Sprintf("Sorry, I couldn't launch that session: %s", err), nil
	}
	return "Launching new session.", nil
}

func (cr *CommandRouter) respondApproval(cmd *ActionCommand) (string, error) {
	pid := cmd.PID

	// If no PID was provided but a session name was, look it up from pending approvals.
	if pid == 0 && cmd.SessionID != "" {
		resolved, err := cr.resolveApprovalPID(cmd.SessionID)
		if err != nil {
			return fmt.Sprintf("Sorry, I couldn't look up pending approvals: %s", err), nil
		}
		if resolved == 0 {
			return fmt.Sprintf("No pending approval found for session %q.", cmd.SessionID), nil
		}
		pid = resolved
	}

	err := cr.provider.RespondToApproval(pid, cmd.Response)
	if err != nil {
		return fmt.Sprintf("Sorry, I couldn't respond to that approval: %s", err), nil
	}
	if cmd.Response == "deny" {
		return "Approval denied.", nil
	}
	return "Approval responded.", nil
}

// resolveApprovalPID searches pending approvals for one whose SessionName
// contains the given name (case-insensitive substring match). Returns the
// matching PID, or 0 if no match is found.
func (cr *CommandRouter) resolveApprovalPID(sessionName string) (int, error) {
	approvals, err := cr.provider.GetPendingApprovals()
	if err != nil {
		return 0, err
	}
	needle := strings.ToLower(sessionName)
	for _, a := range approvals {
		if strings.Contains(strings.ToLower(a.SessionName), needle) {
			return a.PID, nil
		}
	}
	return 0, nil
}

func (cr *CommandRouter) approveAll(cmd *ActionCommand) (string, error) {
	return cr.respondAll("y")
}

func (cr *CommandRouter) denyAll(cmd *ActionCommand) (string, error) {
	return cr.respondAll("n")
}

// respondAll fetches all pending approvals and sends the given response
// ("y" or "n") to each one. Returns a count summary or the first error.
func (cr *CommandRouter) respondAll(response string) (string, error) {
	approvals, err := cr.provider.GetPendingApprovals()
	if err != nil {
		return fmt.Sprintf("Sorry, I couldn't fetch pending approvals: %s", err), nil
	}
	if len(approvals) == 0 {
		return "No pending approvals to respond to.", nil
	}

	action := "Approved"
	if response == "n" {
		action = "Denied"
	}

	count := 0
	for _, a := range approvals {
		if respondErr := cr.provider.RespondToApproval(a.PID, response); respondErr != nil {
			slog.Warn("respondAll: failed for PID", "pid", a.PID, "err", respondErr)
			continue
		}
		count++
	}

	return fmt.Sprintf("%s %d pending requests.", action, count), nil
}

func (cr *CommandRouter) focusSession(cmd *ActionCommand) (string, error) {
	err := cr.provider.FocusSession(cmd.PID)
	if err != nil {
		return fmt.Sprintf("Sorry, I couldn't focus that session: %s", err), nil
	}
	return "Focused on session.", nil
}

func (cr *CommandRouter) broadcast(cmd *ActionCommand) (string, error) {
	results, err := cr.provider.BroadcastToAll(cmd.Command)
	if err != nil {
		return fmt.Sprintf("Sorry, I couldn't broadcast that command: %s", err), nil
	}
	return fmt.Sprintf("Broadcast sent to %d sessions.", len(results)), nil
}

// ---------------------------------------------------------------------------
// Git operations
// ---------------------------------------------------------------------------

func (cr *CommandRouter) gitStage(cmd *ActionCommand) (string, error) {
	repoPath, err := cr.resolveRepoPath(cmd.Project)
	if err != nil {
		return err.Error(), nil
	}
	if err := cr.provider.GitStageAll(repoPath); err != nil {
		return fmt.Sprintf("Sorry, I couldn't stage changes in %s: %s", cmd.Project, err), nil
	}
	return fmt.Sprintf("Staged all changes in %s.", cmd.Project), nil
}

func (cr *CommandRouter) gitCommit(cmd *ActionCommand) (string, error) {
	repoPath, err := cr.resolveRepoPath(cmd.Project)
	if err != nil {
		return err.Error(), nil
	}
	message := cmd.Command
	if message == "" {
		message = "Update from Jarvis"
	}
	// Stage first, then commit.
	if err := cr.provider.GitStageAll(repoPath); err != nil {
		return fmt.Sprintf("Sorry, I couldn't stage changes in %s: %s", cmd.Project, err), nil
	}
	if err := cr.provider.GitCommit(repoPath, message); err != nil {
		return fmt.Sprintf("Sorry, I couldn't commit to %s: %s", cmd.Project, err), nil
	}
	return fmt.Sprintf("Committed to %s: %s", cmd.Project, message), nil
}

func (cr *CommandRouter) gitPush(cmd *ActionCommand) (string, error) {
	repoPath, err := cr.resolveRepoPath(cmd.Project)
	if err != nil {
		return err.Error(), nil
	}
	if err := cr.provider.GitPush(repoPath); err != nil {
		return fmt.Sprintf("Sorry, I couldn't push %s: %s", cmd.Project, err), nil
	}
	return fmt.Sprintf("Pushed %s to remote.", cmd.Project), nil
}

// resolveRepoPath looks up a project name in active sessions and returns
// the full repo path. Matches by case-insensitive basename.
func (cr *CommandRouter) resolveRepoPath(projectName string) (string, error) {
	if projectName == "" {
		return "", fmt.Errorf("I need a project name to do that")
	}

	sessions, err := cr.provider.GetActiveSessions()
	if err != nil {
		return "", fmt.Errorf("Sorry, I couldn't look up your sessions: %s", err)
	}

	target := strings.ToLower(projectName)
	for _, s := range sessions {
		parts := strings.Split(s.RepoPath, "/")
		basename := strings.ToLower(parts[len(parts)-1])
		if strings.Contains(basename, target) {
			return s.RepoPath, nil
		}
	}

	return "", fmt.Errorf("I don't see %s in your active sessions", projectName)
}

// resolveSessionByProject looks up a project name in active sessions and
// returns the matching session. Uses the same case-insensitive basename
// matching as resolveRepoPath.
func (cr *CommandRouter) resolveSessionByProject(projectName string) (*model.Session, error) {
	if projectName == "" {
		return nil, fmt.Errorf("I need a project name to do that")
	}

	sessions, err := cr.provider.GetActiveSessions()
	if err != nil {
		return nil, fmt.Errorf("Sorry, I couldn't look up your sessions: %s", err)
	}

	target := strings.ToLower(projectName)
	for _, s := range sessions {
		parts := strings.Split(s.RepoPath, "/")
		basename := strings.ToLower(parts[len(parts)-1])
		if strings.Contains(basename, target) {
			return &s, nil
		}
	}

	return nil, fmt.Errorf("I couldn't find a terminal for %s", projectName)
}

// ---------------------------------------------------------------------------
// CMux terminal operations
// ---------------------------------------------------------------------------

func (cr *CommandRouter) cmuxSend(cmd *ActionCommand) (string, error) {
	if !cr.provider.IsCMuxAvailable() {
		return "CMux isn't running right now.", nil
	}

	sess, err := cr.resolveSessionByProject(cmd.Project)
	if err != nil {
		return err.Error(), nil
	}

	surfaceRef := fmt.Sprintf("%d", sess.PID)
	if err := cr.provider.SendToCMux(surfaceRef, cmd.Command); err != nil {
		return fmt.Sprintf("Sorry, I couldn't send to %s: %s", cmd.Project, err), nil
	}
	return fmt.Sprintf("Sent to %s.", cmd.Project), nil
}

func (cr *CommandRouter) cmuxFocus(cmd *ActionCommand) (string, error) {
	if !cr.provider.IsCMuxAvailable() {
		return "CMux isn't running right now.", nil
	}

	sess, err := cr.resolveSessionByProject(cmd.Project)
	if err != nil {
		return err.Error(), nil
	}

	surfaceRef := fmt.Sprintf("%d", sess.PID)
	if err := cr.provider.FocusCMuxSurface(surfaceRef); err != nil {
		return fmt.Sprintf("Sorry, I couldn't focus %s: %s", cmd.Project, err), nil
	}
	return fmt.Sprintf("Focusing %s terminal.", cmd.Project), nil
}

func (cr *CommandRouter) cmuxRead(cmd *ActionCommand) (string, error) {
	if !cr.provider.IsCMuxAvailable() {
		return "CMux isn't running right now.", nil
	}

	sess, err := cr.resolveSessionByProject(cmd.Project)
	if err != nil {
		return err.Error(), nil
	}

	surfaceRef := fmt.Sprintf("%d", sess.PID)
	output, err := cr.provider.ReadFromCMux(surfaceRef)
	if err != nil {
		return fmt.Sprintf("Sorry, I couldn't read from %s: %s", cmd.Project, err), nil
	}

	output = strings.TrimSpace(output)
	if output == "" {
		return fmt.Sprintf("The %s terminal is empty.", cmd.Project), nil
	}

	// Truncate long output so TTS stays manageable.
	const maxLen = 200
	if len(output) > maxLen {
		output = output[len(output)-maxLen:]
	}
	return output, nil
}

// ---------------------------------------------------------------------------
// Terminal window operations
// ---------------------------------------------------------------------------

func (cr *CommandRouter) terminalSend(cmd *ActionCommand) (string, error) {
	windowID := cmd.SessionID
	if windowID == "" {
		return "I need a terminal window ID to send to.", nil
	}
	if err := cr.provider.SendToTerminal(windowID, cmd.Command); err != nil {
		return fmt.Sprintf("Sorry, I couldn't send to that terminal: %s", err), nil
	}
	return "Sent to terminal.", nil
}

func (cr *CommandRouter) terminalFocus(cmd *ActionCommand) (string, error) {
	windowID := cmd.SessionID
	if windowID == "" {
		return "I need a terminal window ID to focus.", nil
	}
	if err := cr.provider.FocusTerminalWindow(windowID); err != nil {
		return fmt.Sprintf("Sorry, I couldn't focus that terminal: %s", err), nil
	}
	return "Terminal focused.", nil
}

// ---------------------------------------------------------------------------
// System operations
// ---------------------------------------------------------------------------

func (cr *CommandRouter) systemFocusApp(cmd *ActionCommand) (string, error) {
	appName := cmd.Command
	if appName == "" {
		return "I need an app name to focus.", nil
	}

	if err := cr.provider.OpenApp(appName); err != nil {
		return fmt.Sprintf("Sorry, I couldn't open %s: %s", appName, err), nil
	}
	return fmt.Sprintf("Opening %s.", appName), nil
}

func (cr *CommandRouter) systemOpenURL(cmd *ActionCommand) (string, error) {
	url := cmd.Command
	if url == "" {
		return "I need a URL to open.", nil
	}

	if err := cr.provider.OpenURL(url); err != nil {
		return fmt.Sprintf("Sorry, I couldn't open that URL: %s", err), nil
	}
	return "Opening the link.", nil
}
