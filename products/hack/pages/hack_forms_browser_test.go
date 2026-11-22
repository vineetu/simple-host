package pages

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"testing"
	"time"

	hacktest "github.com/vsriram/simple-host/products/hack/hacktest"

	core "github.com/vsriram/simple-host/products/hack/core"

	accountapp "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/auth"
	accountrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/products/hack/webassets"
)

// Opt-in server for scripts/e2e/e2e-hack-forms.mjs, using real editable event APIs.
func TestServeHackFormsBrowser(t *testing.T) {
	file := os.Getenv("HACK_FORMS_FIXTURE")
	if file == "" {
		t.Skip("HACK_FORMS_FIXTURE unset")
	}
	w := newRulesWorld(t, 1) // Open: both participant entries and organiser settings are editable.
	a := w.a
	if r := a.At(t, "PUT", w.EntryPath(), hacktest.CompleteFields(), a.Key(w.Teams[0].Person)); r.Status != 200 {
		t.Fatalf("entry: %d %s", r.Status, r.Body)
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
		json.NewEncoder(w).Encode(map[string]any{"id": u.ID, "display_name": "Forms person", "email": u.Username})
	})))
	a.Mux.HandleFunc("GET /v1/auth/oauth/providers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"providers":[],"email_enabled":false}`))
	})
	RegisterHackUI(a.Mux, navigationBrowserPages{}, "simple-hack.test")
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
	raw, err := json.Marshal(map[string]any{"url": a.Srv.URL, "slug": w.Slug, "keys": map[string]string{"organiser": w.Org.Key, "participant": w.Teams[0].Person.Key}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stop:
	case <-time.After(30 * time.Minute):
		t.Fatal("forms browser timed out")
	}
}
