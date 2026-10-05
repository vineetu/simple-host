package mcp

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// hackRouteTool is deliberately only a REST adapter. The existing event
// handlers remain the source of validation, permission checks and errors.
type hackRouteTool struct {
	name, title, description, method, path string
	params                                 []string
	body                                   bool
	destructive, idempotent, public        bool
	csv, archive                           bool
}

// HackOrganiserTools covers the event administration routes not already
// exposed by HackTools. A personal connection may call these across events;
// the REST handler verifies organiser membership for each request.
func HackOrganiserTools() []Tool {
	routes := hackOrganiserRoutes()
	tools := make([]Tool, 0, len(routes))
	for _, route := range routes {
		r := route
		props := map[string]any{}
		for _, p := range r.params {
			props[p] = str("REST path parameter: " + p + ".")
		}
		if r.body {
			props["body"] = map[string]any{"type": "object", "description": hackRouteBodyDescriptions[r.name] + " The REST route validates each field and returns its own errors.", "additionalProperties": true}
		}
		if r.name == "hack_remove_judge_conflict" {
			props["judge_user_id"] = str("Optional judge user ID. Omit to remove your own conflict.")
		}
		schema := object(props, append(append([]string{}, r.params...), requiredBody(r.body)...)...)
		outSchema := outObject(map[string]any{"response": anyJSON("Unchanged REST JSON response."), "status": outInteger("REST status code.")}, "response", "status")
		if r.csv {
			outSchema = hackOutputSchemas()["hack_export_scores"]
		}
		if r.archive {
			outSchema = outObject(map[string]any{"url": outString("Short-lived private download link for the complete archive."), "expires_at": outString("Link expiration in RFC3339 format."), "expires_in": outInteger("Seconds until expiration.")}, "url", "expires_at", "expires_in")
		}
		if r.name == "hack_get_team_screenshot" {
			outSchema = outObject(map[string]any{"content_type": outString("Screenshot image MIME type."), "content_base64": outString("Complete screenshot image bytes encoded as base64.")}, "content_type", "content_base64")
		}
		tool := Tool{Name: r.name, Title: r.title, Description: r.description, InputSchema: schema, OutputSchema: outSchema,
			Annotations: writes(r.destructive, r.idempotent, r.public)}
		if r.method == http.MethodGet || r.archive {
			tool.Annotations = readOnly()
		}
		tool.run = func(c *call, args map[string]any) (output, error) { return runHackRoute(c, r, args) }
		tools = append(tools, tool)
	}
	return tools
}

// These hints make the pass-through body usable from a tool listing without
// restating REST validation rules in the connector.
var hackRouteBodyDescriptions = map[string]string{
	"hack_delete_event":            "confirm: the exact event address name (slug), supplied only after the person confirms deletion.",
	"hack_set_content":             "Whole content document: sponsors [{name,tier,logo_data,url}], faq [{question,answer}], schedule [{title,description,start_at,end_at}].",
	"hack_post_announcement":       "title, body, and optional email_participants boolean.",
	"hack_set_registration":        "questions [{id,prompt,required}] and approval_required boolean.",
	"hack_decide_application":      "decision: approved or rejected.",
	"hack_set_tracks":              "tracks [{slug,name,challenge,prize}].",
	"hack_set_voting_settings":     "enabled, opens_at, closes_at, eligibility (all_signed_in, event_members or participants).",
	"hack_set_directory_listing":   "listed boolean.",
	"hack_move_member":             "user_id of the participant to move to the team named in the path.",
	"hack_rename_team":             "name for the team's new display name.",
	"hack_set_team_deadline":       "deadline in the event time zone, or an empty string to remove the extension.",
	"hack_accept_organiser_invite": "accept_coc true and display_name.",
	"hack_set_judging_settings":    "Any changed field: assignment_mode, judges_per_team, score_mode, tie_criterion_id, public_scores, public_ranks.",
	"hack_set_assignments":         "assignments [{judge_id,team_id}], replacing the whole manual list.",
	"hack_set_panels":              "panels [{track_id,judge_id}], replacing the whole panel list.",
	"hack_set_judge_conflict":      "team_id and optional judge_user_id (omit for your own conflict).",
	"hack_unlock_judging":          "reason explaining why scoring is reopened.",
	"hack_publish_results":         "rank_overrides [{team_id,rank}], or an empty list for computed ranks.",
	"hack_set_results_view":        "full_ranking boolean (true for every rank, false for winners only).",
}

func hackOrganiserRoutes() []hackRouteTool {
	return []hackRouteTool{
		{"hack_get_directory", "List public events", "Read the public event directory.", "GET", "/v1/hack/directory", nil, false, false, true, false, false, false},
		{"hack_get_content", "Read event content", "Read sponsors, FAQ and schedule. Organiser only for private drafts.", "GET", "/v1/hack/events/{slug}/content", []string{"slug"}, false, false, true, false, false, false},
		{"hack_set_content", "Replace event content", "Replace sponsors, FAQ and schedule with the supplied REST content body. Ask before overwriting. Organiser only.", "PUT", "/v1/hack/events/{slug}/content", []string{"slug"}, true, true, true, true, false, false},
		{"hack_get_announcements", "Read announcements", "Read event announcements. Organiser only.", "GET", "/v1/hack/events/{slug}/announcements", []string{"slug"}, false, false, true, false, false, false},
		{"hack_post_announcement", "Post an announcement", "Post an event announcement. Body includes text and optional email flag; email sends it to participants. Ask the person before posting or emailing. Organiser only.", "POST", "/v1/hack/events/{slug}/announcements", []string{"slug"}, true, false, false, true, false, false},
		{"hack_get_registration", "Read registration", "Read signup questions and approval settings. Organiser only.", "GET", "/v1/hack/events/{slug}/registration", []string{"slug"}, false, false, true, false, false, false},
		{"hack_set_registration", "Replace registration", "Replace signup questions and approval_required. Ask before overwriting existing questions. Organiser only.", "PUT", "/v1/hack/events/{slug}/registration", []string{"slug"}, true, true, true, false, false, false},
		{"hack_get_applications", "List applications", "Read pending and decided applications. Organiser only.", "GET", "/v1/hack/events/{slug}/applications", []string{"slug"}, false, false, true, false, false, false},
		{"hack_decide_application", "Decide application", "Approve or reject an applicant using body.decision. Ask before rejecting. Organiser only.", "POST", "/v1/hack/events/{slug}/applications/{user_id}/decision", []string{"slug", "user_id"}, true, true, false, false, false, false},
		{"hack_get_tracks", "Read tracks", "Read event tracks. Organiser or participant.", "GET", "/v1/hack/events/{slug}/tracks", []string{"slug"}, false, false, true, false, false, false},
		{"hack_set_tracks", "Replace tracks", "Replace the full tracks list. Ask before overwriting. Organiser only.", "PUT", "/v1/hack/events/{slug}/tracks", []string{"slug"}, true, true, true, false, false, false},
		{"hack_get_voting_settings", "Read voting settings", "Read event voting settings. Organiser only.", "GET", "/v1/hack/events/{slug}/voting", []string{"slug"}, false, false, true, false, false, false},
		{"hack_set_voting_settings", "Set voting settings", "Replace voting settings in the REST body. Opening voting is public; ask first. Organiser only.", "PUT", "/v1/hack/events/{slug}/voting", []string{"slug"}, true, true, true, true, false, false},
		{"hack_set_directory_listing", "Set directory listing", "Set listed in the REST body to show or hide the event in the public directory. Ask before listing publicly. Organiser only.", "PATCH", "/v1/hack/events/{slug}/directory", []string{"slug"}, true, true, true, true, false, false},
		{"hack_regenerate_code", "Make a new join or judge link", "Regenerate a join or judge invitation (kind is join or judge). The old link stops working. Ask first. Organiser only.", "POST", "/v1/hack/events/{slug}/codes/{kind}", []string{"slug", "kind"}, false, true, false, true, false, false},
		{"hack_delete_event", "Delete event", "Permanently delete an event at any stage, its public page, results, team sites, entries, scores and votes. Participants and judges lose access; the name stays reserved unless nobody ever joined. No undo. Always explain this and confirm with the person first, then send body.confirm matching slug. Organiser only.", "DELETE", "/v1/hack/events/{slug}", []string{"slug"}, true, true, false, true, false, false},
		{"hack_get_people", "List event people", "Read organiser, participant and judge members. Organiser only.", "GET", "/v1/hack/events/{slug}/people", []string{"slug"}, false, false, true, false, false, false},
		{"hack_remove_person", "Remove event person", "Remove a member and their event access. Ask first. Organiser only.", "DELETE", "/v1/hack/events/{slug}/people/{user_id}", []string{"slug", "user_id"}, false, true, false, false, false, false},
		{"hack_revoke_person_key", "Turn a person's key off", "Revoke a person's event key. Ask first; publishing with it stops. Organiser only.", "DELETE", "/v1/hack/events/{slug}/people/{user_id}/key", []string{"slug", "user_id"}, false, true, false, false, false, false},
		{"hack_get_teams", "List event teams", "Read event teams and members. Organiser only.", "GET", "/v1/hack/events/{slug}/teams", []string{"slug"}, false, false, true, false, false, false},
		{"hack_get_entries", "List entries", "Read all team entries and pinned website links. Organiser or judge.", "GET", "/v1/hack/events/{slug}/entries", []string{"slug"}, false, false, true, false, false, false},
		{"hack_get_team_screenshot", "Read team screenshot", "Read one team's entry screenshot as base64 image bytes. Organiser or judge.", "GET", "/v1/hack/events/{slug}/teams/{team}/screenshot", []string{"slug", "team"}, false, false, true, false, false, false},
		{"hack_move_member", "Move a member to team", "Move a member to the specified team using the REST body. Ask first. Organiser only.", "POST", "/v1/hack/events/{slug}/teams/{team}/members", []string{"slug", "team"}, true, true, false, false, false, false},
		{"hack_remove_team_member", "Remove a team member", "Take a person off a team. Ask first. Organiser only.", "DELETE", "/v1/hack/events/{slug}/teams/{team}/members/{user_id}", []string{"slug", "team", "user_id"}, false, true, false, false, false, false},
		{"hack_rename_team", "Rename a team", "Rename an event team's display name with body.name. Its site slug and address stay the same. Organiser only.", "PATCH", "/v1/hack/events/{slug}/teams/{team}", []string{"slug", "team"}, true, true, false, false, false, false},
		{"hack_delete_team", "Delete a team", "Delete a team and its event entry. Ask first. Organiser only.", "DELETE", "/v1/hack/events/{slug}/teams/{team}", []string{"slug", "team"}, false, true, false, true, false, false},
		{"hack_set_team_deadline", "Set team deadline", "Give one team more time with body.deadline. Ask first. Organiser only.", "PUT", "/v1/hack/events/{slug}/teams/{team}/deadline", []string{"slug", "team"}, true, true, true, false, false, false},
		{"hack_take_down_team_site", "Take down team site", "Take a team's public site down. Ask first. Organiser only.", "POST", "/v1/hack/events/{slug}/teams/{team}/takedown", []string{"slug", "team"}, false, true, true, true, false, false},
		{"hack_restore_team_site", "Restore team site", "Restore a team's public site. Ask first. Organiser only.", "POST", "/v1/hack/events/{slug}/teams/{team}/restore", []string{"slug", "team"}, false, false, true, true, false, false},
		{"hack_create_organiser_invite", "Invite co-organiser", "Create a co-organiser invitation link. Ask first before sharing it. Organiser only.", "POST", "/v1/hack/events/{slug}/organiser-invite", []string{"slug"}, false, false, false, false, false, false},
		{"hack_revoke_organiser_invite", "Revoke co-organiser invite", "Revoke the current co-organiser invitation link. Ask first. Organiser only.", "DELETE", "/v1/hack/events/{slug}/organiser-invite", []string{"slug"}, false, true, true, false, false, false},
		{"hack_preview_organiser_invite", "Preview co-organiser invite", "Read a co-organiser invitation before accepting it.", "GET", "/v1/hack/organiser/{code}", []string{"code"}, false, false, true, false, false, false},
		{"hack_accept_organiser_invite", "Accept co-organiser invite", "Accept the invitation with body.accept_coc and body.display_name. Ask the person first.", "POST", "/v1/hack/organiser/{code}", []string{"code"}, true, false, false, false, false, false},
		{"hack_remove_organiser", "Remove co-organiser", "Remove a co-organiser. Ask first. Organiser only.", "DELETE", "/v1/hack/events/{slug}/organisers/{user_id}", []string{"slug", "user_id"}, false, true, false, false, false, false},
		{"hack_get_judging_settings", "Read judging settings", "Read assignment and scoring settings. Organiser only.", "GET", "/v1/hack/events/{slug}/judging/settings", []string{"slug"}, false, false, true, false, false, false},
		{"hack_set_judging_settings", "Set judging settings", "Change assignment_mode, judges_per_team, score_mode, tie_criterion_id or public visibility flags. Ask before changing public flags. Organiser only.", "PATCH", "/v1/hack/events/{slug}/judging/settings", []string{"slug"}, true, true, false, true, false, false},
		{"hack_generate_assignments", "Generate judge assignments", "Generate assignments from current settings. Replaces previous automatic assignments; ask first. Organiser only.", "POST", "/v1/hack/events/{slug}/assignments/generate", []string{"slug"}, false, true, false, false, false, false},
		{"hack_get_assignments", "Read judge assignments", "Read manual assignments. Organiser only.", "GET", "/v1/hack/events/{slug}/assignments", []string{"slug"}, false, false, true, false, false, false},
		{"hack_set_assignments", "Replace judge assignments", "Replace manual assignments with body.assignments. Ask first. Organiser only.", "PUT", "/v1/hack/events/{slug}/assignments", []string{"slug"}, true, true, true, false, false, false},
		{"hack_get_panels", "Read judging panels", "Read track judging panels. Organiser only.", "GET", "/v1/hack/events/{slug}/judging/panels", []string{"slug"}, false, false, true, false, false, false},
		{"hack_set_panels", "Replace judging panels", "Replace track panel memberships with body.panels. Ask first. Organiser only.", "PUT", "/v1/hack/events/{slug}/judging/panels", []string{"slug"}, true, true, true, false, false, false},
		{"hack_preview_judging_assignments", "Preview judging", "Preview assignment coverage without changing it. Organiser only.", "GET", "/v1/hack/events/{slug}/judging/preview", []string{"slug"}, false, false, true, false, false, false},
		{"hack_get_conflicts", "Read conflicts", "Read declared judge/team conflicts. Organiser only.", "GET", "/v1/hack/events/{slug}/conflicts", []string{"slug"}, false, false, true, false, false, false},
		{"hack_set_judge_conflict", "Record judge conflict", "Record a conflict for any event judge using body.team_id and body.judge_user_id. Organisers may also omit judge_user_id to record their own conflict. Existing scores for that pairing leave current totals; ask first.", "POST", "/v1/hack/events/{slug}/conflicts", []string{"slug"}, true, true, false, false, false, false},
		{"hack_remove_judge_conflict", "Remove judge conflict", "Remove a conflict; use query judge_user_id to choose another judge, or omit it for yourself. Ask before removing. Organiser only for another judge.", "DELETE", "/v1/hack/events/{slug}/conflicts/{team_id}", []string{"slug", "team_id"}, false, true, true, false, false, false},
		{"hack_get_judging_dashboard", "Read judging progress", "Read judging coverage and totals. Organiser only.", "GET", "/v1/hack/events/{slug}/judging/dashboard", []string{"slug"}, false, false, true, false, false, false},
		{"hack_lock_judging", "Lock judging", "Freeze scores and comments. Ask first. Organiser only.", "POST", "/v1/hack/events/{slug}/judging/lock", []string{"slug"}, false, false, true, false, false, false},
		{"hack_unlock_judging", "Unlock judging", "Reopen scoring with body.reason. Ask first. Organiser only.", "POST", "/v1/hack/events/{slug}/judging/unlock", []string{"slug"}, true, false, false, false, false, false},
		{"hack_publish_results", "Publish results", "Publish results using body.rank_overrides for any tie breaks. This replaces the previous public snapshot: show the preview and ask first. Organiser only.", "POST", "/v1/hack/events/{slug}/results/publish", []string{"slug"}, true, true, false, true, false, false},
		{"hack_set_results_view", "Set public results view", "Set body.full_ranking to show all ranks or winners only. Ask first. Organiser only.", "PATCH", "/v1/hack/events/{slug}/results", []string{"slug"}, true, false, true, true, false, false},
		{"hack_get_public_results", "Read public results", "Read published results. Available publicly after publication.", "GET", "/v1/hack/events/{slug}/results", []string{"slug"}, false, false, true, false, false, false},
		{"hack_export_participants", "Export participants", "Read the participant CSV. Organiser only; keep private.", "GET", "/v1/hack/events/{slug}/export/participants.csv", []string{"slug"}, false, false, true, false, true, false},
		{"hack_export_teams", "Export teams", "Read the team CSV. Organiser only; keep private.", "GET", "/v1/hack/events/{slug}/export/teams.csv", []string{"slug"}, false, false, true, false, true, false},
		{"hack_export_entries", "Export entries", "Read the entries CSV. Organiser only; keep private.", "GET", "/v1/hack/events/{slug}/export/entries.csv", []string{"slug"}, false, false, true, false, true, false},
		{"hack_export_projects_archive", "Export team site archive", "Create a short-lived private download link for the complete team site archive. Organiser only; give the link only to the person.", "POST", "/v1/hack/events/{slug}/export/projects-link", []string{"slug"}, false, false, false, false, false, true},
		{"hack_export_own_team_archive", "Export own team archive", "Create a short-lived private download link for your team's complete archive; give the link only to the person.", "POST", "/v1/hack/events/{slug}/export/own-team-link", []string{"slug"}, false, false, false, false, false, true},
		{"hack_get_usage", "Read event usage", "Read event storage and traffic usage. Organiser only.", "GET", "/v1/hack/events/{slug}/usage", []string{"slug"}, false, false, true, false, false, false},
		{"hack_get_team_key", "Read team key status", "Read whether your team has a publishing key. Participant only.", "GET", "/v1/hack/events/{slug}/key", []string{"slug"}, false, false, true, false, false, false},
		{"hack_create_team_key", "Make team key", "Create or rotate your team's publishing key. The old key stops working; ask first. Participant only.", "POST", "/v1/hack/events/{slug}/key", []string{"slug"}, false, true, false, false, false, false},
		{"hack_revoke_team_key", "Revoke team key", "Turn your team's publishing key off. Ask first. Participant only.", "DELETE", "/v1/hack/events/{slug}/key", []string{"slug"}, false, true, false, false, false, false},
	}
}

func requiredBody(body bool) []string {
	if body {
		return []string{"body"}
	}
	return nil
}

func runHackRoute(c *call, route hackRouteTool, args map[string]any) (output, error) {
	path := route.path
	for _, p := range route.params {
		value, err := stringArg(args, p)
		if err != nil {
			return output{}, err
		}
		if p == "slug" {
			value = strings.ToLower(value)
		}
		path = strings.ReplaceAll(path, "{"+p+"}", url.PathEscape(value))
	}
	if route.name == "hack_remove_judge_conflict" {
		if judgeID, present, err := presentString(args, "judge_user_id"); err != nil {
			return output{}, err
		} else if present && judgeID != "" {
			path += "?judge_user_id=" + url.QueryEscape(judgeID)
		}
	}
	var raw []byte
	if route.body {
		body, ok := args["body"].(map[string]any)
		if !ok {
			return output{}, fmt.Errorf("body must be a JSON object")
		}
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			return output{}, err
		}
	}
	res := c.do(route.method, path, raw, nil)
	if !res.ok() {
		return output{}, hackRestError(route.name, res)
	}
	if route.csv {
		return hackCSVOutput(fmt.Sprint(args["slug"]), strings.TrimPrefix(route.name, "hack_export_"), res)
	}
	if route.archive {
		var link map[string]any
		if err := json.Unmarshal(res.body, &link); err != nil {
			return output{}, fmt.Errorf("%s: server returned invalid JSON: %w", route.name, err)
		}
		return output{Text: jsonText(link), Structured: link}, nil
	}
	if route.name == "hack_get_team_screenshot" {
		structured := map[string]any{"content_type": res.header.Get("Content-Type"), "content_base64": base64.StdEncoding.EncodeToString(res.body)}
		return output{Text: "Screenshot image in structured result.", Structured: structured}, nil
	}
	var response any
	if len(res.body) > 0 {
		if err := json.Unmarshal(res.body, &response); err != nil {
			return output{}, fmt.Errorf("%s: server returned invalid JSON: %w", route.name, err)
		}
	}
	structured := map[string]any{"response": response, "status": res.status}
	return output{Text: jsonText(structured), Structured: structured}, nil
}
