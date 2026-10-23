package handler

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

	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/mcp"
)

func notificationKey(t *testing.T) *SiteHandler {
	t.Helper()
	h := &SiteHandler{}
	if err := h.SetPasscodeKey(testPasscodeKey); err != nil {
		t.Fatal(err)
	}
	return h
}
func openedNotification(t *testing.T, s presetSite, id, stored string) string {
	t.Helper()
	plain, err := s.a.sites.passcode.openNotification(id, stored)
	if err != nil {
		t.Fatal("could not open stored notification value")
	}
	return plain
}
func assertNotificationSealed(t *testing.T, stored, plain string) {
	t.Helper()
	if !strings.HasPrefix(stored, "enc1:") || strings.Contains(stored, plain) {
		t.Fatal("notification value was not sealed")
	}
}
func TestNotificationSealOpen(t *testing.T) {
	h := notificationKey(t)
	p := &h.passcode
	sealed, err := p.sealNotification("sub_a", notifyTestSecret)
	if err != nil {
		t.Fatal(err)
	}
	assertNotificationSealed(t, sealed, notifyTestSecret)
	if plain, err := p.openNotification("sub_a", sealed); err != nil || plain != notifyTestSecret {
		t.Fatal("round trip failed")
	}
	again, err := p.sealNotification("sub_a", notifyTestSecret)
	if err != nil || sealed == again {
		t.Fatal("nonce was reused")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(sealed, "enc1:"))
	if err != nil || len(raw) != 12+len(notifyTestSecret)+16 {
		t.Fatal("unexpected stored format")
	}
	raw[len(raw)-1] ^= 1
	wrong := notificationKey(t)
	if err := wrong.SetPasscodeKey(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, id, value string
		p               *passcodeState
	}{
		{"wrong AD", "sub_b", sealed, p},
		{"tampered", "sub_a", "enc1:" + base64.StdEncoding.EncodeToString(raw), p},
		{"wrong key", "sub_a", sealed, &wrong.passcode},
		{"missing key", "sub_a", sealed, &passcodeState{}},
		{"bad base64", "sub_a", "enc1:!", p},
		{"short ciphertext", "sub_a", "enc1:AAAA", p},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.p.openNotification(tc.id, tc.value); !errors.Is(err, notifyFailure("secret_unreadable")) {
				t.Fatal("unreadable value did not fail cleanly")
			}
		})
	}
	for _, p := range []*passcodeState{p, {}} {
		for _, plain := range []string{"", notifyTestSecret, "Bearer legacy"} {
			if got, err := p.openNotification("sub_a", plain); err != nil || got != plain {
				t.Fatal("legacy value changed")
			}
		}
		if got, err := p.sealNotification("sub_a", ""); err != nil || got != "" {
			t.Fatal("empty value changed")
		}
	}
	if _, err := (&passcodeState{}).sealNotification("sub_a", notifyTestSecret); !errors.Is(err, errPasscodeNoKey) {
		t.Fatal("sealing without a key accepted")
	}
	// A custom header beginning enc1: is still plaintext input and is sealed once.
	value, err := p.sealNotification("sub_a:header", "enc1:literal-header")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := p.openNotification("sub_a:header", value); err != nil || got != "enc1:literal-header" {
		t.Fatal("header prefix changed")
	}
}

func TestPasscodeStoredFormatCompatibility(t *testing.T) {
	h := notificationKey(t)
	p := &h.passcode
	// Construct the pre-change on-disk format directly, without the shared helper.
	nonce := bytes.Repeat([]byte{1}, 12)
	legacy := p.aead.Seal(nonce, nonce, []byte("123456"), []byte("site:site-a"))
	if got, err := p.open("site-a", legacy); err != nil || got != "123456" {
		t.Fatal("old passcode format no longer opens")
	}
	sealed, err := p.seal("site-a", "123456")
	if err != nil {
		t.Fatal(err)
	}
	plain, err := p.aead.Open(nil, sealed[:12], sealed[12:], []byte("site:site-a"))
	if err != nil || string(plain) != "123456" {
		t.Fatal("new passcode format changed")
	}
	if _, err := p.openNotification("site-a", "enc1:"+base64.StdEncoding.EncodeToString(sealed)); err == nil {
		t.Fatal("passcode accepted as notification secret")
	}
}

func TestNotificationSealedDelivery(t *testing.T) {
	h := notificationKey(t)
	x := newNotificationReceiver(t)
	c := x.client()
	c.secrets = &h.passcode
	second, err := newNotifySecret()
	if err != nil {
		t.Fatal(err)
	}
	x.keys = append(x.keys, second)
	until := time.Now().Add(time.Hour)
	n := db.Notification{ID: "sub_sealed", URL: x.server.URL, Source: "owner", PrevSecretUntil: &until, AuthHeaderName: "Authorization"}
	n.Secret, err = h.passcode.sealNotification(n.ID, second)
	if err != nil {
		t.Fatal(err)
	}
	n.PrevSecret, err = h.passcode.sealNotification(n.ID, notifyTestSecret)
	if err != nil {
		t.Fatal(err)
	}
	n.AuthHeaderValue, err = h.passcode.sealNotification(n.ID+":header", "Bearer receiver")
	if err != nil {
		t.Fatal(err)
	}
	if r := c.send(context.Background(), n, "evt_sealed", []byte(`{}`)); !r.Delivered {
		t.Fatal("sealed delivery failed")
	}
	got := x.snapshot()[0]
	if len(strings.Fields(got.header.Get("webhook-signature"))) != 2 || got.header.Get("Authorization") != "Bearer receiver" {
		t.Fatal("rotation or custom header failed")
	}
	for _, field := range []string{"secret", "prev_secret", "auth_header_value"} {
		t.Run(field, func(t *testing.T) {
			broken := n
			switch field {
			case "secret":
				broken.Secret = "enc1:!"
			case "prev_secret":
				broken.PrevSecret = "enc1:!"
			case "auth_header_value":
				broken.AuthHeaderValue = "enc1:!"
			}
			if r := c.send(context.Background(), broken, "evt_bad", []byte(`{}`)); r.Delivered || r.Result != "secret_unreadable" {
				t.Fatal("unreadable field was sent")
			}
		})
	}
	c.secrets = nil
	if r := c.send(context.Background(), n, "evt_missing", []byte(`{}`)); r.Delivered || r.Result != "secret_unreadable" {
		t.Fatal("sealed delivery without key accepted")
	}
	if len(x.snapshot()) != 1 {
		t.Fatal("unreadable delivery reached receiver")
	}
	// A keyless server still delivers legacy rows, including the auth header.
	n.Secret, n.PrevSecret, n.AuthHeaderValue = second, notifyTestSecret, "Bearer receiver"
	if r := c.send(context.Background(), n, "evt_legacy", []byte(`{}`)); !r.Delivered {
		t.Fatal("legacy delivery without key failed")
	}
}

func TestNotificationStoredSecretsAndRefresh(t *testing.T) {
	s, x := notificationFixture(t)
	id := subscribeNotification(t, s, x)
	ctx := context.Background()
	n, err := db.GetNotification(ctx, s.a.database, id)
	if err != nil {
		t.Fatal(err)
	}
	assertNotificationSealed(t, n.Secret, notifyTestSecret)
	assertNotificationSealed(t, n.AuthHeaderValue, "Bearer receiver")
	if n.PrevSecret != "" {
		t.Fatal("new row has previous secret")
	}
	notificationRequest(t, s, "POST", "/"+id+"/rotate-secret", nil, 200)
	n, err = db.GetNotification(ctx, s.a.database, id)
	if err != nil {
		t.Fatal(err)
	}
	assertNotificationSealed(t, n.PrevSecret, notifyTestSecret)
	if openedNotification(t, s, id, n.PrevSecret) != notifyTestSecret {
		t.Fatal("rotation lost previous secret")
	}

	backend := s.a.sites.EventsBackend()
	req := mcp.EventSubscribeRequest{Name: "record.added", Site: "shop", CanonicalArgs: `{"site":"shop"}`, URL: x.server.URL, Secret: notifyTestSecret}
	caller := mcp.Caller{Owner: s.ownerID}
	result, err := backend.Subscribe(ctx, caller, req)
	if err != nil {
		t.Fatal(err)
	}
	first, err := db.GetNotification(ctx, s.a.database, result.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertNotificationSealed(t, first.Secret, notifyTestSecret)
	if _, err = backend.Subscribe(ctx, caller, req); err != nil {
		t.Fatal(err)
	}
	same, err := db.GetNotification(ctx, s.a.database, result.ID)
	if err != nil {
		t.Fatal(err)
	}
	if same.Secret == first.Secret || same.PrevSecret != "" || same.PrevSecretUntil != nil {
		t.Fatal("same secret refresh rotated or reused nonce")
	}
	req.Secret, err = newNotifySecret()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = backend.Subscribe(ctx, caller, req); err != nil {
		t.Fatal(err)
	}
	changed, err := db.GetNotification(ctx, s.a.database, result.ID)
	if err != nil {
		t.Fatal(err)
	}
	if openedNotification(t, s, result.ID, changed.PrevSecret) != notifyTestSecret || changed.PrevSecretUntil == nil || time.Until(*changed.PrevSecretUntil) < 23*time.Hour {
		t.Fatal("different secret did not rotate")
	}
	if _, err = backend.Subscribe(ctx, caller, req); err != nil {
		t.Fatal(err)
	}
	refreshed, err := db.GetNotification(ctx, s.a.database, result.ID)
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
	before, err := db.GetNotification(ctx, s.a.database, untouched)
	if err != nil {
		t.Fatal(err)
	}
	notificationSQL(t, s, `UPDATE site_notifications SET secret=$2,prev_secret=$2,auth_header_value=$3 WHERE id=$1`, id, notifyTestSecret, "Bearer legacy")
	count, err := s.a.sites.SealNotificationSecrets(ctx, s.a.database)
	if err != nil || count != 1 {
		t.Fatal("sweep did not seal one legacy row")
	}
	after, err := db.GetNotification(ctx, s.a.database, id)
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
	other, err := db.GetNotification(ctx, s.a.database, untouched)
	if err != nil || other.Secret != before.Secret || other.AuthHeaderValue != before.AuthHeaderValue || other.PrevSecret != "" {
		t.Fatal("sweep changed sealed or empty fields")
	}
	if count, err = s.a.sites.SealNotificationSecrets(ctx, s.a.database); err != nil || count != 0 {
		t.Fatal("rerun was not a no-op")
	}
	// A concurrent writer holds the row while the sweep skips it without waiting.
	notificationSQL(t, s, `UPDATE site_notifications SET secret=$2,prev_secret='',auth_header_value='old' WHERE id=$1`, id, notifyTestSecret)
	tx, err := s.a.database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE site_notifications SET auth_header_value='concurrent-value' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	bounded, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if count, err := s.a.sites.SealNotificationSecrets(bounded, s.a.database); err != nil || count != 0 {
		t.Fatal("sweep did not skip the locked row")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if count, err := s.a.sites.SealNotificationSecrets(ctx, s.a.database); err != nil || count != 1 {
		t.Fatal("next sweep did not seal the skipped row")
	}
	concurrent, err := db.GetNotification(ctx, s.a.database, id)
	if err != nil || openedNotification(t, s, id+":header", concurrent.AuthHeaderValue) != "concurrent-value" {
		t.Fatal("sweep lost the concurrent write")
	}
	if count, err := s.a.sites.SealNotificationSecrets(ctx, s.a.database); err != nil || count != 0 {
		t.Fatal("rerun was not a no-op after the concurrent write")
	}
	// Only a legacy header also qualifies; sealed secrets stay byte-for-byte intact.
	notificationSQL(t, s, `UPDATE site_notifications SET secret=$2,prev_secret='',auth_header_value='legacy-header' WHERE id=$1`, id, after.Secret)
	if count, err := s.a.sites.SealNotificationSecrets(ctx, s.a.database); err != nil || count != 1 {
		t.Fatal("header-only row was not sealed")
	}
	final, err := db.GetNotification(ctx, s.a.database, id)
	if err != nil || final.Secret != after.Secret || final.PrevSecret != "" {
		t.Fatal("mixed row changed sealed or empty values")
	}
}

func TestNotificationMissingKey(t *testing.T) {
	s, x := notificationFixture(t)
	id := subscribeNotification(t, s, x)
	if err := s.a.sites.SetPasscodeKey(""); err != nil {
		t.Fatal(err)
	}
	response := notificationRequest(t, s, "POST", "", map[string]any{"url": x.server.URL, "events": []string{"record.added"}}, 409).json(t)
	if response["code"] != "notifications_not_enabled" || !strings.Contains(response["error"].(string), "PASSCODE_ENC_KEY") {
		t.Fatal("missing-key REST error unclear")
	}
	notificationRequest(t, s, "POST", "/"+id+"/rotate-secret", nil, 409)
	limits := notificationRequest(t, s, "GET", "", nil, 200).json(t)["limits"].(map[string]any)
	if limits["enabled"] != false {
		t.Fatal("limits advertise notifications without key")
	}
	_, err := s.a.sites.EventsBackend().Subscribe(context.Background(), mcp.Caller{Owner: s.ownerID}, mcp.EventSubscribeRequest{})
	var eventErr *mcp.EventError
	if !errors.As(err, &eventErr) || eventErr.Data["reason"] != "notifications_not_enabled" {
		t.Fatal("missing-key MCP error unclear")
	}
	emitWorkerNotification(t, s, s.sid)
	drainNotifications(t, s)
	n, err := db.GetNotification(context.Background(), s.a.database, id)
	if err != nil || n.LastResult != "secret_unreadable" || len(x.snapshot()) != 0 {
		t.Fatal("unreadable delivery not recorded safely")
	}
	notificationSQL(t, s, `UPDATE site_notifications SET secret=$2,auth_header_value='Bearer receiver' WHERE id=$1`, id, notifyTestSecret)
	emitWorkerNotification(t, s, s.sid)
	drainNotifications(t, s)
	if len(x.snapshot()) != 1 {
		t.Fatal("legacy worker delivery stopped without key")
	}
}

func TestNotificationMissingKeyMCPDiscovery(t *testing.T) {
	h := &SiteHandler{}
	server := mcp.NewServer(mcp.Config{Events: h.EventsBackend()})
	for _, method := range []string{"server/discover", "events/subscribe"} {
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": map[string]any{"_meta": map[string]any{"io.modelcontextprotocol/protocolVersion": "2026-07-28", "io.modelcontextprotocol/clientCapabilities": map[string]any{}}}})
		req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
		req.Header.Set("MCP-Protocol-Version", "2026-07-28")
		req.Header.Set("Mcp-Method", method)
		req = req.WithContext(mcp.WithCaller(req.Context(), mcp.Caller{Owner: "owner", APIKey: "test-key"}))
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		var result map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if method == "server/discover" {
			out, ok := result["result"].(map[string]any)
			if !ok {
				t.Fatal("discovery failed")
			}
			caps := out["capabilities"].(map[string]any)
			if _, ok := caps["events"]; ok {
				t.Fatal("events advertised without key")
			}
			if _, ok := caps["extensions"]; ok {
				t.Fatal("events extension advertised without key")
			}
		} else {
			out, ok := result["error"].(map[string]any)
			if !ok || out["code"] != float64(mcp.EventCodeForbidden) {
				t.Fatal("subscribe did not return JSON-RPC error")
			}
			data, ok := out["data"].(map[string]any)
			if !ok || data["reason"] != "notifications_not_enabled" {
				t.Fatal("subscribe missing unavailable reason")
			}
		}
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
				n, err := db.GetNotification(ctx, s.a.database, id)
				if err != nil {
					t.Fatal(err)
				}
				if _, _, err := db.SaveNotification(ctx, s.a.database, n, 10, nil, 7, s.a.sites.notificationSecrets()); err != nil {
					t.Fatal(err)
				}
			}
			n, err := db.GetNotification(ctx, s.a.database, id)
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
	withPasscodeLimits(t, func(l *config.Limits) { l.SitePasscodes = true })
	h := notificationKey(t)
	if !h.SharedOrigin() || !h.NotificationsReady() || h.PasscodesReady() {
		t.Fatal("notification key enabled shared-origin passcodes")
	}
}

func TestNotificationPurposeBinding(t *testing.T) {
	s, x := notificationFixture(t)
	id := subscribeNotification(t, s, x)
	n, err := db.GetNotification(context.Background(), s.a.database, id)
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
			if r := s.a.sites.notifyEngine().client.send(context.Background(), copied, "evt_swap", []byte(`{}`)); r.Delivered || r.Result != "secret_unreadable" {
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
	if err := s.a.sites.SetPasscodeKey(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))); err != nil {
		t.Fatal(err)
	}
	out := notificationRequest(t, s, "POST", "/"+id+"/rotate-secret", nil, 200).json(t)
	if out["previous_valid_until"] != nil {
		t.Fatal("unreadable previous secret has a grace period")
	}
	n, err := db.GetNotification(context.Background(), s.a.database, id)
	if err != nil {
		t.Fatal(err)
	}
	if n.PrevSecret != "" || n.PrevSecretUntil != nil || openedNotification(t, s, id, n.Secret) != out["secret"] {
		t.Fatal("rotation retained an unreadable secret or lost its replacement")
	}
	x.keys = []string{out["secret"].(string)}
	if r := s.a.sites.notifyEngine().client.send(context.Background(), n, "evt_rotated", []byte(`{}`)); !r.Delivered {
		t.Fatal("rotated notification could not deliver")
	}
}

func TestNotificationMissingKeyMCPUnsubscribe(t *testing.T) {
	s, x := notificationFixture(t)
	backend := s.a.sites.EventsBackend()
	caller := mcp.Caller{Owner: s.ownerID, APIKey: s.key["X-API-Key"]}
	sub, err := backend.Subscribe(context.Background(), caller, mcp.EventSubscribeRequest{
		Name: "record.added", Site: "shop", CanonicalArgs: `{"site":"shop"}`, URL: x.server.URL, Secret: notifyTestSecret,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.a.sites.SetPasscodeKey(""); err != nil {
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
	if _, err := db.GetNotification(context.Background(), s.a.database, sub.ID); !errors.Is(err, sql.ErrNoRows) {
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
		return s.a.sites.passcode.sealNotification(id, plain)
	}
	if count, err := db.SealNotificationSecrets(ctx, s.a.database, seal); err == nil || count != 0 {
		t.Fatal("expired sweep did not stop")
	}
	n, err := db.GetNotification(context.Background(), s.a.database, id)
	if err != nil || n.Secret != notifyTestSecret {
		t.Fatal("expired sweep changed the row")
	}
	if count, err := s.a.sites.SealNotificationSecrets(context.Background(), s.a.database); err != nil || count != 1 {
		t.Fatal("sweep did not release the row after timeout")
	}
}

func TestNotificationMCPResubscribeAfterKeyChange(t *testing.T) {
	s, x := notificationFixture(t)
	backend := s.a.sites.EventsBackend()
	caller := mcp.Caller{Owner: s.ownerID, APIKey: s.key["X-API-Key"]}
	req := mcp.EventSubscribeRequest{Name: "record.added", Site: "shop", CanonicalArgs: `{"site":"shop"}`, URL: x.server.URL, Secret: notifyTestSecret}
	sub, err := backend.Subscribe(context.Background(), caller, req)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.a.sites.SetPasscodeKey(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Subscribe(context.Background(), caller, req); err != nil {
		t.Fatalf("re-subscribe after a key change failed: %v", err)
	}
	n, err := db.GetNotification(context.Background(), s.a.database, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n.PrevSecret != "" || openedNotification(t, s, sub.ID, n.Secret) != notifyTestSecret {
		t.Fatal("re-subscribe kept an unreadable secret")
	}
}
