package handler

import "net/http"

// SecurityHeaders adds X-Content-Type-Options: nosniff to every response.
//
// It deliberately does NOT set X-Frame-Options, Strict-Transport-Security, or
// Referrer-Policy: in every real deployment a TLS-terminating proxy fronts this
// binary (the app port is not publicly reachable) and that proxy already sets
// those edge headers. Re-setting them here produced *conflicting duplicates*
// (e.g. two X-Frame-Options values, which browsers then ignore, and a weaker
// HSTS max-age shadowing the proxy's). nosniff is kept because identical
// duplicates are harmless and it's valuable on API JSON if ever served direct.
// The admin UI's Content-Security-Policy — which the proxy does NOT set — is
// applied separately in adminUICSP (ui.go); its frame-ancestors 'none'
// supersedes X-Frame-Options on modern browsers.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}
