#!/usr/bin/env bash
#
# fetch-uv.sh
#
# Downloads a pinned `uv` release (aarch64-apple-darwin) from the astral-sh/uv
# GitHub releases, verifies its SHA256 against the release's `.sha256` sidecar
# file, and extracts the `uv` binary to `build/uv/uv`.
#
# Why bundle uv at all? On first launch the app spawns the v0.2.0 setup
# orchestrator (`scripts/setup/install-daemon.sh`, TASK-001) which uses uv to
# materialize the Python interpreter + daemon venv into ~/Library/Application
# Support/Jarvis/. Shipping uv (~10MB binary) instead of CPython + venv (~150MB)
# saves ~140MB in the DMG.
#
# Pinned release tag: 0.11.14   (published 2026-05-12)
# Source: https://github.com/astral-sh/uv/releases/tag/0.11.14
#
# Why pinned: surprise version drift breaks the setup script's behavior. Bump
# UV_RELEASE_TAG below with intent, e.g. when upgrading to pick up a uv bugfix.
# Re-run on a clean `build/uv/` tree to repopulate the SHA cache.
#
# Notes on SHA verification:
#   uv publishes per-asset `.sha256` sidecar files (standard `shasum -c` format)
#   AND a consolidated `sha256.sum` manifest. We use the per-asset sidecar for
#   minimal surface area.
#
# This script is idempotent: re-running with a valid `build/uv/uv` already in
# place AND matching the pinned tag is a no-op.
#
# macOS only. Uses `shasum -a 256` (not `sha256sum` which is Linux-only).
#
# Usage:
#   bash build/scripts/fetch-uv.sh

set -euo pipefail

# -----------------------------------------------------------------------------
# Configuration (bump this when upgrading uv)
# -----------------------------------------------------------------------------
UV_RELEASE_TAG="0.11.14"
UV_PLATFORM="aarch64-apple-darwin"
UV_ASSET="uv-${UV_PLATFORM}.tar.gz"
UV_BASE_URL="https://github.com/astral-sh/uv/releases/download/${UV_RELEASE_TAG}"

# -----------------------------------------------------------------------------
# Paths
# -----------------------------------------------------------------------------
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
UV_DIR="${REPO_ROOT}/build/uv"
CACHE_DIR="${UV_DIR}/.fetch-cache"
TARBALL_PATH="${CACHE_DIR}/${UV_ASSET}"
SHA_PATH="${CACHE_DIR}/${UV_ASSET}.sha256"
STAMP_PATH="${UV_DIR}/.installed-tag"
UV_BIN="${UV_DIR}/uv"

# -----------------------------------------------------------------------------
# Logging helpers (all progress to stderr; only final success to stdout)
# -----------------------------------------------------------------------------
log() { echo "[fetch-uv] $*" >&2; }
die() { echo "[fetch-uv] ERROR: $*" >&2; exit 1; }

# -----------------------------------------------------------------------------
# Sanity checks
# -----------------------------------------------------------------------------
if [[ "$(uname -s)" != "Darwin" ]]; then
    die "this script targets macOS only (uname=$(uname -s))"
fi
if [[ "$(uname -m)" != "arm64" ]]; then
    log "warning: host arch is $(uname -m), but the asset targets aarch64-apple-darwin (arm64)."
    log "         the binary will still be extracted, just unrunnable on this host."
fi
for tool in curl shasum tar; do
    command -v "${tool}" >/dev/null 2>&1 || die "required tool not found in PATH: ${tool}"
done

# -----------------------------------------------------------------------------
# Idempotency check
# -----------------------------------------------------------------------------
if [[ -x "${UV_BIN}" ]] && [[ -f "${STAMP_PATH}" ]]; then
    installed_tag="$(cat "${STAMP_PATH}" 2>/dev/null || true)"
    if [[ "${installed_tag}" == "${UV_RELEASE_TAG}" ]]; then
        log "uv ${UV_RELEASE_TAG} already extracted at ${UV_BIN}"
        log "already up to date — nothing to do"
        echo "uv ${UV_RELEASE_TAG} already installed at ${UV_BIN}"
        exit 0
    fi
    log "stamp file says installed tag is '${installed_tag}', but we want '${UV_RELEASE_TAG}'"
    log "removing stale binary and re-fetching"
    rm -f "${UV_BIN}"
fi

mkdir -p "${CACHE_DIR}"

# -----------------------------------------------------------------------------
# Download tarball (idempotent in cache)
# -----------------------------------------------------------------------------
if [[ -f "${TARBALL_PATH}" ]]; then
    log "tarball already cached at ${TARBALL_PATH}"
else
    log "downloading ${UV_ASSET} (~20MB) from ${UV_BASE_URL}"
    curl --fail --location --show-error --silent \
        --output "${TARBALL_PATH}.partial" \
        "${UV_BASE_URL}/${UV_ASSET}" \
        || die "failed to download ${UV_ASSET}"
    mv "${TARBALL_PATH}.partial" "${TARBALL_PATH}"
    log "downloaded $(du -h "${TARBALL_PATH}" | awk '{print $1}')"
fi

# -----------------------------------------------------------------------------
# Download .sha256 sidecar (always re-fetch — tiny, authoritative)
# -----------------------------------------------------------------------------
log "fetching ${UV_ASSET}.sha256 sidecar"
curl --fail --location --show-error --silent \
    --output "${SHA_PATH}" \
    "${UV_BASE_URL}/${UV_ASSET}.sha256" \
    || die "failed to download ${UV_ASSET}.sha256 from ${UV_BASE_URL}"

# -----------------------------------------------------------------------------
# Verify SHA256
# -----------------------------------------------------------------------------
log "verifying SHA256 of ${UV_ASSET}"
expected_sha="$(awk '{print $1}' "${SHA_PATH}")"
if [[ -z "${expected_sha}" ]]; then
    die "empty SHA256 in ${SHA_PATH} — release may be malformed"
fi
actual_sha="$(shasum -a 256 "${TARBALL_PATH}" | awk '{print $1}')"
if [[ "${expected_sha}" != "${actual_sha}" ]]; then
    log "SHA256 MISMATCH for ${TARBALL_PATH}"
    log "  expected: ${expected_sha}"
    log "  actual:   ${actual_sha}"
    log "the cached tarball may be corrupt or tampered with."
    log "delete ${TARBALL_PATH} and re-run to fetch a fresh copy."
    exit 1
fi
log "SHA256 verified: ${actual_sha}"

# -----------------------------------------------------------------------------
# Extract
# -----------------------------------------------------------------------------
# uv tarballs unpack to a top-level `uv-<platform>/` directory containing the
# `uv` and `uvx` binaries. We only need `uv` — copy it to build/uv/uv.
log "extracting ${UV_ASSET}"
extract_tmp="$(mktemp -d "${REPO_ROOT}/build/uv.extract.XXXXXX")"
trap 'rm -rf "${extract_tmp}"' EXIT

tar -xzf "${TARBALL_PATH}" -C "${extract_tmp}" \
    || die "failed to extract ${TARBALL_PATH}"

inner_dir="${extract_tmp}/uv-${UV_PLATFORM}"
if [[ ! -d "${inner_dir}" ]]; then
    die "extracted archive missing expected '${inner_dir##*/}' directory"
fi
if [[ ! -x "${inner_dir}/uv" ]]; then
    die "extracted archive missing expected '${inner_dir##*/}/uv' binary"
fi

# Move the binary into place. Preserves the cache dir at build/uv/.fetch-cache.
mkdir -p "${UV_DIR}"
cp "${inner_dir}/uv" "${UV_BIN}"
chmod +x "${UV_BIN}"

# Write the install stamp.
echo "${UV_RELEASE_TAG}" > "${STAMP_PATH}"

# Quick smoke test — fail loud if the extracted binary doesn't run.
if [[ "$(uname -m)" == "arm64" ]]; then
    if ! "${UV_BIN}" --version >/dev/null 2>&1; then
        die "extracted uv failed to execute — binary may be incomplete"
    fi
    actual_version="$("${UV_BIN}" --version 2>&1)"
    log "extracted ${actual_version}"
fi

log "size on disk: $(du -h "${UV_BIN}" | awk '{print $1}')"

echo "uv ${UV_RELEASE_TAG} installed at ${UV_BIN}"
