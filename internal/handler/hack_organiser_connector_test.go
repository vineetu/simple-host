package handler

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// Real-handler checks: organiser tools use the personal OAuth grant and have
// the same result and permission refusal as REST, across the event's private
// administration areas.
func TestHackOrganiserConnectorParity(t *testing.T) {
	a := newTeamSiteApp(t)
	organiser := a.newPerson(t, "mcpoadmin")
	stranger := a.newPerson(t, "mcpostranger")
	slug := a.makeEvent(t, organiser)
	base := "/v1/hack/events/" + slug
	tokenFor := func(p person) string {
		t.Helper()
		reg := a.do(t, http.MethodPost, "/oauth/register", jsonBody(map[string]any{"client_name": "Organiser test", "redirect_uris": []string{testRedirect}, "token_endpoint_auth_method": "none"}), map[string]string{"Content-Type": "application/json"})
		if reg.status != 201 {
			t.Fatalf("register: %d", reg.status)
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
		return a.exchangeCode(t, client, testRedirect, verifier, codeFrom(t, allow.json(t)["redirect_to"].(string)), a.srv.URL)["access_token"].(string)
	}
	orgToken, strangerToken := tokenFor(organiser), tokenFor(stranger)
	call := func(token, name string, args map[string]any) (map[string]any, string, bool) {
		t.Helper()
		text, out, bad := toolResultOf(t, a.rpc(t, token, "tools/call", map[string]any{"name": name, "arguments": args}))
		return out, text, bad
	}
	// Read parity compares the whole REST payload, including nested fields.
	reads := []struct{ name, path string }{
		{"hack_get_content", "/content"},
		{"hack_get_announcements", "/announcements"},
		{"hack_get_registration", "/registration"},
		{"hack_get_applications", "/applications"},
		{"hack_get_tracks", "/tracks"},
		{"hack_get_voting_settings", "/voting"},
		{"hack_get_people", "/people"},
		{"hack_get_teams", "/teams"},
		{"hack_get_judging_settings", "/judging/settings"},
		{"hack_get_assignments", "/assignments"},
		{"hack_get_panels", "/judging/panels"},
		{"hack_preview_judging_assignments", "/judging/preview"},
		{"hack_get_conflicts", "/conflicts"},
		{"hack_get_judging_dashboard", "/judging/dashboard"},
		{"hack_get_usage", "/usage"},
	}
	for _, tc := range reads {
		t.Run(tc.name, func(t *testing.T) {
			rest := a.api(t, http.MethodGet, base+tc.path, nil, organiser.key)
			if rest.status != 200 {
				t.Fatalf("REST %d %s", rest.status, rest.body)
			}
			var restJSON any
			if err := json.Unmarshal([]byte(rest.body), &restJSON); err != nil {
				t.Fatal(err)
			}
			out, text, bad := call(orgToken, tc.name, map[string]any{"slug": slug})
			if bad || out["status"] != float64(rest.status) || !reflect.DeepEqual(out["response"], restJSON) {
				t.Fatalf("MCP/REST mismatch: %s", text)
			}
		})
	}
	// These private routes span event, people, team, judging and export
	// mutations. A nonmember's connector must get the REST refusal before any
	// body validation or data disclosure.
	denied := []struct {
		name, method, path string
		args               map[string]any
	}{
		{"hack_set_content", "PUT", "/content", map[string]any{"body": map[string]any{}}},
		{"hack_post_announcement", "POST", "/announcements", map[string]any{"body": map[string]any{}}},
		{"hack_set_registration", "PUT", "/registration", map[string]any{"body": map[string]any{}}},
		{"hack_get_applications", "GET", "/applications", nil},
		{"hack_set_tracks", "PUT", "/tracks", map[string]any{"body": map[string]any{}}},
		{"hack_set_voting_settings", "PUT", "/voting", map[string]any{"body": map[string]any{}}},
		{"hack_set_directory_listing", "PATCH", "/directory", map[string]any{"body": map[string]any{"listed": true}}},
		{"hack_regenerate_code", "POST", "/codes/join", map[string]any{"kind": "join"}},
		{"hack_get_people", "GET", "/people", nil},
		{"hack_get_teams", "GET", "/teams", nil},
		{"hack_create_organiser_invite", "POST", "/organiser-invite", nil},
		{"hack_get_judging_settings", "GET", "/judging/settings", nil},
		{"hack_generate_assignments", "POST", "/assignments/generate", nil},
		{"hack_get_judging_dashboard", "GET", "/judging/dashboard", nil},
		{"hack_lock_judging", "POST", "/judging/lock", nil},
		{"hack_publish_results", "POST", "/results/publish", map[string]any{"body": map[string]any{}}},
		{"hack_export_participants", "GET", "/export/participants.csv", nil},
		{"hack_export_teams", "GET", "/export/teams.csv", nil},
		{"hack_export_entries", "GET", "/export/entries.csv", nil},
		{"hack_get_usage", "GET", "/usage", nil},
	}
	for _, tc := range denied {
		t.Run("deny_"+tc.name, func(t *testing.T) {
			rest := a.api(t, tc.method, base+tc.path, tc.args["body"], stranger.key)
			if rest.status < 400 {
				t.Fatalf("REST unexpectedly allowed: %d", rest.status)
			}
			args := map[string]any{"slug": slug}
			for k, v := range tc.args {
				args[k] = v
			}
			_, text, bad := call(strangerToken, tc.name, args)
			if !bad || !strings.Contains(text, rest.json(t)["code"].(string)) {
				t.Fatalf("MCP did not preserve REST refusal: %s", text)
			}
		})
	}
}
