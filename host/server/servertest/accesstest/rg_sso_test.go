package accesstest

import (
	"context"
	"database/sql"
	"net/url"
	"testing"
	"time"

	accountapp "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/accesshttp"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
)

// The public SSO endpoints are rate limited per client address: the second
// request inside a one-request budget answers 429 with Retry-After.
func TestSSOPublicRoutesRateLimited(t *testing.T) {
	for _, route := range []struct{ method, path string }{{"GET", "/v1/sso/hop"}, {"POST", "/v1/sso/consent"}, {"GET", "/v1/sso/signed-out"}, {"POST", "/v1/sso/signed-out"}} {
		t.Run(route.method+route.path, func(t *testing.T) {
			a, o := newSSOApp(t)
			o.IPLimiter.Capacity = 1
			o.IPLimiter.Rate = 0
			headers := map[string]string{"Origin": "https://" + a.AppHost(), "Sec-Fetch-Site": "same-origin", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document"}
			if r := a.At(t, route.method, a.AppHost(), route.path, nil, headers); r.Status == 429 {
				t.Fatal("first request limited")
			}
			if r := a.At(t, route.method, a.AppHost(), route.path, nil, headers); r.Status != 429 || r.Header.Get("Retry-After") == "" {
				t.Fatalf("missing rate limit: %d", r.Status)
			}
		})
	}
}

// A person with ordinary sign-in who keeps opening sites cannot pile up
// unfinished hand-offs: the ninth pending consent is refused with 429.
func TestSSORedteamPendingCap(t *testing.T) {
	a, _ := newSSOApp(t)
	owner := a.NewPerson(t, "capowner")
	site := a.Deploy(t, owner, "site")
	host := owner.SiteHost("site")
	pass := a.SSOPass(t, owner.Key)
	if err := accountsrepo.RememberSSOConsent(context.Background(), a.Database, owner.ID, site); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 9; i++ {
		state, _ := a.SSOStartAt(t, host)
		want := 302
		if i == 8 {
			want = 429
		}
		if got := a.SSOHop(t, state, pass); got.Status != want {
			t.Fatalf("pending %d: %d", i, got.Status)
		}
	}
}

// Issuing an SSO session is limited per address and per key, independently.
func TestSSOSessionIssuanceLimits(t *testing.T) {
	for _, by := range []string{"IP", "key"} {
		t.Run(by, func(t *testing.T) {
			a, o := newSSOApp(t)
			one, two := a.NewPerson(t, "limitone"), a.NewPerson(t, "limittwo")
			o.IPLimiter.Capacity = 1
			o.IPLimiter.Rate = 0
			o.SSOKeyLimiter.Capacity = 1
			o.SSOKeyLimiter.Rate = 0
			request := func(key, ip string) Resp {
				return a.At(t, "POST", a.AppHost(), "/v1/sso/session", nil, map[string]string{"X-API-Key": key, "X-Forwarded-For": ip, "Origin": "https://" + a.AppHost()})
			}
			if r := request(one.Key, "192.0.2.1"); r.Status != 204 {
				t.Fatalf("first: %d", r.Status)
			}
			key, ip := two.Key, "192.0.2.1"
			if by == "key" {
				key, ip = one.Key, "192.0.2.2"
			}
			if r := request(key, ip); r.Status != 429 || r.Header.Get("Retry-After") == "" {
				t.Fatalf("limit: %d", r.Status)
			}
		})
	}
}

// A key keeps one SSO pass: a valid pass is reused, and each replacement
// retires the previous one. (The concurrent-issuance half is not restored.)
func TestSSOSessionRetainsOnePassPerKey(t *testing.T) {
	a, _ := newSSOApp(t)
	p := a.NewPerson(t, "boundedpasses")
	pass := a.SSOPass(t, p.Key)
	r := a.At(t, "POST", a.AppHost(), "/v1/sso/session", nil, map[string]string{"X-API-Key": p.Key, "Cookie": accesshttp.SSOCookieHost + "=" + pass, "Origin": "https://" + a.AppHost()})
	if r.Status != 204 || r.Header.Get("Set-Cookie") != "" {
		t.Fatal("valid pass was not reused")
	}
	svc := accountapp.New(a.Database)
	lim := a.Access.Limits
	for i := 0; i < 3; i++ {
		raw, err := accesshttp.RandomHex(32)
		if err != nil {
			t.Fatal(err)
		}
		if err := svc.ReplaceSSOPass(context.Background(), accountsrepo.HashAPIKey(raw), accountsrepo.HashAPIKey(p.Key), "", lim.VisitorSessionTTL, lim.VisitorSessionIdle, lim.KeyIdleExpiry); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := a.Database.QueryRow(`SELECT count(*) FROM sso_passes WHERE parent_key_hash=$1`, accountsrepo.HashAPIKey(p.Key)).Scan(&n); err != nil || n != 1 {
		t.Fatalf("retained %d passes: %v", n, err)
	}
	if _, err := accountsrepo.GetSSOPass(context.Background(), a.Database, accountsrepo.HashAPIKey(pass), 180*24*time.Hour); err != sql.ErrNoRows {
		t.Fatal("replaced pass survived")
	}
}

// A company (OIDC) sign-in started from a site hop returns through the hop
// and ends in a site session.
func TestSSOCompanyProvider(t *testing.T) {
	a, o := newSSOApp(t)
	owner := a.NewPerson(t, "ssooidcowner")
	a.Deploy(t, owner, "portal")
	host := owner.SiteHost("portal")
	o.SignIn = accountapp.NewProviderSignIn(o.Accounts, map[string]accountapp.Provider{"google": FakeProvider{}, "oidc": SSOOIDCProvider{}})
	state, _ := a.SSOStartAt(t, host)
	ret := a.Srv.URL + "/v1/sso/hop?state=" + state + "&cn=" + NonceHash(TestNonce).String
	r := a.At(t, "GET", a.AppHost(), "/v1/auth/oauth/oidc?return_to="+url.QueryEscape(ret), nil, nil)
	if r.Status != 302 {
		t.Fatalf("company provider start: %d %s", r.Status, r.Body)
	}
	u, _ := url.Parse(r.Header.Get("Location"))
	r = a.At(t, "GET", a.AppHost(), "/v1/auth/oauth/oidc/callback?state="+u.Query().Get("state")+"&code=company-"+owner.Handle, nil, nil)
	if r.Status != 302 {
		t.Fatalf("OIDC callback: %d", r.Status)
	}
	u, _ = url.Parse(r.Header.Get("Location"))
	if u.Path != "/v1/sso/hop" {
		t.Fatalf("OIDC lost the hop: %s", u)
	}
	r = a.At(t, "POST", a.AppHost(), "/v1/auth/verify", map[string]string{"token": u.Query().Get("token"), "nonce": TestNonce, "sso_state": state}, nil)
	if r.Status != 200 {
		t.Fatalf("OIDC verify: %d %s", r.Status, r.Body)
	}
	pass := a.SSOPass(t, r.JSON(t)["api_key"].(string))
	a.FinishSSO(t, host, pass)
}
