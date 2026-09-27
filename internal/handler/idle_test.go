package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
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
	uid, _ := a.userID(t, olive)
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
		exec(`UPDATE sites SET created_at = now() - make_interval(days => $2) WHERE id = $1`, site, days)
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
	if links["keep"] == "" || links["download"] == "" {
		t.Fatalf("warning links: %q", warn)
	}
	// The site list shows when it would go.
	r = a.at(t, "GET", "simple-host.test", "/v1/sites", nil, okey)
	if !strings.Contains(string(r.body), `"idle_removal_at"`) || !strings.Contains(string(r.body), `"keep":true`) {
		t.Fatalf("site list: %s", r.body)
	}
	// Download works without a key.
	if r := a.at(t, "GET", "simple-host.test", links["download"], nil, nil); r.status != http.StatusOK || r.header.Get("Content-Type") != "application/gzip" {
		t.Fatalf("download: %d %s", r.status, r.header.Get("Content-Type"))
	}
	// A second run does not warn again.
	n := len(m.sent)
	a.sites.runIdleCleanup(ctx, time.Now())
	if len(m.sent) != n {
		t.Fatalf("warned twice")
	}
	// Keep it: clock reset, link spent.
	if r := a.at(t, "GET", "simple-host.test", links["keep"], nil, nil); r.status != http.StatusOK || !strings.Contains(string(r.body), "stays online") {
		t.Fatalf("keep: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", "simple-host.test", links["keep"], nil, nil); r.status != http.StatusNotFound {
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
	exec(`UPDATE sites SET idle_warned_at = now() - interval '31 days' WHERE id = $1`, quiet)
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
	if r := a.at(t, "GET", "simple-host.test", restore, nil, nil); r.status != http.StatusOK || !strings.Contains(string(r.body), "back online") {
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
	if r := a.at(t, "GET", "simple-host.test", restore, nil, nil); r.status != http.StatusNotFound {
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
