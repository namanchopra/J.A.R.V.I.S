# Development Guide

## Tech Stack

| Layer | Tech |
|---|---|
| Backend (desktop) | Go 1.25, Wails v2, Cobra CLI, Echo HTTP, modernc.org/sqlite, gopsutil, fsnotify, go-ole (Windows), go-toast/v2 (Windows) |
| Voice daemon | Python 3.13 (bundled via python-build-standalone + uv), Pipecat, MLX Whisper (Mac), faster-whisper (Windows), VibeVoice |
| Frontend (desktop) | React 18, Vite 8, Tailwind CSS 4, TypeScript 6, framer-motion |
| Frontend (mobile) | Expo SDK 52, expo-router, React Native, TypeScript |
| Frontend (website) | Next.js 15 (App Router), Tailwind CSS, qrcode.react |
| Build | `wails dev` (hot reload), `wails build`, Inno Setup (Win), `create-dmg` (Mac), GoReleaser-equivalent via `release.yml`/`release-windows.yml` |
| Data | SQLite at `~/.jarvis/awm.db` (Mac) / `%USERPROFILE%\.jarvis\awm.db` (Win) · Config at `~/.jarvis/config.json` · Logs at `~/.jarvis/logs/` · Meetings at `~/.jarvis/meetings/` |

## Project Invariants (read these first)

1. **App struct method = Wails binding.** Every exported method on `App` in `app.go` or any `app_*.go` is auto-exposed to the frontend. ~250 exist.
2. **Error wrapping:** `fmt.Errorf("MethodName: %w", err)`.
3. **Nil slices:** Return `[]T{}` not `nil` (Wails serializes nil as JSON `null`).
4. **Input validation:** Validate and trim at the top of each binding method.
5. **Migrations:** Append-only to `internal/store/migrations.go`. Never modify existing entries.
6. **Platform code:** Build tags `//go:build darwin`, `//go:build windows`, `//go:build !darwin` etc. Mirror the existing pattern — pair `_darwin.go` and `_windows.go` with an `_other.go` fallback or cross-platform `*.go` for the shared surface.
7. **Mobile API:** Provider interfaces decouple handlers from `App`. All satisfied by the `App` struct in `app.go`.
8. **Frontend:** Views in `src/views/`, components in `src/components/`, call `wailsjs/go/main/App`.
9. **macOS build stays green:** Anything new must compile on `darwin/arm64` even if it's Windows-targeted (use build tags).
10. **Wails v2 supports Windows natively.** No platform branch needed at the Wails layer for windowing; differences live in build-tagged Go code.

## Adding a New Wails Binding

### Step 1: Add the method to `app.go` or a relevant `app_*.go` partial

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

**Which file?** Put it in the partial that matches the domain:
- `app.go` — task/session/workflow/git CRUD (the original AWM surface)
- `app_jarvis.go` — daemon lifecycle, conversation
- `app_meeting.go` — meeting capture
- `app_setup.go` — first-launch setup
- `app_gcal.go` — Google Calendar
- `app_spotify.go` — Spotify
- `app_macctl.go` — system control (open/quit/volume/clipboard etc.)
- `app_hotkey.go` — overlay hotkeys
- `app_overlay.go` — overlay show/hide/mode
- `app_diagnostics.go` — debug helpers
- New domain? Create `app_<domain>.go`.

### Step 2: Run `wails dev` (or `wails generate module`)

Regenerates `frontend/wailsjs/go/main/App.js` + `App.d.ts` with the new binding.

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
// Version N — create my_types table.
`CREATE TABLE IF NOT EXISTS my_types (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    created_at DATETIME NOT NULL
);`,
```

### Step 3: Add CRUD methods to `internal/store/sqlite.go`

Follow existing patterns: `CreateMyType`, `GetMyType`, `ListMyTypes`, `UpdateMyType`, `DeleteMyType`.

### Step 4: Add Wails bindings (see above).

## Adding a New Platform-Specific Implementation

When a feature has Mac and Windows backends, use the **build-tag split** pattern that's now used across 10 packages (`internal/notify/`, `internal/paths/`, `internal/screencapture/`, etc.).

### Step 1: Define the cross-platform interface or constructor

```go
// internal/myfeature/myfeature.go
package myfeature

type Capturer interface {
    Start(callback Callback) error
    Stop() error
}

// New() picks the platform-appropriate implementation.
// Concrete types live in build-tagged files.
func New() Capturer {
    return newCapturer()  // defined per-platform
}
```

### Step 2: Add platform files

```go
// internal/myfeature/myfeature_darwin.go
//go:build darwin

package myfeature
// ... Mac impl, returns Mac-specific Capturer

func newCapturer() Capturer { return &darwinCapturer{} }
```

```go
// internal/myfeature/myfeature_windows.go
//go:build windows

package myfeature
// ... Win impl

func newCapturer() Capturer { return &windowsCapturer{} }
```

```go
// internal/myfeature/myfeature_other.go
//go:build !darwin && !windows

package myfeature
// ... no-op fallback for Linux/BSD

func newCapturer() Capturer { return &noopCapturer{} }
```

### Step 3: For CGO Windows, add a no-CGO fallback

If the Windows file uses `import "C"`, also add:

```go
// internal/myfeature/myfeature_windows_nocgo.go
//go:build windows && !cgo

package myfeature
// Stub for Mac→Windows cross-compile without MinGW.
// Production builds (CI on windows-2022 + MSVC) use the real CGO file.

func newCapturer() Capturer { return &windowsNoCgoStub{} }
```

The real CGO file gets `//go:build windows && cgo`.

### Step 4: Verify on both platforms

```bash
go build ./...                                     # macOS native
GOOS=windows GOARCH=amd64 go build ./internal/...  # cross-compile sanity
GOOS=windows GOARCH=arm64 go build ./internal/...
```

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
    // Build command, start process, wire output channel.
    // See claude.go for the full pattern.
}
func (a *MyAgentAdapter) SendMessage(ctx context.Context, sess *RunningSession, msg string) error { ... }
func (a *MyAgentAdapter) Stop(ctx context.Context, sess *RunningSession) error { ... }
```

### Step 2: Register the AgentType in `internal/model/task.go`

```go
AgentMyAgent AgentType = "myagent"
```

Add to `allAgentTypes` slice.

### Step 3: Register the adapter in `main.go`

```go
sm.RegisterAdapter(agent.NewMyAgentAdapter())
```

### Step 4: Add scanner detection in `internal/scanner/scanner.go`

```go
if matchExactAgent(nameLower, cmdlineLower, "myagent") {
    return model.AgentMyAgent, true
}
```

## Adding a New Mobile API Endpoint

### Step 1: Define a provider interface in your handler file

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

### Step 3: Wire into `server.go::WireRoutes`

Add the provider to `WireRoutes` parameters and call `RegisterMyFeatureRoutes`.

### Step 4: Implement on `App`

The `App` struct in `app.go` must satisfy the new provider interface. `WireRoutes` passes `a` (the App) for all providers.

## Adding a New syscontrol Tool

The `internal/syscontrol/` package extracts cross-platform system-control interfaces. Mac implementations live in `internal/macctl/`; Windows implementations live in `internal/syscontrol/*_windows.go`.

### Step 1: Extend the relevant interface

```go
// internal/syscontrol/audiocontroller.go
type AudioController interface {
    SetVolume(percent int) error
    Mute() error
    Unmute() error
    NewMethod() error  // ← new
}
```

### Step 2: Add the Mac backend in `internal/macctl/audio.go`

(Macctl provides AppleScript-driven implementations.)

### Step 3: Add the Windows backend in `internal/syscontrol/audiocontroller_windows.go`

Use the existing COM/PowerShell pattern. See `appcontroller_windows.go` for the `psSingleQuote` + `powershellArgs` helpers.

### Step 4: Expose via a Wails binding in `app_macctl.go`

Keep the `Mac*` prefix even on Windows for API stability — frontend code doesn't need to branch.

## Adding a New Settings Panel

### Step 1: Create `frontend/src/views/settings/MyPanel.tsx`

```tsx
import { GetConfig, SaveConfig } from '../../wailsjs/go/main/App'

export function MyPanel(): React.ReactElement {
  // ... follows the pattern from existing panels (BehaviorPanel, VoicePanel, etc.)
}
```

### Step 2: Add to the tab list in `SettingsTabs.tsx`

```tsx
const TABS = [
  { id: 'behavior', label: 'Behavior' },
  // ...
  { id: 'my-panel', label: 'My Panel' },
]
```

### Step 3: Wire into `SettingsView.tsx` router

```tsx
{activeTab === 'my-panel' && <MyPanel />}
```

### Step 4: Add a Vitest companion `MyPanel.test.tsx`

Test source-level invariants (text strings, conditional rendering by platform).

## Adding to the Daemon (Python sidecar)

The Pipecat daemon lives at `scripts/jarvis-daemon/main.py`. Modules:

| File | Purpose |
|---|---|
| `main.py` | Pipecat pipeline wiring + WebSocket transport to Go |
| `pipecat_stt.py` | STT (MLX on Mac, faster-whisper on Win) |
| `pipecat_tts_vibevoice.py` | TTS via VibeVoice (CUDA opt-in on Win) |
| `pipecat_tts_kokoro.py` / `_cartesia.py` / `_macos_say.py` | Alternative TTS backends |
| `pipecat_llm.py` | LLM tool-calling loop |
| `llm_picker.py` | OpenRouter vs Ollama selection |
| `tools.py` | Tool definitions exposed to the LLM (open_app, search_spotify, run_user_script, etc.) |
| `tool_bridge.py` | Bridge: tool call → Go-side execution via WebSocket → response |
| `meeting_notes.py` | Markdown generation + recap synthesis |
| `mic.py` | PyAudio capture |
| `wakeword.py` (or model_status.py) | openWakeWord integration |
| `config.py` | Daemon-side config (matches Go-side `internal/config/`) |

To add a new tool the LLM can call:
1. Define it in `tools.py` with JSON schema for arguments
2. Implement the handler in `tool_bridge.py` (route to existing Go-side Wails binding or new endpoint)
3. If new Go-side work needed: add a Wails binding per the section above

## Response Format

### Wails (Go → Frontend)
All methods return native Go types, serialized as JSON by Wails. Errors are thrown as JS exceptions.

### Mobile API (Echo HTTP)
- Success: `200 { ...data }` or `200 [{...}, ...]`
- Error: `4xx/5xx { "error": "human-readable message" }`

### Wails Events (streaming, Go → Frontend)
- `runtime.EventsEmit(ctx, "eventName", payload)` in Go
- `EventsOn("eventName", handler)` in React

## Testing

### Go
```bash
go test ./...                       # All tests
go test ./internal/syscontrol/...   # Specific package
go test ./internal/jarvis/... -v    # Verbose
go test -race ./internal/jarvis/... # With race detector
```

Cross-platform build sanity from a Mac:
```bash
GOOS=windows GOARCH=amd64 go build ./internal/...
GOOS=windows GOARCH=arm64 go build ./internal/...
```

### Frontend (desktop)
```bash
cd frontend && npm run test          # Vitest
cd frontend && npm run test -- MyPanel.test.tsx
```

### Python (daemon)
```bash
cd scripts/jarvis-daemon && python -m pytest tests/
```

### Website (Next.js)
```bash
cd website && npm run build          # Production build (also typechecks)
cd website && npm run lint
```

### Mobile (Expo)
```bash
cd mobile && npm run start           # Expo dev server
cd mobile && npx tsc --noEmit        # Typecheck
```

## Running

```bash
wails dev                            # Dev mode with hot reload
wails build                          # Production binary (macOS arm64)
wails build -platform windows/amd64  # Windows x64 binary
wails build -platform windows/arm64  # Windows arm64 binary
./build/bin/jarvis                   # Run built binary
jarvis list                          # CLI mode (legacy AWM)
jarvis open                          # Launch desktop from CLI
```

## Build & Distribution

### macOS
1. `wails build` produces `build/bin/Jarvis.app`
2. `build/scripts/post-build.sh` stages bundled Python + uv + daemon source into Resources/
3. `release.yml` (on tag) signs with Developer ID, notarizes via notarytool, creates DMG, attaches to GitHub Release

### Windows
1. `wails build -platform windows/<arch>` produces `build/bin/jarvis.exe`
2. `build/scripts/post-build.ps1` stages bundled Python + uv + portaudio.dll + daemon into Resources/
3. `installer/jarvis.iss` (Inno Setup) packages into `Jarvis-Setup-<version>.exe`
4. `release-windows.yml` (on tag) signs via Azure Trusted Signing (signtool), publishes to GitHub Release
5. `winget-pkgs/manifests/n/namanchopra/Jarvis/` — submit to microsoft/winget-pkgs for `winget install JarvisAI.Jarvis`

## Project Structure

```
.
├── main.go                  Entry: CLI vs desktop routing + arch.Check()
├── app.go                   Wails-bound App struct (~130 methods)
├── app_*.go                 18 partials grouping bindings by domain
├── app_voice_{darwin,windows,other}.go  Platform-split helpers
├── wails.json               Wails v2 config (Mac + Windows targets)
├── go.mod                   Go deps
├── internal/                31 packages — see architecture.md
├── frontend/
│   ├── src/
│   │   ├── views/           OverlayView + SettingsView
│   │   ├── views/settings/  7 panels + FridayPairingModal + tab chrome
│   │   ├── components/      SetupScreen + shared
│   │   ├── lib/             use-setup-state + utilities
│   ├── wailsjs/             Auto-generated Wails bindings
│   ├── package.json         React/Vite/Tailwind/Vitest
├── scripts/
│   ├── jarvis-daemon/       Python Pipecat daemon (~30 modules)
│   ├── setup/install-daemon.{sh,ps1}  First-launch installer
├── build/scripts/           fetch-{python,uv,portaudio} + post-build scripts (sh + ps1)
├── installer/jarvis.iss     Inno Setup script (Windows)
├── mobile/                  Expo phone companion (Friday)
├── website/                 Next.js landing page
├── docs/                    Acceptance docs (per-version)
├── cmd/awm-cmux-helper/     Standalone CMux helper binary (Mac)
├── plans/                   Planning docs (gitignored)
├── .workflows/              Workflow scripts (gitignored)
├── .github/workflows/       CI: ci.yml, release.yml (Mac), release-windows.yml, install-smoke.yml, mobile-update.yml
```
