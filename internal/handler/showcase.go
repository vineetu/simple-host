package handler

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	db "github.com/vsriram/simple-host/internal/db"
)

// showcaseHandleRe mirrors the handle charset used across the app
// (^[a-z0-9-]{1,39}$). nginx already constrains the single-segment location to
// the same charset; we re-validate here as defense in depth and to avoid
// querying the DB with junk.
var showcaseHandleRe = regexp.MustCompile(`^[a-z0-9-]{1,39}$`)

// reservedShowcaseHandles are single-segment paths that must never be treated as
// a user handle even if the charset matches. Handle claiming should already deny
// these; this is a belt-and-suspenders 404 for the showcase/notfound path.
var reservedShowcaseHandles = map[string]bool{
	"sites": true, "www": true, "api": true, "v1": true, "internal": true,
	"cname": true, "admin": true, "static": true, "skills": true, "plugin": true,
	"install": true, "healthz": true, "readyz": true, "assets": true, "favicon.ico": true,
}

type showcaseSite struct {
	Name       string    `json:"name"`
	URL        string    `json:"url"`
	CreatedAt  time.Time `json:"created_at"`
	Visibility string    `json:"visibility"`
}

type showcaseData struct {
	Handle       string         `json:"handle"`
	SitesBaseURL string         `json:"sitesBaseUrl"`
	MainURL      string         `json:"mainUrl"`
	Sites        []showcaseSite `json:"sites"`
}

// publicSitesBase reconstructs the scheme://host the browser reached the content
// host on (e.g. https://sites.simple-host.app), used to build site + showcase
// links. Behind nginx, Host is preserved and X-Forwarded-Proto carries scheme.
func (h *SiteHandler) publicSitesBase(r *http.Request) string {
	scheme := r.Header.Get("X-Forwarded-Proto")
	if scheme == "" {
		scheme = "https"
	}
	host := r.Host
	if host == "" {
		host = h.contentHost
	}
	return scheme + "://" + host
}

func (h *SiteHandler) mainSiteURL() string {
	d := h.siteDomain
	if d == "" {
		d = "simple-host.app"
	}
	return "https://" + d
}

// showcase renders a user's public profile at sites.<domain>/<handle>. nginx
// proxies the single-segment path here as GET /internal/showcase/{handle}. The
// server always renders the PUBLIC view (safe default); the page hydrates into
// the owner view client-side when a matching same-origin API key is present.
func (h *SiteHandler) showcase(w http.ResponseWriter, r *http.Request) {
	handle := strings.ToLower(strings.TrimSpace(r.PathValue("handle")))
	if handle == "" || !showcaseHandleRe.MatchString(handle) || reservedShowcaseHandles[handle] {
		h.renderNotFound(w, r, "/"+handle)
		return
	}

	user, err := db.GetUserByHandle(r.Context(), h.database, handle)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.renderNotFound(w, r, "/"+handle)
			return
		}
		h.renderServiceError(w)
		return
	}

	sites, err := db.ListSitesByUser(r.Context(), h.database, user.ID)
	if err != nil {
		h.renderServiceError(w)
		return
	}

	base := h.publicSitesBase(r)
	data := showcaseData{
		Handle:       handle,
		SitesBaseURL: base,
		MainURL:      h.mainSiteURL(),
		Sites:        []showcaseSite{},
	}
	for _, s := range sites {
		vis := s.Visibility
		if vis == "" {
			vis = "public"
		}
		if vis != "public" {
			continue // public server-render lists public sites only
		}
		data.Sites = append(data.Sites, showcaseSite{
			Name:       s.Name,
			URL:        base + "/" + handle + "/" + s.Name + "/",
			CreatedAt:  s.CreatedAt,
			Visibility: vis,
		})
	}

	tmpl, err := staticFiles.ReadFile("static/showcase.html")
	if err != nil {
		h.renderServiceError(w)
		return
	}
	// json.Marshal HTML-escapes <, >, & by default, so the injected blob cannot
	// break out of the <script> even if a value somehow contained markup.
	blob, err := json.Marshal(data)
	if err != nil {
		h.renderServiceError(w)
		return
	}

	page := strings.ReplaceAll(string(tmpl), "__SH_HANDLE__", handle)
	page = strings.Replace(page, "/*__SHOWCASE_DATA__*/", string(blob), 1)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "index")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(page))
}

// notFound is the nginx error_page fallback. nginx sends file misses here and
// forwards the original path as X-Original-URI so we can tailor the back-link.
func (h *SiteHandler) notFound(w http.ResponseWriter, r *http.Request) {
	orig := r.Header.Get("X-Original-URI")
	if orig == "" {
		orig = r.URL.Path
	}
	h.renderNotFound(w, r, orig)
}

// renderNotFound serves the branded 404. If the first path segment is a real
// handle, the back-link points at that user's showcase (owner's stated
// priority); otherwise it points at the main page. Only regex-validated handles
// that resolve to a real user are ever echoed back into the page.
func (h *SiteHandler) renderNotFound(w http.ResponseWriter, r *http.Request, origPath string) {
	if i := strings.IndexByte(origPath, '?'); i >= 0 {
		origPath = origPath[:i]
	}
	var segs []string
	for _, s := range strings.Split(strings.Trim(origPath, "/"), "/") {
		if s != "" {
			segs = append(segs, s)
		}
	}

	base := h.publicSitesBase(r)
	message := "Page not found"
	subtext := "The page you’re looking for doesn’t exist."
	backURL := h.mainSiteURL()
	backLabel := "Go to simple-host.app"

	if len(segs) >= 1 {
		handle := strings.ToLower(segs[0])
		if showcaseHandleRe.MatchString(handle) && !reservedShowcaseHandles[handle] {
			if _, err := db.GetUserByHandle(r.Context(), h.database, handle); err == nil {
				message = "That page isn’t here"
				subtext = "This site or page doesn’t exist under @" + handle + "."
				backURL = base + "/" + handle
				backLabel = "Back to @" + handle + "’s sites"
			}
		}
	}

	tmpl, err := staticFiles.ReadFile("static/notfound.html")
	if err != nil {
		// Last-resort inline 404 so a miss never falls through to nginx's default.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`<!doctype html><meta charset=utf-8><meta name=robots content=noindex><title>404</title><h1>404</h1><p>` + message + `</p><a href="` + backURL + `">` + backLabel + `</a>`))
		return
	}

	page := string(tmpl)
	page = strings.ReplaceAll(page, "__SH_MESSAGE__", message)
	page = strings.ReplaceAll(page, "__SH_SUBTEXT__", subtext)
	page = strings.ReplaceAll(page, "__SH_BACKLINK_URL__", backURL)
	page = strings.ReplaceAll(page, "__SH_BACKLINK_LABEL__", backLabel)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(page))
}

func (h *SiteHandler) renderServiceError(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(`<!doctype html><meta charset=utf-8><meta name=robots content=noindex><title>Temporarily unavailable</title><h1>Temporarily unavailable</h1><p>Please try again in a moment.</p>`))
}
