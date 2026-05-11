package claude

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/namanchopra/jarvis/internal/model"
)

// ---------------------------------------------------------------------------
// Pricing constants
// ---------------------------------------------------------------------------

// Claude API pricing (Opus 4) in USD per million tokens.
const (
	OpusInputPerMTok  = 3.00  // $3 per million input tokens
	OpusOutputPerMTok = 15.00 // $15 per million output tokens
)

// ---------------------------------------------------------------------------
// SessionUsage
// ---------------------------------------------------------------------------

// SessionUsage is the public representation of a single Claude Code session's
// token usage and cost, derived from the on-disk session-meta JSON files that
// Claude Code writes to ~/.claude/usage-data/session-meta/.
type SessionUsage struct {
	SessionID    string         `json:"sessionId"`
	ProjectPath  string         `json:"projectPath"`
	StartTime    time.Time      `json:"startTime"`
	DurationMins int            `json:"durationMinutes"`
	InputTokens  int            `json:"inputTokens"`
	OutputTokens int            `json:"outputTokens"`
	ToolCounts   map[string]int `json:"toolCounts"`
	MessageCount int            `json:"messageCount"`
	CostUSD      float64        `json:"costUsd"`
}

// rawSessionMeta mirrors the JSON schema that Claude Code writes to disk.
// Fields use snake_case to match the on-disk format.
type rawSessionMeta struct {
	SessionID             string         `json:"session_id"`
	ProjectPath           string         `json:"project_path"`
	StartTime             string         `json:"start_time"`
	DurationMinutes       int            `json:"duration_minutes"`
	InputTokens           int            `json:"input_tokens"`
	OutputTokens          int            `json:"output_tokens"`
	ToolCounts            map[string]int `json:"tool_counts"`
	UserMessageCount      int            `json:"user_message_count"`
	AssistantMessageCount int            `json:"assistant_message_count"`
}

// ---------------------------------------------------------------------------
// Public functions
// ---------------------------------------------------------------------------

// CalculateCost returns the estimated USD cost for the given token counts
// using the Opus 4 pricing model.
func CalculateCost(inputTokens, outputTokens int) float64 {
	inputCost := float64(inputTokens) / 1_000_000 * OpusInputPerMTok
	outputCost := float64(outputTokens) / 1_000_000 * OpusOutputPerMTok
	return inputCost + outputCost
}

// GetAllSessionUsage reads every session-meta JSON file and returns the
// parsed results. Malformed or unreadable files are skipped with a warning
// logged via slog.
func GetAllSessionUsage() ([]SessionUsage, error) {
	dir := sessionMetaDir()

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read session-meta dir: %w", err)
	}

	var sessions []SessionUsage
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		usage, err := parseSessionFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			slog.Warn("skipping malformed session-meta file",
				"file", entry.Name(),
				"err", err,
			)
			continue
		}

		sessions = append(sessions, usage)
	}

	return sessions, nil
}

// GetUsageByProject returns session usage filtered to the given project path.
func GetUsageByProject(projectPath string) ([]SessionUsage, error) {
	all, err := GetAllSessionUsage()
	if err != nil {
		return nil, err
	}

	var filtered []SessionUsage
	for _, s := range all {
		if s.ProjectPath == projectPath {
			filtered = append(filtered, s)
		}
	}

	return filtered, nil
}

// GetDailyCosts aggregates all session usage into per-day cost summaries,
// sorted by date ascending.
func GetDailyCosts() ([]model.DailyCost, error) {
	all, err := GetAllSessionUsage()
	if err != nil {
		return nil, err
	}

	daily := make(map[string]*model.DailyCost)

	for _, s := range all {
		date := s.StartTime.Format("2006-01-02")

		dc, ok := daily[date]
		if !ok {
			dc = &model.DailyCost{Date: date}
			daily[date] = dc
		}

		dc.InputTokens += s.InputTokens
		dc.OutputTokens += s.OutputTokens
		dc.CostUSD += s.CostUSD
		dc.SessionCount++
	}

	result := make([]model.DailyCost, 0, len(daily))
	for _, dc := range daily {
		result = append(result, *dc)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Date < result[j].Date
	})

	return result, nil
}

// GetTotalSpend returns cumulative cost across three time horizons: all-time,
// this calendar month, and today.
func GetTotalSpend() (model.TotalSpend, error) {
	all, err := GetAllSessionUsage()
	if err != nil {
		return model.TotalSpend{}, err
	}

	now := time.Now()
	today := now.Format("2006-01-02")
	monthPrefix := now.Format("2006-01")

	var spend model.TotalSpend

	for _, s := range all {
		spend.AllTime += s.CostUSD

		sessionDate := s.StartTime.Format("2006-01-02")

		if strings.HasPrefix(sessionDate, monthPrefix) {
			spend.ThisMonth += s.CostUSD
		}
		if sessionDate == today {
			spend.Today += s.CostUSD
		}
	}

	return spend, nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// sessionMetaDir returns the path to Claude Code's usage-data session-meta
// directory.
func sessionMetaDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "usage-data", "session-meta")
}

// parseSessionFile reads a single session-meta JSON file and converts it to a
// SessionUsage value.
func parseSessionFile(path string) (SessionUsage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return SessionUsage{}, fmt.Errorf("read file: %w", err)
	}

	var raw rawSessionMeta
	if err := json.Unmarshal(data, &raw); err != nil {
		return SessionUsage{}, fmt.Errorf("unmarshal json: %w", err)
	}

	startTime, err := time.Parse(time.RFC3339Nano, raw.StartTime)
	if err != nil {
		return SessionUsage{}, fmt.Errorf("parse start_time %q: %w", raw.StartTime, err)
	}

	toolCounts := raw.ToolCounts
	if toolCounts == nil {
		toolCounts = make(map[string]int)
	}

	return SessionUsage{
		SessionID:    raw.SessionID,
		ProjectPath:  raw.ProjectPath,
		StartTime:    startTime,
		DurationMins: raw.DurationMinutes,
		InputTokens:  raw.InputTokens,
		OutputTokens: raw.OutputTokens,
		ToolCounts:   toolCounts,
		MessageCount: raw.UserMessageCount + raw.AssistantMessageCount,
		CostUSD:      CalculateCost(raw.InputTokens, raw.OutputTokens),
	}, nil
}
