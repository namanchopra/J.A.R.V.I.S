package jarvis

import (
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestConversation_AddAndGet(t *testing.T) {
	t.Parallel()

	conv := NewConversation(8000, 5*time.Minute)

	conv.AddMessage("user", "Hello")
	conv.AddMessage("assistant", "Hi there.")
	conv.AddMessage("user", "How are the sessions?")

	msgs := conv.GetMessages()
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}

	// Verify ordering and content.
	if msgs[0].Role != "user" || msgs[0].Content != "Hello" {
		t.Errorf("message 0: got role=%q content=%q, want user/Hello", msgs[0].Role, msgs[0].Content)
	}
	if msgs[1].Role != "assistant" || msgs[1].Content != "Hi there." {
		t.Errorf("message 1: got role=%q content=%q, want assistant/Hi there.", msgs[1].Role, msgs[1].Content)
	}
	if msgs[2].Role != "user" || msgs[2].Content != "How are the sessions?" {
		t.Errorf("message 2: got role=%q content=%q, want user/How are the sessions?", msgs[2].Role, msgs[2].Content)
	}

	// Verify timestamps are set (non-zero).
	for i, m := range msgs {
		if m.Timestamp.IsZero() {
			t.Errorf("message %d has zero timestamp", i)
		}
	}
}

func TestConversation_GetReturnsDefensiveCopy(t *testing.T) {
	t.Parallel()

	conv := NewConversation(8000, 5*time.Minute)
	conv.AddMessage("user", "original")

	msgs := conv.GetMessages()
	msgs[0].Content = "mutated"

	// The mutation should not affect the conversation's internal state.
	fresh := conv.GetMessages()
	if fresh[0].Content != "original" {
		t.Errorf("GetMessages returned a live reference, not a copy: content = %q", fresh[0].Content)
	}
}

func TestConversation_TokenTruncation_ForLLM(t *testing.T) {
	t.Parallel()

	// GetMessages() now returns full untruncated history.
	// GetMessagesForLLM() returns truncated for Claude context window.
	conv := NewConversation(150, 5*time.Minute)

	conv.AddMessage("user", strings.Repeat("a", 396))
	conv.AddMessage("assistant", strings.Repeat("b", 396))
	conv.AddMessage("user", strings.Repeat("c", 396))

	// Full history should have all 3 messages.
	allMsgs := conv.GetMessages()
	if len(allMsgs) != 3 {
		t.Errorf("GetMessages should return all 3 messages, got %d", len(allMsgs))
	}

	// LLM history should be truncated.
	llmMsgs := conv.GetMessagesForLLM()
	if len(llmMsgs) > 2 {
		t.Errorf("GetMessagesForLLM should truncate, got %d messages", len(llmMsgs))
	}

	// Newest message should survive truncation.
	lastMsg := llmMsgs[len(llmMsgs)-1]
	if !strings.Contains(lastMsg.Content, "ccc") {
		t.Errorf("newest message should survive, got content starting with %q", lastMsg.Content[:3])
	}
}

func TestConversation_SystemMessagePreserved(t *testing.T) {
	t.Parallel()

	// Very small budget to force aggressive truncation.
	conv := NewConversation(100, 5*time.Minute)

	// Add a system message first.
	conv.AddMessage("system", "You are Jarvis.")

	// Add enough messages to exceed the budget.
	conv.AddMessage("user", strings.Repeat("x", 396))
	conv.AddMessage("assistant", strings.Repeat("y", 396))

	msgs := conv.GetMessages()

	// System message at index 0 should never be truncated.
	hasSystem := false
	for _, m := range msgs {
		if m.Role == "system" {
			hasSystem = true
			break
		}
	}
	if !hasSystem {
		t.Error("system message was truncated; it should always be preserved")
	}
}

func TestConversation_NeverExpires(t *testing.T) {
	t.Parallel()

	// Conversation no longer auto-expires — messages persist indefinitely.
	conv := NewConversation(8000, 1*time.Millisecond)

	conv.AddMessage("user", "Hello")
	time.Sleep(5 * time.Millisecond)

	// GetMessages should still return the message (no auto-reset).
	msgs := conv.GetMessages()
	if len(msgs) != 1 {
		t.Errorf("expected 1 message (no expiry), got %d", len(msgs))
	}
}

func TestConversation_IsExpired(t *testing.T) {
	t.Parallel()

	conv := NewConversation(8000, 1*time.Millisecond)

	conv.AddMessage("user", "Hello")

	// Wait for expiry.
	time.Sleep(5 * time.Millisecond)

	if !conv.IsExpired() {
		t.Error("expected IsExpired to return true after inactivity exceeds expiry duration")
	}
}

func TestConversation_NotExpiredWhenActive(t *testing.T) {
	t.Parallel()

	conv := NewConversation(8000, 5*time.Minute)

	conv.AddMessage("user", "Hello")

	if conv.IsExpired() {
		t.Error("expected IsExpired to return false when recently active")
	}
}

func TestConversation_ResetClearsHistory(t *testing.T) {
	t.Parallel()

	conv := NewConversation(8000, 5*time.Minute)

	conv.AddMessage("user", "Hello")
	conv.AddMessage("assistant", "Hi.")
	conv.AddMessage("user", "Status?")

	if conv.Len() != 3 {
		t.Fatalf("expected 3 messages before reset, got %d", conv.Len())
	}

	conv.Reset()

	msgs := conv.GetMessages()
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages after reset, got %d", len(msgs))
	}
	if conv.Len() != 0 {
		t.Errorf("expected Len() = 0 after reset, got %d", conv.Len())
	}
}

func TestConversation_LenAccurate(t *testing.T) {
	t.Parallel()

	conv := NewConversation(8000, 5*time.Minute)

	if conv.Len() != 0 {
		t.Errorf("expected Len() = 0 for new conversation, got %d", conv.Len())
	}

	conv.AddMessage("user", "First")
	if conv.Len() != 1 {
		t.Errorf("expected Len() = 1, got %d", conv.Len())
	}

	conv.AddMessage("assistant", "Second")
	if conv.Len() != 2 {
		t.Errorf("expected Len() = 2, got %d", conv.Len())
	}

	conv.AddMessage("user", "Third")
	if conv.Len() != 3 {
		t.Errorf("expected Len() = 3, got %d", conv.Len())
	}
}

func TestConversation_DefaultValues(t *testing.T) {
	t.Parallel()

	// Zero values should be replaced with defaults.
	conv := NewConversation(0, 0)

	if conv.maxTokens != defaultMaxTokens {
		t.Errorf("maxTokens = %d, want default %d", conv.maxTokens, defaultMaxTokens)
	}
	if conv.expiryDuration != defaultExpiryDuration {
		t.Errorf("expiryDuration = %v, want default %v", conv.expiryDuration, defaultExpiryDuration)
	}
}

func TestConversation_NegativeValues(t *testing.T) {
	t.Parallel()

	// Negative values should also be replaced with defaults.
	conv := NewConversation(-100, -1*time.Minute)

	if conv.maxTokens != defaultMaxTokens {
		t.Errorf("maxTokens = %d, want default %d", conv.maxTokens, defaultMaxTokens)
	}
	if conv.expiryDuration != defaultExpiryDuration {
		t.Errorf("expiryDuration = %v, want default %v", conv.expiryDuration, defaultExpiryDuration)
	}
}

// ---------------------------------------------------------------------------
// estimateTokens tests
// ---------------------------------------------------------------------------

func TestEstimateTokens(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		msgs []Message
		want int
	}{
		{
			name: "empty",
			msgs: []Message{},
			want: 0,
		},
		{
			name: "single message",
			msgs: []Message{
				{Role: "user", Content: "hello world"},
			},
			// len("user") + len("hello world") = 4 + 11 = 15 chars / 4 = 3
			want: 3,
		},
		{
			name: "multiple messages",
			msgs: []Message{
				{Role: "user", Content: strings.Repeat("a", 100)},
				{Role: "assistant", Content: strings.Repeat("b", 100)},
			},
			// (4 + 100 + 9 + 100) / 4 = 213 / 4 = 53
			want: 53,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := estimateTokens(tt.msgs)
			if got != tt.want {
				t.Errorf("estimateTokens() = %d, want %d", got, tt.want)
			}
		})
	}
}
