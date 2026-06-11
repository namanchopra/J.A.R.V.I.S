# Architecture

## System Overview

Jarvis is a native voice companion for AI coding agents. Built with **Wails v2** (Go backend + React frontend in a native WebView), it runs on **macOS (Apple Silicon)** and **Windows (10/11, x64+arm64)**. The desktop UI is intentionally minimal: a frameless HUD overlay + Settings. The heavy lifting happens in a Python sidecar daemon (Pipecat) for STT/TTS/LLM, and a sprawling Go backend that orchestrates voice, AI coding agents, system control, meeting capture, and a mobile companion API.

## Sub-projects in this repo

| Path | Stack | Purpose |
|---|---|---|
| `./` (root) | Wails v2 (Go 1.25 + React 18) | Primary desktop app — binary name `jarvis` |
| `scripts/jarvis-daemon/` | Python 3.13 + Pipecat | Voice pipeline (STT/TTS/LLM/wake-word). Runs as a sidecar process; communicates with Go over WebSocket on localhost. |
| `mobile/` | Expo (React Native) | "Friday" phone companion — push-to-talk over WebSocket to the desktop's mobile API |
| `website/` | Next.js 15 (App Router) | Landing page at jarvis.namanchopra.com; auto-detects platform for download CTA |

## Platform Architecture (cross-platform via build tags)

Platform-specific Go code uses `//go:build <os>` tags. The pattern is consistent across 10 packages:

| Package | `_darwin.go` | `_windows.go` | `_other.go` |
|---|---|---|---|
| `internal/arch/` | (in main `arch.go`) | (in main) | — |
| `internal/paths/` | `paths_darwin.go` | `paths_windows.go` | (cross-platform fallback in `paths.go`) |
| `internal/notify/` | AppleScript via `osascript` | WinRT toast via `go-toast/v2` | no-op |
| `internal/permissions/` | TCC plist check | `CapabilityAccessManager` registry | no-op |
| `internal/screencapture/` | ScreenCaptureKit CGO (`.m` + `.go`) | WASAPI loopback CGO (`.c` + `.go`) + nocgo stub | no-op |
| `internal/macctl/overlay_chrome` | NSWindow CGO | Wails native API | no-op |
| `internal/workspace/` | POSIX symlinks | NTFS junctions (`mklink /J`) | (symlinks via `_other.go`) |
| `internal/cmux/` | `Setsid` SysProcAttr | no-op (cmux is macOS-only feature) | (in `_unix.go`) |
| `internal/syscontrol/` | (interfaces only; macOS backend still in `internal/macctl/`) | full Win backend (7 files) | — |
| `app_voice.go` | CoreAudio mic enumeration | IMMDeviceEnumerator COM | empty stub |

## Dependency Graph

```
main.go
  |
  +-- app.go (Wails-bound App struct + 18 app_*.go partial files = ~250 exported methods)
  |     |
  |     +-- internal/jarvis/      Jarvis "personality" + conversation + commands + claude bridge
  |     |     +-- audio/           Wake-word detection, fast STT, TTS, capture
  |     |
  |     +-- internal/setup/        First-launch installer orchestration (parses PHASE: events from install-daemon.{sh,ps1})
  |     +-- internal/arch/         Architecture guard (Apple Silicon on Mac, x64/arm64 on Windows)
  |     +-- internal/paths/        Platform-aware path resolution (~/.jarvis vs %USERPROFILE%\.jarvis)
  |     +-- internal/hotkey/       Global hotkeys (golang.design/x/hotkey) — Alt+Space, Ctrl+Space
  |     +-- internal/permissions/  Mic permission status (TCC on Mac, CapabilityAccessManager on Win)
  |     +-- internal/screencapture/ Meeting-mode audio capture (ScreenCaptureKit / WASAPI loopback)
  |     +-- internal/syscontrol/   System control interfaces (App/Audio/Display/Files/Clipboard/Screenshot/Shortcuts) — Windows backends
  |     +-- internal/macctl/       Mac system control via AppleScript (15 tools)
  |     +-- internal/gcal/         Google Calendar OAuth + events client
  |     +-- internal/spotify/      Spotify control — AppleScript (Mac) + Web API (cross-platform)
  |     +-- internal/notify/       Toast/banner notifications (platform-split)
  |     +-- internal/store/        SQLite persistence (~/.jarvis/awm.db, 11 migrations, WAL)
  |     +-- internal/agent/        AI agent session adapters (Claude/Kiro/Gemini/Codex/Aider)
  |     +-- internal/scanner/      Process auto-detection (gopsutil, 5s scan)
  |     +-- internal/terminal/     Terminal control (CMux/iTerm2/Terminal.app/Windows Terminal+ConPTY)
  |     +-- internal/cmux/         CMux socket RPC client (macOS-only feature)
  |     +-- internal/git/          Git operations (info, diff, stage, commit, push)
  |     +-- internal/workspace/    Virtual monorepo workspaces (symlinks / junctions)
  |     +-- internal/discovery/    Project filesystem discovery
  |     +-- internal/config/       Config file (~/.jarvis/config.json)
  |     +-- internal/claude/       Claude Code session cost reader (JSONL parsing)
  |     +-- internal/impact/       Cross-session conflict detection
  |     +-- internal/nlquery/      Natural-language command engine (pure keyword matching)
  |     +-- internal/recording/    Session recording/replay
  |     +-- internal/api/          Echo HTTP server (mobile API) + WebSocket handlers
  |     +-- internal/watcher/      File tail watcher
  |     +-- internal/ci/           CI pipeline watcher (GitHub Actions / GitLab)
  |     +-- internal/proc/         Process alive check
  |
  +-- internal/cli/                Cobra CLI (legacy AWM commands: add, list, update, remove, open)
  |
  +-- scripts/jarvis-daemon/       Python Pipecat sidecar (LLM, TTS, STT, wake-word, meeting transcripts)
  +-- scripts/setup/               install-daemon.{sh,ps1} — first-launch installer
  +-- installer/                   Inno Setup script (Windows distribution)
  +-- build/scripts/               Build orchestration (fetch-python, fetch-uv, fetch-portaudio, post-build)

frontend/
  +-- src/views/OverlayView.tsx       The frameless HUD orb (primary UI)
  +-- src/views/SettingsView.tsx      Settings shell + tab routing
  +-- src/views/settings/             7 settings panels (Behavior, Voice, Connections, Diagnostics, Meeting, Overlay, Permissions, Advanced, FridayPairingModal)
  +-- src/components/setup/SetupScreen.tsx  First-launch progress UI
  +-- src/lib/                        Hooks (use-setup-state) + utilities
  +-- wailsjs/                        Auto-generated Go bindings
```

## Request Lifecycle

### Desktop (Wails — React → Go)
```
User action in OverlayView/SettingsView
  → Wails JS binding (e.g. StartMeeting, MacOpenApp, GoogleCalendarSignIn)
  → exported method on App struct (in app.go or app_*.go)
  → internal package (jarvis, syscontrol, macctl, gcal, spotify, store, ...)
  → Return value serialized as JSON, or runtime.EventsEmit for streaming events
```

### Daemon ↔ Go (Voice loop)
```
Python daemon (scripts/jarvis-daemon/main.py)
  ↔ WebSocket on localhost (port managed by daemon)
  ↔ internal/jarvis/ orchestrator on Go side
  ↔ runtime.EventsEmit("jarvis", payload) → OverlayView updates
```

### Mobile API (Friday phone → Desktop)
```
Friday HTTP request → port 4422 (config: mobileAPIPort)
  → Bearer-token auth middleware (internal/api/server.go)
  → Echo route handler (internal/api/handlers_*.go)
  → Provider interface method (satisfied by App struct)
  → Same internal packages
  → JSON response
```

### Mobile WebSocket (Voice relay)
```
Friday WebSocket → /ws/jarvis/mobile?token=<bearer>
  → handlers_jarvis_mobile_ws.go upgrades + auths
  → Bidirectional audio frames between phone mic and desktop Pipecat daemon
  → Phone is mic + speaker; desktop is the brain
```

## Mobile API Routes (Echo HTTP on port 4422)

Defined across `internal/api/handlers_*.go`. All routes require `Authorization: Bearer <token>` (HTTP) or `?token=<token>` (WebSocket).

| Method | Path | Handler | Purpose |
|---|---|---|---|
| GET | /ping | handlePing | Health check |
| GET | /dashboard | handlers_dashboard | Aggregate stats + active sessions/tasks |
| GET | /activity | handlers_dashboard | Paginated activity feed |
| GET | /indicators | handlers_dashboard | Claude session indicators |
| GET | /tasks/:id | handlers_dashboard | Single task by ID |
| GET | /sessions | handlers_sessions list | List sessions (`?status=`) |
| GET | /sessions/:id | handlers_sessions get | Single session by ID |
| POST | /sessions/:id/stop | handlers_sessions stop | Stop running session |
| GET | /workspaces | handlers_workspaces list | Virtual monorepo workspaces |
| DELETE | /workspaces/:id | handlers_workspaces delete | Delete workspace |
| GET | /saved-projects | handlers_repos | Saved projects |
| GET | /approvals | handlers_approvals list | Pending approval prompts |
| POST | /approvals/:pid/respond | handlers_approvals respond | Answer y/n |
| GET | /settings | handlers_settings get | Current config (safe subset) |
| PUT | /settings | handlers_settings put | Partial config update |
| POST | /push-token | handlers_settings registerToken | Register Expo push token |
| GET | /calendar/events | handlers_calendar | Upcoming Google Calendar events |
| POST | /calendar/auth/start | handlers_calendar | Begin OAuth flow |
| POST | /calendar/auth/callback | handlers_calendar | OAuth code exchange |
| GET | /jarvis/chat | handlers_jarvis_chat | Conversation history |
| POST | /jarvis/chat | handlers_jarvis_chat | Send text message → Jarvis response |
| GET | /livekit/token | handlers_livekit | Mint LiveKit room token (experimental voice transport) |
| GET | /ws/sessions/:id/output | handlers_jarvis_ws | Stream terminal output (WebSocket) |
| GET | /ws/jarvis/mobile | handlers_jarvis_mobile_ws | Bidirectional voice WebSocket for Friday |

## CLI Commands

The Cobra CLI under `internal/cli/` is the legacy AWM (AI Workflow Manager) surface. The binary `jarvis` (or `awm` on older installs) supports:

| Command | Purpose |
|---|---|
| `jarvis` (no args) | Launch desktop window |
| `jarvis add` | Create a task |
| `jarvis list` | List tasks (with filters) |
| `jarvis update` | Update task fields |
| `jarvis remove` | Delete a task |
| `jarvis open` | Explicitly launch desktop window |
| `jarvis --version` | Print version |

Output: `--output table` (default) or `--output json`.

## Database Schema (SQLite)

Location: `~/.jarvis/awm.db` (macOS) or `%USERPROFILE%\.jarvis\awm.db` (Windows). Journal: WAL. Max connections: 1.

11 migrations in `internal/store/migrations.go`. Append-only — never modify existing entries.

| Table | Migration | Purpose |
|---|---|---|
| tasks | v1 | Core task records |
| workflows | v2 | Workflow groupings |
| activity_events | v3 | Activity feed events |
| sessions | v4 | Agent session records |
| projects | v5 | Saved project metadata |
| project_repos | v5 | Project ↔ repo mappings |
| session_groups | v6 | Named repo collections |
| session_group_members | v6 | Group membership (CASCADE) |
| session_templates | v6 | Reusable session configs |
| cost_snapshots | v6 | Token usage tracking |
| (+ later migrations) | v7–v11 | (see migrations.go for full list — calendar tokens, push tokens, conversation history, etc.) |
| schema_version | auto | Migration version tracker |

## Agent Adapter Pattern

```
AgentAdapter (interface in internal/agent/adapter.go)
  +-- ClaudeAdapter    claude --print --output-format stream-json
  +-- KiroAdapter      kiro-cli
  +-- GeminiAdapter    gemini
  +-- CodexAdapter     codex
  +-- AiderAdapter     aider
```

Each adapter implements: `Name()`, `Launch()`, `SendMessage()`, `Stop()`, `IsAvailable()`. The `SessionManager` in `internal/agent/manager.go` registers adapters at startup, launches via the adapter, streams output to log files + Wails events, detects "needs input" via heuristic text matching, sends notifications on completion/failure, and recovers sessions on restart.

## Terminal Control

```
TerminalManager (internal/terminal/manager.go)
  +-- CMuxProvider               Direct socket RPC (password auth) — macOS only
  +-- ITerm2Provider             AppleScript automation — macOS only
  +-- MacOSTermProvider          AppleScript automation — macOS only
  +-- WindowsTerminalProvider    wt.exe + ConPTY — Windows only
```

Focus/send/read operations route through the preferred terminal provider (`config.preferredTerminal`).

## Voice Pipeline (Pipecat daemon)

```
Mic capture (mic.py via PyAudio + portaudio)
  → Wake-word detection (jarvis_daemon/wakeword via openWakeWord)
  → Pipecat pipeline:
       STT (MLX Whisper on Mac, faster-whisper on Win)
       LLM (OpenRouter cloud or Ollama local — picked by llm_picker.py)
       TTS (VibeVoice on Mac+CUDA, macos_say or kokoro fallback)
  → Audio out → speaker
  → runtime.EventsEmit("jarvis", { type: "transcript" | "speaking" | "idle", text, ... })
  → OverlayView orb animates + transcript banner
```

The Go side embeds the daemon process lifecycle in `app_jarvis.go` (StartJarvis/StopJarvis/RestartJarvis) and `internal/jarvis/`. Audio frame routing uses `internal/jarvis/audio/{capture,fast_stt,tts,wakeword}.go` for the Go-side wake-word + fast-path interrupt detection.

## Meeting Mode (System audio capture)

```
StartMeeting (app_meeting.go)
  → internal/screencapture/New() → platform-tagged Capturer
       Mac:    ScreenCaptureKit (requires Screen Recording permission)
       Win:    WASAPI loopback (no permission required)
  → 16kHz mono int16 frames piped to daemon WebSocket
  → Pipecat transcribes mic + system audio with [mic]/[system] tags
  → On Stop: writes Markdown to ~/.jarvis/meetings/<unix>.md with summary + action items
  → TTS speaks 2-sentence recap
```

Calendar integration (`app_gcal.go` + `internal/gcal/`) polls Google Calendar; matching events (keywords: "sync", "1:1", etc.) trigger an auto-suggest banner in OverlayView 2 minutes before start.

## Overlay + Hotkeys

```
internal/hotkey/ (golang.design/x/hotkey)
  + parse_aliases_{darwin,windows,other}.go      Platform-specific key labels (⌥+Space vs Alt+Space)

app_hotkey.go bindings:
  RebindOverlayHotkey      Change toggle hotkey
  OverlayPTTPress          Begin push-to-talk capture
  OverlayPTTRelease        End PTT → send to daemon

internal/macctl/overlay_chrome_{darwin,windows,other}.go
  Frameless 320×420 always-on-top panel.
  Mac:  NSWindow level + collection behavior via CGO ObjC
  Win:  Wails native WindowSetTitleBarStyle / SetAlwaysOnTop
```

## Setup Flow (First Launch)

```
SetupScreen.tsx (frontend) ← Wails events ← app_setup.go ← internal/setup/setup.go
  ↓
Spawns scripts/setup/install-daemon.{sh,ps1} (Mac/Win)
  ↓
Phases emitted as PHASE:/PHASE_PROGRESS:/PHASE_DONE:/PHASE_ERROR: on stderr
  1. python_install        Unpack bundled python-build-standalone
  2. venv_install          uv pip install daemon requirements
  3. vibevoice_download    HF download to ~/.cache/huggingface/
  4. whisper_download      HF download (MLX on Mac, faster-whisper on Win)
  ↓
Writes ~/.jarvis/.setup-version-<v> sentinel
  ↓
App proceeds to main UI
```

On Windows the parser tolerates `\r\n` line endings (app_setup.go normalizes before pattern-matching).

## Configuration (`~/.jarvis/config.json`)

Loaded/saved via `internal/config/`. Keys include:

| Key | Default | Purpose |
|---|---|---|
| defaultAgent | `"claude-code"` | Default agent for new sessions |
| scanIntervalSeconds | `5` | Process scan interval |
| preferredTerminal | `""` (auto) | Terminal provider override |
| projectRootPaths | `null` | Directories to scan for repos |
| notificationsEnabled | `true` | Toast/banner toggle |
| notifyOnApproval | `true` | Notify on approval prompts |
| notifyOnCompletion | `true` | Notify on session complete |
| ciWatchEnabled | `false` | CI pipeline monitoring |
| ciProvider | `""` | `"github-actions"` or `"gitlab-ci"` |
| defaultCommand | `"claude"` | CLI command for sessions |
| mobileAPIPort | `4422` | HTTP server port |
| mobileAPIToken | (auto-generated) | Bearer token |
| dotClaudeSourcePath | `""` | .claude source for workspaces |
| overlayHotkey | `"alt+space"` | Overlay toggle hotkey |
| pttHotkey | `"ctrl+space"` | Push-to-talk hotkey |
| sttComputeDevice | `"cpu"` | Windows: `"cpu"` or `"cuda"` for faster-whisper |
| ttsComputeDevice | `"auto"` | Mac: mps; Win: cuda or cpu |
| spotifyEnabled | `false` | Spotify control toggle |
| googleCalendarEnabled | `false` | Calendar integration toggle |
| meetingDaemonRestartHours | `24` | Auto-restart daemon to mitigate WASAPI long-run memory growth (Win) |

## Frontend View Map

The desktop UI is intentionally minimal — the heavy interactions happen via voice/overlay. The full AWM task/workflow/session UI is exposed via the **mobile** Friday companion (over the mobile API), not the desktop.

| ViewId | Component | File | Purpose |
|---|---|---|---|
| `overlay` | OverlayView | `views/OverlayView.tsx` | Frameless HUD: orb, transcript banner, meeting banner, PTT, controls |
| `settings` | SettingsView | `views/SettingsView.tsx` | Tabbed settings shell |

### Settings Panels (under `views/settings/`)

| Panel | File | Purpose |
|---|---|---|
| Behavior | `BehaviorPanel.tsx` | Default agent, notifications, scan interval |
| Voice | `VoicePanel.tsx` | STT/TTS compute device, voice preset, wake-word sensitivity |
| Overlay | `OverlayPanel.tsx` | Hotkey rebinding, position, visibility settings |
| Permissions | `PermissionsPanel.tsx` | Mic + Screen Recording (Mac) deep links — `ms-settings:` on Windows |
| Meeting | `MeetingPanel.tsx` | Calendar integration toggle, meeting keywords, output dir |
| Connections | `ConnectionsPanel.tsx` | Google Calendar OAuth, Spotify OAuth, Friday pairing, mobile token |
| Diagnostics | `DiagnosticsPanel.tsx` | Daemon logs, re-run setup, regenerate token, dump config |
| Advanced | `AdvancedPanel.tsx` | Power-user toggles, experimental flags |
| FridayPairingModal | `FridayPairingModal.tsx` | QR-code generator for phone pairing |

## Wails Events (Go → Frontend)

Streamed via `runtime.EventsEmit(ctx, name, payload)`. Listeners in `OverlayView.tsx` and panels.

| Event | Payload | Purpose |
|---|---|---|
| `jarvis` | `{type, text, role, ts, ...}` | Streamed daemon updates (transcript, speaking state, tool call) |
| `hud` | `{state, ...}` | HUD orb state transitions (idle/listening/thinking/speaking) |
| `overlay` | `{visible, ...}` | Overlay visibility toggle |
| `overlay:mode` | `string` | Overlay morphs (compact / expanded / meeting) |
| `overlay:hotkey_error` | `string` | Hotkey registration failure (e.g. conflict with another app) |
| `meeting:state` | `{active, recording, ...}` | Meeting capture lifecycle |
| `meeting:permission_error` | `string` | Mic/Screen Recording denied |
| `navigate` | `string` (viewId) | Programmatic view switch (e.g. Settings → Diagnostics) |
| `text` | `string` | Banner text (e.g. setup phase labels) |
| `type` | `string` | Banner type discriminator |
| `workflow_progress` | `{phase, progress, ...}` | Setup or long-running operation progress |

## Key Subsystems

### Process Scanner (`internal/scanner/`)
gopsutil-based, scans every 5s. Matches processes to known agent types (claude, kiro, gemini, codex, aider) via name + cmdline patterns. Filters out Electron helpers, IDE extensions, non-git processes. Auto-creates tasks for detected agents, marks tasks done when processes exit.

### Workspace System (`internal/workspace/`)
Virtual monorepos at `~/.jarvis/workspaces/<name>/`. Symlinks on macOS, junctions on Windows. Auto-generates CLAUDE.md with task description + repo metadata + cross-repo guidelines. Copies `.claude/` from dotClaudeSourcePath. Launches sessions with `--add-dir` flags. Cross-platform via `workspace_windows.go` + `workspace_other.go`.

### Impact Detection (`internal/impact/`)
Compares `git diff --name-only HEAD` across active sessions. Detects 3 conflict types: shared-dependency (package.json/go.mod), shared-file (shared/ or common/ dirs), API contract (/api/ or /routes/ dirs).

### Project Discovery (`internal/discovery/`)
Scans configured root paths 2 levels deep for git repos. Groups by parent dir into Projects. Detects language, branch, active agent sessions. Suggests coordinated tasks based on repo relationships.

### Cost Tracking (`internal/claude/`)
Reads Claude Code session cost data from `~/.claude/projects/*/sessions/*/` JSONL files. Aggregates by session, project, day. Snapshots in `cost_snapshots` table.

### NL Query Engine (`internal/nlquery/`)
Pure keyword-matching (no LLM). Supports: show idle/active/running sessions, total cost/spend, stop/kill session, broadcast command, count sessions, history/recordings. Returns structured `QueryResult`.

### Spotify (`internal/spotify/`)
- `applescript.go` (Mac only, `//go:build darwin`) — local driver for sub-100ms control
- `web.go` + `oauth.go` — cross-platform Web API (Windows uses this exclusively)
- `store.go` — token persistence

### Google Calendar (`internal/gcal/`)
- `oauth.go` — OAuth flow, refresh-token handling
- `client.go` — events fetch with caching
- `store.go` — token + cache persistence

### Jarvis Brain (`internal/jarvis/`)
- `jarvis.go` — orchestrator entry point
- `conversation.go` — multi-turn context management
- `personality.go` + `sentences.go` — voice persona configuration
- `commands.go` — natural-language command parsing → tool dispatch
- `analysis.go` — semantic intent classification
- `monitor.go` — daemon health monitoring
- `claude.go` — bridges to internal/claude/ for Claude Code session data
- `context.go` — assembles LLM context (repos, sessions, calendar, recent activity)
- `audio/{capture,fast_stt,tts,wakeword}.go` — Go-side audio fast path (wake-word + interrupt detection runs in-process for sub-100ms response)
