package dispatch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOrchestrator_modelComplete(t *testing.T) {
	// Mock API server that returns "pong".
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "application/json", r.Header.Get("content-type"))

		resp := map[string]any{
			"content": []map[string]string{
				{"type": "text", "text": "pong"},
			},
		}
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	cfg := OrchestratorConfig{
		Default: "test",
		Models: map[string]ModelConfig{
			"test": {
				Provider: "test",
				Model:    "test-model",
				APIKey:   "test-key",
				BaseURL:  srv.URL,
			},
		},
	}

	o := NewOrchestrator(cfg).WithClient(srv.Client())
	reply, err := o.Invoke(context.Background(), Routed{
		Action:  "model_complete",
		Payload: "ping",
	})
	require.NoError(t, err)
	assert.Equal(t, "pong", reply.Text)
	assert.Empty(t, reply.Error)
	assert.Nil(t, reply.Handoff)
}

func TestOrchestrator_modelComplete_missingModel(t *testing.T) {
	cfg := OrchestratorConfig{
		Default: "missing",
		Models:  map[string]ModelConfig{},
	}

	o := NewOrchestrator(cfg)
	reply, err := o.Invoke(context.Background(), Routed{
		Action:  "model_complete",
		Payload: "ping",
	})
	require.NoError(t, err)
	assert.Empty(t, reply.Text)
	assert.Contains(t, reply.Error, "missing")
	assert.Nil(t, reply.Handoff)
}

func TestOrchestrator_tool(t *testing.T) {
	cfg := OrchestratorConfig{
		Default: "test",
		Models:  map[string]ModelConfig{},
	}

	o := NewOrchestrator(cfg)
	reply, err := o.Invoke(context.Background(), Routed{
		Action:  "tool",
		Payload: "echo|hello world",
	})
	require.NoError(t, err)
	assert.Equal(t, "hello world", reply.Text)
	assert.Empty(t, reply.Error)
	assert.Nil(t, reply.Handoff)
}

func TestOrchestrator_tool_noArgs(t *testing.T) {
	cfg := OrchestratorConfig{
		Default: "test",
		Models:  map[string]ModelConfig{},
	}

	o := NewOrchestrator(cfg)
	reply, err := o.Invoke(context.Background(), Routed{
		Action:  "tool",
		Payload: "echo",
	})
	require.NoError(t, err)
	assert.Equal(t, "", reply.Text)
	assert.Empty(t, reply.Error)
	assert.Nil(t, reply.Handoff)
}

func TestOrchestrator_tool_unknown(t *testing.T) {
	cfg := OrchestratorConfig{
		Default: "test",
		Models:  map[string]ModelConfig{},
	}

	o := NewOrchestrator(cfg)
	reply, err := o.Invoke(context.Background(), Routed{
		Action:  "tool",
		Payload: "nonexistent",
	})
	require.NoError(t, err)
	assert.Empty(t, reply.Text)
	assert.Contains(t, reply.Error, "unknown tool")
	assert.Nil(t, reply.Handoff)
}

func TestOrchestrator_noop(t *testing.T) {
	cfg := OrchestratorConfig{
		Default: "test",
		Models:  map[string]ModelConfig{},
	}

	o := NewOrchestrator(cfg)
	reply, err := o.Invoke(context.Background(), Routed{
		Action:  "noop",
		Payload: "ignored",
	})
	require.NoError(t, err)
	assert.Empty(t, reply.Text)
	assert.Empty(t, reply.Error)
	assert.Nil(t, reply.Handoff)
}

func TestOrchestrator_handoff(t *testing.T) {
	cfg := OrchestratorConfig{
		Default: "test",
		Models:  map[string]ModelConfig{},
	}

	o := NewOrchestrator(cfg)
	reply, err := o.Invoke(context.Background(), Routed{
		Action:  "handoff",
		Payload: "onboarding",
	})
	require.NoError(t, err)
	assert.NotNil(t, reply.Handoff)
	assert.Equal(t, "onboarding", reply.Handoff.TargetFlow)
	assert.Equal(t, "onboarding", reply.Handoff.Payload)
	assert.Empty(t, reply.Text)
	assert.Empty(t, reply.Error)
}

func TestOrchestrator_unknownAction(t *testing.T) {
	cfg := OrchestratorConfig{
		Default: "test",
		Models:  map[string]ModelConfig{},
	}

	o := NewOrchestrator(cfg)
	reply, err := o.Invoke(context.Background(), Routed{
		Action:  "bogus",
		Payload: "",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, reply.Error)
	assert.Contains(t, reply.Error, "unknown action")
	assert.Nil(t, reply.Handoff)
}

func TestOrchestrator_RegisterTool(t *testing.T) {
	cfg := OrchestratorConfig{
		Default: "test",
		Models:  map[string]ModelConfig{},
	}

	o := NewOrchestrator(cfg)
	o.RegisterTool("upper", func(_ context.Context, args string) (string, error) {
		return strings.ToUpper(args), nil
	})

	reply, err := o.Invoke(context.Background(), Routed{
		Action:  "tool",
		Payload: "upper|hello",
	})
	require.NoError(t, err)
	assert.Equal(t, "HELLO", reply.Text)
}

func TestOrchestrator_modelComplete_httpError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("server error"))
	}))
	defer srv.Close()

	cfg := OrchestratorConfig{
		Default: "test",
		Models: map[string]ModelConfig{
			"test": {
				Provider: "test",
				Model:    "test-model",
				BaseURL:  srv.URL,
			},
		},
	}

	o := NewOrchestrator(cfg).WithClient(srv.Client())
	reply, err := o.Invoke(context.Background(), Routed{
		Action:  "model_complete",
		Payload: "ping",
	})
	require.NoError(t, err)
	assert.Empty(t, reply.Text)
	assert.Contains(t, reply.Error, "http 500")
}
