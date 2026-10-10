package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"path"
	"sync"
	"time"
)

// filmTypes are the landing film's files (static/film/) and their types: H.264
// MP4 first, and VP9 WebM for browsers built without H.264. The
// type is set here rather than read from the system's mime table, which a
// small container image may not have.
var filmTypes = map[string]string{
	"host-film-390.mp4":         "video/mp4",
	"host-film-1280.mp4":        "video/mp4",
	"host-film-390.webm":        "video/webm",
	"host-film-1280.webm":       "video/webm",
	"host-film-390-poster.jpg":  "image/jpeg",
	"host-film-1280-poster.jpg": "image/jpeg",
}

// serveFilm serves the landing film from the binary. http.ServeContent
// answers Range requests with 206, which iPhone Safari needs before it plays
// or seeks a video. The page asks for each file with a ?v= stamp, and the
// ETag lets a cached copy revalidate cheaply.
func serveFilm(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("file")
	ctype, ok := filmTypes[name]
	if !ok {
		http.NotFound(w, r)
		return
	}
	f, err := embeddedStatic.Open(path.Join("static/film", name))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "public, max-age=604800")
	w.Header().Set("ETag", filmETags()[name])
	http.ServeContent(w, r, name, time.Time{}, f.(io.ReadSeeker))
}

// filmETags hashes each film file once, on first use.
var filmETags = sync.OnceValue(func() map[string]string {
	tags := make(map[string]string, len(filmTypes))
	for name := range filmTypes {
		b, err := embeddedStatic.ReadFile(path.Join("static/film", name))
		if err != nil {
			continue
		}
		sum := sha256.Sum256(b)
		tags[name] = `"` + hex.EncodeToString(sum[:8]) + `"`
	}
	return tags
})
