// Package queue provides a MongoDB-backed task queue with atomic claim,
// status transitions, event emission, and soft-delete semantics.
package queue

import (
	"context"
	"fmt"
	"time"

	"github.com/silas/otoxan/internal/softdelete"
	"github.com/silas/otoxan/internal/store/tasks"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ------------------------------------------------------------------
// TaskQueue
// ------------------------------------------------------------------

// NewTaskQueue creates a TaskQueue backed by the given MongoDB database.
// It ensures required indexes on tasks, task_events, and task_counters.
func NewTaskQueue(db *mongo.Database, taskStore *tasks.TaskStore) *TaskQueue {
	eventsColl := db.Collection("task_events")
	countersColl := db.Collection("task_counters")

	tq := &TaskQueue{
		tasks:    taskStore,
		events:   softdelete.NewSoftDelete(eventsColl),
		counters: countersColl,
	}
	_ = tq.ensureIndexes(context.Background())
	return tq
}

// TaskQueue is a MongoDB-backed task queue with atomic claim, status
// transitions, event emission, and soft-delete semantics.
type TaskQueue struct {
	tasks    *tasks.TaskStore
	events   *softdelete.SoftDeleteCollection
	counters *mongo.Collection
}

// ------------------------------------------------------------------
// Indexes
// ------------------------------------------------------------------

func (q *TaskQueue) ensureIndexes(ctx context.Context) error {
	// events indexes
	eventsIdx := []mongo.IndexModel{
		{Keys: bson.D{{Key: "task_id", Value: 1}, {Key: "sequence", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "task_id", Value: 1}}},
		{Keys: bson.D{{Key: "timestamp", Value: 1}}},
	}
	if _, err := q.events.Database().Collection(q.events.Name()).Indexes().CreateMany(ctx, eventsIdx); err != nil {
		return fmt.Errorf("events indexes: %w", err)
	}
	return nil
}

// ------------------------------------------------------------------
// Atomic claim
// ------------------------------------------------------------------

// Claim atomically finds a QUEUED task and transitions it to CLAIMED for the
// given agent. Returns the claimed task or mongo.ErrNoDocuments if none
// available. The operation is atomic via findOneAndUpdate.
func (q *TaskQueue) Claim(ctx context.Context, agent string) (*tasks.Task, error) {
	now := time.Now().UTC()
	filter := bson.M{"status": tasks.StatusQueued}
	update := bson.M{
		"$set": bson.M{
			"status":     tasks.StatusClaimed,
			"claimed_by": agent,
			"claimed_at": now,
			"updated_at": now,
		},
	}
	opts := options.FindOneAndUpdate().SetReturnDocument(options.After)

	var task tasks.Task
	err := q.tasks.Database().Collection(q.tasks.Name()).FindOneAndUpdate(ctx, filter, update, opts).Decode(&task)
	if err != nil {
		return nil, err
	}
	return &task, nil
}

// ------------------------------------------------------------------
// Status transitions
// ------------------------------------------------------------------

// MarkRunning transitions a CLAIMED task to RUNNING.
func (q *TaskQueue) MarkRunning(ctx context.Context, taskID string) (*tasks.Task, error) {
	now := time.Now().UTC()
	filter := bson.M{"task_id": taskID, "status": tasks.StatusClaimed}
	update := bson.M{
		"$set": bson.M{
			"status":     tasks.StatusRunning,
			"started_at": now,
			"updated_at": now,
		},
	}
	return q.transitionAndEmit(ctx, filter, update, taskID, "task_started", agentFromCtx(ctx), bson.M{})
}

// MarkCompleted transitions a RUNNING task to COMPLETED.
func (q *TaskQueue) MarkCompleted(ctx context.Context, taskID string, output string) (*tasks.Task, error) {
	now := time.Now().UTC()
	filter := bson.M{"task_id": taskID, "status": tasks.StatusRunning}
	update := bson.M{
		"$set": bson.M{
			"status":       tasks.StatusCompleted,
			"completed_at": now,
			"updated_at":   now,
			"output":       output,
		},
	}
	return q.transitionAndEmit(ctx, filter, update, taskID, "task_completed", agentFromCtx(ctx), bson.M{"output": output})
}

// MarkFailed transitions a RUNNING or CLAIMED task to FAILED.
func (q *TaskQueue) MarkFailed(ctx context.Context, taskID string, reason string) (*tasks.Task, error) {
	now := time.Now().UTC()
	filter := bson.M{"task_id": taskID, "status": bson.M{"$in": []tasks.TaskStatus{tasks.StatusRunning, tasks.StatusClaimed}}}
	update := bson.M{
		"$set": bson.M{
			"status":     tasks.StatusFailed,
			"updated_at": now,
		},
		"$inc": bson.M{"attempts": 1},
	}
	return q.transitionAndEmit(ctx, filter, update, taskID, "task_failed", agentFromCtx(ctx), bson.M{"reason": reason})
}

// MarkRetried transitions a FAILED task back to QUEUED for retry.
func (q *TaskQueue) MarkRetried(ctx context.Context, taskID string) (*tasks.Task, error) {
	now := time.Now().UTC()
	filter := bson.M{"task_id": taskID, "status": tasks.StatusFailed}
	update := bson.M{
		"$set": bson.M{
			"status":     tasks.StatusQueued,
			"updated_at": now,
		},
		"$unset": bson.M{"claimed_by": "", "claimed_at": ""},
	}
	return q.transitionAndEmit(ctx, filter, update, taskID, "task_retried", agentFromCtx(ctx), bson.M{})
}

// transitionAndEmit performs the findOneAndUpdate and emits an event.
func (q *TaskQueue) transitionAndEmit(ctx context.Context, filter bson.M, update bson.M, taskID, eventType, actor string, data bson.M) (*tasks.Task, error) {
	opts := options.FindOneAndUpdate().SetReturnDocument(options.After)
	var task tasks.Task
	err := q.tasks.Database().Collection(q.tasks.Name()).FindOneAndUpdate(ctx, filter, update, opts).Decode(&task)
	if err != nil {
		return nil, err
	}
	_, _ = q.EmitEvent(ctx, taskID, eventType, actor, data)
	return &task, nil
}

// ------------------------------------------------------------------
// Reclaim stale
// ------------------------------------------------------------------

// ReclaimStale finds CLAIMED tasks older than maxAge and transitions them
// back to QUEUED, clearing claimed_by and claimed_at.
func (q *TaskQueue) ReclaimStale(ctx context.Context, maxAge time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-maxAge)
	filter := bson.M{
		"status":     tasks.StatusClaimed,
		"claimed_at": bson.M{"$lt": cutoff},
	}
	update := bson.M{
		"$set": bson.M{
			"status":     tasks.StatusQueued,
			"updated_at": time.Now().UTC(),
		},
		"$unset": bson.M{"claimed_by": "", "claimed_at": ""},
	}
	res, err := q.tasks.Database().Collection(q.tasks.Name()).UpdateMany(ctx, filter, update)
	if err != nil {
		return 0, err
	}
	return res.ModifiedCount, nil
}

// ------------------------------------------------------------------
// Queue status
// ------------------------------------------------------------------

// QueueStatus returns counts per status via aggregation.
func (q *TaskQueue) QueueStatus(ctx context.Context) ([]StatusCount, error) {
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: bson.M{"deleted": bson.M{"$ne": true}}}},
		{{Key: "$group", Value: bson.M{"_id": "$status", "count": bson.M{"$sum": 1}}}},
	}
	cur, err := q.tasks.Database().Collection(q.tasks.Name()).Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)

	var results []StatusCount
	if err := cur.All(ctx, &results); err != nil {
		return nil, err
	}
	return results, nil
}
// ------------------------------------------------------------------
// Events
// ------------------------------------------------------------------

// EmitEvent inserts a task event and atomically increments the per-task
// sequence counter.
func (q *TaskQueue) EmitEvent(ctx context.Context, taskID, eventType, actor string, data bson.M) (*mongo.InsertOneResult, error) {
	now := time.Now().UTC()

	// Atomically increment the per-task sequence counter.
	counterFilter := bson.M{"_id": taskID}
	counterUpdate := bson.M{"$inc": bson.M{"sequence": 1}, "$setOnInsert": bson.M{"created_at": now}}
	counterOpts := options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)

	var counterDoc struct {
		Sequence int64 `bson:"sequence"`
	}
	err := q.counters.FindOneAndUpdate(ctx, counterFilter, counterUpdate, counterOpts).Decode(&counterDoc)
	if err != nil {
		return nil, fmt.Errorf("counter increment: %w", err)
	}

	event := TaskEvent{
		TaskID:    taskID,
		Sequence:  int(counterDoc.Sequence),
		EventType: eventType,
		Timestamp: now,
		Actor:     actor,
		Data:      data,
	}
	return q.events.InsertOne(ctx, event)
}

// ListEvents returns all events for a task, sorted by sequence ascending.
func (q *TaskQueue) ListEvents(ctx context.Context, taskID string) ([]TaskEvent, error) {
	cur, err := q.events.Find(ctx, bson.M{"task_id": taskID})
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)

	var events []TaskEvent
	if err := cur.All(ctx, &events); err != nil {
		return nil, err
	}
	// Sort by sequence in-memory (index on (task_id, sequence) ensures cursor
	// order, but we enforce for correctness).
	for i := 0; i < len(events); i++ {
		for j := i + 1; j < len(events); j++ {
			if events[j].Sequence < events[i].Sequence {
				events[i], events[j] = events[j], events[i]
			}
		}
	}
	return events, nil
}

// ------------------------------------------------------------------
// Passthrough
// ------------------------------------------------------------------

// Tasks returns the underlying TaskStore.
func (q *TaskQueue) Tasks() *tasks.TaskStore {
	return q.tasks
}

// ------------------------------------------------------------------
// Helpers
// ------------------------------------------------------------------

// agentFromCtx extracts an agent identifier from context. Falls back to
// "system" when not present.
func agentFromCtx(ctx context.Context) string {
	if v, ok := ctx.Value("agent").(string); ok && v != "" {
		return v
	}
	return "system"
}
