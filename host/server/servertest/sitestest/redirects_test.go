package sitestest

import (
	"archive/zip"
	"bytes"
	"github.com/vsriram/simple-host/host/server/servertest"
	"strings"
	"testing"
)

func TestZIPPageWithNonUTF8Name(t *testing.T) {
	a, dir := servertest.NewSiteApp(t, "serve")
	owner := a.NewPerson(t, "zip-pages")
	_, handle := a.UserID(t, owner)
	servertest.MarkReady(t, dir, handle)
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	// This pair validates only when the published file shadows the first rule.
	for name, body := range map[string]string{"index.html": "home", "caf\xe9.html": "original bytes", "caf%E9.html": "literal percent", "_redirects": "/caf%E9 /old\n/old /caf%E9"} {
		f, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if r := a.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/blog", buf.String(), map[string]string{"X-API-Key": owner.Key, "Content-Type": "application/zip"}); r.Status != 201 {
		t.Fatal(r.Status, string(r.Body))
	}
	host := "blog." + handle + "." + servertest.PcSiteDomain
	for p, want := range map[string]string{"/caf%E9.html": "original bytes", "/caf%E9": "original bytes", "/caf%25E9": "literal percent"} {
		if r := a.At(t, "GET", host, p, nil, nil); r.Status != 200 || string(r.Body) != want {
			t.Errorf("%s: %d %q", p, r.Status, r.Body)
		}
	}
}

func TestRedirectsAfterGates(t *testing.T) {
	p := servertest.NewPasscodeApp(t)
	r := p.At(t, "PUT", servertest.PcSiteDomain, "/v1/sites/trip/files?keep=1", map[string]any{"files": map[string]string{"_redirects": "/old https://example.com/secret 302!"}}, p.Okey)
	if r.Status != 200 {
		t.Fatal(r.Status, string(r.Body))
	}
	if r = p.At(t, "GET", p.Host, "/old", nil, nil); r.Status != 302 || r.Header.Get("Location") != "https://example.com/secret" {
		t.Fatal(r.Status, r.Header)
	}
	if r = p.Lock(t, "123456"); r.Status != 200 {
		t.Fatal(r.Status, string(r.Body))
	}
	if r = p.At(t, "GET", p.Host, "/old", nil, nil); r.Header.Get("Location") != "" || strings.Contains(string(r.Body), "example.com/secret") {
		t.Fatal("target leaked", r.Status, r.Header)
	}
	if r = p.At(t, "PATCH", servertest.PcSiteDomain, "/v1/sites/trip", map[string]bool{"offline": true}, p.Okey); r.Status != 200 {
		t.Fatal(r.Status, string(r.Body))
	}
	if r = p.At(t, "GET", p.Host, "/old", nil, nil); r.Status != 503 || r.Header.Get("Location") != "" {
		t.Fatal(r.Status, r.Header)
	}
	if r = p.At(t, "PATCH", servertest.PcSiteDomain, "/v1/sites/trip", map[string]bool{"offline": false}, p.Okey); r.Status != 200 {
		t.Fatal(r.Status, string(r.Body))
	}
	uid, _ := p.UserID(t, p.Olive)
	if e := p.Sites.Disk.SetSuspended(uid, "trip", true); e != nil {
		t.Fatal(e)
	}
	if r = p.At(t, "GET", p.Host, "/old", nil, nil); r.Header.Get("Location") != "" || strings.Contains(string(r.Body), "example.com/secret") {
		t.Fatal("take-down leaked target", r.Status, r.Header)
	}
	if e := p.Sites.Disk.SetSuspended(uid, "trip", false); e != nil {
		t.Fatal(e)
	}
	if r = p.At(t, "DELETE", servertest.PcSiteDomain, "/v1/sites/trip/lock", nil, p.Okey); r.Status != 200 {
		t.Fatal(r.Status, string(r.Body))
	}
	if r = p.Grant(t, "viewer@example.com"); r.Status != 200 {
		t.Fatal(r.Status, string(r.Body))
	}
	if r = p.At(t, "GET", p.Host, "/old", nil, nil); strings.Contains(r.Header.Get("Location"), "example.com/secret") || strings.Contains(string(r.Body), "example.com/secret") {
		t.Fatal("named-viewer gate leaked target", r.Status, r.Header)
	}

}

const publicPageCache = "public, max-age=0, must-revalidate"

func checkPageRedirect(t *testing.T, a *servertest.PrivateApp, host, request, location, cache string) {
	t.Helper()
	const query = "?one=a%2Fb&one=two"
	for _, method := range []string{"GET", "HEAD"} {
		r := a.At(t, method, host, request+query, nil, nil)
		if r.Status != 301 || r.Header.Get("Location") != location+query || r.Header.Get("Cache-Control") != cache || method == "HEAD" && len(r.Body) != 0 {
			t.Fatalf("%s %s%s: %d %v, body %q", method, host, request, r.Status, r.Header, r.Body)
		}
	}
}

func TestRedirectsOnAddresses(t *testing.T) {
	a, dir := servertest.NewSiteApp(t, "serve")
	a.Sites.Hosts.SetPersonHosts("serve")
	owner := a.NewPerson(t, "redirect-owner")
	_, handle := a.UserID(t, owner)
	servertest.MarkReady(t, dir, handle)
	key := map[string]string{"X-API-Key": owner.Key}
	files := map[string]string{
		"index.html":  "home",
		"faq.html":    "faq",
		"forced.html": "forced",
		"x.html":      "catch-all shadow",
		"a/b/c.html":  "nested page",
		"assets/logo": "logo",
		"_redirects": `/old /new 302!
/faq/ /faq
/forced/ /new 302!
/_redirects /404.html 404
/* /index.html 302`,
	}
	if r := a.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/blog/files", map[string]any{"files": files}, key); r.Status != 201 {
		t.Fatal(r.Status, string(r.Body))
	}
	for _, tt := range []struct{ host, path, want string }{{"blog." + handle + "." + servertest.PcSiteDomain, "/old", "/new"}, {handle + "." + servertest.PcSiteDomain, "/blog/old", "/blog/new"}, {servertest.PcContentHost, "/internal/site-redirect/" + handle + "/blog/old", "/" + handle + "/blog/new"}} {
		r := a.At(t, "GET", tt.host, tt.path, nil, nil)
		if r.Status != 302 || r.Header.Get("Location") != tt.want {
			t.Fatalf("%+v: %d %v", tt, r.Status, r.Header)
		}
	}
	for _, method := range []string{"GET", "HEAD"} {
		r := a.At(t, method, servertest.PcContentHost, "/internal/notfound", nil, map[string]string{"X-Original-URI": "/" + handle + "/blog/faq/?q=a%2Fb"})
		if r.Status != 404 || r.Header.Get("Location") != "" || r.Header.Get("Cache-Control") != "no-store" || method == "HEAD" && len(r.Body) != 0 {
			t.Fatal(method, r.Status, r.Header)
		}
	}
	siteHost := "blog." + handle + "." + servertest.PcSiteDomain
	for _, route := range []string{"/internal/site-redirect", "/internal/passcode"} {
		mount := "/" + handle + "/blog"
		checkPageRedirect(t, a, servertest.PcContentHost, route+mount+"/%66aq/", mount+"/%66aq", publicPageCache)
	}
	checkPageRedirect(t, a, siteHost, "/faq/", "/faq", publicPageCache)
	checkPageRedirect(t, a, siteHost, "/x/", "/x", publicPageCache)
	checkPageRedirect(t, a, handle+"."+servertest.PcSiteDomain, "/blog/%66aq/", "/blog/%66aq", publicPageCache)
	// The redirect only removes the slash; the next request serves the page.
	checkPageRedirect(t, a, handle+"."+servertest.PcSiteDomain, "/blog/a%2Fb/%2e/c/", "/blog/a%2Fb/%2e/c", publicPageCache)
	if r := a.At(t, "GET", handle+"."+servertest.PcSiteDomain, "/blog/a%2Fb/%2e/c", nil, nil); r.Status != 200 || string(r.Body) != "nested page" {
		t.Fatal(r.Status, string(r.Body))
	}
	// A folder without a page still hides a rule that is not forced.
	if r := a.At(t, "GET", siteHost, "/assets", nil, nil); r.Status != 404 || r.Header.Get("Location") != "" {
		t.Fatal(r.Status, r.Header)
	}
	if r := a.At(t, "GET", siteHost, "/forced/", nil, nil); r.Status != 302 || r.Header.Get("Location") != "/new" {
		t.Fatal(r.Status, r.Header)
	}
	if r := a.At(t, "GET", siteHost, "/faq", nil, nil); r.Status != 200 || string(r.Body) != "faq" {
		t.Fatal(r.Status, string(r.Body))
	}
	if r := a.At(t, "PUT", servertest.PcSiteDomain, "/v1/sites/blog/files", map[string]any{"files": map[string]string{
		"index.html": "home", "x/index.html": "folder", "_redirects": "/x/ /x 301!",
	}}, key); r.Status != 400 || r.JSON(t)["code"] != "redirects_invalid" || !strings.Contains(string(r.Body), "loop") {
		t.Fatal(r.Status, string(r.Body))
	}
	claimed := servertest.Uniq("redirect") + "." + servertest.PcSiteDomain
	if r := a.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/blog/domain", map[string]string{"domain": claimed}, key); r.Status != 200 {
		t.Fatal(r.Status, string(r.Body))
	}
	if r := a.At(t, "GET", claimed, "/old", nil, nil); r.Status != 302 || r.Header.Get("Location") != "/new" {
		t.Fatal(r.Status, r.Header)
	}
	checkPageRedirect(t, a, claimed, "/faq/", "/faq", publicPageCache)
	custom := servertest.Uniq("redirect") + ".example.test"
	if r := a.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/blog/domain", map[string]string{"domain": custom}, key); r.Status != 200 {
		t.Fatal(r.Status, string(r.Body))
	}
	a.Verify(t, a.SiteID(t, owner, "blog"), custom)
	checkPageRedirect(t, a, custom, "/internal/passcode/%66aq/", "/%66aq", publicPageCache)
	if r := a.At(t, "GET", custom, "/internal/passcode/old", nil, nil); r.Status != 302 || r.Header.Get("Location") != "/new" {
		t.Fatal(r.Status, r.Header)
	}
	if r := a.At(t, "GET", custom, "/internal/passcode/_redirects", nil, nil); r.Status != 404 {
		t.Fatal(r.Status, string(r.Body))
	}
}
func TestRedirectsOnFamilyAndGroupSite(t *testing.T) {
	a, _, famDir, dns := newFamilyApp(t)
	owner := a.NewPerson(t, "family-redirect")
	key := map[string]string{"X-API-Key": owner.Key}
	files := map[string]string{"index.html": "home", "_redirects": "/old /new 301!", "faq.html": "file"}
	if r := a.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/voucher-shop/files", map[string]any{"files": files}, key); r.Status != 201 {
		t.Fatal(r.Status, string(r.Body))
	}
	a.LiveFamily(t, famDir, dns, owner, "redirects.fam.test", "voucher-", false, 0)
	if r := a.At(t, "GET", "shop.redirects.fam.test", "/old", nil, nil); r.Status != 301 || r.Header.Get("Location") != "/new" {
		t.Fatal(r.Status, r.Header)
	}
	for _, route := range []string{"", "/internal/family", "/internal/passcode"} {
		checkPageRedirect(t, a, "shop.redirects.fam.test", route+"/%66aq/", "/%66aq", publicPageCache)
	}
	b, _ := servertest.NewSiteApp(t, "canonical")
	event := b.NewPerson(t, "redirect-event")
	eventHandle := servertest.UniqueLabel(t, "weekend")
	setHandle(t, b, event, eventHandle)
	if r := b.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/alpha/files", map[string]any{"files": files}, map[string]string{"X-API-Key": event.Key}); r.Status != 201 {
		t.Fatal(r.Status, string(r.Body))
	}
	asGroupFlat(b)
	if r := b.At(t, "GET", "alpha--"+eventHandle+"."+servertest.PcSiteDomain, "/old", nil, nil); r.Status != 301 || r.Header.Get("Location") != "/new" {
		t.Fatal(r.Status, r.Header)
	}
}
