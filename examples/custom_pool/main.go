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

func main() {
	// Custom pool tuned for a service that talks to a single host intensively
	pool := httpclient.NewConnectionPoolWithOptions(httpclient.ConnectionPoolOptions{
		MaxIdleConns:        10,
		MaxIdleConnsPerHost: 10,
		MaxConnsPerHost:     20,
		IdleConnTimeout:     30 * time.Second,
	})

	client := httpclient.NewHTTPClient(
		httpclient.WithConnectionPool(pool),
	)

	response, err := client.Get[[]User](
		context.Background(),
		"https://gorest.co.in/public/v2/users",
	)
	if err != nil {
		log.Fatalf("network error: %v", err)
	}

	if !response.IsSuccess() {
		log.Fatalf("unexpected status %d: %s", response.StatusCode(), response.Body())
	}

	users := response.Data()
	fmt.Printf("fetched %d users (via custom pool)\n", len(users))
}
