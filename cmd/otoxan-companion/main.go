// cmd/otoxan-companion/main.go — Chrome native-messaging host daemon
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/silas/otoxan/internal/auth"
	"github.com/silas/otoxan/internal/config"
	"github.com/spf13/cobra"
)

const version = "0.1.0"

var (
	flagHome   string
	flagConfig string
)

func main() {
	root := newRootCmd()
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "otoxan-companion",
		Short: "otoxan companion — Chrome native-messaging host daemon",
		Long: `otoxan-companion is the local trusted bridge between the browser
extension and the otoxan backend. It speaks length-prefixed JSON over stdio
(Chrome native messaging protocol).`,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if flagHome == "" {
				flagHome = resolveHome()
			}
			return nil
		},
	}

	cmd.PersistentFlags().StringVar(&flagHome, "home", "", "otoxan home directory")
	cmd.PersistentFlags().StringVar(&flagConfig, "config", "", "path to config.yaml")

	cmd.AddCommand(
		newVersionCmd(),
		newCheckCmd(),
		newInstallCmd(),
		newUninstallCmd(),
	)

	// When no subcommand is given, run the native-messaging loop.
	cmd.RunE = runNativeHost

	// Support --version as a root flag alias.
	cmd.Flags().Bool("version", false, "print version and exit")
	cmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		if v, _ := cmd.Flags().GetBool("version"); v {
			fmt.Println("otoxan-companion", version)
			os.Exit(0)
		}
		return nil
	}

	return cmd
}

// ------------------------------------------------------------------
// Subcommands
// ------------------------------------------------------------------

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print companion version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("otoxan-companion", version)
		},
	}
}

func newCheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "check",
		Short: "Verify MongoDB connectivity",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			client, dbName, err := auth.MongoClient(ctx)
			if err != nil {
				fmt.Fprintf(os.Stderr, "mongo connect: %v\n", err)
				os.Exit(1)
			}
			defer client.Disconnect(ctx)

			cfg, _ := loadConfig()
			if cfg != nil && cfg.MongoDB != "" {
				dbName = cfg.MongoDB
			}

			db := client.Database(dbName)
			// list_indexes on ownership collection (or any collection to prove read)
			coll := db.Collection("ownership")
			_, err = coll.Indexes().List(ctx)
			if err != nil {
				fmt.Fprintf(os.Stderr, "list indexes: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("OK")
			return nil
		},
	}
}

func newInstallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install-native-host",
		Short: "Install Chrome native-messaging host manifest",
		RunE: func(cmd *cobra.Command, args []string) error {
			extID, _ := cmd.Flags().GetString("extension-id")
			return installManifest(extID)
		},
	}
	cmd.Flags().String("extension-id", "", "Chrome extension ID (e.g. abcdefghijklmnopqrstuvwxyzabcdef)")
	return cmd
}

func newUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall-native-host",
		Short: "Remove Chrome native-messaging host manifest",
		RunE: func(cmd *cobra.Command, args []string) error {
			return uninstallManifest()
		},
	}
}

// ------------------------------------------------------------------
// Native-messaging loop (default mode)
// ------------------------------------------------------------------



func runNativeHost(cmd *cobra.Command, args []string) error {
	return runNativeHostLoop(os.Stdin, os.Stdout)
}

// ------------------------------------------------------------------
// Manifest helpers
// ------------------------------------------------------------------

const manifestName = "com.otoxan.companion.json"

func chromeNativeMessagingDir() string {
	return chromeNativeMessagingDirFunc()
}

// chromeNativeMessagingDirFunc is overridable in tests.
var chromeNativeMessagingDirFunc = defaultChromeNativeMessagingDir

func defaultChromeNativeMessagingDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "google-chrome", "NativeMessagingHosts")
}

func installManifest(extID string) error {
	dir := chromeNativeMessagingDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create manifest dir: %w", err)
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}

	origins := []string{}
	if extID != "" {
		origins = append(origins, "chrome-extension://"+extID+"/")
	}

	manifest := map[string]any{
		"name":            "com.otoxan.companion",
		"description":     "Otoxan Companion Native Host",
		"path":            exe,
		"type":            "stdio",
		"allowed_origins": origins,
	}

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}

	path := filepath.Join(dir, manifestName)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	fmt.Println("Installed", path)
	return nil
}

func uninstallManifest() error {
	path := filepath.Join(chromeNativeMessagingDir(), manifestName)
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			fmt.Println("Manifest already removed")
			return nil
		}
		return err
	}
	fmt.Println("Uninstalled", path)
	return nil
}

// ------------------------------------------------------------------
// Config helpers
// ------------------------------------------------------------------

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

func loadConfig() (*config.Config, error) {
	if flagConfig != "" {
		return config.Load(filepath.Dir(flagConfig))
	}
	return config.Load(flagHome)
}
