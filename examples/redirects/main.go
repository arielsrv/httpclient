package main

import (
	"context"
	"fmt"
	"log"

	"github.com/arielsrv/httpclient"
)

func main() {
	ctx := context.Background()
	redirectURL := "https://httpbingo.org/redirect-to?url=https://example.com"

	// WithFollowRedirects(true) — client follows the redirect and returns the final 200.
	// Download is used so the response body (HTML from example.com) is not
	// passed through a JSON codec — we only care about the status code here.
	fmt.Println("=== WithFollowRedirects(true) ===")
	following := httpclient.NewHTTPClient(httpclient.WithFollowRedirects(true))
	resp, err := following.Download(ctx, redirectURL)
	if err != nil {
		log.Fatalf("request error: %v", err)
	}
	fmt.Printf("  status: %d  (followed redirect to https://example.com)\n", resp.StatusCode())

	// WithFollowRedirects(false) — client stops at the first 3xx and exposes Location.
	fmt.Println("\n=== WithFollowRedirects(false) ===")
	noFollow := httpclient.NewHTTPClient(httpclient.WithFollowRedirects(false))
	resp2, err := noFollow.Download(ctx, redirectURL)
	if err != nil {
		log.Fatalf("request error: %v", err)
	}
	fmt.Printf("  status:   %d\n", resp2.StatusCode())
	fmt.Printf("  Location: %s\n", resp2.Headers().Get("Location"))
}
