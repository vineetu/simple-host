package hackhttp

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"net/http"
	"path"
	"sync"
	"time"

	hackassets "github.com/vsriram/simple-host/products/hack/webassets"
)

// HackFilmTypes are the Hack landing film's files (static/film/ in the
// product's embed) and their types: H.264 MP4 first, and VP9 WebM for browsers
// built without H.264.
var HackFilmTypes = hackFilmTypes()

func hackFilmTypes() map[string]string {
	return map[string]string{
		"hack-film-390.mp4":         "video/mp4",
		"hack-film-1280.mp4":        "video/mp4",
		"hack-film-390.webm":        "video/webm",
		"hack-film-1280.webm":       "video/webm",
		"hack-film-390-poster.jpg":  "image/jpeg",
		"hack-film-1280-poster.jpg": "image/jpeg",
	}
}

// hackCompaniesFilmTypes are the /companies page's film files.
func hackCompaniesFilmTypes() map[string]string {
	return map[string]string{
		"hack-companies-film-390.mp4":         "video/mp4",
		"hack-companies-film-1280.mp4":        "video/mp4",
		"hack-companies-film-390.webm":        "video/webm",
		"hack-companies-film-1280.webm":       "video/webm",
		"hack-companies-film-390-poster.jpg":  "image/jpeg",
		"hack-companies-film-1280-poster.jpg": "image/jpeg",
	}
}

// hackFilmETags hashes each film file once, on first use.
var hackFilmETags = sync.OnceValue(func() map[string]string {
	tags := map[string]string{}
	for _, types := range []map[string]string{hackFilmTypes(), hackCompaniesFilmTypes()} {
		for name := range types {
			b, err := fs.ReadFile(hackassets.Static, path.Join("static/film", name))
			if err != nil {
				continue
			}
			sum := sha256.Sum256(b)
			tags[name] = `"` + hex.EncodeToString(sum[:8]) + `"`
		}
	}
	return tags
})

// RegisterHackFilm mounts Hack's film files at the addresses the platform's
// /film/{file} route answered while they were in its embed: one exact route per
// file, so the platform's route keeps every other film.
func RegisterHackFilm(mux *http.ServeMux) {
	for name, ctype := range hackFilmTypes() {
		mux.HandleFunc("GET /film/"+name, serveHackFilm(name, ctype))
	}
	for name, ctype := range hackCompaniesFilmTypes() {
		mux.HandleFunc("GET /film/"+name, serveHackFilm(name, ctype))
	}
}

func serveHackFilm(name, ctype string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f, err := hackassets.Static.Open(path.Join("static/film", name))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", ctype)
		w.Header().Set("Cache-Control", "public, max-age=604800")
		w.Header().Set("ETag", hackFilmETags()[name])
		http.ServeContent(w, r, name, time.Time{}, f.(io.ReadSeeker))
	}
}
