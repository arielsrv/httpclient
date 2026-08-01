package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/arielsrv/httpclient"
)

func main() {
	client := httpclient.NewHTTPClient()

	// Download a small public PNG — Data() returns the raw []byte directly,
	// no codec unmarshaling involved.
	const url = "https://www.google.com/favicon.ico"

	resp, err := client.Get[[]byte](context.Background(), url, httpclient.AcceptBinary())
	if err != nil {
		log.Fatalf("network error: %v", err)
	}
	if !resp.IsSuccess() {
		log.Fatalf("unexpected status %d: %s", resp.StatusCode(), resp.Body())
	}

	data := resp.Data()
	fmt.Printf("downloaded %d bytes (Content-Type: %s)\n",
		len(data), resp.Headers().Get("Content-Type"))

	if err := os.WriteFile("favicon.ico", data, 0o600); err != nil {
		log.Fatalf("writing file: %v", err)
	}
	fmt.Println("saved to favicon.ico")
}
