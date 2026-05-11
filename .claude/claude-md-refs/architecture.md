# Architecture

## System Overview

Jarvis is a native desktop app for managing AI coding agent sessions across multiple projects. Built with **Wails v2** (Go backend + React frontend in a native WebView).

## Dependency Graph

```
main.go
  |
  +-- app.go (Wails-bound App struct, ~90 exported methods)
  |     |
  |     +-- internal/store/         SQLite persistence
  |     +-- internal/agent/         Session lifecycle (adapters)
  |     +-- internal/scanner/       Process auto-detection
  |     +-- internal/terminal/      Terminal control (CMux/iTerm2/Terminal.app)
  |     +-- internal/git/           Git operations
  |     +-- internal/workspace/     Virtual monorepo workspaces
  |     +-- internal/discovery/     Project filesystem discovery
  |     +-- internal/config/        Config file (~/.jarvis/config.json)
  |     +-- internal/claude/        Claude-specific usage/sessions
  |     +-- internal/notify/        macOS notifications
  |     +-- internal/impact/        Cross-session conflict detection
  |     +-- internal/nlquery/       Natural language command engine
  |     +-- internal/recording/     Session recording/replay
  |     +-- internal/cmux/          CMux socket RPC client
  |     +-- internal/api/           Echo HTTP server (mobile API)
  |     +-- internal/watcher/       File tail watcher
  |     +-- internal/ci/            CI pipeline watcher
  |     +-- internal/proc/          Process alive check
  |
  +-- internal/cli/            Cobra CLI (add, list, update, remove, open)
  |
  +-- cmd/awm-cmux-helper/     Standalone CMux helper binary

frontend/
  +-- src/App.tsx               View router (10 views)
  +-- src/views/                View components
  +-- src/components/           UI components (~40)
  +-- src/lib/                  Utilities & hooks
  +-- wailsjs/                  Auto-generated Go bindings
```

## Request Lifecycle

### Desktop (Wails)
```
User action in React UI
  -> Wails JS binding call (e.g., GetTasks("running"))
  -> app.go exported method
  -> internal package (store, agent, git, etc.)
  -> Return value serialized as JSON to frontend
```

### Mobile API (Echo)
```
Mobile app HTTP request
  -> Bearer token auth middleware
  -> Echo route handler (api/handlers_*.go)
  -> Provider interface method (satisfied by App struct)
  -> Same internal packages
  -> JSON response
```

### WebSocket (Terminal Output)
```
Mobile app WebSocket connection
  -> ?token= query param auth
  -> Upgrade to WebSocket
  -> Poll GetSessionTerminalOutput every 500ms
  -> Send delta text to client
```

## Mobile API Routes

| Method | Path | Handler | Purpose |
|--------|------|---------|---------|
| GET | /ping | handlePing | Health check |
| GET | /dashboard | handleDashboard | Stats + active sessions/tasks |
| GET | /activity | handleActivity | Paginated activity feed |
| GET | /indicators | handleIndicators | Claude session indicators |
| GET | /tasks/:id | handleGetTask | Single task by ID |
| GET | /sessions | list | List sessions (optional ?status) |
| GET | /sessions/:id | get | Single session by ID |
| POST | /sessions/:id/stop | stop | Stop running session |
| GET | /workspaces | handleListWorkspaces | Virtual monorepo workspaces |
| DELETE | /workspaces/:id | handleDeleteWorkspace | Delete workspace |
| GET | /saved-projects | handleListSavedProjects | Saved projects |
| GET | /approvals | list | Pending approval prompts |
| POST | /approvals/:pid/respond | respond | Answer y/n to approval |
| GET | /settings | getSettings | Current config (safe subset) |
| PUT | /settings | putSettings | Partial config update |
| POST | /push-token | registerToken | Register Expo push token |
| GET | /ws/sessions/:id/output | WebSocket | Stream terminal output |

Auth: Bearer token via `Authorization` header (HTTP) or `?token=` (WebSocket).

## CLI Commands

| Command | Purpose |
|---------|---------|
| `awm` | Launch desktop window (no args) |
| `awm add` | Create a new task |
| `awm list` | List tasks (with filters) |
| `awm update` | Update task fields |
| `awm remove` | Delete a task |
| `awm open` | Explicitly launch desktop window |
| `awm --help` | Show help |
| `awm --version` | Show version |

Output format: `--output table` (default) or `--output json`.

## Database Schema (SQLite)

Location: `~/.jarvis/awm.db` | Journal: WAL | Max connections: 1

| Table | Migration | Purpose |
|-------|-----------|---------|
| tasks | v1 | Core task records |
| workflows | v2 | Workflow groupings |
| activity_events | v3 | Activity feed events |
| sessions | v4 | Agent session records |
| projects | v5 | Saved project metadata |
| project_repos | v5 | Project-to-repo mappings |
| session_groups | v6 | Named repo collections |
| session_group_members | v6 | Group membership (CASCADE) |
| session_templates | v6 | Reusable session configs |
| cost_snapshots | v6 | Token usage tracking |
| schema_version | auto | Migration version tracker |

## Agent Adapter Pattern

```
AgentAdapter (interface)
  |
  +-- ClaudeAdapter    claude --print --output-format stream-json
  +-- KiroAdapter      kiro-cli
  +-- GeminiAdapter    gemini
  +-- CodexAdapter     codex
  +-- AiderAdapter     aider
```

Each adapter implements: `Name()`, `Launch()`, `SendMessage()`, `Stop()`, `IsAvailable()`.

The `SessionManager` (agent/manager.go):
1. Registers adapters at startup
2. Launches sessions via adapter
3. Streams output to log files + Wails events
4. Detects "needs input" via heuristic text matching
5. Sends macOS notifications on completion/failure
6. Recovers sessions on restart

## Terminal Control Architecture

```
TerminalManager
  |
  +-- CMuxProvider       Direct socket RPC (password auth)
  +-- ITerm2Provider     AppleScript automation
  +-- MacOSTermProvider  AppleScript automation
```

Focus/send/read operations route through the preferred terminal provider.

## Key Subsystems

### Process Scanner (`internal/scanner/`)
Scans OS process table every 5s using gopsutil. Matches processes to known agent types (claude, kiro, gemini, codex, aider) via exact name or cmdline pattern matching. Filters out Electron helpers, IDE extensions, and non-git processes. Auto-creates tasks for detected agents, marks tasks done when processes exit.

### Workspace System (`internal/workspace/`)
Creates virtual monorepo directories at `~/.jarvis/workspaces/<name>/` with symlinks to real repos. Auto-generates CLAUDE.md with task description, repo metadata, and cross-repo guidelines. Copies `.claude/` from dotAiAgent source. Launches sessions with `--add-dir` flags.

### Impact Detection (`internal/impact/`)
Compares `git diff --name-only HEAD` across active sessions. Detects 3 conflict types: shared-dependency (both modify package.json/go.mod), shared-file (both touch shared/ or common/ dirs), API contract (both modify /api/ or /routes/ dirs).

### Project Discovery (`internal/discovery/`)
Scans configured root paths 2 levels deep for git repos. Groups by parent directory into Projects. Detects language, branch, and active agent sessions. Suggests coordinated tasks based on repo relationships.

### Cost Tracking (`internal/claude/`)
Reads Claude Code session cost data from `~/.claude/projects/*/sessions/*/` JSONL files. Aggregates by session, project, day. Snapshots stored in cost_snapshots table.

### NL Query Engine (`internal/nlquery/`)
Pure keyword-matching (no LLM). Supports: show idle/active/running sessions, total cost/spend, stop/kill session, broadcast command, count sessions, history/recordings. Returns structured `QueryResult` with action type.

## Configuration (`~/.jarvis/config.json`)

| Key | Default | Purpose |
|-----|---------|---------|
| defaultAgent | `"claude-code"` | Default agent for new sessions |
| scanIntervalSeconds | `5` | Process scan interval |
| preferredTerminal | `""` (auto) | Terminal provider override |
| projectRootPaths | `null` | Directories to scan for repos |
| notificationsEnabled | `true` | macOS notification toggle |
| notifyOnApproval | `true` | Notify on approval prompts |
| notifyOnCompletion | `true` | Notify on session complete |
| ciWatchEnabled | `false` | CI pipeline monitoring |
| ciProvider | `""` | `"github-actions"` or `"gitlab-ci"` |
| defaultCommand | `"claude"` | CLI command for sessions |
| mobileAPIPort | `4422` | HTTP server port |
| mobileAPIToken | (auto-generated) | Bearer token |
| dotClaudeSourcePath | `""` | .claude source directory |

## Frontend View Map

| ViewId | Component | Purpose |
|--------|-----------|---------|
| `control-center` | ControlCenterView | Main hub: session indicators, broadcast, NL commands |
| `dashboard` | DashboardView | Stats, active sessions/tasks, recent activity |
| `activity` | ActivityView | Chronological activity feed |
| `tasks` | TasksView | Task list + detail with output/git/activity |
| `sessions` | SessionsView | Session management, launch, output streaming |
| `workflows` | WorkflowsView | Create/manage workflows, assign tasks |
| `history` | HistoryView | Session recordings/replay |
| `costs` | CostDashboard (inline) | Token usage, daily costs, spend summary |
| `groups` | SessionGroups (inline) | Repo group management |
| `settings` | SettingsView | Configuration, mobile API, terminal setup |

## Wails Events (Go -> Frontend)

| Event | Payload | Purpose |
|-------|---------|---------|
| `session_launched` | `{sessionId, agentType, repoPath}` | Session started |
| `session:<id>` | `OutputLine` | Live output line |
| `session:<id>:status` | `string` | Status change |
| `output:<taskID>` | `string` | Task output tail line |
