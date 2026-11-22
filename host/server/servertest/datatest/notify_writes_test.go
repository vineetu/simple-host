package datatest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/data/resources"

	"github.com/vsriram/simple-host/host/access"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/config"
	appstorage "github.com/vsriram/simple-host/host/data"
	notifyapp "github.com/vsriram/simple-host/host/data/notify"
	notificationsrepo "github.com/vsriram/simple-host/host/data/repo/notify"
	"github.com/vsriram/simple-host/host/model"
)

// A visitor's own write pushes to the owner's devices only; the owner's
// edit of a visitor's row pushes to that visitor's devices only.
func TestWebPushRoutingDirections(t *testing.T) {
	s, _ := webPushFixture(t)
	srv, _ := pushTestServer(t, 201)
	ownerDevice := pushSubscribe(t, s, "owner", PushSubscribeBody(t, srv.URL, ""))
	aliceDevice := pushSubscribe(t, s, "alice", PushSubscribeBody(t, srv.URL+"/a", "orders"))
	pushSubscribe(t, s, "bob", PushSubscribeBody(t, srv.URL+"/b", "orders"))
	path := "/sqlite/orders/tables/orders/rows"
	row := fmt.Sprint(s.Must(t, "POST", path, map[string]string{"item": "alice's order"}, s.Callers["alice"], 200).JSON(t)["last_insert_id"])
	for _, step := range []struct {
		who    map[string]string
		method string
		want   string
	}{
		{nil, "", ownerDevice},
		{s.Key, "PATCH", aliceDevice},
		{s.Callers["alice"], "PATCH", ownerDevice},
	} {
		if step.who != nil {
			s.Must(t, step.method, path+"/"+row, map[string]string{"item": "changed"}, step.who, 200)
		}
		if pending := pendingPushSubscriptions(t, s); !reflect.DeepEqual(pending, []string{step.want}) {
			t.Fatalf("want only %s, got %v", step.want, pending)
		}
		notificationSQL(t, s, `DELETE FROM site_event_deliveries`)
	}
}

// Regression: a visitor-role push was once skipped as site_unavailable;
// the owner's edit is actually delivered to the row's author.
func TestWebPushDeliversThroughProcessDelivery(t *testing.T) {
	s, _ := webPushFixture(t)
	srv, hits := pushTestServer(t, 200)
	trustPush(s, srv)
	aliceDevice := pushSubscribe(t, s, "alice", PushSubscribeBody(t, srv.URL, "orders"))
	row := s.Must(t, "POST", "/sqlite/orders/tables/orders/rows", map[string]string{"item": "alice's own order"}, s.Callers["alice"], 200).JSON(t)["last_insert_id"]
	s.Must(t, "PATCH", "/sqlite/orders/tables/orders/rows/"+fmt.Sprint(row), map[string]string{"item": "owner's reply"}, s.Key, 200)
	drainNotifications(t, s)
	if atomic.LoadInt32(hits) != 1 {
		t.Fatalf("push endpoint received %d requests, want 1", atomic.LoadInt32(hits))
	}
	if n := getNotification(t, s, aliceDevice); n.State != "active" || n.LastResult != "ok" {
		t.Fatalf("subscription state=%s last_result=%s", n.State, n.LastResult)
	}
}

// The owner's edit of a visitor's row, stored only so push can reach the
// visitor, never reaches a webhook.
func TestWebPushOwnerWriteNeverReachesWebhook(t *testing.T) {
	s, x := notificationFixture(t)
	ordersTable(t, s, RulesPersonal)
	subscribeNotification(t, s, x)
	row := s.Must(t, "POST", "/sqlite/orders/tables/orders/rows", map[string]string{"item": "alice's order"}, s.Callers["alice"], 200).JSON(t)["last_insert_id"]
	drainNotifications(t, s)
	if len(x.snapshot()) != 1 {
		t.Fatal("alice's insert was not delivered once")
	}
	s.Must(t, "PATCH", "/sqlite/orders/tables/orders/rows/"+fmt.Sprint(row), map[string]string{"item": "owner's reply"}, s.Key, 200)
	drainNotifications(t, s)
	if len(x.snapshot()) != 1 {
		t.Fatal("owner's edit of a visitor's row reached the webhook")
	}
}

// An owner add for a visitor (?for=) reaches only that visitor's device:
// not the webhook and not the owner's own device.
func TestWebPushOwnerForReachesOnlyTargetVisitor(t *testing.T) {
	s, x := webPushFixture(t)
	subscribeNotification(t, s, x)
	ownerSrv, ownerHits := pushTestServer(t, 200)
	aliceSrv, aliceHits := pushTestServer(t, 200)
	trustPush(s, x.server, ownerSrv, aliceSrv)
	pushSubscribe(t, s, "owner", PushSubscribeBody(t, ownerSrv.URL, ""))
	pushSubscribe(t, s, "alice", PushSubscribeBody(t, aliceSrv.URL, "orders"))
	s.Must(t, "POST", "/sqlite/orders/tables/orders/rows?for="+s.AliceID, map[string]string{"item": "owner on behalf of alice"}, s.Key, 200)
	drainNotifications(t, s)
	if len(x.snapshot()) != 0 || atomic.LoadInt32(ownerHits) != 0 || atomic.LoadInt32(aliceHits) != 1 {
		t.Fatalf("webhook %d, owner %d, alice %d; want 0, 0, 1", len(x.snapshot()), atomic.LoadInt32(ownerHits), atomic.LoadInt32(aliceHits))
	}
}

// The owner signed in on the site editing a row notifies the webhook as an
// ordinary write but never pushes back to the owner's own device.
func TestWebPushOwnerOnSiteNeverPushesBackToOwner(t *testing.T) {
	s, x := webPushFixture(t)
	subscribeNotification(t, s, x)
	srv, hits := pushTestServer(t, 200)
	trustPush(s, x.server, srv)
	body := PushSubscribeBody(t, srv.URL, "")
	body["events"] = []string{"record.added", "record.changed"}
	pushSubscribe(t, s, "owner-site", body)
	row := s.Must(t, "POST", "/sqlite/orders/tables/orders/rows", map[string]string{"item": "alice's order"}, s.Callers["alice"], 200).JSON(t)["last_insert_id"]
	drainNotifications(t, s)
	webhookBefore, hitsBefore := len(x.snapshot()), atomic.LoadInt32(hits)
	s.Must(t, "PATCH", "/sqlite/orders/tables/orders/rows/"+fmt.Sprint(row), map[string]string{"item": "owner reply"}, s.Callers["owner-site"], 200)
	drainNotifications(t, s)
	if len(x.snapshot()) != webhookBefore+1 || atomic.LoadInt32(hits) != hitsBefore {
		t.Fatalf("webhook %d->%d, own device %d->%d", webhookBefore, len(x.snapshot()), hitsBefore, atomic.LoadInt32(hits))
	}
}

// A push queued for a visitor who has since lost named-viewer access is
// not delivered (the delivery-time recheck).
func TestWebPushSkipsDeliveryWhenViewerRemoved(t *testing.T) {
	s, _ := webPushFixture(t)
	aliceEmail, err := access.VisitorEmail(context.Background(), s.A.Database, s.AliceID)
	if err != nil {
		t.Fatal(err)
	}
	notificationSQL(t, s, `UPDATE sites SET access='specific' WHERE id=$1`, s.Sid)
	if _, err := accountsrepo.AddSiteViewer(context.Background(), s.A.Database, s.Sid, aliceEmail); err != nil {
		t.Fatal(err)
	}
	srv, hits := pushTestServer(t, 200)
	trustPush(s, srv)
	pushSubscribe(t, s, "alice", PushSubscribeBody(t, srv.URL, "orders"))
	row := s.Must(t, "POST", "/sqlite/orders/tables/orders/rows", map[string]string{"item": "alice's own order"}, s.Callers["alice"], 200).JSON(t)["last_insert_id"]
	s.Must(t, "PATCH", "/sqlite/orders/tables/orders/rows/"+fmt.Sprint(row), map[string]string{"item": "owner's reply"}, s.Key, 200)
	waitNotificationEmits(t, s)
	if _, err := accountsrepo.RemoveSiteViewer(context.Background(), s.A.Database, s.Sid, aliceEmail); err != nil {
		t.Fatal(err)
	}
	drainNotifications(t, s)
	if got := atomic.LoadInt32(hits); got != 0 {
		t.Fatalf("push delivered to a removed viewer: %d", got)
	}
}

// The push rule's body column must exist and must not look like personal
// data; the title is plain text.
func TestWebPushRuleBodyColumnValidatedAtSave(t *testing.T) {
	s, _ := webPushFixture(t)
	for _, col := range []string{"email_address", "customerEmail", "ip_addr", "notes"} {
		s.Must(t, "POST", "/sqlite/orders/schema", map[string]string{"sql": "ALTER TABLE orders ADD COLUMN " + col + " TEXT"}, s.Key, 200)
	}
	for _, c := range []struct {
		body   map[string]any
		status int
		code   string
	}{
		{map[string]any{"body_column": "no_such_column"}, 400, "invalid_body_column"},
		{map[string]any{"body_column": "email_address"}, 400, "invalid_body_column"},
		{map[string]any{"body_column": "customerEmail"}, 400, "invalid_body_column"},
		{map[string]any{"body_column": "ip_addr"}, 400, "invalid_body_column"},
		{map[string]any{"body_column": "visitor_id"}, 400, "invalid_body_column"},
		{map[string]any{"title": "line one\nline two"}, 400, "invalid_request"},
		{map[string]any{"body_column": "notes", "title": "New order"}, 200, ""},
	} {
		c.body["resource"], c.body["recipients"] = "orders", "both"
		if r := putPushRule(t, s, c.body); r.Status != c.status || r.Code() != c.code {
			t.Fatalf("%v: %d %s", c.body, r.Status, r.Body)
		}
	}
}

// Even a permitted body column falls back to generic text when its value
// looks like an email address or an IP.
func TestWebPushBodyValueFallsBackWhenSensitive(t *testing.T) {
	s, _ := webPushFixture(t)
	s.Must(t, "POST", "/sqlite/orders/schema", map[string]string{"sql": "ALTER TABLE orders ADD COLUMN notes TEXT"}, s.Key, 200)
	if r := putPushRule(t, s, map[string]any{"resource": "orders", "recipients": "both", "body_column": "notes"}); r.Status != 200 {
		t.Fatalf("set rule: %d %s", r.Status, r.Body)
	}
	n := model.Notification{SiteID: s.Sid, SubscriberRole: "owner"}
	bodyFor := func(value string) string {
		raw, _ := json.Marshal(map[string]any{"storage": "orders", "site": "shop", "site_url": "https://shop.example", "fields": map[string]any{"notes": value}})
		var payload map[string]any
		if err := json.Unmarshal(s.A.Data.Notifier().PushPayloadForTest(context.Background(), n, model.NotificationDelivery{Name: "record.added", Data: raw}), &payload); err != nil {
			t.Fatal(err)
		}
		return fmt.Sprint(payload["body"])
	}
	if b := bodyFor("contact alice@example.com for details"); strings.Contains(b, "@") {
		t.Fatalf("email leaked into push body: %s", b)
	}
	if b := bodyFor("reach us at 10.0.0.5 on the office network"); strings.Contains(b, "10.0.0.5") {
		t.Fatalf("IP leaked into push body: %s", b)
	}
	if b := bodyFor("pickup window is 9 to 5"); b != "pickup window is 9 to 5" {
		t.Fatalf("ordinary value wrongly replaced: %s", b)
	}
}

// A queued push goes only to the row's author under every read rule and
// carries the owner-chosen column as it was queued; the rule changing to
// "none" skips it; a failed rule lookup defers the push, never drops it.
func TestWebPushRulesQueuedDelivery(t *testing.T) {
	for _, c := range []struct{ read, after string }{
		{"true", "unchanged"},
		{`visitor_id == auth.id || auth.role == "owner"`, "change-row"},
		{`visitor_id == auth.id || auth.role == "owner"`, "none"},
		{`visitor_id == auth.id || auth.role == "owner"`, "lookup-failure"},
	} {
		t.Run(c.read+"/"+c.after, func(t *testing.T) {
			s, _ := webPushFixture(t)
			row := s.Must(t, "POST", "/sqlite/orders/tables/orders/rows", map[string]string{"item": "original"}, s.Callers["alice"], 200).JSON(t)["last_insert_id"]
			waitNotificationEmits(t, s)
			saveNotificationRules(t, s, c.read)
			srv, hits := pushTestServer(t, 200)
			trustPush(s, srv)
			devices := map[string]string{}
			for _, caller := range []string{"alice", "bob", "owner-site"} {
				devices[caller] = pushSubscribe(t, s, caller, PushSubscribeBody(t, srv.URL+"/"+caller, "orders"))
			}
			if r := putPushRule(t, s, map[string]any{"resource": "orders", "recipients": "both", "body_column": "item"}); r.Status != 200 {
				t.Fatalf("push rule: %d %s", r.Status, r.Body)
			}
			s.Must(t, "PATCH", "/sqlite/orders/tables/orders/rows/"+fmt.Sprint(row), map[string]string{"item": "owner's reply"}, s.Key, 200)
			if got := pendingPushSubscriptions(t, s); !reflect.DeepEqual(got, []string{devices["alice"]}) {
				t.Fatalf("push recipients = %v, want only the row's author", got)
			}
			switch c.after {
			case "change-row":
				s.Must(t, "POST", "/sqlite/orders/execute", map[string]string{"sql": "UPDATE orders SET item='later value' WHERE id=" + fmt.Sprint(row)}, s.Key, 200)
			case "none":
				if r := putPushRule(t, s, map[string]any{"resource": "orders", "recipients": "none", "body_column": "item"}); r.Status != 200 {
					t.Fatalf("push rule none: %d", r.Status)
				}
			}
			h, ctx := s.A.Data, context.Background()
			d, err := notificationsSvc(h).ClaimNotificationDelivery(ctx, time.Minute, 5, 7, time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			n := getNotification(t, s, d.SubscriptionID)
			want := int32(1)
			switch c.after {
			case "lookup-failure":
				failed, cancel := context.WithCancel(ctx)
				cancel()
				if got := h.Notifier().PushPayloadForTest(failed, n, d); got != nil {
					t.Fatalf("failed push-rule lookup must defer, got %s", got)
				}
				notificationSQL(t, s, `UPDATE site_event_deliveries SET lease_until=now()-interval '1 second' WHERE subscription_id=$1`, devices["alice"])
			case "none":
				if got := h.Notifier().PushPayloadForTest(ctx, n, d); string(got) != "null" {
					t.Fatalf("revoked push = %s, want skip", got)
				}
				h.Notifier().Process(ctx, h.NotifyEnv(), d)
				want = 0
			default:
				var payload map[string]any
				if err := json.Unmarshal(h.Notifier().PushPayloadForTest(ctx, n, d), &payload); err != nil || payload["body"] != "owner's reply" {
					t.Fatalf("push must use the queued column: %v (%v)", payload, err)
				}
				h.Notifier().Process(ctx, h.NotifyEnv(), d)
			}
			drainNotifications(t, s)
			if got := atomic.LoadInt32(hits); got != want {
				t.Fatalf("push requests = %d, want %d", got, want)
			}
			if got := pendingPushSubscriptions(t, s); len(got) != 0 {
				t.Fatalf("unfinished deliveries: %v", got)
			}
		})
	}
}

func TestNotifySiteEventsRESTAndDelivery(t *testing.T) {
	s, x := notificationFixture(t)
	all := s.A.Data.Notifier().EventNamesForTest()
	if len(all) != 7 {
		t.Fatalf("event names: %v", all)
	}
	created := notificationRequest(t, s, "POST", "", map[string]any{"url": x.server.URL, "events": all, "secret": notifyTestSecret}, 201).JSON(t)["notification"].(map[string]any)
	notificationRequest(t, s, "DELETE", "/"+created["id"].(string), nil, 200)
	for _, events := range [][]string{{"record.added", "site.nope"}, {"site.deployed", "site.deployed"}} {
		if r := notificationRequest(t, s, "POST", "", map[string]any{"url": x.server.URL, "events": events, "secret": notifyTestSecret}, 400); r.Code() != "invalid_events" {
			t.Fatalf("%v: code %v", events, r.Code())
		}
	}
	// A storage filter narrows record events only.
	s.Must(t, "PUT", "/resources/orders", StorageRules("sqlite", RulesPersonal), s.Key, 201)
	notificationRequest(t, s, "POST", "", map[string]any{"url": x.server.URL, "events": []string{"record.added", "site.deployed", "viewer.access_requested", "visitor.signed_up"}, "storage": "orders", "secret": notifyTestSecret}, 201)
	s.A.Data.EmitSiteNotification(s.Sid, "site.deployed", map[string]any{"version": 2, "previous_version": 1, "via": "deploy"}, 0)
	drainNotifications(t, s)
	got := siteEventDeliveries(x, "site.deployed")
	if len(got) != 1 {
		t.Fatalf("want one site.deployed delivery, got %d", len(got))
	}
	data := got[0]["data"].(map[string]any)
	if data["v"] != float64(1) || data["site"] != "shop" || !strings.HasPrefix(data["site_url"].(string), "https://shop.") || data["version"] != float64(2) || data["via"] != "deploy" || len(data) != 6 {
		t.Fatalf("site.deployed data: %v", data)
	}
	// The owner on their own site is not an event; one per person per
	// dedupe window; another person is their own event.
	s.A.Data.EmitSiteNotification(s.Sid, "visitor.signed_up", map[string]any{"visitor": s.OwnerID}, 0)
	for _, who := range []string{s.AliceID, s.AliceID, s.Ids["bob"]} {
		s.A.Data.EmitSiteNotification(s.Sid, "viewer.access_requested", map[string]any{"visitor": who}, 24*time.Hour)
		waitNotificationEmits(t, s)
	}
	if siteEventCount(t, s, "visitor.signed_up") != 0 || siteEventCount(t, s, "viewer.access_requested") != 2 {
		t.Fatal("owner event stored, or dedupe wrong")
	}
	drainNotifications(t, s)
	requested := siteEventDeliveries(x, "viewer.access_requested")
	if len(requested) != 2 || requested[0]["data"].(map[string]any)["visitor"] != s.AliceID {
		t.Fatalf("access requests: %v", requested)
	}
}

// Visitor writes to SQLite, KV and files reach an owner webhook; the
// owner's own writes and failed writes do not.
func TestNotifyVisitorRoutes(t *testing.T) {
	s, x := notificationFixture(t)
	id := subscribeNotification(t, s, x)
	ordersTable(t, s, RulesPersonal)
	ctx, cancel := context.WithCancel(context.Background())
	s.A.Data.StartNotifications(ctx)
	t.Cleanup(func() { cancel(); s.A.Data.WaitNotifications() })
	alice := s.Callers["alice"]
	r := s.Must(t, "POST", "/sqlite/orders/tables/orders/rows", map[string]string{"item": "Chocolate cake"}, alice, 200)
	row := fmt.Sprint(r.JSON(t)["last_insert_id"])
	s.Must(t, "PATCH", "/sqlite/orders/tables/orders/rows/"+row, map[string]string{"item": "Vanilla"}, alice, 200)
	s.Must(t, "DELETE", "/sqlite/orders/tables/orders/rows/"+row, nil, alice, 200)
	s.Must(t, "POST", "/sqlite/orders/tables/orders/rows", map[string]string{"item": "Owner tools"}, s.Key, 200)
	s.Must(t, "POST", "/sqlite/orders/tables/orders/rows", map[string]string{"item": "Owner on site"}, s.Callers["owner-site"], 200)
	s.Must(t, "POST", "/sqlite/orders/tables/orders/rows", map[string]string{"missing_column": "failed"}, alice, 400)
	for _, kind := range []string{"kv", "files"} {
		s.Must(t, "PUT", "/resources/"+kind, StorageRules(kind, RulesPersonal), s.Key, 201)
	}
	s.Must(t, "PUT", "/kv/kv/keys/order", `{"value":{"item":"bread"}}`, alice, 200)
	s.Must(t, "PUT", "/kv/kv/keys/order", `{"value":"changed"}`, alice, 200)
	s.Must(t, "DELETE", "/kv/kv/keys/order", nil, alice, 200)
	s.Must(t, "PUT", "/files/files/objects/photo.txt", "photo bytes", alice, 200)
	waitNotificationEmits(t, s)
	deadline := time.Now().Add(5 * time.Second)
	for len(x.snapshot()) < 8 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	got := x.snapshot()
	if len(got) != 8 {
		t.Fatalf("want 8 visitor events, got %d", len(got))
	}
	names := map[string]int{}
	for _, r := range got {
		name := r.body["name"].(string)
		names[name]++
		d := r.body["data"].(map[string]any)
		if (d["visitor"] != s.AliceID && d["visitor"] != s.OwnerID) || !strings.HasPrefix(d["site_url"].(string), "https://shop.") {
			t.Fatalf("event data: %v", d)
		}
		if d["change"] == "deleted" && d["fields"] != nil {
			t.Fatal("deleted row carries fields")
		}
		if name == "file.uploaded" && (d["size"] != float64(11) || d["fields"] != nil) {
			t.Fatal("file fields")
		}
		if f, ok := d["fields"].(map[string]any); ok && d["table"] == "orders" && name == "record.added" && d["visitor"] == s.AliceID && (f["item"] != "Chocolate cake" || f["visitor_id"] != nil) {
			t.Fatalf("row fields: %v", f)
		}
	}
	if names["record.added"] != 3 || names["record.changed"] != 4 || names["file.uploaded"] != 1 {
		t.Fatal(names)
	}
	list := notificationRequest(t, s, "GET", "", nil, 200)
	if strings.Contains(string(list.Body), "whsec_") || strings.Contains(string(list.Body), "Bearer receiver") {
		t.Fatal("subscription list leaked credentials")
	}
	if history := notificationRequest(t, s, "GET", "/"+id+"/deliveries", nil, 200).JSON(t)["deliveries"].([]any); len(history) != 8 {
		t.Fatalf("history count %d", len(history))
	}
}

// The owner adding a row for a visitor (?for=), by key or on the site, is
// the owner's own write: never notified.
func TestStorageOwnerForDoesNotNotify(t *testing.T) {
	s, x := notificationFixture(t)
	subscribeNotification(t, s, x)
	ordersTable(t, s, RulesRecords)
	rows := "/sqlite/orders/tables/orders/rows"
	s.Must(t, "POST", rows+"?for="+s.AliceID, map[string]string{"item": "owner key, for alice"}, s.Key, 200)
	s.Must(t, "POST", rows+"?for="+s.AliceID, map[string]string{"item": "owner on site, for alice"}, s.Callers["owner-site"], 200)
	s.Must(t, "POST", rows, map[string]string{"item": "alice's own order"}, s.Callers["alice"], 200)
	drainNotifications(t, s)
	got := x.snapshot()
	if len(got) != 1 || got[0].body["name"] != "record.added" || got[0].body["data"].(map[string]any)["visitor"] != s.AliceID {
		t.Fatalf("want only alice's own add, got %v", got)
	}
}

func TestNotifyRetriesSuspensionAndResume(t *testing.T) {
	s, x := notificationFixture(t, notifyLimits(func(l *config.Limits) { l.NotifySuspendAttempts = 3 }))
	id := subscribeNotification(t, s, x)
	mail := &mailbox{}
	s.A.Data.Mailer = mail
	x.setStatus(500)
	s.Must(t, "PUT", "/resources/kv", StorageRules("kv", RulesPersonal), s.Key, 201)
	s.Must(t, "PUT", "/kv/kv/keys/one", `{"value":1}`, s.Callers["alice"], 200)
	for i := 0; i < 3; i++ {
		notificationSQL(t, s, `UPDATE site_event_deliveries SET next_attempt_at=now() WHERE subscription_id=$1`, id)
		drainNotifications(t, s)
	}
	n := getNotification(t, s, id)
	if n.State != "suspended" {
		t.Fatalf("not suspended: %s", n.State)
	}
	if got := x.snapshot(); len(got) != 3 || !got[1].duplicate || !got[2].duplicate {
		t.Fatal("retries must reuse the event id")
	}
	s.A.Data.Notifier().NotifySuspension(context.Background(), s.A.Data.NotifyEnv().Mailer, n)
	if mail.to(s.Owner.Email) != 1 {
		t.Fatal("suspension must send one notice")
	}
	drainNotifications(t, s)
	if len(x.snapshot()) != 3 {
		t.Fatal("suspended attempt sent")
	}
	x.setStatus(200)
	notificationRequest(t, s, "PATCH", "/"+id, map[string]bool{"paused": false}, 200)
	drainNotifications(t, s)
	if n := getNotification(t, s, id); n.State != "active" || n.LastSuccessAt == nil {
		t.Fatal("resume did not deliver")
	}
	// 410 and 413 are permanent: failed after one attempt, subscription kept.
	for _, status := range []int{410, 413} {
		x.setStatus(status)
		s.Must(t, "PUT", "/kv/kv/keys/"+strconv.Itoa(status), `{"value":1}`, s.Callers["alice"], 200)
		drainNotifications(t, s)
		if state, _, attempts := deliveryState(t, s, id); state != "failed" || attempts != 1 {
			t.Fatalf("%d: %s %d", status, state, attempts)
		}
		if getNotification(t, s, id).State != "active" {
			t.Fatal("permanent HTTP result changed subscription")
		}
	}
	// A receiver on a refused (loopback) address is never contacted.
	s.A.Data.Notifier().UseTestClient(x.roots(), false)
	before := len(x.snapshot())
	emitWorkerNotification(t, s, s.Sid)
	drainNotifications(t, s)
	if state, category, attempts := deliveryState(t, s, id); state != "failed" || category != "refused_address" || attempts != 1 || len(x.snapshot()) != before {
		t.Fatalf("refused address: %s %s %d", state, category, attempts)
	}
}

func TestNotifyCapsTruncationRotationAndControls(t *testing.T) {
	s, x := notificationFixture(t, notifyLimits(func(l *config.Limits) { l.NotifyMaxPerSite = 1; l.NotifyPerMinute = 2 }))
	id := subscribeNotification(t, s, x)
	notificationRequest(t, s, "POST", "", map[string]any{"url": x.server.URL, "events": []string{"record.added"}}, 409)
	s.Must(t, "PUT", "/resources/kv", StorageRules("kv", RulesPersonal), s.Key, 201)
	s.Must(t, "PUT", "/kv/kv/keys/long", map[string]any{"value": map[string]string{"long": strings.Repeat("é", 2000)}}, s.Callers["alice"], 200)
	many := map[string]string{}
	for i := 0; i < 30; i++ {
		many[fmt.Sprint(i)] = strings.Repeat("x", 1000)
	}
	s.Must(t, "PUT", "/kv/kv/keys/big", map[string]any{"value": many}, s.Callers["alice"], 200)
	s.Must(t, "PUT", "/kv/kv/keys/capped", `{"value":1}`, s.Callers["alice"], 200)
	drainNotifications(t, s)
	got := x.snapshot()
	if len(got) != 2 {
		t.Fatalf("minute cap: got %d", len(got))
	}
	data := got[0].body["data"].(map[string]any)
	if len(data["fields"].(map[string]any)["long"].(string)) > 2048 || data["truncated"] == nil {
		t.Fatal("field truncation")
	}
	data = got[1].body["data"].(map[string]any)
	if data["fields"] != nil || data["truncated"].([]any)[0] != "fields" {
		t.Fatal("body truncation")
	}
	rotated := notificationRequest(t, s, "POST", "/"+id+"/rotate-secret", nil, 200).JSON(t)["secret"].(string)
	x.set(func(x *notificationReceiver) { x.keys = append(x.keys, rotated) })
	notificationRequest(t, s, "POST", "/"+id+"/test", nil, 200)
	if len(strings.Fields(x.snapshot()[2].header.Get("webhook-signature"))) != 2 {
		t.Fatal("rotation needs both signatures")
	}
	notificationRequest(t, s, "PATCH", "/"+id, map[string]bool{"paused": true}, 200)
	notificationRequest(t, s, "DELETE", "/"+id, nil, 200)
	notificationRequest(t, s, "GET", "/"+id+"/deliveries", nil, 404)
}

func TestNotifyDailyCapAndNoSubscribers(t *testing.T) {
	s, x := notificationFixture(t, notifyLimits(func(l *config.Limits) { l.NotifyPerDay = 1 }))
	s.Must(t, "PUT", "/resources/kv", StorageRules("kv", RulesPersonal), s.Key, 201)
	s.Must(t, "PUT", "/kv/kv/keys/no-subscriber", `{"value":1}`, s.Callers["alice"], 200)
	waitNotificationEmits(t, s)
	if countRows(t, s, `SELECT count(*) FROM site_events WHERE site_id=$1`, s.Sid) != 0 {
		t.Fatal("stored event without subscriptions")
	}
	subscribeNotification(t, s, x)
	for _, key := range []string{"one", "two"} {
		s.Must(t, "PUT", "/kv/kv/keys/"+key, `{"value":1}`, s.Callers["alice"], 200)
	}
	drainNotifications(t, s)
	if len(x.snapshot()) != 1 {
		t.Fatal("daily cap")
	}
}

// Storing an event never holds the storage write, and a slow database
// loses no committed event; a full emit queue drops (and counts) the event.
func TestNotifyEmitAfterStorageRelease(t *testing.T) {
	s, x := notificationFixture(t)
	subscribeNotification(t, s, x)
	ordersTable(t, s, RulesPersonal)
	tx, err := s.A.Database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`SELECT id FROM sites WHERE id=$1 FOR UPDATE`, s.Sid); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	s.Must(t, "POST", "/sqlite/orders/tables/orders/rows", map[string]string{"item": "first"}, s.Callers["alice"], 200)
	s.Must(t, "POST", "/sqlite/orders/tables/orders/rows", map[string]string{"item": "second"}, s.Callers["alice"], 200)
	if time.Since(start) > time.Second {
		t.Fatal("Postgres emission held the storage request")
	}
	time.Sleep(150 * time.Millisecond)
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	drainNotifications(t, s)
	if len(x.snapshot()) != 2 {
		t.Fatal("transient Postgres delay lost committed events")
	}
	e := s.A.Data.Notifier()
	release := e.FillEmitQueueForTest()
	before := e.DroppedForTest()
	s.A.Data.EmitNotification(appstorage.Call{SiteID: s.Sid}, "record.added", map[string]any{})
	if e.DroppedForTest() != before+1 {
		t.Fatal("full emit queue did not drop")
	}
	release()
}

// The challenge is echoed once, a refresh is idempotent, a cursor replay
// backfills, another owner is refused and unsubscribe is idempotent.
func TestNotifyMCPChallengeRefreshReplay(t *testing.T) {
	s, x := notificationFixture(t)
	caller := notifyapp.Subscriber{Owner: s.OwnerID, AppName: "Test app"}
	req := mcpRequest(x)
	result := mustMCPSubscribe(t, s, caller, req)
	if result.Refreshed || !result.Active || result.RefreshBefore == nil {
		t.Fatal("initial subscribe")
	}
	if got := x.snapshot(); len(got) != 1 || got[0].body["type"] != "verification" || len(got[0].body["challenge"].(string)) != 32 {
		t.Fatal("missing challenge")
	}
	if refresh := mustMCPSubscribe(t, s, caller, req); refresh.ID != result.ID || !refresh.Refreshed || len(x.snapshot()) != 1 {
		t.Fatal("refresh was not idempotent")
	}
	notificationRequest(t, s, "POST", "/"+result.ID+"/rotate-secret", nil, 409)
	s.Must(t, "PUT", "/resources/kv", StorageRules("kv", RulesPersonal), s.Key, 201)
	s.Must(t, "PUT", "/kv/kv/keys/one", `{"value":1}`, s.Callers["alice"], 200)
	waitNotificationEmits(t, s)
	seq := int64(countRows(t, s, `SELECT max(seq) FROM site_events WHERE site_id=$1`, s.Sid))
	notificationRequest(t, s, "DELETE", "/"+result.ID, nil, 200)
	cursor := "c_0"
	req.Cursor, req.TTLSet = &cursor, true
	replay := mustMCPSubscribe(t, s, caller, req)
	if !replay.Truncated || replay.RefreshBefore == nil {
		t.Fatalf("replay: %+v", replay)
	}
	drainNotifications(t, s)
	if safe, err := notificationsrepo.NotificationCursor(context.Background(), s.A.Database, replay.ID, s.Sid); err != nil || safe != seq {
		t.Fatal("cursor did not advance")
	}
	got := x.snapshot()
	if last := got[len(got)-1]; last.body["name"] != "record.added" || last.header.Get("Authorization") != "" {
		t.Fatal("MCP event headers")
	}
	// A new endpoint must echo the challenge.
	x.set(func(x *notificationReceiver) { x.echo = false })
	bad := req
	bad.URL = x.server.URL + "/no-echo"
	var ee *notifyapp.EventError
	if _, err := mcpSubscribe(s, caller, bad); !errors.As(err, &ee) || ee.Code != notifyapp.EventCodeCallbackEndpoint || ee.Data["reason"] != "challenge_failed" {
		t.Fatalf("missing echo: %v", err)
	}
	if _, err := mcpSubscribe(s, notifyapp.Subscriber{Owner: s.AliceID}, req); !errors.As(err, &ee) || ee.Code != notifyapp.EventCodeForbidden {
		t.Fatal("other owner accepted")
	}
	unsub := notifyapp.EventUnsubscribeRequest{Name: req.Name, Site: req.Site, CanonicalArgs: req.CanonicalArgs, URL: req.URL}
	for i := 0; i < 2; i++ {
		if err := s.A.Data.Notifier().Unsubscribe(context.Background(), caller, unsub); err != nil {
			t.Fatalf("unsubscribe %d: %v", i, err)
		}
	}
}

// An owner-authored write (?for=) never reaches a connector subscriber,
// live or by a cursor replay.
func TestNotifyMCPReplayExcludesOwnerWrite(t *testing.T) {
	s, x := notificationFixture(t)
	ordersTable(t, s, RulesPersonal)
	caller := notifyapp.Subscriber{Owner: s.OwnerID, AppName: "Test app"}
	req := mcpRequest(x)
	mustMCPSubscribe(t, s, caller, req)
	drainNotifications(t, s)
	s.Must(t, "POST", "/sqlite/orders/tables/orders/rows?for="+s.AliceID, map[string]string{"item": "owner on behalf of alice"}, s.Key, 200)
	drainNotifications(t, s)
	before := len(x.snapshot())
	cursor := "c_0"
	req.Cursor, req.TTLSet = &cursor, true
	mustMCPSubscribe(t, s, caller, req)
	drainNotifications(t, s)
	if got := len(x.snapshot()); got != before {
		t.Fatalf("owner write reached the connector subscriber by replay: %d -> %d", before, got)
	}
}

// saveNotificationRules saves a read rule on orders through the service.
func saveNotificationRules(t *testing.T, s Shop, read string) {
	t.Helper()
	h := s.A.Data
	resource, err := h.LoadStorageResource(context.Background(), s.Sid, "orders")
	if err != nil {
		t.Fatal(err)
	}
	c := appstorage.Call{SiteID: s.Sid, OwnerID: s.OwnerID, SiteName: "shop", ResourceName: "orders", Resource: resource, Owner: true}
	raw, _ := json.Marshal(map[string]any{"version": 1, "policies": []any{map[string]any{"name": "read", "command": "read", "using": read}}})
	rules := json.RawMessage(raw)
	if _, err := resources.PutStorageResource(h.StorageFor(c), context.Background(), resources.ResourceInput{Call: c, Name: "orders", Kind: "sqlite", Rules: &rules}); err != nil {
		t.Fatal(err)
	}
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

func siteEventCount(t *testing.T, s Shop, name string) int {
	t.Helper()
	return countRows(t, s, `SELECT count(*) FROM site_events WHERE site_id=$1 AND name=$2`, s.Sid, name)
}
