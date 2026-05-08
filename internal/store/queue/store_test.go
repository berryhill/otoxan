package queue

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/silas/otoxan/internal/store/tasks"
	"github.com/testcontainers/testcontainers-go/modules/mongodb"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// setupMongo spins up a testcontainers MongoDB container and returns a client.
// It also sets MONGO_URI in the environment so the Python bridge can connect
// to the same instance.
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
	// Export so Python helper sees the same URI
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

// newTestQueue returns a TaskQueue backed by fresh test collections.
func newTestQueue(t *testing.T, client *mongo.Client) *TaskQueue {
	t.Helper()
	db := client.Database("silas")
	tasksColl := db.Collection("tasks")
	taskStore := tasks.NewTaskStore(tasksColl)
	return NewTaskQueue(db, taskStore)
}

// makeMinimalTask returns a task with only required fields set.
func makeMinimalTask(id, title string) *tasks.Task {
	now := time.Now().UTC().Truncate(time.Millisecond)
	return &tasks.Task{
		TaskID:    id,
		Title:     title,
		Status:    tasks.StatusQueued,
		Type:      tasks.TypeInternal,
		Priority:  2,
		Assignee:  "silas",
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// ------------------------------------------------------------------
// CRUD round-trip tests
// ------------------------------------------------------------------

func TestTaskQueue_Claim(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	q := newTestQueue(t, client)

	// Seed a QUEUED task
	task := makeMinimalTask("tq_001", "Claim me")
	_, err := q.Tasks().Create(ctx, task)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	claimed, err := q.Claim(ctx, "agent-1")
	if err != nil {
		t.Fatalf("Claim failed: %v", err)
	}
	if claimed.Status != tasks.StatusClaimed {
		t.Fatalf("expected status CLAIMED, got %s", claimed.Status)
	}
	if claimed.ClaimedAt == nil {
		t.Fatal("expected claimed_at to be set")
	}

	// Second claim should return no documents
	_, err = q.Claim(ctx, "agent-2")
	if err != mongo.ErrNoDocuments {
		t.Fatalf("expected ErrNoDocuments on empty queue, got %v", err)
	}
}

func TestTaskQueue_MarkRunning(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	q := newTestQueue(t, client)

	task := makeMinimalTask("tq_002", "Run me")
	_, _ = q.Tasks().Create(ctx, task)
	_, _ = q.Claim(ctx, "agent-1")

	running, err := q.MarkRunning(ctx, "tq_002")
	if err != nil {
		t.Fatalf("MarkRunning failed: %v", err)
	}
	if running.Status != tasks.StatusRunning {
		t.Fatalf("expected status RUNNING, got %s", running.Status)
	}
	if running.StartedAt == nil {
		t.Fatal("expected started_at to be set")
	}
}

func TestTaskQueue_MarkCompleted(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	q := newTestQueue(t, client)

	task := makeMinimalTask("tq_003", "Complete me")
	_, _ = q.Tasks().Create(ctx, task)
	_, _ = q.Claim(ctx, "agent-1")
	_, _ = q.MarkRunning(ctx, "tq_003")

	completed, err := q.MarkCompleted(ctx, "tq_003", "done")
	if err != nil {
		t.Fatalf("MarkCompleted failed: %v", err)
	}
	if completed.Status != tasks.StatusCompleted {
		t.Fatalf("expected status COMPLETED, got %s", completed.Status)
	}
	if completed.CompletedAt == nil {
		t.Fatal("expected completed_at to be set")
	}
	if completed.Output == nil || *completed.Output != "done" {
		t.Fatalf("expected output 'done', got %v", completed.Output)
	}
}

func TestTaskQueue_MarkFailed(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	q := newTestQueue(t, client)

	task := makeMinimalTask("tq_004", "Fail me")
	_, _ = q.Tasks().Create(ctx, task)
	_, _ = q.Claim(ctx, "agent-1")
	_, _ = q.MarkRunning(ctx, "tq_004")

	failed, err := q.MarkFailed(ctx, "tq_004", "timeout")
	if err != nil {
		t.Fatalf("MarkFailed failed: %v", err)
	}
	if failed.Status != tasks.StatusFailed {
		t.Fatalf("expected status FAILED, got %s", failed.Status)
	}
	if failed.Attempts != 1 {
		t.Fatalf("expected attempts=1, got %d", failed.Attempts)
	}
}

func TestTaskQueue_MarkRetried(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	q := newTestQueue(t, client)

	task := makeMinimalTask("tq_005", "Retry me")
	_, _ = q.Tasks().Create(ctx, task)
	_, _ = q.Claim(ctx, "agent-1")
	_, _ = q.MarkRunning(ctx, "tq_005")
	_, _ = q.MarkFailed(ctx, "tq_005", "timeout")

	retried, err := q.MarkRetried(ctx, "tq_005")
	if err != nil {
		t.Fatalf("MarkRetried failed: %v", err)
	}
	if retried.Status != tasks.StatusQueued {
		t.Fatalf("expected status QUEUED after retry, got %s", retried.Status)
	}
	if retried.ClaimedAt != nil {
		t.Fatal("expected claimed_at to be cleared after retry")
	}
}

func TestTaskQueue_ReclaimStale(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	q := newTestQueue(t, client)

	// Seed a CLAIMED task with an old claimed_at
	task := makeMinimalTask("tq_006", "Stale claim")
	_, _ = q.Tasks().Create(ctx, task)
	_, _ = q.Claim(ctx, "agent-1")

	// Manually backdate claimed_at to 15 minutes ago
	_, err := q.Tasks().Database().Collection(q.Tasks().Name()).UpdateOne(ctx,
		bson.M{"task_id": "tq_006"},
		bson.M{"$set": bson.M{"claimed_at": time.Now().UTC().Add(-15 * time.Minute)}},
	)
	if err != nil {
		t.Fatalf("backdate failed: %v", err)
	}

	reclaimed, err := q.ReclaimStale(ctx, 10*time.Minute)
	if err != nil {
		t.Fatalf("ReclaimStale failed: %v", err)
	}
	if reclaimed != 1 {
		t.Fatalf("expected 1 reclaimed, got %d", reclaimed)
	}

	// Verify task is QUEUED again
	got, err := q.Tasks().Get(ctx, "tq_006")
	if err != nil {
		t.Fatalf("Get after reclaim failed: %v", err)
	}
	if got.Status != tasks.StatusQueued {
		t.Fatalf("expected status QUEUED after reclaim, got %s", got.Status)
	}
}

func TestTaskQueue_QueueStatus(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	q := newTestQueue(t, client)

	// Seed tasks with different statuses
	for i := 0; i < 3; i++ {
		task := makeMinimalTask(fmt.Sprintf("tq_qs_%d", i), fmt.Sprintf("Queue status %d", i))
		_, _ = q.Tasks().Create(ctx, task)
	}
	for i := 3; i < 5; i++ {
		task := makeMinimalTask(fmt.Sprintf("tq_qs_%d", i), fmt.Sprintf("Queue status %d", i))
		_, _ = q.Tasks().Create(ctx, task)
		_, _ = q.Claim(ctx, "agent-1")
	}

	statuses, err := q.QueueStatus(ctx)
	if err != nil {
		t.Fatalf("QueueStatus failed: %v", err)
	}

	var queuedCount, claimedCount int64
	for _, sc := range statuses {
		switch sc.Status {
		case string(tasks.StatusQueued):
			queuedCount = sc.Count
		case string(tasks.StatusClaimed):
			claimedCount = sc.Count
		}
	}
	if queuedCount != 3 {
		t.Fatalf("expected 3 QUEUED, got %d", queuedCount)
	}
	if claimedCount != 2 {
		t.Fatalf("expected 2 CLAIMED, got %d", claimedCount)
	}
}

func TestTaskQueue_EmitEvent(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	q := newTestQueue(t, client)

	res, err := q.EmitEvent(ctx, "tq_007", "task_created", "silas", bson.M{"source": "test"})
	if err != nil {
		t.Fatalf("EmitEvent failed: %v", err)
	}
	if res.InsertedID == nil {
		t.Fatal("expected InsertedID")
	}

	events, err := q.ListEvents(ctx, "tq_007")
	if err != nil {
		t.Fatalf("ListEvents failed: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].EventType != "task_created" {
		t.Fatalf("expected event_type task_created, got %s", events[0].EventType)
	}
	if events[0].Sequence != 1 {
		t.Fatalf("expected sequence 1, got %d", events[0].Sequence)
	}
	if events[0].Actor != "silas" {
		t.Fatalf("expected actor silas, got %s", events[0].Actor)
	}

	// Emit a second event — sequence should auto-increment
	_, _ = q.EmitEvent(ctx, "tq_007", "task_started", "agent-1", bson.M{})
	events, err = q.ListEvents(ctx, "tq_007")
	if err != nil {
		t.Fatalf("ListEvents second failed: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[1].Sequence != 2 {
		t.Fatalf("expected sequence 2 for second event, got %d", events[1].Sequence)
	}
}

// ------------------------------------------------------------------
// Concurrency test
// ------------------------------------------------------------------

func TestConcurrentClaim(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	q := newTestQueue(t, client)

	// Seed 10 QUEUED tasks
	for i := 0; i < 10; i++ {
		task := makeMinimalTask(fmt.Sprintf("tq_cc_%d", i), fmt.Sprintf("Concurrent task %d", i))
		_, err := q.Tasks().Create(ctx, task)
		if err != nil {
			t.Fatalf("Create failed: %v", err)
		}
	}

	// 50 goroutines compete for 10 tasks
	var wg sync.WaitGroup
	var successCount int64
	var failCount int64

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(agentNum int) {
			defer wg.Done()
			agent := fmt.Sprintf("agent-%d", agentNum)
			_, err := q.Claim(ctx, agent)
			if err == nil {
				atomic.AddInt64(&successCount, 1)
			} else if err == mongo.ErrNoDocuments {
				atomic.AddInt64(&failCount, 1)
			} else {
				t.Errorf("unexpected claim error: %v", err)
			}
		}(i)
	}

	wg.Wait()

	if successCount != 10 {
		t.Fatalf("expected exactly 10 successful claims, got %d", successCount)
	}
	if failCount != 40 {
		t.Fatalf("expected exactly 40 failed claims (no docs), got %d", failCount)
	}

	// Verify all claimed tasks have unique agents
	claimed, err := q.Tasks().List(ctx, tasks.ListOptions{Status: []tasks.TaskStatus{tasks.StatusClaimed}})
	if err != nil {
		t.Fatalf("List claimed failed: %v", err)
	}
	if len(claimed) != 10 {
		t.Fatalf("expected 10 CLAIMED tasks, got %d", len(claimed))
	}

	agentSet := make(map[string]struct{})
	for _, task := range claimed {
		// The claimed_by field isn't on Task struct — verify via raw lookup
		var raw bson.M
		err := q.Tasks().Database().Collection(q.Tasks().Name()).FindOne(ctx, bson.M{"task_id": task.TaskID}).Decode(&raw)
		if err != nil {
			t.Fatalf("raw lookup failed: %v", err)
		}
		agent, ok := raw["claimed_by"].(string)
		if !ok || agent == "" {
			t.Fatalf("expected claimed_by set for %s", task.TaskID)
		}
		agentSet[agent] = struct{}{}
	}
	if len(agentSet) != 10 {
		t.Fatalf("expected 10 unique agents, got %d", len(agentSet))
	}
}

// ------------------------------------------------------------------
// Full CRUD round-trip
// ------------------------------------------------------------------

func TestTaskQueue_FullCRUDRoundTrip(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	q := newTestQueue(t, client)

	// 1. Create
	task := makeMinimalTask("tq_round", "Round-trip queue task")
	_, err := q.Tasks().Create(ctx, task)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// 2. Claim
	claimed, err := q.Claim(ctx, "round-agent")
	if err != nil {
		t.Fatalf("Claim failed: %v", err)
	}
	if claimed.Status != tasks.StatusClaimed {
		t.Fatalf("expected CLAIMED, got %s", claimed.Status)
	}

	// 3. MarkRunning
	running, err := q.MarkRunning(ctx, "tq_round")
	if err != nil {
		t.Fatalf("MarkRunning failed: %v", err)
	}
	if running.Status != tasks.StatusRunning {
		t.Fatalf("expected RUNNING, got %s", running.Status)
	}

	// 4. MarkCompleted
	completed, err := q.MarkCompleted(ctx, "tq_round", "all done")
	if err != nil {
		t.Fatalf("MarkCompleted failed: %v", err)
	}
	if completed.Status != tasks.StatusCompleted {
		t.Fatalf("expected COMPLETED, got %s", completed.Status)
	}

	// 5. Verify events
	events, err := q.ListEvents(ctx, "tq_round")
	if err != nil {
		t.Fatalf("ListEvents failed: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events (started, completed), got %d", len(events))
	}

	// 6. QueueStatus
	statuses, err := q.QueueStatus(ctx)
	if err != nil {
		t.Fatalf("QueueStatus failed: %v", err)
	}
	var completedCount int64
	for _, sc := range statuses {
		if sc.Status == string(tasks.StatusCompleted) {
			completedCount = sc.Count
		}
	}
	if completedCount != 1 {
		t.Fatalf("expected 1 COMPLETED in status, got %d", completedCount)
	}
}
