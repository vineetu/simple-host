package handler

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"html/template"
	"io/fs"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

// The site chrome — one header, one footer, one stylesheet — is defined once,
// in static/partials/ and static/site.css, and injected into every page at
// serve time. A page opts in by carrying three marker comments:
//
//	<!--sh:head-->    in <head>, before the page's own <style>: site.css + the pre-paint theme
//	<!--sh:header-->  first thing in <body>
//	<!--sh:footer-->  after the page's content, before its scripts
//
// Every path that serves an HTML page goes through withChrome: the file server
// (chromeFileServer, including "/" → index.html), serveStaticPage, the
// host-rewritten install.html on other instances (serveRewrittenAsset), and the
// showcase and 404 templates (renderShowcase, renderNotFound). A page without
// markers — setup.html, the first-run wizard — passes through untouched.
const (
	markerHead   = "<!--sh:head-->"
	markerHeader = "<!--sh:header-->"
	markerFooter = "<!--sh:footer-->"
)

// markerAsk is where a page wants an "Ask" assistant, named in the marker:
// <!--sh:ask simple-host--> or <!--sh:ask enterprise-->. Every page naming the
// same assistant gets the same widget (partials/ask.html + ask.js); it renders
// only when the assistants are on (see ask.go) and the name is known, and
// otherwise the marker becomes nothing.
var markerAsk = regexp.MustCompile(`<!--sh:ask ([a-z-]+)-->`)

var (
	chromeTemplates = template.Must(template.ParseFS(staticFiles, "static/partials/*.html"))

	// siteCSSVersion busts caches on site.css whenever its bytes change.
	siteCSSVersion = func() string {
		b, err := staticFiles.ReadFile("static/site.css")
		if err != nil {
			panic("site.css missing from the embedded static files: " + err.Error())
		}
		sum := sha256.Sum256(b)
		return hex.EncodeToString(sum[:])[:10]
	}()

	// askJSVersion busts caches on ask.js the same way.
	askJSVersion = func() string {
		b, err := staticFiles.ReadFile("static/ask.js")
		if err != nil {
			panic("ask.js missing from the embedded static files: " + err.Error())
		}
		sum := sha256.Sum256(b)
		return hex.EncodeToString(sum[:])[:10]
	}()
)

// chromeData is everything the partials vary on. Every field is drawn from a
// small fixed set, so it is also a safe cache key.
type chromeData struct {
	// Base prefixes every chrome link. Empty on the main origin; the main
	// site's absolute URL when the page is served from the content host or a
	// custom domain, where "/install.html" would resolve to the wrong server.
	Base string
	// Current names the page being served, for aria-current. Empty when the
	// page is not in the nav (a showcase, a 404).
	Current string
	// HackHome is the hackathons page served as simple-hack.app's homepage.
	// There "Home" would be a link back to this same page and "For hackathons"
	// a second one, so Home points at the main product and the duplicate goes.
	HackHome   bool
	CSSVersion string
	// AskOn: the "Ask" assistants are on. AskPage is the page key of the
	// path served, "" when it is not an assistant page.
	AskOn        bool
	AskPage      string
	AskJSVersion string
}

// askWidgetData is what partials/ask.html renders: one assistant, and the page
// of it the reader is on.
type askWidgetData struct {
	Base, AskJSVersion       string
	Key, Name, Tone, Example string
	Page                     string
}

// navKeys maps a request path to the chrome link that names it.
var navKeys = map[string]string{
	"/":                        "home",
	"/install.html":            "install",
	"/dashboard":               "dashboard",
	"/enterprise":              "enterprise",
	"/enterprise.html":         "enterprise",
	"/enterprise/brief":        "enterprise",
	"/enterprise/architecture": "enterprise",
	"/hackathons":              "hackathons",
	"/hackathons.html":         "hackathons",
	"/features":                "features",
	"/features.html":           "features",
	"/docs.html":               "docs",
	"/architecture.html":       "architecture",
	"/privacy.html":            "privacy",
	"/terms":                   "terms",
	"/support":                 "support",
}

// chromeDataFor derives the chrome for one request. base is "" for pages on
// the main origin.
func chromeDataFor(r *http.Request, base string) chromeData {
	current := navKeys[r.URL.Path]
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(host)
	return chromeData{
		Base:         base,
		Current:      current,
		HackHome:     current == "hackathons" && strings.HasPrefix(host, "simple-hack."),
		CSSVersion:   siteCSSVersion,
		AskOn:        askEnabled,
		AskPage:      askPageFor(r.URL.Path),
		AskJSVersion: askJSVersion,
	}
}

// withChrome replaces each marker in page with its rendered partial. Each
// marker is replaced once; a page's source is checked by tests to carry each
// exactly once.
func withChrome(page []byte, d chromeData) ([]byte, error) {
	if !bytes.Contains(page, []byte("<!--sh:")) {
		return page, nil
	}
	for _, m := range []struct{ marker, partial string }{
		{markerHead, "head.html"},
		{markerHeader, "header.html"},
		{markerFooter, "footer.html"},
	} {
		if !bytes.Contains(page, []byte(m.marker)) {
			continue
		}
		var buf bytes.Buffer
		if err := chromeTemplates.ExecuteTemplate(&buf, m.partial, d); err != nil {
			return nil, err
		}
		page = bytes.Replace(page, []byte(m.marker), bytes.TrimRight(buf.Bytes(), "\n"), 1)
	}
	if loc := markerAsk.FindSubmatchIndex(page); loc != nil {
		var out []byte
		if a := askAssistantByKey(string(page[loc[2]:loc[3]])); a != nil && d.AskOn {
			w := askWidgetData{Base: d.Base, AskJSVersion: d.AskJSVersion, Key: a.key, Name: a.name, Tone: a.tone, Example: a.example}
			if a.page(d.AskPage) != nil {
				w.Page = d.AskPage
			}
			var buf bytes.Buffer
			if err := chromeTemplates.ExecuteTemplate(&buf, "ask.html", w); err != nil {
				return nil, err
			}
			out = bytes.TrimRight(buf.Bytes(), "\n")
		}
		page = append(page[:loc[0]:loc[0]], append(out, page[loc[1]:]...)...)
	}
	return page, nil
}

type chromeCacheKey struct {
	name string
	d    chromeData
}

// chromeCache holds assembled static pages. Bounded: a handful of pages times
// a handful of chromeData values.
var chromeCache sync.Map

// chromePage returns the embedded static/<name> with its chrome filled in and
// this install's limits in its copy (limitstext.go). The enterprise-* pages
// describe the other product, whose limits are its own, so they keep theirs.
func chromePage(name string, d chromeData) ([]byte, error) {
	page, err := chromePageRaw(name, d)
	if err != nil || strings.HasPrefix(name, "enterprise-") || name == "enterprise.html" {
		return page, err
	}
	return instanceLimits.apply(page), nil
}

func chromePageRaw(name string, d chromeData) ([]byte, error) {
	key := chromeCacheKey{name, d}
	if v, ok := chromeCache.Load(key); ok {
		return v.([]byte), nil
	}
	raw, err := staticFiles.ReadFile("static/" + name)
	if err != nil {
		return nil, err
	}
	page, err := withChrome(raw, d)
	if err != nil {
		return nil, err
	}
	chromeCache.Store(key, page)
	return page, nil
}

// chromeFileServer puts the chrome on HTML pages the file server would
// otherwise hand out raw, and passes everything else — assets, 404s, the
// /index.html → / redirect — to next. fsys is the file server's own view, so
// a page hidden from it stays hidden here.
func chromeFileServer(fsys fs.FS, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		switch {
		case name == "":
			name = "index.html"
		case name == "index.html" || !strings.HasSuffix(name, ".html") || !fs.ValidPath(name):
			next.ServeHTTP(w, r)
			return
		}
		f, err := fsys.Open(name)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}
		f.Close()
		body, err := chromePage(name, chromeDataFor(r, ""))
		if err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// A zero modtime, as the embedded FS reports, so no Last-Modified —
		// exactly what the file server sent for these pages before.
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(stampNonce(r, body)))
	})
}

// chromeBase is the link base for the showcase and 404 pages, which are also
// served off the content host (nginx proxies those as /internal/…) and custom
// domains, where a relative link would land on the wrong server.
func (h *SiteHandler) chromeBase(r *http.Request) string {
	// Pages rendered for the content host (/internal/...) or on a person host
	// link back to the main site absolutely.
	if strings.HasPrefix(r.URL.Path, "/internal/") || h.isPlatformSubdomainHost(requestHostName(r)) || h.isSiteHostName(requestHostName(r)) {
		return h.mainSiteURL()
	}
	return ""
}
