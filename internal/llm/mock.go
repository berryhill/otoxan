package llm

import (
	"context"
	"fmt"
	"strings"
)

// Mock implements Provider for testing.
type Mock struct {
	model string
}

// NewMock creates a mock provider.
func NewMock(model string) *Mock {
	return &Mock{model: model}
}

// RunSession echoes the prompt back as output with a canned prefix.
func (m *Mock) RunSession(ctx context.Context, prompt string) (*SessionResult, error) {
	if strings.Contains(strings.ToLower(prompt), "error") {
		return nil, fmt.Errorf("mock: simulated error")
	}
	return &SessionResult{
		Output:       fmt.Sprintf("[mock %s] %s", m.model, prompt),
		TokensUsed:   len(prompt) / 4,
		TokensInput:  len(prompt) / 4,
		TokensOutput: 10,
	}, nil
}
