package syscontrol

// ClipboardController reads from and writes to the system clipboard.
// The macOS reference implementation (internal/macctl/clipboard.go)
// shells `pbpaste` and `pbcopy`; TASK-026's Windows backend uses
// golang.design/x/clipboard which itself wraps the Win32 clipboard API.
//
// Implementations gate destructive operations through their own policy
// layer (see internal/macctl/policy.go for the macOS reference) before
// writing. Reads default to allow (purely informational); writes default
// to ask because they silently overwrite whatever the user previously
// copied.
type ClipboardController interface {
	// ClipboardGet returns the current clipboard text verbatim — no
	// trimming, no normalization. Implementations MUST return ("",
	// nil) (not an error) when the clipboard is empty; the daemon's
	// spoken-response layer handles "empty clipboard" framing.
	ClipboardGet() (string, error)

	// ClipboardSet writes text to the clipboard. Destructive — the
	// previous clipboard contents are lost. Empty text is a valid
	// input (deliberately clearing the clipboard) and MUST NOT be
	// rejected as if it were a missing argument.
	ClipboardSet(text string) (string, error)
}
