// Package featuretest is what Access's feature tests share: a test server on
// a disposable database with the key middleware, the scope gate and the core,
// on which each feature's test mounts its own Register and nothing else, plus
// people, sites, a capturing mailbox and the HTTP steps of the flows. It
// imports no feature, so a change to one feature reruns only that feature's
// tests. Sites' upload and photo routes are stand-ins that answer whom the
// key acts as; GET /v1/sites/{sitename}/whoami is the plainest route that
// resolves owner= for any non-editor route. Tests only.
package featuretest

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	appstorage "github.com/vsriram/simple-host/host/data"
	"github.com/vsriram/simple-host/host/ops"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/auth"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/email"
	"github.com/vsriram/simple-host/host/httpx"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/storage"
	"github.com/vsriram/simple-host/host/web"
)

// Base is the shared part of a feature's test app. A feature's test mounts
// its routes on Mux with Auth and Notice; SiteGate (the gate's tests) serves
// a site's own host, and Bearer (the connector's tests) wraps the app.
type Base struct {
	*rt.Server
	Database *sql.DB
	Disk     *storage.DiskStorage
	Hosts    *sites.Hosts
	Mux      *http.ServeMux
	Ed       *edition.Edition
	Limits   edition.Limits
	Mailer   email.Sender
	Admin    string // the admin key
	AdminID  string
	URL      string // the server's URL, the app's public base

	Auth, Notice func(http.Handler) http.Handler
	// Gated is Mux behind the scope gate, as the app serves it.
	Gated http.Handler
	// EmailLimiter is the sign-in email limiter the features share.
	EmailLimiter *httpx.RateLimiter

	SiteGate http.Handler
	Bearer   func(http.Handler) http.Handler
	certDir  string
	locks    sync.Map
}

// NewBase builds the shared app for ed (the default edition when none), its mail
// sent to mailer (nil: an unconfigured sender).
func NewBase(t *testing.T, mailer email.Sender, eds ...*edition.Edition) *Base {
	t.Helper()
	ed := edition.Default(config.DefaultLimits())
	if len(eds) > 0 && eds[0] != nil {
		ed = eds[0]
	}
	if mailer == nil {
		mailer = email.NewResendSender("", "test@example.com")
	}
	lim := ed.Limits()
	database := rt.DB(t)
	disk := rt.Disk(t)
	certDir := t.TempDir()
	for _, d := range []string{"requests", "ready"} {
		if err := os.MkdirAll(filepath.Join(certDir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	hosts := sites.NewHosts(database, disk, ed, rt.SiteDomain, rt.ContentHost, "cname."+rt.SiteDomain, "")
	hosts.SetPersonHosts("canonical")
	hosts.SetSiteHosts("canonical", certDir)
	accountsrepo.SetPlatformDomains(rt.SiteDomain)
	t.Cleanup(func() { accountsrepo.SetPlatformDomains("") })
	adminKey, _ := auth.GenerateAPIKey()
	adminID, err := accountsrepo.EnsureAdminUser(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	a := &Base{Database: database, Disk: disk, Hosts: hosts, Mux: http.NewServeMux(), Ed: ed, Limits: lim, Mailer: mailer,
		Admin: adminKey, AdminID: adminID, certDir: certDir,
		EmailLimiter: httpx.NewRateLimiterFor(lim.RateSigninEmail.Burst, lim.RateSigninEmail.Every)}
	a.Server = rt.NewServer(t, http.HandlerFunc(a.serve))
	a.URL = a.Srv.URL
	a.Auth = auth.Middleware(adminKey, adminID, accounts.New(database), lim.KeyIdleExpiry)
	a.Notice = web.NoticeMiddleware("1.0.0")
	a.Gated = auth.ScopeGate(accounts.New(database), a.Mux, lim.KeyIdleExpiry, nil)
	actsAs := func(status int) http.Handler {
		return a.Auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			httpx.WriteJSON(w, status, map[string]string{"user": auth.GetUser(r.Context()).ID, "site": r.PathValue("sitename")})
		}))
	}
	a.Mux.Handle("PUT /v1/sites/{sitename}", actsAs(http.StatusCreated))
	a.Mux.Handle("POST /v1/sites/{sitename}/photos", actsAs(http.StatusOK))
	a.Mux.Handle("GET /v1/sites/{sitename}/whoami", actsAs(http.StatusOK))
	return a
}

// serve is the app: a site's own host (not under /v1/) through SiteGate when
// one is set, everything else through the scope gate.
func (a *Base) serve(w http.ResponseWriter, r *http.Request) {
	if a.SiteGate != nil && !strings.HasPrefix(r.URL.Path, "/v1/") {
		if s, _, ok, err := a.Hosts.SiteHostSite(r.Context(), httpx.HostName(r)); err == nil && ok {
			a.servePage(w, r, s.UserID, s.Name)
			return
		}
	}
	app := a.Gated
	if a.Bearer != nil {
		app = a.Bearer(app)
	}
	web.SecurityHeaders(web.CORS(app)).ServeHTTP(w, r)
}

// servePage stands in for the site handler: the site's folder goes on the
// request (sites.Serving) and the gate decides; a request it lets in gets the
// live file.
func (a *Base) servePage(w http.ResponseWriter, r *http.Request, userID, name string) {
	root, err := os.OpenRoot(a.Disk.SiteDir(userID, name))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer root.Close()
	rel := r.URL.Path
	then := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		file := strings.TrimPrefix(rel, "/")
		if file == "" || strings.HasSuffix(file, "/") {
			file += "index.html"
		}
		b, err := root.ReadFile(filepath.Join("current", file))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(b)
	})
	a.SiteGate.ServeHTTP(w, sites.WithServing(r, sites.Serving{UserID: userID, Name: name, Rel: rel, Root: root, Then: then}))
}

// NewPerson is an account with a handle and a key.
func (a *Base) NewPerson(t *testing.T, label string) rt.Person {
	t.Helper()
	return rt.NewPerson(t, a.Database, label)
}

// Deploy publishes p's site name and gives p's handle its site-host
// certificate, so <site>.<handle>.simple-host.test answers; it returns the
// site's id.
func (a *Base) Deploy(t *testing.T, p rt.Person, name string) string {
	t.Helper()
	id := rt.Deploy(t, a.Database, a.Disk, p, name, nil)
	MarkReadyHandle(t, a.certDir, p.Handle)
	return id
}

// MarkReadyHandle gives handle its site-host certificate under certDir.
func MarkReadyHandle(t *testing.T, certDir, handle string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(certDir, "ready", handle), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Session signs p in as a visitor of siteID on host; rt.Browser sends the cookie.
func (a *Base) Session(t *testing.T, p rt.Person, siteID, host string) string {
	t.Helper()
	return rt.Session(t, a.Database, p.ID, siteID, host)
}

// RoomyMail makes the shared email limiter roomy for tests that send many codes.
func (a *Base) RoomyMail() {
	a.EmailLimiter.Capacity, a.EmailLimiter.Rate = 1000, 1000
}

// SitesSvc is the sites workflows Access drives, as the gate builds them.
func (a *Base) SitesSvc() *sites.Service {
	return sites.New(a.Database, a.Disk, nil, 0, sites.Deps{SiteViewersMax: a.Limits.SiteViewersMax, ContentHost: rt.ContentHost, SiteDomain: rt.SiteDomain,
		Locks: &a.locks, PersonalSitesEnabled: a.Ed.Identity().PersonalSites})
}

// SiteFiles is the account's sites as the composition hands them to Access
// (export, erasure, rename).
func (a *Base) SiteFiles() *sites.Service {
	return sites.New(a.Database, a.Disk, nil, 0, sites.Deps{Locks: &a.locks, Hosts: a.Hosts, DeletedRetention: a.Limits.DeletedRetention})
}

// Cutoff is the one workflow that erases, deletes a team or suspends.
func (a *Base) Cutoff() ops.Cutoff {
	return ops.Cutoff{DB: a.Database, Sites: a.SitesSvc(), Hosts: a.Hosts, Files: a.SiteFiles()}
}

// SiteArchive writes a site's files and saved data into an account export.
func (a *Base) SiteArchive() *ops.SiteArchive {
	return &ops.SiteArchive{Database: a.Database, Disk: a.Disk, Locks: &a.locks, Slots: appstorage.NewSiteSlots(a.Limits.StorageSiteInflight,
		appstorage.NewSQLiteGate(a.Limits.StorageSQLConcurrency, a.Limits.StorageAcquireWait), a.Limits.StorageAcquireWait)}
}
