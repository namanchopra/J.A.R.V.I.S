// app_onboarding.go — Wails bindings for TASK-024 (first-run onboarding modal).
//
// The frontend mounts the onboarding modal on app launch iff IsFirstRun() returns
// true. The modal walks the user through three steps (welcome → pick LLM → grant
// mic permission) and calls MarkFirstRunComplete() to dismiss permanently once
// the user has either (a) entered a validated LLM key (any provider) or
// (b) confirmed Ollama is running locally.
//
// "First run" is detected via a sentinel file at
// ~/.jarvis/.onboarding-complete. We deliberately use a sentinel file rather
// than extending the Config struct because the task scope forbids touching
// internal/config/config.go. The sentinel approach also has a nice property:
// blowing away ~/.jarvis/ (the canonical "factory reset") restores onboarding
// without any extra surgery.
//
// SECURITY CONTRACT (mirrors TASK-017):
// Neither binding logs or persists pasted API key values. IsFirstRun only
// reads existing cfg.JarvisAPIKey to decide whether to skip onboarding for
// an already-configured user; it does not echo the value anywhere.
package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/namanchopra/jarvis/internal/config"
	"github.com/namanchopra/jarvis/internal/paths"
)

// onboardingSentinelPath returns the absolute path to the sentinel file whose
// existence indicates the user has completed first-run onboarding. Centralised
// here so IsFirstRun and MarkFirstRunComplete cannot drift apart.
func onboardingSentinelPath() string {
	return filepath.Join(paths.JarvisHome(), ".onboarding-complete")
}

// IsFirstRun reports whether the onboarding modal should be shown on launch.
//
// The frontend calls this once on mount (see App.tsx) and conditionally
// renders <Onboarding> instead of the HUD. Returns true iff ALL of:
//
//  1. The sentinel file ~/.jarvis/.onboarding-complete does not exist.
//  2. The user has no Jarvis LLM API key configured (cfg.JarvisAPIKey blank).
//  3. A local Ollama server is NOT reachable.
//
// Any of (1..3) being false means the user is "already set up" and the modal
// is suppressed. This avoids re-prompting users upgrading from a build that
// predates this binding.
func (a *App) IsFirstRun() bool {
	// Fast path: explicit "I've done onboarding" marker.
	if _, err := os.Stat(onboardingSentinelPath()); err == nil {
		return false
	}

	// Implicit "already configured" detection — covers users who upgraded
	// from a pre-onboarding build with a key already in config.json.
	cfg := config.Get()
	if cfg != nil && strings.TrimSpace(cfg.JarvisAPIKey) != "" {
		return false
	}

	// Final fallback: if Ollama is reachable, treat the install as ready.
	// IsOllamaRunning has its own 1s timeout (see app_validators.go) so the
	// blocking IsFirstRun call stays bounded.
	if a.IsOllamaRunning() {
		return false
	}

	return true
}

// MarkFirstRunComplete writes the sentinel file that suppresses the onboarding
// modal on future launches. Called by the frontend when the user finishes the
// three-step flow.
//
// Idempotent: re-writing the sentinel is harmless, so the frontend may call
// this without checking whether onboarding was already marked complete.
func (a *App) MarkFirstRunComplete() error {
	home := paths.JarvisHome()
	if err := os.MkdirAll(home, 0o755); err != nil {
		return err
	}
	return os.WriteFile(onboardingSentinelPath(), []byte("ok\n"), 0o644)
}
