// Package hotkey provides a Mac-friendly wrapper over golang.design/x/hotkey
// that maps hotkey-string specs (e.g. "alt+space") to OS event-tap callbacks.
//
// Lifecycle: caller creates a Manager, calls Register with a parsed spec and
// press/release callbacks. The Manager owns a goroutine that watches the
// library's key channels and dispatches callbacks until Unregister or Close.
//
// Manager invariants (TASK-005):
//
//   - Register on an already-registered Manager first calls Unregister to
//     produce a clean slate. The library's *Hotkey is not reused — a stale
//     ptr would race the channel close from the prior teardown.
//   - The watch goroutine is owned by the Manager and exits exactly once,
//     when Unregister (or Close) is called. The `done` channel is the
//     cancellation primitive; closing it is the unambiguous "stop now" signal.
//   - The active *Hotkey is captured into a local at goroutine start so a
//     concurrent Unregister flipping m.active to nil does not race the
//     channel reads. The lib's Unregister closes the channels, which
//     manifests as the select case firing with the zero Event value; we
//     ignore that and exit via the done channel instead.
//   - Unregister is idempotent: calling it on a fresh Manager or twice in
//     a row is a no-op returning nil.
//   - macOS main-thread requirement: `hk.Register()` (the lib's underlying
//     CGO call) MUST be called from the main thread. Wails owns the main
//     thread via runtime.LockOSThread; the clean integration is to call
//     Manager.Register from inside Wails's OnStartup hook (which runs on
//     the main goroutine). Receiving on the lib's Keydown/Keyup channels
//     is safe off-thread, which is what our watch goroutine does.
//   - The lib has no Close on the package itself — only per-Hotkey
//     Unregister. Manager.Close delegates to Unregister.
//
// Channel-pattern note: the lib's Keydown() and Keyup() return independent
// <-chan Event channels. We deliberately watch both inside a single select
// so a double-tap (down, down, up, up) is not collapsed by the naive
// sequential `<-Keydown; <-Keyup` pattern from the lib's minimal example.
// On a double-tap, two press events arrive on Keydown before either Keyup
// fires; the lib's channel-of-quasi-infinite-capacity adapter queues them
// for us, so the select drains both before the eventual Keyup pair.
package hotkey

import (
	"errors"
	"fmt"
	"sync"

	libhotkey "golang.design/x/hotkey"
)

// ErrNotImplemented is retained as a public sentinel for compatibility with
// downstream callers that may have asserted on it during the TASK-002
// integration window. Production code paths in this package no longer return
// it — Register/Unregister are fully implemented in TASK-005.
var ErrNotImplemented = errors.New("hotkey: not implemented (TASK-005)")

// libHotkey is the narrow surface of golang.design/x/hotkey.Hotkey that
// Manager actually depends on. Abstracting it lets the tests inject a fake
// without grabbing a system-level hotkey. Production swaps in the real lib
// type via newHotkeyFn below.
type libHotkey interface {
	Register() error
	Unregister() error
	Keydown() <-chan libhotkey.Event
	Keyup() <-chan libhotkey.Event
}

// newHotkeyFn constructs a libHotkey from the parsed modifiers + key. The
// indirection is a test seam: tests replace this with a factory that returns
// a fake implementation; production keeps the default which delegates to
// libhotkey.New (whose *Hotkey already satisfies libHotkey).
var newHotkeyFn = func(mods []libhotkey.Modifier, key libhotkey.Key) libHotkey {
	return libhotkey.New(mods, key)
}

// Manager is the per-app hotkey lifecycle owner. Construct with NewManager.
type Manager struct {
	mu        sync.Mutex
	active    libHotkey     // nil when nothing is registered
	done      chan struct{} // closed by Unregister to stop the watch goroutine
	onPress   func()
	onRelease func()
}

// NewManager returns a fresh Manager with no active binding.
func NewManager() *Manager { return &Manager{} }

// Register parses spec, allocates a library Hotkey, and arms onPress/onRelease.
//
// Order matters:
//  1. Unregister any prior binding (idempotent if none).
//  2. Parse the spec — bail before touching the OS if it's malformed.
//  3. Allocate a fresh libHotkey (never reuse: see file-level invariants).
//  4. Call Register() on it — this is the OS-denial path (Accessibility
//     permission missing, or another app owns the combo). On error we
//     return the wrapped error and leave the Manager in a clean
//     unregistered state.
//  5. Publish the new state under the lock and start the watch goroutine.
//
// onPress / onRelease may be nil; the watch goroutine no-ops on nil
// callbacks rather than panicking. This is the lenient path that lets the
// wiring layer pass closures that swallow + log binding errors.
func (m *Manager) Register(spec string, onPress, onRelease func()) error {
	// 1. Clean slate. Ignore the error: the only failure path is the OS
	//    rejecting Unregister on a stale ref, which we can't recover from
	//    here and which our state has already moved past.
	_ = m.Unregister()

	// 2. Parse before allocating anything. Validates the spec independently
	//    of OS-level failures, so the caller can distinguish "bad spec" from
	//    "Accessibility denied".
	mods, key, err := Parse(spec)
	if err != nil {
		return fmt.Errorf("hotkey.Manager.Register: parse spec %q: %w", spec, err)
	}

	// 3. Fresh libHotkey — never reuse a previous *Hotkey (see file-level
	//    invariants for why).
	hk := newHotkeyFn(mods, key)

	// 4. Hit the OS. This is the documented failure-case path:
	//    Accessibility-denied or shortcut-already-owned both surface here.
	if err := hk.Register(); err != nil {
		return fmt.Errorf("hotkey.Manager.Register: OS register %q: %w", spec, err)
	}

	// 5. Publish state under the lock, then start the watcher. The
	//    watcher captures the libHotkey + done channel locally so a
	//    subsequent Unregister (which nils m.active) does not race.
	m.mu.Lock()
	m.active = hk
	m.done = make(chan struct{})
	m.onPress = onPress
	m.onRelease = onRelease
	doneCh := m.done
	pressFn := m.onPress
	releaseFn := m.onRelease
	m.mu.Unlock()

	go m.watchLoop(hk, doneCh, pressFn, releaseFn)
	return nil
}

// watchLoop fans the lib's two independent event channels into onPress /
// onRelease invocations until done is closed.
//
// All four arguments are captured locally to insulate the loop from
// concurrent Manager state changes — once Unregister flips m.active to nil
// and closes m.done, this goroutine continues to reference its own local
// `hk` and `done` until it exits. The lib's Unregister will close the
// channels which would otherwise manifest as a tight loop on zero-Events
// from the closed channel; the done case wins thanks to select fairness
// once it's been closed.
func (m *Manager) watchLoop(hk libHotkey, done <-chan struct{}, onPress, onRelease func()) {
	keydown := hk.Keydown()
	keyup := hk.Keyup()

	for {
		select {
		case _, ok := <-keydown:
			if !ok {
				// The lib closed the channel — Unregister was called.
				// Wait for done to confirm and exit cleanly.
				<-done
				return
			}
			if onPress != nil {
				onPress()
			}
		case _, ok := <-keyup:
			if !ok {
				<-done
				return
			}
			if onRelease != nil {
				onRelease()
			}
		case <-done:
			return
		}
	}
}

// Unregister tears down the active binding if any. Safe to call when nothing
// is registered (no-op returning nil).
//
// Sequence:
//  1. Under the lock, snapshot the active hotkey + done channel, then clear
//     them. We clear before releasing the lock so a concurrent Register
//     observes an empty slot and proceeds.
//  2. Close `done` to signal the watch goroutine to exit.
//  3. Call hk.Unregister() outside the lock — the lib may block briefly on
//     the OS side and we don't want to hold the manager mutex during that.
func (m *Manager) Unregister() error {
	m.mu.Lock()
	if m.active == nil {
		m.mu.Unlock()
		return nil // idempotent no-op
	}
	hk := m.active
	done := m.done
	m.active = nil
	m.done = nil
	m.onPress = nil
	m.onRelease = nil
	m.mu.Unlock()

	// Signal the watch goroutine to exit. Closing is safe because only
	// Unregister (and indirectly Close) closes this channel, and we just
	// snapshotted-and-cleared it under the lock, so a concurrent Unregister
	// would observe nil and bail at step 1.
	if done != nil {
		close(done)
	}

	if err := hk.Unregister(); err != nil {
		return fmt.Errorf("hotkey.Manager.Unregister: %w", err)
	}
	return nil
}

// Close releases all resources. Idempotent.
func (m *Manager) Close() error {
	return m.Unregister()
}
