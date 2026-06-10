//go:build !windows

package workspace

import (
	"fmt"
	"os"
)

// linkRepo creates a POSIX symbolic link at linkPath pointing at target on
// macOS / Linux. This is the historical workspace behaviour (TASK-033 split
// out the platform-specific implementations so Windows can use junction
// points instead of symlinks).
//
// We intentionally do NOT pre-create or pre-validate the target here — the
// workspace Create() caller already feeds us absolute repo paths discovered
// from the filesystem, and os.Symlink itself surfaces a clear error when the
// target genuinely does not exist on dereference (Lstat/EvalSymlinks). That
// keeps behaviour symmetric with the Windows junction path which validates
// up-front because `mklink /J` requires the target to exist.
func linkRepo(target, linkPath string) error {
	if err := os.Symlink(target, linkPath); err != nil {
		return fmt.Errorf("linkRepo: creating symlink %q -> %q: %w", linkPath, target, err)
	}
	return nil
}
