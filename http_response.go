package httpclient

import (
	"fmt"
	"net/http"
)

type HTTPResponse[T any] struct {
	statusCode int
	data       T
	body       []byte
	headers    http.Header
	codec      Codec
}

func (r *HTTPResponse[T]) StatusCode() int {
	return r.statusCode
}

func (r *HTTPResponse[T]) Data() T {
	return r.data
}

func (r *HTTPResponse[T]) Body() string {
	return string(r.body)
}

func (r *HTTPResponse[T]) IsSuccess() bool {
	return r.statusCode >= http.StatusOK && r.statusCode < http.StatusMultipleChoices
}

func (r *HTTPResponse[T]) Headers() http.Header {
	return r.headers
}

// As deserializes the raw response body into E, using the same codec negotiated
// for the response. Useful for decoding error payloads (4xx/5xx) into a typed
// struct, since Data() only covers the success type T. Returns the zero value of
// E if the body is empty.
func (r *HTTPResponse[T]) As[E any]() (E, error) {
	var out E
	if len(r.body) == 0 {
		return out, nil
	}
	codec := r.codec
	if codec == nil {
		codec = defaultCodec
	}
	if err := codec.Unmarshal(r.body, &out); err != nil {
		return out, fmt.Errorf("deserializing body: %w", err)
	}
	return out, nil
}
