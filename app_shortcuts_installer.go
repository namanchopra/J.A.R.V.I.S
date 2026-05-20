package main

// ---------------------------------------------------------------------------
// TASK-016 — bundled Shortcuts.app first-run installer.
//
// JarvisInstallBundledShortcuts walks build/shortcuts/manifest.json (in dev)
// or <.app>/Contents/Resources/shortcuts/manifest.json (in production), and
// for each entry either:
//
//   - skips it if the corresponding .shortcut file starts with the sentinel
//     header "JARVIS_PLACEHOLDER_SHORTCUT" (placeholder, not a real export);
//   - imports it via the macOS `shortcuts import <path>` CLI otherwise.
//
// A sentinel file at ~/.jarvis/.bundled-shortcuts-installed-v0.3.0 records
// the outcome and short-circuits subsequent calls so the installer is
// idempotent across launches (TASK-016 acceptance: "second run is a no-op").
//
// Test seam: shortcutsImportCommandFn lets tests swap in a stub instead of
// shelling out to `shortcuts` — mirrors the startJarvisCommandFn pattern
// from app_jarvis.go.
// ---------------------------------------------------------------------------

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/namanchopra/jarvis/internal/paths"
)

// bundledShortcutsSentinelName is the filename written under JarvisHome()
// once JarvisInstallBundledShortcuts has run. Bump the version suffix to
// force a re-install on machines that already ran an earlier pass.
const bundledShortcutsSentinelName = ".bundled-shortcuts-installed-v0.3.0"

// placeholderShortcutHeader is the sentinel that tags a .shortcut file as a
// stub stand-in for a real Shortcuts.app export. The installer detects it
// and skips the entry rather than handing the text file to `shortcuts
// import`, which would fail.
const placeholderShortcutHeader = "JARVIS_PLACEHOLDER_SHORTCUT"

// bundledShortcutEntry mirrors one element of manifest.json's "shortcuts"
// array — name + relative filename + a short description.
type bundledShortcutEntry struct {
	Name        string `json:"name"`
	Filename    string `json:"filename"`
	Description string `json:"description"`
}

// bundledShortcutsManifest is the top-level shape of manifest.json.
type bundledShortcutsManifest struct {
	Shortcuts []bundledShortcutEntry `json:"shortcuts"`
}

// shortcutsImportCommandFn is the indirection JarvisInstallBundledShortcuts
// uses to construct the exec.Cmd that calls `shortcuts import`. Production
// returns a real exec.Command; tests swap this for a stub that records the
// invocation without actually shelling out. Same pattern as
// startJarvisCommandFn in app_jarvis.go.
var shortcutsImportCommandFn = func(name string, arg ...string) *exec.Cmd {
	return exec.Command(name, arg...)
}

// bundledShortcutsDirOverride lets tests pin the manifest/shortcuts dir to a
// temp directory without spinning up a fake .app bundle. When nil
// (production), resolveBundledShortcutsDir falls back to BundledResourcesDir
// and then to ./build/shortcuts.
var bundledShortcutsDirOverride func() string

// resolveBundledShortcutsDir returns the directory that holds manifest.json
// and the .shortcut files. Production order:
//
//  1. The .app bundle's Resources/shortcuts/ (set by post-build.sh).
//  2. ./build/shortcuts or ../build/shortcuts (dev mode).
//
// Returns "" if nothing is found, so the caller can return a clean
// no-shortcuts-here message instead of pretending to install.
func resolveBundledShortcutsDir() string {
	if bundledShortcutsDirOverride != nil {
		return bundledShortcutsDirOverride()
	}
	if res := paths.BundledResourcesDir(); res != "" {
		c := filepath.Join(res, "shortcuts")
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			return c
		}
	}
	for _, c := range []string{"build/shortcuts", "../build/shortcuts"} {
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			return c
		}
	}
	return ""
}

// JarvisInstallBundledShortcuts is a Wails-bound binding that installs the
// shortcuts shipped under build/shortcuts/ (dev) or Resources/shortcuts/
// (production). It is idempotent: once the sentinel under JarvisHome()
// exists, subsequent calls return early with "already installed".
//
// Returns a human-readable summary string suitable for surfacing in the
// Settings → Permissions panel.
func (a *App) JarvisInstallBundledShortcuts() (string, error) {
	sentinel := filepath.Join(paths.JarvisHome(), bundledShortcutsSentinelName)
	if _, err := os.Stat(sentinel); err == nil {
		return "Bundled shortcuts already installed.", nil
	}

	dir := resolveBundledShortcutsDir()
	if dir == "" {
		return "No bundled shortcuts directory found.", nil
	}

	manifestPath := filepath.Join(dir, "manifest.json")
	f, err := os.Open(manifestPath)
	if err != nil {
		return "", fmt.Errorf("JarvisInstallBundledShortcuts: open manifest: %w", err)
	}
	defer f.Close()

	var m bundledShortcutsManifest
	if err := json.NewDecoder(f).Decode(&m); err != nil {
		return "", fmt.Errorf("JarvisInstallBundledShortcuts: decode manifest: %w", err)
	}

	installed, skipped, failed := 0, 0, 0
	for _, s := range m.Shortcuts {
		path := filepath.Join(dir, s.Filename)
		contents, rerr := os.ReadFile(path)
		if rerr != nil {
			// File listed in manifest but absent on disk — count as failed so
			// the user can see the discrepancy in the summary.
			failed++
			continue
		}
		if strings.HasPrefix(string(contents), placeholderShortcutHeader) {
			// Placeholder — installer can't hand a text file to `shortcuts
			// import` (it expects a binary plist), so skip without failing.
			skipped++
			continue
		}
		cmd := shortcutsImportCommandFn("shortcuts", "import", path)
		if err := cmd.Run(); err != nil {
			failed++
			continue
		}
		installed++
	}

	// Write the sentinel even when nothing imported successfully — the point
	// is "we tried in v0.3.0", so we don't re-scan on every launch. Settings
	// can offer a Reinstall button that deletes the sentinel first.
	if err := os.MkdirAll(paths.JarvisHome(), 0o755); err != nil {
		return "", fmt.Errorf("JarvisInstallBundledShortcuts: mkdir jarvis home: %w", err)
	}
	body := []byte(fmt.Sprintf("installed=%d skipped=%d failed=%d\n", installed, skipped, failed))
	if err := os.WriteFile(sentinel, body, 0o644); err != nil {
		return "", fmt.Errorf("JarvisInstallBundledShortcuts: write sentinel: %w", err)
	}

	return fmt.Sprintf("Bundled shortcuts: %d installed, %d placeholders skipped, %d failed.", installed, skipped, failed), nil
}
