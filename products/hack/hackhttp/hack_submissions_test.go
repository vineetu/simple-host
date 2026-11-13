package hackhttp

import (
	"context"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/webassets"
)

func TestHackSubmissions(t *testing.T) {
	w := newHackEntryWorld(t)
	a := w.a
	path := "/v1/hack/events/" + w.slug
	pages := NewHackEventPages(webassets.Static)
	RegisterHackPublic(a.mux, a.database, a.srv.URL, nil, nil, pages)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := a.database.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	stage := func(s string) { exec(`UPDATE events SET stage=$2 WHERE slug=$1`, w.slug, s) }
	get := func(p string, who person) resp { return a.at(t, "GET", p, nil, a.key(who)) }
	patch := func(on bool) {
		t.Helper()
		r := a.at(t, "PATCH", path, map[string]any{"submissions_public": on}, a.key(w.org))
		if r.status != 200 {
			t.Fatalf("patch: %d %s", r.status, r.body)
		}
	}
	if get(path, w.org).json(t)["event"].(map[string]any)["submissions_public"] != false {
		t.Fatal("must default off")
	}
	for _, p := range []person{w.p1, w.p2} {
		r := a.at(t, "PUT", w.entryPath(), map[string]any{"title": "A project"}, a.key(p))
		if r.status != 200 {
			t.Fatalf("entry: %s", r.body)
		}
	}
	exec(`UPDATE event_entries SET submitted_at=now(), screenshot=$2, screenshot_type='image/png' WHERE event_id=(SELECT id FROM events WHERE slug=$1)`, w.slug, []byte("test image"))
	for _, s := range []string{"building", "closed", "judging", "results", "archived"} {
		stage(s)
		r := get(path+"/submissions", w.lone)
		want := 200
		if s == "building" || s == "closed" {
			want = 409
		}
		if r.status != want {
			t.Fatalf("%s: %d %s", s, r.status, r.body)
		}
		if want == 409 {
			if r.json(t)["code"] != "submissions_not_open" {
				t.Fatal(string(r.body))
			}
			continue
		}
		if len(r.json(t)["submissions"].([]any)) != 2 {
			t.Fatal(string(r.body))
		}
		for _, key := range []string{"score", "rank", "comments", "missing", "excluded"} {
			if strings.Contains(string(r.body), `"`+key+`"`) {
				t.Fatalf("leaked %s", key)
			}
		}
		allowed := map[string]bool{"title": true, "tagline": true, "problem": true, "audience": true, "tools": true, "maturity": true, "track": true, "video_url": true, "video_host": true, "code_url": true, "code_host": true, "site_url": true, "has_screenshot": true, "team_name": true, "team": true, "screenshot_url": true}
		for _, row := range r.json(t)["submissions"].([]any) {
			for key := range row.(map[string]any) {
				if !allowed[key] {
					t.Fatalf("unexpected card field %q", key)
				}
			}
		}
		if r.header.Get("Cache-Control") != "private, no-store" {
			t.Fatalf("cache: %q", r.header.Get("Cache-Control"))
		}
	}
	stage("judging")
	for _, p := range []person{w.p1, w.p2, w.lone} {
		if r := get(path+"/submissions", p); r.status != 200 {
			t.Fatal(string(r.body))
		}
	}
	outsider := a.newPerson(t, "outsider")
	if r := get(path+"/submissions", outsider); r.status == 200 {
		t.Fatal("non-member allowed")
	}
	pending := a.newPerson(t, "pending")
	exec(`INSERT INTO event_members (event_id,user_id,role,display_name,approval_status) SELECT id,$2,'participant','Pending person','pending' FROM events WHERE slug=$1`, w.slug, a.userID(t, pending))
	if r := get(path+"/submissions", pending); r.status != 403 {
		t.Fatalf("pending applicant: %d %s", r.status, r.body)
	}
	if r := get(path+"/submissions/"+w.alpha+"/screenshot", pending); r.status != 403 {
		t.Fatalf("pending applicant screenshot: %d", r.status)
	}
	if r := a.at(t, "PATCH", path, map[string]any{"submissions_public": true}, a.key(w.p1)); r.status == 200 {
		t.Fatal("participant changed the setting")
	}
	if r := a.at(t, "GET", path+"/submissions", nil, nil); r.status == 200 {
		t.Fatal("anonymous allowed")
	}
	shot := "/e/" + w.slug + "/submissions/" + w.alpha + "/screenshot"
	if r := a.at(t, "GET", shot, nil, nil); r.status != 404 {
		t.Fatalf("off shot: %d", r.status)
	}
	if r := a.at(t, "GET", path+"/public", nil, nil); len(r.json(t)["submissions"].([]any)) != 0 {
		t.Fatal("off feed")
	}
	patch(true)
	if get(path, w.org).json(t)["event"].(map[string]any)["submissions_public"] != true {
		t.Fatal("patch did not round-trip")
	}
	for _, s := range []string{"building", "closed", "judging", "results", "archived"} {
		stage(s)
		r := a.at(t, "GET", path+"/public", nil, nil)
		rows := r.json(t)["submissions"].([]any)
		want := 2
		shotStatus := 200
		if s == "building" || s == "closed" {
			want = 0
			shotStatus = 404
		}
		if len(rows) != want {
			t.Fatalf("public %s: %s", s, r.body)
		}
		r = a.at(t, "GET", shot, nil, nil)
		if r.status != shotStatus {
			t.Fatalf("shot %s: %d %s", s, r.status, r.body)
		}
		if shotStatus == 200 && r.header.Get("Cross-Origin-Resource-Policy") != "cross-origin" {
			t.Fatal("cross-origin image blocked")
		}
		if want > 0 && !strings.HasPrefix(rows[0].(map[string]any)["screenshot_url"].(string), a.srv.URL+"/e/") {
			t.Fatal("screenshot URL must be absolute")
		}
	}
	stage("judging")
	if r := get(path+"/submissions/"+w.alpha+"/screenshot", w.p1); r.status != 200 {
		t.Fatal("participant screenshot unavailable")
	}
	var accountID string
	if err := a.database.QueryRow(`SELECT account_id FROM events WHERE slug=$1`, w.slug).Scan(&accountID); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO sites (user_id,name,site_url,active_version) VALUES ($1,$2,'https://example.test/',1)`, accountID, w.alpha)
	ev, err := a.hack.eventsSvc().EventBySlug(context.Background(), w.slug)
	if err != nil {
		t.Fatal(err)
	}
	cards, err := visibleSubmissions(context.Background(), a.database, ev, entrySiteStub{}.TeamSiteURL, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 2 || cards[0].Team != w.alpha || cards[0].SiteURL == "" {
		t.Fatalf("live site missing: %+v", cards)
	}
	a.hack.SetSites(entrySiteStub{})
	exec(`UPDATE event_teams SET site_taken_down_at=now() WHERE event_id=$1 AND slug=$2`, ev.ID, w.alpha)
	r := get(path+"/submissions", w.p1)
	if r.json(t)["submissions"].([]any)[0].(map[string]any)["site_url"] != "" {
		t.Fatal("taken-down site linked")
	}
	if r := a.at(t, "GET", "/e/"+w.slug, nil, nil); r.status != 200 || !strings.Contains(string(r.body), `id="submissions"`) || !strings.Contains(string(r.body), `href="#submissions"`) {
		t.Fatalf("submissions page: %d %s", r.status, r.body)
	}
	patch(false)
	if r := a.at(t, "GET", "/e/"+w.slug, nil, nil); strings.Contains(string(r.body), `id="submissions"`) {
		t.Fatal("off page shows submissions")
	}
	patch(true)
	for _, state := range []string{"draft", "not_submitted", "excluded"} {
		exec(`UPDATE event_entries SET submitted_at=NULL, submit_settled_at=NULL, excluded_at=NULL WHERE team_id=(SELECT id FROM event_teams WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND slug=$2)`, w.slug, w.alpha)
		if state == "not_submitted" {
			exec(`UPDATE event_entries SET submit_settled_at=now() WHERE team_id=(SELECT id FROM event_teams WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND slug=$2)`, w.slug, w.alpha)
		}
		if state == "excluded" {
			exec(`UPDATE event_entries SET submitted_at=now(),excluded_at=now() WHERE team_id=(SELECT id FROM event_teams WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND slug=$2)`, w.slug, w.alpha)
		}
		r := get(path+"/submissions", w.p2)
		if len(r.json(t)["submissions"].([]any)) != 1 {
			t.Fatalf("%s: %s", state, r.body)
		}
		if feed := a.at(t, "GET", path+"/public", nil, nil); len(feed.json(t)["submissions"].([]any)) != 1 {
			t.Fatalf("hidden public %s: %s", state, feed.body)
		}
		if r := a.at(t, "GET", shot, nil, nil); r.status != 404 {
			t.Fatalf("hidden screenshot %s", state)
		}
		if r := get(path+"/submissions/"+w.alpha+"/screenshot", w.p1); r.status != 404 || r.json(t)["code"] != "team_not_found" {
			t.Fatal("hidden participant screenshot")
		}
	}
	exec(`UPDATE event_entries SET submitted_at=now(), excluded_at=NULL WHERE team_id=(SELECT id FROM event_teams WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND slug=$2)`, w.slug, w.alpha)
	if r := a.at(t, "GET", shot, nil, nil); r.status != 200 || r.header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("restored screenshot: %d %q", r.status, r.header.Get("Cache-Control"))
	}
	if r := get(path+"/submissions/"+w.alpha+"/screenshot", w.p1); r.status != 200 {
		t.Fatal("restored participant screenshot")
	}
	exec(`UPDATE events SET taken_down_at=now() WHERE slug=$1`, w.slug)
	if r := get(path+"/submissions/"+w.alpha+"/screenshot", w.p1); r.status == 200 {
		t.Fatal("taken-down participant screenshot")
	}
	if r := a.at(t, "GET", path+"/public", nil, nil); r.status == 200 {
		t.Fatal("taken-down feed")
	}
	if r := get(path+"/submissions", w.p1); r.status == 200 {
		t.Fatal("taken-down event exposed")
	}
	if r := a.at(t, "GET", shot, nil, nil); r.status != 404 {
		t.Fatal("taken-down screenshot")
	}
}
