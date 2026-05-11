package agent

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/namanchopra/jarvis/internal/model"
)

// CodexAdapter implements AgentAdapter for the OpenAI Codex CLI tool.
type CodexAdapter struct{}

// NewCodexAdapter returns a new CodexAdapter.
func NewCodexAdapter() *CodexAdapter { return &CodexAdapter{} }

// Name returns the agent type this adapter handles.
func (a *CodexAdapter) Name() model.AgentType {
	return model.AgentCodex
}

// IsAvailable checks whether the "codex" CLI is installed and in PATH.
func (a *CodexAdapter) IsAvailable() bool {
	_, err := exec.LookPath("codex")
	return err == nil
}

// Launch starts a new Codex CLI session with the given prompt and returns a
// RunningSession that streams output line by line.
func (a *CodexAdapter) Launch(ctx context.Context, opts LaunchOptions) (*RunningSession, error) {
	args := []string{"exec", opts.Prompt}

	args = append(args, opts.ExtraArgs...)

	cmdCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(cmdCtx, "codex", args...)
	cmd.Dir = opts.RepoPath

	if len(opts.Env) > 0 {
		cmd.Env = append(cmd.Environ(), opts.Env...)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("codex stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("codex stderr pipe: %w", err)
	}

	output := make(chan OutputLine, 200)
	done := make(chan error, 1)

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("codex start: %w", err)
	}

	rs := &RunningSession{
		PID:       cmd.Process.Pid,
		SessionID: opts.SessionID,
		Output:    output,
		Done:      done,
		Cmd:       cmd,
		SendInput: nil, // Codex CLI exec mode does not support interactive stdin.
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

// SendMessage is not supported for Codex CLI.
// Codex exec mode runs a single prompt to completion without session support.
func (a *CodexAdapter) SendMessage(_ context.Context, _ *RunningSession, _ string) error {
	return fmt.Errorf("codex adapter: SendMessage is not supported; codex exec runs a single prompt to completion")
}

// Stop gracefully terminates a running Codex session. It sends SIGTERM first,
// then SIGKILL after a 5-second grace period if the process has not exited.
func (a *CodexAdapter) Stop(_ context.Context, session *RunningSession) error {
	return stopProcess(session.Cmd, 5*time.Second)
}
