package httpclient

import "net/url"

// Body is an encoded request payload together with its Content-Type. Build one
// with JSON, XML, or Form. The zero value represents no body (used by Get/Delete).
//
// The payload is encoded eagerly; any encoding error is deferred and surfaces
// when the request is sent. Retaining the encoded bytes also makes the body
// safe to reuse (e.g. on a future retry) without re-serializing.
type Body struct {
	contentType string
	data        []byte
	err         error
}

// JSON encodes v as an application/json request body.
func JSON(v any) Body { return encodeBody(jsonCodec{}, v) }

// XML encodes v as an application/xml request body.
func XML(v any) Body { return encodeBody(xmlCodec{}, v) }

// Form encodes values as an application/x-www-form-urlencoded request body.
// It takes url.Values rather than an arbitrary struct because form encoding has
// no struct marshaler in the standard library.
func Form(values url.Values) Body {
	return Body{
		contentType: "application/x-www-form-urlencoded",
		data:        []byte(values.Encode()),
	}
}

func encodeBody(c Codec, v any) Body {
	data, err := c.Marshal(v)
	return Body{contentType: c.ContentType(), data: data, err: err}
}
