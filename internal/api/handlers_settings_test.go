package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/namanchopra/jarvis/internal/config"

	"github.com/labstack/echo/v4"
)

// --- Mock implementation of SettingsProvider ---

type mockSettingsProvider struct {
	cfg     config.Config
	loadErr error
	saveErr error
	saved   *config.Config // captures the last config passed to SaveConfig
}

func (m *mockSettingsProvider) GetConfig() (config.Config, error) {
	if m.loadErr != nil {
		return config.Config{}, m.loadErr
	}
	return m.cfg, nil
}

func (m *mockSettingsProvider) SaveConfig(cfg config.Config) (config.SaveResult, error) {
	if m.saveErr != nil {
		return config.SaveResult{}, m.saveErr
	}
	m.cfg = cfg
	m.saved = &cfg
	return config.SaveResult{}, nil
}

// --- Helpers ---

// setupEcho creates a fresh Echo instance with the settings routes mounted on
// a group. It returns the Echo instance for use with httptest.
func setupEcho(provider SettingsProvider) *echo.Echo {
	e := echo.New()
	g := e.Group("")
	RegisterSettingsRoutes(g, provider)
	return e
}

// --- Tests ---

func TestGetSettingsNeverExposesToken(t *testing.T) {
	secret := "super-secret-mobile-token-12345"
	mock := &mockSettingsProvider{
		cfg: config.Config{
			DefaultAgent:         "claude-code",
			ScanIntervalSeconds:  5,
			NotificationsEnabled: true,
			NotifyOnApproval:     true,
			NotifyOnCompletion:   true,
			DefaultCommand:       "claude",
			MobileAPIPort:        4422,
			MobileAPIToken:       secret,
		},
	}

	e := setupEcho(mock)
	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	body := rec.Body.String()

	// The token string must never appear anywhere in the response body.
	if strings.Contains(body, secret) {
		t.Fatal("response body contains MobileAPIToken — token leaked!")
	}

	// The JSON keys themselves must not be present either.
	if strings.Contains(body, "mobileAPIToken") {
		t.Fatal("response body contains mobileAPIToken key")
	}
	if strings.Contains(body, "mobileAPIPort") {
		t.Fatal("response body contains mobileAPIPort key")
	}
	if strings.Contains(body, "dotClaudeSourcePath") {
		t.Fatal("response body contains dotClaudeSourcePath key")
	}

	// Verify we can decode the response into SettingsResponse.
	var resp SettingsResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if resp.DefaultAgent != "claude-code" {
		t.Errorf("expected defaultAgent 'claude-code', got %q", resp.DefaultAgent)
	}
}

func TestGetSettingsReturnsAllPublicFields(t *testing.T) {
	mock := &mockSettingsProvider{
		cfg: config.Config{
			DefaultAgent:         "cursor",
			ScanIntervalSeconds:  10,
			PreferredTerminal:    "iterm2",
			ProjectRootPaths:     []string{"/home/user/projects", "/tmp/work"},
			NotificationsEnabled: false,
			NotifyOnApproval:     false,
			NotifyOnCompletion:   true,
			CIWatchEnabled:       true,
			CIProvider:           "github-actions",
			DefaultCommand:       "aider",
			MobileAPIToken:       "should-not-appear",
			MobileAPIPort:        9999,
		},
	}

	e := setupEcho(mock)
	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var resp SettingsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}

	if resp.DefaultAgent != "cursor" {
		t.Errorf("DefaultAgent: want %q, got %q", "cursor", resp.DefaultAgent)
	}
	if resp.ScanIntervalSeconds != 10 {
		t.Errorf("ScanIntervalSeconds: want 10, got %d", resp.ScanIntervalSeconds)
	}
	if resp.PreferredTerminal != "iterm2" {
		t.Errorf("PreferredTerminal: want %q, got %q", "iterm2", resp.PreferredTerminal)
	}
	if len(resp.ProjectRootPaths) != 2 {
		t.Errorf("ProjectRootPaths: want len 2, got %d", len(resp.ProjectRootPaths))
	}
	if resp.NotificationsEnabled != false {
		t.Error("NotificationsEnabled: want false, got true")
	}
	if resp.CIWatchEnabled != true {
		t.Error("CIWatchEnabled: want true, got false")
	}
	if resp.CIProvider != "github-actions" {
		t.Errorf("CIProvider: want %q, got %q", "github-actions", resp.CIProvider)
	}
	if resp.DefaultCommand != "aider" {
		t.Errorf("DefaultCommand: want %q, got %q", "aider", resp.DefaultCommand)
	}
}

func TestPutSettingsPartialUpdate(t *testing.T) {
	mock := &mockSettingsProvider{
		cfg: config.Config{
			DefaultAgent:         "claude-code",
			ScanIntervalSeconds:  5,
			NotificationsEnabled: true,
			NotifyOnApproval:     true,
			NotifyOnCompletion:   true,
			DefaultCommand:       "claude",
			MobileAPIToken:       "keep-this-secret",
			MobileAPIPort:        4422,
		},
	}

	e := setupEcho(mock)

	// Only update two fields.
	body := `{"defaultAgent":"cursor","scanIntervalSeconds":30}`
	req := httptest.NewRequest(http.MethodPut, "/settings", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	// Verify the response reflects the update.
	var resp SettingsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.DefaultAgent != "cursor" {
		t.Errorf("DefaultAgent: want %q, got %q", "cursor", resp.DefaultAgent)
	}
	if resp.ScanIntervalSeconds != 30 {
		t.Errorf("ScanIntervalSeconds: want 30, got %d", resp.ScanIntervalSeconds)
	}

	// Fields not in the request must remain unchanged.
	if resp.NotificationsEnabled != true {
		t.Error("NotificationsEnabled should remain true")
	}
	if resp.DefaultCommand != "claude" {
		t.Errorf("DefaultCommand should remain %q, got %q", "claude", resp.DefaultCommand)
	}

	// The saved config must preserve the token (not wiped by the handler).
	if mock.saved == nil {
		t.Fatal("SaveConfig was not called")
	}
	if mock.saved.MobileAPIToken != "keep-this-secret" {
		t.Errorf("MobileAPIToken was mutated: got %q", mock.saved.MobileAPIToken)
	}
	if mock.saved.MobileAPIPort != 4422 {
		t.Errorf("MobileAPIPort was mutated: got %d", mock.saved.MobileAPIPort)
	}

	// The response must not leak the token.
	respBody := rec.Body.String()
	if strings.Contains(respBody, "keep-this-secret") {
		t.Fatal("PUT response leaks MobileAPIToken")
	}
}

func TestPutSettingsUnknownFieldsIgnored(t *testing.T) {
	mock := &mockSettingsProvider{
		cfg: config.Config{
			DefaultAgent:   "claude-code",
			DefaultCommand: "claude",
		},
	}

	e := setupEcho(mock)

	// Include unknown fields — they should be silently ignored.
	body := `{"defaultAgent":"cursor","mobileAPIToken":"hacked","unknownField":42}`
	req := httptest.NewRequest(http.MethodPut, "/settings", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	// The token must NOT have been overwritten by the "mobileAPIToken" in the body,
	// because settingsUpdateRequest does not have that field.
	if mock.saved != nil && mock.saved.MobileAPIToken == "hacked" {
		t.Fatal("mobileAPIToken was overwritten via PUT — security breach!")
	}
}

func TestPutSettingsInvalidJSON(t *testing.T) {
	mock := &mockSettingsProvider{
		cfg: config.Config{},
	}

	e := setupEcho(mock)

	req := httptest.NewRequest(http.MethodPut, "/settings", strings.NewReader("{invalid"))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", rec.Code)
	}

	var errResp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if errResp["error"] == "" {
		t.Error("expected non-empty error message")
	}
}

func TestGetSettingsLoadError(t *testing.T) {
	mock := &mockSettingsProvider{
		loadErr: http.ErrServerClosed, // any non-nil error
	}

	e := setupEcho(mock)
	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", rec.Code)
	}
}

func TestPutSettingsBooleanZeroValues(t *testing.T) {
	// Verify that explicitly setting a boolean to false actually persists,
	// rather than being treated as "not provided".
	mock := &mockSettingsProvider{
		cfg: config.Config{
			NotificationsEnabled: true,
			NotifyOnApproval:     true,
			NotifyOnCompletion:   true,
			CIWatchEnabled:       true,
		},
	}

	e := setupEcho(mock)

	body := `{"notificationsEnabled":false,"ciWatchEnabled":false}`
	req := httptest.NewRequest(http.MethodPut, "/settings", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var resp SettingsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}

	if resp.NotificationsEnabled != false {
		t.Error("NotificationsEnabled: want false, got true — zero-value bool not applied")
	}
	if resp.CIWatchEnabled != false {
		t.Error("CIWatchEnabled: want false, got true — zero-value bool not applied")
	}
	// Unchanged fields should stay true.
	if resp.NotifyOnApproval != true {
		t.Error("NotifyOnApproval should remain true")
	}
	if resp.NotifyOnCompletion != true {
		t.Error("NotifyOnCompletion should remain true")
	}
}
