package store

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/namanchopra/jarvis/internal/model"
)

// newTestStore creates a Store backed by a temporary SQLite database. The
// database file is automatically cleaned up when the test finishes.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore(%q): %v", dbPath, err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestNewStore(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sub", "test.db")

	s, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore(%q): %v", dbPath, err)
	}
	defer s.Close()

	// The database file should have been created on disk.
	info, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("os.Stat(%q): %v", dbPath, err)
	}
	if info.IsDir() {
		t.Fatalf("expected %q to be a file, got directory", dbPath)
	}
}

func TestCreateAndGetTask(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)

	task := model.NewTask("implement-auth", "Add JWT authentication", "/repos/api", model.AgentClaudeCode)
	task.OutputPath = "/tmp/auth.log"

	created, err := s.CreateTask(task)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	got, err := s.GetTask(created.ID)
	if err != nil {
		t.Fatalf("GetTask(%q): %v", created.ID, err)
	}

	// Verify all fields round-trip correctly.
	if got.ID != task.ID {
		t.Errorf("ID = %q, want %q", got.ID, task.ID)
	}
	if got.Name != task.Name {
		t.Errorf("Name = %q, want %q", got.Name, task.Name)
	}
	if got.Description != task.Description {
		t.Errorf("Description = %q, want %q", got.Description, task.Description)
	}
	if got.RepoPath != task.RepoPath {
		t.Errorf("RepoPath = %q, want %q", got.RepoPath, task.RepoPath)
	}
	if got.AgentType != task.AgentType {
		t.Errorf("AgentType = %q, want %q", got.AgentType, task.AgentType)
	}
	if got.Status != task.Status {
		t.Errorf("Status = %q, want %q", got.Status, task.Status)
	}
	if got.OutputPath != task.OutputPath {
		t.Errorf("OutputPath = %q, want %q", got.OutputPath, task.OutputPath)
	}

	// Timestamps lose sub-second precision through RFC 3339 round-trip.
	// Compare truncated to the second.
	if !got.CreatedAt.Truncate(time.Second).Equal(task.CreatedAt.Truncate(time.Second)) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, task.CreatedAt)
	}
	if !got.UpdatedAt.Truncate(time.Second).Equal(task.UpdatedAt.Truncate(time.Second)) {
		t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, task.UpdatedAt)
	}
}

func TestListTasks(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)

	// Create three tasks with different statuses.
	tasks := []model.Task{
		model.NewTask("task-1", "first", "/repos/a", model.AgentClaudeCode),
		model.NewTask("task-2", "second", "/repos/b", model.AgentGemini),
		model.NewTask("task-3", "third", "/repos/c", model.AgentAider),
	}
	// Manually set statuses so we can filter.
	tasks[1].Status = model.StatusRunning
	tasks[2].Status = model.StatusDone

	for i, task := range tasks {
		if _, err := s.CreateTask(task); err != nil {
			t.Fatalf("CreateTask[%d]: %v", i, err)
		}
	}

	// List all tasks (no filter).
	all, err := s.ListTasks("", "")
	if err != nil {
		t.Fatalf("ListTasks (all): %v", err)
	}
	if len(all) != 3 {
		t.Errorf("ListTasks (all) returned %d tasks, want 3", len(all))
	}

	// Filter by status = pending.
	pending, err := s.ListTasks("pending", "")
	if err != nil {
		t.Fatalf("ListTasks (pending): %v", err)
	}
	if len(pending) != 1 {
		t.Errorf("ListTasks (pending) returned %d tasks, want 1", len(pending))
	}

	// Filter by status = running.
	running, err := s.ListTasks("running", "")
	if err != nil {
		t.Fatalf("ListTasks (running): %v", err)
	}
	if len(running) != 1 {
		t.Errorf("ListTasks (running) returned %d tasks, want 1", len(running))
	}

	// Filter by status = done.
	done, err := s.ListTasks("done", "")
	if err != nil {
		t.Fatalf("ListTasks (done): %v", err)
	}
	if len(done) != 1 {
		t.Errorf("ListTasks (done) returned %d tasks, want 1", len(done))
	}

	// Filter by non-existent status returns empty.
	none, err := s.ListTasks("failed", "")
	if err != nil {
		t.Fatalf("ListTasks (failed): %v", err)
	}
	if len(none) != 0 {
		t.Errorf("ListTasks (failed) returned %d tasks, want 0", len(none))
	}
}

func TestListTasksRepoFilter(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)

	tasks := []model.Task{
		model.NewTask("task-a", "desc", "/home/user/repos/frontend", model.AgentClaudeCode),
		model.NewTask("task-b", "desc", "/home/user/repos/backend", model.AgentGemini),
		model.NewTask("task-c", "desc", "/home/user/other/scripts", model.AgentAider),
	}
	for i, task := range tasks {
		if _, err := s.CreateTask(task); err != nil {
			t.Fatalf("CreateTask[%d]: %v", i, err)
		}
	}

	// Filter by "repos" should match the first two.
	repoTasks, err := s.ListTasks("", "repos")
	if err != nil {
		t.Fatalf("ListTasks (repo=repos): %v", err)
	}
	if len(repoTasks) != 2 {
		t.Errorf("ListTasks (repo=repos) returned %d tasks, want 2", len(repoTasks))
	}

	// Filter by "frontend" should match exactly one.
	frontendTasks, err := s.ListTasks("", "frontend")
	if err != nil {
		t.Fatalf("ListTasks (repo=frontend): %v", err)
	}
	if len(frontendTasks) != 1 {
		t.Errorf("ListTasks (repo=frontend) returned %d tasks, want 1", len(frontendTasks))
	}

	// Filter by "nonexistent" should match nothing.
	noTasks, err := s.ListTasks("", "nonexistent")
	if err != nil {
		t.Fatalf("ListTasks (repo=nonexistent): %v", err)
	}
	if len(noTasks) != 0 {
		t.Errorf("ListTasks (repo=nonexistent) returned %d tasks, want 0", len(noTasks))
	}
}

func TestUpdateTask(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)

	task := model.NewTask("update-me", "original description", "/repos/app", model.AgentCodex)
	created, err := s.CreateTask(task)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// Small sleep to ensure updated_at will differ from created_at after the
	// round-trip through RFC 3339 (second-level precision).
	time.Sleep(1100 * time.Millisecond)

	updated, err := s.UpdateTask(created.ID, map[string]interface{}{
		"status": "running",
	})
	if err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}

	if updated.Status != model.StatusRunning {
		t.Errorf("Status = %q, want %q", updated.Status, model.StatusRunning)
	}

	// updated_at should be strictly after created_at.
	if !updated.UpdatedAt.After(created.CreatedAt.Truncate(time.Second)) {
		t.Errorf("UpdatedAt (%v) should be after CreatedAt (%v)", updated.UpdatedAt, created.CreatedAt)
	}
}

func TestUpdateTaskInvalidTransition(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)

	task := model.NewTask("blocked", "desc", "/repos/app", model.AgentGemini)
	created, err := s.CreateTask(task)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// Transition: pending -> running -> done.
	if _, err := s.UpdateTask(created.ID, map[string]interface{}{"status": "running"}); err != nil {
		t.Fatalf("UpdateTask (pending->running): %v", err)
	}
	if _, err := s.UpdateTask(created.ID, map[string]interface{}{"status": "done"}); err != nil {
		t.Fatalf("UpdateTask (running->done): %v", err)
	}

	// Attempt: done -> pending (forbidden).
	_, err = s.UpdateTask(created.ID, map[string]interface{}{"status": "pending"})
	if err == nil {
		t.Fatal("UpdateTask (done->pending) should have returned an error, got nil")
	}

	// The error should wrap ErrInvalidStatusTransition.
	if !errors.Is(err, model.ErrInvalidStatusTransition) {
		t.Errorf("expected error wrapping ErrInvalidStatusTransition, got: %v", err)
	}
}

func TestDeleteTask(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)

	task := model.NewTask("delete-me", "desc", "/repos/app", model.AgentAider)
	created, err := s.CreateTask(task)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	if err := s.DeleteTask(created.ID); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}

	// GetTask should return an error wrapping sql.ErrNoRows.
	_, err = s.GetTask(created.ID)
	if err == nil {
		t.Fatal("GetTask after delete should have returned an error, got nil")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expected error wrapping sql.ErrNoRows, got: %v", err)
	}
}

func TestDeleteNonexistent(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)

	err := s.DeleteTask("nonexistent-id-that-does-not-exist")
	if err == nil {
		t.Fatal("DeleteTask for non-existent ID should have returned an error, got nil")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expected error wrapping sql.ErrNoRows, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Session CRUD Tests
// ---------------------------------------------------------------------------

func TestCreateAndGetSession(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)

	sess := model.NewSession(model.AgentClaudeCode, "/repos/myapp", "implement auth")
	sess.TaskID = "task-123"
	sess.AgentSessionID = "claude-sess-abc"
	sess.OutputPath = "/tmp/session.log"
	sess.PID = 12345

	created, err := s.CreateSession(sess)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	got, err := s.GetSession(created.ID)
	if err != nil {
		t.Fatalf("GetSession(%q): %v", created.ID, err)
	}

	// Verify all fields round-trip correctly.
	if got.ID != sess.ID {
		t.Errorf("ID = %q, want %q", got.ID, sess.ID)
	}
	if got.TaskID != sess.TaskID {
		t.Errorf("TaskID = %q, want %q", got.TaskID, sess.TaskID)
	}
	if got.AgentType != sess.AgentType {
		t.Errorf("AgentType = %q, want %q", got.AgentType, sess.AgentType)
	}
	if got.RepoPath != sess.RepoPath {
		t.Errorf("RepoPath = %q, want %q", got.RepoPath, sess.RepoPath)
	}
	if got.Prompt != sess.Prompt {
		t.Errorf("Prompt = %q, want %q", got.Prompt, sess.Prompt)
	}
	if got.AgentSessionID != sess.AgentSessionID {
		t.Errorf("AgentSessionID = %q, want %q", got.AgentSessionID, sess.AgentSessionID)
	}
	if got.Status != sess.Status {
		t.Errorf("Status = %q, want %q", got.Status, sess.Status)
	}
	if got.PID != sess.PID {
		t.Errorf("PID = %d, want %d", got.PID, sess.PID)
	}
	if got.OutputPath != sess.OutputPath {
		t.Errorf("OutputPath = %q, want %q", got.OutputPath, sess.OutputPath)
	}
	if got.ExitCode != sess.ExitCode {
		t.Errorf("ExitCode = %d, want %d", got.ExitCode, sess.ExitCode)
	}
	if got.ErrorMessage != sess.ErrorMessage {
		t.Errorf("ErrorMessage = %q, want %q", got.ErrorMessage, sess.ErrorMessage)
	}

	// Timestamps lose sub-second precision through RFC 3339 round-trip.
	if !got.CreatedAt.Truncate(time.Second).Equal(sess.CreatedAt.Truncate(time.Second)) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, sess.CreatedAt)
	}
	if !got.UpdatedAt.Truncate(time.Second).Equal(sess.UpdatedAt.Truncate(time.Second)) {
		t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, sess.UpdatedAt)
	}
}

func TestListSessions(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)

	// Create three sessions with different statuses.
	sessions := []model.Session{
		model.NewSession(model.AgentClaudeCode, "/repos/a", "prompt one"),
		model.NewSession(model.AgentGemini, "/repos/b", "prompt two"),
		model.NewSession(model.AgentAider, "/repos/c", "prompt three"),
	}
	sessions[1].Status = model.SessionRunning
	sessions[2].Status = model.SessionCompleted

	for i, sess := range sessions {
		if _, err := s.CreateSession(sess); err != nil {
			t.Fatalf("CreateSession[%d]: %v", i, err)
		}
	}

	// List all sessions (no filter).
	all, err := s.ListSessions("")
	if err != nil {
		t.Fatalf("ListSessions (all): %v", err)
	}
	if len(all) != 3 {
		t.Errorf("ListSessions (all) returned %d sessions, want 3", len(all))
	}

	// Filter by status = launching.
	launching, err := s.ListSessions("launching")
	if err != nil {
		t.Fatalf("ListSessions (launching): %v", err)
	}
	if len(launching) != 1 {
		t.Errorf("ListSessions (launching) returned %d sessions, want 1", len(launching))
	}

	// Filter by status = running.
	running, err := s.ListSessions("running")
	if err != nil {
		t.Fatalf("ListSessions (running): %v", err)
	}
	if len(running) != 1 {
		t.Errorf("ListSessions (running) returned %d sessions, want 1", len(running))
	}

	// Filter by status = completed.
	completed, err := s.ListSessions("completed")
	if err != nil {
		t.Fatalf("ListSessions (completed): %v", err)
	}
	if len(completed) != 1 {
		t.Errorf("ListSessions (completed) returned %d sessions, want 1", len(completed))
	}

	// Filter by non-existent status returns empty.
	none, err := s.ListSessions("paused")
	if err != nil {
		t.Fatalf("ListSessions (paused): %v", err)
	}
	if len(none) != 0 {
		t.Errorf("ListSessions (paused) returned %d sessions, want 0", len(none))
	}
}

func TestUpdateSession(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)

	sess := model.NewSession(model.AgentClaudeCode, "/repos/app", "build feature")
	created, err := s.CreateSession(sess)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Small sleep to ensure updated_at will differ from created_at after the
	// round-trip through RFC 3339 (second-level precision).
	time.Sleep(1100 * time.Millisecond)

	updated, err := s.UpdateSession(created.ID, map[string]interface{}{
		"status": "running",
		"pid":    42,
	})
	if err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}

	if updated.Status != model.SessionRunning {
		t.Errorf("Status = %q, want %q", updated.Status, model.SessionRunning)
	}
	if updated.PID != 42 {
		t.Errorf("PID = %d, want 42", updated.PID)
	}

	// updated_at should be strictly after created_at.
	if !updated.UpdatedAt.After(created.CreatedAt.Truncate(time.Second)) {
		t.Errorf("UpdatedAt (%v) should be after CreatedAt (%v)", updated.UpdatedAt, created.CreatedAt)
	}
}

func TestDeleteSession(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)

	sess := model.NewSession(model.AgentClaudeCode, "/repos/app", "delete me")
	created, err := s.CreateSession(sess)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if err := s.DeleteSession(created.ID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	// GetSession should return an error wrapping sql.ErrNoRows.
	_, err = s.GetSession(created.ID)
	if err == nil {
		t.Fatal("GetSession after delete should have returned an error, got nil")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expected error wrapping sql.ErrNoRows, got: %v", err)
	}
}

func TestGetActiveSessions(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)

	// Create sessions with various statuses.
	sessions := []model.Session{
		model.NewSession(model.AgentClaudeCode, "/repos/a", "prompt a"), // launching (active)
		model.NewSession(model.AgentGemini, "/repos/b", "prompt b"),     // running (active)
		model.NewSession(model.AgentAider, "/repos/c", "prompt c"),      // needs-input (active)
		model.NewSession(model.AgentCodex, "/repos/d", "prompt d"),      // completed (terminal)
		model.NewSession(model.AgentClaudeCode, "/repos/e", "prompt e"), // failed (terminal)
	}
	sessions[1].Status = model.SessionRunning
	sessions[2].Status = model.SessionNeedsInput
	sessions[3].Status = model.SessionCompleted
	sessions[4].Status = model.SessionFailed

	for i, sess := range sessions {
		if _, err := s.CreateSession(sess); err != nil {
			t.Fatalf("CreateSession[%d]: %v", i, err)
		}
	}

	active, err := s.GetActiveSessions()
	if err != nil {
		t.Fatalf("GetActiveSessions: %v", err)
	}

	if len(active) != 3 {
		t.Errorf("GetActiveSessions returned %d sessions, want 3", len(active))
	}

	// Verify that all returned sessions are in non-terminal statuses.
	for _, sess := range active {
		if sess.Status.IsTerminal() {
			t.Errorf("GetActiveSessions returned session %q with terminal status %q", sess.ID, sess.Status)
		}
	}
}

func TestDeleteNonexistentSession(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)

	err := s.DeleteSession("nonexistent-session-id-that-does-not-exist")
	if err == nil {
		t.Fatal("DeleteSession for non-existent ID should have returned an error, got nil")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expected error wrapping sql.ErrNoRows, got: %v", err)
	}
}

func TestSessionDependsOnAndPhaseRoundTrip(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)

	// Create a session with DependsOn and Phase fields populated.
	sess := model.NewSession(model.AgentClaudeCode, "/repos/app", "build feature")
	sess.DependsOn = []string{"dep-id-1", "dep-id-2"}
	sess.Phase = "build"

	created, err := s.CreateSession(sess)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	got, err := s.GetSession(created.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}

	// Verify DependsOn round-trips correctly.
	if len(got.DependsOn) != 2 {
		t.Fatalf("DependsOn length = %d, want 2", len(got.DependsOn))
	}
	if got.DependsOn[0] != "dep-id-1" {
		t.Errorf("DependsOn[0] = %q, want %q", got.DependsOn[0], "dep-id-1")
	}
	if got.DependsOn[1] != "dep-id-2" {
		t.Errorf("DependsOn[1] = %q, want %q", got.DependsOn[1], "dep-id-2")
	}

	// Verify Phase round-trips correctly.
	if got.Phase != "build" {
		t.Errorf("Phase = %q, want %q", got.Phase, "build")
	}
}

func TestSessionEmptyDependsOn(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)

	// A session with no dependencies should round-trip as an empty slice.
	sess := model.NewSession(model.AgentGemini, "/repos/lib", "write tests")

	created, err := s.CreateSession(sess)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	got, err := s.GetSession(created.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}

	if got.DependsOn == nil {
		t.Fatal("DependsOn should be [] not nil")
	}
	if len(got.DependsOn) != 0 {
		t.Errorf("DependsOn length = %d, want 0", len(got.DependsOn))
	}
	if got.Phase != "" {
		t.Errorf("Phase = %q, want empty string", got.Phase)
	}
}

func TestSessionQueuedStatus(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)

	sess := model.NewSession(model.AgentClaudeCode, "/repos/app", "queued task")
	sess.Status = model.SessionQueued
	sess.DependsOn = []string{"some-dep-id"}
	sess.Phase = "plan"

	created, err := s.CreateSession(sess)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	got, err := s.GetSession(created.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}

	if got.Status != model.SessionQueued {
		t.Errorf("Status = %q, want %q", got.Status, model.SessionQueued)
	}

	// ListSessions with "queued" filter should find it.
	queued, err := s.ListSessions("queued")
	if err != nil {
		t.Fatalf("ListSessions(queued): %v", err)
	}
	if len(queued) != 1 {
		t.Errorf("ListSessions(queued) returned %d sessions, want 1", len(queued))
	}

	// GetActiveSessions should NOT include queued sessions (no running process).
	active, err := s.GetActiveSessions()
	if err != nil {
		t.Fatalf("GetActiveSessions: %v", err)
	}
	for _, a := range active {
		if a.ID == created.ID {
			t.Error("GetActiveSessions should not include queued sessions")
		}
	}
}

func TestConcurrentAccess(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)

	const goroutines = 10
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			task := model.NewTask(
				"concurrent-task",
				"created by goroutine",
				"/repos/concurrent",
				model.AgentClaudeCode,
			)
			if _, err := s.CreateTask(task); err != nil {
				errs <- err
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent CreateTask failed: %v", err)
	}

	// Verify all 10 tasks were persisted.
	tasks, err := s.ListTasks("", "")
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != goroutines {
		t.Errorf("ListTasks returned %d tasks, want %d", len(tasks), goroutines)
	}
}
