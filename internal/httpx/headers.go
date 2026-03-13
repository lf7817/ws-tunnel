package httpx

import (
	"net/http"
	"strings"
)

var hopByHopHeaders = map[string]struct{}{
	"connection":        {},
	"proxy-connection":  {},
	"keep-alive":        {},
	"te":                {},
	"trailer":           {},
	"transfer-encoding": {},
	"upgrade":           {},
}

func IsHopByHopHeader(key string) bool {
	_, ok := hopByHopHeaders[strings.ToLower(key)]
	return ok
}

// FirstValueHeaders converts http.Header -> map[string]string using only the first value.
// This matches the current tunnel protocol shape (map[string]string).
func FirstValueHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, vals := range h {
		if k == "" || len(vals) == 0 {
			continue
		}
		if IsHopByHopHeader(k) {
			continue
		}
		kl := strings.ToLower(k)
		if kl == "content-length" {
			continue
		}
		out[k] = vals[0]
	}
	return out
}

func WriteResponseHeaders(dst http.Header, src map[string]string) {
	for k, v := range src {
		if k == "" {
			continue
		}
		if IsHopByHopHeader(k) {
			continue
		}
		if strings.ToLower(k) == "content-length" {
			continue
		}
		// Note: Set-Cookie is single-value under current protocol limitation.
		dst.Set(k, v)
	}
}
