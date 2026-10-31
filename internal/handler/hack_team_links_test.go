package handler

import (
	"net/http"
	"net/url"
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
			if got := h.teamCodeFromInput(c.in); got != c.want {
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

// The preview's applications_open follows the same rule as GET
// .../apply (organiser switch + directory listing + open/building stage +
// not taken down), not the raw per-event switch (Codex review, 2026-11-01):
// a fresh event defaults applications_open true but directory_listed
// false, so an unlisted event must still report false here.
func TestHackTeamLinkPreviewApplicationsOpenUsesEffectiveRule(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "tlappsorg")
	leader := a.newPerson(t, "tlappsleader")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)
	join, _ := a.joinCodes(t, org, slug)
	a.joinParticipant(t, join, leader, "Leader")
	_, code := a.createTeamAndJoin(t, slug, leader, "Alpha")
	base := "/v1/hack/events/" + slug
	r := a.at(t, "GET", "/v1/hack/team-links/"+code, nil, nil)
	if got := r.json(t)["event"].(map[string]any)["applications_open"]; got != false {
		t.Fatalf("unlisted event applications_open: %v", got)
	}
	wantCode(t, "list it", a.at(t, "PATCH", base+"/directory", map[string]any{"listed": true}, a.key(org)), 200, "")
	r = a.at(t, "GET", "/v1/hack/team-links/"+code, nil, nil)
	if got := r.json(t)["event"].(map[string]any)["applications_open"]; got != true {
		t.Fatalf("listed, open-stage event applications_open: %v", got)
	}
}

// teamCodeFromInput's "or a pasted link" path: a client (or the MCP tool's
// url.PathEscape) sends the whole team link, percent-encoded so it is one
// path segment on the wire; the server decodes it back to a path containing
// "/team/" and takes the last segment as the code.
func TestHackTeamLinkPreviewAcceptsFullLink(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "tlplinkorg")
	leader := a.newPerson(t, "tlplinkleader")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)
	join, _ := a.joinCodes(t, org, slug)
	a.joinParticipant(t, join, leader, "Leader")
	_, code := a.createTeamAndJoin(t, slug, leader, "Alpha")
	link := a.at(t, "GET", "/v1/hack/events/"+slug, nil, a.key(leader)).json(t)["me"].(map[string]any)["team"].(map[string]any)["link"].(string)
	if !strings.Contains(link, code) {
		t.Fatalf("link %q missing code %q", link, code)
	}
	r := a.at(t, "GET", "/v1/hack/team-links/"+url.PathEscape(link), nil, nil)
	if r.status != http.StatusOK {
		t.Fatalf("full-link preview: %d %s", r.status, r.body)
	}
	if r.json(t)["team"].(map[string]any)["name"] != "Alpha" {
		t.Fatalf("full-link preview resolved wrong team: %v", r.json(t))
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

func TestHackTeamLinkPreviewYouStatuses(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "tlyorg")
	leader := a.newPerson(t, "tlyleader")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)
	join, judge := a.joinCodes(t, org, slug)
	a.joinParticipant(t, join, leader, "Leader")
	_, code := a.createTeamAndJoin(t, slug, leader, "Alpha")

	previewAs := func(p person) map[string]any {
		t.Helper()
		return a.at(t, "GET", "/v1/hack/team-links/"+code, nil, a.key(p)).json(t)["you"].(map[string]any)
	}

	notInEvent := a.newPerson(t, "tlynotinevent")
	if got := previewAs(notInEvent)["status"]; got != "not_in_event" {
		t.Fatalf("not_in_event: %v", got)
	}

	pendingPerson := a.newPerson(t, "tlypending")
	a.joinParticipant(t, join, pendingPerson, "Pending")
	if _, err := a.database.Exec(`UPDATE event_members SET approval_status='pending' WHERE user_id=$1`, a.userID(t, pendingPerson)); err != nil {
		t.Fatal(err)
	}
	if got := previewAs(pendingPerson)["status"]; got != "pending" {
		t.Fatalf("pending: %v", got)
	}

	rejectedPerson := a.newPerson(t, "tlyrejected")
	a.joinParticipant(t, join, rejectedPerson, "Rejected")
	if _, err := a.database.Exec(`UPDATE event_members SET approval_status='rejected' WHERE user_id=$1`, a.userID(t, rejectedPerson)); err != nil {
		t.Fatal(err)
	}
	if got := previewAs(rejectedPerson)["status"]; got != "rejected" {
		t.Fatalf("rejected: %v", got)
	}

	judgePerson := a.newPerson(t, "tlyjudge")
	a.joinJudge(t, judge, judgePerson, "Judge")
	if got := previewAs(judgePerson)["status"]; got != "judge" {
		t.Fatalf("judge: %v", got)
	}

	if got := previewAs(org)["status"]; got != "organiser" {
		t.Fatalf("organiser: %v", got)
	}

	noTeam := a.newPerson(t, "tlynoteam")
	a.joinParticipant(t, join, noTeam, "NoTeam")
	if got := previewAs(noTeam)["status"]; got != "no_team" {
		t.Fatalf("no_team: %v", got)
	}

	if got := previewAs(leader)["status"]; got != "on_this_team" {
		t.Fatalf("on_this_team: %v", got)
	}

	otherMember := a.newPerson(t, "tlyother")
	a.joinParticipant(t, join, otherMember, "OtherMember")
	a.createTeamAndJoin(t, slug, otherMember, "Beta")
	you := previewAs(otherMember)
	if you["status"] != "on_other_team" || you["team_name"] != "Beta" {
		t.Fatalf("on_other_team: %v", you)
	}
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
