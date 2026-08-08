package main

import (
	"context"
	"encoding/xml"
	"fmt"
	"log"

	"github.com/arielsrv/httpclient"
)

// Order is sent as JSON, XML, and form — the struct tags drive each encoding.
// XMLName is form:"-" so the form encoder skips it (it is not a real field).
type Order struct {
	XMLName xml.Name `json:"-"    xml:"order" form:"-"`
	Item    string   `json:"item" xml:"item"  form:"item"`
	ID      int      `json:"id"   xml:"id"    form:"id"`
}

// httpbinResponse captures the fields of httpbin.org/post's echo that we care
// about: how the request arrived on the server side.
type httpbinResponse struct {
	JSON    map[string]any    `json:"json"` // parsed body when JSON
	Form    map[string]any    `json:"form"` // parsed body when form-encoded
	Headers map[string]string `json:"headers"`
	Data    string            `json:"data"` // raw body (used for XML)
}

func main() {
	client := httpclient.NewHTTPClient()
	ctx := context.Background()
	const endpoint = "https://httpbin.org/post"

	order := Order{ID: 42, Item: "book"}

	// One client, three content types, one struct — set Content-Type in the
	// header and the client picks the right codec automatically.
	// Fire all three concurrently; total ≈ the slowest request.
	jsonFuture := client.PostAsync[httpbinResponse](ctx, endpoint, order, httpclient.AsJSON())
	xmlFuture := client.PostAsync[httpbinResponse](ctx, endpoint, order, httpclient.AsXML())
	formFuture := client.PostAsync[httpbinResponse](ctx, endpoint, order, httpclient.AsForm())

	jsonResp := await(jsonFuture, "JSON")
	fmt.Printf("JSON  → Content-Type: %s | server parsed: %v\n",
		jsonResp.Data().Headers["Content-Type"], jsonResp.Data().JSON)

	xmlResp := await(xmlFuture, "XML")
	fmt.Printf("XML   → Content-Type: %s | raw body: %s\n",
		xmlResp.Data().Headers["Content-Type"], xmlResp.Data().Data)

	// httpbin echoes JSON and form bodies parsed (under "json"/"form") and leaves
	// "data" empty; only bodies it doesn't parse (like XML above) come back raw.
	formResp := await(formFuture, "Form")
	fmt.Printf(
		"Form  → Content-Type: %s | server parsed: %v (raw echoed under \"form\", not \"data\")\n",
		formResp.Data().Headers["Content-Type"],
		formResp.Data().Form,
	)
}

func await(
	f *httpclient.Future[httpbinResponse],
	label string,
) *httpclient.HTTPResponse[httpbinResponse] {
	resp, err := f.Await()
	if err != nil {
		log.Fatalf("%s network error: %v", label, err)
	}
	if !resp.IsSuccess() {
		log.Fatalf("%s failed %d: %s", label, resp.StatusCode(), resp.Body())
	}
	return resp
}
