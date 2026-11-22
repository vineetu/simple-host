package families_test

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/model"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sites/families"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
	"github.com/vsriram/simple-host/host/sites/sitesroute/fixture"
)

const famIP = "203.0.113.7"

// familyDNS stands in for DNS: every name resolves to famIP (this server)
// unless listed in away; TXT records by name.
type familyDNS struct {
	txt  map[string]string
	away map[string]string // host (or "*.<suffix>") -> another address
}

func stubFamilyDNS(t *testing.T) *familyDNS {
	t.Helper()
	d := &familyDNS{txt: map[string]string{}, away: map[string]string{}}
	oldTXT, oldHost := sites.LookupTXT, sites.LookupHost
	t.Cleanup(func() { sites.LookupTXT, sites.LookupHost = oldTXT, oldHost })
	sites.LookupTXT = func(_ context.Context, name string) ([]string, error) {
		if v, ok := d.txt[name]; ok {
			return []string{v}, nil
		}
		return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
	}
	sites.LookupHost = func(_ context.Context, name string) ([]string, error) {
		if ip, ok := d.away[name]; ok {
			return []string{ip}, nil
		}
		if _, suffix, ok := strings.Cut(name, "."); ok {
			if ip, ok := d.away["*."+suffix]; ok {
				return []string{ip}, nil
			}
		}
		return []string{famIP}, nil
	}
	return d
}

// familyApp is the fixture with the family routes mounted and address
// families on, forgetting families of earlier runs under the .test TLD.
type familyApp struct {
	*fixture.App
	famDir string
	dns    *familyDNS
}

func newFamilyApp(t *testing.T) *familyApp {
	t.Helper()
	a := fixture.New(t)
	families.Register(a.Mux, a.H, a.Auth, a.Notice)
	famDir := t.TempDir()
	for _, d := range []string{"requests", "ready", "failed"} {
		if err := os.MkdirAll(filepath.Join(famDir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	a.H.SetAddressFamilies(famDir)
	a.Hosts.SetPlatformZones(rt.SiteDomain, "simple-hack.test")
	if _, err := a.Database.Exec(`DELETE FROM address_families WHERE suffix LIKE '%.test'`); err != nil {
		t.Fatal(err)
	}
	return &familyApp{App: a, famDir: famDir, dns: stubFamilyDNS(t)}
}

func (f *familyApp) checks() families.Handler { return families.Handler{Handler: f.H} }

func (f *familyApp) connect(t *testing.T, p rt.Person, suffix string) rt.Resp {
	t.Helper()
	return f.API(t, p, "POST", "/v1/me/address-families", map[string]any{"suffix": suffix})
}

// Verification (TXT and the wildcard pointing here; the first account to
// prove a name wins it), failing checks and the lapse, the admin's proof
// exemption, and the unproven drop.
func TestFamilyLifecycle(t *testing.T) {
	f := newFamilyApp(t)
	olive, oscar := f.NewPerson(t, "olive"), f.NewPerson(t, "oscar")
	ctx := context.Background()
	ours := f.Hosts.ServerAddrs(ctx)
	mk := func(p rt.Person, suffix string) model.AddressFamily {
		t.Helper()
		r := f.connect(t, p, suffix)
		if r.Status != http.StatusCreated {
			t.Fatalf("connect %s: %d %s", suffix, r.Status, r.Body)
		}
		fam, _ := sitesrepo.GetFamilyByID(ctx, f.Database, r.JSON(t)["id"].(string))
		return fam
	}
	reload := func(fam model.AddressFamily) model.AddressFamily {
		got, _ := sitesrepo.GetFamilyByID(ctx, f.Database, fam.ID)
		return got
	}
	fo, fs := mk(olive, "*.race.test"), mk(oscar, "*.race.test") // both may wait on it
	f.checks().CheckFamily(ctx, fo, ours)
	if fo = reload(fo); fo.VerifiedAt.Valid {
		t.Fatalf("verified without TXT: %+v", fo)
	}
	f.dns.txt["_simple-host.race.test"] = fo.Token
	f.dns.away["*.race.test"] = "192.0.2.50"
	f.checks().CheckFamily(ctx, fo, ours)
	if fo = reload(fo); fo.VerifiedAt.Valid {
		t.Fatalf("verified pointing elsewhere: %+v", fo)
	}
	delete(f.dns.away, "*.race.test")
	f.checks().CheckFamily(ctx, fo, ours)
	if fo = reload(fo); !fo.VerifiedAt.Valid || fo.Status != "active" {
		t.Fatalf("not verified: %+v", fo)
	}
	if _, err := sitesrepo.GetFamilyByID(ctx, f.Database, fs.ID); err == nil {
		t.Fatal("the loser's claim survived")
	}
	delete(f.dns.txt, "_simple-host.race.test")
	f.checks().CheckFamily(ctx, fo, ours)
	if fo = reload(fo); fo.Status != "failing" || !fo.FailingSince.Valid {
		t.Fatalf("failing: %+v", fo)
	}
	if _, err := f.Database.Exec(`UPDATE address_families SET failing_since = now() - interval '100 hours' WHERE id = $1`, fo.ID); err != nil {
		t.Fatal(err)
	}
	f.checks().CheckFamily(ctx, reload(fo), ours)
	if _, err := sitesrepo.GetFamilyByID(ctx, f.Database, fo.ID); err == nil || f.Disk.FamilyLinkTarget("race.test") != "" {
		t.Fatalf("lapsed family kept: %v", err)
	}

	r := f.AsAdmin(t, "POST", "/v1/admin/users/"+olive.ID+"/address-families", map[string]any{"suffix": "*.exempt.test", "proof_exempt": true, "cert_name": "exempt.test", "canonical": false})
	if r.Status != http.StatusCreated || r.JSON(t)["proof_exempt"] != true || r.JSON(t)["dns_txt"] != nil {
		t.Fatalf("exempt: %d %s", r.Status, r.Body)
	}
	fe, _ := sitesrepo.GetFamilyByID(ctx, f.Database, r.JSON(t)["id"].(string))
	f.checks().CheckFamily(ctx, fe, ours)
	if fe = reload(fe); !fe.VerifiedAt.Valid {
		t.Fatalf("exempt not verified: %+v", fe)
	}
	if r := f.API(t, olive, "PUT", "/v1/admin/address-families/"+fe.ID+"/proof-exempt", map[string]bool{"proof_exempt": false}); r.Status == 200 {
		t.Fatal("owner changed the exemption")
	}
	if r := f.API(t, olive, "GET", "/v1/admin/address-families", nil); r.Status == 200 {
		t.Fatal("owner read the admin list")
	}
	fp := mk(oscar, "*.late.test")
	if _, err := f.Database.Exec(`UPDATE address_families SET bound_at = now() - interval '30 hours' WHERE id = $1`, fp.ID); err != nil {
		t.Fatal(err)
	}
	f.checks().CheckFamilies(ctx)
	if _, err := sitesrepo.GetFamilyByID(ctx, f.Database, fp.ID); err == nil {
		t.Fatal("never-proved family kept")
	}
}
