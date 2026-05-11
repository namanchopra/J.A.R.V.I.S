package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"syscall"
	"time"

	"github.com/namanchopra/jarvis/internal/model"
)

// ClaudeAdapter implements AgentAdapter for the Claude Code CLI.
type ClaudeAdapter struct{}

// NewClaudeAdapter returns a new ClaudeAdapter.
func NewClaudeAdapter() *ClaudeAdapter { return &ClaudeAdapter{} }

// Name returns the agent type this adapter handles.
func (a *ClaudeAdapter) Name() model.AgentType {
	return model.AgentClaudeCode
}

// IsAvailable checks whether the "claude" CLI is installed and in PATH.
func (a *ClaudeAdapter) IsAvailable() bool {
	_, err := exec.LookPath("claude")
	return err == nil
}

// claudeStreamMessage represents a single JSON object emitted by
// `claude --output-format stream-json`. The format emits several message types;
// the two we care about are:
//
//   - {"type":"assistant","content":[{"type":"text","text":"..."}]}
//   - {"type":"result","session_id":"...","result":"..."}
type claudeStreamMessage struct {
	Type      string               `json:"type"`
	Content   []claudeContentBlock `json:"content,omitempty"`
	SessionID string               `json:"session_id,omitempty"`
	Result    string               `json:"result,omitempty"`

	// Some stream events carry a single content_block instead of an array.
	// We handle both shapes.
	Subtype string `json:"subtype,omitempty"`
	Text    string `json:"text,omitempty"`
}

type claudeContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// buildArgs constructs the CLI arguments for a Claude Code invocation based on
// the provided launch options.
func (a *ClaudeAdapter) buildArgs(opts LaunchOptions) []string {
	args := []string{"-p", opts.Prompt, "--output-format", "stream-json"}

	if opts.SessionID != "" {
		args = append(args, "--resume", opts.SessionID)
	}

	args = append(args, opts.ExtraArgs...)
	return args
}

// Launch starts a new Claude Code session (or resumes one) and returns a
// RunningSession that streams output.
func (a *ClaudeAdapter) Launch(ctx context.Context, opts LaunchOptions) (*RunningSession, error) {
	args := a.buildArgs(opts)

	cmdCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(cmdCtx, "claude", args...)
	cmd.Dir = opts.RepoPath

	if len(opts.Env) > 0 {
		cmd.Env = append(cmd.Environ(), opts.Env...)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("claude stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("claude stderr pipe: %w", err)
	}

	output := make(chan OutputLine, 200)
	done := make(chan error, 1)

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("claude start: %w", err)
	}

	rs := &RunningSession{
		PID:       cmd.Process.Pid,
		Output:    output,
		Done:      done,
		Cmd:       cmd,
		SendInput: nil, // Claude Code does not support interactive stdin in -p mode.
		cancel:    cancel,
	}

	// Read stdout — streaming JSON, one JSON object per line.
	go func() {
		scanner := bufio.NewScanner(stdout)
		// Claude Code can produce long lines; increase the buffer.
		scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}

			var msg claudeStreamMessage
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				// Unparseable line — emit as raw text.
				output <- OutputLine{
					Text:      line,
					Timestamp: time.Now(),
				}
				continue
			}

			// Capture session ID from result messages.
			if msg.SessionID != "" {
				rs.SessionID = msg.SessionID
			}

			text := extractText(msg)
			if text == "" {
				continue
			}

			output <- OutputLine{
				Text:      text,
				Timestamp: time.Now(),
			}
		}
	}()

	// Read stderr — emit each line as an error output line.
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

// extractText pulls human-readable text from a Claude stream message.
func extractText(msg claudeStreamMessage) string {
	// "assistant" messages carry content blocks.
	if len(msg.Content) > 0 {
		var combined string
		for _, block := range msg.Content {
			if block.Type == "text" && block.Text != "" {
				if combined != "" {
					combined += "\n"
				}
				combined += block.Text
			}
		}
		return combined
	}

	// Some streaming events carry text at the top level.
	if msg.Text != "" {
		return msg.Text
	}

	// "result" messages may carry a result field.
	if msg.Result != "" {
		return msg.Result
	}

	return ""
}

// SendMessage is not supported for Claude Code in headless mode. Claude Code
// requires a full new process to continue a conversation. Use Launch with
// LaunchOptions.SessionID set to resume instead.
func (a *ClaudeAdapter) SendMessage(_ context.Context, _ *RunningSession, _ string) error {
	return fmt.Errorf("claude code does not support interactive stdin: use Launch with SessionID to resume a session")
}

// Stop gracefully terminates a running Claude Code session. It sends SIGTERM
// first and falls back to SIGKILL after 5 seconds.
func (a *ClaudeAdapter) Stop(_ context.Context, session *RunningSession) error {
	if session.Cmd == nil || session.Cmd.Process == nil {
		return nil
	}

	// Send SIGTERM for graceful shutdown.
	if err := session.Cmd.Process.Signal(syscall.SIGTERM); err != nil {
		// Process may have already exited.
		return nil
	}

	// Wait up to 5 seconds for the process to exit.
	exited := make(chan struct{})
	go func() {
		// Ignore the error — we just need to know when it's done. The actual
		// exit error is delivered via session.Done.
		_, _ = session.Cmd.Process.Wait()
		close(exited)
	}()

	select {
	case <-exited:
		return nil
	case <-time.After(5 * time.Second):
		// Force kill.
		if err := session.Cmd.Process.Signal(syscall.SIGKILL); err != nil {
			return fmt.Errorf("claude kill: %w", err)
		}
		return nil
	}
}
