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
	Addr        string // where to listen (127.0.0.1:8091)
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
		Addr:               env("SH_ADDR", "127.0.0.1:8091"),
		DatabaseURL:        env("DATABASE_URL", ""),
		DataDir:            env("SH_DATA_DIR", "/srv/simple-host-next"),
		AppDomain:          env("SH_APP_DOMAIN", "simple-host.app"),
		SiteDomain:         env("SH_SITE_DOMAIN", "simple-host.site"),
		PasscodeKey:        env("PASSCODE_ENC_KEY", ""),
		MailKey:            env("RESEND_API_KEY", ""),
		MailFrom:           env("MAIL_FROM", "Simple Host <hello@simple-host.app>"),
		MailURL:            "https://api.resend.com/emails",
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
	a.Data = &data.Data{DB: conn, Dir: cfg.DataDir, TurnstileURL: cfg.TurnstileURL, TurnstileSecret: cfg.TurnstileSecret, Client: a.HTTP}
	a.Routes.Platform.Handle("GET /health", web.Func(func(w http.ResponseWriter, r *http.Request) error {
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
	if web.Host(r) != a.Config.AppDomain {
		p, err := a.Sites.Lookup(r.Context(), web.Host(r))
		if err != nil {
			web.Fail(w, r, err)
			return
		}
		if p.Redirect != "" && web.HTTPS(r) {
			http.Redirect(w, r, "https://"+p.Redirect+r.URL.RequestURI(), http.StatusMovedPermanently)
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
	if err := limits.Allow(r, limits.Requests, ""); err != nil {
		web.Fail(w, r, err)
		return
	}
	mux.ServeHTTP(w, r)
}

// Close releases the database.
func (a *App) Close() error { return a.DB.Close() }
