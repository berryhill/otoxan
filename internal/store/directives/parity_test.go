package directives

import (
	"context"
	"testing"
	"time"

	"github.com/silas/otoxan/internal/testutil"
)

func TestDirectiveStore_Parity_GoWritePythonRead(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	now := time.Now().UTC().Truncate(time.Millisecond)
	d := &Directive{
		DirectiveID: "d_parity_gwpr",
		Title:       "Parity GWPR",
		Content:     "Go writes, Python reads",
		Category:    "general",
		Priority:    5,
		Enabled:     true,
		Tags:        []string{"parity"},
		Owner:       "silas",
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	_, err := store.Create(ctx, d)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	pyDoc := testutil.PythonReadFixture(t, "directives", "d_parity_gwpr")
	if pyDoc == nil {
		t.Fatal("Python read returned nil")
	}
	testutil.NormalizeTimeFields(t, pyDoc)

	testutil.AssertParityString(t, pyDoc, "directive_id", "d_parity_gwpr")
	testutil.AssertParityString(t, pyDoc, "title", "Parity GWPR")
	testutil.AssertParityString(t, pyDoc, "content", "Go writes, Python reads")
	testutil.AssertParityString(t, pyDoc, "category", "general")
	testutil.AssertParityInt(t, pyDoc, "priority", 5)
	testutil.AssertParityBool(t, pyDoc, "enabled", true)
	testutil.AssertParityString(t, pyDoc, "owner", "silas")

	if delVal, ok := pyDoc["deleted"]; ok && delVal == true {
		t.Fatalf("expected deleted absent/false, got %v", delVal)
	}
	if _, ok := pyDoc["deleted_at"]; ok {
		t.Fatal("expected deleted_at absent for live document")
	}
}

func TestDirectiveStore_Parity_PythonWriteGoRead(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	testutil.PythonWriteFixture(t, "directives", "d_parity_pwgr")

	got, err := store.Get(ctx, "d_parity_pwgr")
	if err != nil {
		t.Fatalf("Go read failed: %v", err)
	}
	if got.DirectiveID != "d_parity_pwgr" {
		t.Fatalf("directive_id mismatch: %s", got.DirectiveID)
	}
	if got.Title != "Parity fixture" {
		t.Fatalf("title mismatch: %s", got.Title)
	}
	if got.Category != "general" {
		t.Fatalf("category mismatch: %s", got.Category)
	}
	if got.Priority != 5 {
		t.Fatalf("priority mismatch: %d", got.Priority)
	}
	if !got.Enabled {
		t.Fatal("expected enabled=true")
	}
	if got.Deleted {
		t.Fatal("expected deleted=false")
	}
	if got.DeletedAt != nil {
		t.Fatal("expected deleted_at=nil")
	}
}

func TestDirectiveStore_Parity_SoftDelete(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	now := time.Now().UTC().Truncate(time.Millisecond)
	d := &Directive{
		DirectiveID: "d_parity_del",
		Title:       "Parity delete",
		Category:    "general",
		Priority:    1,
		Owner:       "silas",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	_, err := store.Create(ctx, d)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	_, err = store.Delete(ctx, "d_parity_del")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	pyDoc := testutil.PythonReadFixture(t, "directives", "d_parity_del")
	if pyDoc != nil {
		if delVal, ok := pyDoc["deleted"]; !ok || delVal != true {
			t.Fatalf("expected Python read nil or deleted=true after soft delete, got %+v", pyDoc)
		}
	}

	_, err = store.Restore(ctx, "d_parity_del")
	if err != nil {
		t.Fatalf("Restore failed: %v", err)
	}
	pyDoc = testutil.PythonReadFixture(t, "directives", "d_parity_del")
	if pyDoc == nil {
		t.Fatal("Python read nil after restore")
	}
	if delVal, ok := pyDoc["deleted"]; ok && delVal == true {
		t.Fatalf("expected deleted=false after restore, got %v", delVal)
	}
}
