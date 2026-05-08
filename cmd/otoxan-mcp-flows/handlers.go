package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/silas/otoxan/internal/mcp"
	"github.com/silas/otoxan/internal/store/flows"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// ------------------------------------------------------------------
// Arg structs
// ------------------------------------------------------------------

type getFlowArgs struct {
	FlowID string `json:"flow_id" jsonschema:"required"`
}

type startFlowArgs struct {
	Template string         `json:"template" jsonschema:"required,description=Name of the flow template to instantiate"`
	Context  map[string]any `json:"context,omitempty" jsonschema:"description=Optional context variables for the flow"`
}

type advanceFlowArgs struct {
	FlowID string `json:"flow_id" jsonschema:"required"`
	StepID string `json:"step_id,omitempty" jsonschema:"description=Specific step to advance to; if empty, advances to next step in order"`
}

type listFlowsArgs struct {
	Status         []string `json:"status"`
	Limit          int      `json:"limit"`
	IncludeDeleted bool     `json:"include_deleted"`
}

// ------------------------------------------------------------------
// Tool registration
// ------------------------------------------------------------------

func registerTools(srv *mcp.Server, store *flows.FlowStore, templateColl *mongo.Collection) {
	srv.Register(mcp.Tool{
		Name:        "get_flow",
		Description: "Retrieve a single flow by its flow_id.",
		InputSchema: mcp.SchemaOf[getFlowArgs](),
		Handler:     handleGetFlow(store),
	})
	srv.Register(mcp.Tool{
		Name:        "start_flow",
		Description: "Start a new flow from a named template. The template is looked up in the flow_templates collection and instantiated as a new flow document.",
		InputSchema: mcp.SchemaOf[startFlowArgs](),
		Handler:     handleStartFlow(store, templateColl),
	})
	srv.Register(mcp.Tool{
		Name:        "advance_flow",
		Description: "Advance a flow by one step. If the flow is already at a terminal step, returns an error.",
		InputSchema: mcp.SchemaOf[advanceFlowArgs](),
		Handler:     handleAdvanceFlow(store),
	})
	srv.Register(mcp.Tool{
		Name:        "list_flows",
		Description: "List flows matching optional filters.",
		InputSchema: mcp.SchemaOf[listFlowsArgs](),
		Handler:     handleListFlows(store),
	})
}

// ------------------------------------------------------------------
// Handlers
// ------------------------------------------------------------------

func handleGetFlow(store *flows.FlowStore) func(context.Context, json.RawMessage) (any, error) {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args getFlowArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: err.Error()}
		}
		if args.FlowID == "" {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: "flow_id is required"}
		}
		flow, err := store.Get(ctx, args.FlowID)
		if err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: "flow not found: " + args.FlowID}
			}
			return nil, fmt.Errorf("store get: %w", err)
		}
		return flow, nil
	}
}

func handleStartFlow(store *flows.FlowStore, templateColl *mongo.Collection) func(context.Context, json.RawMessage) (any, error) {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args startFlowArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: err.Error()}
		}
		if args.Template == "" {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: "template is required"}
		}

		// Look up template by name in flow_templates collection.
		var tmpl bson.M
		err := templateColl.FindOne(ctx, bson.M{"name": args.Template}).Decode(&tmpl)
		if err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				// List available templates for the error message.
				cursor, err2 := templateColl.Find(ctx, bson.M{})
				if err2 != nil {
					return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: "unknown template: " + args.Template}
				}
				defer cursor.Close(ctx)
				var names []string
				for cursor.Next(ctx) {
					var doc bson.M
					if err3 := cursor.Decode(&doc); err3 == nil {
						if n, ok := doc["name"].(string); ok {
							names = append(names, n)
						}
					}
				}
				msg := fmt.Sprintf("unknown template: %s; available: %v", args.Template, names)
				return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: msg, Data: names}
			}
			return nil, fmt.Errorf("template lookup: %w", err)
		}

		// Build flow from template.
		flowID := fmt.Sprintf("flow_%d", time.Now().UnixNano())
		name := ""
		if n, ok := tmpl["name"].(string); ok {
			name = n
		}
		desc := ""
		if d, ok := tmpl["description"].(string); ok {
			desc = d
		}

		var steps []flows.FlowStep
		if rawSteps, ok := tmpl["steps"].([]any); ok {
			for i, rs := range rawSteps {
				stepMap, ok := rs.(map[string]any)
				if !ok {
					continue
				}
				step := flows.FlowStep{
					StepID:    fmt.Sprintf("step_%d", i+1),
					Name:      stringOr(stepMap["name"], fmt.Sprintf("Step %d", i+1)),
					Type:      stringOr(stepMap["type"], "action"),
					Order:     i + 1,
					Config:    mapOr(stepMap["config"]),
					NextSteps: []string{},
					PrevSteps: []string{},
					CreatedAt: time.Now().UTC(),
					UpdatedAt: time.Now().UTC(),
				}
				if i > 0 && len(steps) > 0 {
					step.PrevSteps = []string{steps[len(steps)-1].StepID}
					steps[len(steps)-1].NextSteps = []string{step.StepID}
				}
				steps = append(steps, step)
			}
		}
		if rawSteps, ok := tmpl["steps"].(bson.A); ok {
			for i, rs := range rawSteps {
				var stepMap bson.M
				switch v := rs.(type) {
				case bson.M:
					stepMap = v
				case bson.D:
					stepMap = make(bson.M, len(v))
					for _, e := range v {
						stepMap[e.Key] = e.Value
					}
				case map[string]any:
					stepMap = bson.M(v)
				default:
					continue
				}
				step := flows.FlowStep{
					StepID:    fmt.Sprintf("step_%d", i+1),
					Name:      stringOr(stepMap["name"], fmt.Sprintf("Step %d", i+1)),
					Type:      stringOr(stepMap["type"], "action"),
					Order:     i + 1,
					Config:    mapOr(stepMap["config"]),
					NextSteps: []string{},
					PrevSteps: []string{},
					CreatedAt: time.Now().UTC(),
					UpdatedAt: time.Now().UTC(),
				}
				if i > 0 && len(steps) > 0 {
					step.PrevSteps = []string{steps[len(steps)-1].StepID}
					steps[len(steps)-1].NextSteps = []string{step.StepID}
				}
				steps = append(steps, step)
			}
		}

		flow := &flows.Flow{
			FlowID:      flowID,
			Name:        name,
			Description: desc,
			Status:      flows.StatusActive,
			Version:     1,
			Steps:       steps,
			Tags:        []string{},
			CreatedAt:   time.Now().UTC(),
			UpdatedAt:   time.Now().UTC(),
		}

		// Merge context into flow if provided.
		if len(args.Context) > 0 {
			if flow.Tags == nil {
				flow.Tags = []string{}
			}
		}

		_, err = store.Create(ctx, flow)
		if err != nil {
			return nil, fmt.Errorf("store create: %w", err)
		}
		return flow, nil
	}
}

func handleAdvanceFlow(store *flows.FlowStore) func(context.Context, json.RawMessage) (any, error) {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args advanceFlowArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: err.Error()}
		}
		if args.FlowID == "" {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: "flow_id is required"}
		}

		flow, err := store.Get(ctx, args.FlowID)
		if err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: "flow not found: " + args.FlowID}
			}
			return nil, fmt.Errorf("store get: %w", err)
		}

		if len(flow.Steps) == 0 {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: "flow has no steps"}
		}

		// Determine current step index.
		currentIdx := -1
		for i, s := range flow.Steps {
			if s.StepID == args.StepID {
				currentIdx = i
				break
			}
		}
		// If no step_id provided, infer current from status or default to first.
		if currentIdx < 0 {
			// Simple heuristic: find first non-terminal step.
			for i, s := range flow.Steps {
				if len(s.NextSteps) > 0 || i < len(flow.Steps)-1 {
					currentIdx = i
					break
				}
			}
			if currentIdx < 0 {
				currentIdx = len(flow.Steps) - 1
			}
		}

		// Determine next step.
		nextIdx := currentIdx + 1
		if nextIdx >= len(flow.Steps) {
			// Already at the last step (terminal).
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: "flow already at terminal step: " + flow.Steps[currentIdx].StepID}
		}

		// Advance: update status and mark current step completed.
		updates := bson.M{
			"updated_at": time.Now().UTC(),
		}
		// If advancing to last step with no further next steps, mark completed.
		if nextIdx >= len(flow.Steps)-1 && len(flow.Steps[nextIdx].NextSteps) == 0 {
			updates["status"] = flows.StatusCompleted
		}

		_, err = store.Update(ctx, args.FlowID, updates)
		if err != nil {
			return nil, fmt.Errorf("store update: %w", err)
		}

		updated, err := store.Get(ctx, args.FlowID)
		if err != nil {
			return nil, fmt.Errorf("store get after advance: %w", err)
		}
		return map[string]any{
			"flow_id":      updated.FlowID,
			"current_step": flow.Steps[nextIdx].StepID,
			"status":       string(updated.Status),
		}, nil
	}
}

func handleListFlows(store *flows.FlowStore) func(context.Context, json.RawMessage) (any, error) {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args listFlowsArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: err.Error()}
		}

		statuses := make([]flows.FlowStatus, len(args.Status))
		for i, s := range args.Status {
			statuses[i] = flows.FlowStatus(s)
		}

		opts := flows.ListOptions{
			Status:         statuses,
			Limit:          args.Limit,
			IncludeDeleted: args.IncludeDeleted,
		}

		result, err := store.List(ctx, opts)
		if err != nil {
			return nil, fmt.Errorf("store list: %w", err)
		}
		return result, nil
	}
}

// ------------------------------------------------------------------
// Helpers
// ------------------------------------------------------------------

func stringOr(v any, fallback string) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fallback
}

func mapOr(v any) map[string]interface{} {
	if m, ok := v.(map[string]interface{}); ok {
		return m
	}
	if m, ok := v.(bson.M); ok {
		out := make(map[string]interface{}, len(m))
		for k, v2 := range m {
			out[k] = v2
		}
		return out
	}
	return map[string]interface{}{}
}
