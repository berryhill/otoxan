// Package sessionflow defines the routing contract between the dispatch session
// and interactive session flows.  The dispatch session consumes
// sessionflow.Registry.Get(flowID) and flow.Route(ctx, RouteInput) → RouteOutput.
// Flow producers (built-in or loaded from external files) register themselves
// in a Registry so the dispatcher can look them up at session start.
package sessionflow

import "context"

// SessionFlow is the interface implemented by every interactive session flow.
// The dispatch session calls Route once per user message (or per step) to
// decide what action the orchestrator should take next.
type SessionFlow interface {
	// Route evaluates the current input and returns the next action the
	// orchestrator should perform.  The dispatch session consumes the returned
	// Action and Payload; the flow itself is stateless—any step state is
	// carried in StepState or persisted by the flow implementation.
	Route(ctx context.Context, in RouteInput) (RouteOutput, error)
}

// RouteInput is passed by the dispatch session into the flow on every Route
// call.  It carries everything the flow needs to decide the next step.
type RouteInput struct {
	// FlowID is the identifier of the flow being executed (e.g. "default",
	// "onboarding").  Set by the dispatch session from the task or session
	// metadata; consumed by the Registry to locate the flow implementation.
	FlowID string

	// StepID is the current step within the flow.  Empty for the first step.
	// Produced by the flow on the previous RouteOutput; consumed by the flow
	// to resume at the correct step.
	StepID string

	// StepState is opaque JSON-serialised state for the current step.
	// Produced by the flow on the previous RouteOutput; consumed by the flow
	// to restore step-local state (e.g. form values, flags).
	StepState []byte

	// UserMessage is the raw text the user sent in this turn.  Produced by
	// the dispatch session (from the task prompt or SSE payload); consumed by
	// the flow to decide branching or to forward to the model.
	UserMessage string

	// SessionID is the unique dispatch session identifier.  Produced by the
	// dispatch session; consumed by flows that need to log or persist
	// session-scoped data.
	SessionID string

	// TaskID is the originating dispatch task identifier.  Produced by the
	// dispatch session; consumed by flows that correlate flow state with
	// the underlying task record.
	TaskID string

	// Agent is the name of the agent running this session.  Produced by the
	// dispatch session from the task metadata; consumed by flows that vary
	// behaviour per agent (e.g. agent-specific onboarding).
	Agent string

	// Toolsets lists the MCP toolsets available to this session.  Produced by
	// the dispatch session from the task metadata; consumed by flows that
	// need to know which tools are reachable before routing.
	Toolsets []string

	// Home is the otoxan home directory path.  Produced by the dispatch
	// session from the resolved --home flag or default; consumed by flows
	// that need to read or write sentinel files (e.g. onboarding).
	Home string
}

// RouteOutput is returned by the flow to tell the dispatch session what to do
// next.  The dispatch session consumes every field; the flow produces them.
type RouteOutput struct {
	// Action tells the dispatch session what to do next.  Known values:
	//   "model_complete" — send Payload as a prompt to the orchestrator's
		//                      model_complete action and wait for a response.
	//   "wait"           — block until the next user message (no payload).
	//   "done"           — end the session successfully.
	//   "error"          — end the session with the Error field.
	// Additional actions may be defined by future flow implementations.
	Action string

	// Payload is the data required by Action.  For "model_complete" this is
	// the prompt text (or JSON) sent to the model.  Produced by the flow;
	// consumed by the dispatch session's orchestrator adapter.
	Payload string

	// NextStepID is the step the flow wants to execute on the next turn.
	// Produced by the flow; stored by the dispatch session and fed back as
	// RouteInput.StepID on the next Route call.
	NextStepID string

	// NextStepState is opaque JSON-serialised state for NextStepID.  Produced
	// by the flow; stored by the dispatch session and fed back as
	// RouteInput.StepState on the next Route call.
	NextStepState []byte

	// Error is a terminal error message when Action is "error".  Produced
	// by the flow; consumed by the dispatch session to fail the task.
	Error string
}
