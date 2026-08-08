package httpclient

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"
)

type KVS struct {
	cache Cache
}

func (r *KVS) Set[T any](ctx context.Context, key string, entry T, ttl ...time.Duration) error {
	return r.cache.Set(ctx, key, entry, ttl...)
}

// ErrCacheMiss reports that a key is absent from the cache. It is an expected
// outcome, not a failure: callers should fall back to the network.
var ErrCacheMiss = errors.New("cache miss")

// Get returns the cached entry, or ErrCacheMiss when the key is absent. It never
// returns a usable value together with an error — a zero T is not a cache hit.
func (r *KVS) Get[T any](ctx context.Context, key string) (*T, error) {
	result, found, err := r.cache.Get(ctx, key)
	if err != nil {
		return nil, err
	}

	if !found {
		return nil, ErrCacheMiss
	}

	value, ok := result.(T)
	if !ok {
		return nil, errors.New("value is not HTTPResponse")
	}

	return &value, nil
}

type HTTPKeyGenerator interface {
	Generate(method, url string, headers ...http.Header) string
}

type defaultCacheKeyGenerator struct{}

func (defaultCacheKeyGenerator) Generate(method, rawURL string, headers ...http.Header) string {
	return rawURL
}

type CacheEntry struct {
	revalidate bool
}

func (r CacheEntry) Revalidate() bool {
	return r.revalidate
}

type Cache interface {
	Set(ctx context.Context, key string, value any, ttl ...time.Duration) error
	Get(ctx context.Context, key string) (any, bool, error)
}

// inMemoryEntry is a stored value with its expiry. A zero expiresAt never expires.
type inMemoryEntry struct {
	expiresAt time.Time
	value     any
}

// InMemoryClientCache is a process-local Cache. Entries are written from the
// client's background pool, so access is guarded by a mutex. Expired entries are
// dropped lazily on read.
type InMemoryClientCache struct {
	m      map[string]inMemoryEntry
	config InMemoryConfig
	mu     sync.Mutex
}

func NewInMemoryClientCache(config InMemoryConfig) *InMemoryClientCache {
	return &InMemoryClientCache{
		config: config,
		m:      make(map[string]inMemoryEntry),
	}
}

// Set stores value under key. The first ttl, when positive, bounds its lifetime.
func (r *InMemoryClientCache) Set(
	ctx context.Context,
	key string,
	value any,
	ttl ...time.Duration,
) error {
	entry := inMemoryEntry{value: value}
	if len(ttl) > 0 && ttl[0] > 0 {
		entry.expiresAt = time.Now().Add(ttl[0])
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.m[key] = entry
	return nil
}

// Get reports a miss for entries whose TTL has elapsed, evicting them on the way.
func (r *InMemoryClientCache) Get(ctx context.Context, key string) (any, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, found := r.m[key]
	if !found {
		return nil, false, nil
	}
	if !entry.expiresAt.IsZero() && time.Now().After(entry.expiresAt) {
		delete(r.m, key)
		return nil, false, nil
	}

	return entry.value, true, nil
}

type RedisConfig struct {
	Host string
	Port int
}

type MemcachedConfig struct {
	Host string
	Port int
}

type InMemoryConfig struct {
	ByteSize int64
}
