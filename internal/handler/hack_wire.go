package handler

import (
	"database/sql"
	"errors"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	db "github.com/vsriram/simple-host/internal/db"
)

// hackGallerySlugRe is one label: lowercase letters, digits and hyphens, not
// starting or ending with a hyphen. A team slug is at most 30 characters; an
// event slug used inside a team-site URL is at most one DNS label.
var hackGallerySlugRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

func hackGallerySlug(s string) bool {
	return len(s) <= 30 && hackGallerySlugRe.MatchString(s)
}

// hackScreenshotPath is the team slug of /screenshots/<slug> when the path is
// exactly one team slug. Extra segments, traversal and any other shape are not
// this route.
func hackScreenshotPath(p string) (string, bool) {
	const prefix = "/screenshots/"
	if !strings.HasPrefix(p, prefix) {
		return "", false
	}
	slug := p[len(prefix):]
	if !hackGallerySlug(slug) {
		return "", false
	}
	return slug, true
}

func hackEventLabel(s string) bool {
	return len(s) <= 63 && hackGallerySlugRe.MatchString(s)
}

func hackGalleryImageType(s string) bool {
	switch s {
	case "image/png", "image/jpeg", "image/webp":
		return true
	default:
		return false
	}
}

// hackGalleryPublic: the event page may list projects, and the event host may
// serve their screenshots. ready is false until the event's team addresses
// have a certificate (and asking reports that, the same as a deploy).
func hackGalleryPublic(ev db.Event, ready func(string) bool) bool {
	return ev.GalleryOpen && ev.Stage != "draft" && !ev.TakenDown() && ready != nil && ready(ev.Slug)
}

// HackEventPage is the SiteHandler hook that renders <event>.<SITE_DOMAIN>/
// (hack_mode.go): user is the host's account; false when it holds no event.
// teamSiteURL and ready are SiteHandler.TeamSiteURL and TeamSitesReady.
func HackEventPage(database *sql.DB, appURL string, teamSiteURL func(eventSlug, teamSlug string) string, ready func(eventSlug string) bool) func(w http.ResponseWriter, r *http.Request, user db.User) bool {
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
		if hackGalleryPublic(ev, ready) {
			cards, gerr := db.ListGalleryCards(r.Context(), database, ev.ID, ev.AccountID)
			if gerr != nil {
				log.Printf("event page %s: gallery: %v", ev.Slug, gerr)
			} else {
				p.Gallery = hackGalleryCards(ev.Slug, cards, teamSiteURL)
			}
		}
		renderHackEventPage(w, r, p)
		return true
	}
}

func hackGalleryCards(eventSlug string, cards []db.GalleryCard, teamSiteURL func(string, string) string) []hackGalleryCard {
	var out []hackGalleryCard
	for _, c := range cards {
		link := hackGalleryLink(teamSiteURL, eventSlug, c.Slug)
		if link == "" {
			continue
		}
		title := c.Title
		if title == "" {
			title = c.Name
		}
		card := hackGalleryCard{Title: title, Tagline: c.Tagline, URL: link}
		if title != c.Name {
			card.Team = c.Name
		}
		if c.HasScreenshot {
			card.Shot = "/screenshots/" + c.Slug
		}
		out = append(out, card)
	}
	return out
}

// hackGalleryLink is the team site URL when both slugs are single labels and
// the builder returned an http(s) URL whose host starts with those labels.
func hackGalleryLink(teamSiteURL func(string, string) string, eventSlug, teamSlug string) string {
	if teamSiteURL == nil || !hackEventLabel(eventSlug) || !hackGallerySlug(teamSlug) {
		return ""
	}
	raw := teamSiteURL(eventSlug, teamSlug)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return ""
	}
	if !strings.HasPrefix(strings.ToLower(u.Hostname()), teamSlug+"."+eventSlug+".") {
		return ""
	}
	return raw
}

// HackScreenshot is the SiteHandler hook for GET/HEAD /screenshots/<team> on
// the event host. It writes the image only while that team is on the public
// gallery; otherwise false, and the caller renders the 404.
func HackScreenshot(database *sql.DB, ready func(string) bool) func(w http.ResponseWriter, r *http.Request, user db.User, team string) bool {
	return func(w http.ResponseWriter, r *http.Request, user db.User, team string) bool {
		if !hackGallerySlug(team) {
			return false
		}
		ev, err := db.GetEventByAccount(r.Context(), database, user.ID)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				log.Printf("event screenshot %s: %v", user.ID, err)
			}
			return false
		}
		if !hackGalleryPublic(ev, ready) {
			return false
		}
		data, ctype, err := db.GalleryScreenshot(r.Context(), database, ev.ID, ev.AccountID, team)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				log.Printf("event screenshot %s/%s: %v", ev.Slug, team, err)
			}
			return false
		}
		if !hackGalleryImageType(ctype) || len(data) == 0 {
			return false
		}
		writeHackScreenshot(w, r, data, ctype)
		return true
	}
}

func writeHackScreenshot(w http.ResponseWriter, r *http.Request, data []byte, contentType string) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}
