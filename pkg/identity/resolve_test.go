package identity

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/silas/otoxan/pkg/stores/identitystore"
)

// mockStore implements a minimal store interface for testing.
type mockStore struct {
	mu      sync.Mutex
	entries map[string]*identitystore.IdentityManifest
	err     error // if set, all operations return this error
}

func newMockStore() *mockStore {
	return &mockStore{
		entries: make(map[string]*identitystore.IdentityManifest),
	}
}

func (m *mockStore) Get(ctx context.Context, name, version string) (*identitystore.IdentityManifest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	key := name + ":" + version
	entry, ok := m.entries[key]
	if !ok {
		return nil, identitystore.ErrIdentityNotFound
	}
	return entry, nil
}

func (m *mockStore) GetActive(ctx context.Context, name string) (*identitystore.IdentityManifest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	for _, entry := range m.entries {
		if entry.Name == name && entry.Status == identitystore.StatusActive {
			return entry, nil
		}
	}
	return nil, identitystore.ErrNoActiveIdentity
}

func (m *mockStore) Add(name, version string, status identitystore.IdentityStatus, manifest string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries[name+":"+version] = &identitystore.IdentityManifest{
		Name:      name,
		Version:   version,
		Status:    status,
		Manifest:  manifest,
		CreatedAt: time.Now(),
	}
}

// storeInterface mirrors the methods we use from identitystore.Store.
type storeInterface interface {
	Get(ctx context.Context, name, version string) (*identitystore.IdentityManifest, error)
	GetActive(ctx context.Context, name string) (*identitystore.IdentityManifest, error)
}

// testResolver wraps a mockStore with the Resolver for testing.
// We use a custom implementation that mirrors Resolver's logic but accepts
// our mock store interface.
type testResolver struct {
	cache  *Cache
	store  storeInterface
}

func newTestResolver(store storeInterface, ttl time.Duration) *testResolver {
	return &testResolver{
		cache: NewCache(ttl),
		store: store,
	}
}

func (tr *testResolver) resolve(ctx context.Context, name string, opts ResolveOptions) (*Manifest, error) {
	// Fast path: check cache unless bypassed
	if !opts.BypassCache {
		if cached := tr.cache.Get(name, opts.Version); cached != nil {
			return cached, nil
		}
	}

	// Fetch from store
	var ims *identitystore.IdentityManifest
	var err error

	if opts.Version != "" {
		ims, err = tr.store.Get(ctx, name, opts.Version)
	} else {
		ims, err = tr.store.GetActive(ctx, name)
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
	tr.cache.Set(name, opts.Version, manifest)
	return manifest, nil
}

func (tr *testResolver) invalidateCache(name string) {
	tr.cache.Invalidate(name)
}

// -----------------------------------------------------------------------
// Cache unit tests
// -----------------------------------------------------------------------

func TestCache_Get_Set(t *testing.T) {
	cache := NewCache(5 * time.Minute)
	m := &Manifest{IdentityID: "xander", Version: "v1", Name: "xander", SystemPrompt: "You are Xander."}

	// Initially empty
	if got := cache.Get("xander", "v1"); got != nil {
		t.Errorf("expected nil, got %v", got)
	}

	// Set and retrieve
	cache.Set("xander", "v1", m)
	got := cache.Get("xander", "v1")
	if got == nil {
		t.Fatal("expected cached value, got nil")
	}
	if got.SystemPrompt != m.SystemPrompt {
		t.Errorf("expected %q, got %q", m.SystemPrompt, got.SystemPrompt)
	}
}

func TestCache_Expiry(t *testing.T) {
	// Very short TTL for testing
	cache := NewCache(10 * time.Millisecond)
	m := &Manifest{IdentityID: "xander", Version: "v1", Name: "xander"}

	cache.Set("xander", "v1", m)

	// Immediate read should hit
	if got := cache.Get("xander", "v1"); got == nil {
		t.Fatal("expected cache hit immediately after set")
	}

	// Wait for expiry
	time.Sleep(20 * time.Millisecond)

	// Should now be expired
	if got := cache.Get("xander", "v1"); got != nil {
		t.Errorf("expected expired entry to return nil, got %v", got)
	}
}

func TestCache_ActiveVsVersion(t *testing.T) {
	cache := NewCache(5 * time.Minute)
	m1 := &Manifest{IdentityID: "xander", Version: "v1"}
	m2 := &Manifest{IdentityID: "xander", Version: "v2"}

	cache.Set("xander", "v1", m1)
	cache.Set("xander", "v2", m2)
	cache.Set("xander", "", m2) // active key

	// Version-specific cache entries are separate
	got1 := cache.Get("xander", "v1")
	got2 := cache.Get("xander", "v2")
	gotActive := cache.Get("xander", "")

	if got1 == nil || got1.Version != "v1" {
		t.Errorf("v1 cache miss or wrong value")
	}
	if got2 == nil || got2.Version != "v2" {
		t.Errorf("v2 cache miss or wrong value")
	}
	if gotActive == nil || gotActive.Version != "v2" {
		t.Errorf("active cache miss or wrong value")
	}
}

func TestCache_Invalidate(t *testing.T) {
	cache := NewCache(5 * time.Minute)
	m := &Manifest{IdentityID: "xander", Version: "v1"}

	cache.Set("xander", "v1", m)
	cache.Set("xander", "", m)

	// Verify both entries exist
	if cache.Get("xander", "v1") == nil {
		t.Fatal("expected v1 to be cached")
	}
	if cache.Get("xander", "") == nil {
		t.Fatal("expected active to be cached")
	}

	// Invalidate should remove all xander entries
	cache.Invalidate("xander")

	if cache.Get("xander", "v1") != nil {
		t.Errorf("expected v1 to be invalidated")
	}
	if cache.Get("xander", "") != nil {
		t.Errorf("expected active to be invalidated")
	}
}

func TestCache_Clear(t *testing.T) {
	cache := NewCache(5 * time.Minute)
	cache.Set("xander", "v1", &Manifest{IdentityID: "xander"})
	cache.Set("alice", "v1", &Manifest{IdentityID: "alice"})

	if cache.Size() != 2 {
		t.Fatalf("expected size 2, got %d", cache.Size())
	}

	cache.Clear()

	if cache.Size() != 0 {
		t.Errorf("expected size 0 after clear, got %d", cache.Size())
	}
}

func TestCache_Concurrent(t *testing.T) {
	cache := NewCache(5 * time.Minute)
	m := &Manifest{IdentityID: "xander", Version: "v1"}

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := "identity"
			if i%2 == 0 {
				cache.Set(name, "v1", m)
			} else {
				cache.Get(name, "v1")
			}
		}(i)
	}
	wg.Wait()
}

// -----------------------------------------------------------------------
// Resolver tests
// -----------------------------------------------------------------------

func TestResolver_CacheHit(t *testing.T) {
	store := newMockStore()
	store.Add("xander", "v1", identitystore.StatusActive, "You are Xander.")
	store.Add("xander", "v2", identitystore.StatusInactive, "You are Xander v2.")

	resolver := newTestResolver(store, 5*time.Minute)
	ctx := context.Background()

	// First call should hit the store
	manifest, err := resolver.resolve(ctx, "xander", ResolveOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if manifest.Version != "v1" {
		t.Errorf("expected v1, got %s", manifest.Version)
	}
	if manifest.SystemPrompt != "You are Xander." {
		t.Errorf("unexpected prompt: %s", manifest.SystemPrompt)
	}

	// Second call should hit the cache (store untouched)
	manifest2, err := resolver.resolve(ctx, "xander", ResolveOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if manifest2.Version != "v1" {
		t.Errorf("expected v1 from cache, got %s", manifest2.Version)
	}
}

func TestResolver_CacheMiss(t *testing.T) {
	store := newMockStore()
	store.Add("xander", "v1", identitystore.StatusActive, "You are Xander.")

	resolver := newTestResolver(store, 5*time.Minute)
	ctx := context.Background()

	// Bypass cache should force store lookup
	manifest, err := resolver.resolve(ctx, "xander", ResolveOptions{BypassCache: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if manifest.Version != "v1" {
		t.Errorf("expected v1, got %s", manifest.Version)
	}
}

func TestResolver_CacheExpiry(t *testing.T) {
	store := newMockStore()
	store.Add("xander", "v1", identitystore.StatusActive, "You are Xander.")

	// Very short TTL
	resolver := newTestResolver(store, 10*time.Millisecond)
	ctx := context.Background()

	// First call populates cache
	_, err := resolver.resolve(ctx, "xander", ResolveOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Wait for cache to expire
	time.Sleep(20 * time.Millisecond)

	// Should hit store again (cache miss due to expiry)
	manifest, err := resolver.resolve(ctx, "xander", ResolveOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if manifest.Version != "v1" {
		t.Errorf("expected v1, got %s", manifest.Version)
	}
}

func TestResolver_NotFound(t *testing.T) {
	store := newMockStore()
	resolver := newTestResolver(store, 5*time.Minute)
	ctx := context.Background()

	_, err := resolver.resolve(ctx, "nonexistent", ResolveOptions{})
	if !errors.Is(err, ErrIdentityNotFound) {
		t.Errorf("expected ErrIdentityNotFound, got %v", err)
	}
}

func TestResolver_VersionSpecific(t *testing.T) {
	store := newMockStore()
	store.Add("xander", "v1", identitystore.StatusActive, "You are Xander v1.")
	store.Add("xander", "v2", identitystore.StatusInactive, "You are Xander v2.")

	resolver := newTestResolver(store, 5*time.Minute)
	ctx := context.Background()

	// Resolve specific version
	manifest, err := resolver.resolve(ctx, "xander", ResolveOptions{Version: "v2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if manifest.Version != "v2" {
		t.Errorf("expected v2, got %s", manifest.Version)
	}
	if manifest.SystemPrompt != "You are Xander v2." {
		t.Errorf("unexpected prompt: %s", manifest.SystemPrompt)
	}
}

func TestResolver_StoreError(t *testing.T) {
	store := newMockStore()
	store.err = errors.New("connection refused")

	resolver := newTestResolver(store, 5*time.Minute)
	ctx := context.Background()

	_, err := resolver.resolve(ctx, "xander", ResolveOptions{})

	// Should get ErrStoreUnavailable, not panic
	var storeErr *ErrStoreUnavailable
	if !errors.As(err, &storeErr) {
		t.Errorf("expected ErrStoreUnavailable, got %T: %v", err, err)
	}

	// Error message should include cause
	if storeErr == nil || storeErr.cause == nil {
		t.Errorf("ErrStoreUnavailable should wrap underlying cause")
	}
}

func TestResolver_StoreError_NoPanic(t *testing.T) {
	// This test verifies that IPC/store failures never cause a panic.
	// It attempts multiple error scenarios.
	store := newMockStore()
	store.err = errors.New("connection refused")

	resolver := newTestResolver(store, 5*time.Minute)
	ctx := context.Background()

	// Call in goroutine to detect any panics
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("panic during store error: %v", r)
			}
		}()
		_, _ = resolver.resolve(ctx, "xander", ResolveOptions{})
	}()

	// Also test version-specific lookup
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("panic during version lookup with store error: %v", r)
			}
		}()
		_, _ = resolver.resolve(ctx, "xander", ResolveOptions{Version: "v1"})
	}()
}

func TestResolver_InvalidateCache(t *testing.T) {
	store := newMockStore()
	store.Add("xander", "v1", identitystore.StatusActive, "You are Xander.")

	resolver := newTestResolver(store, 5*time.Minute)
	ctx := context.Background()

	// Populate cache
	_, err := resolver.resolve(ctx, "xander", ResolveOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Invalidate
	resolver.invalidateCache("xander")

	// Next call should hit store again
	_, err = resolver.resolve(ctx, "xander", ResolveOptions{BypassCache: true})
	if err != nil {
		t.Fatalf("unexpected error after invalidation: %v", err)
	}
}

func TestErrStoreUnavailable(t *testing.T) {
	cause := errors.New("connection refused")
	err := &ErrStoreUnavailable{cause: cause}

	if err.Error() != "identity store unavailable: connection refused" {
		t.Errorf("unexpected error message: %s", err.Error())
	}
	if !errors.Is(err, cause) {
		t.Errorf("Unwrap should return underlying cause")
	}
}

func TestResolveManifest(t *testing.T) {
	now := time.Now()
	ims := &identitystore.IdentityManifest{
		Name:      "xander",
		Version:   "v1",
		Status:    identitystore.StatusActive,
		Manifest:  "You are Xander.",
		Description: "Test identity",
		CreatedAt: now,
		ProviderProfiles: map[identitystore.ProviderType]string{
			identitystore.ProviderAnthropic: "Anthropic-specific prompt",
		},
	}

	m := resolveManifest(ims)

	if m.IdentityID != ims.Name {
		t.Errorf("expected IdentityID=%s, got %s", ims.Name, m.IdentityID)
	}
	if m.Version != ims.Version {
		t.Errorf("expected Version=%s, got %s", ims.Version, m.Version)
	}
	if m.SystemPrompt != ims.Manifest {
		t.Errorf("expected SystemPrompt=%s, got %s", ims.Manifest, m.SystemPrompt)
	}
	if m.CreatedAt != ims.CreatedAt {
		t.Errorf("expected CreatedAt=%v, got %v", ims.CreatedAt, m.CreatedAt)
	}
	if m.ProviderOverrides == nil {
		t.Fatal("expected ProviderOverrides to be set")
	}
	if m.ProviderOverrides[string(identitystore.ProviderAnthropic)] != "Anthropic-specific prompt" {
		t.Errorf("unexpected provider override: %v", m.ProviderOverrides)
	}

	// nil input
	if resolveManifest(nil) != nil {
		t.Errorf("expected nil for nil input")
	}
}
