package handler

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	db "github.com/vsriram/simple-host/internal/db"
)

// near reports whether got is within a minute of want.
func near(got, want time.Time) bool {
	d := got.Sub(want)
	return d > -time.Minute && d < time.Minute
}

// MAX_KEYS_PER_ACCOUNT caps the Keys panel, and the refusal says the number.
func TestKeyLimitFollowsSetting(t *testing.T) {
	a := newConnectorApp(t)
	withLimits(t, map[string]string{"MAX_KEYS_PER_ACCOUNT": "3"})
	p := a.newPerson(t, "three-keys")
	h := map[string]string{"X-API-Key": p.key, "Content-Type": "application/json"}
	for i := len(keysOf(t, a, p.key)); i < 3; i++ {
		if r := a.do(t, http.MethodPost, "/v1/me/keys", jsonBody(map[string]string{"name": "k"}), h); r.status != http.StatusCreated {
			t.Fatalf("key %d: %d %s", i, r.status, r.body)
		}
	}
	r := a.do(t, http.MethodPost, "/v1/me/keys", jsonBody(map[string]string{"name": "fourth"}), h)
	if r.status != http.StatusConflict || !strings.Contains(string(r.body), "already holds 3 keys") {
		t.Fatalf("over a limit of 3: %d %s", r.status, r.body)
	}
}

// MAX_SITES_PER_ACCOUNT caps sites per account, and the refusal says the number.
func TestSiteLimitFollowsSetting(t *testing.T) {
	a := newPersonApp(t, "serve")
	withLimits(t, map[string]string{"MAX_SITES_PER_ACCOUNT": "2"})
	olive := a.newPerson(t, "two-sites")
	a.deploy(t, olive, "one")
	a.deploy(t, olive, "two")
	r := a.at(t, "POST", "simple-host.test", "/v1/sites/three/files",
		map[string]any{"files": map[string]string{"index.html": "x"}}, map[string]string{"X-API-Key": olive.key})
	if r.status != http.StatusForbidden || !strings.Contains(string(r.body), "site_quota_reached") || !strings.Contains(string(r.body), "at most 2 sites") {
		t.Fatalf("third site under a cap of 2: %d %s", r.status, r.body)
	}
}

// DELETED_RETENTION_DAYS sets how long Recently deleted keeps a site, and the
// list says so.
func TestDeletedRetentionFollowsSetting(t *testing.T) {
	a := newPersonApp(t, "serve")
	withLimits(t, map[string]string{"DELETED_RETENTION_DAYS": "30"})
	olive := a.newPerson(t, "keeper")
	key := map[string]string{"X-API-Key": olive.key}
	a.deploy(t, olive, "gone")
	if r := a.at(t, "DELETE", "simple-host.test", "/v1/sites/gone", nil, key); r.status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.status, r.body)
	}
	var out struct {
		Sites []struct {
			Name    string    `json:"name"`
			PurgeAt time.Time `json:"purge_at"`
		} `json:"sites"`
		RetentionDays int `json:"retention_days"`
	}
	r := a.at(t, "GET", "simple-host.test", "/v1/me/deleted-sites", nil, key)
	if err := json.Unmarshal(r.body, &out); err != nil || out.RetentionDays != 30 || len(out.Sites) != 1 || !near(out.Sites[0].PurgeAt, time.Now().Add(30*24*time.Hour)) {
		t.Fatalf("deleted list: %v %s", err, r.body)
	}
}

// The email-change codes and the undo link state SIGNIN_CODE_TTL_MINUTES and
// EMAIL_CHANGE_UNDO_DAYS, and the undo link lasts that long.
func TestEmailChangeWordingFollowsSetting(t *testing.T) {
	a, mb := newAccountApp(t)
	withLimits(t, map[string]string{"SIGNIN_CODE_TTL_MINUTES": "20", "EMAIL_CHANGE_UNDO_DAYS": "9"})
	p := a.newPerson(t, "wording")
	key := map[string]string{"X-API-Key": p.key}
	newAddr := uniq("worded") + "@example.org"
	code, cur := requestEmailChange(t, a, mb, key, p.email, newAddr)
	for _, n := range append(mb.to(newAddr), mb.to(p.email)...) {
		if strings.Contains(n, "confirm") && !strings.Contains(n, "It expires in 20 minutes.") {
			t.Fatalf("code email does not say 20 minutes: %q", n)
		}
	}
	if r := a.at(t, "POST", "simple-host.test", "/v1/me/email/verify", map[string]string{"code": code, "current_code": cur}, key); r.status != 200 {
		t.Fatalf("verify: %d %s", r.status, r.body)
	}
	old := mb.to(p.email)
	if n := old[len(old)-1]; !strings.Contains(n, "works for 9 days") {
		t.Fatalf("undo notice: %q", n)
	}
	var exp time.Time
	if err := a.database.QueryRow(`SELECT expires_at FROM email_change_undos ORDER BY expires_at DESC LIMIT 1`).Scan(&exp); err != nil {
		t.Fatal(err)
	}
	if !near(exp, time.Now().Add(9*24*time.Hour)) {
		t.Fatalf("undo link expires %v, want 9 days out", exp)
	}
}

// A visitor sign-in lives VISITOR_SESSION_DAYS (cookie and row) and slides
// by VISITOR_SESSION_IDLE_DAYS.
func TestVisitorSessionFollowsSetting(t *testing.T) {
	a, dir := newSiteApp(t, "canonical")
	a.withOAuth(t)
	withLimits(t, map[string]string{"VISITOR_SESSION_DAYS": "60", "VISITOR_SESSION_IDLE_DAYS": "21"})
	olive := a.newPerson(t, "sessions")
	a.deploy(t, olive, "shop")
	_, oh := a.userID(t, olive)
	markReady(t, dir, oh)
	host := "shop." + oh + "." + pcSiteDomain
	returnTo := "https://" + host + "/"
	state, nonce := a.signInStart(t, host, returnTo)
	loc := a.signInCallback(t, state, "visitor", host)
	h := nonceCookie(nonce)
	h["X-Forwarded-For"] = "198.51.100.60"
	r := a.at(t, "GET", host, loc.RequestURI(), nil, h)
	var sess string
	for _, c := range r.header.Values("Set-Cookie") {
		if strings.HasPrefix(c, visitorCookieHost+"=") {
			sess = strings.TrimPrefix(strings.SplitN(c, ";", 2)[0], visitorCookieHost+"=")
			if !strings.Contains(c, "Max-Age=5184000") {
				t.Errorf("session cookie max age is not 60 days: %s", c)
			}
		}
	}
	if sess == "" {
		t.Fatalf("no session: %d %v", r.status, r.header.Values("Set-Cookie"))
	}
	id, _ := hex.DecodeString(sess)
	var exp, idle time.Time
	if err := a.database.QueryRow(`SELECT expires_at, idle_expires_at FROM visitor_sessions WHERE id = $1`, id).Scan(&exp, &idle); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if !near(exp, now.Add(60*24*time.Hour)) || !near(idle, now.Add(21*24*time.Hour)) {
		t.Fatalf("session expires %v, idle %v; want 60 and 21 days out", exp, idle)
	}
	if _, err := a.database.Exec(`UPDATE visitor_sessions SET idle_expires_at = now() + interval '1 hour' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if err := db.TouchVisitorSession(context.Background(), a.database, id); err != nil {
		t.Fatal(err)
	}
	if err := a.database.QueryRow(`SELECT idle_expires_at FROM visitor_sessions WHERE id = $1`, id).Scan(&idle); err != nil || !near(idle, now.Add(21*24*time.Hour)) {
		t.Fatalf("slid idle expiry %v (%v), want 21 days out", idle, err)
	}
}

// The failing-domain email states DOMAIN_LAPSE_WARN_HOURS and the time left
// before DOMAIN_LAPSE_HOURS.
func TestDomainFailingEmailFollowsSetting(t *testing.T) {
	a := newPersonApp(t, "serve")
	withLimits(t, map[string]string{"DOMAIN_LAPSE_WARN_HOURS": "36", "DOMAIN_LAPSE_HOURS": "108"})
	m := &noticeMailer{}
	a.sites.mailer = m
	olive := a.newPerson(t, "lapsing")
	a.deploy(t, olive, "shop")
	a.sites.emailDomainFailing(context.Background(), db.BoundDomain{SiteID: a.siteID(t, olive, "shop"), Domain: "shop.example.test"}, "shop", "")
	if len(m.sent) != 1 || !strings.Contains(m.sent[0], "failed every check for 36 hours.") || !strings.Contains(m.sent[0], "still failing 3 days from now") {
		t.Fatalf("failing email: %v", m.sent)
	}
}

// The idle-cleanup emails state IDLE_AFTER_DAYS, IDLE_GRACE_DAYS and
// DELETED_RETENTION_DAYS, go out with IDLE_REPLY_TO, and the run uses the
// configured windows.
func TestIdleCleanupFollowsSettings(t *testing.T) {
	a := newPersonApp(t, "canonical")
	withLimits(t, map[string]string{"IDLE_AFTER_DAYS": "40", "IDLE_GRACE_DAYS": "10", "DELETED_RETENTION_DAYS": "20", "IDLE_REPLY_TO": "help@example.org"})
	ctx := context.Background()
	m := &replyMailer{}
	a.sites.mailer = m
	a.sites.SetIdleCleanup(true, 50)
	olive := a.newPerson(t, "idleknobs")
	uid, _ := a.userID(t, olive)
	site := "quiet" + uniq("")
	a.deploy(t, olive, site)
	id := a.siteID(t, olive, site)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := a.database.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	t.Cleanup(func() { exec(`UPDATE sites SET idle_keep = true WHERE user_id = $1`, uid) })
	exec(`INSERT INTO site_view_hourly (site_id, hour, class, views) VALUES ($1, date_trunc('hour', now() - interval '200 days'), 'person', 1) ON CONFLICT DO NOTHING`, id)
	exec(`INSERT INTO analytics_ingest_state (logfile, updated_at) VALUES ('idle-test', now()) ON CONFLICT (logfile) DO UPDATE SET updated_at = now()`)
	exec(`UPDATE sites SET created_at = now() - interval '45 days', updated_at = now() - interval '45 days' WHERE id = $1`, id)
	exec(`UPDATE versions SET created_at = now() - interval '45 days' WHERE site_id = $1`, id)
	exec(`DELETE FROM site_view_hourly WHERE site_id = $1 AND hour > now() - interval '100 days'`, id)

	a.sites.runIdleCleanup(ctx, time.Now())
	warn := m.last(site)
	if warn == "" || !strings.Contains(warn, "|help@example.org|") || !strings.Contains(warn, "restored for 20 days before") {
		t.Fatalf("warning after 40 idle days: %q (all: %d)", warn, len(m.sent))
	}
	exec(`UPDATE sites SET idle_warned_at = now() - interval '11 days' WHERE id = $1`, id)
	a.sites.runIdleCleanup(ctx, time.Now())
	gone := m.last(site)
	if !strings.Contains(gone, "no visitors for over 50 days") || !strings.Contains(gone, "our email 10 days ago") {
		t.Fatalf("removal email: %q", gone)
	}
}
