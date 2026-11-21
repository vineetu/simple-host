package servertest

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	model "github.com/vsriram/simple-host/host/model"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
)

// familyDNS stands in for DNS: every name resolves to famIP (this server,
// through the CNAME target) unless listed in away; TXT records by name.
type FamilyDNS struct {
	Txt  map[string]string
	Away map[string]string // host (or "*.<suffix>") -> another address
}

// liveFamily connects *.<suffix> for p and brings it all the way to live:
// the TXT record, the check, the operator's certificate and the issuer's
// ready marker.
func (a *PrivateApp) LiveFamily(t *testing.T, famDir string, dns *FamilyDNS, p Person, suffix, prefix string, canonical bool, rank int) model.AddressFamily {
	t.Helper()
	r := a.At(t, "POST", PcSiteDomain, "/v1/me/address-families",
		map[string]any{"suffix": "*." + suffix, "site_prefix": prefix, "canonical": canonical, "rank": rank},
		map[string]string{"X-API-Key": p.Key})
	if r.Status != http.StatusCreated {
		t.Fatalf("connect *.%s: %d %s", suffix, r.Status, r.Body)
	}
	id := r.JSON(t)["id"].(string)
	f, err := sitesrepo.GetFamilyByID(context.Background(), a.Database, id)
	if err != nil {
		t.Fatal(err)
	}
	dns.Txt["_simple-host."+suffix] = f.Token
	a.Sites.CheckFamily(context.Background(), f, a.Sites.Hosts.ServerAddrs(context.Background()))
	if r := a.At(t, "PUT", PcSiteDomain, "/v1/admin/address-families/"+id+"/cert-mode",
		map[string]string{"cert_mode": "wildcard", "cert_name": suffix}, map[string]string{"X-API-Key": a.Admin}); r.Status != 200 {
		t.Fatalf("cert: %d %s", r.Status, r.Body)
	}
	f, _ = sitesrepo.GetFamilyByID(context.Background(), a.Database, id)
	if !f.VerifiedAt.Valid {
		t.Fatalf("*.%s not verified: %+v", suffix, f)
	}
	req, err := os.ReadFile(filepath.Join(famDir, "requests", suffix))
	if err != nil {
		t.Fatalf("no issuer request: %v", err)
	}
	want := f.Token + "\n../by-id/" + f.UserID + "\nwildcard\n" + suffix + "\n" + prefix + "\nwww\n"
	if string(req) != want {
		t.Fatalf("request = %q, want %q", req, want)
	}
	if got := a.Sites.Disk.FamilyLinkTarget(suffix); got != "../by-id/"+f.UserID {
		t.Fatalf("family link = %q", got)
	}
	MarkFamilyReady(t, famDir, suffix, prefix)
	a.Sites.Hosts.RefreshFamilies(context.Background())
	return f
}

func MarkFamilyReady(t *testing.T, famDir, suffix, prefix string) {
	t.Helper()
	body := "prefix=" + prefix + "\ncert=" + suffix + "\nexpires=" + "1900000000" + "\nreserved=www\n"
	if err := os.WriteFile(filepath.Join(famDir, "ready", suffix), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
