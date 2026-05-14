package qdrant

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/qdrant"
)

// TestQdrant runs collection create, upsert batch, search, and delete against
// a real Qdrant instance spun up via testcontainers.
func TestQdrant(t *testing.T) {
	ctx := context.Background()

	// Spin up Qdrant container
	container, err := qdrant.Run(ctx, "qdrant/qdrant:v1.13.0")
	require.NoError(t, err, "failed to start qdrant container")
	defer func() {
		_ = container.Terminate(ctx)
	}()

	uri, err := container.RESTEndpoint(ctx)
	require.NoError(t, err, "failed to get qdrant REST endpoint")

	client := NewClient(uri)

	collection := "test_agent_index"
	vectorSize := 4

	// 1. Create collection
	err = client.CreateCollection(ctx, collection, vectorSize)
	require.NoError(t, err, "CreateCollection failed")

	// 2. Upsert batch of points
	points := []Point{
		{
			ID:     "550e8400-e29b-41d4-a716-446655440001",
			Vector: []float32{1.0, 0.0, 0.0, 0.0},
			Payload: map[string]interface{}{
				"source_type": "session",
				"source_id":   "sess-001",
			},
		},
		{
			ID:     "550e8400-e29b-41d4-a716-446655440002",
			Vector: []float32{0.0, 1.0, 0.0, 0.0},
			Payload: map[string]interface{}{
				"source_type": "task",
				"source_id":   "task-042",
			},
		},
		{
			ID:     "550e8400-e29b-41d4-a716-446655440003",
			Vector: []float32{0.0, 0.0, 1.0, 0.0},
			Payload: map[string]interface{}{
				"source_type": "plan",
				"source_id":   "plan-007",
			},
		},
	}
	err = client.Upsert(ctx, collection, points)
	require.NoError(t, err, "Upsert batch failed")

	// 3. Search — query close to first point should return it first
	query := []float32{0.9, 0.1, 0.0, 0.0}
	results, err := client.Search(ctx, collection, query, 3)
	require.NoError(t, err, "Search failed")
	require.Len(t, results, 3, "expected 3 search results")

	assert.Equal(t, interface{}("550e8400-e29b-41d4-a716-446655440001"), results[0].ID, "expected first point as top hit")
	assert.Greater(t, results[0].Score, float32(0.8), "expected high score for top hit")
	assert.Equal(t, "session", results[0].Payload["source_type"])
	assert.Equal(t, "sess-001", results[0].Payload["source_id"])

	// 4. Delete points
	err = client.DeletePoints(ctx, collection, []string{"550e8400-e29b-41d4-a716-446655440002"})
	require.NoError(t, err, "DeletePoints failed")

	// Search again — second point should be gone, only 2 results
	results, err = client.Search(ctx, collection, query, 3)
	require.NoError(t, err, "Search after delete failed")
	require.Len(t, results, 2, "expected 2 results after deleting second point")

	for _, r := range results {
		assert.NotEqual(t, interface{}("550e8400-e29b-41d4-a716-446655440002"), r.ID, "deleted point should not appear")
	}

	// 5. Delete collection (cleanup)
	err = client.DeleteCollection(ctx, collection)
	require.NoError(t, err, "DeleteCollection failed")
}
