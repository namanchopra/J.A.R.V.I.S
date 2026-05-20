// Package macctl provides programmatic control of the host macOS system —
// app launch/focus, audio/display tunables, clipboard I/O, screenshots, and
// macOS Shortcuts invocation — exposed as tools the Jarvis daemon's LLM can
// call via the daemon ⇄ Wails tool bridge.
//
// This file implements the permissions policy that gates destructive macctl
// (and adjacent spotify_*) tool invocations. Every tool the LLM can route to
// is associated with one of three decisions:
//
//	DecisionAllow  — execute without prompting (read-only / safe tools).
//	DecisionAsk    — prompt the user in the Jarvis HUD before executing.
//	DecisionDeny   — refuse outright; the controller surfaces an error.
//
// The policy is persisted as JSON at ~/.jarvis/policy.json, written
// atomically (tmp + rename) so a crash mid-write never leaves the file
// half-written. The default policy ships with read-only tools set to allow
// and destructive tools set to ask; users opt in to deny via the Settings
// UI (TASK-017, TASK-018) — there are no deny defaults.
//
// The public surface in this file (type names + method signatures) is a
// contract: TASK-002's macctl.Controller constructor accepts *Policy and
// every Controller method calls Policy.Check before performing its action.
// Renaming types or changing method signatures will break callers downstream.
package macctl

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/namanchopra/jarvis/internal/paths"
)

// Decision is the per-tool permission verdict. The wire format (JSON value)
// is a lowercase string so users hand-editing ~/.jarvis/policy.json can read
// it without consulting docs.
type Decision string

const (
	// DecisionAllow lets the tool execute without prompting. Reserved for
	// read-only / safe tools (Spotlight, ClipboardGet, Screenshot, etc.).
	DecisionAllow Decision = "allow"

	// DecisionAsk prompts the user in the HUD before executing. The safest
	// non-trivial default — used for any tool whose side effects could
	// surprise the user (volume changes, quitting apps, running Shortcuts).
	DecisionAsk Decision = "ask"

	// DecisionDeny refuses the call outright; the controller returns an
	// error without running osascript. Never a default; users opt in via
	// the Settings UI.
	DecisionDeny Decision = "deny"
)

// IsValid reports whether d is one of the three known decisions. Used by
// callers (and by Load) to reject malformed user-edited JSON values.
func (d Decision) IsValid() bool {
	switch d {
	case DecisionAllow, DecisionAsk, DecisionDeny:
		return true
	default:
		return false
	}
}

// Policy is the persisted, mutable map of tool name → Decision. The
// zero value is NOT safe to use; construct one via NewDefaultPolicy or Load.
//
// Concurrent reads are safe; concurrent reads and writes are guarded by an
// internal RWMutex so the Wails App can call Check from a request handler
// goroutine while the Settings UI calls Set on another. Save serializes
// under the read lock since it only inspects state.
type Policy struct {
	// Decisions maps the canonical tool name (e.g. "mac_open_app",
	// "spotify_pause") to the current Decision. Exported and JSON-tagged
	// so the file format mirrors the in-memory shape — easy to inspect by
	// hand.
	Decisions map[string]Decision `json:"decisions"`

	// mu guards Decisions on read/write. Not serialized.
	mu sync.RWMutex `json:"-"`
}

// defaultAllowTools enumerates the tools whose default decision is
// DecisionAllow. These are read-only or otherwise low-impact: querying
// state, taking a screenshot, controlling music playback that the user
// just started themselves. The list is pinned by tests so accidental
// additions/removals are caught loudly.
//
// Keep this list in sync with the daemon's TOOL_DEFINITIONS (TASK-004).
var defaultAllowTools = []string{
	// macctl read-only.
	"mac_spotlight",
	"mac_clipboard_get",
	"mac_screenshot",
	"mac_list_shortcuts",

	// spotify — playback control of the user's own session is considered
	// safe-by-default; the user already initiated music playback, so
	// pause/skip/volume are no more surprising than tapping the menu bar.
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

// defaultAskTools enumerates the tools whose default decision is
// DecisionAsk. These have observable side effects (launching apps,
// changing system volume/brightness, writing to the clipboard, running
// user-defined Shortcuts) and so default to a confirmation prompt.
//
// Pinned by tests.
var defaultAskTools = []string{
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

// NewDefaultPolicy returns a Policy with safe defaults wired for all known
// macctl + spotify tools. Read-only / safe tools default to allow;
// destructive / surprising tools default to ask. No tools default to deny —
// the user opts in to deny via the Settings UI.
//
// Callers should treat the returned *Policy as the canonical first-run
// policy: Load returns this when ~/.jarvis/policy.json is missing.
func NewDefaultPolicy() *Policy {
	p := &Policy{
		// Pre-size the map to avoid rehash during the initial fill. Both
		// slices are package-level constants in spirit, so len() is cheap.
		Decisions: make(map[string]Decision, len(defaultAllowTools)+len(defaultAskTools)),
	}
	for _, tool := range defaultAllowTools {
		p.Decisions[tool] = DecisionAllow
	}
	for _, tool := range defaultAskTools {
		p.Decisions[tool] = DecisionAsk
	}
	return p
}

// Check returns the current decision for the named tool. Unknown tools
// (those not present in the map) fall back to DecisionAsk — the safest
// default, since a tool the user hasn't seen yet is by definition one we
// don't have explicit consent for.
//
// Check holds a read lock and is safe to call from many goroutines.
func (p *Policy) Check(tool string) Decision {
	if p == nil {
		// Defensive: a nil Policy should never reach this code path (the
		// Controller constructor refuses nil), but if it does, fall back to
		// the safest decision rather than panicking inside a tool dispatch.
		return DecisionAsk
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if d, ok := p.Decisions[tool]; ok && d.IsValid() {
		return d
	}
	return DecisionAsk
}

// Set updates the in-memory decision for the named tool. Does NOT touch
// disk — the caller is responsible for invoking Save afterwards. This
// split lets the Settings UI batch many edits and persist them once at
// the end of the form interaction.
//
// Set holds a write lock and is safe to call from multiple goroutines.
// Invalid Decision values are silently ignored (no panic) so user-supplied
// strings from the API layer can't crash the process.
func (p *Policy) Set(tool string, d Decision) {
	if p == nil || tool == "" || !d.IsValid() {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.Decisions == nil {
		p.Decisions = make(map[string]Decision)
	}
	p.Decisions[tool] = d
}

// Save writes the policy to path atomically (tmp + rename) so a crashed
// mid-write never leaves a half-written file. The parent directory is
// created if missing (0o755). Permissions on the resulting file are 0o644.
//
// On any failure the .tmp file is best-effort removed so we don't litter
// the ~/.jarvis directory with stale temporaries after a failed write.
func (p *Policy) Save(path string) error {
	if p == nil {
		return errors.New("Save: policy is nil")
	}
	if path == "" {
		return errors.New("Save: path is required")
	}

	// Snapshot the map under the read lock, then marshal outside the lock
	// so we don't hold it across the os.WriteFile syscall. We use a small
	// wrapper struct so the on-disk shape is unambiguous regardless of
	// future additions to Policy that we'd rather not serialize.
	p.mu.RLock()
	snapshot := struct {
		Decisions map[string]Decision `json:"decisions"`
	}{
		Decisions: make(map[string]Decision, len(p.Decisions)),
	}
	for k, v := range p.Decisions {
		snapshot.Decisions[k] = v
	}
	p.mu.RUnlock()

	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("Save: marshal: %w", err)
	}
	// Trailing newline — POSIX files customarily end in \n and editors
	// don't add a noisy "no newline at end of file" indicator when humans
	// inspect ~/.jarvis/policy.json.
	data = append(data, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("Save: mkdir %s: %w", dir, err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("Save: write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("Save: rename %s -> %s: %w", tmp, path, err)
	}
	return nil
}

// Load reads and parses the policy at path. Returns NewDefaultPolicy() with
// a nil error when the file does not exist (first-run case): the caller can
// treat first-launch and subsequent launches uniformly.
//
// Malformed JSON or unreadable files return a non-nil error so callers can
// distinguish "first run" (proceed with defaults) from "user broke the
// file" (surface a banner). Unknown decision strings inside an otherwise
// valid file are silently coerced to DecisionAsk — a defensive choice that
// keeps a typo in the user's hand-edited JSON from disabling all permission
// gates.
func Load(path string) (*Policy, error) {
	if path == "" {
		return nil, errors.New("Load: path is required")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// First run: no file on disk yet. Return defaults so the caller
			// gets a usable policy immediately. The defaults will be persisted
			// when the user first interacts with the Settings UI (or by the
			// controller proactively, depending on TASK-002's choice).
			return NewDefaultPolicy(), nil
		}
		return nil, fmt.Errorf("Load: read %s: %w", path, err)
	}

	var raw struct {
		Decisions map[string]Decision `json:"decisions"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("Load: parse %s: %w", path, err)
	}

	p := &Policy{Decisions: make(map[string]Decision, len(raw.Decisions))}
	for tool, decision := range raw.Decisions {
		if tool == "" {
			// Drop empty tool names rather than poisoning the map. A user
			// hand-editing the file shouldn't be able to create an entry
			// nothing can ever look up.
			continue
		}
		if !decision.IsValid() {
			// Coerce typos to ask rather than failing the entire load —
			// we'd rather over-prompt than silently allow.
			p.Decisions[tool] = DecisionAsk
			continue
		}
		p.Decisions[tool] = decision
	}
	return p, nil
}

// PolicyPath returns the canonical on-disk location of the policy file:
// ~/.jarvis/policy.json. Routed through internal/paths so HOME redirection
// (e.g., t.Setenv("HOME", tmp) in tests) works automatically.
func PolicyPath() string {
	return filepath.Join(paths.JarvisHome(), "policy.json")
}
