package teams

import (
	"context"
	"testing"
	"time"

	"github.com/silas/otoxan/internal/testutil"
)

func TestTeamStore_Parity_GoWritePythonRead(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	now := time.Now().UTC().Truncate(time.Millisecond)
	team := &Team{
		TeamID:    "tm_parity_gwpr",
		Name:      "Parity GWPR",
		DBName:    "silas",
		Members:   []Member{},
		Artifacts: map[string]interface{}{},
		Status:    StatusForming,
		CreatedAt: now,
		UpdatedAt: now,
	}

	_, err := store.Create(ctx, team)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	pyDoc := testutil.PythonReadFixture(t, "teams", "tm_parity_gwpr")
	if pyDoc == nil {
		t.Fatal("Python read returned nil")
	}
	testutil.NormalizeTimeFields(t, pyDoc)

	testutil.AssertParityString(t, pyDoc, "team_id", "tm_parity_gwpr")
	testutil.AssertParityString(t, pyDoc, "name", "Parity GWPR")
	testutil.AssertParityString(t, pyDoc, "db_name", "silas")
	testutil.AssertParityString(t, pyDoc, "status", "FORMING")

	if delVal, ok := pyDoc["deleted"]; ok && delVal == true {
		t.Fatalf("expected deleted absent/false, got %v", delVal)
	}
	if _, ok := pyDoc["deleted_at"]; ok {
		t.Fatal("expected deleted_at absent for live document")
	}
}

func TestTeamStore_Parity_PythonWriteGoRead(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	testutil.PythonWriteFixture(t, "teams", "tm_parity_pwgr")

	got, err := store.Get(ctx, "tm_parity_pwgr")
	if err != nil {
		t.Fatalf("Go read failed: %v", err)
	}
	if got.TeamID != "tm_parity_pwgr" {
		t.Fatalf("team_id mismatch: %s", got.TeamID)
	}
	if got.Name != "Parity fixture" {
		t.Fatalf("name mismatch: %s", got.Name)
	}
	if got.Status != StatusForming {
		t.Fatalf("status mismatch: %s", got.Status)
	}
	if got.Deleted {
		t.Fatal("expected deleted=false")
	}
	if got.DeletedAt != nil {
		t.Fatal("expected deleted_at=nil")
	}
}

func TestTeamStore_Parity_SoftDelete(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	now := time.Now().UTC().Truncate(time.Millisecond)
	team := &Team{
		TeamID:    "tm_parity_del",
		Name:      "Parity delete",
		DBName:    "silas",
		Members:   []Member{},
		Artifacts: map[string]interface{}{},
		Status:    StatusForming,
		CreatedAt: now,
		UpdatedAt: now,
	}
	_, err := store.Create(ctx, team)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	_, err = store.Delete(ctx, "tm_parity_del")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	pyDoc := testutil.PythonReadFixture(t, "teams", "tm_parity_del")
	if pyDoc != nil {
		if delVal, ok := pyDoc["deleted"]; !ok || delVal != true {
			t.Fatalf("expected Python read nil or deleted=true after soft delete, got %+v", pyDoc)
		}
	}

	_, err = store.Restore(ctx, "tm_parity_del")
	if err != nil {
		t.Fatalf("Restore failed: %v", err)
	}
	pyDoc = testutil.PythonReadFixture(t, "teams", "tm_parity_del")
	if pyDoc == nil {
		t.Fatal("Python read nil after restore")
	}
	if delVal, ok := pyDoc["deleted"]; ok && delVal == true {
		t.Fatalf("expected deleted=false after restore, got %v", delVal)
	}
}
