package datatest

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/data/datahttp"
	notifyapp "github.com/vsriram/simple-host/host/data/notify"
	notificationsrepo "github.com/vsriram/simple-host/host/data/repo/notify"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/model"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
	"github.com/vsriram/simple-host/host/sqlkit"
)

const notifyTestSecret = "whsec_MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="

// notifyTestKey is a PASSCODE_ENC_KEY for tests (32 bytes).
const notifyTestKey = "q83vEjRWeJq83vEjRWeJq83vEjRWeJq83vEjRWeJq80="

// otherNotifyKey is a different install key, for key-change tests.
var otherNotifyKey = base64.StdEncoding.EncodeToString([]byte(strings.Repeat("\x07", 32)))

type receivedNotification struct {
	header    http.Header
	body      map[string]any
	duplicate bool
}

// notificationReceiver is a TLS webhook endpoint that verifies each
// request's Standard Webhooks signature against its keys and echoes the
// verification challenge.
type notificationReceiver struct {
	server      *httptest.Server
	mu          sync.Mutex
	keys        []string
	seen        map[string]bool
	requests    []receivedNotification
	status      int
	echo        bool
	reflectBody bool
}

func newNotificationReceiver(t *testing.T) *notificationReceiver {
	t.Helper()
	x := &notificationReceiver{keys: []string{notifyTestSecret}, seen: map[string]bool{}, status: 200, echo: true}
	x.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		x.mu.Lock()
		defer x.mu.Unlock()
		id, ts := r.Header.Get("webhook-id"), r.Header.Get("webhook-timestamp")
		seconds, err := strconv.ParseInt(ts, 10, 64)
		if id == "" || err != nil || time.Since(time.Unix(seconds, 0)).Abs() > 5*time.Minute {
			t.Error("missing id or stale timestamp")
		}
		for _, secret := range x.keys {
			key, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
			mac := hmac.New(sha256.New, key)
			_, _ = fmt.Fprintf(mac, "%s.%s.", id, ts)
			mac.Write(body)
			valid := false
			for _, entry := range strings.Fields(r.Header.Get("webhook-signature")) {
				version, encoded, ok := strings.Cut(entry, ",")
				signature, e := base64.StdEncoding.DecodeString(encoded)
				if ok && version == "v1" && e == nil && hmac.Equal(mac.Sum(nil), signature) {
					valid = true
				}
			}
			if !valid {
				t.Error("signature did not verify")
			}
		}
		var payload map[string]any
		if json.Unmarshal(body, &payload) != nil {
			t.Error("invalid event JSON")
		}
		x.requests = append(x.requests, receivedNotification{r.Header.Clone(), payload, x.seen[id]})
		x.seen[id] = true
		w.WriteHeader(x.status)
		if x.reflectBody {
			_, _ = w.Write(body)
		} else if payload["type"] == "verification" && x.echo {
			_ = json.NewEncoder(w).Encode(map[string]any{"challenge": payload["challenge"]})
		} else {
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(x.server.Close)
	return x
}

func (x *notificationReceiver) roots() *x509.CertPool {
	roots := x509.NewCertPool()
	roots.AddCert(x.server.Certificate())
	return roots
}

func (x *notificationReceiver) snapshot() []receivedNotification {
	x.mu.Lock()
	defer x.mu.Unlock()
	return append([]receivedNotification(nil), x.requests...)
}

func (x *notificationReceiver) set(f func(x *notificationReceiver)) {
	x.mu.Lock()
	defer x.mu.Unlock()
	f(x)
}

func (x *notificationReceiver) setStatus(status int) {
	x.set(func(x *notificationReceiver) { x.status = status })
}

// notifyLimits is an edition with change applied to the default limits.
func notifyLimits(change func(*config.Limits)) *edition.Edition {
	l := config.DefaultLimits()
	change(&l)
	return edition.Default(l)
}

// notificationFixture is the shop with the install key set and webhook
// delivery trusting the receiver it returns.
func notificationFixture(t *testing.T, eds ...*edition.Edition) (Shop, *notificationReceiver) {
	t.Helper()
	isolateQueueTest(t)
	s := NewShop(t, eds...)
	x := newNotificationReceiver(t)
	setNotifyKey(t, s, notifyTestKey)
	s.A.Data.Notifier().UseTestClient(x.roots(), true)
	return s, x
}

func setNotifyKey(t *testing.T, s Shop, key string) {
	t.Helper()
	if err := s.A.Data.PasscodeKeys.Set(key); err != nil {
		t.Fatal(err)
	}
}

func notificationRequest(t *testing.T, s Shop, method, suffix string, body any, status int) rt.Resp {
	t.Helper()
	r := s.A.At(t, method, rt.SiteDomain, "/v1/sites/shop/notifications"+suffix, body, s.Key)
	if r.Status != status {
		t.Fatalf("%s notifications%s: status %d, want %d: %s", method, suffix, r.Status, status, r.Body)
	}
	return r
}

// subscribeNotification is an owner webhook on every record event.
func subscribeNotification(t *testing.T, s Shop, x *notificationReceiver) string {
	t.Helper()
	r := notificationRequest(t, s, "POST", "", map[string]any{"url": x.server.URL, "events": notifyapp.RecordEventNames, "secret": notifyTestSecret, "auth_header": map[string]string{"name": "Authorization", "value": "Bearer receiver"}}, 201)
	return r.JSON(t)["notification"].(map[string]any)["id"].(string)
}

// mcpRequest is a connector events/subscribe for record.added on the shop.
func mcpRequest(x *notificationReceiver) notifyapp.EventSubscribeRequest {
	return notifyapp.EventSubscribeRequest{Name: "record.added", Site: "shop", CanonicalArgs: `{"site":"shop"}`, URL: x.server.URL, Secret: notifyTestSecret}
}

func mcpSubscribe(s Shop, c notifyapp.Subscriber, req notifyapp.EventSubscribeRequest) (notifyapp.EventSubscribeResult, error) {
	return s.A.Data.Notifier().Subscribe(context.Background(), s.A.Data.NotifyEnv(), c, req)
}

func mustMCPSubscribe(t *testing.T, s Shop, c notifyapp.Subscriber, req notifyapp.EventSubscribeRequest) notifyapp.EventSubscribeResult {
	t.Helper()
	res, err := mcpSubscribe(s, c, req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func notificationSQL(t *testing.T, s Shop, query string, args ...any) {
	t.Helper()
	waitNotificationEmits(t, s)
	if _, err := s.A.Database.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func waitNotificationEmits(t *testing.T, s Shop) {
	t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for s.A.Data.Notifier().PendingForTest() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("notification emit did not finish")
		}
		time.Sleep(time.Millisecond)
	}
}

func drainNotifications(t *testing.T, s Shop) {
	t.Helper()
	waitNotificationEmits(t, s)
	for i := 0; i < 100; i++ {
		if !s.A.Data.DeliverNotification(context.Background()) {
			return
		}
	}
	t.Fatal("delivery queue did not drain")
}

// emitWorkerNotification stores one record.added event straight in the queue.
func emitWorkerNotification(t *testing.T, s Shop, site string) {
	t.Helper()
	if err := notificationsrepo.EmitSiteEvent(context.Background(), s.A.Database, site, notifyapp.EventID(), "record.added", "orders", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
}

func getNotification(t *testing.T, s Shop, id string) model.Notification {
	t.Helper()
	n, err := notificationsSvc(s.A.Data).GetNotification(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func notificationGone(s Shop, id string) bool {
	_, err := notificationsSvc(s.A.Data).GetNotification(context.Background(), id)
	return err != nil
}

func saveNotification(t *testing.T, s Shop, n model.Notification, cursor *int64) error {
	t.Helper()
	_, _, err := notificationsSvc(s.A.Data).SaveNotification(context.Background(), n, 10, cursor, 7, s.A.Data.NotifyEnv().Secrets())
	return err
}

// deliveryState is the newest delivery of subscription id.
func deliveryState(t *testing.T, s Shop, id string) (state, category string, attempts int) {
	t.Helper()
	if err := s.A.Database.QueryRow(`SELECT state,last_category,attempts FROM site_event_deliveries WHERE subscription_id=$1 ORDER BY event_seq DESC LIMIT 1`, id).Scan(&state, &category, &attempts); err != nil {
		t.Fatal(err)
	}
	return
}

func countRows(t *testing.T, s Shop, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.A.Database.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func assertNotificationSealed(t *testing.T, stored, plain string) {
	t.Helper()
	if !strings.HasPrefix(stored, "enc1:") || strings.Contains(stored, plain) {
		t.Fatal("notification value was not sealed")
	}
}

func openedNotification(t *testing.T, s Shop, id, stored string) string {
	t.Helper()
	plain, err := s.A.Data.NotifyEnv().OpenSecret(id, stored)
	if err != nil {
		t.Fatal("could not open stored notification value")
	}
	return plain
}

// notificationsSvc is the notification store, for tests that read and seed it.
func notificationsSvc(h *datahttp.DataHandler) notifyStore { return notifyStore{h.Database} }

type notifyStore struct{ db *sql.DB }

// siteAccess is the engine's ownership check for saved subscriptions.
type siteAccess struct{}

func (siteAccess) ActsOnSite(ctx context.Context, q sqlkit.Querier, siteID, person string) (bool, error) {
	return notifyapp.MayActOnSite(ctx, q, siteID, person)
}

func (s notifyStore) GetNotification(ctx context.Context, id string) (model.Notification, error) {
	return notificationsrepo.GetNotification(ctx, s.db, id)
}

func (s notifyStore) ClaimNotificationDelivery(ctx context.Context, lease time.Duration, maxAttempts, historyDays int, window time.Duration) (model.NotificationDelivery, error) {
	return notificationsrepo.ClaimNotificationDelivery(ctx, s.db, lease, maxAttempts, historyDays, window)
}

func (s notifyStore) DeleteNotification(ctx context.Context, id string) (bool, error) {
	return notificationsrepo.DeleteNotification(ctx, s.db, id)
}

func (s notifyStore) ListNotifications(ctx context.Context, owner, site string) ([]model.Notification, error) {
	return notificationsrepo.ListNotifications(ctx, s.db, owner, site)
}

func (s notifyStore) ListVisitorPushDevices(ctx context.Context, site, visitorID string) ([]model.Notification, error) {
	return notificationsrepo.ListVisitorPushDevices(ctx, s.db, site, visitorID)
}

func (s notifyStore) SaveNotification(ctx context.Context, n model.Notification, max int, cursor *int64, historyDays int, secrets model.NotificationSecrets) (bool, bool, error) {
	return notificationsrepo.SaveNotification(ctx, s.db, n, max, cursor, historyDays, secrets, siteAccess{})
}

// mailbox captures notices.
type mailbox struct {
	mu      sync.Mutex
	notices []string
}

func (m *mailbox) SendSignInCode(string, string, string) error { return nil }
func (m *mailbox) SendNotice(to, subject, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.notices = append(m.notices, to+"|"+subject)
	return nil
}
func (m *mailbox) to(address string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, s := range m.notices {
		if strings.HasPrefix(s, address+"|") {
			n++
		}
	}
	return n
}

// Web push.

// webPushFixture is notificationFixture with push on for every account and
// an "orders" personal table alice writes her own rows to.
func webPushFixture(t *testing.T) (Shop, *notificationReceiver) {
	t.Helper()
	s, x := notificationFixture(t)
	s.A.Data.SetWebPushAccounts(nil, true)
	ordersTable(t, s, RulesPersonal)
	return s, x
}

func ordersTable(t *testing.T, s Shop, rules string) {
	t.Helper()
	s.Must(t, "PUT", "/resources/orders", StorageRules("sqlite", rules), s.Key, 201)
	waitNotificationEmits(t, s)
	s.Must(t, "POST", "/sqlite/orders/schema", map[string]string{"sql": "CREATE TABLE orders(id INTEGER PRIMARY KEY, item TEXT, created_at TEXT, updated_at TEXT)"}, s.Key, 200)
	waitNotificationEmits(t, s)
}

// pushTestServer is a push endpoint answering status, counting requests.
func pushTestServer(t *testing.T, status int) (*httptest.Server, *int32) {
	t.Helper()
	var hits int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The client disables keep-alives: read the body before replying.
		_, _ = io.Copy(io.Discard, r.Body)
		atomic.AddInt32(&hits, 1)
		if status == 302 {
			w.Header().Set("Location", "https://example.invalid/elsewhere")
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// trustPush makes delivery trust the given test servers.
func trustPush(s Shop, srvs ...*httptest.Server) {
	roots := x509.NewCertPool()
	for _, srv := range srvs {
		roots.AddCert(srv.Certificate())
	}
	s.A.Data.SetNotifyClientForTest(roots)
}

// pushSubscribe subscribes caller ("owner" = the owner's key) and returns the device id.
func pushSubscribe(t *testing.T, s Shop, caller string, body map[string]any) string {
	t.Helper()
	host, h := s.Host, s.Callers[caller]
	if caller == "owner" {
		host, h = rt.SiteDomain, s.Key
	}
	r := s.A.At(t, "POST", host, "/v1/sites/shop/push/subscribe", body, h)
	if r.Status != 201 {
		t.Fatalf("subscribe %s: %d %s", caller, r.Status, r.Body)
	}
	return r.JSON(t)["id"].(string)
}

// pendingPushSubscriptions is the devices a stored event queued a push for.
func pendingPushSubscriptions(t *testing.T, s Shop) []string {
	t.Helper()
	waitNotificationEmits(t, s)
	rows, err := s.A.Database.Query(`SELECT d.subscription_id FROM site_event_deliveries d JOIN site_notifications n ON n.id=d.subscription_id WHERE n.kind='web-push' AND d.state='pending' ORDER BY d.subscription_id`)
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

func putPushRule(t *testing.T, s Shop, body map[string]any) rt.Resp {
	t.Helper()
	return s.A.At(t, "PUT", rt.SiteDomain, "/v1/sites/shop/push/rules", body, s.Key)
}
