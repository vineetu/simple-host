// famHackEvents organiser administration and shared REST route adapters.
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
	// These hints make the pass-through body usable from a tool listing without
	// restating REST validation rules in the connector.
	bodyDescription                        string
	name, title, description, method, path string
	params                                 []string
	body                                   bool
	destructive, idempotent, public        bool
	csv, archive                           bool
	policy                                 toolPolicy
}

// HackOrganiserTools covers the event administration routes not already
// exposed by HackTools. A personal connection may call these across events;
// the REST handler verifies organiser membership for each request.
func HackOrganiserTools() []Tool {
	return append(hackOrganiserEventTools(), hackOrganiserEntryTools()...)
}

func hackOrganiserEventTools() []Tool {
	return hackRouteTools(hackOrganiserEventRoutes())
}

func hackRouteTools(routes []hackRouteTool) []Tool {
	tools := make([]Tool, 0, len(routes)+1)
	for _, route := range routes {
		r := route
		props := map[string]any{}
		for _, p := range r.params {
			props[p] = str("REST path parameter: " + p + ".")
		}
		if r.body {
			props["body"] = map[string]any{"type": "object", "description": r.bodyDescription + " The REST route validates each field and returns its own errors.", "additionalProperties": true}
		}
		if r.policy.judgeParameter {
			props["judge_user_id"] = str("Optional judge user ID. Omit to remove your own conflict.")
		}
		if r.policy.includeEmails {
			props["include_emails"] = map[string]any{"type": "boolean", "description": "Set true only when the organiser asks for team members' email addresses."}
		}
		schema := object(props, append(append([]string{}, r.params...), requiredBody(r.body)...)...)
		outSchema := outObject(map[string]any{"response": anyJSON("REST JSON response with fields limited to this operation."), "status": outInteger("REST status code.")}, "response", "status")
		if r.csv {
			outSchema = hackEntrySchemas().csv
		}
		if r.archive {
			outSchema = outObject(map[string]any{"url": outString("Short-lived private download link for the complete archive."), "expires_at": outString("Link expiration in RFC3339 format."), "expires_in": outInteger("Seconds until expiration.")}, "url", "expires_at", "expires_in")
		}
		if r.policy.screenshot {
			outSchema = outObject(map[string]any{"content_type": outString("Screenshot image MIME type."), "content_base64": outString("Complete screenshot image bytes encoded as base64.")}, "content_type", "content_base64")
		}
		tool := Tool{policy: r.policy, Name: r.name, Title: r.title, Description: r.description, InputSchema: schema, OutputSchema: outSchema,
			Annotations: writes(r.destructive, r.idempotent, r.public)}
		if r.method == http.MethodGet || r.archive {
			tool.Annotations = readOnly()
			tool.Annotations["openWorldHint"] = r.public
		}
		if r.policy.recordsPins {
			// These reads record deadline pins when due.
			tool.Annotations = writes(false, true, false)
		}
		tool.run = func(c *call, args map[string]any) (output, error) { return runHackRoute(c, r, args) }
		tools = append(tools, tool)
	}
	return tools
}

func hackOrganiserRoutes() []hackRouteTool {
	return append(hackOrganiserEventRoutes(), hackOrganiserEntryRoutes()...)
}

func hackOrganiserEventRoutes() []hackRouteTool {
	return []hackRouteTool{
		{name: "hack_get_directory", title: "List public events", description: "Read the public event directory. Rows with applications_open true take applications without a join link: use hack_preview_application then hack_apply_to_event.", method: "GET", path: "/v1/hack/directory", params: nil, body: false, destructive: false, idempotent: true, public: true, csv: false, archive: false},
		{name: "hack_get_content", title: "Read event content", description: "Read sponsors, FAQ and schedule. fixed_schedule lists the event's real times as read-only rows marked fixed: true (event start and end, the entry deadline or the caller's own team extension, the voting window, results published), each with set_in naming where it is changed (basics, entries, results). Show them with the schedule but never write them back. prompt on a row (organisers only) is the move the next step asks for at that time. Organiser only for private drafts.", method: "GET", path: "/v1/hack/events/{slug}/content", params: []string{"slug"}, body: false, destructive: false, idempotent: true, public: false, csv: false, archive: false},
		{bodyDescription: "Whole content document: sponsors [{name,tier,logo_data,url}], faq [{question,answer}], schedule [{title,description,start_at,end_at,prompt}]. prompt is optional: open, building, closed, judging, results or publish_results makes the organiser's next step ask for that move when the row starts (it never moves the stage itself). Send only the editable schedule rows: fixed_schedule rows and any row marked fixed are ignored, since they come from the event's settings.", name: "hack_set_content", title: "Replace event content", description: "Replace sponsors, FAQ and schedule with the supplied REST content body. Ask before overwriting. Organiser only.", method: "PUT", path: "/v1/hack/events/{slug}/content", params: []string{"slug"}, body: true, destructive: true, idempotent: true, public: true, csv: false, archive: false},
		{name: "hack_get_announcements", title: "Read announcements", description: "Read event announcements. Organiser only.", method: "GET", path: "/v1/hack/events/{slug}/announcements", params: []string{"slug"}, body: false, destructive: false, idempotent: true, public: false, csv: false, archive: false},
		{bodyDescription: "title and body. Editing does not send a new email.", name: "hack_edit_announcement", title: "Edit an announcement", description: "Edit title and body. Editing does not send a new email. Organiser only.", method: "PUT", path: "/v1/hack/events/{slug}/announcements/{announcement}", params: []string{"slug", "announcement"}, body: true, destructive: true, idempotent: true, public: true, csv: false, archive: false},
		{name: "hack_delete_announcement", title: "Delete an announcement", description: "Delete an announcement. Ask first. Organiser only.", method: "DELETE", path: "/v1/hack/events/{slug}/announcements/{announcement}", params: []string{"slug", "announcement"}, body: false, destructive: true, idempotent: true, public: true, csv: false, archive: false},
		{bodyDescription: "title, body, and optional email_participants boolean. When true, email goes to every approved participant with an email-shaped username, plus one copy to the posting organiser if their own username is email-shaped too (never a duplicate if they are also a participant).", name: "hack_post_announcement", title: "Post an announcement", description: "Post an event announcement. Body includes text and optional email flag; email sends it to every approved participant with an email-shaped username, plus a copy to the posting organiser when their own username is email-shaped (one delivery, never duplicated if they are also a participant). The response's emails_queued is the real count actually queued, not an estimate; organiser_copied says whether one of those is the organiser's own copy. Ask the person before posting or emailing. Organiser only.", method: "POST", path: "/v1/hack/events/{slug}/announcements", params: []string{"slug"}, body: true, destructive: true, idempotent: false, public: true, csv: false, archive: false},
		{name: "hack_get_registration", title: "Read registration", description: "Read signup questions and approval settings. Organiser only.", method: "GET", path: "/v1/hack/events/{slug}/registration", params: []string{"slug"}, body: false, destructive: false, idempotent: true, public: false, csv: false, archive: false},
		{bodyDescription: "questions [{id,prompt,required}], approval_required boolean (join-link sign-ups only), optional admit_pending boolean (approves join-link pending only; applications always stay pending).", name: "hack_set_registration", title: "Replace registration", description: "Replace signup questions and approval_required. approval_required and admit_pending govern join-link sign-ups only; an application (from hack_apply_to_event) always stays pending until decided, whatever this says. When turning approval off, ask whether to approve waiting join-link sign-ups with admit_pending: true in this request or leave them pending. Ask before overwriting existing questions. Organiser only.", method: "PUT", path: "/v1/hack/events/{slug}/registration", params: []string{"slug"}, body: true, destructive: true, idempotent: true, public: false, csv: false, archive: false},
		{policy: toolPolicy{analytics: analyticsPolicy{applicationAnswers: true}},
			name: "hack_get_applications", title: "List applications", description: "Read pending and decided applications, with joined_via, city, country, age_range and gender for each. An application (joined_via application) always needs a decision, even when approval is off for join-link sign-ups. An under_18 applicant deserves the organiser's attention (no extra data is collected). Organiser only.", method: "GET", path: "/v1/hack/events/{slug}/applications", params: []string{"slug"}, body: false, destructive: false, idempotent: true, public: false, csv: false, archive: false},
		{policy: toolPolicy{organiserResult: "decision"}, bodyDescription: "decision: approved or rejected.", name: "hack_decide_application", title: "Decide application", description: "Approve or reject an applicant using body.decision. Ask before rejecting. Organiser only.", method: "POST", path: "/v1/hack/events/{slug}/applications/{user_id}/decision", params: []string{"slug", "user_id"}, body: true, destructive: true, idempotent: false, public: false, csv: false, archive: false},
		{name: "hack_get_tracks", title: "Read tracks", description: "Read event tracks. Organiser or participant.", method: "GET", path: "/v1/hack/events/{slug}/tracks", params: []string{"slug"}, body: false, destructive: false, idempotent: true, public: false, csv: false, archive: false},
		{bodyDescription: "tracks [{slug,name,challenge,prize}].", name: "hack_set_tracks", title: "Replace tracks", description: "Replace the full tracks list. Slugs allow 80 characters; duplicate names are allowed. Before removing a track, read teams and warn how many teams will have no track. Ask before overwriting. Organiser only.", method: "PUT", path: "/v1/hack/events/{slug}/tracks", params: []string{"slug"}, body: true, destructive: true, idempotent: true, public: true, csv: false, archive: false},
		{bodyDescription: "listed boolean.", name: "hack_set_directory_listing", title: "Set directory listing", description: "New events are unlisted. Set listed in the REST body to show or hide the event in the public directory. Ask before listing publicly. Organiser only.", method: "PATCH", path: "/v1/hack/events/{slug}/directory", params: []string{"slug"}, body: true, destructive: true, idempotent: true, public: true, csv: false, archive: false},
		{policy: toolPolicy{analytics: analyticsPolicy{secretText: true, secretURL: true}},
			name: "hack_regenerate_code", title: "Make a new join or judge link", description: "Regenerate a join or judge invitation (kind is join or judge). The old link stops working. Ask first. Organiser only.", method: "POST", path: "/v1/hack/events/{slug}/codes/{kind}", params: []string{"slug", "kind"}, body: false, destructive: true, idempotent: false, public: true, csv: false, archive: false},
		{bodyDescription: "confirm: the exact event address name (slug), supplied only after the person confirms deletion.", name: "hack_delete_event", title: "Delete event", description: "Permanently delete an event at any stage, its public page, results, team sites, entries, scores and votes. Participants and judges lose access; the name stays reserved unless nobody ever joined. No undo. Always explain this and confirm with the person first, then send body.confirm matching slug. Organiser only.", method: "DELETE", path: "/v1/hack/events/{slug}", params: []string{"slug"}, body: true, destructive: true, idempotent: false, public: true, csv: false, archive: false},
		{name: "hack_get_people", title: "List event people", description: "Read organiser, participant and judge members. Organiser only.", method: "GET", path: "/v1/hack/events/{slug}/people", params: []string{"slug"}, body: false, destructive: false, idempotent: true, public: false, csv: false, archive: false},
		{name: "hack_remove_person", title: "Remove event person", description: "Remove a member and their event access. Ask first. Organiser only.", method: "DELETE", path: "/v1/hack/events/{slug}/people/{user_id}", params: []string{"slug", "user_id"}, body: false, destructive: true, idempotent: false, public: false, csv: false, archive: false},
		{name: "hack_revoke_person_key", title: "Turn a person's key off", description: "Revoke a person's event key. Ask first; publishing with it stops. Organiser only.", method: "DELETE", path: "/v1/hack/events/{slug}/people/{user_id}/key", params: []string{"slug", "user_id"}, body: false, destructive: true, idempotent: false, public: false, csv: false, archive: false},
		{bodyDescription: "Set apply:false to preview placements of approved solo participants into teams with room, topped up to size (people per team, default min(4, the event's team size)), then new teams of that size. Locked teams are skipped. Show the plan and confirm before apply:true; the plan is recomputed using current membership.", name: "hack_fill_teams", title: "Fill teams", description: "Preview with body.apply:false. Show the plan and confirm before body.apply:true. Organiser only.", method: "POST", path: "/v1/hack/events/{slug}/teams/fill", params: []string{"slug"}, body: true, destructive: true, idempotent: false, public: false, csv: false, archive: false},
		{policy: toolPolicy{includeEmails: true, recordsPins: true, organiserResult: "teams"}, name: "hack_get_teams", title: "List event teams", description: "Read event teams and member names and IDs. Emails are omitted unless include_emails is true. Records any due submission deadline pins. Organiser only.", method: "GET", path: "/v1/hack/events/{slug}/teams", params: []string{"slug"}, body: false, destructive: false, idempotent: true, public: false, csv: false, archive: false},
		{policy: toolPolicy{organiserResult: "member"}, bodyDescription: "user_id of the participant to move to the team named in the path.", name: "hack_move_member", title: "Move a member to team", description: "Move a member to the specified team using the REST body. Ask first. Organiser only.", method: "POST", path: "/v1/hack/events/{slug}/teams/{team}/members", params: []string{"slug", "team"}, body: true, destructive: true, idempotent: false, public: false, csv: false, archive: false},
		{name: "hack_remove_team_member", title: "Remove a team member", description: "Take a person off a team. Ask first. Organiser only.", method: "DELETE", path: "/v1/hack/events/{slug}/teams/{team}/members/{user_id}", params: []string{"slug", "team", "user_id"}, body: false, destructive: true, idempotent: false, public: false, csv: false, archive: false},
		{bodyDescription: "name for the team's new display name.", name: "hack_rename_team", title: "Rename a team", description: "Rename an event team's display name with body.name. Its site slug and address stay the same. Organiser only.", method: "PATCH", path: "/v1/hack/events/{slug}/teams/{team}", params: []string{"slug", "team"}, body: true, destructive: true, idempotent: false, public: true, csv: false, archive: false},
		{bodyDescription: "locked boolean.", name: "hack_lock_team", title: "Lock or unlock a team", description: "Body.locked true or false. Locking stops the team taking anyone new by request, link, code or invite, and clears its pending requests and invites. Ask first, and say those are cleared. Organiser only.", method: "PUT", path: "/v1/hack/events/{slug}/teams/{team}/lock", params: []string{"slug", "team"}, body: true, destructive: true, idempotent: true, public: false, csv: false, archive: false},
		{bodyDescription: "admin boolean for the user_id in the path.", name: "hack_set_team_admin", title: "Make or remove a team admin", description: "Body.admin true or false for the member in the path. Destructive: it changes who can approve requests, lock, rename, remove members and rotate the link. The last admin can't be unmade until someone else is admin. Ask first. Organiser only.", method: "PUT", path: "/v1/hack/events/{slug}/teams/{team}/members/{user_id}/admin", params: []string{"slug", "team", "user_id"}, body: true, destructive: true, idempotent: true, public: false, csv: false, archive: false},
		{name: "hack_new_team_link", title: "Make a new team link", description: "Destructive: the team's old link and code stop working at once. Ask first. Organiser only.", method: "POST", path: "/v1/hack/events/{slug}/teams/{team}/link", params: []string{"slug", "team"}, body: false, destructive: true, idempotent: false, public: false, csv: false, archive: false},
		{name: "hack_delete_team", title: "Delete a team", description: "Delete a team and its event entry. Ask first. Organiser only.", method: "DELETE", path: "/v1/hack/events/{slug}/teams/{team}", params: []string{"slug", "team"}, body: false, destructive: true, idempotent: false, public: true, csv: false, archive: false},
		{policy: toolPolicy{organiserResult: "team"}, bodyDescription: "deadline in the event time zone, or an empty string to remove the extension.", name: "hack_set_team_deadline", title: "Set team deadline", description: "Give one team more time with body.deadline. Ask first. Organiser only.", method: "PUT", path: "/v1/hack/events/{slug}/teams/{team}/deadline", params: []string{"slug", "team"}, body: true, destructive: true, idempotent: true, public: false, csv: false, archive: false},
		{policy: toolPolicy{organiserResult: "team"}, name: "hack_take_down_team_site", title: "Take down team site", description: "Take a team's public site down. Ask first. Organiser only.", method: "POST", path: "/v1/hack/events/{slug}/teams/{team}/takedown", params: []string{"slug", "team"}, body: false, destructive: true, idempotent: true, public: true, csv: false, archive: false},
		{policy: toolPolicy{organiserResult: "team"}, name: "hack_restore_team_site", title: "Restore team site", description: "Restore a team's public site. Ask first. Organiser only.", method: "POST", path: "/v1/hack/events/{slug}/teams/{team}/restore", params: []string{"slug", "team"}, body: false, destructive: true, idempotent: true, public: true, csv: false, archive: false},
		{policy: toolPolicy{analytics: analyticsPolicy{secretText: true, secretURL: true}},
			name: "hack_create_organiser_invite", title: "Invite co-organiser", description: "Create a co-organiser invitation link. Ask first before sharing it. Organiser only.", method: "POST", path: "/v1/hack/events/{slug}/organiser-invite", params: []string{"slug"}, body: false, destructive: true, idempotent: false, public: false, csv: false, archive: false},
		{name: "hack_revoke_organiser_invite", title: "Revoke co-organiser invite", description: "Revoke the current co-organiser invitation link. Ask first. Organiser only.", method: "DELETE", path: "/v1/hack/events/{slug}/organiser-invite", params: []string{"slug"}, body: false, destructive: true, idempotent: true, public: false, csv: false, archive: false},
		{name: "hack_preview_organiser_invite", title: "Preview co-organiser invite", description: "Read a co-organiser invitation before accepting it.", method: "GET", path: "/v1/hack/organiser/{code}", params: []string{"code"}, body: false, destructive: false, idempotent: true, public: false, csv: false, archive: false},
		{bodyDescription: "accept_coc true and display_name.", name: "hack_accept_organiser_invite", title: "Accept co-organiser invite", description: "Accept the invitation with body.accept_coc and body.display_name. Ask the person first.", method: "POST", path: "/v1/hack/organiser/{code}", params: []string{"code"}, body: true, destructive: true, idempotent: false, public: false, csv: false, archive: false},
		{name: "hack_remove_organiser", title: "Remove co-organiser", description: "Remove a co-organiser. Ask first. Organiser only.", method: "DELETE", path: "/v1/hack/events/{slug}/organisers/{user_id}", params: []string{"slug", "user_id"}, body: false, destructive: true, idempotent: false, public: false, csv: false, archive: false},
		{policy: toolPolicy{analytics: analyticsPolicy{fileResult: true}},
			name: "hack_export_participants", title: "Export participants", description: "Read the participant CSV, which adds city, country, age_range, gender and joined_via columns. Organiser only; keep private.", method: "GET", path: "/v1/hack/events/{slug}/export/participants.csv", params: []string{"slug"}, body: false, destructive: false, idempotent: true, public: false, csv: true, archive: false},
		{policy: toolPolicy{analytics: analyticsPolicy{fileResult: true}},
			name: "hack_export_teams", title: "Export teams", description: "Read the team CSV. Organiser only; keep private.", method: "GET", path: "/v1/hack/events/{slug}/export/teams.csv", params: []string{"slug"}, body: false, destructive: false, idempotent: true, public: false, csv: true, archive: false},
		{policy: toolPolicy{analytics: analyticsPolicy{secretText: true, secretURL: true}},
			name: "hack_export_projects_archive", title: "Export team site archive", description: "Create a short-lived private download link for the complete team site archive. Organiser only; give the link only to the person.", method: "POST", path: "/v1/hack/events/{slug}/export/projects-link", params: []string{"slug"}, body: false, destructive: false, idempotent: false, public: false, csv: false, archive: true},
		{policy: toolPolicy{analytics: analyticsPolicy{secretText: true, secretURL: true}},
			name: "hack_export_own_team_archive", title: "Export own team archive", description: "Create a short-lived private download link for your team's complete archive; give the link only to the person.", method: "POST", path: "/v1/hack/events/{slug}/export/own-team-link", params: []string{"slug"}, body: false, destructive: false, idempotent: false, public: false, csv: false, archive: true},
		{name: "hack_get_usage", title: "Read event usage", description: "Read event storage and traffic usage. Organiser only.", method: "GET", path: "/v1/hack/events/{slug}/usage", params: []string{"slug"}, body: false, destructive: false, idempotent: true, public: false, csv: false, archive: false},
		{policy: toolPolicy{organiserResult: "key", analytics: analyticsPolicy{secretText: true, secretKey: true}},
			name: "hack_get_team_key", title: "Read team key status", description: "Read whether your team has a publishing key. Participant only.", method: "GET", path: "/v1/hack/events/{slug}/key", params: []string{"slug"}, body: false, destructive: false, idempotent: true, public: false, csv: false, archive: false},
		// Minting a team key belongs in the trusted browser/REST workflow,
		// never in a connector result that could put the secret in chat.
		{policy: toolPolicy{connectorHidden: true, analytics: analyticsPolicy{secretText: true, secretKey: true}},
			name: "hack_create_team_key", title: "Make team key", description: "Create or rotate your team's publishing key. The old key stops working; ask first. Participant only.", method: "POST", path: "/v1/hack/events/{slug}/key", params: []string{"slug"}, body: false, destructive: true, idempotent: false, public: false, csv: false, archive: false},
		{name: "hack_revoke_team_key", title: "Revoke team key", description: "Turn your team's publishing key off. Ask first. Participant only.", method: "DELETE", path: "/v1/hack/events/{slug}/key", params: []string{"slug"}, body: false, destructive: true, idempotent: false, public: false, csv: false, archive: false},
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
	if route.policy.judgeParameter {
		if judgeID, present, err := presentString(args, "judge_user_id"); err != nil {
			return output{}, err
		} else if present && judgeID != "" {
			path += "?judge_user_id=" + url.QueryEscape(judgeID)
		}
	}
	var raw []byte
	// Generating assignments takes an optional body (include_organisers).
	_, hasBody := args["body"]
	if route.body && (hasBody || !route.policy.optionalBody) {
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
	if route.policy.screenshot {
		structured := map[string]any{"content_type": res.header.Get("Content-Type"), "content_base64": base64.StdEncoding.EncodeToString(res.body)}
		return output{Text: "Screenshot image in structured result.", Structured: structured}, nil
	}
	var response any
	if len(res.body) > 0 {
		if err := json.Unmarshal(res.body, &response); err != nil {
			return output{}, fmt.Errorf("%s: server returned invalid JSON: %w", route.name, err)
		}
	}
	if obj, ok := response.(map[string]any); ok {
		includeEmails, _ := args["include_emails"].(bool)
		minimiseHackOrganiserResult(route.policy, includeEmails, obj)
	}
	structured := map[string]any{"response": response, "status": res.status}
	return output{Text: jsonText(structured), Structured: structured}, nil
}

// hackSetEntryExcludedTool is hack_set_entry_excluded: a toggle over the
// website's two REST actions, POST .../teams/{team}/entry/exclude and
// .../include, chosen by the excluded bool (parity rule, build-plan.md
// section 4.5: everything on the website must exist in the AI apps too).
