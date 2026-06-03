package gcal

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/namanchopra/jarvis/internal/model"
)

// noopBrowser is a stub `openBrowser` for tests that never need the URL
// actually opened. Returns nil and discards the URL — so the test process
// never spawns a real browser window.
func noopBrowser(string) error { return nil }

// TestRunOAuthFlowEmptyClientID verifies the input-validation guard runs
// BEFORE we open a browser or start a callback server. Returning
// ErrInvalidConfig (wrapped) is the documented contract so Settings can
// show "configure credentials" rather than "network failed".
func TestRunOAuthFlowEmptyClientID(t *testing.T) {
	cfg := &model.GCalConfig{}
	browserCalled := false
	browser := func(string) error {
		browserCalled = true
		return nil
	}

	err := RunOAuthFlow("", "the-secret", browser, cfg)
	if err == nil {
		t.Fatal("expected error for empty clientID, got nil")
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("expected errors.Is(err, ErrInvalidConfig); got: %v", err)
	}
	if browserCalled {
		t.Error("openBrowser was invoked despite empty clientID — validation must run first")
	}
	// cfg must NOT be mutated on validation failure.
	if cfg.ClientID != "" || cfg.AccessToken != "" {
		t.Errorf("cfg mutated on validation failure: %+v", *cfg)
	}
}

// TestRunOAuthFlowEmptyClientSecret mirrors the clientID guard for the
// secret. Both halves of the credential are required — desktop OAuth clients
// are "confidential" in Google's taxonomy and the token exchange will reject
// a missing client_secret.
func TestRunOAuthFlowEmptyClientSecret(t *testing.T) {
	cfg := &model.GCalConfig{}
	browserCalled := false
	browser := func(string) error {
		browserCalled = true
		return nil
	}

	err := RunOAuthFlow("the-id", "", browser, cfg)
	if err == nil {
		t.Fatal("expected error for empty clientSecret, got nil")
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("expected errors.Is(err, ErrInvalidConfig); got: %v", err)
	}
	if browserCalled {
		t.Error("openBrowser was invoked despite empty clientSecret — validation must run first")
	}
	if cfg.ClientID != "" || cfg.AccessToken != "" {
		t.Errorf("cfg mutated on validation failure: %+v", *cfg)
	}
}

// TestRunOAuthFlowStateMismatch is the CSRF-guard regression test. The
// openBrowser stub parses the authorize URL Jarvis would have opened,
// extracts the redirect_uri (which points at the one-shot callback server
// already running on a free loopback port), and hits the callback with a
// state value that does NOT match the nonce. The CSRF guard runs BEFORE
// the token exchange so we never need to mock Google's token endpoint —
// RunOAuthFlow must short-circuit with a state-mismatch error and cfg must
// be left untouched.
func TestRunOAuthFlowStateMismatch(t *testing.T) {
	cfg := &model.GCalConfig{}

	browser := func(rawURL string) error {
		u, err := url.Parse(rawURL)
		if err != nil {
			return err
		}
		redirect := u.Query().Get("redirect_uri")
		if redirect == "" {
			t.Errorf("authorize URL missing redirect_uri: %s", rawURL)
			return nil
		}
		go func() {
			// Tiny delay so the callback server is definitely accepting
			// before we hit it. The listener is bound synchronously
			// inside StartCallbackServer, but Serve is in a goroutine.
			time.Sleep(50 * time.Millisecond)
			// Deliberately send a DIFFERENT state than the one in the
			// authorize URL — the CSRF guard must catch this.
			resp, getErr := http.Get(redirect + "?code=stub-code&state=ATTACKER_STATE")
			if getErr == nil {
				_ = resp.Body.Close()
			}
		}()
		return nil
	}

	err := RunOAuthFlow("my-cid", "my-secret", browser, cfg)
	if err == nil {
		t.Fatal("expected state-mismatch error, got nil")
	}
	// The exact phrase is part of the documented contract — Settings copy
	// branches on it for the user-visible "the sign-in link was tampered
	// with" message. Pin both halves so a rename of the error doesn't
	// silently slip past.
	if !strings.Contains(err.Error(), "state nonce mismatch") {
		t.Errorf("expected error to mention 'state nonce mismatch', got: %v", err)
	}
	if !strings.Contains(err.Error(), "csrf") {
		t.Errorf("expected error to mention 'csrf' marker, got: %v", err)
	}

	// cfg must NOT have been mutated on a CSRF failure — that's the whole
	// point of failing before the token exchange.
	if cfg.AccessToken != "" || cfg.RefreshToken != "" || cfg.ClientID != "" {
		t.Errorf("cfg mutated despite CSRF failure: %+v", *cfg)
	}
}

// TestRunOAuthFlowNilCfg pins the nil-cfg guard. Documented contract: a
// nil cfg returns an error rather than panicking with a nil dereference
// later inside the flow.
func TestRunOAuthFlowNilCfg(t *testing.T) {
	err := RunOAuthFlow("cid", "secret", noopBrowser, nil)
	if err == nil {
		t.Fatal("expected error for nil cfg, got nil")
	}
}

// TestRunOAuthFlowNilBrowser pins the nil-openBrowser guard. The flow
// cannot proceed without a way to launch the user's browser, so we fail
// fast rather than panicking on a nil func call mid-flow.
func TestRunOAuthFlowNilBrowser(t *testing.T) {
	cfg := &model.GCalConfig{}
	err := RunOAuthFlow("cid", "secret", nil, cfg)
	if err == nil {
		t.Fatal("expected error for nil openBrowser, got nil")
	}
}
