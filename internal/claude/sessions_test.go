package claude

import "testing"

// ---------------------------------------------------------------------------
// IsLikelyApprovalPrompt tests (TASK-003 false-positive fix)
// ---------------------------------------------------------------------------

func TestIsLikelyApprovalPrompt_RealApproval(t *testing.T) {
	t.Parallel()

	output := "Some output\nAllow tool use: Edit file src/main.go? (y/n)"
	if !IsLikelyApprovalPrompt(output) {
		t.Error("should detect real approval prompt")
	}
}

func TestIsLikelyApprovalPrompt_IdlePrompt(t *testing.T) {
	t.Parallel()

	// Terminal showing the idle Claude Code prompt — must NOT be flagged.
	output := "Task completed.\n\n? for shortcuts  esc to interrupt"
	if IsLikelyApprovalPrompt(output) {
		t.Error("should NOT flag idle prompt as approval")
	}
}

func TestIsLikelyApprovalPrompt_ActivelyWorking(t *testing.T) {
	t.Parallel()

	// Terminal showing active work with no approval indicators.
	output := "Reading file src/main.go\nAnalyzing code...\nFound 3 issues"
	if IsLikelyApprovalPrompt(output) {
		t.Error("should NOT flag active work as approval")
	}
}

func TestIsLikelyApprovalPrompt_EmptyOutput(t *testing.T) {
	t.Parallel()

	if IsLikelyApprovalPrompt("") {
		t.Error("empty output should not be approval")
	}
}

// ---------------------------------------------------------------------------
// Edge-case / table-driven tests
// ---------------------------------------------------------------------------

func TestIsLikelyApprovalPrompt_Variants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		output string
		want   bool
	}{
		{
			name:   "yes/no dialog",
			output: "Do you want to run this command? (yes/no)",
			want:   true,
		},
		{
			name:   "tool use confirmation",
			output: "Claude wants to execute command: rm -rf /tmp/build\nAllow this action?",
			want:   true,
		},
		{
			name:   "idle with type a message",
			output: "Ready.\nType a message to continue",
			want:   false,
		},
		{
			name:   "idle with help shortcut",
			output: "Done.\n? for help",
			want:   false,
		},
		{
			name:   "watching for changes",
			output: "Watching for file changes...",
			want:   false,
		},
		{
			name:   "whitespace only",
			output: "   \n\n   \n  ",
			want:   false,
		},
		{
			name:   "approval pattern overshadowed by idle pattern",
			output: "Allow edit?\n? for shortcuts  esc to interrupt",
			want:   false, // idle pattern takes precedence
		},
		{
			name:   "Y/n prompt",
			output: "Proceed with installation? (Y/n)",
			want:   true,
		},
		{
			name:   "grant access prompt",
			output: "The tool needs to grant access to /etc/hosts",
			want:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := IsLikelyApprovalPrompt(tt.output)
			if got != tt.want {
				t.Errorf("IsLikelyApprovalPrompt(%q) = %v, want %v", tt.output, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// lastNonEmptyLines helper tests
// ---------------------------------------------------------------------------

func TestLastNonEmptyLines(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		text  string
		n     int
		count int // expected number of returned lines
	}{
		{
			name:  "fewer lines than n",
			text:  "hello\nworld",
			n:     5,
			count: 2,
		},
		{
			name:  "blank lines skipped",
			text:  "a\n\n\nb\n\nc",
			n:     5,
			count: 3,
		},
		{
			name:  "empty string",
			text:  "",
			n:     5,
			count: 0,
		},
		{
			name:  "only blank lines",
			text:  "\n\n\n",
			n:     5,
			count: 0,
		},
		{
			name:  "truncates to n",
			text:  "a\nb\nc\nd\ne\nf",
			n:     3,
			count: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := lastNonEmptyLines(tt.text, tt.n)
			if len(got) != tt.count {
				t.Errorf("lastNonEmptyLines(%q, %d) returned %d lines, want %d", tt.text, tt.n, len(got), tt.count)
			}
		})
	}
}
