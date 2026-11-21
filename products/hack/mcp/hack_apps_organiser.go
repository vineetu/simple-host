package hackmcp

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"

	"github.com/vsriram/simple-host/host/mcp"
)

// Organiser screen: one resource, ui://simple-hack/organiser, and one show tool,
// hack_show_organiser, covering three views: overview (next step, snapshot,
// stage), people (applications and member list) and judging (rubric/assignment
// setup, progress, coverage and former judges). It follows hack_apps.go's judge
// pattern: same screen shape, flag format, gating (hackAppsGate) and a show tool
// that exists only to open a view; every organiser action it fronts keeps its
// own full-text result for a host or model with no UI.
// apps/hack-organiser-bundle.js and apps/hack-organiser-kit.css are generated
// from products/hack/webassets/static/hack by apps/bundlegen; after changing
// organiser-overview.js, organiser-people.js, organiser-judging.js,
// _orgA-util.js or kit.css, regenerate with:
//
//go:generate go run ./apps/gentool
//
// TestHackOrganiserBundleFresh fails if the checked-in copies drift.

// hackShowOrganiserAppAllowed is hackAppsGate plus the organiser screen's flag.
func (s *hackScreens) hackShowOrganiserAppAllowed(c mcp.Caller, uiCapable bool) bool {
	return s.hackAppsGate(c, uiCapable) && mcp.AppFlag(os.Getenv("MCP_APP_HACK_ORGANISER"), c.Handle)
}

func (s *hackScreens) hackShowOrganiserTool() mcp.Tool {
	return mcp.WithRun(mcp.WithPolicy(mcp.Tool{
		Name:  "hack_show_organiser",
		Title: "Show the organiser view",
		Description: "Open the organiser app view in chat: overview (next step, snapshot, stage), people (applications and the member list) or judging (assignments, progress and former judges; rubric in website Settings). " +
			"Only offered to a connection whose app can render it; every organiser action also has a plain tool (hack_get_event, hack_set_event_stage, hack_get_applications, hack_decide_application, hack_get_judging_dashboard, hack_set_event_stage, ...) that works the same everywhere.",
		InputSchema: mcp.Object(map[string]any{
			"slug": mcp.Str("Event name."),
			"view": map[string]any{"type": "string", "enum": []string{"overview", "people", "judging"}, "description": "Which organiser view to open."},
		}, "slug", "view"),
		OutputSchema: mcp.OutObject(map[string]any{
			"can":        hackAbilitiesSchema(),
			"view":       mcp.OutString("The view that was opened."),
			"slug":       mcp.OutString("Event name."),
			"admin_view": map[string]any{"type": "boolean", "description": "True when a platform admin is inspecting without real membership; every write is refused."},
			"event":      mcp.AnyJSON("Event detail for the opened view."),
			"organiser":  mcp.AnyJSON("Organiser-only counts for the opened view."),
		}, "view", "slug"),
		Annotations: mcp.Writes(false, true, false),
	}, mcp.Screen("hack-organiser"), mcp.ShowAllowed(s.hackShowOrganiserAppAllowed)), s.runHackShowOrganiser)
}

var organiserViewNames = map[string]string{"overview": "the organiser overview", "people": "the applications and people list", "judging": "the judging dashboard"}

// runHackShowOrganiser opens an organiser view and refuses anyone who is not an
// organiser of the event (a platform admin inspecting counts: loadMember gives
// it that role). A refusal carries no event data. The join/judge links and the
// organiser's display_name ride in _meta, read only by the view, never in a
// model-visible field.
func (s *hackScreens) runHackShowOrganiser(c *mcp.Call, args map[string]any) (mcp.Output, error) {
	if !s.hackShowOrganiserAppAllowed(c.Caller(), c.UICapable()) {
		return mcp.Output{}, errors.New("hack_show_organiser only opens in an app that can render it; use hack_get_event, hack_get_applications, hack_get_people or hack_get_judging_dashboard instead")
	}
	view, err := mcp.StringArg(args, "view")
	if err != nil {
		return mcp.Output{}, err
	}
	if organiserViewNames[view] == "" {
		return mcp.Output{}, errors.New("view must be overview, people or judging")
	}
	slug, err := eventSlug(args)
	if err != nil {
		return mcp.Output{}, err
	}
	raw, err := hackDo(c, "hack_show_organiser", http.MethodGet, eventPath(slug, ""), nil)
	if err != nil {
		return mcp.Output{}, err
	}
	var body struct {
		Event     map[string]any  `json:"event"`
		Organiser map[string]any  `json:"organiser"`
		Role      string          `json:"role"`
		Can       map[string]bool `json:"can"`
		AdminView bool            `json:"admin_view"`
		Me        map[string]any  `json:"me"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return mcp.Output{}, errors.New("hack_show_organiser: server returned invalid JSON")
	}
	if body.Role != "organiser" {
		return mcp.Output{}, errors.New("hack_show_organiser is for an organiser of this event")
	}
	structured := map[string]any{
		"view": view, "slug": slug, "admin_view": body.AdminView,
		"event":     hackShowOrganiserEventFields(body.Event),
		"organiser": hackShowOrganiserCounts(body.Organiser),
	}
	if body.Can != nil {
		structured["can"] = body.Can
	}
	out := mcp.Output{Text: "Opened " + organiserViewNames[view] + " for " + slug + ".", Structured: structured}
	if meta := hackShowOrganiserViewOnly(body.Organiser, body.Me); len(meta) > 0 {
		out.Meta = map[string]any{"simple-hack/organiser-view": meta}
	}
	return out, nil
}

// hackShowOrganiserEventFields mirrors hackShowJudgeEventFields: the fixed,
// non-prose event fields every view needs (stage, deadlines, lock/publish
// state), never the full REST body, which can carry join_code/judge_code. title
// is harmless fixed metadata but is never interpolated into this tool's out.Text.
func hackShowOrganiserEventFields(event map[string]any) map[string]any {
	out := map[string]any{}
	for _, key := range []string{
		// url is the event's public page address, computed from its slug (h.EventURL):
		// fixed and server-computed, not organiser prose. taken_down is needed by
		// organiser-judging.js's ui.writeLockReason to show the takedown explanation
		// and disable writes; without it a taken-down event's controls render editable
		// although the REST layer refuses their actions.
		"slug", "title", "stage", "scoring_open", "judging_locked_at",
		"results_published", "submission_deadline", "time_zone", "url", "taken_down",
	} {
		if v, ok := event[key]; ok {
			out[key] = v
		}
	}
	return out
}

// hackShowOrganiserCounts is the part of the REST response's organiser-only
// sub-object that is safe for the model: aggregate integer counts
// (participants, teams, judges, on_no_team). The rest (join_url, judge_url,
// organiser_name, organisation, purpose, ...) carries a secret or free text and
// stays out of structuredContent; see hackShowOrganiserViewOnly.
func hackShowOrganiserCounts(organiser map[string]any) map[string]any {
	if organiser == nil {
		return nil
	}
	counts, ok := organiser["counts"]
	if !ok {
		return nil
	}
	return map[string]any{"counts": counts}
}

// hackShowOrganiserViewOnly is the join/judge link cards and nothing else:
// view-only, carried in _meta rather than content or structuredContent (the
// channel hack_show_judge also uses for about/rules/prizes). display_name is the
// organiser's own chosen text, needed for organiser-people.js's "(you)" labels.
// me_id is that person's user_id (hack.go's meView): not secret, but an account
// ID is identifying data like a display name, so it takes this channel rather
// than the fixed fields.
func hackShowOrganiserViewOnly(organiser, me map[string]any) map[string]any {
	out := map[string]any{}
	if organiser != nil {
		for _, key := range []string{"join_url", "judge_url"} {
			if v, ok := organiser[key]; ok {
				out[key] = v
			}
		}
	}
	if me != nil {
		if v, ok := me["display_name"]; ok {
			out["display_name"] = v
		}
		if v, ok := me["user_id"]; ok {
			out["me_id"] = v
		}
	}
	return out
}
