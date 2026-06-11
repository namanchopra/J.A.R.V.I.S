# Exports Reference

## Domain Models (`internal/model/`)

| Model | File | Key Fields | Purpose |
|---|---|---|---|
| Task | task.go | ID, Name, Description, RepoPath, AgentType, Status, OutputPath, WorkflowID | Unit of work delegated to an AI agent |
| Workflow | task.go | ID, Name, Description | Groups related tasks |
| ActivityEvent | task.go | ID, TaskID, TaskName, EventType, Message, Metadata | Lifecycle event for activity feed |
| DashboardStats | task.go | Total, Running, Pending, Done, Failed, NeedsInput | Aggregate task counts |
| Session | session.go | ID, TaskID, AgentType, RepoPath, Prompt, AgentSessionID, Status, PID, OutputPath | Managed AI agent session |
| SessionGroup | group.go | ID, Name, Description, Color | Named collection of repo paths |
| GroupMember | group.go | GroupID, RepoPath, AddedAt | Repo membership in a group |
| SessionTemplate | template.go | ID, Name, AgentType, RepoPaths, Command | Reusable session configuration |
| CostSnapshot | cost.go | ID, SessionID, ProjectPath, InputTokens, OutputTokens, CostUSD | Token usage at a point in time |
| DailyCost | cost.go | Date, InputTokens, OutputTokens, CostUSD, SessionCount | Aggregated daily cost |
| TotalSpend | cost.go | AllTime, ThisMonth, Today | Cumulative cost summary |
| ApprovalRequest | approval.go | PID, SessionName, CWD, PromptText, DetectedAt | Detected approval prompt |
| CalendarEvent | gcal.go | ID, Summary, Start, End, MeetingURL | Google Calendar event (upcoming/active) |
| SpotifyTrack | spotify.go | URI, Name, Artist, Album, DurationMs | Currently-playing track metadata |
| TodoItem | todo.go | ID, Title, Done, CreatedAt | Daemon-side todo (memory feature) |

## Enums

| Enum | Values | File |
|---|---|---|
| Status (Task) | `pending`, `running`, `done`, `failed`, `needs-input` | task.go |
| SessionStatus | `launching`, `running`, `paused`, `completed`, `failed`, `needs-input` | session.go |
| AgentType | `claude-code`, `kiro`, `gemini`, `codex`, `aider`, `other` | task.go |
| OverlayMode | `compact`, `expanded`, `meeting`, `hidden` | (frontend types.ts) |
| MeetingState | `idle`, `recording`, `paused`, `processing`, `error` | (frontend types.ts) |

## State Machines

### Task Status
```
pending → running → done
   ↓        ↓
 failed   needs-input → running
   ↓
 pending (retry)
```

| From | To | Allowed |
|---|---|---|
| pending | running, done, failed, needs-input | Yes |
| running | done, failed, needs-input | Yes |
| done | pending, running | **No** |
| failed | pending, running | Yes (retry) |
| needs-input | running, done, failed | Yes |

### Session Status
```
launching → running → completed
              ↓
           needs-input → running
              ↓
           failed
```

### Meeting State
```
idle → recording → processing → idle
         ↓             ↓
       paused        error
         ↓
       recording
```

## Agent Adapter Interface (`internal/agent/adapter.go`)

| Method | Signature | Purpose |
|---|---|---|
| Name | `() AgentType` | Returns agent type |
| Launch | `(ctx, LaunchOptions) (*RunningSession, error)` | Start new session |
| SendMessage | `(ctx, *RunningSession, string) error` | Send follow-up |
| Stop | `(ctx, *RunningSession) error` | Gracefully terminate |
| IsAvailable | `() bool` | Check CLI installed |

Adapters: `internal/agent/{claude,kiro,gemini,codex,aider}.go`. Registered at startup in `main.go`.

## syscontrol Interfaces (`internal/syscontrol/`)

Cross-platform interfaces with Windows backends (`*_windows.go`). macOS implementations remain in `internal/macctl/` and use these interfaces transitively.

| Interface | File | Methods | Windows backend |
|---|---|---|---|
| AppController | appcontroller.go | OpenApp, QuitApp, FocusWindow | PowerShell Start-Process / Stop-Process + user32.SetForegroundWindow |
| AudioController | audiocontroller.go | SetVolume, Mute, Unmute | IAudioEndpointVolume COM via go-ole |
| DisplayController | displaycontroller.go | SetBrightness, GetBrightness, ToggleDND | WMI WmiSetBrightness + Focus Assist registry |
| FilesController | filescontroller.go | OpenFile, Search | explorer.exe + `search-ms:` URI |
| ClipboardController | clipboardcontroller.go | Get, Set | golang.design/x/clipboard |
| ScreenshotController | screenshotcontroller.go | Capture(mode) | Windows.Graphics.Capture / Snipping Tool |
| (free fns) | shortcuts_windows.go | ListShortcuts, RunShortcut | Scans `~/.jarvis/powershell-scripts/*.ps1` |

## Wails Bindings (`app.go` + 18 `app_*.go` partials)

~250 exported methods total. Counts by file shown in parentheses.

### Core App / AWM tasks & sessions (`app.go`, 130 methods)

#### Tasks
| Method | Signature | Purpose |
|---|---|---|
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
| GetRunningTasks | `() -> []Task` | Running/needs-input tasks |

#### Sessions
| Method | Signature | Purpose |
|---|---|---|
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

#### Workflows
| Method | Signature | Purpose |
|---|---|---|
| CreateWorkflow | `(name, desc) -> Workflow` | Create workflow |
| GetWorkflows | `() -> []Workflow` | List all workflows |
| DeleteWorkflow | `(id) -> error` | Delete workflow |
| AddTaskToWorkflow | `(taskID, workflowID) -> Task` | Link task to workflow |
| RemoveTaskFromWorkflow | `(taskID) -> Task` | Unlink task |
| GetWorkflowTasks | `(workflowID) -> []Task` | Tasks in workflow |

#### Activity & Dashboard
| Method | Signature | Purpose |
|---|---|---|
| GetDashboardStats | `() -> DashboardStats` | Aggregate task counts |
| GetActivityFeed | `(limit, beforeID) -> []ActivityEvent` | Paginated activity feed |
| GetTaskActivity | `(taskID, limit) -> []ActivityEvent` | Activity for one task |
| SearchOutput | `(query) -> []OutputSearchResult` | Grep across output files |

#### Git
| Method | Signature | Purpose |
|---|---|---|
| GetRepoInfo | `(repoPath) -> RepoInfo` | Branch, commits, diff stats |
| GetRepoDiff | `(repoPath) -> DiffResult` | Parsed unified diff |
| GetStagedDiff | `(repoPath) -> DiffResult` | Staged changes diff |
| GitStageAll / GitStageFiles | `(repoPath[, files]) -> error` | `git add` |
| GitCommit | `(repoPath, message) -> error` | `git commit -m` |
| GitPush | `(repoPath) -> error` | `git push` |
| GitCreateBranch | `(repoPath, name) -> error` | `git checkout -b` |
| OpenPRInBrowser | `(repoPath) -> error` | Open GitHub/GitLab PR URL |

#### Session Groups
| Method | Signature | Purpose |
|---|---|---|
| CreateSessionGroup / DeleteSessionGroup / ListSessionGroups | CRUD | Named repo collections |
| AddToSessionGroup / RemoveFromSessionGroup / GetSessionGroupMembers | Membership | Repo ↔ group mapping |

#### Terminal Control
| Method | Signature | Purpose |
|---|---|---|
| IsCMuxAvailable | `() -> bool` | Check CMux installed (Mac only) |
| GetCMuxWorkspaces / GetCMuxSurfaces | `() -> []` | List workspaces/surfaces |
| SendToCMux / ReadFromCMux / FocusCMuxSurface | I/O | Terminal surface control |
| GetTerminalWindows | `() -> []TerminalWindow` | All terminal windows |
| SendToTerminal / ReadFromTerminal / FocusTerminalWindow | I/O | Window control |
| GetAvailableTerminals | `() -> []string` | Available terminal types |

#### Claude Direct (live process control)
| Method | Signature | Purpose |
|---|---|---|
| GetClaudeSessions | `() -> []claude.Session` | Active Claude sessions |
| GetSessionIndicators | `() -> []SessionIndicator` | Status indicators for HUD |
| SendCommandToSession | `(pid, command) -> error` | Send via terminal |
| BroadcastCommand / BroadcastToAll | `(pids?, cmd) -> map[int]string` | Multi-session broadcast |
| FocusSession | `(pid) -> error` | Focus terminal window |
| GetSessionTerminalOutput | `(pid) -> string` | Live terminal output |
| GetPendingApprovals | `() -> []ApprovalRequest` | Approval prompts |
| RespondToApproval | `(pid, response) -> error` | Answer y/n |

#### Projects / Workspaces / Discovery / Costs / NL / Recording / Config
| Method | Signature | Purpose |
|---|---|---|
| DiscoverProjects / GetProjectSuggestions / SearchRepos | discovery | Filesystem scan |
| SaveProject / ListSavedProjects / DeleteSavedProject | persistence | Saved projects |
| CreateWorkspace / CreateWorkspaceAndLaunch / ListWorkspaces / DeleteWorkspace | workspaces | Virtual monorepos |
| SyncDotClaude / OpenWorkspaceInTerminal | workspaces | Helpers |
| GetTotalSpend / GetDailyCostSummary / GetProjectCosts / GetAllCosts | costs | Token usage |
| ScanNow / GetAutoDetectedCount | scanner | Process detection |
| WatchTaskOutput / StopWatchingOutput | streaming | Tail output files |
| ExecuteDivideAndConquer / LaunchReposInTerminal | multi-repo | Coordinated dispatch |
| SaveSessionTemplate / ListSessionTemplates / DeleteSessionTemplate / LaunchFromTemplate | templates | Reusable configs |
| SuggestWorkflows / GetImpactWarnings | analysis | Heuristics |
| GetConfig / SaveConfig | config | Settings |
| GetMobileConnectionInfo / RegenerateMobileToken | mobile | Friday pairing |
| SetDotClaudeSource | config | .claude source dir |
| ListRecordedSessions / GetSessionRecording | recording | Replay |
| ExecuteNLQuery | nlquery | Natural-language commands |

### Voice / Jarvis daemon (`app_jarvis.go`, 20 methods)
| Method | Purpose |
|---|---|
| StartJarvis / StopJarvis / RestartJarvis | Daemon process lifecycle |
| GetJarvisStatus | Running state + version |
| OpenDaemonLog | Open daemon log file in OS viewer |
| SendJarvisMessage | Push text into the daemon's conversation buffer |
| GetJarvisConversation | Recent transcript turns |
| ClearJarvisConversation | Reset context |
| SetJarvisPersonality | Switch voice persona |
| ListJarvisVoices | Available TTS presets |
| (+ 11 more orchestration helpers) | Wake-word toggling, interrupt, mute, etc. |

### System control (`app_macctl.go`, 18 methods) — Mac-prefixed names preserved on Windows
| Method | Mac backend | Windows backend |
|---|---|---|
| MacOpenApp / MacQuitApp | AppleScript | syscontrol.AppController |
| MacFocusWindow | AppleScript | syscontrol.AppController |
| MacSetVolume / MacMute / MacUnmute | AppleScript | syscontrol.AudioController |
| MacSetBrightness | `brightness` CLI | syscontrol.DisplayController |
| MacToggleDND | DNDHelper plist | syscontrol.DisplayController |
| MacOpenPath / MacSpotlight | `open` / NSWorkspace | explorer.exe / search-ms: |
| MacScreenshot | screencapture CLI | syscontrol.ScreenshotController |
| MacClipboardGet / MacClipboardSet | pbcopy/pbpaste | golang.design/x/clipboard |
| MacListShortcuts / MacRunShortcut | Shortcuts.app | PowerShell scripts in ~/.jarvis/powershell-scripts/ |
| GetMacctlPolicy / SetMacctlPolicy | policy gate | shared |

### Spotify (`app_spotify.go`, 13 methods)
9 tool methods (Search/PlayURI/Pause/Resume/Skip/Previous/SeekToPosition/SetVolume/AddToQueue) + OAuth helpers. On Windows all 9 route through `internal/spotify/web.go`; Mac uses `applescript.go` for sub-100ms latency.

### Google Calendar (`app_gcal.go`, 11 methods)
GoogleCalendarSignIn / SignOut / GetUpcomingEvents / GetCurrentEvent / RefreshNow / GetCredentialsStatus / GetSyncStatus / + OAuth callback handlers. Backed by `internal/gcal/`.

### Meeting mode (`app_meeting.go`, 10 methods)
StartMeeting / StopMeeting / PauseMeeting / ResumeMeeting / GetMeetingState / ListMeetings / OpenMeetingNotes / DeleteMeeting / + 2 helpers. Uses `internal/screencapture/` for system audio.

### Setup orchestration (`app_setup.go`, 14 methods)
IsSetupComplete / RunSetup / RerunSetup / GetSetupProgress / + parser internals. Spawns `scripts/setup/install-daemon.{sh,ps1}` and streams `PHASE:` events to frontend.

### Hotkeys (`app_hotkey.go`, 8 methods)
RebindOverlayHotkey / RebindPTTHotkey / OverlayPTTPress / OverlayPTTRelease / GetHotkeyStatus / + internal callbacks.

### Overlay (`app_overlay.go`, 3 methods)
OverlayShow / OverlayHide / SetOverlayMode.

### Diagnostics (`app_diagnostics.go`, 6 methods)
DumpConfig / DumpLogs / RunDoctor / PingDaemon / ListAudioDevices / + helpers.

### Permissions (`app_permissions.go`, 2 methods)
GetPermissionStatus / OpenPermissionSettings (deep-links: TCC pane on Mac, `ms-settings:privacy-microphone` on Win).

### Pairing (`app_pairing.go`, 1 method)
GeneratePairingQR — produces the `jarvis://pair?host=...&token=...&room=jarvis` deep link encoded as a QR.

### Update check (`app_update_check.go`, 2 methods)
CheckForUpdate / GetLatestVersion — polls GitHub Releases.

### Other partials
| File | Methods |
|---|---|
| app_config_io.go | 4 — Export/Import config, validators |
| app_settings_apply.go | 2 — ApplySettings, ResetSettings |
| app_onboarding.go | 2 — IsFirstLaunch, MarkOnboardingComplete |
| app_voice.go | 2 — GetAudioInputDevices (cross-platform), SetMicDevice |
| app_voice_{darwin,windows,other}.go | platform-specific helper (not Wails-bound directly; called from app_voice.go) |
| app_shortcuts_installer.go | 1 — InstallDefaultShortcuts |
| app_push.go | 1 — SendTestPush (Expo push token validation) |
| app_validators.go | 2 — validation helpers (not all exported) |
| app_dialogs.go | dialog helpers |

## Store Methods (`internal/store/sqlite.go`)

| Method group | Methods |
|---|---|
| Tasks | CreateTask, GetTask, ListTasks, UpdateTask, DeleteTask |
| Workflows | CreateWorkflow, GetWorkflow, ListWorkflows, UpdateWorkflow, DeleteWorkflow, GetWorkflowTasks |
| Activity | CreateActivityEvent, ListActivityEvents, ListTaskActivityEvents, GetDashboardStats |
| Output search | SearchOutput |
| Sessions | CreateSession, GetSession, ListSessions, UpdateSession, DeleteSession, GetActiveSessions |
| Projects | CreateProject, ListProjects, DeleteProject, GetProjectRepos, SetProjectRepos |
| Groups | CreateSessionGroup, ListSessionGroups, DeleteSessionGroup, AddToGroup, RemoveFromGroup, GetGroupMembers |
| Templates | CreateSessionTemplate, ListSessionTemplates, GetSessionTemplate, DeleteSessionTemplate |
| Costs | InsertCostSnapshot, GetCostsBySession, GetCostsByProject |
| Misc | CountAutoDetected |

## Mobile API Routes Summary (`internal/api/`)

See `architecture.md` for the full route table. Handler files:

| File | Routes |
|---|---|
| handlers_dashboard.go | /ping, /dashboard, /activity, /indicators, /tasks/:id |
| handlers_sessions.go | /sessions, /sessions/:id, /sessions/:id/stop |
| handlers_workspaces.go | /workspaces, /workspaces/:id |
| handlers_repos.go | /saved-projects |
| handlers_approvals.go | /approvals, /approvals/:pid/respond |
| handlers_settings.go | /settings, /push-token |
| handlers_calendar.go | /calendar/* |
| handlers_jarvis_chat.go | /jarvis/chat |
| handlers_jarvis_ws.go | /ws/sessions/:id/output |
| handlers_jarvis_mobile_ws.go | /ws/jarvis/mobile |
| handlers_livekit.go | /livekit/token |

## Frontend Views (`frontend/src/views/`)

| View | File | ViewId | Purpose |
|---|---|---|---|
| OverlayView | OverlayView.tsx | `overlay` | Frameless HUD orb |
| SettingsView | SettingsView.tsx | `settings` | Tabbed settings shell |

The AWM dashboard/tasks/sessions/workflows UIs were removed from desktop; the **mobile Friday app** at `mobile/app/` is the AWM viewer over the mobile API.

## Frontend Settings Panels (`frontend/src/views/settings/`)

| Panel | File | Tab key |
|---|---|---|
| BehaviorPanel | BehaviorPanel.tsx | `behavior` |
| VoicePanel | VoicePanel.tsx | `voice` |
| OverlayPanel | OverlayPanel.tsx | `overlay` |
| PermissionsPanel | PermissionsPanel.tsx | `permissions` |
| MeetingPanel | MeetingPanel.tsx | `meeting` |
| ConnectionsPanel | ConnectionsPanel.tsx | `connections` |
| DiagnosticsPanel | DiagnosticsPanel.tsx | `diagnostics` |
| AdvancedPanel | AdvancedPanel.tsx | `advanced` |
| FridayPairingModal | FridayPairingModal.tsx | (modal) |
| SettingsTabs | SettingsTabs.tsx | (chrome) |

Shared: `types.ts`, `hotkey-spec.ts`. Every panel ships a `.test.tsx` companion.

## Frontend Components

| File | Purpose |
|---|---|
| components/setup/SetupScreen.tsx | First-launch 4-phase progress UI |
| components/setup/SetupScreen.test.tsx | Vitest coverage |
| (+ other small components) | See `ls frontend/src/components/` |

## Frontend Libs (`frontend/src/lib/`)

| File | Purpose |
|---|---|
| use-setup-state.ts | Subscribes to setup events, exposes phase + progress state |
| use-setup-state.test.ts | Vitest coverage |

## Mobile App (`mobile/app/`, Expo Router)

| Route | File | Purpose |
|---|---|---|
| / | index.tsx | Friday home (orb + PTT) |
| /settings | settings.tsx | Connection settings |
| /pair | pair.tsx | QR-scan pairing |
| _layout.tsx | (root layout) | Auth gate + stack nav |

## Website (`website/app/`, Next.js)

| Route | File | Purpose |
|---|---|---|
| / | page.tsx | Single landing page (UA-sniff for platform CTA, Windows section, Friday QR) |

## Import Patterns

```go
// Go backend
import "github.com/namanchopra/jarvis/internal/model"
import "github.com/namanchopra/jarvis/internal/store"
import "github.com/namanchopra/jarvis/internal/agent"
import "github.com/namanchopra/jarvis/internal/syscontrol"
import "github.com/namanchopra/jarvis/internal/jarvis"
import "github.com/namanchopra/jarvis/internal/gcal"
import "github.com/namanchopra/jarvis/internal/spotify"
import "github.com/namanchopra/jarvis/internal/screencapture"
import "github.com/namanchopra/jarvis/internal/paths"
// ... (see go.mod for module path)
```

```typescript
// Frontend — Wails-generated bindings
import { StartJarvis, MacOpenApp, GoogleCalendarSignIn, StartMeeting } from '../wailsjs/go/main/App'
import { model } from '../wailsjs/go/models'
import { EventsOn, EventsEmit } from '../wailsjs/runtime/runtime'
```
