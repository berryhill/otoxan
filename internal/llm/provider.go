package llm

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// SessionResult is the output of a provider RunSession call.
type SessionResult struct {
	Output       string
	TokensUsed   int
	TokensInput  int
	TokensOutput int
}

// Provider is the interface for LLM backends.
type Provider interface {
	RunSession(ctx context.Context, prompt string) (*SessionResult, error)
}

// ErrProviderNotFound is returned when the requested provider is unknown.
var ErrProviderNotFound = errors.New("unknown provider")

// New instantiates a Provider by name.
// Supported names: "claude", "openrouter", "mock".
func New(name, model string) (Provider, error) {
	switch name {
	case "claude":
		return NewClaude(model), nil
	case "openrouter":
		return NewOpenRouter(model), nil
	case "mock":
		return NewMock(model), nil
	default:
		return nil, fmt.Errorf("%w: %s", ErrProviderNotFound, name)
	}
}

// retryWithBackoff runs fn up to maxAttempts with exponential backoff.
// It returns the first non-retryable error or the last error after exhausting attempts.
func retryWithBackoff(ctx context.Context, maxAttempts int, fn func() error) error {
	var lastErr error
	for i := 0; i < maxAttempts; i++ {
		if err := fn(); err != nil {
			lastErr = err
			// Simple classification: context errors and permanent errors are not retryable.
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			if i < maxAttempts-1 {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(time.Duration(i+1) * 2 * time.Second):
				}
			}
			continue
		}
		return nil
	}
	return fmt.Errorf("after %d attempts: %w", maxAttempts, lastErr)
}
