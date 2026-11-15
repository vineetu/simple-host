package servertest

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
	"github.com/vsriram/simple-host/internal/model"
	runtime "github.com/vsriram/simple-host/internal/storage"
)

// Preset equivalence on Simple Hack (R3-S5b). Every request goes over HTTP to
// a preset resource and to its twin saved with rules, and the two answers
// must have the same status and the same body. Two groups, kept apart:
//
// Group 1, real gates. The twin's rules are as open as the engine allows
// (read true, add true, edit and delete auth.signed_in; true on edit or
// delete is refused at save). The gate still refuses, with the answer it
// gives the preset twin: a rule cannot open it.
//
// Group 2, loss of owner-on-site. The twin is the preset converted with
// Convert. A caller who is not, or is no longer, owner on the site is not
// refused by a gate; they get whatever the preset gives an ordinary visitor,
// and the converted twin gives the same.

const (
	hackEquivSeedTime = "2020-01-01T00:00:00Z"
	hackEquivTable    = `CREATE TABLE items (id INTEGER PRIMARY KEY, v TEXT, visitor_id TEXT, created_at TEXT, updated_at TEXT)`
	hackEquivOpen     = `{"policies":[{"command":"read","using":"true"},{"command":"add","check":"true"},{"command":"edit","using":"auth.signed_in"},{"command":"delete","using":"auth.signed_in"}]}`
)

// hackEquivSite is one site (a team site or an event's website) as its owner
// credential and its visitors address it.
type hackEquivSite struct {
	t         *testing.T
	a         *teamSiteApp
	ownerBase string // the owner credential's storage routes
	ownerKey  string
	pageBase  string // the storage routes a page on the site calls
	host      string
	siteID    string
	dir       string // the site's runtime directory
	authors   [2]string
	// asOwner runs the test's own owner requests (reading data back, putting
	// it back). A state that stops the owner credential lifts itself around
	// them.
	asOwner func(func())

	comparisons, wording int
	first                map[string]int // the preset twin's status, by scenario/config/caller/operation
}

type hackEquivTwins struct {
	name, kind string
	names      [2]string // preset, rules
}

func newHackEquivSite(t *testing.T, a *teamSiteApp, ownerBase, ownerKey, siteName, host, siteID string, authors [2]string) *hackEquivSite {
	t.Helper()
	var owner, name string
	if err := a.database.QueryRow(`SELECT user_id, name FROM sites WHERE id=$1`, siteID).Scan(&owner, &name); err != nil {
		t.Fatal(err)
	}
	return &hackEquivSite{t: t, a: a, ownerBase: ownerBase, ownerKey: ownerKey, pageBase: "/v1/sites/" + siteName + "/storage", host: host, siteID: siteID,
		dir: filepath.Join(a.disk.SiteDir(owner, name), "runtime"), authors: authors, asOwner: func(f func()) { f() }, first: map[string]int{}}
}

func (s *hackEquivSite) owner(method, path string, body any, status int) resp {
	s.t.Helper()
	r := s.a.api(s.t, method, s.ownerBase+path, body, s.ownerKey)
	if r.status != status {
		s.t.Fatalf("owner %s %s: %d %s", method, path, r.status, r.body)
	}
	return r
}

// twins saves a preset resource and its twin: both with the preset and the
// same table, then one saved again with rules (the preset converted, or the
// open rules of group 1).
func (s *hackEquivSite) twins(prefix, kind, preset string, open bool) hackEquivTwins {
	s.t.Helper()
	tw := hackEquivTwins{name: preset + "-" + kind, kind: kind, names: [2]string{prefix + "p-" + preset + "-" + kind, prefix + "r-" + preset + "-" + kind}}
	for _, name := range tw.names {
		s.owner("PUT", "/resources/"+name, map[string]string{"kind": kind, "preset": preset}, 201)
		if kind == "sqlite" {
			s.owner("POST", "/sqlite/"+name+"/schema", map[string]string{"sql": hackEquivTable}, 200)
		}
		for _, entry := range []string{"1", "2", "3"} {
			s.restore(kind, name, entry)
		}
	}
	rules := json.RawMessage(hackEquivOpen)
	if !open {
		m, ok := model.PresetMatrix(preset)
		if !ok {
			s.t.Fatalf("no preset %s", preset)
		}
		out := ruleengine.Convert(ruleengine.ConvertInput{Kind: kind, Default: ruleengine.Matrix(m)})
		if len(out.Manual) != 0 {
			s.t.Fatalf("%s: not convertible: %v", tw.name, out.Manual)
		}
		rules = json.RawMessage(ruleengine.EncodeSet(out.Default))
	}
	body := map[string]any{"kind": kind, "rules": rules}
	if kind == "sqlite" {
		body["tables"] = map[string]any{}
	}
	if got := s.owner("PUT", "/resources/"+tw.names[1], body, 200).json(s.t); got["rules_status"] != "rules" {
		s.t.Fatalf("%s: the twin is %v, want rules", tw.name, got["rules_status"])
	}
	status := map[string]any{}
	for _, r := range s.owner("GET", "/resources", nil, 200).json(s.t)["resources"].([]any) {
		m := r.(map[string]any)
		status[m["name"].(string)] = m["rules_status"]
	}
	if status[tw.names[0]] != "preset" || status[tw.names[1]] != "rules" {
		s.t.Fatalf("%s: twins are %v and %v, want preset and rules", tw.name, status[tw.names[0]], status[tw.names[1]])
	}
	return tw
}

// restore puts one entry back as seeded ("new": takes away what an add left).
// Entry 1 is the first author's, 2 the second's, 3 nobody's.
func (s *hackEquivSite) restore(kind, name, entry string) {
	s.t.Helper()
	author := map[string]any{"1": s.authors[0], "2": s.authors[1], "3": nil}[entry]
	text := map[string]string{"1": "own", "2": "other", "3": "no author"}[entry]
	if kind == "sqlite" {
		if entry == "new" {
			s.owner("POST", "/sqlite/"+name+"/execute", map[string]string{"sql": "DELETE FROM items WHERE id > 3"}, 200)
			return
		}
		s.owner("POST", "/sqlite/"+name+"/execute", map[string]any{
			"sql":    "INSERT OR REPLACE INTO items(id,v,visitor_id,created_at) VALUES (?1,?2,?3,?4)",
			"params": []any{entry, text, author, hackEquivSeedTime},
		}, 200)
		return
	}
	key := hackEquivKey(entry)
	if r := s.a.api(s.t, "DELETE", s.ownerBase+"/kv/"+name+"/keys/"+key, nil, s.ownerKey); r.status != 200 && r.status != 404 {
		s.t.Fatalf("restore %s %s: %d %s", name, key, r.status, r.body)
	}
	if entry == "new" {
		return
	}
	// A key with a visitor as its author, written the way the server's own
	// key-value file holds it.
	writer, _ := author.(string)
	conn, done, err := runtime.OpenRuntimeKV(context.Background(), runtime.RuntimeKVPath(s.dir, name), true, false)
	if err != nil {
		s.t.Fatal(err)
	}
	defer done()
	if err = conn.Exec("BEGIN"); err != nil {
		s.t.Fatal(err)
	}
	if err = runtime.RuntimeKVPut(conn, key, `"`+text+`"`, writer, false, 0); err != nil {
		s.t.Fatal(err)
	}
	if err = conn.Exec("COMMIT; PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		s.t.Fatal(err)
	}
}

func hackEquivKey(entry string) string {
	if entry == "new" {
		return entry
	}
	return "k" + entry
}

// state is the twin's data as the owner credential sees it: every row, or
// every key and the values of the entries named (none: all of them).
func (s *hackEquivSite) state(kind, name string, entries ...string) any {
	s.t.Helper()
	if kind == "sqlite" {
		return hackEquivBody(s.owner("POST", "/sqlite/"+name+"/query", map[string]string{"sql": "SELECT * FROM items ORDER BY id"}, 200).body, name)
	}
	out := []any{hackEquivBody(s.owner("GET", "/kv/"+name+"/keys", nil, 200).body, name)}
	if len(entries) == 0 {
		entries = []string{"1", "2", "3", "new"}
	}
	for _, entry := range entries {
		r := s.a.api(s.t, "GET", s.ownerBase+"/kv/"+name+"/keys/"+hackEquivKey(entry), nil, s.ownerKey)
		out = append(out, r.status, hackEquivBody(r.body, name))
	}
	return out
}

// hackEquivBody is a response body ready to compare: parsed when it is JSON,
// with the twin's own name and every timestamp replaced. Nothing else is
// touched.
func hackEquivBody(body []byte, name string) any {
	text := strings.ReplaceAll(string(body), name, "<resource>")
	var out any
	if json.Unmarshal([]byte(text), &out) != nil {
		return text
	}
	return hackEquivTimes(out)
}

func hackEquivTimes(v any) any {
	switch x := v.(type) {
	case string:
		if _, err := time.Parse(time.RFC3339Nano, x); err == nil {
			return "<time>"
		}
	case []any:
		for i := range x {
			x[i] = hackEquivTimes(x[i])
		}
	case map[string]any:
		for k := range x {
			x[k] = hackEquivTimes(x[k])
		}
	}
	return v
}

// The approved wording differences, the same two as on the platform (R3-S5a
// ruling 1): status and code identical, every other body field identical, and
// the message differing only as pinned here.
func hackEquivWordingApproved(preset, converted any) bool {
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

type hackEquivOp struct {
	name, method, path, entry string
	body                      any
}

func (o hackEquivOp) write() bool { return o.method != "GET" && !strings.HasSuffix(o.path, "/query") }

func hackEquivOps(kind, name string) []hackEquivOp {
	if kind == "sqlite" {
		rows := "/sqlite/" + name + "/tables/items/rows"
		return []hackEquivOp{
			{"list", "GET", rows + "?order=id", "", nil},
			{"get-own", "GET", rows + "/1", "1", nil}, {"get-other", "GET", rows + "/2", "2", nil},
			{"add", "POST", rows, "new", `{"v":"new"}`},
			{"edit-own", "PATCH", rows + "/1", "1", `{"v":"changed"}`}, {"edit-other", "PATCH", rows + "/2", "2", `{"v":"changed"}`},
			{"delete-own", "DELETE", rows + "/1", "1", nil},
		}
	}
	keys := "/kv/" + name + "/keys"
	return []hackEquivOp{
		{"list", "GET", keys, "", nil},
		{"get-own", "GET", keys + "/k1", "1", nil}, {"get-other", "GET", keys + "/k2", "2", nil},
		{"add", "PUT", keys + "/new", "new", `{"value":"new"}`},
		{"edit-own", "PUT", keys + "/k1", "1", `{"value":"changed"}`}, {"edit-other", "PUT", keys + "/k2", "2", `{"value":"changed"}`},
		{"delete-own", "DELETE", keys + "/k1", "1", nil},
	}
}

// The owner credential's own routes, which a page never reaches: raw SQL,
// the schema, and saving the resource itself.
func hackEquivOwnerOps(kind, name string) []hackEquivOp {
	save := hackEquivOp{"save-resource", "PUT", "/resources/" + name, "", map[string]string{"kind": kind, "preset": "public"}}
	if kind != "sqlite" {
		return []hackEquivOp{save}
	}
	return []hackEquivOp{
		{"sql-query", "POST", "/sqlite/" + name + "/query", "", map[string]string{"sql": "SELECT count(*) FROM items"}},
		{"sql-execute", "POST", "/sqlite/" + name + "/execute", "", map[string]string{"sql": "UPDATE items SET v='changed'"}},
		{"sql-schema", "POST", "/sqlite/" + name + "/schema", "", map[string]string{"sql": "CREATE TABLE more (id INTEGER PRIMARY KEY)"}},
		save,
	}
}

// A caller: a name and how one request of theirs is sent.
type hackEquivCaller struct {
	name string
	send func(method, path string, body any) resp
	// ownerRoutes: the caller holds a credential, so the owner's own routes
	// are tried too.
	ownerRoutes bool
}

func (s *hackEquivSite) page(name, uid string) hackEquivCaller {
	session := hackSession(s.t, s.a, uid, s.siteID, s.host)
	return hackEquivCaller{name: name, send: func(method, path string, body any) resp { return session(method, s.pageBase+path, body) }}
}

func (s *hackEquivSite) credential(name, key string) hackEquivCaller {
	return hackEquivCaller{name: name, ownerRoutes: true, send: func(method, path string, body any) resp {
		return s.a.api(s.t, method, s.ownerBase+path, body, key)
	}}
}

// same sends op as caller to both twins and fails on any difference in status
// or body. It returns the preset twin's answer as "status code".
func (s *hackEquivSite) same(scenario string, tw hackEquivTwins, caller hackEquivCaller, i int, ops func(kind, name string) []hackEquivOp) (answer string, wrote bool) {
	s.t.Helper()
	var got [2]resp
	var op hackEquivOp
	for twin, name := range tw.names {
		o := ops(tw.kind, name)[i]
		if twin == 0 {
			op = o
		}
		got[twin] = caller.send(o.method, o.path, o.body)
		// The visitor write limit is not a case here (the platform's test
		// has it); when the test's own pace reaches it, wait and ask again.
		for try := 0; got[twin].status == 429 && try < 100; try++ {
			time.Sleep(250 * time.Millisecond)
			got[twin] = caller.send(o.method, o.path, o.body)
		}
		// A broken fixture must never pass as "both refused alike".
		if got[twin].status >= 500 || got[twin].status == 429 {
			s.t.Fatalf("%s %s %s %s on %s: %d %s", scenario, tw.name, caller.name, op.name, name, got[twin].status, got[twin].body)
		}
		wrote = wrote || o.write() && got[twin].status < 300
	}
	a, b := hackEquivBody(got[0].body, tw.names[0]), hackEquivBody(got[1].body, tw.names[1])
	s.comparisons++
	if got[0].status != got[1].status || !reflect.DeepEqual(a, b) {
		if got[0].status != got[1].status || !hackEquivWordingApproved(a, b) {
			s.t.Fatalf("MISMATCH scenario=%s config=%s caller=%s operation=%s:\n  preset    %d %s\n  converted %d %s", scenario, tw.name, caller.name, op.name, got[0].status, got[0].body, got[1].status, got[1].body)
		}
		s.wording++
	}
	s.first[scenario+"/"+tw.name+"/"+caller.name+"/"+op.name] = got[0].status
	code := ""
	if m, ok := a.(map[string]any); ok {
		code, _ = m["code"].(string)
	}
	return strings.TrimSpace(fmt.Sprintf("%d %s", got[0].status, code)), wrote
}

// gate is one group 1 scenario: with the gate raised, every request of every
// caller is refused on both twins alike, with one of the pinned answers, and
// nothing is left changed once the gate is lowered.
func (s *hackEquivSite) gate(scenario string, twins []hackEquivTwins, callers []hackEquivCaller, writesOnly bool, raise, lower func(), want ...string) {
	s.t.Helper()
	before := map[string]any{}
	for _, tw := range twins {
		for _, name := range tw.names {
			before[name] = s.state(tw.kind, name)
		}
	}
	raise()
	seen := map[string]bool{}
	for _, tw := range twins {
		for _, caller := range callers {
			sets := []func(kind, name string) []hackEquivOp{hackEquivOps}
			if caller.ownerRoutes {
				sets = append(sets, hackEquivOwnerOps)
			}
			for _, ops := range sets {
				for i, op := range ops(tw.kind, tw.names[0]) {
					if writesOnly && !op.write() {
						// Compared, but the gate does not claim reads.
						s.same(scenario, tw, caller, i, ops)
						continue
					}
					answer, _ := s.same(scenario, tw, caller, i, ops)
					seen[answer] = true
					ok := false
					for _, w := range want {
						ok = ok || answer == w
					}
					if !ok {
						s.t.Errorf("gate %s, config %s, caller %s, %s: answered %q, want one of %v", scenario, tw.name, caller.name, op.name, answer, want)
					}
				}
			}
		}
	}
	lower()
	for _, w := range want {
		if !seen[w] {
			s.t.Errorf("gate %s never answered %q; it answered %v", scenario, w, hackEquivKeys(seen))
		}
	}
	for _, tw := range twins {
		for _, name := range tw.names {
			if !reflect.DeepEqual(before[name], s.state(tw.kind, name)) {
				s.t.Fatalf("gate %s: %s was changed by a refused request", scenario, name)
			}
		}
	}
}

// visitors is one group 2 scenario: every caller's every request gets the
// same answer on both twins and leaves the same data behind.
func (s *hackEquivSite) visitors(scenario string, twins []hackEquivTwins, callers []hackEquivCaller) {
	s.t.Helper()
	for _, tw := range twins {
		for _, caller := range callers {
			for i, op := range hackEquivOps(tw.kind, tw.names[0]) {
				_, wrote := s.same(scenario, tw, caller, i, hackEquivOps)
				if !op.write() {
					continue
				}
				s.asOwner(func() {
					if !reflect.DeepEqual(s.state(tw.kind, tw.names[0], op.entry), s.state(tw.kind, tw.names[1], op.entry)) {
						s.t.Fatalf("MISMATCH (data left behind) scenario=%s config=%s caller=%s operation=%s", scenario, tw.name, caller.name, op.name)
					}
					if wrote {
						s.restore(tw.kind, tw.names[0], op.entry)
						s.restore(tw.kind, tw.names[1], op.entry)
					}
				})
			}
		}
	}
}

func hackEquivKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestHackStorageRulesPresetEquivalence(t *testing.T) {
	t.Setenv("STORAGE_RULES", "on")
	t.Setenv("KV_STORE", "sqlite")
	a := newTeamSiteApp(t)
	a.sites.SetStorageIPLimiterForTest(100000, 0)
	org, member, rival, judge := a.newPerson(t, "he-org"), a.newPerson(t, "he-member"), a.newPerson(t, "he-rival"), a.newPerson(t, "he-judge")
	alice, bob := a.newPerson(t, "he-alice"), a.newPerson(t, "he-bob")
	slug := a.makeEvent(t, org)
	a.join(t, slug, member, org)
	a.join(t, slug, rival, org)
	judgeCode := a.api(t, "GET", "/v1/hack/events/"+slug, nil, org.key).json(t)["organiser"].(map[string]any)["judge_code"].(string)
	wantTS(t, "judge joins", a.api(t, "POST", "/v1/hack/judge/"+judgeCode, map[string]any{"accept_coc": true, "display_name": "Judge"}, judge.key), 200, "")
	team, _ := a.startTeam(t, slug, "Equivalence", member)
	rivalTeam, _ := a.startTeam(t, slug, "Rivals", rival)
	markReady(t, a.certDir, slug)
	key, rivalKey := a.teamKey(t, slug, member), a.teamKey(t, slug, rival)
	wantTS(t, "deploy team", a.deployTeam(t, team, key, "equivalence"), 201, "")
	wantTS(t, "deploy rival", a.deployTeam(t, rivalTeam, rivalKey, "rival"), 201, "")
	wantTS(t, "deploy event website", a.api(t, "PUT", "/v1/hack/events/"+slug+"/website/files?create=1", deployBody("event"), org.key), 201, "")
	var eventID string
	if err := a.database.QueryRow(`SELECT id FROM events WHERE slug=$1`, slug).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := a.database.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	authors := [2]string{a.uid(t, alice), a.uid(t, bob)}
	site := newHackEquivSite(t, a, "/v1/sites/"+team+"/storage", key, team, team+"."+slug+"."+tsDomain, hackSiteID(t, a, slug, team), authors)
	web := newHackEquivSite(t, a, "/v1/hack/events/"+slug+"/website/storage", org.key, slug, slug+"."+tsDomain, hackSiteID(t, a, slug, eventID), authors)

	// The twins. Group 2: the preset converted. Group 1: the open rules.
	var teamTwins, webTwins, teamOpen, webOpen []hackEquivTwins
	for _, c := range []struct{ kind, preset string }{{"sqlite", "private"}, {"sqlite", "records"}, {"sqlite", "public"}, {"kv", "private"}} {
		teamTwins = append(teamTwins, site.twins("", c.kind, c.preset, false))
	}
	for _, c := range []struct{ kind, preset string }{{"sqlite", "private"}, {"sqlite", "inbox"}, {"kv", "private"}} {
		webTwins = append(webTwins, web.twins("", c.kind, c.preset, false))
	}
	for _, kind := range []string{"sqlite", "kv"} {
		teamOpen = append(teamOpen, site.twins("open-", kind, "private", true))
		webOpen = append(webOpen, web.twins("open-", kind, "private", true))
	}

	teammate, otherTeam := site.page("teammate", a.uid(t, member)), site.page("participant-of-another-team", a.uid(t, rival))
	judgeOnSite, organiserOnSite := site.page("judge", a.uid(t, judge)), site.page("organiser", a.uid(t, org))
	anonymous, author := site.page("anonymous", ""), site.page("visitor-author", a.uid(t, alice))
	teamKey := site.credential("team-key", key)
	everyoneOnSite := []hackEquivCaller{teammate, otherTeam, judgeOnSite, organiserOnSite, anonymous, author}

	deadline := func(event, override string) func() {
		return func() {
			exec(`UPDATE events SET submission_deadline=`+event+` WHERE id=$1`, eventID)
			exec(`UPDATE event_teams SET deadline_override=`+override+` WHERE event_id=$1 AND slug=$2`, eventID, team)
		}
	}
	noDeadline := deadline("NULL", "NULL")
	stage := func(to string) func() { return func() { exec(`UPDATE events SET stage=$2 WHERE id=$1`, eventID, to) } }
	var running string
	if err := a.database.QueryRow(`SELECT stage FROM events WHERE id=$1`, eventID).Scan(&running); err != nil {
		t.Fatal(err)
	}
	// during makes site's own owner requests run with the state lifted.
	during := func(s *hackEquivSite, raise, lower func(), f func()) {
		raise()
		s.asOwner = func(g func()) { lower(); g(); raise() }
		f()
		lower()
		s.asOwner = func(g func()) { g() }
	}

	// --- Group 2 on the team site: who is owner there changes with the
	// deadline; nobody is refused by it.
	site.visitors("before-deadline", teamTwins, append(everyoneOnSite, teamKey))
	during(site, deadline("now() - interval '1 hour'", "NULL"), noDeadline, func() {
		site.visitors("after-deadline", teamTwins, everyoneOnSite)
	})
	during(site, deadline("now() - interval '1 hour'", "now() + interval '1 hour'"), noDeadline, func() {
		site.visitors("after-deadline-team-extended", teamTwins, []hackEquivCaller{teammate, otherTeam, anonymous})
	})
	during(site, deadline("now() - interval '2 hours'", "now() - interval '1 hour'"), noDeadline, func() {
		site.visitors("team-extension-over", teamTwins, []hackEquivCaller{teammate, anonymous})
	})

	// --- Group 2 on the event's website: an organiser is owner there until
	// the event is archived.
	organiserOnWeb, participantOnWeb := web.page("organiser", a.uid(t, org)), web.page("participant", a.uid(t, member))
	everyoneOnWeb := []hackEquivCaller{organiserOnWeb, participantOnWeb, web.page("anonymous", ""), web.page("visitor-author", a.uid(t, alice))}
	web.visitors("event-running", webTwins, append(everyoneOnWeb, web.credential("organiser-route", org.key)))
	during(web, stage("archived"), stage(running), func() {
		web.visitors("event-archived", webTwins, everyoneOnWeb)
	})

	// What loss of owner-on-site means, pinned on the preset twin.
	for _, pin := range []struct {
		s    *hackEquivSite
		key  string
		want int
	}{
		{site, "before-deadline/private-sqlite/teammate/list", 200},
		{site, "before-deadline/private-sqlite/teammate/edit-other", 200},
		{site, "before-deadline/private-kv/teammate/get-own", 200},
		{site, "before-deadline/private-sqlite/participant-of-another-team/list", 403},
		{site, "before-deadline/private-sqlite/judge/list", 403},
		{site, "before-deadline/private-sqlite/organiser/list", 403},
		{site, "before-deadline/private-sqlite/anonymous/list", 403},
		{site, "before-deadline/public-sqlite/anonymous/list", 200},
		{site, "before-deadline/records-sqlite/visitor-author/get-own", 200},
		{site, "before-deadline/private-sqlite/team-key/list", 200},
		{site, "after-deadline/private-sqlite/teammate/list", 403},
		{site, "after-deadline/private-kv/teammate/get-own", 403},
		{site, "after-deadline/public-sqlite/teammate/list", 200},
		{site, "after-deadline/public-sqlite/teammate/edit-other", 403},
		{site, "after-deadline/records-sqlite/visitor-author/get-own", 200},
		{site, "after-deadline-team-extended/private-sqlite/teammate/list", 200},
		{site, "after-deadline-team-extended/private-sqlite/participant-of-another-team/list", 403},
		{site, "team-extension-over/private-sqlite/teammate/list", 403},
		{web, "event-running/private-sqlite/organiser/list", 200},
		{web, "event-running/private-sqlite/participant/list", 403},
		{web, "event-running/inbox-sqlite/anonymous/add", 200},
		{web, "event-running/private-sqlite/organiser-route/list", 200},
		{web, "event-archived/private-sqlite/organiser/list", 403},
		{web, "event-archived/inbox-sqlite/anonymous/add", 200},
	} {
		if got, ok := pin.s.first[pin.key]; !ok || got != pin.want {
			t.Errorf("%s: preset twin answered %d (asked: %v), want %d", pin.key, got, ok, pin.want)
		}
	}
	if t.Failed() {
		return
	}

	// --- Group 1: real gates, against the open rules. First, that the rules
	// are open: with no gate raised the open twin lets a signed-out visitor
	// read and add and a signed-in one edit and delete, where its private
	// preset twin refuses all four. So below it is the gate that refuses.
	for _, c := range []struct {
		s      *hackEquivSite
		twins  []hackEquivTwins
		caller hackEquivCaller
		ops    []string
	}{
		{site, teamOpen, anonymous, []string{"list", "get-own", "add"}},
		{site, teamOpen, author, []string{"edit-other", "delete-own"}},
		{web, webOpen, web.page("anonymous", ""), []string{"list", "get-own", "add"}},
		{web, webOpen, web.page("visitor-author", a.uid(t, alice)), []string{"edit-other", "delete-own"}},
	} {
		for _, tw := range c.twins {
			for _, op := range hackEquivOps(tw.kind, tw.names[1]) {
				if !strings.Contains(" "+strings.Join(c.ops, " ")+" ", " "+op.name+" ") {
					continue
				}
				closed := hackEquivOps(tw.kind, tw.names[0])
				for _, o := range closed {
					if o.name == op.name {
						if r := c.caller.send(o.method, o.path, o.body); r.status < 400 {
							t.Fatalf("private preset twin %s let %s %s: %d %s", tw.names[0], c.caller.name, op.name, r.status, r.body)
						}
					}
				}
				if r := c.caller.send(op.method, op.path, op.body); r.status != 200 {
					t.Fatalf("open rules twin %s refused %s %s: %d %s", tw.names[1], c.caller.name, op.name, r.status, r.body)
				}
				if op.write() {
					c.s.restore(tw.kind, tw.names[1], op.entry)
				}
			}
		}
	}
	gates := 0
	gate := func(s *hackEquivSite, scenario string, twins []hackEquivTwins, callers []hackEquivCaller, writesOnly bool, raise, lower func(), want ...string) {
		t.Helper()
		gates++
		s.gate(scenario, twins, callers, writesOnly, raise, lower, want...)
	}
	none := func() {}
	// A team key: its writes stop at the deadline, in judging and on a
	// take-down (teamGate); another team's key never reaches the site.
	gate(site, "team-key-after-deadline", teamOpen, []hackEquivCaller{teamKey}, true, deadline("now() - interval '1 hour'", "NULL"), noDeadline, "409 submissions_closed")
	gate(site, "team-key-in-judging", teamOpen, []hackEquivCaller{teamKey}, true, stage("judging"), stage(running), "409 submissions_closed")
	takeDownTeam := func() {
		wantTS(t, "take down team site", a.api(t, "POST", "/v1/hack/events/"+slug+"/teams/"+team+"/takedown", map[string]string{"reason": "test"}, org.key), 200, "")
	}
	restoreTeam := func() {
		wantTS(t, "restore team site", a.api(t, "POST", "/v1/hack/events/"+slug+"/teams/"+team+"/restore", nil, org.key), 200, "")
	}
	gate(site, "team-key-on-take-down", teamOpen, []hackEquivCaller{teamKey}, true, takeDownTeam, restoreTeam, "403 team_site_taken_down")
	gate(site, "another-teams-key", teamOpen, []hackEquivCaller{site.credential("another-teams-key", rivalKey)}, false, none, none, "403 team_site_only")
	// The organiser-only event-website routes, called by a participant.
	gate(web, "organiser-only-route-by-a-participant", webOpen, []hackEquivCaller{web.credential("participant", member.key)}, false, none, none, "404 event_not_found")
	// An archived event's website routes take no change.
	gate(web, "archived-event-website", webOpen, []hackEquivCaller{web.credential("organiser-route", org.key)}, true, stage("archived"), stage(running), "409 event_closed")
	// A taken-down team site, a taken-down event and a suspended site refuse
	// every page request, the teammate's too.
	pages := []hackEquivCaller{teammate, anonymous, author}
	gate(site, "taken-down-team-site", teamOpen, pages, false, takeDownTeam, restoreTeam, "403 site_suspended")
	takeDownEvent := func() {
		wantTS(t, "take down event", a.api(t, "POST", "/v1/admin/hack/events/"+slug+"/takedown", map[string]string{"reason": "test"}, a.admin), 200, "")
	}
	restoreEvent := func() {
		wantTS(t, "restore event", a.api(t, "POST", "/v1/admin/hack/events/"+slug+"/restore", nil, a.admin), 200, "")
	}
	gate(site, "taken-down-event-team-site", teamOpen, append(pages, teamKey), false, takeDownEvent, restoreEvent, "403 site_suspended", "403 event_taken_down", "404 site_not_found")
	gate(web, "taken-down-event-website", webOpen, []hackEquivCaller{organiserOnWeb, participantOnWeb, web.page("anonymous", "")}, false, takeDownEvent, restoreEvent, "404 not_found")
	suspend := func(on string) func() {
		return func() { exec(`UPDATE sites SET suspended_at=`+on+` WHERE id=$1`, site.siteID) }
	}
	gate(site, "suspended-site", teamOpen, append(pages, teamKey), false, suspend("now()"), suspend("NULL"), "403 site_suspended", "403 team_site_taken_down")

	// The same organiser-only and archived gates through the connector's
	// event-website tools.
	clientID := a.registerClient(t, testRedirect)
	tool := func(access, name string, args map[string]any) (string, bool) {
		text, _, failed := toolResultOf(t, a.rpc(t, access, "tools/call", map[string]any{"name": name, "arguments": args}))
		return text, failed
	}
	tools := func(scenario, access string, writesOnly bool, want string) {
		t.Helper()
		gates++
		for _, tw := range webOpen {
			calls := []struct {
				name  string
				write bool
				args  map[string]any
			}{
				{"hack_event_storage_list_resources", false, map[string]any{}},
				{"hack_event_storage_set_resource", true, map[string]any{"body": map[string]any{"kind": tw.kind, "preset": "public"}}},
			}
			if tw.kind == "sqlite" {
				calls = append(calls,
					struct {
						name  string
						write bool
						args  map[string]any
					}{"hack_event_storage_sql_query", false, map[string]any{"sql": "SELECT count(*) FROM items"}},
					struct {
						name  string
						write bool
						args  map[string]any
					}{"hack_event_storage_sql_execute", true, map[string]any{"sql": "UPDATE items SET v='changed'"}})
			} else {
				calls = append(calls,
					struct {
						name  string
						write bool
						args  map[string]any
					}{"hack_event_storage_get_kv", false, map[string]any{"key": "k1"}},
					struct {
						name  string
						write bool
						args  map[string]any
					}{"hack_event_storage_put_kv", true, map[string]any{"key": "k1", "value": "changed"}})
			}
			for _, c := range calls {
				var text [2]string
				var failed [2]bool
				for twin, name := range tw.names {
					args := map[string]any{"event": slug}
					if c.name != "hack_event_storage_list_resources" {
						args["name"] = name
					}
					for k, v := range c.args {
						args[k] = v
					}
					text[twin], failed[twin] = tool(access, c.name, args)
					// The list names every resource, so it is the same call twice.
					if c.name != "hack_event_storage_list_resources" {
						text[twin] = fmt.Sprint(hackEquivBody([]byte(text[twin]), name))
					}
				}
				web.comparisons++
				if text[0] != text[1] || failed[0] != failed[1] {
					t.Fatalf("MISMATCH scenario=%s config=%s tool=%s:\n  preset    %v %s\n  converted %v %s", scenario, tw.name, c.name, failed[0], text[0], failed[1], text[1])
				}
				if (c.write || !writesOnly) && (!failed[0] || !strings.Contains(text[0], want)) {
					t.Fatalf("gate %s, config %s, tool %s: answered %v %q, want a refusal naming %q", scenario, tw.name, c.name, failed[0], text[0], want)
				}
			}
		}
	}
	before := map[string]any{}
	for _, tw := range webOpen {
		for _, name := range tw.names {
			before[name] = web.state(tw.kind, name)
		}
	}
	tools("organiser-only-tool-by-a-participant", a.connect(t, member, clientID, testRedirect)["access_token"].(string), false, "event_not_found")
	orgAccess := a.connect(t, org, clientID, testRedirect)["access_token"].(string)
	stage("archived")()
	tools("archived-event-website-tools", orgAccess, true, "event_closed")
	stage(running)()
	for _, tw := range webOpen {
		for _, name := range tw.names {
			if !reflect.DeepEqual(before[name], web.state(tw.kind, name)) {
				t.Fatalf("event-website tools: %s was changed by a refused call", name)
			}
		}
	}

	// "gate-summary:" lines of passing tests are printed by
	// scripts/lib/summarize-go-test.py, so this line is in the gate's record.
	t.Logf("gate-summary: R3-S5b hack group1_gates=%d group1_configurations=%d group2_configurations=%d comparisons=%d mismatches=0 refusal_wording_approved=%d",
		gates, len(teamOpen)+len(webOpen), len(teamTwins)+len(webTwins), site.comparisons+web.comparisons, site.wording+web.wording)
}
