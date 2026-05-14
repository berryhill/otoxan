package sessionflow

import (
	"slices"
	"testing"
)

// TestDefaultRegistry_Integration asserts that the package-level DefaultRegistry
// is seeded with both the built-in "default" flow and the real "onboarding" flow
// at init time.  This is the smoke test for the full registry wiring.
func TestDefaultRegistry_Integration(t *testing.T) {
	ids := DefaultRegistry.List()
	if len(ids) != 2 {
		t.Fatalf("expected 2 flows in DefaultRegistry, got %d: %v", len(ids), ids)
	}

	want := []string{"default", "onboarding"}
	for _, id := range want {
		if !slices.Contains(ids, id) {
			t.Errorf("DefaultRegistry.List() missing %q, got %v", id, ids)
		}
	}

	// Verify each flow can be retrieved and returns a non-nil implementation.
	for _, id := range want {
		flow, err := DefaultRegistry.Get(id)
		if err != nil {
			t.Fatalf("DefaultRegistry.Get(%q) error: %v", id, err)
		}
		if flow == nil {
			t.Fatalf("DefaultRegistry.Get(%q) returned nil flow", id)
		}
	}

	// Acceptnace: onboarding must be the real *onboardingFlow, not the stub.
	flow, _ := DefaultRegistry.Get("onboarding")
	if _, ok := flow.(onboardingFlow); !ok {
		t.Fatalf("DefaultRegistry.Get(\"onboarding\") returned %T, want onboardingFlow", flow)
	}
}

// TestLoadFromDir_Stub asserts that LoadFromDir exists and does not panic when
// called with a non-existent directory (the v1 no-op behaviour).
func TestLoadFromDir_Stub(t *testing.T) {
	// Use a temp path that definitely does not exist.
	LoadFromDir("/tmp/nonexistent_otoxan_home_for_test")
}
