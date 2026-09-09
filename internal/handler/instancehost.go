package handler

import (
	"bytes"
	"net/http"
	"path"
	"strings"
	"time"
)

// canonicalSiteDomain is the hostname baked into every served text asset: the
// skills, llms.txt, install.html, auth.js and the OpenAPI spec all name it
// literally. That is correct for simple-host.app and wrong everywhere else.
//
// An instance running on another domain has to describe itself, or an agent
// following its documentation publishes to simple-host.app instead. That is not
// a cosmetic bug: a hackathon's entries would land on somebody else's server.
const canonicalSiteDomain = "simple-host.app"

// rewrittenAssets are served through a handler that substitutes the instance's
// own hostnames, and hidden from the file server so there is exactly one path
// to each. Binary and vendored assets are excluded: nothing in swagger-ui or an
// image mentions the host, and rewriting bytes we do not own is a bad habit.
var rewrittenAssets = []string{
	"llms.txt",
	"install.html",
	"auth.js",
	"openapi.yaml",
	"openapi.json",
}

// hostRewriter substitutes this instance's hostnames for the canonical ones.
//
// Nil means "no rewriting needed", which is the case on simple-host.app itself,
// where every replacement would be identity. Callers must handle nil rather
// than paying for a no-op pass over every asset in production.
type hostRewriter struct{ r *strings.Replacer }

// newHostRewriter returns nil when this instance IS the canonical one.
//
// Order matters and is the whole subtlety here: "sites.simple-host.app"
// contains "simple-host.app", so the longer hostnames must be listed first or
// the content host would be rewritten into "sites.<newdomain>" with the wrong
// prefix. strings.Replacer prefers the earliest-listed match at a position.
func newHostRewriter(siteDomain, contentHost, cnameTarget string) *hostRewriter {
	if siteDomain == "" || siteDomain == canonicalSiteDomain {
		return nil
	}
	if contentHost == "" {
		contentHost = "sites." + siteDomain
	}
	if cnameTarget == "" {
		cnameTarget = "cname." + siteDomain
	}
	return &hostRewriter{r: strings.NewReplacer(
		"sites."+canonicalSiteDomain, contentHost,
		"cname."+canonicalSiteDomain, cnameTarget,
		canonicalSiteDomain, siteDomain,
	)}
}

// apply rewrites a copy of b. Nil receiver returns b untouched so callers can
// stay branch-free.
func (h *hostRewriter) apply(b []byte) []byte {
	if h == nil {
		return b
	}
	return []byte(h.r.Replace(string(b)))
}

var assetContentTypes = map[string]string{
	".txt":  "text/plain; charset=utf-8",
	".html": "text/html; charset=utf-8",
	".js":   "application/javascript; charset=utf-8",
	".json": "application/json; charset=utf-8",
	".yaml": "application/yaml; charset=utf-8",
}

// serveRewrittenAsset serves one embedded asset with the instance's own
// hostnames substituted in.
func serveRewrittenAsset(name string, rw *hostRewriter, modTime time.Time) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := staticFiles.ReadFile("static/" + name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		body = rw.apply(body)
		if ct := assetContentTypes[strings.ToLower(path.Ext(name))]; ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		http.ServeContent(w, r, name, modTime, bytes.NewReader(body))
	})
}

// instanceHosts rewrites the canonical hostnames baked into served assets to
// this instance's own. Nil on simple-host.app, where every substitution is
// identity.
//
// It is a package var because the skills zip builders are package-level
// singletons with sync.Once caches: whichever request builds a zip first
// freezes its contents for the process lifetime. So this must be set before the
// server accepts a request, and SetInstanceHosts is called from main directly
// after config load rather than as a side effect of registering routes.
var instanceHosts *hostRewriter

// SetInstanceHosts configures host rewriting for served assets. Call once, at
// startup, before serving.
func SetInstanceHosts(siteDomain, contentHost, cnameTarget string) {
	instanceHosts = newHostRewriter(siteDomain, contentHost, cnameTarget)
}
