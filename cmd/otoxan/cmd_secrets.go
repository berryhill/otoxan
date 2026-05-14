// cmd_secrets.go — otoxan secrets subcommand
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/silas/otoxan/internal/auth"
	"github.com/silas/otoxan/internal/config"
	"github.com/silas/otoxan/internal/secrets"
	"github.com/silas/otoxan/pkg/stores/scopestore"
	"github.com/spf13/cobra"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func newSecretsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secrets",
		Short: "Secret management via Xander",
		Long:  `Test, list, and manage secret scopes mediated by Xander.`,
	}
	cmd.AddCommand(
		newSecretsTestCmd(),
		newSecretsListCmd(),
		newSecretsGrantCmd(),
		newSecretsRevokeCmd(),
	)
	return cmd
}

// ------------------------------------------------------------------
// secrets list
// ------------------------------------------------------------------

func newSecretsListCmd() *cobra.Command {
	var (
		agentName      string
		limit          int
		includeDeleted bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List secret scopes",
		Long:  `List all secret scopes, optionally filtered by agent name.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			store, err := openScopeStore(ctx)
			if err != nil {
				return err
			}
			opts := scopestore.ListOptions{
				AgentName:      agentName,
				Limit:          limit,
				IncludeDeleted: includeDeleted,
			}
			result, err := store.List(ctx, opts)
			if err != nil {
				return err
			}
			b, _ := json.MarshalIndent(result, "", "  ")
			if len(result) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "[]")
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), string(b))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&agentName, "agent", "", "filter by agent name")
	cmd.Flags().IntVar(&limit, "limit", 0, "max results (0 = unlimited)")
	cmd.Flags().BoolVar(&includeDeleted, "include-deleted", false, "show soft-deleted scopes")
	return cmd
}

// ------------------------------------------------------------------
// secrets grant
// ------------------------------------------------------------------

func newSecretsGrantCmd() *cobra.Command {
	var paths []string
	cmd := &cobra.Command{
		Use:   "grant <agent-name>",
		Short: "Grant secret paths to an agent",
		Long: `Grant creates or updates a scope document for the named agent,
replacing any previous secret paths with the new set.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			store, err := openScopeStore(ctx)
			if err != nil {
				return err
			}
			if len(paths) == 0 {
				return fmt.Errorf("at least one --path is required")
			}
			res, err := store.Grant(ctx, args[0], paths)
			if err != nil {
				return err
			}
			b, _ := json.MarshalIndent(map[string]any{
				"agent_name":   args[0],
				"secret_paths": paths,
				"upserted":     res.UpsertedCount > 0,
				"modified":     res.ModifiedCount > 0,
			}, "", "  ")
			fmt.Fprintln(cmd.OutOrStdout(), string(b))
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&paths, "path", nil, "secret path(s) to grant (required, repeatable)")
	_ = cmd.MarkFlagRequired("path")
	return cmd
}

// ------------------------------------------------------------------
// secrets revoke
// ------------------------------------------------------------------

func newSecretsRevokeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "revoke <agent-name>",
		Short: "Revoke all secret scopes from an agent",
		Long:  `Revoke soft-deletes the scope document for the named agent.`,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			store, err := openScopeStore(ctx)
			if err != nil {
				return err
			}
			res, err := store.Revoke(ctx, args[0])
			if err != nil {
				return err
			}
			b, _ := json.MarshalIndent(map[string]any{
				"agent_name": args[0],
				"deleted":    res.ModifiedCount > 0,
			}, "", "  ")
			fmt.Fprintln(cmd.OutOrStdout(), string(b))
			return nil
		},
	}
	return cmd
}

// ------------------------------------------------------------------
// secrets test
// ------------------------------------------------------------------

// newSecretsTestCmd creates the `otoxan secrets test --as <agent> <scope>` subcommand.
//
// It verifies that the named agent has been granted the named scope, resolves
// each secret path via a mockable XanderClient, and prints presence markers
// without ever leaking the actual secret values.
func newSecretsTestCmd() *cobra.Command {
	var asAgent string
	cmd := &cobra.Command{
		Use:   "test <scope>",
		Short: "Test whether an agent's scope resolves",
		Long: `test checks that the requested scope exists for the agent and that
each secret path can be resolved.  It prints only presence markers — never
secret values.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			scopeName := args[0]

			if asAgent == "" {
				return fmt.Errorf("--as <agent> is required")
			}

			// Load config to get Infisical settings.
			cfg, err := loadConfig()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			// Open MongoDB client.
			client, _, err := auth.MongoClient(ctx)
			if err != nil {
				return fmt.Errorf("mongo connect: %w", err)
			}

			// Build scope store.
			scopes, err := scopestore.NewStore(client)
			if err != nil {
				return fmt.Errorf("open scope store: %w", err)
			}

			// Look up the agent's scope.
			scopeDoc, err := scopes.Get(ctx, asAgent)
			if err != nil {
				if err == mongo.ErrNoDocuments {
					return fmt.Errorf("agent %q has no scope granted", asAgent)
				}
				return fmt.Errorf("scope lookup: %w", err)
			}
			if scopeDoc == nil {
				return fmt.Errorf("agent %q has no scope granted", asAgent)
			}

			// Filter paths to those matching the requested scope name.
			// A path like "/linear/api_token" matches scope "linear".
			var matched []string
			for _, p := range scopeDoc.SecretPaths {
				if pathMatchesScope(p, scopeName) {
					matched = append(matched, p)
				}
			}
			if len(matched) == 0 {
				return fmt.Errorf("scope %q not found in agent %q's granted paths", scopeName, asAgent)
			}

			// Build Infisical client from config.
			infisical := buildInfisicalClient(cfg)
			xander := secrets.NewXanderClient(infisical, scopes)
			xander.WorkspaceID = os.Getenv("INFISICAL_PROJECT_ID")
			if xander.WorkspaceID == "" {
				xander.WorkspaceID = cfg.Infisical.ProjectID
			}
			xander.Environment = os.Getenv("INFISICAL_ENV")
			if xander.Environment == "" {
				xander.Environment = cfg.Infisical.Env
			}
			// Resolve each matched path via Xander (presence check only).
			var results []string
			for _, path := range matched {
				name := pathToSecretName(path)
				_, err = infisical.Get(ctx, name, xander.WorkspaceID, xander.Environment)
				if err != nil {
					results = append(results, fmt.Sprintf("%s: missing (%v)", path, err))
				} else {
					results = append(results, fmt.Sprintf("%s: present", path))
				}
			}

			out := fmt.Sprintf("paths_resolved=[%s]\n", strings.Join(results, ", "))
			fmt.Fprint(cmd.OutOrStdout(), out)
			return nil
		},
	}
	cmd.Flags().StringVar(&asAgent, "as", "", "agent name to test scope for (required)")
	return cmd
}

// pathMatchesScope reports whether a secret path belongs to the given scope.
// e.g. "/linear/api_token" matches scope "linear".
func pathMatchesScope(path, scope string) bool {
	path = strings.TrimPrefix(path, "/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 {
		return false
	}
	return parts[0] == scope
}

// pathToSecretName extracts the secret name from a path for Infisical lookup.
// e.g. "/linear/api_token" -> "LINEAR_API_TOKEN" (Infisical convention).
func pathToSecretName(path string) string {
	path = strings.TrimPrefix(path, "/")
	path = strings.ReplaceAll(path, "/", "_")
	return strings.ToUpper(path)
}

// buildInfisicalClient constructs a secrets.Client from config + env.
// Env vars win over config file so tests can inject a mock server.
func buildInfisicalClient(cfg *config.Config) *secrets.Client {
	baseURL := os.Getenv("INFISICAL_BASE_URL")
	if baseURL == "" {
		baseURL = cfg.Infisical.BaseURL
	}
	if baseURL == "" {
		baseURL = "https://app.infisical.com"
	}

	clientID := os.Getenv("INFISICAL_CLIENT_ID")
	clientSecret := os.Getenv("INFISICAL_CLIENT_SECRET")

	return secrets.NewClient(baseURL, clientID, clientSecret)
}

// openScopeStore creates a scopestore.Store connected to MongoDB.
func openScopeStore(ctx context.Context) (*scopestore.Store, error) {
	client, _, err := auth.MongoClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("mongo connect: %w", err)
	}
	return scopestore.NewStore(client)
}
