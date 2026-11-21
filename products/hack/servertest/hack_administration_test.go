package servertest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/csv"
	"io"
	"net/url"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/products/hack/repository/hackevents"
)

func TestHackAdministrationExportsAndUsage(t *testing.T) {
	a := newTeamSiteApp(t)
	org := a.newPerson(t, "org")
	otherOrg := a.newPerson(t, "otherorg")
	p1 := a.newPerson(t, "one")
	p2 := a.newPerson(t, "two")
	p3 := a.newPerson(t, "foreign")
	slug := a.makeEvent(t, org)
	foreign := a.makeEvent(t, otherOrg)
	markReady(t, a.certDir, slug)
	markReady(t, a.certDir, foreign)
	a.join(t, slug, p1, org)
	a.join(t, slug, p2, org)
	a.join(t, foreign, p3, otherOrg)
	team1, _ := a.startTeam(t, slug, "Alpha", p1)
	team2, _ := a.startTeam(t, slug, "Beta", p2)
	team3, _ := a.startTeam(t, foreign, "Alpha", p3)
	wantTS(t, "project alpha", a.deployTeam(t, slug, team1, p1.key, "alpha marker"), 201, "")
	wantTS(t, "project beta", a.deployTeam(t, slug, team2, p2.key, "beta marker"), 201, "")
	wantTS(t, "foreign project", a.deployTeam(t, foreign, team3, p3.key, "foreign marker"), 201, "")
	base := "/v1/hack/events/" + slug
	wantTS(t, "entry", a.api(t, "PUT", base+"/entry", map[string]any{"title": "=SUM(1,2)", "tagline": "Hello, world", "description": "two\nlines"}, p1.key), 200, "")
	wantTS(t, "rename denied", a.api(t, "PATCH", base+"/teams/"+team1, map[string]string{"name": "No"}, p1.key), 404, "event_not_found")
	wantTS(t, "rename cross-event denied", a.api(t, "PATCH", "/v1/hack/events/"+foreign+"/teams/"+team3, map[string]string{"name": "No"}, org.key), 404, "event_not_found")
	wantTS(t, "rename collision", a.api(t, "PATCH", base+"/teams/"+team1, map[string]string{"name": "Beta"}, org.key), 409, "team_name_taken")
	renamed := a.api(t, "PATCH", base+"/teams/"+team1, map[string]string{"name": "Renamed, Alpha"}, org.key)
	wantTS(t, "rename", renamed, 200, "")
	if renamed.json(t)["slug"] != team1 {
		t.Fatal("rename changed site address")
	}
	// Existing member keys keep working after a display-name change.
	wantTS(t, "publish after rename", a.deployTeam(t, slug, team1, p1.key, "alpha latest"), 200, "")
	ev, err := hackevents.GetEventBySlug(context.Background(), a.database, slug)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"participants.csv", "teams.csv", "entries.csv"} {
		path := base + "/export/" + kind
		r := a.api(t, "GET", path, nil, org.key)
		wantTS(t, kind, r, 200, "")
		rows, err := csv.NewReader(bytes.NewReader(r.body)).ReadAll()
		if err != nil || len(rows) < 2 {
			t.Fatalf("invalid CSV: %v", err)
		}
		if strings.Contains(string(r.body), p3.email) || strings.Contains(string(r.body), "foreign marker") {
			t.Fatal("foreign event leaked")
		}
		if kind == "entries.csv" && (rows[1][2] != "'=SUM(1,2)" || rows[1][3] != "Hello, world" || rows[1][4] != "two\nlines") {
			t.Fatalf("CSV content/escaping: %v", rows)
		}
		wantTS(t, "participant CSV denied", a.api(t, "GET", path, nil, p1.key), 404, "event_not_found")
	}
	unpack := func(r resp) map[string]string {
		t.Helper()
		wantTS(t, "archive", r, 200, "")
		gz, err := gzip.NewReader(bytes.NewReader(r.body))
		if err != nil {
			t.Fatal(err)
		}
		defer gz.Close()
		tr := tar.NewReader(gz)
		out := map[string]string{}
		for {
			head, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			b, err := io.ReadAll(tr)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(head.Name, "..") || strings.HasPrefix(head.Name, "/") {
				t.Fatal("unsafe archive path")
			}
			out[head.Name] = string(b)
		}
		return out
	}
	all := unpack(a.api(t, "GET", base+"/export/projects.tar.gz", nil, org.key))
	if !strings.Contains(all[team1+"/files/index.html"], "alpha latest") || !strings.Contains(all[team2+"/files/index.html"], "beta marker") {
		t.Fatalf("missing actual project files: %v", all)
	}
	for _, value := range all {
		if strings.Contains(value, "foreign marker") {
			t.Fatal("foreign archive leak")
		}
	}
	own := unpack(a.api(t, "GET", base+"/team/export.tar.gz", nil, p1.key))
	if len(own) != 1 || !strings.Contains(own[team1+"/files/index.html"], "alpha latest") {
		t.Fatalf("own archive wrong: %v", own)
	}
	wantTS(t, "all-team archive denied", a.api(t, "GET", base+"/export/projects.tar.gz", nil, p1.key), 404, "event_not_found")
	wantTS(t, "foreign own-team denied", a.api(t, "GET", "/v1/hack/events/"+foreign+"/team/export.tar.gz", nil, p1.key), 404, "event_not_found")
	usage := a.api(t, "GET", base+"/usage", nil, org.key)
	wantTS(t, "usage", usage, 200, "")
	u := usage.json(t)
	if u["sites"] != float64(2) || u["versions"] != float64(3) || u["file_bytes"].(float64) <= 0 {
		t.Fatalf("usage wrong: %v", u)
	}
	wantTS(t, "usage denied", a.api(t, "GET", base+"/usage", nil, p1.key), 404, "event_not_found")
	if err := a.sites.TrashTeamSite(context.Background(), ev.AccountID, team1); err != nil {
		t.Fatal(err)
	}
	after := a.api(t, "GET", base+"/usage", nil, org.key).json(t)
	if after["file_bytes"].(float64) < u["file_bytes"].(float64) {
		t.Fatal("usage lost retained files in Recently deleted")
	}
	wantTS(t, "deleted project cannot export", a.api(t, "GET", base+"/team/export.tar.gz", nil, p1.key), 404, "no_project")
}

// The platform admin joins an event as an organiser like anyone else and is an
// ordinary organiser there: no admin bypass in that event, and still only a
// read-only view of events it is not in (owner 2026-10-30).
func TestHackPlatformAdminJoinsAsOrganiser(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "adminhost")
	other := a.newPerson(t, "otherhost")
	slug := uniqueSlug()
	foreign := uniqueSlug()
	wantTS(t, "create", a.createEvent(t, org, slug, nil), 201, "")
	wantTS(t, "foreign", a.createEvent(t, other, foreign, nil), 201, "")
	base := "/v1/hack/events/" + slug
	fbase := "/v1/hack/events/" + foreign
	r := a.at(t, "POST", base+"/organiser-invite", nil, a.key(org))
	wantTS(t, "invite", r, 201, "")
	u, _ := url.Parse(r.json(t)["url"].(string))
	body := map[string]any{"accept_coc": true, "display_name": "Platform admin"}
	joined := a.at(t, "POST", "/v1/hack"+u.Path, body, a.adminH())
	wantTS(t, "admin accepts", joined, 200, "")
	if joined.json(t)["role"] != "organiser" {
		t.Fatalf("admin role: %s", joined.body)
	}
	view := a.at(t, "GET", base, nil, a.adminH())
	wantTS(t, "admin reads own event", view, 200, "")
	if v := view.json(t); v["role"] != "organiser" || v["admin_view"] != nil {
		t.Fatalf("admin member view: %s", view.body)
	}
	wantTS(t, "admin edits", a.at(t, "PATCH", base, map[string]any{"tagline": "Edited by the admin"}, a.adminH()), 200, "")
	wantTS(t, "admin invites", a.at(t, "POST", base+"/organiser-invite", nil, a.adminH()), 201, "")
	wantTS(t, "select organiser judges", a.at(t, "PATCH", base+"/judging/settings", map[string]any{"include_organisers": true}, a.adminH()), 200, "")
	wantTS(t, "admin judge queue", a.at(t, "GET", base+"/judge/queue", nil, a.adminH()), 200, "")
	// Nothing leaks to an event the admin is not in.
	fview := a.at(t, "GET", fbase, nil, a.adminH())
	wantTS(t, "foreign read", fview, 200, "")
	if fview.json(t)["admin_view"] != true {
		t.Fatalf("foreign view: %s", fview.body)
	}
	wantTS(t, "foreign edit", a.at(t, "PATCH", fbase, map[string]any{"tagline": "x"}, a.adminH()), 404, "event_not_found")
	wantTS(t, "foreign invite", a.at(t, "POST", fbase+"/organiser-invite", nil, a.adminH()), 404, "event_not_found")
	wantTS(t, "foreign queue", a.at(t, "GET", fbase+"/judge/queue", nil, a.adminH()), 404, "event_not_found")
	// A join code works for the admin too.
	a.openEvent(t, other, foreign)
	code := a.at(t, "GET", fbase, nil, a.key(other)).json(t)["organiser"].(map[string]any)["join_code"].(string)
	p := a.at(t, "POST", "/v1/hack/join/"+code, body, a.adminH())
	wantTS(t, "admin joins with a code", p, 200, "")
	if p.json(t)["role"] != "participant" {
		t.Fatalf("admin participant: %s", p.body)
	}
	wantTS(t, "participant admin cannot edit", a.at(t, "PATCH", fbase, map[string]any{"tagline": "x"}, a.adminH()), 404, "event_not_found")
	// A taken-down event stops the admin's organiser writes like anyone else's.
	wantTS(t, "takedown", a.at(t, "POST", "/v1/admin/hack/events/"+slug+"/takedown", map[string]string{"reason": "spam"}, a.adminH()), 200, "")
	wantTS(t, "taken down edit", a.at(t, "PATCH", base, map[string]any{"tagline": "y"}, a.adminH()), 403, "event_taken_down")
}
