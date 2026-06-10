//go:build windows

// screenshotcontroller_windows.go — Windows backend for the
// syscontrol.ScreenshotController interface. Mirrors the macOS reference
// implementation at internal/macctl/screenshot.go but uses PowerShell
// interop with the .NET System.Drawing / System.Windows.Forms classes
// (built into every supported Windows SKU) for the screen / window
// captures, and shells SnippingTool.exe for interactive selection.
//
// Pipeline shape:
//
//	"screen"    → powershell System.Windows.Forms.Screen.PrimaryScreen.Bounds
//	              + Graphics.CopyFromScreen → PNG via Bitmap.Save.
//	"window"    → powershell user32!GetForegroundWindow + GetWindowRect
//	              + Graphics.CopyFromScreen on the foreground rect.
//	"selection" → SnippingTool.exe /clip then read PNG out of the
//	              clipboard; if the tool is missing or the user cancels,
//	              fall back to full-screen capture (acceptance criterion
//	              "Snipping unavailable falls back to full screen").
//
// Why PowerShell + .NET rather than direct Win32 syscalls: keeps the
// implementation CGO-free, sidesteps the need to vendor a WinRT runtime
// for Windows.Graphics.Capture, and the tooling is preinstalled on every
// supported Windows 10 / 11 release. The latency cost (~200ms PowerShell
// cold-start) is acceptable for a user-initiated voice command.
//
// Permission model: Windows has no system-level "Screen Recording"
// permission equivalent to macOS TCC. Any process can read the desktop
// surface, so no permission prompt or fallback is needed — the only
// failure modes are missing binaries (rare) or interactive cancellation.

package syscontrol

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/namanchopra/jarvis/internal/paths"
)

// WindowsScreenshotController is the Windows backend for
// syscontrol.ScreenshotController. The zero value is usable — there is
// no configuration to thread through.
//
// powershell is a test seam: production passes a closure that shells
// powershell.exe -NoProfile -Command <script>. Tests substitute a
// recorder so they can assert which scripts get issued without invoking
// the real PowerShell binary (which would actually capture the screen).
// Kept unexported so callers outside the package cannot override it;
// in-package tests swap c.powershell directly on the struct.
type WindowsScreenshotController struct {
	powershell powershellFn

	// snippingTool is a test seam for SnippingTool.exe /clip invocation.
	// Returns the path to the .exe if found, "" otherwise. Tests
	// substitute a recorder to simulate the "Snipping Tool unavailable"
	// branch without mutating the real PATH.
	snippingTool snippingToolLocator
}

// powershellFn is the type of the PowerShell shell-out closure. The
// script is executed via `powershell.exe -NoProfile -ExecutionPolicy
// Bypass -Command <script>`. stdout is returned verbatim. -NoProfile is
// load-bearing — a slow user profile script would otherwise add hundreds
// of milliseconds to every capture.
type powershellFn func(script string) (stdout string, err error)

// snippingToolLocator returns the absolute path to SnippingTool.exe if
// present on the host (Windows 10 1809+ ships it in System32), or ""
// when unavailable. Returning "" routes to the full-screen fallback per
// the failure-case acceptance criterion.
type snippingToolLocator func() string

// Compile-time assertion that WindowsScreenshotController satisfies the
// ScreenshotController interface. Drift in either signature fails the
// build at the canonical location rather than at the call site.
var _ ScreenshotController = (*WindowsScreenshotController)(nil)

// NewWindowsScreenshotController returns a Controller wired with the
// production PowerShell + SnippingTool locators. Tests should construct
// the struct literal directly so they can inject seam functions.
func NewWindowsScreenshotController() *WindowsScreenshotController {
	return &WindowsScreenshotController{
		powershell:   defaultPowershell,
		snippingTool: defaultSnippingToolLocator,
	}
}

// defaultPowershell is the production PowerShell invoker. Shells out to
// powershell.exe with -NoProfile and -ExecutionPolicy Bypass so the
// inline capture script cannot be blocked by a host-level execution
// policy (the script is built from constants — no user input flows in).
// stdout is returned verbatim; stderr is captured and merged into the
// returned error when the process exits non-zero so the failure mode
// has actionable context.
func defaultPowershell(script string) (string, error) {
	cmd := exec.Command("powershell.exe",
		"-NoProfile",
		"-NonInteractive",
		"-ExecutionPolicy", "Bypass",
		"-Command", script,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("powershell: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

// defaultSnippingToolLocator probes the well-known System32 location for
// SnippingTool.exe. We do not consult %PATH% — the tool is a Windows
// component, not a user-installed binary, so the canonical path is the
// only one we want to trust. Returns "" on any stat failure so callers
// route to the full-screen fallback rather than blocking on a PATH walk.
func defaultSnippingToolLocator() string {
	// %SystemRoot% defaults to C:\Windows but respects relocated installs.
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	p := filepath.Join(root, "System32", "SnippingTool.exe")
	info, err := os.Stat(p)
	if err != nil || info.IsDir() {
		return ""
	}
	return p
}

// Screenshot captures the requested target to a PNG under
// ~/.jarvis/screenshots/<unix>.png and returns the absolute path. The
// `target` selector accepts "screen", "window", or "selection". An
// invalid target is rejected with a wrapped error whose message
// substring-matches "invalid" — the same contract the macOS reference
// upholds so the daemon's tool layer can match on a single substring
// across platforms.
//
// "selection" mode shells out to SnippingTool.exe /clip. When the
// Snipping Tool is unavailable on the host (older SKUs, stripped
// installations) the implementation falls back to a full-screen capture
// rather than failing — the failure-case acceptance criterion.
func (c *WindowsScreenshotController) Screenshot(target string) (string, error) {
	// Unix-second granularity is sufficient — a user voicing back-to-back
	// "screenshot" commands would have to fire two within a single second
	// for the collision to matter, and the second one would simply
	// overwrite the first PNG (a behaviour we'd want anyway since the
	// older one is definitionally stale at that point).
	dir := paths.DataPath("screenshots")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("Screenshot: mkdir %s: %w", dir, err)
	}
	out := filepath.Join(dir, fmt.Sprintf("%d.png", time.Now().Unix()))

	switch target {
	case "screen":
		if err := c.captureScreen(out); err != nil {
			return "", fmt.Errorf("Screenshot(screen): %w", err)
		}
	case "window":
		if err := c.captureWindow(out); err != nil {
			return "", fmt.Errorf("Screenshot(window): %w", err)
		}
	case "selection":
		if err := c.captureSelection(out); err != nil {
			return "", fmt.Errorf("Screenshot(selection): %w", err)
		}
	default:
		return "", fmt.Errorf("Screenshot: invalid target %q: must be screen|window|selection", target)
	}

	// Post-check: every capture path is supposed to write a non-empty
	// PNG. If the file is missing or zero bytes after a "success" return
	// from the underlying tool, surface a clear error rather than a
	// phantom path that the daemon would then try to upload.
	info, statErr := os.Stat(out)
	if statErr != nil || info.Size() == 0 {
		return "", fmt.Errorf("Screenshot(%s): no file written", target)
	}
	return out, nil
}

// captureScreen writes a full-display PNG via PowerShell + .NET
// System.Drawing. The script grabs the primary screen bounds, creates a
// Bitmap of matching dimensions, blits the desktop into it via
// Graphics.CopyFromScreen, and saves as PNG. Single quotes around the
// output path are doubled to escape PowerShell's string syntax.
func (c *WindowsScreenshotController) captureScreen(outPath string) error {
	script := fmt.Sprintf(`
Add-Type -AssemblyName System.Drawing
Add-Type -AssemblyName System.Windows.Forms
$bounds = [System.Windows.Forms.Screen]::PrimaryScreen.Bounds
$bitmap = New-Object System.Drawing.Bitmap $bounds.Width, $bounds.Height
$graphics = [System.Drawing.Graphics]::FromImage($bitmap)
$graphics.CopyFromScreen($bounds.Location, [System.Drawing.Point]::Empty, $bounds.Size)
$bitmap.Save('%s', [System.Drawing.Imaging.ImageFormat]::Png)
$graphics.Dispose()
$bitmap.Dispose()
`, escapePSPath(outPath))
	if _, err := c.powershell(script); err != nil {
		return fmt.Errorf("captureScreen: %w", err)
	}
	return nil
}

// captureWindow writes a PNG of only the foreground window. The script
// pinvokes user32!GetForegroundWindow + GetWindowRect to locate the
// window, then copies that rectangle into a bitmap. If the foreground
// window has zero area (rare — minimised state races), we fall back to
// the full primary screen so the user still gets a useful artifact.
func (c *WindowsScreenshotController) captureWindow(outPath string) error {
	script := fmt.Sprintf(`
Add-Type -AssemblyName System.Drawing
Add-Type -AssemblyName System.Windows.Forms
$signature = @'
[DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
[DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr hWnd, out RECT lpRect);
[StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left; public int Top; public int Right; public int Bottom; }
'@
$type = Add-Type -MemberDefinition $signature -Name 'WinAPI' -Namespace 'Jarvis' -UsingNamespace System.Runtime.InteropServices -PassThru
$hwnd = $type::GetForegroundWindow()
$rect = New-Object Jarvis.WinAPI+RECT
[void]$type::GetWindowRect($hwnd, [ref]$rect)
$w = $rect.Right - $rect.Left
$h = $rect.Bottom - $rect.Top
if ($w -le 0 -or $h -le 0) {
  $bounds = [System.Windows.Forms.Screen]::PrimaryScreen.Bounds
  $w = $bounds.Width
  $h = $bounds.Height
  $rect.Left = $bounds.X
  $rect.Top  = $bounds.Y
}
$bitmap = New-Object System.Drawing.Bitmap $w, $h
$graphics = [System.Drawing.Graphics]::FromImage($bitmap)
$graphics.CopyFromScreen($rect.Left, $rect.Top, 0, 0, (New-Object System.Drawing.Size($w, $h)))
$bitmap.Save('%s', [System.Drawing.Imaging.ImageFormat]::Png)
$graphics.Dispose()
$bitmap.Dispose()
`, escapePSPath(outPath))
	if _, err := c.powershell(script); err != nil {
		return fmt.Errorf("captureWindow: %w", err)
	}
	return nil
}

// captureSelection invokes SnippingTool.exe /clip for an interactive
// crosshair-drag rectangle. The tool copies the selection to the
// clipboard rather than to a file, so we then run a small PowerShell
// snippet to flush the clipboard image to disk as PNG.
//
// Failure modes — all of which fall through to a full-screen capture so
// the user always gets a usable artifact (acceptance criterion):
//
//   - SnippingTool.exe missing on the host (older Win10 SKUs, server
//     installs, stripped images).
//   - User cancels with Esc — the clipboard does not receive an image,
//     so the clipboard-flush step writes nothing and we retry with
//     captureScreen.
//   - The clipboard already holds non-image content — same outcome.
func (c *WindowsScreenshotController) captureSelection(outPath string) error {
	snip := c.snippingTool()
	if snip == "" {
		// Snipping Tool not installed — fall back to full screen so the
		// failure-case acceptance criterion ("Snipping unavailable falls
		// back to full screen") is satisfied.
		return c.captureScreen(outPath)
	}

	// /clip puts the user-drawn rectangle on the clipboard and exits;
	// blocking on the process Run() naturally awaits the user's gesture.
	cmd := exec.Command(snip, "/clip")
	if err := cmd.Run(); err != nil {
		// Tool present but failed to launch — fall back rather than
		// surface a non-actionable exec error to the user.
		return c.captureScreen(outPath)
	}

	// Flush clipboard image to disk. If the clipboard does not contain
	// an image (Esc cancellation, the tool exited without a capture, or
	// a clipboard manager beat us to it), the PS snippet writes nothing
	// and we fall through to the full-screen path.
	script := fmt.Sprintf(`
Add-Type -AssemblyName System.Drawing
Add-Type -AssemblyName System.Windows.Forms
$img = [System.Windows.Forms.Clipboard]::GetImage()
if ($img -ne $null) {
  $img.Save('%s', [System.Drawing.Imaging.ImageFormat]::Png)
  $img.Dispose()
  Write-Output 'OK'
} else {
  Write-Output 'CANCELLED'
}
`, escapePSPath(outPath))
	stdout, err := c.powershell(script)
	if err != nil {
		return c.captureScreen(outPath)
	}
	if !strings.Contains(stdout, "OK") {
		// User cancelled or clipboard held no image — graceful fallback.
		return c.captureScreen(outPath)
	}
	return nil
}

// escapePSPath escapes a filesystem path for inclusion inside a
// PowerShell single-quoted string literal. In PowerShell single quotes
// only need single-quote-doubling for escapes; backslashes are taken
// verbatim, which is exactly what we want for Windows paths.
func escapePSPath(p string) string {
	return strings.ReplaceAll(p, "'", "''")
}
