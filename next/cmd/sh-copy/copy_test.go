package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ncruces/go-sqlite3/driver"

	"simplehost/app"
	"simplehost/core/testkit"
)

const (
	alice, bob, crew, carol                                                  = "a0000000-0000-0000-0000-000000000001", "b0000000-0000-0000-0000-000000000002", "c0000000-0000-0000-0000-000000000003", "d0000000-0000-0000-0000-000000000004"
	blog, secret, club, shop, menu, binned, gone, tasks, paris, napping, bad = "10000000-0000-0000-0000-000000000001", "10000000-0000-0000-0000-000000000002",
		"10000000-0000-0000-0000-000000000003", "10000000-0000-0000-0000-000000000004", "10000000-0000-0000-0000-000000000005", "10000000-0000-0000-0000-000000000006",
		"10000000-0000-0000-0000-000000000007", "10000000-0000-0000-0000-000000000008", "10000000-0000-0000-0000-000000000009", "10000000-0000-0000-0000-00000000000a",
		"10000000-0000-0000-0000-00000000000b"
)

// fixture: a small old service holding one of every kind of thing copied.
var fixture = strings.NewReplacer("ALICE", alice, "BOB", bob, "CREW", crew, "CAROL", carol, "BLOG", blog, "SECRET", secret, "CLUB", club,
	"SHOP", shop, "MENU", menu, "BINNED", binned, "GONE", gone, "TASKS", tasks, "PARIS", paris, "NAPPING", napping, "BAD", bad).Replace(`
INSERT INTO users (id, username, handle, display_name, is_admin, is_team, created_at) VALUES
 ('ALICE', 'Alice@Example.com', 'alice', 'Alice', true, false, now() - interval '200 days'),
 ('BOB', 'bob@example.com', 'bob', 'Bob', false, false, now() - interval '100 days'),
 ('CREW', 'team:crew', 'crew', 'Crew', false, true, now() - interval '50 days'),
 ('CAROL', 'carol@example.com', NULL, '', false, false, now() - interval '10 days');
INSERT INTO api_keys (key_hash, user_id, name, last4, scope, expires_at) VALUES
 ('h-laptop', 'ALICE', 'laptop', 'abcd', 'full', NULL), ('h-dash', 'ALICE', 'dashboard sign-in', 'ef01', 'full', NULL),
 ('h-ci', 'BOB', 'ci', '9999', 'deploy', NULL), ('h-old', 'BOB', 'old', '0000', 'full', now() - interval '1 day');
INSERT INTO team_members (team_id, user_id) VALUES ('CREW', 'ALICE');
INSERT INTO team_invitations (team_id, invited_by, addressed_to, expires_at) VALUES ('CREW', 'ALICE', 'dave@example.com', now() + interval '5 days');
INSERT INTO oauth_clients (client_id, client_name, redirect_uris) VALUES ('claude', 'Claude', '{https://claude.ai/api/mcp/auth_callback}');
INSERT INTO oauth_grants (id, user_id, client_id, scope, resource, last_used_at) VALUES
 ('e0000000-0000-0000-0000-000000000001', 'ALICE', 'claude', 'mcp', 'https://simple-host.app/mcp', now() - interval '3 days'),
 ('e0000000-0000-0000-0000-000000000002', 'ALICE', 'claude', 'mcp', 'https://simple-host.app/mcp', now());
INSERT INTO oauth_tokens (token_hash, grant_id, kind, expires_at, used_at) VALUES
 ('t-access', 'e0000000-0000-0000-0000-000000000001', 'access', now() + interval '1 hour', NULL),
 ('t-refresh', 'e0000000-0000-0000-0000-000000000002', 'refresh', now() + interval '30 days', NULL),
 ('t-rotated', 'e0000000-0000-0000-0000-000000000002', 'refresh', now() + interval '30 days', now()),
 ('t-expired', 'e0000000-0000-0000-0000-000000000002', 'access', now() - interval '1 hour', NULL);
INSERT INTO sites (id, user_id, name, active_version, visibility, created_at) VALUES
 ('BLOG', 'ALICE', 'blog', 5, 'public', now() - interval '150 days'), ('SECRET', 'ALICE', 'secret', 1, 'unlisted', now()),
 ('CLUB', 'ALICE', 'club', 1, 'unlisted', now()), ('SHOP', 'ALICE', 'shop', 1, 'public', now()), ('MENU', 'ALICE', 'menu', 1, 'unlisted', now()),
 ('BINNED', 'ALICE', 'binned', 1, 'unlisted', now()), ('GONE', 'ALICE', 'gone', 1, 'unlisted', now()), ('TASKS', 'CREW', 'tasks', 1, 'unlisted', now()),
 ('PARIS', 'BOB', 'quote-paris', 1, 'unlisted', now()), ('NAPPING', 'BOB', 'napping', 1, 'unlisted', now()), ('BAD', 'BOB', 'bad', 1, 'unlisted', now());
UPDATE sites SET passcode_enc = '\x0102030405', passcode_generation = 2 WHERE id = 'SECRET';
UPDATE sites SET access = 'specific' WHERE id = 'CLUB';
UPDATE sites SET custom_domain = 'ShopExample.com', domain_status = 'active', domain_verified_at = now(), domain_cert_status = 'live', domain_proof_exempt = '{shopexample.com}' WHERE id = 'SHOP';
UPDATE sites SET custom_domain = 'menu.simple-host.app', domain_status = 'active', domain_verified_at = now() WHERE id = 'MENU';
UPDATE sites SET custom_domain = 'help.simple-host.app', domain_status = 'active', domain_verified_at = now() WHERE id = 'TASKS';
UPDATE sites SET deleted_at = now() - interval '2 days' WHERE id = 'BINNED';
UPDATE sites SET deleted_at = now() - interval '30 days' WHERE id = 'GONE';
UPDATE sites SET offline_at = now(), state = '{"count": 3}' WHERE id = 'NAPPING';
UPDATE sites SET suspended_at = now(), suspended_reason = 'spam', view_password_hash = 'x', allowed_origins = 'https://bob.github.io' WHERE id = 'BAD';
UPDATE sites SET state = '{"count": 7, "title": "Hello"}' WHERE id = 'BLOG';
UPDATE users SET home_site_id = 'BLOG' WHERE id = 'ALICE';
INSERT INTO versions (site_id, version_number, disk_path, status, author)
 SELECT 'BLOG', n, '', CASE WHEN n = 6 THEN 'ready' ELSE 'active' END, 'alice' FROM generate_series(1, 6) n;
INSERT INTO versions (site_id, version_number, disk_path, status) SELECT id, 1, '', 'active' FROM sites WHERE id <> 'BLOG';
INSERT INTO site_viewers (site_id, email) VALUES ('CLUB', 'Bob@Example.com'), ('CLUB', 'eve@example.com');
INSERT INTO handle_aliases (handle, user_id) VALUES ('alice-old', 'ALICE'), ('zed', NULL);
INSERT INTO legacy_hostnames (hostname) VALUES ('oldname.simple-host.app');
INSERT INTO address_families (user_id, suffix, site_prefix, status, verified_at) VALUES ('BOB', 'quotes.example.com', 'quote-', 'active', now());
INSERT INTO visitor_sessions (id, user_id, site_id, host, expires_at, idle_expires_at) VALUES
 ('\xaa01', 'BOB', 'CLUB', 'club.alice.simple-host.app', now() + interval '20 days', now() + interval '7 days'),
 ('\xaa02', 'CAROL', 'CLUB', 'club.alice.simple-host.app', now() - interval '1 day', now() - interval '1 day');
INSERT INTO site_visitor_signins (site_id, user_id) VALUES ('CLUB', 'BOB'), ('BLOG', 'CAROL');
INSERT INTO sso_consents (user_id, site_id) VALUES ('BOB', 'CLUB');
INSERT INTO site_storage_resources (site_id, name, kind, rules) VALUES
 ('BLOG', 'guestbook', 'sqlite', '{"version":1,"policies":[{"name":"read","command":"read","using":"true"}]}'),
 ('BLOG', 'prefs', 'kv', '{"version":1,"policies":[{"name":"mine","command":"read","using":"visitor_id == auth.id"}]}'),
 ('BLOG', 'photos', 'files', '{"version":1,"policies":[{"name":"add","command":"add","check":"auth.signed_in and path != ''x''"}]}'),
 ('BLOG', 'Bad Name', 'kv', NULL);
INSERT INTO site_storage_tables (site_id, resource_name, table_name, rules) VALUES
 ('BLOG', 'guestbook', 'entries', '{"version":1,"policies":[{"name":"add","command":"add","check":"auth.checked"}]}');
INSERT INTO site_storage_files (site_id, resource_name, path, writer_id) VALUES ('BLOG', 'photos', 'a.jpg', 'CAROL'), ('BLOG', 'photos', 'b/c.png', '');
INSERT INTO collection_items (site_id, collection, data, submitted_by, deleted_at) VALUES
 ('BLOG', 'rsvp', '{"name": "Eve"}', NULL, NULL), ('BLOG', 'rsvp', '{"name": "Gone"}', NULL, now());
INSERT INTO site_view_hourly (site_id, hour, class, views) VALUES
 ('BLOG', date_trunc('hour', now() - interval '1 day'), 'person', 12), ('BLOG', date_trunc('hour', now() - interval '1 day'), 'bot', 5);
INSERT INTO site_visitor_hourly (site_id, hour, class, ip_hash, country) VALUES
 ('BLOG', date_trunc('hour', now() - interval '1 day'), 'person', '\x01', 'GB'), ('BLOG', date_trunc('hour', now() - interval '1 day'), 'person', '\x02', 'IN');
INSERT INTO site_view_daily (site_id, day, views) VALUES ('BLOG', '2026-01-02', 40);
INSERT INTO site_page_daily (site_id, day, path, views) VALUES ('BLOG', '2026-11-01', '/', 9);
INSERT INTO site_referrer_daily (site_id, day, domain, views) VALUES ('BLOG', '2026-11-01', 'news.ycombinator.com', 4);
INSERT INTO site_geo_daily (site_id, day, country, class, views, visitors) VALUES ('BLOG', '2026-11-01', 'GB', 'person', 9, 2), ('BLOG', '2026-11-01', 'US', 'bot', 3, 1);
INSERT INTO site_notifications (id, site_id, owner_id, source, url, events, secret, kind, push_p256dh, push_auth) VALUES
 ('n1', 'BLOG', 'ALICE', 'dashboard', 'https://push.example.com/dev1', '{data}', 's', 'web-push', 'p256', 'auth'),
 ('n2', 'PARIS', 'BOB', 'api', 'https://hooks.example.com/x', '{data}', 's', 'webhook', '', '');
INSERT INTO api_growth_daily (day, dim, key, calls) VALUES ('2026-11-01', 'country', 'GB', 120), ('2026-11-01', 'group', 'deploy', 50);
INSERT INTO net_usage_daily (day, iface, rx_bytes, tx_bytes) VALUES ('2026-11-01', 'eth0', 100, 200), ('2026-11-01', 'eth1', 1, 2);
`)

// files writes the old data folder: version folders, a SQLite resource, a
// key-value file and a file store.
func files(t *testing.T, old string) {
	write := func(p, body string) {
		p = filepath.Join(old, p)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for n := 1; n <= 6; n++ {
		write(fmt.Sprintf("by-id/%s/blog/v%d/index.html", alice, n), fmt.Sprintf("<h1>blog %d</h1>", n))
	}
	write("by-id/"+alice+"/blog/v5/_redirects", "/old /new 301\n/blog/* /posts/:splat 301\n")
	write("by-id/"+alice+"/blog/v5/css/site.css", "body{}")
	for _, s := range [][2]string{{alice, "secret"}, {alice, "club"}, {alice, "shop"}, {alice, "menu"}, {crew, "tasks"}, {bob, "quote-paris"}, {bob, "napping"}, {bob, "bad"}} {
		write("by-id/"+s[0]+"/"+s[1]+"/v1/index.html", "<h1>"+s[1]+"</h1>")
	}
	write("by-id/"+alice+"/club/v1/app.js", "fetch('/v1/sites/club/storage/people')")
	write("deleted/"+alice+"/"+binned+"/v1/index.html", "<h1>binned</h1>")
	write("by-id/"+alice+"/blog/runtime/files/photos/a.jpg", "\xff\xd8\xff\xe0 jpeg")
	write("by-id/"+alice+"/blog/runtime/files/photos/b/c.png", "\x89PNG\r\n\x1a\n png")
	runtime := filepath.Join(old, "by-id", alice, "blog", "runtime")
	sqlite := func(p string, stmts ...string) {
		os.MkdirAll(filepath.Dir(p), 0o755)
		f, err := driver.Open("file:" + p)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		for _, s := range stmts {
			if _, err := f.Exec(s); err != nil {
				t.Fatal(err)
			}
		}
	}
	sqlite(filepath.Join(runtime, "sqlite", "guestbook.sqlite"),
		`CREATE TABLE entries (id INTEGER PRIMARY KEY, msg TEXT, visitor_id TEXT)`, `INSERT INTO entries (msg, visitor_id) VALUES ('hi', '`+carol+`'), ('hello', '')`,
		`CREATE TABLE tags (tag TEXT PRIMARY KEY) WITHOUT ROWID`, `INSERT INTO tags VALUES ('news')`)
	sqlite(filepath.Join(runtime, "kv", "prefs.sqlite"),
		`CREATE TABLE kv (key TEXT PRIMARY KEY NOT NULL, value TEXT NOT NULL, visitor_id TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, updated_at TEXT NOT NULL) WITHOUT ROWID`,
		`INSERT INTO kv VALUES ('theme', '"dark"', '`+carol+`', '2026-11-01', '2026-11-02'), ('motd', '{"text":"hi"}', '', '2026-11-01', '2026-11-01')`)
}

// certificate writes a self-signed certificate lineage (as certbot's live/
// folder holds) for names, ending in 60 days.
func certificate(t *testing.T, dir string, names ...string) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: names[0]}, DNSNames: names,
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(60 * 24 * time.Hour).Truncate(time.Second)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	k, _ := x509.MarshalECPrivateKey(key)
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "fullchain.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
	os.WriteFile(filepath.Join(dir, "privkey.pem"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: k}), 0o600)
}

// Phase 5: a full copy of the fixture, a second copy that changes nothing,
// and a check with zero differences. A broken file is found by the check and
// mended by the next copy.
func TestCopyRecopyCheck(t *testing.T) {
	start := time.Now()
	ctx := context.Background()
	oldURL, newURL, oldDir, newDir := testkit.Database(t), testkit.Database(t), t.TempDir(), t.TempDir()
	schema, err := os.ReadFile("../../../host/db/schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	old, err := sql.Open("postgres", oldURL)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	for _, s := range []string{string(schema), fixture} {
		if _, err := old.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	files(t, oldDir)
	nc := app.FromEnv()
	nc.DatabaseURL, nc.DataDir, nc.AppDomain, nc.SiteDomain = newURL, newDir, "simple-host.app", "simple-host.site"
	// alice has her own certificate (keeps blog.alice.simple-host.app); bob
	// and crew are one-level accounts (quote-paris--bob.simple-host.site).
	ready, live := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(ready, "alice"), nil, 0o644)
	certificate(t, filepath.Join(live, "shopexample.com"), "shopexample.com", "www.shopexample.com")
	certificate(t, filepath.Join(live, "quotes.example.com"), "*.quotes.example.com")
	cfg := config{New: nc, OldDB: oldURL, OldDir: oldDir, OldCertReady: ready, OldCertDir: live}
	do := func(cmd string) (int, string) {
		var out bytes.Buffer
		n, err := run(ctx, cfg, cmd, &out)
		if err != nil {
			t.Fatalf("%s: %v\n%s", cmd, err, out.String())
		}
		return n, out.String()
	}
	if n, _ := do("copy"); n == 0 {
		t.Fatal("the first copy changed nothing")
	}
	if n, _ := do("copy"); n != 0 {
		t.Fatalf("the second copy made %d changes", n)
	}
	if n, out := do("check"); n != 0 {
		t.Fatalf("check found differences:\n%s", out)
	}

	// What landed, in the new service's own terms.
	newDB, _ := sql.Open("postgres", newURL)
	defer newDB.Close()
	for q, want := range map[string]string{
		`SELECT string_agg(name, ',' ORDER BY name) FROM keys`:                                                                                     "ci,laptop",
		`SELECT string_agg(name || ':' || (retired_at IS NOT NULL), ',' ORDER BY name) FROM names`:                                                 "alice:false,alice-old:true,bob:false,crew:false,help:false,menu:false,oldname:true,zed:true",
		`SELECT string_agg(owner_id || ':' || app_shape, ',' ORDER BY owner_id) FROM owner_addresses`:                                              alice + ":true," + bob + ":false," + crew + ":false",
		`SELECT string_agg(host || ':' || status || ':' || cert_status || ':' || (cert_expires_at IS NOT NULL), ',' ORDER BY host) FROM hostnames`: "quotes.example.com:live:issued:false,shopexample.com:live:issued:true",
		`SELECT string_agg(name || ':' || access, ',' ORDER BY name) FROM sites WHERE deleted_at IS NULL`:                                          "bad:open,blog:open,club:viewers,menu:open,napping:open,quote-paris:open,secret:passcode,shop:open,tasks:open",
		`SELECT string_agg(n::text, ',' ORDER BY n) FROM versions WHERE site_id = '` + blog + `'`:                                                  "2,3,4,5",
		`SELECT string_agg(hash, ',' ORDER BY hash) FROM oauth_tokens`:                                                                             "t-access,t-refresh",
		`SELECT string_agg(name, ',' ORDER BY name) FROM namespaces`:                                                                               "collections,guestbook,photos,prefs,state",
		`SELECT count(*)::text FROM viewers v JOIN people p ON p.id = v.person_id WHERE p.email IN ('bob@example.com', 'eve@example.com')`:         "2",
		`SELECT string_agg(name || ':' || spam_check, ',' ORDER BY name) FROM namespaces WHERE site_id = '` + blog + `'`:                           "collections:false,guestbook:true,photos:false,prefs:false,state:false",
	} {
		var got string
		if err := newDB.QueryRow(q).Scan(&got); err != nil || got != want {
			t.Errorf("%s\n got %q (%v), want %q", q, got, err, want)
		}
	}
	values, _ := driver.Open("file:" + filepath.Join(newDir, "sites", blog, "data", "prefs.sqlite"))
	var author string
	values.QueryRow(`SELECT visitor_id FROM _values WHERE key = 'theme'`).Scan(&author)
	values.Close()
	if author != carol {
		t.Errorf("a value's author is %q, want %q", author, carol)
	}

	for _, p := range []string{"shopexample.com/privkey.pem", "www.shopexample.com/fullchain.pem", "*.quotes.example.com/fullchain.pem"} {
		if _, err := os.Stat(filepath.Join(newDir, "certs", p)); err != nil {
			t.Errorf("carried certificate: %v", err)
		}
	}

	broken := filepath.Join(newDir, "sites", blog, "v5", "index.html")
	os.Remove(broken) // a hard link to the old file: never write through it
	os.WriteFile(broken, []byte("changed"), 0o644)
	if n, _ := do("check"); n == 0 {
		t.Fatal("check missed a changed file")
	}
	do("copy")
	if n, out := do("check"); n != 0 {
		t.Fatalf("after a re-copy check found differences:\n%s", out)
	}

	_, report := do("report")
	for _, want := range []string{"older view password", "redirect rules not supported", "allowed_origins dropped", "webhook dropped",
		"deploy key is now a full key", "page calls a dropped address", "older saved data not carried", "saved data not carried", "notification choices not carried", "reserved name carried"} {
		if !strings.Contains(report, want) {
			t.Errorf("the report has no %q line:\n%s", want, report)
		}
	}
	t.Logf("fixture copy, re-copy, check and report: %v", time.Since(start).Round(time.Millisecond))
}
