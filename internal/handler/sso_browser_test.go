package handler

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	db "github.com/vsriram/simple-host/internal/db"
)

// Same captured-mail and host-routing fixture as visitor_records_browser_test.
// Only the local browser harness can see these test-only endpoints.
func TestServeSSOBrowser(t *testing.T) {
	file := os.Getenv("SSO_FIXTURE")
	if file == "" {
		t.Skip("SSO_FIXTURE unset")
	}
	const apex = "https://simple-host.test"
	mb := &mailbox{}
	a := newPrivateAppMailerBase(t, mb, apex)
	a.sites.SetPersonHosts("canonical")
	db.SetPlatformDomain(pcSiteDomain)
	t.Cleanup(func() { db.SetPlatformDomain("") })
	dir := t.TempDir()
	os.Mkdir(dir+"/ready", 0755)
	a.sites.SetSiteHosts("canonical", dir)
	if err := a.sites.SetPasscodeKey(testPasscodeKey); err != nil {
		t.Fatal(err)
	}
	owner := a.newPerson(t, "ssobrowser")
	uid, handle := a.userID(t, owner)
	markReady(t, dir, handle)
	o := a.withOAuth(t)
	o.cfg.ResendAPIKey = "fixture-mail"
	// Registered middleware holds the limiter pointer; adjust it in place.
	for _, limiter := range []*rateLimiter{a.users.ipLimiter, a.users.emailLimiter, a.sites.emailLimiter, a.sites.visitorAuthLimiter, o.ipLimiter} {
		limiter.mu.Lock()
		limiter.capacity = 10000
		limiter.rate = 0
		limiter.buckets = map[string]*tokenBucket{}
		limiter.mu.Unlock()
	}
	o.providers = nil
	a.sites.SetSSOHop(o)
	RegisterUIRoutes(a.mux, apex, a.sites)
	// Production nginx serves custom-domain files at the host root. Reuse
	// the same Go file gate here, retaining the actual API and cookie paths.
	root := a.srv.Config.Handler
	a.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requestHostName(r) == "sso-custom.example.test" && !strings.HasPrefix(r.URL.Path, "/v1/") {
			a.sites.serveSiteFileRel(w, r, uid, "custom", r.URL.Path)
			return
		}
		root.ServeHTTP(w, r)
	})
	// Establish custom-domain DNS proof without public DNS in a local test.
	a.mux.HandleFunc("GET /_fixture/mail", func(w http.ResponseWriter, r *http.Request) {
		mb.mu.Lock()
		defer mb.mu.Unlock()
		writeJSON(w, 200, map[string]string{"code": mb.codes[r.URL.Query().Get("email")]})
	})
	a.mux.HandleFunc("POST /_fixture/custom", func(w http.ResponseWriter, r *http.Request) {
		_, err := a.database.Exec(`UPDATE sites SET custom_domain='sso-custom.example.test',domain_verified_at=now(),domain_status='active' WHERE user_id=$1 AND name='custom'`, uid)
		if err != nil {
			http.Error(w, "custom fixture", 500)
			return
		}
		w.WriteHeader(204)
	})
	a.mux.HandleFunc("POST /_fixture/sweep-pass", func(w http.ResponseWriter, r *http.Request) {
		_, err := a.database.Exec(`UPDATE sso_passes SET idle_expires_at=now()-interval '1 second' WHERE parent_key_hash=$1`, db.HashAPIKey(r.Header.Get("X-API-Key")))
		if err == nil {
			err = db.SweepSSO(r.Context(), a.database)
		}
		if err != nil {
			http.Error(w, "sweep fixture", 500)
			return
		}
		w.WriteHeader(204)
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
	info, _ := json.Marshal(map[string]string{"url": a.srv.URL, "apex": apex, "owner_key": owner.key, "owner_email": owner.email, "handle": handle, "site_domain": pcSiteDomain})
	if err := os.WriteFile(file, info, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stop:
	case <-time.After(40 * time.Minute):
		t.Fatal("SSO browser fixture timed out")
	}
}
