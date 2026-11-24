// Package serve answers a site's pages on every address of the site (route
// 33) and one published file on the platform (route 26). Before any file it
// asks the one visit decision. Then: clean links (/about, /about/ and
// /about.html reach one page), the site's _redirects, its own 404.html, and
// the visit count.
package serve

import (
	"fmt"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"

	"simplehost/app"
	"simplehost/core/web"
	sites "simplehost/sites/core"
)

type handler struct{ a *app.App }

// Mount adds routes 26 and 33.
func Mount(a *app.App) {
	h := &handler{a}
	a.Routes.Site.Handle("GET /", web.Func(h.page))
	a.Routes.Platform.Handle("GET /v1/sites/{site}/files/{path...}", web.Func(h.file))
}

// page is route 33: what a visitor sees at a path of a site.
func (h *handler) page(w http.ResponseWriter, r *http.Request) error {
	site := sites.PlaceOf(r).Site
	d, err := h.a.Sites.Visit(r, site)
	if err != nil {
		return err // the database is down: an error, never the page
	}
	if d != sites.Open {
		sites.GatePage(w, r, d, 0, "")
		return nil
	}
	if site.Access != "open" {
		// A locked site's pages are never kept by a shared cache.
		w.Header().Set("Cache-Control", "private, no-cache")
		w.Header().Add("Vary", "Cookie")
	}
	root, err := os.OpenRoot(h.a.Sites.VersionDir(site.ID, site.LiveVersion))
	if err != nil {
		http.Error(w, "Not found.", http.StatusNotFound) // nothing published yet
		return nil
	}
	defer root.Close()
	kind, file := resolve(root, r.URL.Path)
	switch kind {
	case none:
		if to, status, ok := redirect(root, site, r); ok {
			revalidate(w)
			http.Redirect(w, r, to, status)
			return nil
		}
		notFound(w, r, root)
		return nil
	case addSlash, dropSlash:
		to := "/" + strings.Trim(r.URL.EscapedPath(), "/")
		if kind == addSlash {
			to += "/"
		}
		if r.URL.RawQuery != "" {
			to += "?" + r.URL.RawQuery
		}
		revalidate(w)
		http.Redirect(w, r, to, http.StatusMovedPermanently)
		return nil
	}
	if !serveFile(w, r, root, file) {
		notFound(w, r, root)
		return nil
	}
	if r.Method == http.MethodGet && strings.HasSuffix(file, ".html") {
		h.count(r, site, "/"+strings.TrimSuffix(strings.TrimSuffix(file, "index.html"), ".html"))
	}
	return nil
}

// file is route 26: one published file, for anyone who may open the site.
func (h *handler) file(w http.ResponseWriter, r *http.Request) error {
	site, err := h.a.Sites.Of(r)
	if err != nil {
		return err
	}
	if h.a.Sites.Manage(r, site) != nil {
		d, err := h.a.Sites.Visit(r, site)
		if err != nil {
			return err
		}
		if err := d.Err(); err != nil {
			return err
		}
	}
	root, err := os.OpenRoot(h.a.Sites.VersionDir(site.ID, site.LiveVersion))
	if err != nil {
		return web.NotFound
	}
	defer root.Close()
	name := strings.TrimPrefix(path.Clean("/"+r.PathValue("path")), "/")
	if st, err := root.Stat(name); err == nil && st.IsDir() {
		name = path.Join(name, "index.html")
	}
	// A published file read here is data, never a page of the platform.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if name == "" || !serveFile(w, r, root, name) {
		return web.NotFound
	}
	return nil
}

const (
	none = iota
	serve
	addSlash  // a folder's page asked for without its slash
	dropSlash // a file's page asked for with a slash
)

// resolve finds the page for a request path: the file itself, the file plus
// .html, or the folder's index.html. A folder without its slash, or a file
// with one, is answered by a redirect to the other spelling.
func resolve(root *os.Root, p string) (int, string) {
	name := strings.TrimPrefix(path.Clean("/"+p), "/")
	regular := func(n string) bool {
		st, err := root.Stat(n)
		return err == nil && st.Mode().IsRegular()
	}
	index := path.Join(name, "index.html")
	if name == "" || name == "_redirects" {
		if name == "" && regular(index) {
			return serve, index
		}
		return none, ""
	}
	file := ""
	if regular(name) {
		file = name
	} else if !strings.HasSuffix(name, ".html") && regular(name+".html") {
		file = name + ".html"
	}
	switch {
	case strings.HasSuffix(p, "/") && regular(index):
		return serve, index
	case strings.HasSuffix(p, "/") && file != "":
		return dropSlash, file
	case strings.HasSuffix(p, "/"):
		return none, ""
	case file != "":
		return serve, file
	case regular(index):
		return addSlash, index
	}
	return none, ""
}

// serveFile answers with one regular file; the type comes from its name,
// and Last-Modified and ranges from the standard library.
func serveFile(w http.ResponseWriter, r *http.Request, root *os.Root, name string) bool {
	f, err := root.Open(name)
	if err != nil {
		return false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return false
	}
	http.ServeContent(w, r, st.Name(), st.ModTime(), f)
	return true
}

// notFound is the site's own 404.html when it has one.
func notFound(w http.ResponseWriter, r *http.Request, root *os.Root) {
	b, err := root.ReadFile("404.html")
	if err != nil {
		http.Error(w, "Not found.", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	if r.Method != http.MethodHead {
		w.Write(b)
	}
}

// revalidate lets the next publish change a redirect at once.
func revalidate(w http.ResponseWriter) {
	if w.Header().Get("Cache-Control") == "" {
		w.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")
	}
}

// redirects remembers each published version's parsed _redirects: versions
// never change, so a version is read once.
var redirects = struct {
	sync.Mutex
	m map[string][]sites.Redirect
}{m: map[string][]sites.Redirect{}}

// redirect is the _redirects line for a path no page answers, if any.
func redirect(root *os.Root, site *sites.Site, r *http.Request) (string, int, bool) {
	key := fmt.Sprintf("%s/%d", site.ID, site.LiveVersion)
	redirects.Lock()
	lines, ok := redirects.m[key]
	redirects.Unlock()
	if !ok {
		if b, err := root.ReadFile("_redirects"); err == nil {
			lines, _ = sites.ParseRedirects(string(b))
		}
		redirects.Lock()
		if len(redirects.m) > 4096 {
			clear(redirects.m)
		}
		redirects.m[key] = lines
		redirects.Unlock()
	}
	trim := func(s string) string {
		if s != "/" {
			return strings.TrimSuffix(s, "/")
		}
		return s
	}
	for _, rd := range lines {
		if trim(rd.From) != trim(r.URL.Path) || trim(rd.To) == trim(r.URL.Path) {
			continue
		}
		to := rd.To
		if r.URL.RawQuery != "" && !strings.Contains(to, "?") {
			to += "?" + r.URL.RawQuery
		}
		return to, rd.Status, true
	}
	return "", 0, false
}
