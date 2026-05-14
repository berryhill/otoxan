// cmd_init.go — otoxan init subcommand
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/silas/otoxan/internal/install"
	"github.com/silas/otoxan/internal/version"
	"github.com/spf13/cobra"
)

func newInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Bootstrap the otoxan home directory",
		Long: `init creates the canonical ~/.otoxan/ directory layout:
  bin/  config.yaml  version  logs/  cache/

If the directory already exists, init is idempotent and prints "already initialized".
No state files are written — runtime state lives in MongoDB.`,
		RunE: runInit,
	}
	cmd.Flags().Bool("force", false, "re-initialize even if already present (preserves config.yaml)")
	return cmd
}

func runInit(cmd *cobra.Command, args []string) error {
	home := flagHome
	if home == "" {
		if v := os.Getenv("OTOXAN_HOME"); v != "" {
			home = v
		} else {
			home = install.Home()
		}
	}

	force, _ := cmd.Flags().GetBool("force")

	// Detect whether the home directory already looks initialized.
	versionFile := filepath.Join(home, "version")
	alreadyInit := false
	if _, err := os.Stat(versionFile); err == nil {
		alreadyInit = true
	}

	if err := install.Init(home, force, version.Short()); err != nil {
		return fmt.Errorf("init failed: %w", err)
	}

	if alreadyInit && !force {
		fmt.Println("already initialized")
	} else {
		fmt.Printf("Initialized otoxan at %s\n", home)
	}
	return nil
}
