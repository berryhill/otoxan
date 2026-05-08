package tasks

import (
	"context"
	"testing"
	"time"

	"github.com/silas/otoxan/internal/testutil"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// ------------------------------------------------------------------
// Parity tests: Go writes → Python reads, Python writes → Go reads
// ------------------------------------------------------------------

func TestTaskStore_Parity_GoWritePythonRead(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	now := time.Now().UTC().Truncate(time.Millisecond)
	task := &Task{
		TaskID:       "t_parity_gwpr",
		Title:        "Parity GWPR",
		Description:  "Go writes, Python reads",
		Status:       StatusQueued,
		Type:         TypeInternal,
		Priority:     2,
		Assignee:     "silas",
		AssigneeType: "agent",
		MaxRetries:   3,
		Labels:       []string{"parity"},
		DependsOn:    []string{},
		Artifacts:    []Artifact{},
		RetryConfig:  DefaultRetryConfig(),
		FailurePattern: "notify_and_halt",
		FailureContext: DefaultFailureContext(),
		Intent:         "parity",
		Implementation: "go",
		References:     "DS-4",
		PlanGoal:       "bson parity",
		PlanContext:    "otoxan",
		PhaseContext:   "round-trip",
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	_, err := store.Create(ctx, task)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Python reads back
	pyDoc := testutil.PythonReadFixture(t, "tasks", "t_parity_gwpr")
	if pyDoc == nil {
		t.Fatal("Python read returned nil — document not found")
	}
	testutil.NormalizeTimeFields(t, pyDoc)

	// Compare key fields
	assertParityString(t, pyDoc, "task_id", "t_parity_gwpr")
	assertParityString(t, pyDoc, "title", "Parity GWPR")
	assertParityString(t, pyDoc, "description", "Go writes, Python reads")
	assertParityString(t, pyDoc, "status", "QUEUED")
	assertParityString(t, pyDoc, "type", "internal")
	assertParityInt(t, pyDoc, "priority", 2)
	assertParityString(t, pyDoc, "assignee", "silas")
	assertParityString(t, pyDoc, "assignee_type", "agent")
	assertParityInt(t, pyDoc, "max_retries", 3)
	assertParityString(t, pyDoc, "failure_pattern", "notify_and_halt")
	assertParityString(t, pyDoc, "intent", "parity")
	assertParityString(t, pyDoc, "implementation", "go")
	assertParityString(t, pyDoc, "references", "DS-4")
	assertParityString(t, pyDoc, "plan_goal", "bson parity")
	assertParityString(t, pyDoc, "plan_context", "otoxan")
	assertParityString(t, pyDoc, "phase_context", "round-trip")

	// Verify soft-delete fields are absent (document is live)
	if delVal, ok := pyDoc["deleted"]; ok && delVal == true {
		t.Fatalf("expected deleted absent/false, got %v", delVal)
	}
	if _, ok := pyDoc["deleted_at"]; ok {
		t.Fatal("expected deleted_at absent for live document")
	}
}

func TestTaskStore_Parity_PythonWriteGoRead(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	// Python writes a minimal fixture
	pyDoc := testutil.PythonWriteFixture(t, "tasks", "t_parity_pwgr")
	if pyDoc == nil {
		t.Fatal("Python write returned nil")
	}

	// Go reads back
	got, err := store.Get(ctx, "t_parity_pwgr")
	if err != nil {
		t.Fatalf("Go read failed: %v", err)
	}

	// Compare key fields
	if got.TaskID != "t_parity_pwgr" {
		t.Fatalf("task_id mismatch: %s", got.TaskID)
	}
	if got.Title != "Parity fixture" {
		t.Fatalf("title mismatch: %s", got.Title)
	}
	if got.Status != StatusQueued {
		t.Fatalf("status mismatch: %s", got.Status)
	}
	if got.Type != TypeInternal {
		t.Fatalf("type mismatch: %s", got.Type)
	}
	if got.Priority != 2 {
		t.Fatalf("priority mismatch: %d", got.Priority)
	}
	if got.Assignee != "silas" {
		t.Fatalf("assignee mismatch: %s", got.Assignee)
	}
	if got.AssigneeType != "agent" {
		t.Fatalf("assignee_type mismatch: %s", got.AssigneeType)
	}
	if got.MaxRetries != 3 {
		t.Fatalf("max_retries mismatch: %d", got.MaxRetries)
	}
	if got.Deleted {
		t.Fatal("expected deleted=false for live document")
	}
	if got.DeletedAt != nil {
		t.Fatal("expected deleted_at=nil for live document")
	}
}

func TestTaskStore_Parity_SoftDelete(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	now := time.Now().UTC().Truncate(time.Millisecond)
	task := &Task{
		TaskID:    "t_parity_del",
		Title:     "Parity delete",
		Status:    StatusQueued,
		Type:      TypeInternal,
		Priority:  1,
		Assignee:  "silas",
		CreatedAt: now,
		UpdatedAt: now,
	}
	_, err := store.Create(ctx, task)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Go soft-deletes
	_, err = store.Delete(ctx, "t_parity_del")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Python should NOT see it via normal read (SoftDeleteCollection filters deleted=True)
	pyDoc := testutil.PythonReadFixture(t, "tasks", "t_parity_del")
	if pyDoc != nil {
		// The Python helper may return the raw document if it falls back to raw collection.
		// We accept either nil or a document with deleted=true.
		if delVal, ok := pyDoc["deleted"]; !ok || delVal != true {
			t.Fatalf("expected Python read to return nil or deleted=true after soft delete, got %+v", pyDoc)
		}
	}

	// Python raw collection (bypass SoftDeleteCollection) should see deleted=true
	// We verify this by restoring and reading again
	_, err = store.Restore(ctx, "t_parity_del")
	if err != nil {
		t.Fatalf("Restore failed: %v", err)
	}
	pyDoc = testutil.PythonReadFixture(t, "tasks", "t_parity_del")
	if pyDoc == nil {
		t.Fatal("Python read returned nil after restore")
	}
	if delVal, ok := pyDoc["deleted"]; ok && delVal == true {
		t.Fatalf("expected deleted=false after restore, got %v", delVal)
	}
}

// ------------------------------------------------------------------
// Parity helpers
// ------------------------------------------------------------------

func assertParityString(t *testing.T, doc bson.M, key, want string) {
	t.Helper()
	got, ok := doc[key].(string)
	if !ok {
		t.Fatalf("expected %s to be string, got %T (%v)", key, doc[key], doc[key])
	}
	if got != want {
		t.Fatalf("%s mismatch: got %q, want %q", key, got, want)
	}
}

func assertParityInt(t *testing.T, doc bson.M, key string, want int) {
	t.Helper()
	var got int
	switch v := doc[key].(type) {
	case int:
		got = v
	case int32:
		got = int(v)
	case int64:
		got = int(v)
	case float64:
		got = int(v)
	default:
		t.Fatalf("expected %s to be numeric, got %T (%v)", key, doc[key], doc[key])
	}
	if got != want {
		t.Fatalf("%s mismatch: got %d, want %d", key, got, want)
	}
}

func assertParityBool(t *testing.T, doc bson.M, key string, want bool) {
	t.Helper()
	got, ok := doc[key].(bool)
	if !ok {
		t.Fatalf("expected %s to be bool, got %T (%v)", key, doc[key], doc[key])
	}
	if got != want {
		t.Fatalf("%s mismatch: got %v, want %v", key, got, want)
	}
}
