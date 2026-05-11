package model

import (
	"time"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// CostSnapshot
// ---------------------------------------------------------------------------

// CostSnapshot records the token usage and estimated cost for a single session
// at a given point in time.
type CostSnapshot struct {
	ID           string    `json:"id"`
	SessionID    string    `json:"sessionId"`
	ProjectPath  string    `json:"projectPath"`
	InputTokens  int       `json:"inputTokens"`
	OutputTokens int       `json:"outputTokens"`
	CostUSD      float64   `json:"costUsd"`
	RecordedAt   time.Time `json:"recordedAt"`
}

// NewCostSnapshot constructs a CostSnapshot with a generated UUID and
// RecordedAt set to the current time.
func NewCostSnapshot(sessionID, projectPath string, inputTokens, outputTokens int, costUSD float64) CostSnapshot {
	return CostSnapshot{
		ID:           uuid.New().String(),
		SessionID:    sessionID,
		ProjectPath:  projectPath,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		CostUSD:      costUSD,
		RecordedAt:   time.Now(),
	}
}

// ---------------------------------------------------------------------------
// DailyCost
// ---------------------------------------------------------------------------

// DailyCost aggregates token usage and cost for a single calendar day.
type DailyCost struct {
	Date         string  `json:"date"`
	InputTokens  int     `json:"inputTokens"`
	OutputTokens int     `json:"outputTokens"`
	CostUSD      float64 `json:"costUsd"`
	SessionCount int     `json:"sessionCount"`
}

// ---------------------------------------------------------------------------
// TotalSpend
// ---------------------------------------------------------------------------

// TotalSpend summarises cumulative cost across three time horizons.
type TotalSpend struct {
	AllTime   float64 `json:"allTime"`
	ThisMonth float64 `json:"thisMonth"`
	Today     float64 `json:"today"`
}
