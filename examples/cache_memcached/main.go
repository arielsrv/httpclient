// Package main shows how to back the client's HTTP cache with Memcached.
//
// Start the server first, from the examples directory:
//
//	docker compose up -d memcached
//	go run ./cache_memcached
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync/atomic"

	"github.com/arielsrv/httpclient"
)

type UserResponse struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	ID    int    `json:"id"`
}

func main() {
	// hits counts the requests that actually reach the origin, so the cache is
	// visible in the output instead of having to be taken on faith.
	var hits atomic.Int64

	ctx := context.Background()

	// A fixed port keeps the URL — and therefore the cache key — stable across
	// runs, which is what makes the shared-cache effect observable.
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(ctx, "tcp", "127.0.0.1:18081")
	if err != nil {
		log.Fatalf("port 18081 is busy: %v", err)
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		// The client honors Cache-Control: this response stays fresh for a minute.
		w.Header().Set("Cache-Control", "max-age=60")
		if encodeErr := json.NewEncoder(w).Encode(UserResponse{
			ID:    2,
			Name:  "Jane Roe",
			Email: "jane@roe.com",
		}); encodeErr != nil {
			log.Fatal(encodeErr)
		}
	})

	server := httptest.NewUnstartedServer(handler)
	if closeErr := server.Listener.Close(); closeErr != nil {
		log.Fatalf("releasing the default listener: %v", closeErr)
	}
	server.Listener = listener
	server.Start()
	defer server.Close()

	// Host and Port default to localhost:11211, which is what docker-compose.yml
	// publishes. Use Servers to point at a sharded setup instead.
	cache := httpclient.NewMemcachedClientCache(httpclient.MemcachedConfig{})
	defer func() {
		if closeErr := cache.Close(); closeErr != nil {
			log.Printf("closing memcached: %v", closeErr)
		}
	}()

	// Ping fails fast on a misconfigured server. Without it the client would
	// silently degrade to the network on every request.
	if pingErr := cache.Ping(); pingErr != nil {
		log.Fatalf(
			"memcached is not reachable — did you run `docker compose up -d memcached`? %v",
			pingErr,
		)
	}

	client := httpclient.NewHTTPClient(httpclient.WithCache(cache, runtime.NumCPU()-1))
	// Close drains the pool that writes cache entries in the background.
	defer client.Close()

	for range 3 {
		response, getErr := client.Get[UserResponse](ctx, server.URL)
		if getErr != nil {
			log.Fatalf("network error: %v", getErr)
		}
		if !response.IsSuccess() {
			log.Fatalf("unexpected status %d: %s", response.StatusCode(), response.Body())
		}

		user := response.Data()
		fmt.Printf(
			"  [%d] %s <%s> (origin hits: %d)\n",
			user.ID,
			user.Name,
			user.Email,
			hits.Load(),
		)
	}

	fmt.Println("\nEntries live in Memcached, so a second run of this example starts already warm.")
}
