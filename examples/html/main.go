package main

import (
	"context"
	"log"
	"runtime"

	"github.com/arielsrv/httpclient"
)

type UserResponse struct {
	Name   string `json:"name"`
	Email  string `json:"email"`
	Header string `json:"header"`
	ID     int    `json:"id"`
}

func main() {
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
	resourceURL := "https://syndicate.synthrone.com/df9g5m2kxcv7/ROY153637_M/latest/ROY153637_M.html"
	// err only represents network-level errors (connection refused, timeout, etc.)
	response, err := httpClient.Download(ctx, resourceURL)
	if err != nil {
		log.Fatalf("network error: %v", err)
	}

	// non-2xx responses are not errors — check IsSuccess() and inspect the body
	if !response.IsSuccess() {
		log.Fatalf("unexpected status %d: %s", response.StatusCode(), response.Body())
	}

	// Same URL: served from the cache even though the headers differ — the entry
	// is keyed by URL, and the echoed X-Request-Id proves it is the stored copy.
	response, err = httpClient.Download(ctx, resourceURL)
	if err != nil {
		log.Fatalf("network error: %v", err)
	}

	if !response.IsSuccess() {
		log.Fatalf("unexpected status %d: %s", response.StatusCode(), response.Body())
	}
}
