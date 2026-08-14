package handler

import (
	"encoding/hex"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"

	db "github.com/vsriram/simple-host/internal/db"
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
