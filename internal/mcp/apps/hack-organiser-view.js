// Hand-written glue between the host bridge (apps/bridge.js) and the
// bundled, otherwise-unmodified organiser screens (hack-organiser-
// bundle.js: organiser-overview.js, organiser-people.js, organiser-
// judging.js and their shared _orgA-util.js). Not generated: this is the
// MCP-specific adapter, the same role hack-judge-view.js plays for the
// judge app.
//
// It gives each screen's render(ctx) a ctx shaped like the real website's
// (main, api, ui, event, me, params, go, refresh, alive), with api()
// backed by tools/call instead of fetch -- the frame holds no credential
// and the CSP's connectDomains is empty, so there is no other way for a
// screen to read or change anything. The route table below maps every
// REST path and method the three organiser screens call to the MCP tool
// that does the same thing; every row's method+path matches the
// hackRouteTool/hand-written-tool registration it stands for (internal/
// mcp/hack_organiser_tools.go, hack_tools.go), checked by
// TestHackOrganiserRouteMapCoversCalls.
(function () {
  'use strict';
  var screens = window.__HACKO.screens;
  var ui = window.__HACKO.ui;

  // --- organiser-scoped wrapper around the bridge ----------------------
  // Every tool this app ever has a reason to call, by name (same
  // allowlist discipline as the judge app). This does NOT stop an
  // attacker-injected script element or an onerror= handler -- the nonce
  // CSP (hack-organiser.html's script-src 'nonce-...') is the control
  // that actually holds against that, since injected code runs outside
  // this bundle's own nonced script block and cannot execute at all. What
  // this narrows is code that DOES already run inside this bundle: a bug in
  // this file, or a future edit to a reused screen, reaching a tool this
  // connection's role might otherwise be allowed to call (a judge's or
  // participant's own tools, or a later organiser phase's teams/results/
  // event-page tools that this view never opens) (Grok security review
  // finding 3, SIM-344 phase 2, correcting this comment's earlier
  // overstatement). The server is the real backstop regardless (every
  // REST call is independently role- and membership-checked no matter
  // which tool name reached it) -- this is defence in depth, not the
  // security boundary. Also caps concurrent calls at 4 and serialises
  // writes (plan section 6): an organiser tapping Approve on several
  // applications in a row, or ticking several score-affecting settings,
  // must not let a later tap's save overtake an earlier one.
  // hack_app_* names are app-only twins (Grok security review finding 1,
  // SIM-344 phase 2, internal/mcp/hack_app_only_tools.go): the view calls
  // these, never the originals, for every read it makes on its own and
  // for the four writes whose REST body carries a secret the show tool
  // deliberately withholds (a new join/judge link, a co-organiser
  // invite, or -- hack_app_set_judging_ends_at, 0003-hack-notices-judges-
  // messages.sql round 2 -- an organiser output that happens to carry
  // the join/judge codes alongside the one field this card actually
  // sets). The originals (hack_get_people, hack_get_applications,
  // hack_update_event, ...) stay reachable only for an explicit chat
  // request, through the model's own tool list -- this view never calls
  // them.
  var ORGANISER_TOOLS = {
    hack_show_organiser: 1, hack_app_get_people: 1, hack_app_get_applications: 1,
    hack_app_regenerate_code: 1, hack_app_set_event_stage: 1, hack_app_get_registration: 1,
    hack_set_registration: 1, hack_decide_application: 1, hack_remove_person: 1,
    hack_revoke_person_key: 1, hack_remove_organiser: 1, hack_app_create_organiser_invite: 1,
    hack_revoke_organiser_invite: 1, hack_app_get_rubric: 1, hack_set_rubric: 1,
    hack_app_get_judging_settings: 1, hack_set_judging_settings: 1, hack_app_get_conflicts: 1,
    hack_set_judge_conflict: 1, hack_remove_judge_conflict: 1, hack_app_get_teams: 1,
    hack_app_get_assignments: 1, hack_generate_assignments: 1, hack_app_get_judging_dashboard: 1,
    hack_delete_former_judge_scores: 1, hack_lock_judging: 1, hack_unlock_judging: 1,
    hack_app_set_judging_ends_at: 1
  };
  var WRITE_TOOLS = {
    hack_app_regenerate_code: 1, hack_app_set_event_stage: 1, hack_decide_application: 1,
    hack_remove_person: 1, hack_revoke_person_key: 1, hack_remove_organiser: 1,
    hack_app_create_organiser_invite: 1, hack_revoke_organiser_invite: 1, hack_set_registration: 1,
    hack_set_rubric: 1, hack_set_judging_settings: 1, hack_set_judge_conflict: 1,
    hack_remove_judge_conflict: 1, hack_generate_assignments: 1, hack_delete_former_judge_scores: 1,
    hack_lock_judging: 1, hack_unlock_judging: 1, hack_app_set_judging_ends_at: 1
  };
  var MAX_CONCURRENT = 4;
  var inFlight = 0;
  var waiting = [];
  function acquireSlot() {
    if (inFlight < MAX_CONCURRENT) { inFlight++; return Promise.resolve(); }
    return new Promise(function (resolve) { waiting.push(resolve); });
  }
  function releaseSlot() {
    inFlight--;
    var next = waiting.shift();
    if (next) { inFlight++; next(); }
  }
  var writeQueue = Promise.resolve();
  var realCallTool = window.SHApp.callTool.bind(window.SHApp);
  var realCallToolResult = window.SHApp.callToolResult.bind(window.SHApp);
  // Shared by both bridge entry points below, same as the judge app's
  // guardedCall: the allowlist, concurrency cap and write-serialisation
  // apply the same way regardless of which one a caller used.
  function guardedCall(name, args, invoke) {
    if (!ORGANISER_TOOLS[name]) return Promise.reject(new Error(name + ' is not a tool the organiser app calls'));
    var run = function () { return acquireSlot().then(function () { return invoke(name, args).then(function (r) { releaseSlot(); return r; }, function (e) { releaseSlot(); throw e; }); }); };
    if (!WRITE_TOOLS[name]) return run();
    var result = writeQueue.then(run, run);
    writeQueue = result.then(function () {}, function () {});
    return result;
  }
  // Replaced in place, then locked (Grok security review finding 3,
  // SIM-344 phase 2) -- a shadow copy (Object.assign({}, window.SHApp,
  // {...})) left window.SHApp.callTool/callToolResult themselves
  // unguarded, reachable by any other code running inside this bundle
  // that reads window.SHApp directly instead of the local `app` this file
  // builds. writable:false, configurable:false stops that same code from
  // reassigning them back. Not a blanket Object.freeze(window.SHApp):
  // bridge.js's own start() later does `window.SHApp.hostCapabilities =
  // ...` (its one legitimate post-construction mutation, run from this
  // file's own app.start() call below) -- 'use strict' there turns that
  // assignment into a thrown TypeError the instant the whole object is
  // frozen, which breaks every connection; locking only the two methods
  // this allowlist exists for avoids that while still making them
  // tamper-proof.
  Object.defineProperty(window.SHApp, 'callTool', { value: function (name, args) { return guardedCall(name, args, realCallTool); }, writable: false, configurable: false });
  Object.defineProperty(window.SHApp, 'callToolResult', { value: function (name, args) { return guardedCall(name, args, realCallToolResult); }, writable: false, configurable: false });
  var app = window.SHApp;

  // --- route table: REST (method, path) -> MCP tool -------------------
  // opts.body lists the REST body fields this route forwards. opts.wrap
  // (hackOrganiserRoutes' generic hackRouteTool shape, hack_organiser_
  // tools.go) nests them under a single `body` argument; omitted
  // (hack_set_event_stage, hack_set_rubric -- both hand-written tools with
  // flat schemas, hack_tools.go) sends each one as its own top-level
  // argument instead. opts.query lists REST query parameters forwarded as
  // flat top-level arguments (hack_remove_judge_conflict's judge_user_id).
  // opts.unwrap marks a route whose tool is a generic hackRouteTool
  // (hack_organiser_tools.go's own HackOrganiserTools): its structured
  // result is always `{response, status}` -- the REST body one level
  // down, under `response` -- unlike a hand-written tool (hack_set_
  // event_stage, hack_get_rubric/hack_set_rubric), whose structured
  // result already IS the shape the reused screens read directly. Without
  // this, e.g. organiser-people.js's `people = p.data || []` would get
  // `{response:[...], status:200}` (a truthy object, not an array) and
  // every `.filter()`/`.map()` on it would throw -- caught live, not by
  // reading hack_organiser_tools.go side by side, when the People view's
  // own render() failed on exactly that in the browser harness.
  // opts.appOnly marks a route whose tool is an app-only twin
  // (hack_app_get_people and the rest, internal/mcp/hack_app_only_tools.go,
  // Grok security review finding 1, SIM-344 phase 2): api() calls it
  // through callToolResult instead of callTool and reads the real data
  // out of the result's own _meta (see api()'s own comment below), since
  // the twin's own content/structuredContent is deliberately reduced to a
  // fixed sentence and small counts. unwrap still applies afterwards,
  // against whatever shape came out of _meta -- a twin's _meta payload is
  // byte-for-byte the wrapped tool's own original structuredContent, so
  // the same wrap/no-wrap split holds.
  // opts.wrapEvent marks the one hand-written-tool exception to "the
  // structured result already IS the shape the reused screens read
  // directly" above: hack_update_event's own output (internal/mcp/
  // hack_event_tools.go's hackEventOutput) is a flat object (slug, stage,
  // ... -- shaped for the model, which never reads it nested), but
  // organiser-judging.js's onSaveCloses, unmodified from the website,
  // reads the PATCH response the website's real REST route returns:
  // `{event: {...}}`. Reshaping hackEventOutput itself would change what
  // every other caller of hack_update_event (the model, any other MCP
  // client) sees, for one screen's convenience, so this nests the tool's
  // own flat result under `event` here instead, after unwrap/appOnly.
  // The PATCH route below combines this with appOnly: hack_app_set_
  // judging_ends_at's _meta is byte-for-byte hack_update_event's own
  // structuredContent (hackAppOnlyTwin wraps it unchanged), so wrapEvent
  // nests that same flat object the same way once appOnly has pulled it
  // out of _meta.
  function route(method, pattern, tool, params, opts) {
    opts = opts || {};
    return {
      method: method, re: new RegExp('^' + pattern + '$'), tool: tool, params: params,
      bodyKeys: opts.body || null, wrap: !!opts.wrap, queryKeys: opts.query || null,
      unwrap: !!opts.unwrap, appOnly: !!opts.appOnly, successStatus: opts.status || 200,
      wrapEvent: !!opts.wrapEvent
    };
  }
  var SEG = '([^/]+)';
  var routes = [
    route('GET', '/v1/hack/events/' + SEG + '/people', 'hack_app_get_people', ['slug'], { unwrap: true, appOnly: true }),
    route('GET', '/v1/hack/events/' + SEG + '/applications', 'hack_app_get_applications', ['slug'], { unwrap: true, appOnly: true }),
    route('POST', '/v1/hack/events/' + SEG + '/codes/' + SEG, 'hack_app_regenerate_code', ['slug', 'kind'], { unwrap: true, appOnly: true }),
    route('POST', '/v1/hack/events/' + SEG + '/stage', 'hack_app_set_event_stage', ['slug'], { body: ['stage'], appOnly: true }),
    route('GET', '/v1/hack/events/' + SEG + '/registration', 'hack_app_get_registration', ['slug'], { unwrap: true, appOnly: true }),
    route('PUT', '/v1/hack/events/' + SEG + '/registration', 'hack_set_registration', ['slug'], { wrap: true, unwrap: true, body: ['questions', 'approval_required', 'admit_pending'] }),
    route('POST', '/v1/hack/events/' + SEG + '/applications/' + SEG + '/decision', 'hack_decide_application', ['slug', 'user_id'], { wrap: true, unwrap: true, body: ['decision'] }),
    route('DELETE', '/v1/hack/events/' + SEG + '/people/' + SEG + '/key', 'hack_revoke_person_key', ['slug', 'user_id'], { unwrap: true }),
    route('DELETE', '/v1/hack/events/' + SEG + '/people/' + SEG, 'hack_remove_person', ['slug', 'user_id'], { unwrap: true }),
    route('DELETE', '/v1/hack/events/' + SEG + '/organisers/' + SEG, 'hack_remove_organiser', ['slug', 'user_id'], { unwrap: true }),
    route('POST', '/v1/hack/events/' + SEG + '/organiser-invite', 'hack_app_create_organiser_invite', ['slug'], { unwrap: true, appOnly: true }),
    route('DELETE', '/v1/hack/events/' + SEG + '/organiser-invite', 'hack_revoke_organiser_invite', ['slug'], { unwrap: true }),
    route('GET', '/v1/hack/events/' + SEG + '/rubric', 'hack_app_get_rubric', ['slug'], { appOnly: true }),
    route('PUT', '/v1/hack/events/' + SEG + '/rubric', 'hack_set_rubric', ['slug'], { body: ['criteria', 'confirm_score_deletion'] }),
    route('GET', '/v1/hack/events/' + SEG + '/judging/settings', 'hack_app_get_judging_settings', ['slug'], { unwrap: true, appOnly: true }),
    route('PATCH', '/v1/hack/events/' + SEG + '/judging/settings', 'hack_set_judging_settings', ['slug'], { wrap: true, unwrap: true, body: ['assignment_mode', 'judges_per_team', 'tie_criterion_id'] }),
    route('GET', '/v1/hack/events/' + SEG + '/conflicts', 'hack_app_get_conflicts', ['slug'], { unwrap: true, appOnly: true }),
    route('POST', '/v1/hack/events/' + SEG + '/conflicts', 'hack_set_judge_conflict', ['slug'], { wrap: true, unwrap: true, body: ['team_id', 'judge_user_id'] }),
    route('DELETE', '/v1/hack/events/' + SEG + '/conflicts/' + SEG, 'hack_remove_judge_conflict', ['slug', 'team_id'], { query: ['judge_user_id'], unwrap: true, status: 204 }),
    route('GET', '/v1/hack/events/' + SEG + '/teams', 'hack_app_get_teams', ['slug'], { unwrap: true, appOnly: true }),
    route('GET', '/v1/hack/events/' + SEG + '/assignments', 'hack_app_get_assignments', ['slug'], { unwrap: true, appOnly: true }),
    route('POST', '/v1/hack/events/' + SEG + '/assignments/generate', 'hack_generate_assignments', ['slug'], { wrap: true, unwrap: true, body: ['include_organisers'] }),
    route('GET', '/v1/hack/events/' + SEG + '/judging/dashboard', 'hack_app_get_judging_dashboard', ['slug'], { unwrap: true, appOnly: true }),
    route('DELETE', '/v1/hack/events/' + SEG + '/judging/former-judges/' + SEG, 'hack_delete_former_judge_scores', ['slug', 'user_id'], { wrap: true, unwrap: true, body: ['confirm'] }),
    route('POST', '/v1/hack/events/' + SEG + '/judging/lock', 'hack_lock_judging', ['slug'], { unwrap: true }),
    route('POST', '/v1/hack/events/' + SEG + '/judging/unlock', 'hack_unlock_judging', ['slug'], { wrap: true, unwrap: true, body: ['reason'] }),
    // organiser-judging.js's "Judging closes" card (onSaveCloses/onClearCloses,
    // notices contract 2026-11-05): the app-only twin (0003-hack-notices-
    // judges-messages.sql round 2, Codex review), not hack_update_event
    // directly -- the general event-patch tool's own output carries the
    // organiser's join/judge codes whenever the caller is the organiser,
    // which this card opening/saving on its own, with nobody in chat
    // having asked for it, must not put in front of the model (the same
    // reasoning as every other appOnly route above). judging_ends_at is
    // still the only field this card ever sends.
    route('PATCH', '/v1/hack/events/' + SEG, 'hack_app_set_judging_ends_at', ['slug'], { body: ['judging_ends_at'], wrapEvent: true, appOnly: true })
  ];

  function matchRoute(method, path) {
    var parts = path.split('?');
    var clean = parts[0], query = parts[1] || '';
    for (var i = 0; i < routes.length; i++) {
      if (routes[i].method !== method) continue;
      var m = routes[i].re.exec(clean);
      if (m) return { def: routes[i], captures: m.slice(1), query: query };
    }
    return null;
  }

  // Mirrors hackRestError/restError's wire format, same as the judge
  // app's parseToolError (see that file's comment for why this is parsed
  // back out of a plain Error rather than carried as structured data).
  function parseToolError(err) {
    var msg = (err && err.message) || 'Something went wrong.';
    var first = msg.split('\n')[0];
    var statusMatch = /\(HTTP (\d+)\)/.exec(first);
    var codeMatch = /\[([a-z0-9_]+)\]\s*$/i.exec(first);
    var sentence = first
      .replace(/^\S+ failed \(HTTP \d+\):\s*/, '')
      .replace(/\s*\[[a-z0-9_]+\]\s*$/i, '')
      .trim();
    return { status: statusMatch ? Number(statusMatch[1]) : 0, code: codeMatch ? codeMatch[1] : '', error: sentence || 'Something went wrong.' };
  }

  // After a write, the model is told what happened in one line this glue
  // writes itself -- never the raw organiser- or participant-written text
  // (an applicant's name, a rubric criterion, a judge's display name) the
  // action touched, both because app-only visibility does not keep that
  // text out of model context once it is repeated there (INTENT) and
  // because hostile text would otherwise become a prompt-injection path
  // straight into the organiser's chat (plan section 6). Every sentence
  // below is fixed; the only values ever interpolated are closed,
  // organiser-chosen enums (a code "kind" of join/judge, an application
  // "decision" of approved/rejected) -- never a name, title or free-text
  // field.
  var MODEL_CONTEXT_SUMMARY = {
    hack_app_regenerate_code: function (args) { return 'Made a new ' + (args.kind === 'judge' ? 'judge' : 'join') + ' link.'; },
    hack_app_set_event_stage: function () { return 'Changed the event stage.'; },
    hack_decide_application: function (args) {
      var decision = args.body && args.body.decision;
      return decision === 'approved' ? 'Approved an application.' : 'Rejected an application.';
    },
    hack_remove_person: function () { return 'Removed a person from the event.'; },
    hack_revoke_person_key: function () { return "Turned off a person's publishing key."; },
    hack_remove_organiser: function () { return 'Removed a co-organiser.'; },
    hack_app_create_organiser_invite: function () { return 'Created a co-organiser invitation.'; },
    hack_revoke_organiser_invite: function () { return 'Revoked the co-organiser invitation.'; },
    hack_set_registration: function () { return 'Saved sign-up questions and approval settings.'; },
    hack_set_rubric: function () { return 'Saved the judging rubric.'; },
    hack_set_judging_settings: function () { return 'Saved judging settings.'; },
    hack_set_judge_conflict: function () { return 'Recorded a judge conflict.'; },
    hack_remove_judge_conflict: function () { return 'Removed a judge conflict.'; },
    hack_generate_assignments: function () { return 'Generated judge assignments.'; },
    hack_delete_former_judge_scores: function () { return "Deleted a departed judge's scores."; },
    hack_lock_judging: function () { return 'Locked judging.'; },
    hack_unlock_judging: function () { return 'Unlocked judging.'; },
    // This route only ever sends judging_ends_at (the route table's own
    // body: ['judging_ends_at']), so the summary can be specific.
    hack_app_set_judging_ends_at: function (args) { return args.judging_ends_at ? 'Set when judging closes.' : 'Cleared when judging closes.'; }
  };

  // The tool-backed api(): same {ok, status, data} shape as the website's
  // real api.js, so every reused screen's `if (!r.ok)` / `errText(r)` /
  // `r.status === ...` check works unchanged. Never rejects -- a REST
  // refusal resolves with ok:false, exactly like the real fetch-based one.
  async function api(method, path, body) {
    var matched = matchRoute(method, path);
    if (!matched) return { ok: false, status: 0, data: { error: 'This action is not available here.' } };
    var def = matched.def;
    var args = {};
    for (var i = 0; i < def.params.length; i++) args[def.params[i]] = decodeURIComponent(matched.captures[i]);
    if (def.queryKeys && matched.query) {
      var qs = new URLSearchParams(matched.query);
      def.queryKeys.forEach(function (k) { var v = qs.get(k); if (v) args[k] = v; });
    }
    if (def.bodyKeys && body && typeof body === 'object') {
      var payload = {};
      for (var k = 0; k < def.bodyKeys.length; k++) {
        var key = def.bodyKeys[k];
        if (Object.prototype.hasOwnProperty.call(body, key)) payload[key] = body[key];
      }
      if (def.wrap) args.body = payload;
      else Object.assign(args, payload);
    }
    try {
      var data;
      if (def.appOnly) {
        // The twin's own content/structuredContent is a fixed sentence
        // and small counts (model-safe by construction, hack_app_only_
        // tools.go) -- the real REST body this screen actually renders
        // travels only in the result's own _meta, under "simple-hack/
        // app-only". callToolResult (not callTool, the model-facing path,
        // which only ever returns structuredContent and silently
        // discards _meta) is the one bridge entry point that keeps it.
        var twinResult = await app.callToolResult(def.tool, args);
        data = (twinResult._meta && twinResult._meta['simple-hack/app-only']) || null;
      } else {
        data = await app.callTool(def.tool, args);
      }
      if (def.unwrap) data = (data && Object.prototype.hasOwnProperty.call(data, 'response')) ? data.response : data;
      if (def.wrapEvent) data = { event: data };
      var summarize = MODEL_CONTEXT_SUMMARY[def.tool];
      if (summarize) app.updateModelContext(summarize(args)).catch(function () {});
      return { ok: true, status: def.successStatus, data: data };
    } catch (e) {
      var parsed = parseToolError(e);
      return { ok: false, status: parsed.status, data: { error: parsed.error, code: parsed.code } };
    }
  }
  function isSignedOut() { return false; }

  // --- in-frame routing -------------------------------------------------
  var IN_FRAME_VIEWS = { overview: 1, people: 1, judging: 1 };
  // VIEW_ORDER/VIEW_LABELS back the view switcher (addViewSwitcher, below):
  // the one-button Overview redesign (teams-redesign branch) removed every
  // in-frame link Overview used to carry into People or Judging ("Sign-up
  // questions", "Set up judging" -- both used to be `<a href="#v=...">`,
  // now a confirm-driven stage-change button that stays on Overview). With
  // no organic link left anywhere in the three reused screens (People and
  // Judging never linked to each other either -- see the click-handler
  // comment below), an organiser working from chat would otherwise have no
  // way to reach People or Judging without the model reopening the view
  // with a fresh hack_show_organiser call. This switcher is frame-level
  // chrome, not part of any reused screen, so it survives regardless of
  // what a given screen's own render() does with `main`.
  var VIEW_ORDER = ['overview', 'people', 'judging'];
  var VIEW_LABELS = { overview: 'Home', people: 'People', judging: 'Judging' };
  var main = document.getElementById('app');
  // history is a tiny back-stack of {view, params}, pushed on every normal
  // navigation and popped by goBack() -- same purpose as the judge app's:
  // none of these three screens have a Back control of their own (that
  // normally comes from the shell this frame leaves out), so switching
  // People -> Judging and back has to actually return to People, not just
  // bounce to Overview.
  var state = { slug: '', orgData: null, view: '', params: {}, history: [] };
  // renderGeneration is the same "superseded navigation" guard as the real
  // app.js's own `turn === generation` (nav.js/app.js, see that file's
  // comment): bumped once per renderView() call, captured by ctxFor, so
  // organiser-overview.js's own `if (!ctx.alive()) return` after its async
  // /people and /applications fetch correctly aborts if a newer navigation
  // (another renderView() call) started before that fetch resolved.
  var renderGeneration = 0;
  function paramsObj(map) {
    return { get: function (key) { return Object.prototype.hasOwnProperty.call(map, key) ? map[key] : ''; } };
  }
  function ctxFor(view, params, generation) {
    return {
      main: main,
      api: { api: api, isSignedOut: isSignedOut },
      ui: ui,
      event: state.orgData || {},
      // ctx.me.id comes from _meta's me_id (hack_apps_organiser.go's
      // hackShowOrganiserViewOnly, Codex review finding 6, SIM-344 phase
      // 2): the signed-in organiser's own user_id, the same value the
      // people list already shows for every OTHER member, so organiser-
      // people.js's "(you)"/"(you, creator)" labels can finally match a
      // row. Every other use of that row (Remove co-organiser) stays
      // keyed by the row's own user_id, unaffected either way.
      me: { username: 'me', id: (state.orgData && state.orgData.me && state.orgData.me.id) || '', display_name: (state.orgData && state.orgData.me && state.orgData.me.display_name) || '' },
      params: paramsObj(params || {}),
      alive: function () { return generation === renderGeneration; },
      // SIM-341: no announcements route here, so organiser-overview.js skips
      // its "Tell participants?" dialog; the AI app offers it instead.
      noAnnounce: true,
      refresh: function () { return renderView(state.view, state.params, { replace: true }); },
      go: function (path) {
        var q = (path.split('?')[1] || '');
        var parts = {};
        q.split('&').forEach(function (pair) {
          if (!pair) return;
          var kv = pair.split('=');
          parts[decodeURIComponent(kv[0])] = decodeURIComponent(kv[1] || '');
        });
        renderView(parts.s || view, parts);
      }
    };
  }
  function showError(message) {
    main.innerHTML = '';
    var note = document.createElement('div');
    note.className = 'note bad';
    note.setAttribute('role', 'alert');
    note.textContent = message;
    main.append(note);
  }
  // Builds the ctx.event/ctx.me shape from one hack_show_organiser
  // result's structuredContent plus its own _meta -- shared by refreshEvent
  // and the bootstrap tool-result handler below, so the two can never
  // drift on which _meta fields they each pick up.
  function buildOrgData(data, meta) {
    var organiser = Object.assign({}, data.organiser);
    var me = {};
    if (meta) {
      if (meta.join_url) organiser.join_url = meta.join_url;
      if (meta.judge_url) organiser.judge_url = meta.judge_url;
      if (meta.display_name) me.display_name = meta.display_name;
      if (meta.me_id) me.id = meta.me_id;
    }
    return { event: data.event || {}, role: 'organiser', can: data.can || {}, organiser: organiser, admin_view: !!data.admin_view, me: me };
  }
  // Re-reads the event through hack_show_organiser itself, the same
  // pattern (and the same reason) as the judge app's refreshEvent: called
  // before every render (including ctx.refresh(), used after a stage
  // change, a new join/judge link or ending the event), so a frame left
  // open across another organiser's own change -- or this one's own write
  // -- never shows stale state. Uses callToolResult, not callTool, so the
  // view-only _meta (join_url/judge_url/display_name/me_id) comes
  // through; see hack_apps_organiser.go's hackShowOrganiserViewOnly.
  //
  // Returns {status: 'ok'|'stale'|'refused'|'superseded', message?}:
  // 'ok' -- state.orgData was updated from a fresh, authoritative read.
  // 'stale' -- the read failed transiently (network blip, timeout); the
  //   last known state.orgData, if any, is left in place rather than
  //   blanking the screen over a one-off failure.
  // 'refused' -- the read failed with an AUTHORITATIVE refusal (401/403/
  //   404: no longer a member, the event is gone, the connection expired)
  //   -- state.orgData is cleared, since continuing to show old counts,
  //   links and write controls for an event the organiser no longer has
  //   would be actively misleading, not merely stale (Codex review
  //   finding 2, SIM-344 phase 2).
  // 'superseded' -- generation or slug no longer match what this call
  //   started with: a newer navigation (possibly a fresh hack_show_
  //   organiser open for a DIFFERENT event, which updates state.slug
  //   before this one's response arrives) already owns the frame, so this
  //   result must never be committed to state.orgData at all, not even to
  //   discard it as a refusal (Codex review finding 3, SIM-344 phase 2).
  async function refreshEvent(view, generation) {
    var expectedSlug = state.slug;
    var superseded = function () { return generation !== renderGeneration || state.slug !== expectedSlug; };
    try {
      var toolView = IN_FRAME_VIEWS[view] ? view : 'overview';
      var result = await app.callToolResult('hack_show_organiser', { view: toolView, slug: expectedSlug });
      if (superseded()) return { status: 'superseded' };
      var data = result.structuredContent;
      if (!data) return { status: 'stale' };
      state.orgData = buildOrgData(data, result._meta && result._meta['simple-hack/organiser-view']);
      return { status: 'ok' };
    } catch (e) {
      if (superseded()) return { status: 'superseded' };
      var parsed = parseToolError(e);
      if (parsed.status === 401 || parsed.status === 403 || parsed.status === 404) {
        state.orgData = null;
        return { status: 'refused', message: parsed.error || 'This event is no longer available.' };
      }
      return { status: 'stale' };
    }
  }
  async function renderView(view, params, opts) {
    opts = opts || {};
    if (!IN_FRAME_VIEWS[view]) view = 'overview';
    var generation = ++renderGeneration;
    if (!opts.replace && state.view && state.view !== view) {
      state.history.push({ view: state.view, params: state.params });
    }
    state.view = view; state.params = params || {};
    if (!opts.skipRefresh) {
      var refreshed = await refreshEvent(view, generation);
      if (refreshed.status === 'superseded' || generation !== renderGeneration) return;
      if (refreshed.status === 'refused') { showError(refreshed.message || 'Could not load this event. Ask the model to reopen the organiser view.'); return; }
      if (!state.orgData) { showError('Could not load this event. Ask the model to reopen the organiser view.'); return; }
    } else if (!state.orgData) {
      showError('Could not load this event. Ask the model to reopen the organiser view.'); return;
    }
    var screen = screens[view];
    if (!screen) { showError('That view is not available.'); return; }
    try {
      await screen.render(ctxFor(view, state.params, generation));
    } catch (e) {
      // A superseded render must never stomp whatever newer screen
      // already owns `main` with an error note of its own (Codex review
      // finding 4, SIM-344 phase 2): the unconditional catch used to do
      // exactly that when a stale People or Judging render, left running
      // past a newer navigation, queried an element the newer screen had
      // already removed and threw.
      if (generation !== renderGeneration) return;
      showError('Something went wrong showing this view. Try again.');
      return;
    }
    if (generation !== renderGeneration) return;
    if (state.history.length) addBackControl();
    addViewSwitcher();
  }
  function goBack() {
    var prev = state.history.pop();
    if (!prev) { renderView('overview', {}, { replace: true }); return; }
    renderView(prev.view, prev.params, { replace: true });
  }
  function addBackControl() {
    if (main.querySelector('[data-app-back]')) return;
    var back = document.createElement('button');
    back.type = 'button';
    back.className = 'btn sm';
    back.setAttribute('data-app-back', '');
    back.style.marginBottom = '14px';
    back.textContent = '‹ Back';
    back.addEventListener('click', goBack);
    main.insertBefore(back, main.firstChild);
  }
  // addViewSwitcher: always present (not gated on history, unlike Back),
  // since it is the organiser's only way into People or Judging now that
  // Overview carries no in-frame link to either -- see VIEW_ORDER's own
  // comment above. Re-added after every render() (each screen replaces
  // `main.innerHTML` wholesale, same reason addBackControl re-adds itself
  // every time), inserted above Back so the fixed "jump anywhere" row
  // always sits above the "go back one step" control.
  function addViewSwitcher() {
    if (main.querySelector('[data-app-view-switcher]')) return;
    var nav = document.createElement('nav');
    nav.setAttribute('data-app-view-switcher', '');
    nav.setAttribute('aria-label', 'Organiser app views');
    nav.style.display = 'flex';
    nav.style.gap = '8px';
    nav.style.marginBottom = '14px';
    VIEW_ORDER.forEach(function (v) {
      var btn = document.createElement('button');
      btn.type = 'button';
      var current = v === state.view;
      btn.className = 'btn sm' + (current ? ' primary' : '');
      btn.setAttribute('data-app-view', v);
      if (current) btn.setAttribute('aria-current', 'page');
      btn.textContent = VIEW_LABELS[v];
      btn.addEventListener('click', function () { if (v !== state.view) renderView(v, {}); });
      nav.appendChild(btn);
    });
    main.insertBefore(nav, main.firstChild);
  }
  // A legacy in-frame Sign-up link now opens its canonical Settings section.
  function openPeopleSignup() {
    app.openLink(window.__HACKO.resolve('o.signup', state.slug).href).catch(function () {});
  }
  // Same-role in-frame links, generalised past the judge app's narrower
  // version: an organiser-only hostless URL our own frame/organiser-nav.js
  // builds (nav.js's own href() shape, nothing this frame renders on its
  // own), a site-relative path a reused screen builds BY HAND rather than
  // through href() (organiser-judging.js's own "Score as a judge" link,
  // literally '/e/<slug>/judge'), and a plain https: link (every
  // target="_blank" external link these screens build, e.g. "Open public
  // page") all have to leave this sandboxed frame through the host's own
  // ui/open-link rather than attempt a real navigation the frame has no
  // top-navigation permission for in the first place (Codex review
  // pattern from the judge app, SIM-344).
  document.addEventListener('click', function (e) {
    var a = e.target.closest('a[href]');
    if (!a) return;
    var href = a.getAttribute('href');
    if (href.indexOf('#v=') === 0) {
      e.preventDefault();
      var rest = href.slice(3);
      var hashIdx = rest.indexOf('#');
      var view = hashIdx === -1 ? rest : rest.slice(0, hashIdx);
      var extra = hashIdx === -1 ? '' : rest.slice(hashIdx + 1);
      if (view === 'people' && extra === 'signup') { openPeopleSignup(); return; }
      renderView(view, {});
      return;
    }
    if (/^https:\/\//i.test(href)) { e.preventDefault(); app.openLink(href).catch(function () {}); return; }
    if (href.charAt(0) === '/') { e.preventDefault(); app.openLink(window.__hackOrgOrigin + href).catch(function () {}); return; }
  });

  // --- bootstrap ----------------------------------------------------
  app.on('tool-cancelled', function () { showError('Loading was cancelled. Try again from the chat.'); });
  app.on('tool-result', function (result) {
    if (result.isError) { showError((result.content && result.content[0] && result.content[0].text) || 'Could not open this view.'); return; }
    var data = result.structuredContent || {};
    state.slug = data.slug || '';
    state.history = [];
    state.orgData = buildOrgData(data, result._meta && result._meta['simple-hack/organiser-view']);
    // replace: true, skipRefresh: true -- this is a fresh open (the model
    // called hack_show_organiser again), not an in-frame navigation, so it
    // must not refresh-then-overwrite the event it just received, and must
    // not push the previous view onto history.
    renderView(data.view || 'overview', {}, { replace: true, skipRefresh: true });
  });
  app.start().catch(function () { showError('Could not connect. Use the chat to try again.'); });
})();
