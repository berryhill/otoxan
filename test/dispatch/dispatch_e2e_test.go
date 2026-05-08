package dispatch_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/silas/otoxan/internal/dispatch"
	"github.com/testcontainers/testcontainers-go/modules/mongodb"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ------------------------------------------------------------------
// Helpers
// ------------------------------------------------------------------

func projectRoot() string {
	_, f, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(f), "..", "..")
}

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

func buildWorker(t *testing.T) string {
	t.Helper()
	root := projectRoot()
	binDir := filepath.Join(root, "testbin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	out := filepath.Join(binDir, "otoxan-worker")
	if _, err := os.Stat(out); err == nil {
		return out
	}
	t.Logf("building otoxan-worker ...")
	cmd := exec.Command("go", "build", "-o", out, "./cmd/otoxan-worker")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build otoxan-worker: %v\n%s", err, out)
	}
	return out
}

func newRequestsColl(t *testing.T, client *mongo.Client) *mongo.Collection {
	t.Helper()
	return client.Database("silas").Collection(fmt.Sprintf("dispatch_requests_%d", time.Now().UnixNano()))
}

func newSpawnsColl(t *testing.T, client *mongo.Client) *mongo.Collection {
	t.Helper()
	return client.Database("silas").Collection(fmt.Sprintf("dispatch_spawns_%d", time.Now().UnixNano()))
}

func seedPendingRequests(ctx context.Context, coll *mongo.Collection, n int) error {
	now := time.Now().UTC()
	for i := 0; i < n; i++ {
		doc := dispatch.DispatchRequest{
			RequestID: fmt.Sprintf("dr_%03d", i),
			TaskID:    fmt.Sprintf("t_%03d", i),
			Status:    dispatch.RequestPending,
			CreatedAt: now,
			Priority:  i,
		}
		doc.Extra = map[string]any{"queued_at": now.Add(time.Duration(i) * time.Millisecond)}
		if _, err := coll.InsertOne(ctx, doc); err != nil {
			return err
		}
	}
	return nil
}

func countByStatus(ctx context.Context, coll *mongo.Collection, status dispatch.RequestStatus) int64 {
	c, _ := coll.CountDocuments(ctx, bson.M{"status": status})
	return c
}

func countSpawnsByStatus(ctx context.Context, coll *mongo.Collection, status dispatch.SpawnStatus) int64 {
	c, _ := coll.CountDocuments(ctx, bson.M{"status": status})
	return c
}

// isPIDAlive checks whether a process with the given PID is still alive.
func isPIDAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	return err == nil
}

// countLivePIDs scans /proc and counts entries that look like PIDs.
func countLivePIDs() int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return -1
	}
	count := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := strconv.Atoi(e.Name()); err == nil {
			count++
		}
	}
	return count
}

// ------------------------------------------------------------------
// End-to-end soak test
// ------------------------------------------------------------------

func TestDispatchE2E_100Tasks(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e soak in short mode")
	}

	ctx := context.Background()
	client := setupMongo(t)
	reqColl := newRequestsColl(t, client)
	spawnColl := newSpawnsColl(t, client)

	// 1. Insert 100 PENDING dispatch_requests.
	if err := seedPendingRequests(ctx, reqColl, 100); err != nil {
		t.Fatalf("seedPendingRequests failed: %v", err)
	}

	// Build the real worker binary so spawnOne can fork it.
	workerBin := buildWorker(t)

	// Ensure the marker directory is clean.
	markerDir := "/tmp/otoxan_completed"
	_ = os.RemoveAll(markerDir)
	_ = os.MkdirAll(markerDir, 0755)
	defer os.RemoveAll(markerDir)

	// Concurrency limit tuned to 6 for CI stability.
	const concurrencyLimit = 6

	// Channels wiring the goroutines.
	claimed := make(chan *dispatch.DispatchRequest, concurrencyLimit)
	completions := make(chan *dispatch.Completion, concurrencyLimit)

	// 2. Start claimLoop.
	claimDeps := dispatch.ClaimDeps{
		Collection:       reqColl,
		AgentID:          "e2e-agent",
		ConcurrencyLimit: concurrencyLimit,
		ActiveCount: func() int {
			running, _ := spawnColl.CountDocuments(ctx, bson.M{"status": dispatch.SpawnRunning})
			claimedCount, _ := reqColl.CountDocuments(ctx, bson.M{"status": dispatch.RequestClaimed})
			return int(running + claimedCount)
		},
	}

	claimCtx, claimCancel := context.WithCancel(ctx)
	defer claimCancel()
	go func() {
		_ = dispatch.RunClaimLoop(claimCtx, claimDeps, claimed)
	}()

	// 3. Start spawnSupervisor.
	spawnDeps := dispatch.SpawnDeps{
		SpawnsColl:   spawnColl,
		RequestsColl: reqColl,
		AgentID:      "e2e-agent",
		WorkerBinary: workerBin,
	}

	spawnCtx, spawnCancel := context.WithCancel(ctx)
	defer spawnCancel()
	go func() {
		_ = dispatch.RunSpawnSupervisor(spawnCtx, spawnDeps, claimed)
	}()

	// 4. Start reapLoop.
	reapDeps := dispatch.ReapDeps{
		SpawnsColl: spawnColl,
	}
	reapCtx, reapCancel := context.WithCancel(ctx)
	defer reapCancel()
	go func() {
		_ = dispatch.RunReapLoop(reapCtx, reapDeps, completions)
	}()

	// 5. Start completionWatcher.
	completeDeps := dispatch.CompletionDeps{
		SpawnsColl:   spawnColl,
		RequestsColl: reqColl,
	}
	completeCtx, completeCancel := context.WithCancel(ctx)
	defer completeCancel()
	go func() {
		_ = dispatch.RunCompletionWatcher(completeCtx, completeDeps, completions)
	}()

	// 6. Wait up to 90s for queue drain.
	timeout := 90 * time.Second
	deadline := time.Now().Add(timeout)

	var baselinePIDs int
	if runtime.GOOS == "linux" {
		baselinePIDs = countLivePIDs()
		t.Logf("baseline /proc PID count: %d", baselinePIDs)
	}

	stuckCounter := 0
	lastFulfilled := int64(0)
	for time.Now().Before(deadline) {
		pending := countByStatus(ctx, reqColl, dispatch.RequestPending)
		claimedCount := countByStatus(ctx, reqColl, dispatch.RequestClaimed)
		running := countSpawnsByStatus(ctx, spawnColl, dispatch.SpawnRunning)
		fulfilled := countByStatus(ctx, reqColl, dispatch.RequestFulfilled)
		failed := countByStatus(ctx, reqColl, dispatch.RequestFailed)

		t.Logf("pending=%d claimed=%d running=%d fulfilled=%d failed=%d",
			pending, claimedCount, running, fulfilled, failed)

		if pending == 0 && claimedCount == 0 && running == 0 && fulfilled == 100 && failed == 0 {
			break
		}

		// Detect stall: if fulfilled count hasn't changed for 15s, the
		// pipeline is wedged (likely markers not being reaped).  Fail
		// fast instead of burning the full timeout.
		if fulfilled == lastFulfilled {
			stuckCounter++
			if stuckCounter >= 15 {
				t.Fatalf("pipeline stalled: fulfilled stuck at %d for 15s", fulfilled)
			}
		} else {
			stuckCounter = 0
			lastFulfilled = fulfilled
		}

		time.Sleep(1 * time.Second)
	}

	// Stop all loops.
	claimCancel()
	spawnCancel()
	reapCancel()
	completeCancel()

	// Allow in-flight work to settle.
	time.Sleep(2 * time.Second)

	// 7. Final assertions.
	pending := countByStatus(ctx, reqColl, dispatch.RequestPending)
	claimedCount := countByStatus(ctx, reqColl, dispatch.RequestClaimed)
	running := countSpawnsByStatus(ctx, spawnColl, dispatch.SpawnRunning)
	fulfilled := countByStatus(ctx, reqColl, dispatch.RequestFulfilled)
	failed := countByStatus(ctx, reqColl, dispatch.RequestFailed)
	stale := countByStatus(ctx, reqColl, dispatch.RequestClaimed) // any still CLAIMED after timeout

	if pending != 0 {
		t.Errorf("expected 0 PENDING, got %d", pending)
	}
	if claimedCount != 0 {
		t.Errorf("expected 0 CLAIMED, got %d", claimedCount)
	}
	if running != 0 {
		t.Errorf("expected 0 RUNNING spawns, got %d", running)
	}
	if fulfilled != 100 {
		t.Errorf("expected 100 FULFILLED, got %d", fulfilled)
	}
	if failed != 0 {
		t.Errorf("expected 0 FAILED, got %d", failed)
	}
	if stale != 0 {
		t.Errorf("expected 0 stale CLAIMED, got %d", stale)
	}

	// 8. PID leak check: verify no worker processes are still alive.
	cursor, err := spawnColl.Find(ctx, bson.M{})
	if err != nil {
		t.Fatalf("find spawns: %v", err)
	}
	defer cursor.Close(ctx)

	var leaks int
	for cursor.Next(ctx) {
		var rec dispatch.SpawnRecord
		if err := cursor.Decode(&rec); err != nil {
			continue
		}
		if isPIDAlive(rec.PID) {
			leaks++
			t.Errorf("PID leak detected: task=%s pid=%d still alive", rec.TaskID, rec.PID)
			_ = syscall.Kill(rec.PID, syscall.SIGKILL)
		}
	}

	if runtime.GOOS == "linux" && baselinePIDs > 0 {
		finalPIDs := countLivePIDs()
		if finalPIDs > baselinePIDs+10 {
			t.Logf("WARNING: /proc PID count grew from %d to %d", baselinePIDs, finalPIDs)
		}
	}

	if leaks > 0 {
		t.Fatalf("detected %d PID leaks", leaks)
	}

	t.Logf("soak complete: 100 tasks, 0 failed, 0 stale, 0 leaks")
}
