// Package main is the otoxan CLI — single-entry dispatch to all subcommands.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/silas/otoxan/internal/config"
	"github.com/silas/otoxan/internal/dispatch"
	"github.com/silas/otoxan/internal/firstrun"
	"github.com/silas/otoxan/internal/runtime"
	versionpkg "github.com/silas/otoxan/internal/version"
	"github.com/spf13/cobra"
	_ "modernc.org/sqlite"
)

var (
	// global flags
	flagHome   string
	flagConfig string
	flagAgent  string
)

// processMode is the runtime mode detected at startup, stashed globally so
// other code can query without re-detecting.  Written once in main() before
// any subcommand runs.
var processMode runtime.Mode

func main() {
	root := newRootCmd()
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "otoxan",
		Short: "otoxan — AI agent operations CLI",
		Long: `otoxan is the single-entry CLI for managing tasks, plans, teams,
flows, memory, dispatch, workers, and MCP servers.`,
		Version:     versionpkg.Short(),
		Args:        cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDefaultDispatch(cmd.Context())
		},
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Detect and validate runtime mode early, before any subcommand runs.
			mode := runtime.DetectMode()
			processMode = mode

			// Log per DS-6 / DS-7 telemetry hook.
			fmt.Fprintf(os.Stderr, "[entry] mode=%s\n", mode)

			// Gateway mode is not implemented in v1 — fail fast with a clear message.
			if mode == runtime.ModeGateway {
				fmt.Fprintf(os.Stderr, "WARN: gateway mode not implemented in v1; unset OTOXAN_GATEWAY_URL to run locally\n")
				return fmt.Errorf("gateway mode is not implemented in v1")
			}

			// Resolve home directory for config loading.
			if flagHome == "" {
				flagHome = resolveHome()
			}
			return nil
		},
	}

	cmd.PersistentFlags().StringVar(&flagHome, "home", "", "otoxan home directory (default: $XDG_DATA_HOME/otoxan or ~/.local/share/otoxan)")
	cmd.PersistentFlags().StringVar(&flagConfig, "config", "", "path to config.yaml (default: <home>/config.yaml)")
	cmd.PersistentFlags().StringVar(&flagAgent, "agent", "", "default agent id (overrides config)")

	cmd.AddCommand(
		newInitCmd(),
		newUpdateCmd(),
		newDBCmd(),
		newTaskCmd(),
		newPlanCmd(),
		newTeamCmd(),
		newFlowCmd(),
		newMemoryCmd(),
		newRecallCmd(),
		newDispatchCmd(),
		newWorkerCmd(),
		newMCPCmd(),
		newServeCmd(),
		newVersionCmd(),
		newCompanionCmd(),
		newSecretsCmd(),
		newXanderCmd(),
		newIdentityCmd(),
	)

	return cmd
}

func runDefaultDispatch(ctx context.Context) error {
	first, err := firstrun.IsFirstRun(flagHome)
	if err != nil {
		return fmt.Errorf("firstrun check: %w", err)
	}

	flowID := "default"
	if first {
		flowID = "onboarding"
	}

	dbPath := filepath.Join(flagHome, "state.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return fmt.Errorf("open state.db: %w", err)
	}
	defer db.Close()

	agent := flagAgent
	if agent == "" {
		cfg, err := loadConfig()
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}
		agent = cfg.DefaultAgent
	}

	opts := dispatch.SessionOptions{
		AgentID:      agent,
		FlowID:       flowID,
		Home:         flagHome,
		DB:           db,
		Orchestrator: dispatch.NewOrchestrator(dispatch.OrchestratorConfig{}),
	}
	return dispatch.OpenInteractiveSession(ctx, opts)
}

// resolveHome returns the otoxan home directory.
// Priority: $OTOXAN_HOME > $XDG_DATA_HOME/otoxan > ~/.local/share/otoxan
func resolveHome() string {
	if v := os.Getenv("OTOXAN_HOME"); v != "" {
		return v
	}
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "otoxan")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "otoxan")
}

// loadConfig loads the otoxan config from the resolved home directory.
func loadConfig() (*config.Config, error) {
	if flagConfig != "" {
		// If a specific config path is given, load from its directory.
		return config.Load(filepath.Dir(flagConfig))
	}
	return config.Load(flagHome)
}
