# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

### Changed

### Fixed

### Removed

## [0.2.2] - 2026-05-20

### Fixed
- **Daemon now boots after first-launch setup.** `StartJarvis` was launching the daemon with the BASE CPython interpreter at `~/.jarvis/python/bin/python3` instead of the uv-managed venv at `~/.jarvis/jarvis-daemon-env/bin/python` — the base interpreter has no site-packages, so the daemon crashed instantly with `ModuleNotFoundError: No module named 'pipecat'` and Jarvis couldn't pick up the user's voice. Resolution order is now venv → base → bundled → dev, with the venv winning whenever it exists. Added `paths.InstalledDaemonVenvPython()` + a regression test (`TestStartJarvis_PrefersDaemonVenv_OverBaseInstalled`) so this can't silently regress.
- **SetupScreen shows phase 1 progress immediately.** `install-daemon.sh` now emits `PHASE: python_install` + `PHASE_PROGRESS: 0` at the top of preflight, so the UI shows "Python — in progress" the moment setup starts instead of all four phases sitting in "pending" while preflight runs. Without this the install felt frozen.
- **Granular preflight diagnostics.** Each preflight step (arch, Xcode CLT, disk space, PATH tools, uv binary, daemon source) now writes a per-check log line to `~/.jarvis/logs/setup.log` before running. A future hang during preflight will be diagnosable from the log without re-running the installer.
- **Defensive stdout drain in the spawn step.** `defaultSetupSpawner` now sets `cmd.Stdout = io.Discard` explicitly. Wails GUI launches inherit nil stdout (routed to `/dev/null` by exec.Cmd) so no observable change in production, but pinning it removes a class of pipe-fill hangs that would have shown up if any future runner shell pre-attached an unread pipe to stdout.

## [0.2.1] - 2026-05-19

### Added
- **Signed + notarized DMG.** v0.2.1 ships with a Developer ID signature on every Mach-O binary (Jarvis itself + bundled `uv`), hardened runtime, secure timestamps, and a stapled Apple notarization ticket. Double-clicking the DMG no longer triggers Gatekeeper warnings.
- **Drag-to-Applications DMG layout.** Mounting the DMG now shows the Jarvis icon on the left and an Applications folder shortcut on the right — the standard macOS install gesture. Set via `create-dmg --app-drop-link` flags.

### Changed
- Website install steps (`website/app/page.tsx`) dropped the obsolete "right-click → Open" workaround now that the DMG is notarized; install flow is now 4 steps instead of 5.

### Fixed
- Codesign step in `release.yml` re-signs every Mach-O executable under `Resources/` (not just `*.dylib`/`*.so`), so the bundled `uv` binary inherits our team's Developer ID + hardened runtime + secure timestamp. Without this, Apple notarization rejected v0.2.0's first DMG with `4000: Archive contains critical validation errors`.
- Notarize step in `release.yml` now captures the submission ID, polls to completion, and unconditionally fetches `notarytool log <id>` — on rejection the workflow output now contains Apple's per-binary failure reasons instead of dying with a bare exit code.

## [0.2.0] - 2026-05-18

### Changed
- **Setup-on-launch architecture.** The DMG no longer bundles the portable Python interpreter or daemon venv — both install into `~/.jarvis/` on first launch, behind a full-screen progress UI. DMG shrinks from ~356 MB to ~80 MB; CI build time drops from ~10 min to ~3 min. Fresh installs work without Gatekeeper-quarantine cascade issues that broke v0.1.6 on colleague machines.
- StartJarvis (TASK-001) routes around a bundled Python entirely: on launch it looks up `~/.jarvis/python/bin/python3` + `~/.jarvis/venv/bin/python` and only spawns the daemon when both are present and the sentinel verifies. If anything is missing/stale, it triggers the install flow before the orb mounts.
- Quarantine self-heal (TASK-001) now runs as a startup pass over `~/.jarvis/` rather than only over `Contents/Resources/` — keeps the runtime usable even if a future asset gets quarantined by a download tool.
- HuggingFace download events from the daemon are re-emitted on the new `setup` Wails channel during the install flow (TASK-007), so VibeVoice + Whisper download progress renders inside the same SetupScreen UI as Python install and venv install — no separate first-run overlay handoff.
- Daemon prefetch sequencing reordered (TASK-009) — VibeVoice + Whisper downloads now run synchronously before the Pipecat pipeline initializes, so the SetupScreen reports completion only when the daemon is genuinely ready to take a `Hey Jarvis`.
- `App.tsx` (TASK-012) is now a setup gate. It calls `IsSetupComplete()` on mount, renders `<SetupScreen>` until completion, then swaps to the HUD without page reload thanks to a live `EventsOn('setup')` listener on the gate.
- Settings → Diagnostics gains a "Setup" row (TASK-015) showing sentinel version + last-verified timestamp + a "Re-run setup" button that deletes `~/.jarvis/.setup-version-*` and relaunches the app to force a fresh install.
- `daemon_supervisor.go` (TASK-006) waits for the setup sentinel to be present before its first daemon spawn — previous startup-race fixes that polled for `python-venv` are obsolete and removed.
- `model_status.py` (TASK-007) gained a `--emit-channel=setup` mode used only during the install bootstrap; in normal daemon operation it continues to emit on the existing `jarvis` channel.
- `release.yml` builds the DMG without the bundled Python + venv steps (TASK-013) — the DMG produced by the workflow is now ~80 MB on every release. Older `LIGHT_BUNDLE` plumbing kept in tree but unused (still gates the voice preset).
- README + website + smoke test docs (TASK-018) rewritten for the new install flow.

### Added
- New `<SetupScreen>` full-viewport component (TASK-011) — corner-bracketed panel with four phase rows (Installing Python runtime / Installing voice pipeline / Downloading VibeVoice / Downloading Whisper), each with progress bar + ETA + state glyph (◌ / ◉ / ✕ / ◯). Borrowed visual vocabulary from v0.1.x's FirstRunDownloadOverlay.
- `useSetupState` React hook (TASK-010) subscribing to the new `'setup'` Wails channel.
- Go-side `setup` package (TASK-002, TASK-008) with `SentinelData` + atomic `WriteSentinel` + `ReadSentinel` with version + sha verification.
- `scripts/setup/install-daemon.sh` orchestrator (TASK-004) — runs phases 1+2 with structured stderr (`PHASE:` / `PHASE_PROGRESS:` / `PHASE_BYTES:` / `PHASE_ETA:` / `PHASE_DONE:` / `PHASE_ERROR:`) the Go side parses.
- `model_setup → setup` channel bridge (TASK-007) — daemon's existing HF download events for VibeVoice + Whisper are re-emitted on the setup channel during the install flow, so SetupScreen renders all 4 phases uniformly.
- `App.tsx` setup gate (TASK-012) with `EventsOn('setup')` listener — mid-session setup completion flips the gate without page reload.
- `OpenSetupLog()` Wails binding (TASK-016) for the SetupScreen's "View setup log" footer link.
- Quarantine strip in StartJarvis (TASK-001) — even with the lean v0.2.0 DMG, any nested bundled binary that gets quarantined by Gatekeeper is self-healed before exec.
- New `install-smoke.yml` CI workflow (TASK-017) — runs the install script standalone on a clean macos-14 runner on every PR that touches setup scripts.
- Setup events schema contract (TASK-003) at `docs/setup-events.md` — canonical reference for Go, Python, and React consumers.

### Removed
- Bundled `~/.app/Contents/Resources/python/` (the portable CPython tree)
- Bundled `~/.app/Contents/Resources/python-venv/` (the daemon venv)
- CI workflow's "Fetch python-build-standalone" and "Build relocatable daemon venv" steps (TASK-013)

### Notes
- **First-launch time**: ~10–15 min on home internet (Python install ~30s + venv install ~1 min + VibeVoice download ~5–7 min + Whisper download ~2–3 min). One-time. After that, fully offline except chosen LLM provider.
- **Sentinel file**: `~/.jarvis/.setup-version-0.2.0`. If a future v0.2.1 changes the install flow, the sentinel version bumps and existing users re-run the setup automatically.
- **Resumability**: per-phase sentinels make every install resumable. Quit mid-VibeVoice-download → next launch resumes from where it stopped.
- **Migration from v0.1.6**: existing users with `~/.cache/huggingface/` populated (from v0.1.1+'s prefetch) skip the model download phases. New users go through the full install. `~/.jarvis/config.json` is preserved across the upgrade — no API keys or settings are lost.

## [0.1.6] - 2026-05-13

### Changed
- **OpenRouter is now the source of truth for cloud LLM routing.** Picking any cloud model in the Connections panel dropdown (`google/gemini-2.5-flash`, `anthropic/claude-haiku-4-5`, `openai/gpt-4o-mini`) routes the request through OpenRouter's `https://openrouter.ai/api/v1` endpoint using `jarvisAPIKey` (must start with `sk-or-`). Previously each prefix routed to its respective vendor's direct API, requiring three separate keys.
- The dropdown labels now suffix each cloud option with `(via OpenRouter)` so the routing is visible at a glance.
- `LLM_OPTIONS` in ConnectionsPanel: all cloud options now declare `requiresProvider: 'openrouter'`, so the per-option availability indicator (`✓ available` / `⚠ needs openrouter key`) is uniform and accurate.
- **Connections panel collapsed from 6 API-key fields to 2.** Only **OpenRouter** (one key unlocks every cloud LLM) and **Cartesia** (the one optional paid voice) remain visible. Google + Anthropic key fields removed from the UI (dead now that OpenRouter routes everything). ElevenLabs + Picovoice fields removed (legacy from earlier designs, not wired to anything current). The Config struct still carries all those fields so existing on-disk configs load cleanly — they're just no longer surfaced in Settings.

### Removed
- The Google-direct and Anthropic-direct branches in `llm_picker.py`. They were ~50 lines of provider-specific construction code that became dead weight once OpenRouter was the canonical path. The user-pick LLM construction collapsed from three branches to one (plus Ollama).
- The `_ANTHROPIC_MODEL_DATE_SUFFIX` mapping is gone — OpenRouter resolves `anthropic/claude-haiku-4-5` to the current dated build server-side.

### Fixed
- **First-run download overlay was invisible to new users.** The daemon emitted its first `model_setup state=downloading` event ~1-2s before the React HUD's WS connection was established. The HUD missed that event and never mounted the FirstRunDownloadOverlay — fresh DMG users stared at the orb in silence for 5-10 min while VibeVoice + Whisper downloaded in the background. Fixed by caching the latest `model_setup` payload in `model_status.py` and adding a `request_model_setup` handler the HUD fires on mount, mirroring the `request_pipeline_status` pattern from v0.1.5.

### Notes
- **Backward compatibility preserved.** Users on v0.1.5 with an `sk-ant-` direct key or a `googleAPIKey` continue to work via the legacy auto-detect chain (untouched) as long as they haven't set an explicit `cfg.llmModel` in Settings. The user-pick path (cfg.llmModel set) is now OpenRouter-only.
- If you previously had `cfg.llmModel = "google/gemini-2.5-flash"` and only a `googleAPIKey` set: pick the model again with an `sk-or-` key in `jarvisAPIKey` and Apply, OR clear `cfg.llmModel` and let the legacy auto-detect path route to Google direct.

## [0.1.5] - 2026-05-13

### Added
- **LLM model dropdown now actually persists.** The "LLM Model" picker in Settings → Connections was stored in `useState` and dropped on every app restart — same class of bug we fixed for TTS/STT in v0.1.2 but missed for the LLM. New `LlmModel` field on the Go `Config` struct, included in `daemonRestartNeeded`, so swapping models in the dropdown now triggers the amber "DAEMON RESTART REQUIRED" banner and applies on restart.
- New daemon module `scripts/jarvis-daemon/llm_picker.py` that parses the dropdown's prefix-style values (`google/...`, `anthropic/...`, `openai/...`, `ollama:...`) and instantiates the right Pipecat service against the right base URL with the right credentials. Missing creds → warning log + graceful fallback to legacy key-driven detection. 14 new pytest cases.
- Daemon logs the resolved LLM choice at INFO on startup: `LLM (user-pick): anthropic → claude-haiku-4-5-20251001 | source: cfg.llmModel`.
- **Live pipeline status on the HUD and in Diagnostics.** The 4 floating labels around the orb used to lie (hardcoded `STT::WHISPER-SMALL.EN` etc. regardless of config). Now they show the resolved choices: top-left `LLM::<model> ◆` (the diamond appears when the user picked the model explicitly vs key-detected), top-right `STT::<MODEL>`, bottom-left `TTS::<PROVIDER>`, bottom-right `SESSIONS::<n>`. Empty-dash fallback before any event arrives.
- New `pipeline_status` daemon event emitted once per pipeline build and on demand via inbound `request_pipeline_status` message. Payload includes resolved provider/model for LLM (with `source: user-pick | key-detected`), STT model, TTS provider + voice id, and wake-word enabled/sensitivity. Allowlisted in the Go WS passthrough.
- Settings → Diagnostics now has a **Voice Pipeline** row mirroring the orb labels with a "last updated Xs ago" stamp + a "Request now" button for late-mounting clients. Includes 17 daemon + 31 HUD pytest/vitest cases.
- New React hook `usePipelineStatus()` (`frontend/src/lib/use-pipeline-status.ts`) — wraps `EventsOn('jarvis')` with a runtime type guard and exposes `{ status, receivedAt, refresh }`. Both the HUD orb labels and the Diagnostics row consume it.

### Fixed
- **OpenRouter key set in Settings was silently ignored.** The daemon's LLM selector read `config.get("dexAPIKey")` directly at three call sites in `main.py`. The frontend writes to `jarvisAPIKey` (the current key name), and the `dexAPIKey` legacy key is never updated — so a fresh key set in Settings never reached the daemon. Patched all three call sites to use the existing `get_api_key()` helper which prefers `jarvisAPIKey` and falls back to `dexAPIKey` for pre-rename configs.

### Changed
- Go WS passthrough at `internal/api/handlers_jarvis_ws.go` now also forwards `pipeline_status` events from the daemon to the React HUD (previously the type-routed switch dropped them on the floor).

## [0.1.2] - 2026-05-12

### Added
- **Runtime reconfiguration without quitting.** Eight settings were stored in component-local React state in v0.1.1 and never persisted to disk: `ttsProvider`, `sttModel`, `voicePreset`, `micInputDevice`, `wakeWordEnabled`, `googleAPIKey`, `anthropicAPIKey`, `cartesiaAPIKey`. They now live in `~/.jarvis/config.json` and survive app restarts.
- **TTS provider switcher actually switches.** The Python daemon now reads `cfg.ttsProvider` and instantiates the chosen TTS service (VibeVoice / Kokoro / Cartesia) rather than always running the hardcoded fallback chain. Same for `cfg.sttModel` (whisper-small.en / whisper-tiny.en / faster-whisper), `cfg.voicePreset`, `cfg.micInputDevice`, and `cfg.wakeWordEnabled`. Missing or invalid values fall back gracefully with a warning log.
- **"Apply now / Apply later" banner.** When the user changes a daemon-relevant setting and clicks Save, the settings overlay surfaces an amber **▸ DAEMON RESTART REQUIRED** banner with two buttons: **Apply now** triggers a new `RestartJarvis()` Wails binding that gracefully stops and relaunches the Python daemon (~3s blip); **Apply later** dismisses the banner so the changes take effect on the next manual quit/relaunch.
- New Wails bindings: `RestartJarvis() error` and `DaemonRestartNeeded(old, next Config) bool`.
- New `config.SaveResult` struct returned by `SaveConfig` — exposes `daemonRestartNeeded` so the frontend knows when to surface the banner.
- Daemon-side input device resolution: `cfg.micInputDevice` (a human-readable device name from `GetAudioInputDevices`) maps to a PyAudio index via exact-then-substring match. Unmatched names fall back to the system default.
- `cfg.wakeWordEnabled === false` enables true always-listening mode: the `WakeWordGate` processor is omitted from the pipeline entirely, the mic feeds STT directly.
- 23 new Python tests for the daemon's v0.1.2 config accessors and 20 new vitest source-level tests for the frontend cfg-migration + restart banner.

### Changed
- `SaveConfig(cfg)` now returns `config.SaveResult` (`{ daemonRestartNeeded: bool }`) instead of just an error. The internal mobile API caller discards the result.
- `internal/api/handlers_settings.go`'s `SettingsProvider.SaveConfig` interface signature updated to match.

### Fixed
- Frontend settings panels were storing user choices in `useState` instead of `cfg`. Every "save" was silently dropping eight fields on the floor. Promoted to cfg-backed reads/writes; user picks now survive app restarts.

## [0.1.1] - 2026-05-12

### Added
- **First-run download UI.** On first launch the daemon downloads ~2.4 GB of HuggingFace model weights (VibeVoice + Whisper). v0.1.0 did this silently — a first-time user would say "Hey Jarvis" and get 5–10 min of dead air, then quit thinking the app was broken. v0.1.1 mounts a full-viewport overlay (corner-bracketed panel, SF Mono labels, scanline-textured progress bars) showing per-model progress, speed, ETA, and total bytes. Auto-dismisses when ready.
- `OpenDaemonLog()` Wails binding — opens `~/.jarvis/logs/daemon.log` in the user's default text editor. The first-run overlay exposes it via a `▸ VIEW DAEMON LOG →` link so users can self-diagnose if a download hangs.
- Per-model retry button on download error. Sends a `retry_model_download` message back to the daemon, which restarts the download for that one model.
- New Python module `scripts/jarvis-daemon/model_status.py` owning HuggingFace cache detection (`try_to_load_from_cache`), a throttled tqdm subclass that emits `model_download` events, and a serial prefetch orchestrator that runs at daemon startup. The VibeVoice / Kokoro / Whisper services all await `model_status.ensure_model(...)` before their lazy-load paths, so the overlay can render progress while a model warms up.
- 9 pytest cases for `model_status` (cache hits, progress event shape, error events, full-schema validation per variant) and 24 source-level vitest cases for `FirstRunDownloadOverlay` (aria attrs, corner brackets, state icons, retry payload shape, the daemon-log link guard).

### Changed
- `internal/api/handlers_jarvis_ws.go` — the daemon-WS → Wails passthrough was type-routed with a drop-on-unknown default. Added a case forwarding `model_download` + `model_setup` events as full JSON payloads so future schema additions reach the frontend without struct changes.

### Fixed
- Long-tail UX regression introduced by the light-bundle DMG: the silent download window is no longer silent.

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
