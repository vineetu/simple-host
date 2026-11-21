package accesstest

import (
	"context"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	accountapp "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/accesshttp"
	"github.com/vsriram/simple-host/host/access/auth"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
)

type ssoReviewEnv struct {
	a                     *App
	host, site, uid, pass string
	viewer                Person
}

func newSSOReviewEnv(t *testing.T, consent bool) ssoReviewEnv {
	a, _ := newSSOApp(t)
	owner := a.NewPerson(t, "revowner")
	viewer := a.NewPerson(t, "revviewer")
	site := a.Deploy(t, owner, "site")
	host := owner.SiteHost("site")
	uid := viewer.ID
	pass := a.SSOPass(t, viewer.Key)
	if consent {
		if err := accountsrepo.RememberSSOConsent(context.Background(), a.Database, uid, site); err != nil {
			t.Fatal(err)
		}
	}
	return ssoReviewEnv{a, host, site, uid, pass, viewer}
}

func (e ssoReviewEnv) sessions(t *testing.T) int {
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
	e := newSSOReviewEnv(t, false)
	sess, _ := e.a.FinishSSO(t, e.host, e.pass)
	me := "/v1/sites/site/me"
	cookie := accesshttp.VisitorCookieHost + "=" + sess
	for _, h := range []map[string]string{
		{"Origin": "https://" + rt.ContentHost, "Sec-Fetch-Site": "same-site", "Cookie": cookie},
		{"Referer": "https://" + rt.ContentHost + "/x/y/", "Sec-Fetch-Site": "same-site", "Cookie": cookie},
		{"Origin": "https://" + rt.ContentHost, "Cookie": cookie},
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
	shared := e.a.At(t, "GET", rt.ContentHost, "/v1/u/"+strings.Split(e.host, ".")[1]+"/sites/site/me", nil, map[string]string{"Origin": "https://" + rt.ContentHost, "Sec-Fetch-Site": "same-origin"})
	if shared.Status != 200 || strings.Contains(string(shared.Body), `"signed_in":true`) {
		t.Fatalf("content host /me: %d %s", shared.Status, shared.Body)
	}
}

// F3: dashboard Sign out ends this browser's family even when the cookie's
// pass came from an older key than the one being signed out.
func TestDashboardSignOutEndsCookieFamily(t *testing.T) {
	e := newSSOReviewEnv(t, false)
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
	e := newSSOReviewEnv(t, true)
	state, _ := e.a.SSOStartAt(t, e.host)
	e.a.At(t, "HEAD", e.a.AppHost(), "/v1/sso/hop?state="+state, nil, map[string]string{"Cookie": accesshttp.SSOCookieHost + "=" + e.pass, "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document", "Sec-Purpose": "prefetch;prerender"})
	if n := e.sessions(t); n != 0 {
		t.Fatalf("HEAD prefetch minted %d sessions", n)
	}
}

// SIM-348: an owner's allowed_origins cannot bring the content host back.
func TestAllowedOriginsCannotReaddContentHost(t *testing.T) {
	e := newSSOReviewEnv(t, false)
	if _, err := e.a.Database.Exec(`UPDATE sites SET allowed_origins=$2 WHERE id=$1`, e.site, "https://"+rt.ContentHost); err != nil {
		t.Fatal(err)
	}
	r := e.a.At(t, "GET", e.host, "/v1/sites/site/me", nil, map[string]string{"Origin": "https://" + rt.ContentHost, "Sec-Fetch-Site": "same-site"})
	if r.Status != 403 || r.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("listed content host: %d acao=%q", r.Status, r.Header.Get("Access-Control-Allow-Origin"))
	}
}
