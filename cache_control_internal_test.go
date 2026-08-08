package httpclient

// White-box tests for Cache-Control parsing and freshness computation.
// They reach unexported helpers, so they live in the internal test package.

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestParseCacheControl(t *testing.T) {
	t.Parallel()
	cases := []struct {
		want   cacheControl
		name   string
		header string
	}{
		{name: "empty header", header: "", want: cacheControl{}},
		{name: "single flag", header: "no-store", want: cacheControl{"no-store": ""}},
		{
			name:   "flag and value",
			header: "public, max-age=60",
			want:   cacheControl{"public": "", "max-age": "60"},
		},
		{
			name:   "extra whitespace and mixed case",
			header: "  No-Cache ,  Max-Age = 30 ",
			want:   cacheControl{"no-cache": "", "max-age": "30"},
		},
		{
			name:   "quoted value",
			header: `private="set-cookie"`,
			want:   cacheControl{"private": "set-cookie"},
		},
		{
			name:   "empty elements are skipped",
			header: "max-age=5,,",
			want:   cacheControl{"max-age": "5"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, parseCacheControl(tc.header))
		})
	}
}

func TestCacheControl_Duration(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		header    string
		want      time.Duration
		wantFound bool
	}{
		{name: "valid", header: "max-age=60", want: time.Minute, wantFound: true},
		{name: "zero", header: "max-age=0", want: 0, wantFound: true},
		{name: "absent", header: "no-store", want: 0, wantFound: false},
		{name: "not a number", header: "max-age=soon", want: 0, wantFound: false},
		{name: "negative", header: "max-age=-5", want: 0, wantFound: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, found := parseCacheControl(tc.header).duration(directiveMaxAge)
			assert.Equal(t, tc.wantFound, found)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestFreshnessFromExpires(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC().Truncate(time.Second)

	t.Run("relative to Date", func(t *testing.T) {
		t.Parallel()
		headers := http.Header{
			dateHeader:    []string{now.Format(http.TimeFormat)},
			expiresHeader: []string{now.Add(2 * time.Minute).Format(http.TimeFormat)},
		}
		ttl, found := freshnessFromExpires(headers)
		assert.True(t, found)
		assert.Equal(t, 2*time.Minute, ttl)
	})

	t.Run("without Date falls back to now", func(t *testing.T) {
		t.Parallel()
		headers := http.Header{
			expiresHeader: []string{time.Now().Add(time.Hour).UTC().Format(http.TimeFormat)},
		}
		ttl, found := freshnessFromExpires(headers)
		assert.True(t, found)
		assert.Positive(t, ttl)
	})

	cases := []struct {
		name    string
		headers http.Header
	}{
		{name: "absent", headers: http.Header{}},
		{name: "unparsable", headers: http.Header{expiresHeader: []string{"nope"}}},
		{
			name: "already expired",
			headers: http.Header{
				dateHeader:    []string{now.Format(http.TimeFormat)},
				expiresHeader: []string{now.Add(-time.Minute).Format(http.TimeFormat)},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ttl, found := freshnessFromExpires(tc.headers)
			assert.False(t, found)
			assert.Zero(t, ttl)
		})
	}
}

// TestCacheable_NilRawResponse covers the defensive branch for a manually-built
// HTTPResponse, unreachable through doRequest.
func TestCacheable_NilRawResponse(t *testing.T) {
	t.Parallel()
	resp := HTTPResponse[any]{}

	ttl, cacheable := resp.Cacheable(time.Minute)
	assert.False(t, cacheable)
	assert.Zero(t, ttl)
	assert.True(t, resp.Revalidate(), "an entry we cannot inspect must be revalidated")
}
