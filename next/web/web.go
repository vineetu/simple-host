// Package web serves the service's own pages (route 61): home, install,
// docs, terms, privacy and support as static files; sign-in, the dashboard
// and admin from the owner app (web/app, built into web/dist); plus
// llms.txt, the one skill set, the page helper /auth.js (also on every site
// address as /_sh/page.js), the photo picker behind an upload link, the
// spam-check frame and the first-run setup of a self-hosted install.
// Pages are static files; they call the /v1 routes from the browser.
package web

import (
	"archive/zip"
	"bytes"
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"

	"simplehost/app"
	cw "simplehost/core/web"
	"simplehost/web/dist"
)

//go:embed static skill
var files embed.FS

var static, _ = fs.Sub(files, "static")

type handler struct {
	a        *app.App
	skills   []byte
	firstRun setupCode
}

// Mount adds the pages to the platform host and the page helper to every site address.
func Mount(a *app.App) {
	h := &handler{a: a, skills: zipSkills()}
	p := a.Routes.Platform
	p.Handle("GET /{$}", cw.Func(h.page))
	p.Handle("GET /{page}", cw.Func(h.page))
	p.Handle("GET /dashboard/{rest...}", cw.Func(h.appPage)) // a site's page, the account, a new site
	p.Handle("GET /assets/{file...}", cw.Func(h.asset))
	p.Handle("GET /app/{file...}", cw.Func(h.appFile))
	p.Handle("GET /index.html", cw.Func(h.appPage)) // the offline copy the app's service worker keeps
	p.Handle("GET /sw.js", cw.Func(h.appFile))
	p.Handle("GET /manifest.webmanifest", cw.Func(h.appFile))
	p.Handle("GET /pages.css", cw.Func(h.appFile))
	p.Handle("GET /auth.js", cw.Func(h.helper))
	p.Handle("GET /skills.zip", cw.Func(h.skillSet))
	p.Handle("GET /upload/{token}", cw.Func(h.picker))
	p.Handle("GET /setup", cw.Func(h.setupPage))
	p.Handle("POST /setup", cw.Func(h.setup))
	a.Routes.Site.Handle("GET /_sh/page.js", cw.Func(h.helper))
	if a.Config.SelfHosted {
		h.setupCode() // printed to the log at the first start
	}
}

// pages are the static pages under /{page}; appPages are the owner app's.
var pages = map[string]bool{"home": true, "install": true, "docs": true, "terms": true, "privacy": true,
	"support": true}
var appPages = map[string]bool{"signin": true, "admin": true, "dashboard": true}

// page answers /{page}: a page's .html file, or a file at the root
// (llms.txt, push-sw.js). The home page is home.html.
func (h *handler) page(w http.ResponseWriter, r *http.Request) error {
	name := r.PathValue("page")
	switch name {
	case "":
		name = "home"
	case "spam-check":
		return h.spamCheck(w, r)
	}
	if name == "llms.txt" || name == "push-sw.js" {
		return serve(w, r, name, "public, max-age=300")
	}
	if appPages[name] {
		return h.appPage(w, r)
	}
	if !pages[name] {
		return cw.NotFound
	}
	return serve(w, r, name+".html", "no-cache")
}

// appPage is the owner app's one page; it reads its screen from the address.
// It is told whether this install offers Google sign-in.
func (h *handler) appPage(w http.ResponseWriter, r *http.Request) error {
	body, err := fs.ReadFile(dist.Files, "index.html")
	if err != nil {
		return err
	}
	google := ""
	if h.a.Config.GoogleClientID != "" {
		google = "1"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(bytes.Replace(body, []byte("__SH_GOOGLE__"), []byte(google), 1))
	return nil
}

// appFile is a file of the owner app's build. Names with a content hash
// (app/index-<hash>.js) never change, so they are kept for a year.
func (h *handler) appFile(w http.ResponseWriter, r *http.Request) error {
	name := path.Clean(strings.TrimPrefix(r.URL.Path, "/"))
	body, err := fs.ReadFile(dist.Files, name)
	if err != nil {
		return cw.NotFound
	}
	cache := "public, max-age=3600"
	if hashed.MatchString(name) {
		cache = "public, max-age=31536000, immutable"
	}
	if name == "sw.js" {
		cache = "no-cache"
	}
	if strings.HasSuffix(name, ".webmanifest") {
		w.Header().Set("Content-Type", "application/manifest+json")
	}
	w.Header().Set("Cache-Control", cache)
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(body))
	return nil
}

var hashed = regexp.MustCompile(`^app/[^/]+-[A-Za-z0-9_-]{8}\.(js|css)$`)

func (h *handler) asset(w http.ResponseWriter, r *http.Request) error {
	return serve(w, r, "assets/"+r.PathValue("file"), "public, max-age=3600")
}

// picker is the photo and file picker behind a one-time upload link. The
// page reads the token from its own address and sends to PUT /v1/uploads/{token}.
func (h *handler) picker(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Referrer-Policy", "no-referrer")
	return serve(w, r, "upload.html", "no-store")
}

// serve writes one embedded file. Anything missing is "not found".
func serve(w http.ResponseWriter, r *http.Request, name, cache string) error {
	name = path.Clean(name)
	body, err := fs.ReadFile(static, name)
	if err != nil {
		return cw.NotFound
	}
	w.Header().Set("Cache-Control", cache)
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(body))
	return nil
}

// helper is the page helper with this service's own address filled in, so
// sign-in and the spam check reach the platform from any site address.
func (h *handler) helper(w http.ResponseWriter, r *http.Request) error {
	body, err := fs.ReadFile(static, "auth.js")
	if err != nil {
		return err
	}
	scheme := "https://"
	if !cw.HTTPS(r) {
		scheme = "http://"
	}
	body = bytes.ReplaceAll(body, []byte("__SH_APP__"), []byte(scheme+h.a.Config.AppDomain))
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Write(body)
	return nil
}

func (h *handler) skillSet(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="simple-host-skill.zip"`)
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Write(h.skills)
	return nil
}

// zipSkills packs the embedded skill folder into one zip, once at start.
func zipSkills() []byte {
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	fs.WalkDir(files, "skill", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, _ := fs.ReadFile(files, name)
		f, err := z.Create(strings.TrimPrefix(name, "skill/"))
		if err == nil {
			_, err = f.Write(body)
		}
		return err
	})
	z.Close()
	return buf.Bytes()
}

// spamCheck is the Turnstile frame a page shows before an anonymous post.
// The widget runs here, on the service's own address, with the platform's
// one site key (a key lists only a few host names); the frame posts its
// token back to the page, which sends it as X-SH-Turnstile.
func (h *handler) spamCheck(w http.ResponseWriter, r *http.Request) error {
	key := h.a.Config.TurnstileSiteKey
	if key == "" {
		return cw.NotFound
	}
	body, err := fs.ReadFile(static, "spam-check.html")
	if err != nil {
		return err
	}
	k, _ := json.Marshal(key)
	body = bytes.Replace(body, []byte(`"__SITE_KEY__"`), k, 1)
	w.Header().Del("X-Frame-Options")
	w.Header().Set("Content-Security-Policy", "frame-ancestors *")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Write(body)
	return nil
}
