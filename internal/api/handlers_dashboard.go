package api

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/namanchopra/jarvis/internal/claude"
	"github.com/namanchopra/jarvis/internal/model"

	"github.com/labstack/echo/v4"
)

// DashboardProvider abstracts the App methods needed by dashboard handlers.
// This avoids importing the main package and prevents circular dependencies.
type DashboardProvider interface {
	GetDashboardStats() (model.DashboardStats, error)
	GetActiveSessions() ([]model.Session, error)
	GetRunningTasks() ([]model.Task, error)
	GetTask(id string) (model.Task, error)
	GetActivityFeed(limit int, beforeID string) ([]model.ActivityEvent, error)
	GetSessionIndicators() ([]claude.SessionIndicator, error)
}

// RegisterDashboardRoutes mounts the dashboard, activity, indicator, and task
// endpoints onto the provided Echo route group.
func RegisterDashboardRoutes(g *echo.Group, app DashboardProvider) {
	h := &dashboardHandler{app: app}

	g.GET("/dashboard", h.handleDashboard)
	g.GET("/activity", h.handleActivity)
	g.GET("/indicators", h.handleIndicators)
	g.GET("/tasks/:id", h.handleGetTask)
}

// dashboardHandler holds the dependency needed by the dashboard endpoints.
type dashboardHandler struct {
	app DashboardProvider
}

// dashboardResponse is the JSON shape returned by GET /dashboard.
type dashboardResponse struct {
	Stats          model.DashboardStats `json:"stats"`
	ActiveSessions []model.Session      `json:"activeSessions"`
	ActiveTasks    []model.Task         `json:"activeTasks"`
}

// handleDashboard returns aggregate task stats and currently active sessions.
func (h *dashboardHandler) handleDashboard(c echo.Context) error {
	stats, err := h.app.GetDashboardStats()
	if err != nil {
		slog.Error("failed to get dashboard stats", "err", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "failed to retrieve dashboard stats",
		})
	}

	sessions, err := h.app.GetActiveSessions()
	if err != nil {
		slog.Error("failed to get active sessions", "err", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "failed to retrieve active sessions",
		})
	}

	tasks, err := h.app.GetRunningTasks()
	if err != nil {
		slog.Error("failed to get running tasks", "err", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "failed to retrieve running tasks",
		})
	}

	return c.JSON(http.StatusOK, dashboardResponse{
		Stats:          stats,
		ActiveSessions: sessions,
		ActiveTasks:    tasks,
	})
}

// handleActivity returns a paginated activity feed using cursor-based
// pagination. Query params:
//   - limit:     number of events to return (default 20, max 100)
//   - before_id: cursor — return events older than this event ID (empty = first page)
func (h *dashboardHandler) handleActivity(c echo.Context) error {
	// Parse limit.
	limit := 20
	if raw := c.QueryParam("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"error": "limit must be a number",
			})
		}
		if parsed < 1 {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"error": "limit must be at least 1",
			})
		}
		if parsed > 100 {
			parsed = 100
		}
		limit = parsed
	}

	beforeID := c.QueryParam("before_id")

	events, err := h.app.GetActivityFeed(limit, beforeID)
	if err != nil {
		slog.Error("failed to get activity feed", "err", err, "limit", limit, "before_id", beforeID)
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "failed to retrieve activity feed",
		})
	}

	return c.JSON(http.StatusOK, events)
}

// handleIndicators returns enriched session indicators for all active Claude
// Code sessions, including heuristic activity state.
func (h *dashboardHandler) handleIndicators(c echo.Context) error {
	indicators, err := h.app.GetSessionIndicators()
	if err != nil {
		slog.Error("failed to get session indicators", "err", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "failed to retrieve session indicators",
		})
	}

	return c.JSON(http.StatusOK, indicators)
}

// handleGetTask returns a single task by its ID.
//
//	GET /tasks/:id -> 200 {task} | 404
func (h *dashboardHandler) handleGetTask(c echo.Context) error {
	id := c.Param("id")
	task, err := h.app.GetTask(id)
	if err != nil {
		slog.Error("failed to get task", "err", err, "task_id", id)
		return c.JSON(http.StatusNotFound, map[string]string{
			"error": "task not found",
		})
	}
	return c.JSON(http.StatusOK, task)
}
