// Package recall provides semantic search across an agent's entire history
// stored in Qdrant. It wraps the qdrant client and an embedder to enable
// the `otoxan recall` search surface.
package recall

import (
	"context"
	"fmt"
	"time"

	"github.com/silas/otoxan/internal/embedder"
	"github.com/silas/otoxan/internal/qdrant"
)

// ------------------------------------------------------------------
// Result
// ------------------------------------------------------------------

// Result is a single ranked hit from a recall search.
type Result struct {
	ID          string                 `json:"id"`
	Score       float32                `json:"score"`
	SourceType  string                 `json:"source_type"`
	SourceID    string                 `json:"source_id"`
	Content     string                 `json:"content,omitempty"`
	Payload     map[string]interface{} `json:"payload,omitempty"`
	IndexedAt   time.Time              `json:"indexed_at,omitempty"`
}

// ------------------------------------------------------------------
// Recall
// ------------------------------------------------------------------

// Recall performs semantic search over an agent's Qdrant collection.
// It embeds the query, searches Qdrant, and returns ranked results.
func Recall(ctx context.Context, query string, embedder embedder.Embedder, qdrantClient *qdrant.Client, collection string, limit int) ([]Result, error) {
	if embedder == nil {
		return nil, fmt.Errorf("embedder is required")
	}
	if qdrantClient == nil {
		return nil, fmt.Errorf("qdrant client is required")
	}
	if collection == "" {
		return nil, fmt.Errorf("collection is required")
	}
	if limit <= 0 {
		limit = 10
	}

	// Embed the query
	vec, err := embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}

	// Search Qdrant
	raw, err := qdrantClient.Search(ctx, collection, vec, limit)
	if err != nil {
		return nil, fmt.Errorf("qdrant search: %w", err)
	}

	// Map to Result
	results := make([]Result, 0, len(raw))
	for _, r := range raw {
		res := Result{
			ID:     fmt.Sprintf("%v", r.ID),
			Score:  r.Score,
			Payload: r.Payload,
		}
		if r.Payload != nil {
			if v, ok := r.Payload["source_type"].(string); ok {
				res.SourceType = v
			}
			if v, ok := r.Payload["source_id"].(string); ok {
				res.SourceID = v
			}
			if v, ok := r.Payload["content"].(string); ok {
				res.Content = v
			}
			if v, ok := r.Payload["indexed_at"].(string); ok {
				res.IndexedAt, _ = time.Parse(time.RFC3339, v)
			}
		}
		results = append(results, res)
	}

	return results, nil
}
