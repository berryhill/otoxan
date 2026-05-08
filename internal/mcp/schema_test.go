package mcp

import (
	"encoding/json"
	"testing"
	"time"
)

func TestSchemaOfBasicStruct(t *testing.T) {
	type Args struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}

	schema := SchemaOf[Args]()
	var m map[string]any
	if err := json.Unmarshal(schema, &m); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}

	if m["type"] != "object" {
		t.Fatalf("expected type object, got %v", m["type"])
	}

	props, ok := m["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties map")
	}

	nameProp, ok := props["name"].(map[string]any)
	if !ok {
		t.Fatal("expected name property")
	}
	if nameProp["type"] != "string" {
		t.Fatalf("expected name type string, got %v", nameProp["type"])
	}

	countProp, ok := props["count"].(map[string]any)
	if !ok {
		t.Fatal("expected count property")
	}
	if countProp["type"] != "integer" {
		t.Fatalf("expected count type integer, got %v", countProp["type"])
	}
}

func TestSchemaOfRequiredFields(t *testing.T) {
	type Args struct {
		Required string `json:"required" jsonschema:"required"`
		Optional string `json:"optional,omitempty"`
	}

	schema := SchemaOf[Args]()
	var m map[string]any
	if err := json.Unmarshal(schema, &m); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}

	req, ok := m["required"].([]any)
	if !ok {
		t.Fatal("expected required array")
	}

	var hasRequired, hasOptional bool
	for _, v := range req {
		s, _ := v.(string)
		if s == "required" {
			hasRequired = true
		}
		if s == "optional" {
			hasOptional = true
		}
	}

	if !hasRequired {
		t.Fatal("expected 'required' in required array")
	}
	if hasOptional {
		t.Fatal("expected 'optional' NOT in required array")
	}
}

func TestSchemaOfPointerField(t *testing.T) {
	type Args struct {
		Optional *string `json:"optional,omitempty"`
		Required *string `json:"required"`
	}

	schema := SchemaOf[Args]()
	var m map[string]any
	if err := json.Unmarshal(schema, &m); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}

	props, ok := m["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties map")
	}

	// Pointer fields with omitempty should not be required.
	optProp, ok := props["optional"].(map[string]any)
	if !ok {
		t.Fatal("expected optional property")
	}
	if optProp["type"] != "string" {
		t.Fatalf("expected optional type string, got %v", optProp["type"])
	}

	reqProp, ok := props["required"].(map[string]any)
	if !ok {
		t.Fatal("expected required property")
	}
	if reqProp["type"] != "string" {
		t.Fatalf("expected required type string, got %v", reqProp["type"])
	}
}

func TestSchemaOfTimeField(t *testing.T) {
	type Args struct {
		CreatedAt time.Time `json:"created_at"`
	}

	schema := SchemaOf[Args]()
	var m map[string]any
	if err := json.Unmarshal(schema, &m); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}

	props, ok := m["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties map")
	}

	createdProp, ok := props["created_at"].(map[string]any)
	if !ok {
		t.Fatal("expected created_at property")
	}
	if createdProp["type"] != "string" {
		t.Fatalf("expected created_at type string, got %v", createdProp["type"])
	}
	if createdProp["format"] != "date-time" {
		t.Fatalf("expected created_at format date-time, got %v", createdProp["format"])
	}
}

func TestSchemaOfCache(t *testing.T) {
	type Args struct {
		A string `json:"a"`
	}

	// First call populates cache.
	s1 := SchemaOf[Args]()
	// Second call should hit cache and return identical bytes.
	s2 := SchemaOf[Args]()

	if string(s1) != string(s2) {
		t.Fatal("expected cached schema to be identical")
	}
}

func TestSchemaOfInterfaceFallback(t *testing.T) {
	schema := SchemaOf[any]()
	var m map[string]any
	if err := json.Unmarshal(schema, &m); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	if m["type"] != "object" {
		t.Fatalf("expected type object for any, got %v", m["type"])
	}
	if m["additionalProperties"] != true {
		t.Fatalf("expected additionalProperties true for any, got %v", m["additionalProperties"])
	}
}
