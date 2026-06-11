//go:build windows

package notify

import (
	"fmt"
	"log/slog"
	"strings"

	toast "git.sr.ht/~jackmordaunt/go-toast/v2"
)

// appID is the AppUserModelID shown in Windows Action Center groupings.
// We use the human-friendly product name so notifications group correctly
// regardless of whether SetAppData has been called by an earlier subsystem.
const appID = "Jarvis"

// Send delivers a Windows toast notification through the WinRT COM APIs with
// an automatic PowerShell fallback (handles Win10 / Server SKUs where the
// modern WinRT path is unavailable). It mirrors the macOS Send signature so
// the shared notify.NotificationManager stays platform-agnostic.
func Send(title, message string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return fmt.Errorf("Send: title is required")
	}

	n := toast.Notification{
		AppID: appID,
		Title: title,
		Body:  message,
	}

	if err := n.Push(); err != nil {
		// The library tries COM first and PowerShell second; if both fail
		// it is almost always because Windows Runtime is unavailable
		// (Server SKU, Sandbox, etc.). Treat that as a soft failure so
		// the caller doesn't crash a 24-hour daemon over a missing toast.
		slog.Warn("notify: toast push failed", "err", err, "title", title)
		return nil
	}
	return nil
}
