package accesstest

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/access/accesshttp"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
)

func TestViewersOwnerAPI(t *testing.T) {
	p := NewPasscodeSite(t)
	marker := filepath.Join(p.PageDir, "passcode")

	if r := p.At(t, "GET", rt.SiteDomain, "/v1/sites/trip/access", nil, p.Okey); r.Status != 200 || r.JSON(t)["access"] != "anyone" {
		t.Fatalf("read: %d %s", r.Status, r.Body)
	}
	if r := p.Grant(t, "not-an-email"); r.Status != 400 || r.Code() != "invalid_email" {
		t.Fatalf("bad email: %d %s", r.Status, r.Body)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("marker after a refused grant")
	}
	r := p.Grant(t, " Mom@Example.COM ", "mom@example.com", "dad@example.com")
	if j := r.JSON(t); r.Status != 200 || j["access"] != "specific" || len(j["viewers"].([]any)) != 2 || len(j["added"].([]any)) != 2 {
		t.Fatalf("grant: %d %s", r.Status, r.Body)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("no marker after the grant")
	}
	if r := p.Grant(t, "MOM@example.com"); r.Status != 200 || r.JSON(t)["already_listed"] == nil {
		t.Fatalf("again: %d %s", r.Status, r.Body)
	}
	// A passcode and named viewers never together, either way round.
	if r := p.Lock(t, "123456"); r.Status != http.StatusConflict || r.Code() != "named_viewers_set" {
		t.Fatalf("passcode on a named-viewers site: %d %s", r.Status, r.Body)
	}
	// The cap.
	p.Access.Limits.SiteViewersMax = 3
	if r := p.Grant(t, "a@example.com", "b@example.com"); r.Status != http.StatusConflict || r.Code() != "too_many_viewers" {
		t.Fatalf("cap: %d %s", r.Status, r.Body)
	}
	if r := p.At(t, "DELETE", rt.SiteDomain, "/v1/sites/trip/viewers/DAD@example.com", nil, p.Okey); r.Status != 200 || r.JSON(t)["removed"] != "dad@example.com" {
		t.Fatalf("remove: %d %s", r.Status, r.Body)
	}
	if r := p.At(t, "DELETE", rt.SiteDomain, "/v1/sites/trip/viewers/dad@example.com", nil, p.Okey); r.Status != 404 {
		t.Fatalf("remove again: %d %s", r.Status, r.Body)
	}
	mallory := p.NewPerson(t, "mallory")
	if r := p.At(t, "GET", rt.SiteDomain, "/v1/sites/trip/access", nil, mallory.Header()); r.Status != 404 {
		t.Fatalf("mallory read: %d %s", r.Status, r.Body)
	}
	if r := p.At(t, "POST", rt.SiteDomain, "/v1/sites/trip/viewers", map[string]any{"emails": []string{"m@example.com"}}, mallory.Header()); r.Status != 404 {
		t.Fatalf("mallory grant: %d %s", r.Status, r.Body)
	}
	// Open to anyone again: the marker goes, the list stays.
	r = p.At(t, "PUT", rt.SiteDomain, "/v1/sites/trip/access", map[string]any{"access": "anyone"}, p.Okey)
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
	if r := p.At(t, "PUT", rt.SiteDomain, "/v1/sites/trip/access", map[string]any{"access": "specific"}, p.Okey); r.Status != http.StatusConflict || r.Code() != "passcode_set" {
		t.Fatalf("specific with a passcode: %d %s", r.Status, r.Body)
	}
	if r := p.Grant(t, "x@example.com"); r.Status != http.StatusConflict || r.Code() != "passcode_set" {
		t.Fatalf("grant with a passcode: %d %s", r.Status, r.Body)
	}
	if r := p.At(t, "GET", rt.SiteDomain, "/v1/sites/trip/access", nil, p.Okey); strings.Contains(string(r.Body), "x@example.com") {
		t.Fatalf("refused grant was stored: %s", r.Body)
	}
	if r := p.At(t, "PUT", rt.SiteDomain, "/v1/sites/trip/access", map[string]any{"access": "bogus"}, p.Okey); r.Status != 400 {
		t.Fatalf("bogus access: %d", r.Status)
	}
}

// gateViewerSite: olive's "trip" open only to mom; dad is signed in but not
// listed.
type gateViewerSite struct {
	*PasscodeSite
	mom, dad Person
}

func newGateViewerSite(t *testing.T) *gateViewerSite {
	t.Helper()
	p := NewPasscodeSite(t)
	v := &gateViewerSite{PasscodeSite: p, mom: p.NewPerson(t, "mom"), dad: p.NewPerson(t, "dad")}
	if r := p.Grant(t, v.mom.Email); r.Status != 200 {
		t.Fatalf("grant: %d %s", r.Status, r.Body)
	}
	return v
}

func TestViewersGatePages(t *testing.T) {
	v := newGateViewerSite(t)
	p := v.PasscodeSite
	mom := p.Session(t, v.mom, p.SiteID, p.Host)
	dad := p.Session(t, v.dad, p.SiteID, p.Host)
	olive := p.Session(t, p.Olive, p.SiteID, p.Host)
	noLeak := func(what string, r Resp) {
		t.Helper()
		if strings.Contains(string(r.Body), PasscodeCanary) {
			t.Fatalf("%s leaked: %d %s", what, r.Status, r.Body)
		}
	}
	// The other spelling of a page: the gate answers first, and only a
	// listed viewer learns where the page lives.
	if r := p.At(t, "GET", p.Host, "/days", nil, nil); r.Status != 401 || r.Header.Get("Location") != "" {
		t.Fatalf("signed out /days: %d %v", r.Status, r.Header)
	}
	if r := p.At(t, "GET", p.Host, "/days", nil, CookieOf(dad)); r.Status != 403 || r.Header.Get("Location") != "" {
		t.Fatalf("dad /days: %d %v", r.Status, r.Header)
	}
	if r := p.At(t, "GET", p.Host, "/days", nil, CookieOf(mom)); r.Status == 401 || r.Status == 403 {
		t.Fatalf("mom /days: %d %v", r.Status, r.Header) // past the gate (the redirect itself is Sites')
	}
	for _, path := range []string{"/", "/app.js", "/days/", "/nope"} {
		r := p.At(t, "GET", p.Host, path, nil, nil)
		noLeak("signed out "+path, r)
		if r.Status != 401 || r.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("signed out %s: %d %v", path, r.Status, r.Header)
		}
		r = p.At(t, "GET", p.Host, path, nil, CookieOf(dad))
		noLeak("dad "+path, r)
		if r.Status != 403 {
			t.Fatalf("dad %s: %d", path, r.Status)
		}
		for who, ck := range map[string]string{"mom": mom, "olive": olive} {
			r = p.At(t, "GET", p.Host, path, nil, CookieOf(ck))
			if path != "/nope" && (r.Status != 200 || !strings.Contains(string(r.Body), PasscodeCanary)) {
				t.Fatalf("%s %s: %d %s", who, path, r.Status, r.Body)
			}
			if cc := r.Header.Get("Cache-Control"); cc != "private, no-cache" && cc != "no-store" {
				t.Fatalf("%s %s cache: %q", who, path, cc)
			}
		}
	}
	// The plain cookie a sibling host could plant never counts, nor mom's
	// cookie on a cross-site subresource, nor her session for another host.
	noLeak("plain cookie", p.At(t, "GET", p.Host, "/app.js", nil, map[string]string{"Cookie": accesshttp.VisitorCookieHTTP + "=" + mom}))
	noLeak("cross-site", p.At(t, "GET", p.Host, "/app.js", nil, map[string]string{"Cookie": accesshttp.VisitorCookieHost + "=" + mom, "Sec-Fetch-Site": "same-site", "Sec-Fetch-Mode": "no-cors", "Sec-Fetch-Dest": "script"}))
	noLeak("other-host session", p.At(t, "GET", p.Host, "/app.js", nil, CookieOf(p.Session(t, v.mom, p.SiteID, "elsewhere."+rt.SiteDomain))))
	// Another site's page framing this one never gets it; the site itself may.
	for _, dest := range []string{"iframe", "embed"} {
		r := p.At(t, "GET", p.Host, "/", nil, map[string]string{"Cookie": accesshttp.VisitorCookieHost + "=" + mom, "Sec-Fetch-Site": "same-site", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": dest})
		noLeak("framed "+dest, r)
	}
	if r := p.At(t, "GET", p.Host, "/", nil, map[string]string{"Cookie": accesshttp.VisitorCookieHost + "=" + mom, "Sec-Fetch-Site": "same-origin", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "iframe"}); r.Status != 200 || !strings.Contains(strings.Join(r.Header.Values("Content-Security-Policy"), ";"), "frame-ancestors 'self'") {
		t.Fatalf("framed by the site itself: %d", r.Status)
	}
	if r := p.At(t, "GET", p.Host, "/robots.txt", nil, nil); r.Status != 200 || !strings.Contains(string(r.Body), "Disallow: /") {
		t.Fatalf("robots: %d %s", r.Status, r.Body)
	}
	// Sign-in routes stay open so a viewer can sign in.
	if r := p.At(t, "GET", p.Host, "/v1/sites/trip/me", nil, rt.Browser(p.Host, dad)); r.Status != 200 || !strings.Contains(string(r.Body), v.dad.Email) {
		t.Fatalf("dad /me: %d %s", r.Status, r.Body)
	}
	if r := p.At(t, "POST", p.Host, "/v1/sites/trip/visitor/auth", map[string]any{"email": "x@example.com"}, rt.Browser(p.Host, "")); r.Status == 403 {
		t.Fatalf("visitor sign-in refused: %d %s", r.Status, r.Body)
	}
}

func TestViewersListChangesAtOnce(t *testing.T) {
	v := newGateViewerSite(t)
	p := v.PasscodeSite
	mom := p.Session(t, v.mom, p.SiteID, p.Host)
	dad := p.Session(t, v.dad, p.SiteID, p.Host)
	status := func(ck string) int { return p.At(t, "GET", p.Host, "/app.js", nil, CookieOf(ck)).Status }
	if status(mom) != 200 || status(dad) != 403 {
		t.Fatal("start")
	}
	if r := p.Grant(t, v.dad.Email); r.Status != 200 {
		t.Fatal(r.Status)
	}
	if status(dad) != 200 {
		t.Fatal("dad not let in at once")
	}
	if r := p.At(t, "DELETE", rt.SiteDomain, "/v1/sites/trip/viewers/"+url.PathEscape(v.mom.Email), nil, p.Okey); r.Status != 200 {
		t.Fatal(r.Status)
	}
	if status(mom) != 403 {
		t.Fatal("mom not locked out at once")
	}
	if _, c := p.Probe(t, p.Host, "trip", rt.Browser(p.Host, mom)); c.ViewersOK {
		t.Fatal("mom's data after removal")
	}
	// A suspended account is out too.
	if _, err := p.Database.Exec(`UPDATE users SET suspended_at = now() WHERE id = $1`, v.dad.ID); err != nil {
		t.Fatal(err)
	}
	if status(dad) == 200 {
		t.Fatal("suspended viewer let in")
	}
	p.At(t, "PUT", rt.SiteDomain, "/v1/sites/trip/access", map[string]any{"access": "anyone"}, p.Okey)
	if r := p.At(t, "GET", p.Host, "/app.js", nil, nil); r.Status != 200 {
		t.Fatalf("anyone: %d", r.Status)
	}
}

// The owner's "Who can open" list says whether each named viewer has
// actually signed in yet, not just when they were named.
func TestViewersSignedInStatus(t *testing.T) {
	v := newGateViewerSite(t)
	p := v.PasscodeSite
	signedIn := func() any {
		r := p.At(t, "GET", rt.SiteDomain, "/v1/sites/trip/access", nil, p.Okey)
		for _, raw := range r.JSON(t)["viewers"].([]any) {
			if vw := raw.(map[string]any); vw["email"] == v.mom.Email {
				return vw["signed_in_at"]
			}
		}
		t.Fatalf("mom not listed: %s", r.Body)
		return nil
	}
	if signedIn() != nil {
		t.Fatal("signed_in_at before any sign-in")
	}
	p.Session(t, v.mom, p.SiteID, p.Host)
	if signedIn() == nil {
		t.Fatal("no signed_in_at after signing in")
	}
}

func TestViewersSignInForm(t *testing.T) {
	v := newGateViewerSite(t)
	p := v.PasscodeSite
	mail := &Mailbox{}
	p.Access.Mailer = mail
	form := func(vals url.Values, extra map[string]string) Resp {
		h := map[string]string{"Origin": "https://" + p.Host, "Content-Type": "application/x-www-form-urlencoded", "Sec-Fetch-Site": "same-origin"}
		for k, val := range extra {
			h[k] = val
		}
		return p.At(t, "POST", p.Host, "/v1/site-signin", vals.Encode(), h)
	}
	if r := form(url.Values{"email": {v.mom.Email}, "next": {"/"}}, map[string]string{"Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site"}); r.Status != 403 || mail.Code(v.mom.Email) != "" {
		t.Fatalf("cross-origin: %d %s", r.Status, r.Body)
	}
	if r := form(url.Values{"email": {strings.ToUpper(v.mom.Email)}, "next": {"/days/"}}, nil); r.Status != 401 || strings.Contains(string(r.Body), PasscodeCanary) {
		t.Fatalf("code page: %d %s", r.Status, r.Body)
	}
	code := mail.Code(v.mom.Email)
	if code == "" {
		t.Fatal("no code sent")
	}
	if r := form(url.Values{"email": {v.mom.Email}, "code": {"000000"}, "next": {"/days/"}}, nil); r.Status != 401 || len(r.Header.Values("Set-Cookie")) != 0 {
		t.Fatalf("wrong code: %d %s", r.Status, r.Body)
	}
	r := form(url.Values{"email": {v.mom.Email}, "code": {code}, "next": {"/days/"}}, nil)
	if r.Status != http.StatusSeeOther || r.Header.Get("Location") != "/days/" {
		t.Fatalf("sign in: %d %v %s", r.Status, r.Header, r.Body)
	}
	ck := ResponseCookie(t, r, accesshttp.VisitorCookieHost)
	if r := p.At(t, "GET", p.Host, "/days/", nil, CookieOf(ck)); r.Status != 200 || !strings.Contains(string(r.Body), PasscodeCanary) {
		t.Fatalf("after sign-in: %d %s", r.Status, r.Body)
	}
	so := p.At(t, "POST", p.Host, "/v1/site-signout", url.Values{"next": {"/"}}.Encode(), map[string]string{"Origin": "https://" + p.Host, "Content-Type": "application/x-www-form-urlencoded", "Sec-Fetch-Site": "same-origin", "Cookie": accesshttp.VisitorCookieHost + "=" + ck})
	if so.Status != http.StatusSeeOther {
		t.Fatalf("sign out: %d %s", so.Status, so.Body)
	}
	if r := p.At(t, "GET", p.Host, "/", nil, CookieOf(ck)); r.Status != 401 {
		t.Fatalf("after sign out: %d", r.Status)
	}
	// next never leaves the host.
	for _, next := range []string{"//evil.example/", "https://evil.example/", "/\\evil.example", "/internal/passcode//evil.example"} {
		r := form(url.Values{"email": {v.mom.Email}, "code": {"1"}, "next": {next}}, nil)
		if loc := r.Header.Get("Location"); (loc != "" && loc != "/") || strings.Contains(string(r.Body), "evil.example") {
			t.Fatalf("next %q: Location %q", next, loc)
		}
	}
}
