package handler

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// hackJudgeAppsFixture builds one event, closed (so scoring_open is true),
// with one team whose entry carries hostile text -- the same payloads
// used elsewhere in this suite to prove escaping -- and one judge who has
// accepted the invite and has that team in their queue.
type hackJudgeAppsFixture struct {
	app                       *teamSiteApp
	judgeAccess, inviteAccess string
	organiserKey              string
	event, team, judgeCode    string
}

func newHackJudgeAppsFixture(t *testing.T) hackJudgeAppsFixture {
	t.Helper()
	a := newTeamSiteApp(t)
	organiser, participant, judge, secondJudge := a.newPerson(t, "judgeapp-org"), a.newPerson(t, "judgeapp-part"), a.newPerson(t, "judgeapp-judge"), a.newPerson(t, "judgeapp-judge2")
	event := a.makeEvent(t, organiser) // starts at stage "open"
	a.join(t, event, participant, organiser)
	team, _ := a.startTeam(t, event, "<img src=x onerror=alert(1)> Team", participant)

	entryBody := map[string]any{
		"title":       "<script>bad()</script> Entry title",
		"tagline":     "<img src=x onerror=alert(1)> tagline",
		"description": "<script>bad()</script> description with \"quotes\" & <b>tags</b>",
	}
	wantTS(t, "entry", a.api(t, "PUT", "/v1/hack/events/"+event+"/entry", entryBody, participant.key), 200, "")

	ev := a.api(t, "GET", "/v1/hack/events/"+event, nil, organiser.key).json(t)
	judgeCode := ev["organiser"].(map[string]any)["judge_code"].(string)
	wantTS(t, "judge join", a.api(t, "POST", "/v1/hack/judge/"+judgeCode, map[string]any{"accept_coc": true, "display_name": "Judge One"}, judge.key), 200, "")
	// secondJudge never accepts: this fixture's judgeCode stays usable for
	// the invite view, which previews the invite before joining.

	for _, stage := range []string{"building", "closed"} {
		wantTS(t, "stage "+stage, a.api(t, "POST", "/v1/hack/events/"+event+"/stage", map[string]string{"stage": stage}, organiser.key), 200, "")
	}

	client := a.registerClient(t, testRedirect)
	judgeAccess := a.connect(t, judge, client, testRedirect)["access_token"].(string)
	inviteAccess := a.connect(t, secondJudge, client, testRedirect)["access_token"].(string)
	// Declare MCP Apps UI support the way a real host does, once, at
	// initialize -- clientUIMemory (internal/mcp/clientui.go) is what
	// filterHackShowTools / hackShowJudgeAppAllowed read for this
	// initialize-era connection.
	for _, access := range []string{judgeAccess, inviteAccess} {
		initResp := a.rpc(t, access, "initialize", map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{"extensions": map[string]any{"io.modelcontextprotocol/ui": map[string]any{}}},
			"clientInfo":      map[string]any{"name": "Fixture Host", "version": "1.0"},
		})
		if initResp.status != http.StatusOK {
			t.Fatalf("initialize: %d %s", initResp.status, initResp.body)
		}
	}
	return hackJudgeAppsFixture{a, judgeAccess, inviteAccess, organiser.key, event, team, judgeCode}
}

// TestHackJudgeAppShowToolAndQueue is the lightweight (non-browser) real-
// stack proof that hack_show_judge actually opens with this judge's real
// data once MCP_APP_HACK_JUDGE is on, and stays absent (and unusable)
// while it is off -- the same two states the browser harness below
// exercises visually.
func TestHackJudgeAppShowToolAndQueue(t *testing.T) {
	f := newHackJudgeAppsFixture(t)
	a := f.app

	listed := a.rpc(t, f.judgeAccess, "tools/list", map[string]any{}).json(t)["result"].(map[string]any)["tools"].([]any)
	for _, raw := range listed {
		if raw.(map[string]any)["name"] == "hack_show_judge" {
			t.Fatal("hack_show_judge listed with its flag off")
		}
	}

	t.Setenv("MCP_APP_HACK_JUDGE", "on")
	listed = a.rpc(t, f.judgeAccess, "tools/list", map[string]any{}).json(t)["result"].(map[string]any)["tools"].([]any)
	found := false
	for _, raw := range listed {
		if raw.(map[string]any)["name"] == "hack_show_judge" {
			found = true
		}
	}
	if !found {
		t.Fatal("hack_show_judge not listed once flagged on for a UI-declared judge connection")
	}

	text, out, bad := toolResultOf(t, a.rpc(t, f.judgeAccess, "tools/call", map[string]any{"name": "hack_show_judge", "arguments": map[string]any{"view": "queue", "slug": f.event}}))
	if bad {
		t.Fatalf("hack_show_judge queue: %s", text)
	}
	if out["view"] != "queue" || out["slug"] != f.event {
		t.Fatalf("hack_show_judge queue result: %#v", out)
	}
	ev, ok := out["event"].(map[string]any)
	if !ok || ev["scoring_open"] != true {
		t.Fatalf("hack_show_judge queue event: %#v", out["event"])
	}

	queueText, queueOut, bad := toolResultOf(t, a.rpc(t, f.judgeAccess, "tools/call", map[string]any{"name": "hack_get_judge_queue", "arguments": map[string]any{"slug": f.event}}))
	if bad {
		t.Fatalf("hack_get_judge_queue: %s", queueText)
	}
	queue := queueOut["queue"].([]any)
	if len(queue) != 1 {
		t.Fatalf("queue has %d teams, want 1: %#v", len(queue), queue)
	}
	row := queue[0].(map[string]any)
	if row["title"] != "<script>bad()</script> Entry title" {
		t.Fatalf("entry title not returned verbatim (escaping is the view's job, not the tool's): %#v", row["title"])
	}

	resource := a.rpc(t, f.judgeAccess, "resources/read", map[string]any{"uri": "ui://simple-hack/judge"})
	if resource.status != http.StatusOK {
		t.Fatalf("resources/read: %d %s", resource.status, resource.body)
	}
	page := resource.json(t)["result"].(map[string]any)["contents"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(page, "script-src 'nonce-") {
		t.Fatal("judge resource missing nonce CSP")
	}
	if strings.Contains(page, f.judgeAccess) {
		t.Fatal("OAuth access token leaked into the rendered judge resource")
	}
}

// TestHackJudgeAppShowToolReflectsLockMidSession is the server-side half of
// Codex's review (SIM-344): hack_show_judge must never cache the event --
// each call is a fresh REST read -- so a judge whose frame is left open
// across an organiser locking judging sees the lock on the next call, not
// the state from when the frame first opened. (The in-frame half -- that
// every view switch re-calls hack_show_judge rather than reusing the
// first response -- is hack-judge-view.js's refreshEvent(), exercised by
// the browser harness.)
func TestHackJudgeAppShowToolReflectsLockMidSession(t *testing.T) {
	f := newHackJudgeAppsFixture(t)
	a := f.app
	t.Setenv("MCP_APP_HACK_JUDGE", "on")

	_, before, bad := toolResultOf(t, a.rpc(t, f.judgeAccess, "tools/call", map[string]any{"name": "hack_show_judge", "arguments": map[string]any{"view": "score", "slug": f.event}}))
	if bad {
		t.Fatal("hack_show_judge before lock refused")
	}
	beforeEvent := before["event"].(map[string]any)
	if beforeEvent["judging_locked_at"] != nil {
		t.Fatalf("judging already locked before the test locked it: %#v", beforeEvent["judging_locked_at"])
	}

	wantTS(t, "lock judging", a.api(t, "POST", "/v1/hack/events/"+f.event+"/judging/lock", nil, f.organiserKey), 200, "")

	_, after, bad := toolResultOf(t, a.rpc(t, f.judgeAccess, "tools/call", map[string]any{"name": "hack_show_judge", "arguments": map[string]any{"view": "score", "slug": f.event}}))
	if bad {
		t.Fatal("hack_show_judge after lock refused")
	}
	afterEvent := after["event"].(map[string]any)
	if afterEvent["judging_locked_at"] == nil {
		t.Fatal("hack_show_judge still reports judging unlocked on the next call after the organiser locked it")
	}
}

// Optional local fixture for the browser bridge harness
// (scripts/hack-judge-app-browser.mjs), mirroring TestServeHackAppsBrowser.
// Its private file is read by the host process, never by the frame, and
// removed by the test cleanup.
func TestServeHackJudgeAppBrowser(t *testing.T) {
	path := os.Getenv("HACK_JUDGE_APP_FIXTURE")
	if path == "" {
		t.Skip("HACK_JUDGE_APP_FIXTURE unset")
	}
	t.Setenv("MCP_APP_HACK_JUDGE", "on")
	f := newHackJudgeAppsFixture(t)
	stop := make(chan struct{})
	f.app.mux.HandleFunc("POST /_fixture/stop", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-stop:
		default:
			close(stop)
		}
		w.WriteHeader(204)
	})
	data, _ := json.Marshal(map[string]string{"url": f.app.srv.URL, "access": f.judgeAccess, "inviteAccess": f.inviteAccess, "event": f.event, "team": f.team, "judgeCode": f.judgeCode})
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
