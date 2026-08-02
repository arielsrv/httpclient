package httpclient

import (
	"context"
	"errors"
	"net/http"
	"time"
)

type KVS struct {
	cache Cache
}

func (r *KVS) Set[T any](ctx context.Context, key string, entry T, ttl ...time.Duration) error {
	return r.cache.Set(key, entry, ttl...)
}

func (r *KVS) Get[T any](ctx context.Context, key string) (*T, bool, error) {
	result, found, err := r.cache.Get(key)
	if err != nil {
		return new(T), false, err
	}

	if !found {
		return new(T), false, nil
	}

	value, ok := result.(T)
	if !ok {
		return new(T), false, errors.New("value is not HTTPResponse")
	}

	return &value, found, nil
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
	Set(key string, value any, ttl ...time.Duration) error
	Get(key string) (any, bool, error)
}

type InMemoryClientCache struct {
	m      map[string]any
	config InMemoryConfig
}

func NewInMemoryClientCache(config InMemoryConfig) *InMemoryClientCache {
	return &InMemoryClientCache{
		config: config,
		m:      make(map[string]any),
	}
}

func (r InMemoryClientCache) Set(key string, value any, ttl ...time.Duration) error {
	r.m[key] = value
	return nil
}

func (r InMemoryClientCache) Get(key string) (any, bool, error) {
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
