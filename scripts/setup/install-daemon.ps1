<#
    install-daemon.ps1

    Windows port of scripts/setup/install-daemon.sh (TASK-004 of the
    v0.4.0 Windows port). Spawned by Go's app_setup.go on the first
    launch where %USERPROFILE%\.jarvis\.setup-version-<ver> is missing.

    Installs the Python interpreter (Phase 1, python_install) and the
    daemon's virtual environment + source tree (Phase 2, venv_install)
    into %USERPROFILE%\.jarvis\. Phases 3 and 4 (VibeVoice + Whisper
    weight downloads) are NOT this script's responsibility — the
    daemon's own model_status.prefetch_models handles them post-launch,
    and the Go bridge re-emits those events on the `setup` channel.

    Usage:
      powershell -ExecutionPolicy Bypass -File install-daemon.ps1 `
          <uv-binary> <bundled-daemon-source>

      <uv-binary>             absolute path to the bundled `uv.exe`
                              (TASK-006 places this under
                              <bundle>\Resources\setup\uv.exe).
      <bundled-daemon-source> absolute path to the bundled jarvis-daemon
                              source tree (TASK-005/006 place this under
                              <bundle>\Resources\jarvis-daemon\).

    Stderr contract: every recognised line begins with one of the
    PHASE_* prefixes documented in docs/setup-events.md (lockstep with
    the bash sibling). All other stderr lines are passed through
    untouched and tee'd to %USERPROFILE%\.jarvis\logs\setup.log so the
    Go-side log viewer can render them. Stdout is reserved for tool
    output; consumers MUST NOT parse stdout.

    Note on line endings: PowerShell on Windows emits `\r\n` per line
    by default. The Go-side stderr parser (TASK-015) trims trailing
    `\r` before regex-matching, so the PHASE_* prefixes parse
    identically to the bash version.

    Resumability: per-phase sentinel files
    (<JarvisHome>\python\.fetch-complete and
    <JarvisHome>\jarvis-daemon-env\.venv-complete) let the script
    no-op completed phases on retry. The final sentinel
    <JarvisHome>\.setup-version-<ver> is written atomically
    (tmp + Move-Item) only after both phases succeed.

    Windows 10 + Windows 11. x64 and arm64 supported.
#>

[CmdletBinding()]
param(
    [Parameter(Mandatory = $true, Position = 0)]
    [string]$UvBin,

    [Parameter(Mandatory = $true, Position = 1)]
    [string]$BundledDaemonSrc
)

# -----------------------------------------------------------------------------
# Strict-mode + global error handling. `set -e` (bash) has no perfect
# PowerShell analogue for external commands — we explicitly check
# $LASTEXITCODE after every native invocation instead of relying on
# automatic propagation. We DO want $ErrorActionPreference = 'Stop' so
# PowerShell-cmdlet failures abort (the `try` at the bottom of the file
# turns them into PHASE_ERROR + non-zero exit).
#
# We do NOT set $PSNativeCommandUseErrorActionPreference = $true because
# (a) it's PS7-only, breaking Windows PowerShell 5.1 (default on Win10/11),
# and (b) it would short-circuit our explicit $LASTEXITCODE checks (e.g.
# the curl HEAD probe and robocopy, both of which legitimately return
# non-zero for benign cases).
# -----------------------------------------------------------------------------
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

# -----------------------------------------------------------------------------
# Constants — keep in lockstep with build/scripts/fetch-python.sh /
# build/scripts/fetch-python.ps1 (TASK-005) and the bash install-daemon.sh.
# -----------------------------------------------------------------------------
$SetupVersion        = '0.2.0'
$PbsReleaseTag       = '20260510'
$PbsCpythonVersion   = '3.13.13'
# python-build-standalone Windows asset family is `install_only_stripped`
# on Windows (vs `install_only` on macOS). TASK-005 mirrors this exact name.
# x64 → x86_64-pc-windows-msvc; arm64 → aarch64-pc-windows-msvc.
$PbsBaseUrl          = "https://github.com/astral-sh/python-build-standalone/releases/download/$PbsReleaseTag"

# Disk free requirement: 4 GB in bytes (4 * 1024 * 1024 * 1024).
$MinFreeDiskBytes    = 4GB

# Download progress poll interval, in seconds. Phase 1 only.
$ProgressPollSeconds = 2

# -----------------------------------------------------------------------------
# Paths — mirror the bash layout, but use Windows separators + filenames.
# `python.exe` lives at the top of the unpacked interpreter dir (no `bin/`
# on Windows). The venv interpreter lives under `Scripts\python.exe` (vs
# `bin/python3` on macOS).
# -----------------------------------------------------------------------------
$JarvisHome          = Join-Path $env:USERPROFILE '.jarvis'
$LogsDir             = Join-Path $JarvisHome 'logs'
$SetupLog            = Join-Path $LogsDir   'setup.log'

$PythonDir           = Join-Path $JarvisHome 'python'
$PythonCacheDir      = Join-Path $PythonDir  '.fetch-cache'
$PythonSentinel      = Join-Path $PythonDir  '.fetch-complete'
$PythonBin           = Join-Path $PythonDir  'python.exe'

$VenvDir             = Join-Path $JarvisHome 'jarvis-daemon-env'
$VenvSentinel        = Join-Path $VenvDir    '.venv-complete'
$VenvReqShaPath      = Join-Path $VenvDir    '.requirements-sha256'
$VenvPython          = Join-Path $VenvDir    'Scripts\python.exe'

$DaemonSrcDir        = Join-Path $JarvisHome 'jarvis-daemon'
$FinalSentinel       = Join-Path $JarvisHome ".setup-version-$SetupVersion"

# Track the current phase so we can describe it in signal-handler-style logs.
$script:CurrentPhase = ''

# -----------------------------------------------------------------------------
# Architecture detection. python-build-standalone publishes separate assets
# for x86_64 and aarch64. Snapdragon Windows (arm64) MUST get the aarch64
# asset; x64 builds get x86_64. Map Go's GOARCH-style values to the
# upstream asset's processor token.
# -----------------------------------------------------------------------------
function Get-PbsAsset {
    $arch = $env:PROCESSOR_ARCHITECTURE
    # Under WOW64 (32-bit shell on 64-bit OS) PROCESSOR_ARCHITECTURE is x86
    # but PROCESSOR_ARCHITEW6432 carries the real arch.
    if ($env:PROCESSOR_ARCHITEW6432) { $arch = $env:PROCESSOR_ARCHITEW6432 }

    switch ($arch.ToUpperInvariant()) {
        'AMD64' { return "cpython-$PbsCpythonVersion+$PbsReleaseTag-x86_64-pc-windows-msvc-install_only_stripped.tar.gz" }
        'ARM64' { return "cpython-$PbsCpythonVersion+$PbsReleaseTag-aarch64-pc-windows-msvc-install_only_stripped.tar.gz" }
        default { return $null }
    }
}

# -----------------------------------------------------------------------------
# Logging — every log line gets tee'd to setup.log. NEVER write PHASE_*
# prefixes from Write-LogLine; those go through Emit-*.
# -----------------------------------------------------------------------------
function Initialize-LogDir {
    if (-not (Test-Path -LiteralPath $LogsDir)) {
        New-Item -ItemType Directory -Force -Path $LogsDir | Out-Null
    }
    # Touch so the file exists even if no log lines fire before an error.
    if (-not (Test-Path -LiteralPath $SetupLog)) {
        New-Item -ItemType File -Path $SetupLog -Force | Out-Null
    }
}

function Write-LogLine {
    param([Parameter(Mandatory)][string]$Message)
    $line = "[install-daemon] $Message"
    [Console]::Error.WriteLine($line)
    if (Test-Path -LiteralPath $LogsDir) {
        try { Add-Content -LiteralPath $SetupLog -Value $line -ErrorAction SilentlyContinue } catch { }
    }
}

# -----------------------------------------------------------------------------
# Event emitters — write the canonical stderr markers documented in
# docs/setup-events.md. They MUST NOT change format without updating that
# doc and notifying the Go parser.
#
# All output goes through [Console]::Error.WriteLine so we bypass
# PowerShell's `Write-Error` wrapper (which prepends category info that
# would break the regex). Lines end with the host's native newline; the
# Go parser strips trailing `\r` before matching.
# -----------------------------------------------------------------------------
function Emit-Phase {
    param([Parameter(Mandatory)][string]$Phase)
    $script:CurrentPhase = $Phase
    [Console]::Error.WriteLine("PHASE: $Phase")
}

function Emit-Progress {
    param([Parameter(Mandatory)][int]$Pct)
    if ($Pct -lt 0)   { $Pct = 0 }
    if ($Pct -gt 100) { $Pct = 100 }
    [Console]::Error.WriteLine("PHASE_PROGRESS: $Pct")
}

function Emit-Bytes {
    param([Parameter(Mandatory)][long]$Done, [Parameter(Mandatory)][long]$Total)
    [Console]::Error.WriteLine("PHASE_BYTES: $Done / $Total")
}

function Emit-Eta {
    param([Parameter(Mandatory)][int]$Seconds)
    [Console]::Error.WriteLine("PHASE_ETA: $Seconds")
}

function Emit-Done {
    param([Parameter(Mandatory)][string]$Phase)
    [Console]::Error.WriteLine("PHASE_DONE: $Phase")
    $script:CurrentPhase = ''
}

function Emit-Error {
    param([Parameter(Mandatory)][string]$Message)
    # Strip embedded newlines per docs/setup-events.md — error MUST be
    # single-line. Collapse runs of whitespace so multi-line stderr from
    # downstream tools doesn't blow up the React error banner.
    $msg = ($Message -replace "[\r\n]+", '; ') -replace '\s+', ' '
    [Console]::Error.WriteLine("PHASE_ERROR: $msg")
}

# -----------------------------------------------------------------------------
# Sentinel + idempotency helpers
# -----------------------------------------------------------------------------
function Test-Sentinel {
    param([Parameter(Mandatory)][string]$Path)
    return (Test-Path -LiteralPath $Path -PathType Leaf)
}

function Get-FileSha256 {
    param([Parameter(Mandatory)][string]$Path)
    return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()
}

# -----------------------------------------------------------------------------
# Resolve-PortaudioDll
#
# pyaudio 0.2.14 ships pre-built wheels for most CPython 3.x ABIs on Windows,
# but coverage for CPython 3.13 (the daemon's pinned interpreter, TASK-005)
# is patchy — when uv falls back to building pyaudio from source, the build
# needs `portaudio.h`, `portaudio_x64.lib`, and at runtime `portaudio.dll` on
# the Windows DLL search path. Bundling the DLL + import lib next to the
# Wails-built `jarvis.exe` lets the source build succeed without requiring
# the end user to install Visual Studio Build Tools or vcpkg.
#
# Search order (first hit wins):
#   1. $env:JARVIS_PORTAUDIO_PATH override (CI / power users / tests).
#   2. <script-parent>\..\lib\portaudio.dll  (production layout when the
#      script lives at <install>\Resources\setup\install-daemon.ps1, so the
#      bundled lib sits at <install>\Resources\lib\portaudio.dll).
#   3. <RepoRoot>\Resources\lib\portaudio.dll (dev / wails-dev layout — the
#      `Resources\lib\` directory checked into source as a staging hint).
#   4. <RepoRoot>\build\portaudio\<arch>\portaudio.dll (output of
#      build\scripts\fetch-portaudio.ps1 — used by `wails dev` workflows
#      where post-build.ps1 hasn't run yet).
#
# Returns the absolute DLL path on hit, $null on miss. NEVER throws — the
# caller decides whether a miss is fatal (preflight is best-effort; the
# venv_install phase is the authoritative gate via Emit-Error).
# -----------------------------------------------------------------------------
function Resolve-PortaudioDll {
    # 1. Explicit override.
    if ($env:JARVIS_PORTAUDIO_PATH) {
        $override = $env:JARVIS_PORTAUDIO_PATH
        if (Test-Path -LiteralPath $override -PathType Leaf) {
            return (Resolve-Path -LiteralPath $override).Path
        }
        # An override that points nowhere is a bug worth surfacing in the log,
        # but we don't fail here — let the preflight/venv warning fire so the
        # user gets the standard PHASE_ERROR with remediation.
        Write-LogLine "warning: JARVIS_PORTAUDIO_PATH=$override does not exist; ignoring override"
    }

    # 2. Production bundle layout: <install>\Resources\lib\portaudio.dll.
    #    $PSScriptRoot is the directory holding install-daemon.ps1, which in
    #    the bundle is <install>\Resources\setup\ — so the lib dir is one
    #    level up + lib.
    $bundledLib = Join-Path (Split-Path -Parent $PSScriptRoot) 'lib\portaudio.dll'
    if (Test-Path -LiteralPath $bundledLib -PathType Leaf) {
        return (Resolve-Path -LiteralPath $bundledLib).Path
    }

    # 3 + 4. Source-tree fallbacks. $PSScriptRoot in dev is
    #        <repo>\scripts\setup\install-daemon.ps1 — two levels up is the
    #        repo root.
    $repoRoot = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)

    $sourceTreeLib = Join-Path $repoRoot 'Resources\lib\portaudio.dll'
    if (Test-Path -LiteralPath $sourceTreeLib -PathType Leaf) {
        return (Resolve-Path -LiteralPath $sourceTreeLib).Path
    }

    # Detect arch the same way Get-PbsAsset does — keep these two paths in
    # lockstep so `build\portaudio\<arch>\` matches the script's auto-detect.
    # Guard against unset env vars (running under PowerShell Core on a non-
    # Windows host) by defaulting to ''.
    $arch = ''
    if ($env:PROCESSOR_ARCHITEW6432)      { $arch = $env:PROCESSOR_ARCHITEW6432 }
    elseif ($env:PROCESSOR_ARCHITECTURE) { $arch = $env:PROCESSOR_ARCHITECTURE }
    $archDir = switch ($arch.ToUpperInvariant()) {
        'AMD64' { 'x64' }
        'ARM64' { 'arm64' }
        default { $null }
    }
    if ($archDir) {
        $buildDirLib = Join-Path $repoRoot "build\portaudio\$archDir\portaudio.dll"
        if (Test-Path -LiteralPath $buildDirLib -PathType Leaf) {
            return (Resolve-Path -LiteralPath $buildDirLib).Path
        }
    }

    return $null
}

# Module-scope cache: populated once during preflight, consumed by the
# venv_install phase to set PORTAUDIO_PATH / PATH / INCLUDE / LIB before
# `uv pip install`. $null means "no bundled DLL found"; the venv phase will
# then emit a PHASE_ERROR if pip install fails on the pyaudio source build.
$script:PortaudioDllPath = $null

# -----------------------------------------------------------------------------
# Preflight — fail-fast checks before touching any disk state. Each failure
# emits a single PHASE_ERROR + exits 1, with the phase set to phase 1 so
# the SetupScreen renders the error under phase 1's row.
# -----------------------------------------------------------------------------
function Invoke-Preflight {
    Initialize-LogDir
    Write-LogLine 'preflight: starting'

    # Emit phase 1 immediately so the SetupScreen shows "python_install:
    # in-progress" during preflight instead of all-pending rows.
    Emit-Phase 'python_install'
    Emit-Progress 0

    # Arch — Windows port supports amd64 + arm64. Anything else (x86 / ia64)
    # is unsupported and there's no python-build-standalone asset to fetch.
    Write-LogLine 'preflight: checking arch'
    $asset = Get-PbsAsset
    if (-not $asset) {
        $arch = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
        Emit-Error "Jarvis requires Windows x64 or arm64; detected $arch"
        exit 1
    }

    # Disk free in %USERPROFILE% — query via Get-PSDrive on the drive
    # letter that hosts $env:USERPROFILE.
    Write-LogLine 'preflight: checking disk space'
    try {
        $driveLetter = (Split-Path -Qualifier $env:USERPROFILE).TrimEnd(':')
        $free = (Get-PSDrive -Name $driveLetter -ErrorAction Stop).Free
    } catch {
        $free = 0
    }
    if (-not $free -or $free -lt $MinFreeDiskBytes) {
        $haveMb = if ($free) { [int]($free / 1MB) } else { 0 }
        Emit-Error "insufficient disk space in $env:USERPROFILE: need 4 GB free, have $haveMb MB"
        exit 1
    }

    # Required external tools. `tar` is built into Windows 10 1803+ and
    # all of Windows 11. `curl.exe` is built into Windows 10 1803+. We
    # rely on both for the python tarball fetch + extract.
    Write-LogLine 'preflight: checking PATH tools'
    foreach ($tool in @('curl.exe', 'tar.exe')) {
        $cmd = Get-Command $tool -ErrorAction SilentlyContinue
        if (-not $cmd) {
            Emit-Error "required tool not found in PATH: $tool (Windows 10 1803+ ships both; please update Windows)"
            exit 1
        }
    }

    # uv binary must exist and be executable.
    Write-LogLine "preflight: checking uv binary at $UvBin"
    if (-not (Test-Path -LiteralPath $UvBin -PathType Leaf)) {
        Emit-Error "uv binary not found at $UvBin"
        exit 1
    }

    # Bundled daemon source must be a directory containing requirements.txt.
    Write-LogLine "preflight: checking bundled daemon source at $BundledDaemonSrc"
    if (-not (Test-Path -LiteralPath $BundledDaemonSrc -PathType Container)) {
        Emit-Error "bundled daemon source not found at $BundledDaemonSrc"
        exit 1
    }
    $reqPath = Join-Path $BundledDaemonSrc 'requirements.txt'
    if (-not (Test-Path -LiteralPath $reqPath -PathType Leaf)) {
        Emit-Error "bundled daemon source missing requirements.txt at $reqPath"
        exit 1
    }

    # portaudio.dll — pyaudio 0.2.14 may fall back to a source build on
    # CPython 3.13 (TASK-007). Resolve the bundled DLL location now so the
    # venv_install phase can prepend it to PATH + export PORTAUDIO_PATH
    # before `uv pip install` runs pyaudio's setup.py. We do NOT fail
    # preflight on a miss — pyaudio may pick up a pre-built wheel that
    # doesn't need the DLL at install time. The venv phase emits the
    # canonical PHASE_ERROR with remediation if pip ultimately can't
    # locate portaudio.
    Write-LogLine 'preflight: resolving bundled portaudio.dll'
    $script:PortaudioDllPath = Resolve-PortaudioDll
    if ($script:PortaudioDllPath) {
        Write-LogLine "preflight: portaudio.dll resolved at $script:PortaudioDllPath"
    } else {
        Write-LogLine 'preflight: no bundled portaudio.dll found (may rely on pyaudio pre-built wheel)'
    }

    if (-not (Test-Path -LiteralPath $JarvisHome)) {
        New-Item -ItemType Directory -Force -Path $JarvisHome | Out-Null
    }
    $haveMb = [int]($free / 1MB)
    Write-LogLine "preflight: ok (asset=$asset, free=$haveMb MB, uv=$UvBin)"
}

# -----------------------------------------------------------------------------
# Phase 1 — python_install
#
# Downloads python-build-standalone (CPython 3.13.13 x86_64 or aarch64
# pc-windows-msvc install_only_stripped) into
# <JarvisHome>\python\.fetch-cache\, SHA-verifies against the release's
# SHA256SUMS manifest, extracts (tar.exe), smoke-tests, and writes
# <JarvisHome>\python\.fetch-complete.
#
# Skip path: if the sentinel + a working python.exe already exist, we
# emit PHASE -> PHASE_PROGRESS 100 -> PHASE_DONE so progress reporting
# stays consistent on a resumed run.
# -----------------------------------------------------------------------------
function Invoke-PhasePython {
    Emit-Phase 'python_install'
    Write-LogLine 'phase 1: python_install — starting'

    if ((Test-Sentinel $PythonSentinel) -and (Test-Path -LiteralPath $PythonBin -PathType Leaf)) {
        Write-LogLine 'phase 1: python already installed (sentinel + interpreter present); skipping'
        Emit-Progress 100
        Emit-Done 'python_install'
        return
    }

    New-Item -ItemType Directory -Force -Path $PythonCacheDir | Out-Null
    $pbsAsset       = Get-PbsAsset
    $tarball        = Join-Path $PythonCacheDir $pbsAsset
    $tarballPartial = "$tarball.partial"
    $shasumsPath    = Join-Path $PythonCacheDir 'SHA256SUMS'

    # Always (re-)fetch SHA256SUMS — small and authoritative.
    Write-LogLine 'phase 1: fetching SHA256SUMS'
    $curlArgs = @(
        '--fail', '--location', '--show-error', '--silent',
        '--retry', '3', '--retry-delay', '2',
        '--output', $shasumsPath,
        "$PbsBaseUrl/SHA256SUMS"
    )
    & curl.exe @curlArgs 2>>$SetupLog
    if ($LASTEXITCODE -ne 0) {
        Emit-Error "failed to download SHA256SUMS from $PbsBaseUrl; check your network connection"
        exit 1
    }

    # python-build-standalone publishes SHA256SUMS as `<sha>  <filename>`
    # lines. Find the line whose filename column matches our asset.
    $expectedSha = $null
    foreach ($line in Get-Content -LiteralPath $shasumsPath) {
        $parts = ($line -split '\s+', 2)
        if ($parts.Length -ge 2 -and $parts[1].Trim() -eq $pbsAsset) {
            $expectedSha = $parts[0].Trim().ToLowerInvariant()
            break
        }
    }
    if (-not $expectedSha) {
        Emit-Error "no SHA256 entry for $pbsAsset in SHA256SUMS — asset renamed or removed upstream"
        exit 1
    }

    # If we already have a fully-downloaded tarball whose SHA matches,
    # reuse it (covers the case where a previous run downloaded but
    # crashed before extract).
    $needDownload = $true
    if (Test-Path -LiteralPath $tarball -PathType Leaf) {
        $actualSha = Get-FileSha256 $tarball
        if ($actualSha -eq $expectedSha) {
            Write-LogLine 'phase 1: cached tarball SHA matches; reusing'
            $needDownload = $false
            Emit-Progress 100
        } else {
            Write-LogLine 'phase 1: cached tarball SHA mismatch; re-downloading'
            Remove-Item -LiteralPath $tarball -Force -ErrorAction SilentlyContinue
        }
    }

    if ($needDownload) {
        # Clean up any stale partial. We can't safely resume curl --range
        # without server cooperation; let curl --retry handle drops.
        if (Test-Path -LiteralPath $tarballPartial) {
            Remove-Item -LiteralPath $tarballPartial -Force -ErrorAction SilentlyContinue
        }

        # Best-effort total bytes from a HEAD request. If HEAD fails we
        # still try the GET and just skip byte math.
        $totalBytes = 0
        try {
            $headOutput = & curl.exe --silent --show-error --location --head --fail `
                                       --retry 2 --retry-delay 1 `
                                       "$PbsBaseUrl/$pbsAsset" 2>$null
            if ($LASTEXITCODE -eq 0) {
                foreach ($line in $headOutput) {
                    if ($line -match '^(?i)content-length:\s*(\d+)') {
                        $totalBytes = [long]$Matches[1]
                        # Keep the LAST Content-Length (HTTP redirects may include earlier ones).
                    }
                }
            }
        } catch { $totalBytes = 0 }

        $sizeMb = if ($totalBytes -gt 0) { [int]($totalBytes / 1MB) } else { 95 }
        Write-LogLine "phase 1: downloading $pbsAsset (~$sizeMb MB)"
        Emit-Progress 0
        if ($totalBytes -gt 0) { Emit-Bytes 0 $totalBytes }

        # Spawn curl in the background so we can poll the partial file
        # size for progress events. Start-Process with -PassThru lets us
        # inspect ExitCode after the process exits.
        $curlLog = Join-Path $PythonCacheDir 'curl.log'
        Set-Content -LiteralPath $curlLog -Value '' -Force
        $curlProc = Start-Process -FilePath 'curl.exe' `
                                   -ArgumentList @(
                                       '--fail', '--location', '--show-error', '--silent',
                                       '--retry', '3', '--retry-delay', '2',
                                       '--output', $tarballPartial,
                                       "$PbsBaseUrl/$pbsAsset"
                                   ) `
                                   -PassThru `
                                   -RedirectStandardError $curlLog `
                                   -NoNewWindow

        $startedAt = [DateTime]::UtcNow
        $lastPct   = 0
        while (-not $curlProc.HasExited) {
            Start-Sleep -Seconds $ProgressPollSeconds
            $sizeNow = 0
            if (Test-Path -LiteralPath $tarballPartial) {
                try { $sizeNow = (Get-Item -LiteralPath $tarballPartial -ErrorAction Stop).Length } catch { $sizeNow = 0 }
            }
            if ($totalBytes -gt 0) {
                $pct = [int]([long]($sizeNow * 100) / $totalBytes)
                if ($pct -gt 100) { $pct = 100 }
                if ($pct -ne $lastPct) {
                    Emit-Bytes $sizeNow $totalBytes
                    Emit-Progress $pct
                    $lastPct = $pct
                }
                $elapsed = ([DateTime]::UtcNow - $startedAt).TotalSeconds
                if ($elapsed -ge 2 -and $sizeNow -gt 0) {
                    $rate = [long]($sizeNow / $elapsed)
                    if ($rate -gt 0) {
                        $remaining = $totalBytes - $sizeNow
                        $eta = [int]($remaining / $rate)
                        if ($eta -ge 0) { Emit-Eta $eta }
                    }
                }
            }
        }
        $curlProc.WaitForExit()
        $exit = $curlProc.ExitCode

        if ($exit -ne 0) {
            $errMsg = ''
            if (Test-Path -LiteralPath $curlLog) {
                $errMsg = (Get-Content -LiteralPath $curlLog -Raw -ErrorAction SilentlyContinue)
                if ($null -eq $errMsg) { $errMsg = '' }
                if ($errMsg.Length -gt 240) { $errMsg = $errMsg.Substring(0, 240) }
            }
            if (-not $errMsg) { $errMsg = "curl exited with status $exit" }
            if (Test-Path -LiteralPath $tarballPartial) {
                Remove-Item -LiteralPath $tarballPartial -Force -ErrorAction SilentlyContinue
            }
            Emit-Error "failed to download python-build-standalone after retries: $errMsg"
            exit 1
        }

        Move-Item -LiteralPath $tarballPartial -Destination $tarball -Force
        Emit-Progress 100
        Write-LogLine 'phase 1: download complete'

        # Verify SHA256 after move.
        $actualSha = Get-FileSha256 $tarball
        if ($actualSha -ne $expectedSha) {
            Remove-Item -LiteralPath $tarball -Force -ErrorAction SilentlyContinue
            Emit-Error "SHA256 mismatch on downloaded tarball: expected $expectedSha got $actualSha"
            exit 1
        }
        Write-LogLine "phase 1: SHA256 verified ($actualSha)"
    }

    # Extract into a temp dir, then atomically swap into PYTHON_DIR.
    # python-build-standalone unpacks to a top-level `python/` dir on
    # Windows just like on macOS.
    Write-LogLine 'phase 1: extracting'
    $extractTmp = Join-Path $JarvisHome ("python.extract." + [guid]::NewGuid().ToString('N').Substring(0, 8))
    New-Item -ItemType Directory -Force -Path $extractTmp | Out-Null

    & tar.exe -xzf $tarball -C $extractTmp 2>>$SetupLog
    if ($LASTEXITCODE -ne 0) {
        Remove-Item -LiteralPath $extractTmp -Recurse -Force -ErrorAction SilentlyContinue
        Emit-Error "failed to extract $pbsAsset"
        exit 1
    }
    $extractedPython = Join-Path $extractTmp 'python\python.exe'
    if (-not (Test-Path -LiteralPath $extractedPython -PathType Leaf)) {
        Remove-Item -LiteralPath $extractTmp -Recurse -Force -ErrorAction SilentlyContinue
        Emit-Error 'extracted python archive missing expected python\python.exe'
        exit 1
    }

    # Preserve our cache dir (it lives INSIDE PYTHON_DIR) across the swap.
    $cacheBackup = $null
    if (Test-Path -LiteralPath $PythonCacheDir -PathType Container) {
        $cacheBackup = Join-Path $JarvisHome ("python.cache-backup." + [guid]::NewGuid().ToString('N').Substring(0, 8))
        New-Item -ItemType Directory -Force -Path $cacheBackup | Out-Null
        Move-Item -LiteralPath $PythonCacheDir -Destination (Join-Path $cacheBackup 'cache') -Force
    }

    if (Test-Path -LiteralPath $PythonDir) {
        Remove-Item -LiteralPath $PythonDir -Recurse -Force
    }
    Move-Item -LiteralPath (Join-Path $extractTmp 'python') -Destination $PythonDir -Force
    Remove-Item -LiteralPath $extractTmp -Recurse -Force -ErrorAction SilentlyContinue

    if ($cacheBackup) {
        if (-not (Test-Path -LiteralPath $PythonDir)) {
            New-Item -ItemType Directory -Force -Path $PythonDir | Out-Null
        }
        Move-Item -LiteralPath (Join-Path $cacheBackup 'cache') -Destination $PythonCacheDir -Force
        Remove-Item -LiteralPath $cacheBackup -Recurse -Force -ErrorAction SilentlyContinue
    }

    # Smoke test the extracted interpreter.
    & $PythonBin --version *>> $SetupLog
    if ($LASTEXITCODE -ne 0) {
        Emit-Error 'extracted python.exe failed to execute — bundle may be incomplete'
        exit 1
    }

    # Write the per-phase sentinel atomically (write to tmp, rename).
    $sentinelTmp = "$PythonSentinel." + [guid]::NewGuid().ToString('N').Substring(0, 8)
    @(
        "pbs_tag=$PbsReleaseTag"
        "cpython_version=$PbsCpythonVersion"
        "sha256=$expectedSha"
    ) | Set-Content -LiteralPath $sentinelTmp -Encoding UTF8
    Move-Item -LiteralPath $sentinelTmp -Destination $PythonSentinel -Force

    Emit-Done 'python_install'
    Write-LogLine 'phase 1: python_install — done'
}

# -----------------------------------------------------------------------------
# sync_daemon_source — copy the bundled daemon tree to
# <JarvisHome>\jarvis-daemon\. Excludes __pycache__, *.pyc, and tests/ to
# keep the install lean. Throws on failure.
# -----------------------------------------------------------------------------
function Sync-DaemonSource {
    Write-LogLine "syncing daemon source: $BundledDaemonSrc -> $DaemonSrcDir"
    if (-not (Test-Path -LiteralPath $DaemonSrcDir)) {
        New-Item -ItemType Directory -Force -Path $DaemonSrcDir | Out-Null
    }
    # Use robocopy: /MIR (mirror = copy + purge), /XD excludes dirs, /XF
    # excludes files, /NFL /NDL /NJH /NJS keep stderr quiet. robocopy
    # uses non-standard exit codes: 0-7 are success, >=8 indicates errors.
    $robocopyArgs = @(
        $BundledDaemonSrc,
        $DaemonSrcDir,
        '/MIR',
        '/XD', '__pycache__', 'tests',
        '/XF', '*.pyc',
        '/NFL', '/NDL', '/NJH', '/NJS', '/NC', '/NS', '/NP', '/R:2', '/W:2'
    )
    & robocopy.exe @robocopyArgs *>> $SetupLog
    # robocopy exit codes 0-7 are non-error states. Anything >=8 is a real failure.
    if ($LASTEXITCODE -ge 8) {
        throw "robocopy failed with exit code $LASTEXITCODE while copying $BundledDaemonSrc -> $DaemonSrcDir"
    }
    # Reset $LASTEXITCODE so downstream `if ($LASTEXITCODE -ne 0)` checks don't trip.
    $global:LASTEXITCODE = 0
}

# -----------------------------------------------------------------------------
# Phase 2 — venv_install
#
#   1. `uv venv <VenvDir> --python <PythonBin>`
#   2. `uv pip install -r <bundled>\requirements.txt`
#   3. Sync the bundled daemon source into <JarvisHome>\jarvis-daemon\
#   4. Write <JarvisHome>\jarvis-daemon-env\.venv-complete
#
# Skip path: sentinel present AND the requirements.txt sha matches the
# one recorded in .requirements-sha256 -> no-op (still rsyncs daemon
# source in case the bundle changed without bumping requirements).
# -----------------------------------------------------------------------------
function Invoke-PhaseVenv {
    Emit-Phase 'venv_install'
    Write-LogLine 'phase 2: venv_install — starting'

    $reqPath = Join-Path $BundledDaemonSrc 'requirements.txt'
    $reqSha  = Get-FileSha256 $reqPath

    $venvComplete = (Test-Sentinel $VenvSentinel) `
        -and (Test-Path -LiteralPath $VenvPython -PathType Leaf) `
        -and (Test-Sentinel $VenvReqShaPath)
    $cachedSha = $null
    if ($venvComplete) {
        try { $cachedSha = (Get-Content -LiteralPath $VenvReqShaPath -Raw -ErrorAction Stop).Trim() } catch { $cachedSha = '' }
    }
    if ($venvComplete -and ($cachedSha -eq $reqSha)) {
        Write-LogLine 'phase 2: venv already installed (sentinel + matching requirements sha); skipping'
        Emit-Progress 100
        try {
            Sync-DaemonSource
        } catch {
            Emit-Error "failed to sync daemon source to $DaemonSrcDir : $($_.Exception.Message)"
            exit 1
        }
        Emit-Done 'venv_install'
        return
    }

    # Phase 2 has no fine-grained byte counters — uv emits free-form
    # progress lines which we tee to setup.log. Emit coarse PROGRESS
    # pings at the major step boundaries so the React phase bar advances.
    Emit-Progress 5

    # Step 1 — create the venv.
    Write-LogLine "phase 2: creating venv at $VenvDir"
    if (Test-Path -LiteralPath $VenvDir) {
        # Wipe any partially-installed venv from a prior interrupted run.
        # The sentinel check above already proved this venv isn't ready.
        Remove-Item -LiteralPath $VenvDir -Recurse -Force
    }
    & $UvBin venv $VenvDir --python $PythonBin *>> $SetupLog
    if ($LASTEXITCODE -ne 0) {
        Emit-Error "uv venv failed to create environment at $VenvDir"
        exit 1
    }
    Emit-Progress 20

    # Step 2 — install requirements. uv pip install needs the target
    # interpreter; on Windows the venv python lives at Scripts\python.exe.
    Write-LogLine 'phase 2: installing requirements via uv pip install'
    Emit-Progress 30

    # PORTAUDIO env hints (TASK-007). pyaudio 0.2.14 may compile from source
    # on CPython 3.13 when no pre-built wheel is available; the source build
    # needs portaudio.h + portaudio_x64.lib at link time and portaudio.dll
    # at runtime (the latter is loaded by `import _portaudio` once pyaudio
    # is importable). We point the build at the bundled DLL the preflight
    # resolved into $script:PortaudioDllPath:
    #
    #   - PORTAUDIO_PATH : pyaudio's setup.py reads this to locate
    #                      portaudio.h / portaudio_x64.lib (expects the
    #                      directory containing portaudio.dll).
    #   - PATH           : prepended so the Windows DLL loader finds
    #                      portaudio.dll when pyaudio is later imported.
    #   - INCLUDE / LIB  : MSVC source-build fallback (cl.exe reads these
    #                      directly — harmless when uv resolves a wheel).
    #
    # We do NOT override any caller-provided env var (CI / power users may
    # have a custom portaudio installation); we only set the var when it's
    # missing AND we have a bundled DLL path to point at. The previous
    # process env is restored in the `finally` block so subsequent script
    # runs (e.g. retry after a re-launch) see a clean baseline.
    $envSnapshot = @{
        PATH            = $env:PATH
        PORTAUDIO_PATH  = $env:PORTAUDIO_PATH
        INCLUDE         = $env:INCLUDE
        LIB             = $env:LIB
    }
    $portaudioDir = $null
    if ($script:PortaudioDllPath) {
        $portaudioDir = Split-Path -Parent $script:PortaudioDllPath
        Write-LogLine "phase 2: exporting portaudio env hints (lib dir: $portaudioDir)"
        if (-not $env:PORTAUDIO_PATH) { $env:PORTAUDIO_PATH = $portaudioDir }
        # Prepend rather than overwrite — preserves anything the caller put
        # ahead of us (e.g. the bundled python's directory from a prior
        # phase).
        if ($env:PATH) {
            $env:PATH = "$portaudioDir;$env:PATH"
        } else {
            $env:PATH = $portaudioDir
        }
        if (-not $env:INCLUDE) { $env:INCLUDE = $portaudioDir }
        if (-not $env:LIB)     { $env:LIB     = $portaudioDir }
    } else {
        Write-LogLine 'phase 2: no bundled portaudio.dll resolved; relying on pyaudio pre-built wheel'
    }

    $uvErrFile = (New-TemporaryFile).FullName
    try {
        try {
            # 1>> appends stdout to setup.log; 2> writes stderr fresh to the
            # temp file (we capture it to extract a single-line error
            # snippet for PHASE_ERROR, then tee to setup.log on success).
            & $UvBin pip install `
                --python $VenvPython `
                -r $reqPath `
                1>> $SetupLog 2> $uvErrFile
            $uvExit = $LASTEXITCODE
        } catch {
            $uvExit = if ($LASTEXITCODE) { $LASTEXITCODE } else { 1 }
        }
    } finally {
        # Restore the pre-call env so a subsequent retry starts clean.
        $env:PATH           = $envSnapshot.PATH
        $env:PORTAUDIO_PATH = $envSnapshot.PORTAUDIO_PATH
        $env:INCLUDE        = $envSnapshot.INCLUDE
        $env:LIB            = $envSnapshot.LIB
    }
    if ($uvExit -ne 0) {
        $errMsg = ''
        if (Test-Path -LiteralPath $uvErrFile) {
            $errMsg = (Get-Content -LiteralPath $uvErrFile -Raw -ErrorAction SilentlyContinue)
            if ($null -eq $errMsg) { $errMsg = '' }
            # Take the last 400 chars, single-line.
            if ($errMsg.Length -gt 400) { $errMsg = $errMsg.Substring($errMsg.Length - 400) }
            $errMsg = ($errMsg -replace "[\r\n]+", '; ') -replace '\s+', ' '
        }
        if (-not $errMsg) { $errMsg = 'see setup.log for details' }
        Remove-Item -LiteralPath $uvErrFile -Force -ErrorAction SilentlyContinue

        # TASK-007 failure case: detect the canonical pyaudio source-build
        # failure signatures (`portaudio.h: No such file` / `LNK1181: cannot
        # open input file 'portaudio_x64.lib'`) and emit a remediation-rich
        # PHASE_ERROR instead of the generic `uv pip install failed: ...`.
        # The remediation differs by whether we *had* a bundled DLL — if we
        # did, the failure is most likely a corrupt bundle; if we didn't,
        # the user needs to re-install Jarvis or bundle the DLL manually.
        $isPortaudioFailure = ($errMsg -match '(?i)portaudio\.h') `
                          -or ($errMsg -match '(?i)portaudio_x64\.lib') `
                          -or ($errMsg -match '(?i)Could not find PortAudio')
        if ($isPortaudioFailure) {
            if ($script:PortaudioDllPath) {
                Emit-Error "uv pip install failed locating portaudio at $script:PortaudioDllPath; the bundled DLL may be corrupt. Reinstall Jarvis or rerun 'pwsh build\scripts\fetch-portaudio.ps1'. Original error: $errMsg"
            } else {
                Emit-Error "uv pip install failed: portaudio.dll is missing. The Jarvis installer should bundle Resources\lib\portaudio.dll; reinstall Jarvis, or set the JARVIS_PORTAUDIO_PATH env var to an existing portaudio.dll and retry setup. Original error: $errMsg"
            }
            exit 1
        }
        Emit-Error "uv pip install failed: $errMsg"
        exit 1
    }
    # Tee the captured stderr to setup.log so it's in the log viewer.
    if (Test-Path -LiteralPath $uvErrFile) {
        try { Get-Content -LiteralPath $uvErrFile | Add-Content -LiteralPath $SetupLog } catch { }
        Remove-Item -LiteralPath $uvErrFile -Force -ErrorAction SilentlyContinue
    }
    Emit-Progress 85

    # Step 3 — copy daemon source.
    try {
        Sync-DaemonSource
    } catch {
        Emit-Error "failed to sync daemon source to $DaemonSrcDir : $($_.Exception.Message)"
        exit 1
    }
    Emit-Progress 95

    # Step 4 — record requirements sha + write sentinel atomically.
    $shaTmp = "$VenvReqShaPath." + [guid]::NewGuid().ToString('N').Substring(0, 8)
    Set-Content -LiteralPath $shaTmp -Value $reqSha -Encoding UTF8
    Move-Item -LiteralPath $shaTmp -Destination $VenvReqShaPath -Force

    $sentinelTmp = "$VenvSentinel." + [guid]::NewGuid().ToString('N').Substring(0, 8)
    @(
        "requirements_sha256=$reqSha"
        "uv_binary=$UvBin"
    ) | Set-Content -LiteralPath $sentinelTmp -Encoding UTF8
    Move-Item -LiteralPath $sentinelTmp -Destination $VenvSentinel -Force

    Emit-Progress 100
    Emit-Done 'venv_install'
    Write-LogLine 'phase 2: venv_install — done'
}

# -----------------------------------------------------------------------------
# write_sentinel — atomically (tmp + rename) write the final
# <JarvisHome>\.setup-version-<ver> file. Go's IsSetupComplete reads
# this; the contents are advisory but documented for debugging.
# -----------------------------------------------------------------------------
function Write-FinalSentinel {
    $reqSha    = Get-FileSha256 (Join-Path $BundledDaemonSrc 'requirements.txt')
    $timestamp = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")

    $tmp = "$FinalSentinel." + [guid]::NewGuid().ToString('N').Substring(0, 8)
    @(
        "version: $SetupVersion"
        "timestamp: $timestamp"
        "requirements_sha256: $reqSha"
        "python_pbs_tag: $PbsReleaseTag"
    ) | Set-Content -LiteralPath $tmp -Encoding UTF8
    Move-Item -LiteralPath $tmp -Destination $FinalSentinel -Force
    Write-LogLine "wrote final sentinel $FinalSentinel"
}

# -----------------------------------------------------------------------------
# Main
# -----------------------------------------------------------------------------
function Invoke-Main {
    Invoke-Preflight

    # Fast-path: if the final sentinel is already present and BOTH
    # per-phase sentinels exist, this is a re-run with nothing to do.
    # Emit PHASE / DONE markers for both phases so React renders a
    # fully-green column even on this no-op path, then exit.
    if ((Test-Sentinel $FinalSentinel) `
            -and (Test-Sentinel $PythonSentinel) `
            -and (Test-Sentinel $VenvSentinel)) {
        Write-LogLine "setup already complete (sentinel $FinalSentinel present); replaying phase markers"
        Emit-Phase     'python_install'
        Emit-Progress  100
        Emit-Done      'python_install'
        Emit-Phase     'venv_install'
        Emit-Progress  100
        Emit-Done      'venv_install'
        exit 0
    }

    Invoke-PhasePython
    Invoke-PhaseVenv
    Write-FinalSentinel
    Write-LogLine "setup complete (v$SetupVersion)"
}

# -----------------------------------------------------------------------------
# Entry point. Any uncaught exception bubbles up here; we translate it
# into a PHASE_ERROR (single-line) + non-zero exit so the Go-side parser
# always sees a structured failure for unexpected aborts (matching the
# bash version's `set -e` behaviour).
# -----------------------------------------------------------------------------
try {
    Invoke-Main
} catch {
    $msg = $_.Exception.Message
    if (-not $msg) { $msg = 'install-daemon.ps1 aborted with no message' }
    Emit-Error "unhandled error in install-daemon.ps1: $msg"
    exit 1
}
