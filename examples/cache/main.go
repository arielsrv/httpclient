package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"runtime"

	"github.com/arielsrv/httpclient"
)

type UserResponse struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

func main() {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"id": 1,
			"name": "John Doe",
			"email": "john@doe.com"
		}`))
	}))
	defer server.Close()
	httpClient := httpclient.NewHTTPClient(httpclient.
		WithCache(httpclient.NewInMemoryClientCache(httpclient.InMemoryConfig{
			ByteSize: 1024,
		}),
			runtime.NumCPU()-1,
		))

	// err only represents network-level errors (connection refused, timeout, etc.)
	response, err := httpClient.Get[UserResponse](context.Background(), server.URL, http.Header{
		"X-Request-Id": []string{"abc-123"},
	})
	if err != nil {
		log.Fatalf("network error: %v", err)
	}

	// non-2xx responses are not errors — check IsSuccess() and inspect the body
	if !response.IsSuccess() {
		log.Fatalf("unexpected status %d: %s", response.StatusCode(), response.Body())
	}

	// Data() returns the deserialized response body
	user := response.Data()
	fmt.Printf("  [%d] %s <%s>\n", user.ID, user.Name, user.Email)

	// err only represents network-level errors (connection refused, timeout, etc.)
	response, err = httpClient.Get[UserResponse](context.Background(), server.URL, http.Header{
		"X-Request-Id": []string{"abc-123", "abc-124"},
		"X-Custom":     []string{"xyz"},
	})
	if err != nil {
		log.Fatalf("network error: %v", err)
	}

	// non-2xx responses are not errors — check IsSuccess() and inspect the body
	if !response.IsSuccess() {
		log.Fatalf("unexpected status %d: %s", response.StatusCode(), response.Body())
	}

	// Data() returns the deserialized response body
	user = response.Data()
	fmt.Printf("  [%d] %s <%s>\n", user.ID, user.Name, user.Email)
}
