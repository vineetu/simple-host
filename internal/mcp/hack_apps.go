// famHackAppOnly registers the judge screen alongside its app resources.
package mcp

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Simple Hack's own MCP Apps, extending the machinery in apps.go and
// clientui.go rather than duplicating it: the same appScreen shape, the
// same per-connection flag format (appFlag: "on" or "accounts:a,b"), the
// same appFiles embed.FS (apps/*.html, apps/*.js, apps/*.css already
// covers the new hack-judge.* files alongside the Simple Host ones), and
// the same clientUIMemory that remembers whether a connection declared
// MCP Apps UI support.
//
// Kept separate from apps.go's appScreens/enabledScreens because those are
// deliberately Simple-Host-only: enabledScreens refuses every screen but
// my-sites/dashboard once c.Mode is set, which is exactly the boundary
// TestHackAppsGatesAndUnchangedInventory holds a Hack connection to. Hack's
// own screens get their own small, parallel gate instead of loosening that
// one.
//
// Phase 0 (foundation), Phase 1 (judge) and Phase 2 (organiser core, SIM-344)
// so far: two resources, ui://simple-hack/judge and
// ui://simple-hack/organiser, and two show tools, hack_show_judge and
// hack_show_organiser. The participant resource and the rest of the
// organiser surface (teams, entries, results, event page) are later
// phases.
//
// apps/hack-judge-bundle.js/apps/hack-judge-kit.css and
// apps/hack-organiser-bundle.js/apps/hack-organiser-kit.css are generated
// from internal/webassets/static/hack by internal/mcp/apps/bundlegen; after
// changing any judge or organiser screen, or kit.css, regenerate with:
//
//go:generate go run ./apps/gentool
//
// TestHackJudgeBundleFresh and TestHackOrganiserBundleFresh rebuild them in
// the test binary and fail if the checked-in copies have drifted.

// hackAppScreens lists Simple Hack's MCP App resources. Each screen's ID
// also names its own generated/hand-written asset files -- readHackApp
// below derives "<ID>-kit.css", "<ID>-stubs.js", "<ID>-bundle.js" and
// "<ID>-view.js" from it, so adding a screen here needs no change to the
// serving code, only the asset files themselves.
var hackAppScreens = []appScreen{
	{
		ID: "hack-judge", Flag: "MCP_APP_HACK_JUDGE", URI: "ui://simple-hack/judge", Label: "Judge", HTML: "hack-judge.html",
		CSP: map[string][]string{"connectDomains": {}, "resourceDomains": {}, "frameDomains": {}, "baseUriDomains": {}},
	},
	{
		// clipboardWrite: organiser-overview.js's join/judge link Copy
		// buttons and organiser-people.js's co-organiser invite Copy
		// button all call navigator.clipboard.writeText directly (not
		// ui.copyButton), same as passcode/site-preview's own Copy.
		ID: "hack-organiser", Flag: "MCP_APP_HACK_ORGANISER", URI: "ui://simple-hack/organiser", Label: "Organiser", HTML: "hack-organiser.html",
		CSP:         map[string][]string{"connectDomains": {}, "resourceDomains": {}, "frameDomains": {}, "baseUriDomains": {}},
		Permissions: map[string]any{"clipboardWrite": map[string]any{}},
	},
}

// hackEnabledScreens is hackAppScreens filtered to this connection: a
// personal event connection (CallerModeEvents -- the mode HackMemberTools'
// judge tools are listed under; see server.go's forCaller) with its flag
// on for this account. Unlike enabledScreens, there is no my-sites/
// dashboard exception to protect, since this list has nothing else in it.
func (s *Server) hackEnabledScreens(c Caller) []appScreen {
	if s.fixed != nil || c.Mode != CallerModeEvents {
		return nil
	}
	var screens []appScreen
	for _, screen := range hackAppScreens {
		if appFlag(os.Getenv(screen.Flag), c.Handle) {
			screens = append(screens, screen)
		}
	}
	return screens
}

// allowed mirrors readHackApp's: resources/list must not even name a
// resource a connection could not read (Grok security review, SIM-344).
func (s *Server) hackAppResources(c Caller, allowed bool) []any {
	resources := []any{}
	if !allowed {
		return resources
	}
	for _, screen := range s.hackEnabledScreens(c) {
		resources = append(resources, map[string]any{"uri": screen.URI, "name": screen.Label, "mimeType": appMIME, "_meta": s.appMeta(screen)})
	}
	return resources
}

// freshNonce is a per-read token for this resource's own
// Content-Security-Policy, independent of (and inside) the host-declared
// one in _meta.ui.csp: script-src 'nonce-<this>', no unsafe-inline, so an
// injected script element or an onerror= handler cannot run even if some
// escaping is missed somewhere upstream (see hack_apps_judge_test.go's
// XSS cases). A fresh value every resources/read call, never reused. No
// fallback on a crypto/rand failure (Grok security review, SIM-344): a
// predictable, clock-derived nonce defeats the one thing this value is
// for, so the read fails outright instead of serving a weakened page.
func freshNonce() (string, error) {
	buf := make([]byte, 18)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("hack_apps: could not generate a nonce: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// readHackApp mirrors readApp (apps.go) for Simple Hack's own screens: it
// is tried by dispatch only after readApp itself reports the URI is not
// one of its (Simple Host) screens, so neither function's tests need to
// know about the other's resources. allowed is the common three-part gate
// every Hack MCP App shares (hackAppsGate: events mode, not ChatGPT/OpenAI/
// Codex, UI declared) -- a resource read reaches here through its own URI,
// not through listing, so without this a connection the tool list already
// hides every hack_show_* tool from could still read a resource directly
// by guessing or being given its URI (Grok security review, SIM-344). The
// per-screen flag (MCP_APP_HACK_JUDGE, MCP_APP_HACK_ORGANISER, ...) is
// checked separately, inside hackEnabledScreens, so one flag being off
// never hides a screen whose own flag is on.
func (s *Server) readHackApp(req request, caller Caller, allowed bool) response {
	if !allowed {
		return failure(req.ID, codeInvalidParams, "resource not available", nil)
	}
	var params struct {
		URI string `json:"uri"`
	}
	if json.Unmarshal(req.Params, &params) != nil {
		return failure(req.ID, codeInvalidParams, "invalid resource params", nil)
	}
	for _, screen := range s.hackEnabledScreens(caller) {
		if params.URI != screen.URI {
			continue
		}
		asset := func(name string) string { b, _ := appFiles.ReadFile("apps/" + name); return string(b) }
		nonce, err := freshNonce()
		if err != nil {
			return failure(req.ID, codeInternalError, "could not prepare this view; try again", nil)
		}
		// The organiser bundle (only, so far) carries its own __ORIGIN__
		// placeholder for the real website's origin (frame/organiser-
		// nav.js: out-of-scope sections open there via ui/open-link rather
		// than staying unreachable). Substituted here, before the bundle
		// text becomes a replacement VALUE below -- strings.Replacer only
		// scans the page template it is given, never a value it is
		// substituting in, so a placeholder inside the bundle would
		// otherwise never be touched. The judge bundle never contains this
		// token (bundlegen.GenerateJS fails generation if it ever does),
		// so this ReplaceAll is a no-op there.
		bundle := strings.ReplaceAll(asset(screen.ID+"-bundle.js"), "__ORIGIN__", "https://"+s.cfg.APIHost)
		page := strings.NewReplacer(
			"__NONCE__", nonce,
			"__STYLE__", asset(screen.ID+"-kit.css"),
			"__BRIDGE__", asset("bridge.js"),
			"__BUNDLE__", bundle,
			"__VIEW__", asset(screen.ID+"-view.js"),
		).Replace(asset(screen.HTML))
		return result(req.ID, map[string]any{"contents": []any{map[string]any{
			"uri": screen.URI, "mimeType": appMIME, "text": page, "_meta": s.appMeta(screen),
		}}})
	}
	return failure(req.ID, codeInvalidParams, "resource not available", nil)
}

// --- hack_show_judge: the one tool that opens the judge app -------------
//
// Every other judge tool (hack_get_judge_queue, hack_score_team, ...)
// keeps exactly the result it has today; linking an app to one of them
// would render a frame on every read, which the spec has no per-call
// opt-out for (INTENT, 2026-10-23). This tool exists only to open the
// view, and like every Hack tool its result stays fully readable as plain
// text for a host or a model with no UI at all.

// hackAppsGate is the three-part gate the owner set (2026-10-31), shared by
// every Hack MCP App: a personal event connection, never ChatGPT/OpenAI/
// Codex app names (the submitted ChatGPT app's pinned tool list must not
// change), and only once this connection has actually declared MCP Apps UI
// support -- a client that has not would get a tool whose only job is
// opening a view it cannot show. The per-screen flag (MCP_APP_HACK_JUDGE,
// MCP_APP_HACK_ORGANISER, ...) is deliberately NOT part of this shared
// gate: each show-tool-specific *Allowed function below checks its own
// flag on top, so one app's flag being off can never hide a different
// app's resource or show tool, and a resource read's shared `allowed`
// parameter (readHackApp, hackAppResources) can stay a single bool that
// hackEnabledScreens then narrows per screen.
func (s *Server) hackAppsGate(c Caller, uiCapable bool) bool {
	return s.fixed == nil && c.Mode == CallerModeEvents && !flatListApp(c.AppName) && uiCapable
}

// hackShowJudgeAppAllowed is hackAppsGate plus the judge screen's own flag.
func (s *Server) hackShowJudgeAppAllowed(c Caller, uiCapable bool) bool {
	return s.hackAppsGate(c, uiCapable) && appFlag(os.Getenv("MCP_APP_HACK_JUDGE"), c.Handle)
}

// uiDeclaredForListing reports whether this connection has declared MCP
// Apps UI support, for gating hack_show_judge's presence in tools/list
// (never its behaviour -- that stays c.uiClient, set at call time from
// the same two sources). A modern (stateless) request carries its
// capabilities on every call, including tools/list, since every modern
// request's params._meta must carry io.modelcontextprotocol/
// clientCapabilities (server.go's validateHeaders); an initialize-era
// client only ever declares it once, in initialize, so clientUIMemory is
// the only record of it by the time tools/list is asked.
func (s *Server) uiDeclaredForListing(caller Caller, params json.RawMessage, modern bool) bool {
	if modern {
		var envelope struct {
			Meta map[string]json.RawMessage `json:"_meta"`
		}
		if json.Unmarshal(params, &envelope) != nil {
			return false
		}
		raw, present := envelope.Meta["io.modelcontextprotocol/clientCapabilities"]
		if !present {
			return false
		}
		var caps struct {
			Extensions map[string]json.RawMessage `json:"extensions"`
		}
		if json.Unmarshal(raw, &caps) != nil {
			return false
		}
		_, ui := caps.Extensions["io.modelcontextprotocol/ui"]
		return ui
	}
	ui, known := s.clientUI.lookup(caller, time.Now())
	return known && ui
}

// hackShowToolsAllowed computes, once per tools/list, whether each
// hack_show_* tool AND every hack_app_* twin (hack_app_only_tools.go)
// should appear in this connection's listing -- one entry per tool name,
// each from that tool's (or that tool's app's) own *Allowed function (its
// own flag on top of the shared hackAppsGate). A tool with no entry here
// is left alone by filterHackShowTools below.
//
// The twins were originally listed unconditionally for every events
// connection: `_meta.ui.visibility: ["app"]` only withholds a tool from
// the model for a host that honours it, so a flagged-off, excluded-app-
// name or non-UI connection could still see (and, since each twin's own
// run() used to check only c.uiClient, call) one simply by asking for
// tools/list (owner follow-up review, SIM-344 phase 2). Filtering them
// here, the same way hack_show_judge/hack_show_organiser already are, is
// the fix; hackAppOnlyTwin's own run() gate (hack_app_only_tools.go) is
// the matching call-time half, so a tool hidden from listing is also
// refused if called by name anyway.
func (s *Server) hackShowToolsAllowed(c Caller, uiCapable bool) map[string]bool {
	allowed := map[string]bool{}
	for _, entry := range s.inventory() {
		if entry.policy.showAllowed != nil {
			allowed[entry.tool.Name] = entry.policy.showAllowed(s, c, uiCapable)
		}
	}
	return allowed
}

// filterHackShowTools removes any hack_show_* tool from a tools/list
// result whose own entry in allowed says this connection should not see
// it. Every other tool is untouched, so an unrelated caller's listing is
// unaffected byte for byte (TestHackAppsGatesAndUnchangedInventory's own
// invariant extended to each new show tool).
func filterHackShowTools(list []Tool, allowed map[string]bool) []Tool {
	out := make([]Tool, 0, len(list))
	for _, tool := range list {
		if ok, named := allowed[tool.Name]; named && !ok {
			continue
		}
		out = append(out, tool)
	}
	return out
}

func hackShowJudgeTool() Tool {
	return Tool{
		policy: toolPolicy{screen: "hack-judge", showAllowed: hackAppOnlyJudgeAllowed},
		Name:   "hack_show_judge",
		Title:  "Show the judge view",
		Description: "Open the judge app view in chat: invite (accept a judge invite, needs code), queue (My teams), score (one entry's scoring, optional team_id) or rubric. " +
			"Only offered to a connection whose app can render it; every judging action also has a plain tool (hack_get_judge_queue, hack_score_team, hack_declare_my_conflict, hack_get_rubric, hack_preview_judge, hack_join_judge) that works the same everywhere.",
		InputSchema: object(map[string]any{
			"slug":    str("Event name. Required for view queue, score or rubric."),
			"code":    str("Code from the judge invite link. Required for view invite."),
			"view":    map[string]any{"type": "string", "enum": []string{"invite", "queue", "score", "rubric"}, "description": "Which judge view to open."},
			"team_id": str("Team ID from the judge queue. Optional for view score: opens that team directly instead of the first unscored one."),
		}, "view"),
		OutputSchema: outObject(map[string]any{
			"view":    outString("The view that was opened."),
			"slug":    outString("Event name, once known."),
			"code":    outString("The judge invite code, for view invite."),
			"team_id": outString("Team ID passed through, for view score."),
			"event":   anyJSON("Event detail for the opened view (omitted for view invite, which has not joined yet)."),
		}, "view"),
		Annotations: writes(false, true, false),
		run:         runHackShowJudge,
	}
}

func runHackShowJudge(c *call, args map[string]any) (output, error) {
	if !c.server.hackShowJudgeAppAllowed(c.caller, c.uiClient) {
		return output{}, errors.New("hack_show_judge only opens in an app that can render it; use hack_get_judge_queue, hack_score_team, hack_get_rubric, hack_preview_judge or hack_join_judge instead")
	}
	view, err := stringArg(args, "view")
	if err != nil {
		return output{}, err
	}
	switch view {
	case "invite":
		code, err := stringArg(args, "code")
		if err != nil {
			return output{}, errors.New("code is required for view invite")
		}
		res := c.do(http.MethodGet, "/v1/hack/judge/"+url.PathEscape(code), nil, nil)
		if !res.ok() {
			return output{}, hackRestError("hack_show_judge", res)
		}
		structured := map[string]any{"view": "invite", "code": code}
		// No organiser-authored title (or anything else from the REST body)
		// in the text: a fixed sentence only (Grok security review,
		// SIM-344) -- that text is as visible to the model as to the
		// frame, and an event title is free text an organiser chose, not
		// something this tool should ever need to repeat back.
		return output{Text: "Opened the judge invite.", Structured: structured}, nil
	case "queue", "score", "rubric":
		slug, err := eventSlug(args)
		if err != nil {
			return output{}, err
		}
		res := c.do(http.MethodGet, "/v1/hack/events/"+url.PathEscape(slug), nil, nil)
		if !res.ok() {
			return output{}, hackRestError("hack_show_judge", res)
		}
		var body struct {
			Event map[string]any `json:"event"`
			Role  string         `json:"role"`
		}
		if err := json.Unmarshal(res.body, &body); err != nil {
			return output{}, errors.New("hack_show_judge: server returned invalid JSON")
		}
		// REST already scopes what a non-member sees of an event, but this
		// tool's whole job is opening a JUDGING view, so it refuses
		// outright for anyone who isn't a judge or organiser of this
		// event -- the same two roles hack_get_judge_queue's own REST
		// route allows -- rather than opening a view with nothing useful
		// in it. No event data in the error (Grok security review,
		// SIM-344): a refusal carries no structuredContent at all, same
		// as every other tool's (TestErrorResultsCarryNoStructuredContent).
		if body.Role != "judge" && body.Role != "organiser" {
			return output{}, errors.New("hack_show_judge is for a judge of this event")
		}
		structured := map[string]any{"view": view, "slug": slug, "event": hackShowJudgeEventFields(body.Event)}
		if teamID, _ := args["team_id"].(string); teamID != "" {
			structured["team_id"] = teamID
		}
		// Fixed sentences, no event title or other free text (Grok
		// security review, SIM-344): slug is a constrained, organiser-
		// chosen identifier (3 to 39 lowercase letters, numbers and
		// hyphens; hack_check_event_name enforces the shape), not open
		// text, so it stays.
		verb := map[string]string{"queue": "the judging queue", "score": "scoring", "rubric": "the rubric"}[view]
		out := output{Text: "Opened " + verb + " for " + slug + ".", Structured: structured}
		// About/rules/prizes (judge-rubric.js's brief card) ride in _meta,
		// not content or structuredContent (coordinator follow-up, SIM-344:
		// Grok finding 2 was "no organiser prose reaches the model", and
		// these three are exactly that -- the one place they had landed,
		// after the first round of fixes, was still inside structuredContent,
		// which a model sees the same as the view does). _meta on a tool
		// result is this codebase's own established channel for "the view
		// needs this, the model does not": site_analytics already puts its
		// daily chart series there (tools.go, "would swamp a conversation"),
		// read by my-sites.js via SHApp.callToolResult (the bridge method
		// that keeps _meta; the plain callTool it also offers returns only
		// structuredContent, discarding _meta -- bridge.js's own doc
		// comment: "Raw results retain host-only metadata"). The glue
		// script (hack-judge-view.js's refreshEvent) reads it the same way
		// and merges it back onto ctx.event, so judge-rubric.js needs no
		// change at all to keep reading ev.about/ev.rules/ev.prizes.
		if text := hackShowJudgeEventText(body.Event); len(text) > 0 {
			out.Meta = map[string]any{"simple-hack/event-text": text}
		}
		return out, nil
	default:
		return output{}, errors.New("view must be invite, queue, score or rubric")
	}
}

// hackShowJudgeEventFields is the only shape of an event this tool's
// content/structuredContent ever carries, in the model's own context as
// much as the view's: the fields the judge screens read that double as
// judging state a model might reasonably act on (scoring_open,
// judging_locked_at, results_published, submission_deadline -- hack_get_
// event's own output deliberately drops these for the model, see this
// file's comment on refreshEvent in hack-judge-view.js), never the full
// raw REST body, which can carry an organiser's join_code/judge_code when
// the caller is also that event's organiser (Grok security review,
// SIM-344). about/rules/prizes are organiser-written prose with no
// judging-state reason to reach the model at all; see hackShowJudgeEventText.
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

// hackShowJudgeEventText is judge-rubric.js's "About this event" card
// (about/rules/prizes) and nothing else -- view-only, carried in the tool
// result's _meta rather than content or structuredContent, which a model
// sees exactly as the view does.
func hackShowJudgeEventText(event map[string]any) map[string]any {
	out := map[string]any{}
	for _, key := range []string{"about", "rules", "prizes"} {
		if v, ok := event[key]; ok {
			out[key] = v
		}
	}
	return out
}
