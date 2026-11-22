package servertest

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"testing"
	"time"

	accountapp "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/auth"
	accountrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/products/hack/core"
	"github.com/vsriram/simple-host/products/hack/hacktest"
	"github.com/vsriram/simple-host/products/hack/pages"
	"github.com/vsriram/simple-host/products/hack/public"
	"github.com/vsriram/simple-host/products/hack/webassets"
)

type navigationBrowserPages struct{ pages.Pages }

func (navigationBrowserPages) ServeStaticPage(name string) http.Handler {
	assets, _ := fs.Sub(webassets.Static, "static")
	return http.FileServer(http.FS(assets))
}
func (navigationBrowserPages) StaticFS() fs.FS                                { return webassets.Static }
func (navigationBrowserPages) AdminUICSP(next http.Handler) http.Handler      { return next }
func (navigationBrowserPages) UICSP(next http.Handler, _ string) http.Handler { return next }
func (navigationBrowserPages) RenderAppShell(_ *http.Request, body []byte, _ bool) ([]byte, error) {
	return body, nil
}

// Opt-in fixture: the node script drives real event APIs and source browser modules.
func TestServeHackNavigationBrowser(t *testing.T) {
	file := os.Getenv("HACK_NAVIGATION_FIXTURE")
	if file == "" {
		t.Skip("HACK_NAVIGATION_FIXTURE unset")
	}
	w := newFeatRulesWorld(t, 2)
	a := w.a
	for _, team := range w.Teams {
		r := a.At(t, "PUT", w.EntryPath(), hacktest.CompleteFields(), a.Key(team.Person))
		if r.Status != 200 {
			t.Fatalf("entry: %d %s", r.Status, r.Body)
		}
		r = a.At(t, "POST", w.EntryPath()+"/submit", nil, a.Key(team.Person))
		if r.Status != 200 {
			t.Fatalf("submit: %d %s", r.Status, r.Body)
		}
	}
	if _, err := a.DB.Exec(`UPDATE events SET stage='closed' WHERE slug=$1`, w.Slug); err != nil {
		t.Fatal(err)
	}
	if r := a.At(t, "POST", "/v1/hack/events/"+w.Slug+"/conflicts", map[string]any{"team_id": w.TeamID(t, w.Teams[0].Slug)}, a.Key(w.Judge)); r.Status != 200 && r.Status != 201 {
		t.Fatalf("conflict: %d %s", r.Status, r.Body)
	}
	multi := a.NewPerson(t, "navmulti")
	fresh := a.NewPerson(t, "navnew")
	other := a.NewPerson(t, "navother")
	extra := []string{hacktest.UniqueSlug(), hacktest.UniqueSlug(), hacktest.UniqueSlug()}
	for i, slug := range extra {
		owner := other
		if i == 0 {
			owner = multi
		}
		if r := a.CreateEvent(t, owner, slug, map[string]any{"title": []string{"Alpha event", "Beta event", "Past event"}[i]}); r.Status != 201 {
			t.Fatalf("event: %d %s", r.Status, r.Body)
		}
	}
	uid := a.UserID(t, multi)
	for _, m := range []struct{ slug, role string }{{w.Slug, "participant"}, {extra[1], "judge"}, {extra[2], "participant"}} {
		if _, err := a.DB.Exec(`INSERT INTO event_members (event_id,user_id,role,display_name,approval_status) SELECT id,$2,$3,'Navigation person','approved' FROM events WHERE slug=$1`, m.slug, uid, m.role); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.DB.Exec(`UPDATE events SET stage='archived',closed_at=now() WHERE slug=$1`, extra[2]); err != nil {
		t.Fatal(err)
	}
	adminID, err := accountrepo.EnsureAdminUser(context.Background(), a.DB)
	if err != nil {
		t.Fatal(err)
	}
	mw := auth.Middleware(a.Admin, adminID, accountapp.New(a.DB), 0)
	a.Mux.Handle("GET /v1/me", mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := core.NeedUser(w, r)
		if u == nil {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"id": u.ID, "display_name": "Navigation person", "email": u.Username})
	})))
	a.Mux.HandleFunc("GET /v1/auth/oauth/providers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"providers":[],"email_enabled":false}`))
	})
	pages.RegisterHackUI(a.Mux, navigationBrowserPages{}, "simple-hack.test")
	// The public event page, for scripts/e2e/e2e-hack-reflow.mjs (SIM-415). No team sites here.
	public.RegisterHackPublic(a.Mux, a.DB, a.Srv.URL, func(string, string) string { return "" }, func(string) bool { return false }, testHackPages)
	assets, _ := fs.Sub(webassets.Static, "static")
	a.Mux.Handle("GET /", http.FileServer(http.FS(assets)))
	stop := make(chan struct{})
	a.Mux.HandleFunc("POST /_fixture/stop", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
		select {
		case <-stop:
		default:
			close(stop)
		}
	})
	a.Mux.HandleFunc("POST /_fixture/clear-results", func(wr http.ResponseWriter, r *http.Request) {
		if _, err := a.DB.Exec(`DELETE FROM event_results WHERE event_id=(SELECT id FROM events WHERE slug=$1)`, w.Slug); err != nil {
			t.Error(err)
			wr.WriteHeader(500)
			return
		}
		wr.WriteHeader(204)
	})
	info := map[string]any{"url": a.Srv.URL, "slug": w.Slug, "multi_slug": extra[0], "past_slug": extra[2], "keys": map[string]string{"admin": a.Admin, "organiser": w.Org.Key, "multi": multi.Key, "judge": w.Judge.Key, "participant": w.Teams[0].Person.Key, "new": fresh.Key}}
	raw, _ := json.Marshal(info)
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stop:
	case <-time.After(30 * time.Minute):
		t.Fatal("navigation browser timed out")
	}
}
