package reports

import (
	"context"
	"testing"
	"time"

	"github.com/silas/otoxan/internal/testutil"
)

func TestReportStore_Parity_GoWritePythonRead(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	now := time.Now().UTC().Truncate(time.Millisecond)
	r := &Report{
		ReportID:       "r_parity_gwpr",
		Title:          "Parity GWPR",
		Status:         StatusDraft,
		Owner:          "silas",
		CreatedAt:      now,
		UpdatedAt:      now,
		Content:        "Go writes, Python reads",
		Tags:           []string{"parity"},
		CreatedSession: "sess_1",
	}

	_, err := store.Create(ctx, r)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	pyDoc := testutil.PythonReadFixture(t, "reports", "r_parity_gwpr")
	if pyDoc == nil {
		t.Fatal("Python read returned nil")
	}
	testutil.NormalizeTimeFields(t, pyDoc)

	assertParityString(t, pyDoc, "report_id", "r_parity_gwpr")
	assertParityString(t, pyDoc, "title", "Parity GWPR")
	assertParityString(t, pyDoc, "status", "DRAFT")
	assertParityString(t, pyDoc, "owner", "silas")
	assertParityString(t, pyDoc, "content", "Go writes, Python reads")
	assertParityString(t, pyDoc, "created_session", "sess_1")

	if delVal, ok := pyDoc["deleted"]; ok && delVal == true {
		t.Fatalf("expected deleted absent/false, got %v", delVal)
	}
	if _, ok := pyDoc["deleted_at"]; ok {
		t.Fatal("expected deleted_at absent for live document")
	}
}

func TestReportStore_Parity_PythonWriteGoRead(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	testutil.PythonWriteFixture(t, "reports", "r_parity_pwgr")

	got, err := store.Get(ctx, "r_parity_pwgr")
	if err != nil {
		t.Fatalf("Go read failed: %v", err)
	}
	if got.ReportID != "r_parity_pwgr" {
		t.Fatalf("report_id mismatch: %s", got.ReportID)
	}
	if got.Title != "Parity fixture" {
		t.Fatalf("title mismatch: %s", got.Title)
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

func TestReportStore_Parity_SoftDelete(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	now := time.Now().UTC().Truncate(time.Millisecond)
	r := &Report{
		ReportID:  "r_parity_del",
		Title:     "Parity delete",
		Status:    StatusDraft,
		Owner:     "silas",
		CreatedAt: now,
		UpdatedAt: now,
	}
	_, err := store.Create(ctx, r)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	_, err = store.Delete(ctx, "r_parity_del")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	pyDoc := testutil.PythonReadFixture(t, "reports", "r_parity_del")
	if pyDoc != nil {
		if delVal, ok := pyDoc["deleted"]; !ok || delVal != true {
			t.Fatalf("expected Python read nil or deleted=true after soft delete, got %+v", pyDoc)
		}
	}

	_, err = store.Restore(ctx, "r_parity_del")
	if err != nil {
		t.Fatalf("Restore failed: %v", err)
	}
	pyDoc = testutil.PythonReadFixture(t, "reports", "r_parity_del")
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
