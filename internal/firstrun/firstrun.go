// Package firstrun detects whether otoxan has been run before.
//
// It uses a sentinel file inside the otoxan home directory.  If the sentinel
// is missing, the binary is considered to be on its first run.  After the
// onboarding flow finishes the sentinel is written so that subsequent invocations
// see IsFirstRun == false.
//
// The package is intentionally thin and free of external dependencies so that
// any other package (including the cobra command tree) can import it without
// creating an import cycle.
package firstrun

import (
	"fmt"
	"os"
	"path/filepath"
)

// SentinelFileName is the base name of the first-run marker.
const SentinelFileName = ".onboarded"

// IsFirstRun reports whether the otoxan home directory lacks the onboarding
// completion sentinel.  If home is empty the function returns an error.
func IsFirstRun(home string) (bool, error) {
	if home == "" {
		return false, fmt.Errorf("firstrun: home directory not provided")
	}
	_, err := os.Stat(filepath.Join(home, SentinelFileName))
	if os.IsNotExist(err) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("firstrun: check sentinel: %w", err)
	}
	return false, nil
}

// MarkOnboardingComplete writes the sentinel file into the otoxan home
// directory, creating the directory if necessary.  Subsequent calls to
// IsFirstRun will return false.
func MarkOnboardingComplete(home string) error {
	if home == "" {
		return fmt.Errorf("firstrun: home directory not provided")
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		return fmt.Errorf("firstrun: create home: %w", err)
	}
	path := filepath.Join(home, SentinelFileName)
	if err := os.WriteFile(path, []byte("1\n"), 0o644); err != nil {
		return fmt.Errorf("firstrun: write sentinel: %w", err)
	}
	return nil
}
