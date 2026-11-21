package accesstest

import (
	"testing"

	"github.com/vsriram/simple-host/host/access/accesshttp"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/model"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
)

// The internal visitor pass a connector tool call carries (even an admin
// owner's) is never a management credential, and it dies when revoked.
// (Pass use on a site and refusal on another site: gates_test.go.)
func TestVisitorPassIsNoManagementCredentialAndRevokes(t *testing.T) {
	a := NewApp(t)
	ann := a.NewPerson(t, "pass-admin")
	if _, err := a.Database.Exec(`UPDATE users SET is_admin = true WHERE id = $1`, ann.ID); err != nil {
		t.Fatal(err)
	}
	sid := a.Deploy(t, ann, "shop")
	host := ann.SiteHost("shop")
	pass, revoke, err := accountsrepo.IssueInternalVisitorKey(model.InternalVisitorPass{UserID: ann.ID, SiteID: sid, Host: host, AppName: "ChatGPT"})
	if err != nil {
		t.Fatal(err)
	}
	defer revoke()
	for _, path := range []string{"/v1/sites/shop/access", "/v1/me"} {
		if r := a.At(t, "GET", rt.SiteDomain, path, nil, map[string]string{"X-API-Key": pass}); r.Status != 401 {
			t.Fatalf("management accepted pass: %s %d", path, r.Status)
		}
	}
	headers := map[string]string{accesshttp.VisitorPassHeader: pass, "Origin": "https://" + host}
	if r, c := a.Probe(t, host, "shop", headers); r.Status != 200 || !c.VisitorAI || c.Owner {
		t.Fatalf("pass on its site: %d %s", r.Status, r.Body)
	}
	revoke()
	if r, _ := a.Probe(t, host, "shop", headers); r.Status == 200 {
		t.Fatalf("revoked pass accepted: %s", r.Body)
	}
}
