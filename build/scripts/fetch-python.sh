#!/usr/bin/env bash
#
# fetch-python.sh
#
# Downloads `python-build-standalone` (CPython 3.13, macOS arm64, install_only flavor)
# from the astral-sh/python-build-standalone GitHub releases, verifies its SHA256
# against the release's SHA256SUMS manifest, and extracts it to build/python/.
#
# Pinned release tag: 20260510   (published 2026-05-10, CPython 3.13.13)
# Source: https://github.com/astral-sh/python-build-standalone/releases/tag/20260510
#
# Why pinned: surprise version drift breaks the daemon venv build (TASK-009).
# Bump the PBS_RELEASE_TAG below + bump PBS_CPYTHON_VERSION when intentionally
# upgrading. Re-run on a clean tree to repopulate the SHA cache.
#
# Notes on SHA verification:
#   python-build-standalone publishes ONE consolidated `SHA256SUMS` manifest per
#   release (not per-file `.sha256` sidecars). We download that manifest, grep
#   for the line matching our tarball, and verify with `shasum -a 256 -c`.
#
# This script is idempotent: re-running with a valid `build/python/bin/python3`
# already in place is a no-op.
#
# macOS only. Uses `shasum -a 256` (not `sha256sum` which is Linux-only).
#
# Usage:
#   bash build/scripts/fetch-python.sh
#
# If the script lost its executable bit (fresh git checkouts sometimes do),
# you can still run it via `bash <path>`; no chmod required.

set -euo pipefail

# -----------------------------------------------------------------------------
# Configuration (bump these in lockstep when upgrading)
# -----------------------------------------------------------------------------
PBS_RELEASE_TAG="20260510"
PBS_CPYTHON_VERSION="3.13.13"
PBS_ASSET="cpython-${PBS_CPYTHON_VERSION}+${PBS_RELEASE_TAG}-aarch64-apple-darwin-install_only.tar.gz"
PBS_BASE_URL="https://github.com/astral-sh/python-build-standalone/releases/download/${PBS_RELEASE_TAG}"

# -----------------------------------------------------------------------------
# Paths
# -----------------------------------------------------------------------------
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
PYTHON_DIR="${REPO_ROOT}/build/python"
CACHE_DIR="${PYTHON_DIR}/.fetch-cache"
TARBALL_PATH="${CACHE_DIR}/${PBS_ASSET}"
SHASUMS_PATH="${CACHE_DIR}/SHA256SUMS"
STAMP_PATH="${PYTHON_DIR}/.installed-tag"

# -----------------------------------------------------------------------------
# Logging helpers (all progress to stderr; only final success to stdout)
# -----------------------------------------------------------------------------
log()  { echo "[fetch-python] $*" >&2; }
die()  { echo "[fetch-python] ERROR: $*" >&2; exit 1; }

# -----------------------------------------------------------------------------
# Sanity checks
# -----------------------------------------------------------------------------
if [[ "$(uname -s)" != "Darwin" ]]; then
    die "this script targets macOS only (uname=$(uname -s))"
fi
if [[ "$(uname -m)" != "arm64" ]]; then
    log "warning: host arch is $(uname -m), but the asset targets aarch64-apple-darwin (arm64)."
    log "         the bundle will still be extracted, just unrunnable on this host."
fi
for tool in curl shasum tar; do
    command -v "${tool}" >/dev/null 2>&1 || die "required tool not found in PATH: ${tool}"
done

# -----------------------------------------------------------------------------
# Idempotency check
# -----------------------------------------------------------------------------
if [[ -x "${PYTHON_DIR}/bin/python3" ]] && [[ -f "${STAMP_PATH}" ]]; then
    installed_tag="$(cat "${STAMP_PATH}" 2>/dev/null || true)"
    if [[ "${installed_tag}" == "${PBS_RELEASE_TAG}" ]]; then
        log "python-build-standalone ${PBS_RELEASE_TAG} already extracted at ${PYTHON_DIR}"
        log "already up to date — nothing to do"
        echo "python-build-standalone ${PBS_RELEASE_TAG} (CPython ${PBS_CPYTHON_VERSION}) already installed at ${PYTHON_DIR}"
        exit 0
    fi
    log "stamp file says installed tag is '${installed_tag}', but we want '${PBS_RELEASE_TAG}'"
    log "removing stale extraction and re-fetching"
    rm -rf "${PYTHON_DIR}"
fi

mkdir -p "${CACHE_DIR}"

# -----------------------------------------------------------------------------
# Download tarball (idempotent in cache)
# -----------------------------------------------------------------------------
if [[ -f "${TARBALL_PATH}" ]]; then
    log "tarball already cached at ${TARBALL_PATH}"
else
    log "downloading ${PBS_ASSET} (~25MB) from ${PBS_BASE_URL}"
    # Download to a .partial file then rename so partial downloads aren't
    # mistaken for complete ones on re-run.
    curl --fail --location --show-error --silent \
        --output "${TARBALL_PATH}.partial" \
        "${PBS_BASE_URL}/${PBS_ASSET}" \
        || die "failed to download ${PBS_ASSET}"
    mv "${TARBALL_PATH}.partial" "${TARBALL_PATH}"
    log "downloaded $(du -h "${TARBALL_PATH}" | awk '{print $1}')"
fi

# -----------------------------------------------------------------------------
# Download SHA256SUMS manifest (always re-fetch — small, authoritative)
# -----------------------------------------------------------------------------
log "fetching SHA256SUMS manifest"
curl --fail --location --show-error --silent \
    --output "${SHASUMS_PATH}" \
    "${PBS_BASE_URL}/SHA256SUMS" \
    || die "failed to download SHA256SUMS from ${PBS_BASE_URL}"

# -----------------------------------------------------------------------------
# Verify SHA256
# -----------------------------------------------------------------------------
log "verifying SHA256 of ${PBS_ASSET}"
expected_line="$(grep " ${PBS_ASSET}\$" "${SHASUMS_PATH}" || true)"
if [[ -z "${expected_line}" ]]; then
    die "no SHA256 entry for ${PBS_ASSET} in SHA256SUMS — was the asset renamed or removed?"
fi
expected_sha="$(echo "${expected_line}" | awk '{print $1}')"
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
# python-build-standalone tarballs unpack to a top-level `python/` directory.
# We want the contents of that directory at build/python/ — i.e., we want
# `build/python/bin/python3` (not `build/python/python/bin/python3`).
#
# Strategy: extract into a temp dir, then move the inner `python/` to
# `build/python/`. This keeps the cache dir (.fetch-cache) inside build/python/
# safe — we move .fetch-cache aside, replace build/python/, then move it back.
log "extracting ${PBS_ASSET}"
extract_tmp="$(mktemp -d "${REPO_ROOT}/build/python.extract.XXXXXX")"
trap 'rm -rf "${extract_tmp}"' EXIT

tar -xzf "${TARBALL_PATH}" -C "${extract_tmp}" \
    || die "failed to extract ${TARBALL_PATH}"

if [[ ! -d "${extract_tmp}/python" ]]; then
    die "extracted archive missing expected 'python/' top-level directory"
fi
if [[ ! -x "${extract_tmp}/python/bin/python3" ]]; then
    die "extracted archive missing expected 'python/bin/python3'"
fi

# Preserve the cache dir across the replacement.
cache_backup=""
if [[ -d "${CACHE_DIR}" ]]; then
    cache_backup="$(mktemp -d "${REPO_ROOT}/build/python.cache-backup.XXXXXX")"
    mv "${CACHE_DIR}" "${cache_backup}/cache"
fi

rm -rf "${PYTHON_DIR}"
mv "${extract_tmp}/python" "${PYTHON_DIR}"

# Restore the cache.
if [[ -n "${cache_backup}" ]]; then
    mkdir -p "${PYTHON_DIR}"
    mv "${cache_backup}/cache" "${CACHE_DIR}"
    rmdir "${cache_backup}"
fi

# Write the install stamp.
echo "${PBS_RELEASE_TAG}" > "${STAMP_PATH}"

# Quick smoke test — fail loud if the extracted interpreter doesn't run.
if [[ "$(uname -m)" == "arm64" ]]; then
    if ! "${PYTHON_DIR}/bin/python3" --version >/dev/null 2>&1; then
        die "extracted python3 failed to execute — bundle may be incomplete"
    fi
    actual_version="$("${PYTHON_DIR}/bin/python3" --version 2>&1)"
    log "extracted ${actual_version}"
fi

log "size on disk: $(du -sh "${PYTHON_DIR}" | awk '{print $1}')"

echo "python-build-standalone ${PBS_RELEASE_TAG} (CPython ${PBS_CPYTHON_VERSION}) installed at ${PYTHON_DIR}"
