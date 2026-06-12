<#
.SYNOPSIS
    Fetches a pinned pre-built PortAudio DLL for Windows x64 (or arm64) from
    the spatialaudio/portaudio-binaries GitHub release artefacts.

.DESCRIPTION
    Windows companion to the build-time setup tooling for the v0.4.0 Windows
    port (TASK-007 of plans/jarvis-windows-port.md). Downloads the pre-built
    `portaudio_x64.dll` (or `portaudio_arm64.dll`) and stages it as
    `portaudio.dll` under `build\portaudio\<arch>\`, plus the matching import
    library `portaudio_x64.lib` when published. The post-build.ps1 step
    (TASK-035) copies the staged DLL into `Resources\lib\` next to the
    Wails-built `jarvis.exe`.

    Why bundle a pre-built portaudio.dll at all?
      pyaudio 0.2.14 ships pre-built wheels for the common Python 3.8-3.12
      Windows ABIs but coverage for CPython 3.13 (the daemon's pinned
      interpreter, TASK-005) is incomplete — uv falls back to building from
      source, which means MSVC + portaudio.h + portaudio.lib. Bundling the
      DLL + import lib lets the source build succeed without requiring the
      end user to install Visual Studio Build Tools or run vcpkg. At runtime
      the daemon loads portaudio.dll via the Windows DLL search order, so
      install-daemon.ps1 (TASK-004 / TASK-007) prepends the bundled lib
      directory to PATH and exports PORTAUDIO_PATH for pyaudio's setup.py.

    Pinned release tag: v19.7.0-1   (spatialaudio/portaudio-binaries)
    Source: https://github.com/spatialaudio/portaudio-binaries/releases/tag/v19.7.0-1

    Why pinned: surprise version drift breaks pyaudio's ABI compatibility
    expectations. Bump $PaReleaseTag below with intent and re-record the
    SHA256s in $KnownSha256.

    Notes on SHA verification:
      The spatialaudio releases do NOT publish per-asset .sha256 sidecars,
      so we ship a small in-script SHA256 manifest ($KnownSha256). When you
      bump $PaReleaseTag, fetch the new asset by hand, run
      `Get-FileHash -Algorithm SHA256` against it, and paste the result here.

    This script is idempotent: re-running with a valid
    `build\portaudio\<arch>\portaudio.dll` already in place AND matching the
    pinned tag + arch is a no-op.

    Windows only (or PowerShell Core on macOS/Linux for CI cross-prep).
    Uses `Get-FileHash -Algorithm SHA256` (built into Windows PowerShell /
    PowerShell Core).

.PARAMETER Arch
    Target architecture for the DLL bundle. One of: x64 (default), amd64
    (alias), arm64. Auto-detected from PROCESSOR_ARCHITECTURE when -Arch is
    omitted on a Windows host. On non-Windows hosts (CI cross-prep) the
    default is x64 and -Arch must be passed explicitly to fetch the arm64
    artefact.

    NOTE: spatialaudio/portaudio-binaries does not (yet) publish an arm64
    Windows binary. Passing -Arch arm64 will exit 0 with a warning so CI
    can call this script unconditionally without short-circuiting the
    pipeline; the v0.4.0 Windows port ships x64 first (per the plan's
    risk register, arm64 is Beta).

.EXAMPLE
    pwsh build/scripts/fetch-portaudio.ps1
    pwsh build/scripts/fetch-portaudio.ps1 -Arch arm64
#>

[CmdletBinding()]
param(
    [ValidateSet('auto', 'x64', 'amd64', 'arm64')]
    [string]$Arch = 'auto'
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

# -----------------------------------------------------------------------------
# Configuration (bump this when upgrading portaudio)
# -----------------------------------------------------------------------------
# spatialaudio/portaudio-binaries has NO GitHub releases (the original
# 'v19.7.0-1' release-asset URL here 404'd — the repo's only tag carries no
# assets). The DLLs are committed directly in the repo tree, so we fetch the
# raw file pinned at a specific commit for reproducibility. Bump the commit
# (and re-record the SHA below) to upgrade portaudio.
$PaCommit     = '855cbb946a89bf645c608a2312d0c56f9d5944d1'
$PaReleaseTag = "master@$($PaCommit.Substring(0, 8))"   # human-readable stamp
$PaBaseUrl    = "https://raw.githubusercontent.com/spatialaudio/portaudio-binaries/$PaCommit"

# In-script SHA256 manifest. Keys are the upstream asset filenames; values are
# lower-case hex digests as printed by `Get-FileHash -Algorithm SHA256`.
# When bumping $PaCommit, re-record these by hand. The fetch path below
# refuses to extract anything whose hash isn't in this table.
#
# Digest recorded + independently verified 2026-06-12 against the
# commit-pinned file (307712 bytes, PE32+ x86-64 DLL):
#   shasum -a 256 libportaudio64bit.dll
$KnownSha256 = @{
    'libportaudio64bit.dll' = 'ec080194f01e4095c7fb43dbd7ed05af922c5b34295056a9ff56782741d65481'
}

# -----------------------------------------------------------------------------
# Logging helpers (all progress to stderr; only final success to stdout)
# -----------------------------------------------------------------------------
function Write-Log {
    param([string]$Message)
    [Console]::Error.WriteLine("[fetch-portaudio] $Message")
}

function Stop-WithError {
    param([string]$Message)
    [Console]::Error.WriteLine("[fetch-portaudio] ERROR: $Message")
    exit 1
}

# -----------------------------------------------------------------------------
# Arch resolution
# -----------------------------------------------------------------------------
if ($Arch -eq 'auto') {
    $procArch = $env:PROCESSOR_ARCHITECTURE
    if ($env:PROCESSOR_ARCHITEW6432) { $procArch = $env:PROCESSOR_ARCHITEW6432 }
    if (-not $procArch) {
        # Non-Windows host with no override — default to x64 (the v0.4.0
        # phase-1 ship target) and warn loudly.
        Write-Log 'warning: PROCESSOR_ARCHITECTURE unset; defaulting to x64 (pass -Arch arm64 explicitly for arm64 cross-prep)'
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

# -----------------------------------------------------------------------------
# arm64 short-circuit
# -----------------------------------------------------------------------------
# spatialaudio/portaudio-binaries publishes x64 only as of $PaReleaseTag. The
# arm64 path is reserved for a future upstream release (or a Jarvis-internal
# CI build). Exit 0 with a clear warning so the higher-level build pipeline
# (post-build.ps1 + installer) can continue without short-circuiting; the
# install-daemon.ps1 preflight surfaces a clear PHASE_ERROR at install time
# if the arm64 DLL never lands.
if ($Arch -eq 'arm64') {
    Write-Log 'warning: spatialaudio/portaudio-binaries does not yet publish an arm64 Windows asset.'
    Write-Log 'no DLL will be staged for arm64; v0.4.0 ships x64 first (arm64 marked Beta in release notes).'
    Write-Log 'when an upstream arm64 build is available, bump $PaReleaseTag and re-record SHA256s in $KnownSha256.'
    exit 0
}

# -----------------------------------------------------------------------------
# Asset selection
# -----------------------------------------------------------------------------
# spatialaudio publishes a single `libportaudio64bit.dll` per release for the
# x64 host. We stage it as `portaudio.dll` (the name pyaudio's source build +
# the Windows DLL loader expect) under `build\portaudio\x64\`.
$UpstreamAsset = 'libportaudio64bit.dll'
$ExpectedSha   = $KnownSha256[$UpstreamAsset]
if (-not $ExpectedSha) {
    Stop-WithError "no SHA256 entry for $UpstreamAsset in `$KnownSha256 — update fetch-portaudio.ps1 when bumping `$PaReleaseTag"
}

# -----------------------------------------------------------------------------
# Paths
# -----------------------------------------------------------------------------
$ScriptDir   = Split-Path -Parent $MyInvocation.MyCommand.Path
$RepoRoot    = (Resolve-Path (Join-Path $ScriptDir '..\..')).Path
$PaDir       = Join-Path $RepoRoot ("build\portaudio\$Arch")
$CacheDir    = Join-Path $PaDir '.fetch-cache'
$AssetPath   = Join-Path $CacheDir $UpstreamAsset
$StampPath   = Join-Path $PaDir   '.installed-tag'
$StagedDll   = Join-Path $PaDir   'portaudio.dll'

# -----------------------------------------------------------------------------
# Sanity checks
# -----------------------------------------------------------------------------
$onWindows = ($PSVersionTable.PSEdition -eq 'Desktop') -or `
             ((Get-Variable -Name 'IsWindows' -ErrorAction SilentlyContinue) -and $IsWindows)
if (-not $onWindows) {
    Write-Log 'warning: running on non-Windows host; staged portaudio.dll will be unloadable here (cross-prep only).'
}

# -----------------------------------------------------------------------------
# Idempotency check
# -----------------------------------------------------------------------------
$WantStamp = "$PaReleaseTag $Arch"
if ((Test-Path $StagedDll) -and (Test-Path $StampPath)) {
    $InstalledStamp = (Get-Content -Raw -Path $StampPath -ErrorAction SilentlyContinue) | ForEach-Object { $_.Trim() }
    if ($InstalledStamp -eq $WantStamp) {
        Write-Log "portaudio $PaReleaseTag ($Arch) already staged at $StagedDll"
        Write-Log 'already up to date - nothing to do'
        Write-Output "portaudio $PaReleaseTag ($Arch) already installed at $StagedDll"
        exit 0
    }
    Write-Log "stamp file says installed '$InstalledStamp', but we want '$WantStamp'"
    Write-Log 'removing stale DLL and re-fetching'
    Remove-Item -Force $StagedDll
}

New-Item -ItemType Directory -Force -Path $CacheDir | Out-Null

# -----------------------------------------------------------------------------
# Download asset (idempotent in cache)
# -----------------------------------------------------------------------------
if (Test-Path $AssetPath) {
    Write-Log "asset already cached at $AssetPath"
} else {
    $url = "$PaBaseUrl/$UpstreamAsset"
    Write-Log "downloading $UpstreamAsset (~200KB) from $PaBaseUrl"
    $partial = "$AssetPath.partial"
    try {
        # Force TLS 1.2+ for Invoke-WebRequest on Windows PowerShell 5.x hosts.
        [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
    } catch {
        # Newer PowerShell editions ignore this; safe to swallow.
    }
    try {
        $oldProgress = $ProgressPreference
        $ProgressPreference = 'SilentlyContinue'  # speed up for binary downloads
        try {
            Invoke-WebRequest -Uri $url -OutFile $partial -UseBasicParsing
        } finally {
            $ProgressPreference = $oldProgress
        }
    } catch {
        Stop-WithError "failed to download ${UpstreamAsset}: $($_.Exception.Message)"
    }
    Move-Item -Force $partial $AssetPath
    $sizeKb = [math]::Round((Get-Item $AssetPath).Length / 1KB, 1)
    Write-Log "downloaded $sizeKb KB"
}

# -----------------------------------------------------------------------------
# Verify SHA256
# -----------------------------------------------------------------------------
Write-Log "verifying SHA256 of $UpstreamAsset"
$ActualSha = (Get-FileHash -Path $AssetPath -Algorithm SHA256).Hash.ToLower()
$ExpectedShaLower = $ExpectedSha.ToLower()
if ($ExpectedShaLower -ne $ActualSha) {
    Write-Log "SHA256 MISMATCH for $AssetPath"
    Write-Log "  expected: $ExpectedShaLower"
    Write-Log "  actual:   $ActualSha"
    Write-Log 'the cached asset may be corrupt, tampered with, or the in-script `$KnownSha256` may be stale.'
    Write-Log "delete $AssetPath and re-run; if the mismatch persists, re-record `$KnownSha256[`'$UpstreamAsset`'] = '$ActualSha' after manual verification."
    exit 1
}
Write-Log "SHA256 verified: $ActualSha"

# -----------------------------------------------------------------------------
# Stage as portaudio.dll
# -----------------------------------------------------------------------------
# The upstream asset is `libportaudio64bit.dll`, but pyaudio's source build
# (and the Windows DLL loader for `import _portaudio`) look for `portaudio.dll`
# on PATH. Copy to that canonical name under build\portaudio\<arch>\.
New-Item -ItemType Directory -Force -Path $PaDir | Out-Null
Copy-Item -Force $AssetPath $StagedDll

# Write the install stamp (includes arch so x64/arm64 swaps invalidate cache).
"$PaReleaseTag $Arch" | Out-File -FilePath $StampPath -Encoding ascii -NoNewline

$sizeKb = [math]::Round((Get-Item $StagedDll).Length / 1KB, 1)
Write-Log "staged portaudio.dll ($sizeKb KB) at $StagedDll"

Write-Output "portaudio $PaReleaseTag ($Arch) installed at $StagedDll"
