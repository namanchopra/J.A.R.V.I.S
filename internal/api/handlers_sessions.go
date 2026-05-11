package api

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/namanchopra/jarvis/internal/model"

	"github.com/labstack/echo/v4"
)

// SessionProvider abstracts the App methods needed by session handlers.
// This avoids importing the main package.
type SessionProvider interface {
	ListSessions(statusFilter string) ([]model.Session, error)
	GetSession(sessionID string) (model.Session, error)
	StopSession(sessionID string) error
	LaunchSession(agentType, repoPath, prompt string) (model.Session, error)
	SendCommandToSession(pid int, command string) error
}

// RegisterSessionRoutes mounts session-related endpoints on the provided
// Echo route group.
func RegisterSessionRoutes(g *echo.Group, app SessionProvider) {
	h := &sessionHandler{app: app}

	g.GET("/sessions", h.list)
	g.GET("/sessions/:id", h.get)
	g.POST("/sessions", h.launch)
	g.POST("/sessions/:id/stop", h.stop)
	g.POST("/sessions/:id/send", h.send)
}

// sessionHandler holds the dependency needed to serve session endpoints.
type sessionHandler struct {
	app SessionProvider
}

// list handles GET /sessions?status=<filter>.
func (h *sessionHandler) list(c echo.Context) error {
	status := strings.TrimSpace(c.QueryParam("status"))

	// Validate the status filter if one was provided.
	if status != "" && !model.ValidSessionStatus(model.SessionStatus(status)) {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "invalid status filter: " + status,
		})
	}

	sessions, err := h.app.ListSessions(status)
	if err != nil {
		slog.Error("failed to list sessions", "err", err, "status", status)
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "failed to list sessions",
		})
	}

	return c.JSON(http.StatusOK, sessions)
}

// get handles GET /sessions/:id.
func (h *sessionHandler) get(c echo.Context) error {
	id := c.Param("id")

	sess, err := h.app.GetSession(id)
	if err != nil {
		if isNotFound(err) {
			return c.JSON(http.StatusNotFound, map[string]string{
				"error": "session not found",
			})
		}
		slog.Error("failed to get session", "err", err, "session_id", id)
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "failed to get session",
		})
	}

	return c.JSON(http.StatusOK, sess)
}

// stop handles POST /sessions/:id/stop.
func (h *sessionHandler) stop(c echo.Context) error {
	id := c.Param("id")

	if err := h.app.StopSession(id); err != nil {
		if isNotFound(err) || isNotActive(err) {
			return c.JSON(http.StatusNotFound, map[string]string{
				"error": "session not found or not active",
			})
		}
		slog.Error("failed to stop session", "err", err, "session_id", id)
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "failed to stop session",
		})
	}

	return c.JSON(http.StatusOK, map[string]string{
		"status": "stopped",
	})
}

// launch handles POST /sessions.
//
// Request body:
//
//	{
//	  "agentType": "claude-code",
//	  "repoPath":  "/path/to/repo",
//	  "prompt":    "implement feature X"
//	}
//
// Returns the newly created session on success (201 Created).
func (h *sessionHandler) launch(c echo.Context) error {
	var req struct {
		AgentType string `json:"agentType"`
		RepoPath  string `json:"repoPath"`
		Prompt    string `json:"prompt"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "invalid request body",
		})
	}

	req.AgentType = strings.TrimSpace(req.AgentType)
	req.RepoPath = strings.TrimSpace(req.RepoPath)
	req.Prompt = strings.TrimSpace(req.Prompt)

	if req.AgentType == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "agentType is required",
		})
	}
	if req.RepoPath == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "repoPath is required",
		})
	}
	if req.Prompt == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "prompt is required",
		})
	}

	// Validate that repoPath is an absolute filesystem path.
	if !filepath.IsAbs(req.RepoPath) {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "repoPath must be an absolute path",
		})
	}

	// Validate that agentType is a recognised value.
	if !model.ValidAgentType(model.AgentType(req.AgentType)) {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "invalid agentType: " + req.AgentType,
		})
	}

	sess, err := h.app.LaunchSession(req.AgentType, req.RepoPath, req.Prompt)
	if err != nil {
		slog.Error("failed to launch session", "err", err,
			"agentType", req.AgentType, "repoPath", req.RepoPath)
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "failed to launch session",
		})
	}

	return c.JSON(http.StatusCreated, sess)
}

// send handles POST /sessions/:id/send.
//
// Request body:
//
//	{"command": "ls -la"}
//
// Looks up the session by ID to obtain its PID, then sends the command
// to the terminal running the session.
func (h *sessionHandler) send(c echo.Context) error {
	id := c.Param("id")

	var req struct {
		Command string `json:"command"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "invalid request body",
		})
	}
	req.Command = strings.TrimSpace(req.Command)
	if req.Command == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "command is required",
		})
	}

	// Look up the session to get its PID.
	sess, err := h.app.GetSession(id)
	if err != nil {
		if isNotFound(err) {
			return c.JSON(http.StatusNotFound, map[string]string{
				"error": "session not found",
			})
		}
		slog.Error("failed to get session for send", "err", err, "session_id", id)
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "failed to get session",
		})
	}

	if sess.PID <= 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "session has no active PID",
		})
	}

	if err := h.app.SendCommandToSession(sess.PID, req.Command); err != nil {
		slog.Error("failed to send command to session", "err", err,
			"session_id", id, "pid", sess.PID)
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "failed to send command to session",
		})
	}

	return c.JSON(http.StatusOK, map[string]string{
		"status": "sent",
	})
}

// isNotFound checks whether the error chain contains sql.ErrNoRows, which the
// store layer wraps when a record does not exist.
func isNotFound(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}

// isNotActive checks whether the error indicates the session is not in the
// active set (returned by the session manager's Stop method).
func isNotActive(err error) bool {
	return strings.Contains(err.Error(), "is not active")
}
