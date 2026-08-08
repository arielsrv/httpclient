package httpclient

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"maps"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/bradfitz/gomemcache/memcache"
	"github.com/dgraph-io/ristretto/v2"
	"github.com/eko/gocache/lib/v4/cache"
	"github.com/eko/gocache/lib/v4/store"
	memcachestore "github.com/eko/gocache/store/memcache/v4"
	redisstore "github.com/eko/gocache/store/redis/v4"
	ristrettostore "github.com/eko/gocache/store/ristretto/v4"
	"github.com/redis/go-redis/v9"
)

// ErrCacheMiss reports that a key is absent from the cache. It is an expected
// outcome, not a failure: callers fall back to the network.
var ErrCacheMiss = errors.New("cache miss")

// Cache is the storage backend behind the HTTP cache. Values are opaque bytes so
// that the same contract serves an in-process store and a distributed one.
//
// Implementations must report a missing key as (nil, false, nil), never as an
// error. The client treats every other failure as a miss too, so a degraded
// backend slows requests down instead of breaking them.
type Cache interface {
	Set(ctx context.Context, key string, value []byte, ttl ...time.Duration) error
	Get(ctx context.Context, key string) ([]byte, bool, error)
}

// cachedResponse is the stored form of an HTTP response. Distributed backends
// only accept bytes, so an entry travels as JSON instead of holding on to a live
// [http.Response]. Body is base64-encoded by encoding/json, which keeps binary
// payloads intact.
//
// An entry outlives its freshness on purpose: once stale it can still answer a
// conditional request, turning a refetch into a 304 that costs no body transfer.
type cachedResponse struct {
	StoredAt   time.Time     `json:"stored_at"`
	Headers    http.Header   `json:"headers"`
	Body       []byte        `json:"body"`
	FreshFor   time.Duration `json:"fresh_for"`
	StatusCode int           `json:"status_code"`
}

// isFresh reports whether the entry is still within its freshness lifetime.
func (r cachedResponse) isFresh(now time.Time) bool {
	return now.Sub(r.StoredAt) < r.FreshFor
}

// mustRevalidate reports whether the entry may not be served as it stands:
// either it has gone stale, or the origin marked it no-cache.
func (r cachedResponse) mustRevalidate(now time.Time) bool {
	if parseCacheControl(r.Headers.Get(cacheControlHeader)).has(directiveNoCache) {
		return true
	}
	return !r.isFresh(now)
}

// validators returns the conditional headers that let the origin reply 304
// instead of resending the body. Empty when the response carried no validator,
// in which case a stale entry can only be refetched in full.
func (r cachedResponse) validators() http.Header {
	conditional := make(http.Header)
	if etag := r.Headers.Get(etagHeader); etag != "" {
		conditional.Set(ifNoneMatchHeader, etag)
	}
	if modified := r.Headers.Get(lastModifiedHeader); modified != "" {
		conditional.Set(ifModifiedSinceHeader, modified)
	}
	return conditional
}

// refreshedWith folds a 304 response into the entry: the body still stands, only
// its freshness and the headers the origin resent are updated (RFC 9111 §4.3.4).
func (r cachedResponse) refreshedWith(
	notModified http.Header,
	defaultTTL time.Duration,
	now time.Time,
) cachedResponse {
	headers := make(http.Header, len(r.Headers))
	maps.Copy(headers, r.Headers)
	maps.Copy(headers, notModified)

	refreshed := cachedResponse{
		StatusCode: r.StatusCode,
		Headers:    headers,
		Body:       r.Body,
		StoredAt:   now,
		FreshFor:   r.FreshFor,
	}
	if freshFor, storable := freshnessFor(headers, defaultTTL); storable {
		refreshed.FreshFor = freshFor
	}

	return refreshed
}

// KVS serializes cache entries on their way to and from a Cache backend.
type KVS struct {
	cache Cache
}

func (r *KVS) Set(ctx context.Context, key string, entry cachedResponse, ttl time.Duration) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("encoding cache entry: %w", err)
	}
	if setErr := r.cache.Set(ctx, key, data, ttl); setErr != nil {
		return fmt.Errorf("writing cache entry: %w", setErr)
	}
	return nil
}

// Get returns the stored entry, or ErrCacheMiss when the key is absent. An entry
// that cannot be decoded is reported as a miss as well: someone else's data under
// our key must not break the request.
func (r *KVS) Get(ctx context.Context, key string) (*cachedResponse, error) {
	data, found, err := r.cache.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("reading cache entry: %w", err)
	}
	if !found {
		return nil, ErrCacheMiss
	}

	var entry cachedResponse
	if unmarshalErr := json.Unmarshal(data, &entry); unmarshalErr != nil {
		return nil, ErrCacheMiss
	}

	return &entry, nil
}

// HTTPKeyGenerator derives the cache key of a request. Two requests that could
// legitimately receive different responses must map to different keys — the URL
// alone is not enough, since request headers select both which representation
// the origin returns and, with Authorization, whose it is.
type HTTPKeyGenerator interface {
	Generate(method, url string, headers ...http.Header) string
}

const (
	authorizationHeader  = "Authorization"
	cookieHeader         = "Cookie"
	acceptLanguageHeader = "Accept-Language"
	acceptEncodingHeader = "Accept-Encoding"
)

// keyVaryingHeaders are the request headers the default generator folds into the
// key. Accept* pick a representation; Authorization and Cookie pick an identity,
// and leaving those out would serve one caller's response to another — a real
// risk now that entries can live in a Redis shared across processes.
var keyVaryingHeaders = []string{
	authorizationHeader,
	cookieHeader,
	acceptHeader,
	acceptLanguageHeader,
	acceptEncodingHeader,
}

type defaultCacheKeyGenerator struct{}

// Generate keys on the method, the URL and the varying request headers. The
// varying part is hashed rather than spelled out so that credentials never
// surface in a key listing of a shared cache.
func (defaultCacheKeyGenerator) Generate(method, rawURL string, headers ...http.Header) string {
	digest := sha256.New()
	writeDigestField(digest, rawURL)

	for _, name := range keyVaryingHeaders {
		writeDigestField(digest, name)
		// The same header may be spread over several maps, so values are read in
		// argument order to keep the key stable.
		for _, header := range headers {
			for _, value := range header.Values(name) {
				writeDigestField(digest, value)
			}
		}
	}

	return "httpclient:" + method + ":" + hex.EncodeToString(digest.Sum(nil))
}

// writeDigestField appends a length-delimited field, so that concatenations such
// as ("ab", "c") and ("a", "bc") cannot collide.
func writeDigestField(digest hash.Hash, value string) {
	_, _ = fmt.Fprintf(digest, "%d:%s", len(value), value)
}

// --- Backends ---

// storeCache adapts a gocache store to the Cache interface. Every backend below
// embeds it, which is what keeps eko/gocache out of this package's exported API.
type storeCache struct {
	manager *cache.Cache[any]
}

func newStoreCache(backend store.StoreInterface) storeCache {
	// [cache.Cache] is instantiated with any rather than []byte on purpose: its
	// Get does a soft type assertion and silently yields the zero value when the
	// backend returns another shape — and backends do disagree (Redis answers
	// with a string, Memcached with bytes). Normalizing here keeps hits honest.
	return storeCache{manager: cache.New[any](backend)}
}

// Set stores value under key. The first ttl, when positive, bounds its lifetime.
// The entry's cost is its size in bytes, which is what makes a byte-denominated
// capacity such as [InMemoryConfig.ByteSize] meaningful.
func (r storeCache) Set(ctx context.Context, key string, value []byte, ttl ...time.Duration) error {
	options := []store.Option{store.WithCost(int64(len(value)))}
	if len(ttl) > 0 && ttl[0] > 0 {
		options = append(options, store.WithExpiration(ttl[0]))
	}

	if err := r.manager.Set(ctx, key, value, options...); err != nil {
		return fmt.Errorf("cache set: %w", err)
	}
	return nil
}

func (r storeCache) Get(ctx context.Context, key string) ([]byte, bool, error) {
	value, err := r.manager.Get(ctx, key)
	if err != nil {
		if errors.Is(err, &store.NotFound{}) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("cache get: %w", err)
	}

	data, ok := asBytes(value)
	if !ok {
		// An unexpected shape means the entry was not written by us. Report a
		// miss so the caller refetches and overwrites it.
		return nil, false, nil
	}

	return data, true, nil
}

// asBytes normalizes the value shapes the different backends hand back.
func asBytes(value any) ([]byte, bool) {
	switch typed := value.(type) {
	case []byte:
		return typed, true
	case string:
		return []byte(typed), true
	default:
		return nil, false
	}
}

// DefaultInMemoryByteSize is the capacity of an in-memory cache built from a
// zero-valued [InMemoryConfig].
const DefaultInMemoryByteSize int64 = 64 << 20 // 64 MiB

const (
	// ristrettoBufferItems is the per-goroutine access buffer size recommended by
	// the library.
	ristrettoBufferItems = 64
	// averageEntrySize is the entry size assumed when sizing the admission
	// counters. Ristretto wants roughly ten counters per expected entry.
	averageEntrySize = 1024
	// minRistrettoCounters keeps tiny caches from being sized down to nothing.
	minRistrettoCounters = 1000
)

// InMemoryConfig configures the in-process cache.
type InMemoryConfig struct {
	// ByteSize caps how much memory the cache may hold. Entries are admitted and
	// evicted by cost, and an entry costs its serialized size in bytes.
	// Defaults to DefaultInMemoryByteSize.
	ByteSize int64
}

// InMemoryClientCache is a process-local Cache backed by Ristretto. It is safe
// for concurrent use and evicts by cost once ByteSize is reached.
type InMemoryClientCache struct {
	storeCache

	client *ristretto.Cache[string, []byte]
}

func NewInMemoryClientCache(config InMemoryConfig) (*InMemoryClientCache, error) {
	maxCost := config.ByteSize
	if maxCost <= 0 {
		maxCost = DefaultInMemoryByteSize
	}

	client, err := ristretto.NewCache(&ristretto.Config[string, []byte]{
		NumCounters: max(minRistrettoCounters, 10*maxCost/averageEntrySize),
		MaxCost:     maxCost,
		BufferItems: ristrettoBufferItems,
	})
	if err != nil {
		return nil, fmt.Errorf("creating in-memory cache: %w", err)
	}

	// Ristretto buffers writes, so a read issued right after a write could miss
	// it. A synchronous set keeps the client's read-your-writes guarantee.
	backend := ristrettostore.NewRistretto(client, store.WithSynchronousSet())

	return &InMemoryClientCache{client: client, storeCache: newStoreCache(backend)}, nil
}

// Close releases the memory held by the cache.
func (r *InMemoryClientCache) Close() {
	r.client.Close()
}

// DefaultRedisPort is used when [RedisConfig.Port] is left at zero.
const DefaultRedisPort = 6379

// RedisConfig configures a Redis-backed cache.
type RedisConfig struct {
	// Host of the Redis server. Defaults to localhost.
	Host string
	// Password for AUTH. Empty means no authentication.
	Password string
	// Port of the Redis server. Defaults to DefaultRedisPort.
	Port int
	// DB index to select. Defaults to 0.
	DB int
}

func (r RedisConfig) address() string {
	host := r.Host
	if host == "" {
		host = "localhost"
	}
	port := r.Port
	if port == 0 {
		port = DefaultRedisPort
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

// RedisClientCache is a Cache backed by Redis, for entries shared across
// processes. Call Close when the client is done with it.
type RedisClientCache struct {
	storeCache

	client *redis.Client
}

func NewRedisClientCache(config RedisConfig) *RedisClientCache {
	client := redis.NewClient(&redis.Options{
		Addr:     config.address(),
		Password: config.Password,
		DB:       config.DB,
	})

	return &RedisClientCache{client: client, storeCache: newStoreCache(redisstore.NewRedis(client))}
}

// Ping checks that the server is reachable, so callers can fail fast at startup
// instead of degrading silently on the first request.
func (r *RedisClientCache) Ping(ctx context.Context) error {
	if err := r.client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("pinging redis: %w", err)
	}
	return nil
}

// Close releases the connection pool.
func (r *RedisClientCache) Close() error {
	if err := r.client.Close(); err != nil {
		return fmt.Errorf("closing redis client: %w", err)
	}
	return nil
}

// DefaultMemcachedPort is used when [MemcachedConfig.Port] is left at zero.
const DefaultMemcachedPort = 11211

// MemcachedConfig configures a Memcached-backed cache.
type MemcachedConfig struct {
	// Host of the server. Defaults to localhost. Ignored when Servers is set.
	Host string
	// Servers lists every node of a sharded setup, as host:port. When non-empty
	// it takes precedence over Host and Port.
	Servers []string
	// Port of the server. Defaults to DefaultMemcachedPort. Ignored when Servers
	// is set.
	Port int
}

func (r MemcachedConfig) servers() []string {
	if len(r.Servers) > 0 {
		return r.Servers
	}
	host := r.Host
	if host == "" {
		host = "localhost"
	}
	port := r.Port
	if port == 0 {
		port = DefaultMemcachedPort
	}
	return []string{net.JoinHostPort(host, strconv.Itoa(port))}
}

// MemcachedClientCache is a Cache backed by Memcached. Call Close when the client
// is done with it.
type MemcachedClientCache struct {
	storeCache

	client *memcache.Client
}

func NewMemcachedClientCache(config MemcachedConfig) *MemcachedClientCache {
	client := memcache.New(config.servers()...)

	return &MemcachedClientCache{
		client:     client,
		storeCache: newStoreCache(memcachestore.NewMemcache(client)),
	}
}

// Ping checks that at least one node is reachable.
func (r *MemcachedClientCache) Ping() error {
	if err := r.client.Ping(); err != nil {
		return fmt.Errorf("pinging memcached: %w", err)
	}
	return nil
}

// Close releases the open connections.
func (r *MemcachedClientCache) Close() error {
	if err := r.client.Close(); err != nil {
		return fmt.Errorf("closing memcached client: %w", err)
	}
	return nil
}
