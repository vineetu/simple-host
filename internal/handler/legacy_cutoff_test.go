package handler

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	appstorage "github.com/vsriram/simple-host/internal/app/storage"
)

const legacyCutoffDay = "2026-10-12"

func legacyCutoffTime() time.Time {
	return time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
}

func TestLegacyStorageCutoffGateSkipsOptionsAndProduct(t *testing.T) {
	h := &SiteHandler{}
	h.SetLegacyStorageCutoff(legacyCutoffTime())
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	gate := h.LegacyStorageCutoffGate(next)

	r := httptest.NewRecorder()
	gate.ServeHTTP(r, httptest.NewRequest(http.MethodOptions, "/v1/sites/shop/state", nil))
	if r.Code != http.StatusNoContent {
		t.Errorf("OPTIONS: %d %s", r.Code, r.Body.String())
	}

	h.ed = groupTestEdition()
	r = httptest.NewRecorder()
	gate.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/v1/sites/shop/state", nil))
	if r.Code != http.StatusNoContent {
		t.Errorf("Hack: %d %s", r.Code, r.Body.String())
	}
	if strings.Contains(r.Body.String(), "legacy_storage_closed") {
		t.Errorf("Hack answered cutoff: %s", r.Body.String())
	}
}

func TestLegacyStorageCutoffNewSiteGone(t *testing.T) {
	a, dir := newSiteApp(t, "canonical")
	a.sites.SetLegacyStorageCutoff(legacyCutoffTime())
	olive := a.newPerson(t, "olive")
	a.deploy(t, olive, "shop")
	_, handle := a.userID(t, olive)
	markReady(t, dir, handle)
	key := map[string]string{"X-API-Key": olive.key}
	id := a.siteID(t, olive, "shop")
	u := "/v1/u/" + handle + "/sites/shop"
	siteHost := "shop." + handle + "." + pcSiteDomain

	cases := []struct {
		name, method, host, path string
		body                     any
		headers                  map[string]string
	}{
		{"GET state", "GET", pcSiteDomain, "/v1/sites/shop/state", nil, key},
		{"PUT state", "PUT", pcSiteDomain, "/v1/sites/shop/state", map[string]any{"n": 1}, key},
		{"PATCH state", "PATCH", pcSiteDomain, "/v1/sites/shop/state", map[string]any{"ops": []any{map[string]any{"op": "set", "path": "n", "value": 1}}}, key},
		{"POST collection", "POST", pcSiteDomain, "/v1/sites/shop/collections/guestbook", map[string]any{"msg": "hi"}, key},
		{"GET collections", "GET", pcSiteDomain, "/v1/sites/shop/collections", nil, key},
		{"GET data", "GET", pcSiteDomain, "/v1/sites/shop/data", nil, key},
		{"PUT kind", "PUT", pcSiteDomain, "/v1/sites/shop/data/notes/kind", map[string]any{"kind": "content"}, key},
		{"GET state by handle", "GET", pcSiteDomain, u + "/state", nil, key},
		{"PUT state by handle", "PUT", pcSiteDomain, u + "/state", map[string]any{"n": 1}, key},
		{"POST collection by handle", "POST", pcSiteDomain, u + "/collections/guestbook", map[string]any{"msg": "hi"}, key},
		{"GET collections by handle", "GET", pcSiteDomain, u + "/collections", nil, key},
		{"GET data by handle", "GET", pcSiteDomain, u + "/data", nil, key},
		{"PUT kind by handle", "PUT", pcSiteDomain, u + "/data/notes/kind", map[string]any{"kind": "content"}, key},
		{"GET state on site host", "GET", siteHost, "/v1/sites/shop/state", nil, nil},
		{"PUT state on site host", "PUT", siteHost, "/v1/sites/shop/state", map[string]any{"n": 1}, key},
		{"admin collections", "GET", pcSiteDomain, "/v1/admin/sites/" + id + "/collections", nil, map[string]string{"X-API-Key": a.admin}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assertLegacyClosed(t, a.at(t, c.method, c.host, c.path, c.body, c.headers), "shop")
		})
	}
}

func TestLegacyStorageCutoffGrandfather(t *testing.T) {
	a, _ := newSiteApp(t, "canonical")
	a.sites.SetLegacyStorageCutoff(legacyCutoffTime())
	olive := a.newPerson(t, "olive")
	a.deploy(t, olive, "shop")
	_, handle := a.userID(t, olive)
	id := a.siteID(t, olive, "shop")
	if _, err := a.database.Exec(`UPDATE sites SET created_at = $1 WHERE id = $2`, time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC), id); err != nil {
		t.Fatal(err)
	}
	key := map[string]string{"X-API-Key": olive.key}
	u := "/v1/u/" + handle + "/sites/shop"

	wantOK := []struct {
		name, method, path string
		body               any
		status             int
	}{
		{"GET state", "GET", "/v1/sites/shop/state", nil, 200},
		{"PUT state", "PUT", "/v1/sites/shop/state", map[string]any{"n": 1}, 200},
		{"PATCH state", "PATCH", "/v1/sites/shop/state", map[string]any{"ops": []any{map[string]any{"op": "inc", "path": "n", "by": 1}}}, 200},
		{"POST collection", "POST", "/v1/sites/shop/collections/guestbook", map[string]any{"msg": "hi"}, 201},
		{"GET collections", "GET", "/v1/sites/shop/collections", nil, 200},
		{"GET data", "GET", "/v1/sites/shop/data", nil, 200},
		{"PUT kind", "PUT", "/v1/sites/shop/data/notes/kind", map[string]any{"kind": "content"}, 200},
		{"GET state by handle", "GET", u + "/state", nil, 200},
		{"PUT state by handle", "PUT", u + "/state", map[string]any{"n": 3}, 200},
	}
	for _, c := range wantOK {
		t.Run(c.name, func(t *testing.T) {
			r := a.at(t, c.method, pcSiteDomain, c.path, c.body, key)
			if r.status != c.status {
				t.Fatalf("got %d %s, want %d", r.status, r.body, c.status)
			}
		})
	}
}

func TestLegacyStorageCutoffKnobOff(t *testing.T) {
	a := newPrivateApp(t)
	olive := a.newPerson(t, "olive")
	a.deploy(t, olive, "shop")
	key := map[string]string{"X-API-Key": olive.key}
	if r := a.at(t, "GET", pcSiteDomain, "/v1/sites/shop/state", nil, key); r.status != 200 {
		t.Fatalf("GET state: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PUT", pcSiteDomain, "/v1/sites/shop/state", map[string]any{"n": 1}, key); r.status != 200 {
		t.Fatalf("PUT state: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", pcSiteDomain, "/v1/sites/shop/collections/guestbook", map[string]any{"msg": "hi"}, key); r.status != 201 {
		t.Fatalf("POST collection: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", pcSiteDomain, "/v1/sites/shop/collections", nil, key); r.status != 200 {
		t.Fatalf("GET collections: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", pcSiteDomain, "/v1/sites/shop/data", nil, key); r.status != 200 {
		t.Fatalf("GET data: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PUT", pcSiteDomain, "/v1/sites/shop/data/notes/kind", map[string]any{"kind": "content"}, key); r.status != 200 {
		t.Fatalf("PUT kind: %d %s", r.status, r.body)
	}
}

func TestLegacyStorageCutoffSkipsOptionsAndStorage(t *testing.T) {
	a := newPrivateApp(t)
	a.sites.SetLegacyStorageCutoff(legacyCutoffTime())
	olive := a.newPerson(t, "olive")
	a.deploy(t, olive, "shop")
	key := map[string]string{"X-API-Key": olive.key}

	if r := a.at(t, "OPTIONS", pcSiteDomain, "/v1/sites/shop/state", nil, nil); r.status == http.StatusGone {
		t.Fatalf("OPTIONS state refused: %d %s", r.status, r.body)
	}
	if r := a.at(t, "OPTIONS", pcSiteDomain, "/v1/sites/shop/collections/guestbook", nil, nil); r.status == http.StatusGone {
		t.Fatalf("OPTIONS collection refused: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", pcSiteDomain, "/v1/sites/shop/storage/resources", nil, key); r.status == http.StatusGone {
		t.Fatalf("GET storage refused: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PUT", pcSiteDomain, "/v1/sites/shop/storage/resources/prefs", map[string]any{"kind": "kv"}, key); r.status == http.StatusGone {
		t.Fatalf("PUT storage refused: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", pcSiteDomain, "/v1/data-notify/stop", nil, nil); r.status == http.StatusGone {
		t.Fatalf("data-notify/stop refused: %d %s", r.status, r.body)
	}
}

func TestLegacyStorageCutoffUnknownSitePassesThrough(t *testing.T) {
	a := newPrivateApp(t)
	a.sites.SetLegacyStorageCutoff(legacyCutoffTime())
	olive := a.newPerson(t, "olive")
	key := map[string]string{"X-API-Key": olive.key}
	r := a.at(t, "GET", pcSiteDomain, "/v1/sites/ghost/state", nil, key)
	if r.status == http.StatusGone {
		t.Fatalf("missing site refused: %d %s", r.status, r.body)
	}
}

func assertLegacyClosed(t *testing.T, r resp, site string) {
	t.Helper()
	if r.status != http.StatusGone {
		t.Fatalf("got %d %s, want 410", r.status, r.body)
	}
	j := r.json(t)
	if j["code"] != legacyStorageClosedCode {
		t.Fatalf("code %v, want %s: %s", j["code"], legacyStorageClosedCode, r.body)
	}
	errText, _ := j["error"].(string)
	if !strings.Contains(errText, legacyCutoffDay) || !strings.Contains(errText, site) {
		t.Fatalf("error %q missing cutoff or site", errText)
	}
	if j["storage"] != "/v1/sites/"+site+"/storage/resources" {
		t.Fatalf("storage %v", j["storage"])
	}
}

// When the resolvers disagree, an older candidate wins: a key that owns an
// older same-named site keeps reading it on a newer site's host.
func TestLegacyStorageCutoffOlderCandidatePasses(t *testing.T) {
	a, dir := newSiteApp(t, "canonical")
	a.sites.SetLegacyStorageCutoff(legacyCutoffTime())
	olive := a.newPerson(t, "olive")
	a.deploy(t, olive, "shop")
	if _, err := a.database.Exec(`UPDATE sites SET created_at = $1 WHERE id = $2`, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), a.siteID(t, olive, "shop")); err != nil {
		t.Fatal(err)
	}
	pear := a.newPerson(t, "pear")
	a.deploy(t, pear, "shop")
	_, pearHandle := a.userID(t, pear)
	markReady(t, dir, pearHandle)
	newHost := "shop." + pearHandle + "." + pcSiteDomain

	// Pear's own key on pear's new site: refused.
	assertLegacyClosed(t, a.at(t, "GET", newHost, "/v1/sites/shop/state", nil, map[string]string{"X-API-Key": pear.key}), "shop")
	// Olive's key names olive's older site: never refused by the gate.
	if r := a.at(t, "GET", newHost, "/v1/sites/shop/state", nil, map[string]string{"X-API-Key": olive.key}); r.status == 410 {
		t.Fatalf("older candidate refused: %d %s", r.status, r.body)
	}
	// The bare name on the API host is olive's older site: open.
	if r := a.at(t, "GET", pcSiteDomain, "/v1/sites/shop/state", nil, map[string]string{"X-API-Key": olive.key}); r.status != 200 {
		t.Fatalf("older site: %d %s", r.status, r.body)
	}
}

func TestLegacyCallLogsSelectedSite(t *testing.T) {
	a := newPrivateApp(t)
	alice := a.newPerson(t, "alice")
	bob := a.newPerson(t, "bob")
	a.deploy(t, alice, "shop")
	a.deploy(t, bob, "shop")
	aliceID, bobID := a.siteID(t, alice, "shop"), a.siteID(t, bob, "shop")
	_, aliceHandle := a.userID(t, alice)
	if _, err := a.database.Exec(`UPDATE sites SET created_at=$1 WHERE id=$2`, legacyCutoffTime().Add(-time.Hour), aliceID); err != nil {
		t.Fatal(err)
	}
	a.sites.SetLegacyStorageCutoff(legacyCutoffTime())
	var logs bytes.Buffer
	old := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(old) })
	for _, tc := range []struct{ method, path, key, want string }{
		{"GET", "/v1/sites/shop/collections", bob.key, bobID},
		{"GET", "/v1/sites/shop/collections/guestbook", bob.key, bobID},
		{"GET", "/v1/sites/shop/state", bob.key, bobID},
		{"PUT", "/v1/sites/shop/state", bob.key, bobID},
		{"GET", "/v1/u/" + aliceHandle + "/sites/shop/state/history", alice.key, aliceID},
		{"GET", "/v1/admin/sites/" + strings.ToUpper(aliceID) + "/collections", a.admin, aliceID},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			logs.Reset()
			// Start a new day so each resolver independently supplies evidence.
			a.sites.legacyStorageCalls.mu.Lock()
			a.sites.legacyStorageCalls.day = ""
			a.sites.legacyStorageCalls.mu.Unlock()
			gate := a.sites.LegacyStorageCutoffGate(a.mux)
			for i := 0; i < 2; i++ {
				r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{}`))
				r.Host = pcSiteDomain
				r.Header.Set("X-API-Key", tc.key)
				w := httptest.NewRecorder()
				gate.ServeHTTP(w, r)
				if w.Code != http.StatusOK {
					t.Fatalf("status %d: %s", w.Code, w.Body.String())
				}
			}
			ids, err := appstorage.LegacyNoticeCalls(strings.NewReader(logs.String()))
			if err != nil || !reflect.DeepEqual(ids, []string{tc.want}) || strings.Count(logs.String(), "legacy_storage_call site=") != 1 {
				t.Fatalf("logged sites %v (%v), want only %s once: %s", ids, err, tc.want, logs.String())
			}
		})
	}
}

func TestLegacyCallAdminRequiresAuthenticatedExistingSite(t *testing.T) {
	a := newPrivateApp(t)
	owner := a.newPerson(t, "owner")
	a.deploy(t, owner, "shop")
	id := a.siteID(t, owner, "shop")
	var logs bytes.Buffer
	old := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(old) })
	for _, tc := range []struct {
		id, key string
		status  int
	}{
		{id, "", http.StatusUnauthorized},
		{id, "invalid", http.StatusUnauthorized},
		{id, owner.key, http.StatusNotFound},
		{"arbitrary-log-text", "", http.StatusUnauthorized},
		{"arbitrary-log-text", a.admin, http.StatusNotFound},
		{"00000000-0000-0000-0000-000000000000", a.admin, http.StatusNotFound},
	} {
		r := a.at(t, "GET", pcSiteDomain, "/v1/admin/sites/"+tc.id+"/collections", nil, map[string]string{"X-API-Key": tc.key})
		if r.status != tc.status {
			t.Fatalf("admin request: %d %s, want %d", r.status, r.body, tc.status)
		}
	}
	if strings.Contains(logs.String(), "legacy_storage_call") {
		t.Fatalf("unauthorized or nonexistent site logged: %s", logs.String())
	}
	// An unauthenticated attempt must not consume the real site's daily entry.
	if r := a.at(t, "GET", pcSiteDomain, "/v1/admin/sites/"+id+"/collections", nil, map[string]string{"X-API-Key": a.admin}); r.status != 200 {
		t.Fatalf("authenticated admin: %d %s", r.status, r.body)
	}
	ids, err := appstorage.LegacyNoticeCalls(strings.NewReader(logs.String()))
	if err != nil || !reflect.DeepEqual(ids, []string{id}) {
		t.Fatalf("authenticated admin logged %v: %v", ids, err)
	}
}
