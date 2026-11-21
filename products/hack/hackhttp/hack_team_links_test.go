package hackhttp

import (
	"net/http"
	"strings"
	"testing"
)

// GET /v1/hack/team-links/{code}: the public team link preview (teams
// redesign, owner 2026-10-31). The harness is newHackApp (DB_DSN).

// teamCodeFromInput needs no database; a pasted link's query/fragment must
// be stripped before trailing slashes are trimmed, not after (Codex
// review, 2026-11-01): ".../abcd1234/?source=chat" has no trailing slash
// of its own, but removing "?source=chat" leaves one behind
// (".../abcd1234/"), and trimming only happened first in the old code, so
// the last segment came out empty.
func TestTeamCodeFromInputTrailingSlashAndQuery(t *testing.T) {
	h := NewHackHandler(nil, "https://simple-hack.app", "simple-hack.test")
	cases := []struct{ name, in, want string }{
		{"bare code", "abcd1234", "abcd1234"},
		{"link, no trailing slash", "https://simple-hack.app/team/abcd1234", "abcd1234"},
		{"link, trailing slash", "https://simple-hack.app/team/abcd1234/", "abcd1234"},
		{"link, query, no trailing slash", "https://simple-hack.app/team/abcd1234?source=chat", "abcd1234"},
		{"link, trailing slash then query", "https://simple-hack.app/team/abcd1234/?source=chat", "abcd1234"},
		{"link, fragment", "https://simple-hack.app/team/abcd1234#x", "abcd1234"},
		{"link, trailing slash then fragment", "https://simple-hack.app/team/abcd1234/#x", "abcd1234"},
		{"bare path, trailing slash then query", "/team/abcd1234/?source=chat", "abcd1234"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := h.links.teamCodeFromInput(c.in); got != c.want {
				t.Fatalf("%q: got %q want %q", c.in, got, c.want)
			}
		})
	}
}

func TestHackTeamLinkPreviewSignedOutShape(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "tlporg")
	leader := a.newPerson(t, "tlpleader")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)
	join, _ := a.joinCodes(t, org, slug)
	a.joinParticipant(t, join, leader, "Leader Name")
	_, code := a.createTeamAndJoin(t, slug, leader, "Alpha")
	r := a.at(t, "GET", "/v1/hack/team-links/"+code, nil, nil)
	if r.status != http.StatusOK {
		t.Fatalf("preview: %d %s", r.status, r.body)
	}
	if strings.Contains(string(r.body), "Leader Name") || strings.Contains(string(r.body), leader.email) {
		t.Fatalf("preview leaked a member name or email: %s", r.body)
	}
	body := r.json(t)
	ev := body["event"].(map[string]any)
	if ev["slug"] != slug || ev["stage"] != "open" {
		t.Fatalf("event shape: %v", ev)
	}
	team := body["team"].(map[string]any)
	if team["name"] != "Alpha" || team["member_count"] != float64(1) {
		t.Fatalf("team shape: %v", team)
	}
	if body["joinable"] != true || body["reason"] != nil {
		t.Fatalf("joinable/reason: %v %v", body["joinable"], body["reason"])
	}
	if body["you"] != nil {
		t.Fatalf("signed out you should be nil: %v", body["you"])
	}
	// GET never joins.
	if a.teamOf(t, leader) == "" {
		t.Fatal("leader lost their team")
	}
	var members int
	if err := a.database.QueryRow(`SELECT count(*) FROM event_members WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND team_id IS NOT NULL`, slug).Scan(&members); err != nil || members != 1 {
		t.Fatalf("GET changed membership: %d %v", members, err)
	}
}

func TestHackTeamLinkPreviewReasons(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "tlrorg")
	leader := a.newPerson(t, "tlrleader")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)
	join, _ := a.joinCodes(t, org, slug)
	a.joinParticipant(t, join, leader, "Leader")
	teamID, code := a.createTeamAndJoin(t, slug, leader, "Alpha")
	base := "/v1/hack/events/" + slug

	// Full.
	wantCode(t, "max 1", a.at(t, "PATCH", base, map[string]any{"team_size_max": 1}, a.key(org)), 200, "")
	r := a.at(t, "GET", "/v1/hack/team-links/"+code, nil, nil)
	if r.json(t)["reason"] != "full" || r.json(t)["joinable"] != false {
		t.Fatalf("full: %v", r.json(t))
	}
	wantCode(t, "restore max", a.at(t, "PATCH", base, map[string]any{"team_size_max": 4}, a.key(org)), 200, "")

	// Locked.
	wantCode(t, "lock", a.at(t, "PUT", base+"/team/lock", map[string]any{"locked": true}, a.key(leader)), 200, "")
	r = a.at(t, "GET", "/v1/hack/team-links/"+code, nil, nil)
	if r.json(t)["reason"] != "locked" {
		t.Fatalf("locked: %v", r.json(t))
	}
	wantCode(t, "unlock", a.at(t, "PUT", base+"/team/lock", map[string]any{"locked": false}, a.key(leader)), 200, "")

	// Teams closed: stage not open/building.
	wantCode(t, "to closed", a.at(t, "POST", base+"/stage", map[string]string{"stage": "closed"}, a.key(org)), 200, "")
	r = a.at(t, "GET", "/v1/hack/team-links/"+code, nil, nil)
	if r.json(t)["reason"] != "teams_closed" {
		t.Fatalf("stage closed: %v", r.json(t))
	}
	wantCode(t, "back to open", a.at(t, "POST", base+"/stage", map[string]string{"stage": "open"}, a.key(org)), 200, "")

	// Teams closed: the team's own deadline passed.
	if _, err := a.database.Exec(`UPDATE events SET submission_deadline = clock_timestamp() - interval '1 hour' WHERE slug = $1`, slug); err != nil {
		t.Fatal(err)
	}
	r = a.at(t, "GET", "/v1/hack/team-links/"+code, nil, nil)
	if r.json(t)["reason"] != "teams_closed" {
		t.Fatalf("deadline passed: %v", r.json(t))
	}
	_ = teamID
}

func TestHackTeamLinkTakenDownAndUnknown(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "tltdorg")
	leader := a.newPerson(t, "tltdleader")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)
	join, _ := a.joinCodes(t, org, slug)
	a.joinParticipant(t, join, leader, "Leader")
	_, code := a.createTeamAndJoin(t, slug, leader, "Alpha")
	wantCode(t, "unknown code", a.at(t, "GET", "/v1/hack/team-links/doesnotexist", nil, nil), 404, "team_link_not_found")
	wantCode(t, "take down", a.at(t, "POST", "/v1/admin/hack/events/"+slug+"/takedown", map[string]string{"reason": "test"}, a.adminH()), 200, "")
	wantCode(t, "taken down team link", a.at(t, "GET", "/v1/hack/team-links/"+code, nil, nil), 404, "team_link_not_found")
}

func TestHackTeamLinkRateLimit(t *testing.T) {
	a := newHackApp(t)
	limited := 0
	for i := 0; i < 150; i++ {
		rr := a.at(t, "GET", "/v1/hack/team-links/zzzzzzzz", nil, nil)
		if rr.status == http.StatusTooManyRequests && rr.json(t)["code"] == "rate_limited" {
			limited++
		}
	}
	if limited == 0 {
		t.Fatal("expected rate_limited")
	}
}
