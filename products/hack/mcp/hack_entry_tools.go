package hackmcp

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/vsriram/simple-host/host/mcp"
	"math"
	"net/http"
	"net/url"
	"strings"
)

func hackBaseEntryTools() []mcp.Tool {
	schemas := hackEntrySchemas()
	tools := declare([]decl{
		{
			OutputSchema: schemas.rubric,
			Name:         "hack_get_rubric",
			Title:        "Get the rubric",
			Description:  "Read an event's judging rubric: each criterion's name, description, weight and maximum points, never scores. Organiser, judge, or an approved participant reading their own event's rubric.",
			InputSchema:  mcp.Object(map[string]any{"slug": mcp.Str("The event name.")}, "slug"),
			Annotations:  mcp.ReadOnly(),
			run:          runGetRubric,
		},
		{
			OutputSchema: schemas.rubric,
			Name:         "hack_set_rubric",
			Title:        "Replace the rubric",
			Description: "Save the whole rubric. Keep existing criterion IDs to preserve scores. Removed criteria and scores above a reduced max_points are deleted only after confirmation. On score_deletion_confirmation explain the counts and ask before retrying with confirm_score_deletion. " +
				"1 to 10 criteria. Each weight is a whole number from 0 to 100 and the weights add up to 100. max_points is a whole number from 1 to 10. Organiser only. Refused at Results; move back to Judging to reopen scoring. Refused during Judging (judging_setup_locked); move back to Entries closed to change.",
			InputSchema: mcp.Object(map[string]any{
				"slug":                   mcp.Str("The event name."),
				"confirm_score_deletion": map[string]any{"type": "boolean", "description": "Set true only after the person confirms the reported score deletions."},
				"criteria": map[string]any{
					"type":        "array",
					"description": "The criteria, in display order. This list replaces the current rubric.",
					"items": mcp.Object(map[string]any{
						"id":          mcp.Str("Existing criterion ID to preserve; omit for a new criterion."),
						"name":        mcp.Str("Criterion name, up to 80 characters."),
						"description": mcp.Str("What judges are scoring, optional, up to 300 characters."),
						"weight":      map[string]any{"type": "integer", "description": "Whole number from 0 to 100. All weights must add up to 100."},
						"max_points":  map[string]any{"type": "integer", "description": "Whole number from 1 to 10."},
					}, "name", "weight", "max_points"),
				},
			}, "slug", "criteria"),
			Annotations: mcp.Writes(true, true, false),
			run:         runSetRubric,
		},
		{
			policy:       toolPolicy{analytics: analyticsPolicy{fileResult: true}},
			OutputSchema: schemas.csv,
			Name:         "hack_export_scores",
			Title:        "Export scores",
			Description:  "Download the event's scores as CSV (team, judge, criterion, points, max points, comment). Organiser only. Give the file to the person; do not post it publicly.",
			InputSchema:  mcp.Object(map[string]any{"slug": mcp.Str("The event name.")}, "slug"),
			Annotations:  mcp.ReadOnly(),
			run:          hackExportRun("scores"),
		},
		{
			policy:       toolPolicy{analytics: analyticsPolicy{fileResult: true}},
			OutputSchema: schemas.csv,
			Name:         "hack_export_results",
			Title:        "Export results",
			Description:  "Download the event's results as CSV (team, total, rank, tied, judges scored). Uses the published results when there are some, otherwise the current totals. Organiser only. Give the file to the person; do not post it publicly.",
			InputSchema:  mcp.Object(map[string]any{"slug": mcp.Str("The event name.")}, "slug"),
			Annotations:  mcp.ReadOnly(),
			run:          hackExportRun("results"),
		},
	})
	return tools
}

func hackCriteria(args map[string]any) ([]any, error) {
	raw, present := args["criteria"]
	if !present || raw == nil {
		return nil, errors.New("criteria is required")
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, errors.New("criteria must be a list of criteria")
	}
	if len(list) == 0 {
		return nil, errors.New("criteria must contain at least one criterion")
	}
	out := make([]any, 0, len(list))
	for i, item := range list {
		one, err := hackCriterion(i, item)
		if err != nil {
			return nil, err
		}
		out = append(out, one)
	}
	return out, nil
}

var criterionFields = map[string]bool{"id": true, "name": true, "description": true, "weight": true, "max_points": true}

func hackCriterion(i int, item any) (map[string]any, error) {
	obj, ok := item.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("criteria[%d] must be an object", i)
	}
	for key := range obj {
		if !criterionFields[key] {
			return nil, fmt.Errorf("criteria[%d] has unexpected field %q", i, key)
		}
	}
	at := func(err error) error { return fmt.Errorf("criteria[%d]: %w", i, err) }
	name, err := mcp.StringArg(obj, "name")
	if err != nil {
		return nil, at(err)
	}
	weight, _, err := mcp.WholeNumber(obj, "weight", true, math.MinInt32, math.MaxInt32)
	if err != nil {
		return nil, at(err)
	}
	points, _, err := mcp.WholeNumber(obj, "max_points", true, math.MinInt32, math.MaxInt32)
	if err != nil {
		return nil, at(err)
	}
	one := map[string]any{"name": name, "weight": weight, "max_points": points}
	id, present, err := presentString(obj, "id")
	if err != nil {
		return nil, err
	}
	if present {
		one["id"] = id
	}
	if _, present := obj["description"]; present {
		desc, _, err := presentString(obj, "description")
		if err != nil {
			return nil, at(err)
		}
		one["description"] = desc
	}
	return one, nil
}

func hackCSVOutput(slug, kind string, res mcp.UpstreamResult) (mcp.Output, error) {
	filename := csvFilename(res.Header(), slug+"-"+kind+".csv")
	csvText := string(res.Body())
	out := map[string]any{"slug": slug, "filename": filename, "csv": csvText}
	text := filename + "\n" + csvText
	return mcp.Output{Text: text, Structured: out}, nil
}

func csvFilename(h http.Header, fallback string) string {
	d := h.Get("Content-Disposition")
	const key = `filename="`
	i := strings.Index(d, key)
	if i < 0 {
		return fallback
	}
	rest := d[i+len(key):]
	j := strings.IndexByte(rest, '"')
	if j <= 0 {
		return fallback
	}
	return rest[:j]
}

type hackRubric struct {
	Version  string `json:"version"`
	Criteria []struct {
		ID          string `json:"id"`
		Position    int    `json:"position"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Weight      int    `json:"weight"`
		MaxPoints   int    `json:"max_points"`
	} `json:"criteria"`
}

func hackRubricOutput(slug string, body []byte) (mcp.Output, error) {
	var parsed hackRubric
	if err := json.Unmarshal(body, &parsed); err != nil {
		return mcp.Output{}, errors.New("the server returned a rubric this tool could not read")
	}
	items := make([]any, 0, len(parsed.Criteria))
	for _, c := range parsed.Criteria {
		items = append(items, map[string]any{
			"id":       c.ID,
			"position": c.Position, "name": c.Name, "description": c.Description,
			"weight": c.Weight, "max_points": c.MaxPoints,
		})
	}
	out := map[string]any{"slug": slug, "criteria": items, "version": parsed.Version}
	return mcp.Output{Text: mcp.JSONText(out), Structured: out}, nil
}

func hackMemberEntryTools() []mcp.Tool {
	slug := map[string]any{"slug": mcp.Str("Event name.")}
	teamID := map[string]any{"team_id": mcp.Str("Team ID from the judge queue.")}
	list := []mcp.Tool{
		memberTool("hack_get_submissions", "Browse submissions", "Read submitted, non-excluded entry summary cards for every team from Judging onward. Any joined participant, including one without a team. Never returns judging metadata.", http.MethodGet, "/v1/hack/events/{slug}/submissions", mcp.Object(slug, "slug"), nil, mcp.ReadOnly()),
		memberTool("hack_get_entry", "Read my team's entry", "Read the entry, the event's entry rules, completeness, submit status, the Home progress checklist and entry deadline.", http.MethodGet, "/v1/hack/events/{slug}/entry", mcp.Object(slug, "slug"), nil, mcp.ReadOnly()),
		memberTool("hack_update_entry", "Update my team's entry", "Set only the entry fields supplied. Empty strings clear fields. tagline is the one-line solution shown on the summary card; description is the longer write-up. Saving a draft never checks or changes completeness and never submits it -- call hack_submit_entry when it's ready. tools lists what was used, up to 10 items, each 1-30 characters, case-insensitive duplicates dropped. maturity is how far along it is: working, partial, concept, or empty to clear. track sets the team's chosen track by its slug, or clears it with an empty string. A save on an already-submitted entry that would make it incomplete is refused with `missing`. Ask before overwriting team text.", http.MethodPut, "/v1/hack/events/{slug}/entry", mcp.Object(withFields(slug, map[string]any{
			"title":       mcp.Str("Project title."),
			"tagline":     mcp.Str("Short summary: the one-line solution."),
			"description": mcp.Str("Project description."),
			"video_url":   mcp.Str("Video URL."),
			"code_url":    mcp.Str("Code URL."),
			"problem":     mcp.Str("The problem it solves, up to 140 characters."),
			"audience":    mcp.Str("Who it's for, up to 100 characters."),
			"tools":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "What was used: up to 10 items, each 1 to 30 characters."},
			"maturity":    map[string]any{"type": "string", "enum": []string{"", "working", "partial", "concept"}, "description": "How far along it is, or empty to clear."},
			"track":       mcp.Str("Track slug to set the team's chosen track, or empty to clear it."),
			"updated_at":  mcp.Str("updated_at from the entry you read, or empty for a new entry. A conflict returns the newer entry; ask before retrying."),
		}), "slug"), []string{"title", "tagline", "description", "video_url", "code_url", "problem", "audience", "tools", "maturity", "track", "updated_at"}, mcp.Writes(true, true, true)),
		memberTool("hack_submit_entry", "Submit my team's entry", "Move the entry from draft to submitted. Refused with `missing` (422 entry_incomplete, \"Add these first: ...\") when a required field or the show-your-work evidence is missing. Idempotent: submitting an already-submitted entry just keeps it submitted. A complete draft is also auto-submitted on its own at the deadline; an incomplete one stays a draft, is still judged, and is flagged Incomplete -- organisers can exclude it by hand with hack_set_entry_excluded.", http.MethodPost, "/v1/hack/events/{slug}/entry/submit", mcp.Object(withFields(slug, map[string]any{"updated_at": mcp.Str("updated_at from the entry you read, optional. A conflict (a teammate changed it) returns the newer entry; ask before retrying.")}), "slug"), []string{"updated_at"}, mcp.Writes(false, true, false)),
		memberTool("hack_unsubmit_entry", "Unsubmit my team's entry", "Put a submitted entry back to draft. Works only before the deadline; refused once it has passed. Reversible: call hack_submit_entry again anytime before the deadline.", http.MethodPost, "/v1/hack/events/{slug}/entry/unsubmit", mcp.Object(slug, "slug"), nil, mcp.Writes(true, true, false)),
		memberTool("hack_delete_entry_screenshot", "Remove my entry screenshot", "Remove the team's entry screenshot. Confirm first.", http.MethodDelete, "/v1/hack/events/{slug}/entry/screenshot", mcp.Object(slug, "slug"), nil, mcp.Writes(true, true, true)),
		memberTool("hack_get_voting", "Read voting options", "Read voting status, eligible teams and published vote ranking when available.", http.MethodGet, "/v1/hack/events/{slug}/vote", mcp.Object(slug, "slug"), nil, hackPublicReadOnly()),
		memberTool("hack_get_my_vote", "Read my vote", "Read this person's current vote.", http.MethodGet, "/v1/hack/events/{slug}/my-vote", mcp.Object(slug, "slug"), nil, mcp.ReadOnly()),
		memberTool("hack_vote", "Cast or change my vote", "Vote for an eligible team. This can replace the person's previous vote. Confirm their choice first.", http.MethodPut, "/v1/hack/events/{slug}/vote", mcp.Object(withFields(slug, map[string]any{"team": mcp.Str("Team slug from voting options.")}), "slug", "team"), []string{"team"}, mcp.Writes(true, true, true)),
		memberTool("hack_get_my_results", "Read my team's results", "Read this participant's published result and judge comments.", http.MethodGet, "/v1/hack/events/{slug}/my-results", mcp.Object(slug, "slug"), nil, mcp.ReadOnly()),
		memberTool("hack_get_judge_queue", "Read my judging queue", "List eligible entries, entry, pinned website link, conflicts and scoring status. Records any due submission deadline pins.", http.MethodGet, "/v1/hack/events/{slug}/judge/queue", mcp.Object(slug, "slug"), nil, mcp.Writes(false, true, false)),
		memberTool("hack_get_judge_scores", "Read my scores for a team", "Read this judge's scores and comment for one queue team.", http.MethodGet, "/v1/hack/events/{slug}/judge/scores/{team_id}", mcp.Object(withFields(slug, teamID), "slug", "team_id"), nil, mcp.ReadOnly()),
		memberTool("hack_score_team", "Score a team", "Save one or more criterion scores and optionally a comment. Confirm the scores with the judge before saving. Scoring opens when the event reaches Judging.", http.MethodPut, "/v1/hack/events/{slug}/judge/scores/{team_id}", mcp.Object(withFields(withFields(slug, teamID), map[string]any{"scores": map[string]any{"type": "array", "description": "One entry per criterion being scored: its criterion_id from the rubric and the points.", "items": mcp.Object(map[string]any{"criterion_id": mcp.Str("Criterion ID from rubric."), "points": map[string]any{"type": "integer", "description": "Points for this criterion, from 0 to its max_points in the rubric."}}, "criterion_id", "points")}, "comment": mcp.Str("Judge comment; an empty string clears it.")}), "slug", "team_id"), []string{"scores", "comment"}, mcp.Writes(true, true, false)),
		withToolPolicy(memberTool("hack_get_my_conflicts", "Read my conflicts", "List this judge's declared conflicts.", http.MethodGet, "/v1/hack/events/{slug}/conflicts", mcp.Object(slug, "slug"), nil, mcp.ReadOnly()), toolPolicy{memberResult: "conflicts"}),
		withToolPolicy(memberTool("hack_declare_my_conflict", "Declare a conflict", "Declare that this judge has a conflict with a team; scores for it will be unavailable. Confirm first.", http.MethodPost, "/v1/hack/events/{slug}/conflicts", mcp.Object(withFields(slug, teamID), "slug", "team_id"), []string{"team_id"}, mcp.Writes(true, true, false)), toolPolicy{memberResult: "conflict"}),
		memberTool("hack_remove_my_conflict", "Remove my conflict", "Remove this judge's conflict with a team. Confirm first.", http.MethodDelete, "/v1/hack/events/{slug}/conflicts/{team_id}", mcp.Object(withFields(slug, teamID), "slug", "team_id"), nil, mcp.Writes(true, true, false)),
	}
	list = append(list, memberScreenshotTool("hack_get_entry_screenshot", http.MethodGet, "Read my team's screenshot", mcp.ReadOnly()))
	list = append(list, memberScreenshotTool("hack_set_entry_screenshot", http.MethodPut, "Upload my team's screenshot. Ask the person before replacing the current image.", mcp.Writes(true, true, true)))
	return list
}

func hackOrganiserEntryTools() []mcp.Tool {
	tools := hackRouteTools(hackOrganiserEntryRoutes())
	// hand-written, not a hackRouteTool entry: the AI apps take a bool rather than
	// two tool names (the website's POST .../entry/exclude and .../entry/include).
	tools = append(tools, hackSetEntryExcludedTool())
	return tools
}

func hackOrganiserEntryRoutes() []hackRouteTool {
	return []hackRouteTool{
		{name: "hack_get_voting_settings", title: "Read voting settings", description: "Read event voting settings. Organiser only.", method: "GET", path: "/v1/hack/events/{slug}/voting", idempotent: true},
		{bodyDescription: "enabled, opens_at, closes_at, eligibility (all_signed_in, event_members or participants).", name: "hack_set_voting_settings", title: "Set voting settings", description: "Replace voting settings in the REST body. Narrowing eligibility removes earlier ineligible votes and returns votes_removed. Check votes_to_remove in hack_get_voting_settings and ask before removing votes; send confirm_vote_removal: true after confirmation or receive 409 vote_removal_confirmation with the count. Opening voting is public; ask first. Organiser only.", method: "PUT", path: "/v1/hack/events/{slug}/voting", body: true, destructive: true, idempotent: true, public: true},
		{policy: toolPolicy{recordsPins: true}, name: "hack_get_entries", title: "List entries", description: "Read all team entries and pinned website links, plus counts by status (submitted, drafts, not_started, not_submitted, sites_live). Records any due submission deadline pins. Organiser or judge.", method: "GET", path: "/v1/hack/events/{slug}/entries", idempotent: true},
		{name: "hack_get_entry_rules", title: "Read entry rules", description: "Read which entry fields are required, optional, off or one of (for the show-your-work evidence: video, website, code or screenshot), the active preset (video_or_website, video_website_code, or a legacy build/code/ideas/custom an older event may still carry), which evidence judges see first (judge_first: video or site), the one-sentence rules summary participants see, and whether rules can still change (editable, false from Closed on). Organiser only.", method: "GET", path: "/v1/hack/events/{slug}/entry-rules"},
		{bodyDescription: "Any subset: preset (video_or_website or video_website_code), fields (map of field name to required/optional/off/one_of -- one_of only on video_url, site, code_url, screenshot), judge_first (video or site), dry_run boolean (true previews impact and the resulting rules, without saving).", name: "hack_set_entry_rules", title: "Set entry rules", description: "Choose a preset -- video_or_website (the default: at least one of a video or a website) or video_website_code (video, website and code link all required) -- and/or set individual fields to required, optional, off, or one_of (video_url, site, code_url and screenshot only; exactly one such field needs to be met). judge_first picks which evidence (video or site) judges see first. Setting a field that differs from the active preset's own table demotes the stored preset to custom. dry_run:true returns the impact (how many submitted entries would newly read as missing something, how many drafts would be newly blocked, and which teams) and a preview, without saving anything. Tightening never un-submits an already-submitted entry. Refused once entries have closed (Closed stage onward): rules are fixed so judging sees one definition of complete. On a new event this is also the organiser's first move (next_step.kind entry_rules on hack_get_event): ask which preset before opening sign-up, even if they want the default. Ask before narrowing rules on a live event. Organiser only.", method: "PUT", path: "/v1/hack/events/{slug}/entry-rules", body: true, destructive: true, idempotent: true},
		{policy: toolPolicy{screenshot: true}, name: "hack_get_team_screenshot", title: "Read team screenshot", description: "Read one team's entry screenshot as base64 image bytes. Organiser or judge.", method: "GET", path: "/v1/hack/events/{slug}/teams/{team}/screenshot", idempotent: true},
		{name: "hack_get_judging_settings", title: "Read judging settings", description: "Read assignment and scoring settings, organiser_judges (selected organiser user IDs) and the canonical judges list. Organiser only.", method: "GET", path: "/v1/hack/events/{slug}/judging/settings", idempotent: true},
		{bodyDescription: "Any changed field: assignment_mode, judges_per_team, organiser_judges (array of current organiser user IDs; [] clears; omission preserves), score_mode, tie_criterion_id, public_scores, public_ranks. Legacy include_organisers: true selects all organisers, false clears, omitted preserves; organiser_judges wins.", name: "hack_set_judging_settings", title: "Set judging settings", description: "Change assignment_mode, judges_per_team, organiser_judges, score_mode, tie_criterion_id or public visibility flags. Ask before changing public flags. Organiser only. Mode and judges per team are refused during Judging (judging_setup_locked); move back to Entries closed to change.", method: "PATCH", path: "/v1/hack/events/{slug}/judging/settings", body: true, destructive: true, public: true},
		{policy: toolPolicy{optionalBody: true}, bodyDescription: "Optional legacy include_organisers: true selects all current organisers, false clears the saved selection, omitted leaves it unchanged.", name: "hack_generate_assignments", title: "Generate judge assignments", description: "Generate assignments from current settings. Uses joined judges plus saved organiser_judges. Legacy include_organisers updates that saved selection before generating. Replaces previous automatic assignments; ask first. Organiser only. Refused during Judging (judging_setup_locked); move back to Entries closed to change.", method: "POST", path: "/v1/hack/events/{slug}/assignments/generate", body: true, destructive: true},
		{name: "hack_get_assignments", title: "Read judge assignments", description: "Read manual assignments. Organiser only.", method: "GET", path: "/v1/hack/events/{slug}/assignments", idempotent: true},
		{bodyDescription: "assignments [{judge_id,team_id}], replacing the whole manual list.", name: "hack_set_assignments", title: "Replace judge assignments", description: "Replace manual assignments with body.assignments. Ask first. Organiser only. Refused during Judging (judging_setup_locked); move back to Entries closed to change.", method: "PUT", path: "/v1/hack/events/{slug}/assignments", body: true, destructive: true, idempotent: true},
		{name: "hack_get_panels", title: "Read judging panels", description: "Read track judging panels. Organiser only.", method: "GET", path: "/v1/hack/events/{slug}/judging/panels", idempotent: true},
		{bodyDescription: "panels [{track_id,judge_id}], replacing the whole panel list.", name: "hack_set_panels", title: "Replace judging panels", description: "Replace track panel memberships with body.panels. Ask first. Organiser only. Refused during Judging (judging_setup_locked); move back to Entries closed to change.", method: "PUT", path: "/v1/hack/events/{slug}/judging/panels", body: true, destructive: true, idempotent: true},
		{name: "hack_preview_judging_assignments", title: "Preview judging", description: "Preview assignment coverage without changing it. Organiser only.", method: "GET", path: "/v1/hack/events/{slug}/judging/preview", idempotent: true},
		{name: "hack_get_conflicts", title: "Read conflicts", description: "Read declared judge/team conflicts. Organiser only.", method: "GET", path: "/v1/hack/events/{slug}/conflicts", idempotent: true},
		{bodyDescription: "team_id and optional judge_user_id (omit for your own conflict).", name: "hack_set_judge_conflict", title: "Record judge conflict", description: "Record a conflict for any event judge using body.team_id and body.judge_user_id. Organisers may also omit judge_user_id to record their own conflict. Existing scores for that pairing leave current totals; ask first.", method: "POST", path: "/v1/hack/events/{slug}/conflicts", body: true, destructive: true},
		{policy: toolPolicy{judgeParameter: true}, name: "hack_remove_judge_conflict", title: "Remove judge conflict", description: "Remove a conflict; use query judge_user_id to choose another judge, or omit it for yourself. Ask before removing. Organiser only for another judge.", method: "DELETE", path: "/v1/hack/events/{slug}/conflicts/{team_id}", destructive: true, idempotent: true},
		{policy: toolPolicy{organiserResult: "judging"}, name: "hack_get_judging_dashboard", title: "Read judging progress", description: "Read judging coverage and totals, plus former_judges (people who left after scoring: their counted_teams still count toward totals until deleted). Organiser only.", method: "GET", path: "/v1/hack/events/{slug}/judging/dashboard", idempotent: true},
		{bodyDescription: "confirm: the exact event address name (slug), supplied only after the person confirms deletion.", name: "hack_delete_former_judge_scores", title: "Delete a departed judge's scores", description: "Delete the scores and comments of a judge who left the event, so they stop counting. user_id from former_judges in hack_get_judging_dashboard. confirm is the event's address name, sent only after the person agreed. Refused at Results; move back to Judging to reopen scoring. Published results change only when results are published again. Ask first. Organiser only.", method: "DELETE", path: "/v1/hack/events/{slug}/judging/former-judges/{user_id}", body: true, destructive: true},
		{bodyDescription: "rank_overrides [{team_id,rank}], or an empty list for computed ranks.", name: "hack_publish_results", title: "Publish results", description: "Publish results using body.rank_overrides for any tie breaks. This replaces the previous public snapshot: show the preview and ask first. Refused before entries close (409 judging_not_started). From Closed or Judging it moves the event to the Results stage and locks judging first, the same as Finish judging, so the stage and the public page never keep saying Judging once winners are out. Safe to call again after the event has ended, to fix a result (for example a disqualified team); it never reopens an ended event. Organiser only.", method: "POST", path: "/v1/hack/events/{slug}/results/publish", body: true, destructive: true, public: true},
		{bodyDescription: "full_ranking boolean (true for every rank, false for winners only).", name: "hack_set_results_view", title: "Set public results view", description: "Set body.full_ranking to show all ranks or winners only. Ask first. Organiser only.", method: "PATCH", path: "/v1/hack/events/{slug}/results", body: true, destructive: true, idempotent: true, public: true},
		{name: "hack_get_public_results", title: "Read public results", description: "Read published results. Available publicly after publication.", method: "GET", path: "/v1/hack/events/{slug}/results", idempotent: true, public: true},
		{policy: toolPolicy{analytics: analyticsPolicy{fileResult: true}},
			name: "hack_export_entries", title: "Export entries", description: "Read the entries CSV. Organiser only; keep private.", method: "GET", path: "/v1/hack/events/{slug}/export/entries.csv", idempotent: true, csv: true},
	}
}

func hackSetEntryExcludedTool() mcp.Tool {
	outSchema := mcp.OutObject(map[string]any{
		"response": mcp.AnyJSON("The team's updated entries-list item (HackEntryListItem)."),
		"status":   mcp.OutInteger("REST status code."),
	}, "response", "status")
	return decl{
		Name:        "hack_set_entry_excluded",
		Title:       "Exclude or include an entry",
		Description: "Hand-exclude a team's entry from judging, or put it back in. Organisers typically do this for an entry flagged Incomplete at the deadline (coordinator decision 2026-11-02: an incomplete draft is still judged by default, and organisers can exclude it by hand instead; the entry and its scores are unaffected, only judging eligibility changes). Ask the person first. Organiser only.",
		InputSchema: mcp.Object(map[string]any{
			"slug":     mcp.Str("The event name."),
			"team":     mcp.Str("Team slug."),
			"excluded": map[string]any{"type": "boolean", "description": "true excludes the entry from judging; false includes it again."},
		}, "slug", "team", "excluded"),
		OutputSchema: outSchema,
		Annotations:  mcp.Writes(true, true, false),
		run:          runSetEntryExcluded,
	}.tool()
}

func hackEntryTools() []mcp.Tool {
	tools := hackBaseEntryTools()
	tools = append(tools, hackMemberEntryTools()...)
	return append(tools, hackOrganiserEntryTools()...)
}

func hackEntrySchemas() hackEntrySchemaSet {
	rubric := mcp.OutObject(map[string]any{
		"version": mcp.OutString("Version of the displayed rubric; submit unchanged when acknowledging it."),
		"slug":    mcp.OutString("The event name."),
		"criteria": mcp.OutArray("The rubric, in display order.", mcp.OutObject(map[string]any{
			"id":          mcp.OutString("Criterion ID to use when scoring."),
			"position":    mcp.OutInteger("Display order, starting at 1."),
			"name":        mcp.OutString("Criterion name."),
			"description": mcp.OutString("What judges are scoring. Empty when unset."),
			"weight":      mcp.OutInteger("Weight, a whole number. All weights add up to 100."),
			"max_points":  mcp.OutInteger("Maximum points a judge can give, from 1 to 10."),
		}, "id", "position", "name", "description", "weight", "max_points")),
	}, "slug", "criteria")
	csv := mcp.OutObject(map[string]any{
		"slug":     mcp.OutString("The event name."),
		"filename": mcp.OutString("Suggested file name."),
		"csv":      mcp.OutString("The complete CSV text."),
	}, "slug", "filename", "csv")
	return hackEntrySchemaSet{rubric: rubric, csv: csv}
}

func memberScreenshotTool(name, method, description string, annotations map[string]any) mcp.Tool {
	props := map[string]any{"slug": mcp.Str("Event name.")}
	req := []string{"slug"}
	if method == http.MethodPut {
		props["base64"] = mcp.Str("Base64 encoded PNG, JPEG or WebP, at most 2 MB decoded.")
		req = append(req, "base64")
	}
	run := func(c *mcp.Call, args map[string]any) (mcp.Output, error) {
		return runMemberScreenshot(c, name, method, args)
	}
	return decl{Name: name, Title: description, Description: description, InputSchema: mcp.Object(props, req...), OutputSchema: map[string]any{"type": "object", "additionalProperties": true}, Annotations: annotations, run: run}.tool()
}

func runMemberScreenshot(c *mcp.Call, name, method string, args map[string]any) (mcp.Output, error) {
	slug, err := eventSlug(args)
	if err != nil {
		return mcp.Output{}, err
	}
	var image []byte
	if method == http.MethodPut {
		encoded, err := mcp.StringArg(args, "base64")
		if err != nil {
			return mcp.Output{}, err
		}
		if image, err = base64.StdEncoding.DecodeString(encoded); err != nil {
			return mcp.Output{}, errors.New("base64 must encode an image")
		}
	}
	res := c.Do(method, eventPath(slug, "/entry/screenshot"), image, nil)
	if !res.OK() {
		return mcp.Output{}, hackRestError(name, res)
	}
	out := map[string]any{"base64": base64.StdEncoding.EncodeToString(res.Body()), "content_type": res.Header().Get("Content-Type")}
	if method != http.MethodGet {
		var answer map[string]any
		if err := json.Unmarshal(res.Body(), &answer); err != nil || answer == nil {
			return mcp.Output{}, fmt.Errorf("%s: server returned invalid JSON", name)
		}
		out = answer
	}
	return mcp.Output{Text: mcp.JSONText(out), Structured: out}, nil
}

type hackEntrySchemaSet struct{ rubric, csv map[string]any }

func runGetRubric(c *mcp.Call, args map[string]any) (mcp.Output, error) {
	slug, err := eventSlug(args)
	if err != nil {
		return mcp.Output{}, err
	}
	body, err := hackDo(c, "hack_get_rubric", http.MethodGet, eventPath(slug, "/rubric"), nil)
	if err != nil {
		return mcp.Output{}, err
	}
	return hackRubricOutput(slug, body)
}

func runSetRubric(c *mcp.Call, args map[string]any) (mcp.Output, error) {
	slug, err := eventSlug(args)
	if err != nil {
		return mcp.Output{}, err
	}
	criteria, err := hackCriteria(args)
	if err != nil {
		return mcp.Output{}, err
	}
	confirm, err := mcp.BoolArg(args, "confirm_score_deletion", false)
	if err != nil {
		return mcp.Output{}, err
	}
	body, err := hackDo(c, "hack_set_rubric", http.MethodPut, eventPath(slug, "/rubric"), map[string]any{"criteria": criteria, "confirm_score_deletion": confirm})
	if err != nil {
		return mcp.Output{}, err
	}
	return hackRubricOutput(slug, body)
}

func hackExportRun(kind string) func(*mcp.Call, map[string]any) (mcp.Output, error) {
	return func(c *mcp.Call, args map[string]any) (mcp.Output, error) {
		tool := "hack_export_" + kind
		slug, err := eventSlug(args)
		if err != nil {
			return mcp.Output{}, err
		}
		res := c.Do(http.MethodGet, eventPath(slug, "/export/"+kind+".csv"), nil, nil)
		if !res.OK() {
			return mcp.Output{}, hackRestError(tool, res)
		}
		return hackCSVOutput(slug, kind, res)
	}
}

func runSetEntryExcluded(c *mcp.Call, args map[string]any) (mcp.Output, error) {
	slug, err := eventSlug(args)
	if err != nil {
		return mcp.Output{}, err
	}
	team, err := mcp.StringArg(args, "team")
	if err != nil {
		return mcp.Output{}, err
	}
	excluded, present, err := mcp.PresentBool(args, "excluded")
	if err != nil {
		return mcp.Output{}, err
	}
	if !present {
		return mcp.Output{}, fmt.Errorf("excluded is required")
	}
	action := map[bool]string{true: "exclude", false: "include"}[excluded]
	res := c.Do(http.MethodPost, eventPath(slug, "/teams/"+url.PathEscape(team)+"/entry/"+action), nil, nil)
	if !res.OK() {
		return mcp.Output{}, hackRestError("hack_set_entry_excluded", res)
	}
	var response any
	if len(res.Body()) > 0 {
		if err := json.Unmarshal(res.Body(), &response); err != nil {
			return mcp.Output{}, fmt.Errorf("hack_set_entry_excluded: server returned invalid JSON: %w", err)
		}
	}
	structured := map[string]any{"response": response, "status": res.Status()}
	return mcp.Output{Text: mcp.JSONText(structured), Structured: structured}, nil
}
