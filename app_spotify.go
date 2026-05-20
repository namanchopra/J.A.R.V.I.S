package main

// ---------------------------------------------------------------------------
// Spotify Wails bindings — TASK-001 skeleton.
//
// These are intentionally thin stubs so the React side, the Python daemon's
// tool registry (TASK-004), and the AppleScript driver (TASK-007) can wire
// up against a stable surface area before the OAuth + Web API + AppleScript
// implementations land in TASK-008 / TASK-009.
//
// Stubs return non-error placeholder values so the frontend can render
// "Connect Spotify" affordances and `wails generate module` produces the
// expected TS signatures. No OAuth, no AppleScript, no HTTP — those land
// in TASK-008 / TASK-009.
// ---------------------------------------------------------------------------

// SpotifySignIn kicks off the Spotify OAuth flow.
//
// Real impl in TASK-008/009: opens the user's browser to the Spotify
// authorize URL with PKCE parameters, blocks on a one-shot localhost
// callback, exchanges the auth code for tokens, persists into
// config.Config.Spotify.
//
// Stub for TASK-001 — returns a placeholder authorize URL so the frontend
// can call this binding without throwing while the rest of v0.3.0 lands.
func (a *App) SpotifySignIn() (string, error) {
	// stub for TASK-001 — real impl in TASK-008/009
	return "https://accounts.spotify.com/authorize?stub=true", nil
}

// SpotifySignOut clears the persisted Spotify credentials so a subsequent
// SpotifyIsConnected() returns false and any in-flight tool call returns
// ErrNotAuthenticated.
//
// Stub for TASK-001 — no-op success. Real impl in TASK-009 will zero out
// config.Config.Spotify and call config.Save.
func (a *App) SpotifySignOut() error {
	// stub for TASK-001 — real impl in TASK-008/009
	return nil
}

// SpotifyIsConnected reports whether the user has signed in to Spotify AND
// the stored access token is still fresh.
//
// Stub for TASK-001 — always returns false. Real impl in TASK-009 will
// inspect config.Get().Spotify.IsConnected() and ExpiresAt.
func (a *App) SpotifyIsConnected() bool {
	// stub for TASK-001 — real impl in TASK-008/009
	return false
}
