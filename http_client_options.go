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
