package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"

	"github.com/arielsrv/httpclient"
)

func main() {
	ctx := context.Background()

	// target: the final destination
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintln(w, `{"arrived": true}`)
	}))
	defer target.Close()

	// redirect server issues 302 → target
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirect.Close()

	// WithFollowRedirects(true) — client follows the 302 and returns the final 200.
	fmt.Println("=== WithFollowRedirects(true) ===")
	following := httpclient.NewHTTPClient(httpclient.WithFollowRedirects(true))
	resp, err := following.Get[any](ctx, redirect.URL)
	if err != nil {
		log.Fatalf("request error: %v", err)
	}
	fmt.Printf("  status: %d  (followed redirect → %s)\n", resp.StatusCode(), target.URL)

	// WithFollowRedirects(false) — client stops at the 302 and exposes Location.
	fmt.Println("\n=== WithFollowRedirects(false) ===")
	noFollow := httpclient.NewHTTPClient(httpclient.WithFollowRedirects(false))
	resp2, err := noFollow.Get[any](ctx, redirect.URL)
	if err != nil {
		log.Fatalf("request error: %v", err)
	}
	fmt.Printf("  status:   %d\n", resp2.StatusCode())
	fmt.Printf("  Location: %s\n", resp2.Headers().Get("Location"))
}
