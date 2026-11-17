package handler

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/testdb"
)

// This is deliberately recorded before changing the page-path implementation.
// UPDATE_PAGEPATH_MATRIX=1 records new cases before implementation changes, never a parity fix.
func TestPagePathMatrix(t *testing.T) {
	isolateQueueTest(t)
	// Other fixture tests configure the canonical install globally. Keep the
	// same unconfigured chrome used by the original baseline, then restore it.
	hosted := instanceIsHostedService
	instanceIsHostedService = false
	t.Cleanup(func() { instanceIsHostedService = hosted })
	limits := config.DefaultLimits()
	limits.RateTLSAsk.Burst = 10000
	a, certDir := newSiteApp(t, "canonical", edition.Default(limits))
	owner := a.newPerson(t, "matrix-owner")
	viewer := a.newPerson(t, "matrix-viewer")
	setHandle(t, a, owner, "matrix-owner")
	uid, _ := a.userID(t, owner)
	markReady(t, certDir, "matrix-owner")
	a.deploy(t, owner, "matrix-site")
	sid := a.siteID(t, owner, "matrix-site")
	a.sites.WaitNotifications()
	if err := a.sites.SetPasscodeKey(testPasscodeKey); err != nil {
		t.Fatal(err)
	}
	enc, err := a.sites.passcode.seal(sid, "matrix-passcode")
	if err != nil {
		t.Fatal(err)
	}
	db, recorded := testdb.OpenRecording(os.Getenv("DB_DSN"))
	defer db.Close()
	unavailable, err := sql.Open("postgres", os.Getenv("DB_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	if err := unavailable.Close(); err != nil {
		t.Fatal(err)
	}
	a.sites.database = db
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := a.database.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO site_name_aliases(user_id, name, site_id) VALUES ($1, 'old-site', $2)`, uid, sid)
	exec(`INSERT INTO handle_aliases(handle, user_id) VALUES ('old-owner', $1)`, uid)
	exec(`INSERT INTO legacy_hostnames(hostname, site_id) VALUES ('retired.simple-host.test', $1)`, sid)
	dir := a.sites.disk.SiteDir(uid, "matrix-site")
	for name, content := range map[string]string{"faq.html": "plain address", "target.html": "redirect target"} {
		if err := os.WriteFile(filepath.Join(dir, "current", name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	famDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(famDir, "ready"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(famDir, "ready", "matrix.example.org"), []byte("prefix=matrix-\n"), 0600); err != nil {
		t.Fatal(err)
	}
	a.sites.SetAddressFamilies(famDir)
	exec(`INSERT INTO address_families(user_id,suffix,site_prefix,canonical,status,verified_at,cert_mode,cert_name) VALUES ($1,'matrix.example.org','matrix-',false,'active',now(),'wildcard','matrix')`, uid)
	a.sites.refreshFamilies(context.Background())
	host := "matrix-site.matrix-owner." + pcSiteDomain
	type address struct{ name, host, path, mode string }
	addresses := []address{
		{"person-home", "matrix-owner." + pcSiteDomain, "/", "home"},
		{"person-path", "matrix-owner." + pcSiteDomain, "/matrix-site/", ""},
		{"site-host", host, "/", ""},
		{"flat-group-site", "matrix-site--matrix-owner." + pcSiteDomain, "/", "flat"},
		{"claimed-current", "claimed." + pcSiteDomain, "/", "claimed"},
		{"claimed-previous", "previous." + pcSiteDomain, "/", "previous"},
		{"custom-current", "matrix.example.net", "/", "custom"},
		{"custom-previous", "previous.example.net", "/", "custom-previous"},
		{"legacy-host", "retired." + pcSiteDomain, "/", ""},
		{"legacy-oldest", "matrix-site." + pcSiteDomain, "/", ""},
		{"renamed-site", "old-site.matrix-owner." + pcSiteDomain, "/", ""},
		{"handle-alias", "matrix-site.old-owner." + pcSiteDomain, "/", ""},
		{"family", "site.matrix.example.org", "/", ""},
		{"content-path", pcContentHost, "/matrix-owner/matrix-site/", ""},
		{"content-handoff", pcContentHost, "/internal/site-redirect/matrix-owner/matrix-site/", ""},
		{"unknown", "unknown.example.net", "/", ""},
		{"custom-gate", "matrix.example.net", "/internal/passcode/", "custom"},
		{"family-gate", "site.matrix.example.org", "/internal/passcode/", ""},
		{"content-gate", pcContentHost, "/internal/passcode/matrix-owner/matrix-site/", ""},
		{"content-notfound-renamed", pcContentHost, "/internal/notfound", "notfound:matrix-owner/old-site/"},
		{"content-notfound-alias", pcContentHost, "/internal/notfound", "notfound:old-owner/old-site/"},
		{"content-notfound-current", pcContentHost, "/internal/notfound", "notfound:matrix-owner/matrix-site/missing/"},
		{"content-notfound-missing", pcContentHost, "/internal/notfound", "notfound:matrix-owner/missing/"},
	}
	states := []string{"public", "unlisted", "passcode-anonymous", "passcode-valid", "passcode-old", "viewers-anonymous", "viewers-listed", "viewers-removed", "offline", "taken-down", "owner-suspended", "expired", "deleted", "redirects", "plain-address", "owner-session"}
	var out strings.Builder
	out.WriteString("# D4-S1a: baseline page responses; body SHA-256; SQL numbered below.\n# address | state | responder | status | Location | Cache-Control | CSP | X-Frame-Options | X-Robots-Tag | CORP | Vary | body-sha256 | reads\n")
	statements := map[string]int{}
	var queries []string
	emit := func(label, state, responder string, r resp) {
		t.Helper()
		a.sites.WaitNotifications()
		var reads []string
		for _, q := range recorded.Statements() {
			q = strings.Join(strings.Fields(q), " ")
			n, ok := statements[q]
			if !ok {
				n = len(queries) + 1
				statements[q] = n
				queries = append(queries, q)
			}
			reads = append(reads, fmt.Sprint(n))
		}
		body := strings.ReplaceAll(string(r.body), a.srv.URL, "https://fixture.invalid")
		body = strings.ReplaceAll(body, sid, "SITE_ID")
		body = strings.ReplaceAll(body, uid, "OWNER_ID")
		body = strings.ReplaceAll(body, viewer.email, "viewer@example.com")
		sum := sha256.Sum256([]byte(body))
		fields := []string{label, state, responder, fmt.Sprint(r.status)}
		for _, k := range []string{"Location", "Cache-Control", "Content-Security-Policy", "X-Frame-Options", "X-Robots-Tag", "Cross-Origin-Resource-Policy", "Vary"} {
			fields = append(fields, strings.Join(r.header.Values(k), "; "))
		}
		fields = append(fields, hex.EncodeToString(sum[:]), strings.Join(reads, ","))
		out.WriteString(strings.Join(fields, " | ") + "\n")
	}
	for _, addr := range addresses {
		for _, state := range states {
			exec(`UPDATE sites SET visibility='public',passcode_enc=NULL,passcode_set_at=NULL,passcode_generation=2,access='anyone',offline_at=NULL,suspended_at=NULL,deleted_at=NULL,expires_at=NULL,custom_domain=NULL,previous_domain=NULL,domain_verified_at=NULL,domain_status=NULL WHERE id=$1`, sid)
			exec(`UPDATE users SET suspended_at=NULL,home_site_id=NULL WHERE id=$1`, uid)
			exec(`DELETE FROM site_viewers WHERE site_id=$1`, sid)
			for _, marker := range []string{"offline", "suspended", "passcode", "redirects"} {
				if err := os.Remove(filepath.Join(dir, marker)); err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
			}
			a.sites.ed = a.users.ed
			a.sites.SetTeamSiteFlatHosts(false)
			a.sites.SetGroupHosts(nil)
			switch addr.mode {
			case "home":
				exec(`UPDATE users SET home_site_id=$1 WHERE id=$2`, sid, uid)
			case "flat":
				asGroupFlat(a)
				a.sites.SetGroupHosts(&matrixGroupHosts{stubGroupHosts: stubGroupHosts{group: Group{GroupRef: GroupRef{ID: "matrix-group", Slug: "matrix-owner", AccountID: uid}}}})
			case "claimed":
				exec(`UPDATE sites SET custom_domain='claimed.simple-host.test',domain_verified_at=now(),domain_status='active' WHERE id=$1`, sid)
			case "previous":
				exec(`UPDATE sites SET custom_domain='claimed.simple-host.test',previous_domain='previous.simple-host.test',domain_status='pending' WHERE id=$1`, sid)
			case "custom":
				exec(`UPDATE sites SET custom_domain='matrix.example.net',domain_verified_at=now(),domain_status='active' WHERE id=$1`, sid)
			case "custom-previous":
				exec(`UPDATE sites SET custom_domain='matrix.example.net',previous_domain='previous.example.net',domain_status='pending' WHERE id=$1`, sid)
			}
			switch state {
			case "unlisted":
				exec(`UPDATE sites SET visibility='unlisted' WHERE id=$1`, sid)
			case "passcode-anonymous", "passcode-valid", "passcode-old":
				exec(`UPDATE sites SET passcode_enc=$2,passcode_set_at=now() WHERE id=$1`, sid, enc)
				if err := a.sites.disk.SetPasscodeMarker(uid, "matrix-site", true); err != nil {
					t.Fatal(err)
				}
			case "viewers-anonymous", "viewers-listed", "viewers-removed":
				exec(`UPDATE sites SET access='specific' WHERE id=$1`, sid)
				if err := a.sites.disk.SetPasscodeMarker(uid, "matrix-site", true); err != nil {
					t.Fatal(err)
				}
				if state == "viewers-listed" {
					exec(`INSERT INTO site_viewers(site_id,email) VALUES ($1,$2)`, sid, viewer.email)
				}
			case "offline":
				exec(`UPDATE sites SET offline_at=now() WHERE id=$1`, sid)
				if err := a.sites.disk.SetOffline(uid, "matrix-site", true); err != nil {
					t.Fatal(err)
				}
			case "taken-down":
				exec(`UPDATE sites SET suspended_at=now() WHERE id=$1`, sid)
				if err := a.sites.disk.SetSuspended(uid, "matrix-site", true); err != nil {
					t.Fatal(err)
				}
			case "owner-suspended":
				exec(`UPDATE users SET suspended_at=now() WHERE id=$1`, uid)
				if err := a.sites.disk.SetSuspended(uid, "matrix-site", true); err != nil {
					t.Fatal(err)
				}
			case "expired":
				exec(`UPDATE sites SET expires_at=now()-interval '1 day' WHERE id=$1`, sid)
			case "deleted":
				exec(`UPDATE sites SET deleted_at=now() WHERE id=$1`, sid)
			case "redirects":
				if err := os.WriteFile(filepath.Join(dir, "redirects"), []byte("/ /target.html 302!\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			headers := map[string]string{}
			if state == "passcode-valid" || state == "passcode-old" {
				gen := 2
				if state == "passcode-old" {
					gen = 1
				}
				w := httptest.NewRecorder()
				req := httptest.NewRequest("GET", "https://"+addr.host+"/", nil)
				a.sites.setUnlockCookie(w, req, sid, gen)
				headers["Cookie"] = w.Result().Cookies()[0].String()
			}
			if state == "viewers-listed" || state == "viewers-removed" || state == "owner-session" {
				p := viewer
				if state == "owner-session" {
					p = owner
				}
				headers["Cookie"] = visitorCookieHost + "=" + a.session(t, p, sid, addr.host)
			}
			p := addr.path
			if state == "plain-address" {
				p += "faq"
			}
			if original, ok := strings.CutPrefix(addr.mode, "notfound:"); ok {
				p = addr.path
				if state == "plain-address" {
					original += "faq"
				}
				headers["X-Original-URI"] = "/" + original + "?from=matrix%20test"
			}
			responder := "program"
			if strings.HasPrefix(addr.mode, "custom") && addr.name != "custom-gate" || addr.name == "family" || addr.name == "content-path" {
				responder = "web-server (direct program response recorded)"
			}
			recorded.Reset()
			page := a.at(t, "GET", addr.host, p, nil, headers)
			emit(addr.name, state, responder, page)
			recorded.Reset()
			tls := a.at(t, "GET", pcSiteDomain, "/internal/tls-ask?domain="+addr.host, nil, nil)
			emit("tls-ask/"+addr.name, state, "program", tls)
			if os.Getenv("UPDATE_PAGEPATH_MATRIX") != "1" {
				table, _, err := a.sites.LoadSiteTable(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				a.sites.SetSiteFacts(NewCopyFacts(table).View())
				a.sites.refreshFamilies(context.Background())
				// nginx's miss hand-off needs only routing/response facts. Prove
				// it still works when the database cannot answer at all.
				if strings.HasPrefix(addr.mode, "notfound:") {
					a.sites.database = unavailable
				}
				for _, check := range []struct {
					name      string
					want, got resp
				}{
					{"page", page, a.at(t, "GET", addr.host, p, nil, headers)},
					{"tls", tls, a.at(t, "GET", pcSiteDomain, "/internal/tls-ask?domain="+addr.host, nil, nil)},
				} {
					a.sites.WaitNotifications()
					if check.want.status != check.got.status || string(check.want.body) != string(check.got.body) {
						t.Fatalf("%s/%s %s copy response differs: postgres %d %q; copy %d %q", addr.name, state, check.name, check.want.status, check.want.body, check.got.status, check.got.body)
					}
					for _, key := range []string{"Location", "Cache-Control", "Content-Security-Policy", "X-Frame-Options", "X-Robots-Tag", "Cross-Origin-Resource-Policy", "Vary"} {
						if !reflect.DeepEqual(check.want.header.Values(key), check.got.header.Values(key)) {
							t.Fatalf("%s/%s %s copy %s: got %q want %q", addr.name, state, check.name, key, check.got.header.Values(key), check.want.header.Values(key))
						}
					}
				}
				a.sites.database = db
				a.sites.SetSiteFacts(nil)
				a.sites.refreshFamilies(context.Background())
			}
		}
	}
	// The family list is a background routing read, not a per-request read.
	recorded.Reset()
	a.sites.refreshFamilies(context.Background())
	for _, q := range recorded.Statements() {
		q = strings.Join(strings.Fields(q), " ")
		n, ok := statements[q]
		if !ok {
			n = len(queries) + 1
			statements[q] = n
			queries = append(queries, q)
		}
		fmt.Fprintf(&out, "# family-refresh read: %d\n", n)
	}
	// SQL is saved without values, cookies, keys, or DSNs. Order and duplicates
	// in each cell remain significant; the list is an execution record, not a grep.
	out.WriteString("\n# Recorded statements\n")
	for i, q := range queries {
		fmt.Fprintf(&out, "%d | %s\n", i+1, q)
	}
	got := out.String()
	filename := "testdata/pagepath-matrix.txt"
	if os.Getenv("UPDATE_PAGEPATH_MATRIX") == "1" {
		if err := os.WriteFile(filename, []byte(got), 0644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		gotLines, wantLines := strings.Split(got, "\n"), strings.Split(string(want), "\n")
		for i := 0; i < len(gotLines) && i < len(wantLines); i++ {
			if gotLines[i] != wantLines[i] {
				t.Fatalf("matrix line %d\n got: %s\nwant: %s", i+1, gotLines[i], wantLines[i])
			}
		}
		t.Fatalf("matrix length changed: got %d lines, want %d", len(gotLines), len(wantLines))
	}
}

// The platform fixture uses the existing GroupHosts seam; real product SQL
// belongs to the product repository's routing snapshot test.
type matrixGroupHosts struct{ stubGroupHosts }

func (g *matrixGroupHosts) LoadRoutingFacts(context.Context, *sql.Tx) ([]model.RouteGroup, []model.RouteGroupSite, error) {
	return []model.RouteGroup{{ID: g.group.ID, Slug: g.group.Slug, AccountID: g.group.AccountID}}, []model.RouteGroupSite{{GroupID: g.group.ID, Name: "matrix-site"}}, nil
}

func TestCopyContentPathOwnerValidation(t *testing.T) {
	facts := NewCopyFacts(model.SiteTable{
		Owners: []model.RouteOwner{
			{ID: "owner", Handle: "current"},
			{ID: "path-only", Handle: "-legacy"},
			{ID: "reserved", Handle: "admin"},
			{ID: "invalid", Handle: "bad.handle"},
		},
		HandleAliases: []model.RouteAlias{{Name: "old", Target: "current"}},
	})
	for _, tc := range []struct{ handle, name, owner string }{
		{"current", "page", "owner"}, {"old", "page", "owner"},
		{"-legacy", "page", "path-only"}, {"admin", "page", "reserved"},
		{"bad.handle", "page", ""}, {"current", "../page", ""},
		{"current", "Page", ""}, {"current", "", ""},
		{"", "page", ""}, {"missing", "page", ""},
	} {
		got, err := facts.ContentPathOwner(context.Background(), tc.handle, tc.name)
		if tc.owner == "" {
			if err != sql.ErrNoRows {
				t.Fatalf("%q/%q: expected no rows, got %v", tc.handle, tc.name, err)
			}
		} else if err != nil || got.ID != tc.owner {
			t.Fatalf("%q/%q: %s %v", tc.handle, tc.name, got.ID, err)
		}
	}
}
