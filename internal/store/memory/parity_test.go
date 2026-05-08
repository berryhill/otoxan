package memory

import (
	"context"
	"testing"
	"time"

	"github.com/silas/otoxan/internal/testutil"
)

func TestMemoryStore_Parity_GoWritePythonRead(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	now := time.Now().UTC().Truncate(time.Millisecond)
	m := &Memory{
		MemoryID:   "m_parity_gwpr",
		AgentID:    "silas",
		Type:       TypeObservation,
		Content:    "Go writes, Python reads",
		Tags:       []string{"parity"},
		Importance: 0.5,
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	_, err := store.Create(ctx, m)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	pyDoc := testutil.PythonReadFixture(t, "memory", "m_parity_gwpr")
	if pyDoc == nil {
		t.Fatal("Python read returned nil")
	}
	testutil.NormalizeTimeFields(t, pyDoc)

	testutil.AssertParityString(t, pyDoc, "memory_id", "m_parity_gwpr")
	testutil.AssertParityString(t, pyDoc, "agent_id", "silas")
	testutil.AssertParityString(t, pyDoc, "type", "observation")
	testutil.AssertParityString(t, pyDoc, "content", "Go writes, Python reads")

	if delVal, ok := pyDoc["deleted"]; ok && delVal == true {
		t.Fatalf("expected deleted absent/false, got %v", delVal)
	}
	if _, ok := pyDoc["deleted_at"]; ok {
		t.Fatal("expected deleted_at absent for live document")
	}
}

func TestMemoryStore_Parity_PythonWriteGoRead(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	testutil.PythonWriteFixture(t, "memory", "m_parity_pwgr")

	got, err := store.Get(ctx, "m_parity_pwgr")
	if err != nil {
		t.Fatalf("Go read failed: %v", err)
	}
	if got.MemoryID != "m_parity_pwgr" {
		t.Fatalf("memory_id mismatch: %s", got.MemoryID)
	}
	if got.AgentID != "silas" {
		t.Fatalf("agent_id mismatch: %s", got.AgentID)
	}
	if got.Type != TypeObservation {
		t.Fatalf("type mismatch: %s", got.Type)
	}
	if got.Deleted {
		t.Fatal("expected deleted=false")
	}
	if got.DeletedAt != nil {
		t.Fatal("expected deleted_at=nil")
	}
}

func TestMemoryStore_Parity_SoftDelete(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	now := time.Now().UTC().Truncate(time.Millisecond)
	m := &Memory{
		MemoryID:  "m_parity_del",
		AgentID:   "silas",
		Type:      TypeObservation,
		Content:   "Parity delete",
		Tags:      []string{},
		CreatedAt: now,
		UpdatedAt: now,
	}
	_, err := store.Create(ctx, m)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	_, err = store.Delete(ctx, "m_parity_del")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	pyDoc := testutil.PythonReadFixture(t, "memory", "m_parity_del")
	if pyDoc != nil {
		if delVal, ok := pyDoc["deleted"]; !ok || delVal != true {
			t.Fatalf("expected Python read nil or deleted=true after soft delete, got %+v", pyDoc)
		}
	}

	_, err = store.Restore(ctx, "m_parity_del")
	if err != nil {
		t.Fatalf("Restore failed: %v", err)
	}
	pyDoc = testutil.PythonReadFixture(t, "memory", "m_parity_del")
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
