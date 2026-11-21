// famHackAppOnly declares app-only twins and judge/organiser screen tools.
package hackmcp

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/vsriram/simple-host/host/mcp"
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
// base tool's run() -- see hackAppOnlyTools below, called through
// AppContribution.AppFamily after every other hack tool is assembled. hack_get_rubric's twin is
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

// hackAppOnlyAllowed is one twin's own call-time (and, via the tool
// server's listing filter, listing-time) gate: the exact same three-part
// check as the matching show tool (hackAppsGate -- events mode, never
// ChatGPT/OpenAI/Codex, UI declared -- plus that app's own flag). Fixed
// after an owner follow-up review found every twin was being listed to
// every events connection regardless of flag, app name or UI support --
// `_meta.ui.visibility` only helps a host that honours it, so the server
// has to withhold the tool itself from anyone a show tool would also be
// withheld from, the same way the listing filter already does for
// hack_show_judge/hack_show_organiser.
type hackAppOnlyAllowed func(c mcp.Caller, uiCapable bool) bool

func (s *hackScreens) hackAppOnlyJudgeAllowed(c mcp.Caller, uiCapable bool) bool {
	return s.hackShowJudgeAppAllowed(c, uiCapable)
}
func (s *hackScreens) hackAppOnlyOrganiserAllowed(c mcp.Caller, uiCapable bool) bool {
	return s.hackShowOrganiserAppAllowed(c, uiCapable)
}

// hackAppOnlySharedAllowed is hack_app_get_rubric's own gate: it is called
// by both the judge and organiser route tables, so it is available
// whenever EITHER app's own gate passes -- never both at once, and never
// neither.
func (s *hackScreens) hackAppOnlySharedAllowed(c mcp.Caller, uiCapable bool) bool {
	return s.hackAppOnlyJudgeAllowed(c, uiCapable) || s.hackAppOnlyOrganiserAllowed(c, uiCapable)
}

// hackAppOnlyTwin builds an app-only companion of base: same name's REST
// call, same auth, same InputSchema (the view sends it identical
// arguments), but a model-safe content shape. summarize turns the
// original (already fully authorized, already minimised-for-the-model-in
// -the-old-sense) output into the fixed text and small counts this twin's
// own content/structuredContent carries; the original's full
// structuredContent always still travels in _meta, untouched.
func hackAppOnlyTwin(base mcp.Tool, name, title string, outSchema map[string]any, allowed hackAppOnlyAllowed, summarize func(mcp.Output) (string, map[string]any)) mcp.Tool {
	twin := base
	twin.Name = name
	twin.Title = title
	twin.Description = "App-only companion of " + base.Name + " (SIM-344): the view calls this on its own, every time a screen opens, with nobody having asked for this data in chat. Returns a fixed summary and small counts; the real data goes to the view only, never to the model. Use " + base.Name + " instead for an explicit chat request."
	twin.Meta = hackAppOnlyVisibility()
	twin.OutputSchema = outSchema
	// The twin's own options replace the base tool's; the base runs under
	// its own (mcp.CallTool).
	twin = mcp.WithPolicy(twin, mcp.ShowAllowed(allowed))
	return mcp.WithRun(twin, func(c *mcp.Call, args map[string]any) (mcp.Output, error) {
		// The same three-part gate the matching show tool uses (flag,
		// events mode, not ChatGPT/OpenAI/Codex, UI declared) -- not just
		// c.UICapable(), which alone would still let a flagged-off or
		// excluded-app-name connection reach this tool merely by having
		// declared UI support generically (owner follow-up review).
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

// --- shape helpers: every hackRouteTool-backed base wraps its REST body
// as {"response": <body>, "status": N} (hack_organiser_tools.go's
// runHackRoute); a hand-written tool (hack_get_rubric, hack_set_event_
// stage) returns the shape directly. These read whichever applies.

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

// twinSpec is one app-only twin: the base tool it wraps, its name, title
// and output schema, which screen may call it, and how the base output is
// summarized for the model.
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
// already-assembled base registry (base must already contain every tool
// named below -- the tool server calls this after all base families are
// assembled).
//
// hack_get_judging_dashboard is NOT a generic twin: its base tool already
// has judge_id stripped from every judges[] row by
// minimiseHackOrganiserResult, a MODEL-facing protection (Grok security
// review, SIM-344 phase 1). The VIEW is fully authorized to see judge_id
// and organiser-judging.js's isCountedJudge() needs it (Codex review
// finding 1, SIM-344 phase 2), so that twin reads the REST route itself,
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

// --- summaries -------------------------------------------------------------

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

// judgingSettingsSummary passes the settings through as-is: every field is
// a fixed configuration enum, number or boolean -- never a name, a
// free-text field or a secret.
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

// --- the twins ---------------------------------------------------------------

// organiserReadTwins: Home's publish dialog reads standings and publishes
// through the first three; the rest load the organiser's screens.
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
// judging_ends_at twin (0003-hack-notices-judges-messages.sql round 2,
// Codex review) wraps hack_update_event, whose output carries the
// organiser's join/judge codes, for the "Judging closes" card; its input
// is narrower than hack_update_event's so it cannot patch page text.
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

// hackAppOnlyJudgingDashboardTool is hack_app_get_judging_dashboard: see
// the doc comment on its registration above for why this reads the REST
// route directly instead of reusing hack_get_judging_dashboard's own
// (model-minimised) run().
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
	// The glue's api() unwrap step expects runHackRoute's {"response",
	// "status"} shape; this read bypasses runHackRoute.
	out.Meta = hackAppOnlyMeta(map[string]any{"response": body, "status": res.Status()})
	return out, nil
}

// hackAppFamilyTools registers famHackAppOnly after all base tools exist.
func (s *hackScreens) hackAppFamilyTools(base []mcp.Tool) []mcp.Tool {
	// App-only twins (hack_app_get_applications and the rest, Grok
	// security review, SIM-344 phase 2): built last, from every tool
	// above, since each twin wraps an already-registered base tool by
	// name. Not additionally gated at tools/list the way hack_show_judge/
	// hack_show_organiser are -- their own content is minimal and safe by
	// design (fixed sentence, small counts, real data only in _meta), so
	// over-listing one costs little, where the show tools' whole reason
	// for conditional listing is that an unflagged or non-UI connection
	// has no frame to open at all.
	tools := s.hackAppOnlyTools(base)
	// Always present, like passcode_screen in the Simple Host inventory:
	// gated at tools/list (the tool server's listing filter) and at call time
	// (hackShowJudgeAppAllowed, hackShowOrganiserAppAllowed), never by
	// being absent from hackByName.
	return append(tools, s.hackShowJudgeTool(), s.hackShowOrganiserTool())
}
