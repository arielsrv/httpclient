package httpclient

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"slices"
	"time"

	"go.etcd.io/etcd/client/pkg/v3/types"
)

// DefaultTimeout bounds the whole request (dial + body read) unless overridden.
// Use WithTimeout(0) to disable it and rely solely on the context deadline.
const DefaultTimeout = 30 * time.Second

type HTTPClient struct {
	httpKeyGenerator HTTPKeyGenerator
	cacheableMethods types.Set
	kvs              *KVS
	pool             *Pool
	client           http.Client
	concurrencyLevel int
}

type ClientOption func(*HTTPClient)

func WithConnectionPool(pool *ConnectionPool) ClientOption {
	return func(c *HTTPClient) {
		c.client.Transport = pool.transport
	}
}

// WithTransport sets a custom RoundTripper. Useful for testing and advanced use cases.
func WithTransport(rt http.RoundTripper) ClientOption {
	return func(c *HTTPClient) {
		c.client.Transport = rt
	}
}

// WithTimeout bounds the whole request (dial + body read). Overrides DefaultTimeout.
// Pass 0 to disable the client-level timeout and rely only on the context deadline.
func WithTimeout(timeout time.Duration) ClientOption {
	return func(c *HTTPClient) {
		c.client.Timeout = timeout
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
			c.client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			}
		} else {
			c.client.CheckRedirect = nil // restore default
		}
	}
}

func NewHTTPClient(opts ...ClientOption) *HTTPClient {
	httpClient := &HTTPClient{
		client: http.Client{
			Transport: NewConnectionPool().transport,
			Timeout:   DefaultTimeout,
		},
		httpKeyGenerator: defaultCacheKeyGenerator{},
		pool:             &Pool{ConcurrencyLevel: runtime.NumCPU() - 1},
		cacheableMethods: types.NewUnsafeSet(
			http.MethodGet,
			http.MethodHead,
			http.MethodOptions,
		),
	}
	for opt := range slices.Values(opts) {
		opt(httpClient)
	}
	return httpClient
}

func (r *HTTPClient) Get[T any](
	ctx context.Context,
	url string,
	headers ...http.Header,
) (*HTTPResponse[T], error) {
	return r.doRequest[T](ctx, http.MethodGet, url, Body{}, headers...)
}

// Download fetches url and returns the raw response bytes. It is a convenience
// wrapper around Get[[]byte] with AcceptBinary() — no codec is involved.
func (r *HTTPClient) Download(
	ctx context.Context,
	url string,
	headers ...http.Header,
) (*HTTPResponse[[]byte], error) {
	return r.Get[[]byte](ctx, url, append(headers, AcceptBinary())...)
}

// DownloadAsync fires a Download in a goroutine and returns a Future immediately.
func (r *HTTPClient) DownloadAsync(
	ctx context.Context,
	url string,
	headers ...http.Header,
) *Future[[]byte] {
	return async(func() (*HTTPResponse[[]byte], error) { return r.Download(ctx, url, headers...) })
}

// Post encodes payload using the codec matching the Content-Type header (defaults
// to application/json) and decodes the response into T.
func (r *HTTPClient) Post[T any](
	ctx context.Context,
	url string,
	payload any,
	headers ...http.Header,
) (*HTTPResponse[T], error) {
	return r.doRequest[T](ctx, http.MethodPost, url, bodyFromHeaders(payload, headers), headers...)
}

// Put encodes payload using the codec matching the Content-Type header (defaults
// to application/json) and decodes the response into T.
func (r *HTTPClient) Put[T any](
	ctx context.Context,
	url string,
	payload any,
	headers ...http.Header,
) (*HTTPResponse[T], error) {
	return r.doRequest[T](ctx, http.MethodPut, url, bodyFromHeaders(payload, headers), headers...)
}

// Patch encodes payload using the codec matching the Content-Type header (defaults
// to application/json) and decodes the response into T.
func (r *HTTPClient) Patch[T any](
	ctx context.Context,
	url string,
	payload any,
	headers ...http.Header,
) (*HTTPResponse[T], error) {
	return r.doRequest[T](ctx, http.MethodPatch, url, bodyFromHeaders(payload, headers), headers...)
}

// Delete sends a DELETE request (no body) and decodes the response into T.
func (r *HTTPClient) Delete[T any](
	ctx context.Context,
	url string,
	headers ...http.Header,
) (*HTTPResponse[T], error) {
	return r.doRequest[T](ctx, http.MethodDelete, url, Body{}, headers...)
}

// GetAsync fires a GET request in a goroutine and returns a Future immediately.
// The context controls cancellation — cancelling ctx will unblock Await with an error.
func (r *HTTPClient) GetAsync[T any](
	ctx context.Context,
	url string,
	headers ...http.Header,
) *Future[T] {
	return async(func() (*HTTPResponse[T], error) { return r.Get[T](ctx, url, headers...) })
}

// PostAsync fires a POST request in a goroutine and returns a Future immediately.
func (r *HTTPClient) PostAsync[T any](
	ctx context.Context,
	url string,
	payload any,
	headers ...http.Header,
) *Future[T] {
	return async(
		func() (*HTTPResponse[T], error) { return r.Post[T](ctx, url, payload, headers...) },
	)
}

// PutAsync fires a PUT request in a goroutine and returns a Future immediately.
func (r *HTTPClient) PutAsync[T any](
	ctx context.Context,
	url string,
	payload any,
	headers ...http.Header,
) *Future[T] {
	return async(
		func() (*HTTPResponse[T], error) { return r.Put[T](ctx, url, payload, headers...) },
	)
}

// PatchAsync fires a PATCH request in a goroutine and returns a Future immediately.
func (r *HTTPClient) PatchAsync[T any](
	ctx context.Context,
	url string,
	payload any,
	headers ...http.Header,
) *Future[T] {
	return async(
		func() (*HTTPResponse[T], error) { return r.Patch[T](ctx, url, payload, headers...) },
	)
}

// DeleteAsync fires a DELETE request in a goroutine and returns a Future immediately.
func (r *HTTPClient) DeleteAsync[T any](
	ctx context.Context,
	url string,
	headers ...http.Header,
) *Future[T] {
	return async(func() (*HTTPResponse[T], error) { return r.Delete[T](ctx, url, headers...) })
}

func (r *HTTPClient) doRequest[T any](
	ctx context.Context,
	method string,
	url string,
	body Body,
	headers ...http.Header,
) (*HTTPResponse[T], error) {
	if r.cacheableMethods.Contains(method) && r.cacheEnabled() {
		value, found, err := r.kvs.Get[HTTPResponse[T]](ctx, url)
		if err != nil {
			return nil, fmt.Errorf("getting cached response: %w", err)
		}
		if found && !value.Revalidate() {
			return value, nil
		}
	}

	request, err := r.newRequest(ctx, method, url, body, headers...)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	response, doErr := r.client.Do(request)
	if doErr != nil {
		return nil, fmt.Errorf("network error: %w", doErr)
	}
	if response == nil {
		return nil, fmt.Errorf("network error: nil response")
	}

	bodyBytes, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()

	if readErr != nil {
		return nil, fmt.Errorf("reading response body: %w", readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("closing response body: %w", closeErr)
	}

	// Prefer the Accept header (caller intent) for codec selection — this lets
	// AcceptBinary() force byteCodec even when the server responds with a
	// content type not in the registry (image/x-icon, image/png, etc.).
	// Fall back to the response Content-Type for standard negotiation.
	codec := codecForAcceptHeader(headers)
	if codec == nil {
		codec = codecForContentType(response.Header.Get("Content-Type"))
	}

	httpResponse := HTTPResponse[T]{
		raw:       response,
		bodyBytes: bodyBytes,
		codec:     codec,
	}

	if httpResponse.IsSuccess() && len(bodyBytes) > 0 {
		if unmarshalErr := codec.Unmarshal(bodyBytes, &httpResponse.data); unmarshalErr != nil {
			return nil, fmt.Errorf("deserializing response: %w", unmarshalErr)
		}
	}

	if r.cacheableMethods.Contains(request.Method) && r.cacheEnabled() {
		if ttl, cacheable := httpResponse.Cacheable(); cacheable {
			r.pool.submit(func() {
				err = r.kvs.Set(ctx, request.URL.String(), httpResponse, ttl)
				if err != nil {
					return
				}
			})
		}
	}

	return &httpResponse, nil
}

// newRequest creates std golang request.
func (r *HTTPClient) newRequest(
	ctx context.Context,
	method string,
	url string,
	body Body,
	headers ...http.Header,
) (*http.Request, error) {
	if body.err != nil {
		return nil, fmt.Errorf("encoding request body: %w", body.err)
	}

	var reader io.Reader
	if body.data != nil {
		reader = bytes.NewReader(body.data)
	}

	request, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	for header := range slices.Values(headers) {
		for key, values := range header {
			request.Header[key] = append(request.Header[key], values...)
		}
	}

	if body.contentType != "" && request.Header.Get(contentTypeHeader) == "" {
		request.Header.Set(contentTypeHeader, body.contentType)
	}

	return request, nil
}

func (r *HTTPClient) cacheEnabled() bool {
	return r.kvs != nil
}

type Pool struct {
	ConcurrencyLevel int
}

func (r *Pool) submit(f func()) {
	f()
}
