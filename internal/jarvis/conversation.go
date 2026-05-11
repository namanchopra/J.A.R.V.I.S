package jarvis

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/namanchopra/jarvis/internal/paths"
)

// Default conversation limits.
const (
	defaultMaxTokens      = 8000
	defaultExpiryDuration = 5 * time.Minute // kept for API compatibility

	// saveDebounce is the minimum interval between disk writes.
	saveDebounce = 2 * time.Second
)

// defaultSavePath is where conversation history is persisted to disk
// (~/.jarvis/jarvis-history.json). It is a var (not a const) because it is
// computed from the user's home directory at startup.
var defaultSavePath = paths.DataPath("jarvis-history.json")

// Conversation tracks multi-turn message history with token budget management
// and disk persistence. It is safe for concurrent use by multiple goroutines.
//
// Messages are stored without truncation so the UI always sees full history.
// GetMessagesForLLM returns a token-truncated copy suitable for sending to
// Claude's context window.
type Conversation struct {
	messages       []Message
	maxTokens      int
	expiryDuration time.Duration // retained for API compatibility; not enforced
	lastActivity   time.Time
	mu             sync.RWMutex

	// Persistence fields.
	savePath      string
	lastSaveTime  time.Time
	saveScheduled bool
	saveMu        sync.Mutex // guards debounce state; never held while holding mu
}

// NewConversation creates a Conversation with the given token budget. The
// expiryDuration parameter is accepted for API compatibility but is no longer
// enforced -- conversations persist indefinitely during the app lifetime. Zero
// values are replaced with sensible defaults (8000 tokens).
func NewConversation(maxTokens int, expiryDuration time.Duration) *Conversation {
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}
	if expiryDuration <= 0 {
		expiryDuration = defaultExpiryDuration
	}

	return &Conversation{
		messages:       make([]Message, 0),
		maxTokens:      maxTokens,
		expiryDuration: expiryDuration,
		lastActivity:   time.Now(),
		savePath:       expandPath(defaultSavePath),
	}
}

// AddMessage appends a message to the conversation history and updates the
// last-activity timestamp. The full history is preserved (no truncation on
// store). After appending, the conversation is auto-saved to disk with a
// debounce of 2 seconds to avoid thrashing.
func (c *Conversation) AddMessage(role, content string) {
	c.mu.Lock()
	msg := Message{
		Role:      role,
		Content:   content,
		Timestamp: time.Now(),
	}
	c.messages = append(c.messages, msg)
	c.lastActivity = time.Now()
	c.mu.Unlock()

	c.debounceSave()
}

// GetMessages returns a copy of the full conversation history. The history is
// never truncated or auto-expired; it persists for the entire app lifetime and
// across restarts via disk persistence.
func (c *Conversation) GetMessages() []Message {
	c.mu.RLock()
	defer c.mu.RUnlock()

	out := make([]Message, len(c.messages))
	copy(out, c.messages)
	return out
}

// GetMessagesForLLM returns a token-truncated copy of the conversation history
// suitable for sending to the LLM context window. The oldest non-system
// messages are removed until the estimated token count fits within the
// configured budget. The stored history is never modified.
func (c *Conversation) GetMessagesForLLM() []Message {
	c.mu.RLock()
	// Make a working copy so truncation doesn't touch the stored messages.
	working := make([]Message, len(c.messages))
	copy(working, c.messages)
	maxTokens := c.maxTokens
	c.mu.RUnlock()

	working = truncateMessages(working, maxTokens)
	return working
}

// Reset clears all messages and resets the last-activity timestamp.
func (c *Conversation) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resetLocked()
}

// ClearHistory deletes the persisted history file and resets the in-memory
// conversation. If the file does not exist, no error is returned.
func (c *Conversation) ClearHistory() error {
	c.mu.Lock()
	c.resetLocked()
	savePath := c.savePath
	c.mu.Unlock()

	if savePath == "" {
		return nil
	}

	err := os.Remove(savePath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	slog.Info("conversation history cleared", "path", savePath)
	return nil
}

// IsExpired reports whether the conversation has been inactive for longer than
// its configured expiry duration. Retained for API compatibility.
func (c *Conversation) IsExpired() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.isExpiredLocked()
}

// Len returns the number of messages in the conversation.
func (c *Conversation) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.messages)
}

// SetSavePath overrides the default save path for disk persistence. An empty
// string disables auto-save.
func (c *Conversation) SetSavePath(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.savePath = expandPath(path)
}

// ---------------------------------------------------------------------------
// Persistence
// ---------------------------------------------------------------------------

// SaveToFile serializes the current messages to JSON and writes them to the
// given path. Parent directories are created as needed.
func (c *Conversation) SaveToFile(path string) error {
	c.mu.RLock()
	snapshot := make([]Message, len(c.messages))
	copy(snapshot, c.messages)
	c.mu.RUnlock()

	expanded := expandPath(path)

	dir := filepath.Dir(expanded)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}

	if err := os.WriteFile(expanded, data, 0o644); err != nil {
		return err
	}

	slog.Debug("conversation saved to disk",
		"path", expanded,
		"messages", len(snapshot),
	)
	return nil
}

// LoadFromFile reads messages from a JSON file on disk. If the file does not
// exist, the conversation starts fresh and no error is returned.
func (c *Conversation) LoadFromFile(path string) error {
	expanded := expandPath(path)

	data, err := os.ReadFile(expanded)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			slog.Info("no conversation history file found, starting fresh",
				"path", expanded,
			)
			return nil
		}
		return err
	}

	var msgs []Message
	if err := json.Unmarshal(data, &msgs); err != nil {
		slog.Warn("failed to parse conversation history, starting fresh",
			"path", expanded,
			"err", err,
		)
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.messages = msgs
	if len(msgs) > 0 {
		c.lastActivity = msgs[len(msgs)-1].Timestamp
	}

	slog.Info("conversation history loaded from disk",
		"path", expanded,
		"messages", len(msgs),
	)
	return nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// estimateTokens returns a rough token count for the given messages. The
// approximation uses 1 token per 4 characters, which is good enough for
// sliding-window management without pulling in a real tokenizer.
func estimateTokens(messages []Message) int {
	total := 0
	for _, m := range messages {
		total += len(m.Role) + len(m.Content)
	}
	return total / 4
}

// truncateMessages returns a truncated copy of messages that fits within the
// given token budget. The oldest non-system messages are removed first.
func truncateMessages(msgs []Message, maxTokens int) []Message {
	for estimateTokens(msgs) > maxTokens && len(msgs) > 1 {
		removed := false
		for i, m := range msgs {
			if m.Role == "system" {
				continue
			}
			slog.Debug("conversation truncating oldest message for LLM",
				"index", i,
				"role", m.Role,
				"content_len", len(m.Content),
			)
			msgs = append(msgs[:i], msgs[i+1:]...)
			removed = true
			break
		}
		if !removed {
			break
		}
	}
	return msgs
}

// resetLocked clears messages and refreshes lastActivity. The caller must
// hold c.mu.
func (c *Conversation) resetLocked() {
	c.messages = make([]Message, 0)
	c.lastActivity = time.Now()
}

// isExpiredLocked checks expiry without acquiring the lock. The caller must
// hold at least c.mu.RLock. Retained for API compatibility.
func (c *Conversation) isExpiredLocked() bool {
	return time.Since(c.lastActivity) > c.expiryDuration
}

// debounceSave triggers a disk save, but no more often than every saveDebounce
// interval. If a save was performed recently, a goroutine is scheduled to save
// after the remaining debounce period elapses.
func (c *Conversation) debounceSave() {
	c.saveMu.Lock()
	defer c.saveMu.Unlock()

	c.mu.RLock()
	savePath := c.savePath
	c.mu.RUnlock()

	if savePath == "" {
		return
	}

	now := time.Now()
	if now.Sub(c.lastSaveTime) >= saveDebounce {
		// Enough time has passed -- save immediately.
		c.lastSaveTime = now
		if err := c.SaveToFile(savePath); err != nil {
			slog.Error("failed to auto-save conversation", "err", err, "path", savePath)
		}
		return
	}

	// A save happened recently. Schedule a deferred save if one isn't already
	// pending, so the final message in a burst is always persisted.
	if !c.saveScheduled {
		c.saveScheduled = true
		remaining := saveDebounce - now.Sub(c.lastSaveTime)
		go func() {
			time.Sleep(remaining)
			c.saveMu.Lock()
			c.saveScheduled = false
			c.lastSaveTime = time.Now()
			c.saveMu.Unlock()

			c.mu.RLock()
			sp := c.savePath
			c.mu.RUnlock()

			if sp != "" {
				if err := c.SaveToFile(sp); err != nil {
					slog.Error("failed to deferred-save conversation", "err", err, "path", sp)
				}
			}
		}()
	}
}

// expandPath resolves a leading ~ to the user's home directory.
func expandPath(path string) string {
	if len(path) == 0 {
		return path
	}
	if path[0] == '~' {
		home, err := os.UserHomeDir()
		if err != nil {
			slog.Warn("could not resolve home directory", "err", err)
			return path
		}
		return filepath.Join(home, path[1:])
	}
	return path
}
