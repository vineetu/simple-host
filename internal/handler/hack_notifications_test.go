package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/mcp"
)

type hackNotifyFixture struct {
	a                          *teamSiteApp
	x                          *notificationReceiver
	org, member, other         person
	slug, team, key, sid, base string
}

func newHackNotifyFixture(t *testing.T) hackNotifyFixture {
	t.Helper()
	a := newTeamSiteApp(t)
	x := newNotificationReceiver(t)
	if err := a.sites.SetPasscodeKey(testPasscodeKey); err != nil {
		t.Fatal(err)
	}
	a.sites.notifyEngine().client = x.client()
	a.sites.notifyEngine().client.secrets = &a.sites.passcode
	a.sites.notifyEngine().perURL = newRateLimiter(100000, 0)
	a.conn.SetEventsBackend(a.sites.EventsBackend())
	org, member, other := a.newPerson(t, "hn-org"), a.newPerson(t, "hn-member"), a.newPerson(t, "hn-other")
	slug := a.makeEvent(t, org)
	a.join(t, slug, member, org)
	a.join(t, slug, other, org)
	team, code := a.startTeam(t, slug, "Notifications", member)
	wantTS(t, "join teammate", a.api(t, "POST", "/v1/hack/events/"+slug+"/teams/join", map[string]string{"code": code}, other.key), 200, "")
	markReady(t, a.certDir, slug)
	key := a.teamKey(t, slug, member)
	wantTS(t, "deploy", a.deployTeam(t, team, key, "notify"), 201, "")
	return hackNotifyFixture{a, x, org, member, other, slug, team, key, hackSiteID(t, a, slug, team), "/v1/sites/" + team + "/notifications"}
}
func (f hackNotifyFixture) body() map[string]any {
	return map[string]any{"url": f.x.server.URL, "secret": notifyTestSecret, "events": []string{"record.added", "record.changed", "file.uploaded"}, "auth_header": map[string]string{"name": "Authorization", "value": "Bearer receiver"}}
}
func (f hackNotifyFixture) sql(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := f.a.database.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}
func (f hackNotifyFixture) create(t *testing.T, base string, p person) string {
	t.Helper()
	r := f.a.api(t, "POST", base, f.body(), p.key)
	if r.status != 201 {
		t.Fatalf("create notification: status %d", r.status)
	}
	return r.json(t)["notification"].(map[string]any)["id"].(string)
}
func (f hackNotifyFixture) drain(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for len(f.a.sites.notifyEngine().emitting) > 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(f.a.sites.notifyEngine().emitting) > 0 {
		t.Fatal("emit timeout")
	}
	for i := 0; i < 100; i++ {
		if !f.a.sites.deliverNotification(context.Background()) {
			return
		}
	}
	t.Fatal("delivery queue did not drain")
}
func (f hackNotifyFixture) emit(t *testing.T) {
	t.Helper()
	if err := db.EmitSiteEvent(context.Background(), f.a.database, f.sid, notifyRandom("evt_", 16), "record.added", "kv", []byte(`{"v":1}`)); err != nil {
		t.Fatal(err)
	}
}
func (f hackNotifyFixture) gone(t *testing.T, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if _, err := db.GetNotification(context.Background(), f.a.database, id); !errors.Is(err, sql.ErrNoRows) {
			t.Fatal("membership loss retained subscription")
		}
	}
}
func TestHackNotificationsTeamVisitorDelivery(t *testing.T) {
	f := newHackNotifyFixture(t)
	a := f.a
	id := f.create(t, f.base, f.member)
	n, err := db.GetNotification(context.Background(), a.database, id)
	if err != nil {
		t.Fatal(err)
	}
	if n.OwnerID != a.uid(t, f.member) {
		t.Fatal("subscription not bound to member")
	}
	assertNotificationSealed(t, n.Secret, notifyTestSecret)
	assertNotificationSealed(t, n.AuthHeaderValue, "Bearer receiver")
	st := "/v1/sites/" + f.team + "/storage"
	for name, kind := range map[string]string{"kv": "kv", "rows": "sqlite", "files": "files"} {
		wantTS(t, "resource", a.api(t, "PUT", st+"/resources/"+name, map[string]string{"kind": kind, "preset": "inbox"}, f.key), 201, "")
	}
	wantTS(t, "schema", a.api(t, "POST", st+"/sqlite/rows/schema", map[string]string{"sql": "CREATE TABLE entries(id INTEGER PRIMARY KEY, body TEXT)"}, f.key), 200, "")
	// Owner credential writes do not emit.
	wantTS(t, "owner writes", a.api(t, "PUT", st+"/kv/kv/keys/owner", `{"value":1}`, f.key), 200, "")
	f.drain(t)
	if len(f.x.snapshot()) != 0 {
		t.Fatal("owner emitted")
	}
	visitor := hackSession(t, a, "", f.sid, f.team+"."+f.slug+"."+tsDomain)
	wantTS(t, "visitor KV", visitor("PUT", st+"/kv/kv/keys/visitor", `{"value":{"message":"hello"}}`), 200, "")
	wantTS(t, "visitor row", visitor("POST", st+"/sqlite/rows/tables/entries/rows", `{"body":"hello"}`), 200, "")
	wantTS(t, "visitor file", visitor("PUT", st+"/files/files/objects/hello.txt", "hello", "Content-Type", "text/plain"), 200, "")
	f.drain(t)
	got := f.x.snapshot()
	if len(got) != 3 {
		t.Fatalf("visitor deliveries: %d", len(got))
	}
	names := map[string]int{}
	for _, r := range got {
		names[r.body["name"].(string)]++
		if r.header.Get("Authorization") != "Bearer receiver" || r.header.Get("svix-id") == "" {
			t.Fatal("missing webhook headers")
		}
		data := r.body["data"].(map[string]any)
		if data["site"] != f.team || data["site_url"] != "https://"+f.team+"."+f.slug+"."+tsDomain+"/" {
			t.Fatal("wrong team address in payload")
		}
		if data["visitor"] != nil {
			t.Fatal("anonymous visitor identity")
		}
	}
	if names["record.added"] != 2 || names["file.uploaded"] != 1 {
		t.Fatal("wrong events")
	}
	history := a.api(t, "GET", f.base+"/"+id+"/deliveries", nil, f.member.key)
	if history.status != 200 || len(history.json(t)["deliveries"].([]any)) != 3 {
		t.Fatal("delivery history")
	}
	wantTS(t, "other member cannot see history", a.api(t, "GET", f.base+"/"+id+"/deliveries", nil, f.other.key), 404, "")
	wantTS(t, "team key excluded", a.api(t, "POST", f.base, f.body(), f.key), 403, "")
	wantTS(t, "organiser cannot subscribe to team", a.api(t, "POST", f.base, f.body(), f.org.key), 404, "")
	// Deadline prevents new subscriptions, but allows existing deliveries and rotation.
	f.sql(t, `UPDATE events SET submission_deadline=now()-interval '1 minute' WHERE slug=$1`, f.slug)
	wantTS(t, "deadline", a.api(t, "POST", f.base, f.body(), f.member.key), 403, "notification_access_ended")
	f.emit(t)
	f.drain(t)
	if len(f.x.snapshot()) != 4 {
		t.Fatal("deadline stopped delivery")
	}
	rotated := a.api(t, "POST", f.base+"/"+id+"/rotate-secret", nil, f.member.key)
	if rotated.status != 200 {
		t.Fatal("rotate after deadline")
	}
	n, err = db.GetNotification(context.Background(), a.database, id)
	if err != nil {
		t.Fatal(err)
	}
	assertNotificationSealed(t, n.Secret, rotated.json(t)["secret"].(string))
	assertNotificationSealed(t, n.PrevSecret, notifyTestSecret)
	f.x.mu.Lock()
	f.x.keys = append(f.x.keys, rotated.json(t)["secret"].(string))
	f.x.mu.Unlock()
	tested := a.api(t, "POST", f.base+"/"+id+"/test", nil, f.member.key)
	if tested.status != 200 || tested.json(t)["delivered"] != true {
		t.Fatal("signed test after rotation")
	}
	// Removal deletes subscriptions and their pending deliveries immediately.
	f.emit(t)
	wantTS(t, "remove member", a.api(t, "DELETE", "/v1/hack/events/"+f.slug+"/teams/"+f.team+"/members/"+a.uid(t, f.member), nil, f.org.key), 204, "")
	f.gone(t, id)
	f.drain(t)
	if len(f.x.snapshot()) != 5 {
		t.Fatal("delivered after removal")
	}
}

func TestHackNotificationsMembershipAndSecretGate(t *testing.T) {
	f := newHackNotifyFixture(t)
	a := f.a
	id := f.create(t, f.base, f.member)
	// Losing approval permanently removes the subscription, even if restored.
	f.sql(t, `UPDATE event_members SET approval_status='pending' WHERE user_id=$1`, a.uid(t, f.member))
	f.gone(t, id)
	wantTS(t, "approval required", a.api(t, "POST", f.base, f.body(), f.member.key), 404, "")
	f.sql(t, `UPDATE event_members SET approval_status='approved' WHERE user_id=$1`, a.uid(t, f.member))
	id = f.create(t, f.base, f.member)
	wantTS(t, "leave", a.api(t, "POST", "/v1/hack/events/"+f.slug+"/teams/leave", nil, f.member.key), 200, "")
	f.gone(t, id)
	// No encryption key: no new subscriptions, no capability, removal still works.
	otherID := f.create(t, f.base, f.other)
	if err := a.sites.SetPasscodeKey(""); err != nil {
		t.Fatal(err)
	}
	wantTS(t, "missing key", a.api(t, "POST", f.base, f.body(), f.other.key), 409, "notifications_not_enabled")
	wantTS(t, "missing key rotate", a.api(t, "POST", f.base+"/"+otherID+"/rotate-secret", nil, f.other.key), 409, "notifications_not_enabled")
	client := a.registerClient(t, testRedirect)
	access := a.connect(t, f.other, client, testRedirect)["access_token"].(string)
	caps := a.rpc(t, access, "initialize", nil).json(t)["result"].(map[string]any)["capabilities"].(map[string]any)
	if _, ok := caps["events"]; ok {
		t.Fatal("events without key")
	}
	wantTS(t, "keyless remove", a.api(t, "DELETE", f.base+"/"+otherID, nil, f.other.key), 200, "")
}

func TestHackNotificationsMCPSelectedTeamAndOrganiser(t *testing.T) {
	f := newHackNotifyFixture(t)
	a := f.a
	client := a.registerClient(t, testRedirect)
	access := a.connect(t, f.member, client, testRedirect)["access_token"].(string)
	orgAccess := a.connect(t, f.org, client, testRedirect)["access_token"].(string)
	caps := a.rpc(t, access, "initialize", nil).json(t)["result"].(map[string]any)["capabilities"].(map[string]any)
	if _, ok := caps["events"]; !ok {
		t.Fatal("Hack Events missing")
	}
	listed := a.rpc(t, access, "tools/list", nil)
	for _, name := range []string{"hack_list_events", "storage_set_resource", "list_notifications", "remove_notification"} {
		if !strings.Contains(string(listed.body), `"`+name+`"`) {
			t.Fatal("full tool list missing " + name)
		}
	}
	subscribe := func(token string, args map[string]any, allowed bool) string {
		t.Helper()
		r := a.rpc(t, token, "events/subscribe", map[string]any{"name": "record.added", "arguments": args, "delivery": map[string]string{"mode": "webhook", "url": f.x.server.URL, "secret": notifyTestSecret}}).json(t)
		if allowed {
			result, ok := r["result"].(map[string]any)
			if !ok {
				t.Fatal("subscribe refused")
			}
			return result["id"].(string)
		}
		if r["error"] == nil {
			t.Fatal("subscribe unexpectedly allowed")
		}
		return ""
	}
	args := map[string]any{"site": f.team}
	subscribe(access, args, false)
	var teamID string
	if err := a.database.QueryRow(`SELECT team_id FROM event_members WHERE user_id=$1`, a.uid(t, f.member)).Scan(&teamID); err != nil {
		t.Fatal(err)
	}
	_, _, failed := toolResultOf(t, a.rpc(t, access, "tools/call", map[string]any{"name": "hack_select_team", "arguments": map[string]any{"team_id": teamID}}))
	if failed {
		t.Fatal("select team")
	}
	id := subscribe(access, args, true)
	// A different team site is never reachable through the selected grant.
	rival := a.newPerson(t, "hn-rival")
	a.join(t, f.slug, rival, f.org)
	rivalTeam, _ := a.startTeam(t, f.slug, "Rival", rival)
	wantTS(t, "rival deploy", a.deployTeam(t, rivalTeam, a.teamKey(t, f.slug, rival), "rival"), 201, "")
	subscribe(access, map[string]any{"site": rivalTeam}, false)
	_, out, failed := toolResultOf(t, a.rpc(t, access, "tools/call", map[string]any{"name": "list_notifications", "arguments": args}))
	if failed || len(out["notifications"].([]any)) != 1 {
		t.Fatal("personal notification tool identity")
	}
	// Existing MCP subscriptions can refresh after the deadline, but new ones cannot.
	f.sql(t, `UPDATE events SET submission_deadline=now()-interval '1 minute' WHERE slug=$1`, f.slug)
	if got := subscribe(access, args, true); got != id {
		t.Fatal("refresh changed subscription")
	}
	subscribe(access, map[string]any{"site": f.team, "storage": ""}, false)
	evBase := "/v1/hack/events/" + f.slug + "/website"
	wantTS(t, "event deploy", a.api(t, "PUT", evBase+"/files?create=1", deployBody("event"), f.org.key), 201, "")
	eventArgs := map[string]any{"event": f.slug}
	subscribe(access, eventArgs, false)
	eventID := subscribe(orgAccess, eventArgs, true)
	_, out, failed = toolResultOf(t, a.rpc(t, orgAccess, "tools/call", map[string]any{"name": "list_notifications", "arguments": eventArgs}))
	if failed || len(out["notifications"].([]any)) != 1 || out["notifications"].([]any)[0].(map[string]any)["event"] != f.slug {
		t.Fatal("organiser tool context")
	}
	// Ownership in another event cannot be borrowed.
	another := a.makeEvent(t, f.other)
	wantTS(t, "other event site", a.api(t, "PUT", "/v1/hack/events/"+another+"/website/files?create=1", deployBody("other"), f.other.key), 201, "")
	subscribe(orgAccess, map[string]any{"event": another}, false)
	n, err := db.GetNotification(context.Background(), a.database, eventID)
	if err != nil {
		t.Fatal(err)
	}
	assertNotificationSealed(t, n.Secret, notifyTestSecret)
	if n.OwnerID != a.uid(t, f.org) {
		t.Fatal("organiser subscription owner")
	}
	// Worker signs organiser visitor events; ended events skip further deliveries.
	st := evBase + "/storage"
	wantTS(t, "event inbox", a.api(t, "PUT", st+"/resources/rows", map[string]string{"kind": "sqlite", "preset": "inbox"}, f.org.key), 201, "")
	wantTS(t, "event schema", a.api(t, "POST", st+"/sqlite/rows/schema", map[string]string{"sql": "CREATE TABLE entries(id INTEGER PRIMARY KEY, body TEXT)"}, f.org.key), 200, "")
	visitor := hackSession(t, a, "", n.SiteID, f.slug+"."+tsDomain)
	before := len(f.x.snapshot())
	wantTS(t, "event visitor", visitor("POST", "/v1/sites/"+f.slug+"/storage/sqlite/rows/tables/entries/rows", `{"body":"hello"}`), 200, "")
	f.drain(t)
	if len(f.x.snapshot()) != before+1 {
		t.Fatal("organiser visitor delivery missing")
	}
	payload := f.x.snapshot()[before].body["data"].(map[string]any)
	if payload["site"] != f.slug || payload["site_url"] != "https://"+f.slug+"."+tsDomain+"/" {
		t.Fatal("wrong organiser website address in payload")
	}
	wantTS(t, "end event", a.api(t, "POST", "/v1/hack/events/"+f.slug+"/stage", map[string]string{"stage": "archived"}, f.org.key), 200, "")
	subscribe(orgAccess, eventArgs, false)
	if err := db.EmitSiteEvent(context.Background(), a.database, n.SiteID, notifyRandom("evt_", 16), "record.added", "rows", json.RawMessage(`{"v":1}`)); err != nil {
		t.Fatal(err)
	}
	f.drain(t)
	if len(f.x.snapshot()) != before+1 {
		t.Fatal("organiser delivery after event ended")
	}
	wantTS(t, "history after event ended", a.api(t, "GET", evBase+"/notifications/"+eventID+"/deliveries", nil, f.org.key), 200, "")
	// Revoking the connection removes the subscription through the existing FK.
	if n.GrantID == nil {
		t.Fatal("missing grant binding")
	}
	f.sql(t, `DELETE FROM oauth_grants WHERE id=$1`, *n.GrantID)
	f.gone(t, eventID)
}

func TestHackNotificationEventAccountExcluded(t *testing.T) {
	f := newHackNotifyFixture(t)
	var holder string
	if err := f.a.database.QueryRow(`SELECT account_id FROM events WHERE slug=$1`, f.slug).Scan(&holder); err != nil {
		t.Fatal(err)
	}
	_, err := f.a.sites.EventsBackend().Subscribe(context.Background(), mcp.Caller{Owner: holder, Mode: mcp.CallerModeEvents}, mcp.EventSubscribeRequest{Site: f.team})
	if err == nil {
		t.Fatal("holding account accepted")
	}
}

func TestHackNotificationsSameNamedTeams(t *testing.T) {
	f := newHackNotifyFixture(t)
	a := f.a
	slug2 := a.makeEvent(t, f.org)
	a.join(t, slug2, f.member, f.org)
	team2, _ := a.startTeam(t, slug2, "Notifications", f.member)
	if team2 != f.team {
		t.Fatal("fixture needs identical team slugs")
	}
	markReady(t, a.certDir, slug2)
	wantTS(t, "second site", a.deployTeam(t, team2, a.teamKey(t, slug2, f.member), "second"), 201, "")
	// An unscoped REST request refuses ambiguity instead of taking the first site.
	wantTS(t, "ambiguous site", a.api(t, "POST", f.base, f.body(), f.member.key), 404, "")
	id := f.create(t, f.base+"?event="+f.slug, f.member)
	otherID := f.create(t, f.base+"?event="+slug2, f.member)
	n, err := db.GetNotification(context.Background(), a.database, id)
	if err != nil {
		t.Fatal(err)
	}
	other, err := db.GetNotification(context.Background(), a.database, otherID)
	if err != nil {
		t.Fatal(err)
	}
	if n.SiteID != f.sid || other.SiteID == f.sid {
		t.Fatal("REST selected the wrong event")
	}
	client := a.registerClient(t, testRedirect)
	access := a.connect(t, f.member, client, testRedirect)["access_token"].(string)
	var teamID string
	if err := a.database.QueryRow(`SELECT t.id FROM event_teams t JOIN events e ON e.id=t.event_id WHERE e.slug=$1 AND t.slug=$2`, slug2, team2).Scan(&teamID); err != nil {
		t.Fatal(err)
	}
	_, _, failed := toolResultOf(t, a.rpc(t, access, "tools/call", map[string]any{"name": "hack_select_team", "arguments": map[string]any{"team_id": teamID}}))
	if failed {
		t.Fatal("select")
	}
	args := map[string]any{"site": team2}
	_, out, failed := toolResultOf(t, a.rpc(t, access, "tools/call", map[string]any{"name": "list_notifications", "arguments": args}))
	if failed || len(out["notifications"].([]any)) != 1 || out["notifications"].([]any)[0].(map[string]any)["id"] != otherID {
		t.Fatal("tool resolved wrong event")
	}
	rpc := a.rpc(t, access, "events/subscribe", map[string]any{"name": "record.added", "arguments": args, "delivery": map[string]string{"mode": "webhook", "url": f.x.server.URL, "secret": notifyTestSecret}}).json(t)
	result, ok := rpc["result"].(map[string]any)
	if !ok {
		t.Fatal("MCP subscribe")
	}
	sub, err := db.GetNotification(context.Background(), a.database, result["id"].(string))
	if err != nil || sub.SiteID != other.SiteID {
		t.Fatal("MCP resolved wrong event")
	}
	// The notification tool also preserves context when adding the deletion suffix.
	_, _, failed = toolResultOf(t, a.rpc(t, access, "tools/call", map[string]any{"name": "remove_notification", "arguments": map[string]any{"site": team2, "id": otherID}}))
	if failed {
		t.Fatal("remove")
	}
	f.gone(t, otherID)
	if _, err := db.GetNotification(context.Background(), a.database, id); err != nil {
		t.Fatal("removed other event subscription")
	}
}

func TestHackNotificationsWorkerAndMembershipMove(t *testing.T) {
	f := newHackNotifyFixture(t)
	a := f.a
	id := f.create(t, f.base, f.member)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.sites.StartNotifications(ctx)
	f.emit(t)
	deadline := time.Now().Add(5 * time.Second)
	for len(f.x.snapshot()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(f.x.snapshot()) != 1 {
		t.Fatal("Hack worker did not deliver")
	}
	cancel()
	// Moving teams cannot leave a live subscription to the old team.
	f.sql(t, `UPDATE event_members SET team_id=NULL WHERE user_id=$1`, a.uid(t, f.member))
	f.gone(t, id)
	// Membership changes serialize with creation, including removal while a save waits.
	tx, err := a.database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE event_members SET team_id=NULL WHERE user_id=$1`, a.uid(t, f.other)); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	n := db.Notification{ID: notifyRandom("sub_", 12), SiteID: f.sid, OwnerID: a.uid(t, f.other), Hack: true, Source: "owner", URL: f.x.server.URL, Events: []string{"record.added"}, Secret: notifyTestSecret, VerifiedAt: &now}
	done := make(chan error, 1)
	go func() {
		_, _, err := db.SaveNotification(context.Background(), a.database, n, 10, nil, 7, a.sites.notificationSecrets())
		done <- err
	}()
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, db.ErrNotificationAccess) {
			t.Fatal("save crossed membership removal")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("save timed out")
	}
	f.gone(t, n.ID)
}
