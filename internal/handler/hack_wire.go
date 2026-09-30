package handler

import (
	"database/sql"
	"errors"
	"log"
	"net/http"

	db "github.com/vsriram/simple-host/internal/db"
)

// HackEventPage is the SiteHandler hook that renders <event>.<SITE_DOMAIN>/
// (hack_mode.go): user is the host's account; false when it holds no event.
func HackEventPage(database *sql.DB, appURL string) func(w http.ResponseWriter, r *http.Request, user db.User) bool {
	return func(w http.ResponseWriter, r *http.Request, user db.User) bool {
		ev, err := db.GetEventByAccount(r.Context(), database, user.ID)
		if errors.Is(err, sql.ErrNoRows) {
			return false
		}
		if err != nil {
			log.Printf("event page %s: %v", user.ID, err)
			http.Error(w, "Something went wrong. Try again in a minute.", http.StatusInternalServerError)
			return true
		}
		participants, teams, _, err := db.CountEventMembers(r.Context(), database, ev.ID)
		if err != nil {
			log.Printf("event page %s: counts: %v", ev.Slug, err)
		}
		p := hackEventPage{
			Slug: ev.Slug, Title: ev.Title, Tagline: ev.Tagline, About: ev.About, Rules: ev.Rules, Prizes: ev.Prizes,
			OrganiserName: ev.OrganiserName, Organisation: ev.Organisation,
			Stage: ev.Stage, TimeZone: ev.TimeZone,
			Participants: participants, Teams: teams,
			AppURL:    appURL,
			TakenDown: ev.TakenDown(), TakenDownReason: ev.TakenDownReason,
		}
		if ev.StartsAt.Valid {
			p.StartsAt = ev.StartsAt.Time
		}
		if ev.EndsAt.Valid {
			p.EndsAt = ev.EndsAt.Time
		}
		renderHackEventPage(w, r, p)
		return true
	}
}
