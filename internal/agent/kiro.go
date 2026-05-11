package agent

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"syscall"
	"time"

	"github.com/namanchopra/jarvis/internal/model"
)

// KiroAdapter implements AgentAdapter for the Kiro CLI tool.
type KiroAdapter struct{}

// NewKiroAdapter creates a new KiroAdapter instance.
func NewKiroAdapter() *KiroAdapter {
	return &KiroAdapter{}
}

// Name returns the agent type this adapter handles.
func (k *KiroAdapter) Name() model.AgentType {
	return model.AgentKiro
}

// IsAvailable checks whether the kiro-cli binary is installed and on PATH.
func (k *KiroAdapter) IsAvailable() bool {
	_, err := exec.LookPath("kiro-cli")
	return err == nil
}

// Launch starts a new Kiro CLI session with the given options.
func (k *KiroAdapter) Launch(ctx context.Context, opts LaunchOptions) (*RunningSession, error) {
	args := []string{"chat", "--no-interactive", "--trust-all-tools"}

	if opts.SessionID != "" {
		args = append(args, "--resume")
	}

	args = append(args, opts.Prompt)
	args = append(args, opts.ExtraArgs...)

	cmdCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(cmdCtx, "kiro-cli", args...)
	cmd.Dir = opts.RepoPath

	if len(opts.Env) > 0 {
		cmd.Env = append(cmd.Environ(), opts.Env...)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("kiro stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("kiro stderr pipe: %w", err)
	}

	output := make(chan OutputLine, 200)
	done := make(chan error, 1)

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("kiro start: %w", err)
	}

	// Read stdout line-by-line.
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			output <- OutputLine{
				Text:      scanner.Text(),
				Timestamp: time.Now(),
			}
		}
	}()

	// Read stderr line-by-line.
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			output <- OutputLine{
				Text:      scanner.Text(),
				Timestamp: time.Now(),
				IsError:   true,
			}
		}
	}()

	// Wait for exit.
	go func() {
		defer close(output)
		defer close(done)
		done <- cmd.Wait()
	}()

	session := &RunningSession{
		PID:       cmd.Process.Pid,
		SessionID: opts.SessionID,
		Output:    output,
		SendInput: nil, // Kiro CLI in --no-interactive mode does not support stdin
		Done:      done,
		Cmd:       cmd,
		cancel:    cancel,
	}

	return session, nil
}

// SendMessage sends a follow-up message to an existing Kiro session.
// Kiro CLI supports --resume for continuing sessions, so callers should use
// Launch with a SessionID instead of SendMessage.
func (k *KiroAdapter) SendMessage(_ context.Context, _ *RunningSession, _ string) error {
	return fmt.Errorf("kiro adapter: SendMessage is not supported; use Launch with --resume (set LaunchOptions.SessionID) to continue a session")
}

// Stop gracefully terminates a running Kiro session. It sends SIGTERM first,
// then SIGKILL after a 5-second grace period if the process has not exited.
func (k *KiroAdapter) Stop(_ context.Context, session *RunningSession) error {
	if session.Cmd == nil || session.Cmd.Process == nil {
		return nil
	}

	// Send SIGTERM for graceful shutdown.
	if err := session.Cmd.Process.Signal(syscall.SIGTERM); err != nil {
		// Process may already be gone.
		return nil
	}

	// Wait up to 5 seconds for the process to exit.
	select {
	case <-session.Done:
		return nil
	case <-time.After(5 * time.Second):
		// Force kill.
		if err := session.Cmd.Process.Kill(); err != nil {
			return fmt.Errorf("kiro kill: %w", err)
		}
		return nil
	}
}
