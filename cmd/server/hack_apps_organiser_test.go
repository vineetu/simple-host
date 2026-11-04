package main

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// hackOrganiserAppsFixture builds one event with a pending application
// whose display name and answer carry hostile text, a team with a hostile
// name, and a judge who has accepted the invite -- the same payloads used
// elsewhere in this suite to prove escaping, now exercised through the
// organiser's overview/people/judging views.
type hackOrganiserAppsFixture struct {
	app                          *teamSiteApp
	organiserAccess, judgeAccess string
	organiserKey                 string
	event, team, applicantUserID string
}

func newHackOrganiserAppsFixture(t *testing.T) hackOrganiserAppsFixture {
	t.Helper()
	a := newTeamSiteApp(t)
	organiser, participant, judge, applicant := a.newPerson(t, "orgapp-org"), a.newPerson(t, "orgapp-part"), a.newPerson(t, "orgapp-judge"), a.newPerson(t, "orgapp-applicant")
	event := a.makeEvent(t, organiser) // starts at stage "open"

	// This fixture's event is new, but the Apply-without-a-join-link flow
	// (hack_apply.go) gates on `applications_open AND directory_listed` --
	// a new event starts unlisted (plan section 2), so both need setting
	// explicitly before /apply will accept anything.
	if r := a.api(t, "PATCH", "/v1/hack/events/"+event, map[string]any{"applications_open": true}, organiser.key); r.status != 200 {
		t.Fatalf("open applications: %d %s", r.status, r.body)
	}
	if r := a.api(t, "PATCH", "/v1/hack/events/"+event+"/directory", map[string]any{"listed": true}, organiser.key); r.status != 200 {
		t.Fatalf("list in directory: %d %s", r.status, r.body)
	}

	// A sign-up question, so the applicant's answer has somewhere to go.
	if r := a.api(t, "PUT", "/v1/hack/events/"+event+"/registration", map[string]any{
		"questions":         []map[string]any{{"id": "q1", "prompt": "Why join?", "required": false}},
		"approval_required": false,
	}, organiser.key); r.status != 200 {
		t.Fatalf("set registration: %d %s", r.status, r.body)
	}

	a.join(t, event, participant, organiser)
	team, _ := a.startTeam(t, event, "<img src=x onerror=alert(1)> Team", participant)

	// Apply without the join link: always stays pending, regardless of
	// approval_required (hack_organiser_tools.go's own hack_decide_
	// application description), so this is the fixture's one deliberately
	// un-decided application -- and its display_name/answer both carry
	// hostile markup the People view's applicationCard() must escape.
	applyBody := map[string]any{
		"accept_coc":   true,
		"display_name": "<script>bad()</script> Applicant",
		"answers":      map[string]any{"q1": "<img src=x onerror=alert(1)> my answer"},
	}
	wantTS(t, "apply", a.api(t, "POST", "/v1/hack/events/"+event+"/apply", applyBody, applicant.key), 200, "")
	applicantUserID := a.uid(t, applicant)

	ev := a.api(t, "GET", "/v1/hack/events/"+event, nil, organiser.key).json(t)
	judgeCode := ev["organiser"].(map[string]any)["judge_code"].(string)
	wantTS(t, "judge join", a.api(t, "POST", "/v1/hack/judge/"+judgeCode, map[string]any{"accept_coc": true, "display_name": "Judge One"}, judge.key), 200, "")

	for _, stage := range []string{"building", "closed"} {
		wantTS(t, "stage "+stage, a.api(t, "POST", "/v1/hack/events/"+event+"/stage", map[string]string{"stage": stage}, organiser.key), 200, "")
	}

	client := a.registerClient(t, testRedirect)
	organiserAccess := a.connect(t, organiser, client, testRedirect)["access_token"].(string)
	judgeAccess := a.connect(t, judge, client, testRedirect)["access_token"].(string)
	// Declare MCP Apps UI support the way a real host does, once, at
	// initialize -- same as the judge fixture.
	for _, access := range []string{organiserAccess, judgeAccess} {
		initResp := a.rpc(t, access, "initialize", map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{"extensions": map[string]any{"io.modelcontextprotocol/ui": map[string]any{}}},
			"clientInfo":      map[string]any{"name": "Fixture Host", "version": "1.0"},
		})
		if initResp.status != http.StatusOK {
			t.Fatalf("initialize: %d %s", initResp.status, initResp.body)
		}
	}
	return hackOrganiserAppsFixture{a, organiserAccess, judgeAccess, organiser.key, event, team, applicantUserID}
}

// TestHackOrganiserAppShowToolAndViews is the lightweight (non-browser)
// real-stack proof that hack_show_organiser actually opens with this
// organiser's real data once MCP_APP_HACK_ORGANISER is on, and stays
// absent (and unusable) while it is off -- the same two states the
// browser harness below exercises visually. It also proves a judge
// connection (role "judge", not "organiser") is refused.
func TestHackOrganiserAppShowToolAndViews(t *testing.T) {
	f := newHackOrganiserAppsFixture(t)
	a := f.app

	listed := a.rpc(t, f.organiserAccess, "tools/list", map[string]any{}).json(t)["result"].(map[string]any)["tools"].([]any)
	for _, raw := range listed {
		if raw.(map[string]any)["name"] == "hack_show_organiser" {
			t.Fatal("hack_show_organiser listed with its flag off")
		}
	}

	t.Setenv("MCP_APP_HACK_ORGANISER", "on")
	listed = a.rpc(t, f.organiserAccess, "tools/list", map[string]any{}).json(t)["result"].(map[string]any)["tools"].([]any)
	found := false
	for _, raw := range listed {
		if raw.(map[string]any)["name"] == "hack_show_organiser" {
			found = true
		}
	}
	if !found {
		t.Fatal("hack_show_organiser not listed once flagged on for a UI-declared organiser connection")
	}

	for _, view := range []string{"overview", "people", "judging"} {
		text, out, bad := toolResultOf(t, a.rpc(t, f.organiserAccess, "tools/call", map[string]any{"name": "hack_show_organiser", "arguments": map[string]any{"view": view, "slug": f.event}}))
		if bad {
			t.Fatalf("hack_show_organiser %s: %s", view, text)
		}
		if out["view"] != view || out["slug"] != f.event {
			t.Fatalf("hack_show_organiser %s result: %#v", view, out)
		}
		ev, ok := out["event"].(map[string]any)
		if !ok || ev["stage"] != "closed" {
			t.Fatalf("hack_show_organiser %s event: %#v", view, out["event"])
		}
		org, ok := out["organiser"].(map[string]any)
		if !ok || org["counts"] == nil {
			t.Fatalf("hack_show_organiser %s organiser counts: %#v", view, out["organiser"])
		}
	}

	// A judge (not organiser) connection must be refused.
	_, _, bad := toolResultOf(t, a.rpc(t, f.judgeAccess, "tools/call", map[string]any{"name": "hack_show_organiser", "arguments": map[string]any{"view": "overview", "slug": f.event}}))
	if !bad {
		t.Fatal("hack_show_organiser opened for a judge connection")
	}

	peopleText, peopleOut, bad := toolResultOf(t, a.rpc(t, f.organiserAccess, "tools/call", map[string]any{"name": "hack_get_applications", "arguments": map[string]any{"slug": f.event}}))
	if bad {
		t.Fatalf("hack_get_applications: %s", peopleText)
	}
	// hack_get_applications returns both pending and already-decided rows
	// (the join-link participant shows up "approved" too); the People
	// view's own applicationCard() only ever renders the pending ones, so
	// this looks for the fixture's one deliberately pending application
	// among whatever else is there.
	apps := peopleOut["response"].([]any)
	var pending map[string]any
	for _, raw := range apps {
		row := raw.(map[string]any)
		if row["status"] == "pending" {
			pending = row
		}
	}
	if pending == nil {
		t.Fatalf("no pending application among %d rows: %#v", len(apps), apps)
	}
	if pending["display_name"] != "<script>bad()</script> Applicant" {
		t.Fatalf("application display_name not returned verbatim (escaping is the view's job, not the tool's): %#v", pending["display_name"])
	}
	answers, _ := pending["answers"].([]any)
	if len(answers) != 1 || answers[0].(map[string]any)["answer"] != "<img src=x onerror=alert(1)> my answer" {
		t.Fatalf("application answer not returned verbatim: %#v", answers)
	}

	resource := a.rpc(t, f.organiserAccess, "resources/read", map[string]any{"uri": "ui://simple-hack/organiser"})
	if resource.status != http.StatusOK {
		t.Fatalf("resources/read: %d %s", resource.status, resource.body)
	}
	page := resource.json(t)["result"].(map[string]any)["contents"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(page, "script-src 'nonce-") {
		t.Fatal("organiser resource missing nonce CSP")
	}
	if strings.Contains(page, f.organiserAccess) {
		t.Fatal("OAuth access token leaked into the rendered organiser resource")
	}
}

// TestHackOrganiserAppShowToolRefusesJoinAndJudgeCodes
// (hackShowOrganiserViewOnly, hack_apps_organiser.go) is the real-stack
// proof that the event's own join_url/judge_url never land in the show
// tool's model-visible content, only in _meta -- exercised over the real
// HTTP + JSON-RPC stack rather than only the in-process unit test in
// internal/mcp.
func TestHackOrganiserAppShowToolRefusesJoinAndJudgeCodes(t *testing.T) {
	f := newHackOrganiserAppsFixture(t)
	a := f.app
	t.Setenv("MCP_APP_HACK_ORGANISER", "on")

	call := a.rpc(t, f.organiserAccess, "tools/call", map[string]any{"name": "hack_show_organiser", "arguments": map[string]any{"view": "overview", "slug": f.event}})
	if call.status != http.StatusOK {
		t.Fatalf("tools/call: %d %s", call.status, call.body)
	}
	result := call.json(t)["result"].(map[string]any)
	full, _ := json.Marshal(result["content"])
	structured, _ := json.Marshal(result["structuredContent"])
	ev := a.api(t, "GET", "/v1/hack/events/"+f.event, nil, f.organiserKey).json(t)
	joinCode := ev["organiser"].(map[string]any)["join_code"].(string)
	judgeCode := ev["organiser"].(map[string]any)["judge_code"].(string)
	for _, code := range []string{joinCode, judgeCode} {
		if strings.Contains(string(full), code) {
			t.Fatalf("join/judge code leaked into model-visible content: %s", full)
		}
		if strings.Contains(string(structured), code) {
			t.Fatalf("join/judge code leaked into model-visible structuredContent: %s", structured)
		}
	}
	meta, _ := json.Marshal(result["_meta"])
	if !strings.Contains(string(meta), joinCode) || !strings.Contains(string(meta), judgeCode) {
		t.Fatalf("join/judge codes missing from the view-only _meta channel: %s", meta)
	}
}

// Optional local fixture for the browser bridge harness
// (scripts/hack-organiser-app-browser.mjs), mirroring
// TestServeHackJudgeAppBrowser.
func TestServeHackOrganiserAppBrowser(t *testing.T) {
	path := os.Getenv("HACK_ORGANISER_APP_FIXTURE")
	if path == "" {
		t.Skip("HACK_ORGANISER_APP_FIXTURE unset")
	}
	t.Setenv("MCP_APP_HACK_ORGANISER", "on")
	f := newHackOrganiserAppsFixture(t)
	stop := make(chan struct{})
	f.app.mux.HandleFunc("POST /_fixture/stop", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-stop:
		default:
			close(stop)
		}
		w.WriteHeader(204)
	})
	data, _ := json.Marshal(map[string]string{
		"url": f.app.srv.URL, "access": f.organiserAccess, "event": f.event, "team": f.team, "applicantUserID": f.applicantUserID,
	})
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	select {
	case <-stop:
	case <-time.After(15 * time.Minute):
		t.Fatal("browser fixture timed out")
	}
}
