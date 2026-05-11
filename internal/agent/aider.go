package agent

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/namanchopra/jarvis/internal/model"
)

// AiderAdapter implements AgentAdapter for the Aider CLI tool.
type AiderAdapter struct{}

// NewAiderAdapter returns a new AiderAdapter.
func NewAiderAdapter() *AiderAdapter { return &AiderAdapter{} }

// Name returns the agent type this adapter handles.
func (a *AiderAdapter) Name() model.AgentType {
	return model.AgentAider
}

// IsAvailable checks whether the "aider" CLI is installed and in PATH.
func (a *AiderAdapter) IsAvailable() bool {
	_, err := exec.LookPath("aider")
	return err == nil
}

// Launch starts a new Aider session with the given prompt and returns a
// RunningSession that streams output line by line.
func (a *AiderAdapter) Launch(ctx context.Context, opts LaunchOptions) (*RunningSession, error) {
	args := []string{"--message", opts.Prompt, "--yes", "--no-auto-commits"}

	args = append(args, opts.ExtraArgs...)

	cmdCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(cmdCtx, "aider", args...)
	cmd.Dir = opts.RepoPath

	if len(opts.Env) > 0 {
		cmd.Env = append(cmd.Environ(), opts.Env...)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("aider stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("aider stderr pipe: %w", err)
	}

	output := make(chan OutputLine, 200)
	done := make(chan error, 1)

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("aider start: %w", err)
	}

	rs := &RunningSession{
		PID:       cmd.Process.Pid,
		SessionID: opts.SessionID,
		Output:    output,
		Done:      done,
		Cmd:       cmd,
		SendInput: nil, // Aider --message mode does not support interactive stdin.
		cancel:    cancel,
	}

	// Read stdout line-by-line.
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 512*1024), 512*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}
			output <- OutputLine{
				Text:      line,
				Timestamp: time.Now(),
			}
		}
	}()

	// Read stderr line-by-line.
	go func() {
		scanner := bufio.NewScanner(stderr)
		scanner.Buffer(make([]byte, 0, 256*1024), 256*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}
			output <- OutputLine{
				Text:      line,
				Timestamp: time.Now(),
				IsError:   true,
			}
		}
	}()

	// Wait for the process to exit.
	go func() {
		err := cmd.Wait()
		done <- err
		close(done)
		close(output)
	}()

	return rs, nil
}

// SendMessage is not supported for Aider.
// Aider does not support session resume; each invocation is a standalone run.
func (a *AiderAdapter) SendMessage(_ context.Context, _ *RunningSession, _ string) error {
	return fmt.Errorf("aider adapter: SendMessage is not supported; aider does not support session resume")
}

// Stop gracefully terminates a running Aider session. It sends SIGTERM first,
// then SIGKILL after a 5-second grace period if the process has not exited.
func (a *AiderAdapter) Stop(_ context.Context, session *RunningSession) error {
	return stopProcess(session.Cmd, 5*time.Second)
}
