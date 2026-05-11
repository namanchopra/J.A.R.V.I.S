package paths

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

// MigrateLegacyHome performs a one-shot migration from ~/.awm/ to ~/.jarvis/
// for users upgrading from the pre-rename version. Idempotent — safe to call
// at every launch.
//
// Decision tree:
//  1. If ~/.jarvis/ already exists → no-op (migration already happened, or fresh install with new path).
//  2. Else if ~/.awm/ does NOT exist → no-op (true fresh install).
//  3. Else → copy ~/.awm/* into ~/.jarvis/, then create a symlink ~/.awm → ~/.jarvis
//     so legacy venv directories (jarvis-daemon-env, jarvis-stt-env, edge-tts-env,
//     dex-daemon-env) and any external tooling still pointing at ~/.awm continue
//     to work transparently for one release.
//
// Failure semantics:
//   - On copy failure, returns a wrapped error and leaves ~/.awm/ unchanged.
//     ~/.jarvis/ may be partially populated; next launch retries.
//   - On symlink failure (e.g., Windows, read-only volume), the migration still
//     succeeds (data is at the new path); we log a warning and continue.
func MigrateLegacyHome() error {
	newHome := JarvisHome()
	legacyHome := LegacyHome()

	// Case 1: target already exists → nothing to do.
	if info, err := os.Stat(newHome); err == nil && info.IsDir() {
		return nil
	}

	// Case 2: legacy source doesn't exist → fresh install, nothing to migrate.
	if _, err := os.Stat(legacyHome); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("MigrateLegacyHome: stat legacy home %q: %w", legacyHome, err)
	}

	slog.Info("paths: migrating legacy data dir", "from", legacyHome, "to", newHome)

	// Case 3: copy ~/.awm/ → ~/.jarvis/
	if err := copyDir(legacyHome, newHome); err != nil {
		// Try to clean up partial new dir on failure so the next launch retries cleanly.
		_ = os.RemoveAll(newHome)
		return fmt.Errorf("MigrateLegacyHome: copy %q → %q: %w", legacyHome, newHome, err)
	}

	// Symlink ~/.awm → ~/.jarvis so any external references to ~/.awm/ keep working.
	// Best-effort; failure is logged but not fatal.
	if err := replaceWithSymlink(legacyHome, newHome); err != nil {
		slog.Warn("paths: legacy symlink creation failed (data migrated, but ~/.awm references won't follow)", "err", err)
	} else {
		slog.Info("paths: legacy symlink created", "link", legacyHome, "target", newHome)
	}

	return nil
}

// copyDir recursively copies src into dst, preserving regular files, directories,
// and symlinks. Special files (devices, sockets) are skipped.
func copyDir(src, dst string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !srcInfo.IsDir() {
		return fmt.Errorf("copyDir: %q is not a directory", src)
	}

	if err := os.MkdirAll(dst, srcInfo.Mode().Perm()); err != nil {
		return err
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		// Skip the entry if it would create a recursive copy (i.e., dst is inside src).
		// Should not happen with ~/.awm and ~/.jarvis but defensive.
		if srcPath == dstPath {
			continue
		}

		switch {
		case entry.Type()&os.ModeSymlink != 0:
			// Preserve symlink as-is.
			target, err := os.Readlink(srcPath)
			if err != nil {
				return fmt.Errorf("readlink %q: %w", srcPath, err)
			}
			if err := os.Symlink(target, dstPath); err != nil {
				return fmt.Errorf("symlink %q → %q: %w", dstPath, target, err)
			}
		case entry.IsDir():
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
		case entry.Type().IsRegular():
			if err := copyFile(srcPath, dstPath); err != nil {
				return err
			}
		default:
			// Devices, pipes, sockets — skip.
			slog.Debug("paths: skipping non-regular file during migration", "path", srcPath, "mode", entry.Type())
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	srcInfo, err := srcFile.Stat()
	if err != nil {
		return err
	}

	dstFile, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, srcInfo.Mode().Perm())
	if err != nil {
		return err
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return err
	}
	return nil
}

// replaceWithSymlink removes legacyPath (which we've already copied from) and
// replaces it with a symlink pointing to newPath. The caller is expected to
// have just successfully copied legacyPath → newPath, so removing legacyPath
// loses no data.
func replaceWithSymlink(legacyPath, newPath string) error {
	if err := os.RemoveAll(legacyPath); err != nil {
		return fmt.Errorf("remove legacy path: %w", err)
	}
	if err := os.Symlink(newPath, legacyPath); err != nil {
		return fmt.Errorf("symlink: %w", err)
	}
	return nil
}
