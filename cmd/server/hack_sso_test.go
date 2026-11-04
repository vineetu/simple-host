package main

import (
	"context"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	accountapp "github.com/vsriram/simple-host/internal/app/accounts"
	"github.com/vsriram/simple-host/internal/config"
	db "github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/edition"
	"github.com/vsriram/simple-host/internal/handler"
)

func TestSSOHackEventAndTeamHosts(t *testing.T) {
	a := newTeamSiteApp(t)
	organiser, member := a.newPerson(t, "ssohackorg"), a.newPerson(t, "ssohackmember")
	event := a.makeEvent(t, organiser)
	a.join(t, event, member, organiser)
	team, _ := a.startTeam(t, event, "SSO team", member)
	markReady(t, a.certDir, event)
	wantTS(t, "team", a.deployTeam(t, team, a.teamKey(t, event, member), "team"), 201, "")
	wantTS(t, "event website", a.api(t, "PUT", "/v1/hack/events/"+event+"/website/files?create=1", deployBody("event"), organiser.key), 201, "")
	o := handler.NewOAuthHandler(a.database, accountapp.New(a.database), oauthSettings(config.Config{PublicBaseURL: a.srv.URL, SiteDomain: tsDomain, ContentHost: "sites." + tsDomain}, accountapp.New(a.database)), edition.ForKind(edition.Hack))
	o.SetPersonSiteResolver(a.sites.PersonReturnSite)
	o.SetPasscodeCheck(a.sites.PasscodeLetsIn)
	a.sites.SetSSOHop(o)
	o.Register(a.mux)
	pa := &composedSSOApp{a}
	pass := pa.ssoPass(t, member.key)
	for _, host := range []string{team + "." + event + "." + tsDomain, event + "." + tsDomain} {
		raw, _ := pa.finishSSO(t, host, pass)
		id, _ := hex.DecodeString(raw)
		sess, err := db.GetVisitorSession(context.Background(), a.database, id, 180*24*time.Hour)
		if err != nil || sess.Host != host {
			t.Fatalf("Hack site pass: %v", err)
		}
	}
}
func responseCookie(t *testing.T, r resp, name string) string {
	t.Helper()
	res := http.Response{Header: r.header}
	for _, c := range res.Cookies() {
		if c.Name == name {
			return c.Value
		}
	}
	t.Fatalf("missing %s cookie (status %d)", name, r.status)
	return ""
}
func (a *composedSSOApp) ssoPass(t *testing.T, key string) string {
	t.Helper()
	r := a.at(t, "POST", a.appHost(), "/v1/sso/session", nil, map[string]string{"X-API-Key": key, "Origin": "https://" + a.appHost(), "Sec-Fetch-Site": "same-origin"})
	if r.status != 204 {
		t.Fatalf("bootstrap: %d %s", r.status, r.body)
	}
	return responseCookie(t, r, ssoCookieHost)
}
func (a *composedSSOApp) ssoStartAt(t *testing.T, host string) (state, nonce string) {
	t.Helper()
	r := a.at(t, "GET", host, "/v1/sso/start?return_to="+url.QueryEscape("https://"+host+"/page?ok=1"), nil, nil)
	if r.status != 302 {
		t.Fatalf("start: %d %s", r.status, r.body)
	}
	u, _ := url.Parse(r.header.Get("Location"))
	return u.Query().Get("state"), responseCookie(t, r, visitorNonceCookieHost)
}
func (a *composedSSOApp) ssoHop(t *testing.T, state, pass string) resp {
	return a.at(t, "GET", a.appHost(), "/v1/sso/hop?state="+state, nil, map[string]string{"Cookie": ssoCookieHost + "=" + pass, "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document"})
}
func consentToken(t *testing.T, r resp) string {
	t.Helper()
	m := regexp.MustCompile(`name="form_token" value="([^"]+)"`).FindSubmatch(r.body)
	if len(m) != 2 {
		t.Fatalf("not consent: %d %s", r.status, r.body)
	}
	return string(m[1])
}
func (a *composedSSOApp) ssoConsent(t *testing.T, state, pass, token, origin, fetchSite string) resp {
	return a.at(t, "POST", a.appHost(), "/v1/sso/consent", url.Values{"state": {state}, "form_token": {token}}.Encode(), map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Cookie": ssoCookieHost + "=" + pass, "Origin": origin, "Sec-Fetch-Site": fetchSite})
}
func (a *composedSSOApp) finishSSO(t *testing.T, host, pass string) (string, string) {
	t.Helper()
	state, nonce := a.ssoStartAt(t, host)
	r := a.ssoHop(t, state, pass)
	if r.status == 200 {
		r = a.ssoConsent(t, state, pass, consentToken(t, r), "https://"+a.appHost(), "same-origin")
	}
	if r.status != 302 {
		t.Fatalf("hop: %d %s", r.status, r.body)
	}
	u, _ := url.Parse(r.header.Get("Location"))
	r = a.at(t, "GET", host, u.RequestURI(), nil, nonceCookie(nonce))
	if r.status != 302 {
		t.Fatalf("establish: %d %s", r.status, r.body)
	}
	if r.header.Get("Location") != "https://"+host+"/page?ok=1" {
		t.Fatalf("return changed: %s", r.header.Get("Location"))
	}
	return responseCookie(t, r, visitorCookieHost), state
}
func (a *composedSSOApp) at(t *testing.T, method, host, path string, body any, headers map[string]string) resp {
	t.Helper()
	var rd io.Reader
	if body != nil {
		if s, ok := body.(string); ok {
			rd = strings.NewReader(s)
		} else {
			rd = jsonBody(body)
		}
	}
	req, err := http.NewRequest(method, a.srv.URL+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	req.Header.Set("X-Forwarded-Proto", "https")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return resp{res.StatusCode, res.Header, b}
}
func nonceCookie(n string) map[string]string {
	return map[string]string{"Cookie": visitorNonceCookieHost + "=" + n}
}

type composedSSOApp struct{ *teamSiteApp }

func (a *composedSSOApp) appHost() string {
	u, _ := url.Parse(a.srv.URL)
	return u.Host
}

const ssoCookieHost = "__Host-sh_sso"
const visitorCookieHost = "__Host-sh_vsess"
const visitorNonceCookieHost = "__Host-sh_vnonce"
