// Package workspace implements the Virtual Monorepo workspace system.
//
// A workspace is a directory at ~/.jarvis/workspaces/<name>/ that contains
// per-platform directory links to multiple real repositories. On macOS / Linux
// we create POSIX symbolic links (os.Symlink); on Windows we create directory
// junction points via `cmd /c mklink /J` (see workspace_windows.go, TASK-033)
// because junctions do not require Developer Mode or admin elevation, while
// symlinks on Windows do. When launched, Claude Code sees all repos as one
// unified workspace via the --add-dir flags. A CLAUDE.md is auto-generated to
// describe the project structure, repo relationships, and the task at hand.
//
// Deletion is handled by os.RemoveAll, which on every platform — including
// Windows for junctions — removes the link itself rather than following it
// into the target, so the underlying repositories are never harmed.
package workspace

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/namanchopra/jarvis/internal/git"
	"github.com/namanchopra/jarvis/internal/paths"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// Workspace represents a virtual monorepo workspace that aggregates multiple
// real repositories into a single working directory with symlinks.
type Workspace struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Path      string    `json:"path"`      // ~/.jarvis/workspaces/<name>/
	RepoPaths []string  `json:"repoPaths"` // absolute paths to real repos
	Prompt    string    `json:"prompt"`    // the task/feature description
	CreatedAt time.Time `json:"createdAt"`
}

// metadataFile is the name of the JSON file that stores workspace metadata.
const metadataFile = ".awm-workspace.json"

// repoMeta holds detected metadata for a single repository within a workspace.
type repoMeta struct {
	basename string
	fullPath string
	language string
	branch   string
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

// WorkspacesDir returns the base directory for all workspaces (~/.jarvis/workspaces/).
func WorkspacesDir() string {
	return paths.WorkspacesDir()
}

// Create builds a new virtual monorepo workspace.
//
//  1. Sanitizes the name (lowercase, hyphens, no special chars).
//  2. Creates the workspace directory at ~/.jarvis/workspaces/<sanitized-name>/.
//  3. Symlinks each repo into the workspace directory.
//  4. Generates a CLAUDE.md describing the workspace structure.
//  5. Creates a .claude/ directory with basic settings.
//  6. Persists metadata to .awm-workspace.json inside the workspace.
func Create(name string, repoPaths []string, prompt string) (*Workspace, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("workspace name is required")
	}
	if len(repoPaths) == 0 {
		return nil, fmt.Errorf("at least one repo path is required")
	}

	sanitized := sanitizeName(name)
	wsDir := filepath.Join(WorkspacesDir(), sanitized)

	// If the directory already exists, append a short suffix to avoid collisions.
	if _, err := os.Stat(wsDir); err == nil {
		wsDir = wsDir + "-" + uuid.New().String()[:8]
	}

	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating workspace directory %q: %w", wsDir, err)
	}

	// Create symlinks for each repo, handling basename collisions.
	usedNames := make(map[string]int)
	for _, repoPath := range repoPaths {
		repoPath = strings.TrimSpace(repoPath)
		if repoPath == "" {
			continue
		}

		baseName := filepath.Base(repoPath)
		linkName := baseName

		if count, exists := usedNames[baseName]; exists {
			usedNames[baseName] = count + 1
			linkName = fmt.Sprintf("%s-%d", baseName, count+1)
		} else {
			usedNames[baseName] = 1
		}

		linkPath := filepath.Join(wsDir, linkName)
		// linkRepo dispatches to os.Symlink on POSIX and `mklink /J` on Windows
		// (workspace_windows.go / workspace_other.go, TASK-033). Junctions, like
		// symlinks, fail cleanly when the target directory does not exist —
		// satisfying the failure-case acceptance criterion for missing repos.
		if err := linkRepo(repoPath, linkPath); err != nil {
			slog.Warn("failed to create link for repo",
				"repo", repoPath, "link", linkPath, "err", err)
			// Continue with other repos rather than failing entirely.
		}
	}

	ws := &Workspace{
		ID:        uuid.New().String(),
		Name:      name,
		Path:      wsDir,
		RepoPaths: repoPaths,
		Prompt:    prompt,
		CreatedAt: time.Now(),
	}

	// Generate CLAUDE.md.
	claudeMD := GenerateCLAUDEmd(ws)
	claudeMDPath := filepath.Join(wsDir, "CLAUDE.md")
	if err := os.WriteFile(claudeMDPath, []byte(claudeMD), 0o644); err != nil {
		slog.Warn("failed to write CLAUDE.md", "path", claudeMDPath, "err", err)
	}

	// Copy .claude/ directory from the dotAiAgent source (or fallback to parent
	// repo's .claude) so the workspace has all agents, skills, learnings, settings.
	if err := copyDotClaude(wsDir, repoPaths); err != nil {
		slog.Warn("failed to copy .claude directory, creating minimal config", "err", err)
		// Fallback: create minimal .claude/ so Claude Code recognises this as a project.
		claudeDir := filepath.Join(wsDir, ".claude")
		_ = os.MkdirAll(claudeDir, 0o755)
		settingsPath := filepath.Join(claudeDir, "settings.json")
		settings := map[string]interface{}{
			"permissions": map[string]interface{}{
				"allow": []string{"Read", "Write", "Edit", "Bash", "Glob", "Grep"},
			},
		}
		settingsJSON, _ := json.MarshalIndent(settings, "", "  ")
		_ = os.WriteFile(settingsPath, settingsJSON, 0o644)
	}

	// Persist workspace metadata for List/recovery.
	if err := saveMetadata(ws); err != nil {
		slog.Warn("failed to save workspace metadata", "path", ws.Path, "err", err)
	}

	return ws, nil
}

// GenerateCLAUDEmd builds the content of the CLAUDE.md file for a workspace.
// The file describes the task, lists each repository with detected metadata,
// auto-detects architecture relationships, and provides cross-repo guidelines.
func GenerateCLAUDEmd(ws *Workspace) string {
	var b strings.Builder

	// Header
	b.WriteString(fmt.Sprintf("# Workspace: %s\n\n", ws.Name))

	// Task section
	b.WriteString("## Task\n")
	if ws.Prompt != "" {
		b.WriteString(ws.Prompt)
	} else {
		b.WriteString("(no task description provided)")
	}
	b.WriteString("\n\n")

	// Repositories section
	b.WriteString("## Repositories\n")
	repos := make([]repoMeta, 0, len(ws.RepoPaths))
	for _, rp := range ws.RepoPaths {
		meta := repoMeta{
			basename: filepath.Base(rp),
			fullPath: rp,
			language: detectLanguage(rp),
			branch:   detectBranch(rp),
		}
		repos = append(repos, meta)
	}

	for _, r := range repos {
		branchInfo := ""
		if r.branch != "" {
			branchInfo = fmt.Sprintf(", branch: `%s`", r.branch)
		}
		langInfo := r.language
		if langInfo == "" {
			langInfo = "Unknown"
		}
		b.WriteString(fmt.Sprintf("- **%s** (`%s`) -- %s%s\n", r.basename, r.fullPath, langInfo, branchInfo))

		// If the repo has a package.json, try to read project name/description.
		if desc := readPackageJSONDescription(r.fullPath); desc != "" {
			b.WriteString(fmt.Sprintf("  %s\n", desc))
		}
	}
	b.WriteString("\n")

	// Architecture section
	b.WriteString("## Architecture\n")
	b.WriteString(fmt.Sprintf("This workspace contains %d repositories that work together.\n\n", len(ws.RepoPaths)))

	// Auto-detect relationships.
	relationships := detectRelationships(repos)
	for _, rel := range relationships {
		b.WriteString(fmt.Sprintf("- %s\n", rel))
	}
	if len(relationships) == 0 {
		b.WriteString("- Repository relationships will be determined based on project structure.\n")
	}
	b.WriteString("\n")

	// Cross-repo guidelines
	b.WriteString("## Cross-Repo Guidelines\n")
	b.WriteString("- Changes to API contracts in backend repos should be reflected in frontend/mobile repos\n")
	b.WriteString("- Run tests in each repo after making changes\n")
	b.WriteString("- Commit changes in each repo separately\n")
	b.WriteString("- When modifying shared types or interfaces, check all consuming repos for breakage\n")

	return b.String()
}

// BuildLaunchArgs returns the Claude Code CLI arguments needed to give the
// session access to all real repo paths. The workspace directory will be the
// CWD; --add-dir flags point to the actual repo paths for full read/write
// access.
func BuildLaunchArgs(ws *Workspace) []string {
	var args []string
	for _, rp := range ws.RepoPaths {
		args = append(args, "--add-dir", rp)
	}
	return args
}

// Delete removes a workspace directory and all of its contents (symlinks,
// CLAUDE.md, metadata, etc.).
func Delete(workspacePath string) error {
	workspacePath = strings.TrimSpace(workspacePath)
	if workspacePath == "" {
		return fmt.Errorf("workspace path is required")
	}

	// Safety check: only delete under the workspaces directory.
	wsBase := WorkspacesDir()
	if !strings.HasPrefix(workspacePath, wsBase) {
		return fmt.Errorf("refusing to delete path outside workspaces directory: %q", workspacePath)
	}

	if err := os.RemoveAll(workspacePath); err != nil {
		return fmt.Errorf("deleting workspace %q: %w", workspacePath, err)
	}

	return nil
}

// List reads ~/.jarvis/workspaces/ and returns metadata for all workspaces.
func List() ([]Workspace, error) {
	wsBase := WorkspacesDir()
	entries, err := os.ReadDir(wsBase)
	if err != nil {
		if os.IsNotExist(err) {
			return []Workspace{}, nil
		}
		return nil, fmt.Errorf("reading workspaces directory %q: %w", wsBase, err)
	}

	workspaces := make([]Workspace, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		wsPath := filepath.Join(wsBase, entry.Name())
		ws, err := loadMetadata(wsPath)
		if err != nil {
			slog.Debug("skipping workspace without metadata", "path", wsPath, "err", err)
			continue
		}
		workspaces = append(workspaces, *ws)
	}

	return workspaces, nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// sanitizeName converts a human-readable name to a filesystem-safe directory
// name: lowercase, spaces to hyphens, only alphanumeric and hyphens allowed.
func sanitizeName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, " ", "-")

	// Remove characters that are not alphanumeric or hyphens.
	re := regexp.MustCompile(`[^a-z0-9-]`)
	name = re.ReplaceAllString(name, "")

	// Collapse multiple hyphens.
	re = regexp.MustCompile(`-{2,}`)
	name = re.ReplaceAllString(name, "-")

	// Trim leading/trailing hyphens.
	name = strings.Trim(name, "-")

	if name == "" {
		name = "workspace"
	}

	return name
}

// saveMetadata writes the workspace metadata JSON to the workspace directory.
func saveMetadata(ws *Workspace) error {
	data, err := json.MarshalIndent(ws, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling workspace metadata: %w", err)
	}

	metaPath := filepath.Join(ws.Path, metadataFile)
	if err := os.WriteFile(metaPath, data, 0o644); err != nil {
		return fmt.Errorf("writing workspace metadata to %q: %w", metaPath, err)
	}

	return nil
}

// loadMetadata reads the workspace metadata JSON from a workspace directory.
func loadMetadata(wsPath string) (*Workspace, error) {
	metaPath := filepath.Join(wsPath, metadataFile)
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, fmt.Errorf("reading workspace metadata from %q: %w", metaPath, err)
	}

	var ws Workspace
	if err := json.Unmarshal(data, &ws); err != nil {
		return nil, fmt.Errorf("parsing workspace metadata from %q: %w", metaPath, err)
	}

	return &ws, nil
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
	if _, err := os.Stat(filepath.Join(repoPath, "package.json")); err == nil {
		if _, err := os.Stat(filepath.Join(repoPath, "tsconfig.json")); err == nil {
			return "TypeScript"
		}
		return "JavaScript"
	}

	return "Unknown"
}

// detectBranch reads the current git branch for a repo path. Returns an empty
// string if the path is not a git repo or the branch cannot be determined.
func detectBranch(repoPath string) string {
	if !git.IsGitRepo(repoPath) {
		return ""
	}

	// Read .git/HEAD directly for speed.
	headPath := filepath.Join(repoPath, ".git", "HEAD")
	data, err := os.ReadFile(headPath)
	if err != nil {
		return ""
	}

	content := strings.TrimSpace(string(data))
	if strings.HasPrefix(content, "ref: refs/heads/") {
		return strings.TrimPrefix(content, "ref: refs/heads/")
	}

	// Detached HEAD -- return short hash.
	if len(content) >= 8 {
		return content[:8]
	}
	return content
}

// readPackageJSONDescription reads a package.json and returns a brief
// description string like "project-name: description". Returns "" on any error.
func readPackageJSONDescription(repoPath string) string {
	pkgPath := filepath.Join(repoPath, "package.json")
	data, err := os.ReadFile(pkgPath)
	if err != nil {
		return ""
	}

	var pkg struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return ""
	}

	if pkg.Name == "" && pkg.Description == "" {
		return ""
	}

	parts := make([]string, 0, 2)
	if pkg.Name != "" {
		parts = append(parts, pkg.Name)
	}
	if pkg.Description != "" {
		parts = append(parts, pkg.Description)
	}
	return strings.Join(parts, ": ")
}

// detectRelationships analyzes repository names and paths to auto-detect
// architectural relationships between them.
func detectRelationships(repos []repoMeta) []string {
	var relationships []string

	// Check if multiple repos share a parent directory (part of the same project).
	parents := make(map[string][]string)
	for _, r := range repos {
		parent := filepath.Dir(r.fullPath)
		parents[parent] = append(parents[parent], r.basename)
	}
	for parent, names := range parents {
		if len(names) >= 2 {
			relationships = append(relationships,
				fmt.Sprintf("Repos %s share the same parent directory (`%s`) and are likely part of the same project",
					strings.Join(names, ", "), parent))
		}
	}

	// Detect backend/frontend/mobile by name patterns.
	backendPatterns := []string{"service", "api", "server", "backend", "gateway"}
	frontendPatterns := []string{"web", "frontend", "app", "ui", "dashboard", "client"}
	mobilePatterns := []string{"mobile", "rn", "native", "ios", "android", "react-native"}

	var backends, frontends, mobiles []string

	for _, r := range repos {
		lower := strings.ToLower(r.basename)
		for _, p := range backendPatterns {
			if strings.Contains(lower, p) {
				backends = append(backends, r.basename)
				break
			}
		}
		for _, p := range frontendPatterns {
			if strings.Contains(lower, p) {
				frontends = append(frontends, r.basename)
				break
			}
		}
		for _, p := range mobilePatterns {
			if strings.Contains(lower, p) {
				mobiles = append(mobiles, r.basename)
				break
			}
		}
	}

	if len(backends) > 0 {
		relationships = append(relationships,
			fmt.Sprintf("Backend services: %s", strings.Join(backends, ", ")))
	}
	if len(frontends) > 0 {
		relationships = append(relationships,
			fmt.Sprintf("Frontend applications: %s", strings.Join(frontends, ", ")))
	}
	if len(mobiles) > 0 {
		relationships = append(relationships,
			fmt.Sprintf("Mobile applications: %s", strings.Join(mobiles, ", ")))
	}

	return relationships
}

// ---------------------------------------------------------------------------
// .claude directory management — copy from dotAiAgent + sync
// ---------------------------------------------------------------------------

// dotAiAgentSourcePath returns the path to the .claude directory source.
// Priority: 1) User-configured path in Jarvis settings, 2) dotAiAgent in repo parent,
// 3) Common filesystem locations, 4) Parent repo .claude folder.
func dotAiAgentSourcePath(repoPaths []string) string {
	home, _ := os.UserHomeDir()

	// 0. Check user-configured path first (from ~/.jarvis/config.json).
	configPath := paths.ConfigPath()
	if data, err := os.ReadFile(configPath); err == nil {
		var cfg struct {
			DotClaudeSourcePath string `json:"dotClaudeSourcePath"`
		}
		if json.Unmarshal(data, &cfg) == nil && cfg.DotClaudeSourcePath != "" {
			// The configured path might point to a repo root or directly to .claude/.
			if isDir(filepath.Join(cfg.DotClaudeSourcePath, ".claude")) {
				return filepath.Join(cfg.DotClaudeSourcePath, ".claude")
			}
			if isDir(cfg.DotClaudeSourcePath) {
				return cfg.DotClaudeSourcePath
			}
		}
	}

	// 1. Check for dotAiAgent repo in common parent of selected repos.
	if len(repoPaths) > 0 {
		parent := filepath.Dir(repoPaths[0])
		candidate := filepath.Join(parent, "dotAiAgent", ".claude")
		if isDir(candidate) {
			return candidate
		}
	}

	// 2. Check common locations (not hardcoded to any specific user's paths).
	commonDirs := []string{"Desktop", "Projects", "projects", "repos", "code", "src", "Developer"}
	for _, dir := range commonDirs {
		// Check <home>/<dir>/dotAiAgent/.claude
		candidate := filepath.Join(home, dir, "dotAiAgent", ".claude")
		if isDir(candidate) {
			return candidate
		}
		// Check <home>/<dir>/**/dotAiAgent/.claude (one level deep)
		entries, _ := os.ReadDir(filepath.Join(home, dir))
		for _, e := range entries {
			if e.IsDir() {
				candidate = filepath.Join(home, dir, e.Name(), "dotAiAgent", ".claude")
				if isDir(candidate) {
					return candidate
				}
			}
		}
	}

	// 3. Check if any selected repo's parent has a .claude folder.
	for _, repoPath := range repoPaths {
		parent := filepath.Dir(repoPath)
		candidate := filepath.Join(parent, ".claude")
		if isDir(candidate) {
			return candidate
		}
	}

	return ""
}

// copyDotClaude copies the .claude directory from the dotAiAgent source into the
// workspace. This includes agents, skills, learnings, settings, and reviews.
func copyDotClaude(wsDir string, repoPaths []string) error {
	src := dotAiAgentSourcePath(repoPaths)
	if src == "" {
		return fmt.Errorf("no .claude source found (dotAiAgent repo not found)")
	}

	dst := filepath.Join(wsDir, ".claude")

	slog.Info("copying .claude directory", "src", src, "dst", dst)

	return copyDir(src, dst)
}

// SyncDotClaude pulls the latest dotAiAgent and re-copies .claude to all workspaces.
// If the dotAiAgent source is a git repo, it runs `git pull` first.
func SyncDotClaude() (int, error) {
	// Find the dotAiAgent source by checking common locations.
	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, "Desktop", "projects", "dotAiAgent"),
		filepath.Join(home, "dotAiAgent"),
	}

	var dotAiAgentRoot string
	for _, c := range candidates {
		if isDir(filepath.Join(c, ".claude")) {
			dotAiAgentRoot = c
			break
		}
	}
	if dotAiAgentRoot == "" {
		return 0, fmt.Errorf("dotAiAgent repo not found: set dotClaudeSourcePath in Settings (or ~/.jarvis/config.json) to point to your dotAiAgent directory")
	}

	// Pull latest if it's a git repo.
	if isDir(filepath.Join(dotAiAgentRoot, ".git")) {
		slog.Info("pulling latest dotAiAgent", "path", dotAiAgentRoot)
		if _, err := git.RunGitCommand(dotAiAgentRoot, "pull"); err != nil {
			slog.Warn("git pull failed for dotAiAgent", "err", err)
			// Continue anyway — use whatever is on disk.
		}
	}

	src := filepath.Join(dotAiAgentRoot, ".claude")

	// Copy to all existing workspaces.
	workspaces, err := List()
	if err != nil {
		return 0, fmt.Errorf("listing workspaces: %w", err)
	}

	synced := 0
	for _, ws := range workspaces {
		dst := filepath.Join(ws.Path, ".claude")
		// Remove existing .claude and re-copy.
		_ = os.RemoveAll(dst)
		if copyErr := copyDir(src, dst); copyErr != nil {
			slog.Warn("failed to sync .claude to workspace",
				"workspace", ws.Name, "err", copyErr)
			continue
		}
		synced++
	}

	slog.Info("synced .claude to workspaces", "count", synced)
	return synced, nil
}

// copyDir recursively copies a directory from src to dst.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip .DS_Store and .git.
		if info.Name() == ".DS_Store" || info.Name() == ".git" {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(dstPath, info.Mode())
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dstPath, data, info.Mode())
	})
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
