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

// Opt-in server for scripts/e2e-hack-forms.mjs, using real editable event APIs.
func TestServeHackFormsBrowser(t *testing.T) {
	file := os.Getenv("HACK_FORMS_FIXTURE")
	if file == "" {
		t.Skip("HACK_FORMS_FIXTURE unset")
	}
	w := newRulesWorld(t, 1) // Open: both participant entries and organiser settings are editable.
	a := w.a
	if r := a.at(t, "PUT", w.entryPath(), buildCompleteFields(), a.key(w.teams[0].person)); r.status != 200 {
		t.Fatalf("entry: %d %s", r.status, r.body)
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
		json.NewEncoder(w).Encode(map[string]any{"id": u.ID, "display_name": "Forms person", "email": u.Username})
	})))
	a.mux.HandleFunc("GET /v1/auth/oauth/providers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"providers":[],"email_enabled":false}`))
	})
	RegisterHackUI(a.mux, navigationBrowserPages{}, "simple-hack.test")
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
	raw, err := json.Marshal(map[string]any{"url": a.srv.URL, "slug": w.slug, "keys": map[string]string{"organiser": w.org.key, "participant": w.teams[0].person.key}})
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
