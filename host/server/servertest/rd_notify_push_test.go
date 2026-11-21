package servertest

import (
	"context"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	operations "github.com/vsriram/simple-host/host/ops"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/access/accesshttp"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/data/datahttp"
	"github.com/vsriram/simple-host/host/sites"
)

// Web push through the whole server: the pieces here live in different
// modules (Access's sign-out and viewer list, Sites' service, Data's push
// devices), so no one module's suite can build them.

func rdPushFixture(t *testing.T) (StorageTestSite, *httptest.Server) {
	t.Helper()
	s := NewStorageTestSite(t)
	if err := s.A.Access.SetPasscodeKey(TestPasscodeKey); err != nil {
		t.Fatal(err)
	}
	s.A.Data.SetWebPushAccounts(nil, true)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(201)
	}))
	t.Cleanup(srv.Close)
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	s.A.Data.Notifier().UseTestClient(roots, true)
	s.Must(t, "PUT", "/resources/orders", StorageRules("sqlite", RulesPersonal), s.Key, 201)
	return s, srv
}

func rdPushBody(t *testing.T, endpoint string) map[string]any {
	t.Helper()
	_, x, y, err := elliptic.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	authBytes := make([]byte, 16)
	if _, err := rand.Read(authBytes); err != nil {
		t.Fatal(err)
	}
	return map[string]any{
		"endpoint": endpoint, "resource": "orders", "label": "test device",
		"keys":   map[string]string{"p256dh": base64.RawURLEncoding.EncodeToString(elliptic.Marshal(elliptic.P256(), x, y)), "auth": base64.RawURLEncoding.EncodeToString(authBytes)},
		"events": []string{"record.added", "record.changed"},
	}
}

func rdSubscribe(t *testing.T, s StorageTestSite, caller map[string]string, body map[string]any) string {
	t.Helper()
	r := s.A.At(t, "POST", s.Host, "/v1/sites/shop/push/subscribe", body, caller)
	if r.Status != 201 {
		t.Fatalf("subscribe: %d %s", r.Status, r.Body)
	}
	return r.JSON(t)["id"].(string)
}

func rdDevices(t *testing.T, s StorageTestSite, ids ...string) []bool {
	t.Helper()
	out := make([]bool, len(ids))
	for i, id := range ids {
		var n int
		if err := s.A.Database.QueryRow(`SELECT count(*) FROM site_notifications WHERE id = $1`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		out[i] = n > 0
	}
	return out
}

// Subscribing the same device again (same endpoint and resource) is the
// same device, not a second row. The concurrent version is not restored
// (race).
func TestRDWebPushSubscribeSameDeviceOneRow(t *testing.T) {
	s, srv := rdPushFixture(t)
	body := rdPushBody(t, srv.URL)
	first := rdSubscribe(t, s, s.Callers["alice"], body)
	if again := rdSubscribe(t, s, s.Callers["alice"], body); again != first {
		t.Fatalf("second subscribe made device %s, want %s", again, first)
	}
	var n int
	if err := s.A.Database.QueryRow(`SELECT count(*) FROM site_notifications WHERE site_id = $1 AND kind = 'web-push' AND owner_id = $2`, s.Sid, s.AliceID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("alice has %d devices (%v), want 1", n, err)
	}
}

// Signing out deletes only the device its own session created: another
// browser signed in as the same person keeps its device.
func TestRDWebPushSignOutOnlyDeletesThatSessionsDevice(t *testing.T) {
	s, srv := rdPushFixture(t)
	srv2 := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(201) }))
	defer srv2.Close()
	mint := func() map[string]string {
		id := make([]byte, 32)
		if _, err := rand.Read(id); err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		if _, err := accountsrepo.InsertVisitorSession(context.Background(), s.A.Database, id, s.AliceID, s.Sid, s.Host, now.Add(time.Hour), now.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		return Browser(s.Host, hex.EncodeToString(id))
	}
	one, two := mint(), mint()
	id1 := rdSubscribe(t, s, one, rdPushBody(t, srv.URL))
	id2 := rdSubscribe(t, s, two, rdPushBody(t, srv2.URL))
	if id1 == id2 {
		t.Fatal("two sessions' devices collapsed into one row")
	}
	if r := s.A.At(t, "POST", s.Host, "/v1/visitor/logout", nil, one); r.Status != 200 && r.Status != 204 {
		t.Fatalf("sign-out: %d %s", r.Status, r.Body)
	}
	if got := rdDevices(t, s, id1, id2); got[0] || !got[1] {
		t.Fatalf("after session one signs out, (one,two) devices exist = %v, want (false,true)", got)
	}
}

// Removing a named viewer through operations.RemoveSiteViewer itself (not
// the HTTP route) also deletes that person's push devices on the site.
func TestRDWebPushServiceLayerViewerRemovalCleansUpPush(t *testing.T) {
	s, srv := rdPushFixture(t)
	email := ""
	if err := s.A.Database.QueryRow(`SELECT username FROM users WHERE id = $1`, s.AliceID).Scan(&email); err != nil {
		t.Fatal(err)
	}
	if r := s.A.At(t, "POST", PcSiteDomain, "/v1/sites/shop/viewers", map[string]any{"emails": []string{email}}, s.Key); r.Status != 200 {
		t.Fatalf("grant viewer: %d %s", r.Status, r.Body)
	}
	id := rdSubscribe(t, s, s.Callers["alice"], rdPushBody(t, srv.URL))
	removed, err := operations.RemoveSiteViewer(context.Background(), s.A.Database, s.A.Sites.Notify, s.Sid, email)
	if err != nil || !removed {
		t.Fatalf("RemoveSiteViewer: removed=%v err=%v", removed, err)
	}
	if got := rdDevices(t, s, id); got[0] {
		t.Fatal("push device survived the service-level viewer removal")
	}
}

// The notification key (the same PASSCODE_ENC_KEY) alone does not turn
// passcodes on where sites share one origin: Data may seal notification
// secrets, Access still refuses passcodes until sites have addresses of
// their own and SITE_PASSCODES is on.
func TestRDNotificationKeyDoesNotEnableSharedOriginPasscodes(t *testing.T) {
	a, _ := NewSiteApp(t, "canonical")
	hosts := sites.NewHosts(a.Database, a.Sites.Disk, nil, PcSiteDomain, PcContentHost, "cname."+PcSiteDomain, "")
	if !hosts.Addressing().SharedOrigin() {
		t.Fatal("a Hosts with no person or site hosts is not a shared origin")
	}
	limits := config.DefaultLimits()
	limits.SitePasscodes = true
	h := &accesshttp.AccessHandler{Hosts: hosts, Limits: limits}
	if err := h.SetPasscodeKey(TestPasscodeKey); err != nil {
		t.Fatal(err)
	}
	d := &datahttp.DataHandler{PasscodeKeys: h.PasscodeKeys()}
	if !d.NotificationsReady() {
		t.Fatal("the key does not enable notifications")
	}
	if h.PasscodesReady() {
		t.Fatal("the notification key enabled passcodes on a shared origin")
	}
	hosts.SetPersonHosts("canonical")
	if !h.PasscodesReady() {
		t.Fatal("passcodes stay off once sites have addresses of their own")
	}
}
