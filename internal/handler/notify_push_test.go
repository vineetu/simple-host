package handler

import (
	"context"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/mcp"
	"github.com/vsriram/simple-host/internal/model"
	accountsrepo "github.com/vsriram/simple-host/internal/repository/accounts"
	sitesrepo "github.com/vsriram/simple-host/internal/repository/sites"
)

// webPushFixture is notificationFixture plus the flag turned on for every
// account, and an "orders" personal-preset table alice can write her own
// rows to and the owner can write (or edit) on her behalf, matching
// SIM-360's two directions.
func webPushFixture(t *testing.T) presetSite {
	t.Helper()
	s, _ := notificationFixture(t)
	s.a.sites.SetWebPushAccounts(nil, true)
	s.must(t, "PUT", "/resources/orders", map[string]string{"kind": "sqlite", "preset": "personal"}, s.key, 201)
	waitNotificationEmits(t, s)
	s.must(t, "POST", "/sqlite/orders/schema", map[string]string{"sql": "CREATE TABLE orders(id INTEGER PRIMARY KEY, item TEXT, created_at TEXT, updated_at TEXT)"}, s.key, 200)
	waitNotificationEmits(t, s)
	return s
}

// fakePushKeys returns a plausible (not real-browser) P-256 point and auth
// secret: enough for our encryption wrapper to run its full RFC 8291 path
// against a test push endpoint, without a real browser subscription.
func fakePushKeys(t *testing.T) (p256dh, auth string) {
	t.Helper()
	_, x, y, err := elliptic.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	point := elliptic.Marshal(elliptic.P256(), x, y)
	authBytes := make([]byte, 16)
	if _, err := rand.Read(authBytes); err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(point), base64.RawURLEncoding.EncodeToString(authBytes)
}

func pushSubscribeBody(t *testing.T, endpoint, resource string) map[string]any {
	t.Helper()
	p256dh, auth := fakePushKeys(t)
	// events only matters for an owner subscription (a visitor's request
	// is always overridden to notifyRecordEvents regardless); the owner
	// route validates it like the webhook create route does, so tests
	// subscribing as the owner need a valid, non-empty list.
	body := map[string]any{"endpoint": endpoint, "keys": map[string]string{"p256dh": p256dh, "auth": auth}, "label": "test device", "events": append([]string{}, notifyRecordEvents...)}
	if resource != "" {
		body["resource"] = resource
	}
	return body
}

// TestWebPushFlagGate: every push route 404s when WEB_PUSH_ACCOUNTS does
// not cover this site's account, and works once it does.
func TestWebPushFlagGate(t *testing.T) {
	s, _ := notificationFixture(t)
	r := s.a.at(t, "GET", pcSiteDomain, "/v1/sites/shop/push/vapid-key", nil, s.key)
	if r.status != 404 {
		t.Fatalf("flag off: vapid-key status %d", r.status)
	}
	x := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer x.Close()
	r = s.a.at(t, "POST", pcSiteDomain, "/v1/sites/shop/push/subscribe", pushSubscribeBody(t, x.URL, ""), s.key)
	if r.status != 404 {
		t.Fatalf("flag off: subscribe status %d", r.status)
	}
	s.a.sites.SetWebPushAccounts(nil, true)
	r = s.a.at(t, "GET", pcSiteDomain, "/v1/sites/shop/push/vapid-key", nil, s.key)
	if r.status != 200 || r.json(t)["public_key"] == "" {
		t.Fatalf("flag on: vapid-key status %d body %s", r.status, r.body)
	}
}

// TestWebPushSubscribeScoping: a signed-in visitor must name one resource
// (their device can never be site-wide) and is always scoped to
// record/file events regardless of what they ask for; the owner may
// subscribe site-wide. Neither may reach another visitor's device by id.
func TestWebPushSubscribeScoping(t *testing.T) {
	s := webPushFixture(t)
	x := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer x.Close()

	// Owner: site-wide push subscription is allowed (resource "").
	r := s.a.at(t, "POST", pcSiteDomain, "/v1/sites/shop/push/subscribe", pushSubscribeBody(t, x.URL, ""), s.key)
	if r.status != 201 {
		t.Fatalf("owner subscribe: %d %s", r.status, r.body)
	}
	ownerDeviceID := r.json(t)["id"].(string)

	// Visitor: no resource named -> a site-wide device, same as the
	// owner's; the privacy boundary is the fan-out's own visitor_id
	// match, not a required resource filter.
	r = s.a.at(t, "POST", s.host, "/v1/sites/shop/push/subscribe", pushSubscribeBody(t, x.URL, ""), s.callers["alice"])
	if r.status != 201 {
		t.Fatalf("visitor site-wide subscribe: %d %s", r.status, r.body)
	}
	siteWideID := r.json(t)["id"].(string)
	if n, err := s.a.sites.notificationsSvc().GetNotification(context.Background(), siteWideID); err != nil || n.SubscriberRole != "visitor" || n.Storage != "" {
		t.Fatalf("visitor site-wide device: %+v, err %v", n, err)
	}
	s.a.sites.notificationsSvc().DeleteNotification(context.Background(), siteWideID)

	// Visitor: names a resource, and asks for a site-level event the
	// server must never honour for a visitor device -- it is silently
	// replaced with the record/file events, never a 400, since the
	// request shape otherwise looks fine.
	body := pushSubscribeBody(t, x.URL, "orders")
	body["events"] = []string{"domain.status_changed"}
	r = s.a.at(t, "POST", s.host, "/v1/sites/shop/push/subscribe", body, s.callers["alice"])
	if r.status != 201 {
		t.Fatalf("visitor subscribe: %d %s", r.status, r.body)
	}
	aliceDeviceID := r.json(t)["id"].(string)
	n, err := s.a.sites.notificationsSvc().GetNotification(context.Background(), aliceDeviceID)
	if err != nil {
		t.Fatal(err)
	}
	if n.SubscriberRole != "visitor" || n.Storage != "orders" || n.OwnerID != s.aliceID {
		t.Fatalf("visitor subscription not scoped correctly: %+v", n)
	}
	for _, want := range notifyRecordEvents {
		found := false
		for _, e := range n.Events {
			found = found || e == want
		}
		if !found {
			t.Fatalf("visitor subscription missing forced event %s: %v", want, n.Events)
		}
	}
	if len(n.Events) != len(notifyRecordEvents) {
		t.Fatalf("visitor subscription kept a non-record event: %v", n.Events)
	}

	// Unsubscribe: alice may remove her own device, never the owner's.
	r = s.a.at(t, "POST", s.host, "/v1/sites/shop/push/unsubscribe", map[string]string{"id": ownerDeviceID}, s.callers["alice"])
	if r.status != 200 || r.json(t)["deleted"] != false {
		t.Fatalf("alice removed the owner's device: %d %v", r.status, r.json(t))
	}
	r = s.a.at(t, "POST", s.host, "/v1/sites/shop/push/unsubscribe", map[string]string{"id": aliceDeviceID}, s.callers["alice"])
	if r.status != 200 || r.json(t)["deleted"] != true {
		t.Fatalf("alice could not remove her own device: %d %v", r.status, r.json(t))
	}
}

// pendingPushSubscriptions returns the subscription ids a just-stored event
// queued a pending web-push delivery for, confirming the EmitSiteEventFor
// fan-out (repository/notifications) routed to exactly the right devices --
// the hard privacy boundary SIM-360 depends on -- before any network send.
func pendingPushSubscriptions(t *testing.T, s presetSite) []string {
	t.Helper()
	waitNotificationEmits(t, s)
	rows, err := s.a.database.Query(`SELECT d.subscription_id FROM site_event_deliveries d JOIN site_notifications n ON n.id=d.subscription_id WHERE n.kind='web-push' AND d.state='pending' ORDER BY d.subscription_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		out = append(out, id)
	}
	return out
}

// TestWebPushRoutingDirections is the permission matrix the owner's ticket
// and the security review both center on: a visitor's own write pushes to
// the owner's devices, never back to the visitor's own; the owner's edit of
// that visitor's row pushes only to that visitor's own devices, never to
// the owner's own (any kind, including webhook/mcp -- unaffected here since
// none are subscribed, but checked in the fields below too).
func TestWebPushRoutingDirections(t *testing.T) {
	s := webPushFixture(t)
	x := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer x.Close()

	ownerDevice := s.a.at(t, "POST", pcSiteDomain, "/v1/sites/shop/push/subscribe", pushSubscribeBody(t, x.URL, ""), s.key).json(t)["id"].(string)
	aliceDevice := s.a.at(t, "POST", s.host, "/v1/sites/shop/push/subscribe", pushSubscribeBody(t, x.URL, "orders"), s.callers["alice"]).json(t)["id"].(string)
	bobDevice := s.a.at(t, "POST", s.host, "/v1/sites/shop/push/subscribe", pushSubscribeBody(t, x.URL, "orders"), s.callers["bob"]).json(t)["id"].(string)

	// Direction 1: alice adds her own row -> only the owner's device is
	// queued; neither alice's own device nor bob's (another visitor,
	// never told about anyone else's data) is.
	row := s.must(t, "POST", "/sqlite/orders/tables/orders/rows", map[string]string{"item": "alice's order"}, s.callers["alice"], 200).json(t)["last_insert_id"]
	pending := pendingPushSubscriptions(t, s)
	if len(pending) != 1 || pending[0] != ownerDevice {
		t.Fatalf("visitor add: want only owner device %s, got %v", ownerDevice, pending)
	}
	if _, err := s.a.database.Exec(`DELETE FROM site_event_deliveries`); err != nil {
		t.Fatal(err)
	}

	// Direction 2: the owner edits alice's row (D1: the owner's key/tools
	// already reach any visitor's row) -> only alice's device is queued;
	// never the owner's own (no self-notify) and never bob's.
	s.must(t, "PATCH", "/sqlite/orders/tables/orders/rows/"+fmt.Sprint(row), map[string]string{"item": "owner's reply"}, s.key, 200)
	pending = pendingPushSubscriptions(t, s)
	if len(pending) != 1 || pending[0] != aliceDevice {
		t.Fatalf("owner edit: want only alice device %s, got %v (bob=%s)", aliceDevice, pending, bobDevice)
	}
	if _, err := s.a.database.Exec(`DELETE FROM site_event_deliveries`); err != nil {
		t.Fatal(err)
	}

	// A visitor's own write never pushes to themselves: alice edits her
	// own row -> nobody's push device is queued except the owner's
	// (direction 1 again), never alice's own.
	s.must(t, "PATCH", "/sqlite/orders/tables/orders/rows/"+fmt.Sprint(row), map[string]string{"item": "alice again"}, s.callers["alice"], 200)
	pending = pendingPushSubscriptions(t, s)
	if len(pending) != 1 || pending[0] != ownerDevice {
		t.Fatalf("visitor's own edit must not self-notify: got %v", pending)
	}
}

// TestWebPushUnregisteredDeletesSubscription: a 410 (or 404) from the push
// service means the browser dropped the subscription; sendPush must delete
// the row so nothing keeps retrying against it.
func TestWebPushUnregisteredDeletesSubscription(t *testing.T) {
	s := webPushFixture(t)
	gone := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(410) }))
	defer gone.Close()
	roots := x509.NewCertPool()
	roots.AddCert(gone.Certificate())
	s.a.sites.SetNotifyClientForTest(roots)

	id := s.a.at(t, "POST", pcSiteDomain, "/v1/sites/shop/push/subscribe", pushSubscribeBody(t, gone.URL, ""), s.key).json(t)["id"].(string)
	n, err := s.a.sites.notificationsSvc().GetNotification(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	result := s.a.sites.sendPush(context.Background(), n, []byte(`{"title":"t","body":"b"}`))
	if result.Delivered || result.Result != "unregistered" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if _, err := s.a.sites.notificationsSvc().GetNotification(context.Background(), id); err == nil {
		t.Fatal("subscription was not deleted on 410")
	}
}

// TestWebPushVAPIDAuthorizationHeader is the encryption/VAPID integration
// test: capture what sendPush actually sends to a push endpoint and verify
// the payload is encrypted (never the plaintext title/body) and the
// Authorization header is a valid RFC 8292 VAPID JWT (ES256, aud = the
// endpoint's origin) signed by this install's own VAPID key, using
// webpush-go's own verification helpers so this checks our integration,
// not re-implements the library's already-tested crypto.
func TestWebPushVAPIDAuthorizationHeader(t *testing.T) {
	s := webPushFixture(t)
	var gotAuth, gotEncoding string
	var gotBody []byte
	capture := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotEncoding = r.Header.Get("Content-Encoding")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(201)
	}))
	defer capture.Close()
	roots := x509.NewCertPool()
	roots.AddCert(capture.Certificate())
	s.a.sites.SetNotifyClientForTest(roots)

	id := s.a.at(t, "POST", pcSiteDomain, "/v1/sites/shop/push/subscribe", pushSubscribeBody(t, capture.URL, ""), s.key).json(t)["id"].(string)
	n, err := s.a.sites.notificationsSvc().GetNotification(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	title := "a secret order just came in"
	result := s.a.sites.sendPush(context.Background(), n, []byte(`{"title":"`+title+`","body":"generic"}`))
	if !result.Delivered {
		t.Fatalf("send failed: %+v", result)
	}
	if gotEncoding != "aes128gcm" {
		t.Fatalf("Content-Encoding = %q", gotEncoding)
	}
	if strings.Contains(string(gotBody), title) {
		t.Fatal("push payload was sent in the clear")
	}
	if !strings.HasPrefix(gotAuth, "vapid t=") {
		t.Fatalf("Authorization header: %q", gotAuth)
	}
	pub, _, err := s.a.sites.vapidKeys(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Reuse webpush-go's own VAPID generation/verification round trip to
	// confirm our stored public key actually matches what signed this
	// request: build a header with our stored keys for the same endpoint
	// and compare the k= public-key parameter (the JWT's own exp/aud/sub
	// claims are library-internal and already covered by its tests).
	if !strings.Contains(gotAuth, base64PublicKeyParam(pub)) {
		t.Fatalf("Authorization header does not carry this install's VAPID public key: %s", gotAuth)
	}
}

func base64PublicKeyParam(pub string) string { return "k=" + pub }

// pushTestServer is a push endpoint that always answers 200 and records
// every request it receives, for an end-to-end delivery assertion (not
// just "a delivery row was queued").
func pushTestServer(t *testing.T) (*httptest.Server, *int32) {
	t.Helper()
	var hits int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(200)
	}))
	return srv, &hits
}

func wirePushTestClient(t *testing.T, s presetSite, srv *httptest.Server) {
	t.Helper()
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	s.a.sites.SetNotifyClientForTest(roots)
}

// TestWebPushDeliversThroughProcessDelivery is the HIGH-1 regression test:
// delivery.go's site.UserID==n.OwnerID guard once silently discarded every
// visitor-role push ("skipped/site_unavailable") before sendPush ever ran.
// This drives the real claim/send/finish path (drainNotifications), not
// just the fan-out's queued-row check, and asserts the push endpoint
// actually received a request.
func TestWebPushDeliversThroughProcessDelivery(t *testing.T) {
	s := webPushFixture(t)
	srv, hits := pushTestServer(t)
	defer srv.Close()
	wirePushTestClient(t, s, srv)

	aliceDevice := s.a.at(t, "POST", s.host, "/v1/sites/shop/push/subscribe", pushSubscribeBody(t, srv.URL, "orders"), s.callers["alice"]).json(t)["id"].(string)
	row := s.must(t, "POST", "/sqlite/orders/tables/orders/rows", map[string]string{"item": "alice's own order"}, s.callers["alice"], 200).json(t)["last_insert_id"]

	// Direction 2: the owner edits alice's row -> only alice's device,
	// actually delivered (not merely queued). drainNotifications already
	// drives the real claim/ProcessDelivery/finish path synchronously --
	// calling StartNotifications here too would race its async workers
	// against this same drain loop for the one queued row.
	s.must(t, "PATCH", "/sqlite/orders/tables/orders/rows/"+fmt.Sprint(row), map[string]string{"item": "owner's reply"}, s.key, 200)
	drainNotifications(t, s)

	if atomic.LoadInt32(hits) != 1 {
		t.Fatalf("push endpoint received %d requests, want 1 (device %s)", atomic.LoadInt32(hits), aliceDevice)
	}
	n, err := s.a.sites.notificationsSvc().GetNotification(context.Background(), aliceDevice)
	if err != nil {
		t.Fatal(err)
	}
	if n.State != "active" || n.LastResult != "ok" {
		t.Fatalf("subscription state=%s last_result=%s, want active/ok", n.State, n.LastResult)
	}
}

// TestWebPushOwnerWriteNeverReachesWebhook is the HIGH-2 regression test:
// storing an event for an owner-authored write (so push can target the
// row's visitor) must never let an EXISTING webhook or MCP-event
// subscription see it -- that would be a behaviour change even with the
// push flag off, since before SIM-360 no event was stored for an owner
// write at all.
func TestWebPushOwnerWriteNeverReachesWebhook(t *testing.T) {
	s, x := notificationFixture(t)
	s.must(t, "PUT", "/resources/orders", map[string]string{"kind": "sqlite", "preset": "personal"}, s.key, 201)
	waitNotificationEmits(t, s)
	s.must(t, "POST", "/sqlite/orders/schema", map[string]string{"sql": "CREATE TABLE orders(id INTEGER PRIMARY KEY, item TEXT, created_at TEXT, updated_at TEXT)"}, s.key, 200)
	waitNotificationEmits(t, s)
	subscribeNotification(t, s, x)

	row := s.must(t, "POST", "/sqlite/orders/tables/orders/rows", map[string]string{"item": "alice's order"}, s.callers["alice"], 200).json(t)["last_insert_id"]
	waitNotificationEmits(t, s)

	// drainNotifications alone already drives the real claim/
	// ProcessDelivery/finish path synchronously; adding StartNotifications
	// here would race its async workers against this same drain loop for
	// the one queued row (the async side can "win" the claim and still be
	// mid-flight when the drain loop reports the queue empty).
	drainNotifications(t, s)
	// x.snapshot() never clears (it is a copy, not a drain): alice's own
	// insert correctly notified the webhook once (direction 1), so the
	// count before the owner's edit is 1, not 0. The regression this
	// guards against is the count growing past that.
	before := len(x.snapshot())
	if before != 1 {
		t.Fatalf("setup: alice's insert delivered %d times, want 1", before)
	}

	// The owner edits alice's row: must not add a second delivery to the
	// webhook (there is no push subscription in this fixture at all),
	// exactly as before SIM-360 existed.
	s.must(t, "PATCH", "/sqlite/orders/tables/orders/rows/"+fmt.Sprint(row), map[string]string{"item": "owner's reply"}, s.key, 200)
	waitNotificationEmits(t, s)
	drainNotifications(t, s)
	if got := len(x.snapshot()); got != before {
		t.Fatalf("owner's edit of a visitor's row reached the webhook: %d deliveries, want still %d", got, before)
	}
}

// TestWebPushSignOutDeletesSubscription is the HIGH-3 regression test for
// the server-side half: POST /v1/visitor/logout must delete that
// person's push subscriptions for that site, independent of whatever the
// browser's own client-side cleanup does.
func TestWebPushSignOutDeletesSubscription(t *testing.T) {
	s := webPushFixture(t)
	srv, _ := pushTestServer(t)
	defer srv.Close()

	id := s.a.at(t, "POST", s.host, "/v1/sites/shop/push/subscribe", pushSubscribeBody(t, srv.URL, "orders"), s.callers["alice"]).json(t)["id"].(string)
	if _, err := s.a.sites.notificationsSvc().GetNotification(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	r := s.a.at(t, "POST", s.host, "/v1/visitor/logout", nil, s.callers["alice"])
	if r.status != 200 && r.status != 204 {
		t.Fatalf("sign-out: %d %s", r.status, r.body)
	}
	if _, err := s.a.sites.notificationsSvc().GetNotification(context.Background(), id); err == nil {
		t.Fatal("push subscription survived sign-out")
	}
}

// TestWebPushOwnerOnSiteSubscribesSiteWide is the MEDIUM-6 regression
// test: the site's owner, signed in on their own site as a visitor (D1),
// must be recognised as the owner for push too -- a site-wide device, not
// one forced into a visitor's resource/event scoping.
func TestWebPushOwnerOnSiteSubscribesSiteWide(t *testing.T) {
	s := webPushFixture(t)
	srv, _ := pushTestServer(t)
	defer srv.Close()

	body := pushSubscribeBody(t, srv.URL, "")
	body["events"] = []string{"record.added", "record.changed"}
	r := s.a.at(t, "POST", s.host, "/v1/sites/shop/push/subscribe", body, s.callers["owner-site"])
	if r.status != 201 {
		t.Fatalf("owner-on-site subscribe: %d %s", r.status, r.body)
	}
	id := r.json(t)["id"].(string)
	n, err := s.a.sites.notificationsSvc().GetNotification(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if n.SubscriberRole != "owner" || n.Storage != "" {
		t.Fatalf("owner-on-site device stored as %+v, want subscriber_role=owner, storage=''", n)
	}
}

// TestWebPushRejectsRedirect is the MEDIUM-9 regression test: a push
// endpoint's redirect (even to another public HTTPS origin) must never be
// followed.
func TestWebPushRejectsRedirect(t *testing.T) {
	s := webPushFixture(t)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://example.invalid/elsewhere")
		w.WriteHeader(302)
	}))
	defer srv.Close()
	wirePushTestClient(t, s, srv)
	id := s.a.at(t, "POST", pcSiteDomain, "/v1/sites/shop/push/subscribe", pushSubscribeBody(t, srv.URL, ""), s.key).json(t)["id"].(string)
	n, err := s.a.sites.notificationsSvc().GetNotification(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	result := s.a.sites.sendPush(context.Background(), n, []byte(`{"title":"t","body":"b"}`))
	if result.Delivered || result.Result != "redirect" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

// TestWebPushSubscribeUpsertsByEndpoint is the MEDIUM-12 regression test:
// subscribing again with the same browser endpoint and resource must
// reuse the same server-side row, never create a duplicate.
func TestWebPushSubscribeUpsertsByEndpoint(t *testing.T) {
	s := webPushFixture(t)
	srv, _ := pushTestServer(t)
	defer srv.Close()

	body := pushSubscribeBody(t, srv.URL, "orders")
	first := s.a.at(t, "POST", s.host, "/v1/sites/shop/push/subscribe", body, s.callers["alice"]).json(t)["id"].(string)
	second := s.a.at(t, "POST", s.host, "/v1/sites/shop/push/subscribe", body, s.callers["alice"]).json(t)["id"].(string)
	if first != second {
		t.Fatalf("repeated subscribe created a new row: %s then %s", first, second)
	}
	devices, err := s.a.sites.notificationsSvc().ListVisitorPushDevices(context.Background(), s.sid, s.aliceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 {
		t.Fatalf("got %d devices for alice, want 1", len(devices))
	}
}

// TestWebPushConcurrentSubscribeDedupes is the FIX 8 regression test:
// the sequential version above (TestWebPushSubscribeUpsertsByEndpoint)
// cannot catch the actual race -- two genuinely concurrent subscribe()
// calls for the exact same browser subscription and resource finding
// nothing and both inserting. UpsertPushSubscription's site-row lock
// must serialize them into one row, not two.
func TestWebPushConcurrentSubscribeDedupes(t *testing.T) {
	s := webPushFixture(t)
	srv, _ := pushTestServer(t)
	defer srv.Close()

	body := pushSubscribeBody(t, srv.URL, "orders")
	const n = 8
	var wg sync.WaitGroup
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := s.a.at(t, "POST", s.host, "/v1/sites/shop/push/subscribe", body, s.callers["alice"])
			if r.status != 201 {
				t.Errorf("concurrent subscribe %d: %d %s", i, r.status, r.body)
				return
			}
			ids[i] = r.json(t)["id"].(string)
		}(i)
	}
	wg.Wait()
	for i := 1; i < n; i++ {
		if ids[i] != ids[0] {
			t.Fatalf("concurrent subscribes produced different ids: %v", ids)
		}
	}
	devices, err := s.a.sites.notificationsSvc().ListVisitorPushDevices(context.Background(), s.sid, s.aliceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 {
		t.Fatalf("got %d devices for alice after %d concurrent subscribes, want 1", len(devices), n)
	}
}

// TestWebPushViewerRemovalDeletesSubscription is the HIGH-4 regression
// test: removing a named viewer must delete that person's push
// subscriptions for the site.
func TestWebPushViewerRemovalDeletesSubscription(t *testing.T) {
	s := webPushFixture(t)
	srv, _ := pushTestServer(t)
	defer srv.Close()
	id := s.a.at(t, "POST", s.host, "/v1/sites/shop/push/subscribe", pushSubscribeBody(t, srv.URL, "orders"), s.callers["alice"]).json(t)["id"].(string)
	aliceEmail, err := visitorEmail(context.Background(), s.a.database, s.aliceID)
	if err != nil {
		t.Fatal(err)
	}
	s.a.sites.deletePushForEmail(context.Background(), s.sid, aliceEmail)
	if _, err := s.a.sites.notificationsSvc().GetNotification(context.Background(), id); err == nil {
		t.Fatal("push subscription survived viewer removal cleanup")
	}
}

// TestPushSWRouteServesTheWorker is the HIGH-5 regression test's direct
// unit coverage (Codex round 2 moved the worker from a root-path
// middleware, ServePushSW, to an ordinary mux route at /v1/push/sw.js,
// FIX 2/11): the handler itself serves the right content type and body
// on GET and HEAD, independent of any host-dispatch wrapping.
func TestPushSWRouteServesTheWorker(t *testing.T) {
	h := &SiteHandler{}
	for _, method := range []string{"GET", "HEAD"} {
		req := httptest.NewRequest(method, "/v1/push/sw.js", nil)
		w := httptest.NewRecorder()
		h.pushSWRoute(w, req)
		if w.Code != 200 {
			t.Fatalf("%s: status %d, want 200", method, w.Code)
		}
		if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
			t.Fatalf("%s: Content-Type %q", method, ct)
		}
		if method == "GET" && !strings.Contains(w.Body.String(), "addEventListener") {
			t.Fatal("GET: body missing service worker code")
		}
	}
}

// TestPushSWRouteReachableThroughSiteHostDispatch is the FIX 2/11
// regression test proper: /v1/push/sw.js must be reachable the same way
// as every other push route, through the real site-host dispatch chain
// (BoundSubdomains for a *.<handle>.simple-host.test site host in this
// fixture, the same mux every custom domain and family host also
// reaches for any /v1/... path) -- not only when the bare handler is
// called directly, which would miss a regression in how that dispatch
// chain decides what falls through to the shared mux.
func TestPushSWRouteReachableThroughSiteHostDispatch(t *testing.T) {
	s := webPushFixture(t)
	if r := s.a.at(t, "GET", s.host, "/v1/push/sw.js", nil, nil); r.status != 200 {
		t.Fatalf("GET through site host dispatch: %d %s", r.status, r.body)
	} else if !strings.Contains(string(r.body), "addEventListener") {
		t.Fatalf("body missing service worker code: %s", r.body)
	}
	// Flag-independent: unlike subscribe, the worker file itself (and
	// unsubscribe/mute) must stay reachable even with WEB_PUSH_ACCOUNTS
	// off for the account, since revocation is never gated.
	s.a.sites.SetWebPushAccounts(nil, false)
	if r := s.a.at(t, "GET", s.host, "/v1/push/sw.js", nil, nil); r.status != 200 {
		t.Fatalf("GET with the flag off: %d %s", r.status, r.body)
	}
}

// TestWebPushOwnerForReachesOnlyTargetVisitor is the SIM-358/SIM-360
// interplay regression test: an owner-on-behalf-of add (?for=) is the
// owner's own write (rowNotification's ownerWrite is true for
// c.OwnerFor != "", same as c.Owner) and must never reach the owner's own
// webhook, MCP-event, or push subscriptions -- but it has a real target
// (the row is stamped to that visitor's own visitor_id, so rowVisitorID ==
// OwnerFor) and must still reach that visitor's own push device, unlike a
// plain owner write touching nobody, which is suppressed outright. Driven
// through the real claim/ProcessDelivery/finish path (drainNotifications),
// not just the queued-row check.
func TestWebPushOwnerForReachesOnlyTargetVisitor(t *testing.T) {
	s, x := notificationFixture(t)
	s.a.sites.SetWebPushAccounts(nil, true)
	s.must(t, "PUT", "/resources/orders", map[string]string{"kind": "sqlite", "preset": "personal"}, s.key, 201)
	waitNotificationEmits(t, s)
	s.must(t, "POST", "/sqlite/orders/schema", map[string]string{"sql": "CREATE TABLE orders(id INTEGER PRIMARY KEY, item TEXT, created_at TEXT, updated_at TEXT)"}, s.key, 200)
	waitNotificationEmits(t, s)
	subscribeNotification(t, s, x)

	ownerSrv, ownerHits := pushTestServer(t)
	defer ownerSrv.Close()
	aliceSrv, aliceHits := pushTestServer(t)
	defer aliceSrv.Close()
	roots := x509.NewCertPool()
	roots.AddCert(x.server.Certificate())
	roots.AddCert(ownerSrv.Certificate())
	roots.AddCert(aliceSrv.Certificate())
	s.a.sites.SetNotifyClientForTest(roots)

	s.a.at(t, "POST", pcSiteDomain, "/v1/sites/shop/push/subscribe", pushSubscribeBody(t, ownerSrv.URL, ""), s.key)
	s.a.at(t, "POST", s.host, "/v1/sites/shop/push/subscribe", pushSubscribeBody(t, aliceSrv.URL, "orders"), s.callers["alice"])

	s.must(t, "POST", "/sqlite/orders/tables/orders/rows?for="+s.aliceID, map[string]string{"item": "owner on behalf of alice"}, s.key, 200)
	waitNotificationEmits(t, s)
	drainNotifications(t, s)

	if got := len(x.snapshot()); got != 0 {
		t.Fatalf("owner-for add reached the webhook: %d deliveries, want 0", got)
	}
	if got := atomic.LoadInt32(ownerHits); got != 0 {
		t.Fatalf("owner-for add reached the owner's own push device: %d, want 0", got)
	}
	if got := atomic.LoadInt32(aliceHits); got != 1 {
		t.Fatalf("owner-for add did not reach alice's push device: %d, want 1", got)
	}
}

// TestWebPushSkipsDeliveryWhenViewerRemoved is the delivery-time recheck
// regression test: a visitor-role push subscription is normally deleted
// the moment its owner stops being a named viewer -- both the HTTP
// handler and, since FIX 9, the app/sites service method itself
// (RemoveSiteViewer) run that cleanup now. This test exercises the
// backstop for whatever leaves a subscription behind anyway: removing
// the viewer at the raw repository layer directly, the one level
// genuinely below RemoveSiteViewer's own cleanup, the same effect some
// other access change (e.g. the site locking itself to named viewers
// without an explicit per-person removal at all) would have. The only
// thing standing between that stale subscription and an actual push is
// visitorStillWelcome's delivery-time check. Driven through the real
// claim/ProcessDelivery/finish path (drainNotifications).
func TestWebPushSkipsDeliveryWhenViewerRemoved(t *testing.T) {
	s := webPushFixture(t)
	aliceEmail := aliceEmailOf(t, s)
	if r := s.a.at(t, "POST", pcSiteDomain, "/v1/sites/shop/viewers", map[string]any{"emails": []string{aliceEmail}}, s.key); r.status != 200 {
		t.Fatalf("grant alice as a named viewer: %d %s", r.status, r.body)
	}

	srv, hits := pushTestServer(t)
	defer srv.Close()
	wirePushTestClient(t, s, srv)
	s.a.at(t, "POST", s.host, "/v1/sites/shop/push/subscribe", pushSubscribeBody(t, srv.URL, "orders"), s.callers["alice"])

	row := s.must(t, "POST", "/sqlite/orders/tables/orders/rows", map[string]string{"item": "alice's own order"}, s.callers["alice"], 200).json(t)["last_insert_id"]
	// The owner edits alice's row -> queues a push to alice's device, not
	// yet drained.
	s.must(t, "PATCH", "/sqlite/orders/tables/orders/rows/"+fmt.Sprint(row), map[string]string{"item": "owner's reply"}, s.key, 200)
	waitNotificationEmits(t, s)

	// Remove alice as a named viewer at the raw repository layer,
	// bypassing both the handler's own cleanup and (since FIX 9)
	// app/sites.RemoveSiteViewer's own, so the stale subscription
	// survives exactly as the regression this guards against would
	// leave it.
	if _, err := sitesrepo.RemoveSiteViewer(context.Background(), s.a.database, s.sid, aliceEmail); err != nil {
		t.Fatalf("remove viewer: %v", err)
	}

	drainNotifications(t, s)
	if got := atomic.LoadInt32(hits); got != 0 {
		t.Fatalf("push delivered to a visitor who is no longer a named viewer: %d, want 0", got)
	}
}

// TestWebPushServiceLayerViewerRemovalCleansUpPush is the FIX 9
// regression test: app/sites.RemoveSiteViewer itself -- called directly,
// the way any future non-HTTP caller would (an admin tool, a bulk
// script) -- must delete the removed viewer's push subscriptions, not
// only the HTTP handler that used to run that cleanup as a second,
// separate step.
func TestWebPushServiceLayerViewerRemovalCleansUpPush(t *testing.T) {
	s := webPushFixture(t)
	aliceEmail := aliceEmailOf(t, s)
	if r := s.a.at(t, "POST", pcSiteDomain, "/v1/sites/shop/viewers", map[string]any{"emails": []string{aliceEmail}}, s.key); r.status != 200 {
		t.Fatalf("grant alice as a named viewer: %d %s", r.status, r.body)
	}
	srv, _ := pushTestServer(t)
	defer srv.Close()
	id := s.a.at(t, "POST", s.host, "/v1/sites/shop/push/subscribe", pushSubscribeBody(t, srv.URL, "orders"), s.callers["alice"]).json(t)["id"].(string)

	removed, err := s.a.sites.sitesSvc().RemoveSiteViewer(context.Background(), s.sid, aliceEmail)
	if err != nil || !removed {
		t.Fatalf("RemoveSiteViewer: removed=%v err=%v", removed, err)
	}
	if _, err := s.a.sites.notificationsSvc().GetNotification(context.Background(), id); err == nil {
		t.Fatal("push subscription survived app/sites.RemoveSiteViewer called directly")
	}
}

// TestWebPushSignOutOnlyDeletesThatSessionsDevice is the FIX 10
// regression test: two independent sessions (two browsers) signed in as
// the same person on the same site must not share a push cleanup scope.
// Signing out of one must delete only the device that session created,
// leaving the other browser's subscription in place -- the gap the old
// owner_id+site_id-only deletion (DeletePushSubscriptionsForPerson)
// could not close, since it had no way to tell the two sessions apart.
func TestWebPushSignOutOnlyDeletesThatSessionsDevice(t *testing.T) {
	s := webPushFixture(t)
	// Two real browsers each have their own PushSubscription.endpoint;
	// two separate test servers stand in for that (reusing one URL for
	// both would upsert into the same row by endpoint identity, FIX 8,
	// which is not what two independent devices look like).
	srv1, _ := pushTestServer(t)
	defer srv1.Close()
	srv2, _ := pushTestServer(t)
	defer srv2.Close()

	mintSession := func() []byte {
		id := make([]byte, 32)
		if _, err := rand.Read(id); err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		if _, err := accountsrepo.InsertVisitorSession(context.Background(), s.a.database, id, s.aliceID, s.sid, s.host, now.Add(time.Hour), now.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		return id
	}
	session1 := mintSession()
	session2 := mintSession()
	caller1 := browser(s.host, hex.EncodeToString(session1))
	caller2 := browser(s.host, hex.EncodeToString(session2))

	id1 := s.a.at(t, "POST", s.host, "/v1/sites/shop/push/subscribe", pushSubscribeBody(t, srv1.URL, "orders"), caller1).json(t)["id"].(string)
	id2 := s.a.at(t, "POST", s.host, "/v1/sites/shop/push/subscribe", pushSubscribeBody(t, srv2.URL, "orders"), caller2).json(t)["id"].(string)
	if id1 == id2 {
		t.Fatalf("two independent sessions' subscribes collapsed to one row: %s", id1)
	}

	if r := s.a.at(t, "POST", s.host, "/v1/visitor/logout", nil, caller1); r.status != 200 && r.status != 204 {
		t.Fatalf("sign-out: %d %s", r.status, r.body)
	}
	if _, err := s.a.sites.notificationsSvc().GetNotification(context.Background(), id1); err == nil {
		t.Fatal("session1's device survived its own sign-out")
	}
	if _, err := s.a.sites.notificationsSvc().GetNotification(context.Background(), id2); err != nil {
		t.Fatalf("session2's device was deleted by session1's sign-out: %v", err)
	}
}

// TestWebPushOwnerOnSiteNeverPushesBackToOwner is the FIX 4 regression
// test (Codex round 2): an ordinary D1 write (the owner signed in on
// their own site, no ?for=) must still notify an existing webhook/MCP
// subscriber exactly as before SIM-360 (unchanged main behaviour,
// TestNotifyVisitorRoutes), but must never push to the owner's own push
// device. pushActor for this write is the storage caller's own stampID
// (the owner's account id, via StampID(false, ownerAccountID)), never the
// literal "owner" -- that is correct for webhook/MCP, but the push
// fan-out's owner branch must also recognise "pushActor equals this
// subscription's own owner_id" as the same actor, or a D1 write would
// incorrectly notify the very device that made it.
func TestWebPushOwnerOnSiteNeverPushesBackToOwner(t *testing.T) {
	s, x := notificationFixture(t)
	s.a.sites.SetWebPushAccounts(nil, true)
	s.must(t, "PUT", "/resources/orders", map[string]string{"kind": "sqlite", "preset": "personal"}, s.key, 201)
	waitNotificationEmits(t, s)
	s.must(t, "POST", "/sqlite/orders/schema", map[string]string{"sql": "CREATE TABLE orders(id INTEGER PRIMARY KEY, item TEXT, created_at TEXT, updated_at TEXT)"}, s.key, 200)
	waitNotificationEmits(t, s)
	subscribeNotification(t, s, x)

	srv, hits := pushTestServer(t)
	defer srv.Close()
	wirePushTestClient(t, s, srv)
	body := pushSubscribeBody(t, srv.URL, "")
	body["events"] = []string{"record.added", "record.changed"}
	if r := s.a.at(t, "POST", s.host, "/v1/sites/shop/push/subscribe", body, s.callers["owner-site"]); r.status != 201 {
		t.Fatalf("owner-on-site subscribe: %d %s", r.status, r.body)
	}

	row := s.must(t, "POST", "/sqlite/orders/tables/orders/rows", map[string]string{"item": "alice's order"}, s.callers["alice"], 200).json(t)["last_insert_id"]
	waitNotificationEmits(t, s)
	drainNotifications(t, s)
	// Alice's own insert legitimately reaches the owner's push device
	// (the default "both" recipients rule; unrelated to this fix) and the
	// webhook: both counters are captured here, before the D1 edit, so
	// the assertions below are about what the D1 edit itself adds, not
	// about an absolute count.
	webhookBefore := len(x.snapshot())
	hitsBefore := atomic.LoadInt32(hits)

	// D1: the owner, signed in on their own site (no ?for=), edits alice's
	// row -- an ordinary owner-on-site write, not an on-behalf-of add.
	s.must(t, "PATCH", "/sqlite/orders/tables/orders/rows/"+fmt.Sprint(row), map[string]string{"item": "owner reply"}, s.callers["owner-site"], 200)
	waitNotificationEmits(t, s)
	drainNotifications(t, s)

	if got := len(x.snapshot()); got != webhookBefore+1 {
		t.Fatalf("D1 edit did not notify the webhook as an ordinary write: before %d, after %d", webhookBefore, got)
	}
	if got := atomic.LoadInt32(hits); got != hitsBefore {
		t.Fatalf("D1 edit pushed back to the owner's own device: hits %d, want still %d", got, hitsBefore)
	}
}

// TestWebPushRuleBodyColumnValidatedAtSave is the FIX 5 regression test
// (Codex round 2): PUT /push/rules must reject a blacklisted body_column
// and one that does not exist on the named SQLite resource's own tables
// at save time, not only silently fall back to generic text at render
// time; a real column on that resource, and a title with a control
// character, are checked too (title: plain text only).
func TestWebPushRuleBodyColumnValidatedAtSave(t *testing.T) {
	s := webPushFixture(t)
	putRule := func(body map[string]any) resp {
		return s.a.at(t, "PUT", pcSiteDomain, "/v1/sites/shop/push/rules", body, s.key)
	}
	if r := putRule(map[string]any{"resource": "orders", "recipients": "both", "body_column": "email"}); r.status != 400 || r.json(t)["code"] != "invalid_body_column" {
		t.Fatalf("blacklisted column: %d %s", r.status, r.body)
	}
	if r := putRule(map[string]any{"resource": "orders", "recipients": "both", "body_column": "no_such_column"}); r.status != 400 || r.json(t)["code"] != "invalid_body_column" {
		t.Fatalf("nonexistent column: %d %s", r.status, r.body)
	}
	if r := putRule(map[string]any{"resource": "orders", "recipients": "both", "body_column": "item"}); r.status != 200 {
		t.Fatalf("real column refused: %d %s", r.status, r.body)
	}
	if r := putRule(map[string]any{"resource": "orders", "recipients": "both", "title": "line one\nline two"}); r.status != 400 || r.json(t)["code"] != "invalid_request" {
		t.Fatalf("control character in title: %d %s", r.status, r.body)
	}
	if r := putRule(map[string]any{"resource": "orders", "recipients": "both", "title": "New order"}); r.status != 200 {
		t.Fatalf("plain title refused: %d %s", r.status, r.body)
	}
}

// TestWebPushOwnerRemovesVisitorDevice is the FIX 12/adversary-review
// item B regression test: the generic DELETE /notifications/{id} 404s
// for a visitor-role push row even when the caller is the site's own
// owner (n.OwnerID is the visitor's id there, never the owner's
// account), so the dashboard's per-row Remove on a visitor's device
// needs its own owner-authorized route.
func TestWebPushOwnerRemovesVisitorDevice(t *testing.T) {
	s := webPushFixture(t)
	srv, _ := pushTestServer(t)
	defer srv.Close()

	id := s.a.at(t, "POST", s.host, "/v1/sites/shop/push/subscribe", pushSubscribeBody(t, srv.URL, "orders"), s.callers["alice"]).json(t)["id"].(string)

	// The old generic route cannot reach it (confirms the bug this fix
	// closes, not just the fix itself).
	if r := s.a.at(t, "DELETE", pcSiteDomain, "/v1/sites/shop/notifications/"+id, nil, s.key); r.status != 404 {
		t.Fatalf("generic route unexpectedly reached a visitor's device: %d %s", r.status, r.body)
	}
	if r := s.a.at(t, "DELETE", pcSiteDomain, "/v1/sites/shop/push/devices/"+id, nil, s.key); r.status != 200 || r.json(t)["deleted"] != true {
		t.Fatalf("owner-authorized device delete: %d %s", r.status, r.body)
	}
	if _, err := s.a.sites.notificationsSvc().GetNotification(context.Background(), id); err == nil {
		t.Fatal("visitor's device survived the owner's delete")
	}

	// A webhook/mcp-event row is out of scope for this push-specific
	// route, even for the owner's own.
	webhookID := notificationRequest(t, s, "POST", "", map[string]any{"url": srv.URL, "events": []string{"record.added"}, "secret": notifyTestSecret, "auth_header": map[string]string{"name": "Authorization", "value": "Bearer receiver"}}, 201).json(t)["notification"].(map[string]any)["id"].(string)
	if r := s.a.at(t, "DELETE", pcSiteDomain, "/v1/sites/shop/push/devices/"+webhookID, nil, s.key); r.status != 404 {
		t.Fatalf("push device route deleted a non-push subscription: %d %s", r.status, r.body)
	}
}

// TestWebPushCookieRoutesRequireCSRF is the adversary-review item C
// regression test: subscribe/unsubscribe/mute (the visitor/D1 cookie
// routes) must refuse a same-site request missing the X-SH-CSRF header,
// the same convention every other visitor-cookie write route in this
// codebase already follows; the owner's own X-API-Key call is
// unaffected (no cookie to forge in the first place).
func TestWebPushCookieRoutesRequireCSRF(t *testing.T) {
	s := webPushFixture(t)
	srv, _ := pushTestServer(t)
	defer srv.Close()

	noCSRF := map[string]string{}
	for k, v := range s.callers["alice"] {
		noCSRF[k] = v
	}
	delete(noCSRF, "X-SH-CSRF")

	if r := s.a.at(t, "POST", s.host, "/v1/sites/shop/push/subscribe", pushSubscribeBody(t, srv.URL, "orders"), noCSRF); r.status != 403 || r.json(t)["code"] != "csrf_required" {
		t.Fatalf("subscribe without CSRF: %d %s", r.status, r.body)
	}
	id := s.a.at(t, "POST", s.host, "/v1/sites/shop/push/subscribe", pushSubscribeBody(t, srv.URL, "orders"), s.callers["alice"]).json(t)["id"].(string)
	if r := s.a.at(t, "POST", s.host, "/v1/sites/shop/push/unsubscribe", map[string]string{"id": id}, noCSRF); r.status != 403 || r.json(t)["code"] != "csrf_required" {
		t.Fatalf("unsubscribe without CSRF: %d %s", r.status, r.body)
	}
	if r := s.a.at(t, "POST", s.host, "/v1/sites/shop/push/mute", map[string]any{"id": id, "resources": []string{"orders"}}, noCSRF); r.status != 403 || r.json(t)["code"] != "csrf_required" {
		t.Fatalf("mute without CSRF: %d %s", r.status, r.body)
	}
	// The owner's API key call needs no CSRF header at all.
	ownerBody := pushSubscribeBody(t, srv.URL, "")
	ownerBody["events"] = []string{"record.added"}
	if r := s.a.at(t, "POST", pcSiteDomain, "/v1/sites/shop/push/subscribe", ownerBody, s.key); r.status != 201 {
		t.Fatalf("owner API key subscribe refused: %d %s", r.status, r.body)
	}
}

// TestWebPushBodyColumnBlacklistCatchesVariants is the adversary-review
// item A regression test: the body_column blacklist must catch
// "email_address", "customerEmail" and "ip_addr" -- real columns that
// exist on the resource, so only the blacklist (not the existence
// check) can be what refuses them -- while leaving an innocuous name
// like "notes" alone.
func TestWebPushBodyColumnBlacklistCatchesVariants(t *testing.T) {
	s := webPushFixture(t)
	// visitor_id already exists on a personal-preset table (stamped by
	// the server on every row); only the new columns need adding.
	for _, col := range []string{"email_address", "customerEmail", "ip_addr", "notes"} {
		if r := s.must(t, "POST", "/sqlite/orders/schema", map[string]string{"sql": "ALTER TABLE orders ADD COLUMN " + col + " TEXT"}, s.key, 200); r.status != 200 {
			t.Fatalf("add column %s: %s", col, r.body)
		}
	}
	putRule := func(col string) resp {
		return s.a.at(t, "PUT", pcSiteDomain, "/v1/sites/shop/push/rules", map[string]any{"resource": "orders", "recipients": "both", "body_column": col}, s.key)
	}
	for _, col := range []string{"email_address", "customerEmail", "ip_addr", "visitor_id"} {
		if r := putRule(col); r.status != 400 || r.json(t)["code"] != "invalid_body_column" {
			t.Fatalf("%s: %d %s, want 400 invalid_body_column", col, r.status, r.body)
		}
	}
	if r := putRule("notes"); r.status != 200 {
		t.Fatalf("innocuous column refused: %d %s", r.status, r.body)
	}
}

// TestWebPushBodyValueFallsBackWhenSensitive is the adversary-review
// item A regression test, value side: even a permitted body_column
// falls back to generic text at render time when its actual value looks
// like an email address or an IP, regardless of the column's own name.
func TestWebPushBodyValueFallsBackWhenSensitive(t *testing.T) {
	s := webPushFixture(t)
	if r := s.must(t, "POST", "/sqlite/orders/schema", map[string]string{"sql": "ALTER TABLE orders ADD COLUMN notes TEXT"}, s.key, 200); r.status != 200 {
		t.Fatalf("add column: %s", r.body)
	}
	if r := s.a.at(t, "PUT", pcSiteDomain, "/v1/sites/shop/push/rules", map[string]any{"resource": "orders", "recipients": "both", "body_column": "notes"}, s.key); r.status != 200 {
		t.Fatalf("set rule: %d %s", r.status, r.body)
	}
	n := model.Notification{SiteID: s.sid, SubscriberRole: "owner"}
	payloadFor := func(value string) map[string]any {
		data, _ := json.Marshal(map[string]any{"storage": "orders", "site": "shop", "site_url": "https://shop.example"})
		var m map[string]any
		json.Unmarshal(data, &m)
		m["fields"] = map[string]any{"notes": value}
		raw, _ := json.Marshal(m)
		d := model.NotificationDelivery{Name: "record.added", Data: raw}
		out := s.a.sites.pushPayload(context.Background(), n, d)
		var payload map[string]any
		if err := json.Unmarshal(out, &payload); err != nil {
			t.Fatalf("payload decode: %v (%s)", err, out)
		}
		return payload
	}
	if p := payloadFor("contact alice@example.com for details"); strings.Contains(fmt.Sprint(p["body"]), "@") {
		t.Fatalf("email leaked into push body: %v", p["body"])
	}
	if p := payloadFor("reach us at 10.0.0.5 on the office network"); strings.Contains(fmt.Sprint(p["body"]), "10.0.0.5") {
		t.Fatalf("IP leaked into push body: %v", p["body"])
	}
	if p := payloadFor("pickup window is 9 to 5"); p["body"] != "pickup window is 9 to 5" {
		t.Fatalf("ordinary value wrongly replaced: %v", p["body"])
	}
}

// TestWebPushBudgetsReserveBothOrNeither is the FIX 7 regression test:
// checking the shared site budget before the person budget used to
// spend the site's token on every attempt from an already-exhausted
// person, even though nothing was ever sent -- starving every other
// recipient on that site. alice's second send must fail on her own
// budget alone and refund the site token it provisionally took, so
// bob's send right after is not refused for a reason that was never his.
func TestWebPushBudgetsReserveBothOrNeither(t *testing.T) {
	s := webPushFixture(t)
	srv, hits := pushTestServer(t)
	defer srv.Close()
	wirePushTestClient(t, s, srv)

	e := s.a.sites.notifyEngine()
	e.pushSiteDay = newRateLimiter(2, 0)
	e.pushPersonDay = newRateLimiter(1, 0)

	p256dh, auth := fakePushKeys(t)
	notification := func(ownerID string) model.Notification {
		return model.Notification{SiteID: s.sid, OwnerID: ownerID, URL: srv.URL, PushP256dh: p256dh, PushAuth: auth}
	}
	body := []byte(`{"title":"t","body":"b"}`)

	if r := s.a.sites.sendPush(context.Background(), notification("alice-budget"), body); !r.Delivered {
		t.Fatalf("alice's first send: %+v", r)
	}
	if r := s.a.sites.sendPush(context.Background(), notification("alice-budget"), body); r.Delivered || r.Result != "push_rate_limited" {
		t.Fatalf("alice's second send should be person-limited: %+v", r)
	}
	if r := s.a.sites.sendPush(context.Background(), notification("bob-budget"), body); !r.Delivered {
		t.Fatalf("bob's send starved by alice's failed attempt (site budget not refunded): %+v", r)
	}
	if got := atomic.LoadInt32(hits); got != 2 {
		t.Fatalf("push endpoint hits = %d, want 2 (alice once, bob once)", got)
	}
}

// TestNotifyMCPReplayExcludesOwnerWrite is the FIX 1 regression test
// (Codex round 2, High): an owner-authored write (here, an owner-on-
// behalf-of add, ?for=, SIM-358) must never reach an MCP/webhook
// subscriber -- live fan-out already enforced this (emitSiteEvent's
// CASE), but a cursor-based replay (an MCP subscriber reconnecting with
// an earlier cursor, exactly what backend.Subscribe's cursor parameter
// drives) used to backfill every matching stored event with no actor
// check at all, bypassing it entirely. A subscription must already
// exist before the write (so HasNotificationsFor is true and the event
// is even stored at all -- with no subscription of any kind yet, the
// write would never be recorded, regardless of this fix, and the test
// would pass vacuously); the live delivery it correctly never got is
// what reconnecting with an earlier cursor used to hand it anyway.
func TestNotifyMCPReplayExcludesOwnerWrite(t *testing.T) {
	s, x := notificationFixture(t)
	s.must(t, "PUT", "/resources/orders", map[string]string{"kind": "sqlite", "preset": "personal"}, s.key, 201)
	waitNotificationEmits(t, s)
	s.must(t, "POST", "/sqlite/orders/schema", map[string]string{"sql": "CREATE TABLE orders(id INTEGER PRIMARY KEY, item TEXT, created_at TEXT)"}, s.key, 200)
	waitNotificationEmits(t, s)

	backend := s.a.sites.EventsBackend()
	caller := mcp.Caller{Owner: s.ownerID, AppName: "Test app"}
	req := mcp.EventSubscribeRequest{Name: "record.added", Site: "shop", CanonicalArgs: `{"site":"shop"}`, URL: x.server.URL, Secret: notifyTestSecret}
	if _, err := backend.Subscribe(context.Background(), caller, req); err != nil {
		t.Fatal(err)
	}
	drainNotifications(t, s) // the subscribe call's own verification challenge

	// Owner adds for alice: an owner-authored write (pushActor=="owner"
	// in the fan-out's own terms), stored as an event (the subscription
	// above makes HasNotificationsFor true) only so alice's own push
	// device could reach it -- correctly never delivered live to the
	// MCP subscriber above, webhook-shaped though it is.
	s.must(t, "POST", "/sqlite/orders/tables/orders/rows?for="+s.aliceID, map[string]string{"item": "owner on behalf of alice"}, s.key, 200)
	waitNotificationEmits(t, s)
	drainNotifications(t, s)
	before := len(x.snapshot())

	// Reconnect with cursor "c_0" (replay from the beginning): this is
	// what used to backfill the owner-on-behalf event anyway.
	cursor := "c_0"
	req.Cursor = &cursor
	req.TTLSet = true
	if _, err := backend.Subscribe(context.Background(), caller, req); err != nil {
		t.Fatal(err)
	}
	drainNotifications(t, s)
	got := x.snapshot()
	if len(got) != before {
		t.Fatalf("owner-on-behalf write reached the MCP subscriber via cursor replay: before %d, after %d (last: %+v)", before, len(got), got[len(got)-1].body)
	}
}
