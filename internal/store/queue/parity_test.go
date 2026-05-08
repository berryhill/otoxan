package queue

import (
	"context"
	"testing"
	"time"

	"github.com/silas/otoxan/internal/store/tasks"
	"github.com/silas/otoxan/internal/testutil"
)

func TestQueueStore_Parity_GoWritePythonRead(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	q := newTestQueue(t, client)

	now := time.Now().UTC().Truncate(time.Millisecond)
	task := &tasks.Task{
		TaskID:         "q_parity_gwpr",
		Title:          "Parity GWPR",
		Description:    "Go writes, Python reads",
		Status:         tasks.StatusQueued,
		Type:           tasks.TypeInternal,
		Priority:       2,
		Assignee:       "silas",
		AssigneeType:   "agent",
		MaxRetries:     3,
		Labels:         []string{"parity"},
		DependsOn:      []string{},
		Artifacts:      []tasks.Artifact{},
		RetryConfig:    tasks.DefaultRetryConfig(),
		FailurePattern: "notify_and_halt",
		FailureContext: tasks.DefaultFailureContext(),
		Intent:         "parity",
		Implementation: "go",
		References:     "DS-4",
		PlanGoal:       "bson parity",
		PlanContext:    "otoxan",
		PhaseContext:   "round-trip",
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	_, err := q.Tasks().Create(ctx, task)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	pyDoc := testutil.PythonReadFixture(t, "queue", "q_parity_gwpr")
	if pyDoc == nil {
		t.Fatal("Python read returned nil")
	}
	testutil.NormalizeTimeFields(t, pyDoc)

	testutil.AssertParityString(t, pyDoc, "task_id", "q_parity_gwpr")
	testutil.AssertParityString(t, pyDoc, "title", "Parity GWPR")
	testutil.AssertParityString(t, pyDoc, "status", "QUEUED")
	testutil.AssertParityString(t, pyDoc, "type", "internal")
	testutil.AssertParityInt(t, pyDoc, "priority", 2)
	testutil.AssertParityString(t, pyDoc, "assignee", "silas")
	testutil.AssertParityString(t, pyDoc, "assignee_type", "agent")
	testutil.AssertParityInt(t, pyDoc, "max_retries", 3)

	if delVal, ok := pyDoc["deleted"]; ok && delVal == true {
		t.Fatalf("expected deleted absent/false, got %v", delVal)
	}
	if _, ok := pyDoc["deleted_at"]; ok {
		t.Fatal("expected deleted_at absent for live document")
	}
}

func TestQueueStore_Parity_PythonWriteGoRead(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	q := newTestQueue(t, client)

	testutil.PythonWriteFixture(t, "queue", "q_parity_pwgr")

	got, err := q.Tasks().Get(ctx, "q_parity_pwgr")
	if err != nil {
		t.Fatalf("Go read failed: %v", err)
	}
	if got.TaskID != "q_parity_pwgr" {
		t.Fatalf("task_id mismatch: %s", got.TaskID)
	}
	if got.Title != "Parity fixture" {
		t.Fatalf("title mismatch: %s", got.Title)
	}
	if got.Status != tasks.StatusQueued {
		t.Fatalf("status mismatch: %s", got.Status)
	}
	if got.Deleted {
		t.Fatal("expected deleted=false")
	}
	if got.DeletedAt != nil {
		t.Fatal("expected deleted_at=nil")
	}
}

func TestQueueStore_Parity_SoftDelete(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	q := newTestQueue(t, client)

	now := time.Now().UTC().Truncate(time.Millisecond)
	task := &tasks.Task{
		TaskID:    "q_parity_del",
		Title:     "Parity delete",
		Status:    tasks.StatusQueued,
		Type:      tasks.TypeInternal,
		Priority:  1,
		Assignee:  "silas",
		CreatedAt: now,
		UpdatedAt: now,
	}
	_, err := q.Tasks().Create(ctx, task)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	_, err = q.Tasks().Delete(ctx, "q_parity_del")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	pyDoc := testutil.PythonReadFixture(t, "queue", "q_parity_del")
	if pyDoc != nil {
		if delVal, ok := pyDoc["deleted"]; !ok || delVal != true {
			t.Fatalf("expected Python read nil or deleted=true after soft delete, got %+v", pyDoc)
		}
	}

	_, err = q.Tasks().Restore(ctx, "q_parity_del")
	if err != nil {
		t.Fatalf("Restore failed: %v", err)
	}
	pyDoc = testutil.PythonReadFixture(t, "queue", "q_parity_del")
	if pyDoc == nil {
		t.Fatal("Python read nil after restore")
	}
	if delVal, ok := pyDoc["deleted"]; ok && delVal == true {
		t.Fatalf("expected deleted=false after restore, got %v", delVal)
	}
}
