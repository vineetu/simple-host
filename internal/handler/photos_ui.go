package handler

import (
	"bytes"
	"net/http"
	"strconv"
	"strings"
)

func registerPhotosUI(mux *http.ServeMux, publicBaseURL string) {
	mux.Handle("GET /photos", photosHostOnly(accountAuthOnly(publicBaseURL, adminUICSP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Bitmap previews need blob URLs. Only the decoder worker compiles Wasm.
		policy := w.Header().Get("Content-Security-Policy")
		policy = strings.Replace(policy, "img-src 'self' data:", "img-src 'self' data: blob:", 1)
		w.Header().Set("Content-Security-Policy", policy+"; worker-src 'self'")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Robots-Tag", "noindex")
		page, err := chromePage("photos.html", chromeDataFor(r, ""))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		page = bytes.ReplaceAll(page, []byte("__PHOTO_MAX_BYTES__"), []byte(strconv.Itoa(photoLimit("PHOTO_MAX_BYTES", 1000000))))
		page = bytes.ReplaceAll(page, []byte("__PHOTO_MAX_DIMENSION__"), []byte(strconv.Itoa(photoLimit("PHOTO_MAX_DIMENSION", 2048))))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(stampNonce(r, page))
	})))))
	mux.Handle("GET /photo-codecs/v2/{file}", photosHostOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		file := r.PathValue("file")
		b, err := embeddedStatic.ReadFile("static/photo-codecs/v2/" + file)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		switch {
		case strings.HasSuffix(file, ".js"):
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		case strings.HasSuffix(file, ".wasm"):
			w.Header().Set("Content-Type", "application/wasm")
		default:
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		}
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self' 'wasm-unsafe-eval'; connect-src 'self'")
		w.Write(b)
	})))
}
