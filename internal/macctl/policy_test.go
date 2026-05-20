package macctl

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// withTempHome redirects $HOME to t.TempDir(). The macctl.PolicyPath helper
// reads paths.JarvisHome which in turn reads os.UserHomeDir, so this is the
// canonical way to point the policy file into a per-test scratch dir.
//
// Matches the pattern in internal/setup/setup_test.go::setupTestHome.
func withTempHome(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("macctl is darwin-only; tests skipped on windows")
	}
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	return tmp
}

// TestDecisionIsValid pins the three legal Decision values and asserts
// every other string is rejected. If a future refactor adds a fourth
// decision, this test must be updated in lockstep with consumers (the
// Settings UI, the Controller dispatch).
func TestDecisionIsValid(t *testing.T) {
	valid := []Decision{DecisionAllow, DecisionAsk, DecisionDeny}
	for _, d := range valid {
		if !d.IsValid() {
			t.Errorf("expected Decision(%q) to be valid", d)
		}
	}
	invalid := []Decision{"", "ALLOW", "permit", "block", "yes", "no", "true"}
	for _, d := range invalid {
		if d.IsValid() {
			t.Errorf("expected Decision(%q) to be invalid", d)
		}
	}
}

// TestNewDefaultPolicyAllowAskSplit is the central contract assertion: it
// pins EVERY tool the daemon knows about to its expected default decision.
// If TASK-004 (daemon tool registry) adds a new tool, this test should be
// updated in the same PR so allow/ask defaults stay explicit. Forgetting to
// update means Check() falls back to DecisionAsk for the new tool, which is
// safe but surprising.
func TestNewDefaultPolicyAllowAskSplit(t *testing.T) {
	p := NewDefaultPolicy()
	if p == nil {
		t.Fatal("NewDefaultPolicy returned nil")
	}
	if p.Decisions == nil {
		t.Fatal("NewDefaultPolicy returned policy with nil Decisions map")
	}

	// Allow-by-default tools — pinned. Mirrors the TASK-003 spec.
	allow := []string{
		"mac_spotlight",
		"mac_clipboard_get",
		"mac_screenshot",
		"mac_list_shortcuts",
		"spotify_what_is_playing",
		"spotify_pause",
		"spotify_resume",
		"spotify_skip",
		"spotify_previous",
		"spotify_set_volume",
		"spotify_like_current",
		"spotify_queue",
		"spotify_search_and_play",
	}
	for _, tool := range allow {
		got := p.Check(tool)
		if got != DecisionAllow {
			t.Errorf("Check(%q) = %q, want %q (allow by default)", tool, got, DecisionAllow)
		}
	}

	// Ask-by-default tools — pinned. Destructive / surprising side effects.
	ask := []string{
		"mac_open_app",
		"mac_quit_app",
		"mac_focus_window",
		"mac_set_volume",
		"mac_mute",
		"mac_unmute",
		"mac_set_brightness",
		"mac_toggle_dnd",
		"mac_open_path",
		"mac_clipboard_set",
		"mac_run_shortcut",
	}
	for _, tool := range ask {
		got := p.Check(tool)
		if got != DecisionAsk {
			t.Errorf("Check(%q) = %q, want %q (ask by default)", tool, got, DecisionAsk)
		}
	}

	// No deny defaults: scan every entry and assert none are deny.
	for tool, d := range p.Decisions {
		if d == DecisionDeny {
			t.Errorf("tool %q defaults to deny; defaults must be allow or ask only", tool)
		}
	}

	// Every tool in the policy must be in either the allow or ask set —
	// catches stray additions to defaultAllowTools/defaultAskTools that
	// weren't mirrored into this test.
	known := make(map[string]struct{}, len(allow)+len(ask))
	for _, tool := range allow {
		known[tool] = struct{}{}
	}
	for _, tool := range ask {
		known[tool] = struct{}{}
	}
	for tool := range p.Decisions {
		if _, ok := known[tool]; !ok {
			t.Errorf("policy contains tool %q that is not pinned in this test; update the allow/ask slices", tool)
		}
	}

	// Size sanity: 13 allow + 11 ask = 24 known tools.
	if want, got := len(allow)+len(ask), len(p.Decisions); want != got {
		t.Errorf("default policy size: got %d, want %d", got, want)
	}
}

// TestCheckUnknownToolFallsBackToAsk asserts the safe default for any tool
// not present in the policy map. This is the contract that lets new tools
// ship in the daemon registry before the policy file gains an explicit
// entry for them.
func TestCheckUnknownToolFallsBackToAsk(t *testing.T) {
	p := NewDefaultPolicy()
	got := p.Check("totally-unknown-tool")
	if got != DecisionAsk {
		t.Errorf("Check(unknown) = %q, want %q", got, DecisionAsk)
	}
}

// TestCheckNilPolicyFallsBackToAsk asserts the defensive nil-receiver
// guard. The Controller refuses nil at construction, but if a future caller
// slips through, falling back to ask is preferable to a nil-pointer panic
// in the middle of a tool dispatch.
func TestCheckNilPolicyFallsBackToAsk(t *testing.T) {
	var p *Policy // intentionally nil
	if got := p.Check("mac_open_app"); got != DecisionAsk {
		t.Errorf("nil Policy.Check = %q, want %q", got, DecisionAsk)
	}
}

// TestSetThenCheck asserts the in-memory mutation path. Set should not
// touch disk (verified separately by the round-trip test) but Check should
// see the new value immediately.
func TestSetThenCheck(t *testing.T) {
	p := NewDefaultPolicy()

	// Default for mac_open_app is ask — flip to deny and verify.
	p.Set("mac_open_app", DecisionDeny)
	if got := p.Check("mac_open_app"); got != DecisionDeny {
		t.Errorf("after Set(deny), Check = %q, want %q", got, DecisionDeny)
	}

	// New tool, not in defaults — Set should add it.
	p.Set("mac_future_tool", DecisionAllow)
	if got := p.Check("mac_future_tool"); got != DecisionAllow {
		t.Errorf("after Set on new tool, Check = %q, want %q", got, DecisionAllow)
	}
}

// TestSetIgnoresInvalidInput asserts that Set silently drops bad input
// (empty tool name, malformed decision string) rather than corrupting the
// map. The API layer may forward user-supplied strings into Set, so this
// guard matters.
func TestSetIgnoresInvalidInput(t *testing.T) {
	p := NewDefaultPolicy()
	before := len(p.Decisions)

	p.Set("", DecisionAllow)        // empty tool name
	p.Set("mac_open_app", "permit") // invalid decision
	p.Set("mac_future", "")         // invalid decision (empty)
	p.Set("mac_future2", "ALLOW")   // case-sensitive: wrong case

	if got := len(p.Decisions); got != before {
		t.Errorf("after invalid Sets, map size = %d, want %d (unchanged)", got, before)
	}
	// Verify mac_open_app default wasn't overwritten.
	if got := p.Check("mac_open_app"); got != DecisionAsk {
		t.Errorf("after invalid Set on mac_open_app, Check = %q, want %q", got, DecisionAsk)
	}
}

// TestSetOnNilPolicyDoesNotPanic asserts the nil-receiver guard for Set.
func TestSetOnNilPolicyDoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("nil Policy.Set panicked: %v", r)
		}
	}()
	var p *Policy
	p.Set("mac_open_app", DecisionAllow) // must not panic
}

// TestSaveLoadRoundTrip is the core persistence test: serialize a populated
// policy, deserialize it from disk, and assert the maps are equal. Also
// asserts the destination file exists with the expected JSON shape.
func TestSaveLoadRoundTrip(t *testing.T) {
	home := withTempHome(t)
	path := filepath.Join(home, ".jarvis", "policy.json")

	original := NewDefaultPolicy()
	// Mutate a few entries so we'd notice if Save dropped any.
	original.Set("mac_open_app", DecisionDeny)
	original.Set("mac_quit_app", DecisionAllow)
	original.Set("custom_tool", DecisionAllow)

	if err := original.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Verify the file exists at the requested path (acceptance criterion).
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("policy file not created at %s: %v", path, err)
	}
	if info.Size() == 0 {
		t.Error("policy file is empty")
	}

	// Verify the file is human-readable JSON with the documented shape.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back file: %v", err)
	}
	if !strings.Contains(string(raw), `"decisions"`) {
		t.Errorf("file missing top-level 'decisions' key: %s", raw)
	}

	// Round-trip via Load and compare maps.
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded == nil {
		t.Fatal("Load returned nil policy")
	}

	original.mu.RLock()
	loaded.mu.RLock()
	if len(loaded.Decisions) != len(original.Decisions) {
		t.Errorf("decision count mismatch: got %d, want %d",
			len(loaded.Decisions), len(original.Decisions))
	}
	for tool, want := range original.Decisions {
		if got := loaded.Decisions[tool]; got != want {
			t.Errorf("tool %q: got %q, want %q", tool, got, want)
		}
	}
	loaded.mu.RUnlock()
	original.mu.RUnlock()
}

// TestSaveAtomicNoTmpLeft asserts the tmp-and-rename pattern leaves no
// .tmp sibling after a successful Save. Without the rename step a crash
// would leave a half-written file; this test would catch a regression that
// switched to a direct WriteFile.
func TestSaveAtomicNoTmpLeft(t *testing.T) {
	home := withTempHome(t)
	dir := filepath.Join(home, ".jarvis")
	path := filepath.Join(dir, "policy.json")

	p := NewDefaultPolicy()
	if err := p.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Final file must exist.
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("final file missing: %v", err)
	}
	// Temp file must NOT exist.
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("tmp file %s should not exist after Save; err=%v", path+".tmp", err)
	}

	// Re-Save: rename must overwrite cleanly (this is the production
	// upgrade path — the user clicks Save in Settings repeatedly).
	if err := p.Save(path); err != nil {
		t.Fatalf("second Save: %v", err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("tmp file lingered after second Save; err=%v", err)
	}
}

// TestSaveAtomicOverwriteIntegrity simulates "in-place upgrade" — write,
// modify, write again — and asserts the second write's contents fully
// replace the first (no leftover bytes from a larger first write). This
// catches a hypothetical bug where Save used os.OpenFile with O_TRUNC
// missing on the destination instead of the rename.
func TestSaveAtomicOverwriteIntegrity(t *testing.T) {
	home := withTempHome(t)
	path := filepath.Join(home, ".jarvis", "policy.json")

	big := NewDefaultPolicy()
	// Add a long-named custom tool so the first write is meaningfully larger
	// than the second.
	big.Set("zzz_very_long_tool_name_to_inflate_the_file_size_just_for_this_test", DecisionAllow)
	if err := big.Save(path); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	bigBytes, _ := os.ReadFile(path)

	// Tiny policy with just one decision.
	small := &Policy{Decisions: map[string]Decision{"a": DecisionAllow}}
	if err := small.Save(path); err != nil {
		t.Fatalf("second Save: %v", err)
	}
	smallBytes, _ := os.ReadFile(path)

	if len(smallBytes) >= len(bigBytes) {
		t.Errorf("expected second write to be smaller; big=%d small=%d", len(bigBytes), len(smallBytes))
	}
	// Verify the tiny content is what's now on disk.
	var parsed struct {
		Decisions map[string]Decision `json:"decisions"`
	}
	if err := json.Unmarshal(smallBytes, &parsed); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(parsed.Decisions) != 1 || parsed.Decisions["a"] != DecisionAllow {
		t.Errorf("after second Save, decisions = %v, want {a: allow}", parsed.Decisions)
	}
}

// TestLoadMissingFileReturnsDefaults asserts that Load on a non-existent
// path returns the default policy with a nil error. This is the first-run
// contract: the caller can unconditionally call Load on startup without
// checking for ErrNotExist.
func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	home := withTempHome(t)
	path := filepath.Join(home, ".jarvis", "policy.json")

	p, err := Load(path)
	if err != nil {
		t.Fatalf("Load on missing file: unexpected err: %v", err)
	}
	if p == nil {
		t.Fatal("Load on missing file returned nil policy")
	}

	// Spot-check a known default.
	if got := p.Check("mac_open_app"); got != DecisionAsk {
		t.Errorf("default-after-missing: Check(mac_open_app) = %q, want %q", got, DecisionAsk)
	}
	if got := p.Check("mac_spotlight"); got != DecisionAllow {
		t.Errorf("default-after-missing: Check(mac_spotlight) = %q, want %q", got, DecisionAllow)
	}
}

// TestLoadMalformedJSONReturnsError asserts that broken JSON surfaces as an
// error (not silent defaults). The caller wants to distinguish first-run
// from "the user pasted garbage" so it can show a Settings banner.
func TestLoadMalformedJSONReturnsError(t *testing.T) {
	home := withTempHome(t)
	dir := filepath.Join(home, ".jarvis")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "policy.json")
	if err := os.WriteFile(path, []byte("{this is not json"), 0o644); err != nil {
		t.Fatalf("write garbage: %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Error("Load on malformed JSON: want error, got nil")
	}
}

// TestLoadCoercesInvalidDecisionsToAsk asserts that an otherwise-valid JSON
// file containing a typo'd decision value (e.g. "permit" instead of "allow")
// coerces that entry to ask rather than failing the whole load. Defensive
// behavior — a typo in policy.json shouldn't disable all permission gates.
func TestLoadCoercesInvalidDecisionsToAsk(t *testing.T) {
	home := withTempHome(t)
	dir := filepath.Join(home, ".jarvis")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "policy.json")

	payload := []byte(`{"decisions":{"mac_open_app":"permit","mac_spotlight":"allow","":"allow"}}`)
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	p, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := p.Check("mac_open_app"); got != DecisionAsk {
		t.Errorf("typo'd decision: Check = %q, want %q (coerced)", got, DecisionAsk)
	}
	if got := p.Check("mac_spotlight"); got != DecisionAllow {
		t.Errorf("valid decision: Check = %q, want %q", got, DecisionAllow)
	}
	// Empty-string tool name must have been dropped, not retained.
	if _, ok := p.Decisions[""]; ok {
		t.Error("empty tool name should have been dropped during Load")
	}
}

// TestLoadEmptyPathReturnsError asserts the input-validation guard.
func TestLoadEmptyPathReturnsError(t *testing.T) {
	if _, err := Load(""); err == nil {
		t.Error("Load(\"\"): want error, got nil")
	}
}

// TestSaveEmptyPathReturnsError mirrors the Load guard.
func TestSaveEmptyPathReturnsError(t *testing.T) {
	p := NewDefaultPolicy()
	if err := p.Save(""); err == nil {
		t.Error("Save(\"\"): want error, got nil")
	}
}

// TestSaveNilReceiverReturnsError asserts defensive handling of the
// pathological nil-pointer call.
func TestSaveNilReceiverReturnsError(t *testing.T) {
	var p *Policy
	if err := p.Save("/tmp/should-not-be-written"); err == nil {
		t.Error("nil Policy.Save: want error, got nil")
	}
}

// TestPolicyPathRoutedThroughPaths asserts that PolicyPath honors HOME
// redirection. Without this, tests that swap HOME would silently still
// write to the developer's real ~/.jarvis directory.
func TestPolicyPathRoutedThroughPaths(t *testing.T) {
	home := withTempHome(t)
	want := filepath.Join(home, ".jarvis", "policy.json")
	if got := PolicyPath(); got != want {
		t.Errorf("PolicyPath = %q, want %q", got, want)
	}
}

// TestConcurrentReadsAndWritesNoRace exercises the RWMutex under -race. If
// the lock is missing or wrong, the race detector will flag it and the
// test will fail. The body is intentionally short — under -race the
// scheduler interleaving is the test, not the iteration count.
func TestConcurrentReadsAndWritesNoRace(t *testing.T) {
	p := NewDefaultPolicy()

	var wg sync.WaitGroup
	// Many readers, a few writers, racing each other.
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_ = p.Check("mac_open_app")
				_ = p.Check("spotify_pause")
				_ = p.Check("unknown_tool")
			}
		}()
	}
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				p.Set("mac_open_app", DecisionAllow)
				p.Set("mac_open_app", DecisionAsk)
				_ = id
			}
		}(i)
	}
	wg.Wait()
}
