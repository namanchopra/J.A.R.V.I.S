# Jarvis Bundled Shortcuts

This directory holds the macOS `.shortcut` files that Jarvis ships with and
the manifest that drives the first-run installer
(`JarvisInstallBundledShortcuts` Wails binding, see
`app_shortcuts_installer.go`).

## Status: placeholders

Each `.shortcut` file currently begins with the sentinel header
`JARVIS_PLACEHOLDER_SHORTCUT`. The installer detects that header and **skips**
the entry — it is recorded as "skipped" in the sentinel file at
`~/.jarvis/.bundled-shortcuts-installed-v0.3.0`, not as a failure.

This is intentional. A real `.shortcut` file is a binary plist encoded by
Apple's Shortcuts.app, so the assets cannot be checked in until each shortcut
is authored interactively. Until then the placeholder is enough to:

1. Round-trip the installer (sentinel write, manifest parse, skip path).
2. Reserve the filenames listed in `manifest.json` so callers don't have to
   change wiring when the real exports drop in.
3. Document the intended set of bundled shortcuts in one place.

## Authoring a real shortcut

1. Open **Shortcuts.app** on macOS.
2. Build (or edit) the shortcut with the matching name from `manifest.json`
   (for example "Take Note").
3. Select the shortcut in the sidebar.
4. **File → Export → Save as Shortcut File…**
5. Save the resulting `.shortcut` over the placeholder of the same filename
   in this directory (for example `build/shortcuts/take-note.shortcut`).
6. Verify it round-trips locally:
   ```sh
   shortcuts import build/shortcuts/take-note.shortcut
   ```
7. Bump the sentinel version constant
   (`bundledShortcutsSentinelName` in `app_shortcuts_installer.go`) if a
   newly-authored shortcut needs to be re-installed on machines that already
   ran the previous installer pass — this is how we trigger an idempotent
   re-install across Jarvis upgrades.

## Re-installing bundled shortcuts

The installer is idempotent: it short-circuits as soon as it sees the
sentinel file at `~/.jarvis/.bundled-shortcuts-installed-v0.3.0`. If the user
wants to re-import (e.g. after deleting one from Shortcuts.app) they can
delete the sentinel and re-launch Jarvis, or hit
**Settings → Permissions → Reinstall bundled shortcuts** (frontend wiring in
TASK-017) which calls `JarvisInstallBundledShortcuts` after removing the
sentinel.

## Files

| File | Manifest entry name | Purpose |
|------|---------------------|---------|
| `manifest.json` | — | Drives the installer — the source of truth for which shortcuts ship. |
| `take-note.shortcut` | Take Note | Append a quick note to Apple Notes. |
| `quick-screenshot.shortcut` | Quick Screenshot | Screenshot the screen + copy to clipboard. |
| `lock-screen.shortcut` | Lock Screen | Immediately lock the Mac. |
| `sleep.shortcut` | Sleep | Put the Mac to sleep. |
| `open-downloads.shortcut` | Open Downloads | Open ~/Downloads in Finder. |
| `new-calendar-event.shortcut` | New Calendar Event | Create an event in the default calendar. |
| `toggle-focus.shortcut` | Toggle Focus | Toggle Do Not Disturb / current Focus mode. |

## How it gets into the .app bundle

`build/scripts/post-build.sh` rsync/cp's `build/shortcuts/` (the entire
directory, including `manifest.json` and the README) into
`<.app>/Contents/Resources/shortcuts/`. The runtime installer looks there
first via `paths.BundledResourcesDir()`; in dev (`wails dev` or `go run`)
it falls back to `build/shortcuts/` relative to the working directory.
