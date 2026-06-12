//go:build windows

// app_setup_spawn_windows.go — platform half of the first-launch setup
// spawner for Windows. Before this file existed, app_setup.go spawned
// `bash install-daemon.sh` unconditionally, which made the Windows
// first-launch setup dead code: install-daemon.ps1 (staged into
// Resources\setup\ by build/scripts/post-build.ps1) was never invoked.

package main

import (
	"context"
	"os/exec"
	"syscall"
)

// setupScriptName is the platform-specific first-launch installer filename
// under <Resources>\setup\ (and scripts\setup\ for dev runs).
const setupScriptName = "install-daemon.ps1"

// setupUvName is the bundled uv binary's filename under <Resources>\setup\.
const setupUvName = "uv.exe"

// setupCommand builds the exec.Cmd that runs the first-launch installer via
// Windows PowerShell 5.1 (powershell.exe — always present on Win10/11;
// install-daemon.ps1 is deliberately 5.1-compatible so we don't depend on
// pwsh being installed). -ExecutionPolicy Bypass scopes only this process —
// the machine policy is untouched, and the script ships inside the
// installer-signed app payload. The PHASE_* contract arrives on stderr with
// \r\n endings, which the parser in app_setup.go normalizes.
func setupCommand(ctx context.Context, args setupSpawnArgs) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "powershell.exe",
		"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-File", args.ScriptPath, args.UvPath, args.DaemonSourcePath,
	)
	// Suppress the transient console window a GUI-subsystem parent would
	// otherwise flash when spawning a console-subsystem child.
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd
}
