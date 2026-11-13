package hackhttp

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"testing"
	"time"

	accountapp "github.com/vsriram/simple-host/internal/app/accounts"
	"github.com/vsriram/simple-host/internal/auth"
	legacyrepo "github.com/vsriram/simple-host/internal/repository/legacy"
	"github.com/vsriram/simple-host/internal/webassets"
)

type navigationBrowserPages struct{ hackPages }

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
	w := newRulesWorld(t, 2)
	a := w.a
	for _, team := range w.teams {
		r := a.at(t, "PUT", w.entryPath(), buildCompleteFields(), a.key(team.person))
		if r.status != 200 {
			t.Fatalf("entry: %d %s", r.status, r.body)
		}
		r = a.at(t, "POST", w.entryPath()+"/submit", nil, a.key(team.person))
		if r.status != 200 {
			t.Fatalf("submit: %d %s", r.status, r.body)
		}
	}
	if _, err := a.database.Exec(`UPDATE events SET stage='closed' WHERE slug=$1`, w.slug); err != nil {
		t.Fatal(err)
	}
	if r := a.at(t, "POST", "/v1/hack/events/"+w.slug+"/conflicts", map[string]any{"team_id": w.teamID(t, w.teams[0].slug)}, a.key(w.judge)); r.status != 200 && r.status != 201 {
		t.Fatalf("conflict: %d %s", r.status, r.body)
	}
	multi := a.newPerson(t, "navmulti")
	fresh := a.newPerson(t, "navnew")
	other := a.newPerson(t, "navother")
	extra := []string{uniqueSlug(), uniqueSlug(), uniqueSlug()}
	for i, slug := range extra {
		owner := other
		if i == 0 {
			owner = multi
		}
		if r := a.createEvent(t, owner, slug, map[string]any{"title": []string{"Alpha event", "Beta event", "Past event"}[i]}); r.status != 201 {
			t.Fatalf("event: %d %s", r.status, r.body)
		}
	}
	uid := a.userID(t, multi)
	for _, m := range []struct{ slug, role string }{{w.slug, "participant"}, {extra[1], "judge"}, {extra[2], "participant"}} {
		if _, err := a.database.Exec(`INSERT INTO event_members (event_id,user_id,role,display_name,approval_status) SELECT id,$2,$3,'Navigation person','approved' FROM events WHERE slug=$1`, m.slug, uid, m.role); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.database.Exec(`UPDATE events SET stage='archived',closed_at=now() WHERE slug=$1`, extra[2]); err != nil {
		t.Fatal(err)
	}
	adminID, err := legacyrepo.EnsureAdminUser(context.Background(), a.database)
	if err != nil {
		t.Fatal(err)
	}
	mw := auth.Middleware(a.admin, adminID, accountapp.New(a.database), 0)
	a.mux.Handle("GET /v1/me", mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := hackNeedUser(w, r)
		if u == nil {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"id": u.ID, "display_name": "Navigation person", "email": u.Username})
	})))
	a.mux.HandleFunc("GET /v1/auth/oauth/providers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"providers":[],"email_enabled":false}`))
	})
	RegisterHackUI(a.mux, navigationBrowserPages{}, "simple-hack.test")
	// The public event page, for scripts/e2e-hack-reflow.mjs (SIM-415). No team sites here.
	RegisterHackPublic(a.mux, a.database, a.srv.URL, func(string, string) string { return "" }, func(string) bool { return false }, testHackPages)
	assets, _ := fs.Sub(webassets.Static, "static")
	a.mux.Handle("GET /", http.FileServer(http.FS(assets)))
	stop := make(chan struct{})
	a.mux.HandleFunc("POST /_fixture/stop", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
		select {
		case <-stop:
		default:
			close(stop)
		}
	})
	a.mux.HandleFunc("POST /_fixture/clear-results", func(wr http.ResponseWriter, r *http.Request) {
		if _, err := a.database.Exec(`DELETE FROM event_results WHERE event_id=(SELECT id FROM events WHERE slug=$1)`, w.slug); err != nil {
			t.Error(err)
			wr.WriteHeader(500)
			return
		}
		wr.WriteHeader(204)
	})
	info := map[string]any{"url": a.srv.URL, "slug": w.slug, "multi_slug": extra[0], "past_slug": extra[2], "keys": map[string]string{"admin": a.admin, "organiser": w.org.key, "multi": multi.key, "judge": w.judge.key, "participant": w.teams[0].person.key, "new": fresh.key}}
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
