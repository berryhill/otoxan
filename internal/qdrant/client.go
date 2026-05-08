// Package qdrant provides a minimal HTTP client for Qdrant vector database
// operations used by the memory store.
package qdrant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Client is a lightweight HTTP wrapper around the Qdrant REST API.
type Client struct {
	baseURL string
	http    *http.Client
}

// NewClient creates a new Qdrant client targeting the given base URL.
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// SetHTTPClient overrides the default HTTP client (useful in tests).
func (c *Client) SetHTTPClient(hc *http.Client) {
	c.http = hc
}

// ------------------------------------------------------------------
// Points / vectors
// ------------------------------------------------------------------

// UpsertPointRequest is the payload for points/upsert.
type UpsertPointRequest struct {
	Points []Point `json:"points"`
}

// Point represents a single vector point.
type Point struct {
	ID      string                 `json:"id"`
	Vector  []float32              `json:"vector"`
	Payload map[string]interface{} `json:"payload,omitempty"`
}

// Upsert inserts or updates points in the given collection.
func (c *Client) Upsert(ctx context.Context, collection string, points []Point) error {
	url := fmt.Sprintf("%s/collections/%s/points?wait=true", c.baseURL, collection)
	body := UpsertPointRequest{Points: points}
	return c.put(ctx, url, body)
}

// SearchRequest is the payload for points/search.
type SearchRequest struct {
	Vector      []float32 `json:"vector"`
	Limit       int       `json:"limit"`
	WithPayload bool      `json:"with_payload"`
}

// SearchResult is a single hit from a vector search.
type SearchResult struct {
	ID      string                 `json:"id"`
	Score   float32                `json:"score"`
	Payload map[string]interface{} `json:"payload,omitempty"`
}

// Search performs a nearest-neighbour search in the given collection.
func (c *Client) Search(ctx context.Context, collection string, vector []float32, limit int) ([]SearchResult, error) {
	url := fmt.Sprintf("%s/collections/%s/points/search", c.baseURL, collection)
	body := SearchRequest{
		Vector:      vector,
		Limit:       limit,
		WithPayload: true,
	}

	respBody, err := c.postWithResponse(ctx, url, body)
	if err != nil {
		return nil, err
	}

	var result struct {
		Result []SearchResult `json:"result"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("unmarshal search response: %w", err)
	}
	return result.Result, nil
}

// DeletePoints removes points by ID from the given collection.
func (c *Client) DeletePoints(ctx context.Context, collection string, ids []string) error {
	url := fmt.Sprintf("%s/collections/%s/points/delete?wait=true", c.baseURL, collection)
	body := map[string]interface{}{
		"points": ids,
	}
	return c.post(ctx, url, body)
}

// ------------------------------------------------------------------
// Collections
// ------------------------------------------------------------------

// CreateCollectionRequest is the payload for creating a collection.
type CreateCollectionRequest struct {
	Vectors struct {
		Size     int    `json:"size"`
		Distance string `json:"distance"`
	} `json:"vectors"`
}

// CreateCollection creates a new collection with the specified vector size.
func (c *Client) CreateCollection(ctx context.Context, collection string, vectorSize int) error {
	url := fmt.Sprintf("%s/collections/%s", c.baseURL, collection)
	body := CreateCollectionRequest{}
	body.Vectors.Size = vectorSize
	body.Vectors.Distance = "Cosine"
	return c.put(ctx, url, body)
}

// DeleteCollection drops a collection.
func (c *Client) DeleteCollection(ctx context.Context, collection string) error {
	url := fmt.Sprintf("%s/collections/%s", c.baseURL, collection)
	return c.delete(ctx, url)
}

// ------------------------------------------------------------------
// HTTP helpers
// ------------------------------------------------------------------

func (c *Client) post(ctx context.Context, url string, body interface{}) error {
	_, err := c.postWithResponse(ctx, url, body)
	return err
}

func (c *Client) postWithResponse(ctx context.Context, url string, body interface{}) ([]byte, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("qdrant %s", resp.Status)
	}

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	return buf.Bytes(), nil
}

func (c *Client) put(ctx context.Context, url string, body interface{}) error {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("qdrant %s", resp.Status)
	}
	return nil
}

func (c *Client) delete(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("qdrant %s", resp.Status)
	}
	return nil
}
