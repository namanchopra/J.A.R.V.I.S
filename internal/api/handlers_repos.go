package api

import (
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/namanchopra/jarvis/internal/discovery"
	"github.com/namanchopra/jarvis/internal/git"

	"github.com/labstack/echo/v4"
)

// ---------------------------------------------------------------------------
// Repo endpoints — GET /repos, GET /repos/:name/info
//
// Provides repo discovery and git info for mobile clients.
// ---------------------------------------------------------------------------

// RepoProvider abstracts the App methods needed by repo handlers.
type RepoProvider interface {
	DiscoverProjects() ([]discovery.Project, error)
	SearchRepos(query string) ([]discovery.RepoSearchResult, error)
	GetRepoInfo(repoPath string) (git.RepoInfo, error)
	GetRepoDiff(repoPath string) (git.DiffResult, error)
	GetStagedDiff(repoPath string) (git.DiffResult, error)
	GitStageAll(repoPath string) error
	GitCommit(repoPath, message string) error
	GitPush(repoPath string) error
}

// RepoPathResolver resolves a fuzzy project name to an absolute repo path.
// This is implemented by the App struct via resolveProjectPath, which is
// unexported. We accept a function to avoid depending on the main package.
type RepoPathResolver func(query string) string

// RegisterRepoRoutes mounts repo-related endpoints on the provided Echo group.
func RegisterRepoRoutes(g *echo.Group, provider RepoProvider, resolve RepoPathResolver) {
	h := &repoHandler{app: provider, resolve: resolve}

	g.GET("/repos", h.list)
	g.GET("/repos/:name/info", h.info)
	g.GET("/repos/:name/diff", h.diff)
	g.GET("/repos/:name/staged", h.staged)
	g.POST("/repos/:name/git/stage", h.stageAll)
	g.POST("/repos/:name/git/commit", h.commit)
	g.POST("/repos/:name/git/push", h.push)
}

// repoHandler holds the dependencies needed by repo endpoints.
type repoHandler struct {
	app     RepoProvider
	resolve RepoPathResolver
}

// repoListEntry is the flat repo representation returned by GET /repos.
type repoListEntry struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Language string `json:"language"`
	Branch   string `json:"branch"`
	HasAgent bool   `json:"hasAgent"`
	Project  string `json:"project"` // parent project name
}

// list handles GET /repos?query=<optional search>.
//
// Without a query param: discovers all projects and flattens their repos
// into a single list. With ?query=<text>: uses the search index for faster,
// more targeted results.
func (h *repoHandler) list(c echo.Context) error {
	query := strings.TrimSpace(c.QueryParam("query"))

	// If the caller provides a search query, use the fast search index.
	if query != "" {
		results, err := h.app.SearchRepos(query)
		if err != nil {
			slog.Error("failed to search repos", "err", err, "query", query)
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": "failed to search repos",
			})
		}
		return c.JSON(http.StatusOK, results)
	}

	// No query — full discovery scan.
	projects, err := h.app.DiscoverProjects()
	if err != nil {
		slog.Error("failed to discover projects", "err", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "failed to discover projects",
		})
	}

	// Flatten projects into a flat repo list.
	var repos []repoListEntry
	for _, proj := range projects {
		for _, repo := range proj.Repos {
			repos = append(repos, repoListEntry{
				Name:     repo.Name,
				Path:     repo.Path,
				Language: repo.Language,
				Branch:   repo.Branch,
				HasAgent: repo.HasAgent,
				Project:  proj.Name,
			})
		}
	}

	if repos == nil {
		repos = []repoListEntry{}
	}

	return c.JSON(http.StatusOK, repos)
}

// info handles GET /repos/:name/info.
//
// The :name parameter is a fuzzy repo/project name (e.g. "service-name",
// "my-app"). It is resolved to an absolute path using the fuzzy resolver.
// Raw absolute paths and path traversal sequences are rejected.
func (h *repoHandler) info(c echo.Context) error {
	repoPath, err := h.resolveRepoPath(c)
	if err != nil {
		return err
	}

	info, err := h.app.GetRepoInfo(repoPath)
	if err != nil {
		slog.Error("failed to get repo info", "err", err, "resolvedPath", repoPath)
		return c.JSON(http.StatusNotFound, map[string]string{
			"error": "repo not found or not a git repository",
		})
	}

	// Wrap the git.RepoInfo with the resolved path and name for convenience.
	type repoInfoResponse struct {
		Name string       `json:"name"`
		Path string       `json:"path"`
		Info git.RepoInfo `json:"info"`
	}

	return c.JSON(http.StatusOK, repoInfoResponse{
		Name: filepath.Base(repoPath),
		Path: repoPath,
		Info: info,
	})
}

// ---------------------------------------------------------------------------
// Compact diff response types
// ---------------------------------------------------------------------------

// compactFileDiff is a lightweight per-file summary with insertion/deletion
// counts derived from hunks.
type compactFileDiff struct {
	Path       string `json:"path"`
	Insertions int    `json:"insertions"`
	Deletions  int    `json:"deletions"`
}

// compactDiffResponse is the mobile-friendly diff envelope returned by the
// diff and staged endpoints.
type compactDiffResponse struct {
	Files []compactFileDiff `json:"files"`
	Stats git.DiffStats     `json:"stats"`
}

// toCompactDiff converts a full git.DiffResult into a compact summary suitable
// for mobile clients (no hunk content, just per-file +/- counts).
func toCompactDiff(dr git.DiffResult) compactDiffResponse {
	files := make([]compactFileDiff, 0, len(dr.Files))
	for _, f := range dr.Files {
		var ins, del int
		for _, h := range f.Hunks {
			for _, l := range h.Lines {
				switch l.Type {
				case "add":
					ins++
				case "delete":
					del++
				}
			}
		}
		files = append(files, compactFileDiff{
			Path:       f.Path,
			Insertions: ins,
			Deletions:  del,
		})
	}
	return compactDiffResponse{
		Files: files,
		Stats: dr.Stats,
	}
}

// ---------------------------------------------------------------------------
// Git diff & action handlers
// ---------------------------------------------------------------------------

// resolveRepoPath extracts :name from the URL, validates it against path
// traversal attacks, and resolves it to an absolute repo path via the fuzzy
// resolver. Returns an error (with JSON response already written) if the name
// is invalid or cannot be resolved.
func (h *repoHandler) resolveRepoPath(c echo.Context) (string, error) {
	name := strings.TrimSpace(c.Param("name"))
	if name == "" {
		return "", c.JSON(http.StatusBadRequest, map[string]string{
			"error": "repo name is required",
		})
	}

	// Reject path traversal sequences and absolute paths — only accept names
	// that the fuzzy resolver can match to a known project repo.
	if strings.Contains(name, "..") || filepath.IsAbs(name) {
		return "", c.JSON(http.StatusBadRequest, map[string]string{
			"error": "invalid repo name",
		})
	}

	var repoPath string
	if h.resolve != nil {
		repoPath = h.resolve(name)
	}

	if repoPath == "" {
		return "", c.JSON(http.StatusNotFound, map[string]string{
			"error": "could not resolve repo: " + name,
		})
	}

	return repoPath, nil
}

// diff handles GET /repos/:name/diff.
//
// Returns a compact diff (file list with per-file insertions/deletions and
// aggregate stats) for unstaged + staged changes vs HEAD.
func (h *repoHandler) diff(c echo.Context) error {
	repoPath, err := h.resolveRepoPath(c)
	if err != nil {
		return err
	}

	dr, err := h.app.GetRepoDiff(repoPath)
	if err != nil {
		slog.Error("failed to get repo diff", "err", err, "repoPath", repoPath)
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "failed to get diff",
		})
	}

	return c.JSON(http.StatusOK, toCompactDiff(dr))
}

// staged handles GET /repos/:name/staged.
//
// Returns a compact diff for staged (cached) changes only.
func (h *repoHandler) staged(c echo.Context) error {
	repoPath, err := h.resolveRepoPath(c)
	if err != nil {
		return err
	}

	dr, err := h.app.GetStagedDiff(repoPath)
	if err != nil {
		slog.Error("failed to get staged diff", "err", err, "repoPath", repoPath)
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "failed to get staged diff",
		})
	}

	return c.JSON(http.StatusOK, toCompactDiff(dr))
}

// stageAll handles POST /repos/:name/git/stage.
//
// Stages all changes (tracked and untracked) in the resolved repo.
func (h *repoHandler) stageAll(c echo.Context) error {
	repoPath, err := h.resolveRepoPath(c)
	if err != nil {
		return err
	}

	if err := h.app.GitStageAll(repoPath); err != nil {
		slog.Error("failed to stage all", "err", err, "repoPath", repoPath)
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "failed to stage changes",
		})
	}

	return c.JSON(http.StatusOK, map[string]string{"status": "staged"})
}

// commitRequest is the JSON body for POST /repos/:name/git/commit.
type commitRequest struct {
	Message string `json:"message"`
}

// commit handles POST /repos/:name/git/commit.
//
// Expects a JSON body with a "message" field.
func (h *repoHandler) commit(c echo.Context) error {
	repoPath, err := h.resolveRepoPath(c)
	if err != nil {
		return err
	}

	var req commitRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "invalid request body",
		})
	}

	msg := strings.TrimSpace(req.Message)
	if msg == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "commit message is required",
		})
	}

	if err := h.app.GitCommit(repoPath, msg); err != nil {
		slog.Error("failed to commit", "err", err, "repoPath", repoPath)
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "failed to commit",
		})
	}

	return c.JSON(http.StatusOK, map[string]string{"status": "committed"})
}

// push handles POST /repos/:name/git/push.
//
// Pushes commits to the remote for the resolved repo.
func (h *repoHandler) push(c echo.Context) error {
	repoPath, err := h.resolveRepoPath(c)
	if err != nil {
		return err
	}

	if err := h.app.GitPush(repoPath); err != nil {
		slog.Error("failed to push", "err", err, "repoPath", repoPath)
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "failed to push",
		})
	}

	return c.JSON(http.StatusOK, map[string]string{"status": "pushed"})
}
