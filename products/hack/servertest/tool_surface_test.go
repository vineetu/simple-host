package servertest

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/extension"
	accountrepo "github.com/vsriram/simple-host/internal/repository/accounts"
	"github.com/vsriram/simple-host/internal/server"
	platformtest "github.com/vsriram/simple-host/internal/server/servertest"
	"github.com/vsriram/simple-host/products/hack"
	"github.com/vsriram/simple-host/products/hack/testdb"
)

// The tests in this file are the things an identical tools/list does not
// prove once Simple Hack's tools and screens come from the product: who may
// call what, which screens a connection can list and read, what discovery
// reaches, and that a write made through a tool still has its side effects.
// Each runs against the built program, as Simple Host (no product) and as
// Simple Hack, for every kind of connection an API key gives; the connection
// kinds an OAuth grant gives are covered on the composed handlers below.

// toolSurface is the built program for one product.
type toolSurface struct {
	t   *testing.T
	app *server.App
	cfg config.Config
	db  *sql.DB
}

func buildToolSurface(t *testing.T, product string) *toolSurface {
	t.Helper()
	dsn := testdb.Fresh(t)
	cfg := platformtest.Config(t, dsn)
	var exts []*extension.Extension
	if product == "hack" {
		exts = []*extension.Extension{hack.Extension()}
	}
	app := platformtest.Build(t, cfg, exts)
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return &toolSurface{t: t, app: app, cfg: cfg, db: db}
}

// person is an account with a full key; the returned id is its user id.
func (s *toolSurface) person(name string) (id, key string) {
	s.t.Helper()
	key, _ = auth.GenerateAPIKey()
	u, err := accountrepo.CreateUser(context.Background(), s.db, name+"@example.com", key, false)
	if err != nil {
		s.t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE users SET handle = $1 WHERE id = $2`, name, u.ID); err != nil {
		s.t.Fatal(err)
	}
	return u.ID, key
}

// deployKey is a deploy-only key of the same account.
func (s *toolSurface) deployKey(userID string) string {
	s.t.Helper()
	key, _ := auth.GenerateAPIKey()
	if _, err := s.db.Exec(`INSERT INTO api_keys(key_hash,user_id,scope) VALUES($1,$2,'deploy')`, accountrepo.HashAPIKey(key), userID); err != nil {
		s.t.Fatal(err)
	}
	return key
}

func (s *toolSurface) rest(method, path string, body any, key string) (int, map[string]any) {
	s.t.Helper()
	var rd *strings.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = strings.NewReader(string(b))
	} else {
		rd = strings.NewReader("")
	}
	req := httptest.NewRequest(method, s.cfg.PublicBaseURL+path, rd)
	req.Header.Set("X-API-Key", key)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.app.Handler.ServeHTTP(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

// rpc sends one MCP request as the holder of key. ui declares MCP Apps
// support the way an initialize-era client does, once, before the request.
func (s *toolSurface) rpc(key, method string, params map[string]any, ui bool) map[string]any {
	s.t.Helper()
	send := func(method string, params map[string]any) map[string]any {
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
		req := httptest.NewRequest(http.MethodPost, s.cfg.PublicBaseURL+"/mcp", strings.NewReader(string(body)))
		req.Header.Set("X-API-Key", key)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("MCP-Protocol-Version", "2025-06-18")
		w := httptest.NewRecorder()
		s.app.Handler.ServeHTTP(w, req)
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			s.t.Fatalf("%s: not JSON (%d): %s", method, w.Code, w.Body.String())
		}
		return out
	}
	extensions := map[string]any{}
	if ui {
		extensions["io.modelcontextprotocol/ui"] = map[string]any{}
	}
	send("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"extensions": extensions}, "clientInfo": map[string]any{"name": "fixture", "version": "1"}})
	if params == nil {
		params = map[string]any{}
	}
	return send(method, params)
}

// call is one tools/call. unknown reports a tool this connection does not
// have (a protocol error, never a tool result); otherwise failed is isError.
func (s *toolSurface) call(key, name string, args map[string]any) (text string, structured map[string]any, failed, unknown bool) {
	s.t.Helper()
	if args == nil {
		args = map[string]any{}
	}
	out := s.rpc(key, "tools/call", map[string]any{"name": name, "arguments": args}, false)
	if e, ok := out["error"].(map[string]any); ok {
		message, _ := e["message"].(string)
		if !strings.Contains(message, "unknown tool") {
			s.t.Fatalf("%s: protocol error %v", name, e)
		}
		return message, nil, true, true
	}
	res := out["result"].(map[string]any)
	text = res["content"].([]any)[0].(map[string]any)["text"].(string)
	structured, _ = res["structuredContent"].(map[string]any)
	return text, structured, res["isError"].(bool), false
}

func (s *toolSurface) listed(key string, ui bool) map[string]bool {
	s.t.Helper()
	names := map[string]bool{}
	for _, raw := range s.rpc(key, "tools/list", nil, ui)["result"].(map[string]any)["tools"].([]any) {
		names[raw.(map[string]any)["name"].(string)] = true
	}
	return names
}

// hackTeam makes an open event with one team and returns the organiser's
// key, a participant's key and the participant's team key.
func (s *toolSurface) hackTeam() (organiser, participant, team string) {
	s.t.Helper()
	_, organiser = s.person("surface-org")
	_, participant = s.person("surface-member")
	slug := "surface-" + strings.ToLower(strings.ReplaceAll(time.Now().UTC().Format("150405.000"), ".", ""))
	if code, out := s.rest("POST", "/v1/hack/events", map[string]any{
		"slug": slug, "title": "Tool surface", "organiser_name": "Org", "contact_email": "org@example.com",
		"purpose": "testing", "expected_participants": 10, "starts_at": "2026-10-01", "time_zone": "UTC",
	}, organiser); code != http.StatusCreated {
		s.t.Fatalf("create event: %d %v", code, out)
	}
	if code, out := s.rest("POST", "/v1/hack/events/"+slug+"/stage", map[string]string{"stage": "open"}, organiser); code != http.StatusOK {
		s.t.Fatalf("open event: %d %v", code, out)
	}
	_, ev := s.rest("GET", "/v1/hack/events/"+slug, nil, organiser)
	join := ev["organiser"].(map[string]any)["join_code"].(string)
	if code, out := s.rest("POST", "/v1/hack/join/"+join, map[string]any{"accept_coc": true, "display_name": "P"}, participant); code != http.StatusOK && code != http.StatusCreated {
		s.t.Fatalf("join: %d %v", code, out)
	}
	if code, out := s.rest("POST", "/v1/hack/events/"+slug+"/teams", map[string]string{"name": "Surface"}, participant); code != http.StatusCreated {
		s.t.Fatalf("team: %d %v", code, out)
	}
	code, out := s.rest("POST", "/v1/hack/events/"+slug+"/key", nil, participant)
	if code != http.StatusCreated {
		s.t.Fatalf("team key: %d %v", code, out)
	}
	return organiser, participant, out["key"].(string)
}

// surfaceConnection is one kind of connection an API key gives.
type surfaceConnection struct {
	name, key string
	// personal: the product's personal connection (its own tools, no website
	// tools); teamSite: a website connection of the product; neither: Simple Host.
	personal, teamSite, deployOnly bool
}

func surfaceConnections(t *testing.T, s *toolSurface, product string) []surfaceConnection {
	t.Helper()
	if product == "host" {
		id, key := s.person("surface-owner")
		return []surfaceConnection{
			{name: "host personal key", key: key},
			{name: "host deploy-only key", key: s.deployKey(id), deployOnly: true},
		}
	}
	_, participant, team := s.hackTeam()
	var participantID string
	if err := s.db.QueryRow(`SELECT id FROM users WHERE handle = 'surface-member'`).Scan(&participantID); err != nil {
		t.Fatal(err)
	}
	return []surfaceConnection{
		{name: "hack personal key", key: participant, personal: true},
		{name: "hack team key", key: team, teamSite: true},
		{name: "hack deploy-only key", key: s.deployKey(participantID), teamSite: true, deployOnly: true},
	}
}

func TestToolCallAuthorisationByConnection(t *testing.T) {
	for _, product := range []string{"host", "hack"} {
		t.Run(product, func(t *testing.T) {
			s := buildToolSurface(t, product)
			for _, c := range surfaceConnections(t, s, product) {
				t.Run(c.name, func(t *testing.T) {
					listed := s.listed(c.key, false)
					// A personal tool of the product: only the product's personal connection has it.
					for _, name := range []string{"hack_list_events", "hack_get_my_teams", "hack_select_team"} {
						args := map[string]any{}
						if name == "hack_select_team" {
							args["team_id"] = "00000000-0000-0000-0000-000000000000"
						}
						text, _, failed, unknown := s.call(c.key, name, args)
						if unknown == c.personal || listed[name] != c.personal {
							t.Fatalf("%s: listed=%t unknown=%t: %s", name, listed[name], unknown, text)
						}
						if c.personal && name != "hack_select_team" && failed {
							t.Fatalf("%s refused on a personal connection: %s", name, text)
						}
					}
					// A website tool: every connection but the product's personal one.
					text, _, failed, unknown := s.call(c.key, "list_sites", nil)
					if unknown != c.personal || listed["list_sites"] == c.personal {
						t.Fatalf("list_sites: listed=%t unknown=%t: %s", listed["list_sites"], unknown, text)
					}
					if !c.personal && failed {
						t.Fatalf("list_sites refused: %s", text)
					}
					// A deploy-only key has the deploy tools and nothing else.
					_, _, _, noNotifications := s.call(c.key, "list_notifications", nil)
					if c.deployOnly && (!noNotifications || listed["list_notifications"]) {
						t.Fatal("a deploy-only key reached list_notifications")
					}
					// What a deploy-only key may not do is refused where the key is
					// checked, whichever tool asks.
					if c.deployOnly {
						if text, _, failed, _ := s.call(c.key, "who_am_i", nil); !failed || !strings.Contains(text, "[deploy_only_key]") {
							t.Fatalf("a deploy-only key read the account: %s", text)
						}
					}
					// No Simple Hack tool is listed to Simple Host, and no connection
					// of either product has both families.
					hackNames := 0
					for name := range listed {
						if strings.HasPrefix(name, "hack_") {
							hackNames++
						}
					}
					if (hackNames > 0) != c.personal {
						t.Fatalf("%d hack_ tools listed", hackNames)
					}
				})
			}
		})
	}
}

// A personal connection made through the connector (an OAuth grant) has the
// website tools too, but none runs before a team is selected; the personal
// tools run at once.
func TestToolCallNeedsSelectedTeam(t *testing.T) {
	a := newTeamSiteApp(t)
	org, participant := a.newPerson(t, "sel-org"), a.newPerson(t, "sel-member")
	access := a.connect(t, participant, a.registerClient(t, testRedirect), testRedirect)["access_token"].(string)
	slug := a.makeEvent(t, org)
	a.join(t, slug, participant, org)
	teamSlug, _ := a.startTeam(t, slug, "Selected", participant)
	call := func(name string, args map[string]any) (string, map[string]any, bool) {
		return toolResultOf(t, a.rpc(t, access, "tools/call", map[string]any{"name": name, "arguments": args}))
	}
	for name, args := range map[string]map[string]any{
		"get_site":       {"site": teamSlug},
		"update_site":    {"site": teamSlug, "files": map[string]any{"index.html": "<h1>x</h1>"}},
		"storage_put_kv": {"site": teamSlug, "name": "settings", "key": "a", "value": 1},
	} {
		text, structured, failed := call(name, args)
		if !failed || structured != nil || text != "Choose a team with hack_select_team before publishing its site." {
			t.Fatalf("%s before a team was selected: %q %v", name, text, structured)
		}
	}
	if text, _, failed := call("hack_list_events", map[string]any{}); failed {
		t.Fatalf("personal tool refused: %s", text)
	}
	var teamID string
	if err := a.database.QueryRow(`SELECT t.id FROM event_teams t JOIN events e ON e.id=t.event_id WHERE e.slug=$1 AND t.slug=$2`, slug, teamSlug).Scan(&teamID); err != nil {
		t.Fatal(err)
	}
	if text, _, failed := call("hack_select_team", map[string]any{"team_id": teamID}); failed {
		t.Fatalf("select team: %s", text)
	}
	if text, _, failed := call("list_sites", map[string]any{}); failed {
		t.Fatalf("website tool refused after selecting a team: %s", text)
	}
}

// resources/list and resources/read for each screen, with the screens on and
// off, for every connection kind of both products.
func TestScreenResourcesByConnection(t *testing.T) {
	hackScreens := []string{"ui://simple-hack/judge", "ui://simple-hack/organiser"}
	const mySites = "ui://simple-host/my-sites"
	for _, product := range []string{"host", "hack"} {
		for _, screens := range []string{"off", "on"} {
			t.Run(product+"/screens="+screens, func(t *testing.T) {
				for _, flag := range []string{"MCP_APP_HACK_JUDGE", "MCP_APP_HACK_ORGANISER", "MCP_APP_MY_SITES"} {
					t.Setenv(flag, screens)
				}
				s := buildToolSurface(t, product)
				for _, c := range surfaceConnections(t, s, product) {
					for _, ui := range []bool{false, true} {
						listed := map[string]bool{}
						for _, raw := range s.rpc(c.key, "resources/list", nil, ui)["result"].(map[string]any)["resources"].([]any) {
							listed[raw.(map[string]any)["uri"].(string)] = true
						}
						read := func(uri string) (string, bool) {
							out := s.rpc(c.key, "resources/read", map[string]any{"uri": uri}, ui)
							if out["error"] != nil {
								return "", false
							}
							return out["result"].(map[string]any)["contents"].([]any)[0].(map[string]any)["text"].(string), true
						}
						// Simple Hack's screens: its personal connection only, with
						// the screens on and MCP Apps support declared.
						wantHack := product == "hack" && c.personal && screens == "on" && ui
						for _, uri := range hackScreens {
							page, ok := read(uri)
							if listed[uri] != wantHack || ok != wantHack {
								t.Fatalf("%s ui=%t %s: listed=%t read=%t, want %t", c.name, ui, uri, listed[uri], ok, wantHack)
							}
							if ok && (!strings.Contains(page, "<script") || strings.Contains(page, "__BUNDLE__") || strings.Contains(page, "__BRIDGE__") || strings.Contains(page, "__ORIGIN__")) {
								t.Fatalf("%s: %s not rendered", c.name, uri)
							}
						}
						// The platform's My sites screen: every website connection, and
						// on Simple Hack it carries the product's script and name.
						wantSites := screens == "on" && !c.personal
						page, ok := read(mySites)
						if listed[mySites] != wantSites || ok != wantSites {
							t.Fatalf("%s ui=%t my-sites: listed=%t read=%t, want %t", c.name, ui, listed[mySites], ok, wantSites)
						}
						if ok {
							hackPage := strings.Contains(page, "SHHackSites") && strings.Contains(page, "Simple Hack")
							if hackPage != (product == "hack") || strings.Contains(page, "__HACK__") || strings.Contains(page, "__PRODUCT__") {
								t.Fatalf("%s: my-sites page for the wrong product", c.name)
							}
						}
					}
				}
			})
		}
	}
}

// find_tools and run_tool belong to Simple Host's connections, which search
// and run Simple Host's tools only. A contributed tool is reached by its own
// name on the product's connections, which list every tool and have neither
// discovery tool; a name the product retired is answered by the product.
func TestDiscoveryAndContributedTools(t *testing.T) {
	const retired = "Judging locks when you finish judging (move to Results) and unlocks when you move the event back. [judging_lock_removed]"
	for _, product := range []string{"host", "hack"} {
		t.Run(product, func(t *testing.T) {
			s := buildToolSurface(t, product)
			for _, c := range surfaceConnections(t, s, product) {
				t.Run(c.name, func(t *testing.T) {
					listed := s.listed(c.key, false)
					hostConnection := product == "host"
					if listed["find_tools"] != hostConnection || listed["run_tool"] != hostConnection {
						t.Fatalf("discovery listed: find_tools=%t run_tool=%t", listed["find_tools"], listed["run_tool"])
					}
					if hostConnection {
						for _, query := range []string{"hackathon event team judge", "hack_list_events", "select a team"} {
							_, structured, failed, _ := s.call(c.key, "find_tools", map[string]any{"query": query})
							if failed {
								t.Fatalf("find_tools %q failed", query)
							}
							for _, raw := range structured["tools"].([]any) {
								if name := raw.(map[string]any)["name"].(string); strings.HasPrefix(name, "hack_") {
									t.Fatalf("find_tools %q returned %s", query, name)
								}
							}
						}
						text, _, failed, _ := s.call(c.key, "run_tool", map[string]any{"name": "hack_list_events", "arguments": map[string]any{}})
						if !failed || !strings.Contains(text, "Unknown tool: hack_list_events") {
							t.Fatalf("run_tool reached a Simple Hack tool: %s", text)
						}
						if text, _, failed, _ := s.call(c.key, "run_tool", map[string]any{"name": "list_sites", "arguments": map[string]any{}}); failed {
							t.Fatalf("run_tool list_sites: %s", text)
						}
						// No product, no retired names: an unknown tool like any other.
						if text, _, _, unknown := s.call(c.key, "hack_lock_judging", nil); !unknown {
							t.Fatalf("Simple Host answered a retired Simple Hack name: %s", text)
						}
						return
					}
					// The product's connections list every tool they have.
					for _, name := range []string{"find_tools", "run_tool"} {
						if _, _, _, unknown := s.call(c.key, name, map[string]any{"query": "events", "name": "hack_list_events"}); !unknown {
							t.Fatalf("%s answered on a Simple Hack connection", name)
						}
					}
					// The contributed tool, by its own name: one of the product's own
					// on its personal connection, its variant of a website tool
					// (Contribution.SiteVariants) on a website connection. A
					// deploy-only key reaches the variant too and is refused by
					// the REST layer for what the key may not do.
					contributed := "who_am_i"
					if c.personal {
						contributed = "hack_list_events"
					}
					text, structured, failed, unknown := s.call(c.key, contributed, nil)
					if unknown {
						t.Fatalf("%s is not on this connection: %s", contributed, text)
					}
					if c.deployOnly {
						if !failed || !strings.Contains(text, "[deploy_only_key]") {
							t.Fatalf("%s with a deploy-only key: %s", contributed, text)
						}
					} else if failed || structured == nil {
						t.Fatalf("%s: %s", contributed, text)
					}
					for _, name := range []string{"hack_lock_judging", "hack_unlock_judging"} {
						text, structured, failed, unknown := s.call(c.key, name, map[string]any{"slug": "x"})
						if unknown || !failed || structured != nil || text != retired {
							t.Fatalf("%s: %q", name, text)
						}
					}
				})
			}
		})
	}
}

// A write made through a tool still produces its notification. On Simple
// Host: a deploy through update_site emits the site.deployed event and queues
// it for the site's subscriber, on the built program. (A storage write with the owner's
// own credential notifies nobody, through a tool or not; and Simple Hack emits
// no site events.) On Simple Hack, below: a team's message to the organisers
// through hack_message_organisers.
// The paper-metrics record of a tool's write is TestPaperRecorderLiveAfterBuild,
// which runs the built program as both products.
func TestToolMutationEmitsNotificationOnHost(t *testing.T) {
	t.Setenv("PASSCODE_ENC_KEY", testPasscodeKey)
	s := buildToolSurface(t, "host")
	_, key := s.person("surface-notify")
	must := func(name string, args map[string]any) {
		t.Helper()
		if text, _, failed, unknown := s.call(key, name, args); failed || unknown {
			t.Fatalf("%s: %s", name, text)
		}
	}
	must("create_site", map[string]any{"site": "shop", "files": map[string]any{"index.html": "<h1>shop</h1>"}})
	// The subscriber is seeded: creating one over REST resolves its address,
	// and the built program has no test receiver.
	if _, err := s.db.Exec(`INSERT INTO site_notifications(id,site_id,owner_id,source,url,events,secret,verified_at)
 SELECT 'sub_surface', id, user_id, 'owner', 'https://hooks.example.com/shop', '{site.deployed}', '', now() FROM sites WHERE name = 'shop'`); err != nil {
		t.Fatal(err)
	}
	// One event, queued for the one subscriber.
	events := func() int {
		var n int
		if err := s.db.QueryRow(`SELECT count(*) FROM site_event_deliveries d JOIN site_events e ON e.seq = d.event_seq JOIN sites s ON s.id = e.site_id
 WHERE s.name = 'shop' AND e.name = 'site.deployed' AND d.subscription_id = 'sub_surface'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := events(); n != 0 {
		t.Fatalf("%d events before the write", n)
	}
	must("update_site", map[string]any{"site": "shop", "files": map[string]any{"index.html": "<h1>shop, second version</h1>"}})
	deadline := time.Now().Add(10 * time.Second)
	for events() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := events(); n != 1 {
		t.Fatalf("the tool's deploy queued %d site.deployed deliveries, want 1", n)
	}
}

func TestToolMutationSendsNotificationOnHack(t *testing.T) {
	a := newTeamSiteApp(t)
	sink := &contentMailSink{}
	a.hack.SetMailer(sink)
	a.noticeSink = sink
	org, lead := a.newPerson(t, "tmnorg"), a.newPerson(t, "tmnlead")
	slug := a.makeEvent(t, org)
	waitJoin(t, a, slug, lead, org, "Lea")
	team, _ := a.startTeam(t, slug, "Alpha", lead)
	// The participant's own key is a personal connection.
	out := a.do(t, http.MethodPost, "/mcp", jsonBody(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{
		"name": "hack_message_organisers", "arguments": map[string]any{"slug": slug, "text": "We need a bigger table."}}}),
		map[string]string{"Content-Type": "application/json", "X-API-Key": lead.key, "MCP-Protocol-Version": "2025-06-18"})
	if text, _, failed := toolResultOf(t, out); failed {
		t.Fatalf("hack_message_organisers: %s", text)
	}
	var rows int
	if err := a.database.QueryRow(`SELECT count(*) FROM event_team_messages WHERE from_team AND event_id = (SELECT id FROM events WHERE slug = $1)`, slug).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("message rows: %d %v", rows, err)
	}
	// The organiser is told on Home, with the team's own words.
	need := findNeed(needsYouOf(t, a, slug, org), "team_inbound")
	if need == nil || need["text"] != "Alpha wrote to the organisers: We need a bigger table." || need["team"] != team {
		t.Fatalf("organiser's notice: %v", need)
	}
	if findNeed(needsYouOf(t, a, slug, lead), "team_inbound") != nil {
		t.Fatal("the sender got the organisers' notice")
	}
}
