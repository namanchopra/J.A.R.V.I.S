#!/usr/bin/env bash
#
# Wails post-build hook (darwin).
#
# After `wails build` produces build/bin/Jarvis.app, bundle:
#   - the daemon source code (scripts/jarvis-daemon/) -> Resources/jarvis-daemon/
#   - the pinned uv binary (build/uv/uv)              -> Resources/setup/uv
#   - the first-launch setup orchestrator             -> Resources/setup/install-daemon.sh
#   - libportaudio.2.dylib + install_name rewrite     -> Contents/Frameworks/
#   - bundled models (build/models/)                  -> Resources/models/
#
# As of v0.2.0 (TASK-005) we no longer bundle the full CPython runtime
# (Resources/python/) or the prebuilt daemon venv (Resources/python-venv/).
# Both are now materialized on first launch by Resources/setup/install-daemon.sh
# using the bundled uv binary. This shrinks the DMG by ~140MB.
#
# Wired in via wails.json -> postBuildHooks.darwin (TASK-010).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
APP_BUNDLE="${REPO_ROOT}/build/bin/Jarvis.app"
RESOURCES="${APP_BUNDLE}/Contents/Resources"

# Sanity check: the .app must already exist before we copy into it.
if [[ ! -d "${APP_BUNDLE}" ]]; then
    echo "post-build: Jarvis.app not found at ${APP_BUNDLE}" >&2
    exit 1
fi

# ---------------------------------------------------------------------------
# Bundle libportaudio.2.dylib and rewrite the load path in the binary.
# ---------------------------------------------------------------------------
FRAMEWORKS="${APP_BUNDLE}/Contents/Frameworks"
BINARY="${APP_BUNDLE}/Contents/MacOS/jarvis"
PORTAUDIO_SRC="/opt/homebrew/opt/portaudio/lib/libportaudio.2.dylib"

if [[ -f "${PORTAUDIO_SRC}" ]]; then
    mkdir -p "${FRAMEWORKS}"

    PORTAUDIO_REAL="$(realpath "${PORTAUDIO_SRC}")"
    cp "${PORTAUDIO_REAL}" "${FRAMEWORKS}/libportaudio.2.dylib"
    chmod 755 "${FRAMEWORKS}/libportaudio.2.dylib"

    install_name_tool -id "@executable_path/../Frameworks/libportaudio.2.dylib" \
        "${FRAMEWORKS}/libportaudio.2.dylib"

    OLD_PATH="$(otool -L "${BINARY}" | grep libportaudio | awk '{print $1}')"
    if [[ -n "${OLD_PATH}" ]]; then
        install_name_tool -change "${OLD_PATH}" \
            "@executable_path/../Frameworks/libportaudio.2.dylib" \
            "${BINARY}"
        echo "post-build: bundled libportaudio.2.dylib and rewrote load path"
        echo "post-build:   old: ${OLD_PATH}"
        echo "post-build:   new: @executable_path/../Frameworks/libportaudio.2.dylib"
    else
        echo "post-build: WARN: could not find libportaudio reference in binary" >&2
    fi
else
    echo "post-build: ERROR: libportaudio not found at ${PORTAUDIO_SRC}" >&2
    echo "post-build: Install it with: brew install portaudio" >&2
    exit 1
fi

# ---------------------------------------------------------------------------
# Copy first-launch setup payload (uv + install-daemon.sh) -> Resources/setup/
#
# v0.2.0 (TASK-005): instead of bundling CPython + daemon venv (~150MB), we
# bundle the pinned uv binary and the first-launch installer script. On first
# launch the Go app spawns install-daemon.sh which uses uv to materialize the
# Python interpreter + daemon venv into ~/Library/Application Support/Jarvis/.
# ---------------------------------------------------------------------------
SETUP_DIR="${RESOURCES}/setup"
mkdir -p "${SETUP_DIR}"

UV_SRC="${REPO_ROOT}/build/uv/uv"
if [[ -x "${UV_SRC}" ]]; then
    echo "post-build: copying build/uv/uv -> Resources/setup/uv"
    cp "${UV_SRC}" "${SETUP_DIR}/uv"
    chmod +x "${SETUP_DIR}/uv"
else
    echo "post-build: ERROR: build/uv/uv not found; run build/scripts/fetch-uv.sh first" >&2
    exit 1
fi

INSTALL_DAEMON_SRC="${REPO_ROOT}/scripts/setup/install-daemon.sh"
if [[ -f "${INSTALL_DAEMON_SRC}" ]]; then
    echo "post-build: copying scripts/setup/install-daemon.sh -> Resources/setup/install-daemon.sh"
    cp "${INSTALL_DAEMON_SRC}" "${SETUP_DIR}/install-daemon.sh"
    chmod +x "${SETUP_DIR}/install-daemon.sh"
else
    echo "post-build: ERROR: scripts/setup/install-daemon.sh not found" >&2
    exit 1
fi

# ---------------------------------------------------------------------------
# Copy daemon source
# ---------------------------------------------------------------------------
echo "post-build: copying scripts/jarvis-daemon/ -> Resources/jarvis-daemon/"
rsync -a --delete \
    --exclude='__pycache__/' \
    --exclude='tests/' \
    --exclude='*.pyc' \
    --exclude='*.pyo' \
    --exclude='*_test.py' \
    "${REPO_ROOT}/scripts/jarvis-daemon/" "${RESOURCES}/jarvis-daemon/"

# ---------------------------------------------------------------------------
# Copy bundled models (TASK-014)
# ---------------------------------------------------------------------------
if [[ -d "${REPO_ROOT}/build/models" ]]; then
    echo "post-build: copying build/models/ -> Resources/models/"
    rsync -a --delete "${REPO_ROOT}/build/models/" "${RESOURCES}/models/"

    STAMP="${RESOURCES}/models/.bundled-version"
    {
        echo "# Jarvis bundled models stamp"
        echo "# Generated by build/scripts/post-build.sh (TASK-014)"
        echo "build_date: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
        echo "vibevoice: microsoft/VibeVoice-Realtime-0.5B"
        echo "whisper:   mlx-community/whisper-small.en-mlx"
    } > "${STAMP}"
    echo "post-build: wrote model stamp ${STAMP}"
else
    echo "post-build: WARN: build/models/ not found; run build/scripts/fetch-models.sh first" >&2
fi

# ---------------------------------------------------------------------------
# Codesign the ENTIRE .app bundle LAST (after all resources are in place).
# Must be the final step — any file change after this invalidates the seal.
# ---------------------------------------------------------------------------
# Purge __pycache__ dirs — Python regenerates them at runtime, and any .pyc
# created after signing would invalidate the bundle seal.
find "${APP_BUNDLE}" -type d -name '__pycache__' -exec rm -rf {} + 2>/dev/null || true

codesign --force --deep --options runtime \
    --entitlements "${REPO_ROOT}/build/darwin/entitlements.plist" \
    --sign - "${APP_BUNDLE}"
echo "post-build: codesigned Jarvis.app (ad-hoc, hardened runtime)"

echo "post-build: done"
