package httpclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
)

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

func NewHTTPClient(opts ...ClientOption) *HTTPClient {
	httpClient := &HTTPClient{
		lowLevelClient: http.Client{
			Transport: NewConnectionPool().transport,
		},
	}
	for opt := range slices.Values(opts) {
		opt(httpClient)
	}
	return httpClient
}

func (r *HTTPClient) Get[T any](ctx context.Context, url string, headers ...http.Header) (HTTPResponse[T], error) {
	return r.doRequest[T](ctx, http.MethodGet, url, headers...)
}

// GetAsync fires a GET request in a goroutine and returns a Future immediately.
// The context controls cancellation — cancelling ctx will unblock Await with an error.
func (r *HTTPClient) GetAsync[T any](ctx context.Context, url string, headers ...http.Header) *Future[T] {
	ch := make(chan futureResult[T], 1)
	go func() {
		resp, err := r.Get[T](ctx, url, headers...)
		ch <- futureResult[T]{response: resp, err: err}
	}()
	return &Future[T]{ch: ch}
}

func (r *HTTPClient) doRequest[T any](ctx context.Context, method string, url string, headers ...http.Header) (result HTTPResponse[T], err error) {
	request, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return result, fmt.Errorf("creating request: %w", err)
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

	result = HTTPResponse[T]{
		statusCode: response.StatusCode,
		body:       bodyBytes,
		headers:    response.Header,
	}

	if result.IsSuccess() && len(bodyBytes) > 0 {
		if err = json.Unmarshal(bodyBytes, &result.data); err != nil {
			return result, fmt.Errorf("deserializing response: %w", err)
		}
	}

	return result, nil
}
