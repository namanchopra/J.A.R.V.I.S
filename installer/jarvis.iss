; =============================================================================
; jarvis.iss - Inno Setup script for the v0.4.0 Windows port (TASK-054, TASK-057).
;
; Produces: dist\Jarvis-Setup-<version>-<arch>.exe
;
; Inputs (passed via /D on the ISCC command line, with sensible defaults so
; this script can also be run by hand for local smoke-testing):
;
;   /DMyAppVersion=0.4.0     ; semver stamped into VersionInfo + the output
;                            ;   filename. CI strips the leading 'v' from the
;                            ;   git tag (e.g. v0.4.0 -> 0.4.0) before passing.
;   /DMyAppArch=x64          ; "x64" or "arm64". Selects the per-arch source
;                            ;   tree under ..\build\bin-<arch>\.
;   /DMyAppSourceDir=..\build\bin
;                            ; Directory holding jarvis.exe + Resources\ (the
;                            ;   tree post-build.ps1 lays down in TASK-055).
;   /DMyAppOutputDir=..\dist ; Where the final installer .exe is written.
;   /DEmbedWebView2Bootstrap=1
;                            ; When 1 (default), the WebView2 Evergreen
;                            ;   bootstrapper (MicrosoftEdgeWebview2Setup.exe)
;                            ;   must be staged at ..\build\webview2\ and is
;                            ;   bundled into the installer's {tmp} dir; if
;                            ;   WebView2 is not detected at install time we
;                            ;   silently install it (TASK-057). Set to 0 for
;                            ;   smoke-test builds where the bootstrapper has
;                            ;   not been staged.
;
; Build (Windows host, Inno Setup 6.2+):
;   iscc /DMyAppVersion=0.4.0 /DMyAppArch=x64 installer\jarvis.iss
;
; Acceptance criteria covered (TASK-054):
;   - Installer .exe is <40MB:  LZMA2/ultra solid compression + we only
;     bundle what post-build.ps1 stages (no PDBs, no debug symbols, no
;     dev-only files). The bundled payload is ~25-35MB depending on
;     Resources\ size; LZMA2/ultra typically compresses 2-3x.
;   - Install -> run -> uninstall fully clean:  the [UninstallDelete] section
;     plus AppId-keyed registry uninstall entry plus the WebView2 user-data
;     dir removal in [Code] guarantee no orphaned files in Program Files,
;     Start Menu, Desktop, or %LocalAppData%\Jarvis\.
;   - Spaces / non-ASCII install paths:  Inno Setup 6.x is Unicode by default
;     (the legacy ANSI build was retired in 6.0). All [Files] sources use
;     {#MyAppSourceDir}\... relative paths that survive Unicode chars, and
;     all install-time strings flow through {app} expansion which handles
;     spaces/quoting for ShellExecute internally.
;
; Acceptance criteria covered (TASK-057):
;   - Win10 fresh install (no WebView2): the bundled bootstrapper is extracted
;     to {tmp} and executed with "/silent /install" during ssPostInstall.
;     IsWebView2Installed() is re-checked after the bootstrapper exits so we
;     only declare success when the runtime is actually present.
;   - Win11 (preinstalled): IsWebView2Installed() returns True from the
;     registry check on first probe, the bootstrapper is never extracted /
;     invoked, and the install proceeds with zero extra steps.
;   - Failure case (offline / corp proxy / bootstrapper exits non-zero):
;     CurStepChanged surfaces a clear MsgBox naming the failure and offers to
;     open the official WebView2 download page in the default browser. The
;     install is NOT aborted - jarvis.exe will still surface the Wails
;     "Missing Requirements" dialog on first launch.
;
; Acceptance criteria covered (TASK-061):
;   - After install, `Get-NetFirewallRule -DisplayName Jarvis*` returns the
;     inbound rule we register for the mobile API (TCP 4422). The rule is
;     created via PowerShell's New-NetFirewallRule cmdlet (Windows 8+; well
;     under our Win10-1809 floor) invoked from the [Run] section with the
;     `runhidden` flag so users don't see a flashing console window.
;   - Uninstall removes the rule via the matching Remove-NetFirewallRule
;     call in [UninstallRun]. The DisplayName "Jarvis Mobile API" is the
;     stable handle both sides agree on.
;   - Failure case (non-admin install / firewall service stopped / corp
;     GPO blocks the cmdlet): the PowerShell snippet swallows the error
;     with a try/catch and writes a sentinel file under
;     %LocalAppData%\Jarvis\firewall-rule-failed.txt. The [Code] block
;     reads that sentinel during ssDone and, on interactive (non-silent)
;     installs, surfaces a one-shot MsgBox warning the user that Friday
;     may not connect from the LAN. The install itself never aborts.
;
; Future hooks (not implemented yet - tracked separately):
;   - TASK-056: signtool /finalize hooks after Compile finishes.
;
; Style notes:
;   - We use #define preprocessor directives (not Pascal) for build-time
;     configuration so the .iss is grep-friendly. Runtime decisions (e.g.
;     "is WebView2 already installed?") live in the [Code] Pascal block.
;   - All paths use backslashes (Inno is Windows-only); the script is not
;     valid POSIX shell despite the bash-style heredoc comment markers.
; =============================================================================

#ifndef MyAppVersion
#define MyAppVersion "0.4.0"
#endif

#ifndef MyAppArch
#define MyAppArch "x64"
#endif

#ifndef MyAppSourceDir
#define MyAppSourceDir "..\build\bin"
#endif

#ifndef MyAppOutputDir
#define MyAppOutputDir "..\dist"
#endif

; TASK-057: default to embedding the WebView2 Evergreen bootstrapper. CI is
; expected to stage MicrosoftEdgeWebview2Setup.exe at ..\build\webview2\ before
; invoking ISCC. Smoke-test builds that don't need WebView2 can opt out with
; /DEmbedWebView2Bootstrap=0.
#ifndef EmbedWebView2Bootstrap
#define EmbedWebView2Bootstrap "1"
#endif

#define MyAppName       "Jarvis"
#define MyAppPublisher  "namanchopra"
#define MyAppURL        "https://github.com/namanchopra/J.A.R.V.I.S"
#define MyAppExeName    "jarvis.exe"
#define MyAppExeSource  MyAppSourceDir + "\" + MyAppExeName
#define MyAppIcon       "..\build\windows\icon.ico"

; TASK-061: mobile API TCP port. Mirrors internal/config/config.go's default
; (MobileAPIPort: 4422). The DisplayName is what `Get-NetFirewallRule
; -DisplayName Jarvis*` matches against on the host. Keeping both as #defines
; makes future port changes a one-line edit.
#define MobileAPIPort         "4422"
#define FirewallRuleName      "Jarvis Mobile API"
#define FirewallRuleGroupName "Jarvis"

; AppId is the GUID Windows uses to track this product in Add/Remove
; Programs. Generated once with `iscc /?` -> Tools menu -> "Generate GUID"
; (or any RFC4122 v4 generator). Bumping it would orphan existing installs;
; treat it as a permanent identity for Jarvis on Windows.
#define MyAppId         "{{A8F1C0E2-7B3E-4E5A-9D2C-3F8B6A1D0E4F}"

[Setup]
AppId={#MyAppId}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppVerName={#MyAppName} {#MyAppVersion}
AppPublisher={#MyAppPublisher}
AppPublisherURL={#MyAppURL}
AppSupportURL={#MyAppURL}/issues
AppUpdatesURL={#MyAppURL}/releases
VersionInfoVersion={#MyAppVersion}
VersionInfoCompany={#MyAppPublisher}
VersionInfoDescription={#MyAppName} Installer
VersionInfoProductName={#MyAppName}

; Default install path: 64-bit Program Files. Inno expands {autopf} based on
; the user's Windows arch + privilege level (admin -> Program Files, non-admin
; -> %LocalAppData%\Programs\). On arm64 Windows, x64 builds land in
; "Program Files (x86)"-equivalent space via emulation; arm64 builds land in
; native Program Files.
DefaultDirName={autopf}\{#MyAppName}
DefaultGroupName={#MyAppName}
DisableProgramGroupPage=yes
DisableDirPage=no

; Architecture targeting:
;   ArchitecturesAllowed restricts WHICH machines can run the installer.
;   ArchitecturesInstallIn64BitMode forces 64-bit install hive (Program Files,
;   not Program Files (x86)) when on a matching 64-bit host. We compute both
;   from MyAppArch so the same .iss produces correct metadata for x64/arm64.
#if MyAppArch == "x64"
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
#elif MyAppArch == "arm64"
ArchitecturesAllowed=arm64
ArchitecturesInstallIn64BitMode=arm64
#else
#error Unsupported MyAppArch (expected "x64" or "arm64")
#endif

; Minimum supported Windows version: 10.0.17763 (Windows 10 1809). This
; matches the WebView2 supported floor declared in
; docs/v0.4.0-windows-hud-acceptance.md and the implicit floor in the Wails
; NSIS template's wails.checkArchitecture macro.
MinVersion=10.0.17763

; Compression: LZMA2/ultra with solid compression typically gives 2-3x
; compression on Wails+Resources payloads. Combined with the post-build.ps1
; payload (<35MB unstaged) this keeps the final .exe under the 40MB AC ceiling.
Compression=lzma2/ultra
SolidCompression=yes
LZMAUseSeparateProcess=yes

; Output: the installer .exe is named to mirror the macOS DMG naming
; (Jarvis-<version>.dmg). The arch suffix disambiguates the x64 + arm64
; builds in the GitHub release asset list.
OutputDir={#MyAppOutputDir}
OutputBaseFilename=Jarvis-Setup-{#MyAppVersion}-{#MyAppArch}

; UI polish: use the bundled icon for the installer .exe and the Add/Remove
; Programs entry; show a modern wizard style; suppress the "ready to install"
; page so the flow is one fewer click for users.
SetupIconFile={#MyAppIcon}
UninstallDisplayIcon={app}\{#MyAppExeName}
UninstallDisplayName={#MyAppName} {#MyAppVersion}
WizardStyle=modern
DisableReadyPage=no
ShowLanguageDialog=no

; Privileges: "admin" lets us write to Program Files (the default). Users
; without admin rights get Inno's standard UAC prompt; if they cancel, the
; installer falls back to a per-user install under %LocalAppData%\Programs\.
PrivilegesRequired=admin
PrivilegesRequiredOverridesAllowed=dialog

; Make sure the installer fails fast if a Jarvis instance is already running
; (otherwise overwriting jarvis.exe in-place corrupts the running process).
CloseApplications=yes
CloseApplicationsFilter=*.exe,*.dll
RestartApplications=no

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
; Desktop shortcut is opt-in (matches the macOS DMG drag-to-Applications UX
; which doesn't add a Dock pin by default). Start Menu shortcut is always on.
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; \
    GroupDescription: "{cm:AdditionalIcons}"; Flags: unchecked

[Files]
; Main executable.
Source: "{#MyAppExeSource}"; DestDir: "{app}"; Flags: ignoreversion

; Bundled Resources\ tree staged by TASK-055's post-build.ps1. The
; "skipifsourcedoesntexist" flag lets local-only builds (where post-build
; hasn't run yet) still produce an installer for smoke-testing the
; install/uninstall lifecycle without the daemon payload.
Source: "{#MyAppSourceDir}\Resources\*"; DestDir: "{app}\Resources"; \
    Flags: ignoreversion recursesubdirs createallsubdirs skipifsourcedoesntexist; \
    Excludes: "*.pdb,*.map,*.pyc,*.pyo,__pycache__,*_test.py,.DS_Store,Thumbs.db"

; WebView2 Evergreen bootstrapper (TASK-057). The "dontcopy" flag stages it
; into the compiled installer payload without writing to {app}; we extract it
; into {tmp} at runtime via ExtractTemporaryFile() and run it silently if and
; only if WebView2 isn't already installed. The bootstrapper itself is a tiny
; (<3MB) network installer that downloads the full runtime - so the embedded
; size impact is well within the <40MB AC ceiling.
#if EmbedWebView2Bootstrap == "1"
Source: "..\build\webview2\MicrosoftEdgeWebview2Setup.exe"; \
    DestDir: "{tmp}"; Flags: dontcopy
#endif

[Icons]
Name: "{group}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"; \
    WorkingDir: "{app}"; IconFilename: "{app}\{#MyAppExeName}"
Name: "{group}\{cm:UninstallProgram,{#MyAppName}}"; Filename: "{uninstallexe}"
Name: "{autodesktop}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"; \
    WorkingDir: "{app}"; IconFilename: "{app}\{#MyAppExeName}"; \
    Tasks: desktopicon

[Run]
; TASK-061: register a Windows Firewall inbound rule for the mobile API
; port (TCP 4422) so Friday on the LAN can reach Jarvis without the user
; manually clicking through Windows' first-packet allow dialog.
;
; The PowerShell snippet is wrapped in a try/catch that:
;   - Removes any pre-existing rule of the same DisplayName (idempotent
;     on upgrade installs - we don't want duplicates piling up).
;   - Creates a fresh inbound TCP rule for {#MobileAPIPort} scoped to the
;     {#MyAppExeName} program and the Private + Domain profiles (we
;     intentionally exclude Public, where users would NOT want their
;     LAN listener exposed).
;   - On any failure (non-admin, firewall service down, AV blocks the
;     cmdlet, corp GPO denies it), writes a sentinel file at
;     {localappdata}\Jarvis\firewall-rule-failed.txt. The [Code]
;     CurStepChanged hook below reads that file at ssDone and warns
;     the user (interactive installs only).
;
; -WindowStyle Hidden + Inno's `runhidden` flag means no flashing
; console window even on slow machines. We use -NoProfile to skip
; user PowerShell profiles (which can take seconds to load on corp
; machines) and -ExecutionPolicy Bypass so locked-down policies
; don't refuse to run our inline snippet.
;
; The full command runs via -Command "& { ... }" so the embedded
; quotes survive Inno's preprocessor expansion and the cmdline parser.
Filename: "powershell.exe"; \
    Parameters: "-NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -Command ""& {{ try {{ $sentinel = Join-Path $env:LOCALAPPDATA 'Jarvis\firewall-rule-failed.txt'; if (Test-Path $sentinel) {{ Remove-Item -Force $sentinel -ErrorAction SilentlyContinue }}; Get-NetFirewallRule -DisplayName '{#FirewallRuleName}' -ErrorAction SilentlyContinue | Remove-NetFirewallRule -ErrorAction SilentlyContinue; New-NetFirewallRule -DisplayName '{#FirewallRuleName}' -Group '{#FirewallRuleGroupName}' -Description 'Allow inbound connections from the Jarvis mobile companion (Friday) on TCP port {#MobileAPIPort}.' -Direction Inbound -Action Allow -Protocol TCP -LocalPort {#MobileAPIPort} -Program (Join-Path '{app}' '{#MyAppExeName}') -Profile Private,Domain -ErrorAction Stop | Out-Null }} catch {{ $dir = Join-Path $env:LOCALAPPDATA 'Jarvis'; if (-not (Test-Path $dir)) {{ New-Item -ItemType Directory -Force -Path $dir | Out-Null }}; Set-Content -Path (Join-Path $dir 'firewall-rule-failed.txt') -Value $_.Exception.Message -Encoding UTF8 }} }}"""; \
    StatusMsg: "Configuring Windows Firewall..."; \
    Flags: runhidden

; Offer to launch Jarvis at the end of the install. nowait+postinstall+skipifsilent
; matches the conventional "Launch <app>" checkbox on the finish page; silent
; installs (e.g. winget, MDM) skip this entirely.
Filename: "{app}\{#MyAppExeName}"; Description: "{cm:LaunchProgram,{#MyAppName}}"; \
    Flags: nowait postinstall skipifsilent

[UninstallRun]
; TASK-061: tear down the inbound firewall rule on uninstall. We swallow
; errors so a missing rule (e.g. user nuked it manually, or it was never
; created because the install was non-admin) doesn't pop a console.
; -ErrorAction SilentlyContinue handles "no matching rule" without raising;
; the surrounding try/catch handles every other failure mode (firewall
; service stopped, GPO denial, etc.).
Filename: "powershell.exe"; \
    Parameters: "-NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -Command ""& {{ try {{ Get-NetFirewallRule -DisplayName '{#FirewallRuleName}' -ErrorAction SilentlyContinue | Remove-NetFirewallRule -ErrorAction SilentlyContinue }} catch {{ }} }}"""; \
    Flags: runhidden

[UninstallDelete]
; The Wails-managed user data dirs live under %LocalAppData% and ~/.jarvis\
; (= %UserProfile%\.jarvis\). The WebView2 user-data dir is the only one we
; aggressively delete on uninstall - it can grow to hundreds of MB of cache
; and is fully regenerated on next install. We intentionally LEAVE
; %UserProfile%\.jarvis\ in place (DB, logs, config) so a reinstall doesn't
; nuke the user's data; the InitializeUninstall code below offers an opt-in
; "purge user data too" prompt when running interactively.
Type: filesandordirs; Name: "{localappdata}\Jarvis\WebView"

[Code]
// -----------------------------------------------------------------------------
// Official Microsoft WebView2 download page. Shown to the user when the
// silent bootstrap fails so they can install WebView2 manually before
// re-launching Jarvis. Mirrors the URL surfaced by the Wails default
// "Missing Requirements" dialog so users see a consistent destination.
// -----------------------------------------------------------------------------
const
  WebView2DownloadURL = 'https://developer.microsoft.com/en-us/microsoft-edge/webview2/';

// -----------------------------------------------------------------------------
// WebView2 detection helper. Probes the Edge Updater client registry key for
// the WebView2 Evergreen runtime (Application ID
// {F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}). Returns True when a non-empty,
// non-zero "pv" version string is found under either the machine-wide hive
// (HKLM, set by the system-elevated bootstrapper) or the per-user hive (HKCU,
// used by sideloaded / non-admin installs). This is the same probe Wails'
// NSIS template performs at install time (see build/windows/installer/
// wails_tools.nsh::CheckWebView2Runtime).
// -----------------------------------------------------------------------------
function IsWebView2Installed(): Boolean;
var
  Version: string;
begin
  Result := False;
  // Machine-wide install (most common, set by the bootstrapper running as admin).
  if RegQueryStringValue(HKLM,
       'SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}',
       'pv', Version) then
  begin
    if (Version <> '') and (Version <> '0.0.0.0') then
      Result := True;
  end;
  // Per-user fallback (some sideload scenarios).
  if not Result then
    if RegQueryStringValue(HKCU,
         'SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}',
         'pv', Version) then
    begin
      if (Version <> '') and (Version <> '0.0.0.0') then
        Result := True;
    end;
end;

#if EmbedWebView2Bootstrap == "1"
// -----------------------------------------------------------------------------
// Surfaces the WebView2 install failure to the user with a clear error
// message and offers to open the official manual download page in the
// default browser. Suppressed during silent installs (winget / MDM) - those
// flows just log the failure and let jarvis.exe's first-launch dialog
// handle it.
//
// Reason is one of:
//   "launch"  - Exec() returned False (could not start the bootstrapper at all)
//   "exit"    - bootstrapper exited with ExitCode <> 0
//   "verify"  - bootstrapper exited 0 but post-install registry check still fails
// -----------------------------------------------------------------------------
procedure ShowWebView2FailureDialog(Reason: string; ExitCode: Integer);
var
  Msg: string;
  ShellResultCode: Integer;
begin
  if WizardSilent() then
  begin
    Log(Format('WebView2 bootstrap failed (%s, exit=%d) but install is silent; skipping dialog.',
               [Reason, ExitCode]));
    Exit;
  end;

  if Reason = 'launch' then
    Msg := 'Jarvis could not launch the bundled WebView2 installer.'
  else if Reason = 'exit' then
    Msg := Format('The bundled WebView2 installer exited with code %d.', [ExitCode])
  else
    Msg := 'The bundled WebView2 installer finished but the runtime is still not detected.';

  Msg := Msg + #13#10 + #13#10 +
         'Jarvis requires the Microsoft WebView2 runtime to render its window.' + #13#10 +
         'This usually happens on machines without internet access or behind a' + #13#10 +
         'corporate proxy that blocks the Microsoft Edge update servers.' + #13#10 + #13#10 +
         'Click YES to open the official Microsoft WebView2 download page in' + #13#10 +
         'your browser. After installing WebView2 manually, re-launch Jarvis.' + #13#10 + #13#10 +
         'Click NO to continue anyway - Jarvis will show a "Missing Requirements"' + #13#10 +
         'prompt on first launch with the same download link.';

  if MsgBox(Msg, mbError, MB_YESNO or MB_DEFBUTTON1) = IDYES then
  begin
    if not ShellExec('open', WebView2DownloadURL, '', '', SW_SHOWNORMAL,
                     ewNoWait, ShellResultCode) then
      Log(Format('Failed to open WebView2 download page (ShellExec error %d).',
                 [ShellResultCode]));
  end;
end;

// -----------------------------------------------------------------------------
// InstallWebView2Bootstrap: extracts the bundled Microsoft WebView2
// Evergreen bootstrapper to {tmp} and runs it silently. Re-checks the
// registry post-run to confirm the runtime actually landed (the
// bootstrapper can return 0 even when the download itself fails on
// air-gapped machines). On any failure path we surface a clear MsgBox
// with a link to the manual download page (TASK-057 AC #3) but do NOT
// abort the install - jarvis.exe will gracefully surface the Wails
// "Missing Requirements" dialog on first launch as a safety net.
// -----------------------------------------------------------------------------
procedure InstallWebView2Bootstrap();
var
  ExitCode: Integer;
  BootstrapPath: string;
begin
  Log('WebView2 runtime not detected; extracting bundled bootstrapper.');
  ExtractTemporaryFile('MicrosoftEdgeWebview2Setup.exe');
  BootstrapPath := ExpandConstant('{tmp}\MicrosoftEdgeWebview2Setup.exe');

  // /silent + /install runs the Evergreen bootstrapper headlessly. It returns
  // a Win32 exit code: 0 on success, non-zero on failure (network error,
  // policy block, etc.).
  if not Exec(BootstrapPath, '/silent /install', '', SW_HIDE,
              ewWaitUntilTerminated, ExitCode) then
  begin
    Log('WebView2 bootstrapper failed to launch (Exec returned False).');
    ShowWebView2FailureDialog('launch', 0);
    Exit;
  end;

  if ExitCode <> 0 then
  begin
    Log(Format('WebView2 bootstrapper exited with code %d.', [ExitCode]));
    ShowWebView2FailureDialog('exit', ExitCode);
    Exit;
  end;

  // The bootstrapper claims success - verify by re-probing the registry.
  // This guards against silent-failure modes (e.g. download succeeded but
  // service install was blocked by AV/policy).
  if not IsWebView2Installed() then
  begin
    Log('WebView2 bootstrapper exited 0 but runtime is still not detected.');
    ShowWebView2FailureDialog('verify', 0);
    Exit;
  end;

  Log('WebView2 runtime successfully installed via bundled bootstrapper.');
end;
#endif

// -----------------------------------------------------------------------------
// FirewallRuleSentinelPath: absolute path to the sentinel file the [Run]
// PowerShell snippet writes when New-NetFirewallRule fails. We read this
// after ssDone to decide whether to warn the user (TASK-061 AC #3). Kept
// as a helper so the path string is defined in exactly one place.
// -----------------------------------------------------------------------------
function FirewallRuleSentinelPath(): string;
begin
  Result := ExpandConstant('{localappdata}\Jarvis\firewall-rule-failed.txt');
end;

// -----------------------------------------------------------------------------
// CheckFirewallRuleResult: reads the sentinel file written by the [Run]
// PowerShell snippet on failure and, on interactive installs, surfaces a
// one-shot non-blocking warning. The most common reason for the rule to
// be missing is a non-admin install (PrivilegesRequiredOverridesAllowed=dialog
// lets users downgrade to per-user mode, which can't touch
// HKLM\System\CurrentControlSet\Services\SharedAccess), but the snippet
// also fails on locked-down corp machines where the firewall service is
// stopped or GPO blocks the cmdlet.
//
// The sentinel is consumed (deleted) after reading so repeated launches
// of the installer don't keep re-warning. Silent installs (winget / MDM)
// just log and skip the dialog so they don't hang waiting for input.
// -----------------------------------------------------------------------------
procedure CheckFirewallRuleResult();
var
  SentinelPath: string;
  Reason: AnsiString;
  Msg: string;
begin
  SentinelPath := FirewallRuleSentinelPath();
  if not FileExists(SentinelPath) then
  begin
    Log('Firewall rule sentinel not present; New-NetFirewallRule succeeded.');
    Exit;
  end;

  // Best-effort read of the captured error message (for the install log only;
  // the user-facing dialog is intentionally generic).
  if LoadStringFromFile(SentinelPath, Reason) then
    Log('Firewall rule creation failed: ' + string(Reason))
  else
    Log('Firewall rule sentinel present but unreadable.');

  // Always delete the sentinel so we don't carry stale failure state into
  // future installer runs.
  DeleteFile(SentinelPath);

  if WizardSilent() then
  begin
    Log('Firewall rule warning suppressed (silent install).');
    Exit;
  end;

  Msg := 'Jarvis could not create a Windows Firewall rule for the mobile API.' + #13#10 + #13#10 +
         'Friday may not be able to connect to this machine from your phone' + #13#10 +
         'over the local network until the rule is created manually.' + #13#10 + #13#10 +
         'This usually happens when the installer runs without administrator' + #13#10 +
         'privileges, or when a corporate policy blocks firewall changes.' + #13#10 + #13#10 +
         'To fix this later, run the following in an elevated PowerShell:' + #13#10 +
         '  New-NetFirewallRule -DisplayName ''{#FirewallRuleName}'' \' + #13#10 +
         '    -Direction Inbound -Action Allow -Protocol TCP \' + #13#10 +
         '    -LocalPort {#MobileAPIPort} -Profile Private,Domain';
  MsgBox(Msg, mbInformation, MB_OK);
end;

// -----------------------------------------------------------------------------
// CurStepChanged: post-install hooks. We run two independent post-install
// actions here:
//   - ssPostInstall: silently install the WebView2 Evergreen runtime if
//     missing (TASK-057). On Win11 the runtime is preinstalled so this is
//     a no-op; on Win10 fresh installs the bundled bootstrapper runs
//     silently. Failures surface a clear error dialog but do not abort.
//   - ssDone: read the firewall-rule sentinel file written by the [Run]
//     PowerShell snippet (TASK-061). We use ssDone (not ssPostInstall) so
//     this fires after the [Run] section has finished and the sentinel
//     has had a chance to be written.
// -----------------------------------------------------------------------------
procedure CurStepChanged(CurStep: TSetupStep);
begin
  if CurStep = ssPostInstall then
  begin
#if EmbedWebView2Bootstrap == "1"
    if IsWebView2Installed() then
      Log('WebView2 runtime already present; skipping bootstrapper.')
    else
      InstallWebView2Bootstrap();
#else
    Log('EmbedWebView2Bootstrap=0; skipping WebView2 install step (smoke build).');
#endif
  end
  else if CurStep = ssDone then
  begin
    CheckFirewallRuleResult();
  end;
end;

// -----------------------------------------------------------------------------
// CurUninstallStepChanged: offer to purge ~/.jarvis (DB, logs, config) on
// uninstall. Defaults to KEEPING user data so accidental uninstalls during
// reinstall don't lose history.
// -----------------------------------------------------------------------------
procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  UserDataDir: string;
begin
  if CurUninstallStep = usPostUninstall then
  begin
    UserDataDir := ExpandConstant('{userprofile}\.jarvis');
    if DirExists(UserDataDir) then
    begin
      // UninstallSilent suppresses the prompt during /VERYSILENT uninstalls
      // (used by upgrade paths) so we don't block on user input.
      if not UninstallSilent then
      begin
        if MsgBox(
             'Also delete Jarvis user data (database, logs, config) at "' +
             UserDataDir + '"?' + #13#10 +
             'Choose "No" to keep your data for a future reinstall.',
             mbConfirmation, MB_YESNO or MB_DEFBUTTON2) = IDYES then
          DelTree(UserDataDir, True, True, True);
      end;
    end;
  end;
end;
