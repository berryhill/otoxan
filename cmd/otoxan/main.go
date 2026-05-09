// Package main is the otoxan CLI — single-entry dispatch to all subcommands.
package main

import (
	"os"
	"path/filepath"

	"github.com/silas/otoxan/internal/config"
	"github.com/spf13/cobra"
)

const version = "0.1.0"

var (
	// global flags
	flagHome   string
	flagConfig string
	flagAgent  string
)

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
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
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
		newTaskCmd(),
		newPlanCmd(),
		newTeamCmd(),
		newFlowCmd(),
		newMemoryCmd(),
		newDispatchCmd(),
		newWorkerCmd(),
		newMCPCmd(),
		newServeCmd(),
		newVersionCmd(),
		newCompanionCmd(),
	)

	return cmd
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
