// cmd_db.go — otoxan db subcommand
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/silas/otoxan/internal/state"
	"github.com/silas/otoxan/pkg/stores/agentregistry"
	"github.com/spf13/cobra"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func newDBCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "db",
		Short: "Database operations",
		Long:  `Database operations for otoxan — ping, status, init, drop, and diagnostics.`,
	}
	cmd.AddCommand(newDBPingCmd())
	cmd.AddCommand(newDBInitCmd())
	cmd.AddCommand(newDBDropCmd())
	return cmd
}

func newDBPingCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ping",
		Short: "Ping the MongoDB server",
		Long:  `Ping verifies connectivity to the MongoDB server configured via OTOXAN_MONGO_URI or config.yaml.`,
		RunE:  runDBPing,
	}
}

func runDBPing(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	uri := cfg.MongoURI
	if uri == "" {
		uri = os.Getenv("OTOXAN_MONGO_URI")
	}
	if uri == "" {
		return fmt.Errorf("no MongoDB URI configured; set mongo_uri in config.yaml or OTOXAN_MONGO_URI env var")
	}

	client, err := state.OpenClient(uri)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	if err := state.Ping(client); err != nil {
		return err
	}

	fmt.Println("ok")
	return nil
}

// ------------------------------------------------------------------
// db init
// ------------------------------------------------------------------

func newDBInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init [--global | <agent>]",
		Short: "Initialize MongoDB databases and collections",
		Long: `init creates the required collections and indexes.

  otoxan db init --global    initialise otoxan_global (agents, teams, …)
  otoxan db init xander      initialise otoxan_agent_xander (tasks, plans, …)

Both commands are idempotent: re-running exits 0 with an "already initialised" message.`,
		Args: cobra.MaximumNArgs(1),
		RunE: runDBInit,
	}
	cmd.Flags().Bool("global", false, "initialise the global otoxan_global database")
	return cmd
}

func runDBInit(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	global, _ := cmd.Flags().GetBool("global")
	var agentName string
	if len(args) > 0 {
		agentName = args[0]
	}
	if !global && agentName == "" {
		return fmt.Errorf("specify --global or an agent name (e.g. otoxan db init xander)")
	}
	if global && agentName != "" {
		return fmt.Errorf("cannot use --global with an agent name")
	}

	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	uri := cfg.MongoURI
	if uri == "" {
		uri = os.Getenv("OTOXAN_MONGO_URI")
	}
	if uri == "" {
		return fmt.Errorf("no MongoDB URI configured; set mongo_uri in config.yaml or OTOXAN_MONGO_URI env var")
	}

	client, err := state.OpenClient(uri)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	if global {
		return initGlobalDB(ctx, client)
	}
	return initAgentDB(ctx, client, agentName)
}

// initGlobalDB ensures otoxan_global has the agents collection and indexes.
func initGlobalDB(ctx context.Context, client *mongo.Client) error {
	globalDB := state.GlobalDB(client)
	_ = globalDB.Collection("agents")

	// Check whether the collection already exists.
	exists, err := collectionExists(ctx, globalDB, "agents")
	if err != nil {
		return fmt.Errorf("check agents collection: %w", err)
	}
	if exists {
		fmt.Println("global database already initialized")
		return nil
	}

	// Create the collection and indexes via the agentregistry store.
	if _, err := agentregistry.NewStore(client); err != nil {
		return fmt.Errorf("create agents store: %w", err)
	}

	fmt.Println("global database initialized")
	return nil
}

// initAgentDB ensures the per-agent database has a sentinel collection
// so the database is materialised, plus any future per-agent indexes.
func initAgentDB(ctx context.Context, client *mongo.Client, name string) error {
	if err := state.ValidateAgentName(name); err != nil {
		return err
	}

	agentDB, err := state.AgentDB(client, name)
	if err != nil {
		return err
	}

	// Check whether the sentinel already exists.
	exists, err := collectionExists(ctx, agentDB, "__init")
	if err != nil {
		return fmt.Errorf("check __init collection: %w", err)
	}
	if exists {
		fmt.Printf("agent database %s already initialized\n", agentDB.Name())
		return nil
	}

	if err := agentDB.CreateCollection(ctx, "__init"); err != nil {
		return fmt.Errorf("create agent init collection: %w", err)
	}

	fmt.Printf("agent database %s initialized\n", agentDB.Name())
	return nil
}

// collectionExists returns true if the named collection exists in db.
func collectionExists(ctx context.Context, db *mongo.Database, name string) (bool, error) {
	colls, err := db.ListCollectionNames(ctx, bson.M{})
	if err != nil {
		return false, err
	}
	for _, c := range colls {
		if c == name {
			return true, nil
		}
	}
	return false, nil
}

func newDBDropCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "drop <agent>",
		Short: "Drop an agent's database",
		Long: `Drop permanently removes the per-agent MongoDB database (otoxan_agent_<name>)
and hard-deletes the agent from the global registry.

This is destructive and cannot be undone. Pass --yes to confirm.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !yes {
				fmt.Fprintf(os.Stderr, "error: drop requires --yes to confirm\n")
				return fmt.Errorf("missing --yes flag")
			}
			return runDBDrop(args[0])
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm destructive drop")
	return cmd
}

func runDBDrop(agentName string) error {
	ctx := context.Background()

	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	uri := cfg.MongoURI
	if uri == "" {
		uri = os.Getenv("OTOXAN_MONGO_URI")
	}
	if uri == "" {
		return fmt.Errorf("no MongoDB URI configured; set mongo_uri in config.yaml or OTOXAN_MONGO_URI env var")
	}

	client, err := state.OpenClient(uri)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	// Validate agent name before touching anything.
	if err := state.ValidateAgentName(agentName); err != nil {
		return err
	}

	// Hard-delete from global registry first.
	regStore, err := agentregistry.NewStore(client)
	if err != nil {
		return fmt.Errorf("open registry store: %w", err)
	}

	_, err = regStore.HardDelete(ctx, agentName)
	if err != nil {
		// It's okay if the agent doc doesn't exist — the DB might still exist
		// from a partial setup. Only report if it's a real error.
		// mongo.ErrNoDocuments is not exported in v2 the same way, so we
		// just proceed regardless of registry delete result.
		_ = err
	}

	// Drop the per-agent database.
	agentDB, err := state.AgentDB(client, agentName)
	if err != nil {
		return err
	}
	if err := agentDB.Drop(ctx); err != nil {
		return fmt.Errorf("drop agent database %s: %w", agentDB.Name(), err)
	}

	fmt.Printf("dropped agent %q (database %s)\n", agentName, agentDB.Name())
	return nil
}
