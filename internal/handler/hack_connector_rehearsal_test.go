package handler

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// A complete local event driven only through personal MCP connections. The
// fixture creates disposable accounts, event and files, then removes them.
func TestHackConnectorWholeEvent(t *testing.T) {
	a := newTeamSiteApp(t)
	org := a.newPerson(t, "mcpwholeorg")
	participant := a.newPerson(t, "mcpwholepart")
	judge := a.newPerson(t, "mcpwholejudge")
	client := a.registerClient(t, testRedirect)
	tokens := map[string]string{
		"org":         a.connect(t, org, client, testRedirect)["access_token"].(string),
		"participant": a.connect(t, participant, client, testRedirect)["access_token"].(string),
		"judge":       a.connect(t, judge, client, testRedirect)["access_token"].(string),
	}
	call := func(role, name string, args map[string]any) map[string]any {
		t.Helper()
		text, out, bad := toolResultOf(t, a.rpc(t, tokens[role], "tools/call", map[string]any{"name": name, "arguments": args}))
		if bad || out == nil {
			t.Fatalf("%s %s: %s", role, name, text)
		}
		return out
	}
	slug := uniqueSlug()
	t.Cleanup(func() { a.cleanupSlug(slug) })
	created := call("org", "hack_create_event", map[string]any{
		"slug": slug, "title": "Connector whole event", "organiser_name": "Org",
		"contact_email": "org@example.com", "purpose": "Exercise connector parity",
		"expected_participants": 3, "starts_at": "2026-10-01", "time_zone": "UTC",
	})
	links := created["organiser"].(map[string]any)
	call("org", "hack_set_event_stage", map[string]any{"slug": slug, "stage": "open"})
	call("participant", "hack_preview_join", map[string]any{"code": links["join_code"]})
	call("participant", "hack_join_event", map[string]any{"code": links["join_code"], "accept_coc": true, "display_name": "Pat"})
	team := call("participant", "hack_create_team", map[string]any{"slug": slug, "name": "Connector team"})
	teamSlug := team["slug"].(string)
	teams := call("participant", "hack_get_my_teams", map[string]any{})["teams"].([]any)
	if len(teams) != 1 {
		t.Fatalf("my teams: %v", teams)
	}
	teamID := teams[0].(map[string]any)["team_id"].(string)
	call("participant", "hack_select_team", map[string]any{"team_id": teamID})
	markReady(t, a.certDir, slug)
	call("participant", "create_site", map[string]any{"site": teamSlug, "files": map[string]any{"index.html": "<h1>Connector project</h1>"}})
	for _, roleAndTool := range [][2]string{{"participant", "hack_export_own_team_archive"}, {"org", "hack_export_projects_archive"}} {
		link := call(roleAndTool[0], roleAndTool[1], map[string]any{"slug": slug})["url"].(string)
		u, err := url.Parse(link)
		if err != nil {
			t.Fatal(err)
		}
		archive := a.api(t, http.MethodGet, u.RequestURI(), nil, "")
		if archive.status != 200 || len(archive.body) == 0 {
			t.Fatalf("%s archive link: %d", roleAndTool[1], archive.status)
		}
	}
	call("participant", "hack_update_entry", map[string]any{"slug": slug, "title": "Connector project", "description": "Built and published through MCP"})
	call("judge", "hack_preview_judge", map[string]any{"code": links["judge_code"]})
	call("judge", "hack_join_judge", map[string]any{"code": links["judge_code"], "accept_coc": true, "display_name": "Jules"})
	call("org", "hack_set_event_stage", map[string]any{"slug": slug, "stage": "judging"})
	queue := call("judge", "hack_get_judge_queue", map[string]any{"slug": slug})
	rows := queue["queue"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["team_id"] != teamID {
		t.Fatalf("judge queue: %v", rows)
	}
	rubric := call("judge", "hack_get_rubric", map[string]any{"slug": slug})
	criterionID := rubric["criteria"].([]any)[0].(map[string]any)["id"].(string)
	call("judge", "hack_score_team", map[string]any{"slug": slug, "team_id": teamID, "scores": []any{map[string]any{"criterion_id": criterionID, "points": 4}}, "comment": "Strong build"})
	call("org", "hack_get_judging_dashboard", map[string]any{"slug": slug})
	call("org", "hack_lock_judging", map[string]any{"slug": slug})
	if text, _, bad := toolResultOf(t, a.rpc(t, tokens["judge"], "tools/call", map[string]any{"name": "hack_score_team", "arguments": map[string]any{"slug": slug, "team_id": teamID, "scores": []any{map[string]any{"criterion_id": criterionID, "points": 5}}}})); !bad || !strings.Contains(text, "scores_locked") {
		t.Fatalf("score after lock: %s", text)
	}
	call("org", "hack_publish_results", map[string]any{"slug": slug, "body": map[string]any{"rank_overrides": []any{}}})
	call("org", "hack_set_event_stage", map[string]any{"slug": slug, "stage": "results"})
	myResults := call("participant", "hack_get_my_results", map[string]any{"slug": slug})
	if myResults["published"] != true {
		t.Fatalf("private results: %v", myResults)
	}
	call("org", "hack_set_event_stage", map[string]any{"slug": slug, "stage": "archived"})
	if r := a.api(t, http.MethodGet, "/v1/hack/events/"+slug, nil, org.key); r.status != 200 || r.json(t)["event"].(map[string]any)["stage"] != "archived" {
		t.Fatalf("archived event REST read: %d", r.status)
	}
}
