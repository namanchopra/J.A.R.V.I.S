//go:build windows

package cmux

import "os/exec"

func setDetached(cmd *exec.Cmd) {
	// No setsid equivalent on Windows; cmux helper invocations use the
	// default process attributes. The terminal-control feature itself is
	// Mac-only today (TASK-029 introduces a separate Windows Terminal
	// provider), so this is a no-op placeholder to keep the package
	// compiling on Windows for any callers that import cmux from
	// cross-platform code paths.
	_ = cmd
}
