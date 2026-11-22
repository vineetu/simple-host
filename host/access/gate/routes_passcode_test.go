package gate

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	. "github.com/vsriram/simple-host/host/access/featuretest"

	"github.com/vsriram/simple-host/host/config"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
)

func TestPasscodeOwnerAPI(t *testing.T) {
	p := NewPasscodeSite(t)

	// Off: refused, nothing changes.
	p.Access.Limits.SitePasscodes = false
	if r := p.Lock(t, "123456"); r.Status != http.StatusConflict || r.Code() != "passcodes_not_enabled" {
		t.Fatalf("off: %d %s", r.Status, r.Body)
	}
	if _, err := os.Stat(filepath.Join(p.PageDir, "passcode")); err == nil {
		t.Fatal("off: marker written")
	}
	p.Access.Limits.SitePasscodes = true

	if r := p.Lock(t, "12345"); r.Status != http.StatusBadRequest || r.Code() != "invalid_passcode" {
		t.Fatalf("short: %d %s", r.Status, r.Body)
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
	// Read back: the owner only, never the admin or another account.
	if r := p.At(t, "GET", rt.SiteDomain, "/v1/sites/trip/lock", nil, p.Okey); r.Status != 200 || r.JSON(t)["passcode"] != "goa-2026" || r.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("read: %d %s", r.Status, r.Body)
	}
	other := p.NewPerson(t, "oscar")
	if r := p.At(t, "GET", rt.SiteDomain, "/v1/sites/trip/lock", nil, other.Header()); r.Status != http.StatusNotFound {
		t.Fatalf("another account read: %d %s", r.Status, r.Body)
	}
	if r := p.At(t, "GET", rt.SiteDomain, "/v1/sites/trip/lock", nil, map[string]string{"X-API-Key": p.Admin}); r.Status != http.StatusNotFound || strings.Contains(string(r.Body), "goa-2026") {
		t.Fatalf("admin read: %d %s", r.Status, r.Body)
	}
	// Same passcode again: nothing changes (no sign-out).
	p.Lock(t, "goa-2026")
	var gen2 int
	_ = p.Database.QueryRow(`SELECT passcode_generation FROM sites WHERE id = $1`, p.SiteID).Scan(&gen2)
	if gen2 != gen {
		t.Fatalf("same passcode bumped the generation %d -> %d", gen, gen2)
	}
	if r := p.At(t, "PUT", rt.SiteDomain, "/v1/sites/trip/lock", map[string]any{"generate": true}, p.Okey); r.Status != 200 || !regexp.MustCompile(`^[0-9]{6}$`).MatchString(r.JSON(t)["passcode"].(string)) {
		t.Fatalf("generate: %d %s", r.Status, r.Body)
	}
	if r := p.At(t, "DELETE", rt.SiteDomain, "/v1/sites/trip/lock", nil, p.Okey); r.Status != 200 || r.JSON(t)["passcode_protected"] != false {
		t.Fatalf("remove: %d %s", r.Status, r.Body)
	}
	// Removing again says nothing was there (SIM-210: no false "removed").
	if r := p.At(t, "DELETE", rt.SiteDomain, "/v1/sites/trip/lock", nil, p.Okey); r.Status != 200 || !strings.Contains(r.JSON(t)["note"].(string), "had no passcode") {
		t.Fatalf("remove again: %d %s", r.Status, r.Body)
	}
	if _, err := os.Stat(filepath.Join(p.PageDir, "passcode")); err == nil {
		t.Fatal("marker left after remove")
	}
	if r := p.At(t, "POST", rt.SiteDomain, "/v1/sites/trip/lock/sign-out-everyone", nil, p.Okey); r.Status != http.StatusConflict {
		t.Fatalf("sign out without a passcode: %d", r.Status)
	}
	if r := p.At(t, "GET", p.Host, "/", nil, nil); r.Status != 200 {
		t.Fatalf("open again: %d", r.Status)
	}
}

// A shared-origin server (no person or site hosts) refuses a passcode.
func TestPasscodeNeedsOwnAddress(t *testing.T) {
	a := NewApp(t)
	a.EnablePasscodes(t)
	a.Hosts.SetPersonHosts("off")
	a.Hosts.SetSiteHosts("off", "")
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "shop")
	if r := a.At(t, "PUT", rt.SiteDomain, "/v1/sites/shop/lock", map[string]any{"passcode": "123456"}, olive.Header()); r.Status != http.StatusConflict || r.Code() != "passcode_needs_own_address" {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	// And a server without the key refuses too.
	b := NewApp(t)
	oscar := b.NewPerson(t, "oscar")
	b.Deploy(t, oscar, "shop")
	if r := b.At(t, "PUT", rt.SiteDomain, "/v1/sites/shop/lock", map[string]any{"passcode": "123456"}, oscar.Header()); r.Status != http.StatusConflict || r.Code() != "passcodes_not_enabled" {
		t.Fatalf("no key: %d %s", r.Status, r.Body)
	}
}

// The canary sweep: with a passcode on, no file of the site reaches a
// visitor without the unlock (a link-preview bot included); the unlock
// opens exactly this site on this host until the passcode changes.
func TestPasscodeCanarySweep(t *testing.T) {
	p := NewPasscodeSite(t)
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	const code = "Canary-Pass-5150"
	if r := p.Lock(t, code); r.Status != 200 {
		t.Fatalf("lock: %d %s", r.Status, r.Body)
	}
	noLeak := func(what string, r Resp) {
		t.Helper()
		if strings.Contains(string(r.Body), PasscodeCanary) {
			t.Fatalf("%s leaked the canary: %d %s", what, r.Status, r.Body)
		}
	}
	for _, path := range []string{"/", "/app.js", "/data.json", "/img.svg", "/days/", "/days", "/nope"} {
		for _, ua := range []string{"", "Slackbot-LinkExpanding 1.0 (+https://api.slack.com/robots)"} {
			h := map[string]string{"User-Agent": ua}
			r := p.At(t, "GET", p.Host, path, nil, h)
			noLeak("site host "+path, r)
			if r.Status != http.StatusUnauthorized || r.Header.Get("Cache-Control") != "no-store" || r.Header.Get("X-Robots-Tag") == "" {
				t.Fatalf("site host %s: %d %v (want the gate, before any redirect)", path, r.Status, r.Header)
			}
			noLeak("HEAD "+path, p.At(t, "HEAD", p.Host, path, nil, h))
		}
	}
	// A script request gets a plain 401, never the form.
	if r := p.At(t, "GET", p.Host, "/app.js", nil, map[string]string{"Sec-Fetch-Dest": "script", "Sec-Fetch-Site": "same-site", "Sec-Fetch-Mode": "no-cors"}); r.Status != 401 || strings.Contains(string(r.Body), "<form") {
		t.Fatalf("script request: %d %s", r.Status, r.Body)
	}
	if r := p.At(t, "GET", p.Host, "/robots.txt", nil, nil); r.Status != 200 || !strings.Contains(string(r.Body), "Disallow: /") || strings.Contains(string(r.Body), PasscodeCanary) {
		t.Fatalf("robots.txt: %d %s", r.Status, r.Body)
	}
	// The admin's key through the passcode gate is logged.
	p.At(t, "GET", rt.SiteDomain, "/v1/sites/trip/gate-probe", nil, map[string]string{"X-API-Key": p.Admin})
	if !strings.Contains(logs.String(), "site_gate_admin_bypass") {
		t.Fatal("admin bypass not logged")
	}

	// ---- unlocking ----
	if r := p.Unlock(t, p.Host, code, "/app.js", map[string]string{"Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site"}); r.Status != http.StatusForbidden || len(r.Header.Values("Set-Cookie")) != 0 {
		t.Fatalf("cross-origin unlock: %d", r.Status)
	}
	if r := p.Unlock(t, p.Host, "wrong-one", "/app.js", nil); r.Status != 401 || len(r.Header.Values("Set-Cookie")) != 0 {
		t.Fatalf("wrong passcode: %d %s", r.Status, r.Body)
	}
	if r := p.Unlock(t, rt.ContentHost, code, "/", nil); len(r.Header.Values("Set-Cookie")) != 0 {
		t.Fatalf("unlock on the content host: %d %v", r.Status, r.Header)
	}
	// next never leaves the host, nor names nginx's internal rewrite.
	for next, want := range map[string]string{"//evil.example/x": "/", "/internal/passcode/days/?d=2": "/days/?d=2", "/internal/offline": "/", "/internal/passcode//evil.com": "/"} {
		if r := p.Unlock(t, p.Host, code, next, nil); r.Status != http.StatusSeeOther || r.Header.Get("Location") != want {
			t.Fatalf("next %q: %d %q want %q", next, r.Status, r.Header.Get("Location"), want)
		}
	}
	r := p.Unlock(t, p.Host, "  "+code+" ", "/app.js?v=2", nil)
	if r.Status != http.StatusSeeOther || r.Header.Get("Location") != "/app.js?v=2" {
		t.Fatalf("unlock: %d %s %s", r.Status, r.Header.Get("Location"), r.Body)
	}
	cookie := CookieHeader(t, r)
	withCookie := map[string]string{"Cookie": cookie}
	for _, path := range []string{"/", "/app.js", "/days/"} {
		r := p.At(t, "GET", p.Host, path, nil, withCookie)
		if r.Status != 200 || !strings.Contains(string(r.Body), PasscodeCanary) || r.Header.Get("Cache-Control") != "private, no-cache" || r.Header.Get("Cross-Origin-Resource-Policy") != "same-origin" {
			t.Fatalf("unlocked %s: %d %v", path, r.Status, r.Header)
		}
	}
	// XSSI: a sibling page cannot pull the site's script in, cookie or not;
	// a top-level navigation from elsewhere works.
	if r := p.At(t, "GET", p.Host, "/app.js", nil, map[string]string{"Cookie": cookie, "Sec-Fetch-Site": "same-site", "Sec-Fetch-Mode": "no-cors", "Sec-Fetch-Dest": "script"}); r.Status != 401 {
		t.Fatalf("cross-site script with cookie: %d", r.Status)
	}
	if r := p.At(t, "GET", p.Host, "/", nil, map[string]string{"Cookie": cookie, "Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document"}); r.Status != 200 {
		t.Fatalf("navigation with cookie: %d", r.Status)
	}

	// Change the passcode, or sign everyone out: the cookie stops working.
	p.Lock(t, "Canary-Pass-6160")
	if r := p.At(t, "GET", p.Host, "/", nil, withCookie); r.Status != 401 {
		t.Fatalf("old cookie after change: %d", r.Status)
	}
	cookie2 := CookieHeader(t, p.Unlock(t, p.Host, "Canary-Pass-6160", "/", nil))
	if r := p.At(t, "GET", p.Host, "/", nil, map[string]string{"Cookie": cookie2}); r.Status != 200 {
		t.Fatalf("new cookie: %d", r.Status)
	}
	p.At(t, "POST", rt.SiteDomain, "/v1/sites/trip/lock/sign-out-everyone", nil, p.Okey)
	if r := p.At(t, "GET", p.Host, "/", nil, map[string]string{"Cookie": cookie2}); r.Status != 401 {
		t.Fatalf("cookie after sign-out-everyone: %d", r.Status)
	}

	// Offline wins over the passcode.
	if err := p.Disk.SetOffline(p.Olive.ID, "trip", true); err != nil {
		t.Fatal(err)
	}
	if r := p.At(t, "GET", p.Host, "/", nil, nil); r.Status != http.StatusServiceUnavailable {
		t.Fatalf("offline + passcode: %d", r.Status)
	}

	// No passcode or cookie in the log; unlocks are logged.
	for _, secret := range []string{code, "Canary-Pass-6160", strings.SplitN(cookie, "=", 2)[1]} {
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("the log holds a secret %q", secret)
		}
	}
	if !strings.Contains(logs.String(), "passcode_unlock ok=true") || !strings.Contains(logs.String(), "passcode_unlock ok=false") {
		t.Fatalf("unlocks not logged: %s", logs.String())
	}
}

// One site's unlock (cookie or passcode) never opens another site of the
// same person.
func TestPasscodePerSite(t *testing.T) {
	a := NewApp(t)
	a.EnablePasscodes(t)
	oscar := a.NewPerson(t, "oscar")
	for _, s := range []string{"camp", "blog"} {
		rt.Deploy(t, a.Database, a.Disk, oscar, s, map[string]string{"index.html": s + " " + PasscodeCanary})
		if r := a.At(t, "PUT", rt.SiteDomain, "/v1/sites/"+s+"/lock", map[string]any{"passcode": "oscar-" + s}, oscar.Header()); r.Status != 200 {
			t.Fatalf("lock %s: %d %s", s, r.Status, r.Body)
		}
	}
	a.MarkReady(t, oscar)
	ps := &PasscodeSite{App: a}
	camp, blog := oscar.SiteHost("camp"), oscar.SiteHost("blog")
	ck := map[string]string{"Cookie": CookieHeader(t, ps.Unlock(t, camp, "oscar-camp", "/", nil))}
	if r := a.At(t, "GET", camp, "/", nil, ck); r.Status != 200 {
		t.Fatalf("camp unlocked: %d", r.Status)
	}
	if r := a.At(t, "GET", blog, "/", nil, ck); r.Status != 401 {
		t.Fatalf("camp's cookie opened blog: %d", r.Status)
	}
	if r := ps.Unlock(t, blog, "oscar-camp", "/", nil); r.Status != 401 {
		t.Fatalf("camp's passcode on blog: %d", r.Status)
	}
}

func TestPasscodeBruteForce(t *testing.T) {
	p := NewPasscodeSite(t)
	const code = "right-one-1"
	p.Lock(t, code)
	l := p.Access.Passcode.Limits
	l.RatePasscodeIP = config.Rate{Burst: 3, Every: time.Hour}
	l.RatePasscodeSite = config.Rate{Burst: 5, Every: time.Hour}
	l.PasscodeLockout, l.PasscodeSiteLockout = 15*time.Minute, 15*time.Minute
	p.Access.Passcode.Limits = l
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
	later := time.Now().Add(16 * time.Minute)
	p.Access.Passcode.Now = func() time.Time { return later }
	if r := p.Unlock(t, p.Host, code, "/", ip("5")); r.Status != http.StatusSeeOther {
		t.Fatalf("after the lockout: %d", r.Status)
	}
}

// The marker follows the database at boot: restored when missing, removed
// when the database no longer backs it.
func TestPasscodeMarkerBootSync(t *testing.T) {
	p := NewPasscodeSite(t)
	p.Lock(t, "marker-pass")
	marker := filepath.Join(p.PageDir, "passcode")
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	p.Access.SyncSuspendMarkers(context.Background())
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("boot sync did not restore the marker: %v", err)
	}
	if _, err := p.Database.Exec(`UPDATE sites SET passcode_enc = NULL WHERE id = $1`, p.SiteID); err != nil {
		t.Fatal(err)
	}
	if r := p.At(t, "GET", p.Host, "/", nil, nil); r.Status != 200 {
		t.Fatalf("database says open: %d", r.Status)
	}
	p.Access.SyncSuspendMarkers(context.Background())
	if p.Disk.HasPasscodeMarker(p.Olive.ID, "trip") {
		t.Fatal("stale marker kept")
	}
}

// Visitor sign-in on a site with a passcode needs the unlock first.
func TestPasscodeVisitorSignIn(t *testing.T) {
	p := NewPasscodeSite(t)
	p.WithOAuth(t)
	p.Lock(t, "signin-pass")
	start := "/v1/visitor/oauth/google?return_to=" + "https%3A%2F%2F" + p.Host + "%2F"
	if r := p.At(t, "GET", p.Host, start, nil, nil); r.Status != http.StatusForbidden || r.Code() != "site_locked" {
		t.Fatalf("sign-in start without the unlock: %d %s", r.Status, r.Body)
	}
	if r := p.At(t, "POST", p.Host, "/v1/sites/trip/visitor/auth", map[string]any{"email": "a@example.test"}, map[string]string{"Origin": "https://" + p.Host}); r.Status != http.StatusForbidden {
		t.Fatalf("email sign-in without the unlock: %d %s", r.Status, r.Body)
	}
	ck := CookieHeader(t, p.Unlock(t, p.Host, "signin-pass", "/", nil))
	if r := p.At(t, "GET", p.Host, start, nil, map[string]string{"Cookie": ck}); r.Status != http.StatusFound {
		t.Fatalf("sign-in start after the unlock: %d %s", r.Status, r.Body)
	}
}
