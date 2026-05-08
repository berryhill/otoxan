package testutil

import (
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// AssertParityString checks that doc[key] is a string equal to want.
func AssertParityString(t *testing.T, doc bson.M, key, want string) {
	t.Helper()
	got, ok := doc[key].(string)
	if !ok {
		t.Fatalf("expected %s to be string, got %T (%v)", key, doc[key], doc[key])
	}
	if got != want {
		t.Fatalf("%s mismatch: got %q, want %q", key, got, want)
	}
}

// AssertParityInt checks that doc[key] is a numeric value equal to want.
func AssertParityInt(t *testing.T, doc bson.M, key string, want int) {
	t.Helper()
	var got int
	switch v := doc[key].(type) {
	case int:
		got = v
	case int32:
		got = int(v)
	case int64:
		got = int(v)
	case float64:
		got = int(v)
	default:
		t.Fatalf("expected %s to be numeric, got %T (%v)", key, doc[key], doc[key])
	}
	if got != want {
		t.Fatalf("%s mismatch: got %d, want %d", key, got, want)
	}
}

// AssertParityBool checks that doc[key] is a bool equal to want.
func AssertParityBool(t *testing.T, doc bson.M, key string, want bool) {
	t.Helper()
	got, ok := doc[key].(bool)
	if !ok {
		t.Fatalf("expected %s to be bool, got %T (%v)", key, doc[key], doc[key])
	}
	if got != want {
		t.Fatalf("%s mismatch: got %v, want %v", key, got, want)
	}
}
