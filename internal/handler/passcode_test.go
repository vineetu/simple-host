package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/config"
	db "github.com/vsriram/simple-host/internal/db"
)

const testPasscodeKey = "q83vEjRWeJq83vEjRWeJq83vEjRWeJq83vEjRWeJq80=" // 32 bytes, tests only

// withLimits runs the rest of the test with l applied, then puts the
// previous limits back.
func withPasscodeLimits(t *testing.T, change func(l *config.Limits)) {
	t.Helper()
	old := *config.Active()
	l := old
	change(&l)
	config.SetActive(l)
	t.Cleanup(func() { config.SetActive(old) })
}

func TestNormalizePasscode(t *testing.T) {
	withPasscodeLimits(t, func(l *config.Limits) { l.PasscodeMinLength = 6 })
	for in, want := range map[string]string{
		"123456":                 "123456",
		"  123456  ":             "123456",
		"Trip 2026 Goa!":         "Trip 2026 Goa!",
		"ಬೆಂಗಳೂರು":               "ಬೆಂಗಳೂರು",
		"12345":                  "",
		"     1234   ":           "",
		"12345\n6":               "",
		"abc\tdef":               "",
		strings.Repeat("x", 128): strings.Repeat("x", 128),
		strings.Repeat("x", 129): "",
	} {
		got, err := normalizePasscode(in)
		if want == "" {
			if err == nil {
				t.Errorf("%q: accepted as %q", in, got)
			}
			continue
		}
		if err != nil || got != want {
			t.Errorf("%q: got %q, %v; want %q", in, got, err, want)
		}
	}
	withPasscodeLimits(t, func(l *config.Limits) { l.PasscodeMinLength = 10 })
	if _, err := normalizePasscode("123456789"); err == nil {
		t.Fatal("PASSCODE_MIN_LENGTH=10 accepted 9 characters")
	}
	withPasscodeLimits(t, func(l *config.Limits) { l.PasscodeMinLength = 6 })
	for i := 0; i < 20; i++ {
		g, err := generatePasscode()
		if err != nil || !regexp.MustCompile(`^[0-9]{6}$`).MatchString(g) {
			t.Fatalf("generated %q, %v", g, err)
		}
	}
}

func TestPasscodeSealAndCookie(t *testing.T) {
	h := &SiteHandler{}
	if err := h.SetPasscodeKey("not base64!"); err == nil {
		t.Fatal("bad key accepted")
	}
	if err := h.SetPasscodeKey(base64.StdEncoding.EncodeToString([]byte("short"))); err == nil {
		t.Fatal("short key accepted")
	}
	if err := h.SetPasscodeKey(testPasscodeKey); err != nil {
		t.Fatal(err)
	}
	enc, err := h.passcode.seal("site-a", "123456")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(enc, []byte("123456")) {
		t.Fatal("sealed value holds the passcode")
	}
	if got, err := h.passcode.open("site-a", enc); err != nil || got != "123456" {
		t.Fatalf("open: %q %v", got, err)
	}
	if _, err := h.passcode.open("site-b", enc); err == nil {
		t.Fatal("sealed value opened for another site")
	}
	if !passcodeMatches("123456", "123456") || passcodeMatches("123457", "123456") || passcodeMatches("1234567", "123456") {
		t.Fatal("passcodeMatches")
	}

	now := time.Unix(1_800_000_000, 0)
	h.passcode.now = func() time.Time { return now }
	mk := func(host string) *http.Request {
		r := httptest.NewRequest("GET", "https://"+host+"/", nil)
		r.Host = host
		r.Header.Set("X-Forwarded-Proto", "https")
		return r
	}
	rec := httptest.NewRecorder()
	h.setUnlockCookie(rec, mk("shop.olive.example.app"), "site-a", 3)
	c := rec.Result().Cookies()[0]
	if !strings.HasPrefix(c.Name, "__Host-sh_pass_") || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.Domain != "" {
		t.Fatalf("cookie attributes: %+v", c)
	}
	if c.MaxAge < 399*24*3600 {
		t.Fatalf("unlock should last ~400 days, Max-Age %d", c.MaxAge)
	}
	with := func(host, name, value string) *http.Request {
		r := mk(host)
		r.AddCookie(&http.Cookie{Name: name, Value: value})
		return r
	}
	if !h.unlocked(with("shop.olive.example.app", c.Name, c.Value), "site-a", 3) {
		t.Fatal("valid cookie refused")
	}
	if h.unlocked(with("blog.olive.example.app", c.Name, c.Value), "site-a", 3) {
		t.Fatal("cookie accepted on another host")
	}
	if h.unlocked(with("shop.olive.example.app", c.Name, c.Value), "site-b", 3) {
		t.Fatal("cookie accepted for another site")
	}
	if h.unlocked(with("shop.olive.example.app", c.Name, c.Value), "site-a", 4) {
		t.Fatal("cookie of an old generation accepted")
	}
	tampered := strings.Replace(c.Value, ".3.", ".4.", 1)
	if h.unlocked(with("shop.olive.example.app", c.Name, tampered), "site-a", 4) {
		t.Fatal("tampered cookie accepted")
	}
	plain := strings.TrimPrefix(c.Name, "__Host-")
	if h.unlocked(with("shop.olive.example.app", plain, c.Value), "site-a", 3) {
		t.Fatal("plain-named cookie accepted over HTTPS")
	}
	now = now.Add(401 * 24 * time.Hour)
	if h.unlocked(with("shop.olive.example.app", c.Name, c.Value), "site-a", 3) {
		t.Fatal("expired cookie accepted")
	}
	// Another key: every cookie made under the old one stops working.
	now = time.Unix(1_800_000_000, 0)
	h2 := &SiteHandler{}
	_ = h2.SetPasscodeKey(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	h2.passcode.now = h.passcode.now
	if h2.unlocked(with("shop.olive.example.app", c.Name, c.Value), "site-a", 3) {
		t.Fatal("cookie accepted under another key")
	}
}

func TestSafeNext(t *testing.T) {
	for in, want := range map[string]string{
		"/":              "/",
		"/a/b?x=1":       "/a/b?x=1",
		"//evil.com/x":   "/",
		"/\\evil.com":    "/",
		"https://evil":   "/",
		"":               "/",
		"/a\r\nSet-X: 1": "/",
		"javascript:x":   "/",
		// nginx's and Caddy's marker rewrite: back to what the visitor asked for.
		"/internal/passcode/some/page?a=1": "/some/page?a=1",
		"/internal/passcode/":              "/",
		"/internal/passcode":               "/",
		"/internal/passcode?x=1":           "/?x=1",
		"/internal/passcode//evil.com":     "/",
		"/internal/passcodex/a":            "/",
		// Never one of the app's internal pages.
		"/internal/offline":             "/",
		"/internal/family/a":            "/",
		"/internal":                     "/",
		"/%69nternal/passcode/x":        "/",
		"/internal/passcode/internal/x": "/",
		"/internals/a":                  "/internals/a",
	} {
		if got := safeNext(in); got != want {
			t.Errorf("%q: %q want %q", in, got, want)
		}
	}
}

// A disk-served address (custom domain, family) reaches the gate through
// nginx's rewrite to /internal/passcode$uri: the form's next and the unlock's
// redirect are the page the visitor asked for, never the rewrite (which is
// a 404 from outside).
func TestPasscodeGateNextThroughRewrite(t *testing.T) {
	p := newPasscodeApp(t)
	const code = "domain-pass-1"
	p.lock(t, code)
	const domain = "trip.example.org"
	if _, err := p.database.ExecContext(context.Background(), `UPDATE sites SET custom_domain = $2, domain_status = 'active', domain_verified_at = now() WHERE id = $1`, p.siteID, domain); err != nil {
		t.Fatal(err)
	}
	r := p.at(t, "GET", domain, "/internal/passcode/days/?d=2", nil, nil)
	if r.status != http.StatusUnauthorized || !strings.Contains(string(r.body), `name="next" value="/days/?d=2"`) || strings.Contains(string(r.body), "/internal/") {
		t.Fatalf("gate next through the rewrite: %d %s", r.status, r.body)
	}
	if r := p.at(t, "GET", domain, "/internal/passcode/robots.txt", nil, nil); r.status != 200 || !strings.Contains(string(r.body), "Disallow: /") {
		t.Fatalf("robots.txt through the rewrite: %d %s", r.status, r.body)
	}
	for next, want := range map[string]string{
		"/internal/passcode/days/?d=2": "/days/?d=2",
		"/internal/offline":            "/",
		"/internal/passcode//evil.com": "/",
	} {
		r := p.unlock(t, domain, code, next, nil)
		if r.status != http.StatusSeeOther || r.header.Get("Location") != want {
			t.Fatalf("unlock next %q: %d %q want %q", next, r.status, r.header.Get("Location"), want)
		}
	}
}

// Every Go route to a site's page-facing data is either the owner's
// (authMiddleware) or behind the passcode gate; a new route that is neither
// fails here. OPTIONS preflights carry nothing.
func TestPasscodeRouteTable(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	route := regexp.MustCompile(`mux\.Handle(?:Func)?\("([A-Z]+) (/v1/(?:u/\{handle\}/)?sites/\{sitename\}/[^"]*)"`)
	n := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(b), "\n") {
			m := route.FindStringSubmatch(line)
			if m == nil || m[1] == "OPTIONS" {
				continue
			}
			n++
			if !strings.Contains(line, "authMiddleware(") && !strings.Contains(line, "h.passcodeGate(") && !(strings.Contains(m[2], "/storage/") && (strings.Contains(line, "h.storage") || strings.Contains(line, "h.listStorageResources") || strings.Contains(line, "h.getStorageUsage") || strings.Contains(line, "h.putStorageResource") || strings.Contains(line, "h.deleteStorageResource") || strings.Contains(line, "h.createStorageFileLink"))) {
				t.Errorf("%s: %s %s is neither owner-only nor behind the passcode gate", f, m[1], m[2])
			}
		}
	}
	if n < 100 {
		t.Fatalf("found only %d routes; the pattern no longer matches Register", n)
	}
}

// passcodeApp is a canonical site-host router with passcodes on, olive's
// site "trip" deployed with a canary in every kind of file, and olive's
// certificate ready.
type passcodeApp struct {
	*privateApp
	olive           person
	handle, siteID  string
	host, person    string
	okey            map[string]string
	canary, pageDir string
}

const passcodeCanary = "CANARY-7f3a91-do-not-leak"

func newPasscodeApp(t *testing.T) *passcodeApp {
	t.Helper()
	a, dir := newSiteApp(t, "canonical")
	if err := a.sites.SetPasscodeKey(testPasscodeKey); err != nil {
		t.Fatal(err)
	}
	withPasscodeLimits(t, func(l *config.Limits) { l.SitePasscodes = true; l.PasscodeMinLength = 6 })
	p := &passcodeApp{privateApp: a, olive: a.newPerson(t, "olive"), canary: passcodeCanary}
	_, p.handle = a.userID(t, p.olive)
	markReady(t, dir, p.handle)
	p.okey = map[string]string{"X-API-Key": p.olive.key}
	r := a.at(t, "POST", pcSiteDomain, "/v1/sites/trip/files", map[string]any{"files": map[string]string{
		"index.html":      "<h1>trip " + passcodeCanary + "</h1>",
		"app.js":          "var s='" + passcodeCanary + "';",
		"data.json":       `{"x":"` + passcodeCanary + `"}`,
		"img.svg":         `<svg xmlns="http://www.w3.org/2000/svg"><text>` + passcodeCanary + `</text></svg>`,
		"days/index.html": "day " + passcodeCanary,
		"404.html":        "missing " + passcodeCanary,
		"robots.txt":      "User-agent: *\nAllow: /\n# " + passcodeCanary,
	}}, p.okey)
	if r.status != http.StatusCreated {
		t.Fatalf("deploy: %d %s", r.status, r.body)
	}
	p.siteID = a.siteID(t, p.olive, "trip")
	p.person = p.handle + "." + pcSiteDomain
	p.host = "trip." + p.person
	uid, _ := a.userID(t, p.olive)
	p.pageDir = a.sites.disk.SiteDir(uid, "trip")
	return p
}

func (p *passcodeApp) lock(t *testing.T, code string) resp {
	t.Helper()
	return p.at(t, "PUT", pcSiteDomain, "/v1/sites/trip/lock", map[string]any{"passcode": code}, p.okey)
}

// unlock posts the gate's form from host and returns the answer.
func (p *passcodeApp) unlock(t *testing.T, host, code, next string, extra map[string]string) resp {
	t.Helper()
	form := url.Values{"passcode": {code}, "next": {next}}.Encode()
	h := map[string]string{"Origin": "https://" + host, "Content-Type": "application/x-www-form-urlencoded", "Sec-Fetch-Site": "same-origin"}
	for k, v := range extra {
		h[k] = v
	}
	return p.at(t, "POST", host, "/v1/site-unlock", form, h)
}

func cookieHeader(t *testing.T, r resp) string {
	t.Helper()
	for _, c := range (&http.Response{Header: r.header}).Cookies() {
		if strings.Contains(c.Name, "sh_pass_") {
			return c.Name + "=" + c.Value
		}
	}
	t.Fatalf("no unlock cookie in %v", r.header)
	return ""
}

func TestPasscodeOwnerAPI(t *testing.T) {
	p := newPasscodeApp(t)

	// Off: refused with a clear 409, nothing changes.
	withPasscodeLimits(t, func(l *config.Limits) { l.SitePasscodes = false })
	if r := p.lock(t, "123456"); r.status != http.StatusConflict || r.json(t)["code"] != "passcodes_not_enabled" {
		t.Fatalf("off: %d %s", r.status, r.body)
	}
	if _, err := os.Stat(filepath.Join(p.pageDir, "passcode")); err == nil {
		t.Fatal("off: marker written")
	}
	withPasscodeLimits(t, func(l *config.Limits) { l.SitePasscodes = true })

	if r := p.lock(t, "12345"); r.status != http.StatusBadRequest || r.json(t)["code"] != "invalid_passcode" {
		t.Fatalf("short: %d %s", r.status, r.body)
	}
	if r := p.at(t, "PUT", pcSiteDomain, "/v1/sites/trip/lock", map[string]any{}, p.okey); r.status != http.StatusBadRequest {
		t.Fatalf("empty body: %d", r.status)
	}
	r := p.lock(t, "  goa-2026  ")
	if r.status != 200 || r.json(t)["passcode"] != "goa-2026" || r.json(t)["passcode_protected"] != true {
		t.Fatalf("set: %d %s", r.status, r.body)
	}
	if _, err := os.Stat(filepath.Join(p.pageDir, "passcode")); err != nil {
		t.Fatalf("marker: %v", err)
	}
	var enc []byte
	var gen int
	if err := p.database.QueryRow(`SELECT passcode_enc, passcode_generation FROM sites WHERE id = $1`, p.siteID).Scan(&enc, &gen); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(enc, []byte("goa-2026")) {
		t.Fatal("passcode stored in the clear")
	}
	var legacy *string
	_ = p.database.QueryRow(`SELECT view_password_hash FROM sites WHERE id = $1`, p.siteID).Scan(&legacy)
	if legacy != nil {
		t.Fatal("view_password_hash written")
	}
	// Read back: the owner only.
	if r := p.at(t, "GET", pcSiteDomain, "/v1/sites/trip/lock", nil, p.okey); r.status != 200 || r.json(t)["passcode"] != "goa-2026" || r.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("read: %d %s", r.status, r.body)
	}
	other := p.newPerson(t, "oscar")
	if r := p.at(t, "GET", pcSiteDomain, "/v1/sites/trip/lock", nil, map[string]string{"X-API-Key": other.key}); r.status != http.StatusNotFound {
		t.Fatalf("another account read: %d %s", r.status, r.body)
	}
	if r := p.at(t, "GET", pcSiteDomain, "/v1/sites/trip/lock", nil, map[string]string{"X-API-Key": p.admin}); r.status != http.StatusNotFound || strings.Contains(string(r.body), "goa-2026") {
		t.Fatalf("admin read: %d %s", r.status, r.body)
	}
	// The site list says protected (never the passcode).
	if r := p.at(t, "GET", pcSiteDomain, "/v1/sites", nil, p.okey); !strings.Contains(string(r.body), `"passcode_protected":true`) || strings.Contains(string(r.body), "goa-2026") {
		t.Fatalf("site list: %s", r.body)
	}
	// Same passcode again: nothing changes (no sign-out).
	p.lock(t, "goa-2026")
	var gen2 int
	_ = p.database.QueryRow(`SELECT passcode_generation FROM sites WHERE id = $1`, p.siteID).Scan(&gen2)
	if gen2 != gen {
		t.Fatalf("same passcode bumped the generation %d -> %d", gen, gen2)
	}
	// Generate: six digits.
	if r := p.at(t, "PUT", pcSiteDomain, "/v1/sites/trip/lock", map[string]any{"generate": true}, p.okey); r.status != 200 || !regexp.MustCompile(`^[0-9]{6}$`).MatchString(r.json(t)["passcode"].(string)) {
		t.Fatalf("generate: %d %s", r.status, r.body)
	}
	if r := p.at(t, "POST", pcSiteDomain, "/v1/sites/trip/lock/sign-out-everyone", nil, p.okey); r.status != 200 {
		t.Fatalf("sign out everyone: %d %s", r.status, r.body)
	}
	if r := p.at(t, "DELETE", pcSiteDomain, "/v1/sites/trip/lock", nil, p.okey); r.status != 200 || r.json(t)["passcode_protected"] != false {
		t.Fatalf("remove: %d %s", r.status, r.body)
	}
	if _, err := os.Stat(filepath.Join(p.pageDir, "passcode")); err == nil {
		t.Fatal("marker left after remove")
	}
	if r := p.at(t, "POST", pcSiteDomain, "/v1/sites/trip/lock/sign-out-everyone", nil, p.okey); r.status != http.StatusConflict {
		t.Fatalf("sign out without a passcode: %d", r.status)
	}
	if r := p.at(t, "GET", p.host, "/", nil, nil); r.status != 200 {
		t.Fatalf("open again: %d", r.status)
	}

	// Admin moderation: remove through the admin route (logged), and a
	// preview link without the passcode.
	p.lock(t, "goa-2026")
	if r := p.at(t, "POST", pcSiteDomain, "/v1/admin/sites/"+p.siteID+"/versions/1/preview-link", nil, map[string]string{"X-API-Key": p.admin}); r.status != 200 || !strings.Contains(r.json(t)["url"].(string), "/__preview/1/") {
		t.Fatalf("admin preview link: %d %s", r.status, r.body)
	}
	if r := p.at(t, "DELETE", pcSiteDomain, "/v1/admin/sites/"+p.siteID+"/lock", nil, map[string]string{"X-API-Key": p.admin}); r.status != 200 {
		t.Fatalf("admin remove: %d %s", r.status, r.body)
	}
	if r := p.at(t, "DELETE", pcSiteDomain, "/v1/admin/sites/"+p.siteID+"/lock", nil, p.okey); r.status != http.StatusNotFound && r.status != http.StatusForbidden {
		t.Fatalf("owner on the admin route: %d", r.status)
	}
}

// A shared-origin server (no person or site hosts) refuses a passcode.
func TestPasscodeNeedsOwnAddress(t *testing.T) {
	a := newPersonApp(t, "off")
	_ = a.sites.SetPasscodeKey(testPasscodeKey)
	withPasscodeLimits(t, func(l *config.Limits) { l.SitePasscodes = true })
	olive := a.newPerson(t, "olive")
	a.deploy(t, olive, "shop")
	r := a.at(t, "PUT", pcSiteDomain, "/v1/sites/shop/lock", map[string]any{"passcode": "123456"}, map[string]string{"X-API-Key": olive.key})
	if r.status != http.StatusConflict || r.json(t)["code"] != "passcode_needs_own_address" {
		t.Fatalf("%d %s", r.status, r.body)
	}
	// And a server without the key refuses too.
	b := newPersonApp(t, "canonical")
	oscar := b.newPerson(t, "oscar")
	b.deploy(t, oscar, "shop")
	if r := b.at(t, "PUT", pcSiteDomain, "/v1/sites/shop/lock", map[string]any{"passcode": "123456"}, map[string]string{"X-API-Key": oscar.key}); r.status != http.StatusConflict || r.json(t)["code"] != "passcodes_not_enabled" {
		t.Fatalf("no key: %d %s", r.status, r.body)
	}
}

// The canary sweep: with a passcode on, no address of the site shows any
// of its files to a visitor without the unlock, a link-preview bot
// included; the unlock opens exactly this site on exactly this host.
func TestPasscodeCanarySweep(t *testing.T) {
	p := newPasscodeApp(t)
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	const code = "Canary-Pass-5150"
	if r := p.lock(t, code); r.status != 200 {
		t.Fatalf("lock: %d %s", r.status, r.body)
	}
	// A claimed free name and a custom domain for the same site are tested
	// in their own sites below; here: site host, person path, legacy.
	paths := []string{"/", "/index.html", "/app.js", "/data.json", "/img.svg", "/days/", "/days", "/nope", "/trip/index.html", "/" + p.handle + "/trip/app.js"}
	uas := []string{"", "Slackbot-LinkExpanding 1.0 (+https://api.slack.com/robots)", "facebookexternalhit/1.1", "Twitterbot/1.0"}
	noLeak := func(what string, r resp) {
		t.Helper()
		if strings.Contains(string(r.body), passcodeCanary) {
			t.Fatalf("%s leaked the canary: %d %s", what, r.status, r.body)
		}
		if r.status == 200 && !strings.HasPrefix(string(r.body), "User-agent") {
			t.Fatalf("%s answered 200: %s", what, r.body)
		}
	}
	for _, path := range paths {
		for _, ua := range uas {
			h := map[string]string{"User-Agent": ua}
			r := p.at(t, "GET", p.host, path, nil, h)
			noLeak("site host "+path, r)
			if r.status != http.StatusUnauthorized {
				t.Fatalf("site host %s: %d (want the gate, before any redirect)", path, r.status)
			}
			if r.header.Get("X-Robots-Tag") == "" || r.header.Get("Cache-Control") != "no-store" || !strings.Contains(r.header.Get("Content-Security-Policy"), "default-src 'none'") {
				t.Fatalf("gate headers: %v", r.header)
			}
			if !strings.Contains(string(r.body), "This site is protected") || (!strings.Contains(path, p.handle) && strings.Contains(strings.ReplaceAll(string(r.body), p.host, ""), p.handle)) || strings.Contains(string(r.body), "og:image") {
				t.Fatalf("gate body: %s", r.body)
			}
			noLeak("HEAD "+path, p.at(t, "HEAD", p.host, path, nil, h))
			noLeak("person path "+path, p.at(t, "GET", p.person, "/trip"+path, nil, h))
			noLeak("legacy "+path, p.at(t, "GET", pcContentHost, "/internal/site-redirect/"+p.handle+"/trip"+path, nil, h))
			noLeak("legacy marker "+path, p.at(t, "GET", pcContentHost, "/internal/passcode/"+p.handle+"/trip"+path, nil, h))
		}
	}
	// Scripts and images from any page get a plain 401, never the page.
	r := p.at(t, "GET", p.host, "/app.js", nil, map[string]string{"Sec-Fetch-Dest": "script", "Sec-Fetch-Site": "same-site", "Sec-Fetch-Mode": "no-cors"})
	if r.status != 401 || strings.Contains(string(r.body), "<form") {
		t.Fatalf("script request: %d %s", r.status, r.body)
	}
	// robots.txt: Disallow, not the site's own.
	if r := p.at(t, "GET", p.host, "/robots.txt", nil, nil); r.status != 200 || !strings.Contains(string(r.body), "Disallow: /") || strings.Contains(string(r.body), passcodeCanary) {
		t.Fatalf("robots.txt: %d %s", r.status, r.body)
	}
	// Data: no reading without the unlock, on any host, with or without Origin.
	for _, host := range []string{pcSiteDomain, p.host, p.person, pcContentHost} {
		for _, path := range []string{"/v1/sites/trip/state", "/v1/u/" + p.handle + "/sites/trip/state", "/v1/u/" + p.handle + "/sites/trip/collections/x", "/v1/sites/trip/me"} {
			r := p.at(t, "GET", host, path, nil, nil)
			if r.status == 200 {
				t.Fatalf("data %s%s without unlock: %d %s", host, path, r.status, r.body)
			}
		}
	}
	if r := p.at(t, "GET", pcSiteDomain, "/v1/sites/trip/state", nil, nil); r.status != http.StatusForbidden || r.json(t)["code"] != "site_locked" {
		t.Fatalf("apex state read: %d %s", r.status, r.body)
	}
	if r := p.at(t, "GET", pcSiteDomain, "/v1/sites/trip/state", nil, p.okey); r.status != 200 {
		t.Fatalf("owner key state read: %d %s", r.status, r.body)
	}
	if r := p.at(t, "GET", pcSiteDomain, "/v1/sites/trip/state", nil, map[string]string{"X-API-Key": p.admin}); r.status != 200 {
		t.Fatalf("admin key state read: %d %s", r.status, r.body)
	}
	// Another account with its own site of the same name, and its own key:
	// the bare name still means olive's (older) site for some routes, so
	// that key must not open it.
	mallory := p.newPerson(t, "mallory")
	if r := p.at(t, "POST", pcSiteDomain, "/v1/sites/trip/files", map[string]any{"files": map[string]string{"index.html": "m"}}, map[string]string{"X-API-Key": mallory.key}); r.status != http.StatusCreated {
		t.Fatalf("mallory deploy: %d", r.status)
	}
	p.at(t, "PUT", pcSiteDomain, "/v1/sites/trip/state", map[string]any{"c": passcodeCanary}, p.okey)
	p.at(t, "POST", pcSiteDomain, "/v1/sites/trip/collections/notes", map[string]any{"c": passcodeCanary}, p.okey)
	for _, path := range []string{"/v1/sites/trip/state", "/v1/sites/trip/collections/notes", "/v1/sites/trip/data/notes"} {
		r := p.at(t, "GET", pcSiteDomain, path, nil, map[string]string{"X-API-Key": mallory.key})
		if strings.Contains(string(r.body), passcodeCanary) {
			t.Fatalf("another account's key read %s: %d %s", path, r.status, r.body)
		}
	}
	if !strings.Contains(logs.String(), "passcode_admin_bypass") {
		t.Fatal("admin bypass not logged")
	}
	if r := p.at(t, "OPTIONS", p.host, "/v1/sites/trip/state", nil, map[string]string{"Origin": "https://" + p.host, "Access-Control-Request-Method": "PUT"}); r.status == http.StatusForbidden && strings.Contains(string(r.body), "site_locked") {
		t.Fatal("preflight gated")
	}

	// ---- unlocking ----------------------------------------------------------
	if r := p.unlock(t, p.host, code, "/app.js", map[string]string{"Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site"}); r.status != http.StatusForbidden || len(r.header.Values("Set-Cookie")) != 0 {
		t.Fatalf("cross-origin unlock: %d", r.status)
	}
	if r := p.unlock(t, p.host, "wrong-one", "/app.js", nil); r.status != 401 || !strings.Contains(string(r.body), "isn’t right") || len(r.header.Values("Set-Cookie")) != 0 {
		t.Fatalf("wrong passcode: %d %s", r.status, r.body)
	}
	if r := p.unlock(t, pcContentHost, code, "/", nil); r.status != http.StatusSeeOther || len(r.header.Values("Set-Cookie")) != 0 {
		t.Fatalf("unlock on the content host: %d %v", r.status, r.header)
	}
	if r := p.unlock(t, p.host, code, "//evil.example/x", nil); r.status != http.StatusSeeOther || r.header.Get("Location") != "/" {
		t.Fatalf("open redirect: %d %s", r.status, r.header.Get("Location"))
	}
	r = p.unlock(t, p.host, "  "+code+" ", "/app.js?v=2", nil)
	if r.status != http.StatusSeeOther || r.header.Get("Location") != "/app.js?v=2" {
		t.Fatalf("unlock: %d %s %s", r.status, r.header.Get("Location"), r.body)
	}
	cookie := cookieHeader(t, r)
	withCookie := map[string]string{"Cookie": cookie}
	for _, path := range []string{"/", "/app.js", "/data.json", "/img.svg", "/days/"} {
		r := p.at(t, "GET", p.host, path, nil, withCookie)
		if r.status != 200 || !strings.Contains(string(r.body), passcodeCanary) {
			t.Fatalf("unlocked %s: %d %s", path, r.status, r.body)
		}
		if r.header.Get("Cache-Control") != "private, no-cache" || r.header.Get("Cross-Origin-Resource-Policy") != "same-origin" || !strings.Contains(strings.Join(r.header.Values("Vary"), ","), "Cookie") {
			t.Fatalf("unlocked headers: %v", r.header)
		}
	}
	if r := p.at(t, "GET", p.host, "/nope", nil, withCookie); r.status != 404 {
		t.Fatalf("unlocked 404: %d", r.status)
	}
	if r := p.at(t, "GET", p.host, "/app.js", nil, map[string]string{"Cookie": cookie, "Range": "bytes=0-3"}); r.status != http.StatusPartialContent {
		t.Fatalf("range: %d", r.status)
	}
	// XSSI: a sibling page cannot pull the site's script in, cookie or not.
	if r := p.at(t, "GET", p.host, "/app.js", nil, map[string]string{"Cookie": cookie, "Sec-Fetch-Site": "same-site", "Sec-Fetch-Mode": "no-cors", "Sec-Fetch-Dest": "script"}); r.status != 401 || strings.Contains(string(r.body), passcodeCanary) {
		t.Fatalf("cross-site script with cookie: %d %s", r.status, r.body)
	}
	// A link clicked elsewhere (a top-level navigation) works.
	if r := p.at(t, "GET", p.host, "/", nil, map[string]string{"Cookie": cookie, "Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document"}); r.status != 200 {
		t.Fatalf("navigation with cookie: %d", r.status)
	}
	// The cookie opens nothing on another host.
	if r := p.at(t, "GET", p.person, "/trip/", nil, withCookie); strings.Contains(string(r.body), passcodeCanary) {
		t.Fatal("cookie worked on the person host")
	}
	// Data with the cookie, from the site's own page.
	page := map[string]string{"Cookie": cookie, "Origin": "https://" + p.host}
	if r := p.at(t, "GET", p.host, "/v1/sites/trip/state", nil, page); r.status != 200 {
		t.Fatalf("state with cookie: %d %s", r.status, r.body)
	}

	// Change the passcode: everyone is out again.
	if r := p.lock(t, "Canary-Pass-6160"); r.status != 200 {
		t.Fatalf("change: %d", r.status)
	}
	if r := p.at(t, "GET", p.host, "/", nil, withCookie); r.status != 401 {
		t.Fatalf("old cookie after change: %d", r.status)
	}
	r = p.unlock(t, p.host, "Canary-Pass-6160", "/", nil)
	cookie = cookieHeader(t, r)
	if r := p.at(t, "GET", p.host, "/", nil, map[string]string{"Cookie": cookie}); r.status != 200 {
		t.Fatalf("new cookie: %d", r.status)
	}
	p.at(t, "POST", pcSiteDomain, "/v1/sites/trip/lock/sign-out-everyone", nil, p.okey)
	if r := p.at(t, "GET", p.host, "/", nil, map[string]string{"Cookie": cookie}); r.status != 401 {
		t.Fatalf("cookie after sign-out-everyone: %d", r.status)
	}

	// Previews bypass it: the owner's preview link, and reads from it.
	r = p.at(t, "POST", pcSiteDomain, "/v1/sites/trip/versions/1/preview-link", nil, p.okey)
	link, _ := url.Parse(r.json(t)["url"].(string))
	if r := p.at(t, "GET", link.Host, link.Path+"app.js", nil, nil); r.status != 200 || !strings.Contains(string(r.body), passcodeCanary) {
		t.Fatalf("preview: %d %s", r.status, r.body)
	}
	if r := p.at(t, "GET", p.host, "/v1/sites/trip/state", nil, map[string]string{"Referer": link.String(), "Origin": "https://" + p.host}); r.status != 200 {
		t.Fatalf("preview read: %d %s", r.status, r.body)
	}
	bad := strings.Replace(link.String(), "/__preview/1/", "/__preview/1/x", 1)
	if r := p.at(t, "GET", p.host, "/v1/sites/trip/state", nil, map[string]string{"Referer": bad, "Origin": "https://" + p.host}); r.status != http.StatusForbidden {
		t.Fatalf("forged preview read: %d %s", r.status, r.body)
	}

	// Showcase: hidden while protected.
	if _, err := p.database.Exec(`UPDATE sites SET visibility = 'public' WHERE id = $1`, p.siteID); err != nil {
		t.Fatal(err)
	}
	if r := p.at(t, "GET", p.person, "/", nil, nil); strings.Contains(string(r.body), "trip."+p.person) {
		t.Fatalf("showcase lists a protected site")
	}

	// Take-down and offline win over the passcode.
	if r := p.at(t, "PATCH", pcSiteDomain, "/v1/sites/trip", map[string]any{"offline": true}, p.okey); r.status != 200 {
		t.Fatalf("offline: %d", r.status)
	}
	if r := p.at(t, "GET", p.host, "/", nil, nil); r.status != http.StatusServiceUnavailable {
		t.Fatalf("offline + passcode: %d", r.status)
	}
	p.at(t, "PATCH", pcSiteDomain, "/v1/sites/trip", map[string]any{"offline": false}, p.okey)

	// Journal hygiene: no passcode, cookie or canary in the log.
	for _, secret := range []string{code, "Canary-Pass-6160", strings.SplitN(cookie, "=", 2)[1]} {
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("the log holds a secret %q", secret)
		}
	}
	if !strings.Contains(logs.String(), "passcode_unlock ok=true") || !strings.Contains(logs.String(), "passcode_unlock ok=false") {
		t.Fatalf("unlocks not logged: %s", logs.String())
	}
}

// Claimed names, custom domains (through nginx's marker rewrite), and the
// person path while a certificate is pending.
func TestPasscodeOtherAddresses(t *testing.T) {
	p := newPasscodeApp(t)
	const code = "domain-pass-1"
	p.lock(t, code)
	ctx := context.Background()

	for _, domain := range []string{"goa-trip." + pcSiteDomain, "trip.example.org"} {
		if _, err := p.database.ExecContext(ctx, `UPDATE sites SET custom_domain = $2, domain_status = 'active', domain_verified_at = now() WHERE id = $1`, p.siteID, domain); err != nil {
			t.Fatal(err)
		}
		get := func(path string, h map[string]string) resp {
			if strings.HasSuffix(domain, pcSiteDomain) {
				return p.at(t, "GET", domain, path, nil, h) // Go serves claimed names
			}
			return p.at(t, "GET", domain, "/internal/passcode"+path, nil, h) // nginx's rewrite
		}
		for _, path := range []string{"/", "/app.js", "/data.json", "/days/"} {
			r := get(path, nil)
			if r.status != 401 || strings.Contains(string(r.body), passcodeCanary) {
				t.Fatalf("%s%s: %d %s", domain, path, r.status, r.body)
			}
		}
		// The site host now redirects to the domain; nothing leaks there either.
		if r := p.at(t, "GET", p.host, "/app.js", nil, nil); strings.Contains(string(r.body), passcodeCanary) {
			t.Fatal("site host leaked with a domain")
		}
		r := p.unlock(t, domain, code, "/app.js", nil)
		if r.status != http.StatusSeeOther {
			t.Fatalf("unlock on %s: %d %s", domain, r.status, r.body)
		}
		ck := map[string]string{"Cookie": cookieHeader(t, r)}
		if r := get("/app.js", ck); r.status != 200 || !strings.Contains(string(r.body), passcodeCanary) {
			t.Fatalf("%s unlocked: %d %s", domain, r.status, r.body)
		}
		if r := get("/days", ck); r.status != http.StatusMovedPermanently || r.header.Get("Location") != "/days/" {
			t.Fatalf("%s directory redirect: %d %s", domain, r.status, r.header.Get("Location"))
		}
		if r := p.at(t, "GET", domain, "/v1/sites/trip/state", nil, map[string]string{"Cookie": ck["Cookie"], "Origin": "https://" + domain}); r.status != 200 {
			t.Fatalf("%s state: %d %s", domain, r.status, r.body)
		}
	}
	if _, err := p.database.ExecContext(ctx, `UPDATE sites SET custom_domain = NULL, domain_status = NULL, domain_verified_at = NULL WHERE id = $1`, p.siteID); err != nil {
		t.Fatal(err)
	}
	// An unknown host with the marker (a hand-made vhost): closed, no form.
	if r := p.at(t, "GET", "unknown.example.net", "/internal/passcode/app.js", nil, nil); r.status != 401 || strings.Contains(string(r.body), "<form") {
		t.Fatalf("unknown host: %d %s", r.status, r.body)
	}

	// A person whose certificate is not ready: the person path asks, with a
	// per-site cookie, and the unlock there opens only this site.
	oscar := p.newPerson(t, "oscar")
	for _, s := range []string{"camp", "blog"} {
		r := p.at(t, "POST", pcSiteDomain, "/v1/sites/"+s+"/files", map[string]any{"files": map[string]string{"index.html": s + " " + passcodeCanary}}, map[string]string{"X-API-Key": oscar.key})
		if r.status != http.StatusCreated {
			t.Fatal(r.status)
		}
	}
	_, sh := p.userID(t, oscar)
	ph := sh + "." + pcSiteDomain
	for _, s := range []string{"camp", "blog"} {
		if r := p.at(t, "PUT", pcSiteDomain, "/v1/sites/"+s+"/lock", map[string]any{"passcode": "oscar-" + s}, map[string]string{"X-API-Key": oscar.key}); r.status != 200 {
			t.Fatalf("lock %s: %d %s", s, r.status, r.body)
		}
	}
	if r := p.at(t, "GET", ph, "/camp/", nil, nil); r.status != 401 || strings.Contains(string(r.body), passcodeCanary) {
		t.Fatalf("person path: %d %s", r.status, r.body)
	}
	r := p.unlock(t, ph, "oscar-camp", "/camp/", nil)
	if r.status != http.StatusSeeOther {
		t.Fatalf("person path unlock: %d %s", r.status, r.body)
	}
	ck := map[string]string{"Cookie": cookieHeader(t, r)}
	if r := p.at(t, "GET", ph, "/camp/", nil, ck); r.status != 200 {
		t.Fatalf("person path unlocked: %d", r.status)
	}
	if r := p.at(t, "GET", ph, "/blog/", nil, ck); r.status != 401 {
		t.Fatalf("camp's cookie opened blog: %d", r.status)
	}
	// camp's passcode does not open blog either.
	if r := p.unlock(t, ph, "oscar-camp", "/blog/", nil); r.status != 401 {
		t.Fatalf("camp's passcode on blog: %d", r.status)
	}
}

func TestPasscodeBruteForce(t *testing.T) {
	p := newPasscodeApp(t)
	const code = "right-one-1"
	p.lock(t, code)
	withPasscodeLimits(t, func(l *config.Limits) {
		l.RatePasscodeIP = config.Rate{Burst: 3, Every: time.Hour}
		l.RatePasscodeSite = config.Rate{Burst: 5, Every: time.Hour}
		l.PasscodeLockout = 15 * time.Minute
		l.PasscodeSiteLockout = 15 * time.Minute
	})
	ip := func(n string) map[string]string { return map[string]string{"X-Forwarded-For": "203.0.113." + n} }
	for i := 0; i < 2; i++ {
		if r := p.unlock(t, p.host, "wrong", "/", ip("1")); r.status != 401 {
			t.Fatalf("try %d: %d", i, r.status)
		}
	}
	if r := p.unlock(t, p.host, "wrong", "/", ip("1")); r.status != http.StatusTooManyRequests || r.header.Get("Retry-After") == "" {
		t.Fatalf("third wrong: %d", r.status)
	}
	// Locked out: even the right passcode is refused from that address.
	if r := p.unlock(t, p.host, code, "/", ip("1")); r.status != http.StatusTooManyRequests || len(r.header.Values("Set-Cookie")) != 0 {
		t.Fatalf("right passcode while locked out: %d", r.status)
	}
	// Another address still gets in.
	if r := p.unlock(t, p.host, code, "/", ip("2")); r.status != http.StatusSeeOther {
		t.Fatalf("other address: %d", r.status)
	}
	// The site as a whole: 5 wrong tries from anywhere lock everyone out.
	p.unlock(t, p.host, "wrong", "/", ip("3"))
	if r := p.unlock(t, p.host, "wrong", "/", ip("4")); r.status != http.StatusTooManyRequests {
		t.Fatalf("site limit: %d", r.status)
	}
	if r := p.unlock(t, p.host, code, "/", ip("5")); r.status != http.StatusTooManyRequests {
		t.Fatalf("right passcode during a site lockout: %d", r.status)
	}
	// ... and the clock moves on.
	later := time.Now().Add(16 * time.Minute)
	p.sites.passcode.now = func() time.Time { return later }
	if r := p.unlock(t, p.host, code, "/", ip("5")); r.status != http.StatusSeeOther {
		t.Fatalf("after the lockout: %d", r.status)
	}
}

// The marker follows the database at boot, and across rename, delete and
// restore.
func TestPasscodeMarkerLifecycle(t *testing.T) {
	p := newPasscodeApp(t)
	p.lock(t, "marker-pass")
	marker := filepath.Join(p.pageDir, "passcode")
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	p.sites.SyncSuspendMarkers(context.Background())
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("boot sync did not restore the marker: %v", err)
	}
	// Rename: the setting (and marker) move with the site.
	if r := p.at(t, "PATCH", pcSiteDomain, "/v1/sites/trip", map[string]any{"name": "voyage"}, p.okey); r.status != 200 {
		t.Fatalf("rename: %d %s", r.status, r.body)
	}
	uid, _ := p.userID(t, p.olive)
	if !p.sites.disk.HasPasscodeMarker(uid, "voyage") {
		t.Fatal("marker did not move with the rename")
	}
	if r := p.at(t, "GET", "voyage."+p.person, "/", nil, nil); r.status != 401 {
		t.Fatalf("renamed site: %d", r.status)
	}
	// Delete and restore keep it.
	if r := p.at(t, "DELETE", pcSiteDomain, "/v1/sites/voyage", nil, p.okey); r.status != 200 && r.status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.status, r.body)
	}
	if r := p.at(t, "POST", pcSiteDomain, "/v1/sites/voyage/restore", nil, p.okey); r.status != 200 {
		t.Fatalf("restore: %d %s", r.status, r.body)
	}
	if r := p.at(t, "GET", "voyage."+p.person, "/", nil, nil); r.status != 401 {
		t.Fatalf("restored site: %d", r.status)
	}
	// A marker the database does not back (a crash mid-unlock) is removed at boot.
	if _, err := p.database.Exec(`UPDATE sites SET passcode_enc = NULL WHERE user_id = $1 AND name = 'voyage'`, uid); err != nil {
		t.Fatal(err)
	}
	if r := p.at(t, "GET", "voyage."+p.person, "/", nil, nil); r.status != 200 {
		t.Fatalf("database says open: %d", r.status)
	}
	p.sites.SyncSuspendMarkers(context.Background())
	if p.sites.disk.HasPasscodeMarker(uid, "voyage") {
		t.Fatal("stale marker kept")
	}
	_ = db.Site{}
}

// Person hosts in serve mode: the content host still serves sites from
// disk, so a protected site's file there 302s to its own address instead.
func TestPasscodeContentHostServeMode(t *testing.T) {
	a := newPersonApp(t, "serve")
	_ = a.sites.SetPasscodeKey(testPasscodeKey)
	withPasscodeLimits(t, func(l *config.Limits) { l.SitePasscodes = true; l.PasscodeMinLength = 6 })
	olive := a.newPerson(t, "olive")
	if r := a.at(t, "POST", pcSiteDomain, "/v1/sites/trip/files", map[string]any{"files": map[string]string{"index.html": passcodeCanary, "a.js": passcodeCanary}}, map[string]string{"X-API-Key": olive.key}); r.status != http.StatusCreated {
		t.Fatal(r.status)
	}
	_, h := a.userID(t, olive)
	if r := a.at(t, "GET", pcContentHost, "/internal/site-redirect/"+h+"/trip/a.js", nil, nil); r.status != 200 {
		t.Fatalf("before the lock the content host serves: %d", r.status)
	}
	if r := a.at(t, "PUT", pcSiteDomain, "/v1/sites/trip/lock", map[string]any{"passcode": "serve-mode"}, map[string]string{"X-API-Key": olive.key}); r.status != 200 {
		t.Fatalf("lock: %d %s", r.status, r.body)
	}
	for _, path := range []string{"/internal/site-redirect/" + h + "/trip/a.js?x=1", "/internal/passcode/" + h + "/trip/a.js?x=1"} {
		r := a.at(t, "GET", pcContentHost, path, nil, nil)
		if r.status != http.StatusFound || r.header.Get("Location") != "https://"+h+"."+pcSiteDomain+"/trip/a.js?x=1" || strings.Contains(string(r.body), passcodeCanary) {
			t.Fatalf("%s: %d %s %s", path, r.status, r.header.Get("Location"), r.body)
		}
	}
	if r := a.at(t, "GET", h+"."+pcSiteDomain, "/trip/a.js", nil, nil); r.status != 401 {
		t.Fatalf("person path: %d", r.status)
	}
}

// Visitor sign-in on a site with a passcode needs the unlock first.
func TestPasscodeVisitorSignIn(t *testing.T) {
	p := newPasscodeApp(t)
	p.withOAuth(t)
	p.lock(t, "signin-pass")
	start := "/v1/visitor/oauth/google?return_to=" + url.QueryEscape("https://"+p.host+"/")
	if r := p.at(t, "GET", p.host, start, nil, nil); r.status != http.StatusForbidden || r.json(t)["code"] != "site_locked" {
		t.Fatalf("sign-in start without the unlock: %d %s", r.status, r.body)
	}
	if r := p.at(t, "POST", p.host, "/v1/sites/trip/visitor/auth", map[string]any{"email": "a@example.test"}, map[string]string{"Origin": "https://" + p.host}); r.status != http.StatusForbidden {
		t.Fatalf("email sign-in without the unlock: %d %s", r.status, r.body)
	}
	ck := cookieHeader(t, p.unlock(t, p.host, "signin-pass", "/", nil))
	if r := p.at(t, "GET", p.host, start, nil, map[string]string{"Cookie": ck}); r.status != http.StatusFound {
		t.Fatalf("sign-in start after the unlock: %d %s", r.status, r.body)
	}
}
