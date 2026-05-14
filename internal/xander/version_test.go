//go:build xander

package xander

import (
	"testing"

	"github.com/silas/otoxan/internal/version"
	"github.com/stretchr/testify/assert"
)

func TestVersionMatchesBinary(t *testing.T) {
	assert.Equal(t, version.Short(), Version, "internal/xander.Version must match version.Short()")
}
