<#
.SYNOPSIS
    Wails post-build hook (windows) — mirrors build/scripts/post-build.sh.

.DESCRIPTION
    After `wails build -platform windows/{amd64,arm64}` produces
    build\bin\jarvis.exe, stage the install-time payload next to the
    executable so Inno Setup (installer\jarvis.iss, TASK-054) can pick it
    up from `..\build\bin\Resources\*`:

      build\bin\jarvis.exe                                  (already built)
      build\bin\Resources\setup\install-daemon.ps1          (TASK-004)
      build\bin\Resources\setup\uv.exe                      (TASK-006)
      build\bin\Resources\lib\portaudio.dll                 (TASK-007)
      build\bin\Resources\jarvis-daemon\                    (TASK-011)

    The layout intentionally matches the macOS bundle:
      Jarvis.app/Contents/Resources/setup/{install-daemon.sh,uv}
      Jarvis.app/Contents/Resources/jarvis-daemon/
      Jarvis.app/Contents/Frameworks/libportaudio.2.dylib   (Windows: lib\portaudio.dll)

    Mirrored decisions vs. post-build.sh:
      * We do NOT bundle the full CPython runtime (Resources\python\) here —
        install-daemon.ps1 phase 1 materialises it on first launch via the
        bundled uv binary, exactly like the v0.2.0+ macOS bundle. This keeps
        the installer .exe under the TASK-054 40MB ceiling.
      * We do NOT bundle the daemon venv (Resources\python-venv\) — phase 2
        of install-daemon.ps1 builds it from requirements.txt at first launch.
      * We do NOT bundle Shortcuts.app exports (Resources\shortcuts\) — that
        feature is macOS-only (TASK-016) and intentionally skipped on Windows.
      * We do NOT bundle ML models here — Pipecat's prefetch_models() pulls
        VibeVoice + Whisper to %USERPROFILE%\.cache\huggingface on first run
        (TASK-014). Bundling them would blow the installer size budget.
      * Codesigning happens AFTER this script runs in the CI pipeline
        (TASK-056), not inline like the macOS ad-hoc codesign step. signtool
        signs jarvis.exe + every staged .exe/.dll in Resources\.

    Idempotency: re-running on an already-staged tree is a no-op modulo
    timestamp updates. We do NOT `Remove-Item -Recurse` the Resources
    directory first — instead we mirror per-subtree with `robocopy /MIR`
    (with PURGE) so partial state from an interrupted run is healed without
    nuking other staged trees. Robocopy is the Windows analogue of
    `rsync -a --delete` used by post-build.sh.

    Failure modes:
      * jarvis.exe missing -> fail fast (exit 1) — Wails build did not run.
      * scripts\setup\install-daemon.ps1 missing -> fail fast.
      * scripts\jarvis-daemon\ missing -> fail fast.
      * build\uv\uv.exe missing -> fail fast (run fetch-uv.ps1 first).
      * build\portaudio\<arch>\portaudio.dll missing -> fail fast (run
        fetch-portaudio.ps1 first). arm64 case where upstream lacks a DLL
        is treated as a WARNING (see Arch resolution + arm64 note below);
        the script still exits 0 so CI can build an arm64 installer minus
        portaudio while we wait on an upstream arm64 release.

    Inputs (default values mirror Wails' build output layout):
      -SourceDir   Directory containing jarvis.exe + the staged Resources
                   tree under it. Default: build\bin (matches wails.json
                   outputfilename + the default Wails output path).
      -RepoRoot    Repo root for locating scripts\, build\uv\, build\portaudio\.
                   Default: two levels up from $PSScriptRoot, i.e. the same
                   directory that contains wails.json. Override only for
                   out-of-tree builds (rare).
      -Arch        x64 (default) or arm64. Selects the per-arch portaudio
                   DLL under build\portaudio\<arch>\. Auto-detected from
                   PROCESSOR_ARCHITECTURE on a Windows host; explicit on
                   non-Windows (CI pwsh cross-prep).

    Wire-in: invoked by wails.json -> postBuildHooks.windows once TASK-018
    (Windows CI build) lands the matching entry, e.g.
        "windows/amd64": "pwsh -NoProfile -File build/scripts/post-build.ps1 -Arch x64",
        "windows/arm64": "pwsh -NoProfile -File build/scripts/post-build.ps1 -Arch arm64"
    For local Windows dev runs the script can also be invoked manually:
        pwsh -NoProfile -File build\scripts\post-build.ps1
#>

[CmdletBinding()]
param(
    [string]$SourceDir = '',
    [string]$RepoRoot  = '',
    [ValidateSet('auto', 'x64', 'amd64', 'arm64')]
    [string]$Arch = 'auto'
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

# -----------------------------------------------------------------------------
# Logging helpers (all progress to stderr; final summary line to stdout so CI
# can capture it). Mirrors the [fetch-python]/[fetch-uv] prefix style.
# -----------------------------------------------------------------------------
function Write-Log {
    param([string]$Message)
    [Console]::Error.WriteLine("[post-build] $Message")
}

function Write-Warn {
    param([string]$Message)
    [Console]::Error.WriteLine("[post-build] WARN: $Message")
}

function Stop-WithError {
    param([string]$Message)
    [Console]::Error.WriteLine("[post-build] ERROR: $Message")
    exit 1
}

# -----------------------------------------------------------------------------
# Paths
# -----------------------------------------------------------------------------
$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
if (-not $RepoRoot) {
    $RepoRoot = (Resolve-Path (Join-Path $ScriptDir '..\..')).Path
}
if (-not $SourceDir) {
    $SourceDir = Join-Path $RepoRoot 'build\bin'
}

$JarvisExe   = Join-Path $SourceDir 'jarvis.exe'
$Resources   = Join-Path $SourceDir 'Resources'
$SetupDest   = Join-Path $Resources 'setup'
$LibDest     = Join-Path $Resources 'lib'
$DaemonDest  = Join-Path $Resources 'jarvis-daemon'

# -----------------------------------------------------------------------------
# Arch resolution (only used to locate the per-arch portaudio.dll)
# -----------------------------------------------------------------------------
if ($Arch -eq 'auto') {
    $procArch = $env:PROCESSOR_ARCHITECTURE
    if ($env:PROCESSOR_ARCHITEW6432) { $procArch = $env:PROCESSOR_ARCHITEW6432 }
    if (-not $procArch) {
        # Non-Windows host with no override — default to x64 (the v0.4.0
        # phase-1 ship target). CI cross-prep should pass -Arch explicitly.
        Write-Log 'PROCESSOR_ARCHITECTURE unset; defaulting to -Arch x64'
        $Arch = 'x64'
    } else {
        switch ($procArch.ToUpperInvariant()) {
            'AMD64' { $Arch = 'x64' }
            'ARM64' { $Arch = 'arm64' }
            default { Stop-WithError "unsupported PROCESSOR_ARCHITECTURE: $procArch (want AMD64 or ARM64)" }
        }
        Write-Log "auto-detected arch: $procArch -> $Arch"
    }
}
if ($Arch -eq 'amd64') { $Arch = 'x64' }

Write-Log "RepoRoot:  $RepoRoot"
Write-Log "SourceDir: $SourceDir"
Write-Log "Arch:      $Arch"

# -----------------------------------------------------------------------------
# Sanity check: jarvis.exe must already exist before we stage anything next
# to it. Mirrors post-build.sh's "Jarvis.app not found" guard.
# -----------------------------------------------------------------------------
if (-not (Test-Path -LiteralPath $JarvisExe -PathType Leaf)) {
    Stop-WithError "jarvis.exe not found at $JarvisExe (did 'wails build' run first?)"
}

# Ensure the destination tree exists. New-Item -Force is idempotent.
New-Item -ItemType Directory -Force -Path $Resources  | Out-Null
New-Item -ItemType Directory -Force -Path $SetupDest  | Out-Null
New-Item -ItemType Directory -Force -Path $LibDest    | Out-Null

# -----------------------------------------------------------------------------
# Robocopy wrapper. Robocopy returns 0..7 for success states (>=8 is error),
# so we cannot trust $LASTEXITCODE the way `cp` callers usually do.
#
# Why robocopy: it's the Windows analogue of `rsync -a --delete` — handles
# long paths, preserves attributes, and (with /MIR) mirrors the source into
# the destination atomically so partial state from an interrupted run heals
# instead of accumulating.
# -----------------------------------------------------------------------------
function Invoke-Robocopy {
    param(
        [Parameter(Mandatory)] [string]$Source,
        [Parameter(Mandatory)] [string]$Destination,
        [string[]]$ExcludeFiles = @(),
        [string[]]$ExcludeDirs  = @(),
        [switch]$Mirror
    )
    $rcArgs = @($Source, $Destination, '/E', '/NFL', '/NDL', '/NP', '/NJH', '/NJS', '/R:2', '/W:2')
    if ($Mirror) { $rcArgs += '/MIR' }
    if ($ExcludeFiles.Count -gt 0) {
        $rcArgs += '/XF'
        $rcArgs += $ExcludeFiles
    }
    if ($ExcludeDirs.Count -gt 0) {
        $rcArgs += '/XD'
        $rcArgs += $ExcludeDirs
    }
    & robocopy @rcArgs | Out-Null
    $code = $LASTEXITCODE
    # Robocopy: 0=no change, 1-7=success variants, 8+=failure. Reset
    # $LASTEXITCODE so a downstream `exit $LASTEXITCODE` in CI sees 0.
    if ($code -ge 8) {
        Stop-WithError "robocopy failed copying $Source -> ${Destination} (exit $code)"
    }
    $global:LASTEXITCODE = 0
}

# -----------------------------------------------------------------------------
# 1. Copy first-launch setup payload (uv.exe + install-daemon.ps1) -> Resources\setup\
#
# Mirrors post-build.sh sections 2 + 3. install-daemon.ps1 expects to find
# itself at <bundle>\Resources\setup\install-daemon.ps1 (see the
# Resolve-PortaudioDll search order in scripts\setup\install-daemon.ps1)
# and the bundled uv binary at <bundle>\Resources\setup\uv.exe (see
# app_setup.go's setupSpawnArgs assembly).
# -----------------------------------------------------------------------------
$UvSrc = Join-Path $RepoRoot 'build\uv\uv.exe'
if (-not (Test-Path -LiteralPath $UvSrc -PathType Leaf)) {
    Stop-WithError "build\uv\uv.exe not found; run build\scripts\fetch-uv.ps1 first (got $UvSrc)"
}
$UvDest = Join-Path $SetupDest 'uv.exe'
Write-Log "copying build\uv\uv.exe -> Resources\setup\uv.exe"
Copy-Item -Force -LiteralPath $UvSrc -Destination $UvDest

$InstallDaemonSrc = Join-Path $RepoRoot 'scripts\setup\install-daemon.ps1'
if (-not (Test-Path -LiteralPath $InstallDaemonSrc -PathType Leaf)) {
    Stop-WithError "scripts\setup\install-daemon.ps1 not found at $InstallDaemonSrc"
}
$InstallDaemonDest = Join-Path $SetupDest 'install-daemon.ps1'
Write-Log "copying scripts\setup\install-daemon.ps1 -> Resources\setup\install-daemon.ps1"
Copy-Item -Force -LiteralPath $InstallDaemonSrc -Destination $InstallDaemonDest

# -----------------------------------------------------------------------------
# 2. Copy bundled portaudio.dll -> Resources\lib\portaudio.dll
#
# pyaudio 0.2.14's source build on CPython 3.13 Windows expects portaudio.dll
# discoverable via PATH / PORTAUDIO_PATH at install time (see install-daemon.ps1's
# Resolve-PortaudioDll). The arm64 case is a soft failure for now: upstream
# spatialaudio/portaudio-binaries has no arm64 asset (fetch-portaudio.ps1
# short-circuits with exit 0), so we WARN and continue without staging. The
# install-daemon.ps1 venv phase will surface a clear PHASE_ERROR at first
# launch if it really can't find a DLL.
# -----------------------------------------------------------------------------
$PortaudioSrc = Join-Path $RepoRoot ("build\portaudio\$Arch\portaudio.dll")
if (Test-Path -LiteralPath $PortaudioSrc -PathType Leaf) {
    $PortaudioDest = Join-Path $LibDest 'portaudio.dll'
    Write-Log "copying build\portaudio\$Arch\portaudio.dll -> Resources\lib\portaudio.dll"
    Copy-Item -Force -LiteralPath $PortaudioSrc -Destination $PortaudioDest
} else {
    if ($Arch -eq 'arm64') {
        Write-Warn "build\portaudio\arm64\portaudio.dll not staged (upstream has no arm64 asset); arm64 build will fall back to runtime PHASE_ERROR if pyaudio source build needs it."
    } else {
        Stop-WithError "build\portaudio\$Arch\portaudio.dll not found; run build\scripts\fetch-portaudio.ps1 -Arch $Arch first (got $PortaudioSrc)"
    }
}

# -----------------------------------------------------------------------------
# 3. Copy daemon source -> Resources\jarvis-daemon\
#
# Mirrors post-build.sh's rsync section. install-daemon.ps1 phase 3 copies
# this into <JarvisHome>\jarvis-daemon\ at first launch (we ship the
# read-only template inside the bundle; the user copy is mutable).
#
# Excluded: __pycache__\, *.pyc, *.pyo, *_test.py, tests\ (parity with the
# macOS rsync flags). On Windows __pycache__ dirs created post-install would
# survive an upgrade and confuse the venv interpreter; we want a clean
# template each install.
# -----------------------------------------------------------------------------
$DaemonSrc = Join-Path $RepoRoot 'scripts\jarvis-daemon'
if (-not (Test-Path -LiteralPath $DaemonSrc -PathType Container)) {
    Stop-WithError "scripts\jarvis-daemon\ not found at $DaemonSrc"
}
Write-Log "copying scripts\jarvis-daemon\ -> Resources\jarvis-daemon\ (mirror, excludes __pycache__/tests/*.pyc/*.pyo/*_test.py)"
Invoke-Robocopy -Source $DaemonSrc -Destination $DaemonDest `
    -ExcludeFiles @('*.pyc', '*.pyo', '*_test.py') `
    -ExcludeDirs  @('__pycache__', 'tests') `
    -Mirror

# -----------------------------------------------------------------------------
# Done. Final summary line to stdout for CI logs.
# -----------------------------------------------------------------------------
$staged = @()
if (Test-Path -LiteralPath $UvDest)            { $staged += 'setup\uv.exe' }
if (Test-Path -LiteralPath $InstallDaemonDest) { $staged += 'setup\install-daemon.ps1' }
if (Test-Path -LiteralPath (Join-Path $LibDest 'portaudio.dll')) { $staged += 'lib\portaudio.dll' }
if (Test-Path -LiteralPath $DaemonDest -PathType Container)     { $staged += 'jarvis-daemon\' }

Write-Log "staged: $($staged -join ', ')"
Write-Output "post-build: staged Resources\ next to jarvis.exe at $Resources"
