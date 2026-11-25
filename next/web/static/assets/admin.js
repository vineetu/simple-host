// The operator's page (the old admin page's layout, on the new routes):
//   GET /v1/admin/overview  key numbers, network use, reports, self_hosted (and the growth tab)
//   GET /v1/admin/people    every person with sites, sizes, views over 30 days, last seen
//   GET /v1/admin/sites     every site with sizes, last change, views and people over 30 days, addresses
//   PATCH /v1/admin/people/{id}            suspend or re-enable an account
//   PATCH /v1/sites/{site}?owner=<handle>  {taken_down, reason}: take a site down or restore it
//   POST /v1/admin/people                  make an account (self-hosted)
// Lists come whole, so sorting, filtering and paging happen here.
(function () {
'use strict';
var PAGE = 50;
var TABS = ['overview', 'users', 'sites', 'api', 'moderation', 'tools'];
var $ = function (id) { return document.getElementById(id); };
function esc(s) { return String(s == null ? '' : s).replace(/[&<>"']/g, function (c) { return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]; }); }
function href(u) { return /^https?:\/\//i.test(String(u)) ? esc(u) : '#'; }
function bytes(n) {
  n = Number(n) || 0;
  if (n >= 1099511627776) return (n / 1099511627776).toFixed(2).replace(/\.?0+$/, '') + ' TB';
  if (n >= 1073741824) return (n / 1073741824).toFixed(1) + ' GB';
  if (n >= 1048576) return (n / 1048576).toFixed(1) + ' MB';
  if (n >= 1024) return Math.round(n / 1024) + ' KB';
  return n + ' B';
}
function num(n) { return Number(n || 0).toLocaleString('en-US'); }
var MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
function dateOf(s) { var d = new Date(s); if (!s || isNaN(d)) return ''; return d.getDate() + ' ' + MONTHS[d.getMonth()] + ' ' + d.getFullYear(); }
function ago(s) {
  var d = new Date(s); if (!s || isNaN(d)) return '';
  var a = new Date(d.getFullYear(), d.getMonth(), d.getDate()), n = new Date(), b = new Date(n.getFullYear(), n.getMonth(), n.getDate());
  var days = Math.round((b - a) / 86400000);
  if (days <= 0) return 'today';
  if (days === 1) return 'yesterday';
  if (days < 30) return days + ' days ago';
  if (days < 365) { var m = Math.max(1, Math.floor(days / 30.44)); return m + ' month' + (m === 1 ? '' : 's') + ' ago'; }
  var y = Math.max(1, Math.floor(days / 365.25)); return y + ' year' + (y === 1 ? '' : 's') + ' ago';
}
function when(s) { return s ? esc(dateOf(s)) + ' <span class="muted ago">· ' + esc(ago(s)) + '</span>' : '<span class="muted">-</span>'; }
var statusTimer = null;
function status(msg, err) {
  var s = $('status'); s.hidden = false; s.className = 'msg' + (err ? ' err' : ''); s.textContent = msg;
  clearTimeout(statusTimer);
  if (!err) statusTimer = setTimeout(function () { s.hidden = true; }, 6000);
}
function api(method, path, body) {
  return fetch(path, { method: method, credentials: 'same-origin', headers: body ? { 'Content-Type': 'application/json', 'X-SH-CSRF': '1' } : { 'X-SH-CSRF': '1' }, body: body ? JSON.stringify(body) : undefined })
    .then(function (r) {
      return r.json().catch(function () { return {}; }).then(function (b) {
        if (!r.ok) { var e = new Error(b.fix || 'That did not work. Try again.'); e.status = r.status; throw e; }
        return b;
      });
    });
}
// Admin search boxes: every plain word must match, a -word hides rows that match it.
function searchMatches(q, text) {
  text = String(text || '').toLowerCase();
  return String(q || '').toLowerCase().split(/\s+/).every(function (w) {
    if (w.charAt(0) !== '-') return !w || text.indexOf(w) >= 0;
    w = w.slice(1); return !w || text.indexOf(w) < 0;
  });
}

// ---------------------------------------------------------------- dialogs
// In-page confirm and prompt (the old shared dialog, styled by .sh-dlg in site.css).
var dlg = null, settle = null;
function ask(o) {
  if (!dlg) {
    dlg = document.createElement('dialog');
    dlg.className = 'sh-dlg';
    dlg.setAttribute('aria-labelledby', 'sh-dlg-title');
    dlg.innerHTML = '<form class="sh-dlg-box" method="dialog" novalidate>' +
      '<h2 class="sh-dlg-title" id="sh-dlg-title"></h2><p class="sh-dlg-body"></p>' +
      '<label class="sh-dlg-field"><span class="sh-dlg-label"></span><input class="sh-dlg-input" type="text" autocomplete="off"></label>' +
      '<div class="sh-dlg-actions"><button class="sh-dlg-btn" type="button" data-no>Cancel</button><button class="sh-dlg-btn sh-dlg-primary" type="submit" data-yes></button></div></form>';
    document.body.appendChild(dlg);
    dlg.querySelector('form').addEventListener('submit', function (e) { e.preventDefault(); done(true); });
    dlg.querySelector('[data-no]').addEventListener('click', function () { done(false); });
    dlg.addEventListener('cancel', function (e) { e.preventDefault(); done(false); });
  }
  function done(ok) { if (!settle) return; var s = settle; settle = null; dlg.close(); s(ok); }
  var q = function (s) { return dlg.querySelector(s); };
  q('.sh-dlg-title').textContent = o.title || '';
  q('.sh-dlg-body').textContent = o.body || ''; q('.sh-dlg-body').hidden = !o.body;
  q('.sh-dlg-field').hidden = !o.label; q('.sh-dlg-label').textContent = o.label || '';
  var input = q('.sh-dlg-input'); input.value = '';
  q('[data-yes]').textContent = o.confirmLabel || 'OK';
  q('[data-yes]').classList.toggle('sh-dlg-danger', !!o.danger);
  return new Promise(function (resolve) {
    settle = function (ok) { resolve(!ok ? null : o.label ? input.value.trim() : true); };
    dlg.showModal();
    (o.label ? input : q('[data-yes]')).focus();
  });
}

// ---------------------------------------------------------------- data
var D = { people: [], sites: [], byId: {}, byHandle: {}, overview: null, loaded: false };
function index(people, sites, overview) {
  D.overview = overview; D.byId = {}; D.byHandle = {};
  people.forEach(function (p) { p.siteList = []; D.byId[p.id] = p; if (p.handle) D.byHandle[p.handle] = p; });
  // # counts from the newest (1 = newest joiner), whichever way the list sorts.
  people.slice().sort(function (a, b) { return new Date(b.created_at) - new Date(a.created_at); }).forEach(function (p, i) { p._n = i + 1; });
  sites.forEach(function (s) { var o = D.byId[s.owner_id]; if (o) o.siteList.push(s); });
  D.people = people; D.sites = sites;
}
function load() {
  return api('GET', '/v1/admin/overview').then(function (o) {
    return Promise.all([api('GET', '/v1/admin/people'), api('GET', '/v1/admin/sites')]).then(function (r) {
      index(r[0].people || [], r[1].sites || [], o);
      D.loaded = true;
    });
  });
}
function reload(msg) { return load().then(function () { renderCurrent(); if (msg) status(msg); }, function (e) { status(e.message, true); }); }
function ownerName(s) { return s.owner || '(no handle)'; }
function siteLive(s) { return s.live_version > 0 && !s.offline && !s.taken_down && !s.owner_suspended; }
function dashLink(s) { return '/dashboard#site/' + encodeURIComponent(s.owner) + '/' + encodeURIComponent(s.name); }

// ---------------------------------------------------------------- tabs
var current = 'overview';
function tabFromHash() { var h = (location.hash || '').replace(/^#/, ''); return TABS.indexOf(h) >= 0 && !$('tab-' + h).hidden ? h : 'overview'; }
function showTab(name, focus) {
  current = name;
  TABS.forEach(function (t) {
    var on = t === name, b = $('tab-' + t);
    b.setAttribute('aria-selected', on ? 'true' : 'false');
    b.tabIndex = on ? 0 : -1;
    $('p-' + t).hidden = !on;
  });
  if (focus) $('tab-' + name).focus();
  closeMenu();
  renderCurrent();
}
function renderCurrent() {
  if (!D.loaded) return;
  if (current === 'overview') renderOverview();
  else if (current === 'users') renderUsers();
  else if (current === 'sites') renderSites();
  else if (current === 'api') renderApi();
  else if (current === 'moderation') renderModeration();
}
$('tabs').addEventListener('click', function (e) {
  var b = e.target.closest('[data-tab]'); if (!b) return;
  history.replaceState(null, '', '#' + b.dataset.tab);
  showTab(b.dataset.tab);
});
$('tabs').addEventListener('keydown', function (e) {
  var open = TABS.filter(function (t) { return !$('tab-' + t).hidden; }), i = open.indexOf(current), n = null;
  if (e.key === 'ArrowRight') n = open[(i + 1) % open.length];
  else if (e.key === 'ArrowLeft') n = open[(i + open.length - 1) % open.length];
  else if (e.key === 'Home') n = open[0];
  else if (e.key === 'End') n = open[open.length - 1];
  if (!n) return;
  e.preventDefault(); history.replaceState(null, '', '#' + n); showTab(n, true);
});
window.addEventListener('hashchange', function () { var t = tabFromHash(); if (t !== current) showTab(t); });
document.addEventListener('click', function (e) {
  var g = e.target.closest('[data-goto]'); if (!g) return;
  e.preventDefault();
  if (g.dataset.sort) { SS.sort = g.dataset.sort; SS.dir = -1; $('s-sort').value = SS.sort; }
  history.replaceState(null, '', '#' + g.dataset.goto); showTab(g.dataset.goto);
});

// ---------------------------------------------------------------- overview
function tiles(id, list) {
  $(id).innerHTML = list.map(function (t) { return '<div class="stat"><div class="n">' + esc(t[0]) + '</div><div class="l">' + t[1] + '</div></div>'; }).join('');
}
function renderOverview() {
  var o = D.overview || {}, p = o.people || {}, people = D.people;
  var live = D.sites.filter(siteLive).length;
  var down = D.sites.filter(function (s) { return s.taken_down; }).length;
  var susp = people.filter(function (u) { return u.suspended; }).length;
  var disk = D.sites.reduce(function (a, s) { return a + (s.bytes || 0); }, 0);
  tiles('ov-stats', [
    [num(p.total), 'Users'], [num(p.new_7d), 'New in 7 days'], [num(p.new_30d), 'New in 30 days'],
    [num(live), 'Live sites <span class="muted">of ' + num(D.sites.length) + '</span>'],
    [bytes(disk), 'Site storage'],
    [susp + ' · ' + down, 'Suspended · taken down']
  ]);
  var net = o.network || [];
  $('net').hidden = !net.length;
  if (net.length) {
    var tin = 0, tout = 0, today = net[net.length - 1];
    net.forEach(function (d) { tin += d.bytes_in || 0; tout += d.bytes_out || 0; });
    tiles('net-stats', [[bytes(tout), 'Out'], [bytes(tin), 'In'], [bytes(today.bytes_out), 'Out on ' + esc(dateOf(today.day + 'T12:00:00Z'))]]);
    $('net-note').textContent = 'Everything the service sent and received, counted as requests pass, since ' + dateOf(net[0].day + 'T12:00:00Z') + '.';
  }
  $('ov-latest').innerHTML = people.slice(0, 10).map(function (u) {
    return '<tr><td>' + when(u.created_at) + '</td><td>' + esc(u.handle || '-') + '</td><td>' + esc(u.email) + '</td></tr>';
  }).join('') || '<tr><td colspan="3" class="empty">Nobody yet.</td></tr>';
  var top = D.sites.filter(function (s) { return s.views_30d > 0; }).sort(function (a, b) { return b.views_30d - a.views_30d; }).slice(0, 10);
  $('ov-views').innerHTML = top.map(function (s, i) {
    return '<tr><td class="num">' + (i + 1) + '</td><td>' + siteName(s) + '</td><td>' + esc(ownerName(s)) + '</td><td class="num">' + num(s.views_30d) + '</td></tr>';
  }).join('') || '<tr><td colspan="4" class="empty">No visits in 30 days.</td></tr>';
  var big = D.sites.slice().sort(function (a, b) { return b.bytes - a.bytes; }).slice(0, 10);
  $('ov-biggest').innerHTML = big.map(function (s, i) {
    return '<tr><td class="num">' + (i + 1) + '</td><td>' + siteName(s) + '</td><td>' + esc(ownerName(s)) + '</td><td class="num">' + bytes(s.live_bytes) + '</td><td class="num">' + bytes(s.bytes) + '</td></tr>';
  }).join('') || '<tr><td colspan="5" class="empty">No websites yet.</td></tr>';
}
function siteName(s) { return s.url ? '<a href="' + href(s.url) + '" target="_blank" rel="noopener">' + esc(s.name) + '</a>' : esc(s.name); }

// ---------------------------------------------------------------- sorting and paging
function sortBy(list, key, dir, keys) {
  var f = keys[key];
  return list.slice().sort(function (a, b) {
    var x = f(a), y = f(b);
    var c = typeof x === 'string' ? x.localeCompare(y) : (x - y);
    return c * dir || (b._n || 0) - (a._n || 0);
  });
}
function arrows(table, st) {
  document.querySelectorAll('#' + table + ' th[data-sort]').forEach(function (th) {
    var on = th.dataset.sort === st.sort;
    if (on) th.setAttribute('aria-sort', st.dir < 0 ? 'descending' : 'ascending'); else th.removeAttribute('aria-sort');
    th.querySelector('span').innerHTML = on ? (st.dir < 0 ? '&darr;' : '&uarr;') : '';
  });
}
function pager(id, st, total, render) {
  var el = $(id), pages = Math.ceil(total / PAGE);
  if (pages <= 1) { el.innerHTML = total ? '<span>' + total + ' shown</span>' : ''; return; }
  var a = st.page * PAGE + 1, b = Math.min(total, (st.page + 1) * PAGE);
  el.innerHTML = '<button type="button" class="btn ghost sm" data-pg="-1"' + (st.page === 0 ? ' disabled' : '') + '>Previous</button>' +
    '<span>' + a + '–' + b + ' of ' + total + '</span>' +
    '<button type="button" class="btn ghost sm" data-pg="1"' + (st.page >= pages - 1 ? ' disabled' : '') + '>Next</button>';
  el.onclick = function (e) { var p = e.target.closest('[data-pg]'); if (!p || p.disabled) return; st.page += Number(p.dataset.pg); render(); el.scrollIntoView({ block: 'nearest' }); };
}
function chips(id, st, render) {
  $(id).addEventListener('click', function (e) {
    var c = e.target.closest('[data-filter]'); if (!c) return;
    st.filter = c.dataset.filter; st.page = 0;
    this.querySelectorAll('.chip').forEach(function (x) { x.setAttribute('aria-pressed', x === c ? 'true' : 'false'); });
    render();
  });
}
function headers(table, st, render, ascFirst) {
  document.querySelector('#' + table + ' thead').addEventListener('click', function (e) {
    var b = e.target.closest('button[data-sort]'); if (!b) return;
    var k = b.dataset.sort;
    if (st.sort === k) st.dir = -st.dir; else { st.sort = k; st.dir = ascFirst.indexOf(k) >= 0 ? 1 : -1; }
    st.page = 0; render();
  });
}

// ---------------------------------------------------------------- users
var US = { q: '', filter: 'all', sort: 'joined', dir: -1, page: 0, open: {} };
var USER_KEYS = {
  joined: function (u) { return new Date(u.created_at).getTime(); }, handle: function (u) { return (u.handle || u.email || '').toLowerCase(); },
  sites: function (u) { return u.sites || 0; }, size: function (u) { return u.bytes || 0; }, views: function (u) { return u.views_30d || 0; },
  seen: function (u) { return u.last_seen ? new Date(u.last_seen).getTime() : 0; }
};
function userStatus(u) { return u.operator ? '<span class="tag acc">admin</span>' : u.suspended ? '<span class="tag down">suspended</span>' : '<span class="tag ok">active</span>'; }
$('u-sort').addEventListener('change', function () { US.sort = this.value; US.dir = US.sort === 'handle' ? 1 : -1; US.page = 0; renderUsers(); });
function renderUsers() {
  var rows = D.people.filter(function (u) {
    if (US.filter === 'sites' && !u.sites) return false;
    if (US.filter === 'suspended' && !u.suspended) return false;
    return searchMatches(US.q, (u.handle || '') + ' ' + (u.email || '') + ' ' + (u.display_name || ''));
  });
  rows = sortBy(rows, US.sort, US.dir, USER_KEYS);
  $('u-count').textContent = '(' + (rows.length !== D.people.length ? rows.length + ' of ' : '') + D.people.length + ')';
  arrows('u-table', US);
  $('u-sort').value = US.sort;
  var pages = Math.max(1, Math.ceil(rows.length / PAGE)); if (US.page >= pages) US.page = pages - 1;
  $('u-rows').innerHTML = rows.slice(US.page * PAGE, (US.page + 1) * PAGE).map(function (u) {
    var open = !!US.open[u.id];
    return '<tr class="clickable' + (open ? ' open' : '') + '" data-user="' + esc(u.id) + '">' +
      '<td class="num"><button type="button" class="row-toggle" aria-expanded="' + open + '" aria-label="Details for ' + esc(u.handle || u.email) + '">' + (open ? '&#9662;' : '&#9656;') + '</button> ' + u._n + '</td>' +
      '<td>' + (u.handle ? esc(u.handle) : '<span class="muted">no handle</span>') +
        '<span class="mobile-metrics">' + esc(u.email) + '<br>' + num(u.sites) + ' site' + (u.sites === 1 ? '' : 's') + ' · ' + bytes(u.bytes) + ' · ' + num(u.views_30d) + ' views · joined ' + esc(dateOf(u.created_at)) + '</span></td>' +
      '<td>' + esc(u.email) + '</td><td>' + when(u.created_at) + '</td>' +
      '<td class="num">' + num(u.sites) + '</td><td class="num">' + bytes(u.bytes) + '</td><td class="num">' + num(u.views_30d) + '</td>' +
      '<td>' + (u.last_seen ? when(u.last_seen) : '<span class="muted">never</span>') + '</td><td>' + userStatus(u) + '</td></tr>' +
      (open ? '<tr class="detail"><td colspan="9">' + userPanel(u) + '</td></tr>' : '');
  }).join('') || '<tr><td colspan="9" class="empty">No one matches.</td></tr>';
  pager('u-pager', US, rows.length, renderUsers);
}
function siteTag(s) {
  return s.taken_down ? ' <span class="tag down">taken down</span>' : s.offline ? ' <span class="tag">offline</span>' : !s.live_version ? ' <span class="tag">not live</span>' : '';
}
function userPanel(u) {
  var why = u.suspended ? '<div class="down-note">Suspended: ' + esc(u.suspended_reason || 'no reason given') + '. Sign-in and keys are refused; every site of theirs is taken down.</div>' : '';
  var list = u.siteList.length ? '<div class="site-mini">' + u.siteList.map(function (s) {
    return '<div><strong>' + esc(s.name) + '</strong>' + siteTag(s) + (s.url ? '<br><a href="' + href(s.url) + '" target="_blank" rel="noopener">' + esc(s.url.replace(/^https?:\/\//, '').replace(/\/$/, '')) + '</a>' : '') + (s.domain ? '<br><span class="muted">' + esc(s.domain) + '</span>' : '') + '</div>';
  }).join('') + '</div>' : '<div class="muted">No sites yet.</div>';
  var meta = '<div class="muted" style="font-size:14px;margin-bottom:6px">' + esc(u.email) + (u.display_name ? ' · ' + esc(u.display_name) : '') + ' · joined ' + esc(dateOf(u.created_at)) + '</div>';
  var acts = u.operator ? '' : '<div class="actions">' +
    (u.suspended ? '<button type="button" class="btn ghost sm" data-user-enable="' + esc(u.id) + '">Re-enable</button>'
                 : '<button type="button" class="btn ghost sm" data-user-suspend="' + esc(u.id) + '">Suspend</button>') +
    (u.handle ? '<button type="button" class="btn ghost sm" data-goto-sites="' + esc(u.handle) + '">Open in Sites</button>' : '') + '</div>';
  return meta + why + list + acts;
}
$('u-search').addEventListener('input', function () { US.q = this.value; US.page = 0; renderUsers(); });
chips('u-chips', US, renderUsers);
headers('u-table', US, renderUsers, ['handle']);
$('u-rows').addEventListener('click', function (e) {
  if (e.target.closest('a') || e.target.closest('button:not(.row-toggle)')) return;
  var tr = e.target.closest('tr[data-user]'); if (!tr) return;
  var id = tr.dataset.user; US.open[id] = !US.open[id]; renderUsers();
});

// ---------------------------------------------------------------- sites
var SS = { q: '', filter: 'all', sort: 'updated', dir: -1, page: 0 };
var SITE_KEYS = {
  created: function (s) { return new Date(s.created_at).getTime(); }, updated: function (s) { return new Date(s.changed_at).getTime(); },
  size: function (s) { return s.bytes || 0; }, live: function (s) { return s.live_bytes || 0; }, views: function (s) { return s.views_30d || 0; },
  people: function (s) { return s.people_30d || 0; }, name: function (s) { return s.name; }, owner: function (s) { return (s.owner || '').toLowerCase(); }
};
function siteStatus(s) {
  if (s.owner_suspended) return '<span class="tag down">suspended</span>';
  if (s.taken_down) return '<span class="tag down">taken down</span>';
  if (s.offline) return '<span class="tag">offline</span>';
  if (!s.live_version) return '<span class="tag">not live</span>';
  return '<span class="tag ok">live</span>';
}
var ACCESS = { open: 'anyone', passcode: 'passcode', viewers: 'named people' };
function renderSites() {
  var rows = D.sites.filter(function (s) {
    var f = SS.filter;
    if (f === 'down' && !s.taken_down && !s.owner_suspended) return false;
    if (f === 'offline' && !s.offline) return false;
    if (f === 'locked' && s.access === 'open') return false;
    if (f === 'domain' && !s.domain && !s.short_name) return false;
    if (f === 'unlisted' && s.listed) return false;
    return searchMatches(SS.q, s.name + ' ' + (s.owner || '') + ' ' + (s.domain || '') + ' ' + (s.short_name || ''));
  });
  rows = sortBy(rows, SS.sort, SS.dir, SITE_KEYS);
  $('s-count').textContent = '(' + (rows.length !== D.sites.length ? rows.length + ' of ' : '') + D.sites.length + ')';
  $('s-sort').value = SS.sort;
  arrows('s-table', SS);
  var pages = Math.max(1, Math.ceil(rows.length / PAGE)); if (SS.page >= pages) SS.page = pages - 1;
  $('s-rows').innerHTML = rows.slice(SS.page * PAGE, (SS.page + 1) * PAGE).map(function (s) {
    var dom = s.domain ? esc(s.domain) + (s.domain_status && s.domain_status !== 'live' && s.domain_status !== 'active' ? ' <span class="muted">· ' + esc(s.domain_status) + '</span>' : '') : '';
    if (s.short_name) dom += (dom ? '<div class="sub">' : '') + esc(s.short_name) + ' <span class="muted">· short name</span>' + (dom ? '</div>' : '');
    return '<tr data-site="' + esc(s.id) + '"><td>' + siteName(s) +
      '<span class="mobile-metrics">' + esc(ownerName(s)) + ' · ' + (s.bytes / 1048576).toFixed(1) + ' MB · ' + num(s.views_30d) + ' views · ' + esc(ago(s.changed_at)) + (s.taken_down || s.owner_suspended || s.offline ? ' ' + siteStatus(s) : '') + '</span></td>' +
      '<td>' + esc(ownerName(s)) + '</td><td class="num">' + bytes(s.live_bytes) + '</td><td class="num">' + (s.bytes / 1048576).toFixed(2) + '</td>' +
      '<td>' + when(s.changed_at) + '</td><td class="num">' + num(s.views_30d) + '</td><td class="num">' + num(s.people_30d) + '</td>' +
      '<td>' + esc(ACCESS[s.access] || s.access) + (s.listed ? '' : ' <span class="muted">· not on profile</span>') + '</td><td>' + siteStatus(s) + '</td><td>' + (dom || '<span class="muted">-</span>') + '</td>' +
      '<td><button type="button" class="more" aria-haspopup="menu" aria-expanded="false" aria-label="Actions for ' + esc(s.name) + '" data-menu="' + esc(s.id) + '">&#8943;</button></td></tr>';
  }).join('') || '<tr><td colspan="11" class="empty">No site matches.</td></tr>';
  pager('s-pager', SS, rows.length, renderSites);
}
$('s-sort').addEventListener('change', function () { SS.sort = this.value; SS.dir = (SS.sort === 'name' || SS.sort === 'owner') ? 1 : -1; SS.page = 0; renderSites(); });
var searchTimer;
$('s-search').addEventListener('input', function () { var v = this.value; clearTimeout(searchTimer); searchTimer = setTimeout(function () { SS.q = v; SS.page = 0; renderSites(); }, 150); });
chips('s-chips', SS, renderSites);
headers('s-table', SS, renderSites, ['name', 'owner']);

// Row menu (styled by .row-menu in site.css)
var menuEl = null, menuBtn = null;
function closeMenu() {
  if (menuEl) { menuEl.remove(); menuEl = null; }
  if (menuBtn) { menuBtn.setAttribute('aria-expanded', 'false'); var b = menuBtn; menuBtn = null; return b; }
  return null;
}
function siteById(id) { for (var i = 0; i < D.sites.length; i++) if (D.sites[i].id === id) return D.sites[i]; return null; }
function place() {
  var r = menuBtn.getBoundingClientRect(), mw = menuEl.offsetWidth, mh = menuEl.offsetHeight;
  var left = Math.max(8, Math.min(window.innerWidth - mw - 8, r.right - mw));
  var top = r.bottom + 4; if (top + mh > window.innerHeight - 8) top = Math.max(8, r.top - mh - 4);
  menuEl.style.left = left + 'px'; menuEl.style.top = top + 'px';
}
function openMenu(btn) {
  var s = siteById(btn.dataset.menu); if (!s) return;
  closeMenu();
  var items = [];
  if (s.url) items.push('<a class="mi" role="menuitem" href="' + href(s.url) + '" target="_blank" rel="noopener">Open site</a>');
  if (s.owner) items.push('<a class="mi" role="menuitem" href="' + dashLink(s) + '">Versions, data and settings</a>');
  items.push('<div class="mi-sep" role="separator"></div>');
  if (s.owner_suspended) items.push('<span class="mi" role="menuitem" aria-disabled="true">Down with the account (re-enable it in Users)</span>');
  else if (s.taken_down) items.push('<button type="button" class="mi" role="menuitem" data-act="restore">Restore</button>');
  else items.push('<button type="button" class="mi danger" role="menuitem" data-act="takedown">Take down</button>');
  menuEl = document.createElement('div');
  menuEl.className = 'row-menu'; menuEl.setAttribute('role', 'menu'); menuEl.setAttribute('aria-label', 'Actions for ' + s.name);
  menuEl.innerHTML = items.join('');
  document.body.appendChild(menuEl);
  menuBtn = btn; btn.setAttribute('aria-expanded', 'true');
  place();
  var first = menuEl.querySelector('.mi:not([aria-disabled])'); if (first) first.focus({ preventScroll: true });
  menuEl.addEventListener('keydown', function (e) {
    var all = [].slice.call(menuEl.querySelectorAll('.mi:not([aria-disabled])')), i = all.indexOf(document.activeElement);
    if (e.key === 'ArrowDown') { e.preventDefault(); all[(i + 1) % all.length].focus(); }
    else if (e.key === 'ArrowUp') { e.preventDefault(); all[(i + all.length - 1) % all.length].focus(); }
    else if (e.key === 'Escape') { e.preventDefault(); var b = closeMenu(); if (b) b.focus(); }
    else if (e.key === 'Tab') closeMenu();
  });
  menuEl.addEventListener('click', function (e) {
    var it = e.target.closest('[data-act]');
    if (!it) { if (e.target.closest('a.mi')) closeMenu(); return; }
    closeMenu();
    if (it.dataset.act === 'takedown') takeDown(s); else restoreSite(s);
  });
}
document.addEventListener('click', function (e) {
  var b = e.target.closest('button[data-menu]');
  if (b) { if (menuBtn === b) closeMenu(); else openMenu(b); return; }
  if (menuEl && !e.target.closest('.row-menu')) closeMenu();
});
window.addEventListener('resize', function () { closeMenu(); });
window.addEventListener('scroll', function (e) {
  if (!menuEl || !menuBtn || menuEl.contains(e.target)) return;
  var r = menuBtn.getBoundingClientRect();
  if (r.bottom < 0 || r.top > window.innerHeight) closeMenu(); else place();
}, true);

// ---------------------------------------------------------------- actions
function sitePath(s) { return '/v1/sites/' + encodeURIComponent(s.name) + '?owner=' + encodeURIComponent(s.owner); }
function takeDown(s) {
  ask({ title: 'Why? One line. The owner sees this.', body: 'Take down ' + s.name + ': every address shows "This site was taken down" and changes are refused. Restore puts it back.', label: 'Reason', confirmLabel: 'Take down', danger: true }).then(function (r) {
    if (!r) return;
    api('PATCH', sitePath(s), { taken_down: true, reason: r }).then(function () { return reload('Took down ' + s.name + '.'); }, function (e) { status(e.message, true); });
  });
}
function restoreSite(s) { api('PATCH', sitePath(s), { taken_down: false }).then(function () { return reload('Restored ' + s.name + '.'); }, function (e) { status(e.message, true); }); }
function suspendUser(u) {
  ask({ title: 'Why? One line. The owner sees this.', body: 'Suspend ' + (u.handle || u.email) + ': their keys, connected apps and sign-in stop working and all their sites are taken down. Re-enable reverses it.', label: 'Reason', confirmLabel: 'Suspend', danger: true }).then(function (r) {
    if (!r) return;
    api('PATCH', '/v1/admin/people/' + encodeURIComponent(u.id), { suspended: true, reason: r }).then(function () { return reload('Suspended ' + (u.handle || u.email) + '.'); }, function (e) { status(e.message, true); });
  });
}
function enableUser(u) { api('PATCH', '/v1/admin/people/' + encodeURIComponent(u.id), { suspended: false }).then(function () { return reload('Re-enabled ' + (u.handle || u.email) + '.'); }, function (e) { status(e.message, true); }); }
$('admin-main').addEventListener('click', function (e) {
  var b = e.target.closest('button'); if (!b) return;
  var d = b.dataset;
  if (d.userSuspend) suspendUser(D.byId[d.userSuspend]);
  else if (d.userEnable) enableUser(D.byId[d.userEnable]);
  else if (d.siteRestore) restoreSite(siteById(d.siteRestore));
  else if (d.siteTakedown) takeDown(siteById(d.siteTakedown));
  else if (d.gotoSites != null) {
    SS.q = d.gotoSites; SS.filter = 'all'; SS.page = 0; $('s-search').value = d.gotoSites;
    document.querySelectorAll('#s-chips .chip').forEach(function (x) { x.setAttribute('aria-pressed', x.dataset.filter === 'all' ? 'true' : 'false'); });
    history.replaceState(null, '', '#sites'); showTab('sites');
  }
});

// ---------------------------------------------------------------- moderation
function renderModeration() {
  var reports = (D.overview && D.overview.reports) || [];
  $('r-count').textContent = '(' + reports.length + ')';
  $('r-rows').innerHTML = reports.map(function (r) {
    var p = r.owner && D.byHandle[r.owner], s = p && p.siteList.filter(function (x) { return x.name === r.site; })[0];
    return '<tr><td class="wrap"><a href="' + href(r.url) + '" target="_blank" rel="noopener">' + esc(r.url) + '</a>' + (s ? '<div class="sub">' + esc(s.name) + ' of ' + esc(s.owner) + '</div>' : '') + '</td>' +
      '<td class="wrap">' + esc(r.reason) + '</td><td>' + when(r.created_at) + '</td>' +
      '<td>' + (s ? (s.taken_down ? '<span class="tag down">taken down</span>' : '<button type="button" class="btn ghost sm" data-site-takedown="' + esc(s.id) + '">Take down</button>') +
        ' <a class="btn ghost sm" href="' + dashLink(s) + '">Open</a>' : '') + '</td></tr>';
  }).join('') || '<tr><td colspan="4" class="empty">No open reports.</td></tr>';
  var rows = [];
  D.people.forEach(function (u) {
    if (u.suspended) rows.push('<tr><td><strong>' + esc(u.handle || u.email) + '</strong> <span class="tag down">account suspended</span><div class="sub">' + num(u.sites) + ' site' + (u.sites === 1 ? '' : 's') + ' down with it</div></td><td>' + esc(u.email) + '</td><td class="wrap">' + esc(u.suspended_reason) + '</td>' +
      '<td><button type="button" class="btn ghost sm" data-user-enable="' + esc(u.id) + '">Re-enable</button></td></tr>');
  });
  D.sites.forEach(function (s) {
    if (!s.taken_down || s.owner_suspended) return;
    rows.push('<tr><td><strong>' + esc(s.name) + '</strong> <span class="tag down">taken down</span></td><td>' + esc(ownerName(s)) + '</td><td class="wrap">' + esc(s.taken_down_reason) + '</td>' +
      '<td><button type="button" class="btn ghost sm" data-site-restore="' + esc(s.id) + '">Restore</button></td></tr>');
  });
  $('m-count').textContent = '(' + rows.length + ')';
  $('m-rows').innerHTML = rows.join('') || '<tr><td colspan="4" class="empty">Nothing is taken down.</td></tr>';
}

// ---------------------------------------------------------------- api growth
var growthStarted = false;
function renderApi() { if (!growthStarted && window.shGrowth) { growthStarted = true; window.shGrowth.start(); } }

// ---------------------------------------------------------------- tools
$('make').addEventListener('submit', function (e) {
  e.preventDefault();
  api('POST', '/v1/admin/people', { email: $('m-email').value.trim() }).then(function (b) {
    status(b.says || 'Account made. They sign in with that email.'); $('m-email').value = ''; reload();
  }, function (err) { status(err.message, true); });
});

// ---------------------------------------------------------------- start
function refused(e) {
  var main = $('admin-main');
  if (e.status === 401) { location.replace('/signin?next=/admin'); return; }
  if (e.status === 403 && !e.checked) {
    // Not the operator, or not signed in at all: /v1/me tells which.
    api('GET', '/v1/me').then(function () { e.checked = true; refused(e); }, function (m) { if (m.status === 401) refused(m); else { e.checked = true; refused(e); } });
    return;
  }
  if (e.status === 403) {
    main.innerHTML = '<p class="page-tag">Admin</p><div class="sec" style="max-width:560px"><h2>Only the operator can open this page</h2>' +
      '<p class="ph">You are signed in with an account that is not the operator\'s.</p><p><a class="btn ghost" href="/dashboard">Back to your sites</a></p></div>';
    return;
  }
  status('Could not load: ' + e.message, true);
}
status('Loading…');
load().then(function () {
  $('status').hidden = true;
  $('tab-tools').hidden = !(D.overview && D.overview.self_hosted);
  $('tabs').hidden = false;
  showTab(tabFromHash());
}, refused);
})();
