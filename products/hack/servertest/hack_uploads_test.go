package servertest

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

func TestHackWebsiteZipUploads(t *testing.T) {
	a := newTeamSiteApp(t)
	org, participant, outsider := a.newPerson(t, "zip-org"), a.newPerson(t, "zip-member"), a.newPerson(t, "zip-outsider")
	slug := a.makeEvent(t, org)
	a.join(t, slug, participant, org)
	team, _ := a.startTeam(t, slug, "Zip team", participant)
	markReady(t, a.certDir, slug)
	upload := func(path string, files map[string]string, key string) resp {
		var b bytes.Buffer
		z := zip.NewWriter(&b)
		for name, text := range files {
			w, err := z.Create(name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write([]byte(text)); err != nil {
				t.Fatal(err)
			}
		}
		if err := z.Close(); err != nil {
			t.Fatal(err)
		}
		return a.api(t, "PUT", path+"&create=1", b.String(), key)
	}
	base := "/v1/hack/events/" + slug
	site := "/v1/sites/" + team + "?owner=" + slug
	files := map[string]string{"index.html": "hello"}
	wantTS(t, "own team", upload(site, files, participant.key), 201, "")
	wantTS(t, "update team", upload(site, files, participant.key), 200, "")
	wantTS(t, "outsider", upload(site, files, outsider.key), 404, "owner_not_found")
	for _, tc := range []struct {
		files   map[string]string
		message string
		code    string
	}{
		{map[string]string{"nested/index.html": "hi"}, "no index.html at the root", "missing_index"},
		{map[string]string{"index.html": "hi", "server.py": "print('hi')"}, "disallowed file extension", "blocked_file_type"},
	} {
		r := upload(site, tc.files, participant.key)
		wantTS(t, "invalid archive", r, 400, tc.code)
		if !strings.Contains(string(r.body), tc.message) {
			t.Fatalf("missing reason: %s", r.body)
		}
	}
	wantTS(t, "bad zip", a.api(t, "PUT", site+"&create=1", "PKbroken", participant.key), 400, "invalid_archive")
	wantTS(t, "close", a.api(t, "POST", base+"/stage", map[string]string{"stage": "closed"}, org.key), 200, "")
	wantTS(t, "closed team", upload(site, files, participant.key), 409, "submissions_closed")
	wantTS(t, "closed before unpacking", a.api(t, "PUT", site+"&create=1", "PKbroken", participant.key), 409, "submissions_closed")
	draft := a.makeEvent(t, org)
	if _, err := a.database.Exec(`UPDATE events SET stage='draft' WHERE slug=$1`, draft); err != nil {
		t.Fatal(err)
	}
	path := "/v1/hack/events/" + draft + "/website"
	eventSitePath := "/v1/sites/" + eventSite(t, a, draft, org) + "?owner=" + draft
	wantTS(t, "draft event", upload(eventSitePath, files, org.key), 201, "")
	state := a.api(t, "GET", path, nil, org.key).json(t)
	if len(state["versions"].([]any)) != 1 || state["upload_limit_bytes"].(float64) <= 0 {
		t.Fatalf("missing versions or limits: %v", state)
	}
	wantTS(t, "event outsider", upload(eventSitePath, files, participant.key), 404, "owner_not_found")
}

func TestHackTeamPublishSuspendedHolderBeforeClosedTeam(t *testing.T) {
	a := newTeamSiteApp(t)
	org, participant := a.newPerson(t, "order-org"), a.newPerson(t, "order-member")
	slug := a.makeEvent(t, org)
	a.join(t, slug, participant, org)
	team, _ := a.startTeam(t, slug, "Order team", participant)
	markReady(t, a.certDir, slug)
	base := "/v1/hack/events/" + slug
	wantTS(t, "close", a.api(t, "POST", base+"/stage", map[string]string{"stage": "closed"}, org.key), 200, "")
	publish := func() resp {
		return a.api(t, "PUT", "/v1/sites/"+team+"?owner="+slug+"&create=1", "PKbroken", participant.key)
	}
	wantTS(t, "closed before archive parsing", publish(), 409, "submissions_closed")
	if _, err := a.database.Exec(`UPDATE users SET suspended_at=now() WHERE id=(SELECT account_id FROM events WHERE slug=$1)`, slug); err != nil {
		t.Fatal(err)
	}
	wantTS(t, "holding account before closed team", publish(), 403, "account_suspended")
	if _, err := a.database.Exec(`UPDATE users SET suspended_at=NULL WHERE id=(SELECT account_id FROM events WHERE slug=$1)`, slug); err != nil {
		t.Fatal(err)
	}
	wantTS(t, "closed after holder restored", publish(), 409, "submissions_closed")
}
