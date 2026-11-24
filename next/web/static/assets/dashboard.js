// The owner dashboard: a plain list of my sites and my teams' sites, one page
// per site, and an Account page (profile, Keys, apps, Notifications, teams,
// Your data). Everything goes through the same /v1 routes the AI app uses.
//
// Answers this page reads:
//   GET /v1/me            {id, email, handle, display_name, operator, home_site, domain (host), domain_status (as below),
//                          teams: [{handle, display_name, members: [{id, email}], invites: [{email}]}],
//                          invitations: [{id, team, display_name}], keys: [{id, name, last4, last_used_at}],
//                          apps: [{id, name, connected_at}], devices: [{id, name}], push_key,
//                          use: {sites, site_bytes_limit, data_bytes_limit, files_bytes_limit, kept_versions, unlimited}}
//   GET /v1/sites         {sites: [{name, owner, url, changed_at, offline, listed, access, deleted_at}]}  ?q= ?bin=true
//   GET /v1/sites/{site}  {name, owner, url, live_version, versions: [{n, author, at}], listed, offline, taken_down,
//                          access, passcode, viewers: [{email, signed_in}], short_name,
//                          domain: {host, status, says, error, records: [{type, host, value, ok, optional}]},
//                          namespaces: [{name, bytes, rules, spam_check, tables}], notifications: [{kind, namespace, target}]}
//                          PATCH answers with the site as GET shows it.
//   GET /v1/sites/{site}/stats  {days: [{day, views, people}], views, pages, referrers, countries: [{key, n}]}
//   Owner SQL: POST /v1/sites/{site}/storage/sqlite/{ns}/query {columns, rows} | execute {changes}
(function () {
  'use strict';
  var app = document.getElementById('app');
  var me = null;

  // ---- small helpers
  function h(tag, attrs) {
    var node = document.createElement(tag);
    for (var k in attrs || {}) {
      var v = attrs[k];
      if (v == null || v === false) continue;
      if (k === 'text') node.textContent = v;
      else if (k.slice(0, 2) === 'on') node.addEventListener(k.slice(2), v);
      else if (k === 'class') node.className = v;
      else node.setAttribute(k, v === true ? '' : v);
    }
    for (var i = 2; i < arguments.length; i++) add(node, arguments[i]);
    return node;
  }
  function add(node, child) {
    if (child == null || child === false) return;
    if (Array.isArray(child)) { child.forEach(function (c) { add(node, c); }); return; }
    node.appendChild(typeof child === 'string' ? document.createTextNode(child) : child);
  }
  function api(method, path, body) {
    var opts = { method: method, credentials: 'same-origin', headers: { 'X-SH-CSRF': '1' } };
    if (body !== undefined) { opts.headers['Content-Type'] = 'application/json'; opts.body = JSON.stringify(body); }
    return fetch(path, opts).then(function (r) {
      return r.text().then(function (t) {
        var b = null;
        try { b = t ? JSON.parse(t) : null; } catch (e) { b = null; }
        if (r.status === 401 && path === '/v1/me') {
          location.replace('/signin?next=' + encodeURIComponent('/dashboard' + location.hash));
          return new Promise(function () {});
        }
        if (!r.ok) { var e = new Error((b && b.fix) || 'That did not work. Try again.'); e.code = b && b.code; e.status = r.status; throw e; }
        return b || {};
      });
    });
  }
  function status() { return h('p', { class: 'status', role: 'status', 'aria-live': 'polite' }); }
  function say(el, text, bad) { el.textContent = text; el.className = 'status' + (bad ? ' bad' : ''); }
  // act runs a call from a button: busy while it runs, the answer or the fix after.
  function act(button, out, fn, done) {
    button.disabled = true;
    say(out, 'Working…');
    return Promise.resolve().then(fn).then(function (res) {
      say(out, typeof done === 'string' ? done : '');
      if (typeof done === 'function') done(res);
    }, function (e) { say(out, e.message, true); }).then(function () { button.disabled = false; });
  }
  function when(s) {
    if (!s) return '';
    var d = new Date(s), diff = (Date.now() - d) / 1000;
    if (diff < 60) return 'just now';
    if (diff < 3600) return Math.round(diff / 60) + ' min ago';
    if (diff < 86400) return Math.round(diff / 3600) + ' h ago';
    if (diff < 86400 * 30) return Math.round(diff / 86400) + ' days ago';
    return d.toLocaleDateString();
  }
  function bytes(n) {
    n = Number(n) || 0;
    function one(x) { return String(Math.round(x * 10) / 10); }
    if (n >= 1 << 30) return one(n / (1 << 30)) + ' GB';
    if (n >= 1 << 20) return one(n / (1 << 20)) + ' MB';
    return Math.max(n ? 1 : 0, Math.round(n / 1024)) + ' KB';
  }
  function copy(text, button) {
    navigator.clipboard.writeText(text).then(function () { button.textContent = 'Copied'; }, function () { button.textContent = 'Select and copy'; });
  }
  // siteAPI is a site's route; params are more query values, like keep_other_files.
  function siteAPI(s, rest, params) {
    var p = new URLSearchParams(params || {});
    if (s.owner && me && s.owner !== me.handle) p.set('owner', s.owner);
    return '/v1/sites/' + encodeURIComponent(s.name) + (rest || '') + (p.toString() ? '?' + p : '');
  }
  function confirmDo(text) { return window.confirm(text); }
  function show() { app.replaceChildren(); add(app, Array.prototype.slice.call(arguments)); window.scrollTo(0, 0); }

  // Files from an <input type=file>, as {path: base64}. A chosen folder loses
  // its own name, so its index.html lands at the top of the site.
  function readFiles(list) {
    return Promise.all(Array.prototype.map.call(list, function (f) {
      return new Promise(function (resolve, reject) {
        var r = new FileReader();
        r.onload = function () { resolve([f, String(r.result).replace(/^data:[^,]*,/, '')]); };
        r.onerror = reject;
        r.readAsDataURL(f);
      });
    })).then(function (pairs) {
      var out = {};
      pairs.forEach(function (p) {
        var path = (p[0].webkitRelativePath || p[0].name).split('/');
        if (path.length > 1) path.shift();
        out[path.join('/')] = p[1];
      });
      return out;
    });
  }
  function publish(s, list, keep) {
    return readFiles(list).then(function (files) {
      if (!Object.keys(files).length) throw new Error('Choose at least one file.');
      return api('PUT', siteAPI(s, '', keep ? { keep_other_files: 'true' } : null), { files_base64: files });
    });
  }

  // ---- sites list
  function sitesPage(bin) {
    if (!me.handle) return [handleCard(), invitationsCard()];
    var out = status(), list = h('ul', { class: 'list' });
    var search = h('input', { type: 'search', placeholder: 'Search your sites', 'aria-label': 'Search your sites' });
    var t = null;
    function load() {
      var params = new URLSearchParams();
      if (search.value.trim()) params.set('q', search.value.trim());
      if (bin) params.set('bin', 'true');
      api('GET', '/v1/sites' + (params.toString() ? '?' + params : '')).then(function (b) {
        var sites = b.sites || [];
        list.replaceChildren();
        if (!sites.length) list.appendChild(h('li', {}, h('span', { class: 'muted', text: bin ? 'The bin is empty.' : search.value ? 'No site matches.' : 'No sites yet. Publish one from your AI app, or with New site above.' })));
        sites.forEach(function (s) { list.appendChild(bin ? binRow(s, load) : siteRow(s)); });
      }).catch(function (e) { say(out, e.message, true); });
    }
    search.addEventListener('input', function () { clearTimeout(t); t = setTimeout(load, 250); });
    load();
    return [
      invitationsCard(),
      h('div', { class: 'row', style: 'justify-content:space-between;margin-bottom:16px' },
        h('h1', { style: 'margin:0', text: bin ? 'Bin' : 'Your sites' }),
        bin ? h('a', { href: '#', text: 'Back to your sites' }) : newSiteButton()),
      bin ? h('p', { class: 'muted', text: 'Deleted sites stay here for 7 days, with their saved data. Restore puts one back as it was.' }) : null,
      h('div', { class: 'field' }, search),
      list, out,
      bin ? null : h('p', {}, h('a', { href: '#bin', text: 'Bin' }))
    ];
  }
  function siteRow(s) {
    var tags = [];
    if (s.owner && s.owner !== me.handle) tags.push(h('span', { class: 'tag', text: s.owner }));
    if (s.taken_down) tags.push(h('span', { class: 'tag bad', text: 'taken down' }));
    else if (s.offline) tags.push(h('span', { class: 'tag', text: 'offline' }));
    if (s.access === 'passcode') tags.push(h('span', { class: 'tag', text: 'passcode' }));
    if (s.access === 'viewers') tags.push(h('span', { class: 'tag', text: 'named people' }));
    return h('li', {},
      h('div', { class: 'grow' },
        h('div', {}, h('a', { href: s.url, target: '_blank', rel: 'noopener', text: s.name }), ' ', tags),
        h('div', { class: 'muted', text: (s.url || '').replace(/^https?:\/\//, '').replace(/\/$/, '') + (s.changed_at ? ' · changed ' + when(s.changed_at) : '') })),
      h('a', { class: 'btn', href: '#site/' + encodeURIComponent(s.owner || me.handle) + '/' + encodeURIComponent(s.name), text: 'Manage' }));
  }
  function binRow(s, reload) {
    var out = status(), b = h('button', { text: 'Restore' });
    b.onclick = function () { act(b, out, function () { return api('PATCH', siteAPI(s), { restore: true }); }, reload); };
    return h('li', {}, h('div', { class: 'grow' }, h('div', { text: s.name }), h('div', { class: 'muted', text: 'Deleted ' + when(s.deleted_at) })), b, out);
  }
  function newSiteButton() {
    var b = h('button', { class: 'primary', text: 'New site' });
    b.onclick = function () { location.hash = '#new'; };
    return b;
  }
  function newSitePage() {
    var out = status();
    var owner = h('select', { id: 'owner' }, [h('option', { value: me.handle, text: me.handle + ' (you)' })].concat((me.teams || []).map(function (t) { return h('option', { value: t.handle, text: t.handle + ' (team)' }); })));
    var name = h('input', { id: 'name', required: true, maxlength: 60, autocomplete: 'off', placeholder: 'my-site' });
    var files = h('input', { id: 'files', type: 'file', multiple: true });
    var folder = h('input', { id: 'folder', type: 'file', webkitdirectory: true });
    var go = h('button', { class: 'primary', text: 'Publish' });
    go.onclick = function () {
      var picked = folder.files.length ? folder.files : files.files;
      var s = { name: name.value.trim().toLowerCase(), owner: owner.value };
      act(go, out, function () { return publish(s, picked, false); }, function (res) {
        location.hash = '#site/' + encodeURIComponent(s.owner) + '/' + encodeURIComponent(s.name);
        if (res && res.url) window.open(res.url, '_blank', 'noopener');
      });
    };
    return [h('p', {}, h('a', { href: '#', text: '← Your sites' })),
      h('h1', { text: 'New site' }),
      h('p', { class: 'muted', text: 'Choose the files of your site, with an index.html for its first page. Most people ask their AI app to do this instead.' }),
      (me.teams || []).length ? h('div', { class: 'field' }, h('label', { for: 'owner', text: 'Whose site' }), owner) : null,
      h('div', { class: 'field' }, h('label', { for: 'name', text: 'Site name' }), name,
        h('p', { class: 'muted', text: 'Lower-case letters, digits and single hyphens. It is part of the address.' })),
      h('div', { class: 'field' }, h('label', { for: 'files', text: 'Files' }), files),
      h('div', { class: 'field' }, h('label', { for: 'folder', text: 'Or a whole folder' }), folder),
      go, out];
  }
  function handleCard() {
    var out = status(), input = h('input', { id: 'pick-handle', maxlength: 40, autocomplete: 'username' });
    var b = h('button', { class: 'primary', text: 'Save handle' });
    b.onclick = function () { act(b, out, function () { return api('PATCH', '/v1/owners/' + encodeURIComponent(me.id), { handle: input.value.trim().toLowerCase() }); }, function () { boot(); }); };
    return h('div', { class: 'panel' }, h('h2', { text: 'Pick your handle' }),
      h('p', { class: 'muted', text: 'Your sites live under it, like blog--yourhandle.simple-host.site. You need one before your first site.' }),
      h('div', { class: 'row' }, input, b), out);
  }
  function invitationsCard() {
    var inv = me.invitations || [];
    if (!inv.length) return null;
    return h('div', { class: 'panel' }, h('h2', { text: 'Invitations' }), h('ul', { class: 'list' }, inv.map(function (i) {
      var out = status(), b = h('button', { class: 'primary', text: 'Join ' + (i.display_name || i.team) });
      b.onclick = function () { act(b, out, function () { return api('POST', '/v1/invites/' + encodeURIComponent(i.id) + '/accept', {}); }, function () { boot(); }); };
      return h('li', {}, h('span', { class: 'grow', text: 'The team ' + (i.display_name ? i.display_name + ' (' + i.team + ')' : i.team) + ' invited you.' }), b, out);
    })));
  }

  // ---- one site
  // redraw draws the open site page again from a site as GET (or PATCH) answers it.
  var redraw = function () {};
  function sitePage(owner, name) {
    var s = { owner: owner, name: name };
    var body = h('div', {}, h('p', { class: 'muted', text: 'Loading…' }));
    redraw = function (site) {
      site.owner = site.owner || owner;
      site.name = site.name || name;
      body.replaceChildren(
        siteHead(site), updatePanel(site), accessPanel(site), statsPanel(site), dataPanel(site),
        notifyPanel(site), addressPanel(site), versionsPanel(site), settingsPanel(site));
    };
    api('GET', siteAPI(s)).then(redraw).catch(function (e) { body.replaceChildren(h('p', { class: 'bad', text: e.message })); });
    return [h('p', {}, h('a', { href: '#', text: '← Your sites' })), body];
  }
  function reload(site) { return api('GET', siteAPI(site)).then(redraw); }
  function siteHead(site) {
    var c = h('button', { text: 'Copy address' });
    c.onclick = function () { copy(site.url, c); };
    return h('section', {},
      h('h1', { text: site.name }),
      site.owner !== me.handle ? h('p', { class: 'muted', text: 'Owned by ' + site.owner }) : null,
      site.taken_down ? h('p', { class: 'bad', text: 'This site was taken down by the operator.' }) : null,
      h('p', {}, h('a', { href: site.url, target: '_blank', rel: 'noopener', text: (site.url || '').replace(/^https?:\/\//, '') })),
      h('div', { class: 'row' }, h('a', { class: 'btn primary', href: site.url, target: '_blank', rel: 'noopener', text: 'Open site' }), c));
  }
  function panel(title, intro) {
    var rest = Array.prototype.slice.call(arguments, 2);
    return h('section', { class: 'panel' }, h('h2', { text: title }), intro ? h('p', { class: 'muted', text: intro }) : null, rest);
  }
  function updatePanel(site) {
    var out = status(), files = h('input', { id: 'up-files', type: 'file', multiple: true });
    var keep = h('input', { type: 'checkbox', id: 'keep', checked: true });
    var b = h('button', { text: 'Publish these files' });
    b.onclick = function () { act(b, out, function () { return publish(site, files.files, keep.checked); }, 'Published. The new version is live.'); };
    var photos = h('button', { text: 'Add photos' }), pout = status();
    photos.onclick = function () {
      var ns = (site.namespaces && site.namespaces[0] && site.namespaces[0].name) || 'photos';
      act(photos, pout, function () { return api('POST', siteAPI(site, '/upload-links'), { kind: 'files', namespace: ns }); }, function (r) {
        if (r.url) location.href = r.url;
      });
    };
    return panel('Update files', 'Send new or changed files. With the box ticked, every other file stays as it is.',
      h('div', { class: 'field' }, h('label', { for: 'up-files', text: 'Files' }), files),
      h('label', { class: 'check' }, keep, 'Keep my other files'), b, out,
      h('p', { class: 'muted', style: 'margin-top:12px', text: 'Photos for the page (shrunk on your device, saved to the site\'s files):' }), photos, pout);
  }
  function accessPanel(site) {
    var out = status(), mode = site.access || 'open';
    function radio(value, label) {
      var r = h('input', { type: 'radio', name: 'access', value: value, checked: mode === value });
      r.onchange = function () { mode = value; draw(); };
      return h('label', { class: 'check' }, r, label);
    }
    var detail = h('div', {});
    var code = h('input', { id: 'passcode', autocomplete: 'off', value: site.passcode || '', placeholder: 'At least 6 characters' });
    var viewers = h('ul', { class: 'list' });
    var email = h('input', { id: 'viewer', type: 'email', placeholder: 'name@example.com', 'aria-label': 'Email to add' });
    var list = (site.viewers || []).slice();
    function drawViewers() {
      viewers.replaceChildren();
      list.forEach(function (v) {
        var b = h('button', { class: 'danger', text: 'Remove' });
        b.onclick = function () {
          if (!confirmDo('Remove ' + v.email + '? They lose access at once.')) return;
          act(b, out, function () { return api('PATCH', siteAPI(site), { viewers_remove: [v.email] }); }, function (r) { list = r.viewers || []; drawViewers(); });
        };
        viewers.appendChild(h('li', {}, h('span', { class: 'grow', text: v.email }), h('span', { class: 'tag' + (v.signed_in ? ' on' : ''), text: v.signed_in ? 'has signed in' : 'not signed in yet' }), b));
      });
    }
    function draw() {
      detail.replaceChildren();
      if (mode === 'passcode') {
        var out2 = h('button', { text: 'Sign everyone out' });
        out2.onclick = function () { act(out2, out, function () { return api('PATCH', siteAPI(site), { sign_everyone_out: true }); }, 'Everyone must enter the passcode again.'); };
        detail.append(h('div', { class: 'field' }, h('label', { for: 'passcode', text: 'Passcode' }), code,
          h('p', { class: 'muted', text: 'One passcode locks every address of the site, its pages and its data.' })), out2);
      }
      if (mode === 'viewers') {
        var addB = h('button', { text: 'Add' });
        addB.onclick = function () {
          var e = email.value.trim();
          act(addB, out, function () { return api('PATCH', siteAPI(site), { viewers_add: [e] }); }, function (r) { list = r.viewers || []; email.value = ''; drawViewers(); });
        };
        drawViewers();
        detail.append(h('p', { class: 'muted', text: 'Only these people can open the site, after signing in. Your team always can.' }), viewers, h('div', { class: 'row' }, email, addB));
      }
    }
    draw();
    var save = h('button', { text: 'Save' });
    save.onclick = function () {
      var body = { access: mode };
      if (mode === 'passcode') body.passcode = code.value;
      if (mode !== 'open' || confirmDo('Open this site to anyone with the link?')) act(save, out, function () { return api('PATCH', siteAPI(site), body); }, 'Saved.');
    };
    return panel('Who can see it', null, radio('open', 'Anyone with the link'), radio('passcode', 'People with the passcode'), radio('viewers', 'Only people I name'), detail, h('p', {}, save), out);
  }
  function statsPanel(site) {
    var box = h('div', {}, h('p', { class: 'muted', text: 'Loading…' }));
    api('GET', siteAPI(site, '/stats')).then(function (st) {
      var days = st.days || [], total = st.views != null ? st.views : days.reduce(function (a, d) { return a + (d.views || 0); }, 0);
      var max = Math.max.apply(null, days.map(function (d) { return d.views || 0; }).concat([1]));
      var bars = h('div', { style: 'display:flex;align-items:flex-end;gap:2px;height:64px;margin:8px 0', role: 'img', 'aria-label': total + ' visits in ' + days.length + ' days' },
        days.map(function (d) { return h('span', { title: d.day + ': ' + (d.views || 0), style: 'flex:1;background:var(--accent);min-height:1px;height:' + Math.round((d.views || 0) / max * 100) + '%' }); }));
      function top(title, rows) {
        return h('div', {}, h('h3', { text: title }), h('ul', { class: 'list' }, (rows || []).slice(0, 5).map(function (r) {
          return h('li', {}, h('span', { class: 'grow', text: r.key || r.name || '(none)' }), h('span', { class: 'muted', text: String(r.n || r.count || 0) }));
        }).concat((rows || []).length ? [] : [h('li', {}, h('span', { class: 'muted', text: 'None yet.' }))])));
      }
      box.replaceChildren(h('p', { class: 'big', text: total.toLocaleString() + (total === 1 ? ' visit' : ' visits') }),
        h('p', { class: 'muted', text: (days.length ? 'Last ' + days.length + (days.length === 1 ? ' day' : ' days') + '. ' : 'No visits yet. ') + 'Your own visits and bots are not counted. New visits show within a minute.' }), days.length ? bars : null,
        h('div', { class: 'grid2' }, top('Top pages', st.pages), top('Referrers', st.referrers), top('Countries', st.countries)));
    }).catch(function (e) { box.replaceChildren(h('p', { class: 'bad', text: e.message })); });
    return panel('Visits', null, box);
  }
  function dataPanel(site) {
    var nss = site.namespaces || [], out = status();
    var pick = h('select', { id: 'ns', 'aria-label': 'Namespace' }, nss.map(function (n) { return h('option', { value: n.name, text: n.name }); }));
    var fresh = h('input', { id: 'ns-new', placeholder: 'New namespace, like bookings', 'aria-label': 'New namespace' });
    var sql = h('textarea', { id: 'sql', 'aria-label': 'SQL', placeholder: 'SELECT * FROM bookings ORDER BY id DESC LIMIT 20' });
    var result = h('div', { class: 'scroll' });
    var run = h('button', { text: 'Run' });
    function ns() { return fresh.value.trim().toLowerCase() || pick.value; }
    run.onclick = function () {
      // A statement that reads goes to query; anything else to execute,
      // which also makes a new namespace.
      var reads = /^\s*(select|with|pragma|explain)\b/i.test(sql.value), name = ns();
      act(run, out, function () {
        if (!name) throw new Error('Pick a namespace, or type a new one.');
        return api('POST', siteAPI(site, '/storage/sqlite/' + encodeURIComponent(name) + (reads ? '/query' : '/execute')), { sql: sql.value });
      }, function (r) {
        if (!reads && (fresh.value.trim() || /^\s*(create|drop|alter)\b/i.test(sql.value))) { reload(site); return; }
        var cols = r.columns || [], rows = r.rows || [];
        result.replaceChildren(reads ? h('table', {}, h('thead', {}, h('tr', {}, cols.map(function (c) { return h('th', { text: c }); }))),
          h('tbody', {}, rows.length ? rows.map(function (row) { return h('tr', {}, row.map(function (v) { return h('td', { text: v == null ? '' : String(v) }); })); })
            : h('tr', {}, h('td', { class: 'muted', colspan: cols.length || 1, text: 'No rows.' }))))
          : h('p', { class: 'muted', text: r.changes === 1 ? '1 row changed.' : (r.changes || 0) + ' rows changed.' }));
      });
    };
    var rules = h('textarea', { id: 'rules', 'aria-label': 'Rules' });
    var spam = h('input', { type: 'checkbox', id: 'spam' });
    function fillRules() {
      var n = nss.filter(function (x) { return x.name === pick.value; })[0];
      rules.value = n && n.rules ? JSON.stringify(n.rules, null, 2) : '{}';
      spam.checked = !!(n && n.spam_check);
    }
    pick.onchange = fillRules; fillRules();
    var saveRules = h('button', { text: 'Save rules' }), rout = status();
    saveRules.onclick = function () {
      var parsed;
      try { parsed = JSON.parse(rules.value || '{}'); } catch (e) { say(rout, 'The rules are not valid JSON: ' + e.message, true); return; }
      act(saveRules, rout, function () { return api('PUT', siteAPI(site, '/storage/' + encodeURIComponent(ns()) + '/rules'), { rules: parsed, spam_check: spam.checked }); }, 'Rules saved.');
    };
    return panel('Saved data', 'Your site\'s tables, values and files. You can read and change anything here, or ask your AI app, for example "show my bookings".',
      nss.length ? h('ul', { class: 'list' }, nss.map(function (n) {
        var tables = (n.tables || []).filter(function (t) { return t.charAt(0) !== '_'; });
        return h('li', {}, h('div', { class: 'grow' }, h('div', { text: n.name }), h('div', { class: 'muted', text: tables.length ? 'Tables: ' + tables.join(', ') : 'No tables yet' })), h('span', { class: 'muted', text: bytes(n.bytes) }));
      })) : h('p', { class: 'muted', text: 'No saved data yet. Type a new namespace name below and run a CREATE TABLE to start one.' }),
      h('div', { class: 'row field' }, nss.length ? pick : null, fresh),
      h('div', { class: 'field' }, sql), run, out, result,
      h('h3', { style: 'margin-top:16px', text: 'Who may read and write' }),
      h('p', { class: 'muted', text: 'For each table, "_values" and "_files": who may read, add, change and delete (anyone, signed-in people, the row\'s author, you). No rules means only you.' }),
      h('div', { class: 'field' }, rules),
      h('label', { class: 'check' }, spam, 'Anonymous visitors pass a spam check before they add (rules name it auth.checked)'), saveRules, rout);
  }
  function notifyPanel(site) {
    var out = status(), current = site.notifications || [];
    var access = h('input', { type: 'checkbox', checked: current.some(function (n) { return n.kind === 'access'; }) });
    var targets = [];
    (site.namespaces || []).forEach(function (n) {
      var tables = (n.tables || Object.keys(n.rules || {})).filter(function (t) { return t; });
      if (!tables.length) tables = ['_values', '_files'];
      tables.forEach(function (t) {
        var box = h('input', { type: 'checkbox', checked: current.some(function (c) { return c.kind === 'data' && c.namespace === n.name && c.target === t; }) });
        targets.push({ ns: n.name, target: t, box: box });
      });
    });
    var b = h('button', { text: 'Save' });
    b.onclick = function () {
      var list = targets.filter(function (t) { return t.box.checked; }).map(function (t) { return { kind: 'data', namespace: t.ns, target: t.target }; });
      if (access.checked) list.push({ kind: 'access' });
      act(b, out, function () { return api('PATCH', siteAPI(site), { notifications: list }); }, 'Saved.');
    };
    return panel('Notifications', 'Tell me in my AI app and on my devices with push turned on (Account page).',
      h('label', { class: 'check' }, access, 'Someone asks for access'),
      targets.map(function (t) { return h('label', { class: 'check' }, t.box, 'Data changes in ' + t.ns + ' / ' + t.target); }), b, out);
  }
  function addressPanel(site) {
    var out = status(), d = site.domain;
    var short = h('input', { id: 'short', placeholder: 'name', value: site.short_name || '', 'aria-label': 'Short name' });
    var sb = h('button', { text: site.short_name ? 'Change' : 'Get it' });
    sb.onclick = function () { act(sb, out, function () { return api('PATCH', siteAPI(site), { short_name: short.value.trim().toLowerCase() }); }, redraw); };
    var dom = h('input', { id: 'domain', placeholder: 'www.example.com', 'aria-label': 'Your domain' });
    var db = h('button', { text: 'Connect' });
    db.onclick = function () { act(db, out, function () { return api('PATCH', siteAPI(site), { domain: dom.value.trim().toLowerCase() }); }, redraw); };
    var rm = function (field, label) {
      var b = h('button', { class: 'danger', text: label });
      b.onclick = function () { if (confirmDo(label + '? The site\'s own address keeps working.')) act(b, out, function () { var x = {}; x[field] = ''; return api('PATCH', siteAPI(site), x); }, redraw); };
      return b;
    };
    return panel('Address', null,
      h('h3', { text: 'A free short name' }),
      site.short_name ? h('p', {}, site.short_name + '.' + location.hostname + ' ', rm('short_name', 'Let it go')) : h('div', { class: 'row field' }, short, h('span', { text: '.' + location.hostname }), sb),
      h('h3', { text: 'Your own domain' }),
      d && d.host ? h('div', {},
        domainStatus(d),
        rm('domain', 'Remove domain'))
        : h('div', { class: 'row field' }, dom, db),
      out);
  }
  // domainStatus is a domain's state and the DNS records it needs (own domain, or one for all sites).
  function domainStatus(d) {
    var tag = { live: 'live', certificate: 'HTTPS on its way', waiting: 'waiting for DNS' }[d.status] || d.status;
    return h('div', {},
      h('p', {}, h('strong', { text: d.host }), ' ', h('span', { class: 'tag' + (d.status === 'live' ? ' on' : ''), text: tag })),
      d.says ? h('p', { class: 'muted', text: d.says }) : null,
      d.error ? h('p', { class: 'bad', text: d.error }) : null,
      d.status !== 'live' && (d.records || []).length ? h('div', { class: 'scroll' }, h('table', {}, h('thead', {}, h('tr', {}, h('th', { text: 'Type' }), h('th', { text: 'Name' }), h('th', { text: 'Value' }), h('th', { text: '' }))),
        h('tbody', {}, d.records.map(function (r) {
          return h('tr', {}, h('td', { text: r.type }), h('td', {}, h('code', { text: r.host })), h('td', {}, h('code', { text: r.value })),
            h('td', { class: r.ok ? '' : 'muted', text: r.ok ? 'found' : (r.optional ? 'optional' : 'missing') }));
        })))) : null);
  }
  function versionsPanel(site) {
    var out = status(), vs = (site.versions || []).slice().sort(function (a, b) { return b.n - a.n; });
    return panel('Versions', 'Each publish is a version. The last few are kept.', h('ul', { class: 'list' }, vs.map(function (v) {
      var live = v.n === site.live_version, b = h('button', { text: 'Make live' });
      b.onclick = function () { act(b, out, function () { return api('PATCH', siteAPI(site), { live_version: v.n }); }, redraw); };
      return h('li', {}, h('span', { class: 'grow', text: 'Version ' + v.n + (v.author ? ' by ' + v.author : '') + ' · ' + when(v.at) }), live ? h('span', { class: 'tag on', text: 'live' }) : b);
    })), out);
  }
  function settingsPanel(site) {
    var out = status();
    var listed = h('input', { type: 'checkbox', checked: !!site.listed });
    listed.onchange = function () { act(listed, out, function () { return api('PATCH', siteAPI(site), { listed: listed.checked }); }, 'Saved.'); };
    var off = h('button', { text: site.offline ? 'Put back online' : 'Take offline' });
    off.onclick = function () { act(off, out, function () { return api('PATCH', siteAPI(site), { offline: !site.offline }); }, redraw); };
    var del = h('button', { class: 'danger', text: 'Delete site' });
    del.onclick = function () {
      if (confirmDo('Delete ' + site.name + '? It goes to the bin for 7 days, with its saved data, and can be restored.')) act(del, out, function () { return api('DELETE', siteAPI(site)); }, function () { location.hash = '#'; });
    };
    var ops = null;
    if (me.operator) {
      ops = h('button', { class: 'danger', text: site.taken_down ? 'Restore (operator)' : 'Take down (operator)' });
      ops.onclick = function () {
        var reason = site.taken_down ? '' : window.prompt('Why is it taken down? The owner sees this.');
        if (reason === null) return;
        act(ops, out, function () { return api('PATCH', siteAPI(site), { taken_down: !site.taken_down, reason: reason }); }, redraw);
      };
    }
    return panel('Site', null,
      h('label', { class: 'check' }, listed, 'List it on my public page'),
      h('p', { class: 'muted', text: site.offline ? 'The site is offline: visitors see a plain "offline" page. Everything is kept.' : 'Taking it offline shows visitors a plain "offline" page and keeps everything.' }),
      h('div', { class: 'row' }, off, del, ops), out,
      h('p', { class: 'muted', style: 'margin-top:12px', text: 'To rename a site, publish it under the new name and delete this one. Saved data does not move.' }));
  }

  // ---- account
  function accountPage() {
    return [h('h1', { text: 'Account' }), h('p', { class: 'muted', text: me.email }),
      profilePanel(), keysPanel(), appsPanel(), pushPanel(), teamsPanel(), dataExportPanel(), signOutPanel()];
  }
  function profilePanel() {
    var out = status();
    var handle = h('input', { id: 'handle', value: me.handle || '', maxlength: 40 });
    var dname = h('input', { id: 'dname', value: me.display_name || '' });
    var home = h('input', { id: 'home', value: me.home_site || '', placeholder: 'One of your site names, or empty for the list' });
    var domain = h('input', { id: 'alldomain', value: me.domain || '', placeholder: 'example.com' });
    var b = h('button', { text: 'Save' });
    b.onclick = function () {
      var body = {};
      if (dname.value !== (me.display_name || '')) body.display_name = dname.value;
      if (home.value.trim() !== (me.home_site || '')) body.home_site = home.value.trim();
      if (domain.value.trim().toLowerCase() !== (me.domain || '')) body.domain = domain.value.trim().toLowerCase();
      var newHandle = handle.value.trim().toLowerCase();
      if (newHandle !== me.handle) {
        if (!confirmDo('Change your handle to ' + newHandle + '? Every site moves to addresses under the new handle at once. Old links stop working, and nobody can use the old handle again.')) return;
        body.handle = newHandle;
      }
      act(b, out, function () { return api('PATCH', '/v1/owners/' + encodeURIComponent(me.handle || me.id), body); }, function () { boot(); });
    };
    return panel('Profile', null,
      h('div', { class: 'field' }, h('label', { for: 'handle', text: 'Handle' }), handle),
      h('div', { class: 'field' }, h('label', { for: 'dname', text: 'Display name' }), dname),
      h('div', { class: 'field' }, h('label', { for: 'home', text: 'Home page site' }), home,
        h('p', { class: 'muted', text: 'Your public page lists the sites you switched on, unless you pick one site to be that page.' })),
      h('div', { class: 'field' }, h('label', { for: 'alldomain', text: 'One domain for all my sites' }), domain,
        h('p', { class: 'muted', text: 'Point *.yourdomain at Simple Host once, and every site answers at site.yourdomain.' }),
        me.domain_status ? domainStatus(me.domain_status) : null),
      b, out, usePanel());
  }
  // usePanel is what the limits allow this account.
  function usePanel() {
    var u = me.use || {};
    if (u.unlimited) return h('p', { class: 'muted', text: (u.sites || 0) + ' sites. This account has no limits.' });
    return h('p', { class: 'muted', text: (u.sites || 0) + ' sites. Each site may hold ' + bytes(u.site_bytes_limit) + ' of files, ' +
      bytes(u.data_bytes_limit) + ' of saved data, and each file store ' + bytes(u.files_bytes_limit) + '. The last ' + (u.kept_versions || 0) + ' versions are kept.' });
  }
  function keysPanel() {
    var out = status(), name = h('input', { id: 'keyname', placeholder: 'What it is for, like deploy script', 'aria-label': 'Key name' });
    var shown = h('div', {});
    var b = h('button', { text: 'Make a key' });
    b.onclick = function () {
      act(b, out, function () { return api('POST', '/v1/keys', { name: name.value.trim() }); }, function (r) {
        var c = h('button', { text: 'Copy' });
        c.onclick = function () { copy(r.key, c); };
        shown.replaceChildren(h('div', { class: 'panel' }, h('p', { text: 'Your new key. It is shown only once:' }), h('pre', { text: r.key }), c));
        me.keys = (me.keys || []).concat([{ id: r.id, name: name.value.trim(), last4: (r.key || '').slice(-4) }]);
        drawKeys();
      });
    };
    var list = h('ul', { class: 'list' });
    function drawKeys() {
      list.replaceChildren();
      (me.keys || []).forEach(function (k) {
        var rb = h('button', { class: 'danger', text: 'Revoke' });
        rb.onclick = function () { if (confirmDo('Revoke ' + (k.name || 'this key') + '? It stops working at once.')) act(rb, out, function () { return api('DELETE', '/v1/keys/' + encodeURIComponent(k.id)); }, function () { me.keys = me.keys.filter(function (x) { return x !== k; }); drawKeys(); }); };
        list.appendChild(h('li', {}, h('div', { class: 'grow' }, h('div', { text: (k.name || 'Key') + ' · …' + (k.last4 || '') }), h('div', { class: 'muted', text: k.last_used_at ? 'Last used ' + when(k.last_used_at) : 'Not used yet' })), rb));
      });
      if (!(me.keys || []).length) list.appendChild(h('li', {}, h('span', { class: 'muted', text: 'No keys.' })));
    }
    drawKeys();
    return panel('Keys', 'For scripts: send a key as Authorization: Bearer <key>. A key acts as you.', list, h('div', { class: 'row' }, name, b), shown, out);
  }
  function appsPanel() {
    var out = status(), list = h('ul', { class: 'list' });
    (me.apps || []).forEach(function (a) {
      var b = h('button', { class: 'danger', text: 'Disconnect' });
      b.onclick = function () { if (confirmDo('Disconnect ' + a.name + '?')) act(b, out, function () { return api('DELETE', '/v1/me/apps/' + encodeURIComponent(a.id)); }, function () { me.apps = me.apps.filter(function (x) { return x !== a; }); list.removeChild(b.parentNode); }); };
      list.appendChild(h('li', {}, h('div', { class: 'grow' }, h('div', { text: a.name || 'AI app' }), h('div', { class: 'muted', text: a.connected_at ? 'Connected ' + when(a.connected_at) : '' })), b));
    });
    if (!(me.apps || []).length) list.appendChild(h('li', {}, h('span', { class: 'muted' }, 'No apps yet. ', h('a', { href: '/install', text: 'Connect one' }))));
    return panel('Connected AI apps', null, list, out);
  }
  function pushPanel() {
    var out = status(), list = h('ul', { class: 'list' });
    var b = h('button', { text: 'Turn on for this browser' });
    var supported = 'serviceWorker' in navigator && 'PushManager' in window;
    b.disabled = !supported || !me.push_key;
    b.onclick = function () {
      act(b, out, function () {
        return navigator.serviceWorker.register('/push-sw.js').then(function (reg) {
          return reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: b64(me.push_key) });
        }).then(function (sub) {
          var j = sub.toJSON();
          return api('POST', '/v1/me/push', { endpoint: j.endpoint, keys: j.keys, name: navigator.platform || 'This browser' });
        });
      }, 'Push is on for this browser.');
    };
    (me.devices || []).forEach(function (d) {
      var rb = h('button', { class: 'danger', text: 'Remove' });
      rb.onclick = function () { act(rb, out, function () { return api('DELETE', '/v1/me/push/' + encodeURIComponent(d.id)); }, function () { list.removeChild(rb.parentNode); }); };
      list.appendChild(h('li', {}, h('span', { class: 'grow', text: d.name || 'A browser' }), rb));
    });
    return panel('Notifications', supported ? 'Get a push on this device when something you picked on a site happens. Pick what in each site\'s Notifications.' : 'This browser cannot show push notifications.', list, b, out);
  }
  function b64(s) {
    var raw = atob((s + '='.repeat((4 - s.length % 4) % 4)).replace(/-/g, '+').replace(/_/g, '/'));
    return Uint8Array.from(raw, function (c) { return c.charCodeAt(0); });
  }
  function teamsPanel() {
    var out = status();
    var teams = (me.teams || []).map(function (t) {
      var email = h('input', { type: 'email', placeholder: 'name@example.com', 'aria-label': 'Email to invite to ' + t.handle });
      var inv = h('button', { text: 'Invite' }), tout = status();
      inv.onclick = function () { act(inv, tout, function () { return api('POST', '/v1/teams/' + encodeURIComponent(t.handle) + '/invites', { email: email.value.trim() }); }, 'Invitation sent. They join when they accept.'); };
      var del = h('button', { class: 'danger', text: 'Delete team' });
      del.onclick = function () { if (confirmDo('Delete the team ' + t.handle + '? Only a team with no sites can be deleted.')) act(del, tout, function () { return api('DELETE', '/v1/teams/' + encodeURIComponent(t.handle)); }, function () { boot(); }); };
      return h('div', { class: 'panel' }, h('h3', { text: t.handle }),
        h('ul', { class: 'list' }, (t.members || []).map(function (m) {
          var rb = h('button', { class: 'danger', text: m.id === me.id ? 'Leave' : 'Remove' });
          rb.onclick = function () { if (confirmDo((m.id === me.id ? 'Leave ' : 'Remove ' + m.email + ' from ') + t.handle + '?')) act(rb, tout, function () { return api('DELETE', '/v1/teams/' + encodeURIComponent(t.handle) + '/members/' + encodeURIComponent(m.id)); }, function () { boot(); }); };
          return h('li', {}, h('span', { class: 'grow', text: m.email }), rb);
        }).concat((t.invites || []).map(function (i) { return h('li', {}, h('span', { class: 'grow muted', text: i.email + ' (invited)' })); }))),
        h('div', { class: 'row' }, email, inv), h('p', {}, del), tout);
    });
    var handle = h('input', { id: 'team', placeholder: 'team handle', 'aria-label': 'New team handle' });
    var mk = h('button', { text: 'Make a team' });
    mk.onclick = function () { act(mk, out, function () { return api('POST', '/v1/teams', { handle: handle.value.trim().toLowerCase() }); }, function () { boot(); }); };
    return panel('Teams', 'A team has its own handle and sites. Every member manages them with their own sign-in.', teams, h('div', { class: 'row' }, handle, mk), out);
  }
  function dataExportPanel() {
    var out = status(), dl = h('button', { text: 'Download my data' });
    dl.onclick = function () { act(dl, out, function () { return api('POST', '/v1/me/export', {}); }, 'We are making it. You will get an email with a link when it is ready.'); };
    var del = h('button', { class: 'danger', text: 'Delete my account' });
    del.onclick = function () {
      var typed = window.prompt('This deletes every site, its data, your keys and app connections at once. It cannot be undone. Type your email to confirm.');
      if (typed === null) return;
      if (typed.trim().toLowerCase() !== (me.email || '').toLowerCase()) { say(out, 'The email did not match, so nothing was deleted.', true); return; }
      act(del, out, function () { return api('DELETE', '/v1/me'); }, function () { location.replace('/'); });
    };
    return panel('Your data', 'Everything: every site, its files, its saved data and your account details, as one download.', h('div', { class: 'row' }, dl, del), out);
  }
  function signOutPanel() {
    var out = status(), one = h('button', { text: 'Sign out' }), all = h('button', { text: 'Sign out everywhere' });
    one.onclick = function () { act(one, out, function () { return api('POST', '/v1/auth/signout', {}); }, function () { location.replace('/'); }); };
    all.onclick = function () { if (confirmDo('Sign out of every browser and disconnect every AI app?')) act(all, out, function () { return api('POST', '/v1/auth/signout', { everywhere: true }); }, function () { location.replace('/'); }); };
    return panel('Sign out', null, h('div', { class: 'row' }, one, all), out);
  }

  // ---- routing
  function route() {
    var parts = location.hash.replace(/^#/, '').split('/').map(decodeURIComponent);
    if (parts[0] === 'site' && parts[2]) show.apply(null, sitePage(parts[1], parts[2]));
    else if (parts[0] === 'account') show.apply(null, accountPage());
    else if (parts[0] === 'new' && me.handle) show.apply(null, newSitePage());
    else show.apply(null, sitesPage(parts[0] === 'bin'));
  }
  function boot() {
    return api('GET', '/v1/me').then(function (m) {
      me = m;
      document.getElementById('admin-link').hidden = !me.operator;
      route();
    }).catch(function (e) { show(h('p', { class: 'bad', text: e.message })); });
  }
  window.addEventListener('hashchange', route);
  boot();
})();
