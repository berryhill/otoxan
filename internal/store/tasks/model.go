// Package tasks provides a MongoDB-backed store for task documents with
// soft-delete semantics.
package tasks

import (
	"time"
)

// TaskStatus enumerates the possible states of a task.
type TaskStatus string

const (
	StatusDraft      TaskStatus = "DRAFT"
	StatusQueued     TaskStatus = "QUEUED"
	StatusClaimed    TaskStatus = "CLAIMED"
	StatusRunning    TaskStatus = "RUNNING"
	StatusValidating TaskStatus = "VALIDATING"
	StatusCompleted  TaskStatus = "COMPLETED"
	StatusFailed     TaskStatus = "FAILED"
	StatusBlocked    TaskStatus = "BLOCKED"
	StatusCancelled  TaskStatus = "CANCELLED"
	StatusSkipped    TaskStatus = "SKIPPED"
)

// TaskType enumerates the kinds of tasks.
type TaskType string

const (
	TypeInternal        TaskType = "internal"
	TypeGitHubIssue     TaskType = "github_issue"
	TypeCodebaseOnboard TaskType = "codebase_onboard"
)

// RetryConfig holds the retry policy for a task.
type RetryConfig struct {
	Backoff             string `bson:"backoff" json:"backoff"`
	InitialDelaySeconds int    `bson:"initial_delay_seconds" json:"initial_delay_seconds"`
	MaxDelaySeconds     int    `bson:"max_delay_seconds" json:"max_delay_seconds"`
	Multiplier          int    `bson:"multiplier" json:"multiplier"`
}

// FailureContext holds the notification settings on failure.
type FailureContext struct {
	NotifyChannel  string `bson:"notify_channel" json:"notify_channel"`
	IncludeLogs    bool   `bson:"include_logs" json:"include_logs"`
	IncludeSummary bool   `bson:"include_summary" json:"include_summary"`
}

// Task is the canonical BSON shape for a task document.
type Task struct {
	TaskID                string         `bson:"task_id" json:"task_id"`
	ParentTaskID          *string        `bson:"parent_task_id,omitempty" json:"parent_task_id,omitempty"`
	EpicID                *string        `bson:"epic_id,omitempty" json:"epic_id,omitempty"`
	Phase                 *string        `bson:"phase,omitempty" json:"phase,omitempty"`
	PhaseOrder            *int           `bson:"phase_order,omitempty" json:"phase_order,omitempty"`
	PlanID                *string        `bson:"plan_id,omitempty" json:"plan_id,omitempty"`
	Title                 string         `bson:"title" json:"title"`
	Description           string         `bson:"description" json:"description"`
	Type                  TaskType       `bson:"type" json:"type"`
	Directive             *string        `bson:"directive,omitempty" json:"directive,omitempty"`
	Status                TaskStatus     `bson:"status" json:"status"`
	Priority              int            `bson:"priority" json:"priority"`
	ScheduledFor          *time.Time     `bson:"scheduled_for,omitempty" json:"scheduled_for,omitempty"`
	ScheduledReason       string         `bson:"scheduled_reason" json:"scheduled_reason"`
	Labels                []string       `bson:"labels" json:"labels"`
	Assignee              string         `bson:"assignee" json:"assignee"`
	AssigneeType          string         `bson:"assignee_type" json:"assignee_type"`
	AssigneeID            *string        `bson:"assignee_id,omitempty" json:"assignee_id,omitempty"`
	Attempts              int            `bson:"attempts" json:"attempts"`
	MaxRetries            int            `bson:"max_retries" json:"max_retries"`
	DependsOn             []string       `bson:"depends_on" json:"depends_on"`
	ParallelGroup         *string        `bson:"parallel_group,omitempty" json:"parallel_group,omitempty"`
	RetryConfig           RetryConfig    `bson:"retry_config" json:"retry_config"`
	FailurePattern        string         `bson:"failure_pattern" json:"failure_pattern"`
	FailureContext        FailureContext `bson:"failure_context" json:"failure_context"`
	Verification          *string        `bson:"verification,omitempty" json:"verification,omitempty"`
	Intent                string         `bson:"intent" json:"intent"`
	Implementation        string         `bson:"implementation" json:"implementation"`
	References            string         `bson:"references" json:"references"`
	PlanGoal              string         `bson:"plan_goal" json:"plan_goal"`
	PlanContext           string         `bson:"plan_context" json:"plan_context"`
	PhaseContext          string         `bson:"phase_context" json:"phase_context"`
	ParentProvider        *string        `bson:"parent_provider,omitempty" json:"parent_provider,omitempty"`
	InitiativeID          *string        `bson:"initiative_id,omitempty" json:"initiative_id,omitempty"`
	FlowRef               *string        `bson:"flow_ref,omitempty" json:"flow_ref,omitempty"`
	FlowSessionID         *string        `bson:"flow_session_id,omitempty" json:"flow_session_id,omitempty"`
	FlowTemplateID        *string        `bson:"flow_template_id,omitempty" json:"flow_template_id,omitempty"`
	FlowStepID            *string        `bson:"flow_step_id,omitempty" json:"flow_step_id,omitempty"`
	FlowStepType          *string        `bson:"flow_step_type,omitempty" json:"flow_step_type,omitempty"`
	FlowCurrentStep       *string        `bson:"flow_current_step,omitempty" json:"flow_current_step,omitempty"`
	FlowDelegatedSessions []string       `bson:"flow_delegated_sessions" json:"flow_delegated_sessions"`
	FlowCompletedAt       *time.Time     `bson:"flow_completed_at,omitempty" json:"flow_completed_at,omitempty"`
	FlowOutputsSummary    *string        `bson:"flow_outputs_summary,omitempty" json:"flow_outputs_summary,omitempty"`
	DelegatedTo           *string        `bson:"delegated_to,omitempty" json:"delegated_to,omitempty"`
	Output                *string        `bson:"output,omitempty" json:"output,omitempty"`
	Artifacts             []Artifact     `bson:"artifacts" json:"artifacts"`
	CreatedAt             time.Time      `bson:"created_at" json:"created_at"`
	UpdatedAt             time.Time      `bson:"updated_at" json:"updated_at"`
	StartedAt             *time.Time     `bson:"started_at,omitempty" json:"started_at,omitempty"`
	ClaimedAt             *time.Time     `bson:"claimed_at,omitempty" json:"claimed_at,omitempty"`
	CompletedAt           *time.Time     `bson:"completed_at,omitempty" json:"completed_at,omitempty"`

	// Soft-delete fields (managed by softdelete package)
	Deleted   bool       `bson:"deleted,omitempty" json:"deleted,omitempty"`
	DeletedAt *time.Time `bson:"deleted_at,omitempty" json:"deleted_at,omitempty"`
}

// Artifact represents an output produced by a task run.
type Artifact struct {
	ArtifactID string                 `bson:"artifact_id" json:"artifact_id"`
	Type       string                 `bson:"type" json:"type"`
	Content    map[string]interface{} `bson:"content" json:"content"`
	FilePath   *string                `bson:"file_path,omitempty" json:"file_path,omitempty"`
	CreatedAt  time.Time              `bson:"created_at" json:"created_at"`
}

// DefaultRetryConfig returns the default retry configuration.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		Backoff:             "exponential",
		InitialDelaySeconds: 30,
		MaxDelaySeconds:     300,
		Multiplier:          2,
	}
}

// DefaultFailureContext returns the default failure notification settings.
func DefaultFailureContext() FailureContext {
	return FailureContext{
		NotifyChannel:  "",
		IncludeLogs:    true,
		IncludeSummary: true,
	}
}
