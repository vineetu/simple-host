package handler

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/csv"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/db"
)

func TestHackAdministrationCoOrganisers(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "adminorg")
	co := a.newPerson(t, "coorg")
	other := a.newPerson(t, "otherorg")
	part := a.newPerson(t, "part")
	slug := uniqueSlug()
	foreign := uniqueSlug()
	wantTS(t, "create", a.createEvent(t, org, slug, nil), 201, "")
	wantTS(t, "other event", a.createEvent(t, other, foreign, nil), 201, "")
	base := "/v1/hack/events/" + slug
	invite := func(p person) string {
		t.Helper()
		r := a.at(t, "POST", base+"/organiser-invite", nil, a.key(p))
		wantTS(t, "invite", r, 201, "")
		u, _ := url.Parse(r.json(t)["url"].(string))
		return "/v1/hack" + u.Path
	}
	first := invite(org)
	second := invite(org)
	wantTS(t, "rotation", a.at(t, "GET", first, nil, nil), 404, "invalid_code")
	wantTS(t, "preview", a.at(t, "GET", second, nil, nil), 200, "")
	var stored string
	err := a.database.QueryRow(`SELECT token_hash FROM event_organiser_invites WHERE event_id=(SELECT id FROM events WHERE slug=$1)`, slug).Scan(&stored)
	if err != nil || strings.Contains(second, stored) {
		t.Fatal("invitation must store a hash")
	}
	a.openEvent(t, org, slug)
	view := a.at(t, "GET", base, nil, a.key(org)).json(t)
	code := view["organiser"].(map[string]any)["join_code"].(string)
	body := map[string]any{"accept_coc": true, "display_name": "Collaborator"}
	wantTS(t, "participant joins", a.at(t, "POST", "/v1/hack/join/"+code, body, a.key(part)), 200, "")
	wantTS(t, "no silent role promotion", a.at(t, "POST", second, body, a.key(part)), 409, "already_member")
	wantTS(t, "coC required", a.at(t, "POST", second, map[string]any{"display_name": "Collaborator"}, a.key(co)), 400, "coc_required")
	joined := a.at(t, "POST", second, body, a.key(co))
	wantTS(t, "accept", joined, 200, "")
	if joined.json(t)["role"] != "organiser" {
		t.Fatal("wrong role")
	}
	wantTS(t, "single use", a.at(t, "POST", second, body, a.key(other)), 404, "invalid_code")
	wantTS(t, "manage own event", a.at(t, "PATCH", base, map[string]any{"tagline": "Co-organiser edit"}, a.key(co)), 200, "")
	wantTS(t, "foreign event denied", a.at(t, "GET", "/v1/hack/events/"+foreign+"/people", nil, a.key(co)), 404, "event_not_found")
	wantTS(t, "participant cannot invite", a.at(t, "POST", base+"/organiser-invite", nil, a.key(part)), 404, "event_not_found")
	wantTS(t, "primary protected", a.at(t, "DELETE", base+"/organisers/"+a.userID(t, org), nil, a.key(co)), 409, "primary_organiser")
	issuedByCo := invite(co)
	wantTS(t, "remove coorganiser", a.at(t, "DELETE", base+"/organisers/"+a.userID(t, co), nil, a.key(org)), 204, "")
	wantTS(t, "removed access denied", a.at(t, "GET", base+"/people", nil, a.key(co)), 404, "event_not_found")
	wantTS(t, "removed issuer link denied", a.at(t, "POST", issuedByCo, body, a.key(other)), 404, "invalid_code")
	fresh := invite(org)
	wantTS(t, "revoke", a.at(t, "DELETE", base+"/organiser-invite", nil, a.key(org)), 204, "")
	wantTS(t, "revoked denied", a.at(t, "POST", fresh, body, a.key(co)), 404, "invalid_code")
	expired := invite(org)
	if _, err := a.database.Exec(`UPDATE event_organiser_invites SET expires_at=now()-interval '1 second' WHERE event_id=(SELECT id FROM events WHERE slug=$1)`, slug); err != nil {
		t.Fatal(err)
	}
	wantTS(t, "expired denied", a.at(t, "POST", expired, body, a.key(co)), 404, "invalid_code")
}

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
	key1 := a.teamKey(t, slug, p1)
	key2 := a.teamKey(t, slug, p2)
	key3 := a.teamKey(t, foreign, p3)
	wantTS(t, "project alpha", a.deployTeam(t, team1, key1, "alpha marker"), 201, "")
	wantTS(t, "project beta", a.deployTeam(t, team2, key2, "beta marker"), 201, "")
	wantTS(t, "foreign project", a.deployTeam(t, team3, key3, "foreign marker"), 201, "")
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
	wantTS(t, "publish after rename", a.deployTeam(t, team1, key1, "alpha latest"), 200, "")
	// Archives preserve saved state and private collections, within the same team.
	ev, err := db.GetEventBySlug(context.Background(), a.database, slug)
	if err != nil {
		t.Fatal(err)
	}
	site, err := db.GetSiteByUser(context.Background(), a.database, ev.AccountID, team1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.Exec(`UPDATE sites SET state='{"team_secret":"alpha only"}' WHERE id=$1`, site.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.Exec(`INSERT INTO collection_settings(site_id,collection,private) VALUES ($1,'private_entries',true)`, site.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.Exec(`INSERT INTO collection_items(site_id,collection,data,submitted_by) VALUES ($1,'private_entries','{"private_marker":"alpha private"}',$2)`, site.ID, a.uid(t, p1)); err != nil {
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
	if len(own) != 3 || !strings.Contains(own[team1+"/files/index.html"], "alpha latest") || !strings.Contains(own[team1+"/state.json"], "alpha only") || !strings.Contains(own[team1+"/collections.json"], "alpha private") {
		t.Fatalf("own archive wrong: %v", own)
	}
	wantTS(t, "all-team archive denied", a.api(t, "GET", base+"/export/projects.tar.gz", nil, p1.key), 404, "event_not_found")
	wantTS(t, "foreign own-team denied", a.api(t, "GET", "/v1/hack/events/"+foreign+"/team/export.tar.gz", nil, p1.key), 404, "event_not_found")
	wantTS(t, "team key cannot export event", a.api(t, "GET", base+"/export/projects.tar.gz", nil, key1), http.StatusForbidden, "")
	usage := a.api(t, "GET", base+"/usage", nil, org.key)
	wantTS(t, "usage", usage, 200, "")
	u := usage.json(t)
	wantSavedBytes := float64(len(`{"team_secret": "alpha only"}`) + len(`{"private_marker": "alpha private"}`))
	if u["saved_data_bytes"] != wantSavedBytes {
		t.Fatalf("saved data must count state once: got %v, want %v", u["saved_data_bytes"], wantSavedBytes)
	}
	if u["sites"] != float64(2) || u["versions"] != float64(3) || u["file_bytes"].(float64) <= 0 || u["saved_data_bytes"].(float64) <= 0 {
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

func TestHackAdministrationInvitationConsumedOnceConcurrently(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "inviteowner")
	one := a.newPerson(t, "one")
	two := a.newPerson(t, "two")
	slug := uniqueSlug()
	wantTS(t, "event", a.createEvent(t, org, slug, nil), 201, "")
	r := a.at(t, "POST", "/v1/hack/events/"+slug+"/organiser-invite", nil, a.key(org))
	wantTS(t, "invitation", r, 201, "")
	u, _ := url.Parse(r.json(t)["url"].(string))
	path := "/v1/hack" + u.Path
	start := make(chan struct{})
	done := make(chan int, 2)
	for _, p := range []person{one, two} {
		go func(p person) {
			<-start
			done <- a.at(t, "POST", path, map[string]any{"accept_coc": true, "display_name": "Concurrent co-organiser"}, a.key(p)).status
		}(p)
	}
	close(start)
	statuses := map[int]int{}
	statuses[<-done]++
	statuses[<-done]++
	if statuses[200] != 1 || statuses[404] != 1 {
		t.Fatalf("single-use invitation admitted wrong callers: %v", statuses)
	}
}

func TestHackAdministrationCannotRemoveLastOrganiserConcurrently(t *testing.T) {
	a := newHackApp(t)
	one := a.newPerson(t, "legacyowner")
	two := a.newPerson(t, "legacyco")
	slug := uniqueSlug()
	wantTS(t, "event", a.createEvent(t, one, slug, nil), 201, "")
	ev, err := db.GetEventBySlug(context.Background(), a.database, slug)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.InsertEventMember(context.Background(), a.database, ev.ID, a.userID(t, two), "organiser", "Second organiser"); err != nil {
		t.Fatal(err)
	}
	// The creator may be absent on a legacy/imported event. Its remaining
	// organisers must not both leave and strand it.
	if _, err := a.database.Exec(`UPDATE events SET created_by=NULL WHERE id=$1`, ev.ID); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	done := make(chan int, 2)
	for _, p := range []person{one, two} {
		id := a.userID(t, p)
		go func(p person, id string) {
			<-start
			done <- a.at(t, "DELETE", "/v1/hack/events/"+slug+"/organisers/"+id, nil, a.key(p)).status
		}(p, id)
	}
	close(start)
	statuses := map[int]int{}
	statuses[<-done]++
	statuses[<-done]++
	if statuses[204] != 1 || statuses[404] != 1 {
		t.Fatalf("concurrent removal: %v", statuses)
	}
	var n int
	if err := a.database.QueryRow(`SELECT count(*) FROM event_members WHERE event_id=$1 AND role='organiser'`, ev.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("remaining organisers=%d, err=%v", n, err)
	}
}
