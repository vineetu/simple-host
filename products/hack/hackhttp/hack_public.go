package hackhttp

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/products/hack/app/entries"
	"github.com/vsriram/simple-host/products/hack/app/events"
)

// RegisterHackPublic serves the trusted, stable built-in event page on the
// apex, whether or not an organiser has published a custom event website.
func RegisterHackPublic(mux *http.ServeMux, database *sql.DB, appURL string,
	teamSiteURL func(string, string) string, ready func(string) bool, pages *HackEventPages, owners ...*HackHandler) {
	mux.HandleFunc("GET /e/{slug}/submissions/{team}/screenshot", submissionScreenshotHandler(database, appURL, pages, screenshotLimiter(owners)))
	render := HackEventPage(database, appURL, teamSiteURL, ready, pages)
	screenshot := HackScreenshot(database, ready, owners...)
	mux.HandleFunc("GET /e/{slug}", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("judge") == "1" {
			http.Redirect(w, r, "/e/"+r.PathValue("slug")+"/judge", http.StatusFound)
			return
		}
		if user, ok := loadPublicEventOwner(w, r, database, appURL, pages); ok {
			render(w, r, user)
		}
	})
	mux.HandleFunc("GET /e/{slug}/screenshots/{team}", func(w http.ResponseWriter, r *http.Request) {
		user, ok := loadPublicEventOwner(w, r, database, appURL, pages)
		if ok && !screenshot(w, r, user, r.PathValue("team")) {
			writeEventNotFound(w)
		}
	})
}

// loadPublicEventOwner is the account holding the event at /e/{slug}; it
// writes the event page's own not-found (or a 500) and returns false otherwise.
func loadPublicEventOwner(w http.ResponseWriter, r *http.Request, database *sql.DB, appURL string, pages *HackEventPages) (model.User, bool) {
	ev, err := events.New(database, nil).EventBySlug(r.Context(), strings.ToLower(r.PathValue("slug")))
	if errors.Is(err, sql.ErrNoRows) {
		writeHackEventNotFound(w, r, appURL, pages)
		return model.User{}, false
	}
	if err != nil {
		httpx.WriteInternal(w)
		return model.User{}, false
	}
	paperHackEvent(r, ev)
	user, err := events.New(database, nil).UserByID(r.Context(), ev.AccountID)
	if err != nil {
		httpx.WriteInternal(w)
		return model.User{}, false
	}
	return user, true
}

// submissionScreenshotHandler serves one public submission's screenshot,
// metered per network address, while submissions are public and open;
// anything else is the event page's not-found.
func submissionScreenshotHandler(database *sql.DB, appURL string, pages *HackEventPages, perIP *httpx.RateLimiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !perIP.AllowHTTP(w, httpx.ClientIP(r)) {
			httpx.TooManyRequests(w)
			return
		}
		ev, err := events.New(database, nil).EventBySlug(r.Context(), strings.ToLower(r.PathValue("slug")))
		if err != nil || !ev.SubmissionsPublic || !entries.SubmissionsOpen(ev) {
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
	}
}
