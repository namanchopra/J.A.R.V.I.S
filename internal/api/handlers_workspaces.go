package api

import (
	"net/http"

	"github.com/namanchopra/jarvis/internal/store"
	"github.com/namanchopra/jarvis/internal/workspace"

	"github.com/labstack/echo/v4"
)

// WorkspaceProvider abstracts the App methods needed by workspace handlers,
// avoiding a direct import of the main package.
type WorkspaceProvider interface {
	ListWorkspaces() ([]workspace.Workspace, error)
	DeleteWorkspace(path string) error
	ListSavedProjects() ([]store.Project, error)
}

// RegisterWorkspaceRoutes mounts workspace-related endpoints onto the given
// Echo route group. The group should already have authentication middleware
// applied by the caller.
func RegisterWorkspaceRoutes(g *echo.Group, app WorkspaceProvider) {
	g.GET("/workspaces", handleListWorkspaces(app))
	g.DELETE("/workspaces/:id", handleDeleteWorkspace(app))
	g.GET("/saved-projects", handleListSavedProjects(app))
}

// handleListWorkspaces returns all virtual monorepo workspaces.
//
//	GET /workspaces -> 200 [{...}, ...]
func handleListWorkspaces(app WorkspaceProvider) echo.HandlerFunc {
	return func(c echo.Context) error {
		workspaces, err := app.ListWorkspaces()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": "failed to list workspaces",
			})
		}
		return c.JSON(http.StatusOK, workspaces)
	}
}

// handleDeleteWorkspace removes a workspace by its UUID.
//
//	DELETE /workspaces/:id -> 200 | 404
//
// The :id param is the workspace UUID (not the filesystem path). The handler
// lists all workspaces, finds the matching ID, and deletes by path.
func handleDeleteWorkspace(app WorkspaceProvider) echo.HandlerFunc {
	return func(c echo.Context) error {
		id := c.Param("id")
		if id == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"error": "workspace id is required",
			})
		}

		workspaces, err := app.ListWorkspaces()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": "failed to list workspaces",
			})
		}

		var targetPath string
		for _, w := range workspaces {
			if w.ID == id {
				targetPath = w.Path
				break
			}
		}

		if targetPath == "" {
			return c.JSON(http.StatusNotFound, map[string]string{
				"error": "workspace not found",
			})
		}

		if err := app.DeleteWorkspace(targetPath); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": "failed to delete workspace",
			})
		}

		return c.JSON(http.StatusOK, map[string]string{
			"status": "deleted",
		})
	}
}

// handleListSavedProjects returns all saved projects from the store.
//
//	GET /saved-projects -> 200 [{...}, ...]
func handleListSavedProjects(app WorkspaceProvider) echo.HandlerFunc {
	return func(c echo.Context) error {
		projects, err := app.ListSavedProjects()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": "failed to list saved projects",
			})
		}
		return c.JSON(http.StatusOK, projects)
	}
}
