package jarvis

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"strings"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// ChatProvider selects how Jarvis talks to Claude.
type ChatProvider string

const (
	// ChatProviderCLI uses the local `claude` CLI with the user's existing
	// Claude Code subscription. No API key needed.
	ChatProviderCLI ChatProvider = "cli"

	// ChatProviderAPI uses the Anthropic Messages API directly. Requires an
	// API key from console.anthropic.com.
	ChatProviderAPI ChatProvider = "api"
)

// actionBlockRe matches [ACTION]{...}[/ACTION] blocks embedded in Claude
// responses. The inner JSON is captured in group 1. The regex uses a non-greedy
// match so that only the first well-formed block is extracted.
var actionBlockRe = regexp.MustCompile(`(?s)\[ACTION\](.*?)\[/ACTION\]`)

// chatDefaultModel is the Claude model used for Jarvis conversations. Sonnet
// provides a good balance of speed and quality for a voice companion.
const chatDefaultModel = anthropic.ModelClaudeSonnet4_5

// chatDefaultMaxTokens caps the API response length. Jarvis responses are
// typically 1-2 sentences, so 1024 tokens is generous without being wasteful.
// Note: this is distinct from defaultMaxTokens in conversation.go, which
// governs the conversation window's character-based token budget.
const chatDefaultMaxTokens int64 = 1024

// ChatClient wraps Claude access for sending conversational requests and
// parsing structured action blocks from responses. Supports two providers:
// "cli" (uses the local `claude` binary with existing subscription) or
// "api" (uses the Anthropic Messages API with an API key).
type ChatClient struct {
	provider  ChatProvider
	apiKey    string
	model     anthropic.Model
	maxTokens int64
}

// NewChatClient creates a ChatClient. If apiKey is empty, it auto-detects:
// if the `claude` CLI is in PATH, it uses ChatProviderCLI (no key needed).
// Otherwise it falls back to ChatProviderAPI (which will error on Chat if
// still no key).
func NewChatClient(apiKey string) *ChatClient {
	provider := ChatProviderAPI
	if apiKey == "" {
		if _, err := exec.LookPath("claude"); err == nil {
			provider = ChatProviderCLI
			slog.Info("jarvis: using Claude CLI (existing subscription, no API key needed)")
		}
	}
	return &ChatClient{
		provider:  provider,
		apiKey:    apiKey,
		model:     chatDefaultModel,
		maxTokens: chatDefaultMaxTokens,
	}
}

// NewChatClientWithProvider creates a ChatClient with an explicit provider.
func NewChatClientWithProvider(provider ChatProvider, apiKey string) *ChatClient {
	return &ChatClient{
		provider:  provider,
		apiKey:    apiKey,
		model:     chatDefaultModel,
		maxTokens: chatDefaultMaxTokens,
	}
}

// Response holds the parsed result of a Claude API call.
type Response struct {
	// Text is the human-readable response with any [ACTION] block stripped.
	Text string

	// Action is the parsed command from an [ACTION] block, or nil if the
	// response contained no action.
	Action *ActionCommand

	// RawText is the full unmodified text returned by Claude.
	RawText string
}

// ActionCommand represents a structured command embedded in Jarvis's response.
// The command router uses this to execute operations like resuming sessions
// or responding to approvals.
type ActionCommand struct {
	Action     string `json:"action"`
	SessionID  string `json:"sessionId,omitempty"`
	PID        int    `json:"pid,omitempty"`
	Response   string `json:"response,omitempty"`
	Command    string `json:"command,omitempty"`
	Project    string `json:"project,omitempty"`
	Prompt     string `json:"prompt,omitempty"`
	ApprovalID string `json:"approvalId,omitempty"`
}

// Chat sends a conversational request to Claude and returns a parsed Response.
// Routes to either the CLI or API provider based on configuration.
func (c *ChatClient) Chat(ctx context.Context, systemPrompt string, messages []Message) (Response, error) {
	switch c.provider {
	case ChatProviderCLI:
		return c.chatViaCLI(ctx, systemPrompt, messages)
	case ChatProviderAPI:
		return c.chatViaAPI(ctx, systemPrompt, messages)
	default:
		return Response{}, fmt.Errorf("unknown chat provider: %q", c.provider)
	}
}

// chatViaCLI shells out to the local `claude` binary with --print mode.
// Uses the user's existing Claude Code subscription — no API key needed.
func (c *ChatClient) chatViaCLI(ctx context.Context, systemPrompt string, messages []Message) (Response, error) {
	claudePath, err := exec.LookPath("claude")
	if err != nil {
		return Response{}, fmt.Errorf("claude CLI not found in PATH — install Claude Code or set an API key")
	}

	// Build user prompt from conversation history. Only include the last few
	// turns to keep it fast — the system prompt has the full context already.
	var userPrompt strings.Builder
	for _, m := range messages {
		switch m.Role {
		case "user":
			userPrompt.WriteString("User: ")
			userPrompt.WriteString(m.Content)
			userPrompt.WriteString("\n")
		case "assistant":
			userPrompt.WriteString("Jarvis: ")
			userPrompt.WriteString(m.Content)
			userPrompt.WriteString("\n")
		}
	}

	// Remind Claude to use ACTION blocks — CLI mode sometimes ignores the
	// system prompt's structured output instructions.
	userPrompt.WriteString("\nRemember: if performing any action, you MUST include [ACTION]{...}[/ACTION] on a new line.\n")

	if ctx == nil {
		ctx = context.Background()
	}

	// Use haiku for speed — Jarvis responses are short and conversational.
	// --verbose=false suppresses Claude Code's startup logging.
	cmd := exec.CommandContext(ctx, claudePath,
		"--print",
		"--output-format", "text",
		"--max-turns", "1",
		"--model", "sonnet",
		"--no-markdown",
		"--system-prompt", systemPrompt,
	)
	// Set HOME so Claude Code finds its config.
	cmd.Env = append(os.Environ(), "DISABLE_AUTOUPDATE=1")
	cmd.Stdin = bytes.NewReader([]byte(userPrompt.String()))

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	slog.Debug("jarvis: running claude CLI", "path", claudePath)

	if err := cmd.Run(); err != nil {
		return Response{}, fmt.Errorf("claude CLI error: %w (stderr: %s)", err, stderr.String())
	}

	rawText := stdout.String()
	action, cleanText := parseAction(rawText)

	// Fallback: if Claude didn't output [ACTION] blocks, try to infer
	// the action from the response text. Haiku often skips structured output.
	if action == nil {
		action = inferAction(cleanText)
	}

	return Response{
		Text:    strings.TrimSpace(cleanText),
		Action:  action,
		RawText: rawText,
	}, nil
}

// chatViaAPI uses the Anthropic Messages API directly. Requires an API key.
func (c *ChatClient) chatViaAPI(ctx context.Context, systemPrompt string, messages []Message) (Response, error) {
	if c.apiKey == "" {
		return Response{}, fmt.Errorf("Claude API key not configured — set it in Jarvis settings, or use CLI mode (install Claude Code)")
	}

	opts := []option.RequestOption{option.WithAPIKey(c.apiKey)}
	// OpenRouter keys start with "sk-or-" — route through their API.
	if strings.HasPrefix(c.apiKey, "sk-or-") {
		opts = append(opts, option.WithBaseURL("https://openrouter.ai/api"))
	}
	client := anthropic.NewClient(opts...)

	apiMessages := toAPIMessages(messages)

	msg, err := client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     c.model,
		MaxTokens: c.maxTokens,
		System: []anthropic.TextBlockParam{
			{Text: systemPrompt},
		},
		Messages: apiMessages,
	})
	if err != nil {
		return Response{}, fmt.Errorf("Claude API error: %w", err)
	}

	rawText := extractText(msg)
	action, cleanText := parseAction(rawText)

	return Response{
		Text:    strings.TrimSpace(cleanText),
		Action:  action,
		RawText: rawText,
	}, nil
}

// ChatStream sends a conversational request to Claude using the streaming API
// and calls onSentence for each complete sentence as it becomes available.
// This enables overlapping LLM generation and TTS — the first sentence can
// play while the rest of the response is still being generated.
//
// Only works with ChatProviderAPI. Returns an error for CLI provider.
// The full Response (with Text, Action, RawText) is returned after the stream
// completes, same as non-streaming Chat.
func (c *ChatClient) ChatStream(ctx context.Context, systemPrompt string, messages []Message, onSentence func(sentence string)) (Response, error) {
	if c.provider != ChatProviderAPI {
		return Response{}, fmt.Errorf("ChatStream: streaming only supported with API provider (current: %q)", c.provider)
	}
	if c.apiKey == "" {
		return Response{}, fmt.Errorf("ChatStream: Claude API key not configured — set it in Jarvis settings")
	}

	opts := []option.RequestOption{option.WithAPIKey(c.apiKey)}
	if strings.HasPrefix(c.apiKey, "sk-or-") {
		opts = append(opts, option.WithBaseURL("https://openrouter.ai/api"))
	}
	client := anthropic.NewClient(opts...)
	apiMessages := toAPIMessages(messages)

	stream := client.Messages.NewStreaming(ctx, anthropic.MessageNewParams{
		Model:     c.model,
		MaxTokens: c.maxTokens,
		System: []anthropic.TextBlockParam{
			{Text: systemPrompt},
		},
		Messages: apiMessages,
	})
	defer stream.Close()

	// Accumulate the full response text and track sentences already dispatched.
	var fullText strings.Builder
	sentencesSent := 0

	for stream.Next() {
		event := stream.Current()

		// We only care about content_block_delta events with text deltas.
		if event.Type != "content_block_delta" {
			continue
		}
		if event.Delta.Type != "text_delta" {
			continue
		}

		delta := event.Delta.Text
		if delta == "" {
			continue
		}

		fullText.WriteString(delta)

		// Check if we have new complete sentences.
		sentences := SplitSentences(fullText.String())
		if len(sentences) <= sentencesSent {
			continue
		}

		// Dispatch any new complete sentences. We hold back the last
		// sentence because it might still be incomplete (no terminal
		// punctuation yet). Only dispatch it if we know the stream is
		// still going and we have a *new* count beyond what we sent.
		//
		// Dispatch all new complete sentences except the last one (which
		// may still be accumulating). The last sentence is flushed after
		// the stream ends.
		for i := sentencesSent; i < len(sentences)-1; i++ {
			if onSentence != nil {
				onSentence(sentences[i])
			}
		}
		sentencesSent = len(sentences) - 1
	}

	if err := stream.Err(); err != nil {
		return Response{}, fmt.Errorf("ChatStream: %w", err)
	}

	// Flush any remaining text as the final sentence.
	rawText := fullText.String()
	sentences := SplitSentences(rawText)
	for i := sentencesSent; i < len(sentences); i++ {
		if onSentence != nil {
			onSentence(sentences[i])
		}
	}

	// If SplitSentences returned nothing but we have text (e.g. no terminal
	// punctuation at all), flush the entire buffer as one sentence.
	if len(sentences) == 0 && rawText != "" {
		trimmed := strings.TrimSpace(rawText)
		if trimmed != "" && onSentence != nil {
			onSentence(trimmed)
		}
	}

	// Parse action blocks from the full response, same as non-streaming.
	action, cleanText := parseAction(rawText)

	return Response{
		Text:    strings.TrimSpace(cleanText),
		Action:  action,
		RawText: rawText,
	}, nil
}

// inferAction tries to detect an actionable intent from Claude's plain text
// response when it doesn't include [ACTION] blocks. Uses simple keyword matching.
// This is the fallback for CLI mode where Claude sometimes omits structured output.
//
// Only matches sentence-initial action confirmations (e.g. "Resuming my-app now.")
// to avoid false positives on conversational text like "I'm showing you how to...".
// Patterns here must stay in sync with the system prompt's phrasing instructions.
func inferAction(text string) *ActionCommand {
	// Only inspect the first sentence — action confirmations always lead.
	// This prevents matching mid-sentence phrases in conversational text.
	firstSentence := strings.ToLower(firstLine(text))

	// Navigate views — matches "Showing you the dashboard." / "Pulling up the sessions."
	for _, view := range []string{"dashboard", "sessions", "tasks", "activity", "workflows", "costs", "settings"} {
		if strings.HasPrefix(firstSentence, "showing you the "+view) ||
			strings.HasPrefix(firstSentence, "navigating to "+view) ||
			strings.HasPrefix(firstSentence, "pulling up the "+view) {
			return &ActionCommand{Action: ActionNavigateView, Command: view}
		}
	}

	// Approve all / deny all
	if strings.HasPrefix(firstSentence, "approving all") || strings.HasPrefix(firstSentence, "approved all") {
		return &ActionCommand{Action: ActionApproveAll}
	}
	if strings.HasPrefix(firstSentence, "denying all") || strings.HasPrefix(firstSentence, "denied all") {
		return &ActionCommand{Action: ActionDenyAll}
	}

	// Approve / deny single — must lead the sentence
	if strings.HasPrefix(firstSentence, "approving") || strings.HasPrefix(firstSentence, "granting approval") {
		return &ActionCommand{Action: ActionRespondApproval, Response: "y"}
	}
	if strings.HasPrefix(firstSentence, "denying") || strings.HasPrefix(firstSentence, "rejecting") {
		return &ActionCommand{Action: ActionRespondApproval, Response: "n"}
	}

	// Session lifecycle — require sentence-initial confirmation phrasing
	if strings.HasPrefix(firstSentence, "launching") || strings.HasPrefix(firstSentence, "spinning up") {
		project := extractSessionRef(firstSentence)
		return &ActionCommand{Action: ActionLaunchSession, Project: project}
	}
	if strings.HasPrefix(firstSentence, "resuming") {
		if id := extractSessionRef(firstSentence); id != "" {
			return &ActionCommand{Action: ActionResumeSession, SessionID: id}
		}
	}
	if strings.HasPrefix(firstSentence, "stopping") || strings.HasPrefix(firstSentence, "killing") {
		if id := extractSessionRef(firstSentence); id != "" {
			return &ActionCommand{Action: ActionStopSession, SessionID: id}
		}
	}
	if strings.HasPrefix(firstSentence, "focusing") {
		if id := extractSessionRef(firstSentence); id != "" {
			return &ActionCommand{Action: ActionFocusSession, SessionID: id}
		}
	}

	// Git operations — sentence-initial only
	if strings.HasPrefix(firstSentence, "pushing") || strings.HasPrefix(firstSentence, "pushed") {
		if project := extractSessionRef(firstSentence); project != "" {
			return &ActionCommand{Action: ActionGitPush, Project: project}
		}
	}
	if strings.HasPrefix(firstSentence, "committing") || strings.HasPrefix(firstSentence, "committed") {
		if project := extractSessionRef(firstSentence); project != "" {
			return &ActionCommand{Action: ActionGitCommit, Project: project}
		}
	}
	if strings.HasPrefix(firstSentence, "staging") || strings.HasPrefix(firstSentence, "staged") {
		if project := extractSessionRef(firstSentence); project != "" {
			return &ActionCommand{Action: ActionGitStage, Project: project}
		}
	}

	// System: open app/URL — sentence-initial "opening"
	if strings.HasPrefix(firstSentence, "opening") {
		if urlRe := regexp.MustCompile(`https?://\S+`); urlRe.MatchString(text) {
			url := urlRe.FindString(text)
			return &ActionCommand{Action: ActionSystemOpenURL, Command: url}
		}
		if appName := extractAppName(firstSentence); appName != "" {
			return &ActionCommand{Action: ActionSystemOpenApp, Command: appName}
		}
	}

	return nil
}

// firstLine returns text up to the first sentence-ending punctuation followed
// by a space, or the full text if no sentence boundary is found. Used by
// inferAction to restrict matching to the action-confirmation sentence.
func firstLine(text string) string {
	for i, ch := range text {
		if ch == '.' || ch == '!' || ch == '?' {
			if i+1 < len(text) && text[i+1] == ' ' {
				return text[:i+1]
			}
		}
	}
	return text
}

// extractAppName pulls a likely app name from text. Only matches when the app
// name directly follows "opening " to avoid false positives.
func extractAppName(lower string) string {
	// Try to extract the word(s) after "opening " as the app name.
	idx := strings.Index(lower, "opening ")
	if idx < 0 {
		return ""
	}
	after := strings.TrimSpace(lower[idx+len("opening "):])
	// Strip trailing punctuation.
	after = strings.TrimRight(after, ".,!?;:")

	// Check against known apps.
	apps := []string{
		"slack", "vs code", "vscode", "terminal", "finder", "safari",
		"chrome", "firefox", "discord", "spotify", "iterm", "xcode",
		"figma", "notion", "obsidian", "arc", "cursor", "warp",
	}
	for _, app := range apps {
		if strings.HasPrefix(after, app) {
			return app
		}
	}
	return ""
}

// extractSessionRef pulls a project/session name from text. Looks for common
// project name patterns (hyphenated words like "my-app", "service-name").
func extractSessionRef(text string) string {
	// Match hyphenated project names like "my-app", "service-name", "another-app"
	re := regexp.MustCompile(`\b([a-z]+-[a-z]+(?:-[a-z]+)?)\b`)
	matches := re.FindStringSubmatch(text)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}

// toAPIMessages converts the jarvis package's Message slice into the
// anthropic SDK's MessageParam slice. Only "user" and "assistant" roles are
// mapped; system messages are handled separately via the System field in
// MessageNewParams.
func toAPIMessages(msgs []Message) []anthropic.MessageParam {
	out := make([]anthropic.MessageParam, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case "user":
			out = append(out, anthropic.NewUserMessage(
				anthropic.NewTextBlock(m.Content),
			))
		case "assistant":
			out = append(out, anthropic.NewAssistantMessage(
				anthropic.NewTextBlock(m.Content),
			))
		default:
			// System messages are passed via the System param, not here.
			// Skip any unrecognised roles with a warning.
			slog.Warn("skipping message with unrecognised role", "role", m.Role)
		}
	}
	return out
}

// extractText concatenates all text content blocks from a Claude API
// response message into a single string.
func extractText(msg *anthropic.Message) string {
	var sb strings.Builder
	for _, block := range msg.Content {
		if block.Type == "text" {
			sb.WriteString(block.Text)
		}
	}
	return sb.String()
}

// parseAction extracts and parses an [ACTION]{...}[/ACTION] block from the
// response text. It returns the parsed ActionCommand and the text with the
// action block removed. If no action block is found, it returns nil and the
// original text unchanged.
//
// Parse failures are logged as warnings but do not cause an error — the
// response is still usable without the action.
func parseAction(text string) (*ActionCommand, string) {
	matches := actionBlockRe.FindStringSubmatch(text)
	if matches == nil {
		return nil, text
	}

	jsonStr := strings.TrimSpace(matches[1])
	var cmd ActionCommand
	if err := json.Unmarshal([]byte(jsonStr), &cmd); err != nil {
		slog.Warn("failed to parse ACTION block JSON",
			"err", err,
			"raw", jsonStr,
		)
		return nil, text
	}

	cleaned := actionBlockRe.ReplaceAllString(text, "")
	return &cmd, cleaned
}
