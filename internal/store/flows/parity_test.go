//go:build full || parity
// +build full parity

package flows

import (
	"context"
	"testing"
	"time"

	"github.com/silas/otoxan/internal/testutil"
)

func TestFlowStore_Parity_GoWritePythonRead(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	now := time.Now().UTC().Truncate(time.Millisecond)
	f := &Flow{
		FlowID:      "f_parity_gwpr",
		Name:        "Parity GWPR",
		Description: "Go writes, Python reads",
		Status:      StatusDraft,
		Version:     1,
		Steps:       []FlowStep{},
		Tags:        []string{"parity"},
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	_, err := store.Create(ctx, f)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	pyDoc := testutil.PythonReadFixture(t, "flows", "f_parity_gwpr")
	if pyDoc == nil {
		t.Fatal("Python read returned nil")
	}
	testutil.NormalizeTimeFields(t, pyDoc)

	assertParityString(t, pyDoc, "flow_id", "f_parity_gwpr")
	assertParityString(t, pyDoc, "name", "Parity GWPR")
	assertParityString(t, pyDoc, "description", "Go writes, Python reads")
	assertParityString(t, pyDoc, "status", "DRAFT")
	assertParityInt(t, pyDoc, "version", 1)

	if delVal, ok := pyDoc["deleted"]; ok && delVal == true {
		t.Fatalf("expected deleted absent/false, got %v", delVal)
	}
	if _, ok := pyDoc["deleted_at"]; ok {
		t.Fatal("expected deleted_at absent for live document")
	}
}

func TestFlowStore_Parity_PythonWriteGoRead(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	testutil.PythonWriteFixture(t, "flows", "f_parity_pwgr")

	got, err := store.Get(ctx, "f_parity_pwgr")
	if err != nil {
		t.Fatalf("Go read failed: %v", err)
	}
	if got.FlowID != "f_parity_pwgr" {
		t.Fatalf("flow_id mismatch: %s", got.FlowID)
	}
	if got.Name != "Parity fixture" {
		t.Fatalf("name mismatch: %s", got.Name)
	}
	if got.Status != StatusDraft {
		t.Fatalf("status mismatch: %s", got.Status)
	}
	if got.Deleted {
		t.Fatal("expected deleted=false")
	}
	if got.DeletedAt != nil {
		t.Fatal("expected deleted_at=nil")
	}
}

func TestFlowStore_Parity_SoftDelete(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	now := time.Now().UTC().Truncate(time.Millisecond)
	f := &Flow{
		FlowID:      "f_parity_del",
		Name:        "Parity delete",
		Description: "",
		Status:      StatusDraft,
		Version:     1,
		Steps:       []FlowStep{},
		Tags:        []string{},
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	_, err := store.Create(ctx, f)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	_, err = store.Delete(ctx, "f_parity_del")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	pyDoc := testutil.PythonReadFixture(t, "flows", "f_parity_del")
	if pyDoc != nil {
		if delVal, ok := pyDoc["deleted"]; !ok || delVal != true {
			t.Fatalf("expected Python read nil or deleted=true after soft delete, got %+v", pyDoc)
		}
	}

	_, err = store.Restore(ctx, "f_parity_del")
	if err != nil {
		t.Fatalf("Restore failed: %v", err)
	}
	pyDoc = testutil.PythonReadFixture(t, "flows", "f_parity_del")
	if pyDoc == nil {
		t.Fatal("Python read nil after restore")
	}
	if delVal, ok := pyDoc["deleted"]; ok && delVal == true {
		t.Fatalf("expected deleted=false after restore, got %v", delVal)
	}
}

func assertParityString(t *testing.T, doc map[string]interface{}, key, want string) {
	t.Helper()
	got, ok := doc[key].(string)
	if !ok {
		t.Fatalf("expected %s to be string, got %T (%v)", key, doc[key], doc[key])
	}
	if got != want {
		t.Fatalf("%s mismatch: got %q, want %q", key, got, want)
	}
}

func assertParityInt(t *testing.T, doc map[string]interface{}, key string, want int) {
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
