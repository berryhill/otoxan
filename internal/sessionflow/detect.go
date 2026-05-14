package sessionflow

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
)

// detected holds all silently-detected context about the user's environment.
// Nothing in this struct is ever asked of the user — it is all inferred
// from the running process, filesystem, or git configuration.
type detected struct {
	CWD                string
	GitRemote          string
	OS                 string
	Shell              string
	Username           string
	Editor             string
	AgentProfileExists bool
}

// detectContext gathers everything we can learn about the user's environment
// without asking a single question.  It is the silent half of onboarding;
// the flow only asks the user to confirm or fill gaps.
func detectContext(home string) detected {
	cwd, _ := os.Getwd()
	return detected{
		CWD:                cwd,
		GitRemote:          readGitRemote(cwd),
		OS:                 runtime.GOOS,
		Shell:              inferShell(),
		Username:           inferUsername(),
		Editor:             inferEditor(),
		AgentProfileExists: checkAgentProfileConfigured(home),
	}
}

// readGitRemote returns the origin remote URL for the repository at cwd,
// or an empty string if cwd is not inside a git repo or has no origin.
func readGitRemote(cwd string) string {
	if cwd == "" {
		return ""
	}
	out, err := exec.Command("git", "-C", cwd, "config", "--get", "remote.origin.url").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// inferShell returns the user's login shell from the SHELL environment variable,
// or the last component of the current process executable name as a fallback.
func inferShell() string {
	if sh := os.Getenv("SHELL"); sh != "" {
		return filepath.Base(sh)
	}
	if exe, err := os.Executable(); err == nil {
		return filepath.Base(exe)
	}
	return ""
}

// inferUsername returns the current user's name.
// Priority: $USER > $USERNAME > os/user.Current() > ""
func inferUsername() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	if u := os.Getenv("USERNAME"); u != "" {
		return u
	}
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return ""
}

// inferEditor returns the user's preferred editor.
// Priority: $VISUAL > $EDITOR > ""
func inferEditor() string {
	if e := os.Getenv("VISUAL"); e != "" {
		return e
	}
	if e := os.Getenv("EDITOR"); e != "" {
		return e
	}
	return ""
}

// checkAgentProfileConfigured reports whether at least one agent profile
// directory exists under <home>/profiles/.
func checkAgentProfileConfigured(home string) bool {
	profilesDir := filepath.Join(home, "profiles")
	entries, err := os.ReadDir(profilesDir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			return true
		}
	}
	return false
}

// summary returns a human-readable paragraph describing what was detected.
// It is used by the onboarding flow to present context to the user for
// confirmation.
func (d detected) summary() string {
	var parts []string
	parts = append(parts, fmt.Sprintf("Working directory: %s", d.CWD))
	if d.GitRemote != "" {
		parts = append(parts, fmt.Sprintf("Git remote: %s", d.GitRemote))
	} else {
		parts = append(parts, "Git remote: none detected")
	}
	parts = append(parts, fmt.Sprintf("OS: %s", d.OS))
	parts = append(parts, fmt.Sprintf("Shell: %s", d.Shell))
	parts = append(parts, fmt.Sprintf("User: %s", d.Username))
	if d.Editor != "" {
		parts = append(parts, fmt.Sprintf("Editor: %s", d.Editor))
	} else {
		parts = append(parts, "Editor: not set ($VISUAL or $EDITOR)")
	}
	if d.AgentProfileExists {
		parts = append(parts, "Agent profile: found")
	} else {
		parts = append(parts, "Agent profile: not yet configured")
	}
	return strings.Join(parts, "\n")
}
