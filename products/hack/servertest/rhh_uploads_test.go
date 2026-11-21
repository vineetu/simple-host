package servertest

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// TestRhhWebsiteZipPolicyAndHolderOrder: the strict ZIP policy and the check
// order (holding account, then stage, then archive) through the real route.
func TestRhhWebsiteZipPolicyAndHolderOrder(t *testing.T) {
	a := newTeamSiteApp(t)
	org, participant, outsider := a.newPerson(t, "rhh-zip-org"), a.newPerson(t, "rhh-zip-member"), a.newPerson(t, "rhh-zip-outsider")
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
	for _, tc := range []struct {
		name   string
		run    func() resp
		status int
		code   string
		reason string
	}{
		{"own team", func() resp { return upload(site, files, participant.key) }, 201, "", ""},
		{"update team", func() resp { return upload(site, files, participant.key) }, 200, "", ""},
		{"outsider", func() resp { return upload(site, files, outsider.key) }, 404, "owner_not_found", ""},
		{"disallowed extension", func() resp {
			return upload(site, map[string]string{"index.html": "hi", "server.py": "print('hi')"}, participant.key)
		}, 400, "blocked_file_type", "disallowed file extension"},
		{"bad zip", func() resp { return a.api(t, "PUT", site+"&create=1", "PKbroken", participant.key) }, 400, "invalid_archive", ""},
	} {
		r := tc.run()
		wantTS(t, tc.name, r, tc.status, tc.code)
		if tc.reason != "" && !strings.Contains(string(r.body), tc.reason) {
			t.Fatalf("%s: missing reason: %s", tc.name, r.body)
		}
	}

	holder := func(suspended string) {
		if _, err := a.database.Exec(`UPDATE users SET suspended_at=`+suspended+` WHERE id=(SELECT account_id FROM events WHERE slug=$1)`, slug); err != nil {
			t.Fatal(err)
		}
	}
	broken := func() resp { return a.api(t, "PUT", site+"&create=1", "PKbroken", participant.key) }
	wantTS(t, "close", a.api(t, "POST", base+"/stage", map[string]string{"stage": "closed"}, org.key), 200, "")
	wantTS(t, "closed team", upload(site, files, participant.key), 409, "submissions_closed")
	wantTS(t, "closed before unpacking", broken(), 409, "submissions_closed")
	holder("now()")
	wantTS(t, "holding account before closed team", broken(), 403, "account_suspended")
	holder("NULL")
	wantTS(t, "closed after holder restored", broken(), 409, "submissions_closed")

	draft := a.makeEvent(t, org)
	if _, err := a.database.Exec(`UPDATE events SET stage='draft' WHERE slug=$1`, draft); err != nil {
		t.Fatal(err)
	}
	eventSitePath := "/v1/sites/" + eventSite(t, a, draft, org) + "?owner=" + draft
	wantTS(t, "draft event", upload(eventSitePath, files, org.key), 201, "")
	wantTS(t, "event outsider", upload(eventSitePath, files, participant.key), 404, "owner_not_found")
}
