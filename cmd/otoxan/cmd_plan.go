// cmd_plan.go — otoxan plan subcommand
package main

import (
	"context"
	"fmt"

	"github.com/silas/otoxan/internal/auth"
	"github.com/silas/otoxan/internal/store/plans"
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/spf13/cobra"
)

func newPlanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Manage plans",
		Long:  "Create, list, get, update, and delete plans in the otoxan plan store.",
	}
	cmd.AddCommand(
		newPlanListCmd(),
		newPlanGetCmd(),
		newPlanCreateCmd(),
		newPlanUpdateCmd(),
		newPlanDeleteCmd(),
	)
	return cmd
}

func newPlanListCmd() *cobra.Command {
	var (
		status string
		limit  int
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List plans",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			store, err := openPlanStore(ctx)
			if err != nil {
				return err
			}
			opts := plans.ListOptions{Limit: limit}
			if status != "" {
				opts.Status = []plans.PlanStatus{plans.PlanStatus(status)}
			}
			result, err := store.List(ctx, opts)
			if err != nil {
				return err
			}
			printJSON(result)
			return nil
		},
	}
	cmd.Flags().StringVar(&status, "status", "", "filter by status")
	cmd.Flags().IntVar(&limit, "limit", 20, "max results")
	return cmd
}

func newPlanGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <plan-id>",
		Short: "Get a plan by ID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			store, err := openPlanStore(ctx)
			if err != nil {
				return err
			}
			plan, err := store.Get(ctx, args[0])
			if err != nil {
				return err
			}
			printJSON(plan)
			return nil
		},
	}
}

func newPlanCreateCmd() *cobra.Command {
	var (
		title    string
		content  string
		goal     string
		planType string
	)
	cmd := &cobra.Command{
		Use:   "create <plan-id>",
		Short: "Create a new plan",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			store, err := openPlanStore(ctx)
			if err != nil {
				return err
			}
			p := &plans.Plan{
				PlanID:  args[0],
				Title:   title,
				Content: content,
			}
			if planType != "" {
				p.PlanType = plans.PlanType(planType)
			}
			_, err = store.Create(ctx, p)
			if err != nil {
				return err
			}
			printJSON(p)
			return nil
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "plan title (required)")
	cmd.Flags().StringVar(&content, "content", "", "plan content")
	cmd.Flags().StringVar(&goal, "goal", "", "plan goal")
	cmd.Flags().StringVar(&planType, "type", "", "plan type (standard, qa_first, hotfix)")
	_ = cmd.MarkFlagRequired("title")
	return cmd
}

func newPlanUpdateCmd() *cobra.Command {
	var (
		title   string
		content string
		goal    string
		status  string
	)
	cmd := &cobra.Command{
		Use:   "update <plan-id>",
		Short: "Update plan fields",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			store, err := openPlanStore(ctx)
			if err != nil {
				return err
			}
			updates := bson.M{}
			if title != "" {
				updates["title"] = title
			}
			if content != "" {
				updates["content"] = content
			}
			if goal != "" {
				updates["goal"] = goal
			}
			if status != "" {
				updates["status"] = status
			}
			if len(updates) == 0 {
				return fmt.Errorf("no fields to update")
			}
			_, err = store.Update(ctx, args[0], updates)
			if err != nil {
				return err
			}
			plan, err := store.Get(ctx, args[0])
			if err != nil {
				return err
			}
			printJSON(plan)
			return nil
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "new title")
	cmd.Flags().StringVar(&content, "content", "", "new content")
	cmd.Flags().StringVar(&goal, "goal", "", "new goal")
	cmd.Flags().StringVar(&status, "status", "", "new status")
	return cmd
}

func newPlanDeleteCmd() *cobra.Command {
	var hard bool
	cmd := &cobra.Command{
		Use:   "delete <plan-id>",
		Short: "Delete a plan (soft by default)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			store, err := openPlanStore(ctx)
			if err != nil {
				return err
			}
			if hard {
				_, err = store.HardDelete(ctx, args[0])
			} else {
				_, err = store.Delete(ctx, args[0])
			}
			return err
		},
	}
	cmd.Flags().BoolVar(&hard, "hard", false, "permanently delete")
	return cmd
}

// ------------------------------------------------------------------
// Helpers
// ------------------------------------------------------------------

func openPlanStore(ctx context.Context) (*plans.PlanStore, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	client, dbName, err := auth.MongoClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("mongo connect: %w", err)
	}
	if cfg.MongoDB != "" {
		dbName = cfg.MongoDB
	}
	return plans.NewPlanStore(client.Database(dbName).Collection("plans")), nil
}
