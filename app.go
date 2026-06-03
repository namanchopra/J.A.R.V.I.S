package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/namanchopra/jarvis/internal/agent"
	"github.com/namanchopra/jarvis/internal/api"
	"github.com/namanchopra/jarvis/internal/claude"
	"github.com/namanchopra/jarvis/internal/cmux"
	"github.com/namanchopra/jarvis/internal/config"
	"github.com/namanchopra/jarvis/internal/hotkey"
	"github.com/namanchopra/jarvis/internal/impact"
	"github.com/namanchopra/jarvis/internal/discovery"
	"github.com/namanchopra/jarvis/internal/nlquery"
	"github.com/namanchopra/jarvis/internal/git"
	"github.com/namanchopra/jarvis/internal/macctl"
	"github.com/namanchopra/jarvis/internal/model"
	"github.com/namanchopra/jarvis/internal/notify"
	"github.com/namanchopra/jarvis/internal/paths"
	"github.com/namanchopra/jarvis/internal/recording"
	"github.com/namanchopra/jarvis/internal/scanner"
	"github.com/namanchopra/jarvis/internal/screencapture"
	"github.com/namanchopra/jarvis/internal/store"
	"github.com/namanchopra/jarvis/internal/terminal"
	"github.com/namanchopra/jarvis/internal/jarvis"
	"github.com/namanchopra/jarvis/internal/watcher"
	"github.com/namanchopra/jarvis/internal/workspace"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App is the Wails-bound application struct. Every exported method is
// automatically exposed to the frontend via Wails bindings.
type App struct {
	ctx   context.Context
	store *store.Store

	// scanner auto-detects running AI agent processes. May be nil if disabled.
	scanner *scanner.Scanner

	// sessionMgr orchestrates agent session lifecycle. May be nil if disabled.
	sessionMgr *agent.SessionManager

	// cmuxClient interfaces with CMux terminal multiplexer. May be nil.
	cmuxClient *cmux.Client

	// termMgr aggregates all terminal providers (CMux, iTerm2, Terminal.app).
	termMgr *terminal.TerminalManager

	// jarvis is the AI voice companion. May be nil if not started.
	jarvis *jarvis.Jarvis

	// jarvisProcess holds the Python daemon's os.Process when running.
	// Guarded by jarvisMu.
	jarvisProcess  *os.Process
	jarvisRestarts int
	jarvisMu       sync.Mutex
	// jarvisGeneration bumps on every successful StartJarvis. The
	// monitorJarvisDaemon goroutine captures the value at launch time and
	// only restarts on unexpected exit if the generation still matches --
	// otherwise it knows another StartJarvis has already happened (e.g.
	// from RestartJarvis) and the new daemon has taken its place, so this
	// stale monitor must NOT spawn yet another one. Guarded by jarvisMu.
	jarvisGeneration uint64

	// apiServer is the mobile API HTTP server. May be nil if startup fails.
	apiServer *api.Server

	// activeWatchers tracks running output tailers keyed by task ID.
	// The value is a CancelFunc that stops the tailer goroutine.
	activeWatchers map[string]context.CancelFunc
	watcherMu      sync.Mutex

	// approvalFailCache tracks PIDs that failed to read in GetPendingApprovals.
	// Cached for 60s to avoid log spam from sessions without CMux workspaces.
	approvalFailCache map[int]time.Time
	approvalFailMu    sync.RWMutex

	// macctlOnce + macctlController back the lazy macctl.Controller singleton
	// returned by App.macctl(). The Controller is constructed once on first
	// use (rather than in NewApp) so policy file I/O failures don't break
	// startup -- a Wails call that never uses macctl shouldn't pay the cost
	// of reading ~/.jarvis/policy.json. See app_macctl.go for the accessor.
	macctlOnce       sync.Once
	macctlController *macctl.Controller

	// overlayMu guards overlayState. The overlay state is the saved size +
	// position + fullscreen flag captured by OverlayShow so OverlayHide can
	// restore the main window to its prior geometry. See app_overlay.go
	// (v0.3.0 TASK-004) for the struct definition and the three bindings
	// (OverlayShow / OverlayHide / OverlayToggle) that read & mutate this
	// field. The zero value of overlayState means "no saved geometry" --
	// OverlayHide treats that as a soft no-op (logs a warning, returns nil).
	overlayMu    sync.Mutex
	overlayState overlayGeometry

	// hotkeyManager owns the OVERLAY-TOGGLE global hotkey lifecycle.
	// Default binding: alt+space. Press once to show the overlay; press
	// again to hide. Created on the macOS main thread in main.go's
	// OnStartup. nil if startup never wired it -- Wails bindings guard on
	// nil so a degraded startup doesn't crash. Released in OnShutdown.
	hotkeyManager *hotkey.Manager

	// hotkeyPTTManager owns the global PUSH-TO-TALK hotkey lifecycle.
	// Default binding: ctrl+space. Press-and-hold to start a turn (mic
	// opens + overlay appears); release to send. Works from any app, no
	// overlay-window focus required. Same construction/teardown rules as
	// hotkeyManager above.
	hotkeyPTTManager *hotkey.Manager

	// meetingCapturer owns the ScreenCaptureKit lifecycle while a meeting
	// is active. nil before the first start. Constructed lazily on the
	// first startMeetingCapture() call; left in place across stop/start
	// cycles (the Capturer itself is reusable per its TASK-004 contract).
	// TASK-009's StartMeeting/StopMeeting Wails bindings drive this via
	// the app_meeting.go helpers.
	meetingCapturer screencapture.Capturer

	// meetingMu guards meetingActive and the meetingCapturer field. The
	// SCK callback fires on a serial dispatch queue (NOT the Go main
	// goroutine) -- meetingActive is consulted there to drop post-stop
	// frames; meetingCapturer is read/written from the binding methods.
	meetingMu     sync.Mutex
	meetingActive bool

	// meetingNotesCh receives the markdown file path emitted by the
	// daemon's ``meeting_notes_written`` WS event (see TASK-007 in
	// scripts/jarvis-daemon/main.py:_dispatch_meeting_finalisation).
	// StopMeeting() (TASK-009) blocks on this channel with a 30s
	// timeout to return the path to the caller. Buffered 1 so the
	// daemon-event emitter never blocks even if no one is awaiting --
	// stale notifications are simply dropped on the next push.
	meetingNotesCh chan string
}

// NewApp creates a new App with the given Store, optional Scanner, optional
// SessionManager, optional CMux client, and TerminalManager (any may be nil).
func NewApp(s *store.Store, sc *scanner.Scanner, sm *agent.SessionManager, cc *cmux.Client, tm *terminal.TerminalManager) *App {
	return &App{
		store:          s,
		scanner:        sc,
		sessionMgr:     sm,
		cmuxClient:     cc,
		termMgr:        tm,
		activeWatchers:    make(map[string]context.CancelFunc),
		approvalFailCache: make(map[int]time.Time),
		// meetingNotesCh is buffered 1 so the daemon WS event handler
		// can push the path without blocking even when StopMeeting()
		// is not currently awaiting. The drop-on-full pattern is
		// applied at the push site (app.go:startMobileAPI emitter).
		meetingNotesCh: make(chan string, 1),
	}
}

// WorkflowPhase describes a single phase in a multi-phase workflow execution.
type WorkflowPhase struct {
	AgentType string `json:"agentType"`
	RepoPath  string `json:"repoPath"`
	Prompt    string `json:"prompt"`
	Phase     string `json:"phase"`
}

// startup is called by Wails when the application starts. It saves the
// runtime context so we can call Wails runtime methods later if needed.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	if a.sessionMgr != nil {
		a.sessionMgr.SetEmitFn(func(event string, data ...interface{}) {
			runtime.EventsEmit(ctx, event, data...)
		})
		a.sessionMgr.Start(ctx)
	}

	if a.scanner != nil {
		a.scanner.Start(ctx)
	}

	// Start the mobile API server.
	a.startMobileAPI()

	// Auto-start Jarvis if previously enabled. Must come after startMobileAPI
	// which calls config.Load() — without that, config.Get() returns defaults
	// with JarvisEnabled=false.
	cfg := config.Get()
	if cfg.JarvisEnabled {
		if err := a.StartJarvis(); err != nil {
			slog.Warn("jarvis: auto-start failed (can be started from settings)", "err", err)
		}
	}

	// Start the push notification poller (no-op if API server failed to start).
	if a.apiServer != nil {
		a.apiServer.StartPoller(ctx)
	}

	// Start the Jarvis context pusher — sends live session/cost/approval data to
	// the Python daemon every 5 seconds so the LLM has fresh context.
	if a.apiServer != nil && a.apiServer.JarvisConn() != nil {
		// Workspace name lookup: maps CWD -> CMux workspace title so Jarvis
		// knows sessions by their user-assigned names (e.g. "Auth Service").
		wsNameFn := func() map[string]string {
			if a.cmuxClient == nil || !a.cmuxClient.IsAvailable() {
				return nil
			}
			ws, err := a.cmuxClient.ListWorkspaces()
			if err != nil {
				return nil
			}
			m := make(map[string]string, len(ws))
			for _, w := range ws {
				m[w.CurrentDirectory] = w.Title
			}
			return m
		}

		// Impact warnings: cross-session conflict detection so Jarvis can
		// proactively alert the user about dependency/file/API conflicts.
		warningsFn := func() []api.JarvisWarning {
			warnings, err := a.GetImpactWarnings()
			if err != nil || len(warnings) == 0 {
				return nil
			}
			result := make([]api.JarvisWarning, 0, len(warnings))
			for _, w := range warnings {
				result = append(result, api.JarvisWarning{
					Type:    w.ConflictType,
					Message: w.Description,
				})
			}
			return result
		}

		api.StartContextPusher(ctx, a.apiServer.JarvisConn(), a, 5*time.Second, api.ContextPusherOpts{
			WorkspaceNames: wsNameFn,
			Warnings:       warningsFn,
		})
	}

	// Register the tool dispatcher so the daemon can execute actions via WS.
	api.SetJarvisToolDispatcher(a.dispatchJarvisTool)
}

// startMobileAPI loads the config, auto-generates a token if needed, and starts
// the embedded HTTP server for mobile clients. Failures are logged as warnings
// but do not prevent AWM from running.
func (a *App) startMobileAPI() {
	cfg, err := config.Load()
	if err != nil {
		slog.Warn("mobile API: failed to load config, skipping", "err", err)
		return
	}

	// Auto-generate a token on first use.
	if cfg.MobileAPIToken == "" {
		token, err := generateToken()
		if err != nil {
			slog.Warn("mobile API: failed to generate token, skipping", "err", err)
			return
		}
		cfg.MobileAPIToken = token
		if err := config.Save(cfg); err != nil {
			slog.Warn("mobile API: failed to save generated token", "err", err)
			// Continue anyway — the token is usable for this session.
		} else {
			slog.Info("mobile API: auto-generated Bearer token", "token_prefix", token[:8]+"...")
		}
	}

	srv := api.New(cfg.MobileAPIPort, cfg.MobileAPIToken)

	// Jarvis daemon events are forwarded to the Wails frontend via EventsEmit.
	// We also peek at the event to route the v0.3.0 meeting-mode
	// ``meeting_notes_written`` notification onto a.meetingNotesCh so the
	// StopMeeting() binding (TASK-009) can resolve. The forward-to-frontend
	// path is unchanged for every other event type.
	jarvisEmitFn := api.JarvisEventEmitter(func(event interface{}) {
		if payload, ok := event.(map[string]interface{}); ok {
			if t, _ := payload["type"].(string); t == "meeting_notes_written" {
				path, _ := payload["path"].(string)
				select {
				case a.meetingNotesCh <- path:
				default:
					// Channel full -- previous notification not consumed.
					// Drop rather than block the daemon WS read loop.
					slog.Warn("meeting_notes_written: dropping (channel full)", "path", path)
				}
			}
		}
		runtime.EventsEmit(a.ctx, "jarvis", event)
	})

	srv.WireRoutes(a, a, a, a, a, a, a, a, a.resolveProjectPath, jarvisEmitFn, a, a)
	if err := srv.Start(); err != nil {
		slog.Warn("mobile API: server failed to start", "err", err)
		return
	}
	a.apiServer = srv
}

// generateToken returns a 128-bit hex-encoded random token formatted as a UUID
// (xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx) using crypto/rand. No third-party
// UUID library is needed.
func generateToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("reading crypto/rand: %w", err)
	}
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// shutdown is called by Wails when the application is closing. It ensures all
// background tailers, the scanner, session manager, and mobile API server are
// stopped cleanly.
func (a *App) shutdown(ctx context.Context) {
	if a.apiServer != nil {
		if err := a.apiServer.Shutdown(ctx); err != nil {
			slog.Warn("mobile API: shutdown error", "err", err)
		}
	}
	a.StopJarvis()
	if a.jarvis != nil {
		a.jarvis.Stop()
		a.jarvis = nil
	}
	if a.sessionMgr != nil {
		a.sessionMgr.Cleanup()
	}
	if a.scanner != nil {
		a.scanner.Stop()
	}
	a.StopAllWatchers()
}

// ---------------------------------------------------------------------------
// Activity logging
// ---------------------------------------------------------------------------

// logActivity creates and persists an activity event. Errors are logged but
// not propagated — activity logging is fire-and-forget.
func (a *App) logActivity(taskID, taskName, eventType, message, metadata string) {
	event := model.NewActivityEvent(taskID, taskName, eventType, message, metadata)
	if err := a.store.CreateActivityEvent(event); err != nil {
		slog.Error("failed to log activity event", "err", err, "eventType", eventType, "taskID", taskID)
	}
}

// ---------------------------------------------------------------------------
// Query helpers
// ---------------------------------------------------------------------------

// GetTasks returns tasks, optionally filtered by status.
func (a *App) GetTasks(statusFilter string) ([]model.Task, error) {
	tasks, err := a.store.ListTasks(statusFilter, "")
	if err != nil {
		return nil, fmt.Errorf("GetTasks: %w", err)
	}
	// Wails serialises nil slices as null; return empty slice for cleaner JSON.
	if tasks == nil {
		tasks = []model.Task{}
	}
	return tasks, nil
}

// GetTask returns a single task by ID.
func (a *App) GetTask(id string) (model.Task, error) {
	if id == "" {
		return model.Task{}, fmt.Errorf("GetTask: id is required")
	}
	task, err := a.store.GetTask(id)
	if err != nil {
		return model.Task{}, fmt.Errorf("GetTask: %w", err)
	}
	return task, nil
}

// ---------------------------------------------------------------------------
// Mutations
// ---------------------------------------------------------------------------

// CreateTask validates inputs, builds a new Task, persists it, and returns it.
func (a *App) CreateTask(name, description, repoPath, agentType string) (model.Task, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return model.Task{}, fmt.Errorf("CreateTask: name is required")
	}

	repoPath = strings.TrimSpace(repoPath)
	if repoPath == "" {
		return model.Task{}, fmt.Errorf("CreateTask: repoPath is required")
	}

	at := model.AgentType(agentType)
	if !model.ValidAgentType(at) {
		return model.Task{}, fmt.Errorf("CreateTask: invalid agentType %q", agentType)
	}

	task := model.NewTask(name, description, repoPath, at)

	created, err := a.store.CreateTask(task)
	if err != nil {
		return model.Task{}, fmt.Errorf("CreateTask: %w", err)
	}

	a.logActivity(created.ID, created.Name, "created",
		fmt.Sprintf("Task '%s' created for %s", created.Name, created.RepoPath), "")

	return created, nil
}

// UpdateTaskStatus validates the new status string and applies it.
func (a *App) UpdateTaskStatus(id string, status string) (model.Task, error) {
	if id == "" {
		return model.Task{}, fmt.Errorf("UpdateTaskStatus: id is required")
	}

	s := model.Status(status)
	if !model.ValidStatus(s) {
		return model.Task{}, fmt.Errorf("UpdateTaskStatus: invalid status %q", status)
	}

	// Fetch the current task so we can log the transition.
	existing, err := a.store.GetTask(id)
	if err != nil {
		return model.Task{}, fmt.Errorf("UpdateTaskStatus: %w", err)
	}
	oldStatus := string(existing.Status)

	updated, err := a.store.UpdateTask(id, map[string]interface{}{
		"status": status,
	})
	if err != nil {
		return model.Task{}, fmt.Errorf("UpdateTaskStatus: %w", err)
	}

	// Log the status change activity event.
	a.logActivity(updated.ID, updated.Name, "status_changed",
		fmt.Sprintf("Task '%s' status: %s -> %s", updated.Name, oldStatus, status),
		fmt.Sprintf(`{"from":%q,"to":%q}`, oldStatus, status))

	// Send macOS notifications for terminal / attention-requiring statuses.
	switch model.Status(status) {
	case model.StatusDone:
		_ = notify.Send("Jarvis", fmt.Sprintf("Task '%s' completed", updated.Name))
	case model.StatusFailed:
		_ = notify.Send("Jarvis", fmt.Sprintf("Task '%s' failed", updated.Name))
	case model.StatusNeedsInput:
		_ = notify.Send("Jarvis", fmt.Sprintf("Task '%s' needs your input", updated.Name))
	}

	return updated, nil
}

// UpdateTaskOutputPath sets the output_path field on a task.
func (a *App) UpdateTaskOutputPath(id string, outputPath string) (model.Task, error) {
	if id == "" {
		return model.Task{}, fmt.Errorf("UpdateTaskOutputPath: id is required")
	}

	updated, err := a.store.UpdateTask(id, map[string]interface{}{
		"output_path": outputPath,
	})
	if err != nil {
		return model.Task{}, fmt.Errorf("UpdateTaskOutputPath: %w", err)
	}
	return updated, nil
}

// DeleteTask removes a task by ID.
func (a *App) DeleteTask(id string) error {
	if id == "" {
		return fmt.Errorf("DeleteTask: id is required")
	}

	// Fetch the task before deletion so we can log the event with its name.
	task, err := a.store.GetTask(id)
	if err != nil {
		return fmt.Errorf("DeleteTask: %w", err)
	}

	if err := a.store.DeleteTask(id); err != nil {
		return fmt.Errorf("DeleteTask: %w", err)
	}

	a.logActivity(task.ID, task.Name, "deleted",
		fmt.Sprintf("Task '%s' deleted", task.Name), "")

	return nil
}

// ---------------------------------------------------------------------------
// File output
// ---------------------------------------------------------------------------

// GetTaskOutput reads the task's output file and returns the last N lines.
// If the task has no OutputPath set, an error is returned.
func (a *App) GetTaskOutput(id string, lastN int) ([]string, error) {
	if id == "" {
		return nil, fmt.Errorf("GetTaskOutput: id is required")
	}

	task, err := a.store.GetTask(id)
	if err != nil {
		return nil, fmt.Errorf("GetTaskOutput: %w", err)
	}

	if task.OutputPath == "" {
		return nil, fmt.Errorf("GetTaskOutput: task %q has no output path", id)
	}

	// Validate the output path is within the allowed Jarvis data directory to
	// prevent arbitrary file reads via crafted OutputPath values.
	allowedPrefix := paths.JarvisHome()
	absPath, _ := filepath.Abs(task.OutputPath)
	if !strings.HasPrefix(absPath, allowedPrefix) {
		return nil, fmt.Errorf("GetTaskOutput: path outside allowed directory")
	}

	// Tail-read: only load the last 100 KB instead of the entire file.
	const maxTailBytes = 100 * 1024
	f, err := os.Open(task.OutputPath)
	if err != nil {
		return nil, fmt.Errorf("GetTaskOutput: opening file %q: %w", task.OutputPath, err)
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("GetTaskOutput: stat file %q: %w", task.OutputPath, err)
	}

	offset := int64(0)
	if stat.Size() > maxTailBytes {
		offset = stat.Size() - maxTailBytes
	}
	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return nil, fmt.Errorf("GetTaskOutput: seek file %q: %w", task.OutputPath, err)
		}
	}

	data, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("GetTaskOutput: reading file %q: %w", task.OutputPath, err)
	}

	lines := strings.Split(string(data), "\n")

	// Remove trailing empty line that Split produces for files ending with \n.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	if lastN <= 0 || lastN >= len(lines) {
		return lines, nil
	}

	return lines[len(lines)-lastN:], nil
}

// ---------------------------------------------------------------------------
// Live output streaming via Wails events
// ---------------------------------------------------------------------------

// WatchTaskOutput starts a live tail on the task's output file. Each new line
// is emitted as a Wails event named "output:<taskID>". If the watcher for this
// task is already active the call is a no-op (idempotent).
func (a *App) WatchTaskOutput(taskID string) error {
	if taskID == "" {
		return fmt.Errorf("WatchTaskOutput: taskID is required")
	}

	a.watcherMu.Lock()

	// Already watching -- nothing to do.
	if _, active := a.activeWatchers[taskID]; active {
		a.watcherMu.Unlock()
		return nil
	}

	task, err := a.store.GetTask(taskID)
	if err != nil {
		a.watcherMu.Unlock()
		return fmt.Errorf("WatchTaskOutput: %w", err)
	}

	if task.OutputPath == "" {
		a.watcherMu.Unlock()
		return fmt.Errorf("WatchTaskOutput: task %q has no output path", taskID)
	}

	childCtx, cancel := context.WithCancel(a.ctx)
	a.activeWatchers[taskID] = cancel
	a.watcherMu.Unlock()

	go a.runWatcher(childCtx, taskID, task.OutputPath)

	return nil
}

// runWatcher is the goroutine that drives a single tailer. It emits the
// initial buffer of historical lines followed by live lines as Wails events.
// When the tailer channel closes or the context is cancelled the goroutine
// removes itself from activeWatchers.
func (a *App) runWatcher(ctx context.Context, taskID, outputPath string) {
	eventName := "output:" + taskID

	defer func() {
		a.watcherMu.Lock()
		delete(a.activeWatchers, taskID)
		a.watcherMu.Unlock()
	}()

	tailer := watcher.NewTailer()

	ch, err := tailer.Start(ctx, outputPath)
	if err != nil {
		// Emit the error so the frontend knows something went wrong.
		runtime.EventsEmit(a.ctx, eventName, "[ERROR] "+err.Error())
		return
	}

	// Emit the initial buffer so the frontend gets historical lines.
	for _, line := range tailer.LastLines(100) {
		runtime.EventsEmit(a.ctx, eventName, line)
	}

	// Stream live lines until the channel closes or the context is done.
	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-ch:
			if !ok {
				return
			}
			runtime.EventsEmit(a.ctx, eventName, line)
		}
	}
}

// StopWatchingOutput cancels the live tailer for the given task, if any.
func (a *App) StopWatchingOutput(taskID string) {
	a.watcherMu.Lock()
	defer a.watcherMu.Unlock()

	if cancel, ok := a.activeWatchers[taskID]; ok {
		cancel()
		delete(a.activeWatchers, taskID)
	}
}

// StopAllWatchers cancels every active tailer.
func (a *App) StopAllWatchers() {
	a.watcherMu.Lock()
	defer a.watcherMu.Unlock()

	for id, cancel := range a.activeWatchers {
		cancel()
		delete(a.activeWatchers, id)
	}
}

// ---------------------------------------------------------------------------
// Process scanner
// ---------------------------------------------------------------------------

// ScanNow triggers an immediate process scan and reconciliation. It returns the
// number of newly created tasks.
func (a *App) ScanNow() (int, error) {
	if a.scanner == nil {
		return 0, fmt.Errorf("ScanNow: scanner is not enabled")
	}
	n, err := a.scanner.Reconcile(a.ctx)
	if err != nil {
		return 0, fmt.Errorf("ScanNow: %w", err)
	}
	return n, nil
}

// GetAutoDetectedCount returns the number of tasks that were created by the
// automatic process scanner (identified by the "[auto-detected]" description
// prefix).
func (a *App) GetAutoDetectedCount() (int, error) {
	count, err := a.store.CountAutoDetected()
	if err != nil {
		return 0, fmt.Errorf("GetAutoDetectedCount: %w", err)
	}
	return count, nil
}

// ---------------------------------------------------------------------------
// Workflows
// ---------------------------------------------------------------------------

// CreateWorkflow creates a new workflow with the given name and description.
func (a *App) CreateWorkflow(name, description string) (model.Workflow, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return model.Workflow{}, fmt.Errorf("CreateWorkflow: name is required")
	}

	wf := model.NewWorkflow(name, description)

	created, err := a.store.CreateWorkflow(wf)
	if err != nil {
		return model.Workflow{}, fmt.Errorf("CreateWorkflow: %w", err)
	}
	return created, nil
}

// GetWorkflows returns all workflows.
func (a *App) GetWorkflows() ([]model.Workflow, error) {
	workflows, err := a.store.ListWorkflows()
	if err != nil {
		return nil, fmt.Errorf("GetWorkflows: %w", err)
	}
	// Wails serialises nil slices as null; return empty slice for cleaner JSON.
	if workflows == nil {
		workflows = []model.Workflow{}
	}
	return workflows, nil
}

// DeleteWorkflow removes a workflow by ID and unlinks any associated tasks.
func (a *App) DeleteWorkflow(id string) error {
	if id == "" {
		return fmt.Errorf("DeleteWorkflow: id is required")
	}
	if err := a.store.DeleteWorkflow(id); err != nil {
		return fmt.Errorf("DeleteWorkflow: %w", err)
	}
	return nil
}

// AddTaskToWorkflow links a task to a workflow by setting its workflow_id.
func (a *App) AddTaskToWorkflow(taskID, workflowID string) (model.Task, error) {
	if taskID == "" {
		return model.Task{}, fmt.Errorf("AddTaskToWorkflow: taskID is required")
	}
	if workflowID == "" {
		return model.Task{}, fmt.Errorf("AddTaskToWorkflow: workflowID is required")
	}

	// Verify the workflow exists.
	if _, err := a.store.GetWorkflow(workflowID); err != nil {
		return model.Task{}, fmt.Errorf("AddTaskToWorkflow: %w", err)
	}

	updated, err := a.store.UpdateTask(taskID, map[string]interface{}{
		"workflow_id": workflowID,
	})
	if err != nil {
		return model.Task{}, fmt.Errorf("AddTaskToWorkflow: %w", err)
	}
	return updated, nil
}

// RemoveTaskFromWorkflow unlinks a task from its workflow by clearing
// workflow_id.
func (a *App) RemoveTaskFromWorkflow(taskID string) (model.Task, error) {
	if taskID == "" {
		return model.Task{}, fmt.Errorf("RemoveTaskFromWorkflow: taskID is required")
	}

	updated, err := a.store.UpdateTask(taskID, map[string]interface{}{
		"workflow_id": "",
	})
	if err != nil {
		return model.Task{}, fmt.Errorf("RemoveTaskFromWorkflow: %w", err)
	}
	return updated, nil
}

// GetWorkflowTasks returns all tasks linked to the given workflow.
func (a *App) GetWorkflowTasks(workflowID string) ([]model.Task, error) {
	if workflowID == "" {
		return nil, fmt.Errorf("GetWorkflowTasks: workflowID is required")
	}

	tasks, err := a.store.GetWorkflowTasks(workflowID)
	if err != nil {
		return nil, fmt.Errorf("GetWorkflowTasks: %w", err)
	}
	if tasks == nil {
		tasks = []model.Task{}
	}
	return tasks, nil
}

// GetDashboardStats returns aggregate task counts grouped by status.
func (a *App) GetDashboardStats() (model.DashboardStats, error) {
	stats, err := a.store.GetDashboardStats()
	if err != nil {
		return model.DashboardStats{}, fmt.Errorf("GetDashboardStats: %w", err)
	}
	return stats, nil
}

// GetRunningTasks returns tasks in the running or needs-input state, used by
// the mobile API dashboard to show active work items.
func (a *App) GetRunningTasks() ([]model.Task, error) {
	tasks, err := a.store.ListTasks(string(model.StatusRunning), "")
	if err != nil {
		return nil, fmt.Errorf("GetRunningTasks: %w", err)
	}
	if tasks == nil {
		tasks = []model.Task{}
	}
	return tasks, nil
}

// GetTasksGroupedByRepo returns active tasks grouped by repository path.
// Only shows running, pending, needs-input, and failed tasks — not completed
// historical sessions. This prevents duplicate entries per repo.
func (a *App) GetTasksGroupedByRepo() (map[string][]model.Task, error) {
	allTasks, err := a.store.ListTasks("", "")
	if err != nil {
		return nil, fmt.Errorf("GetTasksGroupedByRepo: %w", err)
	}

	grouped := make(map[string][]model.Task)
	for _, t := range allTasks {
		// Skip completed/done tasks — they're history, not active work.
		if t.Status == model.StatusDone {
			continue
		}
		grouped[t.RepoPath] = append(grouped[t.RepoPath], t)
	}
	return grouped, nil
}

// ---------------------------------------------------------------------------
// Activity feed
// ---------------------------------------------------------------------------

// GetActivityFeed returns the most recent activity events. If beforeID is
// non-empty, only events older than that event are returned (cursor-based
// pagination for infinite scroll).
func (a *App) GetActivityFeed(limit int, beforeID string) ([]model.ActivityEvent, error) {
	events, err := a.store.ListActivityEvents(limit, beforeID)
	if err != nil {
		return nil, fmt.Errorf("GetActivityFeed: %w", err)
	}
	if events == nil {
		events = []model.ActivityEvent{}
	}
	return events, nil
}

// GetTaskActivity returns activity events for a specific task.
func (a *App) GetTaskActivity(taskID string, limit int) ([]model.ActivityEvent, error) {
	if taskID == "" {
		return nil, fmt.Errorf("GetTaskActivity: taskID is required")
	}
	events, err := a.store.ListTaskActivityEvents(taskID, limit)
	if err != nil {
		return nil, fmt.Errorf("GetTaskActivity: %w", err)
	}
	if events == nil {
		events = []model.ActivityEvent{}
	}
	return events, nil
}

// SearchOutput searches across all task output files for the given query string
// (case-insensitive). Returns up to 100 matching lines.
func (a *App) SearchOutput(query string) ([]store.OutputSearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return []store.OutputSearchResult{}, nil
	}
	results, err := a.store.SearchOutput(query, 100)
	if err != nil {
		return nil, fmt.Errorf("SearchOutput: %w", err)
	}
	if results == nil {
		results = []store.OutputSearchResult{}
	}
	return results, nil
}

// ---------------------------------------------------------------------------
// Git integration
// ---------------------------------------------------------------------------

// GetRepoInfo returns git repository metadata for the given path.
func (a *App) GetRepoInfo(repoPath string) (git.RepoInfo, error) {
	repoPath = strings.TrimSpace(repoPath)
	if repoPath == "" {
		return git.RepoInfo{}, fmt.Errorf("GetRepoInfo: repoPath is required")
	}

	info, err := git.GetRepoInfo(repoPath)
	if err != nil {
		return git.RepoInfo{}, fmt.Errorf("GetRepoInfo: %w", err)
	}
	return info, nil
}

// GetTaskGitInfo retrieves the task by ID and returns git repository metadata
// for the task's RepoPath.
func (a *App) GetTaskGitInfo(taskID string) (git.RepoInfo, error) {
	if taskID == "" {
		return git.RepoInfo{}, fmt.Errorf("GetTaskGitInfo: taskID is required")
	}

	task, err := a.store.GetTask(taskID)
	if err != nil {
		return git.RepoInfo{}, fmt.Errorf("GetTaskGitInfo: %w", err)
	}

	if task.RepoPath == "" {
		return git.RepoInfo{}, fmt.Errorf("GetTaskGitInfo: task %q has no repo path", taskID)
	}

	info, err := git.GetRepoInfo(task.RepoPath)
	if err != nil {
		return git.RepoInfo{}, fmt.Errorf("GetTaskGitInfo: %w", err)
	}
	return info, nil
}

// ---------------------------------------------------------------------------
// Session management
// ---------------------------------------------------------------------------

// LaunchSession starts a new AI agent session for the given agent type, repo
// path, and prompt. The session is persisted and the agent process is started.
func (a *App) LaunchSession(agentType, repoPath, prompt string) (model.Session, error) {
	agentType = strings.TrimSpace(agentType)
	if agentType == "" {
		return model.Session{}, fmt.Errorf("LaunchSession: agentType is required")
	}
	repoPath = strings.TrimSpace(repoPath)
	if repoPath == "" {
		return model.Session{}, fmt.Errorf("LaunchSession: repoPath is required")
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return model.Session{}, fmt.Errorf("LaunchSession: prompt is required")
	}
	if a.sessionMgr == nil {
		return model.Session{}, fmt.Errorf("LaunchSession: session manager is not enabled")
	}

	sess, err := a.sessionMgr.Launch(model.AgentType(agentType), repoPath, prompt)
	if err != nil {
		return model.Session{}, fmt.Errorf("LaunchSession: %w", err)
	}

	a.logActivity(sess.ID, string(sess.AgentType), "session_launched",
		fmt.Sprintf("Session launched for %s in %s", sess.AgentType, sess.RepoPath), "")

	return sess, nil
}

// QueueSession schedules a session with dependency-aware execution. If the
// session has dependencies (dependsOn), it waits for all of them to complete
// before launching. Sessions without dependencies launch immediately. The
// phase parameter is optional metadata ("plan", "build", "review", "test", "").
func (a *App) QueueSession(agentType, repoPath, prompt string, dependsOn []string, phase string) (model.Session, error) {
	agentType = strings.TrimSpace(agentType)
	if agentType == "" {
		return model.Session{}, fmt.Errorf("QueueSession: agentType is required")
	}
	repoPath = strings.TrimSpace(repoPath)
	if repoPath == "" {
		return model.Session{}, fmt.Errorf("QueueSession: repoPath is required")
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return model.Session{}, fmt.Errorf("QueueSession: prompt is required")
	}
	if a.sessionMgr == nil {
		return model.Session{}, fmt.Errorf("QueueSession: session manager is not enabled")
	}

	// Ensure dependsOn is non-nil.
	if dependsOn == nil {
		dependsOn = []string{}
	}

	sess := model.NewSession(model.AgentType(agentType), repoPath, prompt)
	sess.DependsOn = dependsOn
	sess.Phase = strings.TrimSpace(phase)

	result, err := a.sessionMgr.QueueSession(sess)
	if err != nil {
		return model.Session{}, fmt.Errorf("QueueSession: %w", err)
	}

	eventType := "session_queued"
	msg := fmt.Sprintf("Session queued for %s in %s", result.AgentType, result.RepoPath)
	if result.Status != model.SessionQueued {
		eventType = "session_launched"
		msg = fmt.Sprintf("Session launched for %s in %s (all deps satisfied)", result.AgentType, result.RepoPath)
	}
	a.logActivity(result.ID, string(result.AgentType), eventType, msg, "")

	return result, nil
}

// SendSessionMessage delivers a follow-up message to a running session.
func (a *App) SendSessionMessage(sessionID, message string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return fmt.Errorf("SendSessionMessage: sessionID is required")
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return fmt.Errorf("SendSessionMessage: message is required")
	}
	if a.sessionMgr == nil {
		return fmt.Errorf("SendSessionMessage: session manager is not enabled")
	}

	if err := a.sessionMgr.SendMessage(sessionID, message); err != nil {
		return fmt.Errorf("SendSessionMessage: %w", err)
	}
	return nil
}

// StopSession gracefully terminates a running session.
func (a *App) StopSession(sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return fmt.Errorf("StopSession: sessionID is required")
	}
	if a.sessionMgr == nil {
		return fmt.Errorf("StopSession: session manager is not enabled")
	}

	if err := a.sessionMgr.Stop(sessionID); err != nil {
		return fmt.Errorf("StopSession: %w", err)
	}

	a.logActivity(sessionID, "", "session_stopped",
		fmt.Sprintf("Session %s stopped", sessionID), "")

	return nil
}

// ResumeSession restarts a previously completed or paused session.
func (a *App) ResumeSession(sessionID string) (model.Session, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return model.Session{}, fmt.Errorf("ResumeSession: sessionID is required")
	}
	if a.sessionMgr == nil {
		return model.Session{}, fmt.Errorf("ResumeSession: session manager is not enabled")
	}

	sess, err := a.sessionMgr.Resume(sessionID)
	if err != nil {
		return model.Session{}, fmt.Errorf("ResumeSession: %w", err)
	}

	a.logActivity(sess.ID, string(sess.AgentType), "session_resumed",
		fmt.Sprintf("Session %s resumed", sess.ID), "")

	return sess, nil
}

// GetSession returns a single session by ID.
func (a *App) GetSession(sessionID string) (model.Session, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return model.Session{}, fmt.Errorf("GetSession: sessionID is required")
	}

	sess, err := a.store.GetSession(sessionID)
	if err != nil {
		return model.Session{}, fmt.Errorf("GetSession: %w", err)
	}
	return sess, nil
}

// ListSessions returns sessions, optionally filtered by status.
func (a *App) ListSessions(statusFilter string) ([]model.Session, error) {
	sessions, err := a.store.ListSessions(statusFilter)
	if err != nil {
		return nil, fmt.Errorf("ListSessions: %w", err)
	}
	if sessions == nil {
		sessions = []model.Session{}
	}
	return sessions, nil
}

// GetActiveSessions returns all sessions that are currently running or
// launching.
func (a *App) GetActiveSessions() ([]model.Session, error) {
	sessions, err := a.store.GetActiveSessions()
	if err != nil {
		return nil, fmt.Errorf("GetActiveSessions: %w", err)
	}
	if sessions == nil {
		sessions = []model.Session{}
	}
	return sessions, nil
}

// GetAvailableAgents returns information about all registered agent adapters,
// including whether each agent's CLI is installed.
func (a *App) GetAvailableAgents() ([]agent.AgentInfo, error) {
	if a.sessionMgr == nil {
		return []agent.AgentInfo{}, nil
	}
	agents := a.sessionMgr.GetAvailableAgents()
	if agents == nil {
		agents = []agent.AgentInfo{}
	}
	return agents, nil
}

// DeleteSession removes a session by ID.
func (a *App) DeleteSession(sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return fmt.Errorf("DeleteSession: sessionID is required")
	}

	if err := a.store.DeleteSession(sessionID); err != nil {
		return fmt.Errorf("DeleteSession: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Session groups
// ---------------------------------------------------------------------------

// CreateSessionGroup creates a new session group.
func (a *App) CreateSessionGroup(name, description, color string) (model.SessionGroup, error) {
	g := model.NewSessionGroup(name, description, color)
	group, err := a.store.CreateSessionGroup(g)
	if err != nil {
		return model.SessionGroup{}, fmt.Errorf("CreateSessionGroup: %w", err)
	}
	return group, nil
}

// ListSessionGroups returns all session groups.
func (a *App) ListSessionGroups() ([]model.SessionGroup, error) {
	groups, err := a.store.ListSessionGroups()
	if err != nil {
		return nil, err
	}
	if groups == nil {
		groups = []model.SessionGroup{}
	}
	return groups, nil
}

// DeleteSessionGroup deletes a session group.
func (a *App) DeleteSessionGroup(id string) error {
	if err := a.store.DeleteSessionGroup(id); err != nil {
		return fmt.Errorf("DeleteSessionGroup: %w", err)
	}
	return nil
}

// AddToSessionGroup adds a repo path to a group.
func (a *App) AddToSessionGroup(groupID, repoPath string) error {
	if err := a.store.AddToGroup(groupID, repoPath); err != nil {
		return fmt.Errorf("AddToSessionGroup: %w", err)
	}
	return nil
}

// RemoveFromSessionGroup removes a repo path from a group.
func (a *App) RemoveFromSessionGroup(groupID, repoPath string) error {
	if err := a.store.RemoveFromGroup(groupID, repoPath); err != nil {
		return fmt.Errorf("RemoveFromSessionGroup: %w", err)
	}
	return nil
}

// GetSessionGroupMembers returns members of a group.
func (a *App) GetSessionGroupMembers(groupID string) ([]model.GroupMember, error) {
	members, err := a.store.GetGroupMembers(groupID)
	if err != nil {
		return nil, fmt.Errorf("GetSessionGroupMembers: %w", err)
	}
	if members == nil {
		members = []model.GroupMember{}
	}
	return members, nil
}

// ---------------------------------------------------------------------------
// Session todos
// ---------------------------------------------------------------------------

// GetSessionTodos returns all todos for a session.
func (a *App) GetSessionTodos(sessionID string) ([]model.SessionTodo, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return []model.SessionTodo{}, fmt.Errorf("GetSessionTodos: sessionID is required")
	}
	todos, err := a.store.ListSessionTodos(sessionID)
	if err != nil {
		return []model.SessionTodo{}, fmt.Errorf("GetSessionTodos: %w", err)
	}
	if todos == nil {
		todos = []model.SessionTodo{}
	}
	return todos, nil
}

// CreateSessionTodo creates a new todo for a session.
func (a *App) CreateSessionTodo(sessionID, title string) (model.SessionTodo, error) {
	sessionID = strings.TrimSpace(sessionID)
	title = strings.TrimSpace(title)
	if sessionID == "" {
		return model.SessionTodo{}, fmt.Errorf("CreateSessionTodo: sessionID is required")
	}
	if title == "" {
		return model.SessionTodo{}, fmt.Errorf("CreateSessionTodo: title is required")
	}
	todo, err := a.store.CreateSessionTodo(sessionID, title)
	if err != nil {
		return model.SessionTodo{}, fmt.Errorf("CreateSessionTodo: %w", err)
	}
	return todo, nil
}

// UpdateSessionTodo updates a session todo's status.
func (a *App) UpdateSessionTodo(id, status string) error {
	id = strings.TrimSpace(id)
	status = strings.TrimSpace(status)
	if id == "" {
		return fmt.Errorf("UpdateSessionTodo: id is required")
	}
	if err := a.store.UpdateSessionTodo(id, status); err != nil {
		return fmt.Errorf("UpdateSessionTodo: %w", err)
	}
	return nil
}

// DeleteSessionTodo deletes a session todo by ID.
func (a *App) DeleteSessionTodo(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("DeleteSessionTodo: id is required")
	}
	if err := a.store.DeleteSessionTodo(id); err != nil {
		return fmt.Errorf("DeleteSessionTodo: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Diff viewer
// ---------------------------------------------------------------------------

// GetRepoDiff returns a parsed unified diff for the given repo path
// (unstaged + staged changes vs HEAD).
func (a *App) GetRepoDiff(repoPath string) (git.DiffResult, error) {
	repoPath = strings.TrimSpace(repoPath)
	if repoPath == "" {
		return git.DiffResult{}, fmt.Errorf("GetRepoDiff: repoPath is required")
	}

	diff, err := git.GetDiff(repoPath)
	if err != nil {
		return git.DiffResult{}, fmt.Errorf("GetRepoDiff: %w", err)
	}
	return diff, nil
}

// GetTaskDiff looks up a task by ID and returns the parsed diff for its repo.
func (a *App) GetTaskDiff(taskID string) (git.DiffResult, error) {
	if taskID == "" {
		return git.DiffResult{}, fmt.Errorf("GetTaskDiff: taskID is required")
	}

	task, err := a.store.GetTask(taskID)
	if err != nil {
		return git.DiffResult{}, fmt.Errorf("GetTaskDiff: %w", err)
	}

	if task.RepoPath == "" {
		return git.DiffResult{}, fmt.Errorf("GetTaskDiff: task %q has no repo path", taskID)
	}

	diff, err := git.GetDiff(task.RepoPath)
	if err != nil {
		return git.DiffResult{}, fmt.Errorf("GetTaskDiff: %w", err)
	}
	return diff, nil
}

// GetStagedDiff returns a parsed unified diff for staged changes only.
func (a *App) GetStagedDiff(repoPath string) (git.DiffResult, error) {
	repoPath = strings.TrimSpace(repoPath)
	if repoPath == "" {
		return git.DiffResult{}, fmt.Errorf("GetStagedDiff: repoPath is required")
	}

	diff, err := git.GetStagedDiff(repoPath)
	if err != nil {
		return git.DiffResult{}, fmt.Errorf("GetStagedDiff: %w", err)
	}
	return diff, nil
}

// GetSessionDiff returns the cumulative diff for a session since it started.
// It finds the commit closest to the session start time, then computes
// git diff from that commit to the current state (including unstaged changes).
func (a *App) GetSessionDiff(pid int) (git.DiffResult, error) {
	empty := git.DiffResult{Files: []git.FileDiff{}}

	// 1. Look up the Claude session by PID.
	sess, err := claude.GetSession(pid)
	if err != nil {
		// Session not found or unreadable — return empty diff, not an error.
		slog.Warn("GetSessionDiff: session not found", "pid", pid, "err", err)
		return empty, nil
	}

	cwd := strings.TrimSpace(sess.CWD)
	if cwd == "" {
		return empty, fmt.Errorf("GetSessionDiff: session %d has no CWD", pid)
	}

	if !git.IsGitRepo(cwd) {
		return empty, fmt.Errorf("GetSessionDiff: session CWD %q is not a git repository", cwd)
	}

	// 2. Find the base commit at or before the session start time.
	startTime := sess.StartedAtTime()
	baseCommit, err := git.FindCommitBefore(cwd, startTime)
	if err != nil {
		return empty, fmt.Errorf("GetSessionDiff: finding base commit: %w", err)
	}

	// 3. Compute cumulative diff.
	// If baseCommit is empty (new repo or no commits before session start),
	// GetCumulativeDiff falls back to `git diff HEAD`.
	diff, err := git.GetCumulativeDiff(cwd, baseCommit)
	if err != nil {
		return empty, fmt.Errorf("GetSessionDiff: computing diff: %w", err)
	}

	return diff, nil
}

// ---------------------------------------------------------------------------
// Git actions
// ---------------------------------------------------------------------------

// GitStageAll stages all changes in the given repo.
func (a *App) GitStageAll(repoPath string) error {
	repoPath = strings.TrimSpace(repoPath)
	if repoPath == "" {
		return fmt.Errorf("GitStageAll: repoPath is required")
	}
	return git.StageAll(repoPath)
}

// GitStageFiles stages specific files in the given repo.
func (a *App) GitStageFiles(repoPath string, files []string) error {
	repoPath = strings.TrimSpace(repoPath)
	if repoPath == "" {
		return fmt.Errorf("GitStageFiles: repoPath is required")
	}
	if len(files) == 0 {
		return fmt.Errorf("GitStageFiles: files list is empty")
	}
	return git.StageFiles(repoPath, files)
}

// GitCommit creates a commit with the given message and logs an activity event.
func (a *App) GitCommit(repoPath, message string) error {
	repoPath = strings.TrimSpace(repoPath)
	if repoPath == "" {
		return fmt.Errorf("GitCommit: repoPath is required")
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return fmt.Errorf("GitCommit: message is required")
	}

	if err := git.Commit(repoPath, message); err != nil {
		return fmt.Errorf("GitCommit: %w", err)
	}

	a.logActivity("", "", "git_commit",
		fmt.Sprintf("Committed to %s: %s", repoPath, message), "")

	return nil
}

// GitPush pushes commits to the remote and logs an activity event.
func (a *App) GitPush(repoPath string) error {
	repoPath = strings.TrimSpace(repoPath)
	if repoPath == "" {
		return fmt.Errorf("GitPush: repoPath is required")
	}

	if err := git.Push(repoPath); err != nil {
		return fmt.Errorf("GitPush: %w", err)
	}

	a.logActivity("", "", "git_push",
		fmt.Sprintf("Pushed changes from %s", repoPath), "")

	return nil
}

// GitCreateBranch creates and checks out a new branch in the given repo.
func (a *App) GitCreateBranch(repoPath, name string) error {
	repoPath = strings.TrimSpace(repoPath)
	if repoPath == "" {
		return fmt.Errorf("GitCreateBranch: repoPath is required")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("GitCreateBranch: name is required")
	}

	if err := git.CreateBranch(repoPath, name); err != nil {
		return fmt.Errorf("GitCreateBranch: %w", err)
	}

	a.logActivity("", "", "git_branch_created",
		fmt.Sprintf("Created branch '%s' in %s", name, repoPath), "")

	return nil
}

// OpenPRInBrowser constructs a PR creation URL from the repo's remote and
// current branch, then opens it in the default browser.
func (a *App) OpenPRInBrowser(repoPath string) error {
	repoPath = strings.TrimSpace(repoPath)
	if repoPath == "" {
		return fmt.Errorf("OpenPRInBrowser: repoPath is required")
	}

	prURL, err := git.GetPRCreationURL(repoPath)
	if err != nil {
		return fmt.Errorf("OpenPRInBrowser: %w", err)
	}

	runtime.BrowserOpenURL(a.ctx, prURL)
	return nil
}

// ---------------------------------------------------------------------------
// Git stash & discard
// ---------------------------------------------------------------------------

// GitStash creates a new AWM-prefixed stash entry.
func (a *App) GitStash(repoPath, name string) error {
	repoPath = strings.TrimSpace(repoPath)
	name = strings.TrimSpace(name)
	if repoPath == "" {
		return fmt.Errorf("GitStash: repoPath is required")
	}
	if name == "" {
		return fmt.Errorf("GitStash: name is required")
	}
	return git.Stash(repoPath, name)
}

// GitStashList returns all AWM-prefixed stash entries for the given repo.
func (a *App) GitStashList(repoPath string) ([]git.StashEntry, error) {
	repoPath = strings.TrimSpace(repoPath)
	if repoPath == "" {
		return []git.StashEntry{}, fmt.Errorf("GitStashList: repoPath is required")
	}
	entries, err := git.StashList(repoPath)
	if err != nil {
		return []git.StashEntry{}, fmt.Errorf("GitStashList: %w", err)
	}
	if entries == nil {
		entries = []git.StashEntry{}
	}
	return entries, nil
}

// GitStashApply applies a stash entry by index without removing it.
func (a *App) GitStashApply(repoPath string, index int) error {
	repoPath = strings.TrimSpace(repoPath)
	if repoPath == "" {
		return fmt.Errorf("GitStashApply: repoPath is required")
	}
	return git.StashApply(repoPath, index)
}

// GitStashDrop removes a stash entry by index.
func (a *App) GitStashDrop(repoPath string, index int) error {
	repoPath = strings.TrimSpace(repoPath)
	if repoPath == "" {
		return fmt.Errorf("GitStashDrop: repoPath is required")
	}
	return git.StashDrop(repoPath, index)
}

// GitDiscardFile reverts a single file to its last committed state.
func (a *App) GitDiscardFile(repoPath, filePath string) error {
	repoPath = strings.TrimSpace(repoPath)
	filePath = strings.TrimSpace(filePath)
	if repoPath == "" {
		return fmt.Errorf("GitDiscardFile: repoPath is required")
	}
	if filePath == "" {
		return fmt.Errorf("GitDiscardFile: filePath is required")
	}
	return git.DiscardFile(repoPath, filePath)
}

// ---------------------------------------------------------------------------
// System operations (macOS)
// ---------------------------------------------------------------------------

// OpenApp activates a macOS application by name using AppleScript.
func (a *App) OpenApp(appName string) error {
	appName = strings.TrimSpace(appName)
	if appName == "" {
		return fmt.Errorf("OpenApp: app name is required")
	}
	appName = sanitizeForAppleScript(appName)
	cmd := exec.Command("osascript", "-e",
		fmt.Sprintf(`tell application "%s" to activate`, appName))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("OpenApp: failed to activate %s: %w", appName, err)
	}
	return nil
}

// OpenURL opens a URL in the default browser. Only http and https URLs are
// permitted to prevent arbitrary scheme execution.
func (a *App) OpenURL(urlStr string) error {
	urlStr = strings.TrimSpace(urlStr)
	if urlStr == "" {
		return fmt.Errorf("OpenURL: URL is required")
	}
	parsed, err := url.Parse(urlStr)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("OpenURL: only http and https URLs are allowed")
	}
	cmd := exec.Command("open", urlStr)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("OpenURL: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Workflow suggestions
// ---------------------------------------------------------------------------

// WorkflowSuggestion represents a suggested workflow grouping based on tasks
// that share a common parent directory.
type WorkflowSuggestion struct {
	Name    string   `json:"name"`
	RepoDir string   `json:"repoDir"`
	TaskIDs []string `json:"taskIds"`
}

// SuggestWorkflows queries all running tasks, groups them by common parent
// directory (2 levels up from their repo path), and returns suggestions for
// groups of 2 or more tasks.
func (a *App) SuggestWorkflows() ([]WorkflowSuggestion, error) {
	tasks, err := a.store.ListTasks(string(model.StatusRunning), "")
	if err != nil {
		return nil, fmt.Errorf("SuggestWorkflows: %w", err)
	}

	// Group tasks by the directory two levels up from their repo path.
	groups := make(map[string][]model.Task)
	for _, t := range tasks {
		if t.RepoPath == "" {
			continue
		}
		// Go two levels up: /a/b/c/repo -> /a/b/c
		parent := filepath.Dir(filepath.Dir(t.RepoPath))
		groups[parent] = append(groups[parent], t)
	}

	suggestions := []WorkflowSuggestion{}
	for dir, groupTasks := range groups {
		if len(groupTasks) < 2 {
			continue
		}

		ids := make([]string, len(groupTasks))
		for i, t := range groupTasks {
			ids[i] = t.ID
		}

		suggestions = append(suggestions, WorkflowSuggestion{
			Name:    fmt.Sprintf("Workflow for %s", filepath.Base(dir)),
			RepoDir: dir,
			TaskIDs: ids,
		})
	}

	return suggestions, nil
}

// ---------------------------------------------------------------------------
// CMux integration
// ---------------------------------------------------------------------------

// IsCMuxAvailable returns true if CMux is installed and accessible.
func (a *App) IsCMuxAvailable() bool {
	return a.cmuxClient != nil && a.cmuxClient.IsAvailable()
}

// GetCMuxWorkspaces returns all CMux workspaces.
func (a *App) GetCMuxWorkspaces() ([]cmux.Workspace, error) {
	if a.cmuxClient == nil {
		return nil, fmt.Errorf("CMux is not available")
	}
	ws, err := a.cmuxClient.ListWorkspaces()
	if err != nil {
		return nil, fmt.Errorf("GetCMuxWorkspaces: %w", err)
	}
	if ws == nil {
		ws = []cmux.Workspace{}
	}
	return ws, nil
}

// GetCMuxSurfaces returns all CMux terminal surfaces.
func (a *App) GetCMuxSurfaces() ([]cmux.Surface, error) {
	if a.cmuxClient == nil {
		return nil, fmt.Errorf("CMux is not available")
	}
	s, err := a.cmuxClient.ListSurfaces()
	if err != nil {
		return nil, fmt.Errorf("GetCMuxSurfaces: %w", err)
	}
	if s == nil {
		s = []cmux.Surface{}
	}
	return s, nil
}

// SendToCMux sends text to a CMux terminal surface.
func (a *App) SendToCMux(surfaceRef, text string) error {
	if a.cmuxClient == nil {
		return fmt.Errorf("CMux is not available")
	}
	if surfaceRef == "" {
		return fmt.Errorf("SendToCMux: surfaceRef is required")
	}
	return a.cmuxClient.SendText(surfaceRef, text)
}

// ReadFromCMux reads text from a CMux terminal surface.
func (a *App) ReadFromCMux(surfaceRef string) (string, error) {
	if a.cmuxClient == nil {
		return "", fmt.Errorf("CMux is not available")
	}
	if surfaceRef == "" {
		return "", fmt.Errorf("ReadFromCMux: surfaceRef is required")
	}
	return a.cmuxClient.ReadText(surfaceRef)
}

// FocusCMuxSurface switches CMux to focus a specific terminal surface.
func (a *App) FocusCMuxSurface(surfaceRef string) error {
	if a.cmuxClient == nil {
		return fmt.Errorf("CMux is not available")
	}
	if surfaceRef == "" {
		return fmt.Errorf("FocusCMuxSurface: surfaceRef is required")
	}
	return a.cmuxClient.FocusSurface(surfaceRef)
}

// ---------------------------------------------------------------------------
// Unified terminal methods (CMux + iTerm2 + Terminal.app)
// ---------------------------------------------------------------------------

// GetTerminalWindows returns all terminal windows across every available
// terminal application (CMux, iTerm2, Terminal.app).
func (a *App) GetTerminalWindows() ([]terminal.TerminalWindow, error) {
	if a.termMgr == nil {
		return []terminal.TerminalWindow{}, nil
	}
	windows, err := a.termMgr.ListAllWindows()
	if err != nil {
		return nil, fmt.Errorf("GetTerminalWindows: %w", err)
	}
	return windows, nil
}

// SendToTerminal sends text to a terminal window identified by windowID. The
// manager routes the command to the correct terminal application automatically.
func (a *App) SendToTerminal(windowID, text string) error {
	if a.termMgr == nil {
		return fmt.Errorf("no terminal providers available")
	}
	return a.termMgr.SendText(windowID, text)
}

// ReadFromTerminal reads the visible text from a terminal window identified by
// windowID.
func (a *App) ReadFromTerminal(windowID string) (string, error) {
	if a.termMgr == nil {
		return "", fmt.Errorf("no terminal providers available")
	}
	return a.termMgr.ReadText(windowID)
}

// FocusTerminalWindow brings the terminal window identified by windowID to the
// foreground.
func (a *App) FocusTerminalWindow(windowID string) error {
	if a.termMgr == nil {
		return fmt.Errorf("no terminal providers available")
	}
	return a.termMgr.FocusWindow(windowID)
}

// GetAvailableTerminals returns the names of terminal applications that are
// currently running and accessible (e.g. ["CMux", "iTerm2"]).
func (a *App) GetAvailableTerminals() ([]string, error) {
	if a.termMgr == nil {
		return []string{}, nil
	}
	return a.termMgr.GetAvailableTerminals(), nil
}

// ---------------------------------------------------------------------------
// Claude Code sessions (from ~/.claude/sessions/)
// ---------------------------------------------------------------------------

// GetClaudeSessions returns all active Claude Code sessions by reading
// the authoritative session files in ~/.claude/sessions/.
func (a *App) GetClaudeSessions() ([]claude.Session, error) {
	sessions, err := claude.GetActiveSessions()
	if err != nil {
		return nil, fmt.Errorf("GetClaudeSessions: %w", err)
	}
	if sessions == nil {
		sessions = []claude.Session{}
	}
	return sessions, nil
}

// GetSessionIndicators returns enriched session state for all active Claude
// Code sessions, including heuristic activity detection (typing, tool_use,
// waiting) based on session file modification times.
func (a *App) GetSessionIndicators() ([]claude.SessionIndicator, error) {
	indicators, err := claude.GetSessionIndicators()
	if err != nil {
		return nil, fmt.Errorf("GetSessionIndicators: %w", err)
	}
	if indicators == nil {
		indicators = []claude.SessionIndicator{}
	}
	return indicators, nil
}

// SendCommandToSession sends a command string to the terminal running the
// Claude session identified by PID. Switches to the matching CMux workspace
// first, then sends text via RPC (or AppleScript fallback).
func (a *App) SendCommandToSession(pid int, command string) error {
	slog.Info("SendCommandToSession: start", "pid", pid, "commandLen", len(command))

	if pid <= 0 {
		return fmt.Errorf("SendCommandToSession: invalid pid %d", pid)
	}
	command = strings.TrimSpace(command)
	if command == "" {
		return fmt.Errorf("SendCommandToSession: command is required")
	}
	command = sanitizeForAppleScript(command)

	if a.cmuxClient == nil || !a.cmuxClient.IsAvailable() {
		return fmt.Errorf("SendCommandToSession: CMux not available")
	}

	sess, getErr := claude.GetSession(pid)
	if getErr != nil {
		slog.Error("SendCommandToSession: GetSession failed", "pid", pid, "err", getErr)
		return fmt.Errorf("SendCommandToSession: %w", getErr)
	}

	// Find the workspace and surface WITHOUT focusing (background send).
	// This avoids stealing focus when Jarvis sends commands.
	slog.Info("SendCommandToSession: finding workspace", "cwd", sess.CWD)
	workspaces, wsErr := a.cmuxClient.ListWorkspaces()
	if wsErr == nil {
		for _, ws := range workspaces {
			if ws.CurrentDirectory == sess.CWD || filepath.Base(ws.CurrentDirectory) == filepath.Base(sess.CWD) {
				// Found workspace — list its surfaces and send directly.
				surfaces, sErr := a.cmuxClient.ListSurfacesInWorkspace(ws.ID)
				if sErr == nil && len(surfaces) > 0 {
					target := surfaces[0].Ref
					for _, s := range surfaces {
						slog.Debug("SendCommandToSession: surface", "ref", s.Ref, "title", s.Title, "selected", s.SelectedInPane, "tty", s.TTY)
						if s.SelectedInPane {
							target = s.Ref
							break
						}
					}
					if sendErr := a.cmuxClient.SendText(target, command+"\r"); sendErr == nil {
						slog.Info("SendCommandToSession: sent via RPC (no focus)", "ref", target, "workspace", ws.Title)
						return nil
					}
					slog.Warn("SendCommandToSession: RPC send failed", "err", "SendText failed")
				}
				break
			}
		}
	}

	// Fallback: focus workspace and send (legacy path — steals focus).
	slog.Info("SendCommandToSession: falling back to focus + send", "cwd", sess.CWD)
	if focusErr := a.cmuxClient.FocusWorkspaceByCWD(sess.CWD); focusErr != nil {
		slog.Warn("SendCommandToSession: workspace focus failed", "err", focusErr)
	} else {
		time.Sleep(100 * time.Millisecond)
		surfaces, listErr := a.cmuxClient.ListSurfaces()
		if listErr == nil && len(surfaces) > 0 {
			target := surfaces[0].Ref
			for _, s := range surfaces {
				if s.SelectedInPane {
					target = s.Ref
					break
				}
			}
			if sendErr := a.cmuxClient.SendText(target, command+"\r"); sendErr == nil {
				slog.Info("SendCommandToSession: sent via RPC (focused)", "ref", target)
				return nil
			}
		}
	}

	// Last resort: AppleScript.
	if sendErr := a.cmuxClient.SendTextViaAppleScript(command); sendErr != nil {
		slog.Error("SendCommandToSession: AppleScript send failed", "err", sendErr)
		return fmt.Errorf("SendCommandToSession: could not send to terminal for pid %d", pid)
	}
	slog.Info("SendCommandToSession: sent via AppleScript")
	return nil
}

// BroadcastCommand sends a command to multiple sessions identified by PIDs.
// It executes concurrently and returns a map of PID to error message (empty
// string for success). The outer error is only returned for invalid input.
func (a *App) BroadcastCommand(pids []int, command string) (map[int]string, error) {
	if len(pids) == 0 {
		return nil, fmt.Errorf("BroadcastCommand: pids must not be empty")
	}
	command = strings.TrimSpace(command)
	if command == "" {
		return nil, fmt.Errorf("BroadcastCommand: command is required")
	}

	slog.Info("BroadcastCommand: start", "pidCount", len(pids), "commandLen", len(command))

	results := make(map[int]string, len(pids))
	var mu sync.Mutex
	var wg sync.WaitGroup

	sem := make(chan struct{}, 5) // max 5 concurrent sends
	for _, pid := range pids {
		sem <- struct{}{} // acquire
		wg.Add(1)
		go func(p int) {
			defer func() { <-sem }() // release
			defer wg.Done()
			err := a.SendCommandToSession(p, command)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				results[p] = err.Error()
			} else {
				results[p] = ""
			}
		}(pid)
	}

	wg.Wait()

	successCount := 0
	for _, msg := range results {
		if msg == "" {
			successCount++
		}
	}
	slog.Info("BroadcastCommand: done", "total", len(pids), "success", successCount, "failed", len(pids)-successCount)

	return results, nil
}

// BroadcastToAll sends a command to all active sessions. It discovers active
// sessions via GetSessionIndicators, extracts their PIDs, and delegates to
// BroadcastCommand.
func (a *App) BroadcastToAll(command string) (map[int]string, error) {
	indicators, err := a.GetSessionIndicators()
	if err != nil {
		return nil, fmt.Errorf("BroadcastToAll: %w", err)
	}
	if len(indicators) == 0 {
		slog.Info("BroadcastToAll: no active sessions")
		return map[int]string{}, nil
	}

	pids := make([]int, len(indicators))
	for i, ind := range indicators {
		pids[i] = ind.PID
	}

	slog.Info("BroadcastToAll: found sessions", "count", len(pids))
	return a.BroadcastCommand(pids, command)
}

// FocusSession brings the terminal or IDE window running the session
// identified by PID to the foreground. For CMux sessions, it finds the
// exact surface by TTY matching across all workspaces. For IDE sessions,
// it activates the IDE app.
func (a *App) FocusSession(pid int) error {
	slog.Info("FocusSession: start", "pid", pid)

	if pid <= 0 {
		return fmt.Errorf("FocusSession: invalid pid %d", pid)
	}

	// Check if this is an IDE process (VS Code, etc.) and focus the IDE.
	if a.focusIDESession(pid) {
		return nil
	}

	if a.cmuxClient == nil || !a.cmuxClient.IsAvailable() {
		return fmt.Errorf("FocusSession: CMux not available")
	}

	// Primary: find the exact surface by TTY across all workspaces.
	tty := getTTYForPID(pid)
	if tty != "" {
		slog.Info("FocusSession: trying TTY match", "pid", pid, "tty", tty)
		if err := a.cmuxClient.FocusSurfaceByTTY(tty); err == nil {
			slog.Info("FocusSession: focused via TTY", "pid", pid, "tty", tty)
			return nil
		} else {
			slog.Warn("FocusSession: TTY match failed", "tty", tty, "err", err)
		}
	}

	// Fallback: try workspace CWD matching.
	sess, sessErr := claude.GetSession(pid)
	if sessErr != nil {
		slog.Error("FocusSession: GetSession failed", "pid", pid, "err", sessErr)
		_ = a.cmuxClient.ActivateAndSwitchTab()
		return nil
	}

	slog.Info("FocusSession: trying CWD match", "cwd", sess.CWD)
	if wsErr := a.cmuxClient.FocusWorkspaceByCWD(sess.CWD); wsErr == nil {
		_ = a.cmuxClient.ActivateAndSwitchTab()
		return nil
	}

	// Last resort: just bring CMux to foreground.
	_ = a.cmuxClient.ActivateAndSwitchTab()
	return nil
}

// getTTYForPID returns the short TTY name (e.g. "ttys004") for a process.
func getTTYForPID(pid int) string {
	out, err := exec.Command("ps", "-o", "tty=", "-p", fmt.Sprintf("%d", pid)).Output()
	if err != nil {
		return ""
	}
	tty := strings.TrimSpace(string(out))
	if tty == "" || tty == "??" {
		return ""
	}
	return tty
}

// idePatterns maps binary path substrings to macOS app names for IDE detection.
// When a Claude/agent process has no TTY (running inside an IDE), we match its
// binary path or command line against these patterns to determine which IDE to activate.
var idePatterns = []struct {
	Pattern string // substring to match in the binary path or command line
	AppName string // macOS app name for AppleScript activation
}{
	{".vscode/extensions", "Visual Studio Code"},
	{".vscode-server", "Visual Studio Code"},
	{".cursor/extensions", "Cursor"},
	{".cursor-server", "Cursor"},
	{".trae/extensions", "Trae"},
	{".windsurf/extensions", "Windsurf"},
	{"zed/extensions", "Zed"},
	{"jetbrains", "IntelliJ IDEA"},
	{".idea", "IntelliJ IDEA"},
	{"webstorm", "WebStorm"},
	{"goland", "GoLand"},
	{"pycharm", "PyCharm"},
	{"phpstorm", "PhpStorm"},
	{"rustrover", "RustRover"},
}

// focusIDESession checks if the PID belongs to an IDE-hosted agent session
// and activates the IDE window. Returns true if handled.
func (a *App) focusIDESession(pid int) bool {
	// IDE sessions have no TTY — terminal sessions always have one.
	tty := getTTYForPID(pid)
	if tty != "" {
		return false // has a TTY → terminal session, not IDE
	}

	cmdline, err := exec.Command("ps", "-o", "command=", "-p", fmt.Sprintf("%d", pid)).Output()
	if err != nil {
		return false
	}
	cmd := strings.ToLower(string(cmdline))

	for _, ide := range idePatterns {
		if strings.Contains(cmd, strings.ToLower(ide.Pattern)) {
			slog.Info("FocusSession: detected IDE session", "pid", pid, "app", ide.AppName)
			script := fmt.Sprintf(`tell application %q to activate`, ide.AppName)
			_ = exec.Command("osascript", "-e", script).Run()
			return true
		}
	}
	return false
}

// GetSessionTerminalOutput reads the visible text from the terminal running
// the Claude session identified by PID. Finds the surface by TTY without
// switching workspaces.
func (a *App) GetSessionTerminalOutput(pid int) (string, error) {
	if pid <= 0 {
		return "", fmt.Errorf("GetSessionTerminalOutput: invalid pid %d", pid)
	}

	if a.cmuxClient == nil || !a.cmuxClient.IsAvailable() {
		return "", fmt.Errorf("GetSessionTerminalOutput: CMux not available")
	}

	// Find the surface by TTY and read using its UUID.
	tty := getTTYForPID(pid)
	if tty != "" {
		loc, err := a.cmuxClient.FindSurfaceByTTY(tty)
		if err == nil && loc.SurfaceID != "" {
			text, readErr := a.cmuxClient.ReadText(loc.SurfaceID)
			if readErr == nil {
				return text, nil
			}
		}
	}

	// Fallback: try CWD-based workspace lookup.
	sess, sessErr := claude.GetSession(pid)
	if sessErr != nil {
		return "", fmt.Errorf("GetSessionTerminalOutput: %w", sessErr)
	}

	wsID, wsErr := a.cmuxClient.FindWorkspaceIDByCWD(sess.CWD)
	if wsErr != nil {
		return "", fmt.Errorf("GetSessionTerminalOutput: %w", wsErr)
	}

	surfaces, listErr := a.cmuxClient.ListSurfacesInWorkspace(wsID)
	if listErr != nil || len(surfaces) == 0 {
		return "", fmt.Errorf("GetSessionTerminalOutput: no surfaces found")
	}

	target := surfaces[0].ID
	for _, s := range surfaces {
		if s.SelectedInPane {
			target = s.ID
			break
		}
	}

	text, readErr := a.cmuxClient.ReadText(target)
	if readErr != nil {
		return "", fmt.Errorf("GetSessionTerminalOutput: read text: %w", readErr)
	}
	return text, nil
}

// ---------------------------------------------------------------------------
// Approval detection & response
// ---------------------------------------------------------------------------

// idleOrWorkingPatterns contains substrings that indicate the terminal is in a
// known idle or actively-working state (not blocked on user input). This
// mirrors the frontend's IDLE_OR_WORKING_PATTERNS logic.
var idleOrWorkingPatterns = []string{
	"? for shortcuts",
	"? for help",
	"esc to interrupt",
	"type a message",
}

// isIdleOrWorking checks whether the last few lines of terminal output match
// any known idle/working pattern. Returns true if the terminal appears idle or
// actively working (i.e. NOT waiting for approval).
func isIdleOrWorking(tailLines []string) bool {
	tail := strings.ToLower(strings.Join(tailLines, "\n"))
	for _, pat := range idleOrWorkingPatterns {
		if strings.Contains(tail, pat) {
			return true
		}
	}
	return false
}

// lastNonEmptyLines returns up to n non-empty (trimmed) lines from the end of
// the given text.
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

// GetPendingApprovals scans all active Claude Code sessions and returns those
// that appear to be blocked on an approval prompt. A session is considered
// pending approval when ALL of the following hold:
//  1. The backend heuristic marks it as hasQuestion (idle > 2 minutes).
//  2. The last 3 non-empty lines of terminal output do NOT match any known
//     idle/working pattern (e.g. "? for shortcuts", "esc to interrupt").
//  3. The terminal output positively matches at least one known approval
//     prompt pattern (e.g. "Allow", "Deny", "(y/n)", "tool use") via
//     claude.IsLikelyApprovalPrompt.
//
// Sessions that cannot be read are silently skipped.
func (a *App) GetPendingApprovals() ([]model.ApprovalRequest, error) {
	indicators, err := claude.GetSessionIndicators()
	if err != nil {
		return nil, fmt.Errorf("GetPendingApprovals: %w", err)
	}

	now := time.Now()
	var approvals []model.ApprovalRequest

	for _, ind := range indicators {
		if !ind.HasQuestion {
			continue
		}

		// Skip PIDs that recently failed to read — avoids log spam for
		// sessions without a CMux workspace (e.g. non-CMux terminals).
		a.approvalFailMu.RLock()
		if failTime, ok := a.approvalFailCache[ind.PID]; ok && now.Sub(failTime) < 5*60*time.Second {
			a.approvalFailMu.RUnlock()
			continue
		}
		a.approvalFailMu.RUnlock()

		output, readErr := a.GetSessionTerminalOutput(ind.PID)
		if readErr != nil {
			a.approvalFailMu.Lock()
			a.approvalFailCache[ind.PID] = now
			a.approvalFailMu.Unlock()
			slog.Debug("GetPendingApprovals: skipping session, cannot read output",
				"pid", ind.PID, "err", readErr)
			continue
		}

		// Check the last 3 non-empty lines against idle/working patterns.
		tail3 := lastNonEmptyLines(output, 3)
		if isIdleOrWorking(tail3) {
			continue
		}

		// Verify the output actually contains approval prompt patterns.
		// This prevents false positives from sessions that are idle but not
		// blocked on user approval (e.g. compiling, running tests, thinking).
		if !claude.IsLikelyApprovalPrompt(output) {
			continue
		}

		// Build the prompt text from the last 5 non-empty lines.
		tail5 := lastNonEmptyLines(output, 5)

		approval := model.ApprovalRequest{
			PID:         ind.PID,
			SessionName: ind.Name,
			CWD:         ind.CWD,
			PromptText:  strings.Join(tail5, "\n"),
			DetectedAt:  now,
		}

		// Evaluate auto-approve/deny rules before surfacing to the user.
		if a.evaluateApprovalRules(approval) {
			continue
		}

		approvals = append(approvals, approval)
	}

	if approvals == nil {
		approvals = []model.ApprovalRequest{}
	}
	return approvals, nil
}

// RespondToApproval sends a text response to a session that is waiting for
// approval. The response is typically "y", "n", or custom text entered by the
// user.
func (a *App) RespondToApproval(pid int, response string) error {
	if pid <= 0 {
		return fmt.Errorf("RespondToApproval: invalid pid %d", pid)
	}
	response = strings.TrimSpace(response)
	if response == "" {
		return fmt.Errorf("RespondToApproval: response is required")
	}

	slog.Info("RespondToApproval", "pid", pid, "responseLen", len(response))
	return a.SendCommandToSession(pid, response)
}

// evaluateApprovalRules checks enabled approval rules against the given
// approval request. If a matching rule is found, it automatically responds
// (approve or deny) and returns true. Returns false if no rule matched.
func (a *App) evaluateApprovalRules(approval model.ApprovalRequest) bool {
	rules, err := a.store.GetEnabledApprovalRules()
	if err != nil || len(rules) == 0 {
		return false
	}
	for _, rule := range rules {
		// Check scope — project-scoped rules only match when the CWD
		// is within the configured project path.
		if rule.Scope == "project" && rule.ProjectPath != "" {
			if !strings.Contains(approval.CWD, rule.ProjectPath) {
				continue
			}
		}
		// Check pattern match against the prompt text.
		re, err := regexp.Compile(rule.Pattern)
		if err != nil {
			continue
		}
		if re.MatchString(approval.PromptText) {
			if rule.Action == "approve" {
				slog.Info("auto-approving via rule",
					"rule", rule.Name,
					"pid", approval.PID,
					"prompt", approval.PromptText[:min(80, len(approval.PromptText))])
				_ = a.RespondToApproval(approval.PID, "y")
				return true
			} else if rule.Action == "deny" {
				slog.Info("auto-denying via rule",
					"rule", rule.Name,
					"pid", approval.PID)
				_ = a.RespondToApproval(approval.PID, "n")
				return true
			}
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Repo search
// ---------------------------------------------------------------------------

// SearchRepos indexes repos from Claude Code's session and project history,
// then filters by the query string. Returns up to 20 results sorted with
// active sessions first.
func (a *App) SearchRepos(query string) ([]discovery.RepoSearchResult, error) {
	results, err := discovery.SearchRepos(query)
	if err != nil {
		return nil, fmt.Errorf("SearchRepos: %w", err)
	}
	if results == nil {
		results = []discovery.RepoSearchResult{}
	}
	return results, nil
}

// ---------------------------------------------------------------------------
// Cost tracking
// ---------------------------------------------------------------------------

// GetTotalSpend returns all-time, this-month, and today cost totals.
func (a *App) GetTotalSpend() (model.TotalSpend, error) {
	return claude.GetTotalSpend()
}

// GetDailyCostSummary returns daily cost aggregation for the last 30 days.
func (a *App) GetDailyCostSummary() ([]model.DailyCost, error) {
	return claude.GetDailyCosts()
}

// GetProjectCosts returns all session costs for a specific project path.
func (a *App) GetProjectCosts(projectPath string) ([]claude.SessionUsage, error) {
	costs, err := claude.GetUsageByProject(projectPath)
	if err != nil {
		return nil, fmt.Errorf("GetProjectCosts: %w", err)
	}
	if costs == nil {
		costs = []claude.SessionUsage{}
	}
	return costs, nil
}

// GetAllCosts returns usage data for all sessions.
func (a *App) GetAllCosts() ([]claude.SessionUsage, error) {
	return claude.GetAllSessionUsage()
}

// ---------------------------------------------------------------------------
// Project Discovery & Divide-and-Conquer
// ---------------------------------------------------------------------------

// DiscoverProjects scans common root directories and returns discovered
// projects with their repos, languages, and active session info.
func (a *App) DiscoverProjects() ([]discovery.Project, error) {
	roots := discovery.GetCommonRootPaths()
	if len(roots) == 0 {
		return []discovery.Project{}, nil
	}

	projects, err := discovery.DiscoverProjects(roots)
	if err != nil {
		return nil, fmt.Errorf("DiscoverProjects: %w", err)
	}
	if projects == nil {
		projects = []discovery.Project{}
	}
	return projects, nil
}

// GetProjectSuggestions returns suggested tasks for a project at the given path.
// It discovers the project structure and generates task suggestions.
func (a *App) GetProjectSuggestions(projectPath string) ([]discovery.TaskSuggestion, error) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return nil, fmt.Errorf("GetProjectSuggestions: projectPath is required")
	}

	// Discover the project by scanning the given path as a root.
	projects, err := discovery.DiscoverProjects([]string{projectPath})
	if err != nil {
		return nil, fmt.Errorf("GetProjectSuggestions: %w", err)
	}

	// Find the project that matches the requested path, or use the first one.
	var target discovery.Project
	for _, p := range projects {
		if p.Path == projectPath {
			target = p
			break
		}
	}
	if target.Path == "" && len(projects) > 0 {
		target = projects[0]
	}
	if target.Path == "" {
		return []discovery.TaskSuggestion{}, nil
	}

	suggestions := discovery.SuggestTasks(target)
	if suggestions == nil {
		suggestions = []discovery.TaskSuggestion{}
	}
	return suggestions, nil
}

// ExecuteDivideAndConquer launches Claude Code sessions across multiple repos
// with the given prompt. If sequential is true, each session waits for the
// previous one to complete before starting. If false, all sessions are launched
// in parallel. The method returns immediately; sessions run in the background.
func (a *App) ExecuteDivideAndConquer(agentType string, repoPaths []string, prompt string, sequential bool) error {
	agentType = strings.TrimSpace(agentType)
	if agentType == "" {
		agentType = string(model.AgentClaudeCode)
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return fmt.Errorf("ExecuteDivideAndConquer: prompt is required")
	}
	if len(repoPaths) == 0 {
		return fmt.Errorf("ExecuteDivideAndConquer: repoPaths is required")
	}
	if a.sessionMgr == nil {
		return fmt.Errorf("ExecuteDivideAndConquer: session manager is not enabled")
	}

	at := model.AgentType(agentType)

	if sequential {
		// Launch sessions one after another in a background goroutine.
		// Use the App's context so the goroutine stops when the app shuts down.
		ctx := a.ctx
		go func() {
			for _, repoPath := range repoPaths {
				// Check for cancellation between iterations.
				select {
				case <-ctx.Done():
					slog.Info("divide-and-conquer: context cancelled, aborting sequential run")
					return
				default:
				}

				repoPath = strings.TrimSpace(repoPath)
				if repoPath == "" {
					continue
				}

				slog.Info("divide-and-conquer: launching sequential session",
					"agentType", agentType, "repoPath", repoPath)

				sess, err := a.sessionMgr.Launch(at, repoPath, prompt)
				if err != nil {
					slog.Error("divide-and-conquer: failed to launch session",
						"repoPath", repoPath, "err", err)
					continue
				}

				a.logActivity(sess.ID, string(sess.AgentType), "dnc_session_launched",
					fmt.Sprintf("D&C sequential session launched in %s", repoPath), "")

				// Wait for this session to reach a terminal state before
				// launching the next one.
				a.waitForSessionComplete(ctx, sess.ID)
			}

			slog.Info("divide-and-conquer: sequential execution complete",
				"repos", len(repoPaths))
		}()
	} else {
		// Launch all sessions in parallel.
		for _, repoPath := range repoPaths {
			repoPath = strings.TrimSpace(repoPath)
			if repoPath == "" {
				continue
			}

			slog.Info("divide-and-conquer: launching parallel session",
				"agentType", agentType, "repoPath", repoPath)

			sess, err := a.sessionMgr.Launch(at, repoPath, prompt)
			if err != nil {
				slog.Error("divide-and-conquer: failed to launch session",
					"repoPath", repoPath, "err", err)
				continue
			}

			a.logActivity(sess.ID, string(sess.AgentType), "dnc_session_launched",
				fmt.Sprintf("D&C parallel session launched in %s", repoPath), "")
		}
	}

	return nil
}

// waitForSessionComplete polls the store until the session reaches a terminal
// state (completed or failed). It checks every 2 seconds with a 30-minute
// timeout to avoid blocking forever. The context allows the caller (or app
// shutdown) to cancel the wait early, preventing goroutine leaks.
func (a *App) waitForSessionComplete(ctx context.Context, sessionID string) {
	const (
		pollInterval = 2 * time.Second
		maxWait      = 30 * time.Minute
	)

	deadline := time.Now().Add(maxWait)
	for time.Now().Before(deadline) {
		sess, err := a.store.GetSession(sessionID)
		if err != nil {
			slog.Warn("waitForSessionComplete: failed to get session",
				"sessionID", sessionID, "err", err)
			return
		}

		if sess.Status.IsTerminal() {
			slog.Info("waitForSessionComplete: session completed",
				"sessionID", sessionID, "status", sess.Status)
			return
		}

		select {
		case <-ctx.Done():
			slog.Info("waitForSessionComplete: context cancelled",
				"sessionID", sessionID)
			return
		case <-time.After(pollInterval):
		}
	}

	slog.Warn("waitForSessionComplete: timed out waiting for session",
		"sessionID", sessionID, "maxWait", maxWait)
}

// SaveProject persists a discovered project to the database along with its
// associated repo paths.
func (a *App) SaveProject(name, path string, repoPaths []string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("SaveProject: name is required")
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("SaveProject: path is required")
	}

	// Determine if monorepo (simple heuristic: path itself is a git repo).
	isMonorepo := git.IsGitRepo(path)

	proj, err := a.store.CreateProject(name, path, isMonorepo)
	if err != nil {
		return fmt.Errorf("SaveProject: %w", err)
	}

	if len(repoPaths) > 0 {
		if err := a.store.SetProjectRepos(proj.ID, repoPaths); err != nil {
			return fmt.Errorf("SaveProject: setting repos: %w", err)
		}
	}

	a.logActivity("", name, "project_saved",
		fmt.Sprintf("Project '%s' saved with %d repos", name, len(repoPaths)), "")

	return nil
}

// ListSavedProjects returns all persisted projects from the database.
func (a *App) ListSavedProjects() ([]store.Project, error) {
	projects, err := a.store.ListProjects()
	if err != nil {
		return nil, fmt.Errorf("ListSavedProjects: %w", err)
	}
	if projects == nil {
		projects = []store.Project{}
	}
	return projects, nil
}

// DeleteSavedProject removes a saved project from the database.
func (a *App) DeleteSavedProject(id string) error {
	if id == "" {
		return fmt.Errorf("DeleteSavedProject: id is required")
	}
	if err := a.store.DeleteProject(id); err != nil {
		return fmt.Errorf("DeleteSavedProject: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Virtual Monorepo Workspaces
// ---------------------------------------------------------------------------

// CreateWorkspace creates a virtual monorepo workspace without launching a
// session. The workspace directory is created with symlinks to each repo and an
// auto-generated CLAUDE.md. The user can launch Claude Code manually from the
// workspace (e.g., via CMux).
func (a *App) CreateWorkspace(name string, repoPaths []string, prompt string) (workspace.Workspace, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return workspace.Workspace{}, fmt.Errorf("CreateWorkspace: name is required")
	}
	if len(repoPaths) == 0 {
		return workspace.Workspace{}, fmt.Errorf("CreateWorkspace: at least one repo path is required")
	}

	ws, err := workspace.Create(name, repoPaths, prompt)
	if err != nil {
		return workspace.Workspace{}, fmt.Errorf("CreateWorkspace: %w", err)
	}

	a.logActivity("", name, "workspace_created",
		fmt.Sprintf("Workspace '%s' created with %d repos at %s", name, len(repoPaths), ws.Path), "")

	return *ws, nil
}

// CreateWorkspaceAndLaunch creates a virtual monorepo workspace and
// immediately launches a Claude Code session within it. The session's CWD is
// the workspace directory, and --add-dir flags give Claude access to each real
// repo path. Returns the workspace metadata.
func (a *App) CreateWorkspaceAndLaunch(name string, repoPaths []string, prompt string) (workspace.Workspace, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return workspace.Workspace{}, fmt.Errorf("CreateWorkspaceAndLaunch: name is required")
	}
	if len(repoPaths) == 0 {
		return workspace.Workspace{}, fmt.Errorf("CreateWorkspaceAndLaunch: at least one repo path is required")
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return workspace.Workspace{}, fmt.Errorf("CreateWorkspaceAndLaunch: prompt is required")
	}
	if a.sessionMgr == nil {
		return workspace.Workspace{}, fmt.Errorf("CreateWorkspaceAndLaunch: session manager is not enabled")
	}

	// Create the workspace.
	ws, err := workspace.Create(name, repoPaths, prompt)
	if err != nil {
		return workspace.Workspace{}, fmt.Errorf("CreateWorkspaceAndLaunch: %w", err)
	}

	// Build the --add-dir flags for each real repo.
	addDirArgs := workspace.BuildLaunchArgs(ws)

	// Launch Claude Code with the workspace as CWD and --add-dir for each repo.
	sess, err := a.sessionMgr.LaunchWithArgs(
		model.AgentClaudeCode,
		ws.Path,   // CWD = workspace directory
		prompt,
		addDirArgs,
	)
	if err != nil {
		// Workspace was created but launch failed. Log but return the workspace
		// so the user can launch manually.
		slog.Error("CreateWorkspaceAndLaunch: session launch failed",
			"workspace", ws.Path, "err", err)
		a.logActivity("", name, "workspace_created",
			fmt.Sprintf("Workspace '%s' created (launch failed: %v)", name, err), "")
		return *ws, fmt.Errorf("workspace created at %s but session launch failed: %w", ws.Path, err)
	}

	a.logActivity(sess.ID, name, "workspace_launched",
		fmt.Sprintf("Workspace '%s' launched with %d repos (session %s)", name, len(repoPaths), sess.ID[:8]), "")

	return *ws, nil
}

// LaunchReposInTerminal opens each repo path in the user's preferred terminal
// and optionally executes a command (e.g. "claude") in each. The preferred
// terminal is read from config; defaults to CMux if available.
//
// When useWorktree is true, a git worktree is created for each repo via
// git.CreateWorktree and the terminal is opened in the worktree directory
// instead of the original repo. If worktree creation fails for a given repo,
// the original repo path is used as a fallback.
func (a *App) LaunchReposInTerminal(repoPaths []string, command string, useWorktree ...bool) error {
	if len(repoPaths) == 0 {
		return fmt.Errorf("LaunchReposInTerminal: no repo paths")
	}
	command = strings.TrimSpace(command)
	command = sanitizeForAppleScript(command)
	worktree := len(useWorktree) > 0 && useWorktree[0]

	cfg := config.Get()
	terminal := strings.ToLower(cfg.PreferredTerminal)

	// Auto-detect if not set.
	if terminal == "" {
		if a.cmuxClient != nil && a.cmuxClient.IsAvailable() {
			terminal = "cmux"
		} else {
			terminal = "terminal"
		}
	}

	// Multi-repo on CMux: create a virtual monorepo workspace so Claude sees
	// all repos in one session, then open a single tab at the workspace root.
	if terminal == "cmux" && len(repoPaths) > 1 {
		name := autoWorkspaceName(repoPaths)
		ws, err := workspace.Create(name, repoPaths, "")
		if err != nil {
			slog.Error("LaunchReposInTerminal: failed to create multi-repo workspace", "err", err)
			// Fall through and open repos individually.
		} else {
			slog.Info("LaunchReposInTerminal: created multi-repo workspace",
				"name", name, "path", ws.Path, "repos", len(repoPaths))
			a.logActivity("", name, "workspace_created",
				fmt.Sprintf("Workspace '%s' created with %d repos", name, len(repoPaths)), "")
			if err := a.cmuxClient.OpenWorkspace(ws.Path, command); err != nil {
				slog.Error("LaunchReposInTerminal: failed to open workspace in CMux", "err", err)
			}
			return nil
		}
	}

	for _, repoPath := range repoPaths {
		targetPath := repoPath

		if worktree {
			wt, err := git.CreateWorktree(repoPath)
			if err != nil {
				slog.Error("LaunchReposInTerminal: failed to create worktree",
					"repo", repoPath, "err", err)
			} else {
				slog.Info("LaunchReposInTerminal: created worktree",
					"repo", repoPath, "worktree", wt.Path, "branch", wt.Branch)
				targetPath = wt.Path
			}
		}

		if err := a.openRepoInTerminal(terminal, targetPath, command); err != nil {
			slog.Error("LaunchReposInTerminal: failed to open repo",
				"terminal", terminal, "repo", targetPath, "err", err)
		}
		time.Sleep(300 * time.Millisecond)
	}
	return nil
}

// autoWorkspaceName generates a workspace name from repo basenames + a short
// timestamp suffix to avoid collisions on repeated launches.
func autoWorkspaceName(repoPaths []string) string {
	parts := make([]string, 0, len(repoPaths))
	for _, p := range repoPaths {
		parts = append(parts, filepath.Base(p))
	}
	name := strings.Join(parts, "-")
	// Append short timestamp to avoid collisions.
	name += "-" + time.Now().Format("0102-1504")
	return name
}

// LaunchReposInTerminalWithWorktree is a convenience wrapper for the frontend.
// Wails bindings do not support variadic parameters, so this provides an
// explicit three-argument signature that the frontend can call directly.
func (a *App) LaunchReposInTerminalWithWorktree(repoPaths []string, command string, useWorktree bool) error {
	return a.LaunchReposInTerminal(repoPaths, command, useWorktree)
}

func (a *App) openRepoInTerminal(terminal, repoPath, command string) error {
	switch terminal {
	case "cmux":
		return a.openInCMux(repoPath, command)
	case "iterm2", "iterm":
		return openInITerm2(repoPath, command)
	default:
		return openInTerminalApp(repoPath, command)
	}
}

func (a *App) openInCMux(repoPath, command string) error {
	if a.cmuxClient == nil {
		return fmt.Errorf("CMux not available")
	}
	if err := a.cmuxClient.OpenWorkspace(repoPath, command); err != nil {
		return fmt.Errorf("cmux open: %w", err)
	}
	return nil
}

// sanitizeForAppleScript escapes user-supplied strings so they can be safely
// embedded inside AppleScript quoted-string literals. It handles backslashes,
// double-quotes, and backticks (which can invoke shell interpolation in some
// terminal emulators).
func sanitizeForAppleScript(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "`", "'")
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\r", "")
	return s
}

func openInITerm2(repoPath, command string) error {
	repoPath = sanitizeForAppleScript(repoPath)
	command = sanitizeForAppleScript(command)
	cmd := fmt.Sprintf("cd %q", repoPath)
	if command != "" {
		cmd = fmt.Sprintf("cd %q && %s", repoPath, command)
	}
	// Create a new tab in the existing window, or a new window if none open.
	script := fmt.Sprintf(`tell application "iTerm2"
	activate
	if (count of windows) > 0 then
		tell current window
			set newTab to (create tab with default profile)
			tell current session of newTab
				write text %q
			end tell
		end tell
	else
		set newWindow to (create window with default profile)
		tell current session of newWindow
			write text %q
		end tell
	end if
end tell`, cmd, cmd)
	return exec.Command("osascript", "-e", script).Run()
}

func openInTerminalApp(repoPath, command string) error {
	repoPath = sanitizeForAppleScript(repoPath)
	command = sanitizeForAppleScript(command)
	cmd := fmt.Sprintf("cd %q", repoPath)
	if command != "" {
		cmd = fmt.Sprintf("cd %q && %s", repoPath, command)
	}
	// Create a new tab in the front window, or a new window if none open.
	script := fmt.Sprintf(`tell application "Terminal"
	activate
	if (count of windows) > 0 then
		tell front window
			set newTab to do script %q
		end tell
	else
		do script %q
	end if
end tell`, cmd, cmd)
	return exec.Command("osascript", "-e", script).Run()
}

// ---------------------------------------------------------------------------
// Session templates
// ---------------------------------------------------------------------------

// SaveSessionTemplate creates a new session template.
func (a *App) SaveSessionTemplate(name, agentType string, repoPaths []string, command string) (model.SessionTemplate, error) {
	t := model.NewSessionTemplate(name, agentType, repoPaths, command)
	return a.store.CreateSessionTemplate(t)
}

// ListSessionTemplates returns all saved templates.
func (a *App) ListSessionTemplates() ([]model.SessionTemplate, error) {
	templates, err := a.store.ListSessionTemplates()
	if err != nil {
		return nil, err
	}
	if templates == nil {
		templates = []model.SessionTemplate{}
	}
	return templates, nil
}

// DeleteSessionTemplate deletes a template.
func (a *App) DeleteSessionTemplate(id string) error {
	return a.store.DeleteSessionTemplate(id)
}

// LaunchFromTemplate launches all repos from a template in the terminal.
func (a *App) LaunchFromTemplate(templateID string) error {
	t, err := a.store.GetSessionTemplate(templateID)
	if err != nil {
		return fmt.Errorf("LaunchFromTemplate: %w", err)
	}
	return a.LaunchReposInTerminal(t.RepoPaths, t.Command)
}

// ListWorkspaces returns metadata for all virtual monorepo workspaces.
func (a *App) ListWorkspaces() ([]workspace.Workspace, error) {
	workspaces, err := workspace.List()
	if err != nil {
		return nil, fmt.Errorf("ListWorkspaces: %w", err)
	}
	if workspaces == nil {
		workspaces = []workspace.Workspace{}
	}
	return workspaces, nil
}

// DeleteWorkspace removes a virtual monorepo workspace directory and all of
// its contents (symlinks, CLAUDE.md, metadata).
func (a *App) DeleteWorkspace(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("DeleteWorkspace: path is required")
	}

	if err := workspace.Delete(path); err != nil {
		return fmt.Errorf("DeleteWorkspace: %w", err)
	}

	a.logActivity("", "", "workspace_deleted",
		fmt.Sprintf("Workspace at %s deleted", path), "")

	return nil
}

// SyncDotClaude pulls the latest dotAiAgent repo and re-copies .claude/ to all
// workspaces. Returns the number of workspaces synced.
func (a *App) SyncDotClaude() (int, error) {
	count, err := workspace.SyncDotClaude()
	if err != nil {
		return 0, fmt.Errorf("SyncDotClaude: %w", err)
	}
	a.logActivity("", "", "dotclaude_synced",
		fmt.Sprintf("Synced .claude to %d workspaces", count), "")
	return count, nil
}

// OpenWorkspaceInTerminal opens a workspace directory in a terminal. If CMux
// is available, it opens a new CMux workspace. Otherwise, it falls back to the
// terminal manager to open a new window.
func (a *App) OpenWorkspaceInTerminal(workspacePath string) error {
	workspacePath = strings.TrimSpace(workspacePath)
	if workspacePath == "" {
		return fmt.Errorf("OpenWorkspaceInTerminal: workspacePath is required")
	}

	// Verify the workspace directory exists.
	if info, err := os.Stat(workspacePath); err != nil || !info.IsDir() {
		return fmt.Errorf("OpenWorkspaceInTerminal: workspace path %q does not exist or is not a directory", workspacePath)
	}

	// Try CMux: first try focusing existing workspace, then open new one.
	if a.cmuxClient != nil && a.cmuxClient.IsAvailable() {
		if err := a.cmuxClient.FocusWorkspaceByCWD(workspacePath); err == nil {
			return nil
		}
		// No existing workspace — open a new one via `open -a cmux`.
		if err := a.cmuxClient.FocusDirectory(workspacePath); err == nil {
			return nil
		}
	}

	// Fall back to terminal manager.
	if a.termMgr != nil {
		windows, err := a.termMgr.ListAllWindows()
		if err == nil && len(windows) > 0 {
			return a.termMgr.SendText(windows[0].ID, fmt.Sprintf("cd %q", workspacePath))
		}
	}

	return fmt.Errorf("OpenWorkspaceInTerminal: no terminal provider available")
}

// ---------------------------------------------------------------------------
// Impact detection
// ---------------------------------------------------------------------------

// GetImpactWarnings checks for conflicts between active sessions.
func (a *App) GetImpactWarnings() ([]impact.ImpactWarning, error) {
	// Get active sessions
	sessions, err := claude.GetActiveSessions()
	if err != nil {
		return []impact.ImpactWarning{}, nil // graceful degradation
	}

	if len(sessions) < 2 {
		return []impact.ImpactWarning{}, nil
	}

	// Build SessionChanges for each active session
	var changes []impact.SessionChanges
	for _, sess := range sessions {
		sc, err := impact.GetSessionChanges(sess.CWD)
		if err != nil {
			continue // skip sessions that fail
		}
		sc.Name = filepath.Base(sess.CWD)
		changes = append(changes, sc)
	}

	warnings := impact.DetectImpacts(changes)
	if warnings == nil {
		warnings = []impact.ImpactWarning{}
	}
	return warnings, nil
}

// ---------------------------------------------------------------------------
// Settings / Configuration
// ---------------------------------------------------------------------------

// GetConfig returns the current Jarvis configuration.
func (a *App) GetConfig() (config.Config, error) {
	cfg, err := config.Load()
	if err != nil {
		return config.Config{}, fmt.Errorf("GetConfig: %w", err)
	}
	return *cfg, nil
}

// SaveConfig and DaemonRestartNeeded live in app_settings_apply.go.

// MobileConnectionInfo holds the data the Settings UI needs to display the
// "Mobile App" section: LAN IPs, the API port, and the Bearer token.
type MobileConnectionInfo struct {
	IPs   []string `json:"ips"`
	Port  int      `json:"port"`
	Token string   `json:"token"`
}

// GetMobileConnectionInfo returns the LAN IP addresses, mobile API port, and
// current Bearer token so the Settings view can display connection details.
func (a *App) GetMobileConnectionInfo() MobileConnectionInfo {
	cfg := config.Get()
	ips := getLANIPs()
	return MobileConnectionInfo{
		IPs:   ips,
		Port:  cfg.MobileAPIPort,
		Token: cfg.MobileAPIToken,
	}
}

// RegenerateMobileToken creates a new random Bearer token, persists it to
// config, and hot-swaps it in the running API server (if active).
func (a *App) RegenerateMobileToken() error {
	token, err := generateToken()
	if err != nil {
		return fmt.Errorf("RegenerateMobileToken: %w", err)
	}
	cfg := config.Get()
	cfg.MobileAPIToken = token
	if err := config.Save(cfg); err != nil {
		return fmt.Errorf("RegenerateMobileToken: %w", err)
	}
	if a.apiServer != nil {
		a.apiServer.UpdateToken(token)
	}
	return nil
}

// getLANIPs returns non-loopback, non-link-local IPv4 addresses from the host's
// network interfaces. Used by GetMobileConnectionInfo to show which IPs the
// mobile client can connect to.
func getLANIPs() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		slog.Warn("getLANIPs: failed to list interfaces", "err", err)
		return nil
	}
	var ips []string
	for _, iface := range ifaces {
		// Skip down or loopback interfaces.
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.To4() == nil {
				continue // skip IPv6 and nil
			}
			// Skip loopback (127.x.x.x) and link-local (169.254.x.x).
			if ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			ips = append(ips, ip.String())
		}
	}
	return ips
}

// SetDotClaudeSource sets the path to the .claude source directory (dotAiAgent repo).
func (a *App) SetDotClaudeSource(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("SetDotClaudeSource: path is required")
	}
	// Validate the path exists and contains a .claude folder (or is one).
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("SetDotClaudeSource: path %q does not exist", path)
	}
	if !info.IsDir() {
		return fmt.Errorf("SetDotClaudeSource: %q is not a directory", path)
	}

	cfg, _ := config.Load()
	cfg.DotClaudeSourcePath = path
	return config.Save(cfg)
}

// ---------------------------------------------------------------------------
// Session recording
// ---------------------------------------------------------------------------

// ListRecordedSessions returns summaries of all recorded sessions.
func (a *App) ListRecordedSessions() ([]recording.RecordingSummary, error) {
	return recording.ListRecordings()
}

// GetSessionRecording returns all snapshots for a recorded session.
func (a *App) GetSessionRecording(sessionID string) ([]recording.Snapshot, error) {
	return recording.GetRecording(sessionID)
}

// ---------------------------------------------------------------------------
// Natural language query
// ---------------------------------------------------------------------------

// ExecuteNLQuery parses a natural language query and returns structured results.
func (a *App) ExecuteNLQuery(query string) nlquery.QueryResult {
	cb := nlquery.Callbacks{
		GetIndicators: func() ([]interface{}, error) {
			indicators, err := claude.GetSessionIndicators()
			if err != nil {
				return nil, err
			}
			result := make([]interface{}, len(indicators))
			for i, ind := range indicators {
				result[i] = map[string]interface{}{
					"pid":          ind.PID,
					"name":         ind.Name,
					"cwd":          ind.CWD,
					"hasQuestion":  ind.HasQuestion,
					"lastActivity": ind.LastActivity,
				}
			}
			return result, nil
		},
		GetTotalSpend: func() (interface{}, error) {
			return claude.GetTotalSpend()
		},
		GetRecordings: func() ([]interface{}, error) {
			recs, err := recording.ListRecordings()
			if err != nil {
				return nil, err
			}
			result := make([]interface{}, len(recs))
			for i, r := range recs {
				result[i] = r
			}
			return result, nil
		},
		BroadcastAll: func(cmd string) (map[int]string, error) {
			return a.BroadcastToAll(cmd)
		},
	}
	return nlquery.Execute(query, cb)
}

// ---------------------------------------------------------------------------
// Session forking
// ---------------------------------------------------------------------------

// ForkSession creates a new session based on an existing one, linking them via
// ParentSessionID. The forked session inherits agent type, repo path, and
// prompt from the original but starts with a fresh ID and launching status.
func (a *App) ForkSession(sessionID string) (model.Session, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return model.Session{}, fmt.Errorf("ForkSession: sessionID is required")
	}
	original, err := a.store.GetSession(sessionID)
	if err != nil {
		return model.Session{}, fmt.Errorf("ForkSession: %w", err)
	}
	forked := model.NewSession(original.AgentType, original.RepoPath, original.Prompt)
	forked.ParentSessionID = original.ID
	result, err := a.store.CreateSession(forked)
	if err != nil {
		return model.Session{}, fmt.Errorf("ForkSession: %w", err)
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// Recipes (template + params + steps)
// ---------------------------------------------------------------------------

// CreateRecipe creates a session template together with its configurable
// parameters and ordered steps in a single transaction.
func (a *App) CreateRecipe(name string, params []model.TemplateParam, steps []model.RecipeStep) (model.SessionTemplate, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return model.SessionTemplate{}, fmt.Errorf("CreateRecipe: name is required")
	}
	tmpl, err := a.store.CreateRecipe(name, params, steps)
	if err != nil {
		return model.SessionTemplate{}, fmt.Errorf("CreateRecipe: %w", err)
	}
	return tmpl, nil
}

// GetRecipeWithDetails returns a recipe template along with its parameters and
// steps. The result is a map with keys "template", "params", and "steps".
func (a *App) GetRecipeWithDetails(templateID string) (map[string]interface{}, error) {
	templateID = strings.TrimSpace(templateID)
	if templateID == "" {
		return nil, fmt.Errorf("GetRecipeWithDetails: templateID is required")
	}
	tmpl, params, steps, err := a.store.GetRecipeWithDetails(templateID)
	if err != nil {
		return nil, fmt.Errorf("GetRecipeWithDetails: %w", err)
	}
	return map[string]interface{}{
		"template": tmpl,
		"params":   params,
		"steps":    steps,
	}, nil
}

// ---------------------------------------------------------------------------
// Multi-phase workflow execution
// ---------------------------------------------------------------------------

// ExecuteWorkflow launches a sequence of workflow phases, each as a queued
// session with dependency on the previous phase. Phases execute sequentially
// via the session manager's dependency-aware queue.
func (a *App) ExecuteWorkflow(phases []WorkflowPhase) error {
	if len(phases) == 0 {
		return fmt.Errorf("ExecuteWorkflow: no phases")
	}
	if a.sessionMgr == nil {
		return fmt.Errorf("ExecuteWorkflow: session manager not initialized")
	}

	// Launch phases sequentially with dependencies on the previous phase.
	var prevSessionID string
	for i, phase := range phases {
		sess := model.NewSession(model.AgentType(phase.AgentType), phase.RepoPath, phase.Prompt)
		sess.Phase = phase.Phase
		if prevSessionID != "" {
			sess.DependsOn = []string{prevSessionID}
		}
		launched, err := a.sessionMgr.QueueSession(sess)
		if err != nil {
			return fmt.Errorf("ExecuteWorkflow: phase %d failed: %w", i+1, err)
		}
		prevSessionID = launched.ID

		runtime.EventsEmit(a.ctx, "workflow_progress", map[string]interface{}{
			"phase":     i + 1,
			"total":     len(phases),
			"status":    "queued",
			"agentType": phase.AgentType,
		})
	}
	return nil
}

// GetNextCalendarEvent satisfies api.StatsProvider for the mobile
// stats broadcaster. Delegates straight to GoogleCalendarGetNextEvent
// — the 60s in-memory cache in app_gcal.go ensures the 5s broadcaster
// tick doesn't hammer the Calendar API.
func (a *App) GetNextCalendarEvent() (*model.NextEventSnapshot, error) {
	return a.GoogleCalendarGetNextEvent()
}
