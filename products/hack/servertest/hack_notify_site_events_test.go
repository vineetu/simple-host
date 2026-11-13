package servertest

import (
	"testing"

	"github.com/vsriram/simple-host/internal/mcp"
)

// Site events (SIM-356): the engine side only. The hooks that call
// emitSiteNotification from deploys, domain checks and sign-ins are covered
// in notify_hooks_test.go.

func TestNotifySiteEventsHack(t *testing.T) {
	f := newHackNotifyFixture(t)
	a := f.a

	body := f.body()
	body["events"] = []string{"site.deployed"}
	wantTS(t, "hack site event", a.api(t, "POST", f.base, body, f.member.key), 400, "invalid_events")
	body["events"] = []string{"record.added", "visitor.signed_up"}
	wantTS(t, "hack mixed events", a.api(t, "POST", f.base, body, f.member.key), 400, "invalid_events")
	client := a.registerClient(t, testRedirect)
	access := a.connect(t, f.member, client, testRedirect)["access_token"].(string)
	listed := a.rpc(t, access, "events/list", nil).json(t)["result"].(map[string]any)["events"].([]any)
	if len(listed) != 3 {
		t.Fatalf("Hack events/list: %d definitions", len(listed))
	}
	r := a.rpc(t, access, "events/subscribe", map[string]any{"name": "site.deployed", "arguments": map[string]any{"site": f.team}, "delivery": map[string]string{"mode": "webhook", "url": f.x.server.URL, "secret": notifyTestSecret}}).json(t)
	e, _ := r["error"].(map[string]any)
	if e == nil || e["code"] != float64(mcp.EventCodeNotFound) || e["data"].(map[string]any)["kind"] != "event" {
		t.Fatalf("Hack site event subscribe: %v", r)
	}
	// No site event is emitted on Hack.
	f.create(t, f.base, f.member)
	wantTS(t, "deploy emits no Hack site event", a.deployTeam(t, f.team, f.key, "version two"), 200, "")
	f.drain(t)
	var n int
	if err := a.database.QueryRow(`SELECT count(*) FROM site_events WHERE site_id=$1 AND name='site.deployed'`, f.sid).Scan(&n); err != nil || n != 0 {
		t.Fatalf("Hack site event stored: %d %v", n, err)
	}
}
