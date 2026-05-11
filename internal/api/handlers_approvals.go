package api

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/namanchopra/jarvis/internal/model"

	"github.com/labstack/echo/v4"
)

// ApprovalProvider abstracts the App methods needed by approval handlers.
// This avoids importing the main package and prevents circular dependencies.
type ApprovalProvider interface {
	GetPendingApprovals() ([]model.ApprovalRequest, error)
	RespondToApproval(pid int, response string) error
}

// RegisterApprovalRoutes mounts approval-related endpoints on the provided
// Echo route group.
func RegisterApprovalRoutes(g *echo.Group, app ApprovalProvider) {
	h := &approvalHandler{app: app}

	g.GET("/approvals", h.list)
	g.POST("/approvals/:pid/respond", h.respond)
}

// approvalHandler holds the dependency needed to serve approval endpoints.
type approvalHandler struct {
	app ApprovalProvider
}

// respondRequest is the expected JSON body for POST /approvals/:pid/respond.
type respondRequest struct {
	Response string `json:"response"`
}

// list handles GET /approvals.
func (h *approvalHandler) list(c echo.Context) error {
	approvals, err := h.app.GetPendingApprovals()
	if err != nil {
		slog.Error("failed to get pending approvals", "err", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "failed to retrieve pending approvals",
		})
	}

	// Guarantee an empty JSON array rather than null.
	if approvals == nil {
		approvals = []model.ApprovalRequest{}
	}

	return c.JSON(http.StatusOK, approvals)
}

// respond handles POST /approvals/:pid/respond.
func (h *approvalHandler) respond(c echo.Context) error {
	// Parse :pid as an integer.
	pidStr := c.Param("pid")
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "pid must be a valid integer",
		})
	}

	// Decode and validate the JSON body.
	var req respondRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "invalid JSON body",
		})
	}

	if req.Response != "y" && req.Response != "n" {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "response must be \"y\" or \"n\"",
		})
	}

	// Forward the response to the approval provider.
	if err := h.app.RespondToApproval(pid, req.Response); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			return c.JSON(http.StatusNotFound, map[string]string{
				"error": "approval not found",
			})
		}
		slog.Error("failed to respond to approval", "err", err, "pid", pid)
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "failed to respond to approval",
		})
	}

	return c.JSON(http.StatusOK, map[string]bool{"ok": true})
}
