// cmd_identity.go — otoxan identity subcommand
package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/silas/otoxan/internal/auth"
	"github.com/silas/otoxan/pkg/stores/identitystore"
	"github.com/spf13/cobra"
)

func newIdentityCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "identity",
		Short: "Manage identity manifests",
		Long:  "Create, list, show, activate, retire, and diff identity manifest versions.",
	}

	cmd.AddCommand(
		newIdentityCreateCmd(),
		newIdentityListCmd(),
		newIdentityShowCmd(),
		newIdentityActivateCmd(),
		newIdentityRetireCmd(),
		newIdentityDiffCmd(),
	)

	return cmd
}

// ------------------------------------------------------------------
// Helpers
// ------------------------------------------------------------------

func openIdentityStore(ctx context.Context) (*identitystore.Store, error) {
	client, _, err := auth.MongoClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("mongo connect: %w", err)
	}
	return identitystore.NewStore(client)
}

func identityID(name, version string) string {
	return fmt.Sprintf("id_%s_%s", name, version)
}

func parseIdentityID(id string) (name, version string, err error) {
	if !strings.HasPrefix(id, "id_") {
		return "", "", fmt.Errorf("invalid identity ID %q: must start with 'id_'", id)
	}
	parts := strings.SplitN(strings.TrimPrefix(id, "id_"), "_", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid identity ID %q: expected id_<name>_<version>", id)
	}
	return parts[0], parts[1], nil
}

// ------------------------------------------------------------------
// identity create
// ------------------------------------------------------------------

func newIdentityCreateCmd() *cobra.Command {
	var (
		manifest    string
		description string
		tags        []string
	)
	cmd := &cobra.Command{
		Use:   "create <name> <version>",
		Short: "Create a new identity version",
		Long:  "Create a new identity manifest version. The name+version must be unique.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			store, err := openIdentityStore(ctx)
			if err != nil {
				return err
			}

			name := args[0]
			version := args[1]

			identity := &identitystore.IdentityManifest{
				Name:        name,
				Version:     version,
				Manifest:    manifest,
				Description: description,
				Tags:        tags,
			}

			res, err := store.Create(ctx, identity)
			if err != nil {
				return fmt.Errorf("create identity: %w", err)
			}

			fmt.Fprintf(os.Stdout, "Created %s (inserted_id=%s)\n", identityID(name, version), res.InsertedID)
			return nil
		},
	}
	cmd.Flags().StringVarP(&manifest, "manifest", "m", "", "identity manifest text (system prompt / persona text)")
	cmd.Flags().StringVarP(&description, "description", "d", "", "human-readable description of this identity version")
	cmd.Flags().StringSliceVar(&tags, "tags", nil, "comma-separated tags")
	return cmd
}

// ------------------------------------------------------------------
// identity list
// ------------------------------------------------------------------

func newIdentityListCmd() *cobra.Command {
	var nameFilter string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List identities",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			store, err := openIdentityStore(ctx)
			if err != nil {
				return err
			}

			opts := identitystore.ListOptions{Name: nameFilter}
			identities, err := store.List(ctx, opts)
			if err != nil {
				return err
			}

			if len(identities) == 0 {
				fmt.Fprintln(os.Stdout, "No identities found.")
				return nil
			}

			tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tNAME\tVERSION\tSTATUS\tDESCRIPTION")
			for _, id := range identities {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
					identityID(id.Name, id.Version),
					id.Name,
					id.Version,
					string(id.Status),
					truncate(id.Description, 40),
				)
			}
			tw.Flush()
			return nil
		},
	}
	cmd.Flags().StringVarP(&nameFilter, "name", "n", "", "filter by identity name")
	return cmd
}

// ------------------------------------------------------------------
// identity show
// ------------------------------------------------------------------

func newIdentityShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <identity-id>",
		Short: "Show an identity version",
		Long:  "Show full details of an identity version. Accepts id_<name>_<version> format.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			store, err := openIdentityStore(ctx)
			if err != nil {
				return err
			}

			name, version, err := parseIdentityID(args[0])
			if err != nil {
				// Fallback: treat arg as name, show active version
				active, err2 := store.GetActive(ctx, args[0])
				if err2 != nil {
					return fmt.Errorf("%w\nalso tried active lookup for %q: %v", err, args[0], err2)
				}
				printJSON(active)
				return nil
			}

			identity, err := store.Get(ctx, name, version)
			if err != nil {
				return err
			}

			printJSON(identity)
			return nil
		},
	}
	return cmd
}

// ------------------------------------------------------------------
// identity activate
// ------------------------------------------------------------------

func newIdentityActivateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "activate <identity-id>",
		Short: "Activate an identity version",
		Long:  "Activate an identity version, deactivating any currently active version for the same name. Accepts id_<name>_<version> format.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			store, err := openIdentityStore(ctx)
			if err != nil {
				return err
			}

			name, version, err := parseIdentityID(args[0])
			if err != nil {
				return err
			}

			if err := store.Activate(ctx, name, version); err != nil {
				return fmt.Errorf("activate: %w", err)
			}

			fmt.Fprintf(os.Stdout, "Activated %s\n", identityID(name, version))
			return nil
		},
	}
	return cmd
}

// ------------------------------------------------------------------
// identity retire
// ------------------------------------------------------------------

func newIdentityRetireCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "retire <identity-id>",
		Short: "Retire an identity version",
		Long:  "Retire an inactive identity version. It can no longer be activated.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			store, err := openIdentityStore(ctx)
			if err != nil {
				return err
			}

			name, version, err := parseIdentityID(args[0])
			if err != nil {
				return err
			}

			if err := store.Retire(ctx, name, version); err != nil {
				return fmt.Errorf("retire: %w", err)
			}

			fmt.Fprintf(os.Stdout, "Retired %s\n", identityID(name, version))
			return nil
		},
	}
	return cmd
}

// ------------------------------------------------------------------
// identity diff
// ------------------------------------------------------------------

func newIdentityDiffCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "diff <identity-id-a> <identity-id-b>",
		Short: "Show word-level diff between two identity versions",
		Long: `Compare two identity manifest versions and show word-level changes.

Shows changes in:
  - system_text (manifest field): word-level additions (+) and removals (-)
  - tool_descriptions delta: changes in provider_profiles entries

Both arguments accept id_<name>_<version> format.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			store, err := openIdentityStore(ctx)
			if err != nil {
				return err
			}

			nameA, verA, err := parseIdentityID(args[0])
			if err != nil {
				return err
			}
			nameB, verB, err := parseIdentityID(args[1])
			if err != nil {
				return err
			}

			manifestA, err := store.Get(ctx, nameA, verA)
			if err != nil {
				return fmt.Errorf("get %s: %w", args[0], err)
			}
			manifestB, err := store.Get(ctx, nameB, verB)
			if err != nil {
				return fmt.Errorf("get %s: %w", args[1], err)
			}

			// --- system_text diff (word-level) ---
			fmt.Fprintln(os.Stdout, "=== system_text (manifest) ===")
			diffLines := wordDiff(manifestA.Manifest, manifestB.Manifest)
			if len(diffLines) == 0 {
				fmt.Fprintln(os.Stdout, "  (no changes)")
			} else {
				for _, line := range diffLines {
					fmt.Fprintf(os.Stdout, "  %s\n", line)
				}
			}

			// --- description diff ---
			if manifestA.Description != manifestB.Description {
				fmt.Fprintln(os.Stdout, "\n=== description ===")
				descDiff := wordDiff(manifestA.Description, manifestB.Description)
				for _, line := range descDiff {
					fmt.Fprintf(os.Stdout, "  %s\n", line)
				}
			}

			// --- tool_descriptions delta (provider_profiles) ---
			fmt.Fprintln(os.Stdout, "\n=== tool_descriptions (provider_profiles) ===")
			profileDiff := providerProfileDiff(manifestA.ProviderProfiles, manifestB.ProviderProfiles)
			if len(profileDiff) == 0 {
				fmt.Fprintln(os.Stdout, "  (no changes)")
			} else {
				for _, line := range profileDiff {
					fmt.Fprintf(os.Stdout, "  %s\n", line)
				}
			}

			// --- metadata changes ---
			fmt.Fprintln(os.Stdout, "\n=== metadata ===")
			metaChanges := metadataDiff(manifestA, manifestB)
			if len(metaChanges) == 0 {
				fmt.Fprintln(os.Stdout, "  (no changes)")
			} else {
				for _, line := range metaChanges {
					fmt.Fprintf(os.Stdout, "  %s\n", line)
				}
			}

			return nil
		},
	}
	return cmd
}

// ------------------------------------------------------------------
// Diff helpers
// ------------------------------------------------------------------

// wordDiff performs a word-level diff between two strings.
// It tokenizes both strings by whitespace and uses a simple LCS-based
// approach to produce word-level additions and removals.
func wordDiff(a, b string) []string {
	wordsA := tokenize(a)
	wordsB := tokenize(b)

	lcs := longestCommonSubsequence(wordsA, wordsB)

	var lines []string
	var changeBuf []string
	changeType := "" // "add" or "remove"

	flush := func() {
		if len(changeBuf) > 0 {
			prefix := "-"
			if changeType == "add" {
				prefix = "+"
			}
			lines = append(lines, fmt.Sprintf("%s %s", prefix, strings.Join(changeBuf, " ")))
			changeBuf = nil
		}
	}

	i, j := 0, 0
	for k := range lcs {
		// Skip words in A before this LCS word
		for i < len(wordsA) && wordsA[i] != lcs[k] {
			if changeType != "remove" {
				flush()
				changeType = "remove"
			}
			changeBuf = append(changeBuf, wordsA[i])
			i++
		}
		// Skip words in B before this LCS word
		for j < len(wordsB) && wordsB[j] != lcs[k] {
			if changeType != "add" {
				flush()
				changeType = "add"
			}
			changeBuf = append(changeBuf, wordsB[j])
			j++
		}
		flush()
		changeType = ""
		// Emit the common word
		lines = append(lines, fmt.Sprintf("  %s", lcs[k]))
		i++
		j++
	}

	// Remaining words in A
	for i < len(wordsA) {
		if changeType != "remove" {
			flush()
			changeType = "remove"
		}
		changeBuf = append(changeBuf, wordsA[i])
		i++
	}
	// Remaining words in B
	for j < len(wordsB) {
		if changeType != "add" {
			flush()
			changeType = "add"
		}
		changeBuf = append(changeBuf, wordsB[j])
		j++
	}
	flush()

	return lines
}

// tokenize splits a string into words (whitespace-separated).
func tokenize(s string) []string {
	return strings.Fields(s)
}

// longestCommonSubsequence returns the LCS of two string slices.
func longestCommonSubsequence(a, b []string) []string {
	m, n := len(a), len(b)
	if m == 0 || n == 0 {
		return nil
	}

	// DP table
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}

	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if a[i-1] == b[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else {
				dp[i][j] = max(dp[i-1][j], dp[i][j-1])
			}
		}
	}

	// Backtrack
	result := make([]string, 0, dp[m][n])
	i, j := m, n
	for i > 0 && j > 0 {
		if a[i-1] == b[j-1] {
			result = append(result, a[i-1])
			i--
			j--
		} else if dp[i-1][j] > dp[i][j-1] {
			i--
		} else {
			j--
		}
	}

	// Reverse
	for l, r := 0, len(result)-1; l < r; l, r = l+1, r-1 {
		result[l], result[r] = result[r], result[l]
	}

	return result
}

// providerProfileDiff compares two provider profile maps and returns
// a human-readable delta.
func providerProfileDiff(a, b map[identitystore.ProviderType]string) []string {
	var lines []string

	// Collect all provider keys
	keys := make(map[identitystore.ProviderType]bool)
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}

	// Sort keys for deterministic output
	sortedKeys := make([]identitystore.ProviderType, 0, len(keys))
	for k := range keys {
		sortedKeys = append(sortedKeys, k)
	}
	sort.Slice(sortedKeys, func(i, j int) bool {
		return string(sortedKeys[i]) < string(sortedKeys[j])
	})

	for _, key := range sortedKeys {
		valA, hasA := a[key]
		valB, hasB := b[key]

		if !hasA && hasB {
			lines = append(lines, fmt.Sprintf("+ %s: (new) %s", key, truncate(valB, 60)))
		} else if hasA && !hasB {
			lines = append(lines, fmt.Sprintf("- %s: (removed)", key))
		} else if valA != valB {
			lines = append(lines, fmt.Sprintf("~ %s:", key))
			wordLines := wordDiff(valA, valB)
			for _, wl := range wordLines {
				lines = append(lines, fmt.Sprintf("    %s", wl))
			}
		}
	}

	return lines
}

// metadataDiff compares non-text metadata fields between two identity manifests.
func metadataDiff(a, b *identitystore.IdentityManifest) []string {
	var lines []string

	if a.Name != b.Name {
		lines = append(lines, fmt.Sprintf("name: %q -> %q", a.Name, b.Name))
	}
	if a.Version != b.Version {
		lines = append(lines, fmt.Sprintf("version: %q -> %q", a.Version, b.Version))
	}
	if a.Status != b.Status {
		lines = append(lines, fmt.Sprintf("status: %q -> %q", a.Status, b.Status))
	}

	// Tags diff
	if !stringSliceEqual(a.Tags, b.Tags) {
		lines = append(lines, fmt.Sprintf("tags: %v -> %v", a.Tags, b.Tags))
	}

	return lines
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
