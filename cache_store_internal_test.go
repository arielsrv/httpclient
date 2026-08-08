package httpclient

// White-box tests for the gocache adapter. They drive it with a fake store so the
// value-shape normalization and the miss/error split are covered without needing
// a real backend — see cache_backends_test.go for the container-backed runs.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/eko/gocache/lib/v4/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeStore is a store.StoreInterface whose Get and Set are scripted per test.
type fakeStore struct {
	value    any
	getErr   error
	setErr   error
	lastKey  any
	lastCost int64
	lastTTL  time.Duration
}

func (r *fakeStore) Get(_ context.Context, _ any) (any, error) {
	return r.value, r.getErr
}

func (r *fakeStore) GetWithTTL(_ context.Context, _ any) (any, time.Duration, error) {
	return r.value, 0, r.getErr
}

func (r *fakeStore) Set(_ context.Context, key any, value any, options ...store.Option) error {
	opts := store.ApplyOptions(options...)
	r.lastKey = key
	r.lastCost = opts.Cost
	r.lastTTL = opts.Expiration
	r.value = value
	return r.setErr
}

func (r *fakeStore) Delete(_ context.Context, _ any) error                           { return nil }
func (r *fakeStore) Invalidate(_ context.Context, _ ...store.InvalidateOption) error { return nil }
func (r *fakeStore) Clear(_ context.Context) error                                   { return nil }

func (r *fakeStore) GetType() string { return "fake" }

func TestStoreCache_GetNormalizesValueShapes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		stored    any
		name      string
		want      []byte
		wantFound bool
	}{
		{
			name:      "bytes as Memcached returns them",
			stored:    []byte("payload"),
			want:      []byte("payload"),
			wantFound: true,
		},
		{
			name:      "string as Redis returns it",
			stored:    "payload",
			want:      []byte("payload"),
			wantFound: true,
		},
		{name: "foreign shape counts as a miss", stored: 42, want: nil, wantFound: false},
		{name: "nil counts as a miss", stored: nil, want: nil, wantFound: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			subject := newStoreCache(&fakeStore{value: tc.stored})

			got, found, err := subject.Get(context.Background(), "k")
			require.NoError(t, err)
			assert.Equal(t, tc.wantFound, found)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestStoreCache_GetMissAndError(t *testing.T) {
	t.Parallel()

	t.Run("NotFound is a miss, not an error", func(t *testing.T) {
		t.Parallel()
		subject := newStoreCache(&fakeStore{
			getErr: store.NotFoundWithCause(errors.New("nothing there")),
		})

		value, found, err := subject.Get(context.Background(), "k")
		require.NoError(t, err)
		assert.False(t, found)
		assert.Nil(t, value)
	})

	t.Run("any other failure is reported", func(t *testing.T) {
		t.Parallel()
		subject := newStoreCache(&fakeStore{getErr: errors.New("connection refused")})

		_, found, err := subject.Get(context.Background(), "k")
		require.Error(t, err)
		assert.False(t, found)
		assert.Contains(t, err.Error(), "cache get")
	})
}

func TestStoreCache_SetPassesCostAndTTL(t *testing.T) {
	t.Parallel()
	payload := []byte("0123456789")

	t.Run("cost is the payload size", func(t *testing.T) {
		t.Parallel()
		backend := &fakeStore{}
		require.NoError(
			t,
			newStoreCache(backend).Set(context.Background(), "k", payload, time.Minute),
		)

		assert.Equal(t, "k", backend.lastKey)
		assert.Equal(t, int64(len(payload)), backend.lastCost)
		assert.Equal(t, time.Minute, backend.lastTTL)
	})

	t.Run("a zero or absent TTL means no expiration", func(t *testing.T) {
		t.Parallel()
		backend := &fakeStore{}
		require.NoError(t, newStoreCache(backend).Set(context.Background(), "k", payload))
		assert.Zero(t, backend.lastTTL)

		require.NoError(t, newStoreCache(backend).Set(context.Background(), "k", payload, 0))
		assert.Zero(t, backend.lastTTL)
	})

	t.Run("a backend failure is wrapped", func(t *testing.T) {
		t.Parallel()
		backend := &fakeStore{setErr: errors.New("disk full")}

		err := newStoreCache(backend).Set(context.Background(), "k", payload)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cache set")
	})
}

// TestKVS_CorruptEntryIsAMiss verifies that data we did not write — say another
// application sharing the Redis instance — degrades to a miss instead of an error.
func TestKVS_CorruptEntryIsAMiss(t *testing.T) {
	t.Parallel()
	backend := &fakeStore{value: []byte("not json at all")}
	kvs := &KVS{cache: newStoreCache(backend)}

	entry, err := kvs.Get(context.Background(), "k")
	require.ErrorIs(t, err, ErrCacheMiss)
	assert.Nil(t, entry)
}

// TestKVS_RoundTrip verifies an entry survives serialization intact, binary body
// and headers included.
func TestKVS_RoundTrip(t *testing.T) {
	t.Parallel()
	backend := &fakeStore{}
	kvs := &KVS{cache: newStoreCache(backend)}
	original := cachedResponse{
		StatusCode: 206,
		Headers:    map[string][]string{"Content-Type": {"application/octet-stream"}},
		Body:       []byte{0x00, 0xFF, 0xFE, 0x89},
	}

	require.NoError(t, kvs.Set(context.Background(), "k", original, time.Minute))

	got, err := kvs.Get(context.Background(), "k")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, original, *got)
}

func TestRedisConfig_Address(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		want   string
		config RedisConfig
	}{
		{name: "zero value falls back to localhost", config: RedisConfig{}, want: "localhost:6379"},
		{
			name:   "host only keeps the default port",
			config: RedisConfig{Host: "cache.internal"},
			want:   "cache.internal:6379",
		},
		{
			name:   "port only keeps the default host",
			config: RedisConfig{Port: 6380},
			want:   "localhost:6380",
		},
		{
			name:   "both set",
			config: RedisConfig{Host: "10.0.0.5", Port: 6390},
			want:   "10.0.0.5:6390",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.config.address())
		})
	}
}

func TestMemcachedConfig_Servers(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		want   []string
		config MemcachedConfig
	}{
		{
			name:   "zero value falls back to localhost",
			config: MemcachedConfig{},
			want:   []string{"localhost:11211"},
		},
		{
			name:   "host and port",
			config: MemcachedConfig{Host: "cache.internal", Port: 11212},
			want:   []string{"cache.internal:11212"},
		},
		{
			name: "explicit servers win over host and port",
			config: MemcachedConfig{
				Host:    "ignored",
				Port:    1,
				Servers: []string{"a:11211", "b:11211"},
			},
			want: []string{"a:11211", "b:11211"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.config.servers())
		})
	}
}

// TestNewInMemoryClientCache_DefaultsToAByteBudget verifies a zero-valued config
// still produces a usable, bounded cache.
func TestNewInMemoryClientCache_DefaultsToAByteBudget(t *testing.T) {
	t.Parallel()
	subject, err := NewInMemoryClientCache(InMemoryConfig{})
	require.NoError(t, err)
	t.Cleanup(subject.Close)

	require.NoError(t, subject.Set(context.Background(), "k", []byte("v")))
	value, found, err := subject.Get(context.Background(), "k")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, []byte("v"), value)
}
