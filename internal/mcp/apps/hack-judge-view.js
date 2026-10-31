// Hand-written glue between the host bridge (apps/bridge.js) and the
// bundled, otherwise-unmodified judge screens (hack-judge-bundle.js). Not
// generated: this is the MCP-specific adapter, the same role my-sites.js
// and passcode.js play for the other apps.
//
// It gives each screen's render(ctx) a ctx shaped like the real website's
// (main, api, ui, event, params, go), with api() backed by tools/call
// instead of fetch -- the frame holds no credential and the CSP's
// connectDomains is empty, so there is no other way for a screen to read
// or change anything. The route table below maps each REST path and
// method the judge screens call to the MCP tool that does the same thing;
// every row's method+path matches the memberTool/hackRouteTool
// registration it stands for (internal/mcp/hack_member_tools.go,
// hack_organiser_tools.go), checked by TestHackJudgeRouteMapCoversCalls.
(function () {
  'use strict';
  var screens = window.__HACKJ.screens;
  var ui = window.__HACKJ.ui;

  // --- judge-scoped wrapper around the bridge --------------------------
  // Every tool this app ever has a reason to call, by name (Grok security
  // review, SIM-344): wraps window.SHApp.callTool so nothing -- not a bug
  // in this file, not a future edit to a reused screen, not a script that
  // somehow ran despite the nonce CSP -- can reach any other tool this
  // connection's role might otherwise be allowed to call (hack_lock_
  // judging, hack_publish_results, anything from the organiser or
  // participant surfaces). The server is the real backstop (every REST
  // call is independently role- and membership-checked regardless of
  // which tool name reached it), but a client-side allowlist is cheap and
  // narrows the blast radius of a bug in this one file to "calls a judge
  // tool with the wrong arguments", never "calls an unrelated tool at
  // all". Also caps concurrent calls at 4 and serialises writes (plan
  // section 6): a judge tapping several score chips in a burst must not
  // let a later tap's save overtake an earlier one.
  var JUDGE_TOOLS = {
    hack_show_judge: 1, hack_preview_judge: 1, hack_join_judge: 1,
    hack_get_judge_queue: 1, hack_get_rubric: 1, hack_get_event: 1,
    hack_get_judge_scores: 1, hack_score_team: 1, hack_get_my_conflicts: 1,
    hack_declare_my_conflict: 1, hack_remove_my_conflict: 1, hack_get_team_screenshot: 1
  };
  var WRITE_TOOLS = { hack_join_judge: 1, hack_score_team: 1, hack_declare_my_conflict: 1, hack_remove_my_conflict: 1 };
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
  // Shared by both bridge entry points below: the allowlist, concurrency
  // cap and write-serialisation apply the same way regardless of which
  // one a caller used -- callToolResult (used only by refreshEvent, to
  // reach a result's _meta) is exactly as capable of calling an arbitrary
  // tool as callTool is, so it gets exactly the same gate.
  function guardedCall(name, args, invoke) {
    if (!JUDGE_TOOLS[name]) return Promise.reject(new Error(name + ' is not a tool the judge app calls'));
    var run = function () { return acquireSlot().then(function () { return invoke(name, args).then(function (r) { releaseSlot(); return r; }, function (e) { releaseSlot(); throw e; }); }); };
    if (!WRITE_TOOLS[name]) return run();
    var result = writeQueue.then(run, run);
    writeQueue = result.then(function () {}, function () {});
    return result;
  }
  var app = Object.assign({}, window.SHApp, {
    callTool: function (name, args) { return guardedCall(name, args, realCallTool); },
    callToolResult: function (name, args) { return guardedCall(name, args, realCallToolResult); }
  });

  // --- route table: REST (method, path) -> MCP tool -------------------
  function route(method, pattern, tool, params, bodyKeys, successStatus) {
    return { method: method, re: new RegExp('^' + pattern + '$'), tool: tool, params: params, bodyKeys: bodyKeys || null, successStatus: successStatus || 200 };
  }
  var SEG = '([^/]+)';
  var routes = [
    route('GET', '/v1/hack/judge/' + SEG, 'hack_preview_judge', ['code']),
    // hack_join_judge's own schema (hack_member_tools.go) has no `answers`
    // property -- unlike hack_join_event, a judge invite asks no sign-up
    // questions -- so bodyKeys stops at the two fields that schema
    // actually declares. public-judge.js's own POST body still sends an
    // empty `answers: {}` (copied from the participant join flow this
    // screen was adapted from); filtering extra body keys to what the
    // route declares (api()'s own job below) is exactly what catches a
    // mismatch like this one before the server's own `additionalProperties:
    // false` schema check would 400 it -- found live, not by reading the
    // schema side by side, when "accept the invite" never got past this.
    route('POST', '/v1/hack/judge/' + SEG, 'hack_join_judge', ['code'], ['accept_coc', 'display_name']),
    route('GET', '/v1/hack/events/' + SEG + '/judge/queue', 'hack_get_judge_queue', ['slug']),
    route('GET', '/v1/hack/events/' + SEG + '/rubric', 'hack_get_rubric', ['slug']),
    route('GET', '/v1/hack/events/' + SEG, 'hack_get_event', ['slug']),
    route('GET', '/v1/hack/events/' + SEG + '/judge/scores/' + SEG, 'hack_get_judge_scores', ['slug', 'team_id']),
    route('PUT', '/v1/hack/events/' + SEG + '/judge/scores/' + SEG, 'hack_score_team', ['slug', 'team_id'], ['scores', 'comment']),
    route('GET', '/v1/hack/events/' + SEG + '/conflicts', 'hack_get_my_conflicts', ['slug']),
    route('POST', '/v1/hack/events/' + SEG + '/conflicts', 'hack_declare_my_conflict', ['slug'], ['team_id']),
    route('DELETE', '/v1/hack/events/' + SEG + '/conflicts/' + SEG, 'hack_remove_my_conflict', ['slug', 'team_id'], null, 204),
    route('GET', '/v1/hack/events/' + SEG + '/teams/' + SEG + '/screenshot', 'hack_get_team_screenshot', ['slug', 'team'])
  ];

  function matchRoute(method, path) {
    var clean = path.split('?')[0];
    for (var i = 0; i < routes.length; i++) {
      if (routes[i].method !== method) continue;
      var m = routes[i].re.exec(clean);
      if (m) return { def: routes[i], captures: m.slice(1) };
    }
    return null;
  }

  // Mirrors hackRestError/restError's wire format ("<tool> failed (HTTP
  // <status>): <message> [<code>]\n<hint>"), the one way a REST refusal's
  // status and code reach here: the bridge's callTool throws a plain
  // Error with only that text, so it is parsed back out rather than
  // carried as structured data (see hack_apps.go's doc comment on why no
  // server change was needed for this).
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
  // writes itself -- never the raw participant text (a team name, an
  // entry's title) the action touched, both because app-only visibility
  // does not keep that text out of model context once it is repeated
  // there (INTENT) and because a hostile team/entry string otherwise
  // becomes a prompt-injection path straight into the organiser's or
  // judge's chat (plan section 6).
  var MODEL_CONTEXT_SUMMARY = {
    hack_join_judge: function () { return 'Accepted the judge invitation.'; },
    hack_score_team: function (args) {
      var n = (args.scores && args.scores.length) || 0;
      return n ? 'Saved ' + n + ' criterion score' + (n === 1 ? '' : 's') + ' for an entry.' : 'Saved a comment for an entry.';
    },
    hack_declare_my_conflict: function () { return 'Declared a conflict with a team; it will not count toward this judge’s scores.'; },
    hack_remove_my_conflict: function () { return 'Removed a conflict; that team can be scored again.'; }
  };

  // The tool-backed api(): same {ok, status, data} shape as the website's
  // real api.js, so every reused screen's `if (!r.ok)` / `errText(r)` /
  // `r.status === ...` check works unchanged. Never rejects -- a REST
  // refusal resolves with ok:false, exactly like the real fetch-based one.
  async function api(method, path, body) {
    var matched = matchRoute(method, path);
    if (!matched) return { ok: false, status: 0, data: { error: 'This action is not available here.' } };
    var args = {};
    for (var i = 0; i < matched.def.params.length; i++) args[matched.def.params[i]] = decodeURIComponent(matched.captures[i]);
    if (matched.def.bodyKeys && body && typeof body === 'object') {
      for (var k = 0; k < matched.def.bodyKeys.length; k++) {
        var key = matched.def.bodyKeys[k];
        if (Object.prototype.hasOwnProperty.call(body, key)) args[key] = body[key];
      }
    }
    try {
      var data = await app.callTool(matched.def.tool, args);
      var summarize = MODEL_CONTEXT_SUMMARY[matched.def.tool];
      if (summarize) app.updateModelContext(summarize(args)).catch(function () {});
      return { ok: true, status: matched.def.successStatus, data: data };
    } catch (e) {
      var parsed = parseToolError(e);
      return { ok: false, status: parsed.status, data: { error: parsed.error, code: parsed.code } };
    }
  }
  function isSignedOut() { return false; }
  // Used only by public-judge.js's "already a member?" check (the single
  // call site in this app), which reads just .ok and .data.role -- never
  // .data.organiser, which hack_get_event's own output includes only when
  // the caller is ALSO that event's organiser (Grok security review,
  // SIM-344: a co-organiser previewing their own judge invite must not
  // have their event's join_code/judge_code pulled into this frame just
  // to check membership). Stripped here rather than left for the screen
  // not to read, since the data would otherwise sit in this frame's JS
  // heap regardless of whether anything used it.
  function getEvent(slug) {
    return api('GET', '/v1/hack/events/' + encodeURIComponent(slug)).then(function (r) {
      if (r.ok && r.data && r.data.organiser) {
        var data = Object.assign({}, r.data);
        delete data.organiser;
        return { ok: r.ok, status: r.status, data: data };
      }
      return r;
    });
  }

  // showImage/fetchBinary in the real ui.js read bytes over fetch with the
  // API key header -- no network at all reaches this frame. The judge
  // screenshot tool already returns base64 bytes directly; this shadows
  // just that one export, over the bundle's otherwise-untouched ui module.
  // content_type is checked against a fixed whitelist (Grok security
  // review, SIM-344) before it is trusted as the type half of a data: URL
  // -- the server already only accepts these three on upload (see
  // memberScreenshotTool's own description), so this is defence in depth,
  // not a behaviour change for a legitimate screenshot.
  var ALLOWED_IMAGE_TYPES = { 'image/png': 1, 'image/jpeg': 1, 'image/webp': 1 };
  var uiShim = Object.assign({}, ui, {
    showImage: async function (ctx, img, path) {
      var r = await api('GET', path);
      if (!r.ok || !r.data || !r.data.content_base64 || !ALLOWED_IMAGE_TYPES[r.data.content_type]) { img.hidden = true; return; }
      img.src = 'data:' + r.data.content_type + ';base64,' + r.data.content_base64;
      img.hidden = false;
    }
  });

  // --- in-frame routing -------------------------------------------------
  // The reused screens' own links (judge-queue.js's hand-built scoreHref,
  // and every href('judge', slug, <section>) call) use the website's
  // section names -- home, queue, scoring, rubric -- not this frame's
  // four view keys (invite, queue, score, rubric; there is no "home"
  // view here, see hack_apps.go's hack_show_judge). Normalized once,
  // here, rather than in both the stub and the click handler.
  var VIEW_ALIAS = { scoring: 'score', home: 'queue' };
  function normalizeView(view) { return VIEW_ALIAS[view] || view; }
  var main = document.getElementById('app');
  // history is a tiny back-stack of {view, params}, pushed on every normal
  // navigation and popped by goBack() (wired to the rubric's own Back
  // control -- the reused judge-rubric.js screen has no return link of its
  // own; on the website that comes from the shell this frame leaves out).
  // Not persisted anywhere, not shown to the model: purely so "What do the
  // scores mean?" from a team's scoring screen can return to that same
  // team (Codex review, SIM-344).
  var state = { slug: '', event: null, view: '', params: {}, history: [] };
  function paramsObj(map) {
    return { get: function (key) { return Object.prototype.hasOwnProperty.call(map, key) ? map[key] : ''; } };
  }
  function ctxFor(view, params) {
    return {
      main: main,
      api: { api: api, isSignedOut: isSignedOut, getEvent: getEvent },
      ui: uiShim,
      event: { event: state.event },
      me: { username: 'me', display_name: '' },
      params: paramsObj(params || {}),
      go: function (path) {
        var q = (path.split('?')[1] || '');
        var parts = {};
        q.split('&').forEach(function (pair) {
          if (!pair) return;
          var kv = pair.split('=');
          parts[decodeURIComponent(kv[0])] = decodeURIComponent(kv[1] || '');
        });
        renderView(normalizeView(parts.s || view), parts);
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
  // Re-reads the event through hack_show_judge itself (not the generic
  // route table's hack_get_event, whose output deliberately drops
  // scoring_open/judging_locked_at/results_published/submission_deadline
  // for the model -- see hack_apps.go's runHackShowJudge, which reads
  // those four straight off the REST body instead of that reduced tool
  // output). Calling the show tool again is exactly how the Go side
  // already gets a fresh, correctly-shaped event; reusing it here avoids
  // inventing a second normalization of hack_get_event's flattened result.
  // Called before every event-backed render (Codex review, SIM-344): a
  // judge's frame can sit open across an organiser locking judging,
  // opening scoring, or publishing results, and the previous design
  // (fetch once, at the model's hack_show_judge call, and never again)
  // left the screens making writes or showing editable controls against
  // state that was no longer true. On a refresh failure (a dropped
  // connection, mid-session), this falls back to whatever state.event
  // already held rather than blank the screen over a transient error.
  //
  // Uses callToolResult, not callTool (coordinator follow-up, SIM-344):
  // judge-rubric.js's about/rules/prizes card needs organiser-written
  // prose the tool's own content/structuredContent must never carry (Grok
  // finding 2 -- a model sees those the same as the view does), so
  // hack_show_judge puts that prose in the result's own _meta instead
  // (hack_apps.go's hackShowJudgeEventText). callTool only ever returns
  // structuredContent, discarding _meta; callToolResult is the bridge's
  // other entry point, which keeps the whole raw result -- the same one
  // my-sites.js already uses to read site_analytics' chart series back
  // out of _meta. The merge below means judge-rubric.js itself needs no
  // change at all: ev.about/ev.rules/ev.prizes read the same either way.
  async function refreshEvent(view, params) {
    try {
      var args = { view: view === 'invite' ? 'queue' : view, slug: state.slug };
      if (params && params.team) args.team_id = params.team;
      var result = await app.callToolResult('hack_show_judge', args);
      var data = result.structuredContent;
      if (data && data.event) {
        var event = Object.assign({}, data.event);
        var text = result._meta && result._meta['simple-hack/event-text'];
        if (text) Object.assign(event, text);
        state.event = event;
      }
      return true;
    } catch (e) {
      return !!state.event; // keep showing the last known state if we have one
    }
  }
  async function renderView(view, params, opts) {
    view = normalizeView(view);
    opts = opts || {};
    if (!opts.replace && state.view && state.view !== view) {
      state.history.push({ view: state.view, params: state.params });
    }
    state.view = view; state.params = params || {};
    if (view !== 'invite' && !opts.skipRefresh) {
      var fresh = await refreshEvent(view, state.params);
      if (!fresh || !state.event) { showError('Could not load this event. Ask the model to reopen the judge view.'); return; }
    } else if (view !== 'invite' && !state.event) {
      showError('Could not load this event. Ask the model to reopen the judge view.'); return;
    }
    var screen = screens[view];
    if (!screen) { showError('That view is not available.'); return; }
    try {
      await screen.render(ctxFor(view, state.params));
    } catch (e) {
      showError('Something went wrong showing this view. Try again.');
      return;
    }
    if (view === 'rubric') addBackControl();
  }
  function goBack() {
    var prev = state.history.pop();
    if (!prev) { renderView('queue', {}, { replace: true }); return; }
    renderView(prev.view, prev.params, { replace: true });
  }
  function addBackControl() {
    if (!state.history.length || main.querySelector('[data-app-back]')) return;
    var back = document.createElement('button');
    back.type = 'button';
    back.className = 'btn sm';
    back.setAttribute('data-app-back', '');
    back.style.marginBottom = '14px';
    back.textContent = '‹ Back';
    back.addEventListener('click', goBack);
    main.insertBefore(back, main.firstChild);
  }
  // Called by the one documented patch to public-judge.js's accept-invite
  // handler (bundlegen/patches.go): the real screen's own success path is
  // `location.assign('/e/<slug>/judge')`, a real page navigation this
  // sandboxed frame cannot make (no allow-top-navigation) and should not
  // even if it could -- it would drop the judge out of the app they just
  // joined through, onto a 404 on this frame's own throwaway serving
  // origin (Codex review, SIM-344). This is the same hand-off
  // __hackJudgeJoined / the judge-path click handler below both use:
  // adopt the slug, then open its queue for real, in-frame.
  window.__hackJudgeJoined = function (slug) {
    state.slug = slug;
    renderView('queue', {});
  };
  // Same-role in-frame links. Three shapes reach here: href() above (the
  // nav.js stand-in) returns '#v=<view>' for every link it builds; but
  // judge-scoring.js's own "Score this team" link in judge-queue.js, and
  // public-judge.js's "Open it" (already a member) link, are both literal
  // `/e/<slug>/judge[?s=<view>&team=<id>]` paths, built by hand rather
  // than through href() -- the real app's app.js (left out of this frame;
  // see the file header) is what catches those normally, by intercepting
  // every same-document link, not just ones it built itself. This does
  // the same, narrowly: any href matching the judge app's own path shape
  // is treated as an in-frame view switch too, resolving the slug from
  // the link itself (Codex review, SIM-344: "Open it" used to fall back
  // to whatever view was already showing -- the invite -- since it carries
  // no ?s= at all, and that view's own cached event was never set in the
  // first place). Anything else (an external https: link, a target=_blank
  // new tab) is left alone.
  var JUDGE_PATH_RE = /^\/e\/([^/]+)\/judge(?:\?(.*))?$/;
  document.addEventListener('click', function (e) {
    var a = e.target.closest('a[href]');
    if (!a) return;
    var href = a.getAttribute('href');
    // newTabLink() (screens/_judge-util.js) always builds https: links
    // this way: the team's site, a video or code URL, every one of them
    // typed by a participant. A sandboxed frame without allow-popups
    // cannot open a real new tab at all, so these go through the host's
    // own ui/open-link instead, which shows the destination before
    // leaving (plan section 6, "Links").
    if (a.target === '_blank') {
      e.preventDefault();
      if (/^https:\/\//i.test(href)) app.openLink(href).catch(function () {});
      return;
    }
    if (href.indexOf('#v=') === 0) {
      e.preventDefault();
      renderView(href.slice(3), {});
      return;
    }
    var m = JUDGE_PATH_RE.exec(href);
    if (!m) return;
    e.preventDefault();
    var slug = decodeURIComponent(m[1]);
    var parts = {};
    (m[2] || '').split('&').forEach(function (pair) {
      if (!pair) return;
      var kv = pair.split('=');
      parts[decodeURIComponent(kv[0])] = decodeURIComponent(kv[1] || '');
    });
    state.slug = slug;
    renderView(parts.s || 'queue', parts);
  });

  // --- bootstrap ----------------------------------------------------
  app.on('tool-cancelled', function () { showError('Loading was cancelled. Try again from the chat.'); });
  app.on('tool-result', function (result) {
    if (result.isError) { showError((result.content && result.content[0] && result.content[0].text) || 'Could not open this view.'); return; }
    var data = result.structuredContent || {};
    state.slug = data.slug || '';
    state.event = data.event || null;
    state.history = [];
    window.__hackJudgeCode = data.code || '';
    var params = {};
    if (data.team_id) params.team = data.team_id;
    // replace: true -- this is a fresh open (the model called
    // hack_show_judge again), not an in-frame navigation, so it must not
    // refresh-then-overwrite the event it just received, and must not
    // push the previous view onto history.
    renderView(data.view || 'queue', params, { replace: true, skipRefresh: true });
  });
  app.start().catch(function () { showError('Could not connect. Use the chat to try again.'); });
})();
