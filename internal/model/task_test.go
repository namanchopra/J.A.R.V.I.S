package model

import (
	"testing"
	"time"
)

func TestNewTask(t *testing.T) {
	t.Parallel()

	before := time.Now()
	task := NewTask("build-feature", "Implement login page", "/repos/myapp", AgentClaudeCode)
	after := time.Now()

	// UUID should be a 36-character string (8-4-4-4-12 hex with dashes).
	if len(task.ID) != 36 {
		t.Errorf("expected UUID of length 36, got %d (%q)", len(task.ID), task.ID)
	}

	if task.Name != "build-feature" {
		t.Errorf("Name = %q, want %q", task.Name, "build-feature")
	}
	if task.Description != "Implement login page" {
		t.Errorf("Description = %q, want %q", task.Description, "Implement login page")
	}
	if task.RepoPath != "/repos/myapp" {
		t.Errorf("RepoPath = %q, want %q", task.RepoPath, "/repos/myapp")
	}
	if task.AgentType != AgentClaudeCode {
		t.Errorf("AgentType = %q, want %q", task.AgentType, AgentClaudeCode)
	}
	if task.Status != StatusPending {
		t.Errorf("Status = %q, want %q", task.Status, StatusPending)
	}
	if task.OutputPath != "" {
		t.Errorf("OutputPath = %q, want empty string", task.OutputPath)
	}

	// Timestamps should be between before and after.
	if task.CreatedAt.Before(before) || task.CreatedAt.After(after) {
		t.Errorf("CreatedAt %v not between %v and %v", task.CreatedAt, before, after)
	}
	if task.UpdatedAt.Before(before) || task.UpdatedAt.After(after) {
		t.Errorf("UpdatedAt %v not between %v and %v", task.UpdatedAt, before, after)
	}

	// CreatedAt and UpdatedAt should be the same on a new task.
	if !task.CreatedAt.Equal(task.UpdatedAt) {
		t.Errorf("CreatedAt (%v) != UpdatedAt (%v) on new task", task.CreatedAt, task.UpdatedAt)
	}
}

func TestValidateStatusTransition(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		from    Status
		to      Status
		wantErr bool
	}{
		{
			name:    "pending to running is allowed",
			from:    StatusPending,
			to:      StatusRunning,
			wantErr: false,
		},
		{
			name:    "running to done is allowed",
			from:    StatusRunning,
			to:      StatusDone,
			wantErr: false,
		},
		{
			name:    "running to failed is allowed",
			from:    StatusRunning,
			to:      StatusFailed,
			wantErr: false,
		},
		{
			name:    "done to pending is forbidden",
			from:    StatusDone,
			to:      StatusPending,
			wantErr: true,
		},
		{
			name:    "done to running is forbidden",
			from:    StatusDone,
			to:      StatusRunning,
			wantErr: true,
		},
		{
			name:    "failed to pending is allowed (retry)",
			from:    StatusFailed,
			to:      StatusPending,
			wantErr: false,
		},
		{
			name:    "failed to running is allowed (retry)",
			from:    StatusFailed,
			to:      StatusRunning,
			wantErr: false,
		},
		{
			name:    "needs-input to running is allowed",
			from:    StatusNeedsInput,
			to:      StatusRunning,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateStatusTransition(tt.from, tt.to)
			if tt.wantErr && err == nil {
				t.Errorf("ValidateStatusTransition(%q, %q) = nil, want error", tt.from, tt.to)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("ValidateStatusTransition(%q, %q) = %v, want nil", tt.from, tt.to, err)
			}
		})
	}
}

func TestValidStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status Status
		want   bool
	}{
		{"pending is valid", StatusPending, true},
		{"running is valid", StatusRunning, true},
		{"done is valid", StatusDone, true},
		{"failed is valid", StatusFailed, true},
		{"needs-input is valid", StatusNeedsInput, true},
		{"empty string is invalid", Status(""), false},
		{"arbitrary string is invalid", Status("bananas"), false},
		{"PENDING (wrong case) is invalid", Status("PENDING"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ValidStatus(tt.status)
			if got != tt.want {
				t.Errorf("ValidStatus(%q) = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}

func TestValidAgentType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		agentType AgentType
		want      bool
	}{
		{"claude-code is valid", AgentClaudeCode, true},
		{"kiro is valid", AgentKiro, true},
		{"gemini is valid", AgentGemini, true},
		{"codex is valid", AgentCodex, true},
		{"aider is valid", AgentAider, true},
		{"other is valid", AgentOther, true},
		{"empty string is invalid", AgentType(""), false},
		{"arbitrary string is invalid", AgentType("chatgpt"), false},
		{"CLAUDE-CODE (wrong case) is invalid", AgentType("CLAUDE-CODE"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ValidAgentType(tt.agentType)
			if got != tt.want {
				t.Errorf("ValidAgentType(%q) = %v, want %v", tt.agentType, got, tt.want)
			}
		})
	}
}

func TestStatusString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status Status
		want   string
	}{
		{StatusPending, "pending"},
		{StatusRunning, "running"},
		{StatusDone, "done"},
		{StatusFailed, "failed"},
		{StatusNeedsInput, "needs-input"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			got := tt.status.String()
			if got != tt.want {
				t.Errorf("Status.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAgentTypeString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		agentType AgentType
		want      string
	}{
		{AgentClaudeCode, "claude-code"},
		{AgentKiro, "kiro"},
		{AgentGemini, "gemini"},
		{AgentCodex, "codex"},
		{AgentAider, "aider"},
		{AgentOther, "other"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			got := tt.agentType.String()
			if got != tt.want {
				t.Errorf("AgentType.String() = %q, want %q", got, tt.want)
			}
		})
	}
}
