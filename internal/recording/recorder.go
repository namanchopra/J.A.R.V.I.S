package recording

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/namanchopra/jarvis/internal/paths"
)

// Snapshot captures the state of a Claude session at a single point in time.
type Snapshot struct {
	Timestamp    time.Time `json:"timestamp"`
	PID          int       `json:"pid"`
	SessionID    string    `json:"sessionId"`
	CWD          string    `json:"cwd"`
	TerminalText string    `json:"terminalText"` // last 50 lines
	Activity     string    `json:"activity"`     // typing, tool_use, waiting, idle
	ToolCalls    []string  `json:"toolCalls"`    // tool names detected in output
}

// RecordingSummary provides metadata about a recorded session without loading
// every snapshot into memory.
type RecordingSummary struct {
	SessionID     string    `json:"sessionId"`
	Name          string    `json:"name"`
	CWD           string    `json:"cwd"`
	StartedAt     time.Time `json:"startedAt"`
	SnapshotCount int       `json:"snapshotCount"`
	FilePath      string    `json:"filePath"`
}

// recordingsDir returns the directory where JSONL recording files are stored.
func recordingsDir() (string, error) {
	return paths.RecordingsDir(), nil
}

// RecordSnapshot appends a single JSON-encoded snapshot followed by a newline
// to ~/.jarvis/recordings/<session-id>.jsonl. The directory and file are created
// if they do not already exist.
func RecordSnapshot(snap Snapshot) error {
	if strings.ContainsAny(snap.SessionID, "/\\") || snap.SessionID != filepath.Base(snap.SessionID) {
		return fmt.Errorf("invalid session ID: %q", snap.SessionID)
	}

	dir, err := recordingsDir()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create recordings dir: %w", err)
	}

	path := filepath.Join(dir, snap.SessionID+".jsonl")

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open recording file: %w", err)
	}
	defer f.Close()

	data, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}

	// Write JSON line atomically (single write call).
	data = append(data, '\n')
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write snapshot: %w", err)
	}

	return nil
}

// ListRecordings scans ~/.jarvis/recordings/*.jsonl and returns a summary for
// each recorded session. Malformed files are logged and skipped rather than
// causing the entire listing to fail.
func ListRecordings() ([]RecordingSummary, error) {
	dir, err := recordingsDir()
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []RecordingSummary{}, nil
		}
		return nil, fmt.Errorf("read recordings dir: %w", err)
	}

	var summaries []RecordingSummary
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		summary, ok := buildSummary(path)
		if !ok {
			continue
		}
		summaries = append(summaries, summary)
	}

	if summaries == nil {
		summaries = []RecordingSummary{}
	}
	return summaries, nil
}

// buildSummary reads the first line of a JSONL file for metadata and counts
// total lines for the snapshot count. Returns false if the file cannot be
// parsed.
func buildSummary(path string) (RecordingSummary, bool) {
	f, err := os.Open(path)
	if err != nil {
		slog.Warn("skip recording file: open failed", "path", path, "err", err)
		return RecordingSummary{}, false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)

	// Read the first line to extract session metadata.
	if !scanner.Scan() {
		slog.Warn("skip recording file: empty", "path", path)
		return RecordingSummary{}, false
	}

	var first Snapshot
	if err := json.Unmarshal(scanner.Bytes(), &first); err != nil {
		slog.Warn("skip recording file: malformed first line", "path", path, "err", err)
		return RecordingSummary{}, false
	}

	// Count remaining lines (first line already consumed).
	count := 1
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			count++
		}
	}

	sessionID := strings.TrimSuffix(filepath.Base(path), ".jsonl")

	return RecordingSummary{
		SessionID:     sessionID,
		Name:          nameFromCWD(first.CWD),
		CWD:           first.CWD,
		StartedAt:     first.Timestamp,
		SnapshotCount: count,
		FilePath:      path,
	}, true
}

// nameFromCWD derives a human-readable recording name from a working
// directory path, mirroring the session DisplayName convention.
func nameFromCWD(cwd string) string {
	base := filepath.Base(cwd)
	if base == "" || base == "." || base == "/" {
		return "recording"
	}
	return base
}

// GetRecording reads every snapshot from a session's JSONL recording file.
// Malformed lines are logged and skipped so a single corrupt entry does not
// prevent the rest of the recording from loading.
func GetRecording(sessionID string) ([]Snapshot, error) {
	if strings.ContainsAny(sessionID, "/\\") || sessionID != filepath.Base(sessionID) {
		return nil, fmt.Errorf("invalid session ID: %q", sessionID)
	}

	dir, err := recordingsDir()
	if err != nil {
		return nil, err
	}

	path := filepath.Join(dir, sessionID+".jsonl")

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("recording not found for session %s", sessionID)
		}
		return nil, fmt.Errorf("open recording file: %w", err)
	}
	defer f.Close()

	var snapshots []Snapshot
	scanner := bufio.NewScanner(f)

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var snap Snapshot
		if err := json.Unmarshal([]byte(line), &snap); err != nil {
			slog.Warn("skip malformed snapshot line",
				"session_id", sessionID,
				"line", lineNum,
				"err", err,
			)
			continue
		}
		snapshots = append(snapshots, snap)
	}

	if err := scanner.Err(); err != nil {
		return snapshots, fmt.Errorf("read recording file: %w", err)
	}

	if snapshots == nil {
		snapshots = []Snapshot{}
	}
	return snapshots, nil
}
