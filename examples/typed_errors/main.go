package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/arielsrv/httpclient"
)

type User struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// APIError models the structured error body that the API returns on non-2xx.
type APIError struct {
	Message string `json:"message"`
}

func main() {
	// WithTimeout bounds the whole request (dial + body read). It overrides the
	// 30s DefaultTimeout; pass WithTimeout(0) to disable it and rely on the context.
	client := httpclient.NewHTTPClient(httpclient.WithTimeout(5 * time.Second))
	ctx := context.Background()

	// Request a user that does not exist — the API answers with a non-2xx status
	// and a structured JSON error body instead of the User payload.
	resp, err := client.Get[User](ctx, "https://gorest.co.in/public/v2/users/0")
	if err != nil {
		log.Fatalf("network error: %v", err)
	}

	if resp.IsSuccess() {
		fmt.Printf("got user: %s <%s>\n", resp.Data().Name, resp.Data().Email)
		return
	}

	// Data() only covers the success type. Use As[E] to decode the error body
	// into a different type on demand.
	apiErr, err := resp.As[APIError]()
	if err != nil {
		log.Fatalf("could not decode error body (%d): %s", resp.StatusCode(), resp.Body())
	}

	fmt.Printf("request failed %d: %s\n", resp.StatusCode(), apiErr.Message)
}
