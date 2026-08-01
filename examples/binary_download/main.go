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

	// Download returns raw []byte directly — no Content-Type negotiation needed.
	const url = "https://www.google.com/favicon.ico"

	resp, err := client.Download(context.Background(), url)
	if err != nil {
		log.Fatalf("network error: %v", err)
	}
	if !resp.IsSuccess() {
		log.Fatalf("unexpected status %d: %s", resp.StatusCode(), resp.Body())
	}

	data := resp.Data()
	fmt.Printf("downloaded %d bytes (Content-Type: %s)\n",
		len(data), resp.Headers().Get("Content-Type"))

	if err = os.WriteFile("favicon.ico", data, 0o600); err != nil {
		log.Fatalf("writing file: %v", err)
	}
	fmt.Println("saved to favicon.ico")
}
