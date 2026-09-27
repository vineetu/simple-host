package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	db "github.com/vsriram/simple-host/internal/db"
)

type replyMailer struct {
	mu   sync.Mutex
	sent []string // to|replyTo|subject|text
}

func (m *replyMailer) SendSignInCode(string, string, string) error { return nil }
func (m *replyMailer) SendNoticeReplyTo(to, replyTo, subject, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, to+"|"+replyTo+"|"+subject+"|"+text)
	return nil
}

// last returns the newest mail about site, or "".
func (m *replyMailer) last(site string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := len(m.sent) - 1; i >= 0; i-- {
		if strings.Contains(m.sent[i], "Your site "+site+" ") {
			return m.sent[i]
		}
	}
	return ""
}

var idleLinkRE = regexp.MustCompile(`/v1/idle/(keep|download|restore)\?t=([0-9a-f]{48})`)

// idleAct opens an emailed link the way a person does: the GET shows a
// confirmation page and changes nothing; the button POSTs the token.
func idleAct(t *testing.T, a *privateApp, link string) resp {
	t.Helper()
	path, q, _ := strings.Cut(link, "?")
	return a.at(t, "POST", "simple-host.test", path, q, map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
}

func idleLinks(mail string) map[string]string {
	out := map[string]string{}
	for _, m := range idleLinkRE.FindAllStringSubmatch(mail, -1) {
		out[m[1]] = "/v1/idle/" + m[1] + "?t=" + m[2]
	}
	return out
}

// The whole flow: warned after 90 idle days with Keep / Download links;
// Keep resets the clock; 30 days after a warning with nothing done the site
// goes to Recently deleted with a Restore link that brings it back; a site
// marked Keep, or with its own domain, is never touched.
func TestIdleSiteCleanup(t *testing.T) {
	a := newPersonApp(t, "canonical")
	ctx := context.Background()
	m := &replyMailer{}
	a.sites.mailer = m
	a.sites.SetIdleCleanup(true, 50)

	olive := a.newPerson(t, "olive")
	okey := map[string]string{"X-API-Key": olive.key}
	uid, ohandle := a.userID(t, olive)
	a.deploy(t, olive, "quiet")
	a.deploy(t, olive, "kept")
	a.deploy(t, olive, "fresh")
	quiet, kept, fresh := a.siteID(t, olive, "quiet"), a.siteID(t, olive, "kept"), a.siteID(t, olive, "fresh")

	// Visit records reach back 200 days and the ingester ran just now.
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := a.database.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO site_view_hourly (site_id, hour, class, views) VALUES ($1, date_trunc('hour', now() - interval '200 days'), 'person', 1) ON CONFLICT DO NOTHING`, fresh)
	exec(`INSERT INTO analytics_ingest_state (logfile, updated_at) VALUES ('idle-test', now()) ON CONFLICT (logfile) DO UPDATE SET updated_at = now()`)
	backdate := func(site string, days int) {
		t.Helper()
		exec(`UPDATE sites SET created_at = now() - make_interval(days => $2), updated_at = now() - make_interval(days => $2) WHERE id = $1`, site, days)
		exec(`UPDATE versions SET created_at = now() - make_interval(days => $2) WHERE site_id = $1`, site, days)
	}
	backdate(quiet, 100)
	backdate(kept, 100)
	// fresh: old, but a person visited it last week.
	backdate(fresh, 100)
	exec(`INSERT INTO site_view_hourly (site_id, hour, class, views) VALUES ($1, date_trunc('hour', now() - interval '7 days'), 'person', 3) ON CONFLICT DO NOTHING`, fresh)
	if r := a.at(t, "PUT", "simple-host.test", "/v1/sites/kept/keep", map[string]any{"keep": true}, okey); r.status != http.StatusOK {
		t.Fatalf("keep flag: %d %s", r.status, r.body)
	}

	// A site with its own name is never idle.
	a.deploy(t, olive, "named")
	named := a.siteID(t, olive, "named")
	backdate(named, 100)
	if r := a.at(t, "POST", "simple-host.test", "/v1/sites/named/domain", map[string]any{"domain": uniq("idlename") + "." + pcSiteDomain}, okey); r.status != http.StatusOK {
		t.Fatalf("claim name: %d %s", r.status, r.body)
	}

	// The admin dry run lists quiet (not kept, not fresh) before anything runs.
	r := a.at(t, "GET", "simple-host.test", "/v1/admin/idle-sites", nil, map[string]string{"X-API-Key": a.admin})
	if r.status != http.StatusOK || !strings.Contains(string(r.body), `"site":"quiet"`) || strings.Contains(string(r.body), `"site":"kept"`) || strings.Contains(string(r.body), `"site":"fresh"`) || strings.Contains(string(r.body), `"site":"named"`) {
		t.Fatalf("dry run: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", "simple-host.test", "/v1/admin/idle-sites", nil, okey); r.status != http.StatusNotFound {
		t.Fatalf("dry run as a non-admin: %d", r.status)
	}

	a.sites.runIdleCleanup(ctx, time.Now())
	warn := m.last("quiet")
	if warn == "" || !strings.Contains(warn, "|support@simple-host.app|") {
		t.Fatalf("no warning with reply-to support: %v", m.sent)
	}
	if m.last("kept") != "" || m.last("fresh") != "" {
		t.Fatalf("kept or visited site warned: %v", m.sent)
	}
	links := idleLinks(warn)
	if links["keep"] == "" || links["download"] != "" || !strings.Contains(warn, "/"+ohandle) {
		t.Fatalf("warning links (Keep, the owner app, no download): %q", warn)
	}
	// The site list shows when it would go.
	r = a.at(t, "GET", "simple-host.test", "/v1/sites", nil, okey)
	if !strings.Contains(string(r.body), `"idle_removal_at"`) || !strings.Contains(string(r.body), `"keep":true`) {
		t.Fatalf("site list: %s", r.body)
	}
	// There is no download link: an export needs the owner signed in.
	if r := a.at(t, "GET", "simple-host.test", strings.Replace(links["keep"], "/keep", "/download", 1), nil, nil); r.status == http.StatusOK {
		t.Fatalf("download link still answers: %d", r.status)
	}
	// A second run does not warn again.
	n := len(m.sent)
	a.sites.runIdleCleanup(ctx, time.Now())
	if len(m.sent) != n {
		t.Fatalf("warned twice")
	}
	// Opening the link (a mail scanner does) only shows the button.
	for i := 0; i < 2; i++ {
		if r := a.at(t, "GET", "simple-host.test", links["keep"], nil, nil); r.status != http.StatusOK || !strings.Contains(string(r.body), `<form method="post" action="/v1/idle/keep">`) {
			t.Fatalf("keep page: %d %s", r.status, r.body)
		}
	}
	var stillWarned bool
	if err := a.database.QueryRow(`SELECT idle_warned_at IS NOT NULL FROM sites WHERE id = $1`, quiet).Scan(&stillWarned); err != nil || !stillWarned {
		t.Fatalf("a GET kept the site: %v %v", stillWarned, err)
	}
	// Keep it (the button): clock reset, link spent.
	if r := idleAct(t, a, links["keep"]); r.status != http.StatusOK || !strings.Contains(string(r.body), "stays online") {
		t.Fatalf("keep: %d %s", r.status, r.body)
	}
	if r := idleAct(t, a, links["keep"]); r.status != http.StatusNotFound {
		t.Fatalf("keep link reused: %d", r.status)
	}
	a.sites.runIdleCleanup(ctx, time.Now())
	if len(m.sent) != n {
		t.Fatalf("warned right after Keep")
	}

	// Idle again for 90 days: warned; 31 days later, removed.
	exec(`UPDATE sites SET idle_kept_at = now() - interval '100 days' WHERE id = $1`, quiet)
	a.sites.runIdleCleanup(ctx, time.Now())
	if len(m.sent) != n+1 {
		t.Fatalf("not warned again after 90 more days")
	}
	exec(`UPDATE sites SET idle_warned_at = now() - interval '31 days', idle_remove_at = now() - interval '1 day' WHERE id = $1`, quiet)
	a.sites.runIdleCleanup(ctx, time.Now())
	gone := m.last("quiet")
	if !strings.Contains(gone, "moved to Recently deleted") {
		t.Fatalf("no removal notice: %q", gone)
	}
	r = a.at(t, "GET", "simple-host.test", "/v1/me/deleted-sites", nil, okey)
	if !strings.Contains(string(r.body), `"name":"quiet"`) {
		t.Fatalf("not in Recently deleted: %s", r.body)
	}
	restore := idleLinks(gone)["restore"]
	if restore == "" {
		t.Fatalf("no restore link: %q", gone)
	}
	if r := a.at(t, "GET", "simple-host.test", restore, nil, nil); r.status != http.StatusOK || !strings.Contains(string(r.body), `action="/v1/idle/restore"`) {
		t.Fatalf("restore page: %d %s", r.status, r.body)
	}
	if r := idleAct(t, a, restore); r.status != http.StatusOK || !strings.Contains(string(r.body), "back online") {
		t.Fatalf("restore: %d %s", r.status, r.body)
	}
	var sites []map[string]any
	r = a.at(t, "GET", "simple-host.test", "/v1/sites", nil, map[string]string{"X-API-Key": olive.key, "X-Skill-Version": "1.0.0"})
	_ = json.Unmarshal(r.body, &sites)
	found := false
	for _, s := range sites {
		if s["name"] == "quiet" {
			found = true
			if s["idle_removal_at"] != nil {
				t.Fatalf("restored site still flagged: %v", s)
			}
		}
	}
	if !found {
		t.Fatalf("restored site not listed: %s", r.body)
	}
	if r := idleAct(t, a, restore); r.status != http.StatusNotFound {
		t.Fatalf("restore link reused: %d", r.status)
	}
	_ = uid

	// Off: nothing happens.
	exec(`UPDATE sites SET idle_kept_at = now() - interval '100 days' WHERE id = $1`, quiet)
	a.sites.SetIdleCleanup(false, 0)
	n = len(m.sent)
	a.sites.runIdleCleanup(ctx, time.Now())
	if len(m.sent) != n {
		t.Fatalf("ran while off")
	}
	// Cleanup: keep this test's old sites out of later runs.
	exec(`UPDATE sites SET idle_keep = true WHERE user_id = $1`, uid)
}

type failingReplyMailer struct{ replyMailer }

func (m *failingReplyMailer) SendNoticeReplyTo(to, replyTo, subject, text string) error {
	return errors.New("mail server down")
}

// The fixes from the batch-2 review: every restore ends the idle state; the
// warning and the removal re-check their conditions where they write; saves
// and list entries count as activity; exempt, reviewer and event accounts are
// never touched; a warning is only on record once its email went; the links
// do nothing on a taken-down site; stale visit data stops everything.
func TestIdleCleanupHardening(t *testing.T) {
	a := newPersonApp(t, "canonical")
	ctx := context.Background()
	m := &replyMailer{}
	a.sites.mailer = m
	a.sites.SetIdleCleanup(true, 50)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := a.database.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO site_view_hourly (site_id, hour, class, views) SELECT id, date_trunc('hour', now() - interval '200 days'), 'person', 1 FROM sites LIMIT 1 ON CONFLICT DO NOTHING`)
	exec(`INSERT INTO analytics_ingest_state (logfile, updated_at) VALUES ('idle-test', now()) ON CONFLICT (logfile) DO UPDATE SET updated_at = now()`)

	pat := a.newPerson(t, "pat")
	uid, handle := a.userID(t, pat)
	t.Cleanup(func() { _, _ = a.database.Exec(`UPDATE sites SET idle_keep = true WHERE user_id = $1`, uid) })
	key := map[string]string{"X-API-Key": pat.key}
	old := func(site string) string {
		t.Helper()
		a.deploy(t, pat, site)
		id := a.siteID(t, pat, site)
		exec(`UPDATE sites SET created_at = now() - interval '100 days', updated_at = now() - interval '100 days' WHERE id = $1`, id)
		exec(`UPDATE versions SET created_at = now() - interval '100 days' WHERE site_id = $1`, id)
		return id
	}
	mine := func() map[string]db.IdleSite {
		t.Helper()
		list, err := db.ListIdleSitesToWarn(ctx, a.database, a.sites.idleExempt, time.Now().Add(-idleAfter()), 0)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]db.IdleSite{}
		for _, s := range list {
			if s.UserID == uid {
				out[s.Name] = s
			}
		}
		return out
	}

	// List entries and saved data count as activity.
	listed, saved, plain := old("listed"), old("saved"), old("plain")
	exec(`INSERT INTO collection_items (site_id, collection, data, created_at) VALUES ($1, 'rsvp', '{}', now() - interval '3 days')`, listed)
	exec(`UPDATE sites SET updated_at = now() - interval '2 days' WHERE id = $1`, saved)
	got := mine()
	if _, ok := got["plain"]; !ok || got["listed"].SiteID != "" || got["saved"].SiteID != "" {
		t.Fatalf("activity: %v", got)
	}

	// Exempt handle, reviewer account, event account: never listed.
	a.sites.SetIdleExempt([]string{" Other ", strings.ToUpper(handle)}, "")
	if len(mine()) != 0 {
		t.Fatalf("exempt handle listed: %v", mine())
	}
	a.sites.SetIdleExempt(nil, strings.ToUpper(pat.email))
	if len(mine()) != 0 {
		t.Fatalf("reviewer account listed: %v", mine())
	}
	a.sites.SetIdleExempt(nil, "")
	exec(`UPDATE api_keys SET name = $2 WHERE user_id = $1`, uid, db.KeyNameEvent)
	if len(mine()) != 0 {
		t.Fatalf("event account listed: %v", mine())
	}
	exec(`UPDATE api_keys SET name = NULL WHERE user_id = $1`, uid)

	// A deploy between the list and the warning: no warning, no email.
	s := mine()["plain"]
	exec(`UPDATE versions SET created_at = now() WHERE site_id = $1`, plain)
	a.sites.warnIdleSite(ctx, m, s, time.Now())
	if m.last("plain") != "" {
		t.Fatalf("warned a site deployed since the list: %v", m.sent)
	}
	exec(`UPDATE versions SET created_at = now() - interval '100 days' WHERE site_id = $1`, plain)

	// A failed send leaves no warning on record.
	s = mine()["plain"]
	a.sites.warnIdleSite(ctx, &failingReplyMailer{}, s, time.Now())
	var warned bool
	if err := a.database.QueryRow(`SELECT idle_warned_at IS NOT NULL FROM sites WHERE id = $1`, plain).Scan(&warned); err != nil || warned {
		t.Fatalf("warning recorded without an email: %v %v", warned, err)
	}

	// Warned; Keep lands between the removal list and the removal: it stays.
	a.sites.warnIdleSite(ctx, m, s, time.Now())
	links := idleLinks(m.last("plain"))
	if links["keep"] == "" {
		t.Fatalf("no keep link: %v", m.sent)
	}
	exec(`UPDATE sites SET idle_warned_at = now() - interval '31 days', idle_remove_at = now() - interval '1 day' WHERE id = $1`, plain)
	remove, err := db.ListIdleSitesToRemove(ctx, a.database, a.sites.idleExempt, time.Now(), idleGrace(), 0)
	if err != nil {
		t.Fatal(err)
	}
	var target db.IdleSite
	for _, r := range remove {
		if r.SiteID == plain {
			target = r
		}
	}
	if target.SiteID == "" {
		t.Fatalf("not due for removal: %v", remove)
	}
	if err := db.KeepIdleSite(ctx, a.database, plain); err != nil {
		t.Fatal(err)
	}
	n := len(m.sent)
	a.sites.removeIdleSite(ctx, m, target, time.Now())
	var deleted bool
	if err := a.database.QueryRow(`SELECT deleted_at IS NOT NULL FROM sites WHERE id = $1`, plain).Scan(&deleted); err != nil || deleted || len(m.sent) != n {
		t.Fatalf("removed right after Keep: deleted=%v mails=%d %v", deleted, len(m.sent)-n, err)
	}

	// A failed removal notice leaves the site where it is.
	exec(`UPDATE sites SET idle_kept_at = NULL, idle_warned_at = now() - interval '31 days', idle_remove_at = now() - interval '1 day' WHERE id = $1`, plain)
	a.sites.removeIdleSite(ctx, &failingReplyMailer{}, target, time.Now())
	if err := a.database.QueryRow(`SELECT deleted_at IS NOT NULL FROM sites WHERE id = $1`, plain).Scan(&deleted); err != nil || deleted {
		t.Fatalf("removed without its email: %v %v", deleted, err)
	}

	// Removed; restored by hand in the owner app: the idle state ends, the
	// emailed link dies, and the next run leaves it alone.
	a.sites.removeIdleSite(ctx, m, target, time.Now())
	restore := idleLinks(m.last("plain"))["restore"]
	if restore == "" {
		t.Fatalf("no restore link: %v", m.sent)
	}
	if r := a.at(t, "POST", "simple-host.test", "/v1/sites/plain/restore", nil, key); r.status != http.StatusOK {
		t.Fatalf("manual restore: %d %s", r.status, r.body)
	}
	var w, rm, tok bool
	if err := a.database.QueryRow(`SELECT idle_warned_at IS NOT NULL, idle_removed_at IS NOT NULL, idle_token_hash IS NOT NULL FROM sites WHERE id = $1`, plain).Scan(&w, &rm, &tok); err != nil || w || rm || tok {
		t.Fatalf("idle state after a manual restore: warned=%v removed=%v token=%v %v", w, rm, tok, err)
	}
	if r := a.at(t, "GET", "simple-host.test", restore, nil, nil); r.status != http.StatusNotFound {
		t.Fatalf("restore link after a manual restore: %d", r.status)
	}
	if _, ok := mine()["plain"]; ok {
		t.Fatalf("restored site idle again")
	}

	// Taken down: the links do nothing.
	exec(`UPDATE sites SET idle_kept_at = NULL, updated_at = now() - interval '100 days' WHERE id = $1`, plain)
	a.sites.warnIdleSite(ctx, m, mine()["plain"], time.Now())
	keep := idleLinks(m.last("plain"))["keep"]
	exec(`UPDATE sites SET suspended_at = now() WHERE id = $1`, plain)
	if r := idleAct(t, a, keep); r.status != http.StatusForbidden {
		t.Fatalf("keep on a taken-down site: %d %s", r.status, r.body)
	}
	exec(`UPDATE sites SET suspended_at = NULL WHERE id = $1`, plain)

	// Visit data 7 hours stale: nothing runs.
	exec(`UPDATE analytics_ingest_state SET updated_at = now() - interval '7 hours'`)
	t.Cleanup(func() {
		_, _ = a.database.Exec(`UPDATE analytics_ingest_state SET updated_at = now() WHERE logfile = 'idle-test'`)
	})
	if ok, _, _, _, _ := a.sites.idleEvidenceOK(ctx, time.Now()); ok {
		t.Fatalf("stale visit data trusted")
	}
}
