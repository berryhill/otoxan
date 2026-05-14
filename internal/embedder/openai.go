package embedder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/silas/otoxan/internal/auth"
)

// ------------------------------------------------------------------
// OpenAI embedder
// ------------------------------------------------------------------

// OpenAIEmbedder calls the OpenAI Embeddings API.
type OpenAIEmbedder struct {
	baseURL    string
	apiKey     string
	model      string
	dimension  int
	httpClient *http.Client
}

// NewOpenAIEmbedder creates an embedder targeting the OpenAI API.
// If apiKey is empty it falls back to the OPENAI_API_KEY env var, then Infisical.
// If model is empty it defaults to "text-embedding-3-small".
func NewOpenAIEmbedder(apiKey, model string, dimension int) *OpenAIEmbedder {
	if model == "" {
		model = "text-embedding-3-small"
	}
	return &OpenAIEmbedder{
		baseURL:    "https://api.openai.com",
		apiKey:     apiKey,
		model:      model,
		dimension:  dimension,
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}
}

// SetHTTPClient overrides the default HTTP client (useful in tests).
func (e *OpenAIEmbedder) SetHTTPClient(hc *http.Client) {
	e.httpClient = hc
}

// Model returns the model name.
func (e *OpenAIEmbedder) Model() string {
	return e.model
}

// Dimension returns the vector dimension.
func (e *OpenAIEmbedder) Dimension() int {
	return e.dimension
}

// Embed returns a single vector for the given text.
func (e *OpenAIEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	vecs, err := e.BatchEmbed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("openai returned no embeddings")
	}
	return vecs[0], nil
}

// BatchEmbed sends multiple texts to OpenAI's /v1/embeddings endpoint.
// The OpenAI API accepts up to 2048 inputs per request; this implementation
// sends all texts in a single call and lets the caller chunk if desired.
func (e *OpenAIEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	key, err := e.resolveKey()
	if err != nil {
		return nil, err
	}

	url := e.baseURL + "/v1/embeddings"
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
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("openai rate limited: %s", resp.Status)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("openai %s", resp.Status)
	}

	var result struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
			Index     int       `json:"index"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(result.Data) != len(texts) {
		return nil, fmt.Errorf("expected %d embeddings, got %d", len(texts), len(result.Data))
	}

	// OpenAI returns data out of order sometimes; sort by index.
	out := make([][]float32, len(texts))
	for _, d := range result.Data {
		if d.Index < 0 || d.Index >= len(texts) {
			return nil, fmt.Errorf("invalid embedding index %d", d.Index)
		}
		if len(d.Embedding) != e.dimension {
			return nil, fmt.Errorf("expected dimension %d, got %d", e.dimension, len(d.Embedding))
		}
		v32 := make([]float32, len(d.Embedding))
		for j, f := range d.Embedding {
			v32[j] = float32(f)
		}
		out[d.Index] = v32
	}

	return out, nil
}

// resolveKey returns the API key, checking the explicit field, env var, and Infisical.
func (e *OpenAIEmbedder) resolveKey() (string, error) {
	if e.apiKey != "" {
		return e.apiKey, nil
	}
	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		return key, nil
	}
	key, err := auth.InfisicalGet("OPENAI_API_KEY")
	if err != nil {
		return "", fmt.Errorf("openai api key: %w", err)
	}
	return key, nil
}
