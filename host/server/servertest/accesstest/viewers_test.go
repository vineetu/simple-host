package accesstest

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/access/accesshttp"
	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/server/servertest"
)

func TestViewersOwnerAPI(t *testing.T) {
	p := servertest.NewPasscodeApp(t)
	marker := filepath.Join(p.PageDir, "passcode")

	r := p.At(t, "GET", servertest.PcSiteDomain, "/v1/sites/trip/access", nil, p.Okey)
	if r.Status != 200 || r.JSON(t)["access"] != "anyone" {
		t.Fatalf("read: %d %s", r.Status, r.Body)
	}
	if r := p.Grant(t, "not-an-email"); r.Status != 400 || r.JSON(t)["code"] != "invalid_email" {
		t.Fatalf("bad email: %d %s", r.Status, r.Body)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("marker after a refused grant")
	}
	r = p.Grant(t, " Mom@Example.COM ", "mom@example.com", "dad@example.com")
	if r.Status != 200 {
		t.Fatalf("grant: %d %s", r.Status, r.Body)
	}
	j := r.JSON(t)
	if j["access"] != "specific" || len(j["viewers"].([]any)) != 2 || len(j["added"].([]any)) != 2 {
		t.Fatalf("grant answer: %s", r.Body)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("no marker after the grant")
	}
	// The site routes say who can open it.
	if r := p.At(t, "GET", servertest.PcSiteDomain, "/v1/sites", nil, p.Okey); !strings.Contains(string(r.Body), `"access":"specific"`) {
		t.Fatalf("site: %s", r.Body)
	}
	// Again: already listed, nothing added.
	if r := p.Grant(t, "MOM@example.com"); r.Status != 200 || r.JSON(t)["already_listed"] == nil {
		t.Fatalf("again: %d %s", r.Status, r.Body)
	}
	// A passcode and named viewers never together, either way round.
	if r := p.Lock(t, "123456"); r.Status != http.StatusConflict || r.JSON(t)["code"] != "named_viewers_set" {
		t.Fatalf("passcode on a named-viewers site: %d %s", r.Status, r.Body)
	}
	// The cap.
	servertest.WithPasscodeLimits(t, func(l *config.Limits) { l.SiteViewersMax = 3 }, &p.Sites.Limits, &p.Access.Limits, &p.Data.Limits)
	if r := p.Grant(t, "a@example.com", "b@example.com"); r.Status != http.StatusConflict || r.JSON(t)["code"] != "too_many_viewers" {
		t.Fatalf("cap: %d %s", r.Status, r.Body)
	}
	if r := p.Grant(t, "a@example.com", "b@example.com", "c@example.com", "d@example.com"); r.Status != 400 || r.JSON(t)["code"] != "too_many_viewers" {
		t.Fatalf("cap in one call: %d %s", r.Status, r.Body)
	}
	// Remove one; an unknown one is 404.
	if r := p.At(t, "DELETE", servertest.PcSiteDomain, "/v1/sites/trip/viewers/DAD@example.com", nil, p.Okey); r.Status != 200 || r.JSON(t)["removed"] != "dad@example.com" {
		t.Fatalf("remove: %d %s", r.Status, r.Body)
	}
	if r := p.At(t, "DELETE", servertest.PcSiteDomain, "/v1/sites/trip/viewers/dad@example.com", nil, p.Okey); r.Status != 404 {
		t.Fatalf("remove again: %d %s", r.Status, r.Body)
	}
	// Another account cannot read or change it.
	mallory := p.NewPerson(t, "mallory")
	if r := p.At(t, "GET", servertest.PcSiteDomain, "/v1/sites/trip/access", nil, map[string]string{"X-API-Key": mallory.Key}); r.Status != 404 {
		t.Fatalf("mallory read: %d %s", r.Status, r.Body)
	}
	// Open to anyone again: the marker goes, the list stays.
	r = p.At(t, "PUT", servertest.PcSiteDomain, "/v1/sites/trip/access", map[string]any{"access": "anyone"}, p.Okey)
	if r.Status != 200 || r.JSON(t)["access"] != "anyone" || len(r.JSON(t)["viewers"].([]any)) != 1 {
		t.Fatalf("anyone: %d %s", r.Status, r.Body)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("marker left after access anyone")
	}
	// Now a passcode is fine, and named viewers are refused while it stands.
	if r := p.Lock(t, "123456"); r.Status != 200 {
		t.Fatalf("passcode: %d %s", r.Status, r.Body)
	}
	if r := p.At(t, "PUT", servertest.PcSiteDomain, "/v1/sites/trip/access", map[string]any{"access": "specific"}, p.Okey); r.Status != http.StatusConflict || r.JSON(t)["code"] != "passcode_set" {
		t.Fatalf("specific with a passcode: %d %s", r.Status, r.Body)
	}
	if r := p.Grant(t, "x@example.com"); r.Status != http.StatusConflict || r.JSON(t)["code"] != "passcode_set" {
		t.Fatalf("grant with a passcode: %d %s", r.Status, r.Body)
	}
	if r := p.At(t, "GET", servertest.PcSiteDomain, "/v1/sites/trip/access", nil, p.Okey); strings.Contains(string(r.Body), "x@example.com") {
		t.Fatalf("refused grant was stored: %s", r.Body)
	}
	// Removing the passcode keeps the marker off (no named viewers).
	p.At(t, "DELETE", servertest.PcSiteDomain, "/v1/sites/trip/lock", nil, p.Okey)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("marker left after the passcode went")
	}
	if r := p.At(t, "PUT", servertest.PcSiteDomain, "/v1/sites/trip/access", map[string]any{"access": "bogus"}, p.Okey); r.Status != 400 {
		t.Fatalf("bogus access: %d", r.Status)
	}
}

// viewerApp: olive's "trip" open only to mom; dad is signed in but not
// listed.
type viewerApp struct {
	*servertest.PasscodeApp
	mom, dad servertest.Person
}

func newViewerApp(t *testing.T) *viewerApp {
	t.Helper()
	p := servertest.NewPasscodeApp(t)
	v := &viewerApp{PasscodeApp: p, mom: p.NewPerson(t, "mom"), dad: p.NewPerson(t, "dad")}
	if r := p.Grant(t, v.mom.Email); r.Status != 200 {
		t.Fatalf("grant: %d %s", r.Status, r.Body)
	}
	return v
}

func TestViewersGateEveryPath(t *testing.T) {
	v := newViewerApp(t)
	p := v.PasscodeApp
	ctx := context.Background()
	momHost := p.Session(t, v.mom, p.SiteID, p.Host)
	dadHost := p.Session(t, v.dad, p.SiteID, p.Host)
	oliveHost := p.Session(t, p.Olive, p.SiteID, p.Host)

	noLeak := func(what string, r servertest.Resp) {
		t.Helper()
		if strings.Contains(string(r.Body), servertest.PasscodeCanary) {
			t.Fatalf("%s leaked: %d %s", what, r.Status, r.Body)
		}
	}
	// The other spelling of a page: the gate answers first, and only a
	// listed viewer learns where the page lives.
	for from, to := range map[string]string{"/app.js/": "/app.js", "/days": "/days/"} {
		if r := p.At(t, "GET", p.Host, from, nil, nil); r.Status != 401 || r.Header.Get("Location") != "" {
			t.Fatalf("signed out %s: %d %v", from, r.Status, r.Header)
		}
		if r := p.At(t, "GET", p.Host, from, nil, servertest.CookieOf(dadHost)); r.Status != 403 || r.Header.Get("Location") != "" {
			t.Fatalf("dad %s: %d %v", from, r.Status, r.Header)
		}
		if r := p.At(t, "GET", p.Host, from, nil, servertest.CookieOf(momHost)); r.Status != 301 || r.Header.Get("Location") != to || r.Header.Get("Cache-Control") != "private, no-cache" {
			t.Fatalf("mom %s: %d %v", from, r.Status, r.Header)
		}
	}
	paths := []string{"/", "/index.html", "/app.js", "/data.json", "/img.svg", "/days/", "/nope"}
	for _, path := range paths {
		// Signed out: the sign-in page, nothing of the site.
		r := p.At(t, "GET", p.Host, path, nil, nil)
		noLeak("signed out "+path, r)
		if r.Status != 401 || !strings.Contains(string(r.Body), ">Continue</button>") || r.Header.Get("Cache-Control") != "no-store" || r.Header.Get("X-Robots-Tag") == "" || strings.Contains(r.Header.Get("Content-Security-Policy"), "unsafe-inline") || !strings.Contains(r.Header.Get("Content-Security-Policy"), "default-src 'none'") {
			t.Fatalf("signed out %s: %d %v %s", path, r.Status, r.Header, r.Body)
		}
		// Signed in, not listed: the private page with the switch.
		r = p.At(t, "GET", p.Host, path, nil, servertest.CookieOf(dadHost))
		noLeak("dad "+path, r)
		if r.Status != 403 || !strings.Contains(string(r.Body), v.dad.Email) || !strings.Contains(string(r.Body), ">Use another email</button>") {
			t.Fatalf("dad %s: %d %s", path, r.Status, r.Body)
		}
		// Listed, and the owner: the site, never stored by a shared cache.
		for who, ck := range map[string]string{"mom": momHost, "olive": oliveHost} {
			r = p.At(t, "GET", p.Host, path, nil, servertest.CookieOf(ck))
			if path != "/nope" && (r.Status != 200 || !strings.Contains(string(r.Body), servertest.PasscodeCanary)) {
				t.Fatalf("%s %s: %d %s", who, path, r.Status, r.Body)
			}
			if cc := r.Header.Get("Cache-Control"); cc != "private, no-cache" && cc != "no-store" {
				t.Fatalf("%s %s cache: %q", who, path, cc)
			}
		}
		// The plain cookie a sibling host could plant never counts.
		noLeak("plain cookie "+path, p.At(t, "GET", p.Host, path, nil, map[string]string{"Cookie": accesshttp.VisitorCookieHTTP + "=" + momHost}))
		// mom's cookie from another site's page: a cross-site subresource.
		r = p.At(t, "GET", p.Host, path, nil, map[string]string{"Cookie": accesshttp.VisitorCookieHost + "=" + momHost, "Sec-Fetch-Site": "same-site", "Sec-Fetch-Mode": "no-cors", "Sec-Fetch-Dest": "script"})
		noLeak("cross-site "+path, r)
		// The person path and the old shared links send to the site's own
		// address; nothing is served there.
		noLeak("person path "+path, p.At(t, "GET", p.Person, "/trip"+path, nil, servertest.CookieOf(momHost)))
		noLeak("legacy "+path, p.At(t, "GET", servertest.PcContentHost, "/internal/site-redirect/"+p.Handle+"/trip"+path, nil, nil))
		noLeak("legacy marker "+path, p.At(t, "GET", servertest.PcContentHost, "/internal/passcode/"+p.Handle+"/trip"+path, nil, nil))
	}
	// A session for this site on another host does not count here.
	other := p.Session(t, v.mom, p.SiteID, "elsewhere."+servertest.PcSiteDomain)
	noLeak("other-host session", p.At(t, "GET", p.Host, "/app.js", nil, servertest.CookieOf(other)))
	// robots.txt: Disallow.
	if r := p.At(t, "GET", p.Host, "/robots.txt", nil, nil); r.Status != 200 || !strings.Contains(string(r.Body), "Disallow: /") {
		t.Fatalf("robots: %d %s", r.Status, r.Body)
	}

	// Data API: same-origin from mom or the owner, the owner's key; never dad
	// or nobody. Sign-in routes stay open so a viewer can sign in.
	for _, path := range []string{servertest.PasscodeData} {
		for _, ck := range []string{"", dadHost} {
			r := p.At(t, "GET", p.Host, path, nil, servertest.Browser(p.Host, ck))
			noLeak("data "+path, r)
			if r.Status != 403 || r.JSON(t)["code"] != "site_private" {
				t.Fatalf("data %s (%q): %d %s", path, ck, r.Status, r.Body)
			}
		}
	}
	if r := p.At(t, "GET", p.Host, servertest.PasscodeData, nil, servertest.Browser(p.Host, momHost)); r.Status != 200 || !strings.Contains(string(r.Body), servertest.PasscodeCanary) || !strings.Contains(r.Header.Get("Cache-Control"), "no-store") {
		t.Fatalf("mom data: %d %v %s", r.Status, r.Header, r.Body)
	}
	// Another site's page framing this one never gets it, cookie or not.
	for _, dest := range []string{"iframe", "frame", "embed", "object"} {
		r := p.At(t, "GET", p.Host, "/", nil, map[string]string{"Cookie": accesshttp.VisitorCookieHost + "=" + momHost, "Sec-Fetch-Site": "same-site", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": dest})
		noLeak("framed "+dest, r)
		if strings.Contains(string(r.Body), "<form") {
			t.Fatalf("framed %s offered a form: %s", dest, r.Body)
		}
	}
	if r := p.At(t, "GET", p.Host, "/", nil, map[string]string{"Cookie": accesshttp.VisitorCookieHost + "=" + momHost, "Sec-Fetch-Site": "same-origin", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "iframe"}); r.Status != 200 || !strings.Contains(strings.Join(r.Header.Values("Content-Security-Policy"), ";"), "frame-ancestors 'self'") {
		t.Fatalf("framed by the site itself: %d", r.Status)
	}
	if r := p.At(t, "GET", servertest.PcSiteDomain, servertest.PasscodeData, nil, p.Okey); r.Status != 200 {
		t.Fatalf("owner key: %d %s", r.Status, r.Body)
	}
	if r := p.At(t, "GET", p.Host, "/v1/sites/trip/me", nil, servertest.Browser(p.Host, dadHost)); r.Status != 200 || !strings.Contains(string(r.Body), v.dad.Email) {
		t.Fatalf("dad /me: %d %s", r.Status, r.Body)
	}
	if r := p.At(t, "POST", p.Host, "/v1/sites/trip/visitor/auth", map[string]any{"email": "x@example.com"}, servertest.Browser(p.Host, "")); r.Status == 403 {
		t.Fatalf("visitor sign-in refused: %d %s", r.Status, r.Body)
	}

	// Storage: a resource anyone may read stays closed to everyone but the
	// listed, whatever its site_passcode says.
	if r := p.At(t, "PUT", servertest.PcSiteDomain, "/v1/sites/trip/storage/resources/menu", `{"kind":"kv","site_passcode":"off","rules":`+servertest.RulesPublic+`}`, p.Okey); r.Status/100 != 2 {
		t.Fatalf("resource: %d %s", r.Status, r.Body)
	}
	p.At(t, "PUT", servertest.PcSiteDomain, "/v1/sites/trip/storage/kv/menu/keys/today", map[string]any{"value": servertest.PasscodeCanary}, p.Okey)
	for _, ck := range []string{"", dadHost} {
		r := p.At(t, "GET", p.Host, "/v1/sites/trip/storage/kv/menu/keys/today", nil, servertest.Browser(p.Host, ck))
		noLeak("storage", r)
		if r.Status != 403 {
			t.Fatalf("storage (%q): %d %s", ck, r.Status, r.Body)
		}
	}
	if r := p.At(t, "GET", p.Host, "/v1/sites/trip/storage/kv/menu/keys/today", nil, servertest.Browser(p.Host, momHost)); r.Status != 200 {
		t.Fatalf("mom storage: %d %s", r.Status, r.Body)
	}
	// A link opened straight from an email (a navigation, no Origin).
	nav := map[string]string{"Cookie": accesshttp.VisitorCookieHost + "=" + momHost, "Sec-Fetch-Site": "none", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document"}
	if r := p.At(t, "GET", p.Host, "/v1/sites/trip/storage/kv/menu/keys/today", nil, nav); r.Status != 200 {
		t.Fatalf("mom storage link: %d %s", r.Status, r.Body)
	}
	nav["Cookie"] = accesshttp.VisitorCookieHost + "=" + dadHost
	if r := p.At(t, "GET", p.Host, "/v1/sites/trip/storage/kv/menu/keys/today", nil, nav); r.Status != 403 {
		t.Fatalf("dad storage link: %d %s", r.Status, r.Body)
	}

	// A custom domain and a claimed name: the same gate, sessions per host.
	for _, domain := range []string{"goa-trip." + servertest.PcSiteDomain, "trip.example.org"} {
		if _, err := p.Database.ExecContext(ctx, `UPDATE sites SET custom_domain = $2, domain_status = 'active', domain_verified_at = now() WHERE id = $1`, p.SiteID, domain); err != nil {
			t.Fatal(err)
		}
		get := func(path string, h map[string]string) servertest.Resp {
			if strings.HasSuffix(domain, servertest.PcSiteDomain) {
				return p.At(t, "GET", domain, path, nil, h)
			}
			return p.At(t, "GET", domain, "/internal/passcode"+path, nil, h)
		}
		if r := get("/app.js", nil); r.Status != 401 || strings.Contains(string(r.Body), servertest.PasscodeCanary) {
			t.Fatalf("%s signed out: %d %s", domain, r.Status, r.Body)
		}
		if r := get("/app.js", servertest.CookieOf(momHost)); strings.Contains(string(r.Body), servertest.PasscodeCanary) {
			t.Fatalf("%s took the site host's session", domain)
		}
		momHere := p.Session(t, v.mom, p.SiteID, domain)
		if r := get("/app.js", servertest.CookieOf(momHere)); r.Status != 200 || !strings.Contains(string(r.Body), servertest.PasscodeCanary) {
			t.Fatalf("%s mom: %d %s", domain, r.Status, r.Body)
		}
		dadHere := p.Session(t, v.dad, p.SiteID, domain)
		if r := get("/app.js", servertest.CookieOf(dadHere)); r.Status != 403 || strings.Contains(string(r.Body), servertest.PasscodeCanary) {
			t.Fatalf("%s dad: %d %s", domain, r.Status, r.Body)
		}
		// The site host now redirects to the domain; nothing leaks there.
		noLeak("site host with a domain", p.At(t, "GET", p.Host, "/app.js", nil, nil))
	}
	if _, err := p.Database.ExecContext(ctx, `UPDATE sites SET custom_domain = NULL, domain_status = NULL, domain_verified_at = NULL WHERE id = $1`, p.SiteID); err != nil {
		t.Fatal(err)
	}

	// The person host when the site is the home page.
	uid, _ := p.UserID(t, p.Olive)
	if _, err := p.Database.ExecContext(ctx, `UPDATE users SET home_site_id = $2 WHERE id = $1`, uid, p.SiteID); err != nil {
		t.Fatal(err)
	}
	if r := p.At(t, "GET", p.Person, "/", nil, nil); r.Status != 401 || strings.Contains(string(r.Body), servertest.PasscodeCanary) {
		t.Fatalf("home page signed out: %d %s", r.Status, r.Body)
	}
	momPerson := p.Session(t, v.mom, p.SiteID, p.Person)
	if r := p.At(t, "GET", p.Person, "/", nil, servertest.CookieOf(momPerson)); r.Status != 200 || !strings.Contains(string(r.Body), servertest.PasscodeCanary) {
		t.Fatalf("home page mom: %d %s", r.Status, r.Body)
	}
	dadPerson := p.Session(t, v.dad, p.SiteID, p.Person)
	if r := p.At(t, "GET", p.Person, "/app.js", nil, servertest.CookieOf(dadPerson)); r.Status != 403 || strings.Contains(string(r.Body), servertest.PasscodeCanary) {
		t.Fatalf("home page dad: %d %s", r.Status, r.Body)
	}
	// Hidden from the person's public listing.
	if _, err := p.Database.ExecContext(ctx, `UPDATE users SET home_site_id = NULL WHERE id = $1`, uid); err != nil {
		t.Fatal(err)
	}
	if r := p.At(t, "GET", servertest.PcSiteDomain, "/v1/u/"+p.Handle+"/showcase.json", nil, nil); strings.Contains(string(r.Body), `"trip"`) {
		t.Fatalf("listed on the public page: %s", r.Body)
	}
}

func TestViewersListChangesAtOnce(t *testing.T) {
	v := newViewerApp(t)
	p := v.PasscodeApp
	mom := p.Session(t, v.mom, p.SiteID, p.Host)
	dad := p.Session(t, v.dad, p.SiteID, p.Host)
	status := func(ck string) int { return p.At(t, "GET", p.Host, "/app.js", nil, servertest.CookieOf(ck)).Status }
	if status(mom) != 200 || status(dad) != 403 {
		t.Fatal("start")
	}
	if r := p.Grant(t, v.dad.Email); r.Status != 200 {
		t.Fatal(r.Status)
	}
	if status(dad) != 200 {
		t.Fatal("dad not let in at once")
	}
	if r := p.At(t, "DELETE", servertest.PcSiteDomain, "/v1/sites/trip/viewers/"+url.PathEscape(v.mom.Email), nil, p.Okey); r.Status != 200 {
		t.Fatal(r.Status)
	}
	if status(mom) != 403 {
		t.Fatal("mom not locked out at once")
	}
	if r := p.At(t, "GET", p.Host, servertest.PasscodeData, nil, servertest.Browser(p.Host, mom)); r.Status != 403 {
		t.Fatalf("mom data after removal: %d", r.Status)
	}
	// A suspended account is out too.
	uid, _ := p.UserID(t, v.dad)
	if _, err := p.Database.Exec(`UPDATE users SET suspended_at = now() WHERE id = $1`, uid); err != nil {
		t.Fatal(err)
	}
	if status(dad) == 200 {
		t.Fatal("suspended viewer let in")
	}
	// Open to anyone: no sign-in needed.
	p.At(t, "PUT", servertest.PcSiteDomain, "/v1/sites/trip/access", map[string]any{"access": "anyone"}, p.Okey)
	if r := p.At(t, "GET", p.Host, "/app.js", nil, nil); r.Status != 200 {
		t.Fatalf("anyone: %d", r.Status)
	}
}

// The owner's "Who can open" list says whether each named viewer has
// actually signed in yet, not just when they were named.
func TestViewersSignedInStatus(t *testing.T) {
	v := newViewerApp(t)
	p := v.PasscodeApp
	viewerOf := func(j map[string]any, email string) map[string]any {
		for _, raw := range j["viewers"].([]any) {
			vw := raw.(map[string]any)
			if vw["email"] == email {
				return vw
			}
		}
		t.Fatalf("%s not in %v", email, j["viewers"])
		return nil
	}
	r := p.At(t, "GET", servertest.PcSiteDomain, "/v1/sites/trip/access", nil, p.Okey)
	if r.Status != 200 {
		t.Fatalf("read: %d %s", r.Status, r.Body)
	}
	if vw := viewerOf(r.JSON(t), v.mom.Email); vw["signed_in_at"] != nil {
		t.Fatalf("mom signed_in_at before any sign-in: %v", vw)
	}
	p.Session(t, v.mom, p.SiteID, p.Host)
	r = p.At(t, "GET", servertest.PcSiteDomain, "/v1/sites/trip/access", nil, p.Okey)
	if vw := viewerOf(r.JSON(t), v.mom.Email); vw["signed_in_at"] == nil {
		t.Fatalf("mom has no signed_in_at after signing in: %v", vw)
	}
}

func TestViewersSignInForm(t *testing.T) {
	v := newViewerApp(t)
	p := v.PasscodeApp
	mail := &servertest.CodeMailer{}
	p.Sites.Mailer = mail
	p.Access.Mailer, p.Data.Mailer = p.Sites.Mailer, p.Sites.Mailer
	form := func(host string, vals url.Values, extra map[string]string) servertest.Resp {
		h := map[string]string{"Origin": "https://" + host, "Content-Type": "application/x-www-form-urlencoded", "Sec-Fetch-Site": "same-origin"}
		for k, val := range extra {
			h[k] = val
		}
		return p.At(t, "POST", host, "/v1/site-signin", vals.Encode(), h)
	}
	// From another origin: refused, no code sent.
	if r := form(p.Host, url.Values{"email": {v.mom.Email}, "next": {"/"}}, map[string]string{"Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site"}); r.Status != 403 || mail.Code(v.mom.Email) != "" {
		t.Fatalf("cross-origin: %d %s", r.Status, r.Body)
	}
	r := form(p.Host, url.Values{"email": {strings.ToUpper(v.mom.Email)}, "next": {"/days/"}}, nil)
	if r.Status != 401 || !strings.Contains(string(r.Body), `name="code"`) || strings.Contains(string(r.Body), servertest.PasscodeCanary) {
		t.Fatalf("code page: %d %s", r.Status, r.Body)
	}
	code := mail.Code(v.mom.Email)
	if code == "" {
		t.Fatal("no code sent")
	}
	if r := form(p.Host, url.Values{"email": {v.mom.Email}, "code": {"000000"}, "next": {"/days/"}}, nil); r.Status != 401 || !strings.Contains(string(r.Body), "That code is not right") {
		t.Fatalf("wrong code: %d %s", r.Status, r.Body)
	}
	r = form(p.Host, url.Values{"email": {v.mom.Email}, "code": {code}, "next": {"/days/"}}, nil)
	if r.Status != http.StatusSeeOther || r.Header.Get("Location") != "/days/" {
		t.Fatalf("sign in: %d %v %s", r.Status, r.Header, r.Body)
	}
	var ck string
	for _, c := range (&http.Response{Header: r.Header}).Cookies() {
		if c.Name == accesshttp.VisitorCookieHost {
			ck = c.Value
		}
	}
	if ck == "" {
		t.Fatal("no session cookie")
	}
	if r := p.At(t, "GET", p.Host, "/days/", nil, servertest.CookieOf(ck)); r.Status != 200 || !strings.Contains(string(r.Body), servertest.PasscodeCanary) {
		t.Fatalf("after sign-in: %d %s", r.Status, r.Body)
	}
	// Sign out from the page: the session ends.
	so := p.At(t, "POST", p.Host, "/v1/site-signout", url.Values{"next": {"/"}}.Encode(), map[string]string{"Origin": "https://" + p.Host, "Content-Type": "application/x-www-form-urlencoded", "Sec-Fetch-Site": "same-origin", "Cookie": accesshttp.VisitorCookieHost + "=" + ck})
	if so.Status != http.StatusSeeOther {
		t.Fatalf("sign out: %d %s", so.Status, so.Body)
	}
	if r := p.At(t, "GET", p.Host, "/", nil, servertest.CookieOf(ck)); r.Status != 401 {
		t.Fatalf("after sign out: %d", r.Status)
	}
	// next never leaves the host.
	for _, next := range []string{"//evil.example/", "https://evil.example/", "/\\evil.example", "/internal/passcode//evil.example"} {
		r := form(p.Host, url.Values{"email": {v.mom.Email}, "code": {"1"}, "next": {next}}, nil)
		if loc := r.Header.Get("Location"); loc != "" && loc != "/" {
			t.Fatalf("next %q: Location %q", next, loc)
		}
		if strings.Contains(string(r.Body), "evil.example") {
			t.Fatalf("next %q echoed: %s", next, r.Body)
		}
	}
	// The Google button points at this host's own sign-in start.
	p.Access.ViewerGoogle = true
	page := p.At(t, "GET", p.Host, "/days/", nil, nil)
	if !regexp.MustCompile(`href="/v1/visitor/oauth/google\?return_to=https%3A%2F%2F` + regexp.QuoteMeta(url.QueryEscape(p.Host)) + `%2Fdays%2F"`).Match(page.Body) {
		t.Fatalf("google link: %s", page.Body)
	}
	// On a site that is open to anyone the form just goes back.
	p.At(t, "PUT", servertest.PcSiteDomain, "/v1/sites/trip/access", map[string]any{"access": "anyone"}, p.Okey)
	if r := form(p.Host, url.Values{"email": {v.dad.Email}, "next": {"/"}}, nil); r.Status != http.StatusSeeOther {
		t.Fatalf("open site: %d", r.Status)
	}
}

func TestStorageVisitorEmails(t *testing.T) {
	p := servertest.NewPasscodeApp(t)
	mom := p.NewPerson(t, "mom")
	dad := p.NewPerson(t, "dad")
	momS := p.Session(t, mom, p.SiteID, p.Host)
	dadS := p.Session(t, dad, p.SiteID, p.Host)
	momID, _ := p.UserID(t, mom)
	dadID, _ := p.UserID(t, dad)
	for name, body := range map[string]string{
		"orders": servertest.StorageRules("sqlite", servertest.RulesRecords),
		"notes":  servertest.StorageRules("kv", servertest.RulesOwnerReadSignedInAdd),
	} {
		if r := p.At(t, "PUT", servertest.PcSiteDomain, "/v1/sites/trip/storage/resources/"+name, body, p.Okey); r.Status/100 != 2 {
			t.Fatalf("%s: %d %s", name, r.Status, r.Body)
		}
	}
	if r := p.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/trip/storage/sqlite/orders/schema", map[string]any{"sql": "CREATE TABLE IF NOT EXISTS orders (id INTEGER PRIMARY KEY, item TEXT, created_at TEXT)"}, p.Okey); r.Status != 200 {
		t.Fatalf("schema: %d %s", r.Status, r.Body)
	}
	if r := p.At(t, "POST", p.Host, "/v1/sites/trip/storage/sqlite/orders/tables/orders/rows", map[string]any{"item": "cake"}, servertest.Browser(p.Host, momS)); r.Status != 200 {
		t.Fatalf("mom order: %d %s", r.Status, r.Body)
	}
	if r := p.At(t, "PUT", p.Host, "/v1/sites/trip/storage/kv/notes/keys/hello", map[string]any{"value": "hi"}, servertest.Browser(p.Host, dadS)); r.Status != 200 {
		t.Fatalf("dad note: %d %s", r.Status, r.Body)
	}
	// Before: the owner sees only an id on the row.
	r := p.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/trip/storage/sqlite/orders/query", map[string]any{"sql": "SELECT id, visitor_id FROM orders WHERE id = ?", "params": []any{1}}, p.Okey)
	if r.Status != 200 || !strings.Contains(string(r.Body), momID) || strings.Contains(string(r.Body), mom.Email) {
		t.Fatalf("owner query: %d %s", r.Status, r.Body)
	}
	// KV: the owner sees who wrote; a visitor never does.
	if r := p.At(t, "GET", servertest.PcSiteDomain, "/v1/sites/trip/storage/kv/notes/keys/hello", nil, p.Okey); !strings.Contains(string(r.Body), dadID) {
		t.Fatalf("owner kv: %s", r.Body)
	}
	if r := p.At(t, "GET", servertest.PcSiteDomain, "/v1/sites/trip/storage/kv/notes/keys", nil, p.Okey); !strings.Contains(string(r.Body), dadID) {
		t.Fatalf("owner kv list: %s", r.Body)
	}
	// The lookup: both writers resolve, a stranger does not.
	stranger := p.NewPerson(t, "stranger")
	strangerID, _ := p.UserID(t, stranger)
	r = p.At(t, "GET", servertest.PcSiteDomain, "/v1/sites/trip/storage/visitors?id="+momID+","+dadID+"&id="+strangerID, nil, p.Okey)
	if r.Status != 200 || !strings.Contains(string(r.Body), mom.Email) || !strings.Contains(string(r.Body), dad.Email) || strings.Contains(string(r.Body), stranger.Email) {
		t.Fatalf("lookup: %d %s", r.Status, r.Body)
	}
	// An id the owner wrote into their own table, or of someone who signed
	// in only on another site, never resolves.
	if r := p.At(t, "PUT", servertest.PcSiteDomain, "/v1/sites/trip/storage/resources/box", map[string]any{"kind": "sqlite"}, p.Okey); r.Status/100 != 2 {
		t.Fatalf("box: %d %s", r.Status, r.Body)
	}
	p.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/trip/storage/sqlite/box/schema", map[string]any{"sql": "CREATE TABLE t (visitor_id TEXT)"}, p.Okey)
	if r := p.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/trip/storage/sqlite/box/execute", map[string]any{"sql": "INSERT INTO t(visitor_id) VALUES(?)", "params": []any{strangerID}}, p.Okey); r.Status != 200 {
		t.Fatalf("plant: %d %s", r.Status, r.Body)
	}
	r = p.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/other/files", map[string]any{"files": map[string]string{"index.html": "o"}}, map[string]string{"X-API-Key": stranger.Key})
	if r.Status != http.StatusCreated {
		t.Fatalf("stranger site: %d", r.Status)
	}
	p.Session(t, stranger, p.SiteIDAny(t, strangerID, "other"), "other."+servertest.PcSiteDomain)
	if r := p.At(t, "GET", servertest.PcSiteDomain, "/v1/sites/trip/storage/visitors?id="+strangerID, nil, p.Okey); r.Status != 200 || strings.Contains(string(r.Body), stranger.Email) || !strings.Contains(string(r.Body), `"found":false`) {
		t.Fatalf("planted id resolved: %d %s", r.Status, r.Body)
	}
	// Owner only: never a visitor, never another account.
	if r := p.At(t, "GET", p.Host, "/v1/sites/trip/storage/visitors?id="+momID, nil, servertest.Browser(p.Host, momS)); r.Status != 403 || strings.Contains(string(r.Body), mom.Email) {
		t.Fatalf("visitor lookup: %d %s", r.Status, r.Body)
	}
	if r := p.At(t, "GET", servertest.PcSiteDomain, "/v1/sites/trip/storage/visitors?id="+momID, nil, map[string]string{"X-API-Key": stranger.Key}); strings.Contains(string(r.Body), mom.Email) {
		t.Fatalf("another account's lookup: %d %s", r.Status, r.Body)
	}
	if r := p.At(t, "GET", servertest.PcSiteDomain, "/v1/sites/trip/storage/visitors?id=not-an-id", nil, p.Okey); r.Status != 400 {
		t.Fatalf("bad id: %d", r.Status)
	}
	// A visitor reading their own rows never sees an email.
	if r := p.At(t, "GET", p.Host, "/v1/sites/trip/storage/sqlite/orders/tables/orders/rows", nil, servertest.Browser(p.Host, momS)); strings.Contains(string(r.Body), "@") {
		t.Fatalf("visitor rows: %s", r.Body)
	}
}
