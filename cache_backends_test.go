package httpclient_test

// Integration tests for the distributed cache backends. Each one starts a real
// server with testcontainers, so they need a working Docker daemon and are
// skipped (not failed) when there is none, and under `go test -short`.

import (
	"context"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/arielsrv/httpclient"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	redisImage     = "redis:7-alpine"
	memcachedImage = "memcached:1.6-alpine"
	containerBoot  = 2 * time.Minute
)

// hostPort resolves the host and mapped port a container is reachable at.
func hostPort(
	ctx context.Context,
	t *testing.T,
	container testcontainers.Container,
	port string,
) (string, int) {
	t.Helper()
	host, err := container.Host(ctx)
	require.NoError(t, err)
	mapped, err := container.MappedPort(ctx, port)
	require.NoError(t, err)
	number, err := strconv.Atoi(mapped.Port())
	require.NoError(t, err)
	return host, number
}

// skipWithoutDocker turns "no Docker available" into a skip: these tests are
// about the backends, not about the machine running them.
func skipWithoutDocker(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	if provider, provErr := testcontainers.ProviderDocker.GetProvider(); provErr != nil ||
		provider.Health(context.Background()) != nil {
		t.Skipf("docker is not available: %v", err)
	}
	require.NoError(t, err)
}

func startRedis(ctx context.Context, t *testing.T) (string, int) {
	t.Helper()
	container, err := tcredis.Run(ctx, redisImage)
	skipWithoutDocker(t, err)
	require.NotNil(t, container)
	t.Cleanup(func() {
		require.NoError(t, testcontainers.TerminateContainer(container))
	})
	return hostPort(ctx, t, container, "6379/tcp")
}

func startMemcached(ctx context.Context, t *testing.T) (string, int) {
	t.Helper()
	container, err := testcontainers.Run(ctx, memcachedImage,
		testcontainers.WithExposedPorts("11211/tcp"),
		testcontainers.WithWaitStrategyAndDeadline(
			containerBoot,
			wait.ForListeningPort("11211/tcp"),
		),
	)
	skipWithoutDocker(t, err)
	require.NotNil(t, container)
	t.Cleanup(func() {
		require.NoError(t, testcontainers.TerminateContainer(container))
	})
	return hostPort(ctx, t, container, "11211/tcp")
}

// --- Backend contract ---

// backendContract is the behavior every Cache implementation must exhibit,
// exercised identically against Redis and Memcached.
func backendContract(t *testing.T, cache httpclient.Cache) {
	t.Helper()
	ctx := context.Background()

	t.Run("missing key is a miss, not an error", func(t *testing.T) {
		value, found, err := cache.Get(ctx, "absent-"+t.Name())
		require.NoError(t, err)
		assert.False(t, found)
		assert.Nil(t, value)
	})

	t.Run("round-trips bytes verbatim", func(t *testing.T) {
		// Non-UTF8 bytes catch backends that hand values back as strings.
		payload := []byte{0x00, 0x89, 0x50, 0x4E, 0xFF, 0xFE}
		key := "binary-" + t.Name()

		require.NoError(t, cache.Set(ctx, key, payload, time.Minute))

		got, found, err := cache.Get(ctx, key)
		require.NoError(t, err)
		require.True(t, found)
		assert.Equal(t, payload, got)
	})

	t.Run("honors the TTL", func(t *testing.T) {
		key := "ttl-" + t.Name()
		require.NoError(t, cache.Set(ctx, key, []byte("v"), time.Second))

		_, found, err := cache.Get(ctx, key)
		require.NoError(t, err)
		require.True(t, found)

		assert.Eventually(t, func() bool {
			_, stillThere, getErr := cache.Get(ctx, key)
			return getErr == nil && !stillThere
		}, 5*time.Second, 200*time.Millisecond, "entry should have expired")
	})
}

// --- Redis ---

func TestRedisClientCache(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping container-backed test in short mode")
	}
	t.Parallel()
	ctx := context.Background()
	host, port := startRedis(ctx, t)

	cache := httpclient.NewRedisClientCache(httpclient.RedisConfig{Host: host, Port: port})
	t.Cleanup(func() { require.NoError(t, cache.Close()) })
	require.NoError(t, cache.Ping(ctx))

	backendContract(t, cache)
}

// TestRedisClientCache_ServesHTTPResponses is the end-to-end path: a response
// cached in Redis must come back decoded, without touching the origin again.
func TestRedisClientCache_ServesHTTPResponses(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping container-backed test in short mode")
	}
	t.Parallel()
	ctx := context.Background()
	host, port := startRedis(ctx, t)

	cache := httpclient.NewRedisClientCache(httpclient.RedisConfig{Host: host, Port: port})
	t.Cleanup(func() { require.NoError(t, cache.Close()) })

	assertServesFromCache(t, cache)
}

func TestRedisClientCache_UnreachableDegradesToNetwork(t *testing.T) {
	t.Parallel()
	// Port 1 has nothing listening, so every cache operation fails.
	cache := httpclient.NewRedisClientCache(httpclient.RedisConfig{Host: "127.0.0.1", Port: 1})
	t.Cleanup(func() { require.NoError(t, cache.Close()) })

	var hits atomic.Int64
	server := countingServer(&hits, testUser{ID: 20, Name: "Nora"}, nil)
	defer server.Close()

	client := httpclient.NewHTTPClient(httpclient.WithCache(cache, 2))
	defer client.Close()

	for range 2 {
		resp, err := client.Get[testUser](context.Background(), server.URL)
		require.NoError(t, err, "an unreachable cache must not break the request")
		assert.Equal(t, "Nora", resp.Data().Name)
	}

	assert.Equal(t, int64(2), hits.Load(), "every request should have gone to the origin")
}

// --- Memcached ---

func TestMemcachedClientCache(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping container-backed test in short mode")
	}
	t.Parallel()
	ctx := context.Background()
	host, port := startMemcached(ctx, t)

	cache := httpclient.NewMemcachedClientCache(httpclient.MemcachedConfig{Host: host, Port: port})
	t.Cleanup(func() { require.NoError(t, cache.Close()) })
	require.NoError(t, cache.Ping())

	backendContract(t, cache)
}

func TestMemcachedClientCache_ServesHTTPResponses(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping container-backed test in short mode")
	}
	t.Parallel()
	ctx := context.Background()
	host, port := startMemcached(ctx, t)

	cache := httpclient.NewMemcachedClientCache(httpclient.MemcachedConfig{Host: host, Port: port})
	t.Cleanup(func() { require.NoError(t, cache.Close()) })

	assertServesFromCache(t, cache)
}

// assertServesFromCache drives a client through two identical GETs and asserts
// the second one was answered by cache rather than by the origin.
func assertServesFromCache(t *testing.T, cache httpclient.Cache) {
	t.Helper()
	var hits atomic.Int64
	server := countingServer(&hits, testUser{ID: 21, Name: "Omar"}, map[string]string{
		"Cache-Control": "max-age=60",
	})
	defer server.Close()

	client := httpclient.NewHTTPClient(httpclient.WithCache(cache, 2))
	defer client.Close()

	first, err := client.Get[testUser](context.Background(), server.URL)
	require.NoError(t, err)
	require.Equal(t, "Omar", first.Data().Name)

	second, err := client.Get[testUser](context.Background(), server.URL)
	require.NoError(t, err)
	assert.Equal(t, "Omar", second.Data().Name, "cached data must decode into T")
	assert.Equal(t, http.StatusOK, second.StatusCode(), "cached status must survive")
	assert.Equal(t, "max-age=60", second.Headers().Get("Cache-Control"),
		"cached headers must survive")
	assert.Equal(t, int64(1), hits.Load(), "second GET should be served from cache")
}
