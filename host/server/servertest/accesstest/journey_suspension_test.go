package accesstest

import (
	"net/http"
	"strings"
	"testing"

	. "github.com/vsriram/simple-host/host/access/featuretest"
	"github.com/vsriram/simple-host/host/edition"

	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
)

func TestSuspendSiteEndToEnd(t *testing.T) {
	a := NewApp(t)
	a.EnablePasscodes(t)
	olive, oscar := a.NewPerson(t, "olive"), a.NewPerson(t, "oscar")
	shopID := a.Deploy(t, olive, "shop")
	const apex = rt.SiteDomain
	okey := olive.Header()
	admin := map[string]string{"X-API-Key": a.Admin}

	// Only the admin may take a site down, and a reason is required.
	if r := a.At(t, "POST", apex, "/v1/admin/sites/"+shopID+"/suspend", map[string]string{"reason": "x"}, oscar.Header()); r.Status != 404 {
		t.Fatalf("non-admin suspend: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", apex, "/v1/admin/sites/"+shopID+"/suspend", map[string]string{"reason": "  "}, admin); r.Status != 400 || r.Code() != "reason_required" {
		t.Fatalf("no reason: %d %s", r.Status, r.Body)
	}
	r := a.At(t, "POST", apex, "/v1/admin/sites/"+shopID+"/suspend", map[string]string{"reason": "Phishing report 2026-09-27"}, admin)
	if r.Status != 200 || r.JSON(t)["suspended"] != true || r.JSON(t)["suspended_by"] != "site" {
		t.Fatalf("suspend: %d %s", r.Status, r.Body)
	}
	if !a.Disk.IsSuspended(olive.ID, "shop") {
		t.Fatal("suspend wrote no disk marker")
	}
	// The owner's change through an Access route is refused with a code.
	if r := a.At(t, "PUT", apex, "/v1/sites/shop/lock", map[string]string{"passcode": "secret-code"}, okey); r.Status != http.StatusForbidden || r.Code() != "site_suspended" {
		t.Fatalf("lock while taken down: %d %s", r.Status, r.Body)
	}

	// A lost marker is put back from the database at boot.
	_ = a.Disk.SetSuspended(olive.ID, "shop", false)
	a.Access.SyncSuspendMarkers(t.Context())
	if !a.Disk.IsSuspended(olive.ID, "shop") {
		t.Fatal("marker sync did not restore the marker")
	}

	// Restore puts it back exactly as it was.
	if r := a.At(t, "POST", apex, "/v1/admin/sites/"+shopID+"/restore", nil, admin); r.Status != 200 || r.JSON(t)["suspended"] != false {
		t.Fatalf("restore: %d %s", r.Status, r.Body)
	}
	if a.Disk.IsSuspended(olive.ID, "shop") {
		t.Fatal("restore left the marker")
	}
	if r := a.At(t, "PUT", apex, "/v1/sites/shop/lock", map[string]string{"passcode": "secret-code"}, okey); r.Status/100 != 2 {
		t.Fatalf("lock after restore: %d %s", r.Status, r.Body)
	}
}

// Wrong keys typed at /admin are rate limited per address, so the sign-in
// form's "Too many tries" answer is real.
func TestAdminWrongKeysRateLimited(t *testing.T) {
	l := WithLimits(t, map[string]string{"RATE_LIMIT_SITE_OPS": "3,1m"})
	a := NewApp(t, edition.Default(l))
	bad := map[string]string{"X-API-Key": "sh_admin_" + strings.Repeat("0", 48)}
	for i := 0; i < 3; i++ {
		if r := a.At(t, "GET", rt.SiteDomain, "/v1/admin/signups?group=x", nil, bad); r.Status != http.StatusUnauthorized {
			t.Fatalf("wrong key %d: %d %s", i, r.Status, r.Body)
		}
	}
	if r := a.At(t, "GET", rt.SiteDomain, "/v1/admin/signups?group=x", nil, bad); r.Status != http.StatusTooManyRequests {
		t.Fatalf("fourth wrong key: %d %s", r.Status, r.Body)
	}
}

func TestSuspendPersonEndToEnd(t *testing.T) {
	a := NewApp(t)
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "shop")
	blogID := a.Deploy(t, olive, "blog")
	const apex = rt.SiteDomain
	okey := olive.Header()
	admin := map[string]string{"X-API-Key": a.Admin}
	bearer := map[string]string{"Authorization": "Bearer " + a.ConnectResource(t, olive, a.Srv.URL)}
	if r := a.At(t, "GET", apex, "/v1/me", nil, bearer); r.Status != 200 {
		t.Fatalf("token before: %d %s", r.Status, r.Body)
	}
	// A site taken down on its own stays down when the person is re-enabled.
	if r := a.At(t, "POST", apex, "/v1/admin/sites/"+blogID+"/suspend", map[string]string{"reason": "copyright notice"}, admin); r.Status != 200 {
		t.Fatalf("suspend blog: %d %s", r.Status, r.Body)
	}

	if r := a.At(t, "POST", apex, "/v1/admin/users/"+olive.ID+"/suspend", map[string]string{"reason": "spam"}, admin); r.Status != 200 {
		t.Fatalf("suspend person: %d %s", r.Status, r.Body)
	}
	// Key and connector token stop working; nothing is deleted.
	r := a.At(t, "GET", apex, "/v1/me", nil, okey)
	if r.Status != http.StatusForbidden || r.Code() != "account_suspended" || r.JSON(t)["reason"] != "spam" {
		t.Fatalf("key while suspended: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", apex, "/v1/me", nil, bearer); r.Status != http.StatusForbidden || r.Code() != "account_suspended" {
		t.Fatalf("token while suspended: %d %s", r.Status, r.Body)
	}
	// Nor can it change its handle or its keys, or be given a new key.
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"PATCH", "/v1/me", map[string]string{"handle": olive.Handle + "-x"}},
		{"POST", "/v1/me/keys", map[string]string{"name": "CI"}},
		{"POST", "/v1/me/api-key/rotate", nil},
	} {
		if r := a.At(t, c.method, apex, c.path, c.body, okey); r.Status != http.StatusForbidden || r.Code() != "account_suspended" {
			t.Fatalf("%s %s while suspended: %d %s", c.method, c.path, r.Status, r.Body)
		}
	}
	if r := a.At(t, "POST", apex, "/v1/admin/users/"+olive.ID+"/key", nil, admin); r.Status != http.StatusConflict || r.Code() != "account_suspended" {
		t.Fatalf("new key while suspended: %d %s", r.Status, r.Body)
	}
	var keys int
	if err := a.Database.QueryRow(`SELECT count(*) FROM api_keys WHERE user_id = $1`, olive.ID).Scan(&keys); err != nil || keys == 0 {
		t.Fatalf("keys deleted: %d %v", keys, err)
	}
	// The admin account cannot be suspended.
	var adminID string
	if err := a.Database.QueryRow(`SELECT id FROM users WHERE is_admin ORDER BY created_at LIMIT 1`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	if r := a.At(t, "POST", apex, "/v1/admin/users/"+adminID+"/suspend", map[string]string{"reason": "x"}, admin); r.Status != 400 {
		t.Fatalf("suspend admin: %d %s", r.Status, r.Body)
	}

	// Re-enable brings key and token back; a site taken down on its own stays down.
	if r := a.At(t, "POST", apex, "/v1/admin/users/"+olive.ID+"/enable", nil, admin); r.Status != 200 {
		t.Fatalf("enable: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", apex, "/v1/me", nil, okey); r.Status != 200 {
		t.Fatalf("key after enable: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", apex, "/v1/me", nil, bearer); r.Status != 200 {
		t.Fatalf("token after enable: %d %s", r.Status, r.Body)
	}
	if !a.Disk.IsSuspended(olive.ID, "blog") || a.Disk.IsSuspended(olive.ID, "shop") {
		t.Fatal("take-down markers after enable: blog should stay down, shop should be up")
	}
}
