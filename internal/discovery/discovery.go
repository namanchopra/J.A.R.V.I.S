package discovery

import (
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/namanchopra/jarvis/internal/claude"
	"github.com/namanchopra/jarvis/internal/git"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// Project represents a group of related repositories discovered under a common
// parent directory.
type Project struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Repos      []Repo `json:"repos"`
	IsMonorepo bool   `json:"isMonorepo"`
}

// Repo represents a single git repository discovered on the filesystem.
type Repo struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Branch   string `json:"branch"`
	HasAgent bool   `json:"hasAgent"` // currently has a Claude session
	Language string `json:"language"` // detected primary language
}

// TaskSuggestion represents a suggested task that can be executed across one or
// more repositories in a project.
type TaskSuggestion struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Repos       []string `json:"repos"`       // repo paths to execute on
	Prompt      string   `json:"prompt"`      // suggested prompt
	Sequential  bool     `json:"sequential"`  // true = one after another, false = parallel
}

// repoEntry is an internal type used during discovery to associate a repo path
// with its parent directory.
type repoEntry struct {
	path   string
	parent string
}

// ---------------------------------------------------------------------------
// DiscoverProjects
// ---------------------------------------------------------------------------

// DiscoverProjects scans the given root paths up to 2 levels deep for
// directories containing .git, groups them by parent directory into Projects,
// and enriches each repo with branch, language, and active session info.
func DiscoverProjects(rootPaths []string) ([]Project, error) {
	// Collect active Claude sessions for cross-referencing.
	activeSessions, err := claude.GetActiveSessions()
	if err != nil {
		slog.Warn("failed to get active Claude sessions", "err", err)
		activeSessions = nil
	}

	sessionPaths := make(map[string]bool, len(activeSessions))
	for _, s := range activeSessions {
		sessionPaths[s.CWD] = true
	}

	// Discover all git repos across all root paths.
	var repos []repoEntry

	for _, root := range rootPaths {
		root = expandHome(root)

		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			slog.Warn("skipping invalid root path", "path", root, "err", err)
			continue
		}

		// Check if root itself is a git repo (monorepo case).
		if git.IsGitRepo(root) {
			repos = append(repos, repoEntry{path: root, parent: filepath.Dir(root)})
		}

		// Scan level 1.
		level1Entries, err := os.ReadDir(root)
		if err != nil {
			slog.Warn("failed to read root directory", "path", root, "err", err)
			continue
		}

		for _, e1 := range level1Entries {
			if !e1.IsDir() || strings.HasPrefix(e1.Name(), ".") {
				continue
			}

			l1Path := filepath.Join(root, e1.Name())

			if git.IsGitRepo(l1Path) {
				repos = append(repos, repoEntry{path: l1Path, parent: root})
				continue
			}

			// Scan level 2.
			level2Entries, err := os.ReadDir(l1Path)
			if err != nil {
				continue
			}

			for _, e2 := range level2Entries {
				if !e2.IsDir() || strings.HasPrefix(e2.Name(), ".") {
					continue
				}

				l2Path := filepath.Join(l1Path, e2.Name())
				if git.IsGitRepo(l2Path) {
					repos = append(repos, repoEntry{path: l2Path, parent: l1Path})
				}
			}
		}
	}

	// Deduplicate repos by path.
	seen := make(map[string]bool, len(repos))
	var uniqueRepos []repoEntry
	for _, r := range repos {
		if !seen[r.path] {
			seen[r.path] = true
			uniqueRepos = append(uniqueRepos, r)
		}
	}
	repos = uniqueRepos

	// Group repos by parent directory to form Projects.
	groups := make(map[string][]repoEntry)
	for _, r := range repos {
		groups[r.parent] = append(groups[r.parent], r)
	}

	var projects []Project
	for parentPath, entries := range groups {
		proj := Project{
			Name:  filepath.Base(parentPath),
			Path:  parentPath,
			Repos: make([]Repo, 0, len(entries)),
		}

		for _, entry := range entries {
			repo := Repo{
				Name:     filepath.Base(entry.path),
				Path:     entry.path,
				HasAgent: sessionPaths[entry.path],
				Language: detectLanguage(entry.path),
			}

			// Get branch name (lightweight, skip expensive git operations).
			repo.Branch = getBranch(entry.path)

			proj.Repos = append(proj.Repos, repo)
		}

		// Check monorepo: root itself is a git repo and has sub-packages.
		proj.IsMonorepo = isMonorepo(parentPath, entries)

		// Sort repos by name for consistent output.
		sort.Slice(proj.Repos, func(i, j int) bool {
			return proj.Repos[i].Name < proj.Repos[j].Name
		})

		projects = append(projects, proj)
	}

	// Sort projects by name for consistent output.
	sort.Slice(projects, func(i, j int) bool {
		return projects[i].Name < projects[j].Name
	})

	return projects, nil
}

// ---------------------------------------------------------------------------
// SuggestTasks
// ---------------------------------------------------------------------------

// SuggestTasks analyzes a project's structure and returns suggested tasks that
// can be executed across its repositories.
func SuggestTasks(project Project) []TaskSuggestion {
	if len(project.Repos) == 0 {
		return []TaskSuggestion{}
	}

	allPaths := make([]string, 0, len(project.Repos))
	for _, r := range project.Repos {
		allPaths = append(allPaths, r.Path)
	}

	suggestions := []TaskSuggestion{}

	// Categorize repos by type.
	var frontends, backends, services, mobile []Repo
	for _, r := range project.Repos {
		name := strings.ToLower(r.Name)
		lang := strings.ToLower(r.Language)

		switch {
		case strings.Contains(name, "frontend") || strings.Contains(name, "web") || strings.Contains(name, "dashboard") || strings.Contains(name, "ui"):
			frontends = append(frontends, r)
		case strings.Contains(name, "mobile") || strings.Contains(name, "ios") || strings.Contains(name, "android") || strings.Contains(name, "app"):
			mobile = append(mobile, r)
		case strings.Contains(name, "api") || strings.Contains(name, "backend") || strings.Contains(name, "server") || strings.Contains(name, "service"):
			if lang == "Go" || lang == "Python" || lang == "Rust" || lang == "Ruby" {
				backends = append(backends, r)
			} else {
				services = append(services, r)
			}
		default:
			services = append(services, r)
		}
	}

	// If there are backends and frontends, suggest a coordinated update.
	if len(backends) > 0 && len(frontends) > 0 {
		seqPaths := make([]string, 0)
		for _, b := range backends {
			seqPaths = append(seqPaths, b.Path)
		}
		for _, f := range frontends {
			seqPaths = append(seqPaths, f.Path)
		}
		for _, m := range mobile {
			seqPaths = append(seqPaths, m.Path)
		}

		suggestions = append(suggestions, TaskSuggestion{
			Name:        "Coordinated API Update",
			Description: "Update the API first, then update frontend and mobile clients to match",
			Repos:       seqPaths,
			Prompt:      "Review and update the API integration. Ensure all endpoints are correctly consumed and error handling is in place.",
			Sequential:  true,
		})
	}

	// If multiple microservices, suggest cross-service change.
	if len(project.Repos) >= 2 {
		suggestions = append(suggestions, TaskSuggestion{
			Name:        "Apply Change Across All Repos",
			Description: "Apply the same change or update across all repositories in the project",
			Repos:       allPaths,
			Prompt:      "Apply the following change: [describe your change here]",
			Sequential:  false,
		})
	}

	// Generic: run /start on all repos.
	suggestions = append(suggestions, TaskSuggestion{
		Name:        "Start All Agents",
		Description: "Launch Claude Code in each repository with /start",
		Repos:       allPaths,
		Prompt:      "Read the project structure and understand the codebase. List the main features and architecture.",
		Sequential:  false,
	})

	// Review all repos for bugs/issues.
	suggestions = append(suggestions, TaskSuggestion{
		Name:        "Review All Repos",
		Description: "Review each repository for bugs, security issues, and code quality",
		Repos:       allPaths,
		Prompt:      "Review this codebase for bugs, security vulnerabilities, and code quality issues. Summarize findings.",
		Sequential:  false,
	})

	// If monorepo, suggest package-level tasks.
	if project.IsMonorepo {
		suggestions = append(suggestions, TaskSuggestion{
			Name:        "Monorepo: Update Shared Dependencies",
			Description: "Update shared dependencies across all packages in the monorepo",
			Repos:       allPaths,
			Prompt:      "Check for outdated dependencies and update them. Ensure compatibility across packages.",
			Sequential:  true,
		})
	}

	return suggestions
}

// ---------------------------------------------------------------------------
// GetCommonRootPaths
// ---------------------------------------------------------------------------

// GetCommonRootPaths returns common root directories by examining the user's
// home directory for typical project locations. It checks well-known paths
// like ~/Desktop, ~/Projects, ~/repos, ~/code, ~/src, and ~/Developer.
func GetCommonRootPaths() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	candidates := []string{
		filepath.Join(home, "Desktop"),
		filepath.Join(home, "Projects"),
		filepath.Join(home, "projects"),
		filepath.Join(home, "repos"),
		filepath.Join(home, "code"),
		filepath.Join(home, "src"),
		filepath.Join(home, "Developer"),
		filepath.Join(home, "dev"),
		filepath.Join(home, "workspace"),
		filepath.Join(home, "Work"),
		filepath.Join(home, "work"),
	}

	var roots []string
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			roots = append(roots, c)
		}
	}

	return roots
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// getBranch returns the current git branch for the given repo path. It only
// reads the branch name and avoids expensive git operations.
func getBranch(repoPath string) string {
	// Try reading .git/HEAD directly for speed (avoids spawning a git process).
	headPath := filepath.Join(repoPath, ".git", "HEAD")
	data, err := os.ReadFile(headPath)
	if err != nil {
		return ""
	}

	content := strings.TrimSpace(string(data))
	// HEAD file looks like: "ref: refs/heads/main"
	if strings.HasPrefix(content, "ref: refs/heads/") {
		return strings.TrimPrefix(content, "ref: refs/heads/")
	}

	// Detached HEAD — return short hash.
	if len(content) >= 8 {
		return content[:8]
	}
	return content
}

// detectLanguage examines config files in a directory to determine the primary
// programming language.
func detectLanguage(repoPath string) string {
	checks := []struct {
		file     string
		language string
	}{
		{"go.mod", "Go"},
		{"Cargo.toml", "Rust"},
		{"pyproject.toml", "Python"},
		{"requirements.txt", "Python"},
		{"setup.py", "Python"},
		{"Gemfile", "Ruby"},
		{"build.gradle", "Java"},
		{"build.gradle.kts", "Kotlin"},
		{"pom.xml", "Java"},
		{"Package.swift", "Swift"},
		{"pubspec.yaml", "Dart"},
		{"mix.exs", "Elixir"},
		{"composer.json", "PHP"},
	}

	for _, c := range checks {
		if _, err := os.Stat(filepath.Join(repoPath, c.file)); err == nil {
			return c.language
		}
	}

	// Check package.json last because it could be TypeScript or JavaScript.
	pkgJSONPath := filepath.Join(repoPath, "package.json")
	if _, err := os.Stat(pkgJSONPath); err == nil {
		// If tsconfig.json exists, it's TypeScript.
		if _, err := os.Stat(filepath.Join(repoPath, "tsconfig.json")); err == nil {
			return "TypeScript"
		}
		return "JavaScript"
	}

	return "Unknown"
}

// isMonorepo checks whether the parent directory itself is a git repo that
// contains subdirectories which look like sub-packages. This detects
// monorepo structures (e.g., Lerna, Nx, Go workspaces, Cargo workspaces).
func isMonorepo(parentPath string, entries []repoEntry) bool {
	// A monorepo root is a git repo that contains child repos or package dirs.
	if !git.IsGitRepo(parentPath) {
		return false
	}

	// Check for common monorepo markers.
	monoMarkers := []string{
		"lerna.json",
		"nx.json",
		"pnpm-workspace.yaml",
		"go.work",
		"Cargo.toml", // Cargo workspace
		"turbo.json",
	}

	for _, marker := range monoMarkers {
		if _, err := os.Stat(filepath.Join(parentPath, marker)); err == nil {
			return true
		}
	}

	// If the parent is a git repo and has multiple child dirs with their own
	// package manifests, it's likely a monorepo.
	packageCount := 0
	childEntries, err := os.ReadDir(parentPath)
	if err != nil {
		return false
	}

	packageFiles := []string{"package.json", "go.mod", "Cargo.toml", "pyproject.toml", "setup.py"}

	for _, child := range childEntries {
		if !child.IsDir() || strings.HasPrefix(child.Name(), ".") || child.Name() == "node_modules" {
			continue
		}
		childPath := filepath.Join(parentPath, child.Name())
		for _, pf := range packageFiles {
			if _, err := os.Stat(filepath.Join(childPath, pf)); err == nil {
				packageCount++
				break
			}
		}
	}

	return packageCount >= 2
}

// expandHome replaces a leading ~ with the user's home directory.
func expandHome(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}

	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	return path
}
