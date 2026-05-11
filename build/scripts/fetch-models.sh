#!/usr/bin/env bash
#
# fetch-models.sh
#
# Downloads the AI models Jarvis bundles into the macOS .app for offline use:
#
#   1. VibeVoice-Realtime-0.5B (Microsoft) - streaming TTS model
#        Repo: huggingface.co/microsoft/VibeVoice-Realtime-0.5B
#        Files: model.safetensors (~1.9 GiB), config.json, preprocessor_config.json
#        Dest:  build/models/vibevoice/
#
#   2. VibeVoice voice preset (en-Carter_man.pt) - the "Jarvis" voice
#        Source: github.com/microsoft/VibeVoice (raw asset, not on HuggingFace)
#        File:   demo/voices/streaming_model/en-Carter_man.pt (~4 MiB)
#        Dest:   build/models/vibevoice/voices/en-Carter_man.pt
#
#   3. Whisper-small.en (mlx-community MLX variant for Apple Silicon)
#        Repo:  huggingface.co/mlx-community/whisper-small.en-mlx
#        Files: weights.npz (~460 MiB), config.json
#        Dest:  build/models/whisper-small/
#
# This script is referenced by TASK-013 of plans/jarvis-oss-prep-phase2-dmg.md.
# It is consumed by TASK-014 (Wails post-build hook), which rsyncs
# build/models/ into Jarvis.app/Contents/Resources/models/.
#
# -----------------------------------------------------------------------------
# Required tools
# -----------------------------------------------------------------------------
#
#   * `huggingface-cli` from the `huggingface_hub` PyPI package
#       Install with one of:
#           pip3 install --user 'huggingface_hub[cli]'
#           pipx install 'huggingface_hub[cli]'
#           uv tool install 'huggingface_hub[cli]'
#       (The fetch-python.sh-installed standalone Python is *not* used here;
#        we rely on the host's pip/pipx since this is a build-machine script.)
#
#   * `curl` (preinstalled on macOS) - used for the GitHub raw asset download
#     of the voice preset, and as a fallback if huggingface-cli is missing.
#
#   * `du` and `awk` (preinstalled on macOS) - for size accounting.
#
# -----------------------------------------------------------------------------
# Environment variables
# -----------------------------------------------------------------------------
#
#   HF_TOKEN     (optional) HuggingFace personal access token. If set, it is
#                forwarded to `huggingface-cli` to avoid anonymous rate limits
#                in CI. Never logged. Never hard-coded.
#
#   DRY_RUN      (optional) If set to "1", the script prints what would be
#                downloaded and exits 0 without touching the network or
#                creating any files. Use this in tests/CI smoke checks.
#
#   SIZE_CAP_BYTES (optional) Override the fail-fast size cap. Defaults to
#                  2_000_000_000 (2.0 GB decimal) per TASK-013 acceptance
#                  criterion. The script exits non-zero if build/models/
#                  exceeds this after download.
#
# -----------------------------------------------------------------------------
# Usage
# -----------------------------------------------------------------------------
#
#   # Real run (downloads ~2.3 GiB on first run, no-op on re-run):
#   bash build/scripts/fetch-models.sh
#
#   # Dry run (no network, no writes - for tests and CI smoke checks):
#   DRY_RUN=1 bash build/scripts/fetch-models.sh
#
#   # CI run with auth token:
#   HF_TOKEN="hf_xxx" bash build/scripts/fetch-models.sh
#
# Re-running on a populated build/models/ tree is a no-op: huggingface-cli
# uses its local cache and the voice-preset download skips when the .pt file
# is already present with non-zero size.
#
# macOS only. Uses `shasum`-style tooling and BSD `du`.
#

set -euo pipefail

# -----------------------------------------------------------------------------
# Configuration
# -----------------------------------------------------------------------------

# Pinned model repo IDs. Bump these in lockstep when upgrading models.
VIBEVOICE_REPO="microsoft/VibeVoice-Realtime-0.5B"
WHISPER_REPO="mlx-community/whisper-small.en-mlx"

# Voice preset lives in the upstream VibeVoice GitHub repo (NOT on HuggingFace).
VOICE_PRESET_NAME="en-Carter_man.pt"
VOICE_PRESET_URL="https://raw.githubusercontent.com/microsoft/VibeVoice/main/demo/voices/streaming_model/${VOICE_PRESET_NAME}"

# Resolve paths relative to the repo root (parent of build/).
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
MODELS_DIR="${REPO_ROOT}/build/models"
VIBEVOICE_DIR="${MODELS_DIR}/vibevoice"
VIBEVOICE_VOICES_DIR="${VIBEVOICE_DIR}/voices"
WHISPER_DIR="${MODELS_DIR}/whisper-small"

# Expected canonical filenames inside each destination. Verified against the
# HuggingFace API on 2026-05-11. If upstream renames these, the post-build
# hook (TASK-014) will fail loudly - update both places together.
VIBEVOICE_MAIN_FILE="model.safetensors"
WHISPER_MAIN_FILE="weights.npz"

# Fail-fast size cap (2.0 GB decimal). Matches TASK-013 acceptance criterion:
#   "exits non-zero if it exceeds 2.0GB (early signal before DMG hits 4GB)".
# NOTE: VibeVoice-Realtime-0.5B's model.safetensors alone is ~2.035 GB decimal
# (~1.896 GiB binary). When combined with Whisper (~0.481 GB) the total is
# ~2.52 GB decimal / ~2.35 GiB binary - which is over this cap. The cap is
# enforced anyway per the acceptance criterion; if the build legitimately
# needs to exceed it, override with SIZE_CAP_BYTES env var and update the
# Failure Modes table in plans/jarvis-oss-prep-phase2-dmg.md.
SIZE_CAP_BYTES="${SIZE_CAP_BYTES:-2000000000}"

# Dry-run support.
DRY_RUN="${DRY_RUN:-0}"

# -----------------------------------------------------------------------------
# Logging helpers
# -----------------------------------------------------------------------------

log()   { printf '\033[1;34m[fetch-models]\033[0m %s\n' "$*"; }
warn()  { printf '\033[1;33m[fetch-models]\033[0m %s\n' "$*" >&2; }
die()   { printf '\033[1;31m[fetch-models]\033[0m %s\n' "$*" >&2; exit 1; }

# Format a byte count as a human-readable size (BSD-friendly; no `numfmt`).
fmt_bytes() {
    awk -v b="$1" 'BEGIN {
        if (b >= 1024*1024*1024) printf "%.2f GiB", b/1024/1024/1024;
        else if (b >= 1024*1024) printf "%.2f MiB", b/1024/1024;
        else if (b >= 1024)      printf "%.2f KiB", b/1024;
        else                      printf "%d B",    b;
    }'
}

# Compute total bytes used by a directory tree. Uses `du -sk` (KiB) because
# macOS BSD `du` has no `-b` flag. Output in bytes for arithmetic.
dir_bytes() {
    local d="$1"
    if [[ ! -d "${d}" ]]; then
        printf '0'
        return
    fi
    du -sk "${d}" 2>/dev/null | awk '{ printf "%d", $1 * 1024 }'
}

# -----------------------------------------------------------------------------
# Tool detection
# -----------------------------------------------------------------------------

have_hf_cli=0
if command -v huggingface-cli >/dev/null 2>&1; then
    have_hf_cli=1
fi

# -----------------------------------------------------------------------------
# Dry-run path: report plan and exit before any network I/O.
# -----------------------------------------------------------------------------

if [[ "${DRY_RUN}" == "1" ]]; then
    log "DRY RUN - no network, no file writes"
    log ""
    log "Repo root:              ${REPO_ROOT}"
    log "Models directory:       ${MODELS_DIR}"
    log "Size cap:               $(fmt_bytes "${SIZE_CAP_BYTES}") (${SIZE_CAP_BYTES} bytes)"
    log "huggingface-cli:        $([[ ${have_hf_cli} -eq 1 ]] && echo "found ($(command -v huggingface-cli))" || echo "MISSING - install with: pip3 install --user 'huggingface_hub[cli]'")"
    log "HF_TOKEN:               $([[ -n "${HF_TOKEN:-}" ]] && echo "set (will pass through to huggingface-cli)" || echo "not set (anonymous access)")"
    log ""
    log "Planned downloads:"
    log "  1) HuggingFace repo ${VIBEVOICE_REPO}"
    log "       -> ${VIBEVOICE_DIR}/"
    log "       expected canonical file: ${VIBEVOICE_MAIN_FILE} (~1.9 GiB)"
    log "  2) GitHub raw asset ${VOICE_PRESET_URL##*/}"
    log "       -> ${VIBEVOICE_VOICES_DIR}/${VOICE_PRESET_NAME} (~4 MiB)"
    log "       URL: ${VOICE_PRESET_URL}"
    log "  3) HuggingFace repo ${WHISPER_REPO}"
    log "       -> ${WHISPER_DIR}/"
    log "       expected canonical file: ${WHISPER_MAIN_FILE} (~460 MiB)"
    log ""
    log "Estimated total:        ~2.35 GiB (~2.52 GB decimal)"
    log "DRY RUN complete - exiting 0 without touching the network."
    exit 0
fi

# -----------------------------------------------------------------------------
# Pre-flight: ensure required tools are present (real run only).
# -----------------------------------------------------------------------------

if [[ ${have_hf_cli} -ne 1 ]]; then
    die "huggingface-cli not found on PATH.
       Install with one of:
           pip3 install --user 'huggingface_hub[cli]'
           pipx install 'huggingface_hub[cli]'
           uv tool install 'huggingface_hub[cli]'
       Then ensure ~/.local/bin (or pipx's bin) is on PATH and re-run."
fi

if ! command -v curl >/dev/null 2>&1; then
    die "curl not found on PATH (needed for the GitHub voice-preset download)."
fi

mkdir -p "${VIBEVOICE_DIR}" "${VIBEVOICE_VOICES_DIR}" "${WHISPER_DIR}"

# -----------------------------------------------------------------------------
# HuggingFace download helper
# -----------------------------------------------------------------------------
#
# huggingface-cli download semantics:
#   * Downloads to its own cache by default and creates symlinks in --local-dir.
#   * On re-run with the cache populated, it is effectively a no-op (it still
#     prints the path of each file but performs no network transfer).
#   * `--local-dir-use-symlinks False` materializes real files instead of
#     symlinks - required so the Wails post-build rsync copies real bytes
#     into Jarvis.app (TASK-014).
#   * `--token` is passed only if HF_TOKEN is set, to keep anonymous access
#     working out of the box.
#
hf_download() {
    local repo="$1"
    local dest="$2"
    local canonical_file="$3"

    local args=(download "${repo}" --local-dir "${dest}" --local-dir-use-symlinks False)
    if [[ -n "${HF_TOKEN:-}" ]]; then
        args+=(--token "${HF_TOKEN}")
    fi

    if [[ -f "${dest}/${canonical_file}" ]]; then
        local existing_size
        existing_size="$(stat -f %z "${dest}/${canonical_file}" 2>/dev/null || echo 0)"
        if [[ "${existing_size}" -gt 0 ]]; then
            log "  ${repo}: ${canonical_file} already present ($(fmt_bytes "${existing_size}")) - using HF cache for any deltas"
        fi
    fi

    # Run huggingface-cli; it handles its own caching so re-runs are cheap.
    # We pipe stderr through unchanged so users see HF progress bars on TTY.
    log "  running: huggingface-cli download ${repo} --local-dir ${dest} ..."
    huggingface-cli "${args[@]}" >/dev/null

    if [[ ! -f "${dest}/${canonical_file}" ]]; then
        die "expected file '${canonical_file}' missing from ${dest} after download.
       The repo may have been renamed upstream. Verify with:
           curl -sL https://huggingface.co/api/models/${repo}/tree/main
       and update ${BASH_SOURCE[0]} accordingly."
    fi
}

# -----------------------------------------------------------------------------
# Voice-preset download helper (GitHub raw, not HuggingFace)
# -----------------------------------------------------------------------------

voice_preset_download() {
    local dest="${VIBEVOICE_VOICES_DIR}/${VOICE_PRESET_NAME}"
    if [[ -f "${dest}" ]]; then
        local existing_size
        existing_size="$(stat -f %z "${dest}" 2>/dev/null || echo 0)"
        if [[ "${existing_size}" -gt 0 ]]; then
            log "  voice preset ${VOICE_PRESET_NAME} already present ($(fmt_bytes "${existing_size}")) - skipping"
            return
        fi
    fi

    log "  curl -L ${VOICE_PRESET_URL}"
    # --fail: non-2xx -> non-zero exit; --location: follow redirects;
    # --retry: ride out transient hiccups; --silent + --show-error: clean logs.
    curl --fail --location --retry 3 --retry-delay 2 --silent --show-error \
        --output "${dest}.tmp" \
        "${VOICE_PRESET_URL}"
    mv "${dest}.tmp" "${dest}"

    local new_size
    new_size="$(stat -f %z "${dest}" 2>/dev/null || echo 0)"
    log "  voice preset saved: $(fmt_bytes "${new_size}")"
}

# -----------------------------------------------------------------------------
# Run downloads
# -----------------------------------------------------------------------------

log "Fetching models into ${MODELS_DIR}"
log "  (HF_TOKEN $([[ -n "${HF_TOKEN:-}" ]] && echo "is set" || echo "not set"); cache enabled; re-runs are no-ops)"
log ""

log "[1/3] VibeVoice realtime model: ${VIBEVOICE_REPO}"
hf_download "${VIBEVOICE_REPO}" "${VIBEVOICE_DIR}" "${VIBEVOICE_MAIN_FILE}"
vibevoice_bytes="$(dir_bytes "${VIBEVOICE_DIR}")"
log "       subtotal: $(fmt_bytes "${vibevoice_bytes}")"
log ""

log "[2/3] VibeVoice voice preset: ${VOICE_PRESET_NAME}"
voice_preset_download
voices_bytes="$(dir_bytes "${VIBEVOICE_VOICES_DIR}")"
log "       subtotal (voices/): $(fmt_bytes "${voices_bytes}")"
log ""

log "[3/3] Whisper MLX model: ${WHISPER_REPO}"
hf_download "${WHISPER_REPO}" "${WHISPER_DIR}" "${WHISPER_MAIN_FILE}"
whisper_bytes="$(dir_bytes "${WHISPER_DIR}")"
log "       subtotal: $(fmt_bytes "${whisper_bytes}")"
log ""

# -----------------------------------------------------------------------------
# Size cap enforcement (TASK-013 acceptance criterion)
# -----------------------------------------------------------------------------

total_bytes="$(dir_bytes "${MODELS_DIR}")"
log "================================================================"
log "Total build/models/ size: $(fmt_bytes "${total_bytes}") (${total_bytes} bytes)"
log "Size cap:                 $(fmt_bytes "${SIZE_CAP_BYTES}") (${SIZE_CAP_BYTES} bytes)"
log "================================================================"

if [[ "${total_bytes}" -gt "${SIZE_CAP_BYTES}" ]]; then
    die "FAIL: build/models/ ($(fmt_bytes "${total_bytes}")) exceeds the size cap ($(fmt_bytes "${SIZE_CAP_BYTES}")).
       Per the Failure Modes table in plans/jarvis-oss-prep-phase2-dmg.md,
       a DMG over 4GB risks rejection by some networks. Options:
         1) Switch to hybrid bundling (ship Whisper, download VibeVoice on
            first run) - see plan's mitigation for 'Bundled models too large'.
         2) Move to a smaller VibeVoice variant if/when Microsoft publishes one.
         3) If this is intentional, override the cap:
              SIZE_CAP_BYTES=$((${total_bytes} + 50000000)) bash build/scripts/fetch-models.sh
            and update the Failure Modes table to reflect the new ceiling."
fi

log "OK: models fetched successfully and total size is within the ${SIZE_CAP_BYTES}-byte cap."
