// Package main shows conditional revalidation. Once a cached response goes
// stale, the client does not refetch it blindly: it asks the origin with an
// If-None-Match built from the stored ETag, and a 304 confirms the copy it
// already has — no body crosses the wire.
//
//	go run ./conditional_cache
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
	"time"

	"github.com/arielsrv/httpclient"
)

type UserResponse struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	ID    int    `json:"id"`
}

const etag = `"users-v1"`

func main() {
	// requests counts every call that reached the origin; bodies counts the ones
	// it had to answer with a full payload.
	var requests, bodies atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("ETag", etag)
		// Deliberately short, so the entry goes stale between calls.
		w.Header().Set("Cache-Control", "max-age=1")

		// The client sends back the validator it stored; nothing changed, so the
		// origin can skip the body entirely.
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}

		bodies.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(UserResponse{
			ID:    1,
			Name:  "John Doe",
			Email: "john@doe.com",
		}); err != nil {
			log.Fatal(err)
		}
	}))
	defer server.Close()

	cache, err := httpclient.NewInMemoryClientCache(httpclient.InMemoryConfig{ByteSize: 8 << 20})
	if err != nil {
		log.Fatalf("creating cache: %v", err)
	}
	defer cache.Close()

	// A stale entry is kept for the revalidation window so it can still be
	// confirmed with a conditional request. Pass 0 to always refetch instead.
	client := httpclient.NewHTTPClient(
		httpclient.WithCache(cache, runtime.NumCPU()-1),
		httpclient.WithCacheRevalidationWindow(10*time.Minute),
	)
	defer client.Close()

	ctx := context.Background()

	fetch := func(label string) {
		response, getErr := client.Get[UserResponse](ctx, server.URL)
		if getErr != nil {
			log.Fatalf("network error: %v", getErr)
		}
		if !response.IsSuccess() {
			log.Fatalf("unexpected status %d: %s", response.StatusCode(), response.Body())
		}
		user := response.Data()
		fmt.Printf(
			"  %-22s %s <%s>  status=%d  origin calls=%d, bodies sent=%d\n",
			label, user.Name, user.Email, response.StatusCode(),
			requests.Load(), bodies.Load(),
		)
	}

	fetch("cold:")
	fetch("fresh hit:")

	// Past max-age the entry is stale, but it still carries the ETag.
	time.Sleep(1100 * time.Millisecond)
	fetch("stale, revalidated:")

	// The 304 refreshed the entry, so this one is a plain hit again.
	fetch("fresh again:")

	fmt.Printf(
		"\nThe origin was called %d times but only sent %d body: the rest were 304s or cache hits.\n",
		requests.Load(),
		bodies.Load(),
	)
}
