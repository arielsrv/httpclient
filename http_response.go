package httpclient

import (
	"fmt"
	"net/http"
	"time"
)

type HTTPResponse[T any] struct {
	raw       *http.Response
	data      T
	bodyBytes []byte
	codec     Codec
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

func (r HTTPResponse[T]) Revalidate() bool {
	return r.raw.Header.Get("Revalidate") == "true"
}

func (r HTTPResponse[T]) Cacheable() (time.Duration, bool) {
	return time.Duration(5) * time.Minute, true
}
