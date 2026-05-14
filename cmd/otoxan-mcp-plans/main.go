package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/silas/otoxan/internal/mcp"
	"github.com/silas/otoxan/internal/store/plans"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const version = "0.1.0-dev"

func main() {
	healthCheck := flag.Bool("health-check", false, "Run health check (connect to Mongo and exit)")
	flag.Parse()

	ctx := context.Background()

	uri := os.Getenv("MONGO_URI")
	if uri == "" {
		uri = "mongodb://localhost:27017"
	}
	dbName := os.Getenv("MONGO_DB")
	if dbName == "" {
		dbName = "otoxan"
	}

	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = client.Disconnect(ctx) }()

	// Health check mode: ping Mongo and exit
	if *healthCheck {
		if err := client.Ping(ctx, nil); err != nil {
			fmt.Fprintf(os.Stderr, "health-check ping: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	planStore := plans.NewPlanStore(client.Database(dbName).Collection("plans"))

	srv := mcp.New("otoxan-mcp-plans", version)
	registerTools(srv, planStore)

	if err := srv.Serve(ctx, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "serve: %v\n", err)
		os.Exit(1)
	}
}

// ------------------------------------------------------------------
// Arg structs
// ------------------------------------------------------------------

type getPlanArgs struct {
	PlanID string `json:"plan_id" jsonschema:"required"`
}

type createPlanArgs struct {
	PlanID      string   `json:"plan_id" jsonschema:"required"`
	Title       string   `json:"title" jsonschema:"required"`
	Content     string   `json:"content"`
	Goal        string   `json:"goal"`
	Status      string   `json:"status"`
	PlanType    string   `json:"plan_type"`
	Priority    int      `json:"priority"`
	Tags        []string `json:"tags"`
	InitiativeID string  `json:"initiative_id"`
	DirectiveID  string  `json:"directive_id"`
	TeamID       string  `json:"team_id"`
}

type updatePlanArgs struct {
	PlanID string `json:"plan_id" jsonschema:"required"`
	Title  string `json:"title"`
	Content string `json:"content"`
	Goal   string `json:"goal"`
	Status string `json:"status"`
}

type listPlansArgs struct {
	Status []string `json:"status"`
	Limit  int      `json:"limit"`
}

type decomposePlanArgs struct {
	PlanID string `json:"plan_id" jsonschema:"required"`
}

// ------------------------------------------------------------------
// Tool registration
// ------------------------------------------------------------------

func registerTools(srv *mcp.Server, store *plans.PlanStore) {
	srv.Register(mcp.Tool{
		Name:        "get_plan",
		Description: "Retrieve a single plan by its plan_id.",
		InputSchema: mcp.SchemaOf[getPlanArgs](),
		Handler:     handleGetPlan(store),
	})
	srv.Register(mcp.Tool{
		Name:        "create_plan",
		Description: "Create a new plan document.",
		InputSchema: mcp.SchemaOf[createPlanArgs](),
		Handler:     handleCreatePlan(store),
	})
	srv.Register(mcp.Tool{
		Name:        "update_plan",
		Description: "Patch fields of an existing plan.",
		InputSchema: mcp.SchemaOf[updatePlanArgs](),
		Handler:     handleUpdatePlan(store),
	})
	srv.Register(mcp.Tool{
		Name:        "list_plans",
		Description: "List plans matching optional filters.",
		InputSchema: mcp.SchemaOf[listPlansArgs](),
		Handler:     handleListPlans(store),
	})
	srv.Register(mcp.Tool{
		Name:        "decompose_plan",
		Description: "Decompose a plan into sub-plans.",
		InputSchema: mcp.SchemaOf[decomposePlanArgs](),
		Handler:     handleDecomposePlan(store),
	})
}

// ------------------------------------------------------------------
// Handlers
// ------------------------------------------------------------------

func handleGetPlan(store *plans.PlanStore) func(context.Context, json.RawMessage) (any, error) {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args getPlanArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: err.Error()}
		}
		if args.PlanID == "" {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: "plan_id is required"}
		}
		plan, err := store.Get(ctx, args.PlanID)
		if err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: "plan not found: " + args.PlanID}
			}
			return nil, fmt.Errorf("store get: %w", err)
		}
		return plan, nil
	}
}

func handleCreatePlan(store *plans.PlanStore) func(context.Context, json.RawMessage) (any, error) {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args createPlanArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: err.Error()}
		}
		if args.PlanID == "" || args.Title == "" {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: "plan_id and title are required"}
		}
		p := &plans.Plan{
			PlanID:  args.PlanID,
			Title:   args.Title,
			Content: args.Content,
			Tags:    args.Tags,
		}
		if args.InitiativeID != "" {
			p.InitiativeID = &args.InitiativeID
		}
		if args.DirectiveID != "" {
			p.DirectiveID = &args.DirectiveID
		}
		if args.TeamID != "" {
			p.TeamID = &args.TeamID
		}
		if args.Status != "" {
			p.Status = plans.PlanStatus(args.Status)
		}
		if args.PlanType != "" {
			p.PlanType = plans.PlanType(args.PlanType)
		}
		_, err := store.Create(ctx, p)
		if err != nil {
			return nil, fmt.Errorf("store create: %w", err)
		}
		return p, nil
	}
}

func handleUpdatePlan(store *plans.PlanStore) func(context.Context, json.RawMessage) (any, error) {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args updatePlanArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: err.Error()}
		}
		if args.PlanID == "" {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: "plan_id is required"}
		}
		updates := bson.M{}
		if args.Title != "" {
			updates["title"] = args.Title
		}
		if args.Content != "" {
			updates["content"] = args.Content
		}
		if args.Goal != "" {
			updates["goal"] = args.Goal
		}
		if args.Status != "" {
			updates["status"] = args.Status
		}
		if len(updates) == 0 {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: "no fields provided to update"}
		}
		_, err := store.Update(ctx, args.PlanID, updates)
		if err != nil {
			return nil, fmt.Errorf("store update: %w", err)
		}
		plan, err := store.Get(ctx, args.PlanID)
		if err != nil {
			return nil, fmt.Errorf("store get after update: %w", err)
		}
		return plan, nil
	}
}

func handleListPlans(store *plans.PlanStore) func(context.Context, json.RawMessage) (any, error) {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args listPlansArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: err.Error()}
		}
		statuses := make([]plans.PlanStatus, len(args.Status))
		for i, s := range args.Status {
			statuses[i] = plans.PlanStatus(s)
		}
		opts := plans.ListOptions{
			Status: statuses,
			Limit:  args.Limit,
		}
		result, err := store.List(ctx, opts)
		if err != nil {
			return nil, fmt.Errorf("store list: %w", err)
		}
		return result, nil
	}
}

func handleDecomposePlan(store *plans.PlanStore) func(context.Context, json.RawMessage) (any, error) {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args decomposePlanArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: err.Error()}
		}
		if args.PlanID == "" {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: "plan_id is required"}
		}
		plan, err := store.Get(ctx, args.PlanID)
		if err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: "plan not found: " + args.PlanID}
			}
			return nil, fmt.Errorf("store get: %w", err)
		}
		// Decompose returns the plan with a decomposition marker.
		return map[string]any{
			"plan_id":       plan.PlanID,
			"title":         plan.Title,
			"decomposed":    true,
			"sub_plan_count": 0,
		}, nil
	}
}
