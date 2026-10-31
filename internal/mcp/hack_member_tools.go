package mcp

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// HackMemberTools exposes participant and judge REST actions to a personal
// event connection. The REST handlers remain the authority for every rule.
func HackMemberTools() []Tool {
	slug := map[string]any{"slug": str("Event name.")}
	code := map[string]any{"code": str("Code from the join or judge link.")}
	teamID := map[string]any{"team_id": str("Team ID from the judge queue.")}
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
	list := []Tool{
		memberTool("hack_list_open_teams", "List team finding", "Read open teams, opted-in solo participants and your pending requests and invites. Approved participant or organiser only.", "GET", "/v1/hack/events/{slug}/teams/open", object(with(slug, map[string]any{}), "slug"), nil, readOnly()),
		memberTool("hack_ask_to_join", "Ask to join a team", "Ask the person before sending a join request. An existing invite joins them immediately; a repeated request stays pending.", "POST", "/v1/hack/events/{slug}/teams/{team}/requests", object(with(slug, map[string]any{"team": str("Team slug.")}), "slug", "team"), nil, writes(true, false, false)),
		memberTool("hack_invite_to_team", "Invite a solo participant", "Invite a listed solo participant to your team. Ask first. An existing request joins them immediately; a repeated invite stays pending.", "POST", "/v1/hack/events/{slug}/team/invites", object(with(slug, map[string]any{"user_id": str("Solo participant ID.")}), "slug", "user_id"), []string{"user_id"}, writes(true, false, false)),
		memberTool("hack_accept_team_request", "Accept a request or invite", "Accept an incoming request as a teammate, or your own invitation. This joins the participant. Confirm first.", "POST", "/v1/hack/events/{slug}/requests/{id}/accept", object(with(slug, map[string]any{"id": str("Request or invite ID.")}), "slug", "id"), nil, writes(true, false, false)),
		memberTool("hack_decline_team_request", "Decline a request or invite", "Decline an incoming request or your own invitation. Ask first.", "POST", "/v1/hack/events/{slug}/requests/{id}/decline", object(with(slug, map[string]any{"id": str("Request or invite ID.")}), "slug", "id"), nil, writes(true, false, false)),
		memberTool("hack_withdraw_team_request", "Withdraw a request or cancel invite", "Withdraw your request, or cancel an invitation sent by your team. Ask first.", "DELETE", "/v1/hack/events/{slug}/requests/{id}", object(with(slug, map[string]any{"id": str("Request or invite ID.")}), "slug", "id"), nil, writes(true, false, false)),
		memberTool("hack_remove_teammate", "Remove a teammate", "Take another member off your team. Their team access stops and the code changes. Confirm first.", "DELETE", "/v1/hack/events/{slug}/team/members/{user_id}", object(with(slug, map[string]any{"user_id": str("Teammate ID.")}), "slug", "user_id"), nil, writes(true, false, false)),
		memberTool("hack_set_team_finding", "Set team finding", "Set who can join and the one-line looking-for note for your team. Ask before overwriting.", "PUT", "/v1/hack/events/{slug}/team/finding", object(with(slug, map[string]any{"open": map[string]any{"type": "boolean"}, "looking_for": str("One line, up to 120 characters.")}), "slug", "open", "looking_for"), []string{"open", "looking_for"}, writes(true, true, false)),
		memberTool("hack_set_looking", "List or unlist me as looking", "Opt in or out of the solo list with a short intro. Ask the person first.", "PUT", "/v1/hack/events/{slug}/me/looking", object(with(slug, map[string]any{"looking": map[string]any{"type": "boolean"}, "intro": str("One line, up to 120 characters.")}), "slug", "looking", "intro"), []string{"looking", "intro"}, writes(true, true, false)),
		memberTool("hack_preview_join", "Read a participant join link", "Read the event, code of conduct, signup questions, and whether joining is open.", http.MethodGet, "/v1/hack/join/{code}", object(code, "code"), nil, readOnly()),
		memberTool("hack_join_event", "Join an event", "Join as a participant. Needs a join-link code. Without one, use hack_apply_to_event. Ask the person to accept the displayed code of conduct and provide their display name and signup answers first.", http.MethodPost, "/v1/hack/join/{code}", object(with(code, map[string]any{"accept_coc": map[string]any{"type": "boolean", "description": "true once the person has read the code of conduct shown by the preview tool and agreed to it."}, "display_name": str("Name shown to others."), "answers": map[string]any{"type": "object", "description": "Answers keyed by signup question ID.", "additionalProperties": true}}), "code", "accept_coc", "display_name"), []string{"accept_coc", "display_name", "answers"}, writes(false, false, false)),
		memberTool("hack_preview_application", "Check whether an event takes applications", "For an event found with hack_get_directory or by its address, when the person has no join link. Reads whether applications are open, the optional fields (city, country code, age range, gender), the organiser's sign-up questions and code of conduct, and the person's own status if they already applied.", http.MethodGet, "/v1/hack/events/{slug}/apply", object(slug, "slug"), nil, readOnly()),
		memberTool("hack_apply_to_event", "Apply to join an event", "Apply as a participant without a join link. Show the code of conduct from hack_preview_application and ask the person to agree, ask their display name and the sign-up questions, and offer the optional city, country, age range and gender; never guess or fill those in yourself, and leave them empty if the person does not answer. Tell them only the organisers see the answers. If they choose under_18, tell them a parent or guardian must agree to the Terms (https://simple-hack.app/terms). Every application waits for an organiser; check with hack_application_status. Use hack_join_event instead when the person has a join link.", http.MethodPost, "/v1/hack/events/{slug}/apply", object(with(slug, map[string]any{
			"accept_coc":   map[string]any{"type": "boolean", "description": "true once the person has read and agreed to the code of conduct."},
			"display_name": str("Name shown to the organisers, and to teammates once approved."),
			"city":         str("Optional. City the person is based in, up to 80 characters."),
			"country":      map[string]any{"type": "string", "description": "Optional. ISO 3166-1 alpha-2 country code, e.g. ID, GB, US."},
			"age_range":    map[string]any{"type": "string", "enum": []string{"", "under_18", "18_24", "25_34", "35_plus"}, "description": "Optional."},
			"gender":       map[string]any{"type": "string", "enum": []string{"", "woman", "man", "non_binary", "another", "prefer_not_to_say"}, "description": "Optional."},
			"answers":      map[string]any{"type": "object", "description": "Answers keyed by signup question ID.", "additionalProperties": true},
		}), "slug", "accept_coc", "display_name"), []string{"accept_coc", "display_name", "city", "country", "age_range", "gender", "answers"}, writes(false, false, false)),
		memberTool("hack_application_status", "Read my application status", "Read your participant application approval status and team information. Records any due submission deadline pins.", http.MethodGet, "/v1/hack/events/{slug}", object(slug, "slug"), nil, writes(false, true, false)),
		memberTool("hack_create_team", "Create my team", "Create a team and join it. Ask the person before creating a team.", http.MethodPost, "/v1/hack/events/{slug}/teams", object(with(slug, map[string]any{"name": str("Team name."), "open": map[string]any{"type": "boolean", "description": "Anyone can ask to join. Defaults to true."}, "looking_for": str("One line, up to 120 characters.")}), "slug", "name"), []string{"name", "open", "looking_for"}, writes(false, false, false)),
		memberTool("hack_join_team", "Join a team", "Join a team with its private team code. Ask the person before joining.", http.MethodPost, "/v1/hack/events/{slug}/teams/join", object(with(slug, map[string]any{"code": str("Private team code.")}), "slug", "code"), []string{"code"}, writes(false, false, false)),
		memberTool("hack_leave_team", "Leave my team", "Leave the current team. Confirm first: the team's access and membership change.", http.MethodPost, "/v1/hack/events/{slug}/teams/leave", object(slug, "slug"), nil, writes(true, false, false)),
		memberTool("hack_choose_track", "Choose my team's track", "Choose a listed track for the current team; an empty track clears the choice. Confirm with the team first.", http.MethodPut, "/v1/hack/events/{slug}/team/track", object(with(slug, map[string]any{"track": str("Track slug, or empty to clear.")}), "slug", "track"), []string{"track"}, writes(true, true, false)),
		memberTool("hack_get_entry", "Read my team's entry", "Read the entry, required fields, completeness and entry deadline.", http.MethodGet, "/v1/hack/events/{slug}/entry", object(slug, "slug"), nil, readOnly()),
		memberTool("hack_update_entry", "Update my team's entry", "Set only the entry fields supplied. Empty strings clear fields. Ask before overwriting team text.", http.MethodPut, "/v1/hack/events/{slug}/entry", object(with(slug, map[string]any{"title": str("Project title."), "tagline": str("Short summary."), "description": str("Project description."), "video_url": str("Video URL."), "updated_at": str("updated_at from the entry you read, or empty for a new entry. A conflict returns the newer entry; ask before retrying."), "code_url": str("Code URL.")}), "slug"), []string{"title", "tagline", "description", "video_url", "code_url", "updated_at"}, writes(true, true, true)),
		memberTool("hack_delete_entry_screenshot", "Remove my entry screenshot", "Remove the team's entry screenshot. Confirm first.", http.MethodDelete, "/v1/hack/events/{slug}/entry/screenshot", object(slug, "slug"), nil, writes(true, true, true)),
		memberTool("hack_get_team_status", "Read my team status", "Read this participant's team, site status, track and deadline. Records any due submission deadline pins.", http.MethodGet, "/v1/hack/events/{slug}", object(slug, "slug"), nil, writes(false, true, false)),
		memberTool("hack_get_my_teams", "Read my teams and sites", "List current teams and their site addresses for this person.", http.MethodGet, "/v1/hack/my-teams", noArgs(), nil, readOnly()),
		memberTool("hack_get_voting", "Read voting options", "Read voting status, eligible teams and published vote ranking when available.", http.MethodGet, "/v1/hack/events/{slug}/vote", object(slug, "slug"), nil, hackPublicReadOnly()),
		memberTool("hack_get_my_vote", "Read my vote", "Read this person's current vote.", http.MethodGet, "/v1/hack/events/{slug}/my-vote", object(slug, "slug"), nil, readOnly()),
		memberTool("hack_vote", "Cast or change my vote", "Vote for an eligible team. This can replace the person's previous vote. Confirm their choice first.", http.MethodPut, "/v1/hack/events/{slug}/vote", object(with(slug, map[string]any{"team": str("Team slug from voting options.")}), "slug", "team"), []string{"team"}, writes(true, true, true)),
		memberTool("hack_get_my_results", "Read my team's results", "Read this participant's published result and judge comments.", http.MethodGet, "/v1/hack/events/{slug}/my-results", object(slug, "slug"), nil, readOnly()),
		memberTool("hack_preview_judge", "Read a judge join link", "Read the event and code of conduct before joining as a judge.", http.MethodGet, "/v1/hack/judge/{code}", object(code, "code"), nil, readOnly()),
		memberTool("hack_join_judge", "Join as a judge", "Join as a judge. Ask the person to accept the displayed code of conduct first.", http.MethodPost, "/v1/hack/judge/{code}", object(with(code, map[string]any{"accept_coc": map[string]any{"type": "boolean", "description": "true once the person has read the code of conduct shown by the preview tool and agreed to it."}, "display_name": str("Name shown to others.")}), "code", "accept_coc", "display_name"), []string{"accept_coc", "display_name"}, writes(false, false, false)),
		memberTool("hack_get_judge_queue", "Read my judging queue", "List eligible entries, entry, pinned website link, conflicts and scoring status. Records any due submission deadline pins.", http.MethodGet, "/v1/hack/events/{slug}/judge/queue", object(slug, "slug"), nil, writes(false, true, false)),
		memberTool("hack_get_judge_scores", "Read my scores for a team", "Read this judge's scores and comment for one queue team.", http.MethodGet, "/v1/hack/events/{slug}/judge/scores/{team_id}", object(with(slug, teamID), "slug", "team_id"), nil, readOnly()),
		memberTool("hack_score_team", "Score a team", "Save one or more criterion scores and optionally a comment. Confirm the scores with the judge before saving.", http.MethodPut, "/v1/hack/events/{slug}/judge/scores/{team_id}", object(with(with(slug, teamID), map[string]any{"scores": map[string]any{"type": "array", "description": "One entry per criterion being scored: its criterion_id from the rubric and the points.", "items": object(map[string]any{"criterion_id": str("Criterion ID from rubric."), "points": map[string]any{"type": "integer", "description": "Points for this criterion, from 0 to its max_points in the rubric."}}, "criterion_id", "points")}, "comment": str("Judge comment; an empty string clears it.")}), "slug", "team_id"), []string{"scores", "comment"}, writes(true, true, false)),
		memberTool("hack_get_my_conflicts", "Read my conflicts", "List this judge's declared conflicts.", http.MethodGet, "/v1/hack/events/{slug}/conflicts", object(slug, "slug"), nil, readOnly()),
		memberTool("hack_declare_my_conflict", "Declare a conflict", "Declare that this judge has a conflict with a team; scores for it will be unavailable. Confirm first.", http.MethodPost, "/v1/hack/events/{slug}/conflicts", object(with(slug, teamID), "slug", "team_id"), []string{"team_id"}, writes(true, true, false)),
		memberTool("hack_remove_my_conflict", "Remove my conflict", "Remove this judge's conflict with a team. Confirm first.", http.MethodDelete, "/v1/hack/events/{slug}/conflicts/{team_id}", object(with(slug, teamID), "slug", "team_id"), nil, writes(true, true, false)),
		memberTool("hack_preview_leave_event", "Check what leaving an event does", "Read what leaving would do for this person before asking them: their case (participant, applicant, judge, co_organiser, creator, last_organiser), whether it is blocked and why, whether their team would be deleted (empty, before its deadline) or instead kept exactly as it is -- entry, site and scores untouched, just taken off its member list -- once past the team's deadline or the event is closed, judging or results, scores that stay, who can take over, and the end or delete choice for the only organiser.", http.MethodGet, "/v1/hack/events/{slug}/leave", object(slug, "slug"), nil, readOnly()),
		memberTool("hack_leave_event", "Leave an event", "Destructive, for the signed-in person only. Call hack_preview_leave_event first, tell the person exactly what happens (team deleted if it would be left empty before its deadline; otherwise, once past the deadline or the event closed/judging/results, the team stays with its entry, site and scores untouched and the person just comes off its member list; access lost; event ended or deleted) and ask them before calling. confirm is the event's address name. The event creator sends successor_user_id from the preview. The only organiser sends close: end or delete, chosen by the person. Not possible on an ended event.", http.MethodPost, "/v1/hack/events/{slug}/leave", object(with(slug, map[string]any{
			"confirm":           str("The event's address name, sent only after the person agreed to leave."),
			"successor_user_id": str("Creator only: user_id of another organiser from the preview."),
			"close":             map[string]any{"type": "string", "enum": []string{"end", "delete"}, "description": "Only organiser: end keeps the page and results, delete removes everything. Ask the person."},
		}), "slug", "confirm"), []string{"confirm", "successor_user_id", "close"}, writes(true, false, true)),
	}
	list = append(list, memberScreenshotTool("hack_get_entry_screenshot", http.MethodGet, "Read my team's screenshot", readOnly()))
	list = append(list, memberScreenshotTool("hack_set_entry_screenshot", http.MethodPut, "Upload my team's screenshot. Ask the person before replacing the current image.", writes(true, true, true)))
	return list
}

func memberTool(name, title, description, method, path string, input map[string]any, bodyKeys []string, annotations map[string]any) Tool {
	return Tool{Name: name, Title: title, Description: description, InputSchema: input, OutputSchema: map[string]any{"type": "object", "additionalProperties": true}, Annotations: annotations, run: func(c *call, args map[string]any) (output, error) {
		p := path
		for _, key := range []string{"slug", "code", "team_id", "team", "id", "user_id"} {
			if strings.Contains(p, "{"+key+"}") {
				value, err := stringArg(args, key)
				if err != nil {
					return output{}, err
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
				return output{}, err
			}
		}
		res := c.do(method, p, raw, nil)
		if !res.ok() {
			return output{}, hackRestError(name, res)
		}
		if res.status == http.StatusNoContent {
			out := map[string]any{"ok": true}
			return output{Text: jsonText(out), Structured: out}, nil
		}
		var out map[string]any
		if err := json.Unmarshal(res.body, &out); err != nil || out == nil {
			return output{}, fmt.Errorf("%s: server returned invalid JSON", name)
		}
		minimiseHackMemberResult(name, c.caller.accountUserID(), out)
		return output{Text: jsonText(out), Structured: out}, nil
	}}
}

func memberScreenshotTool(name, method, description string, annotations map[string]any) Tool {
	props := map[string]any{"slug": str("Event name.")}
	req := []string{"slug"}
	if method == http.MethodPut {
		props["base64"] = str("Base64 encoded PNG, JPEG or WebP, at most 2 MB decoded.")
		req = append(req, "base64")
	}
	return Tool{Name: name, Title: description, Description: description, InputSchema: object(props, req...), OutputSchema: map[string]any{"type": "object", "additionalProperties": true}, Annotations: annotations, run: func(c *call, args map[string]any) (output, error) {
		slug, err := eventSlug(args)
		if err != nil {
			return output{}, err
		}
		path := "/v1/hack/events/" + url.PathEscape(slug) + "/entry/screenshot"
		var raw []byte
		if method == http.MethodPut {
			b64, err := stringArg(args, "base64")
			if err != nil {
				return output{}, err
			}
			raw, err = base64.StdEncoding.DecodeString(b64)
			if err != nil {
				return output{}, errors.New("base64 must encode an image")
			}
		}
		res := c.do(method, path, raw, nil)
		if !res.ok() {
			return output{}, hackRestError(name, res)
		}
		if method == http.MethodGet {
			out := map[string]any{"base64": base64.StdEncoding.EncodeToString(res.body), "content_type": res.header.Get("Content-Type")}
			return output{Text: jsonText(out), Structured: out}, nil
		}
		var out map[string]any
		if err := json.Unmarshal(res.body, &out); err != nil || out == nil {
			return output{}, fmt.Errorf("%s: server returned invalid JSON", name)
		}
		return output{Text: jsonText(out), Structured: out}, nil
	}}
}
