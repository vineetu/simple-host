package hackmcp

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/vsriram/simple-host/host/mcp"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// hackRouteTool is only a REST adapter: the event handlers stay the source of
// validation, permission checks and errors.
type hackRouteTool struct {
	bodyDescription                        string
	name, title, description, method, path string
	body                                   bool
	destructive, idempotent, public        bool
	csv, archive                           bool
	policy                                 toolPolicy
}

// HackOrganiserTools covers the event administration routes HackTools does not;
// the REST handler verifies organiser membership on each request.
func HackOrganiserTools() []mcp.Tool {
	return append(hackOrganiserEventTools(), hackOrganiserEntryTools()...)
}

func hackOrganiserEventTools() []mcp.Tool {
	return hackRouteTools(hackOrganiserEventRoutes())
}

func hackRouteTools(routes []hackRouteTool) []mcp.Tool {
	tools := make([]mcp.Tool, 0, len(routes))
	for _, route := range routes {
		r := route
		tool := withToolPolicy(mcp.Tool{Name: r.name, Title: r.title, Description: r.description,
			InputSchema: r.inputSchema(), OutputSchema: r.outputSchema(), Annotations: r.annotations()}, r.policy)
		tools = append(tools, mcp.WithRun(tool, func(c *mcp.Call, args map[string]any) (mcp.Output, error) { return runHackRoute(c, r, args) }))
	}
	return tools
}

func (r hackRouteTool) params() []string {
	var params []string
	for _, m := range pathParam.FindAllStringSubmatch(r.path, -1) {
		params = append(params, m[1])
	}
	return params
}

var pathParam = regexp.MustCompile(`\{([a-z_]+)\}`)

// routeParams describes each path parameter a route tool takes.
var routeParams = map[string]string{
	"slug": "Event name.", "team": "The team's name as it appears in its address (its slug, e.g. blue-fox): team.slug in hack_get_judge_queue or hack_get_entries, or slug in hack_get_teams.", "team_id": "Team ID.",
	"user_id": "The person's user ID.", "announcement": "Announcement ID.",
	"code": "Code from the invite link.", "kind": "join or judge.",
}

func (r hackRouteTool) inputSchema() map[string]any {
	props := map[string]any{}
	required := r.params()
	for _, p := range required {
		props[p] = mcp.Str(routeParams[p])
	}
	if r.body {
		props["body"] = map[string]any{"type": "object", "description": r.bodyDescription, "additionalProperties": true}
		required = append(required, "body")
	}
	if announcement := announcementBodySchema(r.name); announcement != nil {
		props["body"] = announcement
	}
	if r.policy.judgeParameter {
		props["judge_user_id"] = mcp.Str("Optional judge user ID. Omit to remove your own conflict.")
	}
	if r.policy.includeEmails {
		props["include_emails"] = map[string]any{"type": "boolean", "description": "Set true only when the organiser asks for team members' email addresses."}
	}
	return mcp.Object(props, required...)
}

func announcementBodySchema(name string) map[string]any {
	if name != "hack_post_announcement" && name != "hack_edit_announcement" {
		return nil
	}
	fields := map[string]any{"title": mcp.Str("Announcement title (1–120 characters)."), "body": mcp.Str("Message (1–3000 characters)."), "pinned": map[string]any{"type": "boolean", "description": "Pin to top, replacing the previous pin, or unpin."}}
	schema := map[string]any{"type": "object", "properties": fields, "additionalProperties": false}
	if name == "hack_post_announcement" {
		fields["email_participants"] = map[string]any{"type": "boolean", "description": "Also email participants; off by default."}
		schema["required"] = []string{"title", "body"}
	} else {
		schema["description"] = "Provide title and body together, or pinned alone. The REST route validates this."
	}
	return schema
}

func (r hackRouteTool) outputSchema() map[string]any {
	switch {
	case r.policy.screenshot:
		return mcp.OutObject(map[string]any{"content_type": mcp.OutString("Screenshot image MIME type."), "content_base64": mcp.OutString("Complete screenshot image bytes encoded as base64.")}, "content_type", "content_base64")
	case r.archive:
		return mcp.OutObject(map[string]any{"url": mcp.OutString("Short-lived private download link for the complete archive."), "expires_at": mcp.OutString("Link expiration in RFC3339 format."), "expires_in": mcp.OutInteger("Seconds until expiration.")}, "url", "expires_at", "expires_in")
	case r.csv:
		return hackEntrySchemas().csv
	}
	response := mcp.AnyJSON("REST JSON response with fields limited to this operation.")
	if r.name == "hack_get_announcements" {
		item := mcp.OutObject(map[string]any{"id": mcp.OutString("Announcement ID."), "title": mcp.OutString("Title."), "body": mcp.OutString("Original text."), "body_html": mcp.OutString("Safe server-rendered HTML; insert as-is."), "pinned": mcp.OutBool("Pinned to top."), "created_at": mcp.OutString("Creation time."), "emails_sent": mcp.OutInteger("Deliveries sent.")}, "id", "title", "body", "body_html", "pinned", "created_at", "emails_sent")
		response = mcp.OutObject(map[string]any{"announcements": mcp.OutArray("Pinned first, then newest.", item)}, "announcements")
	}
	return mcp.OutObject(map[string]any{"response": response, "status": mcp.OutInteger("REST status code.")}, "response", "status")
}

func (r hackRouteTool) annotations() map[string]any {
	switch {
	case r.policy.recordsPins:
		return mcp.Writes(false, true, false)
	case r.method == http.MethodGet || r.archive:
		// A read stays inside the service even when what it reads is public.
		return mcp.ReadOnly()
	}
	return mcp.Writes(r.destructive, r.idempotent, r.public)
}

func hackOrganiserEventRoutes() []hackRouteTool {
	return []hackRouteTool{
		{name: "hack_get_directory", title: "List public events", description: "Read the public event directory. Rows with applications_open true take applications without a join link: use hack_preview_application then hack_apply_to_event.", method: "GET", path: "/v1/hack/directory", idempotent: true, public: true},
		{name: "hack_get_content", title: "Read event content", description: "Read sponsors, FAQ and schedule. fixed_schedule lists the event's real times as read-only rows marked fixed: true (event start and end, the entry deadline or the caller's own team extension, the voting window, results published), each with set_in naming where it is changed (basics, entries, results). Show them with the schedule but never write them back. prompt on a row (organisers only) is the move the next step asks for at that time. Organiser only for private drafts.", method: "GET", path: "/v1/hack/events/{slug}/content", idempotent: true},
		{bodyDescription: "Whole content document: sponsors [{name,tier,logo_data,url}], faq [{question,answer}], schedule [{title,description,start_at,end_at,prompt}]. prompt is optional: open, building, closed, judging, results or publish_results makes the organiser's next step ask for that move when the row starts (it never moves the stage itself). Send only the editable schedule rows: fixed_schedule rows and any row marked fixed are ignored, since they come from the event's settings.", name: "hack_set_content", title: "Replace event content", description: "Replace sponsors, FAQ and schedule with the supplied REST content body. Ask before overwriting. Organiser only.", method: "PUT", path: "/v1/hack/events/{slug}/content", body: true, destructive: true, idempotent: true, public: true},
		{name: "hack_get_announcements", title: "Read announcements", description: "Read event announcements as any member, pinned first; body_html is safe server-rendered HTML.", method: "GET", path: "/v1/hack/events/{slug}/announcements", idempotent: true},
		{bodyDescription: "title and body, or pinned boolean alone to pin/unpin. Pinning replaces the previous pin. Editing does not send a new email.", name: "hack_edit_announcement", title: "Edit an announcement", description: "Edit title and body, or pinned boolean alone to pin/unpin. Pinning replaces the previous pin. The body takes the same formatting as hack_post_announcement. Editing does not send a new email. Organiser only.", method: "PUT", path: "/v1/hack/events/{slug}/announcements/{announcement}", body: true, destructive: true, idempotent: true, public: true},
		{name: "hack_delete_announcement", title: "Delete an announcement", description: "Delete an announcement. Ask first. Organiser only.", method: "DELETE", path: "/v1/hack/events/{slug}/announcements/{announcement}", destructive: true, idempotent: true, public: true},
		{bodyDescription: "title, body, optional pinned and email_participants booleans. When true, email goes to every approved participant with an email-shaped username, plus one copy to the posting organiser if their own username is email-shaped too (never a duplicate if they are also a participant).", name: "hack_post_announcement", title: "Post an announcement", description: "Post an event announcement; pinned replaces the previous pin. The body supports [label](https://...) links, **bold**, - bullets and line breaks; a Zoom/Meet/Teams/YouTube address on its own line shows as a card. email_participants also emails every approved participant and the posting organiser; emails_queued is the real count. Ask the person before posting or emailing. Organiser only.", method: "POST", path: "/v1/hack/events/{slug}/announcements", body: true, destructive: true, public: true},
		{name: "hack_get_registration", title: "Read registration", description: "Read signup questions and approval settings. Organiser only.", method: "GET", path: "/v1/hack/events/{slug}/registration", idempotent: true},
		{bodyDescription: "questions [{id (unique lowercase name, e.g. tshirt),prompt,required}], approval_required boolean (join-link sign-ups only), optional admit_pending boolean (approves join-link pending only; applications always stay pending).", name: "hack_set_registration", title: "Replace registration", description: "Replace signup questions and approval_required. approval_required and admit_pending govern join-link sign-ups only; an application (from hack_apply_to_event) always stays pending until decided, whatever this says. When turning approval off, ask whether to approve waiting join-link sign-ups with admit_pending: true in this request or leave them pending. Ask before overwriting existing questions. Organiser only.", method: "PUT", path: "/v1/hack/events/{slug}/registration", body: true, destructive: true, idempotent: true},
		{policy: toolPolicy{analytics: analyticsPolicy{applicationAnswers: true}},
			name: "hack_get_applications", title: "List applications", description: "Read pending and decided applications, with joined_via, city, country, age_range and gender for each. An application (joined_via application) always needs a decision, even when approval is off for join-link sign-ups. An under_18 applicant deserves the organiser's attention (no extra data is collected). Organiser only.", method: "GET", path: "/v1/hack/events/{slug}/applications", idempotent: true},
		{policy: toolPolicy{organiserResult: "decision"}, bodyDescription: "decision: approved or rejected.", name: "hack_decide_application", title: "Decide application", description: "Approve or reject an applicant using body.decision. Ask before rejecting. Organiser only.", method: "POST", path: "/v1/hack/events/{slug}/applications/{user_id}/decision", body: true, destructive: true},
		{name: "hack_get_tracks", title: "Read tracks", description: "Read event tracks. Organiser or participant.", method: "GET", path: "/v1/hack/events/{slug}/tracks", idempotent: true},
		{bodyDescription: "tracks [{slug,name,challenge,prize}].", name: "hack_set_tracks", title: "Replace tracks", description: "Replace the full tracks list. Slugs allow 80 characters; duplicate names are allowed. Before removing a track, read teams and warn how many teams will have no track. Ask before overwriting. Organiser only.", method: "PUT", path: "/v1/hack/events/{slug}/tracks", body: true, destructive: true, idempotent: true, public: true},
		{bodyDescription: "listed boolean.", name: "hack_set_directory_listing", title: "Set directory listing", description: "New events are unlisted. Set listed in the REST body to show or hide the event in the public directory. Ask before listing publicly. Organiser only.", method: "PATCH", path: "/v1/hack/events/{slug}/directory", body: true, destructive: true, idempotent: true, public: true},
		{policy: toolPolicy{analytics: analyticsPolicy{secretText: true, secretURL: true}},
			name: "hack_regenerate_code", title: "Make a new join or judge link", description: "Regenerate a join or judge invitation (kind is join or judge). The old link stops working. Ask first. Organiser only.", method: "POST", path: "/v1/hack/events/{slug}/codes/{kind}", destructive: true, public: true},
		{bodyDescription: "confirm: the exact event address name (slug), supplied only after the person confirms deletion.", name: "hack_delete_event", title: "Delete event", description: "Permanently delete an event at any stage, its public page, results, team sites, entries, scores and votes. Participants and judges lose access; the name stays reserved unless nobody ever joined. No undo. Always explain this and confirm with the person first, then send body.confirm matching slug. Organiser only.", method: "DELETE", path: "/v1/hack/events/{slug}", body: true, destructive: true, public: true},
		{name: "hack_get_people", title: "List event people", description: "Read organiser, participant and judge members. Organiser only.", method: "GET", path: "/v1/hack/events/{slug}/people", idempotent: true},
		{name: "hack_remove_person", title: "Remove event person", description: "Remove a member and their event access. Ask first. Organiser only.", method: "DELETE", path: "/v1/hack/events/{slug}/people/{user_id}", destructive: true},
		{bodyDescription: "Set apply:false to preview placements of approved solo participants into teams with room, topped up to size (people per team, default min(4, the event's team size)), then new teams of that size. Locked teams are skipped. Show the plan and confirm before apply:true; the plan is recomputed using current membership.", name: "hack_fill_teams", title: "Fill teams", description: "Preview with body.apply:false. Show the plan and confirm before body.apply:true. Organiser only.", method: "POST", path: "/v1/hack/events/{slug}/teams/fill", body: true, destructive: true},
		{policy: toolPolicy{includeEmails: true, recordsPins: true, organiserResult: "teams"}, name: "hack_get_teams", title: "List event teams", description: "Read event teams and member names and IDs. Emails are omitted unless include_emails is true. Records any due submission deadline pins. Organiser only.", method: "GET", path: "/v1/hack/events/{slug}/teams", idempotent: true},
		{policy: toolPolicy{organiserResult: "member"}, bodyDescription: "user_id of the participant to move to the team named in the path.", name: "hack_move_member", title: "Move a member to team", description: "Move a member to the specified team using the REST body. Ask first. Organiser only.", method: "POST", path: "/v1/hack/events/{slug}/teams/{team}/members", body: true, destructive: true},
		{name: "hack_remove_team_member", title: "Remove a team member", description: "Take a person off a team. Ask first. Organiser only.", method: "DELETE", path: "/v1/hack/events/{slug}/teams/{team}/members/{user_id}", destructive: true},
		{bodyDescription: "name for the team's new display name.", name: "hack_rename_team", title: "Rename a team", description: "Rename an event team's display name with body.name. Its site slug and address stay the same. Organiser only.", method: "PATCH", path: "/v1/hack/events/{slug}/teams/{team}", body: true, destructive: true, public: true},
		{bodyDescription: "locked boolean.", name: "hack_lock_team", title: "Lock or unlock a team", description: "Body.locked true or false. Locking stops the team taking anyone new by request, link, code or invite, and clears its pending requests and invites. Ask first, and say those are cleared. Organiser only.", method: "PUT", path: "/v1/hack/events/{slug}/teams/{team}/lock", body: true, destructive: true, idempotent: true},
		{bodyDescription: "admin boolean for the user_id in the path.", name: "hack_set_team_admin", title: "Make or remove a team admin", description: "Body.admin true or false for the member in the path. Destructive: it changes who can approve requests, lock, rename, remove members and rotate the link. The last admin can't be unmade until someone else is admin. Ask first. Organiser only.", method: "PUT", path: "/v1/hack/events/{slug}/teams/{team}/members/{user_id}/admin", body: true, destructive: true, idempotent: true},
		{name: "hack_new_team_link", title: "Make a new team link", description: "Destructive: the team's old link and code stop working at once. Ask first. Organiser only.", method: "POST", path: "/v1/hack/events/{slug}/teams/{team}/link", destructive: true},
		{name: "hack_delete_team", title: "Delete a team", description: "Delete a team and its event entry. Ask first. Organiser only.", method: "DELETE", path: "/v1/hack/events/{slug}/teams/{team}", destructive: true, public: true},
		{policy: toolPolicy{organiserResult: "team"}, bodyDescription: "deadline in the event time zone, or an empty string to remove the extension.", name: "hack_set_team_deadline", title: "Set team deadline", description: "Give one team more time with body.deadline. Ask first. Organiser only.", method: "PUT", path: "/v1/hack/events/{slug}/teams/{team}/deadline", body: true, destructive: true, idempotent: true},
		{policy: toolPolicy{organiserResult: "team"}, name: "hack_take_down_team_site", title: "Take down team site", description: "Take a team's public site down. Ask first. Organiser only.", method: "POST", path: "/v1/hack/events/{slug}/teams/{team}/takedown", destructive: true, idempotent: true, public: true},
		{policy: toolPolicy{organiserResult: "team"}, name: "hack_restore_team_site", title: "Restore team site", description: "Restore a team's public site. Ask first. Organiser only.", method: "POST", path: "/v1/hack/events/{slug}/teams/{team}/restore", destructive: true, idempotent: true, public: true},
		{policy: toolPolicy{analytics: analyticsPolicy{secretText: true, secretURL: true}},
			name: "hack_create_organiser_invite", title: "Invite co-organiser", description: "Create a co-organiser invitation link. Anyone with it can become an organiser until it is revoked, replaced or expires in 7 days. Ask first before sharing it. Organiser only.", method: "POST", path: "/v1/hack/events/{slug}/organiser-invite", destructive: true},
		{name: "hack_revoke_organiser_invite", title: "Revoke co-organiser invite", description: "Revoke the current co-organiser invitation link; people who already accepted stay organisers. Ask first. Organiser only.", method: "DELETE", path: "/v1/hack/events/{slug}/organiser-invite", destructive: true, idempotent: true},
		{name: "hack_preview_organiser_invite", title: "Preview co-organiser invite", description: "Read a co-organiser invitation before accepting it.", method: "GET", path: "/v1/hack/organiser/{code}", idempotent: true},
		{bodyDescription: "accept_coc true; optional display_name. Only when the person's account has no name yet (the preview shows account_name empty): ask once; it becomes their account name, shown in every event. Leave out otherwise.", name: "hack_accept_organiser_invite", title: "Accept co-organiser invite", description: "Accept the invitation with body.accept_coc. Ask the person first. Ask for body.display_name only when the preview account_name is empty.", method: "POST", path: "/v1/hack/organiser/{code}", body: true, destructive: true},
		{name: "hack_remove_organiser", title: "Remove co-organiser", description: "Remove a co-organiser. Also revokes the current invitation link. Ask first. Organiser only.", method: "DELETE", path: "/v1/hack/events/{slug}/organisers/{user_id}", destructive: true},
		{policy: toolPolicy{analytics: analyticsPolicy{fileResult: true}},
			name: "hack_export_participants", title: "Export participants", description: "Read the participant CSV, which adds city, country, age_range, gender and joined_via columns. Organiser only; keep private.", method: "GET", path: "/v1/hack/events/{slug}/export/participants.csv", idempotent: true, csv: true},
		{policy: toolPolicy{analytics: analyticsPolicy{fileResult: true}},
			name: "hack_export_teams", title: "Export teams", description: "Read the team CSV. Organiser only; keep private.", method: "GET", path: "/v1/hack/events/{slug}/export/teams.csv", idempotent: true, csv: true},
		{policy: toolPolicy{analytics: analyticsPolicy{secretText: true, secretURL: true}},
			name: "hack_export_projects_archive", title: "Export team site archive", description: "Create a short-lived private download link for the complete team site archive. Organiser only; give the link only to the person.", method: "POST", path: "/v1/hack/events/{slug}/export/projects-link", archive: true},
		{policy: toolPolicy{analytics: analyticsPolicy{secretText: true, secretURL: true}},
			name: "hack_export_own_team_archive", title: "Export own team archive", description: "Create a short-lived private download link for your team's complete archive; give the link only to the person.", method: "POST", path: "/v1/hack/events/{slug}/export/own-team-link", archive: true},
		{name: "hack_get_usage", title: "Read event usage", description: "Read event storage and traffic usage. Organiser only.", method: "GET", path: "/v1/hack/events/{slug}/usage", idempotent: true},
		{bodyDescription: "team_ids (1 to 25 distinct team ids of this event) and text (1 to 1000 characters).", name: "hack_message_team", title: "Message teams", description: "Send a message to one or more teams, in-app and by email, from the organisers. Ask first: it emails people. At most 50 team messages per event per rolling hour. Organiser only.", method: "POST", path: "/v1/hack/events/{slug}/team-messages", body: true, destructive: true, public: true},
		{name: "hack_get_team_messages", title: "Read a team's messages", description: "Read one team's thread with the organisers, newest first: messages the organisers sent the team and messages the team wrote to the organisers (from_team is true for those). Reading it clears the team's unread notice on Home. Organiser only.", method: "GET", path: "/v1/hack/events/{slug}/teams/{team}/messages", idempotent: true},
		{name: "hack_get_reminder_settings", title: "Read reminder settings", description: "Read the deadline reminder offsets (minutes before a team's deadline and before judging closes), the allowed range, and when judging closes. Organiser only.", method: "GET", path: "/v1/hack/events/{slug}/reminders", idempotent: true},
		{bodyDescription: "offsets_minutes: 0 to 5 distinct minute values, each 15 to 20160 (14 days). Teams without a submitted entry, and judges with unscored teams once judging_ends_at is set, are emailed this long before their deadline.", name: "hack_set_reminder_settings", title: "Set reminder settings", description: "Replace the reminder offsets with body.offsets_minutes. Ask before overwriting. Organiser only.", method: "PUT", path: "/v1/hack/events/{slug}/reminders", body: true, destructive: true, idempotent: true},
	}
}

func runHackRoute(c *mcp.Call, route hackRouteTool, args map[string]any) (mcp.Output, error) {
	path, err := routePath(route.path, args)
	if err != nil {
		return mcp.Output{}, err
	}
	if route.policy.judgeParameter {
		judgeID, _, err := presentString(args, "judge_user_id")
		if err != nil {
			return mcp.Output{}, err
		}
		if judgeID != "" {
			path += "?judge_user_id=" + url.QueryEscape(judgeID)
		}
	}
	raw, err := routeBody(route, args)
	if err != nil {
		return mcp.Output{}, err
	}
	includeEmails, err := mcp.BoolArg(args, "include_emails", false)
	if err != nil {
		return mcp.Output{}, err
	}
	res := c.Do(route.method, path, raw, nil)
	if !res.OK() {
		return mcp.Output{}, hackRestError(route.name, res)
	}
	return routeOutput(c, route, args, res, includeEmails)
}

func routePath(path string, args map[string]any) (string, error) {
	for _, m := range pathParam.FindAllStringSubmatch(path, -1) {
		value, err := mcp.StringArg(args, m[1])
		if err != nil {
			return "", err
		}
		if m[1] == "slug" {
			value = strings.ToLower(value)
		}
		path = strings.ReplaceAll(path, m[0], url.PathEscape(value))
	}
	return path, nil
}

func routeBody(route hackRouteTool, args map[string]any) ([]byte, error) {
	_, hasBody := args["body"]
	if !route.body || (!hasBody && route.policy.optionalBody) {
		return nil, nil
	}
	body, ok := args["body"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("body must be a JSON object")
	}
	return json.Marshal(body)
}

func routeOutput(c *mcp.Call, route hackRouteTool, args map[string]any, res mcp.UpstreamResult, includeEmails bool) (mcp.Output, error) {
	switch {
	case route.csv:
		return hackCSVOutput(fmt.Sprint(args["slug"]), strings.TrimPrefix(route.name, "hack_export_"), res)
	case route.archive:
		var link map[string]any
		if err := json.Unmarshal(res.Body(), &link); err != nil {
			return mcp.Output{}, fmt.Errorf("%s: server returned invalid JSON: %w", route.name, err)
		}
		return mcp.Output{Text: mcp.JSONText(link), Structured: link}, nil
	case route.policy.screenshot:
		structured := map[string]any{"content_type": res.Header().Get("Content-Type"), "content_base64": base64.StdEncoding.EncodeToString(res.Body())}
		return mcp.Output{Text: "Screenshot image in structured result.", Structured: structured}, nil
	}
	var response any
	if len(res.Body()) > 0 {
		if err := json.Unmarshal(res.Body(), &response); err != nil {
			return mcp.Output{}, fmt.Errorf("%s: server returned invalid JSON: %w", route.name, err)
		}
	}
	if obj, ok := response.(map[string]any); ok {
		minimiseHackOrganiserResult(c, includeEmails, obj)
	}
	structured := map[string]any{"response": response, "status": res.Status()}
	return mcp.Output{Text: mcp.JSONText(structured), Structured: structured}, nil
}

// hackSetEntryExcludedTool is a toggle over the website's two REST actions
// (POST .../entry/exclude and .../include), chosen by the excluded bool.
