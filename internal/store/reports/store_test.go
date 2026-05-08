package reports

import (
	"context"
	"fmt"
	"os"
	"strings"
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

// newTestStore returns a ReportStore backed by a fresh test collection.
func newTestStore(t *testing.T, client *mongo.Client) *ReportStore {
	t.Helper()
	db := client.Database("silas")
	coll := db.Collection("reports")
	return NewReportStore(coll)
}

// makeMinimalReport returns a report with only required fields set.
func makeMinimalReport(id, title string) *Report {
	now := time.Now().UTC().Truncate(time.Millisecond)
	return &Report{
		ReportID: id,
		Title:    title,
		Status:   StatusDraft,
		Owner:    "silas",
		Tags:     []string{},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// ------------------------------------------------------------------
// CRUD round-trip tests
// ------------------------------------------------------------------

func TestReportStore_CreateAndGet(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	report := makeMinimalReport("r_001", "Q1 Analysis")
	report.Content = "# Q1 Analysis\n\nResults..."
	res, err := store.Create(ctx, report)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if res.InsertedID == nil {
		t.Fatal("expected InsertedID")
	}

	got, err := store.Get(ctx, "r_001")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.ReportID != "r_001" {
		t.Fatalf("expected report_id r_001, got %s", got.ReportID)
	}
	if got.Title != "Q1 Analysis" {
		t.Fatalf("expected title 'Q1 Analysis', got %s", got.Title)
	}
	if got.Status != StatusDraft {
		t.Fatalf("expected status DRAFT, got %s", got.Status)
	}
	if got.Owner != "silas" {
		t.Fatalf("expected owner silas, got %s", got.Owner)
	}
	if got.Content != "# Q1 Analysis\n\nResults..." {
		t.Fatalf("expected content mismatch, got %s", got.Content)
	}
	if len(got.Tags) != 0 {
		t.Fatalf("expected empty tags, got %v", got.Tags)
	}
}

func TestReportStore_CreateAppliesDefaults(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	now := time.Now().UTC().Truncate(time.Millisecond)
	report := &Report{
		ReportID:  "r_def",
		Title:     "Default test",
		CreatedAt: now,
		UpdatedAt: now,
	}

	_, err := store.Create(ctx, report)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	got, err := store.Get(ctx, "r_def")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.Status != StatusDraft {
		t.Fatalf("expected default status DRAFT, got %s", got.Status)
	}
	if got.Owner != "" {
		t.Fatalf("expected empty owner, got %s", got.Owner)
	}
	if len(got.Tags) != 0 {
		t.Fatalf("expected default empty tags, got %v", got.Tags)
	}
}

func TestReportStore_CreateRejectsOversizedDoc(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	// Create a report with a body that exceeds 16MB when BSON-encoded
	report := makeMinimalReport("r_big", "Oversized report")
	report.Content = strings.Repeat("x", MaxBSONDocSize)

	_, err := store.Create(ctx, report)
	if err == nil {
		t.Fatal("expected error for oversized document, got nil")
	}
	if !strings.Contains(err.Error(), "16MB") {
		t.Fatalf("expected 16MB limit error, got: %v", err)
	}
}

func TestReportStore_Update(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	report := makeMinimalReport("r_upd", "Update me")
	_, err := store.Create(ctx, report)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	ures, err := store.Update(ctx, "r_upd", bson.M{
		"status":  StatusPublished,
		"content": "Updated content",
		"tags":    []string{"research"},
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if ures.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1, got %d", ures.ModifiedCount)
	}

	got, err := store.Get(ctx, "r_upd")
	if err != nil {
		t.Fatalf("Get after update failed: %v", err)
	}
	if got.Status != StatusPublished {
		t.Fatalf("expected status PUBLISHED, got %s", got.Status)
	}
	if got.Content != "Updated content" {
		t.Fatalf("expected content 'Updated content', got %s", got.Content)
	}
	if len(got.Tags) != 1 || got.Tags[0] != "research" {
		t.Fatalf("expected tags [research], got %v", got.Tags)
	}
	if got.UpdatedAt.Before(report.UpdatedAt) {
		t.Fatal("expected updated_at to be newer than created_at")
	}
}

func TestReportStore_SoftDelete(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	report := makeMinimalReport("r_del", "Delete me")
	_, err := store.Create(ctx, report)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	dres, err := store.Delete(ctx, "r_del")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if dres.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1, got %d", dres.ModifiedCount)
	}

	_, err = store.Get(ctx, "r_del")
	if err != mongo.ErrNoDocuments {
		t.Fatalf("expected ErrNoDocuments after soft delete, got %v", err)
	}

	got, err := store.GetWithDeleted(ctx, "r_del")
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

func TestReportStore_Restore(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	report := makeMinimalReport("r_res", "Restore me")
	_, err := store.Create(ctx, report)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	_, _ = store.Delete(ctx, "r_res")

	rres, err := store.Restore(ctx, "r_res")
	if err != nil {
		t.Fatalf("Restore failed: %v", err)
	}
	if rres.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1 on restore, got %d", rres.ModifiedCount)
	}

	got, err := store.Get(ctx, "r_res")
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

func TestReportStore_HardDelete(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	report := makeMinimalReport("r_hard", "Hard delete me")
	_, err := store.Create(ctx, report)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	dres, err := store.HardDelete(ctx, "r_hard")
	if err != nil {
		t.Fatalf("HardDelete failed: %v", err)
	}
	if dres.DeletedCount != 1 {
		t.Fatalf("expected DeletedCount=1, got %d", dres.DeletedCount)
	}

	_, err = store.GetWithDeleted(ctx, "r_hard")
	if err != mongo.ErrNoDocuments {
		t.Fatalf("expected ErrNoDocuments after hard delete, got %v", err)
	}
}

func TestReportStore_List(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	reports := []*Report{
		makeMinimalReport("r_l1", "Report one"),
		makeMinimalReport("r_l2", "Report two"),
		makeMinimalReport("r_l3", "Report three"),
	}
	reports[0].Status = StatusDraft
	reports[0].Tags = []string{"research"}
	reports[1].Status = StatusPublished
	reports[1].Tags = []string{"analysis"}
	reports[2].Status = StatusDraft
	reports[2].Tags = []string{"research"}

	for _, report := range reports {
		_, err := store.Create(ctx, report)
		if err != nil {
			t.Fatalf("Create failed: %v", err)
		}
	}

	all, err := store.List(ctx, ListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("List all failed: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 reports, got %d", len(all))
	}

	drafts, err := store.List(ctx, ListOptions{Status: []ReportStatus{StatusDraft}})
	if err != nil {
		t.Fatalf("List drafts failed: %v", err)
	}
	if len(drafts) != 2 {
		t.Fatalf("expected 2 draft reports, got %d", len(drafts))
	}

	research, err := store.List(ctx, ListOptions{Tag: "research"})
	if err != nil {
		t.Fatalf("List by tag failed: %v", err)
	}
	if len(research) != 2 {
		t.Fatalf("expected 2 research reports, got %d", len(research))
	}

	limited, err := store.List(ctx, ListOptions{Limit: 1})
	if err != nil {
		t.Fatalf("List limited failed: %v", err)
	}
	if len(limited) != 1 {
		t.Fatalf("expected 1 report with limit, got %d", len(limited))
	}
}

func TestReportStore_ListWithDeleted(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	report := makeMinimalReport("r_ld", "List deleted")
	_, err := store.Create(ctx, report)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	_, _ = store.Delete(ctx, "r_ld")

	live, err := store.List(ctx, ListOptions{})
	if err != nil {
		t.Fatalf("List live failed: %v", err)
	}
	if len(live) != 0 {
		t.Fatalf("expected 0 live reports, got %d", len(live))
	}

	withDeleted, err := store.List(ctx, ListOptions{IncludeDeleted: true})
	if err != nil {
		t.Fatalf("List with deleted failed: %v", err)
	}
	if len(withDeleted) != 1 {
		t.Fatalf("expected 1 report with include_deleted, got %d", len(withDeleted))
	}
	if withDeleted[0].ReportID != "r_ld" {
		t.Fatalf("expected report_id r_ld, got %s", withDeleted[0].ReportID)
	}
}

func TestReportStore_Count(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	for i := 0; i < 5; i++ {
		report := makeMinimalReport(fmt.Sprintf("r_c%d", i), fmt.Sprintf("Count report %d", i))
		_, err := store.Create(ctx, report)
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

func TestReportStore_Archive(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	report := makeMinimalReport("r_arch", "Archive me")
	_, err := store.Create(ctx, report)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	ares, err := store.Archive(ctx, "r_arch")
	if err != nil {
		t.Fatalf("Archive failed: %v", err)
	}
	if ares.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1, got %d", ares.ModifiedCount)
	}

	got, err := store.Get(ctx, "r_arch")
	if err != nil {
		t.Fatalf("Get after archive failed: %v", err)
	}
	if got.Status != StatusArchived {
		t.Fatalf("expected status ARCHIVED, got %s", got.Status)
	}
	if got.ArchivedAt == nil {
		t.Fatal("expected archived_at to be set")
	}

	// Should not appear in non-archived list
	live, err := store.List(ctx, ListOptions{})
	if err != nil {
		t.Fatalf("List live failed: %v", err)
	}
	if len(live) != 0 {
		t.Fatalf("expected 0 live reports after archive, got %d", len(live))
	}

	// Unarchive
	_, err = store.Unarchive(ctx, "r_arch")
	if err != nil {
		t.Fatalf("Unarchive failed: %v", err)
	}

	got, err = store.Get(ctx, "r_arch")
	if err != nil {
		t.Fatalf("Get after unarchive failed: %v", err)
	}
	if got.Status != StatusDraft {
		t.Fatalf("expected status DRAFT after unarchive, got %s", got.Status)
	}
	if got.ArchivedAt != nil {
		t.Fatal("expected archived_at to be nil after unarchive")
	}
}

func TestReportStore_PublishUnpublish(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	report := makeMinimalReport("r_pub", "Publish me")
	_, err := store.Create(ctx, report)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Publish
	pres, err := store.Publish(ctx, "r_pub")
	if err != nil {
		t.Fatalf("Publish failed: %v", err)
	}
	if pres.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1 on publish, got %d", pres.ModifiedCount)
	}

	got, err := store.Get(ctx, "r_pub")
	if err != nil {
		t.Fatalf("Get after publish failed: %v", err)
	}
	if got.Status != StatusPublished {
		t.Fatalf("expected status PUBLISHED, got %s", got.Status)
	}

	// Unpublish
	_, err = store.Unpublish(ctx, "r_pub")
	if err != nil {
		t.Fatalf("Unpublish failed: %v", err)
	}

	got, err = store.Get(ctx, "r_pub")
	if err != nil {
		t.Fatalf("Get after unpublish failed: %v", err)
	}
	if got.Status != StatusDraft {
		t.Fatalf("expected status DRAFT after unpublish, got %s", got.Status)
	}
}

func TestReportStore_LinkPlan(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	report := makeMinimalReport("r_link", "Linked report")
	_, err := store.Create(ctx, report)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	lres, err := store.LinkPlan(ctx, "r_link", "plan_001")
	if err != nil {
		t.Fatalf("LinkPlan failed: %v", err)
	}
	if lres.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1, got %d", lres.ModifiedCount)
	}

	got, err := store.Get(ctx, "r_link")
	if err != nil {
		t.Fatalf("Get after link failed: %v", err)
	}
	if got.LinkedPlanID == nil || *got.LinkedPlanID != "plan_001" {
		t.Fatalf("expected linked_plan_id plan_001, got %v", got.LinkedPlanID)
	}

	// Unlink
	_, err = store.UnlinkPlan(ctx, "r_link")
	if err != nil {
		t.Fatalf("UnlinkPlan failed: %v", err)
	}

	got, err = store.Get(ctx, "r_link")
	if err != nil {
		t.Fatalf("Get after unlink failed: %v", err)
	}
	if got.LinkedPlanID != nil {
		t.Fatalf("expected linked_plan_id nil after unlink, got %v", got.LinkedPlanID)
	}
}

// ------------------------------------------------------------------
// Fixture round-trip test
// ------------------------------------------------------------------

func TestReportStore_FullCRUDRoundTrip(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	// 1. Create
	linkedPlanID := "plan_001"
	report := &Report{
		ReportID:        "r_round",
		Title:           "Round-trip report",
		Status:          StatusDraft,
		Owner:           "silas",
		Content:         "# Round-trip report\n\nThis is a comprehensive analysis...",
		Tags:            []string{"analysis", "q1"},
		LinkedPlanID:    &linkedPlanID,
		CreatedSession:  "sess_001",
		UpdatedSessions: []string{"sess_001"},
		CreatedAt:       time.Now().UTC().Truncate(time.Millisecond),
		UpdatedAt:       time.Now().UTC().Truncate(time.Millisecond),
	}

	_, err := store.Create(ctx, report)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// 2. Read back
	got, err := store.Get(ctx, "r_round")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if got.ReportID != report.ReportID {
		t.Fatalf("report_id mismatch")
	}
	if got.Title != report.Title {
		t.Fatalf("title mismatch")
	}
	if got.Status != report.Status {
		t.Fatalf("status mismatch")
	}
	if got.Owner != report.Owner {
		t.Fatalf("owner mismatch")
	}
	if got.Content != report.Content {
		t.Fatalf("content mismatch")
	}
	if len(got.Tags) != 2 || got.Tags[0] != "analysis" || got.Tags[1] != "q1" {
		t.Fatalf("tags mismatch: %v", got.Tags)
	}
	if got.LinkedPlanID == nil || *got.LinkedPlanID != linkedPlanID {
		t.Fatalf("linked_plan_id mismatch")
	}
	if got.CreatedSession != "sess_001" {
		t.Fatalf("created_session mismatch")
	}

	// 3. Update
	ures, err := store.Update(ctx, "r_round", bson.M{
		"status":  StatusPublished,
		"content": "Updated round-trip content",
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if ures.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1, got %d", ures.ModifiedCount)
	}

	updated, err := store.Get(ctx, "r_round")
	if err != nil {
		t.Fatalf("Get after update failed: %v", err)
	}
	if updated.Status != StatusPublished {
		t.Fatalf("expected status PUBLISHED after update, got %s", updated.Status)
	}
	if updated.Content != "Updated round-trip content" {
		t.Fatalf("expected content updated, got %s", updated.Content)
	}

	// 4. Soft delete
	_, err = store.Delete(ctx, "r_round")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	_, err = store.Get(ctx, "r_round")
	if err != mongo.ErrNoDocuments {
		t.Fatalf("expected ErrNoDocuments after soft delete, got %v", err)
	}

	// 5. Restore
	_, err = store.Restore(ctx, "r_round")
	if err != nil {
		t.Fatalf("Restore failed: %v", err)
	}

	restored, err := store.Get(ctx, "r_round")
	if err != nil {
		t.Fatalf("Get after restore failed: %v", err)
	}
	if restored.Deleted {
		t.Fatal("expected deleted=false after restore")
	}
	if restored.DeletedAt != nil {
		t.Fatal("expected deleted_at=nil after restore")
	}
	if restored.Status != StatusPublished {
		t.Fatalf("expected status preserved as PUBLISHED after restore, got %s", restored.Status)
	}

	// 6. Hard delete
	_, err = store.HardDelete(ctx, "r_round")
	if err != nil {
		t.Fatalf("HardDelete failed: %v", err)
	}

	_, err = store.GetWithDeleted(ctx, "r_round")
	if err != mongo.ErrNoDocuments {
		t.Fatalf("expected ErrNoDocuments after hard delete, got %v", err)
	}
}
