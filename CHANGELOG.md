# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

### Changed

### Fixed

### Removed

## [0.1.0] - 2026-05-12

### Added
- Apache 2.0 LICENSE
- `internal/paths/` package providing canonical path helpers (`JarvisHome`, `ConfigPath`, `DataPath`, `LogsDir`, `WorkspacesDir`, `ModelsDir`, `RecordingsDir`, `LegacyHome`)
- `MigrateLegacyHome()` migration shim — one-shot copy of `~/.awm/` → `~/.jarvis/` on first launch, with backward-compat symlink for legacy venv directories
- 11 unit tests for paths package + migration shim (race-detector clean, stable across 10 iterations)
- Comprehensive `.gitignore` covering macOS, IDE, Python venvs, secrets, backups, Go test artifacts, and local data dirs
- Settings overlay reached via the gear button on the orb (or **Cmd+,**) with 5 tabs: Connections, Voice, Behavior, Diagnostics, and Advanced. Mission Control Diagnostic aesthetic — corner-bracketed panels, custom-painted SF Mono inputs/selects, cyan-glow focus underlines, custom checkboxes that flip to glowing cyan, gradient-filled range sliders with bracket-shaped thumbs, sticky save bar, and a scanline + grid ambient backdrop
- API key validation for OpenRouter, Google AI Studio, Anthropic, Cartesia, ElevenLabs, and Picovoice — eye-toggle show/hide on each field plus a Validate button that hits each provider's cheapest authenticated endpoint and returns a gray/green/red result pill (keys are never logged)
- LLM provider dropdown (Gemini 2.5 Flash, Claude Haiku 4.5, GPT-4o-mini, `ollama:qwen3:4b`) with check/warn availability indicators; new probe for a running local Ollama instance on `localhost:11434`
- TTS provider dropdown (VibeVoice, Kokoro, Cartesia) and STT model dropdown (Whisper small.en, Whisper tiny.en, faster-whisper)
- Persistent daemon log at `~/.jarvis/logs/daemon.log` — daemon stderr+stdout are teed to a file (truncated on each start) so voice-pipeline issues can be diagnosed without keeping a terminal open
- Voice preset selector with a Preview button that streams a sample through the existing daemon WebSocket channel
- Microphone input device picker on macOS (sourced via `system_profiler SPAudioDataType -json`, with a "Default" fallback on non-darwin)
- Wake word enable toggle and sensitivity slider (0.3–0.8)
- Audio transport selector (Local vs LiveKit) with LiveKit credential fields rendered only when the transport is LiveKit
- Notifications settings and a Browse button for adding scanner project root paths via the native folder picker
- Live Diagnostics panel that polls every 2s and surfaces 7 health rows: daemon status, microphone permission, mobile API, LLM chain, bundled models, Ollama, and disk usage — each with per-row Copy plus a master "Copy diagnostics" markdown export
- Config import/export and Reset to defaults from the Advanced tab, including a "Preserve API keys" toggle on reset and confirmation modals on destructive actions
- First-run onboarding modal walking new users through Welcome → LLM API key → microphone permission
- macOS microphone permission Wails bindings (`GetMicPermissionStatus`, `RequestMicPermission`) backed by an `AVCaptureDevice` cgo binding, with a non-darwin stub for cross-builds
- Apple Silicon startup guard: Jarvis exits with a clear stderr message when launched under Rosetta or on a non-arm64 host
- DMG build pipeline scripts: `fetch-python.sh` (pinned python-build-standalone CPython 3.13.13, SHA256-verified), `build-daemon-venv.sh` (relocatable venv with `@VENV_ROOT@` placeholder), `fetch-models.sh` with `LIGHT_BUNDLE` mode (bundles only the 4 MB voice preset; VibeVoice + Whisper weights download on first launch to stay under GitHub's 2 GiB release-asset limit), and a `post-build.sh` packaging step explicitly invoked by `.github/workflows/release.yml` (wails.json's `postBuildHooks` field is silently ignored by Wails v2)
- GitHub Actions `ci.yml` (full build + test on PR/push) and a `release.yml` skeleton for the DMG release pipeline
- macOS `Info.plist` permission usage strings (`NSMicrophoneUsageDescription`, `NSAppleEventsUsageDescription`), `LSMinimumSystemVersion=12.0`, `LSArchitecturePriority=arm64`, plus a hardened-runtime `entitlements.plist`
- Bundled-resource path helpers (`BundledResourcesDir`, `BundledPython`, `BundledDaemonScript`, `BundledModelsDir`) so the .app bundle can locate its Python venv, daemon script, and models at runtime
- README rewritten for OSS launch: 14 sections including pitch, install, system requirements, feature list, Settings walkthrough, privacy policy listing every cloud endpoint, troubleshooting, and license/status badges
- Manual smoke test plan at `docs/smoke-test.md` with 15 numbered steps from clean Mac → DMG mount → onboarding → voice loop → session control, plus failure-mode and reset-state appendices

### Changed
- **Go module renamed** from `awm` to `github.com/namanchopra/jarvis`
- **Data directory** moved from `~/.awm/` to `~/.jarvis/` (existing installs auto-migrate via `MigrateLegacyHome()`; `~/.awm` becomes a symlink for backward compat)
- **UI radically simplified to voice-only.** Removed the navigation rail and every secondary view (tasks, sessions, workflows, history, activity, dashboard, control-center, costs, session groups, ~50 component files). The app shell is now just the Jarvis HUD orb plus the Settings overlay reached via the floating gear button. Multi-repo workspace creation and session launching continue to work entirely through voice commands (`create_workspace`, `launch_session` tools).
- Python daemon (`scripts/jarvis-daemon/config.py`) migrated from `dex*` to `jarvis*` config keys with bidirectional backward-compat reads (legacy configs continue to work)
- Sanitized company-specific example names (`maya-web`, `auth-service`, `mumz-cosmos`, `Mumzworld`) from system prompts, regex docs, comments, and `SyncDotClaude` candidate paths
- Go-side `internal/config/config.go` migrated from `dex*` to `jarvis*` JSON keys with a custom `UnmarshalJSON` that reads both prefixes (`jarvis*` wins on collision) and a `MarshalJSON` that writes only the new keys — legacy on-disk configs continue to load without data loss
- HUD now always starts unmuted on launch; the prior-session mute state no longer persists across app restarts (legacy `jarvis-muted` localStorage key is best-effort cleared on mount). The in-session mute toggle (button + Cmd/Ctrl+Shift+M) still works as before
- LiveKit transport defaults to off on fresh installs, and LiveKit credential fields are `omitempty` so a default config no longer writes empty placeholder secrets to disk
- Silero VAD thresholds tightened (`confidence` 0.7 → 0.85, `start_secs` 1.0 → 1.5) so breath and tail noise no longer register as fresh user speech
- Voice pipeline reliability fixes: TTS prewarm on startup, more aggressive conversation context trimming, wake-gate listening window tightened from 30s to 6s with re-arm on bot-stop, STT n-gram repetition filter, and `TextFrame` forward from TTS into the assistant aggregator

### Fixed
- `TextFrame` outputs from TTS are now forwarded downstream so the assistant aggregator captures spoken responses — Jarvis no longer "forgets" what it just said when summarizing or following up on its own replies
- **Phantom VAD interruption silently killing replies before audio played.** A spurious `UserStartedSpeakingFrame` between LLM-start and TTS-start was triggering `broadcast_interruption()` and cancelling the in-flight reply. Fixed by passing `VADUserTurnStartStrategy(enable_interruptions=False)` + `TranscriptionUserTurnStartStrategy(enable_interruptions=False)` to the user aggregator and adding a 2.5s grace window inside `InterruptionHandler` so a phantom tick during the LLM→TTS handoff drops the frame instead of broadcasting an interruption
- VibeVoice no longer fails over to a missing fallback when the local `~/.jarvis/models/vibevoice/` directory contains only `voices/` (no weight file) — the model path resolver now checks for `model.safetensors` / `pytorch_model.bin` before redirecting `from_pretrained`, and falls back to the HuggingFace cache while still loading voices from the local dir
- Wails dev server (Vite proxy) no longer dies on regenerated bindings — fixed 7 `noUncheckedIndexedAccess` TS errors and removed 4 stale `@ts-expect-error` directives that broke under TypeScript strict mode after `wails generate module` ran
- Settings overlay now scrolls correctly — the wrapping container is a proper `flex flex-col` so `SettingsView`'s internal scroll area has bounded height

### Removed
- **Microsoft Edge TTS** removed entirely — `edge-tts` dependency, the standalone `tts.py` engine, the `EdgeTTSService` class in `pipecat_tts.py`, the `fallback_voice` constructor parameter on every other TTS service, and every `_synthesize_edge_fallback()` code path. VibeVoice, Kokoro, and Cartesia remain the available providers; on synthesis error the daemon now logs and stops instead of silently substituting a different voice
- ~50 frontend component and view files orphaned by the voice-only UI strip — `NavRail`, `SearchBar`, every `Tasks*`/`Sessions*`/`Workflows*`/`History*` view, all `Session*` and `Workflow*` and `Task*` components, `GitActionsPanel`, `DiffViewer`, `BroadcastPanel`, `NLCommandBar`, `RepoGroup`, `RecipeManager`, the `terminal/` subdirectory, and the no-longer-used `session-helpers` / `terminal-*` / `recipe-utils` lib modules
- 3 unrouted view components (`ControlCenterView`, `DashboardView`, `ActivityView`)
- 8 orphan UI components (`CostDashboard`, `JarvisOnboarding` + 3 siblings, `Layout`, `ProjectsPanel`, `SessionMiniOutput`, `SessionOutput`, `WorkspacePreview`, `JarvisMiniOrb`)
- 3 legacy setup scripts (`setup-vance.sh`, `setup-dex-v5.sh`, `setup-dex-v7.sh`)
- 43 internal planning documents from `plans/` directory (preserved at `~/Documents/jarvis-plans/`; active plans moved to in-repo `.local-plans/`)

### Known issues
- The v0.1.0 build is ad-hoc signed: on first launch macOS Gatekeeper shows "developer cannot be verified" and refuses to open the app. Workaround: right-click the app → **Open** → confirm. Subsequent launches proceed normally.
- Apple Silicon only — no Intel Mac builds are produced. The app fails fast with a clear message if launched on Intel or under Rosetta.
- The Expo mobile companion is not yet wired up to this release; the desktop mobile API server is running but no shipped client connects to it yet.
- First launch performs a one-time ~2.4 GB download of VibeVoice + Whisper model weights to `~/.cache/huggingface/`. The DMG itself ships at ~1.7 GB because GitHub release assets are capped at 2 GiB and the bundled model footprint exceeds that. Once the first-launch download completes, Jarvis runs fully offline.
