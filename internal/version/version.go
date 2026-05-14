// Package version holds build-time version metadata injected via ldflags.
package version

import "fmt"

// These vars are overridden at link time by the Makefile / CI.
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildTime = "unknown"
)

// String returns a human-friendly version string.
func String() string {
	return fmt.Sprintf("%s (commit %s, built %s)", Version, Commit, BuildTime)
}

// Short returns just the version tag.
func Short() string {
	return Version
}
