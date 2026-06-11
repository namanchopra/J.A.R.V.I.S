# fetch-python.ps1
#
# Windows companion to build/scripts/fetch-python.sh (TASK-005 of
# plans/jarvis-windows-port.md). Downloads python-build-standalone (CPython
# 3.13) for Windows from the astral-sh/python-build-standalone GitHub
# releases, verifies its SHA256 against the release's SHA256SUMS manifest,
# and extracts it to build\python\.
#
# Pinned release tag: 20260510   (published 2026-05-10, CPython 3.13.13)
# Source: https://github.com/astral-sh/python-build-standalone/releases/tag/20260510
#
# Why pinned: surprise version drift breaks the daemon venv build. Bump
# $PbsReleaseTag below + bump $PbsCpythonVersion when intentionally upgrading.
# Re-run on a clean tree to repopulate the SHA cache.
#
# Notes on SHA verification:
#   python-build-standalone publishes ONE consolidated `SHA256SUMS` manifest
#   per release (not per-file `.sha256` sidecars). We download that manifest,
#   grep for the line matching our tarball, and compare against Get-FileHash.
#
# This script is idempotent: re-running with a valid build\python\python.exe
# already in place AND matching the pinned tag is a no-op.
#
# Architecture:
#   x64   -> cpython-...-x86_64-pc-windows-msvc-install_only_stripped.tar.gz
#   arm64 -> cpython-...-aarch64-pc-windows-msvc-install_only_stripped.tar.gz
#
# Auto-detection uses $env:PROCESSOR_ARCHITECTURE (set by Windows). Override
# explicitly with -Arch x64 or -Arch arm64. Cross-fetching to populate a
# release zip for the other arch is supported.
#
# Usage (from repo root, in PowerShell):
#   powershell -ExecutionPolicy Bypass -File build\scripts\fetch-python.ps1
#   powershell -ExecutionPolicy Bypass -File build\scripts\fetch-python.ps1 -Arch arm64
#
# Or, if executing pwsh on macOS/Linux for cross-arch validation in CI:
#   pwsh build/scripts/fetch-python.ps1 -Arch x64
#
# Requires PowerShell 5.1+ or PowerShell Core (pwsh) 7+.

[CmdletBinding()]
param(
    # Target architecture for the Python bundle. Auto-detected from
    # PROCESSOR_ARCHITECTURE on Windows hosts; explicit override required on
    # non-Windows hosts (CI cross-fetch).
    [ValidateSet('auto', 'x64', 'amd64', 'arm64')]
    [string]$Arch = 'auto'
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

# -----------------------------------------------------------------------------
# Configuration (bump these in lockstep when upgrading)
# -----------------------------------------------------------------------------
$PbsReleaseTag     = '20260510'
$PbsCpythonVersion = '3.13.13'
$PbsBaseUrl        = "https://github.com/astral-sh/python-build-standalone/releases/download/$PbsReleaseTag"

# -----------------------------------------------------------------------------
# Logging helpers (progress to stderr via Write-Host -ForegroundColor; final
# success line goes to stdout for callers that capture it)
# -----------------------------------------------------------------------------
function Write-Log {
    param([string]$Message)
    [Console]::Error.WriteLine("[fetch-python] $Message")
}

function Die {
    param([string]$Message)
    [Console]::Error.WriteLine("[fetch-python] ERROR: $Message")
    exit 1
}

# -----------------------------------------------------------------------------
# Target selection
# -----------------------------------------------------------------------------
if ($Arch -eq 'auto') {
    # PROCESSOR_ARCHITECTURE: AMD64 | ARM64 | x86 (32-bit, unsupported)
    $procArch = $env:PROCESSOR_ARCHITECTURE
    if (-not $procArch) {
        Die "cannot auto-detect arch: PROCESSOR_ARCHITECTURE is not set. Pass -Arch x64 or -Arch arm64 explicitly."
    }
    switch ($procArch.ToUpperInvariant()) {
        'AMD64' { $Arch = 'x64' }
        'ARM64' { $Arch = 'arm64' }
        default { Die "unsupported PROCESSOR_ARCHITECTURE: $procArch (want AMD64 or ARM64)" }
    }
    Write-Log "auto-detected arch: $procArch -> $Arch"
}

switch ($Arch) {
    'x64'   { $PbsArch = 'x86_64' }
    'amd64' { $PbsArch = 'x86_64' }
    'arm64' { $PbsArch = 'aarch64' }
    default { Die "unsupported -Arch: $Arch (want x64|amd64|arm64)" }
}

# Windows assets ship as install_only_stripped.tar.gz — smaller, no debug
# symbols. We don't sign with a code-signing cert yet (TASK-018 ships
# unsigned zips), so the stripped variant is a clear DMG-size win.
$PbsAsset = "cpython-$PbsCpythonVersion+$PbsReleaseTag-$PbsArch-pc-windows-msvc-install_only_stripped.tar.gz"

Write-Log "target: windows/$Arch -> $PbsAsset"

# -----------------------------------------------------------------------------
# Paths
# -----------------------------------------------------------------------------
$ScriptDir   = Split-Path -Parent $MyInvocation.MyCommand.Path
$RepoRoot    = (Resolve-Path (Join-Path $ScriptDir '..\..')).Path
$PythonDir   = Join-Path $RepoRoot 'build\python'
$CacheDir    = Join-Path $PythonDir '.fetch-cache'
$TarballPath = Join-Path $CacheDir $PbsAsset
$ShaSumsPath = Join-Path $CacheDir 'SHA256SUMS'
$StampPath   = Join-Path $PythonDir '.installed-tag'

# python-build-standalone Windows tarballs put python.exe at the top of the
# extracted python\ directory (no Scripts\ or bin\ for the base interpreter).
$SmokeTestBinary = 'python.exe'

# -----------------------------------------------------------------------------
# Required tools
# -----------------------------------------------------------------------------
# tar.exe ships with Windows 10 1803+ (bsdtar). Get-FileHash is built into
# PowerShell. Invoke-WebRequest is built in but we use System.Net.Http for
# better progress + redirect handling.
foreach ($tool in @('tar')) {
    $cmd = Get-Command $tool -ErrorAction SilentlyContinue
    if (-not $cmd) {
        Die "required tool not found in PATH: $tool (tar.exe ships with Win10 1803+)"
    }
}

# -----------------------------------------------------------------------------
# Idempotency check
# -----------------------------------------------------------------------------
# Stamp format matches fetch-python.sh: "<release-tag> windows <arch>". A
# target switch (e.g. x64 -> arm64) forces a re-fetch.
$WantStamp = "$PbsReleaseTag windows $Arch"

if ((Test-Path (Join-Path $PythonDir $SmokeTestBinary)) -and (Test-Path $StampPath)) {
    $InstalledStamp = (Get-Content $StampPath -Raw -ErrorAction SilentlyContinue).Trim()
    if ($InstalledStamp -eq $WantStamp) {
        Write-Log "python-build-standalone $PbsReleaseTag (windows/$Arch) already extracted at $PythonDir"
        Write-Log "already up to date — nothing to do"
        Write-Output "python-build-standalone $PbsReleaseTag (CPython $PbsCpythonVersion, windows/$Arch) already installed at $PythonDir"
        exit 0
    }
    Write-Log "stamp file says installed '$InstalledStamp', but we want '$WantStamp'"
    Write-Log "removing stale extraction and re-fetching"
    Remove-Item -Recurse -Force $PythonDir
}

New-Item -ItemType Directory -Force -Path $CacheDir | Out-Null

# -----------------------------------------------------------------------------
# Download tarball (idempotent in cache)
# -----------------------------------------------------------------------------
if (Test-Path $TarballPath) {
    Write-Log "tarball already cached at $TarballPath"
} else {
    $url = "$PbsBaseUrl/$PbsAsset"
    Write-Log "downloading $PbsAsset (~30MB) from $PbsBaseUrl"
    $partial = "$TarballPath.partial"
    try {
        # Use BITS-free Invoke-WebRequest with TLS 1.2 enforced; older
        # PowerShell defaults to TLS 1.0 which GitHub rejects.
        [Net.ServicePointManager]::SecurityProtocol = `
            [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
        $oldProgress = $ProgressPreference
        $ProgressPreference = 'SilentlyContinue'  # massive speedup for large downloads
        try {
            Invoke-WebRequest -Uri $url -OutFile $partial -UseBasicParsing
        } finally {
            $ProgressPreference = $oldProgress
        }
    } catch {
        Die "failed to download ${PbsAsset}: $_"
    }
    Move-Item -Force $partial $TarballPath
    $sizeMb = [math]::Round((Get-Item $TarballPath).Length / 1MB, 1)
    Write-Log "downloaded $sizeMb MB"
}

# -----------------------------------------------------------------------------
# Download SHA256SUMS manifest (always re-fetch — small, authoritative)
# -----------------------------------------------------------------------------
Write-Log 'fetching SHA256SUMS manifest'
try {
    [Net.ServicePointManager]::SecurityProtocol = `
        [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
    $oldProgress = $ProgressPreference
    $ProgressPreference = 'SilentlyContinue'
    try {
        Invoke-WebRequest -Uri "$PbsBaseUrl/SHA256SUMS" -OutFile $ShaSumsPath -UseBasicParsing
    } finally {
        $ProgressPreference = $oldProgress
    }
} catch {
    Die "failed to download SHA256SUMS from ${PbsBaseUrl}: $_"
}

# -----------------------------------------------------------------------------
# Verify SHA256
# -----------------------------------------------------------------------------
Write-Log "verifying SHA256 of $PbsAsset"
$ExpectedLine = Get-Content $ShaSumsPath | Where-Object { $_ -match "  $([Regex]::Escape($PbsAsset))$" } | Select-Object -First 1
if (-not $ExpectedLine) {
    Die "no SHA256 entry for $PbsAsset in SHA256SUMS — was the asset renamed or removed?"
}
$ExpectedSha = ($ExpectedLine -split '\s+')[0].ToLowerInvariant()
$ActualSha   = (Get-FileHash -Algorithm SHA256 -Path $TarballPath).Hash.ToLowerInvariant()
if ($ExpectedSha -ne $ActualSha) {
    Write-Log "SHA256 MISMATCH for $TarballPath"
    Write-Log "  expected: $ExpectedSha"
    Write-Log "  actual:   $ActualSha"
    Write-Log "the cached tarball may be corrupt or tampered with."
    Write-Log "delete $TarballPath and re-run to fetch a fresh copy."
    exit 1
}
Write-Log "SHA256 verified: $ActualSha"

# -----------------------------------------------------------------------------
# Extract
# -----------------------------------------------------------------------------
# python-build-standalone tarballs unpack to a top-level `python\` directory.
# We want the contents of that directory at build\python\ — i.e., we want
# build\python\python.exe (not build\python\python\python.exe).
#
# Strategy: extract into a temp dir, then move the inner `python\` to
# build\python\. We move the cache dir aside, replace build\python\, then
# move it back so the cached tarball survives re-runs.
Write-Log "extracting $PbsAsset"
$ExtractTmp = Join-Path $RepoRoot ("build\python.extract." + [Guid]::NewGuid().ToString('N').Substring(0,8))
New-Item -ItemType Directory -Force -Path $ExtractTmp | Out-Null

try {
    # tar.exe on Windows handles .tar.gz natively (libarchive-backed).
    & tar -xzf $TarballPath -C $ExtractTmp
    if ($LASTEXITCODE -ne 0) {
        Die "failed to extract $TarballPath (tar exit code $LASTEXITCODE)"
    }

    $ExtractedInner = Join-Path $ExtractTmp 'python'
    if (-not (Test-Path $ExtractedInner -PathType Container)) {
        Die "extracted archive missing expected 'python\' top-level directory"
    }
    $ExtractedExe = Join-Path $ExtractedInner $SmokeTestBinary
    if (-not (Test-Path $ExtractedExe -PathType Leaf)) {
        Die "extracted archive missing expected 'python\$SmokeTestBinary'"
    }

    # Preserve the cache dir across the replacement.
    $CacheBackup = $null
    if (Test-Path $CacheDir) {
        $CacheBackup = Join-Path $RepoRoot ("build\python.cache-backup." + [Guid]::NewGuid().ToString('N').Substring(0,8))
        New-Item -ItemType Directory -Force -Path $CacheBackup | Out-Null
        Move-Item -Force $CacheDir (Join-Path $CacheBackup 'cache')
    }

    if (Test-Path $PythonDir) {
        Remove-Item -Recurse -Force $PythonDir
    }
    Move-Item -Force $ExtractedInner $PythonDir

    if ($CacheBackup) {
        # python\.fetch-cache
        Move-Item -Force (Join-Path $CacheBackup 'cache') $CacheDir
        Remove-Item -Recurse -Force $CacheBackup
    }
} finally {
    if (Test-Path $ExtractTmp) {
        Remove-Item -Recurse -Force $ExtractTmp -ErrorAction SilentlyContinue
    }
}

# Write the install stamp.
Set-Content -Path $StampPath -Value $WantStamp -NoNewline

# -----------------------------------------------------------------------------
# Quick smoke test — fail loud if the extracted interpreter doesn't run.
# -----------------------------------------------------------------------------
# Only attempt to invoke the binary when the target arch matches the host;
# cross-fetches (e.g. x64 asset on an arm64 runner or non-Windows host) just
# verify the binary file exists. On native Windows with matching arch this
# satisfies the TASK-005 acceptance criterion: "python.exe --version prints
# Python 3.13.13".
$IsWindowsHost = $false
try {
    # $IsWindows is true on PowerShell 6+ on Windows; on PS 5.1 it's
    # undefined, but the runtime is implicitly Windows.
    if (Get-Variable -Name IsWindows -ErrorAction SilentlyContinue) {
        $IsWindowsHost = [bool]$IsWindows
    } else {
        $IsWindowsHost = $true
    }
} catch {
    $IsWindowsHost = $true
}

$HostArch = if ($env:PROCESSOR_ARCHITECTURE) { $env:PROCESSOR_ARCHITECTURE.ToUpperInvariant() } else { '' }
$ArchMatches = `
    (($Arch -in @('x64','amd64')) -and ($HostArch -eq 'AMD64')) -or `
    (($Arch -eq 'arm64')         -and ($HostArch -eq 'ARM64'))

if ($IsWindowsHost -and $ArchMatches) {
    $PythonExe = Join-Path $PythonDir $SmokeTestBinary
    try {
        $VersionOutput = & $PythonExe --version 2>&1
        if ($LASTEXITCODE -ne 0) {
            Die "extracted python.exe failed to execute (exit $LASTEXITCODE) — bundle may be incomplete"
        }
        Write-Log "extracted $VersionOutput"
        if ($VersionOutput -notmatch [Regex]::Escape("Python $PbsCpythonVersion")) {
            Die "version mismatch: got '$VersionOutput', expected 'Python $PbsCpythonVersion'"
        }
    } catch {
        Die "extracted python.exe failed to execute: $_"
    }
} else {
    Write-Log "cross-fetch (host=$(if ($IsWindowsHost) { 'windows' } else { 'non-windows' })/$HostArch target=windows/$Arch); skipping --version smoke test"
}

# Final size report.
$Size = (Get-ChildItem -Recurse -Force $PythonDir | Measure-Object -Property Length -Sum).Sum
$SizeMb = [math]::Round($Size / 1MB, 1)
Write-Log "size on disk: $SizeMb MB"

Write-Output "python-build-standalone $PbsReleaseTag (CPython $PbsCpythonVersion, windows/$Arch) installed at $PythonDir"
