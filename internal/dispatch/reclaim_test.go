package dispatch

import (
	"context"
	"fmt"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// ------------------------------------------------------------------
// Test helpers
// ------------------------------------------------------------------

func newReclaimTestColl(t *testing.T, client *mongo.Client) *mongo.Collection {
	t.Helper()
	return client.Database("silas").Collection(fmt.Sprintf("dispatch_reclaim_%d", time.Now().UnixNano()))
}

func seedRequestClaimedAt(ctx context.Context, coll *mongo.Collection, reqID, taskID string, claimedAt time.Time) error {
	doc := bson.M{
		"request_id": reqID,
		"task_id":    taskID,
		"status":     string(RequestClaimed),
		"claimed_at": claimedAt,
		"claimed_by": "agent-old",
		"created_at": time.Now().UTC(),
	}
	_, err := coll.InsertOne(ctx, doc)
	return err
}

// ------------------------------------------------------------------
// Unit tests
// ------------------------------------------------------------------

func TestReclaimStale_ResetsOldClaims(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	coll := newReclaimTestColl(t, client)

	now := time.Now().UTC()

	// Seed one stale (15 min old) and one fresh (1 min old) CLAIMED request.
	if err := seedRequestClaimedAt(ctx, coll, "dr_stale", "t_stale", now.Add(-15*time.Minute)); err != nil {
		t.Fatalf("seedRequestClaimedAt failed: %v", err)
	}
	if err := seedRequestClaimedAt(ctx, coll, "dr_fresh", "t_fresh", now.Add(-1*time.Minute)); err != nil {
		t.Fatalf("seedRequestClaimedAt failed: %v", err)
	}

	deps := ReclaimDeps{
		Collection:     coll,
		StaleThreshold: 10 * time.Minute,
	}

	n, err := reclaimStale(ctx, deps)
	if err != nil {
		t.Fatalf("reclaimStale failed: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 reclaimed, got %d", n)
	}

	// Verify stale request is now PENDING.
	var stale bson.M
	if err := coll.FindOne(ctx, bson.M{"request_id": "dr_stale"}).Decode(&stale); err != nil {
		t.Fatalf("stale lookup failed: %v", err)
	}
	if stale["status"] != string(RequestPending) {
		t.Errorf("expected stale status PENDING, got %v", stale["status"])
	}
	if stale["claimed_by"] != nil {
		t.Errorf("expected claimed_by nil, got %v", stale["claimed_by"])
	}
	if _, ok := stale["claimed_at"]; ok {
		t.Errorf("expected claimed_at unset, got %v", stale["claimed_at"])
	}

	// Verify fresh request is still CLAIMED.
	var fresh bson.M
	if err := coll.FindOne(ctx, bson.M{"request_id": "dr_fresh"}).Decode(&fresh); err != nil {
		t.Fatalf("fresh lookup failed: %v", err)
	}
	if fresh["status"] != string(RequestClaimed) {
		t.Errorf("expected fresh status CLAIMED, got %v", fresh["status"])
	}
	if fresh["claimed_by"] != "agent-old" {
		t.Errorf("expected claimed_by unchanged, got %v", fresh["claimed_by"])
	}
}

func TestReclaimStale_NoStaleClaims(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	coll := newReclaimTestColl(t, client)

	now := time.Now().UTC()

	// Seed only fresh CLAIMED requests.
	for i := 0; i < 3; i++ {
		if err := seedRequestClaimedAt(ctx, coll, fmt.Sprintf("dr_fresh_%d", i), fmt.Sprintf("t_fresh_%d", i), now.Add(-5*time.Minute)); err != nil {
			t.Fatalf("seedRequestClaimedAt failed: %v", err)
		}
	}

	deps := ReclaimDeps{
		Collection:     coll,
		StaleThreshold: 10 * time.Minute,
	}

	n, err := reclaimStale(ctx, deps)
	if err != nil {
		t.Fatalf("reclaimStale failed: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 reclaimed, got %d", n)
	}

	// All should still be CLAIMED.
	count, err := coll.CountDocuments(ctx, bson.M{"status": string(RequestClaimed)})
	if err != nil {
		t.Fatalf("count failed: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 CLAIMED, got %d", count)
	}
}

func TestReclaimStale_NoClaimedAt(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	coll := newReclaimTestColl(t, client)

	// Seed a CLAIMED request with no claimed_at field.
	doc := bson.M{
		"request_id": "dr_noclaimedat",
		"task_id":    "t_noclaimedat",
		"status":     string(RequestClaimed),
		"claimed_by": "agent-orphan",
		"created_at": time.Now().UTC().Add(-20 * time.Minute),
	}
	if _, err := coll.InsertOne(ctx, doc); err != nil {
		t.Fatalf("InsertOne failed: %v", err)
	}

	deps := ReclaimDeps{
		Collection:     coll,
		StaleThreshold: 10 * time.Minute,
	}

	// A CLAIMED doc without claimed_at is NOT matched by the $lt filter,
	// so it should NOT be reclaimed.
	n, err := reclaimStale(ctx, deps)
	if err != nil {
		t.Fatalf("reclaimStale failed: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 reclaimed for missing claimed_at, got %d", n)
	}

	var raw bson.M
	if err := coll.FindOne(ctx, bson.M{"request_id": "dr_noclaimedat"}).Decode(&raw); err != nil {
		t.Fatalf("lookup failed: %v", err)
	}
	if raw["status"] != string(RequestClaimed) {
		t.Errorf("expected status CLAIMED, got %v", raw["status"])
	}
}

func TestReclaimStale_DefaultThreshold(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	coll := newReclaimTestColl(t, client)

	now := time.Now().UTC()

	// Seed one 11-minute-old CLAIMED request.
	if err := seedRequestClaimedAt(ctx, coll, "dr_default", "t_default", now.Add(-11*time.Minute)); err != nil {
		t.Fatalf("seedRequestClaimedAt failed: %v", err)
	}

	// Use zero StaleThreshold — should default to 10 minutes.
	deps := ReclaimDeps{
		Collection: coll,
	}

	n, err := reclaimStale(ctx, deps)
	if err != nil {
		t.Fatalf("reclaimStale failed: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 reclaimed with default threshold, got %d", n)
	}
}

func TestReclaimStale_EmptyCollection(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	coll := newReclaimTestColl(t, client)

	deps := ReclaimDeps{
		Collection:     coll,
		StaleThreshold: 10 * time.Minute,
	}

	n, err := reclaimStale(ctx, deps)
	if err != nil {
		t.Fatalf("reclaimStale failed: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 reclaimed from empty collection, got %d", n)
	}
}

func TestReclaimStale_MultipleStale(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	coll := newReclaimTestColl(t, client)

	now := time.Now().UTC()

	// Seed 5 stale and 2 fresh.
	for i := 0; i < 5; i++ {
		if err := seedRequestClaimedAt(ctx, coll, fmt.Sprintf("dr_stale_%d", i), fmt.Sprintf("t_stale_%d", i), now.Add(-20*time.Minute)); err != nil {
			t.Fatalf("seedRequestClaimedAt failed: %v", err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := seedRequestClaimedAt(ctx, coll, fmt.Sprintf("dr_fresh_%d", i), fmt.Sprintf("t_fresh_%d", i), now.Add(-2*time.Minute)); err != nil {
			t.Fatalf("seedRequestClaimedAt failed: %v", err)
		}
	}

	deps := ReclaimDeps{
		Collection:     coll,
		StaleThreshold: 10 * time.Minute,
	}

	n, err := reclaimStale(ctx, deps)
	if err != nil {
		t.Fatalf("reclaimStale failed: %v", err)
	}
	if n != 5 {
		t.Fatalf("expected 5 reclaimed, got %d", n)
	}

	pendingCount, err := coll.CountDocuments(ctx, bson.M{"status": string(RequestPending)})
	if err != nil {
		t.Fatalf("count pending failed: %v", err)
	}
	if pendingCount != 5 {
		t.Errorf("expected 5 PENDING, got %d", pendingCount)
	}

	claimedCount, err := coll.CountDocuments(ctx, bson.M{"status": string(RequestClaimed)})
	if err != nil {
		t.Fatalf("count claimed failed: %v", err)
	}
	if claimedCount != 2 {
		t.Errorf("expected 2 CLAIMED, got %d", claimedCount)
	}
}

// ------------------------------------------------------------------
// Umbrella runner
// ------------------------------------------------------------------

func TestReclaim(t *testing.T) {
	t.Run("ResetsOldClaims", func(t *testing.T) { TestReclaimStale_ResetsOldClaims(t) })
	t.Run("NoStaleClaims", func(t *testing.T) { TestReclaimStale_NoStaleClaims(t) })
	t.Run("NoClaimedAt", func(t *testing.T) { TestReclaimStale_NoClaimedAt(t) })
	t.Run("DefaultThreshold", func(t *testing.T) { TestReclaimStale_DefaultThreshold(t) })
	t.Run("EmptyCollection", func(t *testing.T) { TestReclaimStale_EmptyCollection(t) })
	t.Run("MultipleStale", func(t *testing.T) { TestReclaimStale_MultipleStale(t) })
}
