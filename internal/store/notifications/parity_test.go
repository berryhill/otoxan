package notifications

import (
	"context"
	"testing"
	"time"

	"github.com/silas/otoxan/internal/testutil"
)

func TestNotificationStore_Parity_GoWritePythonRead(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	now := time.Now().UTC().Truncate(time.Millisecond)
	n := &Notification{
		NotificationID: "n_parity_gwpr",
		RecipientID:    "silas",
		Channel:        ChannelInApp,
		Status:         StatusPending,
		Title:          "Parity GWPR",
		Body:           "Go writes, Python reads",
		Payload:        map[string]interface{}{},
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	_, err := store.Create(ctx, n)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	pyDoc := testutil.PythonReadFixture(t, "notifications", "n_parity_gwpr")
	if pyDoc == nil {
		t.Fatal("Python read returned nil")
	}
	testutil.NormalizeTimeFields(t, pyDoc)

	testutil.AssertParityString(t, pyDoc, "notification_id", "n_parity_gwpr")
	testutil.AssertParityString(t, pyDoc, "recipient_id", "silas")
	testutil.AssertParityString(t, pyDoc, "channel", "in_app")
	testutil.AssertParityString(t, pyDoc, "status", "PENDING")
	testutil.AssertParityString(t, pyDoc, "title", "Parity GWPR")
	testutil.AssertParityString(t, pyDoc, "body", "Go writes, Python reads")

	if delVal, ok := pyDoc["deleted"]; ok && delVal == true {
		t.Fatalf("expected deleted absent/false, got %v", delVal)
	}
	if _, ok := pyDoc["deleted_at"]; ok {
		t.Fatal("expected deleted_at absent for live document")
	}
}

func TestNotificationStore_Parity_PythonWriteGoRead(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	testutil.PythonWriteFixture(t, "notifications", "n_parity_pwgr")

	got, err := store.Get(ctx, "n_parity_pwgr")
	if err != nil {
		t.Fatalf("Go read failed: %v", err)
	}
	if got.NotificationID != "n_parity_pwgr" {
		t.Fatalf("notification_id mismatch: %s", got.NotificationID)
	}
	if got.RecipientID != "silas" {
		t.Fatalf("recipient_id mismatch: %s", got.RecipientID)
	}
	if got.Channel != ChannelInApp {
		t.Fatalf("channel mismatch: %s", got.Channel)
	}
	if got.Status != StatusPending {
		t.Fatalf("status mismatch: %s", got.Status)
	}
	if got.Deleted {
		t.Fatal("expected deleted=false")
	}
	if got.DeletedAt != nil {
		t.Fatal("expected deleted_at=nil")
	}
}

func TestNotificationStore_Parity_SoftDelete(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	now := time.Now().UTC().Truncate(time.Millisecond)
	n := &Notification{
		NotificationID: "n_parity_del",
		RecipientID:    "silas",
		Channel:        ChannelInApp,
		Status:         StatusPending,
		Title:          "Parity delete",
		Body:           "",
		Payload:        map[string]interface{}{},
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	_, err := store.Create(ctx, n)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	_, err = store.Delete(ctx, "n_parity_del")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	pyDoc := testutil.PythonReadFixture(t, "notifications", "n_parity_del")
	if pyDoc != nil {
		if delVal, ok := pyDoc["deleted"]; !ok || delVal != true {
			t.Fatalf("expected Python read nil or deleted=true after soft delete, got %+v", pyDoc)
		}
	}

	_, err = store.Restore(ctx, "n_parity_del")
	if err != nil {
		t.Fatalf("Restore failed: %v", err)
	}
	pyDoc = testutil.PythonReadFixture(t, "notifications", "n_parity_del")
	if pyDoc == nil {
		t.Fatal("Python read nil after restore")
	}
	if delVal, ok := pyDoc["deleted"]; ok && delVal == true {
		t.Fatalf("expected deleted=false after restore, got %v", delVal)
	}
}
