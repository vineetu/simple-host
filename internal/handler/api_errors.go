package handler

import (
	"net/http"
	"strings"
)

// APIErrors keeps unmatched API requests out of the website catch-alls. Looking
// up patterns before serving also catches method mismatches hidden by GET / or
// POST /, without buffering downloads or changing a route's own error response.
func APIErrors(mux *http.ServeMux, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isAPIPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		_, pattern := mux.Handler(r)
		if !apiFallbackPattern(pattern) {
			next.ServeHTTP(w, r)
			return
		}

		// These are the methods used by this service. Probe only route lookup,
		// never handlers, and exclude the website catch-alls from Allow.
		var allowed []string
		probe := r.Clone(r.Context())
		for _, method := range []string{"CONNECT", "DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT", "TRACE"} {
			probe.Method = method
			_, pattern := mux.Handler(probe)
			if !apiFallbackPattern(pattern) {
				allowed = append(allowed, method)
			}
		}
		status, code, message := http.StatusNotFound, "not_found", "API route not found"
		if len(allowed) != 0 {
			status, code, message = http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed"
			w.Header().Set("Allow", strings.Join(allowed, ", "))
		}
		writeJSON(w, status, errorResponse{
			Error: message, Code: code,
			Hint: "See https://simple-host.app/docs.html for supported routes and methods.",
		})
	})
}

func isAPIPath(path string) bool {
	return path == "/v1" || strings.HasPrefix(path, "/v1/") || path == "/mcp" || strings.HasPrefix(path, "/mcp/")
}

func apiFallbackPattern(pattern string) bool {
	return pattern == "" || pattern == "/" || pattern == "GET /" || pattern == "POST /"
}
