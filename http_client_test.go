package httpclient_test

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/arielsrv/httpclient"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testUser struct {
	Name string `json:"name"`
	ID   int    `json:"id"`
}

type xmlUser struct {
	XMLName xml.Name `xml:"user"`
	Name    string   `xml:"name"`
	ID      int      `xml:"id"`
}

// capturedRequest records what the server received, for asserting on the wire.
type capturedRequest struct {
	form        url.Values
	method      string
	contentType string
	body        []byte
}

// echoServer captures the incoming request and replies with the given status,
// optionally writing rawBody with contentType.
func echoServer(
	t *testing.T,
	status int,
	contentType string,
	rawBody []byte,
) (*httptest.Server, *capturedRequest) {
	t.Helper()
	captured := &capturedRequest{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.method = r.Method
		captured.contentType = r.Header.Get("Content-Type")
		captured.body, _ = io.ReadAll(r.Body)
		if r.Header.Get("Content-Type") == "application/x-www-form-urlencoded" {
			// Body is already drained by ReadAll, so parse the captured bytes.
			captured.form, _ = url.ParseQuery(string(captured.body))
		}
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(status)
		if rawBody != nil {
			_, _ = w.Write(rawBody)
		}
	}))
	return server, captured
}

// roundTripFunc allows using a plain function as an [http.RoundTripper].
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
	t.Parallel()
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
			t.Parallel()
			server := newTestServer(tc.statusCode, nil, nil)
			defer server.Close()

			resp, err := httpclient.NewHTTPClient().Get[any](context.Background(), server.URL)
			require.NoError(t, err)
			assert.Equal(t, tc.want, resp.IsSuccess())
		})
	}
}

func TestHTTPResponse_StatusCode(t *testing.T) {
	t.Parallel()
	server := newTestServer(http.StatusTeapot, nil, nil)
	defer server.Close()

	resp, err := httpclient.NewHTTPClient().Get[any](context.Background(), server.URL)
	require.NoError(t, err)
	assert.Equal(t, http.StatusTeapot, resp.StatusCode())
}

func TestHTTPResponse_DataDeserialized(t *testing.T) {
	t.Parallel()
	user := testUser{ID: 1, Name: "Alice"}
	server := newTestServer(http.StatusOK, user, nil)
	defer server.Close()

	resp, err := httpclient.NewHTTPClient().Get[testUser](context.Background(), server.URL)
	require.NoError(t, err)
	assert.Equal(t, user.ID, resp.Data().ID)
	assert.Equal(t, user.Name, resp.Data().Name)
}

func TestHTTPResponse_DataNotDeserializedOnNonSuccess(t *testing.T) {
	t.Parallel()
	server := newTestServer(http.StatusBadRequest, map[string]string{"error": "bad input"}, nil)
	defer server.Close()

	resp, err := httpclient.NewHTTPClient().Get[testUser](context.Background(), server.URL)
	require.NoError(t, err)
	assert.False(t, resp.IsSuccess())
	assert.Zero(t, resp.Data().ID)
	assert.Empty(t, resp.Data().Name)
}

func TestHTTPResponse_BodyOnNonSuccess(t *testing.T) {
	t.Parallel()
	server := newTestServer(
		http.StatusUnauthorized,
		map[string]string{"message": "unauthorized"},
		nil,
	)
	defer server.Close()

	resp, err := httpclient.NewHTTPClient().Get[testUser](context.Background(), server.URL)
	require.NoError(t, err)
	assert.NotEmpty(t, resp.Body())
}

func TestHTTPResponse_Headers(t *testing.T) {
	t.Parallel()
	server := newTestServer(http.StatusOK, nil, map[string]string{"X-Custom-Header": "hello"})
	defer server.Close()

	resp, err := httpclient.NewHTTPClient().Get[any](context.Background(), server.URL)
	require.NoError(t, err)
	assert.Equal(t, "hello", resp.Headers().Get("X-Custom-Header"))
}

func TestHTTPResponse_As_TypedErrorBody(t *testing.T) {
	t.Parallel()
	server := newTestServer(http.StatusBadRequest, map[string]string{"error": "bad input"}, nil)
	defer server.Close()

	resp, err := httpclient.NewHTTPClient().Get[testUser](context.Background(), server.URL)
	require.NoError(t, err)
	require.False(t, resp.IsSuccess())

	apiErr, err := resp.As[struct {
		Error string `json:"error"`
	}]()
	require.NoError(t, err)
	assert.Equal(t, "bad input", apiErr.Error)
}

func TestHTTPResponse_As_EmptyBody(t *testing.T) {
	t.Parallel()
	server := newTestServer(http.StatusNoContent, nil, nil)
	defer server.Close()

	resp, err := httpclient.NewHTTPClient().Get[any](context.Background(), server.URL)
	require.NoError(t, err)

	out, err := resp.As[testUser]()
	require.NoError(t, err)
	assert.Zero(t, out.ID)
}

func TestHTTPResponse_As_InvalidJSON(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("not-json"))
	}))
	defer server.Close()

	resp, err := httpclient.NewHTTPClient().Get[any](context.Background(), server.URL)
	require.NoError(t, err)

	_, err = resp.As[testUser]()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deserializing body")
}

// --- Request bodies / verbs ---

func TestPost_JSONBody(t *testing.T) {
	t.Parallel()
	server, captured := echoServer(
		t,
		http.StatusCreated,
		"application/json",
		[]byte(`{"id":1,"name":"Alice"}`),
	)
	defer server.Close()

	resp, err := httpclient.NewHTTPClient().Post[testUser](
		context.Background(), server.URL, testUser{ID: 1, Name: "Alice"},
		http.Header{"Content-Type": []string{"application/json"}})
	require.NoError(t, err)

	assert.Equal(t, http.MethodPost, captured.method)
	assert.Equal(t, "application/json", captured.contentType)
	assert.JSONEq(t, `{"id":1,"name":"Alice"}`, string(captured.body))

	assert.True(t, resp.IsSuccess())
	assert.Equal(t, "Alice", resp.Data().Name)
}

func TestPost_XMLBody_And_XMLResponse(t *testing.T) {
	t.Parallel()
	respBody, err := xml.Marshal(xmlUser{ID: 7, Name: "Bob"})
	require.NoError(t, err)

	server, captured := echoServer(t, http.StatusOK, "application/xml", respBody)
	defer server.Close()

	resp, err := httpclient.NewHTTPClient().Post[xmlUser](
		context.Background(), server.URL, xmlUser{ID: 7, Name: "Bob"},
		http.Header{"Content-Type": []string{"application/xml"}})
	require.NoError(t, err)

	assert.Equal(t, "application/xml", captured.contentType)
	assert.Contains(t, string(captured.body), "<name>Bob</name>")

	// Response decoded via content-negotiated XML codec.
	assert.Equal(t, 7, resp.Data().ID)
	assert.Equal(t, "Bob", resp.Data().Name)
}

func TestForm_StructWithTags(t *testing.T) {
	t.Parallel()
	server, captured := echoServer(t, http.StatusOK, "", nil)
	defer server.Close()

	body := struct {
		ID   int    `form:"id"`
		Item string `form:"item"`
	}{ID: 42, Item: "book"}

	_, err := httpclient.NewHTTPClient().Post[any](context.Background(), server.URL, body,
		http.Header{"Content-Type": []string{"application/x-www-form-urlencoded"}})
	require.NoError(t, err)

	assert.Equal(t, "application/x-www-form-urlencoded", captured.contentType)
	assert.Equal(t, "42", captured.form.Get("id"))
	assert.Equal(t, "book", captured.form.Get("item"))
}

func TestForm_OmitemptyDashAndFieldName(t *testing.T) {
	t.Parallel()
	server, captured := echoServer(t, http.StatusOK, "", nil)
	defer server.Close()

	body := struct {
		Query    string `form:"q"`
		Page     int    `form:"page,omitempty"` // zero → skipped
		Internal string `form:"-"`              // never sent
		Untagged string // uses field name
	}{Query: "go", Page: 0, Internal: "secret", Untagged: "x"}

	_, err := httpclient.NewHTTPClient().Post[any](context.Background(), server.URL, body,
		http.Header{"Content-Type": []string{"application/x-www-form-urlencoded"}})
	require.NoError(t, err)

	assert.Equal(t, "go", captured.form.Get("q"))
	assert.Empty(t, captured.form["page"])
	assert.Empty(t, captured.form["-"])
	assert.NotContains(t, string(captured.body), "secret")
	assert.Equal(t, "x", captured.form.Get("Untagged"))
}

func TestForm_SliceRepeatsKey(t *testing.T) {
	t.Parallel()
	server, captured := echoServer(t, http.StatusOK, "", nil)
	defer server.Close()

	body := struct {
		Tags []string `form:"tag"`
	}{Tags: []string{"a", "b"}}

	_, err := httpclient.NewHTTPClient().Post[any](context.Background(), server.URL, body,
		http.Header{"Content-Type": []string{"application/x-www-form-urlencoded"}})
	require.NoError(t, err)

	assert.Equal(t, []string{"a", "b"}, captured.form["tag"])
}

func TestForm_NilPointerSkipped(t *testing.T) {
	t.Parallel()
	server, captured := echoServer(t, http.StatusOK, "", nil)
	defer server.Close()

	limit := 10
	body := struct {
		Limit *int `form:"limit"`
		Cap   *int `form:"cap"`
	}{Limit: &limit, Cap: nil}

	_, err := httpclient.NewHTTPClient().Post[any](context.Background(), server.URL, body,
		http.Header{"Content-Type": []string{"application/x-www-form-urlencoded"}})
	require.NoError(t, err)

	assert.Equal(t, "10", captured.form.Get("limit"))
	assert.Empty(t, captured.form["cap"])
}

func TestForm_NestedStruct(t *testing.T) {
	t.Parallel()
	server, captured := echoServer(t, http.StatusOK, "", nil)
	defer server.Close()

	body := struct {
		Inner struct {
			X int `form:"x"`
		} `form:"inner"`
	}{Inner: struct {
		X int `form:"x"`
	}{X: 5}}

	_, err := httpclient.NewHTTPClient().Post[any](context.Background(), server.URL, body,
		http.Header{"Content-Type": []string{"application/x-www-form-urlencoded"}})
	require.NoError(t, err)

	// go-playground/form encodes nested fields with dotted keys.
	assert.Equal(t, "5", captured.form.Get("inner.x"))
}

func TestPost_EncodeError(t *testing.T) {
	t.Parallel()
	// A channel cannot be marshaled to JSON — the error must surface on send.
	_, err := httpclient.NewHTTPClient().Post[any](
		context.Background(), "http://example.com", make(chan int))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "encoding request body")
}

func TestPut_SendsBodyAndMethod(t *testing.T) {
	t.Parallel()
	server, captured := echoServer(
		t,
		http.StatusOK,
		"application/json",
		[]byte(`{"id":2,"name":"Dave"}`),
	)
	defer server.Close()

	resp, err := httpclient.NewHTTPClient().Put[testUser](
		context.Background(), server.URL, testUser{ID: 2, Name: "Dave"},
		http.Header{"Content-Type": []string{"application/json"}})
	require.NoError(t, err)

	assert.Equal(t, http.MethodPut, captured.method)
	assert.Equal(t, "Dave", resp.Data().Name)
}

func TestPatch_SendsBodyAndMethod(t *testing.T) {
	t.Parallel()
	server, captured := echoServer(t, http.StatusOK, "", nil)
	defer server.Close()

	_, err := httpclient.NewHTTPClient().Patch[any](
		context.Background(), server.URL, map[string]string{"name": "Eve"},
		http.Header{"Content-Type": []string{"application/json"}})
	require.NoError(t, err)

	assert.Equal(t, http.MethodPatch, captured.method)
	assert.JSONEq(t, `{"name":"Eve"}`, string(captured.body))
}

func TestDelete_NoBody(t *testing.T) {
	t.Parallel()
	server, captured := echoServer(t, http.StatusNoContent, "", nil)
	defer server.Close()

	resp, err := httpclient.NewHTTPClient().Delete[any](context.Background(), server.URL)
	require.NoError(t, err)

	assert.Equal(t, http.MethodDelete, captured.method)
	assert.Empty(t, captured.body)
	assert.True(t, resp.IsSuccess())
}

func TestPostAsync_Await(t *testing.T) {
	t.Parallel()
	server, captured := echoServer(
		t,
		http.StatusCreated,
		"application/json",
		[]byte(`{"id":5,"name":"Grace"}`),
	)
	defer server.Close()

	future := httpclient.NewHTTPClient().PostAsync[testUser](
		context.Background(), server.URL, testUser{ID: 5, Name: "Grace"},
		http.Header{"Content-Type": []string{"application/json"}})

	resp, err := future.Await()
	require.NoError(t, err)
	assert.Equal(t, http.MethodPost, captured.method)
	assert.True(t, resp.IsSuccess())
	assert.Equal(t, "Grace", resp.Data().Name)
}

func TestVerbAsync_MethodsReachServer(t *testing.T) {
	t.Parallel()
	client := httpclient.NewHTTPClient()
	ctx := context.Background()

	cases := []struct {
		name   string
		fire   func(url string) *httpclient.Future[any]
		method string
	}{
		{"Put", func(u string) *httpclient.Future[any] {
			return client.PutAsync[any](ctx, u, map[string]int{"n": 1},
				http.Header{"Content-Type": []string{"application/json"}})
		}, http.MethodPut},
		{"Patch", func(u string) *httpclient.Future[any] {
			return client.PatchAsync[any](ctx, u, map[string]int{"n": 1},
				http.Header{"Content-Type": []string{"application/json"}})
		}, http.MethodPatch},
		{"Delete", func(u string) *httpclient.Future[any] {
			return client.DeleteAsync[any](ctx, u)
		}, http.MethodDelete},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server, captured := echoServer(t, http.StatusOK, "", nil)
			defer server.Close()

			_, err := tc.fire(server.URL).Await()
			require.NoError(t, err)
			assert.Equal(t, tc.method, captured.method)
		})
	}
}

// --- Response content negotiation ---

func TestResponse_XMLNegotiationWithCharset(t *testing.T) {
	t.Parallel()
	respBody, err := xml.Marshal(xmlUser{ID: 9, Name: "Frank"})
	require.NoError(t, err)

	// Content-Type carries a charset parameter — must still negotiate XML.
	server, _ := echoServer(t, http.StatusOK, "application/xml; charset=utf-8", respBody)
	defer server.Close()

	resp, err := httpclient.NewHTTPClient().Get[xmlUser](context.Background(), server.URL)
	require.NoError(t, err)
	assert.Equal(t, "Frank", resp.Data().Name)
}

func TestResponse_As_UsesNegotiatedXMLCodec(t *testing.T) {
	t.Parallel()
	type apiError struct {
		XMLName xml.Name `xml:"error"`
		Message string   `xml:"message"`
	}
	respBody := []byte(`<error><message>not found</message></error>`)

	server, _ := echoServer(t, http.StatusNotFound, "application/xml", respBody)
	defer server.Close()

	resp, err := httpclient.NewHTTPClient().Get[xmlUser](context.Background(), server.URL)
	require.NoError(t, err)
	require.False(t, resp.IsSuccess())

	// As must reuse the XML codec negotiated for the response, not JSON.
	decoded, err := resp.As[apiError]()
	require.NoError(t, err)
	assert.Equal(t, "not found", decoded.Message)
}

func TestResponse_ProblemJSONNegotiatedBySuffix(t *testing.T) {
	t.Parallel()
	// RFC 7807 problem+json — the "+json" suffix must route to the JSON codec.
	type problem struct {
		Title  string `json:"title"`
		Status int    `json:"status"`
	}
	respBody := []byte(`{"title":"Unprocessable","status":422}`)

	server, _ := echoServer(t, http.StatusUnprocessableEntity, "application/problem+json", respBody)
	defer server.Close()

	resp, err := httpclient.NewHTTPClient().Get[any](context.Background(), server.URL)
	require.NoError(t, err)
	require.False(t, resp.IsSuccess())

	decoded, err := resp.As[problem]()
	require.NoError(t, err)
	assert.Equal(t, "Unprocessable", decoded.Title)
	assert.Equal(t, 422, decoded.Status)
}

func TestResponse_ProblemXMLNegotiatedBySuffix(t *testing.T) {
	t.Parallel()
	// The "+xml" suffix must route to the XML codec — before suffix support this
	// fell back to JSON and failed to decode.
	type problem struct {
		XMLName xml.Name `xml:"problem"`
		Title   string   `xml:"title"`
	}
	respBody := []byte(`<problem><title>Unprocessable</title></problem>`)

	server, _ := echoServer(t, http.StatusUnprocessableEntity, "application/problem+xml", respBody)
	defer server.Close()

	resp, err := httpclient.NewHTTPClient().Get[any](context.Background(), server.URL)
	require.NoError(t, err)

	decoded, err := resp.As[problem]()
	require.NoError(t, err)
	assert.Equal(t, "Unprocessable", decoded.Title)
}

// --- HTTPClient ---

func TestNewHTTPClient_DefaultPool(t *testing.T) {
	t.Parallel()
	assert.NotNil(t, httpclient.NewHTTPClient())
}

func TestWithTimeout_TripsBeforeSlowResponse(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer server.Close()

	client := httpclient.NewHTTPClient(httpclient.WithTimeout(10 * time.Millisecond))
	_, err := client.Get[any](context.Background(), server.URL)
	assert.Error(t, err)
}

func TestNewHTTPClient_WithCustomPool(t *testing.T) {
	t.Parallel()
	pool := httpclient.NewConnectionPoolWithOptions(httpclient.ConnectionPoolOptions{
		MaxIdleConns:        5,
		MaxIdleConnsPerHost: 2,
		MaxConnsPerHost:     10,
		IdleConnTimeout:     10 * time.Second,
	})
	assert.NotNil(t, httpclient.NewHTTPClient(httpclient.WithConnectionPool(pool)))
}

func TestHTTPClient_Get_NetworkError(t *testing.T) {
	t.Parallel()
	_, err := httpclient.NewHTTPClient().Get[any](
		context.Background(),
		"http://localhost:0/invalid",
	)
	assert.Error(t, err)
}

func TestHTTPClient_Get_ContextCancellation(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	assert.NotNil(t, httpclient.NewConnectionPool())
}

func TestNewConnectionPoolWithOptions(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	user := testUser{ID: 42, Name: "Bob"}
	server := newTestServer(http.StatusOK, user, nil)
	defer server.Close()

	resp, err := httpclient.NewHTTPClient().GetAsync[testUser](
		context.Background(),
		server.URL,
	).Await()
	require.NoError(t, err)
	assert.True(t, resp.IsSuccess())
	assert.Equal(t, 42, resp.Data().ID)
	assert.Equal(t, "Bob", resp.Data().Name)
}

func TestGetAsync_ConcurrentRequests(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

func TestHTTPClient_Get_Binary(t *testing.T) {
	t.Parallel()
	payload := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A} // PNG magic bytes
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	resp, err := httpclient.NewHTTPClient().Download(context.Background(), server.URL)
	require.NoError(t, err)
	assert.True(t, resp.IsSuccess())
	assert.Equal(t, payload, resp.Data())
}

// TestHTTPClient_Get_InvalidURL covers the NewRequestWithContext error branch
// (null byte makes [url.Parse] reject the URL).
func TestHTTPClient_Get_InvalidURL(t *testing.T) {
	t.Parallel()
	_, err := httpclient.NewHTTPClient().Get[any](context.Background(), "http://foo\x00.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "creating request")
}

// TestHTTPClient_Get_BodyReadError covers the [io.ReadAll] error branch.
func TestHTTPClient_Get_BodyReadError(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

// TestHTTPClient_Get_InvalidJSONResponse covers the [json.Unmarshal] error branch.
func TestHTTPClient_Get_InvalidJSONResponse(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not-valid-json"))
	}))
	defer server.Close()

	_, err := httpclient.NewHTTPClient().Get[testUser](context.Background(), server.URL)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deserializing response")
}

// TestDownloadAsync_Await verifies that DownloadAsync returns a Future whose Await
// delivers the raw bytes.
func TestDownloadAsync_Await(t *testing.T) {
	t.Parallel()
	payload := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A} // PNG magic bytes
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	resp, err := httpclient.NewHTTPClient().DownloadAsync(context.Background(), server.URL).Await()
	require.NoError(t, err)
	assert.True(t, resp.IsSuccess())
	assert.Equal(t, payload, resp.Data())
}

// TestGet_AcceptJSON_ForcesCodingViaAcceptHeader verifies that AcceptJSON drives
// JSON codec selection even when the server omits Content-Type.
func TestGet_AcceptJSON_ForcesCodingViaAcceptHeader(t *testing.T) {
	t.Parallel()
	user := testUser{ID: 99, Name: "Hana"}
	server, _ := echoServer(t, http.StatusOK, "application/json", mustMarshalJSON(t, user))
	defer server.Close()

	resp, err := httpclient.NewHTTPClient().Get[testUser](
		context.Background(),
		server.URL,
		httpclient.AcceptJSON(),
	)
	require.NoError(t, err)
	assert.Equal(t, user.Name, resp.Data().Name)
}

// TestGet_AcceptXML_ForcesCodingViaAcceptHeader verifies that AcceptXML drives
// XML codec selection.
func TestGet_AcceptXML_ForcesCodingViaAcceptHeader(t *testing.T) {
	t.Parallel()
	body, err := xml.Marshal(xmlUser{ID: 11, Name: "Ivar"})
	require.NoError(t, err)
	server, _ := echoServer(t, http.StatusOK, "application/xml", body)
	defer server.Close()

	resp, err := httpclient.NewHTTPClient().Get[xmlUser](
		context.Background(),
		server.URL,
		httpclient.AcceptXML(),
	)
	require.NoError(t, err)
	assert.Equal(t, "Ivar", resp.Data().Name)
}

// TestPost_DefaultJSON_WhenNoContentTypeHeader verifies that Post defaults to JSON
// when the caller does not pass a Content-Type header. This exercises the
// body.contentType != "" && request.Header.Get("Content-Type") == "" branch in doRequest.
func TestPost_DefaultJSON_WhenNoContentTypeHeader(t *testing.T) {
	t.Parallel()
	server, captured := echoServer(
		t,
		http.StatusCreated,
		"application/json",
		[]byte(`{"id":1,"name":"Jan"}`),
	)
	defer server.Close()

	// No explicit Content-Type header — bodyFromHeaders defaults to JSON.
	resp, err := httpclient.NewHTTPClient().Post[testUser](
		context.Background(), server.URL, testUser{ID: 1, Name: "Jan"})
	require.NoError(t, err)
	assert.Equal(t, "application/json", captured.contentType)
	assert.JSONEq(t, `{"id":1,"name":"Jan"}`, string(captured.body))
	assert.Equal(t, "Jan", resp.Data().Name)
}

// TestPost_AsJSON_Helper verifies the AsJSON() helper produces a valid request.
func TestPost_AsJSON_Helper(t *testing.T) {
	t.Parallel()
	server, captured := echoServer(t, http.StatusOK, "", nil)
	defer server.Close()

	_, err := httpclient.NewHTTPClient().Post[any](
		context.Background(), server.URL, testUser{ID: 2, Name: "Kai"}, httpclient.AsJSON())
	require.NoError(t, err)
	assert.Equal(t, "application/json", captured.contentType)
}

// TestPost_AsXML_Helper verifies the AsXML() helper produces a valid XML request.
func TestPost_AsXML_Helper(t *testing.T) {
	t.Parallel()
	server, captured := echoServer(t, http.StatusOK, "", nil)
	defer server.Close()

	_, err := httpclient.NewHTTPClient().Post[any](
		context.Background(), server.URL, xmlUser{ID: 3, Name: "Lena"}, httpclient.AsXML())
	require.NoError(t, err)
	assert.Equal(t, "application/xml", captured.contentType)
	assert.Contains(t, string(captured.body), "<name>Lena</name>")
}

// TestPost_AsForm_Helper verifies the AsForm() helper encodes as form data.
func TestPost_AsForm_Helper(t *testing.T) {
	t.Parallel()
	server, captured := echoServer(t, http.StatusOK, "", nil)
	defer server.Close()

	body := struct {
		Name string `form:"name"`
	}{Name: "Mia"}

	_, err := httpclient.NewHTTPClient().Post[any](
		context.Background(), server.URL, body, httpclient.AsForm())
	require.NoError(t, err)
	assert.Equal(t, "application/x-www-form-urlencoded", captured.contentType)
	assert.Equal(t, "Mia", captured.form.Get("name"))
}

// --- WithFollowRedirects ---

func TestWithFollowRedirects_True_FollowsRedirect(t *testing.T) {
	t.Parallel()
	// target returns 200
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	// redirect server issues 302 → target
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirect.Close()

	client := httpclient.NewHTTPClient(httpclient.WithFollowRedirects(true))
	resp, err := client.Get[any](context.Background(), redirect.URL)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode())
}

func TestWithFollowRedirects_False_ReturnsRedirectResponse(t *testing.T) {
	t.Parallel()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirect.Close()

	client := httpclient.NewHTTPClient(httpclient.WithFollowRedirects(false))
	resp, err := client.Get[any](context.Background(), redirect.URL)
	require.NoError(t, err)
	assert.Equal(t, http.StatusFound, resp.StatusCode())
	assert.Contains(t, resp.Headers().Get("Location"), target.URL)
}

func TestWithFollowRedirects_DefaultFollows(t *testing.T) {
	t.Parallel()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusMovedPermanently)
	}))
	defer redirect.Close()

	// Default client (no option) must follow redirects.
	resp, err := httpclient.NewHTTPClient().Get[any](context.Background(), redirect.URL)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode())
}

// mustMarshalJSON is a test helper that marshals v to JSON or fails the test.
func mustMarshalJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return data
}

// --- Cache pool ---

// countingServer replies with body and records how many requests it served.
func countingServer(hits *atomic.Int64, body any) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(body); err != nil {
			panic(err)
		}
	}))
}

// TestWithCache_WritesThroughPool verifies that the cache write submitted to the
// worker pool completes — after Close() flushes it, a second GET is served from
// the cache and never reaches the server.
func TestWithCache_WritesThroughPool(t *testing.T) {
	t.Parallel()
	var hits atomic.Int64
	server := countingServer(&hits, testUser{ID: 7, Name: "Dave"})
	defer server.Close()

	client := httpclient.NewHTTPClient(
		httpclient.WithCache(httpclient.NewInMemoryClientCache(httpclient.InMemoryConfig{
			ByteSize: 1024,
		}), 2),
	)

	first, err := client.Get[testUser](context.Background(), server.URL)
	require.NoError(t, err)
	assert.True(t, first.IsSuccess())

	// Close waits for the queued cache write to land.
	client.Close()

	second, err := client.Get[testUser](context.Background(), server.URL)
	require.NoError(t, err)
	assert.Equal(t, "Dave", second.Data().Name)
	assert.Equal(t, int64(1), hits.Load(), "second GET should be served from cache")
}

// TestRace_ConcurrentCacheWrites hammers a cached endpoint from many goroutines
// so the race detector can catch unsynchronized access to the cache.
func TestRace_ConcurrentCacheWrites(t *testing.T) {
	t.Parallel()
	var hits atomic.Int64
	server := countingServer(&hits, testUser{ID: 8, Name: "Erin"})
	defer server.Close()

	client := httpclient.NewHTTPClient(
		httpclient.WithCache(httpclient.NewInMemoryClientCache(httpclient.InMemoryConfig{
			ByteSize: 1024,
		}), 4),
	)
	defer client.Close()

	var wg sync.WaitGroup
	wg.Add(20)

	for range 20 {
		go func() {
			defer wg.Done()
			resp, err := client.Get[testUser](context.Background(), server.URL)
			require.NoError(t, err)
			assert.Equal(t, "Erin", resp.Data().Name)
		}()
	}

	wg.Wait()
}
