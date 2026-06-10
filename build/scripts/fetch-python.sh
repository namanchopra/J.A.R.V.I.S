#!/usr/bin/env bash
#
# fetch-python.sh
#
# Downloads `python-build-standalone` (CPython 3.13) from the
# astral-sh/python-build-standalone GitHub releases, verifies its SHA256
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
# This script is idempotent: re-running with a valid extracted interpreter
# already in place AND matching the pinned tag is a no-op.
#
# Default target is macOS arm64. To cross-fetch a Windows asset on a Mac/Linux
# CI runner, set JARVIS_TARGET_OS=windows and JARVIS_TARGET_ARCH=x64|arm64.
# Native Windows builds should use `build/scripts/fetch-python.ps1` instead
# (TASK-005 of plans/jarvis-windows-port.md).
#
# Uses `shasum -a 256` (preinstalled on macOS).
#
# Usage:
#   bash build/scripts/fetch-python.sh                                # macOS arm64 (default)
#   JARVIS_TARGET_OS=windows JARVIS_TARGET_ARCH=x64   bash <path>     # Win x64
#   JARVIS_TARGET_OS=windows JARVIS_TARGET_ARCH=arm64 bash <path>     # Win arm64
#
# If the script lost its executable bit (fresh git checkouts sometimes do),
# you can still run it via `bash <path>`; no chmod required.

set -euo pipefail

# -----------------------------------------------------------------------------
# Configuration (bump these in lockstep when upgrading)
# -----------------------------------------------------------------------------
PBS_RELEASE_TAG="20260510"
PBS_CPYTHON_VERSION="3.13.13"
PBS_BASE_URL="https://github.com/astral-sh/python-build-standalone/releases/download/${PBS_RELEASE_TAG}"

# -----------------------------------------------------------------------------
# Logging helpers (all progress to stderr; only final success to stdout)
# -----------------------------------------------------------------------------
log()  { echo "[fetch-python] $*" >&2; }
die()  { echo "[fetch-python] ERROR: $*" >&2; exit 1; }

# -----------------------------------------------------------------------------
# Target selection
# -----------------------------------------------------------------------------
# JARVIS_TARGET_OS:   "darwin" (default) | "windows"
# JARVIS_TARGET_ARCH: "arm64" (default)  | "x64" | "amd64"  (windows only;
#                                                            darwin is always arm64)
JARVIS_TARGET_OS="${JARVIS_TARGET_OS:-darwin}"
JARVIS_TARGET_ARCH="${JARVIS_TARGET_ARCH:-arm64}"

case "${JARVIS_TARGET_OS}" in
    darwin)
        # python-build-standalone publishes both `install_only` and
        # `install_only_stripped` flavors for darwin. We use `install_only` for
        # macOS to retain debug symbols (we ship signed + notarized).
        PBS_ASSET="cpython-${PBS_CPYTHON_VERSION}+${PBS_RELEASE_TAG}-aarch64-apple-darwin-install_only.tar.gz"
        EXTRACTED_DIR_NAME="python"
        SMOKE_TEST_BINARY="bin/python3"
        ;;
    windows)
        # Windows assets ship as `install_only_stripped.tar.gz` — smaller without
        # debug symbols, which matters more for Windows since we don't sign with
        # a code-signing cert yet (TASK-018 ships unsigned zips).
        case "${JARVIS_TARGET_ARCH}" in
            x64|amd64) pbs_arch="x86_64" ;;
            arm64)     pbs_arch="aarch64" ;;
            *)         die "unsupported JARVIS_TARGET_ARCH for windows: ${JARVIS_TARGET_ARCH} (want x64|amd64|arm64)" ;;
        esac
        PBS_ASSET="cpython-${PBS_CPYTHON_VERSION}+${PBS_RELEASE_TAG}-${pbs_arch}-pc-windows-msvc-install_only_stripped.tar.gz"
        EXTRACTED_DIR_NAME="python"
        # On Windows, python-build-standalone puts python.exe at the top level
        # of the extracted python/ directory (no bin/ subdirectory).
        SMOKE_TEST_BINARY="python.exe"
        ;;
    *)
        die "unsupported JARVIS_TARGET_OS: ${JARVIS_TARGET_OS} (want darwin|windows)"
        ;;
esac

log "target: ${JARVIS_TARGET_OS}/${JARVIS_TARGET_ARCH} -> ${PBS_ASSET}"

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
# Sanity checks
# -----------------------------------------------------------------------------
# Host OS gating: the default (darwin target) only runs on macOS. Windows-target
# fetches can be invoked from any *nix host (CI macOS runners cross-fetch the
# Windows tarball before zipping it into the release bundle).
if [[ "${JARVIS_TARGET_OS}" == "darwin" ]]; then
    if [[ "$(uname -s)" != "Darwin" ]]; then
        die "darwin-target fetch must run on macOS (uname=$(uname -s)); set JARVIS_TARGET_OS=windows to cross-fetch"
    fi
    if [[ "$(uname -m)" != "arm64" ]]; then
        log "warning: host arch is $(uname -m), but the asset targets aarch64-apple-darwin (arm64)."
        log "         the bundle will still be extracted, just unrunnable on this host."
    fi
fi
for tool in curl shasum tar; do
    command -v "${tool}" >/dev/null 2>&1 || die "required tool not found in PATH: ${tool}"
done

# -----------------------------------------------------------------------------
# Idempotency check
# -----------------------------------------------------------------------------
# The stamp file format is "<release-tag> <target-os> <target-arch>". A target
# switch (e.g. darwin -> windows on the same CI runner) forces a re-fetch.
want_stamp="${PBS_RELEASE_TAG} ${JARVIS_TARGET_OS} ${JARVIS_TARGET_ARCH}"

if [[ -e "${PYTHON_DIR}/${SMOKE_TEST_BINARY}" ]] && [[ -f "${STAMP_PATH}" ]]; then
    installed_stamp="$(cat "${STAMP_PATH}" 2>/dev/null || true)"
    # Back-compat: pre-Windows-port stamps were just the release tag with no
    # OS/arch suffix. Treat them as darwin/arm64 (the only previous target).
    legacy_stamp_for_darwin="${PBS_RELEASE_TAG}"
    if [[ "${installed_stamp}" == "${want_stamp}" ]] \
       || { [[ "${JARVIS_TARGET_OS}" == "darwin" ]] && [[ "${installed_stamp}" == "${legacy_stamp_for_darwin}" ]]; }; then
        log "python-build-standalone ${PBS_RELEASE_TAG} (${JARVIS_TARGET_OS}/${JARVIS_TARGET_ARCH}) already extracted at ${PYTHON_DIR}"
        log "already up to date — nothing to do"
        # Refresh stamp into new format if upgrading from legacy.
        if [[ "${installed_stamp}" != "${want_stamp}" ]]; then
            echo "${want_stamp}" > "${STAMP_PATH}"
        fi
        echo "python-build-standalone ${PBS_RELEASE_TAG} (CPython ${PBS_CPYTHON_VERSION}, ${JARVIS_TARGET_OS}/${JARVIS_TARGET_ARCH}) already installed at ${PYTHON_DIR}"
        exit 0
    fi
    log "stamp file says installed '${installed_stamp}', but we want '${want_stamp}'"
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
# We want the contents of that directory at build/python/ — i.e., on darwin we
# want `build/python/bin/python3`, and on windows we want `build/python/python.exe`
# (not `build/python/python/...`).
#
# Strategy: extract into a temp dir, then move the inner `python/` to
# `build/python/`. This keeps the cache dir (.fetch-cache) inside build/python/
# safe — we move .fetch-cache aside, replace build/python/, then move it back.
log "extracting ${PBS_ASSET}"
extract_tmp="$(mktemp -d "${REPO_ROOT}/build/python.extract.XXXXXX")"
trap 'rm -rf "${extract_tmp}"' EXIT

tar -xzf "${TARBALL_PATH}" -C "${extract_tmp}" \
    || die "failed to extract ${TARBALL_PATH}"

if [[ ! -d "${extract_tmp}/${EXTRACTED_DIR_NAME}" ]]; then
    die "extracted archive missing expected '${EXTRACTED_DIR_NAME}/' top-level directory"
fi
# Windows assets land python.exe at python/python.exe (not executable bit on
# darwin filesystems, so use -e not -x).
if [[ ! -e "${extract_tmp}/${EXTRACTED_DIR_NAME}/${SMOKE_TEST_BINARY}" ]]; then
    die "extracted archive missing expected '${EXTRACTED_DIR_NAME}/${SMOKE_TEST_BINARY}'"
fi

# Preserve the cache dir across the replacement.
cache_backup=""
if [[ -d "${CACHE_DIR}" ]]; then
    cache_backup="$(mktemp -d "${REPO_ROOT}/build/python.cache-backup.XXXXXX")"
    mv "${CACHE_DIR}" "${cache_backup}/cache"
fi

rm -rf "${PYTHON_DIR}"
mv "${extract_tmp}/${EXTRACTED_DIR_NAME}" "${PYTHON_DIR}"

# Restore the cache.
if [[ -n "${cache_backup}" ]]; then
    mkdir -p "${PYTHON_DIR}"
    mv "${cache_backup}/cache" "${CACHE_DIR}"
    rmdir "${cache_backup}"
fi

# Write the install stamp.
echo "${want_stamp}" > "${STAMP_PATH}"

# Quick smoke test — fail loud if the extracted interpreter doesn't run.
# Only attempt to invoke the binary when the target arch matches the host;
# cross-fetches (e.g. windows tarball on a darwin runner) just verify the
# binary file exists.
if [[ "${JARVIS_TARGET_OS}" == "darwin" ]] && [[ "$(uname -s)" == "Darwin" ]] && [[ "$(uname -m)" == "arm64" ]]; then
    if ! "${PYTHON_DIR}/${SMOKE_TEST_BINARY}" --version >/dev/null 2>&1; then
        die "extracted python3 failed to execute — bundle may be incomplete"
    fi
    actual_version="$("${PYTHON_DIR}/${SMOKE_TEST_BINARY}" --version 2>&1)"
    log "extracted ${actual_version}"
else
    log "cross-fetch (host=$(uname -s)/$(uname -m) target=${JARVIS_TARGET_OS}/${JARVIS_TARGET_ARCH}); skipping --version smoke test"
fi

log "size on disk: $(du -sh "${PYTHON_DIR}" | awk '{print $1}')"

echo "python-build-standalone ${PBS_RELEASE_TAG} (CPython ${PBS_CPYTHON_VERSION}, ${JARVIS_TARGET_OS}/${JARVIS_TARGET_ARCH}) installed at ${PYTHON_DIR}"
