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

// InMemoryClientCache is a process-local Cache. Entries are written from the
// client's background pool, so access is guarded by a mutex.
type InMemoryClientCache struct {
	m      map[string]any
	config InMemoryConfig
	mu     sync.RWMutex
}

func NewInMemoryClientCache(config InMemoryConfig) *InMemoryClientCache {
	return &InMemoryClientCache{
		config: config,
		m:      make(map[string]any),
	}
}

func (r *InMemoryClientCache) Set(
	ctx context.Context,
	key string,
	value any,
	ttl ...time.Duration,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m[key] = value
	return nil
}

func (r *InMemoryClientCache) Get(ctx context.Context, key string) (any, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	value, found := r.m[key]
	return value, found, nil
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
