// cmd_task.go — otoxan task subcommand
package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/silas/otoxan/internal/auth"
	"github.com/silas/otoxan/internal/store/tasks"
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/spf13/cobra"
)

func newTaskCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "Manage tasks",
		Long:  "Create, list, get, update, and delete tasks in the otoxan task store.",
	}

	cmd.AddCommand(
		newTaskListCmd(),
		newTaskGetCmd(),
		newTaskCreateCmd(),
		newTaskUpdateCmd(),
		newTaskDeleteCmd(),
	)

	return cmd
}

func newTaskListCmd() *cobra.Command {
	var (
		status string
		agent  string
		limit  int
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List tasks",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			store, err := openTaskStore(ctx)
			if err != nil {
				return err
			}

			opts := tasks.ListOptions{Limit: limit}
			if agent != "" {
				opts.Assignee = agent
			}
			if status != "" {
				opts.Status = []tasks.TaskStatus{tasks.TaskStatus(status)}
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
	cmd.Flags().StringVar(&agent, "agent", "", "filter by assignee")
	cmd.Flags().IntVar(&limit, "limit", 20, "max results")
	return cmd
}

func newTaskGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <task-id>",
		Short: "Get a task by ID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			store, err := openTaskStore(ctx)
			if err != nil {
				return err
			}
			task, err := store.Get(ctx, args[0])
			if err != nil {
				return err
			}
			printJSON(task)
			return nil
		},
	}
}

func newTaskCreateCmd() *cobra.Command {
	var (
		title       string
		description string
		status      string
		assignee    string
		priority    int
	)
	cmd := &cobra.Command{
		Use:   "create <task-id>",
		Short: "Create a new task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			store, err := openTaskStore(ctx)
			if err != nil {
				return err
			}

			t := &tasks.Task{
				TaskID:      args[0],
				Title:       title,
				Description: description,
				Assignee:    assignee,
				Priority:    priority,
			}
			if status != "" {
				t.Status = tasks.TaskStatus(status)
			}

			_, err = store.Create(ctx, t)
			if err != nil {
				return err
			}
			printJSON(t)
			return nil
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "task title (required)")
	cmd.Flags().StringVar(&description, "description", "", "task description")
	cmd.Flags().StringVar(&status, "status", "", "initial status")
	cmd.Flags().StringVar(&assignee, "assignee", "", "assignee agent")
	cmd.Flags().IntVar(&priority, "priority", 2, "priority (1-5)")
	_ = cmd.MarkFlagRequired("title")
	return cmd
}

func newTaskUpdateCmd() *cobra.Command {
	var (
		title       string
		description string
		status      string
		assignee    string
		output      string
	)
	cmd := &cobra.Command{
		Use:   "update <task-id>",
		Short: "Update task fields",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			store, err := openTaskStore(ctx)
			if err != nil {
				return err
			}

			updates := bson.M{}
			if title != "" {
				updates["title"] = title
			}
			if description != "" {
				updates["description"] = description
			}
			if status != "" {
				updates["status"] = status
			}
			if assignee != "" {
				updates["assignee"] = assignee
			}
			if output != "" {
				updates["output"] = output
			}
			if len(updates) == 0 {
				return fmt.Errorf("no fields to update")
			}

			_, err = store.Update(ctx, args[0], updates)
			if err != nil {
				return err
			}
			task, err := store.Get(ctx, args[0])
			if err != nil {
				return err
			}
			printJSON(task)
			return nil
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "new title")
	cmd.Flags().StringVar(&description, "description", "", "new description")
	cmd.Flags().StringVar(&status, "status", "", "new status")
	cmd.Flags().StringVar(&assignee, "assignee", "", "new assignee")
	cmd.Flags().StringVar(&output, "output", "", "task output")
	return cmd
}

func newTaskDeleteCmd() *cobra.Command {
	var hard bool
	cmd := &cobra.Command{
		Use:   "delete <task-id>",
		Short: "Delete a task (soft by default)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			store, err := openTaskStore(ctx)
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

func openTaskStore(ctx context.Context) (*tasks.TaskStore, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	client, dbName, err := auth.MongoClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("mongo connect: %w", err)
	}

	// Override dbName from config if set.
	if cfg.MongoDB != "" {
		dbName = cfg.MongoDB
	}

	return tasks.NewTaskStore(client.Database(dbName).Collection("tasks")), nil
}

func printJSON(v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(b))
}
