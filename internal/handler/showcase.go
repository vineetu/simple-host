package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"html"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	xhtml "golang.org/x/net/html"

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
	Name        string    `json:"name"`
	URL         string    `json:"url"`
	CreatedAt   time.Time `json:"created_at"`
	Visibility  string    `json:"visibility"`
	Title       string    `json:"title,omitempty"`
	Description string    `json:"description,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
	Pinned      bool      `json:"pinned"`
	Order       int       `json:"order"`
}

type showcaseData struct {
	Handle            string         `json:"handle"`
	Bio               string         `json:"bio"`
	SitesBaseURL      string         `json:"sitesBaseUrl"`
	PublicShowcaseURL string         `json:"publicShowcaseUrl"`
	OwnerAppURL       string         `json:"ownerAppUrl"`
	MainURL           string         `json:"mainUrl"`
	Sites             []showcaseSite `json:"sites"`
	// Voice: this server has voice input (/v1/transcribe). The page shows the
	// mic from this alone, so loading it sends no request to find out.
	Voice bool `json:"voice"`
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
	if h.redirectHandleAlias(w, r, handle) {
		return
	}
	h.renderShowcase(w, r, handle)
}

// redirectHandleAlias: handle is an old handle kept as an alias (the account
// changed its address), so /<old> 302s to /<current> on the same host — the
// owner app on the base origin, the showcase on the content host.
func (h *SiteHandler) redirectHandleAlias(w http.ResponseWriter, r *http.Request, handle string) bool {
	if !showcaseHandleRe.MatchString(handle) || reservedShowcaseHandles[handle] {
		return false
	}
	if _, err := db.GetUserByHandle(r.Context(), h.database, handle); err == nil {
		return false
	}
	current, err := db.ResolveHandleAlias(r.Context(), h.database, handle)
	if err != nil || current == "" || current == handle {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, "/"+current, http.StatusFound)
	return true
}

// ownerAppOrStatic serves the per-user OWNER APP on the base origin
// (<domain>/<handle>) when the single path segment resolves to a real handle,
// otherwise falls through to the static file server (landing page, docs, install
// page, assets, SPA). The page it renders hydrates into the full owner dashboard
// client-side, because on the base origin the API key is already in localStorage
// from login — no paste, no redirect. The cheap dot/charset filter means the DB
// lookup only fires for handle-shaped paths, never for a static asset request.
func (h *SiteHandler) ownerAppOrStatic(fileServer http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seg := strings.Trim(r.URL.Path, "/")
		// The hackathon platform has no person pages (hack_mode.go).
		if seg != "" && !hackMode && !strings.ContainsAny(seg, "/.") &&
			showcaseHandleRe.MatchString(strings.ToLower(seg)) && !reservedShowcaseHandles[strings.ToLower(seg)] {
			if _, err := db.GetUserByHandle(r.Context(), h.database, strings.ToLower(seg)); err == nil {
				h.renderShowcase(w, r, strings.ToLower(seg))
				return
			}
			if h.redirectHandleAlias(w, r, strings.ToLower(seg)) {
				return
			}
		}
		fileServer.ServeHTTP(w, r)
	})
}

// contentBaseURL is the absolute base for user site + showcase links — always the
// content host (sites.<domain>), regardless of which origin served the page, so
// the rendered links are correct whether it came from the base app or the
// content host.
func (h *SiteHandler) contentBaseURL() string {
	host := h.contentHost
	if host == "" {
		host = "sites." + h.siteDomain
	}
	return "https://" + host
}

// renderShowcase renders the per-user showcase for `handle`. Same output on both
// origins (all links absolute): served on the content host it's the public,
// read-only view; served on the base app it hydrates into the owner dashboard.
func (h *SiteHandler) renderShowcase(w http.ResponseWriter, r *http.Request, handle string) {
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
		h.renderServiceError(w, r)
		return
	}

	data, err := h.publicShowcaseData(r.Context(), user)
	if err != nil {
		h.renderServiceError(w, r)
		return
	}

	page, err := showcasePage(chromeDataFor(r, h.chromeBase(r)), data)
	if err != nil {
		h.renderServiceError(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "index")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(stampNonce(r, page))
}

// publicShowcaseData is the single public projection used by HTML and the JSON feed.
func (h *SiteHandler) publicShowcaseData(ctx context.Context, user db.User) (showcaseData, error) {
	sites, err := db.ListSitesByUser(ctx, h.database, user.ID)
	if err != nil {
		return showcaseData{}, err
	}
	bio, err := db.GetShowcaseBio(ctx, h.database, user.ID)
	if err != nil {
		return showcaseData{}, err
	}
	prefs, err := db.ShowcasePreferences(ctx, h.database, user.ID)
	if err != nil {
		return showcaseData{}, err
	}
	handle := user.Handle.String
	data := showcaseData{Bio: bio, Handle: handle, SitesBaseURL: h.contentBaseURL(), PublicShowcaseURL: h.PersonPageURL(handle), OwnerAppURL: h.mainSiteURL() + "/" + handle, MainURL: h.mainSiteURL(), Sites: []showcaseSite{}, Voice: voiceInputEnabled}
	for _, s := range sites {
		if s.Visibility != "public" || s.Suspended() || s.Offline || s.Passcode {
			continue
		}
		title, desc := h.showcaseMetadata(s)
		data.Sites = append(data.Sites, showcaseSite{Name: s.Name, URL: h.SiteURL(handle, s.Name), CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt, Visibility: "public", Title: title, Description: desc, Pinned: prefs[s.ID].Pinned, Order: prefs[s.ID].Order})
	}
	sort.SliceStable(data.Sites, func(i, j int) bool {
		if data.Sites[i].Pinned != data.Sites[j].Pinned {
			return data.Sites[i].Pinned
		}
		return data.Sites[i].Order < data.Sites[j].Order
	})
	return data, nil
}

// Metadata comes only from a visible site's deployed index, never a remote fetch.
func (h *SiteHandler) showcaseMetadata(s db.Site) (title, description string) {
	root, err := os.OpenRoot(h.disk.SiteDir(s.UserID, s.Name) + "/current")
	if err != nil {
		return "", ""
	}
	defer root.Close()
	f, err := root.Open("index.html")
	if err != nil {
		return "", ""
	}
	defer f.Close()
	z := xhtml.NewTokenizer(io.LimitReader(f, 256<<10))
	inTitle := false
	for {
		switch z.Next() {
		case xhtml.ErrorToken:
			return strings.TrimSpace(title), strings.TrimSpace(description)
		case xhtml.StartTagToken, xhtml.SelfClosingTagToken:
			t := z.Token()
			if t.Data == "title" {
				inTitle = true
			}
			if t.Data == "meta" {
				var name, content string
				for _, a := range t.Attr {
					if a.Key == "name" {
						name = strings.ToLower(a.Val)
					}
					if a.Key == "content" {
						content = a.Val
					}
				}
				if name == "description" && description == "" {
					description = content
				}
			}
		case xhtml.EndTagToken:
			if z.Token().Data == "title" {
				inTitle = false
			}
		case xhtml.TextToken:
			if inTitle {
				title += string(z.Text())
			}
		}
	}
}

func (h *SiteHandler) showcaseFeed(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Del("Access-Control-Allow-Credentials")
	w.Header().Set("Cache-Control", "no-store")
	handle := strings.ToLower(strings.TrimSpace(r.PathValue("handle")))
	if hackMode || !showcaseHandleRe.MatchString(handle) || reservedShowcaseHandles[handle] {
		writeJSON(w, 404, errorResponse{Error: "not found", Code: "not_found"})
		return
	}
	user, err := db.GetUserByHandle(r.Context(), h.database, handle)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, 404, errorResponse{Error: "not found", Code: "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, 500, errorResponse{Error: "internal server error"})
		return
	}
	// Same limiter/settings as other public reads; every spelling/host uses the user ID.
	if h.readLimiter != nil && !h.readLimiter.allow("showcase:"+user.ID+":"+clientIP(r)) {
		tooManyRequests(w)
		return
	}
	data, err := h.publicShowcaseData(r.Context(), user)
	if err != nil {
		writeJSON(w, 500, errorResponse{Error: "internal server error"})
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=30")
	writeJSON(w, 200, map[string]any{"handle": data.Handle, "bio": data.Bio, "sites": data.Sites})
}

// showcasePage assembles the showcase template: the shared chrome, then the
// handle and the page data. Kept apart from the database work so the served
// markup can be tested on its own.
func showcasePage(d chromeData, data showcaseData) ([]byte, error) {
	tmpl, err := chromePage("showcase.html", d)
	if err != nil {
		return nil, err
	}
	// json.Marshal HTML-escapes <, >, & by default, so the injected blob cannot
	// break out of the <script> even if a value somehow contained markup.
	blob, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	page := strings.ReplaceAll(string(tmpl), "__SH_HANDLE__", data.Handle)
	page = strings.Replace(page, "/*__SHOWCASE_DATA__*/", string(blob), 1)
	return []byte(page), nil
}

// notFound is the nginx error_page fallback. nginx sends file misses here and
// forwards the original path as X-Original-URI so we can tailor the back-link.
func (h *SiteHandler) notFound(w http.ResponseWriter, r *http.Request) {
	orig := r.Header.Get("X-Original-URI")
	if orig == "" {
		orig = r.URL.Path
	}
	// sites.<domain>/<handle>/<old-name>/...: nginx finds no folder for a
	// renamed site's old name; send the link to the site's current address.
	if target, ok := h.renamedContentPath(r.Context(), orig); ok {
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, target, http.StatusFound)
		return
	}
	h.renderNotFound(w, r, orig)
}

// renamedContentPath: uri (a content-host request URI, escaped, with any
// query) is /<handle>/<name>/... where name is an old name of one of that
// person's renamed sites and no site has it now. Returns the site's current
// address with the rest of the path and the query.
func (h *SiteHandler) renamedContentPath(ctx context.Context, uri string) (string, bool) {
	p, query, hasQuery := strings.Cut(uri, "?")
	parts := strings.SplitN(strings.TrimPrefix(p, "/"), "/", 3)
	if len(parts) < 2 {
		return "", false
	}
	handle, name := strings.ToLower(parts[0]), parts[1]
	if !showcaseHandleRe.MatchString(handle) || !validSiteName.MatchString(name) {
		return "", false
	}
	user, err := db.GetUserByHandleOrAlias(ctx, h.database, handle)
	if err != nil {
		return "", false
	}
	if _, err := db.GetSiteByUser(ctx, h.database, user.ID, name); !errors.Is(err, sql.ErrNoRows) {
		return "", false // a real site (a missing file), or a lookup error
	}
	rest := ""
	if len(parts) == 3 {
		rest = parts[2]
	}
	target, ok := h.renamedSiteAddress(ctx, user.ID, name, "/"+rest)
	if !ok {
		return "", false
	}
	if hasQuery {
		target += "?" + query
	}
	return target, true
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
				backURL = h.PersonPageURL(handle)
				if backURL == "" {
					backURL = base + "/" + handle
				}
				backLabel = "Back to @" + handle + "’s sites"
			}
		}
	}

	h.renderNotFoundPage(w, r, message, subtext, backURL, backLabel)
}

// renderNotFoundPage writes the branded 404 with the given words and way back.
func (h *SiteHandler) renderNotFoundPage(w http.ResponseWriter, r *http.Request, message, subtext, backURL, backLabel string) {
	h.renderMessagePage(w, r, http.StatusNotFound, message, subtext, backURL, backLabel)
}

// renderMessagePage is the branded one-message page (the not-found page's
// layout) with any status. message and subtext are inserted as given: escape
// anything that did not come from this code.
func (h *SiteHandler) renderMessagePage(w http.ResponseWriter, r *http.Request, status int, message, subtext, backURL, backLabel string) {
	writeMessagePage(w, r, h.chromeBase(r), status, message, subtext, backURL, backLabel, "")
}

// confirmForm is a one-button POST form for a confirmation page: action is
// the path it posts to, fields its hidden inputs (escaped here).
func confirmForm(action string, fields map[string]string, button string) string {
	var b strings.Builder
	b.WriteString(`<form method="post" action="` + html.EscapeString(action) + `">`)
	for k, v := range fields {
		b.WriteString(`<input type="hidden" name="` + html.EscapeString(k) + `" value="` + html.EscapeString(v) + `">`)
	}
	b.WriteString(`<button type="submit" class="btn-primary" style="border:0;cursor:pointer;font:inherit">` + html.EscapeString(button) + `</button></form>`)
	return b.String()
}

// Emailed one-time links carry their token after "#" (#t=...), so it never
// reaches a server or proxy log: a browser does not send the fragment. The
// link's GET finds no token and answers with fragmentTokenPage, whose script
// reads it and POSTs it back with peek=1; the handler then shows its usual
// confirmation page (peek never acts). Links sent before this change carry
// ?t= and keep working on GET until they expire.

// linkToken is an emailed link's token: from the posted form on POST, from
// the query on GET (older links).
func linkToken(w http.ResponseWriter, r *http.Request) string {
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
		return strings.TrimSpace(r.PostFormValue("t"))
	}
	return strings.TrimSpace(r.URL.Query().Get("t"))
}

// linkConfirming reports whether r only asks for the confirmation page: a GET
// (an older ?t= link) or the fragment page's POST with peek=1.
func linkConfirming(r *http.Request) bool {
	return r.Method != http.MethodPost || r.PostFormValue("peek") == "1"
}

// fragmentLinkGET answers the GET of an emailed link that carries its token
// in the fragment (no ?t=): the page that reads it and posts it back. It
// reports whether it wrote the response.
func fragmentLinkGET(w http.ResponseWriter, r *http.Request, base, action string) bool {
	if r.Method == http.MethodPost || r.URL.Query().Get("t") != "" {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	nonce, _ := r.Context().Value(cspNonceKey{}).(string)
	attr := ""
	if nonce != "" {
		attr = ` nonce="` + html.EscapeString(nonce) + `"`
	}
	form := `<form id="sh-link" method="post" action="` + html.EscapeString(action) + `">` +
		`<input type="hidden" name="t" value=""><input type="hidden" name="peek" value="1">` +
		`<button type="submit" class="btn-primary" style="border:0;cursor:pointer;font:inherit">Continue</button></form>` +
		`<script` + attr + `>(function(){var m=/[#&]t=([^&]+)/.exec(location.hash||"");var f=document.getElementById("sh-link");` +
		`if(!m){f.hidden=true;return;}f.elements.t.value=decodeURIComponent(m[1]);` +
		`try{history.replaceState(null,"",location.pathname);}catch(e){}f.submit();})();</script>`
	writeMessagePage(w, r, base, http.StatusOK, "Opening your link…",
		"If nothing happens, press Continue. If there is no button, open the link from the email again.", "", "", form)
	return true
}

// writeMessagePage writes the one-message page. A non-empty form (from
// confirmForm) takes the place of the way-back link: emailed links open such
// a page on GET and act only when the person presses its button, so a mail
// scanner that follows every link changes nothing.
func writeMessagePage(w http.ResponseWriter, r *http.Request, base string, status int, message, subtext, backURL, backLabel, form string) {
	tmpl, err := chromePage("notfound.html", chromeDataFor(r, base))
	if err != nil {
		// Last-resort inline page so a miss never falls through to nginx's default.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		tail := `<a href="` + html.EscapeString(backURL) + `">` + html.EscapeString(backLabel) + `</a>`
		if form != "" {
			tail = form
		}
		_, _ = w.Write([]byte(`<!doctype html><meta charset=utf-8><meta name=robots content=noindex><title>simple·host</title><h1>` + message + `</h1><p>` + subtext + `</p>` + tail))
		return
	}

	page := string(stampNonce(r, tmpl))
	if form != "" {
		page = strings.Replace(page, `<a class="btn-primary" href="__SH_BACKLINK_URL__">__SH_BACKLINK_LABEL__</a>`, form, 1)
	}
	page = strings.ReplaceAll(page, "__SH_MESSAGE__", message)
	page = strings.ReplaceAll(page, "__SH_SUBTEXT__", subtext)
	page = strings.ReplaceAll(page, "__SH_BACKLINK_URL__", backURL)
	page = strings.ReplaceAll(page, "__SH_BACKLINK_LABEL__", backLabel)
	if status != http.StatusNotFound {
		// Same layout, without the "404".
		page = strings.Replace(page, `<div class="nf-code">404</div>`, "", 1)
		page = strings.Replace(page, "<title>404 — Not found · simple·host</title>", "<title>simple·host</title>", 1)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(page))
}

var serviceErrorPage = themed(`<!doctype html><meta charset=utf-8><meta name=robots content=noindex><!--sh:theme--><title>Temporarily unavailable</title><h1>Temporarily unavailable</h1><p>Please try again in a moment.</p>`)

func (h *SiteHandler) renderServiceError(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write(stampNonce(r, hostedStatusPage(serviceErrorPage)))
}
