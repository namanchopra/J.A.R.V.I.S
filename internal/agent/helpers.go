package agent

import (
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

// stopProcess sends SIGTERM to the process and waits for it to exit within the
// given timeout. If the process does not exit in time, it sends SIGKILL.
// This is the shared stop logic used by adapters whose CLI tools do not require
// special shutdown handling.
func stopProcess(cmd *exec.Cmd, timeout time.Duration) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}

	// Send SIGTERM for graceful shutdown.
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		// Process may have already exited.
		return nil
	}

	// Wait for the process to exit within the timeout.
	exited := make(chan struct{})
	go func() {
		// Ignore the error — the actual exit error is delivered via session.Done.
		_, _ = cmd.Process.Wait()
		close(exited)
	}()

	select {
	case <-exited:
		return nil
	case <-time.After(timeout):
		if err := cmd.Process.Signal(syscall.SIGKILL); err != nil {
			return fmt.Errorf("force kill: %w", err)
		}
		return nil
	}
}
