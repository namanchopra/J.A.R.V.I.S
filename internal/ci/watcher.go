// Package ci provides background monitoring of CI pipeline status for active
// Claude Code sessions. It shells out to the `gh` CLI to query GitHub Actions
// and exposes results via a thread-safe Watcher that the UI can poll.
package ci

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// CIStatus represents the high-level state of a CI run.
type CIStatus string

const (
	CIStatusPending CIStatus = "pending"
	CIStatusRunning CIStatus = "running"
	CIStatusPassed  CIStatus = "passed"
	CIStatusFailed  CIStatus = "failed"
	CIStatusUnknown CIStatus = "unknown"
)

// CIResult holds the outcome of a single CI check for one repository.
type CIResult struct {
	RepoPath   string   `json:"repoPath"`
	Branch     string   `json:"branch"`
	Status     CIStatus `json:"status"`
	Conclusion string   `json:"conclusion"` // success, failure, cancelled, or empty
	URL        string   `json:"url"`        // link to CI run
	CheckedAt  time.Time `json:"checkedAt"`
}

// pollInterval is how often the Watcher checks CI status.
const pollInterval = 30 * time.Second

// ghRunResult maps the JSON fields returned by `gh run list --json`.
type ghRunResult struct {
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	URL        string `json:"url"`
}

// CheckCI checks GitHub Actions status for a repo using the `gh` CLI.
// It returns the latest workflow run status for the current branch.
// If `gh` is not installed or any step fails, it returns CIStatusUnknown
// rather than an error — CI monitoring is best-effort.
func CheckCI(ctx context.Context, repoPath string) (*CIResult, error) {
	result := &CIResult{
		RepoPath:  repoPath,
		Status:    CIStatusUnknown,
		CheckedAt: time.Now(),
	}

	// 1. Get the current branch.
	branch, err := runGit(ctx, repoPath, "branch", "--show-current")
	if err != nil {
		slog.Debug("ci: failed to get branch", "repo", repoPath, "err", err)
		return result, nil
	}
	branch = strings.TrimSpace(branch)
	if branch == "" {
		// Detached HEAD or empty repo — nothing useful to query.
		return result, nil
	}
	result.Branch = branch

	// 2. Query the latest workflow run for this branch.
	ghOut, err := runGh(ctx, repoPath,
		"run", "list",
		"--branch", branch,
		"--limit", "1",
		"--json", "status,conclusion,url",
	)
	if err != nil {
		slog.Debug("ci: gh run list failed", "repo", repoPath, "err", err)
		return result, nil
	}

	// 3. Parse the JSON output.
	var runs []ghRunResult
	if err := json.Unmarshal([]byte(ghOut), &runs); err != nil {
		slog.Debug("ci: failed to parse gh output", "repo", repoPath, "err", err)
		return result, nil
	}
	if len(runs) == 0 {
		// No workflow runs found for this branch.
		return result, nil
	}

	run := runs[0]
	result.URL = run.URL
	result.Conclusion = run.Conclusion
	result.Status = mapGhStatus(run.Status, run.Conclusion)

	return result, nil
}

// mapGhStatus converts the GitHub Actions status/conclusion pair into our
// CIStatus enum.
func mapGhStatus(status, conclusion string) CIStatus {
	switch status {
	case "queued", "waiting", "pending":
		return CIStatusPending
	case "in_progress":
		return CIStatusRunning
	case "completed":
		switch conclusion {
		case "success":
			return CIStatusPassed
		case "failure", "timed_out":
			return CIStatusFailed
		default:
			// cancelled, skipped, action_required, etc.
			return CIStatusUnknown
		}
	default:
		return CIStatusUnknown
	}
}

// ---------------------------------------------------------------------------
// Watcher
// ---------------------------------------------------------------------------

// Watcher periodically checks CI status for repositories that have active
// Claude Code sessions. It is safe for concurrent reads via GetResults.
type Watcher struct {
	results map[string]*CIResult // repoPath -> latest result
	mu      sync.RWMutex
}

// NewWatcher creates a Watcher with an empty result set.
func NewWatcher() *Watcher {
	return &Watcher{
		results: make(map[string]*CIResult),
	}
}

// GetResults returns a snapshot of the current CI results keyed by repo path.
// The returned map is a shallow copy; callers may read it without holding a lock.
func (w *Watcher) GetResults() map[string]*CIResult {
	w.mu.RLock()
	defer w.mu.RUnlock()

	out := make(map[string]*CIResult, len(w.results))
	for k, v := range w.results {
		out[k] = v
	}
	return out
}

// Start begins polling CI status for every CWD returned by getActiveCWDs.
// It blocks until ctx is cancelled. Call it in a goroutine:
//
//	go watcher.Start(ctx, func() []string { ... })
func (w *Watcher) Start(ctx context.Context, getActiveCWDs func() []string) {
	slog.Info("ci: watcher started", "interval", pollInterval)

	// Run an initial check immediately, then tick.
	w.poll(ctx, getActiveCWDs)

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("ci: watcher stopped")
			return
		case <-ticker.C:
			w.poll(ctx, getActiveCWDs)
		}
	}
}

// poll runs a single round of CI checks across all active CWDs.
func (w *Watcher) poll(ctx context.Context, getActiveCWDs func() []string) {
	cwds := getActiveCWDs()
	if len(cwds) == 0 {
		return
	}

	// Deduplicate CWDs so we don't check the same repo twice.
	seen := make(map[string]struct{}, len(cwds))
	unique := make([]string, 0, len(cwds))
	for _, cwd := range cwds {
		if _, ok := seen[cwd]; ok {
			continue
		}
		seen[cwd] = struct{}{}
		unique = append(unique, cwd)
	}

	for _, repoPath := range unique {
		// Bail early if the context was cancelled mid-loop.
		if ctx.Err() != nil {
			return
		}

		result, err := CheckCI(ctx, repoPath)
		if err != nil {
			// CheckCI already logs and returns a safe default; this is
			// defence-in-depth for any future code path that returns an error.
			slog.Warn("ci: unexpected error from CheckCI", "repo", repoPath, "err", err)
			continue
		}

		w.mu.Lock()
		w.results[repoPath] = result
		w.mu.Unlock()
	}

	slog.Debug("ci: poll complete", "repos_checked", len(unique))
}

// ---------------------------------------------------------------------------
// Shell helpers
// ---------------------------------------------------------------------------

// runGit executes a git command scoped to the given repo path and returns
// the combined stdout output.
func runGit(ctx context.Context, repoPath string, args ...string) (string, error) {
	full := append([]string{"-C", repoPath}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %v: %w", args, err)
	}
	return string(out), nil
}

// runGh executes a `gh` CLI command with the working directory set to
// repoPath and returns the combined stdout output.
func runGh(ctx context.Context, repoPath string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("gh %v: %w", args, err)
	}
	return string(out), nil
}
