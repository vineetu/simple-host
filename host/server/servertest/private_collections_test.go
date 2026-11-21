package servertest

import (
	"context"
	"encoding/hex"
	"net/http"
	"strings"
	"testing"
	"time"

	accountapp "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/accesshttp"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
)

// Sessions and cookies on a claimed address: host-only __Host- cookie, never a
// Domain= attribute; /me from a sibling host reports signed out; sign-in by
// emailed code must come from the host itself.
func TestClaimedSubdomainSessionIsolation(t *testing.T) {
	a := NewPrivateApp(t)
	olive, vic := a.NewPerson(t, "olive"), a.NewPerson(t, "vic")
	a.Deploy(t, olive, "clay")
	const dom = "clay-and-kiln.simple-host.test"
	if r := a.At(t, "POST", "simple-host.test", "/v1/sites/clay/domain", map[string]string{"domain": dom}, map[string]string{"X-API-Key": olive.Key}); r.Status != 200 {
		t.Fatalf("claim: %d %s", r.Status, r.Body)
	}
	siteID := a.SiteID(t, olive, "clay")
	// Establish hand-off on the claimed host sets a host-only __Host- cookie.
	uid, _ := a.UserID(t, vic)
	sid, _ := hex.DecodeString(strings.Repeat("ab", 32))
	now := time.Now()
	if _, err := accountsrepo.InsertVisitorSession(context.Background(), a.Database, sid, uid, siteID, dom, now.Add(time.Hour), now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = accountsrepo.DeleteVisitorSession(context.Background(), a.Database, sid) })
	if err := accountsrepo.InsertEstablishToken(context.Background(), a.Database, "once-"+uid, sid, dom, "https://"+dom+"/", TestNonceHash(TestNonce), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	r := a.At(t, "GET", dom, "/v1/visitor/establish?once=once-"+uid, nil, NonceCookie(TestNonce))
	sc := r.Header.Values("Set-Cookie")
	if r.Status != http.StatusFound || len(sc) != 2 || !strings.HasPrefix(sc[0], accesshttp.VisitorCookieHost+"=") ||
		strings.Contains(strings.ToLower(sc[0]), "domain=") || !strings.Contains(sc[0], "Secure") || !strings.Contains(sc[0], "HttpOnly") {
		t.Fatalf("establish cookie: %d %v", r.Status, sc)
	}
	cookie := strings.Repeat("ab", 32)
	// /me: same origin sees the visitor; a sibling host's page does not.
	me := "/v1/sites/clay/me"
	if r := a.At(t, "GET", dom, me, nil, Browser(dom, cookie)); r.JSON(t)["signed_in"] != true || r.JSON(t)["email"] != vic.Email {
		t.Fatalf("/me same origin: %s", r.Body)
	}
	for _, o := range []string{"https://" + PcContentHost, "https://evil.simple-host.test"} {
		r := a.At(t, "GET", dom, me, nil, Browser(dom, cookie, "Origin", o, "Sec-Fetch-Site", "same-site"))
		if r.JSON(t)["signed_in"] == true || strings.Contains(string(r.Body), vic.Email) {
			t.Errorf("/me from %s: %s", o, r.Body)
		}
	}
	// Email sign-in on the claimed host must be same-origin.
	if r := a.At(t, "POST", dom, "/v1/sites/clay/visitor/auth/verify", map[string]string{"email": vic.Email, "code": "123456"}, map[string]string{"Origin": "https://" + PcContentHost, "X-SH-CSRF": "1"}); r.Status != 403 {
		t.Errorf("sibling sign-in: %d %s", r.Status, r.Body)
	}
	// CORS: the claimed host's Origin is its own; another site's API does not trust it.
	a.Deploy(t, vic, "other")
	_, vicHandle := a.UserID(t, vic)
	if r := a.At(t, "GET", PcContentHost, "/v1/u/"+vicHandle+"/sites/other/me", nil, map[string]string{"Origin": "https://" + dom}); r.Status != 403 {
		t.Errorf("claimed origin trusted by another site: %d", r.Status)
	}
	// Apex auth is unaffected: the dashboard API takes keys, never this cookie.
	if r := a.At(t, "GET", "simple-host.test", "/v1/me", nil, map[string]string{"Cookie": accesshttp.VisitorCookieHost + "=" + cookie}); r.Status != 401 {
		t.Errorf("cookie accepted as apex credential: %d", r.Status)
	}
	// Sign-in may return to the claimed host (no DNS proof needed); not to an unclaimed one.
	oh := &accesshttp.OAuthHandler{Database: a.Database, Accounts: accountapp.New(a.Database), Cfg: accesshttp.OAuthSettings{PublicBaseURL: "https://simple-host.test", SiteDomain: PcSiteDomain, ContentHost: PcContentHost, CNAMETarget: "cname." + PcSiteDomain}}
	if _, site, _, purpose, err := oh.SanitizeReturnTo(context.Background(), "https://"+dom+"/rsvp.html"); err != nil || purpose != "site" || site.String != siteID {
		t.Errorf("return_to claimed host: %v %q", err, purpose)
	}
	for _, bad := range []string{"https://unclaimed.simple-host.test/", "https://www.simple-host.test/", "https://" + PcContentHost + "/x/y/"} {
		if _, _, _, _, err := oh.SanitizeReturnTo(context.Background(), bad); err == nil {
			t.Errorf("return_to %s accepted", bad)
		}
	}
	// Releasing the address frees the name and restores the legacy redirect.
	if r := a.At(t, "DELETE", "simple-host.test", "/v1/sites/clay/domain", nil, map[string]string{"X-API-Key": olive.Key}); r.Status != 204 {
		t.Fatalf("release: %d", r.Status)
	}
	if r := a.At(t, "GET", dom, "/", nil, nil); r.Status != http.StatusMovedPermanently {
		t.Fatalf("released host: %d", r.Status)
	}
}

// An establish code presented on the wrong host is burned: it cannot then be
// replayed on the host it was issued for. The normal hand-off still works once.
func TestEstablishTokenWrongHostBurns(t *testing.T) {
	a := NewPrivateApp(t)
	olive, vic := a.NewPerson(t, "olive"), a.NewPerson(t, "vic")
	a.Deploy(t, olive, "clay")
	a.Deploy(t, olive, "pots")
	const dom, other = "burn-clay.simple-host.test", "burn-pots.simple-host.test"
	for site, d := range map[string]string{"clay": dom, "pots": other} {
		if r := a.At(t, "POST", "simple-host.test", "/v1/sites/"+site+"/domain", map[string]string{"domain": d}, map[string]string{"X-API-Key": olive.Key}); r.Status != 200 {
			t.Fatalf("claim %s: %d %s", d, r.Status, r.Body)
		}
	}
	siteID := a.SiteID(t, olive, "clay")
	uid, _ := a.UserID(t, vic)
	ctx := context.Background()
	now := time.Now()
	issue := func(sidByte, once string) {
		sid, _ := hex.DecodeString(strings.Repeat(sidByte, 32))
		if _, err := accountsrepo.InsertVisitorSession(ctx, a.Database, sid, uid, siteID, dom, now.Add(time.Hour), now.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = accountsrepo.DeleteVisitorSession(ctx, a.Database, sid) })
		if err := accountsrepo.InsertEstablishToken(ctx, a.Database, once, sid, dom, "https://"+dom+"/", TestNonceHash(TestNonce), now.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	establish := func(host, once string) Resp {
		return a.At(t, "GET", host, "/v1/visitor/establish?once="+once, nil, NonceCookie(TestNonce))
	}

	// Wrong host: rejected, then the right host is rejected too (burned).
	burn := "burn-" + uid
	issue("cd", burn)
	if r := establish(other, burn); r.Status != http.StatusBadRequest || len(r.Header.Values("Set-Cookie")) != 0 {
		t.Fatalf("wrong host accepted: %d %v", r.Status, r.Header.Values("Set-Cookie"))
	}
	if r := establish(dom, burn); r.Status != http.StatusBadRequest || len(r.Header.Values("Set-Cookie")) != 0 {
		t.Fatalf("burned code replayed on right host: %d %v", r.Status, r.Header.Values("Set-Cookie"))
	}

	// Normal flow: right host works once, then the code is spent.
	good := "good-" + uid
	issue("ef", good)
	if r := establish(strings.ToUpper(dom), good); r.Status != http.StatusFound || len(r.Header.Values("Set-Cookie")) != 2 {
		t.Fatalf("normal establish: %d %v", r.Status, r.Header.Values("Set-Cookie"))
	}
	if r := establish(dom, good); r.Status != http.StatusBadRequest {
		t.Fatalf("code reused: %d", r.Status)
	}
}
