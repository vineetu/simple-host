package datatest

import (
	"context"
	"errors"
	"testing"
	"time"

	notifyapp "github.com/vsriram/simple-host/host/data/notify"
)

// Secrets and custom headers are sealed at rest; rotation keeps the old
// secret; a connector refresh with the same secret reseals without
// rotating, a new secret rotates with a day's grace.
func TestNotificationStoredSecretsAndRefresh(t *testing.T) {
	s, x := notificationFixture(t)
	id := subscribeNotification(t, s, x)
	n := getNotification(t, s, id)
	assertNotificationSealed(t, n.Secret, notifyTestSecret)
	assertNotificationSealed(t, n.AuthHeaderValue, "Bearer receiver")
	notificationRequest(t, s, "POST", "/"+id+"/rotate-secret", nil, 200)
	if n := getNotification(t, s, id); openedNotification(t, s, id, n.PrevSecret) != notifyTestSecret {
		t.Fatal("rotation lost previous secret")
	}

	caller := notifyapp.Subscriber{Owner: s.OwnerID}
	req := mcpRequest(x)
	result := mustMCPSubscribe(t, s, caller, req)
	first := getNotification(t, s, result.ID)
	assertNotificationSealed(t, first.Secret, notifyTestSecret)
	mustMCPSubscribe(t, s, caller, req)
	if same := getNotification(t, s, result.ID); same.Secret == first.Secret || same.PrevSecret != "" {
		t.Fatal("same secret refresh rotated or reused nonce")
	}
	req.Secret, _ = notifyapp.NewSecret()
	mustMCPSubscribe(t, s, caller, req)
	changed := getNotification(t, s, result.ID)
	if openedNotification(t, s, result.ID, changed.PrevSecret) != notifyTestSecret || changed.PrevSecretUntil == nil || time.Until(*changed.PrevSecretUntil) < 23*time.Hour {
		t.Fatal("different secret did not rotate")
	}
	mustMCPSubscribe(t, s, caller, req)
	if again := getNotification(t, s, result.ID); !again.PrevSecretUntil.Equal(*changed.PrevSecretUntil) {
		t.Fatal("same secret refresh extended rotation grace")
	}
}

// The startup sweep seals legacy plain values once, leaves sealed rows
// alone, and skips (then later seals) a row another writer holds.
func TestSealNotificationSecretsSweep(t *testing.T) {
	s, x := notificationFixture(t)
	ctx := context.Background()
	id := subscribeNotification(t, s, x)
	untouched := getNotification(t, s, subscribeNotification(t, s, x))
	notificationSQL(t, s, `UPDATE site_notifications SET secret=$2,prev_secret=$2,auth_header_value=$3 WHERE id=$1`, id, notifyTestSecret, "Bearer legacy")
	seal := func(want int64) {
		t.Helper()
		if count, err := s.A.Data.SealNotificationSecrets(ctx, s.A.Database); err != nil || count != want {
			t.Fatalf("sweep sealed %d (%v), want %d", count, err, want)
		}
	}
	seal(1)
	after := getNotification(t, s, id)
	for _, value := range []string{after.Secret, after.PrevSecret} {
		assertNotificationSealed(t, value, notifyTestSecret)
		if openedNotification(t, s, id, value) != notifyTestSecret {
			t.Fatal("sweep changed secret")
		}
	}
	if openedNotification(t, s, id+":header", after.AuthHeaderValue) != "Bearer legacy" {
		t.Fatal("sweep changed header")
	}
	if other := getNotification(t, s, untouched.ID); other.Secret != untouched.Secret || other.AuthHeaderValue != untouched.AuthHeaderValue {
		t.Fatal("sweep changed a sealed row")
	}
	seal(0)
	notificationSQL(t, s, `UPDATE site_notifications SET secret=$2,prev_secret='',auth_header_value='old' WHERE id=$1`, id, notifyTestSecret)
	tx, err := s.A.Database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE site_notifications SET auth_header_value='concurrent-value' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	bounded, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if count, err := s.A.Data.SealNotificationSecrets(bounded, s.A.Database); err != nil || count != 0 {
		t.Fatal("sweep did not skip the locked row")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	seal(1)
	if openedNotification(t, s, id+":header", getNotification(t, s, id).AuthHeaderValue) != "concurrent-value" {
		t.Fatal("sweep lost the concurrent write")
	}
}

// Without the install key, new subscriptions are refused, stored secrets
// are never sent unreadable, a legacy plain row still delivers, and a
// connector can still unsubscribe.
func TestNotificationMissingKey(t *testing.T) {
	s, x := notificationFixture(t)
	id := subscribeNotification(t, s, x)
	sub := mustMCPSubscribe(t, s, notifyapp.Subscriber{Owner: s.OwnerID}, mcpRequest(x))
	setNotifyKey(t, s, "")
	if r := notificationRequest(t, s, "POST", "", map[string]any{"url": x.server.URL, "events": []string{"record.added"}}, 409); r.Code() != "notifications_not_enabled" {
		t.Fatalf("missing key: %s", r.Body)
	}
	notificationRequest(t, s, "POST", "/"+id+"/rotate-secret", nil, 409)
	if notificationRequest(t, s, "GET", "", nil, 200).JSON(t)["limits"].(map[string]any)["enabled"] != false {
		t.Fatal("limits advertise notifications without key")
	}
	var ee *notifyapp.EventError
	if _, err := mcpSubscribe(s, notifyapp.Subscriber{Owner: s.OwnerID}, mcpRequest(x)); !errors.As(err, &ee) || ee.Data["reason"] != "notifications_not_enabled" {
		t.Fatalf("missing-key connector error: %v", err)
	}
	before := len(x.snapshot())
	unsub := notifyapp.EventUnsubscribeRequest{Name: "record.added", Site: "shop", CanonicalArgs: `{"site":"shop"}`, URL: x.server.URL}
	if err := s.A.Data.Notifier().Unsubscribe(context.Background(), notifyapp.Subscriber{Owner: s.OwnerID}, unsub); err != nil || !notificationGone(s, sub.ID) {
		t.Fatalf("keyless unsubscribe: %v", err)
	}
	emitWorkerNotification(t, s, s.Sid)
	drainNotifications(t, s)
	if n := getNotification(t, s, id); n.LastResult != "secret_unreadable" || len(x.snapshot()) != before {
		t.Fatal("unreadable delivery not recorded safely")
	}
	notificationSQL(t, s, `UPDATE site_notifications SET secret=$2,auth_header_value='Bearer receiver' WHERE id=$1`, id, notifyTestSecret)
	emitWorkerNotification(t, s, s.Sid)
	drainNotifications(t, s)
	if len(x.snapshot()) != before+1 {
		t.Fatal("legacy worker delivery stopped without key")
	}
}

// A refresh or a rotation of a legacy plain row seals it without changing
// its values or grace period.
func TestNotificationLegacyWrites(t *testing.T) {
	s, x := notificationFixture(t)
	id := subscribeNotification(t, s, x)
	for _, operation := range []string{"refresh", "rotate"} {
		t.Run(operation, func(t *testing.T) {
			until := time.Now().Add(time.Hour).Truncate(time.Microsecond)
			notificationSQL(t, s, `UPDATE site_notifications SET secret=$2,prev_secret=$2,prev_secret_until=$3,auth_header_value='Bearer legacy' WHERE id=$1`, id, notifyTestSecret, until)
			if operation == "rotate" {
				notificationRequest(t, s, "POST", "/"+id+"/rotate-secret", nil, 200)
			} else if err := saveNotification(t, s, getNotification(t, s, id), nil); err != nil {
				t.Fatal(err)
			}
			n := getNotification(t, s, id)
			assertNotificationSealed(t, n.Secret, notifyTestSecret)
			assertNotificationSealed(t, n.AuthHeaderValue, "Bearer legacy")
			if openedNotification(t, s, id, n.PrevSecret) != notifyTestSecret || openedNotification(t, s, id+":header", n.AuthHeaderValue) != "Bearer legacy" {
				t.Fatal("write changed legacy values")
			}
			if operation == "refresh" && (n.PrevSecretUntil == nil || !n.PrevSecretUntil.Equal(until)) {
				t.Fatal("legacy same-secret refresh changed grace period")
			}
		})
	}
}

// A sealed secret copied into the header field (or back) does not open.
func TestNotificationPurposeBinding(t *testing.T) {
	s, x := notificationFixture(t)
	n := getNotification(t, s, subscribeNotification(t, s, x))
	for _, field := range []string{"secret", "auth_header_value"} {
		copied := n
		if field == "secret" {
			copied.Secret = n.AuthHeaderValue
		} else {
			copied.AuthHeaderValue = n.Secret
		}
		if r := s.A.Data.Notifier().SendWebhookForTest(context.Background(), s.A.Data.NotifyEnv(), copied, "evt_swap", []byte(`{}`)); r.Delivered || r.Result != "secret_unreadable" {
			t.Fatalf("%s: ciphertext from another purpose accepted", field)
		}
	}
	if len(x.snapshot()) != 0 {
		t.Fatal("copied ciphertext reached the receiver")
	}
}

// After a key change, rotating drops the unreadable previous secret, and a
// connector re-subscribe replaces its unreadable secret.
func TestNotificationAfterKeyChange(t *testing.T) {
	s, x := notificationFixture(t)
	id := subscribeNotification(t, s, x)
	notificationSQL(t, s, `UPDATE site_notifications SET auth_header_name='',auth_header_value='',prev_secret=secret,prev_secret_until=now()+interval '24 hours' WHERE id=$1`, id)
	caller := notifyapp.Subscriber{Owner: s.OwnerID}
	sub := mustMCPSubscribe(t, s, caller, mcpRequest(x))
	setNotifyKey(t, s, otherNotifyKey)
	out := notificationRequest(t, s, "POST", "/"+id+"/rotate-secret", nil, 200).JSON(t)
	n := getNotification(t, s, id)
	if out["previous_valid_until"] != nil || n.PrevSecret != "" || openedNotification(t, s, id, n.Secret) != out["secret"] {
		t.Fatal("rotation kept an unreadable secret or lost its replacement")
	}
	x.set(func(x *notificationReceiver) { x.keys = []string{out["secret"].(string)} })
	if r := s.A.Data.Notifier().SendWebhookForTest(context.Background(), s.A.Data.NotifyEnv(), n, "evt_rotated", []byte(`{}`)); !r.Delivered {
		t.Fatal("rotated notification could not deliver")
	}
	x.set(func(x *notificationReceiver) { x.keys = []string{notifyTestSecret} })
	mustMCPSubscribe(t, s, caller, mcpRequest(x))
	if m := getNotification(t, s, sub.ID); m.PrevSecret != "" || openedNotification(t, s, sub.ID, m.Secret) != notifyTestSecret {
		t.Fatal("re-subscribe kept an unreadable secret")
	}
}
