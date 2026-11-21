package hackmcp

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/vsriram/simple-host/host/mcp"
)

// App-only twins: each wraps a base tool's run() with the same REST route and
// auth but a model-safe result shape. A screen calls several read tools itself
// on open, with nobody in chat having asked for that data, and a host may route
// that call through its model-tool pipeline. So `content` and
// `structuredContent` (visible to the model) carry only a fixed sentence plus
// small non-identifying counts, and the real body travels in the result's
// `_meta` under "simple-hack/app-only", read back only by
// `window.SHApp.callToolResult`. The three organiser writes (hack_set_event_stage,
// hack_regenerate_code, hack_create_organiser_invite) work the same way so
// join/judge/co-organiser secret URLs never reach `content`.
//
// `_meta.ui.visibility: ["app"]` asks a host to hide the twin from the model,
// but that is advisory: the content shape is the real control, and run() also
// refuses a connection that has not declared MCP Apps UI support.
//
// Twins are built once at startup by hackAppOnlyTools, after every other hack
// tool is assembled. hack_app_get_rubric is shared by the judge and organiser
// route tables.

// hackAppOnlyVisibility asks a compliant host never to offer the twin to the model.
func hackAppOnlyVisibility() map[string]any {
	return map[string]any{"ui": map[string]any{"visibility": []string{"app"}}}
}

// hackAppOnlyMeta wraps the original tool's output as the view-only payload:
// the one copy of any personal data, secret URL or free text it returned.
func hackAppOnlyMeta(real map[string]any) map[string]any {
	if real == nil {
		return nil
	}
	return map[string]any{"simple-hack/app-only": real}
}

// hackAppOnlyAllowed gates one twin at call time and at listing time: the same
// three-part check as the matching show tool (hackAppsGate) plus that app's own
// flag. `_meta.ui.visibility` only helps a host that honours it, so the server
// withholds the tool from anyone a show tool would be withheld from.
type hackAppOnlyAllowed func(c mcp.Caller, uiCapable bool) bool

func (s *hackScreens) hackAppOnlyJudgeAllowed(c mcp.Caller, uiCapable bool) bool {
	return s.hackShowJudgeAppAllowed(c, uiCapable)
}
func (s *hackScreens) hackAppOnlyOrganiserAllowed(c mcp.Caller, uiCapable bool) bool {
	return s.hackShowOrganiserAppAllowed(c, uiCapable)
}

// hackAppOnlySharedAllowed gates hack_app_get_rubric: available when either the
// judge or the organiser app's gate passes.
func (s *hackScreens) hackAppOnlySharedAllowed(c mcp.Caller, uiCapable bool) bool {
	return s.hackAppOnlyJudgeAllowed(c, uiCapable) || s.hackAppOnlyOrganiserAllowed(c, uiCapable)
}

// hackAppOnlyTwin builds an app-only companion of base: same REST call, auth
// and InputSchema, but a model-safe content shape. summarize turns the
// authorized output into the fixed text and counts this twin's
// content/structuredContent carries; the full structuredContent still travels
// in _meta.
func hackAppOnlyTwin(base mcp.Tool, name, title string, outSchema map[string]any, allowed hackAppOnlyAllowed, summarize func(mcp.Output) (string, map[string]any)) mcp.Tool {
	twin := base
	twin.Name = name
	twin.Title = title
	twin.Description = "App-only companion of " + base.Name + " (SIM-344): the view calls this on its own, every time a screen opens, with nobody having asked for this data in chat. Returns a fixed summary and small counts; the real data goes to the view only, never to the model. Use " + base.Name + " instead for an explicit chat request."
	twin.Meta = hackAppOnlyVisibility()
	twin.OutputSchema = outSchema
	// The twin's own options replace the base tool's; the base runs under its own.
	twin = mcp.WithPolicy(twin, mcp.ShowAllowed(allowed))
	return mcp.WithRun(twin, func(c *mcp.Call, args map[string]any) (mcp.Output, error) {
		// Same gate as the matching show tool: c.UICapable() alone would let a
		// flagged-off or excluded-app connection through.
		if !allowed(c.Caller(), c.UICapable()) {
			return mcp.Output{}, errors.New(name + " is not available for this connection; use " + base.Name + " instead")
		}
		out, err := mcp.CallTool(base, c, args)
		if err != nil {
			return mcp.Output{}, err
		}
		text, counts := summarize(out)
		result := mcp.Output{Text: text, Structured: counts}
		result.Meta = hackAppOnlyMeta(out.Structured)
		return result, nil
	})
}

// Shape helpers: a hackRouteTool-backed base wraps its REST body as
// {"response": <body>, "status": N} (runHackRoute); a hand-written tool
// returns the shape directly. These read whichever applies.

func hackAppOnlyRouteBody(out mcp.Output) map[string]any {
	body, _ := out.Structured["response"].(map[string]any)
	return body
}

func hackAppOnlyRouteArray(out mcp.Output) []any {
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
		props[f] = mcp.OutInteger("Count.")
	}
	return mcp.OutObject(props)
}

// twinSpec is one app-only twin: base tool, name, title, output schema, which
// screen may call it, and how the base output is summarized for the model.
type twinSpec struct {
	base, name, title string
	gate              twinGate
	out               map[string]any
	summarize         func(mcp.Output) (string, map[string]any)
	input             map[string]any // replaces the base's input schema when set
}

type twinGate int

const (
	organiserTwin twinGate = iota
	judgeTwin
	sharedTwin // called by both the judge and organiser route tables
)

func (s *hackScreens) twinAllowed(gate twinGate) hackAppOnlyAllowed {
	switch gate {
	case judgeTwin:
		return s.hackAppOnlyJudgeAllowed
	case sharedTwin:
		return s.hackAppOnlySharedAllowed
	}
	return s.hackAppOnlyOrganiserAllowed
}

// hackAppOnlyTools builds every app-only twin, in listing order, from the
// assembled base registry (which must already contain every tool named below).
//
// hack_get_judging_dashboard is not a generic twin: its base tool strips
// judge_id from every judges[] row (minimiseHackOrganiserResult) to protect the
// model, but the view is authorized to see it and organiser-judging.js's
// isCountedJudge() needs it, so that twin reads the REST route itself,
// unminimised, straight into _meta.
func (s *hackScreens) hackAppOnlyTools(base []mcp.Tool) []mcp.Tool {
	byName := make(map[string]mcp.Tool, len(base))
	for _, t := range base {
		byName[t.Name] = t
	}
	build := func(specs []twinSpec) []mcp.Tool {
		tools := make([]mcp.Tool, 0, len(specs))
		for _, spec := range specs {
			baseTool, ok := byName[spec.base]
			if !ok {
				panic("hack_apps: app-only twin wiring: tool " + spec.base + " not found in the base registry")
			}
			twin := hackAppOnlyTwin(baseTool, spec.name, spec.title, spec.out, s.twinAllowed(spec.gate), spec.summarize)
			if spec.input != nil {
				twin.InputSchema = spec.input
			}
			tools = append(tools, twin)
		}
		return tools
	}
	twins := build(organiserReadTwins())
	twins = append(twins, s.hackAppOnlyJudgingDashboardTool())
	return append(twins, build(append(organiserWriteTwins(), judgeTwins()...))...)
}

// --- summaries ---

// loaded counts the rows read reports: "Loaded 3 teams.", {"count": 3}.
func loaded(read func(mcp.Output) int, singular, plural string) func(mcp.Output) (string, map[string]any) {
	return func(out mcp.Output) (string, map[string]any) {
		n := read(out)
		return pluralSentence("Loaded", n, singular, plural), map[string]any{"count": n}
	}
}

// fixed is a summary that never depends on the base output.
func fixed(text string, counts map[string]any) func(mcp.Output) (string, map[string]any) {
	return func(mcp.Output) (string, map[string]any) { return text, counts }
}

// routeField counts the array key in a route tool's REST body.
func routeField(key string) func(mcp.Output) int {
	return func(out mcp.Output) int { return hackAppOnlyArrayField(hackAppOnlyRouteBody(out), key) }
}

// ownField counts the array key in a hand-written tool's structured output.
func ownField(key string) func(mcp.Output) int {
	return func(out mcp.Output) int { return hackAppOnlyArrayField(out.Structured, key) }
}

func routeRows(out mcp.Output) int { return len(hackAppOnlyRouteArray(out)) }

func resultsSummary(title string) func(mcp.Output) (string, map[string]any) {
	return func(out mcp.Output) (string, map[string]any) {
		return title + ".", map[string]any{"count": routeField("results")(out)}
	}
}

func applicationsSummary(out mcp.Output) (string, map[string]any) {
	rows := hackAppOnlyRouteArray(out)
	pending := 0
	for _, row := range rows {
		if m, ok := row.(map[string]any); ok && m["status"] == "pending" {
			pending++
		}
	}
	return pluralSentence("Loaded", len(rows), "application", "applications"), map[string]any{"count": len(rows), "pending": pending}
}

func registrationSummary(out mcp.Output) (string, map[string]any) {
	body := hackAppOnlyRouteBody(out)
	return "Loaded sign-up questions and approval settings.", map[string]any{
		"questions":         hackAppOnlyArrayField(body, "questions"),
		"approval_required": body["approval_required"],
		"applications_open": body["applications_open"],
	}
}

// judgingSettingsSummary passes the settings through as-is: every field is a
// fixed enum, number or boolean, never free text or a secret.
func judgingSettingsSummary(out mcp.Output) (string, map[string]any) {
	body := hackAppOnlyRouteBody(out)
	counts := map[string]any{}
	for _, key := range []string{"assignment_mode", "judges_per_team", "score_mode", "tie_criterion_id", "public_scores", "public_ranks"} {
		if v, ok := body[key]; ok {
			counts[key] = v
		}
	}
	return "Loaded judging settings.", counts
}

func teamsSummary(out mcp.Output) (string, map[string]any) {
	teams := routeField("teams")(out)
	return pluralSentence("Loaded", teams, "team", "teams"), map[string]any{"teams": teams, "on_no_team": routeField("on_no_team")(out)}
}

func stageSummary(out mcp.Output) (string, map[string]any) {
	stage, _ := out.Structured["stage"].(string)
	return "Stage changed.", map[string]any{"stage": stage}
}

func judgingEndsAtSummary(out mcp.Output) (string, map[string]any) {
	v := out.Structured["judging_ends_at"]
	text := "Cleared when judging closes."
	if v != nil {
		text = "Set when judging closes."
	}
	return text, map[string]any{"judging_ends_at": v}
}

func scoresSummary(out mcp.Output) (string, map[string]any) {
	return "Loaded scores for this team.", map[string]any{"scored": ownField("scores")(out)}
}

func rubricSummary(out mcp.Output) (string, map[string]any) {
	criteria, _ := out.Structured["criteria"].([]any)
	return "Loaded the rubric.", map[string]any{"criteria": len(criteria)}
}

// --- the twins ---

// organiserReadTwins: the first three serve Home's publish dialog; the rest
// load the organiser's screens.
func organiserReadTwins() []twinSpec {
	return []twinSpec{
		{base: "hack_preview_judging_assignments", name: "hack_app_preview_results", title: "App: preview results", out: countSchema("count"), summarize: resultsSummary("App: preview results")},
		{base: "hack_get_public_results", name: "hack_app_get_results", title: "App: load published results", out: countSchema("count"), summarize: resultsSummary("App: load published results")},
		{base: "hack_publish_results", name: "hack_app_publish_results", title: "App: publish results", out: countSchema("count"), summarize: resultsSummary("App: publish results")},
		{base: "hack_get_people", name: "hack_app_get_people", title: "App: load people", out: countSchema("count"), summarize: loaded(routeRows, "person", "people")},
		{base: "hack_get_applications", name: "hack_app_get_applications", title: "App: load applications", out: countSchema("count", "pending"), summarize: applicationsSummary},
		{base: "hack_get_registration", name: "hack_app_get_registration", title: "App: load sign-up settings",
			out:       mcp.OutObject(map[string]any{"questions": mcp.OutInteger("Count."), "approval_required": map[string]any{"type": "boolean"}, "applications_open": map[string]any{"type": "boolean"}}),
			summarize: registrationSummary},
		{base: "hack_get_judging_settings", name: "hack_app_get_judging_settings", title: "App: load judging settings",
			out: mcp.OutObject(map[string]any{
				"assignment_mode": mcp.OutString("open, automatic, manual or panel."), "judges_per_team": mcp.OutInteger("Target judges per team."),
				"score_mode": mcp.OutString("Scoring mode."), "tie_criterion_id": mcp.OutString("Tie-break criterion ID, or empty."),
				"public_scores": map[string]any{"type": "boolean"}, "public_ranks": map[string]any{"type": "boolean"},
			}),
			summarize: judgingSettingsSummary},
		{base: "hack_get_conflicts", name: "hack_app_get_conflicts", title: "App: load conflicts", out: countSchema("count"), summarize: loaded(routeField("conflicts"), "conflict", "conflicts")},
		{base: "hack_get_teams", name: "hack_app_get_teams", title: "App: load teams", out: countSchema("teams", "on_no_team"), summarize: teamsSummary},
		{base: "hack_get_assignments", name: "hack_app_get_assignments", title: "App: load assignments", out: countSchema("count"), summarize: loaded(routeField("assignments"), "assignment", "assignments")},
	}
}

// organiserWriteTwins are the writes whose body carries a secret. The
// judging_ends_at twin wraps hack_update_event, whose output carries the
// join/judge codes; its input is narrower so it cannot patch page text.
func organiserWriteTwins() []twinSpec {
	return []twinSpec{
		{base: "hack_set_event_stage", name: "hack_app_set_event_stage", title: "App: change event stage",
			out: mcp.OutObject(map[string]any{"stage": mcp.OutString("The new stage.")}), summarize: stageSummary},
		{base: "hack_regenerate_code", name: "hack_app_regenerate_code", title: "App: make a new join or judge link",
			out: mcp.OutObject(map[string]any{}), summarize: fixed("Made a new link.", map[string]any{})},
		{base: "hack_create_organiser_invite", name: "hack_app_create_organiser_invite", title: "App: create a co-organiser invitation",
			out: mcp.OutObject(map[string]any{"created": map[string]any{"type": "boolean"}}), summarize: fixed("Created a co-organiser invitation.", map[string]any{"created": true})},
		{base: "hack_update_event", name: "hack_app_set_judging_ends_at", title: "App: set when judging closes",
			out: mcp.OutObject(map[string]any{
				"judging_ends_at": map[string]any{"type": []any{"string", "null"}, "description": "When judging closes, after the change, or null when cleared."},
			}, "judging_ends_at"),
			summarize: judgingEndsAtSummary,
			input: mcp.Object(map[string]any{
				"slug":            mcp.Str("The event name."),
				"judging_ends_at": mcp.Str("When judging closes, in the event time zone. Empty clears it. Judges with unscored teams are reminded before this time, once set; with no judging_ends_at they get no reminder."),
			}, "slug")},
	}
}

func judgeTwins() []twinSpec {
	return []twinSpec{
		{base: "hack_get_rubric", name: "hack_app_get_rubric", title: "App: load the rubric", gate: sharedTwin, out: countSchema("criteria"), summarize: rubricSummary},
		{base: "hack_get_judge_queue", name: "hack_app_get_judge_queue", title: "App: load the judging queue", gate: judgeTwin, out: countSchema("count"), summarize: loaded(ownField("queue"), "entry", "entries")},
		{base: "hack_get_judge_scores", name: "hack_app_get_judge_scores", title: "App: load my scores for a team", gate: judgeTwin,
			out: mcp.OutObject(map[string]any{"scored": mcp.OutInteger("Count.")}), summarize: scoresSummary},
		{base: "hack_get_my_conflicts", name: "hack_app_get_my_conflicts", title: "App: load my conflicts", gate: judgeTwin, out: countSchema("count"), summarize: loaded(ownField("conflicts"), "conflict", "conflicts")},
	}
}

func pluralSentence(verb string, n int, singular, plural string) string {
	noun := plural
	if n == 1 {
		noun = singular
	}
	return verb + " " + strconv.Itoa(n) + " " + noun + "."
}

// hackAppOnlyJudgingDashboardTool is hack_app_get_judging_dashboard; it reads
// the REST route directly instead of the model-minimised base run().
func (s *hackScreens) hackAppOnlyJudgingDashboardTool() mcp.Tool {
	return mcp.WithRun(mcp.WithPolicy(mcp.Tool{
		Name:  "hack_app_get_judging_dashboard",
		Title: "App: load judging progress",
		Description: "App-only companion of hack_get_judging_dashboard (SIM-344): the Judging view calls this on its own, every time it opens. " +
			"Returns a fixed summary and counts; the real coverage data (team and judge names, former judges) goes to the view only, never to the model. Use hack_get_judging_dashboard instead for an explicit chat request.",
		InputSchema:  mcp.Object(map[string]any{"slug": mcp.Str("The event name.")}, "slug"),
		OutputSchema: countSchema("teams", "judges", "former_judges"),
		Meta:         hackAppOnlyVisibility(),
		Annotations:  mcp.Writes(false, true, false),
	}, mcp.ShowAllowed(s.hackAppOnlyOrganiserAllowed)), s.runAppJudgingDashboard)
}

func (s *hackScreens) runAppJudgingDashboard(c *mcp.Call, args map[string]any) (mcp.Output, error) {
	if !s.hackAppOnlyOrganiserAllowed(c.Caller(), c.UICapable()) {
		return mcp.Output{}, errors.New("hack_app_get_judging_dashboard is not available for this connection; use hack_get_judging_dashboard instead")
	}
	slug, err := eventSlug(args)
	if err != nil {
		return mcp.Output{}, err
	}
	res := c.Do(http.MethodGet, eventPath(slug, "/judging/dashboard"), nil, nil)
	if !res.OK() {
		return mcp.Output{}, hackRestError("hack_app_get_judging_dashboard", res)
	}
	var body map[string]any
	if len(res.Body()) > 0 && json.Unmarshal(res.Body(), &body) != nil {
		return mcp.Output{}, errors.New("hack_app_get_judging_dashboard: server returned invalid JSON")
	}
	counts := map[string]any{}
	for _, key := range []string{"teams", "judges", "former_judges"} {
		counts[key] = hackAppOnlyArrayField(body, key)
	}
	out := mcp.Output{Text: "Loaded judging progress.", Structured: counts}
	// The glue's api() unwrap expects runHackRoute's {"response","status"} shape.
	out.Meta = hackAppOnlyMeta(map[string]any{"response": body, "status": res.Status()})
	return out, nil
}

func (s *hackScreens) hackAppFamilyTools(base []mcp.Tool) []mcp.Tool {
	// App-only twins are built last, since each wraps an already-registered base
	// tool by name. They are not gated again at tools/list: their content is
	// minimal by design, so over-listing costs little.
	tools := s.hackAppOnlyTools(base)
	// Always present; gated at tools/list and at call time
	// (hackShowJudgeAppAllowed, hackShowOrganiserAppAllowed), never by absence
	// from hackByName.
	return append(tools, s.hackShowJudgeTool(), s.hackShowOrganiserTool())
}
