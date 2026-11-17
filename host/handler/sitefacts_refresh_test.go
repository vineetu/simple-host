package handler

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/model"
	static "github.com/vsriram/simple-host/host/static"
)

// The request fixture composes the already-existing page handler with the
// routing copy. This is not a Static listener, CLI or reverse proxy (D4-S4a).
func TestRoutingCopyAPIChanges(t *testing.T) {
	isolateQueueTest(t)
	a, dir := newSiteApp(t, "canonical")
	owner := a.newPerson(t, "copy-owner")
	uid, handle := a.userID(t, owner)
	markReady(t, dir, handle)
	data := t.TempDir()
	if err := os.WriteFile(filepath.Join(data, ".install-id"), []byte("fixture-install"), 0600); err != nil {
		t.Fatal(err)
	}
	failLoad := false
	load := func(ctx context.Context) (model.SiteTable, time.Time, error) {
		if failLoad {
			return model.SiteTable{}, time.Time{}, errors.New("test database unavailable")
		}
		return a.sites.LoadSiteTable(ctx)
	}
	r := static.New(static.Options{DataDir: data, StateDir: t.TempDir(), Product: "host", Load: load, Lookup: a.sites.LookupRoutingSite, Logf: func(string, ...any) {}})
	tick := func() {
		t.Helper()
		if err := r.Refresh(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	tick()
	key := map[string]string{"X-API-Key": owner.key}
	api := func(method, path string, body any, want int) {
		t.Helper()
		a.sites.SetSiteFacts(nil)
		res := a.at(t, method, pcSiteDomain, path, body, key)
		if res.status != want {
			t.Fatalf("%s %s: %d %s", method, path, res.status, res.body)
		}
		a.sites.WaitNotifications()
	}
	page := func(host string) resp {
		t.Helper()
		a.sites.SetSiteFacts(NewCopyFacts(r.Current().Table).View())
		res := a.at(t, "GET", host, "/", nil, nil)
		a.sites.SetSiteFacts(nil)
		return res
	}
	lookup := func(address model.RoutingAddress) {
		t.Helper()
		ok, err := r.Lookup(context.Background(), address)
		if err != nil || !ok {
			t.Fatalf("miss lookup: %v %v", ok, err)
		}
	}
	// Publish after the full load; rule 8's leaf lookup makes it reachable now.
	a.deploy(t, owner, "copy-first")
	oldHost := "copy-first." + handle + "." + pcSiteDomain
	if page(oldHost).status != 404 {
		t.Fatal("unloaded address was guessed")
	}
	lookup(model.RoutingAddress{Handle: handle, Name: "copy-first"})
	if got := page(oldHost); got.status != 200 || !strings.Contains(string(got.body), "copy-first") {
		t.Fatal("published address not served at once")
	}
	failLoad = true
	if err := r.Refresh(context.Background()); err == nil {
		t.Fatal("failed load unexpectedly succeeded")
	}
	if got := page(oldHost); got.status != 200 {
		t.Fatal("failed refresh stopped serving the old copy")
	}
	failLoad = false
	api("PATCH", "/v1/sites/copy-first", map[string]any{"name": "copy-next"}, 200)
	newHost := "copy-next." + handle + "." + pcSiteDomain
	lookup(model.RoutingAddress{Handle: handle, Name: "copy-next"})
	if got := page(newHost); got.status != 200 {
		t.Fatalf("renamed address: %d", got.status)
	}
	tick()
	if got := page(oldHost); got.status != 302 || !strings.Contains(got.header.Get("Location"), "copy-next.") {
		t.Fatalf("old name did not follow alias after one refresh: %d", got.status)
	}
	// Claimed names exercise connect/remove without external DNS or issuers.
	claimed := "copy-bound." + pcSiteDomain
	api("POST", "/v1/sites/copy-next/domain", map[string]any{"domain": claimed}, 200)
	lookup(model.RoutingAddress{Domain: claimed})
	if got := page(claimed); got.status != 200 {
		t.Fatalf("connected domain: %d", got.status)
	}
	tick()
	if got := page(newHost); got.status != 302 || got.header.Get("Location") != "https://"+claimed+"/" {
		t.Fatalf("main address did not refresh: %d %s", got.status, got.header.Get("Location"))
	}
	api("DELETE", "/v1/sites/copy-next/domain?domain="+claimed, nil, 204)
	tick()
	if got := page(claimed); got.status != 302 || !strings.Contains(got.header.Get("Location"), newHost) {
		t.Fatalf("removed name did not retire: %d %s", got.status, got.header.Get("Location"))
	}
	// A stale row cannot serve a vanished folder even before the refresh.
	api("DELETE", "/v1/sites/copy-next", nil, 204)
	if got := page(newHost); got.status != 404 {
		t.Fatalf("stale copy served deleted folder: %d", got.status)
	}
	tick()
	f := NewCopyFacts(r.Current().Table)
	if _, err := f.GetSiteByUser(context.Background(), uid, "copy-next"); err == nil {
		t.Fatal("deleted site retained in active routing lookup")
	}
	if got := page(newHost); got.status != 404 {
		t.Fatalf("deleted address after tick: %d", got.status)
	}
}

func TestRoutingCopyDiscoversNewFamily(t *testing.T) {
	isolateQueueTest(t)
	a, certDir := newSiteApp(t, "canonical")
	owner := a.newPerson(t, "copy-family")
	uid, handle := a.userID(t, owner)
	markReady(t, certDir, handle)
	a.deploy(t, owner, "family-page")
	familyDir := t.TempDir()
	a.sites.SetAddressFamilies(familyDir)
	data := t.TempDir()
	if err := os.WriteFile(filepath.Join(data, ".install-id"), []byte("family-install"), 0600); err != nil {
		t.Fatal(err)
	}
	r := static.New(static.Options{DataDir: data, StateDir: t.TempDir(), Product: "host", Load: a.sites.LoadSiteTable, Lookup: a.sites.LookupRoutingSite, Logf: func(string, ...any) {}})
	if err := r.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := r.Current()
	// The family becomes live after the last full load, as DNS/certificate
	// verification would make it live. No external DNS or issuer is used.
	if _, err := a.database.Exec(`INSERT INTO address_families(user_id,suffix,site_prefix,canonical,status,verified_at,cert_mode,cert_name) VALUES ($1,'new-family.example.org','family-',false,'active',now(),'wildcard','fixture')`, uid); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(familyDir, "ready"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(familyDir, "ready", "new-family.example.org"), []byte("prefix=family-\n"), 0600); err != nil {
		t.Fatal(err)
	}
	page := func() resp {
		a.sites.SetSiteFacts(NewCopyFacts(r.Current().Table).View())
		a.sites.refreshFamilies(context.Background())
		res := a.at(t, "GET", "page.new-family.example.org", "/", nil, nil)
		a.sites.SetSiteFacts(nil)
		return res
	}
	if got := page(); got.status != 404 {
		t.Fatalf("unknown family was guessed: %d", got.status)
	}
	if ok, err := r.Lookup(context.Background(), model.RoutingAddress{Domain: "page.new-family.example.org"}); err != nil || !ok {
		t.Fatalf("family miss: %v %v", ok, err)
	}
	if len(before.Table.Families) != 0 || len(r.Current().Table.Families) != 1 {
		t.Fatal("family fact not merged immutably")
	}
	if got := page(); got.status != 200 || !strings.Contains(string(got.body), "family-page") {
		t.Fatalf("family not served before next full load: %d %s", got.status, got.body)
	}
	if ok, err := r.Lookup(context.Background(), model.RoutingAddress{Domain: "page.new-family.example.org"}); err != nil || !ok || len(r.Current().Table.Families) != 1 {
		t.Fatal("repeated miss duplicated family")
	}
	// Discovery does not bypass the existing issuer readiness check.
	if err := os.Remove(filepath.Join(familyDir, "ready", "new-family.example.org")); err != nil {
		t.Fatal(err)
	}
	if got := page(); got.status != 404 {
		t.Fatalf("unready family served: %d", got.status)
	}
}
