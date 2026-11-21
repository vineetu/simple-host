package accesstest

import (
	"context"
	"encoding/hex"
	"net/url"
	"strings"
	"testing"
	"time"

	accountapp "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/accesshttp"
	"github.com/vsriram/simple-host/host/access/auth"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/server/servertest"
)

// Claude review of the SSO red-team fixes and SIM-348 (2026-10-30). Needs DB_DSN.

type reviewEnv struct {
	a                     *servertest.PrivateApp
	o                     *accesshttp.OAuthHandler
	host, site, uid, pass string
	viewer                servertest.Person
}

func newReviewEnv(t *testing.T, consent bool) reviewEnv {
	a, dir, o := newSSOApp(t)
	owner := a.NewPerson(t, "revowner")
	viewer := a.NewPerson(t, "revviewer")
	a.Deploy(t, owner, "site")
	_, handle := a.UserID(t, owner)
	servertest.MarkReady(t, dir, handle)
	host := "site." + handle + "." + servertest.PcSiteDomain
	site := a.SiteID(t, owner, "site")
	uid, _ := a.UserID(t, viewer)
	pass := a.SSOPass(t, viewer.Key)
	if consent {
		if err := accountsrepo.RememberSSOConsent(context.Background(), a.Database, uid, site); err != nil {
			t.Fatal(err)
		}
	}
	return reviewEnv{a, o, host, site, uid, pass, viewer}
}

func (e reviewEnv) sessions(t *testing.T) int {
	var n int
	if err := e.a.Database.QueryRow(`SELECT count(*) FROM visitor_sessions WHERE user_id=$1`, e.uid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// SIM-348: a page on the shared content host gets no credentialed read of a
// site's own host, so it never learns that site's visitor. The site's own
// page and the content host's own requests are unchanged.
func TestContentHostOriginGetsNoOtherSiteVisitor(t *testing.T) {
	e := newReviewEnv(t, false)
	sess, _ := e.a.FinishSSO(t, e.host, e.pass)
	me := "/v1/sites/site/me"
	cookie := accesshttp.VisitorCookieHost + "=" + sess
	for _, h := range []map[string]string{
		{"Origin": "https://" + servertest.PcContentHost, "Sec-Fetch-Site": "same-site", "Cookie": cookie},
		{"Referer": "https://" + servertest.PcContentHost + "/x/y/", "Sec-Fetch-Site": "same-site", "Cookie": cookie},
		{"Origin": "https://" + servertest.PcContentHost, "Cookie": cookie},
	} {
		r := e.a.At(t, "GET", e.host, me, nil, h)
		if r.Header.Get("Access-Control-Allow-Origin") != "" || strings.Contains(string(r.Body), "@") {
			t.Fatalf("content host origin read the site's visitor: %d acao=%q %s", r.Status, r.Header.Get("Access-Control-Allow-Origin"), r.Body)
		}
		if r.Status != 403 {
			t.Fatalf("content host origin on a site host: %d, want 403", r.Status)
		}
	}
	own := e.a.At(t, "GET", e.host, me, nil, map[string]string{"Origin": "https://" + e.host, "Sec-Fetch-Site": "same-origin", "Cookie": cookie})
	if own.Status != 200 || !strings.Contains(string(own.Body), `"signed_in":true`) {
		t.Fatalf("own page lost its visitor: %d %s", own.Status, own.Body)
	}
	shared := e.a.At(t, "GET", servertest.PcContentHost, "/v1/u/"+strings.Split(e.host, ".")[1]+"/sites/site/me", nil, map[string]string{"Origin": "https://" + servertest.PcContentHost, "Sec-Fetch-Site": "same-origin"})
	if shared.Status != 200 || strings.Contains(string(shared.Body), `"signed_in":true`) {
		t.Fatalf("content host /me: %d %s", shared.Status, shared.Body)
	}
}

// F3: dashboard Sign out ends this browser's family even when the cookie's
// pass came from an older key than the one being signed out.
func TestDashboardSignOutEndsCookieFamily(t *testing.T) {
	e := newReviewEnv(t, false)
	sessA, _ := e.a.FinishSSO(t, e.host, e.pass)
	k2, err := auth.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := accountapp.New(e.a.Database).AddSignInKey(context.Background(), e.uid, k2, "dashboard sign-in"); err != nil {
		t.Fatal(err)
	}
	r := e.a.At(t, "POST", e.a.AppHost(), "/v1/me/sign-out", nil, map[string]string{"X-API-Key": k2, "Cookie": accesshttp.SSOCookieHost + "=" + e.pass, "Origin": "https://" + e.a.AppHost(), "Sec-Fetch-Site": "same-origin"})
	if r.Status != 204 {
		t.Fatalf("sign-out: %d %s", r.Status, r.Body)
	}
	id, _ := hex.DecodeString(sessA)
	if _, err := accountsrepo.GetVisitorSession(context.Background(), e.a.Database, id, 180*24*time.Hour); err == nil {
		t.Fatal("site session from the browser's pass survived dashboard sign-out")
	}
	if me := e.a.At(t, "GET", e.a.AppHost(), "/v1/me", nil, map[string]string{"X-API-Key": e.viewer.Key}); me.Status != 401 {
		t.Fatalf("the pass's parent key still works: %d", me.Status)
	}
}

// F2: HEAD matches the GET hop route and gets the same prefetch check.
func TestHopHEADPrefetchMintsNothing(t *testing.T) {
	e := newReviewEnv(t, true)
	state, _ := e.a.SSOStartAt(t, e.host)
	e.a.At(t, "HEAD", e.a.AppHost(), "/v1/sso/hop?state="+state, nil, map[string]string{"Cookie": accesshttp.SSOCookieHost + "=" + e.pass, "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document", "Sec-Purpose": "prefetch;prerender"})
	if n := e.sessions(t); n != 0 {
		t.Fatalf("HEAD prefetch minted %d sessions", n)
	}
}

// Image loads of /v1/sso/start from another page do not spend the per-IP
// allowance an honest sign-in needs.
func TestHopStartSubresourcesDoNotDrainLimit(t *testing.T) {
	e := newReviewEnv(t, false)
	e.o.IPLimiter.Capacity = 20
	e.o.IPLimiter.Rate = 0.2
	for i := 0; i < 30; i++ {
		e.a.At(t, "GET", e.host, "/v1/sso/start?return_to="+url.QueryEscape("https://"+e.host+"/"), nil, map[string]string{"Sec-Fetch-Dest": "image", "Sec-Fetch-Mode": "no-cors", "Sec-Fetch-Site": "cross-site"})
	}
	r := e.a.At(t, "GET", e.host, "/v1/sso/start?return_to="+url.QueryEscape("https://"+e.host+"/page"), nil, map[string]string{"Sec-Fetch-Dest": "document", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Site": "none"})
	if r.Status == 429 {
		t.Fatal("honest start refused after 30 cross-site image loads")
	}
}

// SIM-348: an owner's allowed_origins cannot bring the content host back.
func TestAllowedOriginsCannotReaddContentHost(t *testing.T) {
	e := newReviewEnv(t, false)
	if _, err := e.a.Database.Exec(`UPDATE sites SET allowed_origins=$2 WHERE id=$1`, e.site, "https://"+servertest.PcContentHost); err != nil {
		t.Fatal(err)
	}
	r := e.a.At(t, "GET", e.host, "/v1/sites/site/me", nil, map[string]string{"Origin": "https://" + servertest.PcContentHost, "Sec-Fetch-Site": "same-site"})
	if r.Status != 403 || r.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("listed content host: %d acao=%q", r.Status, r.Header.Get("Access-Control-Allow-Origin"))
	}
}
