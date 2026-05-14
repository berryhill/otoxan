// Package planstore provides a MongoDB-backed store for plan documents.
package planstore

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/silas/otoxan/pkg/stores/taskqueue"
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

func makeMinimalPlan(id, title string) *Plan {
	now := time.Now().UTC().Truncate(time.Millisecond)
	return &Plan{
		PlanID:    id,
		Title:     title,
		Status:    StatusPlanning,
		Owner:     "test-agent",
		CreatedAt: now,
		UpdatedAt: now,
		Content:   "",
		Tags:      []string{},
		PlanType:  TypeStandard,
	}
}

// ------------------------------------------------------------------
// CRUD tests
// ------------------------------------------------------------------

func TestStore_CreateAndGet(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "test-agent")

	plan := makeMinimalPlan("plan_001", "First Plan")
	plan.Content = "# Plan 001\n\nSome content."
	res, err := store.Create(ctx, plan)
	require.NoError(t, err)
	assert.NotNil(t, res.InsertedID)

	got, err := store.Get(ctx, "plan_001")
	require.NoError(t, err)
	assert.Equal(t, "plan_001", got.PlanID)
	assert.Equal(t, "First Plan", got.Title)
	assert.Equal(t, StatusPlanning, got.Status)
}

func TestStore_Update(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "test-agent")

	plan := makeMinimalPlan("plan_upd", "Update me")
	_, err := store.Create(ctx, plan)
	require.NoError(t, err)

	ures, err := store.Update(ctx, "plan_upd", bson.M{
		"status":  StatusExecuting,
		"content": "# Updated\n\nNew content.",
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), ures.ModifiedCount)

	got, err := store.Get(ctx, "plan_upd")
	require.NoError(t, err)
	assert.Equal(t, StatusExecuting, got.Status)
	assert.Equal(t, "# Updated\n\nNew content.", got.Content)
}

func TestStore_SoftDelete(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "test-agent")

	plan := makeMinimalPlan("plan_del", "Delete me")
	_, err := store.Create(ctx, plan)
	require.NoError(t, err)

	dres, err := store.Delete(ctx, "plan_del")
	require.NoError(t, err)
	assert.Equal(t, int64(1), dres.ModifiedCount)

	_, err = store.Get(ctx, "plan_del")
	assert.Equal(t, mongo.ErrNoDocuments, err)

	got, err := store.GetWithDeleted(ctx, "plan_del")
	require.NoError(t, err)
	assert.True(t, got.Deleted)
	assert.NotNil(t, got.DeletedAt)
}

func TestStore_Restore(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "test-agent")

	plan := makeMinimalPlan("plan_res", "Restore me")
	_, err := store.Create(ctx, plan)
	require.NoError(t, err)

	_, _ = store.Delete(ctx, "plan_res")

	rres, err := store.Restore(ctx, "plan_res")
	require.NoError(t, err)
	assert.Equal(t, int64(1), rres.ModifiedCount)

	got, err := store.Get(ctx, "plan_res")
	require.NoError(t, err)
	assert.False(t, got.Deleted)
	assert.Nil(t, got.DeletedAt)
}

func TestStore_HardDelete(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "test-agent")

	plan := makeMinimalPlan("plan_hard", "Hard delete me")
	_, err := store.Create(ctx, plan)
	require.NoError(t, err)

	dres, err := store.HardDelete(ctx, "plan_hard")
	require.NoError(t, err)
	assert.Equal(t, int64(1), dres.DeletedCount)

	_, err = store.GetWithDeleted(ctx, "plan_hard")
	assert.Equal(t, mongo.ErrNoDocuments, err)
}

// ------------------------------------------------------------------
// ListByStatus tests
// ------------------------------------------------------------------

func TestStore_ListByStatus(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "test-agent")

	plans := []*Plan{
		makeMinimalPlan("plan_l1", "Plan one"),
		makeMinimalPlan("plan_l2", "Plan two"),
		makeMinimalPlan("plan_l3", "Plan three"),
	}
	plans[0].Status = StatusPlanning
	plans[1].Status = StatusExecuting
	plans[2].Status = StatusPlanning

	for _, p := range plans {
		_, err := store.Create(ctx, p)
		require.NoError(t, err)
	}

	all, err := store.ListByStatus(ctx, "", 10)
	require.NoError(t, err)
	assert.Len(t, all, 3)

	executing, err := store.ListByStatus(ctx, StatusExecuting, 10)
	require.NoError(t, err)
	assert.Len(t, executing, 1)
	assert.Equal(t, "plan_l2", executing[0].PlanID)

	planning, err := store.ListByStatus(ctx, StatusPlanning, 10)
	require.NoError(t, err)
	assert.Len(t, planning, 2)
}

// ------------------------------------------------------------------
// ExtractTasks tests
// ------------------------------------------------------------------

func TestStore_ExtractTasks(t *testing.T) {
	client := setupMongo(t)
	store := newTestStore(t, client, "test-agent")

	content := `# Test Plan

### T1: Set up database
**Status:** PENDING
**Depends on:** (none)
**Assigned:** silas
**Tool:** psql
**Verify:** tables exist

### T2: Implement API
**Status:** RUNNING
**Depends on:** T1
**Assigned:** archer
**Tool:** go

### T3: Write tests
**Status:** PENDING
**Depends on:** T2
**Parent Provider:** kimi
`

	tasks := store.ExtractTasks(content)
	require.Len(t, tasks, 3)

	assert.Equal(t, "T1", tasks[0].ID)
	assert.Equal(t, "Set up database", tasks[0].Title)
	assert.Equal(t, "PENDING", tasks[0].Status)
	assert.Empty(t, tasks[0].DependsOn)
	assert.Equal(t, "silas", *tasks[0].Assigned)
	assert.Equal(t, "psql", *tasks[0].Tool)
	assert.Equal(t, "tables exist", *tasks[0].Verify)
	assert.Nil(t, tasks[0].ParentProvider)

	assert.Equal(t, "T2", tasks[1].ID)
	assert.Equal(t, "RUNNING", tasks[1].Status)
	assert.Equal(t, []string{"T1"}, tasks[1].DependsOn)
	assert.Equal(t, "archer", *tasks[1].Assigned)
	assert.Equal(t, "go", *tasks[1].Tool)

	assert.Equal(t, "T3", tasks[2].ID)
	assert.Equal(t, "PENDING", tasks[2].Status)
	assert.Equal(t, []string{"T2"}, tasks[2].DependsOn)
	assert.Equal(t, "kimi", *tasks[2].ParentProvider)
}

func TestStore_ExtractTasks_Empty(t *testing.T) {
	client := setupMongo(t)
	store := newTestStore(t, client, "test-agent")

	tasks := store.ExtractTasks("# No tasks here\n\nJust some text.")
	assert.Empty(t, tasks)
}

// ------------------------------------------------------------------
// GetExecutionStatus tests
// ------------------------------------------------------------------

func TestStore_GetExecutionStatus(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "test-agent")

	plan := makeMinimalPlan("plan_exec", "Exec Plan")
	plan.Status = StatusExecuting
	_, err := store.Create(ctx, plan)
	require.NoError(t, err)

	// Create some tasks for the plan
	tq := store.tasks
	_, err = tq.CreateTask(ctx, bson.M{
		"title":   "Task A",
		"plan_id": "plan_exec",
		"status":  string(taskqueue.TaskStatusCompleted),
	})
	require.NoError(t, err)
	_, err = tq.CreateTask(ctx, bson.M{
		"title":   "Task B",
		"plan_id": "plan_exec",
		"status":  string(taskqueue.TaskStatusRunning),
	})
	require.NoError(t, err)
	_, err = tq.CreateTask(ctx, bson.M{
		"title":   "Task C",
		"plan_id": "plan_exec",
		"status":  string(taskqueue.TaskStatusQueued),
	})
	require.NoError(t, err)

	status, err := store.GetExecutionStatus(ctx, "plan_exec")
	require.NoError(t, err)
	assert.Equal(t, "plan_exec", status.PlanID)
	assert.Equal(t, 3, status.Total)
	assert.Equal(t, 1, status.Completed)
	assert.Equal(t, 1, status.Running)
	assert.Equal(t, 1, status.Queued)
	assert.False(t, status.IsStuck)
}

func TestStore_GetExecutionStatus_Empty(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "test-agent")

	plan := makeMinimalPlan("plan_empty", "Empty Plan")
	_, err := store.Create(ctx, plan)
	require.NoError(t, err)

	status, err := store.GetExecutionStatus(ctx, "plan_empty")
	require.NoError(t, err)
	assert.Equal(t, 0, status.Total)
	assert.Equal(t, 0.0, status.PercentDone)
}

func TestStore_GetExecutionStatus_Stuck(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "test-agent")

	plan := makeMinimalPlan("plan_stuck", "Stuck Plan")
	plan.Status = StatusExecuting
	_, err := store.Create(ctx, plan)
	require.NoError(t, err)

	// Create a failed task but no running/queued
	tq := store.tasks
	_, err = tq.CreateTask(ctx, bson.M{
		"title":   "Task F",
		"plan_id": "plan_stuck",
		"status":  string(taskqueue.TaskStatusFailed),
	})
	require.NoError(t, err)

	status, err := store.GetExecutionStatus(ctx, "plan_stuck")
	require.NoError(t, err)
	assert.True(t, status.IsStuck)
	assert.NotNil(t, status.StuckReason)
	assert.Equal(t, "all_tasks_failed", *status.StuckReason)
}

// ------------------------------------------------------------------
// FindStuck tests
// ------------------------------------------------------------------

func TestStore_FindStuck(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "test-agent")

	// Old EXECUTING plan with no running tasks
	plan := makeMinimalPlan("plan_stuck_old", "Old Stuck")
	plan.Status = StatusExecuting
	plan.UpdatedAt = time.Now().UTC().AddDate(0, 0, -5)
	_, err := store.Create(ctx, plan)
	require.NoError(t, err)

	// Create a failed task
	tq := store.tasks
	_, err = tq.CreateTask(ctx, bson.M{
		"title":   "Task X",
		"plan_id": "plan_stuck_old",
		"status":  string(taskqueue.TaskStatusFailed),
	})
	require.NoError(t, err)

	stuck, err := store.FindStuck(ctx, 3, 10)
	require.NoError(t, err)
	require.Len(t, stuck, 1)
	assert.Equal(t, "plan_stuck_old", stuck[0].Plan.PlanID)
	assert.Equal(t, "zero_running_zero_queued", stuck[0].StuckReason)
	assert.Equal(t, 1, stuck[0].QueueSummary.Total)
	assert.Equal(t, 1, stuck[0].QueueSummary.Failed)
}

func TestStore_FindStuck_RecentPlanIgnored(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "test-agent")

	// Recent EXECUTING plan — should NOT be stuck
	plan := makeMinimalPlan("plan_recent", "Recent")
	plan.Status = StatusExecuting
	plan.UpdatedAt = time.Now().UTC()
	_, err := store.Create(ctx, plan)
	require.NoError(t, err)

	stuck, err := store.FindStuck(ctx, 3, 10)
	require.NoError(t, err)
	assert.Empty(t, stuck)
}

// ------------------------------------------------------------------
// FindUndecomposed tests
// ------------------------------------------------------------------

func TestStore_FindUndecomposed(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "test-agent")

	// Plan with no tasks
	plan := makeMinimalPlan("plan_undecomp", "Undecomposed")
	_, err := store.Create(ctx, plan)
	require.NoError(t, err)

	// Another plan with a task
	plan2 := makeMinimalPlan("plan_with_task", "Has Task")
	_, err = store.Create(ctx, plan2)
	require.NoError(t, err)

	tq := store.tasks
	_, err = tq.CreateTask(ctx, bson.M{
		"title":   "Task Y",
		"plan_id": "plan_with_task",
		"status":  string(taskqueue.TaskStatusQueued),
	})
	require.NoError(t, err)

	undecomposed, err := store.FindUndecomposed(ctx, 10)
	require.NoError(t, err)
	require.Len(t, undecomposed, 1)
	assert.Equal(t, "plan_undecomp", undecomposed[0].Plan.PlanID)
	assert.Equal(t, 0, undecomposed[0].TaskCount)
}

// ------------------------------------------------------------------
// SyncProgress tests
// ------------------------------------------------------------------

func TestStore_SyncProgress(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "test-agent")

	content := `# Test Plan

### T1: Do thing
**Status:** PENDING
**Depends on:** (none)

### T2: Do other
**Status:** PENDING
**Depends on:** T1

**Progress:** 0/2 completed, 0 failed
`

	plan := makeMinimalPlan("plan_sync", "Sync Plan")
	plan.Content = content
	_, err := store.Create(ctx, plan)
	require.NoError(t, err)

	// Create tasks in queue
	tq := store.tasks
	task1, err := tq.CreateTask(ctx, bson.M{
		"title":   "Do thing",
		"plan_id": "plan_sync",
		"status":  string(taskqueue.TaskStatusCompleted),
	})
	require.NoError(t, err)
	task2, err := tq.CreateTask(ctx, bson.M{
		"title":   "Do other",
		"plan_id": "plan_sync",
		"status":  string(taskqueue.TaskStatusFailed),
	})
	require.NoError(t, err)
	_ = task1
	_ = task2

	result, err := store.SyncProgress(ctx, "plan_sync")
	require.NoError(t, err)
	assert.True(t, result.Synced)
	assert.Equal(t, "plan_sync", result.PlanID)
	assert.Equal(t, 2, result.Total)
	assert.Equal(t, 1, result.Completed)
	assert.Equal(t, 1, result.Failed)

	// Verify content was updated
	got, err := store.Get(ctx, "plan_sync")
	require.NoError(t, err)
	assert.Contains(t, got.Content, "**Progress:** 1/2 completed, 1 failed")
}

func TestStore_SyncProgress_NoChanges(t *testing.T) {
	client := setupMongo(t)
	store := newTestStore(t, client, "test-agent")

	plan := makeMinimalPlan("plan_no_sync", "No Sync")
	plan.Content = "# Plain plan\n\nNo tasks here."
	_, err := store.Create(context.Background(), plan)
	require.NoError(t, err)

	result, err := store.SyncProgress(context.Background(), "plan_no_sync")
	require.NoError(t, err)
	assert.False(t, result.Synced)
	assert.Equal(t, "no changes needed", result.Reason)
}

func TestStore_SyncProgress_PlanNotFound(t *testing.T) {
	client := setupMongo(t)
	store := newTestStore(t, client, "test-agent")

	result, err := store.SyncProgress(context.Background(), "nonexistent")
	require.NoError(t, err)
	assert.False(t, result.Synced)
	assert.Equal(t, "plan not found", result.Reason)
}

// ------------------------------------------------------------------
// Archive / Unarchive tests
// ------------------------------------------------------------------

func TestStore_ArchiveUnarchive(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "test-agent")

	plan := makeMinimalPlan("plan_arch", "Archive me")
	_, err := store.Create(ctx, plan)
	require.NoError(t, err)

	ares, err := store.Archive(ctx, "plan_arch")
	require.NoError(t, err)
	assert.Equal(t, int64(1), ares.ModifiedCount)

	got, err := store.Get(ctx, "plan_arch")
	require.NoError(t, err)
	assert.NotNil(t, got.ArchivedAt)

	ures, err := store.Unarchive(ctx, "plan_arch")
	require.NoError(t, err)
	assert.Equal(t, int64(1), ures.ModifiedCount)

	got, err = store.Get(ctx, "plan_arch")
	require.NoError(t, err)
	assert.Nil(t, got.ArchivedAt)
}

// ------------------------------------------------------------------
// Count tests
// ------------------------------------------------------------------

func TestStore_Count(t *testing.T) {
	client := setupMongo(t)
	store := newTestStore(t, client, "test-agent")

	for i := 0; i < 3; i++ {
		plan := makeMinimalPlan(fmt.Sprintf("plan_cnt_%d", i), fmt.Sprintf("Count %d", i))
		_, err := store.Create(context.Background(), plan)
		require.NoError(t, err)
	}

	count, err := store.Count(context.Background(), bson.M{})
	require.NoError(t, err)
	assert.Equal(t, int64(3), count)
}

// ------------------------------------------------------------------
// Name / Database passthrough
// ------------------------------------------------------------------

func TestStore_NameAndDatabase(t *testing.T) {
	client := setupMongo(t)
	store := newTestStore(t, client, "test-agent")

	assert.Equal(t, "plans", store.Name())
	assert.NotNil(t, store.Database())
	assert.Equal(t, "otoxan_agent_test-agent", store.Database().Name())
}
