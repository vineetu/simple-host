package handler

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"
)

// Opt-in isolated full app and mail sink for the Playwright presentation rehearsal.
// None of these fixture routes is compiled into the server.
func TestServeHackInkBrowser(t *testing.T) {
	path := os.Getenv("HACK_INK_BROWSER_FILE")
	if path == "" {
		t.Skip("HACK_INK_BROWSER_FILE unset")
	}
	a := newTeamSiteApp(t)
	SetHackChrome(true)
	t.Cleanup(func() { SetHackChrome(false) })
	mb := &mailbox{}
	a.users.mailer = mb
	a.users.alerts.mailer = mb
	a.sites.mailer = mb
	a.hack.SetMailer(mb)
	RegisterUIRoutes(a.mux, a.srv.URL, a.sites)
	RegisterHackHome(a.mux)
	RegisterHackUI(a.mux)
	RegisterHackPublic(a.mux, a.database, a.srv.URL, a.sites.TeamSiteURL, a.sites.TeamSitesReady)
	a.mux.HandleFunc("GET /_fixture/mail", func(w http.ResponseWriter, r *http.Request) {
		mb.mu.Lock()
		defer mb.mu.Unlock()
		writeJSON(w, 200, map[string]string{"code": mb.codes[r.URL.Query().Get("email")]})
	})
	a.mux.HandleFunc("POST /_fixture/ready/{slug}", func(w http.ResponseWriter, r *http.Request) {
		markReady(t, a.certDir, r.PathValue("slug"))
		w.WriteHeader(204)
	})
	a.mux.HandleFunc("GET /_fixture/takedown", serveTakedown)
	a.mux.HandleFunc("GET /_fixture/offline", serveOffline)
	a.mux.HandleFunc("GET /_fixture/error", a.sites.renderServiceError)
	stop := make(chan struct{})
	a.mux.HandleFunc("POST /_fixture/stop", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204); close(stop) })
	info, _ := json.Marshal(map[string]string{"url": a.srv.URL, "admin": a.admin})
	if err := os.WriteFile(path, info, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stop:
	case <-time.After(30 * time.Minute):
		t.Fatal("browser fixture timed out")
	}
}
