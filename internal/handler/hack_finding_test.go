package handler

import (
	"os"
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
	wantTS(t, "off removal", a.api(t, "DELETE", base+"/team/members/"+a.uid(t, q), nil, p.key), 409, "team_finding_off")
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
	for i, p := range people {
		var placed bool
		if err := a.database.QueryRow("SELECT team_id IS NOT NULL FROM event_members WHERE user_id=$1", a.uid(t, p)).Scan(&placed); err != nil || placed != (i < 4) {
			t.Fatalf("person %d placed=%v err=%v", i, placed, err)
		}
	}
	var closed int
	if err := a.database.QueryRow("SELECT count(*) FROM event_teams WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND NOT open", slug).Scan(&closed); err != nil || closed != 0 {
		t.Fatalf("fill teams closed=%d err=%v", closed, err)
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

// Grok retest 3, Confusing #3: a code-only team ("Only people with the
// code") with room must be left alone by fill-teams -- its members chose
// not to take strangers -- but the preview response needs to say why,
// instead of silently starting a one-person "Team N" while Beta Builders
// still had a seat free.
func TestHackFillTeamsSkipsCodeOnly(t *testing.T) {
	a := newTeamSiteApp(t)
	org := a.newPerson(t, "codeonlyorg")
	leader := a.newPerson(t, "codeonlyleader")
	slug := a.makeEvent(t, org)
	base := "/v1/hack/events/" + slug
	a.join(t, slug, leader, org)
	team, _ := a.startTeam(t, slug, "Beta Builders", leader)
	wantTS(t, "max", a.api(t, "PATCH", base, map[string]any{"team_size_max": 3}, org.key), 200, "")
	wantTS(t, "code only", a.api(t, "PUT", base+"/team/finding", map[string]any{"open": false, "looking_for": ""}, leader.key), 200, "")
	solo := a.newPerson(t, "codeonlysolo")
	a.join(t, slug, solo, org)
	preview := a.api(t, "POST", base+"/teams/fill", map[string]any{"apply": false}, org.key)
	wantTS(t, "preview", preview, 200, "")
	body := preview.json(t)
	plan := body["plan"].([]any)
	if len(plan) != 1 || plan[0].(map[string]any)["team"] == team {
		t.Fatalf("expected a new team, not Beta Builders: %v", plan)
	}
	if plan[0].(map[string]any)["new"] != true {
		t.Fatalf("expected the lone solo to start a new team: %v", plan)
	}
	skipped := body["skipped"].([]any)
	if len(skipped) != 1 {
		t.Fatalf("skipped %v", skipped)
	}
	s := skipped[0].(map[string]any)
	if s["team"] != team || s["reason"] != "code_only" {
		t.Fatalf("skipped entry %v", s)
	}
}

func TestHackFindingCrossedOffersAndRemoval(t *testing.T) {
	a := newTeamSiteApp(t)
	org := a.newPerson(t, "crossorg")
	leader := a.newPerson(t, "crossleader")
	slug := a.makeEvent(t, org)
	base := "/v1/hack/events/" + slug
	a.join(t, slug, leader, org)
	team, _ := a.startTeam(t, slug, "Cross", leader)
	for _, inviteFirst := range []bool{false, true} {
		p := a.newPerson(t, "crosssolo")
		a.join(t, slug, p, org)
		wantTS(t, "looking", a.api(t, "PUT", base+"/me/looking", map[string]any{"looking": true}, p.key), 200, "")
		ask := func(want int) {
			wantTS(t, "ask", a.api(t, "POST", base+"/teams/"+team+"/requests", nil, p.key), want, "")
		}
		invite := func(want int) {
			wantTS(t, "invite", a.api(t, "POST", base+"/team/invites", map[string]any{"user_id": a.uid(t, p)}, leader.key), want, "")
		}
		if inviteFirst {
			invite(201)
			invite(200)
			ask(200)
		} else {
			ask(201)
			ask(200)
			invite(200)
		}
		var joined bool
		if err := a.database.QueryRow("SELECT team_id IS NOT NULL FROM event_members WHERE user_id=$1", a.uid(t, p)).Scan(&joined); err != nil || !joined {
			t.Fatalf("crossed join %v %v", joined, err)
		}
		var offers int
		if err := a.database.QueryRow("SELECT count(*) FROM event_team_requests WHERE user_id=$1", a.uid(t, p)).Scan(&offers); err != nil || offers != 0 {
			t.Fatalf("crossed cleanup %d %v", offers, err)
		}
	}
	p := a.newPerson(t, "removedsolo")
	a.join(t, slug, p, org)
	wantTS(t, "ask before removal", a.api(t, "POST", base+"/teams/"+team+"/requests", nil, p.key), 201, "")
	wantTS(t, "remove person", a.api(t, "DELETE", base+"/people/"+a.uid(t, p), nil, org.key), 204, "")
	var n int
	if err := a.database.QueryRow("SELECT count(*) FROM event_team_requests WHERE user_id=$1", a.uid(t, p)).Scan(&n); err != nil || n != 0 {
		t.Fatalf("removed offers %d %v", n, err)
	}
}

func TestHackFindingDraft(t *testing.T) {
	a := newTeamSiteApp(t)
	org := a.newPerson(t, "draftfindingorg")
	leader := a.newPerson(t, "draftleader")
	p := a.newPerson(t, "draftsolo")
	slug := a.makeEvent(t, org)
	base := "/v1/hack/events/" + slug
	a.join(t, slug, leader, org)
	a.join(t, slug, p, org)
	team, _ := a.startTeam(t, slug, "Draft team", leader)
	wantTS(t, "looking", a.api(t, "PUT", base+"/me/looking", map[string]any{"looking": true}, p.key), 200, "")
	wantTS(t, "ask", a.api(t, "POST", base+"/teams/"+team+"/requests", nil, p.key), 201, "")
	pending := a.api(t, "GET", base+"/teams/open", nil, p.key).json(t)["pending"].([]any)[0].(map[string]any)["id"].(string)
	if _, err := a.database.Exec("UPDATE events SET stage='draft' WHERE slug=$1", slug); err != nil {
		t.Fatal(err)
	}
	wantTS(t, "draft ask", a.api(t, "POST", base+"/teams/"+team+"/requests", nil, p.key), 409, "teams_locked")
	wantTS(t, "draft invite", a.api(t, "POST", base+"/team/invites", map[string]any{"user_id": a.uid(t, p)}, leader.key), 409, "teams_locked")
	wantTS(t, "draft accept", a.api(t, "POST", base+"/requests/"+pending+"/accept", nil, leader.key), 409, "teams_locked")
	wantTS(t, "draft fill preview", a.api(t, "POST", base+"/teams/fill", map[string]any{"apply": false}, org.key), 409, "teams_locked")
	wantTS(t, "draft fill apply", a.api(t, "POST", base+"/teams/fill", map[string]any{"apply": true}, org.key), 409, "teams_locked")
	wantTS(t, "draft remove", a.api(t, "DELETE", base+"/team/members/"+a.uid(t, p), nil, leader.key), 403, "")
}

func TestHackFindingMigrationKeepsExistingTeamsClosed(t *testing.T) {
	a := newTeamSiteApp(t)
	migration, err := os.ReadFile("../../db/migrations/zl-hack-team-finding.sql")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := a.database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec("CREATE TEMP TABLE finding_migration_teams(id integer); INSERT INTO finding_migration_teams VALUES(1)"); err != nil {
		t.Fatal(err)
	}
	var statement string
	for _, line := range strings.Split(string(migration), "\n") {
		if strings.Contains(line, "ADD COLUMN IF NOT EXISTS open ") {
			statement = strings.Replace(line, "event_teams", "finding_migration_teams", 1)
		}
	}
	if statement == "" {
		t.Fatal("missing open migration")
	}
	for i := 0; i < 2; i++ {
		if _, err = tx.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	var open bool
	if err = tx.QueryRow("SELECT open FROM finding_migration_teams WHERE id=1").Scan(&open); err != nil || open {
		t.Fatalf("existing team open=%v err=%v", open, err)
	}
	if _, err = tx.Exec("INSERT INTO finding_migration_teams(id) VALUES(2)"); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow("SELECT open FROM finding_migration_teams WHERE id=2").Scan(&open); err != nil || open {
		t.Fatalf("default open=%v err=%v", open, err)
	}
}

func TestHackFillTeamsExtendedDeadline(t *testing.T) {
	a := newTeamSiteApp(t)
	org := a.newPerson(t, "extendfillorg")
	leader := a.newPerson(t, "extendleader")
	slug := a.makeEvent(t, org)
	base := "/v1/hack/events/" + slug
	a.join(t, slug, leader, org)
	team, _ := a.startTeam(t, slug, "Extended", leader)
	wantTS(t, "limit", a.api(t, "PATCH", base, map[string]any{"team_size_max": 2}, org.key), 200, "")
	people := []person{a.newPerson(t, "extendone"), a.newPerson(t, "extendtwo")}
	for _, p := range people {
		a.join(t, slug, p, org)
	}
	if _, err := a.database.Exec("UPDATE events SET submission_deadline=clock_timestamp()-interval '1 hour' WHERE slug=$1", slug); err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.Exec("UPDATE event_teams SET deadline_override=clock_timestamp()+interval '1 hour' WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND slug=$2", slug, team); err != nil {
		t.Fatal(err)
	}
	preview := a.api(t, "POST", base+"/teams/fill", map[string]any{"apply": false}, org.key)
	wantTS(t, "extended preview", preview, 200, "")
	plan := preview.json(t)["plan"].([]any)
	if len(plan) != 1 || plan[0].(map[string]any)["team"] != team || plan[0].(map[string]any)["new"] != false {
		t.Fatalf("extended plan %v", plan)
	}
	wantTS(t, "extended apply", a.api(t, "POST", base+"/teams/fill", map[string]any{"apply": true}, org.key), 200, "")
	var teams, placed int
	if err := a.database.QueryRow("SELECT count(*) FROM event_teams WHERE event_id=(SELECT id FROM events WHERE slug=$1)", slug).Scan(&teams); err != nil || teams != 1 {
		t.Fatalf("teams %d %v", teams, err)
	}
	if err := a.database.QueryRow("SELECT count(*) FROM event_members WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND team_id IS NOT NULL", slug).Scan(&placed); err != nil || placed != 2 {
		t.Fatalf("placed %d %v", placed, err)
	}
}
