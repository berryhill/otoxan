package identity

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/silas/otoxan/pkg/stores/identitystore"
)

// ErrIdentityNotFound is returned when an identity cannot be resolved.
var ErrIdentityNotFound = errors.New("identity not found")

// ErrStoreUnavailable is returned when the identity store is unavailable.
// This wraps the underlying error from the store.
type ErrStoreUnavailable struct {
	cause error
}

func (e *ErrStoreUnavailable) Error() string {
	return fmt.Sprintf("identity store unavailable: %v", e.cause)
}

func (e *ErrStoreUnavailable) Unwrap() error {
	return e.cause
}

// ResolveOptions configures the Resolve behavior.
type ResolveOptions struct {
	// Version specifies a specific version to resolve. If empty, resolves the
	// active version.
	Version string

	// BypassCache forces a fresh fetch from the store, bypassing the cache.
	BypassCache bool
}

// CacheEntry holds a cached manifest with its expiry time.
type CacheEntry struct {
	Manifest  *Manifest
	ExpiresAt time.Time
}

// Cache is a TTL-based in-memory cache for identity manifests.
// It is safe for concurrent use.
type Cache struct {
	mu      sync.RWMutex
	entries map[string]CacheEntry
	ttl     time.Duration
}

// NewCache creates a new cache with the given TTL for entries.
func NewCache(ttl time.Duration) *Cache {
	return &Cache{
		entries: make(map[string]CacheEntry),
		ttl:     ttl,
	}
}

// cacheKey generates the cache key for an identity lookup.
func cacheKey(name string, version string) string {
	if version != "" {
		return name + ":" + version
	}
	return name + ":active"
}

// Get returns the cached manifest for the given name/version, or nil if
// not found or expired.
func (c *Cache) Get(name, version string) *Manifest {
	c.mu.RLock()
	defer c.mu.RUnlock()

	key := cacheKey(name, version)
	entry, ok := c.entries[key]
	if !ok {
		return nil
	}
	if time.Now().After(entry.ExpiresAt) {
		return nil
	}
	return entry.Manifest
}

// Set stores a manifest in the cache with the given name/version.
func (c *Cache) Set(name, version string, manifest *Manifest) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := cacheKey(name, version)
	c.entries[key] = CacheEntry{
		Manifest:  manifest,
		ExpiresAt: time.Now().Add(c.ttl),
	}
}

// Invalidate removes an entry from the cache. Call this when an identity is
// updated, activated, or retired to ensure fresh data.
func (c *Cache) Invalidate(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Invalidate both active and version-specific cache entries
	delete(c.entries, cacheKey(name, ""))
	for key := range c.entries {
		if len(key) > len(name) && key[:len(name)] == name {
			delete(c.entries, key)
		}
	}
}

// Clear removes all entries from the cache.
func (c *Cache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]CacheEntry)
}

// Size returns the number of entries in the cache (including expired ones).
func (c *Cache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

// DefaultCacheTTL is the default TTL for cache entries.
const DefaultCacheTTL = 5 * time.Minute

// Resolver resolves identity manifests with local caching.
type Resolver struct {
	cache  *Cache
	store  *identitystore.Store
}

// NewResolver creates a Resolver with a default 5-minute cache TTL.
func NewResolver(store *identitystore.Store) *Resolver {
	return NewResolverWithTTL(store, DefaultCacheTTL)
}

// NewResolverWithTTL creates a Resolver with a custom cache TTL.
func NewResolverWithTTL(store *identitystore.Store, ttl time.Duration) *Resolver {
	return &Resolver{
		cache: NewCache(ttl),
		store: store,
	}
}

// Cache returns the underlying cache for inspection or external invalidation.
func (r *Resolver) Cache() *Cache {
	return r.cache
}

// resolveManifest converts an IdentityManifest from the store into a
// pkg/identity.Manifest for use by adapters.
func resolveManifest(ims *identitystore.IdentityManifest) *Manifest {
	if ims == nil {
		return nil
	}
	// Convert ProviderType keys to string keys
	overrides := make(map[string]string, len(ims.ProviderProfiles))
	for k, v := range ims.ProviderProfiles {
		overrides[string(k)] = v
	}
	return &Manifest{
		IdentityID:        ims.Name,
		Version:           ims.Version,
		Name:              ims.Name,
		Description:       ims.Description,
		SystemPrompt:      ims.Manifest,
		CreatedAt:         ims.CreatedAt,
		ProviderOverrides: overrides,
	}
}

// Resolve looks up an identity manifest by name. By default, it resolves the
// currently active version. Use opts.Version to resolve a specific version.
//
// Resolution order:
//   - Check local cache (if not bypassed)
//   - Fetch from store (GetActive or Get depending on options)
//   - Cache the result
//
// Returns ErrIdentityNotFound if the identity does not exist.
// Returns *ErrStoreUnavailable if the store call fails.
func (r *Resolver) Resolve(ctx context.Context, name string, opts ResolveOptions) (*Manifest, error) {
	// Fast path: check cache unless bypassed
	if !opts.BypassCache {
		if cached := r.cache.Get(name, opts.Version); cached != nil {
			return cached, nil
		}
	}

	// Fetch from store
	var ims *identitystore.IdentityManifest
	var err error

	if opts.Version != "" {
		ims, err = r.store.Get(ctx, name, opts.Version)
	} else {
		ims, err = r.store.GetActive(ctx, name)
	}

	if err != nil {
		if errors.Is(err, identitystore.ErrIdentityNotFound) ||
			errors.Is(err, identitystore.ErrNoActiveIdentity) {
			return nil, ErrIdentityNotFound
		}
		// Wrap store errors as ErrStoreUnavailable - never panic
		return nil, &ErrStoreUnavailable{cause: err}
	}

	manifest := resolveManifest(ims)

	// Cache the result
	r.cache.Set(name, opts.Version, manifest)

	return manifest, nil
}

// ResolveActive is a convenience method that resolves the currently active
// version of an identity.
func (r *Resolver) ResolveActive(ctx context.Context, name string) (*Manifest, error) {
	return r.Resolve(ctx, name, ResolveOptions{})
}

// ResolveVersion resolves a specific version of an identity.
func (r *Resolver) ResolveVersion(ctx context.Context, name, version string) (*Manifest, error) {
	return r.Resolve(ctx, name, ResolveOptions{Version: version})
}

// InvalidateCache invalidates cached entries for the given identity name.
// Call this after updating, activating, or retiring an identity.
func (r *Resolver) InvalidateCache(name string) {
	r.cache.Invalidate(name)
}
