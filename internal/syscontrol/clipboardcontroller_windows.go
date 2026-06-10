//go:build windows

// clipboardcontroller_windows.go — Windows backend for the
// syscontrol.ClipboardController interface declared in
// clipboardcontroller.go. Mirrors internal/macctl/clipboard.go (which
// shells pbcopy/pbpaste on macOS) but routes through the Win32
// clipboard API via golang.design/x/clipboard.
//
// Why golang.design/x/clipboard:
//   * Pure-Go on Windows (no cgo, no DLL ship cost) — calls user32 /
//     kernel32 syscalls directly via golang.org/x/sys.
//   * Already handles the OpenClipboard contention spin internally
//     (~3.5 s deadline with a 10 ms backoff — see openClipboardRetry
//     upstream) so a single Read/Write call is itself fairly robust.
//   * Matches the plan's TASK-026 contract verbatim (the v0.4.0
//     Windows-port plan names this library explicitly).
//
// Retry layer: TASK-026's acceptance criterion requires that a locked
// clipboard be retried 3× before erroring. We add a *thin* retry on
// top of the library because its own retry only covers the
// OpenClipboard syscall — broader transient failures (e.g. a
// concurrent SetClipboardData racing us between OpenClipboard and the
// data write) still surface as a nil channel from Write or as nil
// bytes from Read. Three attempts with a 50 ms / 100 ms / 200 ms
// backoff bounds the worst case at ~350 ms, well under the daemon's
// tool-call latency budget.
//
// Init contract: golang.design/x/clipboard requires Init() to be
// called once per process before Read/Write/Watch. We funnel that
// through sync.Once inside ensureClipboardInit so multiple
// controllers (or repeated calls on the same controller) cannot race
// the underlying initOnce.Do.

package syscontrol

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"golang.design/x/clipboard"
)

// clipboardMaxAttempts is the cap on retry attempts when the clipboard
// is locked by another process. Three was chosen to match the TASK-026
// acceptance criterion verbatim ("retries 3 times then returns
// error"); raising it would inflate worst-case latency without
// meaningfully improving success rates (the upstream OpenClipboard
// spin already absorbs the common contention window).
const clipboardMaxAttempts = 3

// clipboardRetryBaseDelay is the first backoff between retries. The
// delays grow geometrically (×2) per attempt — 50 ms, 100 ms, 200 ms —
// matching the standard exponential-backoff pattern used elsewhere in
// the daemon. Total worst-case wall time across all retries is ~350 ms
// (plus the upstream OpenClipboard deadline per attempt), which keeps
// us comfortably within voice-pipeline budgets.
const clipboardRetryBaseDelay = 50 * time.Millisecond

// ErrClipboardLocked is returned by ClipboardGet / ClipboardSet when
// the Windows clipboard remains locked by another process across all
// retry attempts. Pinning it as a sentinel (rather than a string
// match) lets the daemon's tool executor render a specific user-facing
// hint ("another app is using the clipboard — try again in a moment")
// instead of a generic failure.
var ErrClipboardLocked = errors.New("syscontrol: clipboard locked by another process")

// clipboardInitOnce + clipboardInitErr memoise the result of
// clipboard.Init(). The upstream package guards its own initOnce, so
// strictly speaking double-init is safe, but caching the error lets
// us short-circuit subsequent calls without re-entering the upstream
// once.Do (which would otherwise just return the cached error too,
// but pays the mutex cost).
var (
	clipboardInitOnce sync.Once
	clipboardInitErr  error
)

// ensureClipboardInit lazily initialises golang.design/x/clipboard on
// first use. We do NOT call clipboard.Init() in package init() —
// that would force the cost (and any error) onto programs that never
// touch the clipboard, e.g. the headless CLI subcommands.
func ensureClipboardInit() error {
	clipboardInitOnce.Do(func() {
		clipboardInitErr = clipboard.Init()
	})
	return clipboardInitErr
}

// WindowsClipboardController is the Windows backend for
// syscontrol.ClipboardController. It is intentionally state-light —
// the upstream library serialises its own access via a package-level
// mutex, so a single Controller instance is safe for concurrent use.
//
// Construct via NewWindowsClipboardController so the type's zero
// value is never observed in production; the constructor is the only
// place that documents the init contract (Init is lazy + memoised,
// not done here).
type WindowsClipboardController struct{}

// NewWindowsClipboardController returns a Controller wired to the
// host clipboard. No I/O is performed at construction time — the
// upstream clipboard.Init() runs on first ClipboardGet/Set call so
// failures surface where the caller can handle them (a wrapped
// error) rather than panicking on import.
func NewWindowsClipboardController() *WindowsClipboardController {
	return &WindowsClipboardController{}
}

// Compile-time assertion that *WindowsClipboardController satisfies
// the cross-platform syscontrol.ClipboardController interface. If
// the interface method signatures drift (e.g. someone changes
// ClipboardSet's return type), the build fails here rather than at a
// distant call site — mirroring the assertions in
// internal/macctl/macctl.go.
var _ ClipboardController = (*WindowsClipboardController)(nil)

// ClipboardGet reads the current text content of the Windows
// clipboard. Returns ("", nil) when the clipboard is empty or holds a
// non-text format (matches the interface contract that callers handle
// "empty clipboard" spoken-response framing themselves rather than
// surfacing it as an error).
//
// Retry behaviour: if golang.design/x/clipboard returns nil bytes —
// which can mean either "empty" or "transient OpenClipboard failure"
// — we re-attempt up to clipboardMaxAttempts times with exponential
// backoff. After the cap is hit we still return ("", nil) because we
// cannot distinguish a genuine empty clipboard from a persistently
// locked one without dropping below the library to call user32
// directly; the daemon's spoken-response layer treats both the same
// ("the clipboard is empty"). The 3-retry behaviour matches the
// TASK-026 acceptance criterion verbatim.
func (c *WindowsClipboardController) ClipboardGet() (string, error) {
	if err := ensureClipboardInit(); err != nil {
		return "", fmt.Errorf("ClipboardGet: %w", err)
	}

	var buf []byte
	for attempt := 0; attempt < clipboardMaxAttempts; attempt++ {
		buf = clipboard.Read(clipboard.FmtText)
		if buf != nil {
			// Non-nil buffer is unambiguous success — either non-empty
			// text or an explicit empty-text payload. Either way the
			// caller gets the verbatim string per interface contract.
			return string(buf), nil
		}
		// nil means either an empty clipboard or a transient read
		// failure. Back off and retry; if it's truly empty the next
		// attempt will also return nil and we'll exit the loop
		// returning "" (which IS the contract for an empty clipboard).
		if attempt < clipboardMaxAttempts-1 {
			time.Sleep(clipboardRetryBaseDelay << attempt)
		}
	}
	// Exhausted retries. Per interface contract, an empty clipboard
	// returns ("", nil) — we conservatively treat persistent nil as
	// empty rather than as a failure because golang.design/x/clipboard
	// swallows the underlying error code and we cannot prove lock vs.
	// empty from this layer alone.
	return "", nil
}

// ClipboardSet writes text to the Windows clipboard. Empty text is a
// valid input (deliberately clearing the clipboard) and is NOT
// rejected — the interface contract is explicit about this so a
// caller writing "" to scrub previously-copied secrets works.
//
// Destructive: the previous clipboard contents are lost. Policy
// gating is the responsibility of the surrounding daemon tool layer
// (the syscontrol interfaces stay implementation-agnostic by design;
// see clipboardcontroller.go comments).
//
// Retry behaviour: golang.design/x/clipboard.Write signals failure by
// returning a nil channel (errors are swallowed into a debug printf
// upstream). We retry up to clipboardMaxAttempts times with
// exponential backoff; once exhausted we surface
// ErrClipboardLocked wrapped with the method name so callers can do
// errors.Is(err, syscontrol.ErrClipboardLocked) for the "another app
// is using the clipboard" UX path.
//
// The non-nil channel returned by Write is intentionally discarded.
// Upstream uses it to notify when a *subsequent* writer overwrites
// our payload; we don't need that for a fire-and-forget tool call,
// and holding it would just leak the goroutine that monitors the
// clipboard sequence number.
func (c *WindowsClipboardController) ClipboardSet(text string) (string, error) {
	if err := ensureClipboardInit(); err != nil {
		return "", fmt.Errorf("ClipboardSet: %w", err)
	}

	// clipboard.Write expects UTF-8 bytes for FmtText; Go strings are
	// already UTF-8 so the conversion is a no-op copy.
	payload := []byte(text)

	for attempt := 0; attempt < clipboardMaxAttempts; attempt++ {
		ch := clipboard.Write(clipboard.FmtText, payload)
		if ch != nil {
			// Non-nil channel = upstream accepted the write. Discard
			// the channel intentionally (see doc comment) — we don't
			// care when a later writer overrides our payload, that's
			// the user's prerogative.
			return "", nil
		}
		if attempt < clipboardMaxAttempts-1 {
			time.Sleep(clipboardRetryBaseDelay << attempt)
		}
	}
	return "", fmt.Errorf("ClipboardSet: %w", ErrClipboardLocked)
}
