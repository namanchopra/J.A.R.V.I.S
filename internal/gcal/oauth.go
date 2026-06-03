// Google Calendar OAuth driver — sibling to internal/spotify/oauth.go.
//
// Google's OAuth flow differs from Spotify's in two load-bearing ways:
//
//  1. Google uses confidential clients for installed desktop apps: the
//     authorization-code grant requires a client_secret on the token
//     exchange. Spotify uses PKCE instead. We delegate the protocol
//     mechanics to golang.org/x/oauth2 + google.Endpoint rather than
//     hand-rolling them.
//
//  2. Google only issues a refresh_token on the very first consent unless
//     `prompt=consent` is forced. We pass oauth2.ApprovalForce so the
//     re-consent screen is always shown — otherwise a user who has previously
//     authorized this client ID would silently land back in Jarvis without a
//     refresh token, and the next access-token expiry would dead-end at
//     ErrNotAuthenticated.
//
// The one-shot localhost callback server, state-nonce CSRF guard, and
// browser-injection ergonomics mirror internal/spotify/oauth.go so the two
// flows are reviewable side-by-side.
package gcal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/namanchopra/jarvis/internal/model"
)

// ---------------------------------------------------------------------------
// Constants — flow parameters
// ---------------------------------------------------------------------------

// oauthFlowTimeout is the deadline for the entire RunOAuthFlow: from opening
// the browser to receiving the callback. Five minutes is generous but
// bounded — protects against a user who walks away mid-flow.
const oauthFlowTimeout = 5 * time.Minute

// scopes is the fixed list of OAuth scopes Jarvis requests at sign-in.
// Joined verbatim into the `scope` query param per RFC 6749 §3.3.
//
//   - calendar.events : read + write events on user-owned calendars (the
//     load-bearing scope; what TASK-008/TASK-010 need).
//   - openid + profile: trigger the consent screen's account-picker UX
//     and let us later attach a stable subject ID if needed; no PII
//     pulled today.
var scopes = []string{
	"https://www.googleapis.com/auth/calendar.events",
	"openid",
	"profile",
}

// callbackHTMLSuccess is rendered to the browser after a successful auth
// code arrives. Plain HTML — no external assets — so it works fully offline.
const callbackHTMLSuccess = `<!doctype html><html><head><meta charset="utf-8">` +
	`<title>Jarvis · Google Calendar connected</title>` +
	`<style>body{background:#0a0a0a;color:#22d3ee;font-family:SF Mono,Menlo,monospace;` +
	`display:flex;align-items:center;justify-content:center;height:100vh;margin:0;}` +
	`.box{text-align:center;}h1{font-size:14px;letter-spacing:.2em;margin:0 0 8px;}` +
	`p{font-size:12px;color:#9ca3af;margin:0;}</style></head><body>` +
	`<div class="box"><h1>GOOGLE CALENDAR :: CONNECTED</h1>` +
	`<p>You can close this tab and return to Jarvis.</p></div></body></html>`

// callbackHTMLError is rendered to the browser if Google returned an
// `error=` query param rather than a `code=`. The error code is HTML-escaped
// via strings.NewReplacer to defend against an unexpected payload.
func callbackHTMLError(googleErr string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		`'`, "&#39;",
	)
	safe := r.Replace(googleErr)
	return `<!doctype html><html><head><meta charset="utf-8">` +
		`<title>Jarvis · Google Calendar error</title>` +
		`<style>body{background:#0a0a0a;color:#ef4444;font-family:SF Mono,Menlo,monospace;` +
		`display:flex;align-items:center;justify-content:center;height:100vh;margin:0;}` +
		`.box{text-align:center;}h1{font-size:14px;letter-spacing:.2em;margin:0 0 8px;}` +
		`p{font-size:12px;color:#9ca3af;margin:0;}</style></head><body>` +
		`<div class="box"><h1>GOOGLE CALENDAR :: ERROR</h1>` +
		`<p>` + safe + `</p></div></body></html>`
}

// ---------------------------------------------------------------------------
// State-nonce CSRF guard
// ---------------------------------------------------------------------------

// generateStateNonce returns a 32-byte hex-encoded random string that goes
// in the OAuth `state` parameter — Google echoes this back on the callback
// so we can detect tampering / cross-flow confusion. Hex-encoded (rather
// than base64url) because hex is unambiguously URL-safe and Google's
// callback URI parsing leaves no room for ambiguity.
func generateStateNonce() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("gcal: rand.Read state: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// ---------------------------------------------------------------------------
// One-shot localhost callback server
// ---------------------------------------------------------------------------

// CallbackResult is what StartCallbackServer surfaces. Either Code is
// populated (success path), or Err is set to the Google-side error code
// (e.g. "access_denied" when the user clicks Cancel on the consent screen).
// State is the value Google echoed back — the caller MUST compare it to
// the nonce it sent on the authorize URL before trusting Code.
type CallbackResult struct {
	Code  string
	State string
	Err   string
}

// StartCallbackServer listens on 127.0.0.1:port for a single GET /callback
// request, captures the OAuth response, and shuts itself down. Returns
// channels the caller can select on alongside a closeFn that forces the
// listener closed if the caller bails early.
//
// The server intentionally accepts ONE request and then idles — a stale
// browser tab can't re-trigger the flow because the buffered codeCh fills
// after the first hit and the consumer (RunOAuthFlow) shuts the listener
// via closeFn.
func StartCallbackServer(port int) (codeCh <-chan CallbackResult, errCh <-chan error, closeFn func()) {
	cCh := make(chan CallbackResult, 1)
	eCh := make(chan error, 1)

	addr := "127.0.0.1:" + strconv.Itoa(port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		eCh <- fmt.Errorf("gcal: StartCallbackServer: listen %s: %w", addr, err)
		close(cCh)
		return cCh, eCh, func() {}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		res := CallbackResult{
			Code:  q.Get("code"),
			State: q.Get("state"),
			Err:   q.Get("error"),
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if res.Err != "" {
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, callbackHTMLError(res.Err))
		} else {
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, callbackHTMLSuccess)
		}

		select {
		case cCh <- res:
		default:
		}
	})

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
	}

	go func() {
		if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			select {
			case eCh <- fmt.Errorf("gcal: callback server: %w", err):
			default:
			}
		}
	}()

	closeFn = func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
	return cCh, eCh, closeFn
}

// ---------------------------------------------------------------------------
// pickFreePort — kernel-assigned loopback port
// ---------------------------------------------------------------------------

// pickFreePort asks the OS to bind 127.0.0.1:0 and returns the kernel-
// assigned port. The listener is closed immediately — there's a tiny TOCTOU
// window where another process could steal the port, but on a single-user
// desktop that's vanishingly rare.
//
// Unlike Spotify's flow (where the redirect URI must match a pre-registered
// value verbatim), Google's "Desktop app" OAuth client type accepts any
// loopback port (per RFC 8252 §7.3), so we can pick freely each run.
func pickFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("gcal: pickFreePort: %w", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port, nil
}

// ---------------------------------------------------------------------------
// RunOAuthFlow — end-to-end orchestration
// ---------------------------------------------------------------------------

// RunOAuthFlow drives the desktop OAuth code flow end to end.
//
// Steps (mirroring spotify.RunPKCEFlow):
//  1. Pick a free loopback port.
//  2. Start a one-shot HTTP callback server on that port.
//  3. Build the authorize URL with state-nonce + scopes.
//  4. Open the user's browser at the authorize URL.
//  5. Wait for the callback (timeout: 5 min).
//  6. Validate state, exchange code for tokens via oauth2.Config.Exchange.
//  7. Mutate cfg.AccessToken / RefreshToken / ExpiresAt / ClientID / ClientSecret.
//
// On success cfg is mutated. The caller is responsible for SaveConfig.
// On failure cfg is left untouched.
//
// openBrowser is injected so callers can use exec.Command("open", url) on
// macOS without dragging os/exec into this package's test surface; tests
// pass a stub that captures the URL.
//
// We force `access_type=offline` + `prompt=consent` so Google issues a
// refresh token on every consent — without ApprovalForce a returning user
// who has previously authorized this client gets only an access token, and
// the next expiry dead-ends at ErrNotAuthenticated with no remediation
// path inside the app.
func RunOAuthFlow(clientID, clientSecret string, openBrowser func(url string) error, cfg *model.GCalConfig) error {
	if cfg == nil {
		return fmt.Errorf("RunOAuthFlow: cfg is required")
	}
	if strings.TrimSpace(clientID) == "" || strings.TrimSpace(clientSecret) == "" {
		return fmt.Errorf("RunOAuthFlow: clientID/Secret required: %w", ErrInvalidConfig)
	}
	if openBrowser == nil {
		return fmt.Errorf("RunOAuthFlow: openBrowser is required")
	}

	port, err := pickFreePort()
	if err != nil {
		return fmt.Errorf("RunOAuthFlow: %w", err)
	}
	redirectURI := "http://127.0.0.1:" + strconv.Itoa(port) + "/callback"

	codeCh, errCh, closeFn := StartCallbackServer(port)
	defer closeFn()

	expectedState, err := generateStateNonce()
	if err != nil {
		return fmt.Errorf("RunOAuthFlow: %w", err)
	}

	oauthCfg := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint:     google.Endpoint,
		RedirectURL:  redirectURI,
		Scopes:       scopes,
	}

	// AccessTypeOffline is what triggers Google to mint a refresh_token
	// in the first place; ApprovalForce makes that issuance idempotent
	// across re-runs of the flow against the same client_id (see func
	// doc above).
	authURL := oauthCfg.AuthCodeURL(
		expectedState,
		oauth2.AccessTypeOffline,
		oauth2.ApprovalForce,
	)

	if err := openBrowser(authURL); err != nil {
		return fmt.Errorf("RunOAuthFlow: openBrowser: %w", err)
	}

	timer := time.NewTimer(oauthFlowTimeout)
	defer timer.Stop()

	var result CallbackResult
	select {
	case result = <-codeCh:
		// fall through
	case err := <-errCh:
		return fmt.Errorf("RunOAuthFlow: %w", err)
	case <-timer.C:
		return fmt.Errorf("RunOAuthFlow: timed out after %s waiting for callback", oauthFlowTimeout)
	}

	if result.Err != "" {
		return fmt.Errorf("RunOAuthFlow: google returned error %q", result.Err)
	}
	if result.State != expectedState {
		// CSRF guard — abort BEFORE exchanging the code. An attacker who
		// can plant a code into our callback can't also forge the matching
		// state nonce, so failing here means we never grant a token to a
		// flow the user didn't initiate.
		return fmt.Errorf("RunOAuthFlow: state nonce mismatch (csrf): got %q want %q", result.State, expectedState)
	}
	if result.Code == "" {
		return fmt.Errorf("RunOAuthFlow: callback returned empty code")
	}

	// oauth2.Config.Exchange uses the package-level http.DefaultClient
	// unless we stash a custom *http.Client on the context via
	// oauth2.HTTPClient — the default 15s connect+read timeout is fine
	// for a token endpoint, so we don't override.
	exchangeCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	tok, err := oauthCfg.Exchange(exchangeCtx, result.Code)
	if err != nil {
		return fmt.Errorf("RunOAuthFlow: token exchange: %w", err)
	}
	if tok.AccessToken == "" {
		return fmt.Errorf("RunOAuthFlow: token exchange: empty access_token in response")
	}
	if tok.RefreshToken == "" {
		// With AccessTypeOffline + ApprovalForce this should be impossible
		// on a healthy Google response. Treat it as a hard failure rather
		// than silently persisting an un-refreshable access token that
		// would brick the integration on the first hour-out expiry.
		return fmt.Errorf("RunOAuthFlow: token exchange: empty refresh_token (re-consent did not yield a refresh token)")
	}

	cfg.AccessToken = tok.AccessToken
	cfg.RefreshToken = tok.RefreshToken
	cfg.ExpiresAt = tok.Expiry
	cfg.ClientID = clientID
	cfg.ClientSecret = clientSecret
	return nil
}
