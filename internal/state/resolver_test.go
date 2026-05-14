package state

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValidateAgentName verifies that ValidateAgentName accepts valid names
// and rejects invalid ones.
func TestValidateAgentName(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{"xander", false},
		{"agent-1", false},
		{"abc123", false},
		{"", true},        // empty
		{"   ", true},     // whitespace-only
		{"Xander", true},  // uppercase
		{"x/y", true},     // slash
		{"x_y", true},     // underscore
		{"x y", true},     // space
		{"x--y", false},   // double hyphen is fine
		{"-leading", false}, // leading hyphen is fine per regex
		{"trailing-", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateAgentName(tt.name)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestAgentDB verifies that AgentDB returns the correct database name for a
// valid agent and rejects invalid names.
func TestAgentDB(t *testing.T) {
	uri := setupMongo(t)

	ResetClient()
	t.Cleanup(ResetClient)

	client, err := OpenClient(uri)
	require.NoError(t, err)
	require.NotNil(t, client)

	// Valid agent name.
	db, err := AgentDB(client, "xander")
	require.NoError(t, err)
	require.NotNil(t, db)
	assert.Equal(t, "otoxan_agent_xander", db.Name())

	// Invalid names should return an error and nil db.
	invalidNames := []string{"", "X-Y", "x/y"}
	for _, name := range invalidNames {
		db, err := AgentDB(client, name)
		assert.Error(t, err)
		assert.Nil(t, db, "expected nil db for invalid name %q", name)
		assert.True(t, strings.Contains(err.Error(), "agent name") || strings.Contains(err.Error(), "invalid agent name"),
			"error should mention name validation for %q: got %v", name, err)
	}
}

// TestGlobalDB verifies that GlobalDB returns the correct database name.
func TestGlobalDB(t *testing.T) {
	uri := setupMongo(t)

	ResetClient()
	t.Cleanup(ResetClient)

	client, err := OpenClient(uri)
	require.NoError(t, err)
	require.NotNil(t, client)

	db := GlobalDB(client)
	require.NotNil(t, db)
	assert.Equal(t, "otoxan_global", db.Name())
}
