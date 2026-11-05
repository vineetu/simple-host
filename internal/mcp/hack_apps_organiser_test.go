package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/mcp/apps/bundlegen"
)

// organiserUpstream mirrors judgeUpstream for the organiser app: one event,
// organiser role, with distinctive marker text on every field that must
// never reach the model (join_url/judge_url carry the event's own codes;
// display_name is the organiser's own chosen name) so a test can tell
// "never present" apart from "present but happens to be empty".
func organiserUpstream() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/hack/events/mockjam", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"event":{"slug":"mockjam","title":"Mock Jam","stage":"open","scoring_open":false,"judging_locked_at":null,"results_published":false,"submission_deadline":null,"time_zone":"UTC"},"organiser":{"join_url":"https://simple-hack.app/join/ORGANISER-SECRET-JOIN","judge_url":"https://simple-hack.app/judge/ORGANISER-SECRET-JUDGE","counts":{"participants":3,"teams":1,"judges":1,"on_no_team":0}},"role":"organiser","admin_view":false,"me":{"display_name":"ORGANISER-OWN-NAME"}}`))
	})
	return mux
}

func organiserCaller(handle, appName string) Caller {
	return Caller{APIKey: "organiser-key", Mode: CallerModeEvents, Handle: handle, AppName: appName}
}

func TestHackOrganiserResourceGating(t *testing.T) {
	s := newTestServer(organiserUpstream())
	for _, flag := range []string{"", "off", "unknown", "accounts:bob", "accounts:alice", "on"} {
		t.Setenv("MCP_APP_HACK_ORGANISER", flag)
		for _, c := range []Caller{
			organiserCaller("alice", ""),
			{APIKey: "k", Mode: CallerModeHackSite, Handle: "alice"},
			{APIKey: "k", Handle: "alice"}, // Mode "": Simple Host, must never see this screen
		} {
			screenEnabled := c.Mode == CallerModeEvents && appFlag(flag, c.Handle)
			screens := s.hackEnabledScreens(c)
			hasOrganiser := false
			for _, scr := range screens {
				if scr.ID == "hack-organiser" {
					hasOrganiser = true
				}
			}
			if hasOrganiser != screenEnabled {
				t.Fatalf("flag=%q mode=%q handle=%q: organiser screen present=%v, want %v", flag, c.Mode, c.Handle, hasOrganiser, screenEnabled)
			}
			// hackAppResources/readHackApp take the common hackAppsGate as
			// `allowed` (mode/appname/UI, never the per-screen flag -- see
			// hack_apps.go's comment); this loop only varies flag/mode, with
			// appname="" and UI capability implicitly true, so passing
			// screenEnabled's mode-only half as the common gate and relying
			// on hackEnabledScreens to narrow by flag mirrors the judge
			// test's own approach.
			commonAllowed := c.Mode == CallerModeEvents
			resources := s.hackAppResources(c, commonAllowed)
			foundResource := false
			for _, raw := range resources {
				if raw.(map[string]any)["uri"] == "ui://simple-hack/organiser" {
					foundResource = true
				}
			}
			if foundResource != screenEnabled {
				t.Fatalf("flag=%q mode=%q handle=%q: hackAppResources organiser present=%v, want %v", flag, c.Mode, c.Handle, foundResource, screenEnabled)
			}
			params, _ := json.Marshal(map[string]string{"uri": "ui://simple-hack/organiser"})
			resp := s.readHackApp(request{ID: json.RawMessage(`1`), Params: params}, c, commonAllowed)
			if (resp.Error == nil) != screenEnabled {
				t.Fatalf("flag=%q mode=%q handle=%q: readHackApp error=%v, want enabled=%v", flag, c.Mode, c.Handle, resp.Error, screenEnabled)
			}
		}
	}
}

// TestHackJudgeAndOrganiserFlagsAreIndependent is the regression test for
// the cross-app gating fix this phase needed: resources/list and
// resources/read used to compute a single `allowed` bool from
// hackShowJudgeAppAllowed (which bakes in MCP_APP_HACK_JUDGE specifically),
// so an organiser-only-flagged connection (MCP_APP_HACK_JUDGE off,
// MCP_APP_HACK_ORGANISER on) would have had that bool come back false and
// never see ui://simple-hack/organiser at all, even though its own flag
// was on. The fix (hackAppsGate, shared and flag-independent) is exercised
// here with each flag on in isolation.
func TestHackJudgeAndOrganiserFlagsAreIndependent(t *testing.T) {
	cases := []struct {
		judgeFlag, organiserFlag string
		wantJudge, wantOrganiser bool
	}{
		{"on", "off", true, false},
		{"off", "on", false, true},
		{"on", "on", true, true},
		{"off", "off", false, false},
	}
	for _, tc := range cases {
		t.Setenv("MCP_APP_HACK_JUDGE", tc.judgeFlag)
		t.Setenv("MCP_APP_HACK_ORGANISER", tc.organiserFlag)
		s := newTestServer(organiserUpstream())
		c := organiserCaller("alice", "")
		s.clientUI.remember(c, true, time.Now())
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"resources/list","params":{}}`))
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(WithCaller(req.Context(), c))
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		resources := decode(t, rec)["result"].(map[string]any)["resources"].([]any)
		gotJudge, gotOrganiser := false, false
		for _, raw := range resources {
			switch raw.(map[string]any)["uri"] {
			case "ui://simple-hack/judge":
				gotJudge = true
			case "ui://simple-hack/organiser":
				gotOrganiser = true
			}
		}
		if gotJudge != tc.wantJudge || gotOrganiser != tc.wantOrganiser {
			t.Fatalf("judgeFlag=%q organiserFlag=%q: judge=%v organiser=%v, want judge=%v organiser=%v", tc.judgeFlag, tc.organiserFlag, gotJudge, gotOrganiser, tc.wantJudge, tc.wantOrganiser)
		}
	}
}

// TestHackOrganiserResourceNeverReadableWithoutGate mirrors
// TestHackJudgeResourceNeverReadableWithoutGate: a connection that would
// never see hack_show_organiser in tools/list must not be able to read
// ui://simple-hack/organiser either, even by naming its URI directly.
func TestHackOrganiserResourceNeverReadableWithoutGate(t *testing.T) {
	t.Setenv("MCP_APP_HACK_ORGANISER", "on")
	s := newTestServer(organiserUpstream())
	readResource := func(c Caller) (listed bool, readErr bool) {
		listReq := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"resources/list","params":{}}`))
		listReq.Header.Set("Content-Type", "application/json")
		listReq = listReq.WithContext(WithCaller(listReq.Context(), c))
		listRec := httptest.NewRecorder()
		s.ServeHTTP(listRec, listReq)
		resources := decode(t, listRec)["result"].(map[string]any)["resources"].([]any)
		for _, raw := range resources {
			if raw.(map[string]any)["uri"] == "ui://simple-hack/organiser" {
				listed = true
			}
		}
		readBody, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "resources/read", "params": map[string]any{"uri": "ui://simple-hack/organiser"}})
		readReq := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(string(readBody)))
		readReq.Header.Set("Content-Type", "application/json")
		readReq = readReq.WithContext(WithCaller(readReq.Context(), c))
		readRec := httptest.NewRecorder()
		s.ServeHTTP(readRec, readReq)
		_, readErr = decode(t, readRec)["error"]
		return listed, readErr
	}

	excluded := Caller{APIKey: "organiser-key-excluded", Mode: CallerModeEvents, Handle: "alice", AppName: "ChatGPT"}
	s.clientUI.remember(excluded, true, time.Now())
	if listed, readErr := readResource(excluded); listed || !readErr {
		t.Fatalf("ChatGPT: listed=%v readErr=%v, want listed=false readErr=true", listed, readErr)
	}

	noUI := Caller{APIKey: "organiser-key-noui", Mode: CallerModeEvents, Handle: "alice"}
	if listed, readErr := readResource(noUI); listed || !readErr {
		t.Fatalf("no UI declared: listed=%v readErr=%v, want listed=false readErr=true", listed, readErr)
	}

	allowed := Caller{APIKey: "organiser-key-allowed", Mode: CallerModeEvents, Handle: "alice"}
	s.clientUI.remember(allowed, true, time.Now())
	if listed, readErr := readResource(allowed); !listed || readErr {
		t.Fatalf("flagged, UI-declared, allowed app name: listed=%v readErr=%v, want listed=true readErr=false", listed, readErr)
	}
}

func TestHackOrganiserResourceRendersAndCSP(t *testing.T) {
	t.Setenv("MCP_APP_HACK_ORGANISER", "on")
	s := newTestServer(organiserUpstream())
	c := organiserCaller("alice", "")
	params, _ := json.Marshal(map[string]string{"uri": "ui://simple-hack/organiser"})

	var nonces []string
	var pages []string
	for i := 0; i < 2; i++ {
		resp := s.readHackApp(request{ID: json.RawMessage(`1`), Params: params}, c, true)
		if resp.Error != nil {
			t.Fatalf("read %d: %v", i, resp.Error)
		}
		content := resp.Result.(map[string]any)["contents"].([]any)[0].(map[string]any)
		page := content["text"].(string)
		pages = append(pages, page)

		if strings.Contains(page, "__NONCE__") || strings.Contains(page, "__STYLE__") || strings.Contains(page, "__BUNDLE__") || strings.Contains(page, "__VIEW__") || strings.Contains(page, "__STUBS__") || strings.Contains(page, "__BRIDGE__") || strings.Contains(page, "__ORIGIN__") {
			t.Fatal("unreplaced placeholder in rendered page")
		}
		m := regexp.MustCompile(`script-src 'nonce-([^']+)'`).FindStringSubmatch(page)
		if m == nil {
			t.Fatal("no script-src nonce in CSP meta")
		}
		nonce := m[1]
		nonces = append(nonces, nonce)
		cspMeta := regexp.MustCompile(`<meta http-equiv="Content-Security-Policy"[^>]*>`).FindString(page)
		scriptSrc := regexp.MustCompile(`script-src[^;]*`).FindString(cspMeta)
		if strings.Contains(scriptSrc, "unsafe-inline") {
			t.Fatal("script-src allows unsafe-inline")
		}
		scriptTags := regexp.MustCompile(`<script[^>]*>`).FindAllString(page, -1)
		if len(scriptTags) == 0 {
			t.Fatal("no script tags rendered")
		}
		for _, tag := range scriptTags {
			if !strings.Contains(tag, `nonce="`+nonce+`"`) {
				t.Fatalf("script tag missing matching nonce: %s", tag)
			}
		}
		csp := content["_meta"].(map[string]any)["ui"].(map[string]any)["csp"].(map[string][]string)
		for key, domains := range csp {
			if len(domains) != 0 {
				t.Fatalf("hack-organiser screen declares external %s: %v", key, domains)
			}
		}
	}
	if nonces[0] == nonces[1] {
		t.Fatal("nonce reused across reads")
	}
	if strings.Contains(pages[0], "organiser-key") {
		t.Fatal("API key leaked into rendered page")
	}
}

func TestHackShowOrganiserNotListedWithoutUISupport(t *testing.T) {
	t.Setenv("MCP_APP_HACK_ORGANISER", "on")
	s := newTestServer(organiserUpstream())
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(WithCaller(req.Context(), organiserCaller("alice", "")))
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	listed := listedTools(t, rec)
	for _, tool := range listed {
		if tool["name"] == "hack_show_organiser" {
			t.Fatal("hack_show_organiser listed to a connection that never declared MCP Apps UI support")
		}
	}
}

func TestHackShowOrganiserListedOnceUIDeclared(t *testing.T) {
	t.Setenv("MCP_APP_HACK_ORGANISER", "on")
	s := newTestServer(organiserUpstream())
	c := organiserCaller("alice", "")
	s.clientUI.remember(c, true, time.Now())
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(WithCaller(req.Context(), c))
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	listed := listedTools(t, rec)
	found := false
	for _, tool := range listed {
		if tool["name"] == "hack_show_organiser" {
			found = true
			if tool["_meta"] == nil {
				t.Fatal("hack_show_organiser listed without _meta.ui.resourceUri")
			}
		}
	}
	if !found {
		t.Fatal("hack_show_organiser not listed for a UI-declared, flagged-in connection")
	}
}

func TestHackShowOrganiserModernPerCallCapability(t *testing.T) {
	t.Setenv("MCP_APP_HACK_ORGANISER", "on")
	s := newTestServer(organiserUpstream())
	for _, ui := range []bool{false, true} {
		caps := map[string]any{}
		if ui {
			caps["extensions"] = map[string]any{"io.modelcontextprotocol/ui": map[string]any{}}
		}
		params := map[string]any{"_meta": map[string]any{"io.modelcontextprotocol/clientCapabilities": caps}}
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list", "params": params})
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("MCP-Protocol-Version", protocolVersion)
		req.Header.Set("Mcp-Method", "tools/list")
		req = req.WithContext(WithCaller(req.Context(), organiserCaller("alice", "")))
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		listed := listedTools(t, rec)
		found := false
		for _, tool := range listed {
			if tool["name"] == "hack_show_organiser" {
				found = true
			}
		}
		if found != ui {
			t.Fatalf("ui=%v: hack_show_organiser listed=%v", ui, found)
		}
	}
}

func TestHackShowOrganiserNeverForExcludedAppNames(t *testing.T) {
	t.Setenv("MCP_APP_HACK_ORGANISER", "on")
	s := newTestServer(organiserUpstream())
	for _, name := range []string{"ChatGPT", "OpenAI", "Codex", "codex-cli", "chatgpt-connector"} {
		c := organiserCaller("alice", name)
		s.clientUI.remember(c, true, time.Now())
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(WithCaller(req.Context(), c))
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		for _, tool := range listedTools(t, rec) {
			if tool["name"] == "hack_show_organiser" {
				t.Fatalf("hack_show_organiser listed for excluded app name %q", name)
			}
		}
	}
	for _, name := range []string{"", "Claude", "Claude for Desktop"} {
		c := organiserCaller("alice", name)
		s.clientUI.remember(c, true, time.Now())
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(WithCaller(req.Context(), c))
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		found := false
		for _, tool := range listedTools(t, rec) {
			if tool["name"] == "hack_show_organiser" {
				found = true
			}
		}
		if !found {
			t.Fatalf("hack_show_organiser not listed for allowed app name %q", name)
		}
	}
}

func TestHackShowOrganiserRunPerView(t *testing.T) {
	t.Setenv("MCP_APP_HACK_ORGANISER", "on")
	s := newTestServer(organiserUpstream())
	c := organiserCaller("alice", "")
	run := func(args map[string]any) (output, error) {
		call := &call{server: s, caller: c, orig: httptest.NewRequest("POST", "/mcp", nil), uiClient: true}
		return s.hackByName["hack_show_organiser"].run(call, args)
	}

	for _, view := range []string{"overview", "people", "judging"} {
		out, err := run(map[string]any{"view": view, "slug": "mockjam"})
		if err != nil {
			t.Fatalf("%s: %v", view, err)
		}
		if out.Structured["view"] != view || out.Structured["slug"] != "mockjam" {
			t.Fatalf("%s structured content: %#v", view, out.Structured)
		}
		event, ok := out.Structured["event"].(map[string]any)
		if !ok || event["title"] != "Mock Jam" {
			t.Fatalf("%s: event detail missing: %#v", view, out.Structured["event"])
		}
		organiser, ok := out.Structured["organiser"].(map[string]any)
		if !ok {
			t.Fatalf("%s: organiser counts missing: %#v", view, out.Structured["organiser"])
		}
		counts, ok := organiser["counts"].(map[string]any)
		if !ok || counts["participants"] != float64(3) {
			t.Fatalf("%s: counts missing/wrong: %#v", view, organiser)
		}
		if out.Text == "" {
			t.Fatalf("%s: empty text summary", view)
		}
		if strings.Contains(out.Text, "Mock Jam") {
			t.Fatalf("%s: text summary interpolates the event title instead of a fixed sentence: %q", view, out.Text)
		}
		encoded, _ := json.Marshal(out)
		if strings.Contains(string(encoded), "organiser-key") {
			t.Fatalf("%s: credential leaked into result", view)
		}
		for key := range event {
			allowedKey := map[string]bool{"slug": true, "title": true, "stage": true, "scoring_open": true, "judging_locked_at": true, "results_published": true, "submission_deadline": true, "time_zone": true, "url": true}
			if !allowedKey[key] {
				t.Fatalf("%s: event field %q is outside hackShowOrganiserEventFields' whitelist: %#v", view, key, event)
			}
		}
	}

	if _, err := run(map[string]any{"view": "bogus", "slug": "mockjam"}); err == nil {
		t.Fatal("bogus view accepted")
	}
	if _, err := run(map[string]any{"view": "overview"}); err == nil {
		t.Fatal("missing slug accepted")
	}
}

// TestHackShowOrganiserContentNeverCarriesSensitiveData is the organiser
// equivalent of the judge app's TestHackShowJudgeContentNeverCarriesEventProse:
// join_url/judge_url (the event's own join and judge codes) and the
// organiser's own display_name must never appear in out.Text or
// out.Structured (the model-visible fields), only in out.Meta (the
// view-only channel organiser-overview.js's refreshEvent reads via
// callToolResult, never callTool).
func TestHackShowOrganiserContentNeverCarriesSensitiveData(t *testing.T) {
	t.Setenv("MCP_APP_HACK_ORGANISER", "on")
	s := newTestServer(organiserUpstream())
	c := organiserCaller("alice", "")
	run := func(args map[string]any) (output, error) {
		call := &call{server: s, caller: c, orig: httptest.NewRequest("POST", "/mcp", nil), uiClient: true}
		return s.hackByName["hack_show_organiser"].run(call, args)
	}

	markers := []string{"ORGANISER-SECRET-JOIN", "ORGANISER-SECRET-JUDGE", "ORGANISER-OWN-NAME"}

	for _, view := range []string{"overview", "people", "judging"} {
		out, err := run(map[string]any{"view": view, "slug": "mockjam"})
		if err != nil {
			t.Fatalf("%s: %v", view, err)
		}

		structuredJSON, err := json.Marshal(out.Structured)
		if err != nil {
			t.Fatalf("%s: marshal structured content: %v", view, err)
		}
		for _, marker := range markers {
			if strings.Contains(out.Text, marker) {
				t.Fatalf("%s: sensitive value %q leaked into model-visible Text: %q", view, marker, out.Text)
			}
			if strings.Contains(string(structuredJSON), marker) {
				t.Fatalf("%s: sensitive value %q leaked into model-visible Structured: %s", view, marker, structuredJSON)
			}
		}

		meta, _ := out.Meta["simple-hack/organiser-view"].(map[string]any)
		for _, marker := range markers {
			found := false
			for _, v := range meta {
				if s, ok := v.(string); ok && strings.Contains(s, marker) {
					found = true
				}
			}
			if !found {
				t.Fatalf("%s: sensitive value %q missing from out.Meta[\"simple-hack/organiser-view\"]: %#v", view, marker, out.Meta)
			}
		}
	}
}

// TestHackShowOrganiserRequiresOrganiserRole: this show tool's whole job is
// opening an ORGANISER view, so it refuses for a judge or participant of
// the same event, mirroring the judge app's own TestHackShowJudgeRequires
// JudgeOrOrganiserRole.
func TestHackShowOrganiserRequiresOrganiserRole(t *testing.T) {
	t.Setenv("MCP_APP_HACK_ORGANISER", "on")
	for _, role := range []string{"judge", "participant"} {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /v1/hack/events/mockjam", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"event":{"slug":"mockjam","title":"Mock Jam","stage":"open"},"role":"` + role + `"}`))
		})
		s := newTestServer(mux)
		c := &call{server: s, caller: organiserCaller("alice", ""), orig: httptest.NewRequest("POST", "/mcp", nil), uiClient: true}
		out, err := s.hackByName["hack_show_organiser"].run(c, map[string]any{"view": "overview", "slug": "mockjam"})
		if err == nil {
			t.Fatalf("hack_show_organiser opened overview for role %q", role)
		}
		if out.Structured != nil {
			t.Fatalf("refusal carries structuredContent: %#v", out.Structured)
		}
		for _, view := range []string{"people", "judging"} {
			if _, err := s.hackByName["hack_show_organiser"].run(c, map[string]any{"view": view, "slug": "mockjam"}); err == nil {
				t.Fatalf("hack_show_organiser opened %s for role %q", view, role)
			}
		}
	}
}

func TestHackShowOrganiserRefusesNonUIAndDisabled(t *testing.T) {
	s := newTestServer(organiserUpstream())
	c := organiserCaller("alice", "")
	tool := s.hackByName["hack_show_organiser"]

	t.Setenv("MCP_APP_HACK_ORGANISER", "on")
	textOnly := &call{server: s, caller: c, orig: httptest.NewRequest("POST", "/mcp", nil), uiClient: false}
	if _, err := tool.run(textOnly, map[string]any{"view": "overview", "slug": "mockjam"}); err == nil {
		t.Fatal("text-only connection allowed to run hack_show_organiser")
	}

	t.Setenv("MCP_APP_HACK_ORGANISER", "off")
	uiCall := &call{server: s, caller: c, orig: httptest.NewRequest("POST", "/mcp", nil), uiClient: true}
	if _, err := tool.run(uiCall, map[string]any{"view": "overview", "slug": "mockjam"}); err == nil {
		t.Fatal("hack_show_organiser ran with its flag off")
	}
}

func TestHackOrganiserBundleFresh(t *testing.T) {
	js, err := bundlegen.GenerateOrganiserJS()
	if err != nil {
		t.Fatal(err)
	}
	gotJS, err := appFiles.ReadFile("apps/hack-organiser-bundle.js")
	if err != nil {
		t.Fatal(err)
	}
	if js != string(gotJS) {
		t.Fatal("apps/hack-organiser-bundle.js is stale; run: go run ./internal/mcp/apps/gentool")
	}
	css, err := bundlegen.GenerateCSS()
	if err != nil {
		t.Fatal(err)
	}
	gotCSS, err := appFiles.ReadFile("apps/hack-organiser-kit.css")
	if err != nil {
		t.Fatal(err)
	}
	if css != string(gotCSS) {
		t.Fatal("apps/hack-organiser-kit.css is stale; run: go run ./internal/mcp/apps/gentool")
	}
}

// TestHackOrganiserRouteMapCoversCalls keeps hack-organiser-view.js's
// hand-written route table (JS, not generated) honest against the real
// tool registrations: every REST path+method the organiser screens call,
// resolved by hand in that file, must match a real hackRouteTool/
// hand-written-tool registration with the same method and path template,
// found by actually invoking each tool here and recording what it sends
// upstream. Mirrors the judge app's TestHackJudgeRouteMapCoversCalls.
func TestHackOrganiserRouteMapCoversCalls(t *testing.T) {
	seen := map[string]bool{}
	mux := http.NewServeMux()
	record := func(method, pattern string) {
		mux.HandleFunc(method+" "+pattern, func(w http.ResponseWriter, r *http.Request) {
			seen[r.Method+" "+r.URL.Path] = true
			w.Header().Set("Content-Type", "application/json")
			if r.Method == http.MethodDelete {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			_, _ = w.Write([]byte(`{"ok":true,"criteria":[{"id":"c1","name":"Idea","weight":100,"max_points":5}],"conflicts":[],"questions":[],"assignments":[],"teams":[],"url":"https://x","expires_at":"2026-01-01T00:00:00Z"}`))
		})
	}
	for _, p := range []struct{ method, pattern string }{
		{"GET", "/v1/hack/events/{slug}/people"},
		{"GET", "/v1/hack/events/{slug}/applications"},
		{"POST", "/v1/hack/events/{slug}/codes/{kind}"},
		{"GET", "/v1/hack/events/{slug}/registration"},
		{"PUT", "/v1/hack/events/{slug}/registration"},
		{"POST", "/v1/hack/events/{slug}/applications/{user_id}/decision"},
		{"DELETE", "/v1/hack/events/{slug}/people/{user_id}/key"},
		{"DELETE", "/v1/hack/events/{slug}/people/{user_id}"},
		{"DELETE", "/v1/hack/events/{slug}/organisers/{user_id}"},
		{"POST", "/v1/hack/events/{slug}/organiser-invite"},
		{"DELETE", "/v1/hack/events/{slug}/organiser-invite"},
		{"GET", "/v1/hack/events/{slug}/rubric"},
		{"PUT", "/v1/hack/events/{slug}/rubric"},
		{"GET", "/v1/hack/events/{slug}/judging/settings"},
		{"PATCH", "/v1/hack/events/{slug}/judging/settings"},
		{"GET", "/v1/hack/events/{slug}/conflicts"},
		{"POST", "/v1/hack/events/{slug}/conflicts"},
		{"DELETE", "/v1/hack/events/{slug}/conflicts/{team}"},
		{"GET", "/v1/hack/events/{slug}/teams"},
		{"GET", "/v1/hack/events/{slug}/assignments"},
		{"POST", "/v1/hack/events/{slug}/assignments/generate"},
		{"GET", "/v1/hack/events/{slug}/judging/dashboard"},
		{"DELETE", "/v1/hack/events/{slug}/judging/former-judges/{user_id}"},
		{"POST", "/v1/hack/events/{slug}/judging/lock"},
		{"POST", "/v1/hack/events/{slug}/judging/unlock"},
	} {
		record(p.method, p.pattern)
	}
	// hack_update_event needs a real event shape (hackEventOutput refuses
	// anything else), unlike the other routes above where this test only
	// checks which path was called (same reason the judge test's
	// TestHackJudgeRouteMapCoversCalls has its own GET /v1/hack/events/{slug}
	// handler for hack_get_event).
	mux.HandleFunc("PATCH /v1/hack/events/{slug}", func(w http.ResponseWriter, r *http.Request) {
		seen[r.Method+" "+r.URL.Path] = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"event":{"slug":"mockjam","title":"Mock Jam","stage":"judging","judging_ends_at":"2026-01-01T00:00:00Z"},"role":"organiser"}`))
	})
	s := newTestServer(mux)
	c := &call{server: s, caller: organiserCaller("alice", ""), orig: httptest.NewRequest("POST", "/mcp", nil)}
	calls := []struct {
		tool string
		args map[string]any
	}{
		{"hack_get_people", map[string]any{"slug": "mockjam"}},
		{"hack_get_applications", map[string]any{"slug": "mockjam"}},
		{"hack_regenerate_code", map[string]any{"slug": "mockjam", "kind": "join"}},
		{"hack_get_registration", map[string]any{"slug": "mockjam"}},
		{"hack_set_registration", map[string]any{"slug": "mockjam", "body": map[string]any{"questions": []any{}, "approval_required": true}}},
		{"hack_decide_application", map[string]any{"slug": "mockjam", "user_id": "u1", "body": map[string]any{"decision": "approved"}}},
		{"hack_revoke_person_key", map[string]any{"slug": "mockjam", "user_id": "u1"}},
		{"hack_remove_person", map[string]any{"slug": "mockjam", "user_id": "u1"}},
		{"hack_remove_organiser", map[string]any{"slug": "mockjam", "user_id": "u1"}},
		{"hack_create_organiser_invite", map[string]any{"slug": "mockjam"}},
		{"hack_revoke_organiser_invite", map[string]any{"slug": "mockjam"}},
		{"hack_get_rubric", map[string]any{"slug": "mockjam"}},
		{"hack_set_rubric", map[string]any{"slug": "mockjam", "criteria": []any{map[string]any{"name": "Idea", "weight": 100.0, "max_points": 5.0}}}},
		{"hack_get_judging_settings", map[string]any{"slug": "mockjam"}},
		{"hack_set_judging_settings", map[string]any{"slug": "mockjam", "body": map[string]any{"assignment_mode": "open"}}},
		{"hack_get_conflicts", map[string]any{"slug": "mockjam"}},
		{"hack_set_judge_conflict", map[string]any{"slug": "mockjam", "body": map[string]any{"team_id": "t1", "judge_user_id": "j1"}}},
		{"hack_remove_judge_conflict", map[string]any{"slug": "mockjam", "team_id": "t1", "judge_user_id": "j1"}},
		{"hack_get_teams", map[string]any{"slug": "mockjam"}},
		{"hack_get_assignments", map[string]any{"slug": "mockjam"}},
		{"hack_generate_assignments", map[string]any{"slug": "mockjam", "body": map[string]any{"include_organisers": false}}},
		{"hack_get_judging_dashboard", map[string]any{"slug": "mockjam"}},
		{"hack_delete_former_judge_scores", map[string]any{"slug": "mockjam", "user_id": "u1", "body": map[string]any{"confirm": "mockjam"}}},
		{"hack_lock_judging", map[string]any{"slug": "mockjam"}},
		{"hack_unlock_judging", map[string]any{"slug": "mockjam", "body": map[string]any{"reason": "testing"}}},
		{"hack_update_event", map[string]any{"slug": "mockjam", "judging_ends_at": "2026-01-01T00:00:00Z"}},
	}
	wantPaths := []string{
		"GET /v1/hack/events/mockjam/people", "GET /v1/hack/events/mockjam/applications",
		"POST /v1/hack/events/mockjam/codes/join", "GET /v1/hack/events/mockjam/registration",
		"PUT /v1/hack/events/mockjam/registration", "POST /v1/hack/events/mockjam/applications/u1/decision",
		"DELETE /v1/hack/events/mockjam/people/u1/key", "DELETE /v1/hack/events/mockjam/people/u1",
		"DELETE /v1/hack/events/mockjam/organisers/u1", "POST /v1/hack/events/mockjam/organiser-invite",
		"DELETE /v1/hack/events/mockjam/organiser-invite", "GET /v1/hack/events/mockjam/rubric",
		"PUT /v1/hack/events/mockjam/rubric", "GET /v1/hack/events/mockjam/judging/settings",
		"PATCH /v1/hack/events/mockjam/judging/settings", "GET /v1/hack/events/mockjam/conflicts",
		"POST /v1/hack/events/mockjam/conflicts", "DELETE /v1/hack/events/mockjam/conflicts/t1",
		"GET /v1/hack/events/mockjam/teams", "GET /v1/hack/events/mockjam/assignments",
		"POST /v1/hack/events/mockjam/assignments/generate", "GET /v1/hack/events/mockjam/judging/dashboard",
		"DELETE /v1/hack/events/mockjam/judging/former-judges/u1", "POST /v1/hack/events/mockjam/judging/lock",
		"POST /v1/hack/events/mockjam/judging/unlock", "PATCH /v1/hack/events/mockjam",
	}
	for i, call := range calls {
		c.policy = s.hackByName[call.tool].policy
		if _, err := s.hackByName[call.tool].run(c, call.args); err != nil {
			t.Fatalf("%s: %v", call.tool, err)
		}
		if !seen[wantPaths[i]] {
			t.Fatalf("%s did not call %s; route table in hack-organiser-view.js may be stale. Seen: %v", call.tool, wantPaths[i], seen)
		}
	}
}

// TestHackOrganiserViewRouteBodyKeysMatchToolSchema hand-maintains the
// same bodyKeys lists hack-organiser-view.js's route table declares (there
// is no automatic way to read a JS literal from a Go test) and checks each
// key is actually in the tool's own InputSchema -- flat (top-level) for a
// hand-written tool, nested under "body" for a generic hackRouteTool.
// Mirrors the judge app's TestHackJudgeViewRouteBodyKeysMatchToolSchema,
// which caught a real mismatched-schema bug live.
func TestHackOrganiserViewRouteBodyKeysMatchToolSchema(t *testing.T) {
	s := newTestServer(http.NewServeMux())
	flatBodyKeys := map[string][]string{
		// hack_app_set_event_stage, not hack_set_event_stage: the route
		// table calls the app-only twin for this write (Grok security
		// review finding 2, SIM-344 phase 2), whose InputSchema is the
		// same object as the original's (hackAppOnlyTwin copies the base
		// Tool verbatim before overriding run/Meta/OutputSchema), so this
		// still pins the same body-key contract.
		"hack_app_set_event_stage": {"stage"},
		"hack_set_rubric":          {"criteria", "confirm_score_deletion"},
		"hack_update_event":        {"judging_ends_at"},
	}
	for tool, keys := range flatBodyKeys {
		schema, ok := s.hackByName[tool]
		if !ok {
			t.Fatalf("%s: not a registered tool", tool)
		}
		props, _ := schema.InputSchema["properties"].(map[string]any)
		for _, key := range keys {
			if _, ok := props[key]; !ok {
				t.Errorf("%s: hack-organiser-view.js's route table sends flat body key %q, which is not in this tool's InputSchema properties: %v", tool, key, props)
			}
		}
	}
	wrappedBodyTools := []string{
		"hack_set_registration", "hack_decide_application", "hack_set_judging_settings",
		"hack_set_judge_conflict", "hack_generate_assignments", "hack_delete_former_judge_scores",
		"hack_unlock_judging",
	}
	for _, tool := range wrappedBodyTools {
		schema, ok := s.hackByName[tool]
		if !ok {
			t.Fatalf("%s: not a registered tool", tool)
		}
		props, _ := schema.InputSchema["properties"].(map[string]any)
		if _, ok := props["body"]; !ok {
			t.Errorf("%s: hack-organiser-view.js's route table wraps the REST body under \"body\", but this tool's InputSchema has no such property: %v", tool, props)
		}
	}
	// hack_remove_judge_conflict's judge_user_id is a flat query-forwarded
	// argument (HackOrganiserTools' own special case), not a body field.
	schema := s.hackByName["hack_remove_judge_conflict"]
	props, _ := schema.InputSchema["properties"].(map[string]any)
	if _, ok := props["judge_user_id"]; !ok {
		t.Errorf("hack_remove_judge_conflict: route table forwards judge_user_id, which is not in this tool's InputSchema properties: %v", props)
	}
}
