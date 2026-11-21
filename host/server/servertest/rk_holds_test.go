package servertest

import (
	"database/sql"
	"net/http"
	"strings"
	"testing"
	"time"

	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
)

// A site hold (operator take-down) refuses the owner's domain check,
// disconnect and rollback, and refuses even the admin key on the ordinary
// storage write path; each answers 403 site_suspended and changes nothing.
func TestRkSiteHoldRefusesDomainRollbackAndAdminStorage(t *testing.T) {
	a := NewPersonApp(t, "serve")
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "shop")
	if r := a.At(t, "PUT", PcSiteDomain, "/v1/sites/shop/files", rkFiles("v2"), rkKey(olive.Key)); r.Status != http.StatusOK {
		t.Fatalf("v2: %d %s", r.Status, r.Body)
	}
	shopID := a.SiteID(t, olive, "shop")
	const claimed = "olive-hold.simple-host.test"
	okey, admin := rkKey(olive.Key), rkKey(a.Admin)
	data := "/v1/sites/shop/storage/kv/notes/keys/n"
	if r := a.At(t, "POST", PcSiteDomain, "/v1/sites/shop/domain", map[string]string{"domain": claimed}, okey); r.Status != http.StatusOK {
		t.Fatalf("claim: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PUT", PcSiteDomain, "/v1/sites/shop/storage/resources/notes", StorageRules("kv", RulesPublic), okey); r.Status/100 != 2 {
		t.Fatalf("storage resource: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", PcSiteDomain, "/v1/admin/sites/"+shopID+"/suspend", map[string]string{"reason": "report"}, admin); r.Status != http.StatusOK {
		t.Fatalf("suspend: %d %s", r.Status, r.Body)
	}
	withOrigin := func(key string) map[string]string {
		return map[string]string{"X-API-Key": key, "Origin": "https://" + claimed}
	}
	for _, c := range []struct {
		name, method, host, path string
		body                     any
		headers                  map[string]string
	}{
		{"check domain", "POST", PcSiteDomain, "/v1/sites/shop/domain/check", nil, okey},
		{"disconnect domain", "DELETE", PcSiteDomain, "/v1/sites/shop/domain", nil, okey},
		{"rollback", "PUT", PcSiteDomain, "/v1/sites/shop/active-version", map[string]int{"version_number": 1}, okey},
		{"admin key storage write", "PUT", claimed, data, map[string]any{"value": 2}, withOrigin(a.Admin)},
		{"owner key storage write", "PUT", claimed, data, map[string]any{"value": 2}, withOrigin(olive.Key)},
	} {
		rkRefusal(t, c.name, a.At(t, c.method, c.host, c.path, c.body, c.headers), http.StatusForbidden, "site_suspended")
	}
	var active int
	var domain sql.NullString
	if err := a.Database.QueryRow(`SELECT active_version, custom_domain FROM sites WHERE id = $1`, shopID).Scan(&active, &domain); err != nil || active != 2 || domain.String != claimed {
		t.Fatalf("a refused request changed the site: active=%d domain=%q %v", active, domain.String, err)
	}
}

// Suspending an account takes down every one of its sites with a real 410
// (a site taken down on its own stays down after re-enabling), blocks its
// key, connector token, publishing and on-site storage writes, and a key
// from a fresh sign-in is refused as well.
func TestRkAccountSuspensionServes410AndRefusesWrites(t *testing.T) {
	a := NewPersonApp(t, "serve")
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "shop")
	a.Deploy(t, olive, "blog")
	oliveID, oh := a.UserID(t, olive)
	host := oh + "." + PcSiteDomain
	okey, admin := rkKey(olive.Key), rkKey(a.Admin)
	bearer := map[string]string{"Authorization": "Bearer " + a.ConnectResource(t, olive, a.Srv.URL)}
	if r := a.At(t, "GET", PcSiteDomain, "/v1/sites", nil, bearer); r.Status != http.StatusOK {
		t.Fatalf("token before: %d %s", r.Status, r.Body)
	}
	blogID := a.SiteID(t, olive, "blog")
	if r := a.At(t, "POST", PcSiteDomain, "/v1/admin/sites/"+blogID+"/suspend", map[string]string{"reason": "copyright notice"}, admin); r.Status != http.StatusOK {
		t.Fatalf("suspend blog: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", PcSiteDomain, "/v1/admin/users/"+oliveID+"/suspend", map[string]string{"reason": "spam"}, admin); r.Status != http.StatusOK {
		t.Fatalf("suspend person: %d %s", r.Status, r.Body)
	}

	for _, p := range []string{"/shop/", "/shop/sub/", "/shop/missing.png"} {
		if r := a.At(t, "GET", host, p, nil, nil); r.Status != http.StatusGone || !strings.Contains(string(r.Body), "taken down") {
			t.Fatalf("site %s while the account is suspended: %d %s", p, r.Status, r.Body)
		}
	}
	for _, c := range []struct {
		name, method, host, path string
		body                     any
		headers                  map[string]string
		code                     string
	}{
		{"key", "GET", PcSiteDomain, "/v1/sites", nil, okey, "account_suspended"},
		{"connector token", "GET", PcSiteDomain, "/v1/sites", nil, bearer, "account_suspended"},
		{"publish", "PUT", PcSiteDomain, "/v1/sites/shop/files", rkFiles("late"), okey, "account_suspended"},
		{"on-site storage write", "PUT", host, "/v1/sites/shop/storage/kv/notes/keys/n", map[string]any{"value": 1}, map[string]string{"X-API-Key": olive.Key, "Origin": "https://" + host}, "site_suspended"}, // the account hold takes its sites down
	} {
		rkRefusal(t, c.name+" while suspended", a.At(t, c.method, c.host, c.path, c.body, c.headers), http.StatusForbidden, c.code)
	}
	var keys int
	if err := a.Database.QueryRow(`SELECT count(*) FROM api_keys WHERE user_id = $1`, oliveID).Scan(&keys); err != nil || keys == 0 {
		t.Fatalf("keys deleted: %d %v", keys, err)
	}

	// A fresh sign-in by email code yields a key that is refused too.
	if err := accountsrepo.CreateAuthToken(t.Context(), a.Database, olive.Email, "424242", "link-token-suspended", time.Now().Add(time.Minute), "dashboard", sql.NullString{}, sql.NullString{}); err != nil {
		t.Fatal(err)
	}
	r := a.At(t, "POST", a.AppHost(), "/v1/auth/verify", map[string]string{"email": olive.Email, "code": "424242"}, nil)
	if fresh, _ := r.JSON(t)["api_key"].(string); fresh != "" {
		rkRefusal(t, "fresh sign-in key", a.At(t, "GET", PcSiteDomain, "/v1/sites", nil, rkKey(fresh)), http.StatusForbidden, "account_suspended")
	} else if r.Status != http.StatusForbidden {
		t.Fatalf("fresh sign-in of a suspended account: %d %s", r.Status, r.Body)
	}

	// Re-enabling brings the account and its sites back, except the site
	// that was taken down on its own.
	if r := a.At(t, "POST", PcSiteDomain, "/v1/admin/users/"+oliveID+"/enable", nil, admin); r.Status != http.StatusOK {
		t.Fatalf("enable: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", host, "/shop/", nil, nil); r.Status != http.StatusOK {
		t.Fatalf("site after enable: %d", r.Status)
	}
	if r := a.At(t, "GET", host, "/blog/", nil, nil); r.Status != http.StatusGone {
		t.Fatalf("separately suspended site after enable: %d", r.Status)
	}
}

// The admin's export of everything holds every owner's live sites, with
// each file's bytes.
func TestRkAdminExportAllHoldsEveryOwner(t *testing.T) {
	a := NewPersonApp(t, "serve")
	olive, oscar := a.NewPerson(t, "olive"), a.NewPerson(t, "oscar")
	a.Deploy(t, olive, "shop")
	a.Deploy(t, oscar, "shop")
	if r := a.At(t, "PUT", PcSiteDomain, "/v1/sites/shop/files", rkFiles("oscar's own page"), rkKey(oscar.Key)); r.Status != http.StatusOK {
		t.Fatalf("oscar v2: %d %s", r.Status, r.Body)
	}
	_, oh := a.UserID(t, olive)
	_, sh := a.UserID(t, oscar)
	if r := a.At(t, "GET", PcSiteDomain, "/v1/admin/export.tar.gz", nil, rkKey(olive.Key)); r.Status != http.StatusNotFound {
		t.Fatalf("non-admin export all: %d", r.Status)
	}
	r := a.At(t, "GET", PcSiteDomain, "/v1/admin/export.tar.gz", nil, rkKey(a.Admin))
	if r.Status != http.StatusOK || r.Header.Get("Content-Type") != "application/gzip" {
		t.Fatalf("export all: %d %s", r.Status, r.Header.Get("Content-Type"))
	}
	seen := rkTarFiles(t, r.Body)
	for path, want := range map[string]string{oh + "/shop/files/index.html": "<h1>shop</h1>", sh + "/shop/files/index.html": "oscar's own page"} {
		if got, ok := seen[path]; !ok || got != want {
			t.Fatalf("archive %s = %q (present %v), want %q; %d entries", path, got, ok, want, len(seen))
		}
	}
}
