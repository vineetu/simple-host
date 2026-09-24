package handler

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
	db "github.com/vsriram/simple-host/internal/db"
)

// Private collections (owner decision 2026-09-24; reverses "nothing gates
// reading" for these lists only).
//
// The owner marks a collection private. From then on:
//   - Submitting needs a visitor signed in on the site's OWN domain (a proven
//     custom domain or a claimed <name>.<SITE_DOMAIN>), from a page on that
//     same origin. API keys cannot submit, and the shared host refuses it.
//     The server stamps every item with the submitter's verified email
//     (_submitted_by) and the time (_submitted_at); whatever the page sent
//     under those keys is overwritten, and the account id is kept in a column
//     the request cannot reach (collection_items.submitted_by).
//   - Reading is the owner's: their API key or connector token (REST, MCP,
//     CSV export, the dashboard), or the owner signed in as a visitor on the
//     site's own domain. Everyone else — anonymous, other signed-in visitors,
//     other sites, scripts — gets 404.
//
// Browser access rides on the host-only __Host- visitor cookie, and is only
// honoured for same-origin requests: every site on the shared host and every
// claimed <name>.<SITE_DOMAIN> is "same-site" with every other, so SameSite=Lax
// alone would let a hostile page there send the cookie along.

const privateListNeedsDomain = "private lists need the site on its own domain — connect one first (a free <name>.%s address works, or your own domain)"

// errPrivateNotFound is the one answer anyone without access gets.
func writePrivateNotFound(w http.ResponseWriter) {
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found", "code": "not_found"})
}

// requestOrigin is the scheme://host the browser says the request came from:
// Origin, else the Referer's origin. Empty when neither is usable.
func requestOrigin(r *http.Request) string {
	if o := r.Header.Get("Origin"); o != "" && o != "null" {
		return o
	}
	if ref := r.Header.Get("Referer"); ref != "" {
		if u, err := url.Parse(ref); err == nil && u.Scheme != "" && u.Host != "" {
			return u.Scheme + "://" + u.Host
		}
	}
	return ""
}

// sameOriginRequest: the request was made by a page on this very origin. A
// browser cannot forge Origin/Referer or Sec-Fetch-Site; a script outside a
// browser can, but then it has no visitor cookie to go with them.
func sameOriginRequest(r *http.Request) bool {
	if sfs := r.Header.Get("Sec-Fetch-Site"); sfs != "" && sfs != "same-origin" {
		return false
	}
	o := requestOrigin(r)
	if o == "" {
		return false
	}
	u, err := url.Parse(o)
	if err != nil || u.Host == "" {
		return false
	}
	want := "http"
	if requestIsHTTPS(r) {
		want = "https"
	}
	return strings.EqualFold(u.Scheme, want) && strings.EqualFold(u.Host, r.Host)
}

// strictVisitorCookie is the visitor session cookie without the fallback
// visitorCookieValue allows: over HTTPS only the __Host- cookie counts, since
// a sibling host under the same parent domain can plant a plain sh_vsess
// cookie (Domain=parent) but can never set a __Host- one.
func strictVisitorCookie(r *http.Request) string {
	name := visitorCookieHTTP
	if requestIsHTTPS(r) {
		name = visitorCookieHost
	}
	if c, err := r.Cookie(name); err == nil {
		return c.Value
	}
	return ""
}

// strictVisitorSession returns the visitor signed in to siteID on this host,
// for a same-origin request carrying the __Host- cookie. ok=false otherwise.
func (h *SiteHandler) strictVisitorSession(r *http.Request, siteID string) (db.VisitorSession, bool) {
	if !sameOriginRequest(r) {
		return db.VisitorSession{}, false
	}
	id, err := hex.DecodeString(strictVisitorCookie(r))
	if err != nil || len(id) != 32 {
		return db.VisitorSession{}, false
	}
	sess, err := db.GetVisitorSession(r.Context(), h.database, id)
	if err != nil {
		return db.VisitorSession{}, false
	}
	now := time.Now()
	if !strings.EqualFold(sess.Host, requestHostName(r)) || sess.SiteID != siteID ||
		now.After(sess.ExpiresAt) || now.After(sess.IdleExpiresAt) {
		return db.VisitorSession{}, false
	}
	return sess, true
}

// visitorEmail is the verified address of an account: its latest verified
// sign-in identity (Google), else the address it signed up with by emailed
// code. The same answer GET /me gives the page.
func visitorEmail(ctx context.Context, database *sql.DB, userID string) (string, error) {
	identity, err := db.GetLatestOAuthIdentity(ctx, database, userID)
	if err == nil && identity.Email.Valid && identity.Email.String != "" {
		return identity.Email.String, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	u, err := db.GetUserByID(ctx, database, userID)
	if err != nil {
		return "", err
	}
	return u.Username, nil
}

// onOwnDomain: this request arrived on the site's proven own domain. Returns
// the binding (for the owner's user id).
func (h *SiteHandler) onOwnDomain(r *http.Request, siteID string) (db.SiteDomainInfo, bool, error) {
	info, ok, err := h.siteOwnDomain(r.Context(), siteID)
	if err != nil || !ok {
		return db.SiteDomainInfo{}, false, err
	}
	return info, strings.EqualFold(info.Domain, requestHostName(r)), nil
}

// ownerBrowserRead decides a non-key read of a private collection: only the
// site owner, signed in as a visitor on the site's own domain, from a page on
// that domain. Anyone else gets the 404 (written here) and ok=false.
//
// A submitter reading back their own items is deliberately not offered: the
// visitor Google sign-in hand-off (/v1/visitor/establish) is not bound to the
// browser that started it, so an attacker can sign a victim's browser in as
// the attacker (login CSRF) and would then read back what the victim
// submitted. A page shows the submitter what they sent from the POST answer.
func (h *SiteHandler) ownerBrowserRead(w http.ResponseWriter, r *http.Request, siteID string) bool {
	if strings.EqualFold(requestHostName(r), h.contentHost) {
		writePrivateNotFound(w)
		return false
	}
	info, here, err := h.onOwnDomain(r, siteID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return false
	}
	if !here {
		writePrivateNotFound(w)
		return false
	}
	sess, ok := h.strictVisitorSession(r, siteID)
	if !ok || sess.UserID != info.UserID {
		writePrivateNotFound(w)
		return false
	}
	_ = db.TouchVisitorSession(r.Context(), h.database, sess.ID)
	return true
}

// appendPrivate is POST to a private collection. Returns after answering.
func (h *SiteHandler) appendPrivate(w http.ResponseWriter, r *http.Request, siteID, siteName, coll string) {
	w.Header().Set("Cache-Control", "private, no-store")
	onShared := strings.EqualFold(requestHostName(r), h.contentHost)
	info, here, err := h.onOwnDomain(r, siteID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if onShared && info.Domain != "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"error": "this site saves on its own domain", "code": "use_custom_domain", "domain": info.Domain,
		})
		return
	}
	if r.Header.Get("X-API-Key") != "" {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "this list is private: only visitors signed in on the site's own domain can add to it; API keys cannot",
			"code":  "private_visitor_only",
		})
		return
	}
	if !here {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "this list is private: it takes submissions only on the site's own domain, from a signed-in visitor",
			"code":  "private_needs_own_domain",
		})
		return
	}
	sess, ok := h.strictVisitorSession(r, siteID)
	if !ok {
		h.logAnonWrite(r, siteID, siteName, writeRouteCollectionPost, coll, "on", "private_rejected")
		writeVisitorAuthRequired(w)
		return
	}
	if r.Header.Get(visitorCSRFHeader) != visitorCSRFValue {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "missing CSRF header", "code": "csrf_required"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxCollectionItemSize)
	var fields map[string]json.RawMessage
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&fields); err != nil || fields == nil || dec.More() {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "item too large"})
			return
		}
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "a private list takes one JSON object per item"})
		return
	}
	email, err := visitorEmail(r.Context(), h.database, sess.UserID)
	if err != nil || email == "" {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	// Server-stamped: whatever the page sent under these keys is replaced.
	by, _ := json.Marshal(email)
	at, _ := json.Marshal(time.Now().UTC().Format(time.RFC3339))
	fields["_submitted_by"] = by
	fields["_submitted_at"] = at
	body, err := json.Marshal(fields)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid item"})
		return
	}
	item, err := db.AppendSubmittedItemByID(r.Context(), h.database, siteID, coll, body, sess.UserID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	_ = db.TouchVisitorSession(r.Context(), h.database, sess.ID)
	writeJSON(w, http.StatusCreated, item)
}

// setCollectionPrivacy is PUT /v1/sites/{sitename}/collections/{coll}/privacy
// {"private": true|false}: the owner (API key or connector token) chooses.
// Making a list private needs the site on its own proven domain.
func (h *SiteHandler) setCollectionPrivacy(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	coll := strings.TrimSpace(r.PathValue("coll"))
	if siteName == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "site name is required"})
		return
	}
	if !validCollectionName.MatchString(coll) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid collection name"})
		return
	}
	var req struct {
		Private *bool `json:"private"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || req.Private == nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: `send {"private": true} or {"private": false}`})
		return
	}
	siteID, err := h.ownedSiteID(r, user, siteName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	info, hasDomain, err := h.siteOwnDomain(r.Context(), siteID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if *req.Private && !hasDomain {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": strings.Replace(privateListNeedsDomain, "%s", h.siteDomain, 1),
			"code":  "custom_domain_required",
		})
		return
	}
	if err := db.SetCollectionPrivate(r.Context(), h.database, siteID, coll, *req.Private); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	resp := map[string]any{"site": siteName, "collection": coll, "private": *req.Private}
	if *req.Private {
		resp["domain"] = info.Domain
		resp["message"] = "Private: visitors signed in on https://" + info.Domain + " can add to it; only you can read it."
	} else {
		resp["message"] = "Public: anyone can read this list again, including everything already in it."
	}
	writeJSON(w, http.StatusOK, resp)
}
