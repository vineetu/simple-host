// famHackAppOnly declares app-only twins and judge/organiser screen tools.
package mcp

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
)

// App-only twins (Grok security review, SIM-344 phase 2). SEP-1865 defines
// `content` as text a host may put in front of the model and
// `structuredContent`/`_meta` as fields a host is told not to; it does not
// require a host to honour that for a `tools/call` the APP made on its
// own. The judge and organiser views open a screen and immediately call
// several read tools themselves -- hack_get_applications, hack_get_
// judging_dashboard, hack_get_judge_queue, and the rest -- with nobody in
// chat having asked for that data. A host that reuses its normal
// model-tool-call pipeline for an app-initiated call would put every
// applicant's email, city, age range and free-text answer, or a judge's
// queue of team names and entries, in front of the model with no question
// asked (finding 1). Three organiser writes have the same shape in
// reverse: hack_set_event_stage, hack_regenerate_code and hack_create_
// organiser_invite put the join/judge/co-organiser secret URLs the show
// tool deliberately withholds back into `content` the moment the
// organiser actually uses them (finding 2).
//
// The fix is not a new permission or a new REST call -- every twin here
// calls the exact same tool, over the exact same REST route, with the
// exact same auth, as the original it wraps. The only thing that changes
// is the SHAPE of the result: `content` and `structuredContent` (the two
// fields a model-facing pipeline can see) become a fixed, generic
// sentence plus small, non-identifying counts (or, for the three writes,
// nothing identifying at all), and the real REST body -- the only copy of
// it -- moves into the result's own `_meta`, under "simple-hack/app-only".
// That is the same channel hack_show_judge/hack_show_organiser already
// use for join/judge codes and organiser prose: written by the server
// alongside (never inside) `content`/`structuredContent`
// (server.go's withMeta-style attachment), and read back only by
// `window.SHApp.callToolResult` -- the bridge entry point the model-facing
// `callTool` path never uses, since `callTool` only ever returns
// `structuredContent` and silently discards `_meta`.
//
// Each twin also carries `_meta.ui.visibility: ["app"]`, the same key
// `passcode_screen` already carries (passcode_screen.go): a host that
// honours it leaves the twin off the tool list it offers the model
// entirely, so the model never calls it by name in the first place. That
// is advisory, not the real control -- the server cannot tell a frame's
// own `tools/call` from one the model made on the same connection, so a
// host that does not honour `visibility` could still let a model call a
// twin directly. The content shape above is what holds regardless; the
// twin's own run() also refuses outright for a connection that has not
// declared MCP Apps UI support (the same gate hack_show_judge/hack_show_
// organiser use), narrowing that further.
//
// Twins are built once, at startup, by wrapping an already-registered
// base tool's run() -- see hackAppOnlyTwins below, called from HackTools()
// after every other hack tool is assembled. hack_get_rubric's twin is
// shared: both the judge and organiser route tables call the same
// hack_app_get_rubric.

// hackAppOnlyVisibility is the _meta every twin carries, asking a
// compliant host to never offer it to the model.
func hackAppOnlyVisibility() map[string]any {
	return map[string]any{"ui": map[string]any{"visibility": []string{"app"}}}
}

// hackAppOnlyMeta wraps real, the original tool's own output, as the
// view-only payload: the one and only copy of whatever personal data,
// secret URL or free text the wrapped tool's REST route returned.
func hackAppOnlyMeta(real map[string]any) map[string]any {
	if real == nil {
		return nil
	}
	return map[string]any{"simple-hack/app-only": real}
}

// hackAppOnlyAllowed is one twin's own call-time (and, via hackShowTools
// Allowed in server.go, listing-time) gate: the exact same three-part
// check as the matching show tool (hackAppsGate -- events mode, never
// ChatGPT/OpenAI/Codex, UI declared -- plus that app's own flag). Fixed
// after an owner follow-up review found every twin was being listed to
// every events connection regardless of flag, app name or UI support --
// `_meta.ui.visibility` only helps a host that honours it, so the server
// has to withhold the tool itself from anyone a show tool would also be
// withheld from, the same way filterHackShowTools already does for
// hack_show_judge/hack_show_organiser.
type hackAppOnlyAllowed func(s *Server, c Caller, uiCapable bool) bool

func hackAppOnlyJudgeAllowed(s *Server, c Caller, uiCapable bool) bool {
	return s.hackShowJudgeAppAllowed(c, uiCapable)
}
func hackAppOnlyOrganiserAllowed(s *Server, c Caller, uiCapable bool) bool {
	return s.hackShowOrganiserAppAllowed(c, uiCapable)
}

// hackAppOnlySharedAllowed is hack_app_get_rubric's own gate: it is called
// by both the judge and organiser route tables, so it is available
// whenever EITHER app's own gate passes -- never both at once, and never
// neither.
func hackAppOnlySharedAllowed(s *Server, c Caller, uiCapable bool) bool {
	return hackAppOnlyJudgeAllowed(s, c, uiCapable) || hackAppOnlyOrganiserAllowed(s, c, uiCapable)
}

// hackAppOnlyTwin builds an app-only companion of base: same name's REST
// call, same auth, same InputSchema (the view sends it identical
// arguments), but a model-safe content shape. summarize turns the
// original (already fully authorized, already minimised-for-the-model-in
// -the-old-sense) output into the fixed text and small counts this twin's
// own content/structuredContent carries; the original's full
// structuredContent always still travels in _meta, untouched.
func hackAppOnlyTwin(base Tool, name, title string, outSchema map[string]any, allowed hackAppOnlyAllowed, summarize func(output) (string, map[string]any)) Tool {
	twin := base
	twin.Name = name
	twin.policy = toolPolicy{showAllowed: allowed}
	twin.Title = title
	twin.Description = "App-only companion of " + base.Name + " (SIM-344): the view calls this on its own, every time a screen opens, with nobody having asked for this data in chat. Returns a fixed summary and small counts; the real data goes to the view only, never to the model. Use " + base.Name + " instead for an explicit chat request."
	twin.Meta = hackAppOnlyVisibility()
	twin.OutputSchema = outSchema
	originalRun := base.run
	twin.run = func(c *call, args map[string]any) (output, error) {
		// The same three-part gate the matching show tool uses (flag,
		// events mode, not ChatGPT/OpenAI/Codex, UI declared) -- not just
		// c.uiClient, which alone would still let a flagged-off or
		// excluded-app-name connection reach this tool merely by having
		// declared UI support generically (owner follow-up review).
		if !allowed(c.server, c.caller, c.uiClient) {
			return output{}, errors.New(name + " is not available for this connection; use " + base.Name + " instead")
		}
		out, err := originalRun(c, args)
		if err != nil {
			return output{}, err
		}
		text, counts := summarize(out)
		result := output{Text: text, Structured: counts}
		result.Meta = hackAppOnlyMeta(out.Structured)
		return result, nil
	}
	return twin
}

// --- shape helpers: every hackRouteTool-backed base wraps its REST body
// as {"response": <body>, "status": N} (hack_organiser_tools.go's
// runHackRoute); a hand-written tool (hack_get_rubric, hack_set_event_
// stage) returns the shape directly. These read whichever applies.

func hackAppOnlyRouteBody(out output) map[string]any {
	body, _ := out.Structured["response"].(map[string]any)
	return body
}

func hackAppOnlyRouteArray(out output) []any {
	arr, _ := out.Structured["response"].([]any)
	return arr
}

func hackAppOnlyArrayField(body map[string]any, key string) int {
	arr, _ := body[key].([]any)
	return len(arr)
}

func countSchema(fields ...string) map[string]any {
	props := map[string]any{}
	for _, f := range fields {
		props[f] = outInteger("Count.")
	}
	return outObject(props)
}

// hackAppOnlyTools builds every app-only twin, by name, from the already-
// assembled base registry (base must already contain every tool named
// below -- hackFamilies() calls this after all base families are assembled).
func hackAppOnlyTools(base []Tool) []Tool {
	byName := make(map[string]Tool, len(base))
	for _, t := range base {
		byName[t.Name] = t
	}
	find := func(name string) Tool {
		t, ok := byName[name]
		if !ok {
			panic("hack_apps: app-only twin wiring: tool " + name + " not found in the base registry")
		}
		return t
	}

	var twins []Tool
	twin := func(baseName, name, title string, outSchema map[string]any, allowed hackAppOnlyAllowed, summarize func(output) (string, map[string]any)) {
		twins = append(twins, hackAppOnlyTwin(find(baseName), name, title, outSchema, allowed, summarize))
	}

	// --- organiser reads --------------------------------------------------
	twin("hack_get_people", "hack_app_get_people", "App: load people",
		countSchema("count"),
		hackAppOnlyOrganiserAllowed,
		func(out output) (string, map[string]any) {
			n := len(hackAppOnlyRouteArray(out))
			return pluralSentence("Loaded", n, "person", "people"), map[string]any{"count": n}
		})
	twin("hack_get_applications", "hack_app_get_applications", "App: load applications",
		countSchema("count", "pending"),
		hackAppOnlyOrganiserAllowed,
		func(out output) (string, map[string]any) {
			rows := hackAppOnlyRouteArray(out)
			pending := 0
			for _, row := range rows {
				if m, ok := row.(map[string]any); ok && m["status"] == "pending" {
					pending++
				}
			}
			return pluralSentence("Loaded", len(rows), "application", "applications"), map[string]any{"count": len(rows), "pending": pending}
		})
	twin("hack_get_registration", "hack_app_get_registration", "App: load sign-up settings",
		outObject(map[string]any{"questions": outInteger("Count."), "approval_required": map[string]any{"type": "boolean"}, "applications_open": map[string]any{"type": "boolean"}}),
		hackAppOnlyOrganiserAllowed,
		func(out output) (string, map[string]any) {
			body := hackAppOnlyRouteBody(out)
			return "Loaded sign-up questions and approval settings.", map[string]any{
				"questions":         hackAppOnlyArrayField(body, "questions"),
				"approval_required": body["approval_required"],
				"applications_open": body["applications_open"],
			}
		})
	twin("hack_get_judging_settings", "hack_app_get_judging_settings", "App: load judging settings",
		outObject(map[string]any{
			"assignment_mode": outString("open, automatic, manual or panel."), "judges_per_team": outInteger("Target judges per team."),
			"score_mode": outString("Scoring mode."), "tie_criterion_id": outString("Tie-break criterion ID, or empty."),
			"public_scores": map[string]any{"type": "boolean"}, "public_ranks": map[string]any{"type": "boolean"},
		}),
		hackAppOnlyOrganiserAllowed,
		func(out output) (string, map[string]any) {
			// Every field here is a fixed configuration enum, number or
			// boolean -- never a name, a free-text field or a secret --
			// so this twin passes them through as-is rather than reducing
			// to a count that would not exist for a settings object.
			body := hackAppOnlyRouteBody(out)
			counts := map[string]any{}
			for _, key := range []string{"assignment_mode", "judges_per_team", "score_mode", "tie_criterion_id", "public_scores", "public_ranks"} {
				if v, ok := body[key]; ok {
					counts[key] = v
				}
			}
			return "Loaded judging settings.", counts
		})
	twin("hack_get_conflicts", "hack_app_get_conflicts", "App: load conflicts",
		countSchema("count"),
		hackAppOnlyOrganiserAllowed,
		func(out output) (string, map[string]any) {
			n := hackAppOnlyArrayField(hackAppOnlyRouteBody(out), "conflicts")
			return pluralSentence("Loaded", n, "conflict", "conflicts"), map[string]any{"count": n}
		})
	twin("hack_get_teams", "hack_app_get_teams", "App: load teams",
		countSchema("teams", "on_no_team"),
		hackAppOnlyOrganiserAllowed,
		func(out output) (string, map[string]any) {
			body := hackAppOnlyRouteBody(out)
			teams := hackAppOnlyArrayField(body, "teams")
			onNone := hackAppOnlyArrayField(body, "on_no_team")
			return pluralSentence("Loaded", teams, "team", "teams"), map[string]any{"teams": teams, "on_no_team": onNone}
		})
	twin("hack_get_assignments", "hack_app_get_assignments", "App: load assignments",
		countSchema("count"),
		hackAppOnlyOrganiserAllowed,
		func(out output) (string, map[string]any) {
			n := hackAppOnlyArrayField(hackAppOnlyRouteBody(out), "assignments")
			return pluralSentence("Loaded", n, "assignment", "assignments"), map[string]any{"count": n}
		})

	// hack_get_judging_dashboard is NOT a generic hackAppOnlyTwin: its
	// base tool already has judge_id stripped from every judges[] row by
	// minimiseHackOrganiserResult, a MODEL-facing protection (Grok
	// security review, SIM-344 phase 1) that exists so a model calling
	// hack_get_judging_dashboard directly never receives an internal
	// judge identifier. That protection is pointless -- and actively
	// breaks the view -- once this read is itself behind an app-only
	// twin whose structuredContent is already reduced to counts: the
	// VIEW is fully authorized to see judge_id (the real REST response
	// always includes it; the organiser's own website shows the same
	// data), and organiser-judging.js's isCountedJudge() needs it to
	// match a dashboard row to the /people list (Codex review finding 1,
	// SIM-344 phase 2). So this twin reads the REST route itself,
	// unminimised, straight into _meta, rather than reusing hack_get_
	// judging_dashboard's already-stripped output.
	twins = append(twins, hackAppOnlyJudgingDashboardTool())

	// --- organiser writes whose body carries a secret ----------------------
	twin("hack_set_event_stage", "hack_app_set_event_stage", "App: change event stage",
		outObject(map[string]any{"stage": outString("The new stage.")}),
		hackAppOnlyOrganiserAllowed,
		func(out output) (string, map[string]any) {
			stage, _ := out.Structured["stage"].(string)
			return "Stage changed.", map[string]any{"stage": stage}
		})
	twin("hack_regenerate_code", "hack_app_regenerate_code", "App: make a new join or judge link",
		outObject(map[string]any{}),
		hackAppOnlyOrganiserAllowed,
		func(out output) (string, map[string]any) {
			return "Made a new link.", map[string]any{}
		})
	twin("hack_create_organiser_invite", "hack_app_create_organiser_invite", "App: create a co-organiser invitation",
		outObject(map[string]any{"created": map[string]any{"type": "boolean"}}),
		hackAppOnlyOrganiserAllowed,
		func(out output) (string, map[string]any) {
			return "Created a co-organiser invitation.", map[string]any{"created": true}
		})

	// --- shared and judge reads --------------------------------------------
	twin("hack_get_rubric", "hack_app_get_rubric", "App: load the rubric",
		countSchema("criteria"),
		hackAppOnlySharedAllowed,
		func(out output) (string, map[string]any) {
			criteria, _ := out.Structured["criteria"].([]any)
			return "Loaded the rubric.", map[string]any{"criteria": len(criteria)}
		})
	twin("hack_get_judge_queue", "hack_app_get_judge_queue", "App: load the judging queue",
		countSchema("count"),
		hackAppOnlyJudgeAllowed,
		func(out output) (string, map[string]any) {
			n := hackAppOnlyArrayField(out.Structured, "queue")
			return pluralSentence("Loaded", n, "entry", "entries"), map[string]any{"count": n}
		})
	twin("hack_get_judge_scores", "hack_app_get_judge_scores", "App: load my scores for a team",
		outObject(map[string]any{"scored": outInteger("Count.")}),
		hackAppOnlyJudgeAllowed,
		func(out output) (string, map[string]any) {
			n := hackAppOnlyArrayField(out.Structured, "scores")
			return "Loaded scores for this team.", map[string]any{"scored": n}
		})
	twin("hack_get_my_conflicts", "hack_app_get_my_conflicts", "App: load my conflicts",
		countSchema("count"),
		hackAppOnlyJudgeAllowed,
		func(out output) (string, map[string]any) {
			n := hackAppOnlyArrayField(out.Structured, "conflicts")
			return pluralSentence("Loaded", n, "conflict", "conflicts"), map[string]any{"count": n}
		})

	return twins
}

func pluralSentence(verb string, n int, singular, plural string) string {
	noun := plural
	if n == 1 {
		noun = singular
	}
	return verb + " " + strconv.Itoa(n) + " " + noun + "."
}

// hackAppOnlyJudgingDashboardTool is hack_app_get_judging_dashboard: see
// the doc comment on its registration above for why this reads the REST
// route directly instead of reusing hack_get_judging_dashboard's own
// (model-minimised) run().
func hackAppOnlyJudgingDashboardTool() Tool {
	return Tool{
		policy: toolPolicy{showAllowed: hackAppOnlyOrganiserAllowed}, Name: "hack_app_get_judging_dashboard",
		Title: "App: load judging progress",
		Description: "App-only companion of hack_get_judging_dashboard (SIM-344): the Judging view calls this on its own, every time it opens. " +
			"Returns a fixed summary and counts; the real coverage data (team and judge names, former judges) goes to the view only, never to the model. Use hack_get_judging_dashboard instead for an explicit chat request.",
		InputSchema:  object(map[string]any{"slug": str("The event name.")}, "slug"),
		OutputSchema: countSchema("teams", "judges", "former_judges"),
		Meta:         hackAppOnlyVisibility(),
		Annotations:  writes(false, true, false),
		run: func(c *call, args map[string]any) (output, error) {
			if !hackAppOnlyOrganiserAllowed(c.server, c.caller, c.uiClient) {
				return output{}, errors.New("hack_app_get_judging_dashboard is not available for this connection; use hack_get_judging_dashboard instead")
			}
			slug, err := eventSlug(args)
			if err != nil {
				return output{}, err
			}
			res := c.do(http.MethodGet, "/v1/hack/events/"+url.PathEscape(slug)+"/judging/dashboard", nil, nil)
			if !res.ok() {
				return output{}, hackRestError("hack_app_get_judging_dashboard", res)
			}
			var body map[string]any
			if len(res.body) > 0 {
				if err := json.Unmarshal(res.body, &body); err != nil {
					return output{}, errors.New("hack_app_get_judging_dashboard: server returned invalid JSON")
				}
			}
			teams, _ := body["teams"].([]any)
			judges, _ := body["judges"].([]any)
			former, _ := body["former_judges"].([]any)
			counts := map[string]any{"teams": len(teams), "judges": len(judges), "former_judges": len(former)}
			out := output{Text: "Loaded judging progress.", Structured: counts}
			// The glue's api() unwrap step expects the same {"response",
			// "status"} shape runHackRoute uses for every other
			// hackRouteTool-backed route (hack_organiser_tools.go); built
			// by hand here since this bypasses runHackRoute entirely.
			out.Meta = hackAppOnlyMeta(map[string]any{"response": body, "status": res.status})
			return out, nil
		},
	}
}

// hackAppFamilyTools registers famHackAppOnly after all base tools exist.
func hackAppFamilyTools(base []Tool) []Tool {
	// App-only twins (hack_app_get_applications and the rest, Grok
	// security review, SIM-344 phase 2): built last, from every tool
	// above, since each twin wraps an already-registered base tool by
	// name. Not additionally gated at tools/list the way hack_show_judge/
	// hack_show_organiser are -- their own content is minimal and safe by
	// design (fixed sentence, small counts, real data only in _meta), so
	// over-listing one costs little, where the show tools' whole reason
	// for conditional listing is that an unflagged or non-UI connection
	// has no frame to open at all.
	tools := hackAppOnlyTools(base)
	// Always present, like passcode_screen in the Simple Host inventory:
	// gated at tools/list (filterHackShowTools) and at call time
	// (hackShowJudgeAppAllowed, hackShowOrganiserAppAllowed), never by
	// being absent from hackByName.
	return append(tools, hackShowJudgeTool(), hackShowOrganiserTool())
}
