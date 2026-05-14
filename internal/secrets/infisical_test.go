package secrets

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestClient_Get_success covers the happy path: token exchange + secret fetch.
func TestClient_Get_success(t *testing.T) {
	var tokenCalls, secretCalls int

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/token/oauth":
			tokenCalls++
			if r.Method != http.MethodPost {
				t.Fatalf("expected POST, got %s", r.Method)
			}
			if ct := r.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
				t.Fatalf("expected Content-Type application/x-www-form-urlencoded, got %s", ct)
			}
			if err := r.ParseForm(); err != nil {
				t.Fatalf("parse form: %v", err)
			}
			if r.PostFormValue("client_id") != "admin-id" {
				t.Fatalf("expected client_id=admin-id, got %s", r.PostFormValue("client_id"))
			}
			if r.PostFormValue("client_secret") != "admin-secret" {
				t.Fatalf("expected client_secret=admin-secret, got %s", r.PostFormValue("client_secret"))
			}
			if r.PostFormValue("grant_type") != "client_credentials" {
				t.Fatalf("expected grant_type=client_credentials, got %s", r.PostFormValue("grant_type"))
			}
			json.NewEncoder(w).Encode(map[string]any{
				"access_token": "tok-123",
				"token_type":   "Bearer",
				"expires_in":   3600,
			})

		case "/api/v3/secrets/raw/MONGO_URI":
			secretCalls++
			if r.Method != http.MethodGet {
				t.Fatalf("expected GET, got %s", r.Method)
			}
			auth := r.Header.Get("Authorization")
			if auth != "Bearer tok-123" {
				t.Fatalf("expected Authorization 'Bearer tok-123', got %s", auth)
			}
			q := r.URL.Query()
			if q.Get("workspaceId") != "proj-123" {
				t.Fatalf("expected workspaceId=proj-123, got %s", q.Get("workspaceId"))
			}
			if q.Get("environment") != "staging" {
				t.Fatalf("expected environment=staging, got %s", q.Get("environment"))
			}
			json.NewEncoder(w).Encode(map[string]any{
				"secret": map[string]string{"secretValue": "mongodb://localhost:27017/testdb"},
			})

		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "admin-id", "admin-secret")
	c.HTTPClient = &http.Client{Timeout: 2 * time.Second}

	val, err := c.Get(context.Background(), "MONGO_URI", "proj-123", "staging")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "mongodb://localhost:27017/testdb" {
		t.Fatalf("expected mongodb://localhost:27017/testdb, got %s", val)
	}
	if tokenCalls != 1 {
		t.Fatalf("expected 1 token call, got %d", tokenCalls)
	}
	if secretCalls != 1 {
		t.Fatalf("expected 1 secret call, got %d", secretCalls)
	}
}

// TestClient_Get_missingCreds verifies error when client_id or client_secret is empty.
func TestClient_Get_missingCreds(t *testing.T) {
	c := NewClient("http://example.com", "", "admin-secret")
	_, err := c.Get(nil, "ANY", "proj", "dev")
	if err == nil {
		t.Fatal("expected error when client_id is empty")
	}
	if !strings.Contains(err.Error(), "client_id and client_secret required") {
		t.Fatalf("expected 'client_id and client_secret required' in error, got: %v", err)
	}

	c = NewClient("http://example.com", "admin-id", "")
	_, err = c.Get(nil, "ANY", "proj", "dev")
	if err == nil {
		t.Fatal("expected error when client_secret is empty")
	}
}

// TestClient_Get_missingWorkspaceID verifies error when workspaceID is empty.
func TestClient_Get_missingWorkspaceID(t *testing.T) {
	c := NewClient("http://example.com", "admin-id", "admin-secret")
	_, err := c.Get(nil, "ANY", "", "dev")
	if err == nil {
		t.Fatal("expected error when workspaceID is empty")
	}
	if !strings.Contains(err.Error(), "workspaceID required") {
		t.Fatalf("expected 'workspaceID required' in error, got: %v", err)
	}
}

// TestClient_Get_tokenEndpointNon200 verifies error mapping when token endpoint fails.
func TestClient_Get_tokenEndpointNon200(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/auth/token/oauth" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		t.Fatalf("unexpected path: %s", r.URL.Path)
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "bad-id", "bad-secret")
	c.HTTPClient = &http.Client{Timeout: 2 * time.Second}

	_, err := c.Get(context.Background(), "ANY", "proj", "dev")
	if err == nil {
		t.Fatal("expected error on 401 from token endpoint")
	}
	if !strings.Contains(err.Error(), "token endpoint returned 401") {
		t.Fatalf("expected 'token endpoint returned 401' in error, got: %v", err)
	}
}

// TestClient_Get_secretEndpointNon200 verifies error mapping when secrets endpoint fails.
func TestClient_Get_secretEndpointNon200(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/token/oauth":
			json.NewEncoder(w).Encode(map[string]any{
				"access_token": "tok-123",
				"token_type":   "Bearer",
				"expires_in":   3600,
			})
		case "/api/v3/secrets/raw/MISSING":
			http.Error(w, "not found", http.StatusNotFound)
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "admin-id", "admin-secret")
	c.HTTPClient = &http.Client{Timeout: 2 * time.Second}

	_, err := c.Get(context.Background(), "MISSING", "proj", "dev")
	if err == nil {
		t.Fatal("expected error on 404 from secret endpoint")
	}
	if !strings.Contains(err.Error(), "secret endpoint returned 404") {
		t.Fatalf("expected 'secret endpoint returned 404' in error, got: %v", err)
	}
}

// TestClient_Get_tokenDecodeError verifies error mapping when token response is malformed.
func TestClient_Get_tokenDecodeError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/auth/token/oauth" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("not-json"))
			return
		}
		t.Fatalf("unexpected path: %s", r.URL.Path)
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "admin-id", "admin-secret")
	c.HTTPClient = &http.Client{Timeout: 2 * time.Second}

	_, err := c.Get(context.Background(), "ANY", "proj", "dev")
	if err == nil {
		t.Fatal("expected error on malformed token response")
	}
	if !strings.Contains(err.Error(), "token decode") {
		t.Fatalf("expected 'token decode' in error, got: %v", err)
	}
}

// TestClient_Get_secretDecodeError verifies error mapping when secret response is malformed.
func TestClient_Get_secretDecodeError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/token/oauth":
			json.NewEncoder(w).Encode(map[string]any{
				"access_token": "tok-123",
				"token_type":   "Bearer",
				"expires_in":   3600,
			})
		case "/api/v3/secrets/raw/BAD":
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("not-json"))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "admin-id", "admin-secret")
	c.HTTPClient = &http.Client{Timeout: 2 * time.Second}

	_, err := c.Get(context.Background(), "BAD", "proj", "dev")
	if err == nil {
		t.Fatal("expected error on malformed secret response")
	}
	if !strings.Contains(err.Error(), "secret decode") {
		t.Fatalf("expected 'secret decode' in error, got: %v", err)
	}
}

// TestClient_Get_timeout verifies the request times out appropriately.
func TestClient_Get_timeout(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(3 * time.Second)
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "admin-id", "admin-secret")
	c.HTTPClient = &http.Client{Timeout: 500 * time.Millisecond}

	start := time.Now()
	_, err := c.Get(context.Background(), "SLOW", "proj", "dev")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("took too long to fail: %v", elapsed)
	}
}

// TestClient_Get_defaultEnv verifies that empty environment defaults to "dev".
func TestClient_Get_defaultEnv(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/token/oauth":
			json.NewEncoder(w).Encode(map[string]any{
				"access_token": "tok-123",
				"token_type":   "Bearer",
				"expires_in":   3600,
			})
		case "/api/v3/secrets/raw/FOO":
			q := r.URL.Query()
			if q.Get("environment") != "dev" {
				t.Fatalf("expected environment=dev (default), got %s", q.Get("environment"))
			}
			json.NewEncoder(w).Encode(map[string]any{
				"secret": map[string]string{"secretValue": "bar"},
			})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "admin-id", "admin-secret")
	c.HTTPClient = &http.Client{Timeout: 2 * time.Second}

	val, err := c.Get(context.Background(), "FOO", "proj", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "bar" {
		t.Fatalf("expected bar, got %s", val)
	}
}

// TestClient_Get_defaultBaseURL verifies that empty baseURL defaults to the production endpoint.
func TestClient_Get_defaultBaseURL(t *testing.T) {
	c := NewClient("", "admin-id", "admin-secret")
	if c.BaseURL != "https://app.infisical.com" {
		t.Fatalf("expected default base URL https://app.infisical.com, got %s", c.BaseURL)
	}
}

// TestClient_Get_tokenMissingAccessToken verifies error when token response omits access_token.
func TestClient_Get_tokenMissingAccessToken(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/auth/token/oauth" {
			json.NewEncoder(w).Encode(map[string]any{
				"token_type": "Bearer",
				"expires_in": 3600,
			})
			return
		}
		t.Fatalf("unexpected path: %s", r.URL.Path)
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "admin-id", "admin-secret")
	c.HTTPClient = &http.Client{Timeout: 2 * time.Second}

	_, err := c.Get(context.Background(), "ANY", "proj", "dev")
	if err == nil {
		t.Fatal("expected error when access_token missing")
	}
	if !strings.Contains(err.Error(), "missing access_token") {
		t.Fatalf("expected 'missing access_token' in error, got: %v", err)
	}
}
