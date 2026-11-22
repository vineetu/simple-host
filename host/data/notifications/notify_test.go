package notifications

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/server/servertest/datatest"

	"github.com/vsriram/simple-host/host/config"

	appstorage "github.com/vsriram/simple-host/host/data"
	notifyapp "github.com/vsriram/simple-host/host/data/notify"
	notificationsrepo "github.com/vsriram/simple-host/host/data/repo/notify"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
)

func TestNotifyWindowAndContinuousFailure(t *testing.T) {
	s, x := notificationFixture(t, notifyLimits(func(l *config.Limits) { l.NotifySuspendAttempts = 3 }))
	id := subscribeNotification(t, s, x)
	x.setStatus(500)
	// Old failures age out of the counting window.
	notificationSQL(t, s, `UPDATE site_notifications SET window_start=now()-interval '2 hours',window_attempts=2,window_failures=2 WHERE id=$1`, id)
	emitWorkerNotification(t, s, s.Sid)
	drainNotifications(t, s)
	if getNotification(t, s, id).State != "active" {
		t.Fatal("expired failures caused suspension")
	}
	if n := countRows(t, s, `SELECT window_attempts FROM site_notifications WHERE id=$1`, id); n != 1 {
		t.Fatal("expired window not reset")
	}
	notificationSQL(t, s, `UPDATE site_notifications SET failing_since=now()-interval '4 days' WHERE id=$1`, id)
	notificationSQL(t, s, `UPDATE site_event_deliveries SET next_attempt_at=now() WHERE subscription_id=$1`, id)
	drainNotifications(t, s)
	if getNotification(t, s, id).State != "suspended" {
		t.Fatal("continuous failure did not suspend")
	}
}

func TestNotifyRESTOwnershipAndValidation(t *testing.T) {
	s, x := notificationFixture(t)
	id := subscribeNotification(t, s, x)
	other := s.A.NewPerson(t, "notification-other")
	if r := s.A.At(t, "DELETE", rt.SiteDomain, "/v1/sites/shop/notifications/"+id, nil, other.Header()); r.Status != 404 {
		t.Fatal("other account could address subscription")
	}
	if r := s.A.At(t, "GET", rt.SiteDomain, "/v1/notifications", nil, other.Header()); r.Status != 200 || len(r.JSON(t)["notifications"].([]any)) != 0 {
		t.Fatal("account list leak")
	}
	if r := s.A.At(t, "GET", rt.SiteDomain, "/v1/sites/shop/notifications", nil, nil); r.Status != 401 {
		t.Fatalf("signed out: %d", r.Status)
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
		if r := notificationRequest(t, s, "POST", "", tc.body, 400); r.Code() != tc.code {
			t.Fatalf("want %s got %s", tc.code, r.Code())
		}
	}
	notificationRequest(t, s, "POST", "", map[string]any{"url": x.server.URL, "events": []string{"record.added"}, "storage": "missing"}, 404)
}

// Event and storage filters pick which subscriptions an event reaches.
func TestNotifyFilters(t *testing.T) {
	s, x := notificationFixture(t)
	n := model.Notification{ID: notifyapp.RandomID("sub_", 12), SiteID: s.Sid, OwnerID: s.OwnerID, Source: "owner", URL: x.server.URL, Events: []string{"record.added"}, Storage: "orders", Secret: notifyTestSecret}
	if err := saveNotification(t, s, n, nil); err != nil {
		t.Fatal(err)
	}
	for _, event := range []struct{ name, storage string }{{"record.added", "other"}, {"record.changed", "orders"}, {"record.added", "orders"}} {
		if err := notificationsrepo.EmitSiteEvent(context.Background(), s.A.Database, s.Sid, notifyapp.EventID(), event.name, event.storage, []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
	drainNotifications(t, s)
	if len(x.snapshot()) != 1 {
		t.Fatal("event and storage filters")
	}
}

// An offline, suspended or deleted site, a suspended owner or a site that
// changed hands sends nothing; a paused subscription holds its events.
func TestNotifyUnavailableHeldAndEventAccounts(t *testing.T) {
	s, x := notificationFixture(t)
	id := subscribeNotification(t, s, x)
	for _, c := range []struct{ name, set, reset string }{
		{"offline", `UPDATE sites SET offline_at=now() WHERE id=$1`, `UPDATE sites SET offline_at=NULL WHERE id=$1`},
		{"suspended", `UPDATE sites SET suspended_at=now() WHERE id=$1`, `UPDATE sites SET suspended_at=NULL WHERE id=$1`},
		{"deleted", `UPDATE sites SET deleted_at=now() WHERE id=$1`, `UPDATE sites SET deleted_at=NULL WHERE id=$1`},
		{"owner suspended", `UPDATE users SET suspended_at=now() WHERE id=(SELECT user_id FROM sites WHERE id=$1)`, `UPDATE users SET suspended_at=NULL WHERE id=(SELECT user_id FROM sites WHERE id=$1)`},
		{"transferred", `UPDATE sites SET user_id=(SELECT u.id FROM users u WHERE u.id<>sites.user_id LIMIT 1) WHERE id=$1`, ``},
	} {
		emitWorkerNotification(t, s, s.Sid)
		notificationSQL(t, s, c.set, s.Sid)
		drainNotifications(t, s)
		if state, category, _ := deliveryState(t, s, id); state != "skipped" || category != "site_unavailable" {
			t.Fatalf("%s: %s %s", c.name, state, category)
		}
		if c.reset == "" {
			notificationSQL(t, s, `UPDATE sites SET user_id=$2 WHERE id=$1`, s.Sid, s.OwnerID)
		} else {
			notificationSQL(t, s, c.reset, s.Sid)
		}
	}
	if len(x.snapshot()) != 0 {
		t.Fatal("unavailable site sent an event")
	}
	emitWorkerNotification(t, s, s.Sid)
	notificationRequest(t, s, "PATCH", "/"+id, map[string]bool{"paused": true}, 200)
	emitWorkerNotification(t, s, s.Sid)
	drainNotifications(t, s)
	if len(x.snapshot()) != 0 {
		t.Fatal("paused sent")
	}
	notificationRequest(t, s, "PATCH", "/"+id, map[string]bool{"paused": false}, 200)
	drainNotifications(t, s)
	if len(x.snapshot()) != 1 {
		t.Fatal("held event lost")
	}
	// An event account (Simple Hack) emits nothing and has no notifications.
	notificationSQL(t, s, `UPDATE users SET event_account=true WHERE id=$1`, s.OwnerID)
	c := appstorage.Call{SiteID: s.Sid, OwnerID: s.OwnerID, SiteName: "shop", ResourceName: "orders", Resource: model.Resource{Kind: "kv"}}
	s.A.Data.EmitNotification(c, "record.added", map[string]any{"key": "event-website"})
	drainNotifications(t, s)
	if len(x.snapshot()) != 1 {
		t.Fatal("event website emitted")
	}
	notificationRequest(t, s, "GET", "", nil, 404)
}

// Paused and expired subscriptions store no event and spend no budget.
func TestNotifyPausedAndExpiredEmission(t *testing.T) {
	s, x := notificationFixture(t)
	id := subscribeNotification(t, s, x)
	call := appstorage.Call{SiteID: s.Sid, OwnerID: s.OwnerID, SiteName: "shop", ResourceName: "orders", Resource: model.Resource{Kind: "kv"}}
	for _, state := range []string{"paused", "expired", "active"} {
		t.Run(state, func(t *testing.T) {
			notificationSQL(t, s, `DELETE FROM site_events WHERE site_id=$1`, s.Sid)
			stored := "active"
			if state == "paused" {
				stored = "paused"
			}
			notificationSQL(t, s, `UPDATE site_notifications SET state=$2,expires_at=NULL WHERE id=$1`, id, stored)
			if state == "expired" {
				notificationSQL(t, s, `UPDATE site_notifications SET expires_at=now()-interval '1 second' WHERE id=$1`, id)
			}
			want := state == "active"
			minute, day := httpx.NewRateLimiter(1, 0), httpx.NewRateLimiter(1, 0)
			s.A.Data.Notifier().EventCapsForTest(minute, day)
			s.A.Data.StoreNotification(call, "record.added", map[string]any{"key": "one"})
			if n := countRows(t, s, `SELECT count(*) FROM site_events WHERE site_id=$1`, s.Sid); (n == 1) != want {
				t.Fatal("ineligible subscription stored an event")
			}
			if minute.Allow(s.Sid) == want || day.Allow(s.Sid) == want {
				t.Fatal("ineligible subscription spent the event budget")
			}
			emitWorkerNotification(t, s, s.Sid)
			if n := countRows(t, s, `SELECT count(*) FROM site_event_deliveries WHERE subscription_id=$1`, id); want && n != 2 || !want && n != 0 {
				t.Fatalf("fan-out eligibility: %d", n)
			}
		})
	}
}

// Leases: a held delivery is not claimed twice, a stale worker cannot
// overwrite a newer lease, the cursor never passes an unresolved event,
// and expired subscriptions and old history are swept.
func TestNotifyLeaseCursorExpiryAndSweep(t *testing.T) {
	s, x := notificationFixture(t)
	id := subscribeNotification(t, s, x)
	ctx := context.Background()
	claim := func() (model.NotificationDelivery, error) {
		return notificationsrepo.ClaimNotificationDelivery(ctx, s.A.Database, time.Minute, 5, 7, time.Hour)
	}
	finish := func(d model.NotificationDelivery) {
		t.Helper()
		if _, err := notificationsrepo.FinishNotificationDelivery(ctx, s.A.Database, d, "delivered", "ok", 200, 1, time.Now(), time.Hour, 100, 95, 72*time.Hour); err != nil {
			t.Fatal(err)
		}
	}
	emitWorkerNotification(t, s, s.Sid)
	emitWorkerNotification(t, s, s.Sid)
	a, err := claim()
	if err != nil {
		t.Fatal(err)
	}
	b, err := claim()
	if err != nil || a.Seq == b.Seq {
		t.Fatalf("second claim: %v", err)
	}
	if _, err := claim(); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("lease did not hold delivery")
	}
	finish(b)
	if cursor, err := notificationsrepo.NotificationCursor(ctx, s.A.Database, id, s.Sid); err != nil || cursor >= a.Seq {
		t.Fatal("cursor passed unresolved earlier event")
	}
	notificationSQL(t, s, `UPDATE site_event_deliveries SET lease_until=now()-interval '1 second' WHERE event_seq=$1`, a.Seq)
	newer, err := claim()
	if err != nil {
		t.Fatal(err)
	}
	finish(a)
	var state string
	if err := s.A.Database.QueryRow(`SELECT state FROM site_event_deliveries WHERE event_seq=$1`, a.Seq).Scan(&state); err != nil || state != "pending" {
		t.Fatal("stale worker overwrote newer lease")
	}
	finish(newer)
	if cursor, err := notificationsrepo.NotificationCursor(ctx, s.A.Database, id, s.Sid); err != nil || cursor != b.Seq {
		t.Fatal("watermark did not advance")
	}
	emitWorkerNotification(t, s, s.Sid)
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
	if err := notificationsrepo.SweepNotifications(ctx, s.A.Database, 7); err != nil {
		t.Fatal(err)
	}
	if countRows(t, s, `SELECT (SELECT count(*) FROM site_events WHERE site_id=$1)+(SELECT count(*) FROM site_event_deliveries WHERE subscription_id=$2)`, s.Sid, id) != 0 || !notificationGone(s, id) {
		t.Fatal("history sweep did not cascade")
	}
}

// A worker that lost its lease does not send; crashed claims count against
// the send budget and end failed.
func TestNotifyCrashBudgetAndStaleWorker(t *testing.T) {
	s, x := notificationFixture(t)
	id := subscribeNotification(t, s, x)
	ctx := context.Background()
	emitWorkerNotification(t, s, s.Sid)
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
	if _, err := notificationsrepo.ClaimNotificationDelivery(ctx, s.A.Database, time.Minute, 2, 7, time.Hour); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("crashes exceeded send budget")
	}
	var attempts, failures int
	var state string
	if err := s.A.Database.QueryRow(`SELECT d.state,n.window_attempts,n.window_failures FROM site_notifications n JOIN site_event_deliveries d ON d.subscription_id=n.id WHERE n.id=$1`, id).Scan(&state, &attempts, &failures); err != nil || state != "failed" || attempts != 2 || failures != 2 {
		t.Fatal("abandoned claims were not accounted")
	}
}

// A stale worker deferring its old claim does not release the current
// lease, and a deferral undoes its attempt once.
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
	if held, err := notificationsrepo.NotificationLeaseHeld(ctx, s.A.Database, current); err != nil || !held {
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

// Expired history is neither claimed nor replayed; startup sweeps it; a
// deleted site's subscriptions are not listed.
func TestNotifyHistoryAndDeletedListings(t *testing.T) {
	s, x := notificationFixture(t)
	id := subscribeNotification(t, s, x)
	ctx := context.Background()
	emitWorkerNotification(t, s, s.Sid)
	notificationSQL(t, s, `UPDATE site_events SET created_at=now()-interval '8 days' WHERE site_id=$1`, s.Sid)
	if _, err := notificationsrepo.ClaimNotificationDelivery(ctx, s.A.Database, time.Minute, 5, 7, time.Hour); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("claimed expired history")
	}
	notificationSQL(t, s, `DELETE FROM site_event_deliveries WHERE subscription_id=$1`, id)
	n := getNotification(t, s, id)
	n.Secret = notifyTestSecret
	zero := int64(0)
	if err := saveNotification(t, s, n, &zero); err != nil {
		t.Fatal(err)
	}
	if countRows(t, s, `SELECT count(*) FROM site_event_deliveries WHERE subscription_id=$1`, id) != 0 {
		t.Fatal("replayed expired history")
	}
	run, cancel := context.WithCancel(ctx)
	s.A.Data.StartNotifications(run)
	cancel()
	s.A.Data.WaitNotifications()
	if countRows(t, s, `SELECT count(*) FROM site_events WHERE site_id=$1`, s.Sid) != 0 {
		t.Fatal("startup did not sweep history")
	}
	notificationSQL(t, s, `UPDATE sites SET deleted_at=now() WHERE id=$1`, s.Sid)
	for _, site := range []string{"", s.Sid} {
		if ns, err := notificationsSvc(s.A.Data).ListNotifications(ctx, s.OwnerID, site); err != nil || len(ns) != 0 {
			t.Fatal("listed deleted site's notifications")
		}
	}
}

type cancellingNoticeMailer struct{ cancel context.CancelFunc }

func (cancellingNoticeMailer) SendSignInCode(string, string, string) error { return nil }
func (cancellingNoticeMailer) SendNotice(string, string, string) error {
	return errors.New("smtp down")
}
func (m cancellingNoticeMailer) SendNoticeCtx(ctx context.Context, _, _, _ string) error {
	m.cancel()
	return ctx.Err()
}

// A suspension notice that fails to send is retried by the next sweep, once.
func TestNotifySuspensionEmailRetry(t *testing.T) {
	s, x := notificationFixture(t)
	id := subscribeNotification(t, s, x)
	notificationSQL(t, s, `UPDATE site_notifications SET state='suspended' WHERE id=$1`, id)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.A.Data.Mailer = cancellingNoticeMailer{cancel: cancel}
	s.A.Data.SweepNotifications(ctx)
	var emailed sql.NullTime
	if err := s.A.Database.QueryRow(`SELECT suspend_emailed_at FROM site_notifications WHERE id=$1`, id).Scan(&emailed); err != nil || emailed.Valid {
		t.Fatal("failed email was not released")
	}
	mail := &mailbox{}
	s.A.Data.Mailer = mail
	s.A.Data.SweepNotifications(context.Background())
	s.A.Data.SweepNotifications(context.Background())
	if mail.to(s.Owner.Email) != 1 {
		t.Fatal("email sweep did not retry exactly once")
	}
}

// Shutdown drains emits already accepted and admits no new ones.
func TestNotificationOwnerDrainsAcceptedEmits(t *testing.T) {
	for _, parentCancel := range []bool{false, true} {
		t.Run(fmt.Sprint(parentCancel), func(t *testing.T) {
			s, _ := notificationFixture(t)
			e := s.A.Data.Notifier()
			owner := s.A.Data.NotificationOwner()
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(func() { cancel(); owner.Stop(); owner.Wait() })
			owner.Start(ctx)
			finished := make(chan struct{})
			e.Emit(func(ctx context.Context) {
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

// Connector (MCP Events) subscriptions, through the engine's Subscribe.

// A refresh with a new secret rotates it; the TTL has a floor; the
// challenge budget per host is capped; an endpoint reflecting the body
// fails verification.
func TestNotifyMCPRotationTTLAndChallengeLimit(t *testing.T) {
	s, x := notificationFixture(t)
	caller := notifyapp.Subscriber{Owner: s.OwnerID}
	short := time.Second
	req := mcpRequest(x)
	req.TTLSet, req.TTL = true, &short
	first := mustMCPSubscribe(t, s, caller, req)
	if first.RefreshBefore == nil || time.Until(*first.RefreshBefore) < 4*time.Minute {
		t.Fatal("minimum TTL")
	}
	secret, err := notifyapp.NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	req.Secret = secret
	if refresh := mustMCPSubscribe(t, s, caller, req); refresh.ID != first.ID {
		t.Fatal("rotating refresh")
	}
	n := getNotification(t, s, first.ID)
	if openedNotification(t, s, n.ID, n.PrevSecret) != notifyTestSecret || openedNotification(t, s, n.ID, n.Secret) != secret {
		t.Fatal("MCP refresh did not rotate secret")
	}
	notificationRequest(t, s, "POST", "/"+first.ID+"/test", nil, 409)
	x.set(func(x *notificationReceiver) { x.reflectBody = true })
	req.Secret, req.URL = notifyTestSecret, x.server.URL+"/reflect"
	var ee *notifyapp.EventError
	if _, err := mcpSubscribe(s, caller, req); !errors.As(err, &ee) || ee.Data["reason"] != "challenge_failed" {
		t.Fatal("reflection passed verification")
	}
	x.set(func(x *notificationReceiver) { x.reflectBody = false; x.echo = false })
	for i := 0; i < 8; i++ {
		req.URL = x.server.URL + fmt.Sprintf("/fail-%d", i)
		if _, err := mcpSubscribe(s, caller, req); !errors.As(err, &ee) || ee.Data["reason"] != "challenge_failed" {
			t.Fatalf("challenge %d: %v", i, err)
		}
	}
	req.URL = x.server.URL + "/limited"
	if _, err := mcpSubscribe(s, caller, req); !errors.As(err, &ee) || ee.Code != notifyapp.EventCodeResourceExhausted {
		t.Fatalf("host challenge cap: %v", err)
	}
}

// A site replaced under the same name needs fresh verification, and a
// subscription is never moved to another site by an upsert.
func TestNotifyMCPSiteReplacement(t *testing.T) {
	s, x := notificationFixture(t)
	c := notifyapp.Subscriber{Owner: s.OwnerID}
	req := mcpRequest(x)
	first := mustMCPSubscribe(t, s, c, req)
	replacement := s.A.Deploy(t, s.Owner, "replacement")
	notificationSQL(t, s, `UPDATE sites SET name='oldshop' WHERE id=$1`, s.Sid)
	notificationSQL(t, s, `UPDATE sites SET name='shop' WHERE id=$1`, replacement)
	count := len(x.snapshot())
	next := mustMCPSubscribe(t, s, c, req)
	if next.Refreshed || next.ID != first.ID || len(x.snapshot()) != count+1 {
		t.Fatal("new site did not require fresh verification")
	}
	n := getNotification(t, s, next.ID)
	if n.SiteID != replacement {
		t.Fatal("subscription remained on old site")
	}
	n.SiteID, n.Secret = s.Sid, notifyTestSecret
	if err := saveNotification(t, s, n, nil); !errors.Is(err, model.ErrNotificationSite) {
		t.Fatal("upsert crossed sites")
	}
}

func notificationGrant(t *testing.T, s datatest.Shop, name string) string {
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

// An API key's subscription expires by the default TTL at most; an OAuth
// grant may ask for no expiry.
func TestNotifyMCPAPIKeyTTL(t *testing.T) {
	s, x := notificationFixture(t, notifyLimits(func(l *config.Limits) { l.NotifyDefaultTTL = time.Hour }))
	grant := notificationGrant(t, s, "ttl")
	long := 2 * time.Hour
	for _, tc := range []struct {
		name       string
		ttl        *time.Duration
		key, oauth time.Duration // 0: no expiry
	}{
		{name: "null", ttl: nil, key: time.Hour, oauth: 0},
		{name: "long", ttl: &long, key: time.Hour, oauth: long},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, oauth := range []bool{false, true} {
				caller, want := notifyapp.Subscriber{Owner: s.OwnerID}, tc.key
				if oauth {
					caller.GrantID, want = grant, tc.oauth
				}
				req := mcpRequest(x)
				req.TTLSet, req.TTL = true, tc.ttl
				before := time.Now()
				result := mustMCPSubscribe(t, s, caller, req)
				if want == 0 {
					if result.RefreshBefore != nil {
						t.Fatal("OAuth null TTL was not granted")
					}
				} else if result.RefreshBefore == nil || result.RefreshBefore.Before(before.Add(want)) || result.RefreshBefore.After(time.Now().Add(want)) {
					t.Fatalf("oauth=%v: refreshBefore does not reflect granted TTL", oauth)
				}
				if n := getNotification(t, s, result.ID); (n.ExpiresAt == nil) != (result.RefreshBefore == nil) {
					t.Fatal("stored expiry differs from grant")
				}
			}
		})
	}
}

// Two apps (grants) subscribing the same endpoint get separate
// subscriptions; neither replaces or removes the other's.
func TestNotifyMCPGrantIdentity(t *testing.T) {
	s, x := notificationFixture(t)
	callers := []notifyapp.Subscriber{{Owner: s.OwnerID, GrantID: notificationGrant(t, s, "app-a")}, {Owner: s.OwnerID, GrantID: notificationGrant(t, s, "app-b")}, {Owner: s.OwnerID}}
	req := mcpRequest(x)
	var ids, secrets []string
	for i, caller := range callers {
		if i > 0 {
			req.Secret, _ = notifyapp.NewSecret()
		}
		result := mustMCPSubscribe(t, s, caller, req)
		for _, id := range ids {
			if id == result.ID {
				t.Fatal("different grants shared a subscription")
			}
		}
		ids, secrets = append(ids, result.ID), append(secrets, req.Secret)
	}
	for i, id := range ids {
		if n := getNotification(t, s, id); openedNotification(t, s, n.ID, n.Secret) != secrets[i] || n.PrevSecret != "" {
			t.Fatal("another app replaced a subscription secret")
		}
	}
	unsub := notifyapp.EventUnsubscribeRequest{Name: req.Name, Site: req.Site, CanonicalArgs: req.CanonicalArgs, URL: req.URL}
	if err := s.A.Data.Notifier().Unsubscribe(context.Background(), callers[1], unsub); err != nil {
		t.Fatal(err)
	}
	if !notificationGone(s, ids[1]) || notificationGone(s, ids[0]) || notificationGone(s, ids[2]) {
		t.Fatal("unsubscribe removed the wrong grant's subscription")
	}
}
