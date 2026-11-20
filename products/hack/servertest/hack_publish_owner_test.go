package servertest

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Publishing as the event: every person uses their own key and names the
// event as owner; the platform decides what they may do with which site.
func TestHackPublishWithOwnKeyAndOwner(t *testing.T) {
	a := newTeamSiteApp(t)
	org, admin, mate, outsider := a.newPerson(t, "org"), a.newPerson(t, "admin"), a.newPerson(t, "mate"), a.newPerson(t, "outsider")
	slug := a.makeEvent(t, org)
	a.join(t, slug, admin, org)
	a.join(t, slug, mate, org)
	team, code := a.startTeam(t, slug, "Builders", admin)
	wantTS(t, "mate joins", a.api(t, "POST", "/v1/hack/events/"+slug+"/teams/join", map[string]string{"code": code}, mate.key), 200, "")
	markReady(t, a.certDir, slug)
	eventID, _ := a.api(t, "GET", "/v1/hack/events/"+slug+"/website", nil, org.key).json(t)["site"].(string)
	if eventID == "" {
		t.Fatal("event website names no site")
	}

	t.Run("team admin publishes the team site", func(t *testing.T) {
		wantTS(t, "admin", a.deployTeam(t, slug, team, admin.key, "admin"), 201, "")
	})
	t.Run("teammate publishes the team site", func(t *testing.T) {
		wantTS(t, "mate", a.deployTeam(t, slug, team, mate.key, "mate"), 200, "")
	})
	t.Run("teammate writes the site's KV storage", func(t *testing.T) {
		base := "/v1/sites/" + team + "/storage"
		wantTS(t, "resource", a.api(t, "PUT", base+"/resources/notes?owner="+slug, map[string]any{"kind": "kv", "rules": json.RawMessage(privateRules)}, mate.key), 201, "")
		wantTS(t, "put", a.api(t, "PUT", base+"/kv/notes/keys/hello?owner="+slug, map[string]any{"value": "from mate"}, mate.key), 200, "")
	})
	t.Run("organiser publishes the event website", func(t *testing.T) {
		wantTS(t, "event website", a.deployTeam(t, slug, eventID, org.key, "event"), 201, "")
	})
	t.Run("organiser reads the team site", func(t *testing.T) {
		if r := a.api(t, "GET", "/v1/sites/"+team+"/storage/kv/notes/keys/hello?owner="+slug, nil, org.key); r.status != 200 || !strings.Contains(string(r.body), "from mate") {
			t.Fatalf("read: %d %s", r.status, r.body)
		}
	})
	t.Run("organiser cannot change the team site", func(t *testing.T) {
		wantTS(t, "organiser write", a.deployTeam(t, slug, team, org.key, "organiser"), 403, "organiser_read_only")
	})
	t.Run("non-member is refused", func(t *testing.T) {
		wantTS(t, "outsider", a.deployTeam(t, slug, team, outsider.key, "outsider"), 404, "owner_not_found")
	})
	t.Run("removed teammate is refused on the next request", func(t *testing.T) {
		wantTS(t, "remove", a.api(t, "DELETE", "/v1/hack/events/"+slug+"/team/members/"+a.uid(t, mate), nil, admin.key), 204, "")
		wantTS(t, "removed mate", a.deployTeam(t, slug, team, mate.key, "removed"), 404, "owner_not_found")
	})
	t.Run("after the deadline publishing is closed", func(t *testing.T) {
		wantTS(t, "deadline", a.api(t, "PATCH", "/v1/hack/events/"+slug, map[string]string{"submission_deadline": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)}, org.key), 200, "")
		wantTS(t, "late", a.deployTeam(t, slug, team, admin.key, "late"), 409, "submissions_closed")
	})
}
