package discovery

import (
	"encoding/json"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/namanchopra/jarvis/internal/claude"
)

// RepoSearchResult represents a single repository found via the repo search
// index. It combines path information with live state (branch, active session).
type RepoSearchResult struct {
	Name     string `json:"name"`     // basename of the repo directory
	Path     string `json:"path"`     // full absolute path
	Language string `json:"language"` // detected primary language
	HasAgent bool   `json:"hasAgent"` // currently has an active Claude session
	Branch   string `json:"branch"`   // current git branch
}

// SearchRepos indexes all git repos the user has worked with (derived from
// Claude Code session history and project history) and filters them by the
// given query string. The query is matched case-insensitively against both the
// full path and the basename.
//
// Results are sorted with active sessions first, then alphabetically by name,
// and limited to 20 entries.
func SearchRepos(query string) ([]RepoSearchResult, error) {
	// Collect repo paths from three sources.
	pathSet := make(map[string]struct{})

	// Source A: CWDs from Claude Code session files (~/.claude/sessions/*.json).
	collectSessionPaths(pathSet)

	// Source B: project history directories (~/.claude/projects/).
	collectProjectHistoryPaths(pathSet)

	// Source C: git repos found under common root directories (~/Desktop, ~/projects, etc.).
	collectFilesystemRepos(pathSet)

	// Build the set of active session CWDs for HasAgent checks.
	activeCWDs := buildActiveSessionSet()

	query = strings.ToLower(strings.TrimSpace(query))

	var results []RepoSearchResult

	for repoPath := range pathSet {
		// Verify the directory still exists on disk.
		info, err := os.Stat(repoPath)
		if err != nil || !info.IsDir() {
			continue
		}

		baseName := filepath.Base(repoPath)

		// Filter by query (case-insensitive substring match on path and basename).
		if query != "" {
			lowerPath := strings.ToLower(repoPath)
			lowerBase := strings.ToLower(baseName)
			if !strings.Contains(lowerPath, query) && !strings.Contains(lowerBase, query) {
				continue
			}
		}

		results = append(results, RepoSearchResult{
			Name:     baseName,
			Path:     repoPath,
			Language: detectLanguage(repoPath),
			HasAgent: activeCWDs[repoPath],
			Branch:   getBranch(repoPath),
		})
	}

	// Sort: active sessions first, then alphabetically by name.
	sort.Slice(results, func(i, j int) bool {
		if results[i].HasAgent != results[j].HasAgent {
			return results[i].HasAgent
		}
		return strings.ToLower(results[i].Name) < strings.ToLower(results[j].Name)
	})

	// Limit to 50 results.
	if len(results) > 50 {
		results = results[:50]
	}

	if results == nil {
		results = []RepoSearchResult{}
	}

	return results, nil
}

// collectSessionPaths reads all session files from ~/.claude/sessions/ and
// adds each session's CWD to the path set. Both live and stale session files
// are read -- the CWD is valuable history regardless of whether the process is
// still alive.
func collectSessionPaths(pathSet map[string]struct{}) {
	dir := claude.SessionsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Debug("SearchRepos: failed to read sessions dir", "err", err)
		}
		return
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}

		var sess claude.Session
		if err := json.Unmarshal(data, &sess); err != nil {
			continue
		}

		if sess.CWD != "" {
			pathSet[sess.CWD] = struct{}{}
		}
	}
}

// collectProjectHistoryPaths reads the ~/.claude/projects/ directory. Each
// subdirectory name is a URL-encoded absolute path representing a project the
// user has previously opened with Claude Code.
func collectProjectHistoryPaths(pathSet map[string]struct{}) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}

	projectsDir := filepath.Join(home, ".claude", "projects")
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Debug("SearchRepos: failed to read projects dir", "err", err)
		}
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		// Directory names are URL-encoded absolute paths
		// (e.g. "%2FUsers%2Ffoo%2Fproject").
		decoded, err := url.PathUnescape(entry.Name())
		if err != nil {
			slog.Debug("SearchRepos: failed to decode project dir name",
				"name", entry.Name(), "err", err)
			continue
		}

		if decoded != "" {
			pathSet[decoded] = struct{}{}
		}
	}
}

// collectFilesystemRepos scans common root directories for git repos (up to
// 2 levels deep) and adds them to the path set. This surfaces all local
// projects, not just ones with Claude Code history.
func collectFilesystemRepos(pathSet map[string]struct{}) {
	roots := GetCommonRootPaths()
	for _, root := range roots {
		// Level 1: direct children
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			dirPath := filepath.Join(root, entry.Name())
			if isGitRepo(dirPath) {
				pathSet[dirPath] = struct{}{}
				continue
			}
			// Level 2: grandchildren (e.g. ~/Desktop/projects/some-app)
			subEntries, err := os.ReadDir(dirPath)
			if err != nil {
				continue
			}
			for _, sub := range subEntries {
				if !sub.IsDir() || strings.HasPrefix(sub.Name(), ".") {
					continue
				}
				subPath := filepath.Join(dirPath, sub.Name())
				if isGitRepo(subPath) {
					pathSet[subPath] = struct{}{}
				}
			}
		}
	}
}

func isGitRepo(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil && info.IsDir()
}

// buildActiveSessionSet returns a set of CWDs from currently active (live
// process) Claude Code sessions.
func buildActiveSessionSet() map[string]bool {
	sessions, err := claude.GetActiveSessions()
	if err != nil {
		slog.Debug("SearchRepos: failed to get active sessions", "err", err)
		return nil
	}

	cwds := make(map[string]bool, len(sessions))
	for _, s := range sessions {
		if s.CWD != "" {
			cwds[s.CWD] = true
		}
	}
	return cwds
}
