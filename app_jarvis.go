package main

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/namanchopra/jarvis/internal/api"
	"github.com/namanchopra/jarvis/internal/claude"
	"github.com/namanchopra/jarvis/internal/config"
	"github.com/namanchopra/jarvis/internal/jarvis"
	"github.com/namanchopra/jarvis/internal/paths"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// maxJarvisRestarts is the maximum number of consecutive automatic restarts
// before the monitor goroutine gives up.
const maxJarvisRestarts = 3

// ---------------------------------------------------------------------------
// Jarvis AI companion bindings
// ---------------------------------------------------------------------------

// StartJarvis launches the Python Jarvis daemon as a subprocess. The daemon
// handles all voice I/O (STT, TTS, wake-word, tool calls) and connects
// back to AWM via the mobile API WebSocket.
//
// Path resolution order (TASK-011):
//  1. Bundled (`<.app>/Contents/Resources/python/bin/python3` +
//     `<Resources>/jarvis-daemon/main.py`) when running from a built .app
//  2. Dev venv (`~/.jarvis/jarvis-daemon-env/bin/python3`) + source-tree script
//     when running via `wails dev`
func (a *App) StartJarvis() error {
	a.jarvisMu.Lock()
	defer a.jarvisMu.Unlock()

	if a.jarvisProcess != nil {
		return fmt.Errorf("StartJarvis: already running")
	}

	// Locate the Python binary. Prefer the bundled interpreter inside the
	// .app when present (paths.BundledPython already verifies existence +
	// execute bit); fall back to the dev venv for `wails dev` runs.
	bundledPython := paths.BundledPython()
	devPython := paths.DataPath("jarvis-daemon-env", "bin", "python3")

	pythonPath := bundledPython
	if pythonPath == "" {
		if _, err := os.Stat(devPython); err != nil {
			return fmt.Errorf("StartJarvis: could not find Python interpreter; tried bundled %q and dev %q: %w",
				filepath.Join("<bundle>", "Contents", "Resources", "python", "bin", "python3"), devPython, err)
		}
		pythonPath = devPython
	}

	// Locate the daemon entry point script. findJarvisDaemonScript already
	// prefers the bundled path when available.
	scriptPath := findJarvisDaemonScript()
	if scriptPath == "" {
		return fmt.Errorf("StartJarvis: could not find daemon script; tried bundled %q and source-tree fallbacks",
			filepath.Join("<bundle>", "Contents", "Resources", "jarvis-daemon", "main.py"))
	}

	cmd := exec.Command(pythonPath, scriptPath)
	// Tee daemon stderr+stdout to ~/.jarvis/logs/daemon.log so it survives
	// across Jarvis restarts and is readable without keeping a terminal open.
	logWriter := newJarvisLogWriter()
	cmd.Stderr = logWriter
	cmd.Stdout = logWriter
	cmd.Env = append(os.Environ(),
		"PYTHONUNBUFFERED=1",
		"PIPECAT_LOG_LEVEL=WARNING",
	)
	// If bundled models are shipped inside the .app, expose their path to the
	// daemon so it can resolve Whisper/etc. without re-downloading. Empty in
	// dev mode — the daemon falls back to ~/.jarvis/models/ then.
	if modelsDir := paths.BundledModelsDir(); modelsDir != "" {
		cmd.Env = append(cmd.Env, "JARVIS_BUNDLED_MODELS_DIR="+modelsDir)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("StartJarvis: %w", err)
	}

	a.jarvisProcess = cmd.Process
	// Do not reset jarvisRestarts here — the monitor goroutine manages restart count.

	// Monitor the daemon in the background — restarts on unexpected exit.
	go a.monitorJarvisDaemon(cmd)

	slog.Info("jarvis daemon launched",
		"pid", cmd.Process.Pid,
		"python", pythonPath,
		"script", scriptPath,
		"bundled", bundledPython != "",
	)
	return nil
}

// StopJarvis sends SIGINT to the Python daemon and waits briefly for a
// graceful exit. If the daemon does not exit within 3 seconds it is killed.
func (a *App) StopJarvis() {
	a.jarvisMu.Lock()
	if a.jarvisProcess == nil {
		a.jarvisMu.Unlock()
		return
	}

	proc := a.jarvisProcess
	a.jarvisProcess = nil                // Clear first so monitor goroutine does not restart.
	a.jarvisRestarts = maxJarvisRestarts // Prevent monitor from restarting.
	a.jarvisMu.Unlock()

	_ = proc.Signal(os.Interrupt)

	// Give it 3s to shut down, then force-kill.
	// Note: proc.Wait() is already called by monitorJarvisDaemon via cmd.Wait(),
	// so we use a timer-based approach instead of spawning another Wait goroutine.
	done := make(chan struct{})
	go func() {
		proc.Wait()
		close(done)
	}()

	select {
	case <-done:
		slog.Info("jarvis daemon exited gracefully")
	case <-time.After(3 * time.Second):
		_ = proc.Kill()
		slog.Warn("jarvis daemon killed after timeout")
	}
}

// monitorJarvisDaemon waits for the daemon process to exit. If it exits
// unexpectedly (i.e. a.jarvisProcess is still set), it attempts to restart
// with exponential-ish back-off up to maxJarvisRestarts times.
func (a *App) monitorJarvisDaemon(cmd *exec.Cmd) {
	err := cmd.Wait()

	// If jarvisProcess was already cleared, StopJarvis was called — do not restart.
	if a.jarvisProcess == nil {
		return
	}
	a.jarvisProcess = nil

	if err != nil {
		slog.Warn("jarvis daemon exited unexpectedly", "err", err)
	} else {
		slog.Warn("jarvis daemon exited with status 0")
	}

	// Attempt restarts with increasing delay (max 1 restart to avoid spawn storms).
	for i := 0; i < 1; i++ {
		delay := time.Duration(i+1) * 2 * time.Second
		slog.Info("jarvis daemon restart scheduled", "attempt", i+1, "delay", delay)
		time.Sleep(delay)

		// Bail if StopJarvis was called while we were sleeping.
		if a.jarvisRestarts >= maxJarvisRestarts {
			return
		}

		if err := a.StartJarvis(); err != nil {
			slog.Warn("jarvis daemon restart failed", "attempt", i+1, "err", err)
			continue
		}
		slog.Info("jarvis daemon restarted successfully", "attempt", i+1)
		return
	}

	slog.Error("jarvis daemon failed to restart after max attempts", "attempts", maxJarvisRestarts)
}

// ---------------------------------------------------------------------------
// Daemon script discovery
// ---------------------------------------------------------------------------

// findJarvisDaemonScript searches common locations for the jarvis-daemon
// entry-point script and returns the first path that exists, or "".
//
// Order: bundled .app Resources first (production), then source-tree
// candidates relative to CWD and executable (dev mode), then ~/.jarvis fallback.
func findJarvisDaemonScript() string {
	// 1. Bundled .app path takes precedence in production. Returns "" in dev
	//    mode, which lets the existing fallback chain run unchanged.
	if bundled := paths.BundledDaemonScript(); bundled != "" {
		slog.Info("found jarvis daemon script (bundled)", "path", bundled)
		return bundled
	}

	candidates := []string{
		"scripts/jarvis-daemon/main.py",
		"../scripts/jarvis-daemon/main.py",
	}

	// Relative to the running executable (production builds).
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(dir, "scripts", "jarvis-daemon", "main.py"),
			filepath.Join(dir, "..", "scripts", "jarvis-daemon", "main.py"),
		)
	}

	// Home-based fallback.
	candidates = append(candidates,
		paths.DataPath("jarvis-daemon", "main.py"),
	)

	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			slog.Info("found jarvis daemon script", "path", p)
			return p
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Daemon log writer
// ---------------------------------------------------------------------------

// jarvisLogWriter implements io.Writer and forwards each line of daemon
// stderr+stdout output to slog AND appends to ~/.jarvis/logs/daemon.log.
// Keeping a persistent file is what lets us diagnose voice issues without
// asking the user to redirect a terminal.
type jarvisLogWriter struct {
	file *os.File
}

func newJarvisLogWriter() *jarvisLogWriter {
	w := &jarvisLogWriter{}
	logPath := paths.DataPath("logs", "daemon.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err == nil {
		// Truncate on every daemon start so the file only contains the
		// current session -- prevents unbounded growth and makes "tail"
		// always show what the current run is doing.
		if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644); err == nil {
			w.file = f
		}
	}
	return w
}

func (w *jarvisLogWriter) Write(p []byte) (int, error) {
	if w.file != nil {
		_, _ = w.file.Write(p)
	}
	slog.Info("[jarvis-daemon] " + strings.TrimSpace(string(p)))
	return len(p), nil
}

// ---------------------------------------------------------------------------
// Daemon log access
// ---------------------------------------------------------------------------

// OpenDaemonLog opens the Jarvis Python daemon log file
// (~/.jarvis/logs/daemon.log) in the user's default text-editor via macOS'
// Launch Services (`open` command). Used by the first-run download progress
// overlay's "VIEW DAEMON LOG" link.
//
// Returns a wrapped error if the log file does not exist (e.g. the daemon
// has never been launched) or if the `open` command fails.
func (a *App) OpenDaemonLog() error {
	path := paths.DataPath("logs", "daemon.log")
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("OpenDaemonLog: log file not found at %s: %w", path, err)
	}
	if err := exec.Command("open", path).Run(); err != nil {
		return fmt.Errorf("OpenDaemonLog: %w", err)
	}
	slog.Info("opened daemon log", "path", path)
	return nil
}

// ---------------------------------------------------------------------------
// Tool dispatcher — routes daemon tool_call messages to App methods
// ---------------------------------------------------------------------------

// resolveProjectPath resolves a fuzzy project name (from voice STT) to an
// actual repo path on disk. Searches saved projects, discovery root paths,
// and active session CWDs. Uses fuzzy matching to handle STT misrecognition
// (e.g. "Autodesk" → "auth-desk", "my app" → "my-app").
func (a *App) resolveProjectPath(query string) string {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return ""
	}

	// Normalize: strip hyphens/spaces for fuzzy comparison.
	normalize := func(s string) string {
		s = strings.ToLower(s)
		s = strings.ReplaceAll(s, "-", "")
		s = strings.ReplaceAll(s, "_", "")
		s = strings.ReplaceAll(s, " ", "")
		return s
	}
	qNorm := normalize(q)

	type candidate struct {
		path  string
		score int // lower = better
	}
	var candidates []candidate

	// 1. Check active session CWDs (highest priority — already running).
	indicators, _ := a.GetSessionIndicators()
	for _, ind := range indicators {
		base := filepath.Base(ind.CWD)
		baseNorm := normalize(base)
		if baseNorm == qNorm || strings.Contains(baseNorm, qNorm) || strings.Contains(qNorm, baseNorm) {
			candidates = append(candidates, candidate{ind.CWD, 0})
		}
	}

	// 2. Check saved projects.
	saved, _ := a.ListSavedProjects()
	for _, proj := range saved {
		nameNorm := normalize(proj.Name)
		if nameNorm == qNorm || strings.Contains(nameNorm, qNorm) || strings.Contains(qNorm, nameNorm) {
			candidates = append(candidates, candidate{proj.Path, 1})
		}
	}

	// 3. Scan project root paths for matching directories.
	cfg, _ := a.GetConfig()
	for _, root := range cfg.ProjectRootPaths {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			dirNorm := normalize(e.Name())
			if dirNorm == qNorm || strings.Contains(dirNorm, qNorm) || strings.Contains(qNorm, dirNorm) {
				candidates = append(candidates, candidate{filepath.Join(root, e.Name()), 2})
			}
		}
	}

	if len(candidates) == 0 {
		return ""
	}

	// Return best match (lowest score = most specific match).
	best := candidates[0]
	for _, c := range candidates[1:] {
		if c.score < best.score {
			best = c
		}
	}
	return best.path
}

// listKnownProjects returns a short list of known project names for error messages.
func (a *App) listKnownProjects() []string {
	seen := make(map[string]bool)
	var names []string

	indicators, _ := a.GetSessionIndicators()
	for _, ind := range indicators {
		name := filepath.Base(ind.CWD)
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}

	saved, _ := a.ListSavedProjects()
	for _, proj := range saved {
		if !seen[proj.Name] {
			seen[proj.Name] = true
			names = append(names, proj.Name)
		}
	}

	return names
}

// matchSessionByName checks if a project query matches an indicator by CWD
// basename, indicator name, or CMux workspace title. This lets users refer to
// sessions by their custom workspace names (e.g. "Auth Service", "Maya PDP")
// in addition to directory names (e.g. "service-name", "my-app").
func (a *App) matchSessionByName(query string, ind claude.SessionIndicator) bool {
	q := strings.ToLower(query)

	// Normalize: strip hyphens/spaces/underscores for fuzzy voice matching.
	normalize := func(s string) string {
		s = strings.ToLower(s)
		s = strings.ReplaceAll(s, "-", "")
		s = strings.ReplaceAll(s, "_", "")
		s = strings.ReplaceAll(s, " ", "")
		return s
	}
	qNorm := normalize(q)

	// Match by CWD basename — exact and fuzzy.
	base := strings.ToLower(filepath.Base(ind.CWD))
	baseNorm := normalize(base)
	if strings.Contains(base, q) || strings.Contains(baseNorm, qNorm) || strings.Contains(qNorm, baseNorm) {
		return true
	}
	// Match by session name from indicator.
	nameNorm := normalize(ind.Name)
	if strings.Contains(strings.ToLower(ind.Name), q) || strings.Contains(nameNorm, qNorm) || strings.Contains(qNorm, nameNorm) {
		return true
	}
	// Match by CMux workspace title if CMux is available.
	if a.cmuxClient != nil && a.cmuxClient.IsAvailable() {
		workspaces, err := a.cmuxClient.ListWorkspaces()
		if err == nil {
			for _, ws := range workspaces {
				if ws.CurrentDirectory == ind.CWD || filepath.Base(ws.CurrentDirectory) == filepath.Base(ind.CWD) {
					wsNorm := normalize(ws.Title)
					if strings.Contains(strings.ToLower(ws.Title), q) || strings.Contains(wsNorm, qNorm) {
						return true
					}
				}
			}
		}
	}
	return false
}

// findSessionPID resolves a project name to a PID by checking both Claude
// session indicators AND managed sessions from the store (Kiro, Codex, Gemini,
// Aider). Returns (pid, displayName, true) on match, or (0, "", false).
func (a *App) findSessionPID(project string) (int, string, bool) {
	q := strings.ToLower(project)

	// 1. Check Claude session indicators (auto-detected via ~/.claude/).
	indicators, err := a.GetSessionIndicators()
	if err == nil {
		for _, ind := range indicators {
			if a.matchSessionByName(project, ind) {
				slog.Info("findSessionPID: matched", "query", project, "pid", ind.PID, "cwd", filepath.Base(ind.CWD))
				return ind.PID, filepath.Base(ind.CWD), true
			}
		}
	}

	// 2. Check managed sessions from the store (all agent types).
	managed, err := a.GetActiveSessions()
	if err == nil {
		for _, sess := range managed {
			basename := filepath.Base(sess.RepoPath)
			if strings.Contains(strings.ToLower(basename), q) {
				return sess.PID, basename, true
			}
		}
	}

	return 0, "", false
}

func (a *App) dispatchJarvisTool(name string, args map[string]interface{}) map[string]interface{} {
	getStr := func(key string) string {
		v, _ := args[key].(string)
		return v
	}

	slog.Info("dispatchJarvisTool", "name", name, "args", args)

	switch name {
	case "approve_session":
		sessionName := getStr("name")
		approvals, err := a.GetPendingApprovals()
		if err != nil {
			return map[string]interface{}{"ok": false, "message": fmt.Sprintf("failed to get approvals: %v", err)}
		}
		for _, ap := range approvals {
			if strings.Contains(strings.ToLower(ap.SessionName), strings.ToLower(sessionName)) {
				if err := a.RespondToApproval(ap.PID, "y"); err != nil {
					return map[string]interface{}{"ok": false, "message": fmt.Sprintf("approve failed: %v", err)}
				}
				return map[string]interface{}{"ok": true, "message": fmt.Sprintf("Approved %s", ap.SessionName)}
			}
		}
		return map[string]interface{}{"ok": false, "message": fmt.Sprintf("no pending approval for '%s'", sessionName)}

	case "approve_all":
		approvals, err := a.GetPendingApprovals()
		if err != nil {
			return map[string]interface{}{"ok": false, "message": fmt.Sprintf("failed: %v", err)}
		}
		count := 0
		for _, ap := range approvals {
			if err := a.RespondToApproval(ap.PID, "y"); err == nil {
				count++
			}
		}
		return map[string]interface{}{"ok": true, "message": fmt.Sprintf("Approved %d sessions", count)}

	case "deny_session":
		sessionName := getStr("name")
		approvals, _ := a.GetPendingApprovals()
		for _, ap := range approvals {
			if strings.Contains(strings.ToLower(ap.SessionName), strings.ToLower(sessionName)) {
				_ = a.RespondToApproval(ap.PID, "n")
				return map[string]interface{}{"ok": true, "message": fmt.Sprintf("Denied %s", ap.SessionName)}
			}
		}
		return map[string]interface{}{"ok": false, "message": "no matching approval"}

	case "focus_session":
		project := getStr("project")
		pid, name, found := a.findSessionPID(project)
		if !found {
			return map[string]interface{}{"ok": false, "message": fmt.Sprintf("no session matching '%s'", project)}
		}
		if err := a.FocusSession(pid); err != nil {
			return map[string]interface{}{"ok": false, "message": fmt.Sprintf("focus failed: %v", err)}
		}
		return map[string]interface{}{"ok": true, "message": fmt.Sprintf("Focused %s (PID %d)", name, pid)}

	case "focus_app":
		appName := getStr("name")
		if err := a.OpenApp(appName); err != nil {
			return map[string]interface{}{"ok": false, "message": fmt.Sprintf("failed: %v", err)}
		}
		return map[string]interface{}{"ok": true, "message": fmt.Sprintf("Opened %s", appName)}

	case "send_to_terminal", "send_to_session":
		project := getStr("project")
		text := getStr("command")
		if text == "" {
			text = getStr("message")
		}
		pid, name, found := a.findSessionPID(project)
		if !found {
			return map[string]interface{}{"ok": false, "message": fmt.Sprintf("no session matching '%s'", project)}
		}
		if err := a.SendCommandToSession(pid, text); err != nil {
			return map[string]interface{}{"ok": false, "message": fmt.Sprintf("send failed: %v", err)}
		}
		return map[string]interface{}{"ok": true, "message": fmt.Sprintf("Sent to %s", name)}

	case "launch_session":
		project := getStr("project")
		prompt := getStr("prompt")
		agent := getStr("agent")
		if agent == "" {
			agent = "claude-code"
		}
		repoPath := a.resolveProjectPath(project)
		if repoPath == "" {
			// List available projects so Jarvis can suggest
			available := a.listKnownProjects()
			return map[string]interface{}{"ok": false, "message": fmt.Sprintf("no project matching '%s'", project), "available": available}
		}
		sess, err := a.LaunchSession(agent, repoPath, prompt)
		if err != nil {
			return map[string]interface{}{"ok": false, "message": fmt.Sprintf("launch failed: %v", err)}
		}
		return map[string]interface{}{"ok": true, "message": fmt.Sprintf("Launched %s session in %s", agent, filepath.Base(repoPath)), "sessionId": sess.ID}

	case "get_status":
		stats, _ := a.GetDashboardStats()
		spend, _ := a.GetTotalSpend()
		approvals, _ := a.GetPendingApprovals()
		indicators, _ := a.GetSessionIndicators()
		sessions := make([]map[string]interface{}, 0, len(indicators))
		for _, ind := range indicators {
			sessions = append(sessions, map[string]interface{}{
				"name": filepath.Base(ind.CWD), "hasQuestion": ind.HasQuestion,
			})
		}
		return map[string]interface{}{
			"ok": true,
			"stats":     map[string]interface{}{"running": stats.Running, "needsInput": stats.NeedsInput, "total": stats.Total},
			"costs":     map[string]interface{}{"today": spend.Today, "month": spend.ThisMonth},
			"approvals": len(approvals),
			"sessions":  sessions,
		}

	case "navigate_view":
		view := getStr("view")
		runtime.EventsEmit(a.ctx, "jarvis", map[string]interface{}{"type": "navigate", "text": view})
		return map[string]interface{}{"ok": true, "message": fmt.Sprintf("Navigating to %s", view)}

	case "git_stage":
		project := getStr("project")
		repoPath := a.resolveProjectPath(project)
		if repoPath == "" {
			available := a.listKnownProjects()
			return map[string]interface{}{"ok": false, "message": fmt.Sprintf("no project matching '%s'", project), "available": available}
		}
		if err := a.GitStageAll(repoPath); err != nil {
			return map[string]interface{}{"ok": false, "message": fmt.Sprintf("stage failed: %v", err)}
		}
		return map[string]interface{}{"ok": true, "message": fmt.Sprintf("Staged all changes in %s", filepath.Base(repoPath))}

	case "git_commit":
		project := getStr("project")
		message := getStr("message")
		repoPath := a.resolveProjectPath(project)
		if repoPath == "" {
			available := a.listKnownProjects()
			return map[string]interface{}{"ok": false, "message": fmt.Sprintf("no project matching '%s'", project), "available": available}
		}
		if err := a.GitCommit(repoPath, message); err != nil {
			return map[string]interface{}{"ok": false, "message": fmt.Sprintf("commit failed: %v", err)}
		}
		return map[string]interface{}{"ok": true, "message": fmt.Sprintf("Committed in %s", filepath.Base(repoPath))}

	case "git_push":
		project := getStr("project")
		repoPath := a.resolveProjectPath(project)
		if repoPath == "" {
			available := a.listKnownProjects()
			return map[string]interface{}{"ok": false, "message": fmt.Sprintf("no project matching '%s'", project), "available": available}
		}
		if err := a.GitPush(repoPath); err != nil {
			return map[string]interface{}{"ok": false, "message": fmt.Sprintf("push failed: %v", err)}
		}
		return map[string]interface{}{"ok": true, "message": fmt.Sprintf("Pushed %s", filepath.Base(repoPath))}

	case "open_url":
		url := getStr("url")
		if err := a.OpenURL(url); err != nil {
			return map[string]interface{}{"ok": false, "message": fmt.Sprintf("failed: %v", err)}
		}
		return map[string]interface{}{"ok": true, "message": fmt.Sprintf("Opened %s", url)}

	case "read_session_output":
		project := getStr("project")
		pid, name, found := a.findSessionPID(project)
		if !found {
			return map[string]interface{}{"ok": false, "message": fmt.Sprintf("no session matching '%s'", project)}
		}
		output, err := a.GetSessionTerminalOutput(pid)
		if err != nil {
			return map[string]interface{}{"ok": false, "message": fmt.Sprintf("read failed: %v", err)}
		}
		lines := strings.Split(output, "\n")
		if len(lines) > 50 {
			lines = lines[len(lines)-50:]
		}
		return map[string]interface{}{
			"ok":      true,
			"project": name,
			"output":  strings.Join(lines, "\n"),
		}

	case "highlight_hud_panel":
		panel := getStr("panel")   // sessions, costs, approvals, activity
		action := getStr("action") // highlight, flash
		if panel == "" {
			return map[string]interface{}{"ok": false, "message": "panel name required"}
		}
		if action == "" {
			action = "flash"
		}
		runtime.EventsEmit(a.ctx, "jarvis", map[string]interface{}{
			"type":   "hud_action",
			"panel":  panel,
			"action": action,
		})
		return map[string]interface{}{"ok": true, "message": fmt.Sprintf("Highlighting %s panel", panel)}

	case "plan_task":
		goal := getStr("goal")
		// Steps come as an array from the LLM
		stepsRaw, _ := args["steps"].([]interface{})
		steps := make([]string, 0, len(stepsRaw))
		for _, s := range stepsRaw {
			if str, ok := s.(string); ok {
				steps = append(steps, str)
			}
		}
		// Emit plan to frontend HUD
		runtime.EventsEmit(a.ctx, "jarvis", map[string]interface{}{
			"type":  "plan",
			"goal":  goal,
			"steps": steps,
		})
		return map[string]interface{}{
			"ok":      true,
			"message": fmt.Sprintf("Plan created with %d steps for: %s", len(steps), goal),
			"goal":    goal,
			"steps":   steps,
		}

	case "create_todo":
		project := getStr("project")
		title := getStr("title")
		// Find session by project name to get session ID
		pid, sessName, found := a.findSessionPID(project)
		if !found {
			return map[string]interface{}{"ok": false, "message": fmt.Sprintf("no session matching '%s'", project)}
		}
		// Look up the session ID from the store using PID
		sessions, _ := a.GetActiveSessions()
		var sessionID string
		for _, s := range sessions {
			if s.PID == pid {
				sessionID = s.ID
				break
			}
		}
		if sessionID == "" {
			// Fallback: use project name as identifier
			return map[string]interface{}{"ok": false, "message": fmt.Sprintf("could not find session ID for %s", sessName)}
		}
		todo, err := a.store.CreateSessionTodo(sessionID, title)
		if err != nil {
			return map[string]interface{}{"ok": false, "message": fmt.Sprintf("create todo failed: %v", err)}
		}
		return map[string]interface{}{"ok": true, "message": fmt.Sprintf("Added to %s: %s", sessName, todo.Title)}

	case "complete_todo":
		project := getStr("project")
		title := getStr("title")
		pid, sessName, found := a.findSessionPID(project)
		if !found {
			return map[string]interface{}{"ok": false, "message": fmt.Sprintf("no session matching '%s'", project)}
		}
		sessions, _ := a.GetActiveSessions()
		var sessionID string
		for _, s := range sessions {
			if s.PID == pid {
				sessionID = s.ID
				break
			}
		}
		if sessionID == "" {
			return map[string]interface{}{"ok": false, "message": fmt.Sprintf("could not find session ID for %s", sessName)}
		}
		// Find matching todo by substring
		todos, _ := a.store.ListSessionTodos(sessionID)
		titleLower := strings.ToLower(title)
		for _, t := range todos {
			if t.Status == "pending" && strings.Contains(strings.ToLower(t.Title), titleLower) {
				if err := a.store.UpdateSessionTodo(t.ID, "done"); err != nil {
					return map[string]interface{}{"ok": false, "message": fmt.Sprintf("update failed: %v", err)}
				}
				return map[string]interface{}{"ok": true, "message": fmt.Sprintf("Marked as done: %s", t.Title)}
			}
		}
		return map[string]interface{}{"ok": false, "message": fmt.Sprintf("no pending todo matching '%s' in %s", title, sessName)}

	case "run_workflow":
		phasesRaw, _ := args["phases"].([]interface{})
		if len(phasesRaw) == 0 {
			return map[string]interface{}{"ok": false, "message": "no phases provided"}
		}
		phases := make([]WorkflowPhase, 0, len(phasesRaw))
		for _, p := range phasesRaw {
			pm, _ := p.(map[string]interface{})
			if pm == nil {
				continue
			}
			phases = append(phases, WorkflowPhase{
				AgentType: fmt.Sprintf("%v", pm["agentType"]),
				RepoPath:  fmt.Sprintf("%v", pm["repoPath"]),
				Prompt:    fmt.Sprintf("%v", pm["prompt"]),
				Phase:     fmt.Sprintf("%v", pm["phase"]),
			})
		}
		if err := a.ExecuteWorkflow(phases); err != nil {
			return map[string]interface{}{"ok": false, "message": fmt.Sprintf("workflow failed: %v", err)}
		}
		return map[string]interface{}{"ok": true, "message": fmt.Sprintf("Workflow started with %d phases", len(phases))}

	// ----- TASK-001: Discovery -----

	case "discover_projects":
		projects, err := a.DiscoverProjects()
		if err != nil {
			return map[string]interface{}{"ok": false, "message": fmt.Sprintf("discovery failed: %v", err)}
		}
		repos := make([]map[string]interface{}, 0)
		for _, proj := range projects {
			for _, repo := range proj.Repos {
				repos = append(repos, map[string]interface{}{
					"name":     repo.Name,
					"path":     repo.Path,
					"language": repo.Language,
					"branch":   repo.Branch,
					"hasAgent": repo.HasAgent,
				})
			}
		}
		return map[string]interface{}{"ok": true, "repos": repos}

	// ----- TASK-003: Discovery and repo info tools -----

	case "search_repos":
		query := getStr("query")
		results, err := a.SearchRepos(query)
		if err != nil {
			return map[string]interface{}{"ok": false, "message": fmt.Sprintf("search failed: %v", err)}
		}
		compact := make([]map[string]interface{}, 0, len(results))
		for _, r := range results {
			compact = append(compact, map[string]interface{}{
				"name":     r.Name,
				"path":     r.Path,
				"language": r.Language,
				"branch":   r.Branch,
				"hasAgent": r.HasAgent,
			})
		}
		return map[string]interface{}{"ok": true, "results": compact}

	case "get_repo_info":
		project := getStr("project")
		repoPath := a.resolveProjectPath(project)
		if repoPath == "" {
			return map[string]interface{}{"ok": false, "message": fmt.Sprintf("no project matching '%s'", project), "available": a.listKnownProjects()}
		}
		info, err := a.GetRepoInfo(repoPath)
		if err != nil {
			return map[string]interface{}{"ok": false, "message": fmt.Sprintf("info failed: %v", err)}
		}
		return map[string]interface{}{
			"ok":               true,
			"name":             filepath.Base(repoPath),
			"branch":           info.Branch,
			"remote":           info.RemoteURL,
			"uncommittedFiles": info.FilesChanged,
			"lastCommit":       info.LastCommitMsg,
			"isClean":          info.IsClean,
			"hasUnpushed":      info.HasUnpushed,
		}

	// ----- TASK-004: Git diff tools -----

	case "get_repo_diff":
		project := getStr("project")
		repoPath := a.resolveProjectPath(project)
		if repoPath == "" {
			return map[string]interface{}{"ok": false, "message": fmt.Sprintf("no project matching '%s'", project), "available": a.listKnownProjects()}
		}
		diff, err := a.GetRepoDiff(repoPath)
		if err != nil {
			return map[string]interface{}{"ok": false, "message": fmt.Sprintf("diff failed: %v", err)}
		}
		files := make([]map[string]interface{}, 0, len(diff.Files))
		for _, f := range diff.Files {
			ins, del := 0, 0
			for _, h := range f.Hunks {
				for _, l := range h.Lines {
					if l.Type == "add" {
						ins++
					} else if l.Type == "delete" {
						del++
					}
				}
			}
			files = append(files, map[string]interface{}{
				"path":       f.Path,
				"status":     f.Status,
				"insertions": ins,
				"deletions":  del,
			})
		}
		// Build a compact raw diff (truncated to 2KB for voice context).
		rawParts := make([]string, 0, len(diff.Files))
		for _, f := range diff.Files {
			rawParts = append(rawParts, fmt.Sprintf("--- %s (%s)", f.Path, f.Status))
		}
		diffText := strings.Join(rawParts, "\n")
		if len(diffText) > 2048 {
			diffText = diffText[:2048] + "\n... (truncated)"
		}
		return map[string]interface{}{
			"ok":    true,
			"files": files,
			"stats": map[string]interface{}{
				"filesChanged": diff.Stats.FilesChanged,
				"insertions":   diff.Stats.Insertions,
				"deletions":    diff.Stats.Deletions,
			},
			"diffText": diffText,
		}

	case "get_staged_diff":
		project := getStr("project")
		repoPath := a.resolveProjectPath(project)
		if repoPath == "" {
			return map[string]interface{}{"ok": false, "message": fmt.Sprintf("no project matching '%s'", project), "available": a.listKnownProjects()}
		}
		diff, err := a.GetStagedDiff(repoPath)
		if err != nil {
			return map[string]interface{}{"ok": false, "message": fmt.Sprintf("staged diff failed: %v", err)}
		}
		files := make([]map[string]interface{}, 0, len(diff.Files))
		for _, f := range diff.Files {
			ins, del := 0, 0
			for _, h := range f.Hunks {
				for _, l := range h.Lines {
					if l.Type == "add" {
						ins++
					} else if l.Type == "delete" {
						del++
					}
				}
			}
			files = append(files, map[string]interface{}{
				"path":       f.Path,
				"status":     f.Status,
				"insertions": ins,
				"deletions":  del,
			})
		}
		return map[string]interface{}{
			"ok":    true,
			"files": files,
			"stats": map[string]interface{}{
				"filesChanged": diff.Stats.FilesChanged,
				"insertions":   diff.Stats.Insertions,
				"deletions":    diff.Stats.Deletions,
			},
		}

	// ----- TASK-005: Git advanced ops -----

	case "git_create_branch":
		project := getStr("project")
		branchName := getStr("name")
		repoPath := a.resolveProjectPath(project)
		if repoPath == "" {
			return map[string]interface{}{"ok": false, "message": fmt.Sprintf("no project matching '%s'", project), "available": a.listKnownProjects()}
		}
		if err := a.GitCreateBranch(repoPath, branchName); err != nil {
			return map[string]interface{}{"ok": false, "message": fmt.Sprintf("branch creation failed: %v", err)}
		}
		return map[string]interface{}{"ok": true, "message": fmt.Sprintf("Created and switched to branch '%s' in %s", branchName, filepath.Base(repoPath))}

	case "git_stash":
		project := getStr("project")
		message := getStr("message")
		repoPath := a.resolveProjectPath(project)
		if repoPath == "" {
			return map[string]interface{}{"ok": false, "message": fmt.Sprintf("no project matching '%s'", project), "available": a.listKnownProjects()}
		}
		if message == "" {
			message = "jarvis-stash"
		}
		if err := a.GitStash(repoPath, message); err != nil {
			return map[string]interface{}{"ok": false, "message": fmt.Sprintf("stash failed: %v", err)}
		}
		return map[string]interface{}{"ok": true, "message": fmt.Sprintf("Stashed changes in %s as '%s'", filepath.Base(repoPath), message)}

	case "git_stash_list":
		project := getStr("project")
		repoPath := a.resolveProjectPath(project)
		if repoPath == "" {
			return map[string]interface{}{"ok": false, "message": fmt.Sprintf("no project matching '%s'", project), "available": a.listKnownProjects()}
		}
		entries, err := a.GitStashList(repoPath)
		if err != nil {
			return map[string]interface{}{"ok": false, "message": fmt.Sprintf("stash list failed: %v", err)}
		}
		stashes := make([]map[string]interface{}, 0, len(entries))
		for _, e := range entries {
			stashes = append(stashes, map[string]interface{}{
				"index": e.Index,
				"name":  e.Name,
			})
		}
		return map[string]interface{}{"ok": true, "stashes": stashes}

	case "git_stash_apply":
		project := getStr("project")
		repoPath := a.resolveProjectPath(project)
		if repoPath == "" {
			return map[string]interface{}{"ok": false, "message": fmt.Sprintf("no project matching '%s'", project), "available": a.listKnownProjects()}
		}
		index := 0
		if idxRaw, ok := args["index"]; ok {
			if idxFloat, ok := idxRaw.(float64); ok {
				index = int(idxFloat)
			}
		}
		if err := a.GitStashApply(repoPath, index); err != nil {
			return map[string]interface{}{"ok": false, "message": fmt.Sprintf("stash apply failed: %v", err)}
		}
		return map[string]interface{}{"ok": true, "message": fmt.Sprintf("Applied stash@{%d} in %s", index, filepath.Base(repoPath))}

	case "git_discard_file":
		project := getStr("project")
		filePath := getStr("file")
		repoPath := a.resolveProjectPath(project)
		if repoPath == "" {
			return map[string]interface{}{"ok": false, "message": fmt.Sprintf("no project matching '%s'", project), "available": a.listKnownProjects()}
		}
		if err := a.GitDiscardFile(repoPath, filePath); err != nil {
			return map[string]interface{}{"ok": false, "message": fmt.Sprintf("discard failed: %v", err)}
		}
		return map[string]interface{}{"ok": true, "message": fmt.Sprintf("Discarded changes to %s in %s", filePath, filepath.Base(repoPath))}

	// ----- TASK-009: Session management tools -----

	case "stop_session":
		project := getStr("project")
		// First try to find by PID (Claude session indicators).
		pid, name, found := a.findSessionPID(project)
		if found {
			// Try to match PID to a managed session ID for StopSession.
			sessions, _ := a.GetActiveSessions()
			for _, s := range sessions {
				if s.PID == pid {
					if err := a.StopSession(s.ID); err != nil {
						return map[string]interface{}{"ok": false, "message": fmt.Sprintf("stop failed: %v", err)}
					}
					return map[string]interface{}{"ok": true, "message": fmt.Sprintf("Stopped session %s", name)}
				}
			}
			// Fallback: send interrupt via terminal (for auto-detected sessions without managed ID).
			if err := a.SendCommandToSession(pid, "\x03"); err != nil {
				return map[string]interface{}{"ok": false, "message": fmt.Sprintf("stop via signal failed: %v", err)}
			}
			return map[string]interface{}{"ok": true, "message": fmt.Sprintf("Sent interrupt to %s (PID %d)", name, pid)}
		}
		// Try matching by repo path against managed sessions.
		sessions, _ := a.GetActiveSessions()
		q := strings.ToLower(project)
		for _, s := range sessions {
			if strings.Contains(strings.ToLower(filepath.Base(s.RepoPath)), q) {
				if err := a.StopSession(s.ID); err != nil {
					return map[string]interface{}{"ok": false, "message": fmt.Sprintf("stop failed: %v", err)}
				}
				return map[string]interface{}{"ok": true, "message": fmt.Sprintf("Stopped session in %s", filepath.Base(s.RepoPath))}
			}
		}
		return map[string]interface{}{"ok": false, "message": fmt.Sprintf("no session matching '%s'", project)}

	case "broadcast_to_all":
		command := getStr("command")
		results, err := a.BroadcastToAll(command)
		if err != nil {
			return map[string]interface{}{"ok": false, "message": fmt.Sprintf("broadcast failed: %v", err)}
		}
		successes := 0
		for _, v := range results {
			if v == "" || strings.Contains(v, "ok") || !strings.Contains(strings.ToLower(v), "error") {
				successes++
			}
		}
		return map[string]interface{}{"ok": true, "message": fmt.Sprintf("Broadcast sent to %d sessions (%d succeeded)", len(results), successes)}

	case "open_pr":
		project := getStr("project")
		repoPath := a.resolveProjectPath(project)
		if repoPath == "" {
			return map[string]interface{}{"ok": false, "message": fmt.Sprintf("no project matching '%s'", project), "available": a.listKnownProjects()}
		}
		if err := a.OpenPRInBrowser(repoPath); err != nil {
			return map[string]interface{}{"ok": false, "message": fmt.Sprintf("open PR failed: %v", err)}
		}
		return map[string]interface{}{"ok": true, "message": fmt.Sprintf("Opened PR creation page for %s", filepath.Base(repoPath))}

	// ----- TASK-010: Workspace and orchestration tools -----

	case "create_workspace":
		wsName := getStr("name")
		prompt := getStr("prompt")
		reposRaw, _ := args["repos"].([]interface{})
		resolvedPaths := make([]string, 0, len(reposRaw))
		for _, r := range reposRaw {
			if name, ok := r.(string); ok {
				p := a.resolveProjectPath(name)
				if p != "" {
					resolvedPaths = append(resolvedPaths, p)
				}
			}
		}
		if len(resolvedPaths) == 0 {
			return map[string]interface{}{"ok": false, "message": "no valid repos resolved", "available": a.listKnownProjects()}
		}
		ws, err := a.CreateWorkspaceAndLaunch(wsName, resolvedPaths, prompt)
		if err != nil {
			return map[string]interface{}{"ok": false, "message": fmt.Sprintf("workspace creation failed: %v", err)}
		}
		return map[string]interface{}{"ok": true, "message": fmt.Sprintf("Workspace '%s' created with %d repos", wsName, len(resolvedPaths)), "path": ws.Path}

	case "divide_and_conquer":
		agent := getStr("agent")
		if agent == "" {
			agent = "claude-code"
		}
		prompt := getStr("prompt")
		sequential := false
		if seqRaw, ok := args["sequential"]; ok {
			if seqBool, ok := seqRaw.(bool); ok {
				sequential = seqBool
			}
		}
		reposRaw, _ := args["repos"].([]interface{})
		resolvedPaths := make([]string, 0, len(reposRaw))
		for _, r := range reposRaw {
			if name, ok := r.(string); ok {
				p := a.resolveProjectPath(name)
				if p != "" {
					resolvedPaths = append(resolvedPaths, p)
				}
			}
		}
		if len(resolvedPaths) == 0 {
			return map[string]interface{}{"ok": false, "message": "no valid repos resolved", "available": a.listKnownProjects()}
		}
		if err := a.ExecuteDivideAndConquer(agent, resolvedPaths, prompt, sequential); err != nil {
			return map[string]interface{}{"ok": false, "message": fmt.Sprintf("divide and conquer failed: %v", err)}
		}
		mode := "parallel"
		if sequential {
			mode = "sequential"
		}
		return map[string]interface{}{"ok": true, "message": fmt.Sprintf("Launched %s across %d repos (%s)", agent, len(resolvedPaths), mode)}

	case "get_impact_warnings":
		warnings, err := a.GetImpactWarnings()
		if err != nil {
			return map[string]interface{}{"ok": false, "message": fmt.Sprintf("impact check failed: %v", err)}
		}
		compact := make([]map[string]interface{}, 0, len(warnings))
		for _, w := range warnings {
			compact = append(compact, map[string]interface{}{
				"severity":     string(w.Severity),
				"description":  w.Description,
				"sessionA":     w.SessionA,
				"sessionB":     w.SessionB,
				"conflictType": w.ConflictType,
			})
		}
		return map[string]interface{}{"ok": true, "warnings": compact, "count": len(warnings)}

	case "launch_from_template":
		templateID := getStr("templateId")
		if err := a.LaunchFromTemplate(templateID); err != nil {
			return map[string]interface{}{"ok": false, "message": fmt.Sprintf("template launch failed: %v", err)}
		}
		return map[string]interface{}{"ok": true, "message": fmt.Sprintf("Launched sessions from template %s", templateID)}

	default:
		return map[string]interface{}{"ok": false, "message": fmt.Sprintf("unknown tool: %s", name)}
	}
}

// ---------------------------------------------------------------------------
// Legacy Go-native Jarvis (retained for fallback/reference, not called)
// ---------------------------------------------------------------------------

// startJarvisLegacy is the original Go-native Jarvis pipeline. It has been
// superseded by the Python daemon launched in StartJarvis.. Kept for
// reference and potential fallback.
func (a *App) startJarvisLegacy() error {
	if a.jarvis != nil {
		return fmt.Errorf("startJarvisLegacy: already running")
	}

	cfg := config.Get()
	vcfg := jarvis.JarvisConfig{
		Enabled:             cfg.JarvisEnabled,
		Provider:            cfg.JarvisProvider,
		APIKey:              cfg.JarvisAPIKey,
		Voice:               cfg.JarvisVoice,
		AmbientEnabled:      cfg.JarvisAmbientEnabled,
		Verbosity:           cfg.JarvisVerbosity,
		PicovoiceAccessKey:  cfg.JarvisPicovoiceKey,
		WakeWordModelPath:   cfg.JarvisWakeWordModel,
		WakeWordSensitivity: cfg.JarvisWakeSensitivity,
		ElevenLabsKey:       cfg.JarvisElevenLabsKey,
		ElevenLabsVoiceID:   cfg.JarvisElevenLabsVoice,
	}

	emitFn := func(event jarvis.JarvisEvent) {
		runtime.EventsEmit(a.ctx, "jarvis", event)
	}

	v := jarvis.NewJarvis(vcfg, a, a, a.GetSessionTerminalOutput, emitFn)
	if err := v.Start(a.ctx); err != nil {
		return fmt.Errorf("startJarvisLegacy: %w", err)
	}

	a.jarvis = v
	slog.Info("jarvis legacy started")
	return nil
}

// stopJarvisLegacy stops the Go-native Jarvis voice companion if it is running.
func (a *App) stopJarvisLegacy() {
	if a.jarvis == nil {
		return
	}
	a.jarvis.Stop()
	a.jarvis = nil
	slog.Info("jarvis legacy stopped")
}

// GetJarvisState returns the current Jarvis state as a string. Returns "idle"
// if Jarvis has not been initialised.
func (a *App) GetJarvisState() string {
	// Check daemon connection first (new path).
	if a.jarvisProcess != nil {
		conn := a.jarvisDaemonConn()
		if conn != nil && conn.Connected() {
			return "running"
		}
		return "launching"
	}
	// Legacy Go-native fallback.
	if a.jarvis == nil {
		return "idle"
	}
	return string(a.jarvis.GetState())
}

// SendJarvisMessage sends a text message to Jarvis and returns the response.
// This is the text-only fallback that works without microphone access.
func (a *App) SendJarvisMessage(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("SendJarvisMessage: text is required")
	}
	if a.jarvis == nil {
		return "", fmt.Errorf("SendJarvisMessage: jarvis not running")
	}
	resp, err := a.jarvis.SendMessage(text)
	if err != nil {
		return "", fmt.Errorf("SendJarvisMessage: %w", err)
	}
	return resp, nil
}

// GetJarvisHistory returns the conversation history. Returns an empty slice
// if Jarvis has not been initialised.
func (a *App) GetJarvisHistory() []jarvis.Message {
	if a.jarvis == nil {
		return []jarvis.Message{}
	}
	history := a.jarvis.GetHistory()
	if history == nil {
		return []jarvis.Message{}
	}
	return history
}

// SetJarvisConfig persists updated Jarvis configuration and restarts the
// daemon if it is currently running so the new settings take effect.
func (a *App) SetJarvisConfig(vcfg jarvis.JarvisConfig) error {
	cfg := config.Get()
	cfg.JarvisEnabled = vcfg.Enabled
	cfg.JarvisProvider = vcfg.Provider
	cfg.JarvisAPIKey = vcfg.APIKey
	cfg.JarvisVoice = vcfg.Voice
	cfg.JarvisAmbientEnabled = vcfg.AmbientEnabled
	cfg.JarvisVerbosity = vcfg.Verbosity
	cfg.JarvisPicovoiceKey = vcfg.PicovoiceAccessKey
	cfg.JarvisWakeWordModel = vcfg.WakeWordModelPath
	cfg.JarvisWakeSensitivity = vcfg.WakeWordSensitivity

	if err := config.Save(cfg); err != nil {
		return fmt.Errorf("SetJarvisConfig: %w", err)
	}

	// Stop existing daemon/legacy if running.
	a.StopJarvis()
	a.stopJarvisLegacy()

	if vcfg.Enabled {
		if err := a.StartJarvis(); err != nil {
			return fmt.Errorf("SetJarvisConfig: start failed: %w", err)
		}
	}

	return nil
}

// SendJarvisCommand sends a text command to the Jarvis Python daemon via the
// WebSocket connection. This is called when the user types in the HUD
// input bar. Returns an error if the daemon is not connected.
func (a *App) SendJarvisCommand(text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("SendJarvisCommand: text is required")
	}

	conn := a.jarvisDaemonConn()
	if conn == nil {
		return fmt.Errorf("SendJarvisCommand: daemon not connected")
	}

	msg := api.JarvisOutgoing{
		Type: "command",
		Text: text,
	}
	if err := conn.Send(msg); err != nil {
		return fmt.Errorf("SendJarvisCommand: %w", err)
	}

	slog.Debug("sent command to jarvis daemon", "text", text)
	return nil
}

// IsJarvisDaemonConnected reports whether the Python daemon has an active
// WebSocket connection.
func (a *App) IsJarvisDaemonConnected() bool {
	conn := a.jarvisDaemonConn()
	return conn != nil && conn.Connected()
}

// jarvisDaemonConn returns the active JarvisDaemonConn from the API server,
// or nil if the server is not running.
func (a *App) jarvisDaemonConn() *api.JarvisDaemonConn {
	if a.apiServer == nil {
		return nil
	}
	return a.apiServer.JarvisConn()
}
