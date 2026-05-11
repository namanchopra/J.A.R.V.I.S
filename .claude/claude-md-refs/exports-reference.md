# Exports Reference

## Domain Models (`internal/model/`)

| Model | File | Key Fields | Purpose |
|-------|------|------------|---------|
| Task | task.go | ID, Name, Description, RepoPath, AgentType, Status, OutputPath, WorkflowID | Unit of work delegated to an AI agent |
| Workflow | task.go | ID, Name, Description | Groups related tasks |
| Session | session.go | ID, TaskID, AgentType, RepoPath, Prompt, AgentSessionID, Status, PID, OutputPath | Managed AI agent session |
| ActivityEvent | task.go | ID, TaskID, TaskName, EventType, Message, Metadata | Lifecycle event for activity feed |
| DashboardStats | task.go | Total, Running, Pending, Done, Failed, NeedsInput | Aggregate task counts |
| SessionGroup | group.go | ID, Name, Description, Color | Named collection of repo paths |
| GroupMember | group.go | GroupID, RepoPath, AddedAt | Repo membership in a group |
| SessionTemplate | template.go | ID, Name, AgentType, RepoPaths, Command | Reusable session configuration |
| CostSnapshot | cost.go | ID, SessionID, ProjectPath, InputTokens, OutputTokens, CostUSD | Token usage at a point in time |
| DailyCost | cost.go | Date, InputTokens, OutputTokens, CostUSD, SessionCount | Aggregated daily cost |
| TotalSpend | cost.go | AllTime, ThisMonth, Today | Cumulative cost summary |
| ApprovalRequest | approval.go | PID, SessionName, CWD, PromptText, DetectedAt | Detected approval prompt |

## Enums

| Enum | Values | File |
|------|--------|------|
| Status (Task) | `pending`, `running`, `done`, `failed`, `needs-input` | task.go |
| SessionStatus | `launching`, `running`, `paused`, `completed`, `failed`, `needs-input` | session.go |
| AgentType | `claude-code`, `kiro`, `gemini`, `codex`, `aider`, `other` | task.go |

## State Machines

### Task Status
```
pending -> running -> done
    |         |
    v         v
  failed   needs-input -> running
    |
    v
  pending (retry)
```

| From | To | Allowed |
|------|----|---------|
| pending | running, done, failed, needs-input | Yes |
| running | done, failed, needs-input | Yes |
| done | pending, running | **No** |
| failed | pending, running | Yes (retry) |
| needs-input | running, done, failed | Yes |

### Session Status
```
launching -> running -> completed
                |
                v
             needs-input -> running
                |
                v
              failed
```

## Agent Adapter Interface (`internal/agent/adapter.go`)

| Method | Signature | Purpose |
|--------|-----------|---------|
| Name | `() AgentType` | Returns agent type |
| Launch | `(ctx, LaunchOptions) (*RunningSession, error)` | Start new session |
| SendMessage | `(ctx, *RunningSession, string) error` | Send follow-up |
| Stop | `(ctx, *RunningSession) error` | Gracefully terminate |
| IsAvailable | `() bool` | Check CLI installed |

Adapters: `internal/agent/claude.go`, `kiro.go`, `gemini.go`, `codex.go`, `aider.go`

## Wails Bindings (`app.go`) -- All exported App methods

### Tasks
| Method | Signature | Purpose |
|--------|-----------|---------|
| GetTasks | `(statusFilter) -> []Task` | List tasks with optional filter |
| GetTask | `(id) -> Task` | Get single task |
| CreateTask | `(name, desc, repoPath, agentType) -> Task` | Create new task |
| UpdateTaskStatus | `(id, status) -> Task` | Change task status |
| UpdateTaskOutputPath | `(id, outputPath) -> Task` | Set output file path |
| DeleteTask | `(id) -> error` | Remove task |
| GetTaskOutput | `(id, lastN) -> []string` | Read last N lines of output |
| GetTasksGroupedByRepo | `() -> map[string][]Task` | Active tasks by repo |
| GetTaskGitInfo | `(taskID) -> RepoInfo` | Git info for task's repo |
| GetTaskDiff | `(taskID) -> DiffResult` | Git diff for task's repo |

### Sessions
| Method | Signature | Purpose |
|--------|-----------|---------|
| LaunchSession | `(agentType, repoPath, prompt) -> Session` | Start agent session |
| SendSessionMessage | `(sessionID, message) -> error` | Send message to session |
| StopSession | `(sessionID) -> error` | Stop running session |
| ResumeSession | `(sessionID) -> Session` | Resume completed/paused session |
| GetSession | `(sessionID) -> Session` | Get single session |
| ListSessions | `(statusFilter) -> []Session` | List sessions |
| GetActiveSessions | `() -> []Session` | Active sessions only |
| DeleteSession | `(sessionID) -> error` | Remove session |
| GetAvailableAgents | `() -> []AgentInfo` | List registered adapters |
| GetSessionDiff | `(pid) -> DiffResult` | Git diff for session's repo |

### Workflows
| Method | Signature | Purpose |
|--------|-----------|---------|
| CreateWorkflow | `(name, desc) -> Workflow` | Create workflow |
| GetWorkflows | `() -> []Workflow` | List all workflows |
| DeleteWorkflow | `(id) -> error` | Delete workflow |
| AddTaskToWorkflow | `(taskID, workflowID) -> Task` | Link task to workflow |
| RemoveTaskFromWorkflow | `(taskID) -> Task` | Unlink task |
| GetWorkflowTasks | `(workflowID) -> []Task` | Tasks in workflow |

### Activity & Dashboard
| Method | Signature | Purpose |
|--------|-----------|---------|
| GetDashboardStats | `() -> DashboardStats` | Aggregate task counts |
| GetActivityFeed | `(limit, beforeID) -> []ActivityEvent` | Paginated activity feed |
| GetTaskActivity | `(taskID, limit) -> []ActivityEvent` | Activity for one task |
| SearchOutput | `(query) -> []OutputSearchResult` | Grep across output files |
| GetRunningTasks | `() -> []Task` | Running/needs-input tasks |

### Git Operations
| Method | Signature | Purpose |
|--------|-----------|---------|
| GetRepoInfo | `(repoPath) -> RepoInfo` | Branch, commits, diff stats |
| GetRepoDiff | `(repoPath) -> DiffResult` | Parsed unified diff |
| GetStagedDiff | `(repoPath) -> DiffResult` | Staged changes diff |
| GitStageAll | `(repoPath) -> error` | `git add -A` |
| GitStageFiles | `(repoPath, files) -> error` | `git add <files>` |
| GitCommit | `(repoPath, message) -> error` | `git commit -m` |
| GitPush | `(repoPath) -> error` | `git push` |
| GitCreateBranch | `(repoPath, name) -> error` | `git checkout -b` |
| OpenPRInBrowser | `(repoPath) -> error` | Opens GitHub/GitLab PR URL |

### Session Groups
| Method | Signature | Purpose |
|--------|-----------|---------|
| CreateSessionGroup | `(name, desc, color) -> SessionGroup` | Create group |
| ListSessionGroups | `() -> []SessionGroup` | List all groups |
| DeleteSessionGroup | `(id) -> error` | Delete group |
| AddToSessionGroup | `(groupID, repoPath) -> error` | Add repo to group |
| RemoveFromSessionGroup | `(groupID, repoPath) -> error` | Remove repo from group |
| GetSessionGroupMembers | `(groupID) -> []GroupMember` | List group repos |

### Terminal Control
| Method | Signature | Purpose |
|--------|-----------|---------|
| IsCMuxAvailable | `() -> bool` | Check CMux installed |
| GetCMuxWorkspaces | `() -> []Workspace` | List CMux workspaces |
| GetCMuxSurfaces | `() -> []Surface` | List terminal surfaces |
| SendToCMux | `(surfaceRef, text) -> error` | Send text to surface |
| ReadFromCMux | `(surfaceRef) -> string` | Read terminal output |
| FocusCMuxSurface | `(surfaceRef) -> error` | Focus terminal |
| GetTerminalWindows | `() -> []TerminalWindow` | All terminal windows |
| SendToTerminal | `(windowID, text) -> error` | Send to terminal |
| ReadFromTerminal | `(windowID) -> string` | Read from terminal |
| FocusTerminalWindow | `(windowID) -> error` | Focus window |
| GetAvailableTerminals | `() -> []string` | Available terminal types |

### Claude Sessions (Direct)
| Method | Signature | Purpose |
|--------|-----------|---------|
| GetClaudeSessions | `() -> []claude.Session` | Active Claude sessions |
| GetSessionIndicators | `() -> []SessionIndicator` | Session status indicators |
| SendCommandToSession | `(pid, command) -> error` | Send command via terminal |
| BroadcastCommand | `(pids, command) -> map[int]string` | Send to multiple sessions |
| BroadcastToAll | `(command) -> map[int]string` | Send to all sessions |
| FocusSession | `(pid) -> error` | Focus terminal window |
| GetSessionTerminalOutput | `(pid) -> string` | Read live terminal output |
| GetPendingApprovals | `() -> []ApprovalRequest` | Approval prompts |
| RespondToApproval | `(pid, response) -> error` | Answer y/n |

### Projects & Discovery
| Method | Signature | Purpose |
|--------|-----------|---------|
| DiscoverProjects | `() -> []discovery.Project` | Scan filesystem for repos |
| GetProjectSuggestions | `(projectPath) -> []TaskSuggestion` | Suggested tasks for project |
| SearchRepos | `(query) -> []RepoSearchResult` | Search repos by name |
| SaveProject | `(name, path, repoPaths) -> error` | Save project to DB |
| ListSavedProjects | `() -> []store.Project` | List saved projects |
| DeleteSavedProject | `(id) -> error` | Delete saved project |

### Workspaces (Virtual Monorepo)
| Method | Signature | Purpose |
|--------|-----------|---------|
| CreateWorkspace | `(name, repoPaths, prompt) -> Workspace` | Create virtual monorepo |
| CreateWorkspaceAndLaunch | `(name, repoPaths, prompt) -> Workspace` | Create + launch session |
| ListWorkspaces | `() -> []Workspace` | List all workspaces |
| DeleteWorkspace | `(path) -> error` | Delete workspace |
| SyncDotClaude | `() -> int` | Sync .claude to workspaces |
| OpenWorkspaceInTerminal | `(workspacePath) -> error` | Open in terminal |

### Cost Tracking
| Method | Signature | Purpose |
|--------|-----------|---------|
| GetTotalSpend | `() -> TotalSpend` | All-time/month/today spend |
| GetDailyCostSummary | `() -> []DailyCost` | Daily cost breakdown |
| GetProjectCosts | `(projectPath) -> []SessionUsage` | Costs for one project |
| GetAllCosts | `() -> []SessionUsage` | All session costs |

### Other
| Method | Signature | Purpose |
|--------|-----------|---------|
| ScanNow | `() -> int` | Trigger process scan |
| GetAutoDetectedCount | `() -> int` | Auto-detected task count |
| WatchTaskOutput | `(taskID) -> error` | Start live output tail |
| StopWatchingOutput | `(taskID)` | Stop live tail |
| ExecuteDivideAndConquer | `(agentType, repoPaths, prompt, sequential) -> error` | Multi-repo execution |
| LaunchReposInTerminal | `(repoPaths, command) -> error` | Launch in terminal tabs |
| SaveSessionTemplate | `(name, agentType, repoPaths, command) -> Template` | Save template |
| ListSessionTemplates | `() -> []SessionTemplate` | List templates |
| DeleteSessionTemplate | `(id) -> error` | Delete template |
| LaunchFromTemplate | `(templateID) -> error` | Launch from template |
| SuggestWorkflows | `() -> []WorkflowSuggestion` | AI workflow suggestions |
| GetImpactWarnings | `() -> []ImpactWarning` | Cross-session conflicts |
| GetConfig | `() -> Config` | Load settings |
| SaveConfig | `(cfg) -> error` | Save settings |
| GetMobileConnectionInfo | `() -> MobileConnectionInfo` | Mobile API connection info |
| RegenerateMobileToken | `() -> error` | Regenerate Bearer token |
| SetDotClaudeSource | `(path) -> error` | Set .claude source path |
| ListRecordedSessions | `() -> []RecordingSummary` | Session recordings |
| GetSessionRecording | `(sessionID) -> []Snapshot` | Replay recording |
| ExecuteNLQuery | `(query) -> QueryResult` | Natural language command |

## Store Methods (`internal/store/sqlite.go`)

| Method | Purpose |
|--------|---------|
| CreateTask, GetTask, ListTasks, UpdateTask, DeleteTask | Task CRUD |
| CreateWorkflow, GetWorkflow, ListWorkflows, UpdateWorkflow, DeleteWorkflow | Workflow CRUD |
| GetWorkflowTasks | Tasks linked to workflow |
| GetDashboardStats | Aggregate task counts |
| CreateActivityEvent, ListActivityEvents, ListTaskActivityEvents | Activity events |
| SearchOutput | Grep across output files |
| CreateSession, GetSession, ListSessions, UpdateSession, DeleteSession | Session CRUD |
| GetActiveSessions | Non-terminal sessions |
| CreateProject, ListProjects, DeleteProject, GetProjectRepos, SetProjectRepos | Project CRUD |
| CreateSessionGroup, ListSessionGroups, DeleteSessionGroup | Group CRUD |
| AddToGroup, RemoveFromGroup, GetGroupMembers | Group membership |
| CreateSessionTemplate, ListSessionTemplates, GetSessionTemplate, DeleteSessionTemplate | Template CRUD |
| InsertCostSnapshot, GetCostsBySession, GetCostsByProject | Cost tracking |
| CountAutoDetected | Auto-detected task count |

## Frontend Views (`frontend/src/views/`)

| View | File | ViewId | Purpose |
|------|------|--------|---------|
| ControlCenterView | ControlCenterView.tsx | `control-center` | Main hub with session indicators |
| DashboardView | DashboardView.tsx | `dashboard` | Stats, active sessions/tasks |
| ActivityView | ActivityView.tsx | `activity` | Chronological activity feed |
| TasksView | TasksView.tsx | `tasks` | Task list + detail panel |
| SessionsView | SessionsView.tsx | `sessions` | Session management + output |
| WorkflowsView | WorkflowsView.tsx | `workflows` | Workflow management |
| HistoryView | HistoryView.tsx | `history` | Session replay/recordings |
| SettingsView | SettingsView.tsx | `settings` | App configuration |
| (inline) | App.tsx | `costs` | Cost tracking dashboard |
| (inline) | App.tsx | `groups` | Session groups management |

## Frontend Components (`frontend/src/components/`)

| Component | Purpose |
|-----------|---------|
| NavRail | Left navigation rail with view switching |
| SearchBar | Global search across tasks/output |
| Layout | App shell layout |
| ErrorBoundary | React error boundary |
| SessionCards | Session card grid |
| SessionRow | Single session in list |
| SessionDetail | Full session detail panel |
| SessionDetailPanel | Side panel for session info |
| SessionOutput | Session output viewer |
| SessionMiniOutput | Compact output preview |
| SessionChat | Interactive chat with session |
| SessionLauncher | Launch new session form |
| SessionGroups | Group management UI |
| SessionTemplates | Template management |
| TemplateManager | Save/load session templates |
| TaskList | Task list display |
| TaskDetail | Task detail panel |
| AddTaskForm | Create task form |
| StatCard | Dashboard stat display card |
| WorkflowCard | Workflow display card |
| WorkflowSuggestions | AI-suggested workflows |
| SavedWorkflows | Saved workflow list |
| CreateWorkflowForm | Create workflow form |
| BroadcastPanel | Send to all sessions |
| NLCommandBar | Natural language command bar |
| DiffViewer | Git diff viewer |
| GitActionsPanel | Stage/commit/push/branch UI |
| OutputViewer | Output file viewer |
| MiniOutput | Compact output display |
| NotificationCenter | Notification display |
| CostDashboard | Cost tracking charts |
| RepoGroup | Repo group card |
| RepoSearch | Search repos UI |
| WorkspacePreview | Workspace detail |
| ProjectsPanel | Project discovery panel |
| ApprovalPanel | Approval request panel |
| ImpactWarnings | Cross-session conflict warnings |
| RecentWorkspaces | Recent workspace list |
| terminal/ToolCallCard | Tool call display |
| terminal/BlockRenderers | Terminal block rendering |
| terminal/AgentTracker | Agent activity tracking |
| terminal/ActivityTimeline | Activity timeline display |

## Frontend Libs (`frontend/src/lib/`)

| File | Purpose |
|------|---------|
| hooks.ts | `useDuration` hook |
| utils.ts | General utilities |
| colors.ts | Color constants |
| theme.ts | Theme toggle (dark/light) |
| terminal-parser.ts | Parse terminal output blocks |
| terminal-utils.ts | Terminal output helpers |
| terminal-theme.ts | Terminal color theme |
| session-helpers.ts | Session utility functions |

## Import Patterns

```go
// Go backend
import "awm/internal/model"
import "awm/internal/store"
import "awm/internal/agent"
import "awm/internal/git"
import "awm/internal/api"
import "awm/internal/config"
import "awm/internal/workspace"
import "awm/internal/discovery"
import "awm/internal/scanner"
import "awm/internal/terminal"
import "awm/internal/cmux"
import "awm/internal/claude"
import "awm/internal/notify"
import "awm/internal/impact"
import "awm/internal/nlquery"
import "awm/internal/recording"
```

```typescript
// Frontend — Wails-generated bindings
import { GetTasks, CreateTask, ... } from '../wailsjs/go/main/App'
import { model } from '../wailsjs/go/models'
```
