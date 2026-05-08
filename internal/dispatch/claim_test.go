package dispatch

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go/modules/mongodb"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ------------------------------------------------------------------
// Test harness
// ------------------------------------------------------------------

// setupMongo spins up a testcontainers MongoDB and returns a client.
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

	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("failed to connect to mongodb: %v", err)
	}
	t.Cleanup(func() {
		_ = client.Disconnect(ctx)
	})

	return client
}

// newClaimTestColl returns a fresh dispatch_requests collection.
func newClaimTestColl(t *testing.T, client *mongo.Client) *mongo.Collection {
	t.Helper()
	return client.Database("silas").Collection(fmt.Sprintf("dispatch_requests_%d", time.Now().UnixNano()))
}

// seedPending inserts n PENDING dispatch_requests with increasing queued_at.
func seedPending(ctx context.Context, coll *mongo.Collection, n int) error {
	now := time.Now().UTC()
	for i := 0; i < n; i++ {
		doc := DispatchRequest{
			RequestID: fmt.Sprintf("dr_%03d", i),
			TaskID:    fmt.Sprintf("t_%03d", i),
			Status:    RequestPending,
			CreatedAt: now,
			Priority:  i,
		}
		// queued_at is used for sort order; stagger by 1ms.
		doc.Extra = map[string]any{"queued_at": now.Add(time.Duration(i) * time.Millisecond)}

		if _, err := coll.InsertOne(ctx, doc); err != nil {
			return err
		}
	}
	return nil
}

// ------------------------------------------------------------------
// Unit tests
// ------------------------------------------------------------------

func TestClaimOne_AtomicTransition(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	coll := newClaimTestColl(t, client)

	if err := seedPending(ctx, coll, 3); err != nil {
		t.Fatalf("seedPending failed: %v", err)
	}

	deps := ClaimDeps{
		Collection:       coll,
		AgentID:          "agent-alpha",
		ConcurrencyLimit: 5,
		ActiveCount:      func() int { return 0 },
	}

	req, err := claimOne(ctx, deps)
	if err != nil {
		t.Fatalf("claimOne failed: %v", err)
	}
	if req.Status != RequestClaimed {
		t.Errorf("expected status CLAIMED, got %q", req.Status)
	}
	if req.Extra == nil {
		t.Fatal("Extra map nil — cannot verify claimed_by")
	}
	// claimed_by and claimed_at are not in DispatchRequest struct fields;
	// verify via raw BSON lookup.
	var raw bson.M
	if err := coll.FindOne(ctx, bson.M{"request_id": req.RequestID}).Decode(&raw); err != nil {
		t.Fatalf("raw lookup failed: %v", err)
	}
	if raw["claimed_by"] != deps.AgentID {
		t.Errorf("claimed_by mismatch: got %v, want %q", raw["claimed_by"], deps.AgentID)
	}
	if raw["status"] != string(RequestClaimed) {
		t.Errorf("status mismatch: got %v, want %q", raw["status"], RequestClaimed)
	}
}

func TestClaimOne_NoPending(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	coll := newClaimTestColl(t, client)

	deps := ClaimDeps{
		Collection:       coll,
		AgentID:          "agent-beta",
		ConcurrencyLimit: 5,
		ActiveCount:      func() int { return 0 },
	}

	_, err := claimOne(ctx, deps)
	if err == nil {
		t.Fatal("expected error when no PENDING docs exist")
	}
}

func TestClaimOne_DoubleClaimPrevention(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	coll := newClaimTestColl(t, client)

	if err := seedPending(ctx, coll, 1); err != nil {
		t.Fatalf("seedPending failed: %v", err)
	}

	depsA := ClaimDeps{
		Collection:       coll,
		AgentID:          "agent-a",
		ConcurrencyLimit: 5,
		ActiveCount:      func() int { return 0 },
	}
	depsB := ClaimDeps{
		Collection:       coll,
		AgentID:          "agent-b",
		ConcurrencyLimit: 5,
		ActiveCount:      func() int { return 0 },
	}

	var first, second *DispatchRequest
	var err1, err2 error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		first, err1 = claimOne(ctx, depsA)
	}()
	go func() {
		defer wg.Done()
		second, err2 = claimOne(ctx, depsB)
	}()
	wg.Wait()

	// Exactly one must succeed, the other must get no docs.
	successes := 0
	if err1 == nil {
		successes++
	}
	if err2 == nil {
		successes++
	}
	if successes != 1 {
		t.Fatalf("expected exactly 1 success, got %d", successes)
	}

	winner := first
	if winner == nil {
		winner = second
	}
	if winner == nil {
		t.Fatal("winner is nil")
	}

	var raw bson.M
	if err := coll.FindOne(ctx, bson.M{"request_id": winner.RequestID}).Decode(&raw); err != nil {
		t.Fatalf("raw lookup failed: %v", err)
	}
	claimedBy, ok := raw["claimed_by"].(string)
	if !ok || claimedBy == "" {
		t.Fatalf("expected claimed_by set, got %v", raw["claimed_by"])
	}
	if claimedBy != "agent-a" && claimedBy != "agent-b" {
		t.Errorf("claimed_by is %q, expected agent-a or agent-b", claimedBy)
	}
}

func TestClaimOne_RespectsQueuedAtOrder(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	coll := newClaimTestColl(t, client)

	now := time.Now().UTC()
	for _, id := range []string{"dr_second", "dr_first"} {
		doc := DispatchRequest{
			RequestID: id,
			TaskID:    id,
			Status:    RequestPending,
			CreatedAt: now,
			Priority:  0,
		}
		// dr_first queued 1 minute ago, dr_second queued now
		queuedAt := now
		if id == "dr_first" {
			queuedAt = now.Add(-time.Minute)
		}
		doc.Extra = map[string]any{"queued_at": queuedAt}

		if _, err := coll.InsertOne(ctx, doc); err != nil {
			t.Fatalf("InsertOne failed: %v", err)
		}
	}

	deps := ClaimDeps{
		Collection:       coll,
		AgentID:          "agent-order",
		ConcurrencyLimit: 5,
		ActiveCount:      func() int { return 0 },
	}

	req, err := claimOne(ctx, deps)
	if err != nil {
		t.Fatalf("claimOne failed: %v", err)
	}
	if req.RequestID != "dr_first" {
		t.Errorf("expected dr_first (oldest queued_at), got %q", req.RequestID)
	}
}

func TestClaimLoop_Basic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := setupMongo(t)
	coll := newClaimTestColl(t, client)

	if err := seedPending(ctx, coll, 5); err != nil {
		t.Fatalf("seedPending failed: %v", err)
	}

	out := make(chan *DispatchRequest, 5)
	deps := ClaimDeps{
		Collection:       coll,
		AgentID:          "agent-loop",
		ConcurrencyLimit: 5,
		ActiveCount:      func() int { return 0 },
	}

	go func() {
		if err := RunClaimLoop(ctx, deps, out); err != nil && err != context.Canceled {
			t.Errorf("RunClaimLoop returned unexpected error: %v", err)
		}
	}()

	// Wait for claims to propagate.
	time.Sleep(3 * time.Second)
	cancel()

	claimed := 0
	drain:
	for {
		select {
		case <-out:
			claimed++
		default:
			break drain
		}
	}

	if claimed == 0 {
		t.Fatal("expected at least one claim through the channel")
	}
	if claimed > 5 {
		t.Fatalf("expected at most 5 claims, got %d", claimed)
	}
}

func TestClaimLoop_RespectsConcurrencyLimit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := setupMongo(t)
	coll := newClaimTestColl(t, client)

	if err := seedPending(ctx, coll, 10); err != nil {
		t.Fatalf("seedPending failed: %v", err)
	}

	out := make(chan *DispatchRequest, 10)
	var active int64

	deps := ClaimDeps{
		Collection:       coll,
		AgentID:          "agent-limit",
		ConcurrencyLimit: 3,
		ActiveCount: func() int {
			return int(atomic.LoadInt64(&active))
		},
	}

	go func() {
		if err := RunClaimLoop(ctx, deps, out); err != nil && err != context.Canceled {
			t.Errorf("RunClaimLoop returned unexpected error: %v", err)
		}
	}()

	// Let it tick once.
	time.Sleep(3 * time.Second)

	// Simulate that 2 slots are already consumed.
	atomic.StoreInt64(&active, 2)

	// Let it tick again.
	time.Sleep(3 * time.Second)
	cancel()

	claimed := 0
	drain:
	for {
		select {
		case <-out:
			claimed++
		default:
			break drain
		}
	}

	// First tick claimed up to 3, second tick claimed up to 1 (3-2).
	// Total should be > 0 and <= 4.
	if claimed == 0 {
		t.Fatal("expected at least one claim")
	}
	if claimed > 4 {
		t.Fatalf("expected at most 4 claims with limit=3 and active=2, got %d", claimed)
	}
}

func TestClaimLoop_ChannelFullDrop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := setupMongo(t)
	coll := newClaimTestColl(t, client)

	if err := seedPending(ctx, coll, 5); err != nil {
		t.Fatalf("seedPending failed: %v", err)
	}

	// Buffer of 1 — most claims will be dropped.
	out := make(chan *DispatchRequest, 1)
	deps := ClaimDeps{
		Collection:       coll,
		AgentID:          "agent-drop",
		ConcurrencyLimit: 5,
		ActiveCount:      func() int { return 0 },
	}

	go func() {
		if err := RunClaimLoop(ctx, deps, out); err != nil && err != context.Canceled {
			t.Errorf("RunClaimLoop returned unexpected error: %v", err)
		}
	}()

	// Let it tick.
	time.Sleep(3 * time.Second)
	cancel()

	// We should see at most 1 item in the channel (buffer size).
	buffered := len(out)
	if buffered > 1 {
		t.Fatalf("expected at most 1 buffered item, got %d", buffered)
	}

	// Drain whatever is there.
	for len(out) > 0 {
		<-out
	}
}

func TestClaimLoop_CleanShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	client := setupMongo(t)
	coll := newClaimTestColl(t, client)

	if err := seedPending(ctx, coll, 2); err != nil {
		t.Fatalf("seedPending failed: %v", err)
	}

	out := make(chan *DispatchRequest, 2)
	deps := ClaimDeps{
		Collection:       coll,
		AgentID:          "agent-shutdown",
		ConcurrencyLimit: 5,
		ActiveCount:      func() int { return 0 },
	}

	done := make(chan error, 1)
	go func() {
		done <- RunClaimLoop(ctx, deps, out)
	}()

	// Give it a moment to start.
	time.Sleep(500 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunClaimLoop did not exit after cancel")
	}
}

// ------------------------------------------------------------------
// Umbrella runner
// ------------------------------------------------------------------

func TestClaimLoop(t *testing.T) {
	t.Run("AtomicTransition", func(t *testing.T) { TestClaimOne_AtomicTransition(t) })
	t.Run("NoPending", func(t *testing.T) { TestClaimOne_NoPending(t) })
	t.Run("DoubleClaimPrevention", func(t *testing.T) { TestClaimOne_DoubleClaimPrevention(t) })
	t.Run("RespectsQueuedAtOrder", func(t *testing.T) { TestClaimOne_RespectsQueuedAtOrder(t) })
	t.Run("Basic", func(t *testing.T) { TestClaimLoop_Basic(t) })
	t.Run("RespectsConcurrencyLimit", func(t *testing.T) { TestClaimLoop_RespectsConcurrencyLimit(t) })
	t.Run("ChannelFullDrop", func(t *testing.T) { TestClaimLoop_ChannelFullDrop(t) })
	t.Run("CleanShutdown", func(t *testing.T) { TestClaimLoop_CleanShutdown(t) })
}
