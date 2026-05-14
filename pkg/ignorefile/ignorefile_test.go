package ignorefile

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse_emptyInput(t *testing.T) {
	gates, err := Parse(strings.NewReader(""))
	require.NoError(t, err)
	assert.Empty(t, gates.ReadGates)
	assert.Empty(t, gates.WriteGates)
}

func TestParse_commentsAndBlanks(t *testing.T) {
	input := `# This is a comment

# Another comment
   
`
	gates, err := Parse(strings.NewReader(input))
	require.NoError(t, err)
	assert.Empty(t, gates.ReadGates)
	assert.Empty(t, gates.WriteGates)
}

func TestParse_defaultGate(t *testing.T) {
	input := `secrets/
.env
`
	gates, err := Parse(strings.NewReader(input))
	require.NoError(t, err)

	// Default gate is rw, so both read and write should have entries.
	assert.Len(t, gates.ReadGates, 2)
	assert.Len(t, gates.WriteGates, 2)

	assert.Equal(t, "secrets", gates.ReadGates[0].Pattern)
	assert.Equal(t, GateReadWrite, gates.ReadGates[0].Gate)
	assert.Equal(t, 1, gates.ReadGates[0].Line)

	assert.Equal(t, ".env", gates.ReadGates[1].Pattern)
	assert.Equal(t, GateReadWrite, gates.ReadGates[1].Gate)
}

func TestParse_readGate(t *testing.T) {
	input := `[read] .env
[read] secrets/key.pem
`
	gates, err := Parse(strings.NewReader(input))
	require.NoError(t, err)

	assert.Len(t, gates.ReadGates, 2)
	assert.Empty(t, gates.WriteGates)

	assert.Equal(t, ".env", gates.ReadGates[0].Pattern)
	assert.Equal(t, GateRead, gates.ReadGates[0].Gate)
	assert.Equal(t, "secrets/key.pem", gates.ReadGates[1].Pattern)
}

func TestParse_writeGate(t *testing.T) {
	input := `[write] prisma/migrations/
[write] package-lock.json
`
	gates, err := Parse(strings.NewReader(input))
	require.NoError(t, err)

	assert.Empty(t, gates.ReadGates)
	assert.Len(t, gates.WriteGates, 2)

	assert.Equal(t, "prisma/migrations", gates.WriteGates[0].Pattern)
	assert.Equal(t, GateWrite, gates.WriteGates[0].Gate)
	assert.Equal(t, "package-lock.json", gates.WriteGates[1].Pattern)
}

func TestParse_rwGate(t *testing.T) {
	input := `[rw] production.yml
`
	gates, err := Parse(strings.NewReader(input))
	require.NoError(t, err)

	assert.Len(t, gates.ReadGates, 1)
	assert.Len(t, gates.WriteGates, 1)

	assert.Equal(t, "production.yml", gates.ReadGates[0].Pattern)
	assert.Equal(t, GateReadWrite, gates.ReadGates[0].Gate)
}

func TestParse_mixedGates(t *testing.T) {
	input := `# Gate configuration
secrets/
[read] .env
[write] prisma/migrations/
[write] package-lock.json
[rw] production.yml
`
	gates, err := Parse(strings.NewReader(input))
	require.NoError(t, err)

	// secrets/ + .env + production.yml = 3 read gates
	assert.Len(t, gates.ReadGates, 3)
	// secrets/ + prisma/migrations/ + package-lock.json + production.yml = 4 write gates
	assert.Len(t, gates.WriteGates, 4)
}

func TestParse_whitespaceInGateTag(t *testing.T) {
	input := `[ read ] .env.local
[	write	]	db.sqlite
`
	gates, err := Parse(strings.NewReader(input))
	require.NoError(t, err)

	assert.Len(t, gates.ReadGates, 1)
	assert.Equal(t, ".env.local", gates.ReadGates[0].Pattern)

	assert.Len(t, gates.WriteGates, 1)
	assert.Equal(t, "db.sqlite", gates.WriteGates[0].Pattern)
}

func TestParse_errorUnclosedTag(t *testing.T) {
	input := `[read .env
`
	_, err := Parse(strings.NewReader(input))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unclosed gate tag")
}

func TestParse_errorEmptyPattern(t *testing.T) {
	input := `[read]
`
	_, err := Parse(strings.NewReader(input))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no pattern")
}

func TestParse_errorUnknownGateTag(t *testing.T) {
	input := `[execute] script.sh
`
	_, err := Parse(strings.NewReader(input))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown gate tag")
}

func TestMatchRead(t *testing.T) {
	input := `secrets/
[read] .env
[write] prisma/migrations/
`
	gates, err := Parse(strings.NewReader(input))
	require.NoError(t, err)

	tests := []struct {
		path     string
		expected bool
	}{
		{"secrets", true},
		{".env", true},
		{"prisma/migrations", false}, // write-only gate
		{"src/main.go", false},
		{"config.json", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			hit, err := gates.MatchRead(tt.path)
			assert.NoError(t, err)
			assert.Equal(t, tt.expected, hit, "MatchRead(%q)", tt.path)
		})
	}
}

func TestMatchWrite(t *testing.T) {
	input := `secrets/
[read] .env
[write] prisma/migrations/
`
	gates, err := Parse(strings.NewReader(input))
	require.NoError(t, err)

	tests := []struct {
		path     string
		expected bool
	}{
		{"secrets", true},           // rw gate
		{".env", false},             // read-only gate
		{"prisma/migrations", true}, // write-only gate
		{"src/main.go", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			hit, err := gates.MatchWrite(tt.path)
			assert.NoError(t, err)
			assert.Equal(t, tt.expected, hit, "MatchWrite(%q)", tt.path)
		})
	}
}

func TestMatchAny(t *testing.T) {
	input := `secrets/
[read] .env
[write] prisma/migrations/
`
	gates, err := Parse(strings.NewReader(input))
	require.NoError(t, err)

	tests := []struct {
		path     string
		expected bool
	}{
		{"secrets", true},
		{".env", true},
		{"prisma/migrations", true},
		{"src/main.go", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			hit, err := gates.MatchAny(tt.path)
			assert.NoError(t, err)
			assert.Equal(t, tt.expected, hit, "MatchAny(%q)", tt.path)
		})
	}
}

func TestMatch_globPatterns(t *testing.T) {
	input := `[read] *.pem
[write] *.lock
[rw] secret/*
`
	gates, err := Parse(strings.NewReader(input))
	require.NoError(t, err)

	hit, err := gates.MatchRead("server.pem")
	assert.NoError(t, err)
	assert.True(t, hit)

	hit, err = gates.MatchRead("cert.pem")
	assert.NoError(t, err)
	assert.True(t, hit)

	hit, err = gates.MatchRead("server.key")
	assert.NoError(t, err)
	assert.False(t, hit)

	hit, err = gates.MatchWrite("package.lock")
	assert.NoError(t, err)
	assert.True(t, hit)

	hit, err = gates.MatchWrite("yarn.lock")
	assert.NoError(t, err)
	assert.True(t, hit)

	// secret/* matches secret/foo but not secret/bar/baz (filepath.Match limitation)
	hit, err = gates.MatchRead("secret/db-password")
	assert.NoError(t, err)
	assert.True(t, hit)
}

func TestGateType_String(t *testing.T) {
	assert.Equal(t, "read", GateRead.String())
	assert.Equal(t, "write", GateWrite.String())
	assert.Equal(t, "rw", GateReadWrite.String())
}

func TestParse_pathCleaning(t *testing.T) {
	input := `./secrets/
./nested/../config
trailing/
`
	gates, err := Parse(strings.NewReader(input))
	require.NoError(t, err)

	// All paths should be cleaned by filepath.Clean.
	assert.Equal(t, "secrets", gates.ReadGates[0].Pattern)
	assert.Equal(t, "config", gates.ReadGates[1].Pattern)
	assert.Equal(t, "trailing", gates.ReadGates[2].Pattern)
}
