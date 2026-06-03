package spotify

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/namanchopra/jarvis/internal/model"
)

// ---------------------------------------------------------------------------
// Wire types
// ---------------------------------------------------------------------------

// TokenResponse is the shape of Spotify's `POST /api/token` success body for
// both the authorization_code and refresh_token grants. Field names match
// Spotify's documented JSON keys verbatim so we can json.Unmarshal directly.
//
// Note: RefreshToken is OMITTED by Spotify on the refresh_token grant when
// the existing refresh token is still valid (per RFC 6749 §6). Callers must
// fall back to the previously stored refresh token when this field is empty.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
	ExpiresIn    int    `json:"expires_in"` // seconds
	RefreshToken string `json:"refresh_token"`
}

// ---------------------------------------------------------------------------
// Constants — endpoints + flow parameters
// ---------------------------------------------------------------------------

// SpotifyAuthURL is the user-facing authorization endpoint the browser hits
// during the auth_code+PKCE flow.
const SpotifyAuthURL = "https://accounts.spotify.com/authorize"

// SpotifyTokenURL is the token-exchange endpoint.
const SpotifyTokenURL = "https://accounts.spotify.com/api/token"

// Package-level vars so tests can substitute the real Spotify endpoints
// without touching production code. Default to the documented Spotify URLs.
//
// Touching them outside tests is a bug; they're var (not const) only because
// Go's testing model demands it for endpoint substitution.
var (
	authEndpoint  = SpotifyAuthURL
	tokenEndpoint = SpotifyTokenURL
)

// pkceVerifierLength is the length (in bytes of random source) we use to
// generate the PKCE code verifier. We base64url-encode the bytes, which
// yields 4 chars per 3 bytes — 48 bytes -> 64-char verifier, comfortably
// inside RFC 7636's [43, 128] range.
const pkceVerifierLength = 48

// pkceFlowTimeout is the deadline for the entire RunPKCEFlow: from opening
// the browser to receiving the callback. Five minutes is generous but
// bounded — protects against a user who walks away mid-flow.
const pkceFlowTimeout = 5 * time.Minute

// CallbackPort is the loopback port the OAuth callback server binds.
//
// Pinned to a single value (rather than a random kernel-assigned port)
// because Spotify's developer dashboard rejects auth requests whose
// redirect_uri doesn't EXACTLY match a registered URI — port included.
// With a random port, the user would have to register every possible
// port up front, which isn't viable.
//
// The exact value (53682) is arbitrary, chosen high enough to avoid
// privileged-port territory and uncommon enough to not collide with
// well-known dev-server ports (3000, 5173, 8080, 8443, etc.). The
// matching CallbackURI constant is what users paste into their Spotify
// Developer app's "Redirect URIs" field.
const CallbackPort = 53682

// CallbackURI is the redirect URI Jarvis builds the Spotify authorize
// URL with. Users must paste this verbatim into their Spotify Developer
// app's "Redirect URIs" list — the UI surfaces it as copyable text in
// the Settings → Connections → Spotify card.
const CallbackURI = "http://127.0.0.1:53682/callback"

// callbackHTMLSuccess is rendered to the browser after a successful auth
// code arrives. Plain HTML — no external assets — so it works fully offline.
const callbackHTMLSuccess = `<!doctype html><html><head><meta charset="utf-8">` +
	`<title>Jarvis · Spotify connected</title>` +
	`<style>body{background:#0a0a0a;color:#22d3ee;font-family:SF Mono,Menlo,monospace;` +
	`display:flex;align-items:center;justify-content:center;height:100vh;margin:0;}` +
	`.box{text-align:center;}h1{font-size:14px;letter-spacing:.2em;margin:0 0 8px;}` +
	`p{font-size:12px;color:#9ca3af;margin:0;}</style></head><body>` +
	`<div class="box"><h1>SPOTIFY :: CONNECTED</h1>` +
	`<p>You can close this tab and return to Jarvis.</p></div></body></html>`

// callbackHTMLError is rendered to the browser if Spotify returned an
// `error=` query param rather than a `code=`. The error code is HTML-escaped
// via strings.NewReplacer to defend against an unexpected payload.
func callbackHTMLError(spotifyErr string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		`'`, "&#39;",
	)
	safe := r.Replace(spotifyErr)
	return `<!doctype html><html><head><meta charset="utf-8">` +
		`<title>Jarvis · Spotify error</title>` +
		`<style>body{background:#0a0a0a;color:#ef4444;font-family:SF Mono,Menlo,monospace;` +
		`display:flex;align-items:center;justify-content:center;height:100vh;margin:0;}` +
		`.box{text-align:center;}h1{font-size:14px;letter-spacing:.2em;margin:0 0 8px;}` +
		`p{font-size:12px;color:#9ca3af;margin:0;}</style></head><body>` +
		`<div class="box"><h1>SPOTIFY :: ERROR</h1>` +
		`<p>` + safe + `</p></div></body></html>`
}

// ---------------------------------------------------------------------------
// PKCE primitives
// ---------------------------------------------------------------------------

// generatePKCEVerifier mints a fresh code verifier: 48 bytes of crypto/rand
// base64url-encoded (no padding) → 64 chars. RFC 7636 §4.1 requires the
// verifier to be 43–128 chars from the [A-Z][a-z][0-9]-._~ set; the
// base64url alphabet is a strict subset of that set.
func generatePKCEVerifier() (string, error) {
	b := make([]byte, pkceVerifierLength)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("spotify: rand.Read: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// pkceChallenge computes the S256 code challenge from the verifier:
// base64url(sha256(verifier)) with no padding — per RFC 7636 §4.2.
func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// generateStateNonce returns a 32-byte base64url-encoded random string that
// goes in the OAuth `state` parameter — Spotify echoes this back on the
// callback so we can detect tampering / cross-flow confusion.
func generateStateNonce() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("spotify: rand.Read state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// ---------------------------------------------------------------------------
// BuildAuthURL — assemble the authorize-endpoint URL
// ---------------------------------------------------------------------------

// BuildAuthURL constructs the Spotify authorize-endpoint URL for the
// Authorization Code + PKCE flow. Returns:
//
//   - authURL:      the URL to open in the user's browser
//   - codeVerifier: the PKCE verifier (must be retained for the token
//                   exchange; never sent to Spotify directly)
//   - state:        the state nonce (must be checked against the value
//                   Spotify echoes on the callback)
//
// redirectURI is NOT a parameter here because RunPKCEFlow owns the
// callback port allocation and threads the resulting URI through itself.
// Callers wanting a non-default redirect should drive the flow manually
// via BuildAuthURLWithRedirect.
//
// scopes is space-joined into the `scope` query param per RFC 6749 §3.3.
func BuildAuthURL(clientID string, scopes []string) (authURL, codeVerifier, state string, err error) {
	return BuildAuthURLWithRedirect(clientID, scopes, "")
}

// BuildAuthURLWithRedirect is BuildAuthURL with an explicit redirect URI.
// Exposed so RunPKCEFlow can pass the random-port localhost URI it picked.
func BuildAuthURLWithRedirect(clientID string, scopes []string, redirectURI string) (authURL, codeVerifier, state string, err error) {
	if strings.TrimSpace(clientID) == "" {
		return "", "", "", fmt.Errorf("spotify: BuildAuthURL: clientID is required")
	}

	verifier, err := generatePKCEVerifier()
	if err != nil {
		return "", "", "", fmt.Errorf("spotify: BuildAuthURL: %w", err)
	}
	stateNonce, err := generateStateNonce()
	if err != nil {
		return "", "", "", fmt.Errorf("spotify: BuildAuthURL: %w", err)
	}

	q := url.Values{}
	q.Set("client_id", clientID)
	q.Set("response_type", "code")
	if redirectURI != "" {
		q.Set("redirect_uri", redirectURI)
	}
	q.Set("code_challenge_method", "S256")
	q.Set("code_challenge", pkceChallenge(verifier))
	q.Set("state", stateNonce)
	if len(scopes) > 0 {
		q.Set("scope", strings.Join(scopes, " "))
	}

	return authEndpoint + "?" + q.Encode(), verifier, stateNonce, nil
}

// ---------------------------------------------------------------------------
// ExchangeCode — grant_type=authorization_code
// ---------------------------------------------------------------------------

// ExchangeCode POSTs the authorization code (received via the browser
// redirect) to Spotify's /api/token endpoint together with the PKCE
// verifier, and returns the resulting access + refresh tokens.
//
// Per RFC 7636 §4.5, the verifier sent here MUST match the one whose
// challenge was sent to BuildAuthURL — Spotify cross-checks them.
func ExchangeCode(clientID, code, codeVerifier, redirectURI string) (*TokenResponse, error) {
	if strings.TrimSpace(clientID) == "" {
		return nil, fmt.Errorf("spotify: ExchangeCode: clientID is required")
	}
	if strings.TrimSpace(code) == "" {
		return nil, fmt.Errorf("spotify: ExchangeCode: code is required")
	}
	if strings.TrimSpace(codeVerifier) == "" {
		return nil, fmt.Errorf("spotify: ExchangeCode: codeVerifier is required")
	}
	if strings.TrimSpace(redirectURI) == "" {
		return nil, fmt.Errorf("spotify: ExchangeCode: redirectURI is required")
	}

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", clientID)
	form.Set("code_verifier", codeVerifier)

	return postTokenForm(form, "ExchangeCode")
}

// ---------------------------------------------------------------------------
// RefreshToken — grant_type=refresh_token
// ---------------------------------------------------------------------------

// RefreshToken mints a new access token from a refresh token. Spotify
// sometimes returns a new refresh token alongside (per RFC 6749 §6) and
// sometimes leaves the existing one valid; callers that care MUST keep
// using the previously stored refresh token when the response's
// RefreshToken field is empty. The Client.refreshIfNeeded helper handles
// this for the Web API client.
//
// Returns wrap ErrTokenRefreshFailed so callers can branch with
// errors.Is(err, ErrTokenRefreshFailed) to decide whether to drop the
// user's stored credentials.
func RefreshToken(clientID, refreshToken string) (*TokenResponse, error) {
	if strings.TrimSpace(clientID) == "" {
		return nil, fmt.Errorf("spotify: RefreshToken: clientID is required")
	}
	if strings.TrimSpace(refreshToken) == "" {
		return nil, fmt.Errorf("spotify: RefreshToken: refreshToken is required")
	}

	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", clientID)

	tok, err := postTokenForm(form, "RefreshToken")
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTokenRefreshFailed, err)
	}
	return tok, nil
}

// postTokenForm is the shared POST-the-token-endpoint helper for both
// grants. Centralizes timeout configuration + non-2xx body capture.
func postTokenForm(form url.Values, op string) (*TokenResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("spotify: %s: build request: %w", op, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("spotify: %s: do request: %w", op, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, fmt.Errorf("spotify: %s: read body: %w", op, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("spotify: %s: token endpoint returned %d: %s", op, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var tok TokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, fmt.Errorf("spotify: %s: decode JSON: %w", op, err)
	}
	if tok.AccessToken == "" {
		return nil, fmt.Errorf("spotify: %s: empty access_token in response", op)
	}
	return &tok, nil
}

// ---------------------------------------------------------------------------
// One-shot localhost callback server
// ---------------------------------------------------------------------------

// CallbackResult is what StartCallbackServer surfaces. Either Code is
// populated (success path), or Err is set to the Spotify-side error code
// (e.g. "access_denied" when the user clicks Cancel on the consent screen).
// State is the value Spotify echoed back — the caller MUST compare it to
// the nonce it sent to BuildAuthURL before trusting Code.
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
// after the first hit and the consumer (RunPKCEFlow) shuts the listener
// via closeFn.
func StartCallbackServer(port int) (codeCh <-chan CallbackResult, errCh <-chan error, closeFn func()) {
	cCh := make(chan CallbackResult, 1)
	eCh := make(chan error, 1)

	addr := "127.0.0.1:" + strconv.Itoa(port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		eCh <- fmt.Errorf("spotify: StartCallbackServer: listen %s: %w", addr, err)
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
			case eCh <- fmt.Errorf("spotify: callback server: %w", err):
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
// RunPKCEFlow — end-to-end orchestration
// ---------------------------------------------------------------------------

// pickFreePort asks the OS to bind 127.0.0.1:0 and returns the kernel-
// assigned port. The listener is closed immediately — there's a tiny TOCTOU
// window where another process could steal the port, but on a single-user
// desktop that's vanishingly rare.
func pickFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("spotify: pickFreePort: %w", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port, nil
}

// DefaultScopes are the scopes Jarvis requests at sign-in. Tuned to the
// features TASK-009 + TASK-010 expose:
//   - user-read-playback-state    : read currently-playing
//   - user-modify-playback-state  : play/pause/skip via Web API
//   - user-read-currently-playing : "what is playing" tool
//   - user-library-modify          : like-current-track tool (TASK-010)
var DefaultScopes = []string{
	"user-read-playback-state",
	"user-modify-playback-state",
	"user-read-currently-playing",
	"user-library-modify",
}

// RunPKCEFlow drives the full Authorization Code + PKCE handshake:
//
//  1. Pick a free localhost port.
//  2. Start the one-shot callback server on that port.
//  3. Build the auth URL (with redirect_uri pointing at the callback).
//  4. Invoke openBrowser to launch the user's default browser.
//  5. Wait for the callback (or pkceFlowTimeout).
//  6. Validate state, exchange code for tokens.
//  7. Mutate cfg in place with AccessToken, RefreshToken, ExpiresAt.
//
// openBrowser is injected so callers can use exec.Command("open", url) on
// macOS without dragging os/exec into this package's test surface; tests
// pass a stub that captures the URL.
//
// On success, cfg.AccessToken / RefreshToken / ExpiresAt are populated.
// ClientID is left populated if previously set, otherwise set to the
// caller's clientID argument. On any failure the cfg is NOT mutated, so a
// botched sign-in doesn't half-clobber a previously valid token set.
func RunPKCEFlow(clientID string, openBrowser func(url string) error, cfg *model.SpotifyConfig) error {
	if cfg == nil {
		return fmt.Errorf("spotify: RunPKCEFlow: cfg is required")
	}
	if strings.TrimSpace(clientID) == "" {
		return fmt.Errorf("spotify: RunPKCEFlow: clientID is required")
	}
	if openBrowser == nil {
		return fmt.Errorf("spotify: RunPKCEFlow: openBrowser is required")
	}

	// Fixed loopback port — see CallbackPort doc for why we don't pick
	// a random one. If 53682 is already in use, StartCallbackServer's
	// net.Listen will surface an "address already in use" error on
	// errCh, which the select below catches.
	redirectURI := CallbackURI

	codeCh, errCh, closeFn := StartCallbackServer(CallbackPort)
	defer closeFn()

	authURL, verifier, expectedState, err := BuildAuthURLWithRedirect(clientID, DefaultScopes, redirectURI)
	if err != nil {
		return fmt.Errorf("spotify: RunPKCEFlow: %w", err)
	}

	if err := openBrowser(authURL); err != nil {
		return fmt.Errorf("spotify: RunPKCEFlow: openBrowser: %w", err)
	}

	timer := time.NewTimer(pkceFlowTimeout)
	defer timer.Stop()

	var result CallbackResult
	select {
	case result = <-codeCh:
		// fall through
	case err := <-errCh:
		return fmt.Errorf("spotify: RunPKCEFlow: %w", err)
	case <-timer.C:
		return fmt.Errorf("spotify: RunPKCEFlow: timed out after %s waiting for callback", pkceFlowTimeout)
	}

	if result.Err != "" {
		return fmt.Errorf("spotify: RunPKCEFlow: spotify returned error %q", result.Err)
	}
	if result.State != expectedState {
		return fmt.Errorf("spotify: RunPKCEFlow: state mismatch (possible CSRF)")
	}
	if result.Code == "" {
		return fmt.Errorf("spotify: RunPKCEFlow: callback returned empty code")
	}

	tok, err := ExchangeCode(clientID, result.Code, verifier, redirectURI)
	if err != nil {
		return fmt.Errorf("spotify: RunPKCEFlow: %w", err)
	}

	cfg.AccessToken = tok.AccessToken
	cfg.RefreshToken = tok.RefreshToken
	cfg.ExpiresAt = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	if cfg.ClientID == "" {
		cfg.ClientID = clientID
	}
	return nil
}
