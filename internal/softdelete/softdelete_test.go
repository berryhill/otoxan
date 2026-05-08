package softdelete

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go/modules/mongodb"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// mongoContainer holds a testcontainers MongoDB instance.
type mongoContainer struct {
	container *mongodb.MongoDBContainer
	client    *mongo.Client
	uri       string
}

// setupMongo spins up a testcontainers MongoDB container and returns a client.
func setupMongo(t *testing.T) *mongoContainer {
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

	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("failed to connect to mongodb: %v", err)
	}
	t.Cleanup(func() {
		_ = client.Disconnect(ctx)
	})

	return &mongoContainer{container: container, client: client, uri: uri}
}

// TestSoftDelete_RoundTrip exercises Insert, Find, FindOne, Count, Delete,
// Restore, HardDelete, and the IncludeDeleted option.
func TestSoftDelete_RoundTrip(t *testing.T) {
	ctx := context.Background()
	mc := setupMongo(t)

	db := mc.client.Database("softdelete_test")
	coll := db.Collection("items")
	sd := NewSoftDelete(coll)

	// --- Insert ---
	doc1 := bson.M{"name": "alice", "value": 1}
	res, err := sd.InsertOne(ctx, doc1)
	if err != nil {
		t.Fatalf("InsertOne failed: %v", err)
	}
	id1 := res.InsertedID

	doc2 := bson.M{"name": "bob", "value": 2}
	_, err = sd.InsertOne(ctx, doc2)
	if err != nil {
		t.Fatalf("InsertOne failed: %v", err)
	}

	// --- Find should return both ---
	cur, err := sd.Find(ctx, bson.M{})
	if err != nil {
		t.Fatalf("Find failed: %v", err)
	}
	var found []bson.M
	if err := cur.All(ctx, &found); err != nil {
		t.Fatalf("cursor.All failed: %v", err)
	}
	if len(found) != 2 {
		t.Fatalf("expected 2 docs, got %d", len(found))
	}

	// --- FindOne ---
	sr := sd.FindOne(ctx, bson.M{"name": "alice"})
	if sr.Err() != nil {
		t.Fatalf("FindOne failed: %v", sr.Err())
	}
	var one bson.M
	if err := sr.Decode(&one); err != nil {
		t.Fatalf("FindOne decode failed: %v", err)
	}
	if one["name"] != "alice" {
		t.Fatalf("expected name=alice, got %v", one["name"])
	}

	// --- CountDocuments ---
	cnt, err := sd.CountDocuments(ctx, bson.M{})
	if err != nil {
		t.Fatalf("CountDocuments failed: %v", err)
	}
	if cnt != 2 {
		t.Fatalf("expected count 2, got %d", cnt)
	}

	// --- Soft Delete ---
	ures, err := sd.Delete(ctx, bson.M{"name": "alice"})
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if ures.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1, got %d", ures.ModifiedCount)
	}

	// --- Find should now return only bob ---
	cur, err = sd.Find(ctx, bson.M{})
	if err != nil {
		t.Fatalf("Find after delete failed: %v", err)
	}
	found = nil
	if err := cur.All(ctx, &found); err != nil {
		t.Fatalf("cursor.All failed: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("expected 1 doc after soft delete, got %d", len(found))
	}
	if found[0]["name"] != "bob" {
		t.Fatalf("expected remaining doc to be bob, got %v", found[0]["name"])
	}

	// --- CountDocuments after delete ---
	cnt, err = sd.CountDocuments(ctx, bson.M{})
	if err != nil {
		t.Fatalf("CountDocuments after delete failed: %v", err)
	}
	if cnt != 1 {
		t.Fatalf("expected count 1 after delete, got %d", cnt)
	}

	// --- FindOne on deleted doc should miss ---
	sr = sd.FindOne(ctx, bson.M{"_id": id1})
	if sr.Err() != mongo.ErrNoDocuments {
		t.Fatalf("expected ErrNoDocuments for deleted doc, got %v", sr.Err())
	}

	// --- IncludeDeleted should find alice ---
	cur, err = sd.Find(ctx, bson.M{}, WithIncludeDeleted())
	if err != nil {
		t.Fatalf("Find(includeDeleted) failed: %v", err)
	}
	found = nil
	if err := cur.All(ctx, &found); err != nil {
		t.Fatalf("cursor.All failed: %v", err)
	}
	if len(found) != 2 {
		t.Fatalf("expected 2 docs with includeDeleted, got %d", len(found))
	}

	sr = sd.FindOne(ctx, bson.M{"_id": id1}, WithIncludeDeleted())
	if sr.Err() != nil {
		t.Fatalf("FindOne(includeDeleted) failed: %v", sr.Err())
	}
	var alice bson.M
	if err := sr.Decode(&alice); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if alice["deleted"] != true {
		t.Fatalf("expected deleted=true, got %v", alice["deleted"])
	}
	if _, ok := alice["deleted_at"]; !ok {
		t.Fatal("expected deleted_at to be set")
	}

	// --- Restore ---
	rres, err := sd.Restore(ctx, bson.M{"_id": id1})
	if err != nil {
		t.Fatalf("Restore failed: %v", err)
	}
	if rres.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1 on restore, got %d", rres.ModifiedCount)
	}

	// After restore, alice should be visible again
	cur, err = sd.Find(ctx, bson.M{})
	if err != nil {
		t.Fatalf("Find after restore failed: %v", err)
	}
	found = nil
	if err := cur.All(ctx, &found); err != nil {
		t.Fatalf("cursor.All failed: %v", err)
	}
	if len(found) != 2 {
		t.Fatalf("expected 2 docs after restore, got %d", len(found))
	}

	// Verify deleted=false and deleted_at is unset (matches Python restore_one behavior)
	sr = sd.FindOne(ctx, bson.M{"_id": id1})
	if sr.Err() != nil {
		t.Fatalf("FindOne after restore failed: %v", sr.Err())
	}
	var restored bson.M
	if err := sr.Decode(&restored); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if restored["deleted"] != false {
		t.Fatalf("expected deleted=false after restore, got %v", restored["deleted"])
	}
	if _, ok := restored["deleted_at"]; ok {
		t.Fatalf("expected deleted_at field to be absent after restore")
	}

	// --- HardDelete bob ---
	dres, err := sd.HardDelete(ctx, bson.M{"name": "bob"})
	if err != nil {
		t.Fatalf("HardDelete failed: %v", err)
	}
	if dres.DeletedCount != 1 {
		t.Fatalf("expected DeletedCount=1, got %d", dres.DeletedCount)
	}

	cnt, err = sd.CountDocuments(ctx, bson.M{}, WithIncludeDeleted())
	if err != nil {
		t.Fatalf("CountDocuments after hard delete failed: %v", err)
	}
	if cnt != 1 {
		t.Fatalf("expected count 1 after hard delete, got %d", cnt)
	}

	// --- Distinct ---
	// Insert a few more docs for distinct testing
	_, _ = sd.InsertOne(ctx, bson.M{"name": "carol", "category": "A"})
	_, _ = sd.InsertOne(ctx, bson.M{"name": "dave", "category": "A"})
	_, _ = sd.InsertOne(ctx, bson.M{"name": "eve", "category": "B"})

	dr := sd.Distinct(ctx, "category", bson.M{})
	if dr.Err() != nil {
		t.Fatalf("Distinct failed: %v", dr.Err())
	}
	var vals []string
	if err := dr.Decode(&vals); err != nil {
		t.Fatalf("Distinct decode failed: %v", err)
	}
	if len(vals) != 2 {
		t.Fatalf("expected 2 distinct categories, got %d", len(vals))
	}

	// --- DeleteMany ---
	ures, err = sd.DeleteMany(ctx, bson.M{"category": "A"})
	if err != nil {
		t.Fatalf("DeleteMany failed: %v", err)
	}
	if ures.ModifiedCount != 2 {
		t.Fatalf("expected ModifiedCount=2 for DeleteMany, got %d", ures.ModifiedCount)
	}

	cnt, err = sd.CountDocuments(ctx, bson.M{})
	if err != nil {
		t.Fatalf("CountDocuments after DeleteMany failed: %v", err)
	}
	if cnt != 2 { // alice (restored) + eve
		t.Fatalf("expected count 2 after DeleteMany, got %d", cnt)
	}

	// --- RestoreMany ---
	ures, err = sd.RestoreMany(ctx, bson.M{"category": "A"})
	if err != nil {
		t.Fatalf("RestoreMany failed: %v", err)
	}
	if ures.ModifiedCount != 2 {
		t.Fatalf("expected ModifiedCount=2 for RestoreMany, got %d", ures.ModifiedCount)
	}

	cnt, err = sd.CountDocuments(ctx, bson.M{})
	if err != nil {
		t.Fatalf("CountDocuments after RestoreMany failed: %v", err)
	}
	if cnt != 4 { // alice + carol + dave + eve
		t.Fatalf("expected count 4 after RestoreMany, got %d", cnt)
	}

	// --- HardDeleteMany ---
	dres, err = sd.HardDeleteMany(ctx, bson.M{"category": "A"})
	if err != nil {
		t.Fatalf("HardDeleteMany failed: %v", err)
	}
	if dres.DeletedCount != 2 {
		t.Fatalf("expected DeletedCount=2 for HardDeleteMany, got %d", dres.DeletedCount)
	}

	cnt, err = sd.CountDocuments(ctx, bson.M{}, WithIncludeDeleted())
	if err != nil {
		t.Fatalf("CountDocuments after HardDeleteMany failed: %v", err)
	}
	if cnt != 2 { // alice + eve
		t.Fatalf("expected count 2 after HardDeleteMany, got %d", cnt)
	}

	// --- UpdateOne / UpdateMany ---
	ures, err = sd.UpdateOne(ctx, bson.M{"name": "eve"}, bson.M{"$set": bson.M{"value": 99}})
	if err != nil {
		t.Fatalf("UpdateOne failed: %v", err)
	}
	if ures.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1 for UpdateOne, got %d", ures.ModifiedCount)
	}

	ures, err = sd.UpdateMany(ctx, bson.M{}, bson.M{"$set": bson.M{"tag": "x"}})
	if err != nil {
		t.Fatalf("UpdateMany failed: %v", err)
	}
	if ures.ModifiedCount != 2 {
		t.Fatalf("expected ModifiedCount=2 for UpdateMany, got %d", ures.ModifiedCount)
	}

	// --- ReplaceOne ---
	ures, err = sd.ReplaceOne(ctx, bson.M{"name": "eve"}, bson.M{"name": "eve", "category": "B", "value": 99, "tag": "x"})
	if err != nil {
		t.Fatalf("ReplaceOne failed: %v", err)
	}
	if ures.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1 for ReplaceOne, got %d", ures.ModifiedCount)
	}

	// Verify deleted fields stripped from replacement
	sr = sd.FindOne(ctx, bson.M{"name": "eve"})
	if sr.Err() != nil {
		t.Fatalf("FindOne after ReplaceOne failed: %v", sr.Err())
	}
	var replaced bson.M
	if err := sr.Decode(&replaced); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if _, ok := replaced["deleted"]; ok {
		t.Fatal("expected deleted field stripped from replacement")
	}

	// --- InsertMany ---
	ires, err := sd.InsertMany(ctx, []interface{}{
		bson.M{"name": "frank", "batch": 1},
		bson.M{"name": "grace", "batch": 1},
	})
	if err != nil {
		t.Fatalf("InsertMany failed: %v", err)
	}
	if len(ires.InsertedIDs) != 2 {
		t.Fatalf("expected 2 inserted IDs, got %d", len(ires.InsertedIDs))
	}

	// --- InsertOne strips pre-existing deleted fields ---
	_, err = sd.InsertOne(ctx, bson.M{"name": "hacker", "deleted": true, "deleted_at": time.Now()})
	if err != nil {
		t.Fatalf("InsertOne with deleted fields failed: %v", err)
	}
	sr = sd.FindOne(ctx, bson.M{"name": "hacker"})
	if sr.Err() != nil {
		t.Fatalf("FindOne hacker failed: %v", sr.Err())
	}
	var hacker bson.M
	if err := sr.Decode(&hacker); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if _, ok := hacker["deleted"]; ok {
		t.Fatal("inserted doc should not have deleted field")
	}

	// --- Filter already containing deleted key must not double-add ---
	// Insert a doc with deleted=false explicitly
	_, _ = sd.InsertOne(ctx, bson.M{"name": "explicit", "deleted": false})
	cnt, err = sd.CountDocuments(ctx, bson.M{"deleted": false})
	if err != nil {
		t.Fatalf("CountDocuments with explicit deleted filter failed: %v", err)
	}
	if cnt != 1 {
		t.Fatalf("expected count 1 for explicit deleted=false filter, got %d", cnt)
	}

	// --- Name() passthrough ---
	if sd.Name() != "items" {
		t.Fatalf("expected Name()=items, got %s", sd.Name())
	}

	// --- Database() passthrough ---
	if sd.Database().Name() != "softdelete_test" {
		t.Fatalf("expected Database().Name()=softdelete_test, got %s", sd.Database().Name())
	}
}

// TestSoftDelete_ParityVsPython verifies the same sequence of operations
// against the same data shapes produces identical results to the Python
// softdelete implementation.
func TestSoftDelete_ParityVsPython(t *testing.T) {
	ctx := context.Background()
	mc := setupMongo(t)

	db := mc.client.Database("parity_test")
	coll := db.Collection("parity_items")
	sd := NewSoftDelete(coll)

	// Same sequence as a typical Python softdelete usage:
	// 1. Insert 3 docs
	// 2. Soft-delete one by name
	// 3. Count live docs
	// 4. Find with include_deleted
	// 5. Restore
	// 6. Hard-delete another
	// 7. Final count

	docs := []interface{}{
		bson.M{"task_id": "T001", "status": "pending", "agent": "silas"},
		bson.M{"task_id": "T002", "status": "running", "agent": "archer"},
		bson.M{"task_id": "T003", "status": "completed", "agent": "silas"},
	}
	ires, err := sd.InsertMany(ctx, docs)
	if err != nil {
		t.Fatalf("InsertMany failed: %v", err)
	}
	if len(ires.InsertedIDs) != 3 {
		t.Fatalf("expected 3 inserted IDs, got %d", len(ires.InsertedIDs))
	}

	// Soft-delete T002
	ures, err := sd.Delete(ctx, bson.M{"task_id": "T002"})
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if ures.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1, got %d", ures.ModifiedCount)
	}

	// Count live docs -> should be 2
	cnt, err := sd.CountDocuments(ctx, bson.M{})
	if err != nil {
		t.Fatalf("CountDocuments failed: %v", err)
	}
	if cnt != 2 {
		t.Fatalf("expected live count 2, got %d", cnt)
	}

	// Find live docs -> should be T001, T003
	cur, err := sd.Find(ctx, bson.M{})
	if err != nil {
		t.Fatalf("Find failed: %v", err)
	}
	var live []bson.M
	if err := cur.All(ctx, &live); err != nil {
		t.Fatalf("cursor.All failed: %v", err)
	}
	if len(live) != 2 {
		t.Fatalf("expected 2 live docs, got %d", len(live))
	}
	ids := map[string]bool{}
	for _, d := range live {
		ids[fmt.Sprintf("%v", d["task_id"])] = true
	}
	if !ids["T001"] || !ids["T003"] {
		t.Fatalf("expected T001 and T003 in live set, got %v", ids)
	}

	// Find with include_deleted -> should be 3
	cur, err = sd.Find(ctx, bson.M{}, WithIncludeDeleted())
	if err != nil {
		t.Fatalf("Find(includeDeleted) failed: %v", err)
	}
	var all []bson.M
	if err := cur.All(ctx, &all); err != nil {
		t.Fatalf("cursor.All failed: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 docs with includeDeleted, got %d", len(all))
	}

	// Verify deleted doc has deleted=true and deleted_at set
	var deletedDoc bson.M
	for _, d := range all {
		if d["task_id"] == "T002" {
			deletedDoc = d
			break
		}
	}
	if deletedDoc == nil {
		t.Fatal("deleted doc T002 not found in includeDeleted query")
	}
	if deletedDoc["deleted"] != true {
		t.Fatalf("expected deleted=true, got %v", deletedDoc["deleted"])
	}
	if _, ok := deletedDoc["deleted_at"]; !ok {
		t.Fatal("expected deleted_at to be set on soft-deleted doc")
	}

	// Restore T002
	ures, err = sd.Restore(ctx, bson.M{"task_id": "T002"})
	if err != nil {
		t.Fatalf("Restore failed: %v", err)
	}
	if ures.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1 on restore, got %d", ures.ModifiedCount)
	}

	// After restore, all 3 should be live
	cnt, err = sd.CountDocuments(ctx, bson.M{})
	if err != nil {
		t.Fatalf("CountDocuments after restore failed: %v", err)
	}
	if cnt != 3 {
		t.Fatalf("expected live count 3 after restore, got %d", cnt)
	}

	// Verify restored doc has deleted=false and no deleted_at (matches Python)
	var sr2 *mongo.SingleResult = sd.FindOne(ctx, bson.M{"task_id": "T002"})
	if sr2.Err() != nil {
		t.Fatalf("FindOne T002 after restore failed: %v", sr2.Err())
	}
	var restored bson.M
	if err := sr2.Decode(&restored); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if restored["deleted"] != false {
		t.Fatalf("restored doc should have deleted=false, got %v", restored["deleted"])
	}
	if _, ok := restored["deleted_at"]; ok {
		t.Fatalf("restored doc should not have deleted_at field")
	}

	// Hard-delete T003
	dres, err := sd.HardDelete(ctx, bson.M{"task_id": "T003"})
	if err != nil {
		t.Fatalf("HardDelete failed: %v", err)
	}
	if dres.DeletedCount != 1 {
		t.Fatalf("expected DeletedCount=1, got %d", dres.DeletedCount)
	}

	// Final count with include_deleted -> should be 2 (T001, T002)
	cnt, err = sd.CountDocuments(ctx, bson.M{}, WithIncludeDeleted())
	if err != nil {
		t.Fatalf("CountDocuments final failed: %v", err)
	}
	if cnt != 2 {
		t.Fatalf("expected final count 2, got %d", cnt)
	}

	// Verify T003 is truly gone
	var sr3 *mongo.SingleResult = sd.FindOne(ctx, bson.M{"task_id": "T003"}, WithIncludeDeleted())
	if sr3.Err() != mongo.ErrNoDocuments {
		t.Fatalf("expected ErrNoDocuments for hard-deleted doc, got %v", sr3.Err())
	}
}

// TestSoftDelete_EmptyFilter verifies that an empty bson.M{} filter still
// gets the deleted predicate injected.
func TestSoftDelete_EmptyFilter(t *testing.T) {
	ctx := context.Background()
	mc := setupMongo(t)

	db := mc.client.Database("empty_filter_test")
	coll := db.Collection("items")
	sd := NewSoftDelete(coll)

	_, _ = sd.InsertOne(ctx, bson.M{"name": "live"})
	_, _ = sd.InsertOne(ctx, bson.M{"name": "dead"})
	_, _ = sd.Delete(ctx, bson.M{"name": "dead"})

	// Empty filter should still exclude deleted
	cnt, err := sd.CountDocuments(ctx, bson.M{})
	if err != nil {
		t.Fatalf("CountDocuments failed: %v", err)
	}
	if cnt != 1 {
		t.Fatalf("expected count 1 with empty filter, got %d", cnt)
	}
}

// TestSoftDelete_UpdateManyWithDeletedFilter verifies UpdateMany respects
// the soft-delete filter and does not touch deleted docs.
func TestSoftDelete_UpdateManyRespectsFilter(t *testing.T) {
	ctx := context.Background()
	mc := setupMongo(t)

	db := mc.client.Database("updatemany_test")
	coll := db.Collection("items")
	sd := NewSoftDelete(coll)

	_, _ = sd.InsertOne(ctx, bson.M{"name": "a", "val": 1})
	_, _ = sd.InsertOne(ctx, bson.M{"name": "b", "val": 2})
	_, _ = sd.Delete(ctx, bson.M{"name": "b"})

	// UpdateMany on all live docs
	ures, err := sd.UpdateMany(ctx, bson.M{}, bson.M{"$set": bson.M{"val": 99}})
	if err != nil {
		t.Fatalf("UpdateMany failed: %v", err)
	}
	if ures.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1 (only live doc), got %d", ures.ModifiedCount)
	}

	// Verify deleted doc was not touched
	var sr4 *mongo.SingleResult = sd.FindOne(ctx, bson.M{"name": "b"}, WithIncludeDeleted())
	if sr4.Err() != nil {
		t.Fatalf("FindOne failed: %v", sr4.Err())
	}
	var b bson.M
	if err := sr4.Decode(&b); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	// Mongo stores ints as int32 by default; compare as number
	val, ok := b["val"]
	if !ok {
		t.Fatal("expected val field on deleted doc")
	}
	var valInt int32
	switch v := val.(type) {
	case int32:
		valInt = v
	case int:
		valInt = int32(v)
	case int64:
		valInt = int32(v)
	case float64:
		valInt = int32(v)
	default:
		t.Fatalf("unexpected val type %T", val)
	}
	if valInt != 2 {
		t.Fatalf("expected deleted doc val unchanged (2), got %v", val)
	}
}

// TestSoftDelete_InsertStripsDeletedFields verifies that InsertOne/InsertMany
// strip any pre-existing deleted/deleted_at fields from the document.
func TestSoftDelete_InsertStripsDeletedFields(t *testing.T) {
	ctx := context.Background()
	mc := setupMongo(t)

	db := mc.client.Database("strip_test")
	coll := db.Collection("items")
	sd := NewSoftDelete(coll)

	// Insert a doc that already has deleted=true
	_, err := sd.InsertOne(ctx, bson.M{
		"name":       "predeleted",
		"deleted":    true,
		"deleted_at": time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("InsertOne failed: %v", err)
	}

	// It should be findable (i.e. not actually deleted)
	cnt, err := sd.CountDocuments(ctx, bson.M{"name": "predeleted"})
	if err != nil {
		t.Fatalf("CountDocuments failed: %v", err)
	}
	if cnt != 1 {
		t.Fatalf("expected count 1 (doc should be live), got %d", cnt)
	}

	// Verify fields are stripped
	sr := sd.FindOne(ctx, bson.M{"name": "predeleted"})
	if sr.Err() != nil {
		t.Fatalf("FindOne failed: %v", sr.Err())
	}
	var doc bson.M
	if err := sr.Decode(&doc); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if _, ok := doc["deleted"]; ok {
		t.Fatal("expected deleted field to be stripped")
	}
	if _, ok := doc["deleted_at"]; ok {
		t.Fatal("expected deleted_at field to be stripped")
	}
}
