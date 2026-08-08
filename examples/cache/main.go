package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync/atomic"

	"github.com/arielsrv/httpclient"
)

type UserResponse struct {
	Name   string `json:"name"`
	Email  string `json:"email"`
	Header string `json:"header"`
	ID     int    `json:"id"`
}

func main() {
	// hits counts how many requests actually reach the origin, so the cache is
	// visible in the output instead of having to be taken on faith.
	var hits atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		// The client honors Cache-Control: this response stays fresh for a minute.
		w.Header().Set("Cache-Control", "max-age=60")
		w.WriteHeader(http.StatusOK)
		bytes, err := json.Marshal(UserResponse{
			ID:     1,
			Name:   "John Doe",
			Email:  "john@doe.com",
			Header: r.Header.Get("X-Request-Id"),
		})
		if err != nil {
			log.Fatal(err)
		}
		if _, err = w.Write(bytes); err != nil {
			log.Fatal(err)
		}
	}))
	server.EnableHTTP2 = true
	defer server.Close()

	// The in-memory cache is backed by Ristretto: ByteSize is a real budget, and
	// entries are evicted by cost once it is reached.
	cache, err := httpclient.NewInMemoryClientCache(httpclient.InMemoryConfig{
		ByteSize: 8 << 20, // 8 MiB
	})
	if err != nil {
		log.Fatalf("creating cache: %v", err)
	}
	defer cache.Close()

	httpClient := httpclient.NewHTTPClient(httpclient.WithCache(cache, runtime.NumCPU()-1))
	// Close drains the pool that writes cache entries in the background.
	defer httpClient.Close()

	ctx := context.Background()
	// err only represents network-level errors (connection refused, timeout, etc.)
	response, err := httpClient.Get[UserResponse](ctx, server.URL, http.Header{
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
	fmt.Printf("  [%d] %s <%s> <%s> (origin hits: %d)\n",
		user.ID, user.Name, user.Email, user.Header, hits.Load())

	// Same URL: served from the cache even though the headers differ — the entry
	// is keyed by URL, and the echoed X-Request-Id proves it is the stored copy.
	response, err = httpClient.Get[UserResponse](ctx, server.URL, http.Header{
		"X-Request-Id": []string{"abc-124"},
		"X-Custom":     []string{"xyz"},
	})
	if err != nil {
		log.Fatalf("network error: %v", err)
	}

	if !response.IsSuccess() {
		log.Fatalf("unexpected status %d: %s", response.StatusCode(), response.Body())
	}

	user = response.Data()
	fmt.Printf("  [%d] %s <%s> <%s> (origin hits: %d)\n",
		user.ID, user.Name, user.Email, user.Header, hits.Load())
}
