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
	fResult := <-r.ch
	return fResult.response, fResult.err
}

// async runs fn in a goroutine and returns a Future that resolves with its result.
func async[T any](fn func() (HTTPResponse[T], error)) *Future[T] {
	ch := make(chan futureResult[T], 1)
	go func() {
		resp, err := fn()
		ch <- futureResult[T]{response: resp, err: err}
	}()
	return &Future[T]{ch: ch}
}
