package datatest

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/access/accesshttp"
	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/data/datahttp"
	notifyapp "github.com/vsriram/simple-host/host/data/notify"
	notificationsrepo "github.com/vsriram/simple-host/host/data/repo/notify"
	"github.com/vsriram/simple-host/host/mcp"
	"github.com/vsriram/simple-host/host/server/servertest"
	"github.com/vsriram/simple-host/host/sites"
)

func notificationKey(t *testing.T) *accesshttp.AccessHandler {
	t.Helper()
	h := &accesshttp.AccessHandler{Hosts: &sites.Hosts{}}
	if err := h.SetPasscodeKey(servertest.TestPasscodeKey); err != nil {
		t.Fatal(err)
	}
	return h
}
func assertNotificationSealed(t *testing.T, stored, plain string) {
	t.Helper()
	if !strings.HasPrefix(stored, "enc1:") || strings.Contains(stored, plain) {
		t.Fatal("notification value was not sealed")
	}
}
func openedNotification(t *testing.T, s servertest.StorageTestSite, id, stored string) string {
	t.Helper()
	plain, err := s.A.Data.NotifyEnv().OpenSecret(id, stored)
	if err != nil {
		t.Fatal("could not open stored notification value")
	}
	return plain
}

func TestNotificationStoredSecretsAndRefresh(t *testing.T) {
	s, x := notificationFixture(t)
	id := subscribeNotification(t, s, x)
	ctx := context.Background()
	n, err := notificationsSvc(s.A.Data).GetNotification(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	assertNotificationSealed(t, n.Secret, notifyTestSecret)
	assertNotificationSealed(t, n.AuthHeaderValue, "Bearer receiver")
	if n.PrevSecret != "" {
		t.Fatal("new row has previous secret")
	}
	notificationRequest(t, s, "POST", "/"+id+"/rotate-secret", nil, 200)
	n, err = notificationsSvc(s.A.Data).GetNotification(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	assertNotificationSealed(t, n.PrevSecret, notifyTestSecret)
	if openedNotification(t, s, id, n.PrevSecret) != notifyTestSecret {
		t.Fatal("rotation lost previous secret")
	}

	backend := s.A.Data.EventsBackend()
	req := mcp.EventSubscribeRequest{Name: "record.added", Site: "shop", CanonicalArgs: `{"site":"shop"}`, URL: x.server.URL, Secret: notifyTestSecret}
	caller := mcp.Caller{Owner: s.OwnerID}
	result, err := backend.Subscribe(ctx, caller, req)
	if err != nil {
		t.Fatal(err)
	}
	first, err := notificationsSvc(s.A.Data).GetNotification(ctx, result.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertNotificationSealed(t, first.Secret, notifyTestSecret)
	if _, err = backend.Subscribe(ctx, caller, req); err != nil {
		t.Fatal(err)
	}
	same, err := notificationsSvc(s.A.Data).GetNotification(ctx, result.ID)
	if err != nil {
		t.Fatal(err)
	}
	if same.Secret == first.Secret || same.PrevSecret != "" || same.PrevSecretUntil != nil {
		t.Fatal("same secret refresh rotated or reused nonce")
	}
	req.Secret, err = notifyapp.NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = backend.Subscribe(ctx, caller, req); err != nil {
		t.Fatal(err)
	}
	changed, err := notificationsSvc(s.A.Data).GetNotification(ctx, result.ID)
	if err != nil {
		t.Fatal(err)
	}
	if openedNotification(t, s, result.ID, changed.PrevSecret) != notifyTestSecret || changed.PrevSecretUntil == nil || time.Until(*changed.PrevSecretUntil) < 23*time.Hour {
		t.Fatal("different secret did not rotate")
	}
	if _, err = backend.Subscribe(ctx, caller, req); err != nil {
		t.Fatal(err)
	}
	refreshed, err := notificationsSvc(s.A.Data).GetNotification(ctx, result.ID)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.PrevSecret != changed.PrevSecret || !refreshed.PrevSecretUntil.Equal(*changed.PrevSecretUntil) {
		t.Fatal("same secret refresh extended rotation grace")
	}
}

func TestSealNotificationSecretsSweep(t *testing.T) {
	s, x := notificationFixture(t)
	ctx := context.Background()
	id := subscribeNotification(t, s, x)
	untouched := subscribeNotification(t, s, x)
	before, err := notificationsSvc(s.A.Data).GetNotification(ctx, untouched)
	if err != nil {
		t.Fatal(err)
	}
	notificationSQL(t, s, `UPDATE site_notifications SET secret=$2,prev_secret=$2,auth_header_value=$3 WHERE id=$1`, id, notifyTestSecret, "Bearer legacy")
	count, err := s.A.Data.SealNotificationSecrets(ctx, s.A.Database)
	if err != nil || count != 1 {
		t.Fatal("sweep did not seal one legacy row")
	}
	after, err := notificationsSvc(s.A.Data).GetNotification(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{after.Secret, after.PrevSecret} {
		assertNotificationSealed(t, value, notifyTestSecret)
		if openedNotification(t, s, id, value) != notifyTestSecret {
			t.Fatal("sweep changed secret")
		}
	}
	assertNotificationSealed(t, after.AuthHeaderValue, "Bearer legacy")
	if openedNotification(t, s, id+":header", after.AuthHeaderValue) != "Bearer legacy" {
		t.Fatal("sweep changed header")
	}
	other, err := notificationsSvc(s.A.Data).GetNotification(ctx, untouched)
	if err != nil || other.Secret != before.Secret || other.AuthHeaderValue != before.AuthHeaderValue || other.PrevSecret != "" {
		t.Fatal("sweep changed sealed or empty fields")
	}
	if count, err = s.A.Data.SealNotificationSecrets(ctx, s.A.Database); err != nil || count != 0 {
		t.Fatal("rerun was not a no-op")
	}
	// A concurrent writer holds the row while the sweep skips it without waiting.
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
	if count, err := s.A.Data.SealNotificationSecrets(ctx, s.A.Database); err != nil || count != 1 {
		t.Fatal("next sweep did not seal the skipped row")
	}
	concurrent, err := notificationsSvc(s.A.Data).GetNotification(ctx, id)
	if err != nil || openedNotification(t, s, id+":header", concurrent.AuthHeaderValue) != "concurrent-value" {
		t.Fatal("sweep lost the concurrent write")
	}
	if count, err := s.A.Data.SealNotificationSecrets(ctx, s.A.Database); err != nil || count != 0 {
		t.Fatal("rerun was not a no-op after the concurrent write")
	}
	// Only a legacy header also qualifies; sealed secrets stay byte-for-byte intact.
	notificationSQL(t, s, `UPDATE site_notifications SET secret=$2,prev_secret='',auth_header_value='legacy-header' WHERE id=$1`, id, after.Secret)
	if count, err := s.A.Data.SealNotificationSecrets(ctx, s.A.Database); err != nil || count != 1 {
		t.Fatal("header-only row was not sealed")
	}
	final, err := notificationsSvc(s.A.Data).GetNotification(ctx, id)
	if err != nil || final.Secret != after.Secret || final.PrevSecret != "" {
		t.Fatal("mixed row changed sealed or empty values")
	}
}

func TestNotificationMissingKey(t *testing.T) {
	s, x := notificationFixture(t)
	id := subscribeNotification(t, s, x)
	if err := s.A.Access.SetPasscodeKey(""); err != nil {
		t.Fatal(err)
	}
	response := notificationRequest(t, s, "POST", "", map[string]any{"url": x.server.URL, "events": []string{"record.added"}}, 409).JSON(t)
	if response["code"] != "notifications_not_enabled" || !strings.Contains(response["error"].(string), "PASSCODE_ENC_KEY") {
		t.Fatal("missing-key REST error unclear")
	}
	notificationRequest(t, s, "POST", "/"+id+"/rotate-secret", nil, 409)
	limits := notificationRequest(t, s, "GET", "", nil, 200).JSON(t)["limits"].(map[string]any)
	if limits["enabled"] != false {
		t.Fatal("limits advertise notifications without key")
	}
	_, err := s.A.Data.EventsBackend().Subscribe(context.Background(), mcp.Caller{Owner: s.OwnerID}, mcp.EventSubscribeRequest{})
	var eventErr *mcp.EventError
	if !errors.As(err, &eventErr) || eventErr.Data["reason"] != "notifications_not_enabled" {
		t.Fatal("missing-key MCP error unclear")
	}
	emitWorkerNotification(t, s, s.Sid)
	drainNotifications(t, s)
	n, err := notificationsSvc(s.A.Data).GetNotification(context.Background(), id)
	if err != nil || n.LastResult != "secret_unreadable" || len(x.snapshot()) != 0 {
		t.Fatal("unreadable delivery not recorded safely")
	}
	notificationSQL(t, s, `UPDATE site_notifications SET secret=$2,auth_header_value='Bearer receiver' WHERE id=$1`, id, notifyTestSecret)
	emitWorkerNotification(t, s, s.Sid)
	drainNotifications(t, s)
	if len(x.snapshot()) != 1 {
		t.Fatal("legacy worker delivery stopped without key")
	}
}

func TestNotificationLegacyWrites(t *testing.T) {
	s, x := notificationFixture(t)
	ctx := context.Background()
	id := subscribeNotification(t, s, x)
	for _, operation := range []string{"refresh", "rotate"} {
		t.Run(operation, func(t *testing.T) {
			until := time.Now().Add(time.Hour).Truncate(time.Microsecond)
			notificationSQL(t, s, `UPDATE site_notifications SET secret=$2,prev_secret=$2,prev_secret_until=$3,auth_header_value='Bearer legacy' WHERE id=$1`, id, notifyTestSecret, until)
			if operation == "rotate" {
				notificationRequest(t, s, "POST", "/"+id+"/rotate-secret", nil, 200)
			} else {
				n, err := notificationsSvc(s.A.Data).GetNotification(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				if _, _, err := notifyapp.New(s.A.Database).SaveNotification(ctx, n, 10, nil, 7, notificationSecrets(s.A.Data)); err != nil {
					t.Fatal(err)
				}
			}
			n, err := notificationsSvc(s.A.Data).GetNotification(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			assertNotificationSealed(t, n.Secret, notifyTestSecret)
			assertNotificationSealed(t, n.PrevSecret, notifyTestSecret)
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

func TestNotificationKeyDoesNotEnableSharedOriginPasscodes(t *testing.T) {
	h := notificationKey(t)
	servertest.WithPasscodeLimits(t, func(l *config.Limits) { l.SitePasscodes = true }, &h.Limits)
	if !h.Hosts.Addressing().SharedOrigin() || !(&datahttp.DataHandler{PasscodeKeys: h.PasscodeKeys()}).NotificationsReady() || h.PasscodesReady() {
		t.Fatal("notification key enabled shared-origin passcodes")
	}
}

func TestNotificationPurposeBinding(t *testing.T) {
	s, x := notificationFixture(t)
	id := subscribeNotification(t, s, x)
	n, err := notificationsSvc(s.A.Data).GetNotification(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"secret", "auth_header_value"} {
		t.Run(field, func(t *testing.T) {
			copied := n
			if field == "secret" {
				copied.Secret = n.AuthHeaderValue
			} else {
				copied.AuthHeaderValue = n.Secret
			}
			if r := s.A.Data.Notifier().SendWebhookForTest(context.Background(), s.A.Data.NotifyEnv(), copied, "evt_swap", []byte(`{}`)); r.Delivered || r.Result != "secret_unreadable" {
				t.Fatal("ciphertext from a different purpose was accepted")
			}
		})
	}
	if len(x.snapshot()) != 0 {
		t.Fatal("copied ciphertext reached the receiver")
	}
}

func TestNotificationRotateUnreadableSecret(t *testing.T) {
	s, x := notificationFixture(t)
	id := subscribeNotification(t, s, x)
	// No custom header: changing the key also makes a stored header unreadable.
	notificationSQL(t, s, `UPDATE site_notifications SET auth_header_name='',auth_header_value='',prev_secret=secret,prev_secret_until=now()+interval '24 hours' WHERE id=$1`, id)
	if err := s.A.Access.SetPasscodeKey(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))); err != nil {
		t.Fatal(err)
	}
	out := notificationRequest(t, s, "POST", "/"+id+"/rotate-secret", nil, 200).JSON(t)
	if out["previous_valid_until"] != nil {
		t.Fatal("unreadable previous secret has a grace period")
	}
	n, err := notificationsSvc(s.A.Data).GetNotification(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if n.PrevSecret != "" || n.PrevSecretUntil != nil || openedNotification(t, s, id, n.Secret) != out["secret"] {
		t.Fatal("rotation retained an unreadable secret or lost its replacement")
	}
	x.keys = []string{out["secret"].(string)}
	if r := s.A.Data.Notifier().SendWebhookForTest(context.Background(), s.A.Data.NotifyEnv(), n, "evt_rotated", []byte(`{}`)); !r.Delivered {
		t.Fatal("rotated notification could not deliver")
	}
}

func TestNotificationMissingKeyMCPUnsubscribe(t *testing.T) {
	s, x := notificationFixture(t)
	backend := s.A.Data.EventsBackend()
	caller := mcp.Caller{Owner: s.OwnerID, APIKey: s.Key["X-API-Key"]}
	sub, err := backend.Subscribe(context.Background(), caller, mcp.EventSubscribeRequest{
		Name: "record.added", Site: "shop", CanonicalArgs: `{"site":"shop"}`, URL: x.server.URL, Secret: notifyTestSecret,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.A.Access.SetPasscodeKey(""); err != nil {
		t.Fatal(err)
	}
	server := mcp.NewServer(mcp.Config{Events: backend})
	for i := 0; i < 2; i++ {
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "events/unsubscribe", "params": map[string]any{
			"name": "record.added", "arguments": map[string]any{"site": "shop"}, "delivery": map[string]any{"url": x.server.URL},
		}})
		req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
		req.Header.Set("MCP-Protocol-Version", "2025-11-25")
		req = req.WithContext(mcp.WithCaller(req.Context(), caller))
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		var result map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result["error"] != nil || result["result"] == nil {
			t.Fatal("keyless unsubscribe failed")
		}
	}
	if _, err := notificationsSvc(s.A.Data).GetNotification(context.Background(), sub.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("keyless unsubscribe did not remove the subscription")
	}
}

func TestSealNotificationSecretsTimeout(t *testing.T) {
	s, x := notificationFixture(t)
	id := subscribeNotification(t, s, x)
	notificationSQL(t, s, `UPDATE site_notifications SET secret=$2 WHERE id=$1`, id, notifyTestSecret)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	seal := func(id, plain string) (string, error) {
		<-ctx.Done()
		return s.A.Data.NotifyEnv().SealSecret(id, plain)
	}
	if count, err := notificationsrepo.SealNotificationSecrets(ctx, s.A.Database, seal); err == nil || count != 0 {
		t.Fatal("expired sweep did not stop")
	}
	n, err := notificationsSvc(s.A.Data).GetNotification(context.Background(), id)
	if err != nil || n.Secret != notifyTestSecret {
		t.Fatal("expired sweep changed the row")
	}
	if count, err := s.A.Data.SealNotificationSecrets(context.Background(), s.A.Database); err != nil || count != 1 {
		t.Fatal("sweep did not release the row after timeout")
	}
}

func TestNotificationMCPResubscribeAfterKeyChange(t *testing.T) {
	s, x := notificationFixture(t)
	backend := s.A.Data.EventsBackend()
	caller := mcp.Caller{Owner: s.OwnerID, APIKey: s.Key["X-API-Key"]}
	req := mcp.EventSubscribeRequest{Name: "record.added", Site: "shop", CanonicalArgs: `{"site":"shop"}`, URL: x.server.URL, Secret: notifyTestSecret}
	sub, err := backend.Subscribe(context.Background(), caller, req)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.A.Access.SetPasscodeKey(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Subscribe(context.Background(), caller, req); err != nil {
		t.Fatalf("re-subscribe after a key change failed: %v", err)
	}
	n, err := notificationsSvc(s.A.Data).GetNotification(context.Background(), sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n.PrevSecret != "" || openedNotification(t, s, sub.ID, n.Secret) != notifyTestSecret {
		t.Fatal("re-subscribe kept an unreadable secret")
	}
}
