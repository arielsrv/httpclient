package httpclient_test

import (
	"context"
	"encoding/json"
	"errors"
	"httpclient"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testUser struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// roundTripFunc allows using a plain function as an http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// errReadCloser is a ReadCloser whose Read always errors.
type errReadCloser struct{ closeErr error }

func (e *errReadCloser) Read([]byte) (int, error) { return 0, errors.New("read error") }
func (e *errReadCloser) Close() error             { return e.closeErr }

// readThenCloseErr is a ReadCloser that reads successfully but errors on Close.
type readThenCloseErr struct {
	data []byte
	pos  int
}

func (r *readThenCloseErr) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}
func (r *readThenCloseErr) Close() error { return errors.New("close error") }

func newTestServer(status int, body any, headers map[string]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		if body != nil {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(status)
		if body != nil {
			if err := json.NewEncoder(w).Encode(body); err != nil {
				panic(err)
			}
		}
	}))
}

// --- HTTPResponse ---

func TestHTTPResponse_IsSuccess(t *testing.T) {
	cases := []struct {
		statusCode int
		want       bool
	}{
		{http.StatusOK, true},
		{http.StatusCreated, true},
		{http.StatusNoContent, true},
		{http.StatusMultipleChoices, false},
		{http.StatusBadRequest, false},
		{http.StatusInternalServerError, false},
	}

	for _, tc := range cases {
		t.Run(http.StatusText(tc.statusCode), func(t *testing.T) {
			server := newTestServer(tc.statusCode, nil, nil)
			defer server.Close()

			resp, err := httpclient.NewHTTPClient().Get[any](context.Background(), server.URL)
			require.NoError(t, err)
			assert.Equal(t, tc.want, resp.IsSuccess())
		})
	}
}

func TestHTTPResponse_StatusCode(t *testing.T) {
	server := newTestServer(http.StatusTeapot, nil, nil)
	defer server.Close()

	resp, err := httpclient.NewHTTPClient().Get[any](context.Background(), server.URL)
	require.NoError(t, err)
	assert.Equal(t, http.StatusTeapot, resp.StatusCode())
}

func TestHTTPResponse_DataDeserialized(t *testing.T) {
	user := testUser{ID: 1, Name: "Alice"}
	server := newTestServer(http.StatusOK, user, nil)
	defer server.Close()

	resp, err := httpclient.NewHTTPClient().Get[testUser](context.Background(), server.URL)
	require.NoError(t, err)
	assert.Equal(t, user.ID, resp.Data().ID)
	assert.Equal(t, user.Name, resp.Data().Name)
}

func TestHTTPResponse_DataNotDeserializedOnNonSuccess(t *testing.T) {
	server := newTestServer(http.StatusBadRequest, map[string]string{"error": "bad input"}, nil)
	defer server.Close()

	resp, err := httpclient.NewHTTPClient().Get[testUser](context.Background(), server.URL)
	require.NoError(t, err)
	assert.False(t, resp.IsSuccess())
	assert.Zero(t, resp.Data().ID)
	assert.Empty(t, resp.Data().Name)
}

func TestHTTPResponse_BodyOnNonSuccess(t *testing.T) {
	server := newTestServer(http.StatusUnauthorized, map[string]string{"message": "unauthorized"}, nil)
	defer server.Close()

	resp, err := httpclient.NewHTTPClient().Get[testUser](context.Background(), server.URL)
	require.NoError(t, err)
	assert.NotEmpty(t, resp.Body())
}

func TestHTTPResponse_Headers(t *testing.T) {
	server := newTestServer(http.StatusOK, nil, map[string]string{"X-Custom-Header": "hello"})
	defer server.Close()

	resp, err := httpclient.NewHTTPClient().Get[any](context.Background(), server.URL)
	require.NoError(t, err)
	assert.Equal(t, "hello", resp.Headers().Get("X-Custom-Header"))
}

// --- HTTPClient ---

func TestNewHTTPClient_DefaultPool(t *testing.T) {
	assert.NotNil(t, httpclient.NewHTTPClient())
}

func TestNewHTTPClient_WithCustomPool(t *testing.T) {
	pool := httpclient.NewConnectionPoolWithOptions(httpclient.ConnectionPoolOptions{
		MaxIdleConns:        5,
		MaxIdleConnsPerHost: 2,
		MaxConnsPerHost:     10,
		IdleConnTimeout:     10 * time.Second,
	})
	assert.NotNil(t, httpclient.NewHTTPClient(httpclient.WithConnectionPool(pool)))
}

func TestHTTPClient_Get_NetworkError(t *testing.T) {
	_, err := httpclient.NewHTTPClient().Get[any](context.Background(), "http://localhost:0/invalid")
	assert.Error(t, err)
}

func TestHTTPClient_Get_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(1 * time.Second)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := httpclient.NewHTTPClient().Get[any](ctx, server.URL)
	assert.Error(t, err)
}

// --- ConnectionPool ---

func TestNewConnectionPool_Defaults(t *testing.T) {
	assert.NotNil(t, httpclient.NewConnectionPool())
}

func TestNewConnectionPoolWithOptions(t *testing.T) {
	pool := httpclient.NewConnectionPoolWithOptions(httpclient.ConnectionPoolOptions{
		MaxIdleConns:        50,
		MaxIdleConnsPerHost: 5,
		MaxConnsPerHost:     100,
		IdleConnTimeout:     60 * time.Second,
	})
	assert.NotNil(t, pool)
}

// --- Future ---

func TestGetAsync_Await(t *testing.T) {
	user := testUser{ID: 42, Name: "Bob"}
	server := newTestServer(http.StatusOK, user, nil)
	defer server.Close()

	resp, err := httpclient.NewHTTPClient().GetAsync[testUser](context.Background(), server.URL).Await()
	require.NoError(t, err)
	assert.True(t, resp.IsSuccess())
	assert.Equal(t, 42, resp.Data().ID)
	assert.Equal(t, "Bob", resp.Data().Name)
}

func TestGetAsync_ConcurrentRequests(t *testing.T) {
	usersServer := newTestServer(http.StatusOK, []testUser{{ID: 1, Name: "Alice"}}, nil)
	postsServer := newTestServer(http.StatusOK, []testUser{{ID: 2, Name: "Post"}}, nil)
	defer usersServer.Close()
	defer postsServer.Close()

	client := httpclient.NewHTTPClient()
	ctx := context.Background()

	// Fire both before awaiting either — total time ≈ slowest, not sum
	usersFuture := client.GetAsync[[]testUser](ctx, usersServer.URL)
	postsFuture := client.GetAsync[[]testUser](ctx, postsServer.URL)

	usersResp, err := usersFuture.Await()
	require.NoError(t, err)
	assert.True(t, usersResp.IsSuccess())
	assert.Len(t, usersResp.Data(), 1)

	postsResp, err := postsFuture.Await()
	require.NoError(t, err)
	assert.True(t, postsResp.IsSuccess())
	assert.Len(t, postsResp.Data(), 1)
}

func TestGetAsync_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(1 * time.Second)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := httpclient.NewHTTPClient().GetAsync[any](ctx, server.URL).Await()
	assert.Error(t, err)
}

// --- Race conditions ---

// TestRace_SharedClientConcurrentGets verifies that a single client can be used
// from multiple goroutines simultaneously without data races.
func TestRace_SharedClientConcurrentGets(t *testing.T) {
	server := newTestServer(http.StatusOK, testUser{ID: 1, Name: "Alice"}, nil)
	defer server.Close()

	client := httpclient.NewHTTPClient()
	const goroutines = 20

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for range goroutines {
		go func() {
			defer wg.Done()
			resp, err := client.Get[testUser](context.Background(), server.URL)
			require.NoError(t, err)
			assert.True(t, resp.IsSuccess())
		}()
	}

	wg.Wait()
}

// TestRace_SharedPoolMultipleClients verifies that multiple clients sharing a
// connection pool don't race on the underlying transport.
func TestRace_SharedPoolMultipleClients(t *testing.T) {
	server := newTestServer(http.StatusOK, testUser{ID: 2, Name: "Bob"}, nil)
	defer server.Close()

	pool := httpclient.NewConnectionPool()
	c1 := httpclient.NewHTTPClient(httpclient.WithConnectionPool(pool))
	c2 := httpclient.NewHTTPClient(httpclient.WithConnectionPool(pool))

	var wg sync.WaitGroup
	wg.Add(20)

	for i := range 20 {
		go func() {
			defer wg.Done()
			client := c1
			if i%2 == 0 {
				client = c2
			}
			resp, err := client.Get[testUser](context.Background(), server.URL)
			require.NoError(t, err)
			assert.True(t, resp.IsSuccess())
		}()
	}

	wg.Wait()
}

// TestRace_ConcurrentFutures verifies that firing many GetAsync calls at once
// and awaiting them all doesn't cause races.
func TestRace_ConcurrentFutures(t *testing.T) {
	server := newTestServer(http.StatusOK, testUser{ID: 3, Name: "Carol"}, nil)
	defer server.Close()

	client := httpclient.NewHTTPClient()
	const count = 20

	futures := make([]*httpclient.Future[testUser], count)
	for i := range count {
		futures[i] = client.GetAsync[testUser](context.Background(), server.URL)
	}

	for _, f := range futures {
		resp, err := f.Await()
		require.NoError(t, err)
		assert.True(t, resp.IsSuccess())
		assert.Equal(t, "Carol", resp.Data().Name)
	}
}

func TestHTTPClient_Get_WithHeaders(t *testing.T) {
	var received http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = r.Header
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	headers := http.Header{
		"Authorization": []string{"Bearer token123"},
		"X-Request-Id":  []string{"abc-456"},
	}
	_, err := httpclient.NewHTTPClient().Get[any](context.Background(), server.URL, headers)
	require.NoError(t, err)
	assert.Equal(t, "Bearer token123", received.Get("Authorization"))
	assert.Equal(t, "abc-456", received.Get("X-Request-Id"))
}

// TestHTTPClient_Get_InvalidURL covers the NewRequestWithContext error branch
// (null byte makes url.Parse reject the URL).
func TestHTTPClient_Get_InvalidURL(t *testing.T) {
	_, err := httpclient.NewHTTPClient().Get[any](context.Background(), "http://foo\x00.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "creating request")
}

// TestHTTPClient_Get_BodyReadError covers the io.ReadAll error branch.
func TestHTTPClient_Get_BodyReadError(t *testing.T) {
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       &errReadCloser{},
			Header:     make(http.Header),
		}, nil
	})

	_, err := httpclient.NewHTTPClient(httpclient.WithTransport(transport)).
		Get[any](context.Background(), "http://example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading response body")
}

// TestHTTPClient_Get_BodyCloseError covers the Body.Close() error branch.
// With named returns the deferred close error propagates to the caller.
func TestHTTPClient_Get_BodyCloseError(t *testing.T) {
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       &readThenCloseErr{data: []byte(`null`)},
			Header:     make(http.Header),
		}, nil
	})

	_, err := httpclient.NewHTTPClient(httpclient.WithTransport(transport)).
		Get[any](context.Background(), "http://example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "closing response body")
}

// TestHTTPClient_Get_InvalidJSONResponse covers the json.Unmarshal error branch.
func TestHTTPClient_Get_InvalidJSONResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not-valid-json"))
	}))
	defer server.Close()

	_, err := httpclient.NewHTTPClient().Get[testUser](context.Background(), server.URL)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deserializing response")
}
