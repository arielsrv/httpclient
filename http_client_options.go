package httpclient

import (
	"net/http"
	"time"
)

type ClientOption func(*HTTPClient)

func WithConnectionPool(pool *ConnectionPool) ClientOption {
	return func(c *HTTPClient) {
		c.lowLevelClient.Transport = pool.transport
	}
}

// WithTransport sets a custom RoundTripper. Useful for testing and advanced use cases.
func WithTransport(rt http.RoundTripper) ClientOption {
	return func(c *HTTPClient) {
		c.lowLevelClient.Transport = rt
	}
}

// WithTimeout bounds the whole request (dial + body read). Overrides DefaultTimeout.
// Pass 0 to disable the client-level timeout and rely only on the context deadline.
func WithTimeout(timeout time.Duration) ClientOption {
	return func(c *HTTPClient) {
		c.lowLevelClient.Timeout = timeout
	}
}

func WithCache(cache Cache, concurrencyLevel int) ClientOption {
	return func(c *HTTPClient) {
		c.kvs = &KVS{
			cache: cache,
		}
		c.concurrencyLevel = concurrencyLevel
	}
}

// WithDefaultCacheTTL sets how long responses that carry no freshness information
// are cached. Explicit Cache-Control max-age and Expires always win over it.
// Pass 0 to cache only what the server explicitly marks as cacheable.
func WithDefaultCacheTTL(ttl time.Duration) ClientOption {
	return func(c *HTTPClient) {
		c.defaultCacheTTL = ttl
	}
}

// WithCacheRevalidationWindow sets how long a response is kept past its freshness
// so it can be confirmed with a conditional request (If-None-Match /
// If-Modified-Since) rather than downloaded again. Pass 0 to drop entries as soon
// as they go stale, which disables conditional revalidation.
func WithCacheRevalidationWindow(window time.Duration) ClientOption {
	return func(c *HTTPClient) {
		c.revalidationWindow = window
	}
}

// WithCacheKeyGenerator replaces how cache keys are derived from a request.
// The default keys on the method, the URL and the request headers that select a
// representation — Authorization and Cookie among them, so that one caller's
// response is never served to another.
func WithCacheKeyGenerator(generator HTTPKeyGenerator) ClientOption {
	return func(c *HTTPClient) {
		if generator != nil {
			c.httpKeyGenerator = generator
		}
	}
}

// WithFollowRedirects controls whether the client follows HTTP redirects (3xx).
// By default, the standard library follows up to 10 redirects automatically.
// Pass false to disable redirect following entirely — the first 3xx response is
// returned as-is so the caller can inspect Location and decide what to do.
func WithFollowRedirects(follow bool) ClientOption {
	return func(c *HTTPClient) {
		if !follow {
			c.lowLevelClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			}
		} else {
			c.lowLevelClient.CheckRedirect = nil // restore default
		}
	}
}
