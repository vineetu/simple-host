package handler

import (
	"context"
	"encoding/hex"
	"net/url"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/auth"
	db "github.com/vsriram/simple-host/internal/db"
)

// Claude review of the SSO red-team fixes and SIM-348 (2026-10-30). Needs DB_DSN.

type reviewEnv struct {
	a                     *privateApp
	o                     *OAuthHandler
	host, site, uid, pass string
	viewer                person
}

func newReviewEnv(t *testing.T, consent bool) reviewEnv {
	a, dir, o := newSSOApp(t)
	owner := a.newPerson(t, "revowner")
	viewer := a.newPerson(t, "revviewer")
	a.deploy(t, owner, "site")
	_, handle := a.userID(t, owner)
	markReady(t, dir, handle)
	host := "site." + handle + "." + pcSiteDomain
	site := a.siteID(t, owner, "site")
	uid, _ := a.userID(t, viewer)
	pass := a.ssoPass(t, viewer.key)
	if consent {
		if err := db.RememberSSOConsent(context.Background(), a.database, uid, site); err != nil {
			t.Fatal(err)
		}
	}
	return reviewEnv{a, o, host, site, uid, pass, viewer}
}

func (e reviewEnv) sessions(t *testing.T) int {
	var n int
	if err := e.a.database.QueryRow(`SELECT count(*) FROM visitor_sessions WHERE user_id=$1`, e.uid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// SIM-348: a page on the shared content host gets no credentialed read of a
// site's own host, so it never learns that site's visitor. The site's own
// page and the content host's own requests are unchanged.
func TestContentHostOriginGetsNoOtherSiteVisitor(t *testing.T) {
	e := newReviewEnv(t, false)
	sess, _ := e.a.finishSSO(t, e.host, e.pass)
	me := "/v1/sites/site/me"
	cookie := visitorCookieHost + "=" + sess
	for _, h := range []map[string]string{
		{"Origin": "https://" + pcContentHost, "Sec-Fetch-Site": "same-site", "Cookie": cookie},
		{"Referer": "https://" + pcContentHost + "/x/y/", "Sec-Fetch-Site": "same-site", "Cookie": cookie},
		{"Origin": "https://" + pcContentHost, "Cookie": cookie},
	} {
		r := e.a.at(t, "GET", e.host, me, nil, h)
		if r.header.Get("Access-Control-Allow-Origin") != "" || strings.Contains(string(r.body), "@") {
			t.Fatalf("content host origin read the site's visitor: %d acao=%q %s", r.status, r.header.Get("Access-Control-Allow-Origin"), r.body)
		}
		if r.status != 403 {
			t.Fatalf("content host origin on a site host: %d, want 403", r.status)
		}
	}
	own := e.a.at(t, "GET", e.host, me, nil, map[string]string{"Origin": "https://" + e.host, "Sec-Fetch-Site": "same-origin", "Cookie": cookie})
	if own.status != 200 || !strings.Contains(string(own.body), `"signed_in":true`) {
		t.Fatalf("own page lost its visitor: %d %s", own.status, own.body)
	}
	shared := e.a.at(t, "GET", pcContentHost, "/v1/u/"+strings.Split(e.host, ".")[1]+"/sites/site/me", nil, map[string]string{"Origin": "https://" + pcContentHost, "Sec-Fetch-Site": "same-origin"})
	if shared.status != 200 || strings.Contains(string(shared.body), `"signed_in":true`) {
		t.Fatalf("content host /me: %d %s", shared.status, shared.body)
	}
}

// F3: dashboard Sign out ends this browser's family even when the cookie's
// pass came from an older key than the one being signed out.
func TestDashboardSignOutEndsCookieFamily(t *testing.T) {
	e := newReviewEnv(t, false)
	sessA, _ := e.a.finishSSO(t, e.host, e.pass)
	k2, err := auth.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AddSignInKey(context.Background(), e.a.database, e.uid, k2, "dashboard sign-in"); err != nil {
		t.Fatal(err)
	}
	r := e.a.at(t, "POST", e.a.appHost(), "/v1/me/sign-out", nil, map[string]string{"X-API-Key": k2, "Cookie": ssoCookieHost + "=" + e.pass, "Origin": "https://" + e.a.appHost(), "Sec-Fetch-Site": "same-origin"})
	if r.status != 204 {
		t.Fatalf("sign-out: %d %s", r.status, r.body)
	}
	id, _ := hex.DecodeString(sessA)
	if _, err := db.GetVisitorSession(context.Background(), e.a.database, id); err == nil {
		t.Fatal("site session from the browser's pass survived dashboard sign-out")
	}
	if me := e.a.at(t, "GET", e.a.appHost(), "/v1/me", nil, map[string]string{"X-API-Key": e.viewer.key}); me.status != 401 {
		t.Fatalf("the pass's parent key still works: %d", me.status)
	}
}

// F2: HEAD matches the GET hop route and gets the same prefetch check.
func TestHopHEADPrefetchMintsNothing(t *testing.T) {
	e := newReviewEnv(t, true)
	state, _ := e.a.ssoStartAt(t, e.host)
	e.a.at(t, "HEAD", e.a.appHost(), "/v1/sso/hop?state="+state, nil, map[string]string{"Cookie": ssoCookieHost + "=" + e.pass, "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document", "Sec-Purpose": "prefetch;prerender"})
	if n := e.sessions(t); n != 0 {
		t.Fatalf("HEAD prefetch minted %d sessions", n)
	}
}

// Image loads of /v1/sso/start from another page do not spend the per-IP
// allowance an honest sign-in needs.
func TestHopStartSubresourcesDoNotDrainLimit(t *testing.T) {
	e := newReviewEnv(t, false)
	e.o.ipLimiter.capacity = 20
	e.o.ipLimiter.rate = 0.2
	for i := 0; i < 30; i++ {
		e.a.at(t, "GET", e.host, "/v1/sso/start?return_to="+url.QueryEscape("https://"+e.host+"/"), nil, map[string]string{"Sec-Fetch-Dest": "image", "Sec-Fetch-Mode": "no-cors", "Sec-Fetch-Site": "cross-site"})
	}
	r := e.a.at(t, "GET", e.host, "/v1/sso/start?return_to="+url.QueryEscape("https://"+e.host+"/page"), nil, map[string]string{"Sec-Fetch-Dest": "document", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Site": "none"})
	if r.status == 429 {
		t.Fatal("honest start refused after 30 cross-site image loads")
	}
}

// SIM-348: an owner's allowed_origins cannot bring the content host back.
func TestAllowedOriginsCannotReaddContentHost(t *testing.T) {
	e := newReviewEnv(t, false)
	if _, err := e.a.database.Exec(`UPDATE sites SET allowed_origins=$2 WHERE id=$1`, e.site, "https://"+pcContentHost); err != nil {
		t.Fatal(err)
	}
	r := e.a.at(t, "GET", e.host, "/v1/sites/site/me", nil, map[string]string{"Origin": "https://" + pcContentHost, "Sec-Fetch-Site": "same-site"})
	if r.status != 403 || r.header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("listed content host: %d acao=%q", r.status, r.header.Get("Access-Control-Allow-Origin"))
	}
}
