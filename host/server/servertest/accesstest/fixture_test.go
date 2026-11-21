package accesstest

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	accountapp "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/accesshttp"
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
	"github.com/vsriram/simple-host/host/storage"
	"github.com/vsriram/simple-host/host/web"
)

// App is Access's routes alone (host/access/accesshttp: accounts, keys,
// connector, passcodes, named viewers, visitor sign-in) on a disposable
// database, with sites made through the modules. Two stand-ins take the
// other modules' places, so Access's own gates are what these tests prove:
//   - a site's pages: a request on a site's own host (not under /v1/) is
//     served from the site's live files through Access's SiteGate, as the
//     site handler does;
//   - a data route: GET /v1/sites/{sitename}/storage/probe runs Access's
//     StorageCaller and answers the Call it resolved as JSON (CallJSON), and
//     GET /v1/sites/{sitename}/gate-probe runs PasscodeGate and answers 200;
//   - Sites' archive deploy (PUT /v1/sites/{sitename}) and photo route, the
//     targets of upload and photo links: they answer whom the key acts as.
//
// The whole composed router is servertest's, for journeys about wiring.
type App struct {
	*rt.Server
	Database *sql.DB
	Disk     *storage.DiskStorage
	Hosts    *sites.Hosts
	Access   *accesshttp.AccessHandler
	Users    *accesshttp.UserHandler
	Conn     *accesshttp.ConnectorHandler
	Mux      *http.ServeMux
	Ed       *edition.Edition
	Admin    string // the admin key
	certDir  string
}

// CallJSON is what the storage probe answers: the Call Access resolved.
type CallJSON struct {
	SiteID, OwnerID, SiteName, AccountID string
	Owner, VisitorAI                     bool
	VisitorID, LinkScope                 string
	PasscodeOK, ViewersOK, OnOwnAddress  bool
	CSRFOK                               bool
}

// NewApp builds Access's routes for ed (the default edition when none).
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
	a := &App{Database: database, Disk: disk, Hosts: hosts, Ed: ed, Mux: http.NewServeMux(), Admin: adminKey, certDir: certDir}
	var root http.Handler
	a.Server = rt.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { root.ServeHTTP(w, r) }))
	base := a.Srv.URL

	authMW := auth.Middleware(adminKey, adminID, accountapp.New(database), lim.KeyIdleExpiry)
	notice := web.NoticeMiddleware("1.0.0")
	users := accesshttp.NewUserHandler(database, accountapp.New(database), mailer, base, ed)
	users.Register(a.Mux, authMW, notice)
	a.Users = users

	// Built as handler.NewSiteHandlers builds Access, without Sites and Data.
	uploadLocks := &sync.Map{}
	access := &accesshttp.AccessHandler{Hosts: hosts, Ed: ed, Limits: lim, Database: database, Disk: disk, Mailer: mailer,
		EmailLimiter: users.EmailLimiter, VisitorAuthLimiter: httpx.NewRateLimiterFor(lim.RateVisitorAuth.Burst, lim.RateVisitorAuth.Every),
		SiteDomain: rt.SiteDomain, ContentHost: rt.ContentHost, ExportKey: []byte("accesstest-export-key-0123456789"), UploadLocks: uploadLocks,
		AdminAPIKey: adminKey, AdminUserID: adminID, HTTPCaches: &httpx.Caches{}, CreateIdempotency: idempotency.New(lim)}
	access.Passcode.Limits = lim
	access.Notify = notify.NewEngine(database, ed, lim, rt.SiteDomain)
	access.SiteFiles = sites.New(database, disk, nil, 0, sites.Deps{
		LockSite:         func(userID, name string) func() { return sites.LockSiteName(uploadLocks, userID, name) },
		DeletedRetention: lim.DeletedRetention,
		HasLiveFamilies:  hosts.HasLiveFamilies, SyncRedirect: hosts.SyncDomainRedirect,
		RequestCert: func(string) {}, CancelDomainCert: hosts.CancelDomainCert,
		// Data's saved-data resources go into an export; none are saved here.
		ExportResources: func(context.Context, string) ([]map[string]any, error) { return nil, nil },
	})
	access.Reporting = operations.NewReporting(database)
	a.Access = access
	siteOps := httpx.NewRateLimiterFor(lim.RateSiteOps.Burst, lim.RateSiteOps.Every)
	exports := httpx.NewRateLimiterFor(lim.RateExport.Burst, lim.RateExport.Every)
	access.RegisterEditors(a.Mux, authMW, notice)
	access.RegisterAccountRoutes(a.Mux, authMW, siteOps, exports)
	access.RegisterGateRoutes(a.Mux, authMW, notice, siteOps)
	users.SetSites(access)
	users.SetPublicPage(func(handle string) string { return "https://" + handle + "." + rt.SiteDomain + "/" })

	probe := access.StorageCaller(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := appstorage.CallFrom(r.Context())
		httpx.WriteJSON(w, http.StatusOK, CallJSON{SiteID: c.SiteID, OwnerID: c.OwnerID, SiteName: c.SiteName, AccountID: c.AccountID,
			Owner: c.Owner, VisitorAI: c.VisitorAI, VisitorID: c.VisitorID, LinkScope: c.LinkScope,
			PasscodeOK: c.PasscodeOK, ViewersOK: c.ViewersOK, OnOwnAddress: c.OnOwnAddress, CSRFOK: c.CSRFOK})
	}))
	a.Mux.Handle("GET /v1/sites/{sitename}/storage/probe", probe)
	// The same probe on a data route editors may use (owner= by an editor
	// passes the scope gate only on editor routes).
	a.Mux.Handle("GET /v1/sites/{sitename}/storage/usage", probe)
	// Sites' archive deploy and photo routes, which upload and photo links
	// forward to: they answer who the forwarded key acts as (Sites' own
	// handling of the upload is sitestest's).
	sitesUpload := func(status int) http.Handler {
		return authMW(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			httpx.WriteJSON(w, status, map[string]string{"user": auth.GetUser(r.Context()).ID, "site": r.PathValue("sitename")})
		}))
	}
	a.Mux.Handle("PUT /v1/sites/{sitename}", sitesUpload(http.StatusCreated))
	a.Mux.Handle("POST /v1/sites/{sitename}/photos", sitesUpload(http.StatusOK))
	a.Mux.Handle("GET /v1/sites/{sitename}/gate-probe", access.PasscodeGate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	gated := auth.ScopeGate(accountapp.New(database), a.Mux, lim.KeyIdleExpiry, nil)
	a.Conn = accesshttp.NewConnectorHandler(database, base, adminKey, rt.SiteDomain, rt.ContentHost, "1.0.0", gated, false, ed)
	a.Conn.Register(a.Mux, authMW)
	a.Conn.SetSignInAlerts(users.SignInAlerts())
	app := web.SecurityHeaders(web.CORS(a.Conn.BearerAuth(gated)))
	gate := access.SiteGate()
	root = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/") {
			if s, _, ok, err := hosts.SiteHostSite(r.Context(), httpx.HostName(r)); err == nil && ok {
				a.servePage(w, r, gate, s.UserID, s.Name)
				return
			}
		}
		app.ServeHTTP(w, r)
	})
	return a
}

// servePage stands in for the site handler: the site's folder goes on the
// request (sites.Serving) and Access's gate decides; a request it lets in
// gets the live file.
func (a *App) servePage(w http.ResponseWriter, r *http.Request, gate http.Handler, userID, name string) {
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
	gate.ServeHTTP(w, sites.WithServing(r, sites.Serving{UserID: userID, Name: name, Rel: rel, Root: root, Then: then}))
}

// NewPerson is an account with a handle and a key.
func (a *App) NewPerson(t *testing.T, label string) rt.Person {
	t.Helper()
	return rt.NewPerson(t, a.Database, label)
}

// Deploy publishes p's site name and gives p's handle its site-host
// certificate, so <site>.<handle>.simple-host.test answers; it returns the
// site's id.
func (a *App) Deploy(t *testing.T, p rt.Person, name string) string {
	t.Helper()
	id := rt.Deploy(t, a.Database, a.Disk, p, name, nil)
	markReady(t, a.certDir, p.Handle)
	return id
}

func markReady(t *testing.T, certDir, handle string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(certDir, "ready", handle), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Session signs p in as a visitor of siteID on host; rt.Browser sends the cookie.
func (a *App) Session(t *testing.T, p rt.Person, siteID, host string) string {
	t.Helper()
	return rt.Session(t, a.Database, p.ID, siteID, host)
}

// Probe asks the storage probe which Call Access resolves for this request.
func (a *App) Probe(t *testing.T, host, site string, headers map[string]string) (rt.Resp, CallJSON) {
	t.Helper()
	r := a.At(t, "GET", host, "/v1/sites/"+site+"/storage/probe", nil, headers)
	var c CallJSON
	if r.Status == http.StatusOK {
		if err := json.Unmarshal(r.Body, &c); err != nil {
			t.Fatal(err)
		}
	}
	return r, c
}

// TestPasscodeKey is the passcode encryption key of EnablePasscodes (tests only).
const TestPasscodeKey = "q83vEjRWeJq83vEjRWeJq83vEjRWeJq83vEjRWeJq80="

// EnablePasscodes turns site passcodes on (a key, SITE_PASSCODES, a
// six-character minimum), as an install with PASSCODE_ENC_KEY has them.
func (a *App) EnablePasscodes(t *testing.T) {
	t.Helper()
	if err := a.Access.SetPasscodeKey(TestPasscodeKey); err != nil {
		t.Fatal(err)
	}
	a.Access.Limits.SitePasscodes, a.Access.Limits.PasscodeMinLength = true, 6
	a.Access.Passcode.Limits = a.Access.Limits
}
