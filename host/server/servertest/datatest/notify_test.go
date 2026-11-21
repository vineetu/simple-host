package datatest

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	accountapp "github.com/vsriram/simple-host/host/access"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/data/datahttp"
	notifyapp "github.com/vsriram/simple-host/host/data/notify"
	notificationsrepo "github.com/vsriram/simple-host/host/data/repo/notify"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/mcp"
	model "github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/server/servertest"
)

const notifyTestSecret = "whsec_MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="

type receivedNotification struct {
	header    http.Header
	body      map[string]any
	duplicate bool
}
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
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		x.mu.Lock()
		defer x.mu.Unlock()
		id, ts := r.Header.Get("webhook-id"), r.Header.Get("webhook-timestamp")
		seconds, err := strconv.ParseInt(ts, 10, 64)
		if id == "" || err != nil || time.Since(time.Unix(seconds, 0)).Abs() > 5*time.Minute {
			t.Error("missing id or stale timestamp")
		}
		// Independent Standard Webhooks verifier: decode the receiver's keys and
		// compare each advertised v1 entry with HMAC(id.timestamp.body).
		for _, secret := range x.keys {
			key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
			if err != nil {
				t.Error(err)
			}
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
		if r.Header.Get("Content-Type") != "application/json" || r.Header.Get("User-Agent") != "SimpleHost-Webhooks/1" {
			t.Error("missing webhook headers")
		}
		x.requests = append(x.requests, receivedNotification{r.Header.Clone(), payload, x.seen[id]})
		x.seen[id] = true
		if x.status == 302 {
			w.Header().Set("Location", x.server.URL+"/redirected")
		}
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
func (x *notificationReceiver) setStatus(status int) {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.status = status
}

// notificationSettings loads config.Limits with change applied, for the
// caller to build a fixture from (e.g. notificationFixture(t, ..., l)): each
// handler holds its own snapshot now, so there is no global left to apply
// it to after the fact.
func notificationSettings(t *testing.T, change func(*config.Limits)) config.Limits {
	t.Helper()
	l := config.DefaultLimits()
	change(&l)
	return l
}
func notificationFixture(t *testing.T, eds ...*edition.Edition) (servertest.StorageTestSite, *notificationReceiver) {
	t.Helper()
	isolateQueueTest(t)
	s := servertest.NewStorageTestSite(t, eds...)
	t.Cleanup(s.A.Data.WaitNotifications)
	x := newNotificationReceiver(t)
	if err := s.A.Access.SetPasscodeKey(servertest.TestPasscodeKey); err != nil {
		t.Fatal(err)
	}
	// Delivery tests drain synchronously; rate-limit tests use the real URL bucket.
	s.A.Data.Notifier().UseTestClient(x.roots(), true)
	return s, x
}
func notificationRequest(t *testing.T, s servertest.StorageTestSite, method, suffix string, body any, status int) servertest.Resp {
	t.Helper()
	r := s.A.At(t, method, servertest.PcSiteDomain, "/v1/sites/shop/notifications"+suffix, body, s.Key)
	if r.Status != status {
		t.Fatalf("%s notifications%s: status %d, want %d", method, suffix, r.Status, status)
	}
	return r
}
func subscribeNotification(t *testing.T, s servertest.StorageTestSite, x *notificationReceiver) string {
	t.Helper()
	r := notificationRequest(t, s, "POST", "", map[string]any{"url": x.server.URL, "events": []string{"record.added", "record.changed", "file.uploaded"}, "secret": notifyTestSecret, "auth_header": map[string]string{"name": "Authorization", "value": "Bearer receiver"}}, 201)
	return r.JSON(t)["notification"].(map[string]any)["id"].(string)
}
func notificationSQL(t *testing.T, s servertest.StorageTestSite, query string, args ...any) {
	t.Helper()
	waitNotificationEmits(t, s)
	if _, err := s.A.Database.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}
func waitNotificationEmits(t *testing.T, s servertest.StorageTestSite) {
	t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for s.A.Data.Notifier().PendingForTest() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("notification emit did not finish")
		}
		time.Sleep(time.Millisecond)
	}
}
func drainNotifications(t *testing.T, s servertest.StorageTestSite) {
	t.Helper()
	waitNotificationEmits(t, s)
	for i := 0; i < 100; i++ {
		if !s.A.Data.DeliverNotification(context.Background()) {
			return
		}
	}
	t.Fatal("delivery queue did not drain")
}

func TestNotifyVisitorRoutes(t *testing.T) {
	s, x := notificationFixture(t)
	id := subscribeNotification(t, s, x)
	s.Must(t, "PUT", "/resources/orders", servertest.StorageRules("sqlite", servertest.RulesPersonal), s.Key, 201)
	waitNotificationEmits(t, s)
	s.Must(t, "POST", "/sqlite/orders/schema", map[string]string{"sql": "CREATE TABLE orders(id INTEGER PRIMARY KEY, item TEXT, created_at TEXT, updated_at TEXT)"}, s.Key, 200)
	waitNotificationEmits(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	s.A.Data.StartNotifications(ctx)
	t.Cleanup(func() { cancel(); s.A.Data.WaitNotifications() })
	r := s.Must(t, "POST", "/sqlite/orders/tables/orders/rows", map[string]string{"item": "Chocolate cake"}, s.Callers["alice"], 200)
	waitNotificationEmits(t, s)
	row := fmt.Sprint(r.JSON(t)["last_insert_id"])
	deadline := time.Now().Add(5 * time.Second)
	for len(x.snapshot()) < 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	got := x.snapshot()
	if len(got) != 1 {
		t.Fatal("worker did not deliver")
	}
	event := got[0].body
	data := event["data"].(map[string]any)
	if event["name"] != "record.added" || data["visitor"] != s.AliceID || data["table"] != "orders" || !strings.HasPrefix(data["site_url"].(string), "https://shop.") {
		t.Fatalf("event data: %v", data)
	}
	fields := data["fields"].(map[string]any)
	if fields["item"] != "Chocolate cake" || fields["visitor_id"] != nil || fields["created_at"] == nil {
		t.Fatal("incorrect fields")
	}
	s.Must(t, "PATCH", "/sqlite/orders/tables/orders/rows/"+row, map[string]string{"item": "Vanilla"}, s.Callers["alice"], 200)
	waitNotificationEmits(t, s)
	s.Must(t, "DELETE", "/sqlite/orders/tables/orders/rows/"+row, nil, s.Callers["alice"], 200)
	waitNotificationEmits(t, s)
	s.Must(t, "POST", "/sqlite/orders/tables/orders/rows", map[string]string{"item": "Owner tools"}, s.Key, 200)
	waitNotificationEmits(t, s)
	s.Must(t, "POST", "/sqlite/orders/tables/orders/rows", map[string]string{"item": "Owner on site"}, s.Callers["owner-site"], 200)
	waitNotificationEmits(t, s)
	s.Must(t, "POST", "/sqlite/orders/tables/orders/rows", map[string]string{"missing_column": "failed"}, s.Callers["alice"], 400)
	waitNotificationEmits(t, s)
	for _, kind := range []string{"kv", "files"} {
		s.Must(t, "PUT", "/resources/"+kind, servertest.StorageRules(kind, servertest.RulesPersonal), s.Key, 201)
		waitNotificationEmits(t, s)
	}
	s.Must(t, "PUT", "/kv/kv/keys/order", `{"value":{"item":"bread"}}`, s.Callers["alice"], 200)
	waitNotificationEmits(t, s)
	s.Must(t, "PUT", "/kv/kv/keys/order", `{"value":"changed"}`, s.Callers["alice"], 200)
	waitNotificationEmits(t, s)
	s.Must(t, "DELETE", "/kv/kv/keys/order", nil, s.Callers["alice"], 200)
	waitNotificationEmits(t, s)
	s.Must(t, "PUT", "/files/files/objects/photo.txt", "photo bytes", s.Callers["alice"], 200)
	waitNotificationEmits(t, s)
	deadline = time.Now().Add(5 * time.Second)
	for len(x.snapshot()) < 8 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	got = x.snapshot()
	if len(got) != 8 {
		t.Fatalf("want 8 visitor events, got %d", len(got))
	}
	names := map[string]int{}
	for _, r := range got {
		names[r.body["name"].(string)]++
		d := r.body["data"].(map[string]any)
		if d["change"] == "deleted" && d["fields"] != nil {
			t.Fatal("deleted fields")
		}
		if r.body["name"] == "file.uploaded" && (d["size"] != float64(11) || d["fields"] != nil) {
			t.Fatal("file fields")
		}
		// No event carries a preset any more.
		if _, ok := d["preset"]; ok {
			t.Fatalf("%s carries preset", r.body["name"])
		}
	}
	if names["record.added"] != 3 || names["record.changed"] != 4 || names["file.uploaded"] != 1 {
		t.Fatal(names)
	}
	list := notificationRequest(t, s, "GET", "", nil, 200)
	if strings.Contains(string(list.Body), "whsec_") || strings.Contains(string(list.Body), "Bearer receiver") {
		t.Fatal("subscription list leaked credentials")
	}
	history := notificationRequest(t, s, "GET", "/"+id+"/deliveries", nil, 200).JSON(t)["deliveries"].([]any)
	if len(history) != 8 {
		t.Fatalf("history count %d", len(history))
	}
}

func TestNotifyRetriesSuspensionAndResume(t *testing.T) {
	l := notificationSettings(t, func(l *config.Limits) { l.NotifySuspendAttempts = 3 })
	s, x := notificationFixture(t, edition.Default(l))
	id := subscribeNotification(t, s, x)
	mail := &servertest.Mailbox{}
	s.A.Sites.Mailer = mail
	s.A.Access.Mailer, s.A.Data.Mailer = s.A.Sites.Mailer, s.A.Sites.Mailer
	x.setStatus(500)
	s.Must(t, "PUT", "/resources/kv", servertest.StorageRules("kv", servertest.RulesPersonal), s.Key, 201)
	waitNotificationEmits(t, s)
	s.Must(t, "PUT", "/kv/kv/keys/one", `{"value":1}`, s.Callers["alice"], 200)
	waitNotificationEmits(t, s)
	for i := 0; i < 3; i++ {
		notificationSQL(t, s, `UPDATE site_event_deliveries SET next_attempt_at=now() WHERE subscription_id=$1`, id)
		drainNotifications(t, s)
	}
	n, err := notificationsSvc(s.A.Data).GetNotification(context.Background(), id)
	if err != nil || n.State != "suspended" {
		t.Fatalf("not suspended: %s %v", n.State, err)
	}
	got := x.snapshot()
	if len(got) != 3 || !got[1].duplicate || !got[2].duplicate {
		t.Fatal("retry id dedup failed")
	}
	s.A.Data.Notifier().NotifySuspension(context.Background(), s.A.Data.NotifyEnv().Mailer, n)
	if len(mail.To(s.Owner.Email)) != 1 {
		t.Fatal("suspension must send one notice")
	}
	drainNotifications(t, s)
	if len(x.snapshot()) != 3 {
		t.Fatal("suspended attempt sent")
	}
	x.setStatus(200)
	notificationRequest(t, s, "PATCH", "/"+id, map[string]bool{"paused": false}, 200)
	drainNotifications(t, s)
	n, err = notificationsSvc(s.A.Data).GetNotification(context.Background(), id)
	if err != nil || n.State != "active" || n.LastSuccessAt == nil {
		t.Fatal("resume did not deliver")
	}
	for _, status := range []int{410, 413} {
		x.setStatus(status)
		s.Must(t, "PUT", "/kv/kv/keys/"+strconv.Itoa(status), `{"value":1}`, s.Callers["alice"], 200)
		waitNotificationEmits(t, s)
		drainNotifications(t, s)
		var state string
		var attempts int
		if err = s.A.Database.QueryRow(`SELECT state,attempts FROM site_event_deliveries WHERE subscription_id=$1 ORDER BY event_seq DESC LIMIT 1`, id).Scan(&state, &attempts); err != nil || state != "failed" || attempts != 1 {
			t.Fatalf("%d: %s %d %v", status, state, attempts, err)
		}
	}
}

func TestNotifyCapsTruncationRotationAndControls(t *testing.T) {
	l := notificationSettings(t, func(l *config.Limits) { l.NotifyMaxPerSite = 1; l.NotifyPerMinute = 2 })
	s, x := notificationFixture(t, edition.Default(l))
	id := subscribeNotification(t, s, x)
	notificationRequest(t, s, "POST", "", map[string]any{"url": x.server.URL, "events": []string{"record.added"}}, 409)
	s.Must(t, "PUT", "/resources/kv", servertest.StorageRules("kv", servertest.RulesPersonal), s.Key, 201)
	waitNotificationEmits(t, s)
	s.Must(t, "PUT", "/kv/kv/keys/long", map[string]any{"value": map[string]string{"long": strings.Repeat("é", 2000)}}, s.Callers["alice"], 200)
	waitNotificationEmits(t, s)
	many := map[string]string{}
	for i := 0; i < 30; i++ {
		many[fmt.Sprint(i)] = strings.Repeat("x", 1000)
	}
	s.Must(t, "PUT", "/kv/kv/keys/big", map[string]any{"value": many}, s.Callers["alice"], 200)
	waitNotificationEmits(t, s)
	s.Must(t, "PUT", "/kv/kv/keys/capped", `{"value":1}`, s.Callers["alice"], 200)
	waitNotificationEmits(t, s)
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
	x.mu.Lock()
	x.keys = append(x.keys, rotated)
	x.mu.Unlock()
	notificationRequest(t, s, "POST", "/"+id+"/test", nil, 200)
	if len(strings.Fields(x.snapshot()[2].header.Get("webhook-signature"))) != 2 {
		t.Fatal("rotation needs both signatures")
	}
	notificationRequest(t, s, "PATCH", "/"+id, map[string]bool{"paused": true}, 200)
	notificationRequest(t, s, "DELETE", "/"+id, nil, 200)
	notificationRequest(t, s, "GET", "/"+id+"/deliveries", nil, 404)
}

func TestNotifyMCPChallengeRefreshReplay(t *testing.T) {
	s, x := notificationFixture(t)
	backend := s.A.Data.EventsBackend()
	caller := mcp.Caller{Owner: s.OwnerID, AppName: "Test app"}
	req := mcp.EventSubscribeRequest{Name: "record.added", Site: "shop", CanonicalArgs: `{"site":"shop"}`, URL: x.server.URL, Secret: notifyTestSecret}
	result, err := backend.Subscribe(context.Background(), caller, req)
	if err != nil {
		t.Fatal(err)
	}
	if result.Refreshed || !result.Active || result.RefreshBefore == nil {
		t.Fatal("initial subscribe")
	}
	got := x.snapshot()
	if len(got) != 1 || got[0].body["type"] != "verification" || len(got[0].body["challenge"].(string)) != 32 {
		t.Fatal("missing challenge")
	}
	refresh, err := backend.Subscribe(context.Background(), caller, req)
	if err != nil || refresh.ID != result.ID || !refresh.Refreshed || len(x.snapshot()) != 1 {
		t.Fatal("refresh was not idempotent")
	}
	notificationRequest(t, s, "POST", "/"+result.ID+"/rotate-secret", nil, 409)
	s.Must(t, "PUT", "/resources/kv", servertest.StorageRules("kv", servertest.RulesPersonal), s.Key, 201)
	waitNotificationEmits(t, s)
	s.Must(t, "PUT", "/kv/kv/keys/one", `{"value":1}`, s.Callers["alice"], 200)
	waitNotificationEmits(t, s)
	var seq int64
	if err = s.A.Database.QueryRow(`SELECT max(seq) FROM site_events WHERE site_id=$1`, s.Sid).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	notificationRequest(t, s, "DELETE", "/"+result.ID, nil, 200)
	cursor := "c_0"
	req.Cursor = &cursor
	req.TTLSet = true
	replay, err := backend.Subscribe(context.Background(), caller, req)
	if err != nil || !replay.Truncated || replay.RefreshBefore == nil {
		t.Fatalf("replay: %+v %v", replay, err)
	}
	safe, err := notificationsrepo.NotificationCursor(context.Background(), s.A.Database, replay.ID, s.Sid)
	if err != nil || safe >= seq {
		t.Fatal("cursor passed a pending delivery")
	}
	drainNotifications(t, s)
	safe, err = notificationsrepo.NotificationCursor(context.Background(), s.A.Database, replay.ID, s.Sid)
	if err != nil || safe != seq {
		t.Fatal("cursor did not advance")
	}
	got = x.snapshot()
	last := got[len(got)-1]
	if last.body["name"] != "record.added" || last.header.Get("svix-id") != "" || last.header.Get("Authorization") != "" {
		t.Fatal("MCP event headers")
	}
	// A different endpoint has no cached verification and must echo the challenge.
	x.mu.Lock()
	x.echo = false
	x.mu.Unlock()
	bad := req
	bad.URL = x.server.URL + "/no-echo"
	_, err = backend.Subscribe(context.Background(), caller, bad)
	var ee *mcp.EventError
	if !errors.As(err, &ee) || ee.Code != mcp.EventCodeCallbackEndpoint || ee.Data["reason"] != "challenge_failed" {
		t.Fatalf("missing echo: %v", err)
	}
	_, err = backend.Subscribe(context.Background(), mcp.Caller{Owner: s.AliceID}, req)
	if !errors.As(err, &ee) || ee.Code != mcp.EventCodeForbidden {
		t.Fatal("other owner accepted")
	}
	unsub := mcp.EventUnsubscribeRequest{Name: req.Name, Site: req.Site, CanonicalArgs: req.CanonicalArgs, URL: req.URL}
	if err = backend.Unsubscribe(context.Background(), caller, unsub); err != nil {
		t.Fatal(err)
	}
	// Idempotent: a second unsubscribe of the same key answers as removed.
	if err = backend.Unsubscribe(context.Background(), caller, unsub); err != nil {
		t.Fatalf("repeat unsubscribe: %v", err)
	}
}

func TestNotifyLeaseCursorExpiryAndSweep(t *testing.T) {
	s, x := notificationFixture(t)
	id := subscribeNotification(t, s, x)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := notificationsrepo.EmitSiteEvent(ctx, s.A.Database, s.Sid, notifyapp.EventID(), "record.added", "orders", []byte(`{"v":1}`)); err != nil {
			t.Fatal(err)
		}
	}
	// Concurrent claimers must get different rows, and a third must find none.
	claims := make(chan model.NotificationDelivery, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			d, err := notificationsrepo.ClaimNotificationDelivery(ctx, s.A.Database, time.Minute, 5, 7, time.Hour)
			claims <- d
			errs <- err
		}()
	}
	a, b := <-claims, <-claims
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if a.Seq == b.Seq {
		t.Fatal("two workers claimed the same delivery")
	}
	if a.Seq > b.Seq {
		a, b = b, a
	}
	if _, err := notificationsrepo.ClaimNotificationDelivery(ctx, s.A.Database, time.Minute, 5, 7, time.Hour); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("lease did not hold delivery")
	}
	finish := func(d model.NotificationDelivery) {
		t.Helper()
		_, err := notificationsrepo.FinishNotificationDelivery(ctx, s.A.Database, d, "delivered", "ok", 200, 1, time.Now(), time.Hour, 100, 95, 72*time.Hour)
		if err != nil {
			t.Fatal(err)
		}
	}
	finish(b)
	cursor, err := notificationsrepo.NotificationCursor(ctx, s.A.Database, id, s.Sid)
	if err != nil || cursor >= a.Seq {
		t.Fatal("cursor passed unresolved earlier event")
	}
	notificationSQL(t, s, `UPDATE site_event_deliveries SET lease_until=now()-interval '1 second' WHERE event_seq=$1`, a.Seq)
	newer, err := notificationsrepo.ClaimNotificationDelivery(ctx, s.A.Database, time.Minute, 5, 7, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	finish(a)
	var state string
	if err = s.A.Database.QueryRow(`SELECT state FROM site_event_deliveries WHERE event_seq=$1`, a.Seq).Scan(&state); err != nil || state != "pending" {
		t.Fatal("stale worker overwrote newer lease")
	}
	finish(newer)
	cursor, err = notificationsrepo.NotificationCursor(ctx, s.A.Database, id, s.Sid)
	if err != nil || cursor != b.Seq {
		t.Fatal("watermark did not advance")
	}
	if err = notificationsrepo.EmitSiteEvent(ctx, s.A.Database, s.Sid, notifyapp.EventID(), "record.added", "orders", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	notificationSQL(t, s, `UPDATE site_notifications SET expires_at=now()-interval '1 minute' WHERE id=$1`, id)
	drainNotifications(t, s)
	if len(x.snapshot()) != 0 {
		t.Fatal("expired subscription delivered")
	}
	if n := notificationRequest(t, s, "GET", "", nil, 200).JSON(t)["notifications"].([]any)[0].(map[string]any); n["state"] != "expired" {
		t.Fatal("expiry not shown")
	}
	notificationSQL(t, s, `UPDATE site_events SET created_at=now()-interval '8 days' WHERE site_id=$1`, s.Sid)
	notificationSQL(t, s, `UPDATE site_notifications SET expires_at=now()-interval '8 days' WHERE id=$1`, id)
	if err = notificationsrepo.SweepNotifications(ctx, s.A.Database, 7); err != nil {
		t.Fatal(err)
	}
	var events, deliveries int
	if err = s.A.Database.QueryRow(`SELECT (SELECT count(*) FROM site_events WHERE site_id=$1),(SELECT count(*) FROM site_event_deliveries WHERE subscription_id=$2)`, s.Sid, id).Scan(&events, &deliveries); err != nil || events != 0 || deliveries != 0 {
		t.Fatal("history sweep did not cascade")
	}
	if _, err = notificationsSvc(s.A.Data).GetNotification(ctx, id); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("expired subscription not swept")
	}
}

func TestNotifyUnavailableHeldAndEventAccounts(t *testing.T) {
	s, x := notificationFixture(t)
	id := subscribeNotification(t, s, x)
	ctx := context.Background()
	emit := func() {
		t.Helper()
		if err := notificationsrepo.EmitSiteEvent(ctx, s.A.Database, s.Sid, notifyapp.EventID(), "record.added", "orders", []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
	for _, column := range []string{"offline_at", "suspended_at", "deleted_at", "owner", "transfer"} {
		emit()
		if column == "transfer" {
			notificationSQL(t, s, `UPDATE sites SET user_id=$2 WHERE id=$1`, s.Sid, s.AliceID)
		} else if column == "owner" {
			notificationSQL(t, s, `UPDATE users SET suspended_at=now() WHERE id=$1`, s.OwnerID)
		} else {
			notificationSQL(t, s, `UPDATE sites SET `+column+`=now() WHERE id=$1`, s.Sid)
		}
		drainNotifications(t, s)
		var state, category string
		if err := s.A.Database.QueryRow(`SELECT state,last_category FROM site_event_deliveries WHERE subscription_id=$1 ORDER BY event_seq DESC LIMIT 1`, id).Scan(&state, &category); err != nil || state != "skipped" || category != "site_unavailable" {
			t.Fatalf("%s: %s %s %v", column, state, category, err)
		}
		if column == "transfer" {
			notificationSQL(t, s, `UPDATE sites SET user_id=$2 WHERE id=$1`, s.Sid, s.OwnerID)
		} else if column == "owner" {
			notificationSQL(t, s, `UPDATE users SET suspended_at=NULL WHERE id=$1`, s.OwnerID)
		} else {
			notificationSQL(t, s, `UPDATE sites SET `+column+`=NULL WHERE id=$1`, s.Sid)
		}
	}
	if len(x.snapshot()) != 0 {
		t.Fatal("unavailable site sent an event")
	}
	emit()
	notificationRequest(t, s, "PATCH", "/"+id, map[string]bool{"paused": true}, 200)
	emit()
	drainNotifications(t, s)
	if len(x.snapshot()) != 0 {
		t.Fatal("paused sent")
	}
	notificationRequest(t, s, "PATCH", "/"+id, map[string]bool{"paused": false}, 200)
	drainNotifications(t, s)
	if len(x.snapshot()) != 1 {
		t.Fatal("held event lost")
	}
	notificationSQL(t, s, `UPDATE users SET event_account=true WHERE id=$1`, s.OwnerID)
	c := datahttp.StorageCall{SiteID: s.Sid, OwnerID: s.OwnerID, SiteName: "shop", ResourceName: "orders", Resource: datahttp.StorageResource{Kind: "kv"}}
	s.A.Data.EmitNotification(c, "record.added", map[string]any{"key": "event-website"})
	drainNotifications(t, s)
	if len(x.snapshot()) != 1 {
		t.Fatal("event website emitted")
	}
	notificationRequest(t, s, "GET", "", nil, 404)
}

func TestNotifyWindowAndContinuousFailure(t *testing.T) {
	l := notificationSettings(t, func(l *config.Limits) { l.NotifySuspendAttempts = 3 })
	s, x := notificationFixture(t, edition.Default(l))
	id := subscribeNotification(t, s, x)
	ctx := context.Background()
	x.setStatus(500)
	// Two old failures must age out of the counting window.
	notificationSQL(t, s, `UPDATE site_notifications SET window_start=now()-interval '2 hours',window_attempts=2,window_failures=2 WHERE id=$1`, id)
	if err := notificationsrepo.EmitSiteEvent(ctx, s.A.Database, s.Sid, notifyapp.EventID(), "record.added", "orders", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	drainNotifications(t, s)
	n, err := notificationsSvc(s.A.Data).GetNotification(ctx, id)
	if err != nil || n.State != "active" {
		t.Fatal("expired failures caused suspension")
	}
	var attempts int
	if err = s.A.Database.QueryRow(`SELECT window_attempts FROM site_notifications WHERE id=$1`, id).Scan(&attempts); err != nil || attempts != 1 {
		t.Fatal("expired window not reset")
	}
	notificationSQL(t, s, `UPDATE site_notifications SET failing_since=now()-interval '4 days' WHERE id=$1`, id)
	notificationSQL(t, s, `UPDATE site_event_deliveries SET next_attempt_at=now() WHERE subscription_id=$1`, id)
	drainNotifications(t, s)
	n, err = notificationsSvc(s.A.Data).GetNotification(ctx, id)
	if err != nil || n.State != "suspended" {
		t.Fatal("continuous failure did not suspend")
	}
}

func TestNotifyDailyCapAndNoSubscribers(t *testing.T) {
	l := notificationSettings(t, func(l *config.Limits) { l.NotifyPerDay = 1 })
	s, x := notificationFixture(t, edition.Default(l))
	s.Must(t, "PUT", "/resources/kv", servertest.StorageRules("kv", servertest.RulesPersonal), s.Key, 201)
	waitNotificationEmits(t, s)
	s.Must(t, "PUT", "/kv/kv/keys/no-subscriber", `{"value":1}`, s.Callers["alice"], 200)
	waitNotificationEmits(t, s)
	var count int
	if err := s.A.Database.QueryRow(`SELECT count(*) FROM site_events WHERE site_id=$1`, s.Sid).Scan(&count); err != nil || count != 0 {
		t.Fatal("stored event without subscriptions")
	}
	subscribeNotification(t, s, x)
	for _, key := range []string{"one", "two"} {
		s.Must(t, "PUT", "/kv/kv/keys/"+key, `{"value":1}`, s.Callers["alice"], 200)
		waitNotificationEmits(t, s)
	}
	drainNotifications(t, s)
	if len(x.snapshot()) != 1 {
		t.Fatal("daily cap")
	}
}

func TestNotifyRESTOwnershipAndValidation(t *testing.T) {
	s, x := notificationFixture(t)
	id := subscribeNotification(t, s, x)
	fullURL := x.server.URL + "/incoming/credential?token=private"
	notificationSQL(t, s, `UPDATE site_notifications SET url=$2 WHERE id=$1`, id, fullURL)
	listed := notificationRequest(t, s, "GET", "", nil, 200).JSON(t)["notifications"].([]any)
	if len(listed) != 1 || listed[0].(map[string]any)["url"] != fullURL {
		t.Fatal("REST list did not preserve the full webhook URL")
	}
	other := s.A.NewPerson(t, "notification-other")
	r := s.A.At(t, "DELETE", servertest.PcSiteDomain, "/v1/sites/shop/notifications/"+id, nil, map[string]string{"X-API-Key": other.Key})
	if r.Status != 404 {
		t.Fatal("other account could address subscription")
	}
	r = s.A.At(t, "GET", servertest.PcSiteDomain, "/v1/notifications", nil, map[string]string{"X-API-Key": other.Key})
	if r.Status != 200 || len(r.JSON(t)["notifications"].([]any)) != 0 {
		t.Fatal("account list leak")
	}
	for _, tc := range []struct {
		body map[string]any
		code string
	}{
		{map[string]any{"url": "http://example.com", "events": []string{"record.added"}}, "invalid_url"},
		{map[string]any{"url": x.server.URL, "events": []string{"other"}}, "invalid_events"},
		{map[string]any{"url": x.server.URL, "events": []string{"record.added"}, "secret": "bad"}, "invalid_secret"},
		{map[string]any{"url": x.server.URL, "events": []string{"record.added"}, "auth_header": map[string]string{"name": "Host", "value": "bad"}}, "invalid_header"},
	} {
		r := notificationRequest(t, s, "POST", "", tc.body, 400)
		if r.JSON(t)["code"] != tc.code {
			t.Fatal("wrong validation code")
		}
	}
	notificationRequest(t, s, "POST", "", map[string]any{"url": x.server.URL, "events": []string{"record.added"}, "storage": "missing"}, 404)
}

func TestNotifyMCPRotationTTLAndChallengeLimit(t *testing.T) {
	s, x := notificationFixture(t)
	backend := s.A.Data.EventsBackend()
	caller := mcp.Caller{Owner: s.OwnerID}
	ctx := context.Background()
	short := time.Second
	req := mcp.EventSubscribeRequest{Name: "record.added", Site: "shop", CanonicalArgs: `{"site":"shop"}`, URL: x.server.URL, Secret: notifyTestSecret, TTLSet: true, TTL: &short}
	first, err := backend.Subscribe(ctx, caller, req)
	if err != nil {
		t.Fatal(err)
	}
	if first.RefreshBefore == nil || time.Until(*first.RefreshBefore) < 4*time.Minute {
		t.Fatal("minimum TTL")
	}
	secret, err := notifyapp.NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	req.Secret = secret
	refresh, err := backend.Subscribe(ctx, caller, req)
	if err != nil || refresh.ID != first.ID {
		t.Fatal("rotating refresh")
	}
	x.mu.Lock()
	x.keys = append(x.keys, secret)
	x.mu.Unlock()
	notificationRequest(t, s, "POST", "/"+first.ID+"/test", nil, 409)
	n, err := notificationsSvc(s.A.Data).GetNotification(ctx, first.ID)
	if err != nil || openedNotification(t, s, n.ID, n.PrevSecret) != notifyTestSecret || openedNotification(t, s, n.ID, n.Secret) != secret {
		t.Fatal("MCP refresh did not rotate secret")
	}
	x.mu.Lock()
	x.keys = []string{notifyTestSecret}
	x.echo = false
	x.mu.Unlock()
	req.Secret = notifyTestSecret
	var ee *mcp.EventError
	for i := 0; i < 9; i++ {
		req.URL = x.server.URL + fmt.Sprintf("/fail-%d", i)
		_, err = backend.Subscribe(ctx, caller, req)
		if !errors.As(err, &ee) || ee.Data["reason"] != "challenge_failed" {
			t.Fatalf("challenge %d: %v", i, err)
		}
	}
	req.URL = x.server.URL + "/limited"
	_, err = backend.Subscribe(ctx, caller, req)
	if !errors.As(err, &ee) || ee.Code != mcp.EventCodeResourceExhausted {
		t.Fatal("host challenge cap")
	}
}

func TestNotifyRefusedAddressAndPermanentHTTPDoNotRetry(t *testing.T) {
	l := notificationSettings(t, func(l *config.Limits) { l.NotifySuspendAttempts = 1 })
	s, x := notificationFixture(t, edition.Default(l))
	id := subscribeNotification(t, s, x)
	ctx := context.Background()
	emit := func() {
		t.Helper()
		if err := notificationsrepo.EmitSiteEvent(ctx, s.A.Database, s.Sid, notifyapp.EventID(), "record.added", "orders", []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
	for _, status := range []int{410, 413} {
		x.setStatus(status)
		emit()
		drainNotifications(t, s)
		n, err := notificationsSvc(s.A.Data).GetNotification(ctx, id)
		if err != nil || n.State != "active" {
			t.Fatal("permanent HTTP result changed subscription")
		}
	}
	s.A.Data.Notifier().UseTestClient(x.roots(), false)
	emit()
	drainNotifications(t, s)
	var state, category string
	var attempts int
	if err := s.A.Database.QueryRow(`SELECT state,last_category,attempts FROM site_event_deliveries WHERE subscription_id=$1 ORDER BY event_seq DESC LIMIT 1`, id).Scan(&state, &category, &attempts); err != nil || state != "failed" || category != "refused_address" || attempts != 1 {
		t.Fatal("refused address should abandon after one attempt")
	}
	if len(x.snapshot()) != 2 {
		t.Fatal("refused callback was contacted")
	}
}

func TestNotifyConcurrentSubscriptionCapAndFilters(t *testing.T) {
	l := notificationSettings(t, func(l *config.Limits) { l.NotifyMaxPerSite = 1 })
	s, x := notificationFixture(t, edition.Default(l))
	ctx := context.Background()
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, _, err := notifyapp.New(s.A.Database).SaveNotification(ctx, model.Notification{ID: notifyapp.RandomID("sub_", 12), SiteID: s.Sid, OwnerID: s.OwnerID, Source: "owner", URL: x.server.URL, Events: []string{"record.added"}, Storage: "orders", Secret: notifyTestSecret}, 1, nil, 7, notificationSecrets(s.A.Data))
			errs <- err
		}()
	}
	accepted, refused := 0, 0
	for i := 0; i < 2; i++ {
		err := <-errs
		if err == nil {
			accepted++
		} else if errors.Is(err, model.ErrNotificationCap) {
			refused++
		} else {
			t.Fatal(err)
		}
	}
	if accepted != 1 || refused != 1 {
		t.Fatal("concurrent cap exceeded")
	}
	for _, event := range []struct{ name, storage string }{{"record.added", "other"}, {"record.changed", "orders"}, {"record.added", "orders"}} {
		if err := notificationsrepo.EmitSiteEvent(ctx, s.A.Database, s.Sid, notifyapp.EventID(), event.name, event.storage, []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
	drainNotifications(t, s)
	if len(x.snapshot()) != 1 {
		t.Fatal("event and storage filters")
	}
}

func TestNotifyRefreshPreservesLeaseAndBackoff(t *testing.T) {
	s, x := notificationFixture(t)
	id := subscribeNotification(t, s, x)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := notificationsrepo.EmitSiteEvent(ctx, s.A.Database, s.Sid, notifyapp.EventID(), "record.added", "", []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
	d, err := notificationsrepo.ClaimNotificationDelivery(ctx, s.A.Database, time.Minute, 5, 7, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	notificationSQL(t, s, `UPDATE site_event_deliveries SET next_attempt_at=now()+interval '1 hour' WHERE subscription_id=$1`, id)
	n, err := notificationsSvc(s.A.Data).GetNotification(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	n.Secret = notifyTestSecret
	if _, _, err = notifyapp.New(s.A.Database).SaveNotification(ctx, n, 10, nil, 7, notificationSecrets(s.A.Data)); err != nil {
		t.Fatal(err)
	}
	var held, deferred bool
	if err = s.A.Database.QueryRow(`SELECT lease_until=$2,next_attempt_at>now() FROM site_event_deliveries WHERE event_seq=$1`, d.Seq, d.LeaseUntil).Scan(&held, &deferred); err != nil || !held || !deferred {
		t.Fatal("active refresh changed live lease or backoff")
	}
	notificationRequest(t, s, "PATCH", "/"+id, map[string]bool{"paused": true}, 200)
	notificationRequest(t, s, "PATCH", "/"+id, map[string]bool{"paused": false}, 200)
	if err = s.A.Database.QueryRow(`SELECT lease_until=$2,next_attempt_at>now() FROM site_event_deliveries WHERE event_seq=$1`, d.Seq, d.LeaseUntil).Scan(&held, &deferred); err != nil || !held || !deferred {
		t.Fatal("resume changed live lease")
	}
	var due bool
	if err = s.A.Database.QueryRow(`SELECT next_attempt_at<=now() FROM site_event_deliveries WHERE subscription_id=$1 AND event_seq<>$2`, id, d.Seq).Scan(&due); err != nil || !due {
		t.Fatal("resume did not release held unleased delivery")
	}
}

func TestNotifyCrashBudgetAndStaleWorker(t *testing.T) {
	s, x := notificationFixture(t)
	id := subscribeNotification(t, s, x)
	ctx := context.Background()
	if err := notificationsrepo.EmitSiteEvent(ctx, s.A.Database, s.Sid, notifyapp.EventID(), "record.added", "", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	var first model.NotificationDelivery
	for i := 1; i <= 2; i++ {
		d, err := notificationsrepo.ClaimNotificationDelivery(ctx, s.A.Database, time.Minute, 2, 7, time.Hour)
		if err != nil || d.Attempts != i {
			t.Fatalf("claim %d: attempts %d, %v", i, d.Attempts, err)
		}
		if i == 1 {
			first = d
		}
		notificationSQL(t, s, `UPDATE site_event_deliveries SET lease_until=now()-interval '1 second' WHERE subscription_id=$1`, id)
	}
	s.A.Data.Notifier().Process(ctx, s.A.Data.NotifyEnv(), first)
	if len(x.snapshot()) != 0 {
		t.Fatal("worker sent without its lease")
	}
	first.LeaseUntil = time.Now().Add(-time.Second)
	s.A.Data.Notifier().Process(ctx, s.A.Data.NotifyEnv(), first)
	if len(x.snapshot()) != 0 {
		t.Fatal("worker sent after lease deadline")
	}
	if _, err := notificationsrepo.ClaimNotificationDelivery(ctx, s.A.Database, time.Minute, 2, 7, time.Hour); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("crashes exceeded send budget")
	}
	var attempts, failures int
	var state string
	if err := s.A.Database.QueryRow(`SELECT d.state,n.window_attempts,n.window_failures FROM site_notifications n JOIN site_event_deliveries d ON d.subscription_id=n.id WHERE n.id=$1`, id).Scan(&state, &attempts, &failures); err != nil || state != "failed" || attempts != 2 || failures != 2 {
		t.Fatal("abandoned claims were not accounted")
	}
}

func TestNotifyHistoryAndDeletedListings(t *testing.T) {
	s, x := notificationFixture(t)
	id := subscribeNotification(t, s, x)
	ctx := context.Background()
	if err := notificationsrepo.EmitSiteEvent(ctx, s.A.Database, s.Sid, notifyapp.EventID(), "record.added", "", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	notificationSQL(t, s, `UPDATE site_events SET created_at=now()-interval '8 days' WHERE site_id=$1`, s.Sid)
	if _, err := notificationsrepo.ClaimNotificationDelivery(ctx, s.A.Database, time.Minute, 5, 7, time.Hour); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("claimed expired history")
	}
	notificationSQL(t, s, `DELETE FROM site_event_deliveries WHERE subscription_id=$1`, id)
	n, err := notificationsSvc(s.A.Data).GetNotification(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	n.Secret = notifyTestSecret
	if _, _, err := notifyapp.New(s.A.Database).SaveNotification(ctx, n, 10, &zero, 7, notificationSecrets(s.A.Data)); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.A.Database.QueryRow(`SELECT count(*) FROM site_event_deliveries WHERE subscription_id=$1`, id).Scan(&count); err != nil || count != 0 {
		t.Fatal("replayed expired history")
	}
	run, cancel := context.WithCancel(ctx)
	s.A.Data.StartNotifications(run)
	cancel()
	s.A.Data.WaitNotifications()
	if err := s.A.Database.QueryRow(`SELECT count(*) FROM site_events WHERE site_id=$1`, s.Sid).Scan(&count); err != nil || count != 0 {
		t.Fatal("startup did not sweep history")
	}
	notificationSQL(t, s, `UPDATE sites SET deleted_at=now() WHERE id=$1`, s.Sid)
	for _, site := range []string{"", s.Sid} {
		ns, err := notificationsSvc(s.A.Data).ListNotifications(ctx, s.OwnerID, site)
		if err != nil || len(ns) != 0 {
			t.Fatal("listed deleted site's notifications")
		}
	}
}

func TestNotifyMCPValidationAndSiteReplacement(t *testing.T) {
	s, x := notificationFixture(t)
	ctx := context.Background()
	b := s.A.Data.EventsBackend()
	c := mcp.Caller{Owner: s.OwnerID}
	req := mcp.EventSubscribeRequest{Name: "record.added", Site: "shop", CanonicalArgs: `{"site":"shop"}`, URL: x.server.URL, Secret: notifyTestSecret}
	for _, raw := range []string{"http://example.com/", "https://user@example.com/", "https://example.com/#frag", "://bad"} {
		bad := req
		bad.URL = raw
		_, err := b.Subscribe(ctx, c, bad)
		var ee *mcp.EventError
		if !errors.As(err, &ee) || ee.Code != -32602 {
			t.Fatalf("static URL code: %v", err)
		}
	}
	x.mu.Lock()
	x.reflectBody = true
	x.mu.Unlock()
	_, err := b.Subscribe(ctx, c, req)
	var ee *mcp.EventError
	if !errors.As(err, &ee) || ee.Code != -32015 || ee.Data["reason"] != "challenge_failed" {
		t.Fatal("reflection passed verification")
	}
	x.mu.Lock()
	x.reflectBody = false
	x.mu.Unlock()
	first, err := b.Subscribe(ctx, c, req)
	if err != nil {
		t.Fatal(err)
	}
	s.A.Deploy(t, s.Owner, "replacement")
	replacement := s.A.SiteID(t, s.Owner, "replacement")
	notificationSQL(t, s, `UPDATE sites SET name='oldshop' WHERE id=$1`, s.Sid)
	notificationSQL(t, s, `UPDATE sites SET name='shop' WHERE id=$1`, replacement)
	count := len(x.snapshot())
	next, err := b.Subscribe(ctx, c, req)
	if err != nil || next.Refreshed || next.ID != first.ID || len(x.snapshot()) != count+1 {
		t.Fatalf("new site did not require fresh verification: %v", err)
	}
	n, err := notificationsSvc(s.A.Data).GetNotification(ctx, next.ID)
	if err != nil || n.SiteID != replacement {
		t.Fatal("subscription remained on old site")
	}
	n.SiteID = s.Sid
	n.Secret = notifyTestSecret
	if _, _, err = notifyapp.New(s.A.Database).SaveNotification(ctx, n, 10, nil, 7, notificationSecrets(s.A.Data)); !errors.Is(err, model.ErrNotificationSite) {
		t.Fatal("upsert crossed sites")
	}
}

func TestNotifyChallengeBusy(t *testing.T) {
	ctx := context.Background()
	s, x := notificationFixture(t)
	id := subscribeNotification(t, s, x)
	release := s.A.Data.Notifier().HoldVerifySlotsForTest()
	r := notificationRequest(t, s, "POST", "/"+id+"/test", nil, 503)
	if r.JSON(t)["code"] != "busy" {
		t.Fatal("busy test category")
	}
	_, err := s.A.Data.EventsBackend().Subscribe(ctx, mcp.Caller{Owner: s.OwnerID}, mcp.EventSubscribeRequest{Name: "record.added", Site: "shop", URL: x.server.URL, Secret: notifyTestSecret})
	var ee *mcp.EventError
	if !errors.As(err, &ee) || ee.Data["reason"] != "timeout" {
		t.Fatal("busy verification category")
	}
	if len(x.snapshot()) != 0 {
		t.Fatal("busy send contacted receiver")
	}
	release()
	notificationSQL(t, s, `UPDATE site_notifications SET expires_at=now()-interval '1 second' WHERE id=$1`, id)
	if notificationRequest(t, s, "POST", "/"+id+"/test", nil, 409).JSON(t)["code"] != "client_subscription" {
		t.Fatal("expired subscription test allowed")
	}
}

func TestNotifySuspensionEmailRetry(t *testing.T) {
	s, x := notificationFixture(t)
	id := subscribeNotification(t, s, x)
	notificationSQL(t, s, `UPDATE site_notifications SET state='suspended' WHERE id=$1`, id)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.A.Sites.Mailer = cancellingNoticeMailer{cancel: cancel}
	s.A.Access.Mailer, s.A.Data.Mailer = s.A.Sites.Mailer, s.A.Sites.Mailer
	s.A.Data.SweepNotifications(ctx)
	var emailed sql.NullTime
	if err := s.A.Database.QueryRow(`SELECT suspend_emailed_at FROM site_notifications WHERE id=$1`, id).Scan(&emailed); err != nil || emailed.Valid {
		t.Fatal("failed email was not released")
	}
	mail := &servertest.Mailbox{}
	s.A.Sites.Mailer = mail
	s.A.Access.Mailer, s.A.Data.Mailer = s.A.Sites.Mailer, s.A.Sites.Mailer
	s.A.Data.SweepNotifications(context.Background())
	s.A.Data.SweepNotifications(context.Background())
	if len(mail.To(s.Owner.Email)) != 1 {
		t.Fatal("email sweep did not retry exactly once")
	}
}

func TestNotifyEmitAfterStorageRelease(t *testing.T) {
	s, x := notificationFixture(t)
	subscribeNotification(t, s, x)
	s.Must(t, "PUT", "/resources/orders", servertest.StorageRules("sqlite", servertest.RulesPersonal), s.Key, 201)
	s.Must(t, "POST", "/sqlite/orders/schema", map[string]string{"sql": "CREATE TABLE orders(id INTEGER PRIMARY KEY, item TEXT)"}, s.Key, 200)
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
		t.Fatal("Postgres emission held SQLite writer or request")
	}
	// More than the old 100 ms budget must still retain both events.
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
	s.A.Data.EmitNotification(datahttp.StorageCall{SiteID: s.Sid}, "record.added", map[string]any{})
	if e.DroppedForTest() != before+1 {
		t.Fatal("full emit queue did not drop")
	}
	release()
}

func TestNotifyConnectorDeployScope(t *testing.T) {
	a := servertest.NewConnectorApp(t)
	if err := a.Access.SetPasscodeKey(servertest.TestPasscodeKey); err != nil {
		t.Fatal(err)
	}
	a.Conn.SetEventsBackend(a.Data.EventsBackend())
	p := a.NewPerson(t, "notify-deploy")
	u, err := accountapp.New(a.Database).GetUserByAPIKey(context.Background(), p.Key, 180*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	key := notifyapp.RandomID("test_", 24)
	if _, err = accountapp.New(a.Database).CreateKey(context.Background(), u.ID, accountsrepo.HashAPIKey(p.Key), key, "deploy", model.KeyScopeDeploy, nil, 50, 180*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"X-API-Key": key, "Content-Type": "application/json"}
	for _, method := range []string{"events/subscribe", "events/unsubscribe"} {
		r := a.Do(t, "POST", "/mcp", servertest.JSONBody(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": map[string]any{}}), headers)
		if r.JSON(t)["error"].(map[string]any)["code"] != float64(mcp.EventCodeForbidden) {
			t.Fatal("deploy scope reached events")
		}
	}
	r := a.Do(t, "POST", "/mcp", servertest.JSONBody(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}), headers)
	for _, raw := range r.JSON(t)["result"].(map[string]any)["tools"].([]any) {
		name := raw.(map[string]any)["name"]
		if name == "list_notifications" || name == "remove_notification" {
			t.Fatal("connector listed notifications for deploy key")
		}
	}
}

func notificationGrant(t *testing.T, s servertest.StorageTestSite, name string) string {
	t.Helper()
	client := name + "-" + s.OwnerID
	notificationSQL(t, s, `INSERT INTO oauth_clients(client_id,client_name,redirect_uris) VALUES($1,$2,ARRAY['https://example.test/cb'])`, client, name)
	t.Cleanup(func() { _, _ = s.A.Database.Exec(`DELETE FROM oauth_clients WHERE client_id=$1`, client) })
	var id string
	if err := s.A.Database.QueryRow(`INSERT INTO oauth_grants(user_id,client_id,scope,resource) VALUES($1,$2,'sites','x') RETURNING id`, s.OwnerID, client).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestNotifyMCPAPIKeyTTL(t *testing.T) {
	l := notificationSettings(t, func(l *config.Limits) { l.NotifyDefaultTTL = time.Hour })
	s, x := notificationFixture(t, edition.Default(l))
	backend := s.A.Data.EventsBackend()
	grant := notificationGrant(t, s, "ttl")
	short, long := 10*time.Minute, 2*time.Hour
	for _, tc := range []struct {
		name string
		set  bool
		ttl  *time.Duration
		want time.Duration
	}{
		{"omitted", false, nil, time.Hour},
		{"null", true, nil, time.Hour},
		{"long", true, &long, time.Hour},
		{"short", true, &short, short},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, oauth := range []bool{false, true} {
				caller := mcp.Caller{Owner: s.OwnerID}
				want := tc.want
				if oauth {
					caller.OAuthGrantID = grant
					if tc.set {
						want = 0
						if tc.ttl != nil {
							want = *tc.ttl
						}
					}
				}
				req := mcp.EventSubscribeRequest{Name: "record.added", Site: "shop", CanonicalArgs: `{"site":"shop"}`, URL: x.server.URL, Secret: notifyTestSecret, TTLSet: tc.set, TTL: tc.ttl}
				before := time.Now()
				result, err := backend.Subscribe(context.Background(), caller, req)
				if err != nil {
					t.Fatal(err)
				}
				if want == 0 {
					if result.RefreshBefore != nil {
						t.Fatal("OAuth null TTL was not granted")
					}
				} else if result.RefreshBefore == nil || result.RefreshBefore.Before(before.Add(want)) || result.RefreshBefore.After(time.Now().Add(want)) {
					t.Fatal("refreshBefore does not reflect granted TTL")
				}
				n, err := notificationsSvc(s.A.Data).GetNotification(context.Background(), result.ID)
				if err != nil {
					t.Fatal(err)
				}
				if (n.ExpiresAt == nil) != (result.RefreshBefore == nil) || n.ExpiresAt != nil && n.ExpiresAt.Sub(*result.RefreshBefore).Abs() > time.Microsecond {
					t.Fatal("stored expiry differs from grant")
				}
			}
		})
	}
}

func TestNotifyMCPGrantIdentity(t *testing.T) {
	s, x := notificationFixture(t)
	backend := s.A.Data.EventsBackend()
	ctx := context.Background()
	callers := []mcp.Caller{{Owner: s.OwnerID, OAuthGrantID: notificationGrant(t, s, "app-a")}, {Owner: s.OwnerID, OAuthGrantID: notificationGrant(t, s, "app-b")}, {Owner: s.OwnerID}}
	req := mcp.EventSubscribeRequest{Name: "record.added", Site: "shop", CanonicalArgs: `{"site":"shop"}`, URL: x.server.URL, Secret: notifyTestSecret}
	ids := map[string]bool{}
	var results []mcp.EventSubscribeResult
	var secrets []string
	for i, caller := range callers {
		if i > 0 {
			var err error
			req.Secret, err = notifyapp.NewSecret()
			if err != nil {
				t.Fatal(err)
			}
		}
		result, err := backend.Subscribe(ctx, caller, req)
		if err != nil {
			t.Fatal(err)
		}
		if result.Refreshed || ids[result.ID] {
			t.Fatal("different grants shared a subscription")
		}
		ids[result.ID] = true
		results = append(results, result)
		secrets = append(secrets, req.Secret)
		refresh, err := backend.Subscribe(ctx, caller, req)
		if err != nil || !refresh.Refreshed || refresh.ID != result.ID {
			t.Fatal("same grant did not refresh its own subscription")
		}
	}
	for i, result := range results {
		n, err := notificationsSvc(s.A.Data).GetNotification(ctx, result.ID)
		if err != nil || openedNotification(t, s, n.ID, n.Secret) != secrets[i] || n.PrevSecret != "" {
			t.Fatal("another app replaced a subscription secret")
		}
		if i < 2 && (n.GrantID == nil || *n.GrantID != callers[i].OAuthGrantID) || i == 2 && n.GrantID != nil {
			t.Fatal("another app replaced a subscription grant")
		}
	}
	unsub := mcp.EventUnsubscribeRequest{Name: req.Name, Site: req.Site, CanonicalArgs: req.CanonicalArgs, URL: req.URL}
	if err := backend.Unsubscribe(ctx, callers[1], unsub); err != nil {
		t.Fatal(err)
	}
	if _, err := notificationsSvc(s.A.Data).GetNotification(ctx, results[1].ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("unsubscribe did not remove its grant's subscription")
	}
	for _, i := range []int{0, 2} {
		if _, err := notificationsSvc(s.A.Data).GetNotification(ctx, results[i].ID); err != nil {
			t.Fatal("unsubscribe removed another grant's subscription")
		}
	}
}

func TestNotifyPausedAndExpiredEmission(t *testing.T) {
	s, x := notificationFixture(t)
	id := subscribeNotification(t, s, x)
	ctx := context.Background()
	engine := s.A.Data.Notifier()
	call := datahttp.StorageCall{SiteID: s.Sid, OwnerID: s.OwnerID, SiteName: "shop", ResourceName: "orders", Resource: datahttp.StorageResource{Kind: "kv"}}
	for _, state := range []string{"paused", "expired", "active", "suspended"} {
		t.Run(state, func(t *testing.T) {
			notificationSQL(t, s, `DELETE FROM site_events WHERE site_id=$1`, s.Sid)
			storedState := state
			if state == "expired" {
				storedState = "active"
			}
			notificationSQL(t, s, `UPDATE site_notifications SET state=$2,expires_at=NULL WHERE id=$1`, id, storedState)
			if state == "expired" {
				notificationSQL(t, s, `UPDATE site_notifications SET state='active',expires_at=now()-interval '1 second' WHERE id=$1`, id)
			}
			want := state == "active" || state == "suspended"
			yes, err := notificationsrepo.HasNotifications(ctx, s.A.Database, s.Sid)
			if err != nil || yes != want {
				t.Fatal("subscription eligibility mismatch")
			}
			minute, day := httpx.NewRateLimiter(1, 0), httpx.NewRateLimiter(1, 0)
			engine.EventCapsForTest(minute, day)
			s.A.Data.StoreNotification(call, "record.added", map[string]any{"key": "one"})
			var count int
			if err := s.A.Database.QueryRow(`SELECT count(*) FROM site_events WHERE site_id=$1`, s.Sid).Scan(&count); err != nil || (count == 1) != want {
				t.Fatal("ineligible subscription stored an event")
			}
			if minute.Allow(s.Sid) == want || day.Allow(s.Sid) == want {
				t.Fatal("ineligible subscription spent the event budget")
			}
			// The database fan-out must also exclude paused and expired rows.
			if err := notificationsrepo.EmitSiteEvent(ctx, s.A.Database, s.Sid, notifyapp.EventID(), "record.added", "orders", []byte(`{}`)); err != nil {
				t.Fatal(err)
			}
			if err := s.A.Database.QueryRow(`SELECT count(*) FROM site_event_deliveries WHERE subscription_id=$1`, id).Scan(&count); err != nil || want && count != 2 || !want && count != 0 {
				t.Fatal("incorrect fan-out eligibility")
			}
		})
	}
}

func TestNotifyOwnerHeaderAllowlist(t *testing.T) {
	s, x := notificationFixture(t)
	for _, name := range []string{"Forwarded", "X-Forwarded-For", "x-FoRwArDeD-Host", "X-Real-IP", "x-client-ip", "X-Originating-IP", "x-mcp-subscription-id", "X-", "X-" + strings.Repeat("a", 63), "X-token_name", "X-K", "X-ſ", "Proxy-Authorization"} {
		r := notificationRequest(t, s, "POST", "", map[string]any{"url": x.server.URL, "events": []string{"record.added"}, "auth_header": map[string]string{"name": name, "value": "test"}}, 400)
		body := r.JSON(t)
		if body["code"] != "invalid_header" || !strings.Contains(body["error"].(string), "Authorization or X-") {
			t.Fatal("REST header error omitted the permitted format")
		}
	}
}

func saveWorkerNotification(t *testing.T, s servertest.StorageTestSite, site, owner, url string) string {
	t.Helper()
	id := notifyapp.RandomID("sub_", 12)
	n := model.Notification{ID: id, SiteID: site, OwnerID: owner, Source: "owner", URL: url, Events: []string{"record.added"}, Secret: notifyTestSecret}
	if _, _, err := notifyapp.New(s.A.Database).SaveNotification(context.Background(), n, 10, nil, 7, notificationSecrets(s.A.Data)); err != nil {
		t.Fatal(err)
	}
	return id
}
func emitWorkerNotification(t *testing.T, s servertest.StorageTestSite, site string) {
	t.Helper()
	if err := notificationsrepo.EmitSiteEvent(context.Background(), s.A.Database, site, notifyapp.EventID(), "record.added", "orders", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
}
func checkDeferredNotification(t *testing.T, s servertest.StorageTestSite, id string, attempts, window int) {
	t.Helper()
	var gotAttempts, gotWindow, failures int
	var state, category string
	var lease *time.Time
	var due time.Time
	if err := s.A.Database.QueryRow(`SELECT d.attempts,n.window_attempts,n.window_failures,d.state,d.last_category,d.lease_until,d.next_attempt_at
 FROM site_event_deliveries d JOIN site_notifications n ON n.id=d.subscription_id WHERE n.id=$1 ORDER BY d.event_seq DESC LIMIT 1`, id).Scan(&gotAttempts, &gotWindow, &failures, &state, &category, &lease, &due); err != nil {
		t.Fatal(err)
	}
	if gotAttempts != attempts || gotWindow != window || failures != 0 || state != "pending" || category != "" || lease != nil || time.Until(due) < time.Second || time.Until(due) > 3*time.Second {
		t.Fatalf("throttle counted or failed an attempt: attempts=%d window=%d failures=%d state=%s category=%s lease=%v due=%v", gotAttempts, gotWindow, failures, state, category, lease, due)
	}
}

func TestNotifyOwnerConcurrency(t *testing.T) {
	l := notificationSettings(t, func(l *config.Limits) { l.NotifyOwnerConcurrency = 2 })
	s, x := notificationFixture(t, edition.Default(l))
	s.A.Deploy(t, s.Owner, "second")
	second := s.A.SiteID(t, s.Owner, "second")
	other := s.A.NewPerson(t, "other-owner")
	s.A.Deploy(t, other, "third")
	third := s.A.SiteID(t, other, "third")
	otherID, _ := s.A.UserID(t, other)
	one := saveWorkerNotification(t, s, s.Sid, s.OwnerID, x.server.URL)
	two := saveWorkerNotification(t, s, second, s.OwnerID, x.server.URL)
	saveWorkerNotification(t, s, third, otherID, x.server.URL)
	entered, release, done := make(chan struct{}, 3), make(chan struct{}), make(chan bool, 2)
	var once sync.Once
	defer once.Do(func() { close(release) })
	original := x.server.Config.Handler
	x.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-MCP-Subscription-Id")
		if id == one || id == two {
			entered <- struct{}{}
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		original.ServeHTTP(w, r)
	})
	for _, site := range []string{s.Sid, second} {
		emitWorkerNotification(t, s, site)
		go func() { done <- s.A.Data.DeliverNotification(context.Background()) }()
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatal("delivery did not reach receiver")
		}
	}
	emitWorkerNotification(t, s, s.Sid)
	emitWorkerNotification(t, s, third)
	if !s.A.Data.DeliverNotification(context.Background()) {
		t.Fatal("over-cap claim was not processed")
	}
	checkDeferredNotification(t, s, one, 0, 1)
	if !s.A.Data.DeliverNotification(context.Background()) || len(x.snapshot()) != 1 {
		t.Fatal("busy owner blocked another owner's delivery")
	}
	select {
	case <-entered:
		t.Fatal("owner exceeded concurrent delivery cap")
	default:
	}
	once.Do(func() { close(release) })
	for i := 0; i < 2; i++ {
		select {
		case ok := <-done:
			if !ok {
				t.Fatal("claimed worker failed")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("worker did not finish")
		}
	}
	notificationSQL(t, s, `UPDATE site_event_deliveries SET next_attempt_at=now() WHERE subscription_id=$1 AND state='pending'`, one)
	drainNotifications(t, s)
	if len(x.snapshot()) != 4 {
		t.Fatal("deferred delivery did not resume")
	}
	if s.A.Data.Notifier().OwnersInFlightForTest() != 0 {
		t.Fatal("owner concurrency entries were not released")
	}
}

func TestNotifyURLRateAcrossSitesAndSubscriptions(t *testing.T) {
	l := notificationSettings(t, func(l *config.Limits) { l.NotifyPerURLPerMinute = 2 })
	s, x := notificationFixture(t, edition.Default(l))
	other := s.A.NewPerson(t, "url-owner")
	s.A.Deploy(t, other, "other")
	otherSite := s.A.SiteID(t, other, "other")
	otherID, _ := s.A.UserID(t, other)
	saveWorkerNotification(t, s, s.Sid, s.OwnerID, x.server.URL)
	saveWorkerNotification(t, s, s.Sid, s.OwnerID, x.server.URL)
	limited := saveWorkerNotification(t, s, otherSite, otherID, x.server.URL)
	saveWorkerNotification(t, s, otherSite, otherID, x.server.URL+"/different")
	now := time.Now()
	budget := s.A.Data.Notifier().URLBudgetForTest(func() time.Time { return now })
	emitWorkerNotification(t, s, s.Sid)
	drainNotifications(t, s)
	if len(x.snapshot()) != 1 {
		t.Fatal("URL attempts burst past the configured rate")
	}
	now = now.Add(30 * time.Second)
	notificationSQL(t, s, `UPDATE site_event_deliveries SET next_attempt_at=now() WHERE state='pending'`)
	drainNotifications(t, s)
	emitWorkerNotification(t, s, otherSite)
	drainNotifications(t, s)
	if len(x.snapshot()) != 3 {
		t.Fatal("URL budget was not shared or different URL was blocked")
	}
	checkDeferredNotification(t, s, limited, 0, 0)
	now = now.Add(30 * time.Second)
	notificationSQL(t, s, `UPDATE site_event_deliveries SET next_attempt_at=now() WHERE subscription_id=$1`, limited)
	x.setStatus(500)
	drainNotifications(t, s)
	if len(x.snapshot()) != 4 {
		t.Fatal("URL token did not refill")
	}
	notificationSQL(t, s, `UPDATE site_event_deliveries SET next_attempt_at=now() WHERE subscription_id=$1`, limited)
	drainNotifications(t, s)
	var attempts, window, failures int
	if err := s.A.Database.QueryRow(`SELECT d.attempts,n.window_attempts,n.window_failures FROM site_event_deliveries d JOIN site_notifications n ON n.id=d.subscription_id WHERE n.id=$1`, limited).Scan(&attempts, &window, &failures); err != nil || attempts != 1 || window != 1 || failures != 1 || len(x.snapshot()) != 4 {
		t.Fatal("throttled retry changed real attempt accounting")
	}
	now = now.Add(2 * time.Hour)
	budget.EvictIdle(time.Hour)
	if budget.BucketCount() != 0 {
		t.Fatal("idle URL buckets were not cleaned up")
	}
}

func TestNotifyDeferredClaimLease(t *testing.T) {
	s, x := notificationFixture(t)
	id := subscribeNotification(t, s, x)
	ctx := context.Background()
	emitWorkerNotification(t, s, s.Sid)
	old, err := notificationsrepo.ClaimNotificationDelivery(ctx, s.A.Database, time.Minute, 5, 7, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	notificationSQL(t, s, `UPDATE site_event_deliveries SET lease_until=now()-interval '1 second' WHERE subscription_id=$1`, id)
	current, err := notificationsrepo.ClaimNotificationDelivery(ctx, s.A.Database, time.Minute, 5, 7, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := notificationsrepo.DeferNotificationDelivery(ctx, s.A.Database, old); err != nil {
		t.Fatal(err)
	}
	held, err := notificationsrepo.NotificationLeaseHeld(ctx, s.A.Database, current)
	if err != nil || !held {
		t.Fatal("stale deferral released a current lease")
	}
	for i := 0; i < 2; i++ {
		if err := notificationsrepo.DeferNotificationDelivery(ctx, s.A.Database, current); err != nil {
			t.Fatal(err)
		}
	}
	var attempts, window int
	if err := s.A.Database.QueryRow(`SELECT d.attempts,n.window_attempts FROM site_event_deliveries d JOIN site_notifications n ON n.id=d.subscription_id WHERE n.id=$1`, id).Scan(&attempts, &window); err != nil || attempts != 1 || window != 1 {
		t.Fatal("deferral undid an attempt more than once")
	}
}

type failingNoticeMailer struct{}

func (failingNoticeMailer) SendSignInCode(string, string, string) error { return nil }
func (failingNoticeMailer) SendNotice(string, string, string) error {
	return errors.New("smtp down")
}

type cancellingNoticeMailer struct {
	failingNoticeMailer
	cancel context.CancelFunc
}

func (m cancellingNoticeMailer) SendNoticeCtx(ctx context.Context, _, _, _ string) error {
	m.cancel()
	return ctx.Err()
}

func TestNotificationOwnerDrainsAcceptedEmits(t *testing.T) {
	for _, parentCancel := range []bool{false, true} {
		t.Run(fmt.Sprint(parentCancel), func(t *testing.T) {
			s, _ := notificationFixture(t)
			h := s.A.Data
			e := h.Notifier()
			owner := h.NotificationOwner()
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(func() { cancel(); owner.Stop(); owner.Wait() })
			owner.Start(ctx)
			finished := make(chan struct{})
			e.Emit(func(ctx context.Context) {
				// Work remains accepted until shutdown has closed admission.
				<-e.StoppedForTest()
				if err := ctx.Err(); err != nil {
					t.Errorf("accepted emit cancelled before drain: %v", err)
				}
				close(finished)
			})
			if parentCancel {
				cancel()
			} else {
				owner.Stop()
			}
			owner.Wait()
			select {
			case <-finished:
			default:
				t.Fatal("Wait did not join accepted emit")
			}
			e.Emit(func(context.Context) { t.Error("emit admitted after stop") })
			owner.Wait()
			if e.PendingForTest() != 0 {
				t.Fatal("stopped emitter leaked admission")
			}
		})
	}
}

// notificationsSvc is the notification store, for tests that read and
// seed it directly.
func notificationsSvc(h *datahttp.DataHandler) *notifyapp.Service { return notifyapp.New(h.Database) }

func notificationSecrets(h *datahttp.DataHandler) model.NotificationSecrets {
	return h.NotifyEnv().Secrets()
}
