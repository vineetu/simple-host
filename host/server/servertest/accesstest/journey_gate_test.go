package accesstest

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"

	"github.com/vsriram/simple-host/host/access/gate"

	. "github.com/vsriram/simple-host/host/access/featuretest"
	model "github.com/vsriram/simple-host/host/model"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"

	accountapp "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/auth"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
)

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
	r := e.a.At(t, "POST", e.a.AppHost(), "/v1/me/sign-out", nil, map[string]string{"X-API-Key": k2, "Cookie": auth.SSOCookieHost + "=" + e.pass, "Origin": "https://" + e.a.AppHost(), "Sec-Fetch-Site": "same-origin"})
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

// SiteGate on a page: take-down and offline win over everything.
func TestGateSitePageHolds(t *testing.T) {
	g := newGateSite(t)
	admin := map[string]string{"X-API-Key": g.Admin}
	page := func() Resp { return g.At(t, "GET", g.Host, "/", nil, nil) }
	gateWant(t, "live", page(), 200, "")

	gateWant(t, "suspend", g.At(t, "POST", rt.SiteDomain, "/v1/admin/sites/"+g.SiteID+"/suspend", map[string]string{"reason": "abuse"}, admin), 200, "")
	if r := page(); r.Status != http.StatusGone {
		t.Fatalf("taken down: %d %s", r.Status, r.Body)
	}
	g.At(t, "POST", rt.SiteDomain, "/v1/admin/sites/"+g.SiteID+"/restore", nil, admin)
	gateWant(t, "restored", page(), 200, "")

	if err := g.Disk.SetOffline(g.Ann.ID, "notes", true); err != nil {
		t.Fatal(err)
	}
	if r := page(); r.Status != http.StatusServiceUnavailable {
		t.Fatalf("offline: %d %s", r.Status, r.Body)
	}
	if err := g.Disk.SetOffline(g.Ann.ID, "notes", false); err != nil {
		t.Fatal(err)
	}
	gateWant(t, "back online", page(), 200, "")
}

// Who the data routes are told is calling.
func TestGateStorageCaller(t *testing.T) {
	g := newGateSite(t)
	g.Deploy(t, g.Bob, "board")
	sess := g.Session(t, g.Bob, g.SiteID, g.Host)
	pass := gatePass(t, g.Bob, g.SiteID, g.Host)
	boardHost := g.Bob.SiteHost("board")
	for _, tc := range []struct {
		name, host string
		h          map[string]string
		status     int
		ok         func(CallJSON) bool
	}{
		{"owner key", rt.SiteDomain, g.Ann.Header(), 200, func(c CallJSON) bool { return c.Owner && c.SiteID == g.SiteID && c.AccountID == g.Ann.ID }},
		{"another account's key", rt.SiteDomain, g.Bob.Header(), 403, nil},
		{"admin key", rt.SiteDomain, map[string]string{"X-API-Key": g.Admin}, 200, func(c CallJSON) bool { return c.Owner }},
		{"no key on the apex", rt.SiteDomain, nil, 403, nil},
		{"no key on the content host", rt.ContentHost, rt.Browser(rt.ContentHost, ""), 403, nil},
		{"a browser from another origin", g.Host, rt.Browser(g.Host, "", "Origin", "https://evil.example"), 403, nil},
		{"a browser on the site's host", g.Host, rt.Browser(g.Host, ""), 200, func(c CallJSON) bool { return !c.Owner && c.VisitorID == "" && c.PasscodeOK && c.ViewersOK }},
		{"a visitor session", g.Host, rt.Browser(g.Host, sess), 200, func(c CallJSON) bool { return !c.Owner && c.VisitorID == g.Bob.ID && c.OnOwnAddress }},
		{"a visitor pass", g.Host, map[string]string{gate.VisitorPassHeader: pass, "Origin": "https://" + g.Host}, 200, func(c CallJSON) bool { return c.VisitorAI && !c.Owner }},
		{"the pass on another site", boardHost, map[string]string{gate.VisitorPassHeader: pass, "Origin": "https://" + boardHost}, 403, nil},
	} {
		site := "notes"
		if tc.host == boardHost {
			site = "board"
		}
		r, c := g.Probe(t, tc.host, site, tc.h)
		if r.Status != tc.status || (tc.ok != nil && !tc.ok(c)) {
			t.Fatalf("%s: %d %s", tc.name, r.Status, r.Body)
		}
	}

	// An editor's key with owner=<account> acts for the site, its file links
	// scoped to the editor; a non-editor's key with owner= is refused. (The
	// probe also answers on storage/usage, a route editors may use.)
	bea := teamNewEditor(t, g.App, g.Ann, "notes")
	asFor := func(p Person) (Resp, CallJSON) {
		r := g.At(t, "GET", rt.SiteDomain, "/v1/sites/notes/storage/usage?owner="+g.Ann.Handle, nil, p.Header())
		var c CallJSON
		_ = json.Unmarshal(r.Body, &c)
		return r, c
	}
	if r, c := asFor(bea); r.Status != 200 || !c.Owner || c.SiteID != g.SiteID || c.LinkScope != "as:"+bea.ID {
		t.Fatalf("editor's key: %d %s", r.Status, r.Body)
	}
	if r, _ := asFor(g.Bob); r.Status < 400 {
		t.Fatalf("non-editor's key: %d %s", r.Status, r.Body)
	}
}

// Operator holds and offline beat every caller but the owner (offline only).
func TestGateStorageCallerHolds(t *testing.T) {
	g := newGateSite(t)
	admin := map[string]string{"X-API-Key": g.Admin}
	visitor := rt.Browser(g.Host, "")
	probe := func(what string, h map[string]string, host string, status int, code string) {
		t.Helper()
		r, _ := g.Probe(t, host, "notes", h)
		gateWant(t, what, r, status, code)
	}

	// Offline: visitors refused, the owner not.
	if err := sitesrepo.SetSiteOffline(context.Background(), g.Database, g.SiteID, true); err != nil {
		t.Fatal(err)
	}
	probe("offline visitor", visitor, g.Host, 403, "site_offline")
	probe("offline owner", g.Ann.Header(), rt.SiteDomain, 200, "")
	if err := sitesrepo.SetSiteOffline(context.Background(), g.Database, g.SiteID, false); err != nil {
		t.Fatal(err)
	}

	// A suspended site: everyone, the owner included.
	gateWant(t, "suspend site", g.At(t, "POST", rt.SiteDomain, "/v1/admin/sites/"+g.SiteID+"/suspend", map[string]string{"reason": "abuse"}, admin), 200, "")
	probe("suspended site owner", g.Ann.Header(), rt.SiteDomain, 403, "site_suspended")
	probe("suspended site visitor", visitor, g.Host, 403, "site_suspended")
	gateWant(t, "restore site", g.At(t, "POST", rt.SiteDomain, "/v1/admin/sites/"+g.SiteID+"/restore", nil, admin), 200, "")
	probe("restored visitor", visitor, g.Host, 200, "")

	// A suspended account: its key and its site's visitors.
	gateWant(t, "suspend account", g.At(t, "POST", rt.SiteDomain, "/v1/admin/users/"+g.Ann.ID+"/suspend", map[string]string{"reason": "abuse"}, admin), 200, "")
	if r, _ := g.Probe(t, rt.SiteDomain, "notes", g.Ann.Header()); r.Status == 200 {
		t.Fatalf("suspended account's key: %d %s", r.Status, r.Body)
	}
	if r, _ := g.Probe(t, g.Host, "notes", visitor); r.Status == 200 {
		t.Fatalf("suspended account's site: %d %s", r.Status, r.Body)
	}
}

// The gate's key check is accounts.ActsAsOwner: a member of the team that
// holds a site passes with their own key, never at someone else's site. An
// expired key on a data route is 401 key_expired, not 500.
func TestPasscodeKeyIsActsAsOwner(t *testing.T) {
	p := NewPasscodeSite(t)
	team := NewTeam(t, p.Base, p.Olive, "pcteam")
	m := p.NewPerson(t, "pcmember")
	AddMember(t, p.Base, p.Olive, team, m)
	req := httptest.NewRequest("GET", "https://"+p.Host+"/", nil)
	req.Header.Set("X-API-Key", m.Key)
	if owner, _ := p.Access.KeyActsFor(req, model.SitePasscodeRow{UserID: TeamPerson(t, p.Base, team).ID, Name: "site"}); !owner {
		t.Fatal("team member's own key refused at the team's gate")
	}
	if owner, admin := p.Access.KeyActsFor(req, model.SitePasscodeRow{UserID: p.Olive.ID, Name: "trip"}); owner || admin {
		t.Fatal("member key passed someone else's gate")
	}
	if _, err := p.Database.Exec(`UPDATE api_keys SET expires_at = now() - interval '1 second' WHERE user_id = $1`, p.Olive.ID); err != nil {
		t.Fatal(err)
	}
	if r, _ := p.Probe(t, rt.SiteDomain, "trip", p.Okey); r.Status != http.StatusUnauthorized || r.Code() != "key_expired" {
		t.Fatalf("expired key on saved data: %d %s", r.Status, r.Body)
	}
}

// An editor cannot manage editors: a direct add or remove by an editor is
// refused as off their routes, and the list is unchanged.
func TestRkEditorCannotManageEditors(t *testing.T) {
	a := NewApp(t)
	ann := a.NewPerson(t, "editor-owner")
	a.Deploy(t, ann, "shop")
	bea := teamNewEditor(t, a, ann, "shop")
	cal := a.NewPerson(t, "editor-wannabe")
	for name, r := range map[string]Resp{
		"add":    teamEditors(t, a, bea, "POST", "shop", ann.Handle, cal.Handle),
		"remove": teamEditors(t, a, bea, "DELETE", "shop", ann.Handle, bea.Handle),
	} {
		if r.Status != http.StatusForbidden || r.Code() != "editor_scope" {
			t.Fatalf("editor %s: %d %s", name, r.Status, r.Body)
		}
	}
	r := teamEditors(t, a, ann, "GET", "shop", "", "")
	if r.Status != http.StatusOK || len(r.JSON(t)["editors"].([]any)) != 1 {
		t.Fatalf("editors after the refused changes: %d %s", r.Status, r.Body)
	}
}

// An invitation that has expired cannot be accepted even by its known id.
func TestRkExpiredInvitationCannotBeAccepted(t *testing.T) {
	a := NewApp(t)
	ann, dee := a.NewPerson(t, "team-exp"), a.NewPerson(t, "team-exp-d")
	team := NewTeam(t, a.Base, ann, "expiry")
	Invite(t, a.Base, ann, team, dee.Email)
	id := InvitationTo(t, a.Base, dee, team)
	if id == "" {
		t.Fatal("no invitation to expire")
	}
	if _, err := a.Database.Exec(`UPDATE team_invitations SET expires_at = now() - interval '1 second' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if r := Answer(t, a.Base, dee, id, "accept"); r.Status != http.StatusNotFound || r.Code() != "invitation_not_found" {
		t.Fatalf("accepting an expired invitation by id: %d %s", r.Status, r.Body)
	}
	if n := rkMemberCount(t, a, team); n != 1 {
		t.Fatalf("members after the refused accept: %d", n)
	}
}

// A company (OIDC) sign-in started from a site hop returns through the hop
// and ends in a site session.
func TestSSOCompanyProvider(t *testing.T) {
	a, o := newSSOApp(t)
	owner := a.NewPerson(t, "ssooidcowner")
	a.Deploy(t, owner, "portal")
	host := owner.SiteHost("portal")
	o.SignIn = gate.NewProviderSignIn(o.Accounts, map[string]gate.Provider{"google": FakeProvider{}, "oidc": SSOOIDCProvider{}})
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

func TestSSOHopConsentAndRevocation(t *testing.T) {
	a, _ := newSSOApp(t)
	owner := a.NewPerson(t, "ssoowner")
	viewer := a.NewPerson(t, "ssoviewer")
	a.Deploy(t, owner, "one")
	a.Deploy(t, owner, "two")
	handle := owner.Handle
	one, two := "one."+handle+"."+rt.SiteDomain, "two."+handle+"."+rt.SiteDomain
	pass := a.SSOPass(t, viewer.Key)
	state, nonce := a.SSOStartAt(t, one)
	r := a.SSOHop(t, state, pass)
	form := ConsentToken(t, r)
	if !strings.Contains(r.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'") || r.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("consent privacy headers")
	}
	for _, tc := range []struct{ name, token, origin, site string }{{"wrong token", "bad", "https://" + a.AppHost(), "same-origin"}, {"sibling", form, "https://" + two, "same-site"}, {"opaque", form, "null", "cross-site"}, {"no origin", form, "", ""}} {
		t.Run(tc.name, func(t *testing.T) {
			r := a.SSOConsent(t, state, pass, tc.token, tc.origin, tc.site)
			if r.Status != 403 {
				t.Fatalf("CSRF accepted: %d", r.Status)
			}
		})
	}
	r = a.SSOConsent(t, state, pass, form, "null", "same-origin")
	if r.Status != 302 {
		t.Fatalf("consent: %d %s", r.Status, r.Body)
	}
	u, _ := url.Parse(r.Header.Get("Location"))
	r = a.At(t, "GET", one, u.RequestURI(), nil, NonceCookie(nonce))
	if r.Status != 302 {
		t.Fatalf("establish: %d %s", r.Status, r.Body)
	}
	first := ResponseCookie(t, r, gate.VisitorCookieHost)
	uid := viewer.ID
	site := ssoSiteID(t, a, owner, "one")
	denied := a.At(t, "DELETE", a.AppHost(), "/v1/sso/consents/"+site, nil, map[string]string{"Cookie": auth.SSOCookieHost + "=" + pass, "Origin": "https://" + a.AppHost(), "Sec-Fetch-Site": "same-origin"})
	if denied.Status != 401 {
		t.Fatal("cookie revoked consent without an account key")
	}
	forgotten := a.At(t, "DELETE", a.AppHost(), "/v1/sso/consents/"+site, nil, map[string]string{"X-API-Key": viewer.Key, "Origin": "https://" + a.AppHost(), "Sec-Fetch-Site": "same-origin"})
	if forgotten.Status != 204 {
		t.Fatalf("forget consent: %d %s", forgotten.Status, forgotten.Body)
	}
	stateAgain, _ := a.SSOStartAt(t, one)
	if page := a.SSOHop(t, stateAgain, pass); page.Status != 200 {
		t.Fatal("revoked consent silently reused")
	}
	if err := accountsrepo.RememberSSOConsent(context.Background(), a.Database, uid, site); err != nil {
		t.Fatal(err)
	}
	if r := a.SSOHop(t, state, pass); r.Status != 400 {
		t.Fatal("state replay")
	}
	state, _ = a.SSOStartAt(t, one)
	if r := a.SSOHop(t, state, pass); r.Status != 302 {
		t.Fatal("consent not remembered")
	}
	second, _ := a.FinishSSO(t, one, pass)
	third, _ := a.FinishSSO(t, two, pass)
	if first == second || second == third {
		t.Fatal("session fixation")
	}
	for _, raw := range []string{first, second, third} {
		id, _ := hex.DecodeString(raw)
		s, err := accountsrepo.GetVisitorSession(context.Background(), a.Database, id, 180*24*time.Hour)
		if err != nil || s.SSOPassHash.String != accountsrepo.HashAPIKey(pass) {
			t.Fatalf("parent: %v", err)
		}
	}
	r = a.At(t, "POST", one, "/v1/visitor/logout", nil, map[string]string{"Cookie": gate.VisitorCookieHost + "=" + second, "X-SH-CSRF": "1", "Origin": "https://" + one, "Sec-Fetch-Site": "same-origin"})
	if r.Status != 200 {
		t.Fatalf("logout: %d %s", r.Status, r.Body)
	}
	dest := r.JSON(t)["redirect_url"].(string)
	u, _ = url.Parse(dest)
	r = a.At(t, "GET", a.AppHost(), u.RequestURI(), nil, map[string]string{"Cookie": auth.SSOCookieHost + "=" + pass, "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document"})
	if r.Status != 200 || ResponseCookie(t, r, auth.SSOCookieHost) != "" {
		t.Fatal("apex cookie not cleared")
	}
	for _, raw := range []string{first, second, third} {
		id, _ := hex.DecodeString(raw)
		if _, err := accountsrepo.GetVisitorSession(context.Background(), a.Database, id, 180*24*time.Hour); err != sql.ErrNoRows {
			t.Fatal("sibling session survived")
		}
	}
	if r := a.At(t, "GET", a.AppHost(), "/v1/me", nil, map[string]string{"X-API-Key": viewer.Key}); r.Status != 401 {
		t.Fatalf("dashboard key survived: %d", r.Status)
	}
}

func TestSSONamedViewerAutoHopAndParentExpiry(t *testing.T) {
	a, _ := newSSOApp(t)
	owner := a.NewPerson(t, "namedowner")
	viewer := a.NewPerson(t, "namedviewer")
	a.Deploy(t, owner, "named")
	h := owner.Handle
	host := "named." + h + "." + rt.SiteDomain
	site := ssoSiteID(t, a, owner, "named")
	uid := viewer.ID
	if _, err := a.Database.Exec(`INSERT INTO site_viewers(site_id,email) VALUES($1,$2)`, site, viewer.Email); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Database.Exec(`UPDATE sites SET access='specific' WHERE id=$1`, site); err != nil {
		t.Fatal(err)
	}
	ownerID := owner.ID
	if err := a.Disk.SetPasscodeMarker(ownerID, "named", true); err != nil {
		t.Fatal(err)
	}
	r := a.At(t, "GET", host, "/", nil, nil)
	if r.Status != 302 || !strings.Contains(r.Header.Get("Location"), "/v1/sso/hop?") {
		t.Fatalf("auto hop: %d %s", r.Status, r.Body)
	}
	pass := a.SSOPass(t, viewer.Key)
	u, _ := url.Parse(r.Header.Get("Location"))
	if r = a.SSOHop(t, u.Query().Get("state"), pass); r.Status != 200 {
		t.Fatalf("named viewer skipped consent: %d", r.Status)
	}
	raw, _ := a.FinishSSO(t, host, pass)
	id, _ := hex.DecodeString(raw)
	for _, q := range []string{`UPDATE sso_passes SET idle_expires_at=now()-interval '1 second' WHERE user_id=$1`, `UPDATE api_keys SET expires_at=now()-interval '1 second' WHERE user_id=$1`, `UPDATE users SET suspended_at=now() WHERE id=$1`} {
		if _, err := a.Database.Exec(q, uid); err != nil {
			t.Fatal(err)
		}
		if _, err := accountsrepo.GetSSOPass(context.Background(), a.Database, accountsrepo.HashAPIKey(pass), 180*24*time.Hour); err != sql.ErrNoRows {
			t.Fatal("expired/suspended pass accepted")
		}
		if _, err := accountsrepo.GetVisitorSession(context.Background(), a.Database, id, 180*24*time.Hour); err != sql.ErrNoRows {
			t.Fatal("expired/suspended parent accepted on site")
		}
		a.Database.Exec(`UPDATE sso_passes SET idle_expires_at=now()+interval '1 day' WHERE user_id=$1`, uid)
		a.Database.Exec(`UPDATE api_keys SET expires_at=NULL WHERE user_id=$1`, uid)
		a.Database.Exec(`UPDATE users SET suspended_at=NULL WHERE id=$1`, uid)
	}
	r = a.At(t, "POST", a.AppHost(), "/v1/me/sign-out", nil, map[string]string{"X-API-Key": viewer.Key})
	if r.Status != 204 {
		t.Fatalf("dashboard logout: %d", r.Status)
	}
	if _, err := accountsrepo.GetVisitorSession(context.Background(), a.Database, id, 180*24*time.Hour); err != sql.ErrNoRows {
		t.Fatal("dashboard logout left a site session")
	}
}

func TestSSOProviderAndEmailSignup(t *testing.T) {
	a, _ := newSSOApp(t)
	owner := a.NewPerson(t, "providerowner")
	a.Deploy(t, owner, "page")
	handle := owner.Handle
	host := "page." + handle + "." + rt.SiteDomain
	state, _ := a.SSOStartAt(t, host)
	ret := a.Srv.URL + "/v1/sso/hop?state=" + state + "&cn=" + NonceHash(TestNonce).String
	r := a.At(t, "GET", a.AppHost(), "/v1/auth/oauth/google?return_to="+url.QueryEscape(ret), nil, nil)
	if r.Status != 302 {
		t.Fatalf("provider start: %d %s", r.Status, r.Body)
	}
	u, _ := url.Parse(r.Header.Get("Location"))
	r = a.At(t, "GET", a.AppHost(), "/v1/auth/oauth/google/callback?state="+u.Query().Get("state")+"&code=sso-provider-"+handle, nil, nil)
	if r.Status != 302 {
		t.Fatalf("provider callback: %d %s", r.Status, r.Body)
	}
	u, _ = url.Parse(r.Header.Get("Location"))
	if u.Path != "/v1/sso/hop" || u.Query().Get("state") != state {
		t.Fatal("provider lost hop")
	}
	token := u.Query().Get("token")
	r = a.At(t, "POST", a.AppHost(), "/v1/auth/verify", map[string]string{"token": token, "nonce": TestNonce, "sso_state": state, "name": "dashboard sign-in"}, nil)
	if r.Status != 200 {
		t.Fatalf("provider verify: %d %s", r.Status, r.Body)
	}
	pass := a.SSOPass(t, r.JSON(t)["api_key"].(string))
	p, err := accountsrepo.GetSSOPass(context.Background(), a.Database, accountsrepo.HashAPIKey(pass), 180*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	var source string
	err = a.Database.QueryRow(`SELECT signup_source FROM users WHERE id=$1`, p.UserID).Scan(&source)
	if err != nil || source != "visitor_sso" {
		t.Fatalf("source: %s %v", source, err)
	}
	var sites int
	a.Database.QueryRow(`SELECT count(*) FROM sites WHERE user_id=$1`, p.UserID).Scan(&sites)
	if sites != 0 {
		t.Fatal("visitor got sites")
	}
	owners, err := sitesrepo.AccountsWithSites(context.Background(), a.Database, []string{p.UserID})
	if err != nil {
		t.Fatal(err)
	}
	if owners[p.UserID] {
		t.Fatal("visitor counted as signup with a site")
	}
}

func TestSSOReplacementRetiresBrowserFamily(t *testing.T) {
	for _, switchPerson := range []bool{false, true} {
		t.Run(map[bool]string{false: "same account", true: "another account"}[switchPerson], func(t *testing.T) {
			a, _ := newSSOApp(t)
			owner := a.NewPerson(t, "replacementowner")
			viewer := a.NewPerson(t, "replacementviewer")
			a.Deploy(t, owner, "page")
			handle := owner.Handle
			host := "page." + handle + "." + rt.SiteDomain
			pass := a.SSOPass(t, viewer.Key)
			oldSession, _ := a.FinishSSO(t, host, pass)
			pendingState, _ := a.SSOStartAt(t, host)
			pending := a.SSOHop(t, pendingState, pass)
			if pending.Status != 302 {
				t.Fatal("pending handoff failed", pending.Status)
			}
			pendingURL, _ := url.Parse(pending.Header.Get("Location"))
			uid := viewer.ID
			if switchPerson {
				other := a.NewPerson(t, "replacementother")
				uid = other.ID
			}
			key, err := auth.GenerateAPIKey()
			if err != nil {
				t.Fatal(err)
			}
			if err := accountapp.New(a.Database).AddSignInKey(context.Background(), uid, key, "dashboard sign-in"); err != nil {
				t.Fatal(err)
			}
			r := a.At(t, "POST", a.AppHost(), "/v1/sso/session", nil, map[string]string{"X-API-Key": key, "Cookie": auth.SSOCookieHost + "=" + pass, "Origin": "https://" + a.AppHost(), "Sec-Fetch-Site": "same-origin"})
			if r.Status != 204 {
				t.Fatalf("replace: %d", r.Status)
			}
			nextPass := ResponseCookie(t, r, auth.SSOCookieHost)
			if _, err := accountsrepo.GetSSOPass(context.Background(), a.Database, accountsrepo.HashAPIKey(pass), 180*24*time.Hour); err != sql.ErrNoRows {
				t.Fatal("old pass survived")
			}
			var pendingCount int
			if err := a.Database.QueryRow(`SELECT count(*) FROM visitor_establish_tokens WHERE once=$1`, pendingURL.Query().Get("once")).Scan(&pendingCount); err != nil || pendingCount != 0 {
				t.Fatal("old pending code survived", err)
			}

			id, _ := hex.DecodeString(oldSession)
			if _, err := accountsrepo.GetVisitorSession(context.Background(), a.Database, id, 180*24*time.Hour); err != sql.ErrNoRows {
				t.Fatalf("old family survived: %v", err)
			}
			if r := a.At(t, "GET", a.AppHost(), "/v1/me", nil, map[string]string{"X-API-Key": viewer.Key}); r.Status != 401 {
				t.Fatal("old key survived")
			}
			newSession, _ := a.FinishSSO(t, host, nextPass)
			r = a.At(t, "POST", host, "/v1/visitor/logout", nil, map[string]string{"Cookie": gate.VisitorCookieHost + "=" + newSession, "X-SH-CSRF": "1", "Origin": "https://" + host})
			if r.Status != 200 {
				t.Fatalf("logout: %d", r.Status)
			}
			id, _ = hex.DecodeString(newSession)
			if _, err := accountsrepo.GetVisitorSession(context.Background(), a.Database, id, 180*24*time.Hour); err != sql.ErrNoRows {
				t.Fatal("new family survived logout")
			}
		})
	}
}

func TestSSOSignoutWithoutLiveLocalSession(t *testing.T) {
	for _, mode := range []string{"expired", "no cookie", "different apex family", "swept parent"} {
		t.Run(mode, func(t *testing.T) {
			a, _ := newSSOApp(t)
			owner := a.NewPerson(t, "logoutowner")
			viewer := a.NewPerson(t, "logoutviewer")
			a.Deploy(t, owner, "one")
			a.Deploy(t, owner, "two")
			handle := owner.Handle
			one, two := "one."+handle+"."+rt.SiteDomain, "two."+handle+"."+rt.SiteDomain
			pass := a.SSOPass(t, viewer.Key)
			local, _ := a.FinishSSO(t, one, pass)
			sibling, _ := a.FinishSSO(t, two, pass)
			id, _ := hex.DecodeString(local)
			var err error
			switch mode {
			case "expired":
				_, err = a.Database.Exec(`UPDATE visitor_sessions SET expires_at=now()-interval '1 second' WHERE id=$1`, id)
			case "no cookie":
				local = ""
			case "swept parent":
				_, err = a.Database.Exec(`UPDATE sso_passes SET idle_expires_at=now()-interval '1 second' WHERE id_hash=$1`, accountsrepo.HashAPIKey(pass))
				if err == nil {
					_, err = accountsrepo.SweepSSOCount(context.Background(), a.Database)
				}
			case "different apex family":
				other := a.NewPerson(t, "logoutother")
				viewer = other
				pass = a.SSOPass(t, other.Key)
				sibling, _ = a.FinishSSO(t, two, pass)
			}
			if err != nil {
				t.Fatal(err)
			}
			r := a.At(t, "POST", one, "/v1/visitor/logout", nil, map[string]string{"Cookie": gate.VisitorCookieHost + "=" + local, "X-SH-CSRF": "1", "Origin": "https://" + one})
			if r.Status != 200 {
				t.Fatalf("logout must navigate: %d", r.Status)
			}
			u, err := url.Parse(r.JSON(t)["redirect_url"].(string))
			if err != nil {
				t.Fatal(err)
			}
			r = a.At(t, "GET", a.AppHost(), u.RequestURI(), nil, map[string]string{"Cookie": auth.SSOCookieHost + "=" + pass, "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document"})
			form := ConsentToken(t, r)
			if r.Status != 200 || !strings.Contains(r.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'") || r.Header.Get("Set-Cookie") != "" {
				t.Fatal("unbound link did not ask for confirmation")
			}
			if r := a.At(t, "GET", a.AppHost(), "/v1/me", nil, map[string]string{"X-API-Key": viewer.Key}); r.Status != 200 {
				t.Fatal("GET revoked another browser's key")
			}
			if mode != "swept parent" {
				sid, _ := hex.DecodeString(sibling)
				if _, err := accountsrepo.GetVisitorSession(context.Background(), a.Database, sid, 180*24*time.Hour); err != nil {
					t.Fatal("GET revoked another browser's family")
				}
			}
			post := func(token, cookie, origin, site string) Resp {
				return a.At(t, "POST", a.AppHost(), "/v1/sso/signed-out", url.Values{"token": {u.Query().Get("token")}, "form_token": {token}}.Encode(), map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Cookie": auth.SSOCookieHost + "=" + cookie, "Origin": origin, "Sec-Fetch-Site": site})
			}
			for _, tc := range []struct{ token, cookie, origin, site string }{
				{form, pass, "https://" + one, "cross-site"},
				{"bad", pass, "https://" + a.AppHost(), "same-origin"},
				{form, "another-cookie", "https://" + a.AppHost(), "same-origin"},
			} {
				if r := post(tc.token, tc.cookie, tc.origin, tc.site); r.Status != 400 && r.Status != 403 {
					t.Fatalf("confirmation CSRF accepted: %d", r.Status)
				}
			}
			r = post(form, pass, "null", "same-origin")
			if r.Status != 200 || ResponseCookie(t, r, auth.SSOCookieHost) != "" {
				t.Fatalf("apex did not complete sign-out: %d", r.Status)
			}
			if replay := post(form, pass, "https://"+a.AppHost(), "same-origin"); replay.Status != 400 {
				t.Fatal("confirmation replayed")
			}
			id, _ = hex.DecodeString(sibling)
			if _, err := accountsrepo.GetVisitorSession(context.Background(), a.Database, id, 180*24*time.Hour); err != sql.ErrNoRows {
				t.Fatal("sibling survived")
			}
			if mode != "swept parent" {
				if r := a.At(t, "GET", a.AppHost(), "/v1/me", nil, map[string]string{"X-API-Key": viewer.Key}); r.Status != 401 {
					t.Fatal("apex key survived")
				}
			} // The swept-parent key is revoked by the landing script (browser regression).
			if r := a.At(t, "GET", a.AppHost(), u.RequestURI(), nil, nil); r.Status != 400 {
				t.Fatal("ticket replayed")
			}
		})
	}
}

// The internal visitor pass a connector tool call carries (even an admin
// owner's) is never a management credential, and it dies when revoked.
// (Pass use on a site and refusal on another site: gates_test.go.)
func TestVisitorPassIsNoManagementCredentialAndRevokes(t *testing.T) {
	a := NewApp(t)
	ann := a.NewPerson(t, "pass-admin")
	if _, err := a.Database.Exec(`UPDATE users SET is_admin = true WHERE id = $1`, ann.ID); err != nil {
		t.Fatal(err)
	}
	sid := a.Deploy(t, ann, "shop")
	host := ann.SiteHost("shop")
	pass, revoke, err := accountsrepo.IssueInternalVisitorKey(model.InternalVisitorPass{UserID: ann.ID, SiteID: sid, Host: host, AppName: "ChatGPT"})
	if err != nil {
		t.Fatal(err)
	}
	defer revoke()
	for _, path := range []string{"/v1/sites/shop/access", "/v1/me"} {
		if r := a.At(t, "GET", rt.SiteDomain, path, nil, map[string]string{"X-API-Key": pass}); r.Status != 401 {
			t.Fatalf("management accepted pass: %s %d", path, r.Status)
		}
	}
	headers := map[string]string{gate.VisitorPassHeader: pass, "Origin": "https://" + host}
	if r, c := a.Probe(t, host, "shop", headers); r.Status != 200 || !c.VisitorAI || c.Owner {
		t.Fatalf("pass on its site: %d %s", r.Status, r.Body)
	}
	revoke()
	if r, _ := a.Probe(t, host, "shop", headers); r.Status == 200 {
		t.Fatalf("revoked pass accepted: %s", r.Body)
	}
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

type ssoReviewEnv struct {
	a                     *App
	host, site, uid, pass string
	viewer                Person
}

func newGateSite(t *testing.T) *gateSite {
	t.Helper()
	a := NewApp(t)
	g := &gateSite{App: a, Ann: a.NewPerson(t, "gate-ann"), Bob: a.NewPerson(t, "gate-bob")}
	g.SiteID = a.Deploy(t, g.Ann, "notes")
	g.Host = g.Ann.SiteHost("notes")
	return g
}

// gateSite is ann's live site "notes" and bob, another account.
type gateSite struct {
	*App
	Ann, Bob Person
	SiteID   string
	Host     string // the site's own host
}

func gateWant(t *testing.T, what string, r Resp, status int, code string) {
	t.Helper()
	if r.Status != status || (code != "" && r.Code() != code) {
		t.Fatalf("%s: %d %s (want %d %s)", what, r.Status, r.Body, status, code)
	}
}

// gatePass is an AI visitor's pass for who on siteID at host.
func gatePass(t *testing.T, who Person, siteID, host string) string {
	t.Helper()
	pass, revoke, err := accountsrepo.IssueInternalVisitorKey(model.InternalVisitorPass{UserID: who.ID, SiteID: siteID, Host: host, AppName: "Test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(revoke)
	return pass
}

func rkMemberCount(t *testing.T, a *App, team string) int {
	t.Helper()
	var n int
	if err := a.Database.QueryRow(`SELECT count(*) FROM team_members m JOIN users u ON u.id = m.team_id WHERE u.handle = $1`, team).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// ssoSiteID is the id of owner's site name.
func ssoSiteID(t *testing.T, a *App, owner Person, name string) string {
	t.Helper()
	var id string
	if err := a.Database.QueryRow(`SELECT id FROM sites WHERE user_id=$1 AND name=$2`, owner.ID, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
