package llm

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestProvider_New(t *testing.T) {
	tests := []struct {
		name    string
		provider string
		model   string
		wantErr error
	}{
		{"claude default model", "claude", "", nil},
		{"claude explicit model", "claude", "claude-opus-4", nil},
		{"mock", "mock", "", nil},
		{"openrouter stub", "openrouter", "", nil}, // returns provider, but RunSession errors
		{"unknown", "gpt-7", "", ErrProviderNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := New(tt.provider, tt.model)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("New(%q) err=%v, want %v", tt.provider, err, tt.wantErr)
			}
			if tt.wantErr != nil {
				return
			}
			if p == nil {
				t.Fatalf("New(%q) returned nil provider", tt.provider)
			}
		})
	}
}

func TestMock_RunSession(t *testing.T) {
	m := NewMock("test-model")

	t.Run("echo", func(t *testing.T) {
		res, err := m.RunSession(context.Background(), "hello world")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(res.Output, "hello world") {
			t.Fatalf("output missing prompt: %q", res.Output)
		}
		if res.TokensUsed <= 0 {
			t.Fatalf("expected positive tokens, got %d", res.TokensUsed)
		}
	})

	t.Run("simulated error", func(t *testing.T) {
		_, err := m.RunSession(context.Background(), "trigger an error please")
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestRetryWithBackoff(t *testing.T) {
	t.Run("success on first try", func(t *testing.T) {
		calls := 0
		err := retryWithBackoff(context.Background(), 3, func() error {
			calls++
			return nil
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if calls != 1 {
			t.Fatalf("expected 1 call, got %d", calls)
		}
	})

	t.Run("success after retries", func(t *testing.T) {
		calls := 0
		err := retryWithBackoff(context.Background(), 3, func() error {
			calls++
			if calls < 2 {
				return errors.New("transient")
			}
			return nil
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if calls != 2 {
			t.Fatalf("expected 2 calls, got %d", calls)
		}
	})

	t.Run("exhausted retries", func(t *testing.T) {
		calls := 0
		err := retryWithBackoff(context.Background(), 2, func() error {
			calls++
			return errors.New("permanent")
		})
		if err == nil {
			t.Fatal("expected error")
		}
		if calls != 2 {
			t.Fatalf("expected 2 calls, got %d", calls)
		}
	})

	t.Run("context cancel stops retry", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		go func() {
			time.Sleep(50 * time.Millisecond)
			cancel()
		}()
		err := retryWithBackoff(ctx, 5, func() error {
			calls++
			return errors.New("transient")
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
		if calls < 1 {
			t.Fatalf("expected at least 1 call, got %d", calls)
		}
	})
}
