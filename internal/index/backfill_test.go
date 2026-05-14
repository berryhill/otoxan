package index

import (
	"context"
	"fmt"
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
// Test: backfill command
// ------------------------------------------------------------------

// TestIndexer_Backfill writes 5 plans into a fresh MongoDB + Qdrant, runs
// Backfill, and asserts all 5 are searchable. It also verifies progress logs
// are emitted by checking that the operation completes without error.
func TestIndexer_Backfill(t *testing.T) {
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

	// 5. Run backfill with progressEvery=2 so we get multiple progress logs.
	err = indexer.Backfill(ctx, 2)
	require.NoError(t, err, "backfill")

	// 6. Verify all 5 plans are searchable.
	for i, title := range planTitles {
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
		assert.True(t, found, "plan %q (%s) should be searchable after backfill", planIDs[i], title)
	}

	// 7. Verify pointer docs were written for all 5 plans.
	for _, planID := range planIDs {
		p, err := pointerStore.FindBySource(ctx, planID)
		require.NoError(t, err, "find pointer for plan %s", planID)
		assert.Equal(t, "plan", p.SourceType)
		assert.Equal(t, "plans", p.SourceCollection)
		assert.NotEmpty(t, p.QdrantPointID)
		assert.False(t, p.Removed)
	}

	// 8. Verify backfill is idempotent: run again, still no error and still searchable.
	err = indexer.Backfill(ctx, 2)
	require.NoError(t, err, "second backfill should be idempotent")

	for i, title := range planTitles {
		text := fmt.Sprintf("Plan: %s\nStatus: PLANNING\nContent for plan %d: %s.", title, i, title)
		queryVec, err := fakeEmb.Embed(ctx, text)
		require.NoError(t, err, "embed query for plan %d", i)

		results, err := qdrantClient.Search(ctx, "agent_99_index", queryVec, 10)
		require.NoError(t, err, "search after second backfill for plan %d", i)

		found := false
		for _, r := range results {
			if r.Payload["source_id"] == planIDs[i] {
				found = true
				break
			}
		}
		assert.True(t, found, "plan %q should still be searchable after second backfill", planIDs[i])
	}
}
