//go:build !windows

// app_setup_spawn_other.go — platform half of the first-launch setup
// spawner for macOS (and other Unix). The Windows counterpart is
// app_setup_spawn_windows.go; both feed defaultSetupSpawner in app_setup.go.

package main

import (
	"context"
	"os/exec"
)

// setupScriptName is the platform-specific first-launch installer filename
// under <Resources>/setup/ (and scripts/setup/ for dev runs).
const setupScriptName = "install-daemon.sh"

// setupUvName is the bundled uv binary's filename under <Resources>/setup/.
const setupUvName = "uv"

// setupCommand builds the exec.Cmd that runs the first-launch installer:
// `bash <script> <uv> <daemon-src>`. The script's PHASE_* contract is
// emitted on stderr (see docs/setup-events.md).
func setupCommand(ctx context.Context, args setupSpawnArgs) *exec.Cmd {
	return exec.CommandContext(ctx, "bash", args.ScriptPath, args.UvPath, args.DaemonSourcePath)
}
