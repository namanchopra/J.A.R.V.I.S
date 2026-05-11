package model

import (
	"time"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// SessionGroup
// ---------------------------------------------------------------------------

// SessionGroup is a named collection of repository paths, allowing the user to
// organise related projects and launch sessions against the whole group.
type SessionGroup struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Color       string    `json:"color"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// NewSessionGroup constructs a SessionGroup with a generated UUID and
// timestamps initialised to the current time.
func NewSessionGroup(name, description, color string) SessionGroup {
	now := time.Now()
	return SessionGroup{
		ID:          uuid.New().String(),
		Name:        name,
		Description: description,
		Color:       color,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

// ---------------------------------------------------------------------------
// GroupMember
// ---------------------------------------------------------------------------

// GroupMember records that a repository path belongs to a session group.
type GroupMember struct {
	GroupID  string    `json:"groupId"`
	RepoPath string   `json:"repoPath"`
	AddedAt  time.Time `json:"addedAt"`
}
