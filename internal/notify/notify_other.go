//go:build !darwin

package notify

// Send is a no-op on non-macOS platforms.
func Send(title, message string) error {
	return nil
}
