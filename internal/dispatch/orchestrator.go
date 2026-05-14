package dispatch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ------------------------------------------------------------------
// Config
// ------------------------------------------------------------------

// OrchestratorConfig carries the model and tool settings for the orchestrator.
type OrchestratorConfig struct {
	// Default is the key into Models that identifies the active model.
	Default string
	// Models is a map of model-name -> per-model settings.
	Models map[string]ModelConfig
}

// ModelConfig holds per-model settings.
type ModelConfig struct {
	Provider string // e.g. "claude", "openrouter", "mock"
	Model    string // model identifier passed to the provider
	APIKey   string // optional API key; falls back to env when empty
	BaseURL  string // optional custom endpoint; provider default when empty
}

// ------------------------------------------------------------------
// Orchestrator
// ------------------------------------------------------------------

// Orchestrator dispatches routed actions to model, tool, noop, or handoff.
type Orchestrator struct {
	cfg    OrchestratorConfig
	client *http.Client
	tools  map[string]ToolFunc
}

// ToolFunc is the signature for tool invocations.
type ToolFunc func(ctx context.Context, args string) (string, error)

// Reply is the result of an orchestrator invocation.
type Reply struct {
	Text    string
	Handoff *HandoffRequest
	Error   string
}

// HandoffRequest carries a handoff action.
type HandoffRequest struct {
	TargetFlow string
	Payload    string
}

// Routed carries the flow routing decision produced by a SessionFlow.
type Routed struct {
	Action  string
	Payload string
}

// NewOrchestrator creates an orchestrator with the given config and a default
// HTTP client.  Built-in tools (echo) are pre-registered.
func NewOrchestrator(cfg OrchestratorConfig) *Orchestrator {
	o := &Orchestrator{
		cfg:    cfg,
		client: http.DefaultClient,
		tools:  make(map[string]ToolFunc),
	}
	o.RegisterTool("echo", echoTool)
	return o
}

// WithClient sets a custom HTTP client (used by tests to inject a mock server).
func (o *Orchestrator) WithClient(client *http.Client) *Orchestrator {
	o.client = client
	return o
}

// RegisterTool adds a named tool to the registry.  Overwrites if name exists.
func (o *Orchestrator) RegisterTool(name string, fn ToolFunc) {
	o.tools[name] = fn
}

// Invoke dispatches the routed action and returns a Reply.
// Errors from the action itself are returned in Reply.Error; only structural
// failures (marshal, network) are returned as Go errors.
func (o *Orchestrator) Invoke(ctx context.Context, routed Routed) (*Reply, error) {
	switch routed.Action {
	case "model_complete":
		return o.modelComplete(ctx, routed.Payload)
	case "tool":
		return o.invokeTool(ctx, routed.Payload)
	case "noop":
		return &Reply{}, nil
	case "handoff":
		return o.handoff(routed.Payload), nil
	default:
		return &Reply{Error: fmt.Sprintf("unknown action: %s", routed.Action)}, nil
	}
}

// ------------------------------------------------------------------
// model_complete
// ------------------------------------------------------------------

func (o *Orchestrator) modelComplete(ctx context.Context, prompt string) (*Reply, error) {
	model, ok := o.cfg.Models[o.cfg.Default]
	if !ok {
		return &Reply{Error: fmt.Sprintf("model %q not found in config", o.cfg.Default)}, nil
	}

	url := model.BaseURL
	if url == "" {
		url = "https://api.anthropic.com/v1/messages"
	}

	payload := map[string]any{
		"model":      model.Model,
		"max_tokens": 4096,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	}

	b, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	req.Header.Set("content-type", "application/json")
	if model.APIKey != "" {
		req.Header.Set("x-api-key", model.APIKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	}

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http do: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return &Reply{Error: fmt.Sprintf("http %d: %s", resp.StatusCode, string(body))}, nil
	}

	var parsed struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}

	var out string
	for _, block := range parsed.Content {
		if block.Type == "text" {
			out += block.Text
		}
	}

	return &Reply{Text: out}, nil
}

// ------------------------------------------------------------------
// tool
// ------------------------------------------------------------------

func (o *Orchestrator) invokeTool(ctx context.Context, payload string) (*Reply, error) {
	// Format: "tool_name|args"  or just "tool_name"
	var toolName, toolArgs string
	if idx := strings.Index(payload, "|"); idx >= 0 {
		toolName = payload[:idx]
		toolArgs = payload[idx+1:]
	} else {
		toolName = payload
	}

	fn, ok := o.tools[toolName]
	if !ok {
		return &Reply{Error: fmt.Sprintf("unknown tool: %s", toolName)}, nil
	}

	out, err := fn(ctx, toolArgs)
	if err != nil {
		return &Reply{Error: err.Error()}, nil
	}

	return &Reply{Text: out}, nil
}

// ------------------------------------------------------------------
// handoff
// ------------------------------------------------------------------

func (o *Orchestrator) handoff(payload string) *Reply {
	return &Reply{
		Handoff: &HandoffRequest{
			TargetFlow: payload,
			Payload:    payload,
		},
	}
}

// ------------------------------------------------------------------
// Built-in tools
// ------------------------------------------------------------------

func echoTool(_ context.Context, args string) (string, error) {
	return args, nil
}
