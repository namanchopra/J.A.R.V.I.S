#!/usr/bin/env bash
#
# install-daemon.sh
#
# v0.2.0 first-launch orchestrator. Spawned by Go's app_setup.go (TASK-006) on
# the first launch where ~/.jarvis/.setup-version-0.2.0 is missing. Installs
# the Python interpreter (Phase 1) and the daemon's virtual environment + source
# tree (Phase 2) into ~/.jarvis/. Phases 3 and 4 (VibeVoice + Whisper weight
# downloads) are NOT this script's responsibility — the daemon's own
# model_status.prefetch_models handles them post-launch, and the Go bridge in
# TASK-007 translates those events to the same setup-progress channel.
#
# Usage:
#   bash install-daemon.sh <uv-binary> <bundled-daemon-source>
#
#   <uv-binary>              absolute path to the bundled `uv` binary
#                             (TASK-005 places this under
#                             <.app>/Contents/Resources/setup/uv)
#   <bundled-daemon-source>  absolute path to the bundled jarvis-daemon source
#                             tree (TASK-005 places this under
#                             <.app>/Contents/Resources/jarvis-daemon/)
#
# Stderr contract: every recognised line begins with one of the PHASE_* prefixes
# documented in docs/setup-events.md. All other stderr lines are passed through
# untouched and tee'd to ~/.jarvis/logs/setup.log so Go can show them in the
# log viewer. Stdout is reserved for tool output; consumers should not parse it.
#
# Resumability: per-phase sentinel files (~/.jarvis/python/.fetch-complete and
# ~/.jarvis/jarvis-daemon-env/.venv-complete) let the script no-op completed
# phases on retry. The final sentinel ~/.jarvis/.setup-version-0.2.0 is written
# atomically (tmp+rename) only after both phases succeed.
#
# macOS only. Apple Silicon (arm64) only. Requires Xcode Command Line Tools.

set -euo pipefail

# -----------------------------------------------------------------------------
# Constants — keep in lockstep with build/scripts/fetch-python.sh
# -----------------------------------------------------------------------------
SETUP_VERSION="0.2.0"
PBS_RELEASE_TAG="20260510"
PBS_CPYTHON_VERSION="3.13.13"
PBS_ASSET="cpython-${PBS_CPYTHON_VERSION}+${PBS_RELEASE_TAG}-aarch64-apple-darwin-install_only.tar.gz"
PBS_BASE_URL="https://github.com/astral-sh/python-build-standalone/releases/download/${PBS_RELEASE_TAG}"

# Disk free requirement: 4 GB in KB (4 * 1024 * 1024).
MIN_FREE_DISK_KB=4194304

# Download progress poll interval, in seconds. Used during phase 1 only.
PROGRESS_POLL_INTERVAL=2

# -----------------------------------------------------------------------------
# Argument parsing
# -----------------------------------------------------------------------------
if [[ "$#" -ne 2 ]]; then
    echo "usage: install-daemon.sh <uv-binary> <bundled-daemon-source>" >&2
    exit 2
fi
UV_BIN="$1"
BUNDLED_DAEMON_SRC="$2"

# -----------------------------------------------------------------------------
# Paths
# -----------------------------------------------------------------------------
JARVIS_HOME="${HOME}/.jarvis"
LOGS_DIR="${JARVIS_HOME}/logs"
SETUP_LOG="${LOGS_DIR}/setup.log"

PYTHON_DIR="${JARVIS_HOME}/python"
PYTHON_CACHE_DIR="${PYTHON_DIR}/.fetch-cache"
PYTHON_SENTINEL="${PYTHON_DIR}/.fetch-complete"
PYTHON_BIN="${PYTHON_DIR}/bin/python3"

VENV_DIR="${JARVIS_HOME}/jarvis-daemon-env"
VENV_SENTINEL="${VENV_DIR}/.venv-complete"
VENV_REQ_SHA_PATH="${VENV_DIR}/.requirements-sha256"

DAEMON_SRC_DIR="${JARVIS_HOME}/jarvis-daemon"
FINAL_SENTINEL="${JARVIS_HOME}/.setup-version-${SETUP_VERSION}"

# Track current phase for the SIGTERM trap. Empty when no phase is active.
CURRENT_PHASE=""

# -----------------------------------------------------------------------------
# Logging — every log line gets tee'd to setup.log. NEVER write PHASE_* prefixes
# from log() — those go through emit_*.
# -----------------------------------------------------------------------------
ensure_log_dir() {
    mkdir -p "${LOGS_DIR}"
    # Touch so the file exists even if no log lines fire before an error.
    : >>"${SETUP_LOG}"
}

log() {
    # Free-form line: NOT a PHASE_* marker. Tee to stderr and setup.log.
    local line="[install-daemon] $*"
    echo "${line}" >&2
    if [[ -d "${LOGS_DIR}" ]]; then
        echo "${line}" >>"${SETUP_LOG}" 2>/dev/null || true
    fi
}

# -----------------------------------------------------------------------------
# Event emitters — these write the canonical stderr markers documented in
# docs/setup-events.md. They MUST NOT change format without updating that doc
# and notifying TASK-006 (the Go parser).
# -----------------------------------------------------------------------------
emit_phase() {
    # emit_phase <phase>
    CURRENT_PHASE="$1"
    echo "PHASE: $1" >&2
}

emit_progress() {
    # emit_progress <0..100>
    local pct="$1"
    if [[ "${pct}" -lt 0 ]]; then pct=0; fi
    if [[ "${pct}" -gt 100 ]]; then pct=100; fi
    echo "PHASE_PROGRESS: ${pct}" >&2
}

emit_bytes() {
    # emit_bytes <done> <total>
    echo "PHASE_BYTES: $1 / $2" >&2
}

emit_eta() {
    # emit_eta <seconds>
    echo "PHASE_ETA: $1" >&2
}

emit_done() {
    # emit_done <phase>
    echo "PHASE_DONE: $1" >&2
    CURRENT_PHASE=""
}

emit_error() {
    # emit_error <single-line-message>
    # Strip embedded newlines per docs/setup-events.md — error MUST be single-line.
    local msg
    msg="$(printf '%s' "$1" | tr '\n' ';' | tr -s ' ')"
    echo "PHASE_ERROR: ${msg}" >&2
}

# -----------------------------------------------------------------------------
# SIGTERM/SIGINT trap — per docs/setup-events.md unhappy path D, we must NOT
# emit PHASE_ERROR when the parent kills us. Exit silently with non-zero so
# `cmd.Wait()` on the Go side sees the signal-killed status.
# -----------------------------------------------------------------------------
on_signal() {
    # Avoid recursion if a trap fires inside a trap.
    trap - INT TERM
    log "received termination signal — exiting silently (current phase: ${CURRENT_PHASE:-none})"
    exit 130
}
trap on_signal INT TERM

# -----------------------------------------------------------------------------
# Sentinel + idempotency helpers
# -----------------------------------------------------------------------------
check_sentinel() {
    # check_sentinel <path>  -> rc 0 if present, 1 otherwise
    [[ -f "$1" ]]
}

sha256_file() {
    # sha256_file <path>  -> hex digest on stdout
    shasum -a 256 "$1" | awk '{print $1}'
}

# -----------------------------------------------------------------------------
# Preflight — fail-fast checks before touching any disk state. Each failure
# emits a single PHASE_ERROR + exits 1, with the phase set to the next phase
# we WOULD have started. This lets the SetupScreen render the error under
# phase 1's row.
# -----------------------------------------------------------------------------
# -----------------------------------------------------------------------------
# ensure_portaudio
#
# pyaudio 0.2.14 ships wheels for Python 3.7-3.11 on macOS but NOT for 3.12+.
# On the daemon's CPython 3.13.x the wheel is missing and pip falls back to
# compiling from src/pyaudio/device_api.c, which #includes <portaudio.h>.
# Without the portaudio system library that header isn't on the include
# search path and the build dies with:
#
#   fatal error: 'portaudio.h' file not found
#
# Detection order:
#   1. Header already present in /opt/homebrew/include or /usr/local/include
#      -> nothing to do.
#   2. Homebrew installed -> `brew install portaudio`.
#   3. No brew -> emit a clear error with remediation steps and exit.
#
# After this returns OK, the venv_install phase exports CFLAGS / LDFLAGS
# pointing at the brew prefix so pyaudio's setup.py finds the headers + libs.
# -----------------------------------------------------------------------------
ensure_portaudio() {
    log "preflight: checking portaudio"

    # Common header locations on macOS (Apple Silicon brew + Intel brew).
    if [[ -f /opt/homebrew/include/portaudio.h ]] \
            || [[ -f /usr/local/include/portaudio.h ]]; then
        log "preflight: portaudio.h found, skipping install"
        return 0
    fi

    # Brew is the standard install path on Mac dev machines. If present,
    # use it; we won't auto-install brew itself (that's a sudo-running
    # one-liner the user should run consciously).
    if command -v brew >/dev/null 2>&1; then
        log "preflight: portaudio.h missing; installing via brew"
        if brew install portaudio >>"${SETUP_LOG}" 2>&1; then
            log "preflight: brew install portaudio ok"
            # Re-verify so we fail fast if the install was a no-op for any reason.
            if [[ -f /opt/homebrew/include/portaudio.h ]] \
                    || [[ -f /usr/local/include/portaudio.h ]]; then
                return 0
            fi
            emit_error "brew install portaudio finished but portaudio.h still not found in /opt/homebrew/include or /usr/local/include"
            exit 1
        fi
        emit_error "brew install portaudio failed; see setup.log for details"
        exit 1
    fi

    # No brew. Fail with the exact remediation.
    emit_error "portaudio system library is required for the voice pipeline. Install it with one of:

  (a) Install Homebrew (one-time), then retry setup:
      /bin/bash -c \"\$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)\"
      brew install portaudio

  (b) Or download portaudio from http://files.portaudio.com/download.html
      and install the dylib + header into /usr/local/lib + /usr/local/include.

Then re-launch Jarvis or click 'Re-run setup' in Settings -> Diagnostics."
    exit 1
}

preflight() {
    ensure_log_dir
    log "preflight: starting"

    # Emit phase 1 immediately so the SetupScreen shows "python_install:
    # in-progress" during preflight instead of all-pending rows. Without
    # this every silent ms of preflight feels like a hang to the user.
    emit_phase "python_install"
    emit_progress 0

    # Arch must be arm64 — python-build-standalone asset is arm64-only.
    log "preflight: checking arch"
    local arch
    arch="$(uname -m)"
    if [[ "${arch}" != "arm64" ]]; then
        emit_error "Jarvis requires Apple Silicon (arm64); detected ${arch}"
        exit 1
    fi

    # Xcode CLI tools required by some wheels that fall back to source builds.
    log "preflight: checking xcode CLT"
    if ! xcode-select -p >/dev/null 2>&1; then
        emit_error "Xcode Command Line Tools are not installed; run 'xcode-select --install' and retry setup"
        exit 1
    fi

    # Disk free in $HOME — `df -k` returns 1K blocks, NR==2 col $4 is "Available".
    log "preflight: checking disk space"
    local free_kb
    free_kb="$(df -k "${HOME}" | awk 'NR==2 {print $4}' 2>/dev/null || echo 0)"
    if [[ -z "${free_kb}" || "${free_kb}" -lt "${MIN_FREE_DISK_KB}" ]]; then
        emit_error "insufficient disk space in ${HOME}: need 4 GB free, have $((free_kb / 1024)) MB"
        exit 1
    fi

    # Required external tools.
    log "preflight: checking PATH tools"
    for tool in curl shasum tar awk df mkdir mv rm rsync stat; do
        if ! command -v "${tool}" >/dev/null 2>&1; then
            emit_error "required tool not found in PATH: ${tool}"
            exit 1
        fi
    done

    # portaudio is required by the pyaudio Python package, which has no
    # pre-built arm64 wheel for Python 3.13 — pip falls back to a source
    # build that fails with "fatal error: 'portaudio.h' file not found"
    # if the system library isn't installed. Handle this here so the
    # phase 2 `uv pip install` step never hits that error.
    ensure_portaudio

    # uv binary must exist and be executable.
    log "preflight: checking uv binary at ${UV_BIN}"
    if [[ ! -x "${UV_BIN}" ]]; then
        emit_error "uv binary not found or not executable at ${UV_BIN}"
        exit 1
    fi

    # Bundled daemon source must be a directory containing requirements.txt.
    log "preflight: checking bundled daemon source at ${BUNDLED_DAEMON_SRC}"
    if [[ ! -d "${BUNDLED_DAEMON_SRC}" ]]; then
        emit_error "bundled daemon source not found at ${BUNDLED_DAEMON_SRC}"
        exit 1
    fi
    if [[ ! -f "${BUNDLED_DAEMON_SRC}/requirements.txt" ]]; then
        emit_error "bundled daemon source missing requirements.txt at ${BUNDLED_DAEMON_SRC}/requirements.txt"
        exit 1
    fi

    mkdir -p "${JARVIS_HOME}"
    log "preflight: ok (arch=arm64, free=$((free_kb / 1024)) MB, uv=${UV_BIN})"
}

# -----------------------------------------------------------------------------
# Phase 1 — python_install
#
# Downloads python-build-standalone (CPython 3.13.13 aarch64-apple-darwin
# install_only) into ~/.jarvis/python/.fetch-cache/, SHA-verifies against the
# release's SHA256SUMS manifest, extracts, smoke-tests, and writes
# ~/.jarvis/python/.fetch-complete.
#
# Skip path: if the sentinel + a working bin/python3 already exist, we emit
# PHASE -> PHASE_PROGRESS 100 -> PHASE_DONE so progress reporting stays
# consistent even on a resumed run.
# -----------------------------------------------------------------------------
phase_python() {
    emit_phase "python_install"
    log "phase 1: python_install — starting"

    if check_sentinel "${PYTHON_SENTINEL}" && [[ -x "${PYTHON_BIN}" ]]; then
        log "phase 1: python already installed (sentinel + interpreter present); skipping"
        emit_progress 100
        emit_done "python_install"
        return 0
    fi

    mkdir -p "${PYTHON_CACHE_DIR}"
    local tarball="${PYTHON_CACHE_DIR}/${PBS_ASSET}"
    local tarball_partial="${tarball}.partial"
    local shasums="${PYTHON_CACHE_DIR}/SHA256SUMS"

    # Always (re-)fetch SHA256SUMS — small and authoritative.
    log "phase 1: fetching SHA256SUMS"
    if ! curl --fail --location --show-error --silent --retry 3 --retry-delay 2 \
            --output "${shasums}" \
            "${PBS_BASE_URL}/SHA256SUMS" 2>>"${SETUP_LOG}"; then
        emit_error "failed to download SHA256SUMS from ${PBS_BASE_URL}; check your network connection"
        exit 1
    fi

    local expected_line expected_sha
    expected_line="$(grep " ${PBS_ASSET}\$" "${shasums}" || true)"
    if [[ -z "${expected_line}" ]]; then
        emit_error "no SHA256 entry for ${PBS_ASSET} in SHA256SUMS — asset renamed or removed upstream"
        exit 1
    fi
    expected_sha="$(echo "${expected_line}" | awk '{print $1}')"

    # If we already have a fully-downloaded tarball whose SHA matches, reuse it.
    local need_download=1
    if [[ -f "${tarball}" ]]; then
        local actual_sha
        actual_sha="$(sha256_file "${tarball}")"
        if [[ "${actual_sha}" == "${expected_sha}" ]]; then
            log "phase 1: cached tarball SHA matches; reusing"
            need_download=0
            emit_progress 100
        else
            log "phase 1: cached tarball SHA mismatch; re-downloading"
            rm -f "${tarball}"
        fi
    fi

    if [[ "${need_download}" -eq 1 ]]; then
        # Remove any stale partial from a previous interrupted run. We can't
        # safely resume a curl --range without HTTP server cooperation; cleaner
        # to start fresh and let curl --retry handle transient drops.
        rm -f "${tarball_partial}"

        # Total bytes — best-effort from a HEAD request. Used for byte-progress
        # math. If the HEAD fails we still try the GET and just skip byte math.
        local total_bytes
        total_bytes="$(curl --silent --show-error --location --head --fail \
                --retry 2 --retry-delay 1 \
                "${PBS_BASE_URL}/${PBS_ASSET}" 2>>"${SETUP_LOG}" \
                | awk 'tolower($1) == "content-length:" { gsub(/\r/,"",$2); print $2; exit }')"
        if ! [[ "${total_bytes}" =~ ^[0-9]+$ ]] || [[ "${total_bytes}" -le 0 ]]; then
            total_bytes=""
        fi

        log "phase 1: downloading ${PBS_ASSET} (~$((${total_bytes:-95000000} / 1024 / 1024)) MB)"
        emit_progress 0
        if [[ -n "${total_bytes}" ]]; then
            emit_bytes 0 "${total_bytes}"
        fi

        # Spawn curl in the background so we can poll the partial file size for
        # progress events. --retry handles transient network drops; on hard
        # failure curl exits non-zero and we emit PHASE_ERROR.
        local curl_log="${PYTHON_CACHE_DIR}/curl.log"
        : >"${curl_log}"
        curl --fail --location --show-error --silent \
            --retry 3 --retry-delay 2 \
            --output "${tarball_partial}" \
            "${PBS_BASE_URL}/${PBS_ASSET}" >>"${SETUP_LOG}" 2>"${curl_log}" &
        local curl_pid=$!

        local started_at
        started_at="$(date +%s)"
        local last_pct=0
        while kill -0 "${curl_pid}" 2>/dev/null; do
            sleep "${PROGRESS_POLL_INTERVAL}"
            local size_now=0
            if [[ -f "${tarball_partial}" ]]; then
                size_now="$(stat -f %z "${tarball_partial}" 2>/dev/null || echo 0)"
            fi
            if [[ -n "${total_bytes}" && "${total_bytes}" -gt 0 ]]; then
                local pct=$(( size_now * 100 / total_bytes ))
                if [[ "${pct}" -gt 100 ]]; then pct=100; fi
                # Avoid spamming identical progress values.
                if [[ "${pct}" -ne "${last_pct}" ]]; then
                    emit_bytes "${size_now}" "${total_bytes}"
                    emit_progress "${pct}"
                    last_pct="${pct}"
                fi
                # Emit ETA once we have meaningful samples (>=2s elapsed,
                # nonzero bytes downloaded).
                local elapsed=$(( $(date +%s) - started_at ))
                if [[ "${elapsed}" -ge 2 && "${size_now}" -gt 0 ]]; then
                    local rate=$(( size_now / elapsed ))   # B/s
                    if [[ "${rate}" -gt 0 ]]; then
                        local remaining=$(( total_bytes - size_now ))
                        local eta=$(( remaining / rate ))
                        if [[ "${eta}" -ge 0 ]]; then
                            emit_eta "${eta}"
                        fi
                    fi
                fi
            fi
        done

        # Reap curl's exit status.
        if ! wait "${curl_pid}"; then
            local err_msg
            err_msg="$(tr '\n' ';' <"${curl_log}" 2>/dev/null | head -c 240)"
            rm -f "${tarball_partial}"
            emit_error "failed to download python-build-standalone after retries: ${err_msg:-curl exited non-zero}"
            exit 1
        fi

        mv "${tarball_partial}" "${tarball}"
        emit_progress 100
        log "phase 1: download complete"

        # Verify SHA256 after move.
        local actual_sha
        actual_sha="$(sha256_file "${tarball}")"
        if [[ "${actual_sha}" != "${expected_sha}" ]]; then
            rm -f "${tarball}"
            emit_error "SHA256 mismatch on downloaded tarball: expected ${expected_sha} got ${actual_sha}"
            exit 1
        fi
        log "phase 1: SHA256 verified (${actual_sha})"
    fi

    # Extract into a temp dir, then atomically swap into PYTHON_DIR.
    # python-build-standalone unpacks to a top-level `python/` dir.
    log "phase 1: extracting"
    local extract_tmp
    extract_tmp="$(mktemp -d "${JARVIS_HOME}/python.extract.XXXXXX")"
    if ! tar -xzf "${tarball}" -C "${extract_tmp}" 2>>"${SETUP_LOG}"; then
        rm -rf "${extract_tmp}"
        emit_error "failed to extract ${PBS_ASSET}"
        exit 1
    fi
    if [[ ! -x "${extract_tmp}/python/bin/python3" ]]; then
        rm -rf "${extract_tmp}"
        emit_error "extracted python archive missing expected bin/python3"
        exit 1
    fi

    # Preserve our cache dir (it lives INSIDE PYTHON_DIR) across the swap.
    local cache_backup=""
    if [[ -d "${PYTHON_CACHE_DIR}" ]]; then
        cache_backup="$(mktemp -d "${JARVIS_HOME}/python.cache-backup.XXXXXX")"
        mv "${PYTHON_CACHE_DIR}" "${cache_backup}/cache"
    fi

    rm -rf "${PYTHON_DIR}"
    mv "${extract_tmp}/python" "${PYTHON_DIR}"
    rm -rf "${extract_tmp}"

    if [[ -n "${cache_backup}" ]]; then
        mkdir -p "${PYTHON_DIR}"
        mv "${cache_backup}/cache" "${PYTHON_CACHE_DIR}"
        rmdir "${cache_backup}"
    fi

    # Smoke test the extracted interpreter.
    if ! "${PYTHON_BIN}" --version >>"${SETUP_LOG}" 2>&1; then
        emit_error "extracted python3 failed to execute — bundle may be incomplete"
        exit 1
    fi

    # Write the per-phase sentinel atomically.
    local sentinel_tmp
    sentinel_tmp="$(mktemp "${PYTHON_DIR}/.fetch-complete.XXXXXX")"
    printf 'pbs_tag=%s\ncpython_version=%s\nsha256=%s\n' \
        "${PBS_RELEASE_TAG}" "${PBS_CPYTHON_VERSION}" "${expected_sha}" \
        >"${sentinel_tmp}"
    mv "${sentinel_tmp}" "${PYTHON_SENTINEL}"

    emit_done "python_install"
    log "phase 1: python_install — done"
}

# -----------------------------------------------------------------------------
# Phase 2 — venv_install
#
#   1. `uv venv ${VENV_DIR} --python ${PYTHON_BIN}`
#   2. `uv pip install -r <bundled>/requirements.txt`
#   3. rsync the bundled daemon source into ~/.jarvis/jarvis-daemon/
#   4. Write ~/.jarvis/jarvis-daemon-env/.venv-complete
#
# Skip path: sentinel present AND the requirements.txt sha matches the one
# recorded in .requirements-sha256 -> no-op.
# -----------------------------------------------------------------------------
phase_venv() {
    emit_phase "venv_install"
    log "phase 2: venv_install — starting"

    local req_path="${BUNDLED_DAEMON_SRC}/requirements.txt"
    local req_sha
    req_sha="$(sha256_file "${req_path}")"

    if check_sentinel "${VENV_SENTINEL}" \
            && [[ -x "${VENV_DIR}/bin/python3" || -x "${VENV_DIR}/bin/python" ]] \
            && [[ -f "${VENV_REQ_SHA_PATH}" ]] \
            && [[ "$(cat "${VENV_REQ_SHA_PATH}" 2>/dev/null || true)" == "${req_sha}" ]]; then
        log "phase 2: venv already installed (sentinel + matching requirements sha); skipping"
        emit_progress 100
        # Still sync the daemon source in case the bundle changed without
        # bumping requirements. rsync is cheap when there's nothing to do.
        if ! sync_daemon_source; then
            emit_error "failed to sync daemon source to ${DAEMON_SRC_DIR}"
            exit 1
        fi
        emit_done "venv_install"
        return 0
    fi

    # Phase 2 has no fine-grained byte counters — uv emits its own free-form
    # progress lines which we tee to setup.log. We emit coarse PROGRESS pings
    # at the major step boundaries so the React phase bar advances.
    emit_progress 5

    # Step 1 — create the venv.
    log "phase 2: creating venv at ${VENV_DIR}"
    if [[ -d "${VENV_DIR}" ]]; then
        # Wipe any partially-installed venv from a prior interrupted run. The
        # sentinel-check above already proved this venv isn't ready.
        rm -rf "${VENV_DIR}"
    fi
    if ! "${UV_BIN}" venv "${VENV_DIR}" --python "${PYTHON_BIN}" \
            >>"${SETUP_LOG}" 2>>"${SETUP_LOG}"; then
        emit_error "uv venv failed to create environment at ${VENV_DIR}"
        exit 1
    fi
    emit_progress 20

    # Step 2 — install requirements. Run uv pip install with VIRTUAL_ENV set so
    # uv resolves the target. Tee both stdout + stderr to setup.log for the log
    # viewer; we don't translate uv's progress lines into PHASE_PROGRESS because
    # uv emits free-form progress that doesn't carry meaningful percentages.
    log "phase 2: installing requirements via uv pip install"
    emit_progress 30

    # pyaudio compiles from source on Python 3.13 (no arm64 wheel). Point its
    # build at the portaudio install ensure_portaudio prepared during preflight.
    # Auto-detect Apple Silicon brew (/opt/homebrew) first, fall back to Intel
    # (/usr/local). Setting both prefixes is safe — clang ignores include paths
    # that don't exist.
    local portaudio_cflags="-I/opt/homebrew/include -I/usr/local/include"
    local portaudio_ldflags="-L/opt/homebrew/lib -L/usr/local/lib"

    local uv_err
    uv_err="$(mktemp -t install-daemon.uv-err.XXXXXX)"
    if ! VIRTUAL_ENV="${VENV_DIR}" \
            CFLAGS="${portaudio_cflags}" \
            LDFLAGS="${portaudio_ldflags}" \
            CPATH="/opt/homebrew/include:/usr/local/include" \
            LIBRARY_PATH="/opt/homebrew/lib:/usr/local/lib" \
            "${UV_BIN}" pip install \
            --python "${VENV_DIR}/bin/python3" \
            -r "${req_path}" \
            >>"${SETUP_LOG}" 2>"${uv_err}"; then
        local err_msg
        err_msg="$(tail -c 400 "${uv_err}" 2>/dev/null | tr '\n' ';' | tr -s ' ')"
        rm -f "${uv_err}"
        emit_error "uv pip install failed: ${err_msg:-see setup.log for details}"
        exit 1
    fi
    # Also tee the captured stderr to setup.log so it's in the log viewer.
    cat "${uv_err}" >>"${SETUP_LOG}" 2>/dev/null || true
    rm -f "${uv_err}"
    emit_progress 85

    # Step 3 — copy daemon source.
    if ! sync_daemon_source; then
        emit_error "failed to sync daemon source to ${DAEMON_SRC_DIR}"
        exit 1
    fi
    emit_progress 95

    # Step 4 — record requirements sha + write sentinel atomically.
    local sha_tmp sentinel_tmp
    sha_tmp="$(mktemp "${VENV_DIR}/.requirements-sha256.XXXXXX")"
    printf '%s\n' "${req_sha}" >"${sha_tmp}"
    mv "${sha_tmp}" "${VENV_REQ_SHA_PATH}"

    sentinel_tmp="$(mktemp "${VENV_DIR}/.venv-complete.XXXXXX")"
    printf 'requirements_sha256=%s\nuv_binary=%s\n' "${req_sha}" "${UV_BIN}" \
        >"${sentinel_tmp}"
    mv "${sentinel_tmp}" "${VENV_SENTINEL}"

    emit_progress 100
    emit_done "venv_install"
    log "phase 2: venv_install — done"
}

# -----------------------------------------------------------------------------
# sync_daemon_source — rsync the bundled daemon tree to ~/.jarvis/jarvis-daemon/.
# Excludes __pycache__ and tests/ to keep the install lean. Returns rc.
# -----------------------------------------------------------------------------
sync_daemon_source() {
    log "syncing daemon source: ${BUNDLED_DAEMON_SRC}/ -> ${DAEMON_SRC_DIR}/"
    mkdir -p "${DAEMON_SRC_DIR}"
    rsync -a --delete \
        --exclude '__pycache__/' \
        --exclude '*.pyc' \
        --exclude 'tests/' \
        "${BUNDLED_DAEMON_SRC}/" "${DAEMON_SRC_DIR}/" \
        >>"${SETUP_LOG}" 2>>"${SETUP_LOG}"
}

# -----------------------------------------------------------------------------
# write_sentinel — atomically (tmp+rename) write the final
# ~/.jarvis/.setup-version-0.2.0 file. Go's IsSetupComplete (TASK-008) reads
# this; the contents are advisory but documented for debugging.
# -----------------------------------------------------------------------------
write_sentinel() {
    local req_sha timestamp
    req_sha="$(sha256_file "${BUNDLED_DAEMON_SRC}/requirements.txt")"
    timestamp="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"

    local tmp
    tmp="$(mktemp "${JARVIS_HOME}/.setup-version-${SETUP_VERSION}.XXXXXX")"
    cat >"${tmp}" <<EOF
version: ${SETUP_VERSION}
timestamp: ${timestamp}
requirements_sha256: ${req_sha}
python_pbs_tag: ${PBS_RELEASE_TAG}
EOF
    mv "${tmp}" "${FINAL_SENTINEL}"
    log "wrote final sentinel ${FINAL_SENTINEL}"
}

# -----------------------------------------------------------------------------
# Main
# -----------------------------------------------------------------------------
main() {
    preflight

    # Fast-path: if the final sentinel is already present and BOTH per-phase
    # sentinels exist, this is a re-run with nothing to do. Emit PHASE/DONE
    # markers for both phases so the React side renders a fully-green column
    # even on this no-op path, then exit.
    if check_sentinel "${FINAL_SENTINEL}" \
            && check_sentinel "${PYTHON_SENTINEL}" \
            && check_sentinel "${VENV_SENTINEL}"; then
        log "setup already complete (sentinel ${FINAL_SENTINEL} present); replaying phase markers"
        emit_phase "python_install"
        emit_progress 100
        emit_done "python_install"
        emit_phase "venv_install"
        emit_progress 100
        emit_done "venv_install"
        exit 0
    fi

    phase_python
    phase_venv
    write_sentinel
    log "setup complete (v${SETUP_VERSION})"
}

main "$@"
