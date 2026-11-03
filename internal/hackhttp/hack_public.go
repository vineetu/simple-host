package hackhttp

import (
	"database/sql"
	"errors"
	"github.com/vsriram/simple-host/internal/httpx"
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/internal/db"
)

// RegisterHackPublic serves the trusted, stable built-in event page on the
// apex, whether or not an organiser has published a custom event website.
// pages is the hack-event.html template cache (HackEventPages), built once
// by the caller and passed in explicitly (cleanup 3.2 slice 4).
func RegisterHackPublic(mux *http.ServeMux, database *sql.DB, appURL string,
	teamSiteURL func(string, string) string, ready func(string) bool, pages *HackEventPages) {
	render := HackEventPage(database, appURL, teamSiteURL, ready, pages)
	screenshot := HackScreenshot(database, ready)
	load := func(w http.ResponseWriter, r *http.Request) (db.User, bool) {
		ev, err := db.GetEventBySlug(r.Context(), database, strings.ToLower(r.PathValue("slug")))
		if err == nil {
			paperHackEvent(r, ev)
		}
		if errors.Is(err, sql.ErrNoRows) {
			writeHackEventNotFound(w, r, appURL, pages)
			return db.User{}, false
		}
		if err != nil {
			httpx.WriteInternal(w)
			return db.User{}, false
		}
		user, err := db.GetUserByID(r.Context(), database, ev.AccountID)
		if err != nil {
			httpx.WriteInternal(w)
			return db.User{}, false
		}
		return user, true
	}
	mux.HandleFunc("GET /e/{slug}", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("judge") == "1" {
			http.Redirect(w, r, "/e/"+r.PathValue("slug")+"/judge", http.StatusFound)
			return
		}
		user, ok := load(w, r)
		if ok {
			render(w, r, user)
		}
	})
	mux.HandleFunc("GET /e/{slug}/screenshots/{team}", func(w http.ResponseWriter, r *http.Request) {
		user, ok := load(w, r)
		if !ok {
			return
		}
		if !screenshot(w, r, user, r.PathValue("team")) {
			writeEventNotFound(w)
		}
	})
}
