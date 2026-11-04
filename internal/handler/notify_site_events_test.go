package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/mcp"
	model "github.com/vsriram/simple-host/internal/model"
	notificationsrepo "github.com/vsriram/simple-host/internal/repository/notifications"
)

// Site events (SIM-356): the engine side only. The hooks that call
// emitSiteNotification from deploys, domain checks and sign-ins are covered
// in notify_hooks_test.go.

func siteEventCount(t *testing.T, s presetSite, name string) int {
	t.Helper()
	var n int
	if err := s.a.database.QueryRow(`SELECT count(*) FROM site_events WHERE site_id=$1 AND name=$2`, s.sid, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func siteEventDeliveries(x *notificationReceiver, name string) []map[string]any {
	var out []map[string]any
	for _, r := range x.snapshot() {
		if r.body["name"] == name {
			out = append(out, r.body)
		}
	}
	return out
}

func TestNotifySiteEventsRESTAndDelivery(t *testing.T) {
	s, x := notificationFixture(t)
	all := s.a.sites.notifyEventNames()
	if len(all) != 7 {
		t.Fatalf("event names: %v", all)
	}
	r := notificationRequest(t, s, "POST", "", map[string]any{"url": x.server.URL, "events": all, "secret": notifyTestSecret}, 201)
	created := r.json(t)["notification"].(map[string]any)
	if got := created["events"].([]any); len(got) != 7 {
		t.Fatalf("seven events not stored: %v", got)
	}
	notificationRequest(t, s, "DELETE", "/"+created["id"].(string), nil, 200)
	for _, events := range [][]string{{"record.added", "site.nope"}, {"site.deployed", "site.deployed"}} {
		r := notificationRequest(t, s, "POST", "", map[string]any{"url": x.server.URL, "events": events, "secret": notifyTestSecret}, 400)
		if r.json(t)["code"] != "invalid_events" {
			t.Fatalf("%v: code %v", events, r.json(t)["code"])
		}
	}

	// A storage filter narrows record events only: a site event reaches the
	// subscription whatever its storage filter.
	s.must(t, "PUT", "/resources/orders", map[string]string{"kind": "sqlite", "preset": "personal"}, s.key, 201)
	waitNotificationEmits(t, s)
	notificationRequest(t, s, "POST", "", map[string]any{"url": x.server.URL, "events": []string{"record.added", "site.deployed", "viewer.access_requested", "visitor.signed_up"}, "storage": "orders", "secret": notifyTestSecret}, 201)
	s.a.sites.emitSiteNotification(s.sid, "site.deployed", map[string]any{"version": 2, "previous_version": 1, "via": "deploy"}, 0)
	waitNotificationEmits(t, s)
	drainNotifications(t, s)
	got := siteEventDeliveries(x, "site.deployed")
	if len(got) != 1 {
		t.Fatalf("want one site.deployed delivery, got %d", len(got))
	}
	data := got[0]["data"].(map[string]any)
	if data["v"] != float64(1) || data["site"] != "shop" || !strings.HasPrefix(data["site_url"].(string), "https://shop.") {
		t.Fatalf("common fields: %v", data)
	}
	if data["version"] != float64(2) || data["previous_version"] != float64(1) || data["via"] != "deploy" {
		t.Fatalf("site.deployed fields: %v", data)
	}
	for _, key := range []string{"storage", "kind", "preset", "visitor", "untrusted", "truncated"} {
		if _, present := data[key]; present {
			t.Fatalf("site event carries %s: %v", key, data)
		}
	}
	if len(data) != 6 {
		t.Fatalf("unexpected keys: %v", data)
	}
	var stored model.NotificationDelivery
	var storage string
	if err := s.a.database.QueryRow(`SELECT storage, name FROM site_events WHERE site_id=$1 AND name='site.deployed'`, s.sid).Scan(&storage, &stored.Name); err != nil || storage != "" {
		t.Fatalf("site event storage %q (%v)", storage, err)
	}

	// The owner signing in on their own site is not an event.
	s.a.sites.emitSiteNotification(s.sid, "visitor.signed_up", map[string]any{"visitor": s.ownerID}, 0)
	waitNotificationEmits(t, s)
	if siteEventCount(t, s, "visitor.signed_up") != 0 {
		t.Fatal("owner event stored")
	}
	// Once per person per site inside the dedupe window.
	for i := 0; i < 2; i++ {
		s.a.sites.emitSiteNotification(s.sid, "viewer.access_requested", map[string]any{"visitor": s.aliceID}, 24*time.Hour)
		waitNotificationEmits(t, s)
	}
	if siteEventCount(t, s, "viewer.access_requested") != 1 {
		t.Fatal("repeat access request stored")
	}
	// Another person inside the same window is their own event.
	s.a.sites.emitSiteNotification(s.sid, "viewer.access_requested", map[string]any{"visitor": s.ids["bob"]}, 24*time.Hour)
	waitNotificationEmits(t, s)
	if siteEventCount(t, s, "viewer.access_requested") != 2 {
		t.Fatal("second person's access request dropped")
	}
	drainNotifications(t, s)
	requested := siteEventDeliveries(x, "viewer.access_requested")
	if len(requested) != 2 {
		t.Fatalf("want two access-request deliveries, got %d", len(requested))
	}
	// Plain webhooks carry the visitor ID, the same stamp as record events.
	if d := requested[0]["data"].(map[string]any); d["visitor"] != s.aliceID || d["site"] != "shop" {
		t.Fatalf("access request data: %v", d)
	}
	if siteEventDeliveries(x, "visitor.signed_up") != nil {
		t.Fatal("owner event delivered")
	}
}

func siteEventRPC(t *testing.T, s presetSite, server *mcp.Server, method string, params map[string]any) map[string]any {
	t.Helper()
	if params == nil {
		params = map[string]any{}
	}
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	req.Header.Set("MCP-Protocol-Version", "2025-11-25")
	req = req.WithContext(mcp.WithCaller(req.Context(), mcp.Caller{Owner: s.ownerID, APIKey: "test-key", AppName: "Test app"}))
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("%s: %v: %s", method, err, rec.Body.String())
	}
	return out
}

func TestNotifySiteEventsMCP(t *testing.T) {
	s, x := notificationFixture(t)
	server := mcp.NewServer(mcp.Config{Events: s.a.sites.EventsBackend()})
	listed := siteEventRPC(t, s, server, "events/list", nil)["result"].(map[string]any)["events"].([]any)
	if len(listed) != 7 {
		t.Fatalf("events/list: %d definitions", len(listed))
	}
	for _, raw := range listed[3:] {
		event := raw.(map[string]any)
		if !isNotifySiteEvent(event["name"].(string)) {
			t.Fatalf("site events do not follow record events: %v", event["name"])
		}
		props := event["inputSchema"].(map[string]any)["properties"].(map[string]any)
		if len(props) != 1 || props["site"] == nil {
			t.Fatalf("%s inputSchema: %v", event["name"], props)
		}
	}
	delivery := map[string]any{"mode": "webhook", "url": x.server.URL, "secret": notifyTestSecret}
	out := siteEventRPC(t, s, server, "events/subscribe", map[string]any{"name": "site.deployed", "arguments": map[string]any{"site": "shop", "storage": "orders"}, "delivery": delivery})
	if e, _ := out["error"].(map[string]any); e == nil || e["code"] != float64(-32602) {
		t.Fatalf("storage on a site event: %v", out)
	}
	out = siteEventRPC(t, s, server, "events/subscribe", map[string]any{"name": "visitor.signed_up", "arguments": map[string]any{"site": "shop"}, "delivery": delivery})
	result, _ := out["result"].(map[string]any)
	if result == nil {
		t.Fatalf("subscribe: %v", out)
	}
	n, err := notificationsrepo.GetNotification(context.Background(), s.a.database, result["id"].(string))
	if err != nil || n.Storage != "" || n.Source != "mcp" || len(n.Events) != 1 || n.Events[0] != "visitor.signed_up" || n.ArgsCanonical != `{"site":"shop"}` {
		t.Fatalf("stored MCP site subscription: %+v %v", n, err)
	}
	s.a.sites.emitSiteNotification(s.sid, "visitor.signed_up", map[string]any{"visitor": s.aliceID}, 0)
	waitNotificationEmits(t, s)
	drainNotifications(t, s)
	got := siteEventDeliveries(x, "visitor.signed_up")
	if len(got) != 1 {
		t.Fatalf("want one visitor.signed_up delivery, got %d", len(got))
	}
	expected, err := mcp.NewVisitorReferences(s.a.sites.visitorRefKey()).Encode(s.sid, s.aliceID)
	if err != nil {
		t.Fatal(err)
	}
	data := got[0]["data"].(map[string]any)
	if data["visitor"] != expected || data["visitor"] == s.aliceID {
		t.Fatalf("MCP delivery visitor: %v", data["visitor"])
	}
	raw, _ := json.Marshal(got[0])
	if bytes.Contains(raw, []byte(s.aliceID)) {
		t.Fatal("MCP delivery leaks the account ID")
	}
	if data["v"] != float64(1) || data["site"] != "shop" || len(data) != 4 {
		t.Fatalf("MCP site event data: %v", data)
	}
	// Unsubscribe uses the same site-only arguments.
	out = siteEventRPC(t, s, server, "events/unsubscribe", map[string]any{"name": "visitor.signed_up", "arguments": map[string]any{"site": "shop"}, "delivery": map[string]any{"url": x.server.URL}})
	if out["result"] == nil {
		t.Fatalf("unsubscribe: %v", out)
	}
}

func TestNotifySiteEventsHack(t *testing.T) {
	f := newBridgeHackNotifyFixture(t)
	a := f.a
	if got := a.sites.notifyEventNames(); len(got) != 3 {
		t.Fatalf("Hack event names: %v", got)
	}
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
	a.sites.emitSiteNotification(f.sid, "site.deployed", map[string]any{"version": 2, "previous_version": 1, "via": "deploy"}, 0)
	f.drain(t)
	var n int
	if err := a.database.QueryRow(`SELECT count(*) FROM site_events WHERE site_id=$1 AND name='site.deployed'`, f.sid).Scan(&n); err != nil || n != 0 {
		t.Fatalf("Hack site event stored: %d %v", n, err)
	}
}

// A site event no subscription names stores nothing and spends no budget.
func TestNotifySiteEventsUnsubscribedFree(t *testing.T) {
	s, x := notificationFixture(t)
	notificationRequest(t, s, "POST", "", map[string]any{"url": x.server.URL, "events": []string{"record.added"}, "secret": notifyTestSecret}, 201)
	engine := s.a.sites.notifyEngine()
	engine.minute = newRateLimiter(1, 0)
	s.a.sites.storeSiteNotification(s.sid, "visitor.signed_up", map[string]any{"visitor": s.aliceID}, 0)
	if n := siteEventCount(t, s, "visitor.signed_up"); n != 0 {
		t.Fatalf("unsubscribed site event stored: %d", n)
	}
	if !engine.minute.allow(s.sid) {
		t.Fatal("unsubscribed site event spent the minute budget")
	}
}

// A record write on a site whose subscriptions name only site events stores
// nothing and spends no budget.
func TestNotifyRecordEventsUnsubscribedFree(t *testing.T) {
	s, x := notificationFixture(t)
	notificationRequest(t, s, "POST", "", map[string]any{"url": x.server.URL, "events": []string{"site.deployed"}, "secret": notifyTestSecret}, 201)
	engine := s.a.sites.notifyEngine()
	engine.minute = newRateLimiter(1, 0)
	s.a.sites.storeNotification(storageCall{siteID: s.sid}, "record.added", map[string]any{"visitor": s.aliceID})
	if n := siteEventCount(t, s, "record.added"); n != 0 {
		t.Fatalf("unsubscribed record event stored: %d", n)
	}
	if !engine.minute.allow(s.sid) {
		t.Fatal("unsubscribed record event spent the minute budget")
	}
}

// Two simultaneous refused page loads by one person store one event: the
// dedupe check runs under the site's row lock.
func TestNotifySiteEventsDedupeUnderLock(t *testing.T) {
	s, x := notificationFixture(t)
	notificationRequest(t, s, "POST", "", map[string]any{"url": x.server.URL, "events": []string{"viewer.access_requested"}, "secret": notifyTestSecret}, 201)
	tx, err := s.a.database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`SELECT id FROM sites WHERE id=$1 FOR UPDATE`, s.sid); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		s.a.sites.emitSiteNotification(s.sid, "viewer.access_requested", map[string]any{"visitor": s.aliceID}, 24*time.Hour)
	}
	// Both emissions passed the cheap check and now wait on the lock.
	deadline := time.Now().Add(3 * time.Second)
	for {
		var waiting int
		if err := s.a.database.QueryRow(`SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE 'SELECT id FROM sites WHERE id=$1 FOR UPDATE%'`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("emissions not waiting on the site lock: %d", waiting)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	waitNotificationEmits(t, s)
	if n := siteEventCount(t, s, "viewer.access_requested"); n != 1 {
		t.Fatalf("want one stored access request, got %d", n)
	}
	drainNotifications(t, s)
	if got := siteEventDeliveries(x, "viewer.access_requested"); len(got) != 1 {
		t.Fatalf("want one delivery, got %d", len(got))
	}
}

// A site event whose body would exceed NotifyBodyMaxBytes is dropped, not
// stored with a misleading truncation.
func TestNotifySiteEventsBodyTooLarge(t *testing.T) {
	s, x := notificationFixture(t)
	notificationRequest(t, s, "POST", "", map[string]any{"url": x.server.URL, "events": []string{"site.deployed"}, "secret": notifyTestSecret}, 201)
	big := strings.Repeat("x", s.a.sites.limits.NotifyBodyMaxBytes+1)
	s.a.sites.storeSiteNotification(s.sid, "site.deployed", map[string]any{"version": 2, "previous_version": 1, "via": big}, 0)
	if n := siteEventCount(t, s, "site.deployed"); n != 0 {
		t.Fatalf("oversized site event stored: %d", n)
	}
	s.a.sites.storeSiteNotification(s.sid, "site.deployed", map[string]any{"version": 2, "previous_version": 1, "via": "deploy"}, 0)
	if n := siteEventCount(t, s, "site.deployed"); n != 1 {
		t.Fatalf("normal site event not stored: %d", n)
	}
}
