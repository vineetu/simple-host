// Package fixture is the route-test fixture of the Sites feature packages,
// built only by tests: Sites' shared route state (sitesroute.Handler, built as
// production builds it) on a disposable database, auth and notice
// middleware, Access's scope gate, a test server, and the deploy routes
// mounted, since every feature test publishes a site first. A feature test
// mounts its own routes on Mux:
//
//	a := fixture.New(t)
//	versions.Register(a.Mux, a.H, a.Auth, a.Notice)
//
// Serving at site, person and family hosts is not mounted here: those tests
// stay in host/server/servertest/sitestest.
package fixture

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

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
	"github.com/vsriram/simple-host/host/sites/deploy"
	"github.com/vsriram/simple-host/host/sites/idempotency"
	"github.com/vsriram/simple-host/host/sites/sitesroute"
	"github.com/vsriram/simple-host/host/storage"
	"github.com/vsriram/simple-host/host/web"
)

// App is one feature's routes (plus deploy) on a disposable database.
type App struct {
	*rt.Server
	Database *sql.DB
	Disk     *storage.DiskStorage
	Hosts    *sites.Hosts
	H        *sitesroute.Handler
	Mux      *http.ServeMux
	Ed       *edition.Edition
	Admin    string // the admin key
	CertDir  string // the site-host certificate hand-off (requests/, ready/)
	// Auth and Notice are the middleware a feature's Register takes;
	// SiteOps and Exports the shared per-address limiters (unlimited here).
	Auth, Notice     func(http.Handler) http.Handler
	SiteOps, Exports *httpx.RateLimiter
}

// New builds the route state for ed (the default edition when none) with
// the deploy routes mounted.
func New(t *testing.T, eds ...*edition.Edition) *App {
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
	// Built as handler.NewSiteHandlers builds Sites, without Access and Data.
	h := &sitesroute.Handler{Ed: ed, Limits: lim, Mailer: email.NewResendSender("", "test@example.com"), Database: database, Disk: disk,
		SiteDomain: rt.SiteDomain, ContentHost: rt.ContentHost, CNAMETarget: "cname." + rt.SiteDomain, Hosts: hosts,
		UploadLimiter:          httpx.NewRateLimiter(100000, 0),
		DomainCheckLimiter:     httpx.NewRateLimiterFor(lim.RateDomainCheck.Burst, lim.RateDomainCheck.Every),
		DomainCheckUserLimiter: httpx.NewRateLimiterFor(lim.RateDomainCheckUser.Burst, lim.RateDomainCheckUser.Every),
		ExportKey:              sitesroute.NewExportKey(), Network: operations.NewNetworkUsage(database, ed), Reporting: operations.NewReporting(database),
		ShowcaseLimiter: httpx.NewRateLimiter(60, 30)}
	h.Assets = web.NewAssetPipeline(ed, "")
	h.CreateIdempotency = idempotency.New(lim)
	sqlite := appstorage.NewSQLiteGate(lim.StorageSQLConcurrency, lim.StorageAcquireWait)
	h.SqliteSites = appstorage.NewSiteSlots(lim.StorageSiteInflight, sqlite, lim.StorageAcquireWait)
	h.Notify = notify.NewEngine(database, ed, lim, rt.SiteDomain)
	a := &App{Database: database, Disk: disk, Hosts: hosts, H: h, Ed: ed, Mux: http.NewServeMux(), Admin: adminKey, CertDir: certDir,
		Auth:    auth.Middleware(adminKey, adminID, accountapp.New(database), lim.KeyIdleExpiry),
		Notice:  web.NoticeMiddleware("1.0.0"),
		SiteOps: httpx.NewRateLimiter(100000, 0), Exports: httpx.NewRateLimiter(100000, 0)}
	deploy.Register(a.Mux, h, a.Auth, a.Notice)
	// Access's scope gate wraps the mux as in production.
	a.Server = rt.NewServer(t, web.SecurityHeaders(web.CORS(auth.ScopeGate(accountapp.New(database), a.Mux, lim.KeyIdleExpiry, nil))))
	return a
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
	r := a.Files(t, p, "POST", name, map[string]string{"index.html": "<h1>" + name + "</h1>", "sub/index.html": "sub"})
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

// SetLimits replaces Sites' limits snapshot for the rest of the test.
func (a *App) SetLimits(t *testing.T, m map[string]string) { a.H.Limits = Limits(t, m) }

// Files deploys or updates p's site name with files through the deploy route.
func (a *App) Files(t *testing.T, p rt.Person, method, name string, files map[string]string) rt.Resp {
	t.Helper()
	return a.At(t, method, rt.SiteDomain, "/v1/sites/"+name+"/files", map[string]any{"files": files}, p.Header())
}

// API is a request to the API host with p's key.
func (a *App) API(t *testing.T, p rt.Person, method, path string, body any) rt.Resp {
	t.Helper()
	return a.At(t, method, rt.SiteDomain, path, body, p.Header())
}

// AsAdmin is a request to the API host with the admin key.
func (a *App) AsAdmin(t *testing.T, method, path string, body any) rt.Resp {
	t.Helper()
	return a.At(t, method, rt.SiteDomain, path, body, map[string]string{"X-API-Key": a.Admin})
}

// SiteDir is p's site name's directory on disk.
func (a *App) SiteDir(p rt.Person, name string) string { return a.Disk.SiteDir(p.ID, name) }

// Uniq is prefix with a suffix no other run shares.
func Uniq(prefix string) string { return prefix + strconv.FormatInt(time.Now().UnixNano(), 36) }

// Limits loads the env settings in m into a config.Limits value.
func Limits(t *testing.T, m map[string]string) config.Limits {
	t.Helper()
	l, err := config.LoadLimits(func(k string) string { return m[k] })
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// TarGzOf is a gzipped tar holding index.html plus a file of n zero bytes:
// a few KB on the wire, n bytes unpacked.
func TarGzOf(t *testing.T, n int) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range map[string][]byte{"index.html": []byte("<h1>big</h1>"), "big.bin": make([]byte, n)} {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// TarNames are the entry names of a gzipped tar.
func TarNames(t *testing.T, b []byte) map[string]bool {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	names := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names[hdr.Name] = true
	}
	return names
}

// WithKeepVersions sets the instance retention (KEEP_VERSIONS) for the rest of the test.
func WithKeepVersions(t *testing.T, n int) {
	before := web.DefaultKeepVersions
	web.DefaultKeepVersions = n
	t.Cleanup(func() { web.DefaultKeepVersions = before })
}

// ZipOf is a zip of files.
func ZipOf(t *testing.T, files map[string][]byte) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		f, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}
