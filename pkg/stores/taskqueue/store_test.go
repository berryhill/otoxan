package taskqueue

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/mongodb"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// setupMongo spins up a testcontainers MongoDB container and returns a client.
func setupMongo(t *testing.T) *mongo.Client {
	t.Helper()
	ctx := context.Background()

	container, err := mongodb.Run(ctx, "mongo:7")
	if err != nil {
		t.Fatalf("failed to start mongodb container: %v", err)
	}
	t.Cleanup(func() {
		_ = container.Terminate(ctx)
	})

	uri, err := container.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("failed to get connection string: %v", err)
	}
	os.Setenv("MONGO_URI", uri)

	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("failed to connect to mongodb: %v", err)
	}
	t.Cleanup(func() {
		_ = client.Disconnect(ctx)
	})

	return client
}

func newTestStore(t *testing.T, client *mongo.Client, agentName string) *Store {
	t.Helper()
	store, err := NewStore(client, agentName)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	return store
}

// ------------------------------------------------------------------
// CreateTask / GetTask / ListTasks
// ------------------------------------------------------------------

func TestStore_CreateAndGetTask(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "test-agent")

	taskID, err := store.CreateTask(ctx, bson.M{
		"title":       "Test task",
		"description": "A test task",
		"priority":    1,
	})
	require.NoError(t, err)
	require.NotEmpty(t, taskID)
	assert.True(t, len(taskID) > 3)

	got, err := store.GetTask(ctx, taskID)
	require.NoError(t, err)
	assert.Equal(t, taskID, got.TaskID)
	assert.Equal(t, "Test task", got.Title)
	assert.Equal(t, "A test task", got.Description)
	assert.Equal(t, 1, got.Priority)
	assert.Equal(t, TaskStatusQueued, got.Status)
	assert.Equal(t, TaskTypeInternal, got.Type)
	assert.Equal(t, "test-agent", got.Assignee)
	assert.Equal(t, "agent", got.AssigneeType)
	assert.Equal(t, 0, got.Attempts)
	assert.Equal(t, 3, got.MaxRetries)
	assert.NotZero(t, got.CreatedAt)
}

func TestStore_CreateTask_WithAllFields(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "test-agent")

	now := time.Now().UTC()
	taskID, err := store.CreateTask(ctx, bson.M{
		"title":          "Full task",
		"description":    "desc",
		"type":           string(TaskTypeGitHubIssue),
		"status":         string(TaskStatusDraft),
		"priority":       0,
		"assignee":       "archer",
		"assignee_type":  "human",
		"assignee_id":    "user_123",
		"max_retries":    5,
		"depends_on":     []string{"t_dep1"},
		"depends_on_plans": []string{"plan_1"},
		"plan_id":        "plan_abc",
		"epic_id":        "Phase 1",
		"phase":          "Phase 1",
		"phase_order":    0,
		"directive":      "agentic-coding",
		"scheduled_for":  now.Add(time.Hour),
		"scheduled_reason": "wait for upstream",
		"parallel_group": "group_a",
		"verification":   "tests pass",
		"intent":         "implement auth",
		"implementation": "step 1, step 2",
		"references":     "DS-9",
		"plan_goal":      "secure the app",
		"plan_context":   "auth is broken",
		"phase_context":  "setup phase",
		"parent_provider": "kimi",
		"initiative_id":  "init_1",
		"labels":         []string{"urgent"},
		"retry_config": bson.M{
			"backoff":                  "linear",
			"initial_delay_seconds":    60,
			"max_delay_seconds":        600,
			"multiplier":               3,
		},
		"failure_pattern": "notify_and_continue",
		"failure_context": bson.M{
			"notify_channel":  "#alerts",
			"include_logs":    false,
			"include_summary": false,
		},
	})
	require.NoError(t, err)

	got, err := store.GetTask(ctx, taskID)
	require.NoError(t, err)
	assert.Equal(t, "Full task", got.Title)
	assert.Equal(t, TaskTypeGitHubIssue, got.Type)
	assert.Equal(t, TaskStatusDraft, got.Status)
	assert.Equal(t, 0, got.Priority)
	assert.Equal(t, "archer", got.Assignee)
	assert.Equal(t, "human", got.AssigneeType)
	assert.Equal(t, "user_123", *got.AssigneeID)
	assert.Equal(t, 5, got.MaxRetries)
	assert.Equal(t, []string{"t_dep1"}, got.DependsOn)
	assert.Equal(t, []string{"plan_1"}, got.DependsOnPlans)
	assert.Equal(t, "plan_abc", *got.PlanID)
	assert.Equal(t, "Phase 1", *got.EpicID)
	assert.Equal(t, "agentic-coding", *got.Directive)
	assert.Equal(t, "group_a", *got.ParallelGroup)
	assert.Equal(t, "tests pass", *got.Verification)
	assert.Equal(t, "implement auth", got.Intent)
	assert.Equal(t, "step 1, step 2", got.Implementation)
	assert.Equal(t, "DS-9", got.References)
	assert.Equal(t, "secure the app", got.PlanGoal)
	assert.Equal(t, "auth is broken", got.PlanContext)
	assert.Equal(t, "setup phase", got.PhaseContext)
	assert.Equal(t, "kimi", *got.ParentProvider)
	assert.Equal(t, "init_1", *got.InitiativeID)
	assert.Equal(t, []string{"urgent"}, got.Labels)
	assert.Equal(t, "linear", got.RetryConfig.Backoff)
	assert.Equal(t, 60, got.RetryConfig.InitialDelaySec)
	assert.Equal(t, 600, got.RetryConfig.MaxDelaySec)
	assert.Equal(t, 3, got.RetryConfig.Multiplier)
	assert.Equal(t, "notify_and_continue", got.FailurePattern)
	assert.Equal(t, "#alerts", got.FailureContext.NotifyChannel)
	assert.True(t, got.FailureContext.IncludeLogs)
	assert.True(t, got.FailureContext.IncludeSummary)
}

func TestStore_CreateTask_InvalidAssigneeType(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "test-agent")

	_, err := store.CreateTask(ctx, bson.M{
		"title":         "Bad",
		"assignee_type": "robot",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "assignee_type")
}

func TestStore_ListTasks(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "test-agent")

	// Create tasks with different statuses
	id1, _ := store.CreateTask(ctx, bson.M{"title": "A", "status": string(TaskStatusQueued), "priority": 1})
	id2, _ := store.CreateTask(ctx, bson.M{"title": "B", "status": string(TaskStatusRunning), "priority": 2})
	id3, _ := store.CreateTask(ctx, bson.M{"title": "C", "status": string(TaskStatusCompleted), "priority": 0})
	_ = id2
	_ = id3

	// List all
	all, err := store.ListTasks(ctx, ListTasksOptions{Limit: 10})
	require.NoError(t, err)
	assert.Len(t, all, 3)

	// List by status
	queued, err := store.ListTasks(ctx, ListTasksOptions{Status: []TaskStatus{TaskStatusQueued}, Limit: 10})
	require.NoError(t, err)
	require.Len(t, queued, 1)
	assert.Equal(t, id1, queued[0].TaskID)

	// List by multiple statuses
	mixed, err := store.ListTasks(ctx, ListTasksOptions{Status: []TaskStatus{TaskStatusQueued, TaskStatusRunning}, Limit: 10})
	require.NoError(t, err)
	assert.Len(t, mixed, 2)

	// List sorted by priority
	sorted, err := store.ListTasks(ctx, ListTasksOptions{Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, "C", sorted[0].Title) // priority 0
	assert.Equal(t, "A", sorted[1].Title) // priority 1
	assert.Equal(t, "B", sorted[2].Title) // priority 2
}

func TestStore_UpdateTask(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "test-agent")

	taskID, _ := store.CreateTask(ctx, bson.M{"title": "Before"})
	res, err := store.UpdateTask(ctx, taskID, bson.M{"title": "After"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), res.ModifiedCount)

	got, err := store.GetTask(ctx, taskID)
	require.NoError(t, err)
	assert.Equal(t, "After", got.Title)
	assert.True(t, got.UpdatedAt.After(got.CreatedAt) || got.UpdatedAt.Equal(got.CreatedAt))
}

func TestStore_DeleteAndHardDeleteTask(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "test-agent")

	taskID, _ := store.CreateTask(ctx, bson.M{"title": "ToDelete"})

	// Soft delete
	dres, err := store.DeleteTask(ctx, taskID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), dres.ModifiedCount)

	_, err = store.GetTask(ctx, taskID)
	assert.Equal(t, mongo.ErrNoDocuments, err)

	// Hard delete
	taskID2, _ := store.CreateTask(ctx, bson.M{"title": "ToHardDelete"})
	hres, err := store.HardDeleteTask(ctx, taskID2)
	require.NoError(t, err)
	assert.Equal(t, int64(1), hres.DeletedCount)

	_, err = store.GetTask(ctx, taskID2)
	assert.Equal(t, mongo.ErrNoDocuments, err)
}

// ------------------------------------------------------------------
// ClaimTask / MarkRunning / MarkCompleted / MarkFailed / MarkRetried
// ------------------------------------------------------------------

func TestStore_ClaimTask(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "test-agent")

	// No tasks → nothing to claim
	claimed, err := store.ClaimTask(ctx, "test-agent", 5)
	require.NoError(t, err)
	assert.Nil(t, claimed)

	// Create a task and claim it
	taskID, _ := store.CreateTask(ctx, bson.M{"title": "Claim me"})
	claimed, err = store.ClaimTask(ctx, "test-agent", 5)
	require.NoError(t, err)
	require.NotNil(t, claimed)
	assert.Equal(t, taskID, claimed.TaskID)
	assert.Equal(t, TaskStatusClaimed, claimed.Status)
	assert.Equal(t, "test-agent", claimed.Assignee)
	assert.NotNil(t, claimed.ClaimedAt)

	// Already claimed → no more runnable
	claimed2, err := store.ClaimTask(ctx, "test-agent", 5)
	require.NoError(t, err)
	assert.Nil(t, claimed2)
}

func TestStore_ClaimTask_RespectsConcurrencyLimit(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "test-agent")

	store.CreateTask(ctx, bson.M{"title": "T1"})
	store.CreateTask(ctx, bson.M{"title": "T2"})

	// Claim first with limit=1
	claimed1, err := store.ClaimTask(ctx, "test-agent", 1)
	require.NoError(t, err)
	require.NotNil(t, claimed1)

	// Mark it RUNNING so it counts against the limit
	store.MarkRunning(ctx, claimed1.TaskID, "ses_1")

	// Second claim blocked by concurrency limit (1 running)
	claimed2, err := store.ClaimTask(ctx, "test-agent", 1)
	require.NoError(t, err)
	assert.Nil(t, claimed2)
}

func TestStore_ClaimTask_RespectsDependencies(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "test-agent")

	depID, _ := store.CreateTask(ctx, bson.M{"title": "Dep"})
	store.CreateTask(ctx, bson.M{"title": "Dependent", "depends_on": []string{depID}})

	// Dependent not runnable because dep not completed — only dep is claimable
	claimed, err := store.ClaimTask(ctx, "test-agent", 5)
	require.NoError(t, err)
	require.NotNil(t, claimed)
	assert.Equal(t, depID, claimed.TaskID) // dep is claimed first

	// Complete dep, then dependent becomes runnable
	store.MarkRunning(ctx, depID, "ses_1")
	store.MarkCompleted(ctx, depID, "done", nil)
	claimed2, err := store.ClaimTask(ctx, "test-agent", 5)
	require.NoError(t, err)
	require.NotNil(t, claimed2)
	assert.Equal(t, "Dependent", claimed2.Title)
}

func TestStore_ClaimTask_RespectsScheduledFor(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "test-agent")

	future := time.Now().UTC().Add(time.Hour)
	store.CreateTask(ctx, bson.M{"title": "Future", "scheduled_for": future})
	nowID, _ := store.CreateTask(ctx, bson.M{"title": "Now"})

	claimed, err := store.ClaimTask(ctx, "test-agent", 5)
	require.NoError(t, err)
	require.NotNil(t, claimed)
	assert.Equal(t, nowID, claimed.TaskID)
}

func TestStore_MarkRunning(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "test-agent")

	taskID, _ := store.CreateTask(ctx, bson.M{"title": "Run me"})
	store.ClaimTask(ctx, "test-agent", 5)

	ok, err := store.MarkRunning(ctx, taskID, "ses_123")
	require.NoError(t, err)
	assert.True(t, ok)

	got, _ := store.GetTask(ctx, taskID)
	assert.Equal(t, TaskStatusRunning, got.Status)
	assert.Equal(t, 1, got.Attempts)
	assert.NotNil(t, got.StartedAt)
}

func TestStore_MarkRunning_NotClaimed(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "test-agent")

	taskID, _ := store.CreateTask(ctx, bson.M{"title": "Run me"})
	// Skip claim
	ok, err := store.MarkRunning(ctx, taskID, "ses_123")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestStore_MarkCompleted(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "test-agent")

	taskID, _ := store.CreateTask(ctx, bson.M{"title": "Complete me"})
	store.ClaimTask(ctx, "test-agent", 5)
	store.MarkRunning(ctx, taskID, "ses_123")

	arts := []Artifact{{Name: "log", Type: "text", URL: "/tmp/log"}}
	ok, err := store.MarkCompleted(ctx, taskID, "all done", arts)
	require.NoError(t, err)
	assert.True(t, ok)

	got, _ := store.GetTask(ctx, taskID)
	assert.Equal(t, TaskStatusCompleted, got.Status)
	assert.Equal(t, "all done", *got.Output)
	assert.Len(t, got.Artifacts, 1)
	assert.NotNil(t, got.CompletedAt)
}

func TestStore_MarkFailed_WithRetry(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "test-agent")

	taskID, _ := store.CreateTask(ctx, bson.M{"title": "Fail me"})
	store.ClaimTask(ctx, "test-agent", 5)
	store.MarkRunning(ctx, taskID, "ses_123")

	ok, err := store.MarkFailed(ctx, taskID, "something broke", true)
	require.NoError(t, err)
	assert.True(t, ok)

	got, _ := store.GetTask(ctx, taskID)
	assert.Equal(t, TaskStatusQueued, got.Status) // re-queued for retry
}

func TestStore_MarkFailed_Final(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "test-agent")

	taskID, _ := store.CreateTask(ctx, bson.M{"title": "Fail me"})
	store.ClaimTask(ctx, "test-agent", 5)
	store.MarkRunning(ctx, taskID, "ses_123")

	ok, err := store.MarkFailed(ctx, taskID, "something broke", false)
	require.NoError(t, err)
	assert.True(t, ok)

	got, _ := store.GetTask(ctx, taskID)
	assert.Equal(t, TaskStatusFailed, got.Status)
	assert.Equal(t, "something broke", *got.Output)
	assert.NotNil(t, got.CompletedAt)
}

func TestStore_MarkRetried(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "test-agent")

	taskID, _ := store.CreateTask(ctx, bson.M{"title": "Retry me"})
	store.ClaimTask(ctx, "test-agent", 5)
	store.MarkRunning(ctx, taskID, "ses_123")
	store.MarkFailed(ctx, taskID, "error", false)

	ok, err := store.MarkRetried(ctx, taskID, "manual retry", "human")
	require.NoError(t, err)
	assert.True(t, ok)

	got, _ := store.GetTask(ctx, taskID)
	assert.Equal(t, TaskStatusQueued, got.Status)
	assert.Equal(t, 0, got.Attempts)
	assert.Nil(t, got.StartedAt)
	assert.Nil(t, got.ClaimedAt)
	assert.Nil(t, got.CompletedAt)
	assert.Nil(t, got.Output)
}

// ------------------------------------------------------------------
// AddEvent / GetEvents / AtomicCounter
// ------------------------------------------------------------------

func TestStore_AddEvent(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "test-agent")

	// Create a task in DRAFT — CreateTask always emits TASK_CREATED
	taskID, _ := store.CreateTask(ctx, bson.M{"title": "Event task", "status": string(TaskStatusDraft)})

	// The task creation already emitted TASK_CREATED with sequence 1
	seq2, err := store.AddEvent(ctx, taskID, EventTypeTaskQueued, bson.M{"session": "s1"})
	require.NoError(t, err)
	assert.Equal(t, 2, seq2)

	// Get events
	events, err := store.GetEvents(ctx, taskID, 10)
	require.NoError(t, err)
	assert.Len(t, events, 2)
	assert.Equal(t, EventTypeTaskCreated, events[0].EventType)
	assert.Equal(t, EventTypeTaskQueued, events[1].EventType)
	assert.Equal(t, "test-agent", events[0].Actor)
}

func TestStore_AtomicCounter(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "test-agent")

	// Create a task in DRAFT — CreateTask always emits TASK_CREATED which creates the counter
	taskID, _ := store.CreateTask(ctx, bson.M{"title": "Counter task", "status": string(TaskStatusDraft)})

	// Counter already at 1 from the TASK_CREATED event emitted by CreateTask
	c2, err := store.AtomicCounter(ctx, taskID)
	require.NoError(t, err)
	assert.Equal(t, 2, c2)

	c3, err := store.AtomicCounter(ctx, taskID)
	require.NoError(t, err)
	assert.Equal(t, 3, c3)

	c4, err := store.AtomicCounter(ctx, taskID)
	require.NoError(t, err)
	assert.Equal(t, 4, c4)
}

// ------------------------------------------------------------------
// DispatchRequest CRUD
// ------------------------------------------------------------------

func TestStore_DispatchRequestCRUD(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "test-agent")

	// Create
	reqID, err := store.CreateDispatchRequest(ctx, DispatchRequestDoc{
		TaskID:    "t_123",
		AgentName: "test-agent",
		Status:    "PENDING",
	})
	require.NoError(t, err)
	require.NotEmpty(t, reqID)

	// Get
	got, err := store.GetDispatchRequest(ctx, reqID)
	require.NoError(t, err)
	assert.Equal(t, reqID, got.RequestID)
	assert.Equal(t, "t_123", got.TaskID)
	assert.Equal(t, "PENDING", got.Status)

	// Update
	ures, err := store.UpdateDispatchRequest(ctx, reqID, bson.M{"status": "CLAIMED"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), ures.ModifiedCount)

	got, _ = store.GetDispatchRequest(ctx, reqID)
	assert.Equal(t, "CLAIMED", got.Status)

	// List
	list, err := store.ListDispatchRequests(ctx, ListDispatchRequestOptions{Status: "CLAIMED", Limit: 10})
	require.NoError(t, err)
	assert.Len(t, list, 1)

	// Delete
	dres, err := store.DeleteDispatchRequest(ctx, reqID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), dres.ModifiedCount)

	_, err = store.GetDispatchRequest(ctx, reqID)
	assert.Equal(t, mongo.ErrNoDocuments, err)
}

// ------------------------------------------------------------------
// DispatchSpawn CRUD
// ------------------------------------------------------------------

func TestStore_DispatchSpawnCRUD(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "test-agent")

	// Create
	spawnID, err := store.CreateDispatchSpawn(ctx, DispatchSpawnDoc{
		TaskID:    "t_123",
		PID:       12345,
		SessionID: "ses_abc",
		AgentName: "test-agent",
	})
	require.NoError(t, err)
	require.NotEmpty(t, spawnID)

	// Get
	got, err := store.GetDispatchSpawn(ctx, spawnID)
	require.NoError(t, err)
	assert.Equal(t, spawnID, got.SpawnID)
	assert.Equal(t, 12345, got.PID)
	assert.Equal(t, "RUNNING", got.Status)

	// Update
	ures, err := store.UpdateDispatchSpawn(ctx, spawnID, bson.M{"status": "COMPLETED", "exit_code": 0})
	require.NoError(t, err)
	assert.Equal(t, int64(1), ures.ModifiedCount)

	got, _ = store.GetDispatchSpawn(ctx, spawnID)
	assert.Equal(t, "COMPLETED", got.Status)
	assert.Equal(t, 0, *got.ExitCode)

	// List
	list, err := store.ListDispatchSpawns(ctx, ListDispatchSpawnOptions{TaskID: "t_123", Limit: 10})
	require.NoError(t, err)
	assert.Len(t, list, 1)

	// Delete
	dres, err := store.DeleteDispatchSpawn(ctx, spawnID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), dres.ModifiedCount)

	_, err = store.GetDispatchSpawn(ctx, spawnID)
	assert.Equal(t, mongo.ErrNoDocuments, err)
}

// ------------------------------------------------------------------
// Query helpers
// ------------------------------------------------------------------

func TestStore_CountRunning(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "test-agent")

	store.CreateTask(ctx, bson.M{"title": "R1", "status": string(TaskStatusRunning)})
	store.CreateTask(ctx, bson.M{"title": "R2", "status": string(TaskStatusRunning)})
	store.CreateTask(ctx, bson.M{"title": "Q1", "status": string(TaskStatusQueued)})

	count, err := store.CountRunning(ctx, "")
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	count2, err := store.CountRunning(ctx, "test-agent")
	require.NoError(t, err)
	assert.Equal(t, 2, count2)
}

func TestStore_CountByStatus(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "test-agent")

	store.CreateTask(ctx, bson.M{"title": "Q1", "status": string(TaskStatusQueued)})
	store.CreateTask(ctx, bson.M{"title": "Q2", "status": string(TaskStatusQueued)})
	store.CreateTask(ctx, bson.M{"title": "R1", "status": string(TaskStatusRunning)})

	counts, err := store.CountByStatus(ctx, "")
	require.NoError(t, err)
	assert.Equal(t, 2, counts["QUEUED"])
	assert.Equal(t, 1, counts["RUNNING"])
}

func TestStore_AreDependenciesMet(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "test-agent")

	depID, _ := store.CreateTask(ctx, bson.M{"title": "Dep"})
	taskID, _ := store.CreateTask(ctx, bson.M{"title": "Main", "depends_on": []string{depID}})

	// Not met yet
	met, err := store.AreDependenciesMet(ctx, taskID)
	require.NoError(t, err)
	assert.False(t, met)

	// Complete dep
	store.ClaimTask(ctx, "test-agent", 5)
	store.MarkRunning(ctx, depID, "ses_1")
	store.MarkCompleted(ctx, depID, "done", nil)

	met, err = store.AreDependenciesMet(ctx, taskID)
	require.NoError(t, err)
	assert.True(t, met)
}

func TestStore_IsPlanComplete(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "test-agent")

	// Empty plan → not complete
	assert.False(t, store.IsPlanComplete(ctx, "plan_empty"))

	// Plan with tasks
	store.CreateTask(ctx, bson.M{"title": "T1", "plan_id": "plan_1", "status": string(TaskStatusCompleted)})
	store.CreateTask(ctx, bson.M{"title": "T2", "plan_id": "plan_1", "status": string(TaskStatusCompleted)})

	assert.True(t, store.IsPlanComplete(ctx, "plan_1"))

	// One task still queued
	store.CreateTask(ctx, bson.M{"title": "T3", "plan_id": "plan_2", "status": string(TaskStatusQueued)})
	assert.False(t, store.IsPlanComplete(ctx, "plan_2"))
}

// ------------------------------------------------------------------
// DecomposePlan
// ------------------------------------------------------------------

func TestStore_DecomposePlan_NotImplemented(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "test-agent")

	_, err := store.DecomposePlan(ctx, "plan_1", "test-agent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not yet implemented")
}

// ------------------------------------------------------------------
// Event emission on lifecycle transitions
// ------------------------------------------------------------------

func TestStore_LifecycleEvents(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "test-agent")

	taskID, _ := store.CreateTask(ctx, bson.M{"title": "Lifecycle"})
	events, _ := store.GetEvents(ctx, taskID, 10)
	assert.True(t, len(events) >= 1) // at least TASK_CREATED

	store.ClaimTask(ctx, "test-agent", 5)
	events, _ = store.GetEvents(ctx, taskID, 10)
	foundClaimed := false
	for _, e := range events {
		if e.EventType == EventTypeTaskClaimed {
			foundClaimed = true
		}
	}
	assert.True(t, foundClaimed)

	store.MarkRunning(ctx, taskID, "ses_1")
	events, _ = store.GetEvents(ctx, taskID, 10)
	foundStarted := false
	for _, e := range events {
		if e.EventType == EventTypeTaskStarted {
			foundStarted = true
		}
	}
	assert.True(t, foundStarted)

	store.MarkCompleted(ctx, taskID, "done", nil)
	events, _ = store.GetEvents(ctx, taskID, 10)
	foundCompleted := false
	for _, e := range events {
		if e.EventType == EventTypeTaskCompleted {
			foundCompleted = true
		}
	}
	assert.True(t, foundCompleted)
}

// ------------------------------------------------------------------
// Soft-delete isolation
// ------------------------------------------------------------------

func TestStore_SoftDelete_Isolation(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "test-agent")

	taskID, _ := store.CreateTask(ctx, bson.M{"title": "SoftDeleteMe"})
	store.DeleteTask(ctx, taskID)

	// Should not appear in normal queries
	_, err := store.GetTask(ctx, taskID)
	assert.Equal(t, mongo.ErrNoDocuments, err)

	all, _ := store.ListTasks(ctx, ListTasksOptions{Limit: 10})
	for _, td := range all {
		assert.NotEqual(t, taskID, td.TaskID)
	}

	// Events also soft-deleted
	events, _ := store.GetEvents(ctx, taskID, 10)
	assert.Empty(t, events)
}

// ------------------------------------------------------------------
// Bulk create
// ------------------------------------------------------------------

func TestStore_BulkCreateTasks(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "test-agent")

	ids, err := store.BulkCreateTasks(ctx, []bson.M{
		{"title": "Bulk1"},
		{"title": "Bulk2"},
		{"title": "Bulk3"},
	})
	require.NoError(t, err)
	assert.Len(t, ids, 3)

	for _, id := range ids {
		got, err := store.GetTask(ctx, id)
		require.NoError(t, err)
		assert.NotEmpty(t, got.Title)
	}
}
