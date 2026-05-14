package sessionflow

import (
	"context"
	"testing"
)

func TestDefaultFlow_Route(t *testing.T) {
	f := defaultFlow{}
	in := RouteInput{UserMessage: "hello"}
	out, err := f.Route(context.Background(), in)
	if err != nil {
		t.Fatalf("Route error: %v", err)
	}
	if out.Action != "model_complete" {
		t.Errorf("Action = %q, want %q", out.Action, "model_complete")
	}
	if out.Payload != "hello" {
		t.Errorf("Payload = %q, want %q", out.Payload, "hello")
	}
}

func TestDefaultFlow_Registered(t *testing.T) {
	flow, err := DefaultRegistry.Get("default")
	if err != nil {
		t.Fatalf("DefaultRegistry.Get(default) error: %v", err)
	}
	if _, ok := flow.(defaultFlow); !ok {
		t.Errorf("registered flow is not defaultFlow, got %T", flow)
	}
}

func TestDefault(t *testing.T) {
	t.Run("Route", func(t *testing.T) { TestDefaultFlow_Route(t) })
	t.Run("Registered", func(t *testing.T) { TestDefaultFlow_Registered(t) })
}
