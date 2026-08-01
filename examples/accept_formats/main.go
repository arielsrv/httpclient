package main

import (
	"context"
	"fmt"
	"log"

	"github.com/arielsrv/httpclient"
)

// User maps a single entry from gorest.co.in /public/v2/users.
type User struct {
	ID     int    `json:"id"     xml:"id"`
	Name   string `json:"name"   xml:"name"`
	Email  string `json:"email"  xml:"email"`
	Gender string `json:"gender" xml:"gender"`
	Status string `json:"status" xml:"status"`
}

// UserList wraps the XML envelope: <objects type="array"><object>…</object></objects>.
// JSON responses use []User directly, so both types share the same User struct.
type UserList struct {
	Users []User `xml:"object"`
}

const apiURL = "https://gorest.co.in/public/v2/users"

func printUsers(users []User) {
	for _, u := range users {
		fmt.Printf("  [%d] %-40s %-10s %s\n", u.ID, u.Email, u.Gender, u.Status)
	}
}

func main() {
	client := httpclient.NewHTTPClient()
	ctx := context.Background()

	// --- JSON: server returns an array, decoded directly into []User ---
	jsonResp, err := client.Get[[]User](ctx, apiURL, httpclient.AcceptJSON())
	if err != nil {
		log.Fatalf("json request error: %v", err)
	}
	if !jsonResp.IsSuccess() {
		log.Fatalf("json unexpected status %d: %s", jsonResp.StatusCode(), jsonResp.Body())
	}

	fmt.Println("=== JSON response ===")
	printUsers(jsonResp.Data())

	// --- XML: server wraps users in <objects><object>…</object></objects> ---
	xmlResp, err := client.Get[UserList](ctx, apiURL, httpclient.AcceptXML())
	if err != nil {
		log.Fatalf("xml request error: %v", err)
	}
	if !xmlResp.IsSuccess() {
		log.Fatalf("xml unexpected status %d: %s", xmlResp.StatusCode(), xmlResp.Body())
	}

	fmt.Println("\n=== XML response ===")
	printUsers(xmlResp.Data().Users)
}
