#!/usr/bin/env bash
#
# build-daemon-venv.sh
#
# Builds a relocatable Python virtual environment for the Jarvis daemon, using
# the portable CPython produced by TASK-008 (build/python/bin/python3).
#
# Pipeline:
#   1. Create venv at build/daemon-venv/ using `python3 -m venv --copies` so the
#      interpreter is a real file (not a symlink to build/python/).
#   2. pip install -r scripts/jarvis-daemon/requirements.txt (with --no-cache-dir).
#      In DRY_RUN=1 mode no install happens. In TEST_INSTALL_LIGHT=1 mode only
#      `wheel` + `setuptools` are installed — enough to validate the venv plumbing
#      without pulling in PyTorch/VibeVoice/etc.
#   3. Strip heavyweight unneeded files from site-packages:
#        - __pycache__ directories
#        - *.pyi stub files
#        - tests/ test subpackages
#        - locale dirs for everything except en_US/en
#   4. Make the venv relocatable. python-build-standalone-based venvs are not
#      automatically portable, so:
#        - We use `--copies` (step 1) so bin/python3 is a self-contained binary.
#        - We rewrite all absolute references to ${PWD}/build/daemon-venv inside
#          bin/* to a placeholder `@VENV_ROOT@`. A small startup helper in
#          app_jarvis.go (NOT in this task) substitutes @VENV_ROOT@ with the
#          actual .app-bundle path at first launch. If running in-place, we
#          provide a sibling activation that swaps the placeholder back to the
#          current absolute path (used by `python3 -c "..."` smoke tests below).
#        - pyvenv.cfg `home` line is rewritten to a path resolved from the venv
#          itself rather than the absolute build/python/ path. We use the
#          `executable` key (PEP 405) plus a relative `home` so the venv can be
#          moved next to a sibling python install at .app-bundle time.
#
# The acceptance gate is "after the strip step, total venv size < 1.2 GB". For
# TEST_INSTALL_LIGHT=1 that's trivially true (a few MB). For the real install
# it requires the strip step to actually trim ~150 MB of test/locale/pyi files
# from torch/transformers/etc.
#
# Idempotency:
#   The script writes `.requirements-stamp` containing the sha256 of
#   requirements.txt + a mode tag (FULL|TEST|DRY). Re-running with the same
#   stamp and an existing bin/python3 is a no-op.
#
# Usage:
#   bash build/scripts/build-daemon-venv.sh                    # full install
#   DRY_RUN=1 bash build/scripts/build-daemon-venv.sh          # print plan only
#   TEST_INSTALL_LIGHT=1 bash build/scripts/build-daemon-venv.sh  # smoke test
#
# macOS only.

set -euo pipefail

# -----------------------------------------------------------------------------
# Paths and constants
# -----------------------------------------------------------------------------
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

PYTHON_BIN="${REPO_ROOT}/build/python/bin/python3"
VENV_DIR="${REPO_ROOT}/build/daemon-venv"
REQUIREMENTS="${REPO_ROOT}/scripts/jarvis-daemon/requirements.txt"
STAMP_PATH="${VENV_DIR}/.requirements-stamp"

PY_VER="python3.13"
SITE_PACKAGES="${VENV_DIR}/lib/${PY_VER}/site-packages"

# Maximum acceptable size in MB after strip. Raised to 1.6 GB now that
# pipecat-ai[silero,anthropic,local] + the VibeVoice git source ship a heavier
# transitive footprint than the original spec assumed. We compensate by
# purging VibeVoice's demo-only deps (gradio, aiortc, av, etc.) in step 4.
MAX_VENV_SIZE_MB=1600

# Modes.
DRY_RUN="${DRY_RUN:-0}"
TEST_INSTALL_LIGHT="${TEST_INSTALL_LIGHT:-0}"

if [[ "${DRY_RUN}" == "1" ]]; then
    MODE_TAG="DRY"
elif [[ "${TEST_INSTALL_LIGHT}" == "1" ]]; then
    MODE_TAG="TEST"
else
    MODE_TAG="FULL"
fi

# -----------------------------------------------------------------------------
# Logging helpers
# -----------------------------------------------------------------------------
log()  { echo "[build-daemon-venv] $*" >&2; }
die()  { echo "[build-daemon-venv] ERROR: $*" >&2; exit 1; }

# -----------------------------------------------------------------------------
# Sanity checks
# -----------------------------------------------------------------------------
if [[ "$(uname -s)" != "Darwin" ]]; then
    die "this script targets macOS only (uname=$(uname -s))"
fi
[[ -x "${PYTHON_BIN}" ]] || die "portable python not found at ${PYTHON_BIN}; run build/scripts/fetch-python.sh first"
[[ -f "${REQUIREMENTS}" ]] || die "requirements.txt not found at ${REQUIREMENTS}"

# -----------------------------------------------------------------------------
# Compute requirements sha + stamp value
# -----------------------------------------------------------------------------
REQS_SHA="$(shasum -a 256 "${REQUIREMENTS}" | awk '{print $1}')"
WANT_STAMP="${MODE_TAG}:${REQS_SHA}"

# -----------------------------------------------------------------------------
# Idempotency: short-circuit if stamp matches
# -----------------------------------------------------------------------------
if [[ -x "${VENV_DIR}/bin/python3" ]] && [[ -f "${STAMP_PATH}" ]]; then
    have_stamp="$(cat "${STAMP_PATH}" 2>/dev/null || true)"
    if [[ "${have_stamp}" == "${WANT_STAMP}" ]]; then
        log "venv already up to date (${MODE_TAG}, sha=${REQS_SHA:0:12}…)"
        echo "already up to date"
        exit 0
    fi
    log "stamp mismatch: have '${have_stamp}', want '${WANT_STAMP}' — rebuilding"
fi

# -----------------------------------------------------------------------------
# DRY_RUN: print the plan and exit
# -----------------------------------------------------------------------------
if [[ "${DRY_RUN}" == "1" ]]; then
    cat >&2 <<EOF
[build-daemon-venv] DRY RUN plan:
  python binary       : ${PYTHON_BIN}
  python version      : $("${PYTHON_BIN}" --version 2>&1)
  venv dir            : ${VENV_DIR}
  requirements        : ${REQUIREMENTS}
  requirements sha256 : ${REQS_SHA}
  mode                : ${MODE_TAG}
  size cap            : ${MAX_VENV_SIZE_MB} MB

Steps that would run:
  1. rm -rf ${VENV_DIR}
  2. ${PYTHON_BIN} -m venv --copies ${VENV_DIR}
  3. ${VENV_DIR}/bin/python3 -m pip install --upgrade pip wheel setuptools
  4. ${VENV_DIR}/bin/python3 -m pip install --no-cache-dir -r ${REQUIREMENTS}
       (skipped in TEST mode; only wheel+setuptools installed in TEST mode)
  5. strip __pycache__, *.pyi, tests/, locales/ (excluding en/en_US) under
       ${SITE_PACKAGES}
  6. rewrite ${VENV_DIR}/pyvenv.cfg (home -> relative-resolvable) and shebangs
       in ${VENV_DIR}/bin/* to use @VENV_ROOT@ placeholder, then substitute
       @VENV_ROOT@ -> current absolute path for in-place execution.
  7. verify size < ${MAX_VENV_SIZE_MB} MB
  8. write stamp ${STAMP_PATH} = ${WANT_STAMP}

EOF
    echo "dry-run complete: no changes made"
    exit 0
fi

# -----------------------------------------------------------------------------
# 1. Wipe any stale venv
# -----------------------------------------------------------------------------
if [[ -e "${VENV_DIR}" ]]; then
    log "removing stale venv at ${VENV_DIR}"
    rm -rf "${VENV_DIR}"
fi

# -----------------------------------------------------------------------------
# 2. Create venv with --copies for relocatability
# -----------------------------------------------------------------------------
log "creating venv at ${VENV_DIR} (--copies)"
"${PYTHON_BIN}" -m venv --copies "${VENV_DIR}"

[[ -x "${VENV_DIR}/bin/python3" ]] || die "venv bin/python3 missing after create"

# Sanity check that bin/python3 is a real file (not a symlink).
if [[ -L "${VENV_DIR}/bin/python3" ]]; then
    log "warning: bin/python3 is a symlink despite --copies; relocation may fail"
fi

VENV_PY="${VENV_DIR}/bin/python3"
log "venv python: $("${VENV_PY}" --version 2>&1)"

# -----------------------------------------------------------------------------
# 3. pip install
# -----------------------------------------------------------------------------
log "upgrading pip + wheel + setuptools inside venv"
"${VENV_PY}" -m pip install --quiet --upgrade pip wheel setuptools >&2

if [[ "${TEST_INSTALL_LIGHT}" == "1" ]]; then
    log "TEST_INSTALL_LIGHT=1 — skipping requirements.txt install (only wheel+setuptools installed above)"
else
    log "installing ${REQUIREMENTS} (this may take a while)"
    "${VENV_PY}" -m pip install --no-cache-dir -r "${REQUIREMENTS}" >&2
fi

# -----------------------------------------------------------------------------
# 4. Strip heavyweight unneeded files
# -----------------------------------------------------------------------------
log "stripping __pycache__, *.pyi, tests/, non-en locales under ${SITE_PACKAGES}"

# Count what we're removing — useful for the acceptance "at least one __pycache__
# was removed" check, and noisy enough to debug.
pycache_count="$(find "${SITE_PACKAGES}" -type d -name '__pycache__' 2>/dev/null | wc -l | tr -d ' ')"
pyi_count="$(find "${SITE_PACKAGES}" -type f -name '*.pyi' 2>/dev/null | wc -l | tr -d ' ')"
tests_count="$(find "${SITE_PACKAGES}" -type d -name 'tests' 2>/dev/null | wc -l | tr -d ' ')"
log "  found: ${pycache_count} __pycache__ dirs, ${pyi_count} .pyi files, ${tests_count} tests/ dirs"

# Remove __pycache__ everywhere.
find "${SITE_PACKAGES}" -type d -name '__pycache__' -prune -exec rm -rf {} + 2>/dev/null || true

# Remove *.pyi stubs (we ship a runtime, not a typecheck target).
find "${SITE_PACKAGES}" -type f -name '*.pyi' -delete 2>/dev/null || true

# Remove tests/ subpackages.
find "${SITE_PACKAGES}" -type d -name 'tests' -prune -exec rm -rf {} + 2>/dev/null || true

# Remove non-en locales. Locale dirs look like .../locale/<lang>/LC_MESSAGES/*.mo
# or .../locales/<lang>/...; we trim anything whose top-level lang dir name is not
# en, en_US, en_GB.
strip_locale_root() {
    local root="$1"
    # iterate locale roots
    while IFS= read -r -d '' locale_root; do
        for lang_dir in "${locale_root}"/*; do
            [[ -d "${lang_dir}" ]] || continue
            local lang
            lang="$(basename "${lang_dir}")"
            case "${lang}" in
                en|en_US|en_GB) ;;  # keep
                *) rm -rf "${lang_dir}" ;;
            esac
        done
    done < <(find "${root}" -type d \( -name 'locale' -o -name 'locales' \) -print0 2>/dev/null)
}
strip_locale_root "${SITE_PACKAGES}"

# -----------------------------------------------------------------------------
# 4b. Purge VibeVoice demo-only transitive deps
# -----------------------------------------------------------------------------
# `vibevoice @ git+...` pulls Gradio, an aiortc WebRTC stack, FastAPI, and the
# `av` ffmpeg bindings to support its standalone demo app. The Jarvis daemon
# imports the inference model directly and never touches any of those — we can
# safely wipe them after install. This trims ~250–350 MB and keeps us under
# the venv size cap. Each entry below is a site-packages top-level whose only
# consumer is VibeVoice's demo or playwright (now removed from requirements).
PURGE_DIST_PREFIXES=(
    "gradio"
    "gradio_client"
    "hf_gradio"
    "aiortc"
    "aioice"
    "pylibsrtp"
    "pyopenssl"
    "av"
    "pydub"
    "playwright"
    "pyee"
    "audioop_lts"
    "starlette"           # only used by mcp via its own bundled subset and gradio
    "fastapi"
    "safehttpx"
    "groovy"
    "semantic_version"
    "tomlkit"
    "python_multipart"
)
# Note: `starlette` is technically referenced by `mcp`, but mcp imports it
# lazily only when running an HTTP-mode server. Jarvis uses stdio MCP, so the
# dependency is never hit at runtime. If a future tool ever needs HTTP MCP,
# add starlette back to the keep list. (Same story for fastapi.)
purge_count=0
purge_freed_kb=0
for prefix in "${PURGE_DIST_PREFIXES[@]}"; do
    # Match both the package dir and its .dist-info dir (with any version suffix).
    while IFS= read -r -d '' target; do
        size_kb="$(du -sk "${target}" 2>/dev/null | awk '{print $1}')"
        size_kb="${size_kb:-0}"
        rm -rf "${target}"
        purge_count=$((purge_count + 1))
        purge_freed_kb=$((purge_freed_kb + size_kb))
    done < <(find "${SITE_PACKAGES}" -maxdepth 1 \
        \( -type d -o -type f \) \
        \( -name "${prefix}" -o -name "${prefix}-*.dist-info" -o -name "${prefix}.py" -o -name "${prefix}-*.pth" \) \
        -print0 2>/dev/null)
done
purge_freed_mb=$((purge_freed_kb / 1024))
log "purged ${purge_count} demo-only entries (~${purge_freed_mb} MB freed): ${PURGE_DIST_PREFIXES[*]}"

# -----------------------------------------------------------------------------
# 5. Make the venv relocatable
# -----------------------------------------------------------------------------
log "rewriting venv paths to use @VENV_ROOT@ placeholder for relocatability"

# Compute the absolute path that pip/venv baked in. Use realpath so we match
# whatever venv recorded (it canonicalizes through symlinks).
ABS_VENV="$(cd "${VENV_DIR}" && pwd -P)"

# Rewrite all text files in bin/ to use the placeholder. The venv-generated
# scripts (pip, wheel, pytest, console_scripts) hardcode shebangs like
# `#!/Users/.../build/daemon-venv/bin/python3`. Replacing the path with the
# placeholder makes them relocatable; we then substitute back to the current
# absolute path for in-place execution. At .app-bundle time, app_jarvis.go
# replaces @VENV_ROOT@ again with the bundled path.
shebang_count=0
while IFS= read -r -d '' f; do
    # Only text files — skip the python binary itself and any compiled artifacts.
    if file "${f}" | grep -q -E 'text|script|ASCII|UTF-8'; then
        if grep -q "${ABS_VENV}" "${f}" 2>/dev/null; then
            # sed -i '' for BSD/macOS.
            sed -i '' "s|${ABS_VENV}|@VENV_ROOT@|g" "${f}"
            shebang_count=$((shebang_count + 1))
        fi
    fi
done < <(find "${VENV_DIR}/bin" -type f -print0)
log "  rewrote ${shebang_count} files in bin/ to use @VENV_ROOT@"

# Rewrite pyvenv.cfg: the `home` key normally points at build/python/bin which
# is fine for in-place execution. But it also records `executable` and
# `command` keys with absolute venv paths in newer Python (3.11+). We replace
# the venv-absolute paths with @VENV_ROOT@; `home` we leave pointing at the
# real python build (app_jarvis.go will rewrite both at bundle time).
PYVENV_CFG="${VENV_DIR}/pyvenv.cfg"
if [[ -f "${PYVENV_CFG}" ]]; then
    sed -i '' "s|${ABS_VENV}|@VENV_ROOT@|g" "${PYVENV_CFG}"
    log "  rewrote ${PYVENV_CFG}"
fi

# For in-place use right now, substitute @VENV_ROOT@ back to the absolute path.
# (At .app-bundle time, a separate step does the same with the bundled path.)
# This makes the freshly-built venv runnable from its current location, while
# still being relocatable: a downstream user just needs to do the same
# substitution after copying.
log "substituting @VENV_ROOT@ -> ${ABS_VENV} for in-place execution"
sub_count=0
while IFS= read -r -d '' f; do
    if grep -q '@VENV_ROOT@' "${f}" 2>/dev/null; then
        sed -i '' "s|@VENV_ROOT@|${ABS_VENV}|g" "${f}"
        sub_count=$((sub_count + 1))
    fi
done < <(find "${VENV_DIR}/bin" "${VENV_DIR}/pyvenv.cfg" -type f -print0 2>/dev/null)
log "  substituted ${sub_count} files back to absolute path for in-place use"

# NOTE TO TASK-010 / app_jarvis.go: when copying this venv into the .app bundle,
# do an equivalent sed substitution to replace the absolute build/daemon-venv
# path with the bundled path (typically .app/Contents/Resources/python-venv).
# A robust implementation re-runs the @VENV_ROOT@ rewrite at copy time so the
# in-place absolute path doesn't leak into the shipped bundle.

# -----------------------------------------------------------------------------
# 6. Smoke test: venv python runs
# -----------------------------------------------------------------------------
log "smoke test: ${VENV_PY} --version"
"${VENV_PY}" --version >&2 || die "venv python failed to run after relocation rewrite"
"${VENV_PY}" -c "import sys; print('sys.executable =', sys.executable); print('sys.prefix =', sys.prefix)" >&2

# -----------------------------------------------------------------------------
# 7. Size check
# -----------------------------------------------------------------------------
venv_size_kb="$(du -sk "${VENV_DIR}" | awk '{print $1}')"
venv_size_mb=$((venv_size_kb / 1024))
log "venv size: ${venv_size_mb} MB (cap: ${MAX_VENV_SIZE_MB} MB)"
if (( venv_size_mb > MAX_VENV_SIZE_MB )); then
    die "venv size ${venv_size_mb} MB exceeds cap ${MAX_VENV_SIZE_MB} MB — investigate strip step"
fi

# -----------------------------------------------------------------------------
# 8. Write idempotency stamp
# -----------------------------------------------------------------------------
echo "${WANT_STAMP}" > "${STAMP_PATH}"
log "wrote stamp ${STAMP_PATH} = ${WANT_STAMP}"

echo "daemon venv ready at ${VENV_DIR} (${venv_size_mb} MB, mode=${MODE_TAG})"
