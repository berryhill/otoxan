// Package plans provides a MongoDB-backed store for plan documents with
// soft-delete semantics.
package plans

import (
	"time"
)

// PlanStatus enumerates the possible states of a plan.
type PlanStatus string

const (
	StatusPlanning  PlanStatus = "PLANNING"
	StatusExecuting PlanStatus = "EXECUTING"
	StatusPaused    PlanStatus = "PAUSED"
	StatusCompleted PlanStatus = "COMPLETED"
	StatusAbandoned PlanStatus = "ABANDONED"
	StatusChecking  PlanStatus = "CHECKING"
	StatusAccepted  PlanStatus = "ACCEPTED"
	StatusRegressed PlanStatus = "REGRESSED"
)

// PlanType enumerates the kinds of plans.
type PlanType string

const (
	TypeStandard PlanType = "standard"
	TypeQAFirst  PlanType = "qa_first"
	TypeHotfix   PlanType = "hotfix"
)

// Plan is the canonical BSON shape for a plan document.
type Plan struct {
	PlanID          string                  `bson:"plan_id" json:"plan_id"`
	Title           string                  `bson:"title" json:"title"`
	Status          PlanStatus              `bson:"status" json:"status"`
	Owner           string                  `bson:"owner" json:"owner"`
	CreatedAt       time.Time               `bson:"created_at" json:"created_at"`
	UpdatedAt       time.Time               `bson:"updated_at" json:"updated_at"`
	Content         string                  `bson:"content" json:"content"`
	Tags            []string                `bson:"tags" json:"tags"`
	CreatedSession  string                  `bson:"created_session" json:"created_session"`
	UpdatedSessions []string                `bson:"updated_sessions" json:"updated_sessions"`
	PlanType        PlanType                `bson:"plan_type" json:"plan_type"`
	InitiativeID    *string                 `bson:"initiative_id,omitempty" json:"initiative_id,omitempty"`
	DirectiveID     *string                 `bson:"directive_id,omitempty" json:"directive_id,omitempty"`
	TeamID          *string                 `bson:"team_id,omitempty" json:"team_id,omitempty"`
	FlowSessionID   *string                 `bson:"flow_session_id,omitempty" json:"flow_session_id,omitempty"`
	EntityType      *string                 `bson:"entity_type,omitempty" json:"entity_type,omitempty"`
	FlowRef         *string                 `bson:"flow_ref,omitempty" json:"flow_ref,omitempty"`
	PlanFlowID      *string                 `bson:"plan_flow_id,omitempty" json:"plan_flow_id,omitempty"`
	Acceptance      *map[string]interface{} `bson:"acceptance,omitempty" json:"acceptance,omitempty"`
	FailureContext  *map[string]interface{} `bson:"failure_context,omitempty" json:"failure_context,omitempty"`
	ArchivedAt      *time.Time              `bson:"archived_at,omitempty" json:"archived_at,omitempty"`

	// Soft-delete fields (managed by softdelete package)
	Deleted   bool       `bson:"deleted,omitempty" json:"deleted,omitempty"`
	DeletedAt *time.Time `bson:"deleted_at,omitempty" json:"deleted_at,omitempty"`
}
