# Jarvis

> A native voice companion for orchestrating AI coding agents. Runs on macOS and Windows.

[![Latest release](https://img.shields.io/github/v/release/namanchopra/J.A.R.V.I.S?label=download&color=00e5ff)](https://github.com/namanchopra/J.A.R.V.I.S/releases/latest)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)
![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Windows-lightgrey)
![Status](https://img.shields.io/badge/status-pre--release-orange)

Jarvis is a desktop voice assistant that drives your AI coding agents the way you'd drive a junior engineer — by talking to them. Say "Hey Jarvis" to launch Claude Code, Kiro, Gemini, Codex, or Aider sessions across multiple repositories, dispatch work in parallel, and get notified when sessions need your attention. Built-in cross-session conflict detection warns you when two agents are about to step on each other's changes.

## Download

Pick the installer for your OS — both download from the same [GitHub Releases](https://github.com/namanchopra/J.A.R.V.I.S/releases/latest) page:

- **macOS** (Apple Silicon, M1 / M2 / M3 / M4, macOS 12+): `Jarvis-<version>.dmg` (~35 MB). Signed with an Apple Developer ID and notarized — no Gatekeeper warnings.
- **Windows** (Windows 10 / 11, x64 or arm64): `Jarvis-Setup-<version>.exe` (~40 MB). Inno Setup installer, code-signed — no SmartScreen "Unknown publisher" warning. WebView2 runtime auto-installs on Win10 if missing.

**First launch** runs a one-time setup (~10–15 min) that installs a portable Python runtime + daemon venv into `~/.jarvis/` (macOS) or `%USERPROFILE%\.jarvis\` (Windows) and downloads the voice model weights. A full-screen progress UI tracks all four phases. After setup, Jarvis runs fully offline except for your chosen cloud LLM.

## Demo

<!-- TODO: record and commit docs/demo.gif — short clip of "Hey Jarvis, start a session" flow -->
![Jarvis demo](docs/demo.gif)

## Install

### macOS

> Apple Silicon (M1 or later) running macOS 12 or newer.

1. **Download** the latest `Jarvis-vX.Y.Z.dmg` from the [Releases](https://github.com/namanchopra/J.A.R.V.I.S/releases) page. The DMG is ~35 MB — signed with an Apple Developer ID and notarized.
2. **Mount and install.** Open the DMG, drag **Jarvis.app** onto the **Applications** folder shortcut inside the DMG window.
3. **Double-click Jarvis** in `/Applications`. No Gatekeeper warning, no right-click workaround — the notarization is stapled to the DMG.
4. **First-launch setup runs automatically** (~10–15 min, one-time). A full-screen progress UI shows four install phases — Installing Python runtime, Installing voice pipeline, Downloading VibeVoice (~1.9 GB), Downloading Whisper (~460 MB). You can keep using your Mac while it runs. The whole install lives in `~/.jarvis/` plus `~/.cache/huggingface/`; nothing else on the system is touched.
5. **Grant microphone permission** when prompted.
6. **Onboarding modal** walks you through picking an LLM provider and pasting an API key (or confirming Ollama is running locally), then previews a voice. Say "Hey Jarvis" to begin.

**macOS prerequisites**

- **CPU**: Apple Silicon — M1, M2, M3, M4 (Intel Macs are not supported).
- **OS**: macOS 12 Monterey or later.
- **Disk**: ~35 MB for the installed `.app`, plus **4 GB free for the first-launch install** (Python venv + VibeVoice + Whisper model weights) staged into `~/.jarvis/` and `~/.cache/huggingface/`.
- **RAM**: 16 GB recommended. The bundled voice models fit happily in 8 GB, but you'll want headroom for running multiple agent sessions.
- **Microphone**: any built-in or external input device.

### Windows

> Windows 10 or 11, x64 or arm64.

1. **Download** the latest `Jarvis-Setup-vX.Y.Z.exe` from the [Releases](https://github.com/namanchopra/J.A.R.V.I.S/releases) page. The installer is ~40 MB — code-signed, so no SmartScreen "Unknown publisher" warning.
2. **Run the installer.** Double-click `Jarvis-Setup-vX.Y.Z.exe` and follow the wizard. A Start Menu shortcut is created automatically; the desktop shortcut is optional. Installs to `%LOCALAPPDATA%\Programs\Jarvis\` by default.
3. **Launch Jarvis** from the Start Menu (or the optional desktop shortcut).
4. **WebView2 runtime.** On Windows 11 this is preinstalled. On Windows 10 the installer detects WebView2 and silently installs the Evergreen runtime bootstrapper if missing. On offline machines the installer falls back to a clear "manual download required" prompt with a link.
5. **First-launch setup runs automatically** (~10–15 min, one-time). The same four-phase progress UI as macOS — Python runtime, voice pipeline venv (uv + pip), faster-whisper STT model, and the TTS voice model — staged into `%USERPROFILE%\.jarvis\`.
6. **Grant microphone permission** when prompted (Windows Settings → Privacy & Security → Microphone → Jarvis).
7. **Onboarding modal** walks you through picking an LLM provider, pasting an API key, and previewing a voice. Say "Hey Jarvis" to begin.

**Windows prerequisites**

- **CPU**: x64 (Intel / AMD) or arm64 (Snapdragon X Elite, Surface Pro / Laptop arm64 models). 32-bit Windows is not supported.
- **OS**: Windows 10 (version 1809+) or Windows 11. WebView2 Evergreen runtime — preinstalled on Win11, auto-installed by the Jarvis installer on Win10.
- **Disk**: ~40 MB for the installed program, plus **4 GB free for the first-launch install** (Python venv + faster-whisper + TTS model weights) staged into `%USERPROFILE%\.jarvis\` and the HuggingFace cache.
- **RAM**: 16 GB recommended. faster-whisper on CPU works in 8 GB; CUDA-accelerated STT (optional, NVIDIA only) reduces RAM pressure.
- **Microphone**: any built-in or USB input device.
- **No Apple Developer fee equivalent on Windows.** The Inno Setup installer is signed with a standard Authenticode certificate; no per-developer recurring fee like the $99/yr Apple Developer Program.

### Installing via winget (optional, Windows only)

Once a winget manifest is published you can install Jarvis from PowerShell with:

```powershell
winget install --id NamanChopra.Jarvis
```

This downloads and runs the same signed `Jarvis-Setup-<version>.exe` as the manual install above. Useful for scripted setups or fleet deployment.

## Features

- 🎤 **"Hey Jarvis" wake word.** Always-listening, local, low-power wake detection via openWakeWord (with an optional Picovoice backend for users who prefer it). No cloud round-trip to start a conversation, no push-to-talk button to hunt for.
- **Spotify control.** Voice-driven Spotify playback. Local AppleScript playback for any Spotify Free user with the desktop client running; Spotify Web API search for "play X by name". Tools: `spotify_search_and_play`, `spotify_pause`, `spotify_resume`, `spotify_skip`, `spotify_previous`, `spotify_what_is_playing`, `spotify_set_volume`, `spotify_like_current`, `spotify_queue`.
- **Mac control.** System-wide voice control via 15 `macctl` tools: open/quit/focus apps, set volume/brightness, take screenshots, clipboard, run Shortcuts.app shortcuts. Per-tool permissions (`allow` / `ask` / `deny`) gate destructive actions; the daemon asks confirmation via voice when policy = `ask`.
- **Friday mobile companion.** Expo-Go-scannable React Native app that relays voice to the Mac's Jarvis. Push-to-talk on the orb, WebSocket transport, QR pairing. No App Store, no developer signing — install Expo Go, scan a QR, done. See [/friday](https://jarvis.namanchopra.dev/friday) for the install QR.
- **Multi-agent session management.** Launch and supervise sessions across five agents — **Claude Code**, **Kiro**, **Gemini CLI**, **Codex**, and **Aider** — side by side. Each agent has its own adapter (`internal/agent/<name>.go`) implementing a uniform `Launch / SendMessage / Stop / IsAvailable` interface, so the rest of the app treats them interchangeably.
- **Cross-session conflict detection (impact warnings).** Jarvis runs `git diff --name-only HEAD` for every active session in the background and raises three classes of warning: *shared-dependency* (two agents both modify `package.json` or `go.mod`), *shared-file* (overlap inside `shared/` or `common/` directories), and *API contract* (concurrent edits under `/api/` or `/routes/` directories). You see the conflict before the merge does.
- **Workspace virtualization.** Spin up a virtual monorepo at `~/.jarvis/workspaces/<name>/` that symlinks several real repos together, auto-generates a `CLAUDE.md` describing the workspace and cross-repo guidelines, and launches a single Claude Code session with `--add-dir` for every repo. One conversation, multiple codebases, no manual juggling.
- **Terminal integration.** Focus, send keystrokes, and read output from CMux workspaces, iTerm2 windows, and Terminal.app windows interchangeably — Jarvis picks the right provider automatically or honors the `preferredTerminal` config override.
- **Mobile companion (Friday).** Friday is the v0.3 phone client: a Bearer-token-authenticated HTTP and WebSocket bridge to the Mac (Echo server on port 4422 by default) plus an Expo Go React Native app you install by scanning a QR. See the **Friday Mobile** section below.
- **Local-first voice pipeline.** STT via `mlx-whisper` running on Apple Silicon Metal and TTS via Microsoft VibeVoice Realtime 0.5B, both bundled into the DMG, both running entirely on your machine. No audio leaves the device.
- **Ollama path for fully offline operation.** Point Jarvis at a local Ollama instance (e.g. `ollama:qwen3:4b`) and the assistant can run end-to-end with zero external network calls — STT, LLM, and TTS all local.
- **Process auto-detection.** Jarvis scans the OS process table every five seconds (configurable) and auto-creates task records for any running agent process it recognizes, marking them done when the process exits.
- **Cost tracking.** Daily, monthly, and all-time Claude Code spend pulled straight from the local `~/.claude/projects/` JSONL logs — no API key needed, nothing transmitted.
- **Natural-language command bar.** Pure keyword-matching (no LLM round-trip) for fast queries like "show idle sessions", "stop session 3", "total cost this month".

## Settings

The Settings view is a full UI replacement for hand-editing `~/.jarvis/config.json`. It is laid out as five tabs:

- **Connections** — LLM provider selection and API key fields for OpenRouter, Google AI Studio, Anthropic, Cartesia, ElevenLabs, and Picovoice. Each key has a show/hide toggle and a one-click **Validate** button that hits the provider with a single-token test request and reports back with a green or red status pill. Keys are never logged.
- **Voice** — TTS provider (VibeVoice / Kokoro / Edge / Cartesia), voice preset with a ✨ **Preview** button, STT model, mic input device, wake-word toggle and sensitivity slider.
- **Behavior** — audio transport (Local Mac mic+speaker by default, LiveKit for advanced setups), ambient-mode toggle, notification toggles, project root paths for the process scanner.
- **Diagnostics** — live health panel: daemon status, mic permission state, mobile API port + token, LLM provider chain, bundled-model availability, Ollama reachability, disk usage of `~/.jarvis/`. Each row has a copy button, plus a **Copy diagnostics** master button that produces a markdown bundle for bug reports.
- **Advanced** — mobile API token regeneration, `dotClaudeSource` path picker, full config import/export, and a reset-to-defaults that can optionally preserve API keys.

<!-- TODO: capture docs/settings-walkthrough.png once Settings UI work (TASK-016..023) lands -->

## Friday Mobile

Friday is the v0.3 phone companion. Press-and-hold the orb to talk; release to send. Audio relays over WebSocket to the Jarvis daemon running on your desktop — the phone is a mic + speaker, the brain stays on your computer. **The pair host can be a Mac or a Windows PC** — both run the same Echo server on port 4422, so Friday's pairing flow is identical.

**Install:** open https://jarvis.namanchopra.dev/friday on a computer, scan the QR with Expo Go on your phone, done. No Apple Developer account, no Play Store listing.

**Pair:** in Jarvis on your Mac or Windows PC, Settings → Connections → "Connect Friday phone". Scan the resulting QR with Friday. Pairing persists.

## What works today

Everything in the **Features** list above is live:

- Wake-word + voice loop end-to-end.
- All five agent adapters (Claude Code, Kiro, Gemini, Codex, Aider).
- Cross-session impact warnings.
- Virtual monorepo workspaces.
- CMux / iTerm2 / Terminal.app providers.
- VibeVoice + Whisper running locally (downloaded once on first launch via the setup UI, then fully offline).
- Comprehensive Settings UI with provider validation.
- Ollama support for fully local operation.
- Signed + notarized DMG — no Gatekeeper friction on install.

## Known limitations

- **macOS: Apple Silicon only.** Intel Macs are not supported and the binary refuses to start under Rosetta 2.
- **Windows: x64 and arm64 only.** 32-bit Windows is not supported. STT on Windows uses faster-whisper (CTranslate2) instead of MLX Whisper; CPU works everywhere, CUDA acceleration requires an NVIDIA GPU + matching drivers.
- **Friday mobile is push-to-talk only.** No wake-word on the phone in v0.3 — you press the orb to record, release to send. Wake-word relay over WebSocket is on the roadmap.
- **No auto-update.** No Sparkle / Squirrel integration yet — new versions are downloaded manually from Releases (or `winget upgrade` on Windows). On the roadmap.
- **No telemetry.** Jarvis does not phone home. This is intentional; an opt-in error reporter may land later.

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

The voice loop will not start without mic access — wake-word detection, STT, and the entire conversation pipeline depend on it.

- **macOS**: open **System Settings → Privacy & Security → Microphone**, find Jarvis in the list, and flip the switch on. If Jarvis is not in the list yet, click the wake-word indicator in the HUD once to retrigger the prompt. After granting permission you must fully quit Jarvis (Cmd-Q) and relaunch — macOS only re-reads permission grants on process start.
- **Windows**: open **Settings → Privacy & security → Microphone**, ensure "Microphone access" is on and Jarvis is enabled. Jarvis surfaces a deep link (`ms-settings:privacy-microphone`) from Settings → Diagnostics that jumps straight to the right page.

You can confirm the current permission state from **Settings → Diagnostics → Mic permission**, which polls the OS every two seconds.

### 2. Daemon won't start

Jarvis spawns a bundled Python daemon under the hood for STT, TTS, and the Pipecat voice pipeline. If it crashes you'll see "Daemon: stopped" in **Settings → Diagnostics**. To investigate:

- **macOS**: check the most recent file in `~/.jarvis/logs/` — daemon stderr and stdout are tailed there. Or open `Console.app` and filter for `jarvis-daemon` to see Python tracebacks raised before the log handler attached.
- **Windows**: check the most recent file in `%USERPROFILE%\.jarvis\logs\` (the same layout, just under the Windows home directory). Event Viewer → Windows Logs → Application catches early-startup crashes raised before the log handler attached.

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

Data lives under `~/.jarvis/` on macOS and `%USERPROFILE%\.jarvis\` on Windows (paths below use the macOS form for brevity):

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
