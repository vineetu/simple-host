package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const legacyCutoffDay = "2026-10-12"

func legacyCutoffTime() time.Time {
	return time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
}

func TestParseLegacyStoragePath(t *testing.T) {
	cases := []struct {
		path                  string
		match, skip           bool
		handle, site, adminID string
	}{
		{"/v1/sites/shop/state", true, false, "", "shop", ""},
		{"/v1/sites/shop/collections/guestbook", true, false, "", "shop", ""},
		{"/v1/sites/shop/data/notes/kind", true, false, "", "shop", ""},
		{"/v1/u/olive/sites/shop/state", true, false, "olive", "shop", ""},
		{"/v1/u/olive/sites/shop/collections/guestbook", true, false, "olive", "shop", ""},
		{"/v1/admin/sites/abc/collections", true, false, "", "", "abc"},
		{"/v1/admin/sites/abc/collections/private/export.csv", true, false, "", "", "abc"},
		{"/v1/data-notify/stop", true, true, "", "", ""},
		{"/v1/admin/data-watch", true, true, "", "", ""},
		{"/v1/sites/shop/storage/resources", false, false, "", "", ""},
		{"/v1/sites/shop/files", false, false, "", "", ""},
		{"/v1/me", false, false, "", "", ""},
	}
	for _, c := range cases {
		got := parseLegacyStoragePath(c.path)
		if got.match != c.match || got.skip != c.skip || got.handle != c.handle || got.site != c.site || got.adminID != c.adminID {
			t.Errorf("%s: %+v, want match=%v skip=%v handle=%q site=%q adminID=%q",
				c.path, got, c.match, c.skip, c.handle, c.site, c.adminID)
		}
		if hackLegacyStoragePath(c.path) != c.match {
			t.Errorf("hackLegacyStoragePath(%s) = %v, want %v", c.path, !c.match, c.match)
		}
	}
}

func TestLegacyStorageCutoffGateSkipsOptionsAndHack(t *testing.T) {
	old := HackMode()
	t.Cleanup(func() { SetHackMode(old) })
	h := &SiteHandler{}
	h.SetLegacyStorageCutoff(legacyCutoffTime())
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	gate := h.LegacyStorageCutoffGate(HackLegacyStorageGate(next))

	r := httptest.NewRecorder()
	gate.ServeHTTP(r, httptest.NewRequest(http.MethodOptions, "/v1/sites/shop/state", nil))
	if r.Code != http.StatusNoContent {
		t.Errorf("OPTIONS: %d %s", r.Code, r.Body.String())
	}

	SetHackMode(true)
	r = httptest.NewRecorder()
	gate.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/v1/sites/shop/state", nil))
	if r.Code != http.StatusGone || !strings.Contains(r.Body.String(), "legacy_storage_removed") {
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
