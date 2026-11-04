package handler

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/vsriram/simple-host/internal/edition"
	sitesrepo "github.com/vsriram/simple-host/internal/repository/sites"
)

// siteEventLog is a subscription to every site event (SIM-356) on the shop,
// read in slices: next returns the events delivered since the last call.
type siteEventLog struct {
	s    presetSite
	x    *notificationReceiver
	seen int
}

func newSiteEventLog(t *testing.T) *siteEventLog {
	t.Helper()
	s, x := notificationFixture(t)
	notificationRequest(t, s, "POST", "", map[string]any{"url": x.server.URL, "events": notifySiteEvents, "secret": notifyTestSecret}, 201)
	return &siteEventLog{s: s, x: x}
}

// next drains the queue and returns the new events as (name, data) pairs.
func (l *siteEventLog) next(t *testing.T) []map[string]any {
	t.Helper()
	drainNotifications(t, l.s)
	all := l.x.snapshot()
	var out []map[string]any
	for _, r := range all[l.seen:] {
		data, _ := r.body["data"].(map[string]any)
		if data == nil {
			t.Fatalf("event without data: %v", r.body)
		}
		data["name"] = r.body["name"]
		out = append(out, data)
	}
	l.seen = len(all)
	return out
}

// one asserts exactly one new event, of the given name, with no record-event
// or visitor-written fields, and returns its data.
func (l *siteEventLog) one(t *testing.T, name string) map[string]any {
	t.Helper()
	got := l.next(t)
	if len(got) != 1 || got[0]["name"] != name {
		t.Fatalf("want one %s, got %v", name, got)
	}
	data := got[0]
	for _, k := range []string{"storage", "kind", "preset", "table", "fields", "untrusted"} {
		if _, has := data[k]; has {
			t.Fatalf("%s carries %s: %v", name, k, data)
		}
	}
	if data["v"] != float64(1) || data["site"] != "shop" || !strings.HasPrefix(data["site_url"].(string), "https://shop.") {
		t.Fatalf("%s envelope: %v", name, data)
	}
	return data
}

func (l *siteEventLog) none(t *testing.T, what string) {
	t.Helper()
	if got := l.next(t); len(got) != 0 {
		t.Fatalf("%s: unexpected events %v", what, got)
	}
}

func (l *siteEventLog) deployShop(t *testing.T, body string) {
	t.Helper()
	r := l.s.a.at(t, "PUT", pcSiteDomain, "/v1/sites/shop/files", map[string]any{"files": map[string]string{"index.html": body}}, l.s.key)
	if r.status/100 != 2 {
		t.Fatalf("deploy: %d %s", r.status, r.body)
	}
}

func (l *siteEventLog) activate(t *testing.T, version int) {
	t.Helper()
	if r := l.s.a.at(t, "PUT", pcSiteDomain, "/v1/sites/shop/active-version", map[string]any{"version_number": version}, l.s.key); r.status != 200 {
		t.Fatalf("active-version %d: %d %s", version, r.status, r.body)
	}
}

func (l *siteEventLog) userID(t *testing.T, email string) string {
	t.Helper()
	var id string
	if err := l.s.a.database.QueryRow(`SELECT id FROM users WHERE username = $1`, email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// A publish and a change of the live version each tell the owner once; a
// no-op change says nothing.
func TestNotifyHooksSiteDeployed(t *testing.T) {
	l := newSiteEventLog(t)
	l.deployShop(t, "<h1>v2</h1>")
	data := l.one(t, "site.deployed")
	if data["version"] != float64(2) || data["previous_version"] != float64(1) || data["via"] != "deploy" {
		t.Fatalf("deploy event: %v", data)
	}
	if _, has := data["visitor"]; has {
		t.Fatalf("deploy event names a visitor: %v", data)
	}
	// A saved preview is not live: nothing to say.
	if r := l.s.a.at(t, "PUT", pcSiteDomain, "/v1/sites/shop/files?publish=false", map[string]any{"files": map[string]string{"index.html": "<h1>v3 draft</h1>"}}, l.s.key); r.status/100 != 2 {
		t.Fatalf("save: %d %s", r.status, r.body)
	}
	l.none(t, "publish=false")

	l.activate(t, 1)
	data = l.one(t, "site.deployed")
	if data["version"] != float64(1) || data["previous_version"] != float64(2) || data["via"] != "active_version" {
		t.Fatalf("rollback event: %v", data)
	}
	l.activate(t, 1)
	l.none(t, "same version again")
	// The saved preview going live counts.
	l.activate(t, 3)
	data = l.one(t, "site.deployed")
	if data["version"] != float64(3) || data["previous_version"] != float64(1) || data["via"] != "active_version" {
		t.Fatalf("publish preview event: %v", data)
	}
	// Two publishes in a row: each names the version live just before it.
	for want := 4; want <= 5; want++ {
		l.deployShop(t, "<h1>again</h1>")
		data = l.one(t, "site.deployed")
		if data["version"] != float64(want) || data["previous_version"] != float64(want-1) || data["via"] != "deploy" {
			t.Fatalf("deploy v%d event: %v", want, data)
		}
	}
}

// A person's first sign-in on the site, by emailed code or Google, is one
// visitor.signed_up; later sign-ins and the owner's own are silent.
func TestNotifyHooksVisitorSignedUp(t *testing.T) {
	l := newSiteEventLog(t)
	s := l.s
	mail := &codeMailer{}
	s.a.sites.mailer = mail
	hdr := browser(s.host, "")
	signIn := func(email string) {
		t.Helper()
		if r := s.a.at(t, "POST", s.host, "/v1/sites/shop/visitor/auth", map[string]string{"email": email}, hdr); r.status != http.StatusAccepted {
			t.Fatalf("code for %s: %d %s", email, r.status, r.body)
		}
		code := mail.code(email)
		if code == "" {
			t.Fatalf("no code sent to %s", email)
		}
		if r := s.a.at(t, "POST", s.host, "/v1/sites/shop/visitor/auth/verify", map[string]string{"email": email, "code": code}, hdr); r.status != http.StatusOK {
			t.Fatalf("verify %s: %d %s", email, r.status, r.body)
		}
	}
	carol := s.a.newPerson(t, "preset-carol")
	signIn(carol.email)
	data := l.one(t, "visitor.signed_up")
	if data["visitor"] != l.userID(t, carol.email) {
		t.Fatalf("visitor: %v", data)
	}
	if _, has := data["email"]; has {
		t.Fatalf("event carries an email: %v", data)
	}
	signIn(carol.email)
	l.none(t, "second sign-in")

	// The owner on their own site: the fixture already recorded a sign-in,
	// so forget it and sign in again through the real route.
	if _, err := s.a.database.Exec(`DELETE FROM site_visitor_signins WHERE site_id = $1 AND user_id = $2`, s.sid, s.ownerID); err != nil {
		t.Fatal(err)
	}
	signIn(s.owner.email)
	l.none(t, "owner sign-in")

	// Google ends in establishVisitor: the same event, once.
	s.a.withOAuth(t)
	google := func(who string) {
		t.Helper()
		state, nonce := s.a.signInStart(t, s.host, "https://"+s.host+"/")
		loc := s.a.signInCallback(t, state, who, s.host)
		if r := s.a.at(t, "GET", s.host, loc.RequestURI(), nil, nonceCookie(nonce)); r.status != http.StatusFound {
			t.Fatalf("establish: %d %s", r.status, r.body)
		}
	}
	google("gina")
	data = l.one(t, "visitor.signed_up")
	if data["visitor"] != l.userID(t, "gina@example.test") {
		t.Fatalf("google visitor: %v", data)
	}
	google("gina")
	l.none(t, "second google sign-in")
}

// On a named-viewers site, a signed-in person who is not listed asks for
// the page: one viewer.access_requested a day, for a page visit only.
func TestNotifyHooksViewerAccessRequested(t *testing.T) {
	l := newSiteEventLog(t)
	s := l.s
	listed, dave := s.a.newPerson(t, "preset-listed"), s.a.newPerson(t, "preset-dave")
	if r := s.a.at(t, "POST", pcSiteDomain, "/v1/sites/shop/viewers", map[string]any{"emails": []string{listed.email}}, s.key); r.status != 200 {
		t.Fatalf("viewers: %d %s", r.status, r.body)
	}
	l.none(t, "listing a viewer")
	listedCk := s.a.session(t, listed, s.sid, s.host)

	// dave signs in through the site's own form (the named-viewers path).
	mail := &codeMailer{}
	s.a.sites.mailer = mail
	form := map[string]string{"Origin": "https://" + s.host, "Content-Type": "application/x-www-form-urlencoded", "Sec-Fetch-Site": "same-origin"}
	if r := s.a.at(t, "POST", s.host, "/v1/site-signin", "email="+dave.email+"&next=/", form); r.status != 401 {
		t.Fatalf("code page: %d %s", r.status, r.body)
	}
	r := s.a.at(t, "POST", s.host, "/v1/site-signin", "email="+dave.email+"&code="+mail.code(dave.email)+"&next=/", form)
	if r.status != http.StatusSeeOther {
		t.Fatalf("form sign-in: %d %s", r.status, r.body)
	}
	var daveCk string
	for _, c := range (&http.Response{Header: r.header}).Cookies() {
		if c.Name == visitorCookieHost {
			daveCk = c.Value
		}
	}
	if daveCk == "" {
		t.Fatal("no session cookie")
	}
	daveID := l.userID(t, dave.email)
	if data := l.one(t, "visitor.signed_up"); data["visitor"] != daveID {
		t.Fatalf("form sign-up: %v", data)
	}

	// A same-origin subresource reaching the gate is not a request to get in.
	if r := s.a.at(t, "GET", s.host, "/app.js", nil, map[string]string{"Cookie": visitorCookieHost + "=" + daveCk, "Sec-Fetch-Site": "same-origin", "Sec-Fetch-Mode": "no-cors", "Sec-Fetch-Dest": "script"}); r.status != 403 {
		t.Fatalf("subresource: %d", r.status)
	}
	l.none(t, "subresource")
	page := map[string]string{"Cookie": visitorCookieHost + "=" + daveCk, "Sec-Fetch-Site": "none", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document"}
	if r := s.a.at(t, "GET", s.host, "/", nil, page); r.status != 403 || !strings.Contains(string(r.body), ">Use another email</button>") {
		t.Fatalf("private page: %d %s", r.status, r.body)
	}
	data := l.one(t, "viewer.access_requested")
	if data["visitor"] != daveID {
		t.Fatalf("access requested: %v", data)
	}
	if _, has := data["email"]; has {
		t.Fatalf("event carries an email: %v", data)
	}
	if r := s.a.at(t, "GET", s.host, "/", nil, page); r.status != 403 {
		t.Fatalf("private page again: %d", r.status)
	}
	l.none(t, "same person within a day")
	if r := s.a.at(t, "GET", s.host, "/", nil, cookieOf(listedCk)); r.status != 200 {
		t.Fatalf("listed: %d %s", r.status, r.body)
	}
	l.none(t, "listed person")
}

// A custom domain's check status moving tells the owner, with the check's
// own words; the same status again is silent; a lapse says released.
func TestNotifyHooksDomainStatusChanged(t *testing.T) {
	l := newSiteEventLog(t)
	s := l.s
	ctx := context.Background()
	dom := uniq("shop") + ".example.test"
	if r := s.a.at(t, "POST", pcSiteDomain, "/v1/sites/shop/domain", map[string]string{"domain": dom}, s.key); r.status != 200 {
		t.Fatalf("bind: %d %s", r.status, r.body)
	}
	l.none(t, "connecting a domain")
	d := s.a.boundDomain(t, s.sid)
	if d.Status != "pending" {
		t.Fatalf("fresh binding status %q", d.Status)
	}
	s.a.sites.applyDomainCheck(ctx, d, "active", "", "live")
	data := l.one(t, "domain.status_changed")
	if data["domain"] != dom || data["status"] != "active" || data["previous_status"] != "pending" || data["reason"] != "" || data["cert"] != "live" {
		t.Fatalf("active event: %v", data)
	}
	if _, has := data["visitor"]; has {
		t.Fatalf("domain event names a visitor: %v", data)
	}
	d = s.a.boundDomain(t, s.sid)
	s.a.sites.applyDomainCheck(ctx, d, "active", "", "live")
	l.none(t, "same status again")

	why := "resolves to 192.0.2.9, not to this server"
	s.a.sites.applyDomainCheck(ctx, d, "pending", why, "pending")
	data = l.one(t, "domain.status_changed")
	if data["status"] != "pending" || data["previous_status"] != "active" || data["reason"] != why || data["cert"] != "pending" {
		t.Fatalf("failing event: %v", data)
	}
	// Failing long enough: the domain is let go.
	if _, err := s.a.database.Exec(`UPDATE sites SET domain_failing_since = now() - interval '73 hours' WHERE id = $1`, s.sid); err != nil {
		t.Fatal(err)
	}
	d = s.a.boundDomain(t, s.sid)
	s.a.sites.applyDomainCheck(ctx, d, "pending", why, "pending")
	data = l.one(t, "domain.status_changed")
	if data["status"] != "released" || data["previous_status"] != "pending" || data["reason"] != why || data["cert"] != "" {
		t.Fatalf("released event: %v", data)
	}
	if _, has, _ := sitesrepo.GetSiteDomainInfo(ctx, s.a.database, s.sid); has {
		t.Fatal("domain still bound after the lapse")
	}
}

// Overlapping checks working from one stale snapshot notify once per real
// change, against the status the row held, and a long reason is cut.
func TestNotifyHooksDomainStaleSnapshot(t *testing.T) {
	l := newSiteEventLog(t)
	s := l.s
	ctx := context.Background()
	dom := uniq("shop") + ".example.test"
	if r := s.a.at(t, "POST", pcSiteDomain, "/v1/sites/shop/domain", map[string]string{"domain": dom}, s.key); r.status != 200 {
		t.Fatalf("bind: %d %s", r.status, r.body)
	}
	stale := s.a.boundDomain(t, s.sid)
	if stale.Status != "pending" {
		t.Fatalf("fresh binding status %q", stale.Status)
	}
	s.a.sites.applyDomainCheck(ctx, stale, "active", "", "live")
	s.a.sites.applyDomainCheck(ctx, stale, "active", "", "live")
	if data := l.one(t, "domain.status_changed"); data["status"] != "active" || data["previous_status"] != "pending" {
		t.Fatalf("active event: %v", data)
	}
	s.a.sites.applyDomainCheck(ctx, stale, "pending", "", "pending")
	if data := l.one(t, "domain.status_changed"); data["status"] != "pending" || data["previous_status"] != "active" {
		t.Fatalf("flap back event: %v", data)
	}

	why := "resolves to " + strings.Repeat("192.0.2.9, é ", 200)
	s.a.sites.applyDomainCheck(ctx, stale, "error", why, "failed")
	data := l.one(t, "domain.status_changed")
	reason, _ := data["reason"].(string)
	if len(why) <= 2000 || len(reason) > 1024 || len(reason) < 1000 || !utf8.ValidString(reason) || !strings.HasPrefix(why, reason) {
		t.Fatalf("reason not cut: %d bytes", len(reason))
	}
	if _, has := data["truncated"]; has {
		t.Fatalf("cut reason marked truncated: %v", data)
	}
}

// Simple Hack has no site events: the hooks are silent there. A personal
// site cannot be redeployed on Hack at all (no_personal_sites), so the live
// version is changed instead, through the same site.deployed hook.
func TestNotifyHooksHackModeSilent(t *testing.T) {
	l := newSiteEventLog(t)
	l.deployShop(t, "<h1>v2</h1>")
	l.one(t, "site.deployed")
	l.s.a.sites.ed = edition.ForKind(edition.Hack)
	l.activate(t, 1)
	l.none(t, "rollback in hack mode")
	if n := len(l.s.a.sites.notifyEngine().emitting); n != 0 {
		t.Fatalf("emit queued in hack mode: %d", n)
	}
	l.s.a.sites.ed = edition.ForKind(edition.Host)
	l.activate(t, 2)
	if data := l.one(t, "site.deployed"); data["version"] != float64(2) || data["previous_version"] != float64(1) {
		t.Fatalf("after hack mode: %v", data)
	}
}
