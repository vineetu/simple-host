package handler

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/db"
)

// Look before it goes live.
//
// A deploy with ?publish=false stores a new version without making it live:
// `current`, active_version and what visitors see do not change. Any kept
// version (stored that way, live now or live before) can be opened by its
// owner through a preview link on the site's own host:
//
//	https://<site>.<handle>.<SITE_DOMAIN>/__preview/<n>/<token>/...
//
// (or <handle>.<SITE_DOMAIN>/<site>/__preview/... while the person's
// certificate is not ready). The token is an HMAC over (site, version,
// expiry) with the per-process key export links use, under its own domain
// string; it opens that one version of that one site for previewLinkTTL, and
// stops working when the server restarts. Preview pages are noindex and never
// cached, and saves made from them are refused, so trying a new version never
// writes into the live site's data. Making the version live is the existing
// rollback (PUT .../active-version).

// previewLinkDomain separates these MACs from export links and anything else
// the key could be asked to sign.
const previewLinkDomain = "simple-host version preview v1"

// previewPathSegment is the first path segment of a preview on a site host
// (the second, after the site name, on a person host).
const previewPathSegment = "__preview"

var errPreviewLink = errors.New("this preview link is not valid or has expired")

func (h *SiteHandler) previewMAC(payload string) []byte {
	mac := hmac.New(sha256.New, h.exportKey)
	mac.Write([]byte(previewLinkDomain))
	mac.Write([]byte{0})
	mac.Write([]byte(payload))
	return mac.Sum(nil)
}

// signPreviewToken binds a token to one site, one version and an expiry.
func (h *SiteHandler) signPreviewToken(siteID string, version int, expires time.Time) string {
	payload := siteID + "." + strconv.Itoa(version) + "." + strconv.FormatInt(expires.Unix(), 10)
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." +
		base64.RawURLEncoding.EncodeToString(h.previewMAC(payload))
}

// checkPreviewToken returns the site and version a token was minted for, if
// its signature holds and it has not expired.
func (h *SiteHandler) checkPreviewToken(token string, now time.Time) (siteID string, version int, err error) {
	encPayload, encMAC, ok := strings.Cut(token, ".")
	if !ok {
		return "", 0, errPreviewLink
	}
	rawPayload, err1 := base64.RawURLEncoding.DecodeString(encPayload)
	gotMAC, err2 := base64.RawURLEncoding.DecodeString(encMAC)
	if err1 != nil || err2 != nil {
		return "", 0, errPreviewLink
	}
	payload := string(rawPayload)
	if !hmac.Equal(gotMAC, h.previewMAC(payload)) {
		return "", 0, errPreviewLink
	}
	parts := strings.Split(payload, ".")
	if len(parts) != 3 {
		return "", 0, errPreviewLink
	}
	v, err := strconv.Atoi(parts[1])
	exp, err2 := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || err2 != nil || v < 1 {
		return "", 0, errPreviewLink
	}
	left := time.Unix(exp, 0).Sub(now)
	if left <= 0 || left > previewLinkTTL() {
		return "", 0, errPreviewLink
	}
	return parts[0], v, nil
}

// previewBase is where a site's previews are served: "https://<host>" plus
// the path prefix up to and including "/__preview/". ok=false when the site
// has no address of its own to preview on (site and person hosts off, or a
// site pinned to the content host).
func (h *SiteHandler) previewBase(site db.Site) (string, bool) {
	handle := site.OwnerHandle
	switch {
	case handle == "":
		return "", false
	case h.siteHostLive(handle, site.Name):
		return "https://" + h.siteHostFor(handle, site.Name) + "/" + previewPathSegment + "/", true
	case hackMode:
		// Person-path addresses are never served on the hackathon platform:
		// a team's work lives on its own origin only.
		return "", false
	case h.personAddressFor(handle, site.Name):
		return "https://" + h.personHostFor(handle) + "/" + site.Name + "/" + previewPathSegment + "/", true
	}
	return "", false
}

// SharedOrigin reports whether every site is served on the content host's
// one origin (no per-site or per-person addresses: a small box).
func (h *SiteHandler) SharedOrigin() bool { return !h.siteHostsOn() && !h.personHostsOn() }

// noPreviewWhy says why a site has no preview link. A preview is served on
// the site's own address (its own browser origin); a server that gives sites
// none (SITE_HOSTS and PERSON_HOSTS off, as on a small box) never has one.
func (h *SiteHandler) noPreviewWhy() string {
	if h.SharedOrigin() {
		return "preview links need a per-site address, and this server serves every site on one shared address, so it makes none. " +
			"To see a stored version, make it live (PUT /v1/sites/<site>/active-version) and switch back if it is not right"
	}
	return "this site has no address of its own yet to preview on (its address is still being set up); try again later"
}

// previewLink mints a preview address for version n of site.
func (h *SiteHandler) previewLink(site db.Site, n int) (string, time.Time, bool) {
	base, ok := h.previewBase(site)
	if !ok {
		return "", time.Time{}, false
	}
	expires := time.Now().Add(previewLinkTTL()).Truncate(time.Second)
	return base + strconv.Itoa(n) + "/" + h.signPreviewToken(site.ID, n, expires) + "/", expires, true
}

// publishParam reads ?publish= on a deploy: true (the default) makes the new
// version live, false only stores it. ok=false after writing a 400.
func publishParam(w http.ResponseWriter, r *http.Request) (publish, ok bool) {
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get("publish"))) {
	case "", "true", "1", "yes":
		return true, true
	case "false", "0", "no":
		return false, true
	}
	writeJSON(w, http.StatusBadRequest, errorResponse{Error: "publish must be true or false", Code: "invalid_request"})
	return false, false
}

// publishOnCreate refuses publish=false on a new site: its first version is
// the only one, and nothing is live to keep. False after writing the answer.
func (h *SiteHandler) publishOnCreate(w http.ResponseWriter, r *http.Request) bool {
	publish, ok := publishParam(w, r)
	if !ok {
		return false
	}
	if !publish {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "publish=false is for a new version of an existing site; a new site's first version always goes live", Code: "invalid_request"})
		return false
	}
	return true
}

// writeUnpublished answers a publish=false deploy: the site as it still is,
// the version just stored and a preview link for it.
func (h *SiteHandler) writeUnpublished(w http.ResponseWriter, site db.Site, n int) {
	note := "Stored as version " + strconv.Itoa(n) + " without making it live; visitors still see version " + strconv.Itoa(site.ActiveVersion) +
		". Make it live with PUT /v1/sites/" + site.Name + "/active-version {\"version_number\": " + strconv.Itoa(n) + "}."
	resp := h.toSiteResponse(site, note)
	resp.UnpublishedVersion = n
	if link, exp, ok := h.previewLink(site, n); ok {
		resp.PreviewURL = link
		resp.PreviewExpiresAt = &exp
	} else {
		resp.Note += " No preview link: " + h.noPreviewWhy() + "."
	}
	writeJSON(w, http.StatusOK, resp)
}

// createPreviewLink is POST /v1/sites/{sitename}/versions/{version}/preview-link:
// the owner mints an hour-long preview address for one kept version.
func (h *SiteHandler) createPreviewLink(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}
	name := strings.ToLower(strings.TrimSpace(r.PathValue("sitename")))
	n, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || n < 1 {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "version not found"})
		return
	}
	site, err := h.siteForCaller(r, user.ID, name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeSiteNotFound(w, name)
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if refuseSuspendedSite(w, site) {
		return
	}
	kept, err := db.VersionKept(r.Context(), h.database, site.ID, n)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if !kept {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "version not found"})
		return
	}
	link, expires, ok := h.previewLink(site, n)
	if !ok {
		writeJSON(w, http.StatusConflict, errorResponse{Error: h.noPreviewWhy(), Code: "preview_unavailable"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"site":       site.Name,
		"version":    n,
		"live":       n == site.ActiveVersion,
		"url":        link,
		"expires_at": expires.UTC().Format(time.RFC3339),
		"expires_in": int(previewLinkTTL().Seconds()),
	})
}

// servePreview answers <prefix><n>/<token>/<rest> for site, where tail is
// everything after "__preview/" (decoded) and prefix the escaped path up to
// and including it.
func (h *SiteHandler) servePreview(w http.ResponseWriter, r *http.Request, site db.Site, tail, prefix string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	// Same-origin requests from a preview name it in Referer, which is how
	// saves from it are told apart and refused (previewReferer).
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	nStr, after, _ := strings.Cut(tail, "/")
	token, rest, hasSlash := strings.Cut(after, "/")
	n, err := strconv.Atoi(nStr)
	if err != nil || n < 1 || token == "" {
		h.renderPreviewExpired(w, r)
		return
	}
	siteID, version, err := h.checkPreviewToken(token, time.Now())
	if err != nil || siteID != site.ID || version != n {
		h.renderPreviewExpired(w, r)
		return
	}
	if !hasSlash {
		target := prefix + nStr + "/" + token + "/"
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusFound)
		return
	}
	if h.disk.IsSuspended(site.UserID, site.Name) {
		serveTakedown(w, r)
		return
	}
	kept, err := db.VersionKept(r.Context(), h.database, site.ID, n)
	if err != nil {
		log.Printf("preview %s v%d: %v", site.ID, n, err)
		h.renderServiceError(w, r)
		return
	}
	if !kept {
		h.renderPreviewExpired(w, r)
		return
	}
	h.serveDirFile(w, r, h.disk.VersionDir(site.UserID, site.Name, n), "/"+rest, r.URL.EscapedPath())
}

func (h *SiteHandler) renderPreviewExpired(w http.ResponseWriter, r *http.Request) {
	h.renderNotFoundPage(w, r, "This preview link has expired",
		"Preview links work for "+config.Span(previewLinkTTL())+". Make a new one from Versions on your Simple Host page, or ask your AI app.",
		h.mainSiteURL(), "Go to "+h.siteDomain)
}

// previewReferer reports whether a request was made by a page opened as a
// preview on this same host (its Referer path is a preview path).
func previewReferer(r *http.Request) bool {
	ref := r.Header.Get("Referer")
	if ref == "" {
		return false
	}
	u, err := url.Parse(ref)
	if err != nil || !strings.EqualFold(u.Hostname(), requestHostName(r)) {
		return false
	}
	p := strings.TrimPrefix(u.Path, "/")
	if strings.HasPrefix(p, previewPathSegment+"/") {
		return true
	}
	_, rest, _ := strings.Cut(p, "/")
	return strings.HasPrefix(rest, previewPathSegment+"/")
}

func writePreviewReadOnly(w http.ResponseWriter) {
	writeJSON(w, http.StatusForbidden, map[string]string{
		"error": "saving is turned off in a preview; make this version live first",
		"code":  "preview_read_only",
	})
}
