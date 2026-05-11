package agent

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/namanchopra/jarvis/internal/model"
)

// GeminiAdapter implements AgentAdapter for the Gemini CLI tool.
type GeminiAdapter struct{}

// NewGeminiAdapter returns a new GeminiAdapter.
func NewGeminiAdapter() *GeminiAdapter { return &GeminiAdapter{} }

// Name returns the agent type this adapter handles.
func (a *GeminiAdapter) Name() model.AgentType {
	return model.AgentGemini
}

// IsAvailable checks whether the "gemini" CLI is installed and in PATH.
func (a *GeminiAdapter) IsAvailable() bool {
	_, err := exec.LookPath("gemini")
	return err == nil
}

// Launch starts a new Gemini CLI session with the given prompt and returns a
// RunningSession that streams output line by line.
func (a *GeminiAdapter) Launch(ctx context.Context, opts LaunchOptions) (*RunningSession, error) {
	args := []string{"-p", opts.Prompt, "--sandbox=false"}

	if opts.SessionID != "" {
		args = append(args, "--session-id", opts.SessionID)
	}

	args = append(args, opts.ExtraArgs...)

	cmdCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(cmdCtx, "gemini", args...)
	cmd.Dir = opts.RepoPath

	if len(opts.Env) > 0 {
		cmd.Env = append(cmd.Environ(), opts.Env...)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("gemini stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("gemini stderr pipe: %w", err)
	}

	output := make(chan OutputLine, 200)
	done := make(chan error, 1)

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("gemini start: %w", err)
	}

	rs := &RunningSession{
		PID:       cmd.Process.Pid,
		SessionID: opts.SessionID,
		Output:    output,
		Done:      done,
		Cmd:       cmd,
		SendInput: nil, // Gemini CLI does not support interactive stdin in -p mode.
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

// SendMessage is not supported for Gemini CLI in non-interactive mode.
// Use Launch with LaunchOptions.SessionID set to resume a session instead.
func (a *GeminiAdapter) SendMessage(_ context.Context, _ *RunningSession, _ string) error {
	return fmt.Errorf("gemini adapter: SendMessage is not supported; use Launch with SessionID to resume a session")
}

// Stop gracefully terminates a running Gemini session. It sends SIGTERM first,
// then SIGKILL after a 5-second grace period if the process has not exited.
func (a *GeminiAdapter) Stop(_ context.Context, session *RunningSession) error {
	return stopProcess(session.Cmd, 5*time.Second)
}
