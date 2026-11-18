package handler

import (
	"context"
	"database/sql"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
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
	front, _ := staticPageFrontWithLookup(t, p, p.sites.database, table, func(context.Context, model.RoutingAddress) (model.SiteTable, error) {
		t.Error("unexpected routing lookup")
		return model.SiteTable{}, nil
	})
	return front
}

func staticPageFrontWithLookup(t *testing.T, p *passcodeApp, pageDB *sql.DB, table model.SiteTable, lookup func(context.Context, model.RoutingAddress) (model.SiteTable, error)) (*httptest.Server, *static.Reloader) {
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
		h := NewSiteHandler(p.sites.ed, "", pageDB, disk, pcSiteDomain, pcContentHost, "", "", "", "", nil, 0, "", "", nil, nil)
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
	front := httptest.NewServer(static.NewHandler(copy, static.NewProxy(target, []*net.IPNet{trusted}, newPages().StaticErrorPage()), func(table model.SiteTable, api http.Handler, retry func(http.ResponseWriter, *http.Request, model.RoutingAddress) bool) http.Handler {
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
	front, copy := staticPageFrontWithLookup(t, p, p.sites.database, table, func(context.Context, model.RoutingAddress) (model.SiteTable, error) {
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
	front, _ := staticPageFrontWithLookup(t, p, p.sites.database, table, func(ctx context.Context, address model.RoutingAddress) (model.SiteTable, error) {
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

func TestStaticMissingHomeDirectoryRoutesCopiedSibling(t *testing.T) {
	p := newPasscodeApp(t)
	p.deploy(t, p.olive, "days")
	if r := p.at(t, "PUT", pcSiteDomain, "/v1/me/home", map[string]any{"site": "trip"}, p.okey); r.status != 200 {
		t.Fatalf("select home: %d", r.status)
	}
	table, _, err := p.sites.LoadSiteTable(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	p.srv = staticPageFront(t, p, table)
	if err := os.Rename(p.pageDir, p.pageDir+"-moved"); err != nil {
		t.Fatal(err)
	}
	page := p.at(t, "GET", p.person, "/days/?q=one%2Ftwo", nil, nil)
	if page.status != http.StatusFound || page.header.Get("Location") != "https://days."+p.person+"/?q=one%2Ftwo" {
		t.Fatalf("copied sibling after home disappeared: %d %q", page.status, page.header.Get("Location"))
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
				h := &SiteHandler{ed: edition.Default(config.DefaultLimits()), disk: disk, siteDomain: pcSiteDomain, contentHost: pcContentHost}
				h.SetPersonHosts("canonical")
				h.SetSiteHosts("canonical", "")
				h.SetSiteFacts(NewCopyFacts(model.SiteTable{Owners: []model.RouteOwner{{ID: "owner", Handle: "alice", HomeSiteID: "home"}}, Sites: []model.RouteSite{{ID: "home", UserID: "owner", Name: "home", ActiveVersion: 1}}}))
				r := httptest.NewRequest("GET", "https://alice."+pcSiteDomain+"/sibling", nil)
				address, err := h.staticMissingAddress(r)
				if err != nil {
					t.Fatal(err)
				}
				if address != (model.RoutingAddress{}) {
					t.Fatalf("marked home triggered a sibling lookup: %+v", address)
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
	front, _ := staticPageFrontWithLookup(t, p, p.sites.database, table, func(ctx context.Context, a model.RoutingAddress) (model.SiteTable, error) {
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

func TestStaticMarkedSiteNeedsAPI(t *testing.T) {
	for _, access := range []string{"passcode", "named-viewers"} {
		t.Run(access, func(t *testing.T) {
			p := newPasscodeApp(t)
			openPages := map[string]resp{}
			ownerCookies := map[string]map[string]string{}
			for _, visibility := range []string{"public", "unlisted"} {
				name := visibility + "-page"
				p.deploy(t, p.olive, name)
				if res := p.at(t, "PUT", pcSiteDomain, "/v1/sites/"+name+"/visibility", map[string]string{"visibility": visibility}, p.okey); res.status != 200 {
					t.Fatalf("set %s visibility: %d", visibility, res.status)
				}
				host := name + "." + p.person
				page := p.at(t, "GET", host, "/", nil, nil)
				if page.status != 200 || !strings.Contains(string(page.body), "<h1>"+name+"</h1>") {
					t.Fatalf("%s before outage: %d", host, page.status)
				}
				openPages[host] = page
				ownerCookies[host] = cookieOf(p.session(t, p.olive, p.privateApp.siteID(t, p.olive, name), host))
			}
			table, _, err := p.sites.LoadSiteTable(context.Background())
			if err != nil {
				t.Fatal(err)
			}

			// Protect the site after the routing copy was taken. Use an admitted
			// visitor so serving a file locally would otherwise look like success.
			var signed map[string]string
			if access == "passcode" {
				if r := p.lock(t, "static-code"); r.status != 200 {
					t.Fatalf("lock: %d", r.status)
				}
				signed = map[string]string{"Cookie": cookieHeader(t, p.unlock(t, p.host, "static-code", "/days/", nil))}
			} else {
				viewer := p.newPerson(t, "static-viewer")
				if r := p.grant(t, viewer.email); r.status != 200 {
					t.Fatalf("grant: %d", r.status)
				}
				signed = cookieOf(p.session(t, viewer, p.siteID, p.host))
			}

			// Close only the page handlers' pool during the outage. The fixture
			// still needs its connection to delete the test users and sites.
			p.sites.WaitNotifications()
			pageDB, err := sql.Open("postgres", os.Getenv("DB_DSN"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { pageDB.Close() })
			p.sites.database = pageDB

			const path = "/%64ays/?q=one%2Ftwo&return=x;y"
			handler := p.srv.Config.Handler
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if _, open := openPages[r.Host]; !open && (r.Host != p.host || r.URL.RequestURI() != path || r.Header.Get("Cookie") != signed["Cookie"]) {
					t.Error("API received a different host, request URI or cookie")
				}
				w.Header().Set("X-Test-API-Method", r.Method)
				handler.ServeHTTP(w, r)
			}))
			t.Cleanup(api.Close)
			p.srv = api
			p.srv = staticPageFront(t, p, table)
			for _, method := range []string{"GET", "HEAD"} {
				got := p.at(t, method, p.host, path, nil, signed)
				if got.status != 200 || got.header.Get("X-Test-API-Method") != method || (method == "GET" && !strings.Contains(string(got.body), passcodeCanary)) {
					t.Fatalf("%s: expected the admitted visitor's API response, got %d", method, got.status)
				}
			}

			// Both processes lose their fixture database; keep the API reachable
			// to distinguish its answer from Static's own service-error page.
			p.sites.WaitNotifications()
			if err := pageDB.Close(); err != nil {
				t.Fatal(err)
			}
			got := p.at(t, "GET", p.host, path, nil, signed)
			if got.status != 503 || got.header.Get("X-Test-API-Method") != "GET" || strings.Contains(string(got.body), passcodeCanary) {
				t.Fatalf("database outage: expected the API error, got %d", got.status)
			}
			checkOpenPages := func() {
				t.Helper()
				for host, before := range openPages {
					for _, cookie := range []map[string]string{nil, ownerCookies[host]} {
						page := p.at(t, "GET", host, "/", nil, cookie)
						if page.status != before.status || string(page.body) != string(before.body) ||
							page.header.Get("Cache-Control") != before.header.Get("Cache-Control") || page.header.Get("X-Robots-Tag") != before.header.Get("X-Robots-Tag") {
							t.Errorf("%s changed during outage: status=%d cache=%q robots=%q", host, page.status, page.header.Get("Cache-Control"), page.header.Get("X-Robots-Tag"))
						}
						if cookie != nil {
							checkOwnerFrameHeaders(t, page)
						}
					}
				}
			}
			checkOpenPages()
			api.Close()
			got = p.at(t, "GET", p.host, path, nil, signed)
			if got.status != 503 || got.header.Get("Cache-Control") != "no-store" || !strings.Contains(string(got.body), "<h1>Temporarily unavailable</h1>") || strings.Contains(string(got.body), passcodeCanary) {
				t.Fatalf("API outage: expected the uncached service-error page, got %d", got.status)
			}
			checkOpenPages()
		})
	}
}

func TestStaticMarkedPageStalledAPI(t *testing.T) {
	p := newPasscodeApp(t)
	if r := p.lock(t, "static-code"); r.status != 200 {
		t.Fatal(r.status)
	}
	table, _, err := p.sites.LoadSiteTable(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	accepted := make(chan struct{}, 1)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accepted <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-time.After(15 * time.Second):
			w.WriteHeader(http.StatusGatewayTimeout)
		}
	}))
	t.Cleanup(api.Close)
	p.srv = api
	p.srv = staticPageFront(t, p, table)
	page := p.at(t, "GET", p.host, "/", nil, nil)
	select {
	case <-accepted:
	default:
		t.Fatal("marked page did not reach the stalled API")
	}
	if page.status != 503 || page.header.Get("Cache-Control") != "no-store" || !strings.Contains(string(page.body), "<h1>Temporarily unavailable</h1>") || strings.Contains(string(page.body), passcodeCanary) {
		t.Fatalf("stalled API: expected uncached service-error page, got %d", page.status)
	}
}

func TestStaticOwnerSessionDatabaseOutage(t *testing.T) {
	p := newPasscodeApp(t)
	table, _, err := p.sites.LoadSiteTable(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	signed := cookieOf(p.session(t, p.olive, p.siteID, p.host))
	handler := p.srv.Config.Handler
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Test-API", "yes")
		if r.URL.Path == "/days/" {
			// Protect the directory while Static is waiting for the owner check.
			if err := os.WriteFile(filepath.Join(p.pageDir, "passcode"), nil, 0600); err != nil {
				t.Error(err)
			}
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(api.Close)
	p.srv = api
	staticDB, err := sql.Open("postgres", os.Getenv("DB_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { staticDB.Close() })
	front, _ := staticPageFrontWithLookup(t, p, staticDB, table, func(context.Context, model.RoutingAddress) (model.SiteTable, error) {
		t.Error("unexpected routing lookup")
		return model.SiteTable{}, nil
	})
	p.srv = front
	checkOwner := func(wantAPI string) {
		t.Helper()
		page := p.at(t, "GET", p.host, "/", nil, signed)
		if page.status != 200 || !strings.Contains(string(page.body), passcodeCanary) || page.header.Get("X-Test-API") != wantAPI ||
			page.header.Get("X-Frame-Options") != "SAMEORIGIN" || !strings.Contains(strings.Join(page.header.Values("Content-Security-Policy"), ";"), "frame-ancestors 'self'") {
			t.Fatalf("owner page: status=%d API=%q framing=%q %q", page.status, page.header.Get("X-Test-API"), page.header.Get("X-Frame-Options"), page.header.Values("Content-Security-Policy"))
		}
	}
	checkOwner("")
	if err := staticDB.Close(); err != nil {
		t.Fatal(err)
	}
	checkOwner("yes")
	page := p.at(t, "GET", p.host, "/days/", nil, signed)
	if page.status != 503 || !strings.Contains(string(page.body), "<h1>Temporarily unavailable</h1>") || strings.Contains(string(page.body), passcodeCanary) {
		t.Fatalf("marker added during owner check: expected the service-error page, got %d", page.status)
	}

	// The healthy API still owns the passcode gate.
	if r := p.lock(t, "static-code"); r.status != 200 {
		t.Fatal(r.status)
	}
	page = p.at(t, "GET", p.host, "/", nil, nil)
	if page.header.Get("X-Test-API") != "yes" || !strings.Contains(string(page.body), "/v1/site-unlock") || strings.Contains(string(page.body), passcodeCanary) {
		t.Fatalf("marked page with Static database down: expected API passcode form, got %d", page.status)
	}
}

func TestStaticMarkerLookupError(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := root.Mkdir("current", 0700); err != nil {
		t.Fatal(err)
	}
	if err := root.WriteFile("current/index.html", []byte(passcodeCanary), 0600); err != nil {
		t.Fatal(err)
	}
	h := &SiteHandler{ed: platformTestEdition()}
	for _, failed := range []string{"suspended", "offline", "passcode"} {
		t.Run(failed, func(t *testing.T) {
			lstat := func(name string) (fs.FileInfo, error) {
				if name == failed {
					return nil, &fs.PathError{Op: "lstat", Path: name, Err: syscall.EIO}
				}
				return root.Lstat(name)
			}
			w := httptest.NewRecorder()
			r := httptest.NewRequest("GET", "https://trip.owner.example/", nil)
			if !h.staticFileGate(w, r, lstat, h.staticAPI) {
				t.Fatal("gate allowed the file read after a marker lookup failed")
			}
			if w.Code != 503 || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), "<h1>Temporarily unavailable</h1>") {
				t.Fatalf("expected uncached service-error page, got %d", w.Code)
			}
			if content, err := root.ReadFile("current/index.html"); err != nil || string(content) != passcodeCanary {
				t.Fatalf("current content should be readable: %v", err)
			}
		})
	}
}

func checkOwnerFrameHeaders(t *testing.T, page resp) {
	t.Helper()
	if page.header.Get("X-Frame-Options") != "SAMEORIGIN" || !strings.Contains(strings.Join(page.header.Values("Content-Security-Policy"), ";"), "frame-ancestors 'self'") {
		t.Errorf("owner frame guard: X-Frame-Options=%q CSP=%q", page.header.Get("X-Frame-Options"), page.header.Values("Content-Security-Policy"))
	}
}

func TestStaticGroupOwnerVisitorCookieMarkerOrder(t *testing.T) {
	p := newPasscodeApp(t)
	table, _, err := p.sites.LoadSiteTable(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	p.sites.WaitNotifications()
	p.sites.ed = groupTestEdition()
	p.sites.SetStorageBindings(&ownerBindingFixture{})
	signed := cookieOf(p.session(t, p.olive, p.siteID, p.host))
	api := p.srv
	p.srv = staticPageFront(t, p, table)
	got := p.at(t, "GET", p.host, "/", nil, signed)
	if got.status != 200 {
		t.Fatalf("expected the API owner page, got %d", got.status)
	}
	checkOwnerFrameHeaders(t, got)

	api.Close()
	got = p.at(t, "GET", p.host, "/", nil, signed)
	if got.status != 200 || !strings.Contains(string(got.body), passcodeCanary) {
		t.Fatalf("unmarked owner page with API down: %d", got.status)
	}
	checkOwnerFrameHeaders(t, got)
	// Keep the lower-priority markers in place as each new marker appears.
	if err := os.WriteFile(filepath.Join(p.pageDir, "passcode"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		marker, heading string
		status          int
	}{
		{"offline", "<h1>This site is offline</h1>", http.StatusServiceUnavailable},
		{"suspended", "<h1>This site has been taken down</h1>", http.StatusGone},
	} {
		if err := os.WriteFile(filepath.Join(p.pageDir, tc.marker), nil, 0600); err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{"/", "/internal/" + tc.marker} {
			got := p.at(t, "GET", p.host, path, nil, signed)
			if got.status != tc.status || !strings.Contains(string(got.body), tc.heading) || got.header.Get("Cache-Control") != "no-store" {
				t.Errorf("%s %s with API down: expected local status page, got %d", tc.marker, path, got.status)
			}
		}
	}
}

func TestStaticMarkerAppearsInOpenedDirectory(t *testing.T) {
	disk, err := storage.NewDiskStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := disk.SiteDir("owner", "trip")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	// Move the open directory, reuse its name for an unmarked directory, then
	// set the marker on the original. A by-name check would miss it.
	if err := os.Rename(dir, dir+"-moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := root.WriteFile("passcode", []byte("arbitrary marker contents"), 0600); err != nil {
		t.Fatal(err)
	}
	h := &SiteHandler{ed: edition.Default(config.DefaultLimits()), disk: disk, staticAPI: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})}
	check := func(want int) {
		t.Helper()
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "https://trip.owner.example/", nil)
		if !h.staticFileGate(w, r, root.Lstat, h.staticAPI) || w.Code != want {
			t.Fatalf("opened directory gate: got %d, want %d", w.Code, want)
		}
	}
	check(http.StatusAccepted)
	if err := root.WriteFile("offline", nil, 0600); err != nil {
		t.Fatal(err)
	}
	check(http.StatusServiceUnavailable)
	if err := root.WriteFile("suspended", nil, 0600); err != nil {
		t.Fatal(err)
	}
	check(http.StatusGone)
}

// Replace the site directory at a specific by-name open in the request.
// The embedded disk still implements all of the single process's marker reads.
type staticReplacingDisk struct {
	SiteDisk
	opens, swapOn int
	swap          func(string)
}

func (d *staticReplacingDisk) SiteDir(owner, name string) string {
	dir := d.SiteDisk.SiteDir(owner, name)
	d.opens++
	if d.opens == d.swapOn {
		d.swap(dir)
	}
	return dir
}

func TestStaticReplacedDirectoryBeforeRedirect(t *testing.T) {
	for _, tc := range []struct {
		name, host, path   string
		open, publicStatus int
	}{
		{"site host legacy path", "trip.alice.", "/trip/file", 1, http.StatusMovedPermanently},
		{"selected home redirect", "alice.", "/trip/file", 2, http.StatusFound},
		{"selected home sibling lookup", "alice.", "/sibling/file", 1, http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			disk, err := storage.NewDiskStorage(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			dir := disk.SiteDir("owner", "trip")
			if err := os.MkdirAll(filepath.Join(dir, "current"), 0700); err != nil {
				t.Fatal(err)
			}
			replacement := dir + "-replacement"
			if err := os.MkdirAll(filepath.Join(replacement, "current"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(replacement, "passcode"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			d := &staticReplacingDisk{SiteDisk: disk, swap: func(path string) {
				if err := os.Rename(path, dir+"-moved"); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(replacement, path); err != nil {
					t.Fatal(err)
				}
			}}
			h := &SiteHandler{ed: edition.Default(config.DefaultLimits()), disk: d, siteDomain: pcSiteDomain, contentHost: pcContentHost}
			h.SetPersonHosts("canonical")
			h.SetSiteHosts("canonical", "")
			table := model.SiteTable{
				Owners: []model.RouteOwner{{ID: "owner", Handle: "alice", HomeSiteID: "trip"}},
				Sites:  []model.RouteSite{{ID: "trip", UserID: "owner", Name: "trip", ActiveVersion: 1}},
			}
			pages := h.StaticPages(table, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusAccepted)
			}), func(w http.ResponseWriter, r *http.Request, _ model.RoutingAddress) bool {
				http.NotFound(w, r)
				return true
			})
			request := func(want int) {
				t.Helper()
				w := httptest.NewRecorder()
				pages.ServeHTTP(w, httptest.NewRequest("GET", "https://"+tc.host+pcSiteDomain+tc.path, nil))
				if w.Code != want {
					t.Fatalf("got %d location=%q, want %d", w.Code, w.Header().Get("Location"), want)
				}
			}
			// An unmarked request reaches the ordinary redirect or lookup. Each
			// probe opens the site by name once, then opens current through it.
			request(tc.publicStatus)
			if d.opens != tc.open {
				t.Fatalf("opened site by name %d times, want %d", d.opens, tc.open)
			}
			d.opens = 0
			d.swapOn = tc.open
			request(http.StatusAccepted)
		})
	}
}
