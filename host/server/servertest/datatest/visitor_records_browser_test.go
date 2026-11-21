package datatest

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	accountapp "github.com/vsriram/simple-host/host/access"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/server/servertest"
	"github.com/vsriram/simple-host/host/sites/siteshttp"
)

// TestServeVisitorRecordsBrowser stands up the whole app (database, captured
// mail, canonical person and site hosts, the connector) for a browser run of
// the "each person's records" recipe, then waits for the script to finish:
//
//	VISITOR_RECORDS_FIXTURE=/tmp/fixture.json DB_DSN=... go test ./host/handler/ -run TestServeVisitorRecordsBrowser
//	node scripts/e2e/e2e-visitor-records.mjs /tmp/fixture.json
//
// It is a fixture, not an assertion: the script asserts, then POSTs
// /_fixture/stop. Skipped unless VISITOR_RECORDS_FIXTURE is set. Nothing here
// is compiled into the server.
func TestServeVisitorRecordsBrowser(t *testing.T) {
	file := os.Getenv("VISITOR_RECORDS_FIXTURE")
	if file == "" {
		t.Skip("VISITOR_RECORDS_FIXTURE unset")
	}
	serveVisitorRecordsBrowser(t, file)
}

func serveVisitorRecordsBrowser(t *testing.T, file string) {
	t.Helper()
	mb := &servertest.Mailbox{}
	a := servertest.NewPrivateAppMailer(t, mb)
	a.Sites.Hosts.SetPersonHosts("canonical")
	accountsrepo.SetPlatformDomain(servertest.PcSiteDomain)
	t.Cleanup(func() { accountsrepo.SetPlatformDomain("") })
	certDir := t.TempDir()
	if err := os.Mkdir(certDir+"/ready", 0o755); err != nil {
		t.Fatal(err)
	}
	a.Sites.Hosts.SetSiteHosts("canonical", certDir)
	// Passcodes on, as on the hosted service, so set_site_passcode works here.
	if err := a.Access.SetPasscodeKey(servertest.TestPasscodeKey); err != nil {
		t.Fatal(err)
	}
	// A fixed handle, so a browser without a host-resolver flag (WebKit) can
	// reach the site host through an /etc/hosts line.
	const handle = "shopowner"
	if _, err := a.Database.Exec(`DELETE FROM users WHERE handle = $1`, handle); err != nil {
		t.Fatal(err)
	}
	owner := a.NewPerson(t, handle)
	if _, err := a.Database.Exec(`UPDATE users SET handle = $1 WHERE username = $2`, handle, owner.Email); err != nil {
		t.Fatal(err)
	}
	servertest.MarkReady(t, certDir, handle)
	// One browser run signs the same people in on many sites from one address.
	a.Access.EmailLimiter = httpx.NewRateLimiter(1000, 0)
	a.Access.VisitorAuthLimiter = httpx.NewRateLimiter(1000, 0)
	a.Data.StorageIPLimiter = httpx.NewRateLimiter(100000, 0)
	siteshttp.RegisterUIRoutes(a.Mux, a.Srv.URL, a.Sites)
	// A mailer is wired (the mailbox above), so provider discovery must say so.
	oauth := servertest.NewTestOAuthHandler(a.Database, accountapp.New(a.Database), config.Config{PublicBaseURL: a.Srv.URL, ResendAPIKey: "test-mail-sink"})
	oauth.Register(a.Mux)
	// The test mail sink: the script reads each customer's code here.
	a.Mux.HandleFunc("GET /_fixture/mail", func(w http.ResponseWriter, r *http.Request) {
		mb.Mu.Lock()
		defer mb.Mu.Unlock()
		httpx.WriteJSON(w, 200, map[string]string{"code": mb.Codes[r.URL.Query().Get("email")]})
	})
	stop := make(chan struct{})
	a.Mux.HandleFunc("POST /_fixture/stop", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
		select {
		case <-stop:
		default:
			close(stop)
		}
	})
	info, _ := json.Marshal(map[string]string{
		"url": a.Srv.URL, "owner_key": owner.Key, "owner_email": owner.Email, "handle": handle, "site_domain": servertest.PcSiteDomain,
	})
	if err := os.WriteFile(file, info, 0o600); err != nil {
		t.Fatal(err)
	}
	minutes := 30
	if v, err := strconv.Atoi(os.Getenv("VISITOR_RECORDS_TIMEOUT_MIN")); err == nil && v > 0 {
		minutes = v
	}
	select {
	case <-stop:
	case <-time.After(time.Duration(minutes) * time.Minute):
		t.Fatal("browser fixture timed out")
	}
}
