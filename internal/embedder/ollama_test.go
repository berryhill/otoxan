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

// TestOllamaEmbedder_Integration attempts to hit a local Ollama instance.
// It skips if Ollama is not reachable on localhost:11434.
func TestOllamaEmbedder_Integration(t *testing.T) {
	ctx := context.Background()

	// Probe Ollama health
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://localhost:11434/api/tags")
	if err != nil {
		t.Skipf("Ollama not available on localhost:11434: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Skipf("Ollama returned status %d, skipping integration test", resp.StatusCode)
	}

	embedder := NewOllamaEmbedder("http://localhost:11434", "nomic-embed-text", 768)

	vec, err := embedder.Embed(ctx, "The quick brown fox jumps over the lazy dog")
	require.NoError(t, err, "Embed failed")
	assert.Len(t, vec, 768, "expected 768-dim vector from nomic-embed-text")
	assert.Equal(t, "nomic-embed-text", embedder.Model())
	assert.Equal(t, 768, embedder.Dimension())

	// Batch test
	vecs, err := embedder.BatchEmbed(ctx, []string{"hello world", "goodbye world"})
	require.NoError(t, err, "BatchEmbed failed")
	assert.Len(t, vecs, 2)
	assert.Len(t, vecs[0], 768)
	assert.Len(t, vecs[1], 768)
}

// TestOllamaEmbedder_Mock tests the embedder against a mock Ollama server.
func TestOllamaEmbedder_Mock(t *testing.T) {
	ctx := context.Background()

	// Build a mock server that returns a fixed 768-dim vector.
	var requestBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/embed", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)

		// Decode request so we can assert on it
		dec := json.NewDecoder(r.Body)
		require.NoError(t, dec.Decode(&requestBody))

		// Return a single 768-dim vector (all 0.1s)
		vec := make([]float64, 768)
		for i := range vec {
			vec[i] = 0.1
		}
		resp := map[string]interface{}{
			"embeddings": [][]float64{vec},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	embedder := NewOllamaEmbedder(server.URL, "nomic-embed-text", 768)
	embedder.SetHTTPClient(&http.Client{Timeout: 5 * time.Second})

	vec, err := embedder.Embed(ctx, "test input")
	require.NoError(t, err)
	assert.Len(t, vec, 768)
	assert.InDelta(t, float32(0.1), vec[0], 0.0001)

	// Verify request payload
	require.NotNil(t, requestBody)
	assert.Equal(t, "nomic-embed-text", requestBody["model"])
	inputs, ok := requestBody["input"].([]interface{})
	require.True(t, ok)
	assert.Len(t, inputs, 1)
	assert.Equal(t, "test input", inputs[0])
}

// TestOllamaEmbedder_BatchMock verifies batching with multiple texts.
func TestOllamaEmbedder_BatchMock(t *testing.T) {
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/embed", r.URL.Path)

		// Return two distinct 768-dim vectors
		vec1 := make([]float64, 768)
		vec2 := make([]float64, 768)
		vec1[0] = 1.0
		vec2[0] = 2.0
		resp := map[string]interface{}{
			"embeddings": [][]float64{vec1, vec2},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	embedder := NewOllamaEmbedder(server.URL, "nomic-embed-text", 768)
	embedder.SetHTTPClient(&http.Client{Timeout: 5 * time.Second})

	vecs, err := embedder.BatchEmbed(ctx, []string{"first", "second"})
	require.NoError(t, err)
	assert.Len(t, vecs, 2)
	assert.Len(t, vecs[0], 768)
	assert.Len(t, vecs[1], 768)
	assert.InDelta(t, float32(1.0), vecs[0][0], 0.0001)
	assert.InDelta(t, float32(2.0), vecs[1][0], 0.0001)
}

// TestOllamaEmbedder_DimensionMismatch verifies error on wrong dimension.
func TestOllamaEmbedder_DimensionMismatch(t *testing.T) {
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return a 4-dim vector when 768 is expected
		resp := map[string]interface{}{
			"embeddings": [][]float64{{0.1, 0.2, 0.3, 0.4}},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	embedder := NewOllamaEmbedder(server.URL, "nomic-embed-text", 768)
	embedder.SetHTTPClient(&http.Client{Timeout: 5 * time.Second})

	_, err := embedder.Embed(ctx, "test")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected dimension 768, got 4")
}

// TestOllamaEmbedder_EmptyBatch returns nil without calling the server.
func TestOllamaEmbedder_EmptyBatch(t *testing.T) {
	ctx := context.Background()

	// No server needed — empty batch should short-circuit.
	embedder := NewOllamaEmbedder("http://localhost:99999", "nomic-embed-text", 768)

	vecs, err := embedder.BatchEmbed(ctx, []string{})
	require.NoError(t, err)
	assert.Nil(t, vecs)
}

// TestOllamaEmbedder_DefaultBaseURL verifies default base URL.
func TestOllamaEmbedder_DefaultBaseURL(t *testing.T) {
	e := NewOllamaEmbedder("", "nomic-embed-text", 768)
	assert.Equal(t, "http://localhost:11434", e.baseURL)
}
