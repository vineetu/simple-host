package handler

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/config"
	db "github.com/vsriram/simple-host/internal/db"
)

// TestServeVisitorRecordsBrowser stands up the whole app (database, captured
// mail, canonical person and site hosts, the connector) for a browser run of
// the "each person's records" recipe, then waits for the script to finish:
//
//	VISITOR_RECORDS_FIXTURE=/tmp/fixture.json DB_DSN=... go test ./internal/handler/ -run TestServeVisitorRecordsBrowser
//	node scripts/e2e-visitor-records.mjs /tmp/fixture.json
//
// It is a fixture, not an assertion: the script asserts, then POSTs
// /_fixture/stop. Skipped unless VISITOR_RECORDS_FIXTURE is set. Nothing here
// is compiled into the server.
func TestServeVisitorRecordsBrowser(t *testing.T) {
	file := os.Getenv("VISITOR_RECORDS_FIXTURE")
	if file == "" {
		t.Skip("VISITOR_RECORDS_FIXTURE unset")
	}
	mb := &mailbox{}
	a := newPrivateAppMailer(t, mb)
	a.sites.SetPersonHosts("canonical")
	db.SetPlatformDomain(pcSiteDomain)
	t.Cleanup(func() { db.SetPlatformDomain("") })
	certDir := t.TempDir()
	if err := os.Mkdir(certDir+"/ready", 0o755); err != nil {
		t.Fatal(err)
	}
	a.sites.SetSiteHosts("canonical", certDir)
	// Passcodes on, as on the hosted service, so set_site_passcode works here.
	if err := a.sites.SetPasscodeKey(testPasscodeKey); err != nil {
		t.Fatal(err)
	}
	// A fixed handle, so a browser without a host-resolver flag (WebKit) can
	// reach the site host through an /etc/hosts line.
	const handle = "shopowner"
	if _, err := a.database.Exec(`DELETE FROM users WHERE handle = $1`, handle); err != nil {
		t.Fatal(err)
	}
	owner := a.newPerson(t, handle)
	if _, err := a.database.Exec(`UPDATE users SET handle = $1 WHERE username = $2`, handle, owner.email); err != nil {
		t.Fatal(err)
	}
	markReady(t, certDir, handle)
	// One browser run signs the same people in on many sites from one address.
	a.sites.emailLimiter = newRateLimiter(1000, 0)
	a.sites.visitorAuthLimiter = newRateLimiter(1000, 0)
	a.sites.storageIPLimiter = newRateLimiter(100000, 0)
	RegisterUIRoutes(a.mux, a.srv.URL, a.sites)
	// A mailer is wired (the mailbox above), so provider discovery must say so.
	oauth := NewOAuthHandler(a.database, config.Config{PublicBaseURL: a.srv.URL, ResendAPIKey: "test-mail-sink"})
	oauth.Register(a.mux)
	// The test mail sink: the script reads each customer's code here.
	a.mux.HandleFunc("GET /_fixture/mail", func(w http.ResponseWriter, r *http.Request) {
		mb.mu.Lock()
		defer mb.mu.Unlock()
		writeJSON(w, 200, map[string]string{"code": mb.codes[r.URL.Query().Get("email")]})
	})
	stop := make(chan struct{})
	a.mux.HandleFunc("POST /_fixture/stop", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
		select {
		case <-stop:
		default:
			close(stop)
		}
	})
	info, _ := json.Marshal(map[string]string{
		"url": a.srv.URL, "owner_key": owner.key, "owner_email": owner.email, "handle": handle, "site_domain": pcSiteDomain,
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
