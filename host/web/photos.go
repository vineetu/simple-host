package web

import (
	"net/http"

	"github.com/vsriram/simple-host/host/httpx"
)

func PhotosHostOnly(enabled bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !enabled {
			if isAPIPath(r.URL.Path) {
				httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{Error: "photos are not offered here", Code: "not_offered"})
			} else {
				http.NotFound(w, r)
			}
			return
		}
		next.ServeHTTP(w, r)
	})
}
