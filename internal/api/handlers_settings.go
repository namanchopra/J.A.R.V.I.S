package api

import (
	"encoding/json"
	"net/http"

	"github.com/namanchopra/jarvis/internal/config"

	"github.com/labstack/echo/v4"
)

// SettingsProvider abstracts the application layer so this package never imports
// the main package directly. Implementors typically wrap config.Load / config.Save.
//
// SaveConfig returns a result carrying a "daemon restart needed?" hint plus
// an error. The mobile API doesn't act on the hint — there's no way to
// trigger a daemon restart from a phone — but the signature has to match
// the Wails binding on App so a single App struct can satisfy both layers.
type SettingsProvider interface {
	GetConfig() (config.Config, error)
	SaveConfig(cfg config.Config) (config.SaveResult, error)
}

// SettingsResponse is the public view of configuration. It deliberately excludes
// sensitive fields (MobileAPIToken) and server-internal fields (MobileAPIPort,
// DotClaudeSourcePath) to prevent accidental exposure over the network.
type SettingsResponse struct {
	DefaultAgent         string   `json:"defaultAgent"`
	ScanIntervalSeconds  int      `json:"scanIntervalSeconds"`
	PreferredTerminal    string   `json:"preferredTerminal"`
	ProjectRootPaths     []string `json:"projectRootPaths"`
	NotificationsEnabled bool     `json:"notificationsEnabled"`
	NotifyOnApproval     bool     `json:"notifyOnApproval"`
	NotifyOnCompletion   bool     `json:"notifyOnCompletion"`
	CIWatchEnabled       bool     `json:"ciWatchEnabled"`
	CIProvider           string   `json:"ciProvider"`
	DefaultCommand       string   `json:"defaultCommand"`
}

// toSettingsResponse converts a full config.Config to the safe public subset.
func toSettingsResponse(cfg config.Config) SettingsResponse {
	return SettingsResponse{
		DefaultAgent:         cfg.DefaultAgent,
		ScanIntervalSeconds:  cfg.ScanIntervalSeconds,
		PreferredTerminal:    cfg.PreferredTerminal,
		ProjectRootPaths:     cfg.ProjectRootPaths,
		NotificationsEnabled: cfg.NotificationsEnabled,
		NotifyOnApproval:     cfg.NotifyOnApproval,
		NotifyOnCompletion:   cfg.NotifyOnCompletion,
		CIWatchEnabled:       cfg.CIWatchEnabled,
		CIProvider:           cfg.CIProvider,
		DefaultCommand:       cfg.DefaultCommand,
	}
}

// RegisterSettingsRoutes mounts GET /settings and PUT /settings on the
// provided Echo route group.
func RegisterSettingsRoutes(g *echo.Group, app SettingsProvider) {
	h := &settingsHandler{app: app}
	g.GET("/settings", h.getSettings)
	g.PUT("/settings", h.putSettings)
}

type settingsHandler struct {
	app SettingsProvider
}

// getSettings returns the current configuration with sensitive fields stripped.
func (h *settingsHandler) getSettings(c echo.Context) error {
	cfg, err := h.app.GetConfig()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "failed to load settings",
		})
	}
	return c.JSON(http.StatusOK, toSettingsResponse(cfg))
}

// settingsUpdateRequest mirrors SettingsResponse but uses pointer fields so we
// can distinguish between "field not provided" (nil) and "field set to zero
// value" (non-nil pointer to zero). This lets PUT /settings act as a partial
// update — only fields present in the JSON body are applied.
type settingsUpdateRequest struct {
	DefaultAgent         *string  `json:"defaultAgent"`
	ScanIntervalSeconds  *int     `json:"scanIntervalSeconds"`
	PreferredTerminal    *string  `json:"preferredTerminal"`
	ProjectRootPaths     []string `json:"projectRootPaths"`
	NotificationsEnabled *bool    `json:"notificationsEnabled"`
	NotifyOnApproval     *bool    `json:"notifyOnApproval"`
	NotifyOnCompletion   *bool    `json:"notifyOnCompletion"`
	CIWatchEnabled       *bool    `json:"ciWatchEnabled"`
	CIProvider           *string  `json:"ciProvider"`
	DefaultCommand       *string  `json:"defaultCommand"`
}

// putSettings accepts a partial JSON body and overlays the provided fields onto
// the current config. Fields not present in the request body are left unchanged.
// Sensitive/internal fields (MobileAPIToken, MobileAPIPort, DotClaudeSourcePath)
// cannot be set through this endpoint.
func (h *settingsHandler) putSettings(c echo.Context) error {
	// Decode the request body. Unknown fields are silently ignored (the default
	// behaviour of json.Decoder without DisallowUnknownFields).
	var req settingsUpdateRequest
	dec := json.NewDecoder(c.Request().Body)
	if err := dec.Decode(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "invalid JSON body",
		})
	}

	// Load the current full config.
	cfg, err := h.app.GetConfig()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "failed to load settings",
		})
	}

	// Overlay only the fields that were explicitly provided.
	if req.DefaultAgent != nil {
		cfg.DefaultAgent = *req.DefaultAgent
	}
	if req.ScanIntervalSeconds != nil {
		cfg.ScanIntervalSeconds = *req.ScanIntervalSeconds
	}
	if req.PreferredTerminal != nil {
		cfg.PreferredTerminal = *req.PreferredTerminal
	}
	if req.ProjectRootPaths != nil {
		cfg.ProjectRootPaths = req.ProjectRootPaths
	}
	if req.NotificationsEnabled != nil {
		cfg.NotificationsEnabled = *req.NotificationsEnabled
	}
	if req.NotifyOnApproval != nil {
		cfg.NotifyOnApproval = *req.NotifyOnApproval
	}
	if req.NotifyOnCompletion != nil {
		cfg.NotifyOnCompletion = *req.NotifyOnCompletion
	}
	if req.CIWatchEnabled != nil {
		cfg.CIWatchEnabled = *req.CIWatchEnabled
	}
	if req.CIProvider != nil {
		cfg.CIProvider = *req.CIProvider
	}
	if req.DefaultCommand != nil {
		cfg.DefaultCommand = *req.DefaultCommand
	}

	// Persist the updated config. The mobile API ignores the
	// DaemonRestartNeeded hint because there's no UX surface on the phone
	// to trigger a restart — only the desktop frontend acts on it.
	if _, err := h.app.SaveConfig(cfg); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "failed to save settings",
		})
	}

	// Return the safe subset — never the full config.
	return c.JSON(http.StatusOK, toSettingsResponse(cfg))
}
