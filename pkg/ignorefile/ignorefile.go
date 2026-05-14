// Package ignorefile parses .otoxanignore files into read-gate and write-gate
// path-pattern sets. The file controls which paths an agent may or may not
// access during a session.
//
// File format
//
// Lines starting with '#' are comments; blank lines are ignored.
// Each non-comment line is a path pattern with an optional gate prefix:
//
//	[read]  pattern   → read-gate  (agent cannot read)
//	[write] pattern   → write-gate (agent cannot write)
//	[rw]    pattern   → both read-gate and write-gate
//	pattern            → defaults to [rw]
//
// Patterns follow filepath.Match semantics (glob-style). Leading and trailing
// whitespace is trimmed from both the gate tag and the pattern.
package ignorefile

import (
	"bufio"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

// GateType represents the kind of access restriction on a path.
type GateType int

const (
	// GateRead blocks agent read access.
	GateRead GateType = iota
	// GateWrite blocks agent write access.
	GateWrite
	// GateReadWrite blocks both read and write access.
	GateReadWrite
)

func (g GateType) String() string {
	switch g {
	case GateRead:
		return "read"
	case GateWrite:
		return "write"
	case GateReadWrite:
		return "rw"
	default:
		return fmt.Sprintf("unknown(%d)", g)
	}
}

// Entry is a single parsed line from an .otoxanignore file.
type Entry struct {
	Pattern string   // The path pattern (glob).
	Gate    GateType // Which gate this entry applies to.
	Line    int      // 1-based line number in the source file.
}

// Gates holds the two gate sets produced by parsing an .otoxanignore file.
type Gates struct {
	ReadGates  []Entry // Patterns that block read access.
	WriteGates []Entry // Patterns that block write access.
}

// MatchRead reports whether path matches any read-gate pattern.
func (g *Gates) MatchRead(path string) (bool, error) {
	return matchAny(path, g.ReadGates)
}

// MatchWrite reports whether path matches any write-gate pattern.
func (g *Gates) MatchWrite(path string) (bool, error) {
	return matchAny(path, g.WriteGates)
}

// MatchAny reports whether path matches any gate (read or write).
func (g *Gates) MatchAny(path string) (bool, error) {
	hit, err := g.MatchRead(path)
	if err != nil {
		return false, err
	}
	if hit {
		return true, nil
	}
	return g.MatchWrite(path)
}

func matchAny(path string, entries []Entry) (bool, error) {
	clean := filepath.Clean(path)
	for _, e := range entries {
		matched, err := filepath.Match(e.Pattern, clean)
		if err != nil {
			return false, fmt.Errorf("pattern %q (line %d): %w", e.Pattern, e.Line, err)
		}
		if matched {
			return true, nil
		}
	}
	return false, nil
}

// Parse reads an .otoxanignore file from r and returns the gate sets.
// Returns an error for malformed gate tags but not for blank lines or comments.
func Parse(r io.Reader) (*Gates, error) {
	gates := &Gates{}
	scanner := bufio.NewScanner(r)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		// Skip blanks and comments.
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		entry, err := parseLine(line, lineNum)
		if err != nil {
			return nil, err
		}

		switch entry.Gate {
		case GateRead:
			gates.ReadGates = append(gates.ReadGates, entry)
		case GateWrite:
			gates.WriteGates = append(gates.WriteGates, entry)
		case GateReadWrite:
			gates.ReadGates = append(gates.ReadGates, entry)
			gates.WriteGates = append(gates.WriteGates, entry)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading .otoxanignore: %w", err)
	}

	return gates, nil
}

// parseLine parses a single non-blank, non-comment line.
func parseLine(line string, lineNum int) (Entry, error) {
	entry := Entry{Line: lineNum}

	// Check for a gate prefix: [read], [write], or [rw].
	if strings.HasPrefix(line, "[") {
		closeIdx := strings.Index(line, "]")
		if closeIdx == -1 {
			return Entry{}, fmt.Errorf("line %d: unclosed gate tag '['", lineNum)
		}

		tag := strings.TrimSpace(line[1:closeIdx])
		rest := strings.TrimSpace(line[closeIdx+1:])

		if rest == "" {
			return Entry{}, fmt.Errorf("line %d: gate tag [%s] with no pattern", lineNum, tag)
		}

		switch tag {
		case "read":
			entry.Gate = GateRead
		case "write":
			entry.Gate = GateWrite
		case "rw":
			entry.Gate = GateReadWrite
		default:
			return Entry{}, fmt.Errorf("line %d: unknown gate tag [%s] (expected read, write, or rw)", lineNum, tag)
		}

		entry.Pattern = rest
	} else {
		// No gate prefix → default to read+write.
		entry.Gate = GateReadWrite
		entry.Pattern = line
	}

	// Clean the pattern: filepath.Match expects forward slashes.
	entry.Pattern = filepath.Clean(entry.Pattern)

	return entry, nil
}
