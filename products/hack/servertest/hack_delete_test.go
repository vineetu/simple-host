package servertest

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/products/hack/hackhttp"
	hackeventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
)

func TestHackDeleteBusyEvent(t *testing.T) {
	a := newTeamSiteApp(t)
	hackhttp.RegisterHackPublic(a.mux, a.database, a.srv.URL, a.sites.TeamSiteURL, a.sites.TeamSitesReady, testHackPages)
	org, co, participant, judge, outsider := a.newPerson(t, "del-org"), a.newPerson(t, "del-co"), a.newPerson(t, "del-part"), a.newPerson(t, "del-judge"), a.newPerson(t, "del-outside")
	slug := a.makeEvent(t, org)
	base := "/v1/hack/events/" + slug
	t.Cleanup(func() { _, _ = a.database.Exec(`DELETE FROM event_used_names WHERE event_slug=$1`, slug) })
	a.join(t, slug, participant, org)
	info := a.api(t, "GET", base, nil, org.key).json(t)["organiser"].(map[string]any)
	judgeCode, joinCode := info["judge_code"].(string), info["join_code"].(string)
	wantTS(t, "join judge", a.api(t, "POST", "/v1/hack/judge/"+judgeCode, map[string]any{"accept_coc": true, "display_name": "Judge"}, judge.key), 200, "")
	ev, err := hackeventrepo.GetEventBySlug(context.Background(), a.database, slug)
	if err != nil {
		t.Fatal(err)
	}
	invite := a.api(t, "POST", base+"/organiser-invite", nil, org.key)
	wantTS(t, "invite", invite, 201, "")
	inviteURL, _ := url.Parse(invite.json(t)["url"].(string))
	wantTS(t, "co-organiser", a.api(t, "POST", "/v1/hack"+inviteURL.Path, map[string]any{"accept_coc": true, "display_name": "Co"}, co.key), 200, "")
	team, _ := a.startTeam(t, slug, "Delete team", participant)
	markReady(t, a.certDir, slug)
	key := participant.key
	wantTS(t, "team deploy", a.deployTeam(t, slug, team, key, "delete site"), 201, "")
	wantTS(t, "entry", a.api(t, "PUT", base+"/entry", map[string]string{"title": "Busy entry"}, participant.key), 200, "")
	wantTS(t, "custom site", a.deployTeam(t, slug, ev.ID, org.key, "delete custom"), 201, "")
	for _, storage := range []struct{ site, key string }{{team, key}, {ev.ID, org.key}} {
		at := func(path string) string { return "/v1/sites/" + storage.site + "/storage" + path + "?owner=" + slug }
		wantTS(t, "resource", a.api(t, "PUT", at("/resources/prefs"), map[string]any{"kind": "kv", "rules": json.RawMessage(publicRules)}, storage.key), 201, "")
		wantTS(t, "value", a.api(t, "PUT", at("/kv/prefs/keys/theme"), `{"value":"red"}`, storage.key), 200, "")
		wantTS(t, "files resource", a.api(t, "PUT", at("/resources/photos"), map[string]string{"kind": "files"}, storage.key), 201, "")
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
	runtime := filepath.Join(a.sites.DiskForTest().SiteDir(ev.AccountID, team), "runtime", "photos")
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
	// B2/B4: a taken-down team site is neither trashed nor erased with its event.
	takeDown := func(on bool) {
		if _, err := a.database.Exec(`UPDATE sites SET suspended_at = CASE WHEN $3 THEN now() END WHERE user_id = $1 AND name = $2`, ev.AccountID, team, on); err != nil {
			t.Fatal(err)
		}
	}
	takeDown(true)
	if err := a.sites.TrashTeamSite(context.Background(), ev.AccountID, team); err != nil {
		t.Fatal(err)
	}
	wantTS(t, "taken-down site kept", a.api(t, "DELETE", base, map[string]string{"confirm": slug}, co.key), 409, "site_taken_down")
	takeDown(false)
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
	wantTS(t, "teammate access gone", a.deployTeam(t, slug, team, key, "after deletion"), 404, "owner_not_found")
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
	for _, path := range []string{filepath.Dir(a.sites.DiskForTest().SiteDir(ev.AccountID, team)), a.sites.DiskForTest().TrashDir(ev.AccountID, "unused")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("files left at %s: %v", path, err)
		}
	}
}
