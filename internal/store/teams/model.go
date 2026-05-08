// Package teams provides a MongoDB-backed store for team documents with
// soft-delete semantics.
package teams

import (
	"time"
)

// TeamStatus enumerates the possible states of a team.
type TeamStatus string

const (
	StatusForming TeamStatus = "FORMING"
	StatusActive  TeamStatus = "ACTIVE"
	StatusPaused  TeamStatus = "PAUSED"
	StatusRetired TeamStatus = "RETIRED"
)

// AgentStatus enumerates the possible states of an agent.
type AgentStatus string

const (
	AgentActive   AgentStatus = "ACTIVE"
	AgentInactive AgentStatus = "INACTIVE"
	AgentRetired  AgentStatus = "RETIRED"
)

// DirectiveStatus enumerates the possible states of a team directive.
type DirectiveStatus string

const (
	DirectiveActive  DirectiveStatus = "ACTIVE"
	DirectiveRevised DirectiveStatus = "REVISED"
	DirectiveRetired DirectiveStatus = "RETIRED"
)

// InitiativeStatus enumerates the possible states of an initiative.
type InitiativeStatus string

const (
	InitiativeProposed  InitiativeStatus = "PROPOSED"
	InitiativeActive    InitiativeStatus = "ACTIVE"
	InitiativeMeasuring InitiativeStatus = "MEASURING"
	InitiativeSucceeded InitiativeStatus = "SUCCEEDED"
	InitiativeFailed    InitiativeStatus = "FAILED"
	InitiativePivoted   InitiativeStatus = "PIVOTED"
)

// Member represents a team member.
type Member struct {
	Agent        string    `bson:"agent" json:"agent"`
	Role         string    `bson:"role" json:"role"`
	Type         string    `bson:"type" json:"type"`
	JoinedAt     time.Time `bson:"joined_at" json:"joined_at"`
	Capabilities []string  `bson:"capabilities,omitempty" json:"capabilities,omitempty"`
}

// Team is the canonical BSON shape for a team document in the global.teams collection.
type Team struct {
	TeamID      string    `bson:"team_id" json:"team_id"`
	Name        string    `bson:"name" json:"name"`
	DBName      string    `bson:"db_name" json:"db_name"`
	DirectiveID *string   `bson:"directive_id,omitempty" json:"directive_id,omitempty"`
	Members     []Member  `bson:"members" json:"members"`
	Artifacts   map[string]interface{} `bson:"artifacts" json:"artifacts"`
	Status      TeamStatus `bson:"status" json:"status"`
	CreatedAt   time.Time `bson:"created_at" json:"created_at"`
	UpdatedAt   time.Time `bson:"updated_at" json:"updated_at"`

	// Soft-delete fields (managed by softdelete package)
	Deleted   bool       `bson:"deleted,omitempty" json:"deleted,omitempty"`
	DeletedAt *time.Time `bson:"deleted_at,omitempty" json:"deleted_at,omitempty"`
}

// Agent is the canonical BSON shape for an agent document in the global.agents collection.
type Agent struct {
	AgentID     string    `bson:"agent_id" json:"agent_id"`
	Name        string    `bson:"name" json:"name"`
	DBName      string    `bson:"db_name" json:"db_name"`
	ProfilePath string    `bson:"profile_path" json:"profile_path"`
	Role        *string   `bson:"role,omitempty" json:"role,omitempty"`
	Status      AgentStatus `bson:"status" json:"status"`
	CreatedAt   time.Time `bson:"created_at" json:"created_at"`
	UpdatedAt   time.Time `bson:"updated_at" json:"updated_at"`

	// Soft-delete fields (managed by softdelete package)
	Deleted   bool       `bson:"deleted,omitempty" json:"deleted,omitempty"`
	DeletedAt *time.Time `bson:"deleted_at,omitempty" json:"deleted_at,omitempty"`
}

// SuccessCriterion represents a measurable target for a directive or initiative.
type SuccessCriterion struct {
	Metric string `bson:"metric" json:"metric"`
	Target string `bson:"target" json:"target"`
	Unit   string `bson:"unit" json:"unit"`
}

// Timeline captures initiative lifecycle timestamps.
type Timeline struct {
	StartedAt        *string `bson:"started_at,omitempty" json:"started_at,omitempty"`
	TargetCompletion *string `bson:"target_completion,omitempty" json:"target_completion,omitempty"`
	CompletedAt      *string `bson:"completed_at,omitempty" json:"completed_at,omitempty"`
}

// Directive is the canonical BSON shape for a team directive.
type Directive struct {
	DirectiveID     string           `bson:"directive_id" json:"directive_id"`
	TeamID          string           `bson:"team_id" json:"team_id"`
	Statement       string           `bson:"statement" json:"statement"`
	SuccessCriteria []SuccessCriterion `bson:"success_criteria" json:"success_criteria"`
	Status          DirectiveStatus  `bson:"status" json:"status"`
	Version         int              `bson:"version" json:"version"`
	CreatedAt       time.Time        `bson:"created_at" json:"created_at"`
	UpdatedAt       time.Time        `bson:"updated_at" json:"updated_at"`

	// Soft-delete fields (managed by softdelete package)
	Deleted   bool       `bson:"deleted,omitempty" json:"deleted,omitempty"`
	DeletedAt *time.Time `bson:"deleted_at,omitempty" json:"deleted_at,omitempty"`
}

// Initiative is the canonical BSON shape for a team initiative.
type Initiative struct {
	InitiativeID     string           `bson:"initiative_id" json:"initiative_id"`
	DirectiveID      string           `bson:"directive_id" json:"directive_id"`
	TeamID           string           `bson:"team_id" json:"team_id"`
	Title            string           `bson:"title" json:"title"`
	Description      string           `bson:"description" json:"description"`
	SuccessCriteria  []SuccessCriterion `bson:"success_criteria" json:"success_criteria"`
	Timeline         Timeline         `bson:"timeline" json:"timeline"`
	PlanIDs          []string         `bson:"plan_ids" json:"plan_ids"`
	Status           InitiativeStatus `bson:"status" json:"status"`
	OutcomeNotes     string           `bson:"outcome_notes" json:"outcome_notes"`
	CreatedAt        time.Time        `bson:"created_at" json:"created_at"`
	UpdatedAt        time.Time        `bson:"updated_at" json:"updated_at"`

	// Soft-delete fields (managed by softdelete package)
	Deleted   bool       `bson:"deleted,omitempty" json:"deleted,omitempty"`
	DeletedAt *time.Time `bson:"deleted_at,omitempty" json:"deleted_at,omitempty"`
}
