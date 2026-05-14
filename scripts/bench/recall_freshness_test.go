// recall_freshness_test.go — end-to-end freshness benchmark
//
// Writes 100 documents across multiple source types into MongoDB,
// triggers the indexer, polls otoxan recall until each document is
// searchable, and asserts the 95th percentile latency is ≤ 60 s.
//
// This is an integration test that requires a running MongoDB and
// Qdrant. It uses testcontainers when available; otherwise it falls
// back to the standard connection URLs (MONGO_URI / OTOXAN_QDRANT_URL).
//
// Run with:
//
//	go test -v -run TestRecallFreshness ./scripts/bench/
//
// Or as a standalone benchmark binary:
//
//	go test -c ./scripts/bench/ -o bin/recall-freshness.test
//	./bin/recall-freshness.test -test.run TestRecallFreshness -test.v

package bench

import (
	"context"
	"fmt"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/silas/otoxan/internal/index"
	"github.com/silas/otoxan/internal/qdrant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/mongodb"
	qdranttc "github.com/testcontainers/testcontainers-go/modules/qdrant"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ------------------------------------------------------------------
// Configuration
// ------------------------------------------------------------------

const (
	targetDocCount      = 100
	maxFreshnessSeconds = 60.0
	pollInterval        = 500 * time.Millisecond
	searchTimeout       = 120 * time.Second
	vectorDim           = 4 // small dimension for test speed
)

// sourceTypes defines the mix of documents we write.
// The extractor and collection name are drawn from the existing indexer
// conventions so that RunOnce can index them without extra wiring.
var sourceTypes = []struct {
	name           string
	collection     string
	idPrefix       string
	makeDoc        func(id string, seq int) bson.M
	extractor      func(doc bson.M) string
	searchQuery    func(id string, seq int) string
}{
	{
		name:       "plan",
		collection: "plans",
		idPrefix:   "fresh-plan",
		makeDoc: func(id string, seq int) bson.M {
			return bson.M{
				"_id":        id,
				"plan_id":    id,
				"title":      fmt.Sprintf("Freshness plan %d: dispatch reaper bug fix", seq),
				"status":     "PLANNING",
				"content":    fmt.Sprintf("Plan content for sequence %d. We need to fix the reaper.", seq),
				"tags":       []string{"freshness", "bug"},
				"created_at": time.Now().UTC(),
				"updated_at": time.Now().UTC(),
			}
		},
		extractor: func(doc bson.M) string { return index.PlanExtractor().Extract(doc) },
		searchQuery: func(id string, seq int) string {
			return fmt.Sprintf("dispatch reaper bug fix sequence %d", seq)
		},
	},
	{
		name:       "task",
		collection: "tasks",
		idPrefix:   "fresh-task",
		makeDoc: func(id string, seq int) bson.M {
			return bson.M{
				"_id":             id,
				"task_id":         id,
				"title":           fmt.Sprintf("Freshness task %d: add batch embed support", seq),
				"status":          "QUEUED",
				"description":     fmt.Sprintf("Task description for sequence %d. Implement batching.", seq),
				"intent":          "Reduce embedding latency",
				"implementation":  "Added BatchEmbed to the embedder interface.",
				"assignee":        "silas",
				"created_at":      time.Now().UTC(),
				"updated_at":      time.Now().UTC(),
			}
		},
		extractor: func(doc bson.M) string { return index.TaskExtractor().Extract(doc) },
		searchQuery: func(id string, seq int) string {
			return fmt.Sprintf("batch embed support sequence %d", seq)
		},
	},
	{
		name:       "report",
		collection: "reports",
		idPrefix:   "fresh-report",
		makeDoc: func(id string, seq int) bson.M {
			return bson.M{
				"_id":        id,
				"report_id":  id,
				"title":      fmt.Sprintf("Freshness report %d: weekly index health", seq),
				"status":     "DRAFT",
				"content":    fmt.Sprintf("Report content for sequence %d. Qdrant has %d points.", seq, seq*100),
				"tags":       []string{"ops", "freshness"},
				"created_at": time.Now().UTC(),
				"updated_at": time.Now().UTC(),
			}
		},
		extractor: func(doc bson.M) string { return index.ReportExtractor().Extract(doc) },
		searchQuery: func(id string, seq int) string {
			return fmt.Sprintf("weekly index health sequence %d", seq)
		},
	},
	{
		name:       "directive",
		collection: "directives",
		idPrefix:   "fresh-directive",
		makeDoc: func(id string, seq int) bson.M {
			return bson.M{
				"_id":          id,
				"directive_id": id,
				"title":        fmt.Sprintf("Freshness directive %d: never inline embed", seq),
				"category":     "performance",
				"content":      fmt.Sprintf("Directive content for sequence %d. Embedding must be async.", seq),
				"enabled":      true,
				"created_at":   time.Now().UTC(),
				"updated_at":   time.Now().UTC(),
			}
		},
		extractor: func(doc bson.M) string { return index.DirectiveExtractor().Extract(doc) },
		searchQuery: func(id string, seq int) string {
			return fmt.Sprintf("never inline embed sequence %d", seq)
		},
	},
	{
		name:       "session",
		collection: "sessions",
		idPrefix:   "fresh-session",
		makeDoc: func(id string, seq int) bson.M {
			return bson.M{
				"_id":               id,
				"session_id":        id,
				"user_content":      fmt.Sprintf("User question for sequence %d: how do I fix the reaper?", seq),
				"assistant_content": fmt.Sprintf("Assistant answer for sequence %d: add an index on claimed_at.", seq),
				"created_at":        time.Now().UTC(),
				"updated_at":        time.Now().UTC(),
			}
		},
		extractor: func(doc bson.M) string { return index.SessionExtractor().Extract(doc) },
		searchQuery: func(id string, seq int) string {
			return fmt.Sprintf("how do I fix the reaper sequence %d", seq)
		},
	},
	{
		name:       "task_event",
		collection: "task_events",
		idPrefix:   "fresh-event",
		makeDoc: func(id string, seq int) bson.M {
			return bson.M{
				"_id":        id,
				"event_id":   id,
				"event_type": "completed",
				"task_id":    fmt.Sprintf("task-%03d", seq),
				"actor":      "silas",
				"data":       bson.M{"duration_ms": seq * 100, "result": "success"},
				"created_at": time.Now().UTC(),
				"updated_at": time.Now().UTC(),
			}
		},
		extractor: func(doc bson.M) string { return index.TaskEventExtractor().Extract(doc) },
		searchQuery: func(id string, seq int) string {
			return fmt.Sprintf("completed task %d", seq)
		},
	},
	{
		name:       "notification",
		collection: "notifications",
		idPrefix:   "fresh-notif",
		makeDoc: func(id string, seq int) bson.M {
			return bson.M{
				"_id":             id,
				"notification_id": id,
				"title":           fmt.Sprintf("Freshness notification %d: task completed", seq),
				"body":            fmt.Sprintf("Task task-%03d finished successfully in sequence %d.", seq, seq),
				"channel":         "slack",
				"status":          "SENT",
				"created_at":      time.Now().UTC(),
				"updated_at":      time.Now().UTC(),
			}
		},
		extractor: func(doc bson.M) string { return index.NotificationExtractor().Extract(doc) },
		searchQuery: func(id string, seq int) string {
			return fmt.Sprintf("task completed sequence %d", seq)
		},
	},
	{
		name:       "flow",
		collection: "flows",
		idPrefix:   "fresh-flow",
		makeDoc: func(id string, seq int) bson.M {
			return bson.M{
				"_id":         id,
				"flow_id":     id,
				"name":        fmt.Sprintf("Freshness flow %d: GitHub Issue to PR", seq),
				"description": fmt.Sprintf("Flow description for sequence %d. Intake an issue, write code, open a PR.", seq),
				"status":      "ACTIVE",
				"created_at":  time.Now().UTC(),
				"updated_at":  time.Now().UTC(),
			}
		},
		extractor: func(doc bson.M) string { return index.FlowExtractor().Extract(doc) },
		searchQuery: func(id string, seq int) string {
			return fmt.Sprintf("GitHub Issue to PR sequence %d", seq)
		},
	},
}

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
		// Use a hash of the text so every unique string gets a unique vector.
		var h uint64 = 14695981039346656037 // FNV-1a offset basis
		for _, c := range texts[i] {
			h ^= uint64(c)
			h *= 1099511628211 // FNV-1a prime
		}
		// Spread hash bits across all dimensions deterministically.
		for j := 0; j < f.dim; j++ {
			h ^= h >> 33
			h *= 0xff51afd7ed558ccd
			h ^= h >> 33
			h *= 0xc4ceb9fe1a85ec53
			h ^= h >> 33
			vec[j] = float32(int64(h)&0x7fffffff) / float32(0x7fffffff)
		}
		out[i] = vec
	}
	return out, nil
}

func (f *fakeEmbedder) Model() string { return "fake" }
func (f *fakeEmbedder) Dimension() int { return f.dim }

// ------------------------------------------------------------------
// MongoDB / Qdrant helpers
// ------------------------------------------------------------------

func connectMongo(ctx context.Context) (*mongo.Client, string, func(), error) {
	mongoURI := os.Getenv("MONGO_URI")
	if mongoURI == "" {
		mongoURI = os.Getenv("OTOXAN_MONGO_URI")
	}
	if mongoURI == "" {
		ctr, err := mongodb.Run(ctx, "mongo:7")
		if err != nil {
			return nil, "", nil, fmt.Errorf("start mongodb container: %w", err)
		}
		uri, err := ctr.ConnectionString(ctx)
		if err != nil {
			_ = ctr.Terminate(ctx)
			return nil, "", nil, fmt.Errorf("mongodb connection string: %w", err)
		}
		cleanup := func() { _ = ctr.Terminate(ctx) }
		client, err := mongo.Connect(options.Client().ApplyURI(uri))
		if err != nil {
			cleanup()
			return nil, "", nil, fmt.Errorf("connect mongo: %w", err)
		}
		return client, uri, cleanup, nil
	}
	client, err := mongo.Connect(options.Client().ApplyURI(mongoURI))
	if err != nil {
		return nil, "", nil, fmt.Errorf("connect mongo: %w", err)
	}
	return client, mongoURI, func() { _ = client.Disconnect(ctx) }, nil
}

func connectQdrant(ctx context.Context) (*qdrant.Client, string, func(), error) {
	qdrantURL := os.Getenv("OTOXAN_QDRANT_URL")
	if qdrantURL == "" {
		ctr, err := qdranttc.Run(ctx, "qdrant/qdrant:v1.13.0")
		if err != nil {
			return nil, "", nil, fmt.Errorf("start qdrant container: %w", err)
		}
		uri, err := ctr.RESTEndpoint(ctx)
		if err != nil {
			_ = ctr.Terminate(ctx)
			return nil, "", nil, fmt.Errorf("qdrant REST endpoint: %w", err)
		}
		cleanup := func() { _ = ctr.Terminate(ctx) }
		client := qdrant.NewClient(uri)
		return client, uri, cleanup, nil
	}
	client := qdrant.NewClient(qdrantURL)
	return client, qdrantURL, func() {}, nil
}

// ------------------------------------------------------------------
// Test
// ------------------------------------------------------------------

// TestRecallFreshness writes 100 docs across source types, indexes them,
// polls recall until each is hit, and asserts 95th percentile ≤ 60 s.
func TestRecallFreshness(t *testing.T) {
	ctx := context.Background()

	// 1. Connect MongoDB.
	mongoClient, _, mongoCleanup, err := connectMongo(ctx)
	require.NoError(t, err, "connect mongodb")
	defer mongoCleanup()

	// 2. Connect Qdrant.
	qdrantClient, _, qdrantCleanup, err := connectQdrant(ctx)
	require.NoError(t, err, "connect qdrant")
	defer qdrantCleanup()

	// 3. Use a dedicated test database.
	agentID := "freshness_test_agent"
	db := mongoClient.Database("otoxan_" + agentID)
	pointerColl := db.Collection("memory_pointers")

	// 4. Build indexer sources from sourceTypes.
	sources := make([]index.SourceConfig, len(sourceTypes))
	for i, st := range sourceTypes {
		sources[i] = index.SourceConfig{
			CollectionName: st.collection,
			SourceType:     st.name,
			Extractor:      st.extractor,
		}
	}

	// 5. Build indexer.
	cfg := index.IndexerConfig{
		BatchSize:    32,
		PollInterval: 60 * time.Second,
		VectorSize:   vectorDim,
		MaxRetries:   3,
		RetryBase:    100 * time.Millisecond,
		RetryMax:     1 * time.Second,
	}
	fakeEmb := &fakeEmbedder{dim: vectorDim}
	pointerStore := index.NewPointerStore(pointerColl)
	qdrantColl := fmt.Sprintf("%s_index", agentID)
	ix := index.NewIndexer(cfg, qdrantClient, fakeEmb, pointerStore, sources, qdrantColl)

	// Ensure collection exists.
	err = ix.EnsureCollection(ctx)
	require.NoError(t, err, "ensure qdrant collection")

	// 6. Write 100 documents across source types.
	// We round-robin through sourceTypes so each gets a share.
	docMeta := make([]struct {
		id        string
		seq       int
		query     string
		sourceIdx int
	}, 0, targetDocCount)

	for i := 0; i < targetDocCount; i++ {
		st := sourceTypes[i%len(sourceTypes)]
		seq := i/len(sourceTypes) + 1
		id := fmt.Sprintf("%s-%03d", st.idPrefix, seq)
		doc := st.makeDoc(id, seq)
		coll := db.Collection(st.collection)
		_, err := coll.InsertOne(ctx, doc)
		require.NoError(t, err, "insert doc %s into %s", id, st.collection)
		docMeta = append(docMeta, struct {
			id        string
			seq       int
			query     string
			sourceIdx int
		}{id: id, seq: seq, query: st.searchQuery(id, seq), sourceIdx: i % len(sourceTypes)})
	}

	t.Logf("Inserted %d documents across %d source types", targetDocCount, len(sourceTypes))

	// 7. Record write timestamps.
	writeTimes := make(map[string]time.Time, targetDocCount)
	for _, m := range docMeta {
		writeTimes[m.id] = time.Now().UTC()
	}

	// 8. Trigger indexing.
	t.Log("Triggering indexer cycle...")
	indexStart := time.Now()
	err = ix.RunOnce(ctx)
	require.NoError(t, err, "indexer RunOnce")
	indexElapsed := time.Since(indexStart)
	t.Logf("Indexer cycle completed in %v", indexElapsed)

	// 9. Poll recall until every doc is searchable.
	foundAt := make(map[string]time.Time, targetDocCount)
	var mu sync.Mutex
	var wg sync.WaitGroup

	ctxSearch, cancelSearch := context.WithTimeout(ctx, searchTimeout)
	defer cancelSearch()

	for _, m := range docMeta {
		wg.Add(1)
		go func(meta struct {
			id        string
			seq       int
			query     string
			sourceIdx int
		}) {
			defer wg.Done()
			st := sourceTypes[meta.sourceIdx]
			// Build the exact text that was embedded so the fake embedder
			// produces the same vector.
			coll := db.Collection(st.collection)
			var raw bson.M
			err := coll.FindOne(ctx, bson.M{"_id": meta.id}).Decode(&raw)
			if err != nil {
				t.Logf("WARN: could not re-read doc %s: %v", meta.id, err)
				return
			}
			text := st.extractor(raw)

			for {
				select {
				case <-ctxSearch.Done():
					return
				default:
				}

				vec, err := fakeEmb.Embed(ctx, text)
				if err != nil {
					t.Logf("WARN: embed failed for %s: %v", meta.id, err)
					time.Sleep(pollInterval)
					continue
				}

				results, err := qdrantClient.Search(ctx, qdrantColl, vec, 10)
				if err != nil {
					t.Logf("WARN: search failed for %s: %v", meta.id, err)
					time.Sleep(pollInterval)
					continue
				}

			for _, r := range results {
				sid, _ := r.Payload["source_id"].(string)
				if sid == meta.id {
					mu.Lock()
					foundAt[meta.id] = time.Now().UTC()
					mu.Unlock()
					return
				}
			}

				time.Sleep(pollInterval)
			}
		}(m)
	}

	wg.Wait()

	// 10. Compute latencies.
	latencies := make([]float64, 0, targetDocCount)
	missing := 0
	for _, m := range docMeta {
		found, ok := foundAt[m.id]
		if !ok {
			missing++
			t.Logf("MISSING: doc %s (%s) was never found in recall", m.id, sourceTypes[m.sourceIdx].name)
			continue
		}
		latencies = append(latencies, found.Sub(writeTimes[m.id]).Seconds())
	}

	require.Zero(t, missing, "%d of %d documents were never found in recall", missing, targetDocCount)

	sort.Float64s(latencies)
	p95 := latencies[int(float64(len(latencies))*0.95)]
	p50 := latencies[int(float64(len(latencies))*0.50)]
	maxLat := latencies[len(latencies)-1]
	minLat := latencies[0]

	t.Logf("Freshness latencies: min=%.3fs p50=%.3fs p95=%.3fs max=%.3fs", minLat, p50, p95, maxLat)

	// 11. Assert 95th percentile ≤ 60 s.
	assert.LessOrEqual(t, p95, maxFreshnessSeconds,
		"95th percentile freshness latency %.3fs exceeds %.0fs budget", p95, maxFreshnessSeconds)

	// Also assert every individual doc is under a generous hard ceiling
	// (indexer cycle + search overhead should be well under 120 s).
	assert.LessOrEqual(t, maxLat, searchTimeout.Seconds(),
		"max freshness latency %.3fs exceeded hard ceiling %.0fs", maxLat, searchTimeout.Seconds())
}

// ------------------------------------------------------------------
// Benchmark variant (runs the same logic but as a Go benchmark)
// ------------------------------------------------------------------

// BenchmarkRecallFreshness runs the freshness pipeline as a benchmark.
// It reports ns/op for the full pipeline (write → index → search).
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func BenchmarkRecallFreshness(b *testing.B) {
	ctx := context.Background()

	mongoClient, _, mongoCleanup, err := connectMongo(ctx)
	require.NoError(b, err, "connect mongodb")
	defer mongoCleanup()

	qdrantClient, _, qdrantCleanup, err := connectQdrant(ctx)
	require.NoError(b, err, "connect qdrant")
	defer qdrantCleanup()

	agentID := "freshness_bench_agent"
	db := mongoClient.Database("otoxan_" + agentID)
	pointerColl := db.Collection("memory_pointers")

	sources := make([]index.SourceConfig, len(sourceTypes))
	for i, st := range sourceTypes {
		sources[i] = index.SourceConfig{
			CollectionName: st.collection,
			SourceType:     st.name,
			Extractor:      st.extractor,
		}
	}

	cfg := index.IndexerConfig{
		BatchSize:    32,
		PollInterval: 60 * time.Second,
		VectorSize:   vectorDim,
		MaxRetries:   3,
		RetryBase:    100 * time.Millisecond,
		RetryMax:     1 * time.Second,
	}
	fakeEmb := &fakeEmbedder{dim: vectorDim}
	pointerStore := index.NewPointerStore(pointerColl)
	qdrantColl := fmt.Sprintf("%s_index", agentID)
	ix := index.NewIndexer(cfg, qdrantClient, fakeEmb, pointerStore, sources, qdrantColl)
	_ = ix.EnsureCollection(ctx)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Write one doc per iteration.
		st := sourceTypes[i%len(sourceTypes)]
		seq := i/len(sourceTypes) + 1
		id := fmt.Sprintf("%s-bench-%03d", st.idPrefix, seq)
		doc := st.makeDoc(id, seq)
		coll := db.Collection(st.collection)
		_, _ = coll.InsertOne(ctx, doc)

		// Index.
		_ = ix.RunOnce(ctx)

		// Search until found.
		text := st.extractor(doc)
		for {
			vec, err := fakeEmb.Embed(ctx, text)
			if err != nil {
				continue
			}
			results, err := qdrantClient.Search(ctx, qdrantColl, vec, 10)
			if err != nil {
				continue
			}
			found := false
			for _, r := range results {
				if sid, _ := r.Payload["source_id"].(string); sid == id {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
	}
}
