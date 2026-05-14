package main

import (
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCLI_Backfill builds the otoxan-indexer binary and verifies it can be
// invoked with --backfill.  Heavy integration testing of the backfill logic
// itself lives in internal/index/backfill_test.go.
func TestCLI_Backfill(t *testing.T) {
	// 1. Build the binary.
	binPath := "../../otoxan-indexer"
	cmd := exec.Command("go", "build", "-o", binPath, ".")
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "build otoxan-indexer: %s", out)

	// 2. Verify --help shows the --backfill flag.
	helpCmd := exec.Command(binPath, "-help")
	helpOut, err := helpCmd.CombinedOutput()
	require.NoError(t, err, "otoxan-indexer -help failed: %s", helpOut)
	helpStr := string(helpOut)
	assert.Contains(t, helpStr, "-backfill")
	assert.Contains(t, helpStr, "-agent")
	assert.Contains(t, helpStr, "-progress-every")
}
