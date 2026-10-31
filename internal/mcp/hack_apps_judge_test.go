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

func judgeUpstream() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/hack/judge/invite-code", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"slug":"mockjam","title":"Mock Jam","joinable":true,"coc_default":"Be kind."}`))
	})
	mux.HandleFunc("GET /v1/hack/events/mockjam", func(w http.ResponseWriter, r *http.Request) {
		// about/rules/prizes carry distinctive marker text (not empty
		// strings) specifically so a test can tell "never present" apart
		// from "present but happens to be empty".
		_, _ = w.Write([]byte(`{"event":{"slug":"mockjam","title":"Mock Jam","stage":"closed","scoring_open":true,"judging_locked_at":null,"results_published":false,"submission_deadline":null,"time_zone":"UTC","about":"ORGANISER-PROSE-ABOUT","rules":"ORGANISER-PROSE-RULES","prizes":"ORGANISER-PROSE-PRIZES"},"role":"judge"}`))
	})
	return mux
}

func judgeCaller(handle, appName string) Caller {
	return Caller{APIKey: "judge-key", Mode: CallerModeEvents, Handle: handle, AppName: appName}
}

func TestHackJudgeResourceGating(t *testing.T) {
	s := newTestServer(judgeUpstream())
	for _, flag := range []string{"", "off", "unknown", "accounts:bob", "accounts:alice", "on"} {
		t.Setenv("MCP_APP_HACK_JUDGE", flag)
		for _, c := range []Caller{
			judgeCaller("alice", ""),
			{APIKey: "k", Mode: CallerModeHackSite, Handle: "alice"},
			{APIKey: "k", Handle: "alice"}, // Mode "": Simple Host, must never see this screen
		} {
			enabled := c.Mode == CallerModeEvents && appFlag(flag, c.Handle)
			if got := len(s.hackEnabledScreens(c)); (got > 0) != enabled {
				t.Fatalf("flag=%q mode=%q handle=%q: hackEnabledScreens=%d, want enabled=%v", flag, c.Mode, c.Handle, got, enabled)
			}
			// hackAppResources/readHackApp take the full three-part gate
			// (flag, events mode, not ChatGPT/OpenAI/Codex, UI declared)
			// as `allowed`, computed by the caller (server.go's dispatch);
			// this loop only varies flag/mode, so it passes `enabled`
			// straight through -- TestHackJudgeResourceNeverReadableWithoutGate
			// below exercises the app-name/UI-declared half through the
			// real dispatch path.
			if got := len(s.hackAppResources(c, enabled)); (got > 0) != enabled {
				t.Fatalf("flag=%q mode=%q handle=%q: hackAppResources=%d, want enabled=%v", flag, c.Mode, c.Handle, got, enabled)
			}
			params, _ := json.Marshal(map[string]string{"uri": "ui://simple-hack/judge"})
			resp := s.readHackApp(request{ID: json.RawMessage(`1`), Params: params}, c, enabled)
			if (resp.Error == nil) != enabled {
				t.Fatalf("flag=%q mode=%q handle=%q: readHackApp error=%v, want enabled=%v", flag, c.Mode, c.Handle, resp.Error, enabled)
			}
		}
	}
}

// TestHackJudgeResourceNeverReadableWithoutGate is the resources/list and
// resources/read half of the owner's gate (Grok security review, SIM-344):
// a connection that would never see hack_show_judge in tools/list -- an
// excluded app name, or one that never declared MCP Apps UI support --
// must not be able to read ui://simple-hack/judge either, even by naming
// its URI directly, bypassing the tool list entirely.
func TestHackJudgeResourceNeverReadableWithoutGate(t *testing.T) {
	t.Setenv("MCP_APP_HACK_JUDGE", "on")
	s := newTestServer(judgeUpstream())
	readResource := func(c Caller) (listed bool, readErr bool) {
		listReq := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"resources/list","params":{}}`))
		listReq.Header.Set("Content-Type", "application/json")
		listReq = listReq.WithContext(WithCaller(listReq.Context(), c))
		listRec := httptest.NewRecorder()
		s.ServeHTTP(listRec, listReq)
		resources := decode(t, listRec)["result"].(map[string]any)["resources"].([]any)
		for _, raw := range resources {
			if raw.(map[string]any)["uri"] == "ui://simple-hack/judge" {
				listed = true
			}
		}
		readBody, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "resources/read", "params": map[string]any{"uri": "ui://simple-hack/judge"}})
		readReq := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(string(readBody)))
		readReq.Header.Set("Content-Type", "application/json")
		readReq = readReq.WithContext(WithCaller(readReq.Context(), c))
		readRec := httptest.NewRecorder()
		s.ServeHTTP(readRec, readReq)
		_, readErr = decode(t, readRec)["error"]
		return listed, readErr
	}

	// Distinct API keys: clientUIMemory is keyed by credential
	// (OAuthGrantID, else APIKey -- see clientui.go's clientUIKey), not by
	// the full Caller, so three callers sharing one key would share one
	// remembered UI answer regardless of AppName.
	excluded := Caller{APIKey: "judge-key-excluded", Mode: CallerModeEvents, Handle: "alice", AppName: "ChatGPT"}
	s.clientUI.remember(excluded, true, time.Now())
	if listed, readErr := readResource(excluded); listed || !readErr {
		t.Fatalf("ChatGPT: listed=%v readErr=%v, want listed=false readErr=true", listed, readErr)
	}

	noUI := Caller{APIKey: "judge-key-noui", Mode: CallerModeEvents, Handle: "alice"}
	if listed, readErr := readResource(noUI); listed || !readErr {
		t.Fatalf("no UI declared: listed=%v readErr=%v, want listed=false readErr=true", listed, readErr)
	}

	allowed := Caller{APIKey: "judge-key-allowed", Mode: CallerModeEvents, Handle: "alice"}
	s.clientUI.remember(allowed, true, time.Now())
	if listed, readErr := readResource(allowed); !listed || readErr {
		t.Fatalf("flagged, UI-declared, allowed app name: listed=%v readErr=%v, want listed=true readErr=false", listed, readErr)
	}
}

func TestHackJudgeResourceRendersAndCSP(t *testing.T) {
	t.Setenv("MCP_APP_HACK_JUDGE", "on")
	s := newTestServer(judgeUpstream())
	c := judgeCaller("alice", "")
	params, _ := json.Marshal(map[string]string{"uri": "ui://simple-hack/judge"})

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

		if strings.Contains(page, "__NONCE__") || strings.Contains(page, "__STYLE__") || strings.Contains(page, "__BUNDLE__") || strings.Contains(page, "__VIEW__") || strings.Contains(page, "__STUBS__") || strings.Contains(page, "__BRIDGE__") {
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
				t.Fatalf("hack-judge screen declares external %s: %v", key, domains)
			}
		}
	}
	if nonces[0] == nonces[1] {
		t.Fatal("nonce reused across reads")
	}
	if strings.Contains(pages[0], "judge-key") {
		t.Fatal("API key leaked into rendered page")
	}
}

// TestHackShowJudgeNotListedWithoutUISupport is the owner's second
// requirement (2026-10-31): hack_show_judge must not appear for a
// connection that has not declared MCP Apps UI support, even with its
// flag on.
func TestHackShowJudgeNotListedWithoutUISupport(t *testing.T) {
	t.Setenv("MCP_APP_HACK_JUDGE", "on")
	s := newTestServer(judgeUpstream())
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(WithCaller(req.Context(), judgeCaller("alice", "")))
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	listed := listedTools(t, rec)
	for _, tool := range listed {
		if tool["name"] == "hack_show_judge" {
			t.Fatal("hack_show_judge listed to a connection that never declared MCP Apps UI support")
		}
	}
}

// TestHackShowJudgeListedOnceUIDeclared exercises the initialize-era path
// (clientUIMemory, server.go's clientUI.remember/lookup): a connection that
// declared UI support at initialize, then asks tools/list later (no
// capabilities on that later call -- the normal shape for that era).
func TestHackShowJudgeListedOnceUIDeclared(t *testing.T) {
	t.Setenv("MCP_APP_HACK_JUDGE", "on")
	s := newTestServer(judgeUpstream())
	c := judgeCaller("alice", "")
	s.clientUI.remember(c, true, time.Now())
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(WithCaller(req.Context(), c))
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	listed := listedTools(t, rec)
	found := false
	for _, tool := range listed {
		if tool["name"] == "hack_show_judge" {
			found = true
			if tool["_meta"] == nil {
				t.Fatal("hack_show_judge listed without _meta.ui.resourceUri")
			}
		}
	}
	if !found {
		t.Fatal("hack_show_judge not listed for a UI-declared, flagged-in connection")
	}
}

// TestHackShowJudgeModernPerCallCapability exercises the stateless/modern
// path, where clientCapabilities travel on every call including
// tools/list, per server.go's validateHeaders.
func TestHackShowJudgeModernPerCallCapability(t *testing.T) {
	t.Setenv("MCP_APP_HACK_JUDGE", "on")
	s := newTestServer(judgeUpstream())
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
		req = req.WithContext(WithCaller(req.Context(), judgeCaller("alice", "")))
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		listed := listedTools(t, rec)
		found := false
		for _, tool := range listed {
			if tool["name"] == "hack_show_judge" {
				found = true
			}
		}
		if found != ui {
			t.Fatalf("ui=%v: hack_show_judge listed=%v", ui, found)
		}
	}
}

// TestHackShowJudgeNeverForExcludedAppNames is the owner's first
// requirement (2026-10-31): ChatGPT, OpenAI and Codex app names never see
// hack_show_judge, even flagged in and UI-declared, and the ChatGPT app's
// pinned tools/list (TestSubmissionToolsMatchChatGPTListing) stays
// unaffected because that test's caller has Mode == "" (flatListApp's own
// surface), never CallerModeEvents.
func TestHackShowJudgeNeverForExcludedAppNames(t *testing.T) {
	t.Setenv("MCP_APP_HACK_JUDGE", "on")
	s := newTestServer(judgeUpstream())
	for _, name := range []string{"ChatGPT", "OpenAI", "Codex", "codex-cli", "chatgpt-connector"} {
		c := judgeCaller("alice", name)
		s.clientUI.remember(c, true, time.Now())
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(WithCaller(req.Context(), c))
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		for _, tool := range listedTools(t, rec) {
			if tool["name"] == "hack_show_judge" {
				t.Fatalf("hack_show_judge listed for excluded app name %q", name)
			}
		}
	}
	for _, name := range []string{"", "Claude", "Claude for Desktop"} {
		c := judgeCaller("alice", name)
		s.clientUI.remember(c, true, time.Now())
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(WithCaller(req.Context(), c))
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		found := false
		for _, tool := range listedTools(t, rec) {
			if tool["name"] == "hack_show_judge" {
				found = true
			}
		}
		if !found {
			t.Fatalf("hack_show_judge not listed for allowed app name %q", name)
		}
	}
}

func TestHackShowJudgeRunPerView(t *testing.T) {
	t.Setenv("MCP_APP_HACK_JUDGE", "on")
	s := newTestServer(judgeUpstream())
	c := judgeCaller("alice", "")
	run := func(args map[string]any) (output, error) {
		call := &call{server: s, caller: c, orig: httptest.NewRequest("POST", "/mcp", nil), uiClient: true}
		return s.hackByName["hack_show_judge"].run(call, args)
	}

	out, err := run(map[string]any{"view": "invite", "code": "invite-code"})
	if err != nil {
		t.Fatalf("invite: %v", err)
	}
	if out.Structured["view"] != "invite" || out.Structured["code"] != "invite-code" {
		t.Fatalf("invite structured content: %#v", out.Structured)
	}
	if out.Text == "" {
		t.Fatal("invite: empty text summary")
	}

	for _, view := range []string{"queue", "score", "rubric"} {
		out, err := run(map[string]any{"view": view, "slug": "mockjam"})
		if err != nil {
			t.Fatalf("%s: %v", view, err)
		}
		if out.Structured["view"] != view || out.Structured["slug"] != "mockjam" {
			t.Fatalf("%s structured content: %#v", view, out.Structured)
		}
		event, ok := out.Structured["event"].(map[string]any)
		if !ok || event["scoring_open"] != true {
			t.Fatalf("%s: event detail missing scoring_open: %#v", view, out.Structured["event"])
		}
		if out.Text == "" {
			t.Fatalf("%s: empty text summary", view)
		}
		if strings.Contains(out.Text, "Mock Jam") {
			t.Fatalf("%s: text summary interpolates the event title instead of a fixed sentence: %q", view, out.Text)
		}
		encoded, _ := json.Marshal(out)
		if strings.Contains(string(encoded), "judge-key") {
			t.Fatalf("%s: credential leaked into result", view)
		}
		// hackShowJudgeEventFields' whitelist (Grok security review,
		// SIM-344): the fixture's upstream response carries "title" and
		// "stage" (both kept) but nothing an organiser-only field like
		// join_code/judge_code could hide behind; this just pins that the
		// event map is never passed through wholesale. about/rules/prizes
		// are organiser-written prose and travel only via out.Meta (see
		// TestHackShowJudgeContentNeverCarriesEventProse below), never in
		// the model-visible structured content.
		for key := range event {
			allowedKey := map[string]bool{"slug": true, "title": true, "stage": true, "scoring_open": true, "judging_locked_at": true, "results_published": true, "submission_deadline": true, "time_zone": true}
			if !allowedKey[key] {
				t.Fatalf("%s: event field %q is outside hackShowJudgeEventFields' whitelist: %#v", view, key, event)
			}
		}
	}

	out, err = run(map[string]any{"view": "score", "slug": "mockjam", "team_id": "team-1"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Structured["team_id"] != "team-1" {
		t.Fatalf("team_id not passed through: %#v", out.Structured)
	}

	if _, err := run(map[string]any{"view": "bogus"}); err == nil {
		t.Fatal("bogus view accepted")
	}
	if _, err := run(map[string]any{"view": "queue"}); err == nil {
		t.Fatal("missing slug accepted")
	}
}

// TestHackShowJudgeContentNeverCarriesEventProse (coordinator follow-up to
// Grok security finding 2, SIM-344): about/rules/prizes are organiser-
// written free text, exactly the kind of prose that must never reach the
// model. They travel to the view only through the result's _meta (under
// "simple-hack/event-text"), which server.go attaches to the JSON-RPC
// result alongside content/structuredContent but which bridge.js's
// callTool() -- the one the model-facing tool-call path always uses --
// discards; only callToolResult(), used solely by the judge view's own
// refreshEvent(), reads it. This test pins that the organiser prose never
// appears in out.Text or out.Structured (the model-visible fields) while
// confirming it does land in out.Meta (the view-only channel), so a
// regression that moves it back into the whitelist is caught immediately.
func TestHackShowJudgeContentNeverCarriesEventProse(t *testing.T) {
	t.Setenv("MCP_APP_HACK_JUDGE", "on")
	s := newTestServer(judgeUpstream())
	c := judgeCaller("alice", "")
	run := func(args map[string]any) (output, error) {
		call := &call{server: s, caller: c, orig: httptest.NewRequest("POST", "/mcp", nil), uiClient: true}
		return s.hackByName["hack_show_judge"].run(call, args)
	}

	markers := []string{"ORGANISER-PROSE-ABOUT", "ORGANISER-PROSE-RULES", "ORGANISER-PROSE-PRIZES"}

	for _, view := range []string{"queue", "score", "rubric"} {
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
				t.Fatalf("%s: organiser prose %q leaked into model-visible Text: %q", view, marker, out.Text)
			}
			if strings.Contains(string(structuredJSON), marker) {
				t.Fatalf("%s: organiser prose %q leaked into model-visible Structured: %s", view, marker, structuredJSON)
			}
		}

		text, _ := out.Meta["simple-hack/event-text"].(map[string]any)
		for _, marker := range markers {
			found := false
			for _, v := range text {
				if s, ok := v.(string); ok && s == marker {
					found = true
				}
			}
			if !found {
				t.Fatalf("%s: organiser prose %q missing from out.Meta[\"simple-hack/event-text\"]: %#v", view, marker, out.Meta)
			}
		}
	}
}

// TestHackShowJudgeRequiresJudgeOrOrganiserRole (Grok security review,
// SIM-344): hack_show_judge's whole job is opening a judging view, so it
// refuses outright for a participant (or anyone else neither judging nor
// organising this event), rather than opening one with nothing useful in
// it -- and that refusal carries no structuredContent at all, same as
// every other tool's refusal.
func TestHackShowJudgeRequiresJudgeOrOrganiserRole(t *testing.T) {
	t.Setenv("MCP_APP_HACK_JUDGE", "on")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/hack/events/mockjam", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"event":{"slug":"mockjam","title":"Mock Jam","stage":"closed"},"role":"participant"}`))
	})
	s := newTestServer(mux)
	c := &call{server: s, caller: judgeCaller("alice", ""), orig: httptest.NewRequest("POST", "/mcp", nil), uiClient: true}
	out, err := s.hackByName["hack_show_judge"].run(c, map[string]any{"view": "queue", "slug": "mockjam"})
	if err == nil {
		t.Fatal("hack_show_judge opened a view for a participant")
	}
	if out.Structured != nil {
		t.Fatalf("refusal carries structuredContent: %#v", out.Structured)
	}
	for _, view := range []string{"score", "rubric"} {
		if _, err := s.hackByName["hack_show_judge"].run(c, map[string]any{"view": view, "slug": "mockjam"}); err == nil {
			t.Fatalf("hack_show_judge opened %s for a participant", view)
		}
	}
}

// TestHackShowJudgeRefusesNonUIAndDisabled is the call-time half of the
// same three gates filterHackShowTools enforces for listing: a direct
// call (e.g. from a host that lists tools once, then loses UI capability,
// or a model calling it directly on a text-only connection) must also
// refuse, not just stay unlisted.
func TestHackShowJudgeRefusesNonUIAndDisabled(t *testing.T) {
	s := newTestServer(judgeUpstream())
	c := judgeCaller("alice", "")
	tool := s.hackByName["hack_show_judge"]

	t.Setenv("MCP_APP_HACK_JUDGE", "on")
	textOnly := &call{server: s, caller: c, orig: httptest.NewRequest("POST", "/mcp", nil), uiClient: false}
	if _, err := tool.run(textOnly, map[string]any{"view": "queue", "slug": "mockjam"}); err == nil {
		t.Fatal("text-only connection allowed to run hack_show_judge")
	}

	t.Setenv("MCP_APP_HACK_JUDGE", "off")
	uiCall := &call{server: s, caller: c, orig: httptest.NewRequest("POST", "/mcp", nil), uiClient: true}
	if _, err := tool.run(uiCall, map[string]any{"view": "queue", "slug": "mockjam"}); err == nil {
		t.Fatal("hack_show_judge ran with its flag off")
	}
}

func TestHackJudgeBundleFresh(t *testing.T) {
	js, err := bundlegen.GenerateJS()
	if err != nil {
		t.Fatal(err)
	}
	gotJS, err := appFiles.ReadFile("apps/hack-judge-bundle.js")
	if err != nil {
		t.Fatal(err)
	}
	if js != string(gotJS) {
		t.Fatal("apps/hack-judge-bundle.js is stale; run: go run ./internal/mcp/apps/gentool")
	}
	css, err := bundlegen.GenerateCSS()
	if err != nil {
		t.Fatal(err)
	}
	gotCSS, err := appFiles.ReadFile("apps/hack-judge-kit.css")
	if err != nil {
		t.Fatal(err)
	}
	if css != string(gotCSS) {
		t.Fatal("apps/hack-judge-kit.css is stale; run: go run ./internal/mcp/apps/gentool")
	}
}

// TestHackJudgeRouteMapCoversCalls keeps hack-judge-view.js's hand-written
// route table (JS, not generated) honest against the real tool
// registrations: every REST path+method the judge screens call, resolved
// by hand in that file, must match a real memberTool/hackRouteTool
// registration with the same method and path template, found by actually
// invoking each tool here and recording what it sends upstream.
func TestHackJudgeRouteMapCoversCalls(t *testing.T) {
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
			_, _ = w.Write([]byte(`{"ok":true,"criteria":[],"queue":[],"conflicts":[],"scores":[],"content_type":"image/png"}`))
		})
	}
	for _, p := range []struct{ method, pattern string }{
		{"GET", "/v1/hack/judge/{code}"},
		{"POST", "/v1/hack/judge/{code}"},
		{"GET", "/v1/hack/events/{slug}/judge/queue"},
		{"GET", "/v1/hack/events/{slug}/rubric"},
		{"GET", "/v1/hack/events/{slug}/judge/scores/{team}"},
		{"PUT", "/v1/hack/events/{slug}/judge/scores/{team}"},
		{"GET", "/v1/hack/events/{slug}/conflicts"},
		{"POST", "/v1/hack/events/{slug}/conflicts"},
		{"DELETE", "/v1/hack/events/{slug}/conflicts/{team}"},
		{"GET", "/v1/hack/events/{slug}/teams/{team}/screenshot"},
	} {
		record(p.method, p.pattern)
	}
	// hack_get_event needs a real event shape (hackEventOutput refuses
	// anything else), unlike the other routes above where this test only
	// checks which path was called.
	mux.HandleFunc("GET /v1/hack/events/{slug}", func(w http.ResponseWriter, r *http.Request) {
		seen[r.Method+" "+r.URL.Path] = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"event":{"slug":"mockjam","title":"Mock Jam","stage":"closed"},"role":"judge"}`))
	})
	s := newTestServer(mux)
	c := &call{server: s, caller: judgeCaller("alice", ""), orig: httptest.NewRequest("POST", "/mcp", nil)}
	calls := []struct {
		tool string
		args map[string]any
	}{
		{"hack_preview_judge", map[string]any{"code": "c1"}},
		{"hack_join_judge", map[string]any{"code": "c1", "accept_coc": true, "display_name": "J"}},
		{"hack_get_judge_queue", map[string]any{"slug": "mockjam"}},
		{"hack_get_rubric", map[string]any{"slug": "mockjam"}},
		{"hack_get_event", map[string]any{"slug": "mockjam"}},
		{"hack_get_judge_scores", map[string]any{"slug": "mockjam", "team_id": "t1"}},
		{"hack_score_team", map[string]any{"slug": "mockjam", "team_id": "t1", "scores": []any{}, "comment": ""}},
		{"hack_get_my_conflicts", map[string]any{"slug": "mockjam"}},
		{"hack_declare_my_conflict", map[string]any{"slug": "mockjam", "team_id": "t1"}},
		{"hack_remove_my_conflict", map[string]any{"slug": "mockjam", "team_id": "t1"}},
		{"hack_get_team_screenshot", map[string]any{"slug": "mockjam", "team": "t1"}},
	}
	wantPaths := []string{
		"GET /v1/hack/judge/c1", "POST /v1/hack/judge/c1",
		"GET /v1/hack/events/mockjam/judge/queue", "GET /v1/hack/events/mockjam/rubric",
		"GET /v1/hack/events/mockjam", "GET /v1/hack/events/mockjam/judge/scores/t1",
		"PUT /v1/hack/events/mockjam/judge/scores/t1", "GET /v1/hack/events/mockjam/conflicts",
		"POST /v1/hack/events/mockjam/conflicts", "DELETE /v1/hack/events/mockjam/conflicts/t1",
		"GET /v1/hack/events/mockjam/teams/t1/screenshot",
	}
	for i, call := range calls {
		if _, err := s.hackByName[call.tool].run(c, call.args); err != nil {
			t.Fatalf("%s: %v", call.tool, err)
		}
		if !seen[wantPaths[i]] {
			t.Fatalf("%s did not call %s; route table in hack-judge-view.js may be stale. Seen: %v", call.tool, wantPaths[i], seen)
		}
	}
}

// TestHackJudgeViewRouteBodyKeysMatchToolSchema is the regression test for
// a real bug this plan's own browser harness caught live: hack-judge-
// view.js's route table listed hack_join_judge's bodyKeys as
// ["accept_coc", "display_name", "answers"], copying the website's own
// POST body (public-judge.js sends an empty answers: {}, left over from
// the participant join flow it was adapted from) -- but hack_join_judge's
// actual schema (unlike hack_join_event) has no answers property at all,
// since a judge invite asks no sign-up questions, so the server's
// additionalProperties:false schema check 400'd every "accept the
// invite" until this was found and the extra key dropped from that one
// route. This hand-maintains the same bodyKeys lists hack-judge-view.js's
// route table declares (there is no automatic way to read a JS literal
// from a Go test) and checks each key is actually in the tool's own
// InputSchema -- so a future edit that adds a bodyKey without checking
// the Go schema fails here instead of silently 400ing in the browser.
func TestHackJudgeViewRouteBodyKeysMatchToolSchema(t *testing.T) {
	s := newTestServer(http.NewServeMux())
	routeBodyKeys := map[string][]string{
		"hack_join_judge":          {"accept_coc", "display_name"},
		"hack_score_team":          {"scores", "comment"},
		"hack_declare_my_conflict": {"team_id"},
	}
	for tool, keys := range routeBodyKeys {
		schema, ok := s.hackByName[tool]
		if !ok {
			t.Fatalf("%s: not a registered tool", tool)
		}
		props, _ := schema.InputSchema["properties"].(map[string]any)
		for _, key := range keys {
			if _, ok := props[key]; !ok {
				t.Errorf("%s: hack-judge-view.js's route table sends body key %q, which is not in this tool's InputSchema properties: %v", tool, key, props)
			}
		}
	}
}
