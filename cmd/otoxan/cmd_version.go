// cmd_version.go — otoxan version subcommand
package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print otoxan version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("otoxan", version)
		},
	}
}
