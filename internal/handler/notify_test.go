package handler

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
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/mcp"
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
func (x *notificationReceiver) client() notifyClient {
	roots := x509.NewCertPool()
	roots.AddCert(x.server.Certificate())
	return notifyClient{allowLoopback: true, roots: roots}
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
func notificationSettings(t *testing.T, change func(*config.Limits)) {
	t.Helper()
	old := *config.Active()
	l := old
	change(&l)
	config.SetActive(l)
	t.Cleanup(func() { config.SetActive(old) })
}
func notificationFixture(t *testing.T) (presetSite, *notificationReceiver) {
	t.Helper()
	s := newPresetSite(t)
	x := newNotificationReceiver(t)
	if err := s.a.sites.SetPasscodeKey(testPasscodeKey); err != nil {
		t.Fatal(err)
	}
	s.a.sites.notifyEngine().client = x.client()
	s.a.sites.notifyEngine().client.secrets = &s.a.sites.passcode
	// Delivery tests drain synchronously; rate-limit tests use the real URL bucket.
	s.a.sites.notifyEngine().perURL = newRateLimiter(100000, 0)
	return s, x
}
func notificationRequest(t *testing.T, s presetSite, method, suffix string, body any, status int) resp {
	t.Helper()
	r := s.a.at(t, method, pcSiteDomain, "/v1/sites/shop/notifications"+suffix, body, s.key)
	if r.status != status {
		t.Fatalf("%s notifications%s: status %d, want %d", method, suffix, r.status, status)
	}
	return r
}
func subscribeNotification(t *testing.T, s presetSite, x *notificationReceiver) string {
	t.Helper()
	r := notificationRequest(t, s, "POST", "", map[string]any{"url": x.server.URL, "events": []string{"record.added", "record.changed", "file.uploaded"}, "secret": notifyTestSecret, "auth_header": map[string]string{"name": "Authorization", "value": "Bearer receiver"}}, 201)
	return r.json(t)["notification"].(map[string]any)["id"].(string)
}
func notificationSQL(t *testing.T, s presetSite, query string, args ...any) {
	t.Helper()
	waitNotificationEmits(t, s)
	if _, err := s.a.database.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}
func waitNotificationEmits(t *testing.T, s presetSite) {
	t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for len(s.a.sites.notifyEngine().emitting) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("notification emit did not finish")
		}
		time.Sleep(time.Millisecond)
	}
}
func drainNotifications(t *testing.T, s presetSite) {
	t.Helper()
	waitNotificationEmits(t, s)
	for i := 0; i < 100; i++ {
		if !s.a.sites.deliverNotification(context.Background()) {
			return
		}
	}
	t.Fatal("delivery queue did not drain")
}

func TestNotifyClientRefusesAddresses(t *testing.T) {
	c := notifyClient{}
	for _, raw := range []string{"https://127.0.0.1/", "https://169.254.169.254/", "https://10.0.0.1/", "https://[::1]/", "https://[::ffff:127.0.0.1]/", "https://100.64.0.1/", "https://[fc00::1]/"} {
		if _, err := c.validate(context.Background(), raw); !errors.Is(err, notifyFailure("refused_address")) {
			t.Errorf("%s: %v", raw, err)
		}
	}
	for _, raw := range []string{"http://example.com/", "https://user:pass@example.com/", "https://example.com/#fragment", "https://example.com/#", "https://example.com:0/", strings.Repeat("a", 2049)} {
		if _, err := c.validate(context.Background(), raw); !errors.Is(err, notifyFailure("invalid_url")) {
			t.Errorf("invalid URL accepted: %s", raw)
		}
	}
	c.resolver = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}, {IP: net.ParseIP("10.0.0.1")}}, nil
	}
	if _, err := c.validate(context.Background(), "https://mixed.example/"); !errors.Is(err, notifyFailure("refused_address")) {
		t.Fatal("mixed answer accepted")
	}
	calls := 0
	c.resolver = func(context.Context, string) ([]net.IPAddr, error) {
		calls++
		ip := "8.8.8.8"
		if calls > 1 {
			ip = "127.0.0.1"
		}
		return []net.IPAddr{{IP: net.ParseIP(ip)}}, nil
	}
	result := c.send(context.Background(), db.Notification{URL: "https://rebind.example/", Secret: notifyTestSecret}, "evt_test", []byte(`{}`))
	if result.Result != "refused_address" || calls != 2 {
		t.Fatalf("rebinding: %s, lookups %d", result.Result, calls)
	}
	c.resolver = func(context.Context, string) ([]net.IPAddr, error) { return nil, errors.New("lookup failed") }
	if _, err := c.validate(context.Background(), "https://dns.example/"); !errors.Is(err, notifyFailure("dns_error")) {
		t.Fatal(err)
	}
}
func TestNotifyClientRedirectAndHeaders(t *testing.T) {
	x := newNotificationReceiver(t)
	c := x.client()
	x.setStatus(302)
	n := db.Notification{ID: "sub_test", URL: x.server.URL, Secret: notifyTestSecret, Source: "owner", AuthHeaderName: "Authorization", AuthHeaderValue: "Bearer receiver"}
	result := c.send(context.Background(), n, "evt_redirect", []byte(`{}`))
	if result.Result != "redirect" || result.Status != 302 {
		t.Fatal(result.Result)
	}
	requests := x.snapshot()
	if len(requests) != 1 {
		t.Fatal("followed redirect")
	}
	r := requests[0]
	if r.header.Get("svix-signature") != r.header.Get("webhook-signature") || r.header.Get("Authorization") != "Bearer receiver" {
		t.Fatal("REST headers missing")
	}
	n.Source = "mcp"
	x.setStatus(200)
	result = c.send(context.Background(), n, "evt_mcp", []byte(`{}`))
	if !result.Delivered {
		t.Fatal(result.Result)
	}
	requests = x.snapshot()
	if requests[1].header.Get("svix-id") != "" || requests[1].header.Get("Authorization") != "" {
		t.Fatal("owner headers sent on MCP")
	}
	c.roots = nil
	result = c.send(context.Background(), n, "evt_tls", []byte(`{}`))
	if result.Result != "tls_error" {
		t.Fatalf("untrusted TLS: %s", result.Result)
	}
}
func TestNotifyHeaderAndSecretRules(t *testing.T) {
	for _, name := range []string{"Host", "Content-Type", "Content-Length", "Connection", "Transfer-Encoding", "Cookie", "User-Agent", "Webhook-ID", "Svix-Signature", "X-MCP-Subscription-Id", "bad_name"} {
		if validNotifyHeader(name, "x") {
			t.Errorf("accepted %s", name)
		}
	}
	if validNotifyHeader("Authorization", "x\r\ny") || validNotifyHeader("Authorization", strings.Repeat("x", 1025)) || !validNotifyHeader("Authorization", "Bearer test") {
		t.Fatal("header values")
	}
	for _, secret := range []string{"no-prefix", "whsec_?", "whsec_" + base64.StdEncoding.EncodeToString(make([]byte, 23)), "whsec_" + base64.StdEncoding.EncodeToString(make([]byte, 65))} {
		if _, err := notifySecretKey(secret); err == nil {
			t.Fatal("accepted invalid secret")
		}
	}
	generated, err := newNotifySecret()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(generated, "whsec_"))
	if err != nil || len(decoded) != 32 {
		t.Fatal("generated key length")
	}
}

func TestNotifyVisitorRoutes(t *testing.T) {
	s, x := notificationFixture(t)
	id := subscribeNotification(t, s, x)
	s.must(t, "PUT", "/resources/orders", map[string]string{"kind": "sqlite", "preset": "personal"}, s.key, 201)
	waitNotificationEmits(t, s)
	s.must(t, "POST", "/sqlite/orders/schema", map[string]string{"sql": "CREATE TABLE orders(id INTEGER PRIMARY KEY, item TEXT, created_at TEXT, updated_at TEXT)"}, s.key, 200)
	waitNotificationEmits(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	s.a.sites.StartNotifications(ctx)
	t.Cleanup(cancel)
	r := s.must(t, "POST", "/sqlite/orders/tables/orders/rows", map[string]string{"item": "Chocolate cake"}, s.callers["alice"], 200)
	waitNotificationEmits(t, s)
	row := fmt.Sprint(r.json(t)["last_insert_id"])
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
	if event["name"] != "record.added" || data["visitor"] != s.aliceID || data["table"] != "orders" || data["preset"] != "personal" || !strings.HasPrefix(data["site_url"].(string), "https://shop.") {
		t.Fatalf("event data: %v", data)
	}
	fields := data["fields"].(map[string]any)
	if fields["item"] != "Chocolate cake" || fields["visitor_id"] != nil || fields["created_at"] == nil {
		t.Fatal("incorrect fields")
	}
	s.must(t, "PATCH", "/sqlite/orders/tables/orders/rows/"+row, map[string]string{"item": "Vanilla"}, s.callers["alice"], 200)
	waitNotificationEmits(t, s)
	s.must(t, "DELETE", "/sqlite/orders/tables/orders/rows/"+row, nil, s.callers["alice"], 200)
	waitNotificationEmits(t, s)
	s.must(t, "POST", "/sqlite/orders/tables/orders/rows", map[string]string{"item": "Owner tools"}, s.key, 200)
	waitNotificationEmits(t, s)
	s.must(t, "POST", "/sqlite/orders/tables/orders/rows", map[string]string{"item": "Owner on site"}, s.callers["owner-site"], 200)
	waitNotificationEmits(t, s)
	s.must(t, "POST", "/sqlite/orders/tables/orders/rows", map[string]string{"missing_column": "failed"}, s.callers["alice"], 400)
	waitNotificationEmits(t, s)
	for _, kind := range []string{"kv", "files"} {
		s.must(t, "PUT", "/resources/"+kind, map[string]string{"kind": kind, "preset": "personal"}, s.key, 201)
		waitNotificationEmits(t, s)
	}
	s.must(t, "PUT", "/kv/kv/keys/order", `{"value":{"item":"bread"}}`, s.callers["alice"], 200)
	waitNotificationEmits(t, s)
	s.must(t, "PUT", "/kv/kv/keys/order", `{"value":"changed"}`, s.callers["alice"], 200)
	waitNotificationEmits(t, s)
	s.must(t, "DELETE", "/kv/kv/keys/order", nil, s.callers["alice"], 200)
	waitNotificationEmits(t, s)
	s.must(t, "PUT", "/files/files/objects/photo.txt", "photo bytes", s.callers["alice"], 200)
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
	}
	if names["record.added"] != 3 || names["record.changed"] != 4 || names["file.uploaded"] != 1 {
		t.Fatal(names)
	}
	list := notificationRequest(t, s, "GET", "", nil, 200)
	if strings.Contains(string(list.body), "whsec_") || strings.Contains(string(list.body), "Bearer receiver") {
		t.Fatal("subscription list leaked credentials")
	}
	history := notificationRequest(t, s, "GET", "/"+id+"/deliveries", nil, 200).json(t)["deliveries"].([]any)
	if len(history) != 8 {
		t.Fatalf("history count %d", len(history))
	}
}

func TestNotifyRetriesSuspensionAndResume(t *testing.T) {
	notificationSettings(t, func(l *config.Limits) { l.NotifySuspendAttempts = 3 })
	s, x := notificationFixture(t)
	id := subscribeNotification(t, s, x)
	mail := &mailbox{}
	s.a.sites.mailer = mail
	x.setStatus(500)
	s.must(t, "PUT", "/resources/kv", map[string]string{"kind": "kv", "preset": "personal"}, s.key, 201)
	waitNotificationEmits(t, s)
	s.must(t, "PUT", "/kv/kv/keys/one", `{"value":1}`, s.callers["alice"], 200)
	waitNotificationEmits(t, s)
	for i := 0; i < 3; i++ {
		notificationSQL(t, s, `UPDATE site_event_deliveries SET next_attempt_at=now() WHERE subscription_id=$1`, id)
		drainNotifications(t, s)
	}
	n, err := db.GetNotification(context.Background(), s.a.database, id)
	if err != nil || n.State != "suspended" {
		t.Fatalf("not suspended: %s %v", n.State, err)
	}
	got := x.snapshot()
	if len(got) != 3 || !got[1].duplicate || !got[2].duplicate {
		t.Fatal("retry id dedup failed")
	}
	s.a.sites.notifySuspension(context.Background(), n)
	if len(mail.to(s.owner.email)) != 1 {
		t.Fatal("suspension must send one notice")
	}
	drainNotifications(t, s)
	if len(x.snapshot()) != 3 {
		t.Fatal("suspended attempt sent")
	}
	x.setStatus(200)
	notificationRequest(t, s, "PATCH", "/"+id, map[string]bool{"paused": false}, 200)
	drainNotifications(t, s)
	n, err = db.GetNotification(context.Background(), s.a.database, id)
	if err != nil || n.State != "active" || n.LastSuccessAt == nil {
		t.Fatal("resume did not deliver")
	}
	for _, status := range []int{410, 413} {
		x.setStatus(status)
		s.must(t, "PUT", "/kv/kv/keys/"+strconv.Itoa(status), `{"value":1}`, s.callers["alice"], 200)
		waitNotificationEmits(t, s)
		drainNotifications(t, s)
		var state string
		var attempts int
		if err = s.a.database.QueryRow(`SELECT state,attempts FROM site_event_deliveries WHERE subscription_id=$1 ORDER BY event_seq DESC LIMIT 1`, id).Scan(&state, &attempts); err != nil || state != "failed" || attempts != 1 {
			t.Fatalf("%d: %s %d %v", status, state, attempts, err)
		}
	}
}

func TestNotifyCapsTruncationRotationAndControls(t *testing.T) {
	notificationSettings(t, func(l *config.Limits) { l.NotifyMaxPerSite = 1; l.NotifyPerMinute = 2 })
	s, x := notificationFixture(t)
	id := subscribeNotification(t, s, x)
	notificationRequest(t, s, "POST", "", map[string]any{"url": x.server.URL, "events": []string{"record.added"}}, 409)
	s.must(t, "PUT", "/resources/kv", map[string]string{"kind": "kv", "preset": "personal"}, s.key, 201)
	waitNotificationEmits(t, s)
	s.must(t, "PUT", "/kv/kv/keys/long", map[string]any{"value": map[string]string{"long": strings.Repeat("é", 2000)}}, s.callers["alice"], 200)
	waitNotificationEmits(t, s)
	many := map[string]string{}
	for i := 0; i < 30; i++ {
		many[fmt.Sprint(i)] = strings.Repeat("x", 1000)
	}
	s.must(t, "PUT", "/kv/kv/keys/big", map[string]any{"value": many}, s.callers["alice"], 200)
	waitNotificationEmits(t, s)
	s.must(t, "PUT", "/kv/kv/keys/capped", `{"value":1}`, s.callers["alice"], 200)
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
	rotated := notificationRequest(t, s, "POST", "/"+id+"/rotate-secret", nil, 200).json(t)["secret"].(string)
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
	backend := s.a.sites.EventsBackend()
	caller := mcp.Caller{Owner: s.ownerID, AppName: "Test app"}
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
	s.must(t, "PUT", "/resources/kv", map[string]string{"kind": "kv", "preset": "personal"}, s.key, 201)
	waitNotificationEmits(t, s)
	s.must(t, "PUT", "/kv/kv/keys/one", `{"value":1}`, s.callers["alice"], 200)
	waitNotificationEmits(t, s)
	var seq int64
	if err = s.a.database.QueryRow(`SELECT max(seq) FROM site_events WHERE site_id=$1`, s.sid).Scan(&seq); err != nil {
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
	safe, err := db.NotificationCursor(context.Background(), s.a.database, replay.ID, s.sid)
	if err != nil || safe >= seq {
		t.Fatal("cursor passed a pending delivery")
	}
	drainNotifications(t, s)
	safe, err = db.NotificationCursor(context.Background(), s.a.database, replay.ID, s.sid)
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
	_, err = backend.Subscribe(context.Background(), mcp.Caller{Owner: s.aliceID}, req)
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
		if err := db.EmitSiteEvent(ctx, s.a.database, s.sid, notifyEventID(), "record.added", "orders", []byte(`{"v":1}`)); err != nil {
			t.Fatal(err)
		}
	}
	// Concurrent claimers must get different rows, and a third must find none.
	claims := make(chan db.NotificationDelivery, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			d, err := db.ClaimNotificationDelivery(ctx, s.a.database, time.Minute, 5, 7, time.Hour)
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
	if _, err := db.ClaimNotificationDelivery(ctx, s.a.database, time.Minute, 5, 7, time.Hour); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("lease did not hold delivery")
	}
	finish := func(d db.NotificationDelivery) {
		t.Helper()
		_, err := db.FinishNotificationDelivery(ctx, s.a.database, d, "delivered", "ok", 200, 1, time.Now(), time.Hour, 100, 95, 72*time.Hour)
		if err != nil {
			t.Fatal(err)
		}
	}
	finish(b)
	cursor, err := db.NotificationCursor(ctx, s.a.database, id, s.sid)
	if err != nil || cursor >= a.Seq {
		t.Fatal("cursor passed unresolved earlier event")
	}
	notificationSQL(t, s, `UPDATE site_event_deliveries SET lease_until=now()-interval '1 second' WHERE event_seq=$1`, a.Seq)
	newer, err := db.ClaimNotificationDelivery(ctx, s.a.database, time.Minute, 5, 7, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	finish(a)
	var state string
	if err = s.a.database.QueryRow(`SELECT state FROM site_event_deliveries WHERE event_seq=$1`, a.Seq).Scan(&state); err != nil || state != "pending" {
		t.Fatal("stale worker overwrote newer lease")
	}
	finish(newer)
	cursor, err = db.NotificationCursor(ctx, s.a.database, id, s.sid)
	if err != nil || cursor != b.Seq {
		t.Fatal("watermark did not advance")
	}
	if err = db.EmitSiteEvent(ctx, s.a.database, s.sid, notifyEventID(), "record.added", "orders", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	notificationSQL(t, s, `UPDATE site_notifications SET expires_at=now()-interval '1 minute' WHERE id=$1`, id)
	drainNotifications(t, s)
	if len(x.snapshot()) != 0 {
		t.Fatal("expired subscription delivered")
	}
	if n := notificationRequest(t, s, "GET", "", nil, 200).json(t)["notifications"].([]any)[0].(map[string]any); n["state"] != "expired" {
		t.Fatal("expiry not shown")
	}
	notificationSQL(t, s, `UPDATE site_events SET created_at=now()-interval '8 days' WHERE site_id=$1`, s.sid)
	notificationSQL(t, s, `UPDATE site_notifications SET expires_at=now()-interval '8 days' WHERE id=$1`, id)
	if err = db.SweepNotifications(ctx, s.a.database, 7); err != nil {
		t.Fatal(err)
	}
	var events, deliveries int
	if err = s.a.database.QueryRow(`SELECT (SELECT count(*) FROM site_events WHERE site_id=$1),(SELECT count(*) FROM site_event_deliveries WHERE subscription_id=$2)`, s.sid, id).Scan(&events, &deliveries); err != nil || events != 0 || deliveries != 0 {
		t.Fatal("history sweep did not cascade")
	}
	if _, err = db.GetNotification(ctx, s.a.database, id); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("expired subscription not swept")
	}
}

func TestNotifyUnavailableHeldAndEventAccounts(t *testing.T) {
	s, x := notificationFixture(t)
	id := subscribeNotification(t, s, x)
	ctx := context.Background()
	emit := func() {
		t.Helper()
		if err := db.EmitSiteEvent(ctx, s.a.database, s.sid, notifyEventID(), "record.added", "orders", []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
	for _, column := range []string{"offline_at", "suspended_at", "deleted_at", "owner", "transfer"} {
		emit()
		if column == "transfer" {
			notificationSQL(t, s, `UPDATE sites SET user_id=$2 WHERE id=$1`, s.sid, s.aliceID)
		} else if column == "owner" {
			notificationSQL(t, s, `UPDATE users SET suspended_at=now() WHERE id=$1`, s.ownerID)
		} else {
			notificationSQL(t, s, `UPDATE sites SET `+column+`=now() WHERE id=$1`, s.sid)
		}
		drainNotifications(t, s)
		var state, category string
		if err := s.a.database.QueryRow(`SELECT state,last_category FROM site_event_deliveries WHERE subscription_id=$1 ORDER BY event_seq DESC LIMIT 1`, id).Scan(&state, &category); err != nil || state != "skipped" || category != "site_unavailable" {
			t.Fatalf("%s: %s %s %v", column, state, category, err)
		}
		if column == "transfer" {
			notificationSQL(t, s, `UPDATE sites SET user_id=$2 WHERE id=$1`, s.sid, s.ownerID)
		} else if column == "owner" {
			notificationSQL(t, s, `UPDATE users SET suspended_at=NULL WHERE id=$1`, s.ownerID)
		} else {
			notificationSQL(t, s, `UPDATE sites SET `+column+`=NULL WHERE id=$1`, s.sid)
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
	notificationSQL(t, s, `UPDATE users SET event_account=true WHERE id=$1`, s.ownerID)
	c := storageCall{siteID: s.sid, ownerID: s.ownerID, siteName: "shop", resourceName: "orders", resource: storageResource{Kind: "kv"}}
	s.a.sites.emitNotification(c, "record.added", map[string]any{"key": "event-website"})
	drainNotifications(t, s)
	if len(x.snapshot()) != 1 {
		t.Fatal("event website emitted")
	}
	notificationRequest(t, s, "GET", "", nil, 404)
}

func TestNotifyWindowAndContinuousFailure(t *testing.T) {
	notificationSettings(t, func(l *config.Limits) { l.NotifySuspendAttempts = 3 })
	s, x := notificationFixture(t)
	id := subscribeNotification(t, s, x)
	ctx := context.Background()
	x.setStatus(500)
	// Two old failures must age out of the counting window.
	notificationSQL(t, s, `UPDATE site_notifications SET window_start=now()-interval '2 hours',window_attempts=2,window_failures=2 WHERE id=$1`, id)
	if err := db.EmitSiteEvent(ctx, s.a.database, s.sid, notifyEventID(), "record.added", "orders", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	drainNotifications(t, s)
	n, err := db.GetNotification(ctx, s.a.database, id)
	if err != nil || n.State != "active" {
		t.Fatal("expired failures caused suspension")
	}
	var attempts int
	if err = s.a.database.QueryRow(`SELECT window_attempts FROM site_notifications WHERE id=$1`, id).Scan(&attempts); err != nil || attempts != 1 {
		t.Fatal("expired window not reset")
	}
	notificationSQL(t, s, `UPDATE site_notifications SET failing_since=now()-interval '4 days' WHERE id=$1`, id)
	notificationSQL(t, s, `UPDATE site_event_deliveries SET next_attempt_at=now() WHERE subscription_id=$1`, id)
	drainNotifications(t, s)
	n, err = db.GetNotification(ctx, s.a.database, id)
	if err != nil || n.State != "suspended" {
		t.Fatal("continuous failure did not suspend")
	}
}

func TestNotifyDailyCapAndNoSubscribers(t *testing.T) {
	notificationSettings(t, func(l *config.Limits) { l.NotifyPerDay = 1 })
	s, x := notificationFixture(t)
	s.must(t, "PUT", "/resources/kv", map[string]string{"kind": "kv", "preset": "personal"}, s.key, 201)
	waitNotificationEmits(t, s)
	s.must(t, "PUT", "/kv/kv/keys/no-subscriber", `{"value":1}`, s.callers["alice"], 200)
	waitNotificationEmits(t, s)
	var count int
	if err := s.a.database.QueryRow(`SELECT count(*) FROM site_events WHERE site_id=$1`, s.sid).Scan(&count); err != nil || count != 0 {
		t.Fatal("stored event without subscriptions")
	}
	subscribeNotification(t, s, x)
	for _, key := range []string{"one", "two"} {
		s.must(t, "PUT", "/kv/kv/keys/"+key, `{"value":1}`, s.callers["alice"], 200)
		waitNotificationEmits(t, s)
	}
	drainNotifications(t, s)
	if len(x.snapshot()) != 1 {
		t.Fatal("daily cap")
	}
}

func TestNotifyRESTOwnershipValidationAndHack(t *testing.T) {
	s, x := notificationFixture(t)
	id := subscribeNotification(t, s, x)
	fullURL := x.server.URL + "/incoming/credential?token=private"
	notificationSQL(t, s, `UPDATE site_notifications SET url=$2 WHERE id=$1`, id, fullURL)
	listed := notificationRequest(t, s, "GET", "", nil, 200).json(t)["notifications"].([]any)
	if len(listed) != 1 || listed[0].(map[string]any)["url"] != fullURL {
		t.Fatal("REST list did not preserve the full webhook URL")
	}
	other := s.a.newPerson(t, "notification-other")
	r := s.a.at(t, "DELETE", pcSiteDomain, "/v1/sites/shop/notifications/"+id, nil, map[string]string{"X-API-Key": other.key})
	if r.status != 404 {
		t.Fatal("other account could address subscription")
	}
	r = s.a.at(t, "GET", pcSiteDomain, "/v1/notifications", nil, map[string]string{"X-API-Key": other.key})
	if r.status != 200 || len(r.json(t)["notifications"].([]any)) != 0 {
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
		if r.json(t)["code"] != tc.code {
			t.Fatal("wrong validation code")
		}
	}
	notificationRequest(t, s, "POST", "", map[string]any{"url": x.server.URL, "events": []string{"record.added"}, "storage": "missing"}, 404)
	old := hackMode
	SetHackMode(true)
	t.Cleanup(func() { SetHackMode(old) })
	notificationRequest(t, s, "GET", "", nil, 404)
	if s.a.sites.EventsBackend() != nil {
		t.Fatal("Hack backend offered")
	}
}

func TestNotifyMCPRotationTTLAndChallengeLimit(t *testing.T) {
	s, x := notificationFixture(t)
	backend := s.a.sites.EventsBackend()
	caller := mcp.Caller{Owner: s.ownerID}
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
	secret, err := newNotifySecret()
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
	n, err := db.GetNotification(ctx, s.a.database, first.ID)
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

func TestNotifyClientTimeoutAndResponseLimit(t *testing.T) {
	notificationSettings(t, func(l *config.Limits) { l.NotifyTimeout = 30 * time.Millisecond })
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			time.Sleep(100 * time.Millisecond)
		}
		_, _ = w.Write([]byte(strings.Repeat("x", 8192)))
	}))
	defer srv.Close()
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	c := notifyClient{allowLoopback: true, roots: roots}
	n := db.Notification{Secret: notifyTestSecret, URL: srv.URL}
	result := c.send(context.Background(), n, "msg_verification_size", []byte(`{}`))
	if !result.Delivered || len(result.body) != 4096 {
		t.Fatalf("response limit: %s, %d", result.Result, len(result.body))
	}
	n.URL += "/slow"
	result = c.send(context.Background(), n, "evt_timeout", []byte(`{}`))
	if result.Result != "timeout" {
		t.Fatalf("timeout category: %s", result.Result)
	}
}

func TestNotifyRefusedAddressAndPermanentHTTPDoNotRetry(t *testing.T) {
	notificationSettings(t, func(l *config.Limits) { l.NotifySuspendAttempts = 1 })
	s, x := notificationFixture(t)
	id := subscribeNotification(t, s, x)
	ctx := context.Background()
	emit := func() {
		t.Helper()
		if err := db.EmitSiteEvent(ctx, s.a.database, s.sid, notifyEventID(), "record.added", "orders", []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
	for _, status := range []int{410, 413} {
		x.setStatus(status)
		emit()
		drainNotifications(t, s)
		n, err := db.GetNotification(ctx, s.a.database, id)
		if err != nil || n.State != "active" {
			t.Fatal("permanent HTTP result changed subscription")
		}
	}
	s.a.sites.notifyEngine().client.allowLoopback = false
	emit()
	drainNotifications(t, s)
	var state, category string
	var attempts int
	if err := s.a.database.QueryRow(`SELECT state,last_category,attempts FROM site_event_deliveries WHERE subscription_id=$1 ORDER BY event_seq DESC LIMIT 1`, id).Scan(&state, &category, &attempts); err != nil || state != "failed" || category != "refused_address" || attempts != 1 {
		t.Fatal("refused address should abandon after one attempt")
	}
	if len(x.snapshot()) != 2 {
		t.Fatal("refused callback was contacted")
	}
}

func TestNotifyConcurrentSubscriptionCapAndFilters(t *testing.T) {
	notificationSettings(t, func(l *config.Limits) { l.NotifyMaxPerSite = 1 })
	s, x := notificationFixture(t)
	ctx := context.Background()
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, _, err := db.SaveNotification(ctx, s.a.database, db.Notification{ID: notifyRandom("sub_", 12), SiteID: s.sid, OwnerID: s.ownerID, Source: "owner", URL: x.server.URL, Events: []string{"record.added"}, Storage: "orders", Secret: notifyTestSecret}, 1, nil, 7, s.a.sites.notificationSecrets())
			errs <- err
		}()
	}
	accepted, refused := 0, 0
	for i := 0; i < 2; i++ {
		err := <-errs
		if err == nil {
			accepted++
		} else if errors.Is(err, db.ErrNotificationCap) {
			refused++
		} else {
			t.Fatal(err)
		}
	}
	if accepted != 1 || refused != 1 {
		t.Fatal("concurrent cap exceeded")
	}
	for _, event := range []struct{ name, storage string }{{"record.added", "other"}, {"record.changed", "orders"}, {"record.added", "orders"}} {
		if err := db.EmitSiteEvent(ctx, s.a.database, s.sid, notifyEventID(), event.name, event.storage, []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
	drainNotifications(t, s)
	if len(x.snapshot()) != 1 {
		t.Fatal("event and storage filters")
	}
}

func TestNotifySpecialAddressesAndExactPayloads(t *testing.T) {
	for _, ip := range []string{"64:ff9b:1::a00:1", "2001::1", "2001:1ff::1", "2002:a00:1::1", "100::1", "2001:db8::1", "fec0::1", "::7f00:1", "::a9fe:a9fe", "::8.8.8.8"} {
		if isPublicIP(ip) {
			t.Errorf("accepted non-public address %s", ip)
		}
	}
	for _, raw := range []string{`9007199254740993`, `1e400`, `{"large":1e400,"n":9007199254740993}`} {
		body, err := json.Marshal(notifyFields(json.RawMessage(raw)))
		if err != nil || !strings.Contains(string(body), raw) {
			t.Errorf("number changed: %s", body)
		}
	}
	if notifyFields(json.RawMessage("not JSON"))["value"] != "not JSON" {
		t.Fatal("undecodable value was discarded")
	}
	notificationSettings(t, func(l *config.Limits) { l.NotifyBodyMaxBytes = 4096 })
	for _, key := range []string{"path", "key"} {
		data := map[string]any{"v": 1, "site": "shop", key: strings.Repeat("é\n", 5000), "fields": map[string]any{"n": json.Number("9007199254740993")}}
		body := notifyBody(db.NotificationDelivery{EventID: "evt_test", Name: "record.added", Data: storageJSON(data)}, 1)
		var payload struct {
			Data map[string]any `json:"data"`
		}
		if len(body) > 4096 || json.Unmarshal(body, &payload) != nil {
			t.Fatal("body exceeded final limit or was invalid")
		}
		if len(payload.Data[key].(string)) > 256 || !strings.Contains(string(body), `"truncated":["fields","`+key+`"]`) {
			t.Fatal("long visitor key was not listed as truncated")
		}
	}
}

func TestNotifyRefreshPreservesLeaseAndBackoff(t *testing.T) {
	s, x := notificationFixture(t)
	id := subscribeNotification(t, s, x)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := db.EmitSiteEvent(ctx, s.a.database, s.sid, notifyEventID(), "record.added", "", []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
	d, err := db.ClaimNotificationDelivery(ctx, s.a.database, time.Minute, 5, 7, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	notificationSQL(t, s, `UPDATE site_event_deliveries SET next_attempt_at=now()+interval '1 hour' WHERE subscription_id=$1`, id)
	n, err := db.GetNotification(ctx, s.a.database, id)
	if err != nil {
		t.Fatal(err)
	}
	n.Secret = notifyTestSecret
	if _, _, err = db.SaveNotification(ctx, s.a.database, n, 10, nil, 7, s.a.sites.notificationSecrets()); err != nil {
		t.Fatal(err)
	}
	var held, deferred bool
	if err = s.a.database.QueryRow(`SELECT lease_until=$2,next_attempt_at>now() FROM site_event_deliveries WHERE event_seq=$1`, d.Seq, d.LeaseUntil).Scan(&held, &deferred); err != nil || !held || !deferred {
		t.Fatal("active refresh changed live lease or backoff")
	}
	notificationRequest(t, s, "PATCH", "/"+id, map[string]bool{"paused": true}, 200)
	notificationRequest(t, s, "PATCH", "/"+id, map[string]bool{"paused": false}, 200)
	if err = s.a.database.QueryRow(`SELECT lease_until=$2,next_attempt_at>now() FROM site_event_deliveries WHERE event_seq=$1`, d.Seq, d.LeaseUntil).Scan(&held, &deferred); err != nil || !held || !deferred {
		t.Fatal("resume changed live lease")
	}
	var due bool
	if err = s.a.database.QueryRow(`SELECT next_attempt_at<=now() FROM site_event_deliveries WHERE subscription_id=$1 AND event_seq<>$2`, id, d.Seq).Scan(&due); err != nil || !due {
		t.Fatal("resume did not release held unleased delivery")
	}
}

func TestNotifyCrashBudgetAndStaleWorker(t *testing.T) {
	s, x := notificationFixture(t)
	id := subscribeNotification(t, s, x)
	ctx := context.Background()
	if err := db.EmitSiteEvent(ctx, s.a.database, s.sid, notifyEventID(), "record.added", "", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	var first db.NotificationDelivery
	for i := 1; i <= 2; i++ {
		d, err := db.ClaimNotificationDelivery(ctx, s.a.database, time.Minute, 2, 7, time.Hour)
		if err != nil || d.Attempts != i {
			t.Fatalf("claim %d: attempts %d, %v", i, d.Attempts, err)
		}
		if i == 1 {
			first = d
		}
		notificationSQL(t, s, `UPDATE site_event_deliveries SET lease_until=now()-interval '1 second' WHERE subscription_id=$1`, id)
	}
	s.a.sites.processNotification(ctx, first)
	if len(x.snapshot()) != 0 {
		t.Fatal("worker sent without its lease")
	}
	first.LeaseUntil = time.Now().Add(-time.Second)
	s.a.sites.processNotification(ctx, first)
	if len(x.snapshot()) != 0 {
		t.Fatal("worker sent after lease deadline")
	}
	if _, err := db.ClaimNotificationDelivery(ctx, s.a.database, time.Minute, 2, 7, time.Hour); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("crashes exceeded send budget")
	}
	var attempts, failures int
	var state string
	if err := s.a.database.QueryRow(`SELECT d.state,n.window_attempts,n.window_failures FROM site_notifications n JOIN site_event_deliveries d ON d.subscription_id=n.id WHERE n.id=$1`, id).Scan(&state, &attempts, &failures); err != nil || state != "failed" || attempts != 2 || failures != 2 {
		t.Fatal("abandoned claims were not accounted")
	}
}

func TestNotifyHistoryAndDeletedListings(t *testing.T) {
	s, x := notificationFixture(t)
	id := subscribeNotification(t, s, x)
	ctx := context.Background()
	if err := db.EmitSiteEvent(ctx, s.a.database, s.sid, notifyEventID(), "record.added", "", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	notificationSQL(t, s, `UPDATE site_events SET created_at=now()-interval '8 days' WHERE site_id=$1`, s.sid)
	if _, err := db.ClaimNotificationDelivery(ctx, s.a.database, time.Minute, 5, 7, time.Hour); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("claimed expired history")
	}
	notificationSQL(t, s, `DELETE FROM site_event_deliveries WHERE subscription_id=$1`, id)
	n, err := db.GetNotification(ctx, s.a.database, id)
	if err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	n.Secret = notifyTestSecret
	if _, _, err := db.SaveNotification(ctx, s.a.database, n, 10, &zero, 7, s.a.sites.notificationSecrets()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.a.database.QueryRow(`SELECT count(*) FROM site_event_deliveries WHERE subscription_id=$1`, id).Scan(&count); err != nil || count != 0 {
		t.Fatal("replayed expired history")
	}
	run, cancel := context.WithCancel(ctx)
	s.a.sites.StartNotifications(run)
	cancel()
	if err := s.a.database.QueryRow(`SELECT count(*) FROM site_events WHERE site_id=$1`, s.sid).Scan(&count); err != nil || count != 0 {
		t.Fatal("startup did not sweep history")
	}
	notificationSQL(t, s, `UPDATE sites SET deleted_at=now() WHERE id=$1`, s.sid)
	for _, site := range []string{"", s.sid} {
		ns, err := db.ListNotifications(ctx, s.a.database, s.ownerID, site)
		if err != nil || len(ns) != 0 {
			t.Fatal("listed deleted site's notifications")
		}
	}
}

func TestNotifyMCPValidationAndSiteReplacement(t *testing.T) {
	s, x := notificationFixture(t)
	ctx := context.Background()
	b := s.a.sites.EventsBackend()
	c := mcp.Caller{Owner: s.ownerID}
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
	s.a.deploy(t, s.owner, "replacement")
	replacement := s.a.siteID(t, s.owner, "replacement")
	notificationSQL(t, s, `UPDATE sites SET name='oldshop' WHERE id=$1`, s.sid)
	notificationSQL(t, s, `UPDATE sites SET name='shop' WHERE id=$1`, replacement)
	count := len(x.snapshot())
	next, err := b.Subscribe(ctx, c, req)
	if err != nil || next.Refreshed || next.ID != first.ID || len(x.snapshot()) != count+1 {
		t.Fatalf("new site did not require fresh verification: %v", err)
	}
	n, err := db.GetNotification(ctx, s.a.database, next.ID)
	if err != nil || n.SiteID != replacement {
		t.Fatal("subscription remained on old site")
	}
	n.SiteID = s.sid
	n.Secret = notifyTestSecret
	if _, _, err = db.SaveNotification(ctx, s.a.database, n, 10, nil, 7, s.a.sites.notificationSecrets()); !errors.Is(err, db.ErrNotificationSite) {
		t.Fatal("upsert crossed sites")
	}
}

func TestNotifyChallengeBudgetsAndBusy(t *testing.T) {
	h := &SiteHandler{}
	h.notifyEngine().client.resolver = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}, {IP: net.ParseIP("8.8.4.4")}}, nil
	}
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		if _, err := h.notifyChallengeClient(ctx, fmt.Sprint(i), fmt.Sprintf("https://host%d.example/", i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.notifyChallengeClient(ctx, "another", "https://alias.example/"); !errors.Is(err, notifyFailure("rate_limited")) {
		t.Fatal("aliases bypassed IP budget")
	}
	h = &SiteHandler{}
	h.notifyEngine().client.resolver = func(_ context.Context, host string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP(host)}}, nil
	}
	for i := 1; i <= 10; i++ {
		if _, err := h.notifyChallengeClient(ctx, "owner", fmt.Sprintf("https://8.8.8.%d/", i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.notifyChallengeClient(ctx, "owner", "https://8.8.4.4/"); !errors.Is(err, notifyFailure("rate_limited")) {
		t.Fatal("destinations bypassed owner budget")
	}
	s, x := notificationFixture(t)
	id := subscribeNotification(t, s, x)
	for i := 0; i < 10; i++ {
		s.a.sites.notifyEngine().verifying <- struct{}{}
	}
	r := notificationRequest(t, s, "POST", "/"+id+"/test", nil, 503)
	if r.json(t)["code"] != "busy" {
		t.Fatal("busy test category")
	}
	_, err := s.a.sites.EventsBackend().Subscribe(ctx, mcp.Caller{Owner: s.ownerID}, mcp.EventSubscribeRequest{Name: "record.added", Site: "shop", URL: x.server.URL, Secret: notifyTestSecret})
	var ee *mcp.EventError
	if !errors.As(err, &ee) || ee.Data["reason"] != "timeout" {
		t.Fatal("busy verification category")
	}
	if len(x.snapshot()) != 0 {
		t.Fatal("busy send contacted receiver")
	}
	for i := 0; i < 10; i++ {
		<-s.a.sites.notifyEngine().verifying
	}
	notificationSQL(t, s, `UPDATE site_notifications SET expires_at=now()-interval '1 second' WHERE id=$1`, id)
	if notificationRequest(t, s, "POST", "/"+id+"/test", nil, 409).json(t)["code"] != "client_subscription" {
		t.Fatal("expired subscription test allowed")
	}
}

func TestNotifySuspensionEmailRetry(t *testing.T) {
	s, x := notificationFixture(t)
	id := subscribeNotification(t, s, x)
	notificationSQL(t, s, `UPDATE site_notifications SET state='suspended' WHERE id=$1`, id)
	s.a.sites.mailer = failingNoticeMailer{}
	s.a.sites.sweepNotifications(context.Background())
	var emailed sql.NullTime
	if err := s.a.database.QueryRow(`SELECT suspend_emailed_at FROM site_notifications WHERE id=$1`, id).Scan(&emailed); err != nil || emailed.Valid {
		t.Fatal("failed email was not released")
	}
	mail := &mailbox{}
	s.a.sites.mailer = mail
	s.a.sites.sweepNotifications(context.Background())
	s.a.sites.sweepNotifications(context.Background())
	if len(mail.to(s.owner.email)) != 1 {
		t.Fatal("email sweep did not retry exactly once")
	}
}

func TestNotifyAcknowledgmentIgnoresBrokenBody(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("{"))
	}))
	defer srv.Close()
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	c := notifyClient{allowLoopback: true, roots: roots}
	n := db.Notification{URL: srv.URL, Secret: notifyTestSecret}
	if got := c.send(context.Background(), n, "evt_test", []byte(`{}`)); !got.Delivered {
		t.Fatal("2xx acknowledgment failed on body read")
	}
	if got := c.send(context.Background(), n, "msg_verification_test", []byte(`{}`)); got.Delivered {
		t.Fatal("verification accepted a broken body")
	}
}

func TestNotifyEmitAfterStorageRelease(t *testing.T) {
	s, x := notificationFixture(t)
	subscribeNotification(t, s, x)
	s.must(t, "PUT", "/resources/orders", map[string]string{"kind": "sqlite", "preset": "personal"}, s.key, 201)
	s.must(t, "POST", "/sqlite/orders/schema", map[string]string{"sql": "CREATE TABLE orders(id INTEGER PRIMARY KEY, item TEXT)"}, s.key, 200)
	tx, err := s.a.database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`SELECT id FROM sites WHERE id=$1 FOR UPDATE`, s.sid); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	s.must(t, "POST", "/sqlite/orders/tables/orders/rows", map[string]string{"item": "first"}, s.callers["alice"], 200)
	s.must(t, "POST", "/sqlite/orders/tables/orders/rows", map[string]string{"item": "second"}, s.callers["alice"], 200)
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
	e := s.a.sites.notifyEngine()
	for i := 0; i < cap(e.emitting); i++ {
		e.emitting <- struct{}{}
	}
	before := e.dropped.Load()
	s.a.sites.emitNotification(storageCall{siteID: s.sid}, "record.added", map[string]any{})
	if e.dropped.Load() != before+1 {
		t.Fatal("full emit queue did not drop")
	}
	for i := 0; i < cap(e.emitting); i++ {
		<-e.emitting
	}
}

func TestNotifyConnectorDeployScope(t *testing.T) {
	a := newConnectorApp(t)
	if err := a.sites.SetPasscodeKey(testPasscodeKey); err != nil {
		t.Fatal(err)
	}
	a.conn.SetEventsBackend(a.sites.EventsBackend())
	p := a.newPerson(t, "notify-deploy")
	u, err := db.GetUserByAPIKey(context.Background(), a.database, p.key)
	if err != nil {
		t.Fatal(err)
	}
	key := notifyRandom("test_", 24)
	if _, err = db.CreateAPIKey(context.Background(), a.database, u.ID, db.HashAPIKey(p.key), key, "deploy", db.KeyScopeDeploy, nil); err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"X-API-Key": key, "Content-Type": "application/json"}
	for _, method := range []string{"events/subscribe", "events/unsubscribe"} {
		r := a.do(t, "POST", "/mcp", jsonBody(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": map[string]any{}}), headers)
		if r.json(t)["error"].(map[string]any)["code"] != float64(mcp.EventCodeForbidden) {
			t.Fatal("deploy scope reached events")
		}
	}
	r := a.do(t, "POST", "/mcp", jsonBody(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}), headers)
	for _, raw := range r.json(t)["result"].(map[string]any)["tools"].([]any) {
		name := raw.(map[string]any)["name"]
		if name == "list_notifications" || name == "remove_notification" {
			t.Fatal("connector listed notifications for deploy key")
		}
	}
}

func notificationGrant(t *testing.T, s presetSite, name string) string {
	t.Helper()
	client := name + "-" + s.ownerID
	notificationSQL(t, s, `INSERT INTO oauth_clients(client_id,client_name,redirect_uris) VALUES($1,$2,ARRAY['https://example.test/cb'])`, client, name)
	t.Cleanup(func() { _, _ = s.a.database.Exec(`DELETE FROM oauth_clients WHERE client_id=$1`, client) })
	var id string
	if err := s.a.database.QueryRow(`INSERT INTO oauth_grants(user_id,client_id,scope,resource) VALUES($1,$2,'sites','x') RETURNING id`, s.ownerID, client).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestNotifyMCPAPIKeyTTL(t *testing.T) {
	notificationSettings(t, func(l *config.Limits) { l.NotifyDefaultTTL = time.Hour })
	s, x := notificationFixture(t)
	backend := s.a.sites.EventsBackend()
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
				caller := mcp.Caller{Owner: s.ownerID}
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
				n, err := db.GetNotification(context.Background(), s.a.database, result.ID)
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
	backend := s.a.sites.EventsBackend()
	ctx := context.Background()
	callers := []mcp.Caller{{Owner: s.ownerID, OAuthGrantID: notificationGrant(t, s, "app-a")}, {Owner: s.ownerID, OAuthGrantID: notificationGrant(t, s, "app-b")}, {Owner: s.ownerID}}
	req := mcp.EventSubscribeRequest{Name: "record.added", Site: "shop", CanonicalArgs: `{"site":"shop"}`, URL: x.server.URL, Secret: notifyTestSecret}
	ids := map[string]bool{}
	var results []mcp.EventSubscribeResult
	var secrets []string
	for i, caller := range callers {
		if i > 0 {
			var err error
			req.Secret, err = newNotifySecret()
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
		n, err := db.GetNotification(ctx, s.a.database, result.ID)
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
	if _, err := db.GetNotification(ctx, s.a.database, results[1].ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("unsubscribe did not remove its grant's subscription")
	}
	for _, i := range []int{0, 2} {
		if _, err := db.GetNotification(ctx, s.a.database, results[i].ID); err != nil {
			t.Fatal("unsubscribe removed another grant's subscription")
		}
	}
}

func TestNotifyPausedAndExpiredEmission(t *testing.T) {
	s, x := notificationFixture(t)
	id := subscribeNotification(t, s, x)
	ctx := context.Background()
	engine := s.a.sites.notifyEngine()
	call := storageCall{siteID: s.sid, ownerID: s.ownerID, siteName: "shop", resourceName: "orders", resource: storageResource{Kind: "kv"}}
	for _, state := range []string{"paused", "expired", "active", "suspended"} {
		t.Run(state, func(t *testing.T) {
			notificationSQL(t, s, `DELETE FROM site_events WHERE site_id=$1`, s.sid)
			storedState := state
			if state == "expired" {
				storedState = "active"
			}
			notificationSQL(t, s, `UPDATE site_notifications SET state=$2,expires_at=NULL WHERE id=$1`, id, storedState)
			if state == "expired" {
				notificationSQL(t, s, `UPDATE site_notifications SET state='active',expires_at=now()-interval '1 second' WHERE id=$1`, id)
			}
			want := state == "active" || state == "suspended"
			yes, err := db.HasNotifications(ctx, s.a.database, s.sid)
			if err != nil || yes != want {
				t.Fatal("subscription eligibility mismatch")
			}
			engine.minute = newRateLimiter(1, 0)
			engine.day = newRateLimiter(1, 0)
			s.a.sites.storeNotification(call, "record.added", map[string]any{"key": "one"})
			var count int
			if err := s.a.database.QueryRow(`SELECT count(*) FROM site_events WHERE site_id=$1`, s.sid).Scan(&count); err != nil || (count == 1) != want {
				t.Fatal("ineligible subscription stored an event")
			}
			if engine.minute.allow(s.sid) == want || engine.day.allow(s.sid) == want {
				t.Fatal("ineligible subscription spent the event budget")
			}
			// The database fan-out must also exclude paused and expired rows.
			if err := db.EmitSiteEvent(ctx, s.a.database, s.sid, notifyEventID(), "record.added", "orders", []byte(`{}`)); err != nil {
				t.Fatal(err)
			}
			if err := s.a.database.QueryRow(`SELECT count(*) FROM site_event_deliveries WHERE subscription_id=$1`, id).Scan(&count); err != nil || want && count != 2 || !want && count != 0 {
				t.Fatal("incorrect fan-out eligibility")
			}
		})
	}
}

func TestNotifyOwnerHeaderAllowlist(t *testing.T) {
	for _, name := range []string{"Authorization", "authorization", "X-API-Key", "x-token", "X-" + strings.Repeat("a", 62)} {
		if !validNotifyHeader(name, "test") {
			t.Errorf("rejected permitted header %s", name)
		}
	}
	s, x := notificationFixture(t)
	for _, name := range []string{"Forwarded", "X-Forwarded-For", "x-FoRwArDeD-Host", "X-Real-IP", "x-client-ip", "X-Originating-IP", "x-mcp-subscription-id", "X-", "X-" + strings.Repeat("a", 63), "X-token_name", "X-K", "X-ſ", "Proxy-Authorization"} {
		if validNotifyHeader(name, "test") {
			t.Errorf("accepted forbidden header %s", name)
		}
		r := notificationRequest(t, s, "POST", "", map[string]any{"url": x.server.URL, "events": []string{"record.added"}, "auth_header": map[string]string{"name": name, "value": "test"}}, 400)
		body := r.json(t)
		if body["code"] != "invalid_header" || !strings.Contains(body["error"].(string), "Authorization or X-") {
			t.Fatal("REST header error omitted the permitted format")
		}
	}
}

func saveWorkerNotification(t *testing.T, s presetSite, site, owner, url string) string {
	t.Helper()
	id := notifyRandom("sub_", 12)
	n := db.Notification{ID: id, SiteID: site, OwnerID: owner, Source: "owner", URL: url, Events: []string{"record.added"}, Secret: notifyTestSecret}
	if _, _, err := db.SaveNotification(context.Background(), s.a.database, n, 10, nil, 7, s.a.sites.notificationSecrets()); err != nil {
		t.Fatal(err)
	}
	return id
}
func emitWorkerNotification(t *testing.T, s presetSite, site string) {
	t.Helper()
	if err := db.EmitSiteEvent(context.Background(), s.a.database, site, notifyEventID(), "record.added", "orders", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
}
func checkDeferredNotification(t *testing.T, s presetSite, id string, attempts, window int) {
	t.Helper()
	var gotAttempts, gotWindow, failures int
	var state, category string
	var lease *time.Time
	var due time.Time
	if err := s.a.database.QueryRow(`SELECT d.attempts,n.window_attempts,n.window_failures,d.state,d.last_category,d.lease_until,d.next_attempt_at
 FROM site_event_deliveries d JOIN site_notifications n ON n.id=d.subscription_id WHERE n.id=$1 ORDER BY d.event_seq DESC LIMIT 1`, id).Scan(&gotAttempts, &gotWindow, &failures, &state, &category, &lease, &due); err != nil {
		t.Fatal(err)
	}
	if gotAttempts != attempts || gotWindow != window || failures != 0 || state != "pending" || category != "" || lease != nil || time.Until(due) < time.Second || time.Until(due) > 3*time.Second {
		t.Fatalf("throttle counted or failed an attempt: attempts=%d window=%d failures=%d state=%s category=%s lease=%v due=%v", gotAttempts, gotWindow, failures, state, category, lease, due)
	}
}

func TestNotifyOwnerConcurrency(t *testing.T) {
	notificationSettings(t, func(l *config.Limits) { l.NotifyOwnerConcurrency = 2 })
	s, x := notificationFixture(t)
	s.a.deploy(t, s.owner, "second")
	second := s.a.siteID(t, s.owner, "second")
	other := s.a.newPerson(t, "other-owner")
	s.a.deploy(t, other, "third")
	third := s.a.siteID(t, other, "third")
	otherID, _ := s.a.userID(t, other)
	one := saveWorkerNotification(t, s, s.sid, s.ownerID, x.server.URL)
	two := saveWorkerNotification(t, s, second, s.ownerID, x.server.URL)
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
	for _, site := range []string{s.sid, second} {
		emitWorkerNotification(t, s, site)
		go func() { done <- s.a.sites.deliverNotification(context.Background()) }()
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatal("delivery did not reach receiver")
		}
	}
	emitWorkerNotification(t, s, s.sid)
	emitWorkerNotification(t, s, third)
	if !s.a.sites.deliverNotification(context.Background()) {
		t.Fatal("over-cap claim was not processed")
	}
	checkDeferredNotification(t, s, one, 0, 1)
	if !s.a.sites.deliverNotification(context.Background()) || len(x.snapshot()) != 1 {
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
	engine := s.a.sites.notifyEngine()
	engine.ownersMu.Lock()
	defer engine.ownersMu.Unlock()
	if len(engine.owners) != 0 {
		t.Fatal("owner concurrency entries were not released")
	}
}

func TestNotifyURLRateAcrossSitesAndSubscriptions(t *testing.T) {
	notificationSettings(t, func(l *config.Limits) { l.NotifyPerURLPerMinute = 2 })
	s, x := notificationFixture(t)
	other := s.a.newPerson(t, "url-owner")
	s.a.deploy(t, other, "other")
	otherSite := s.a.siteID(t, other, "other")
	otherID, _ := s.a.userID(t, other)
	saveWorkerNotification(t, s, s.sid, s.ownerID, x.server.URL)
	saveWorkerNotification(t, s, s.sid, s.ownerID, x.server.URL)
	limited := saveWorkerNotification(t, s, otherSite, otherID, x.server.URL)
	saveWorkerNotification(t, s, otherSite, otherID, x.server.URL+"/different")
	engine := s.a.sites.notifyEngine()
	engine.perURL = (&SiteHandler{}).notifyEngine().perURL
	now := time.Now()
	engine.perURL.now = func() time.Time { return now }
	emitWorkerNotification(t, s, s.sid)
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
	if err := s.a.database.QueryRow(`SELECT d.attempts,n.window_attempts,n.window_failures FROM site_event_deliveries d JOIN site_notifications n ON n.id=d.subscription_id WHERE n.id=$1`, limited).Scan(&attempts, &window, &failures); err != nil || attempts != 1 || window != 1 || failures != 1 || len(x.snapshot()) != 4 {
		t.Fatal("throttled retry changed real attempt accounting")
	}
	now = now.Add(2 * time.Hour)
	engine.perURL.evictIdle(time.Hour)
	if len(engine.perURL.buckets) != 0 {
		t.Fatal("idle URL buckets were not cleaned up")
	}
}

func TestNotifyDeferredClaimLease(t *testing.T) {
	s, x := notificationFixture(t)
	id := subscribeNotification(t, s, x)
	ctx := context.Background()
	emitWorkerNotification(t, s, s.sid)
	old, err := db.ClaimNotificationDelivery(ctx, s.a.database, time.Minute, 5, 7, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	notificationSQL(t, s, `UPDATE site_event_deliveries SET lease_until=now()-interval '1 second' WHERE subscription_id=$1`, id)
	current, err := db.ClaimNotificationDelivery(ctx, s.a.database, time.Minute, 5, 7, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.DeferNotificationDelivery(ctx, s.a.database, old); err != nil {
		t.Fatal(err)
	}
	held, err := db.NotificationLeaseHeld(ctx, s.a.database, current)
	if err != nil || !held {
		t.Fatal("stale deferral released a current lease")
	}
	for i := 0; i < 2; i++ {
		if err := db.DeferNotificationDelivery(ctx, s.a.database, current); err != nil {
			t.Fatal(err)
		}
	}
	var attempts, window int
	if err := s.a.database.QueryRow(`SELECT d.attempts,n.window_attempts FROM site_event_deliveries d JOIN site_notifications n ON n.id=d.subscription_id WHERE n.id=$1`, id).Scan(&attempts, &window); err != nil || attempts != 1 || window != 1 {
		t.Fatal("deferral undid an attempt more than once")
	}
}

func TestNotifyURLMinuteBudget(t *testing.T) {
	notificationSettings(t, func(l *config.Limits) { l.NotifyPerURLPerMinute = 120 })
	engine := (&SiteHandler{}).notifyEngine()
	now := time.Now()
	engine.perURL.now = func() time.Time { return now }
	for minute := 0; minute < 2; minute++ {
		attempts := 0
		for tick := 0; tick < 240; tick++ {
			for worker := 0; worker < 20; worker++ {
				if engine.perURL.allow("https://receiver.example/events") {
					attempts++
				}
			}
			now = now.Add(250 * time.Millisecond)
		}
		if attempts != 120 {
			t.Fatalf("minute %d: got %d attempts, want 120", minute, attempts)
		}
	}
}
