package handler

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"log"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/config"
	db "github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/oauth"

	"golang.org/x/oauth2"
)

const (
	oauthStateTTL   = 10 * time.Minute
	oauthHTMLFailed = `<!DOCTYPE html><html><head><meta charset="utf-8"><title>Sign-in failed</title></head><body><p>Sign-in failed. Close this tab and try again from the site.</p></body></html>`
)

// visitorHandleRe / visitorSitenameRe are the path-model segments allowed in
// return_to (SPEC §2.1). Sitename is the nginx content-host charset, not the
// slightly stricter create-site DNS-label regex.
var (
	visitorHandleRe   = regexp.MustCompile(`^[a-z0-9-]{1,39}$`)
	visitorSitenameRe = regexp.MustCompile(`^[a-z0-9-]{1,63}$`)

	errOAuthEmailRefused = errors.New("oauth email refused")
	errOAuthAdminRefused = errors.New("oauth admin refused")
)

type OAuthHandler struct {
	database    *sql.DB
	cfg         config.Config
	providers   map[string]oauth.Provider
	ipLimiter   *rateLimiter
	lookupIP    func(ctx context.Context, host string) ([]net.IP, error)
	lookupCNAME func(ctx context.Context, host string) (string, error)
}

func NewOAuthHandler(database *sql.DB, cfg config.Config) *OAuthHandler {
	providers := make(map[string]oauth.Provider, 2)
	if cfg.GoogleOAuthEnabled() {
		providers["google"] = oauth.NewGoogle(cfg.GoogleOAuthClientID, cfg.GoogleOAuthClientSecret, cfg.OAuthRedirectURI("google"))
	}
	if cfg.GitHubOAuthEnabled() {
		providers["github"] = oauth.NewGitHub(cfg.GitHubOAuthClientID, cfg.GitHubOAuthClientSecret, cfg.OAuthRedirectURI("github"))
	}
	ipLimiter := newRateLimiter(20, 0.2)
	ipLimiter.startCleanup(10*time.Minute, 30*time.Minute)
	h := &OAuthHandler{
		database:    database,
		cfg:         cfg,
		providers:   providers,
		ipLimiter:   ipLimiter,
		lookupIP:    defaultLookupIP,
		lookupCNAME: defaultLookupCNAME,
	}
	h.startVisitorAuthSweep(time.Hour)
	return h
}

func (h *OAuthHandler) Register(mux *http.ServeMux) {
	// Always register the string literals, even when no provider is enabled.
	mux.HandleFunc("GET /v1/auth/oauth/providers", h.listProviders)
	mux.Handle("GET /v1/auth/oauth/{provider}/callback", rateLimitByIP(h.ipLimiter, http.HandlerFunc(h.callback)))
	mux.Handle("GET /v1/auth/oauth/{provider}", rateLimitByIP(h.ipLimiter, http.HandlerFunc(h.start)))
}

func (h *OAuthHandler) listProviders(w http.ResponseWriter, r *http.Request) {
	names := make([]string, 0, 2)
	if _, ok := h.providers["google"]; ok {
		names = append(names, "google")
	}
	if _, ok := h.providers["github"]; ok {
		names = append(names, "github")
	}
	writeJSON(w, http.StatusOK, map[string][]string{"providers": names})
}

func (h *OAuthHandler) start(w http.ResponseWriter, r *http.Request) {
	name := strings.ToLower(strings.TrimSpace(r.PathValue("provider")))
	p, ok := h.providers[name]
	if !ok || (name != "google" && name != "github") {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "not found"})
		return
	}

	sanitized, siteID, host, purpose, err := h.sanitizeReturnTo(r.Context(), r.URL.Query().Get("return_to"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid return_to"})
		return
	}

	if err := db.PruneExpiredOAuthStates(r.Context(), h.database); err != nil {
		log.Printf("oauth: prune states: %v", err)
	}

	state, err := randomHex(32)
	if err != nil {
		log.Printf("oauth: random state: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	verifier := oauth2.GenerateVerifier()
	expiresAt := time.Now().Add(oauthStateTTL)
	if err := db.InsertOAuthState(r.Context(), h.database, state, name, verifier, sanitized, host, siteID, purpose, expiresAt); err != nil {
		log.Printf("oauth: insert state: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	http.Redirect(w, r, p.AuthCodeURL(state, verifier), http.StatusFound)
}

func (h *OAuthHandler) callback(w http.ResponseWriter, r *http.Request) {
	name := strings.ToLower(strings.TrimSpace(r.PathValue("provider")))
	p, ok := h.providers[name]
	if !ok || (name != "google" && name != "github") {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "not found"})
		return
	}

	q := r.URL.Query()
	state := strings.TrimSpace(q.Get("state"))
	if state == "" {
		writeOAuthHTMLError(w, http.StatusBadRequest)
		return
	}

	st, err := db.ConsumeOAuthState(r.Context(), h.database, state)
	if err != nil {
		writeOAuthHTMLError(w, http.StatusBadRequest)
		return
	}
	if st.Provider != name {
		writeOAuthHTMLError(w, http.StatusBadRequest)
		return
	}
	if q.Get("error") != "" || strings.TrimSpace(q.Get("code")) == "" {
		writeOAuthHTMLError(w, http.StatusBadRequest)
		return
	}

	ident, err := p.Exchange(r.Context(), strings.TrimSpace(q.Get("code")), st.CodeVerifier)
	if err != nil {
		log.Printf("oauth: %s exchange: %v", name, err)
		writeOAuthHTMLError(w, http.StatusBadGateway)
		return
	}
	if ident.UserID == "" {
		writeOAuthHTMLError(w, http.StatusBadGateway)
		return
	}

	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		log.Printf("oauth: begin: %v", err)
		writeOAuthHTMLError(w, http.StatusBadGateway)
		return
	}
	defer tx.Rollback()

	user, created, err := resolveUser(r.Context(), tx, ident)
	if errors.Is(err, errOAuthEmailRefused) || errors.Is(err, errOAuthAdminRefused) {
		writeOAuthHTMLError(w, http.StatusBadRequest)
		return
	}
	if err != nil {
		log.Printf("oauth: resolve user: %v", err)
		writeOAuthHTMLError(w, http.StatusBadGateway)
		return
	}

	if st.Purpose == "owner" {
		if created || !user.Handle.Valid {
			assignHandle(r.Context(), tx, user.ID, user.Username)
		}
		linkToken, err := generateLinkToken(24)
		if err != nil {
			log.Printf("oauth: owner link token: %v", err)
			writeOAuthHTMLError(w, http.StatusBadGateway)
			return
		}
		code, err := generateNumericCode(6)
		if err != nil {
			log.Printf("oauth: owner unused code: %v", err)
			writeOAuthHTMLError(w, http.StatusBadGateway)
			return
		}
		if err := db.CreateAuthToken(r.Context(), tx, user.Username, code, linkToken, time.Now().Add(authTokenTTL)); err != nil {
			log.Printf("oauth: owner auth token: %v", err)
			writeOAuthHTMLError(w, http.StatusBadGateway)
			return
		}
		if err := tx.Commit(); err != nil {
			log.Printf("oauth: commit: %v", err)
			writeOAuthHTMLError(w, http.StatusBadGateway)
			return
		}
		dest := strings.TrimRight(h.cfg.PublicBaseURL, "/") + "/?token=" + url.QueryEscape(linkToken)
		http.Redirect(w, r, dest, http.StatusFound)
		return
	}

	if !st.SiteID.Valid || st.SiteID.String == "" || st.Host == "" {
		writeOAuthHTMLError(w, http.StatusBadRequest)
		return
	}

	// The session cookie token is generated in establishVisitor, not here,
	// so the database never stores a usable credential. This row carries
	// user_id/site_id; only sha256(once) is written.
	once, err := randomHex(32)
	if err != nil {
		log.Printf("oauth: establish once: %v", err)
		writeOAuthHTMLError(w, http.StatusBadGateway)
		return
	}
	if err := db.InsertEstablishToken(r.Context(), tx, once, user.ID, st.SiteID.String, st.Host, st.ReturnTo, time.Now().Add(60*time.Second)); err != nil {
		log.Printf("oauth: insert establish: %v", err)
		writeOAuthHTMLError(w, http.StatusBadGateway)
		return
	}
	if err := tx.Commit(); err != nil {
		log.Printf("oauth: commit: %v", err)
		writeOAuthHTMLError(w, http.StatusBadGateway)
		return
	}

	scheme := "https"
	if u, err := url.Parse(st.ReturnTo); err == nil && u.Scheme != "" {
		scheme = u.Scheme
	}
	dest := scheme + "://" + st.Host + "/v1/visitor/establish?once=" + url.QueryEscape(once)
	http.Redirect(w, r, dest, http.StatusFound)
}

func writeOAuthHTMLError(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(oauthHTMLFailed))
}

// resolveUser maps a provider identity onto one users row.
// Re-logins key only on (provider, provider_user_id). A missing or unverified
// email is refused and creates nothing.
func resolveUser(ctx context.Context, q db.Querier, ident oauth.Identity) (db.User, bool, error) {
	existing, err := db.GetOAuthIdentity(ctx, q, ident.Provider, ident.UserID)
	if err == nil {
		user, uerr := db.GetUserByID(ctx, q, existing.UserID)
		if uerr != nil {
			return db.User{}, false, uerr
		}
		if terr := db.TouchOAuthIdentity(ctx, q, existing.ID, ident.Email, ident.EmailVerified); terr != nil {
			return db.User{}, false, terr
		}
		return user, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return db.User{}, false, err
	}

	email := strings.ToLower(strings.TrimSpace(ident.Email))
	if email == "" || !ident.EmailVerified || !validEmail.MatchString(email) {
		if email == "" {
			log.Printf("oauth: refused unverified or missing email provider=%s", ident.Provider)
		} else {
			log.Printf("oauth: refused unverified or missing email provider=%s email=%s", ident.Provider, redactEmail(email))
		}
		return db.User{}, false, errOAuthEmailRefused
	}

	user, err := db.GetUserByUsername(ctx, q, email)
	created := false
	switch {
	case err == nil:
		if user.Username == "admin" || user.IsAdmin {
			log.Printf("oauth: refused admin link provider=%s", ident.Provider)
			return db.User{}, false, errOAuthAdminRefused
		}
	case errors.Is(err, sql.ErrNoRows):
		apiKey, kerr := auth.GenerateAPIKey()
		if kerr != nil {
			return db.User{}, false, kerr
		}
		user, err = db.CreateUser(ctx, q, email, apiKey, false)
		if err != nil {
			if isUniqueViolation(err) {
				user, err = db.GetUserByUsername(ctx, q, email)
			}
			if err != nil {
				return db.User{}, false, err
			}
		} else {
			created = true
		}
		if user.Username == "admin" || user.IsAdmin {
			log.Printf("oauth: refused admin link provider=%s", ident.Provider)
			return db.User{}, false, errOAuthAdminRefused
		}
	default:
		return db.User{}, false, err
	}

	if _, err := db.InsertOAuthIdentity(ctx, q, user.ID, ident.Provider, ident.UserID, email, ident.EmailVerified); err != nil {
		if isUniqueViolation(err) {
			// Concurrent callback won the race — reload the linked row.
			linked, lerr := db.GetOAuthIdentity(ctx, q, ident.Provider, ident.UserID)
			if lerr != nil {
				return db.User{}, false, lerr
			}
			user, uerr := db.GetUserByID(ctx, q, linked.UserID)
			return user, false, uerr
		}
		return db.User{}, false, err
	}
	return user, created, nil
}

func redactEmail(email string) string {
	local, domain, ok := strings.Cut(email, "@")
	if !ok || local == "" || domain == "" {
		return "***"
	}
	return string([]rune(local)[0]) + "***@" + domain
}

// sanitizeReturnTo validates return_to at start time. On success it returns
// parsed.String(), the resolved site_id (null for owner), hostname, and purpose.
func (h *OAuthHandler) sanitizeReturnTo(ctx context.Context, raw string) (string, sql.NullString, string, string, error) {
	parsed, err := parseAbsoluteReturnTo(raw, h.cfg.PublicBaseURL)
	if err != nil {
		return "", sql.NullString{}, "", "", err
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return "", sql.NullString{}, "", "", errInvalidReturnTo
	}

	if ownerReturnToOK(parsed, h.cfg.PublicBaseURL) {
		return parsed.String(), sql.NullString{}, "", "owner", nil
	}

	if isRejectedPlatformHost(host, h.cfg.SiteDomain, h.cfg.ContentHost, publicBaseHost(h.cfg.PublicBaseURL)) {
		return "", sql.NullString{}, "", "", errInvalidReturnTo
	}

	if strings.EqualFold(host, h.cfg.ContentHost) {
		handle, sitename, ok := splitContentHostPath(parsed.Path)
		if !ok {
			return "", sql.NullString{}, "", "", errInvalidReturnTo
		}
		user, err := db.GetUserByHandle(ctx, h.database, handle)
		if err != nil {
			return "", sql.NullString{}, "", "", errInvalidReturnTo
		}
		site, err := db.GetSiteByUser(ctx, h.database, user.ID, sitename)
		if err != nil {
			return "", sql.NullString{}, "", "", errInvalidReturnTo
		}
		return parsed.String(), sql.NullString{String: site.ID, Valid: true}, host, "site", nil
	}

	info, err := db.GetSiteByCustomDomain(ctx, h.database, host)
	if err != nil {
		return "", sql.NullString{}, "", "", errInvalidReturnTo
	}
	if !strings.EqualFold(info.Domain, host) {
		return "", sql.NullString{}, "", "", errInvalidReturnTo
	}
	if err := h.proveCustomDomainControl(ctx, host); err != nil {
		return "", sql.NullString{}, "", "", errInvalidReturnTo
	}
	return parsed.String(), sql.NullString{String: info.SiteID, Valid: true}, host, "site", nil
}

var errInvalidReturnTo = errors.New("invalid return_to")

func parseAbsoluteReturnTo(raw, publicBaseURL string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errInvalidReturnTo
	}
	parsed, err := url.Parse(raw)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" || parsed.Opaque != "" {
		return nil, errInvalidReturnTo
	}
	if parsed.User != nil {
		return nil, errInvalidReturnTo
	}
	httpOK := publicBaseIsHTTP(publicBaseURL)
	switch parsed.Scheme {
	case "https":
	case "http":
		if !httpOK {
			return nil, errInvalidReturnTo
		}
	default:
		return nil, errInvalidReturnTo
	}
	if port := parsed.Port(); port != "" {
		schemeDefault := (parsed.Scheme == "https" && port == "443") || (parsed.Scheme == "http" && port == "80")
		if !schemeDefault && port != publicBasePort(publicBaseURL) {
			return nil, errInvalidReturnTo
		}
	}
	return parsed, nil
}

func publicBaseIsHTTP(publicBaseURL string) bool {
	u, err := url.Parse(publicBaseURL)
	return err == nil && strings.EqualFold(u.Scheme, "http")
}

func publicBaseHost(publicBaseURL string) string {
	u, err := url.Parse(publicBaseURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

func publicBasePort(publicBaseURL string) string {
	u, err := url.Parse(publicBaseURL)
	if err != nil {
		return ""
	}
	return u.Port()
}

// ownerReturnToOK is the dashboard allow-list: exact PUBLIC_BASE_URL host,
// path Clean is "/", empty query, empty fragment. Apex is still rejected as
// a site return_to via isRejectedPlatformHost.
func ownerReturnToOK(parsed *url.URL, publicBaseURL string) bool {
	if parsed == nil {
		return false
	}
	base, err := url.Parse(publicBaseURL)
	if err != nil || base.Host == "" {
		return false
	}
	if !strings.EqualFold(parsed.Scheme, base.Scheme) {
		return false
	}
	if !strings.EqualFold(parsed.Hostname(), base.Hostname()) {
		return false
	}
	pPort, bPort := parsed.Port(), base.Port()
	if pPort != bPort {
		// Treat omitted vs scheme-default as the same port.
		if !((pPort == "" || isSchemeDefaultPort(parsed.Scheme, pPort)) &&
			(bPort == "" || isSchemeDefaultPort(base.Scheme, bPort))) {
			return false
		}
		if pPort != "" && bPort != "" && pPort != bPort {
			return false
		}
	}
	if parsed.User != nil {
		return false
	}
	if path.Clean("/"+parsed.Path) != "/" {
		return false
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	return true
}

func isSchemeDefaultPort(scheme, port string) bool {
	return (scheme == "https" && port == "443") || (scheme == "http" && port == "80")
}

func isRejectedPlatformHost(host, siteDomain, contentHost, publicBaseHostName string) bool {
	host = strings.ToLower(host)
	siteDomain = strings.ToLower(strings.TrimSpace(siteDomain))
	contentHost = strings.ToLower(strings.TrimSpace(contentHost))
	publicBaseHostName = strings.ToLower(strings.TrimSpace(publicBaseHostName))
	if host == siteDomain || host == "www."+siteDomain || (publicBaseHostName != "" && host == publicBaseHostName) {
		return true
	}
	if contentHost != "" && host == contentHost {
		return false
	}
	// Legacy <name>.<siteDomain> host.
	if siteDomain != "" && strings.HasSuffix(host, "."+siteDomain) {
		return true
	}
	return false
}

func splitContentHostPath(p string) (handle, sitename string, ok bool) {
	cleaned := path.Clean("/" + p)
	parts := strings.Split(strings.Trim(cleaned, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	handle, sitename = parts[0], parts[1]
	if !visitorHandleRe.MatchString(handle) || !visitorSitenameRe.MatchString(sitename) {
		return "", "", false
	}
	prefix := "/" + handle + "/" + sitename
	if cleaned != prefix && !strings.HasPrefix(cleaned, prefix+"/") {
		return "", "", false
	}
	return handle, sitename, true
}

func (h *OAuthHandler) proveCustomDomainControl(ctx context.Context, host string) error {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	target := strings.ToLower(strings.TrimSuffix(h.cfg.CNAMETarget, "."))

	if h.cfg.CustomDomainIP != "" && isApexDomain(host) {
		ips, err := h.lookupIP(ctx, host)
		if err != nil {
			return err
		}
		if ipsContainIPv4(ips, h.cfg.CustomDomainIP) {
			return nil
		}
		return errInvalidReturnTo
	}

	if target != "" {
		canon, err := h.lookupCNAME(ctx, host)
		if err == nil {
			canon = strings.ToLower(strings.TrimSuffix(canon, "."))
			if canon == target {
				return nil
			}
		}
	}
	if h.cfg.CustomDomainIP != "" {
		ips, err := h.lookupIP(ctx, host)
		if err != nil {
			return err
		}
		if ipsContainIPv4(ips, h.cfg.CustomDomainIP) {
			return nil
		}
	}
	return errInvalidReturnTo
}

func ipsContainIPv4(ips []net.IP, want string) bool {
	for _, ip := range ips {
		v4 := ip.To4()
		if v4 != nil && v4.String() == want {
			return true
		}
	}
	return false
}

func defaultLookupIP(ctx context.Context, host string) ([]net.IP, error) {
	return net.DefaultResolver.LookupIP(ctx, "ip", host)
}

func defaultLookupCNAME(ctx context.Context, host string) (string, error) {
	return net.DefaultResolver.LookupCNAME(ctx, host)
}

func (h *OAuthHandler) startVisitorAuthSweep(every time.Duration) {
	go func() {
		time.Sleep(30 * time.Second)
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			if err := db.SweepVisitorAuth(ctx, h.database); err != nil {
				log.Printf("visitor auth sweep: %v", err)
			}
			cancel()
			time.Sleep(every)
		}
	}()
}

func randomRaw(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}

func randomHex(n int) (string, error) {
	b, err := randomRaw(n)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
