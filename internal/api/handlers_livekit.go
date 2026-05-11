package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/namanchopra/jarvis/internal/config"

	"github.com/labstack/echo/v4"
)

// LiveKitProvider exposes the LiveKit credentials and room from the active
// config so this package never imports the main package directly.
type LiveKitProvider interface {
	GetConfig() (config.Config, error)
}

// liveKitTokenResponse is what the mobile client gets back from /livekit/token.
type liveKitTokenResponse struct {
	URL      string `json:"url"`
	Token    string `json:"token"`
	Room     string `json:"room"`
	Identity string `json:"identity"`
}

// RegisterLiveKitRoutes attaches /livekit/token to the given Echo group.
// The route requires Bearer auth (already enforced by the Server's middleware).
func RegisterLiveKitRoutes(g *echo.Group, app LiveKitProvider) {
	g.GET("/livekit/token", handleLiveKitToken(app))
}

// handleLiveKitToken mints a short-lived (1 hour) LiveKit JWT for the caller.
// Query params:
//   - identity (optional): client identity in the room, defaults to "phone"
//   - room (optional): override the configured default room
//
// The bot identity ("jarvis") is reserved for the daemon and rejected here.
func handleLiveKitToken(app LiveKitProvider) echo.HandlerFunc {
	return func(c echo.Context) error {
		cfg, err := app.GetConfig()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "config unavailable"})
		}
		if cfg.LiveKitAPIKey == "" || cfg.LiveKitAPISecret == "" || cfg.LiveKitURL == "" {
			return c.JSON(http.StatusServiceUnavailable, map[string]string{
				"error": "LiveKit not configured (set livekitUrl, livekitApiKey, livekitApiSecret)",
			})
		}

		identity := strings.TrimSpace(c.QueryParam("identity"))
		if identity == "" {
			identity = "phone"
		}
		if strings.EqualFold(identity, "jarvis") {
			return c.JSON(http.StatusForbidden, map[string]string{"error": "identity 'jarvis' is reserved"})
		}

		room := strings.TrimSpace(c.QueryParam("room"))
		if room == "" {
			room = cfg.LiveKitRoomName
		}
		if room == "" {
			room = "jarvis"
		}

		token, err := signLiveKitToken(cfg.LiveKitAPIKey, cfg.LiveKitAPISecret, identity, room, time.Hour)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to sign token"})
		}

		return c.JSON(http.StatusOK, liveKitTokenResponse{
			URL:      cfg.LiveKitURL,
			Token:    token,
			Room:     room,
			Identity: identity,
		})
	}
}

// signLiveKitToken builds a LiveKit-compatible HS256 JWT granting room-join
// + publish + subscribe rights to the given identity for `ttl` duration.
//
// LiveKit accepts any HS256 JWT signed with the project's API secret; the
// claims schema is documented at https://docs.livekit.io/reference/auth/.
func signLiveKitToken(apiKey, apiSecret, identity, room string, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := map[string]any{
		"iss":  apiKey,
		"sub":  identity,
		"nbf":  now.Unix(),
		"iat":  now.Unix(),
		"exp":  now.Add(ttl).Unix(),
		"name": identity,
		"video": map[string]any{
			"room":           room,
			"roomJoin":       true,
			"canPublish":     true,
			"canSubscribe":   true,
			"canPublishData": true,
		},
	}

	header := map[string]string{"alg": "HS256", "typ": "JWT"}
	headerBytes, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("marshal header: %w", err)
	}
	claimsBytes, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal claims: %w", err)
	}

	enc := base64.RawURLEncoding
	signingInput := enc.EncodeToString(headerBytes) + "." + enc.EncodeToString(claimsBytes)

	mac := hmac.New(sha256.New, []byte(apiSecret))
	mac.Write([]byte(signingInput))
	signature := enc.EncodeToString(mac.Sum(nil))

	return signingInput + "." + signature, nil
}
