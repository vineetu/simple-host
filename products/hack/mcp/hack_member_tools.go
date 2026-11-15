// famHackEvents member declarations and shared member REST adapters.
package hackmcp

import (
	"encoding/json"
	"fmt"
	"github.com/vsriram/simple-host/internal/mcp"
	"net/http"
	"net/url"
	"strings"
)

// HackMemberTools exposes participant and judge REST actions to a personal
// event connection. The REST handlers remain the authority for every rule.
func HackMemberTools() []mcp.Tool {
	return append(hackMemberEventTools(), hackMemberEntryTools()...)
}

func hackMemberEventTools() []mcp.Tool {
	slug := map[string]any{"slug": mcp.Str("Event name.")}
	code := map[string]any{"code": mcp.Str("Code from the join or judge link.")}
	with := func(base map[string]any, fields map[string]any) map[string]any {
		p := map[string]any{}
		for k, v := range base {
			p[k] = v
		}
		for k, v := range fields {
			p[k] = v
		}
		return p
	}
	return []mcp.Tool{
		memberTool("hack_list_open_teams", "List team finding", "Read every unlocked team (each with a full flag), opted-in solo participants and your pending requests and invites. Approved participant or organiser only.", "GET", "/v1/hack/events/{slug}/teams/open", mcp.Object(with(slug, map[string]any{}), "slug"), nil, mcp.ReadOnly()),
		memberTool("hack_ask_to_join", "Ask to join a team", "Ask the person before sending a join request. An existing invite joins them immediately; a repeated request stays pending. Refused when the team is locked.", "POST", "/v1/hack/events/{slug}/teams/{team}/requests", mcp.Object(with(slug, map[string]any{"team": mcp.Str("Team slug.")}), "slug", "team"), nil, mcp.Writes(true, false, false)),
		memberTool("hack_invite_to_team", "Invite a solo participant", "Invite a listed solo participant to your team. Ask first. An existing request joins them immediately; a repeated invite stays pending. Refused when the team is locked.", "POST", "/v1/hack/events/{slug}/team/invites", mcp.Object(with(slug, map[string]any{"user_id": mcp.Str("Solo participant ID.")}), "slug", "user_id"), []string{"user_id"}, mcp.Writes(true, false, false)),
		memberTool("hack_accept_team_request", "Accept a request or invite", "Accept an incoming request (team admins only) as a teammate, or your own invitation. This joins the participant. Confirm first.", "POST", "/v1/hack/events/{slug}/requests/{id}/accept", mcp.Object(with(slug, map[string]any{"id": mcp.Str("Request or invite ID.")}), "slug", "id"), nil, mcp.Writes(true, false, false)),
		memberTool("hack_decline_team_request", "Decline a request or invite", "Decline an incoming request (team admins only) or your own invitation. Ask first.", "POST", "/v1/hack/events/{slug}/requests/{id}/decline", mcp.Object(with(slug, map[string]any{"id": mcp.Str("Request or invite ID.")}), "slug", "id"), nil, mcp.Writes(true, false, false)),
		memberTool("hack_withdraw_team_request", "Withdraw a request or cancel invite", "Withdraw your request, or cancel an invitation sent by your team. Ask first.", "DELETE", "/v1/hack/events/{slug}/requests/{id}", mcp.Object(with(slug, map[string]any{"id": mcp.Str("Request or invite ID.")}), "slug", "id"), nil, mcp.Writes(true, false, false)),
		memberTool("hack_remove_teammate", "Remove a teammate", "Take another member off your team. Admins only. Their team access stops and the code changes. Confirm first.", "DELETE", "/v1/hack/events/{slug}/team/members/{user_id}", mcp.Object(with(slug, map[string]any{"user_id": mcp.Str("Teammate ID.")}), "slug", "user_id"), nil, mcp.Writes(true, false, false)),
		memberTool("hack_set_looking", "List or unlist me as looking", "Opt in or out of the solo list with a short intro. Ask the person first.", "PUT", "/v1/hack/events/{slug}/me/looking", mcp.Object(with(slug, map[string]any{"looking": map[string]any{"type": "boolean"}, "intro": mcp.Str("One line, up to 120 characters.")}), "slug", "looking", "intro"), []string{"looking", "intro"}, mcp.Writes(true, true, false)),
		memberTool("hack_preview_team_link", "Read a team link", "Use when the person pastes a team link. Shows the team and whether they can join. Then ask, and call hack_join_team with the event slug and code.", http.MethodGet, "/v1/hack/team-links/{code}", mcp.Object(map[string]any{"code": mcp.Str("Team code, or the whole team link.")}, "code"), nil, mcp.ReadOnly()),
		memberTool("hack_update_my_team", "Rename my team or edit Looking for", "Change your team's name or its one-line looking-for note. Send only what changes. Admins only. Ask first.", http.MethodPatch, "/v1/hack/events/{slug}/team", mcp.Object(with(slug, map[string]any{"name": mcp.Str("New team name, 1 to 80 characters."), "looking_for": mcp.Str("One line, up to 120 characters.")}), "slug"), []string{"name", "looking_for"}, mcp.Writes(false, true, false)),
		memberTool("hack_lock_my_team", "Lock or unlock my team", "Admins only. Locking stops the team taking anyone new by request, link, code or invite, and clears its pending requests and invites. Ask first, and say those are cleared.", http.MethodPut, "/v1/hack/events/{slug}/team/lock", mcp.Object(with(slug, map[string]any{"locked": map[string]any{"type": "boolean"}}), "slug", "locked"), []string{"locked"}, mcp.Writes(true, true, false)),
		memberTool("hack_set_teammate_admin", "Make or remove a team admin", "Admins only. Destructive: it changes who can approve requests, lock, rename, remove members and rotate the link. The last admin can't be unmade until someone else is admin. Ask first.", http.MethodPut, "/v1/hack/events/{slug}/team/members/{user_id}/admin", mcp.Object(with(slug, map[string]any{"user_id": mcp.Str("Teammate ID."), "admin": map[string]any{"type": "boolean"}}), "slug", "user_id", "admin"), []string{"admin"}, mcp.Writes(true, true, false)),
		memberTool("hack_new_my_team_link", "Make a new team link", "Admins only. Destructive: the old link and code stop working at once. Ask first.", http.MethodPost, "/v1/hack/events/{slug}/team/link", mcp.Object(slug, "slug"), nil, mcp.Writes(true, false, false)),
		memberTool("hack_preview_join", "Read a participant join link", "Read the event, code of conduct, signup questions, and whether joining is open.", http.MethodGet, "/v1/hack/join/{code}", mcp.Object(code, "code"), nil, mcp.ReadOnly()),
		withToolPolicy(memberTool("hack_join_event", "Join an event", "Join as a participant. Needs a join-link code. Without one, use hack_apply_to_event. Ask the person to accept the displayed code of conduct and provide signup answers first. Ask for a name only when the preview account_name is empty.", http.MethodPost, "/v1/hack/join/{code}", mcp.Object(with(code, map[string]any{"accept_coc": map[string]any{"type": "boolean", "description": "true once the person has read the code of conduct shown by the preview tool and agreed to it."}, "display_name": mcp.Str("Only when the person's account has no name yet (the preview shows account_name empty): ask once; it becomes their account name, shown in every event. Leave out otherwise."), "answers": map[string]any{"type": "object", "description": "Answers keyed by signup question ID.", "additionalProperties": true}}), "code", "accept_coc"), []string{"accept_coc", "display_name", "answers"}, mcp.Writes(false, false, false)), toolPolicy{memberResult: "me"}),
		memberTool("hack_preview_application", "Check whether an event takes applications", "For an event found with hack_get_directory or by its address, when the person has no join link. Reads whether applications are open, the optional fields (city, country code, age range, gender), the organiser's sign-up questions and code of conduct, and the person's own status if they already applied.", http.MethodGet, "/v1/hack/events/{slug}/apply", mcp.Object(slug, "slug"), nil, mcp.ReadOnly()),
		withToolPolicy(memberTool("hack_apply_to_event", "Apply to join an event", "Apply as a participant without a join link. Show the code of conduct from hack_preview_application and ask the person to agree, ask for a name only when account_name is empty, ask the sign-up questions, and offer the optional city, country, age range and gender; never guess or fill those in yourself, and leave them empty if the person does not answer. Tell them only the organisers see the answers. If they choose under_18, tell them a parent or guardian must agree to the Terms (https://simple-hack.app/terms). Every application waits for an organiser; check with hack_application_status. Use hack_join_event instead when the person has a join link.", http.MethodPost, "/v1/hack/events/{slug}/apply", mcp.Object(with(slug, map[string]any{
			"accept_coc":   map[string]any{"type": "boolean", "description": "true once the person has read and agreed to the code of conduct."},
			"display_name": mcp.Str("Only when the person's account has no name yet (the preview shows account_name empty): ask once; it becomes their account name, shown in every event. Leave out otherwise."),
			"city":         mcp.Str("Optional. City the person is based in, up to 80 characters."),
			"country":      map[string]any{"type": "string", "description": "Optional. ISO 3166-1 alpha-2 country code, e.g. ID, GB, US."},
			"age_range":    map[string]any{"type": "string", "enum": []string{"", "under_18", "18_24", "25_34", "35_plus"}, "description": "Optional."},
			"gender":       map[string]any{"type": "string", "enum": []string{"", "woman", "man", "non_binary", "another", "prefer_not_to_say"}, "description": "Optional."},
			"answers":      map[string]any{"type": "object", "description": "Answers keyed by signup question ID.", "additionalProperties": true},
		}), "slug", "accept_coc"), []string{"accept_coc", "display_name", "city", "country", "age_range", "gender", "answers"}, mcp.Writes(false, false, false)), toolPolicy{memberResult: "me", analytics: analyticsPolicy{applicationAnswers: true}}),
		withToolPolicy(memberTool("hack_application_status", "Read my application status", "Read your participant application approval status and team information. Records any due submission deadline pins.", http.MethodGet, "/v1/hack/events/{slug}", mcp.Object(slug, "slug"), nil, mcp.Writes(false, true, false)), toolPolicy{memberResult: "me"}),
		memberTool("hack_create_team", "Create my team", "Create a team and join it. You become the team's admin. Ask the person before creating a team.", http.MethodPost, "/v1/hack/events/{slug}/teams", mcp.Object(with(slug, map[string]any{"name": mcp.Str("Team name."), "looking_for": mcp.Str("One line, up to 120 characters.")}), "slug", "name"), []string{"name", "looking_for"}, mcp.Writes(false, false, false)),
		memberTool("hack_join_team", "Join a team", "Join a team with its code or team link. Ask the person before joining. Refused when the team is locked or full.", http.MethodPost, "/v1/hack/events/{slug}/teams/join", mcp.Object(with(slug, map[string]any{"code": mcp.Str("Team code, or the whole team link.")}), "slug", "code"), []string{"code"}, mcp.Writes(false, false, false)),
		memberTool("hack_leave_team", "Leave my team", "Leave the current team. Confirm first: the team's access and membership change.", http.MethodPost, "/v1/hack/events/{slug}/teams/leave", mcp.Object(slug, "slug"), nil, mcp.Writes(true, false, false)),
		memberTool("hack_choose_track", "Choose my team's track", "Choose a listed track for the current team; an empty track clears the choice. An organiser can supply team (team slug) before Judging to fix a missing panel track. Confirm first.", http.MethodPut, "/v1/hack/events/{slug}/team/track", mcp.Object(with(slug, map[string]any{"track": mcp.Str("Track slug, or empty to clear."), "team": mcp.Str("Organiser only: team slug to update before Judging.")}), "slug", "track"), []string{"track", "team"}, mcp.Writes(true, true, false)),
		withToolPolicy(memberTool("hack_get_team_status", "Read my team status", "Read this participant's team (its link, admins and whether you are one, and whether it is locked), site status, track and deadline. Records any due submission deadline pins.", http.MethodGet, "/v1/hack/events/{slug}", mcp.Object(slug, "slug"), nil, mcp.Writes(false, true, false)), toolPolicy{memberResult: "me"}),
		memberTool("hack_get_my_teams", "Read my teams and sites", "List current teams and their site addresses for this person.", http.MethodGet, "/v1/hack/my-teams", mcp.NoArgs(), nil, mcp.ReadOnly()),
		memberTool("hack_get_my_team_messages", "Read my team's messages with the organisers", "Read your team's thread with the organisers, newest first: what the organisers sent the team and what any teammate wrote to them (from_team is true for those). Empty when you have no team.", http.MethodGet, "/v1/hack/events/{slug}/team/messages", mcp.Object(slug, "slug"), nil, mcp.ReadOnly()),
		memberTool("hack_message_organisers", "Message the organisers", "Write to the event's organisers as a member of your team, up to 1000 characters. Every organiser sees it and so do your teammates; replies arrive in hack_get_my_team_messages. At most 10 per team per hour. Ask the person for the wording first.", http.MethodPost, "/v1/hack/events/{slug}/team/messages", mcp.Object(with(slug, map[string]any{"text": mcp.Str("The message, 1 to 1000 characters.")}), "slug", "text"), []string{"text"}, mcp.Writes(false, false, false)),
		memberTool("hack_get_my_todos", "Read what waits on me", "List what waits on this person in every event they are in: join requests for a team they admin, invites, applications waiting for an organiser, and news such as an application answer, a team site taken down, a deadline extension or being made team admin. Each item has the same text the website shows. Also says whether they get these by email. Start here when the person asks what needs them.", http.MethodGet, "/v1/hack/todos", mcp.NoArgs(), nil, mcp.ReadOnly()),
		memberTool("hack_dismiss_notices", "Dismiss news lines", "Mark this person's own news lines in one event as seen, the given ids from hack_get_my_todos or every line when ids is left out. Live to-dos (requests, invites, applications) cannot be dismissed, only answered. Ask first.", http.MethodPost, "/v1/hack/events/{slug}/notices/seen", mcp.Object(with(slug, map[string]any{"ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Notice ids (the id of an item with dismissable true). Leave out to dismiss every news line in the event."}}), "slug"), []string{"ids"}, mcp.Writes(false, true, false)),
		memberTool("hack_set_email_updates", "Turn emails about what waits on me on or off", "Turn on or off the emails about join requests, invites, application answers and changes to the person's team or its site (organisers also hear about new applications). At most one email of each kind every 15 minutes. An account without an email address never gets any. Ask first.", http.MethodPatch, "/v1/hack/preferences", mcp.Object(map[string]any{"email_updates": map[string]any{"type": "boolean"}}, "email_updates"), []string{"email_updates"}, mcp.Writes(false, true, false)),
		memberTool("hack_preview_judge", "Read a judge join link", "Read the event and code of conduct before joining as a judge.", http.MethodGet, "/v1/hack/judge/{code}", mcp.Object(code, "code"), nil, mcp.ReadOnly()),
		withToolPolicy(memberTool("hack_join_judge", "Join as a judge", "Join as a judge. Ask the person to accept the displayed code of conduct first. Ask for a name only when the preview account_name is empty.", http.MethodPost, "/v1/hack/judge/{code}", mcp.Object(with(code, map[string]any{"accept_coc": map[string]any{"type": "boolean", "description": "true once the person has read the code of conduct shown by the preview tool and agreed to it."}, "display_name": mcp.Str("Only when the person's account has no name yet (the preview shows account_name empty): ask once; it becomes their account name, shown in every event. Leave out otherwise.")}), "code", "accept_coc"), []string{"accept_coc", "display_name"}, mcp.Writes(false, false, false)), toolPolicy{memberResult: "me"}),
		withToolPolicy(memberTool("hack_preview_leave_event", "Check what leaving an event does", "Read what leaving would do for this person before asking them: their case (participant, applicant, judge, co_organiser, creator, last_organiser), whether it is blocked and why, whether their team would be deleted (empty, before its deadline) or instead kept exactly as it is -- entry, site and scores untouched, just taken off its member list -- once past the team's deadline or the event is closed, judging or results, scores that stay, who can take over, and the end or delete choice for the only organiser.", http.MethodGet, "/v1/hack/events/{slug}/leave", mcp.Object(slug, "slug"), nil, mcp.ReadOnly()), toolPolicy{memberResult: "successors"}),
		memberTool("hack_leave_event", "Leave an event", "Destructive, for the signed-in person only. Call hack_preview_leave_event first, tell the person exactly what happens (team deleted if it would be left empty before its deadline; otherwise, once past the deadline or the event closed/judging/results, the team stays with its entry, site and scores untouched and the person just comes off its member list; access lost; event ended or deleted) and ask them before calling. confirm is the event's address name. The event creator sends successor_user_id from the preview. The only organiser sends close: end or delete, chosen by the person. Not possible on an ended event.", http.MethodPost, "/v1/hack/events/{slug}/leave", mcp.Object(with(slug, map[string]any{
			"confirm":           mcp.Str("The event's address name, sent only after the person agreed to leave."),
			"successor_user_id": mcp.Str("Creator only: user_id of another organiser from the preview."),
			"close":             map[string]any{"type": "string", "enum": []string{"end", "delete"}, "description": "Only organiser: end keeps the page and results, delete removes everything. Ask the person."},
		}), "slug", "confirm"), []string{"confirm", "successor_user_id", "close"}, mcp.Writes(true, false, true)),
	}
}

func memberTool(name, title, description, method, path string, input map[string]any, bodyKeys []string, annotations map[string]any) mcp.Tool {
	schema := map[string]any{"type": "object", "additionalProperties": true}
	if name == "hack_get_submissions" {
		schema["properties"] = map[string]any{"submissions": mcp.OutArray("Submitted entry summary cards.", map[string]any{"type": "object", "additionalProperties": true})}
		schema["required"] = []string{"submissions"}
	}
	if name == "hack_get_team_status" {
		schema["properties"] = map[string]any{"can": hackAbilitiesSchema(), "checklist": checklistNullableObject(hackChecklistSchema())}
	}
	if name == "hack_get_navigation" {
		schema = mcp.OutObject(map[string]any{"roles": mcp.OutArray("Roles.", map[string]any{"type": "object", "additionalProperties": true}), "resolution": map[string]any{"type": "object", "additionalProperties": true}, "version": mcp.OutInteger("Map version."), "screens": mcp.OutArray("Screens.", map[string]any{"type": "object", "additionalProperties": true}), "targets": mcp.OutArray("Destinations.", map[string]any{"type": "object", "additionalProperties": true}), "aliases": mcp.OutArray("Old destinations.", map[string]any{"type": "object", "additionalProperties": true})}, "version", "screens", "targets", "aliases", "roles", "resolution")
	}
	return decl{Name: name, Title: title, Description: description, InputSchema: input, OutputSchema: schema, Annotations: annotations, run: func(c *mcp.Call, args map[string]any) (mcp.Output, error) {
		p := path
		for _, key := range []string{"slug", "code", "team_id", "team", "id", "user_id"} {
			if strings.Contains(p, "{"+key+"}") {
				value, err := mcp.StringArg(args, key)
				if err != nil {
					return mcp.Output{}, err
				}
				if key == "slug" {
					value = strings.ToLower(value)
				}
				p = strings.ReplaceAll(p, "{"+key+"}", url.PathEscape(value))
			}
		}
		var raw []byte
		if bodyKeys != nil {
			body := map[string]any{}
			for _, key := range bodyKeys {
				if value, ok := args[key]; ok {
					body[key] = value
				}
			}
			var err error
			raw, err = json.Marshal(body)
			if err != nil {
				return mcp.Output{}, err
			}
		}
		res := c.Do(method, p, raw, nil)
		if !res.OK() {
			return mcp.Output{}, hackRestError(name, res)
		}
		if res.Status() == http.StatusNoContent {
			out := map[string]any{"ok": true}
			return mcp.Output{Text: mcp.JSONText(out), Structured: out}, nil
		}
		var out map[string]any
		if err := json.Unmarshal(res.Body(), &out); err != nil || out == nil {
			return mcp.Output{}, fmt.Errorf("%s: server returned invalid JSON", name)
		}
		minimiseHackMemberResult(c, out)
		return mcp.Output{Text: mcp.JSONText(out), Structured: out}, nil
	}}.tool()
}
