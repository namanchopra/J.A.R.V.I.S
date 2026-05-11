package model

import (
	"time"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// SessionTodo
// ---------------------------------------------------------------------------

// SessionTodo represents a to-do item associated with a managed session. Todos
// help track sub-tasks or goals within a session's scope.
type SessionTodo struct {
	ID        string    `json:"id"`
	SessionID string    `json:"sessionId"`
	Title     string    `json:"title"`
	Status    string    `json:"status"` // "pending" or "done"
	SortOrder int       `json:"sortOrder"`
	CreatedAt time.Time `json:"createdAt"`
}

// NewSessionTodo constructs a SessionTodo with a generated UUID, status set to
// "pending", and CreatedAt set to the current time.
func NewSessionTodo(sessionID, title string, sortOrder int) SessionTodo {
	return SessionTodo{
		ID:        uuid.New().String(),
		SessionID: sessionID,
		Title:     title,
		Status:    "pending",
		SortOrder: sortOrder,
		CreatedAt: time.Now(),
	}
}

// ValidTodoStatus reports whether s is a recognised todo status.
func ValidTodoStatus(s string) bool {
	return s == "pending" || s == "done"
}
