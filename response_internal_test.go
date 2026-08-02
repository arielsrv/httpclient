package httpclient

import "testing"

// TestAs_NilCodecFallsBackToJSON covers the defensive nil-codec branch in As.
// A response produced by doRequest always carries a codec, so this path is only
// reachable on a manually-built (zero-ish) HTTPResponse — exercised here as a
// white-box test since it can set the unexported fields.
func TestAs_NilCodecFallsBackToJSON(t *testing.T) {
	t.Parallel()
	resp := HTTPResponse[any]{bodyBytes: []byte(`{"x":1}`), codec: nil}

	out, err := resp.As[map[string]int]()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["x"] != 1 {
		t.Fatalf("got %v, want map[x:1]", out)
	}
}
