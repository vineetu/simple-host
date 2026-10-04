package handler

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestHackArchiveLinksFullBytesAndRevocation(t *testing.T) {
	a := newTeamSiteApp(t)
	org := a.newPerson(t, "linkorg")
	part := a.newPerson(t, "linkpart")
	other := a.newPerson(t, "linkother")
	slug := a.makeEvent(t, org)
	markReady(t, a.certDir, slug)
	a.join(t, slug, part, org)
	a.join(t, slug, other, org)
	teamSlug, _ := a.startTeam(t, slug, "Linked archive", part)
	otherSlug, _ := a.startTeam(t, slug, "Other archive", other)
	wantTS(t, "published", a.deployTeam(t, teamSlug, a.teamKey(t, slug, part), "private link bytes"), 201, "")
	wantTS(t, "published other", a.deployTeam(t, otherSlug, a.teamKey(t, slug, other), "other private bytes"), 201, "")
	base := "/v1/hack/events/" + slug
	link := func(kind, key string) string {
		t.Helper()
		r := a.api(t, http.MethodPost, base+"/export/"+kind+"-link", nil, key)
		wantTS(t, "mint "+kind, r, 200, "")
		return r.json(t)["url"].(string)
	}
	download := func(raw string) resp {
		t.Helper()
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		return a.api(t, http.MethodGet, u.RequestURI(), nil, "")
	}
	unpack := func(r resp) string {
		t.Helper()
		wantTS(t, "archive download", r, 200, "")
		gz, err := gzip.NewReader(bytes.NewReader(r.body))
		if err != nil {
			t.Fatal(err)
		}
		defer gz.Close()
		// Generated JSON files use export-time mtimes. Compare complete paths
		// and payload bytes, rather than tar headers across a second boundary.
		tr := tar.NewReader(gz)
		var files strings.Builder
		for {
			header, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(tr)
			if err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(&files, "%d:%s%d:%s", len(header.Name), header.Name, len(body), body)
		}
		return files.String()
	}
	wantTS(t, "participant cannot mint all", a.api(t, http.MethodPost, base+"/export/projects-link", nil, part.key), 404, "event_not_found")
	wantTS(t, "organiser cannot mint own team", a.api(t, http.MethodPost, base+"/export/own-team-link", nil, org.key), 404, "event_not_found")
	wantTS(t, "nonmember admin cannot mint", a.api(t, http.MethodPost, base+"/export/projects-link", nil, a.admin), 404, "event_not_found")
	allLink := link("projects", org.key)
	ownLink := link("own-team", part.key)
	if got, want := unpack(download(allLink)), unpack(a.api(t, http.MethodGet, base+"/export/projects.tar.gz", nil, org.key)); got != want {
		t.Fatal("linked projects archive differs from keyed archive")
	}
	if got, want := unpack(download(ownLink)), unpack(a.api(t, http.MethodGet, base+"/team/export.tar.gz", nil, part.key)); got != want || !strings.Contains(got, "private link bytes") || strings.Contains(got, "other private bytes") {
		t.Fatal("linked team archive differs from keyed own-team archive or leaks another team")
	}
	for _, token := range []string{"", "bogus", strings.TrimSuffix(allLink, "x") + "x"} {
		if token == "" || token == "bogus" {
			token = a.srv.URL + "/v1/hack/archive?token=" + token
		}
		wantTS(t, "invalid link", download(token), 404, "archive_link_invalid")
	}
	var eventID string
	if err := a.database.QueryRow(`SELECT id FROM events WHERE slug=$1`, slug).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	var ownTeamID string
	if err := a.database.QueryRow(`SELECT team_id FROM event_members WHERE event_id=$1 AND user_id=$2`, eventID, a.uid(t, part)).Scan(&ownTeamID); err != nil {
		t.Fatal(err)
	}
	claim := hackArchiveClaim{UserID: a.uid(t, org), EventID: eventID, Slug: slug, Kind: "projects", Expiry: time.Now().Add(-time.Second).Unix()}
	wantTS(t, "expired link", download(a.srv.URL+"/v1/hack/archive?token="+a.hack.signHackArchiveClaim(claim)), 404, "archive_link_invalid")
	if _, err := a.database.Exec(`UPDATE event_members SET team_id=NULL WHERE event_id=$1 AND user_id=$2`, claim.EventID, a.uid(t, part)); err != nil {
		t.Fatal(err)
	}
	wantTS(t, "team switch revokes", download(ownLink), 404, "archive_link_invalid")
	if _, err := a.database.Exec(`UPDATE event_members SET team_id=$3, approval_status='pending' WHERE event_id=$1 AND user_id=$2`, eventID, a.uid(t, part), ownTeamID); err != nil {
		t.Fatal(err)
	}
	wantTS(t, "approval revokes", download(ownLink), 404, "archive_link_invalid")
	if _, err := a.database.Exec(`DELETE FROM event_members WHERE event_id=$1 AND user_id=$2`, claim.EventID, a.uid(t, org)); err != nil {
		t.Fatal(err)
	}
	wantTS(t, "organiser removal revokes", download(allLink), 404, "archive_link_invalid")
}
