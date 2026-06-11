package syscontrol

// FilesController exposes "open this filesystem path or URL in its
// default handler" and a Spotlight-equivalent file-search primitive.
// The macOS reference implementation (internal/macctl/files.go) shells
// `open` for path handling and `mdfind` for search; TASK-025's Windows
// backend shells `explorer.exe` and the `search-ms:` URI scheme.
//
// Implementations gate destructive operations through their own policy
// layer (see internal/macctl/policy.go for the macOS reference) before
// touching the filesystem. Search is read-only and defaults to allow on
// macOS; opening arbitrary paths defaults to ask because a malicious
// LLM hallucination could in principle exfiltrate via URL.
type FilesController interface {
	// OpenPath opens a filesystem path or URL in its default handler.
	// macOS: `open <path>`; Windows: `explorer.exe <path>`. The same
	// command works for both file paths and URLs on both platforms.
	// An empty path MUST be rejected with a wrapped error rather than
	// silently no-oping. Implementations SHOULD surface stderr in the
	// wrapped error so callers see "the file does not exist" rather
	// than a bare exit code.
	OpenPath(path string) (string, error)

	// Spotlight runs a system-wide filename / metadata search and
	// returns up to 20 matching absolute paths joined by '\n'. macOS:
	// `mdfind`; Windows: the `search-ms:` URI scheme (TASK-025) plus
	// a follow-up file enumeration. Read-only — never modifies user
	// state. Returns ("", nil) when the query has no hits (not an
	// error).
	Spotlight(query string) (string, error)
}
