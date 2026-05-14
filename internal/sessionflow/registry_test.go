package sessionflow

import (
	"context"
	"strings"
	"testing"
)

// stubFlow is a minimal SessionFlow implementation for testing.
type stubFlow struct{}

func (stubFlow) Route(_ context.Context, _ RouteInput) (RouteOutput, error) {
	return RouteOutput{Action: "model_complete"}, nil
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := NewRegistry()
	f1 := stubFlow{}
	f2 := stubFlow{}

	r.Register("default", f1)
	r.Register("onboarding", f2)

	got1, err := r.Get("default")
	if err != nil {
		t.Fatalf("Get(default) error: %v", err)
	}
	if got1 != f1 {
		t.Error("Get(default) returned wrong flow instance")
	}

	got2, err := r.Get("onboarding")
	if err != nil {
		t.Fatalf("Get(onboarding) error: %v", err)
	}
	if got2 != f2 {
		t.Error("Get(onboarding) returned wrong flow instance")
	}
}

func TestRegistry_ListSorted(t *testing.T) {
	r := NewRegistry()
	r.Register("zebra", stubFlow{})
	r.Register("alpha", stubFlow{})
	r.Register("beta", stubFlow{})

	ids := r.List()
	want := []string{"alpha", "beta", "zebra"}
	if len(ids) != len(want) {
		t.Fatalf("List() returned %d ids, want %d", len(ids), len(want))
	}
	for i, id := range ids {
		if id != want[i] {
			t.Errorf("List()[%d] = %q, want %q", i, id, want[i])
		}
	}
}

func TestRegistry_GetUnknown(t *testing.T) {
	r := NewRegistry()
	_, err := r.Get("nonexistent")
	if err == nil {
		t.Fatal("Get(nonexistent) expected error, got nil")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("error message should contain id %q, got: %v", "nonexistent", err)
	}
}

func TestRegistry(t *testing.T) {
	t.Run("RegisterAndGet", func(t *testing.T) { TestRegistry_RegisterAndGet(t) })
	t.Run("ListSorted", func(t *testing.T) { TestRegistry_ListSorted(t) })
	t.Run("GetUnknown", func(t *testing.T) { TestRegistry_GetUnknown(t) })
}
