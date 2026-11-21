package datatest

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/vsriram/simple-host/host/access/accesshttp"
	"github.com/vsriram/simple-host/host/server/servertest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestStorageStoryBrowser(t *testing.T) {
	if os.Getenv("STORAGE_STORY_BROWSER") != "1" {
		t.Skip("set STORAGE_STORY_BROWSER=1 with Playwright available")
	}
	a, cert := servertest.NewSiteApp(t, "canonical")
	owner := a.NewPerson(t, "shop-browser-owner")
	alice := a.NewPerson(t, "shop-browser-alice")
	bob := a.NewPerson(t, "shop-browser-bob")
	a.Deploy(t, owner, "shop")
	_, handle := a.UserID(t, owner)
	servertest.MarkReady(t, cert, handle)
	host := "shop." + handle + "." + servertest.PcSiteDomain
	sid := a.SiteID(t, owner, "shop")
	key := map[string]string{"X-API-Key": owner.Key}
	base := "/v1/sites/shop/storage"
	if res := a.At(t, "PUT", servertest.PcSiteDomain, base+"/resources/orders", servertest.StorageRules("sqlite", servertest.RulesRecords), key); res.Status != 201 {
		t.Fatalf("resource: %d", res.Status)
	}
	if res := a.At(t, "POST", servertest.PcSiteDomain, base+"/sqlite/orders/schema", `{"sql":"CREATE TABLE orders (id INTEGER PRIMARY KEY,item TEXT NOT NULL,quantity INTEGER NOT NULL,status TEXT DEFAULT 'placed',created_at TEXT)"}`, key); res.Status != 200 {
		t.Fatalf("schema: %d %s", res.Status, res.Body)
	}
	if res := a.At(t, "POST", servertest.PcSiteDomain, base+"/sqlite/orders/schema", `{"sql":"CREATE TABLE order_changes (id INTEGER PRIMARY KEY,order_id INTEGER NOT NULL REFERENCES orders(id),kind TEXT CHECK(kind IN ('change','note','cancel_request')),details TEXT,created_at TEXT)"}`, key); res.Status != 200 {
		t.Fatalf("change schema: %d %s", res.Status, res.Body)
	}
	if res := a.At(t, "PUT", servertest.PcSiteDomain, base+"/resources/photos", servertest.StorageRules("files", servertest.RulesPublic), key); res.Status != 201 {
		t.Fatalf("photos resource: %d", res.Status)
	}
	photo, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=")
	for i := 0; i < 150; i++ {
		if res := a.At(t, "PUT", servertest.PcSiteDomain, fmt.Sprintf("%s/files/photos/objects/p%03d.png", base, i), string(photo), key); res.Status != 200 {
			t.Fatalf("photo %d: %d", i, res.Status)
		}
	}
	fixture := map[string]any{"url": a.Srv.URL, "host": host, "ownerKey": owner.Key, "cookies": []string{accesshttp.VisitorCookieHost + "=" + a.Session(t, alice, sid, host), accesshttp.VisitorCookieHost + "=" + a.Session(t, bob, sid, host)}}
	raw, _ := json.Marshal(fixture)
	fp := filepath.Join(t.TempDir(), "fixture.json")
	if err := os.WriteFile(fp, raw, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", "scripts/e2e/e2e-storage-shop.js")
	cmd.Dir = "../../.."
	cmd.Env = append(os.Environ(), "STORAGE_SHOP_FIXTURE="+fp)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("browser check: %v\n%s", err, out)
	}
	t.Log(string(out))
}
