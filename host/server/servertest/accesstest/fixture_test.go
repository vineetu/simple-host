package accesstest

import (
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"github.com/vsriram/simple-host/host/access/featuretest"

	accountapp "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/accesshttp"
	appstorage "github.com/vsriram/simple-host/host/data"
	"github.com/vsriram/simple-host/host/data/notify"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/email"
	"github.com/vsriram/simple-host/host/httpx"
	operations "github.com/vsriram/simple-host/host/ops"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sites/idempotency"
)

// App is Access's routes, every feature composed by accesshttp, on the
// shared feature-test app (featuretest.Base): the journeys that cross
// features (sign in, then use another feature). Each feature's own route
// tests are in its package. Stand-ins, as featuretest's, plus Access's gates:
//   - a site's pages are served through Access's SiteGate;
//   - GET /v1/sites/{sitename}/storage/probe runs StorageCaller and answers
//     the Call it resolved (CallJSON); GET .../gate-probe runs PasscodeGate.
type App struct {
	*featuretest.Base
	Access *accesshttp.AccessHandler
	Users  *accesshttp.UserHandler
	Conn   *accesshttp.ConnectorHandler
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
	b := featuretest.NewBase(t, mailer, eds...)
	a := &App{Base: b}
	database, ed, lim, authMW, notice := b.Database, b.Ed, b.Limits, b.Auth, b.Notice
	users := accesshttp.NewUserHandler(database, accountapp.New(database), mailer, b.URL, ed)
	users.Register(a.Mux, authMW, notice)
	a.Users = users
	b.EmailLimiter = users.EmailLimiter

	// Built as handler.NewSiteHandlers builds Access, without Sites and Data.
	uploadLocks := &sync.Map{}
	access := &accesshttp.AccessHandler{Hosts: b.Hosts, Ed: ed, Limits: lim, Database: database, Disk: b.Disk, Mailer: mailer,
		EmailLimiter: users.EmailLimiter, VisitorAuthLimiter: httpx.NewRateLimiterFor(lim.RateVisitorAuth.Burst, lim.RateVisitorAuth.Every),
		SiteDomain: rt.SiteDomain, ContentHost: rt.ContentHost, ExportKey: []byte("accesstest-export-key-0123456789"), UploadLocks: uploadLocks,
		AdminAPIKey: b.Admin, AdminUserID: b.AdminID, HTTPCaches: &httpx.Caches{}, CreateIdempotency: idempotency.New(lim)}
	access.Passcode.Limits = lim
	access.Notify = notify.NewEngine(database, ed, lim, rt.SiteDomain)
	access.SiteFiles = sites.New(database, b.Disk, nil, 0, sites.Deps{Locks: uploadLocks, Hosts: b.Hosts, DeletedRetention: lim.DeletedRetention})
	access.SiteArchive = &operations.SiteArchive{Database: database, Disk: b.Disk, Locks: uploadLocks, Slots: appstorage.NewSiteSlots(lim.StorageSiteInflight, appstorage.NewSQLiteGate(lim.StorageSQLConcurrency, lim.StorageAcquireWait), lim.StorageAcquireWait)}
	access.Reporting = operations.NewReporting(database)
	a.Access = access
	siteOps := httpx.NewRateLimiterFor(lim.RateSiteOps.Burst, lim.RateSiteOps.Every)
	exports := httpx.NewRateLimiterFor(lim.RateExport.Burst, lim.RateExport.Every)
	accesshttp.RegisterEditors(a.Mux, access, authMW, notice)
	accesshttp.RegisterAccountRoutes(a.Mux, access, authMW, siteOps, exports)
	access.RegisterGateRoutes(a.Mux, authMW, notice, siteOps)
	users.SetSites(access)
	mountGateProbes(a.Mux, access)
	a.Conn = accesshttp.NewConnectorHandler(database, b.URL, b.Admin, rt.SiteDomain, rt.ContentHost, "1.0.0", b.Gated, false, access, users.SignInAlerts(), ed)
	a.Conn.Register(a.Mux, authMW)
	b.Bearer = a.Conn.BearerAuth
	b.SiteGate = access.SiteGate()
	return a
}

// mountGateProbes mounts the storage probe and the gate probe.
func mountGateProbes(mux *http.ServeMux, access *accesshttp.AccessHandler) {
	probe := access.StorageCaller(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := appstorage.CallFrom(r.Context())
		httpx.WriteJSON(w, http.StatusOK, CallJSON{SiteID: c.SiteID, OwnerID: c.OwnerID, SiteName: c.SiteName, AccountID: c.AccountID,
			Owner: c.Owner, VisitorAI: c.VisitorAI, VisitorID: c.VisitorID, LinkScope: c.LinkScope,
			PasscodeOK: c.PasscodeOK, ViewersOK: c.ViewersOK, OnOwnAddress: c.OnOwnAddress, CSRFOK: c.CSRFOK})
	}))
	mux.Handle("GET /v1/sites/{sitename}/storage/probe", probe)
	// The same probe on a data route editors may use (owner= by an editor
	// passes the scope gate only on editor routes).
	mux.Handle("GET /v1/sites/{sitename}/storage/usage", probe)
	mux.Handle("GET /v1/sites/{sitename}/gate-probe", access.PasscodeGate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))
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
