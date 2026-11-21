package datatest

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"

	accountapp "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/auth"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/config"
	appstorage "github.com/vsriram/simple-host/host/data"
	"github.com/vsriram/simple-host/host/data/datahttp"
	"github.com/vsriram/simple-host/host/data/notify"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/email"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sites/idempotency"
	"github.com/vsriram/simple-host/host/storage"
	"github.com/vsriram/simple-host/host/web"
)

// App is Data's routes alone (host/data/datahttp) on a disposable database,
// with sites and people made through the modules. Access's caller lookup is
// replaced by caller (below): these tests prove what Data decides for a
// given caller; whether Access resolves the caller right is accesstest's.
type App struct {
	*rt.Server
	Database *sql.DB
	Disk     *storage.DiskStorage
	Hosts    *sites.Hosts
	Data     *datahttp.DataHandler
	Ed       *edition.Edition
	Mux      *http.ServeMux
	Admin    string // the admin key
	certDir  string
}

// Unlocked is the header the stand-in caller reads as "this visitor passed
// the site's passcode" (Access decides that from the unlock cookie).
const Unlocked = "X-Test-Unlocked"

// NewApp builds Data's routes for ed (the default edition when none).
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
	// Built as handler.NewSiteHandlers builds Data, without Sites and Access.
	d := &datahttp.DataHandler{Hosts: hosts, Ed: ed, Limits: lim, Database: database, Disk: disk, Mailer: mailer,
		ExportKey: []byte("datatest-export-key-0123456789ab"), CreateIdempotency: idempotency.New(lim), HTTPCaches: &httpx.Caches{},
		PasscodeKeys: &accountapp.PasscodeKeys{}, UploadLocks: &sync.Map{},
		Sqlite:           datahttp.NewSQLiteGate(lim.StorageSQLConcurrency, lim.StorageAcquireWait),
		StorageIPLimiter: httpx.NewRateLimiter(100000, 0), StorageVisitorLimiter: httpx.NewRateLimiter(100000, 0),
		SpamIPLimiter:   httpx.NewRateLimiterFor(lim.RateSpamCheckedIP.Burst, lim.RateSpamCheckedIP.Every),
		SpamSiteLimiter: httpx.NewRateLimiterFor(lim.RateSpamCheckedSite.Burst, lim.RateSpamCheckedSite.Every)}
	d.SqliteSites = appstorage.NewSiteSlots(lim.StorageSiteInflight, d.Sqlite, lim.StorageAcquireWait)
	d.Notifications = notify.NewEngine(database, ed, lim, rt.SiteDomain)
	t.Cleanup(d.WaitNotifications)

	a := &App{Database: database, Disk: disk, Hosts: hosts, Data: d, Ed: ed, Mux: http.NewServeMux(), Admin: adminKey, certDir: certDir}
	authMW := auth.Middleware(adminKey, adminID, accountapp.New(database), lim.KeyIdleExpiry)
	notice := web.NoticeMiddleware("1.0.0")
	d.RegisterNotifications(a.Mux, authMW, notice)
	d.RegisterWebPush(a.Mux, a.passcodeGate, a.caller, authMW, notice)
	d.RegisterStorageRoutes(a.Mux, a.caller)
	a.Server = rt.NewServer(t, web.SecurityHeaders(web.CORS(a.Mux)))
	return a
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
	if err := os.WriteFile(filepath.Join(a.certDir, "ready", p.Handle), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return id
}

// Session signs p in as a visitor of siteID on host; Browser sends the cookie.
func (a *App) Session(t *testing.T, p rt.Person, siteID, host string) string {
	t.Helper()
	return rt.Session(t, a.Database, p.ID, siteID, host)
}

// passcodeGate stands in for Access's PasscodeGate on the push routes:
// a protected site refuses a visitor who has not passed its passcode.
func (a *App) passcodeGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodOptions && r.Header.Get("X-API-Key") == "" && r.Header.Get(Unlocked) == "" {
			if s, _, err := a.Hosts.ResolveSite(r.Context(), httpx.HostName(r), "", r.PathValue("sitename")); err == nil {
				if row, err := accountsrepo.GetSitePasscode(r.Context(), a.Database, s.ID); err == nil && row.Enc != nil {
					httpx.WriteError(w, http.StatusForbidden, "site_locked", "site locked")
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// caller stands in for Access's StorageCaller: the owner's key (or the
// admin's) names the owner's site and gives owner rights; otherwise the
// request's host names the site and the visitor cookie, when it is a
// session on that site, names the visitor. The passcode counts as passed
// with the Unlocked header; the named-viewers rule is the module's own
// (accounts.ViewerAllowed).
func (a *App) caller(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		name := r.PathValue("sitename")
		var c appstorage.Call
		var site model.Site
		if key := r.Header.Get("X-API-Key"); key != "" {
			var u model.User
			var err error
			if key == a.Admin {
				u = model.User{ID: "admin", IsAdmin: true}
				site, _, err = a.Hosts.ResolveSite(ctx, rt.SiteDomain, "", name)
			} else if u, err = accountapp.New(a.Database).GetUserByAPIKey(ctx, key, 0); err == nil {
				site, err = a.Hosts.WriterSite(ctx, name, &u)
			}
			if err != nil || (site.UserID != u.ID && !u.IsAdmin) {
				httpx.WriteError(w, http.StatusForbidden, "forbidden", "owner credential required")
				return
			}
			c.Owner, c.AccountID = true, u.ID
		} else {
			s, _, err := a.Hosts.ResolveSite(ctx, httpx.HostName(r), "", name)
			if err != nil {
				httpx.WriteError(w, http.StatusNotFound, "site_not_found", "site not found")
				return
			}
			site = s
			if ck, err := r.Cookie(rt.VisitorCookie); err == nil {
				id, _ := hex.DecodeString(ck.Value)
				if sess, err := accountsrepo.GetVisitorSession(ctx, a.Database, id, 0); err == nil && sess.SiteID == site.ID {
					c.VisitorID, c.VisitorSessionID, c.VisitorSessionUserID = sess.UserID, sess.ID, sess.UserID
					c.VisitorFamily = notify.PushFamily(sess)
					c.OnOwnAddress = r.Header.Get("X-SH-Framed") == ""
				}
			}
			c.PasscodeOK, c.ViewersOK = true, true
			row, err := accountsrepo.GetSitePasscode(ctx, a.Database, site.ID)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				httpx.WriteInternal(w)
				return
			}
			if err == nil {
				c.PasscodeOK = row.Enc == nil || r.Header.Get(Unlocked) != ""
				if row.NamedViewers {
					ok, _, err := accountapp.ViewerAllowed(ctx, a.Database, row, c.VisitorID)
					c.ViewersOK = c.VisitorID != "" && err == nil && ok
				}
			}
		}
		c.SiteID, c.OwnerID, c.SiteName = site.ID, site.UserID, site.Name
		c.CSRFOK = r.Header.Get("X-SH-CSRF") == "1"
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r.WithContext(appstorage.WithCall(ctx, c)))
	})
}
