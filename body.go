package httpclient

import (
	"fmt"
	"net/url"
	"reflect"
	"strconv"
	"strings"
)

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

// Form encodes a struct as an application/x-www-form-urlencoded request body
// using `form` tags, e.g. `form:"user_id,omitempty"` or `form:"-"` to skip.
// Fields without a tag use the field name. It handles scalar fields and slices
// (repeated key), dereferences pointers (nil pointers are skipped), and returns
// an error for nested structs and maps, which have no canonical form encoding.
func Form(v any) Body {
	values, err := structToValues(v)
	if err != nil {
		return Body{err: err}
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

func structToValues(v any) (url.Values, error) {
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil, fmt.Errorf("form: nil pointer")
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return nil, fmt.Errorf("form: expected struct, got %s", rv.Kind())
	}

	out := url.Values{}
	rt := rv.Type()
	for i := 0; i < rt.NumField(); i++ {
		field := rt.Field(i)
		if !field.IsExported() {
			continue
		}

		name, omitempty := parseFormTag(field)
		if name == "-" {
			continue
		}

		fv := rv.Field(i)
		for fv.Kind() == reflect.Pointer {
			if fv.IsNil() {
				fv = reflect.Value{}
				break
			}
			fv = fv.Elem()
		}
		if !fv.IsValid() { // nil pointer
			continue
		}
		if omitempty && fv.IsZero() {
			continue
		}

		if err := encodeFormField(out, name, fv); err != nil {
			return nil, fmt.Errorf("form: field %q: %w", name, err)
		}
	}
	return out, nil
}

func parseFormTag(field reflect.StructField) (name string, omitempty bool) {
	tag := field.Tag.Get("form")
	if tag == "" {
		return field.Name, false
	}
	parts := strings.Split(tag, ",")
	name = parts[0]
	if name == "" {
		name = field.Name
	}
	for _, opt := range parts[1:] {
		if opt == "omitempty" {
			omitempty = true
		}
	}
	return name, omitempty
}

func encodeFormField(out url.Values, name string, fv reflect.Value) error {
	if fv.Kind() == reflect.Slice || fv.Kind() == reflect.Array {
		for i := 0; i < fv.Len(); i++ {
			elem := fv.Index(i)
			for elem.Kind() == reflect.Pointer {
				if elem.IsNil() {
					elem = reflect.Value{}
					break
				}
				elem = elem.Elem()
			}
			if !elem.IsValid() {
				continue
			}
			s, err := scalarToString(elem)
			if err != nil {
				return err
			}
			out.Add(name, s)
		}
		return nil
	}

	s, err := scalarToString(fv)
	if err != nil {
		return err
	}
	out.Add(name, s)
	return nil
}

func scalarToString(fv reflect.Value) (string, error) {
	switch fv.Kind() {
	case reflect.String:
		return fv.String(), nil
	case reflect.Bool:
		return strconv.FormatBool(fv.Bool()), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(fv.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(fv.Uint(), 10), nil
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(fv.Float(), 'g', -1, 64), nil
	case reflect.Struct, reflect.Map:
		return "", fmt.Errorf("unsupported nested %s", fv.Kind())
	default:
		return "", fmt.Errorf("unsupported kind %s", fv.Kind())
	}
}
