package servertest

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
)

var (
	shotShown  = []byte("\x89PNG\r\n\x1a\nshown-team")
	shotHidden = []byte("\x89PNG\r\n\x1a\nhidden-team")
)

// The event page's gallery of team sites, on the event host, never shows a
// site the operator or organiser holds: suspended, taken down or deleted
// sites have no card and no screenshot, and an event take-down hides it all.
func TestHackEventGalleryHidesHeldSites(t *testing.T) {
	a := newTeamSiteApp(t)
	org := a.newPerson(t, "org")
	slug := a.makeEvent(t, org)
	var eventID, accountID string
	if err := a.database.QueryRow(`SELECT id, account_id FROM events WHERE slug = $1`, slug).Scan(&eventID, &accountID); err != nil {
		t.Fatal(err)
	}
	slugOf := map[string]string{}
	for _, name := range []string{"Live", "Suspended", "Takedown", "Deleted"} {
		p := a.newPerson(t, strings.ToLower(name))
		a.join(t, slug, p, org)
		teamSlug, _ := a.startTeam(t, slug, name, p)
		slugOf[name] = teamSlug
		shot := shotHidden
		if name == "Live" {
			shot = shotShown
		}
		if _, err := a.database.Exec(`INSERT INTO event_entries (team_id, event_id, title, screenshot, screenshot_type)
			SELECT id, event_id, $3, $4, 'image/png' FROM event_teams WHERE event_id = $1 AND slug = $2`, eventID, teamSlug, "ENTRY_"+name, shot); err != nil {
			t.Fatal(err)
		}
	}
	for name, extra := range map[string]string{"Live": "", "Suspended": ", suspended_at", "Takedown": "", "Deleted": ", deleted_at"} {
		cols, vals := "user_id, name, active_version"+extra, "$1, $2, 1"
		if extra != "" {
			vals += ", now()"
		}
		if _, err := a.database.Exec(`INSERT INTO sites (`+cols+`) VALUES (`+vals+`)`, accountID, slugOf[name]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.database.Exec(`UPDATE event_teams SET site_taken_down_at = now(), site_taken_down_reason = 'spam' WHERE event_id = $1 AND slug = $2`, eventID, slugOf["Takedown"]); err != nil {
		t.Fatal(err)
	}
	markReady(t, a.certDir, slug)
	wantTS(t, "open gallery", a.api(t, "PATCH", "/v1/hack/events/"+slug, map[string]any{"gallery_open": true}, org.key), 200, "")
	host := slug + "." + tsDomain

	page := a.on(t, "GET", host, "/", nil, "")
	if page.status != http.StatusOK || !strings.Contains(string(page.body), "ENTRY_Live") {
		t.Fatalf("gallery: %d", page.status)
	}
	if shot := a.on(t, "GET", host, "/screenshots/"+slugOf["Live"], nil, ""); shot.status != http.StatusOK || !bytes.Equal(shot.body, shotShown) {
		t.Fatalf("live screenshot: %d", shot.status)
	}
	for _, name := range []string{"Suspended", "Takedown", "Deleted"} {
		if strings.Contains(string(page.body), "ENTRY_"+name) {
			t.Errorf("gallery shows the %s site", name)
		}
		if r := a.on(t, "GET", host, "/screenshots/"+slugOf[name], nil, ""); r.status != http.StatusNotFound || bytes.Contains(r.body, shotHidden) {
			t.Errorf("%s screenshot: %d", name, r.status)
		}
	}

	// The platform's take-down of the event hides the gallery and every screenshot.
	if _, err := a.database.Exec(`UPDATE events SET taken_down_at = now(), taken_down_reason = 'nope' WHERE id = $1`, eventID); err != nil {
		t.Fatal(err)
	}
	if down := a.on(t, "GET", host, "/", nil, ""); down.status != http.StatusGone || strings.Contains(string(down.body), "ENTRY_") {
		t.Fatalf("taken-down event page: %d", down.status)
	}
	if r := a.on(t, "GET", host, "/screenshots/"+slugOf["Live"], nil, ""); r.status != http.StatusNotFound {
		t.Fatalf("taken-down event screenshot: %d", r.status)
	}
}
