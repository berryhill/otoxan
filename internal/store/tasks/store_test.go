package tasks

import (
	"context"
	"fmt"
	"testing"
	"time"

	"os"

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

// newTestStore returns a TaskStore backed by a fresh test collection.
func newTestStore(t *testing.T, client *mongo.Client) *TaskStore {
	t.Helper()
	db := client.Database("silas")
	coll := db.Collection("tasks")
	return NewTaskStore(coll)
}

// makeMinimalTask returns a task with only required fields set.
func makeMinimalTask(id, title string) *Task {
	now := time.Now().UTC().Truncate(time.Millisecond)
	return &Task{
		TaskID:    id,
		Title:     title,
		Status:    StatusQueued,
		Type:      TypeInternal,
		Priority:  2,
		Assignee:  "silas",
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// ------------------------------------------------------------------
// CRUD round-trip tests
// ------------------------------------------------------------------

func TestTaskStore_CreateAndGet(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	task := makeMinimalTask("t_001", "Implement auth")
	res, err := store.Create(ctx, task)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if res.InsertedID == nil {
		t.Fatal("expected InsertedID")
	}

	got, err := store.Get(ctx, "t_001")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.TaskID != "t_001" {
		t.Fatalf("expected task_id t_001, got %s", got.TaskID)
	}
	if got.Title != "Implement auth" {
		t.Fatalf("expected title 'Implement auth', got %s", got.Title)
	}
	if got.Status != StatusQueued {
		t.Fatalf("expected status QUEUED, got %s", got.Status)
	}
	if got.Type != TypeInternal {
		t.Fatalf("expected type internal, got %s", got.Type)
	}
	if got.Priority != 2 {
		t.Fatalf("expected priority 2, got %d", got.Priority)
	}
	if got.Assignee != "silas" {
		t.Fatalf("expected assignee silas, got %s", got.Assignee)
	}
	if got.AssigneeType != "agent" {
		t.Fatalf("expected assignee_type agent, got %s", got.AssigneeType)
	}
	if got.MaxRetries != 3 {
		t.Fatalf("expected max_retries 3, got %d", got.MaxRetries)
	}
	if got.RetryConfig != DefaultRetryConfig() {
		t.Fatalf("expected default retry config, got %+v", got.RetryConfig)
	}
	if got.FailurePattern != "notify_and_halt" {
		t.Fatalf("expected failure_pattern notify_and_halt, got %s", got.FailurePattern)
	}
	if got.FailureContext != DefaultFailureContext() {
		t.Fatalf("expected default failure context, got %+v", got.FailureContext)
	}
}

func TestTaskStore_CreateAppliesDefaults(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	// Create with minimal fields — defaults should be applied
	now := time.Now().UTC().Truncate(time.Millisecond)
	task := &Task{
		TaskID:    "t_def",
		Title:     "Default test",
		CreatedAt: now,
		UpdatedAt: now,
	}

	_, err := store.Create(ctx, task)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	got, err := store.Get(ctx, "t_def")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.Type != TypeInternal {
		t.Fatalf("expected default type internal, got %s", got.Type)
	}
	if got.Status != StatusQueued {
		t.Fatalf("expected default status QUEUED, got %s", got.Status)
	}
	if got.Priority != 2 {
		t.Fatalf("expected default priority 2, got %d", got.Priority)
	}
	if got.Assignee != "default" {
		t.Fatalf("expected default assignee 'default', got %s", got.Assignee)
	}
	if got.AssigneeType != "agent" {
		t.Fatalf("expected default assignee_type agent, got %s", got.AssigneeType)
	}
	if got.MaxRetries != 3 {
		t.Fatalf("expected default max_retries 3, got %d", got.MaxRetries)
	}
}

func TestTaskStore_Update(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	task := makeMinimalTask("t_upd", "Update me")
	_, err := store.Create(ctx, task)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	ures, err := store.Update(ctx, "t_upd", bson.M{
		"status":      StatusRunning,
		"started_at":  time.Now().UTC(),
		"description": "Updated description",
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if ures.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1, got %d", ures.ModifiedCount)
	}

	got, err := store.Get(ctx, "t_upd")
	if err != nil {
		t.Fatalf("Get after update failed: %v", err)
	}
	if got.Status != StatusRunning {
		t.Fatalf("expected status RUNNING, got %s", got.Status)
	}
	if got.Description != "Updated description" {
		t.Fatalf("expected description 'Updated description', got %s", got.Description)
	}
	if got.StartedAt == nil {
		t.Fatal("expected started_at to be set")
	}
	if got.UpdatedAt.Before(task.UpdatedAt) {
		t.Fatal("expected updated_at to be newer than created_at")
	}
}

func TestTaskStore_SoftDelete(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	task := makeMinimalTask("t_del", "Delete me")
	_, err := store.Create(ctx, task)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Soft delete
	dres, err := store.Delete(ctx, "t_del")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if dres.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1, got %d", dres.ModifiedCount)
	}

	// Should not be findable by normal Get
	_, err = store.Get(ctx, "t_del")
	if err != mongo.ErrNoDocuments {
		t.Fatalf("expected ErrNoDocuments after soft delete, got %v", err)
	}

	// Should be findable with IncludeDeleted
	got, err := store.GetWithDeleted(ctx, "t_del")
	if err != nil {
		t.Fatalf("GetWithDeleted failed: %v", err)
	}
	if !got.Deleted {
		t.Fatalf("expected deleted=true, got %v", got.Deleted)
	}
	if got.DeletedAt == nil {
		t.Fatal("expected deleted_at to be set")
	}
}

func TestTaskStore_Restore(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	task := makeMinimalTask("t_res", "Restore me")
	_, err := store.Create(ctx, task)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	_, _ = store.Delete(ctx, "t_res")

	rres, err := store.Restore(ctx, "t_res")
	if err != nil {
		t.Fatalf("Restore failed: %v", err)
	}
	if rres.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1 on restore, got %d", rres.ModifiedCount)
	}

	got, err := store.Get(ctx, "t_res")
	if err != nil {
		t.Fatalf("Get after restore failed: %v", err)
	}
	if got.Deleted {
		t.Fatalf("expected deleted=false after restore, got %v", got.Deleted)
	}
	if got.DeletedAt != nil {
		t.Fatalf("expected deleted_at nil after restore, got %v", got.DeletedAt)
	}
}

func TestTaskStore_HardDelete(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	task := makeMinimalTask("t_hard", "Hard delete me")
	_, err := store.Create(ctx, task)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	dres, err := store.HardDelete(ctx, "t_hard")
	if err != nil {
		t.Fatalf("HardDelete failed: %v", err)
	}
	if dres.DeletedCount != 1 {
		t.Fatalf("expected DeletedCount=1, got %d", dres.DeletedCount)
	}

	_, err = store.GetWithDeleted(ctx, "t_hard")
	if err != mongo.ErrNoDocuments {
		t.Fatalf("expected ErrNoDocuments after hard delete, got %v", err)
	}
}

func TestTaskStore_List(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	// Insert 3 tasks with different statuses and assignees
	tasks := []*Task{
		makeMinimalTask("t_l1", "Task one"),
		makeMinimalTask("t_l2", "Task two"),
		makeMinimalTask("t_l3", "Task three"),
	}
	tasks[0].Status = StatusQueued
	tasks[0].Assignee = "silas"
	tasks[0].Priority = 1
	tasks[1].Status = StatusRunning
	tasks[1].Assignee = "archer"
	tasks[1].Priority = 2
	tasks[2].Status = StatusQueued
	tasks[2].Assignee = "silas"
	tasks[2].Priority = 3

	for _, task := range tasks {
		_, err := store.Create(ctx, task)
		if err != nil {
			t.Fatalf("Create failed: %v", err)
		}
	}

	// List all (no filter)
	all, err := store.List(ctx, ListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("List all failed: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(all))
	}

	// List by status QUEUED
	queued, err := store.List(ctx, ListOptions{Status: []TaskStatus{StatusQueued}})
	if err != nil {
		t.Fatalf("List queued failed: %v", err)
	}
	if len(queued) != 2 {
		t.Fatalf("expected 2 queued tasks, got %d", len(queued))
	}

	// List by assignee silas
	silasTasks, err := store.List(ctx, ListOptions{Assignee: "silas"})
	if err != nil {
		t.Fatalf("List by assignee failed: %v", err)
	}
	if len(silasTasks) != 2 {
		t.Fatalf("expected 2 silas tasks, got %d", len(silasTasks))
	}

	// Verify sort order: priority asc, created_at asc
	if silasTasks[0].Priority != 1 {
		t.Fatalf("expected first silas task priority=1, got %d", silasTasks[0].Priority)
	}
	if silasTasks[1].Priority != 3 {
		t.Fatalf("expected second silas task priority=3, got %d", silasTasks[1].Priority)
	}

	// List with limit
	limited, err := store.List(ctx, ListOptions{Limit: 1})
	if err != nil {
		t.Fatalf("List limited failed: %v", err)
	}
	if len(limited) != 1 {
		t.Fatalf("expected 1 task with limit, got %d", len(limited))
	}
}

func TestTaskStore_ListWithDeleted(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	task := makeMinimalTask("t_ld", "List deleted")
	_, err := store.Create(ctx, task)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	_, _ = store.Delete(ctx, "t_ld")

	// Normal list should not find it
	live, err := store.List(ctx, ListOptions{})
	if err != nil {
		t.Fatalf("List live failed: %v", err)
	}
	if len(live) != 0 {
		t.Fatalf("expected 0 live tasks, got %d", len(live))
	}

	// Include deleted should find it
	withDeleted, err := store.List(ctx, ListOptions{IncludeDeleted: true})
	if err != nil {
		t.Fatalf("List with deleted failed: %v", err)
	}
	if len(withDeleted) != 1 {
		t.Fatalf("expected 1 task with include_deleted, got %d", len(withDeleted))
	}
	if withDeleted[0].TaskID != "t_ld" {
		t.Fatalf("expected task_id t_ld, got %s", withDeleted[0].TaskID)
	}
}

func TestTaskStore_Count(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	for i := 0; i < 5; i++ {
		task := makeMinimalTask(fmt.Sprintf("t_c%d", i), fmt.Sprintf("Count task %d", i))
		_, err := store.Create(ctx, task)
		if err != nil {
			t.Fatalf("Create failed: %v", err)
		}
	}

	cnt, err := store.Count(ctx, bson.M{})
	if err != nil {
		t.Fatalf("Count failed: %v", err)
	}
	if cnt != 5 {
		t.Fatalf("expected count 5, got %d", cnt)
	}
}

// ------------------------------------------------------------------
// Fixture-exchange parity test: Go writes, Go reads back
// ------------------------------------------------------------------

func TestTaskStore_FullCRUDRoundTrip(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	// 1. Create
	task := &Task{
		TaskID:       "t_round",
		Title:        "Round-trip task",
		Description:  "A task for full CRUD verification",
		Status:       StatusDraft,
		Type:         TypeGitHubIssue,
		Priority:     1,
		Assignee:     "archer",
		AssigneeType: "agent",
		MaxRetries:   5,
		DependsOn:    []string{"t_dep1", "t_dep2"},
		Labels:       []string{"urgent", "backend"},
		RetryConfig: RetryConfig{
			Backoff:             "linear",
			InitialDelaySeconds: 10,
			MaxDelaySeconds:     60,
			Multiplier:          1,
		},
		FailurePattern: "notify_and_continue",
		FailureContext: FailureContext{
			NotifyChannel:  "#alerts",
			IncludeLogs:    false,
			IncludeSummary: false,
		},
		Intent:         "Verify round-trip",
		Implementation: "Go store test",
		References:     "DS-3",
		PlanGoal:       "Port all stores",
		PlanContext:    "otoxan migration",
		PhaseContext:   "taskstore first",
		CreatedAt:      time.Now().UTC().Truncate(time.Millisecond),
		UpdatedAt:      time.Now().UTC().Truncate(time.Millisecond),
	}

	_, err := store.Create(ctx, task)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// 2. Read back
	got, err := store.Get(ctx, "t_round")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	// Verify all fields
	if got.TaskID != task.TaskID {
		t.Fatalf("task_id mismatch: %s vs %s", got.TaskID, task.TaskID)
	}
	if got.Title != task.Title {
		t.Fatalf("title mismatch")
	}
	if got.Description != task.Description {
		t.Fatalf("description mismatch")
	}
	if got.Status != task.Status {
		t.Fatalf("status mismatch")
	}
	if got.Type != task.Type {
		t.Fatalf("type mismatch")
	}
	if got.Priority != task.Priority {
		t.Fatalf("priority mismatch")
	}
	if got.Assignee != task.Assignee {
		t.Fatalf("assignee mismatch")
	}
	if got.AssigneeType != task.AssigneeType {
		t.Fatalf("assignee_type mismatch")
	}
	if got.MaxRetries != task.MaxRetries {
		t.Fatalf("max_retries mismatch")
	}
	if len(got.DependsOn) != 2 || got.DependsOn[0] != "t_dep1" || got.DependsOn[1] != "t_dep2" {
		t.Fatalf("depends_on mismatch: %v", got.DependsOn)
	}
	if len(got.Labels) != 2 || got.Labels[0] != "urgent" || got.Labels[1] != "backend" {
		t.Fatalf("labels mismatch: %v", got.Labels)
	}
	if got.RetryConfig != task.RetryConfig {
		t.Fatalf("retry_config mismatch: %+v vs %+v", got.RetryConfig, task.RetryConfig)
	}
	if got.FailurePattern != task.FailurePattern {
		t.Fatalf("failure_pattern mismatch")
	}
	if got.FailureContext != task.FailureContext {
		t.Fatalf("failure_context mismatch: %+v vs %+v", got.FailureContext, task.FailureContext)
	}
	if got.Intent != task.Intent {
		t.Fatalf("intent mismatch")
	}
	if got.Implementation != task.Implementation {
		t.Fatalf("implementation mismatch")
	}
	if got.References != task.References {
		t.Fatalf("references mismatch")
	}
	if got.PlanGoal != task.PlanGoal {
		t.Fatalf("plan_goal mismatch")
	}
	if got.PlanContext != task.PlanContext {
		t.Fatalf("plan_context mismatch")
	}
	if got.PhaseContext != task.PhaseContext {
		t.Fatalf("phase_context mismatch")
	}

	// 3. Update
	ures, err := store.Update(ctx, "t_round", bson.M{
		"status": StatusCompleted,
		"output": "All tests passed",
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if ures.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1, got %d", ures.ModifiedCount)
	}

	updated, err := store.Get(ctx, "t_round")
	if err != nil {
		t.Fatalf("Get after update failed: %v", err)
	}
	if updated.Status != StatusCompleted {
		t.Fatalf("expected status COMPLETED after update, got %s", updated.Status)
	}
	if updated.Output == nil || *updated.Output != "All tests passed" {
		t.Fatalf("expected output 'All tests passed', got %v", updated.Output)
	}

	// 4. Soft delete
	_, err = store.Delete(ctx, "t_round")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	_, err = store.Get(ctx, "t_round")
	if err != mongo.ErrNoDocuments {
		t.Fatalf("expected ErrNoDocuments after soft delete, got %v", err)
	}

	// 5. Restore
	_, err = store.Restore(ctx, "t_round")
	if err != nil {
		t.Fatalf("Restore failed: %v", err)
	}

	restored, err := store.Get(ctx, "t_round")
	if err != nil {
		t.Fatalf("Get after restore failed: %v", err)
	}
	if restored.Deleted {
		t.Fatal("expected deleted=false after restore")
	}
	if restored.DeletedAt != nil {
		t.Fatal("expected deleted_at=nil after restore")
	}
	if restored.Status != StatusCompleted {
		t.Fatalf("expected status preserved as COMPLETED after restore, got %s", restored.Status)
	}

	// 6. Hard delete
	_, err = store.HardDelete(ctx, "t_round")
	if err != nil {
		t.Fatalf("HardDelete failed: %v", err)
	}

	_, err = store.GetWithDeleted(ctx, "t_round")
	if err != mongo.ErrNoDocuments {
		t.Fatalf("expected ErrNoDocuments after hard delete, got %v", err)
	}
}

// ------------------------------------------------------------------
// Optional / pointer field tests
// ------------------------------------------------------------------

func TestTaskStore_OptionalFields(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	planID := "plan_123"
	epicID := "epic_456"
	phase := "phase_1"
	phaseOrder := 1
	directive := "agentic-coding"
	scheduledFor := time.Now().UTC().Add(time.Hour).Truncate(time.Millisecond)
	assigneeID := "user_789"
	verification := "manual"
	parentProvider := "openai"
	initiativeID := "init_001"
	flowRef := "flow_123"
	flowSessionID := "fs_456"
	flowTemplateID := "ft_789"
	flowStepID := "fst_001"
	flowStepType := "action"
	flowCurrentStep := "step_2"
	delegatedTo := "luca"
	output := "Done"

	task := &Task{
		TaskID:                "t_opt",
		Title:                 "Optional fields test",
		ParentTaskID:          taskID("parent_1"),
		EpicID:                &epicID,
		Phase:                 &phase,
		PhaseOrder:            &phaseOrder,
		PlanID:                &planID,
		Directive:             &directive,
		ScheduledFor:          &scheduledFor,
		ScheduledReason:       "Deferred for review",
		AssigneeID:            &assigneeID,
		Verification:          &verification,
		ParentProvider:        &parentProvider,
		InitiativeID:          &initiativeID,
		FlowRef:               &flowRef,
		FlowSessionID:         &flowSessionID,
		FlowTemplateID:        &flowTemplateID,
		FlowStepID:            &flowStepID,
		FlowStepType:          &flowStepType,
		FlowCurrentStep:       &flowCurrentStep,
		FlowDelegatedSessions: []string{"sess_1", "sess_2"},
		DelegatedTo:           &delegatedTo,
		Output:                &output,
		Artifacts: []Artifact{
			{
				ArtifactID: "art_1",
				Type:       "report",
				Content:    map[string]interface{}{"summary": "good"},
				CreatedAt:  time.Now().UTC().Truncate(time.Millisecond),
			},
		},
		CreatedAt: time.Now().UTC().Truncate(time.Millisecond),
		UpdatedAt: time.Now().UTC().Truncate(time.Millisecond),
	}

	_, err := store.Create(ctx, task)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	got, err := store.Get(ctx, "t_opt")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if got.ParentTaskID == nil || *got.ParentTaskID != "parent_1" {
		t.Fatalf("parent_task_id mismatch")
	}
	if got.EpicID == nil || *got.EpicID != epicID {
		t.Fatalf("epic_id mismatch")
	}
	if got.Phase == nil || *got.Phase != phase {
		t.Fatalf("phase mismatch")
	}
	if got.PhaseOrder == nil || *got.PhaseOrder != phaseOrder {
		t.Fatalf("phase_order mismatch")
	}
	if got.PlanID == nil || *got.PlanID != planID {
		t.Fatalf("plan_id mismatch")
	}
	if got.Directive == nil || *got.Directive != directive {
		t.Fatalf("directive mismatch")
	}
	if got.ScheduledFor == nil || !got.ScheduledFor.Equal(scheduledFor) {
		t.Fatalf("scheduled_for mismatch: %v vs %v", got.ScheduledFor, scheduledFor)
	}
	if got.ScheduledReason != "Deferred for review" {
		t.Fatalf("scheduled_reason mismatch")
	}
	if got.AssigneeID == nil || *got.AssigneeID != assigneeID {
		t.Fatalf("assignee_id mismatch")
	}
	if got.Verification == nil || *got.Verification != verification {
		t.Fatalf("verification mismatch")
	}
	if got.ParentProvider == nil || *got.ParentProvider != parentProvider {
		t.Fatalf("parent_provider mismatch")
	}
	if got.InitiativeID == nil || *got.InitiativeID != initiativeID {
		t.Fatalf("initiative_id mismatch")
	}
	if got.FlowRef == nil || *got.FlowRef != flowRef {
		t.Fatalf("flow_ref mismatch")
	}
	if got.FlowSessionID == nil || *got.FlowSessionID != flowSessionID {
		t.Fatalf("flow_session_id mismatch")
	}
	if got.FlowTemplateID == nil || *got.FlowTemplateID != flowTemplateID {
		t.Fatalf("flow_template_id mismatch")
	}
	if got.FlowStepID == nil || *got.FlowStepID != flowStepID {
		t.Fatalf("flow_step_id mismatch")
	}
	if got.FlowStepType == nil || *got.FlowStepType != flowStepType {
		t.Fatalf("flow_step_type mismatch")
	}
	if got.FlowCurrentStep == nil || *got.FlowCurrentStep != flowCurrentStep {
		t.Fatalf("flow_current_step mismatch")
	}
	if len(got.FlowDelegatedSessions) != 2 || got.FlowDelegatedSessions[0] != "sess_1" {
		t.Fatalf("flow_delegated_sessions mismatch: %v", got.FlowDelegatedSessions)
	}
	if got.DelegatedTo == nil || *got.DelegatedTo != delegatedTo {
		t.Fatalf("delegated_to mismatch")
	}
	if got.Output == nil || *got.Output != output {
		t.Fatalf("output mismatch")
	}
	if len(got.Artifacts) != 1 || got.Artifacts[0].ArtifactID != "art_1" {
		t.Fatalf("artifacts mismatch: %v", got.Artifacts)
	}
}

func taskID(id string) *string {
	return &id
}
