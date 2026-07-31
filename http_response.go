package httpclient

import "net/http"

type HTTPResponse[T any] struct {
	statusCode int
	data       T
	body       []byte
	headers    http.Header
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
