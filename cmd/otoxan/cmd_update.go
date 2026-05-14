// cmd_update.go — otoxan update subcommand
package main

import (
	"fmt"
	"os/exec"

	"github.com/silas/otoxan/internal/install"
	"github.com/silas/otoxan/internal/version"
	"github.com/spf13/cobra"
)

func newUpdateCmd() *cobra.Command {
	var (
		flagCheck   bool
		flagDryRun  bool
	)

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Self-update the otoxan binary",
		Long: `update fetches the latest otoxan release from GitHub and atomically
replaces the current binary. A backup is kept at .otoxan.prev for rollback.

Flags:
  --check    Print the latest version without downloading.
  --dry-run  Exercise the full flow (fetch metadata, resolve asset, download)
             but skip the final binary rename. The current binary is untouched.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUpdate(flagCheck, flagDryRun)
		},
	}

	cmd.Flags().BoolVar(&flagCheck, "check", false, "print latest version without downloading")
	cmd.Flags().BoolVar(&flagDryRun, "dry-run", false, "exercise full flow except final rename")

	return cmd
}

// updateClientOverride is set by tests to bypass the real GitHub client.
var updateClientOverride *install.GitHubClient

func runUpdate(check, dryRun bool) error {
	client := updateClientOverride
	if client == nil {
		client = install.NewGitHubClient("silas", "otoxan")
	}

	// 1. Fetch latest release metadata.
	rel, err := client.LatestRelease()
	if err != nil {
		return fmt.Errorf("fetch latest release: %w", err)
	}
	latest := rel.Version()
	current := version.Short()

	// --check: print and exit.
	if check {
		fmt.Printf("latest: %s\ncurrent: %s\n", latest, current)
		if latest == current {
			fmt.Println("already up to date")
		}
		return nil
	}

	// No-op when already on latest.
	if latest == current {
		fmt.Println("already up to date")
		return nil
	}

	fmt.Printf("updating: %s -> %s\n", current, latest)

	// --dry-run: exercise everything except the swap.
	if dryRun {
		// Resolve asset.
		assetURL, err := rel.AssetForCurrentPlatform()
		if err != nil {
			return fmt.Errorf("find asset: %w", err)
		}
		fmt.Printf("asset: %s\n", assetURL)

		// Download to verify the asset is reachable.
		resp, err := client.HTTPClient.Get(assetURL)
		if err != nil {
			return fmt.Errorf("download asset: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return fmt.Errorf("download asset: HTTP %d", resp.StatusCode)
		}
		fmt.Printf("download: HTTP %d (size %d bytes)\n", resp.StatusCode, resp.ContentLength)
		fmt.Println("dry-run complete — binary unchanged")
		return nil
	}

	// Production update path.
	smoke := func(path string) error {
		out, err := exec.Command(path, "--version").CombinedOutput()
		if err != nil {
			return fmt.Errorf("smoke test failed: %w (output: %s)", err, string(out))
		}
		return nil
	}

	if err := install.Update(current, client, smoke); err != nil {
		return fmt.Errorf("update failed: %w", err)
	}

	fmt.Println("update complete — restart otoxan to use the new binary")
	return nil
}
