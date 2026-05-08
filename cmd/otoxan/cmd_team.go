// cmd_team.go — otoxan team subcommand
package main

import (
	"context"
	"fmt"

	"github.com/silas/otoxan/internal/auth"
	"github.com/silas/otoxan/internal/store/teams"
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/spf13/cobra"
)

func newTeamCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "team",
		Short: "Manage teams",
		Long:  "Create, list, get, update, and delete teams in the otoxan team store.",
	}
	cmd.AddCommand(
		newTeamListCmd(),
		newTeamGetCmd(),
		newTeamCreateCmd(),
		newTeamUpdateCmd(),
		newTeamDeleteCmd(),
	)
	return cmd
}

func newTeamListCmd() *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List teams",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			store, err := openTeamStore(ctx)
			if err != nil {
				return err
			}
			result, err := store.List(ctx, teams.ListOptions{Limit: limit})
			if err != nil {
				return err
			}
			printJSON(result)
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "max results")
	return cmd
}

func newTeamGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <team-id>",
		Short: "Get a team by ID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			store, err := openTeamStore(ctx)
			if err != nil {
				return err
			}
			team, err := store.Get(ctx, args[0])
			if err != nil {
				return err
			}
			printJSON(team)
			return nil
		},
	}
}

func newTeamCreateCmd() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "create <team-id>",
		Short: "Create a new team",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			store, err := openTeamStore(ctx)
			if err != nil {
				return err
			}
			t := &teams.Team{
				TeamID: args[0],
				Name:   name,
			}
			_, err = store.Create(ctx, t)
			if err != nil {
				return err
			}
			printJSON(t)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "team name (required)")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func newTeamUpdateCmd() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "update <team-id>",
		Short: "Update team fields",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			store, err := openTeamStore(ctx)
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
			team, err := store.Get(ctx, args[0])
			if err != nil {
				return err
			}
			printJSON(team)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new name")
	return cmd
}

func newTeamDeleteCmd() *cobra.Command {
	var hard bool
	cmd := &cobra.Command{
		Use:   "delete <team-id>",
		Short: "Delete a team (soft by default)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			store, err := openTeamStore(ctx)
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

func openTeamStore(ctx context.Context) (*teams.TeamStore, error) {
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
	return teams.NewTeamStore(client.Database(dbName).Collection("teams")), nil
}
