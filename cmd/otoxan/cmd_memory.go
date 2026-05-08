// cmd_memory.go — otoxan memory subcommand
package main

import (
	"context"
	"fmt"

	"github.com/silas/otoxan/internal/auth"
	"github.com/silas/otoxan/internal/store/memory"
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/spf13/cobra"
)

func newMemoryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "memory",
		Short: "Manage agent memory",
		Long:  "Create, list, get, update, and delete memory entries in the otoxan memory store.",
	}
	cmd.AddCommand(
		newMemoryListCmd(),
		newMemoryGetCmd(),
		newMemoryCreateCmd(),
		newMemoryUpdateCmd(),
		newMemoryDeleteCmd(),
	)
	return cmd
}

func newMemoryListCmd() *cobra.Command {
	var (
		agent string
		memType string
		limit int
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List memories",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			store, err := openMemoryStore(ctx)
			if err != nil {
				return err
			}
			opts := memory.ListOptions{AgentID: agent, Limit: limit}
			if memType != "" {
				opts.Type = []memory.MemoryType{memory.MemoryType(memType)}
			}
			result, err := store.List(ctx, opts)
			if err != nil {
				return err
			}
			printJSON(result)
			return nil
		},
	}
	cmd.Flags().StringVar(&agent, "agent", "", "filter by agent id")
	cmd.Flags().StringVar(&memType, "type", "", "filter by memory type")
	cmd.Flags().IntVar(&limit, "limit", 20, "max results")
	return cmd
}

func newMemoryGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <memory-id>",
		Short: "Get a memory entry by ID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			store, err := openMemoryStore(ctx)
			if err != nil {
				return err
			}
			mem, err := store.Get(ctx, args[0])
			if err != nil {
				return err
			}
			printJSON(mem)
			return nil
		},
	}
}

func newMemoryCreateCmd() *cobra.Command {
	var (
		agentID string
		content string
		memType string
	)
	cmd := &cobra.Command{
		Use:   "create <memory-id>",
		Short: "Create a new memory entry",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			store, err := openMemoryStore(ctx)
			if err != nil {
				return err
			}
			m := &memory.Memory{
				MemoryID: args[0],
				AgentID:  agentID,
				Content:  content,
			}
			if memType != "" {
				m.Type = memory.MemoryType(memType)
			}
			_, err = store.Create(ctx, m)
			if err != nil {
				return err
			}
			printJSON(m)
			return nil
		},
	}
	cmd.Flags().StringVar(&agentID, "agent", "", "agent id (required)")
	cmd.Flags().StringVar(&content, "content", "", "memory content (required)")
	cmd.Flags().StringVar(&memType, "type", "", "memory type")
	_ = cmd.MarkFlagRequired("agent")
	_ = cmd.MarkFlagRequired("content")
	return cmd
}

func newMemoryUpdateCmd() *cobra.Command {
	var content string
	cmd := &cobra.Command{
		Use:   "update <memory-id>",
		Short: "Update memory content",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			store, err := openMemoryStore(ctx)
			if err != nil {
				return err
			}
			updates := bson.M{}
			if content != "" {
				updates["content"] = content
			}
			if len(updates) == 0 {
				return fmt.Errorf("no fields to update")
			}
			_, err = store.Update(ctx, args[0], updates)
			if err != nil {
				return err
			}
			mem, err := store.Get(ctx, args[0])
			if err != nil {
				return err
			}
			printJSON(mem)
			return nil
		},
	}
	cmd.Flags().StringVar(&content, "content", "", "new content")
	return cmd
}

func newMemoryDeleteCmd() *cobra.Command {
	var hard bool
	cmd := &cobra.Command{
		Use:   "delete <memory-id>",
		Short: "Delete a memory entry (soft by default)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			store, err := openMemoryStore(ctx)
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

func openMemoryStore(ctx context.Context) (*memory.MemoryStore, error) {
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
	return memory.NewMemoryStore(client.Database(dbName).Collection("memory"), nil), nil
}
