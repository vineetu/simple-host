// Package app holds the running service: its config, its database, the
// three cores and the route tables every feature mounts on. main builds it
// from the environment; the test kit builds it against a throwaway database.
package app

import (
	"context"
	"database/sql"
	"net/http"
	"os"
	"strings"
	"time"

	access "simplehost/access/core"
	"simplehost/core/db"
	"simplehost/core/guard"
	"simplehost/core/limits"
	"simplehost/core/mail"
	"simplehost/core/web"
	data "simplehost/data/core"
	sites "simplehost/sites/core"
)

// Config is everything the service reads at start. The URLs of outside
// services point at the real ones in production and at fakes in tests.
type Config struct {
	Addr        string // where to listen (127.0.0.1:8095)
	DatabaseURL string
	DataDir     string // /srv/simple-host-next
	AppDomain   string // the service's own host, and the older address family
	SiteDomain  string // the flat address family for new accounts
	PasscodeKey string // PASSCODE_ENC_KEY, base64 of 32 bytes
	MailKey     string // Resend
	MailFrom    string

	MailURL, TurnstileURL, DNSURL                string
	TurnstileSiteKey, TurnstileSecret            string // the platform's one spam check key pair
	GoogleClientID, GoogleClientSecret           string
	GoogleAuthURL, GoogleTokenURL, GoogleUserURL string
	ACMEDirectories                              []string // in order: Let's Encrypt, Google, ZeroSSL
	// ACMEBindings is, per directory, "" or "<key id>:<base64url HMAC key>":
	// the account binding Google and ZeroSSL ask for.
	ACMEBindings     []string
	LoopbackOutbound bool // tests only: let the guarded client reach the fakes

	// A company's own sign-in, one more way to prove an email (off when
	// CompanyClientID is empty). The same three URLs as Google.
	CompanyClientID, CompanyClientSecret            string
	CompanyAuthURL, CompanyTokenURL, CompanyUserURL string

	// VAPIDKey is the browser push signing key: base64url of a P-256
	// private key's 32 bytes (push is off when empty).
	VAPIDKey string
	// AgentCat receives every AI tool call (off when AgentCatProject is empty).
	AgentCatURL, AgentCatProject string
	// SelfHosted: an install someone runs for themselves; its operator may
	// make accounts.
	SelfHosted bool
	// CountryFile is the IP-to-country file (MaxMind .mmdb format, such as
	// DB-IP's) for the admin page's world map; "" means no countries.
	CountryFile string
	// Cloudflare in front of the site addresses: purges after a change
	// (off when either is empty).
	CFZoneID, CFAPIToken, CFAPIURL string
}

// FromEnv reads the config from the environment, with production defaults.
func FromEnv() Config {
	env := func(name, def string) string {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return v
		}
		return def
	}
	return Config{
		Addr:               env("SH_ADDR", "127.0.0.1:8095"),
		DatabaseURL:        env("DATABASE_URL", ""),
		DataDir:            env("SH_DATA_DIR", "/srv/simple-host-next"),
		AppDomain:          env("SH_APP_DOMAIN", "simple-host.app"),
		SiteDomain:         env("SH_SITE_DOMAIN", "simple-host.site"),
		PasscodeKey:        env("PASSCODE_ENC_KEY", ""),
		MailKey:            env("RESEND_API_KEY", ""),
		MailFrom:           env("MAIL_FROM", "Simple Host <hello@simple-host.app>"),
		MailURL:            env("SH_MAIL_URL", "https://api.resend.com/emails"),
		TurnstileURL:       "https://challenges.cloudflare.com/turnstile/v0/siteverify",
		DNSURL:             "https://dns.google/resolve",
		TurnstileSiteKey:   env("TURNSTILE_SITE_KEY", ""),
		TurnstileSecret:    env("TURNSTILE_SECRET", ""),
		GoogleClientID:     env("GOOGLE_CLIENT_ID", ""),
		GoogleClientSecret: env("GOOGLE_CLIENT_SECRET", ""),
		GoogleAuthURL:      "https://accounts.google.com/o/oauth2/v2/auth",
		GoogleTokenURL:     "https://oauth2.googleapis.com/token",
		GoogleUserURL:      "https://openidconnect.googleapis.com/v1/userinfo",
		ACMEDirectories: []string{"https://acme-v02.api.letsencrypt.org/directory",
			"https://dv.acme-v02.api.pki.goog/directory", "https://acme.zerossl.com/v2/DV90"},
		ACMEBindings:    []string{"", env("ACME_EAB_GOOGLE", ""), env("ACME_EAB_ZEROSSL", "")},
		CompanyClientID: env("COMPANY_CLIENT_ID", ""), CompanyClientSecret: env("COMPANY_CLIENT_SECRET", ""),
		CompanyAuthURL: env("COMPANY_AUTH_URL", ""), CompanyTokenURL: env("COMPANY_TOKEN_URL", ""), CompanyUserURL: env("COMPANY_USER_URL", ""),
		VAPIDKey:    env("SH_VAPID_KEY", ""),
		AgentCatURL: env("SH_AGENTCAT_URL", "https://api.agentcat.com/v1/public/events/"), AgentCatProject: env("AGENTCAT_PROJECT_ID", ""),
		SelfHosted:  env("SH_SELF_HOSTED", "") == "1",
		CountryFile: env("SH_COUNTRY_FILE", ""),
		CFZoneID:    env("CF_ZONE_ID", ""), CFAPIToken: env("CF_API_TOKEN", ""), CFAPIURL: env("SH_CF_API_URL", "https://api.cloudflare.com/client/v4"),
	}
}

// App is the running service. Features read what they need from it.
type App struct {
	Config Config
	DB     *sql.DB
	Routes *web.Routes
	Mail   mail.Mail
	People *access.People
	Sites  *sites.Sites
	Data   *data.Data
	// HTTP is for the service's own outbound calls (DNS, Google, ACME,
	// AgentCat). Guarded is for addresses a browser hands us (push).
	HTTP, Guarded *http.Client
}

// New opens the database, creates the tables on an empty one, and builds
// the cores. Features are mounted on a.Routes afterwards.
func New(ctx context.Context, cfg Config) (*App, error) {
	conn, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	if err := db.Apply(ctx, conn, access.Schema, sites.Schema, data.Schema, db.Schema); err != nil {
		conn.Close()
		return nil, err
	}
	a := &App{Config: cfg, DB: conn, Routes: web.NewRoutes(), People: &access.People{DB: conn},
		HTTP: &http.Client{Timeout: 15 * time.Second}, Guarded: guard.Client(10*time.Second, cfg.LoopbackOutbound)}
	a.Mail = mail.Mail{URL: cfg.MailURL, Key: cfg.MailKey, From: cfg.MailFrom, Client: a.HTTP}
	if a.Sites, err = sites.New(conn, a.People, cfg.DataDir, cfg.AppDomain, cfg.SiteDomain, cfg.PasscodeKey); err != nil {
		conn.Close()
		return nil, err
	}
	if cfg.CFZoneID != "" && cfg.CFAPIToken != "" {
		a.Sites.Cloudflare = &sites.Cloudflare{URL: cfg.CFAPIURL, Zone: cfg.CFZoneID, Token: cfg.CFAPIToken, Client: a.Guarded}
	}
	a.Data = &data.Data{DB: conn, Dir: cfg.DataDir, TurnstileURL: cfg.TurnstileURL, TurnstileSecret: cfg.TurnstileSecret, Client: a.HTTP}
	a.Routes.Platform.Handle("GET /healthz", web.Func(func(w http.ResponseWriter, r *http.Request) error {
		if err := conn.PingContext(r.Context()); err != nil {
			return err
		}
		web.JSON(w, http.StatusOK, map[string]bool{"ok": true})
		return nil
	}))
	return a, nil
}

// ServeHTTP finds what the host name is (the platform, a site or a public
// page), who the caller is, counts the request against the caller's limit,
// and hands it to that host's routes.
func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	mux, siteID := a.Routes.Platform, ""
	if web.Host(r) == a.Config.AppDomain {
		// The platform's own answers: never sniffed, never framed (the spam
		// check frame says otherwise for itself), no full address sent on.
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
	} else {
		k := &keeper{ResponseWriter: w, r: r}
		w = k
		p, err := a.Sites.Lookup(r.Context(), web.Host(r))
		k.place = p
		if err != nil {
			web.Fail(w, r, err)
			return
		}
		if p.Redirect != "" && web.HTTPS(r) && pageView(r) {
			// 302, as the live service: a browser keeps no 301, so the site's own
			// address works again at once when its domain or short name goes.
			http.Redirect(w, r, "https://"+p.Redirect+r.URL.RequestURI(), http.StatusFound)
			return
		}
		r = sites.WithPlace(r, p)
		mux = a.Routes.Page
		if p.Site != nil {
			mux, siteID = a.Routes.Site, p.Site.ID
		}
	}
	c, err := a.People.Caller(r, siteID)
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	r = web.WithCaller(r, c)
	if mux == a.Routes.Platform || !pageView(r) { // a site's pages and files are not counted
		if err := limits.Allow(r, limits.Requests, ""); err != nil {
			web.Fail(w, r, err)
			return
		}
	}
	if mux != a.Routes.Page && strings.HasPrefix(r.URL.Path, "/v1/") && !v1Route(mux, r) {
		web.Fail(w, r, a.changed(mux, r))
	} else {
		mux.ServeHTTP(w, r)
	}
	if k, ok := w.(*keeper); ok && !k.done {
		k.WriteHeader(http.StatusOK) // an empty answer gets the rule too
	}
}

// changed is the answer for a /v1/ address no route takes: most likely a
// caller of the service before it was rebuilt. 405 when the path has a
// route for another method.
func (a *App) changed(mux *http.ServeMux, r *http.Request) *web.Refusal {
	status := http.StatusNotFound
	for _, m := range []string{"GET", "POST", "PUT", "PATCH", "DELETE"} {
		other := r.Clone(r.Context())
		other.Method = m
		if m != r.Method && v1Route(mux, other) {
			status = http.StatusMethodNotAllowed
			break
		}
	}
	d := "https://" + a.Config.AppDomain
	routes := "Current routes: " + d + "/llms.txt."
	ai := "Using an AI app? Reconnect Simple Host, or start a new chat so it lists the tools again."
	skill := "Using the installed skill? Get the new one at " + d + "/skills.zip."
	fix := []string{routes, ai, skill}
	seg := strings.Split(strings.Trim(r.URL.Path, "/"), "/") // v1, sites, {s}, ...
	switch {
	case len(seg) >= 4 && seg[1] == "sites" && seg[3] == "files" && (r.Method == "POST" || r.Method == "PUT"):
		fix = append([]string{"Publish with PUT /v1/sites/" + seg[2] + " and the same JSON body; send ?keep_other_files=true to keep files you do not send."}, fix...)
	case len(seg) >= 2 && seg[1] == "skills":
		fix = []string{skill, routes, ai}
	case len(seg) == 5 && seg[1] == "sites" && seg[3] == "storage" && seg[4] == "people":
		fix = append([]string{"The people list was removed."}, fix...)
	}
	ref := web.Refuse(status, "changed", strings.Join(fix, " "))
	ref.Message = "This address is not part of Simple Host any more. The service was rebuilt on 25 November 2026."
	return ref
}

// v1Route: one of mux's /v1/ routes takes r (not a catch-all such as GET /).
func v1Route(mux *http.ServeMux, r *http.Request) bool {
	_, pattern := mux.Handler(r)
	_, path, ok := strings.Cut(pattern, " ")
	if !ok {
		path = pattern
	}
	return strings.HasPrefix(path, "/v1/")
}

// ServeSite answers r with a site's routes, exactly as on its own address
// (a public page whose owner chose a home site).
func (a *App) ServeSite(w http.ResponseWriter, r *http.Request, site *sites.Site) error {
	c, err := a.People.Caller(r, site.ID)
	if err != nil {
		return err
	}
	p := sites.Place{Site: site}
	if k, ok := w.(*keeper); ok {
		k.place = p
	}
	a.Routes.Site.ServeHTTP(w, web.WithCaller(sites.WithPlace(r, p), c))
	return nil
}

// keeper applies the one cache rule to every answer from a site address or
// public page as its status is written, whatever the route set.
type keeper struct {
	http.ResponseWriter
	r     *http.Request
	place sites.Place
	done  bool
}

func (k *keeper) WriteHeader(status int) {
	if !k.done {
		k.done = true
		cacheRule(k.Header(), k.r, k.place, status)
	}
	k.ResponseWriter.WriteHeader(status)
}

func (k *keeper) Write(b []byte) (int, error) {
	if !k.done {
		k.WriteHeader(http.StatusOK)
	}
	return k.ResponseWriter.Write(b)
}

func (k *keeper) Unwrap() http.ResponseWriter { return k.ResponseWriter }

// cacheRule: a page or file of an open site, or a public page, is kept a
// minute by browsers and an hour at the edge (purged on every change),
// tagged by the site's or owner's id so every address shares one tag. Every
// other answer (a gate, a locked, offline or taken-down site, a redirect to
// another address, /v1/ and /_sh/, an error, one that sets a cookie) is
// never kept, except the page helper /_sh/page.js (5 minutes, an hour at the edge, no tag).
func cacheRule(h http.Header, r *http.Request, p sites.Place, status int) {
	if r.URL.Path == "/_sh/page.js" && status == http.StatusOK { // the one exception: the same helper for every visitor and site
		h.Set("Cache-Control", "public, max-age=300")
		h.Set("Cloudflare-CDN-Cache-Control", "public, max-age=3600")
		h.Del("Cache-Tag")
		return
	}
	tag := ""
	s, o := p.Site, p.Owner
	switch {
	case !pageView(r) || p.Redirect != "" || h.Get("Set-Cookie") != "" || status >= 400 && status != http.StatusNotFound:
	case s != nil && s.Access == "open" && !s.Offline && !s.TakenDown && !s.OwnerSuspended:
		tag = "site:" + s.ID
	case s == nil && o != nil && !o.Suspended:
		tag = "owner:" + o.ID
	}
	if tag == "" {
		h.Set("Cache-Control", "private, no-store")
		h.Del("Cloudflare-CDN-Cache-Control")
		h.Del("Cache-Tag")
		return
	}
	h.Set("Cache-Control", "public, max-age=60")
	h.Set("Cloudflare-CDN-Cache-Control", "public, max-age=3600")
	h.Set("Cache-Tag", tag)
}

// pageView is a visit to a page or file of a site or public page: a read
// outside the service's own paths (/v1/ data and /_sh/ sign-in and gates).
func pageView(r *http.Request) bool {
	return (r.Method == http.MethodGet || r.Method == http.MethodHead) &&
		!strings.HasPrefix(r.URL.Path, "/v1/") && !strings.HasPrefix(r.URL.Path, "/_sh/")
}

// Close releases the database.
func (a *App) Close() error { return a.DB.Close() }
