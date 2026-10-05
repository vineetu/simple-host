package handler

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/db"
)

func TestHackDeleteBusyEvent(t *testing.T) {
	a := newTeamSiteApp(t)
	RegisterHackPublic(a.mux, a.database, a.srv.URL, a.sites.TeamSiteURL, a.sites.TeamSitesReady)
	org, co, participant, judge, outsider := a.newPerson(t, "del-org"), a.newPerson(t, "del-co"), a.newPerson(t, "del-part"), a.newPerson(t, "del-judge"), a.newPerson(t, "del-outside")
	slug := a.makeEvent(t, org)
	base := "/v1/hack/events/" + slug
	t.Cleanup(func() { _, _ = a.database.Exec(`DELETE FROM event_used_names WHERE event_slug=$1`, slug) })
	a.join(t, slug, participant, org)
	info := a.api(t, "GET", base, nil, org.key).json(t)["organiser"].(map[string]any)
	judgeCode, joinCode := info["judge_code"].(string), info["join_code"].(string)
	wantTS(t, "join judge", a.api(t, "POST", "/v1/hack/judge/"+judgeCode, map[string]any{"accept_coc": true, "display_name": "Judge"}, judge.key), 200, "")
	ev, err := db.GetEventBySlug(context.Background(), a.database, slug)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.InsertEventMember(context.Background(), a.database, ev.ID, a.uid(t, co), "organiser", "Co"); err != nil {
		t.Fatal(err)
	}
	team, _ := a.startTeam(t, slug, "Delete team", participant)
	markReady(t, a.certDir, slug)
	key := a.teamKey(t, slug, participant)
	wantTS(t, "team deploy", a.deployTeam(t, team, key, "delete site"), 201, "")
	wantTS(t, "entry", a.api(t, "PUT", base+"/entry", map[string]string{"title": "Busy entry"}, participant.key), 200, "")
	wantTS(t, "custom site", a.api(t, "PUT", base+"/website/files?create=1", deployBody("delete custom"), org.key), 201, "")
	for _, storage := range []struct{ base, key string }{{"/v1/sites/" + team + "/storage", key}, {base + "/website/storage", org.key}} {
		wantTS(t, "resource", a.api(t, "PUT", storage.base+"/resources/prefs", map[string]string{"kind": "kv", "read": "anyone", "write": "owner"}, storage.key), 201, "")
		wantTS(t, "value", a.api(t, "PUT", storage.base+"/kv/prefs/keys/theme", `{"value":"red"}`, storage.key), 200, "")
		wantTS(t, "files resource", a.api(t, "PUT", storage.base+"/resources/photos", map[string]string{"kind": "files", "read": "owner", "write": "owner"}, storage.key), 201, "")
	}
	var teamID string
	if err := a.database.QueryRow(`SELECT id FROM event_teams WHERE event_id=$1 AND slug=$2`, ev.ID, team).Scan(&teamID); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO event_scores(event_id,judge_id,team_id,criterion_id,points) SELECT $1,$2,$3,id,4 FROM rubric_criteria WHERE event_id=$1`,
		`INSERT INTO event_votes(event_id,voter_email,team_id) SELECT $1,'delete-voter@example.com',$3 WHERE $2::uuid IS NOT NULL`,
		`INSERT INTO event_results(event_id,published_by,snapshot) SELECT $1,$2,'[]'::jsonb WHERE $3::uuid IS NOT NULL`,
	} {
		if _, err := a.database.Exec(statement, ev.ID, a.uid(t, judge), teamID); err != nil {
			t.Fatal(err)
		}
	}
	// Runtime bytes must go with the account, as well as PostgreSQL resources.
	runtime := filepath.Join(a.sites.disk.SiteDir(ev.AccountID, team), "runtime", "photos")
	if err := os.MkdirAll(runtime, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtime, "image.bin"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, who := range []person{outsider, participant, judge} {
		wantTS(t, "wrong role refused", a.api(t, "DELETE", base, map[string]string{"confirm": slug}, who.key), 404, "event_not_found")
	}
	for _, body := range []any{nil, map[string]string{"confirm": "wrong"}, map[string]string{"confirm": strings.ToUpper(slug)}} {
		r := a.api(t, "DELETE", base, body, org.key)
		wantTS(t, "confirmation required", r, 409, "delete_only_empty")
		if !strings.Contains(string(r.body), slug) {
			t.Fatal("missing required confirmation in error")
		}
	}
	// Deletion is available even after archive, with a co-organiser identity.
	wantTS(t, "archive", a.api(t, "POST", base+"/stage", map[string]string{"stage": "archived"}, org.key), 200, "")
	wantTS(t, "ended confirmation required", a.api(t, "DELETE", base, nil, co.key), 409, "delete_only_empty")
	wantTS(t, "confirmed deletion", a.api(t, "DELETE", base, map[string]string{"confirm": slug}, co.key), 204, "")
	for _, who := range []person{org, co, participant, judge} {
		wantTS(t, "access lost", a.api(t, "GET", base, nil, who.key), 404, "event_not_found")
		list := a.api(t, "GET", "/v1/hack/events", nil, who.key)
		if list.status != 200 || strings.Contains(string(list.body), slug) {
			t.Fatalf("Your events retains deletion: %d", list.status)
		}
	}
	dir := a.api(t, "GET", "/v1/hack/directory", nil, "")
	if dir.status != 200 || strings.Contains(string(dir.body), slug) {
		t.Fatal("directory retains deletion")
	}
	for _, path := range []string{base + "/public", base + "/results", base + "/vote"} {
		wantTS(t, "public data gone", a.api(t, "GET", path, nil, ""), 404, "event_not_found")
	}
	for _, host := range []string{slug + "." + tsDomain, team + "." + slug + "." + tsDomain} {
		r := a.on(t, "GET", host, "/", nil, "")
		if r.status != 404 || strings.Contains(string(r.body), "delete custom") || strings.Contains(string(r.body), "delete site") {
			t.Fatalf("host still live: %d", r.status)
		}
	}
	r := a.api(t, "GET", "/e/"+slug, nil, "")
	if r.status != 404 || !strings.Contains(r.header.Get("Content-Type"), "text/html") || !strings.Contains(string(r.body), "Event not found") {
		t.Fatalf("old page: %d %s", r.status, r.body)
	}
	for _, path := range []string{"/v1/hack/join/" + joinCode, "/v1/hack/judge/" + judgeCode} {
		wantTS(t, "old invitation", a.api(t, "GET", path, nil, ""), 404, "invalid_code")
	}
	wantTS(t, "team key revoked", a.api(t, "GET", "/v1/sites", nil, key), 401, "")
	name := a.api(t, "GET", "/v1/hack/names/"+slug, nil, org.key).json(t)
	if name["available"] != false || name["code"] != "name_taken" {
		t.Fatalf("slug free: %v", name)
	}
	for _, table := range []string{"event_members", "event_teams", "event_entries", "event_scores", "event_votes", "event_results"} {
		var count int
		if err := a.database.QueryRow(`SELECT count(*) FROM `+table+` WHERE event_id=$1`, ev.ID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s not erased: %d %v", table, count, err)
		}
	}
	var count int
	if err := a.database.QueryRow(`SELECT count(*) FROM sites WHERE user_id=$1`, ev.AccountID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("sites left: %v", err)
	}
	if err := a.database.QueryRow(`SELECT count(*) FROM users WHERE id=$1`, ev.AccountID).Scan(&count); err != nil || count != 0 {
		t.Fatal("holding account left")
	}
	for _, path := range []string{filepath.Dir(a.sites.disk.SiteDir(ev.AccountID, team)), a.sites.disk.TrashDir(ev.AccountID, "unused")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("files left at %s: %v", path, err)
		}
	}
}

func TestHackDeleteJoinedNameWithoutTeams(t *testing.T) {
	for _, role := range []string{"participant", "judge"} {
		t.Run(role, func(t *testing.T) {
			a := newTeamSiteApp(t)
			org, p := a.newPerson(t, "name-org"), a.newPerson(t, "name-person")
			slug := a.makeEvent(t, org)
			base := "/v1/hack/events/" + slug
			t.Cleanup(func() { _, _ = a.database.Exec(`DELETE FROM event_used_names WHERE event_slug=$1`, slug) })
			if role == "participant" {
				a.join(t, slug, p, org)
			} else {
				code := a.api(t, "GET", base, nil, org.key).json(t)["organiser"].(map[string]any)["judge_code"].(string)
				wantTS(t, "judge join", a.api(t, "POST", "/v1/hack/judge/"+code, map[string]any{"accept_coc": true, "display_name": "J"}, p.key), 200, "")
			}
			// Simulate a member imported before reservation-on-join existed.
			if _, err := a.database.Exec(`DELETE FROM event_used_names WHERE event_slug=$1`, slug); err != nil {
				t.Fatal(err)
			}
			wantTS(t, "remove person", a.api(t, "DELETE", base+"/people/"+a.uid(t, p), nil, org.key), 204, "")
			wantTS(t, "old empty deletion", a.api(t, "DELETE", base, nil, org.key), 204, "")
			taken, err := db.EventSlugTaken(context.Background(), a.database, slug)
			if err != nil || !taken {
				t.Fatalf("joined name released: %v", err)
			}
		})
	}
}

func TestHackDeleteConfirmEveryStage(t *testing.T) {
	for _, stage := range []string{"draft", "open", "building", "closed", "judging", "results", "archived"} {
		t.Run(stage, func(t *testing.T) {
			a := newHackApp(t)
			org := a.newPerson(t, "stage-delete")
			slug := uniqueSlug()
			wantTS(t, "create", a.createEvent(t, org, slug, nil), 201, "")
			if _, err := a.database.Exec(`UPDATE events SET stage=$2 WHERE slug=$1`, slug, stage); err != nil {
				t.Fatal(err)
			}
			base := "/v1/hack/events/" + slug
			wantTS(t, "wrong confirm", a.at(t, "DELETE", base, map[string]string{"confirm": "wrong"}, a.key(org)), 409, "delete_only_empty")
			wantTS(t, "matching confirm", a.at(t, "DELETE", base, map[string]string{"confirm": slug}, a.key(org)), 204, "")
		})
	}
}
