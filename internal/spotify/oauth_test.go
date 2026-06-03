package spotify

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/namanchopra/jarvis/internal/model"
)

// TestGeneratePKCEVerifier_LengthAndAlphabet verifies the verifier we mint
// is RFC 7636 §4.1-compliant: length in [43, 128] from the unreserved
// alphabet. base64url (no padding) is a strict subset of [A-Z][a-z][0-9]-_.
func TestGeneratePKCEVerifier_LengthAndAlphabet(t *testing.T) {
	v, err := generatePKCEVerifier()
	if err != nil {
		t.Fatalf("generatePKCEVerifier: %v", err)
	}
	if n := len(v); n < 43 || n > 128 {
		t.Errorf("verifier length %d out of RFC 7636 [43,128] range", n)
	}
	// 48 bytes of random source -> 64-char base64url (no padding).
	if got, want := len(v), 64; got != want {
		t.Errorf("verifier length: got %d, want %d (48 bytes -> 64 base64url chars)", got, want)
	}
	for _, c := range v {
		ok := (c >= 'A' && c <= 'Z') ||
			(c >= 'a' && c <= 'z') ||
			(c >= '0' && c <= '9') ||
			c == '-' || c == '_'
		if !ok {
			t.Errorf("verifier contains illegal char %q (full=%s)", c, v)
		}
	}
}

// TestGeneratePKCEVerifier_UniquePerCall pins that two consecutive calls
// don't return the same verifier — would mean rand source is broken.
func TestGeneratePKCEVerifier_UniquePerCall(t *testing.T) {
	a, err := generatePKCEVerifier()
	if err != nil {
		t.Fatalf("a: %v", err)
	}
	b, err := generatePKCEVerifier()
	if err != nil {
		t.Fatalf("b: %v", err)
	}
	if a == b {
		t.Errorf("two verifiers identical (rand source broken?): %s", a)
	}
}

// TestPKCEChallenge_IsBase64URLSHA256OfVerifier verifies the documented
// S256 transformation: challenge = base64url(sha256(verifier)), no padding.
func TestPKCEChallenge_IsBase64URLSHA256OfVerifier(t *testing.T) {
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk" // RFC 7636 Appendix B example
	wantChallenge := "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	if got := pkceChallenge(verifier); got != wantChallenge {
		t.Errorf("pkceChallenge(%q) = %q, want %q", verifier, got, wantChallenge)
	}
	// Also verify the property generally: it must equal a manual computation.
	verifier2, _ := generatePKCEVerifier()
	sum := sha256.Sum256([]byte(verifier2))
	manual := base64.RawURLEncoding.EncodeToString(sum[:])
	if got := pkceChallenge(verifier2); got != manual {
		t.Errorf("pkceChallenge property violated: %q vs %q", got, manual)
	}
}

// TestBuildAuthURL_HasRequiredParams locks the URL shape: code_challenge,
// code_challenge_method=S256, state, scopes, response_type=code.
func TestBuildAuthURL_HasRequiredParams(t *testing.T) {
	scopes := []string{"user-read-playback-state", "user-modify-playback-state"}
	authURL, verifier, state, err := BuildAuthURL("my-client-id", scopes)
	if err != nil {
		t.Fatalf("BuildAuthURL: %v", err)
	}
	if !strings.HasPrefix(authURL, SpotifyAuthURL+"?") {
		t.Errorf("authURL doesn't start with Spotify endpoint: %s", authURL)
	}

	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	q := parsed.Query()

	if got := q.Get("client_id"); got != "my-client-id" {
		t.Errorf("client_id: got %q, want %q", got, "my-client-id")
	}
	if got := q.Get("response_type"); got != "code" {
		t.Errorf("response_type: got %q, want %q", got, "code")
	}
	if got := q.Get("code_challenge_method"); got != "S256" {
		t.Errorf("code_challenge_method: got %q, want S256", got)
	}
	if got := q.Get("code_challenge"); got == "" {
		t.Errorf("code_challenge: empty")
	}
	if got := q.Get("state"); got != state {
		t.Errorf("state mismatch: query %q vs returned %q", got, state)
	}
	if got := q.Get("scope"); got != strings.Join(scopes, " ") {
		t.Errorf("scope: got %q, want %q", got, strings.Join(scopes, " "))
	}
	// Verify challenge derives from verifier.
	if got := q.Get("code_challenge"); got != pkceChallenge(verifier) {
		t.Errorf("code_challenge doesn't derive from returned verifier")
	}
	// Verifier itself must NOT appear in the URL (only the challenge does).
	if strings.Contains(authURL, verifier) {
		t.Errorf("verifier leaked into authURL: %s", authURL)
	}
}

// TestBuildAuthURL_EmptyClientID returns an error rather than emitting a
// half-broken URL.
func TestBuildAuthURL_EmptyClientID(t *testing.T) {
	_, _, _, err := BuildAuthURL("", []string{"foo"})
	if err == nil {
		t.Fatal("expected error for empty clientID, got nil")
	}
}

// TestExchangeCode_HappyPath stands up a mock token endpoint, runs
// ExchangeCode, and verifies the returned TokenResponse plus the form-
// encoded body the mock server received.
func TestExchangeCode_HappyPath(t *testing.T) {
	var gotForm url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		gotForm = r.PostForm
		if ct := r.Header.Get("Content-Type"); !strings.Contains(ct, "application/x-www-form-urlencoded") {
			t.Errorf("Content-Type: got %q, want form-encoded", ct)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"access_token": "BQA-access-abc",
			"token_type": "Bearer",
			"scope": "user-read-playback-state",
			"expires_in": 3600,
			"refresh_token": "AQA-refresh-xyz"
		}`))
	}))
	defer srv.Close()
	restore := withTokenEndpoint(srv.URL)
	defer restore()

	tok, err := ExchangeCode("my-cid", "auth-code-123", "verifier-456", "http://127.0.0.1:0/callback")
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}

	if tok.AccessToken != "BQA-access-abc" {
		t.Errorf("AccessToken: got %q, want %q", tok.AccessToken, "BQA-access-abc")
	}
	if tok.RefreshToken != "AQA-refresh-xyz" {
		t.Errorf("RefreshToken: got %q, want %q", tok.RefreshToken, "AQA-refresh-xyz")
	}
	if tok.ExpiresIn != 3600 {
		t.Errorf("ExpiresIn: got %d, want 3600", tok.ExpiresIn)
	}
	if tok.TokenType != "Bearer" {
		t.Errorf("TokenType: got %q, want Bearer", tok.TokenType)
	}

	// Verify the request form.
	if got := gotForm.Get("grant_type"); got != "authorization_code" {
		t.Errorf("grant_type: got %q, want authorization_code", got)
	}
	if got := gotForm.Get("code"); got != "auth-code-123" {
		t.Errorf("code: got %q, want auth-code-123", got)
	}
	if got := gotForm.Get("code_verifier"); got != "verifier-456" {
		t.Errorf("code_verifier: got %q, want verifier-456", got)
	}
	if got := gotForm.Get("client_id"); got != "my-cid" {
		t.Errorf("client_id: got %q, want my-cid", got)
	}
	if got := gotForm.Get("redirect_uri"); got != "http://127.0.0.1:0/callback" {
		t.Errorf("redirect_uri: got %q, want callback URL", got)
	}
}

// TestExchangeCode_Non2xxSurfacesBody verifies a 400 from Spotify is
// returned with the body in the error message — actionable in logs.
func TestExchangeCode_Non2xxSurfacesBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"code already used"}`))
	}))
	defer srv.Close()
	restore := withTokenEndpoint(srv.URL)
	defer restore()

	_, err := ExchangeCode("cid", "code", "verifier", "http://127.0.0.1:0/callback")
	if err == nil {
		t.Fatal("expected error for 400 response, got nil")
	}
	if !strings.Contains(err.Error(), "invalid_grant") {
		t.Errorf("expected error to mention invalid_grant, got: %v", err)
	}
}

// TestExchangeCode_InputValidation pins that empty inputs short-circuit
// without an HTTP call.
func TestExchangeCode_InputValidation(t *testing.T) {
	cases := []struct {
		name      string
		cid, code string
		verifier  string
		redirect  string
	}{
		{"empty client id", "", "code", "v", "https://e"},
		{"empty code", "c", "", "v", "https://e"},
		{"empty verifier", "c", "code", "", "https://e"},
		{"empty redirect", "c", "code", "v", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ExchangeCode(tc.cid, tc.code, tc.verifier, tc.redirect)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

// TestRefreshToken_HappyPath verifies the refresh-token grant call.
func TestRefreshToken_HappyPath(t *testing.T) {
	var gotForm url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		// Spotify may omit refresh_token on refresh — simulate that.
		_, _ = w.Write([]byte(`{
			"access_token": "BQA-NEW-access",
			"token_type": "Bearer",
			"scope": "user-read-playback-state",
			"expires_in": 1800
		}`))
	}))
	defer srv.Close()
	restore := withTokenEndpoint(srv.URL)
	defer restore()

	tok, err := RefreshToken("cid", "old-refresh-token")
	if err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	if tok.AccessToken != "BQA-NEW-access" {
		t.Errorf("AccessToken: got %q, want BQA-NEW-access", tok.AccessToken)
	}
	if tok.ExpiresIn != 1800 {
		t.Errorf("ExpiresIn: got %d, want 1800", tok.ExpiresIn)
	}
	if tok.RefreshToken != "" {
		t.Errorf("RefreshToken: got %q, want empty (Spotify omits)", tok.RefreshToken)
	}
	if got := gotForm.Get("grant_type"); got != "refresh_token" {
		t.Errorf("grant_type: got %q, want refresh_token", got)
	}
	if got := gotForm.Get("refresh_token"); got != "old-refresh-token" {
		t.Errorf("refresh_token: got %q, want old-refresh-token", got)
	}
}

// TestRefreshToken_FailureWrapsErrTokenRefreshFailed verifies the wrapping
// contract: callers using errors.Is(err, ErrTokenRefreshFailed) can detect
// the failure mode.
func TestRefreshToken_FailureWrapsErrTokenRefreshFailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer srv.Close()
	restore := withTokenEndpoint(srv.URL)
	defer restore()

	_, err := RefreshToken("cid", "bad-refresh")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrTokenRefreshFailed) {
		t.Errorf("expected errors.Is(err, ErrTokenRefreshFailed); got: %v", err)
	}
}

// TestStartCallbackServer_CapturesCode stands up the one-shot server and
// hits it like Spotify would.
func TestStartCallbackServer_CapturesCode(t *testing.T) {
	codeCh, errCh, closeFn := startCallbackOnFreePort(t)
	defer closeFn()

	port := portFromAddr(t, <-portCh) // see helper below
	// Wait briefly for the listener to be ready (Listen is sync, so this is
	// effectively a no-op — but cheap to be safe).
	time.Sleep(20 * time.Millisecond)

	resp, err := http.Get("http://127.0.0.1:" + port + "/callback?code=mycode&state=mystate")
	if err != nil {
		t.Fatalf("GET callback: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("callback status: got %d, want 200", resp.StatusCode)
	}

	select {
	case res := <-codeCh:
		if res.Code != "mycode" {
			t.Errorf("Code: got %q, want mycode", res.Code)
		}
		if res.State != "mystate" {
			t.Errorf("State: got %q, want mystate", res.State)
		}
		if res.Err != "" {
			t.Errorf("Err: got %q, want empty", res.Err)
		}
	case err := <-errCh:
		t.Fatalf("unexpected errCh send: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for codeCh")
	}
}

// TestStartCallbackServer_HandlesError verifies the error= query param
// path (e.g. user clicks Cancel on Spotify's consent page).
func TestStartCallbackServer_HandlesError(t *testing.T) {
	codeCh, _, closeFn := startCallbackOnFreePort(t)
	defer closeFn()

	port := portFromAddr(t, <-portCh)
	time.Sleep(20 * time.Millisecond)

	resp, err := http.Get("http://127.0.0.1:" + port + "/callback?error=access_denied&state=s")
	if err != nil {
		t.Fatalf("GET callback: %v", err)
	}
	_ = resp.Body.Close()

	select {
	case res := <-codeCh:
		if res.Err != "access_denied" {
			t.Errorf("Err: got %q, want access_denied", res.Err)
		}
		if res.Code != "" {
			t.Errorf("Code: got %q, want empty on error path", res.Code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for codeCh on error path")
	}
}

// TestRunPKCEFlow_HappyPath end-to-end: state-mismatch defense + token
// exchange + cfg mutation.
func TestRunPKCEFlow_HappyPath(t *testing.T) {
	// Mock token endpoint.
	tokSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"access_token":"acc-1",
			"token_type":"Bearer",
			"scope":"user-read-playback-state",
			"expires_in":3600,
			"refresh_token":"ref-1"
		}`))
	}))
	defer tokSrv.Close()
	restore := withTokenEndpoint(tokSrv.URL)
	defer restore()

	// openBrowser stub: parse the URL we'd open, fetch the local callback
	// with code+state. Mimics the user completing consent in a browser.
	cfg := &model.SpotifyConfig{}
	openBrowser := func(rawURL string) error {
		u, err := url.Parse(rawURL)
		if err != nil {
			return err
		}
		state := u.Query().Get("state")
		redirect := u.Query().Get("redirect_uri")
		go func() {
			// Tiny delay to ensure the callback server is ready.
			time.Sleep(50 * time.Millisecond)
			_, _ = http.Get(redirect + "?code=themcode&state=" + state)
		}()
		return nil
	}

	if err := RunPKCEFlow("my-cid", openBrowser, cfg); err != nil {
		t.Fatalf("RunPKCEFlow: %v", err)
	}
	if cfg.AccessToken != "acc-1" {
		t.Errorf("cfg.AccessToken: got %q, want acc-1", cfg.AccessToken)
	}
	if cfg.RefreshToken != "ref-1" {
		t.Errorf("cfg.RefreshToken: got %q, want ref-1", cfg.RefreshToken)
	}
	if cfg.ExpiresAt.IsZero() {
		t.Errorf("cfg.ExpiresAt: expected non-zero (3600s from now)")
	}
	if cfg.ClientID != "my-cid" {
		t.Errorf("cfg.ClientID: got %q, want my-cid", cfg.ClientID)
	}
}

// TestRunPKCEFlow_StateMismatch verifies CSRF defense: a callback with a
// state value that doesn't match the one BuildAuthURL emitted is refused.
func TestRunPKCEFlow_StateMismatch(t *testing.T) {
	tokSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("token endpoint should NOT be called on state mismatch")
	}))
	defer tokSrv.Close()
	restore := withTokenEndpoint(tokSrv.URL)
	defer restore()

	cfg := &model.SpotifyConfig{}
	openBrowser := func(rawURL string) error {
		u, _ := url.Parse(rawURL)
		redirect := u.Query().Get("redirect_uri")
		go func() {
			time.Sleep(50 * time.Millisecond)
			// Send a DIFFERENT state than the one in the auth URL.
			_, _ = http.Get(redirect + "?code=c&state=ATTACKER_STATE")
		}()
		return nil
	}

	err := RunPKCEFlow("cid", openBrowser, cfg)
	if err == nil {
		t.Fatal("expected state-mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "state mismatch") {
		t.Errorf("expected state-mismatch in error, got: %v", err)
	}
	// cfg must NOT have been mutated.
	if cfg.AccessToken != "" {
		t.Errorf("cfg mutated on failure: AccessToken=%q", cfg.AccessToken)
	}
}

// TestRunPKCEFlow_InputValidation pins the guard clauses.
func TestRunPKCEFlow_InputValidation(t *testing.T) {
	cases := []struct {
		name        string
		clientID    string
		openBrowser func(string) error
		cfg         *model.SpotifyConfig
	}{
		{"nil cfg", "cid", func(string) error { return nil }, nil},
		{"empty clientID", "", func(string) error { return nil }, &model.SpotifyConfig{}},
		{"nil openBrowser", "cid", nil, &model.SpotifyConfig{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := RunPKCEFlow(tc.clientID, tc.openBrowser, tc.cfg); err == nil {
				t.Errorf("expected error, got nil")
			}
		})
	}
}

// TestTokenResponse_RoundTrip verifies the JSON shape matches Spotify.
func TestTokenResponse_RoundTrip(t *testing.T) {
	in := `{
		"access_token":"a",
		"token_type":"Bearer",
		"scope":"s1 s2",
		"expires_in":7200,
		"refresh_token":"r"
	}`
	var tok TokenResponse
	if err := json.Unmarshal([]byte(in), &tok); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if tok.AccessToken != "a" || tok.RefreshToken != "r" || tok.ExpiresIn != 7200 {
		t.Errorf("decoded unexpected: %+v", tok)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// withTokenEndpoint overrides tokenEndpoint for the duration of a test.
// The returned func restores the original.
func withTokenEndpoint(u string) func() {
	prev := tokenEndpoint
	tokenEndpoint = u
	return func() { tokenEndpoint = prev }
}

// portCh / startCallbackOnFreePort are a helper for tests that need to
// know which port the one-shot server is listening on. We pick the port
// up-front, push it to portCh, then call StartCallbackServer with it.
//
// portCh is buffered with capacity 1; each call pushes the port it
// allocated and the test reads it back. Helper makes tests less brittle
// than guessing port numbers.
var portCh = make(chan int, 8)

func startCallbackOnFreePort(t *testing.T) (<-chan CallbackResult, <-chan error, func()) {
	t.Helper()
	port, err := pickFreePort()
	if err != nil {
		t.Fatalf("pickFreePort: %v", err)
	}
	portCh <- port
	return StartCallbackServer(port)
}

// portFromAddr returns the int port as a string. We accept an int via the
// channel and stringify here, because http.Get wants strings.
func portFromAddr(t *testing.T, port int) string {
	t.Helper()
	return itoaTest(port)
}

func itoaTest(n int) string {
	// Avoid importing strconv at the file top to keep the test imports tight.
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+(n%10))) + digits
		n /= 10
	}
	if neg {
		return "-" + digits
	}
	return digits
}
