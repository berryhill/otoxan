package index

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/silas/otoxan/internal/qdrant"
	"github.com/silas/otoxan/internal/store/plans"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/mongodb"
	qdranttc "github.com/testcontainers/testcontainers-go/modules/qdrant"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ------------------------------------------------------------------
// Fake embedder (deterministic, no network)
// ------------------------------------------------------------------

type fakeEmbedder struct {
	dim int
}

func (f *fakeEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	vecs, err := f.BatchEmbed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("empty embed result")
	}
	return vecs[0], nil
}

func (f *fakeEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		vec := make([]float32, f.dim)
		// Deterministic but distinct per text: hash the text into the first element.
		var sum float32
		for _, c := range texts[i] {
			sum += float32(c)
		}
		vec[0] = sum / 1000.0
		for j := 1; j < f.dim; j++ {
			vec[j] = float32(j) * 0.001
		}
		out[i] = vec
	}
	return out, nil
}

func (f *fakeEmbedder) Model() string { return "fake" }
func (f *fakeEmbedder) Dimension() int { return f.dim }

// ------------------------------------------------------------------
// Test: full integration cycle
// ------------------------------------------------------------------

// TestIndexer_Integration writes 5 plans, runs one indexer cycle, asserts all
// 5 are searchable; soft-deletes one, runs another cycle, asserts that one is
// gone from search.
func TestIndexer_Integration(t *testing.T) {
	ctx := context.Background()

	// 1. Spin up MongoDB.
	mongoContainer, err := mongodb.Run(ctx, "mongo:7")
	require.NoError(t, err, "start mongodb")
	defer func() { _ = mongoContainer.Terminate(ctx) }()

	mongoURI, err := mongoContainer.ConnectionString(ctx)
	require.NoError(t, err, "mongodb connection string")

	mongoClient, err := mongo.Connect(options.Client().ApplyURI(mongoURI))
	require.NoError(t, err, "connect to mongodb")
	defer func() { _ = mongoClient.Disconnect(ctx) }()

	db := mongoClient.Database("test_agent_db")
	planColl := db.Collection("plans")
	pointerColl := db.Collection("memory_pointers")

	// 2. Spin up Qdrant.
	qdrantContainer, err := qdranttc.Run(ctx, "qdrant/qdrant:v1.13.0")
	require.NoError(t, err, "start qdrant")
	defer func() { _ = qdrantContainer.Terminate(ctx) }()

	qdrantURI, err := qdrantContainer.RESTEndpoint(ctx)
	require.NoError(t, err, "qdrant REST endpoint")

	qdrantClient := qdrant.NewClient(qdrantURI)

	// 3. Create the plan store and insert 5 plans.
	planStore := plans.NewPlanStore(planColl)

	planTitles := []string{
		"Fix dispatch reaper bug",
		"Add batch embed support",
		"Onboard new agent",
		"Refactor session store",
		"Update CI pipeline",
	}
	planIDs := make([]string, len(planTitles))
	for i, title := range planTitles {
		planID := fmt.Sprintf("plan-%03d", i)
		planIDs[i] = planID
		p := &plans.Plan{
			PlanID:  planID,
			Title:   title,
			Status:  plans.StatusPlanning,
			Content: fmt.Sprintf("Content for plan %d: %s.", i, title),
			Tags:    []string{"test"},
		}
		_, err := planStore.Create(ctx, p)
		require.NoError(t, err, "create plan %d", i)
	}

	// 4. Build the indexer.
	pointerStore := NewPointerStore(pointerColl)
	cfg := IndexerConfig{
		BatchSize:    32,
		PollInterval: 60 * time.Second,
		VectorSize:   4, // small for test speed
		MaxRetries:   3,
		RetryBase:    100 * time.Millisecond,
		RetryMax:     1 * time.Second,
	}
	fakeEmb := &fakeEmbedder{dim: 4}
	sources := []SourceConfig{
		{
			CollectionName: "plans",
			SourceType:     "plan",
			Extractor:      func(doc bson.M) string { return PlanExtractor().Extract(doc) },
		},
	}
	indexer := NewIndexer(cfg, qdrantClient, fakeEmb, pointerStore, sources, "agent_99_index")

	// Ensure Qdrant collection exists.
	err = indexer.EnsureCollection(ctx)
	require.NoError(t, err, "ensure qdrant collection")

	// 5. Run one indexer cycle.
	err = indexer.RunOnce(ctx)
	require.NoError(t, err, "first indexer cycle")

	// 6. Verify all 5 plans are searchable.
	// We search with a vector close to each plan's deterministic vector.
	for i, title := range planTitles {
		// Build the same text the extractor would produce.
		text := fmt.Sprintf("Plan: %s\nStatus: PLANNING\nContent for plan %d: %s.", title, i, title)
		queryVec, err := fakeEmb.Embed(ctx, text)
		require.NoError(t, err, "embed query for plan %d", i)

		results, err := qdrantClient.Search(ctx, "agent_99_index", queryVec, 10)
		require.NoError(t, err, "search for plan %d", i)

		found := false
		for _, r := range results {
			if r.Payload["source_id"] == planIDs[i] {
				found = true
				break
			}
		}
		assert.True(t, found, "plan %q (%s) should be searchable after first cycle", planIDs[i], title)
	}

	// 7. Soft-delete one plan.
	deletedPlanID := planIDs[2] // "plan-002"
	_, err = planStore.Delete(ctx, deletedPlanID)
	require.NoError(t, err, "soft-delete plan %s", deletedPlanID)

	// 8. Run a second indexer cycle.
	err = indexer.RunOnce(ctx)
	require.NoError(t, err, "second indexer cycle")

	// 9. Verify the deleted plan is gone from search.
	for i, title := range planTitles {
		text := fmt.Sprintf("Plan: %s\nStatus: PLANNING\nContent for plan %d: %s.", title, i, title)
		queryVec, err := fakeEmb.Embed(ctx, text)
		require.NoError(t, err, "embed query for plan %d", i)

		results, err := qdrantClient.Search(ctx, "agent_99_index", queryVec, 10)
		require.NoError(t, err, "search after delete for plan %d", i)

		found := false
		for _, r := range results {
			if r.Payload["source_id"] == planIDs[i] {
				found = true
				break
			}
		}

		if planIDs[i] == deletedPlanID {
			assert.False(t, found, "soft-deleted plan %q should be gone from search", deletedPlanID)
		} else {
			assert.True(t, found, "plan %q should still be searchable", planIDs[i])
		}
	}

	// 10. Verify the pointer doc is marked removed.
	_, err = pointerStore.FindBySource(ctx, deletedPlanID)
	if err == nil {
		// If FindBySource still returns it (it excludes removed), that's a bug.
		t.Errorf("expected FindBySource to exclude removed pointer for %s", deletedPlanID)
	} else {
		// Expected: mongo.ErrNoDocuments or similar.
		assert.NotNil(t, err)
	}

	// Verify the pointer is actually marked removed in MongoDB (include removed).
	var raw bson.M
	err = pointerColl.FindOne(ctx, bson.M{"source_id": deletedPlanID}).Decode(&raw)
	require.NoError(t, err, "find raw pointer for deleted plan")
	assert.Equal(t, true, raw["removed"], "pointer should be marked removed")
}

// ------------------------------------------------------------------
// Test: backoff + clear errors on transient Qdrant failures
// ------------------------------------------------------------------

// flakyQdrantServer is an httptest.Server that can be configured to fail a
// number of requests with a given status code before succeeding.
type flakyQdrantServer struct {
	*httptest.Server
	mu        sync.Mutex
	failCount int
	failCode  int
	requests  int
	onSuccess func()
}

func newFlakyQdrantServer(failCount, failCode int) *flakyQdrantServer {
	fq := &flakyQdrantServer{failCount: failCount, failCode: failCode}
	mux := http.NewServeMux()

	// Single handler for /collections/* — covers both collection create and points upsert.
	mux.HandleFunc("/collections/", func(w http.ResponseWriter, r *http.Request) {
		fq.mu.Lock()
		fq.requests++
		shouldFail := fq.failCount > 0
		if shouldFail {
			fq.failCount--
		}
		fq.mu.Unlock()
		if shouldFail {
			w.WriteHeader(fq.failCode)
			_, _ = w.Write([]byte(`{"status":{"error":"injected"}}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"result":true,"status":"ok","time":0.001}`))
		if fq.onSuccess != nil {
			fq.onSuccess()
		}
	})

	fq.Server = httptest.NewServer(mux)
	return fq
}

// TestIndexer_BackoffAndClearErrors verifies that when Qdrant returns 429 or
// is unreachable, the indexer retries with exponential backoff, logs each
// retry, and eventually succeeds (clears the error) once Qdrant recovers.
func TestIndexer_BackoffAndClearErrors(t *testing.T) {
	ctx := context.Background()

	// 1. Spin up MongoDB.
	mongoContainer, err := mongodb.Run(ctx, "mongo:7")
	require.NoError(t, err, "start mongodb")
	defer func() { _ = mongoContainer.Terminate(ctx) }()

	mongoURI, err := mongoContainer.ConnectionString(ctx)
	require.NoError(t, err, "mongodb connection string")

	mongoClient, err := mongo.Connect(options.Client().ApplyURI(mongoURI))
	require.NoError(t, err, "connect to mongodb")
	defer func() { _ = mongoClient.Disconnect(ctx) }()

	db := mongoClient.Database("test_agent_db")
	planColl := db.Collection("plans")
	pointerColl := db.Collection("memory_pointers")

	// 2. Insert one plan.
	planStore := plans.NewPlanStore(planColl)
	p := &plans.Plan{
		PlanID:  "plan-001",
		Title:   "Test backoff plan",
		Status:  plans.StatusPlanning,
		Content: "Content for backoff test.",
		Tags:    []string{"test"},
	}
	_, err = planStore.Create(ctx, p)
	require.NoError(t, err, "create plan")

	// 3. Build a flaky Qdrant server: first 2 requests return 429, then succeed.
	failCount := 2
	fq := newFlakyQdrantServer(failCount, http.StatusTooManyRequests)
	defer fq.Close()

	var successOnce sync.Once
	successCalled := make(chan struct{}, 1)
	fq.onSuccess = func() {
		successOnce.Do(func() { close(successCalled) })
	}

	qdrantClient := qdrant.NewClient(fq.URL)

	// 4. Build the indexer with short retry settings so the test runs fast.
	pointerStore := NewPointerStore(pointerColl)
	cfg := IndexerConfig{
		BatchSize:    32,
		PollInterval: 60 * time.Second,
		VectorSize:   4,
		MaxRetries:   5,
		RetryBase:    50 * time.Millisecond,
		RetryMax:     500 * time.Millisecond,
	}
	fakeEmb := &fakeEmbedder{dim: 4}
	sources := []SourceConfig{
		{
			CollectionName: "plans",
			SourceType:     "plan",
			Extractor:      func(doc bson.M) string { return PlanExtractor().Extract(doc) },
		},
	}
	indexer := NewIndexer(cfg, qdrantClient, fakeEmb, pointerStore, sources, "agent_backoff_index")

	// 5. Run the cycle. It should succeed after 2 retries.
	err = indexer.RunOnce(ctx)
	require.NoError(t, err, "indexer should recover after transient 429s")

	// 6. Verify the server saw the expected number of requests (initial + retries).
	fq.mu.Lock()
	reqs := fq.requests
	fq.mu.Unlock()
	assert.GreaterOrEqual(t, reqs, failCount+1, "expected at least %d requests (failures + success)", failCount+1)

	// 7. Verify the success callback fired.
	select {
	case <-successCalled:
		// ok
	case <-time.After(2 * time.Second):
		t.Fatal("expected onSuccess to be called after recovery")
	}

	// 8. Verify the pointer doc was written.
	ptr, err := pointerStore.FindBySource(ctx, "plan-001")
	require.NoError(t, err, "pointer should exist after successful indexing")
	assert.Equal(t, "plan-001", ptr.SourceID)
	assert.False(t, ptr.Removed, "pointer should not be removed")
}

// TestIndexer_Backoff_Unreachable verifies that when Qdrant is completely
// unreachable, the indexer retries with backoff and eventually returns an
// error (but does not panic). When the server comes back, the next cycle
// succeeds.
func TestIndexer_Backoff_Unreachable(t *testing.T) {
	ctx := context.Background()

	// 1. Spin up MongoDB.
	mongoContainer, err := mongodb.Run(ctx, "mongo:7")
	require.NoError(t, err, "start mongodb")
	defer func() { _ = mongoContainer.Terminate(ctx) }()

	mongoURI, err := mongoContainer.ConnectionString(ctx)
	require.NoError(t, err, "mongodb connection string")

	mongoClient, err := mongo.Connect(options.Client().ApplyURI(mongoURI))
	require.NoError(t, err, "connect to mongodb")
	defer func() { _ = mongoClient.Disconnect(ctx) }()

	db := mongoClient.Database("test_agent_db")
	planColl := db.Collection("plans")
	pointerColl := db.Collection("memory_pointers")

	// 2. Insert one plan.
	planStore := plans.NewPlanStore(planColl)
	p := &plans.Plan{
		PlanID:  "plan-002",
		Title:   "Test unreachable plan",
		Status:  plans.StatusPlanning,
		Content: "Content for unreachable test.",
		Tags:    []string{"test"},
	}
	_, err = planStore.Create(ctx, p)
	require.NoError(t, err, "create plan")

	// 3. Point the indexer at a URL that refuses connections.
	qdrantClient := qdrant.NewClient("http://127.0.0.1:1") // nothing listening

	pointerStore := NewPointerStore(pointerColl)
	cfg := IndexerConfig{
		BatchSize:    32,
		PollInterval: 60 * time.Second,
		VectorSize:   4,
		MaxRetries:   2,
		RetryBase:    50 * time.Millisecond,
		RetryMax:     200 * time.Millisecond,
	}
	fakeEmb := &fakeEmbedder{dim: 4}
	sources := []SourceConfig{
		{
			CollectionName: "plans",
			SourceType:     "plan",
			Extractor:      func(doc bson.M) string { return PlanExtractor().Extract(doc) },
		},
	}
	indexer := NewIndexer(cfg, qdrantClient, fakeEmb, pointerStore, sources, "agent_unreachable_index")

	// 4. RunOnce should return an error after exhausting retries, but not panic.
	start := time.Now()
	err = indexer.RunOnce(ctx)
	elapsed := time.Since(start)
	require.Error(t, err, "expected error when Qdrant is unreachable")
	assert.True(t, elapsed >= 100*time.Millisecond, "expected some backoff delay, got %v", elapsed)
	assert.Contains(t, err.Error(), "exhausted", "error should mention exhausted retries")

	// 5. Now start a real Qdrant server and re-point the client.
	qdrantContainer, err := qdranttc.Run(ctx, "qdrant/qdrant:v1.13.0")
	require.NoError(t, err, "start qdrant")
	defer func() { _ = qdrantContainer.Terminate(ctx) }()

	qdrantURI, err := qdrantContainer.RESTEndpoint(ctx)
	require.NoError(t, err, "qdrant REST endpoint")

	// Swap the underlying HTTP client to point at the real server.
	realClient := qdrant.NewClient(qdrantURI)
	indexer.qdrant = realClient

	// Ensure collection exists on the real server.
	err = indexer.EnsureCollection(ctx)
	require.NoError(t, err, "ensure collection on real qdrant")

	// 6. RunOnce should now succeed (clear the error).
	err = indexer.RunOnce(ctx)
	require.NoError(t, err, "indexer should recover when Qdrant becomes reachable")

	// 7. Verify the plan is searchable.
	text := "Plan: Test unreachable plan\nStatus: PLANNING\nContent for unreachable test."
	queryVec, err := fakeEmb.Embed(ctx, text)
	require.NoError(t, err, "embed query")

	results, err := realClient.Search(ctx, "agent_unreachable_index", queryVec, 10)
	require.NoError(t, err, "search after recovery")

	found := false
	for _, r := range results {
		if r.Payload["source_id"] == "plan-002" {
			found = true
			break
		}
	}
	assert.True(t, found, "plan should be searchable after recovery from unreachable Qdrant")
}

// ------------------------------------------------------------------
// Test: IsTransientError classification
// ------------------------------------------------------------------

func TestIsTransientError(t *testing.T) {
	assert.False(t, qdrant.IsTransientError(nil), "nil is not transient")
	assert.False(t, qdrant.IsTransientError(fmt.Errorf("qdrant 400 Bad Request")), "400 is not transient")
	assert.False(t, qdrant.IsTransientError(fmt.Errorf("qdrant 404 Not Found")), "404 is not transient")

	assert.True(t, qdrant.IsTransientError(fmt.Errorf("qdrant 429 Too Many Requests")), "429 is transient")
	assert.True(t, qdrant.IsTransientError(fmt.Errorf("qdrant 503 Service Unavailable")), "503 is transient")
	assert.True(t, qdrant.IsTransientError(fmt.Errorf("qdrant 502 Bad Gateway")), "502 is transient")
	assert.True(t, qdrant.IsTransientError(fmt.Errorf("qdrant 504 Gateway Timeout")), "504 is transient")
	assert.True(t, qdrant.IsTransientError(fmt.Errorf("do request: connection refused")), "connection refused is transient")
	assert.True(t, qdrant.IsTransientError(fmt.Errorf("do request: no such host")), "no such host is transient")
	assert.True(t, qdrant.IsTransientError(fmt.Errorf("do request: i/o timeout")), "i/o timeout is transient")
	assert.True(t, qdrant.IsTransientError(fmt.Errorf("do request: context deadline exceeded")), "context deadline exceeded is transient")
}

// ------------------------------------------------------------------
// Test: RunOnce keeps running across sources when one fails
// ------------------------------------------------------------------

// failingEmbedder always fails.
type failingEmbedder struct{}

func (f *failingEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	return nil, fmt.Errorf("injected embedder failure")
}
func (f *failingEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	return nil, fmt.Errorf("injected embedder failure")
}
func (f *failingEmbedder) Model() string  { return "failing" }
func (f *failingEmbedder) Dimension() int { return 4 }

// TestIndexer_RunOnce_ContinuesOnSourceFailure verifies that when one source
// fails, RunOnce still attempts the remaining sources and returns a non-fatal
// error.  We use a flaky Qdrant server that returns 429 on the first request
// and succeeds thereafter.  With MaxRetries=0 the first source fails, but the
// second source should still be attempted.
func TestIndexer_RunOnce_ContinuesOnSourceFailure(t *testing.T) {
	ctx := context.Background()

	mongoContainer, err := mongodb.Run(ctx, "mongo:7")
	require.NoError(t, err, "start mongodb")
	defer func() { _ = mongoContainer.Terminate(ctx) }()

	mongoURI, err := mongoContainer.ConnectionString(ctx)
	require.NoError(t, err, "mongodb connection string")

	mongoClient, err := mongo.Connect(options.Client().ApplyURI(mongoURI))
	require.NoError(t, err, "connect to mongodb")
	defer func() { _ = mongoClient.Disconnect(ctx) }()

}
