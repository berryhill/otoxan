//go:build xander

// Package main is the Xander admin daemon — otoxan's primary system-administrating agent.
//
// Xander is the only binary that links the admin Infisical client (DS-4 in the
// credential-hierarchy). It is gated behind the "xander" build tag so that
// non-admin builds of otoxan cannot link this path even by accident.
//
// Build: go build -tags=xander -o bin/xander ./cmd/xander/
package main

import (
	"fmt"
	"os"

	"github.com/silas/otoxan/internal/version"
	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{
		Use:   "xander",
		Short: "Xander — otoxan admin daemon",
		Long: `Xander is otoxan's primary, system-administrating agent.

It mediates secret access for all other agents, manages agent lifecycle,
and is the single point of escalation for the platform.`,
		Version:      version.String(),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("use 'xander serve' to start the daemon, or 'xander --help' for commands")
		},
	}

	root.AddCommand(newServeCmd())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func newServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Start the Xander IPC server (Unix socket at ~/.otoxan/run/xander.sock)",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Placeholder: IPC server will be implemented in a follow-up task.
			return fmt.Errorf("serve not yet implemented")
		},
	}
}
