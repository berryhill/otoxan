package taskqueue

import (
	"context"
	"fmt"
	"time"

	"github.com/silas/otoxan/internal/state"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ------------------------------------------------------------------
// Enums
// ------------------------------------------------------------------

// TaskStatus enumerates the possible states of a task.
type TaskStatus string

const (
	TaskStatusDraft      TaskStatus = "DRAFT"
	TaskStatusQueued     TaskStatus = "QUEUED"
	TaskStatusClaimed    TaskStatus = "CLAIMED"
	TaskStatusRunning    TaskStatus = "RUNNING"
	TaskStatusValidating TaskStatus = "VALIDATING"
	TaskStatusCompleted  TaskStatus = "COMPLETED"
	TaskStatusFailed     TaskStatus = "FAILED"
	TaskStatusBlocked    TaskStatus = "BLOCKED"
	TaskStatusCancelled  TaskStatus = "CANCELLED"
	TaskStatusSkipped    TaskStatus = "SKIPPED"
)

// TaskType enumerates the kinds of tasks.
type TaskType string

const (
	TaskTypeInternal        TaskType = "internal"
	TaskTypeGitHubIssue     TaskType = "github_issue"
	TaskTypeCodebaseOnboard TaskType = "codebase_onboard"
)

// EventType enumerates the kinds of task events.
type EventType string

const (
	EventTypeTaskCreated    EventType = "task_created"
	EventTypeTaskQueued     EventType = "task_queued"
	EventTypeTaskClaimed    EventType = "task_claimed"
	EventTypeTaskStarted    EventType = "task_started"
	EventTypeTaskProgress   EventType = "task_progress"
	EventTypeTaskOutput     EventType = "task_output"
	EventTypeTaskValidating EventType = "task_validating"
	EventTypeTaskValidated  EventType = "task_validated"
	EventTypeTaskFailed     EventType = "task_failed"
	EventTypeTaskRetrying   EventType = "task_retrying"
	EventTypeTaskBlocked    EventType = "task_blocked"
	EventTypeTaskUnblocked  EventType = "task_unblocked"
	EventTypeTaskCancelled  EventType = "task_cancelled"
	EventTypeTaskCompleted  EventType = "task_completed"
	EventTypeTaskRetried    EventType = "task_retried"
	EventTypeTaskPaused     EventType = "task_paused"
)

// ------------------------------------------------------------------
// Document models
// ------------------------------------------------------------------

// TaskDoc is the canonical BSON shape for a task in the tasks collection.
type TaskDoc struct {
	TaskID                string     `bson:"task_id" json:"task_id"`
	ParentTaskID          *string    `bson:"parent_task_id,omitempty" json:"parent_task_id,omitempty"`
	EpicID                *string    `bson:"epic_id,omitempty" json:"epic_id,omitempty"`
	Phase                 *string    `bson:"phase,omitempty" json:"phase,omitempty"`
	PhaseOrder            *int       `bson:"phase_order,omitempty" json:"phase_order,omitempty"`
	PlanID                *string    `bson:"plan_id,omitempty" json:"plan_id,omitempty"`
	Title                 string     `bson:"title" json:"title"`
	Description           string     `bson:"description" json:"description"`
	Type                  TaskType   `bson:"type" json:"type"`
	Directive             *string    `bson:"directive,omitempty" json:"directive,omitempty"`
	Status                TaskStatus `bson:"status" json:"status"`
	Priority              int        `bson:"priority" json:"priority"`
	ScheduledFor          *time.Time `bson:"scheduled_for,omitempty" json:"scheduled_for,omitempty"`
	ScheduledReason       string     `bson:"scheduled_reason" json:"scheduled_reason"`
	Labels                []string   `bson:"labels" json:"labels"`
	Assignee              string     `bson:"assignee" json:"assignee"`
	AssigneeType          string     `bson:"assignee_type" json:"assignee_type"`
	AssigneeID            *string    `bson:"assignee_id,omitempty" json:"assignee_id,omitempty"`
	Attempts              int        `bson:"attempts" json:"attempts"`
	MaxRetries            int        `bson:"max_retries" json:"max_retries"`
	DependsOn             []string   `bson:"depends_on" json:"depends_on"`
	DependsOnPlans        []string   `bson:"depends_on_plans" json:"depends_on_plans"`
	ParallelGroup         *string    `bson:"parallel_group,omitempty" json:"parallel_group,omitempty"`
	RetryConfig           RetryConfig `bson:"retry_config" json:"retry_config"`
	FailurePattern        string      `bson:"failure_pattern" json:"failure_pattern"`
	FailureContext        FailureContext `bson:"failure_context" json:"failure_context"`
	Verification          *string    `bson:"verification,omitempty" json:"verification,omitempty"`
	Intent                string     `bson:"intent" json:"intent"`
	Implementation        string     `bson:"implementation" json:"implementation"`
	References            string     `bson:"references" json:"references"`
	PlanGoal              string     `bson:"plan_goal" json:"plan_goal"`
	PlanContext           string     `bson:"plan_context" json:"plan_context"`
	PhaseContext          string     `bson:"phase_context" json:"phase_context"`
	ParentProvider        *string    `bson:"parent_provider,omitempty" json:"parent_provider,omitempty"`
	InitiativeID          *string    `bson:"initiative_id,omitempty" json:"initiative_id,omitempty"`
	FlowRef               *string    `bson:"flow_ref,omitempty" json:"flow_ref,omitempty"`
	FlowSessionID         *string    `bson:"flow_session_id,omitempty" json:"flow_session_id,omitempty"`
	FlowTemplateID        *string    `bson:"flow_template_id,omitempty" json:"flow_template_id,omitempty"`
	FlowStepID            *string    `bson:"flow_step_id,omitempty" json:"flow_step_id,omitempty"`
	FlowStepType          *string    `bson:"flow_step_type,omitempty" json:"flow_step_type,omitempty"`
	FlowCurrentStep       *int       `bson:"flow_current_step,omitempty" json:"flow_current_step,omitempty"`
	FlowDelegatedSessions []string  `bson:"flow_delegated_sessions" json:"flow_delegated_sessions"`
	FlowCompletedAt       *time.Time `bson:"flow_completed_at,omitempty" json:"flow_completed_at,omitempty"`
	FlowOutputsSummary    *string    `bson:"flow_outputs_summary,omitempty" json:"flow_outputs_summary,omitempty"`
	DelegatedTo           *string    `bson:"delegated_to,omitempty" json:"delegated_to,omitempty"`
	Output                *string    `bson:"output,omitempty" json:"output,omitempty"`
	Artifacts             []Artifact `bson:"artifacts" json:"artifacts"`
	CreatedAt             time.Time  `bson:"created_at" json:"created_at"`
	UpdatedAt             time.Time  `bson:"updated_at" json:"updated_at"`
	StartedAt             *time.Time `bson:"started_at,omitempty" json:"started_at,omitempty"`
	ClaimedAt             *time.Time `bson:"claimed_at,omitempty" json:"claimed_at,omitempty"`
	CompletedAt           *time.Time `bson:"completed_at,omitempty" json:"completed_at,omitempty"`
	QueuedAt              *time.Time `bson:"queued_at,omitempty" json:"queued_at,omitempty"`
	QueuedBy              *string    `bson:"queued_by,omitempty" json:"queued_by,omitempty"`
	PausedAt              *time.Time `bson:"paused_at,omitempty" json:"paused_at,omitempty"`
	PausedBy              *string    `bson:"paused_by,omitempty" json:"paused_by,omitempty"`
	LastHeartbeat         *time.Time `bson:"last_heartbeat,omitempty" json:"last_heartbeat,omitempty"`
	BlockingOn            *string    `bson:"blocking_on,omitempty" json:"blocking_on,omitempty"`
	DeletedAt             *time.Time `bson:"deleted_at,omitempty" json:"deleted_at,omitempty"`
}

// RetryConfig defines backoff behaviour for failed tasks.
type RetryConfig struct {
	Backoff         string `bson:"backoff" json:"backoff"`
	InitialDelaySec int    `bson:"initial_delay_seconds" json:"initial_delay_seconds"`
	MaxDelaySec     int    `bson:"max_delay_seconds" json:"max_delay_seconds"`
	Multiplier      int    `bson:"multiplier" json:"multiplier"`
}

// FailureContext carries notification settings for terminal failures.
type FailureContext struct {
	NotifyChannel  string `bson:"notify_channel" json:"notify_channel"`
	IncludeLogs    bool   `bson:"include_logs" json:"include_logs"`
	IncludeSummary bool   `bson:"include_summary" json:"include_summary"`
}

// Artifact represents a task output artifact.
type Artifact struct {
	Name string `bson:"name" json:"name"`
	Type string `bson:"type" json:"type"`
	URL  string `bson:"url" json:"url"`
}

// TaskEventDoc is the canonical BSON shape for a task event.
type TaskEventDoc struct {
	TaskID    string    `bson:"task_id" json:"task_id"`
	Sequence  int       `bson:"sequence" json:"sequence"`
	EventType EventType `bson:"event_type" json:"event_type"`
	Timestamp time.Time `bson:"timestamp" json:"timestamp"`
	Actor     string    `bson:"actor" json:"actor"`
	Data      bson.M    `bson:"data" json:"data"`
	DeletedAt *time.Time `bson:"deleted_at,omitempty" json:"deleted_at,omitempty"`
}

// TaskCounterDoc tracks per-task atomic sequence counters.
type TaskCounterDoc struct {
	TaskID    string     `bson:"task_id" json:"task_id"`
	Sequence  int        `bson:"sequence" json:"sequence"`
	DeletedAt *time.Time `bson:"deleted_at,omitempty" json:"deleted_at,omitempty"`
}

// DispatchRequestDoc represents a pending dispatch request.
type DispatchRequestDoc struct {
	RequestID   string     `bson:"request_id" json:"request_id"`
	TaskID      string     `bson:"task_id" json:"task_id"`
	Status      string     `bson:"status" json:"status"` // PENDING, CLAIMED, COMPLETED, FAILED
	AgentName   string     `bson:"agent_name" json:"agent_name"`
	CreatedAt   time.Time  `bson:"created_at" json:"created_at"`
	UpdatedAt   time.Time  `bson:"updated_at" json:"updated_at"`
	ClaimedAt   *time.Time `bson:"claimed_at,omitempty" json:"claimed_at,omitempty"`
	CompletedAt *time.Time `bson:"completed_at,omitempty" json:"completed_at,omitempty"`
	DeletedAt   *time.Time `bson:"deleted_at,omitempty" json:"deleted_at,omitempty"`
}

// DispatchSpawnDoc tracks a spawned subagent process.
type DispatchSpawnDoc struct {
	SpawnID     string     `bson:"spawn_id" json:"spawn_id"`
	TaskID      string     `bson:"task_id" json:"task_id"`
	PID         int        `bson:"pid" json:"pid"`
	SessionID   string     `bson:"session_id" json:"session_id"`
	Status      string     `bson:"status" json:"status"` // RUNNING, COMPLETED, FAILED, REAPED
	AgentName   string     `bson:"agent_name" json:"agent_name"`
	StartedAt   time.Time  `bson:"started_at" json:"started_at"`
	CompletedAt *time.Time `bson:"completed_at,omitempty" json:"completed_at,omitempty"`
	ExitCode    *int       `bson:"exit_code,omitempty" json:"exit_code,omitempty"`
	OutputPath  string     `bson:"output_path" json:"output_path"`
	DeletedAt   *time.Time `bson:"deleted_at,omitempty" json:"deleted_at,omitempty"`
}

// ------------------------------------------------------------------
// Defaults
// ------------------------------------------------------------------

// DefaultRetryConfig is the fallback retry configuration.
var DefaultRetryConfig = RetryConfig{
	Backoff:         "exponential",
	InitialDelaySec: 30,
	MaxDelaySec:     300,
	Multiplier:      2,
}

// DefaultFailureContext is the fallback failure notification context.
var DefaultFailureContext = FailureContext{
	NotifyChannel:  "",
	IncludeLogs:    true,
	IncludeSummary: true,
}

// ------------------------------------------------------------------
// Store construction
// ------------------------------------------------------------------

// NewStore creates a task queue store for the named agent.
// It ensures required indexes on all collections.
func NewStore(client *mongo.Client, agentName string) (*Store, error) {
	if err := state.ValidateAgentName(agentName); err != nil {
		return nil, err
	}
	db, err := state.AgentDB(client, agentName)
	if err != nil {
		return nil, err
	}
	s := &Store{
		agentName:        agentName,
		tasks:            db.Collection("tasks"),
		taskEvents:       db.Collection("task_events"),
		taskCounters:     db.Collection("task_counters"),
		dispatchRequests: db.Collection("dispatch_requests"),
		dispatchSpawns:   db.Collection("dispatch_spawns"),
	}
	if err := s.ensureIndexes(context.Background()); err != nil {
		return nil, fmt.Errorf("ensure indexes: %w", err)
	}
	return s, nil
}

// Store is a MongoDB-backed task queue for a single agent.
type Store struct {
	agentName        string
	tasks            *mongo.Collection
	taskEvents       *mongo.Collection
	taskCounters     *mongo.Collection
	dispatchRequests *mongo.Collection
	dispatchSpawns   *mongo.Collection
}

// ------------------------------------------------------------------
// Indexes
// ------------------------------------------------------------------

func (s *Store) ensureIndexes(ctx context.Context) error {
	// Tasks indexes
	taskIndexes := []mongo.IndexModel{
		{Keys: bson.D{{Key: "task_id", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "status", Value: 1}}},
		{Keys: bson.D{{Key: "plan_id", Value: 1}}},
		{Keys: bson.D{{Key: "epic_id", Value: 1}}},
		{Keys: bson.D{{Key: "assignee", Value: 1}}},
		{Keys: bson.D{{Key: "assignee_type", Value: 1}, {Key: "assignee_id", Value: 1}}},
		{Keys: bson.D{{Key: "priority", Value: 1}}},
		{Keys: bson.D{{Key: "created_at", Value: 1}}},
		{Keys: bson.D{{Key: "status", Value: 1}, {Key: "priority", Value: 1}, {Key: "created_at", Value: 1}}},
		{Keys: bson.D{{Key: "scheduled_for", Value: 1}}, Options: options.Index().SetSparse(true)},
		{Keys: bson.D{{Key: "initiative_id", Value: 1}}, Options: options.Index().SetSparse(true)},
	}
	if _, err := s.tasks.Indexes().CreateMany(ctx, taskIndexes); err != nil {
		return fmt.Errorf("tasks indexes: %w", err)
	}

	// Task events indexes
	eventIndexes := []mongo.IndexModel{
		{Keys: bson.D{{Key: "task_id", Value: 1}, {Key: "sequence", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "task_id", Value: 1}}},
		{Keys: bson.D{{Key: "timestamp", Value: 1}}},
	}
	if _, err := s.taskEvents.Indexes().CreateMany(ctx, eventIndexes); err != nil {
		return fmt.Errorf("task_events indexes: %w", err)
	}

	// Dispatch requests indexes
	drIndexes := []mongo.IndexModel{
		{Keys: bson.D{{Key: "request_id", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "task_id", Value: 1}}},
		{Keys: bson.D{{Key: "status", Value: 1}}},
		{Keys: bson.D{{Key: "agent_name", Value: 1}}},
	}
	if _, err := s.dispatchRequests.Indexes().CreateMany(ctx, drIndexes); err != nil {
		return fmt.Errorf("dispatch_requests indexes: %w", err)
	}

	// Dispatch spawns indexes
	dsIndexes := []mongo.IndexModel{
		{Keys: bson.D{{Key: "spawn_id", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "task_id", Value: 1}}},
		{Keys: bson.D{{Key: "status", Value: 1}}},
	}
	if _, err := s.dispatchSpawns.Indexes().CreateMany(ctx, dsIndexes); err != nil {
		return fmt.Errorf("dispatch_spawns indexes: %w", err)
	}

	return nil
}

// ------------------------------------------------------------------
// Task CRUD
// ------------------------------------------------------------------

// CreateTask creates a new task. Returns the generated task_id.
func (s *Store) CreateTask(ctx context.Context, taskData bson.M) (string, error) {
	now := time.Now().UTC()
	taskID := generateTaskID()

	assigneeType := "agent"
	if v, ok := taskData["assignee_type"]; ok {
		assigneeType = v.(string)
	}
	if assigneeType != "agent" && assigneeType != "human" {
		return "", fmt.Errorf("assignee_type must be 'agent' or 'human', got %q", assigneeType)
	}

	doc := TaskDoc{
		TaskID:         taskID,
		Title:          getString(taskData, "title"),
		Description:    getString(taskData, "description"),
		Type:           TaskType(getString(taskData, "type", string(TaskTypeInternal))),
		Status:         TaskStatus(getString(taskData, "status", string(TaskStatusQueued))),
		Priority:       getInt(taskData, "priority", 2),
		Assignee:       getString(taskData, "assignee", s.agentName),
		AssigneeType:   assigneeType,
		Attempts:       0,
		MaxRetries:     getInt(taskData, "max_retries", 3),
		DependsOn:      getStringSlice(taskData, "depends_on"),
		DependsOnPlans: getStringSlice(taskData, "depends_on_plans"),
		RetryConfig:    getRetryConfig(taskData),
		FailurePattern: getString(taskData, "failure_pattern", "notify_and_halt"),
		FailureContext: getFailureContext(taskData),
		Labels:         getStringSlice(taskData, "labels"),
		Artifacts:      []Artifact{},
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if v, ok := taskData["parent_task_id"]; ok {
		str := v.(string)
		doc.ParentTaskID = &str
	}
	if v, ok := taskData["epic_id"]; ok {
		str := v.(string)
		doc.EpicID = &str
	}
	if v, ok := taskData["phase"]; ok {
		str := v.(string)
		doc.Phase = &str
	}
	if v, ok := taskData["phase_order"]; ok {
		n := v.(int)
		doc.PhaseOrder = &n
	}
	if v, ok := taskData["plan_id"]; ok {
		str := v.(string)
		doc.PlanID = &str
	}
	if v, ok := taskData["directive"]; ok {
		str := v.(string)
		doc.Directive = &str
	}
	if v, ok := taskData["scheduled_for"]; ok {
		t := v.(time.Time)
		doc.ScheduledFor = &t
	}
	if v, ok := taskData["scheduled_reason"]; ok {
		doc.ScheduledReason = v.(string)
	}
	if v, ok := taskData["assignee_id"]; ok {
		str := v.(string)
		doc.AssigneeID = &str
	}
	if v, ok := taskData["parallel_group"]; ok {
		str := v.(string)
		doc.ParallelGroup = &str
	}
	if v, ok := taskData["verification"]; ok {
		str := v.(string)
		doc.Verification = &str
	}
	if v, ok := taskData["intent"]; ok {
		doc.Intent = v.(string)
	}
	if v, ok := taskData["implementation"]; ok {
		doc.Implementation = v.(string)
	}
	if v, ok := taskData["references"]; ok {
		doc.References = v.(string)
	}
	if v, ok := taskData["plan_goal"]; ok {
		doc.PlanGoal = v.(string)
	}
	if v, ok := taskData["plan_context"]; ok {
		doc.PlanContext = v.(string)
	}
	if v, ok := taskData["phase_context"]; ok {
		doc.PhaseContext = v.(string)
	}
	if v, ok := taskData["parent_provider"]; ok {
		str := v.(string)
		doc.ParentProvider = &str
	}
	if v, ok := taskData["initiative_id"]; ok {
		str := v.(string)
		doc.InitiativeID = &str
	}
	if v, ok := taskData["flow_ref"]; ok {
		str := v.(string)
		doc.FlowRef = &str
	}
	if v, ok := taskData["flow_session_id"]; ok {
		str := v.(string)
		doc.FlowSessionID = &str
	}
	if v, ok := taskData["flow_template_id"]; ok {
		str := v.(string)
		doc.FlowTemplateID = &str
	}
	if v, ok := taskData["flow_step_id"]; ok {
		str := v.(string)
		doc.FlowStepID = &str
	}
	if v, ok := taskData["flow_step_type"]; ok {
		str := v.(string)
		doc.FlowStepType = &str
	}
	if v, ok := taskData["flow_current_step"]; ok {
		n := v.(int)
		doc.FlowCurrentStep = &n
	}
	if v, ok := taskData["flow_delegated_sessions"]; ok {
		doc.FlowDelegatedSessions = v.([]string)
	}
	if v, ok := taskData["flow_completed_at"]; ok {
		t := v.(time.Time)
		doc.FlowCompletedAt = &t
	}
	if v, ok := taskData["flow_outputs_summary"]; ok {
		str := v.(string)
		doc.FlowOutputsSummary = &str
	}
	if v, ok := taskData["delegated_to"]; ok {
		str := v.(string)
		doc.DelegatedTo = &str
	}

	if _, err := s.tasks.InsertOne(ctx, doc); err != nil {
		return "", fmt.Errorf("insert task: %w", err)
	}

	s.AddEvent(ctx, taskID, EventTypeTaskCreated, bson.M{
		"source":          getString(taskData, "source", "manual"),
		"plan_id":         doc.PlanID,
		"epic_id":         doc.EpicID,
		"title":           doc.Title,
		"type":            doc.Type,
		"directive":       doc.Directive,
		"priority":        doc.Priority,
		"depends_on":      doc.DependsOn,
		"retry_config":    doc.RetryConfig,
		"failure_pattern": doc.FailurePattern,
	})

	if doc.Status == TaskStatusQueued && len(doc.DependsOn) == 0 {
		s.AddEvent(ctx, taskID, EventTypeTaskQueued, bson.M{
			"position": 0,
			"reason":   "no_dependencies",
		})
	}

	return taskID, nil
}

// GetTask retrieves a single task by task_id.
func (s *Store) GetTask(ctx context.Context, taskID string) (*TaskDoc, error) {
	var doc TaskDoc
	if err := s.tasks.FindOne(ctx, bson.M{"task_id": taskID, "deleted_at": bson.M{"$exists": false}}).Decode(&doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

// ListTasks lists tasks with optional filters.
type ListTasksOptions struct {
	Status         []TaskStatus
	Assignee       string
	PlanID         string
	AssigneeType   string
	AssigneeID     string
	Limit          int
	IncludeDeleted bool
}

// ListTasks returns tasks matching the provided filters, sorted by priority ASC, created_at ASC.
func (s *Store) ListTasks(ctx context.Context, opts ListTasksOptions) ([]TaskDoc, error) {
	filter := bson.M{}
	if !opts.IncludeDeleted {
		filter["deleted_at"] = bson.M{"$exists": false}
	}
	if len(opts.Status) > 0 {
		if len(opts.Status) == 1 {
			filter["status"] = opts.Status[0]
		} else {
			statuses := make([]string, len(opts.Status))
			for i, st := range opts.Status {
				statuses[i] = string(st)
			}
			filter["status"] = bson.M{"$in": statuses}
		}
	}
	if opts.Assignee != "" {
		filter["assignee"] = opts.Assignee
	}
	if opts.PlanID != "" {
		filter["plan_id"] = opts.PlanID
	}
	if opts.AssigneeType != "" {
		filter["assignee_type"] = opts.AssigneeType
	}
	if opts.AssigneeID != "" {
		filter["assignee_id"] = opts.AssigneeID
	}

	findOpts := options.Find().SetSort(bson.D{
		{Key: "priority", Value: 1},
		{Key: "created_at", Value: 1},
	})
	if opts.Limit > 0 {
		findOpts.SetLimit(int64(opts.Limit))
	}

	cur, err := s.tasks.Find(ctx, filter, findOpts)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)

	var docs []TaskDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	return docs, nil
}

// UpdateTask patches fields of an existing task. Sets updated_at automatically.
func (s *Store) UpdateTask(ctx context.Context, taskID string, updates bson.M) (*mongo.UpdateResult, error) {
	updates["updated_at"] = time.Now().UTC()
	return s.tasks.UpdateOne(ctx, bson.M{"task_id": taskID}, bson.M{"$set": updates})
}

// DeleteTask soft-deletes a task and its events/counters.
func (s *Store) DeleteTask(ctx context.Context, taskID string) (*mongo.UpdateResult, error) {
	now := time.Now().UTC()
	// Soft-delete events and counters
	_, _ = s.taskEvents.UpdateMany(ctx, bson.M{"task_id": taskID}, bson.M{"$set": bson.M{"deleted_at": now}})
	_, _ = s.taskCounters.UpdateOne(ctx, bson.M{"task_id": taskID}, bson.M{"$set": bson.M{"deleted_at": now}})
	return s.tasks.UpdateOne(ctx, bson.M{"task_id": taskID}, bson.M{"$set": bson.M{"deleted_at": now}})
}

// HardDeleteTask permanently removes a task and its events/counters.
func (s *Store) HardDeleteTask(ctx context.Context, taskID string) (*mongo.DeleteResult, error) {
	_, _ = s.taskEvents.DeleteMany(ctx, bson.M{"task_id": taskID})
	_, _ = s.taskCounters.DeleteOne(ctx, bson.M{"task_id": taskID})
	return s.tasks.DeleteOne(ctx, bson.M{"task_id": taskID})
}

// ------------------------------------------------------------------
// Queue operations
// ------------------------------------------------------------------

// ClaimTask atomically claims the next runnable task for the given assignee.
// Returns the claimed task, or nil if nothing is runnable or at concurrency limit.
func (s *Store) ClaimTask(ctx context.Context, assignee string, concurrencyLimit int) (*TaskDoc, error) {
	running, err := s.CountRunning(ctx, assignee)
	if err != nil {
		return nil, err
	}
	if running >= concurrencyLimit {
		return nil, nil
	}

	// Fetch all task statuses for dependency checking
	allTasks, err := s.ListTasks(ctx, ListTasksOptions{Limit: 10000})
	if err != nil {
		return nil, err
	}
	taskStatuses := make(map[string]TaskStatus, len(allTasks))
	for _, t := range allTasks {
		taskStatuses[t.TaskID] = t.Status
	}

	// Find QUEUED tasks whose scheduled time has passed
	scheduledFilter := bson.M{
		"status":     TaskStatusQueued,
		"deleted_at": bson.M{"$exists": false},
		"$or": []bson.M{
			{"scheduled_for": bson.M{"$exists": false}},
			{"scheduled_for": bson.M{"$eq": nil}},
			{"scheduled_for": bson.M{"$lte": time.Now().UTC()}},
		},
	}
	findOpts := options.Find().SetSort(bson.D{
		{Key: "priority", Value: 1},
		{Key: "created_at", Value: 1},
	})
	cur, err := s.tasks.Find(ctx, scheduledFilter, findOpts)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)

	for cur.Next(ctx) {
		var task TaskDoc
		if err := cur.Decode(&task); err != nil {
			continue
		}
		depsMet := true
		for _, dep := range task.DependsOn {
			st, ok := taskStatuses[dep]
			if !ok || (st != TaskStatusCompleted && st != TaskStatusSkipped) {
				depsMet = false
				break
			}
		}
		if !depsMet {
			continue
		}

		// Atomic claim
		now := time.Now().UTC()
		res, err := s.tasks.UpdateOne(ctx,
			bson.M{"task_id": task.TaskID, "status": TaskStatusQueued},
			bson.M{"$set": bson.M{
				"status":     TaskStatusClaimed,
				"claimed_at": now,
				"updated_at": now,
				"assignee":   assignee,
			}},
		)
		if err != nil {
			continue
		}
		if res.ModifiedCount > 0 {
			s.AddEvent(ctx, task.TaskID, EventTypeTaskClaimed, bson.M{
				"agent":      assignee,
				"session_id": nil,
			})
			return s.GetTask(ctx, task.TaskID)
		}
	}
	return nil, nil
}

// MarkRunning transitions CLAIMED -> RUNNING.
func (s *Store) MarkRunning(ctx context.Context, taskID string, sessionID string) (bool, error) {
	now := time.Now().UTC()
	res, err := s.tasks.UpdateOne(ctx,
		bson.M{"task_id": taskID, "status": TaskStatusClaimed},
		bson.M{"$set": bson.M{
			"status":     TaskStatusRunning,
			"started_at": now,
			"updated_at": now,
		}},
	)
	if err != nil {
		return false, err
	}
	if res.ModifiedCount > 0 {
		// Increment attempts
		_, _ = s.tasks.UpdateOne(ctx,
			bson.M{"task_id": taskID},
			bson.M{"$inc": bson.M{"attempts": 1}},
		)
		s.AddEvent(ctx, taskID, EventTypeTaskStarted, bson.M{
			"delegate_session": sessionID,
		})
		return true, nil
	}
	return false, nil
}

// MarkCompleted transitions RUNNING/VALIDATING -> COMPLETED.
func (s *Store) MarkCompleted(ctx context.Context, taskID string, output string, artifacts []Artifact) (bool, error) {
	now := time.Now().UTC()
	res, err := s.tasks.UpdateOne(ctx,
		bson.M{"task_id": taskID, "status": bson.M{"$in": []TaskStatus{TaskStatusRunning, TaskStatusValidating}}},
		bson.M{"$set": bson.M{
			"status":      TaskStatusCompleted,
			"completed_at": now,
			"updated_at":   now,
			"output":       output,
			"artifacts":    artifacts,
		}},
	)
	if err != nil {
		return false, err
	}
	if res.ModifiedCount > 0 {
		task, _ := s.GetTask(ctx, taskID)
		duration := 0
		if task != nil && task.StartedAt != nil {
			duration = int(now.Sub(*task.StartedAt).Seconds())
		}
		s.AddEvent(ctx, taskID, EventTypeTaskCompleted, bson.M{
			"final_status":     "COMPLETED",
			"duration_seconds": duration,
			"output_size":      len(output),
		})
		s.AddEvent(ctx, taskID, EventTypeTaskOutput, bson.M{
			"summary":   output,
			"artifacts": artifacts,
		})
		return true, nil
	}
	return false, nil
}

// MarkFailed transitions RUNNING/VALIDATING -> FAILED (or re-queues for retry).
func (s *Store) MarkFailed(ctx context.Context, taskID string, errorMsg string, willRetry bool) (bool, error) {
	now := time.Now().UTC()
	task, err := s.GetTask(ctx, taskID)
	if err != nil {
		return false, err
	}

	eventData := bson.M{
		"attempt":       task.Attempts,
		"error_message": errorMsg,
		"error_type":    "execution_error",
		"will_retry":    willRetry,
	}
	s.AddEvent(ctx, taskID, EventTypeTaskFailed, eventData)

	if willRetry {
		res, err := s.tasks.UpdateOne(ctx,
			bson.M{"task_id": taskID},
			bson.M{"$set": bson.M{
				"status":     TaskStatusQueued,
				"updated_at": now,
			}},
		)
		if err != nil {
			return false, err
		}
		return res.ModifiedCount > 0, nil
	}

	res, err := s.tasks.UpdateOne(ctx,
		bson.M{"task_id": taskID, "status": bson.M{"$in": []TaskStatus{TaskStatusRunning, TaskStatusValidating}}},
		bson.M{"$set": bson.M{
			"status":       TaskStatusFailed,
			"updated_at":   now,
			"completed_at": now,
			"output":       errorMsg,
		}},
	)
	if err != nil {
		return false, err
	}
	return res.ModifiedCount > 0, nil
}

// MarkRetried manually retries a FAILED/BLOCKED task. Resets attempts and re-queues.
func (s *Store) MarkRetried(ctx context.Context, taskID string, reason string, retriedBy string) (bool, error) {
	now := time.Now().UTC()
	res, err := s.tasks.UpdateOne(ctx,
		bson.M{"task_id": taskID, "status": bson.M{"$in": []TaskStatus{TaskStatusFailed, TaskStatusBlocked}}},
		bson.M{"$set": bson.M{
			"status":       TaskStatusQueued,
			"attempts":     0,
			"updated_at":   now,
			"output":       nil,
			"started_at":   nil,
			"claimed_at":   nil,
			"completed_at": nil,
		}},
	)
	if err != nil {
		return false, err
	}
	if res.ModifiedCount > 0 {
		s.AddEvent(ctx, taskID, EventTypeTaskRetried, bson.M{
			"reason":     reason,
			"retried_by": retriedBy,
		})
		s.AddEvent(ctx, taskID, EventTypeTaskQueued, bson.M{
			"reason":   "retry",
			"position": 0,
		})
		return true, nil
	}
	return false, nil
}

// ------------------------------------------------------------------
// Event history
// ------------------------------------------------------------------

// AddEvent adds an event to the task's event history. Returns the sequence number.
func (s *Store) AddEvent(ctx context.Context, taskID string, eventType EventType, data bson.M) (int, error) {
	// Atomic sequence increment
	counterRes := s.taskCounters.FindOneAndUpdate(ctx,
		bson.M{"task_id": taskID},
		bson.M{"$inc": bson.M{"sequence": 1}},
		options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After),
	)
	var counter TaskCounterDoc
	seq := 1
	if err := counterRes.Decode(&counter); err != nil {
		// Upsert created the doc but returned nil pre-image. Re-read.
		_ = s.taskCounters.FindOne(ctx, bson.M{"task_id": taskID}).Decode(&counter)
	}
	seq = counter.Sequence
	if seq == 0 {
		// Fallback: insert manually
		counter = TaskCounterDoc{TaskID: taskID, Sequence: 1}
		_, _ = s.taskCounters.InsertOne(ctx, counter)
		seq = 1
	}

	event := TaskEventDoc{
		TaskID:    taskID,
		Sequence:  seq,
		EventType: eventType,
		Timestamp: time.Now().UTC(),
		Actor:     s.agentName,
		Data:      data,
	}
	if _, err := s.taskEvents.InsertOne(ctx, event); err != nil {
		return 0, err
	}
	return seq, nil
}

// GetEvents returns all events for a task, ordered by sequence.
func (s *Store) GetEvents(ctx context.Context, taskID string, limit int) ([]TaskEventDoc, error) {
	filter := bson.M{"task_id": taskID, "deleted_at": bson.M{"$exists": false}}
	findOpts := options.Find().SetSort(bson.D{{Key: "sequence", Value: 1}})
	if limit > 0 {
		findOpts.SetLimit(int64(limit))
	}
	cur, err := s.taskEvents.Find(ctx, filter, findOpts)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var docs []TaskEventDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	return docs, nil
}

// ------------------------------------------------------------------
// Atomic counter
// ------------------------------------------------------------------

// AtomicCounter increments and returns the next sequence number for a task.
func (s *Store) AtomicCounter(ctx context.Context, taskID string) (int, error) {
	counterRes := s.taskCounters.FindOneAndUpdate(ctx,
		bson.M{"task_id": taskID},
		bson.M{"$inc": bson.M{"sequence": 1}},
		options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After),
	)
	var counter TaskCounterDoc
	if err := counterRes.Decode(&counter); err != nil {
		counter = TaskCounterDoc{TaskID: taskID, Sequence: 1}
		if _, err := s.taskCounters.InsertOne(ctx, counter); err != nil {
			return 0, err
		}
		return 1, nil
	}
	return counter.Sequence, nil
}

// ------------------------------------------------------------------
// DispatchRequest CRUD
// ------------------------------------------------------------------

// CreateDispatchRequest creates a new dispatch request.
func (s *Store) CreateDispatchRequest(ctx context.Context, req DispatchRequestDoc) (string, error) {
	now := time.Now().UTC()
	req.CreatedAt = now
	req.UpdatedAt = now
	if req.RequestID == "" {
		req.RequestID = generateRequestID()
	}
	if req.Status == "" {
		req.Status = "PENDING"
	}
	if _, err := s.dispatchRequests.InsertOne(ctx, req); err != nil {
		return "", err
	}
	return req.RequestID, nil
}

// GetDispatchRequest retrieves a dispatch request by request_id.
func (s *Store) GetDispatchRequest(ctx context.Context, requestID string) (*DispatchRequestDoc, error) {
	var doc DispatchRequestDoc
	if err := s.dispatchRequests.FindOne(ctx, bson.M{"request_id": requestID, "deleted_at": bson.M{"$exists": false}}).Decode(&doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

// UpdateDispatchRequest patches a dispatch request.
func (s *Store) UpdateDispatchRequest(ctx context.Context, requestID string, updates bson.M) (*mongo.UpdateResult, error) {
	updates["updated_at"] = time.Now().UTC()
	return s.dispatchRequests.UpdateOne(ctx, bson.M{"request_id": requestID}, bson.M{"$set": updates})
}

// DeleteDispatchRequest soft-deletes a dispatch request.
func (s *Store) DeleteDispatchRequest(ctx context.Context, requestID string) (*mongo.UpdateResult, error) {
	return s.dispatchRequests.UpdateOne(ctx,
		bson.M{"request_id": requestID},
		bson.M{"$set": bson.M{"deleted_at": time.Now().UTC()}},
	)
}

// ListDispatchRequests lists dispatch requests with optional filters.
type ListDispatchRequestOptions struct {
	Status    string
	TaskID    string
	AgentName string
	Limit     int
}

// ListDispatchRequests returns dispatch requests matching filters.
func (s *Store) ListDispatchRequests(ctx context.Context, opts ListDispatchRequestOptions) ([]DispatchRequestDoc, error) {
	filter := bson.M{"deleted_at": bson.M{"$exists": false}}
	if opts.Status != "" {
		filter["status"] = opts.Status
	}
	if opts.TaskID != "" {
		filter["task_id"] = opts.TaskID
	}
	if opts.AgentName != "" {
		filter["agent_name"] = opts.AgentName
	}
	findOpts := options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}})
	if opts.Limit > 0 {
		findOpts.SetLimit(int64(opts.Limit))
	}
	cur, err := s.dispatchRequests.Find(ctx, filter, findOpts)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var docs []DispatchRequestDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	return docs, nil
}

// ------------------------------------------------------------------
// DispatchSpawn CRUD
// ------------------------------------------------------------------

// CreateDispatchSpawn creates a new dispatch spawn record.
func (s *Store) CreateDispatchSpawn(ctx context.Context, spawn DispatchSpawnDoc) (string, error) {
	now := time.Now().UTC()
	spawn.StartedAt = now
	if spawn.SpawnID == "" {
		spawn.SpawnID = generateSpawnID()
	}
	if spawn.Status == "" {
		spawn.Status = "RUNNING"
	}
	if _, err := s.dispatchSpawns.InsertOne(ctx, spawn); err != nil {
		return "", err
	}
	return spawn.SpawnID, nil
}

// GetDispatchSpawn retrieves a dispatch spawn by spawn_id.
func (s *Store) GetDispatchSpawn(ctx context.Context, spawnID string) (*DispatchSpawnDoc, error) {
	var doc DispatchSpawnDoc
	if err := s.dispatchSpawns.FindOne(ctx, bson.M{"spawn_id": spawnID, "deleted_at": bson.M{"$exists": false}}).Decode(&doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

// UpdateDispatchSpawn patches a dispatch spawn.
func (s *Store) UpdateDispatchSpawn(ctx context.Context, spawnID string, updates bson.M) (*mongo.UpdateResult, error) {
	updates["updated_at"] = time.Now().UTC()
	return s.dispatchSpawns.UpdateOne(ctx, bson.M{"spawn_id": spawnID}, bson.M{"$set": updates})
}

// DeleteDispatchSpawn soft-deletes a dispatch spawn.
func (s *Store) DeleteDispatchSpawn(ctx context.Context, spawnID string) (*mongo.UpdateResult, error) {
	return s.dispatchSpawns.UpdateOne(ctx,
		bson.M{"spawn_id": spawnID},
		bson.M{"$set": bson.M{"deleted_at": time.Now().UTC()}},
	)
}

// ListDispatchSpawns lists dispatch spawns with optional filters.
type ListDispatchSpawnOptions struct {
	Status string
	TaskID string
	Limit  int
}

// ListDispatchSpawns returns dispatch spawns matching filters.
func (s *Store) ListDispatchSpawns(ctx context.Context, opts ListDispatchSpawnOptions) ([]DispatchSpawnDoc, error) {
	filter := bson.M{"deleted_at": bson.M{"$exists": false}}
	if opts.Status != "" {
		filter["status"] = opts.Status
	}
	if opts.TaskID != "" {
		filter["task_id"] = opts.TaskID
	}
	findOpts := options.Find().SetSort(bson.D{{Key: "started_at", Value: 1}})
	if opts.Limit > 0 {
		findOpts.SetLimit(int64(opts.Limit))
	}
	cur, err := s.dispatchSpawns.Find(ctx, filter, findOpts)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var docs []DispatchSpawnDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	return docs, nil
}

// ------------------------------------------------------------------
// Query helpers
// ------------------------------------------------------------------

// CountRunning counts tasks with status=RUNNING.
func (s *Store) CountRunning(ctx context.Context, assignee string) (int, error) {
	filter := bson.M{"status": TaskStatusRunning, "deleted_at": bson.M{"$exists": false}}
	if assignee != "" {
		filter["assignee"] = assignee
	}
	count, err := s.tasks.CountDocuments(ctx, filter)
	if err != nil {
		return 0, err
	}
	return int(count), nil
}

// CountByStatus counts tasks grouped by status.
func (s *Store) CountByStatus(ctx context.Context, assignee string) (map[string]int, error) {
	pipeline := mongo.Pipeline{}
	if assignee != "" {
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: bson.M{"assignee": assignee, "deleted_at": bson.M{"$exists": false}}}})
	} else {
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: bson.M{"deleted_at": bson.M{"$exists": false}}}})
	}
	pipeline = append(pipeline, bson.D{{Key: "$group", Value: bson.M{"_id": "$status", "count": bson.M{"$sum": 1}}}})

	cur, err := s.tasks.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)

	result := make(map[string]int)
	for cur.Next(ctx) {
		var doc struct {
			ID    string `bson:"_id"`
			Count int    `bson:"count"`
		}
		if err := cur.Decode(&doc); err != nil {
			continue
		}
		result[doc.ID] = doc.Count
	}
	return result, nil
}

// AreDependenciesMet checks if all dependencies for a task are met.
func (s *Store) AreDependenciesMet(ctx context.Context, taskID string) (bool, error) {
	task, err := s.GetTask(ctx, taskID)
	if err != nil {
		return false, err
	}
	for _, depID := range task.DependsOn {
		dep, err := s.GetTask(ctx, depID)
		if err != nil || dep == nil {
			return false, nil
		}
		if dep.Status != TaskStatusCompleted && dep.Status != TaskStatusSkipped {
			return false, nil
		}
	}
	for _, planID := range task.DependsOnPlans {
		if !s.IsPlanComplete(ctx, planID) {
			return false, nil
		}
	}
	return true, nil
}

// IsPlanComplete returns true when all non-phase-parent tasks in a plan are terminal.
func (s *Store) IsPlanComplete(ctx context.Context, planID string) bool {
	doneStates := []TaskStatus{TaskStatusCompleted, TaskStatusSkipped, TaskStatusCancelled}
	total, err := s.tasks.CountDocuments(ctx, bson.M{
		"plan_id":    planID,
		"labels":     bson.M{"$ne": "phase-parent"},
		"deleted_at": bson.M{"$exists": false},
	})
	if err != nil || total == 0 {
		return false
	}
	statuses := make([]string, len(doneStates))
	for i, st := range doneStates {
		statuses[i] = string(st)
	}
	openCount, err := s.tasks.CountDocuments(ctx, bson.M{
		"plan_id":    planID,
		"labels":     bson.M{"$ne": "phase-parent"},
		"status":     bson.M{"$nin": statuses},
		"deleted_at": bson.M{"$exists": false},
	})
	if err != nil {
		return false
	}
	return openCount == 0
}

// ------------------------------------------------------------------
// Plan decomposition
// ------------------------------------------------------------------

// DecomposePlan breaks a plan into leaf tasks with phase metadata.
// This is a simplified port of the Python decompose_plan function.
func (s *Store) DecomposePlan(ctx context.Context, planID string, agentName string) ([]string, error) {
	if agentName == "" {
		agentName = s.agentName
	}

	// In the Go version, we accept the plan content directly via the context
	// or the caller should provide parsed phases. This avoids importing planstore.
	// For now, return an error indicating the caller should use CreateTask directly
	return nil, fmt.Errorf("DecomposePlan: plan parsing not yet implemented in Go; use CreateTask with phase metadata directly")
}

// BulkCreateTasks creates multiple tasks at once. Returns list of task_ids.
func (s *Store) BulkCreateTasks(ctx context.Context, tasksData []bson.M) ([]string, error) {
	ids := make([]string, 0, len(tasksData))
	for _, td := range tasksData {
		tid, err := s.CreateTask(ctx, td)
		if err != nil {
			return nil, err
		}
		ids = append(ids, tid)
	}
	return ids, nil
}

// ------------------------------------------------------------------
// Soft-delete helpers
// ------------------------------------------------------------------

func generateTaskID() string {
	return fmt.Sprintf("t_%s", generateHex(7))
}

func generateRequestID() string {
	return fmt.Sprintf("dr_%s", generateHex(7))
}

func generateSpawnID() string {
	return fmt.Sprintf("ds_%s", generateHex(7))
}

func generateHex(n int) string {
	const hexChars = "0123456789abcdef"
	b := make([]byte, n)
	for i := range b {
		// Simple deterministic ID generation; replace with crypto/rand in production
		b[i] = hexChars[time.Now().UnixNano()%int64(len(hexChars))]
	}
	return string(b)
}

func getString(m bson.M, key string, fallback ...string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	if len(fallback) > 0 {
		return fallback[0]
	}
	return ""
}

func getInt(m bson.M, key string, fallback int) int {
	if v, ok := m[key]; ok {
		switch n := v.(type) {
		case int:
			return n
		case int32:
			return int(n)
		case int64:
			return int(n)
		case float64:
			return int(n)
		}
	}
	return fallback
}

func getStringSlice(m bson.M, key string) []string {
	if v, ok := m[key]; ok {
		if sl, ok := v.([]string); ok {
			return sl
		}
		if sl, ok := v.([]interface{}); ok {
			result := make([]string, len(sl))
			for i, item := range sl {
				if s, ok := item.(string); ok {
					result[i] = s
				}
			}
			return result
		}
	}
	return []string{}
}

func getRetryConfig(m bson.M) RetryConfig {
	if v, ok := m["retry_config"]; ok {
		if cfg, ok := v.(RetryConfig); ok {
			return cfg
		}
		if bm, ok := v.(bson.M); ok {
			return RetryConfig{
				Backoff:         getString(bm, "backoff", "exponential"),
				InitialDelaySec: getInt(bm, "initial_delay_seconds", 30),
				MaxDelaySec:     getInt(bm, "max_delay_seconds", 300),
				Multiplier:      getInt(bm, "multiplier", 2),
			}
		}
	}
	return DefaultRetryConfig
}

func getFailureContext(m bson.M) FailureContext {
	if v, ok := m["failure_context"]; ok {
		if fc, ok := v.(FailureContext); ok {
			return fc
		}
		if bm, ok := v.(bson.M); ok {
			return FailureContext{
				NotifyChannel:  getString(bm, "notify_channel"),
				IncludeLogs:    true,
				IncludeSummary: true,
			}
		}
	}
	return DefaultFailureContext
}
