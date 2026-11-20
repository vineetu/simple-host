package servertest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	accountapp "github.com/vsriram/simple-host/host/app/accounts"
	"github.com/vsriram/simple-host/host/auth"
	"github.com/vsriram/simple-host/host/model"
	accountrepo "github.com/vsriram/simple-host/host/repository/accounts"
)

func (a *teamSiteApp) consentPage(t *testing.T, q url.Values) (string, string) {
	t.Helper()
	page := a.do(t, http.MethodGet, "/oauth/authorize?"+q.Encode(), nil, nil)
	if page.status != http.StatusOK {
		t.Fatalf("authorize page: %d %s", page.status, page.body)
	}
	m := connectDataRe.FindSubmatch(page.body)
	if m == nil {
		t.Fatal("consent page carries no request data")
	}
	var data map[string]any
	if err := json.Unmarshal(m[1], &data); err != nil {
		t.Fatal(err)
	}
	return string(page.body), data["csrf"].(string)
}

func (a *teamSiteApp) decideConsent(t *testing.T, q url.Values, csrf, key string, extra map[string]any) resp {
	t.Helper()
	body := map[string]any{"query": q.Encode(), "csrf": csrf, "decision": "allow"}
	for k, v := range extra {
		body[k] = v
	}
	return a.do(t, http.MethodPost, "/oauth/authorize/decision", jsonBody(body), map[string]string{
		"Content-Type": "application/json", "X-API-Key": key, "Origin": a.srv.URL,
	})
}

func (a *teamSiteApp) exchangeCode(t *testing.T, clientID, redirect, verifier, code, resource string) map[string]any {
	t.Helper()
	r := a.form(t, "/oauth/token", url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect},
		"client_id": {clientID}, "code_verifier": {verifier}, "resource": {resource},
	}, nil)
	if r.status != http.StatusOK {
		t.Fatalf("token: %d %s", r.status, r.body)
	}
	return r.json(t)
}

func mcpToolNames(t *testing.T, r resp) []string {
	t.Helper()
	if r.status != http.StatusOK {
		t.Fatalf("mcp: %d %s", r.status, r.body)
	}
	raw, _ := r.json(t)["result"].(map[string]any)["tools"].([]any)
	names := make([]string, 0, len(raw))
	for _, item := range raw {
		names = append(names, item.(map[string]any)["name"].(string))
	}
	return names
}

func hasTool(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

func (a *teamSiteApp) mcpWithKey(t *testing.T, key string) resp {
	t.Helper()
	return a.do(t, http.MethodPost, "/mcp", jsonBody(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}), map[string]string{
		"Content-Type": "application/json", "X-API-Key": key, "MCP-Protocol-Version": "2025-06-18",
	})
}

func (a *teamSiteApp) setGrantScope(t *testing.T, userID, clientID, scope string) {
	t.Helper()
	res, err := a.database.Exec(`UPDATE oauth_grants SET scope = $1 WHERE user_id = $2 AND client_id = $3`, scope, userID, clientID)
	if err != nil {
		t.Fatal(err)
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		t.Fatalf("grant update affected %d rows", n)
	}
}

func scopeWords(scope string) map[string]bool {
	out := map[string]bool{}
	for _, f := range strings.Fields(scope) {
		out[f] = true
	}
	return out
}

const reconnectGrant = "this connection cannot be refreshed; reconnect and choose a team or Manage my events"

func TestHackOrganiserConnector(t *testing.T) {
	a := newTeamSiteApp(t)
	for _, path := range []string{"/.well-known/oauth-protected-resource/mcp", "/.well-known/oauth-authorization-server"} {
		got := scopeWords(strings.Join(stringList(t, a.do(t, http.MethodGet, path, nil, nil).json(t)["scopes_supported"]), " "))
		if !got["sites"] || !got["events"] || len(got) != 2 {
			t.Fatalf("%s scopes: %v", path, got)
		}
	}
	reg := a.do(t, http.MethodPost, "/oauth/register", jsonBody(map[string]any{
		"client_name": "Test Chat", "redirect_uris": []string{testRedirect}, "token_endpoint_auth_method": "none",
	}), map[string]string{"Content-Type": "application/json"})
	if reg.status != http.StatusCreated || reg.json(t)["scope"] != "sites" {
		t.Fatalf("registration scope: %d %s", reg.status, reg.body)
	}
	clientID := reg.json(t)["client_id"].(string)
	challengeProbe := a.do(t, http.MethodPost, "/mcp", jsonBody(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize"}), nil)
	if !strings.Contains(challengeProbe.header.Get("WWW-Authenticate"), `scope="sites"`) || strings.Contains(challengeProbe.header.Get("WWW-Authenticate"), "events") {
		t.Fatalf("challenge: %s", challengeProbe.header.Get("WWW-Authenticate"))
	}

	org := a.newPerson(t, "org")
	verifier, challenge := newVerifier()
	q := authorizeQuery(clientID, testRedirect, challenge)
	q.Set("resource", a.srv.URL)
	page, csrf := a.consentPage(t, q)
	if !strings.Contains(page, `"hack":true`) || !strings.Contains(page, "help with your hackathons") || !strings.Contains(page, "@media (max-width: 390px)") {
		t.Fatal("consent page is missing the unified hackathon choice or the narrow layout")
	}
	if strings.Count(page, "prefers-color-scheme") != 1 {
		t.Fatalf("consent page prefers-color-scheme count %d", strings.Count(page, "prefers-color-scheme"))
	}
	// A consent page left open from the old team-only flow cannot silently
	// approve the broader personal connection.
	stale := a.decideConsent(t, q, csrf, org.key, map[string]any{
		"mode": "team", "team_id": "00000000-0000-4000-8000-000000000000",
	})
	wantTS(t, "stale consent", stale, http.StatusConflict, "consent_updated")
	allowed := a.decideConsent(t, q, csrf, org.key, nil)
	if allowed.status != http.StatusOK {
		t.Fatalf("allow events: %d %s", allowed.status, allowed.body)
	}
	tok := a.exchangeCode(t, clientID, testRedirect, verifier, codeFrom(t, allowed.json(t)["redirect_to"].(string)), a.srv.URL)
	if tok["scope"] != "sites events" {
		t.Fatalf("token scope: %v", tok["scope"])
	}
	access := tok["access_token"].(string)
	refresh := tok["refresh_token"].(string)
	var stored string
	if err := a.database.QueryRow(`SELECT scope FROM oauth_grants WHERE user_id = $1 AND client_id = $2`, a.uid(t, org), clientID).Scan(&stored); err != nil || stored != "sites events" {
		t.Fatalf("stored grant %q (%v)", stored, err)
	}

	names := mcpToolNames(t, a.rpc(t, access, "tools/list", map[string]any{}))
	if !hasTool(names, "hack_create_event") || !hasTool(names, "create_site") {
		t.Fatalf("events tools: %v", names)
	}
	init := a.rpc(t, access, "initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "t", "version": "1"}})
	if text, _ := init.json(t)["result"].(map[string]any)["instructions"].(string); !strings.Contains(text, "events") {
		t.Fatalf("instructions: %s", text)
	}
	emptyText, empty, emptyErr := toolResultOf(t, a.rpc(t, access, "tools/call", map[string]any{"name": "hack_list_events", "arguments": map[string]any{}}))
	if emptyErr || empty["count"].(float64) != 0 || !strings.Contains(emptyText, "hack_create_event") {
		t.Fatalf("empty list: %v %s", emptyErr, emptyText)
	}

	slug := uniqueSlug()
	t.Cleanup(func() { a.cleanupSlug(slug) })
	badName, _, badErr := toolResultOf(t, a.rpc(t, access, "tools/call", map[string]any{"name": "hack_create_event", "arguments": map[string]any{
		"slug": "ab", "title": "T", "organiser_name": "Org", "contact_email": "org@example.com",
		"purpose": "testing", "expected_participants": 10, "starts_at": "2026-10-01",
	}}))
	if !badErr || strings.Contains(badName, "1 to 63") || !strings.Contains(badName, "3 to 39") {
		t.Fatalf("short name: %s", badName)
	}
	checked, checkOut, checkErr := toolResultOf(t, a.rpc(t, access, "tools/call", map[string]any{"name": "hack_check_event_name", "arguments": map[string]any{"slug": slug}}))
	if checkErr || checkOut["available"] != true {
		t.Fatalf("name check: %v %s", checkErr, checked)
	}
	createdText, created, createErr := toolResultOf(t, a.rpc(t, access, "tools/call", map[string]any{"name": "hack_create_event", "arguments": map[string]any{
		"slug": slug, "title": "Connector hack", "organiser_name": "Org", "contact_email": "org@example.com",
		"purpose": "testing the connector", "expected_participants": 12, "starts_at": "2026-10-01", "time_zone": "UTC",
	}}))
	if createErr {
		t.Fatalf("create: %s", createdText)
	}
	orgView := created["organiser"].(map[string]any)
	if !strings.Contains(orgView["join_url"].(string), "/join/") || !strings.Contains(orgView["judge_url"].(string), "/judge/") || !strings.Contains(createdText, orgView["join_url"].(string)) {
		t.Fatalf("create links: %s", createdText)
	}
	rubricText, rubric, rubricErr := toolResultOf(t, a.rpc(t, access, "tools/call", map[string]any{"name": "hack_set_rubric", "arguments": map[string]any{
		"slug": slug, "criteria": []any{
			map[string]any{"name": "Idea", "weight": 60, "max_points": 10},
			map[string]any{"name": "Build", "weight": 40, "max_points": 10},
		},
	}}))
	if rubricErr {
		t.Fatalf("rubric: %s", rubricText)
	}
	weights := rubric["criteria"].([]any)
	if len(weights) != 2 || weights[0].(map[string]any)["weight"].(float64) != 60 || weights[1].(map[string]any)["weight"].(float64) != 40 {
		t.Fatalf("rubric weights: %v", rubric["criteria"])
	}
	gotRubric, _, gotErr := toolResultOf(t, a.rpc(t, access, "tools/call", map[string]any{"name": "hack_get_rubric", "arguments": map[string]any{"slug": slug}}))
	if gotErr || !strings.Contains(gotRubric, "Idea") {
		t.Fatalf("get rubric: %s", gotRubric)
	}
	updatedText, updated, updateErr := toolResultOf(t, a.rpc(t, access, "tools/call", map[string]any{"name": "hack_update_event", "arguments": map[string]any{"slug": slug, "title": "Connector hack renamed"}}))
	if updateErr || updated["title"] != "Connector hack renamed" {
		t.Fatalf("update: %v %s", updateErr, updatedText)
	}
	live := a.api(t, "GET", "/v1/hack/events/"+slug, nil, org.key).json(t)
	if live["event"].(map[string]any)["title"] != "Connector hack renamed" || live["organiser"].(map[string]any)["purpose"] != "testing the connector" {
		t.Fatalf("update changed more than the title: %v", live)
	}
	stageText, stage, stageErr := toolResultOf(t, a.rpc(t, access, "tools/call", map[string]any{"name": "hack_set_event_stage", "arguments": map[string]any{"slug": slug, "stage": "open"}}))
	if stageErr || stage["stage"] != "open" {
		t.Fatalf("stage: %v %s", stageErr, stageText)
	}
	for _, tool := range []string{"hack_export_scores", "hack_export_results"} {
		csvText, csv, csvErr := toolResultOf(t, a.rpc(t, access, "tools/call", map[string]any{"name": tool, "arguments": map[string]any{"slug": slug}}))
		header := "team,total,rank"
		if tool == "hack_export_scores" {
			header = "team,judge,criterion"
		}
		if csvErr || !strings.Contains(csv["csv"].(string), header) || !strings.Contains(csvText, header) {
			t.Fatalf("%s: %v %s", tool, csvErr, csvText)
		}
	}
	wantTS(t, "personal site", a.do(t, http.MethodPut, "/v1/sites/personal/files?create=1", jsonBody(deployBody("x")), map[string]string{
		"Authorization": "Bearer " + access, "Content-Type": "application/json",
	}), 403, "no_personal_sites")

	// A participant's personal connection keeps their participant role.
	p1 := a.newPerson(t, "p1")
	joined := a.makeEvent(t, org)
	a.join(t, joined, p1, org)
	pVerifier, pChallenge := newVerifier()
	pq := authorizeQuery(clientID, testRedirect, pChallenge)
	pq.Set("resource", a.srv.URL)
	_, pcsrf := a.consentPage(t, pq)
	pAllowed := a.decideConsent(t, pq, pcsrf, p1.key, nil)
	if pAllowed.status != http.StatusOK {
		t.Fatalf("participant allow: %d %s", pAllowed.status, pAllowed.body)
	}
	pTok := a.exchangeCode(t, clientID, testRedirect, pVerifier, codeFrom(t, pAllowed.json(t)["redirect_to"].(string)), a.srv.URL)
	pAccess := pTok["access_token"].(string)
	listedText, listed, listedErr := toolResultOf(t, a.rpc(t, pAccess, "tools/call", map[string]any{"name": "hack_list_events", "arguments": map[string]any{}}))
	if listedErr {
		t.Fatalf("participant list: %s", listedText)
	}
	foundRole := ""
	for _, item := range listed["events"].([]any) {
		row := item.(map[string]any)
		if row["slug"] == joined {
			foundRole, _ = row["role"].(string)
		}
	}
	if foundRole != "participant" {
		t.Fatalf("participant role: %q in %s", foundRole, listedText)
	}
	gotText, got, gotEventErr := toolResultOf(t, a.rpc(t, pAccess, "tools/call", map[string]any{"name": "hack_get_event", "arguments": map[string]any{"slug": joined}}))
	if gotEventErr {
		t.Fatalf("participant get: %s", gotText)
	}
	if _, ok := got["organiser"]; ok || strings.Contains(gotText, "/join/") || strings.Contains(gotText, "Join link") {
		t.Fatalf("participant received join links: %s", gotText)
	}
	rubricDenied, _, rubricDeniedErr := toolResultOf(t, a.rpc(t, pAccess, "tools/call", map[string]any{"name": "hack_set_rubric", "arguments": map[string]any{
		"slug": joined, "criteria": []any{map[string]any{"name": "Idea", "weight": 100, "max_points": 10}},
	}}))
	if !rubricDeniedErr || !strings.Contains(rubricDenied, "event_not_found") {
		t.Fatalf("participant rubric: %v %s", rubricDeniedErr, rubricDenied)
	}
	wantTS(t, "participant site", a.do(t, http.MethodPut, "/v1/sites/personal/files?create=1", jsonBody(deployBody("x")), map[string]string{
		"Authorization": "Bearer " + pAccess, "Content-Type": "application/json",
	}), 403, "no_personal_sites")

	// Key scope is unchanged. Tool advertisement follows what that key can already do.
	personalNames := mcpToolNames(t, a.mcpWithKey(t, org.key))
	if !hasTool(personalNames, "hack_score_team") || !hasTool(personalNames, "hack_list_events") || !hasTool(personalNames, "create_site") {
		t.Fatalf("personal key tools: %v", personalNames)
	}
	deployRaw, _ := auth.GenerateAPIKey()
	if _, err := accountapp.New(a.database).CreateKey(context.Background(), a.uid(t, org), accountrepo.HashAPIKey(org.key), deployRaw, "ci", model.KeyScopeDeploy, nil, 50, 180*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	deployNames := mcpToolNames(t, a.mcpWithKey(t, deployRaw))
	if !hasTool(deployNames, "storage_list_resources") || hasTool(deployNames, "get_state") || !hasTool(deployNames, "create_site") || hasTool(deployNames, "hack_list_events") {
		t.Fatalf("deploy key tools: %v", deployNames)
	}
	adminNames := mcpToolNames(t, a.mcpWithKey(t, a.admin))
	if !hasTool(adminNames, "storage_list_resources") || hasTool(adminNames, "get_state") || hasTool(adminNames, "hack_list_events") {
		t.Fatalf("admin key tools: %v", adminNames)
	}

	// An old or malformed grant fails closed without spending its refresh token.
	uid := a.uid(t, org)
	assertClosed := func(scope string) {
		t.Helper()
		a.setGrantScope(t, uid, clientID, scope)
		mcpDenied := a.rpc(t, access, "ping", map[string]any{})
		if mcpDenied.status != http.StatusUnauthorized || !strings.Contains(mcpDenied.header.Get("WWW-Authenticate"), `error="invalid_token"`) {
			t.Fatalf("%s mcp: %d %s", scope, mcpDenied.status, mcpDenied.header.Get("WWW-Authenticate"))
		}
		me := a.do(t, http.MethodGet, "/v1/me", nil, map[string]string{"Authorization": "Bearer " + access})
		if me.status != http.StatusUnauthorized || me.json(t)["code"] != "invalid_token" {
			t.Fatalf("%s bearer: %d %s", scope, me.status, me.body)
		}
		for i := 0; i < 2; i++ {
			refreshed := a.form(t, "/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {clientID}}, nil)
			if refreshed.status != http.StatusBadRequest || refreshed.json(t)["error"] != "invalid_grant" || refreshed.json(t)["error_description"] != reconnectGrant {
				t.Fatalf("%s refresh %d: %d %s", scope, i, refreshed.status, refreshed.body)
			}
		}
		var n int
		if err := a.database.QueryRow(`SELECT count(*) FROM oauth_grants WHERE user_id = $1 AND client_id = $2`, uid, clientID).Scan(&n); err != nil || n != 1 {
			t.Fatalf("%s grant count %d (%v)", scope, n, err)
		}
	}
	assertClosed("sites")
	assertClosed("sites team:00000000-0000-4000-8000-000000000000 events")
	assertClosed("sites team:")
	assertClosed("sites eventsx")
	assertClosed("sites team: events")
	assertClosed("sites team:not-a-uuid")
	assertClosed("sites events events")
	a.setGrantScope(t, uid, clientID, "sites events")
	if me := a.do(t, http.MethodGet, "/v1/me", nil, map[string]string{"Authorization": "Bearer " + access}); me.status != http.StatusOK {
		t.Fatalf("restored bearer: %d %s", me.status, me.body)
	}
	refreshed := a.form(t, "/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {clientID}}, nil)
	if refreshed.status != http.StatusOK || refreshed.json(t)["scope"] != "sites events" {
		t.Fatalf("restored refresh: %d %s", refreshed.status, refreshed.body)
	}
	newAccess := refreshed.json(t)["access_token"].(string)
	restoredNames := mcpToolNames(t, a.rpc(t, newAccess, "tools/list", map[string]any{}))
	if !hasTool(restoredNames, "hack_list_events") || !hasTool(restoredNames, "create_site") {
		t.Fatalf("restored tools: %v", restoredNames)
	}
}

func (a *teamSiteApp) cleanupSlug(slug string) {
	var accountID string
	_ = a.database.QueryRow(`SELECT account_id FROM events WHERE slug = $1`, slug).Scan(&accountID)
	_, _ = a.database.Exec(`DELETE FROM events WHERE slug = $1`, slug)
	if accountID != "" {
		_, _ = a.database.Exec(`DELETE FROM users WHERE id = $1`, accountID)
	}
}

func stringList(t *testing.T, v any) []string {
	t.Helper()
	raw, ok := v.([]any)
	if !ok {
		t.Fatalf("not a list: %v", v)
	}
	out := make([]string, len(raw))
	for i, item := range raw {
		out[i], ok = item.(string)
		if !ok {
			t.Fatalf("not a string: %v", item)
		}
	}
	return out
}
