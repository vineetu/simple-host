package servertest

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/access/auth"
	accountrepo "github.com/vsriram/simple-host/host/access/repo"
)

// The gallery card and screenshot are hidden in draft, with the gallery
// switch off, and while addresses are not ready; they show once permitted.
func TestRhpGalleryVisibility(t *testing.T) {
	a := newTeamSiteApp(t)
	org := a.newPerson(t, "rhpgalorg")
	slug := a.makeEvent(t, org)
	var eventID, accountID string
	if err := a.database.QueryRow(`SELECT id, account_id FROM events WHERE slug = $1`, slug).Scan(&eventID, &accountID); err != nil {
		t.Fatal(err)
	}
	p := a.newPerson(t, "rhpgalmember")
	a.join(t, slug, p, org)
	team, _ := a.startTeam(t, slug, "Shown", p)
	if _, err := a.database.Exec(`INSERT INTO sites (user_id, name, active_version) VALUES ($1, $2, 1)`, accountID, team); err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.Exec(`INSERT INTO event_entries (team_id, event_id, title, screenshot, screenshot_type)
		SELECT id, event_id, 'ENTRY_SHOWN', $3, 'image/png' FROM event_teams WHERE event_id = $1 AND slug = $2`, eventID, team, shotShown); err != nil {
		t.Fatal(err)
	}
	host := slug + "." + tsDomain
	shows := func() (card, shot bool) {
		page := a.on(t, "GET", host, "/", nil, "")
		if page.status != http.StatusOK {
			t.Fatalf("page: %d", page.status)
		}
		r := a.on(t, "GET", host, "/screenshots/"+team, nil, "")
		if r.status != http.StatusOK && r.status != http.StatusNotFound {
			t.Fatalf("screenshot: %d", r.status)
		}
		return strings.Contains(string(page.body), "ENTRY_SHOWN"), r.status == http.StatusOK && bytes.Equal(r.body, shotShown)
	}
	setStage := func(stage string) {
		t.Helper()
		if _, err := a.database.Exec(`UPDATE events SET stage = $2 WHERE slug = $1`, slug, stage); err != nil {
			t.Fatal(err)
		}
	}
	setGallery := func(on bool) {
		t.Helper()
		if _, err := a.database.Exec(`UPDATE events SET gallery_open = $2 WHERE slug = $1`, slug, on); err != nil {
			t.Fatal(err)
		}
	}
	setReady := func(on bool) {
		t.Helper()
		if !on {
			if err := os.Remove(filepath.Join(a.certDir, "ready", slug)); err != nil {
				t.Fatal(err)
			}
			return
		}
		markReady(t, a.certDir, slug)
	}

	setStage("building")
	setReady(true)
	for _, tc := range []struct {
		name               string
		stage              string
		gallery, ready     bool
		wantCard, wantShot bool
	}{
		{"switch off", "building", false, true, false, false},
		{"not ready", "building", true, false, false, false},
		{"draft", "draft", true, true, false, false},
		{"permitted", "building", true, true, true, true},
	} {
		setStage(tc.stage)
		setGallery(tc.gallery)
		setReady(tc.ready)
		if card, shot := shows(); card != tc.wantCard || shot != tc.wantShot {
			t.Fatalf("%s: card=%v screenshot=%v, want card=%v screenshot=%v", tc.name, card, shot, tc.wantCard, tc.wantShot)
		}
		if !tc.ready {
			setReady(true)
		}
	}
}

// The public vote endpoint hides tallies while open, refuses writes before
// and after the window and for the wrong members, and counts one vote per
// mailbox. Draft events have no vote routes.
func TestRhpVotingEndpoint(t *testing.T) {
	a := newTeamSiteApp(t)
	org, otherOrg := a.newPerson(t, "rhpvoteorg"), a.newPerson(t, "rhpvoteother")
	p1, p2 := a.newPerson(t, "rhpvote1"), a.newPerson(t, "rhpvote2")
	slug := a.makeEvent(t, org)
	base := "/v1/hack/events/" + slug

	draft := uniqueSlug()
	wantTS(t, "create draft", a.api(t, "POST", "/v1/hack/events", map[string]any{
		"slug": draft, "title": "Hidden draft", "organiser_name": "Org", "contact_email": "org@example.com",
		"purpose": "testing", "expected_participants": 10, "starts_at": "2026-10-01", "time_zone": "UTC",
	}, otherOrg.key), 201, "")
	t.Cleanup(func() {
		var accountID string
		_ = a.database.QueryRow(`SELECT account_id FROM events WHERE slug=$1`, draft).Scan(&accountID)
		_, _ = a.database.Exec(`DELETE FROM events WHERE slug=$1`, draft)
		if accountID != "" {
			_, _ = a.database.Exec(`DELETE FROM users WHERE id=$1`, accountID)
		}
	})
	wantTS(t, "draft vote hidden", a.api(t, "GET", "/v1/hack/events/"+draft+"/vote", nil, ""), 404, "event_not_found")
	wantTS(t, "draft own vote hidden", a.api(t, "GET", "/v1/hack/events/"+draft+"/my-vote", nil, p1.key), 404, "event_not_found")
	wantTS(t, "draft write hidden", a.api(t, "PUT", "/v1/hack/events/"+draft+"/vote", map[string]string{"team": "alpha"}, p1.key), 404, "event_not_found")

	for _, p := range []person{p1, p2} {
		a.join(t, slug, p, org)
	}
	alpha, _ := a.startTeam(t, slug, "Alpha", p1)
	beta, _ := a.startTeam(t, slug, "Beta", p2)
	markReady(t, a.certDir, slug)
	for team, p := range map[string]person{alpha: p1, beta: p2} {
		if r := a.deployTeam(t, slug, team, p.key, team); r.status != 200 && r.status != 201 {
			t.Fatalf("deploy %s: %d %s", team, r.status, r.body)
		}
	}
	wantTS(t, "gallery open", a.api(t, "PATCH", base, map[string]any{"gallery_open": true}, org.key), 200, "")
	now := time.Now().UTC()
	window := func(open, closeAt time.Time, eligibility string) {
		t.Helper()
		wantTS(t, "voting settings", a.api(t, "PUT", base+"/voting", map[string]any{
			"enabled": true, "confirm_vote_removal": true, "opens_at": open.Format(time.RFC3339),
			"closes_at": closeAt.Format(time.RFC3339), "eligibility": eligibility,
		}, org.key), 200, "")
	}
	vote := func(key, team string) resp {
		return a.api(t, "PUT", base+"/vote", map[string]string{"team": team}, key)
	}

	window(now.Add(-time.Hour), now.Add(time.Hour), "all_signed_in")
	view := a.api(t, "GET", base+"/vote", nil, "")
	wantTS(t, "public options", view, 200, "")
	if view.json(t)["status"] != "open" || !strings.Contains(string(view.body), alpha) || strings.Contains(string(view.body), "votes") {
		t.Fatalf("open vote response must list teams and hide tallies: %s", view.body)
	}
	wantTS(t, "own team", vote(p1.key, alpha), 409, "own_team_vote")
	if got := a.api(t, "GET", base+"/my-vote", nil, p1.key).json(t)["own_team"]; got != alpha {
		t.Fatalf("my-vote own_team = %v, want %s", got, alpha)
	}

	voter := a.newPerson(t, "rhpvoter")
	wantTS(t, "cast", vote(voter.key, alpha), 200, "")
	wantTS(t, "same vote", vote(voter.key, alpha), 409, "duplicate_vote")
	aliasKey, _ := auth.GenerateAPIKey()
	alias, err := accountrepo.CreateUser(t.Context(), a.database, strings.Replace(voter.email, "@", "+other@", 1), aliasKey, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = a.database.Exec(`DELETE FROM users WHERE id = $1`, alias.ID) })
	// Votes are per account (0029): a second account on the same mailbox votes too.
	wantTS(t, "mailbox alias votes", vote(aliasKey, alpha), 200, "")

	// Narrowing to members removes earlier votes and then admits members only.
	wantTS(t, "confirmation required", a.api(t, "PUT", base+"/voting", map[string]any{
		"enabled": true, "opens_at": now.Add(-time.Hour).Format(time.RFC3339), "closes_at": now.Add(time.Hour).Format(time.RFC3339), "eligibility": "event_members",
	}, org.key), 409, "vote_removal_confirmation")
	window(now.Add(-time.Hour), now.Add(time.Hour), "event_members")
	wantTS(t, "nonmember", vote(a.newPerson(t, "rhpvoteout").key, alpha), 403, "vote_ineligible")
	wantTS(t, "member", vote(p1.key, beta), 200, "")
	if _, err := a.database.Exec(`UPDATE event_members SET approval_status='pending' WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND user_id=$2`, slug, a.uid(t, p1)); err != nil {
		t.Fatal(err)
	}
	wantTS(t, "pending cannot change vote", vote(p1.key, alpha), 403, "vote_ineligible")
	if got := a.api(t, "GET", base+"/my-vote", nil, p1.key).json(t)["eligible"]; got != false {
		t.Fatalf("pending my-vote eligible: %v", got)
	}

	window(now.Add(time.Hour), now.Add(2*time.Hour), "all_signed_in")
	wantTS(t, "not open yet", vote(voter.key, beta), 409, "voting_closed")

	// Closed: tallies become public and writes stop.
	window(now.Add(-2*time.Hour), now.Add(-time.Hour), "all_signed_in")
	closed := a.api(t, "GET", base+"/vote", nil, "")
	wantTS(t, "closed results", closed, 200, "")
	if closed.json(t)["status"] != "closed" || !strings.Contains(string(closed.body), "votes") {
		t.Fatalf("closed results: %s", closed.body)
	}
	wantTS(t, "late vote", vote(voter.key, beta), 409, "voting_closed")
}
