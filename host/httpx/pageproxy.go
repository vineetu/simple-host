package httpx

import (
	"context"
	"maps"
	"net/http"
)

type pageAPIUnavailableKey struct{}

// TryPageAPI streams the API's response when it can answer. On a transport
// or server error it restores the local page headers and leaves the response unwritten.
func TryPageAPI(w http.ResponseWriter, r *http.Request, api http.Handler) bool {
	headers := w.Header().Clone()
	unavailable := false
	ctx := context.WithValue(r.Context(), pageAPIUnavailableKey{}, func() { unavailable = true })
	api.ServeHTTP(w, r.WithContext(ctx))
	if unavailable {
		clear(w.Header())
		maps.Copy(w.Header(), headers)
	}
	return !unavailable
}

// PageAPIFallback returns the callback for an unmarked page whose API request failed.
// A mandatory hand-off has no fallback and must forward the API error.
func PageAPIFallback(r *http.Request) func() {
	fallback, _ := r.Context().Value(pageAPIUnavailableKey{}).(func())
	return fallback
}
