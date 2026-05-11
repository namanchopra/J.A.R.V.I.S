package store

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/namanchopra/jarvis/internal/model"

	_ "modernc.org/sqlite" // Pure-Go SQLite driver.
)

// OutputSearchResult represents a single matching line found in a task's output file.
type OutputSearchResult struct {
	TaskID   string `json:"taskId"`
	TaskName string `json:"taskName"`
	Line     string `json:"line"`
	LineNum  int    `json:"lineNum"`
}

// Project represents a saved project in the store with its associated repo paths.
type Project struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Path       string   `json:"path"`
	IsMonorepo bool     `json:"isMonorepo"`
	RepoPaths  []string `json:"repoPaths"`
	CreatedAt  string   `json:"createdAt"`
	UpdatedAt  string   `json:"updatedAt"`
}

// timeFormat is the layout used for persisting timestamps in SQLite. It matches
// the RFC 3339 subset that time.Time.Format / time.Parse understand.
const timeFormat = time.RFC3339

// Store provides CRUD access to the SQLite-backed task database.
type Store struct {
	db *sql.DB
}

// DefaultDBPath returns the default database file path (~/.awm/awm.db).
func DefaultDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		// Fallback: use current directory so callers always get a usable path.
		return "awm.db"
	}
	return filepath.Join(home, ".awm", "awm.db")
}

// NewStore opens (or creates) a SQLite database at dbPath and runs any pending
// migrations. If dbPath is empty the default location (~/.awm/awm.db) is used.
//
// The database is opened with WAL journal mode and a 5-second busy timeout for
// better concurrent-read performance.
func NewStore(dbPath string) (*Store, error) {
	if dbPath == "" {
		dbPath = DefaultDBPath()
	}

	// Ensure the parent directory exists.
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating database directory %s: %w", dir, err)
	}

	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", dbPath)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	// SQLite does not support concurrent writes. Limit the pool to a single
	// connection to avoid "database is locked" errors.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	// Verify the connection is usable.
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	if err := runMigrations(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("running migrations: %w", err)
	}

	return &Store{db: db}, nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// ---------------------------------------------------------------------------
// Create
// ---------------------------------------------------------------------------

// CreateTask inserts a new task row and returns the task as stored.
func (s *Store) CreateTask(task model.Task) (model.Task, error) {
	const query = `INSERT INTO tasks (id, name, description, repo_path, agent_type, status, output_path, workflow_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err := s.db.Exec(query,
		task.ID,
		task.Name,
		task.Description,
		task.RepoPath,
		string(task.AgentType),
		string(task.Status),
		task.OutputPath,
		task.WorkflowID,
		task.CreatedAt.Format(timeFormat),
		task.UpdatedAt.Format(timeFormat),
	)
	if err != nil {
		return model.Task{}, fmt.Errorf("inserting task: %w", err)
	}

	return task, nil
}

// ---------------------------------------------------------------------------
// Read
// ---------------------------------------------------------------------------

// GetTask retrieves a single task by its ID. If no row is found an error
// wrapping sql.ErrNoRows is returned.
func (s *Store) GetTask(id string) (model.Task, error) {
	const query = `SELECT id, name, description, repo_path, agent_type, status, output_path, workflow_id, created_at, updated_at
		FROM tasks WHERE id = ?`

	row := s.db.QueryRow(query, id)

	task, err := scanTask(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return model.Task{}, fmt.Errorf("task %q not found: %w", id, sql.ErrNoRows)
		}
		return model.Task{}, fmt.Errorf("querying task %q: %w", id, err)
	}

	return task, nil
}

// ListTasks returns tasks optionally filtered by status and/or repository path.
// An empty statusFilter or repoFilter means no filtering on that column.
// The repoFilter uses LIKE matching so callers can pass partial paths.
func (s *Store) ListTasks(statusFilter string, repoFilter string) ([]model.Task, error) {
	var (
		clauses []string
		args    []interface{}
	)

	if statusFilter != "" {
		clauses = append(clauses, "status = ?")
		args = append(args, statusFilter)
	}
	if repoFilter != "" {
		clauses = append(clauses, "repo_path LIKE ?")
		args = append(args, "%"+repoFilter+"%")
	}

	query := "SELECT id, name, description, repo_path, agent_type, status, output_path, workflow_id, created_at, updated_at FROM tasks"
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += " ORDER BY created_at DESC"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing tasks: %w", err)
	}
	defer rows.Close()

	var tasks []model.Task
	for rows.Next() {
		t, err := scanTaskRows(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning task row: %w", err)
		}
		tasks = append(tasks, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating task rows: %w", err)
	}

	return tasks, nil
}

// ---------------------------------------------------------------------------
// Update
// ---------------------------------------------------------------------------

// UpdateTask applies a partial update to the task identified by id. Only the
// keys present in updates are modified. Recognised keys are: "name",
// "description", "repo_path", "agent_type", "status", "output_path".
//
// If "status" is included, the transition is validated against the current
// status using model.ValidateStatusTransition.
//
// updated_at is always set to the current time.
func (s *Store) UpdateTask(id string, updates map[string]interface{}) (model.Task, error) {
	// Validate the status transition when a new status is supplied.
	if newStatusRaw, ok := updates["status"]; ok {
		newStatus, isString := newStatusRaw.(string)
		if !isString {
			return model.Task{}, fmt.Errorf("status must be a string")
		}

		existing, err := s.GetTask(id)
		if err != nil {
			return model.Task{}, err
		}

		if err := model.ValidateStatusTransition(existing.Status, model.Status(newStatus)); err != nil {
			return model.Task{}, fmt.Errorf("status transition for task %q: %w", id, err)
		}
	}

	// Build the SET clause dynamically.
	allowedCols := map[string]bool{
		"name":        true,
		"description": true,
		"repo_path":   true,
		"agent_type":  true,
		"status":      true,
		"output_path": true,
		"workflow_id": true,
	}

	var (
		setClauses []string
		args       []interface{}
	)

	for col, val := range updates {
		if !allowedCols[col] {
			continue
		}
		setClauses = append(setClauses, col+" = ?")
		args = append(args, val)
	}

	// Always bump updated_at.
	now := time.Now().Format(timeFormat)
	setClauses = append(setClauses, "updated_at = ?")
	args = append(args, now)

	if len(setClauses) == 0 {
		return s.GetTask(id)
	}

	query := "UPDATE tasks SET " + strings.Join(setClauses, ", ") + " WHERE id = ?"
	args = append(args, id)

	res, err := s.db.Exec(query, args...)
	if err != nil {
		return model.Task{}, fmt.Errorf("updating task %q: %w", id, err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return model.Task{}, fmt.Errorf("checking rows affected for task %q: %w", id, err)
	}
	if n == 0 {
		return model.Task{}, fmt.Errorf("task %q not found: %w", id, sql.ErrNoRows)
	}

	return s.GetTask(id)
}

// ---------------------------------------------------------------------------
// Delete
// ---------------------------------------------------------------------------

// DeleteTask removes the task identified by id. An error is returned if no
// matching row exists.
func (s *Store) DeleteTask(id string) error {
	res, err := s.db.Exec("DELETE FROM tasks WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("deleting task %q: %w", id, err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected for task %q: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("task %q not found: %w", id, sql.ErrNoRows)
	}

	return nil
}

// CountAutoDetected returns the number of tasks whose description starts with
// "[auto-detected]", using a SQL COUNT(*) for efficiency.
func (s *Store) CountAutoDetected() (int, error) {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM tasks WHERE description LIKE '[auto-detected]%'").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting auto-detected tasks: %w", err)
	}
	return count, nil
}

// ---------------------------------------------------------------------------
// Workflow CRUD
// ---------------------------------------------------------------------------

// CreateWorkflow inserts a new workflow row and returns the workflow as stored.
func (s *Store) CreateWorkflow(w model.Workflow) (model.Workflow, error) {
	const query = `INSERT INTO workflows (id, name, description, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)`

	_, err := s.db.Exec(query,
		w.ID,
		w.Name,
		w.Description,
		w.CreatedAt.Format(timeFormat),
		w.UpdatedAt.Format(timeFormat),
	)
	if err != nil {
		return model.Workflow{}, fmt.Errorf("inserting workflow: %w", err)
	}

	return w, nil
}

// GetWorkflow retrieves a single workflow by its ID. If no row is found an
// error wrapping sql.ErrNoRows is returned.
func (s *Store) GetWorkflow(id string) (model.Workflow, error) {
	const query = `SELECT id, name, description, created_at, updated_at
		FROM workflows WHERE id = ?`

	row := s.db.QueryRow(query, id)

	w, err := scanWorkflow(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return model.Workflow{}, fmt.Errorf("workflow %q not found: %w", id, sql.ErrNoRows)
		}
		return model.Workflow{}, fmt.Errorf("querying workflow %q: %w", id, err)
	}

	return w, nil
}

// ListWorkflows returns all workflows ordered by creation time descending.
func (s *Store) ListWorkflows() ([]model.Workflow, error) {
	const query = `SELECT id, name, description, created_at, updated_at
		FROM workflows ORDER BY created_at DESC`

	rows, err := s.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("listing workflows: %w", err)
	}
	defer rows.Close()

	var workflows []model.Workflow
	for rows.Next() {
		w, err := scanWorkflowRows(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning workflow row: %w", err)
		}
		workflows = append(workflows, w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating workflow rows: %w", err)
	}

	return workflows, nil
}

// UpdateWorkflow applies a partial update to the workflow identified by id.
// Recognised keys are: "name", "description". updated_at is always set to the
// current time.
func (s *Store) UpdateWorkflow(id string, updates map[string]interface{}) (model.Workflow, error) {
	allowedCols := map[string]bool{
		"name":        true,
		"description": true,
	}

	var (
		setClauses []string
		args       []interface{}
	)

	for col, val := range updates {
		if !allowedCols[col] {
			continue
		}
		setClauses = append(setClauses, col+" = ?")
		args = append(args, val)
	}

	// Always bump updated_at.
	now := time.Now().Format(timeFormat)
	setClauses = append(setClauses, "updated_at = ?")
	args = append(args, now)

	if len(setClauses) == 0 {
		return s.GetWorkflow(id)
	}

	query := "UPDATE workflows SET " + strings.Join(setClauses, ", ") + " WHERE id = ?"
	args = append(args, id)

	res, err := s.db.Exec(query, args...)
	if err != nil {
		return model.Workflow{}, fmt.Errorf("updating workflow %q: %w", id, err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return model.Workflow{}, fmt.Errorf("checking rows affected for workflow %q: %w", id, err)
	}
	if n == 0 {
		return model.Workflow{}, fmt.Errorf("workflow %q not found: %w", id, sql.ErrNoRows)
	}

	return s.GetWorkflow(id)
}

// DeleteWorkflow removes the workflow identified by id and unlinks any tasks
// that reference it (sets their workflow_id to empty). An error is returned if
// no matching workflow row exists.
func (s *Store) DeleteWorkflow(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction for workflow delete: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Unlink tasks that reference this workflow.
	if _, err := tx.Exec("UPDATE tasks SET workflow_id = '' WHERE workflow_id = ?", id); err != nil {
		return fmt.Errorf("unlinking tasks from workflow %q: %w", id, err)
	}

	res, err := tx.Exec("DELETE FROM workflows WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("deleting workflow %q: %w", id, err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected for workflow %q: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("workflow %q not found: %w", id, sql.ErrNoRows)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing workflow delete %q: %w", id, err)
	}

	return nil
}

// GetWorkflowTasks returns all tasks linked to the given workflow, ordered by
// creation time descending.
func (s *Store) GetWorkflowTasks(workflowID string) ([]model.Task, error) {
	const query = `SELECT id, name, description, repo_path, agent_type, status, output_path, workflow_id, created_at, updated_at
		FROM tasks WHERE workflow_id = ? ORDER BY created_at DESC`

	rows, err := s.db.Query(query, workflowID)
	if err != nil {
		return nil, fmt.Errorf("listing tasks for workflow %q: %w", workflowID, err)
	}
	defer rows.Close()

	var tasks []model.Task
	for rows.Next() {
		t, err := scanTaskRows(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning task row: %w", err)
		}
		tasks = append(tasks, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating task rows: %w", err)
	}

	return tasks, nil
}

// ---------------------------------------------------------------------------
// Dashboard stats
// ---------------------------------------------------------------------------

// GetDashboardStats returns aggregate counts of tasks grouped by status in a
// single query.
func (s *Store) GetDashboardStats() (model.DashboardStats, error) {
	const query = `SELECT
		COUNT(*)                                          AS total,
		COUNT(*) FILTER (WHERE status = 'running')        AS running,
		COUNT(*) FILTER (WHERE status = 'pending')        AS pending,
		COUNT(*) FILTER (WHERE status = 'done')           AS done,
		COUNT(*) FILTER (WHERE status = 'failed')         AS failed,
		COUNT(*) FILTER (WHERE status = 'needs-input')    AS needs_input
	FROM tasks`

	var stats model.DashboardStats
	err := s.db.QueryRow(query).Scan(
		&stats.Total,
		&stats.Running,
		&stats.Pending,
		&stats.Done,
		&stats.Failed,
		&stats.NeedsInput,
	)
	if err != nil {
		return model.DashboardStats{}, fmt.Errorf("querying dashboard stats: %w", err)
	}

	return stats, nil
}

// ---------------------------------------------------------------------------
// Activity Events
// ---------------------------------------------------------------------------

// CreateActivityEvent inserts a new activity event row.
func (s *Store) CreateActivityEvent(event model.ActivityEvent) error {
	const query = `INSERT INTO activity_events (id, task_id, task_name, event_type, message, metadata, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`

	_, err := s.db.Exec(query,
		event.ID,
		event.TaskID,
		event.TaskName,
		event.EventType,
		event.Message,
		event.Metadata,
		event.CreatedAt.Format(timeFormat),
	)
	if err != nil {
		return fmt.Errorf("inserting activity event: %w", err)
	}

	return nil
}

// ListActivityEvents returns activity events ordered by created_at DESC. If
// beforeID is non-empty, only events older than the event with that ID are
// returned (cursor-based pagination for infinite scroll). Limit defaults to 50
// if <= 0.
func (s *Store) ListActivityEvents(limit int, beforeID string) ([]model.ActivityEvent, error) {
	if limit <= 0 {
		limit = 50
	}

	var (
		query string
		args  []interface{}
	)

	if beforeID != "" {
		query = `SELECT id, task_id, task_name, event_type, message, metadata, created_at
			FROM activity_events
			WHERE created_at < (SELECT created_at FROM activity_events WHERE id = ?)
			ORDER BY created_at DESC
			LIMIT ?`
		args = []interface{}{beforeID, limit}
	} else {
		query = `SELECT id, task_id, task_name, event_type, message, metadata, created_at
			FROM activity_events
			ORDER BY created_at DESC
			LIMIT ?`
		args = []interface{}{limit}
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing activity events: %w", err)
	}
	defer rows.Close()

	var events []model.ActivityEvent
	for rows.Next() {
		e, err := scanActivityEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning activity event row: %w", err)
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating activity event rows: %w", err)
	}

	return events, nil
}

// ListTaskActivityEvents returns activity events for a specific task, ordered
// by created_at DESC. Limit defaults to 50 if <= 0.
func (s *Store) ListTaskActivityEvents(taskID string, limit int) ([]model.ActivityEvent, error) {
	if limit <= 0 {
		limit = 50
	}

	const query = `SELECT id, task_id, task_name, event_type, message, metadata, created_at
		FROM activity_events
		WHERE task_id = ?
		ORDER BY created_at DESC
		LIMIT ?`

	rows, err := s.db.Query(query, taskID, limit)
	if err != nil {
		return nil, fmt.Errorf("listing task activity events: %w", err)
	}
	defer rows.Close()

	var events []model.ActivityEvent
	for rows.Next() {
		e, err := scanActivityEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning activity event row: %w", err)
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating activity event rows: %w", err)
	}

	return events, nil
}

// ---------------------------------------------------------------------------
// Output Search
// ---------------------------------------------------------------------------

// SearchOutput performs a case-insensitive grep-like search across all task
// output files. It iterates through every task that has an OutputPath set,
// reads each file line by line, and collects matches up to limit total results.
func (s *Store) SearchOutput(query string, limit int) ([]OutputSearchResult, error) {
	if limit <= 0 {
		limit = 100
	}

	tasks, err := s.ListTasks("", "")
	if err != nil {
		return nil, fmt.Errorf("SearchOutput: listing tasks: %w", err)
	}

	queryLower := strings.ToLower(query)
	var results []OutputSearchResult

	for _, task := range tasks {
		if len(results) >= limit {
			break
		}
		if task.OutputPath == "" {
			continue
		}

		f, err := os.Open(task.OutputPath)
		if err != nil {
			// File may have been removed — skip silently.
			continue
		}

		scanner := bufio.NewScanner(f)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			line := scanner.Text()
			if strings.Contains(strings.ToLower(line), queryLower) {
				results = append(results, OutputSearchResult{
					TaskID:   task.ID,
					TaskName: task.Name,
					Line:     line,
					LineNum:  lineNum,
				})
				if len(results) >= limit {
					break
				}
			}
		}
		f.Close()
	}

	return results, nil
}

// ---------------------------------------------------------------------------
// Session CRUD
// ---------------------------------------------------------------------------

// CreateSession inserts a new session row and returns the session as stored.
func (s *Store) CreateSession(sess model.Session) (model.Session, error) {
	// Ensure DependsOn is non-nil so it serialises as "[]" not "null".
	if sess.DependsOn == nil {
		sess.DependsOn = []string{}
	}

	dependsOnJSON, err := json.Marshal(sess.DependsOn)
	if err != nil {
		return model.Session{}, fmt.Errorf("marshalling depends_on: %w", err)
	}

	const query = `INSERT INTO sessions (id, task_id, agent_type, repo_path, prompt, agent_session_id, status, pid, output_path, exit_code, error_message, parent_session_id, depends_on, phase, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err = s.db.Exec(query,
		sess.ID,
		nullString(sess.TaskID),
		string(sess.AgentType),
		sess.RepoPath,
		sess.Prompt,
		nullString(sess.AgentSessionID),
		string(sess.Status),
		sess.PID,
		nullString(sess.OutputPath),
		sess.ExitCode,
		nullString(sess.ErrorMessage),
		nullString(sess.ParentSessionID),
		string(dependsOnJSON),
		sess.Phase,
		sess.CreatedAt.Format(timeFormat),
		sess.UpdatedAt.Format(timeFormat),
	)
	if err != nil {
		return model.Session{}, fmt.Errorf("inserting session: %w", err)
	}

	return sess, nil
}

// GetSession retrieves a single session by its ID. If no row is found an error
// wrapping sql.ErrNoRows is returned.
func (s *Store) GetSession(id string) (model.Session, error) {
	const query = `SELECT id, task_id, agent_type, repo_path, prompt, agent_session_id, status, pid, output_path, exit_code, error_message, parent_session_id, depends_on, phase, created_at, updated_at
		FROM sessions WHERE id = ?`

	row := s.db.QueryRow(query, id)

	sess, err := scanSession(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return model.Session{}, fmt.Errorf("session %q not found: %w", id, sql.ErrNoRows)
		}
		return model.Session{}, fmt.Errorf("querying session %q: %w", id, err)
	}

	return sess, nil
}

// ListSessions returns sessions optionally filtered by status. An empty
// statusFilter means no filtering. Results are ordered by created_at DESC.
func (s *Store) ListSessions(statusFilter string) ([]model.Session, error) {
	var (
		clauses []string
		args    []interface{}
	)

	if statusFilter != "" {
		clauses = append(clauses, "status = ?")
		args = append(args, statusFilter)
	}

	query := `SELECT id, task_id, agent_type, repo_path, prompt, agent_session_id, status, pid, output_path, exit_code, error_message, parent_session_id, depends_on, phase, created_at, updated_at FROM sessions`
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += " ORDER BY created_at DESC"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing sessions: %w", err)
	}
	defer rows.Close()

	var sessions []model.Session
	for rows.Next() {
		sess, err := scanSessionRows(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning session row: %w", err)
		}
		sessions = append(sessions, sess)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating session rows: %w", err)
	}

	return sessions, nil
}

// UpdateSession applies a partial update to the session identified by id. Only
// the keys present in updates are modified. Recognised keys are: "task_id",
// "agent_session_id", "status", "pid", "output_path", "exit_code",
// "error_message".
//
// updated_at is always set to the current time.
func (s *Store) UpdateSession(id string, updates map[string]interface{}) (model.Session, error) {
	allowedCols := map[string]bool{
		"task_id":           true,
		"agent_session_id":  true,
		"status":            true,
		"pid":               true,
		"output_path":       true,
		"exit_code":         true,
		"error_message":     true,
		"parent_session_id": true,
		"depends_on":        true,
		"phase":             true,
	}

	var (
		setClauses []string
		args       []interface{}
	)

	for col, val := range updates {
		if !allowedCols[col] {
			continue
		}
		setClauses = append(setClauses, col+" = ?")
		args = append(args, val)
	}

	// Always bump updated_at.
	now := time.Now().Format(timeFormat)
	setClauses = append(setClauses, "updated_at = ?")
	args = append(args, now)

	if len(setClauses) == 0 {
		return s.GetSession(id)
	}

	query := "UPDATE sessions SET " + strings.Join(setClauses, ", ") + " WHERE id = ?"
	args = append(args, id)

	res, err := s.db.Exec(query, args...)
	if err != nil {
		return model.Session{}, fmt.Errorf("updating session %q: %w", id, err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return model.Session{}, fmt.Errorf("checking rows affected for session %q: %w", id, err)
	}
	if n == 0 {
		return model.Session{}, fmt.Errorf("session %q not found: %w", id, sql.ErrNoRows)
	}

	return s.GetSession(id)
}

// DeleteSession removes the session identified by id. An error is returned if
// no matching row exists.
func (s *Store) DeleteSession(id string) error {
	res, err := s.db.Exec("DELETE FROM sessions WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("deleting session %q: %w", id, err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected for session %q: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("session %q not found: %w", id, sql.ErrNoRows)
	}

	return nil
}

// GetActiveSessions returns all sessions in a non-terminal, active state
// (launching, running, or needs-input), ordered by created_at DESC.
func (s *Store) GetActiveSessions() ([]model.Session, error) {
	const query = `SELECT id, task_id, agent_type, repo_path, prompt, agent_session_id, status, pid, output_path, exit_code, error_message, parent_session_id, depends_on, phase, created_at, updated_at
		FROM sessions
		WHERE status IN ('launching', 'running', 'needs-input')
		ORDER BY created_at DESC`

	rows, err := s.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("listing active sessions: %w", err)
	}
	defer rows.Close()

	var sessions []model.Session
	for rows.Next() {
		sess, err := scanSessionRows(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning active session row: %w", err)
		}
		sessions = append(sessions, sess)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating active session rows: %w", err)
	}

	return sessions, nil
}

// ---------------------------------------------------------------------------
// Project CRUD
// ---------------------------------------------------------------------------

// CreateProject inserts a new project row with a generated UUID and returns it.
// The repoPaths are stored in the project_repos join table.
func (s *Store) CreateProject(name, path string, isMonorepo bool) (Project, error) {
	id := fmt.Sprintf("proj_%s", generateShortID())
	now := time.Now().Format(timeFormat)

	const query = `INSERT INTO projects (id, name, path, is_monorepo, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`

	_, err := s.db.Exec(query, id, name, path, isMonorepo, now, now)
	if err != nil {
		return Project{}, fmt.Errorf("inserting project: %w", err)
	}

	return Project{
		ID:         id,
		Name:       name,
		Path:       path,
		IsMonorepo: isMonorepo,
		RepoPaths:  []string{},
		CreatedAt:  now,
		UpdatedAt:  now,
	}, nil
}

// ListProjects returns all saved projects with their associated repo paths,
// ordered by creation time descending.
func (s *Store) ListProjects() ([]Project, error) {
	const query = `SELECT id, name, path, is_monorepo, created_at, updated_at
		FROM projects ORDER BY created_at DESC`

	rows, err := s.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("listing projects: %w", err)
	}
	defer rows.Close()

	var projects []Project
	for rows.Next() {
		var p Project
		var isMonorepo int
		if err := rows.Scan(&p.ID, &p.Name, &p.Path, &isMonorepo, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning project row: %w", err)
		}
		p.IsMonorepo = isMonorepo != 0
		p.RepoPaths = []string{} // populated below
		projects = append(projects, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating project rows: %w", err)
	}

	// Load repo paths for each project.
	for i := range projects {
		repoPaths, err := s.GetProjectRepos(projects[i].ID)
		if err != nil {
			return nil, fmt.Errorf("loading repos for project %q: %w", projects[i].ID, err)
		}
		projects[i].RepoPaths = repoPaths
	}

	return projects, nil
}

// DeleteProject removes the project and its associated repo mappings.
func (s *Store) DeleteProject(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction for project delete: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Remove associated repo mappings first.
	if _, err := tx.Exec("DELETE FROM project_repos WHERE project_id = ?", id); err != nil {
		return fmt.Errorf("deleting project repos for %q: %w", id, err)
	}

	res, err := tx.Exec("DELETE FROM projects WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("deleting project %q: %w", id, err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected for project %q: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("project %q not found: %w", id, sql.ErrNoRows)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing project delete %q: %w", id, err)
	}

	return nil
}

// GetProjectRepos returns all repo paths associated with a project.
func (s *Store) GetProjectRepos(projectID string) ([]string, error) {
	const query = `SELECT repo_path FROM project_repos WHERE project_id = ? ORDER BY repo_path`

	rows, err := s.db.Query(query, projectID)
	if err != nil {
		return nil, fmt.Errorf("listing project repos for %q: %w", projectID, err)
	}
	defer rows.Close()

	var paths []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, fmt.Errorf("scanning project repo path: %w", err)
		}
		paths = append(paths, path)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating project repo rows: %w", err)
	}

	if paths == nil {
		paths = []string{}
	}

	return paths, nil
}

// SetProjectRepos replaces all repo path associations for a project. Existing
// mappings are deleted and replaced with the provided repoPaths.
func (s *Store) SetProjectRepos(projectID string, repoPaths []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction for SetProjectRepos: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Remove existing mappings.
	if _, err := tx.Exec("DELETE FROM project_repos WHERE project_id = ?", projectID); err != nil {
		return fmt.Errorf("clearing project repos for %q: %w", projectID, err)
	}

	// Insert new mappings.
	const insertQuery = `INSERT INTO project_repos (project_id, repo_path) VALUES (?, ?)`
	for _, rp := range repoPaths {
		if _, err := tx.Exec(insertQuery, projectID, rp); err != nil {
			return fmt.Errorf("inserting project repo %q for %q: %w", rp, projectID, err)
		}
	}

	// Bump updated_at on the project.
	now := time.Now().Format(timeFormat)
	if _, err := tx.Exec("UPDATE projects SET updated_at = ? WHERE id = ?", now, projectID); err != nil {
		return fmt.Errorf("updating project %q timestamp: %w", projectID, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing SetProjectRepos for %q: %w", projectID, err)
	}

	return nil
}

// generateShortID produces a short unique identifier using the first 8 chars
// of a UUID (for project IDs which don't need full UUID length).
func generateShortID() string {
	id := fmt.Sprintf("%x", time.Now().UnixNano())
	if len(id) > 12 {
		id = id[len(id)-12:]
	}
	return id
}

// ---------------------------------------------------------------------------
// Session Group CRUD
// ---------------------------------------------------------------------------

// CreateSessionGroup inserts a new session group row and returns the group as
// stored.
func (s *Store) CreateSessionGroup(g model.SessionGroup) (model.SessionGroup, error) {
	const query = `INSERT INTO session_groups (id, name, description, color, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`

	_, err := s.db.Exec(query,
		g.ID,
		g.Name,
		g.Description,
		g.Color,
		g.CreatedAt.Format(timeFormat),
		g.UpdatedAt.Format(timeFormat),
	)
	if err != nil {
		return model.SessionGroup{}, fmt.Errorf("inserting session group: %w", err)
	}

	return g, nil
}

// ListSessionGroups returns all session groups ordered by name ascending.
func (s *Store) ListSessionGroups() ([]model.SessionGroup, error) {
	const query = `SELECT id, name, description, color, created_at, updated_at
		FROM session_groups ORDER BY name ASC`

	rows, err := s.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("listing session groups: %w", err)
	}
	defer rows.Close()

	var groups []model.SessionGroup
	for rows.Next() {
		g, err := scanSessionGroup(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning session group row: %w", err)
		}
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating session group rows: %w", err)
	}

	return groups, nil
}

// DeleteSessionGroup removes the session group identified by id. Because the
// session_group_members table has ON DELETE CASCADE, members are removed
// automatically. An error is returned if no matching row exists.
func (s *Store) DeleteSessionGroup(id string) error {
	res, err := s.db.Exec("DELETE FROM session_groups WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("deleting session group %q: %w", id, err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected for session group %q: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("session group %q not found: %w", id, sql.ErrNoRows)
	}

	return nil
}

// AddToGroup inserts a repository path into the given session group. The
// operation is idempotent — if the (group_id, repo_path) pair already exists
// the insert is silently ignored.
func (s *Store) AddToGroup(groupID, repoPath string) error {
	const query = `INSERT OR IGNORE INTO session_group_members (group_id, repo_path, added_at)
		VALUES (?, ?, ?)`

	now := time.Now().Format(timeFormat)
	_, err := s.db.Exec(query, groupID, repoPath, now)
	if err != nil {
		return fmt.Errorf("adding repo %q to group %q: %w", repoPath, groupID, err)
	}

	return nil
}

// RemoveFromGroup removes a repository path from the given session group. No
// error is returned if the membership did not exist.
func (s *Store) RemoveFromGroup(groupID, repoPath string) error {
	_, err := s.db.Exec(
		"DELETE FROM session_group_members WHERE group_id = ? AND repo_path = ?",
		groupID, repoPath,
	)
	if err != nil {
		return fmt.Errorf("removing repo %q from group %q: %w", repoPath, groupID, err)
	}

	return nil
}

// GetGroupMembers returns all members of the given session group ordered by
// added_at ascending.
func (s *Store) GetGroupMembers(groupID string) ([]model.GroupMember, error) {
	const query = `SELECT group_id, repo_path, added_at
		FROM session_group_members WHERE group_id = ? ORDER BY added_at ASC`

	rows, err := s.db.Query(query, groupID)
	if err != nil {
		return nil, fmt.Errorf("listing members for group %q: %w", groupID, err)
	}
	defer rows.Close()

	var members []model.GroupMember
	for rows.Next() {
		var (
			m       model.GroupMember
			addedAt string
		)
		if err := rows.Scan(&m.GroupID, &m.RepoPath, &addedAt); err != nil {
			return nil, fmt.Errorf("scanning group member row: %w", err)
		}
		var parseErr error
		m.AddedAt, parseErr = time.Parse(timeFormat, addedAt)
		if parseErr != nil {
			return nil, fmt.Errorf("parsing added_at %q: %w", addedAt, parseErr)
		}
		members = append(members, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating group member rows: %w", err)
	}

	return members, nil
}

// ---------------------------------------------------------------------------
// Session Template CRUD
// ---------------------------------------------------------------------------

// CreateSessionTemplate inserts a new session template row. The RepoPaths slice
// is serialised as a JSON string for storage. Returns the template as stored.
func (s *Store) CreateSessionTemplate(t model.SessionTemplate) (model.SessionTemplate, error) {
	repoPathsJSON, err := json.Marshal(t.RepoPaths)
	if err != nil {
		return model.SessionTemplate{}, fmt.Errorf("marshalling repo paths: %w", err)
	}

	const query = `INSERT INTO session_templates (id, name, agent_type, repo_paths, command, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`

	_, err = s.db.Exec(query,
		t.ID,
		t.Name,
		t.AgentType,
		string(repoPathsJSON),
		t.Command,
		t.CreatedAt.Format(timeFormat),
	)
	if err != nil {
		return model.SessionTemplate{}, fmt.Errorf("inserting session template: %w", err)
	}

	return t, nil
}

// ListSessionTemplates returns all session templates ordered by name ascending.
// The repo_paths JSON column is deserialised into a []string.
func (s *Store) ListSessionTemplates() ([]model.SessionTemplate, error) {
	const query = `SELECT id, name, agent_type, repo_paths, command, created_at
		FROM session_templates ORDER BY name ASC`

	rows, err := s.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("listing session templates: %w", err)
	}
	defer rows.Close()

	var templates []model.SessionTemplate
	for rows.Next() {
		t, err := scanSessionTemplate(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning session template row: %w", err)
		}
		templates = append(templates, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating session template rows: %w", err)
	}

	return templates, nil
}

// GetSessionTemplate retrieves a single session template by its ID. If no row
// is found an error wrapping sql.ErrNoRows is returned.
func (s *Store) GetSessionTemplate(id string) (model.SessionTemplate, error) {
	const query = `SELECT id, name, agent_type, repo_paths, command, created_at
		FROM session_templates WHERE id = ?`

	row := s.db.QueryRow(query, id)

	var (
		t             model.SessionTemplate
		repoPathsJSON string
		createdAt     string
	)

	if err := row.Scan(&t.ID, &t.Name, &t.AgentType, &repoPathsJSON, &t.Command, &createdAt); err != nil {
		if err == sql.ErrNoRows {
			return model.SessionTemplate{}, fmt.Errorf("session template %q not found: %w", id, sql.ErrNoRows)
		}
		return model.SessionTemplate{}, fmt.Errorf("querying session template %q: %w", id, err)
	}

	if err := json.Unmarshal([]byte(repoPathsJSON), &t.RepoPaths); err != nil {
		return model.SessionTemplate{}, fmt.Errorf("unmarshalling repo paths for template %q: %w", id, err)
	}

	var parseErr error
	t.CreatedAt, parseErr = time.Parse(timeFormat, createdAt)
	if parseErr != nil {
		return model.SessionTemplate{}, fmt.Errorf("parsing created_at %q: %w", createdAt, parseErr)
	}

	return t, nil
}

// DeleteSessionTemplate removes the session template identified by id. An
// error is returned if no matching row exists.
func (s *Store) DeleteSessionTemplate(id string) error {
	res, err := s.db.Exec("DELETE FROM session_templates WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("deleting session template %q: %w", id, err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected for session template %q: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("session template %q not found: %w", id, sql.ErrNoRows)
	}

	return nil
}

// ---------------------------------------------------------------------------
// Cost Snapshot CRUD
// ---------------------------------------------------------------------------

// InsertCostSnapshot inserts a new cost snapshot row.
func (s *Store) InsertCostSnapshot(c model.CostSnapshot) error {
	const query = `INSERT INTO cost_snapshots (id, session_id, project_path, input_tokens, output_tokens, cost_usd, recorded_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`

	_, err := s.db.Exec(query,
		c.ID,
		c.SessionID,
		c.ProjectPath,
		c.InputTokens,
		c.OutputTokens,
		c.CostUSD,
		c.RecordedAt.Format(timeFormat),
	)
	if err != nil {
		return fmt.Errorf("inserting cost snapshot: %w", err)
	}

	return nil
}

// GetCostsBySession returns all cost snapshots for the given session, ordered
// by recorded_at ascending.
func (s *Store) GetCostsBySession(sessionID string) ([]model.CostSnapshot, error) {
	const query = `SELECT id, session_id, project_path, input_tokens, output_tokens, cost_usd, recorded_at
		FROM cost_snapshots WHERE session_id = ? ORDER BY recorded_at ASC`

	rows, err := s.db.Query(query, sessionID)
	if err != nil {
		return nil, fmt.Errorf("listing cost snapshots for session %q: %w", sessionID, err)
	}
	defer rows.Close()

	var snapshots []model.CostSnapshot
	for rows.Next() {
		c, err := scanCostSnapshot(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning cost snapshot row: %w", err)
		}
		snapshots = append(snapshots, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating cost snapshot rows: %w", err)
	}

	return snapshots, nil
}

// GetCostsByProject returns all cost snapshots for the given project path,
// ordered by recorded_at ascending.
func (s *Store) GetCostsByProject(projectPath string) ([]model.CostSnapshot, error) {
	const query = `SELECT id, session_id, project_path, input_tokens, output_tokens, cost_usd, recorded_at
		FROM cost_snapshots WHERE project_path = ? ORDER BY recorded_at ASC`

	rows, err := s.db.Query(query, projectPath)
	if err != nil {
		return nil, fmt.Errorf("listing cost snapshots for project %q: %w", projectPath, err)
	}
	defer rows.Close()

	var snapshots []model.CostSnapshot
	for rows.Next() {
		c, err := scanCostSnapshot(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning cost snapshot row: %w", err)
		}
		snapshots = append(snapshots, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating cost snapshot rows: %w", err)
	}

	return snapshots, nil
}

// ---------------------------------------------------------------------------
// Session Todo CRUD
// ---------------------------------------------------------------------------

// CreateSessionTodo inserts a new session todo. The sort_order is set to one
// more than the current maximum for the session, ensuring new items appear at
// the end.
func (s *Store) CreateSessionTodo(sessionID, title string) (model.SessionTodo, error) {
	if sessionID == "" {
		return model.SessionTodo{}, fmt.Errorf("CreateSessionTodo: sessionID is required")
	}
	if title == "" {
		return model.SessionTodo{}, fmt.Errorf("CreateSessionTodo: title is required")
	}

	// Determine next sort_order.
	var maxOrder int
	row := s.db.QueryRow("SELECT COALESCE(MAX(sort_order), -1) FROM session_todos WHERE session_id = ?", sessionID)
	if err := row.Scan(&maxOrder); err != nil {
		return model.SessionTodo{}, fmt.Errorf("CreateSessionTodo: reading max sort_order: %w", err)
	}

	todo := model.NewSessionTodo(sessionID, title, maxOrder+1)

	const query = `INSERT INTO session_todos (id, session_id, title, status, sort_order, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`

	_, err := s.db.Exec(query,
		todo.ID,
		todo.SessionID,
		todo.Title,
		todo.Status,
		todo.SortOrder,
		todo.CreatedAt.Format(timeFormat),
	)
	if err != nil {
		return model.SessionTodo{}, fmt.Errorf("CreateSessionTodo: %w", err)
	}

	return todo, nil
}

// ListSessionTodos returns all todos for a session ordered by sort_order ASC.
func (s *Store) ListSessionTodos(sessionID string) ([]model.SessionTodo, error) {
	const query = `SELECT id, session_id, title, status, sort_order, created_at
		FROM session_todos WHERE session_id = ? ORDER BY sort_order ASC`

	rows, err := s.db.Query(query, sessionID)
	if err != nil {
		return nil, fmt.Errorf("ListSessionTodos: %w", err)
	}
	defer rows.Close()

	todos := []model.SessionTodo{}
	for rows.Next() {
		var (
			todo      model.SessionTodo
			createdAt string
		)
		if err := rows.Scan(&todo.ID, &todo.SessionID, &todo.Title, &todo.Status, &todo.SortOrder, &createdAt); err != nil {
			return nil, fmt.Errorf("ListSessionTodos: scanning row: %w", err)
		}
		var parseErr error
		todo.CreatedAt, parseErr = time.Parse(timeFormat, createdAt)
		if parseErr != nil {
			return nil, fmt.Errorf("ListSessionTodos: parsing created_at %q: %w", createdAt, parseErr)
		}
		todos = append(todos, todo)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ListSessionTodos: iterating rows: %w", err)
	}

	return todos, nil
}

// UpdateSessionTodo updates the status of a session todo. The status must be
// "pending" or "done".
func (s *Store) UpdateSessionTodo(id, status string) error {
	if !model.ValidTodoStatus(status) {
		return fmt.Errorf("UpdateSessionTodo: invalid status %q (must be \"pending\" or \"done\")", status)
	}

	res, err := s.db.Exec("UPDATE session_todos SET status = ? WHERE id = ?", status, id)
	if err != nil {
		return fmt.Errorf("UpdateSessionTodo: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("UpdateSessionTodo: checking rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("UpdateSessionTodo: todo %q not found: %w", id, sql.ErrNoRows)
	}

	return nil
}

// DeleteSessionTodo removes a session todo by ID.
func (s *Store) DeleteSessionTodo(id string) error {
	res, err := s.db.Exec("DELETE FROM session_todos WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("DeleteSessionTodo: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("DeleteSessionTodo: checking rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("DeleteSessionTodo: todo %q not found: %w", id, sql.ErrNoRows)
	}

	return nil
}

// ---------------------------------------------------------------------------
// Approval Rule CRUD
// ---------------------------------------------------------------------------

// CreateApprovalRule inserts a new approval rule and returns it.
func (s *Store) CreateApprovalRule(name, pattern, action, scope, projectPath string) (model.ApprovalRule, error) {
	if name == "" {
		return model.ApprovalRule{}, fmt.Errorf("CreateApprovalRule: name is required")
	}
	if pattern == "" {
		return model.ApprovalRule{}, fmt.Errorf("CreateApprovalRule: pattern is required")
	}
	if !model.ValidApprovalAction(action) {
		return model.ApprovalRule{}, fmt.Errorf("CreateApprovalRule: invalid action %q (must be \"approve\" or \"deny\")", action)
	}
	if !model.ValidApprovalScope(scope) {
		return model.ApprovalRule{}, fmt.Errorf("CreateApprovalRule: invalid scope %q (must be \"all\" or \"project\")", scope)
	}

	rule := model.NewApprovalRule(name, pattern, action, scope, projectPath)

	const query = `INSERT INTO approval_rules (id, name, pattern, action, scope, project_path, enabled, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`

	enabled := 0
	if rule.Enabled {
		enabled = 1
	}

	_, err := s.db.Exec(query,
		rule.ID,
		rule.Name,
		rule.Pattern,
		rule.Action,
		rule.Scope,
		rule.ProjectPath,
		enabled,
		rule.CreatedAt.Format(timeFormat),
	)
	if err != nil {
		return model.ApprovalRule{}, fmt.Errorf("CreateApprovalRule: %w", err)
	}

	return rule, nil
}

// ListApprovalRules returns all approval rules ordered by created_at DESC.
func (s *Store) ListApprovalRules() ([]model.ApprovalRule, error) {
	const query = `SELECT id, name, pattern, action, scope, project_path, enabled, created_at
		FROM approval_rules ORDER BY created_at DESC`

	rows, err := s.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("ListApprovalRules: %w", err)
	}
	defer rows.Close()

	rules := []model.ApprovalRule{}
	for rows.Next() {
		rule, err := scanApprovalRule(rows)
		if err != nil {
			return nil, fmt.Errorf("ListApprovalRules: scanning row: %w", err)
		}
		rules = append(rules, rule)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ListApprovalRules: iterating rows: %w", err)
	}

	return rules, nil
}

// UpdateApprovalRule applies a partial update to the approval rule identified
// by id. Recognised keys: "name", "pattern", "action", "scope",
// "project_path", "enabled".
func (s *Store) UpdateApprovalRule(id string, updates map[string]interface{}) error {
	allowedCols := map[string]bool{
		"name":         true,
		"pattern":      true,
		"action":       true,
		"scope":        true,
		"project_path": true,
		"enabled":      true,
	}

	var (
		setClauses []string
		args       []interface{}
	)

	for col, val := range updates {
		if !allowedCols[col] {
			continue
		}
		setClauses = append(setClauses, col+" = ?")
		args = append(args, val)
	}

	if len(setClauses) == 0 {
		return nil // nothing to update
	}

	query := "UPDATE approval_rules SET " + strings.Join(setClauses, ", ") + " WHERE id = ?"
	args = append(args, id)

	res, err := s.db.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("UpdateApprovalRule: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("UpdateApprovalRule: checking rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("UpdateApprovalRule: rule %q not found: %w", id, sql.ErrNoRows)
	}

	return nil
}

// DeleteApprovalRule removes an approval rule by ID.
func (s *Store) DeleteApprovalRule(id string) error {
	res, err := s.db.Exec("DELETE FROM approval_rules WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("DeleteApprovalRule: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("DeleteApprovalRule: checking rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("DeleteApprovalRule: rule %q not found: %w", id, sql.ErrNoRows)
	}

	return nil
}

// GetEnabledApprovalRules returns only enabled approval rules ordered by
// created_at DESC.
func (s *Store) GetEnabledApprovalRules() ([]model.ApprovalRule, error) {
	const query = `SELECT id, name, pattern, action, scope, project_path, enabled, created_at
		FROM approval_rules WHERE enabled = 1 ORDER BY created_at DESC`

	rows, err := s.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("GetEnabledApprovalRules: %w", err)
	}
	defer rows.Close()

	rules := []model.ApprovalRule{}
	for rows.Next() {
		rule, err := scanApprovalRule(rows)
		if err != nil {
			return nil, fmt.Errorf("GetEnabledApprovalRules: scanning row: %w", err)
		}
		rules = append(rules, rule)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("GetEnabledApprovalRules: iterating rows: %w", err)
	}

	return rules, nil
}

// ---------------------------------------------------------------------------
// Recipe CRUD (Template + Params + Steps)
// ---------------------------------------------------------------------------

// CreateRecipe creates a session template along with its params and steps in a
// single transaction. The template is created first, then params and steps are
// inserted referencing it.
func (s *Store) CreateRecipe(name string, params []model.TemplateParam, steps []model.RecipeStep) (model.SessionTemplate, error) {
	if name == "" {
		return model.SessionTemplate{}, fmt.Errorf("CreateRecipe: name is required")
	}

	tmpl := model.NewSessionTemplate(name, "claude-code", []string{}, "claude")

	tx, err := s.db.Begin()
	if err != nil {
		return model.SessionTemplate{}, fmt.Errorf("CreateRecipe: beginning transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// Insert template.
	repoPathsJSON, err := json.Marshal(tmpl.RepoPaths)
	if err != nil {
		return model.SessionTemplate{}, fmt.Errorf("CreateRecipe: marshalling repo paths: %w", err)
	}

	_, err = tx.Exec(
		`INSERT INTO session_templates (id, name, agent_type, repo_paths, command, created_at)
			VALUES (?, ?, ?, ?, ?, ?)`,
		tmpl.ID, tmpl.Name, tmpl.AgentType, string(repoPathsJSON), tmpl.Command, tmpl.CreatedAt.Format(timeFormat),
	)
	if err != nil {
		return model.SessionTemplate{}, fmt.Errorf("CreateRecipe: inserting template: %w", err)
	}

	// Insert params.
	for i := range params {
		params[i].TemplateID = tmpl.ID
		if params[i].ID == "" {
			params[i] = model.NewTemplateParam(tmpl.ID, params[i].Name, params[i].ParamType, params[i].DefaultValue, params[i].Description, params[i].SortOrder)
		}

		_, err = tx.Exec(
			`INSERT INTO template_params (id, template_id, name, param_type, default_value, description, sort_order)
				VALUES (?, ?, ?, ?, ?, ?, ?)`,
			params[i].ID, params[i].TemplateID, params[i].Name, params[i].ParamType,
			params[i].DefaultValue, params[i].Description, params[i].SortOrder,
		)
		if err != nil {
			return model.SessionTemplate{}, fmt.Errorf("CreateRecipe: inserting param %q: %w", params[i].Name, err)
		}
	}

	// Insert steps.
	for i := range steps {
		steps[i].TemplateID = tmpl.ID
		if steps[i].ID == "" {
			steps[i] = model.NewRecipeStep(tmpl.ID, steps[i].AgentType, steps[i].PromptTemplate, steps[i].DependsOn, steps[i].SortOrder)
		}

		_, err = tx.Exec(
			`INSERT INTO recipe_steps (id, template_id, agent_type, prompt_template, depends_on, sort_order)
				VALUES (?, ?, ?, ?, ?, ?)`,
			steps[i].ID, steps[i].TemplateID, steps[i].AgentType, steps[i].PromptTemplate,
			steps[i].DependsOn, steps[i].SortOrder,
		)
		if err != nil {
			return model.SessionTemplate{}, fmt.Errorf("CreateRecipe: inserting step: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return model.SessionTemplate{}, fmt.Errorf("CreateRecipe: committing transaction: %w", err)
	}

	return tmpl, nil
}

// GetRecipeWithDetails retrieves a session template along with its params and
// steps. Returns the template, params (sorted by sort_order), and steps
// (sorted by sort_order).
func (s *Store) GetRecipeWithDetails(templateID string) (model.SessionTemplate, []model.TemplateParam, []model.RecipeStep, error) {
	tmpl, err := s.GetSessionTemplate(templateID)
	if err != nil {
		return model.SessionTemplate{}, nil, nil, fmt.Errorf("GetRecipeWithDetails: %w", err)
	}

	// Fetch params.
	paramRows, err := s.db.Query(
		`SELECT id, template_id, name, param_type, default_value, description, sort_order
			FROM template_params WHERE template_id = ? ORDER BY sort_order ASC`, templateID)
	if err != nil {
		return model.SessionTemplate{}, nil, nil, fmt.Errorf("GetRecipeWithDetails: listing params: %w", err)
	}
	defer paramRows.Close()

	params := []model.TemplateParam{}
	for paramRows.Next() {
		var p model.TemplateParam
		if err := paramRows.Scan(&p.ID, &p.TemplateID, &p.Name, &p.ParamType, &p.DefaultValue, &p.Description, &p.SortOrder); err != nil {
			return model.SessionTemplate{}, nil, nil, fmt.Errorf("GetRecipeWithDetails: scanning param row: %w", err)
		}
		params = append(params, p)
	}
	if err := paramRows.Err(); err != nil {
		return model.SessionTemplate{}, nil, nil, fmt.Errorf("GetRecipeWithDetails: iterating param rows: %w", err)
	}

	// Fetch steps.
	stepRows, err := s.db.Query(
		`SELECT id, template_id, agent_type, prompt_template, depends_on, sort_order
			FROM recipe_steps WHERE template_id = ? ORDER BY sort_order ASC`, templateID)
	if err != nil {
		return model.SessionTemplate{}, nil, nil, fmt.Errorf("GetRecipeWithDetails: listing steps: %w", err)
	}
	defer stepRows.Close()

	steps := []model.RecipeStep{}
	for stepRows.Next() {
		var st model.RecipeStep
		if err := stepRows.Scan(&st.ID, &st.TemplateID, &st.AgentType, &st.PromptTemplate, &st.DependsOn, &st.SortOrder); err != nil {
			return model.SessionTemplate{}, nil, nil, fmt.Errorf("GetRecipeWithDetails: scanning step row: %w", err)
		}
		steps = append(steps, st)
	}
	if err := stepRows.Err(); err != nil {
		return model.SessionTemplate{}, nil, nil, fmt.Errorf("GetRecipeWithDetails: iterating step rows: %w", err)
	}

	return tmpl, params, steps, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...interface{}) error
}

// scanInto reads a single task from the scanner. The column order must match
// the SELECT used in queries: id, name, description, repo_path, agent_type,
// status, output_path, workflow_id, created_at, updated_at.
func scanInto(sc scanner) (model.Task, error) {
	var (
		t                                    model.Task
		agentType, status                    string
		description, outputPath, workflowID  sql.NullString
		createdAt, updatedAt                 string
	)

	if err := sc.Scan(
		&t.ID,
		&t.Name,
		&description,
		&t.RepoPath,
		&agentType,
		&status,
		&outputPath,
		&workflowID,
		&createdAt,
		&updatedAt,
	); err != nil {
		return model.Task{}, err
	}

	t.AgentType = model.AgentType(agentType)
	t.Status = model.Status(status)
	t.Description = description.String
	t.OutputPath = outputPath.String
	t.WorkflowID = workflowID.String

	var err error
	t.CreatedAt, err = time.Parse(timeFormat, createdAt)
	if err != nil {
		return model.Task{}, fmt.Errorf("parsing created_at %q: %w", createdAt, err)
	}
	t.UpdatedAt, err = time.Parse(timeFormat, updatedAt)
	if err != nil {
		return model.Task{}, fmt.Errorf("parsing updated_at %q: %w", updatedAt, err)
	}

	return t, nil
}

// scanTask scans a single *sql.Row into a model.Task.
func scanTask(row *sql.Row) (model.Task, error) {
	return scanInto(row)
}

// scanTaskRows scans the current row of *sql.Rows into a model.Task.
func scanTaskRows(rows *sql.Rows) (model.Task, error) {
	return scanInto(rows)
}

// scanWorkflowInto reads a single workflow from the scanner. The column order
// must match: id, name, description, created_at, updated_at.
func scanWorkflowInto(sc scanner) (model.Workflow, error) {
	var (
		w           model.Workflow
		description sql.NullString
		createdAt   string
		updatedAt   string
	)

	if err := sc.Scan(
		&w.ID,
		&w.Name,
		&description,
		&createdAt,
		&updatedAt,
	); err != nil {
		return model.Workflow{}, err
	}

	w.Description = description.String

	var err error
	w.CreatedAt, err = time.Parse(timeFormat, createdAt)
	if err != nil {
		return model.Workflow{}, fmt.Errorf("parsing created_at %q: %w", createdAt, err)
	}
	w.UpdatedAt, err = time.Parse(timeFormat, updatedAt)
	if err != nil {
		return model.Workflow{}, fmt.Errorf("parsing updated_at %q: %w", updatedAt, err)
	}

	return w, nil
}

// scanWorkflow scans a single *sql.Row into a model.Workflow.
func scanWorkflow(row *sql.Row) (model.Workflow, error) {
	return scanWorkflowInto(row)
}

// scanWorkflowRows scans the current row of *sql.Rows into a model.Workflow.
func scanWorkflowRows(rows *sql.Rows) (model.Workflow, error) {
	return scanWorkflowInto(rows)
}

// scanSessionInto reads a single session from the scanner. The column order
// must match the SELECT used in queries: id, task_id, agent_type, repo_path,
// prompt, agent_session_id, status, pid, output_path, exit_code,
// error_message, parent_session_id, depends_on, phase, created_at, updated_at.
func scanSessionInto(sc scanner) (model.Session, error) {
	var (
		sess                                                              model.Session
		agentType, status                                                 string
		taskID, agentSessionID, outputPath, errorMessage, parentSessionID sql.NullString
		dependsOnJSON, phase                                              string
		createdAt, updatedAt                                              string
	)

	if err := sc.Scan(
		&sess.ID,
		&taskID,
		&agentType,
		&sess.RepoPath,
		&sess.Prompt,
		&agentSessionID,
		&status,
		&sess.PID,
		&outputPath,
		&sess.ExitCode,
		&errorMessage,
		&parentSessionID,
		&dependsOnJSON,
		&phase,
		&createdAt,
		&updatedAt,
	); err != nil {
		return model.Session{}, err
	}

	sess.AgentType = model.AgentType(agentType)
	sess.Status = model.SessionStatus(status)
	sess.TaskID = taskID.String
	sess.AgentSessionID = agentSessionID.String
	sess.OutputPath = outputPath.String
	sess.ErrorMessage = errorMessage.String
	sess.ParentSessionID = parentSessionID.String
	sess.Phase = phase

	// Deserialise depends_on JSON array. Default to empty slice on any error
	// so the frontend always receives [] rather than null.
	sess.DependsOn = []string{}
	if dependsOnJSON != "" && dependsOnJSON != "[]" {
		if err := json.Unmarshal([]byte(dependsOnJSON), &sess.DependsOn); err != nil {
			return model.Session{}, fmt.Errorf("unmarshalling depends_on: %w", err)
		}
	}

	var err error
	sess.CreatedAt, err = time.Parse(timeFormat, createdAt)
	if err != nil {
		return model.Session{}, fmt.Errorf("parsing created_at %q: %w", createdAt, err)
	}
	sess.UpdatedAt, err = time.Parse(timeFormat, updatedAt)
	if err != nil {
		return model.Session{}, fmt.Errorf("parsing updated_at %q: %w", updatedAt, err)
	}

	return sess, nil
}

// scanSession scans a single *sql.Row into a model.Session.
func scanSession(row *sql.Row) (model.Session, error) {
	return scanSessionInto(row)
}

// scanSessionRows scans the current row of *sql.Rows into a model.Session.
func scanSessionRows(rows *sql.Rows) (model.Session, error) {
	return scanSessionInto(rows)
}

// nullString converts an empty Go string to a sql.NullString with Valid=false,
// and a non-empty string to a valid NullString. This is used for nullable TEXT
// columns.
func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

// scanActivityEvent scans a single row into a model.ActivityEvent. The column
// order must match: id, task_id, task_name, event_type, message, metadata,
// created_at.
func scanActivityEvent(sc scanner) (model.ActivityEvent, error) {
	var (
		e         model.ActivityEvent
		taskID    sql.NullString
		metadata  sql.NullString
		createdAt string
	)

	if err := sc.Scan(
		&e.ID,
		&taskID,
		&e.TaskName,
		&e.EventType,
		&e.Message,
		&metadata,
		&createdAt,
	); err != nil {
		return model.ActivityEvent{}, err
	}

	e.TaskID = taskID.String
	e.Metadata = metadata.String

	var err error
	e.CreatedAt, err = time.Parse(timeFormat, createdAt)
	if err != nil {
		return model.ActivityEvent{}, fmt.Errorf("parsing created_at %q: %w", createdAt, err)
	}

	return e, nil
}

// scanSessionGroup scans a single row into a model.SessionGroup. The column
// order must match: id, name, description, color, created_at, updated_at.
func scanSessionGroup(sc scanner) (model.SessionGroup, error) {
	var (
		g                      model.SessionGroup
		description            sql.NullString
		color                  sql.NullString
		createdAt, updatedAt   string
	)

	if err := sc.Scan(
		&g.ID,
		&g.Name,
		&description,
		&color,
		&createdAt,
		&updatedAt,
	); err != nil {
		return model.SessionGroup{}, err
	}

	g.Description = description.String
	g.Color = color.String

	var err error
	g.CreatedAt, err = time.Parse(timeFormat, createdAt)
	if err != nil {
		return model.SessionGroup{}, fmt.Errorf("parsing created_at %q: %w", createdAt, err)
	}
	g.UpdatedAt, err = time.Parse(timeFormat, updatedAt)
	if err != nil {
		return model.SessionGroup{}, fmt.Errorf("parsing updated_at %q: %w", updatedAt, err)
	}

	return g, nil
}

// scanSessionTemplate scans a single row into a model.SessionTemplate. The
// column order must match: id, name, agent_type, repo_paths, command,
// created_at. The repo_paths column is deserialised from JSON.
func scanSessionTemplate(sc scanner) (model.SessionTemplate, error) {
	var (
		t             model.SessionTemplate
		repoPathsJSON string
		createdAt     string
	)

	if err := sc.Scan(
		&t.ID,
		&t.Name,
		&t.AgentType,
		&repoPathsJSON,
		&t.Command,
		&createdAt,
	); err != nil {
		return model.SessionTemplate{}, err
	}

	if err := json.Unmarshal([]byte(repoPathsJSON), &t.RepoPaths); err != nil {
		return model.SessionTemplate{}, fmt.Errorf("unmarshalling repo paths: %w", err)
	}

	var err error
	t.CreatedAt, err = time.Parse(timeFormat, createdAt)
	if err != nil {
		return model.SessionTemplate{}, fmt.Errorf("parsing created_at %q: %w", createdAt, err)
	}

	return t, nil
}

// scanCostSnapshot scans a single row into a model.CostSnapshot. The column
// order must match: id, session_id, project_path, input_tokens, output_tokens,
// cost_usd, recorded_at.
func scanCostSnapshot(sc scanner) (model.CostSnapshot, error) {
	var (
		c          model.CostSnapshot
		recordedAt string
	)

	if err := sc.Scan(
		&c.ID,
		&c.SessionID,
		&c.ProjectPath,
		&c.InputTokens,
		&c.OutputTokens,
		&c.CostUSD,
		&recordedAt,
	); err != nil {
		return model.CostSnapshot{}, err
	}

	var err error
	c.RecordedAt, err = time.Parse(timeFormat, recordedAt)
	if err != nil {
		return model.CostSnapshot{}, fmt.Errorf("parsing recorded_at %q: %w", recordedAt, err)
	}

	return c, nil
}

// scanApprovalRule scans a single row into a model.ApprovalRule. The column
// order must match: id, name, pattern, action, scope, project_path, enabled,
// created_at.
func scanApprovalRule(sc scanner) (model.ApprovalRule, error) {
	var (
		rule      model.ApprovalRule
		enabled   int
		createdAt string
	)

	if err := sc.Scan(
		&rule.ID,
		&rule.Name,
		&rule.Pattern,
		&rule.Action,
		&rule.Scope,
		&rule.ProjectPath,
		&enabled,
		&createdAt,
	); err != nil {
		return model.ApprovalRule{}, err
	}

	rule.Enabled = enabled != 0

	var err error
	rule.CreatedAt, err = time.Parse(timeFormat, createdAt)
	if err != nil {
		return model.ApprovalRule{}, fmt.Errorf("parsing created_at %q: %w", createdAt, err)
	}

	return rule, nil
}
