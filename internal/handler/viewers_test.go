package handler

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/vsriram/simple-host/internal/config"
)

// Named viewers (viewers.go), end to end over the real router. Needs DB_DSN.

type codeMailer struct {
	mu    sync.Mutex
	codes map[string]string
}

func (m *codeMailer) SendSignInCode(to, code, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.codes == nil {
		m.codes = map[string]string{}
	}
	m.codes[to] = code
	return nil
}

func (m *codeMailer) code(to string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.codes[to]
}

func TestNormalizeViewerEmail(t *testing.T) {
	for in, want := range map[string]string{
		" Mom@Example.COM ":     "mom@example.com",
		"dad+trip@example.org":  "dad+trip@example.org",
		"first.last@sub.ex.com": "first.last@sub.ex.com",
	} {
		if got, ok := normalizeViewerEmail(in); !ok || got != want {
			t.Errorf("%q: %q %v, want %q", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "mom", "mom@", "@example.com", "mom@example", "mоm@example.com", "mom@exa mple.com", "mom@exa\nmple.com", "a..b@example.com", "mom@example.com,dad@example.com", strings.Repeat("a", 250) + "@example.com"} {
		if got, ok := normalizeViewerEmail(bad); ok {
			t.Errorf("%q accepted as %q", bad, got)
		}
	}
}

func (p *passcodeApp) grant(t *testing.T, emails ...string) resp {
	t.Helper()
	return p.at(t, "POST", pcSiteDomain, "/v1/sites/trip/viewers", map[string]any{"emails": emails}, p.okey)
}

func TestViewersOwnerAPI(t *testing.T) {
	p := newPasscodeApp(t)
	marker := filepath.Join(p.pageDir, "passcode")

	r := p.at(t, "GET", pcSiteDomain, "/v1/sites/trip/access", nil, p.okey)
	if r.status != 200 || r.json(t)["access"] != "anyone" {
		t.Fatalf("read: %d %s", r.status, r.body)
	}
	if r := p.grant(t, "not-an-email"); r.status != 400 || r.json(t)["code"] != "invalid_email" {
		t.Fatalf("bad email: %d %s", r.status, r.body)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("marker after a refused grant")
	}
	r = p.grant(t, " Mom@Example.COM ", "mom@example.com", "dad@example.com")
	if r.status != 200 {
		t.Fatalf("grant: %d %s", r.status, r.body)
	}
	j := r.json(t)
	if j["access"] != "specific" || len(j["viewers"].([]any)) != 2 || len(j["added"].([]any)) != 2 {
		t.Fatalf("grant answer: %s", r.body)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("no marker after the grant")
	}
	// The site routes say who can open it.
	if r := p.at(t, "GET", pcSiteDomain, "/v1/sites", nil, p.okey); !strings.Contains(string(r.body), `"access":"specific"`) {
		t.Fatalf("site: %s", r.body)
	}
	// Again: already listed, nothing added.
	if r := p.grant(t, "MOM@example.com"); r.status != 200 || r.json(t)["already_listed"] == nil {
		t.Fatalf("again: %d %s", r.status, r.body)
	}
	// A passcode and named viewers never together, either way round.
	if r := p.lock(t, "123456"); r.status != http.StatusConflict || r.json(t)["code"] != "named_viewers_set" {
		t.Fatalf("passcode on a named-viewers site: %d %s", r.status, r.body)
	}
	// The cap.
	withPasscodeLimits(t, func(l *config.Limits) { l.SiteViewersMax = 3 })
	if r := p.grant(t, "a@example.com", "b@example.com"); r.status != http.StatusConflict || r.json(t)["code"] != "too_many_viewers" {
		t.Fatalf("cap: %d %s", r.status, r.body)
	}
	if r := p.grant(t, "a@example.com", "b@example.com", "c@example.com", "d@example.com"); r.status != 400 || r.json(t)["code"] != "too_many_viewers" {
		t.Fatalf("cap in one call: %d %s", r.status, r.body)
	}
	// Remove one; an unknown one is 404.
	if r := p.at(t, "DELETE", pcSiteDomain, "/v1/sites/trip/viewers/DAD@example.com", nil, p.okey); r.status != 200 || r.json(t)["removed"] != "dad@example.com" {
		t.Fatalf("remove: %d %s", r.status, r.body)
	}
	if r := p.at(t, "DELETE", pcSiteDomain, "/v1/sites/trip/viewers/dad@example.com", nil, p.okey); r.status != 404 {
		t.Fatalf("remove again: %d %s", r.status, r.body)
	}
	// Another account cannot read or change it.
	mallory := p.newPerson(t, "mallory")
	if r := p.at(t, "GET", pcSiteDomain, "/v1/sites/trip/access", nil, map[string]string{"X-API-Key": mallory.key}); r.status != 404 {
		t.Fatalf("mallory read: %d %s", r.status, r.body)
	}
	// Open to anyone again: the marker goes, the list stays.
	r = p.at(t, "PUT", pcSiteDomain, "/v1/sites/trip/access", map[string]any{"access": "anyone"}, p.okey)
	if r.status != 200 || r.json(t)["access"] != "anyone" || len(r.json(t)["viewers"].([]any)) != 1 {
		t.Fatalf("anyone: %d %s", r.status, r.body)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("marker left after access anyone")
	}
	// Now a passcode is fine, and named viewers are refused while it stands.
	if r := p.lock(t, "123456"); r.status != 200 {
		t.Fatalf("passcode: %d %s", r.status, r.body)
	}
	if r := p.at(t, "PUT", pcSiteDomain, "/v1/sites/trip/access", map[string]any{"access": "specific"}, p.okey); r.status != http.StatusConflict || r.json(t)["code"] != "passcode_set" {
		t.Fatalf("specific with a passcode: %d %s", r.status, r.body)
	}
	if r := p.grant(t, "x@example.com"); r.status != http.StatusConflict || r.json(t)["code"] != "passcode_set" {
		t.Fatalf("grant with a passcode: %d %s", r.status, r.body)
	}
	if r := p.at(t, "GET", pcSiteDomain, "/v1/sites/trip/access", nil, p.okey); strings.Contains(string(r.body), "x@example.com") {
		t.Fatalf("refused grant was stored: %s", r.body)
	}
	// Removing the passcode keeps the marker off (no named viewers).
	p.at(t, "DELETE", pcSiteDomain, "/v1/sites/trip/lock", nil, p.okey)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("marker left after the passcode went")
	}
	if r := p.at(t, "PUT", pcSiteDomain, "/v1/sites/trip/access", map[string]any{"access": "bogus"}, p.okey); r.status != 400 {
		t.Fatalf("bogus access: %d", r.status)
	}
}

// viewerApp: olive's "trip" open only to mom; dad is signed in but not
// listed.
type viewerApp struct {
	*passcodeApp
	mom, dad person
}

func newViewerApp(t *testing.T) *viewerApp {
	t.Helper()
	p := newPasscodeApp(t)
	v := &viewerApp{passcodeApp: p, mom: p.newPerson(t, "mom"), dad: p.newPerson(t, "dad")}
	if r := p.grant(t, v.mom.email); r.status != 200 {
		t.Fatalf("grant: %d %s", r.status, r.body)
	}
	return v
}

func cookieOf(raw string) map[string]string {
	return map[string]string{"Cookie": visitorCookieHost + "=" + raw}
}

func TestViewersGateEveryPath(t *testing.T) {
	v := newViewerApp(t)
	p := v.passcodeApp
	ctx := context.Background()
	momHost := p.session(t, v.mom, p.siteID, p.host)
	dadHost := p.session(t, v.dad, p.siteID, p.host)
	oliveHost := p.session(t, p.olive, p.siteID, p.host)

	noLeak := func(what string, r resp) {
		t.Helper()
		if strings.Contains(string(r.body), passcodeCanary) {
			t.Fatalf("%s leaked: %d %s", what, r.status, r.body)
		}
	}
	paths := []string{"/", "/index.html", "/app.js", "/data.json", "/img.svg", "/days/", "/nope"}
	for _, path := range paths {
		// Signed out: the sign-in page, nothing of the site.
		r := p.at(t, "GET", p.host, path, nil, nil)
		noLeak("signed out "+path, r)
		if r.status != 401 || !strings.Contains(string(r.body), "Email me a code") || r.header.Get("Cache-Control") != "no-store" || r.header.Get("X-Robots-Tag") == "" {
			t.Fatalf("signed out %s: %d %v %s", path, r.status, r.header, r.body)
		}
		// Signed in, not listed: the private page with the switch.
		r = p.at(t, "GET", p.host, path, nil, cookieOf(dadHost))
		noLeak("dad "+path, r)
		if r.status != 403 || !strings.Contains(string(r.body), v.dad.email) || !strings.Contains(string(r.body), "Switch account") {
			t.Fatalf("dad %s: %d %s", path, r.status, r.body)
		}
		// Listed, and the owner: the site, never stored by a shared cache.
		for who, ck := range map[string]string{"mom": momHost, "olive": oliveHost} {
			r = p.at(t, "GET", p.host, path, nil, cookieOf(ck))
			if path != "/nope" && (r.status != 200 || !strings.Contains(string(r.body), passcodeCanary)) {
				t.Fatalf("%s %s: %d %s", who, path, r.status, r.body)
			}
			if cc := r.header.Get("Cache-Control"); cc != "private, no-cache" && cc != "no-store" {
				t.Fatalf("%s %s cache: %q", who, path, cc)
			}
		}
		// The plain cookie a sibling host could plant never counts.
		noLeak("plain cookie "+path, p.at(t, "GET", p.host, path, nil, map[string]string{"Cookie": visitorCookieHTTP + "=" + momHost}))
		// mom's cookie from another site's page: a cross-site subresource.
		r = p.at(t, "GET", p.host, path, nil, map[string]string{"Cookie": visitorCookieHost + "=" + momHost, "Sec-Fetch-Site": "same-site", "Sec-Fetch-Mode": "no-cors", "Sec-Fetch-Dest": "script"})
		noLeak("cross-site "+path, r)
		// The person path and the old shared links send to the site's own
		// address; nothing is served there.
		noLeak("person path "+path, p.at(t, "GET", p.person, "/trip"+path, nil, cookieOf(momHost)))
		noLeak("legacy "+path, p.at(t, "GET", pcContentHost, "/internal/site-redirect/"+p.handle+"/trip"+path, nil, nil))
		noLeak("legacy marker "+path, p.at(t, "GET", pcContentHost, "/internal/passcode/"+p.handle+"/trip"+path, nil, nil))
	}
	// A session for this site on another host does not count here.
	other := p.session(t, v.mom, p.siteID, "elsewhere."+pcSiteDomain)
	noLeak("other-host session", p.at(t, "GET", p.host, "/app.js", nil, cookieOf(other)))
	// robots.txt: Disallow.
	if r := p.at(t, "GET", p.host, "/robots.txt", nil, nil); r.status != 200 || !strings.Contains(string(r.body), "Disallow: /") {
		t.Fatalf("robots: %d %s", r.status, r.body)
	}

	// Data API: same-origin from mom or the owner, the owner's key; never dad
	// or nobody. Sign-in routes stay open so a viewer can sign in.
	p.at(t, "PUT", pcSiteDomain, "/v1/sites/trip/state", map[string]any{"c": passcodeCanary}, p.okey)
	for _, path := range []string{"/v1/sites/trip/state", "/v1/u/" + p.handle + "/sites/trip/state", "/v1/u/" + p.handle + "/sites/trip/collections/x"} {
		for _, ck := range []string{"", dadHost} {
			r := p.at(t, "GET", p.host, path, nil, browser(p.host, ck))
			noLeak("data "+path, r)
			if r.status != 403 || r.json(t)["code"] != "site_private" {
				t.Fatalf("data %s (%q): %d %s", path, ck, r.status, r.body)
			}
		}
	}
	if r := p.at(t, "GET", p.host, "/v1/sites/trip/state", nil, browser(p.host, momHost)); r.status != 200 || !strings.Contains(string(r.body), passcodeCanary) || r.header.Get("Cache-Control") != "private, no-store" {
		t.Fatalf("mom state: %d %v %s", r.status, r.header, r.body)
	}
	// Another site's page framing this one never gets it, cookie or not.
	for _, dest := range []string{"iframe", "frame", "embed", "object"} {
		r := p.at(t, "GET", p.host, "/", nil, map[string]string{"Cookie": visitorCookieHost + "=" + momHost, "Sec-Fetch-Site": "same-site", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": dest})
		noLeak("framed "+dest, r)
		if strings.Contains(string(r.body), "<form") {
			t.Fatalf("framed %s offered a form: %s", dest, r.body)
		}
	}
	if r := p.at(t, "GET", p.host, "/", nil, map[string]string{"Cookie": visitorCookieHost + "=" + momHost, "Sec-Fetch-Site": "same-origin", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "iframe"}); r.status != 200 {
		t.Fatalf("framed by the site itself: %d", r.status)
	}
	if r := p.at(t, "GET", pcSiteDomain, "/v1/sites/trip/state", nil, p.okey); r.status != 200 {
		t.Fatalf("owner key: %d %s", r.status, r.body)
	}
	if r := p.at(t, "GET", p.host, "/v1/sites/trip/me", nil, browser(p.host, dadHost)); r.status != 200 || !strings.Contains(string(r.body), v.dad.email) {
		t.Fatalf("dad /me: %d %s", r.status, r.body)
	}
	if r := p.at(t, "POST", p.host, "/v1/sites/trip/visitor/auth", map[string]any{"email": "x@example.com"}, browser(p.host, "")); r.status == 403 {
		t.Fatalf("visitor sign-in refused: %d %s", r.status, r.body)
	}

	// Storage: a resource anyone may read stays closed to everyone but the
	// listed, whatever its site_passcode says.
	if r := p.at(t, "PUT", pcSiteDomain, "/v1/sites/trip/storage/resources/menu", map[string]any{"kind": "kv", "read": "anyone", "write": "owner", "site_passcode": "off"}, p.okey); r.status/100 != 2 {
		t.Fatalf("resource: %d %s", r.status, r.body)
	}
	p.at(t, "PUT", pcSiteDomain, "/v1/sites/trip/storage/kv/menu/keys/today", map[string]any{"value": passcodeCanary}, p.okey)
	for _, ck := range []string{"", dadHost} {
		r := p.at(t, "GET", p.host, "/v1/sites/trip/storage/kv/menu/keys/today", nil, browser(p.host, ck))
		noLeak("storage", r)
		if r.status != 403 {
			t.Fatalf("storage (%q): %d %s", ck, r.status, r.body)
		}
	}
	if r := p.at(t, "GET", p.host, "/v1/sites/trip/storage/kv/menu/keys/today", nil, browser(p.host, momHost)); r.status != 200 {
		t.Fatalf("mom storage: %d %s", r.status, r.body)
	}
	// A link opened straight from an email (a navigation, no Origin).
	nav := map[string]string{"Cookie": visitorCookieHost + "=" + momHost, "Sec-Fetch-Site": "none", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document"}
	if r := p.at(t, "GET", p.host, "/v1/sites/trip/storage/kv/menu/keys/today", nil, nav); r.status != 200 {
		t.Fatalf("mom storage link: %d %s", r.status, r.body)
	}
	nav["Cookie"] = visitorCookieHost + "=" + dadHost
	if r := p.at(t, "GET", p.host, "/v1/sites/trip/storage/kv/menu/keys/today", nil, nav); r.status != 403 {
		t.Fatalf("dad storage link: %d %s", r.status, r.body)
	}

	// A custom domain and a claimed name: the same gate, sessions per host.
	for _, domain := range []string{"goa-trip." + pcSiteDomain, "trip.example.org"} {
		if _, err := p.database.ExecContext(ctx, `UPDATE sites SET custom_domain = $2, domain_status = 'active', domain_verified_at = now() WHERE id = $1`, p.siteID, domain); err != nil {
			t.Fatal(err)
		}
		get := func(path string, h map[string]string) resp {
			if strings.HasSuffix(domain, pcSiteDomain) {
				return p.at(t, "GET", domain, path, nil, h)
			}
			return p.at(t, "GET", domain, "/internal/passcode"+path, nil, h)
		}
		if r := get("/app.js", nil); r.status != 401 || strings.Contains(string(r.body), passcodeCanary) {
			t.Fatalf("%s signed out: %d %s", domain, r.status, r.body)
		}
		if r := get("/app.js", cookieOf(momHost)); strings.Contains(string(r.body), passcodeCanary) {
			t.Fatalf("%s took the site host's session", domain)
		}
		momHere := p.session(t, v.mom, p.siteID, domain)
		if r := get("/app.js", cookieOf(momHere)); r.status != 200 || !strings.Contains(string(r.body), passcodeCanary) {
			t.Fatalf("%s mom: %d %s", domain, r.status, r.body)
		}
		dadHere := p.session(t, v.dad, p.siteID, domain)
		if r := get("/app.js", cookieOf(dadHere)); r.status != 403 || strings.Contains(string(r.body), passcodeCanary) {
			t.Fatalf("%s dad: %d %s", domain, r.status, r.body)
		}
		// The site host now redirects to the domain; nothing leaks there.
		noLeak("site host with a domain", p.at(t, "GET", p.host, "/app.js", nil, nil))
	}
	if _, err := p.database.ExecContext(ctx, `UPDATE sites SET custom_domain = NULL, domain_status = NULL, domain_verified_at = NULL WHERE id = $1`, p.siteID); err != nil {
		t.Fatal(err)
	}

	// The person host when the site is the home page.
	uid, _ := p.userID(t, p.olive)
	if _, err := p.database.ExecContext(ctx, `UPDATE users SET home_site_id = $2 WHERE id = $1`, uid, p.siteID); err != nil {
		t.Fatal(err)
	}
	if r := p.at(t, "GET", p.person, "/", nil, nil); r.status != 401 || strings.Contains(string(r.body), passcodeCanary) {
		t.Fatalf("home page signed out: %d %s", r.status, r.body)
	}
	momPerson := p.session(t, v.mom, p.siteID, p.person)
	if r := p.at(t, "GET", p.person, "/", nil, cookieOf(momPerson)); r.status != 200 || !strings.Contains(string(r.body), passcodeCanary) {
		t.Fatalf("home page mom: %d %s", r.status, r.body)
	}
	dadPerson := p.session(t, v.dad, p.siteID, p.person)
	if r := p.at(t, "GET", p.person, "/app.js", nil, cookieOf(dadPerson)); r.status != 403 || strings.Contains(string(r.body), passcodeCanary) {
		t.Fatalf("home page dad: %d %s", r.status, r.body)
	}
	// Hidden from the person's public listing.
	if _, err := p.database.ExecContext(ctx, `UPDATE users SET home_site_id = NULL WHERE id = $1`, uid); err != nil {
		t.Fatal(err)
	}
	if r := p.at(t, "GET", pcSiteDomain, "/v1/u/"+p.handle+"/showcase.json", nil, nil); strings.Contains(string(r.body), `"trip"`) {
		t.Fatalf("listed on the public page: %s", r.body)
	}
}

func TestViewersListChangesAtOnce(t *testing.T) {
	v := newViewerApp(t)
	p := v.passcodeApp
	mom := p.session(t, v.mom, p.siteID, p.host)
	dad := p.session(t, v.dad, p.siteID, p.host)
	status := func(ck string) int { return p.at(t, "GET", p.host, "/app.js", nil, cookieOf(ck)).status }
	if status(mom) != 200 || status(dad) != 403 {
		t.Fatal("start")
	}
	if r := p.grant(t, v.dad.email); r.status != 200 {
		t.Fatal(r.status)
	}
	if status(dad) != 200 {
		t.Fatal("dad not let in at once")
	}
	if r := p.at(t, "DELETE", pcSiteDomain, "/v1/sites/trip/viewers/"+url.PathEscape(v.mom.email), nil, p.okey); r.status != 200 {
		t.Fatal(r.status)
	}
	if status(mom) != 403 {
		t.Fatal("mom not locked out at once")
	}
	if r := p.at(t, "GET", p.host, "/v1/sites/trip/state", nil, browser(p.host, mom)); r.status != 403 {
		t.Fatalf("mom data after removal: %d", r.status)
	}
	// A suspended account is out too.
	uid, _ := p.userID(t, v.dad)
	if _, err := p.database.Exec(`UPDATE users SET suspended_at = now() WHERE id = $1`, uid); err != nil {
		t.Fatal(err)
	}
	if status(dad) == 200 {
		t.Fatal("suspended viewer let in")
	}
	// Open to anyone: no sign-in needed.
	p.at(t, "PUT", pcSiteDomain, "/v1/sites/trip/access", map[string]any{"access": "anyone"}, p.okey)
	if r := p.at(t, "GET", p.host, "/app.js", nil, nil); r.status != 200 {
		t.Fatalf("anyone: %d", r.status)
	}
}

func TestViewersSignInForm(t *testing.T) {
	v := newViewerApp(t)
	p := v.passcodeApp
	mail := &codeMailer{}
	p.sites.mailer = mail
	form := func(host string, vals url.Values, extra map[string]string) resp {
		h := map[string]string{"Origin": "https://" + host, "Content-Type": "application/x-www-form-urlencoded", "Sec-Fetch-Site": "same-origin"}
		for k, val := range extra {
			h[k] = val
		}
		return p.at(t, "POST", host, "/v1/site-signin", vals.Encode(), h)
	}
	// From another origin: refused, no code sent.
	if r := form(p.host, url.Values{"email": {v.mom.email}, "next": {"/"}}, map[string]string{"Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site"}); r.status != 403 || mail.code(v.mom.email) != "" {
		t.Fatalf("cross-origin: %d %s", r.status, r.body)
	}
	r := form(p.host, url.Values{"email": {strings.ToUpper(v.mom.email)}, "next": {"/days/"}}, nil)
	if r.status != 401 || !strings.Contains(string(r.body), `name="code"`) || strings.Contains(string(r.body), passcodeCanary) {
		t.Fatalf("code page: %d %s", r.status, r.body)
	}
	code := mail.code(v.mom.email)
	if code == "" {
		t.Fatal("no code sent")
	}
	if r := form(p.host, url.Values{"email": {v.mom.email}, "code": {"000000"}, "next": {"/days/"}}, nil); r.status != 401 || !strings.Contains(string(r.body), "isn’t right") {
		t.Fatalf("wrong code: %d %s", r.status, r.body)
	}
	r = form(p.host, url.Values{"email": {v.mom.email}, "code": {code}, "next": {"/days/"}}, nil)
	if r.status != http.StatusSeeOther || r.header.Get("Location") != "/days/" {
		t.Fatalf("sign in: %d %v %s", r.status, r.header, r.body)
	}
	var ck string
	for _, c := range (&http.Response{Header: r.header}).Cookies() {
		if c.Name == visitorCookieHost {
			ck = c.Value
		}
	}
	if ck == "" {
		t.Fatal("no session cookie")
	}
	if r := p.at(t, "GET", p.host, "/days/", nil, cookieOf(ck)); r.status != 200 || !strings.Contains(string(r.body), passcodeCanary) {
		t.Fatalf("after sign-in: %d %s", r.status, r.body)
	}
	// Sign out from the page: the session ends.
	so := p.at(t, "POST", p.host, "/v1/site-signout", url.Values{"next": {"/"}}.Encode(), map[string]string{"Origin": "https://" + p.host, "Content-Type": "application/x-www-form-urlencoded", "Sec-Fetch-Site": "same-origin", "Cookie": visitorCookieHost + "=" + ck})
	if so.status != http.StatusSeeOther {
		t.Fatalf("sign out: %d %s", so.status, so.body)
	}
	if r := p.at(t, "GET", p.host, "/", nil, cookieOf(ck)); r.status != 401 {
		t.Fatalf("after sign out: %d", r.status)
	}
	// next never leaves the host.
	for _, next := range []string{"//evil.example/", "https://evil.example/", "/\\evil.example", "/internal/passcode//evil.example"} {
		r := form(p.host, url.Values{"email": {v.mom.email}, "code": {"1"}, "next": {next}}, nil)
		if loc := r.header.Get("Location"); loc != "" && loc != "/" {
			t.Fatalf("next %q: Location %q", next, loc)
		}
		if strings.Contains(string(r.body), "evil.example") {
			t.Fatalf("next %q echoed: %s", next, r.body)
		}
	}
	// The Google button points at this host's own sign-in start.
	viewerGoogle = true
	t.Cleanup(func() { viewerGoogle = false })
	page := p.at(t, "GET", p.host, "/days/", nil, nil)
	if !regexp.MustCompile(`href="/v1/visitor/oauth/google\?return_to=https%3A%2F%2F` + regexp.QuoteMeta(url.QueryEscape(p.host)) + `%2Fdays%2F"`).Match(page.body) {
		t.Fatalf("google link: %s", page.body)
	}
	// On a site that is open to anyone the form just goes back.
	p.at(t, "PUT", pcSiteDomain, "/v1/sites/trip/access", map[string]any{"access": "anyone"}, p.okey)
	if r := form(p.host, url.Values{"email": {v.dad.email}, "next": {"/"}}, nil); r.status != http.StatusSeeOther {
		t.Fatalf("open site: %d", r.status)
	}
}

func TestStorageVisitorEmails(t *testing.T) {
	p := newPasscodeApp(t)
	mom := p.newPerson(t, "mom")
	dad := p.newPerson(t, "dad")
	momS := p.session(t, mom, p.siteID, p.host)
	dadS := p.session(t, dad, p.siteID, p.host)
	momID, _ := p.userID(t, mom)
	dadID, _ := p.userID(t, dad)
	for name, body := range map[string]map[string]any{
		"orders": {"kind": "sqlite", "read": "own", "write": "signed-in", "write_mode": "add"},
		"notes":  {"kind": "kv", "read": "owner", "write": "signed-in", "write_mode": "add"},
	} {
		if r := p.at(t, "PUT", pcSiteDomain, "/v1/sites/trip/storage/resources/"+name, body, p.okey); r.status/100 != 2 {
			t.Fatalf("%s: %d %s", name, r.status, r.body)
		}
	}
	if r := p.at(t, "POST", pcSiteDomain, "/v1/sites/trip/storage/sqlite/orders/schema", map[string]any{"sql": "CREATE TABLE IF NOT EXISTS orders (id INTEGER PRIMARY KEY, item TEXT, created_at TEXT)"}, p.okey); r.status != 200 {
		t.Fatalf("schema: %d %s", r.status, r.body)
	}
	if r := p.at(t, "POST", p.host, "/v1/sites/trip/storage/sqlite/orders/tables/orders/rows", map[string]any{"item": "cake"}, browser(p.host, momS)); r.status != 200 {
		t.Fatalf("mom order: %d %s", r.status, r.body)
	}
	if r := p.at(t, "PUT", p.host, "/v1/sites/trip/storage/kv/notes/keys/hello", map[string]any{"value": "hi"}, browser(p.host, dadS)); r.status != 200 {
		t.Fatalf("dad note: %d %s", r.status, r.body)
	}
	// Before: the owner sees only an id on the row.
	r := p.at(t, "POST", pcSiteDomain, "/v1/sites/trip/storage/sqlite/orders/query", map[string]any{"sql": "SELECT id, visitor_id FROM orders WHERE id = ?", "params": []any{1}}, p.okey)
	if r.status != 200 || !strings.Contains(string(r.body), momID) || strings.Contains(string(r.body), mom.email) {
		t.Fatalf("owner query: %d %s", r.status, r.body)
	}
	// KV: the owner sees who wrote; a visitor never does.
	if r := p.at(t, "GET", pcSiteDomain, "/v1/sites/trip/storage/kv/notes/keys/hello", nil, p.okey); !strings.Contains(string(r.body), dadID) {
		t.Fatalf("owner kv: %s", r.body)
	}
	if r := p.at(t, "GET", pcSiteDomain, "/v1/sites/trip/storage/kv/notes/keys", nil, p.okey); !strings.Contains(string(r.body), dadID) {
		t.Fatalf("owner kv list: %s", r.body)
	}
	// The lookup: both writers resolve, a stranger does not.
	stranger := p.newPerson(t, "stranger")
	strangerID, _ := p.userID(t, stranger)
	r = p.at(t, "GET", pcSiteDomain, "/v1/sites/trip/storage/visitors?id="+momID+","+dadID+"&id="+strangerID, nil, p.okey)
	if r.status != 200 || !strings.Contains(string(r.body), mom.email) || !strings.Contains(string(r.body), dad.email) || strings.Contains(string(r.body), stranger.email) {
		t.Fatalf("lookup: %d %s", r.status, r.body)
	}
	// An id the owner wrote into their own table, or of someone who signed
	// in only on another site, never resolves.
	if r := p.at(t, "PUT", pcSiteDomain, "/v1/sites/trip/storage/resources/box", map[string]any{"kind": "sqlite", "read": "owner", "write": "owner"}, p.okey); r.status/100 != 2 {
		t.Fatalf("box: %d %s", r.status, r.body)
	}
	p.at(t, "POST", pcSiteDomain, "/v1/sites/trip/storage/sqlite/box/schema", map[string]any{"sql": "CREATE TABLE t (visitor_id TEXT)"}, p.okey)
	if r := p.at(t, "POST", pcSiteDomain, "/v1/sites/trip/storage/sqlite/box/execute", map[string]any{"sql": "INSERT INTO t(visitor_id) VALUES(?)", "params": []any{strangerID}}, p.okey); r.status != 200 {
		t.Fatalf("plant: %d %s", r.status, r.body)
	}
	r = p.at(t, "POST", pcSiteDomain, "/v1/sites/other/files", map[string]any{"files": map[string]string{"index.html": "o"}}, map[string]string{"X-API-Key": stranger.key})
	if r.status != http.StatusCreated {
		t.Fatalf("stranger site: %d", r.status)
	}
	p.session(t, stranger, p.siteIDAny(t, strangerID, "other"), "other."+pcSiteDomain)
	if r := p.at(t, "GET", pcSiteDomain, "/v1/sites/trip/storage/visitors?id="+strangerID, nil, p.okey); r.status != 200 || strings.Contains(string(r.body), stranger.email) || !strings.Contains(string(r.body), `"found":false`) {
		t.Fatalf("planted id resolved: %d %s", r.status, r.body)
	}
	// Owner only: never a visitor, never another account.
	if r := p.at(t, "GET", p.host, "/v1/sites/trip/storage/visitors?id="+momID, nil, browser(p.host, momS)); r.status != 403 || strings.Contains(string(r.body), mom.email) {
		t.Fatalf("visitor lookup: %d %s", r.status, r.body)
	}
	if r := p.at(t, "GET", pcSiteDomain, "/v1/sites/trip/storage/visitors?id="+momID, nil, map[string]string{"X-API-Key": stranger.key}); strings.Contains(string(r.body), mom.email) {
		t.Fatalf("another account's lookup: %d %s", r.status, r.body)
	}
	if r := p.at(t, "GET", pcSiteDomain, "/v1/sites/trip/storage/visitors?id=not-an-id", nil, p.okey); r.status != 400 {
		t.Fatalf("bad id: %d", r.status)
	}
	// A visitor reading their own rows never sees an email.
	if r := p.at(t, "GET", p.host, "/v1/sites/trip/storage/sqlite/orders/tables/orders/rows", nil, browser(p.host, momS)); strings.Contains(string(r.body), "@") {
		t.Fatalf("visitor rows: %s", r.body)
	}
}
