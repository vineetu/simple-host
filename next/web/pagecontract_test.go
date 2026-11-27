package web_test

import (
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"simplehost/access/connector"
	"simplehost/access/keys"
	"simplehost/access/signin"
	"simplehost/access/teams"
	"simplehost/access/visitors"
	"simplehost/api"
	"simplehost/app"
	"simplehost/core/testkit"
	"simplehost/data/files"
	"simplehost/data/recipes"
	"simplehost/data/rows"
	datarules "simplehost/data/rules"
	datasql "simplehost/data/sql"
	"simplehost/data/values"
	"simplehost/mcp"
	"simplehost/ops/admin"
	"simplehost/ops/erase"
	"simplehost/ops/export"
	"simplehost/ops/holds"
	"simplehost/ops/notify"
	"simplehost/sites/domains"
	"simplehost/sites/locks"
	"simplehost/sites/manage"
	"simplehost/sites/publicpage"
	"simplehost/sites/publish"
	"simplehost/sites/serve"
	"simplehost/sites/stats"
	"simplehost/sites/uploads"
	"simplehost/web"
)

// The page contract: what a customer's page can receive. Its fixture,
// testdata/page-contract.json, holds the calls a page makes (the page
// helper's routes on a site's own address, and the same data routes with
// the owner's key on the platform), each in every sign-in state (anyone,
// two signed-in visitors, the owner signed in on the site, the owner's key,
// a named viewer, on an open, a passcode-locked and a named-viewer site),
// and for each the status, the headers a page reads, the fields of the
// answer with their types, and the values pages branch on (code, signed_in,
// site_owner, mine, changes, deleted). It also holds what the page helper's
// promise resolves to, or rejects with, for those answers.
//
// A CHANGE TO THE FIXTURE IS A CHANGE TO WHAT CUSTOMERS' PAGES RECEIVE.
// It needs a deliberate decision (and a line in RW2/DECISIONS.md), never a
// regenerate to make this test pass. Pages on 750 live sites read these
// fields: in November 2026 a lost site_owner told 41 owners they were not
// the owner. After such a decision: go test ./web -run TestPageContract -update
var update = flag.Bool("update", false, "rewrite testdata/page-contract.json from the service as it is")

const fixture = "testdata/page-contract.json"

var all = []func(*app.App){signin.Mount, keys.Mount, teams.Mount, connector.Mount, visitors.Mount, domains.Mount, locks.Mount,
	publish.Mount, uploads.Mount, manage.Mount, serve.Mount, stats.Mount, publicpage.Mount, rows.Mount, values.Mount, files.Mount,
	datasql.Mount, datarules.Mount, recipes.Mount, api.Mount, mcp.Mount, notify.Mount, export.Mount, erase.Mount, admin.Mount, holds.Mount, web.Mount}

// step is one call a page (or the owner's key) makes.
type step struct {
	ID     int    `json:"id"`
	Site   string `json:"site"`
	Caller string `json:"caller"`
	Method string `json:"method"`
	Path   string `json:"path"` // "me", "page", or a path under /v1/sites/{site}/storage
	Body   any    `json:"body,omitempty"`
	Ctype  string `json:"ctype,omitempty"`
	Label  string `json:"label"`
	Helper string `json:"helper,omitempty"` // the SH call that makes it, when one does
}

// answer is what the step received, as the contract pins it.
type answer struct {
	ID      int               `json:"id"`
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Shape   any               `json:"shape"`
	Values  map[string]any    `json:"values,omitempty"`
	Helper  any               `json:"helper,omitempty"` // the helper's resolved value (its shape) or {"rejects": code}
	body    any
}

type contract struct {
	Note    string   `json:"note"`
	Surface any      `json:"helper_surface"` // every SH name a page can call, and the stand-ins' answers
	Steps   []step   `json:"steps"`
	Answers []answer `json:"answers"`
}

func TestPageContract(t *testing.T) {
	raw, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	var want contract
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	got := run(t, want.Steps)
	surface := helperAnswers(t, want.Steps, got)
	if *update {
		want.Answers, want.Surface = got, surface
		b, _ := json.MarshalIndent(want, "", " ")
		if err := os.WriteFile(fixture, append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	if dump := os.Getenv("PAGE_CONTRACT_DUMP"); dump != "" {
		var bodies []map[string]any
		for _, a := range got {
			bodies = append(bodies, map[string]any{"id": a.ID, "status": a.Status, "headers": a.Headers, "body": a.body, "helper": a.Helper})
		}
		b, _ := json.MarshalIndent(bodies, "", " ")
		os.WriteFile(dump, b, 0o644)
	}
	if surface != nil && !reflect.DeepEqual(norm(want.Surface), norm(surface)) {
		wb, _ := json.Marshal(want.Surface)
		gb, _ := json.Marshal(surface)
		t.Errorf("the page helper's names changed\n want %s\n got  %s", wb, gb)
	}
	if len(want.Answers) != len(got) {
		t.Fatalf("fixture has %d answers for %d steps: run with -update after a deliberate decision", len(want.Answers), len(got))
	}
	for i, g := range got {
		w, s := want.Answers[i], want.Steps[i]
		g.body = nil
		if surface == nil {
			w.Helper = nil // no node: the server half only
		}
		if !reflect.DeepEqual(norm(w), norm(g)) {
			wb, _ := json.Marshal(w)
			gb, _ := json.Marshal(g)
			t.Errorf("%s as %s on %s (step %d): what pages receive changed\n want %s\n got  %s", s.Label, s.Caller, s.Site, s.ID, wb, gb)
		}
	}
}

// norm passes a value through JSON, so a fixture read back and an answer
// just made compare alike.
func norm(v any) any {
	b, _ := json.Marshal(v)
	var out any
	json.Unmarshal(b, &out)
	return out
}

// run seeds the same sites as the old service's comparison did and makes every call.
func run(t *testing.T, steps []step) []answer {
	k := testkit.Start(t, all...)
	ctx := context.Background()
	olive := k.Person(t, "olive")
	people := map[string]testkit.Who{"alice": k.Person(t, "alice"), "bob": k.Person(t, "bob"), "carol": k.Person(t, "carol"), "owner": olive}
	const platform = "https://simple-host.app"
	must := func(method, path string, body any) {
		t.Helper()
		if r := testkit.Call(t, k.Browser(), method, platform+path, body, "X-API-Key", olive.Key, "Content-Type", "application/json"); r.Status/100 != 2 {
			t.Fatalf("%s %s: %d %s", method, path, r.Status, r.Body)
		}
	}
	const entries = "CREATE TABLE entries(id INTEGER PRIMARY KEY, text TEXT, visitor_id TEXT, created_at TEXT, updated_at TEXT)"
	rule := func(read, add, edit, del string) json.RawMessage {
		b, _ := json.Marshal(map[string]any{"version": 1, "policies": []map[string]string{
			{"name": "read", "command": "read", "using": read}, {"name": "add", "command": "add", "check": add},
			{"name": "edit", "command": "edit", "using": edit}, {"name": "delete", "command": "delete", "using": del}}})
		return b
	}
	const owner, own, in = `auth.role == "owner"`, `visitor_id == auth.id || auth.role == "owner"`, "auth.signed_in"
	presets := map[string]json.RawMessage{
		"pub": rule("true", owner, owner, owner), "inbox": rule(owner, "true", owner, owner),
		"wall": rule("true", in, owner, own), "own": rule(own, in, own, own), "board": rule(in, in, in, owner),
	}
	urls := map[string]string{}
	browsers := map[string]map[string]*http.Client{}
	for _, site := range []string{"shop", "locked", "viewers"} {
		k.Publish(t, olive, site, map[string]string{"index.html": "<h1>" + site + "</h1>"})
		s, _ := k.App.Sites.Get(ctx, olive.ID, site)
		urls[site] = strings.TrimSuffix(k.App.Sites.URL(s), "/")
		st := "/v1/sites/" + site + "/storage/"
		sqlite := func(ns, preset string, extra ...string) {
			tables := map[string]json.RawMessage{"entries": presets[preset]}
			for _, sql := range append([]string{entries}, extra...) {
				must("POST", st+"sqlite/"+ns+"/execute", map[string]any{"sql": sql})
				if name := strings.SplitN(strings.Fields(sql)[2], "(", 2)[0]; name != "entries" {
					// no author column: the wall's add and read, the owner's delete
					tables[name] = rule("true", in, owner, owner)
				}
			}
			must("PUT", st+ns+"/rules", map[string]any{"rules": tables})
		}
		kv := func(ns, preset string) {
			must("PUT", st+ns+"/rules", map[string]any{"rules": map[string]json.RawMessage{"_values": presets[preset]}})
			must("PUT", st+"kv/"+ns+"/keys/seed", map[string]any{"value": "s0"})
		}
		sqlite("spub", "pub")
		sqlite("swall", "wall", "CREATE TABLE plain(id INTEGER PRIMARY KEY, text TEXT)", "CREATE TABLE uniq(id INTEGER PRIMARY KEY, text TEXT NOT NULL UNIQUE)")
		kv("kpub", "pub")
		must("POST", st+"sqlite/spub/tables/entries/rows", map[string]any{"text": "seed"})
		if site == "shop" {
			sqlite("sinbox", "inbox")
			sqlite("sown", "own")
			sqlite("sboard", "board")
			kv("kinbox", "inbox")
			kv("kwall", "wall")
			kv("kown", "own")
			for ns, preset := range map[string]string{"fpub": "pub", "fwall": "wall", "fown": "own"} {
				must("PUT", st+ns+"/rules", map[string]any{"rules": map[string]json.RawMessage{"_files": presets[preset]}})
			}
		}
		browsers[site] = map[string]*http.Client{"anon": k.Browser()}
		for name, who := range people {
			b := k.Browser()
			k.SignIn(t, b, who, urls[site])
			browsers[site][name] = b
		}
	}
	must("PATCH", "/v1/sites/locked", map[string]any{"passcode": "123456"})
	must("PATCH", "/v1/sites/viewers", map[string]any{"viewers": []string{people["carol"].Email}})

	var out []answer
	for _, s := range steps {
		address, header := urls[s.Site], []string{"X-SH-CSRF", "1"}
		switch s.Path {
		case "me":
			address += "/_sh/me"
		case "page":
			address += "/"
			header = nil
		default:
			address += "/v1/sites/" + s.Site + "/storage" + s.Path
		}
		b := browsers[s.Site][s.Caller]
		if s.Caller == "key" {
			address = platform + "/v1/sites/" + s.Site + "/storage" + s.Path
			header, b = []string{"X-API-Key", olive.Key}, k.Browser()
		}
		var body any = s.Body
		if s.Ctype != "" {
			header = append(header, "Content-Type", s.Ctype)
		} else if s.Body != nil {
			header = append(header, "Content-Type", "application/json")
		}
		if str, ok := s.Body.(string); ok {
			body = str
		}
		r := testkit.Call(t, b, s.Method, address, body, header...)
		a := answer{ID: s.ID, Status: r.Status, Headers: map[string]string{}}
		for _, name := range []string{"Content-Type", "Cache-Control", "Access-Control-Allow-Origin", "Access-Control-Allow-Credentials"} {
			if v := r.Header.Get(name); v != "" {
				a.Headers[name] = v
			}
		}
		if json.Unmarshal([]byte(r.Body), &a.body) != nil {
			a.body = "<text>"
		}
		a.Shape, a.Values = shape(a.body), pinned(a.body)
		out = append(out, a)
	}
	return out
}

// shape is a JSON value's fields and their types: an object's fields by
// name, an array as the merged shape of its items.
func shape(v any) any {
	switch x := v.(type) {
	case map[string]any:
		m := map[string]any{}
		for k, e := range x {
			m[k] = shape(e)
		}
		return m
	case []any:
		if len(x) == 0 {
			return []any{}
		}
		kinds := map[string]any{}
		for _, e := range x {
			b, _ := json.Marshal(shape(e))
			kinds[string(b)] = shape(e)
		}
		names := make([]string, 0, len(kinds))
		for n := range kinds {
			names = append(names, n)
		}
		sort.Strings(names)
		items := []any{}
		for _, n := range names {
			items = append(items, kinds[n])
		}
		return items
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "bool"
	case nil:
		return "null"
	}
	return "?"
}

// pinned are the values pages branch on.
func pinned(v any) map[string]any {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := map[string]any{}
	for _, k := range []string{"code", "signed_in", "site_owner", "mine", "changes", "deleted", "sign_in_available"} {
		if x, ok := m[k]; ok {
			out[k] = x
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// helperAnswers runs the page helper (static/auth.js) in node on each
// answer, as a page's call to it would receive it, and keeps the shape of
// what its promise resolves to, or the code it rejects with; it returns the
// helper's surface. Without node the helper half is skipped (the server
// half still runs).
func helperAnswers(t *testing.T, steps []step, got []answer) any {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Log("no node: the helper's answers are not checked")
		for i := range got {
			got[i].Helper = nil
		}
		return nil
	}
	in := []map[string]any{}
	for i, s := range steps {
		if s.Helper != "" {
			in = append(in, map[string]any{"id": s.ID, "helper": s.Helper, "status": got[i].Status, "headers": got[i].Headers, "body": got[i].body})
		}
	}
	b, _ := json.Marshal(in)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "in.json"), b, 0o644)
	cmd := exec.Command(node, "testdata/helper-run.js", "static/auth.js", filepath.Join(dir, "in.json"))
	res, err := cmd.Output()
	if err != nil {
		t.Fatalf("helper run: %v %s", err, res)
	}
	var outs []struct {
		ID     int `json:"id"`
		Result any `json:"result"`
	}
	if err := json.Unmarshal(res, &outs); err != nil {
		t.Fatalf("helper run: %v %s", err, res)
	}
	for _, o := range outs {
		got[o.ID].Helper = o.Result
	}
	res, err = exec.Command(node, "testdata/helper-run.js", "static/auth.js", "--surface").Output()
	var surface any
	if err != nil || json.Unmarshal(res, &surface) != nil {
		t.Fatalf("helper surface: %v %s", err, res)
	}
	return surface
}
