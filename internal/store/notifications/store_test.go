package notifications

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

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

// newTestStore returns a NotificationStore backed by a fresh test collection.
func newTestStore(t *testing.T, client *mongo.Client) *NotificationStore {
	t.Helper()
	db := client.Database("silas")
	coll := db.Collection("notifications")
	return NewNotificationStore(coll)
}

// makeMinimalNotification returns a notification with only required fields set.
func makeMinimalNotification(id, recipientID, title string) *Notification {
	now := time.Now().UTC().Truncate(time.Millisecond)
	return &Notification{
		NotificationID: id,
		RecipientID:    recipientID,
		Title:          title,
		Body:           "Test body",
		Status:         StatusPending,
		Channel:        ChannelInApp,
		CreatedAt:      now,
		UpdatedAt:      now,
		Payload:        map[string]interface{}{},
	}
}

// ------------------------------------------------------------------
// CRUD round-trip tests
// ------------------------------------------------------------------

func TestNotificationStore_CreateAndGet(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	n := makeMinimalNotification("notif_001", "user_a", "Welcome")
	res, err := store.Create(ctx, n)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if res.InsertedID == nil {
		t.Fatal("expected InsertedID")
	}

	got, err := store.Get(ctx, "notif_001")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.NotificationID != "notif_001" {
		t.Fatalf("expected notification_id notif_001, got %s", got.NotificationID)
	}
	if got.RecipientID != "user_a" {
		t.Fatalf("expected recipient_id user_a, got %s", got.RecipientID)
	}
	if got.Title != "Welcome" {
		t.Fatalf("expected title 'Welcome', got %s", got.Title)
	}
	if got.Status != StatusPending {
		t.Fatalf("expected status PENDING, got %s", got.Status)
	}
	if got.Channel != ChannelInApp {
		t.Fatalf("expected channel in_app, got %s", got.Channel)
	}
	if len(got.Payload) != 0 {
		t.Fatalf("expected empty payload, got %v", got.Payload)
	}
}

func TestNotificationStore_CreateAppliesDefaults(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	now := time.Now().UTC().Truncate(time.Millisecond)
	n := &Notification{
		NotificationID: "notif_def",
		RecipientID:    "user_b",
		Title:          "Default test",
		Body:           "Body",
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	_, err := store.Create(ctx, n)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	got, err := store.Get(ctx, "notif_def")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.Status != StatusPending {
		t.Fatalf("expected default status PENDING, got %s", got.Status)
	}
	if got.Channel != ChannelInApp {
		t.Fatalf("expected default channel in_app, got %s", got.Channel)
	}
	if len(got.Payload) != 0 {
		t.Fatalf("expected empty payload default, got %v", got.Payload)
	}
}

func TestNotificationStore_Update(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	n := makeMinimalNotification("notif_upd", "user_a", "Update me")
	_, err := store.Create(ctx, n)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	ures, err := store.Update(ctx, "notif_upd", bson.M{
		"title":   "Updated title",
		"body":    "Updated body",
		"channel": ChannelSlack,
		"payload": map[string]interface{}{"url": "https://example.com"},
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if ures.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1, got %d", ures.ModifiedCount)
	}

	got, err := store.Get(ctx, "notif_upd")
	if err != nil {
		t.Fatalf("Get after update failed: %v", err)
	}
	if got.Title != "Updated title" {
		t.Fatalf("expected title 'Updated title', got %s", got.Title)
	}
	if got.Body != "Updated body" {
		t.Fatalf("expected body 'Updated body', got %s", got.Body)
	}
	if got.Channel != ChannelSlack {
		t.Fatalf("expected channel slack, got %s", got.Channel)
	}
	if got.Payload["url"] != "https://example.com" {
		t.Fatalf("expected payload url, got %v", got.Payload)
	}
	if got.UpdatedAt.Before(n.UpdatedAt) {
		t.Fatal("expected updated_at to be newer than created_at")
	}
}

func TestNotificationStore_SoftDelete(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	n := makeMinimalNotification("notif_del", "user_a", "Delete me")
	_, err := store.Create(ctx, n)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	dres, err := store.Delete(ctx, "notif_del")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if dres.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1, got %d", dres.ModifiedCount)
	}

	_, err = store.Get(ctx, "notif_del")
	if err != mongo.ErrNoDocuments {
		t.Fatalf("expected ErrNoDocuments after soft delete, got %v", err)
	}

	got, err := store.GetWithDeleted(ctx, "notif_del")
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

func TestNotificationStore_Restore(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	n := makeMinimalNotification("notif_res", "user_a", "Restore me")
	_, err := store.Create(ctx, n)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	_, _ = store.Delete(ctx, "notif_res")

	rres, err := store.Restore(ctx, "notif_res")
	if err != nil {
		t.Fatalf("Restore failed: %v", err)
	}
	if rres.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1 on restore, got %d", rres.ModifiedCount)
	}

	got, err := store.Get(ctx, "notif_res")
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

func TestNotificationStore_HardDelete(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	n := makeMinimalNotification("notif_hard", "user_a", "Hard delete me")
	_, err := store.Create(ctx, n)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	dres, err := store.HardDelete(ctx, "notif_hard")
	if err != nil {
		t.Fatalf("HardDelete failed: %v", err)
	}
	if dres.DeletedCount != 1 {
		t.Fatalf("expected DeletedCount=1, got %d", dres.DeletedCount)
	}

	_, err = store.GetWithDeleted(ctx, "notif_hard")
	if err != mongo.ErrNoDocuments {
		t.Fatalf("expected ErrNoDocuments after hard delete, got %v", err)
	}
}

func TestNotificationStore_List(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	notifications := []*Notification{
		makeMinimalNotification("notif_l1", "user_a", "One"),
		makeMinimalNotification("notif_l2", "user_b", "Two"),
		makeMinimalNotification("notif_l3", "user_a", "Three"),
	}
	notifications[0].Channel = ChannelInApp
	notifications[0].Status = StatusPending
	notifications[1].Channel = ChannelEmail
	notifications[1].Status = StatusSent
	notifications[2].Channel = ChannelInApp
	notifications[2].Status = StatusPending

	for _, n := range notifications {
		_, err := store.Create(ctx, n)
		if err != nil {
			t.Fatalf("Create failed: %v", err)
		}
	}

	all, err := store.List(ctx, ListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("List all failed: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 notifications, got %d", len(all))
	}

	userA, err := store.List(ctx, ListOptions{RecipientID: "user_a"})
	if err != nil {
		t.Fatalf("List by recipient failed: %v", err)
	}
	if len(userA) != 2 {
		t.Fatalf("expected 2 user_a notifications, got %d", len(userA))
	}

	email, err := store.List(ctx, ListOptions{Channel: []Channel{ChannelEmail}})
	if err != nil {
		t.Fatalf("List by channel failed: %v", err)
	}
	if len(email) != 1 {
		t.Fatalf("expected 1 email notification, got %d", len(email))
	}

	pending, err := store.List(ctx, ListOptions{Status: []NotificationStatus{StatusPending}})
	if err != nil {
		t.Fatalf("List by status failed: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("expected 2 pending notifications, got %d", len(pending))
	}

	limited, err := store.List(ctx, ListOptions{Limit: 1})
	if err != nil {
		t.Fatalf("List limited failed: %v", err)
	}
	if len(limited) != 1 {
		t.Fatalf("expected 1 notification with limit, got %d", len(limited))
	}
}

func TestNotificationStore_ListWithDeleted(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	n := makeMinimalNotification("notif_ld", "user_a", "List deleted")
	_, err := store.Create(ctx, n)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	_, _ = store.Delete(ctx, "notif_ld")

	live, err := store.List(ctx, ListOptions{})
	if err != nil {
		t.Fatalf("List live failed: %v", err)
	}
	if len(live) != 0 {
		t.Fatalf("expected 0 live notifications, got %d", len(live))
	}

	withDeleted, err := store.List(ctx, ListOptions{IncludeDeleted: true})
	if err != nil {
		t.Fatalf("List with deleted failed: %v", err)
	}
	if len(withDeleted) != 1 {
		t.Fatalf("expected 1 notification with include_deleted, got %d", len(withDeleted))
	}
	if withDeleted[0].NotificationID != "notif_ld" {
		t.Fatalf("expected notification_id notif_ld, got %s", withDeleted[0].NotificationID)
	}
}

func TestNotificationStore_Count(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	for i := 0; i < 5; i++ {
		n := makeMinimalNotification(fmt.Sprintf("notif_c%d", i), "user_a", fmt.Sprintf("Count %d", i))
		_, err := store.Create(ctx, n)
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

func TestNotificationStore_MarkSent(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	n := makeMinimalNotification("notif_sent", "user_a", "Mark sent")
	_, err := store.Create(ctx, n)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	sres, err := store.MarkSent(ctx, "notif_sent")
	if err != nil {
		t.Fatalf("MarkSent failed: %v", err)
	}
	if sres.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1, got %d", sres.ModifiedCount)
	}

	got, err := store.Get(ctx, "notif_sent")
	if err != nil {
		t.Fatalf("Get after mark sent failed: %v", err)
	}
	if got.Status != StatusSent {
		t.Fatalf("expected status SENT, got %s", got.Status)
	}
	if got.SentAt == nil {
		t.Fatal("expected sent_at to be set")
	}
}

func TestNotificationStore_MarkRead(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	n := makeMinimalNotification("notif_read", "user_a", "Mark read")
	_, err := store.Create(ctx, n)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	rres, err := store.MarkRead(ctx, "notif_read")
	if err != nil {
		t.Fatalf("MarkRead failed: %v", err)
	}
	if rres.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1, got %d", rres.ModifiedCount)
	}

	got, err := store.Get(ctx, "notif_read")
	if err != nil {
		t.Fatalf("Get after mark read failed: %v", err)
	}
	if got.Status != StatusRead {
		t.Fatalf("expected status READ, got %s", got.Status)
	}
	if got.ReadAt == nil {
		t.Fatal("expected read_at to be set")
	}
}

// ------------------------------------------------------------------
// Full CRUD round-trip with channel-typed payload
// ------------------------------------------------------------------

func TestNotificationStore_FullCRUDRoundTrip(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	// 1. Create
	n := &Notification{
		NotificationID: "notif_round",
		RecipientID:    "user_round",
		Channel:        ChannelEmail,
		Status:         StatusPending,
		Title:          "Round-trip notification",
		Body:           "This is a round-trip test body",
		Payload: map[string]interface{}{
			"action_url": "https://example.com/action",
			"priority":   "high",
		},
		CreatedAt: time.Now().UTC().Truncate(time.Millisecond),
		UpdatedAt: time.Now().UTC().Truncate(time.Millisecond),
	}

	_, err := store.Create(ctx, n)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// 2. Read back
	got, err := store.Get(ctx, "notif_round")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.NotificationID != n.NotificationID {
		t.Fatalf("notification_id mismatch")
	}
	if got.RecipientID != n.RecipientID {
		t.Fatalf("recipient_id mismatch")
	}
	if got.Channel != n.Channel {
		t.Fatalf("channel mismatch")
	}
	if got.Title != n.Title {
		t.Fatalf("title mismatch")
	}
	if got.Body != n.Body {
		t.Fatalf("body mismatch")
	}
	if got.Payload["action_url"] != "https://example.com/action" {
		t.Fatalf("payload mismatch")
	}

	// 3. Update
	ures, err := store.Update(ctx, "notif_round", bson.M{
		"title":   "Updated round-trip title",
		"channel": ChannelSlack,
		"payload": map[string]interface{}{"slack_channel": "#alerts"},
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if ures.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1, got %d", ures.ModifiedCount)
	}

	got, err = store.Get(ctx, "notif_round")
	if err != nil {
		t.Fatalf("Get after update failed: %v", err)
	}
	if got.Title != "Updated round-trip title" {
		t.Fatalf("title mismatch after update")
	}
	if got.Channel != ChannelSlack {
		t.Fatalf("channel mismatch after update")
	}
	if got.Payload["slack_channel"] != "#alerts" {
		t.Fatalf("payload mismatch after update")
	}

	// 4. Mark sent
	_, err = store.MarkSent(ctx, "notif_round")
	if err != nil {
		t.Fatalf("MarkSent failed: %v", err)
	}

	got, err = store.Get(ctx, "notif_round")
	if err != nil {
		t.Fatalf("Get after mark sent failed: %v", err)
	}
	if got.Status != StatusSent {
		t.Fatalf("expected status SENT after mark sent, got %s", got.Status)
	}
	if got.SentAt == nil {
		t.Fatal("expected sent_at to be set")
	}

	// 5. Mark read
	_, err = store.MarkRead(ctx, "notif_round")
	if err != nil {
		t.Fatalf("MarkRead failed: %v", err)
	}

	got, err = store.Get(ctx, "notif_round")
	if err != nil {
		t.Fatalf("Get after mark read failed: %v", err)
	}
	if got.Status != StatusRead {
		t.Fatalf("expected status READ after mark read, got %s", got.Status)
	}
	if got.ReadAt == nil {
		t.Fatal("expected read_at to be set")
	}

	// 6. Soft delete
	_, err = store.Delete(ctx, "notif_round")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	_, err = store.Get(ctx, "notif_round")
	if err != mongo.ErrNoDocuments {
		t.Fatalf("expected ErrNoDocuments after soft delete, got %v", err)
	}

	// 7. Restore
	_, err = store.Restore(ctx, "notif_round")
	if err != nil {
		t.Fatalf("Restore failed: %v", err)
	}

	got, err = store.Get(ctx, "notif_round")
	if err != nil {
		t.Fatalf("Get after restore failed: %v", err)
	}
	if got.Deleted {
		t.Fatalf("expected deleted=false after restore")
	}

	// 8. Hard delete
	_, err = store.HardDelete(ctx, "notif_round")
	if err != nil {
		t.Fatalf("HardDelete failed: %v", err)
	}

	_, err = store.GetWithDeleted(ctx, "notif_round")
	if err != mongo.ErrNoDocuments {
		t.Fatalf("expected ErrNoDocuments after hard delete, got %v", err)
	}
}
