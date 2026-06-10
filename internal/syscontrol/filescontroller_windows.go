//go:build windows

package syscontrol

// filescontroller_windows.go — Windows backend for the FilesController
// interface declared in filescontroller.go. TASK-025 of the v0.4.0
// Windows port.
//
// Symmetry with the macOS reference (internal/macctl/files.go):
//
//	macOS                          Windows
//	-----                          -------
//	open <path>                    explorer.exe <path>
//	mdfind <query>                 explorer.exe search-ms:query=<escaped>
//
// Why explorer.exe (rather than `cmd /c start`):
//   - explorer.exe is the canonical "open this thing in the user's
//     default handler" entry point on Windows; the shell delegates to
//     the same Shell API surface that File Explorer itself uses.
//   - It handles both filesystem paths AND protocol-handler URIs
//     (http://, https://, search-ms:, mailto:, ...) uniformly with a
//     single argument, matching macOS `open`'s behaviour.
//   - exec.Command's argv-style invocation gives correct quoting for
//     paths with spaces, embedded quotes, etc. without us having to
//     hand-roll cmd.exe quoting rules — which is the bear trap the
//     acceptance criterion "spaces escaped correctly" calls out.
//
// Why fire-and-forget (cmd.Start, not cmd.Run):
//   - explorer.exe's exit code is notoriously unreliable. It frequently
//     returns 1 even on a successful launch (a long-standing Windows
//     shell quirk going back to NT4). Waiting on it and treating exit
//     1 as failure would produce constant false negatives.
//   - The user-visible signal of success is the new window appearing;
//     blocking the daemon goroutine on .Wait() for an interactive UI
//     launch would be wrong anyway.
//
// Why we still pre-validate filesystem paths:
//   - With fire-and-forget we'd otherwise silently no-op for typos /
//     hallucinated paths (the "silent success" trap that TASK-013
//     specifically flagged on macOS). We os.Stat any input that looks
//     like a local filesystem path (drive-letter or UNC) and surface
//     a clear error if it doesn't exist. URIs (anything with a scheme
//     followed by ':') are passed straight through — only the OS knows
//     which handlers are installed.
//
// Why we do NOT use cmd.exe / PowerShell:
//   - Routing through a shell introduces its own quoting rules on top
//     of Go's exec.Command argv handling, which is exactly the surface
//     where the "spaces escaped correctly" acceptance criterion would
//     break. Calling explorer.exe directly with argv keeps quoting
//     deterministic.

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
)

// WindowsFilesController is the Windows implementation of
// syscontrol.FilesController. Zero-value usable; obtain via
// NewWindowsFilesController for forward-compatibility if we later need
// to thread in a policy gate parallel to *macctl.Controller.
//
// Compile-time interface assertion lives at the bottom of the file so
// drift in either FilesController or this type fails the build.
type WindowsFilesController struct{}

// NewWindowsFilesController returns a ready-to-use Windows FilesController.
// Kept as a constructor (rather than exposing the zero value) so adding a
// policy field later does not break callers.
func NewWindowsFilesController() *WindowsFilesController {
	return &WindowsFilesController{}
}

// OpenPath launches a filesystem path or URI in its default Windows
// handler via `explorer.exe <arg>`. Returns ("", nil) on a successful
// launch.
//
// Behaviour matrix (mirrors macOS internal/macctl/files.go):
//
//	input                          action
//	-----                          ------
//	""                             error (path required)
//	"C:\Users\x\note.txt"          stat -> explorer.exe note.txt
//	"C:\path with spaces\a.pdf"    stat -> explorer.exe "path with spaces\a.pdf"
//	"\\server\share\foo"           stat -> explorer.exe UNC path
//	"https://example.com"          explorer.exe https://example.com
//	"search-ms:query=invoice"      explorer.exe search-ms:query=invoice
//	"C:\does\not\exist.txt"        error (path does not exist)
//
// Spaces in paths are handled by exec.Command's argv-style invocation —
// we pass `path` as a single argv element so the Windows command-line
// quoting layer escapes embedded spaces correctly. We never concatenate
// path into a command string.
func (c *WindowsFilesController) OpenPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("OpenPath: path is required")
	}

	// Pre-validate anything that looks like a local filesystem path.
	// explorer.exe's exit code is unreliable (see file-level comment),
	// so we cannot rely on it to signal missing-file errors. The OS
	// also pops a modal "Windows cannot find ..." dialog in that case,
	// which would block a daemon-driven launch — surface the error
	// before invoking explorer instead.
	if isLocalFilesystemPath(path) {
		if _, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("OpenPath(%q): %w", path, err)
		}
	}

	// Fire-and-forget. explorer.exe's exit code is unreliable; the
	// user-visible signal of success is the new window appearing. If
	// the Start() call itself fails (e.g. explorer.exe missing from
	// PATH on a stripped-down Server SKU) we surface that error.
	cmd := exec.Command("explorer.exe", path)
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("OpenPath(%q): %w", path, err)
	}
	// Release process resources without waiting on exit.
	if cmd.Process != nil {
		_ = cmd.Process.Release()
	}
	return "", nil
}

// Spotlight opens Windows Search pre-filled with `query` via the
// `search-ms:` URI scheme. Read-only by contract — never modifies user
// state, only surfaces an interactive search UI.
//
// Behavioural deviation from macOS mdfind: macOS returns up to 20
// result paths joined by '\n'. Windows' search-ms: URI launches an
// interactive Explorer search window rather than producing a result
// list a daemon can read, so we return ("", nil) on a successful
// launch. Callers that need machine-readable hits should bind a
// separate "search the filesystem" tool against Windows.Storage.Search
// (out of scope for TASK-025; the interactive UX is what the v0.4.0
// plan calls for).
//
// Escaping: the query is URL-percent-encoded with net/url.QueryEscape
// so spaces, ampersands, quotes, and non-ASCII characters round-trip
// correctly through the search-ms: handler. Bare spaces in the URI
// would either be split into multiple argv elements by exec.Command
// (mangling the query) or rejected by the protocol handler — encoding
// avoids both failure modes and is exactly the "spaces escaped
// correctly" acceptance criterion.
func (c *WindowsFilesController) Spotlight(query string) (string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", fmt.Errorf("Spotlight: query is required")
	}

	// net/url.QueryEscape encodes spaces as "+", which is valid in
	// application/x-www-form-urlencoded bodies but the search-ms:
	// handler expects "%20" for spaces (per the
	// search-ms: URI scheme spec). Rewrite "+" to "%20" — the literal
	// "+" character is itself encoded as "%2B" by QueryEscape, so this
	// substitution is safe and unambiguous.
	escaped := strings.ReplaceAll(url.QueryEscape(query), "+", "%20")
	uri := "search-ms:query=" + escaped

	cmd := exec.Command("explorer.exe", uri)
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("Spotlight(%q): %w", query, err)
	}
	if cmd.Process != nil {
		_ = cmd.Process.Release()
	}
	return "", nil
}

// isLocalFilesystemPath reports whether `s` looks like a Windows local
// or UNC path (as opposed to a URI with a scheme). Used by OpenPath to
// decide whether to pre-validate with os.Stat.
//
// Heuristics, in order:
//   - UNC paths start with `\\` (or `//` if a caller passed a forward-
//     slashed variant).
//   - Drive-letter paths start with `<letter>:\` or `<letter>:/` — note
//     this overlaps with URI schemes (`http:`, `search-ms:`, ...), so
//     we require the third character to be a path separator. A bare
//     `C:` with no separator is ambiguous; we treat it as a path
//     (matches Windows shell behaviour, which interprets `C:` as the
//     current directory on drive C:).
//
// Anything else is treated as a URI and passed through to explorer.exe
// without validation.
func isLocalFilesystemPath(s string) bool {
	if len(s) >= 2 && (s[:2] == `\\` || s[:2] == "//") {
		return true
	}
	if len(s) >= 2 && isDriveLetter(s[0]) && s[1] == ':' {
		if len(s) == 2 {
			return true
		}
		if s[2] == '\\' || s[2] == '/' {
			return true
		}
		// Two-char-plus form like `C:foo` is technically a Windows
		// relative-path-on-drive — treat as filesystem so we surface
		// a clear error rather than silently passing to explorer.
		return true
	}
	return false
}

// isDriveLetter reports whether b is an ASCII letter A-Z or a-z.
// Avoids unicode.IsLetter so we match exactly the Windows drive-letter
// grammar (NT path namespace permits only ASCII letters here).
func isDriveLetter(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}

// Compile-time assertion: *WindowsFilesController satisfies the
// FilesController interface. A signature drift on either side fails
// the build here rather than at a distant call site.
var _ FilesController = (*WindowsFilesController)(nil)
