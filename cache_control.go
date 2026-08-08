package httpclient

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	cacheControlHeader = "Cache-Control"
	expiresHeader      = "Expires"
	dateHeader         = "Date"

	directiveNoStore = "no-store"
	directiveNoCache = "no-cache"
	directiveMaxAge  = "max-age"
)

// cacheControl holds the directives of a Cache-Control header. Valueless
// directives (no-store, no-cache, …) map to an empty string.
type cacheControl map[string]string

// parseCacheControl splits a Cache-Control header into its directives. Names are
// lowercased and quoted values unquoted; malformed elements are skipped.
func parseCacheControl(header string) cacheControl {
	directives := make(cacheControl)
	if header == "" {
		return directives
	}

	for element := range strings.SplitSeq(header, ",") {
		name, value, hasValue := strings.Cut(strings.TrimSpace(element), "=")
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		if !hasValue {
			directives[name] = ""
			continue
		}
		directives[name] = strings.Trim(strings.TrimSpace(value), `"`)
	}

	return directives
}

// has reports whether a valueless directive such as no-store is present.
func (r cacheControl) has(directive string) bool {
	_, found := r[directive]
	return found
}

// duration returns the value of a delta-seconds directive such as max-age.
// The second result is false when the directive is absent or not a valid number.
func (r cacheControl) duration(directive string) (time.Duration, bool) {
	raw, found := r[directive]
	if !found {
		return 0, false
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds < 0 {
		return 0, false
	}
	return time.Duration(seconds) * time.Second, true
}

// freshnessFromExpires derives a lifetime from the Expires header, relative to
// the response Date (or to now when the server omits it). It returns false when
// Expires is absent, unparsable, or already in the past.
func freshnessFromExpires(headers http.Header) (time.Duration, bool) {
	raw := headers.Get(expiresHeader)
	if raw == "" {
		return 0, false
	}
	expires, err := http.ParseTime(raw)
	if err != nil {
		return 0, false
	}

	origin := time.Now()
	if date, dateErr := http.ParseTime(headers.Get(dateHeader)); dateErr == nil {
		origin = date
	}

	ttl := expires.Sub(origin)
	if ttl <= 0 {
		return 0, false
	}
	return ttl, true
}
