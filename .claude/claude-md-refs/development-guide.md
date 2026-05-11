# Development Guide

## Tech Stack

- **Backend:** Go 1.25, Wails v2, Cobra CLI, Echo HTTP, modernc.org/sqlite, gopsutil, fsnotify
- **Frontend:** React 18, Vite 8, Tailwind CSS 4, TypeScript 6, framer-motion
- **Build:** `wails dev` (dev), `wails build` (prod), `npm run test` (frontend tests)
- **Data:** SQLite at `~/.jarvis/awm.db`, config at `~/.jarvis/config.json`, logs at `~/.jarvis/logs/`

## Adding a New Wails Binding

Every exported method on `App` in `app.go` is auto-exposed to the frontend.

### Step 1: Add the method to `app.go`

```go
func (a *App) MyNewMethod(param string) (model.MyType, error) {
    param = strings.TrimSpace(param)
    if param == "" {
        return model.MyType{}, fmt.Errorf("MyNewMethod: param is required")
    }
    result, err := a.store.MyStoreMethod(param)
    if err != nil {
        return model.MyType{}, fmt.Errorf("MyNewMethod: %w", err)
    }
    return result, nil
}
```

Conventions:
- Validate inputs at the top
- Wrap errors with method name prefix
- Return empty slice `[]T{}` instead of nil (Wails serializes nil as null)

### Step 2: Run `wails dev` or `wails generate module`

This regenerates `frontend/wailsjs/go/main/App.js` with the new binding.

### Step 3: Call from React

```typescript
import { MyNewMethod } from '../wailsjs/go/main/App'

const result = await MyNewMethod("hello")
```

## Adding a New Domain Model

### Step 1: Define the struct in `internal/model/`

```go
// internal/model/mytype.go
type MyType struct {
    ID        string    `json:"id"`
    Name      string    `json:"name"`
    CreatedAt time.Time `json:"createdAt"`
}

func NewMyType(name string) MyType {
    return MyType{
        ID:        uuid.New().String(),
        Name:      name,
        CreatedAt: time.Now(),
    }
}
```

JSON tags are required (Wails serializes to frontend).

### Step 2: Add a migration in `internal/store/migrations.go`

Append to the `migrations` slice (never modify existing entries):

```go
// Version N -- create my_types table.
`CREATE TABLE IF NOT EXISTS my_types (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    created_at DATETIME NOT NULL
);`,
```

### Step 3: Add CRUD methods to `internal/store/sqlite.go`

Follow existing patterns: `CreateMyType`, `GetMyType`, `ListMyTypes`, `UpdateMyType`, `DeleteMyType`.

### Step 4: Add Wails bindings to `app.go`

Follow the pattern in Step 1 of "Adding a New Wails Binding".

## Adding a New Agent Adapter

### Step 1: Create `internal/agent/myagent.go`

```go
type MyAgentAdapter struct{}

func NewMyAgentAdapter() *MyAgentAdapter { return &MyAgentAdapter{} }

func (a *MyAgentAdapter) Name() model.AgentType { return model.AgentMyAgent }
func (a *MyAgentAdapter) IsAvailable() bool {
    _, err := exec.LookPath("myagent")
    return err == nil
}
func (a *MyAgentAdapter) Launch(ctx context.Context, opts LaunchOptions) (*RunningSession, error) {
    // Build command, start process, wire output channel
    // See claude.go for the full pattern
}
func (a *MyAgentAdapter) SendMessage(ctx context.Context, sess *RunningSession, msg string) error { ... }
func (a *MyAgentAdapter) Stop(ctx context.Context, sess *RunningSession) error { ... }
```

### Step 2: Add the AgentType constant in `internal/model/task.go`

```go
AgentMyAgent AgentType = "myagent"
```

Add to `allAgentTypes` slice.

### Step 3: Register in `main.go`

```go
sm.RegisterAdapter(agent.NewMyAgentAdapter())
```

### Step 4: Add scanner detection in `internal/scanner/scanner.go`

Add a case to `matchAgent()`:
```go
if matchExactAgent(nameLower, cmdlineLower, "myagent") {
    return model.AgentMyAgent, true
}
```

## Adding a New Mobile API Endpoint

### Step 1: Define a provider interface

```go
// internal/api/handlers_myfeature.go
type MyFeatureProvider interface {
    MyMethod() ([]model.MyType, error)
}
```

### Step 2: Register routes

```go
func RegisterMyFeatureRoutes(g *echo.Group, app MyFeatureProvider) {
    g.GET("/my-feature", handleMyFeature(app))
}

func handleMyFeature(app MyFeatureProvider) echo.HandlerFunc {
    return func(c echo.Context) error {
        data, err := app.MyMethod()
        if err != nil {
            return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed"})
        }
        return c.JSON(http.StatusOK, data)
    }
}
```

### Step 3: Wire in `server.go` WireRoutes

Add the provider to `WireRoutes` parameters and call `RegisterMyFeatureRoutes`.

### Step 4: Implement the provider on App

The `App` struct in `app.go` must satisfy the new provider interface. `WireRoutes` passes `a` (the App) for all providers.

## Adding a New Frontend View

### Step 1: Create `frontend/src/views/MyView.tsx`

```tsx
export function MyView(): React.ReactElement {
  return (
    <div className="flex-1 flex flex-col min-h-0">
      <header className="h-12 flex-shrink-0 flex items-center px-5 border-b border-border bg-surface">
        <h1 className="text-base font-bold tracking-wide text-primary">My View</h1>
      </header>
      <div className="flex-1 overflow-y-auto p-5">
        {/* content */}
      </div>
    </div>
  )
}
```

### Step 2: Add ViewId to NavRail

In `frontend/src/components/NavRail.tsx`, add to the `ViewId` union type and add a nav item.

### Step 3: Add to App.tsx router

```tsx
{activeView === 'my-view' && <MyView />}
```

## Adding a New Frontend Component

Create in `frontend/src/components/MyComponent.tsx`. Call Wails bindings:

```typescript
import { MyNewMethod } from '../../wailsjs/go/main/App'

const [data, setData] = useState<model.MyType[]>([])
useEffect(() => {
  MyNewMethod("param").then(setData).catch(console.error)
}, [])
```

## Response Format

### Wails (Go -> Frontend)
All methods return native Go types, serialized as JSON by Wails. Errors are thrown as JS exceptions.

### Mobile API
Success: `200 { ...data }` or `200 [{...}, ...]`
Error: `4xx/5xx { "error": "human-readable message" }`

## Testing

### Go
```bash
go test ./...                    # All tests
go test ./internal/model/...     # Specific package
go test ./internal/store/... -v  # Verbose
```

### Frontend
```bash
cd frontend && npm run test      # Vitest
```

## Running

```bash
wails dev                        # Dev mode with hot reload
wails build                      # Production binary
./build/bin/awm                  # Run built binary
awm list                         # CLI mode
awm open                         # Launch desktop from CLI
```

## Project Structure

```
.
+-- main.go                  Entry point (CLI vs desktop routing)
+-- app.go                   Wails-bound App struct (~2600 lines)
+-- wails.json               Wails v2 config
+-- go.mod                   Go dependencies
+-- internal/
|   +-- model/               Domain types (Task, Session, Workflow, etc.)
|   +-- store/               SQLite CRUD + migrations
|   +-- agent/               Agent adapters + SessionManager
|   +-- api/                 Echo HTTP handlers (mobile API)
|   +-- cli/                 Cobra CLI commands
|   +-- git/                 Git operations (info, diff, stage, commit, push)
|   +-- scanner/             Process auto-detection
|   +-- terminal/            Terminal providers (CMux, iTerm2, Terminal.app)
|   +-- workspace/           Virtual monorepo workspaces
|   +-- discovery/           Project filesystem discovery
|   +-- config/              Config file management
|   +-- claude/              Claude-specific sessions/usage
|   +-- cmux/                CMux socket RPC client
|   +-- notify/              macOS notifications (platform-specific)
|   +-- impact/              Cross-session conflict detection
|   +-- nlquery/             Natural language command engine
|   +-- recording/           Session recording/replay
|   +-- watcher/             File tail watcher
|   +-- ci/                  CI pipeline watcher
|   +-- proc/                Process alive check
+-- frontend/
|   +-- src/
|   |   +-- App.tsx          View router
|   |   +-- main.tsx         React entry point
|   |   +-- views/           8 view components
|   |   +-- components/      ~40 UI components
|   |   +-- lib/             Utilities, hooks, parsers
|   +-- wailsjs/             Auto-generated Wails bindings
|   +-- package.json         React/Vite/Tailwind deps
+-- plans/                   Planning docs
+-- mobile/                  Expo mobile app (future)
+-- cmd/awm-cmux-helper/     CMux helper binary
```
