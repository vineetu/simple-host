package handler

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	db "github.com/vsriram/simple-host/internal/db"
)

const (
	writeRouteStatePut        = "state_put"
	writeRouteStatePatch      = "state_patch"
	writeRouteStatePutPath    = "state_put_path"
	writeRouteStateDeletePath = "state_delete_path"
	writeRouteCollectionPost  = "collection_post"
)

const (
	visitorCookieHost   = "__Host-sh_vsess"
	visitorCookieHTTP   = "sh_vsess"
	visitorCookieMaxAge = 1209600 // 14 days
	visitorCSRFHeader   = "X-SH-CSRF"
	visitorCSRFValue    = "1"
)

func requestIsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func visitorCookieName(r *http.Request) string {
	if requestIsHTTPS(r) {
		return visitorCookieHost
	}
	return visitorCookieHTTP
}

func requestHostName(r *http.Request) string {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.ToLower(host)
}

func setVisitorSessionCookie(w http.ResponseWriter, r *http.Request, value string, maxAge int) {
	secure := requestIsHTTPS(r)
	name := visitorCookieHTTP
	if secure {
		name = visitorCookieHost
	}
	// net/http: MaxAge=0 omits the attribute; MaxAge<0 emits Max-Age=0 (delete).
	if maxAge == 0 {
		maxAge = -1
	}
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func visitorCookieValue(r *http.Request) string {
	if c, err := r.Cookie(visitorCookieName(r)); err == nil && c.Value != "" {
		return c.Value
	}
	// Proto detection can disagree with when the cookie was issued; accept either name.
	if c, err := r.Cookie(visitorCookieHost); err == nil && c.Value != "" {
		return c.Value
	}
	if c, err := r.Cookie(visitorCookieHTTP); err == nil && c.Value != "" {
		return c.Value
	}
	return ""
}

func hasVisitorCSRF(r *http.Request) bool {
	if r.Header.Get(visitorCSRFHeader) == visitorCSRFValue {
		return true
	}
	ct := r.Header.Get("Content-Type")
	if ct == "" {
		return false
	}
	media, _, err := mime.ParseMediaType(ct)
	return err == nil && media == "application/json"
}

func (h *SiteHandler) isVisitorApexHost(host string) bool {
	host = strings.ToLower(host)
	if host == strings.ToLower(h.siteDomain) || host == "www."+strings.ToLower(h.siteDomain) {
		return true
	}
	raw := os.Getenv("PUBLIC_BASE_URL")
	if raw == "" {
		raw = "https://simple-host.app"
	}
	if u, err := url.Parse(raw); err == nil {
		if ah := strings.ToLower(u.Hostname()); ah != "" && host == ah {
			return true
		}
	}
	return false
}

// establishVisitor handles GET /v1/visitor/establish: consume the one-time
// token, Set-Cookie on this host, 302 to the stored return_to.
func (h *SiteHandler) establishVisitor(w http.ResponseWriter, r *http.Request) {
	host := requestHostName(r)
	if host == "" || h.isVisitorApexHost(host) {
		writeOAuthHTMLError(w, http.StatusBadRequest)
		return
	}

	once := strings.TrimSpace(r.URL.Query().Get("once"))
	if once == "" {
		writeOAuthHTMLError(w, http.StatusBadRequest)
		return
	}

	tok, err := db.ConsumeEstablishToken(r.Context(), h.database, once, host)
	if err != nil {
		writeOAuthHTMLError(w, http.StatusBadRequest)
		return
	}

	setVisitorSessionCookie(w, r, hex.EncodeToString(tok.SessionID), visitorCookieMaxAge)
	http.Redirect(w, r, tok.ReturnTo, http.StatusFound)
}

// logoutVisitor handles POST /v1/visitor/logout: delete this session and clear
// the cookie. Requires X-SH-CSRF: 1 or Content-Type: application/json.
func (h *SiteHandler) logoutVisitor(w http.ResponseWriter, r *http.Request) {
	if !hasVisitorCSRF(r) {
		writeJSON(w, http.StatusForbidden, struct {
			Error string `json:"error"`
			Code  string `json:"code"`
		}{Error: "missing CSRF header", Code: "csrf_required"})
		return
	}

	if raw := visitorCookieValue(r); raw != "" {
		if id, err := hex.DecodeString(raw); err == nil && len(id) == 32 {
			if err := db.DeleteVisitorSession(r.Context(), h.database, id); err != nil {
				writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
				return
			}
		}
	}
	setVisitorSessionCookie(w, r, "", 0)
	w.WriteHeader(http.StatusNoContent)
}

func writeVisitorAuthRequired(w http.ResponseWriter) {
	writeJSON(w, http.StatusUnauthorized, map[string]any{
		"error":   "sign-in required to write",
		"code":    "visitor_auth_required",
		"sign_in": "/v1/auth/oauth/providers",
		"retry":   true,
	})
}

// visitorWriteOK is the write gate for PUT/PATCH/DELETE state (whole document
// or a path) and POST collections.
// See SPEC.md §4.4. Returns false after writing the error response.
func (h *SiteHandler) visitorWriteOK(w http.ResponseWriter, r *http.Request, siteID, siteName, route, collection string) bool {
	userID, allowAnon, err := db.GetSiteWriteGate(r.Context(), h.database, siteID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return false
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return false
	}

	if key := r.Header.Get("X-API-Key"); key != "" {
		u, ok, resolveErr := h.resolveWriterKey(r.Context(), key)
		if resolveErr != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return false
		}
		if ok && (u.IsAdmin || u.ID == userID) {
			return true
		}
		writeJSON(w, http.StatusForbidden, struct {
			Error string `json:"error"`
			Code  string `json:"code"`
		}{Error: "forbidden", Code: "writer_forbidden"})
		return false
	}

	mode := h.writeAuthMode
	if mode == "" {
		mode = "log"
	}
	if mode == "off" {
		return true
	}

	if raw := visitorCookieValue(r); raw != "" {
		if id, decErr := hex.DecodeString(raw); decErr == nil && len(id) == 32 {
			sess, sessErr := db.GetVisitorSession(r.Context(), h.database, id)
			if sessErr == nil {
				now := time.Now()
				hostOK := strings.EqualFold(sess.Host, requestHostName(r))
				siteOK := sess.SiteID == siteID
				fresh := !now.After(sess.ExpiresAt) && !now.After(sess.IdleExpiresAt)
				if hostOK && siteOK && fresh {
					if r.Header.Get(visitorCSRFHeader) == visitorCSRFValue {
						_ = db.TouchVisitorSession(r.Context(), h.database, id)
						return true
					}
					if mode == "on" {
						writeJSON(w, http.StatusForbidden, struct {
							Error string `json:"error"`
							Code  string `json:"code"`
						}{Error: "missing CSRF header", Code: "csrf_required"})
						return false
					}
					// log: treat missing CSRF as anonymous so old widgets still write.
				}
			}
		}
	}

	outcome := "allowed"
	if mode == "on" {
		if allowAnon {
			outcome = "overridden"
		} else {
			outcome = "rejected"
		}
	}
	h.logAnonWrite(r, siteID, siteName, route, collection, mode, outcome)
	if outcome == "rejected" {
		writeVisitorAuthRequired(w)
		return false
	}
	return true
}

func (h *SiteHandler) resolveWriterKey(ctx context.Context, key string) (db.User, bool, error) {
	if subtle.ConstantTimeCompare([]byte(key), []byte(h.adminAPIKey)) == 1 {
		return db.User{ID: h.adminUserID, Username: "admin", IsAdmin: true}, true, nil
	}
	u, err := db.GetUserByAPIKey(ctx, h.database, key)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return db.User{}, false, nil
		}
		return db.User{}, false, err
	}
	return u, true, nil
}

func (h *SiteHandler) logAnonWrite(r *http.Request, siteID, name, route, collection, mode, outcome string) {
	// Never log a cookie value or a request body.
	log.Printf("anon_write site_id=%s name=%s route=%s collection=%s mode=%s outcome=%s origin_class=%s has_cookie=%t",
		siteID, name, route, collection, mode, outcome, h.originClass(r, siteID), visitorCookieValue(r) != "")
}

func (h *SiteHandler) originClass(r *http.Request, siteID string) string {
	origin := r.Header.Get("Origin")
	if origin == "" {
		if ref := r.Header.Get("Referer"); ref != "" {
			if u, err := url.Parse(ref); err == nil {
				origin = u.Scheme + "://" + u.Host
			}
		}
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return "allowed_origin"
	}
	if strings.EqualFold(u.Host, h.contentHost) {
		return "content_host"
	}
	if h.originIsBoundDomainID(r.Context(), siteID, u.Host) {
		return "custom_domain"
	}
	return "allowed_origin"
}
