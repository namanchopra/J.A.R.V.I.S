package scanner

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/namanchopra/jarvis/internal/model"
	"github.com/namanchopra/jarvis/internal/notify"
	"github.com/namanchopra/jarvis/internal/store"

	"github.com/shirou/gopsutil/v4/process"
)

// autoDetectedPrefix is prepended to the description of tasks created by the
// scanner so the UI can distinguish them from manually-created tasks.
const autoDetectedPrefix = "[auto-detected] "

// maxCommandLen is the maximum number of characters stored from the process
// command line.
const maxCommandLen = 200

// maxGitSearchDepth is the maximum number of parent directories to walk when
// searching for a .git directory.
const maxGitSearchDepth = 5

// excludePatterns contains substrings found in command lines of known false
// positives (Electron helpers, GPU renderers, macOS app bundles, etc.).
var excludePatterns = []string{
	"Electron",
	"Helper",
	"renderer",
	"gpu-process",
	".app/Contents",
}

// DetectedProcess represents a discovered AI agent process.
type DetectedProcess struct {
	PID      int32
	Agent    model.AgentType
	RepoPath string // working directory of the process
	Command  string // full command line (truncated)
}

// Scanner periodically scans for AI agent processes.
type Scanner struct {
	store    *store.Store
	interval time.Duration
	cancel   context.CancelFunc
	mu       sync.Mutex
	tracked  map[int32]string // PID -> task ID mapping
	selfPID  int32
}

// NewScanner creates a Scanner that uses s for persistence and scans every
// interval. Call Start to begin the periodic loop.
func NewScanner(s *store.Store, interval time.Duration) *Scanner {
	return &Scanner{
		store:    s,
		interval: interval,
		tracked:  make(map[int32]string),
		selfPID:  int32(os.Getpid()),
	}
}

// ---------------------------------------------------------------------------
// ScanOnce --- single pass over the process table
// ---------------------------------------------------------------------------

// ScanOnce performs a one-time scan of all running processes and returns those
// that match a known AI agent pattern.
func (sc *Scanner) ScanOnce(ctx context.Context) ([]DetectedProcess, error) {
	procs, err := process.ProcessesWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing processes: %w", err)
	}

	var detected []DetectedProcess
	for _, p := range procs {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		// Skip our own process tree.
		if sc.isSelfOrChild(ctx, p) {
			continue
		}

		// Filter out very short-lived processes (< 2 s).
		if ct, err := p.CreateTimeWithContext(ctx); err == nil {
			age := time.Since(time.UnixMilli(ct))
			if age < 2*time.Second {
				continue
			}
		}

		// Pre-filter: check the cmdline against known false positive patterns
		// before the more expensive matchAgent logic.
		cmdline, _ := p.CmdlineWithContext(ctx)
		if containsAnyPattern(cmdline, excludePatterns) {
			continue
		}

		agent, ok := sc.matchAgent(ctx, p)
		if !ok {
			continue
		}

		// Parent process filter: skip if the parent is an Electron app.
		if sc.parentIsElectron(ctx, p) {
			slog.Debug("scanner: skipping Electron child process",
				"pid", p.Pid, "agent", agent)
			continue
		}

		repoPath := sc.resolveRepoPath(ctx, p)

		// Git repo validation: skip processes whose CWD is not inside a Git
		// repository --- they are unlikely to be real coding sessions.
		if repoPath == "" {
			slog.Debug("scanner: skipping process without git repo",
				"pid", p.Pid, "agent", agent)
			continue
		}

		cmdlineTrunc := truncate(cmdline, maxCommandLen)

		detected = append(detected, DetectedProcess{
			PID:      p.Pid,
			Agent:    agent,
			RepoPath: repoPath,
			Command:  cmdlineTrunc,
		})
	}

	return detected, nil
}

// ---------------------------------------------------------------------------
// Reconcile --- sync detected processes with the task store
// ---------------------------------------------------------------------------

// Reconcile scans for running AI agent processes and synchronises the task
// store: new processes get a task created, vanished processes get their task
// marked as done. It returns the number of newly created tasks.
func (sc *Scanner) Reconcile(ctx context.Context) (int, error) {
	detected, err := sc.ScanOnce(ctx)
	if err != nil {
		return 0, fmt.Errorf("Reconcile: %w", err)
	}

	sc.mu.Lock()
	defer sc.mu.Unlock()

	// Build a set of currently-seen PIDs for the cleanup step.
	seenPIDs := make(map[int32]struct{}, len(detected))
	newCount := 0

	for _, dp := range detected {
		seenPIDs[dp.PID] = struct{}{}

		// Already tracking this PID --- nothing to do.
		if _, tracked := sc.tracked[dp.PID]; tracked {
			continue
		}

		// Check whether a running task already exists for this repo+agent
		// combo to avoid duplicates across rescans (e.g. the PID changed but
		// the same agent session continues in the same repo).
		if sc.taskExistsForRepoAgent(dp.RepoPath, dp.Agent) {
			continue
		}

		// Create a new task.
		repoBase := filepath.Base(dp.RepoPath)
		if repoBase == "." || repoBase == "/" {
			repoBase = "unknown"
		}

		name := fmt.Sprintf("%s: %s", dp.Agent, repoBase)
		desc := autoDetectedPrefix + dp.Command

		task := model.NewTask(name, desc, dp.RepoPath, dp.Agent)
		task.Status = model.StatusRunning

		created, err := sc.store.CreateTask(task)
		if err != nil {
			slog.Error("scanner: failed to create task", "err", err, "pid", dp.PID)
			continue
		}

		sc.tracked[dp.PID] = created.ID
		newCount++
		slog.Info("scanner: auto-detected agent",
			"agent", dp.Agent,
			"pid", dp.PID,
			"repo", dp.RepoPath,
			"task_id", created.ID,
		)

		// Emit activity event for auto-detection.
		sc.logActivity(created.ID, created.Name, "auto_detected",
			fmt.Sprintf("Auto-detected %s session in %s", dp.Agent, repoBase), "")
	}

	// Mark tasks for PIDs that are no longer running as done.
	for pid, taskID := range sc.tracked {
		if _, alive := seenPIDs[pid]; alive {
			continue
		}

		slog.Info("scanner: agent process exited", "pid", pid, "task_id", taskID)

		// Fetch the task to get its name for activity logging and notifications.
		task, getErr := sc.store.GetTask(taskID)
		taskName := taskID // fallback if GetTask fails
		if getErr == nil {
			taskName = task.Name
		}

		if _, err := sc.store.UpdateTask(taskID, map[string]interface{}{
			"status": string(model.StatusDone),
		}); err != nil {
			slog.Error("scanner: failed to mark task done", "err", err, "task_id", taskID)
		} else {
			sc.logActivity(taskID, taskName, "completed",
				fmt.Sprintf("%s completed", taskName), "")
			_ = notify.Send("Jarvis", fmt.Sprintf("Task '%s' completed", taskName))
		}
		delete(sc.tracked, pid)
	}

	// Clean up stale sessions whose processes are no longer alive.
	sc.reconcileSessions()

	return newCount, nil
}

// reconcileSessions marks active sessions as completed if their PID is dead.
func (sc *Scanner) reconcileSessions() {
	sessions, err := sc.store.GetActiveSessions()
	if err != nil {
		slog.Error("scanner: failed to list active sessions", "err", err)
		return
	}
	for _, sess := range sessions {
		if sess.PID <= 0 {
			continue
		}
		p, err := process.NewProcess(int32(sess.PID))
		if err != nil {
			// Process doesn't exist.
			sc.markSessionCompleted(sess.ID, sess.PID)
			continue
		}
		running, err := p.IsRunning()
		if err != nil || !running {
			sc.markSessionCompleted(sess.ID, sess.PID)
		}
	}
}

func (sc *Scanner) markSessionCompleted(sessionID string, pid int) {
	if _, err := sc.store.UpdateSession(sessionID, map[string]interface{}{
		"status": "completed",
	}); err != nil {
		slog.Error("scanner: failed to mark session completed", "err", err, "session_id", sessionID)
	} else {
		slog.Info("scanner: marked stale session completed", "session_id", sessionID, "pid", pid)
	}
}

// ---------------------------------------------------------------------------
// Start / Stop --- periodic loop
// ---------------------------------------------------------------------------

// Start launches a background goroutine that calls Reconcile every interval.
// It returns immediately. The goroutine is stopped by calling Stop or when the
// parent ctx is cancelled.
func (sc *Scanner) Start(ctx context.Context) {
	childCtx, cancel := context.WithCancel(ctx)

	sc.mu.Lock()
	sc.cancel = cancel
	sc.mu.Unlock()

	go sc.loop(childCtx)
}

// Stop cancels the periodic scan loop. It is safe to call multiple times.
func (sc *Scanner) Stop() {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	if sc.cancel != nil {
		sc.cancel()
		sc.cancel = nil
	}
}

func (sc *Scanner) loop(ctx context.Context) {
	slog.Info("scanner: starting periodic scan", "interval", sc.interval)

	// Run immediately on start, then on every tick.
	if _, err := sc.Reconcile(ctx); err != nil {
		slog.Error("scanner: reconcile error", "err", err)
	}

	ticker := time.NewTicker(sc.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("scanner: stopped")
			return
		case <-ticker.C:
			if _, err := sc.Reconcile(ctx); err != nil {
				slog.Error("scanner: reconcile error", "err", err)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Activity logging
// ---------------------------------------------------------------------------

// logActivity creates and persists an activity event via the store. Errors are
// logged but not propagated --- activity logging is fire-and-forget.
func (sc *Scanner) logActivity(taskID, taskName, eventType, message, metadata string) {
	event := model.NewActivityEvent(taskID, taskName, eventType, message, metadata)
	if err := sc.store.CreateActivityEvent(event); err != nil {
		slog.Error("scanner: failed to log activity event", "err", err, "eventType", eventType, "taskID", taskID)
	}
}

// ---------------------------------------------------------------------------
// Agent detection
// ---------------------------------------------------------------------------

// matchAgent checks whether the process matches a known AI agent pattern and
// returns the corresponding AgentType. The second return value is false when the
// process does not match any pattern.
//
// The matching rules are intentionally strict to avoid false positives from
// Electron helper processes, VS Code extensions, GUI launchers, etc.
func (sc *Scanner) matchAgent(ctx context.Context, p *process.Process) (model.AgentType, bool) {
	name, _ := p.NameWithContext(ctx)
	nameLower := strings.ToLower(strings.TrimSpace(name))

	cmdline, _ := p.CmdlineWithContext(ctx)
	cmdlineLower := strings.ToLower(cmdline)

	// ---- Claude Code ----
	// Exact process name "claude", OR cmdline contains the claude binary as a
	// standalone command with headless flags. Exclude known non-CLI processes.
	if matchClaude(nameLower, cmdlineLower) {
		return model.AgentClaudeCode, true
	}

	// ---- Kiro ----
	// Exact process name "kiro-cli" only (NOT "kiro" which matches VS Code
	// extensions). Cmdline fallback requires "kiro-cli" as standalone command.
	if matchExactAgent(nameLower, cmdlineLower, "kiro-cli") {
		return model.AgentKiro, true
	}

	// ---- Gemini ----
	if matchExactAgent(nameLower, cmdlineLower, "gemini") {
		return model.AgentGemini, true
	}

	// ---- Codex ----
	if matchExactAgent(nameLower, cmdlineLower, "codex") {
		return model.AgentCodex, true
	}

	// ---- Aider ----
	if matchExactAgent(nameLower, cmdlineLower, "aider") {
		return model.AgentAider, true
	}

	return "", false
}

// claudeExcludePatterns are substrings that disqualify a process from being
// identified as a Claude Code CLI session.
var claudeExcludePatterns = []string{
	"claude-code-guide",
	"claude-launcher",
	"electron",
	"helper",
	".app/contents",
}

// claudeHeadlessFlags are flags that indicate a headless Claude Code session
// (as opposed to the desktop app or other helpers).
var claudeHeadlessFlags = []string{
	" -p ", " -p\x00",
	" --print ", " --print\x00",
	"--output-format",
}

// matchClaude returns true if the process is a Claude Code CLI session.
func matchClaude(nameLower, cmdlineLower string) bool {
	// Disqualify if any exclude pattern is present.
	if containsAnyPattern(cmdlineLower, claudeExcludePatterns) {
		return false
	}

	// Exact process name match is the strongest signal.
	if nameLower == "claude" {
		return true
	}

	// Cmdline fallback: the command must reference the claude binary as a
	// standalone command (not a substring of another word) AND must include
	// one of the headless flags to filter out Desktop app helpers.
	if !isStandaloneCommand(cmdlineLower, "claude") {
		return false
	}

	for _, flag := range claudeHeadlessFlags {
		if strings.Contains(cmdlineLower, flag) {
			return true
		}
	}

	return false
}

// matchExactAgent returns true if the process matches agent by exact name or
// by standalone command in the cmdline.
func matchExactAgent(nameLower, cmdlineLower, agent string) bool {
	// Exact process name.
	if nameLower == agent {
		return true
	}

	// Cmdline fallback: starts with "agent " or contains "/agent ".
	if strings.HasPrefix(cmdlineLower, agent+" ") || strings.Contains(cmdlineLower, "/"+agent+" ") {
		return true
	}

	return false
}

// isStandaloneCommand reports whether cmd appears in cmdline as a standalone
// command --- either at the start ("cmd ...") or after a path separator
// ("/path/to/cmd ..."). This prevents matching substrings like
// "some-claude-thing".
func isStandaloneCommand(cmdlineLower, cmd string) bool {
	// "cmd " at the very start.
	if strings.HasPrefix(cmdlineLower, cmd+" ") {
		return true
	}
	// "/cmd " anywhere (path-qualified invocation).
	if strings.Contains(cmdlineLower, "/"+cmd+" ") {
		return true
	}
	// Exact match (no trailing space --- the command might be the only arg).
	if cmdlineLower == cmd {
		return true
	}
	// "/cmd" at the very end.
	if strings.HasSuffix(cmdlineLower, "/"+cmd) {
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// isSelfOrChild returns true if p is Jarvis's own process or a direct child of it.
func (sc *Scanner) isSelfOrChild(ctx context.Context, p *process.Process) bool {
	if p.Pid == sc.selfPID {
		return true
	}
	ppid, err := p.PpidWithContext(ctx)
	if err != nil {
		return false
	}
	return ppid == sc.selfPID
}

// parentIsElectron returns true if the parent process of p appears to be an
// Electron application (by name or command line). This catches false positives
// from helper/renderer processes spawned by Electron-based editors.
func (sc *Scanner) parentIsElectron(ctx context.Context, p *process.Process) bool {
	ppid, err := p.PpidWithContext(ctx)
	if err != nil {
		return false
	}

	parent, err := process.NewProcess(ppid)
	if err != nil {
		return false
	}

	if parentName, err := parent.NameWithContext(ctx); err == nil {
		if strings.Contains(strings.ToLower(parentName), "electron") {
			return true
		}
	}

	if parentCmd, err := parent.CmdlineWithContext(ctx); err == nil {
		if strings.Contains(parentCmd, ".app/Contents/MacOS") {
			return true
		}
	}

	return false
}

// resolveRepoPath tries to determine the working directory for p. On macOS
// Cwd() may fail due to permissions, so we fall back to extracting a directory
// argument from the command line.
//
// After resolving the path, it validates that the directory is inside a Git
// repository (has a .git directory in itself or a parent, up to
// maxGitSearchDepth levels). If no .git is found the path is likely not a real
// coding session, so "" is returned.
func (sc *Scanner) resolveRepoPath(ctx context.Context, p *process.Process) string {
	candidate := ""

	// Try Cwd first.
	if cwd, err := p.CwdWithContext(ctx); err == nil && cwd != "" {
		candidate = cwd
	}

	// Fallback: look through command-line arguments for something that looks
	// like an absolute directory path.
	if candidate == "" {
		if cmdline, err := p.CmdlineSliceWithContext(ctx); err == nil {
			for _, arg := range cmdline {
				if filepath.IsAbs(arg) {
					if info, err := os.Stat(arg); err == nil && info.IsDir() {
						candidate = arg
						break
					}
				}
			}
		}
	}

	if candidate == "" {
		return ""
	}

	// Validate: the candidate (or a parent) must contain a .git directory.
	if !hasGitDir(candidate, maxGitSearchDepth) {
		return ""
	}

	return candidate
}

// hasGitDir walks up from dir for at most maxDepth levels checking for a .git
// directory. Returns true as soon as one is found.
func hasGitDir(dir string, maxDepth int) bool {
	for i := 0; i <= maxDepth; i++ {
		gitPath := filepath.Join(dir, ".git")
		if info, err := os.Stat(gitPath); err == nil && info.IsDir() {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root.
			break
		}
		dir = parent
	}
	return false
}

// truncate returns s truncated to maxLen characters.
func truncate(s string, maxLen int) string {
	if len(s) > maxLen {
		return s[:maxLen]
	}
	return s
}

// containsAnyPattern reports whether s contains any of the given patterns
// (case-insensitive).
func containsAnyPattern(s string, patterns []string) bool {
	lower := strings.ToLower(s)
	for _, p := range patterns {
		if strings.Contains(lower, strings.ToLower(p)) {
			return true
		}
	}
	return false
}

// taskExistsForRepoAgent checks whether there is already a running task in the
// store for the given repo path and agent type. This avoids creating duplicate
// tasks when a PID is recycled or during rapid re-scans.
func (sc *Scanner) taskExistsForRepoAgent(repoPath string, agent model.AgentType) bool {
	// Check for ANY task (not just running) to avoid creating duplicates
	// when a session restarts in the same repo.
	tasks, err := sc.store.ListTasks("", repoPath)
	if err != nil {
		return false
	}
	for _, t := range tasks {
		if t.AgentType == agent && t.RepoPath == repoPath {
			// If found a done task, reactivate it instead of creating a new one.
			if t.Status == model.StatusDone {
				if _, updateErr := sc.store.UpdateTask(t.ID, map[string]interface{}{
					"status": string(model.StatusRunning),
				}); updateErr == nil {
					// Track this reactivated task.
					// (caller will skip creation since we return true)
					return true
				}
			}
			return true
		}
	}
	return false
}
