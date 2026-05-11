package jarvis

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// BuildSystemPrompt tests
// ---------------------------------------------------------------------------

func TestBuildSystemPrompt_Concise(t *testing.T) {
	t.Parallel()

	ctx := "### Sessions\n2 active sessions\n- maya-web (PID 123): running, 5m"
	prompt := BuildSystemPrompt(ctx, VerbosityConcise)

	// Should contain the concise verbosity instruction.
	if !strings.Contains(strings.ToLower(prompt), "one to two sentence") {
		t.Error("concise prompt should mention 1-2 sentence responses")
	}

	// Should include the environment context.
	if !strings.Contains(prompt, "maya-web") {
		t.Error("prompt should include the provided context data")
	}
	if !strings.Contains(prompt, "PID 123") {
		t.Error("prompt should include session details from context")
	}

	// Should contain Jarvis identity.
	if !strings.Contains(prompt, "Jarvis") {
		t.Error("prompt should mention Jarvis's identity")
	}
}

func TestBuildSystemPrompt_Detailed(t *testing.T) {
	t.Parallel()

	ctx := "### Sessions\n1 active session"
	prompt := BuildSystemPrompt(ctx, VerbosityDetailed)

	// Should contain the detailed verbosity instruction.
	if !strings.Contains(strings.ToLower(prompt), "three to four sentence") {
		t.Error("detailed prompt should mention three to four sentence responses")
	}

	// Should still contain the context.
	if !strings.Contains(prompt, "1 active session") {
		t.Error("prompt should include the provided context data")
	}
}

func TestBuildSystemPrompt_EmptyVerbosity(t *testing.T) {
	t.Parallel()

	// Empty verbosity should default to concise.
	prompt := BuildSystemPrompt("some context", "")

	if !strings.Contains(strings.ToLower(prompt), "one to two sentence") {
		t.Error("empty verbosity should default to concise (1-2 sentence)")
	}
}

func TestBuildSystemPrompt_EmptyContext(t *testing.T) {
	t.Parallel()

	prompt := BuildSystemPrompt("", VerbosityConcise)

	// Should instruct Jarvis to acknowledge limited visibility.
	if !strings.Contains(prompt, "No session data") {
		t.Error("empty context should include 'No session data' instruction")
	}

	// Should not contain the "current state" wrapper that non-empty context gets.
	if strings.Contains(prompt, "Here is the current state") {
		t.Error("empty context should not contain 'Here is the current state'")
	}

	// Should still contain Jarvis identity and rules.
	if !strings.Contains(prompt, "Jarvis") {
		t.Error("prompt should always contain Jarvis identity")
	}
	if !strings.Contains(prompt, "[ACTION]") {
		t.Error("prompt should always contain ACTION block format")
	}
}

func TestBuildSystemPrompt_ActionFormat(t *testing.T) {
	t.Parallel()

	prompt := BuildSystemPrompt("some context", VerbosityConcise)

	// Must mention the [ACTION] block format for command handling.
	if !strings.Contains(prompt, "[ACTION]") {
		t.Error("prompt should describe the [ACTION] block format")
	}
	if !strings.Contains(prompt, "[/ACTION]") {
		t.Error("prompt should describe the [/ACTION] closing tag")
	}

	// Should list the supported action types.
	actions := []string{
		"resume_session",
		"stop_session",
		"launch_session",
		"respond_approval",
		"focus_session",
		"broadcast",
	}
	for _, action := range actions {
		if !strings.Contains(prompt, action) {
			t.Errorf("prompt should mention action type %q", action)
		}
	}
}

func TestBuildSystemPrompt_GreetingBehaviour(t *testing.T) {
	t.Parallel()

	prompt := BuildSystemPrompt("", VerbosityConcise)

	// The prompt should contain greeting behaviour instructions.
	if !strings.Contains(strings.ToLower(prompt), "greeting") {
		t.Error("prompt should contain greeting behaviour section")
	}
}

func TestBuildSystemPrompt_WithRichContext(t *testing.T) {
	t.Parallel()

	ctx := `### Sessions
3 active sessions
- maya-web (PID 123): running, 10m
- auth-service (PID 456): running, 5m
- cosmos (PID 789): waiting for input, 30m

### Tasks
12 total, 3 running, 1 needs-input

### Approvals
2 approvals waiting
- PID 123 (maya-web): Allow edit?
- PID 789 (cosmos): Allow bash?

### Cost
Today: $1.23, This month: $45.67, All time: $123.45`

	prompt := BuildSystemPrompt(ctx, VerbosityConcise)

	// Should wrap context with the "current state" heading.
	if !strings.Contains(prompt, "Here is the current state") {
		t.Error("non-empty context should be introduced with 'Here is the current state'")
	}

	// All context data should be present verbatim.
	if !strings.Contains(prompt, "maya-web") {
		t.Error("prompt should contain session names from context")
	}
	if !strings.Contains(prompt, "$1.23") {
		t.Error("prompt should contain cost data from context")
	}
	if !strings.Contains(prompt, "2 approvals waiting") {
		t.Error("prompt should contain approval data from context")
	}
}

// ---------------------------------------------------------------------------
// DefaultPersonality tests
// ---------------------------------------------------------------------------

func TestDefaultPersonality(t *testing.T) {
	t.Parallel()

	personality := DefaultPersonality()

	if personality == "" {
		t.Error("DefaultPersonality should return a non-empty string")
	}

	// Should describe Jarvis's personality traits.
	if !strings.Contains(personality, "oncise") {
		t.Error("personality should mention conciseness")
	}
}

// ---------------------------------------------------------------------------
// verbosityBlock tests
// ---------------------------------------------------------------------------

func TestVerbosityBlock_Concise(t *testing.T) {
	t.Parallel()

	block := verbosityBlock(VerbosityConcise)

	if !strings.Contains(strings.ToLower(block), "one to two sentence") {
		t.Error("concise block should mention one to two sentence responses")
	}
}

func TestVerbosityBlock_Detailed(t *testing.T) {
	t.Parallel()

	block := verbosityBlock(VerbosityDetailed)

	if !strings.Contains(strings.ToLower(block), "three to four sentence") {
		t.Error("detailed block should mention three to four sentence responses")
	}
	if !strings.Contains(strings.ToLower(block), "specific") {
		t.Error("detailed block should mention providing specifics")
	}
}

func TestVerbosityBlock_UnknownDefaultsToConcise(t *testing.T) {
	t.Parallel()

	block := verbosityBlock("verbose")

	// Unknown verbosity should fall through to the default (concise).
	if !strings.Contains(strings.ToLower(block), "one to two sentence") {
		t.Error("unknown verbosity should default to concise block")
	}
}

// ---------------------------------------------------------------------------
// buildContextBlock tests
// ---------------------------------------------------------------------------

func TestBuildContextBlock_Empty(t *testing.T) {
	t.Parallel()

	block := buildContextBlock("")

	if !strings.Contains(block, "No session data") {
		t.Error("empty context block should mention no session data available")
	}
	if !strings.Contains(block, "do not have visibility") {
		t.Error("empty context block should instruct Jarvis to acknowledge limited visibility")
	}
}

func TestBuildContextBlock_WithData(t *testing.T) {
	t.Parallel()

	block := buildContextBlock("3 sessions running, all green")

	if !strings.Contains(block, "Here is the current state") {
		t.Error("non-empty context block should introduce with 'Here is the current state'")
	}
	if !strings.Contains(block, "3 sessions running, all green") {
		t.Error("context block should contain the provided data verbatim")
	}
	if !strings.Contains(block, "Do not reference data outside") {
		t.Error("context block should instruct Jarvis not to fabricate data")
	}
}
