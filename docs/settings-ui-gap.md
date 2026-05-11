# Settings UI Gap Analysis (Phase 2, TASK-006)

This document inventories every configuration field currently exposed by
`frontend/src/views/SettingsView.tsx` (plus its sub-component
`frontend/src/components/JarvisSettings.tsx`) and compares it against the
target Settings spec defined in plan tasks **TASK-016 .. TASK-024** of
`.local-plans/jarvis-oss-prep-phase2-dmg.md`.

The intent is to give the implementer of TASK-016 (the Settings IA rewrite)
a single, reviewable inventory of what already exists, what is partial, and
what is entirely missing — with a direct mapping from each missing item to
the TASK that will implement it.

Files of record:

- Current UI: `frontend/src/views/SettingsView.tsx` (433 lines)
- Current UI partial: `frontend/src/components/JarvisSettings.tsx` (216 lines)
- Canonical config schema: `internal/config/config.go` (260 lines)
- Plan source: `.local-plans/jarvis-oss-prep-phase2-dmg.md` (TASKs 016-024)

---

## 1. Headline Numbers

| Metric | Count |
|---|---|
| Distinct fields/sections rendered in current SettingsView (incl. JarvisSettings) | **15** |
| Distinct fields/sections required by target spec (TASKs 016-024) | **31** |
| **Gap (new fields/sections to add)** | **16** |

Counting rules: a "field" is a user-visible, persisted setting OR a UI
affordance that the spec calls out (e.g. validate button, preview button,
diagnostics panel row). Fields/affordances that exist but only partially
satisfy the spec (e.g. plain text input instead of dropdown + validate)
are counted as present but marked `partial` in the table below.

---

## 2. Target Spec to Section Mapping

The target IA from TASK-016 is a 5-tab layout. Each subsequent task fills
one or more tabs:

| Target tab | Implementing TASKs | Scope |
|---|---|---|
| **Connections** | TASK-017, TASK-018 (LLM dropdown) | API keys (6 providers), LLM model override |
| **Voice** | TASK-018 (TTS/STT), TASK-019, TASK-020 | TTS provider, STT model, voice preset + preview, mic device, wake word toggle/sensitivity |
| **Behavior** | TASK-020 (transport, ambient), TASK-021 | Audio transport, ambient mode, notifications, scanner project roots |
| **Diagnostics** | TASK-022 | Live health panel (daemon, mic perm, mobile API, LLM chain, models, Ollama, disk) |
| **Advanced** | TASK-023 | Mobile API token + regenerate, `dotClaudeSourcePath`, import/export, reset to defaults |
| (cross-cutting onboarding) | TASK-024 | First-run modal that reuses TASK-017 validate + TASK-018 dropdown |

TASK-016 itself is pure scaffolding — it creates the tab shell and migrates
all existing fields into the new IA without changing semantics.

---

## 3. Field-by-Field Comparison

Every config field exported by `internal/config/config.go` (post Phase 1)
is listed below, plus every UI-only affordance demanded by the spec
(validate buttons, preview buttons, diagnostics rows, etc.) that does not
correspond to a single config field.

Legend for *In current SettingsView?*:
- **yes** — field is rendered with a control that matches the spec
- **partial** — field is rendered but the control diverges from the spec
  (e.g. free-text where spec wants a dropdown, no validate/preview button,
  no availability indicator)
- **no** — field is not rendered anywhere in the Settings UI

### 3.1 Config fields from `internal/config/config.go`

| Field / Section | In current SettingsView? | Needed for v0.1 | Implementing TASK |
|---|---|---|---|
| `dotClaudeSourcePath` | yes (top section, text input + Sync button) | yes (Advanced tab, with Browse button) | TASK-016 (move), TASK-023 (add Browse) |
| `defaultAgent` | yes (dropdown: claude-code/kiro/gemini/codex/aider) | yes (retained — regression target) | TASK-016 |
| `scanIntervalSeconds` | yes (number input 1-60) | yes (retained) | TASK-016 |
| `preferredTerminal` | yes (dropdown + detected pills) | yes (retained) | TASK-016 |
| `projectRootPaths` | partial (textarea only, no Browse button) | yes (multi-line + native folder picker) | TASK-021 |
| `notificationsEnabled` | yes (toggle) | yes (retained) | TASK-021 |
| `notifyOnApproval` | yes (toggle) | yes (retained) | TASK-021 |
| `notifyOnCompletion` | yes (toggle) | yes (retained) | TASK-021 |
| `ciWatchEnabled` | yes (toggle) | yes (retained — not in spec but no removal planned) | TASK-016 (migrate as-is) |
| `ciProvider` | yes (dropdown github-actions/gitlab-ci) | yes (retained) | TASK-016 |
| `defaultCommand` | yes (text input) | yes (retained) | TASK-016 |
| `mobileAPIPort` | partial (shown read-only inside Mobile App section via `mobileInfo.port`, not editable) | yes (Advanced tab — keep current read-only display behaviour) | TASK-023 |
| `mobileAPIToken` | yes (reveal/copy/regenerate) | yes (Advanced tab, identical UX) | TASK-023 |
| `jarvisEnabled` | yes (toggle in JarvisSettings) | yes (retained, exposed under Voice/Behavior) | TASK-016 |
| `jarvisProvider` (`"cli"` vs `"api"`) | no | yes (Connections tab — informs LLM model dropdown) | TASK-018 |
| `jarvisAPIKey` (OpenRouter / Anthropic key — same field per plan) | partial (rendered as "Claude API Key" with show/hide, no Validate button) | yes (Connections tab — labelled OpenRouter, with Validate button) | TASK-017 |
| `jarvisVoice` | partial (free-text input, no preset list, no preview) | yes (Voice tab — preset dropdown + Preview button) | TASK-019 |
| `jarvisAmbientEnabled` | yes (toggle) | yes (Behavior tab) | TASK-020 |
| `jarvisVerbosity` | yes (segmented concise/detailed) | yes (retained — not called out in 016-024 but no removal planned) | TASK-016 |
| `jarvisPicovoiceKey` | no | yes (Connections tab — Picovoice key with Validate) | TASK-017 |
| `jarvisWakeWordModel` | no | yes (Voice tab — paired with wake word toggle) | TASK-020 |
| `jarvisWakeSensitivity` (0.3-0.8) | no | yes (Voice tab — slider) | TASK-020 |
| `jarvisElevenLabsKey` | no | yes (Connections tab — ElevenLabs key with Validate) | TASK-017 |
| `jarvisElevenLabsVoice` | no | yes (Voice tab — surfaces when TTS provider = elevenlabs; spec routes preset selection through TASK-019) | TASK-019 |
| `useLiveKitTransport` | no | yes (Behavior tab — Audio transport dropdown Local/LiveKit) | TASK-020 |
| `livekitUrl` | no | yes (Behavior tab — visible only when transport = LiveKit) | TASK-020 |
| `livekitApiKey` | no | yes (Behavior tab — LiveKit-only field) | TASK-020 |
| `livekitApiSecret` | no | yes (Behavior tab — LiveKit-only, masked) | TASK-020 |
| `livekitRoomName` | no | yes (Behavior tab — LiveKit-only) | TASK-020 |

### 3.2 Fields demanded by spec that are NOT yet config keys

These are new fields (UI + config schema additions) that TASK-017 / TASK-018
will introduce. They do not exist in `internal/config/config.go` today but
are explicitly called out in the plan and will need to be added when the
respective task is executed.

| Field / Section | In current SettingsView? | Needed for v0.1 | Implementing TASK |
|---|---|---|---|
| Google AI Studio API key (`googleAPIKey`) | no | yes (Connections tab — Validate) | TASK-017 |
| Anthropic API key (distinct from OpenRouter when `jarvisProvider="api"`) | no (today, `jarvisAPIKey` is reused as Claude key) | yes (Connections tab — Validate) | TASK-017 |
| Cartesia API key (`cartesiaAPIKey`) | no | yes (Connections tab — Validate) | TASK-017 |
| TTS provider dropdown (vibevoice / kokoro / edge / cartesia) | no | yes (Voice tab — with availability indicators) | TASK-018 |
| STT model dropdown (whisper-small.en / whisper-tiny.en / faster-whisper) | no | yes (Voice tab — with availability indicators) | TASK-018 |
| LLM model override dropdown (gemini-2.5-flash / claude-haiku-4-5 / gpt-4o-mini / ollama:qwen3:4b) | no | yes (Connections tab — with availability indicators) | TASK-018 |
| Mic input device picker (`GetAudioInputDevices`) | no | yes (Behavior tab — populated from new Wails binding) | TASK-020 |
| Wake word toggle | no | yes (Voice tab) | TASK-020 |

### 3.3 UI-only affordances (not config fields)

These rows are buttons/panels the spec demands but which do not map to a
config key.

| Field / Section | In current SettingsView? | Needed for v0.1 | Implementing TASK |
|---|---|---|---|
| Five-tab IA shell (Connections / Voice / Behavior / Diagnostics / Advanced) | no (single flat scroll today) | yes | TASK-016 |
| API key show/hide eye toggle | partial (JarvisSettings has Show/Hide for `jarvisAPIKey` only; other keys absent) | yes (all 6 key fields) | TASK-017 |
| Per-key `Validate` button + green/red pill | no | yes (calls new `ValidateAPIKey` binding) | TASK-017 |
| Voice preview button (`PreviewVoice` binding) | no | yes (Voice tab, ▶ Preview) | TASK-019 |
| Availability indicators on dropdowns (✓ bundled / ⚠ needs key / ⚠ Ollama not running) | no | yes (Voice + Connections dropdowns) | TASK-018 |
| Browse... button for scanner project roots (`runtime.OpenDirectoryDialog`) | no | yes (Behavior tab) | TASK-021 |
| Browse... button for `dotClaudeSourcePath` | no | yes (Advanced tab) | TASK-023 |
| Diagnostics panel — daemon status row | no | yes (polls every 2s) | TASK-022 |
| Diagnostics panel — mic permission row | no | yes | TASK-022 |
| Diagnostics panel — mobile API row (port + token) | no | yes | TASK-022 |
| Diagnostics panel — LLM provider chain row (active + last error) | no | yes | TASK-022 |
| Diagnostics panel — bundled models row (per-model loaded/missing) | no | yes | TASK-022 |
| Diagnostics panel — Ollama row (localhost:11434 reachable) | no | yes | TASK-022 |
| Diagnostics panel — disk usage of `~/.jarvis/` | no | yes | TASK-022 |
| Diagnostics panel — per-row copy-to-clipboard button | no | yes | TASK-022 |
| Diagnostics panel — master "Copy diagnostics" button (markdown blob) | no | yes | TASK-022 |
| Export config button (save dialog → write current config.json) | no | yes (Advanced tab) | TASK-023 |
| Import config button (open dialog → schema-validate → confirm) | no | yes (Advanced tab) | TASK-023 |
| Reset to defaults button + "Preserve API keys" checkbox | no | yes (Advanced tab, confirmation modal) | TASK-023 |
| First-run onboarding modal (Welcome → Pick LLM → Grant Mic) | no | yes (blocks HUD on fresh install) | TASK-024 |
| Mobile API token reveal/copy/regenerate cluster | yes (already implemented in current SettingsView) | yes (move into Advanced tab) | TASK-023 (move) |
| Section-level Save button | yes (single bottom-of-page Save) | yes (retained — IA shell preserves behaviour) | TASK-016 |
| Inline status message banner | yes (success/error toast) | yes (retained) | TASK-016 |

---

## 4. Tasks That Are Already Satisfied or Partially Satisfied

| TASK | Status today | Notes |
|---|---|---|
| TASK-016 (5-tab IA) | not started | The current UI is a single vertical scroll with ~10 sections. No tabs. |
| TASK-017 (6 API keys + validate) | 1/6 partial | Only `jarvisAPIKey` is rendered (as "Claude API Key"); no Validate buttons anywhere. Missing: OpenRouter (relabel), Google, Anthropic, Cartesia, ElevenLabs, Picovoice. |
| TASK-018 (TTS/STT/LLM dropdowns) | not started | No dropdowns exist for any of the three. |
| TASK-019 (voice preset + preview) | partial | `jarvisVoice` exists as free-text. No preset dropdown, no preview button, no `PreviewVoice` binding. |
| TASK-020 (transport + mic + wake) | not started | `useLiveKitTransport`, all 4 LiveKit fields, mic picker, wake-word toggle, sensitivity slider are all missing. `jarvisAmbientEnabled` is the only piece present. |
| TASK-021 (notifications + scanner roots) | partial | 3 notification toggles present; `projectRootPaths` is a plain textarea with no Browse button. |
| TASK-022 (Diagnostics) | not started | No Diagnostics view exists at all. |
| TASK-023 (Advanced — import/export/reset) | partial | Mobile token reveal/regenerate already exist; everything else (import, export, reset, dotClaudeSource Browse button) is missing. |
| TASK-024 (first-run onboarding) | not started | No onboarding flow exists; the app currently lands directly on Control Center on fresh install. |

---

## 5. Top 5 Most-Impactful Gaps

In rough order of "what a new user will hit first on a fresh DMG install":

1. **No first-run onboarding (TASK-024).** A new user opens the app and is dropped onto the Control Center HUD with no guidance on adding an LLM key or granting mic permission. Voice features silently fail.
2. **No LLM / TTS / STT provider dropdowns (TASK-018).** The current UI lets the user type a free-text `jarvisVoice` and one API key, but doesn't expose the bundled-vs-cloud TTS choice (vibevoice / kokoro / edge / cartesia) or the LLM model selector that the daemon now expects.
3. **Missing API key fields for 5 of 6 providers (TASK-017).** Only `jarvisAPIKey` is rendered. Picovoice (wake word), ElevenLabs (premium voice), Google AI Studio, Cartesia, and a distinct OpenRouter key are all absent — users can't enable several flagship features without hand-editing `~/.jarvis/config.json`.
4. **No Diagnostics panel (TASK-022).** When something goes wrong (daemon dead, Ollama not running, mic permission denied, model missing) there's no in-app way to see it. Users will file "Jarvis doesn't speak" bugs with no actionable info.
5. **No LiveKit transport UI (TASK-020).** `useLiveKitTransport` and its four credential fields are entirely invisible in the UI. Phase 1 set the default to false, so the feature is opt-in — but there's currently no way to opt in from the UI at all.

---

## 6. Notes on Counting and Coverage

- Phase 1's `UnmarshalJSON` migration (TASK-032 in plan) means every
  `jarvis*` key listed in `internal/config/config.go` is the canonical
  name; legacy `dex*` keys are read-only-for-compat and need not appear
  in the new UI.
- `mobileAPIPort` is treated as `partial` rather than `no` because the
  current UI exposes the port read-only inside the LAN-address pills
  (`${ip}:${mobileInfo.port}`). The spec does not require it to become
  editable, so TASK-023 simply preserves the read-only display.
- The Approval Rules section (`<ApprovalRulesSettings />` component) is
  rendered in the current SettingsView but is out of scope for TASKs
  016-024; it should be preserved as-is by TASK-016 and slotted into
  Behavior or Advanced.
- `jarvisVerbosity` and `ciWatchEnabled`/`ciProvider` are not explicitly
  called out by TASKs 016-024 but no task removes them either — TASK-016
  must retain them per its acceptance criterion "no field disappears".

---

## 7. Quick Reference: TASK → Required Bindings

For each task that adds new fields, the following Wails bindings are
mentioned in the plan and will need to be added/wired (this is informational
— not part of the gap count, but useful when scoping TASK-016 follow-ups):

| TASK | New Wails bindings | Existing bindings reused |
|---|---|---|
| TASK-017 | `ValidateAPIKey(provider, key) -> {ok, error}` | — |
| TASK-018 | `GetAvailableTTSProviders`, `GetAvailableSTTModels`, `GetAvailableLLMModels` | — |
| TASK-019 | `PreviewVoice(provider, voiceId)` | — |
| TASK-020 | `GetAudioInputDevices() -> []Device` | — |
| TASK-021 | `runtime.OpenDirectoryDialog` | — |
| TASK-022 | `GetDiagnostics() -> Diagnostics` | `GetMobileConnectionInfo` |
| TASK-023 | `ExportConfig(path)`, `ImportConfig(path)`, `ResetConfig(preserveKeys bool)` | `RegenerateMobileToken`, `GetConfig`, `SaveConfig` |
| TASK-024 | `IsFirstRun() bool`, `MarkFirstRunComplete()` | reuses TASK-017 + TASK-018 bindings |
| TASK-025 (referenced by TASK-022/024) | `GetMicPermissionStatus()`, `RequestMicPermission()` | — |
