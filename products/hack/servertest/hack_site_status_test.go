package servertest

import (
	"net/http"
	"testing"
)

// GET .../site names the event as owner and says whether the person has a
// connected app: none before connecting, connected after.
func TestHackSiteStatusConnection(t *testing.T) {
	a := newTeamSiteApp(t)
	org, p1 := a.newPerson(t, "org"), a.newPerson(t, "p1")
	ev := a.makeEvent(t, org)
	a.join(t, ev, p1, org)
	a.startTeam(t, ev, "Alpha", p1)

	none := a.api(t, "GET", "/v1/hack/events/"+ev+"/site", nil, p1.key)
	if none.status != http.StatusOK || none.json(t)["owner"] != ev {
		t.Fatalf("site status: %d %s", none.status, none.body)
	}
	if conn := none.json(t)["connection"].(map[string]any); conn["state"] != "none" || conn["app"] != nil {
		t.Fatalf("none state: %v", conn)
	}

	a.connect(t, p1, a.registerClient(t, testRedirect), testRedirect)
	connected := a.api(t, "GET", "/v1/hack/events/"+ev+"/site", nil, p1.key).json(t)["connection"].(map[string]any)
	if connected["state"] != "connected" || connected["app"] != "Test Chat" || connected["last_used_at"] == nil {
		t.Fatalf("connected state: %v", connected)
	}
}
