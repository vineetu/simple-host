package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func sendMode(t *testing.T, s *Server, body, mode string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.RemoteAddr = "203.0.113.7:5555"
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(WithCaller(req.Context(), Caller{APIKey: "user-key", Mode: mode}))
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

func listedTools(t *testing.T, rec *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("tools/list %d %s", rec.Code, rec.Body.String())
	}
	raw := decode(t, rec)["result"].(map[string]any)["tools"].([]any)
	out := make([]map[string]any, len(raw))
	for i, item := range raw {
		out[i] = item.(map[string]any)
	}
	return out
}

func rpcMessage(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	out := decode(t, rec)
	if out["result"] != nil {
		t.Fatalf("unknown tool came back as a tool result: %s", rec.Body.String())
	}
	msg, _ := out["error"].(map[string]any)["message"].(string)
	return msg
}

type capturedCall struct {
	method, path, body, key string
}

type scriptUpstream struct {
	mu   sync.Mutex
	reqs []capturedCall
	ans  map[string]func(http.ResponseWriter, *http.Request)
}

func (u *scriptUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	u.mu.Lock()
	u.reqs = append(u.reqs, capturedCall{r.Method, r.URL.Path, string(b), r.Header.Get("X-API-Key")})
	u.mu.Unlock()
	if f, ok := u.ans[r.Method+" "+r.URL.Path]; ok {
		f(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(`{"error":"unscripted","code":"event_not_found"}`))
}

func (u *scriptUpstream) last() capturedCall {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.reqs[len(u.reqs)-1]
}

func (u *scriptUpstream) len() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.reqs)
}

func jsonReply(status int, body string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

func TestHackToolSchemasAndInventory(t *testing.T) {
	if !strings.Contains(codeHints["invalid_name"], "1 to 63") {
		t.Fatal("site invalid_name hint changed")
	}
	website := map[string]bool{}
	for _, tool := range Tools() {
		website[tool.Name] = true
	}
	hack := HackTools()
	if len(hack) == 0 {
		t.Fatal("no hack tools")
	}
	for _, tool := range hack {
		if !strings.HasPrefix(tool.Name, "hack_") {
			t.Errorf("%s is not prefixed", tool.Name)
		}
		if website[tool.Name] {
			t.Errorf("%s collides with a website tool", tool.Name)
		}
		if err := CheckOutputSchema(tool.OutputSchema); err != nil {
			t.Errorf("%s output: %v", tool.Name, err)
		}
		if err := CheckOutputSchema(tool.InputSchema); err != nil {
			t.Errorf("%s input: %v", tool.Name, err)
		}
	}
	ann := map[string][3]bool{
		"hack_set_event_stage": {false, true, true},
		"hack_set_rubric":      {false, true, false},
		"hack_update_event":    {false, false, false},
		"hack_create_event":    {false, false, true},
		"hack_list_events":     {true, false, false},
	}
	for _, tool := range hack {
		if want, ok := ann[tool.Name]; ok {
			got := [3]bool{tool.Annotations["readOnlyHint"].(bool), tool.Annotations["destructiveHint"].(bool), tool.Annotations["openWorldHint"].(bool)}
			if got != want {
				t.Errorf("%s annotations %v, want %v", tool.Name, got, want)
			}
		}
		for _, key := range []string{"readOnlyHint", "destructiveHint", "openWorldHint"} {
			if _, ok := tool.Annotations[key].(bool); !ok {
				t.Errorf("%s missing %s", tool.Name, key)
			}
		}
	}

	s := newTestServer(&scriptUpstream{ans: map[string]func(http.ResponseWriter, *http.Request){}})
	listed := listedTools(t, send(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, nil, true))
	if len(listed) != len(Tools()) {
		t.Fatalf("website list has %d tools, want %d", len(listed), len(Tools()))
	}
	for _, tool := range listed {
		name := tool["name"].(string)
		if strings.HasPrefix(name, "hack_") {
			t.Errorf("website list advertises %s", name)
		}
	}
	events := listedTools(t, sendMode(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, CallerModeEvents))
	visibleHack := make([]Tool, 0, len(hack))
	for _, tool := range hack {
		if tool.Name != "hack_create_team_key" {
			visibleHack = append(visibleHack, tool)
		}
	}
	if len(events) != len(visibleHack) {
		t.Fatalf("events list has %d tools, want %d", len(events), len(visibleHack))
	}
	for i, tool := range events {
		if tool["name"] != visibleHack[i].Name {
			t.Errorf("events tool %d is %v, want %s", i, tool["name"], visibleHack[i].Name)
		}
		if _, ok := tool["outputSchema"]; !ok {
			t.Errorf("%s listed without outputSchema", tool["name"])
		}
		if strings.HasPrefix(tool["name"].(string), "hack_") && website[tool["name"].(string)] {
			t.Errorf("events list includes website tool %s", tool["name"])
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.RemoteAddr = "203.0.113.7:5555"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("MCP-Protocol-Version", "2025-03-26")
	req = req.WithContext(WithCaller(req.Context(), Caller{APIKey: "user-key", Mode: CallerModeEvents}))
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	for _, tool := range listedTools(t, rec) {
		if _, ok := tool["outputSchema"]; ok {
			t.Errorf("2025-03-26 still lists outputSchema on %s", tool["name"])
		}
		if tool["inputSchema"] == nil || tool["annotations"] == nil {
			t.Errorf("2025-03-26 dropped inputSchema or annotations on %s", tool["name"])
		}
	}

	init := decode(t, sendMode(t, s, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`, CallerModeEvents))
	instructions := init["result"].(map[string]any)["instructions"].(string)
	if !strings.Contains(instructions, "Simple Hack connection") || !strings.Contains(instructions, "hack_select_team") || strings.Contains(instructions, "create_site") {
		t.Fatalf("events instructions: %s", instructions)
	}
	web := decode(t, send(t, s, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`, nil, true))
	webText := web["result"].(map[string]any)["instructions"].(string)
	if !strings.Contains(webText, "create_site") || strings.Contains(webText, "hack_create_event") {
		t.Fatal("website instructions picked up the event inventory")
	}

	if msg := rpcMessage(t, sendMode(t, s, toolCall("list_sites", map[string]any{}), CallerModeEvents)); !strings.Contains(msg, "unknown tool") {
		t.Fatalf("events caller list_sites: %s", msg)
	}
	if msg := rpcMessage(t, sendMode(t, s, toolCall("create_site", map[string]any{}), CallerModeEvents)); !strings.Contains(msg, "unknown tool") {
		t.Fatalf("events caller create_site: %s", msg)
	}
	if msg := rpcMessage(t, send(t, s, toolCall("hack_list_events", map[string]any{}), nil, true)); !strings.Contains(msg, "unknown tool") {
		t.Fatalf("website caller hack_list_events: %s", msg)
	}
}

func TestHackToolDispatch(t *testing.T) {
	const eventBody = `{"event":{"slug":"spring","title":"Spring","tagline":"","about":"","rules":"","prizes":"","coc_text":"","stage":"draft","time_zone":"UTC","starts_at":"2026-10-01T00:00:00Z","ends_at":null,"team_size_max":4,"url":"https://simple-hack.app/e/spring","taken_down":false,"stages_offered":["draft","open"]},"role":"organiser","me":{"team":{"code":"SECRET-TEAM"}},"organiser":{"organiser_name":"Ada","organisation":"","contact_email":"ada@example.com","purpose":"build","expected_participants":10,"join_code":"abc","join_url":"https://simple-hack.app/join/abc","judge_code":"def","judge_url":"https://simple-hack.app/judge/def","counts":{"participants":1,"teams":0,"judges":0,"on_no_team":1}}}`
	const rubricBody = `{"criteria":[{"position":1,"name":"Idea","description":"","weight":60,"max_points":10},{"position":2,"name":"Build","description":"working software","weight":40,"max_points":5}]}`
	const listBody = `[{"slug":"spring","title":"Spring","stage":"draft","role":"organiser","url":"https://simple-hack.app/e/spring","manage_url":"https://simple-hack.app/e/spring/manage","starts_at":"2026-10-01T00:00:00Z","ends_at":null,"taken_down":false,"time_zone":"UTC"}]`

	up := &scriptUpstream{ans: map[string]func(http.ResponseWriter, *http.Request){}}
	s := newTestServer(up)
	byName := map[string]Tool{}
	for _, tool := range HackTools() {
		if _, existing := hackOutputSchemas()[tool.Name]; existing {
			byName[tool.Name] = tool
		}
	}
	seen := map[string]map[string]bool{}
	call := func(name string, args map[string]any) (string, map[string]any, bool, string) {
		t.Helper()
		rec := sendMode(t, s, toolCall(name, args), CallerModeEvents)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
		text, structured, isErr := resultOf(t, rec)
		return text, structured, isErr, rec.Body.String()
	}
	check := func(name string, args map[string]any) map[string]any {
		t.Helper()
		text, structured, isErr, raw := call(name, args)
		if isErr || structured == nil {
			t.Fatalf("%s: isErr=%v text=%s", name, isErr, text)
		}
		if strings.Contains(raw, "SECRET-TEAM") || strings.Contains(raw, `"me"`) {
			t.Fatalf("%s leaked the team invite: %s", name, raw)
		}
		if seen[name] == nil {
			seen[name] = map[string]bool{}
		}
		if err := ValidateOutput(byName[name].OutputSchema, structured, seen[name]); err != nil {
			t.Fatalf("%s: %v\n%s", name, err, raw)
		}
		return structured
	}

	up.ans["GET /v1/hack/events"] = jsonReply(200, `[]`)
	text, structured, isErr, _ := call("hack_list_events", map[string]any{})
	if isErr || structured["count"].(float64) != 0 || !strings.Contains(text, "hack_create_event") {
		t.Fatalf("empty list: %v %s", isErr, text)
	}
	if err := ValidateOutput(byName["hack_list_events"].OutputSchema, structured, nil); err != nil {
		t.Fatal(err)
	}

	up.ans["GET /v1/hack/events"] = jsonReply(200, listBody)
	check("hack_list_events", map[string]any{})

	up.ans["GET /v1/hack/names/spring"] = jsonReply(200, `{"address":"https://simple-hack.app/e/spring","available":true}`)
	name := check("hack_check_event_name", map[string]any{"slug": "Spring"})
	if name["available"] != true || up.last().path != "/v1/hack/names/spring" {
		t.Fatalf("name check: %v %s", name, up.last().path)
	}
	up.ans["GET /v1/hack/names/spring"] = jsonReply(200, `{"address":"https://simple-hack.app/e/spring","available":false,"code":"name_taken","error":"that name is already in use"}`)
	taken := check("hack_check_event_name", map[string]any{"slug": "spring"})
	if taken["available"] != false || taken["code"] != "name_taken" {
		t.Fatalf("taken name returned as an error or dropped the code: %v", taken)
	}

	up.ans["POST /v1/hack/events"] = jsonReply(201, eventBody)
	before := up.len()
	text, _, isErr, _ = call("hack_create_event", map[string]any{
		"slug": "spring", "title": "Spring", "organiser_name": "Ada", "contact_email": "ada@example.com",
		"purpose": "build", "expected_participants": 1.5, "starts_at": "2026-10-01",
	})
	if !isErr || up.len() != before || !strings.Contains(text, "whole number") {
		t.Fatalf("fraction accepted or forwarded: %v %s (%d calls)", isErr, text, up.len()-before)
	}
	created := check("hack_create_event", map[string]any{
		"slug": "Spring", "title": "Spring", "organiser_name": "Ada", "organisation": "",
		"contact_email": "ada@example.com", "purpose": "build", "expected_participants": 12,
		"starts_at": "2026-10-01", "ends_at": "", "time_zone": "UTC",
	})
	if !strings.Contains(created["organiser"].(map[string]any)["join_url"].(string), "/join/") {
		t.Fatalf("create dropped the join link: %v", created["organiser"])
	}
	sent := map[string]any{}
	if err := json.Unmarshal([]byte(up.last().body), &sent); err != nil {
		t.Fatal(err)
	}
	if _, ok := sent["organisation"]; ok {
		t.Fatalf("empty organisation was sent: %s", up.last().body)
	}
	if _, ok := sent["ends_at"]; ok || sent["time_zone"] != "UTC" || sent["slug"] != "spring" || up.last().method != "POST" || up.last().key != "user-key" {
		t.Fatalf("create body: %s %s", up.last().method, up.last().body)
	}
	up.ans["POST /v1/hack/events"] = jsonReply(400, `{"error":"use 3 to 39 lowercase letters, numbers or hyphens","code":"invalid_name"}`)
	text, structured, isErr, raw := call("hack_create_event", map[string]any{
		"slug": "ab", "title": "Spring", "organiser_name": "Ada", "contact_email": "ada@example.com",
		"purpose": "build", "expected_participants": 12, "starts_at": "2026-10-01",
	})
	if !isErr || structured != nil || strings.Contains(raw, "1 to 63") || !strings.Contains(text, "3 to 39") {
		t.Fatalf("invalid_name hint: %s", text)
	}

	up.ans["GET /v1/hack/events/spring"] = jsonReply(200, eventBody)
	got := check("hack_get_event", map[string]any{"slug": "spring"})
	if !strings.Contains(got["organiser"].(map[string]any)["judge_url"].(string), "/judge/") || up.last().method != "GET" {
		t.Fatalf("get: %v via %s", got["organiser"], up.last().method)
	}
	participant := strings.Replace(eventBody, `"role":"organiser"`, `"role":"participant"`, 1)
	participant = participant[:strings.Index(participant, `,"organiser":`)] + "}"
	up.ans["GET /v1/hack/events/spring"] = jsonReply(200, participant)
	part, structured, isErr, raw := call("hack_get_event", map[string]any{"slug": "spring"})
	if isErr || structured["role"] != "participant" {
		t.Fatalf("participant get: %v %s", isErr, part)
	}
	if _, ok := structured["organiser"]; ok || strings.Contains(raw, "SECRET-TEAM") || strings.Contains(raw, "/join/") {
		t.Fatalf("participant view included organiser links: %s", raw)
	}
	if err := ValidateOutput(byName["hack_get_event"].OutputSchema, structured, nil); err != nil {
		t.Fatal(err)
	}
	up.ans["GET /v1/hack/events/spring"] = jsonReply(200, eventBody)

	up.ans["PATCH /v1/hack/events/spring"] = jsonReply(200, eventBody)
	before = up.len()
	text, _, isErr, _ = call("hack_update_event", map[string]any{"slug": "spring"})
	if !isErr || up.len() != before {
		t.Fatalf("empty update forwarded: %v %s", isErr, text)
	}
	check("hack_update_event", map[string]any{"slug": "spring", "title": "Renamed", "ends_at": "", "team_size_max": 3})
	sent = map[string]any{}
	if err := json.Unmarshal([]byte(up.last().body), &sent); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 3 || sent["title"] != "Renamed" || sent["ends_at"] != "" || sent["team_size_max"].(float64) != 3 || up.last().method != "PATCH" {
		t.Fatalf("update sent extra fields: %s", up.last().body)
	}
	check("hack_update_event", map[string]any{"slug": "spring", "submission_deadline": "2026-10-02", "entry_required": []any{"title", "description"}, "gallery_open": true})
	sent = map[string]any{}
	if err := json.Unmarshal([]byte(up.last().body), &sent); err != nil {
		t.Fatal(err)
	}
	if sent["submission_deadline"] != "2026-10-02" || sent["gallery_open"] != true || len(sent["entry_required"].([]any)) != 2 {
		t.Fatalf("update omitted deadline, entry fields or gallery setting: %s", up.last().body)
	}

	up.ans["POST /v1/hack/events/spring/stage"] = jsonReply(200, eventBody)
	check("hack_set_event_stage", map[string]any{"slug": "spring", "stage": "open"})
	if up.last().body != `{"stage":"open"}` {
		t.Fatalf("stage body: %s", up.last().body)
	}

	up.ans["GET /v1/hack/events/spring/rubric"] = jsonReply(200, rubricBody)
	check("hack_get_rubric", map[string]any{"slug": "spring"})
	up.ans["PUT /v1/hack/events/spring/rubric"] = jsonReply(200, rubricBody)
	before = up.len()
	text, _, isErr, _ = call("hack_set_rubric", map[string]any{"slug": "spring", "criteria": []any{map[string]any{"name": "Idea", "weight": 100, "max_points": 10, "id": "x"}}})
	if !isErr || up.len() != before || !strings.Contains(text, "unexpected field") {
		t.Fatalf("rubric accepted an unknown criterion field: %v %s", isErr, text)
	}
	check("hack_set_rubric", map[string]any{"slug": "spring", "criteria": []any{
		map[string]any{"name": "Idea", "weight": 60, "max_points": 10},
		map[string]any{"name": "Build", "description": "working software", "weight": 40, "max_points": 5},
	}})
	sent = map[string]any{}
	if err := json.Unmarshal([]byte(up.last().body), &sent); err != nil {
		t.Fatal(err)
	}
	criteria := sent["criteria"].([]any)
	if len(sent) != 1 || len(criteria) != 2 || up.last().method != "PUT" {
		t.Fatalf("rubric body: %s", up.last().body)
	}

	csvBody := "team,judge,criterion,points,max_points,comment\n"
	up.ans["GET /v1/hack/events/spring/export/scores.csv"] = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", `attachment; filename="spring-scores.csv"`)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(csvBody))
	}
	scores := check("hack_export_scores", map[string]any{"slug": "spring"})
	if scores["filename"] != "spring-scores.csv" || scores["csv"] != csvBody {
		t.Fatalf("scores: %v", scores)
	}
	up.ans["GET /v1/hack/events/spring/export/results.csv"] = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", `attachment; filename="spring-results.csv"`)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(strings.Repeat("x", (200<<10)+8)))
	}
	results := check("hack_export_results", map[string]any{"slug": "spring"})
	if len(results["csv"].(string)) != (200<<10)+8 || results["filename"] != "spring-results.csv" {
		t.Fatalf("complete results: len=%d name=%v", len(results["csv"].(string)), results["filename"])
	}

	up.ans["GET /v1/hack/events/missing"] = jsonReply(404, `{"error":"event not found","code":"event_not_found"}`)
	text, structured, isErr, raw = call("hack_get_event", map[string]any{"slug": "missing"})
	if !isErr || structured != nil || !strings.Contains(text, "event_not_found") {
		t.Fatalf("404 was not a tool error: %v %s", isErr, raw)
	}
	up.ans["GET /v1/hack/events"] = jsonReply(403, `{"error":"team key","code":"team_key_scope"}`)
	text, structured, isErr, raw = call("hack_list_events", map[string]any{})
	if !isErr || structured != nil || !strings.Contains(text, "team_key_scope") || !strings.Contains(text, "Manage my events") {
		t.Fatalf("403 was not a tool error: %v %s", isErr, text)
	}

	for name, tool := range byName {
		if seen[name] == nil {
			t.Errorf("%s never returned a successful result", name)
			continue
		}
		var unseen []string
		for _, path := range SchemaPropertyPaths(tool.OutputSchema) {
			if !seen[name][path] {
				unseen = append(unseen, path)
			}
		}
		if len(unseen) > 0 {
			t.Errorf("%s schema describes fields the tool did not return: %s", name, strings.Join(unseen, ", "))
		}
	}
}
