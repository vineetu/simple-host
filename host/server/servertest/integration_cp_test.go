package servertest

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
)

// How the completeness-plan features meet: Recently deleted (soft delete),
// operator take-down, named keys, handle changes, the owner app's routes and
// custom-domain certificates. Needs DB_DSN (host/db/schema.sql applied).

// A site the operator took down cannot be deleted by its owner; a site taken
// down while in Recently deleted comes back from Restore still taken down.
func TestSoftDeleteMeetsTakeDown(t *testing.T) {
	a, dir := NewSiteApp(t, "canonical")
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "shop")
	a.Deploy(t, olive, "blog")
	uid, oh := a.UserID(t, olive)
	MarkReady(t, dir, oh)
	const apex = PcSiteDomain
	okey := map[string]string{"X-API-Key": olive.Key}
	admin := map[string]string{"X-API-Key": a.Admin}
	shopID := a.SiteID(t, olive, "shop")
	blogID := a.SiteID(t, olive, "blog")

	// Taken down: the owner's delete is refused and nothing moves.
	if r := a.At(t, "POST", apex, "/v1/admin/sites/"+shopID+"/suspend", map[string]string{"reason": "report"}, admin); r.Status != 200 {
		t.Fatalf("suspend: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "DELETE", apex, "/v1/sites/shop", nil, okey); r.Status != http.StatusForbidden || r.JSON(t)["code"] != "site_suspended" {
		t.Fatalf("owner deleted a taken-down site: %d %s", r.Status, r.Body)
	}
	if _, err := os.Stat(a.Sites.Disk.SiteDir(uid, "shop")); err != nil {
		t.Fatalf("taken-down site's files moved: %v", err)
	}
	// The owner app's own writes are refused too.
	if r := a.At(t, "PUT", apex, "/v1/sites/shop/storage/resources/notes", map[string]string{"kind": "kv"}, okey); r.Status != http.StatusForbidden || r.JSON(t)["code"] != "site_suspended" {
		t.Fatalf("storage change on a taken-down site: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", apex, "/v1/sites/shop/domain/check", nil, okey); r.Status != http.StatusForbidden || r.JSON(t)["code"] != "site_suspended" {
		t.Fatalf("check domain on a taken-down site: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", apex, "/v1/admin/sites/"+shopID+"/restore", nil, admin); r.Status != 200 {
		t.Fatalf("unsuspend: %d %s", r.Status, r.Body)
	}

	// Deleted, then taken down while in Recently deleted: no folder appears
	// where the site used to be served, and Restore brings it back down.
	if r := a.At(t, "DELETE", apex, "/v1/sites/blog", nil, okey); r.Status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", apex, "/v1/admin/sites/"+blogID+"/suspend", map[string]string{"reason": "copyright notice"}, admin); r.Status != 200 {
		t.Fatalf("suspend a deleted site: %d %s", r.Status, r.Body)
	}
	if _, err := os.Stat(a.Sites.Disk.SiteDir(uid, "blog")); !os.IsNotExist(err) {
		t.Fatalf("suspending a deleted site recreated its served folder: %v", err)
	}
	r := a.At(t, "POST", apex, "/v1/sites/blog/restore", nil, okey)
	if r.Status != 200 || r.JSON(t)["suspended"] != true || r.JSON(t)["suspended_reason"] != "copyright notice" {
		t.Fatalf("restore of a taken-down site: %d %s", r.Status, r.Body)
	}
	if !a.Sites.Disk.IsSuspended(uid, "blog") {
		t.Fatal("restored site lost its take-down marker")
	}
	if r := a.At(t, "GET", "blog."+oh+"."+PcSiteDomain, "/", nil, nil); r.Status != http.StatusGone {
		t.Fatalf("restored taken-down site serves: %d", r.Status)
	}
	if r := a.At(t, "POST", apex, "/v1/admin/sites/"+blogID+"/restore", nil, admin); r.Status != 200 {
		t.Fatalf("unsuspend: %d %s", r.Status, r.Body)
	}
	if a.Sites.Disk.IsSuspended(uid, "blog") {
		t.Fatal("marker left after restore")
	}
}

// The organiser's download-all and an owner's export link leave out sites in
// Recently deleted; deleting an account also clears its deleted sites.
func TestSoftDeleteMeetsExportAndAccountDelete(t *testing.T) {
	a, dir := NewSiteApp(t, "canonical")
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "keep")
	a.Deploy(t, olive, "gone")
	uid, oh := a.UserID(t, olive)
	MarkReady(t, dir, oh)
	const apex = PcSiteDomain
	okey := map[string]string{"X-API-Key": olive.Key}
	admin := map[string]string{"X-API-Key": a.Admin}
	goneID := a.SiteID(t, olive, "gone")

	r := a.At(t, "POST", apex, "/v1/sites/gone/export-link", nil, okey)
	if r.Status != 200 {
		t.Fatalf("export link: %d %s", r.Status, r.Body)
	}
	link, _ := r.JSON(t)["url"].(string)
	i := strings.Index(link, "/v1/export?")
	if i < 0 {
		t.Fatalf("export link url: %q", link)
	}
	if r := a.At(t, "DELETE", apex, "/v1/sites/gone", nil, okey); r.Status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", apex, link[i:], nil, nil); r.Status != http.StatusNotFound {
		t.Fatalf("export link of a deleted site still downloads: %d", r.Status)
	}

	r = a.At(t, "GET", apex, "/v1/admin/export.tar.gz", nil, admin)
	if r.Status != 200 {
		t.Fatalf("download all: %d %s", r.Status, r.Body)
	}
	var hasKeep, hasGone bool
	for name := range TarNames(t, r.Body) {
		hasKeep = hasKeep || strings.HasPrefix(name, oh+"/keep/")
		hasGone = hasGone || strings.HasPrefix(name, oh+"/gone/")
	}
	if !hasKeep || hasGone {
		names := TarNames(t, r.Body)
		t.Fatalf("download all should hold keep and not the deleted site: %v", names)
	}

	// Account delete: immediate, and the Recently deleted site goes with it.
	if r := a.At(t, "DELETE", apex, "/v1/admin/users/"+uid, nil, admin); r.Status != http.StatusNoContent {
		t.Fatalf("delete account: %d %s", r.Status, r.Body)
	}
	var n int
	if err := a.Database.QueryRow(`SELECT count(*) FROM sites WHERE user_id = $1`, uid).Scan(&n); err != nil || n != 0 {
		t.Fatalf("account delete left %d site rows (%v)", n, err)
	}
	if _, err := os.Stat(a.Sites.Disk.TrashDir(uid, goneID)); !os.IsNotExist(err) {
		t.Fatalf("account delete left the deleted site's files: %v", err)
	}
}

// A suspended person cannot change their handle, list, mint or revoke keys,
// and the organiser cannot hand them a new key.
func TestSuspendedAccountKeysAndHandle(t *testing.T) {
	a, _ := NewSiteApp(t, "canonical")
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "shop")
	uid, oh := a.UserID(t, olive)
	const apex = PcSiteDomain
	okey := map[string]string{"X-API-Key": olive.Key}
	admin := map[string]string{"X-API-Key": a.Admin}

	r := a.At(t, "GET", apex, "/v1/me/keys", nil, okey)
	keys, _ := r.JSON(t)["keys"].([]any)
	if r.Status != 200 || len(keys) == 0 {
		t.Fatalf("keys before: %d %s", r.Status, r.Body)
	}
	keyID, _ := keys[0].(map[string]any)["id"].(string)

	if r := a.At(t, "POST", apex, "/v1/admin/users/"+uid+"/suspend", map[string]string{"reason": "spam"}, admin); r.Status != 200 {
		t.Fatalf("suspend person: %d %s", r.Status, r.Body)
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
		r := a.At(t, c.method, apex, c.path, c.body, okey)
		if r.Status != http.StatusForbidden || r.JSON(t)["code"] != "account_suspended" {
			t.Fatalf("%s %s as a suspended person: %d %s", c.method, c.path, r.Status, r.Body)
		}
	}
	if r := a.At(t, "POST", apex, "/v1/admin/users/"+uid+"/key", nil, admin); r.Status != http.StatusConflict || r.JSON(t)["code"] != "account_suspended" {
		t.Fatalf("new key for a suspended person: %d %s", r.Status, r.Body)
	}
	var handle string
	if err := a.Database.QueryRow(`SELECT handle FROM users WHERE id = $1`, uid).Scan(&handle); err != nil || handle != oh {
		t.Fatalf("handle changed while suspended: %q %v", handle, err)
	}

	// Re-enabled: the same key works again, keys and handle are theirs.
	if r := a.At(t, "POST", apex, "/v1/admin/users/"+uid+"/enable", nil, admin); r.Status != 200 {
		t.Fatalf("enable: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", apex, "/v1/me/keys", nil, okey); r.Status != 200 {
		t.Fatalf("keys after enable: %d %s", r.Status, r.Body)
	}
}

// The site list tells the owner app what a pending custom domain is doing:
// its certificate status and the earlier address the site still answers at.
func TestSiteListCarriesPendingDomainState(t *testing.T) {
	a, dir := NewSiteApp(t, "canonical")
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "shop")
	_, oh := a.UserID(t, olive)
	MarkReady(t, dir, oh)
	const apex = PcSiteDomain
	okey := map[string]string{"X-API-Key": olive.Key}
	claimed := "olv" + strings.ToLower(strings.TrimPrefix(oh, "olive-")) + "." + PcSiteDomain
	own := "shop-" + strings.TrimPrefix(oh, "olive-") + ".example.test"

	if r := a.At(t, "POST", apex, "/v1/sites/shop/domain", map[string]string{"domain": claimed}, okey); r.Status != 200 {
		t.Fatalf("claim: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", apex, "/v1/sites/shop/domain", map[string]string{"domain": own}, okey); r.Status != 200 {
		t.Fatalf("bind own domain: %d %s", r.Status, r.Body)
	}
	r := a.At(t, "GET", apex, "/v1/sites", nil, okey)
	var sites []any
	_ = json.Unmarshal(r.Body, &sites)
	if r.Status != 200 || len(sites) != 1 {
		t.Fatalf("list: %d %s", r.Status, r.Body)
	}
	s := sites[0].(map[string]any)
	if s["custom_domain"] != own || s["previous_domain"] != claimed || s["domain_dns"] == nil {
		t.Fatalf("pending domain state missing from the site list: %s", r.Body)
	}
	// Until the certificate process has seen DNS, the binding lapses in 24 h.
	if _, ok := s["domain_expires_at"]; !ok {
		t.Fatalf("pending binding without expiry: %s", r.Body)
	}
	if _, err := a.Database.Exec(`UPDATE sites SET domain_cert_status = 'issuing' WHERE custom_domain = $1`, own); err != nil {
		t.Fatal(err)
	}
	r = a.At(t, "GET", apex, "/v1/sites", nil, okey)
	sites = nil
	_ = json.Unmarshal(r.Body, &sites)
	s = sites[0].(map[string]any)
	if s["domain_certificate_status"] != "issuing" {
		t.Fatalf("certificate status: %s", r.Body)
	}
	if _, ok := s["domain_expires_at"]; ok {
		t.Fatalf("a binding whose DNS points here should not show an expiry: %s", r.Body)
	}
}
