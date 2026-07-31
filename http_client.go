package httpclient

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"slices"
	"time"
)

// DefaultTimeout bounds the whole request (dial + body read) unless overridden.
// Use WithTimeout(0) to disable it and rely solely on the context deadline.
const DefaultTimeout = 30 * time.Second

type HTTPClient struct {
	lowLevelClient http.Client
}

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
func WithTimeout(d time.Duration) ClientOption {
	return func(c *HTTPClient) {
		c.lowLevelClient.Timeout = d
	}
}

func NewHTTPClient(opts ...ClientOption) *HTTPClient {
	httpClient := &HTTPClient{
		lowLevelClient: http.Client{
			Transport: NewConnectionPool().transport,
			Timeout:   DefaultTimeout,
		},
	}
	for opt := range slices.Values(opts) {
		opt(httpClient)
	}
	return httpClient
}

func (r *HTTPClient) Get[T any](ctx context.Context, url string, headers ...http.Header) (HTTPResponse[T], error) {
	return r.doRequest[T](ctx, http.MethodGet, url, Body{}, headers...)
}

// Post sends body (built with JSON/XML/Form) and decodes the response into T.
func (r *HTTPClient) Post[T any](ctx context.Context, url string, body Body, headers ...http.Header) (HTTPResponse[T], error) {
	return r.doRequest[T](ctx, http.MethodPost, url, body, headers...)
}

// Put sends body (built with JSON/XML/Form) and decodes the response into T.
func (r *HTTPClient) Put[T any](ctx context.Context, url string, body Body, headers ...http.Header) (HTTPResponse[T], error) {
	return r.doRequest[T](ctx, http.MethodPut, url, body, headers...)
}

// Patch sends body (built with JSON/XML/Form) and decodes the response into T.
func (r *HTTPClient) Patch[T any](ctx context.Context, url string, body Body, headers ...http.Header) (HTTPResponse[T], error) {
	return r.doRequest[T](ctx, http.MethodPatch, url, body, headers...)
}

// Delete sends a DELETE request (no body) and decodes the response into T.
func (r *HTTPClient) Delete[T any](ctx context.Context, url string, headers ...http.Header) (HTTPResponse[T], error) {
	return r.doRequest[T](ctx, http.MethodDelete, url, Body{}, headers...)
}

// GetAsync fires a GET request in a goroutine and returns a Future immediately.
// The context controls cancellation — cancelling ctx will unblock Await with an error.
func (r *HTTPClient) GetAsync[T any](ctx context.Context, url string, headers ...http.Header) *Future[T] {
	return async(func() (HTTPResponse[T], error) { return r.Get[T](ctx, url, headers...) })
}

// PostAsync fires a POST request in a goroutine and returns a Future immediately.
func (r *HTTPClient) PostAsync[T any](ctx context.Context, url string, body Body, headers ...http.Header) *Future[T] {
	return async(func() (HTTPResponse[T], error) { return r.Post[T](ctx, url, body, headers...) })
}

// PutAsync fires a PUT request in a goroutine and returns a Future immediately.
func (r *HTTPClient) PutAsync[T any](ctx context.Context, url string, body Body, headers ...http.Header) *Future[T] {
	return async(func() (HTTPResponse[T], error) { return r.Put[T](ctx, url, body, headers...) })
}

// PatchAsync fires a PATCH request in a goroutine and returns a Future immediately.
func (r *HTTPClient) PatchAsync[T any](ctx context.Context, url string, body Body, headers ...http.Header) *Future[T] {
	return async(func() (HTTPResponse[T], error) { return r.Patch[T](ctx, url, body, headers...) })
}

// DeleteAsync fires a DELETE request in a goroutine and returns a Future immediately.
func (r *HTTPClient) DeleteAsync[T any](ctx context.Context, url string, headers ...http.Header) *Future[T] {
	return async(func() (HTTPResponse[T], error) { return r.Delete[T](ctx, url, headers...) })
}

func (r *HTTPClient) doRequest[T any](ctx context.Context, method string, url string, body Body, headers ...http.Header) (result HTTPResponse[T], err error) {
	if body.err != nil {
		return result, fmt.Errorf("encoding request body: %w", body.err)
	}

	var reader io.Reader
	if body.data != nil {
		reader = bytes.NewReader(body.data)
	}

	request, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return result, fmt.Errorf("creating request: %w", err)
	}

	if body.contentType != "" {
		request.Header.Set("Content-Type", body.contentType)
	}

	for _, h := range headers {
		for key, values := range h {
			for _, value := range values {
				request.Header.Add(key, value)
			}
		}
	}

	response, err := r.lowLevelClient.Do(request)
	if err != nil {
		return result, fmt.Errorf("network error: %w", err)
	}
	defer func() {
		if closeErr := response.Body.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("closing response body: %w", closeErr)
		}
	}()

	bodyBytes, err := io.ReadAll(response.Body)
	if err != nil {
		return result, fmt.Errorf("reading response body: %w", err)
	}

	codec := codecForContentType(response.Header.Get("Content-Type"))

	result = HTTPResponse[T]{
		statusCode: response.StatusCode,
		body:       bodyBytes,
		headers:    response.Header,
		codec:      codec,
	}

	if result.IsSuccess() && len(bodyBytes) > 0 {
		if err = codec.Unmarshal(bodyBytes, &result.data); err != nil {
			return result, fmt.Errorf("deserializing response: %w", err)
		}
	}

	return result, nil
}
