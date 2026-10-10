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
		memberTool("hack_preview_join", "Read a participant join link", "Read the event, code of conduct, signup questions, and whether joining is open.", http.MethodGet, "/v1/hack/join/{code}", object(code, "code"), nil, readOnly()),
		memberTool("hack_join_event", "Join an event", "Join as a participant. Ask the person to accept the displayed code of conduct and provide their display name and signup answers first.", http.MethodPost, "/v1/hack/join/{code}", object(with(code, map[string]any{"accept_coc": map[string]any{"type": "boolean", "description": "true once the person has read the code of conduct shown by the preview tool and agreed to it."}, "display_name": str("Name shown to others."), "answers": map[string]any{"type": "object", "description": "Answers keyed by signup question ID.", "additionalProperties": true}}), "code", "accept_coc", "display_name"), []string{"accept_coc", "display_name", "answers"}, writes(false, false, false)),
		memberTool("hack_application_status", "Read my application status", "Read your participant application approval status and team information.", http.MethodGet, "/v1/hack/events/{slug}", object(slug, "slug"), nil, readOnly()),
		memberTool("hack_create_team", "Create my team", "Create a team and join it. Ask the person before creating a team.", http.MethodPost, "/v1/hack/events/{slug}/teams", object(with(slug, map[string]any{"name": str("Team name.")}), "slug", "name"), []string{"name"}, writes(false, false, false)),
		memberTool("hack_join_team", "Join a team", "Join a team with its private team code. Ask the person before joining.", http.MethodPost, "/v1/hack/events/{slug}/teams/join", object(with(slug, map[string]any{"code": str("Private team code.")}), "slug", "code"), []string{"code"}, writes(false, false, false)),
		memberTool("hack_leave_team", "Leave my team", "Leave the current team. Confirm first: the team's access and membership change.", http.MethodPost, "/v1/hack/events/{slug}/teams/leave", object(slug, "slug"), nil, writes(true, false, false)),
		memberTool("hack_choose_track", "Choose my team's track", "Choose a listed track for the current team; an empty track clears the choice. Confirm with the team first.", http.MethodPut, "/v1/hack/events/{slug}/team/track", object(with(slug, map[string]any{"track": str("Track slug, or empty to clear.")}), "slug", "track"), []string{"track"}, writes(true, true, false)),
		memberTool("hack_get_entry", "Read my team's entry", "Read the entry, required fields, completeness and entry deadline.", http.MethodGet, "/v1/hack/events/{slug}/entry", object(slug, "slug"), nil, readOnly()),
		memberTool("hack_update_entry", "Update my team's entry", "Set only the entry fields supplied. Empty strings clear fields. Ask before overwriting team text.", http.MethodPut, "/v1/hack/events/{slug}/entry", object(with(slug, map[string]any{"title": str("Project title."), "tagline": str("Short summary."), "description": str("Project description."), "video_url": str("Video URL."), "code_url": str("Code URL.")}), "slug"), []string{"title", "tagline", "description", "video_url", "code_url"}, writes(true, true, false)),
		memberTool("hack_delete_entry_screenshot", "Remove my entry screenshot", "Remove the team's entry screenshot. Confirm first.", http.MethodDelete, "/v1/hack/events/{slug}/entry/screenshot", object(slug, "slug"), nil, writes(true, true, false)),
		memberTool("hack_get_team_status", "Read my team status", "Read this participant's team, site status, track and deadline.", http.MethodGet, "/v1/hack/events/{slug}", object(slug, "slug"), nil, readOnly()),
		memberTool("hack_get_my_teams", "Read my teams and sites", "List current teams and their site addresses for this person.", http.MethodGet, "/v1/hack/my-teams", noArgs(), nil, readOnly()),
		memberTool("hack_get_voting", "Read voting options", "Read voting status, eligible teams and published vote ranking when available.", http.MethodGet, "/v1/hack/events/{slug}/vote", object(slug, "slug"), nil, readOnly()),
		memberTool("hack_get_my_vote", "Read my vote", "Read this person's current vote.", http.MethodGet, "/v1/hack/events/{slug}/my-vote", object(slug, "slug"), nil, readOnly()),
		memberTool("hack_vote", "Cast or change my vote", "Vote for an eligible team. This can replace the person's previous vote. Confirm their choice first.", http.MethodPut, "/v1/hack/events/{slug}/vote", object(with(slug, map[string]any{"team": str("Team slug from voting options.")}), "slug", "team"), []string{"team"}, writes(true, true, false)),
		memberTool("hack_get_my_results", "Read my team's results", "Read this participant's published result and judge comments.", http.MethodGet, "/v1/hack/events/{slug}/my-results", object(slug, "slug"), nil, readOnly()),
		memberTool("hack_preview_judge", "Read a judge join link", "Read the event and code of conduct before joining as a judge.", http.MethodGet, "/v1/hack/judge/{code}", object(code, "code"), nil, readOnly()),
		memberTool("hack_join_judge", "Join as a judge", "Join as a judge. Ask the person to accept the displayed code of conduct first.", http.MethodPost, "/v1/hack/judge/{code}", object(with(code, map[string]any{"accept_coc": map[string]any{"type": "boolean", "description": "true once the person has read the code of conduct shown by the preview tool and agreed to it."}, "display_name": str("Name shown to others.")}), "code", "accept_coc", "display_name"), []string{"accept_coc", "display_name"}, writes(false, false, false)),
		memberTool("hack_get_judge_queue", "Read my judging queue", "List eligible entries, entry, pinned website link, conflicts and scoring status.", http.MethodGet, "/v1/hack/events/{slug}/judge/queue", object(slug, "slug"), nil, readOnly()),
		memberTool("hack_get_judge_scores", "Read my scores for a team", "Read this judge's scores and comment for one queue team.", http.MethodGet, "/v1/hack/events/{slug}/judge/scores/{team_id}", object(with(slug, teamID), "slug", "team_id"), nil, readOnly()),
		memberTool("hack_score_team", "Score a team", "Save one or more criterion scores and optionally a comment. Confirm the scores with the judge before saving.", http.MethodPut, "/v1/hack/events/{slug}/judge/scores/{team_id}", object(with(with(slug, teamID), map[string]any{"scores": map[string]any{"type": "array", "description": "One entry per criterion being scored: its criterion_id from the rubric and the points.", "items": object(map[string]any{"criterion_id": str("Criterion ID from rubric."), "points": map[string]any{"type": "integer", "description": "Points for this criterion, from 0 to its max_points in the rubric."}}, "criterion_id", "points")}, "comment": str("Judge comment; an empty string clears it.")}), "slug", "team_id"), []string{"scores", "comment"}, writes(true, true, false)),
		memberTool("hack_get_my_conflicts", "Read my conflicts", "List this judge's declared conflicts.", http.MethodGet, "/v1/hack/events/{slug}/conflicts", object(slug, "slug"), nil, readOnly()),
		memberTool("hack_declare_my_conflict", "Declare a conflict", "Declare that this judge has a conflict with a team; scores for it will be unavailable. Confirm first.", http.MethodPost, "/v1/hack/events/{slug}/conflicts", object(with(slug, teamID), "slug", "team_id"), []string{"team_id"}, writes(true, true, false)),
		memberTool("hack_remove_my_conflict", "Remove my conflict", "Remove this judge's conflict with a team. Confirm first.", http.MethodDelete, "/v1/hack/events/{slug}/conflicts/{team_id}", object(with(slug, teamID), "slug", "team_id"), nil, writes(true, true, false)),
	}
	list = append(list, memberScreenshotTool("hack_get_entry_screenshot", http.MethodGet, "Read my team's screenshot", readOnly()))
	list = append(list, memberScreenshotTool("hack_set_entry_screenshot", http.MethodPut, "Upload my team's screenshot. Ask the person before replacing the current image.", writes(true, true, false)))
	return list
}

func memberTool(name, title, description, method, path string, input map[string]any, bodyKeys []string, annotations map[string]any) Tool {
	return Tool{Name: name, Title: title, Description: description, InputSchema: input, OutputSchema: map[string]any{"type": "object", "additionalProperties": true}, Annotations: annotations, run: func(c *call, args map[string]any) (output, error) {
		p := path
		for _, key := range []string{"slug", "code", "team_id"} {
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
