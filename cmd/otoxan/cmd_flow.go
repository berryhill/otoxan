// cmd_flow.go — otoxan flow subcommand
package main

import (
	"context"
	"fmt"

	"github.com/silas/otoxan/internal/auth"
	"github.com/silas/otoxan/internal/store/flows"
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/spf13/cobra"
)

func newFlowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "flow",
		Short: "Manage flows",
		Long:  "Create, list, get, update, and delete flows in the otoxan flow store.",
	}
	cmd.AddCommand(
		newFlowListCmd(),
		newFlowGetCmd(),
		newFlowCreateCmd(),
		newFlowUpdateCmd(),
		newFlowDeleteCmd(),
	)
	return cmd
}

func newFlowListCmd() *cobra.Command {
	var (
		status string
		limit  int
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List flows",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			store, err := openFlowStore(ctx)
			if err != nil {
				return err
			}
			opts := flows.ListOptions{Limit: limit}
			if status != "" {
				opts.Status = []flows.FlowStatus{flows.FlowStatus(status)}
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

func newFlowGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <flow-id>",
		Short: "Get a flow by ID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			store, err := openFlowStore(ctx)
			if err != nil {
				return err
			}
			flow, err := store.Get(ctx, args[0])
			if err != nil {
				return err
			}
			printJSON(flow)
			return nil
		},
	}
}

func newFlowCreateCmd() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "create <flow-id>",
		Short: "Create a new flow",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			store, err := openFlowStore(ctx)
			if err != nil {
				return err
			}
			f := &flows.Flow{
				FlowID: args[0],
				Name:   name,
			}
			_, err = store.Create(ctx, f)
			if err != nil {
				return err
			}
			printJSON(f)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "flow name (required)")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func newFlowUpdateCmd() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "update <flow-id>",
		Short: "Update flow fields",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			store, err := openFlowStore(ctx)
			if err != nil {
				return err
			}
			updates := bson.M{}
			if name != "" {
				updates["name"] = name
			}
			if len(updates) == 0 {
				return fmt.Errorf("no fields to update")
			}
			_, err = store.Update(ctx, args[0], updates)
			if err != nil {
				return err
			}
			flow, err := store.Get(ctx, args[0])
			if err != nil {
				return err
			}
			printJSON(flow)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new name")
	return cmd
}

func newFlowDeleteCmd() *cobra.Command {
	var hard bool
	cmd := &cobra.Command{
		Use:   "delete <flow-id>",
		Short: "Delete a flow (soft by default)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			store, err := openFlowStore(ctx)
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

func openFlowStore(ctx context.Context) (*flows.FlowStore, error) {
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
	return flows.NewFlowStore(client.Database(dbName).Collection("flows")), nil
}
