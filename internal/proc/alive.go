package proc

import (
	"os"
	"syscall"
)

// IsAlive checks if a process with the given PID is still running.
func IsAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, FindProcess always succeeds. Signal 0 checks existence.
	return p.Signal(syscall.Signal(0)) == nil
}
