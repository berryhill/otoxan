package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/silas/otoxan/internal/auth"
)

// Claude implements Provider for the Anthropic Messages API.
type Claude struct {
	model  string
	client *http.Client
}

// NewClaude creates a Claude provider.
// If model is empty it defaults to "claude-sonnet-4-20250514".
func NewClaude(model string) *Claude {
	if model == "" {
		model = "claude-sonnet-4-20250514"
	}
	return &Claude{
		model:  model,
		client: &http.Client{Timeout: 120 * time.Second},
	}
}

// RunSession sends a single-turn messages request to Anthropic.
func (c *Claude) RunSession(ctx context.Context, prompt string) (*SessionResult, error) {
	apiKey, err := c.apiKey()
	if err != nil {
		return nil, err
	}

	payload := map[string]any{
		"model":      c.model,
		"max_tokens": 4096,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	}

	var result *SessionResult
	var lastErr error

	err = retryWithBackoff(ctx, 3, func() error {
		b, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal: %w", err)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.anthropic.com/v1/messages", bytes.NewReader(b))
		if err != nil {
			return fmt.Errorf("request: %w", err)
		}
		req.Header.Set("x-api-key", apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Set("content-type", "application/json")

		resp, err := c.client.Do(req)
		if err != nil {
			return fmt.Errorf("http do: %w", err)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("read body: %w", err)
		}

		if resp.StatusCode >= 500 {
			return fmt.Errorf("anthropic %d: %s", resp.StatusCode, string(body))
		}
		if resp.StatusCode >= 400 {
			// 4xx is not retryable.
			lastErr = fmt.Errorf("anthropic %d: %s", resp.StatusCode, string(body))
			return lastErr
		}

		var parsed struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			Usage struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(body, &parsed); err != nil {
			return fmt.Errorf("unmarshal: %w", err)
		}

		var out string
		for _, block := range parsed.Content {
			if block.Type == "text" {
				out += block.Text
			}
		}

		result = &SessionResult{
			Output:       out,
			TokensInput:  parsed.Usage.InputTokens,
			TokensOutput: parsed.Usage.OutputTokens,
			TokensUsed:   parsed.Usage.InputTokens + parsed.Usage.OutputTokens,
		}
		return nil
	})

	if err != nil {
		return nil, err
	}
	return result, nil
}

// apiKey resolves the Anthropic API key.
// It checks ANTHROPIC_API_KEY env, then falls back to Infisical "ANTHROPIC_API_KEY".
func (c *Claude) apiKey() (string, error) {
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		return key, nil
	}
	key, err := auth.InfisicalGet("ANTHROPIC_API_KEY")
	if err != nil {
		return "", fmt.Errorf("anthropic api key: %w", err)
	}
	return key, nil
}
