package hackmcp

import (
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/vsriram/simple-host/host/mcp"
)

// hackAppFiles holds Simple Hack's own screen files; the bridge shared by every
// screen stays with the tool server (mcp.AppFiles).
//
//go:embed apps/*.html apps/*.js apps/*.css
var hackAppFiles embed.FS

// hackScreens is one tool server's Simple Hack screens, built once per server
// from Contribution.Apps. It holds no request data.
type hackScreens struct{ ctx mcp.AppContext }

func hackApps(ctx mcp.AppContext) mcp.AppContribution {
	s := &hackScreens{ctx: ctx}
	return mcp.AppContribution{
		Screens:   s.hackEnabledScreens,
		Resources: s.hackAppResources,
		Gate:      s.hackAppsGate,
		Read: func(req mcp.Request, c mcp.Caller, allowed bool) (mcp.Response, bool) {
			return s.readHackApp(req, c, allowed), true
		},
		AppFamily: func(base []mcp.Tool) mcp.FamilyTools {
			return mcp.FamilyTools{Family: "hack-app-only", Tools: s.hackAppFamilyTools(base)}
		},
		SitesPage: func() (string, string) {
			script, _ := hackAppFiles.ReadFile("apps/hack-sites.js")
			return string(script), "Simple Hack"
		},
	}
}

// Simple Hack's own MCP Apps extend the tool server's machinery (host/mcp's
// apps.go and clientui.go): the same screen shape (mcp.AppScreen), per-connection
// flag format (mcp.AppFlag: "on" or "accounts:a,b"), shared bridge.js and
// UI-support memory.
//
// They are kept apart from the tool server's own screens because those are
// deliberately Simple-Host-only: enabledScreens refuses every screen but
// my-sites/dashboard once c.Mode is set (the boundary
// TestHackAppsGatesAndUnchangedInventory holds a Hack connection to). Hack's
// screens get their own parallel gate instead of loosening that one.
//
// Resources: ui://simple-hack/judge and ui://simple-hack/organiser, opened by
// the show tools hack_show_judge and hack_show_organiser.
//
// apps/hack-{judge,organiser}-bundle.js and -kit.css are generated from
// products/hack/webassets/static/hack by apps/bundlegen; after changing a judge
// or organiser screen, or kit.css, regenerate with:
//
//go:generate go run ./apps/gentool
// TestHackJudgeBundleFresh and TestHackOrganiserBundleFresh fail if the
// checked-in copies drift.

// hackAppScreens lists Simple Hack's MCP App resources. A screen's ID names its
// asset files ("<ID>-kit.css", "-stubs.js", "-bundle.js", "-view.js", read by
// readHackApp), so adding a screen needs only the asset files.
var hackAppScreens = []mcp.AppScreen{
	{
		ID: "hack-judge", Flag: "MCP_APP_HACK_JUDGE", URI: "ui://simple-hack/judge", Label: "Judge", HTML: "hack-judge.html",
		CSP: map[string][]string{"connectDomains": {}, "resourceDomains": {}, "frameDomains": {}, "baseUriDomains": {}},
	},
	{
		// clipboardWrite: the Copy buttons in organiser-overview.js and
		// organiser-people.js call navigator.clipboard.writeText directly.
		ID: "hack-organiser", Flag: "MCP_APP_HACK_ORGANISER", URI: "ui://simple-hack/organiser", Label: "Organiser", HTML: "hack-organiser.html",
		CSP:         map[string][]string{"connectDomains": {}, "resourceDomains": {}, "frameDomains": {}, "baseUriDomains": {}},
		Permissions: map[string]any{"clipboardWrite": map[string]any{}},
	},
}

// hackEnabledScreens is hackAppScreens filtered to this connection: a personal
// event connection (CallerModeEvents) with its flag on for this account.
func (s *hackScreens) hackEnabledScreens(c mcp.Caller) []mcp.AppScreen {
	if c.Mode != mcp.CallerModeEvents {
		return nil
	}
	var screens []mcp.AppScreen
	for _, screen := range hackAppScreens {
		if mcp.AppFlag(os.Getenv(screen.Flag), c.Handle) {
			screens = append(screens, screen)
		}
	}
	return screens
}

// allowed mirrors readHackApp's: resources/list must not name a resource the
// connection could not read.
func (s *hackScreens) hackAppResources(c mcp.Caller, allowed bool) []map[string]any {
	resources := []map[string]any{}
	if !allowed {
		return resources
	}
	for _, screen := range s.hackEnabledScreens(c) {
		resources = append(resources, map[string]any{"uri": screen.URI, "name": screen.Label, "mimeType": mcp.AppMIME, "_meta": s.ctx.Meta(screen)})
	}
	return resources
}

// freshNonce is a per-read token for this resource's Content-Security-Policy,
// inside the host-declared one in _meta.ui.csp: script-src 'nonce-<this>', no
// unsafe-inline, so an injected script element or onerror= handler cannot run
// even if escaping is missed upstream. A fresh value every resources/read. No
// fallback on a crypto/rand failure: a predictable nonce defeats its purpose,
// so the read fails instead.
func freshNonce() (string, error) {
	buf := make([]byte, 18)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("hack_apps: could not generate a nonce: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// readHackApp mirrors the tool server's readApp for Simple Hack's screens; it
// is tried only after readApp reports the URI is not a Simple Host screen.
// allowed is the shared three-part gate (hackAppsGate). A read reaches here by
// URI, not listing, so without it a connection hidden from every hack_show_*
// tool could still read a resource by guessing its URI. The per-screen flag is
// checked in hackEnabledScreens, so one flag being off never hides a screen
// whose own flag is on.
func (s *hackScreens) readHackApp(req mcp.Request, caller mcp.Caller, allowed bool) mcp.Response {
	if !allowed {
		return mcp.Failure(req.ID, mcp.CodeInvalidParams, "resource not available", nil)
	}
	var params struct {
		URI string `json:"uri"`
	}
	if json.Unmarshal(req.Params, &params) != nil {
		return mcp.Failure(req.ID, mcp.CodeInvalidParams, "invalid resource params", nil)
	}
	for _, screen := range s.hackEnabledScreens(caller) {
		if params.URI != screen.URI {
			continue
		}
		page, err := s.appPage(screen)
		if err != nil {
			return mcp.Failure(req.ID, mcp.CodeInternalError, "could not prepare this view; try again", nil)
		}
		return mcp.Result(req.ID, map[string]any{"contents": []any{map[string]any{
			"uri": screen.URI, "mimeType": mcp.AppMIME, "text": page, "_meta": s.ctx.Meta(screen),
		}}})
	}
	return mcp.Failure(req.ID, mcp.CodeInvalidParams, "resource not available", nil)
}

// appPage is a screen's HTML with its style, bridge, bundle and view script
// inlined under a fresh nonce. The organiser bundle carries an __ORIGIN__
// placeholder for the website's origin (frame/organiser-nav.js), substituted
// before the bundle becomes a replacement value, since strings.Replacer never
// scans the values it substitutes in. The judge bundle never contains it
// (bundlegen.GenerateJS fails if it does).
func (s *hackScreens) appPage(screen mcp.AppScreen) (string, error) {
	asset := func(name string) string { b, _ := hackAppFiles.ReadFile("apps/" + name); return string(b) }
	bridge, _ := mcp.AppFiles().ReadFile("apps/bridge.js")
	nonce, err := freshNonce()
	if err != nil {
		return "", err
	}
	bundle := strings.ReplaceAll(asset(screen.ID+"-bundle.js"), "__ORIGIN__", "https://"+s.ctx.APIHost)
	return strings.NewReplacer(
		"__NONCE__", nonce,
		"__STYLE__", asset(screen.ID+"-kit.css"),
		"__BRIDGE__", string(bridge),
		"__BUNDLE__", bundle,
		"__VIEW__", asset(screen.ID+"-view.js"),
	).Replace(asset(screen.HTML)), nil
}

// --- hack_show_judge: the one tool that opens the judge app ---
//
// Every other judge tool keeps its plain result: linking an app to one of them
// would render a frame on every read, and the spec has no per-call opt-out.
// Results stay readable as plain text for a host or model with no UI.

// hackAppsGate is the three-part gate shared by every Hack MCP App: a personal
// event connection, never ChatGPT/OpenAI/Codex app names (the submitted ChatGPT
// app's pinned tool list must not change), and only once the connection has
// declared MCP Apps UI support. The per-screen flag is deliberately not part of
// it: each show-tool *Allowed function checks its own flag on top, so one app's
// flag being off never hides another app's resource or show tool, and the
// resource-read `allowed` parameter can stay a single bool that
// hackEnabledScreens narrows per screen.
func (s *hackScreens) hackAppsGate(c mcp.Caller, uiCapable bool) bool {
	return c.Mode == mcp.CallerModeEvents && !mcp.FlatListApp(c.AppName) && uiCapable
}

// hackShowJudgeAppAllowed is hackAppsGate plus the judge screen's own flag.
func (s *hackScreens) hackShowJudgeAppAllowed(c mcp.Caller, uiCapable bool) bool {
	return s.hackAppsGate(c, uiCapable) && mcp.AppFlag(os.Getenv("MCP_APP_HACK_JUDGE"), c.Handle)
}

func (s *hackScreens) hackShowJudgeTool() mcp.Tool {
	return mcp.WithRun(mcp.WithPolicy(mcp.Tool{
		Name:  "hack_show_judge",
		Title: "Show the judge view",
		Description: "Open the judge app view in chat: invite (accept a judge invite, needs code), queue (My teams), score (one entry's scoring, optional team_id) or rubric. " +
			"Only offered to a connection whose app can render it; every judging action also has a plain tool (hack_get_judge_queue, hack_score_team, hack_declare_my_conflict, hack_get_rubric, hack_preview_judge, hack_join_judge) that works the same everywhere.",
		InputSchema: mcp.Object(map[string]any{
			"slug":    mcp.Str("Event name. Required for view queue, score or rubric."),
			"code":    mcp.Str("Code from the judge invite link. Required for view invite."),
			"view":    map[string]any{"type": "string", "enum": []string{"invite", "queue", "score", "rubric"}, "description": "Which judge view to open."},
			"team_id": mcp.Str("Team ID from the judge queue. Optional for view score: opens that team directly instead of the first unscored one."),
		}, "view"),
		OutputSchema: mcp.OutObject(map[string]any{
			"view":    mcp.OutString("The view that was opened."),
			"slug":    mcp.OutString("Event name, once known."),
			"code":    mcp.OutString("The judge invite code, for view invite."),
			"team_id": mcp.OutString("Team ID passed through, for view score."),
			"event":   mcp.AnyJSON("Event detail for the opened view (omitted for view invite, which has not joined yet)."),
		}, "view"),
		Annotations: mcp.Writes(false, true, false),
	}, mcp.Screen("hack-judge"), mcp.ShowAllowed(s.hackShowJudgeAppAllowed)), s.runHackShowJudge)
}

func (s *hackScreens) runHackShowJudge(c *mcp.Call, args map[string]any) (mcp.Output, error) {
	if !s.hackShowJudgeAppAllowed(c.Caller(), c.UICapable()) {
		return mcp.Output{}, errors.New("hack_show_judge only opens in an app that can render it; use hack_get_judge_queue, hack_score_team, hack_get_rubric, hack_preview_judge or hack_join_judge instead")
	}
	view, err := mcp.StringArg(args, "view")
	if err != nil {
		return mcp.Output{}, err
	}
	switch view {
	case "invite":
		return showJudgeInvite(c, args)
	case "queue", "score", "rubric":
		return showJudgeEvent(c, args, view)
	}
	return mcp.Output{}, errors.New("view must be invite, queue, score or rubric")
}

// showJudgeInvite opens the invite view. Its text is a fixed sentence with no
// organiser-authored title or REST-body content, since the model sees it too.
func showJudgeInvite(c *mcp.Call, args map[string]any) (mcp.Output, error) {
	code, err := mcp.StringArg(args, "code")
	if err != nil {
		return mcp.Output{}, errors.New("code is required for view invite")
	}
	if _, err := hackDo(c, "hack_show_judge", http.MethodGet, "/v1/hack/judge/"+url.PathEscape(code), nil); err != nil {
		return mcp.Output{}, err
	}
	return mcp.Output{Text: "Opened the judge invite.", Structured: map[string]any{"view": "invite", "code": code}}, nil
}

var judgeViewNames = map[string]string{"queue": "the judging queue", "score": "scoring", "rubric": "the rubric"}

// showJudgeEvent opens a judging view of one event for a judge of it. The text
// is a fixed sentence (slug is a constrained identifier). About/rules/prizes
// ride in _meta, which a model never reads (bridge.js callToolResult).
func showJudgeEvent(c *mcp.Call, args map[string]any, view string) (mcp.Output, error) {
	slug, err := eventSlug(args)
	if err != nil {
		return mcp.Output{}, err
	}
	teamID, err := mcp.OptionalString(args, "team_id")
	if err != nil {
		return mcp.Output{}, err
	}
	raw, err := hackDo(c, "hack_show_judge", http.MethodGet, eventPath(slug, ""), nil)
	if err != nil {
		return mcp.Output{}, err
	}
	var body struct {
		Event map[string]any `json:"event"`
		Can   struct {
			Judge bool `json:"judge"`
		} `json:"can"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return mcp.Output{}, errors.New("hack_show_judge: server returned invalid JSON")
	}
	if !body.Can.Judge {
		return mcp.Output{}, errors.New("hack_show_judge is for a judge of this event")
	}
	structured := map[string]any{"view": view, "slug": slug, "event": hackShowJudgeEventFields(body.Event)}
	if teamID != "" {
		structured["team_id"] = teamID
	}
	out := mcp.Output{Text: "Opened " + judgeViewNames[view] + " for " + slug + ".", Structured: structured}
	if text := hackShowJudgeEventText(body.Event); len(text) > 0 {
		out.Meta = map[string]any{"simple-hack/event-text": text}
	}
	return out, nil
}

// hackShowJudgeEventFields is the only shape of an event that content/
// structuredContent ever carries: the fields judge screens read that double as
// judging state (scoring_open, judging_locked_at, results_published,
// submission_deadline; hack_get_event drops these for the model, see
// refreshEvent in hack-judge-view.js), never the full REST body, which can
// carry an organiser's join_code/judge_code when the caller also organises the
// event. about/rules/prizes have no judging-state reason to reach the model;
// see hackShowJudgeEventText.
func hackShowJudgeEventFields(event map[string]any) map[string]any {
	out := map[string]any{}
	for _, key := range []string{
		"slug", "title", "stage", "scoring_open", "judging_locked_at",
		"results_published", "submission_deadline", "time_zone",
	} {
		if v, ok := event[key]; ok {
			out[key] = v
		}
	}
	return out
}

// hackShowJudgeEventText is the "About this event" card (about/rules/prizes)
// and nothing else: view-only, carried in _meta rather than content.
func hackShowJudgeEventText(event map[string]any) map[string]any {
	out := map[string]any{}
	for _, key := range []string{"about", "rules", "prizes"} {
		if v, ok := event[key]; ok {
			out[key] = v
		}
	}
	return out
}
