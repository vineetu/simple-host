package servertest

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// On Simple Hack the shared website tools act on the team's site named with
// owner=<event>:
// an upload link and a partial update land there, never on another name,
// and a link stops working when its person leaves the team.
func TestHackTeamUploadLinkAndPartialUpdate(t *testing.T) {
	a := newTeamSiteApp(t)
	org, participant := a.newPerson(t, "upl-org"), a.newPerson(t, "upl-part")
	clientID := a.registerClient(t, testRedirect)
	access := a.connect(t, participant, clientID, testRedirect)["access_token"].(string)
	event := a.makeEvent(t, org)
	t.Cleanup(func() { a.cleanupSlug(event) })
	a.join(t, event, participant, org)
	teamSlug, _ := a.startTeam(t, event, "Uploaders", participant)
	markReady(t, a.certDir, event)
	call := func(name string, args map[string]any) (string, map[string]any, bool) {
		return toolResultOf(t, a.rpc(t, access, "tools/call", map[string]any{"name": name, "arguments": args}))
	}
	if _, out, failed := call("create_upload_link", map[string]any{"site": "someone-else", "owner": event}); !failed {
		u, _ := url.Parse(out["upload_url"].(string))
		r := a.do(t, "PUT", u.Path, strings.NewReader(tarGzOf(t, 0)), map[string]string{"Content-Type": "application/gzip"})
		t.Logf("upload to another name: %d %s", r.status, r.body)
		if r.status < 400 {
			t.Fatalf("upload link published a site other than the team's: %d %s", r.status, r.body)
		}
	}
	mint := func() string {
		t.Helper()
		text, out, failed := call("create_upload_link", map[string]any{"site": teamSlug, "owner": event})
		if failed {
			t.Fatalf("upload link for the team site: %s", text)
		}
		u, err := url.Parse(out["upload_url"].(string))
		if err != nil {
			t.Fatal(err)
		}
		return u.Path
	}
	r := a.do(t, "PUT", mint(), strings.NewReader(tarGzOf(t, 0)), map[string]string{"Content-Type": "application/gzip"})
	if r.status != http.StatusCreated && r.status != http.StatusOK {
		t.Fatalf("team upload: %d %s", r.status, r.body)
	}
	text, _, failed := call("update_site", map[string]any{"site": teamSlug, "owner": event, "keep_other_files": true,
		"files": map[string]any{"about.html": "<p>about</p>"}})
	if failed {
		t.Fatalf("partial update: %s", text)
	}
	host := teamSlug + "." + event + "." + tsDomain
	if index, about := a.on(t, "GET", host, "/", nil, ""), a.on(t, "GET", host, "/about.html", nil, ""); !strings.Contains(string(index.body), "big") || !strings.Contains(string(about.body), "about") {
		t.Fatalf("after the partial update the site serves: %d %s / %d %s", index.status, index.body, about.status, about.body)
	}
	path := mint()
	if r := a.api(t, "POST", "/v1/hack/events/"+event+"/teams/leave", nil, participant.key); r.status != http.StatusOK {
		t.Fatalf("leave team: %d %s", r.status, r.body)
	}
	if r := a.do(t, "PUT", path, strings.NewReader(tarGzOf(t, 0)), nil); r.status < 400 {
		t.Fatalf("upload link worked after leaving the team: %d %s", r.status, r.body)
	}
}
