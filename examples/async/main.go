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

type Post struct {
	ID     int    `json:"id"`
	Title  string `json:"title"`
	UserID int    `json:"user_id"`
}

func main() {
	client := httpclient.NewHTTPClient()
	ctx := context.Background()

	// Fire both requests concurrently — neither blocks the other
	usersFuture := client.GetAsync[[]User](ctx, "https://gorest.co.in/public/v2/users")
	postsFuture := client.GetAsync[[]Post](ctx, "https://gorest.co.in/public/v2/posts")

	// Await resolves each future — total time ≈ slowest request, not the sum
	usersResp, err := usersFuture.Await()
	if err != nil {
		log.Fatalf("users network error: %v", err)
	}
	if !usersResp.IsSuccess() {
		log.Fatalf("users failed %d: %s", usersResp.StatusCode(), usersResp.Body())
	}

	postsResp, err := postsFuture.Await()
	if err != nil {
		log.Fatalf("posts network error: %v", err)
	}
	if !postsResp.IsSuccess() {
		log.Fatalf("posts failed %d: %s", postsResp.StatusCode(), postsResp.Body())
	}

	fmt.Printf(
		"fetched %d users and %d posts concurrently\n",
		len(usersResp.Data()),
		len(postsResp.Data()),
	)

	// Cancellation: context timeout cancels the in-flight request
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	future := client.GetAsync[[]User](ctx, "https://gorest.co.in/public/v2/users")
	_, err = future.Await()
	if err != nil {
		fmt.Printf("request cancelled as expected: %v\n", err)
	}
}
