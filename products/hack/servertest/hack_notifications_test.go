package servertest

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

	notifyrepo "github.com/vsriram/simple-host/host/data/repo/notify"
)

// testPasscodeKey, notifyTestSecret, notifyRandom, receivedNotification,
// notificationReceiver/newNotificationReceiver/.snapshot()/.setStatus() and
// assertNotificationSealed below are duplicated from host/handler
// (passcode_test.go, notify_test.go, notify.go, notify_secrets_test.go;
// cleanup 3.2 slice 3): pure test scaffolding or pure helpers with no
// handler-private dependency once the client-swap itself moves behind
// SiteHandler.SetNotifyClientForTest (a test hook added for exactly this —
// notifyClient is unexported, so the receiver's old .client() method, which
// returned one, cannot cross the package boundary; this copy builds the
// cert pool instead and hands it to the new hook).
const testPasscodeKey = "q83vEjRWeJq83vEjRWeJq83vEjRWeJq83vEjRWeJq80=" // 32 bytes, tests only
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

type hackNotifyFixture struct {
	a                          *teamSiteApp
	x                          *notificationReceiver
	org, member, other         person
	slug, team, key, sid, base string
	own                        string // ?owner=<event>: act for the event's account
}

func newHackNotifyFixture(t *testing.T) hackNotifyFixture {
	t.Helper()
	isolateQueueTest(t)
	a := newTeamSiteApp(t)
	t.Cleanup(a.data.WaitNotifications)
	x := newNotificationReceiver(t)
	if err := a.access.SetPasscodeKey(testPasscodeKey); err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(x.server.Certificate())
	a.data.SetNotifyClientForTest(roots)
	a.conn.SetEventsBackend(a.data.EventsBackend())
	org, member, other := a.newPerson(t, "hn-org"), a.newPerson(t, "hn-member"), a.newPerson(t, "hn-other")
	slug := a.makeEvent(t, org)
	a.join(t, slug, member, org)
	a.join(t, slug, other, org)
	team, code := a.startTeam(t, slug, "Notifications", member)
	wantTS(t, "join teammate", a.api(t, "POST", "/v1/hack/events/"+slug+"/teams/join", map[string]string{"code": code}, other.key), 200, "")
	markReady(t, a.certDir, slug)
	key := member.key
	wantTS(t, "deploy", a.deployTeam(t, slug, team, key, "notify"), 201, "")
	return hackNotifyFixture{a, x, org, member, other, slug, team, key, hackSiteID(t, a, slug, team), "/v1/sites/" + team + "/notifications", "?owner=" + slug}
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
func (f hackNotifyFixture) gone(t *testing.T, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if _, err := notifyrepo.GetNotification(context.Background(), f.a.database, id); !errors.Is(err, sql.ErrNoRows) {
			t.Fatal("membership loss retained subscription")
		}
	}
}
func TestHackNotificationsMembershipAndSecretGate(t *testing.T) {
	f := newHackNotifyFixture(t)
	a := f.a
	id := f.create(t, f.base+f.own, f.member)
	// Losing approval permanently removes the subscription, even if restored.
	f.sql(t, `UPDATE event_members SET approval_status='pending' WHERE user_id=$1`, a.uid(t, f.member))
	f.gone(t, id)
	f.sql(t, `UPDATE event_members SET approval_status='approved' WHERE user_id=$1`, a.uid(t, f.member))
	id = f.create(t, f.base+f.own, f.member)
	wantTS(t, "leave", a.api(t, "POST", "/v1/hack/events/"+f.slug+"/teams/leave", nil, f.member.key), 200, "")
	f.gone(t, id)
	// No encryption key: no new subscriptions, no capability, removal still works.
	otherID := f.create(t, f.base+f.own, f.other)
	if err := a.access.SetPasscodeKey(""); err != nil {
		t.Fatal(err)
	}
	wantTS(t, "missing key", a.api(t, "POST", f.base+f.own, f.body(), f.other.key), 409, "notifications_not_enabled")
	wantTS(t, "missing key rotate", a.api(t, "POST", f.base+"/"+otherID+"/rotate-secret"+f.own, nil, f.other.key), 409, "notifications_not_enabled")
	client := a.registerClient(t, testRedirect)
	access := a.connect(t, f.other, client, testRedirect)["access_token"].(string)
	caps := a.rpc(t, access, "initialize", nil).json(t)["result"].(map[string]any)["capabilities"].(map[string]any)
	if _, ok := caps["events"]; ok {
		t.Fatal("events without key")
	}
	wantTS(t, "keyless remove", a.api(t, "DELETE", f.base+"/"+otherID+f.own, nil, f.other.key), 200, "")
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
	wantTS(t, "second site", a.deployTeam(t, slug2, team2, f.member.key, "second"), 201, "")
	id := f.create(t, f.base+f.own, f.member)
	otherID := f.create(t, f.base+"?owner="+slug2, f.member)
	n, err := notifyrepo.GetNotification(context.Background(), a.database, id)
	if err != nil {
		t.Fatal(err)
	}
	other, err := notifyrepo.GetNotification(context.Background(), a.database, otherID)
	if err != nil {
		t.Fatal(err)
	}
	if n.SiteID != f.sid || other.SiteID == f.sid {
		t.Fatal("REST selected the wrong event")
	}
	client := a.registerClient(t, testRedirect)
	access := a.connect(t, f.member, client, testRedirect)["access_token"].(string)
	args := map[string]any{"site": team2, "owner": slug2}
	_, out, failed := toolResultOf(t, a.rpc(t, access, "tools/call", map[string]any{"name": "list_notifications", "arguments": args}))
	if failed || len(out["notifications"].([]any)) != 1 || out["notifications"].([]any)[0].(map[string]any)["id"] != otherID {
		t.Fatal("tool resolved wrong event")
	}
	rpc := a.rpc(t, access, "events/subscribe", map[string]any{"name": "record.added", "arguments": args, "delivery": map[string]string{"mode": "webhook", "url": f.x.server.URL, "secret": notifyTestSecret}}).json(t)
	result, ok := rpc["result"].(map[string]any)
	if !ok {
		t.Fatal("MCP subscribe")
	}
	sub, err := notifyrepo.GetNotification(context.Background(), a.database, result["id"].(string))
	if err != nil || sub.SiteID != other.SiteID {
		t.Fatal("MCP resolved wrong event")
	}
	// The notification tool also preserves context when adding the deletion suffix.
	_, _, failed = toolResultOf(t, a.rpc(t, access, "tools/call", map[string]any{"name": "remove_notification", "arguments": map[string]any{"site": team2, "owner": slug2, "id": otherID}}))
	if failed {
		t.Fatal("remove")
	}
	f.gone(t, otherID)
	if _, err := notifyrepo.GetNotification(context.Background(), a.database, id); err != nil {
		t.Fatal("removed other event subscription")
	}
}
