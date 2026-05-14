package embedder

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOpenAIEmbedder_Mock tests the embedder against a mock OpenAI server.
func TestOpenAIEmbedder_Mock(t *testing.T) {
	ctx := context.Background()

	var requestBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/embeddings", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))

		dec := json.NewDecoder(r.Body)
		require.NoError(t, dec.Decode(&requestBody))

		// Return a single 1536-dim vector (all 0.1s)
		vec := make([]float64, 1536)
		for i := range vec {
			vec[i] = 0.1
		}
		resp := map[string]interface{}{
			"data": []map[string]interface{}{
				{"embedding": vec, "index": 0},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	embedder := NewOpenAIEmbedder("test-key", "text-embedding-3-small", 1536)
	embedder.baseURL = server.URL
	embedder.SetHTTPClient(&http.Client{Timeout: 5 * time.Second})

	vec, err := embedder.Embed(ctx, "test input")
	require.NoError(t, err)
	assert.Len(t, vec, 1536)
	assert.InDelta(t, float32(0.1), vec[0], 0.0001)

	// Verify request payload
	require.NotNil(t, requestBody)
	assert.Equal(t, "text-embedding-3-small", requestBody["model"])
	inputs, ok := requestBody["input"].([]interface{})
	require.True(t, ok)
	assert.Len(t, inputs, 1)
	assert.Equal(t, "test input", inputs[0])
}

// TestOpenAIEmbedder_BatchMock verifies that 32 inputs are sent in a single request.
func TestOpenAIEmbedder_BatchMock(t *testing.T) {
	ctx := context.Background()

	var requestBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/embeddings", r.URL.Path)

		dec := json.NewDecoder(r.Body)
		require.NoError(t, dec.Decode(&requestBody))

		inputs, ok := requestBody["input"].([]interface{})
		require.True(t, ok)

		// Return one vector per input, preserving index order
		data := make([]map[string]interface{}, len(inputs))
		for i := range inputs {
			vec := make([]float64, 1536)
			vec[0] = float64(i) + 1.0 // distinct value per index
			data[i] = map[string]interface{}{
				"embedding": vec,
				"index":     i,
			}
		}
		resp := map[string]interface{}{"data": data}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	embedder := NewOpenAIEmbedder("test-key", "text-embedding-3-small", 1536)
	embedder.baseURL = server.URL
	embedder.SetHTTPClient(&http.Client{Timeout: 5 * time.Second})

	// Build 32 inputs
	texts := make([]string, 32)
	for i := range texts {
		texts[i] = "input " + string(rune('a'+i))
	}

	vecs, err := embedder.BatchEmbed(ctx, texts)
	require.NoError(t, err)
	assert.Len(t, vecs, 32)

	// Verify the mock received exactly one request with 32 inputs
	require.NotNil(t, requestBody)
	inputs, ok := requestBody["input"].([]interface{})
	require.True(t, ok)
	assert.Len(t, inputs, 32, "expected 32 inputs in a single request")

	// Verify each vector has the expected dimension and distinct first value
	for i, v := range vecs {
		assert.Len(t, v, 1536, "vector %d dimension mismatch", i)
		assert.InDelta(t, float32(i+1), v[0], 0.0001, "vector %d first value mismatch", i)
	}
}

// TestOpenAIEmbedder_RateLimit verifies that a 429 response is surfaced as an error.
func TestOpenAIEmbedder_RateLimit(t *testing.T) {
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"error": map[string]interface{}{
				"message": "Rate limit exceeded",
				"type":    "rate_limit_error",
			},
		})
	}))
	defer server.Close()

	embedder := NewOpenAIEmbedder("test-key", "text-embedding-3-small", 1536)
	embedder.baseURL = server.URL
	embedder.SetHTTPClient(&http.Client{Timeout: 5 * time.Second})

	_, err := embedder.Embed(ctx, "test")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rate limited")
}

// TestOpenAIEmbedder_DimensionMismatch verifies error on wrong dimension.
func TestOpenAIEmbedder_DimensionMismatch(t *testing.T) {
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return a 4-dim vector when 1536 is expected
		resp := map[string]interface{}{
			"data": []map[string]interface{}{
				{"embedding": []float64{0.1, 0.2, 0.3, 0.4}, "index": 0},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	embedder := NewOpenAIEmbedder("test-key", "text-embedding-3-small", 1536)
	embedder.baseURL = server.URL
	embedder.SetHTTPClient(&http.Client{Timeout: 5 * time.Second})

	_, err := embedder.Embed(ctx, "test")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected dimension 1536, got 4")
}

// TestOpenAIEmbedder_EmptyBatch returns nil without calling the server.
func TestOpenAIEmbedder_EmptyBatch(t *testing.T) {
	ctx := context.Background()

	// No server needed — empty batch should short-circuit.
	embedder := NewOpenAIEmbedder("test-key", "text-embedding-3-small", 1536)

	vecs, err := embedder.BatchEmbed(ctx, []string{})
	require.NoError(t, err)
	assert.Nil(t, vecs)
}

// TestOpenAIEmbedder_DefaultModel verifies default model.
func TestOpenAIEmbedder_DefaultModel(t *testing.T) {
	e := NewOpenAIEmbedder("", "", 1536)
	assert.Equal(t, "text-embedding-3-small", e.Model())
}
