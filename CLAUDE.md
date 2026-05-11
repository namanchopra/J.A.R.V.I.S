# Jarvis - AI Voice Companion

Wails v2 desktop app (Go 1.25 + React 18/Vite/Tailwind) for managing AI coding agent sessions across multiple projects. Binary name: `jarvis`. App name: **Jarvis**.

## Project Documentation

@.claude/claude-md-refs/architecture.md
@.claude/claude-md-refs/development-guide.md
@.claude/claude-md-refs/exports-reference.md

## Quick Documentation Reference

| Need Help With | See File |
|----------------|----------|
| Adding features, endpoints, views | development-guide.md |
| Understanding system structure, flows | architecture.md |
| Finding models, methods, components | exports-reference.md |

## Key Conventions

- **App struct** (`app.go`): Every exported method is a Wails binding. ~90 methods, ~2600 lines.
- **Error wrapping**: Always prefix errors with method name: `fmt.Errorf("MethodName: %w", err)`
- **Nil slices**: Return `[]T{}` not `nil` (Wails serializes nil as JSON `null`)
- **Input validation**: Validate and trim at the top of each binding method
- **Store pattern**: Dynamic `map[string]interface{}` for partial updates
- **Models**: All have `json:` tags for Wails/JSON serialization
- **Migrations**: Append-only to `internal/store/migrations.go`, never modify existing
- **Agent adapters**: Implement `AgentAdapter` interface in `internal/agent/`
- **Mobile API**: Provider interfaces decouple handlers from App, all satisfied by App struct
- **Frontend**: Views in `src/views/`, components in `src/components/`, call `wailsjs/go/main/App`

## Build & Run

```bash
wails dev          # Development with hot reload
wails build        # Production binary
go test ./...      # Backend tests
cd frontend && npm run test  # Frontend tests
```

## Data Locations

| Path | Purpose |
|------|---------|
| `~/.jarvis/awm.db` | SQLite database |
| `~/.jarvis/config.json` | App settings |
| `~/.jarvis/logs/` | Session output logs |
| `~/.jarvis/workspaces/` | Virtual monorepo workspaces |
