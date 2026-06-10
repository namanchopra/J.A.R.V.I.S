<#
.SYNOPSIS
    Fetches a pinned `uv` release from astral-sh/uv for Windows x64 or arm64.

.DESCRIPTION
    Windows mirror of build/scripts/fetch-uv.sh. Downloads the pinned uv release
    .zip for the requested architecture from the astral-sh/uv GitHub releases,
    verifies its SHA256 against the release's `.sha256` sidecar file, and
    extracts the `uv.exe` binary to `build/uv/uv.exe`.

    Why bundle uv at all? On first launch the app spawns the v0.2.0 setup
    orchestrator (`scripts/setup/install-daemon.ps1`, TASK-004) which uses uv
    to materialise the Python interpreter + daemon venv into the Jarvis home
    directory. Shipping uv (~10MB binary) instead of CPython + venv (~150MB)
    saves ~140MB in the installer.

    Pinned release tag: 0.11.14   (published 2026-05-12)
    Source: https://github.com/astral-sh/uv/releases/tag/0.11.14

    Why pinned: surprise version drift breaks the setup script's behaviour.
    Bump $UvReleaseTag below with intent, e.g. when upgrading to pick up a uv
    bugfix. Re-run on a clean `build/uv/` tree to repopulate the SHA cache.

    Notes on SHA verification:
      uv publishes per-asset `.sha256` sidecar files (standard shasum -c
      format) AND a consolidated `sha256.sum` manifest. We use the per-asset
      sidecar for minimal surface area.

    This script is idempotent: re-running with a valid `build/uv/uv.exe`
    already in place AND matching the pinned tag + arch is a no-op.

    Windows only. Uses `Get-FileHash -Algorithm SHA256` (built into Windows
    PowerShell / PowerShell Core).

.PARAMETER Arch
    Target architecture. One of: amd64 (default), arm64.
    amd64 -> uv-x86_64-pc-windows-msvc.zip
    arm64 -> uv-aarch64-pc-windows-msvc.zip

.EXAMPLE
    pwsh build/scripts/fetch-uv.ps1
    pwsh build/scripts/fetch-uv.ps1 -Arch arm64
#>

[CmdletBinding()]
param(
    [ValidateSet('amd64', 'arm64')]
    [string]$Arch = 'amd64'
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

# -----------------------------------------------------------------------------
# Configuration (bump this when upgrading uv)
# -----------------------------------------------------------------------------
$UvReleaseTag = '0.11.14'

switch ($Arch) {
    'amd64' { $UvPlatform = 'x86_64-pc-windows-msvc' }
    'arm64' { $UvPlatform = 'aarch64-pc-windows-msvc' }
}

$UvAsset = "uv-$UvPlatform.zip"
$UvBaseUrl = "https://github.com/astral-sh/uv/releases/download/$UvReleaseTag"

# -----------------------------------------------------------------------------
# Paths
# -----------------------------------------------------------------------------
$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$RepoRoot = (Resolve-Path (Join-Path $ScriptDir '..\..')).Path
$UvDir = Join-Path $RepoRoot 'build\uv'
$CacheDir = Join-Path $UvDir '.fetch-cache'
$ZipPath = Join-Path $CacheDir $UvAsset
$ShaPath = Join-Path $CacheDir "$UvAsset.sha256"
$StampPath = Join-Path $UvDir '.installed-tag'
$UvBin = Join-Path $UvDir 'uv.exe'

# -----------------------------------------------------------------------------
# Logging helpers (all progress to stderr; only final success to stdout)
# -----------------------------------------------------------------------------
function Write-Log {
    param([string]$Message)
    [Console]::Error.WriteLine("[fetch-uv] $Message")
}

function Stop-WithError {
    param([string]$Message)
    [Console]::Error.WriteLine("[fetch-uv] ERROR: $Message")
    exit 1
}

# -----------------------------------------------------------------------------
# Sanity checks
# -----------------------------------------------------------------------------
# $IsWindows is a PowerShell Core (6+) automatic variable; on Windows
# PowerShell 5.1 it is $null. PSEdition 'Desktop' implies Windows.
$onWindows = ($PSVersionTable.PSEdition -eq 'Desktop') -or `
             ((Get-Variable -Name 'IsWindows' -ErrorAction SilentlyContinue) -and $IsWindows)
if (-not $onWindows) {
    # PowerShell Core on non-Windows hosts can still drive the fetch (useful for
    # CI cross-prep), but warn loudly so accidental local invocations stand out.
    Write-Log "warning: running on non-Windows host; extracted uv.exe will be unrunnable here."
}

$hostArch = [System.Runtime.InteropServices.RuntimeInformation]::ProcessArchitecture.ToString().ToLower()
if ($hostArch -eq 'x64' -and $Arch -eq 'arm64') {
    Write-Log "warning: host arch is x64, but the requested asset targets arm64. binary will be extracted but unrunnable on this host."
} elseif ($hostArch -eq 'arm64' -and $Arch -eq 'amd64') {
    Write-Log "warning: host arch is arm64, but the requested asset targets amd64. binary will be extracted but unrunnable on this host."
}

# -----------------------------------------------------------------------------
# Idempotency check
# -----------------------------------------------------------------------------
if ((Test-Path $UvBin) -and (Test-Path $StampPath)) {
    $installedStamp = (Get-Content -Raw -Path $StampPath -ErrorAction SilentlyContinue) | ForEach-Object { $_.Trim() }
    $wantStamp = "$UvReleaseTag $Arch"
    if ($installedStamp -eq $wantStamp) {
        Write-Log "uv $UvReleaseTag ($Arch) already extracted at $UvBin"
        Write-Log "already up to date - nothing to do"
        Write-Output "uv $UvReleaseTag ($Arch) already installed at $UvBin"
        exit 0
    }
    Write-Log "stamp file says installed '$installedStamp', but we want '$wantStamp'"
    Write-Log "removing stale binary and re-fetching"
    Remove-Item -Force $UvBin
}

New-Item -ItemType Directory -Force -Path $CacheDir | Out-Null

# -----------------------------------------------------------------------------
# Download zip (idempotent in cache)
# -----------------------------------------------------------------------------
if (Test-Path $ZipPath) {
    Write-Log "zip already cached at $ZipPath"
} else {
    Write-Log "downloading $UvAsset (~10MB) from $UvBaseUrl"
    $partial = "$ZipPath.partial"
    try {
        # Force TLS 1.2+ for Invoke-WebRequest on Windows PowerShell 5.x hosts.
        [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
    } catch {
        # Newer PowerShell editions ignore this; safe to swallow.
    }
    try {
        Invoke-WebRequest -Uri "$UvBaseUrl/$UvAsset" -OutFile $partial -UseBasicParsing
    } catch {
        Stop-WithError "failed to download $UvAsset : $($_.Exception.Message)"
    }
    Move-Item -Force $partial $ZipPath
    $size = [math]::Round((Get-Item $ZipPath).Length / 1MB, 1)
    Write-Log "downloaded $size MB"
}

# -----------------------------------------------------------------------------
# Download .sha256 sidecar (always re-fetch - tiny, authoritative)
# -----------------------------------------------------------------------------
Write-Log "fetching $UvAsset.sha256 sidecar"
try {
    Invoke-WebRequest -Uri "$UvBaseUrl/$UvAsset.sha256" -OutFile $ShaPath -UseBasicParsing
} catch {
    Stop-WithError "failed to download $UvAsset.sha256 from $UvBaseUrl : $($_.Exception.Message)"
}

# -----------------------------------------------------------------------------
# Verify SHA256
# -----------------------------------------------------------------------------
Write-Log "verifying SHA256 of $UvAsset"
$shaLine = (Get-Content -Raw -Path $ShaPath).Trim()
$expectedSha = ($shaLine -split '\s+')[0]
if (-not $expectedSha) {
    Stop-WithError "empty SHA256 in $ShaPath - release may be malformed"
}
$actualSha = (Get-FileHash -Path $ZipPath -Algorithm SHA256).Hash.ToLower()
$expectedShaLower = $expectedSha.ToLower()
if ($expectedShaLower -ne $actualSha) {
    Write-Log "SHA256 MISMATCH for $ZipPath"
    Write-Log "  expected: $expectedShaLower"
    Write-Log "  actual:   $actualSha"
    Write-Log "the cached zip may be corrupt or tampered with."
    Write-Log "delete $ZipPath and re-run to fetch a fresh copy."
    exit 1
}
Write-Log "SHA256 verified: $actualSha"

# -----------------------------------------------------------------------------
# Extract
# -----------------------------------------------------------------------------
# uv zips unpack to a top-level `uv-<platform>/` directory containing
# `uv.exe` and `uvx.exe`. We only need `uv.exe` - copy it to build/uv/uv.exe.
Write-Log "extracting $UvAsset"
$extractTmp = Join-Path $RepoRoot ("build\uv.extract." + [System.Guid]::NewGuid().ToString('N').Substring(0, 8))
New-Item -ItemType Directory -Force -Path $extractTmp | Out-Null

try {
    try {
        Expand-Archive -Path $ZipPath -DestinationPath $extractTmp -Force
    } catch {
        Stop-WithError "failed to extract $ZipPath : $($_.Exception.Message)"
    }

    # Some uv release zips lay out the binaries flat at the archive root;
    # others nest them under uv-<platform>/. Handle both.
    $innerDir = Join-Path $extractTmp "uv-$UvPlatform"
    $candidate = $null
    if (Test-Path (Join-Path $innerDir 'uv.exe')) {
        $candidate = Join-Path $innerDir 'uv.exe'
    } elseif (Test-Path (Join-Path $extractTmp 'uv.exe')) {
        $candidate = Join-Path $extractTmp 'uv.exe'
    } else {
        # Last resort: recursive search (newer zip layouts may add wrappers).
        $found = Get-ChildItem -Path $extractTmp -Filter 'uv.exe' -Recurse -ErrorAction SilentlyContinue | Select-Object -First 1
        if ($found) {
            $candidate = $found.FullName
        }
    }

    if (-not $candidate) {
        Stop-WithError "extracted archive missing expected 'uv.exe' binary"
    }

    New-Item -ItemType Directory -Force -Path $UvDir | Out-Null
    Copy-Item -Force $candidate $UvBin
} finally {
    Remove-Item -Recurse -Force $extractTmp -ErrorAction SilentlyContinue
}

# Write the install stamp (includes arch so x64/arm64 swaps invalidate cache).
"$UvReleaseTag $Arch" | Out-File -FilePath $StampPath -Encoding ascii -NoNewline

# Quick smoke test - fail loud if the extracted binary doesn't run, but only
# when host arch matches the asset arch (otherwise the binary is intentionally
# unrunnable here, e.g. cross-fetching arm64 on x64 CI).
$archMatches = ($hostArch -eq 'x64' -and $Arch -eq 'amd64') -or `
               ($hostArch -eq 'arm64' -and $Arch -eq 'arm64')
if ($archMatches) {
    try {
        $versionOutput = & $UvBin --version 2>&1
        if ($LASTEXITCODE -ne 0) {
            Stop-WithError "extracted uv failed to execute (exit $LASTEXITCODE) - binary may be incomplete"
        }
        Write-Log "extracted $versionOutput"
    } catch {
        Stop-WithError "extracted uv failed to execute - binary may be incomplete: $($_.Exception.Message)"
    }
}

$sizeKb = [math]::Round((Get-Item $UvBin).Length / 1KB, 1)
Write-Log "size on disk: $sizeKb KB"

Write-Output "uv $UvReleaseTag ($Arch) installed at $UvBin"
