# Jarvis v0.1.0 — Manual Smoke Test

## Overview

This document is the end-to-end manual test plan for verifying a Jarvis release DMG. It is intended to be run by a release engineer (or any contributor) immediately before tagging a final `v0.1.0` build, and is the formal acceptance procedure for TASK-031 of the Phase 2 DMG plan. The goal is to prove that a person who has never seen Jarvis can download the DMG, install it, and reach a working voice loop without reading source code or hand-editing config files. Each numbered step must pass on a clean Mac (or freshly created user account) before the release is shipped.

The test specifically validates the foundation fixes (TASK-001, TASK-002), the bundled-resources spawn path (TASK-011, TASK-015), the Settings UI rewrite (TASK-016 through TASK-023), the first-run onboarding flow (TASK-024), and the mic-permission UX (TASK-025, TASK-026). If any step fails, file the regression as a blocker against the release candidate.

## Prerequisites

Before starting, make sure you have:

- A clean Apple Silicon Mac (M1 or later) running macOS 12 or newer — either a fresh machine, a fresh user account, or a wiped install. To create a fresh user without erasing the machine:
  ```bash
  sudo dscl . -create /Users/smoketest
  sudo dscl . -create /Users/smoketest UserShell /bin/zsh
  sudo dscl . -create /Users/smoketest UniqueID 510
  sudo dscl . -create /Users/smoketest PrimaryGroupID 20
  sudo dscl . -create /Users/smoketest NFSHomeDirectory /Users/smoketest
  sudo dscl . -passwd /Users/smoketest <password>
  sudo createhomedir -c -u smoketest
  ```
- An OpenRouter API key (or another supported LLM key — Google AI Studio, Anthropic, etc.). Keep it on the clipboard or in a password manager you can paste from.
- At least 3.5 GB of free disk space — the bundled `.app` is ~3 GB and the DMG itself is ~1.5 GB compressed.
- A working microphone (built-in is fine; USB / Bluetooth headsets also OK).
- An installed `claude` CLI (Claude Code) in `$PATH`, used by step 11 to launch a session.
- The release artifact downloaded locally: `Jarvis-v0.1.0-rc1.dmg` from the GitHub Releases page produced by TASK-027's workflow.

If you have ever run Jarvis (or its predecessor `awm`) on this machine, **run the reset commands in the appendix at the bottom of this doc first**, then come back here.

## Smoke Test Steps

### 1. Wipe prior Jarvis state

**Action:** Run the reset block from the [Reset state appendix](#reset-state-appendix) at the bottom of this file to remove any prior `~/.jarvis/`, `~/.awm/`, installed `.app`, and TCC microphone grants.

**Expected:** `ls ~/.jarvis ~/.awm /Applications/Jarvis.app` returns "No such file or directory" for all three paths. (This also verifies Phase 1's `MigrateLegacyHome()` shim from `internal/paths/migrate.go` will not see legacy data on this run — important so we test the true cold-start path.)

### 2. Mount the DMG

**Action:** Double-click `Jarvis-v0.1.0-rc1.dmg` in Finder, or from a terminal:

```bash
open ~/Downloads/Jarvis-v0.1.0-rc1.dmg
```

**Expected:** A Finder window opens showing `Jarvis.app` next to an `/Applications` symlink. The mounted volume is named "Jarvis" and the window size is roughly 600x400 (as configured by `create-dmg` in TASK-027).

### 3. Install to /Applications

**Action:** Drag `Jarvis.app` onto the `/Applications` shortcut inside the mounted DMG window.

**Expected:** The copy completes within ~20 seconds (the bundle is ~3 GB). `ls /Applications/Jarvis.app/Contents/Resources/python/bin/python3` exists and is executable — this confirms TASK-010's post-build hook bundled the portable venv. `ls /Applications/Jarvis.app/Contents/Resources/models/vibevoice/` is non-empty — confirms TASK-014's model bundling.

### 4. First launch — expect Gatekeeper rejection

**Action:** Double-click `Jarvis.app` from `/Applications` in Finder.

**Expected:** A macOS dialog appears titled "Jarvis cannot be opened because the developer cannot be verified." with an "OK" or "Move to Trash" button — this is the expected ad-hoc-signing Gatekeeper warning called out by TASK-027 and the README (TASK-028). Click "OK" to dismiss; do **not** move the app to trash.

### 5. Right-click → Open

**Action:** In Finder, right-click (or Control-click) `Jarvis.app` in `/Applications` and choose **Open** from the context menu.

**Expected:** A second dialog appears with the message "macOS cannot verify the developer of 'Jarvis'. Are you sure you want to open it?" and three buttons: **Open**, **Move to Trash**, **Cancel**. Click **Open**. The app launches and the Dock icon appears. (After this one-time approval, future launches from Finder or Spotlight will skip both prompts.)

### 6. Onboarding modal appears

**Action:** Wait for the Wails window to render.

**Expected:** Within 2-3 seconds a modal overlay appears with the Jarvis logo and 3 steps visible in a progress indicator: **Welcome → Pick LLM → Grant Mic**. The HUD behind the modal is dimmed and not interactive. (This verifies TASK-024 — the first-run onboarding modal — and that `config.IsFirstRun()` correctly detected the wiped `~/.jarvis/`.)

### 7. Paste API key + validate

**Action:** Click **Continue** on the welcome screen, then on the "Pick LLM" step, paste your OpenRouter API key into the **OpenRouter** field and click **Validate**.

**Expected:** Within 3 seconds a **green validation pill** appears next to the input reading "Valid". The **Continue** button becomes enabled. (This verifies TASK-017's `ValidateAPIKey` binding and the green/red status pill UI.)

If the pill is red, double-check the key, the network, and that the OpenRouter account has at least one credit available.

### 8. Grant mic permission

**Action:** Click **Continue** to advance to the "Grant Mic" step, then click **Grant microphone permission**.

**Expected:** The native macOS dialog appears: "Jarvis would like to access the microphone." with the usage string from TASK-003's `NSMicrophoneUsageDescription` ("Jarvis listens for your voice commands when you say 'Hey Jarvis' or activate the HUD."). Click **Allow**. The onboarding step shows a green check next to "Microphone access granted." (This verifies TASK-025's `RequestMicPermission` binding and TASK-026's mic-permission UI state.)

### 9. Onboarding completes; HUD appears

**Action:** Click **Finish** on the final onboarding step.

**Expected:** The modal disappears and the Jarvis HUD becomes interactive. The orb visualization is visible and unmuted (no red mic-disabled banner). `cat ~/.jarvis/config.json` shows `"firstRunCompleted": true` and `"useLiveKitTransport": false` (verifying TASK-001's default and TASK-024's persisted completion flag). The daemon log at `~/.jarvis/logs/jarvis-daemon.log` contains the line `Audio transport: LocalAudioTransport (Mac mic + speaker)`.

### 10. Voice query — "what time is it?"

**Action:** Speak clearly into the mic: **"Hey Jarvis, what time is it?"**

**Expected:** Within ~3 seconds the orb shows a listening animation, then within another ~3 seconds Jarvis speaks back through the system default output with the current local time (e.g., "It's 3:47 PM, sir."). The terminal output in the HUD log panel shows the wake-word trigger, the transcribed text, and the LLM response text. (This is the full end-to-end voice loop: wake word → STT → LLM → TTS.)

### 11. Voice query against a real Claude Code session

**Action:** Open Terminal.app, `cd` into any git repo, and run:

```bash
claude
```

Let the Claude Code session reach its prompt. Then speak: **"Hey Jarvis, what's happening on my Claude Code session?"**

**Expected:** Jarvis identifies the running Claude Code session (by PID and repo path) and verbally reports its current state — e.g., "You have one Claude session running in `~/code/example`. It's currently idle, waiting for input." The HUD's session indicator panel shows the same session. (This verifies the scanner integration and that voice commands can read live session state.)

### 12. Approval prompt via voice

**Action:** In the same Claude Code session, ask it to do something that triggers an approval prompt (e.g., "Please edit a file"). When Claude shows the y/N approval, speak: **"Hey Jarvis, approve all."**

**Expected:** Within ~2 seconds Jarvis speaks an acknowledgement ("Approved.") and the Claude Code session moves past the prompt — observable by Claude resuming work in the terminal. (This verifies `RespondToApproval` is wired through the NL command engine.)

### 13. Restart persistence

**Action:** Quit Jarvis with **Cmd+Q**, then relaunch from `/Applications/Jarvis.app` (single-click from Finder, or via Spotlight).

**Expected:**
- No Gatekeeper warning (already approved in step 5).
- **No onboarding modal** — config is persisted (TASK-024's `firstRunCompleted` gate).
- HUD appears in ~3 seconds and the mic is still functional. Speaking "Hey Jarvis, are you there?" produces a response.
- `~/.jarvis/config.json` is unchanged from after step 9 (no surprise overwrites).

### 14. Diagnostics tab — green across the board

**Action:** Open Jarvis's main window (not the HUD — the full Wails window via the menu bar or by clicking the Jarvis Dock icon), navigate to **Settings → Diagnostics**.

**Expected:** All seven status rows from TASK-022 are green:
- **Daemon:** running, restarts = 0
- **Mic permission:** granted
- **Mobile API:** running on port 4422 (token visible)
- **LLM provider chain:** OpenRouter (or whichever provider you keyed) — last error: none
- **Bundled models:** VibeVoice loaded, Whisper-small loaded
- **Ollama:** not-running (this is OK — green or grey is acceptable, red is not)
- **Disk usage of `~/.jarvis/`:** under 200 MB

Click **Copy diagnostics** at the bottom of the panel — paste into a scratch text editor and verify a markdown snippet appears with timestamps and the same values.

### 15. Optional — priority alert path (CI failure or stop-session)

**Action:** Pick one of:
- (a) Trigger a CI-style failure: open a repo with GitHub Actions configured, push a known-broken commit, then wait for the workflow to fail (requires `ciWatchEnabled: true` in settings — toggle it on in Settings → Behavior first).
- (b) Voice-stop a session: with at least one Claude Code session running, speak: **"Hey Jarvis, stop my Claude Code session."**

**Expected:**
- (a) A macOS notification pops up titled "CI Failure" with the workflow name. Jarvis speaks a priority alert: "Heads up — your CI build just failed on \<repo\>." (Verifies the CI watcher + notify path.)
- (b) Jarvis confirms ("Stopping your Claude Code session in \<repo\>.") and the Claude process exits within 5 seconds (`ps aux | grep claude` shows no matches). The session indicator in the HUD turns grey.

If all 14 mandatory steps pass (15 is optional but recommended), the release candidate is good to promote to `v0.1.0`. File any failures as P0 bugs blocking the final tag.

## Failure-mode Appendix

When something goes wrong on a step, check the matching entry below before filing a bug.

### Gatekeeper double-prompt won't dismiss (step 5)

Some macOS versions show the warning twice even after right-click → Open. Workaround:

```bash
sudo xattr -dr com.apple.quarantine /Applications/Jarvis.app
```

This strips the quarantine attribute set by LaunchServices when the app was downloaded. After this, double-clicking from Finder should launch the app normally. Document this in the README troubleshooting section (TASK-028).

### Daemon won't start (steps 9 or 10 — orb stays grey, no voice response)

1. Open **Console.app**, set the filter to `process:jarvis-daemon` and look for tracebacks in the last 60 seconds.
2. Check the daemon log directly: `tail -100 ~/.jarvis/logs/jarvis-daemon.log`.
3. Confirm the bundled python launched: `ps -ef | grep jarvis-daemon` — the python binary path should be inside `/Applications/Jarvis.app/Contents/Resources/python/bin/python3` (TASK-011). If it's pointing at a `~/.jarvis/jarvis-daemon-env` venv that doesn't exist, the bundle lookup is broken.
4. Confirm entitlements were applied: `codesign --display --entitlements - /Applications/Jarvis.app | grep -E 'allow-jit|audio-input'` — should show the entries from TASK-004's `build/darwin/entitlements.plist`. If empty, the release pipeline didn't sign correctly.

### Mic permission denied (step 8 or returning to denied state)

If the OS dialog never appeared, or the user accidentally clicked Don't Allow:

1. Open **System Settings → Privacy & Security → Microphone**.
2. Find **Jarvis** in the list. Toggle it **on**.
3. If Jarvis is missing from the list entirely, the request never reached TCC — that's a TASK-025 regression (the cgo binding didn't fire). Workaround: `tccutil reset Microphone com.namanchopra.jarvis` and restart Jarvis to re-prompt.

### Voice loop is silent — text responses appear in HUD but no audio (step 10)

1. Confirm `~/.jarvis/config.json` contains `"useLiveKitTransport": false`. If it's `true`, the daemon is routing audio to a LiveKit endpoint and not to the local speaker — fix the value or use Settings → Behavior → Audio transport → Local Mac mic+speaker.
2. Check the system output device: **System Settings → Sound → Output** — should be a real speaker/headset, not "AirPlay" with no target.
3. Confirm bundled VibeVoice loaded: in the daemon log search for `VibeVoice model loaded from /Applications/Jarvis.app/Contents/Resources/models/vibevoice`. If you see `HF cache` instead, TASK-015's env-var resolution is broken.

### LLM errors — "rate limited" / "invalid_api_key" mid-conversation

1. Settings → Connections → re-paste the API key and click **Validate**. A red pill means the key has expired or has no credits left.
2. Check the diagnostics tab's "LLM provider chain" row for the exact error string.
3. As a fast fallback, install Ollama locally (`brew install ollama && ollama pull qwen3:4b`) and switch the provider dropdown in Settings → Connections to `ollama:qwen3:4b`. Voice queries will go entirely local without an API key.

### Settings UI loses my changes after save

Most often this means the TASK-032 `dex*` ↔ `jarvis*` JSON unmarshal regressed and the read path is still looking for legacy keys. Verify: `cat ~/.jarvis/config.json` after a save should contain only `jarvis*`-prefixed keys (no `dexAPIKey`, `dexEnabled`, etc.). If `dex*` keys reappear, file a P0 bug against TASK-032.

## Reset State Appendix

Run this block whenever you need to put a Mac back into a "never seen Jarvis" state — before step 1 of this doc, or between runs when re-testing:

```bash
# Quit Jarvis if running
osascript -e 'quit app "Jarvis"' 2>/dev/null || true
pkill -f jarvis-daemon 2>/dev/null || true

# Wipe app data and the previous-life `~/.awm/` directory
rm -rf ~/.jarvis ~/.awm

# Remove the installed app
rm -rf /Applications/Jarvis.app

# Eject any leftover mounted DMG volume
hdiutil detach /Volumes/Jarvis 2>/dev/null || true

# Reset TCC mic permission so the dialog re-prompts on next launch
tccutil reset Microphone com.namanchopra.jarvis 2>/dev/null || true

# Optional: clear the launch-services quarantine cache so Gatekeeper
# re-applies the warning the next time the DMG is opened
/System/Library/Frameworks/CoreServices.framework/Versions/A/Frameworks/LaunchServices.framework/Versions/A/Support/lsregister \
  -kill -r -domain local -domain system -domain user 2>/dev/null || true

# Verify wipe
ls -la ~/.jarvis ~/.awm /Applications/Jarvis.app 2>&1 | grep -E 'No such|cannot access' && echo "Reset OK"
```

After this completes, return to step 1 of the smoke test.
