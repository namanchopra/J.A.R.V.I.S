package jarvis

import (
	"os/exec"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// parseAction tests
// ---------------------------------------------------------------------------

func TestParseAction_ValidResume(t *testing.T) {
	t.Parallel()

	input := `Sure, resuming that session now.
[ACTION]{"action":"resume_session","sessionId":"abc-123"}[/ACTION]`

	action, cleaned := parseAction(input)

	if action == nil {
		t.Fatal("expected non-nil action")
	}
	if action.Action != ActionResumeSession {
		t.Errorf("action = %q, want %q", action.Action, ActionResumeSession)
	}
	if action.SessionID != "abc-123" {
		t.Errorf("sessionId = %q, want %q", action.SessionID, "abc-123")
	}

	// The [ACTION] block should be stripped from the cleaned text.
	if cleaned != "Sure, resuming that session now.\n" {
		t.Errorf("cleaned text = %q, want text without ACTION block", cleaned)
	}
}

func TestParseAction_ValidBroadcast(t *testing.T) {
	t.Parallel()

	input := `Broadcasting git status to all sessions.
[ACTION]{"action":"broadcast","command":"git status"}[/ACTION]`

	action, cleaned := parseAction(input)

	if action == nil {
		t.Fatal("expected non-nil action")
	}
	if action.Action != ActionBroadcast {
		t.Errorf("action = %q, want %q", action.Action, ActionBroadcast)
	}
	if action.Command != "git status" {
		t.Errorf("command = %q, want %q", action.Command, "git status")
	}
	if cleaned != "Broadcasting git status to all sessions.\n" {
		t.Errorf("cleaned text = %q, want text without ACTION block", cleaned)
	}
}

func TestParseAction_ValidLaunchSession(t *testing.T) {
	t.Parallel()

	input := `Launching new session for maya-web.
[ACTION]{"action":"launch_session","project":"/repos/maya-web","prompt":"fix auth bug"}[/ACTION]`

	action, cleaned := parseAction(input)

	if action == nil {
		t.Fatal("expected non-nil action")
	}
	if action.Action != ActionLaunchSession {
		t.Errorf("action = %q, want %q", action.Action, ActionLaunchSession)
	}
	if action.Project != "/repos/maya-web" {
		t.Errorf("project = %q, want %q", action.Project, "/repos/maya-web")
	}
	if action.Prompt != "fix auth bug" {
		t.Errorf("prompt = %q, want %q", action.Prompt, "fix auth bug")
	}
	if cleaned != "Launching new session for maya-web.\n" {
		t.Errorf("cleaned text = %q, want text without ACTION block", cleaned)
	}
}

func TestParseAction_ValidRespondApproval(t *testing.T) {
	t.Parallel()

	input := `Approving that request.
[ACTION]{"action":"respond_approval","pid":12345,"response":"approve"}[/ACTION]`

	action, cleaned := parseAction(input)

	if action == nil {
		t.Fatal("expected non-nil action")
	}
	if action.Action != ActionRespondApproval {
		t.Errorf("action = %q, want %q", action.Action, ActionRespondApproval)
	}
	if action.PID != 12345 {
		t.Errorf("pid = %d, want %d", action.PID, 12345)
	}
	if action.Response != "approve" {
		t.Errorf("response = %q, want %q", action.Response, "approve")
	}
	if cleaned != "Approving that request.\n" {
		t.Errorf("cleaned text = %q, want text without ACTION block", cleaned)
	}
}

func TestParseAction_NoAction(t *testing.T) {
	t.Parallel()

	input := "Everything looks good. Three sessions running, no issues."

	action, cleaned := parseAction(input)

	if action != nil {
		t.Errorf("expected nil action, got %+v", action)
	}
	if cleaned != input {
		t.Errorf("cleaned text should be unchanged, got %q", cleaned)
	}
}

func TestParseAction_MalformedJSON(t *testing.T) {
	t.Parallel()

	input := `Trying something.
[ACTION]{this is not valid json}[/ACTION]`

	action, cleaned := parseAction(input)

	// Malformed JSON should gracefully return nil action and original text.
	if action != nil {
		t.Errorf("expected nil action for malformed JSON, got %+v", action)
	}
	if cleaned != input {
		t.Errorf("cleaned text should be unchanged for malformed JSON, got %q", cleaned)
	}
}

func TestParseAction_EmptyBlock(t *testing.T) {
	t.Parallel()

	input := `Here is the status.
[ACTION][/ACTION]`

	action, cleaned := parseAction(input)

	// Empty block has no valid JSON, should return nil action.
	if action != nil {
		t.Errorf("expected nil action for empty block, got %+v", action)
	}
	if cleaned != input {
		t.Errorf("cleaned text should be unchanged for empty block, got %q", cleaned)
	}
}

func TestParseAction_MultipleBlocks(t *testing.T) {
	t.Parallel()

	input := `Resuming first, then stopping second.
[ACTION]{"action":"resume_session","sessionId":"first"}[/ACTION]
Also doing this.
[ACTION]{"action":"stop_session","sessionId":"second"}[/ACTION]`

	action, cleaned := parseAction(input)

	// Only the first [ACTION] block should be parsed.
	if action == nil {
		t.Fatal("expected non-nil action")
	}
	if action.Action != ActionResumeSession {
		t.Errorf("action = %q, want %q (first block)", action.Action, ActionResumeSession)
	}
	if action.SessionID != "first" {
		t.Errorf("sessionId = %q, want %q", action.SessionID, "first")
	}

	// Both ACTION blocks should be stripped from cleaned text.
	if action.Action == ActionResumeSession && action.SessionID == "first" {
		// Verify the second block was also stripped by ReplaceAll.
		// The regex replaces all matches.
		if len(cleaned) >= len(input) {
			t.Error("expected cleaned text to be shorter than input (blocks stripped)")
		}
	}
}

// ---------------------------------------------------------------------------
// NewChatClient tests
// ---------------------------------------------------------------------------

func TestNewChatClient_EmptyAPIKey(t *testing.T) {
	t.Parallel()

	// Force API mode so we can test the "no API key" error path
	// (NewChatClient auto-detects CLI when claude is in PATH).
	client := NewChatClientWithProvider(ChatProviderAPI, "")

	// Chat should return a descriptive error when API key is empty,
	// without making any network call.
	_, err := client.Chat(nil, "system prompt", []Message{
		{Role: "user", Content: "hello"},
	})
	if err == nil {
		t.Fatal("expected error for empty API key")
	}
	if err.Error() == "" {
		t.Error("expected descriptive error message, got empty string")
	}

	// The error message should mention the API key being missing.
	errMsg := err.Error()
	if !(strings.Contains(errMsg, "API key") || strings.Contains(errMsg, "api key") || strings.Contains(errMsg, "configured")) {
		t.Errorf("error should mention API key, got: %q", errMsg)
	}
}

func TestNewChatClient_AutoDetectsCLI(t *testing.T) {
	t.Parallel()

	// Skip if claude CLI is not in PATH (e.g. CI runners).
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude CLI not in PATH")
	}

	// When no API key is provided and claude is in PATH, should auto-select CLI.
	client := NewChatClient("")
	if client.provider != ChatProviderCLI {
		t.Errorf("expected CLI provider when claude is in PATH, got %q", client.provider)
	}
}

func TestNewChatClient_Defaults(t *testing.T) {
	t.Parallel()

	client := NewChatClient("sk-test-key-123")

	if client.apiKey != "sk-test-key-123" {
		t.Errorf("apiKey = %q, want %q", client.apiKey, "sk-test-key-123")
	}
	if client.model != chatDefaultModel {
		t.Errorf("model = %v, want %v", client.model, chatDefaultModel)
	}
	if client.maxTokens != chatDefaultMaxTokens {
		t.Errorf("maxTokens = %d, want %d", client.maxTokens, chatDefaultMaxTokens)
	}
}

// ---------------------------------------------------------------------------
// toAPIMessages tests
// ---------------------------------------------------------------------------

func TestToAPIMessages_FiltersSystemRole(t *testing.T) {
	t.Parallel()

	msgs := []Message{
		{Role: "system", Content: "You are Jarvis."},
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there."},
		{Role: "unknown", Content: "ignored"},
	}

	result := toAPIMessages(msgs)

	// Only user and assistant messages should be converted.
	if len(result) != 2 {
		t.Errorf("expected 2 messages, got %d", len(result))
	}
}

func TestToAPIMessages_Empty(t *testing.T) {
	t.Parallel()

	result := toAPIMessages([]Message{})

	if len(result) != 0 {
		t.Errorf("expected 0 messages, got %d", len(result))
	}
}
