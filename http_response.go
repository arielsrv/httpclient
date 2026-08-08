package httpclient

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

type HTTPResponse[T any] struct {
	raw       *http.Response
	data      T
	bodyBytes []byte
	codec     Codec
}

// newHTTPResponse assembles a typed response, decoding the body with codec when
// the status is successful. Both the network path and the cache-hit path go
// through it so a cached response is indistinguishable from a fresh one.
func newHTTPResponse[T any](
	raw *http.Response,
	bodyBytes []byte,
	codec Codec,
) (HTTPResponse[T], error) {
	response := HTTPResponse[T]{raw: raw, bodyBytes: bodyBytes, codec: codec}

	if response.IsSuccess() && len(bodyBytes) > 0 {
		if err := codec.Unmarshal(bodyBytes, &response.data); err != nil {
			return response, fmt.Errorf("deserializing response: %w", err)
		}
	}

	return response, nil
}

// toCacheEntry captures the parts of the response worth storing, valid for
// freshFor. The decoded data is left out: it is rebuilt from the body on the way
// back, which keeps the entry usable from any type parameter.
func (r HTTPResponse[T]) toCacheEntry(freshFor time.Duration) cachedResponse {
	return cachedResponse{
		StatusCode: r.raw.StatusCode,
		Headers:    r.raw.Header,
		Body:       r.bodyBytes,
		StoredAt:   time.Now(),
		FreshFor:   freshFor,
	}
}

// responseFromCache rebuilds a typed response from a stored entry. The codec is
// negotiated exactly as it was for the live response, preferring the caller's
// Accept header over the stored Content-Type.
func responseFromCache[T any](
	entry cachedResponse,
	headers []http.Header,
) (*HTTPResponse[T], error) {
	raw := &http.Response{StatusCode: entry.StatusCode, Header: entry.Headers}
	if raw.Header == nil {
		raw.Header = make(http.Header)
	}

	codec := codecForAcceptHeader(headers)
	if codec == nil {
		codec = codecForContentType(raw.Header.Get(contentTypeHeader))
	}

	response, err := newHTTPResponse[T](raw, entry.Body, codec)
	if err != nil {
		return nil, err
	}

	return &response, nil
}

func (r HTTPResponse[T]) Data() T {
	return r.data
}

func (r HTTPResponse[T]) StatusCode() int {
	return r.raw.StatusCode
}

func (r HTTPResponse[T]) Body() string {
	return string(r.bodyBytes)
}

func (r HTTPResponse[T]) Headers() http.Header {
	return r.raw.Header
}

func (r HTTPResponse[T]) IsSuccess() bool {
	return r.raw.StatusCode >= http.StatusOK &&
		r.raw.StatusCode < http.StatusMultipleChoices
}

// As deserializes the raw response body into E, using the same codec negotiated
// for the response. Useful for decoding error payloads (4xx/5xx) into a typed
// struct, since Data() only covers the success type T. Returns the zero value of
// E if the body is empty.
func (r HTTPResponse[T]) As[E any]() (E, error) {
	var out E
	if len(r.bodyBytes) == 0 {
		return out, nil
	}
	codec := r.codec
	if codec == nil {
		codec = defaultCodec
	}
	if err := codec.Unmarshal(r.bodyBytes, &out); err != nil {
		return out, fmt.Errorf("deserializing body: %w", err)
	}
	return out, nil
}

// Revalidate reports whether a cached copy of this response must be checked with
// the origin before being reused, that is whether it carries Cache-Control:
// no-cache. The client revalidates with a conditional request when the response
// also carries an ETag or a Last-Modified.
func (r HTTPResponse[T]) Revalidate() bool {
	if r.raw == nil {
		return true
	}
	return parseCacheControl(r.raw.Header.Get(cacheControlHeader)).has(directiveNoCache)
}

// Cacheable reports whether the response may be stored and for how long, honoring
// the response's Cache-Control directives and Expires header (RFC 9111).
// Responses that carry no freshness information fall back to defaultTTL.
//
// Only successful responses are cacheable; no-store, max-age=0 and Vary: *
// opt out.
func (r HTTPResponse[T]) Cacheable(defaultTTL time.Duration) (time.Duration, bool) {
	if r.raw == nil || !r.IsSuccess() {
		return 0, false
	}

	// Vary: * means the response depends on something no cache key can capture.
	if strings.TrimSpace(r.raw.Header.Get(varyHeader)) == "*" {
		return 0, false
	}

	return freshnessFor(r.raw.Header, defaultTTL)
}
