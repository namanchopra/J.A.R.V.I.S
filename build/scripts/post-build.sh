#!/usr/bin/env bash
#
# Wails post-build hook (darwin).
#
# After `wails build` produces build/bin/Jarvis.app, bundle the Python daemon
# venv and source code into the .app under Contents/Resources/. The venv is
# created by build/scripts/build-daemon-venv.sh (TASK-009) and the daemon
# source lives in scripts/jarvis-daemon/.
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

# Copy the daemon venv (Python interpreter + site-packages) into the bundle.
if [[ -d "${REPO_ROOT}/build/daemon-venv" ]]; then
    echo "post-build: copying daemon-venv -> Resources/python/"
    rsync -a --delete \
        --exclude='__pycache__/' \
        --exclude='tests/' \
        --exclude='*.pyc' \
        --exclude='*.pyo' \
        "${REPO_ROOT}/build/daemon-venv/" "${RESOURCES}/python/"
else
    echo "post-build: WARN: build/daemon-venv/ not found; run build/scripts/build-daemon-venv.sh first" >&2
fi

# Copy the daemon source code into the bundle.
echo "post-build: copying scripts/jarvis-daemon/ -> Resources/jarvis-daemon/"
rsync -a --delete \
    --exclude='__pycache__/' \
    --exclude='tests/' \
    --exclude='*.pyc' \
    --exclude='*.pyo' \
    --exclude='*_test.py' \
    "${REPO_ROOT}/scripts/jarvis-daemon/" "${RESOURCES}/jarvis-daemon/"

echo "post-build: done"
