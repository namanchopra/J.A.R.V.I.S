# Jarvis

> A native macOS voice companion for orchestrating AI coding agents.

[![Latest release](https://img.shields.io/github/v/release/namanchopra/J.A.R.V.I.S?label=download&color=00e5ff)](https://github.com/namanchopra/J.A.R.V.I.S/releases/latest)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)
![Platform](https://img.shields.io/badge/platform-Apple%20Silicon-lightgrey)
![Status](https://img.shields.io/badge/status-pre--release-orange)

Jarvis is a desktop voice assistant that drives your AI coding agents the way you'd drive a junior engineer — by talking to them. Say "Hey Jarvis" to launch Claude Code, Kiro, Gemini, Codex, or Aider sessions across multiple repositories, dispatch work in parallel, and get notified when sessions need your attention. Built-in cross-session conflict detection warns you when two agents are about to step on each other's changes.

## Download

➜ **[Get the latest DMG](https://github.com/namanchopra/J.A.R.V.I.S/releases/latest)** · Apple Silicon Macs (M1 / M2 / M3 / M4) · macOS 12+ · ~2.5 GB (bundles a portable Python runtime + the VibeVoice and Whisper models — no first-run download required)

After downloading, see **[Install](#install)** below — the first launch needs a one-time Gatekeeper workaround because the build is ad-hoc signed.

## Demo

<!-- TODO: record and commit docs/demo.gif — short clip of "Hey Jarvis, start a session" flow -->
![Jarvis demo](docs/demo.gif)

## Install

> Apple Silicon (M1 or later) running macOS 12 or newer.

1. **Download** the latest `Jarvis-vX.Y.Z.dmg` from the [Releases](https://github.com/namanchopra/J.A.R.V.I.S/releases) page. The DMG is about 3.5 GB because it bundles a portable Python runtime and the VibeVoice + Whisper models — no first-run download required.
2. **Mount and install.** Open the DMG and drag **Jarvis.app** into `/Applications`.
3. **Get past Gatekeeper.** The first launch will be blocked with a *"developer cannot be verified"* warning. This is expected — the v0.1 build is ad-hoc signed because there is no Apple Developer ID attached yet, and Apple's notarization service requires one. To get past it:
   - Open `/Applications` in Finder.
   - **Right-click** Jarvis.app and choose **Open** (this is the magic that double-clicking will *not* do).
   - In the dialog that appears, click **Open** again.
   - macOS now remembers your choice; future launches work normally from Spotlight, Launchpad, or Dock.
4. **Grant mic access.** On first run Jarvis will prompt for **microphone permission**. Grant it — the wake-word listener and voice loop are dead without it. If you miss the prompt, re-enable it under **System Settings -> Privacy & Security -> Microphone -> Jarvis**.
5. **Onboarding.** On first launch you'll be walked through a short onboarding flow: pick an LLM provider, paste an API key (or confirm Ollama is running locally at `http://localhost:11434`), preview a voice, and you're ready to talk. Say "Hey Jarvis" to begin.

If macOS refuses to launch the app at all (rare; usually means the download tool stamped a stubborn quarantine attribute), strip the quarantine flag from a terminal:

```bash
xattr -d com.apple.quarantine /Applications/Jarvis.app
```

Then go back to step 3.

## System Requirements

- **CPU**: Apple Silicon — M1, M2, M3, M4 (Intel Macs are not supported in v0.1).
- **OS**: macOS 12 Monterey or later.
- **Disk**: ~3.5 GB for the installed `.app` (bundled Python runtime + VibeVoice TTS + Whisper STT models).
- **RAM**: 16 GB recommended. The bundled voice models fit happily in 8 GB, but you'll want headroom for running multiple agent sessions.
- **Microphone**: any built-in or external input device.

## Features

- 🎤 **"Hey Jarvis" wake word.** Always-listening, local, low-power wake detection via openWakeWord (with an optional Picovoice backend for users who prefer it). No cloud round-trip to start a conversation, no push-to-talk button to hunt for.
- **Multi-agent session management.** Launch and supervise sessions across five agents — **Claude Code**, **Kiro**, **Gemini CLI**, **Codex**, and **Aider** — side by side. Each agent has its own adapter (`internal/agent/<name>.go`) implementing a uniform `Launch / SendMessage / Stop / IsAvailable` interface, so the rest of the app treats them interchangeably.
- **Cross-session conflict detection (impact warnings).** Jarvis runs `git diff --name-only HEAD` for every active session in the background and raises three classes of warning: *shared-dependency* (two agents both modify `package.json` or `go.mod`), *shared-file* (overlap inside `shared/` or `common/` directories), and *API contract* (concurrent edits under `/api/` or `/routes/` directories). You see the conflict before the merge does.
- **Workspace virtualization.** Spin up a virtual monorepo at `~/.jarvis/workspaces/<name>/` that symlinks several real repos together, auto-generates a `CLAUDE.md` describing the workspace and cross-repo guidelines, and launches a single Claude Code session with `--add-dir` for every repo. One conversation, multiple codebases, no manual juggling.
- **Terminal integration.** Focus, send keystrokes, and read output from CMux workspaces, iTerm2 windows, and Terminal.app windows interchangeably — Jarvis picks the right provider automatically or honors the `preferredTerminal` config override.
- **Mobile companion (coming v0.2).** A Bearer-token-authenticated HTTP and WebSocket API is already live in the desktop build (Echo server on port 4422 by default) — the Expo mobile client that consumes it ships in v0.2.
- **Local-first voice pipeline.** STT via `mlx-whisper` running on Apple Silicon Metal and TTS via Microsoft VibeVoice Realtime 0.5B, both bundled into the DMG, both running entirely on your machine. No audio leaves the device.
- **Ollama path for fully offline operation.** Point Jarvis at a local Ollama instance (e.g. `ollama:qwen3:4b`) and the assistant can run end-to-end with zero external network calls — STT, LLM, and TTS all local.
- **Process auto-detection.** Jarvis scans the OS process table every five seconds (configurable) and auto-creates task records for any running agent process it recognizes, marking them done when the process exits.
- **Cost tracking.** Daily, monthly, and all-time Claude Code spend pulled straight from the local `~/.claude/projects/` JSONL logs — no API key needed, nothing transmitted.
- **Natural-language command bar.** Pure keyword-matching (no LLM round-trip) for fast queries like "show idle sessions", "stop session 3", "total cost this month".

## Settings

The Settings view in v0.1 is a full UI replacement for hand-editing `~/.jarvis/config.json`. It is laid out as five tabs:

- **Connections** — LLM provider selection and API key fields for OpenRouter, Google AI Studio, Anthropic, Cartesia, ElevenLabs, and Picovoice. Each key has a show/hide toggle and a one-click **Validate** button that hits the provider with a single-token test request and reports back with a green or red status pill. Keys are never logged.
- **Voice** — TTS provider (VibeVoice / Kokoro / Edge / Cartesia), voice preset with a ✨ **Preview** button, STT model, mic input device, wake-word toggle and sensitivity slider.
- **Behavior** — audio transport (Local Mac mic+speaker by default, LiveKit for advanced setups), ambient-mode toggle, notification toggles, project root paths for the process scanner.
- **Diagnostics** — live health panel: daemon status, mic permission state, mobile API port + token, LLM provider chain, bundled-model availability, Ollama reachability, disk usage of `~/.jarvis/`. Each row has a copy button, plus a **Copy diagnostics** master button that produces a markdown bundle for bug reports.
- **Advanced** — mobile API token regeneration, `dotClaudeSource` path picker, full config import/export, and a reset-to-defaults that can optionally preserve API keys.

<!-- TODO: capture docs/settings-walkthrough.png once Settings UI work (TASK-016..023) lands -->

## What works in v0.1

Everything in the **Features** list above is live in v0.1:

- Wake-word + voice loop end-to-end.
- All five agent adapters (Claude Code, Kiro, Gemini, Codex, Aider).
- Cross-session impact warnings.
- Virtual monorepo workspaces.
- CMux / iTerm2 / Terminal.app providers.
- Bundled VibeVoice + Whisper (no first-run model download required).
- Comprehensive Settings UI with provider validation.
- Ollama support for fully local operation.

## Known limitations

- **Apple Silicon only.** Intel Macs are not supported and the binary refuses to start under Rosetta 2.
- **Gatekeeper warning on first launch.** The DMG is ad-hoc signed because the project does not currently have an Apple Developer ID. See the [Install](#install) section for the right-click workaround. Notarization is on the v0.2 roadmap.
- **Mobile companion is not yet wired.** The Bearer-token HTTP API and WebSocket terminal stream are in the desktop build, but the Expo client ships in v0.2.
- **No auto-update.** v0.1 has no Sparkle integration — new versions are downloaded manually from Releases.
- **No telemetry.** Jarvis does not phone home. This is intentional for v0.1; an opt-in error reporter may land in v0.2.

## Privacy & Data

Jarvis is local-first. **All voice processing (STT and TTS) runs on your Mac by default** — audio never leaves the device unless you explicitly configure a cloud TTS provider.

The following are the only network endpoints Jarvis can ever talk to, and only when you have configured them:

| Endpoint | When it's called | How to disable |
|---|---|---|
| `openrouter.ai` (OpenRouter) | Default LLM provider for routing prompts | Switch LLM provider in Settings -> Connections |
| `generativelanguage.googleapis.com` (Google AI Studio) | When you select a `gemini-*` model | Switch LLM provider |
| `api.anthropic.com` (Anthropic) | When you select a `claude-*` model directly | Switch LLM provider |
| `huggingface.co` (HuggingFace) | **Only if** the bundled models in the .app are missing or corrupted — Jarvis will offer to redownload them on first run | Reinstall the DMG so bundled models are restored |
| `api.cartesia.ai` (Cartesia) | **Only if** you select Cartesia as your TTS provider | Switch TTS provider to VibeVoice or Kokoro |
| `api.elevenlabs.io` (ElevenLabs) | **Only if** you select ElevenLabs as your TTS provider | Switch TTS provider |
| `api.picovoice.ai` (Picovoice) | **Only if** you supply a Picovoice key for the wake-word engine | Leave the Picovoice key empty (Jarvis falls back to openWakeWord, which is fully local) |

**Fully offline path: Ollama.** Set the LLM provider to `ollama:<model>` (e.g. `ollama:qwen3:4b`) in Settings and Jarvis routes all language-model traffic through your local Ollama instance at `http://localhost:11434`. Combined with the default local STT and TTS, this configuration makes **zero external network calls** during normal operation.

API keys are stored in `~/.jarvis/config.json` (chmod 600 on first save). They are never sent anywhere except the corresponding provider, never logged, and never written to crash reports.

## Troubleshooting

The three issues most new users hit:

### 1. Mic permission denied

The voice loop will not start without mic access — wake-word detection, STT, and the entire conversation pipeline depend on it. Open **System Settings -> Privacy & Security -> Microphone**, find Jarvis in the list, and flip the switch on. If Jarvis is not in the list yet, click the wake-word indicator in the HUD once to retrigger the prompt. After granting permission you must fully quit Jarvis (Cmd-Q) and relaunch — macOS only re-reads permission grants on process start.

You can confirm the current permission state from **Settings -> Diagnostics -> Mic permission**, which polls the OS every two seconds.

### 2. Daemon won't start

Jarvis spawns a bundled Python daemon under the hood for STT, TTS, and the Pipecat voice pipeline. If it crashes you'll see "Daemon: stopped" in **Settings -> Diagnostics**. To investigate:

- Check the most recent file in `~/.jarvis/logs/` — daemon stderr and stdout are tailed there.
- Open `Console.app` and filter for `jarvis-daemon` to see Python tracebacks raised before the log handler attached.

Most common causes:

- A stale `~/.jarvis/jarvis-daemon-env/` left over from a development install of an earlier version. Delete the directory — the DMG ships its own Python and won't fall back to the dev venv if the bundled one is intact.
- Entitlements stripped after a manual `codesign` retry. Reinstall the DMG.
- macOS Software Update mid-install broke a system library Jarvis links against. Reboot and try again.

### 3. Voice is silent

If Jarvis hears you (you see waveform activity in the HUD) but doesn't talk back, you're almost certainly stuck on the LiveKit transport without valid credentials. The default for fresh installs is **Local (Mac mic + speaker)**, but upgraded configs from earlier dev builds sometimes carry over `useLiveKitTransport: true`. Fix:

- Open **Settings -> Behavior -> Audio transport** and switch to **Local (Mac mic + speaker)**, then restart the app.
- Or hand-edit `~/.jarvis/config.json` and confirm `"useLiveKitTransport": false`.

Also worth checking: your Mac's system audio output device. Jarvis plays TTS through the *system default* — if you've connected Bluetooth headphones since launching the app, switch them out of Jarvis or pick a fresh output device under **Settings -> Voice -> Output**.

## Development

Jarvis is a [Wails v2](https://wails.io) app: Go 1.25 backend, React 18 + Vite + Tailwind frontend, with a sidecar Python daemon (Pipecat + VibeVoice + mlx-whisper) spawned on demand for the voice pipeline.

```bash
# Run in dev mode (hot reload, dev venv for the daemon)
wails dev

# Production build (writes Jarvis.app to build/bin/)
wails build

# Backend tests
go test ./...

# Frontend tests
cd frontend && npm test
```

Data lives under `~/.jarvis/`:

| Path | Purpose |
|---|---|
| `~/.jarvis/awm.db` | SQLite database (tasks, sessions, workflows, cost snapshots) |
| `~/.jarvis/config.json` | App settings |
| `~/.jarvis/logs/` | Session and daemon logs |
| `~/.jarvis/workspaces/` | Virtual monorepo workspaces |

See [`CLAUDE.md`](CLAUDE.md) and the references under `.claude/claude-md-refs/` for the full architecture, dependency graph, adding-a-binding guide, and exports map.

## License

Licensed under the [Apache License 2.0](LICENSE). Copyright 2026 Naman Chopra.

## Contributing

Contributions are welcome. Bug reports, feature suggestions, and pull requests should go through [GitHub Issues](https://github.com/namanchopra/J.A.R.V.I.S/issues). For larger changes please open an issue first to discuss the approach — Jarvis is in pre-release and the internal interfaces are still in motion.

## Acknowledgments

Jarvis stands on the shoulders of an enormous amount of open-source work. In particular:

- **[VibeVoice](https://huggingface.co/microsoft/VibeVoice-Realtime-0.5B)** (Microsoft) — the realtime text-to-speech model that gives Jarvis its voice.
- **[mlx-whisper](https://github.com/ml-explore/mlx-examples/tree/main/whisper)** (mlx-community) — Apple Silicon-native Whisper STT.
- **[Pipecat](https://github.com/pipecat-ai/pipecat)** — the voice agent framework wiring STT, LLM, and TTS together inside the daemon.
- **[openWakeWord](https://github.com/dscripka/openWakeWord)** — local, fully-offline wake-word detection.
- **[Wails](https://wails.io)** — Go + WebView native app framework that makes the desktop binary feel at home on macOS.
