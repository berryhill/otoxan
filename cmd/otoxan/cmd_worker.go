// cmd_worker.go — otoxan worker subcommand
package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

func newWorkerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "worker",
		Short: "Manage workers",
		Long:  "Run or manage otoxan worker processes.",
	}
	cmd.AddCommand(
		newWorkerRunCmd(),
	)
	return cmd
}

func newWorkerRunCmd() *cobra.Command {
	var (
		taskID  string
		prompt  string
		markerDir string
	)
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run the otoxan worker for a task",
		RunE: func(cmd *cobra.Command, args []string) error {
			bin, err := exec.LookPath("otoxan-worker")
			if err != nil {
				return fmt.Errorf("otoxan-worker not found in PATH: %w", err)
			}
			wArgs := []string{"--task-id", taskID}
			if prompt != "" {
				wArgs = append(wArgs, "--prompt", prompt)
			}
			if markerDir != "" {
				wArgs = append(wArgs, "--marker-dir", markerDir)
			}
			c := exec.Command(bin, wArgs...)
			c.Stdin = os.Stdin
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			return c.Run()
		},
	}
	cmd.Flags().StringVar(&taskID, "task-id", "", "task identifier (required)")
	cmd.Flags().StringVar(&prompt, "prompt", "", "task prompt")
	cmd.Flags().StringVar(&markerDir, "marker-dir", "", "completion marker directory")
	_ = cmd.MarkFlagRequired("task-id")
	return cmd
}
