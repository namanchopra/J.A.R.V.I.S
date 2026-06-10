package syscontrol

// ScreenshotController captures the screen, a window, or a user-drawn
// selection rectangle to a PNG on disk and returns the absolute path.
// The macOS reference implementation (internal/macctl/screenshot.go)
// shells `screencapture`; TASK-027's Windows backend uses
// Windows.Graphics.Capture or the Snipping Tool's PowerShell interop.
//
// Implementations gate operations through their own policy layer (see
// internal/macctl/policy.go for the macOS reference). The default is
// allow on both platforms — the resulting file lives under the user's
// own ~/.jarvis tree, the screen content is the user's own work, and
// the interactive modes require an explicit user gesture before any
// pixels are captured.
type ScreenshotController interface {
	// Screenshot captures and writes a PNG to ~/.jarvis/screenshots/
	// (or the platform equivalent — see internal/paths), returning
	// the absolute path of the saved file.
	//
	// `target` selects the capture mode:
	//
	//	"screen"    — entire main display (non-interactive).
	//	"window"    — interactive window picker.
	//	"selection" — interactive crosshair-drag rectangle.
	//
	// An invalid target MUST be rejected with a wrapped error that
	// substring-matches "invalid" (see internal/macctl/screenshot.go
	// for the macOS rationale). When the user cancels an interactive
	// capture (Esc), implementations MUST return a clear error rather
	// than a phantom path that the daemon would then try to upload.
	Screenshot(target string) (string, error)
}
