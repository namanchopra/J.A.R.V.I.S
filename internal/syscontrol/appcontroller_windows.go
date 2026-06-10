//go:build windows

// appcontroller_windows.go — Windows implementation of the syscontrol
// AppController contract declared in appcontroller.go. The macOS reference
// backend lives in internal/macctl/apps.go + internal/macctl/windows.go;
// this file is the Windows counterpart for the full AppController surface:
// OpenApp + QuitApp shell PowerShell (TASK-020), FocusWindow shells the
// Win32 windowing APIs directly via golang.org/x/sys/windows (TASK-021).
//
// Shell choice: every entry point shells PowerShell because the cmd.exe
// equivalents either don't exist (no native "start app by display name")
// or are riddled with quoting traps (`taskkill /IM` matches process image
// names but not display names, so `taskkill /IM notepad` works while
// `taskkill /IM Notepad` doesn't on case-sensitive locales). PowerShell's
// Start-Process / Get-Process / Stop-Process trio handles both display
// names and process names uniformly, so the AppController contract holds:
// "OpenApp("notepad") launches Notepad" works the same way whether the
// caller said "notepad", "Notepad", or "notepad.exe".
//
// Quoting strategy: every user-supplied name is interpolated into a
// PowerShell single-quoted string ('...'). Single quotes in PowerShell
// are literal — no expansion, no escapes other than '' for an embedded
// single quote. That makes injection-safety trivial: we replace each `'`
// in the name with `''` and wrap the result in `'...'`. We intentionally
// do NOT use double quotes — those interpolate $variables and `subexpressions`,
// which would be an injection vector on a stray voice misfire like
// `OpenApp("$(rm -rf /)")`. The macOS backend has the same hardening via
// fmt.Sprintf %q against AppleScript, so the two backends are symmetric.
//
// PowerShell flags rationale:
//   * -NoProfile      Skip $PROFILE which can take >1s to load on developer
//                     boxes and could in theory mutate Get-Process behaviour
//                     via a function override.
//   * -NonInteractive Refuses interactive prompts (e.g. "Are you sure?"
//                     when stopping a system process). Without this, a
//                     hung confirm prompt would block the daemon's tool
//                     dispatch forever.
//   * -Command        Inline script body — avoids writing a temp file and
//                     keeps the syscall surface limited to a single exec.
//
// Failure mode coverage (per TASK-020 acceptance criteria):
//   1. OpenApp("notepad") launches Notepad   — Start-Process handles both
//      "notepad" and "notepad.exe"; -ErrorAction Stop converts the
//      "command not found" warning into a non-zero exit we can wrap.
//   2. QuitApp("notepad") closes cleanly     — Stop-Process without -Force
//      sends WM_CLOSE so Notepad's "save changes?" path is honoured; if
//      the caller really wants a kill, they can switch to the policy
//      layer's "force quit" tool (out of scope for TASK-020).
//   3. QuitApp("nonexistent-app") errors     — Get-Process -ErrorAction
//      Stop converts the "no process found" warning into a terminating
//      error, which PowerShell exits non-zero on, which exec.Command
//      surfaces as *exec.ExitError, which we wrap with the name + stderr.
//      Crucially this is a returned error, never a panic — there is no
//      panic() call anywhere in this file, and exec.Command itself does
//      not panic on missing processes.

package syscontrol

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// WindowsAppController is the Windows backend for syscontrol.AppController.
// Construction is allocation-free (no DLL probes at constructor time) so
// callers can NewWindowsAppController() unconditionally even on hosts where
// PowerShell or user32.dll is unavailable — the failure surfaces at the
// first method call as a wrapped syscall/exec error rather than as a
// constructor panic. This matches the macctl.NewController pattern where
// wiring a Controller never touches the host until a tool actually runs.
//
// The struct intentionally holds no state. The user32 procedure handles
// resolved by FocusWindow are kept at package scope (modUser32 + procXxx
// vars below) because lazy DLLs are concurrency-safe and process-global:
// caching them on the struct would just duplicate the same handle per
// controller instance without any thread-safety benefit.
type WindowsAppController struct{}

// Compile-time assertion that *WindowsAppController fully satisfies the
// syscontrol.AppController interface. Drift in any method signature
// (OpenApp / QuitApp / FocusWindow) fails the build here at the canonical
// location rather than at a distant call site, mirroring the assertion
// pattern in internal/macctl/macctl.go.
var _ AppController = (*WindowsAppController)(nil)

// NewWindowsAppController returns a ready-to-use Windows AppController.
// Returning a pointer (rather than a value) keeps the type future-proof
// for TASK-021's syscall.LazyDLL cache without a breaking signature change.
func NewWindowsAppController() *WindowsAppController {
	return &WindowsAppController{}
}

// powershellArgs builds the -NoProfile -NonInteractive -Command argv slice
// for a one-shot PowerShell invocation. Factored out so OpenApp and QuitApp
// don't duplicate the flag list and so TASK-022..TASK-027's other Windows
// backends can share it once they land (they will all need the same hardened
// invocation pattern).
//
// The function returns the full argv (program + args) for exec.Command —
// callers should pass it via exec.Command("powershell", args...). We don't
// return *exec.Cmd directly because tests may want to inject a context or
// override stdin/stdout before running.
func powershellArgs(script string) []string {
	return []string{
		"-NoProfile",
		"-NonInteractive",
		"-Command",
		script,
	}
}

// psSingleQuote wraps `s` in a PowerShell single-quoted literal, escaping
// embedded single quotes by doubling them per PowerShell's literal-string
// rules. This is the injection-safe primitive used by every PowerShell
// invocation in this package — see the file-header "Quoting strategy"
// section for the rationale.
//
// Example:
//
//	psSingleQuote("notepad")        -> 'notepad'
//	psSingleQuote("O'Brien.exe")    -> 'O''Brien.exe'
//	psSingleQuote("a$(b)c")         -> 'a$(b)c'   (subexpression is literal)
//
// The third example is the load-bearing one: a stray voice misfire that
// produced a `$(...)` subexpression in the app name would otherwise execute
// arbitrary PowerShell. Single-quoting neuters it.
func psSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// OpenApp activates (or launches) the named application via PowerShell's
// Start-Process cmdlet. Start-Process resolves `name` against:
//
//  1. The PATH (so "notepad" finds C:\Windows\System32\notepad.exe).
//  2. App Paths registry entries (so "code" finds VS Code even when not
//     on PATH — Start-Process does this resolution natively).
//  3. URL protocol handlers (so "ms-settings:" opens Windows Settings).
//
// -ErrorAction Stop converts Start-Process's default warning-only behaviour
// into a terminating error on resolution failure; without this flag a typo'd
// app name would silently succeed (PowerShell prints a warning and exits 0),
// which is exactly the "silent success" trap the TASK-020 acceptance
// criteria call out.
//
// The macOS counterpart (internal/macctl/apps.go OpenApp) shells `open -a`;
// the behavioural contract here is the same — empty name rejected, non-zero
// exit wrapped with stderr context, "" returned on success so the daemon's
// status-string accumulator stays empty for the trivial happy path.
//
// Policy gate: TASK-020 deliberately does not wire a policy check here —
// the macOS side guards via c.policy.Check("mac_open_app") inside macctl,
// but the syscontrol.AppController interface places no policy requirement
// on its implementations, and the only current call sites route through
// macctl's gate before reaching syscontrol on Windows-equivalent flows.
// If a future task wants per-OS policy on Windows, TASK-019's interface
// is the right place to add it (an explicit Check method on the
// controller), not this file.
func (a *WindowsAppController) OpenApp(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("OpenApp: name is required")
	}

	script := fmt.Sprintf(
		"Start-Process -FilePath %s -ErrorAction Stop",
		psSingleQuote(name),
	)
	out, err := exec.Command("powershell", powershellArgs(script)...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("OpenApp(%q): %s: %w", name, strings.TrimSpace(string(out)), err)
	}
	return "", nil
}

// QuitApp asks the named application to terminate via PowerShell's
// Get-Process | Stop-Process pipeline. The cmdlet pair sends WM_CLOSE
// to each matching process's main window — equivalent to a user clicking
// the X button — so apps with unsaved state (e.g. Notepad with edits)
// surface their normal save-changes prompt rather than losing data.
//
// Match strategy: Get-Process accepts either a process name (no .exe) or
// an image name; we pass `name` verbatim with -Name. The macOS counterpart
// uses `tell application <name> to quit` which is display-name based;
// Windows Get-Process is process-name based (case-insensitive). For the
// canonical "notepad" / "code" / "chrome" cases this round-trips fine.
// Display-name-only resolution (e.g. "Visual Studio Code" -> Code.exe) is
// not handled here; if it becomes a frequent voice-misfire pattern, a
// future TASK can add a friendly-name lookup table or a "Get-Process *
// | Where-Object MainWindowTitle -like ..." fallback.
//
// -ErrorAction Stop is critical for the failure-case acceptance criterion:
// without it, Get-Process emits a non-terminating warning ("Cannot find a
// process with the name 'nonexistent-app'") and exits 0, which would
// silently succeed in exec.Command's eyes. With -ErrorAction Stop the
// warning becomes a terminating error and PowerShell exits non-zero, which
// surfaces as an *exec.ExitError that we wrap with name + stderr context.
//
// Stop-Process is called WITHOUT -Force so unsaved-changes prompts are
// honoured (the macOS counterpart sends a graceful AppleScript "quit"
// for the same reason). A future "force quit" tool — analogous to macOS's
// `kill -9` — would be a separate AppController method gated through a
// more restrictive policy.
//
// Failure case (TASK-020 AC #3): QuitApp("nonexistent-app") returns a
// non-nil error with a clear message and never panics. exec.Command does
// not panic on a non-zero exit; it returns *exec.ExitError, which the
// fmt.Errorf wrap turns into a regular error value. The combined stderr
// from PowerShell ("Cannot find a process with the name '...'") is
// folded into the wrapped message so the caller can show the user a
// useful explanation rather than a bare exit-code.
func (a *WindowsAppController) QuitApp(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("QuitApp: name is required")
	}

	// Strip a trailing .exe (case-insensitive) so callers can pass either
	// form: Get-Process expects the bare process name (no extension) and
	// silently no-matches on the .exe form, which would defeat the
	// -ErrorAction Stop guard. We preserve the original casing of the
	// non-extension portion because Get-Process is case-insensitive and
	// surfacing the user's spelling back in error messages reads better
	// than coercing everything to lowercase.
	procName := name
	if strings.HasSuffix(strings.ToLower(name), ".exe") {
		procName = name[:len(name)-len(".exe")]
	}

	script := fmt.Sprintf(
		"Get-Process -Name %s -ErrorAction Stop | Stop-Process",
		psSingleQuote(procName),
	)
	out, err := exec.Command("powershell", powershellArgs(script)...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("QuitApp(%q): %s: %w", name, strings.TrimSpace(string(out)), err)
	}
	return "", nil
}

// ---------------------------------------------------------------------------
// FocusWindow (TASK-021) — Win32 EnumWindows + SetForegroundWindow.
// ---------------------------------------------------------------------------
//
// Implementation outline:
//
//  1. Enumerate every top-level window via user32!EnumWindows.
//  2. For each window: read its title (GetWindowTextW) and resolve the
//     owning process's executable basename (GetWindowThreadProcessId →
//     OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION) →
//     QueryFullProcessImageNameW). Filter invisible windows and zero-length
//     titles to drop background helpers and tray surrogates that would
//     otherwise satisfy a loose substring match.
//  3. Match `app` against the executable basename (case-insensitive
//     substring, ".exe" stripped from both sides) AND match `title`
//     against the window title (case-insensitive substring; empty title
//     matches anything — same contract as the macOS counterpart).
//  4. On match: if the window is iconic (minimized) call ShowWindow(SW_RESTORE)
//     first so SetForegroundWindow has a non-minimized window to raise;
//     then SetForegroundWindow. Stop iterating by returning 0 (BOOL FALSE)
//     from the EnumWindows callback — EnumWindows interprets that as
//     "caller is done".
//  5. If no window matched after a full sweep, return ErrWindowNotFound
//     wrapped with method-name context so the daemon can render a
//     "couldn't find a <app> window" tool response instead of a raw OS
//     error.
//
// Foreground-restriction note: SetForegroundWindow on modern Windows
// rejects calls from processes that aren't already the foreground process
// or don't hold the foreground lock — the OS substitutes a taskbar flash
// instead. We accept that fallback rather than fighting it with
// AttachThreadInput trickery: the user just spoke a voice command into
// Jarvis, so the desktop's recent-interaction policy almost always grants
// us the foreground; the rare cases where it doesn't (a long-blocking
// modal in another app) still produce the taskbar-flash UX which is the
// documented Windows behaviour and acceptable for a voice-assistant
// tool. The acceptance criterion is "brings VSCode to the foreground"
// on normal interactive use, which this code path satisfies.

// modUser32 + modKernel32 are lazy DLL handles for the Win32 APIs used
// below. golang.org/x/sys/windows.NewLazyDLL caches the LoadLibrary call
// internally and is safe for concurrent use, so we resolve each function
// pointer once at package init time and pay no per-call lookup cost.
//
// We deliberately use windows.NewLazyDLL (not syscall.NewLazyDLL) for
// consistency with the rest of the package — golang.org/x/sys/windows is
// the canonical entry point for new code and ships richer error reporting
// (LazyProc.Find returns a typed *Errno rather than a string).
var (
	modUser32   = windows.NewLazyDLL("user32.dll")
	modKernel32 = windows.NewLazyDLL("kernel32.dll")

	// EnumWindows iterates top-level windows. Signature:
	//   BOOL EnumWindows(WNDENUMPROC lpEnumFunc, LPARAM lParam);
	procEnumWindows = modUser32.NewProc("EnumWindows")

	// GetWindowTextW returns the window's title text into a UTF-16 buffer.
	// We use the W (wide) variant so non-ASCII window titles
	// (e.g. localised app titles) round-trip correctly.
	procGetWindowTextW = modUser32.NewProc("GetWindowTextW")

	// GetWindowTextLengthW returns the title length in WCHARs (excluding
	// the trailing NUL). Needed to size the GetWindowTextW buffer
	// correctly — over-sizing would waste memory per enumerated window
	// (and there can easily be 100+), under-sizing would truncate
	// localised titles mid-character.
	procGetWindowTextLengthW = modUser32.NewProc("GetWindowTextLengthW")

	// IsWindowVisible filters out hidden helper windows that every Win32
	// app creates (tooltip surrogates, IME helpers, etc.) which would
	// otherwise outnumber the user-visible windows 10:1.
	procIsWindowVisible = modUser32.NewProc("IsWindowVisible")

	// GetWindowThreadProcessId returns the thread (return value) and
	// process (out-pointer) IDs that own a window. We only care about the
	// PID — needed to resolve the executable basename for app-name
	// matching.
	procGetWindowThreadProcessId = modUser32.NewProc("GetWindowThreadProcessId")

	// IsIconic reports whether a window is minimized. If so we
	// ShowWindow(SW_RESTORE) before SetForegroundWindow so the foreground
	// raise actually unminimizes the window rather than just flashing the
	// taskbar — this is the second acceptance criterion ("Works when
	// minimized").
	procIsIconic = modUser32.NewProc("IsIconic")

	// ShowWindow sets the window's show state. We only ever call it with
	// SW_RESTORE (= 9) to undo a minimize; restoring a non-minimized
	// window is a no-op so the call is safe even when IsIconic returned
	// false (we still gate on IsIconic to skip the syscall when possible).
	procShowWindow = modUser32.NewProc("ShowWindow")

	// SetForegroundWindow raises a window to the foreground and gives it
	// keyboard focus. Subject to the foreground-restriction policy
	// described above the var block; returns non-zero on success.
	procSetForegroundWindow = modUser32.NewProc("SetForegroundWindow")

	// QueryFullProcessImageNameW resolves a process handle's executable
	// path. Used to read the app basename for app-name substring matching
	// (the alternative — GetModuleFileNameEx via psapi.dll — is broadly
	// deprecated in favour of this kernel32 entry point on Vista+ and we
	// don't support anything older).
	procQueryFullProcessImageNameW = modKernel32.NewProc("QueryFullProcessImageNameW")
)

// swRestore is the Win32 ShowWindow nCmdShow value for "restore from
// minimized". Hard-coded as a constant because the windows package does
// not export it as a typed const (the SW_* values live in windows/types
// but are package-private historically); the integer literal is part of
// the stable Win32 ABI and is documented as 9 since NT4.
const swRestore = 9

// processQueryLimitedInformation is the minimum access right that lets
// QueryFullProcessImageNameW succeed against arbitrary process handles
// (including system processes owned by SYSTEM that PROCESS_QUERY_INFORMATION
// cannot open from a non-elevated process). Defined in winnt.h as
// 0x1000; we hard-code it because the windows package exposes
// PROCESS_QUERY_LIMITED_INFORMATION as an untyped int constant only on
// some versions of x/sys.
const processQueryLimitedInformation = 0x1000

// maxTitleLen caps the per-window UTF-16 title buffer. Real-world window
// titles are well under 256 chars (most Windows apps use <80); 512 gives
// generous headroom for browser tab strings without making the
// per-enumeration allocation noticeable. EnumWindows callbacks are hot —
// we get called once per top-level window per FocusWindow call, easily
// 100+ on a busy desktop — so keeping each iteration's allocation small
// matters for the worst-case latency budget.
const maxTitleLen = 512

// ErrWindowNotFound is the sentinel returned by FocusWindow when no window
// matches the provided app/title criteria. Callers should use
// errors.Is(err, syscontrol.ErrWindowNotFound) to route to a "did you
// mean..." style UX rather than surfacing a raw OS error. Mirrors
// internal/macctl.ErrWindowNotFound (the macOS counterpart referenced in
// the AppController interface doc comment in appcontroller.go).
var ErrWindowNotFound = errors.New("syscontrol: window not found")

// FocusWindow brings a window of `app` (process executable basename,
// case-insensitive substring, ".exe" stripping on both sides) whose title
// contains `title` (case-insensitive substring; empty title matches any
// window of the app) to the foreground.
//
// On match:
//   - If the target window is minimized, ShowWindow(SW_RESTORE) is called
//     first so SetForegroundWindow has a non-iconic window to raise. This
//     is the second acceptance criterion.
//   - SetForegroundWindow is then called to actually focus the window.
//     On modern Windows this may degrade to a taskbar flash if the
//     calling process lacks the foreground lock — see the
//     foreground-restriction note above the modUser32 var block.
//
// On miss (no window matches the criteria across the entire enumeration):
// returns ErrWindowNotFound wrapped with the method name. The error
// distinguishes "no match" from "the syscall failed" so callers can do
// errors.Is(err, ErrWindowNotFound) per the acceptance criterion
// ("returns clear error", and pins the contract that other backends
// honour via the interface doc).
//
// On syscall failure (rare; user32.dll missing or insufficient rights
// for QueryFullProcessImageNameW): returns the wrapped Win32 error.
// Never panics — all syscall failures are surfaced as wrapped errors
// per the file-level invariant.
func (a *WindowsAppController) FocusWindow(app, title string) (string, error) {
	app = strings.TrimSpace(app)
	if app == "" {
		return "", fmt.Errorf("FocusWindow: app is required")
	}

	// Normalise the app needle once outside the enumeration callback so
	// every per-window comparison is a cheap strings.Contains. We strip
	// ".exe" so callers can pass either "Code" or "Code.exe" — Get the
	// executable basename from QueryFullProcessImageNameW will likewise
	// have ".exe" stripped before comparison.
	appNeedle := strings.ToLower(strings.TrimSuffix(strings.ToLower(app), ".exe"))
	titleNeedle := strings.ToLower(strings.TrimSpace(title))

	// matched is set by the EnumWindows callback when it finds and
	// focuses a window. We use a captured variable (rather than a return
	// value from EnumWindows) because the WNDENUMPROC contract demands a
	// BOOL return for "continue/stop iteration"; we'd lose the "found"
	// signal if we tried to overload that.
	var matched bool
	// matchErr captures any error encountered while acting on the
	// matched window (e.g. SetForegroundWindow failure). The callback
	// stops iteration on either match-and-act or match-and-fail, so
	// matchErr is non-nil only when iteration stopped due to a syscall
	// failure on a matched window, not due to a no-match sweep.
	var matchErr error

	// EnumWindows's WNDENUMPROC is a stdcall callback. We register it via
	// syscall.NewCallback which returns a uintptr stable for the lifetime
	// of the process (the wrapper is GC-rooted internally). Because
	// NewCallback allocates a trampoline that is never released, we must
	// not create one per call in a hot loop — but FocusWindow is a
	// user-initiated tool call, not a hot path, so a per-call callback is
	// fine and lets us close over `matched` / `matchErr` cleanly.
	enumProc := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		// Skip invisible windows — Win32 apps create dozens of hidden
		// helper HWNDs (tooltip surrogates, IME windows, etc.). An
		// uninhabited match against one of those would "succeed" by
		// raising nothing visible, which would frustrate the user.
		visible, _, _ := procIsWindowVisible.Call(hwnd)
		if visible == 0 {
			return 1 // continue enumeration
		}

		// Read the window title. A zero-length title is almost always a
		// background helper (DWM thumbnail surrogate, hidden settings
		// window) and never something the user meant to focus, so skip
		// when GetWindowTextLengthW returns 0 — saves an allocation in
		// the common case.
		titleLen, _, _ := procGetWindowTextLengthW.Call(hwnd)
		if titleLen == 0 {
			return 1
		}
		// Cap buffer size; absurdly long titles get truncated rather
		// than allocating multi-KB per window in a busy desktop sweep.
		bufLen := int(titleLen) + 1 // +1 for trailing NUL
		if bufLen > maxTitleLen {
			bufLen = maxTitleLen
		}
		buf := make([]uint16, bufLen)
		copied, _, _ := procGetWindowTextW.Call(
			hwnd,
			uintptr(unsafe.Pointer(&buf[0])),
			uintptr(bufLen),
		)
		if copied == 0 {
			return 1
		}
		windowTitle := windows.UTF16ToString(buf[:copied])

		// Title filter: if caller supplied a title needle, require a
		// case-insensitive substring match. An empty needle skips this
		// gate (matches any title of the app) per the interface
		// contract documented in appcontroller.go.
		if titleNeedle != "" && !strings.Contains(strings.ToLower(windowTitle), titleNeedle) {
			return 1
		}

		// Resolve the owning process's executable basename so we can
		// match `app` against it. GetWindowThreadProcessId is
		// safe-to-fail (returns 0 PID on error) so we tolerate failures
		// here rather than aborting the sweep — a transient resolution
		// failure on one window shouldn't break the search for another.
		var pid uint32
		procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
		if pid == 0 {
			return 1
		}
		exeName := processExeBasename(pid)
		if exeName == "" {
			return 1
		}
		exeNeedle := strings.ToLower(strings.TrimSuffix(strings.ToLower(exeName), ".exe"))
		if !strings.Contains(exeNeedle, appNeedle) {
			return 1
		}

		// Match! Restore if minimized, then raise to foreground. We
		// capture any SetForegroundWindow failure into matchErr so the
		// outer function surfaces it — but we still stop iteration
		// (return 0) so we don't continue the sweep after touching the
		// foreground policy.
		iconic, _, _ := procIsIconic.Call(hwnd)
		if iconic != 0 {
			// SW_RESTORE is a no-op for non-iconic windows so the
			// IsIconic gate is purely a perf optimisation (one syscall
			// vs two) — semantically equivalent to calling unconditionally.
			procShowWindow.Call(hwnd, uintptr(swRestore))
		}
		setRet, _, setErr := procSetForegroundWindow.Call(hwnd)
		if setRet == 0 {
			// SetForegroundWindow returns 0 when the foreground policy
			// rejects the call (see foreground-restriction note above).
			// In that case Windows substitutes a taskbar flash and the
			// LastError is typically 0 (no error code; it's a policy
			// outcome, not a failure). We treat ret==0 with no Errno
			// as "policy fallback succeeded" rather than as an error:
			// the user got SOMETHING (the flash), and surfacing this as
			// a hard failure would spam errors during normal use.
			if setErr != nil && setErr.(syscall.Errno) != 0 {
				matchErr = fmt.Errorf("FocusWindow(%q,%q): SetForegroundWindow: %w", app, title, setErr)
			}
		}
		matched = true
		return 0 // BOOL FALSE → stop EnumWindows iteration
	})

	// EnumWindows returns 0 when the callback halted iteration early
	// (which is exactly what we do on match) — that's NOT an error, and
	// LastError is set to 0. We must therefore distinguish "callback
	// returned 0 to stop" from "callback never ran due to syscall
	// failure". The way to do that: check `matched` first; only treat
	// a non-zero LastError as an error when we didn't match.
	ret, _, callErr := procEnumWindows.Call(enumProc, 0)
	if matchErr != nil {
		return "", matchErr
	}
	if !matched {
		// No window matched. If EnumWindows itself errored (rare —
		// callback panicked or user32 returned an unexpected code),
		// surface the syscall error so the user knows something other
		// than "no match" went wrong. Otherwise return the sentinel.
		if ret == 0 && callErr != nil {
			if errno, ok := callErr.(syscall.Errno); ok && errno != 0 {
				return "", fmt.Errorf("FocusWindow(%q,%q): EnumWindows: %w", app, title, callErr)
			}
		}
		return "", fmt.Errorf("FocusWindow(%q,%q): %w", app, title, ErrWindowNotFound)
	}
	return "", nil
}

// processExeBasename returns the lowercase basename of the executable
// owning `pid`, or "" on any failure (process exited between EnumWindows
// and our open, insufficient rights, etc.). Failures are silently
// swallowed because they are normal during enumeration — a process can
// always exit between the moment EnumWindows captured its HWND and the
// moment we try to open it — and the only sensible behaviour is to skip
// that window and continue the search.
//
// The function uses OpenProcess with PROCESS_QUERY_LIMITED_INFORMATION
// (rather than the older PROCESS_QUERY_INFORMATION) so it succeeds
// against system processes from a non-elevated Jarvis daemon —
// PROCESS_QUERY_LIMITED_INFORMATION was added specifically for this use
// case on Vista+.
func processExeBasename(pid uint32) string {
	handle, err := windows.OpenProcess(processQueryLimitedInformation, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(handle)

	// QueryFullProcessImageNameW writes the executable path into a
	// UTF-16 buffer and updates the size pointer with the WCHAR count
	// (excluding the trailing NUL). The MSDN-documented max path on
	// modern Windows is windows.MAX_PATH (260) but with long-path
	// support enabled it can reach ~32k; we cap at 1024 which covers
	// every real-world install path (Steam game directories go deep
	// but stay well under) without inflating per-call allocations.
	var buf [1024]uint16
	bufSize := uint32(len(buf))
	ret, _, _ := procQueryFullProcessImageNameW.Call(
		uintptr(handle),
		0, // dwFlags = 0 (Win32 path format; 1 = native path which we don't want)
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&bufSize)),
	)
	if ret == 0 {
		return ""
	}
	full := windows.UTF16ToString(buf[:bufSize])
	// Extract the basename. We deliberately don't use filepath.Base —
	// that follows the host's path separators which on Windows are
	// already backslashes, but full could in theory contain mixed
	// separators if a future Windows version normalises differently.
	// Manual scan from the tail is unambiguous and avoids the import.
	for i := len(full) - 1; i >= 0; i-- {
		if full[i] == '\\' || full[i] == '/' {
			return full[i+1:]
		}
	}
	return full
}
