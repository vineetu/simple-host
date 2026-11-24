// Package journeys holds the four journey tests (SPEC section 7, 38 to 41):
// whole paths a person takes through the service, with every feature mounted.
package journeys_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"regexp"
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

// every feature, as main mounts them
var all = []func(*app.App){signin.Mount, keys.Mount, teams.Mount, connector.Mount, visitors.Mount, domains.Mount, locks.Mount,
	publish.Mount, uploads.Mount, manage.Mount, serve.Mount, stats.Mount, publicpage.Mount, rows.Mount, values.Mount, files.Mount,
	datasql.Mount, datarules.Mount, recipes.Mount, api.Mount, mcp.Mount, notify.Mount, export.Mount, erase.Mount, admin.Mount, holds.Mount, web.Mount}

const platform = "https://simple-host.app"

// person is one person in a journey: a browser, and their AI app.
type person struct {
	t   *testing.T
	k   *testkit.Kit
	who testkit.Who
	b   *http.Client
}

func as(t *testing.T, k *testkit.Kit, who testkit.Who) *person {
	return &person{t, k, who, k.Browser()}
}

// api calls a platform route with the person's key; want is the status.
func (p *person) api(method, path string, body any, want int) map[string]any {
	p.t.Helper()
	r := testkit.Call(p.t, p.b, method, platform+path, body, "Authorization", "Bearer "+p.who.Key, "Content-Type", "application/json")
	if r.Status != want {
		p.t.Fatalf("%s %s: %d %s, want %d", method, path, r.Status, r.Body, want)
	}
	var out map[string]any
	json.Unmarshal([]byte(r.Body), &out)
	return out
}

// tool calls an AI tool through /mcp; the result must not be a refusal.
func (p *person) tool(name string, args map[string]any) map[string]any {
	p.t.Helper()
	r := testkit.Call(p.t, p.b, "POST", platform+"/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": args}}, "Authorization", "Bearer "+p.who.Key)
	var out struct{ Result map[string]any }
	if json.Unmarshal([]byte(r.Body), &out); r.Status != 200 || out.Result == nil || out.Result["isError"] == true {
		p.t.Fatalf("%s: %d %s", name, r.Status, r.Body)
	}
	s, _ := out.Result["structuredContent"].(map[string]any)
	return s
}

// signUp signs a new person up with an emailed code and a handle, and makes
// a key for their AI app in the dashboard.
func signUp(t *testing.T, k *testkit.Kit, email, handle string) *person {
	p := &person{t, k, testkit.Who{Email: email, Handle: handle}, k.Browser()}
	testkit.Call(t, p.b, "POST", platform+"/v1/auth/code", map[string]string{"email": email})
	mails := k.Mail.To(email)
	code := regexp.MustCompile(`\d{6}`).FindString(mails[len(mails)-1].Text)
	r := testkit.Call(t, p.b, "POST", platform+"/v1/auth/verify", map[string]string{"email": email, "code": code, "handle": handle})
	var me struct{ ID string }
	if json.Unmarshal([]byte(r.Body), &me); r.Status != 200 || me.ID == "" {
		t.Fatalf("sign up: %d %s", r.Status, r.Body)
	}
	r = testkit.Call(t, p.b, "POST", platform+"/v1/keys", map[string]string{"name": "my AI app"})
	var key struct{ Key string }
	if json.Unmarshal([]byte(r.Body), &key); r.Status != 201 {
		t.Fatalf("key: %d %s", r.Status, r.Body)
	}
	p.who.ID, p.who.Key = me.ID, key.Key
	return p
}

func page(t *testing.T, b *http.Client, url string, status int, has string) {
	t.Helper()
	r := testkit.Call(t, b, "GET", url, nil, "User-Agent", "Mozilla/5.0")
	if r.Status != status || !strings.Contains(r.Body, has) {
		t.Fatalf("%s: %d %.200q, want %d with %q", url, r.Status, r.Body, status, has)
	}
}

// SPEC journey 38. Owner: sign up, publish a booking page from a recipe, a
// visitor books, the owner is notified, reads the booking, locks the site
// with a passcode, connects a domain.
func TestOwnerJourney(t *testing.T) {
	k := testkit.Start(t, all...)
	ctx := context.Background()
	omar := signUp(t, k, "omar@example.com", "omar")

	recipe := omar.tool("get_recipe", map[string]any{"topic": "personal", "site": "salon"})["text"].(string)
	_, html, _ := strings.Cut(recipe, "```html\n")
	html, _, _ = strings.Cut(html, "```")
	url := omar.tool("publish_site", map[string]any{"site": "salon", "files": map[string]string{"index.html": html}})["url"].(string)
	if url != "https://salon--omar.simple-host.site/" {
		t.Fatalf("address %s", url)
	}
	_, setup, _ := strings.Cut(recipe, "## Setup")
	setup, _, _ = strings.Cut(setup, "\n## ")
	for _, line := range strings.Split(setup, "\n") {
		name, args, _ := strings.Cut(line, " ")
		var in map[string]any
		if json.Unmarshal([]byte(args), &in) == nil {
			omar.tool(name, in)
		}
	}
	omar.tool("set_notifications", map[string]any{"site": "salon", "notifications": []any{map[string]any{"kind": "data", "namespace": "bookings", "target": "bookings"}}})
	page(t, k.Browser(), url, 200, "Book a time")

	// A visitor books.
	vera := k.Person(t, "vera")
	visitor := k.Browser()
	k.SignIn(t, visitor, vera, url)
	r := testkit.Call(t, visitor, "POST", url+"v1/sites/salon/storage/sqlite/bookings/tables/bookings/rows",
		map[string]string{"day": "2026-12-01", "time": "10:00", "name": "Vera", "status": "requested"}, "X-SH-CSRF", "1")
	if r.Status != 200 {
		t.Fatalf("booking: %d %s", r.Status, r.Body)
	}
	// The owner is told, and reads it.
	if events, _ := notify.Feed(ctx, k.App, omar.who.ID, 0); len(events) != 1 || !strings.Contains(string(events[0].Body), "bookings") {
		t.Fatalf("omar's events: %v", events)
	}
	rows := omar.tool("run_sql", map[string]any{"site": "salon", "namespace": "bookings", "sql": "SELECT name, day FROM bookings"})
	if b, _ := json.Marshal(rows); !strings.Contains(string(b), "Vera") {
		t.Fatalf("bookings: %s", b)
	}
	// Locks the site with a passcode (the in-chat screen's call).
	omar.tool("set_site_access", map[string]any{"site": "salon", "access": "passcode", "passcode": "salon-door-42"})
	page(t, k.Browser(), url, 401, "passcode")
	// Connects a domain: the records to add, then live once DNS is right.
	k.DNS.Set("simple-host.app", "A", "203.0.113.7")
	site := omar.tool("set_site_address", map[string]any{"site": "salon", "domain": "salon.example.com"})
	d := site["domain"].(map[string]any)
	records := d["records"].([]any)
	if d["status"] != "waiting" || len(records) < 2 {
		t.Fatalf("domain: %v", d)
	}
	k.DNS.Set("salon.example.com", "A", "203.0.113.7")
	k.DNS.Set("_simple-host.salon.example.com", "TXT", records[1].(map[string]any)["value"].(string))
	if err := domains.Run(ctx, k.App); err != nil {
		t.Fatal(err)
	}
	if d := omar.tool("get_site", map[string]any{"site": "salon"})["domain"].(map[string]any); d["status"] != "live" {
		t.Fatalf("domain after DNS: %v", d)
	}
	page(t, k.Browser(), "https://salon.example.com/", 401, "passcode")
}

// SPEC journey 39. Team member: accept an invite, publish to the team site,
// roll it back, set named viewers.
func TestTeamMemberJourney(t *testing.T) {
	k := testkit.Start(t, all...)
	tara, mo := as(t, k, k.Person(t, "tara")), as(t, k, k.Person(t, "mo"))
	tara.api("POST", "/v1/teams", map[string]string{"handle": "studio"}, 201)
	tara.api("POST", "/v1/teams/studio/invites", map[string]string{"email": mo.who.Email}, 201)
	mo.api("PUT", "/v1/sites/work?owner=studio", map[string]any{"files": map[string]string{"index.html": "x"}}, 403) // not yet a member
	invites := mo.api("GET", "/v1/me", nil, 200)["invitations"].([]any)
	mo.api("POST", "/v1/invites/"+invites[0].(map[string]any)["id"].(string)+"/accept", map[string]any{}, 200)

	url := mo.api("PUT", "/v1/sites/work?owner=studio", map[string]any{"files": map[string]string{"index.html": "first"}}, 200)["url"].(string)
	mo.api("PUT", "/v1/sites/work?owner=studio", map[string]any{"files": map[string]string{"index.html": "second"}}, 200)
	page(t, k.Browser(), url, 200, "second")
	mo.api("PATCH", "/v1/sites/work?owner=studio", map[string]any{"live_version": 1}, 200)
	page(t, k.Browser(), url, 200, "first")
	site := mo.api("PATCH", "/v1/sites/work?owner=studio", map[string]any{"viewers": []string{"client@example.com"}}, 200)
	if site["access"] != "viewers" || len(site["viewers"].([]any)) != 1 {
		t.Fatalf("viewers: %v", site)
	}
	page(t, k.Browser(), url, 401, "Sign in")
	// The team's members pass the gate.
	member := k.Browser()
	k.SignIn(t, member, tara.who, url)
	page(t, member, url, 200, "first")
}

// SPEC journey 40. Visitor: open a named-viewer site, ask for access, get
// listed, sign in, add an entry, then use their own AI app to change it.
func TestVisitorJourney(t *testing.T) {
	k := testkit.Start(t, all...)
	ctx := context.Background()
	ann := as(t, k, k.Person(t, "ann"))
	ann.api("PUT", "/v1/sites/notes", map[string]any{"files": map[string]string{"index.html": "notes"}}, 200)
	ann.api("POST", "/v1/sites/notes/storage/sqlite/wall/execute", map[string]any{"sql": "CREATE TABLE entries (id INTEGER PRIMARY KEY, body TEXT, visitor_id TEXT)"}, 200)
	ann.api("PUT", "/v1/sites/notes/storage/wall/rules", `{"rules": {"entries": {"version": 1, "policies": [{"name": "read", "command": "read", "using": "true"},
		{"name": "add", "command": "add", "check": "auth.signed_in"}, {"name": "edit own", "command": "edit", "using": "visitor_id == auth.id", "check": "visitor_id == auth.id"}]}}}`, 200)
	ann.api("PATCH", "/v1/sites/notes", map[string]any{"viewers": []string{"someone@example.com"}, "notifications": []any{map[string]string{"kind": "access"}}}, 200)
	url := "https://notes--ann.simple-host.site/"

	vic := as(t, k, k.Person(t, "vic"))
	page(t, vic.b, url, 401, "Sign in")
	k.SignIn(t, vic.b, vic.who, url)
	page(t, vic.b, url, 403, "Ask for access")
	r := testkit.Call(t, vic.b, "POST", url+"_sh/ask-access", "next=/", "Content-Type", "application/x-www-form-urlencoded")
	if r.Status != 200 {
		t.Fatalf("ask for access: %d %s", r.Status, r.Body)
	}
	if events, _ := notify.Feed(ctx, k.App, ann.who.ID, 0); len(events) != 1 || !strings.Contains(string(events[0].Body), vic.who.Email) {
		t.Fatalf("ann's events: %v", events)
	}
	ann.api("PATCH", "/v1/sites/notes", map[string]any{"viewers_add": []string{vic.who.Email}}, 200)
	page(t, vic.b, url, 200, "notes")
	r = testkit.Call(t, vic.b, "POST", url+"v1/sites/notes/storage/sqlite/wall/tables/entries/rows", map[string]string{"body": "hello"}, "X-SH-CSRF", "1")
	if r.Status != 200 {
		t.Fatalf("entry: %d %s", r.Status, r.Body)
	}
	// Vic's own AI app changes his entry, as him.
	vic.tool("data", map[string]any{"site": url, "method": "PATCH", "path": "storage/sqlite/wall/tables/entries/rows/1", "body": map[string]string{"body": "hello, edited"}})
	got := vic.tool("data", map[string]any{"site": url, "path": "storage/sqlite/wall/tables/entries/rows/1"})
	if b, _ := json.Marshal(got); !strings.Contains(string(b), "hello, edited") {
		t.Fatalf("after the edit: %s", b)
	}
}

// SPEC journey 41. Operator: a self-hosted install's first-run setup page
// checks DNS and makes the first account the operator; the operator reads the
// admin overview, opens a reported site's data, suspends the account and
// restores it, then makes an account that publishes its first site.
func TestOperatorJourney(t *testing.T) {
	k := testkit.Start(t, all...)
	b := k.Browser()
	page(t, b, platform+"/setup", 404, "") // the hosted service has no setup page
	k.App.Config.SelfHosted = true
	var logged bytes.Buffer
	log.SetOutput(&logged)
	page(t, b, platform+"/setup", 200, "Finish setup")
	log.SetOutput(os.Stderr)
	code := regexp.MustCompile(`setup code: (\w+)`).FindStringSubmatch(logged.String())
	if code == nil {
		t.Fatalf("no setup code in the log: %q", logged.String())
	}
	setup := func(body any) map[string]any {
		r := testkit.Call(t, b, "POST", platform+"/setup", body, "Content-Type", "application/json")
		var out map[string]any
		if json.Unmarshal([]byte(r.Body), &out); r.Status != 200 {
			t.Fatalf("setup: %d %s", r.Status, r.Body)
		}
		return out
	}
	if out := setup(map[string]any{"email": "op@example.com"}); out["ok"] != false || out["next"] != nil {
		t.Fatalf("setup before DNS: %v", out)
	}
	k.DNS.Set("simple-host.app", "A", "203.0.113.7")
	k.DNS.Set("setup-check--x.simple-host.site", "A", "203.0.113.7")
	if r := testkit.Call(t, b, "POST", platform+"/setup", map[string]any{"email": "mallory@example.com", "code": "guess"}); r.Status != 403 {
		t.Fatalf("setup without the code: %d %s", r.Status, r.Body)
	}
	if out := setup(map[string]any{"email": "op@example.com", "code": code[1]}); out["ok"] != true || out["next"] != "/dashboard" {
		t.Fatalf("setup: %v", out)
	}
	page(t, b, platform+"/setup", 404, "") // gone once there is an operator
	r := testkit.Call(t, b, "POST", platform+"/v1/keys", map[string]string{"name": "admin"}, "Content-Type", "application/json")
	var key struct{ Key string }
	json.Unmarshal([]byte(r.Body), &key)
	op := &person{t, k, testkit.Who{Email: "op@example.com", Key: key.Key}, b}
	if me := op.api("GET", "/v1/me", nil, 200); me["operator"] != true {
		t.Fatalf("setup did not make the operator: %v", me)
	}

	nina := as(t, k, k.Person(t, "nina"))
	nina.api("PUT", "/v1/sites/promo", map[string]any{"files": map[string]string{"index.html": "promo"}}, 200)
	nina.api("POST", "/v1/sites/promo/storage/sqlite/leads/execute", map[string]any{"sql": "CREATE TABLE leads (email TEXT)"}, 200)
	url := "https://promo--nina.simple-host.site/"
	testkit.Call(t, k.Browser(), "POST", platform+"/v1/reports", map[string]string{"url": url + "win", "reason": "Asks for bank details."})

	nina.api("GET", "/v1/admin/overview", nil, 403)
	o := op.api("GET", "/v1/admin/overview", nil, 200)
	if reports := o["reports"].([]any); len(reports) != 1 || reports[0].(map[string]any)["site"] != "promo" || o["growth"] == nil {
		t.Fatalf("overview: %v", o)
	}
	// The operator opens the site's data through the ordinary routes.
	op.api("GET", "/v1/sites/promo?owner=nina", nil, 200)
	op.api("POST", "/v1/sites/promo/storage/sqlite/leads/query?owner=nina", map[string]any{"sql": "SELECT count(*) FROM leads"}, 200)
	// Suspend: every site is taken down and the account's key is refused.
	op.api("PATCH", "/v1/admin/people/"+nina.who.ID, map[string]any{"suspended": true, "reason": "Phishing."}, 200)
	page(t, k.Browser(), url, 410, "taken down")
	nina.api("GET", "/v1/me", nil, 403)
	op.api("PATCH", "/v1/admin/people/"+nina.who.ID, map[string]any{"suspended": false}, 200)
	page(t, k.Browser(), url, 200, "promo")
	nina.api("GET", "/v1/me", nil, 200)

	// The operator makes an account; it signs in and publishes its first site.
	op.api("POST", "/v1/admin/people", map[string]string{"email": "first@example.com"}, 200)
	first := signUp(t, k, "first@example.com", "first")
	site := first.tool("publish_site", map[string]any{"site": "hello", "files": map[string]string{"index.html": "my first site"}})
	page(t, k.Browser(), site["url"].(string), 200, "my first site")
}
