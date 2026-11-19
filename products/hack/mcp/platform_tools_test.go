package hackmcp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	appstorage "github.com/vsriram/simple-host/host/app/storage"
	"github.com/vsriram/simple-host/host/mcp"
	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
)

// The tests in this file are Simple Hack's half of checks the tool server
// used to run on both products from its own package: what the server's
// shared machinery (telemetry, argument aliases, the storage tools and their
// rules wording, the page recipes, the listing) does with Hack's tools. They
// go through the server's public surface.

// --- telemetry ---------------------------------------------------------

// A planted private value in the arguments or the result of these tools never
// reaches the telemetry copy of the call.
func TestAnalyticsHackToolsKeepPrivateValuesOut(t *testing.T) {
	s := newTestServer(nil)
	const planted = "PLANTED_PRIVATE_VALUE"
	for i, tc := range []struct {
		name             string
		args, structured map[string]any
		text             string
	}{
		{"hack_create_team_key", map[string]any{"slug": "ordinary-site"}, map[string]any{"key": planted}, planted},
		{"hack_create_event", map[string]any{"contact_email": planted}, map[string]any{"organiser": map[string]any{"contact_email": planted}}, planted},
		{"hack_update_event", map[string]any{"contact_email": planted}, map[string]any{"organiser": map[string]any{"contact_email": planted}}, planted},
		{"hack_get_event", nil, map[string]any{"organiser": map[string]any{"contact_email": planted}}, planted},
		{"hack_export_results", nil, map[string]any{"csv": planted, "filename": "results.csv"}, planted},
		{"hack_export_scores", nil, map[string]any{"csv": planted, "filename": "scores.csv"}, planted},
		{"hack_regenerate_code", nil, map[string]any{"url": planted, "code": planted}, planted},
		{"hack_export_participants", nil, map[string]any{"csv": planted, "filename": "participants.csv"}, planted},
	} {
		t.Run(fmt.Sprintf("%d_%s", i, tc.name), func(t *testing.T) {
			originalArgs, _ := json.Marshal(tc.args)
			originalStructured, _ := json.Marshal(tc.structured)
			parameters, response := s.AnalyticsCopy(tc.name, tc.args, tc.text, tc.structured)
			body, err := json.Marshal([]any{parameters, response})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(body), planted) {
				t.Errorf("telemetry copy contains the planted value: %s", body)
			}
			if tc.args["slug"] != nil && !strings.Contains(string(body), "ordinary-site") {
				t.Errorf("ordinary argument lost: %s", body)
			}
			after, _ := json.Marshal(tc.structured)
			afterArgs, _ := json.Marshal(tc.args)
			if string(after) != string(originalStructured) || string(afterArgs) != string(originalArgs) {
				t.Fatal("source mutated")
			}
		})
	}
}

// An applicant's city, age range, gender and answers are removed from the
// telemetry copy of hack_apply_to_event's arguments.
func TestAnalyticsHackApplyFieldsRemoved(t *testing.T) {
	args := map[string]any{
		"slug": "spring", "accept_coc": true, "display_name": "Dewi",
		"city": "Jakarta", "country": "ID", "age_range": "under_18", "gender": "woman",
		"answers": map[string]any{"why": "I want to build an app for my school library."},
	}
	got, _ := newTestServer(nil).AnalyticsCopy("hack_apply_to_event", args, "", nil)
	body, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, leaked := range []string{"Jakarta", "\"ID\"", "under_18", "woman", "Dewi", "school library"} {
		if strings.Contains(s, leaked) {
			t.Errorf("AgentCat payload still carries %q: %s", leaked, s)
		}
	}
	if !strings.Contains(s, "spring") {
		t.Errorf("unrelated fields were also removed: %s", s)
	}
}

// The response-side half: the organiser's own hack_get_applications call
// keeps seeing real city/age/gender/answers, but the telemetry copy of that
// same response strips them, including answers and display_name.
func TestAnalyticsHackGetApplicationsFieldsRemoved(t *testing.T) {
	text := `{"response":[{"user_id":"u1","display_name":"Dewi","city":"Jakarta","country":"ID","age_range":"under_18","gender":"woman","answers":[{"id":"why","prompt":"Why?","answer":"I want to build an app for my school library."}]}],"status":200}`
	_, got := newTestServer(nil).AnalyticsCopy("hack_get_applications", nil, text, nil)
	body, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, leaked := range []string{"Jakarta", "\"ID\"", "under_18", "woman", "Dewi", "school library"} {
		if strings.Contains(s, leaked) {
			t.Errorf("AgentCat payload still carries %q: %s", leaked, s)
		}
	}
	if !strings.Contains(s, "u1") {
		t.Errorf("unrelated fields were also removed: %s", s)
	}
}

// --- argument aliases --------------------------------------------------

func TestHackGetPageRecipeAcceptsKindAliasForTopic(t *testing.T) {
	s := newTestServer(nil)
	text, structured, isErr := resultOf(t, callAs(t, s, mcp.Caller{APIKey: "test", Mode: mcp.CallerModeEvents}, "hack_get_page_recipe", map[string]any{"kind": "inbox"}))
	if isErr {
		t.Fatalf("kind alias refused: %s", text)
	}
	if structured["topic"] != "inbox" {
		t.Fatalf("topic = %v", structured["topic"])
	}
}

// hack_event_storage_* tools share the storage_* builder, so the alias must
// work on the organiser's event-website copies too.
func TestHackEventStorageAliasAcceptsResourceForName(t *testing.T) {
	up := &scriptUpstream{ans: map[string]func(http.ResponseWriter, *http.Request){
		"PUT /v1/hack/events/weekend/website/storage/kv/settings/keys/feature": jsonReply(200, `{"key":"feature","value":true}`),
	}}
	product := Contribution()
	s := mcp.NewServer(mcp.Config{Contribution: &product, Events: wireEvents{}, Upstream: up, APIHost: "simple-hack.app"})
	caller := mcp.Caller{APIKey: "test", Mode: mcp.CallerModeEvents}
	text, _, isErr := resultOf(t, callAs(t, s, caller, "hack_event_storage_put_kv", map[string]any{"event": "weekend", "resource": "settings", "key": "feature", "value": true}))
	if isErr {
		t.Fatalf("resource alias refused for hack_event_storage_put_kv: %s", text)
	}
	if got := up.last().path; got != "/v1/hack/events/weekend/website/storage/kv/settings/keys/feature" {
		t.Fatalf("alias did not resolve to name: %s", got)
	}
}

// No Hack tool's advertised schema has grown the alias the server accepts
// for it: "kind" for hack_get_page_recipe's topic, "resource" for a
// hack_event_storage_* tool's name. If it had, the alias would have become a
// second real argument, changing the frozen schema some clients cache.
func TestHackArgumentAliasNeverAppearsInAnyAdvertisedSchema(t *testing.T) {
	checked := 0
	for _, tool := range Tools() {
		canonical, alias := "", ""
		switch {
		case tool.Name == "hack_get_page_recipe":
			canonical, alias = "topic", "kind"
		case strings.HasPrefix(tool.Name, "hack_event_storage_"):
			canonical, alias = "name", "resource"
		default:
			continue
		}
		checked++
		props, _ := tool.InputSchema["properties"].(map[string]any)
		if _, ok := props[alias]; ok {
			t.Errorf("%s advertises %q, which is also this family's alias for %q", tool.Name, alias, canonical)
		}
	}
	if checked < 2 {
		t.Fatalf("checked %d tools", checked)
	}
}

// --- storage tools -----------------------------------------------------

// The event-website SQL tools say upfront that PRAGMA is not available and
// what to use instead (SIM-359).
func TestHackSQLToolDescriptionsNamePragmaAlternative(t *testing.T) {
	want := map[string]bool{"hack_event_storage_sql_query": false, "hack_event_storage_sql_execute": false, "hack_event_storage_sql_schema": false}
	for _, tool := range Tools() {
		if _, tracked := want[tool.Name]; tracked {
			if !strings.Contains(tool.Description, "PRAGMA") || !strings.Contains(tool.Description, "sqlite_schema") {
				t.Errorf("%s description lacks the PRAGMA hint: %s", tool.Name, tool.Description)
			}
			want[tool.Name] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("%s not found in the tool inventory", name)
		}
	}
}

func hackEventStorageTools() []mcp.Tool {
	var tools []mcp.Tool
	for _, tool := range Tools() {
		if strings.HasPrefix(tool.Name, "hack_event_storage_") {
			tools = append(tools, tool)
		}
	}
	return tools
}

func TestHackStorageRulesSchema(t *testing.T) {
	tool := toolNamed(t, "hack_event_storage_set_resource")
	if err := mcp.CheckOutputSchema(tool.InputSchema); err != nil {
		t.Fatal(err)
	}
	for _, body := range []any{
		map[string]any{"kind": "sqlite", "rules": nil},
		map[string]any{"kind": "sqlite", "rules": map[string]any{"policies": []any{}}, "tables": map[string]any{"notes": map[string]any{"rules": map[string]any{"policies": []any{}}}}},
		map[string]any{"kind": "sqlite", "preset": "private", "tables": map[string]any{"notes": map[string]any{}}},
	} {
		if err := mcp.ValidateOutput(tool.InputSchema, map[string]any{"event": "shop", "name": "notes", "body": body}, nil); err != nil {
			t.Fatal(err)
		}
	}
}

// An organiser's connection has the event-website storage tools.
func TestHackOrganiserConnectionHasEventStorage(t *testing.T) {
	_, byName := toolsAs(t, newTestServer(nil), mcp.Caller{APIKey: "person", Mode: mcp.CallerModeEvents}, false)
	if _, ok := byName["hack_event_storage_sql_schema"]; !ok {
		t.Fatal("organiser connection lost event website storage tool")
	}
}

// Simple Hack does not offer the comments preset: its storage tools never
// mention it, its checked value, or approve_first, while Simple Host's do.
func TestHackStorageToolsOmitSpamCheck(t *testing.T) {
	s := newTestServer(nil)
	schemaText := func(description string, schema map[string]any) string {
		b, err := json.Marshal(schema)
		if err != nil {
			t.Fatal(err)
		}
		return description + string(b)
	}
	_, hostTools := toolsAs(t, s, mcp.Caller{APIKey: "host"}, false)
	host := schemaText(hostTools["storage_set_resource"].Description, hostTools["storage_set_resource"].InputSchema)
	for _, want := range []string{`"comments"`, `"checked"`, `approve_first`} {
		if !strings.Contains(host, want) {
			t.Errorf("Host storage_set_resource lacks %s", want)
		}
	}
	_, teamTools := toolsAs(t, s, mcp.Caller{APIKey: "team", Mode: mcp.CallerModeGroupSite}, false)
	hack := map[string]string{}
	for _, name := range []string{"storage_set_resource", "storage_sql_schema"} {
		tool, ok := teamTools[name]
		if !ok {
			t.Fatalf("team connection lost %s", name)
		}
		hack[name] = schemaText(tool.Description, tool.InputSchema)
	}
	for _, tool := range hackEventStorageTools() {
		if tool.Name == "hack_event_storage_set_resource" || tool.Name == "hack_event_storage_sql_schema" {
			hack[tool.Name] = schemaText(tool.Description, tool.InputSchema)
		}
	}
	if len(hack) != 4 {
		t.Fatalf("found %d Hack tools, want 4", len(hack))
	}
	for name, text := range hack {
		for _, banned := range []string{`"comments"`, "comments (", "wall, comments", `"checked"`, "or checked", "approve_first", "spam check", "Turnstile"} {
			if strings.Contains(text, banned) {
				t.Errorf("%s on Hack mentions %s", name, banned)
			}
		}
		if strings.HasSuffix(name, "set_resource") && !strings.Contains(text, "Lookalikes") {
			t.Errorf("%s lost its other text", name)
		}
	}
}

// A refused hack_event_storage_set_resource gains the same data-rules hint
// the site tool gains when rules are on, and none for a files resource.
func TestHackStorageRulesRefusedResourceHints(t *testing.T) {
	added := func(off, on string) string {
		seen := map[string]bool{}
		for _, line := range strings.Split(off, "\n") {
			seen[line] = true
		}
		var lines []string
		for _, line := range strings.Split(on, "\n") {
			if !seen[line] {
				lines = append(lines, line)
			}
		}
		return strings.Join(lines, "\n")
	}
	for _, kind := range []string{"files", "sqlite", "kv"} {
		for _, code := range []string{"invalid_resource", "storage_busy", "invalid_rules"} {
			t.Run(kind+"/"+code, func(t *testing.T) {
				body := map[string]any{"kind": kind, "site_passcode": "invalid"}
				if code == "invalid_rules" {
					body = map[string]any{"kind": kind, "rules": map[string]any{"version": 1, "policies": []any{}}}
				}
				refuse := jsonReply(400, fmt.Sprintf(`{"error":"settings refused","code":%q}`, code))
				s := newTestServer(&scriptUpstream{ans: map[string]func(http.ResponseWriter, *http.Request){
					"PUT /v1/sites/test/storage/resources/data":               refuse,
					"PUT /v1/hack/events/test/website/storage/resources/data": refuse,
				}})
				render := func(flag string, event bool) string {
					t.Setenv("STORAGE_RULES", flag)
					tool, caller, args := "storage_set_resource", mcp.Caller{APIKey: "test-key"}, map[string]any{"site": "test", "name": "data", "body": body}
					if event {
						tool, caller, args = "hack_event_storage_set_resource", mcp.Caller{APIKey: "test-key", Mode: mcp.CallerModeEvents}, map[string]any{"event": "test", "name": "data", "body": body}
					}
					text, _, refused := resultOf(t, callAs(t, s, caller, tool, args))
					if !refused || !strings.Contains(text, "["+code+"]") {
						t.Fatalf("expected storage refusal: %s", text)
					}
					return text
				}
				off, on := render("off", true), render("on", true)
				siteHint := added(render("off", false), render("on", false))
				if hint := added(off, on); hint == "" || hint != siteHint {
					t.Fatalf("missing data rule hint: %s\nsite tool adds: %s", on, siteHint)
				}
			})
		}
	}
}

// The rules wording is chosen at request time on Hack's connections too: the
// storage tools' descriptions and the instructions switch with the flag, and
// nothing else in the listing does.
func TestHackStorageRulesTextAtRequestTime(t *testing.T) {
	t.Setenv("STORAGE_RULES", "off")
	for _, noSpam := range []bool{false, true} {
		product := Contribution()
		s := mcp.NewServer(mcp.Config{Contribution: &product, NoSpamCheck: noSpam})
		for _, caller := range []mcp.Caller{{Mode: mcp.CallerModeGroupSite, APIKey: "fixture"}, {Mode: mcp.CallerModeEvents, APIKey: "fixture"}, {Mode: mcp.CallerModeEvents, GrantID: "test", APIKey: "fixture"}} {
			before, _ := toolsAs(t, s, caller, false)
			old, _ := json.Marshal(before)
			oldInstructions := instructionsAs(t, s, caller)
			t.Setenv("STORAGE_RULES", "on")
			after, _ := toolsAs(t, s, caller, false)
			if len(before) != len(after) || len(after) == 0 {
				t.Fatal("tool inventory shape changed")
			}
			changedTools := 0
			for i, tool := range after {
				name := strings.TrimPrefix(tool.Name, "hack_event_")
				changed := name == "storage_set_resource" || name == "storage_list_resources" || name == "storage_sql_query" || name == "storage_sql_schema"
				if !changed {
					a, _ := json.Marshal(before[i])
					b, _ := json.Marshal(tool)
					if string(a) != string(b) {
						t.Errorf("unrelated tool changed: %s", tool.Name)
					}
					continue
				}
				changedTools++
				if name != "storage_set_resource" && tool.Title != before[i].Title {
					t.Errorf("unrelated title changed: %s", tool.Name)
				}
				if strings.Count(tool.Title, "Event website:") > 1 {
					t.Errorf("duplicated event title: %s", tool.Title)
				}
				if tool.Description == before[i].Description {
					t.Errorf("stale description: %s", tool.Name)
				}
				a, _ := json.Marshal(before[i].InputSchema)
				b, _ := json.Marshal(tool.InputSchema)
				if string(a) != string(b) {
					t.Errorf("schema changed: %s", tool.Name)
				}
				if name == "storage_sql_schema" && !strings.Contains(tool.Description, "Do not declare visitor_id: the server ensures the indexed author column when rules reference visitor_id or allow ordinary visitors to add.") {
					t.Errorf("%s lacks conditional author-column guidance", tool.Name)
				}
				// File presets are the only access menu the rules wording may teach.
				dataText := strings.Split(tool.Description, "For kind: \"files\"")[0]
				for _, choice := range []string{"preset public", "preset private", "preset records", "preset personal", "preset inbox", "preset wall", "preset board", "The presets (", "whatever the preset", "whose preset"} {
					if strings.Contains(dataText, choice) {
						t.Errorf("%s teaches %q", tool.Name, choice)
					}
				}
				if name == "storage_set_resource" {
					prefix := ""
					if strings.HasPrefix(tool.Name, "hack_event_") {
						prefix = "hack_event_"
					}
					if !strings.Contains(tool.Description, "Create the resource with "+prefix+"storage_set_resource, then create its tables with "+prefix+"storage_sql_schema only for SQLite") {
						t.Errorf("incorrect resource creation instructions: %s", tool.Name)
					}
					count := 0
					for _, line := range strings.Split(tool.Description, "\n") {
						if !strings.HasPrefix(line, `{"version"`) {
							continue
						}
						set, errs := ruleengine.DecodeSet([]byte(line))
						if len(errs) != 0 {
							t.Fatal(errs)
						}
						// Simple Hack runs no spam check on either install.
						if errs := ruleengine.ValidateSyntax(set, appstorage.PlatformSchema(false), ruleengine.DefaultLimits()); len(errs) != 0 {
							t.Fatal(errs)
						}
						count++
					}
					if count != 3 {
						t.Errorf("%s: want three examples, got %d", tool.Name, count)
					}
				}
			}
			if changedTools == 0 {
				t.Errorf("no storage tool listed for %+v", caller)
			}
			if text := instructionsAs(t, s, caller); text == oldInstructions || !strings.Contains(text, "Deny by default") {
				t.Errorf("stale instructions for %+v", caller)
			}
			t.Setenv("STORAGE_RULES", "off")
			restored, _ := toolsAs(t, s, caller, false)
			b, _ := json.Marshal(restored)
			if string(b) != string(old) || instructionsAs(t, s, caller) != oldInstructions {
				t.Error("cached inventory changed after flag toggled")
			}
		}
	}
}

// --- website tools on Hack's connections -----------------------------------

// Simple Hack has no named viewers: its create_site has neither input.
func TestHackCreateSiteHasNoNamedViewers(t *testing.T) {
	for _, tool := range mcp.ToolsWithLimits(mcp.Limits{SiteViewersMax: 3}) {
		if tool.Name != "create_site" {
			continue
		}
		if tool.InputSchema["properties"].(map[string]any)["viewers"] == nil {
			t.Fatal("the platform's create_site has no viewers input to remove")
		}
		hp := hackSiteCreateSite(tool, mcp.Limits{}).InputSchema["properties"].(map[string]any)
		if hp["access"] != nil || hp["viewers"] != nil {
			t.Fatal("Hack exposes named viewers")
		}
		return
	}
	t.Fatal("no create_site")
}

// Simple Hack has no photos link, so its deploy tools keep the compress line.
func TestHackDeployToolsDoNotOfferPhotosLink(t *testing.T) {
	s := newTestServer(nil)
	for _, mode := range []string{mcp.CallerModeGroupSite, mcp.CallerModeEvents} {
		_, tools := toolsAs(t, s, mcp.Caller{Mode: mode, GrantID: "grant", APIKey: "owner"}, false)
		for _, name := range []string{"create_site", "update_site"} {
			d := tools[name].Description
			if strings.Contains(d, "photos_url") || !strings.Contains(d, hackPhotoTip) || strings.Contains(d, mcp.DeployPhotoTip) || !strings.Contains(d, "must save to storage: call get_page_recipe first") {
				t.Fatalf("%s %s: %q", mode, name, d)
			}
		}
	}
}

func TestHackPhotoToolRemainsArchive(t *testing.T) {
	s := newTestServer(nil)
	for _, mode := range []string{mcp.CallerModeGroupSite, mcp.CallerModeEvents} {
		caller := mcp.Caller{Mode: mode, GrantID: "grant", APIKey: "owner", TeamAPIKey: "team"}
		_, tools := toolsAs(t, s, caller, false)
		if tools["create_upload_link"].Description != mcp.ArchiveUploadDescription {
			t.Fatal("Hack description changed")
		}
		text, structured, failed := resultOf(t, callAs(t, s, caller, "create_upload_link", map[string]any{"site": "blog"}))
		if failed || structured["upload_url"] == nil || structured["photos_url"] != nil || len(structured) != 4 {
			t.Fatalf("Hack default changed: %s %v", text, structured)
		}
	}
}

// --- instructions and the public inventory ----------------------------------

func TestHackInstructionsLength(t *testing.T) {
	for _, flag := range []string{"off", "on"} {
		t.Run(flag, func(t *testing.T) {
			t.Setenv("STORAGE_RULES", flag)
			for _, host := range []string{"simple-hack.app", "hack.company.example", strings.Repeat("a", 63) + ".sites.example-company.org", strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61)} {
				for _, caller := range []mcp.Caller{{Mode: mcp.CallerModeEvents, APIKey: "fixture"}, {Mode: mcp.CallerModeEvents, GrantID: "personal", APIKey: "fixture"}, {Mode: mcp.CallerModeGroupSite, APIKey: "fixture"}} {
					t.Run(host+"/"+caller.Mode+"/"+caller.GrantID, func(t *testing.T) {
						product := Contribution()
						text := instructionsAs(t, mcp.NewServer(mcp.Config{Contribution: &product, APIHost: host, SelfHosted: host != "simple-hack.app"}), caller)
						t.Logf("instructions: %d characters", len([]rune(text)))
						if n := len([]rune(text)); n >= 2000 {
							t.Fatalf("instructions too long: %d", n)
						}
						for _, want := range []string{host, "membership", "deadline", "Ask before"} {
							if !strings.Contains(text, want) {
								t.Errorf("missing %q", want)
							}
						}
						if caller.Mode == mcp.CallerModeEvents {
							for _, want := range []string{"organiser", "participant", "judge", "hack_select_team", "hack_event_storage_*", "code of conduct", "Archiving is final", "keep scores unless you confirm deletions", "Stage results does not publish"} {
								if !strings.Contains(text, want) {
									t.Errorf("missing %q", want)
								}
							}
						}
					})
				}
			}
		})
	}
}

// The public inventory (the agent card and the docs) is what a personal
// connection is offered: the same tools in the same order with the same
// wording. tools/list leaves out the screen tools for a connection with no
// screen support, so those are skipped on the public side.
func TestHackPublicInventory(t *testing.T) {
	t.Setenv("STORAGE_RULES", "off")
	product := Contribution()
	s := mcp.NewServer(mcp.Config{Contribution: &product})
	for _, flag := range []string{"off", "on"} {
		t.Run(flag, func(t *testing.T) {
			t.Setenv("STORAGE_RULES", flag)
			var public []mcp.Tool
			for _, tool := range s.ProductListedTools() {
				if !strings.HasPrefix(tool.Name, "hack_app_") && !strings.HasPrefix(tool.Name, "hack_show_") {
					public = append(public, tool)
				}
			}
			personal, _ := toolsAs(t, s, mcp.Caller{Mode: mcp.CallerModeEvents, GrantID: "test", APIKey: "fixture"}, false)
			if len(public) != len(personal) || len(public) == len(s.ProductListedTools()) {
				t.Fatalf("public inventory %d (of %d) != personal %d", len(public), len(s.ProductListedTools()), len(personal))
			}
			for i, tool := range public {
				if tool.Name != personal[i].Name || tool.Title != personal[i].Title || tool.Description != personal[i].Description {
					t.Fatalf("tool %d differs", i)
				}
			}
			for _, name := range []string{"storage_set_resource", "hack_event_storage_set_resource"} {
				found := false
				for _, tool := range public {
					if tool.Name != name {
						continue
					}
					found = true
					want := "Create a storage resource or set its preset"
					if flag == "on" {
						want = "Create a storage resource or set its rules"
					}
					if strings.HasPrefix(name, "hack_event_") {
						want = "Event website: " + want
					}
					if tool.Title != want {
						t.Errorf("%s title = %q, want %q", name, tool.Title, want)
					}
				}
				if !found {
					t.Errorf("missing public tool %s", name)
				}
			}
			s.ProductListedTools()[0].Name = "changed"
			if s.ProductListedTools()[0].Name == "changed" {
				t.Fatal("returned slice aliases server inventory")
			}
		})
	}
}

// Simple Hack's tools change nothing for a Simple Host connection: the
// listing and the instructions are the ones a server with no product gives.
func TestHackContributionLeavesHostConnectionsAlone(t *testing.T) {
	for _, events := range []bool{false, true} {
		product := Contribution()
		composedCfg := mcp.Config{APIHost: "connector.example.test", Contribution: &product}
		hostCfg := mcp.Config{APIHost: "connector.example.test"}
		if events {
			composedCfg.Events, hostCfg.Events = wireEvents{}, wireEvents{}
		}
		composed, host := mcp.NewServer(composedCfg), mcp.NewServer(hostCfg)
		for _, caller := range []mcp.Caller{{APIKey: "fixture"}, {APIKey: "fixture", DeployOnly: true}, {APIKey: "fixture", AppName: "ChatGPT"}, {APIKey: "fixture", AppName: "ChatGPT", DeployOnly: true}} {
			if listingBytes(t, composed, caller) != listingBytes(t, host, caller) || instructionsAs(t, composed, caller) != instructionsAs(t, host, caller) {
				t.Fatalf("Host wire differs with Simple Hack contributed (events=%t caller=%+v)", events, caller)
			}
		}
		for _, entry := range mcp.Inventory(mcp.Contribution{}) {
			if entry.Edition == "product" {
				t.Fatalf("no product registered %s", entry.Name)
			}
		}
	}
}

// --- retired names -------------------------------------------------------------

// The two judging-lock tools are gone; a call to either name is answered with
// what replaced them, directly and through run_tool's refusal path.
func TestHackRetiredJudgingLockNames(t *testing.T) {
	const want = "Judging locks when you finish judging (move to Results) and unlocks when you move the event back. [judging_lock_removed]"
	s := newTestServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("a retired tool reached REST") }))
	for _, name := range []string{"hack_lock_judging", "hack_unlock_judging"} {
		for _, caller := range []mcp.Caller{{APIKey: "person", Mode: mcp.CallerModeEvents}, {APIKey: "team", Mode: mcp.CallerModeGroupSite}, {APIKey: "host"}} {
			text, structured, failed := resultOf(t, callAs(t, s, caller, name, map[string]any{"slug": "weekend"}))
			if !failed || structured != nil || text != want {
				t.Fatalf("%s as %q: %q %v failed=%t", name, caller.Mode, text, structured, failed)
			}
		}
	}
	// Any other unknown name is still an unknown tool.
	rec := callAs(t, s, mcp.Caller{APIKey: "person", Mode: mcp.CallerModeEvents}, "hack_lock_everything", nil)
	if decode(t, rec)["error"] == nil {
		t.Fatalf("unknown tool answered: %s", rec.Body.String())
	}
}

// --- page recipes --------------------------------------------------------------

// The recipe tool re-reads the rules flag on each call, including a cached tool.
func TestHackPageRecipeSwitchesAtRequestTime(t *testing.T) {
	t.Setenv("STORAGE_RULES", "off")
	tool := hackPageRecipeTool()
	c := mcp.NewCall(newTestServer(nil), nil, mcp.Caller{})
	args := map[string]any{"topic": "booking", "site": "shop"}
	before, err := mcp.CallTool(tool, c, args)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("STORAGE_RULES", "on")
	on, err := mcp.CallTool(tool, c, args)
	if err != nil {
		t.Fatal(err)
	}
	if on.Text == before.Text || !strings.Contains(on.Text, `"policies"`) {
		t.Error("recipe did not switch at request time")
	}
	if on.Structured["recipe"] != on.Text {
		t.Error("structured recipe differs from text")
	}
	t.Setenv("STORAGE_RULES", "off")
	after, err := mcp.CallTool(tool, c, args)
	if err != nil || after.Text != before.Text {
		t.Errorf("legacy response not restored: %v", err)
	}
}

func TestRulesHackRecipes(t *testing.T) {
	t.Setenv("STORAGE_RULES", "on")
	for _, topic := range append(mcp.RecipeTopics(), "event") {
		text, err := HackPageRecipe(topic, "my-event")
		switch topic {
		case "portal", "comments", "guestbook", "people":
			if err == nil {
				t.Errorf("%s product refusal lost", topic)
			}
		default:
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(text, "Ask who should read") {
				t.Errorf("%s missing question", topic)
			}
			if topic != "event" && topic != "gallery" && !strings.Contains(text, `"policies"`) {
				t.Errorf("%s has no rules", topic)
			}
			if strings.Contains(text, "https://simple-host.app/auth.js") || strings.Contains(text, "Simple Host account") {
				t.Errorf("%s has Host wording", topic)
			}
		}
	}
}

func TestRulesHackRecipeAdaptations(t *testing.T) {
	t.Setenv("STORAGE_RULES", "on")
	for _, topic := range []string{"public", "menu"} {
		text, err := HackPageRecipe(topic, "my-event")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(text, "files resource with the same rules") || strings.Contains(text, "same preset") {
			t.Errorf("%s has ambiguous file access advice", topic)
		}
	}
	t.Setenv("STORAGE_RULES", "off")
	text, err := HackPageRecipe("public", "my-event")
	if err != nil || !strings.Contains(text, "files resource with the same preset") {
		t.Fatalf("flag-off Hack photo advice changed: %v", err)
	}
}

// The preset recipes and the event-website storage tools in Hack's wording.
func TestHackStoragePresetRecipesAndTools(t *testing.T) {
	tools := hackEventStorageTools()
	if len(tools) == 0 {
		t.Fatal("no event-website storage tools")
	}
	for _, tool := range tools {
		if strings.Contains(tool.Description, "write_mode") || strings.Contains(tool.Description, " storage_") {
			t.Fatalf("%s keeps older or Host wording: %s", tool.Name, tool.Description)
		}
		if tool.Name == "hack_event_storage_set_resource" && (!strings.Contains(tool.Description, "organisers") || !strings.Contains(tool.Description, "Lookalikes")) {
			t.Fatalf("event set_resource: %s", tool.Description)
		}
	}
	for _, topic := range mcp.RecipeTopics() {
		if topic == "portal" {
			continue // refused on Simple Hack (TestHackRefusesPortalRecipe)
		}
		if topic == "people" {
			// for and the people pick list are Host only (review round 2);
			// not offered on Simple Hack at all, not even in the enum.
			if _, err := HackPageRecipe(topic, "team"); err == nil {
				t.Fatal("people recipe served on Simple Hack")
			}
			continue
		}
		if topic == "comments" || topic == "guestbook" {
			// Simple Hack runs no spam check, so the comments preset is
			// not offered there.
			if _, err := HackPageRecipe(topic, "team"); err == nil || !strings.Contains(err.Error(), "topic wall") {
				t.Fatalf("hack %s: want a refusal naming wall, got %v", topic, err)
			}
			continue
		}
		text, err := HackPageRecipe(topic, "team")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(text, "https://simple-host.app/auth.js") || strings.Contains(text, "Simple Host account") || strings.Contains(text, "who_am_i") || !strings.HasPrefix(text, "On Simple Hack") {
			t.Fatalf("hack recipe %s keeps Host wording", topic)
		}
	}
}

// Simple Hack does not offer the people recipe at all (review round 2,
// item D): for and the people pick list are Host only, so unlike
// portal/comments/guestbook (listed, refused with a reason),
// people is removed from hack_get_page_recipe's topic enum entirely.
func TestHackPeopleRecipeNotOffered(t *testing.T) {
	if _, err := HackPageRecipe("people", "team"); err == nil {
		t.Fatal("people recipe served on Simple Hack")
	}
	tool := hackPageRecipeTool()
	topicEnum, _ := tool.InputSchema["properties"].(map[string]any)["topic"].(map[string]any)["enum"].([]string)
	if len(topicEnum) == 0 {
		t.Fatal("hack_get_page_recipe has no topic enum")
	}
	for _, topic := range topicEnum {
		if topic == "people" {
			t.Fatal("hack_get_page_recipe's topic enum still lists people")
		}
	}
	if strings.Contains(tool.Description, "a coach and clients") {
		t.Fatal("hack_get_page_recipe description still advertises the people recipe")
	}
}

// --- screens: the metadata the tool server gives -----------------------------

// Each Hack screen is listed and read with the metadata the tool server
// built for it (AppContext.Meta), asked for once per listing and once per read.
func TestHackScreenMetadataComesFromTheToolServer(t *testing.T) {
	t.Setenv("MCP_APP_HACK_JUDGE", "on")
	t.Setenv("MCP_APP_HACK_ORGANISER", "on")
	product := Contribution()
	var metadata func(mcp.AppScreen) map[string]any
	var seen []mcp.AppScreen
	product.Apps = func(ctx mcp.AppContext) mcp.AppContribution {
		metadata = ctx.Meta
		ctx.Meta = func(screen mcp.AppScreen) map[string]any {
			seen = append(seen, screen)
			return metadata(screen)
		}
		return hackApps(ctx)
	}
	s := mcp.NewServer(mcp.Config{APIHost: "screens.example.test", Contribution: &product})
	caller := mcp.Caller{Mode: mcp.CallerModeEvents, Handle: "fixture", APIKey: "fixture"}
	asJSON := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	initializeAs(t, s, caller, true)
	resources := decode(t, rpcAs(t, s, caller, "2025-06-18", "resources/list", nil, true))["result"].(map[string]any)["resources"].([]any)
	if len(resources) != len(hackAppScreens) || asJSON(seen) != asJSON(hackAppScreens) {
		t.Fatalf("resource metadata callbacks = %#v", seen)
	}
	seen = nil
	for i, screen := range hackAppScreens {
		t.Run(screen.ID, func(t *testing.T) {
			want := metadata(screen)
			if got := resources[i].(map[string]any)["_meta"]; asJSON(got) != asJSON(want) {
				t.Fatalf("listed metadata = %#v; want %#v", got, want)
			}
			reply := decode(t, rpcAs(t, s, caller, "2025-06-18", "resources/read", map[string]any{"uri": screen.URI}, true))
			if reply["error"] != nil {
				t.Fatalf("read: %v", reply["error"])
			}
			content := reply["result"].(map[string]any)["contents"].([]any)[0].(map[string]any)
			if got := content["_meta"]; asJSON(got) != asJSON(want) {
				t.Fatalf("read metadata = %#v; want %#v", got, want)
			}
			ui := want["ui"].(map[string]any)
			permissions, hasPermissions := ui["permissions"]
			if screen.ID == "hack-organiser" {
				if asJSON(permissions) != `{"clipboardWrite":{}}` {
					t.Fatalf("organiser permissions = %#v", permissions)
				}
			} else if hasPermissions {
				t.Fatalf("judge has permissions: %#v", permissions)
			}
		})
	}
	if asJSON(seen) != asJSON(hackAppScreens) {
		t.Fatalf("read metadata callbacks = %#v", seen)
	}
}
