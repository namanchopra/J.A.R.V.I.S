package main

// app_update_check_test.go — TASK-062 unit tests for the auto-update path.
//
// Covers the three acceptance criteria from the plan:
//
//	1. Banner appears when GitHub returns a tag strictly greater than
//	   productVersion.
//	2. OpenUpdateURL routes to the GitHub release page (we capture the
//	   URL via the updateCheckBrowserOpen test seam).
//	3. Failure case: offline / rate-limit / malformed response keeps the
//	   banner hidden (Available == false).
//
// Test seams used (declared in app_update_check.go):
//   - updateCheckLatestURL          — pointed at httptest.NewServer
//   - updateCheckHTTPClient         — replaced with a low-timeout client
//   - updateCheckBrowserOpen        — captures the OpenUpdateURL target
//   - updateCheckReleasePagePrefix  — left at production default; tests
//     assert the constructed release URL contains it.
//
// No network is touched: every test wires updateCheckLatestURL to a local
// httptest.Server (or to a deliberately-unreachable URL for the offline
// case) and restores the production default via t.Cleanup.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// withFakeGitHub spins up an httptest.Server that responds with the given
// status + body and rewires updateCheckLatestURL / updateCheckHTTPClient to
// hit it. The cleanup restores all package-level vars and shuts the server
// down so tests stay isolated.
func withFakeGitHub(t *testing.T, status int, body string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Assert the production headers reach the API — defensive: any
		// future refactor that drops User-Agent would silently get 403 in
		// prod (and pass our test against a permissive mock); checking
		// here makes that regression loud.
		if r.Header.Get("User-Agent") == "" {
			t.Errorf("expected User-Agent header on outbound request")
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	prevURL := updateCheckLatestURL
	prevClient := updateCheckHTTPClient
	updateCheckLatestURL = srv.URL
	updateCheckHTTPClient = &http.Client{Timeout: 2 * time.Second}
	t.Cleanup(func() {
		updateCheckLatestURL = prevURL
		updateCheckHTTPClient = prevClient
		srv.Close()
	})
}

// makeAppForUpdateCheck returns a minimal *App with a non-nil context so
// CheckForUpdate's `if a.ctx == nil` defensive branch isn't the path we
// exercise (we want the production code path).
func makeAppForUpdateCheck() *App {
	return &App{ctx: context.Background()}
}

// ---------------------------------------------------------------------------
// AC #1 — banner appears for a strictly-greater tag.
// ---------------------------------------------------------------------------

func TestCheckForUpdate_BannerVisibleForNewerTag(t *testing.T) {
	// Use a sentinel that is strictly greater than ANY real productVersion,
	// so this test stays correct as productVersion (now ldflags-stamped from
	// the git tag) advances. Hardcoding a near-version like "0.1.8" silently
	// inverted once productVersion moved past it.
	const newerTag = "999.0.0"
	body, _ := json.Marshal(map[string]string{"tag_name": "v" + newerTag})
	withFakeGitHub(t, http.StatusOK, string(body))

	a := makeAppForUpdateCheck()
	got := a.CheckForUpdate()

	if !got.Available {
		t.Fatalf("expected Available=true for newer tag, got false (result=%+v)", got)
	}
	if got.LatestVersion != newerTag {
		t.Errorf("LatestVersion: want %q, got %q", newerTag, got.LatestVersion)
	}
	if got.CurrentVersion != productVersion {
		t.Errorf("CurrentVersion: want %q, got %q", productVersion, got.CurrentVersion)
	}
	if !strings.HasPrefix(got.ReleaseURL, updateCheckReleasePagePrefix) {
		t.Errorf("ReleaseURL should start with %q, got %q", updateCheckReleasePagePrefix, got.ReleaseURL)
	}
	if !strings.HasSuffix(got.ReleaseURL, newerTag) {
		t.Errorf("ReleaseURL should end with version %q, got %q", newerTag, got.ReleaseURL)
	}
}

// Equal version: NOT a newer release, banner hidden. This guards the
// strict-greater-than semantics — a tag exactly equal to productVersion
// must not trigger an "update available" prompt.
func TestCheckForUpdate_HiddenForEqualTag(t *testing.T) {
	body, _ := json.Marshal(map[string]string{"tag_name": "v" + productVersion})
	withFakeGitHub(t, http.StatusOK, string(body))

	a := makeAppForUpdateCheck()
	got := a.CheckForUpdate()

	if got.Available {
		t.Fatalf("Available must be false for equal tag, got true (result=%+v)", got)
	}
}

// Older tag (GitHub temporarily ahead-of-tag during a release window).
// Banner hidden.
func TestCheckForUpdate_HiddenForOlderTag(t *testing.T) {
	body, _ := json.Marshal(map[string]string{"tag_name": "v0.0.1"})
	withFakeGitHub(t, http.StatusOK, string(body))

	a := makeAppForUpdateCheck()
	got := a.CheckForUpdate()

	if got.Available {
		t.Fatalf("Available must be false for older tag, got true (result=%+v)", got)
	}
}

// ---------------------------------------------------------------------------
// AC #3 — failure modes keep the banner hidden.
// ---------------------------------------------------------------------------

func TestCheckForUpdate_HiddenOnRateLimit(t *testing.T) {
	// GitHub returns 403 with a rate-limit body — we should not parse the
	// body and we should not mark Available.
	withFakeGitHub(t, http.StatusForbidden, `{"message":"API rate limit exceeded"}`)

	a := makeAppForUpdateCheck()
	got := a.CheckForUpdate()

	if got.Available {
		t.Fatalf("Available must be false on 403 rate-limit, got true (result=%+v)", got)
	}
	if got.CurrentVersion != productVersion {
		t.Errorf("CurrentVersion still populated on failure; got %q", got.CurrentVersion)
	}
}

func TestCheckForUpdate_HiddenOnMalformedBody(t *testing.T) {
	// 200 OK but the body is not valid JSON. Decode must fail silently.
	withFakeGitHub(t, http.StatusOK, `<html>oops</html>`)

	a := makeAppForUpdateCheck()
	got := a.CheckForUpdate()

	if got.Available {
		t.Fatalf("Available must be false on malformed body, got true (result=%+v)", got)
	}
}

func TestCheckForUpdate_HiddenOnEmptyTag(t *testing.T) {
	// Decode succeeds but tag_name is empty. Banner stays hidden.
	withFakeGitHub(t, http.StatusOK, `{"tag_name":""}`)

	a := makeAppForUpdateCheck()
	got := a.CheckForUpdate()

	if got.Available {
		t.Fatalf("Available must be false on empty tag, got true (result=%+v)", got)
	}
}

func TestCheckForUpdate_HiddenWhenOffline(t *testing.T) {
	// Point the URL at an unreachable host. The 1s HTTP timeout caps
	// the test's worst-case duration.
	prevURL := updateCheckLatestURL
	prevClient := updateCheckHTTPClient
	updateCheckLatestURL = "http://127.0.0.1:1/this-port-is-not-listening"
	updateCheckHTTPClient = &http.Client{Timeout: 1 * time.Second}
	t.Cleanup(func() {
		updateCheckLatestURL = prevURL
		updateCheckHTTPClient = prevClient
	})

	a := makeAppForUpdateCheck()
	got := a.CheckForUpdate()

	if got.Available {
		t.Fatalf("Available must be false when offline, got true (result=%+v)", got)
	}
}

// ---------------------------------------------------------------------------
// AC #2 — OpenUpdateURL routes to the GitHub release page.
// ---------------------------------------------------------------------------

func TestOpenUpdateURL_DispatchesToBrowser(t *testing.T) {
	var captured string
	prev := updateCheckBrowserOpen
	updateCheckBrowserOpen = func(_ context.Context, url string) { captured = url }
	t.Cleanup(func() { updateCheckBrowserOpen = prev })

	a := makeAppForUpdateCheck()
	target := updateCheckReleasePagePrefix + "0.1.8"
	if err := a.OpenUpdateURL(target); err != nil {
		t.Fatalf("OpenUpdateURL returned error: %v", err)
	}
	if captured != target {
		t.Errorf("browser opener received %q, want %q", captured, target)
	}
}

func TestOpenUpdateURL_RejectsEmpty(t *testing.T) {
	a := makeAppForUpdateCheck()
	if err := a.OpenUpdateURL("   "); err == nil {
		t.Fatal("expected error for empty url, got nil")
	}
}

func TestOpenUpdateURL_RejectsNonGitHub(t *testing.T) {
	a := makeAppForUpdateCheck()
	if err := a.OpenUpdateURL("https://evil.example.com/payload"); err == nil {
		t.Fatal("expected error for non-github url, got nil")
	}
}

// ---------------------------------------------------------------------------
// semverCompare — algorithmic mirror of website/app/page.tsx.
// ---------------------------------------------------------------------------

func TestSemverCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.1", "1.0.0", 1},
		{"1.0.0", "1.0.1", -1},
		{"1.1.0", "1.0.9", 1},
		{"2.0.0", "1.99.99", 1},
		{"0.1.6", "0.1.6", 0},
		{"0.1.7", "0.1.6", 1},
		{"0.1.6", "0.1.7", -1},
		// Missing patch defaults to 0.
		{"1.2", "1.2.0", 0},
		{"1.2.0", "1.2", 0},
		{"1.2", "1.2.1", -1},
		// Non-numeric component degrades to 0 (matches the website's
		// parseInt(...) || 0 fallback).
		{"1.x.0", "1.0.0", 0},
		{"1.0.x", "1.0.0", 0},
	}
	for _, tc := range cases {
		t.Run(tc.a+" vs "+tc.b, func(t *testing.T) {
			got := semverCompare(tc.a, tc.b)
			if got != tc.want {
				t.Errorf("semverCompare(%q,%q) = %d, want %d", tc.a, tc.b, got, tc.want)
			}
		})
	}
}
