package hackhttp

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/internal/app/hack/entries"
	"github.com/vsriram/simple-host/internal/app/hack/events"
	"github.com/vsriram/simple-host/internal/httpx"
	"github.com/vsriram/simple-host/internal/model"
)

// RegisterHackPublic serves the trusted, stable built-in event page on the
// apex, whether or not an organiser has published a custom event website.
// pages is the hack-event.html template cache (HackEventPages), built once
// by the caller and passed in explicitly (cleanup 3.2 slice 4).
func RegisterHackPublic(mux *http.ServeMux, database *sql.DB, appURL string,
	teamSiteURL func(string, string) string, ready func(string) bool, pages *HackEventPages) {
	perIP := httpx.NewRateLimiter(120, 5)
	mux.HandleFunc("GET /e/{slug}/submissions/{team}/screenshot", func(w http.ResponseWriter, r *http.Request) {
		if !perIP.AllowHTTP(w, httpx.ClientIP(r)) {
			http.Error(w, "Too many requests. Try again in a minute.", 429)
			return
		}
		ev, err := events.New(database).EventBySlug(r.Context(), strings.ToLower(r.PathValue("slug")))
		if err != nil || !ev.SubmissionsPublic || !submissionsOpen(ev) {
			writeHackEventNotFound(w, r, appURL, pages)
			return
		}
		teamID, err := visibleSubmissionTeam(r.Context(), database, ev, r.PathValue("team"))
		if err != nil {
			httpx.WriteInternal(w)
			return
		}
		if teamID != "" {
			data, mime, err := entries.New(database, nil).Screenshot(r.Context(), teamID)
			if err == nil && len(data) > 0 && entryImageAllowed(mime) {
				writeHackScreenshotPolicy(w, r, data, mime, true)
				return
			}
		}
		writeHackEventNotFound(w, r, appURL, pages)
	})
	render := HackEventPage(database, appURL, teamSiteURL, ready, pages)
	screenshot := HackScreenshot(database, ready)
	load := func(w http.ResponseWriter, r *http.Request) (model.User, bool) {
		ev, err := events.New(database).EventBySlug(r.Context(), strings.ToLower(r.PathValue("slug")))
		if err == nil {
			paperHackEvent(r, ev)
		}
		if errors.Is(err, sql.ErrNoRows) {
			writeHackEventNotFound(w, r, appURL, pages)
			return model.User{}, false
		}
		if err != nil {
			httpx.WriteInternal(w)
			return model.User{}, false
		}
		user, err := events.New(database).UserByID(r.Context(), ev.AccountID)
		if err != nil {
			httpx.WriteInternal(w)
			return model.User{}, false
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
