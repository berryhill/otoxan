package mcp

import (
	"encoding/json"
	"reflect"
	"sync"
	"time"

	"github.com/invopop/jsonschema"
)

// schemaCache maps reflect.Type → cached JSON schema bytes.
var schemaCache sync.Map // map[reflect.Type]json.RawMessage

// SchemaOf returns a JSON Schema 2020-12 for the type parameter T.
// It caches the result per type to avoid repeated reflection cost.
func SchemaOf[T any]() json.RawMessage {
	var zero T
	typ := reflect.TypeOf(zero)
	if typ == nil {
		// T is an interface{} or similar; return permissive schema.
		return json.RawMessage(`{"type":"object","additionalProperties":true}`)
	}

	if cached, ok := schemaCache.Load(typ); ok {
		return cached.(json.RawMessage)
	}

	r := &jsonschema.Reflector{
		ExpandedStruct:             true,
		RequiredFromJSONSchemaTags: true,
		Mapper: func(t reflect.Type) *jsonschema.Schema {
			if t == reflect.TypeOf(time.Time{}) {
				return &jsonschema.Schema{
					Type:   "string",
					Format: "date-time",
				}
			}
			return nil
		},
	}

	schema := r.Reflect(zero)
	b, err := json.Marshal(schema)
	if err != nil {
		// Fallback: permissive object schema.
		b = []byte(`{"type":"object","additionalProperties":true}`)
	}

	schemaCache.Store(typ, json.RawMessage(b))
	return json.RawMessage(b)
}
