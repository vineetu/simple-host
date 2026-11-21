package accesstest

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/access/accesshttp"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
)

func TestVisitorSignInBoundToStartingBrowser(t *testing.T) {
	a := NewApp(t)
	a.WithOAuth(t)
	olive, oscar := a.NewPerson(t, "olive"), a.NewPerson(t, "oscar")
	// oscar has no site-host certificate yet: person-path fallback.
	rt.Deploy(t, a.Database, a.Disk, oscar, "cafe", nil)
	personHost := oscar.Handle + "." + rt.SiteDomain
	shopID := a.Deploy(t, olive, "shop")
	a.Deploy(t, olive, "blog")
	a.Deploy(t, olive, "pots")
	oh := olive.Handle
	siteHost := olive.SiteHost("shop")
	claimed := "blog-" + oh + "." + rt.SiteDomain
	custom := "pots-" + oh + ".example.test"
	for site, domain := range map[string]string{"blog": claimed, "pots": custom} {
		if _, err := a.Database.Exec(`UPDATE sites SET custom_domain=$3, domain_verified_at=now(), domain_status='active' WHERE user_id=$1 AND name=$2`, olive.ID, site, domain); err != nil {
			t.Fatal(err)
		}
	}

	for _, tc := range []struct{ name, host, site, path string }{
		{"site host", siteHost, "shop", "/page.html?x=1"},
		{"claimed name", claimed, "blog", "/page.html?x=1"},
		{"custom domain", custom, "pots", "/page.html?x=1"},
		{"person host fallback", personHost, "cafe", "/cafe/page.html?x=1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host := tc.host
			returnTo := "https://" + host + tc.path
			// Each case is its own client, clear of the establish rate limit.
			ip := "198.51.100." + map[string]string{"shop": "1", "blog": "2", "pots": "3", "cafe": "4"}[tc.site]
			establish := func(loc *url.URL, headers map[string]string) Resp {
				h := map[string]string{"X-Forwarded-For": ip}
				for k, v := range headers {
					h[k] = v
				}
				return a.At(t, "GET", host, loc.RequestURI(), nil, h)
			}
			me := func(sess string) map[string]any {
				return a.At(t, "GET", host, "/v1/sites/"+tc.site+"/me", nil, rt.Browser(host, sess)).JSON(t)
			}

			// Attacker starts and completes sign-in in their own browser, then
			// gets the victim's browser (no nonce) to open the establish link.
			state, attackerNonce := a.SignInStart(t, host, returnTo)
			loc := a.SignInCallback(t, state, "attacker", host)
			if r := establish(loc, nil); r.Status != http.StatusBadRequest || len(r.Header.Values("Set-Cookie")) != 0 {
				t.Fatalf("no nonce accepted: %d %v", r.Status, r.Header.Values("Set-Cookie"))
			}
			// Burned: not even the attacker's own browser can use it now.
			if r := establish(loc, NonceCookie(attackerNonce)); r.Status != http.StatusBadRequest || len(r.Header.Values("Set-Cookie")) != 0 {
				t.Fatalf("burned code redeemed: %d", r.Status)
			}

			// Victim holds a nonce of their own sign-in: still refused.
			_, victimNonce := a.SignInStart(t, host, returnTo)
			state, attackerNonce = a.SignInStart(t, host, returnTo)
			loc = a.SignInCallback(t, state, "attacker", host)
			if r := establish(loc, NonceCookie(victimNonce)); r.Status != http.StatusBadRequest || len(r.Header.Values("Set-Cookie")) != 0 {
				t.Fatalf("wrong nonce accepted: %d %v", r.Status, r.Header.Values("Set-Cookie"))
			}
			if r := establish(loc, NonceCookie(attackerNonce)); r.Status != http.StatusBadRequest {
				t.Fatalf("code reusable after a wrong nonce: %d", r.Status)
			}

			// A plain (non-__Host-) cookie a sibling host could plant does not count.
			state, attackerNonce = a.SignInStart(t, host, returnTo)
			loc = a.SignInCallback(t, state, "attacker", host)
			if r := establish(loc, map[string]string{"Cookie": accesshttp.VisitorNonceCookieHTTP + "=" + attackerNonce}); r.Status != http.StatusBadRequest || len(r.Header.Values("Set-Cookie")) != 0 {
				t.Fatalf("planted plain nonce accepted: %d", r.Status)
			}

			// The real flow: same browser from start to establish.
			state, nonce := a.SignInStart(t, host, returnTo)
			loc = a.SignInCallback(t, state, "visitor", host)
			r := establish(loc, NonceCookie(nonce))
			if r.Status != http.StatusFound || r.Header.Get("Location") != returnTo {
				t.Fatalf("real establish: %d %q %s", r.Status, r.Header.Get("Location"), r.Body)
			}
			var sess string
			cleared := false
			for _, c := range r.Header.Values("Set-Cookie") {
				if strings.HasPrefix(c, accesshttp.VisitorCookieHost+"=") {
					sess = strings.TrimPrefix(strings.SplitN(c, ";", 2)[0], accesshttp.VisitorCookieHost+"=")
				}
				if strings.HasPrefix(c, accesshttp.VisitorNonceCookieHost+"=;") && strings.Contains(c, "Max-Age=0") {
					cleared = true
				}
			}
			if sess == "" || !cleared {
				t.Fatalf("establish cookies: %v", r.Header.Values("Set-Cookie"))
			}
			if m := me(sess); m["signed_in"] != true || m["email"] != "visitor@example.test" {
				t.Fatalf("/me after sign-in: %v", m)
			}
			// /me sees the session storage sees: never the plain cookie, never
			// a cross-origin request.
			plain := rt.Browser(host, "")
			plain["Cookie"] = accesshttp.VisitorCookieHTTP + "=" + sess
			for _, h := range []map[string]string{plain, rt.Browser(host, sess, "Origin", "https://elsewhere.test", "Sec-Fetch-Site", "cross-site")} {
				if m := a.At(t, "GET", host, "/v1/sites/"+tc.site+"/me", nil, h).JSON(t); m["signed_in"] == true {
					t.Fatalf("/me signed in without the storage session: %v %v", h, m)
				}
			}
			if r := establish(loc, NonceCookie(nonce)); r.Status != http.StatusBadRequest {
				t.Fatalf("code reused: %d", r.Status)
			}
		})
	}

	// The apex start sends a site sign-in to the site host's start; it sets
	// no cookie and issues no state itself.
	returnTo := "https://" + siteHost + "/a?b=c"
	r := a.At(t, "GET", rt.SiteDomain, "/v1/auth/oauth/google?return_to="+url.QueryEscape(returnTo), nil, nil)
	if want := "https://" + siteHost + "/v1/visitor/oauth/google?return_to=" + url.QueryEscape(returnTo); r.Status != http.StatusFound || r.Header.Get("Location") != want || len(r.Header.Values("Set-Cookie")) != 0 {
		t.Fatalf("apex start: %d %q %v", r.Status, r.Header.Get("Location"), r.Header.Values("Set-Cookie"))
	}
	// The site-host start refuses a return_to on another host, and owner flows.
	for _, bad := range []string{"https://" + claimed + "/", "https://" + rt.SiteDomain + "/?cn=" + NonceHash("x").String, "https://evil.example/"} {
		if r := a.At(t, "GET", siteHost, "/v1/visitor/oauth/google?return_to="+url.QueryEscape(bad), nil, nil); r.Status != http.StatusBadRequest || len(r.Header.Values("Set-Cookie")) != 0 {
			t.Errorf("start with return_to %s: %d", bad, r.Status)
		}
	}
	siteID := shopID
	// An offline site starts no sign-in.
	if _, err := a.Database.Exec(`UPDATE sites SET offline_at = now() WHERE id = $1`, siteID); err != nil {
		t.Fatal(err)
	}
	if r := a.At(t, "GET", siteHost, "/v1/visitor/oauth/google?return_to="+url.QueryEscape(returnTo), nil, nil); r.Status != http.StatusForbidden || r.Code() != "site_offline" {
		t.Fatalf("sign-in start while offline: %d %s", r.Status, r.Body)
	}
	if _, err := a.Database.Exec(`UPDATE sites SET offline_at = NULL WHERE id = $1`, siteID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Database.Exec(`INSERT INTO oauth_states (state, provider, code_verifier, return_to, host, site_id, purpose, expires_at) VALUES ('legacy-'||$1, 'google', 'v', $2, $3, $4, 'site', $5)`,
		oh, returnTo, siteHost, siteID, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if r := a.At(t, "GET", rt.SiteDomain, "/v1/auth/oauth/google/callback?state=legacy-"+oh+"&code=attacker", nil, nil); r.Status != http.StatusBadRequest {
		t.Fatalf("unbound state completed: %d %q", r.Status, r.Header.Get("Location"))
	}
}
