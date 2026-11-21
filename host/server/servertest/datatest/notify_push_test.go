package datatest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/access"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	appstorage "github.com/vsriram/simple-host/host/data"
	"github.com/vsriram/simple-host/host/data/datahttp"
	notifyapp "github.com/vsriram/simple-host/host/data/notify"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
)

// Push routes 404 until WEB_PUSH_ACCOUNTS covers the site's account; the
// service worker file is served whatever the flag.
func TestWebPushFlagGate(t *testing.T) {
	s, _ := notificationFixture(t)
	srv, _ := pushTestServer(t, 201)
	if r := s.A.At(t, "GET", rt.SiteDomain, "/v1/sites/shop/push/vapid-key", nil, s.Key); r.Status != 404 {
		t.Fatalf("flag off: vapid-key status %d", r.Status)
	}
	if r := s.A.At(t, "POST", rt.SiteDomain, "/v1/sites/shop/push/subscribe", PushSubscribeBody(t, srv.URL, ""), s.Key); r.Status != 404 {
		t.Fatalf("flag off: subscribe status %d", r.Status)
	}
	if r := s.A.At(t, "GET", s.Host, "/v1/push/sw.js", nil, nil); r.Status != 200 || !strings.Contains(string(r.Body), "addEventListener") {
		t.Fatalf("sw.js with the flag off: %d", r.Status)
	}
	s.A.Data.SetWebPushAccounts(nil, true)
	if r := s.A.At(t, "GET", rt.SiteDomain, "/v1/sites/shop/push/vapid-key", nil, s.Key); r.Status != 200 || r.JSON(t)["public_key"] == "" {
		t.Fatalf("flag on: vapid-key status %d body %s", r.Status, r.Body)
	}
}

// A visitor's device is a visitor device with the record events whatever
// it asks; the owner (by key or on the site) subscribes site-wide; the
// same endpoint again is the same device; a visitor removes only their own.
func TestWebPushSubscribeScoping(t *testing.T) {
	s, _ := webPushFixture(t)
	srv, _ := pushTestServer(t, 201)
	ownerDevice := pushSubscribe(t, s, "owner", PushSubscribeBody(t, srv.URL, ""))
	body := PushSubscribeBody(t, srv.URL, "")
	body["events"] = []string{"record.added"}
	onSite := getNotification(t, s, pushSubscribe(t, s, "owner-site", body))
	if onSite.SubscriberRole != "owner" || onSite.Storage != "" {
		t.Fatalf("owner on the site stored as %+v", onSite)
	}
	if n := getNotification(t, s, pushSubscribe(t, s, "alice", PushSubscribeBody(t, srv.URL+"/wide", ""))); n.SubscriberRole != "visitor" || n.Storage != "" {
		t.Fatalf("visitor site-wide device: %+v", n)
	}
	body = PushSubscribeBody(t, srv.URL+"/orders", "orders")
	body["events"] = []string{"domain.status_changed"}
	aliceDevice := pushSubscribe(t, s, "alice", body)
	n := getNotification(t, s, aliceDevice)
	if n.SubscriberRole != "visitor" || n.Storage != "orders" || n.OwnerID != s.AliceID || !reflect.DeepEqual(n.Events, notifyapp.RecordEventNames) {
		t.Fatalf("visitor subscription not scoped: %+v", n)
	}
	if again := pushSubscribe(t, s, "alice", body); again != aliceDevice {
		t.Fatal("same endpoint and resource made a second device")
	}
	unsub := func(id string) bool {
		r := s.A.At(t, "POST", s.Host, "/v1/sites/shop/push/unsubscribe", map[string]string{"id": id}, s.Callers["alice"])
		if r.Status != 200 {
			t.Fatalf("unsubscribe: %d %s", r.Status, r.Body)
		}
		return r.JSON(t)["deleted"] == true
	}
	if unsub(ownerDevice) || !unsub(aliceDevice) {
		t.Fatal("alice removed the owner's device, or could not remove her own")
	}
}

// Subscribe, unsubscribe and mute from a page need the CSRF header; the
// owner's key does not.
func TestWebPushCookieRoutesRequireCSRF(t *testing.T) {
	s, _ := webPushFixture(t)
	srv, _ := pushTestServer(t, 201)
	noCSRF := rt.Browser(s.Host, "", "X-SH-CSRF", "", "Cookie", s.Callers["alice"]["Cookie"])
	id := pushSubscribe(t, s, "alice", PushSubscribeBody(t, srv.URL, "orders"))
	for _, c := range []struct {
		path string
		body any
	}{
		{"subscribe", PushSubscribeBody(t, srv.URL, "orders")},
		{"unsubscribe", map[string]string{"id": id}},
		{"mute", map[string]any{"id": id, "resources": []string{"orders"}}},
	} {
		if r := s.A.At(t, "POST", s.Host, "/v1/sites/shop/push/"+c.path, c.body, noCSRF); r.Status != 403 || r.Code() != "csrf_required" {
			t.Fatalf("%s without CSRF: %d %s", c.path, r.Status, r.Body)
		}
	}
	pushSubscribe(t, s, "owner", PushSubscribeBody(t, srv.URL, ""))
}

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

// Removing a person's access deletes their push devices on the site.
func TestWebPushViewerRemovalDeletesSubscription(t *testing.T) {
	s, _ := webPushFixture(t)
	srv, _ := pushTestServer(t, 201)
	id := pushSubscribe(t, s, "alice", PushSubscribeBody(t, srv.URL, "orders"))
	aliceEmail, err := access.VisitorEmail(context.Background(), s.A.Database, s.AliceID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.A.Data.DeletePushForEmail(context.Background(), s.Sid, aliceEmail); err != nil {
		t.Fatal(err)
	}
	if !notificationGone(s, id) {
		t.Fatal("push subscription survived viewer removal cleanup")
	}
}

// The owner removes a visitor's device through the push route (the generic
// notifications route cannot reach it), and that route reaches only push rows.
func TestWebPushOwnerRemovesVisitorDevice(t *testing.T) {
	s, _ := webPushFixture(t)
	srv, _ := pushTestServer(t, 201)
	id := pushSubscribe(t, s, "alice", PushSubscribeBody(t, srv.URL, "orders"))
	if r := s.A.At(t, "DELETE", s.Host, "/v1/sites/shop/push/devices/"+id, nil, s.Callers["alice"]); r.Status < 400 {
		t.Fatalf("visitor reached the owner's device route: %d", r.Status)
	}
	if r := s.A.At(t, "DELETE", rt.SiteDomain, "/v1/sites/shop/notifications/"+id, nil, s.Key); r.Status != 404 {
		t.Fatalf("generic route reached a visitor's device: %d", r.Status)
	}
	if r := s.A.At(t, "DELETE", rt.SiteDomain, "/v1/sites/shop/push/devices/"+id, nil, s.Key); r.Status != 200 || r.JSON(t)["deleted"] != true || !notificationGone(s, id) {
		t.Fatalf("owner device delete: %d %s", r.Status, r.Body)
	}
	webhookID := notificationRequest(t, s, "POST", "", map[string]any{"url": srv.URL, "events": []string{"record.added"}, "secret": notifyTestSecret}, 201).JSON(t)["notification"].(map[string]any)["id"].(string)
	if r := s.A.At(t, "DELETE", rt.SiteDomain, "/v1/sites/shop/push/devices/"+webhookID, nil, s.Key); r.Status != 404 {
		t.Fatalf("push device route deleted a webhook: %d", r.Status)
	}
}

// The push service's answers: 410 deletes the device, a redirect is never
// followed; the payload is encrypted and VAPID-signed with this install's key.
func TestWebPushSendResults(t *testing.T) {
	s, _ := webPushFixture(t)
	var gotAuth, gotEncoding string
	var gotBody []byte
	capture := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotEncoding = r.Header.Get("Authorization"), r.Header.Get("Content-Encoding")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(201)
	}))
	t.Cleanup(capture.Close)
	gone, _ := pushTestServer(t, 410)
	moved, _ := pushTestServer(t, 302)
	trustPush(s, capture, gone, moved)
	send := func(url, body string) (notifyapp.Result, string) {
		id := pushSubscribe(t, s, "owner", PushSubscribeBody(t, url, ""))
		return s.A.Data.Notifier().SendPushForTest(context.Background(), s.A.Data.NotifyEnv(), getNotification(t, s, id), []byte(body)), id
	}
	if r, id := send(gone.URL, `{"title":"t","body":"b"}`); r.Delivered || r.Result != "unregistered" || !notificationGone(s, id) {
		t.Fatalf("410: %+v", r)
	}
	if r, _ := send(moved.URL, `{"title":"t","body":"b"}`); r.Delivered || r.Result != "redirect" {
		t.Fatalf("302: %+v", r)
	}
	title := "a secret order just came in"
	if r, _ := send(capture.URL, `{"title":"`+title+`","body":"generic"}`); !r.Delivered {
		t.Fatalf("send failed: %+v", r)
	}
	pub, _, err := s.A.Data.Notifier().VAPIDKeys(context.Background(), s.A.Data.NotifyEnv())
	if err != nil {
		t.Fatal(err)
	}
	if gotEncoding != "aes128gcm" || strings.Contains(string(gotBody), title) || !strings.HasPrefix(gotAuth, "vapid t=") || !strings.Contains(gotAuth, "k="+pub) {
		t.Fatalf("not encrypted or not signed by this install: %q %q", gotEncoding, gotAuth)
	}
}

// A person over their push budget does not spend the site's budget.
func TestWebPushBudgetsReserveBothOrNeither(t *testing.T) {
	s, _ := webPushFixture(t)
	srv, hits := pushTestServer(t, 200)
	trustPush(s, srv)
	s.A.Data.Notifier().PushBudgetsForTest(httpx.NewRateLimiter(2, 0), httpx.NewRateLimiter(1, 0))
	p256dh, auth := FakePushKeys(t)
	send := func(owner string) notifyapp.Result {
		n := model.Notification{SiteID: s.Sid, OwnerID: owner, URL: srv.URL, PushP256dh: p256dh, PushAuth: auth}
		return s.A.Data.Notifier().SendPushForTest(context.Background(), s.A.Data.NotifyEnv(), n, []byte(`{"title":"t","body":"b"}`))
	}
	if r := send("alice-budget"); !r.Delivered {
		t.Fatalf("alice's first send: %+v", r)
	}
	if r := send("alice-budget"); r.Delivered || r.Result != "push_rate_limited" {
		t.Fatalf("alice's second send should be person-limited: %+v", r)
	}
	if r := send("bob-budget"); !r.Delivered {
		t.Fatalf("bob starved by alice's refused attempt: %+v", r)
	}
	if got := atomic.LoadInt32(hits); got != 2 {
		t.Fatalf("hits = %d, want 2", got)
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

// saveNotificationRules saves a read rule on orders through the service.
func saveNotificationRules(t *testing.T, s Shop, read string) {
	t.Helper()
	h := s.A.Data
	resource, err := h.LoadStorageResource(context.Background(), s.Sid, "orders")
	if err != nil {
		t.Fatal(err)
	}
	c := datahttp.StorageCall{SiteID: s.Sid, OwnerID: s.OwnerID, SiteName: "shop", ResourceName: "orders", Resource: resource, Owner: true}
	raw, _ := json.Marshal(map[string]any{"version": 1, "policies": []any{map[string]any{"name": "read", "command": "read", "using": read}}})
	rules := json.RawMessage(raw)
	if _, err := h.StorageFor(c).PutStorageResource(context.Background(), appstorage.ResourceInput{Call: c, Name: "orders", Kind: "sqlite", Rules: &rules}); err != nil {
		t.Fatal(err)
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

// A team's site offers no web push.
func TestWebPushRefusedOnTeamSite(t *testing.T) {
	s, _ := webPushFixture(t)
	srv, _ := pushTestServer(t, 201)
	for _, q := range []string{`DELETE FROM api_keys WHERE user_id = $1`, `UPDATE users SET is_team = true WHERE id = $1`} {
		if _, err := s.A.Database.Exec(q, s.OwnerID); err != nil {
			t.Fatal(err)
		}
	}
	if r := s.A.At(t, "POST", s.Host, "/v1/sites/shop/push/subscribe", PushSubscribeBody(t, srv.URL, ""), s.Callers["alice"]); r.Code() != "not_offered" {
		t.Fatalf("team push subscribe: %d %s", r.Status, r.Body)
	}
}
