package servertest

// The visitor gate on every kind of address, through the composed router:
// Access's real gate, Sites' real serving and Data's real storage together.
// The single-module rules live in accesstest, datatest and sitestest; the
// cases here are the ones that need all three.

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/access/gate"
	"github.com/vsriram/simple-host/host/config"
	model "github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sites/families"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
)

const rgDomainSQL = `UPDATE sites SET custom_domain = $2, domain_status = $3, domain_verified_at = CASE WHEN $3 = 'active' THEN now() ELSE NULL END WHERE id = $1`

// rgBind points the fixture site at domain with the given status.
func rgBind(t *testing.T, p *PasscodeApp, domain, status string) {
	t.Helper()
	if _, err := p.Database.ExecContext(context.Background(), rgDomainSQL, p.SiteID, domain, status); err != nil {
		t.Fatal(err)
	}
}

func rgUnbind(t *testing.T, p *PasscodeApp) {
	t.Helper()
	if _, err := p.Database.ExecContext(context.Background(), `UPDATE sites SET custom_domain = NULL, domain_status = NULL, domain_verified_at = NULL WHERE id = $1`, p.SiteID); err != nil {
		t.Fatal(err)
	}
}

func rgCookie(raw string) map[string]string {
	return map[string]string{"Cookie": gate.VisitorCookieHost + "=" + raw}
}

func rgNoCanary(t *testing.T, what string, r Resp) {
	t.Helper()
	if strings.Contains(string(r.Body), PasscodeCanary) {
		t.Fatalf("%s leaked the site: %d %s", what, r.Status, r.Body)
	}
}

// rgAddress is one kind of address the fixture site can be reached on. A
// custom domain reaches Go through nginx's /internal/passcode rewrite; a
// claimed name is served by Go itself.
type rgAddress struct {
	name, domain, status string
	viaMarker            bool
}

func (k rgAddress) get(t *testing.T, p *PasscodeApp, path string, h map[string]string) Resp {
	t.Helper()
	if k.viaMarker {
		path = "/internal/passcode" + path
	}
	return p.At(t, "GET", k.domain, path, nil, h)
}

var (
	rgClaimed  = rgAddress{"claimed name", "goa-trip." + PcSiteDomain, "active", false}
	rgVerified = rgAddress{"verified custom domain", "trip.example.org", "active", true}
	rgPending  = rgAddress{"pending custom domain", "waiting.example.org", "pending", true}
)

// A site with a passcode answers 401 with no bytes on its claimed name, its
// verified custom domain and a domain still waiting for proof; the right
// passcode unlocks that address (and, where it is checked, its saved data).
// A host nothing is bound to gets no form.
func TestGatePasscodeEveryAddressKind(t *testing.T) {
	for _, k := range []rgAddress{rgClaimed, rgVerified, rgPending} {
		t.Run(k.name, func(t *testing.T) {
			p := NewPasscodeApp(t)
			const code = "domain-pass-1"
			rgBind(t, p, k.domain, k.status)
			if k.status == "pending" {
				// The proof fetches the home page and needs a 2xx, so the
				// open site is served here, redirect rules included.
				if r := p.At(t, "PUT", PcSiteDomain, "/v1/sites/trip/files?keep=1", map[string]any{"files": map[string]string{"_redirects": "/old /app.js 301"}}, p.Okey); r.Status != 200 {
					t.Fatalf("redirect rules: %d %s", r.Status, r.Body)
				}
				if r := k.get(t, p, "/", nil); r.Status != 200 || !strings.Contains(string(r.Body), PasscodeCanary) {
					t.Fatalf("open site on a pending domain: %d %s", r.Status, r.Body)
				}
				if r := k.get(t, p, "/old", nil); r.Status != http.StatusMovedPermanently || r.Header.Get("Location") != "/app.js" {
					t.Fatalf("pending domain redirect: %d %v", r.Status, r.Header)
				}
			}
			if r := p.Lock(t, code); r.Status != 200 {
				t.Fatalf("lock: %d %s", r.Status, r.Body)
			}
			for _, path := range []string{"/", "/app.js", "/data.json", "/days/"} {
				r := k.get(t, p, path, nil)
				if r.Status != http.StatusUnauthorized || strings.Contains(string(r.Body), PasscodeCanary) || !strings.Contains(string(r.Body), `name="passcode"`) {
					t.Fatalf("%s locked: %d %s", path, r.Status, r.Body)
				}
			}
			if !k.viaMarker || k.status == "active" {
				// The site host forwards to the address; nothing leaks there either.
				rgNoCanary(t, "site host", p.At(t, "GET", p.Host, "/app.js", nil, nil))
			}
			if r := p.Unlock(t, k.domain, "wrong-pass-9", "/app.js", nil); r.Status != http.StatusUnauthorized {
				t.Fatalf("wrong passcode: %d %s", r.Status, r.Body)
			}
			r := p.Unlock(t, k.domain, code, "/app.js", nil)
			if r.Status != http.StatusSeeOther {
				t.Fatalf("unlock on %s: %d %s", k.domain, r.Status, r.Body)
			}
			ck := map[string]string{"Cookie": CookieHeader(t, r)}
			if r := k.get(t, p, "/app.js", ck); r.Status != 200 || !strings.Contains(string(r.Body), PasscodeCanary) {
				t.Fatalf("unlocked: %d %s", r.Status, r.Body)
			}
			if k.status == "active" {
				if r := p.At(t, "GET", k.domain, PasscodeData, nil, map[string]string{"Cookie": ck["Cookie"], "Origin": "https://" + k.domain}); r.Status != 200 {
					t.Fatalf("data after unlock: %d %s", r.Status, r.Body)
				}
			}
			// The unlock is this host's: the site's other address does not accept it.
			rgNoCanary(t, "site host with the domain's cookie", p.At(t, "GET", p.Host, "/app.js", nil, ck))
			// A host nothing is bound to: closed, and no form to try a passcode in.
			if r := p.At(t, "GET", "unknown.example.net", "/internal/passcode/app.js", nil, nil); r.Status != http.StatusUnauthorized || strings.Contains(string(r.Body), "<form") {
				t.Fatalf("unknown host: %d %s", r.Status, r.Body)
			}
		})
	}
}

// On the person path (no certificate for the person's site hosts yet) the
// unlock is per site: one site's passcode and cookie open only that site.
func TestGatePasscodePersonPathIsPerSite(t *testing.T) {
	p := NewPasscodeApp(t)
	oscar := p.NewPerson(t, "oscar")
	for _, s := range []string{"camp", "blog"} {
		files := map[string]any{"files": map[string]string{"index.html": s + " " + PasscodeCanary}}
		if r := p.At(t, "POST", PcSiteDomain, "/v1/sites/"+s+"/files", files, map[string]string{"X-API-Key": oscar.Key}); r.Status != http.StatusCreated {
			t.Fatalf("deploy %s: %d %s", s, r.Status, r.Body)
		}
		if r := p.At(t, "PUT", PcSiteDomain, "/v1/sites/"+s+"/lock", map[string]any{"passcode": "oscar-" + s}, map[string]string{"X-API-Key": oscar.Key}); r.Status != 200 {
			t.Fatalf("lock %s: %d %s", s, r.Status, r.Body)
		}
	}
	_, sh := p.UserID(t, oscar)
	ph := sh + "." + PcSiteDomain
	r := p.At(t, "GET", ph, "/camp/", nil, nil)
	if r.Status != http.StatusUnauthorized {
		t.Fatalf("person path locked: %d %s", r.Status, r.Body)
	}
	rgNoCanary(t, "person path", r)
	r = p.Unlock(t, ph, "oscar-camp", "/camp/", nil)
	if r.Status != http.StatusSeeOther {
		t.Fatalf("person path unlock: %d %s", r.Status, r.Body)
	}
	ck := map[string]string{"Cookie": CookieHeader(t, r)}
	if r := p.At(t, "GET", ph, "/camp/", nil, ck); r.Status != 200 {
		t.Fatalf("camp unlocked: %d", r.Status)
	}
	if r := p.At(t, "GET", ph, "/blog/", nil, ck); r.Status != http.StatusUnauthorized {
		t.Fatalf("camp's cookie opened blog: %d", r.Status)
	}
	if r := p.Unlock(t, ph, "oscar-camp", "/blog/", nil); r.Status != http.StatusUnauthorized {
		t.Fatalf("camp's passcode on blog: %d", r.Status)
	}
}

// Person hosts in serve mode: the content host still serves sites from disk,
// so a protected site's file there redirects to its own address and never
// carries the bytes.
func TestGatePasscodeContentHostServeMode(t *testing.T) {
	a := NewPersonApp(t, "serve")
	if err := a.Access.SetPasscodeKey(TestPasscodeKey); err != nil {
		t.Fatal(err)
	}
	WithPasscodeLimits(t, func(l *config.Limits) { l.SitePasscodes = true; l.PasscodeMinLength = 6 }, &a.Sites.Limits, &a.Access.Limits, &a.Data.Limits)
	olive := a.NewPerson(t, "olive")
	okey := map[string]string{"X-API-Key": olive.Key}
	if r := a.At(t, "POST", PcSiteDomain, "/v1/sites/trip/files", map[string]any{"files": map[string]string{"index.html": PasscodeCanary, "a.js": PasscodeCanary}}, okey); r.Status != http.StatusCreated {
		t.Fatalf("deploy: %d %s", r.Status, r.Body)
	}
	_, h := a.UserID(t, olive)
	if r := a.At(t, "GET", PcContentHost, "/internal/site-redirect/"+h+"/trip/a.js", nil, nil); r.Status != 200 {
		t.Fatalf("before the lock the content host serves: %d", r.Status)
	}
	if r := a.At(t, "PUT", PcSiteDomain, "/v1/sites/trip/lock", map[string]any{"passcode": "serve-mode"}, okey); r.Status != 200 {
		t.Fatalf("lock: %d %s", r.Status, r.Body)
	}
	for _, path := range []string{"/internal/site-redirect/" + h + "/trip/a.js?x=1", "/internal/passcode/" + h + "/trip/a.js?x=1"} {
		r := a.At(t, "GET", PcContentHost, path, nil, nil)
		if r.Status != http.StatusFound || r.Header.Get("Location") != "https://"+h+"."+PcSiteDomain+"/trip/a.js?x=1" || strings.Contains(string(r.Body), PasscodeCanary) {
			t.Fatalf("%s: %d %s %s", path, r.Status, r.Header.Get("Location"), r.Body)
		}
	}
	if r := a.At(t, "GET", h+"."+PcSiteDomain, "/trip/a.js", nil, nil); r.Status != http.StatusUnauthorized {
		t.Fatalf("person path: %d", r.Status)
	}
}

// rgViewerApp is olive's "trip" open only to mom; dad is signed in but not
// listed.
func rgViewerApp(t *testing.T) (p *PasscodeApp, mom, dad Person) {
	t.Helper()
	p = NewPasscodeApp(t)
	mom, dad = p.NewPerson(t, "mom"), p.NewPerson(t, "dad")
	r := p.At(t, "POST", PcSiteDomain, "/v1/sites/trip/viewers", map[string]any{"emails": []string{mom.Email}}, p.Okey)
	if r.Status != 200 {
		t.Fatalf("grant: %d %s", r.Status, r.Body)
	}
	return
}

// A site open to named viewers: on a claimed name and a verified custom
// domain a signed-out visitor is asked to sign in, a signed-in stranger is
// refused, a listed viewer with that host's own session gets the page, and a
// session minted on the ordinary site host counts for nothing there.
func TestGateNamedViewersEveryAddressKind(t *testing.T) {
	for _, k := range []rgAddress{rgClaimed, rgVerified} {
		t.Run(k.name, func(t *testing.T) {
			p, mom, dad := rgViewerApp(t)
			rgBind(t, p, k.domain, k.status)
			momSite := p.Session(t, mom, p.SiteID, p.Host)
			if r := k.get(t, p, "/app.js", nil); r.Status != http.StatusUnauthorized {
				t.Fatalf("signed out: %d %s", r.Status, r.Body)
			} else {
				rgNoCanary(t, "signed out", r)
			}
			rgNoCanary(t, "the site host's session", k.get(t, p, "/app.js", rgCookie(momSite)))
			if r := k.get(t, p, "/app.js", rgCookie(momSite)); r.Status == 200 {
				t.Fatalf("the site host's session accepted on %s", k.domain)
			}
			momHere := p.Session(t, mom, p.SiteID, k.domain)
			if r := k.get(t, p, "/app.js", rgCookie(momHere)); r.Status != 200 || !strings.Contains(string(r.Body), PasscodeCanary) {
				t.Fatalf("listed viewer: %d %s", r.Status, r.Body)
			}
			dadHere := p.Session(t, dad, p.SiteID, k.domain)
			r := k.get(t, p, "/app.js", rgCookie(dadHere))
			if r.Status != http.StatusForbidden {
				t.Fatalf("unlisted viewer: %d %s", r.Status, r.Body)
			}
			rgNoCanary(t, "unlisted viewer", r)
			rgNoCanary(t, "site host with the domain bound", p.At(t, "GET", p.Host, "/app.js", nil, nil))
		})
	}
}

// Named viewers restrict saved data too: a resource anyone may read, with or
// without the site passcode applying to it, is closed to everyone but the
// listed (and the owner's key); and a redirect file never discloses its
// destination to a visitor the gate refuses.
func TestGateNamedViewersStorageAndRedirects(t *testing.T) {
	p, mom, dad := rgViewerApp(t)
	momCk := p.Session(t, mom, p.SiteID, p.Host)
	dadCk := p.Session(t, dad, p.SiteID, p.Host)
	for _, mode := range []string{"inherit", "off"} {
		t.Run("site_passcode "+mode, func(t *testing.T) {
			res := "menu-" + mode
			if r := p.At(t, "PUT", PcSiteDomain, "/v1/sites/trip/storage/resources/"+res, `{"kind":"kv","site_passcode":"`+mode+`","rules":`+RulesPublic+`}`, p.Okey); r.Status/100 != 2 {
				t.Fatalf("resource: %d %s", r.Status, r.Body)
			}
			path := "/v1/sites/trip/storage/kv/" + res + "/keys/today"
			if r := p.At(t, "PUT", PcSiteDomain, path, map[string]any{"value": PasscodeCanary}, p.Okey); r.Status != 200 {
				t.Fatalf("put: %d %s", r.Status, r.Body)
			}
			for who, ck := range map[string]string{"signed out": "", "unlisted": dadCk} {
				r := p.At(t, "GET", p.Host, path, nil, Browser(p.Host, ck))
				rgNoCanary(t, "storage for "+who, r)
				if r.Status != http.StatusForbidden {
					t.Fatalf("storage for %s: %d %s", who, r.Status, r.Body)
				}
			}
			if r := p.At(t, "GET", p.Host, path, nil, Browser(p.Host, momCk)); r.Status != 200 || !strings.Contains(string(r.Body), PasscodeCanary) {
				t.Fatalf("storage for the listed viewer: %d %s", r.Status, r.Body)
			}
			if r := p.At(t, "GET", PcSiteDomain, path, nil, p.Okey); r.Status != 200 {
				t.Fatalf("storage for the owner's key: %d %s", r.Status, r.Body)
			}
		})
	}

	const secret = "https://example.com/secret"
	if r := p.At(t, "PUT", PcSiteDomain, "/v1/sites/trip/files?keep=1", map[string]any{"files": map[string]string{"_redirects": "/old " + secret + " 302!"}}, p.Okey); r.Status != 200 {
		t.Fatalf("redirect rules: %d %s", r.Status, r.Body)
	}
	for who, ck := range map[string]string{"signed out": "", "unlisted": dadCk} {
		r := p.At(t, "GET", p.Host, "/old", nil, rgCookie(ck))
		if strings.Contains(r.Header.Get("Location"), "example.com") || strings.Contains(string(r.Body), "example.com/secret") {
			t.Fatalf("redirect target disclosed to %s: %d %v", who, r.Status, r.Header)
		}
	}
}

// A claimed name is its own origin: the visitor session on it is host-only,
// a sibling host's page cannot read it or sign in on it, another site's API
// does not trust it, and sign-in may return to it but not to an unclaimed name.
func TestGateClaimedNameIsolatedFromSiblings(t *testing.T) {
	a := NewPrivateApp(t)
	olive, vic := a.NewPerson(t, "olive"), a.NewPerson(t, "vic")
	a.Deploy(t, olive, "clay")
	dom := Uniq("clay") + "." + PcSiteDomain
	if r := a.At(t, "POST", PcSiteDomain, "/v1/sites/clay/domain", map[string]string{"domain": dom}, map[string]string{"X-API-Key": olive.Key}); r.Status != 200 {
		t.Fatalf("claim: %d %s", r.Status, r.Body)
	}
	siteID := a.SiteID(t, olive, "clay")
	cookie := a.Session(t, vic, siteID, dom)
	me := "/v1/sites/clay/me"
	if r := a.At(t, "GET", dom, me, nil, Browser(dom, cookie)); r.JSON(t)["signed_in"] != true || r.JSON(t)["email"] != vic.Email {
		t.Fatalf("/me from the address itself: %s", r.Body)
	}
	for _, o := range []string{"https://" + PcContentHost, "https://evil." + PcSiteDomain} {
		r := a.At(t, "GET", dom, me, nil, Browser(dom, cookie, "Origin", o, "Sec-Fetch-Site", "same-site"))
		if r.JSON(t)["signed_in"] == true || strings.Contains(string(r.Body), vic.Email) {
			t.Errorf("/me from %s: %s", o, r.Body)
		}
	}
	if r := a.At(t, "POST", dom, "/v1/sites/clay/visitor/auth/verify", map[string]string{"email": vic.Email, "code": "123456"}, map[string]string{"Origin": "https://" + PcContentHost, "X-SH-CSRF": "1"}); r.Status != http.StatusForbidden {
		t.Errorf("email sign-in from a sibling: %d %s", r.Status, r.Body)
	}
	a.Deploy(t, vic, "other")
	_, vicHandle := a.UserID(t, vic)
	if r := a.At(t, "GET", PcContentHost, "/v1/u/"+vicHandle+"/sites/other/me", nil, map[string]string{"Origin": "https://" + dom}); r.Status != http.StatusForbidden {
		t.Errorf("claimed origin trusted by another site: %d", r.Status)
	}
	oh := a.WithOAuth(t)
	if _, site, _, purpose, err := oh.SanitizeReturnTo(context.Background(), "https://"+dom+"/rsvp.html"); err != nil || purpose != "site" || site.String != siteID {
		t.Errorf("return_to the claimed name: %v %q", err, purpose)
	}
	for _, bad := range []string{"https://unclaimed." + PcSiteDomain + "/", "https://www." + PcSiteDomain + "/", "https://" + PcContentHost + "/x/y/"} {
		if _, _, _, _, err := oh.SanitizeReturnTo(context.Background(), bad); err == nil {
			t.Errorf("return_to %s accepted", bad)
		}
	}
}

// The owner's offline switch closes the visitor side of a site's saved data
// and sign-in (a visitor save, a visitor read and an emailed sign-in code all
// answer site_offline) while the owner's key still reads and writes.
func TestGateOfflineRefusesVisitorActions(t *testing.T) {
	a, dir := NewSiteApp(t, "canonical")
	olive, vic := a.NewPerson(t, "olive"), a.NewPerson(t, "vic")
	a.Deploy(t, olive, "shop")
	_, oh := a.UserID(t, olive)
	MarkReady(t, dir, oh)
	shop := "shop." + oh + "." + PcSiteDomain
	okey := map[string]string{"X-API-Key": olive.Key}
	cookie := a.Session(t, vic, a.SiteID(t, olive, "shop"), shop)
	if r := a.At(t, "PUT", PcSiteDomain, "/v1/sites/shop/storage/resources/rsvps", StorageRules("kv", RulesBoard), okey); r.Status/100 != 2 {
		t.Fatalf("resource: %d %s", r.Status, r.Body)
	}
	data := "/v1/sites/shop/storage/kv/rsvps/keys/ann"
	save := func() Resp { return a.At(t, "PUT", shop, data, map[string]any{"value": "Ann"}, Browser(shop, cookie)) }
	if r := save(); r.Status != http.StatusOK {
		t.Fatalf("visitor save while online: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PATCH", PcSiteDomain, "/v1/sites/shop", map[string]bool{"offline": true}, okey); r.Status != 200 {
		t.Fatalf("offline: %d %s", r.Status, r.Body)
	}
	refused := func(what string, r Resp) {
		t.Helper()
		if r.Status != http.StatusForbidden || r.JSON(t)["code"] != "site_offline" {
			t.Fatalf("%s while offline: %d %s", what, r.Status, r.Body)
		}
	}
	refused("visitor save", save())
	refused("visitor read", a.At(t, "GET", shop, data, nil, Browser(shop, cookie)))
	refused("emailed sign-in", a.At(t, "POST", shop, "/v1/sites/shop/visitor/auth", map[string]string{"email": "ann@example.com"}, Browser(shop, "")))
	if r := a.At(t, "GET", PcSiteDomain, data, nil, okey); r.Status != 200 {
		t.Fatalf("owner read while offline: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PUT", shop, data, map[string]any{"value": "Bo"}, map[string]string{"X-API-Key": olive.Key, "Origin": "https://" + shop}); r.Status != 200 {
		t.Fatalf("owner write while offline: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PATCH", PcSiteDomain, "/v1/sites/shop", map[string]bool{"offline": false}, okey); r.Status != 200 {
		t.Fatalf("online: %d %s", r.Status, r.Body)
	}
	if r := save(); r.Status != http.StatusOK {
		t.Fatalf("visitor save after coming back: %d %s", r.Status, r.Body)
	}
}

// ---- family hosts ----

const rgFamIP = "203.0.113.7"

type rgFamilyDNS struct{ txt, away map[string]string }

// rgFamilies is a site-hosts app with address families on and a DNS stub
// that resolves every name to this server unless listed in away.
func rgFamilies(t *testing.T) (*PrivateApp, string, string, *rgFamilyDNS) {
	t.Helper()
	a, siteDir := NewSiteApp(t, "canonical")
	famDir := t.TempDir()
	for _, d := range []string{"requests", "ready", "failed"} {
		if err := os.MkdirAll(filepath.Join(famDir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	a.Sites.SetAddressFamilies(famDir)
	a.Sites.Hosts.SetPlatformZones(PcSiteDomain, "simple-hack.test")
	if _, err := a.Database.Exec(`DELETE FROM address_families WHERE suffix LIKE '%.test'`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Database.Exec(`UPDATE sites SET custom_domain = NULL, domain_status = NULL, domain_verified_at = NULL, previous_domain = NULL WHERE custom_domain LIKE '%.fam.test'`); err != nil {
		t.Fatal(err)
	}
	d := &rgFamilyDNS{txt: map[string]string{}, away: map[string]string{}}
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
		return []string{rgFamIP}, nil
	}
	return a, siteDir, famDir, d
}

// rgLiveFamily connects *.<suffix> for p and brings it to live: the TXT
// proof, the check, the operator's certificate and the issuer's ready marker.
func rgLiveFamily(t *testing.T, a *PrivateApp, famDir string, dns *rgFamilyDNS, p Person, suffix, prefix string) {
	t.Helper()
	ctx := context.Background()
	r := a.At(t, "POST", PcSiteDomain, "/v1/me/address-families", map[string]any{"suffix": "*." + suffix, "site_prefix": prefix, "canonical": true}, map[string]string{"X-API-Key": p.Key})
	if r.Status != http.StatusCreated {
		t.Fatalf("connect *.%s: %d %s", suffix, r.Status, r.Body)
	}
	id := r.JSON(t)["id"].(string)
	fam, err := sitesrepo.GetFamilyByID(ctx, a.Database, id)
	if err != nil {
		t.Fatal(err)
	}
	dns.txt["_simple-host."+suffix] = fam.Token
	families.Handler{Handler: a.Sites.Handler}.CheckFamily(ctx, fam, a.Sites.Hosts.ServerAddrs(ctx))
	if r := a.At(t, "PUT", PcSiteDomain, "/v1/admin/address-families/"+id+"/cert-mode", map[string]string{"cert_mode": "wildcard", "cert_name": suffix}, map[string]string{"X-API-Key": a.Admin}); r.Status != 200 {
		t.Fatalf("cert: %d %s", r.Status, r.Body)
	}
	var fam2 model.AddressFamily
	if fam2, err = sitesrepo.GetFamilyByID(ctx, a.Database, id); err != nil || !fam2.VerifiedAt.Valid {
		t.Fatalf("*.%s not verified: %+v %v", suffix, fam2, err)
	}
	body := "prefix=" + prefix + "\ncert=" + suffix + "\nexpires=1900000000\nreserved=www\n"
	if err := os.WriteFile(filepath.Join(famDir, "ready", suffix), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	a.Sites.Hosts.RefreshFamilies(ctx)
}

func rgRequest(host string, h map[string]string) *http.Request {
	r, _ := http.NewRequest("GET", "https://"+host+"/", nil)
	r.Host = host
	for k, v := range h {
		r.Header.Set(k, v)
	}
	return r
}

// A family host is its site's own origin: the first label (with the family's
// prefix) names the site for the storage and sign-in routes and no other,
// saves are accepted only from the page's own address, email sign-in only
// from this very host, and the visitor cookie counts only as __Host-.
func TestGateFamilyHostStorageAndOrigins(t *testing.T) {
	a, siteDir, famDir, dns := rgFamilies(t)
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "voucher-meera")
	a.Deploy(t, olive, "shop")
	_, oh := a.UserID(t, olive)
	MarkReady(t, siteDir, oh)
	rgLiveFamily(t, a, famDir, dns, olive, "voucher.fam.test", "voucher-")
	vm := a.SiteID(t, olive, "voucher-meera")
	host, sib := "meera.voucher.fam.test", "other.voucher.fam.test"

	for _, name := range []string{"meera", "voucher-meera"} {
		if id, err := a.Access.ResolveSiteIDScoped(rgRequest(host, nil), name); err != nil || id != vm {
			t.Errorf("resolve %s on %s: %q %v", name, host, id, err)
		}
	}
	if _, err := a.Access.ResolveSiteIDScoped(rgRequest(host, nil), "shop"); err == nil {
		t.Error("another site resolved on a family address")
	}
	if r := a.At(t, "PUT", PcSiteDomain, "/v1/sites/voucher-meera/storage/resources/notes", StorageRules("kv", RulesBoard), map[string]string{"X-API-Key": olive.Key}); r.Status/100 != 2 {
		t.Fatalf("storage resource: %d %s", r.Status, r.Body)
	}
	save := "/v1/sites/meera/storage/kv/notes/keys/n"
	sess := a.Session(t, olive, vm, host)
	if r := a.At(t, "PUT", host, save, map[string]any{"value": 1}, Browser(host, sess)); r.Status != 200 {
		t.Fatalf("save on the family address: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", host, save, nil, Browser(host, sess)); r.Status != 200 {
		t.Fatalf("read back on the family address: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PUT", host, save, map[string]any{"value": 2}, Browser(host, sess, "Origin", "https://"+sib, "Sec-Fetch-Site", "same-site")); r.Status != http.StatusForbidden {
		t.Fatalf("sibling origin saved: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PUT", host, save, map[string]any{"value": 2}, Browser(host, sess, "Origin", "http://"+host)); r.Status != http.StatusForbidden {
		t.Fatalf("http origin saved: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", host, "/v1/sites/meera/visitor/auth", map[string]string{"email": "v@example.com"}, map[string]string{"Origin": "https://" + sib, "Sec-Fetch-Site": "same-site", "X-SH-CSRF": "1"}); r.Status != http.StatusForbidden {
		t.Fatalf("email sign-in from a sibling: %d %s", r.Status, r.Body)
	}
	plain := rgRequest(host, map[string]string{"Cookie": gate.VisitorCookieHTTP + "=" + sess, "Sec-Fetch-Site": "same-origin", "X-Forwarded-Proto": "https"})
	if got := gate.StrictVisitorCookie(plain); got != "" {
		t.Fatalf("plain cookie read on a family host: %q", got)
	}
}

// Visitor sign-in (Google) returns to a family address while it points at
// this server, and never to a host that points elsewhere or has no site.
func TestGateFamilyHostVisitorSignIn(t *testing.T) {
	a, siteDir, famDir, dns := rgFamilies(t)
	a.WithOAuth(t)
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "meera")
	_, oh := a.UserID(t, olive)
	MarkReady(t, siteDir, oh)
	rgLiveFamily(t, a, famDir, dns, olive, "quotes.fam.test", "")
	host := "meera.quotes.fam.test"
	returnTo := "https://" + host + "/page.html"
	state, nonce := a.SignInStart(t, host, returnTo)
	loc := a.SignInCallback(t, state, "visitor", host)
	r := a.At(t, "GET", host, loc.RequestURI(), nil, map[string]string{"Cookie": gate.VisitorNonceCookieHost + "=" + nonce, "X-Forwarded-For": "198.51.100.9"})
	if r.Status != http.StatusFound || r.Header.Get("Location") != returnTo {
		t.Fatalf("establish: %d %q %s", r.Status, r.Header.Get("Location"), r.Body)
	}
	dns.away[host] = "192.0.2.50"
	if r := a.At(t, "GET", host, "/v1/visitor/oauth/google?return_to="+url.QueryEscape(returnTo), nil, nil); r.Status == http.StatusFound {
		t.Fatalf("sign-in started for a host that points elsewhere: %d", r.Status)
	}
	delete(dns.away, host)
	if r := a.At(t, "GET", "nope.quotes.fam.test", "/v1/visitor/oauth/google?return_to="+url.QueryEscape("https://nope.quotes.fam.test/"), nil, nil); r.Status == http.StatusFound {
		t.Fatalf("sign-in for a missing site: %d", r.Status)
	}
}

// The passcode gate covers a family address, both served by Go and through
// nginx's /internal/passcode rewrite, and the unlock is that host's only.
func TestGateFamilyHostPasscode(t *testing.T) {
	a, siteDir, famDir, dns := rgFamilies(t)
	if err := a.Access.SetPasscodeKey(TestPasscodeKey); err != nil {
		t.Fatal(err)
	}
	WithPasscodeLimits(t, func(l *config.Limits) { l.SitePasscodes = true }, &a.Sites.Limits, &a.Access.Limits, &a.Data.Limits)
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "meera")
	_, oh := a.UserID(t, olive)
	MarkReady(t, siteDir, oh)
	rgLiveFamily(t, a, famDir, dns, olive, "quotes.fam.test", "")
	host := "meera.quotes.fam.test"
	if r := a.At(t, "PUT", PcSiteDomain, "/v1/sites/meera/lock", map[string]string{"passcode": "Trip-2026-x"}, map[string]string{"X-API-Key": olive.Key}); r.Status != 200 {
		t.Fatalf("lock: %d %s", r.Status, r.Body)
	}
	for _, p := range []string{"/", "/sub/", "/nope"} {
		for _, prefix := range []string{"", "/internal/passcode"} {
			if r := a.At(t, "GET", host, prefix+p, nil, nil); r.Status != http.StatusUnauthorized || strings.Contains(string(r.Body), "<h1>meera</h1>") {
				t.Errorf("family %s%s: %d", prefix, p, r.Status)
			}
		}
	}
	// Through nginx's rewrite the form sends the visitor back to the page they
	// asked for, never /internal/passcode/... (a 404 from outside).
	if r := a.At(t, "GET", host, "/internal/passcode/sub/?day=2", nil, nil); r.Status != http.StatusUnauthorized || !strings.Contains(string(r.Body), `name="next" value="/sub/?day=2"`) || strings.Contains(string(r.Body), "/internal/") {
		t.Fatalf("gate next through the rewrite: %d %s", r.Status, r.Body)
	}
	form := url.Values{"passcode": {"Trip-2026-x"}, "next": {"/internal/passcode/sub/"}}.Encode()
	r := a.At(t, "POST", host, "/v1/site-unlock", form, map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Origin": "https://" + host, "Sec-Fetch-Site": "same-origin"})
	if r.Status != http.StatusSeeOther || r.Header.Get("Location") != "/sub/" {
		t.Fatalf("unlock: %d %q %s", r.Status, r.Header.Get("Location"), r.Body)
	}
	ck := map[string]string{"Cookie": CookieHeader(t, r)}
	if !strings.HasPrefix(ck["Cookie"], "__Host-sh_pass_") {
		t.Fatalf("unlock cookie is not host-only: %s", ck["Cookie"])
	}
	for _, prefix := range []string{"", "/internal/passcode"} {
		if r := a.At(t, "GET", host, prefix+"/sub/", nil, ck); r.Status != 200 || string(r.Body) != "sub" {
			t.Fatalf("unlocked %q: %d %s", prefix, r.Status, r.Body)
		}
	}
	if r := a.At(t, "GET", "meera."+oh+"."+PcSiteDomain, "/", nil, ck); r.Status == 200 {
		t.Fatal("family unlock opened the site host")
	}
}
