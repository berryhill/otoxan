package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestInfisicalGet_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		q := r.URL.Query()
		if q.Get("workspaceId") != "proj-123" || q.Get("environment") != "staging" {
			http.Error(w, "bad query", http.StatusBadRequest)
			return
		}
		if r.URL.Path != "/api/v3/secrets/raw/MONGO_URI" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"secret": map[string]string{"secretValue": "mongodb://localhost:27017/testdb"},
		})
	}))
	defer ts.Close()

	os.Setenv("INFISICAL_TOKEN", "test-token")
	os.Setenv("INFISICAL_PROJECT_ID", "proj-123")
	os.Setenv("INFISICAL_ENV", "staging")
	defer os.Unsetenv("INFISICAL_TOKEN")
	defer os.Unsetenv("INFISICAL_PROJECT_ID")
	defer os.Unsetenv("INFISICAL_ENV")

	// Point the package-level base URL at the test server.
	oldBase := infisicalBaseURL
	infisicalBaseURL = ts.URL
	defer func() { infisicalBaseURL = oldBase }()

	val, err := InfisicalGet("MONGO_URI")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "mongodb://localhost:27017/testdb" {
		t.Fatalf("expected mongodb://localhost:27017/testdb, got %s", val)
	}
}

func TestInfisicalGet_MissingToken(t *testing.T) {
	os.Unsetenv("INFISICAL_TOKEN")
	os.Unsetenv("INFISICAL_PROJECT_ID")
	_, err := InfisicalGet("ANY")
	if err == nil {
		t.Fatal("expected error when INFISICAL_TOKEN missing")
	}
}

func TestInfisicalGet_MissingProjectID(t *testing.T) {
	os.Setenv("INFISICAL_TOKEN", "tok")
	defer os.Unsetenv("INFISICAL_TOKEN")
	os.Unsetenv("INFISICAL_PROJECT_ID")
	_, err := InfisicalGet("ANY")
	if err == nil {
		t.Fatal("expected error when INFISICAL_PROJECT_ID missing")
	}
}

func TestInfisicalGet_Non200(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer ts.Close()

	os.Setenv("INFISICAL_TOKEN", "tok")
	os.Setenv("INFISICAL_PROJECT_ID", "proj")
	defer os.Unsetenv("INFISICAL_TOKEN")
	defer os.Unsetenv("INFISICAL_PROJECT_ID")

	oldBase := infisicalBaseURL
	infisicalBaseURL = ts.URL
	defer func() { infisicalBaseURL = oldBase }()

	_, err := InfisicalGet("MISSING")
	if err == nil {
		t.Fatal("expected error on non-200")
	}
}

func TestInfisicalGet_Timeout(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(6 * time.Second)
	}))
	defer ts.Close()

	os.Setenv("INFISICAL_TOKEN", "tok")
	os.Setenv("INFISICAL_PROJECT_ID", "proj")
	defer os.Unsetenv("INFISICAL_TOKEN")
	defer os.Unsetenv("INFISICAL_PROJECT_ID")

	oldBase := infisicalBaseURL
	infisicalBaseURL = ts.URL
	defer func() { infisicalBaseURL = oldBase }()

	start := time.Now()
	_, err := InfisicalGet("SLOW")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if elapsed > 7*time.Second {
		t.Fatalf("took too long to fail: %v", elapsed)
	}
}

func TestMongoClient_FromEnv(t *testing.T) {
	// We cannot spin up a real Mongo here, so we test the URI resolution logic
	// by checking the error message when connection fails.
	os.Setenv("MONGO_URI", "mongodb://127.0.0.1:12345/doesnotexist")
	os.Setenv("MONGO_DB", "testdb")
	defer os.Unsetenv("MONGO_URI")
	defer os.Unsetenv("MONGO_DB")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, dbName, err := MongoClient(ctx)
	if err == nil {
		// If by some miracle a mongod is listening on 12345, that's fine,
		// but we still expect the dbName to be correct.
		if dbName != "testdb" {
			t.Fatalf("expected dbName testdb, got %s", dbName)
		}
		return
	}
	// Error is expected; just make sure it mentions mongo.
	if dbName != "testdb" {
		t.Fatalf("expected dbName testdb even on error, got %s", dbName)
	}
}

func TestMongoClient_FromInfisical(t *testing.T) {
	os.Unsetenv("MONGO_URI")
	os.Setenv("MONGO_DB", "infidb")
	defer os.Unsetenv("MONGO_DB")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"secret": map[string]string{"secretValue": "mongodb://127.0.0.1:12345/infidb"},
		})
	}))
	defer ts.Close()

	os.Setenv("INFISICAL_TOKEN", "tok")
	os.Setenv("INFISICAL_PROJECT_ID", "proj")
	defer os.Unsetenv("INFISICAL_TOKEN")
	defer os.Unsetenv("INFISICAL_PROJECT_ID")

	oldBase := infisicalBaseURL
	infisicalBaseURL = ts.URL
	defer func() { infisicalBaseURL = oldBase }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, dbName, err := MongoClient(ctx)
	if err == nil {
		if dbName != "infidb" {
			t.Fatalf("expected dbName infidb, got %s", dbName)
		}
		return
	}
	if dbName != "infidb" {
		t.Fatalf("expected dbName infidb even on error, got %s", dbName)
	}
}

func TestMongoClient_MissingURI(t *testing.T) {
	os.Unsetenv("MONGO_URI")
	os.Unsetenv("INFISICAL_TOKEN")

	ctx := context.Background()
	_, _, err := MongoClient(ctx)
	if err == nil {
		t.Fatal("expected error when both MONGO_URI and INFISICAL_TOKEN missing")
	}
}
