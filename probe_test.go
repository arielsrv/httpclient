package httpclient_test

// Coverage for the error/fallback branches of body and codec negotiation.

import (
	"context"
	"net/http"
	"testing"

	"github.com/arielsrv/httpclient"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestForm_EncodeError covers Form's error branch: go-playground/form rejects a
// nil value, and the error must surface when the request is sent.
func TestForm_EncodeError(t *testing.T) {
	t.Parallel()
	_, err := httpclient.NewHTTPClient().Post[any](
		context.Background(), "http://example.com", nil,
		http.Header{"Content-Type": []string{"application/x-www-form-urlencoded"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "encoding request body")
	assert.Contains(t, err.Error(), "form")
}

// TestResponse_MalformedContentTypeFallsBackToJSON covers the ParseMediaType
// error branch: an unparseable Content-Type must fall back to the JSON codec.
func TestResponse_MalformedContentTypeFallsBackToJSON(t *testing.T) {
	t.Parallel()
	server, _ := echoServer(t, http.StatusOK, "application/", []byte(`{"id":1,"name":"Zed"}`))
	defer server.Close()

	resp, err := httpclient.NewHTTPClient().Get[testUser](context.Background(), server.URL)
	require.NoError(t, err)
	assert.Equal(t, "Zed", resp.Data().Name)
}
