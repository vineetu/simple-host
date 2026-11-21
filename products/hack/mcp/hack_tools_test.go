package hackmcp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/vsriram/simple-host/host/mcp"
)

func sendMode(t *testing.T, s *mcp.Server, body, mode string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.RemoteAddr = "203.0.113.7:5555"
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(mcp.WithCaller(req.Context(), mcp.Caller{APIKey: "user-key", Mode: mode}))
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

// The one tool inventory test: every Hack tool is hack_-prefixed, never
// collides with a website tool, has valid schemas and all three hints, and
// is offered only on a Hack (events) connection.
func TestHackToolInventory(t *testing.T) {
	website := map[string]bool{}
	for _, tool := range mcp.Tools() {
		website[tool.Name] = true
	}
	hack := Tools()
	if len(hack) == 0 {
		t.Fatal("no hack tools")
	}
	seen := map[string]bool{}
	for _, tool := range hack {
		if seen[tool.Name] {
			t.Errorf("duplicate %s", tool.Name)
		}
		seen[tool.Name] = true
		if !strings.HasPrefix(tool.Name, "hack_") {
			t.Errorf("%s is not prefixed", tool.Name)
		}
		if website[tool.Name] {
			t.Errorf("%s collides with a website tool", tool.Name)
		}
		if err := mcp.CheckOutputSchema(tool.OutputSchema); err != nil {
			t.Errorf("%s output: %v", tool.Name, err)
		}
		if err := mcp.CheckOutputSchema(tool.InputSchema); err != nil {
			t.Errorf("%s input: %v", tool.Name, err)
		}
	}
	s := newTestServer(&scriptUpstream{ans: map[string]func(http.ResponseWriter, *http.Request){}})
	for _, tool := range listedTools(t, send(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, nil, true)) {
		if strings.HasPrefix(tool["name"].(string), "hack_") {
			t.Errorf("website list advertises %s", tool["name"])
		}
	}
	hackListed := 0
	for _, tool := range listedTools(t, sendMode(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, mcp.CallerModeEvents)) {
		if strings.HasPrefix(tool["name"].(string), "hack_") {
			hackListed++
		}
	}
	if hackListed == 0 {
		t.Fatal("events connection lists no Hack tools")
	}
	rec := send(t, s, toolCall("hack_list_events", map[string]any{}), nil, true)
	if out := decode(t, rec); out["result"] != nil || !strings.Contains(rec.Body.String(), "unknown tool") {
		t.Fatalf("website caller ran hack_list_events: %s", rec.Body.String())
	}
}

// SIM-384: hack_create_event once failed for a client holding an older,
// closed output schema. Tool.MarshalJSON opens additionalProperties on the
// wire copy only; the authored schema and the input schema stay closed.
func TestWireOutputSchemaOpensOnlyOnTheWire(t *testing.T) {
	s := newTestServer(&scriptUpstream{ans: map[string]func(http.ResponseWriter, *http.Request){}})
	var wire, wireInput map[string]any
	for _, tool := range listedTools(t, sendMode(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, mcp.CallerModeEvents)) {
		if tool["name"] == "hack_create_event" {
			wire, _ = tool["outputSchema"].(map[string]any)
			wireInput, _ = tool["inputSchema"].(map[string]any)
		}
	}
	if wire == nil || wireInput == nil {
		t.Fatal("hack_create_event not listed with schemas")
	}
	authored := toolNamed(t, "hack_create_event").OutputSchema
	authoredProps, _ := authored["properties"].(map[string]any)
	props, _ := wire["properties"].(map[string]any)
	if wire["additionalProperties"] != true || authored["additionalProperties"] != false || wireInput["additionalProperties"] != false {
		t.Fatalf("root: wire=%v authored=%v input=%v", wire["additionalProperties"], authored["additionalProperties"], wireInput["additionalProperties"])
	}
	for _, nested := range []string{"next_step", "organiser"} {
		sub, _ := props[nested].(map[string]any)
		authoredSub, _ := authoredProps[nested].(map[string]any)
		if sub == nil || sub["additionalProperties"] != true || authoredSub == nil || authoredSub["additionalProperties"] != false {
			t.Fatalf("%s: wire=%v authored=%v", nested, sub, authoredSub)
		}
	}
}

// The base event tools forward to REST with the caller's key, refuse bad
// input without forwarding it, keep a confirmation they were given, and a
// participant's view never carries organiser links or the team code.
func TestHackToolDispatch(t *testing.T) {
	const eventBody = `{"event":{"slug":"spring","title":"Spring","stage":"draft","time_zone":"UTC","starts_at":"2026-10-01T00:00:00Z","ends_at":null,"judging_ends_at":"2026-10-05T18:00:00Z","team_size_max":4,"url":"https://simple-hack.app/e/spring","taken_down":false,"stages_offered":["draft","open"]},"can":{"manage":true,"judge":true,"take_part":false},"role":"organiser","me":{"team":{"code":"SECRET-TEAM"}},"organiser":{"organiser_name":"Ada","contact_email":"ada@example.com","join_code":"abc","join_url":"https://simple-hack.app/join/abc","judge_code":"def","judge_url":"https://simple-hack.app/judge/def","counts":{"participants":1,"teams":0,"judges":0,"on_no_team":1}},"checklist":` + hackChecklistFixtureJSON + `}`
	const rubricBody = `{"criteria":[{"position":1,"name":"Idea","description":"","weight":60,"max_points":10},{"position":2,"name":"Build","description":"working software","weight":40,"max_points":5}]}`

	up := &scriptUpstream{ans: map[string]func(http.ResponseWriter, *http.Request){}}
	s := newTestServer(up)
	call := func(name string, args map[string]any) (string, map[string]any, bool, string) {
		t.Helper()
		rec := sendMode(t, s, toolCall(name, args), mcp.CallerModeEvents)
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
		return structured
	}
	lastBody := func() map[string]any {
		t.Helper()
		sent := map[string]any{}
		if err := json.Unmarshal([]byte(up.last().body), &sent); err != nil {
			t.Fatal(err)
		}
		return sent
	}
	refusedLocally := func(name string, args map[string]any) {
		t.Helper()
		before := up.len()
		if text, _, isErr, _ := call(name, args); !isErr || up.len() != before {
			t.Fatalf("%s: bad input accepted or forwarded: %v %s", name, isErr, text)
		}
	}

	up.ans["POST /v1/hack/events"] = jsonReply(201, eventBody)
	refusedLocally("hack_create_event", map[string]any{
		"slug": "spring", "title": "Spring", "organiser_name": "Ada", "contact_email": "ada@example.com",
		"purpose": "build", "expected_participants": 1.5, "starts_at": "2026-10-01",
	})
	created := check("hack_create_event", map[string]any{
		"slug": "Spring", "title": "Spring", "organiser_name": "Ada", "organisation": "",
		"contact_email": "ada@example.com", "purpose": "build", "expected_participants": 12,
		"starts_at": "2026-10-01", "ends_at": "", "time_zone": "UTC",
	})
	if !strings.Contains(created["organiser"].(map[string]any)["join_url"].(string), "/join/") {
		t.Fatalf("create dropped the join link: %v", created["organiser"])
	}
	// The event output once omitted judging_ends_at, unlike the REST event.
	if created["judging_ends_at"] != "2026-10-05T18:00:00Z" {
		t.Fatalf("hack_create_event output missing judging_ends_at: %v", created["judging_ends_at"])
	}
	sent := lastBody()
	if _, ok := sent["organisation"]; ok {
		t.Fatalf("empty organisation was sent: %s", up.last().body)
	}
	if _, ok := sent["ends_at"]; ok || sent["slug"] != "spring" || up.last().method != "POST" || up.last().key != "user-key" {
		t.Fatalf("create forwarded: %s %s key=%s", up.last().method, up.last().body, up.last().key)
	}

	participant := strings.Replace(eventBody, `"role":"organiser"`, `"role":"participant"`, 1)
	participant = participant[:strings.Index(participant, `,"organiser":`)] + "}"
	up.ans["GET /v1/hack/events/spring"] = jsonReply(200, participant)
	_, structured, isErr, raw := call("hack_get_event", map[string]any{"slug": "spring"})
	if isErr || structured["role"] != "participant" {
		t.Fatalf("participant get: %v %s", isErr, raw)
	}
	if _, ok := structured["organiser"]; ok || strings.Contains(raw, "SECRET-TEAM") || strings.Contains(raw, "/join/") {
		t.Fatalf("participant view included organiser links: %s", raw)
	}

	up.ans["PATCH /v1/hack/events/spring"] = jsonReply(200, eventBody)
	refusedLocally("hack_update_event", map[string]any{"slug": "spring"})
	check("hack_update_event", map[string]any{"slug": "spring", "title": "Renamed", "ends_at": "", "team_size_max": 3})
	if sent := lastBody(); len(sent) != 3 || sent["title"] != "Renamed" || sent["ends_at"] != "" || up.last().method != "PATCH" {
		t.Fatalf("update sent: %s", up.last().body)
	}

	up.ans["PUT /v1/hack/events/spring/rubric"] = jsonReply(200, rubricBody)
	refusedLocally("hack_set_rubric", map[string]any{"slug": "spring", "criteria": []any{map[string]any{"name": "Idea", "weight": 100, "max_points": 10, "unknown": "x"}}})
	check("hack_set_rubric", map[string]any{"slug": "spring", "confirm_score_deletion": true, "criteria": []any{
		map[string]any{"id": "existing-criterion", "name": "Idea", "weight": 60, "max_points": 10},
		map[string]any{"name": "Build", "description": "working software", "weight": 40, "max_points": 5},
	}})
	sent = lastBody()
	criteria := sent["criteria"].([]any)
	if sent["confirm_score_deletion"] != true || len(criteria) != 2 || criteria[0].(map[string]any)["id"] != "existing-criterion" {
		t.Fatalf("rubric identity or confirmation lost: %s", up.last().body)
	}

	// A large export comes back whole.
	up.ans["GET /v1/hack/events/spring/export/results.csv"] = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", `attachment; filename="spring-results.csv"`)
		_, _ = w.Write([]byte(strings.Repeat("x", (200<<10)+8)))
	}
	results := check("hack_export_results", map[string]any{"slug": "spring"})
	if len(results["csv"].(string)) != (200<<10)+8 || results["filename"] != "spring-results.csv" {
		t.Fatalf("results export: len=%d name=%v", len(results["csv"].(string)), results["filename"])
	}

	up.ans["GET /v1/hack/events/missing"] = jsonReply(404, `{"error":"event not found","code":"event_not_found"}`)
	text, structured, isErr, _ := call("hack_get_event", map[string]any{"slug": "missing"})
	if !isErr || structured != nil || !strings.Contains(text, "event_not_found") {
		t.Fatalf("404 was not a tool error: %v %s", isErr, text)
	}
}

// A website tool Simple Hack shares with Simple Host keeps Host's hints, and
// delete_site still asks for confirm_name.
func TestHackSharedWebsiteToolsMatchHost(t *testing.T) {
	s := newTestServer(nil)
	site := listedTools(t, rpcAs(t, s, mcp.Caller{APIKey: "user-key", Mode: mcp.CallerModeEvents, GrantID: "grant"}, "2025-06-18", "tools/list", nil, false))
	hostByName := map[string]mcp.Tool{}
	for _, tool := range mcp.Tools() {
		hostByName[tool.Name] = tool
	}
	deleteSite := ""
	for _, tool := range site {
		name := tool["name"].(string)
		if name == "delete_site" {
			deleteSite, _ = tool["description"].(string)
		}
		host, ok := hostByName[name]
		if !ok {
			continue
		}
		annotations, _ := tool["annotations"].(map[string]any)
		for _, key := range []string{"readOnlyHint", "destructiveHint", "openWorldHint", "idempotentHint"} {
			if annotations[key] != host.Annotations[key] {
				t.Errorf("%s %s = %v on Hack, %v on Host", name, key, annotations[key], host.Annotations[key])
			}
		}
	}
	if !strings.Contains(deleteSite, "confirm_name") {
		t.Errorf("Hack delete_site does not ask for confirm_name: %q", deleteSite)
	}
}

func TestHackPatchBody(t *testing.T) {
	for _, args := range []map[string]any{
		{"team_finding": false},
		{"submissions_public": true},
		{"overall_prizes": []any{"", "$200"}},
		{"overall_prizes": []any{}},
	} {
		body, err := hackPatchBody(args)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		for key, value := range args {
			got, _ := json.Marshal(body[key])
			want, _ := json.Marshal(value)
			if string(got) != string(want) {
				t.Errorf("%s = %s, want %s", key, got, want)
			}
		}
	}
	for _, args := range []map[string]any{{"team_finding": "false"}, {"submissions_public": "yes"}} {
		if _, err := hackPatchBody(args); err == nil {
			t.Errorf("accepted non-boolean %v", args)
		}
	}
}
