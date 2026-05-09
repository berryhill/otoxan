package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"
)

// devExtensionID is the fixed Chrome extension ID for the unpacked dev build.
// It is derived from the public key in manifest.json and is stable across reloads.
const devExtensionID = "abcdefghijklmnopqrstuvwxyzabcdef"

func newCompanionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "companion",
		Short: "Manage the browser companion daemon",
		Long:  `Build, install, and verify the otoxan-companion native-messaging daemon.`,
	}
	cmd.AddCommand(newCompanionInitCmd())
	return cmd
}

func newCompanionInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Build daemon, install native host, and verify connectivity",
		Long: `init performs the full cold-machine setup:
1. Builds the otoxan-companion binary.
2. Installs the Chrome native-messaging host manifest.
3. Runs 'check' to verify Mongo connectivity.
4. Prints "OK" when everything passes.`,
		RunE: runCompanionInit,
	}
}

func runCompanionInit(cmd *cobra.Command, args []string) error {
	// 0. Verify Go is available.
	if _, err := exec.LookPath("go"); err != nil {
		return fmt.Errorf("go is not installed or not in PATH: %w\nInstall Go 1.22+ and retry", err)
	}

	// 1. Determine install directory for the binary.
	// Use the same home-resolution logic as the root command.
	home := os.Getenv("OTOXAN_HOME")
	if home == "" {
		if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
			home = filepath.Join(xdg, "otoxan")
		} else {
			hdir, _ := os.UserHomeDir()
			home = filepath.Join(hdir, ".local", "share", "otoxan")
		}
	}
	binDir := filepath.Join(home, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return fmt.Errorf("create bin directory: %w", err)
	}
	binPath := filepath.Join(binDir, "otoxan-companion")

	// 2. Resolve the otoxan module root so 'go build' can find the package.
	moduleRoot, err := resolveModuleRoot()
	if err != nil {
		return err
	}

	// 3. Build the daemon.
	fmt.Println("Building otoxan-companion...")
	build := exec.Command("go", "build", "-o", binPath, "./cmd/otoxan-companion")
	build.Dir = moduleRoot
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if err := build.Run(); err != nil {
		return fmt.Errorf("build otoxan-companion: %w", err)
	}
	fmt.Println("Built", binPath)

	// 4. Install the native-messaging host manifest.
	fmt.Println("Installing native-messaging host manifest...")
	install := exec.Command(binPath, "install-native-host", "--extension-id", devExtensionID)
	install.Stdout = os.Stdout
	install.Stderr = os.Stderr
	if err := install.Run(); err != nil {
		return fmt.Errorf("install native host manifest: %w", err)
	}

	// 5. Run check.
	fmt.Println("Running connectivity check...")
	chk := exec.Command(binPath, "check")
	chk.Stdout = os.Stdout
	chk.Stderr = os.Stderr
	if err := chk.Run(); err != nil {
		return fmt.Errorf("connectivity check failed: %w", err)
	}

	fmt.Println("OK")
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Println("  1. Go to chrome://extensions and reload the Otoxan extension.")
	fmt.Println("  2. Open the chat panel (Ctrl+Shift+A) on any tab.")
	fmt.Println("  3. The extension will auto-detect the daemon and route through it.")
	return nil
}

// resolveModuleRoot finds the otoxan Go module root by walking up from the
// current working directory or from the otoxan binary location.
func resolveModuleRoot() (string, error) {
	// Strategy 1: walk up from CWD looking for go.mod.
	cwd, err := os.Getwd()
	if err == nil {
		if root := findGoMod(cwd); root != "" {
			return root, nil
		}
	}

	// Strategy 2: walk up from the otoxan binary path.
	exe, err := os.Executable()
	if err == nil {
		if root := findGoMod(filepath.Dir(exe)); root != "" {
			return root, nil
		}
	}

	// Strategy 3: on Unix, try the well-known repo path.
	if runtime.GOOS != "windows" {
		wellKnown := filepath.Join(os.Getenv("HOME"), "code", "otoxan", "otoxan")
		if _, err := os.Stat(filepath.Join(wellKnown, "go.mod")); err == nil {
			return wellKnown, nil
		}
	}

	return "", fmt.Errorf("cannot find otoxan Go module root (looked for go.mod in CWD, binary dir, and ~/code/otoxan/otoxan)\nRun this command from inside the otoxan repo")
}

// findGoMod walks upward from dir until it finds a directory containing go.mod.
func findGoMod(dir string) string {
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}
