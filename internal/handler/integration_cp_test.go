package handler

import (
	"net/http"
	"os"
	"strings"
	"testing"
)

// How the completeness-plan features meet: Recently deleted (soft delete),
// operator take-down, named keys, handle changes, the owner app's routes and
// custom-domain certificates. Needs DB_DSN (db/schema.sql applied).

// A site the operator took down cannot be deleted by its owner; a site taken
// down while in Recently deleted comes back from Restore still taken down.
func TestSoftDeleteMeetsTakeDown(t *testing.T) {
	a, dir := newSiteApp(t, "canonical")
	olive := a.newPerson(t, "olive")
	a.deploy(t, olive, "shop")
	a.deploy(t, olive, "blog")
	uid, oh := a.userID(t, olive)
	markReady(t, dir, oh)
	const apex = pcSiteDomain
	okey := map[string]string{"X-API-Key": olive.key}
	admin := map[string]string{"X-API-Key": a.admin}
	shopID := a.siteID(t, olive, "shop")
	blogID := a.siteID(t, olive, "blog")

	// Taken down: the owner's delete is refused and nothing moves.
	if r := a.at(t, "POST", apex, "/v1/admin/sites/"+shopID+"/suspend", map[string]string{"reason": "report"}, admin); r.status != 200 {
		t.Fatalf("suspend: %d %s", r.status, r.body)
	}
	if r := a.at(t, "DELETE", apex, "/v1/sites/shop", nil, okey); r.status != http.StatusForbidden || r.json(t)["code"] != "site_suspended" {
		t.Fatalf("owner deleted a taken-down site: %d %s", r.status, r.body)
	}
	if _, err := os.Stat(a.sites.disk.SiteDir(uid, "shop")); err != nil {
		t.Fatalf("taken-down site's files moved: %v", err)
	}
	// The owner app's own writes are refused too.
	if r := a.at(t, "DELETE", apex, "/v1/sites/shop/collections/rsvps", map[string]string{"confirm": "rsvps"}, okey); r.status != http.StatusForbidden || r.json(t)["code"] != "site_suspended" {
		t.Fatalf("clear list on a taken-down site: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", apex, "/v1/sites/shop/domain/check", nil, okey); r.status != http.StatusForbidden || r.json(t)["code"] != "site_suspended" {
		t.Fatalf("check domain on a taken-down site: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", apex, "/v1/admin/sites/"+shopID+"/restore", nil, admin); r.status != 200 {
		t.Fatalf("unsuspend: %d %s", r.status, r.body)
	}

	// Deleted, then taken down while in Recently deleted: no folder appears
	// where the site used to be served, and Restore brings it back down.
	if r := a.at(t, "DELETE", apex, "/v1/sites/blog", nil, okey); r.status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", apex, "/v1/admin/sites/"+blogID+"/suspend", map[string]string{"reason": "copyright notice"}, admin); r.status != 200 {
		t.Fatalf("suspend a deleted site: %d %s", r.status, r.body)
	}
	if _, err := os.Stat(a.sites.disk.SiteDir(uid, "blog")); !os.IsNotExist(err) {
		t.Fatalf("suspending a deleted site recreated its served folder: %v", err)
	}
	r := a.at(t, "POST", apex, "/v1/sites/blog/restore", nil, okey)
	if r.status != 200 || r.json(t)["suspended"] != true || r.json(t)["suspended_reason"] != "copyright notice" {
		t.Fatalf("restore of a taken-down site: %d %s", r.status, r.body)
	}
	if !a.sites.disk.IsSuspended(uid, "blog") {
		t.Fatal("restored site lost its take-down marker")
	}
	if r := a.at(t, "GET", "blog."+oh+"."+pcSiteDomain, "/", nil, nil); r.status != http.StatusGone {
		t.Fatalf("restored taken-down site serves: %d", r.status)
	}
	if r := a.at(t, "POST", apex, "/v1/admin/sites/"+blogID+"/restore", nil, admin); r.status != 200 {
		t.Fatalf("unsuspend: %d %s", r.status, r.body)
	}
	if a.sites.disk.IsSuspended(uid, "blog") {
		t.Fatal("marker left after restore")
	}
}

// The organiser's download-all and an owner's export link leave out sites in
// Recently deleted; deleting an account also clears its deleted sites.
func TestSoftDeleteMeetsExportAndAccountDelete(t *testing.T) {
	a, dir := newSiteApp(t, "canonical")
	olive := a.newPerson(t, "olive")
	a.deploy(t, olive, "keep")
	a.deploy(t, olive, "gone")
	uid, oh := a.userID(t, olive)
	markReady(t, dir, oh)
	const apex = pcSiteDomain
	okey := map[string]string{"X-API-Key": olive.key}
	admin := map[string]string{"X-API-Key": a.admin}
	goneID := a.siteID(t, olive, "gone")

	r := a.at(t, "POST", apex, "/v1/sites/gone/export-link", nil, okey)
	if r.status != 200 {
		t.Fatalf("export link: %d %s", r.status, r.body)
	}
	link, _ := r.json(t)["url"].(string)
	i := strings.Index(link, "/v1/export?")
	if i < 0 {
		t.Fatalf("export link url: %q", link)
	}
	if r := a.at(t, "DELETE", apex, "/v1/sites/gone", nil, okey); r.status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", apex, link[i:], nil, nil); r.status != http.StatusNotFound {
		t.Fatalf("export link of a deleted site still downloads: %d", r.status)
	}

	r = a.at(t, "GET", apex, "/v1/admin/export.tar.gz", nil, admin)
	if r.status != 200 {
		t.Fatalf("download all: %d %s", r.status, r.body)
	}
	var hasKeep, hasGone bool
	for name := range tarNames(t, r.body) {
		hasKeep = hasKeep || strings.HasPrefix(name, oh+"/keep/")
		hasGone = hasGone || strings.HasPrefix(name, oh+"/gone/")
	}
	if !hasKeep || hasGone {
		names := tarNames(t, r.body)
		t.Fatalf("download all should hold keep and not the deleted site: %v", names)
	}

	// Account delete: immediate, and the Recently deleted site goes with it.
	if r := a.at(t, "DELETE", apex, "/v1/admin/users/"+uid, nil, admin); r.status != http.StatusNoContent {
		t.Fatalf("delete account: %d %s", r.status, r.body)
	}
	var n int
	if err := a.database.QueryRow(`SELECT count(*) FROM sites WHERE user_id = $1`, uid).Scan(&n); err != nil || n != 0 {
		t.Fatalf("account delete left %d site rows (%v)", n, err)
	}
	if _, err := os.Stat(a.sites.disk.TrashDir(uid, goneID)); !os.IsNotExist(err) {
		t.Fatalf("account delete left the deleted site's files: %v", err)
	}
}

// A suspended person cannot change their handle, list, mint or revoke keys,
// and the organiser cannot hand them a new key.
func TestSuspendedAccountKeysAndHandle(t *testing.T) {
	a, _ := newSiteApp(t, "canonical")
	olive := a.newPerson(t, "olive")
	a.deploy(t, olive, "shop")
	uid, oh := a.userID(t, olive)
	const apex = pcSiteDomain
	okey := map[string]string{"X-API-Key": olive.key}
	admin := map[string]string{"X-API-Key": a.admin}

	r := a.at(t, "GET", apex, "/v1/me/keys", nil, okey)
	keys, _ := r.json(t)["keys"].([]any)
	if r.status != 200 || len(keys) == 0 {
		t.Fatalf("keys before: %d %s", r.status, r.body)
	}
	keyID, _ := keys[0].(map[string]any)["id"].(string)

	if r := a.at(t, "POST", apex, "/v1/admin/users/"+uid+"/suspend", map[string]string{"reason": "spam"}, admin); r.status != 200 {
		t.Fatalf("suspend person: %d %s", r.status, r.body)
	}
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"PATCH", "/v1/me", map[string]string{"handle": oh + "-x"}},
		{"GET", "/v1/me/keys", nil},
		{"POST", "/v1/me/keys", map[string]string{"name": "CI"}},
		{"DELETE", "/v1/me/keys/" + keyID, nil},
		{"POST", "/v1/me/sign-out", nil},
		{"POST", "/v1/me/api-key/rotate", nil},
	} {
		r := a.at(t, c.method, apex, c.path, c.body, okey)
		if r.status != http.StatusForbidden || r.json(t)["code"] != "account_suspended" {
			t.Fatalf("%s %s as a suspended person: %d %s", c.method, c.path, r.status, r.body)
		}
	}
	if r := a.at(t, "POST", apex, "/v1/admin/users/"+uid+"/key", nil, admin); r.status != http.StatusConflict || r.json(t)["code"] != "account_suspended" {
		t.Fatalf("new key for a suspended person: %d %s", r.status, r.body)
	}
	var handle string
	if err := a.database.QueryRow(`SELECT handle FROM users WHERE id = $1`, uid).Scan(&handle); err != nil || handle != oh {
		t.Fatalf("handle changed while suspended: %q %v", handle, err)
	}

	// Re-enabled: the same key works again, keys and handle are theirs.
	if r := a.at(t, "POST", apex, "/v1/admin/users/"+uid+"/enable", nil, admin); r.status != 200 {
		t.Fatalf("enable: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", apex, "/v1/me/keys", nil, okey); r.status != 200 {
		t.Fatalf("keys after enable: %d %s", r.status, r.body)
	}
}

// The site list tells the owner app what a pending custom domain is doing:
// its certificate status and the earlier address the site still answers at.
func TestSiteListCarriesPendingDomainState(t *testing.T) {
	a, dir := newSiteApp(t, "canonical")
	olive := a.newPerson(t, "olive")
	a.deploy(t, olive, "shop")
	_, oh := a.userID(t, olive)
	markReady(t, dir, oh)
	const apex = pcSiteDomain
	okey := map[string]string{"X-API-Key": olive.key}
	claimed := "olv" + strings.ToLower(strings.TrimPrefix(oh, "olive-")) + "." + pcSiteDomain
	own := "shop-" + strings.TrimPrefix(oh, "olive-") + ".example.test"

	if r := a.at(t, "POST", apex, "/v1/sites/shop/domain", map[string]string{"domain": claimed}, okey); r.status != 200 {
		t.Fatalf("claim: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", apex, "/v1/sites/shop/domain", map[string]string{"domain": own}, okey); r.status != 200 {
		t.Fatalf("bind own domain: %d %s", r.status, r.body)
	}
	r := a.at(t, "GET", apex, "/v1/sites", nil, okey)
	sites, _ := r.json(t)["data"].([]any)
	if r.status != 200 || len(sites) != 1 {
		t.Fatalf("list: %d %s", r.status, r.body)
	}
	s := sites[0].(map[string]any)
	if s["custom_domain"] != own || s["previous_domain"] != claimed || s["domain_dns"] == nil {
		t.Fatalf("pending domain state missing from the site list: %s", r.body)
	}
	// Until the certificate process has seen DNS, the binding lapses in 24 h.
	if _, ok := s["domain_expires_at"]; !ok {
		t.Fatalf("pending binding without expiry: %s", r.body)
	}
	if _, err := a.database.Exec(`UPDATE sites SET domain_cert_status = 'issuing' WHERE custom_domain = $1`, own); err != nil {
		t.Fatal(err)
	}
	r = a.at(t, "GET", apex, "/v1/sites", nil, okey)
	s = r.json(t)["data"].([]any)[0].(map[string]any)
	if s["domain_certificate_status"] != "issuing" {
		t.Fatalf("certificate status: %s", r.body)
	}
	if _, ok := s["domain_expires_at"]; ok {
		t.Fatalf("a binding whose DNS points here should not show an expiry: %s", r.body)
	}
}
