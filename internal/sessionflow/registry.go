package sessionflow

import (
	"fmt"
	"sort"
	"sync"
)

// Registry is a thread-safe map of flow identifiers to SessionFlow implementations.
// It is seeded at process start and may be extended by built-in flows or future
// external loaders (e.g. <home>/flows/*.yaml).
type Registry struct {
	mu     sync.RWMutex
	flows  map[string]SessionFlow
}

// NewRegistry returns an empty Registry.  Built-in flows should be registered
// immediately after construction, typically in an init or bootstrap function.
func NewRegistry() *Registry {
	return &Registry{
		flows: make(map[string]SessionFlow),
	}
}

// Register adds a flow to the registry under the given id.  If a flow already
// exists for id it is overwritten.  Register is safe for concurrent use.
func (r *Registry) Register(id string, flow SessionFlow) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.flows[id] = flow
}

// Get returns the SessionFlow registered under id, or an error if no flow is
// registered for that id.  The error message contains the id so callers can
// surface it to logs or telemetry.
func (r *Registry) Get(id string) (SessionFlow, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	flow, ok := r.flows[id]
	if !ok {
		return nil, fmt.Errorf("sessionflow: no flow registered for id %q", id)
	}
	return flow, nil
}

// List returns all registered flow ids sorted lexicographically.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.flows))
	for id := range r.flows {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// DefaultRegistry is the package-level default registry, seeded at init time with
// built-in flows ("default" and "onboarding").  External loaders may add
// additional flows at startup.
var DefaultRegistry = NewRegistry()

func init() {
	DefaultRegistry.Register("default", defaultFlow{})
}
