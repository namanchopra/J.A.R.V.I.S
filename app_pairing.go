package main

// ---------------------------------------------------------------------------
// Mobile pairing QR generator — TASK-025 of v0.3.0.
//
// The Friday phone companion (mobile/) bootstraps by scanning a QR code
// displayed in Settings → Connections on the Mac. Payload format:
//
//   jarvis://pair?host=<localIP>:<port>&token=<bearer>&room=<roomID>
//
// The phone's pair.tsx (TASK-020) decodes this URL, persists host + token in
// expo-secure-store, and connects to the existing /ws/jarvis-mobile endpoint
// using the stored bearer. The "room" field is reserved for future
// multi-room support (today it's a constant "jarvis").
//
// We keep the QR rendering server-side (Go) rather than client-side (npm
// `qrcode`) so:
//   1. There's no extra frontend dependency for a feature that only fires
//      in one modal.
//   2. The bearer token never crosses a React render boundary as a plain
//      string — it's baked into a PNG data URL on the Go side and only the
//      PNG bytes reach the renderer.
// ---------------------------------------------------------------------------

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"

	"github.com/namanchopra/jarvis/internal/config"
	qrcode "github.com/skip2/go-qrcode"
)

// GenerateMobilePairingQR returns a base64-encoded PNG QR code that the
// Friday app's pair.tsx scans to bootstrap. Payload is a jarvis://pair URL
// with host (LAN IP + mobile API port), Bearer token, and a room ID
// (currently "jarvis" — reserved for future multi-room support).
//
// The returned string is a `data:image/png;base64,<...>` URL ready to drop
// into an <img src=...> tag. Errors come from three places:
//   - the mobile API token is empty (user hasn't generated one yet — they
//     need to hit "Regenerate token" in Settings first)
//   - no non-loopback IPv4 interface is up (rare; happens on isolated dev
//     boxes — we fall back to 127.0.0.1 in localLANIP rather than failing,
//     so this branch is unreachable in practice)
//   - the QR encoder fails (effectively impossible for a short URL)
func (a *App) GenerateMobilePairingQR() (string, error) {
	cfg := config.Get()
	if cfg.MobileAPIToken == "" {
		return "", errors.New("mobile API token missing — regenerate via Settings")
	}
	ip, err := localLANIP()
	if err != nil {
		return "", fmt.Errorf("local IP: %w", err)
	}
	port := cfg.MobileAPIPort
	if port == 0 {
		port = 4422
	}
	url := fmt.Sprintf("jarvis://pair?host=%s:%d&token=%s&room=jarvis", ip, port, cfg.MobileAPIToken)
	qr, err := qrcode.New(url, qrcode.Medium)
	if err != nil {
		return "", fmt.Errorf("GenerateMobilePairingQR: encode: %w", err)
	}
	png, err := qr.PNG(256)
	if err != nil {
		return "", fmt.Errorf("GenerateMobilePairingQR: render: %w", err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png), nil
}

// localLANIP returns the first non-loopback IPv4 address on a UP interface.
// Used for the pairing QR so the phone knows where to connect.
//
// Falls back to 127.0.0.1 (rather than returning an error) when no LAN
// interface is found — this gracefully handles the isolated-dev-box case
// where Jarvis runs on the same machine as a simulator-based Friday client.
func localLANIP() (string, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", err
	}
	for _, addr := range addrs {
		ipnet, ok := addr.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() {
			continue
		}
		if ip4 := ipnet.IP.To4(); ip4 != nil {
			return ip4.String(), nil
		}
	}
	return "127.0.0.1", nil // fallback; user on isolated dev box
}
