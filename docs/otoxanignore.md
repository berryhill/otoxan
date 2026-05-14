# `.otoxanignore` Semantics

> Canonical specification for per-project ignore rules in otoxan.
> Every otoxan subsystem that reads files from a project directory (indexer, codebase onboarding, MCP knowledge tools, companion captures) MUST respect these rules.

---

## Table of Contents

1. [Purpose](#purpose)
2. [File Name and Location](#file-name-and-location)
3. [Syntax](#syntax)
4. [Pattern Semantics](#pattern-semantics)
5. [Negation](#negation)
6. [File Discovery and Merge Order](#file-discovery-and-merge-order)
7. [Relationship to `.gitignore`](#relationship-to-gitignore)
8. [Default Exclusions](#default-exclusions)
9. [Consumer Contract](#consumer-contract)
10. [Go API](#go-api)
11. [CLI Integration](#cli-integration)
12. [Examples](#examples)

---

## Purpose

`.otoxanignore` controls which files and directories otoxan excludes when scanning a project codebase. Without it, otoxan would index everything under the project root -- including build artifacts, vendored dependencies, large binary blobs, and machine-generated files that waste embedding budget and pollute search results.

The semantics follow `.gitignore` syntax exactly so that developers can reuse muscle memory. A project that already has a `.gitignore` needs zero additional configuration to get reasonable behavior; `.otoxanignore` is opt-in for otoxan-specific overrides.

---

## File Name and Location

The file is named `.otoxanignore`.

### Search path

When otoxan needs to scan a directory tree rooted at `projectRoot`, it looks for ignore files in this order:

1. `$OTOXAN_HOME/otoxanignore` — global user-level ignore (applies to all projects).
2. `$projectRoot/.otoxanignore` — project-level ignore.
3. `$projectRoot/.gitignore` — git's ignore file, used as a baseline (see [Relationship to `.gitignore`](#relationship-to-gitignore)).
4. `.otoxanignore` files in subdirectories — scoped to their directory and below (same as `.gitignore` scoping).

All discovered files are merged into a single matcher (see [File Discovery and Merge Order](#file-discovery-and-merge-order)).

### Absence

If no `.otoxanignore` exists at any level, otoxan falls back to `.gitignore` alone, plus the built-in default exclusions (see [Default Exclusions](#default-exclusions)).

---

## Syntax

The syntax is identical to `.gitignore` as specified by `gitignore(5)`. A summary:

| Syntax | Meaning |
|--------|---------|
| `# comment` | Line is ignored. |
| Blank line | Line is ignored. |
| `foo` | Matches any file or directory named `foo` at any depth. |
| `foo/` | Matches only directories named `foo` at any depth. |
| `/foo` | Matches `foo` only at the root of the file where this rule is defined. |
| `/foo/` | Matches directory `foo` only at the root. |
| `foo/bar` | Matches `bar` inside any directory named `foo`. |
| `/foo/bar` | Matches `bar` inside root-level `foo` only. |
| `*.log` | Glob: matches any file ending in `.log`. |
| `foo/**/*.log` | Glob: matches `.log` files at any depth under any `foo/`. |
| `**/foo` | Matches `foo` at any depth. |
| `foo/**` | Matches everything inside any `foo/`. |
| `!pattern` | Negation: un-ignores a previously ignored path (see [Negation](#negation)). |
| `\!pattern` | Literal `!` at start of pattern. |
| `\#pattern` | Literal `#` at start of pattern. |

### Trailing whitespace

Trailing whitespace is NOT trimmed (matching git behavior), unless the trailing spaces are followed by a backslash: `foo\ ` matches `foo ` (trailing space). In practice, otoxan SHOULD warn on trailing whitespace in `.otoxanignore` since it is almost always accidental.

---

## Pattern Semantics

### Matching algorithm

Patterns are evaluated against the path relative to the directory containing the `.otoxanignore` file.

1. Compute `relPath` = path relative to the directory containing the ignore file.
2. If the pattern contains a slash (other than trailing), match against `relPath`.
3. If the pattern has no slash, match against the basename of `relPath`.
4. Directory patterns (ending in `/`) only match directories; non-directory patterns match both files and directories.

### Glob expansion

`*` matches anything except `/`. `**` matches zero or more directories. This is standard `gitignore` glob semantics.

otoxan SHOULD use `go:filepath.Match` extended with `**` support, or `github.com/sabhiram/go-gitignore` for full compatibility.

---

## Negation

A line starting with `!` un-ignores a path that was previously ignored by an earlier rule.

```
# Ignore all .md files
*.md

# But keep README.md
!README.md
```

Negation follows git semantics: a negated pattern cannot un-ignore a file if its parent directory is ignored. To un-ignore a file inside an ignored directory, you must first un-ignore the directory:

```
# Wrong — won't work
build/
!build/important.txt

# Correct
build/
!build/
build/*
!build/important.txt
```

---

## File Discovery and Merge Order

When otoxan scans a project, it builds a composite ignore matcher from multiple sources. The merge order determines precedence:

### Priority (highest to lowest)

1. **Subdirectory `.otoxanignore`** — closest to the file wins. A `.otoxanignore` in `src/` overrides the root `.otoxanignore` for paths under `src/`.
2. **Root `.otoxanignore`** — project-level overrides.
3. **Global `$OTOXAN_HOME/otoxanignore`** — user-level defaults.
4. **`.gitignore`** (root and subdirectory) — baseline. otoxan respects these unless explicitly overridden by a higher-priority `!` negation in `.otoxanignore`.
5. **Built-in defaults** — hardcoded list (see [Default Exclusions](#default-exclusions)).

### Merge algorithm

```
For each path P being considered:
  1. Check built-in defaults. If matched, P is excluded (cannot be overridden).
  2. Walk from root to P's directory. Collect all .gitignore and .otoxanignore files.
  3. Apply rules in order: global otoxanignore → root .otoxanignore → subdirectory .otoxanignore.
  4. Within each file, rules are applied top-to-bottom (later rules override earlier).
  5. .otoxanignore rules take precedence over .gitignore rules at the same directory level.
  6. The final result: included or excluded.
```

### Why this order?

- `.gitignore` is the baseline because it reflects what the developer already excludes from version control -- those files are almost always noise for AI indexing too.
- `.otoxanignore` overrides `.gitignore` because the AI might need to see files that git ignores (e.g., `package-lock.json`, generated docs) or exclude files that git tracks (e.g., large test fixtures).
- Built-in defaults cannot be overridden because they protect against pathological cases (gigabyte binaries, system directories) that would waste resources regardless of user intent.

---

## Relationship to `.gitignore`

otoxan ALWAYS reads `.gitignore` as a baseline. This means:

| Scenario | Behavior |
|----------|----------|
| No `.otoxanignore`, has `.gitignore` | otoxan excludes everything `.gitignore` excludes, plus built-in defaults. |
| No `.otoxanignore`, no `.gitignore` | otoxan excludes only built-in defaults. |
| `.otoxanignore` present | otoxan merges `.gitignore` + `.otoxanignore` with `.otoxanignore` taking precedence. |
| `.otoxanignore` negates a `.gitignore` rule | The negation wins. otoxan includes the file. |
| `.gitignore` excludes `foo/`, `.otoxanignore` says `!foo/` | otoxan includes `foo/`. |

This design ensures zero-config works for most projects (just use `.gitignore`), while giving users escape hatches for AI-specific needs.

---

## Default Exclusions

The following patterns are ALWAYS excluded by otoxan, regardless of `.otoxanignore` or `.gitignore` content. These are hardcoded in the Go matcher and cannot be overridden with `!` negation.

### Directory patterns

```
.git/
.svn/
.hg/
.otoxan/            # otoxan home directory itself
node_modules/
__pycache__/
.venv/
venv/
.env/
vendor/             # Go vendor, Ruby vendor, etc.
.next/
.nuxt/
target/             # Java/Maven/Rust
build/              # Generic build output
dist/               # Distribution output
out/                # IDE/compiler output
.cache/
.parcel-cache/
.turbo/
.gradle/
.idea/
.vscode/
*.egg-info/
```

### File patterns

```
*.o
*.so
*.dylib
*.dll
*.exe
*.a
*.lib
*.pyc
*.pyo
*.class
*.jar
*.war
*.min.js
*.min.css
*.map               # Source maps
*.woff
*.woff2
*.ttf
*.eot
*.ico
*.png               # Use vision analysis, not text indexing
*.jpg
*.jpeg
*.gif
*.bmp
*.svg
*.mp3
*.mp4
*.avi
*.mov
*.mkv
*.webm
*.wav
*.flac
*.zip
*.tar
*.gz
*.bz2
*.xz
*.7z
*.rar
*.iso
*.dmg
*.pkg
*.deb
*.rpm
*.apk
*.wasm
*.pdf               # Binary PDF; text extraction is a separate concern
*.doc
*.docx
*.xls
*.xlsx
*.ppt
*.pptx
*.db
*.sqlite
*.sqlite3
*.lock              # Lock files (package-lock, yarn.lock, etc.)
*.log
```

### Rationale

These defaults protect against:
1. **Large binary files** that waste embedding budget and produce meaningless vectors.
2. **Version control internals** (`.git/`) that are never relevant to AI context.
3. **Dependency directories** (`node_modules/`, `vendor/`) that dwarf project code and are retrievable from package managers.
4. **Build output** that is derivative of source and changes frequently.
5. **IDE configuration** that is machine-specific.

### Overriding defaults

Defaults CANNOT be overridden. If a user truly needs to include one of these (e.g., `vendor/` contains project-specific C code), they should use the `--no-default-ignore` CLI flag, which disables the hardcoded list entirely (at their own risk). The `.otoxanignore` file still applies.

---

## Consumer Contract

Any otoxan subsystem that reads files from a project directory MUST:

1. **Initialize the ignore matcher** before scanning.
2. **Check every path** against the matcher before reading/embedding.
3. **Skip directories** that match a directory pattern (do not recurse into them).
4. **Log skipped paths** at debug level for auditability.

### Affected subsystems

| Subsystem | Package | What it does with ignore |
|-----------|---------|--------------------------|
| Indexer | `internal/index/` | Excludes matched files from embedding pipeline |
| Codebase onboarding | `internal/store/tasks/` (type `codebase_onboard`) | Excludes matched files from initial scan |
| MCP knowledge server | `cmd/otoxan-mcp-knowledge/` | Excludes matched files from `search_codebase` responses |
| Companion captures | `internal/companion/` | Excludes matched paths when capturing page context |
| CLI `otoxan scan` | `cmd/otoxan/` | Respects ignore in `--dry-run` and live mode |

### Error handling

- If `.otoxanignore` exists but is unreadable (permissions), otoxan MUST log a warning and proceed with `.gitignore` + defaults only. It MUST NOT fail the entire scan.
- If `.otoxanignore` contains invalid syntax (unparseable lines), otoxan MUST log a warning per bad line and continue processing valid lines. It MUST NOT fail the entire scan.
- If the global `$OTOXAN_HOME/otoxanignore` is absent, that is not an error -- it is simply skipped.

---

## Go API

### Package location

```
internal/ignore/
    matcher.go          // Matcher type and constructor
    matcher_test.go     // Unit tests
    defaults.go         // Built-in default patterns
    defaults_test.go    // Default pattern tests
```

### Core types

```go
package ignore

// Matcher determines whether a file or directory should be excluded.
type Matcher struct {
    // (unexported fields)
}

// Config controls Matcher construction.
type Config struct {
    // ProjectRoot is the top-level directory being scanned.
    ProjectRoot string

    // GlobalIgnorePath is $OTOXAN_HOME/otoxanignore. Empty = skip.
    GlobalIgnorePath string

    // NoDefaults disables the built-in exclusion list.
    NoDefaults bool
}

// NewMatcher builds a composite matcher from all ignore files found
// within and above ProjectRoot.
//
// It reads (in merge order):
//   1. .gitignore files (root + subdirectories, discovered on demand)
//   2. .otoxanignore files (global, root, subdirectories)
//   3. Built-in defaults (unless NoDefaults is true)
//
// Returns an error only if ProjectRoot does not exist or is not a directory.
func NewMatcher(cfg Config) (*Matcher, error)

// Match reports whether path should be excluded.
// path must be relative to ProjectRoot, using forward slashes.
// isDir indicates whether path refers to a directory.
func (m *Matcher) Match(path string, isDir bool) bool

// MatchAbs is a convenience wrapper that converts an absolute path
// to relative and calls Match.
func (m *Matcher) MatchAbs(absPath string, isDir bool) (bool, error)

// Patterns returns the ordered list of active patterns for debugging.
func (m *Matcher) Patterns() []Pattern

// Pattern represents a single ignore rule.
type Pattern struct {
    Raw      string // Original line from the ignore file
    Negate   bool   // true for ! patterns
    DirOnly  bool   // true for patterns ending in /
    RootOnly bool   // true for patterns starting with /
    Source   string // File the pattern came from (e.g., ".otoxanignore")
}
```

### Usage pattern

```go
matcher, err := ignore.NewMatcher(ignore.Config{
    ProjectRoot:      "/home/user/code/myproject",
    GlobalIgnorePath: os.Getenv("OTOXAN_HOME") + "/otoxanignore",
})
if err != nil {
    return err
}

err = filepath.Walk(projectRoot, func(path string, info fs.FileInfo, err error) error {
    if err != nil {
        return err
    }
    rel, _ := filepath.Rel(projectRoot, path)
    if matcher.Match(rel, info.IsDir()) {
        if info.IsDir() {
            return filepath.SkipDir
        }
        return nil
    }
    // Process the file...
    return nil
})
```

### Recommended dependency

Use `github.com/sabhiram/go-gitignore` (or equivalent) for the core pattern matching engine rather than reimplementing gitignore semantics. otoxan's `Matcher` wraps it to add:

1. Multi-file merging (global + root + subdirectory + .gitignore).
2. Built-in defaults with non-overrideable enforcement.
3. Source tracking for debug/audit output.

---

## CLI Integration

### `otoxan scan`

```bash
# Scan project, respecting all ignore files
otoxan scan /path/to/project

# Dry-run: show what would be included/excluded
otoxan scan --dry-run /path/to/project

# Disable built-in defaults (dangerous)
otoxan scan --no-default-ignore /path/to/project

# Show which ignore files are loaded
otoxan scan --debug /path/to/project
```

### `otoxan ignore`

```bash
# List active patterns and their sources
otoxan ignore list /path/to/project

# Check if a specific path would be ignored
otoxan ignore check /path/to/project/src/main.go

# Generate a starter .otoxanignore
otoxan ignore init /path/to/project
```

### Exit codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | General error |
| 2 | Invalid .otoxanignore syntax (warnings logged, operation continues) |

---

## Examples

### Minimal project

No `.otoxanignore` needed. otoxan reads `.gitignore` and applies defaults.

```
# .gitignore
node_modules/
dist/
*.log
```

Result: `node_modules/`, `dist/`, `*.log`, and all defaults are excluded.

### AI needs gitignored files

```
# .otoxanignore
# Override .gitignore: we want package-lock.json for dependency analysis
!package-lock.json

# Override .gitignore: we want generated API docs
!docs/api/generated/
```

### AI wants to exclude tracked files

```
# .otoxanignore
# Large test fixtures that waste embedding budget
tests/fixtures/
tests/__snapshots__/

# Vendored code we don't need to index
src/vendor/

# Secrets that should never be embedded
*.pem
*.key
.env.local
```

### Subdirectory override

```
# .otoxanignore (root)
# Ignore all markdown
*.md

# src/.otoxanignore
# But keep markdown in src/ for API docs
!*.md
```

Result: `*.md` excluded everywhere except under `src/`.

### Global user defaults

```
# $OTOXAN_HOME/otoxanignore
# Personal preference: always ignore TODO files
TODO.md
TODO.txt
```

---

## Changelog

| Date | Change |
|------|--------|
| 2026-05-13 | Initial specification. |
