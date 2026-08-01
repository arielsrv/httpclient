package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"slices"

	"github.com/arielsrv/httpclient"
)

type UserResponse struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

func main() {
	httpClient := httpclient.NewHTTPClient()

	baseURL := "https://gorest.co.in"
	apiURL := fmt.Sprintf("%s/public/v2/users", baseURL)

	// err only represents network-level errors (connection refused, timeout, etc.)
	response, err := httpClient.Get[[]UserResponse](context.Background(), apiURL, http.Header{
		"X-Request-Id": []string{"abc-123"},
	})
	if err != nil {
		log.Fatalf("network error: %v", err)
	}

	// non-2xx responses are not errors — check IsSuccess() and inspect the body
	if !response.IsSuccess() {
		log.Fatalf("unexpected status %d: %s", response.StatusCode(), response.Body())
	}

	// Data() returns the deserialized response body
	for user := range slices.Values(response.Data()) {
		fmt.Printf("  [%d] %s <%s>\n", user.ID, user.Name, user.Email)
	}
}
