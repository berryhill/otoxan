package sessionflow

import (
	"os"
	"strings"
	"testing"
)

// TestOnboarding_Redundancy asserts that none of the literal strings
// "your cwd", "git remote", "your OS", "your shell", or "username"
// appear in user-facing prompts in onboarding.go.  These values must
// be detected silently — asking the user for them violates the
// never-redundant contract.
func TestOnboarding_Redundancy(t *testing.T) {
	b, err := os.ReadFile("onboarding.go")
	if err != nil {
		t.Fatalf("read onboarding.go: %v", err)
	}
	src := string(b)

	forbidden := []string{
		`"your cwd"`,
		`"git remote"`,
		`"your OS"`,
		`"your shell"`,
		`"username"`,
	}

	for _, lit := range forbidden {
		if strings.Contains(src, lit) {
			t.Errorf("onboarding.go contains forbidden prompt string %s — detect silently, do not ask", lit)
		}
	}
}
