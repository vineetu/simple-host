package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
	"github.com/vsriram/simple-host/internal/config"
	accountsrepo "github.com/vsriram/simple-host/internal/repository/accounts"
	sitesrepo "github.com/vsriram/simple-host/internal/repository/sites"
)

// Preset equivalence through HTTP (R3-S5b). R2-S6 compares decisions and
// R3-S5a the service; neither sees the gates the handler puts in front of the
// engine. Here every request is sent over HTTP to a preset resource and to
// its twin, the same resource converted to rules with Convert, on one site,
// and the two answers must have the same status and the same body.
//
// The twins are made the way a real conversion is: both saved with the
// preset, given the same table, then one of them saved again with rules.

type equivHTTPConfig struct {
	name, kind, preset string
	table              string // a preset for the table itself (SQLite only)
}

// Every preset is compared with no gate raised. A gate sits in front of the
// permissions and does not read them, so each gate is knocked on with a few
// configurations that differ where a gate could matter: only the owner reads
// (private), each visitor reads their own (records), anyone reads (public).
// A gate that refuses every caller outright needs one of each kind.
var (
	equivHTTPGateConfigs     = map[string]bool{"private-sqlite": true, "records-sqlite": true, "public-sqlite": true, "private-kv": true, "records-kv": true}
	equivHTTPAbsoluteConfigs = map[string]bool{"private-sqlite": true, "records-kv": true}
)

func equivHTTPConfigs() []equivHTTPConfig {
	var out []equivHTTPConfig
	for _, kind := range []string{"sqlite", "kv"} {
		for _, p := range storagePresets {
			out = append(out, equivHTTPConfig{name: p.ID + "-" + kind, kind: kind, preset: p.ID})
		}
	}
	return append(out, equivHTTPConfig{name: "override-sqlite", kind: "sqlite", preset: "private", table: "wall"})
}

const (
	equivHTTPSeedTime = "2020-01-01T00:00:00Z"
	equivHTTPTable    = `CREATE TABLE items (id INTEGER PRIMARY KEY, v TEXT, visitor_id TEXT, created_at TEXT, updated_at TEXT)`
)

type equivHTTPSite struct {
	presetSite
	bobID string
	// What the comparison counted.
	comparisons, wording int
	successes            map[string]int
	gateCodes            map[string]map[string]bool // case -> codes seen on the preset twin
	// first is the preset twin's first status for gate/config/caller/operation.
	first map[string]int
	// asOwner runs the test's own owner requests (reading the data back,
	// seeding it again). A gate that stops the owner's key too lowers itself
	// around them.
	asOwner func(func())
}

// twins saves both twins of cfg and returns their resource names.
func (s *equivHTTPSite) twins(t *testing.T, cfg equivHTTPConfig) [2]string {
	t.Helper()
	names := [2]string{"p-" + cfg.name, "r-" + cfg.name}
	matrix, ok := storagePresetMatrix(cfg.preset)
	if !ok {
		t.Fatalf("no preset %s", cfg.preset)
	}
	in := ruleengine.ConvertInput{Kind: cfg.kind, Default: ruleengine.Matrix(matrix)}
	body := map[string]any{"kind": cfg.kind, "preset": cfg.preset}
	if cfg.table != "" {
		m, _ := storagePresetMatrix(cfg.table)
		in.Tables = map[string]ruleengine.Matrix{"items": ruleengine.Matrix(m)}
		body["tables"] = map[string]any{"items": map[string]string{"preset": cfg.table}}
	}
	for _, name := range names {
		s.must(t, "PUT", "/resources/"+name, body, s.key, 201)
		if cfg.kind == "sqlite" {
			s.must(t, "POST", "/sqlite/"+name+"/schema", map[string]string{"sql": equivHTTPTable}, s.key, 200)
		}
	}
	out := ruleengine.Convert(in)
	if len(out.Manual) != 0 {
		t.Fatalf("%s: not convertible: %v", cfg.name, out.Manual)
	}
	rules := map[string]any{"kind": cfg.kind, "rules": json.RawMessage(ruleengine.EncodeSet(out.Default))}
	if cfg.kind == "sqlite" {
		tables := map[string]any{}
		for name, set := range out.Tables {
			tables[name] = map[string]any{"rules": json.RawMessage(ruleengine.EncodeSet(set))}
		}
		rules["tables"] = tables
	}
	s.must(t, "PUT", "/resources/"+names[1], rules, s.key, 200)
	// Each twin is what it claims to be.
	status := map[string]string{}
	var list struct {
		Resources []struct {
			Name   string `json:"name"`
			Status string `json:"rules_status"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(s.must(t, "GET", "/resources", nil, s.key, 200).body, &list); err != nil {
		t.Fatal(err)
	}
	for _, r := range list.Resources {
		status[r.Name] = r.Status
	}
	if status[names[0]] != "preset" || status[names[1]] != "rules" {
		t.Fatalf("%s: twins are %q and %q, want preset and rules", cfg.name, status[names[0]], status[names[1]])
	}
	return names
}

// reset puts the same three entries in one twin: alice's, bob's, and one
// with no author.
func (s *equivHTTPSite) reset(t *testing.T, kind, name string) {
	t.Helper()
	if kind == "sqlite" {
		s.must(t, "POST", "/sqlite/"+name+"/execute", map[string]string{"sql": "DELETE FROM items"}, s.key, 200)
	}
	for _, entry := range []string{"1", "2", "3"} {
		s.restore(t, kind, name, entry)
	}
}

// restore puts one entry back as seeded ("new": takes away what an add left).
func (s *equivHTTPSite) restore(t *testing.T, kind, name, entry string) {
	t.Helper()
	author := map[string]any{"1": s.aliceID, "2": s.bobID, "3": nil}[entry]
	text := map[string]string{"1": "own", "2": "other", "3": "no author"}[entry]
	if kind == "sqlite" {
		if entry == "new" {
			s.must(t, "POST", "/sqlite/"+name+"/execute", map[string]string{"sql": "DELETE FROM items WHERE id > 3"}, s.key, 200)
			return
		}
		s.must(t, "POST", "/sqlite/"+name+"/execute", map[string]any{
			"sql":    "INSERT OR REPLACE INTO items(id,v,visitor_id,created_at) VALUES (?1,?2,?3,?4)",
			"params": []any{entry, text, author, equivHTTPSeedTime},
		}, s.key, 200)
		return
	}
	key := "k" + entry
	if entry == "new" {
		key = entry
	}
	if r := s.do(t, "DELETE", "/kv/"+name+"/keys/"+key, nil, s.key); r.status != 200 && r.status != 404 {
		t.Fatalf("restore %s %s: %d %s", name, key, r.status, r.body)
	}
	if entry != "new" {
		writer, _ := author.(string)
		seedStorageKV(t, s.a.sites, s.sid, name, key, `"`+text+`"`, writer)
	}
}

// state is the twin's data as the owner's tools see it: every row, or every
// key and the value of the one the operation named.
func (s *equivHTTPSite) state(t *testing.T, kind, name, entry string) any {
	t.Helper()
	if kind == "sqlite" {
		return equivHTTPBody(s.must(t, "POST", "/sqlite/"+name+"/query", map[string]string{"sql": "SELECT * FROM items ORDER BY id"}, s.key, 200).body, name)
	}
	key := "k" + entry
	if entry == "new" {
		key = entry
	}
	r := s.do(t, "GET", "/kv/"+name+"/keys/"+key, nil, s.key)
	return []any{equivHTTPBody(s.must(t, "GET", "/kv/"+name+"/keys", nil, s.key, 200).body, name), r.status, equivHTTPBody(r.body, name)}
}

// equivHTTPBody is a response body ready to compare: parsed when it is JSON,
// with the twin's own name and every timestamp replaced. Nothing else is
// touched.
func equivHTTPBody(body []byte, name string) any {
	text := strings.ReplaceAll(string(body), name, "<resource>")
	var out any
	if json.Unmarshal([]byte(text), &out) != nil {
		return text
	}
	return equivHTTPTimes(out)
}

func equivHTTPTimes(v any) any {
	switch x := v.(type) {
	case string:
		if _, err := time.Parse(time.RFC3339Nano, x); err == nil {
			return "<time>"
		}
	case []any:
		for i := range x {
			x[i] = equivHTTPTimes(x[i])
		}
	case map[string]any:
		for k := range x {
			x[k] = equivHTTPTimes(x[k])
		}
	}
	return v
}

// The approved wording differences, the same two as at the service (R3-S5a
// ruling 1, which R3-S5b applies over HTTP): status and code identical, every
// other body field identical, and the message differing only as pinned here.
func equivHTTPWordingApproved(preset, converted any) bool {
	p, ok := preset.(map[string]any)
	c, ok2 := converted.(map[string]any)
	if !ok || !ok2 || p["code"] != c["code"] {
		return false
	}
	pm, _ := p["error"].(string)
	cm, _ := c["error"].(string)
	rest := func(m map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range m {
			if k != "error" {
				out[k] = v
			}
		}
		return out
	}
	if !reflect.DeepEqual(rest(p), rest(c)) {
		return false
	}
	switch p["code"] {
	case "forbidden":
		return pm == cm+" (owner)" || pm == cm+" (nobody)"
	case "for_requires_own":
		return strings.HasPrefix(pm, "for only adds a row on a table whose read is own (the records or personal presets); this table's read is ") &&
			cm == "for needs read rules limited to the row's visitor or owner"
	}
	return false
}

type equivHTTPCaller struct {
	name string
	host string // the host the request is sent to; "" is the site's own
	// headers for one request to the named twin (a spam-check token is bound
	// to the resource and spent by one request).
	headers func(resource string) map[string]string
	// before runs ahead of each twin's request (a fresh rate limiter).
	before func()
	// repeat sends the request this many times; every answer is compared.
	repeat int
	// only limits the caller to these operations ("" all).
	only string
	// query is added to an add (the owner's ?for=).
	query string
}

// entry is the seeded entry the operation names ("new" for an add).
type equivHTTPOp struct{ name, method, path, body, entry string }

func equivHTTPOps(kind, name string) []equivHTTPOp {
	if kind == "sqlite" {
		rows := "/sqlite/" + name + "/tables/items/rows"
		return []equivHTTPOp{
			{"list", "GET", rows + "?order=id", "", ""},
			{"get-own", "GET", rows + "/1", "", "1"}, {"get-other", "GET", rows + "/2", "", "2"}, {"get-missing", "GET", rows + "/99", "", ""},
			{"add", "POST", rows, `{"v":"new"}`, "new"},
			{"edit-own", "PATCH", rows + "/1", `{"v":"changed"}`, "1"}, {"edit-other", "PATCH", rows + "/2", `{"v":"changed"}`, "2"},
			{"delete-own", "DELETE", rows + "/1", "", "1"}, {"delete-other", "DELETE", rows + "/2", "", "2"},
		}
	}
	keys := "/kv/" + name + "/keys"
	return []equivHTTPOp{
		{"list", "GET", keys, "", ""},
		{"get-own", "GET", keys + "/k1", "", "1"}, {"get-other", "GET", keys + "/k2", "", "2"}, {"get-missing", "GET", keys + "/k99", "", ""},
		{"add", "PUT", keys + "/new", `{"value":"new"}`, "new"},
		{"edit-own", "PUT", keys + "/k1", `{"value":"changed"}`, "1"}, {"edit-other", "PUT", keys + "/k2", `{"value":"changed"}`, "2"},
		{"delete-own", "DELETE", keys + "/k1", "", "1"}, {"delete-other", "DELETE", keys + "/k2", "", "2"},
	}
}

// compare sends every operation as caller to both twins and fails on the
// first difference in status, body, or the data left behind.
func (s *equivHTTPSite) compare(t *testing.T, gate string, cfg equivHTTPConfig, names [2]string, caller equivHTTPCaller) {
	t.Helper()
	ops := [2][]equivHTTPOp{equivHTTPOps(cfg.kind, names[0]), equivHTTPOps(cfg.kind, names[1])}
	for i := range ops[0] {
		op := ops[0][i]
		if caller.only != "" && caller.only != op.name {
			continue
		}
		var status [2][]int
		var bodies [2][]any
		var raw [2][]string
		wrote := false
		for twin, name := range names {
			if caller.before != nil {
				caller.before()
			}
			o := ops[twin][i]
			host := caller.host
			if host == "" {
				host = s.host
			}
			path := o.path
			if caller.query != "" {
				path += "?" + caller.query
			}
			for n := 0; n < max(caller.repeat, 1); n++ {
				var body any
				if o.body != "" {
					body = o.body
				}
				headers := caller.headers(name)
				if headers["X-API-Key"] != "" {
					host = pcSiteDomain
				}
				r := s.a.at(t, o.method, host, "/v1/sites/shop/storage"+path, body, headers)
				// A broken fixture must never pass as "both refused alike".
				if r.status >= 500 {
					t.Fatalf("%s %s %s %s on %s: %d %s", gate, cfg.name, caller.name, op.name, name, r.status, r.body)
				}
				status[twin] = append(status[twin], r.status)
				bodies[twin] = append(bodies[twin], equivHTTPBody(r.body, name))
				raw[twin] = append(raw[twin], string(r.body))
				if o.method != "GET" && r.status < 300 {
					wrote = true
				}
				if twin == 0 {
					if n == 0 {
						s.first[gate+"/"+cfg.name+"/"+caller.name+"/"+op.name] = r.status
					}
					if r.status < 300 {
						s.successes[cfg.kind+"/"+op.name]++
					} else if m, ok := equivHTTPBody(r.body, name).(map[string]any); ok {
						if s.gateCodes[gate] == nil {
							s.gateCodes[gate] = map[string]bool{}
						}
						s.gateCodes[gate][fmt.Sprintf("%d %v", r.status, m["code"])] = true
					}
				}
			}
		}
		fail := func(what string) {
			t.Helper()
			t.Fatalf("MISMATCH (%s) gate=%s config=%s caller=%s operation=%s:\n  preset    %v %q\n  converted %v %q", what, gate, cfg.name, caller.name, op.name, status[0], raw[0], status[1], raw[1])
		}
		if !reflect.DeepEqual(status[0], status[1]) {
			fail("status")
		}
		for n := range bodies[0] {
			s.comparisons++
			if reflect.DeepEqual(bodies[0][n], bodies[1][n]) {
				continue
			}
			if !equivHTTPWordingApproved(bodies[0][n], bodies[1][n]) {
				fail("body")
			}
			s.wording++
		}
		if op.method == "GET" {
			continue
		}
		s.asOwner(func() {
			if !reflect.DeepEqual(s.state(t, cfg.kind, names[0], op.entry), s.state(t, cfg.kind, names[1], op.entry)) {
				fail("data left behind")
			}
			if wrote {
				s.restore(t, cfg.kind, names[0], op.entry)
				s.restore(t, cfg.kind, names[1], op.entry)
			}
		})
	}
}

func equivHTTPFixed(h map[string]string) func(string) map[string]string {
	return func(string) map[string]string { return h }
}

func equivHTTPWith(h map[string]string, extra ...string) map[string]string {
	out := map[string]string{}
	for k, v := range h {
		out[k] = v
	}
	for i := 0; i+1 < len(extra); i += 2 {
		if extra[i+1] == "" {
			delete(out, extra[i])
		} else {
			out[extra[i]] = extra[i+1]
		}
	}
	return out
}

// An equivalence case: a gate, how to raise and lower it, and who knocks.
type equivHTTPCase struct {
	name    string
	raise   func(t *testing.T) (lower func())
	callers func(t *testing.T) []equivHTTPCaller
	// codes: answers ("status code") the gate must have given on the preset
	// twin at least once, so a gate that never fired cannot pass.
	codes []string
	// every: each answer on the preset twin is one of codes.
	every bool
}

func TestStorageRulesPresetEquivalenceHTTP(t *testing.T) {
	t.Setenv("STORAGE_RULES", "on")
	t.Setenv("KV_STORE", "sqlite")
	base, _ := commentsSite(t)
	if err := base.a.sites.SetPasscodeKey(testPasscodeKey); err != nil {
		t.Fatal(err)
	}
	withPasscodeLimits(t, func(l *config.Limits) { l.SitePasscodes = true; l.PasscodeMinLength = 6 }, &base.a.sites.limitSet, &base.a.sites.passcode.limitSet)
	h := base.a.sites
	open := func() {
		h.storageIPLimiter = newRateLimiter(100000, 0)
		h.storageVisitorLimiter = newRateLimiter(100000, 0)
	}
	open()
	direct := func(f func()) { f() }
	s := &equivHTTPSite{presetSite: base, bobID: base.ids["bob"], successes: map[string]int{}, gateCodes: map[string]map[string]bool{}, first: map[string]int{}, asOwner: direct}
	alice, bob, anon, owner := s.callers["alice"], s.callers["bob"], s.callers["anon"], s.callers["owner-site"]
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := s.a.database.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	_, handle := s.a.userID(t, s.owner)
	person := handle + "." + pcSiteDomain

	// Another site of the same owner, for a session and a visitor pass that
	// belong elsewhere.
	s.a.deploy(t, s.owner, "blog")
	blogID, blogHost := s.a.siteID(t, s.owner, "blog"), "blog."+person
	pass := func(user, site, host string) string {
		t.Helper()
		key, revoke, err := accountsrepo.IssueInternalVisitorKey(accountsrepo.InternalVisitorPass{UserID: user, SiteID: site, Host: host, AppName: "ChatGPT"})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(revoke)
		return key
	}
	ai := func(key string) map[string]string {
		return map[string]string{visitorPassHeader: key, "Origin": "https://" + s.host, "X-SH-CSRF": "1"}
	}
	fixed := func(name string, h map[string]string) equivHTTPCaller {
		return equivHTTPCaller{name: name, headers: equivHTTPFixed(h)}
	}
	everyone := func(*testing.T) []equivHTTPCaller {
		return []equivHTTPCaller{fixed("anonymous", anon), fixed("author", alice), fixed("other-visitor", bob), fixed("owner-on-site", owner), fixed("owner-key", s.key)}
	}
	visitors := func(*testing.T) []equivHTTPCaller {
		return []equivHTTPCaller{fixed("anonymous", anon), fixed("author", alice), fixed("owner-on-site", owner)}
	}
	preview := "https://" + s.host + "/__preview/1/" + h.signPreviewToken(s.sid, 1, time.Now().Add(time.Hour)) + "/"

	cases := []equivHTTPCase{
		{name: "no-gate", callers: func(t *testing.T) []equivHTTPCaller {
			return append(everyone(t),
				// A visitor's AI holding a pass for this site and host.
				fixed("visitor-AI", ai(pass(s.aliceID, s.sid, s.host))),
				equivHTTPCaller{name: "anonymous-checked", headers: func(resource string) map[string]string { return withToken(anon, s.token(resource)) }},
				equivHTTPCaller{name: "owner-on-site-for", headers: equivHTTPFixed(owner), only: "add", query: "for=" + url.QueryEscape(s.aliceID)},
			)
		}},
		{name: "missing-csrf-header", codes: []string{"403 csrf_required"}, callers: func(*testing.T) []equivHTTPCaller {
			return []equivHTTPCaller{
				fixed("anonymous", equivHTTPWith(anon, "X-SH-CSRF", "")),
				fixed("author", equivHTTPWith(alice, "X-SH-CSRF", "")),
				fixed("owner-on-site", equivHTTPWith(owner, "X-SH-CSRF", "")),
			}
		}},
		{name: "another-host", codes: []string{"403 forbidden"}, every: true, callers: func(t *testing.T) []equivHTTPCaller {
			return []equivHTTPCaller{
				fixed("another-origin", equivHTTPWith(alice, "Origin", "https://evil.example", "Sec-Fetch-Site", "cross-site")),
				fixed("sibling-site-origin", equivHTTPWith(owner, "Origin", "https://"+blogHost, "Sec-Fetch-Site", "same-site")),
				{name: "apex-host", host: pcSiteDomain, headers: equivHTTPFixed(browser(pcSiteDomain, ""))},
				{name: "content-host", host: pcContentHost, headers: equivHTTPFixed(browser(pcContentHost, ""))},
			}
		}},
		{name: "session-from-elsewhere", callers: func(t *testing.T) []equivHTTPCaller {
			return []equivHTTPCaller{
				// The owner signed in on another of their sites, on the person
				// host, and on the person host itself: never owner here.
				fixed("another-sites-session", browser(s.host, s.a.session(t, s.owner, blogID, blogHost))),
				fixed("person-host-session", browser(s.host, s.a.session(t, s.owner, s.sid, person))),
				{name: "on-person-host", host: person, headers: equivHTTPFixed(browser(person, s.a.session(t, s.owner, s.sid, person)))},
			}
		}},
		{name: "framed-page", callers: func(*testing.T) []equivHTTPCaller {
			return []equivHTTPCaller{
				fixed("owner-on-site", equivHTTPWith(owner, "X-SH-Framed", "1")),
				fixed("author", equivHTTPWith(alice, "X-SH-Framed", "1")),
			}
		}},
		{name: "preview-link", callers: func(*testing.T) []equivHTTPCaller {
			return []equivHTTPCaller{
				fixed("owner-on-site", equivHTTPWith(owner, "Referer", preview)),
				fixed("author", equivHTTPWith(alice, "Referer", preview)),
			}
		}},
		{name: "passcode-locked", codes: []string{"403 site_locked"},
			raise: func(t *testing.T) func() {
				if r := s.a.at(t, "PUT", pcSiteDomain, "/v1/sites/shop/lock", map[string]any{"passcode": "123456"}, s.key); r.status != 200 {
					t.Fatalf("lock: %d %s", r.status, r.body)
				}
				return func() {
					if r := s.a.at(t, "DELETE", pcSiteDomain, "/v1/sites/shop/lock", nil, s.key); r.status >= 300 {
						t.Fatalf("unlock: %d %s", r.status, r.body)
					}
				}
			},
			callers: func(t *testing.T) []equivHTTPCaller {
				form := url.Values{"passcode": {"123456"}, "next": {"/"}}.Encode()
				r := s.a.at(t, "POST", s.host, "/v1/site-unlock", form, map[string]string{"Origin": "https://" + s.host, "Content-Type": "application/x-www-form-urlencoded", "Sec-Fetch-Site": "same-origin"})
				if r.status != 303 {
					t.Fatalf("unlock: %d %s", r.status, r.body)
				}
				unlocked := cookieHeader(t, r)
				return append(everyone(t),
					fixed("anonymous-unlocked", equivHTTPWith(anon, "Cookie", unlocked)),
					fixed("author-unlocked", equivHTTPWith(alice, "Cookie", alice["Cookie"]+"; "+unlocked)),
				)
			}},
		{name: "named-viewers", codes: []string{"403 site_private"},
			raise: func(t *testing.T) func() {
				u, err := accountsrepo.GetUserByID(ctx, s.a.database, s.bobID)
				if err != nil {
					t.Fatal(err)
				}
				var access string
				if err := s.a.database.QueryRow(`SELECT access FROM sites WHERE id=$1`, s.sid).Scan(&access); err != nil {
					t.Fatal(err)
				}
				exec(`UPDATE sites SET access='specific' WHERE id=$1`, s.sid)
				exec(`INSERT INTO site_viewers(site_id,email) VALUES($1,$2)`, s.sid, u.Username)
				return func() {
					exec(`DELETE FROM site_viewers WHERE site_id=$1`, s.sid)
					exec(`UPDATE sites SET access=$2 WHERE id=$1`, s.sid, access)
				}
			},
			// bob is the named viewer; alice and a signed-out caller are not.
			callers: everyone},
		{name: "offline-site", codes: []string{"403 site_offline"},
			raise: func(t *testing.T) func() {
				if err := sitesrepo.SetSiteOffline(ctx, s.a.database, s.sid, true); err != nil {
					t.Fatal(err)
				}
				return func() {
					if err := sitesrepo.SetSiteOffline(ctx, s.a.database, s.sid, false); err != nil {
						t.Fatal(err)
					}
				}
			},
			callers: func(t *testing.T) []equivHTTPCaller {
				return append(everyone(t), fixed("visitor-AI", ai(pass(s.aliceID, s.sid, s.host))))
			}},
		{name: "offline-site-visitors", codes: []string{"403 site_offline"}, every: true,
			raise: func(t *testing.T) func() {
				if err := sitesrepo.SetSiteOffline(ctx, s.a.database, s.sid, true); err != nil {
					t.Fatal(err)
				}
				return func() {
					if err := sitesrepo.SetSiteOffline(ctx, s.a.database, s.sid, false); err != nil {
						t.Fatal(err)
					}
				}
			},
			callers: visitors},
		{name: "suspended-site", codes: []string{"403 site_suspended"}, every: true,
			raise: func(t *testing.T) func() {
				exec(`UPDATE sites SET suspended_at=now() WHERE id=$1`, s.sid)
				restore := func() { exec(`UPDATE sites SET suspended_at=NULL WHERE id=$1`, s.sid) }
				s.asOwner = func(f func()) {
					restore()
					f()
					exec(`UPDATE sites SET suspended_at=now() WHERE id=$1`, s.sid)
				}
				return func() { restore(); s.asOwner = direct }
			},
			callers: everyone},
		{name: "visitor-write-rate-limit", codes: []string{"429 rate_limited"}, callers: func(*testing.T) []equivHTTPCaller {
			// One write per address, then one write per visitor: the second
			// request of each pair is over the limit.
			perAddress := func() {
				h.storageIPLimiter = newRateLimiter(1, 0)
				h.storageVisitorLimiter = newRateLimiter(100000, 0)
			}
			perVisitor := func() {
				h.storageIPLimiter = newRateLimiter(100000, 0)
				h.storageVisitorLimiter = newRateLimiter(1, 0)
			}
			return []equivHTTPCaller{
				{name: "anonymous-per-address", headers: equivHTTPFixed(anon), before: perAddress, repeat: 2},
				{name: "author-per-address", headers: equivHTTPFixed(alice), before: perAddress, repeat: 2},
				{name: "author-per-visitor", headers: equivHTTPFixed(alice), before: perVisitor, repeat: 2},
				{name: "owner-on-site-per-visitor", headers: equivHTTPFixed(owner), before: perVisitor, repeat: 2},
			}
		}, raise: func(*testing.T) func() { return open }},
		{name: "visitor-AI-pass-for-another-site", codes: []string{"403 forbidden"}, every: true, callers: func(t *testing.T) []equivHTTPCaller {
			return []equivHTTPCaller{
				fixed("pass-for-another-site", ai(pass(s.aliceID, blogID, blogHost))),
				fixed("pass-for-another-host", ai(pass(s.aliceID, s.sid, person))),
				fixed("owners-pass-for-another-site", ai(pass(s.ownerID, blogID, blogHost))),
			}
		}},
	}

	configs := equivHTTPConfigs()
	callers := map[string]bool{}
	for _, cfg := range configs {
		names := s.twins(t, cfg)
		s.reset(t, cfg.kind, names[0])
		s.reset(t, cfg.kind, names[1])
		for _, c := range cases {
			if c.name != "no-gate" && !equivHTTPGateConfigs[cfg.name] || c.every && !equivHTTPAbsoluteConfigs[cfg.name] {
				continue
			}
			lower := func() {}
			if c.raise != nil {
				lower = c.raise(t)
			}
			for _, caller := range c.callers(t) {
				if caller.name == "anonymous-checked" && cfg.preset != "comments" && cfg.table != "comments" {
					continue
				}
				if caller.query != "" && cfg.kind != "sqlite" {
					continue
				}
				callers[c.name+"/"+caller.name] = true
				s.compare(t, c.name, cfg, names, caller)
			}
			lower()
		}
	}

	// Each gate fired, and the absolute ones gave nothing else.
	for _, c := range cases {
		seen := s.gateCodes[c.name]
		for _, code := range c.codes {
			if !seen[code] {
				t.Fatalf("gate %s never answered %q; it answered %v", c.name, code, equivHTTPKeys(seen))
			}
		}
		if c.every && len(seen) != len(c.codes) {
			t.Fatalf("gate %s answered %v, want only %v", c.name, equivHTTPKeys(seen), c.codes)
		}
	}
	// Pinned answers of the preset twin: what each gate does to a caller who
	// is let in without it. A gate that only takes owner rights away (a frame,
	// a preview, a session made elsewhere) refuses nothing by itself, so it is
	// proved on a resource only the owner may read.
	for _, pin := range []struct {
		key  string
		want int
	}{
		{"no-gate/private-sqlite/owner-on-site/list", 200},
		{"no-gate/private-kv/owner-on-site/get-own", 200},
		{"no-gate/public-sqlite/visitor-AI/list", 200},
		{"no-gate/comments-sqlite/anonymous/add", 403},
		{"no-gate/comments-sqlite/anonymous-checked/add", 200},
		{"no-gate/records-sqlite/owner-on-site-for/add", 200},
		{"missing-csrf-header/private-sqlite/owner-on-site/list", 200},
		{"missing-csrf-header/private-sqlite/owner-on-site/add", 403},
		{"framed-page/private-sqlite/owner-on-site/list", 403},
		{"framed-page/private-kv/owner-on-site/get-own", 403},
		{"preview-link/private-sqlite/owner-on-site/list", 403},
		{"preview-link/private-kv/owner-on-site/get-own", 403},
		{"session-from-elsewhere/private-sqlite/another-sites-session/list", 403},
		{"session-from-elsewhere/private-sqlite/person-host-session/list", 403},
		{"session-from-elsewhere/private-sqlite/on-person-host/list", 403},
		{"passcode-locked/public-sqlite/anonymous/list", 403},
		{"passcode-locked/public-sqlite/anonymous-unlocked/list", 200},
		{"passcode-locked/public-sqlite/owner-key/list", 200},
		{"named-viewers/public-sqlite/anonymous/list", 403},
		{"named-viewers/public-sqlite/author/list", 403},
		{"named-viewers/public-sqlite/other-visitor/list", 200},
		{"offline-site/public-sqlite/anonymous/list", 403},
		{"offline-site/public-sqlite/owner-key/list", 200},
		{"offline-site/private-sqlite/visitor-AI/list", 403},
		{"visitor-write-rate-limit/records-sqlite/author-per-address/add", 200},
		{"visitor-write-rate-limit/records-sqlite/author-per-visitor/add", 200},
	} {
		if got, ok := s.first[pin.key]; !ok || got != pin.want {
			t.Errorf("%s: preset twin answered %d (asked: %v), want %d", pin.key, got, ok, pin.want)
		}
	}
	if t.Failed() {
		return
	}
	// With no gate raised every operation works for someone on both kinds.
	for _, kind := range []string{"sqlite", "kv"} {
		for _, op := range equivHTTPOps(kind, "x") {
			if op.name != "get-missing" && s.successes[kind+"/"+op.name] == 0 {
				t.Fatalf("%s %s never succeeded for any configuration or caller", kind, op.name)
			}
		}
	}
	// "gate-summary:" lines of passing tests are printed by
	// scripts/lib/summarize-go-test.py, so this line is in the gate's record.
	t.Logf("gate-summary: R3-S5b platform configurations=%d gates=%d callers=%d comparisons=%d mismatches=0 refusal_wording_approved=%d", len(configs), len(cases)-1, len(callers), s.comparisons, s.wording)
}

func equivHTTPKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
