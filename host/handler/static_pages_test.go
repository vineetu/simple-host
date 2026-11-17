package handler

import (
	"context"
	"database/sql"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/static"
	"github.com/vsriram/simple-host/host/storage"
)

// staticPageFront uses a separate handler and signing key, as the process does.
func staticPageFront(t *testing.T, p *passcodeApp, table model.SiteTable) *httptest.Server {
	t.Helper()
	front, _ := staticPageFrontWithLookup(t, p, table, func(context.Context, model.RoutingAddress) (model.SiteTable, error) {
		t.Error("unexpected routing lookup")
		return model.SiteTable{}, nil
	})
	return front
}

func staticPageFrontWithLookup(t *testing.T, p *passcodeApp, table model.SiteTable, lookup func(context.Context, model.RoutingAddress) (model.SiteTable, error)) (*httptest.Server, *static.Reloader) {
	t.Helper()
	_, trusted, _ := net.ParseCIDR("127.0.0.0/8")
	httpx.SetTrustedProxies([]*net.IPNet{trusted})
	t.Cleanup(func() { httpx.SetTrustedProxies(nil) })
	target, _ := url.Parse(p.srv.URL)
	disk, err := storage.OpenReadOnly(p.sites.disk.DataDir())
	if err != nil {
		t.Fatal(err)
	}
	newPages := func() *SiteHandler {
		h := NewSiteHandler(p.sites.ed, "", p.database, disk, pcSiteDomain, pcContentHost, "", "", "", "", nil, 0, "", "", nil, nil)
		h.personHosts, h.siteHosts, h.siteCertDir = p.sites.personHosts, p.sites.siteHosts, p.sites.siteCertDir
		h.limitSet = p.sites.limitSet
		h.SetAddressFamilies(p.sites.familyCertDir)
		return h
	}
	copy := static.New(static.Options{DataDir: disk.DataDir(), StateDir: t.TempDir(), Product: "simple-host", Lookup: lookup,
		Logf: func(string, ...any) {}, Load: func(context.Context) (model.SiteTable, time.Time, error) { return table, time.Now(), nil }})
	if err := copy.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	front := httptest.NewServer(static.NewHandler(copy, static.NewProxy(target, []*net.IPNet{trusted}), func(table model.SiteTable, api http.Handler, retry func(http.ResponseWriter, *http.Request, model.RoutingAddress) bool) http.Handler {
		return newPages().StaticPages(table, api, retry)
	}, newPages().StaticAPIRequest))
	t.Cleanup(front.Close)
	return front, copy
}

func TestStaticDomainRedirectPreservesNewMarker(t *testing.T) {
	p := newPasscodeApp(t)
	table, _, err := p.sites.LoadSiteTable(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	api := p.srv
	p.srv = staticPageFront(t, p, table)
	domain := "static-new-domain." + pcSiteDomain
	if r := p.at(t, "POST", pcSiteDomain, "/v1/sites/trip/domain", map[string]any{"domain": domain}, p.okey); r.status != 200 {
		t.Fatalf("connect domain: %d %s", r.status, r.body)
	}
	marker := filepath.Join(p.pageDir, "domain-redirect")
	before, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	path := "/internal/domain-redirect/" + p.handle + "/trip/days/?q=one%2Ftwo"
	for _, outage := range []bool{false, true} {
		if outage {
			api.Close()
		}
		r := p.at(t, "GET", pcContentHost, path, nil, nil)
		if !outage && (r.status != 302 || r.header.Get("Location") != "https://"+domain+"/days/?q=one%2Ftwo") {
			t.Fatalf("fresh domain hand-off: %d %s", r.status, r.header.Get("Location"))
		}
		if outage && r.status != 503 {
			t.Fatalf("API outage: %d", r.status)
		}
		if after, err := os.ReadFile(marker); err != nil || string(after) != string(before) {
			t.Fatalf("live redirect marker changed: %q %v", after, err)
		}
	}
}

func TestStaticFamilyReadinessWithoutTableChange(t *testing.T) {
	p := newPasscodeApp(t)
	withPasscodeLimits(t, func(l *config.Limits) { l.AddressFamilies = true }, &p.sites.limitSet)
	certDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(certDir, "ready"), 0700); err != nil {
		t.Fatal(err)
	}
	p.sites.SetAddressFamilies(certDir)
	table, _, err := p.sites.LoadSiteTable(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	uid, _ := p.userID(t, p.olive)
	table.Families = []model.AddressFamily{{ID: "family", UserID: uid, Suffix: "static-family.example.org", Canonical: true,
		VerifiedAt: sql.NullTime{Time: time.Now(), Valid: true}, CertMode: "wildcard", CertName: "fixture"}}
	front, copy := staticPageFrontWithLookup(t, p, table, func(context.Context, model.RoutingAddress) (model.SiteTable, error) {
		return model.SiteTable{}, sql.ErrNoRows
	})
	p.srv = front
	before := copy.Current()
	marker := filepath.Join(certDir, "ready", table.Families[0].Suffix)
	for _, ready := range []bool{false, true, false} {
		if ready {
			if err := os.WriteFile(marker, []byte("prefix=\n"), 0600); err != nil {
				t.Fatal(err)
			}
		} else if err := os.Remove(marker); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if err := copy.Refresh(context.Background()); err != nil {
			t.Fatal(err)
		}
		if copy.Current() != before {
			t.Fatal("identical table replaced snapshot")
		}
		family := p.at(t, "GET", "trip.static-family.example.org", "/days/", nil, nil)
		canonical := p.at(t, "GET", p.host, "/days/", nil, nil)
		if ready {
			if family.status != 200 || !strings.Contains(string(family.body), passcodeCanary) || canonical.status != 302 || canonical.header.Get("Location") != "https://trip.static-family.example.org/days/" {
				t.Fatalf("ready family: page=%d canonical=%d %s", family.status, canonical.status, canonical.header.Get("Location"))
			}
		} else if family.status != 404 || canonical.status != 200 || !strings.Contains(string(canonical.body), passcodeCanary) {
			t.Fatalf("unready family: page=%d canonical=%d", family.status, canonical.status)
		}
	}
}

func TestStaticHomeDiscoversNewSibling(t *testing.T) {
	p := newPasscodeApp(t)
	if r := p.at(t, "PUT", pcSiteDomain, "/v1/me/home", map[string]any{"site": "trip"}, p.okey); r.status != 200 {
		t.Fatalf("select home: %d %s", r.status, r.body)
	}
	table, _, err := p.sites.LoadSiteTable(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	lookups := 0
	front, _ := staticPageFrontWithLookup(t, p, table, func(ctx context.Context, address model.RoutingAddress) (model.SiteTable, error) {
		lookups++
		if address.Handle != p.handle || address.Name != "other" {
			t.Errorf("unexpected sibling lookup: %+v", address)
		}
		return p.sites.LookupRoutingSite(ctx, address)
	})
	p.srv = front
	for _, name := range []string{"other", "days"} {
		if r := p.at(t, "POST", pcSiteDomain, "/v1/sites/"+name+"/files", map[string]any{"files": map[string]string{"index.html": "new sibling"}}, p.okey); r.status != 201 {
			t.Fatalf("publish %s: %d %s", name, r.status, r.body)
		}
	}
	if r := p.at(t, "GET", p.person, "/days/", nil, nil); r.status != 200 || !strings.Contains(string(r.body), passcodeCanary) || lookups != 0 {
		t.Fatalf("home file precedence: %d lookups=%d", r.status, lookups)
	}
	for i := 0; i < 2; i++ {
		r := p.at(t, "GET", p.person, "/other/?q=one%2Ftwo", nil, nil)
		if r.status != 302 || r.header.Get("Location") != "https://other."+p.person+"/?q=one%2Ftwo" || lookups != 1 {
			t.Fatalf("new sibling: %d %s lookups=%d", r.status, r.header.Get("Location"), lookups)
		}
	}
}

func TestStaticContentHostShowcaseHandOff(t *testing.T) {
	for _, mode := range []string{"off", "serve", "canonical"} {
		t.Run(mode, func(t *testing.T) {
			p := newPasscodeApp(t)
			p.sites.SetPersonHosts(mode)
			path := "/internal/site-redirect/" + p.handle + "?q=one%2Ftwo"
			direct := p.at(t, "GET", pcContentHost, path, nil, nil)
			table, _, err := p.sites.LoadSiteTable(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			api := p.srv
			p.srv = staticPageFront(t, p, table)
			via := p.at(t, "GET", pcContentHost, path, nil, nil)
			if via.status != direct.status || string(via.body) != string(direct.body) || via.header.Get("Location") != direct.header.Get("Location") {
				t.Fatalf("showcase direct=%d via=%d", direct.status, via.status)
			}
			api.Close()
			outage := p.at(t, "GET", pcContentHost, path, nil, nil)
			if mode == "canonical" {
				if direct.status != 302 || outage.status != direct.status || outage.header.Get("Location") != direct.header.Get("Location") {
					t.Fatalf("local canonical redirect: %d %s", outage.status, outage.header.Get("Location"))
				}
			} else if direct.status != 200 || outage.status != 503 {
				t.Fatalf("showcase hand-off: direct=%d outage=%d", direct.status, outage.status)
			}
		})
	}
}

func TestStaticAPIPreviewLinks(t *testing.T) {
	for _, siteHosts := range []string{"canonical", "off"} {
		t.Run(siteHosts, func(t *testing.T) {
			p := newPasscodeApp(t)
			p.sites.SetSiteHosts(siteHosts, "")
			issued := p.at(t, "POST", pcSiteDomain, "/v1/sites/trip/versions/1/preview-link", nil, p.okey)
			if issued.status != 200 {
				t.Fatalf("issue preview: %d %s", issued.status, issued.body)
			}
			link, err := url.Parse(issued.json(t)["url"].(string))
			if err != nil {
				t.Fatal(err)
			}
			path := link.EscapedPath() + "days/?q=one%2Ftwo"
			direct := p.at(t, "GET", link.Host, path, nil, nil)
			table, _, err := p.sites.LoadSiteTable(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			p.srv = staticPageFront(t, p, table)
			via := p.at(t, "GET", link.Host, path, nil, nil)
			if direct.status != 200 || via.status != direct.status || string(via.body) != string(direct.body) || !strings.Contains(string(via.body), passcodeCanary) {
				t.Fatalf("preview direct=%d via=%d %s", direct.status, via.status, via.body)
			}
		})
	}
}

func TestStaticCustomDomainProtectedHandOff(t *testing.T) {
	for _, access := range []string{"passcode", "named-viewer"} {
		t.Run(access, func(t *testing.T) {
			p := newPasscodeApp(t)
			const domain = "static-trip.example.org"
			if _, err := p.database.Exec(`UPDATE sites SET custom_domain=$2, domain_status='active', domain_verified_at=now() WHERE id=$1`, p.siteID, domain); err != nil {
				t.Fatal(err)
			}
			var signed map[string]string
			if access == "passcode" {
				if r := p.lock(t, "static-code"); r.status != 200 {
					t.Fatalf("lock: %d %s", r.status, r.body)
				}
				signed = map[string]string{"Cookie": cookieHeader(t, p.unlock(t, domain, "static-code", "/days/", nil))}
			} else {
				viewer := p.newPerson(t, "static-viewer")
				if r := p.grant(t, viewer.email); r.status != 200 {
					t.Fatalf("grant: %d %s", r.status, r.body)
				}
				signed = cookieOf(p.session(t, viewer, p.siteID, domain))
			}
			const path = "/internal/passcode/days/?q=one%2Ftwo"
			directAnon := p.at(t, "GET", domain, path, nil, nil)
			directSigned := p.at(t, "GET", domain, path, nil, signed)
			table, _, err := p.sites.LoadSiteTable(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			p.srv = staticPageFront(t, p, table)
			for _, tc := range []struct {
				name    string
				headers map[string]string
				want    resp
			}{{"anonymous", nil, directAnon}, {"authenticated", signed, directSigned}} {
				got := p.at(t, "GET", domain, path, nil, tc.headers)
				if got.status != tc.want.status || string(got.body) != string(tc.want.body) || got.header.Get("Location") != tc.want.header.Get("Location") {
					t.Fatalf("%s direct=%d via=%d %s", tc.name, tc.want.status, got.status, got.body)
				}
			}
			if directAnon.status != 401 || directSigned.status != 200 || !strings.Contains(string(directSigned.body), passcodeCanary) {
				t.Fatalf("invalid direct oracle: anonymous=%d signed=%d", directAnon.status, directSigned.status)
			}
		})
	}
}

func TestStaticPersonRootWithoutCopiedHome(t *testing.T) {
	for _, home := range []bool{false, true} {
		t.Run(map[bool]string{false: "profile", true: "home"}[home], func(t *testing.T) {
			p := newPasscodeApp(t)
			if home {
				if r := p.at(t, "PUT", pcSiteDomain, "/v1/me/home", map[string]any{"site": "trip"}, p.okey); r.status != 200 {
					t.Fatalf("set home: %d %s", r.status, r.body)
				}
			}
			direct := p.at(t, "GET", p.person, "/?q=one%2Ftwo", nil, nil)
			p.srv = staticPageFront(t, p, model.SiteTable{})
			via := p.at(t, "GET", p.person, "/?q=one%2Ftwo", nil, nil)
			if direct.status != 200 || via.status != direct.status || string(via.body) != string(direct.body) {
				t.Fatalf("person root direct=%d via=%d %s", direct.status, via.status, via.body)
			}
		})
	}
}

func TestStaticVisitorSignInAndNamedViewerHost(t *testing.T) {
	v := newViewerApp(t)
	p := v.passcodeApp
	mail := &codeMailer{}
	p.sites.mailer = mail
	_, trusted, _ := net.ParseCIDR("127.0.0.0/8")
	httpx.SetTrustedProxies([]*net.IPNet{trusted})
	t.Cleanup(func() { httpx.SetTrustedProxies(nil) })
	target, _ := url.Parse(p.srv.URL)
	table, _, err := p.sites.LoadSiteTable(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	copy := static.New(static.Options{DataDir: p.sites.disk.DataDir(), StateDir: t.TempDir(), Product: "simple-host", Lookup: p.sites.LookupRoutingSite, Logf: func(string, ...any) {}, Load: func(context.Context) (model.SiteTable, time.Time, error) { return table, time.Now(), nil }})
	if err := copy.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	front := httptest.NewServer(static.NewHandler(copy, static.NewProxy(target, []*net.IPNet{trusted}), func(table model.SiteTable, api http.Handler, retry func(http.ResponseWriter, *http.Request, model.RoutingAddress) bool) http.Handler {
		h := NewSiteHandler(p.sites.ed, "", p.database, p.sites.disk, pcSiteDomain, pcContentHost, "", "", "", "", nil, 0, "", "", nil, nil)
		h.SetPersonHosts("serve")
		h.SetSiteHosts("serve", "")
		return h.StaticPages(table, api, retry)
	}))
	defer front.Close()
	original := p.srv
	p.srv = front
	t.Cleanup(func() { p.srv = original })
	form := func(vals url.Values) resp {
		return p.at(t, "POST", p.host, "/v1/site-signin", vals.Encode(), map[string]string{"Origin": "https://" + p.host, "Content-Type": "application/x-www-form-urlencoded", "Sec-Fetch-Site": "same-origin"})
	}
	if r := form(url.Values{"email": {v.mom.email}, "next": {"/days/"}}); r.status != 401 {
		t.Fatalf("send code: %d", r.status)
	}
	code := mail.code(v.mom.email)
	if code == "" {
		t.Fatal("no code sent to test mailer")
	}
	signed := form(url.Values{"email": {v.mom.email}, "code": {code}, "next": {"/days/"}})
	if signed.status != http.StatusSeeOther || signed.header.Get("Location") != "/days/" {
		t.Fatalf("sign-in response %d %s", signed.status, signed.body)
	}
	var cookie string
	for _, c := range (&http.Response{Header: signed.header}).Cookies() {
		if c.Name == visitorCookieHost {
			cookie = c.Value
		}
	}
	if cookie == "" {
		t.Fatal("missing site-host cookie")
	}
	page := p.at(t, "GET", p.host, "/days/", nil, cookieOf(cookie))
	if page.status != 200 || !strings.Contains(string(page.body), passcodeCanary) {
		t.Fatalf("named viewer through both hops: %d %s", page.status, page.body)
	}
	other := p.at(t, "GET", "different."+pcSiteDomain, "/days/", nil, cookieOf(cookie))
	if strings.Contains(string(other.body), passcodeCanary) {
		t.Fatal("site-host session admitted another host")
	}
	if r := p.at(t, "GET", p.host, "/", nil, nil); r.status != 401 || strings.Contains(string(r.body), passcodeCanary) {
		t.Fatalf("anonymous page admitted: %d", r.status)
	}
}

func TestStaticTLSGroupMiss(t *testing.T) {
	for _, host := range []string{"new-group." + pcSiteDomain, "site.new-group." + pcSiteDomain, "site--new-group." + pcSiteDomain} {
		t.Run(host, func(t *testing.T) {
			h := &SiteHandler{ed: groupTestEdition(), siteDomain: pcSiteDomain, contentHost: pcContentHost}
			h.SetPersonHosts("serve")
			h.SetSiteHosts("serve", "")
			h.SetTeamSiteFlatHosts(true)
			calls := 0
			copy := static.New(static.Options{DataDir: t.TempDir(), StateDir: t.TempDir(), Product: "test-product", Logf: func(string, ...any) {}, Load: func(context.Context) (model.SiteTable, time.Time, error) { return model.SiteTable{}, time.Now(), nil }, Lookup: func(ctx context.Context, a model.RoutingAddress) (model.SiteTable, error) {
				calls++
				if a.Group != "new-group" {
					t.Errorf("wrong group: %+v", a)
				}
				table := model.SiteTable{Groups: []model.RouteGroup{{ID: "group", Slug: "new-group", AccountID: "owner"}}}
				if a.GroupSite != "" {
					table.GroupSites = []model.RouteGroupSite{{GroupID: "group", Name: a.GroupSite}}
					table.Sites = []model.RouteSite{{ID: "site", UserID: "owner", Name: a.GroupSite, ActiveVersion: 1}}
				}
				return table, nil
			}})
			if err := copy.Refresh(context.Background()); err != nil {
				t.Fatal(err)
			}
			via := static.NewHandler(copy, http.NotFoundHandler(), func(table model.SiteTable, api http.Handler, retry func(http.ResponseWriter, *http.Request, model.RoutingAddress) bool) http.Handler {
				return h.StaticPages(table, api, retry)
			})
			w := httptest.NewRecorder()
			via.ServeHTTP(w, httptest.NewRequest("GET", "http://"+pcSiteDomain+"/internal/tls-ask?domain=https://"+host+"/", nil))
			if w.Code != 200 || calls != 1 {
				t.Fatalf("new TLS address: status=%d lookups=%d", w.Code, calls)
			}
		})
	}
}

// A SiteDir call here opens current to probe a home-path collision. Marker
// checks use the embedded disk directly, so they do not count as probes.
type staticHomeProbeDisk struct {
	SiteDisk
	probes int
}

func (d *staticHomeProbeDisk) SiteDir(owner, name string) string {
	d.probes++
	return d.SiteDisk.SiteDir(owner, name)
}

func TestStaticMarkedHomeSkipsCollisionProbe(t *testing.T) {
	for _, marker := range []string{"passcode", "offline", "suspended"} {
		for _, exists := range []bool{false, true} {
			t.Run(marker+"/exists="+map[bool]string{false: "false", true: "true"}[exists], func(t *testing.T) {
				disk, err := storage.NewDiskStorage(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				dir := disk.SiteDir("owner", "home")
				if err := os.MkdirAll(filepath.Join(dir, "current"), 0700); err != nil {
					t.Fatal(err)
				}
				if exists {
					if err := os.WriteFile(filepath.Join(dir, "current", "sibling"), []byte("private file"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(filepath.Join(dir, marker), nil, 0600); err != nil {
					t.Fatal(err)
				}
				spy := &staticHomeProbeDisk{SiteDisk: disk}
				h := &SiteHandler{ed: edition.Default(config.DefaultLimits()), disk: spy, siteDomain: pcSiteDomain, contentHost: pcContentHost}
				h.SetPersonHosts("canonical")
				h.SetSiteHosts("canonical", "")
				h.SetSiteFacts(NewCopyFacts(model.SiteTable{Owners: []model.RouteOwner{{ID: "owner", Handle: "alice", HomeSiteID: "home"}}, Sites: []model.RouteSite{{ID: "home", UserID: "owner", Name: "home", ActiveVersion: 1}}}))
				r := httptest.NewRequest("GET", "https://alice."+pcSiteDomain+"/sibling", nil)
				address, missing := h.staticMissingAddress(r)
				if spy.probes != 0 || missing {
					t.Fatalf("marked home: probes=%d missing=%v address=%+v", spy.probes, missing, address)
				}
			})
		}
	}
}

func TestStaticAliasDiscoversNewSite(t *testing.T) {
	p := newPasscodeApp(t)
	uid, _ := p.userID(t, p.olive)
	alias := p.handle + "-old"
	if _, err := p.database.Exec(`INSERT INTO handle_aliases(handle,user_id) VALUES ($1,$2)`, alias, uid); err != nil {
		t.Fatal(err)
	}
	table, _, err := p.sites.LoadSiteTable(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	lookups := 0
	front, _ := staticPageFrontWithLookup(t, p, table, func(ctx context.Context, a model.RoutingAddress) (model.SiteTable, error) {
		lookups++
		if a.Handle != p.handle || a.Name != "newsite" {
			t.Errorf("wrong alias lookup: %+v", a)
		}
		return p.sites.LookupRoutingSite(ctx, a)
	})
	// Publish after the copy is loaded, under the owner's current handle.
	if r := p.at(t, "POST", pcSiteDomain, "/v1/sites/newsite/files", map[string]any{"files": map[string]string{"index.html": "new site"}}, p.okey); r.status != 201 {
		t.Fatalf("publish: %d %s", r.status, r.body)
	}
	host := "newsite." + alias + "." + pcSiteDomain
	path := "/?q=one%2Ftwo"
	direct := p.at(t, "GET", host, path, nil, nil)
	p.srv = front
	for i := 0; i < 2; i++ {
		via := p.at(t, "GET", host, path, nil, nil)
		if direct.status != 302 || via.status != direct.status || via.header.Get("Location") != direct.header.Get("Location") || lookups != 1 {
			t.Fatalf("alias direct=%d %s via=%d %s lookups=%d", direct.status, direct.header.Get("Location"), via.status, via.header.Get("Location"), lookups)
		}
	}
}
