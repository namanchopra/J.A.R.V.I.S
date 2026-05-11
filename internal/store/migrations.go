package store

import (
	"database/sql"
	"fmt"
	"log/slog"
)

// migrations is an ordered list of SQL statements. Each entry corresponds to a
// schema version (1-indexed). New migrations are appended to the end of the
// slice; existing entries must never be modified.
var migrations = []string{
	// Version 1 — create the tasks table.
	`CREATE TABLE IF NOT EXISTS tasks (
		id          TEXT PRIMARY KEY,
		name        TEXT NOT NULL,
		description TEXT,
		repo_path   TEXT NOT NULL,
		agent_type  TEXT NOT NULL,
		status      TEXT NOT NULL DEFAULT 'pending',
		output_path TEXT,
		created_at  DATETIME NOT NULL,
		updated_at  DATETIME NOT NULL
	);`,

	// Version 2 — create the workflows table and add workflow_id to tasks.
	`CREATE TABLE IF NOT EXISTS workflows (
		id          TEXT PRIMARY KEY,
		name        TEXT NOT NULL,
		description TEXT,
		created_at  DATETIME NOT NULL,
		updated_at  DATETIME NOT NULL
	);
	ALTER TABLE tasks ADD COLUMN workflow_id TEXT REFERENCES workflows(id);`,

	// Version 3 — create the activity_events table.
	`CREATE TABLE IF NOT EXISTS activity_events (
		id         TEXT PRIMARY KEY,
		task_id    TEXT,
		task_name  TEXT NOT NULL,
		event_type TEXT NOT NULL,
		message    TEXT NOT NULL,
		metadata   TEXT,
		created_at DATETIME NOT NULL
	);
	CREATE INDEX idx_activity_events_created_at ON activity_events(created_at);
	CREATE INDEX idx_activity_events_task_id ON activity_events(task_id);`,

	// Version 4 — create the sessions table.
	`CREATE TABLE sessions (
		id               TEXT PRIMARY KEY,
		task_id          TEXT,
		agent_type       TEXT NOT NULL,
		repo_path        TEXT NOT NULL,
		prompt           TEXT NOT NULL,
		agent_session_id TEXT,
		status           TEXT NOT NULL DEFAULT 'launching',
		pid              INTEGER DEFAULT 0,
		output_path      TEXT,
		exit_code        INTEGER DEFAULT 0,
		error_message    TEXT,
		created_at       DATETIME NOT NULL,
		updated_at       DATETIME NOT NULL
	);
	CREATE INDEX idx_sessions_status ON sessions(status);
	CREATE INDEX idx_sessions_created_at ON sessions(created_at);`,

	// Version 5 — create the projects and project_repos tables for project
	// discovery and divide-and-conquer features.
	`CREATE TABLE projects (
		id          TEXT PRIMARY KEY,
		name        TEXT NOT NULL,
		path        TEXT NOT NULL UNIQUE,
		is_monorepo BOOLEAN DEFAULT 0,
		created_at  DATETIME NOT NULL,
		updated_at  DATETIME NOT NULL
	);

	CREATE TABLE project_repos (
		project_id TEXT NOT NULL REFERENCES projects(id),
		repo_path  TEXT NOT NULL,
		PRIMARY KEY (project_id, repo_path)
	);`,

	// Version 6 — create session_groups, session_group_members,
	// session_templates, and cost_snapshots tables.
	`CREATE TABLE IF NOT EXISTS session_groups (
		id          TEXT PRIMARY KEY,
		name        TEXT NOT NULL,
		description TEXT,
		color       TEXT DEFAULT '#8b949e',
		created_at  DATETIME NOT NULL,
		updated_at  DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS session_group_members (
		group_id  TEXT NOT NULL REFERENCES session_groups(id) ON DELETE CASCADE,
		repo_path TEXT NOT NULL,
		added_at  DATETIME NOT NULL,
		PRIMARY KEY (group_id, repo_path)
	);

	CREATE TABLE IF NOT EXISTS session_templates (
		id         TEXT PRIMARY KEY,
		name       TEXT NOT NULL,
		agent_type TEXT NOT NULL DEFAULT 'claude-code',
		repo_paths TEXT NOT NULL,
		command    TEXT NOT NULL DEFAULT 'claude',
		created_at DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS cost_snapshots (
		id           TEXT PRIMARY KEY,
		session_id   TEXT NOT NULL,
		project_path TEXT NOT NULL,
		input_tokens  INTEGER DEFAULT 0,
		output_tokens INTEGER DEFAULT 0,
		cost_usd     REAL DEFAULT 0,
		recorded_at  DATETIME NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_cost_snapshots_session ON cost_snapshots(session_id);
	CREATE INDEX IF NOT EXISTS idx_cost_snapshots_project ON cost_snapshots(project_path);
	CREATE INDEX IF NOT EXISTS idx_cost_snapshots_recorded ON cost_snapshots(recorded_at);`,

	// Version 7 — create the session_todos table for per-session to-do items.
	`CREATE TABLE IF NOT EXISTS session_todos (
		id         TEXT PRIMARY KEY,
		session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
		title      TEXT NOT NULL,
		status     TEXT NOT NULL DEFAULT 'pending',
		sort_order INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME NOT NULL
	);`,

	// Version 8 — create the approval_rules table for automatic approval/deny rules.
	`CREATE TABLE IF NOT EXISTS approval_rules (
		id           TEXT PRIMARY KEY,
		name         TEXT NOT NULL,
		pattern      TEXT NOT NULL,
		action       TEXT NOT NULL DEFAULT 'approve',
		scope        TEXT NOT NULL DEFAULT 'all',
		project_path TEXT NOT NULL DEFAULT '',
		enabled      INTEGER NOT NULL DEFAULT 1,
		created_at   DATETIME NOT NULL
	);`,

	// Version 9 — add parent_session_id to sessions for session forking.
	`ALTER TABLE sessions ADD COLUMN parent_session_id TEXT DEFAULT '';`,

	// Version 10 — create template_params and recipe_steps tables for multi-step recipes.
	`CREATE TABLE IF NOT EXISTS template_params (
		id            TEXT PRIMARY KEY,
		template_id   TEXT NOT NULL REFERENCES session_templates(id) ON DELETE CASCADE,
		name          TEXT NOT NULL,
		param_type    TEXT NOT NULL DEFAULT 'string',
		default_value TEXT NOT NULL DEFAULT '',
		description   TEXT NOT NULL DEFAULT '',
		sort_order    INTEGER NOT NULL DEFAULT 0
	);

	CREATE TABLE IF NOT EXISTS recipe_steps (
		id              TEXT PRIMARY KEY,
		template_id     TEXT NOT NULL REFERENCES session_templates(id) ON DELETE CASCADE,
		agent_type      TEXT NOT NULL,
		prompt_template TEXT NOT NULL,
		depends_on      TEXT NOT NULL DEFAULT '',
		sort_order      INTEGER NOT NULL DEFAULT 0
	);`,

	// Version 11 — add depends_on (JSON array of session IDs) and phase columns
	// to sessions for dependency-aware scheduling.
	`ALTER TABLE sessions ADD COLUMN depends_on TEXT NOT NULL DEFAULT '[]';
	ALTER TABLE sessions ADD COLUMN phase TEXT NOT NULL DEFAULT '';`,
}

// runMigrations applies any outstanding schema migrations to db. It uses a
// schema_version table to track which migrations have already been applied.
func runMigrations(db *sql.DB) error {
	// Ensure the version-tracking table exists.
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (
		version INTEGER NOT NULL
	)`); err != nil {
		return fmt.Errorf("creating schema_version table: %w", err)
	}

	// Read the current version. A missing row means version 0.
	var current int
	row := db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_version")
	if err := row.Scan(&current); err != nil {
		return fmt.Errorf("reading schema version: %w", err)
	}

	// Apply each migration whose version is greater than current.
	for i := current; i < len(migrations); i++ {
		version := i + 1
		slog.Info("applying migration", "version", version)

		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("beginning transaction for migration %d: %w", version, err)
		}

		if _, err := tx.Exec(migrations[i]); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("executing migration %d: %w", version, err)
		}

		if _, err := tx.Exec("INSERT INTO schema_version (version) VALUES (?)", version); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("recording migration %d: %w", version, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("committing migration %d: %w", version, err)
		}

		slog.Info("migration applied", "version", version)
	}

	return nil
}
