package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/namanchopra/jarvis/internal/model"
	"github.com/namanchopra/jarvis/internal/notify"
	"github.com/namanchopra/jarvis/internal/paths"
	"github.com/namanchopra/jarvis/internal/proc"
	"github.com/namanchopra/jarvis/internal/store"
)

// SessionManager orchestrates the lifecycle of agent sessions. It launches,
// monitors, and cleans up sessions, persists state to the store, streams
// output to log files, and emits Wails events for the frontend.
type SessionManager struct {
	adapters map[model.AgentType]AgentAdapter
	store    *store.Store
	active   map[string]*managedSession // session ID -> managed session
	pending  map[string]model.Session   // session ID -> session waiting for deps
	mu       sync.Mutex
	ctx      context.Context
	cancel   context.CancelFunc
	emitFn   func(eventName string, data ...interface{}) // Wails EventsEmit wrapper
	logDir   string                                       // directory for session output logs
}

// managedSession pairs the persisted session model with its live process handle.
type managedSession struct {
	session model.Session
	running *RunningSession
}

// NewSessionManager creates a SessionManager backed by the given store. The
// emitFn callback is used to push events to the Wails frontend (typically
// runtime.EventsEmit bound to the app context).
func NewSessionManager(s *store.Store, emitFn func(string, ...interface{})) *SessionManager {
	logDir := paths.LogsDir()

	// Best-effort directory creation; errors are logged but not fatal.
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		slog.Error("failed to create session log directory", "path", logDir, "err", err)
	}

	return &SessionManager{
		adapters: make(map[model.AgentType]AgentAdapter),
		store:    s,
		active:   make(map[string]*managedSession),
		pending:  make(map[string]model.Session),
		emitFn:   emitFn,
		logDir:   logDir,
	}
}

// SetEmitFn replaces the Wails event-emitter callback. This is useful when the
// Wails runtime context is not available at construction time (it is only
// available inside the OnStartup callback).
func (m *SessionManager) SetEmitFn(fn func(string, ...interface{})) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.emitFn = fn
}

// RegisterAdapter adds an adapter for the given agent type. It overwrites any
// previously registered adapter for the same type.
func (m *SessionManager) RegisterAdapter(adapter AgentAdapter) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.adapters[adapter.Name()] = adapter
}

// Start initialises the manager's context and recovers sessions that were
// active before the last shutdown. Processes that are no longer running are
// marked as failed.
func (m *SessionManager) Start(ctx context.Context) {
	m.ctx, m.cancel = context.WithCancel(ctx)

	sessions, err := m.store.GetActiveSessions()
	if err != nil {
		slog.Error("failed to recover active sessions", "err", err)
		return
	}

	for _, sess := range sessions {
		if sess.PID <= 0 {
			// No process was ever started — mark as failed.
			m.markSessionTerminal(sess.ID, model.SessionFailed, -1, "no process found on recovery")
			continue
		}

		if !proc.IsAlive(sess.PID) {
			slog.Info("recovered session process is dead, marking as failed",
				"session_id", sess.ID, "pid", sess.PID)
			m.markSessionTerminal(sess.ID, model.SessionFailed, -1, "process not running after restart")
			continue
		}

		// Process is still alive but we have no handle to its stdout/stderr.
		// We cannot re-attach pipes to an existing process, so we log a
		// warning and mark the session as completed. The user can resume it.
		slog.Warn("recovered session process still alive but cannot re-attach",
			"session_id", sess.ID, "pid", sess.PID)
		m.markSessionTerminal(sess.ID, model.SessionCompleted, 0, "process was running at restart; marked completed (use Resume to continue)")
	}

	// Recover queued sessions that were waiting for dependencies.
	queued, err := m.store.ListSessions(string(model.SessionQueued))
	if err != nil {
		slog.Error("failed to recover queued sessions", "err", err)
		return
	}
	for _, sess := range queued {
		if len(sess.DependsOn) == 0 {
			// Queued with no deps — shouldn't happen, but launch it.
			slog.Warn("recovered queued session with no dependencies, launching",
				"session_id", sess.ID)
			go m.launchQueuedSession(sess)
			continue
		}

		// Check if deps are already satisfied from the time we were down.
		allDone, anyFailed, failedID := m.checkDeps(sess.DependsOn)
		if anyFailed {
			m.markSessionTerminal(sess.ID, model.SessionFailed, -1,
				fmt.Sprintf("dependency %s failed while app was not running", failedID))
			continue
		}
		if allDone {
			slog.Info("recovered queued session with all deps satisfied, launching",
				"session_id", sess.ID)
			go m.launchQueuedSession(sess)
			continue
		}

		// Still waiting — put back in pending queue.
		m.mu.Lock()
		m.pending[sess.ID] = sess
		m.mu.Unlock()
		slog.Info("recovered queued session, still waiting for dependencies",
			"session_id", sess.ID, "depends_on", sess.DependsOn)
	}
}

// Launch starts a new agent session for the given agent type, repo, and prompt.
// The session is persisted, the agent process is started, and output streaming
// begins in a background goroutine.
func (m *SessionManager) Launch(agentType model.AgentType, repoPath, prompt string) (model.Session, error) {
	m.mu.Lock()
	adapter, ok := m.adapters[agentType]
	m.mu.Unlock()

	if !ok {
		return model.Session{}, fmt.Errorf("Launch: no adapter registered for agent type %q", agentType)
	}

	if !adapter.IsAvailable() {
		return model.Session{}, fmt.Errorf("Launch: agent %q is not available (CLI not found in PATH)", agentType)
	}

	// Create the session model.
	sess := model.NewSession(agentType, repoPath, prompt)
	sess.OutputPath = filepath.Join(m.logDir, sess.ID+".log")

	// Persist the session to the store.
	sess, err := m.store.CreateSession(sess)
	if err != nil {
		return model.Session{}, fmt.Errorf("Launch: creating session: %w", err)
	}

	// Update status to launching (it already is from NewSession, but this
	// ensures the store row reflects the same state).
	sess, err = m.store.UpdateSession(sess.ID, map[string]interface{}{
		"status": string(model.SessionLaunching),
	})
	if err != nil {
		return model.Session{}, fmt.Errorf("Launch: updating status to launching: %w", err)
	}

	// Launch the agent process.
	opts := LaunchOptions{
		RepoPath: repoPath,
		Prompt:   prompt,
	}

	return m.launchWithOpts(adapter, sess, opts)
}

// LaunchWithArgs starts a new agent session with additional CLI arguments.
// This is used by the workspace system to pass --add-dir flags for virtual
// monorepo workspaces. The extraArgs are forwarded to the agent adapter.
func (m *SessionManager) LaunchWithArgs(agentType model.AgentType, repoPath, prompt string, extraArgs []string) (model.Session, error) {
	m.mu.Lock()
	adapter, ok := m.adapters[agentType]
	m.mu.Unlock()

	if !ok {
		return model.Session{}, fmt.Errorf("LaunchWithArgs: no adapter registered for agent type %q", agentType)
	}

	if !adapter.IsAvailable() {
		return model.Session{}, fmt.Errorf("LaunchWithArgs: agent %q is not available (CLI not found in PATH)", agentType)
	}

	// Create the session model.
	sess := model.NewSession(agentType, repoPath, prompt)
	sess.OutputPath = filepath.Join(m.logDir, sess.ID+".log")

	// Persist the session to the store.
	sess, err := m.store.CreateSession(sess)
	if err != nil {
		return model.Session{}, fmt.Errorf("LaunchWithArgs: creating session: %w", err)
	}

	sess, err = m.store.UpdateSession(sess.ID, map[string]interface{}{
		"status": string(model.SessionLaunching),
	})
	if err != nil {
		return model.Session{}, fmt.Errorf("LaunchWithArgs: updating status to launching: %w", err)
	}

	opts := LaunchOptions{
		RepoPath:  repoPath,
		Prompt:    prompt,
		ExtraArgs: extraArgs,
	}

	return m.launchWithOpts(adapter, sess, opts)
}

// launchWithOpts is the shared implementation for Launch and LaunchWithArgs.
// It starts the agent process, updates the session, tracks it, and starts
// output streaming.
func (m *SessionManager) launchWithOpts(adapter AgentAdapter, sess model.Session, opts LaunchOptions) (model.Session, error) {
	running, err := adapter.Launch(m.ctx, opts)
	if err != nil {
		// Mark the session as failed.
		m.markSessionTerminal(sess.ID, model.SessionFailed, -1, fmt.Sprintf("launch failed: %v", err))
		return model.Session{}, fmt.Errorf("Launch: starting agent: %w", err)
	}

	// Update the session with process info.
	updates := map[string]interface{}{
		"pid":    running.PID,
		"status": string(model.SessionRunning),
	}
	if running.SessionID != "" {
		updates["agent_session_id"] = running.SessionID
	}

	sess, err = m.store.UpdateSession(sess.ID, updates)
	if err != nil {
		// Process is running but we failed to record it. Stop the process.
		_ = adapter.Stop(m.ctx, running)
		return model.Session{}, fmt.Errorf("Launch: updating session with PID: %w", err)
	}

	// Track the session.
	m.mu.Lock()
	m.active[sess.ID] = &managedSession{session: sess, running: running}
	m.mu.Unlock()

	// Start the output streaming goroutine.
	go m.streamOutput(sess.ID, running)

	// Emit launch event.
	m.emit("session_launched", map[string]interface{}{
		"sessionId": sess.ID,
		"agentType": string(sess.AgentType),
		"repoPath":  opts.RepoPath,
	})

	return sess, nil
}

// streamOutput reads from the running session's output channel, writes each
// line to the log file, and emits Wails events for the frontend. When the
// output channel closes it finalises the session.
func (m *SessionManager) streamOutput(sessionID string, running *RunningSession) {
	// Resolve the output path from the stored session.
	m.mu.Lock()
	ms, ok := m.active[sessionID]
	m.mu.Unlock()
	if !ok {
		slog.Error("streamOutput: session not in active map", "session_id", sessionID)
		return
	}

	logPath := ms.session.OutputPath

	// Open or create the log file.
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		slog.Error("streamOutput: failed to open log file", "path", logPath, "err", err)
		// Continue without file logging; we still emit events.
		logFile = nil
	}

	defer func() {
		if logFile != nil {
			logFile.Close()
		}
	}()

	// Read output lines from the agent process.
	for line := range running.Output {
		// Write to log file.
		if logFile != nil {
			ts := line.Timestamp.Format(time.RFC3339)
			prefix := ""
			if line.IsError {
				prefix = "[stderr] "
			}
			if line.IsSystem {
				prefix = "[system] "
			}
			logLine := fmt.Sprintf("[%s] %s%s\n", ts, prefix, line.Text)
			if _, err := logFile.WriteString(logLine); err != nil {
				slog.Error("streamOutput: write to log file failed", "session_id", sessionID, "err", err)
			}
		}

		// Emit the line to the frontend.
		m.emit("session:"+sessionID, line)

		// Heuristic: detect "needs input" patterns.
		if looksLikeNeedsInput(line.Text) {
			m.emit("session:"+sessionID+":status", "needs-input")
			// Update session status in store.
			if _, err := m.store.UpdateSession(sessionID, map[string]interface{}{
				"status": string(model.SessionNeedsInput),
			}); err != nil {
				slog.Error("streamOutput: failed to update needs-input status", "session_id", sessionID, "err", err)
			}
		}
	}

	// Output channel closed — process has exited. Read the exit error.
	var exitErr error
	select {
	case exitErr = <-running.Done:
	case <-time.After(10 * time.Second):
		slog.Warn("streamOutput: timed out waiting for done signal", "session_id", sessionID)
	}

	// Determine final status and exit code.
	var (
		finalStatus model.SessionStatus
		exitCode    int
		errMsg      string
	)

	if exitErr == nil {
		finalStatus = model.SessionCompleted
		exitCode = 0
	} else {
		finalStatus = model.SessionFailed
		exitCode = -1
		errMsg = exitErr.Error()

		// Try to extract the real exit code.
		if exitError, ok := exitErr.(*os.PathError); ok {
			_ = exitError // fallthrough
		}
	}

	// Check if agent session ID was captured during the run (e.g., Claude
	// Code emits session_id in its stream-json result message).
	agentUpdates := map[string]interface{}{
		"status":        string(finalStatus),
		"exit_code":     exitCode,
		"error_message": errMsg,
		"pid":           0, // process is no longer running
	}
	if running.SessionID != "" {
		agentUpdates["agent_session_id"] = running.SessionID
	}

	if _, err := m.store.UpdateSession(sessionID, agentUpdates); err != nil {
		slog.Error("streamOutput: failed to update terminal status", "session_id", sessionID, "err", err)
	}

	// Emit status event.
	m.emit("session:"+sessionID+":status", string(finalStatus))

	// Check the pending queue for sessions waiting on this one.
	m.checkPendingQueue(sessionID, finalStatus)

	// Send OS notification.
	switch finalStatus {
	case model.SessionCompleted:
		_ = notify.Send("Jarvis", fmt.Sprintf("Session completed (%s)", sessionID[:8]))
	case model.SessionFailed:
		_ = notify.Send("Jarvis", fmt.Sprintf("Session failed (%s): %s", sessionID[:8], errMsg))
	}

	// Write a final system line to the log file.
	if logFile != nil {
		ts := time.Now().Format(time.RFC3339)
		finalLine := fmt.Sprintf("[%s] [system] Session %s (exit code %d)\n", ts, finalStatus, exitCode)
		_, _ = logFile.WriteString(finalLine)
	}

	// Remove from active map.
	m.mu.Lock()
	delete(m.active, sessionID)
	m.mu.Unlock()
}

// SendMessage delivers a follow-up message to a running session. If the
// adapter does not support interactive stdin (e.g., Claude Code), an error is
// returned advising the caller to use Resume instead.
func (m *SessionManager) SendMessage(sessionID, message string) error {
	m.mu.Lock()
	ms, ok := m.active[sessionID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("SendMessage: session %q is not active", sessionID)
	}

	adapter, adapterOk := m.adapters[ms.session.AgentType]
	m.mu.Unlock()

	if !adapterOk {
		return fmt.Errorf("SendMessage: no adapter for agent type %q", ms.session.AgentType)
	}

	// Try adapter-level SendMessage first (handles stdin-capable agents).
	err := adapter.SendMessage(m.ctx, ms.running, message)
	if err != nil {
		// Log the attempted message to the output file as a system message.
		m.logSystemMessage(sessionID, fmt.Sprintf("User message (not delivered — use Resume): %s", message))
		return fmt.Errorf("SendMessage: %w", err)
	}

	// Log the sent message to the output file.
	m.logSystemMessage(sessionID, fmt.Sprintf("User message: %s", message))

	return nil
}

// Stop gracefully terminates a running session.
func (m *SessionManager) Stop(sessionID string) error {
	m.mu.Lock()
	ms, ok := m.active[sessionID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("Stop: session %q is not active", sessionID)
	}

	adapter, adapterOk := m.adapters[ms.session.AgentType]
	m.mu.Unlock()

	if !adapterOk {
		return fmt.Errorf("Stop: no adapter for agent type %q", ms.session.AgentType)
	}

	if err := adapter.Stop(m.ctx, ms.running); err != nil {
		slog.Error("Stop: adapter stop failed, cancelling context", "session_id", sessionID, "err", err)
		ms.running.Cancel()
	}

	// The streamOutput goroutine will handle the final status update and
	// removal from the active map when the process exits and the Done
	// channel fires. We do an optimistic status update here.
	if _, err := m.store.UpdateSession(sessionID, map[string]interface{}{
		"status": string(model.SessionCompleted),
	}); err != nil {
		slog.Error("Stop: failed to update session status", "session_id", sessionID, "err", err)
	}

	m.emit("session:"+sessionID+":status", string(model.SessionCompleted))

	return nil
}

// Resume restarts a previously completed or paused session by launching a new
// process that continues the conversation using the agent-specific session ID.
func (m *SessionManager) Resume(sessionID string) (model.Session, error) {
	sess, err := m.store.GetSession(sessionID)
	if err != nil {
		return model.Session{}, fmt.Errorf("Resume: %w", err)
	}

	// Prevent resuming a session that is already active.
	m.mu.Lock()
	if _, active := m.active[sessionID]; active {
		m.mu.Unlock()
		return model.Session{}, fmt.Errorf("Resume: session %q is already running", sessionID)
	}

	adapter, ok := m.adapters[sess.AgentType]
	m.mu.Unlock()

	if !ok {
		return model.Session{}, fmt.Errorf("Resume: no adapter registered for agent type %q", sess.AgentType)
	}

	if !adapter.IsAvailable() {
		return model.Session{}, fmt.Errorf("Resume: agent %q is not available", sess.AgentType)
	}

	if sess.AgentSessionID == "" {
		return model.Session{}, fmt.Errorf("Resume: session %q has no agent session ID for resume", sessionID)
	}

	// Launch with the existing agent session ID to resume.
	opts := LaunchOptions{
		RepoPath:  sess.RepoPath,
		Prompt:    sess.Prompt,
		SessionID: sess.AgentSessionID,
	}

	running, err := adapter.Launch(m.ctx, opts)
	if err != nil {
		return model.Session{}, fmt.Errorf("Resume: launching agent: %w", err)
	}

	// Update session with new PID and running status.
	updates := map[string]interface{}{
		"pid":           running.PID,
		"status":        string(model.SessionRunning),
		"exit_code":     0,
		"error_message": "",
	}
	if running.SessionID != "" {
		updates["agent_session_id"] = running.SessionID
	}

	sess, err = m.store.UpdateSession(sessionID, updates)
	if err != nil {
		_ = adapter.Stop(m.ctx, running)
		return model.Session{}, fmt.Errorf("Resume: updating session: %w", err)
	}

	// Track the resumed session.
	m.mu.Lock()
	m.active[sessionID] = &managedSession{session: sess, running: running}
	m.mu.Unlock()

	// Start output streaming.
	go m.streamOutput(sessionID, running)

	m.emit("session:"+sessionID+":status", string(model.SessionRunning))

	return sess, nil
}

// GetAvailableAgents returns info about all registered adapters, including
// whether each agent's CLI is installed and its version (best-effort).
func (m *SessionManager) GetAvailableAgents() []AgentInfo {
	m.mu.Lock()
	defer m.mu.Unlock()

	agents := make([]AgentInfo, 0, len(m.adapters))
	for agentType, adapter := range m.adapters {
		info := AgentInfo{
			AgentType: agentType,
			Name:      string(agentType),
			Available: adapter.IsAvailable(),
			Version:   detectVersion(string(agentType)),
		}
		agents = append(agents, info)
	}

	return agents
}

// Cleanup cancels the manager's context and stops all active sessions.
func (m *SessionManager) Cleanup() {
	if m.cancel != nil {
		m.cancel()
	}

	m.mu.Lock()
	activeSessions := make([]*managedSession, 0, len(m.active))
	for _, ms := range m.active {
		activeSessions = append(activeSessions, ms)
	}
	m.mu.Unlock()

	for _, ms := range activeSessions {
		m.mu.Lock()
		adapter, ok := m.adapters[ms.session.AgentType]
		m.mu.Unlock()

		if ok {
			if err := adapter.Stop(context.Background(), ms.running); err != nil {
				slog.Error("Cleanup: failed to stop session", "session_id", ms.session.ID, "err", err)
				ms.running.Cancel()
			}
		} else {
			ms.running.Cancel()
		}
	}
}

// ---------------------------------------------------------------------------
// Dependency-aware scheduling
// ---------------------------------------------------------------------------

// QueueSession schedules a session for execution. If the session has no
// dependencies (DependsOn is empty), it is launched immediately. If it has
// dependencies, it is placed in a pending queue and launched automatically
// when all dependencies complete. If any dependency fails, this session is
// marked as failed.
//
// The session must already have been constructed via model.NewSession with the
// DependsOn and Phase fields populated. QueueSession persists the session and
// manages its full lifecycle.
func (m *SessionManager) QueueSession(sess model.Session) (model.Session, error) {
	// Ensure DependsOn is non-nil for consistent handling.
	if sess.DependsOn == nil {
		sess.DependsOn = []string{}
	}

	// No dependencies — launch immediately using the standard path.
	if len(sess.DependsOn) == 0 {
		return m.Launch(sess.AgentType, sess.RepoPath, sess.Prompt)
	}

	// Validate adapter exists and is available.
	m.mu.Lock()
	adapter, adapterOk := m.adapters[sess.AgentType]
	m.mu.Unlock()

	if !adapterOk {
		return model.Session{}, fmt.Errorf("QueueSession: no adapter registered for agent type %q", sess.AgentType)
	}
	if !adapter.IsAvailable() {
		return model.Session{}, fmt.Errorf("QueueSession: agent %q is not available (CLI not found in PATH)", sess.AgentType)
	}

	// Validate that dependency session IDs exist.
	for _, depID := range sess.DependsOn {
		if _, err := m.store.GetSession(depID); err != nil {
			return model.Session{}, fmt.Errorf("QueueSession: dependency %q not found: %w", depID, err)
		}
	}

	// Build a dependency graph that includes both pending and the new session,
	// then check for cycles.
	m.mu.Lock()
	depGraph := make(map[string][]string, len(m.pending)+1)
	for id, ps := range m.pending {
		depGraph[id] = ps.DependsOn
	}
	depGraph[sess.ID] = sess.DependsOn
	m.mu.Unlock()

	if detectCycle(depGraph, sess.ID) {
		return model.Session{}, fmt.Errorf("QueueSession: circular dependency detected for session %q", sess.ID)
	}

	// Check if all deps are already completed (fast path).
	allDone, anyFailed, failedID := m.checkDeps(sess.DependsOn)

	if anyFailed {
		return model.Session{}, fmt.Errorf("QueueSession: dependency %q has already failed", failedID)
	}

	if allDone {
		// All deps already satisfied — persist the session and launch
		// immediately, preserving the DependsOn/Phase metadata.
		sess.OutputPath = filepath.Join(m.logDir, sess.ID+".log")

		stored, err := m.store.CreateSession(sess)
		if err != nil {
			return model.Session{}, fmt.Errorf("QueueSession: creating session: %w", err)
		}

		stored, err = m.store.UpdateSession(stored.ID, map[string]interface{}{
			"status": string(model.SessionLaunching),
		})
		if err != nil {
			return model.Session{}, fmt.Errorf("QueueSession: updating status to launching: %w", err)
		}

		opts := LaunchOptions{
			RepoPath: sess.RepoPath,
			Prompt:   sess.Prompt,
		}
		return m.launchWithOpts(adapter, stored, opts)
	}

	// Deps not yet satisfied — persist in "queued" status and add to pending.
	sess.Status = model.SessionQueued
	sess.OutputPath = filepath.Join(m.logDir, sess.ID+".log")

	stored, err := m.store.CreateSession(sess)
	if err != nil {
		return model.Session{}, fmt.Errorf("QueueSession: creating session: %w", err)
	}

	// Add to pending queue.
	m.mu.Lock()
	m.pending[stored.ID] = stored
	m.mu.Unlock()

	slog.Info("session queued waiting for dependencies",
		"session_id", stored.ID,
		"depends_on", stored.DependsOn,
		"phase", stored.Phase,
	)

	m.emit("session:"+stored.ID+":status", string(model.SessionQueued))

	return stored, nil
}

// GetPendingSessions returns a snapshot of all sessions currently waiting for
// dependencies to complete.
func (m *SessionManager) GetPendingSessions() []model.Session {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]model.Session, 0, len(m.pending))
	for _, sess := range m.pending {
		out = append(out, sess)
	}
	return out
}

// checkPendingQueue is called whenever a session reaches a terminal state
// (completed or failed). It iterates the pending queue and either launches
// sessions whose deps are all satisfied or fails sessions whose deps have
// failed.
func (m *SessionManager) checkPendingQueue(completedID string, finalStatus model.SessionStatus) {
	m.mu.Lock()
	// Take a snapshot so we can release the lock before launching.
	snapshot := make(map[string]model.Session, len(m.pending))
	for id, sess := range m.pending {
		snapshot[id] = sess
	}
	m.mu.Unlock()

	for id, sess := range snapshot {
		// Check if this pending session depends on the completed session.
		dependsOnCompleted := false
		for _, depID := range sess.DependsOn {
			if depID == completedID {
				dependsOnCompleted = true
				break
			}
		}
		if !dependsOnCompleted {
			continue
		}

		// If the completed session failed, mark this pending session as failed.
		if finalStatus == model.SessionFailed {
			m.mu.Lock()
			delete(m.pending, id)
			m.mu.Unlock()

			errMsg := fmt.Sprintf("dependency %s failed", completedID)
			m.markSessionTerminal(id, model.SessionFailed, -1, errMsg)
			m.emit("session:"+id+":status", string(model.SessionFailed))

			slog.Info("pending session failed due to dependency failure",
				"session_id", id, "failed_dep", completedID)

			// This session's failure may cascade to other pending sessions.
			m.checkPendingQueue(id, model.SessionFailed)
			continue
		}

		// Check if ALL dependencies are now completed.
		allDone, anyFailed, failedDepID := m.checkDeps(sess.DependsOn)

		if anyFailed {
			m.mu.Lock()
			delete(m.pending, id)
			m.mu.Unlock()

			errMsg := fmt.Sprintf("dependency %s failed", failedDepID)
			m.markSessionTerminal(id, model.SessionFailed, -1, errMsg)
			m.emit("session:"+id+":status", string(model.SessionFailed))

			slog.Info("pending session failed due to dependency failure",
				"session_id", id, "failed_dep", failedDepID)

			m.checkPendingQueue(id, model.SessionFailed)
			continue
		}

		if !allDone {
			// Still waiting for other deps.
			continue
		}

		// All deps satisfied — remove from pending and launch.
		m.mu.Lock()
		delete(m.pending, id)
		m.mu.Unlock()

		slog.Info("all dependencies satisfied, launching queued session",
			"session_id", id, "phase", sess.Phase)

		go m.launchQueuedSession(sess)
	}
}

// launchQueuedSession launches a session that was waiting in the pending queue.
// The session already exists in the store (with status "queued"). This method
// looks up the adapter, updates the status to launching, and starts the
// process using launchWithOpts — reusing the existing session row rather than
// creating a new one.
func (m *SessionManager) launchQueuedSession(sess model.Session) {
	m.mu.Lock()
	adapter, ok := m.adapters[sess.AgentType]
	m.mu.Unlock()

	if !ok {
		errMsg := fmt.Sprintf("no adapter registered for agent type %q", sess.AgentType)
		slog.Error("launchQueuedSession: "+errMsg, "session_id", sess.ID)
		m.markSessionTerminal(sess.ID, model.SessionFailed, -1, errMsg)
		m.emit("session:"+sess.ID+":status", string(model.SessionFailed))
		m.checkPendingQueue(sess.ID, model.SessionFailed)
		return
	}

	if !adapter.IsAvailable() {
		errMsg := fmt.Sprintf("agent %q is not available (CLI not found in PATH)", sess.AgentType)
		slog.Error("launchQueuedSession: "+errMsg, "session_id", sess.ID)
		m.markSessionTerminal(sess.ID, model.SessionFailed, -1, errMsg)
		m.emit("session:"+sess.ID+":status", string(model.SessionFailed))
		m.checkPendingQueue(sess.ID, model.SessionFailed)
		return
	}

	// Update status to launching.
	updatedSess, err := m.store.UpdateSession(sess.ID, map[string]interface{}{
		"status": string(model.SessionLaunching),
	})
	if err != nil {
		slog.Error("launchQueuedSession: failed to update status to launching",
			"session_id", sess.ID, "err", err)
		m.markSessionTerminal(sess.ID, model.SessionFailed, -1, fmt.Sprintf("status update failed: %v", err))
		m.emit("session:"+sess.ID+":status", string(model.SessionFailed))
		m.checkPendingQueue(sess.ID, model.SessionFailed)
		return
	}

	opts := LaunchOptions{
		RepoPath: sess.RepoPath,
		Prompt:   sess.Prompt,
	}

	if _, err := m.launchWithOpts(adapter, updatedSess, opts); err != nil {
		slog.Error("launchQueuedSession: failed to launch",
			"session_id", sess.ID, "err", err)
		// launchWithOpts already marks the session as failed internally.
		m.checkPendingQueue(sess.ID, model.SessionFailed)
	}
}

// checkDeps checks whether all dependency session IDs have reached a terminal
// state. Returns whether all are completed, whether any have failed, and the
// ID of the first failed dependency (if any).
func (m *SessionManager) checkDeps(depIDs []string) (allCompleted, anyFailed bool, failedID string) {
	allCompleted = true
	for _, depID := range depIDs {
		dep, err := m.store.GetSession(depID)
		if err != nil {
			// If we can't find the dep, treat it as not done.
			allCompleted = false
			continue
		}
		if dep.Status == model.SessionFailed {
			return false, true, depID
		}
		if dep.Status != model.SessionCompleted {
			allCompleted = false
		}
	}
	return allCompleted, false, ""
}

// detectCycle checks for circular dependencies in a dependency graph using DFS.
// The graph maps session IDs to their dependency IDs. Returns true if a cycle
// is detected starting from startID.
func detectCycle(graph map[string][]string, startID string) bool {
	// Track nodes in the current DFS path (gray) and fully visited (black).
	const (
		white = 0 // not visited
		gray  = 1 // in current path
		black = 2 // fully explored
	)
	color := make(map[string]int)

	var dfs func(id string) bool
	dfs = func(id string) bool {
		color[id] = gray
		for _, dep := range graph[id] {
			switch color[dep] {
			case gray:
				// Back edge — cycle detected.
				return true
			case white:
				if dfs(dep) {
					return true
				}
			}
			// black nodes are fully explored, skip.
		}
		color[id] = black
		return false
	}

	return dfs(startID)
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// markSessionTerminal updates a session to a terminal state in the store.
func (m *SessionManager) markSessionTerminal(sessionID string, status model.SessionStatus, exitCode int, errMsg string) {
	updates := map[string]interface{}{
		"status":        string(status),
		"exit_code":     exitCode,
		"error_message": errMsg,
		"pid":           0,
	}
	if _, err := m.store.UpdateSession(sessionID, updates); err != nil {
		slog.Error("markSessionTerminal: failed to update session",
			"session_id", sessionID, "status", string(status), "err", err)
	}
}

// emit invokes the Wails event emitter if configured. It is safe to call with
// a nil emitFn.
func (m *SessionManager) emit(eventName string, data ...interface{}) {
	if m.emitFn != nil {
		m.emitFn(eventName, data...)
	}
}

// logSystemMessage appends a system-level message to the session's log file.
func (m *SessionManager) logSystemMessage(sessionID, message string) {
	m.mu.Lock()
	ms, ok := m.active[sessionID]
	m.mu.Unlock()
	if !ok {
		return
	}

	logPath := ms.session.OutputPath
	if logPath == "" {
		return
	}

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		slog.Error("logSystemMessage: failed to open log file", "path", logPath, "err", err)
		return
	}
	defer f.Close()

	ts := time.Now().Format(time.RFC3339)
	_, _ = fmt.Fprintf(f, "[%s] [system] %s\n", ts, message)
}

// looksLikeNeedsInput returns true if the text matches heuristic patterns that
// suggest the agent is waiting for user input.
func looksLikeNeedsInput(text string) bool {
	lower := strings.ToLower(text)
	patterns := []string{
		"waiting for",
		"waiting for input",
		"question:",
		"do you want to",
		"would you like to",
		"please confirm",
		"press enter",
		"y/n",
		"yes/no",
		"(y/n)",
	}
	for _, p := range patterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	// Check if the line ends with a question mark (common pattern).
	trimmed := strings.TrimSpace(text)
	if len(trimmed) > 10 && strings.HasSuffix(trimmed, "?") {
		return true
	}
	return false
}


// detectVersion attempts to determine the version of an agent CLI by running
// `<binary> --version`. Returns an empty string on any failure.
func detectVersion(agentType string) string {
	// Map agent types to their CLI binary names.
	binaryMap := map[string]string{
		"claude-code": "claude",
		"kiro":        "kiro",
		"gemini":      "gemini",
		"codex":       "codex",
		"aider":       "aider",
	}

	binary, ok := binaryMap[agentType]
	if !ok {
		return ""
	}

	// Check if the binary exists in PATH first.
	path, err := exec.LookPath(binary)
	if err != nil {
		return ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, path, "--version")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}

	// Take the first line and trim whitespace.
	version := strings.TrimSpace(string(out))
	if idx := strings.IndexByte(version, '\n'); idx >= 0 {
		version = version[:idx]
	}

	return version
}
