# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Apache 2.0 LICENSE
- `internal/paths/` package providing canonical path helpers (`JarvisHome`, `ConfigPath`, `DataPath`, `LogsDir`, `WorkspacesDir`, `ModelsDir`, `RecordingsDir`, `LegacyHome`)
- `MigrateLegacyHome()` migration shim — one-shot copy of `~/.awm/` → `~/.jarvis/` on first launch, with backward-compat symlink for legacy venv directories
- 11 unit tests for paths package + migration shim (race-detector clean, stable across 10 iterations)
- Comprehensive `.gitignore` covering macOS, IDE, Python venvs, secrets, backups, Go test artifacts, and local data dirs

### Changed
- **Go module renamed** from `awm` to `github.com/namanchopra/jarvis`
- **Data directory** moved from `~/.awm/` to `~/.jarvis/` (existing installs auto-migrate via `MigrateLegacyHome()`; `~/.awm` becomes a symlink for backward compat)
- Python daemon (`scripts/jarvis-daemon/config.py`) migrated from `dex*` to `jarvis*` config keys with bidirectional backward-compat reads (legacy configs continue to work)
- Sanitized company-specific example names (`maya-web`, `auth-service`, `mumz-cosmos`, `Mumzworld`) from system prompts, regex docs, comments, and `SyncDotClaude` candidate paths

### Removed
- 3 unrouted view components (`ControlCenterView`, `DashboardView`, `ActivityView`)
- 8 orphan UI components (`CostDashboard`, `JarvisOnboarding` + 3 siblings, `Layout`, `ProjectsPanel`, `SessionMiniOutput`, `SessionOutput`, `WorkspacePreview`, `JarvisMiniOrb`)
- 3 legacy setup scripts (`setup-vance.sh`, `setup-dex-v5.sh`, `setup-dex-v7.sh`)
- 43 internal planning documents from `plans/` directory (preserved at `~/Documents/jarvis-plans/`; active plans moved to in-repo `.local-plans/`)

### Deferred
- Go-side config struct still writes legacy `dex*` JSON keys (e.g. `dexEnabled`, `dexAPIKey`). Migration to `jarvis*` keys with `UnmarshalJSON` backward-compat is deferred to Phase 2 (DMG-ready public build) and will land alongside the Settings UI rewrite. Existing user configs continue to work unchanged.

### Known Issues
- The repo is not yet initialized as a git repository. `git init` will happen in Phase 2 release pipeline setup.
