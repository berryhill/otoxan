// Package flows provides a MongoDB-backed store for flow documents with
// soft-delete semantics.
package flows

import (
	"time"
)

// FlowStatus enumerates the possible states of a flow.
type FlowStatus string

const (
	StatusDraft     FlowStatus = "DRAFT"
	StatusActive    FlowStatus = "ACTIVE"
	StatusPaused    FlowStatus = "PAUSED"
	StatusCompleted FlowStatus = "COMPLETED"
	StatusFailed    FlowStatus = "FAILED"
	StatusArchived  FlowStatus = "ARCHIVED"
)

// FlowStep represents a single step inside a flow.
type FlowStep struct {
	StepID      string                 `bson:"step_id" json:"step_id"`
	Name        string                 `bson:"name" json:"name"`
	Type        string                 `bson:"type" json:"type"`
	Order       int                    `bson:"order" json:"order"`
	Config      map[string]interface{} `bson:"config" json:"config"`
	NextSteps   []string               `bson:"next_steps" json:"next_steps"`
	PrevSteps   []string               `bson:"prev_steps" json:"prev_steps"`
	Condition   *string                `bson:"condition,omitempty" json:"condition,omitempty"`
	DelegatedTo *string                `bson:"delegated_to,omitempty" json:"delegated_to,omitempty"`
	CreatedAt   time.Time              `bson:"created_at" json:"created_at"`
	UpdatedAt   time.Time              `bson:"updated_at" json:"updated_at"`
}

// Flow is the canonical BSON shape for a flow document.
type Flow struct {
	FlowID       string     `bson:"flow_id" json:"flow_id"`
	Name         string     `bson:"name" json:"name"`
	Description  string     `bson:"description" json:"description"`
	Status       FlowStatus `bson:"status" json:"status"`
	Version      int        `bson:"version" json:"version"`
	Steps        []FlowStep `bson:"steps" json:"steps"`
	Tags         []string   `bson:"tags" json:"tags"`
	InitiativeID *string    `bson:"initiative_id,omitempty" json:"initiative_id,omitempty"`
	TeamID       *string    `bson:"team_id,omitempty" json:"team_id,omitempty"`
	SessionID    *string    `bson:"session_id,omitempty" json:"session_id,omitempty"`
	CreatedAt    time.Time  `bson:"created_at" json:"created_at"`
	UpdatedAt    time.Time  `bson:"updated_at" json:"updated_at"`

	// Soft-delete fields (managed by softdelete package)
	Deleted   bool       `bson:"deleted,omitempty" json:"deleted,omitempty"`
	DeletedAt *time.Time `bson:"deleted_at,omitempty" json:"deleted_at,omitempty"`
}
