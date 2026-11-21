package sitestest

import (
	"context"
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"

	accountapp "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/auth"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/config"
	appstorage "github.com/vsriram/simple-host/host/data"
	"github.com/vsriram/simple-host/host/data/notify"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/email"
	"github.com/vsriram/simple-host/host/httpx"
	operations "github.com/vsriram/simple-host/host/ops"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sites/idempotency"
	"github.com/vsriram/simple-host/host/sites/siteshttp"
	"github.com/vsriram/simple-host/host/storage"
	"github.com/vsriram/simple-host/host/web"
)

// App is Sites' routes alone (host/sites/siteshttp: deploys, versions,
// addresses, domains, exports, the host routing in front of hosted pages) on
// a disposable database. Access's visitor gate is replaced by gate (below),
// which applies the module's own visit rule (accounts.DecideVisit): a
// suspended, offline or protected site is never served, and the answer is
// a bare status (410, 503, 404, 403) instead of Access's pages. Unlocking a
// protected site is accesstest's; the whole composed router is servertest's.
type App struct {
	*rt.Server
	Database *sql.DB
	Disk     *storage.DiskStorage
	Hosts    *sites.Hosts
	Sites    *siteshttp.SiteHandler
	Mux      *http.ServeMux
	Ed       *edition.Edition
	Admin    string // the admin key
	CertDir  string // the site-host certificate hand-off (requests/, ready/)
}

// NewApp builds Sites' routes for ed (the default edition when none).
func NewApp(t *testing.T, eds ...*edition.Edition) *App {
	t.Helper()
	return NewAppMailer(t, email.NewResendSender("", "test@example.com"), eds...)
}

// NewAppMailer is NewApp with the given mailer.
func NewAppMailer(t *testing.T, mailer email.Sender, eds ...*edition.Edition) *App {
	t.Helper()
	ed := edition.Default(config.DefaultLimits())
	if len(eds) > 0 && eds[0] != nil {
		ed = eds[0]
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
	a := &App{Database: database, Disk: disk, Hosts: hosts, Ed: ed, Mux: http.NewServeMux(), Admin: adminKey, CertDir: certDir}
	// Built as handler.NewSiteHandlers builds Sites, without Access and Data.
	h := &siteshttp.SiteHandler{Ed: ed, Limits: lim, Mailer: mailer, Database: database, Disk: disk,
		SiteDomain: rt.SiteDomain, ContentHost: rt.ContentHost, CNAMETarget: "cname." + rt.SiteDomain, Hosts: hosts,
		UploadLimiter:          httpx.NewRateLimiter(100000, 0),
		DomainCheckLimiter:     httpx.NewRateLimiterFor(lim.RateDomainCheck.Burst, lim.RateDomainCheck.Every),
		DomainCheckUserLimiter: httpx.NewRateLimiterFor(lim.RateDomainCheckUser.Burst, lim.RateDomainCheckUser.Every),
		ExportKey:              siteshttp.NewExportKey(), Network: operations.NewNetworkUsage(database, ed), Reporting: operations.NewReporting(database),
		ShowcaseLimiter: httpx.NewRateLimiter(60, 30)}
	h.Assets = web.NewAssetPipeline(ed, "")
	h.CreateIdempotency = idempotency.New(lim)
	sqlite := appstorage.NewSQLiteGate(lim.StorageSQLConcurrency, lim.StorageAcquireWait)
	h.SqliteSites = appstorage.NewSiteSlots(lim.StorageSiteInflight, sqlite, lim.StorageAcquireWait)
	h.Notify = notify.NewEngine(database, ed, lim, rt.SiteDomain)
	h.Gate = a.gate()
	a.Sites = h

	authMW := auth.Middleware(adminKey, adminID, accountapp.New(database), lim.KeyIdleExpiry)
	siteOps := httpx.NewRateLimiter(100000, 0)
	exports := httpx.NewRateLimiter(100000, 0)
	h.RegisterRoutes(a.Mux, authMW, web.NoticeMiddleware("1.0.0"), siteOps, exports, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent) // Access's passcode removal
	})
	// Access's scope gate (deploy-only keys, owner=<team or editor>) wraps the
	// mux as in production: it is host/access/auth, not Access's routes.
	app := web.SecurityHeaders(web.CORS(auth.ScopeGate(accountapp.New(database), a.Mux, lim.KeyIdleExpiry, nil)))
	// The same chain as cmd/simple-host wires in front of the app.
	a.Server = rt.NewServer(t, h.FamilyHosts(app, h.SiteBaseHosts(h.BoundSubdomains(app, h.SiteHosts(app, h.PersonHosts(app, h.LegacyHostRedirect(app)))))))
	return a
}

// gate stands in for Access's SiteGate (see App).
func (a *App) gate() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s, ok := sites.ServingOf(r)
		if !ok || s.UserID == "" {
			http.NotFound(w, r)
			return
		}
		if !s.FrameOnly {
			present := func(name string) bool { _, err := s.Root.Lstat(name); return err == nil }
			v := accountapp.Visit{Suspended: present("suspended"), Offline: present("offline"), Protected: present("passcode"), Bound: true,
				SharedHost: a.Hosts.IsSharedHost(httpx.HostName(r))}
			if v.Protected && !v.Suspended && !v.Offline {
				v.Row, v.RowErr = sitesrepo.GetSitePasscodeByName(r.Context(), a.Database, s.UserID, s.Name)
			}
			switch accountapp.DecideVisit(v) {
			case accountapp.VisitServe:
			case accountapp.VisitTakedown:
				w.WriteHeader(http.StatusGone)
				return
			case accountapp.VisitOffline:
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			case accountapp.VisitNotFound:
				http.NotFound(w, r)
				return
			default:
				w.Header().Set("Cache-Control", "private, no-store")
				w.WriteHeader(http.StatusForbidden)
				return
			}
		}
		s.Then.ServeHTTP(w, r)
	})
}

// NewPerson is an account with a handle and a key.
func (a *App) NewPerson(t *testing.T, label string) rt.Person {
	t.Helper()
	return rt.NewPerson(t, a.Database, label)
}

// Deploy publishes p's site name through the deploy route (two small pages)
// and gives p's handle its site-host certificate; it returns the site's id.
func (a *App) Deploy(t *testing.T, p rt.Person, name string) string {
	t.Helper()
	r := a.At(t, "POST", rt.SiteDomain, "/v1/sites/"+name+"/files",
		map[string]any{"files": map[string]string{"index.html": "<h1>" + name + "</h1>", "sub/index.html": "sub"}}, p.Header())
	if r.Status != http.StatusCreated {
		t.Fatalf("deploy %s: %d %s", name, r.Status, r.Body)
	}
	a.MarkReady(t, p.Handle)
	return a.SiteID(t, p, name)
}

// MarkReady gives handle its site-host certificate.
func (a *App) MarkReady(t *testing.T, handle string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(a.CertDir, "ready", handle), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

// SiteID is the id of p's site name.
func (a *App) SiteID(t *testing.T, p rt.Person, name string) string {
	t.Helper()
	var id string
	if err := a.Database.QueryRow(`SELECT id FROM sites WHERE user_id = $1 AND name = $2 AND deleted_at IS NULL`, p.ID, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
