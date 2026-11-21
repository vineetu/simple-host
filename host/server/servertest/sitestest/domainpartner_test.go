package sitestest

import (
	"context"
	"strings"
	"testing"

	model "github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/server/servertest"
	"github.com/vsriram/simple-host/host/sites/siteshttp"
)

// A partner another site has bound (another account's, not proved yet) is
// never asked for or offered: the request carries no line 3.
func TestDomainPartnerBoundElsewhere(t *testing.T) {
	a := servertest.NewPrivateApp(t)
	ctx := context.Background()
	olive, oscar := a.NewPerson(t, "olive"), a.NewPerson(t, "oscar")
	a.Deploy(t, olive, "shop")
	a.Deploy(t, oscar, "grab")
	apex := servertest.Uniq("brand") + ".test"
	shop, grab := a.SiteID(t, olive, "shop"), a.SiteID(t, oscar, "grab")
	if _, err := a.Database.Exec(`UPDATE sites SET custom_domain = $2 WHERE id = $1`, shop, apex); err != nil {
		t.Fatal(err)
	}
	d := model.BoundDomain{SiteID: shop, Domain: apex, UserID: "u", Name: "shop", Token: "sh-0123456789abcdef0123456789abcdef"}
	if got := a.Sites.RequestablePartner(ctx, d); got != "www."+apex {
		t.Fatalf("free partner: %q", got)
	}
	if _, err := a.Database.Exec(`UPDATE sites SET custom_domain = $2 WHERE id = $1`, grab, "www."+apex); err != nil {
		t.Fatal(err)
	}
	if got := a.Sites.RequestablePartner(ctx, d); got != "" {
		t.Fatalf("partner bound to another account's site still asked for: %q", got)
	}
	if body := siteshttp.DomainCertRequestBody(d, a.Sites.RequestablePartner(ctx, d)); strings.Count(body, "\n") != 2 {
		t.Fatalf("request body: %q", body)
	}
	a.Sites.Hosts.SetDomainCertDir(t.TempDir())
	if p := a.Sites.PartnerInfoFor(apex, shop); p != nil {
		t.Fatalf("partner offered: %+v", p)
	}
	if _, err := a.Database.Exec(`UPDATE sites SET custom_domain = NULL WHERE id = ANY($1::uuid[])`, "{"+shop+","+grab+"}"); err != nil {
		t.Fatal(err)
	}
}
