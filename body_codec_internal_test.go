package httpclient

// White-box tests for body helpers and codec internals.
// These need access to unexported fields and types, so they live in the
// internal test package (package httpclient, not httpclient_test).

import (
	"encoding/xml"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- JSON / XML legacy body helpers ---

func TestJSON_EncodesPayload(t *testing.T) {
	t.Parallel()
	b := JSON(map[string]int{"x": 1})
	require.NoError(t, b.err)
	assert.Equal(t, mimeJSON, b.contentType)
	assert.JSONEq(t, `{"x":1}`, string(b.data))
}

func TestJSON_PropagatesEncodeError(t *testing.T) {
	t.Parallel()
	b := JSON(make(chan int)) // channels cannot be JSON-marshaled
	assert.Error(t, b.err)
}

func TestXML_EncodesPayload(t *testing.T) {
	t.Parallel()
	type item struct {
		XMLName xml.Name `xml:"item"`
		Name    string   `xml:"name"`
	}
	b := XML(item{Name: "test"})
	require.NoError(t, b.err)
	assert.Equal(t, mimeXML, b.contentType)
	assert.Contains(t, string(b.data), "<name>test</name>")
}

// --- Content-Type header helpers ---

func TestAsJSON_ReturnsContentTypeJSON(t *testing.T) {
	t.Parallel()
	h := AsJSON()
	assert.Equal(t, mimeJSON, h.Get(contentTypeHeader))
}

func TestAsXML_ReturnsContentTypeXML(t *testing.T) {
	t.Parallel()
	h := AsXML()
	assert.Equal(t, mimeXML, h.Get(contentTypeHeader))
}

func TestAsForm_ReturnsContentTypeForm(t *testing.T) {
	t.Parallel()
	h := AsForm()
	assert.Equal(t, mimeForm, h.Get(contentTypeHeader))
}

// --- Accept header helpers ---

func TestAcceptJSON_ReturnsAcceptJSON(t *testing.T) {
	t.Parallel()
	h := AcceptJSON()
	assert.Equal(t, mimeJSON, h.Get(acceptHeader))
}

func TestAcceptXML_ReturnsAcceptXML(t *testing.T) {
	t.Parallel()
	h := AcceptXML()
	assert.Equal(t, mimeXML, h.Get(acceptHeader))
}

// --- byteCodec ---

func TestByteCodec_ContentType(t *testing.T) {
	t.Parallel()
	assert.Equal(t, mimeBinary, byteCodec{}.ContentType())
}

func TestByteCodec_Marshal_ByteSlice(t *testing.T) {
	t.Parallel()
	in := []byte{0x01, 0x02, 0x03}
	out, err := byteCodec{}.Marshal(in)
	require.NoError(t, err)
	assert.Equal(t, in, out)
}

func TestByteCodec_Marshal_WrongType(t *testing.T) {
	t.Parallel()
	_, err := byteCodec{}.Marshal("not a byte slice")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "byteCodec")
}

func TestByteCodec_Unmarshal_WrongType(t *testing.T) {
	t.Parallel()
	var s string
	err := byteCodec{}.Unmarshal([]byte{1, 2, 3}, &s)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "byteCodec")
}
