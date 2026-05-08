// cmd_init.go — otoxan init subcommand
package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func newInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Bootstrap otoxan configuration interactively",
		Long: `init creates the otoxan home directory and a config.yaml file.
If the directory already exists, it prompts before overwriting.`,
		RunE: runInit,
	}
	return cmd
}

func runInit(cmd *cobra.Command, args []string) error {
	home := flagHome
	if home == "" {
		home = resolveHome()
	}

	// Ensure home directory exists.
	if err := os.MkdirAll(home, 0o755); err != nil {
		return fmt.Errorf("create home directory: %w", err)
	}

	configPath := filepath.Join(home, "config.yaml")

	// Check for existing config.
	if _, err := os.Stat(configPath); err == nil {
		reader := bufio.NewReader(os.Stdin)
		fmt.Printf("Config already exists at %s. Overwrite? [y/N]: ", configPath)
		resp, _ := reader.ReadString('\n')
		resp = strings.TrimSpace(strings.ToLower(resp))
		if resp != "y" && resp != "yes" {
			fmt.Println("init cancelled")
			return nil
		}
	}

	// Prompt for basic configuration values.
	reader := bufio.NewReader(os.Stdin)

	fmt.Print("Mongo URI [mongodb://localhost:27017]: ")
	mongoURI, _ := reader.ReadString('\n')
	mongoURI = strings.TrimSpace(mongoURI)
	if mongoURI == "" {
		mongoURI = "mongodb://localhost:27017"
	}

	fmt.Print("Mongo DB [otoxan]: ")
	mongoDB, _ := reader.ReadString('\n')
	mongoDB = strings.TrimSpace(mongoDB)
	if mongoDB == "" {
		mongoDB = "otoxan"
	}

	fmt.Print("Default Agent [default]: ")
	defaultAgent, _ := reader.ReadString('\n')
	defaultAgent = strings.TrimSpace(defaultAgent)
	if defaultAgent == "" {
		defaultAgent = "default"
	}

	fmt.Print("Infisical Base URL [https://app.infisical.com]: ")
	infisicalURL, _ := reader.ReadString('\n')
	infisicalURL = strings.TrimSpace(infisicalURL)
	if infisicalURL == "" {
		infisicalURL = "https://app.infisical.com"
	}

	fmt.Print("Infisical Project ID (optional): ")
	projectID, _ := reader.ReadString('\n')
	projectID = strings.TrimSpace(projectID)

	fmt.Print("Infisical Env [dev]: ")
	infisicalEnv, _ := reader.ReadString('\n')
	infisicalEnv = strings.TrimSpace(infisicalEnv)
	if infisicalEnv == "" {
		infisicalEnv = "dev"
	}

	cfg := map[string]interface{}{
		"default_agent": defaultAgent,
		"mongo_uri":     mongoURI,
		"mongo_db":      mongoDB,
		"infisical": map[string]interface{}{
			"base_url":   infisicalURL,
			"project_id": projectID,
			"env":        infisicalEnv,
		},
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	fmt.Printf("Initialized otoxan at %s\n", home)
	fmt.Printf("Config written to %s\n", configPath)
	fmt.Println("Set OTOXAN_HOME or use --home to override the home directory.")
	return nil
}
