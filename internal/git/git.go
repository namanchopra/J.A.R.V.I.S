package git

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// RepoInfo holds git repository metadata extracted by running git commands.
type RepoInfo struct {
	Branch        string   `json:"branch"`
	CommitCount   int      `json:"commitCount"`
	FilesChanged  int      `json:"filesChanged"`
	Insertions    int      `json:"insertions"`
	Deletions     int      `json:"deletions"`
	ChangedFiles  []string `json:"changedFiles"`
	LastCommitMsg string   `json:"lastCommitMsg"`
	LastCommitAge string   `json:"lastCommitAge"`
	HasUnpushed   bool     `json:"hasUnpushed"`
	IsClean       bool     `json:"isClean"`
	RemoteURL     string   `json:"remoteUrl"`
	PRNumber      int      `json:"prNumber"`
}

// commandTimeout is the maximum duration for a single git command.
const commandTimeout = 5 * time.Second

// GetRepoInfo runs git commands against repoPath and assembles a RepoInfo.
// Individual command failures are handled gracefully: the corresponding field
// is left at its zero value rather than aborting the entire call. An error is
// returned only when the path is not a git repository at all.
func GetRepoInfo(repoPath string) (RepoInfo, error) {
	if !IsGitRepo(repoPath) {
		return RepoInfo{}, fmt.Errorf("path %q is not a git repository", repoPath)
	}

	var info RepoInfo

	// Ensure changedFiles is never nil so it serialises as [] not null.
	info.ChangedFiles = []string{}

	// --- branch ---
	if out, err := runGit(repoPath, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		info.Branch = strings.TrimSpace(out)
	}

	// --- commitCount: commits ahead of main or master ---
	info.CommitCount = commitsAhead(repoPath, info.Branch)

	// --- diff --stat HEAD: filesChanged, insertions, deletions ---
	parseDiffStat(repoPath, &info)

	// --- diff --name-only HEAD: changedFiles ---
	parseChangedFiles(repoPath, &info)

	// --- last commit message ---
	if out, err := runGit(repoPath, "log", "-1", "--pretty=format:%s"); err == nil {
		msg := strings.TrimSpace(out)
		if len(msg) > 100 {
			msg = msg[:100]
		}
		info.LastCommitMsg = msg
	}

	// --- last commit age (relative) ---
	if out, err := runGit(repoPath, "log", "-1", "--pretty=format:%cr"); err == nil {
		info.LastCommitAge = strings.TrimSpace(out)
	}

	// --- working tree clean ---
	if out, err := runGit(repoPath, "status", "--porcelain"); err == nil {
		info.IsClean = strings.TrimSpace(out) == ""
	}

	// --- has unpushed commits ---
	if out, err := runGit(repoPath, "log", "@{u}..HEAD", "--oneline"); err == nil {
		info.HasUnpushed = strings.TrimSpace(out) != ""
	}

	// --- remote URL ---
	if out, err := runGit(repoPath, "remote", "get-url", "origin"); err == nil {
		info.RemoteURL = strings.TrimSpace(out)
	}

	// --- PR number (best effort, parse from branch name like "pr/123" or "pull/123") ---
	info.PRNumber = parsePRNumber(info.Branch)

	return info, nil
}

// IsGitRepo checks whether path is inside a git repository.
func IsGitRepo(path string) bool {
	// Fast check: look for .git directory or file at the given path.
	gitPath := filepath.Join(path, ".git")
	if fi, err := os.Stat(gitPath); err == nil {
		// .git can be a directory (normal) or a file (worktree/submodule).
		_ = fi
		return true
	}

	// Fallback: ask git itself (handles nested worktrees, etc.).
	_, err := runGit(path, "rev-parse", "--git-dir")
	return err == nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// runGit executes a git command with a 5-second timeout, working directory set
// to repoPath, and returns the combined stdout. Stderr is discarded on success
// and included in the error on failure.
// RunGitCommand executes a git command in the given directory and returns stdout.
func RunGitCommand(repoPath string, args ...string) (string, error) {
	return runGit(repoPath, args...)
}

func runGit(repoPath string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repoPath

	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

// commitsAhead returns the number of commits on the current branch ahead of
// main or master. Returns 0 if neither base branch exists or an error occurs.
func commitsAhead(repoPath, branch string) int {
	// Skip if we are on main/master itself.
	if branch == "main" || branch == "master" {
		return 0
	}

	// Try main first, then master.
	for _, base := range []string{"main", "master"} {
		out, err := runGit(repoPath, "log", base+"..HEAD", "--oneline")
		if err != nil {
			continue
		}
		trimmed := strings.TrimSpace(out)
		if trimmed == "" {
			return 0
		}
		return len(strings.Split(trimmed, "\n"))
	}
	return 0
}

// parseDiffStat extracts filesChanged, insertions, and deletions from
// `git diff --stat HEAD`.
func parseDiffStat(repoPath string, info *RepoInfo) {
	out, err := runGit(repoPath, "diff", "--stat", "HEAD")
	if err != nil {
		return
	}

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) == 0 {
		return
	}

	// The summary line is the last line and looks like:
	//   " 3 files changed, 10 insertions(+), 2 deletions(-)"
	summary := lines[len(lines)-1]

	// files changed
	if idx := strings.Index(summary, " file"); idx > 0 {
		parts := strings.TrimSpace(summary[:idx])
		// parts may have leading spaces; take the last token.
		tokens := strings.Fields(parts)
		if len(tokens) > 0 {
			if n, err := strconv.Atoi(tokens[len(tokens)-1]); err == nil {
				info.FilesChanged = n
			}
		}
	}

	// insertions
	if idx := strings.Index(summary, " insertion"); idx > 0 {
		before := summary[:idx]
		tokens := strings.Fields(before)
		if len(tokens) > 0 {
			if n, err := strconv.Atoi(tokens[len(tokens)-1]); err == nil {
				info.Insertions = n
			}
		}
	}

	// deletions
	if idx := strings.Index(summary, " deletion"); idx > 0 {
		before := summary[:idx]
		tokens := strings.Fields(before)
		if len(tokens) > 0 {
			if n, err := strconv.Atoi(tokens[len(tokens)-1]); err == nil {
				info.Deletions = n
			}
		}
	}
}

// parseChangedFiles populates info.ChangedFiles from `git diff --name-only HEAD`,
// capping the list at 50 entries.
func parseChangedFiles(repoPath string, info *RepoInfo) {
	out, err := runGit(repoPath, "diff", "--name-only", "HEAD")
	if err != nil {
		return
	}

	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		return
	}

	files := strings.Split(trimmed, "\n")
	const maxFiles = 50
	if len(files) > maxFiles {
		files = files[:maxFiles]
	}
	info.ChangedFiles = files
}

// parsePRNumber attempts to extract a PR number from a branch name. It
// recognises patterns like "pr/123", "pull/123", and "feature/pr-123". Returns
// 0 if no number is found.
func parsePRNumber(branch string) int {
	lower := strings.ToLower(branch)

	// Look for "pr/NNN" or "pull/NNN" patterns.
	for _, prefix := range []string{"pr/", "pull/"} {
		if idx := strings.Index(lower, prefix); idx >= 0 {
			numStr := lower[idx+len(prefix):]
			// Take only leading digits.
			numStr = leadingDigits(numStr)
			if n, err := strconv.Atoi(numStr); err == nil && n > 0 {
				return n
			}
		}
	}

	// Look for "pr-NNN" pattern.
	if idx := strings.Index(lower, "pr-"); idx >= 0 {
		numStr := leadingDigits(lower[idx+3:])
		if n, err := strconv.Atoi(numStr); err == nil && n > 0 {
			return n
		}
	}

	return 0
}

// leadingDigits returns the prefix of s that consists of ASCII digits.
func leadingDigits(s string) string {
	for i, c := range s {
		if c < '0' || c > '9' {
			return s[:i]
		}
	}
	return s
}

// ---------------------------------------------------------------------------
// Diff types
// ---------------------------------------------------------------------------

// DiffResult holds the parsed output of a unified diff.
type DiffResult struct {
	Files []FileDiff `json:"files"`
	Stats DiffStats  `json:"stats"`
}

// FileDiff represents the diff for a single file.
type FileDiff struct {
	Path    string     `json:"path"`
	OldPath string     `json:"oldPath"`
	Status  string     `json:"status"` // "added", "modified", "deleted", "renamed"
	Hunks   []DiffHunk `json:"hunks"`
	Binary  bool       `json:"binary"`
}

// DiffHunk represents a single hunk within a file diff.
type DiffHunk struct {
	OldStart int        `json:"oldStart"`
	OldCount int        `json:"oldCount"`
	NewStart int        `json:"newStart"`
	NewCount int        `json:"newCount"`
	Header   string     `json:"header"`
	Lines    []DiffLine `json:"lines"`
}

// DiffLine represents a single line within a diff hunk.
type DiffLine struct {
	Type    string `json:"type"` // "add", "delete", "context"
	Content string `json:"content"`
	OldNum  int    `json:"oldNum"`
	NewNum  int    `json:"newNum"`
}

// DiffStats holds aggregate statistics for a diff.
type DiffStats struct {
	FilesChanged int `json:"filesChanged"`
	Insertions   int `json:"insertions"`
	Deletions    int `json:"deletions"`
}

// hunkHeaderRe matches unified diff hunk headers like "@@ -1,5 +1,7 @@" or
// "@@ -1,5 +1,7 @@ optional section heading".
var hunkHeaderRe = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@(.*)$`)

// ---------------------------------------------------------------------------
// Diff functions
// ---------------------------------------------------------------------------

// GetDiff runs `git diff --no-color -U3 HEAD` against repoPath and parses the
// unified diff output into a structured DiffResult.
func GetDiff(repoPath string) (DiffResult, error) {
	if !IsGitRepo(repoPath) {
		return DiffResult{}, fmt.Errorf("path %q is not a git repository", repoPath)
	}

	out, err := runGit(repoPath, "diff", "--no-color", "-U3", "HEAD")
	if err != nil {
		return DiffResult{}, fmt.Errorf("GetDiff: %w", err)
	}

	return parseDiffOutput(out), nil
}

// GetCumulativeDiff returns the full diff from baseCommit to the current
// working tree (committed + staged + unstaged changes). It runs
// `git diff <baseCommit> --no-color -U3` which compares the base commit
// against the working directory, capturing everything in one pass.
//
// If baseCommit is empty, it falls back to `git diff HEAD` (unstaged only).
func GetCumulativeDiff(repoPath, baseCommit string) (DiffResult, error) {
	if !IsGitRepo(repoPath) {
		return DiffResult{}, fmt.Errorf("path %q is not a git repository", repoPath)
	}

	var args []string
	if baseCommit == "" {
		// No base commit — fall back to diff against HEAD.
		args = []string{"diff", "--no-color", "-U3", "HEAD"}
	} else {
		// Diff from base commit to working tree (includes committed, staged, and unstaged).
		args = []string{"diff", "--no-color", "-U3", baseCommit}
	}

	out, err := runGit(repoPath, args...)
	if err != nil {
		return DiffResult{}, fmt.Errorf("GetCumulativeDiff: %w", err)
	}

	return parseDiffOutput(out), nil
}

// FindCommitBefore returns the most recent commit hash at or before the given
// time. It runs `git log --before=<t> --format=%H -1`. Returns an empty string
// (and nil error) if no commit exists before the given time.
func FindCommitBefore(repoPath string, t time.Time) (string, error) {
	if !IsGitRepo(repoPath) {
		return "", fmt.Errorf("path %q is not a git repository", repoPath)
	}

	iso := t.UTC().Format(time.RFC3339)
	out, err := runGit(repoPath, "log", "--before="+iso, "--format=%H", "-1")
	if err != nil {
		// git log returns exit 0 even with no results, so an error here
		// likely means the repo has no commits at all.
		return "", nil
	}

	return strings.TrimSpace(out), nil
}

// GetStagedDiff runs `git diff --cached --no-color -U3` against repoPath and
// parses the unified diff output into a structured DiffResult.
func GetStagedDiff(repoPath string) (DiffResult, error) {
	if !IsGitRepo(repoPath) {
		return DiffResult{}, fmt.Errorf("path %q is not a git repository", repoPath)
	}

	out, err := runGit(repoPath, "diff", "--cached", "--no-color", "-U3")
	if err != nil {
		return DiffResult{}, fmt.Errorf("GetStagedDiff: %w", err)
	}

	return parseDiffOutput(out), nil
}

// parseDiffOutput parses a unified diff string into a DiffResult.
func parseDiffOutput(raw string) DiffResult {
	result := DiffResult{
		Files: []FileDiff{},
	}

	if strings.TrimSpace(raw) == "" {
		return result
	}

	lines := strings.Split(raw, "\n")
	var currentFile *FileDiff
	var currentHunk *DiffHunk
	var oldLineNum, newLineNum int

	for i := 0; i < len(lines); i++ {
		line := lines[i]

		// New file diff header: "diff --git a/X b/Y"
		if strings.HasPrefix(line, "diff --git ") {
			// Flush the current file if any.
			if currentFile != nil {
				if currentHunk != nil {
					currentFile.Hunks = append(currentFile.Hunks, *currentHunk)
					currentHunk = nil
				}
				result.Files = append(result.Files, *currentFile)
			}

			fd := FileDiff{
				Hunks: []DiffHunk{},
			}

			// Parse "diff --git a/X b/Y".
			parts := strings.SplitN(line, " b/", 2)
			if len(parts) == 2 {
				fd.Path = parts[1]
			}
			aParts := strings.SplitN(line, " a/", 2)
			if len(aParts) == 2 {
				oldPath := strings.SplitN(aParts[1], " b/", 2)
				if len(oldPath) > 0 {
					fd.OldPath = oldPath[0]
				}
			}

			fd.Status = "modified" // default; refined below
			currentFile = &fd
			currentHunk = nil
			continue
		}

		if currentFile == nil {
			continue
		}

		// Detect binary files.
		if strings.HasPrefix(line, "Binary files") {
			currentFile.Binary = true
			continue
		}

		// Detect new file.
		if strings.HasPrefix(line, "new file mode") {
			currentFile.Status = "added"
			continue
		}

		// Detect deleted file.
		if strings.HasPrefix(line, "deleted file mode") {
			currentFile.Status = "deleted"
			continue
		}

		// Detect renamed file.
		if strings.HasPrefix(line, "rename from ") {
			currentFile.Status = "renamed"
			currentFile.OldPath = strings.TrimPrefix(line, "rename from ")
			continue
		}
		if strings.HasPrefix(line, "rename to ") {
			currentFile.Path = strings.TrimPrefix(line, "rename to ")
			continue
		}

		// Skip --- and +++ header lines.
		if strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ") {
			continue
		}

		// Skip index/similarity/dissimilarity lines.
		if strings.HasPrefix(line, "index ") || strings.HasPrefix(line, "similarity ") || strings.HasPrefix(line, "dissimilarity ") {
			continue
		}

		// Hunk header.
		if matches := hunkHeaderRe.FindStringSubmatch(line); matches != nil {
			// Flush the previous hunk.
			if currentHunk != nil {
				currentFile.Hunks = append(currentFile.Hunks, *currentHunk)
			}

			oldStart, _ := strconv.Atoi(matches[1])
			oldCount := 1
			if matches[2] != "" {
				oldCount, _ = strconv.Atoi(matches[2])
			}
			newStart, _ := strconv.Atoi(matches[3])
			newCount := 1
			if matches[4] != "" {
				newCount, _ = strconv.Atoi(matches[4])
			}

			currentHunk = &DiffHunk{
				OldStart: oldStart,
				OldCount: oldCount,
				NewStart: newStart,
				NewCount: newCount,
				Header:   line,
				Lines:    []DiffLine{},
			}
			oldLineNum = oldStart
			newLineNum = newStart
			continue
		}

		// Content lines within a hunk.
		if currentHunk != nil {
			if strings.HasPrefix(line, "+") {
				currentHunk.Lines = append(currentHunk.Lines, DiffLine{
					Type:    "add",
					Content: line[1:],
					OldNum:  0,
					NewNum:  newLineNum,
				})
				newLineNum++
				result.Stats.Insertions++
			} else if strings.HasPrefix(line, "-") {
				currentHunk.Lines = append(currentHunk.Lines, DiffLine{
					Type:    "delete",
					Content: line[1:],
					OldNum:  oldLineNum,
					NewNum:  0,
				})
				oldLineNum++
				result.Stats.Deletions++
			} else if strings.HasPrefix(line, " ") {
				currentHunk.Lines = append(currentHunk.Lines, DiffLine{
					Type:    "context",
					Content: line[1:],
					OldNum:  oldLineNum,
					NewNum:  newLineNum,
				})
				oldLineNum++
				newLineNum++
			}
			// Lines starting with "\" (e.g., "\ No newline at end of file") are skipped.
		}
	}

	// Flush the last file/hunk.
	if currentFile != nil {
		if currentHunk != nil {
			currentFile.Hunks = append(currentFile.Hunks, *currentHunk)
		}
		result.Files = append(result.Files, *currentFile)
	}

	result.Stats.FilesChanged = len(result.Files)

	return result
}

// ---------------------------------------------------------------------------
// Git actions
// ---------------------------------------------------------------------------

// longCommandTimeout is used for operations that may take longer (e.g., push).
const longCommandTimeout = 30 * time.Second

// runGitLong executes a git command with a longer timeout (30s), suitable for
// network operations like push.
func runGitLong(repoPath string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), longCommandTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repoPath

	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

// StageAll stages all changes (tracked and untracked) via `git add -A`.
func StageAll(repoPath string) error {
	if !IsGitRepo(repoPath) {
		return fmt.Errorf("path %q is not a git repository", repoPath)
	}
	_, err := runGit(repoPath, "add", "-A")
	if err != nil {
		return fmt.Errorf("StageAll: %w", err)
	}
	return nil
}

// StageFiles stages specific files via `git add <files...>`.
func StageFiles(repoPath string, files []string) error {
	if !IsGitRepo(repoPath) {
		return fmt.Errorf("path %q is not a git repository", repoPath)
	}
	if len(files) == 0 {
		return fmt.Errorf("StageFiles: no files specified")
	}

	args := append([]string{"add", "--"}, files...)
	_, err := runGit(repoPath, args...)
	if err != nil {
		return fmt.Errorf("StageFiles: %w", err)
	}
	return nil
}

// Commit creates a commit with the given message via `git commit -m "<message>"`.
func Commit(repoPath, message string) error {
	if !IsGitRepo(repoPath) {
		return fmt.Errorf("path %q is not a git repository", repoPath)
	}
	_, err := runGit(repoPath, "commit", "-m", message)
	if err != nil {
		return fmt.Errorf("Commit: %w", err)
	}
	return nil
}

// Push pushes commits to the remote using a 30-second timeout.
func Push(repoPath string) error {
	if !IsGitRepo(repoPath) {
		return fmt.Errorf("path %q is not a git repository", repoPath)
	}
	_, err := runGitLong(repoPath, "push")
	if err != nil {
		return fmt.Errorf("Push: %w", err)
	}
	return nil
}

// CreateBranch creates and checks out a new branch via `git checkout -b <name>`.
func CreateBranch(repoPath, name string) error {
	if !IsGitRepo(repoPath) {
		return fmt.Errorf("path %q is not a git repository", repoPath)
	}
	_, err := runGit(repoPath, "checkout", "-b", name)
	if err != nil {
		return fmt.Errorf("CreateBranch: %w", err)
	}
	return nil
}

// GetPRCreationURL parses the remote URL and current branch, then constructs a
// PR creation URL for GitHub or GitLab.
//
// GitHub: https://github.com/<org>/<repo>/compare/<branch>?expand=1
// GitLab: https://gitlab.com/<org>/<repo>/-/merge_requests/new?merge_request[source_branch]=<branch>
func GetPRCreationURL(repoPath string) (string, error) {
	if !IsGitRepo(repoPath) {
		return "", fmt.Errorf("path %q is not a git repository", repoPath)
	}

	remoteOut, err := runGit(repoPath, "remote", "get-url", "origin")
	if err != nil {
		return "", fmt.Errorf("GetPRCreationURL: %w", err)
	}
	remoteURL := strings.TrimSpace(remoteOut)

	branchOut, err := runGit(repoPath, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", fmt.Errorf("GetPRCreationURL: %w", err)
	}
	branch := strings.TrimSpace(branchOut)

	host, orgRepo := parseRemoteURL(remoteURL)

	switch {
	case strings.Contains(host, "github"):
		return fmt.Sprintf("https://%s/%s/compare/%s?expand=1", host, orgRepo, url.PathEscape(branch)), nil
	case strings.Contains(host, "gitlab"):
		return fmt.Sprintf("https://%s/%s/-/merge_requests/new?merge_request[source_branch]=%s", host, orgRepo, url.QueryEscape(branch)), nil
	default:
		// Best effort: assume GitHub-like.
		return fmt.Sprintf("https://%s/%s/compare/%s?expand=1", host, orgRepo, url.PathEscape(branch)), nil
	}
}

// sshRemoteRe matches SSH remote URLs like "git@github.com:org/repo.git".
var sshRemoteRe = regexp.MustCompile(`^[\w.-]+@([\w.-]+):([\w./-]+?)(?:\.git)?$`)

// parseRemoteURL extracts the host and "org/repo" from an HTTPS or SSH git
// remote URL.
func parseRemoteURL(remote string) (host, orgRepo string) {
	// Try SSH format: git@github.com:org/repo.git
	if m := sshRemoteRe.FindStringSubmatch(remote); m != nil {
		return m[1], m[2]
	}

	// Try HTTPS format: https://github.com/org/repo.git
	if u, err := url.Parse(remote); err == nil && u.Host != "" {
		path := strings.TrimPrefix(u.Path, "/")
		path = strings.TrimSuffix(path, ".git")
		return u.Host, path
	}

	return "github.com", remote
}

// ---------------------------------------------------------------------------
// Stash operations
// ---------------------------------------------------------------------------

// StashEntry represents a single entry in the git stash list that was created
// by AWM (identified by the "awm-checkpoint:" prefix in the message).
type StashEntry struct {
	Index int    `json:"index"`
	Name  string `json:"name"`
	Date  string `json:"date"`
}

// stashEntryRe matches git stash list output lines like:
//
//	stash@{0}: On main: awm-checkpoint: my checkpoint name
var stashEntryRe = regexp.MustCompile(`^stash@\{(\d+)\}:\s+.*?:\s+awm-checkpoint:\s+(.+)$`)

// Stash creates a new stash entry with an AWM-prefixed message.
func Stash(repoPath, name string) error {
	if !IsGitRepo(repoPath) {
		return fmt.Errorf("path %q is not a git repository", repoPath)
	}
	if name == "" {
		return fmt.Errorf("Stash: name is required")
	}

	msg := "awm-checkpoint: " + name
	_, err := runGit(repoPath, "stash", "push", "-m", msg)
	if err != nil {
		return fmt.Errorf("Stash: %w", err)
	}
	return nil
}

// StashList returns all stash entries created by AWM (those whose message
// begins with "awm-checkpoint:"). Entries are returned in stash order (most
// recent first).
func StashList(repoPath string) ([]StashEntry, error) {
	if !IsGitRepo(repoPath) {
		return nil, fmt.Errorf("path %q is not a git repository", repoPath)
	}

	out, err := runGit(repoPath, "stash", "list", "--date=relative")
	if err != nil {
		// git stash list returns non-zero when there are no stashes in some
		// configurations; treat as empty.
		return []StashEntry{}, nil
	}

	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		return []StashEntry{}, nil
	}

	var entries []StashEntry
	for _, line := range strings.Split(trimmed, "\n") {
		m := stashEntryRe.FindStringSubmatch(line)
		if m == nil {
			continue // not an AWM checkpoint
		}
		idx, _ := strconv.Atoi(m[1])
		entries = append(entries, StashEntry{
			Index: idx,
			Name:  strings.TrimSpace(m[2]),
		})
	}

	if entries == nil {
		entries = []StashEntry{}
	}
	return entries, nil
}

// StashApply applies a stash entry by index without removing it from the stash
// list.
func StashApply(repoPath string, index int) error {
	if !IsGitRepo(repoPath) {
		return fmt.Errorf("path %q is not a git repository", repoPath)
	}

	ref := fmt.Sprintf("stash@{%d}", index)
	_, err := runGit(repoPath, "stash", "apply", ref)
	if err != nil {
		return fmt.Errorf("StashApply: %w", err)
	}
	return nil
}

// StashDrop removes a stash entry by index.
func StashDrop(repoPath string, index int) error {
	if !IsGitRepo(repoPath) {
		return fmt.Errorf("path %q is not a git repository", repoPath)
	}

	ref := fmt.Sprintf("stash@{%d}", index)
	_, err := runGit(repoPath, "stash", "drop", ref)
	if err != nil {
		return fmt.Errorf("StashDrop: %w", err)
	}
	return nil
}

// DiscardFile reverts a single file to its last committed state via
// `git checkout -- <filePath>`.
func DiscardFile(repoPath, filePath string) error {
	if !IsGitRepo(repoPath) {
		return fmt.Errorf("path %q is not a git repository", repoPath)
	}
	if filePath == "" {
		return fmt.Errorf("DiscardFile: filePath is required")
	}

	_, err := runGit(repoPath, "checkout", "--", filePath)
	if err != nil {
		return fmt.Errorf("DiscardFile: %w", err)
	}
	return nil
}
