package servertest

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	notifyrepo "github.com/vsriram/simple-host/host/data/repo/notify"
)

func rhhDrainNotifications(t *testing.T, f hackNotifyFixture) []receivedNotification {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for f.a.data.PendingNotificationsForTest() > 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	for i := 0; i < 100 && f.a.data.DeliverNotificationForTest(context.Background()); i++ {
	}
	f.x.mu.Lock()
	defer f.x.mu.Unlock()
	return append([]receivedNotification(nil), f.x.requests...)
}

// TestRhhNotificationsScopedToSubscriber: a visitor's write on a team site is
// delivered to the subscribing member only (history included), organisers
// cannot subscribe to team sites, and the subscription is revoked when the
// member is moved off the team or removed from it.
func TestRhhNotificationsScopedToSubscriber(t *testing.T) {
	f := newHackNotifyFixture(t)
	a := f.a
	id := f.create(t, f.base+f.own, f.member)
	n, err := notifyrepo.GetNotification(context.Background(), a.database, id)
	if err != nil || n.OwnerID != a.uid(t, f.member) {
		t.Fatalf("subscription not bound to the member: %v", err)
	}
	st := "/v1/sites/" + f.team + "/storage"
	wantTS(t, "resource", a.api(t, "PUT", st+"/resources/kv"+f.own, map[string]any{"kind": "kv", "rules": json.RawMessage(inboxRules)}, f.key), 201, "")
	visitor := hackSession(t, a, "", f.sid, f.team+"."+f.slug+"."+tsDomain)
	wantTS(t, "visitor KV", visitor("PUT", st+"/kv/kv/keys/visitor", `{"value":{"message":"hello"}}`), 200, "")
	if got := rhhDrainNotifications(t, f); len(got) != 1 {
		t.Fatalf("visitor deliveries: %d", len(got))
	}
	history := a.api(t, "GET", f.base+"/"+id+"/deliveries"+f.own, nil, f.member.key)
	if history.status != 200 || len(history.json(t)["deliveries"].([]any)) != 1 {
		t.Fatalf("delivery history: %d %s", history.status, history.body)
	}
	wantTS(t, "other member history", a.api(t, "GET", f.base+"/"+id+"/deliveries"+f.own, nil, f.other.key), 404, "")
	wantTS(t, "organiser subscribes to team", a.api(t, "POST", f.base+f.own, f.body(), f.org.key), 403, "organiser_read_only")

	otherID := f.create(t, f.base+f.own, f.other)
	f.sql(t, `UPDATE event_members SET team_id=NULL WHERE user_id=$1`, a.uid(t, f.member))
	f.gone(t, id)
	wantTS(t, "remove teammate", a.api(t, "DELETE", "/v1/hack/events/"+f.slug+"/teams/"+f.team+"/members/"+a.uid(t, f.other), nil, f.org.key), 204, "")
	f.gone(t, otherID)
}

// TestRhhNotificationsMCPSubscriptionScope: over MCP a member subscribes to
// their own team's site only; an organiser subscribes to their own event's
// website, not to another event's, and a member cannot take the organisers'.
func TestRhhNotificationsMCPSubscriptionScope(t *testing.T) {
	f := newHackNotifyFixture(t)
	a := f.a
	client := a.registerClient(t, testRedirect)
	access := a.connect(t, f.member, client, testRedirect)["access_token"].(string)
	orgAccess := a.connect(t, f.org, client, testRedirect)["access_token"].(string)
	subscribe := func(token string, args map[string]any, allowed bool) {
		t.Helper()
		r := a.rpc(t, token, "events/subscribe", map[string]any{"name": "record.added", "arguments": args, "delivery": map[string]string{"mode": "webhook", "url": f.x.server.URL, "secret": notifyTestSecret}}).json(t)
		if _, ok := r["result"].(map[string]any); ok != allowed {
			t.Fatalf("subscribe %v: allowed=%v, want %v: %v", args, ok, allowed, r)
		}
	}
	siteOf := func(slug string) string {
		var id string
		if err := a.database.QueryRow(`SELECT id FROM events WHERE slug=$1`, slug).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	rival := a.newPerson(t, "rhh-rival")
	a.join(t, f.slug, rival, f.org)
	rivalTeam, _ := a.startTeam(t, f.slug, "Rival", rival)
	wantTS(t, "rival deploy", a.deployTeam(t, f.slug, rivalTeam, rival.key, "rival"), 201, "")
	eventSite := siteOf(f.slug)
	wantTS(t, "event deploy", a.deployTeam(t, f.slug, eventSite, f.org.key, "event"), 201, "")
	another := a.makeEvent(t, f.other)
	anotherSite := siteOf(another)
	wantTS(t, "other event site", a.deployTeam(t, another, anotherSite, f.other.key, "other"), 201, "")

	subscribe(access, map[string]any{"site": f.team, "owner": f.slug}, true)
	subscribe(access, map[string]any{"site": rivalTeam, "owner": f.slug}, false)
	subscribe(access, map[string]any{"site": eventSite, "owner": f.slug}, false)
	subscribe(orgAccess, map[string]any{"site": eventSite, "owner": f.slug}, true)
	subscribe(orgAccess, map[string]any{"site": anotherSite, "owner": another}, false)
}
