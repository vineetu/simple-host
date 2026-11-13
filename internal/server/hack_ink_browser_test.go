package server

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/hackhttp"
	"github.com/vsriram/simple-host/internal/handler"
	"github.com/vsriram/simple-host/internal/httpx"
)

// Opt-in isolated full app and mail sink for the Playwright presentation rehearsal.
// None of these fixture routes is compiled into the server.
func TestServeHackInkBrowser(t *testing.T) {
	path := os.Getenv("HACK_INK_BROWSER_FILE")
	if path == "" {
		t.Skip("HACK_INK_BROWSER_FILE unset")
	}
	mb := &mailbox{}
	a := newTeamSiteAppMailer(t, mb)
	a.sites.RebasePagesForTest(a.srv.URL)
	a.hack.SetMailer(mb)
	handler.RegisterUIRoutes(a.mux, a.srv.URL, a.sites)
	a.sites.Pages().RegisterSkillsHub(a.mux, a.srv.URL)
	hackhttp.RegisterHackHome(a.mux, a.sites.Pages())
	hackhttp.RegisterHackUI(a.mux, a.sites.Pages(), "")
	hackhttp.RegisterHackPublic(a.mux, a.database, a.srv.URL, a.sites.TeamSiteURL, a.sites.TeamSitesReady, testHackPages)
	a.mux.HandleFunc("GET /_fixture/mail", func(w http.ResponseWriter, r *http.Request) {
		mb.mu.Lock()
		defer mb.mu.Unlock()
		httpx.WriteJSON(w, 200, map[string]string{"code": mb.codes[r.URL.Query().Get("email")]})
	})
	a.mux.HandleFunc("POST /_fixture/ready/{slug}", func(w http.ResponseWriter, r *http.Request) {
		markReady(t, a.certDir, r.PathValue("slug"))
		w.WriteHeader(204)
	})
	a.mux.HandleFunc("GET /_fixture/takedown", a.sites.ServeTakedownForTest)
	a.mux.HandleFunc("GET /_fixture/offline", a.sites.ServeOfflineForTest)
	a.mux.HandleFunc("GET /_fixture/error", a.sites.RenderServiceErrorForTest)
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
