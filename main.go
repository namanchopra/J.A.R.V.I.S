package main

import (
	"embed"
	"fmt"
	"log/slog"
	"os"

	"time"

	"github.com/namanchopra/jarvis/internal/agent"
	"github.com/namanchopra/jarvis/internal/cli"
	"github.com/namanchopra/jarvis/internal/cmux"
	"github.com/namanchopra/jarvis/internal/paths"
	"github.com/namanchopra/jarvis/internal/scanner"
	"github.com/namanchopra/jarvis/internal/store"
	"github.com/namanchopra/jarvis/internal/terminal"

	"github.com/spf13/cobra"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
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
		OnStartup:  app.startup,
		OnShutdown: app.shutdown,
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		slog.Error("wails.Run error", "err", err)
		os.Exit(1)
	}
}
