# Jarvis — AI Voice Companion

Wails v2 desktop app (Go 1.25 + React 18 / Vite / Tailwind) — a voice-driven orchestrator for AI coding agents with a frameless HUD overlay, system control, meeting mode, Google Calendar, Spotify, and a phone companion (Friday). Runs on **macOS (Apple Silicon)** and **Windows 10/11 (x64 + arm64)**. Binary name: `jarvis`. App name: **Jarvis**.

## Project Documentation

@.claude/claude-md-refs/architecture.md
@.claude/claude-md-refs/development-guide.md
@.claude/claude-md-refs/exports-reference.md

## Quick Documentation Reference

| Need Help With | See File |
|---|---|
| Adding features, endpoints, panels, syscontrol tools | development-guide.md |
| Understanding system structure, request flows, packages | architecture.md |
| Finding models, Wails bindings, mobile API routes, views | exports-reference.md |

## Key Conventions

- **App struct = Wails binding surface.** ~250 exported methods across `app.go` (130) + 18 `app_*.go` partials. Every exported method on `App` is auto-exposed to React.
- **Error wrapping:** Always prefix errors with the method name: `fmt.Errorf("MethodName: %w", err)`.
- **Nil slices:** Return `[]T{}` not `nil` (Wails serializes nil as JSON `null`).
- **Input validation:** Validate and trim at the top of each binding method.
- **Models:** All have `json:` tags for Wails/JSON serialization.
- **Migrations:** Append-only to `internal/store/migrations.go`, never modify existing. Currently at v11.
- **Agent adapters:** Implement the `AgentAdapter` interface in `internal/agent/` and register in `main.go`.
- **Mobile API:** Provider interfaces decouple handlers from App; all satisfied by the App struct. See `internal/api/handlers_*.go`.
- **Frontend:** Views in `src/views/` (OverlayView + SettingsView), settings panels in `src/views/settings/`, components in `src/components/`. Call `wailsjs/go/main/App` for bindings.
- **Platform-split files:** Build tags `//go:build darwin|windows|!darwin`. Pair `_darwin.go` and `_windows.go` with an `_other.go` fallback. CGO Windows files also need a `_nocgo.go` companion for Mac→Win cross-compile sanity (see `internal/screencapture/`).
- **syscontrol pattern:** New cross-platform system-control work goes in `internal/syscontrol/` (interfaces) with Windows backends. macOS implementations stay in `internal/macctl/` and use the interfaces.
- **macOS build stays green:** Anything new must `go build ./...` clean on `darwin/arm64`. Use build tags to isolate Windows-only code.

## Build & Run

```bash
wails dev                            # Dev (hot reload)
wails build                          # macOS arm64 binary
wails build -platform windows/amd64  # Windows x64 binary
wails build -platform windows/arm64  # Windows arm64 binary
go test ./...                        # Backend tests
cd frontend && npm run test          # Frontend tests (Vitest)
```

## Data Locations

| Path (macOS) | Path (Windows) | Purpose |
|---|---|---|
| `~/.jarvis/awm.db` | `%USERPROFILE%\.jarvis\awm.db` | SQLite database (WAL) |
| `~/.jarvis/config.json` | `%USERPROFILE%\.jarvis\config.json` | App settings |
| `~/.jarvis/logs/` | `%USERPROFILE%\.jarvis\logs\` | Session + daemon logs |
| `~/.jarvis/workspaces/` | `%USERPROFILE%\.jarvis\workspaces\` | Virtual monorepo workspaces (symlinks / junctions) |
| `~/.jarvis/meetings/` | `%USERPROFILE%\.jarvis\meetings\` | Meeting transcripts (Markdown) |
| `~/.jarvis/powershell-scripts/` (Win only) | `%USERPROFILE%\.jarvis\powershell-scripts\` | User PowerShell scripts (Shortcuts.app substitute) |
| `~/.cache/huggingface/` | `%USERPROFILE%\.cache\huggingface\` | VibeVoice + Whisper model weights |

## Sub-projects

- `./` — Wails desktop app (primary)
- `scripts/jarvis-daemon/` — Python Pipecat sidecar (voice loop, meeting transcripts)
- `mobile/` — Expo phone companion (Friday)
- `website/` — Next.js landing page (jarvis.namanchopra.com)
