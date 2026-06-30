package handler

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"html"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/vsriram/simple-host/internal/auth"
	db "github.com/vsriram/simple-host/internal/db"
)

const viewCookiePrefix = "shview_"
const viewCookieTTL = 7 * 24 * time.Hour

// viewCookieName is per-site so the cookie can be set domain-wide (sent to both
// the subdomain page AND the apex state API) without sites colliding.
func viewCookieName(site string) string { return viewCookiePrefix + site }

// viewLocks is an in-memory cache of site name -> bcrypt view-password hash,
// loaded at boot and updated on set/clear. nginx's auth_request subrequest hits
// viewAuth on every page view, so an UNLOCKED view is just a memory lookup (no
// DB, no bcrypt); bcrypt runs only when someone submits a password.
type viewLocks struct {
	mu sync.RWMutex
	m  map[string]string
}

func newViewLocks() *viewLocks { return &viewLocks{m: map[string]string{}} }

func (v *viewLocks) get(name string) (string, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	h, ok := v.m[name]
	return h, ok
}
func (v *viewLocks) set(name, hash string) {
	v.mu.Lock()
	v.m[name] = hash
	v.mu.Unlock()
}
func (v *viewLocks) del(name string) {
	v.mu.Lock()
	delete(v.m, name)
	v.mu.Unlock()
}

func siteFromHost(host string) string {
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	if i := strings.IndexByte(host, '.'); i >= 0 {
		return host[:i]
	}
	return host
}

// makeViewCookie / checkViewCookie sign a short-lived "this browser entered the
// correct password for this site" token with HMAC — so we never re-run bcrypt
// after the first successful unlock.
func (h *SiteHandler) makeViewCookie(site string, exp int64) string {
	mac := hmac.New(sha256.New, h.viewSecret)
	mac.Write([]byte(site + "." + strconv.FormatInt(exp, 10)))
	return strconv.FormatInt(exp, 10) + "." + hex.EncodeToString(mac.Sum(nil))
}

func (h *SiteHandler) checkViewCookie(site, val string) bool {
	i := strings.IndexByte(val, '.')
	if i < 0 {
		return false
	}
	exp, err := strconv.ParseInt(val[:i], 10, 64)
	if err != nil || exp < time.Now().Unix() {
		return false
	}
	return hmac.Equal([]byte(val), []byte(h.makeViewCookie(site, exp)))
}

// viewAuth is the nginx auth_request target: 200 = allow, 401 = show the login
// page (nginx maps 401 -> the login form). No WWW-Authenticate, so the browser
// never shows its native dialog.
func (h *SiteHandler) viewAuth(w http.ResponseWriter, r *http.Request) {
	if h.viewSessionOK(r, siteFromHost(r.Host)) {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusUnauthorized)
}

// viewSessionOK reports whether a request may access a site: true if the site is
// not locked, or if it carries a valid view-session cookie. Used both by the
// nginx page gate (viewAuth) and the state API (so locking a page also locks its
// data — closing the "KV gap").
func (h *SiteHandler) viewSessionOK(r *http.Request, name string) bool {
	if _, locked := h.locks.get(name); !locked {
		return true
	}
	c, err := r.Cookie(viewCookieName(name))
	return err == nil && h.checkViewCookie(name, c.Value)
}

// viewLoginPage renders the custom password gate (served by nginx on a 401).
func (h *SiteHandler) viewLoginPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(viewLoginHTML(siteFromHost(r.Host), false)))
}

// viewLogin handles the password submission: bcrypt-check, set the signed
// cookie, redirect back to the page.
func (h *SiteHandler) viewLogin(w http.ResponseWriter, r *http.Request) {
	name := siteFromHost(r.Host)
	hash, locked := h.locks.get(name)
	if !locked {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	_ = r.ParseForm()
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(r.PostFormValue("password"))) != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(viewLoginHTML(name, true)))
		return
	}
	exp := time.Now().Add(viewCookieTTL).Unix()
	http.SetCookie(w, &http.Cookie{
		Name: viewCookieName(name), Value: h.makeViewCookie(name, exp), Path: "/",
		// Domain-wide so the cookie is also sent to the apex state API (cross-origin
		// from the page), letting the KV honor the same view lock.
		Domain:   h.siteDomain,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: int(viewCookieTTL.Seconds()),
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// setViewPassword (owner only) locks viewing behind a single password.
func (h *SiteHandler) setViewPassword(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "site name is required"})
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}
	if strings.TrimSpace(req.Password) == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "password is required"})
		return
	}
	site, err := db.GetSiteByUser(r.Context(), h.database, user.ID, siteName)
	if err != nil {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if err := db.SetViewPasswordHash(r.Context(), h.database, site.ID, string(hash)); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	h.locks.set(siteName, string(hash))
	writeJSON(w, http.StatusOK, map[string]any{"status": "locked", "name": siteName})
}

// deleteViewPassword (owner only) removes the view lock.
func (h *SiteHandler) deleteViewPassword(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "site name is required"})
		return
	}
	site, err := db.GetSiteByUser(r.Context(), h.database, user.ID, siteName)
	if err != nil {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
		return
	}
	if err := db.SetViewPasswordHash(r.Context(), h.database, site.ID, ""); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	h.locks.del(siteName)
	writeJSON(w, http.StatusOK, map[string]any{"status": "unlocked", "name": siteName})
}

// viewLoginHTML is the password-only gate page — modern, centered, no username.
func viewLoginHTML(site string, wrong bool) string {
	errBlock := ""
	if wrong {
		errBlock = `<p class="err">Incorrect password. Try again.</p>`
	}
	return `<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Private page</title>
<style>
:root{--accent:#5b5ef4}
*{box-sizing:border-box}
body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;
 font:16px/1.5 system-ui,-apple-system,sans-serif;color:#1a1a1a;
 background:linear-gradient(135deg,#eef0ff,#f7f5ff 60%,#fff)}
.card{background:#fff;border:1px solid #eceaf6;border-radius:18px;padding:32px 28px;width:340px;max-width:92vw;
 box-shadow:0 18px 50px rgba(40,40,90,.12);text-align:center}
.lock{width:52px;height:52px;border-radius:14px;background:var(--accent);display:flex;align-items:center;justify-content:center;margin:0 auto 14px;font-size:24px}
h1{font-size:19px;margin:0 0 4px;letter-spacing:-.2px}
p.sub{color:#6b6b78;margin:0 0 20px;font-size:14px}
input{width:100%;padding:12px 14px;border:1px solid #ddd;border-radius:10px;font:inherit;text-align:center}
input:focus{outline:none;border-color:var(--accent);box-shadow:0 0 0 3px rgba(91,94,244,.15)}
button{width:100%;margin-top:12px;padding:12px;border:0;border-radius:10px;background:var(--accent);color:#fff;
 font:600 15px system-ui;cursor:pointer}
button:hover{filter:brightness(1.05)}
.err{color:#d33;font-size:13px;margin:12px 0 0}
.foot{margin-top:18px;color:#a6a6b3;font-size:12px}
</style></head>
<body>
<form class="card" method="POST" action="/__view_login" autocomplete="off">
 <div class="lock">🔒</div>
 <h1>This page is private</h1>
 <p class="sub">Enter the password to view <b>` + html.EscapeString(site) + `</b>.</p>
 <input type="password" name="password" placeholder="Password" autofocus required>
 <button type="submit">View page</button>
 ` + errBlock + `
 <div class="foot">Protected by simple-host</div>
</form>
</body></html>`
}
