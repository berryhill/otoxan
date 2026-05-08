// cmd_mcp.go — otoxan mcp subcommand
package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

func newMCPCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run MCP servers",
		Long:  "Start an otoxan MCP server by name (tasks, plans, flows, memory, knowledge).",
	}
	cmd.AddCommand(
		newMCPRunCmd(),
	)
	return cmd
}

func newMCPRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run <name>",
		Short: "Run an MCP server",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			bin := fmt.Sprintf("otoxan-mcp-%s", name)
			path, err := exec.LookPath(bin)
			if err != nil {
				return fmt.Errorf("%s not found in PATH: %w", bin, err)
			}
			c := exec.Command(path)
			c.Stdin = os.Stdin
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			return c.Run()
		},
	}
}
