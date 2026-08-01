package httpclient

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"mime"
	"strings"
)

const (
	contentTypeHeader = "Content-Type"
	acceptHeader      = "Accept"
	mimeJSON          = "application/json"
	mimeXML           = "application/xml"
	mimeForm          = "application/x-www-form-urlencoded"
	mimeBinary        = "application/octet-stream"
)

// Codec (de)serializes a single media type. Request bodies are encoded with the
// codec chosen by the Body wrapper (JSON/XML/Form); responses are decoded with
// the codec negotiated from the response Content-Type.
type Codec interface {
	ContentType() string
	Marshal(v any) ([]byte, error)
	Unmarshal(data []byte, v any) error
}

type jsonCodec struct{}

func (jsonCodec) ContentType() string                { return mimeJSON }
func (jsonCodec) Marshal(v any) ([]byte, error)      { return json.Marshal(v) }
func (jsonCodec) Unmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }

type xmlCodec struct{}

func (xmlCodec) ContentType() string                { return mimeXML }
func (xmlCodec) Marshal(v any) ([]byte, error)      { return xml.Marshal(v) }
func (xmlCodec) Unmarshal(data []byte, v any) error { return xml.Unmarshal(data, v) }

type byteCodec struct{}

func (byteCodec) ContentType() string { return mimeBinary }
func (byteCodec) Marshal(v any) ([]byte, error) {
	b, ok := v.([]byte)
	if !ok {
		return nil, fmt.Errorf("byteCodec: value must be []byte, got %T", v)
	}
	return b, nil
}

func (byteCodec) Unmarshal(data []byte, v any) error {
	ptr, ok := v.(*[]byte)
	if !ok {
		return fmt.Errorf("byteCodec: target must be *[]byte, got %T", v)
	}
	*ptr = data
	return nil
}

// by defaultCodec is used when a response omits Content-Type or advertises an
// unrecognized media type. JSON keeps backward compatibility with the original
// hardcoded behavior.
var defaultCodec Codec = jsonCodec{}

// codecRegistry maps a media type to its codec for response content negotiation.
var codecRegistry = map[string]Codec{
	mimeJSON:   jsonCodec{},
	mimeXML:    xmlCodec{},
	"text/xml": xmlCodec{},
	mimeBinary: byteCodec{},
}

// codecForContentType negotiates a response codec from a Content-Type header,
// tolerating parameters like "; charset=utf-8". Falls back to defaultCodec.
func codecForContentType(header string) Codec {
	if header == "" {
		return defaultCodec
	}
	mediaType, _, err := mime.ParseMediaType(header)
	if err != nil {
		return defaultCodec
	}
	if c, ok := codecRegistry[mediaType]; ok {
		return c
	}
	// Structured syntax suffixes (RFC 6839): application/problem+json,
	// application/vnd.api+json, application/problem+xml, etc.
	switch {
	case strings.HasSuffix(mediaType, "+json"):
		return jsonCodec{}
	case strings.HasSuffix(mediaType, "+xml"):
		return xmlCodec{}
	}
	return defaultCodec
}
