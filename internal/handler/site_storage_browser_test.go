package handler

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestStorageStoryBrowser(t *testing.T) {
	if os.Getenv("STORAGE_STORY_BROWSER") != "1" {
		t.Skip("set STORAGE_STORY_BROWSER=1 with Playwright available")
	}
	a, cert := newSiteApp(t, "canonical")
	owner := a.newPerson(t, "shop-browser-owner")
	alice := a.newPerson(t, "shop-browser-alice")
	bob := a.newPerson(t, "shop-browser-bob")
	a.deploy(t, owner, "shop")
	_, handle := a.userID(t, owner)
	markReady(t, cert, handle)
	host := "shop." + handle + "." + pcSiteDomain
	sid := a.siteID(t, owner, "shop")
	key := map[string]string{"X-API-Key": owner.key}
	base := "/v1/sites/shop/storage"
	if res := a.at(t, "PUT", pcSiteDomain, base+"/resources/orders", map[string]string{"kind": "sqlite", "read": "own", "write": "signed-in", "write_mode": "add"}, key); res.status != 201 {
		t.Fatalf("resource: %d", res.status)
	}
	if res := a.at(t, "POST", pcSiteDomain, base+"/sqlite/orders/schema", `{"sql":"CREATE TABLE orders (id INTEGER PRIMARY KEY,item TEXT NOT NULL,quantity INTEGER NOT NULL,status TEXT DEFAULT 'placed',created_at TEXT)"}`, key); res.status != 200 {
		t.Fatalf("schema: %d %s", res.status, res.body)
	}
	if res := a.at(t, "POST", pcSiteDomain, base+"/sqlite/orders/schema", `{"sql":"CREATE TABLE order_changes (id INTEGER PRIMARY KEY,order_id INTEGER NOT NULL REFERENCES orders(id),kind TEXT CHECK(kind IN ('change','note','cancel_request')),details TEXT,created_at TEXT)"}`, key); res.status != 200 {
		t.Fatalf("change schema: %d %s", res.status, res.body)
	}
	fixture := map[string]any{"url": a.srv.URL, "host": host, "ownerKey": owner.key, "cookies": []string{visitorCookieHost + "=" + a.session(t, alice, sid, host), visitorCookieHost + "=" + a.session(t, bob, sid, host)}}
	raw, _ := json.Marshal(fixture)
	fp := filepath.Join(t.TempDir(), "fixture.json")
	if err := os.WriteFile(fp, raw, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", "scripts/e2e-storage-shop.js")
	cmd.Dir = "../.."
	cmd.Env = append(os.Environ(), "STORAGE_SHOP_FIXTURE="+fp)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("browser check: %v\n%s", err, out)
	}
	t.Log(string(out))
}
