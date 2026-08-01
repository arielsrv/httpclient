package httpclient

import (
	"fmt"
	"mime"
	"net/http"

	"github.com/go-playground/form/v4"
)

// Body is an encoded request payload together with its Content-Type. Build one
// with JSON, XML, or Form. The zero value represents no body (used by Get/Delete).
//
// The payload is encoded eagerly; any encoding error is deferred and surfaces
// when the request is sent. Retaining the encoded bytes also makes the body
// safe to reuse (e.g. on a future retry) without re-serializing.
type Body struct {
	err         error
	contentType string
	data        []byte
}

// formEncoder is safe for concurrent use and caches struct metadata across calls.
var formEncoder = form.NewEncoder()

// JSON encodes v as an application/json request body.
func JSON(v any) Body { return encodeBody(jsonCodec{}, v) }

// XML encodes v as an application/xml request body.
func XML(v any) Body { return encodeBody(xmlCodec{}, v) }

// Form encodes a struct as an application/x-www-form-urlencoded request body
// using `form` tags (via github.com/go-playground/form). Fields without a tag
// use the field name; `form:"-"` skips a field. Nested structs and slices are
// supported through the library's dotted/indexed key notation.
func Form(v any) Body {
	values, err := formEncoder.Encode(v)
	if err != nil {
		return Body{err: fmt.Errorf("form: %w", err)}
	}
	return Body{
		contentType: mimeForm,
		data:        []byte(values.Encode()),
	}
}

// AsJSON returns an [http.Header] with Content-Type set to application/json.
// Pass it to Post/Put/Patch to encode the payload as JSON (this is also the default).
func AsJSON() http.Header { return http.Header{contentTypeHeader: []string{mimeJSON}} }

// AsXML returns an [http.Header] with Content-Type set to application/xml.
// Pass it to Post/Put/Patch to encode the payload as XML.
func AsXML() http.Header { return http.Header{contentTypeHeader: []string{mimeXML}} }

// AsForm returns an [http.Header] with Content-Type set to application/x-www-form-urlencoded.
// Pass it to Post/Put/Patch to encode the payload as a form.
func AsForm() http.Header { return http.Header{contentTypeHeader: []string{mimeForm}} }

// AcceptJSON returns an [http.Header] with Accept set to application/json.
// Pass it to Get/Post/etc. to request a JSON response from the server.
func AcceptJSON() http.Header { return http.Header{acceptHeader: []string{mimeJSON}} }

// AcceptXML returns an [http.Header] with Accept set to application/xml.
// Pass it to Get/Post/etc. to request an XML response from the server.
func AcceptXML() http.Header { return http.Header{acceptHeader: []string{mimeXML}} }

// AcceptBinary returns an [http.Header] with Accept set to application/octet-stream.
// Use with Get[[]byte] to download raw binary content — the response bytes are
// returned directly in Data() without any codec unmarshaling.
func AcceptBinary() http.Header { return http.Header{acceptHeader: []string{mimeBinary}} }

// found in headers. Falls back to JSON when no Content-Type is present.
// application/x-www-form-urlencoded is handled via Form encoding.
func bodyFromHeaders(v any, headers []http.Header) Body {
	ct := contentTypeFromHeaders(headers)
	if ct == "" {
		return encodeBody(jsonCodec{}, v)
	}

	mediaType, _, _ := mime.ParseMediaType(ct)
	if mediaType == mimeForm {
		return Form(v)
	}

	return encodeBody(codecForContentType(ct), v)
}

// contentTypeFromHeaders returns the first Content-Type value found across all
// provided header maps, or empty string if none is set.
func contentTypeFromHeaders(headers []http.Header) string {
	for _, h := range headers {
		if ct := h.Get(contentTypeHeader); ct != "" {
			return ct
		}
	}
	return ""
}

func encodeBody(c Codec, v any) Body {
	data, err := c.Marshal(v)
	return Body{contentType: c.ContentType(), data: data, err: err}
}
