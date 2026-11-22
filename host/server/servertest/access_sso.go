package servertest

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"testing"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/access/gate"
)

func ResponseCookie(t *testing.T, r Resp, name string) string {
	t.Helper()
	res := http.Response{Header: r.Header}
	for _, c := range res.Cookies() {
		if c.Name == name {
			return c.Value
		}
	}
	t.Fatalf("missing %s cookie (status %d)", name, r.Status)
	return ""
}
func (a *PrivateApp) SSOPass(t *testing.T, key string) string {
	t.Helper()
	r := a.At(t, "POST", a.AppHost(), "/v1/sso/session", nil, map[string]string{"X-API-Key": key, "Origin": "https://" + a.AppHost(), "Sec-Fetch-Site": "same-origin"})
	if r.Status != 204 {
		t.Fatalf("bootstrap: %d %s", r.Status, r.Body)
	}
	return ResponseCookie(t, r, auth.SSOCookieHost)
}
func (a *PrivateApp) SSOStartAt(t *testing.T, host string) (state, nonce string) {
	t.Helper()
	r := a.At(t, "GET", host, "/v1/sso/start?return_to="+url.QueryEscape("https://"+host+"/page?ok=1"), nil, nil)
	if r.Status != 302 {
		t.Fatalf("start: %d %s", r.Status, r.Body)
	}
	u, _ := url.Parse(r.Header.Get("Location"))
	return u.Query().Get("state"), ResponseCookie(t, r, gate.VisitorNonceCookieHost)
}
func (a *PrivateApp) SSOHop(t *testing.T, state, pass string) Resp {
	return a.At(t, "GET", a.AppHost(), "/v1/sso/hop?state="+state, nil, map[string]string{"Cookie": auth.SSOCookieHost + "=" + pass, "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document"})
}
func ConsentToken(t *testing.T, r Resp) string {
	t.Helper()
	m := regexp.MustCompile(`name="form_token" value="([^"]+)"`).FindSubmatch(r.Body)
	if len(m) != 2 {
		t.Fatalf("not consent: %d %s", r.Status, r.Body)
	}
	return string(m[1])
}
func (a *PrivateApp) SSOConsent(t *testing.T, state, pass, token, origin, fetchSite string) Resp {
	return a.At(t, "POST", a.AppHost(), "/v1/sso/consent", url.Values{"state": {state}, "form_token": {token}}.Encode(), map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Cookie": auth.SSOCookieHost + "=" + pass, "Origin": origin, "Sec-Fetch-Site": fetchSite})
}
func (a *PrivateApp) FinishSSO(t *testing.T, host, pass string) (string, string) {
	t.Helper()
	state, nonce := a.SSOStartAt(t, host)
	r := a.SSOHop(t, state, pass)
	if r.Status == 200 {
		r = a.SSOConsent(t, state, pass, ConsentToken(t, r), "https://"+a.AppHost(), "same-origin")
	}
	if r.Status != 302 {
		t.Fatalf("hop: %d %s", r.Status, r.Body)
	}
	u, _ := url.Parse(r.Header.Get("Location"))
	r = a.At(t, "GET", host, u.RequestURI(), nil, NonceCookie(nonce))
	if r.Status != 302 {
		t.Fatalf("establish: %d %s", r.Status, r.Body)
	}
	if r.Header.Get("Location") != "https://"+host+"/page?ok=1" {
		t.Fatalf("return changed: %s", r.Header.Get("Location"))
	}
	return ResponseCookie(t, r, gate.VisitorCookieHost), state
}

type SSOOIDCProvider struct{ FakeProvider }

func (SSOOIDCProvider) Name() string { return "oidc" }
func (SSOOIDCProvider) Exchange(ctx context.Context, code, verifier string) (gate.ProviderIdentity, error) {
	id, err := (FakeProvider{}).Exchange(ctx, code, verifier)
	id.Provider = "oidc"
	return id, err
}
