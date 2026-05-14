// Package install defines otoxan installation paths and layout.
//
// The install layer is stateless: it records the binary location, version,
// and canonical directory layout. All runtime state (agents, plans, tasks,
// memory) lives in MongoDB, not on disk.
package install

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"
)

// Home returns the otoxan home directory.
//
// Priority:
//  1. $OTOXAN_HOME — if set and non-empty, used verbatim.
//  2. $HOME/.otoxan — the canonical default.
//
// This mirrors the ~/.hermes precedent deliberately to keep mental model
// overlap during the cutover. XDG migration is a future concern.
func Home() string {
	if v := os.Getenv("OTOXAN_HOME"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".otoxan")
}

// Layout returns the canonical subdirectories and files within the otoxan home.
type Layout struct {
	Home    string
	Bin     string
	Config  string
	Version string
	Logs    string
	Cache   string
	Binary  string // path to the otoxan binary itself
}

// DirLayout returns the canonical layout for the given home directory.
func DirLayout(home string) Layout {
	return Layout{
		Home:    home,
		Bin:     filepath.Join(home, "bin"),
		Config:  filepath.Join(home, "config.yaml"),
		Version: filepath.Join(home, "version"),
		Logs:    filepath.Join(home, "logs"),
		Cache:   filepath.Join(home, "cache"),
		Binary:  filepath.Join(home, "bin", "otoxan"),
	}
}

// Init creates the canonical ~/.otoxan/ directory layout.
//
// If the directory already exists and force is false, Init is idempotent:
// it ensures all subdirectories exist and writes the version file, but
// does not overwrite an existing config.yaml.
//
// If force is true, it recreates the layout and preserves any existing
// config.yaml (merging / keeping it intact).
//
// The version file records the currently running binary's version so that
// `otoxan update` can detect "already on latest" and support can read the
// installed version without executing the binary.
//
// No state files are written — this is install layout only.
func Init(home string, force bool, version string) error {
	lay := DirLayout(home)

	// Create subdirectories.
	dirs := []string{lay.Home, lay.Bin, lay.Logs, lay.Cache}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("create directory %s: %w", d, err)
		}
	}

	// Write version file (always, so update can detect latest).
	if err := os.WriteFile(lay.Version, []byte(strings.TrimSpace(version)+"\n"), 0o644); err != nil {
		return fmt.Errorf("write version file: %w", err)
	}

	// If force is true and config exists, preserve it.
	configExists := false
	if _, err := os.Stat(lay.Config); err == nil {
		configExists = true
	}

	// Only write a stub config.yaml if it doesn't exist yet.
	if !configExists {
		stub := "# otoxan configuration\n# See docs for full reference.\n"
		if err := os.WriteFile(lay.Config, []byte(stub), 0o600); err != nil {
			return fmt.Errorf("write config stub: %w", err)
		}
	}

	// If force is true, we still preserve config — nothing else to do.
	_ = force

	return nil
}

// currentBinaryOverride is set by tests to bypass os.Executable().
var currentBinaryOverride string

// SetCurrentBinaryOverride sets the path returned by CurrentBinary().
// Used by tests in other packages that cannot access unexported variables.
func SetCurrentBinaryOverride(path string) {
	currentBinaryOverride = path
}

// CurrentBinary returns the path to the currently running otoxan binary.
// This is used by `otoxan update` to self-replace.
func CurrentBinary() (string, error) {
	if currentBinaryOverride != "" {
		return currentBinaryOverride, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable: %w", err)
	}
	return filepath.EvalSymlinks(exe)
}

// BinaryPathForPlatform returns the expected binary name for the given OS/arch.
func BinaryPathForPlatform(goos, goarch string) string {
	name := "otoxan"
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// SmokeTest is a function that verifies a binary works after replacement.
// It receives the path to the binary and returns an error if the binary
// is corrupted or otherwise unusable.
type SmokeTest func(binaryPath string) error

// Update performs an atomic self-update of the otoxan binary.
//
// Steps:
//  1. Fetch latest release metadata from GitHub.
//  2. Compare version to current; if already latest, return (no-op).
//  3. Download the platform-matching asset.
//  4. Write to a temp file in the same directory as the current binary.
//  5. Make the temp file executable.
//  6. Run the smoke test on the new binary.
//  7. If smoke test passes: back up current binary to .prev, atomically swap.
//  8. If smoke test fails: clean up temp file, leave current binary untouched.
//
// The smokeTest parameter allows injection of a test verifier. In production,
// callers pass a function that runs the binary with --version.
func Update(currentVersion string, client *GitHubClient, smokeTest SmokeTest) error {
	// 1. Fetch latest release.
	rel, err := client.LatestRelease()
	if err != nil {
		return fmt.Errorf("fetch latest release: %w", err)
	}
	latest := rel.Version()

	// 2. No-op if already on latest.
	if latest == currentVersion {
		return nil
	}

	// 3. Find asset for current platform.
	assetURL, err := rel.AssetForCurrentPlatform()
	if err != nil {
		return fmt.Errorf("find asset: %w", err)
	}

	// 4. Download the asset.
	resp, err := client.HTTPClient.Get(assetURL)
	if err != nil {
		return fmt.Errorf("download asset: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download asset: HTTP %d", resp.StatusCode)
	}

	newBinary, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read asset: %w", err)
	}

	// 5. Determine current binary path.
	currentBinary, err := CurrentBinary()
	if err != nil {
		return fmt.Errorf("resolve current binary: %w", err)
	}
	dir := filepath.Dir(currentBinary)

	// 6. Write temp binary.
	tmpPath := filepath.Join(dir, ".otoxan.tmp")
	if err := os.WriteFile(tmpPath, newBinary, 0o755); err != nil {
		return fmt.Errorf("write temp binary: %w", err)
	}

	// Ensure cleanup on any subsequent error.
	cleanup := func() {
		_ = os.Remove(tmpPath)
	}

	// 7. Smoke test the new binary.
	if smokeTest != nil {
		if err := smokeTest(tmpPath); err != nil {
			cleanup()
			return fmt.Errorf("smoke test failed: %w", err)
		}
	}

	// 8. Back up current binary to .prev.
	prevPath := filepath.Join(dir, ".otoxan.prev")
	if err := os.Rename(currentBinary, prevPath); err != nil {
		cleanup()
		return fmt.Errorf("backup current binary: %w", err)
	}

	// 9. Atomically swap temp into place.
	if err := os.Rename(tmpPath, currentBinary); err != nil {
		// Attempt rollback: restore .prev to current binary.
		_ = os.Rename(prevPath, currentBinary)
		cleanup()
		return fmt.Errorf("replace binary: %w", err)
	}

	return nil
}

// Rollback reverts the binary from .prev backup.
// This is used when a post-update smoke test (run externally on the new
// binary after restart) detects a problem.
func Rollback() error {
	currentBinary, err := CurrentBinary()
	if err != nil {
		return fmt.Errorf("resolve current binary: %w", err)
	}
	dir := filepath.Dir(currentBinary)
	prevPath := filepath.Join(dir, ".otoxan.prev")

	if _, err := os.Stat(prevPath); err != nil {
		return fmt.Errorf("no backup found: %w", err)
	}

	// Move current to .bad, restore .prev to current.
	badPath := filepath.Join(dir, ".otoxan.bad")
	_ = os.Remove(badPath) // clean up any stale .bad

	if err := os.Rename(currentBinary, badPath); err != nil {
		return fmt.Errorf("stash current binary: %w", err)
	}
	if err := os.Rename(prevPath, currentBinary); err != nil {
		// Attempt to restore from bad.
		_ = os.Rename(badPath, currentBinary)
		return fmt.Errorf("restore backup: %w", err)
	}

	return nil
}

// ReadVersion reads the version file from the given home directory.
func ReadVersion(home string) (string, error) {
	lay := DirLayout(home)
	data, err := os.ReadFile(lay.Version)
	if err != nil {
		return "", fmt.Errorf("read version file: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// EnsureRuntimeDirs ensures runtime directories (logs, cache) exist.
// Called by long-lived agents and scheduled jobs.
func EnsureRuntimeDirs(home string) error {
	lay := DirLayout(home)
	dirs := []string{lay.Logs, lay.Cache}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("create runtime directory %s: %w", d, err)
		}
	}
	return nil
}

// init ensures we import runtime for platform detection.
var _ = runtime.GOOS

// --- GitHub Releases client ---

// Release represents a single GitHub release.
type Release struct {
	TagName string  `json:"tag_name"`
	Assets  []Asset `json:"assets"`
}

// Asset represents a single release asset.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

// GitHubClient fetches release metadata from the GitHub API.
type GitHubClient struct {
	HTTPClient *http.Client
	BaseURL    string // e.g. "https://api.github.com"
	Owner      string
	Repo       string
}

// NewGitHubClient creates a client for the given owner/repo.
func NewGitHubClient(owner, repo string) *GitHubClient {
	return &GitHubClient{
		HTTPClient: http.DefaultClient,
		BaseURL:    "https://api.github.com",
		Owner:      owner,
		Repo:       repo,
	}
}

// LatestRelease fetches the most recent release.
func (c *GitHubClient) LatestRelease() (*Release, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases/latest", c.BaseURL, c.Owner, c.Repo)
	resp, err := c.HTTPClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetch latest release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github API returned %d", resp.StatusCode)
	}

	var rel Release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("decode release: %w", err)
	}
	return &rel, nil
}

// Version returns the semver string from a tag name, stripping a leading 'v'.
func (r *Release) Version() string {
	v := r.TagName
	if strings.HasPrefix(v, "v") {
		return v[1:]
	}
	return v
}

// AssetForPlatform returns the download URL for the asset matching GOOS/GOARCH.
//
// Naming convention: otoxan_{version}_{goos}_{goarch}.tar.gz
// Example: otoxan_0.3.1_linux_amd64.tar.gz
func (r *Release) AssetForPlatform(goos, goarch string) (string, error) {
	wantSuffix := fmt.Sprintf("_%s_%s.tar.gz", goos, goarch)
	for _, a := range r.Assets {
		if strings.HasSuffix(a.Name, wantSuffix) {
			return a.URL, nil
		}
	}
	return "", fmt.Errorf("no asset found for %s/%s", goos, goarch)
}

// AssetForCurrentPlatform is a convenience wrapper using runtime.GOOS/GOARCH.
func (r *Release) AssetForCurrentPlatform() (string, error) {
	return r.AssetForPlatform(runtime.GOOS, runtime.GOARCH)
}

// --- Config management with preserve-on-update semantics ---

// DefaultConfigYAML is the canonical default config written on fresh init.
const DefaultConfigYAML = `# otoxan configuration
# See docs for full reference.

# Default agent used when none is explicitly specified.
default_agent: default

# MongoDB connection string. If empty, Infisical is consulted.
mongo_uri: ""

# MongoDB database name.
mongo_db: otoxan

# Infisical secret-manager settings.
infisical:
  base_url: https://app.infisical.com
  token: ""
  project_id: ""
  env: dev

# Per-agent overrides.
agents: {}

# Strict mode rejects unknown env-var keys when true.
strict_mode: false
`

// WriteDefaultConfig writes the default config to the given path.
// It is a no-op if the file already exists.
func WriteDefaultConfig(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil // preserve existing
	}
	return os.WriteFile(path, []byte(DefaultConfigYAML), 0o600)
}

// MergeConfig updates an existing config.yaml with new default keys while
// preserving user edits. Any keys present in the new defaults but missing
// from the user's file are appended as commented-out YAML at the end.
//
// The user's existing values, ordering, and comments are left untouched.
func MergeConfig(userPath string, defaults []byte) error {
	userData, err := os.ReadFile(userPath)
	if err != nil {
		return fmt.Errorf("read user config: %w", err)
	}

	// Parse both documents so we can compare keys at the top level.
	var userRoot yaml.Node
	var defaultRoot yaml.Node
	if err := yaml.Unmarshal(userData, &userRoot); err != nil {
		return fmt.Errorf("parse user config: %w", err)
	}
	if err := yaml.Unmarshal(defaults, &defaultRoot); err != nil {
		return fmt.Errorf("parse default config: %w", err)
	}

	// Collect top-level keys present in the user's file.
	userKeys := make(map[string]struct{})
	if userRoot.Kind == yaml.DocumentNode && len(userRoot.Content) > 0 {
		userDoc := userRoot.Content[0]
		if userDoc.Kind == yaml.MappingNode {
			for i := 0; i < len(userDoc.Content); i += 2 {
				keyNode := userDoc.Content[i]
				userKeys[keyNode.Value] = struct{}{}
			}
		}
	}

	// Find top-level keys in defaults that the user does not have.
	var missing []yaml.Node // pairs of key, value
	if defaultRoot.Kind == yaml.DocumentNode && len(defaultRoot.Content) > 0 {
		defaultDoc := defaultRoot.Content[0]
		if defaultDoc.Kind == yaml.MappingNode {
			for i := 0; i < len(defaultDoc.Content); i += 2 {
				keyNode := defaultDoc.Content[i]
				valNode := defaultDoc.Content[i+1]
				if _, ok := userKeys[keyNode.Value]; !ok {
					missing = append(missing, *keyNode, *valNode)
				}
			}
		}
	}

	if len(missing) == 0 {
		return nil // nothing to append
	}

	// Build a YAML snippet of the missing keys, commented out.
	var buf bytes.Buffer
	buf.WriteString("\n# --- New keys added by otoxan update (commented, review and uncomment if needed) ---\n")
	for i := 0; i < len(missing); i += 2 {
		keyNode := missing[i]
		valNode := missing[i+1]
		// Encode just this key-value pair.
		pair := yaml.Node{
			Kind:    yaml.MappingNode,
			Tag:     "!!map",
			Content: []*yaml.Node{&keyNode, &valNode},
		}
		out, err := yaml.Marshal(&pair)
		if err != nil {
			return fmt.Errorf("marshal missing key %s: %w", keyNode.Value, err)
		}
		for _, line := range strings.Split(strings.TrimSuffix(string(out), "\n"), "\n") {
			if line == "" {
				continue
			}
			buf.WriteString("# ")
			buf.WriteString(line)
			buf.WriteByte('\n')
		}
		buf.WriteByte('\n')
	}

	// Append to the user's file.
	f, err := os.OpenFile(userPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open config for append: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("append missing keys: %w", err)
	}
	return nil
}
