// app_update_check.go — TASK-062 (Auto-update path Windows).
//
// Wires up a GitHub-Releases-based update check that the frontend calls on
// launch. The binding returns a `UpdateCheckResult` describing whether a
// newer version is available, what the latest tag is, and the release-page
// URL the user should be sent to (we do *not* auto-apply updates — the user
// downloads the .exe / .dmg manually).
//
// Why a manual check rather than auto-install:
//
//	The Windows port (v0.4.0+) ships an Inno Setup .exe and the macOS port
//	a notarized .dmg. Neither installer supports silent self-update from
//	inside the running app — and writing an auto-applier that survives
//	WebView2 / signing / antivirus quirks on Windows is a much bigger
//	project than this task. Showing a banner with a release-notes link is
//	the simplest correct UX.
//
// Reuse of the website's compare logic:
//
//	`website/app/page.tsx` ships a tiny `semverCompare` helper that compares
//	MAJOR.MINOR.PATCH tags (no -rc / -beta). The implementation here mirrors
//	that algorithm exactly so the homepage banner and the in-app banner
//	agree on "is there a newer tag?". Both deliberately skip pre-release
//	suffixes because Jarvis only ships clean semver tags.
//
// Failure handling (acceptance: "banner stays hidden" when offline /
// rate-limited):
//
//	CheckForUpdate never returns an error. Any of the following failure
//	modes return `UpdateCheckResult{Available: false}` and the banner
//	simply does not render:
//	  - DNS / TCP failure (offline)
//	  - HTTP 403 / 429 (GitHub rate-limited the anonymous request)
//	  - Non-200 status
//	  - Malformed JSON or missing `tag_name`
//	  - Empty / unparseable version string after stripping the leading "v"
//
//	Errors are logged at slog.Debug — they're noisy on slow networks and
//	not actionable for the user, so Debug avoids cluttering the production
//	log without losing the signal during local triage.
//
// Wails wiring (no startup hook from this file):
//
//	The TASK-062 plan lists `filesToModify: []`, so we do NOT modify
//	app.go's `startup`. The frontend calls `CheckForUpdate()` once on
//	mount (typically inside the top-level layout effect) and renders a
//	banner when `Available == true`. The `OpenUpdateURL` binding sends
//	the user to the release page via the platform browser opener.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// productVersion is the user-visible release version of the running binary.
// It aliases the single source-of-truth `version` var (main.go), which CI
// stamps from the git tag via ldflags — so the auto-update check can never
// again drift stale the way the old hardcoded "0.1.6" const did (that would
// have told every v0.4.x user they were perpetually out of date). The
// compare logic below strips the leading "v" from the GitHub tag before
// comparing, so this stays the bare "MAJOR.MINOR.PATCH" form (no "v").
var productVersion = version

// UpdateCheckResult is what `CheckForUpdate` returns to the frontend. The
// banner component renders when `Available == true`; on a hidden banner
// the other fields may be empty strings (frontend should not rely on them).
type UpdateCheckResult struct {
	// Available is true iff the GitHub /releases/latest endpoint returned
	// a tag strictly greater than productVersion. Network / parse failures
	// always set this to false (acceptance: "banner stays hidden").
	Available bool `json:"available"`

	// CurrentVersion is the running binary's productVersion (constant
	// above). Always populated, even when Available is false, so the
	// frontend can surface "Jarvis vX.Y.Z" without a second binding call.
	CurrentVersion string `json:"currentVersion"`

	// LatestVersion is the GitHub tag stripped of its "v" prefix. Empty
	// when the check failed (offline, rate-limited, malformed response).
	LatestVersion string `json:"latestVersion"`

	// ReleaseURL is the GitHub release page for LatestVersion. The banner's
	// link target. Empty when the check failed. We deliberately point at
	// the HTML release page (not the .exe / .dmg asset URL) so the user
	// can read release notes before downloading.
	ReleaseURL string `json:"releaseUrl"`
}

// ---------------------------------------------------------------------------
// Test seams — both are vars so tests can override without touching the
// production export surface. See app_update_check_test.go (if added).
// ---------------------------------------------------------------------------

// updateCheckLatestURL is the GitHub Releases API endpoint queried by
// CheckForUpdate. Exposed as a var (not const) so tests can point it at a
// httptest.Server. Production callers should treat it as read-only.
var updateCheckLatestURL = "https://api.github.com/repos/namanchopra/J.A.R.V.I.S/releases/latest"

// updateCheckReleasePagePrefix is prepended to the bare version (e.g.
// "0.1.7") to form the human-visible GitHub release page URL. The trailing
// "v" is part of the tag scheme (`gh release create v0.1.7`).
var updateCheckReleasePagePrefix = "https://github.com/namanchopra/J.A.R.V.I.S/releases/tag/v"

// updateCheckHTTPClient is the HTTP client used by CheckForUpdate. The 5s
// timeout is intentionally short — the check runs on launch and a slow
// GitHub response should never delay the banner from staying hidden. Tests
// can swap this for a mock RoundTripper.
var updateCheckHTTPClient = &http.Client{Timeout: 5 * time.Second}

// updateCheckBrowserOpen is the seam tests use to verify OpenUpdateURL
// dispatches to the platform browser without actually shelling out. The
// production implementation forwards to wails runtime.BrowserOpenURL.
var updateCheckBrowserOpen = func(ctx context.Context, url string) {
	runtime.BrowserOpenURL(ctx, url)
}

// ---------------------------------------------------------------------------
// Wails bindings
// ---------------------------------------------------------------------------

// CheckForUpdate hits the GitHub Releases API and reports whether a newer
// release than productVersion is available. The call is bounded by a 5s
// timeout (see updateCheckHTTPClient) and NEVER returns an error — any
// failure mode (offline, rate-limited, malformed response) yields
// `UpdateCheckResult{Available: false, CurrentVersion: productVersion}`
// so the banner stays hidden.
//
// Called by the frontend once on app mount. Cheap on the hot path because
// the request body is ~2 KB and the parsing is a single struct-decode.
func (a *App) CheckForUpdate() UpdateCheckResult {
	result := UpdateCheckResult{
		Available:      false,
		CurrentVersion: productVersion,
	}

	// Build the request with the app's context so a window-close cancels
	// the in-flight check. Falls back to context.Background when startup
	// hasn't wired a.ctx yet (defensive — shouldn't happen in production).
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, updateCheckLatestURL, nil)
	if err != nil {
		slog.Debug("CheckForUpdate: NewRequest failed", "err", err)
		return result
	}
	// Accept header per GitHub's recommended REST API media type. Without
	// it GitHub may downgrade to the legacy schema for some endpoints.
	req.Header.Set("Accept", "application/vnd.github+json")
	// User-Agent is REQUIRED by the GitHub API — anonymous requests
	// without one are rejected with 403. The "Jarvis-Updater" tag also
	// lets us spot the in-app check in GitHub's API logs separately
	// from the website's banner.
	req.Header.Set("User-Agent", "Jarvis-Updater/"+productVersion)

	resp, err := updateCheckHTTPClient.Do(req)
	if err != nil {
		// Offline, DNS failure, TLS handshake error, request timeout.
		// Acceptance: "banner stays hidden" — we silently swallow.
		slog.Debug("CheckForUpdate: request failed", "err", err)
		return result
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// 403 = rate-limited (anonymous IP exceeded 60 req/h); 404 =
		// repo has no releases yet. Both are non-fatal: banner hidden.
		slog.Debug("CheckForUpdate: non-200 status", "status", resp.StatusCode)
		return result
	}

	// Cap the read at 64 KB so a hostile server can't OOM the app by
	// streaming a multi-GB body. The real GitHub response is ~10 KB.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		slog.Debug("CheckForUpdate: read body failed", "err", err)
		return result
	}

	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		slog.Debug("CheckForUpdate: decode failed", "err", err)
		return result
	}

	// Strip the conventional "v" prefix. semverCompare expects bare
	// MAJOR.MINOR.PATCH on both sides.
	latest := strings.TrimPrefix(strings.TrimSpace(payload.TagName), "v")
	if latest == "" {
		slog.Debug("CheckForUpdate: empty tag_name in response")
		return result
	}

	// Only mark available when GitHub's tag is STRICTLY greater than the
	// running binary. Equal or older releases (the window between a tag
	// push and the website's FALLBACK bump) leave the banner hidden.
	if semverCompare(latest, productVersion) <= 0 {
		// Not an error — populate LatestVersion / ReleaseURL anyway so
		// a future "what's my latest seen version" UI can use them.
		result.LatestVersion = latest
		result.ReleaseURL = updateCheckReleasePagePrefix + latest
		return result
	}

	result.Available = true
	result.LatestVersion = latest
	result.ReleaseURL = updateCheckReleasePagePrefix + latest
	return result
}

// OpenUpdateURL routes the user to the GitHub release page via the
// platform's default browser. The frontend banner's "Update available"
// link should call this binding with the `ReleaseURL` from the most
// recent CheckForUpdate result.
//
// We require an https://github.com/... prefix as a defensive check: this
// binding is reachable from any frontend JS (and theoretically from a
// compromised renderer), so we refuse to dispatch arbitrary URLs to the
// system browser opener.
func (a *App) OpenUpdateURL(target string) error {
	target = strings.TrimSpace(target)
	if target == "" {
		return fmt.Errorf("OpenUpdateURL: url is required")
	}
	if !strings.HasPrefix(target, "https://github.com/") {
		return fmt.Errorf("OpenUpdateURL: refusing non-github URL: %q", target)
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	updateCheckBrowserOpen(ctx, target)
	return nil
}

// ---------------------------------------------------------------------------
// semverCompare — mirrors website/app/page.tsx semverCompare so the
// homepage and the in-app banner agree on ordering.
// ---------------------------------------------------------------------------

// semverCompare returns 1 if a > b, -1 if a < b, 0 if equal, comparing
// bare MAJOR.MINOR.PATCH strings (no pre-release suffix). Missing
// components are treated as 0 ("1.2" == "1.2.0"). Non-numeric components
// are coerced to 0 — same fallback the website's `parseInt(n, 10) || 0`
// produces — so "1.x.0" compares as "1.0.0" rather than panicking.
//
// Kept simple because the Jarvis tag scheme is just "vX.Y.Z" — no
// "v0.4.0-rc1" tags ever ship. If that changes, this function and the
// website's mirror need a coordinated update.
func semverCompare(a, b string) int {
	pa := parseSemverTriplet(a)
	pb := parseSemverTriplet(b)
	for i := 0; i < 3; i++ {
		if pa[i] > pb[i] {
			return 1
		}
		if pa[i] < pb[i] {
			return -1
		}
	}
	return 0
}

// parseSemverTriplet splits "X.Y.Z" into a fixed-length [3]int, defaulting
// missing or non-numeric components to 0. The fixed length lets the caller
// loop without bounds-checking.
func parseSemverTriplet(s string) [3]int {
	var out [3]int
	parts := strings.SplitN(s, ".", 4) // 4 keeps any trailing junk in [3]
	for i := 0; i < 3 && i < len(parts); i++ {
		n, err := strconv.Atoi(strings.TrimSpace(parts[i]))
		if err != nil {
			out[i] = 0
			continue
		}
		out[i] = n
	}
	return out
}
