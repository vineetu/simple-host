package teams

import (
	"net/http"
	"strings"
	"testing"

	hacktest "github.com/vsriram/simple-host/products/hack/hacktest"
)

// GET /v1/hack/team-links/{code}: the public team link preview (teams
// redesign, owner 2026-10-31). The harness is newHackApp (DB_DSN).

func TestHackTeamLinkPreviewSignedOutShape(t *testing.T) {
	a := newHackApp(t)
	org := a.NewPerson(t, "tlporg")
	leader := a.NewPerson(t, "tlpleader")
	slug := hacktest.UniqueSlug()
	a.CreateEvent(t, org, slug, nil)
	a.OpenEvent(t, org, slug)
	join, _ := a.JoinCodes(t, org, slug)
	a.JoinParticipant(t, join, leader, "Leader Name")
	_, code := a.CreateTeamAndJoin(t, slug, leader, "Alpha")
	r := a.At(t, "GET", "/v1/hack/team-links/"+code, nil, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("preview: %d %s", r.Status, r.Body)
	}
	if strings.Contains(string(r.Body), "Leader Name") || strings.Contains(string(r.Body), leader.Email) {
		t.Fatalf("preview leaked a member name or email: %s", r.Body)
	}
	body := r.JSON(t)
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
	if a.TeamOf(t, leader) == "" {
		t.Fatal("leader lost their team")
	}
	var members int
	if err := a.DB.QueryRow(`SELECT count(*) FROM event_members WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND team_id IS NOT NULL`, slug).Scan(&members); err != nil || members != 1 {
		t.Fatalf("GET changed membership: %d %v", members, err)
	}
}

func TestHackTeamLinkRateLimit(t *testing.T) {
	a := newHackApp(t)
	limited := 0
	for i := 0; i < 150; i++ {
		rr := a.At(t, "GET", "/v1/hack/team-links/zzzzzzzz", nil, nil)
		if rr.Status == http.StatusTooManyRequests && rr.JSON(t)["code"] == "rate_limited" {
			limited++
		}
	}
	if limited == 0 {
		t.Fatal("expected rate_limited")
	}
}
