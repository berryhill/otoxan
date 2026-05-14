// cmd_version.go — otoxan version subcommand
package main

import (
	"fmt"

	versionpkg "github.com/silas/otoxan/internal/version"
	"github.com/spf13/cobra"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print otoxan version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("otoxan", versionpkg.Short())
		},
	}
}
