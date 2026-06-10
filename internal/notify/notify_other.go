//go:build !darwin && !windows

package notify

// Send is a no-op on platforms without a native notification backend
// (currently Linux). macOS uses notify_darwin.go and Windows uses
// notify_windows.go.
func Send(title, message string) error {
	return nil
}
