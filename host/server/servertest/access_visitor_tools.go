package servertest

import (
	"context"
	"strings"
	"testing"

	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"

	accountapp "github.com/vsriram/simple-host/host/access"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/handler"
)

type visitorFixture struct {
	StorageTestSite
	Token string
}

func NewVisitorFixture(t *testing.T) visitorFixture {
	t.Helper()
	s := NewStorageTestSite(t)
	o := NewTestOAuthHandler(s.A.Database, accountapp.New(s.A.Database), config.Config{PublicBaseURL: s.A.Srv.URL, SiteDomain: PcSiteDomain, ContentHost: PcContentHost, CNAMETarget: "cname." + PcSiteDomain})
	o.SetPersonSiteResolver(s.A.Sites.Hosts.ReturnSite)
	handler.SetVisitorTools(s.A.Conn, s.A.Access, s.A.Data, o)
	// These fixtures use an already verified account, then exercise the real
	// OAuth authorization and MCP boundary as a different person from the owner.
	u, err := accountsrepo.GetUserByID(context.Background(), s.A.Database, s.Ids["bob"])
	if err != nil {
		t.Fatal(err)
	}
	key, undo, err := accountsrepo.IssueInternalKey(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer undo()
	client := s.A.RegisterClient(t, TestRedirect)
	if _, err := s.A.Database.Exec(`UPDATE oauth_clients SET client_name='ChatGPT' WHERE client_id=$1`, client); err != nil {
		t.Fatal(err)
	}
	tok := s.A.Connect(t, Person{Email: u.Username, Key: key}, client, TestRedirect)["access_token"].(string)
	return visitorFixture{s, tok}
}
func (f visitorFixture) Call(t *testing.T, name string, args map[string]any, want string) map[string]any {
	t.Helper()
	text, out, bad := ToolResultOf(t, f.A.Rpc(t, f.Token, "tools/call", map[string]any{"name": name, "arguments": args}))
	if want != "" {
		if !bad || !strings.Contains(text, want) {
			t.Fatalf("%s: want error %q, got %v %s", name, want, bad, text)
		}
		return nil
	}
	if bad {
		t.Fatalf("%s: %s", name, text)
	}
	for _, secret := range []string{f.Owner.Email, f.OwnerID, f.Ids["alice"], f.Ids["bob"]} {
		if strings.Contains(text, secret) {
			t.Fatalf("%s exposes account identity", name)
		}
	}
	return out
}
func (f visitorFixture) Args(kind, resource string) map[string]any {
	return map[string]any{"address": "https://" + f.Host + "/", "kind": kind, "resource": resource}
}
func (f visitorFixture) Named(t *testing.T) {
	t.Helper()
	u, err := accountsrepo.GetUserByID(context.Background(), f.A.Database, f.Ids["bob"])
	if err != nil {
		t.Fatal(err)
	}
	if err = sitesrepo.SetSiteAccess(context.Background(), f.A.Database, f.Sid, "specific"); err != nil {
		t.Fatal(err)
	}
	if _, err = f.A.Database.Exec(`INSERT INTO site_viewers(site_id,email) VALUES($1,$2)`, f.Sid, u.Username); err != nil {
		t.Fatal(err)
	}
}
