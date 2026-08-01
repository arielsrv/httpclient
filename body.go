package httpclient

import (
	"fmt"

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
		contentType: "application/x-www-form-urlencoded",
		data:        []byte(values.Encode()),
	}
}

func encodeBody(c Codec, v any) Body {
	data, err := c.Marshal(v)
	return Body{contentType: c.ContentType(), data: data, err: err}
}
