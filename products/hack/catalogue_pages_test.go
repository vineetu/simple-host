package hack

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/handler"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/sites/siteshttp"
	"github.com/vsriram/simple-host/host/web"
	productedition "github.com/vsriram/simple-host/products/hack/edition"
	"github.com/vsriram/simple-host/products/hack/pages"
	hackassets "github.com/vsriram/simple-host/products/hack/webassets"
)

// TestWriteCataloguePages is not a check: with SH_HACK_PAGES set to a folder
// it writes Simple Hack's server-written pages there for the web app's
// catalogue (make hack-web-pages): the marketing pages and the status pages,
// as simple-hack.app serves them, then every file those pages (and the event
// pages the public package wrote there first) load from this server, so the
// catalogue opens them as they are. The stylesheets built from the tokens
// (hack-pages.css, hack-event.css) are left to the catalogue's own build.
func TestWriteCataloguePages(t *testing.T) {
	dir := os.Getenv("SH_HACK_PAGES")
	if dir == "" {
		t.Skip("SH_HACK_PAGES not set")
	}
	sh, _, _ := handler.NewSiteHandlers(productedition.ForKind(productedition.Hack), "", nil, nil, nil, "simple-hack.app", "sites.simple-hack.app", "", "", "", "", nil, 0, "", nil, nil)
	assets := Extension().Assets(sh)
	sh.SetProductAssets(assets.Replace, assets.Own, assets.Files, assets.Skills, assets.Hooks, assets.Rewrite)
	mux := http.NewServeMux()
	assets.Routes(mux, sh.AssetServices())
	pages.RegisterHackHome(mux, sh.Pages(), pages.NewAssets(sh.AssetServices(), sh.Pages().InkBaseURL()))
	siteshttp.RegisterUIRoutes(mux, "https://simple-hack.app", sh)
	get := func(p string) (w *httptest.ResponseRecorder) {
		w = httptest.NewRecorder()
		// A page address that needs the database (a person's showcase) is not a file.
		defer func() {
			if recover() != nil {
				w = httptest.NewRecorder()
				w.Code = http.StatusNotFound
			}
		}()
		mux.ServeHTTP(w, httptest.NewRequest("GET", "https://simple-hack.app"+p, nil))
		return w
	}
	// Status pages link the main origin absolutely; the catalogue is that origin.
	local := func(b []byte) []byte { return []byte(strings.ReplaceAll(string(b), `"https://simple-hack.app/`, `"/`)) }
	save := func(name string, body []byte) {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for name, p := range map[string]string{
		"home": "/", "hackathons": "/hackathons", "get-started": "/get-started", "companies": "/companies",
		"watch": "/watch", "features": "/features.html", "setup": "/setup", "notfound": "/no-such-page.html",
	} {
		w := get(p)
		if w.Code != http.StatusOK && name != "notfound" {
			t.Fatalf("%s: %d", p, w.Code)
		}
		save(name+".html", w.Body.Bytes())
	}
	p := sh.Pages()
	r := httptest.NewRequest("GET", "https://simple-hack.app/x", nil)
	r = r.WithContext(r.Context())
	for name, page := range map[string]string{
		"status-takendown": web.TakedownPage,
		"status-offline":   web.OfflinePage,
	} {
		save(name+".html", local(httpx.StampNonce(r, p.HostedStatusPage(page))))
	}
	w := httptest.NewRecorder()
	p.WriteMessagePage(w, r, "", http.StatusNotFound, "This link has already been used or has expired", "Ask for a new one from the page you came from.", "/", "Back to Simple Hack", "")
	save("status-message.html", local(w.Body.Bytes()))
	w = httptest.NewRecorder()
	web.RenderServiceErrorPage(w, r, p)
	save("status-error.html", local(w.Body.Bytes()))

	// Every file the pages load from this server, and what their stylesheets load.
	ref := regexp.MustCompile(`(?:href|src|poster)="(/[^"/][^"]*)"|url\(['"]?([^'")]+)['"]?\)`)
	seen := map[string]bool{"/hack-pages.css": true, "/hack-event.css": true}
	var fetch func(body []byte, from string)
	fetch = func(body []byte, from string) {
		for _, m := range ref.FindAllSubmatch(body, -1) {
			u := string(m[1]) + string(m[2])
			if strings.Contains(u, ":") || strings.HasPrefix(u, "#") || strings.HasPrefix(u, "//") {
				continue // data:, another origin, or a fragment
			}
			if !strings.HasPrefix(u, "/") {
				u = path.Join(path.Dir(from), u) // relative to the stylesheet
			}
			if i := strings.IndexAny(u, "?#"); i >= 0 {
				u = u[:i]
			}
			if seen[u] || strings.HasSuffix(u, "/") || strings.HasSuffix(u, ".zip") || strings.HasPrefix(u, "/catalogue") {
				continue
			}
			seen[u] = true
			w := get(u)
			if w.Code != http.StatusOK {
				continue // a link to another page, not a file the page loads
			}
			ct := w.Header().Get("Content-Type")
			if strings.HasPrefix(ct, "text/html") {
				continue
			}
			save(strings.TrimPrefix(path.Clean(u), "/"), w.Body.Bytes())
			if strings.HasPrefix(ct, "text/css") {
				fetch(w.Body.Bytes(), u)
			}
		}
	}
	// The films and their narration are chosen by the pages' scripts, so no tag names them.
	extra := []string{"/hack-watch-judge.mp3", "/hack-watch-organizer.mp3", "/hack-watch-participant.mp3"}
	films, _ := fs.ReadDir(hackassets.Static, "static/film")
	for _, f := range films {
		extra = append(extra, "/film/"+f.Name())
	}
	for _, u := range extra {
		if w := get(u); w.Code == http.StatusOK {
			seen[u] = true
			save(strings.TrimPrefix(u, "/"), w.Body.Bytes())
		}
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.html"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		fetch(b, "/")
	}
}
