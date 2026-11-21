package accesstest

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	accounts "github.com/vsriram/simple-host/host/access"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/server/servertest"
	"github.com/vsriram/simple-host/host/sites/siteshttp"
)

// Same captured-mail and host-routing fixture as visitor_records_browser_test.
// Only the local browser harness can see these test-only endpoints.
func TestServeSSOBrowser(t *testing.T) {
	file := os.Getenv("SSO_FIXTURE")
	if file == "" {
		t.Skip("SSO_FIXTURE unset")
	}
	const apex = "https://simple-host.test"
	mb := &servertest.Mailbox{}
	a := servertest.NewPrivateAppMailerBase(t, mb, apex)
	a.Sites.Hosts.SetPersonHosts("canonical")
	accountsrepo.SetPlatformDomain(servertest.PcSiteDomain)
	t.Cleanup(func() { accountsrepo.SetPlatformDomain("") })
	dir := t.TempDir()
	os.Mkdir(dir+"/ready", 0755)
	a.Sites.Hosts.SetSiteHosts("canonical", dir)
	if err := a.Access.SetPasscodeKey(servertest.TestPasscodeKey); err != nil {
		t.Fatal(err)
	}
	owner := a.NewPerson(t, "ssobrowser")
	uid, handle := a.UserID(t, owner)
	servertest.MarkReady(t, dir, handle)
	o := a.WithOAuth(t)
	o.Cfg.EmailEnabled = true
	// Registered middleware holds the limiter pointer; adjust it in place.
	for _, limiter := range []*httpx.RateLimiter{a.Users.IPLimiter, a.Users.EmailLimiter, a.Access.EmailLimiter, a.Access.VisitorAuthLimiter, o.IPLimiter} {
		limiter.Reset(10000, 0)
	}
	o.SignIn = accounts.NewProviderSignIn(o.Accounts, nil)
	a.Access.SetSSOHop(o)
	siteshttp.RegisterUIRoutes(a.Mux, apex, a.Sites)
	// Production nginx serves custom-domain files at the host root. Reuse
	// the same Go file gate here, retaining the actual API and cookie paths.
	root := a.Srv.Config.Handler
	a.Srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if httpx.HostName(r) == "sso-custom.example.test" && !strings.HasPrefix(r.URL.Path, "/v1/") {
			a.Sites.ServeSiteFileRel(w, r, uid, "custom", r.URL.Path)
			return
		}
		root.ServeHTTP(w, r)
	})
	// Establish custom-domain DNS proof without public DNS in a local test.
	a.Mux.HandleFunc("GET /_fixture/mail", func(w http.ResponseWriter, r *http.Request) {
		mb.Mu.Lock()
		defer mb.Mu.Unlock()
		httpx.WriteJSON(w, 200, map[string]string{"code": mb.Codes[r.URL.Query().Get("email")]})
	})
	a.Mux.HandleFunc("POST /_fixture/custom", func(w http.ResponseWriter, r *http.Request) {
		_, err := a.Database.Exec(`UPDATE sites SET custom_domain='sso-custom.example.test',domain_verified_at=now(),domain_status='active' WHERE user_id=$1 AND name='custom'`, uid)
		if err != nil {
			http.Error(w, "custom fixture", 500)
			return
		}
		w.WriteHeader(204)
	})
	a.Mux.HandleFunc("POST /_fixture/sweep-pass", func(w http.ResponseWriter, r *http.Request) {
		_, err := a.Database.Exec(`UPDATE sso_passes SET idle_expires_at=now()-interval '1 second' WHERE parent_key_hash=$1`, accountsrepo.HashAPIKey(r.Header.Get("X-API-Key")))
		if err == nil {
			err = accountsrepo.SweepSSO(r.Context(), a.Database)
		}
		if err != nil {
			http.Error(w, "sweep fixture", 500)
			return
		}
		w.WriteHeader(204)
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
	info, _ := json.Marshal(map[string]string{"url": a.Srv.URL, "apex": apex, "owner_key": owner.Key, "owner_email": owner.Email, "handle": handle, "site_domain": servertest.PcSiteDomain})
	if err := os.WriteFile(file, info, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stop:
	case <-time.After(40 * time.Minute):
		t.Fatal("SSO browser fixture timed out")
	}
}
