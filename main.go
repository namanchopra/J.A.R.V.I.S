package main

import (
	"context"
	"embed"
	"fmt"
	"log"
	"log/slog"
	"os"

	"time"

	"github.com/namanchopra/jarvis/internal/agent"
	"github.com/namanchopra/jarvis/internal/arch"
	"github.com/namanchopra/jarvis/internal/cli"
	"github.com/namanchopra/jarvis/internal/cmux"
	"github.com/namanchopra/jarvis/internal/config"
	"github.com/namanchopra/jarvis/internal/hotkey"
	"github.com/namanchopra/jarvis/internal/paths"
	"github.com/namanchopra/jarvis/internal/scanner"
	"github.com/namanchopra/jarvis/internal/store"
	"github.com/namanchopra/jarvis/internal/terminal"

	"github.com/spf13/cobra"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed all:frontend/dist
var assets embed.FS

// cliSubcommands is the set of first-positional arguments that trigger CLI mode
// instead of launching the Wails desktop window.
var cliSubcommands = map[string]bool{
	"add":        true,
	"list":       true,
	"update":     true,
	"remove":     true,
	"open":       true,
	"help":       true,
	"version":    true,
	"completion": true,
}

// cliFlags is the set of global flags that should route through the CLI rather
// than launching the desktop window.
var cliFlags = map[string]bool{
	"--help":    true,
	"-h":        true,
	"--version": true,
	"-v":        true,
}

// isCLIMode returns true when the user invoked a CLI subcommand
// (e.g. `awm list`, `awm add --name ...`) or a global flag like --help.
// It returns false for bare `awm` (no args) and when `wails dev` passes
// its own flags such as `-assetdir` or `-e`.
func isCLIMode() bool {
	if len(os.Args) < 2 {
		return false
	}
	first := os.Args[1]

	// Explicit global flags that the user expects CLI behaviour from.
	if cliFlags[first] {
		return true
	}

	// Any other flag (e.g. -assetdir, --debounce injected by `wails dev`)
	// should fall through to desktop mode.
	if len(first) > 0 && first[0] == '-' {
		return false
	}

	return cliSubcommands[first]
}

func main() {
	// Architecture guard: Jarvis requires native Apple Silicon (arm64). If the
	// Universal binary is launched under Rosetta 2 (x86_64), the Python daemon
	// will fail deep inside an MPS call. Fail fast with a clear message that
	// is visible both on stderr (terminal) and in Console.app (.app bundle —
	// stderr from launchd-started apps is captured into the system log).
	if err := arch.Check(); err != nil {
		// log.Fatalln writes to the standard logger (stderr by default) and
		// calls os.Exit(1). The timestamped prefix makes the message easy
		// to find in Console.app when launched as a .app bundle.
		log.Fatalln(err.Error())
	}

	// Migrate ~/.awm → ~/.jarvis if needed (one-shot, idempotent on subsequent runs).
	if err := paths.MigrateLegacyHome(); err != nil {
		slog.Warn("legacy home migration failed; continuing with new path", "err", err)
		// Continue anyway — fresh-install or partial-migration paths are still functional.
	}

	// Open the SQLite store (default path: ~/.jarvis/awm.db).
	// Both CLI and desktop modes share the same database.
	s, err := store.NewStore("")
	if err != nil {
		slog.Error("failed to open store", "err", err)
		os.Exit(1)
	}
	defer s.Close()

	if isCLIMode() {
		runCLI(s)
		return
	}

	runDesktop(s)
}

// runCLI builds the Cobra command tree, adds the "open" subcommand, and
// executes the command derived from os.Args.
func runCLI(s *store.Store) {
	rootCmd := cli.NewRootCmd(s)

	// "open" explicitly launches the desktop window from the CLI.
	rootCmd.AddCommand(&cobra.Command{
		Use:   "open",
		Short: "Launch the Jarvis desktop window",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("Launching Jarvis desktop...")
			launchDesktop(s)
			return nil
		},
	})

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// runDesktop starts the Wails v2 desktop application.
func runDesktop(s *store.Store) {
	launchDesktop(s)
}

// launchDesktop contains the actual Wails bootstrap so it can be called from
// both the default (no-args) path and the "open" subcommand.
func launchDesktop(s *store.Store) {
	sc := scanner.NewScanner(s, 5*time.Second)

	// Create the session manager with nil emitFn — the Wails runtime context
	// is not available until startup(), where SetEmitFn is called.
	sm := agent.NewSessionManager(s, nil)
	sm.RegisterAdapter(agent.NewClaudeAdapter())
	sm.RegisterAdapter(agent.NewKiroAdapter())
	sm.RegisterAdapter(agent.NewGeminiAdapter())
	sm.RegisterAdapter(agent.NewCodexAdapter())
	sm.RegisterAdapter(agent.NewAiderAdapter())

	// CMux client — enables terminal control if CMux is installed.
	cc := cmux.NewClient()

	// Terminal manager — aggregates CMux, iTerm2, and Terminal.app.
	tm := terminal.NewTerminalManager(cc)

	app := NewApp(s, sc, sm, cc, tm)

	err := wails.Run(&options.App{
		Title:            "Jarvis",
		Width:            1440,
		Height:           900,
		MinWidth:         800,
		MinHeight:        600,
		WindowStartState: options.Normal,
		Frameless:        false,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 2, G: 10, B: 8, A: 1},
		Mac: &mac.Options{
			TitleBar: mac.TitleBarDefault(),
			About: &mac.AboutInfo{
				Title:   "Jarvis",
				Message: "AI Voice Companion",
			},
		},
		// OnStartup wraps app.startup with the v0.3.0 global PTT hotkey
		// wiring (TASK-005). The lib's hk.Register MUST be invoked from
		// the macOS main thread; Wails runs OnStartup on the main goroutine
		// (which is the locked main thread), so this is the correct seam.
		// A registration failure (Accessibility denied, hotkey conflict)
		// logs a warning and emits "overlay:hotkey_error" — the app still
		// starts so the rest of the surface (HUD, mobile API) keeps working.
		OnStartup: func(ctx context.Context) {
			app.startup(ctx)
			wireOverlayHotkey(ctx, app)
		},
		OnShutdown: func(ctx context.Context) {
			if app.hotkeyManager != nil {
				if err := app.hotkeyManager.Close(); err != nil {
					slog.Warn("overlay hotkey: close error", "err", err)
				}
			}
			if app.hotkeyPTTManager != nil {
				if err := app.hotkeyPTTManager.Close(); err != nil {
					slog.Warn("overlay PTT hotkey: close error", "err", err)
				}
			}
			app.shutdown(ctx)
		},
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		slog.Error("wails.Run error", "err", err)
		os.Exit(1)
	}
}

// wireOverlayHotkey constructs the hotkey.Manager, attaches it to the App,
// and registers the configured global PTT hotkey (default "alt+space").
//
// The macOS Carbon EventHotKey APIs (called transitively from hk.Register)
// require execution on the main thread. Wails invokes OnStartup on the
// main goroutine which it has runtime.LockOSThread'd to the OS main
// thread, so this function runs on the right thread by virtue of being
// called from OnStartup. Receiving on the lib's Keydown / Keyup channels
// from the watch goroutine is safe off-thread (see internal/hotkey for
// the lifecycle docs).
//
// Failure handling is non-fatal: an OS-denied registration (Accessibility
// not granted, or another app owns the combo) is logged at Warn level and
// the "overlay:hotkey_error" Wails event is emitted so the Settings panel
// (TASK-009) can surface a "grant Accessibility access" CTA. The rest of
// the app continues to start.
func wireOverlayHotkey(ctx context.Context, app *App) {
	cfg, err := config.Load()
	spec := ""
	if err != nil {
		slog.Warn("overlay hotkey: config load failed, falling back to default", "err", err)
	} else {
		spec = cfg.OverlayHotkey
	}
	// Defensive: an empty spec on a degraded config load would brick the
	// binding; fall back to the documented default. config.Load already
	// has its own empty-spec defence (TASK-001) but mirroring it here
	// keeps this wiring robust against future config-loading regressions.
	if spec == "" {
		spec = "alt+space"
	}

	app.hotkeyManager = hotkey.NewManager()
	if err := app.hotkeyManager.Register(
		spec,
		app.hotkeyPressCallback(),
		app.hotkeyReleaseCallback(),
	); err != nil {
		slog.Warn("overlay hotkey: registration failed (Accessibility permission?)",
			"spec", spec, "err", err)
		// Emit on the Wails event bus so the Settings panel can render
		// the "grant Accessibility access" inline warning. The frontend
		// is responsible for displaying it; we just publish the signal.
		wailsruntime.EventsEmit(ctx, "overlay:hotkey_error", err.Error())
		// Continue startup -- the feature is degraded but the app still
		// works (HUD, mobile API, all other Wails bindings unaffected).
		// (Don't return: still try to register the PTT hotkey -- one
		// failing doesn't necessarily mean the other will.)
	} else {
		slog.Info("overlay hotkey: registered", "spec", spec)
	}

	// Second hotkey: global push-to-talk (default ctrl+space). Press =
	// OverlayShow + send ptt_active; release = send ptt_release. Lets the
	// user talk to Jarvis from any app without first focusing the overlay.
	pttSpec := "ctrl+space"
	if cfg != nil && cfg.OverlayPTTHotkey != "" {
		pttSpec = cfg.OverlayPTTHotkey
	}
	app.hotkeyPTTManager = hotkey.NewManager()
	if err := app.hotkeyPTTManager.Register(
		pttSpec,
		app.hotkeyPTTPressCallback(),
		app.hotkeyPTTReleaseCallback(),
	); err != nil {
		slog.Warn("overlay PTT hotkey: registration failed",
			"spec", pttSpec, "err", err)
		wailsruntime.EventsEmit(ctx, "overlay:hotkey_error", err.Error())
		return
	}
	slog.Info("overlay PTT hotkey: registered", "spec", pttSpec)
}
