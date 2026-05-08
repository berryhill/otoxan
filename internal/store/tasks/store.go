package tasks

import (
	"context"
	"time"

	"github.com/silas/otoxan/internal/softdelete"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// NewTaskStore creates a TaskStore backed by the given MongoDB collection.
// It ensures required indexes on the tasks collection.
func NewTaskStore(coll *mongo.Collection) *TaskStore {
	sd := softdelete.NewSoftDelete(coll)
	ts := &TaskStore{sd: sd}
	_ = ts.ensureIndexes(context.Background())
	return ts
}

// TaskStore is a MongoDB-backed store for task documents with soft-delete
// semantics. It mirrors the Python taskqueue.TaskQueue CRUD surface.
type TaskStore struct {
	sd *softdelete.SoftDeleteCollection
}

// ------------------------------------------------------------------
// Indexes
// ------------------------------------------------------------------

func (s *TaskStore) ensureIndexes(ctx context.Context) error {
	indexes := []mongo.IndexModel{
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
	_, err := s.sd.Database().Collection(s.sd.Name()).Indexes().CreateMany(ctx, indexes)
	return err
}

// ------------------------------------------------------------------
// CRUD
// ------------------------------------------------------------------

// Create inserts a new task document. The caller must set TaskID, Title,
// and any other required fields. Defaults are applied for optional fields.
func (s *TaskStore) Create(ctx context.Context, task *Task) (*mongo.InsertOneResult, error) {
	now := time.Now().UTC()
	if task.CreatedAt.IsZero() {
		task.CreatedAt = now
	}
	if task.UpdatedAt.IsZero() {
		task.UpdatedAt = now
	}
	if task.Type == "" {
		task.Type = TypeInternal
	}
	if task.Status == "" {
		task.Status = StatusQueued
	}
	if task.Priority == 0 {
		task.Priority = 2
	}
	if task.Assignee == "" {
		task.Assignee = "default"
	}
	if task.AssigneeType == "" {
		task.AssigneeType = "agent"
	}
	if task.MaxRetries == 0 {
		task.MaxRetries = 3
	}
	if task.RetryConfig.Backoff == "" {
		task.RetryConfig = DefaultRetryConfig()
	}
	if task.FailurePattern == "" {
		task.FailurePattern = "notify_and_halt"
	}
	if task.FailureContext.NotifyChannel == "" && !task.FailureContext.IncludeLogs && !task.FailureContext.IncludeSummary {
		// Only apply default if all fields are zero value
		if task.FailureContext == (FailureContext{}) {
			task.FailureContext = DefaultFailureContext()
		}
	}
	return s.sd.InsertOne(ctx, task)
}

// Get retrieves a single task by task_id. Returns mongo.ErrNoDocuments if
// not found (or soft-deleted).
func (s *TaskStore) Get(ctx context.Context, taskID string) (*Task, error) {
	sr := s.sd.FindOne(ctx, bson.M{"task_id": taskID})
	if sr.Err() != nil {
		return nil, sr.Err()
	}
	var t Task
	if err := sr.Decode(&t); err != nil {
		return nil, err
	}
	return &t, nil
}

// GetWithDeleted retrieves a task including soft-deleted ones.
func (s *TaskStore) GetWithDeleted(ctx context.Context, taskID string) (*Task, error) {
	sr := s.sd.FindOne(ctx, bson.M{"task_id": taskID}, softdelete.WithIncludeDeleted())
	if sr.Err() != nil {
		return nil, sr.Err()
	}
	var t Task
	if err := sr.Decode(&t); err != nil {
		return nil, err
	}
	return &t, nil
}

// Update patches fields of an existing task. Sets updated_at automatically.
func (s *TaskStore) Update(ctx context.Context, taskID string, updates bson.M) (*mongo.UpdateResult, error) {
	updates["updated_at"] = time.Now().UTC()
	return s.sd.UpdateOne(ctx, bson.M{"task_id": taskID}, bson.M{"$set": updates})
}

// Delete soft-deletes a task by task_id.
func (s *TaskStore) Delete(ctx context.Context, taskID string) (*mongo.UpdateResult, error) {
	return s.sd.Delete(ctx, bson.M{"task_id": taskID})
}

// Restore un-deletes a soft-deleted task.
func (s *TaskStore) Restore(ctx context.Context, taskID string) (*mongo.UpdateResult, error) {
	return s.sd.Restore(ctx, bson.M{"task_id": taskID})
}

// HardDelete permanently removes a task.
func (s *TaskStore) HardDelete(ctx context.Context, taskID string) (*mongo.DeleteResult, error) {
	return s.sd.HardDelete(ctx, bson.M{"task_id": taskID})
}

// ------------------------------------------------------------------
// List / Query
// ------------------------------------------------------------------

// ListOptions configures the List query.
type ListOptions struct {
	Status         []TaskStatus
	Assignee       string
	PlanID         string
	AssigneeType   string
	AssigneeID     string
	Limit          int
	IncludeDeleted bool
}

// List returns tasks matching the provided filters, sorted by priority
// ascending then created_at ascending.
func (s *TaskStore) List(ctx context.Context, opts ListOptions) ([]Task, error) {
	filter := bson.M{}
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

	var sdOpts []softdelete.Option
	if opts.IncludeDeleted {
		sdOpts = append(sdOpts, softdelete.WithIncludeDeleted())
	}

	cur, err := s.sd.Find(ctx, filter, sdOpts...)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)

	var tasks []Task
	if err := cur.All(ctx, &tasks); err != nil {
		return nil, err
	}

	// Sort and limit in-memory since SoftDeleteCollection.Find doesn't
	// accept mongo FindOptions. The collection has the composite index
	// (status, priority, created_at) so the cursor comes back in a useful
	// order, but we enforce the exact sort here for correctness.
	sortTasks(tasks)
	if opts.Limit > 0 && len(tasks) > opts.Limit {
		tasks = tasks[:opts.Limit]
	}
	return tasks, nil
}

// Count returns the number of tasks matching the filter.
func (s *TaskStore) Count(ctx context.Context, filter bson.M) (int64, error) {
	return s.sd.CountDocuments(ctx, filter)
}

// sortTasks sorts by priority ascending then created_at ascending.
func sortTasks(tasks []Task) {
	// Use a stable sort since priority may have ties
	for i := 0; i < len(tasks); i++ {
		for j := i + 1; j < len(tasks); j++ {
			if tasks[j].Priority < tasks[i].Priority ||
				(tasks[j].Priority == tasks[i].Priority && tasks[j].CreatedAt.Before(tasks[i].CreatedAt)) {
				tasks[i], tasks[j] = tasks[j], tasks[i]
			}
		}
	}
}

// ------------------------------------------------------------------
// Collection passthrough
// ------------------------------------------------------------------

// Name returns the underlying collection name.
func (s *TaskStore) Name() string {
	return s.sd.Name()
}

// Database returns the underlying database.
func (s *TaskStore) Database() *mongo.Database {
	return s.sd.Database()
}
