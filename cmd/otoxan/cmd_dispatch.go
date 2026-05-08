// cmd_dispatch.go — otoxan dispatch subcommand
package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newDispatchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dispatch",
		Short: "Dispatch operations",
		Long:  "Dispatch tasks and manage dispatch sessions.",
	}
	cmd.AddCommand(
		newDispatchRunCmd(),
	)
	return cmd
}

func newDispatchRunCmd() *cobra.Command {
	var agent string
	cmd := &cobra.Command{
		Use:   "run <task-id>",
		Short: "Dispatch a task to an agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			taskID := args[0]
			if agent == "" {
				cfg, err := loadConfig()
				if err != nil {
					return fmt.Errorf("load config: %w", err)
				}
				agent = cfg.DefaultAgent
			}
			fmt.Printf("dispatching task %s to agent %s\n", taskID, agent)
			// Stub: real dispatch would enqueue via TaskQueue and notify worker.
			return nil
		},
	}
	cmd.Flags().StringVar(&agent, "agent", "", "target agent (default from config)")
	return cmd
}
