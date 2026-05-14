package embedder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Embedder is the interface for text embedding backends.
type Embedder interface {
	// Embed returns a dense vector for the given text.
	Embed(ctx context.Context, text string) ([]float32, error)
	// BatchEmbed returns vectors for multiple texts in a single call.
	BatchEmbed(ctx context.Context, texts []string) ([][]float32, error)
	// Model returns the model name used by this embedder.
	Model() string
	// Dimension returns the vector dimension.
	Dimension() int
}

// ------------------------------------------------------------------
// Ollama embedder
// ------------------------------------------------------------------

// OllamaEmbedder calls a local Ollama instance for embeddings.
type OllamaEmbedder struct {
	baseURL    string
	model      string
	dimension  int
	httpClient *http.Client
}

// NewOllamaEmbedder creates an embedder targeting the given Ollama base URL and model.
func NewOllamaEmbedder(baseURL, model string, dimension int) *OllamaEmbedder {
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	return &OllamaEmbedder{
		baseURL:    baseURL,
		model:      model,
		dimension:  dimension,
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}
}

// SetHTTPClient overrides the default HTTP client (useful in tests).
func (e *OllamaEmbedder) SetHTTPClient(hc *http.Client) {
	e.httpClient = hc
}

// Model returns the model name.
func (e *OllamaEmbedder) Model() string {
	return e.model
}

// Dimension returns the vector dimension.
func (e *OllamaEmbedder) Dimension() int {
	return e.dimension
}

// Embed returns a single vector for the given text.
func (e *OllamaEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	vecs, err := e.BatchEmbed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("ollama returned no embeddings")
	}
	return vecs[0], nil
}

// BatchEmbed sends multiple texts to Ollama's /api/embed endpoint.
func (e *OllamaEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	url := e.baseURL + "/api/embed"
	payload := map[string]interface{}{
		"model": e.model,
		"input": texts,
	}

	b, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("ollama %s", resp.Status)
	}

	var result struct {
		Embeddings [][]float64 `json:"embeddings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(result.Embeddings) != len(texts) {
		return nil, fmt.Errorf("expected %d embeddings, got %d", len(texts), len(result.Embeddings))
	}

	// Convert float64 -> float32
	out := make([][]float32, len(result.Embeddings))
	for i, vec := range result.Embeddings {
		if len(vec) != e.dimension {
			return nil, fmt.Errorf("expected dimension %d, got %d", e.dimension, len(vec))
		}
		v32 := make([]float32, len(vec))
		for j, f := range vec {
			v32[j] = float32(f)
		}
		out[i] = v32
	}
	return out, nil
}
