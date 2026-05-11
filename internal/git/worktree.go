package git

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// WorktreeInfo holds metadata about a git worktree managed by Jarvis.
type WorktreeInfo struct {
	Path      string    `json:"path"`
	Branch    string    `json:"branch"`
	RepoPath  string    `json:"repoPath"`
	CreatedAt time.Time `json:"createdAt"`
}

// worktreesBaseDir returns the path to ~/.awm/worktrees/, creating it if
// necessary.
func worktreesBaseDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("worktreesBaseDir: %w", err)
	}

	dir := filepath.Join(home, ".awm", "worktrees")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("worktreesBaseDir: mkdir %s: %w", dir, err)
	}
	return dir, nil
}

// CreateWorktree creates a git worktree for the given repo. The worktree is
// placed at ~/.awm/worktrees/<repo-basename>-<timestamp>/ and a new branch
// named awm/<repo-basename>-<timestamp> is created automatically.
func CreateWorktree(repoPath string) (*WorktreeInfo, error) {
	if !IsGitRepo(repoPath) {
		return nil, fmt.Errorf("CreateWorktree: path %q is not a git repository", repoPath)
	}

	absRepo, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, fmt.Errorf("CreateWorktree: resolve path: %w", err)
	}

	base, err := worktreesBaseDir()
	if err != nil {
		return nil, fmt.Errorf("CreateWorktree: %w", err)
	}

	now := time.Now()
	stamp := now.Format("20060102-150405")
	repoName := filepath.Base(absRepo)

	worktreePath := filepath.Join(base, fmt.Sprintf("%s-%s", repoName, stamp))
	branchName := fmt.Sprintf("github.com/namanchopra/jarvis/%s-%s", repoName, stamp)

	// git worktree add -b <branch> <path>
	_, err = runGit(absRepo, "worktree", "add", "-b", branchName, worktreePath)
	if err != nil {
		return nil, fmt.Errorf("CreateWorktree: %w", err)
	}

	return &WorktreeInfo{
		Path:      worktreePath,
		Branch:    branchName,
		RepoPath:  absRepo,
		CreatedAt: now,
	}, nil
}

// DeleteWorktree removes a git worktree and cleans up its directory. It first
// runs `git worktree remove` from the parent repo, then ensures the directory
// is gone.
func DeleteWorktree(worktreePath string) error {
	absPath, err := filepath.Abs(worktreePath)
	if err != nil {
		return fmt.Errorf("DeleteWorktree: resolve path: %w", err)
	}

	// Discover the parent repo so we can run `git worktree remove` from it.
	repoPath, err := resolveMainWorktree(absPath)
	if err != nil {
		return fmt.Errorf("DeleteWorktree: %w", err)
	}

	// Force-remove the worktree entry. --force handles unclean worktrees.
	_, err = runGit(repoPath, "worktree", "remove", "--force", absPath)
	if err != nil {
		return fmt.Errorf("DeleteWorktree: %w", err)
	}

	// Belt-and-suspenders: if the directory still exists, remove it.
	if _, statErr := os.Stat(absPath); statErr == nil {
		if removeErr := os.RemoveAll(absPath); removeErr != nil {
			return fmt.Errorf("DeleteWorktree: cleanup directory: %w", removeErr)
		}
	}

	return nil
}

// MergeWorktree merges the worktree's branch back into the target branch in
// the original (main) repository. It checks out the target branch, performs
// the merge, then checks out the original branch if different.
func MergeWorktree(worktreePath, targetBranch string) error {
	absPath, err := filepath.Abs(worktreePath)
	if err != nil {
		return fmt.Errorf("MergeWorktree: resolve path: %w", err)
	}

	// Resolve the main repo path.
	repoPath, err := resolveMainWorktree(absPath)
	if err != nil {
		return fmt.Errorf("MergeWorktree: %w", err)
	}

	// Determine the worktree's branch name.
	branch, err := runGit(absPath, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return fmt.Errorf("MergeWorktree: get worktree branch: %w", err)
	}
	branch = strings.TrimSpace(branch)

	// Remember the current branch in the main repo so we can restore it.
	origBranch, err := runGit(repoPath, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return fmt.Errorf("MergeWorktree: get original branch: %w", err)
	}
	origBranch = strings.TrimSpace(origBranch)

	// Checkout the target branch in the main repo.
	_, err = runGit(repoPath, "checkout", targetBranch)
	if err != nil {
		return fmt.Errorf("MergeWorktree: checkout %s: %w", targetBranch, err)
	}

	// Merge the worktree branch.
	_, mergeErr := runGit(repoPath, "merge", branch)

	// If the original branch differs from the target, restore it regardless
	// of whether the merge succeeded — but only if they differ.
	if origBranch != targetBranch {
		if _, restoreErr := runGit(repoPath, "checkout", origBranch); restoreErr != nil {
			// If we also had a merge error, report both.
			if mergeErr != nil {
				return fmt.Errorf("MergeWorktree: merge failed: %w (also failed to restore branch %s: %v)", mergeErr, origBranch, restoreErr)
			}
			return fmt.Errorf("MergeWorktree: restore branch %s: %w", origBranch, restoreErr)
		}
	}

	if mergeErr != nil {
		return fmt.Errorf("MergeWorktree: merge %s into %s: %w", branch, targetBranch, mergeErr)
	}

	return nil
}

// ListWorktrees returns all git worktrees for the repository at repoPath by
// parsing the output of `git worktree list --porcelain`.
func ListWorktrees(repoPath string) ([]WorktreeInfo, error) {
	if !IsGitRepo(repoPath) {
		return nil, fmt.Errorf("ListWorktrees: path %q is not a git repository", repoPath)
	}

	absRepo, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, fmt.Errorf("ListWorktrees: resolve path: %w", err)
	}

	out, err := runGit(absRepo, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("ListWorktrees: %w", err)
	}

	return parseWorktreeList(out, absRepo), nil
}

// parseWorktreeList parses the porcelain output of `git worktree list`.
//
// The format is blocks separated by blank lines. Each block contains lines
// like:
//
//	worktree /path/to/worktree
//	HEAD <sha>
//	branch refs/heads/branch-name
//
// A bare or detached entry may have "bare" or "detached" instead of "branch".
func parseWorktreeList(raw, repoPath string) []WorktreeInfo {
	var results []WorktreeInfo

	blocks := strings.Split(strings.TrimSpace(raw), "\n\n")
	for _, block := range blocks {
		if strings.TrimSpace(block) == "" {
			continue
		}

		var info WorktreeInfo
		info.RepoPath = repoPath

		for _, line := range strings.Split(block, "\n") {
			line = strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(line, "worktree "):
				info.Path = strings.TrimPrefix(line, "worktree ")
			case strings.HasPrefix(line, "branch "):
				ref := strings.TrimPrefix(line, "branch ")
				// Strip refs/heads/ prefix to get the short branch name.
				info.Branch = strings.TrimPrefix(ref, "refs/heads/")
			}
		}

		// Try to read the directory's modification time as a proxy for
		// creation time. Falls back to zero time if stat fails.
		if fi, statErr := os.Stat(info.Path); statErr == nil {
			info.CreatedAt = fi.ModTime()
		}

		if info.Path != "" {
			results = append(results, info)
		}
	}

	return results
}

// CleanupOrphanedWorktrees scans ~/.awm/worktrees/ and removes any
// directories that no longer correspond to a valid git worktree entry. A
// directory is considered orphaned if `git rev-parse --git-dir` fails inside
// it.
func CleanupOrphanedWorktrees() error {
	base, err := worktreesBaseDir()
	if err != nil {
		return fmt.Errorf("CleanupOrphanedWorktrees: %w", err)
	}

	entries, err := os.ReadDir(base)
	if err != nil {
		return fmt.Errorf("CleanupOrphanedWorktrees: read dir: %w", err)
	}

	var firstErr error
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		dir := filepath.Join(base, entry.Name())

		// A valid worktree has a .git file (not directory) that points back to
		// the parent repo's worktree admin area. If rev-parse fails, the
		// worktree link is broken.
		if _, gitErr := runGit(dir, "rev-parse", "--git-dir"); gitErr != nil {
			if removeErr := os.RemoveAll(dir); removeErr != nil && firstErr == nil {
				firstErr = fmt.Errorf("CleanupOrphanedWorktrees: remove %s: %w", dir, removeErr)
			}
		}
	}

	return firstErr
}

// resolveMainWorktree finds the main repository path from a worktree path by
// reading the git-dir and following it back to the main .git directory.
func resolveMainWorktree(worktreePath string) (string, error) {
	out, err := runGit(worktreePath, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", fmt.Errorf("resolveMainWorktree: %w", err)
	}

	commonDir := strings.TrimSpace(out)

	// The common dir may be relative to the worktree path.
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(worktreePath, commonDir)
	}

	// Clean up the path — the common dir is the .git directory itself (or the
	// bare repo root). For a standard repo layout, the repo root is the parent
	// of .git.
	commonDir = filepath.Clean(commonDir)

	if filepath.Base(commonDir) == ".git" {
		return filepath.Dir(commonDir), nil
	}

	// For bare repos or unusual layouts, return the common dir directly.
	return commonDir, nil
}
