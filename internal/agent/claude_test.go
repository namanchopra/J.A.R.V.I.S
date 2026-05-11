package agent

import (
	"context"
	"os/exec"
	"reflect"
	"testing"

	"github.com/namanchopra/jarvis/internal/model"
)

func TestClaudeAdapterName(t *testing.T) {
	t.Parallel()

	adapter := NewClaudeAdapter()
	got := adapter.Name()
	want := model.AgentClaudeCode

	if got != want {
		t.Errorf("Name() = %q, want %q", got, want)
	}
}

func TestClaudeAdapterIsAvailable(t *testing.T) {
	t.Parallel()

	adapter := NewClaudeAdapter()

	// We cannot assert true/false because availability depends on the host.
	// The important thing is that it returns a bool without panicking.
	got := adapter.IsAvailable()
	if got != true && got != false {
		t.Errorf("IsAvailable() returned unexpected value: %v", got)
	}
}

func TestClaudeAdapterBuildArgs(t *testing.T) {
	t.Parallel()

	adapter := NewClaudeAdapter()

	tests := []struct {
		name string
		opts LaunchOptions
		want []string
	}{
		{
			name: "new session with prompt only",
			opts: LaunchOptions{
				Prompt: "implement auth",
			},
			want: []string{"-p", "implement auth", "--output-format", "stream-json"},
		},
		{
			name: "resume session",
			opts: LaunchOptions{
				Prompt:    "continue work",
				SessionID: "sess-abc-123",
			},
			want: []string{"-p", "continue work", "--output-format", "stream-json", "--resume", "sess-abc-123"},
		},
		{
			name: "new session with extra args",
			opts: LaunchOptions{
				Prompt:    "fix bug",
				ExtraArgs: []string{"--verbose", "--max-tokens", "4096"},
			},
			want: []string{"-p", "fix bug", "--output-format", "stream-json", "--verbose", "--max-tokens", "4096"},
		},
		{
			name: "resume session with extra args",
			opts: LaunchOptions{
				Prompt:    "continue",
				SessionID: "sess-xyz",
				ExtraArgs: []string{"--verbose"},
			},
			want: []string{"-p", "continue", "--output-format", "stream-json", "--resume", "sess-xyz", "--verbose"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := adapter.buildArgs(tt.opts)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("buildArgs() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestClaudeStopAlreadyExited(t *testing.T) {
	t.Parallel()

	adapter := NewClaudeAdapter()

	// Start a short-lived process that exits immediately.
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start test process: %v", err)
	}

	// Wait for the process to finish so it is in an exited state.
	if err := cmd.Wait(); err != nil {
		t.Fatalf("test process exited with error: %v", err)
	}

	session := &RunningSession{
		PID: cmd.Process.Pid,
		Cmd: cmd,
	}

	// Calling Stop on an already-exited process should not return an error.
	err := adapter.Stop(context.Background(), session)
	if err != nil {
		t.Errorf("Stop() on already-exited process returned error: %v", err)
	}
}

func TestClaudeStopNilCmd(t *testing.T) {
	t.Parallel()

	adapter := NewClaudeAdapter()

	// Stop with nil Cmd should be a no-op.
	session := &RunningSession{Cmd: nil}
	err := adapter.Stop(context.Background(), session)
	if err != nil {
		t.Errorf("Stop() with nil Cmd returned error: %v", err)
	}
}
