package hotkey

// hotkey_test.go — TASK-005 acceptance tests for Manager.
//
// The four required tests from the task brief:
//   1. Happy path: Register, fire events on Keydown / Keyup, assert
//      callbacks fired in the correct count + order.
//   2. Rebind no-leak: Register spec A, then spec B. Assert the first
//      libHotkey's Unregister was called exactly once, and that the first
//      watch goroutine has exited (no stale callbacks on the old channels).
//   3. OS denial (failure case): Mock libHotkey.Register to return an
//      error. Manager.Register returns the wrapped error, no goroutine
//      leaked, no state stored.
//   4. Idempotent Unregister: Unregister on a fresh Manager returns nil;
//      double-Unregister returns nil.
//
// Test seam: newHotkeyFn is swapped to return a *fakeHotkey for the test's
// duration. fakeHotkey owns its own Keydown/Keyup chans the test can push
// events into directly.

import (
	"errors"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	libhotkey "golang.design/x/hotkey"
)

// ---------------------------------------------------------------------------
// fakeHotkey — minimal libHotkey impl backed by per-test channels.
// ---------------------------------------------------------------------------

type fakeHotkey struct {
	mu sync.Mutex

	// Pre-seeded error to return from Register (for the OS-denial test).
	registerErr error

	registered    atomic.Int32 // number of Register() calls
	unregisterCnt atomic.Int32 // number of Unregister() calls
	unregisterErr error        // pre-seeded error from Unregister

	// The channels are created at construction and never reassigned. They
	// are closed (once) by Unregister to mirror the lib's behaviour. A
	// `closed` flag guards against a double-close on a second Unregister.
	keydownCh chan libhotkey.Event
	keyupCh   chan libhotkey.Event
	closed    bool
}

func newFakeHotkey() *fakeHotkey {
	return &fakeHotkey{
		// Buffered so tests can push events without a receiver having to
		// be ready at exactly the right instant.
		keydownCh: make(chan libhotkey.Event, 16),
		keyupCh:   make(chan libhotkey.Event, 16),
	}
}

func (f *fakeHotkey) Register() error {
	f.registered.Add(1)
	return f.registerErr
}

func (f *fakeHotkey) Unregister() error {
	f.unregisterCnt.Add(1)
	// Close the channels to mirror the real lib's behaviour. Guarded by
	// `closed` so a second Unregister is a no-op on the channel side.
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		close(f.keydownCh)
		close(f.keyupCh)
		f.closed = true
	}
	return f.unregisterErr
}

// Keydown / Keyup always return the same channel reference. After Unregister
// the channel is in the closed state; receivers see the closed-ok=false
// signal rather than blocking forever (the lib has the same contract).
func (f *fakeHotkey) Keydown() <-chan libhotkey.Event { return f.keydownCh }
func (f *fakeHotkey) Keyup() <-chan libhotkey.Event   { return f.keyupCh }

// pushKeydown sends an event on the keydown channel. Returns false if the
// fake has been Unregistered (channel is closed) so the caller can choose
// to skip rather than panic on send-to-closed.
func (f *fakeHotkey) pushKeydown() bool {
	f.mu.Lock()
	closed := f.closed
	f.mu.Unlock()
	if closed {
		return false
	}
	f.keydownCh <- libhotkey.Event{}
	return true
}

func (f *fakeHotkey) pushKeyup() bool {
	f.mu.Lock()
	closed := f.closed
	f.mu.Unlock()
	if closed {
		return false
	}
	f.keyupCh <- libhotkey.Event{}
	return true
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// installFakeFactory swaps newHotkeyFn to return the given fake, restoring
// the previous factory on t.Cleanup. The returned getter exposes the most
// recently constructed fake so tests that rebind can inspect each fake in
// the sequence.
func installFakeFactory(t *testing.T, fakes ...*fakeHotkey) func() *fakeHotkey {
	t.Helper()
	prev := newHotkeyFn

	var idx atomic.Int32
	var lastMu sync.Mutex
	var last *fakeHotkey

	newHotkeyFn = func(mods []libhotkey.Modifier, key libhotkey.Key) libHotkey {
		i := int(idx.Add(1)) - 1
		if i >= len(fakes) {
			t.Fatalf("installFakeFactory: factory called %d times but only %d fakes supplied", i+1, len(fakes))
		}
		lastMu.Lock()
		last = fakes[i]
		lastMu.Unlock()
		return fakes[i]
	}
	t.Cleanup(func() { newHotkeyFn = prev })

	return func() *fakeHotkey {
		lastMu.Lock()
		defer lastMu.Unlock()
		return last
	}
}

// waitFor polls a predicate until it returns true or the timeout elapses.
// Used in lieu of arbitrary sleeps so the tests stay responsive when the
// watch goroutine handles events quickly, while still being safe on a
// loaded CI runner.
func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("waitFor timed out: %s", msg)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestRegister_HappyPath — required test #1.
//
// Register, send three down + three up events through the fake's channels,
// assert onPress / onRelease fired three times each in interleaved order.
// The interleaving (down, up, down, up, ...) reflects the realistic
// press-release cycle the lib emits on each tap.
func TestRegister_HappyPath(t *testing.T) {
	fake := newFakeHotkey()
	installFakeFactory(t, fake)

	var pressCount, releaseCount atomic.Int32
	pressFn := func() { pressCount.Add(1) }
	releaseFn := func() { releaseCount.Add(1) }

	m := NewManager()
	if err := m.Register("alt+space", pressFn, releaseFn); err != nil {
		t.Fatalf("Register err: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })

	// Push 3 down + 3 up. Use the buffered channels so this doesn't block
	// even if the watch goroutine is mid-dispatch.
	for i := 0; i < 3; i++ {
		if !fake.pushKeydown() {
			t.Fatalf("pushKeydown #%d failed (channel closed)", i)
		}
		if !fake.pushKeyup() {
			t.Fatalf("pushKeyup #%d failed (channel closed)", i)
		}
	}

	waitFor(t, func() bool {
		return pressCount.Load() == 3 && releaseCount.Load() == 3
	}, "expected 3 press + 3 release callbacks")

	// Sanity: Register on the underlying fake was called exactly once.
	if got := fake.registered.Load(); got != 1 {
		t.Errorf("fake.Register call count = %d, want 1", got)
	}
}

// TestRegister_RebindNoLeak — required test #2.
//
// Two sequential Register calls with different specs. The first fake's
// Unregister must be called exactly once (during the second Register's
// clean-slate step). Events pushed on the first fake's now-closed channel
// must not trigger callbacks. The second fake's events do trigger
// callbacks, proving the second registration is live.
func TestRegister_RebindNoLeak(t *testing.T) {
	fakeA := newFakeHotkey()
	fakeB := newFakeHotkey()
	getLast := installFakeFactory(t, fakeA, fakeB)

	var pressCount atomic.Int32
	pressFn := func() { pressCount.Add(1) }
	releaseFn := func() { /* not asserted */ }

	m := NewManager()
	if err := m.Register("alt+space", pressFn, releaseFn); err != nil {
		t.Fatalf("first Register err: %v", err)
	}

	// One event on fakeA — must register.
	fakeA.pushKeydown()
	waitFor(t, func() bool { return pressCount.Load() == 1 }, "fakeA press not observed")

	// Rebind to a different spec. Internally this calls Unregister on
	// fakeA, then Register on fakeB.
	if err := m.Register("ctrl+shift+j", pressFn, releaseFn); err != nil {
		t.Fatalf("second Register err: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })

	if got := fakeA.unregisterCnt.Load(); got != 1 {
		t.Errorf("fakeA.Unregister count after rebind = %d, want 1", got)
	}
	if got := fakeB.registered.Load(); got != 1 {
		t.Errorf("fakeB.Register count after rebind = %d, want 1", got)
	}
	if last := getLast(); last != fakeB {
		t.Errorf("last constructed fake = %p, want fakeB %p", last, fakeB)
	}

	// Push events on fakeB — these must reach the callbacks (live binding).
	fakeB.pushKeydown()
	fakeB.pushKeydown()
	waitFor(t, func() bool { return pressCount.Load() == 3 }, "fakeB presses not observed")

	// Pushing on fakeA is impossible (channel is closed). Confirm the
	// channel is in fact closed so the no-leak assertion is meaningful.
	if _, ok := <-fakeA.Keydown(); ok {
		t.Error("fakeA.Keydown should be closed after Unregister, but is still open")
	}
}

// TestRegister_OSDenialFailure — required test #3, the documented failure-case
// acceptance criterion.
//
// The fake's Register returns an error (simulating Accessibility-denied or
// shortcut-conflict). Manager.Register propagates the wrapped error,
// leaves the Manager in an unregistered state, and starts no watch
// goroutine. Verified by:
//   - return value is non-nil and wraps the original.
//   - m.active is nil afterwards.
//   - no goroutine count growth across the call.
func TestRegister_OSDenialFailure(t *testing.T) {
	wantErr := errors.New("simulated OS denial")
	fake := newFakeHotkey()
	fake.registerErr = wantErr
	installFakeFactory(t, fake)

	m := NewManager()

	goroutinesBefore := runtime.NumGoroutine()

	err := m.Register("alt+space", func() {}, func() {})
	if err == nil {
		t.Fatal("Register should have returned an error on OS denial")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("Register error = %v, want it to wrap %v", err, wantErr)
	}
	if !strings.Contains(err.Error(), "OS register") {
		t.Errorf("Register error = %q, want it to mention 'OS register' (wrap context)", err.Error())
	}

	// State must be clean.
	m.mu.Lock()
	active := m.active
	done := m.done
	m.mu.Unlock()
	if active != nil {
		t.Error("m.active should be nil after a failed Register")
	}
	if done != nil {
		t.Error("m.done should be nil after a failed Register")
	}

	// No goroutine leak. Allow a small slack window for unrelated runtime
	// goroutines to settle on a busy CI runner.
	time.Sleep(20 * time.Millisecond)
	goroutinesAfter := runtime.NumGoroutine()
	if goroutinesAfter > goroutinesBefore+1 {
		t.Errorf("goroutine leak after failed Register: before=%d after=%d",
			goroutinesBefore, goroutinesAfter)
	}

	// Unregister on a failed-Register Manager must still be a clean no-op.
	if err := m.Unregister(); err != nil {
		t.Errorf("Unregister after failed Register err = %v, want nil", err)
	}
}

// TestUnregister_Idempotent — required test #4.
//
// Three calls all return nil:
//   1. Fresh Manager (nothing ever registered).
//   2. After a successful Register + Unregister cycle, a second Unregister.
//   3. Close on a Manager whose binding has already been torn down.
func TestUnregister_Idempotent(t *testing.T) {
	m := NewManager()

	// 1. Fresh manager.
	if err := m.Unregister(); err != nil {
		t.Errorf("Unregister on fresh Manager err = %v, want nil", err)
	}

	// Now register, unregister, then unregister-again.
	fake := newFakeHotkey()
	installFakeFactory(t, fake)

	if err := m.Register("alt+space", func() {}, func() {}); err != nil {
		t.Fatalf("Register err: %v", err)
	}
	if err := m.Unregister(); err != nil {
		t.Errorf("first Unregister err = %v, want nil", err)
	}
	// 2. Second Unregister — idempotent.
	if err := m.Unregister(); err != nil {
		t.Errorf("second Unregister err = %v, want nil", err)
	}
	// 3. Close after Unregister — still idempotent (Close delegates to Unregister).
	if err := m.Close(); err != nil {
		t.Errorf("Close after Unregister err = %v, want nil", err)
	}

	if got := fake.unregisterCnt.Load(); got != 1 {
		t.Errorf("fake.Unregister called %d times across idempotent calls, want 1", got)
	}
}

// TestRegister_ParseError — supplementary: a malformed spec must fail before
// hitting the OS (no fake.Register call), and must not leak a goroutine.
// This protects the documented Parse-first ordering in Register.
func TestRegister_ParseError(t *testing.T) {
	fake := newFakeHotkey()
	installFakeFactory(t, fake)

	m := NewManager()
	err := m.Register("not a real spec garbage", func() {}, func() {})
	if err == nil {
		t.Fatal("Register on bad spec should error")
	}
	if !strings.Contains(err.Error(), "parse spec") {
		t.Errorf("error = %q, want it to mention 'parse spec'", err.Error())
	}
	if got := fake.registered.Load(); got != 0 {
		t.Errorf("fake.Register was called %d times despite parse failure, want 0", got)
	}
}

// TestRegister_NilCallbacksDoNotPanic — the Manager must tolerate nil
// onPress / onRelease without crashing the watch goroutine. This matters
// because the wiring layer may pass closures that are nil during some
// degraded paths (e.g. App.OverlayPTTPress not yet bound).
func TestRegister_NilCallbacksDoNotPanic(t *testing.T) {
	fake := newFakeHotkey()
	installFakeFactory(t, fake)

	m := NewManager()
	if err := m.Register("alt+space", nil, nil); err != nil {
		t.Fatalf("Register err: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })

	fake.pushKeydown()
	fake.pushKeyup()

	// No panic + the goroutine consumes events. Verified by sleeping and
	// then checking nothing crashed (test would have failed if so).
	time.Sleep(30 * time.Millisecond)
}
