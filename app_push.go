package main

import (
	"fmt"
)

// ---------------------------------------------------------------------------
// app_push.go -- Wails bindings for the push-notification surface (TASK-028).
// ---------------------------------------------------------------------------
// The mobile push poller is wired in internal/api/push.go and already handles
// approval-needed alerts, session lifecycle transitions, and impact warnings.
// This file adds a single manual-trigger binding the frontend can call from
// the Settings -> Permissions panel ("Test push" button) so a maintainer can
// verify end-to-end push delivery without staging a real session event.
//
// All other push-related logic (token storage, poller, Expo HTTP client)
// lives in internal/api/push.go. This file is intentionally thin: it
// resolves the api.Server -> api.PushHandler hop, fans the test message out
// to every registered Expo push token, and bubbles a human-readable status
// string back to the frontend toast.
// ---------------------------------------------------------------------------

// JarvisSendTestPush sends a test notification to every Expo push token the
// Mac has registered (one per paired Friday device). Returns a short status
// string the frontend can display verbatim in a toast.
//
// Failure modes (all returned as a non-nil error):
//   - apiServer is nil (mobile API failed to start at boot)
//   - PushHandler is nil (WireRoutes not yet called)
//   - No registered push tokens (nothing paired yet)
//
// A partial fan-out (some sends succeeded, others failed) is treated as
// success -- the count in the status string makes the discrepancy visible.
func (a *App) JarvisSendTestPush() (string, error) {
	if a.apiServer == nil {
		return "", fmt.Errorf("JarvisSendTestPush: mobile API server not running")
	}
	ph := a.apiServer.PushHandler()
	if ph == nil {
		return "", fmt.Errorf("JarvisSendTestPush: push handler not initialised")
	}

	tokens := ph.PushTokens()
	if len(tokens) == 0 {
		return "", fmt.Errorf("JarvisSendTestPush: no push tokens registered -- pair Friday first")
	}

	sent := ph.SendExpoPushToAll(
		"Jarvis test",
		"Push notifications are working.",
		map[string]interface{}{
			// `screen: "orb"` lets the mobile-side tap handler route the user
			// back to the orb root if/when it grows a tap-routing layer. For
			// now this is informational; the orb is the only screen anyway.
			"screen": "orb",
			"kind":   "test",
		},
	)

	if sent == 0 {
		return "", fmt.Errorf("JarvisSendTestPush: all %d send(s) to Expo failed -- check Mac network", len(tokens))
	}

	if sent == len(tokens) {
		return fmt.Sprintf("Sent test push to %d device(s).", sent), nil
	}
	// Partial success -- still a success outcome but worth surfacing the gap.
	return fmt.Sprintf("Sent test push to %d of %d device(s).", sent, len(tokens)), nil
}
