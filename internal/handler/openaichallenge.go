package handler

import (
	"net/http"
	"strings"
)

// RegisterOpenAIAppsChallenge serves the OpenAI plugin portal's domain
// verification token at /.well-known/openai-apps-challenge: the token and
// nothing else, as plain text. The portal requires exactly that body (not
// JSON, not a list). With no token configured the path is a 404, as it was
// before this existed.
func RegisterOpenAIAppsChallenge(mux *http.ServeMux, token string) {
	token = strings.TrimSpace(token)
	mux.HandleFunc("GET /.well-known/openai-apps-challenge", func(w http.ResponseWriter, r *http.Request) {
		if token == "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write([]byte(token))
	})
}
