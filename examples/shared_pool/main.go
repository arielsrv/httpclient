package main

import (
	"context"
	"fmt"
	"log"

	"github.com/arielsrv/httpclient"
)

type User struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	ID    int    `json:"id"`
}

type Post struct {
	Title  string `json:"title"`
	ID     int    `json:"id"`
	UserID int    `json:"user_id"`
}

func main() {
	// A single pool shared across multiple clients — connections are reused
	// across different API clients without each one managing its own transport.
	sharedPool := httpclient.NewConnectionPool()

	usersClient := httpclient.NewHTTPClient(httpclient.WithConnectionPool(sharedPool))
	postsClient := httpclient.NewHTTPClient(httpclient.WithConnectionPool(sharedPool))

	ctx := context.Background()

	usersResp, err := usersClient.Get[[]User](ctx, "https://gorest.co.in/public/v2/users")
	if err != nil {
		log.Fatalf("network error: %v", err)
	}
	if !usersResp.IsSuccess() {
		log.Fatalf("users request failed %d: %s", usersResp.StatusCode(), usersResp.Body())
	}

	postsResp, err := postsClient.Get[[]Post](ctx, "https://gorest.co.in/public/v2/posts")
	if err != nil {
		log.Fatalf("network error: %v", err)
	}
	if !postsResp.IsSuccess() {
		log.Fatalf("posts request failed %d: %s", postsResp.StatusCode(), postsResp.Body())
	}

	fmt.Printf(
		"fetched %d users and %d posts (shared pool)\n",
		len(usersResp.Data()),
		len(postsResp.Data()),
	)
}
