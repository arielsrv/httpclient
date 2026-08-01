package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"

	"github.com/arielsrv/httpclient"
)

// CreateUser is the success payload the caller expects on 2xx.
type CreateUser struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// Problem models an RFC 7807 application/problem+json error body.
type Problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail"`
}

func main() {
	// Self-contained server: no public API reliably returns problem+json on
	// demand, so we stand one up locally. It rejects the request with 422 and an
	// RFC 7807 body under Content-Type: application/problem+json.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{
			"type": "https://example.com/probs/invalid-name",
			"title": "Invalid name",
			"status": 422,
			"detail": "name must not be empty"
		}`))
	}))
	defer server.Close()

	client := httpclient.NewHTTPClient()

	// The caller asks for the success type. err is only for network failures —
	// a 422 is a valid HTTP response, not an error here.
	resp, err := client.Post[CreateUser](context.Background(), server.URL, httpclient.JSON(CreateUser{Name: ""}))
	if err != nil {
		log.Fatalf("network error: %v", err)
	}

	if resp.IsSuccess() {
		fmt.Printf("created user %d (%s)\n", resp.Data().ID, resp.Data().Name)
		return
	}

	// Non-2xx: Data() (the success type) is empty. Decode the error body with
	// As. The "+json" suffix of application/problem+json is negotiated to the
	// JSON codec automatically, so As knows how to parse it.
	problem, err := resp.As[Problem]()
	if err != nil {
		log.Fatalf("could not decode problem body (%d): %s", resp.StatusCode(), resp.Body())
	}

	fmt.Printf("request rejected %d %s\n", resp.StatusCode(), resp.Headers().Get("Content-Type"))
	fmt.Printf("  title:  %s\n", problem.Title)
	fmt.Printf("  detail: %s\n", problem.Detail)
	fmt.Printf("  type:   %s\n", problem.Type)
}
