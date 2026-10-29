package handler

import (
	"strings"
	"testing"
)

func TestHackTeamFinding(t *testing.T) {
	a := newTeamSiteApp(t)
	org := a.newPerson(t, "findingorg")
	leader := a.newPerson(t, "leader")
	other := a.newPerson(t, "otherleader")
	p := a.newPerson(t, "solo")
	q := a.newPerson(t, "invitee")
	stranger := a.newPerson(t, "stranger")
	slug := a.makeEvent(t, org)
	base := "/v1/hack/events/" + slug
	for _, person := range []person{leader, other, p, q} {
		a.join(t, slug, person, org)
	}
	team, code := a.startTeam(t, slug, "Alpha", leader)
	second, _ := a.startTeam(t, slug, "Beta", other)
	wantTS(t, "stranger list", a.api(t, "GET", base+"/teams/open", nil, stranger.key), 404, "")
	wantTS(t, "intro", a.api(t, "PUT", base+"/me/looking", map[string]any{"looking": true, "intro": "I design"}, p.key), 200, "")
	wantTS(t, "request", a.api(t, "POST", base+"/teams/"+team+"/requests", nil, p.key), 201, "")
	wantTS(t, "second request", a.api(t, "POST", base+"/teams/"+second+"/requests", nil, p.key), 201, "")
	findID := func(key, kind string) string {
		t.Helper()
		r := a.api(t, "GET", base+"/teams/open", nil, key)
		wantTS(t, "list", r, 200, "")
		for _, v := range r.json(t)["pending"].([]any) {
			m := v.(map[string]any)
			if m["kind"] == kind {
				return m["id"].(string)
			}
		}
		t.Fatal("missing pending offer")
		return ""
	}
	id := findID(leader.key, "request")
	wantTS(t, "outsider accept", a.api(t, "POST", base+"/requests/"+id+"/accept", nil, other.key), 403, "team_action_unavailable")
	wantTS(t, "full size", a.api(t, "PATCH", base, map[string]any{"team_size_max": 1}, org.key), 200, "")
	wantTS(t, "full cannot accept", a.api(t, "POST", base+"/requests/"+id+"/accept", nil, leader.key), 409, "team_full")
	wantTS(t, "room", a.api(t, "PATCH", base, map[string]any{"team_size_max": 4}, org.key), 200, "")
	wantTS(t, "accept request", a.api(t, "POST", base+"/requests/"+id+"/accept", nil, leader.key), 200, "")
	var count int
	var looking bool
	if err := a.database.QueryRow(`SELECT count(*) FROM event_team_requests WHERE user_id=$1`, a.uid(t, p)).Scan(&count); err != nil || count != 0 {
		t.Fatalf("pending cleanup: %d, %v", count, err)
	}
	if err := a.database.QueryRow(`SELECT looking FROM event_members WHERE user_id=$1`, a.uid(t, p)).Scan(&looking); err != nil || looking {
		t.Fatalf("solo cleanup: %v, %v", looking, err)
	}
	wantTS(t, "cannot remove self", a.api(t, "DELETE", base+"/team/members/"+a.uid(t, p), nil, p.key), 403, "")
	wantTS(t, "teammate removal", a.api(t, "DELETE", base+"/team/members/"+a.uid(t, leader), nil, p.key), 204, "")
	notice := a.api(t, "GET", base, nil, leader.key).json(t)["me"].(map[string]any)["team_removal_notice"].(string)
	if !strings.Contains(notice, "took you off Alpha") {
		t.Fatalf("notice %q", notice)
	}
	wantTS(t, "old code rotated", a.api(t, "POST", base+"/teams/join", map[string]any{"code": code}, leader.key), 404, "team_not_found")
	wantTS(t, "invite listing", a.api(t, "PUT", base+"/me/looking", map[string]any{"looking": true, "intro": "I build"}, q.key), 200, "")
	wantTS(t, "invite", a.api(t, "POST", base+"/team/invites", map[string]any{"user_id": a.uid(t, q)}, p.key), 201, "")
	invite := findID(q.key, "invite")
	wantTS(t, "accept invite", a.api(t, "POST", base+"/requests/"+invite+"/accept", nil, q.key), 200, "")
	joined := a.api(t, "GET", base, nil, q.key).json(t)["me"].(map[string]any)["team"].(map[string]any)
	if joined["slug"] != team {
		t.Fatalf("invite joined %v", joined)
	}
	wantTS(t, "full cannot invite", a.api(t, "PATCH", base, map[string]any{"team_size_max": 2}, org.key), 200, "")
	wantTS(t, "leader relists", a.api(t, "PUT", base+"/me/looking", map[string]any{"looking": true, "intro": "Back"}, leader.key), 200, "")
	wantTS(t, "full invite refused", a.api(t, "POST", base+"/team/invites", map[string]any{"user_id": a.uid(t, leader)}, p.key), 409, "team_full")
	wantTS(t, "restore max", a.api(t, "PATCH", base, map[string]any{"team_size_max": 4}, org.key), 200, "")
	// Code joining also clears outstanding requests and the solo listing.
	wantTS(t, "leader looking", a.api(t, "PUT", base+"/me/looking", map[string]any{"looking": true, "intro": "Back"}, leader.key), 200, "")
	wantTS(t, "leader asks", a.api(t, "POST", base+"/teams/"+second+"/requests", nil, leader.key), 201, "")
	newCode := a.api(t, "GET", base, nil, p.key).json(t)["me"].(map[string]any)["team"].(map[string]any)["code"]
	wantTS(t, "code join", a.api(t, "POST", base+"/teams/join", map[string]any{"code": newCode}, leader.key), 200, "")
	if err := a.database.QueryRow(`SELECT count(*) FROM event_team_requests WHERE user_id=$1`, a.uid(t, leader)).Scan(&count); err != nil || count != 0 {
		t.Fatalf("code cleanup: %d %v", count, err)
	}
	wantTS(t, "off", a.api(t, "PATCH", base, map[string]any{"team_finding": false}, org.key), 200, "")
	hidden := a.api(t, "GET", base+"/teams/open", nil, other.key).json(t)
	if hidden["enabled"] != false || len(hidden["teams"].([]any)) != 0 || len(hidden["solo"].([]any)) != 0 || len(hidden["pending"].([]any)) != 0 {
		t.Fatalf("off lists: %v", hidden)
	}
	wantTS(t, "off request", a.api(t, "POST", base+"/teams/"+team+"/requests", nil, other.key), 409, "team_finding_off")
	wantTS(t, "off invite", a.api(t, "POST", base+"/team/invites", map[string]any{"user_id": a.uid(t, other)}, p.key), 409, "team_finding_off")
}

func TestHackFillTeams(t *testing.T) {
	a := newTeamSiteApp(t)
	org := a.newPerson(t, "fillorg")
	leader := a.newPerson(t, "fillleader")
	slug := a.makeEvent(t, org)
	base := "/v1/hack/events/" + slug
	a.join(t, slug, leader, org)
	team, _ := a.startTeam(t, slug, "Team 1", leader)
	wantTS(t, "max", a.api(t, "PATCH", base, map[string]any{"team_size_max": 2}, org.key), 200, "")
	people := []person{}
	for i := 0; i < 5; i++ {
		p := a.newPerson(t, "fillsolo")
		people = append(people, p)
		a.join(t, slug, p, org)
	}
	wantTS(t, "fill request", a.api(t, "POST", base+"/teams/"+team+"/requests", nil, people[0].key), 201, "")
	wantTS(t, "fill solo listing", a.api(t, "PUT", base+"/me/looking", map[string]any{"looking": true, "intro": "Hello"}, people[0].key), 200, "")
	// Pending participants must remain unplaced.
	if _, err := a.database.Exec(`UPDATE event_members SET approval_status='pending' WHERE user_id=$1`, a.uid(t, people[4])); err != nil {
		t.Fatal(err)
	}
	wantTS(t, "participant fill refused", a.api(t, "POST", base+"/teams/fill", map[string]any{"apply": false}, leader.key), 404, "")
	preview := a.api(t, "POST", base+"/teams/fill", map[string]any{"apply": false}, org.key)
	wantTS(t, "preview", preview, 200, "")
	plan := preview.json(t)["plan"].([]any)
	if len(plan) != 3 || plan[0].(map[string]any)["team"] != team || plan[1].(map[string]any)["name"] != "Team 2" {
		t.Fatalf("plan %v", plan)
	}
	var n int
	if err := a.database.QueryRow(`SELECT count(*) FROM event_members WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND team_id IS NOT NULL`, slug).Scan(&n); err != nil || n != 1 {
		t.Fatalf("preview mutated: %d %v", n, err)
	}
	wantTS(t, "apply", a.api(t, "POST", base+"/teams/fill", map[string]any{"apply": true}, org.key), 200, "")
	if err := a.database.QueryRow(`SELECT count(*) FROM event_members WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND team_id IS NOT NULL`, slug).Scan(&n); err != nil || n != 5 {
		t.Fatalf("placed: %d %v", n, err)
	}
	var offers int
	if err := a.database.QueryRow(`SELECT count(*) FROM event_team_requests WHERE event_id=(SELECT id FROM events WHERE slug=$1)`, slug).Scan(&offers); err != nil || offers != 0 {
		t.Fatalf("fill cleanup: %d %v", offers, err)
	}
	again := a.api(t, "POST", base+"/teams/fill", map[string]any{"apply": false}, org.key).json(t)
	if len(again["plan"].([]any)) != 0 {
		t.Fatalf("remaining plan %v", again)
	}
}
