package httpclient

type futureResult[T any] struct {
	response HTTPResponse[T]
	err      error
}

// Future represents an in-flight async HTTP request.
// Call Await() to block until the result is ready.
type Future[T any] struct {
	ch <-chan futureResult[T]
}

// Await blocks until the request completes and returns the response and error.
func (r *Future[T]) Await() (HTTPResponse[T], error) {
	response := <-r.ch
	return response.response, response.err
}
