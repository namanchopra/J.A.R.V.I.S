// Package api provides a lightweight HTTP server for mobile clients to interact
// with AWM over the local network. It embeds an Echo server with Bearer token
// authentication and CORS support.
package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

// Server is the mobile API HTTP server. It is safe for concurrent use.
type Server struct {
	echo                 *echo.Echo
	token                atomic.Value // stores string for lock-free hot-swap
	port                 int
	pushHandler          *PushHandler
	jarvisConn           *JarvisDaemonConn // active Jarvis daemon WebSocket (may be nil)
	mobileBroadcaster    *MobileBroadcaster
	stopStatsBroadcaster func() // stop fn for the periodic stats push goroutine
}

// JarvisConn returns the active Jarvis daemon WebSocket connection wrapper, or nil
// if WireRoutes has not been called yet.
func (s *Server) JarvisConn() *JarvisDaemonConn {
	return s.jarvisConn
}

// PushHandler exposes the registered push notification handler, or nil if
// WireRoutes has not been called yet. Used by the Wails-bound
// JarvisSendTestPush binding (app_push.go) to fan a manual test push out to
// every registered device. Safe to call from any goroutine -- callers must
// nil-check before invoking handler methods.
func (s *Server) PushHandler() *PushHandler {
	return s.pushHandler
}

// New creates a new Server that will listen on the given port and require the
// provided Bearer token for authentication. Call Start to begin serving.
func New(port int, token string) *Server {
	s := &Server{
		echo: echo.New(),
		port: port,
	}
	s.token.Store(token)

	// Silence Echo's built-in banner and logger — we use slog instead.
	s.echo.HideBanner = true
	s.echo.HidePort = true

	s.registerRoutes()
	return s
}

// Start begins serving HTTP requests in a background goroutine. It returns
// immediately. If the server fails to bind (e.g. port in use), the error is
// logged but not returned — the caller should treat this as non-fatal.
func (s *Server) Start() error {
	addr := fmt.Sprintf(":%d", s.port)

	go func() {
		slog.Info("mobile API server starting", "addr", addr)
		if err := s.echo.Start(addr); err != nil && err != http.ErrServerClosed {
			slog.Warn("mobile API server failed", "err", err, "addr", addr)
		}
	}()

	return nil
}

// Shutdown gracefully drains in-flight requests and stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	slog.Info("mobile API server shutting down")
	return s.echo.Shutdown(ctx)
}

// UpdateToken hot-swaps the Bearer token used for authentication. In-flight
// requests that already passed the middleware are unaffected; subsequent
// requests will use the new token.
func (s *Server) UpdateToken(newToken string) {
	s.token.Store(newToken)
	slog.Info("mobile API token updated")
}

// registerRoutes configures middleware and routes on the Echo instance.
func (s *Server) registerRoutes() {
	e := s.echo

	// CORS — allow all origins so Expo dev clients can connect freely.
	e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
		AllowOrigins: []string{"*"},
		AllowMethods: []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodOptions},
		AllowHeaders: []string{echo.HeaderAuthorization, echo.HeaderContentType},
	}))

	// Bearer token authentication.
	e.Use(s.bearerAuth)

	// Routes.
	e.GET("/ping", s.handlePing)
}

// bearerAuth is Echo middleware that validates the Authorization header against
// the current token. The token is read via atomic.Value for lock-free access,
// allowing hot-swaps without restarting the server.
func (s *Server) bearerAuth(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		// Let CORS preflight through without auth.
		if c.Request().Method == http.MethodOptions {
			return next(c)
		}

		// Skip auth for WebSocket endpoints that handle their own ?token= auth
		// (e.g. /ws/jarvis-mobile, /ws/sessions/:id/output) and for the
		// local-only Jarvis daemon endpoint (/ws/jarvis).
		path := c.Request().URL.Path
		if IsJarvisWSPath(path) || strings.HasPrefix(path, "/ws/") {
			return next(c)
		}

		expected := s.token.Load().(string)
		if expected == "" {
			// No token configured — reject all requests as a safety measure.
			slog.Warn("mobile API request rejected: no token configured")
			return c.JSON(http.StatusUnauthorized, map[string]string{
				"error": "server has no API token configured",
			})
		}

		auth := c.Request().Header.Get("Authorization")
		if auth == "" {
			return c.JSON(http.StatusUnauthorized, map[string]string{
				"error": "missing Authorization header",
			})
		}

		// Expect "Bearer <token>".
		const prefix = "Bearer "
		if !strings.HasPrefix(auth, prefix) {
			return c.JSON(http.StatusUnauthorized, map[string]string{
				"error": "invalid Authorization format, expected Bearer token",
			})
		}

		provided := strings.TrimPrefix(auth, prefix)
		if provided != expected {
			return c.JSON(http.StatusUnauthorized, map[string]string{
				"error": "invalid token",
			})
		}

		return next(c)
	}
}

// handlePing is a simple health-check endpoint.
func (s *Server) handlePing(c echo.Context) error {
	return c.JSON(http.StatusOK, map[string]bool{"ok": true})
}

// WireRoutes connects all API handler packages to the Echo router.
// Call this after New() and before Start(). The App struct in the main package
// satisfies all provider interfaces — pass it for every argument.
//
// jarvisEmitFn is called when the Jarvis daemon sends events over its WebSocket.
// Pass nil to disable the Jarvis WS endpoint (the route is still registered
// but events are silently dropped).
func (s *Server) WireRoutes(
	sessions SessionProvider,
	dashboard DashboardProvider,
	workspaces WorkspaceProvider,
	approvals ApprovalProvider,
	settings SettingsProvider,
	terminal TerminalProvider,
	push PushProvider,
	repos RepoProvider,
	repoResolve RepoPathResolver,
	jarvisEmitFn JarvisEventEmitter,
	stats StatsProvider,
	calendar CalendarProvider,
) {
	g := s.echo.Group("")
	RegisterSessionRoutes(g, sessions)
	RegisterDashboardRoutes(g, dashboard)
	RegisterWorkspaceRoutes(g, workspaces)
	RegisterApprovalRoutes(g, approvals)
	RegisterSettingsRoutes(g, settings)
	RegisterRepoRoutes(g, repos, repoResolve)
	RegisterLiveKitRoutes(g, settings)
	RegisterCalendarRoutes(g, calendar)

	tokenFn := func() string {
		t, _ := s.token.Load().(string)
		return t
	}
	RegisterWSRoutes(g, terminal, tokenFn)
	s.pushHandler = RegisterPushRoutes(g, push)

	// Wrap the Jarvis emitter so daemon events are forwarded to both the
	// Wails frontend (via the original emitFn) and connected mobile clients.
	wrappedEmit := JarvisEventEmitter(func(event interface{}) {
		jarvisEmitFn(event)
		if s.mobileBroadcaster != nil {
			s.mobileBroadcaster.HandleDaemonEvent(event)
		}
	})

	// Jarvis daemon WebSocket — no auth, local daemon only.
	s.jarvisConn = RegisterJarvisWSRoute(g, wrappedEmit)

	// Jarvis mobile WebSocket — auth via ?token= query param.
	s.mobileBroadcaster = RegisterJarvisMobileWSRoute(g, tokenFn, s.jarvisConn)

	// Periodic dashboard-stats push for mobile clients. iOS Expo Go's
	// ATS blocks plain http:// fetches from RN so REST polling fails;
	// pushing the snapshot over the already-authorised WS sidesteps it
	// entirely. 5s cadence matches the Mac HUD's poll loop.
	if stats != nil {
		s.stopStatsBroadcaster = s.mobileBroadcaster.StartStatsBroadcaster(
			stats, 5*time.Second,
		)
	}

	// Jarvis chat endpoint — uses the daemon WS for request/response.
	RegisterJarvisChatRoute(g, s.jarvisConn)
}

// StartPoller starts the background push notification poller if push routes
// have been wired. It returns immediately. The poller stops when ctx is
// cancelled.
func (s *Server) StartPoller(ctx context.Context) {
	if s.pushHandler != nil {
		s.pushHandler.StartPushPoller(ctx)
	}
}
