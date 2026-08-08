package httpclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"slices"
	"time"

	"github.com/alitto/pond/v2"
	"github.com/emirpasic/gods/sets/hashset"
)

// DefaultTimeout bounds the whole request (dial + body read) unless overridden.
// Use WithTimeout(0) to disable it and rely solely on the context deadline.
const DefaultTimeout = 30 * time.Second

type HTTPClient struct {
	httpKeyGenerator HTTPKeyGenerator
	kvs              *KVS
	pool             pond.Pool
	cacheableMethods *hashset.Set
	lowLevelClient   http.Client
	concurrencyLevel int
}

func NewHTTPClient(opts ...ClientOption) *HTTPClient {
	httpClient := &HTTPClient{
		lowLevelClient: http.Client{
			Transport: NewConnectionPool().transport,
			Timeout:   DefaultTimeout,
		},
		httpKeyGenerator: defaultCacheKeyGenerator{},
		concurrencyLevel: defaultConcurrencyLevel(),
		cacheableMethods: hashset.New(
			http.MethodGet,
			http.MethodHead,
			http.MethodOptions,
		),
	}
	for opt := range slices.Values(opts) {
		opt(httpClient)
	}
	// Built after the options so WithCache's concurrency level actually sizes it.
	// pond spawns workers on demand, so an unused pool costs nothing.
	httpClient.pool = pond.NewPool(max(1, httpClient.concurrencyLevel))
	return httpClient
}

// defaultConcurrencyLevel leaves one CPU to the caller and never drops below one worker.
func defaultConcurrencyLevel() int {
	return max(1, runtime.NumCPU()-1)
}

// Close stops the background pool used for cache writes and waits for the
// pending ones to finish. The client must not be used after Close returns.
func (r *HTTPClient) Close() {
	r.pool.StopAndWait()
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
		value, err := r.kvs.Get[HTTPResponse[T]](ctx, url)
		switch {
		case errors.Is(err, ErrCacheMiss): // nothing cached — fall through to the network
		case err != nil:
			return nil, fmt.Errorf("getting cached response: %w", err)
		case !value.Revalidate():
			return value, nil
		}
	}

	request, err := r.newRequest(ctx, method, url, body, headers...)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	response, doErr := r.lowLevelClient.Do(request)
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
			// The caller may cancel ctx as soon as doRequest returns, while the
			// write is still queued — keep its values, drop its cancellation.
			cacheCtx := context.WithoutCancel(ctx)
			key := request.URL.String()
			_ = r.pool.Go(func() {
				_ = r.kvs.Set(cacheCtx, key, httpResponse, ttl)
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
