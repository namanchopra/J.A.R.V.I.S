//go:build windows

package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// linkRepo creates a Windows directory junction point at linkPath pointing at
// target. We use junctions (mklink /J) rather than symbolic links (mklink /D)
// because:
//
//  1. Junctions can be created by any unprivileged user; directory symlinks
//     require either admin elevation or Developer Mode enabled. Asking every
//     Jarvis user to flip Developer Mode is an unacceptable onboarding step.
//  2. Junctions are transparent to Claude Code and other tools — they appear
//     as regular directories via the standard CreateFileW APIs.
//  3. os.RemoveAll on a junction removes the reparse point only, not the
//     target — so DeleteWorkspace stays safe (acceptance criterion #2).
//
// Junctions have one constraint vs. symlinks: the target must be an absolute
// path to an existing directory on a local (non-network) volume. We resolve
// the target to an absolute path and verify it exists as a directory before
// shelling out, so the failure-case acceptance criterion ("missing target
// fails cleanly") is satisfied with a clear error rather than an opaque
// cmd.exe non-zero exit.
//
// TASK-033: Windows-specific link backend for workspace.Create().
func linkRepo(target, linkPath string) error {
	target = strings.TrimSpace(target)
	linkPath = strings.TrimSpace(linkPath)
	if target == "" {
		return fmt.Errorf("linkRepo: target is required")
	}
	if linkPath == "" {
		return fmt.Errorf("linkRepo: linkPath is required")
	}

	// mklink /J requires an absolute target. The caller already feeds us
	// absolute paths (workspace.Create() iterates Workspace.RepoPaths which
	// are stored absolute) but we normalise defensively because Abs() also
	// canonicalises slashes which mklink is sensitive to.
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("linkRepo: resolving absolute target %q: %w", target, err)
	}
	absLink, err := filepath.Abs(linkPath)
	if err != nil {
		return fmt.Errorf("linkRepo: resolving absolute linkPath %q: %w", linkPath, err)
	}

	// Validate the target up front — `mklink /J` will fail with a generic
	// "Cannot create a file when that file already exists" or "The system
	// cannot find the path specified" otherwise. Returning a clean Go error
	// here gives the caller (workspace.Create) a useful message in slog.
	info, err := os.Stat(absTarget)
	if err != nil {
		return fmt.Errorf("linkRepo: target %q not accessible: %w", absTarget, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("linkRepo: target %q is not a directory", absTarget)
	}

	// Shell out to cmd's built-in mklink. There is no Win32 API exposed to
	// Go's stdlib for junctions; the documented alternative is the
	// FSCTL_SET_REPARSE_POINT IOCTL dance, which is ~100 LOC of CGO-free
	// syscall code and considerably riskier than calling the OS's own
	// junction helper. cmd.exe is present on every supported Windows SKU
	// (Win10 1607+ and Win11). We hide the console window so a wails dev
	// session does not flash a cmd shell on every workspace creation.
	cmd := exec.Command("cmd", "/c", "mklink", "/J", absLink, absTarget)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("linkRepo: mklink /J %q %q failed: %w (output: %s)",
			absLink, absTarget, err, strings.TrimSpace(string(out)))
	}
	return nil
}
