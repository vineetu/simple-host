package accesstest

import (
	"bytes"
	"context"
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

	"github.com/vsriram/simple-host/host/config"
	model "github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/server/servertest"
)

// A disk-served address (custom domain, family) reaches the gate through
// nginx's rewrite to /internal/passcode$uri: the form's next and the unlock's
// redirect are the page the visitor asked for, never the rewrite (which is
// a 404 from outside).
func TestPasscodeGateNextThroughRewrite(t *testing.T) {
	p := servertest.NewPasscodeApp(t)
	const code = "domain-pass-1"
	p.Lock(t, code)
	const domain = "trip.example.org"
	if _, err := p.Database.ExecContext(context.Background(), `UPDATE sites SET custom_domain = $2, domain_status = 'active', domain_verified_at = now() WHERE id = $1`, p.SiteID, domain); err != nil {
		t.Fatal(err)
	}
	r := p.At(t, "GET", domain, "/internal/passcode/days/?d=2", nil, nil)
	if r.Status != http.StatusUnauthorized || !strings.Contains(string(r.Body), `name="next" value="/days/?d=2"`) || strings.Contains(string(r.Body), "/internal/") {
		t.Fatalf("gate next through the rewrite: %d %s", r.Status, r.Body)
	}
	if r := p.At(t, "GET", domain, "/internal/passcode/robots.txt", nil, nil); r.Status != 200 || !strings.Contains(string(r.Body), "Disallow: /") {
		t.Fatalf("robots.txt through the rewrite: %d %s", r.Status, r.Body)
	}
	for next, want := range map[string]string{
		"/internal/passcode/days/?d=2": "/days/?d=2",
		"/internal/offline":            "/",
		"/internal/passcode//evil.com": "/",
	} {
		r := p.Unlock(t, domain, code, next, nil)
		if r.Status != http.StatusSeeOther || r.Header.Get("Location") != want {
			t.Fatalf("unlock next %q: %d %q want %q", next, r.Status, r.Header.Get("Location"), want)
		}
	}
}

func TestPasscodeOwnerAPI(t *testing.T) {
	p := servertest.NewPasscodeApp(t)

	// Off: refused with a clear 409, nothing changes.
	servertest.WithPasscodeLimits(t, func(l *config.Limits) { l.SitePasscodes = false }, &p.Sites.Limits, &p.Access.Limits, &p.Data.Limits)
	if r := p.Lock(t, "123456"); r.Status != http.StatusConflict || r.JSON(t)["code"] != "passcodes_not_enabled" {
		t.Fatalf("off: %d %s", r.Status, r.Body)
	}
	if _, err := os.Stat(filepath.Join(p.PageDir, "passcode")); err == nil {
		t.Fatal("off: marker written")
	}
	servertest.WithPasscodeLimits(t, func(l *config.Limits) { l.SitePasscodes = true }, &p.Sites.Limits, &p.Access.Limits, &p.Data.Limits)

	if r := p.Lock(t, "12345"); r.Status != http.StatusBadRequest || r.JSON(t)["code"] != "invalid_passcode" {
		t.Fatalf("short: %d %s", r.Status, r.Body)
	}
	if r := p.At(t, "PUT", servertest.PcSiteDomain, "/v1/sites/trip/lock", map[string]any{}, p.Okey); r.Status != http.StatusBadRequest {
		t.Fatalf("empty body: %d", r.Status)
	}
	r := p.Lock(t, "  goa-2026  ")
	if r.Status != 200 || r.JSON(t)["passcode"] != "goa-2026" || r.JSON(t)["passcode_protected"] != true {
		t.Fatalf("set: %d %s", r.Status, r.Body)
	}
	if _, err := os.Stat(filepath.Join(p.PageDir, "passcode")); err != nil {
		t.Fatalf("marker: %v", err)
	}
	var enc []byte
	var gen int
	if err := p.Database.QueryRow(`SELECT passcode_enc, passcode_generation FROM sites WHERE id = $1`, p.SiteID).Scan(&enc, &gen); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(enc, []byte("goa-2026")) {
		t.Fatal("passcode stored in the clear")
	}
	var legacy *string
	_ = p.Database.QueryRow(`SELECT view_password_hash FROM sites WHERE id = $1`, p.SiteID).Scan(&legacy)
	if legacy != nil {
		t.Fatal("view_password_hash written")
	}
	// Read back: the owner only.
	if r := p.At(t, "GET", servertest.PcSiteDomain, "/v1/sites/trip/lock", nil, p.Okey); r.Status != 200 || r.JSON(t)["passcode"] != "goa-2026" || r.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("read: %d %s", r.Status, r.Body)
	}
	other := p.NewPerson(t, "oscar")
	if r := p.At(t, "GET", servertest.PcSiteDomain, "/v1/sites/trip/lock", nil, map[string]string{"X-API-Key": other.Key}); r.Status != http.StatusNotFound {
		t.Fatalf("another account read: %d %s", r.Status, r.Body)
	}
	if r := p.At(t, "GET", servertest.PcSiteDomain, "/v1/sites/trip/lock", nil, map[string]string{"X-API-Key": p.Admin}); r.Status != http.StatusNotFound || strings.Contains(string(r.Body), "goa-2026") {
		t.Fatalf("admin read: %d %s", r.Status, r.Body)
	}
	// The site list says protected (never the passcode).
	if r := p.At(t, "GET", servertest.PcSiteDomain, "/v1/sites", nil, p.Okey); !strings.Contains(string(r.Body), `"passcode_protected":true`) || strings.Contains(string(r.Body), "goa-2026") {
		t.Fatalf("site list: %s", r.Body)
	}
	// Same passcode again: nothing changes (no sign-out).
	p.Lock(t, "goa-2026")
	var gen2 int
	_ = p.Database.QueryRow(`SELECT passcode_generation FROM sites WHERE id = $1`, p.SiteID).Scan(&gen2)
	if gen2 != gen {
		t.Fatalf("same passcode bumped the generation %d -> %d", gen, gen2)
	}
	// Generate: six digits.
	if r := p.At(t, "PUT", servertest.PcSiteDomain, "/v1/sites/trip/lock", map[string]any{"generate": true}, p.Okey); r.Status != 200 || !regexp.MustCompile(`^[0-9]{6}$`).MatchString(r.JSON(t)["passcode"].(string)) {
		t.Fatalf("generate: %d %s", r.Status, r.Body)
	}
	if r := p.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/trip/lock/sign-out-everyone", nil, p.Okey); r.Status != 200 {
		t.Fatalf("sign out everyone: %d %s", r.Status, r.Body)
	}
	if r := p.At(t, "DELETE", servertest.PcSiteDomain, "/v1/sites/trip/lock", nil, p.Okey); r.Status != 200 || r.JSON(t)["passcode_protected"] != false || !strings.Contains(r.JSON(t)["note"].(string), "is removed") {
		t.Fatalf("remove: %d %s", r.Status, r.Body)
	}
	// Removing again says nothing was there (SIM-210: no false "removed").
	if r := p.At(t, "DELETE", servertest.PcSiteDomain, "/v1/sites/trip/lock", nil, p.Okey); r.Status != 200 || !strings.Contains(r.JSON(t)["note"].(string), "had no passcode") {
		t.Fatalf("remove again: %d %s", r.Status, r.Body)
	}
	if _, err := os.Stat(filepath.Join(p.PageDir, "passcode")); err == nil {
		t.Fatal("marker left after remove")
	}
	if r := p.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/trip/lock/sign-out-everyone", nil, p.Okey); r.Status != http.StatusConflict {
		t.Fatalf("sign out without a passcode: %d", r.Status)
	}
	if r := p.At(t, "GET", p.Host, "/", nil, nil); r.Status != 200 {
		t.Fatalf("open again: %d", r.Status)
	}

	// Admin moderation: remove through the admin route (logged), and a
	// preview link without the passcode.
	p.Lock(t, "goa-2026")
	if r := p.At(t, "POST", servertest.PcSiteDomain, "/v1/admin/sites/"+p.SiteID+"/versions/1/preview-link", nil, map[string]string{"X-API-Key": p.Admin}); r.Status != 200 || !strings.Contains(r.JSON(t)["url"].(string), "/__preview/1/") {
		t.Fatalf("admin preview link: %d %s", r.Status, r.Body)
	}
	if r := p.At(t, "DELETE", servertest.PcSiteDomain, "/v1/admin/sites/"+p.SiteID+"/lock", nil, map[string]string{"X-API-Key": p.Admin}); r.Status != 200 {
		t.Fatalf("admin remove: %d %s", r.Status, r.Body)
	}
	if r := p.At(t, "DELETE", servertest.PcSiteDomain, "/v1/admin/sites/"+p.SiteID+"/lock", nil, p.Okey); r.Status != http.StatusNotFound && r.Status != http.StatusForbidden {
		t.Fatalf("owner on the admin route: %d", r.Status)
	}
}

// A shared-origin server (no person or site hosts) refuses a passcode.
func TestPasscodeNeedsOwnAddress(t *testing.T) {
	a := servertest.NewPersonApp(t, "off")
	_ = a.Access.SetPasscodeKey(servertest.TestPasscodeKey)
	servertest.WithPasscodeLimits(t, func(l *config.Limits) { l.SitePasscodes = true }, &a.Sites.Limits, &a.Access.Limits, &a.Data.Limits)
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "shop")
	r := a.At(t, "PUT", servertest.PcSiteDomain, "/v1/sites/shop/lock", map[string]any{"passcode": "123456"}, map[string]string{"X-API-Key": olive.Key})
	if r.Status != http.StatusConflict || r.JSON(t)["code"] != "passcode_needs_own_address" {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	// And a server without the key refuses too.
	b := servertest.NewPersonApp(t, "canonical")
	oscar := b.NewPerson(t, "oscar")
	b.Deploy(t, oscar, "shop")
	if r := b.At(t, "PUT", servertest.PcSiteDomain, "/v1/sites/shop/lock", map[string]any{"passcode": "123456"}, map[string]string{"X-API-Key": oscar.Key}); r.Status != http.StatusConflict || r.JSON(t)["code"] != "passcodes_not_enabled" {
		t.Fatalf("no key: %d %s", r.Status, r.Body)
	}
}

// The canary sweep: with a passcode on, no address of the site shows any
// of its files to a visitor without the unlock, a link-preview bot
// included; the unlock opens exactly this site on exactly this host.
func TestPasscodeCanarySweep(t *testing.T) {
	p := servertest.NewPasscodeApp(t)
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	const code = "Canary-Pass-5150"
	if r := p.Lock(t, code); r.Status != 200 {
		t.Fatalf("lock: %d %s", r.Status, r.Body)
	}
	// A claimed free name and a custom domain for the same site are tested
	// in their own sites below; here: site host, person path, legacy.
	paths := []string{"/", "/index.html", "/app.js", "/data.json", "/img.svg", "/days/", "/days", "/nope", "/trip/index.html", "/" + p.Handle + "/trip/app.js"}
	uas := []string{"", "Slackbot-LinkExpanding 1.0 (+https://api.slack.com/robots)", "facebookexternalhit/1.1", "Twitterbot/1.0"}
	noLeak := func(what string, r servertest.Resp) {
		t.Helper()
		if strings.Contains(string(r.Body), servertest.PasscodeCanary) {
			t.Fatalf("%s leaked the canary: %d %s", what, r.Status, r.Body)
		}
		if r.Status == 200 && !strings.HasPrefix(string(r.Body), "User-agent") {
			t.Fatalf("%s answered 200: %s", what, r.Body)
		}
	}
	for _, path := range paths {
		for _, ua := range uas {
			h := map[string]string{"User-Agent": ua}
			r := p.At(t, "GET", p.Host, path, nil, h)
			noLeak("site host "+path, r)
			if r.Status != http.StatusUnauthorized {
				t.Fatalf("site host %s: %d (want the gate, before any redirect)", path, r.Status)
			}
			csp := r.Header.Get("Content-Security-Policy")
			if r.Header.Get("X-Robots-Tag") == "" || r.Header.Get("Cache-Control") != "no-store" || !strings.Contains(csp, "default-src 'none'") || strings.Contains(csp, "unsafe-inline") || !strings.Contains(csp, "frame-ancestors 'none'") || !strings.Contains(csp, "form-action 'self'") {
				t.Fatalf("gate headers: %v", r.Header)
			}
			if !strings.Contains(string(r.Body), "Enter the passcode") || (!strings.Contains(path, p.Handle) && strings.Contains(strings.ReplaceAll(string(r.Body), p.Host, ""), p.Handle)) || strings.Contains(string(r.Body), "og:image") {
				t.Fatalf("gate body: %s", r.Body)
			}
			noLeak("HEAD "+path, p.At(t, "HEAD", p.Host, path, nil, h))
			noLeak("person path "+path, p.At(t, "GET", p.Person, "/trip"+path, nil, h))
			noLeak("legacy "+path, p.At(t, "GET", servertest.PcContentHost, "/internal/site-redirect/"+p.Handle+"/trip"+path, nil, h))
			noLeak("legacy marker "+path, p.At(t, "GET", servertest.PcContentHost, "/internal/passcode/"+p.Handle+"/trip"+path, nil, h))
		}
	}
	// Scripts and images from any page get a plain 401, never the page.
	r := p.At(t, "GET", p.Host, "/app.js", nil, map[string]string{"Sec-Fetch-Dest": "script", "Sec-Fetch-Site": "same-site", "Sec-Fetch-Mode": "no-cors"})
	if r.Status != 401 || strings.Contains(string(r.Body), "<form") || !strings.Contains(string(r.Body), "This site is protected") {
		t.Fatalf("script request: %d %s", r.Status, r.Body)
	}
	// robots.txt: Disallow, not the site's own.
	if r := p.At(t, "GET", p.Host, "/robots.txt", nil, nil); r.Status != 200 || !strings.Contains(string(r.Body), "Disallow: /") || strings.Contains(string(r.Body), servertest.PasscodeCanary) {
		t.Fatalf("robots.txt: %d %s", r.Status, r.Body)
	}
	// Data: no reading without the unlock, on any host, with or without Origin.
	for _, host := range []string{servertest.PcSiteDomain, p.Host, p.Person, servertest.PcContentHost} {
		for _, path := range []string{servertest.PasscodeData, "/v1/sites/trip/me"} {
			r := p.At(t, "GET", host, path, nil, nil)
			if r.Status == 200 {
				t.Fatalf("data %s%s without unlock: %d %s", host, path, r.Status, r.Body)
			}
		}
	}
	if r := p.At(t, "GET", p.Host, servertest.PasscodeData, nil, nil); r.Status != http.StatusForbidden || r.JSON(t)["code"] != "site_locked" {
		t.Fatalf("apex data read: %d %s", r.Status, r.Body)
	}
	if r := p.At(t, "GET", servertest.PcSiteDomain, servertest.PasscodeData, nil, p.Okey); r.Status != 200 {
		t.Fatalf("owner key data read: %d %s", r.Status, r.Body)
	}
	if r := p.At(t, "GET", servertest.PcSiteDomain, servertest.PasscodeData, nil, map[string]string{"X-API-Key": p.Admin}); r.Status != 200 {
		t.Fatalf("admin key data read: %d %s", r.Status, r.Body)
	}
	// Another account with its own site of the same name, and its own key:
	// the bare name still means olive's (older) site for some routes, so
	// that key must not open it.
	mallory := p.NewPerson(t, "mallory")
	if r := p.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/trip/files", map[string]any{"files": map[string]string{"index.html": "m"}}, map[string]string{"X-API-Key": mallory.Key}); r.Status != http.StatusCreated {
		t.Fatalf("mallory deploy: %d", r.Status)
	}
	for _, path := range []string{servertest.PasscodeData} {
		r := p.At(t, "GET", servertest.PcSiteDomain, path, nil, map[string]string{"X-API-Key": mallory.Key})
		if strings.Contains(string(r.Body), servertest.PasscodeCanary) {
			t.Fatalf("another account's key read %s: %d %s", path, r.Status, r.Body)
		}
	}
	// The admin's key through the passcode gate is logged.
	p.At(t, "GET", servertest.PcSiteDomain, "/v1/sites/trip/push/vapid-key", nil, map[string]string{"X-API-Key": p.Admin})
	if !strings.Contains(logs.String(), "site_gate_admin_bypass") {
		t.Fatal("admin bypass not logged")
	}
	if r := p.At(t, "OPTIONS", p.Host, servertest.PasscodeData, nil, map[string]string{"Origin": "https://" + p.Host, "Access-Control-Request-Method": "PUT"}); r.Status == http.StatusForbidden && strings.Contains(string(r.Body), "site_locked") {
		t.Fatal("preflight gated")
	}

	// ---- unlocking ----------------------------------------------------------
	if r := p.Unlock(t, p.Host, code, "/app.js", map[string]string{"Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site"}); r.Status != http.StatusForbidden || len(r.Header.Values("Set-Cookie")) != 0 {
		t.Fatalf("cross-origin unlock: %d", r.Status)
	}
	if r := p.Unlock(t, p.Host, "wrong-one", "/app.js", nil); r.Status != 401 || !strings.Contains(string(r.Body), "isn’t right") || len(r.Header.Values("Set-Cookie")) != 0 {
		t.Fatalf("wrong passcode: %d %s", r.Status, r.Body)
	}
	if r := p.Unlock(t, servertest.PcContentHost, code, "/", nil); r.Status != http.StatusSeeOther || len(r.Header.Values("Set-Cookie")) != 0 {
		t.Fatalf("unlock on the content host: %d %v", r.Status, r.Header)
	}
	if r := p.Unlock(t, p.Host, code, "//evil.example/x", nil); r.Status != http.StatusSeeOther || r.Header.Get("Location") != "/" {
		t.Fatalf("open redirect: %d %s", r.Status, r.Header.Get("Location"))
	}
	r = p.Unlock(t, p.Host, "  "+code+" ", "/app.js?v=2", nil)
	if r.Status != http.StatusSeeOther || r.Header.Get("Location") != "/app.js?v=2" {
		t.Fatalf("unlock: %d %s %s", r.Status, r.Header.Get("Location"), r.Body)
	}
	cookie := servertest.CookieHeader(t, r)
	withCookie := map[string]string{"Cookie": cookie}
	for _, path := range []string{"/", "/app.js", "/data.json", "/img.svg", "/days/"} {
		r := p.At(t, "GET", p.Host, path, nil, withCookie)
		if r.Status != 200 || !strings.Contains(string(r.Body), servertest.PasscodeCanary) {
			t.Fatalf("unlocked %s: %d %s", path, r.Status, r.Body)
		}
		if r.Header.Get("Cache-Control") != "private, no-cache" || r.Header.Get("Cross-Origin-Resource-Policy") != "same-origin" || !strings.Contains(strings.Join(r.Header.Values("Vary"), ","), "Cookie") {
			t.Fatalf("unlocked headers: %v", r.Header)
		}
	}
	if r := p.At(t, "GET", p.Host, "/nope", nil, withCookie); r.Status != 404 {
		t.Fatalf("unlocked 404: %d", r.Status)
	}
	if r := p.At(t, "GET", p.Host, "/app.js", nil, map[string]string{"Cookie": cookie, "Range": "bytes=0-3"}); r.Status != http.StatusPartialContent {
		t.Fatalf("range: %d", r.Status)
	}
	// XSSI: a sibling page cannot pull the site's script in, cookie or not.
	if r := p.At(t, "GET", p.Host, "/app.js", nil, map[string]string{"Cookie": cookie, "Sec-Fetch-Site": "same-site", "Sec-Fetch-Mode": "no-cors", "Sec-Fetch-Dest": "script"}); r.Status != 401 || strings.Contains(string(r.Body), servertest.PasscodeCanary) {
		t.Fatalf("cross-site script with cookie: %d %s", r.Status, r.Body)
	}
	// A link clicked elsewhere (a top-level navigation) works.
	if r := p.At(t, "GET", p.Host, "/", nil, map[string]string{"Cookie": cookie, "Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document"}); r.Status != 200 {
		t.Fatalf("navigation with cookie: %d", r.Status)
	}
	// The cookie opens nothing on another host.
	if r := p.At(t, "GET", p.Person, "/trip/", nil, withCookie); strings.Contains(string(r.Body), servertest.PasscodeCanary) {
		t.Fatal("cookie worked on the person host")
	}
	// Data with the cookie, from the site's own page.
	page := map[string]string{"Cookie": cookie, "Origin": "https://" + p.Host}
	if r := p.At(t, "GET", p.Host, servertest.PasscodeData, nil, page); r.Status != 200 {
		t.Fatalf("data with cookie: %d %s", r.Status, r.Body)
	}

	// Change the passcode: everyone is out again.
	if r := p.Lock(t, "Canary-Pass-6160"); r.Status != 200 {
		t.Fatalf("change: %d", r.Status)
	}
	if r := p.At(t, "GET", p.Host, "/", nil, withCookie); r.Status != 401 {
		t.Fatalf("old cookie after change: %d", r.Status)
	}
	r = p.Unlock(t, p.Host, "Canary-Pass-6160", "/", nil)
	cookie = servertest.CookieHeader(t, r)
	if r := p.At(t, "GET", p.Host, "/", nil, map[string]string{"Cookie": cookie}); r.Status != 200 {
		t.Fatalf("new cookie: %d", r.Status)
	}
	p.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/trip/lock/sign-out-everyone", nil, p.Okey)
	if r := p.At(t, "GET", p.Host, "/", nil, map[string]string{"Cookie": cookie}); r.Status != 401 {
		t.Fatalf("cookie after sign-out-everyone: %d", r.Status)
	}

	// Previews bypass it: the owner's preview link, and reads from it.
	r = p.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/trip/versions/1/preview-link", nil, p.Okey)
	link, _ := url.Parse(r.JSON(t)["url"].(string))
	if r := p.At(t, "GET", link.Host, link.Path+"app.js", nil, nil); r.Status != 200 || !strings.Contains(string(r.Body), servertest.PasscodeCanary) {
		t.Fatalf("preview: %d %s", r.Status, r.Body)
	}
	if r := p.At(t, "GET", p.Host, servertest.PasscodeData, nil, map[string]string{"Referer": link.String(), "Origin": "https://" + p.Host}); r.Status != 200 {
		t.Fatalf("preview read: %d %s", r.Status, r.Body)
	}
	bad := strings.Replace(link.String(), "/__preview/1/", "/__preview/1/x", 1)
	if r := p.At(t, "GET", p.Host, servertest.PasscodeData, nil, map[string]string{"Referer": bad, "Origin": "https://" + p.Host}); r.Status != http.StatusForbidden {
		t.Fatalf("forged preview read: %d %s", r.Status, r.Body)
	}

	// Showcase: hidden while protected.
	if _, err := p.Database.Exec(`UPDATE sites SET visibility = 'public' WHERE id = $1`, p.SiteID); err != nil {
		t.Fatal(err)
	}
	if r := p.At(t, "GET", p.Person, "/", nil, nil); strings.Contains(string(r.Body), "trip."+p.Person) {
		t.Fatalf("showcase lists a protected site")
	}

	// Take-down and offline win over the passcode.
	if r := p.At(t, "PATCH", servertest.PcSiteDomain, "/v1/sites/trip", map[string]any{"offline": true}, p.Okey); r.Status != 200 {
		t.Fatalf("offline: %d", r.Status)
	}
	if r := p.At(t, "GET", p.Host, "/", nil, nil); r.Status != http.StatusServiceUnavailable {
		t.Fatalf("offline + passcode: %d", r.Status)
	}
	p.At(t, "PATCH", servertest.PcSiteDomain, "/v1/sites/trip", map[string]any{"offline": false}, p.Okey)

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

// A custom domain still waiting for its proof serves its site through the
// hand-off, redirect rules and passcode gate included: the proof fetches the
// domain's home page and needs a 2xx, so a closed page would block it forever.
func TestPasscodeUnverifiedBoundDomain(t *testing.T) {
	p := servertest.NewPasscodeApp(t)
	const domain = "waiting.example.org"
	if _, err := p.Database.ExecContext(context.Background(), `UPDATE sites SET custom_domain = $2, domain_status = 'pending', domain_verified_at = NULL WHERE id = $1`, p.SiteID, domain); err != nil {
		t.Fatal(err)
	}
	if r := p.At(t, "PUT", servertest.PcSiteDomain, "/v1/sites/trip/files?keep=1", map[string]any{"files": map[string]string{"_redirects": "/old /app.js 301"}}, p.Okey); r.Status != 200 {
		t.Fatalf("redirect rules: %d %s", r.Status, r.Body)
	}
	if r := p.At(t, "GET", domain, "/internal/passcode/", nil, nil); r.Status != 200 || !strings.Contains(string(r.Body), servertest.PasscodeCanary) {
		t.Fatalf("unverified domain home: %d %s", r.Status, r.Body)
	}
	if r := p.At(t, "GET", domain, "/internal/passcode/old", nil, nil); r.Status != http.StatusMovedPermanently || r.Header.Get("Location") != "/app.js" {
		t.Fatalf("unverified domain redirect: %d %v", r.Status, r.Header)
	}
	const code = "waiting-pass-1"
	p.Lock(t, code)
	r := p.At(t, "GET", domain, "/internal/passcode/app.js", nil, nil)
	if r.Status != http.StatusUnauthorized || !strings.Contains(string(r.Body), `name="passcode"`) || strings.Contains(string(r.Body), servertest.PasscodeCanary) {
		t.Fatalf("unverified domain gate: %d %s", r.Status, r.Body)
	}
	if r := p.Unlock(t, domain, code, "/app.js", nil); r.Status != http.StatusSeeOther {
		t.Fatalf("unlock on the unverified domain: %d %s", r.Status, r.Body)
	}
	if r := p.At(t, "GET", "unknown.example.net", "/internal/passcode/app.js", nil, nil); r.Status != http.StatusUnauthorized || strings.Contains(string(r.Body), "<form") {
		t.Fatalf("unknown host: %d %s", r.Status, r.Body)
	}
}

// Claimed names, custom domains (through nginx's marker rewrite), and the
// person path while a certificate is pending.
func TestPasscodeOtherAddresses(t *testing.T) {
	p := servertest.NewPasscodeApp(t)
	const code = "domain-pass-1"
	p.Lock(t, code)
	ctx := context.Background()

	for _, domain := range []string{"goa-trip." + servertest.PcSiteDomain, "trip.example.org"} {
		if _, err := p.Database.ExecContext(ctx, `UPDATE sites SET custom_domain = $2, domain_status = 'active', domain_verified_at = now() WHERE id = $1`, p.SiteID, domain); err != nil {
			t.Fatal(err)
		}
		get := func(path string, h map[string]string) servertest.Resp {
			if strings.HasSuffix(domain, servertest.PcSiteDomain) {
				return p.At(t, "GET", domain, path, nil, h) // Go serves claimed names
			}
			return p.At(t, "GET", domain, "/internal/passcode"+path, nil, h) // nginx's rewrite
		}
		for _, path := range []string{"/", "/app.js", "/data.json", "/days/"} {
			r := get(path, nil)
			if r.Status != 401 || strings.Contains(string(r.Body), servertest.PasscodeCanary) {
				t.Fatalf("%s%s: %d %s", domain, path, r.Status, r.Body)
			}
		}
		// The site host now redirects to the domain; nothing leaks there either.
		if r := p.At(t, "GET", p.Host, "/app.js", nil, nil); strings.Contains(string(r.Body), servertest.PasscodeCanary) {
			t.Fatal("site host leaked with a domain")
		}
		r := p.Unlock(t, domain, code, "/app.js", nil)
		if r.Status != http.StatusSeeOther {
			t.Fatalf("unlock on %s: %d %s", domain, r.Status, r.Body)
		}
		ck := map[string]string{"Cookie": servertest.CookieHeader(t, r)}
		if r := get("/app.js", ck); r.Status != 200 || !strings.Contains(string(r.Body), servertest.PasscodeCanary) {
			t.Fatalf("%s unlocked: %d %s", domain, r.Status, r.Body)
		}
		path := "/%61pp.js/?from=a%2Fb"
		if !strings.HasSuffix(domain, servertest.PcSiteDomain) {
			path = "/internal/passcode" + path
		}
		for _, method := range []string{"GET", "HEAD"} {
			if r := p.At(t, method, domain, path, nil, nil); r.Status != 401 || r.Header.Get("Location") != "" {
				t.Fatalf("%s locked %s: %d %v", domain, method, r.Status, r.Header)
			}
			r := p.At(t, method, domain, path, nil, ck)
			if r.Status != 301 || r.Header.Get("Location") != "/%61pp.js?from=a%2Fb" || r.Header.Get("Cache-Control") != "private, no-cache" || method == "HEAD" && len(r.Body) != 0 {
				t.Fatalf("%s unlocked %s: %d %v", domain, method, r.Status, r.Header)
			}
		}
		if r := get("/days", ck); r.Status != http.StatusMovedPermanently || r.Header.Get("Location") != "/days/" || r.Header.Get("Cache-Control") != "private, no-cache" {
			t.Fatalf("%s directory redirect: %d %s", domain, r.Status, r.Header.Get("Location"))
		}
		if r := p.At(t, "GET", domain, servertest.PasscodeData, nil, map[string]string{"Cookie": ck["Cookie"], "Origin": "https://" + domain}); r.Status != 200 {
			t.Fatalf("%s data: %d %s", domain, r.Status, r.Body)
		}
	}
	if _, err := p.Database.ExecContext(ctx, `UPDATE sites SET custom_domain = NULL, domain_status = NULL, domain_verified_at = NULL WHERE id = $1`, p.SiteID); err != nil {
		t.Fatal(err)
	}
	// An unknown host with the marker (a hand-made vhost): closed, no form.
	if r := p.At(t, "GET", "unknown.example.net", "/internal/passcode/app.js", nil, nil); r.Status != 401 || strings.Contains(string(r.Body), "<form") {
		t.Fatalf("unknown host: %d %s", r.Status, r.Body)
	}

	// A person whose certificate is not ready: the person path asks, with a
	// per-site cookie, and the unlock there opens only this site.
	oscar := p.NewPerson(t, "oscar")
	for _, s := range []string{"camp", "blog"} {
		r := p.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/"+s+"/files", map[string]any{"files": map[string]string{"index.html": s + " " + servertest.PasscodeCanary}}, map[string]string{"X-API-Key": oscar.Key})
		if r.Status != http.StatusCreated {
			t.Fatal(r.Status)
		}
	}
	_, sh := p.UserID(t, oscar)
	ph := sh + "." + servertest.PcSiteDomain
	for _, s := range []string{"camp", "blog"} {
		if r := p.At(t, "PUT", servertest.PcSiteDomain, "/v1/sites/"+s+"/lock", map[string]any{"passcode": "oscar-" + s}, map[string]string{"X-API-Key": oscar.Key}); r.Status != 200 {
			t.Fatalf("lock %s: %d %s", s, r.Status, r.Body)
		}
	}
	if r := p.At(t, "GET", ph, "/camp/", nil, nil); r.Status != 401 || strings.Contains(string(r.Body), servertest.PasscodeCanary) {
		t.Fatalf("person path: %d %s", r.Status, r.Body)
	}
	r := p.Unlock(t, ph, "oscar-camp", "/camp/", nil)
	if r.Status != http.StatusSeeOther {
		t.Fatalf("person path unlock: %d %s", r.Status, r.Body)
	}
	ck := map[string]string{"Cookie": servertest.CookieHeader(t, r)}
	if r := p.At(t, "GET", ph, "/camp/", nil, ck); r.Status != 200 {
		t.Fatalf("person path unlocked: %d", r.Status)
	}
	if r := p.At(t, "GET", ph, "/blog/", nil, ck); r.Status != 401 {
		t.Fatalf("camp's cookie opened blog: %d", r.Status)
	}
	// camp's passcode does not open blog either.
	if r := p.Unlock(t, ph, "oscar-camp", "/blog/", nil); r.Status != 401 {
		t.Fatalf("camp's passcode on blog: %d", r.Status)
	}
}

func TestPasscodeBruteForce(t *testing.T) {
	p := servertest.NewPasscodeApp(t)
	const code = "right-one-1"
	p.Lock(t, code)
	servertest.WithPasscodeLimits(t, func(l *config.Limits) {
		l.RatePasscodeIP = config.Rate{Burst: 3, Every: time.Hour}
		l.RatePasscodeSite = config.Rate{Burst: 5, Every: time.Hour}
		l.PasscodeLockout = 15 * time.Minute
		l.PasscodeSiteLockout = 15 * time.Minute
	}, &p.Access.Passcode.Limits)
	ip := func(n string) map[string]string { return map[string]string{"X-Forwarded-For": "203.0.113." + n} }
	for i := 0; i < 2; i++ {
		if r := p.Unlock(t, p.Host, "wrong", "/", ip("1")); r.Status != 401 {
			t.Fatalf("try %d: %d", i, r.Status)
		}
	}
	if r := p.Unlock(t, p.Host, "wrong", "/", ip("1")); r.Status != http.StatusTooManyRequests || r.Header.Get("Retry-After") == "" {
		t.Fatalf("third wrong: %d", r.Status)
	}
	// Locked out: even the right passcode is refused from that address.
	if r := p.Unlock(t, p.Host, code, "/", ip("1")); r.Status != http.StatusTooManyRequests || len(r.Header.Values("Set-Cookie")) != 0 {
		t.Fatalf("right passcode while locked out: %d", r.Status)
	}
	// Another address still gets in.
	if r := p.Unlock(t, p.Host, code, "/", ip("2")); r.Status != http.StatusSeeOther {
		t.Fatalf("other address: %d", r.Status)
	}
	// The site as a whole: 5 wrong tries from anywhere lock everyone out.
	p.Unlock(t, p.Host, "wrong", "/", ip("3"))
	if r := p.Unlock(t, p.Host, "wrong", "/", ip("4")); r.Status != http.StatusTooManyRequests {
		t.Fatalf("site limit: %d", r.Status)
	}
	if r := p.Unlock(t, p.Host, code, "/", ip("5")); r.Status != http.StatusTooManyRequests {
		t.Fatalf("right passcode during a site lockout: %d", r.Status)
	}
	// ... and the clock moves on.
	later := time.Now().Add(16 * time.Minute)
	p.Access.Passcode.Now = func() time.Time { return later }
	if r := p.Unlock(t, p.Host, code, "/", ip("5")); r.Status != http.StatusSeeOther {
		t.Fatalf("after the lockout: %d", r.Status)
	}
}

// The marker follows the database at boot, and across rename, delete and
// restore.
func TestPasscodeMarkerLifecycle(t *testing.T) {
	p := servertest.NewPasscodeApp(t)
	p.Lock(t, "marker-pass")
	marker := filepath.Join(p.PageDir, "passcode")
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	p.Access.SyncSuspendMarkers(context.Background())
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("boot sync did not restore the marker: %v", err)
	}
	// Rename: the setting (and marker) move with the site.
	if r := p.At(t, "PATCH", servertest.PcSiteDomain, "/v1/sites/trip", map[string]any{"name": "voyage"}, p.Okey); r.Status != 200 {
		t.Fatalf("rename: %d %s", r.Status, r.Body)
	}
	uid, _ := p.UserID(t, p.Olive)
	if !p.Sites.Disk.HasPasscodeMarker(uid, "voyage") {
		t.Fatal("marker did not move with the rename")
	}
	if r := p.At(t, "GET", "voyage."+p.Person, "/", nil, nil); r.Status != 401 {
		t.Fatalf("renamed site: %d", r.Status)
	}
	// Delete and restore keep it.
	if r := p.At(t, "DELETE", servertest.PcSiteDomain, "/v1/sites/voyage", nil, p.Okey); r.Status != 200 && r.Status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.Status, r.Body)
	}
	if r := p.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/voyage/restore", nil, p.Okey); r.Status != 200 {
		t.Fatalf("restore: %d %s", r.Status, r.Body)
	}
	if r := p.At(t, "GET", "voyage."+p.Person, "/", nil, nil); r.Status != 401 {
		t.Fatalf("restored site: %d", r.Status)
	}
	// A marker the database does not back (a crash mid-unlock) is removed at boot.
	if _, err := p.Database.Exec(`UPDATE sites SET passcode_enc = NULL WHERE user_id = $1 AND name = 'voyage'`, uid); err != nil {
		t.Fatal(err)
	}
	if r := p.At(t, "GET", "voyage."+p.Person, "/", nil, nil); r.Status != 200 {
		t.Fatalf("database says open: %d", r.Status)
	}
	p.Access.SyncSuspendMarkers(context.Background())
	if p.Sites.Disk.HasPasscodeMarker(uid, "voyage") {
		t.Fatal("stale marker kept")
	}
	_ = model.Site{}
}

// Person hosts in serve mode: the content host still serves sites from
// disk, so a protected site's file there 302s to its own address instead.
func TestPasscodeContentHostServeMode(t *testing.T) {
	a := servertest.NewPersonApp(t, "serve")
	_ = a.Access.SetPasscodeKey(servertest.TestPasscodeKey)
	servertest.WithPasscodeLimits(t, func(l *config.Limits) { l.SitePasscodes = true; l.PasscodeMinLength = 6 }, &a.Sites.Limits, &a.Access.Limits, &a.Data.Limits)
	olive := a.NewPerson(t, "olive")
	if r := a.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/trip/files", map[string]any{"files": map[string]string{"index.html": servertest.PasscodeCanary, "a.js": servertest.PasscodeCanary}}, map[string]string{"X-API-Key": olive.Key}); r.Status != http.StatusCreated {
		t.Fatal(r.Status)
	}
	_, h := a.UserID(t, olive)
	if r := a.At(t, "GET", servertest.PcContentHost, "/internal/site-redirect/"+h+"/trip/a.js", nil, nil); r.Status != 200 {
		t.Fatalf("before the lock the content host serves: %d", r.Status)
	}
	if r := a.At(t, "PUT", servertest.PcSiteDomain, "/v1/sites/trip/lock", map[string]any{"passcode": "serve-mode"}, map[string]string{"X-API-Key": olive.Key}); r.Status != 200 {
		t.Fatalf("lock: %d %s", r.Status, r.Body)
	}
	for _, path := range []string{"/internal/site-redirect/" + h + "/trip/a.js?x=1", "/internal/passcode/" + h + "/trip/a.js?x=1"} {
		r := a.At(t, "GET", servertest.PcContentHost, path, nil, nil)
		if r.Status != http.StatusFound || r.Header.Get("Location") != "https://"+h+"."+servertest.PcSiteDomain+"/trip/a.js?x=1" || strings.Contains(string(r.Body), servertest.PasscodeCanary) {
			t.Fatalf("%s: %d %s %s", path, r.Status, r.Header.Get("Location"), r.Body)
		}
	}
	if r := a.At(t, "GET", h+"."+servertest.PcSiteDomain, "/trip/a.js", nil, nil); r.Status != 401 {
		t.Fatalf("person path: %d", r.Status)
	}
}

// Visitor sign-in on a site with a passcode needs the unlock first.
func TestPasscodeVisitorSignIn(t *testing.T) {
	p := servertest.NewPasscodeApp(t)
	p.WithOAuth(t)
	p.Lock(t, "signin-pass")
	start := "/v1/visitor/oauth/google?return_to=" + url.QueryEscape("https://"+p.Host+"/")
	if r := p.At(t, "GET", p.Host, start, nil, nil); r.Status != http.StatusForbidden || r.JSON(t)["code"] != "site_locked" {
		t.Fatalf("sign-in start without the unlock: %d %s", r.Status, r.Body)
	}
	if r := p.At(t, "POST", p.Host, "/v1/sites/trip/visitor/auth", map[string]any{"email": "a@example.test"}, map[string]string{"Origin": "https://" + p.Host}); r.Status != http.StatusForbidden {
		t.Fatalf("email sign-in without the unlock: %d %s", r.Status, r.Body)
	}
	ck := servertest.CookieHeader(t, p.Unlock(t, p.Host, "signin-pass", "/", nil))
	if r := p.At(t, "GET", p.Host, start, nil, map[string]string{"Cookie": ck}); r.Status != http.StatusFound {
		t.Fatalf("sign-in start after the unlock: %d %s", r.Status, r.Body)
	}
}

// The site gate's key check is accounts.ActsAsOwner: a member of the team
// that holds a site gets past its passcode with their own key, as the
// named-viewers rule already let them. An expired key on a saved-data route
// is 401 key_expired, not 500. Needs DB_DSN.
func TestPasscodeKeyIsActsAsOwner(t *testing.T) {
	p := servertest.NewPasscodeApp(t)
	team := newTeam(t, p.PrivateApp, p.Olive, "pcteam")
	m := p.NewPerson(t, "pcmember")
	addMember(t, p.PrivateApp, p.Olive, team, m)
	var teamID string
	if err := p.Database.QueryRow(`SELECT id FROM users WHERE handle = $1`, team).Scan(&teamID); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "https://"+p.Host+"/", nil)
	req.Header.Set("X-API-Key", m.Key)
	if !p.Access.OwnerOrAdminKey(req, model.SitePasscodeRow{UserID: teamID, Name: "site"}) {
		t.Fatal("team member's own key refused at the team's gate")
	}
	uid, _ := p.UserID(t, p.Olive)
	if p.Access.OwnerOrAdminKey(req, model.SitePasscodeRow{UserID: uid, Name: "trip"}) {
		t.Fatal("member key passed someone else's gate")
	}
	if _, err := p.Database.Exec(`UPDATE api_keys SET expires_at = now() - interval '1 second' WHERE user_id = $1`, uid); err != nil {
		t.Fatal(err)
	}
	if r := p.At(t, "GET", servertest.PcSiteDomain, servertest.PasscodeData, nil, p.Okey); r.Status != http.StatusUnauthorized || r.JSON(t)["code"] != "key_expired" {
		t.Fatalf("expired key on saved data: %d %s", r.Status, r.Body)
	}
}
