package handler

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"strings"
	"testing"
)

// Exercises member tools through an OAuth event connection and compares their
// visible state with the corresponding REST reads.
func TestHackMemberConnectorParity(t *testing.T) {
	a := newTeamSiteApp(t)
	org := a.newPerson(t, "mcporg")
	participant := a.newPerson(t, "mcppart")
	judge := a.newPerson(t, "mcpjudge")
	slug := a.makeEvent(t, org)
	base := "/v1/hack/events/" + slug
	orgEvent := a.api(t, "GET", base, nil, org.key).json(t)
	links := orgEvent["organiser"].(map[string]any)
	tokenFor := func(p person) string {
		t.Helper()
		reg := a.do(t, http.MethodPost, "/oauth/register", jsonBody(map[string]any{"client_name": "Member test", "redirect_uris": []string{testRedirect}, "token_endpoint_auth_method": "none"}), map[string]string{"Content-Type": "application/json"})
		if reg.status != 201 {
			t.Fatalf("register: %d %s", reg.status, reg.body)
		}
		client := reg.json(t)["client_id"].(string)
		verifier, challenge := newVerifier()
		q := authorizeQuery(client, testRedirect, challenge)
		q.Set("resource", a.srv.URL)
		_, csrf := a.consentPage(t, q)
		allow := a.decideConsent(t, q, csrf, p.key, nil)
		if allow.status != 200 {
			t.Fatalf("consent: %d %s", allow.status, allow.body)
		}
		tok := a.exchangeCode(t, client, testRedirect, verifier, codeFrom(t, allow.json(t)["redirect_to"].(string)), a.srv.URL)
		return tok["access_token"].(string)
	}
	ptok := tokenFor(participant)
	jtok := tokenFor(judge)
	otok := tokenFor(org)
	call := func(token, name string, args map[string]any) (map[string]any, string, bool) {
		t.Helper()
		text, out, bad := toolResultOf(t, a.rpc(t, token, "tools/call", map[string]any{"name": name, "arguments": args}))
		return out, text, bad
	}
	joinCode := links["join_code"].(string)
	preview, text, bad := call(ptok, "hack_preview_join", map[string]any{"code": joinCode})
	if bad || preview["slug"] != slug || preview["coc_default"] == "" {
		t.Fatalf("preview: %s", text)
	}
	_, text, bad = call(ptok, "hack_join_event", map[string]any{"code": joinCode, "accept_coc": false, "display_name": "Pat"})
	if !bad || !strings.Contains(text, "coc_required") {
		t.Fatalf("coc check: %s", text)
	}
	joined, text, bad := call(ptok, "hack_join_event", map[string]any{"code": joinCode, "accept_coc": true, "display_name": "Pat"})
	if bad || joined["role"] != "participant" {
		t.Fatalf("join: %s", text)
	}
	state, text, bad := call(ptok, "hack_application_status", map[string]any{"slug": slug})
	if bad || state["me"].(map[string]any)["approval_status"] != "approved" {
		t.Fatalf("application: %s", text)
	}
	team, text, bad := call(ptok, "hack_create_team", map[string]any{"slug": slug, "name": "Member team"})
	if bad || team["name"] != "Member team" {
		t.Fatalf("team: %s", text)
	}
	_, text, bad = call(jtok, "hack_create_team", map[string]any{"slug": slug, "name": "Wrong role"})
	if !bad || !strings.Contains(text, "event_not_found") {
		t.Fatalf("judge team denied: %s", text)
	}
	saved, text, bad := call(ptok, "hack_update_entry", map[string]any{"slug": slug, "title": "Connector project", "description": "Built here"})
	if bad || saved["entry"].(map[string]any)["title"] != "Connector project" {
		t.Fatalf("entry save: %s", text)
	}
	read, text, bad := call(ptok, "hack_get_entry", map[string]any{"slug": slug})
	if bad || read["entry"].(map[string]any)["title"] != a.api(t, "GET", base+"/entry", nil, participant.key).json(t)["entry"].(map[string]any)["title"] {
		t.Fatalf("entry parity: %s", text)
	}
	var shot bytes.Buffer
	pixel := image.NewRGBA(image.Rect(0, 0, 1, 1))
	pixel.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(&shot, pixel); err != nil {
		t.Fatal(err)
	}
	shotB64 := base64.StdEncoding.EncodeToString(shot.Bytes())
	_, text, bad = call(ptok, "hack_set_entry_screenshot", map[string]any{"slug": slug, "base64": shotB64})
	if bad {
		t.Fatalf("screenshot save: %s", text)
	}
	imageResult, imageText, imageErr := call(ptok, "hack_get_entry_screenshot", map[string]any{"slug": slug})
	if imageErr || imageResult["base64"] != shotB64 || imageResult["content_type"] != "image/png" {
		t.Fatalf("screenshot read: %s", imageText)
	}
	organiserImage, imageText, imageErr := call(otok, "hack_get_team_screenshot", map[string]any{"slug": slug, "team": team["slug"]})
	if imageErr || organiserImage["content_base64"] != shotB64 || organiserImage["content_type"] != "image/png" {
		t.Fatalf("organiser screenshot read: %s", imageText)
	}
	entries, entriesText, entriesErr := call(otok, "hack_get_entries", map[string]any{"slug": slug})
	if entriesErr || entries["response"] == nil {
		t.Fatalf("organiser entries: %s", entriesText)
	}
	_, text, bad = call(ptok, "hack_delete_entry_screenshot", map[string]any{"slug": slug})
	if bad {
		t.Fatalf("screenshot removal: %s", text)
	}
	judgeCode := links["judge_code"].(string)
	_, text, bad = call(jtok, "hack_preview_judge", map[string]any{"code": judgeCode})
	if bad {
		t.Fatalf("judge preview: %s", text)
	}
	_, text, bad = call(jtok, "hack_join_judge", map[string]any{"code": judgeCode, "accept_coc": true, "display_name": "Jules"})
	if bad {
		t.Fatalf("judge join: %s", text)
	}
	queue, text, bad := call(jtok, "hack_get_judge_queue", map[string]any{"slug": slug})
	if bad || queue["queue"] == nil {
		t.Fatalf("judge queue: %s", text)
	}
	teamStatus, text, bad := call(ptok, "hack_get_team_status", map[string]any{"slug": slug})
	if bad || teamStatus["me"].(map[string]any)["team"].(map[string]any)["site"] == nil {
		t.Fatalf("team site status: %s", text)
	}
	items := queue["queue"].([]any)
	if len(items) != 1 {
		t.Fatalf("judge queue should have one team: %s", text)
	}
	teamID := items[0].(map[string]any)["team_id"].(string)
	rubric, rubricText, rubricErr := call(jtok, "hack_get_rubric", map[string]any{"slug": slug})
	if rubricErr {
		t.Fatalf("judge rubric: %s", rubricText)
	}
	criterionID, _ := rubric["criteria"].([]any)[0].(map[string]any)["id"].(string)
	if criterionID == "" {
		t.Fatalf("rubric lacks criterion ID needed to score: %v", rubric)
	}
	scores, scoreText, scoreErr := call(jtok, "hack_score_team", map[string]any{"slug": slug, "team_id": teamID, "scores": []any{map[string]any{"criterion_id": criterionID, "points": 3}}, "comment": "Good"})
	if scoreErr || scores["comment"] != "Good" {
		t.Fatalf("score: %s", scoreText)
	}
	scores, scoreText, scoreErr = call(jtok, "hack_get_judge_scores", map[string]any{"slug": slug, "team_id": teamID})
	if scoreErr || scores["comment"] != "Good" {
		t.Fatalf("score read: %s", scoreText)
	}
	conflict, conflictText, conflictErr := call(jtok, "hack_declare_my_conflict", map[string]any{"slug": slug, "team_id": teamID})
	if conflictErr || conflict["team_id"] != teamID {
		t.Fatalf("declare conflict: %s", conflictText)
	}
	conflicts, conflictText, conflictErr := call(jtok, "hack_get_my_conflicts", map[string]any{"slug": slug})
	if conflictErr || len(conflicts["conflicts"].([]any)) != 1 {
		t.Fatalf("list conflicts: %s", conflictText)
	}
	_, scoreText, scoreErr = call(jtok, "hack_score_team", map[string]any{"slug": slug, "team_id": teamID, "scores": []any{map[string]any{"criterion_id": criterionID, "points": 2}}})
	if !scoreErr || !strings.Contains(scoreText, "conflict") {
		t.Fatalf("conflicted score allowed: %s", scoreText)
	}
	removed, conflictText, conflictErr := call(jtok, "hack_remove_my_conflict", map[string]any{"slug": slug, "team_id": teamID})
	if conflictErr || removed["ok"] != true {
		t.Fatalf("remove conflict: %s", conflictText)
	}
	_, text, bad = call(ptok, "hack_get_judge_queue", map[string]any{"slug": slug})
	if !bad || !strings.Contains(text, "event_not_found") {
		t.Fatalf("participant queue denied: %s", text)
	}
}
