package notifications

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vsriram/simple-host/host/server/servertest/datatest"

	"github.com/vsriram/simple-host/host/data/push"

	"github.com/vsriram/simple-host/host/access"

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
	if r := s.A.At(t, "POST", rt.SiteDomain, "/v1/sites/shop/push/subscribe", datatest.PushSubscribeBody(t, srv.URL, ""), s.Key); r.Status != 404 {
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
	ownerDevice := pushSubscribe(t, s, "owner", datatest.PushSubscribeBody(t, srv.URL, ""))
	body := datatest.PushSubscribeBody(t, srv.URL, "")
	body["events"] = []string{"record.added"}
	onSite := getNotification(t, s, pushSubscribe(t, s, "owner-site", body))
	if onSite.SubscriberRole != "owner" || onSite.Storage != "" {
		t.Fatalf("owner on the site stored as %+v", onSite)
	}
	if n := getNotification(t, s, pushSubscribe(t, s, "alice", datatest.PushSubscribeBody(t, srv.URL+"/wide", ""))); n.SubscriberRole != "visitor" || n.Storage != "" {
		t.Fatalf("visitor site-wide device: %+v", n)
	}
	body = datatest.PushSubscribeBody(t, srv.URL+"/orders", "orders")
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
	id := pushSubscribe(t, s, "alice", datatest.PushSubscribeBody(t, srv.URL, "orders"))
	for _, c := range []struct {
		path string
		body any
	}{
		{"subscribe", datatest.PushSubscribeBody(t, srv.URL, "orders")},
		{"unsubscribe", map[string]string{"id": id}},
		{"mute", map[string]any{"id": id, "resources": []string{"orders"}}},
	} {
		if r := s.A.At(t, "POST", s.Host, "/v1/sites/shop/push/"+c.path, c.body, noCSRF); r.Status != 403 || r.Code() != "csrf_required" {
			t.Fatalf("%s without CSRF: %d %s", c.path, r.Status, r.Body)
		}
	}
	pushSubscribe(t, s, "owner", datatest.PushSubscribeBody(t, srv.URL, ""))
}

// Removing a person's access deletes their push devices on the site.
func TestWebPushViewerRemovalDeletesSubscription(t *testing.T) {
	s, _ := webPushFixture(t)
	srv, _ := pushTestServer(t, 201)
	id := pushSubscribe(t, s, "alice", datatest.PushSubscribeBody(t, srv.URL, "orders"))
	aliceEmail, err := access.VisitorEmail(context.Background(), s.A.Database, s.AliceID)
	if err != nil {
		t.Fatal(err)
	}
	if err := push.DeletePushForEmail(s.A.Data, context.Background(), s.Sid, aliceEmail); err != nil {
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
	id := pushSubscribe(t, s, "alice", datatest.PushSubscribeBody(t, srv.URL, "orders"))
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
		id := pushSubscribe(t, s, "owner", datatest.PushSubscribeBody(t, url, ""))
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
	p256dh, auth := datatest.FakePushKeys(t)
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

// A team's site offers no web push.
func TestWebPushRefusedOnTeamSite(t *testing.T) {
	s, _ := webPushFixture(t)
	srv, _ := pushTestServer(t, 201)
	for _, q := range []string{`DELETE FROM api_keys WHERE user_id = $1`, `UPDATE users SET is_team = true WHERE id = $1`} {
		if _, err := s.A.Database.Exec(q, s.OwnerID); err != nil {
			t.Fatal(err)
		}
	}
	if r := s.A.At(t, "POST", s.Host, "/v1/sites/shop/push/subscribe", datatest.PushSubscribeBody(t, srv.URL, ""), s.Callers["alice"]); r.Code() != "not_offered" {
		t.Fatalf("team push subscribe: %d %s", r.Status, r.Body)
	}
}
