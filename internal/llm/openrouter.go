package llm

import (
	"context"
	"fmt"
)

// OpenRouter implements Provider for the OpenRouter API.
type OpenRouter struct {
	model string
}

// NewOpenRouter creates an OpenRouter provider.
// If model is empty it defaults to "anthropic/claude-3.5-sonnet".
func NewOpenRouter(model string) *OpenRouter {
	if model == "" {
		model = "anthropic/claude-3.5-sonnet"
	}
	return &OpenRouter{model: model}
}

// RunSession is a stub. It will be wired to the OpenRouter chat completions
// endpoint with the same retry/backoff semantics as Claude.
func (o *OpenRouter) RunSession(ctx context.Context, prompt string) (*SessionResult, error) {
	return nil, fmt.Errorf("openrouter: not yet implemented")
}
