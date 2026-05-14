package sessionflow

import (
	"context"
)

// defaultFlow is the built-in pass-through flow.
// Its only job is to wrap the user's input as a model prompt and return
// Action: "model_complete".
type defaultFlow struct{}

func (defaultFlow) Route(_ context.Context, in RouteInput) (RouteOutput, error) {
	return RouteOutput{
		Action:  "model_complete",
		Payload: in.UserMessage,
	}, nil
}

func init() {
	DefaultRegistry.Register("default", defaultFlow{})
}

