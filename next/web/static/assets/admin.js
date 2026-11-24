// The operator's page: people, sites, sizes, new sign-ups, visitor reports
// and network use from GET /v1/admin/overview, and the API growth tab
// (admin-growth.js). Suspending is PATCH /v1/admin/people/{id}. Opening a
// site's versions and data, or taking it down, is the ordinary site page in
// the dashboard: the operator may open any site.
//
// GET /v1/admin/overview answers {people: {total, new_7d, new_30d}, sites: {total, bytes},
//   signups: [{day, count}], people_list: [{id, email, handle, created_at, sites, bytes, suspended}],
//   largest_sites: [{owner, name, bytes, url}], reports: [{id, url, reason, created_at}],
//   network: [{day, bytes_in, bytes_out}], self_hosted, growth: {...}}
(function () {
  'use strict';
  var box = document.getElementById('overview'), growth = document.getElementById('growth'), started = false;
  function esc(s) { return String(s == null ? '' : s).replace(/[&<>"']/g, function (c) { return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]; }); }
  function num(n) { return Number(n || 0).toLocaleString('en-US'); }
  function bytes(n) { n = Number(n) || 0; return n >= 1e9 ? (n / 1e9).toFixed(1) + ' GB' : n >= 1e6 ? (n / 1e6).toFixed(1) + ' MB' : Math.round(n / 1e3) + ' KB'; }
  function day(s) { return s ? new Date(s).toLocaleDateString() : ''; }
  function siteLink(owner, name) { return '/dashboard#site/' + encodeURIComponent(owner) + '/' + encodeURIComponent(name); }
  function api(method, path, body) {
    return fetch(path, { method: method, credentials: 'same-origin', headers: body ? { 'Content-Type': 'application/json' } : {}, body: body ? JSON.stringify(body) : undefined })
      .then(function (r) {
        if (r.status === 401) { location.replace('/signin?next=/admin'); return new Promise(function () {}); }
        return r.json().catch(function () { return {}; }).then(function (b) { if (!r.ok) throw new Error(b.fix || 'That did not work.'); return b; });
      });
  }
  function tab() {
    var g = location.hash === '#growth';
    box.hidden = g; growth.hidden = !g;
    if (g && !started) { started = true; window.shGrowth.start(); }
  }
  function render(o) {
    var p = o.people || {}, s = o.sites || {}, net = o.network || [];
    var inTotal = net.reduce(function (a, d) { return a + (d.bytes_in || 0); }, 0), outTotal = net.reduce(function (a, d) { return a + (d.bytes_out || 0); }, 0);
    box.innerHTML =
      '<h1>Admin</h1>' +
      '<div class="grid2">' +
        '<div class="panel"><h3>People</h3><p class="big">' + num(p.total) + '</p><p class="muted">' + num(p.new_7d) + ' new this week, ' + num(p.new_30d) + ' in 30 days</p></div>' +
        '<div class="panel"><h3>Sites</h3><p class="big">' + num(s.total) + '</p><p class="muted">' + bytes(s.bytes) + ' on disk</p></div>' +
        '<div class="panel"><h3>Network, last ' + net.length + ' days</h3><p class="big">' + bytes(outTotal) + ' out</p><p class="muted">' + bytes(inTotal) + ' in</p></div>' +
      '</div>' +
      '<section><h2>Open a site</h2><p class="muted">See any site\'s versions and data, or take it down, on its ordinary page.</p>' +
        '<form class="row" id="open"><input id="o-owner" placeholder="handle" aria-label="Owner handle"><input id="o-name" placeholder="site name" aria-label="Site name"><button class="primary">Open</button></form></section>' +
      '<section><h2>Reports</h2><ul class="list">' + ((o.reports || []).map(function (r) {
        return '<li><div class="grow"><div><a href="' + esc(r.url) + '" target="_blank" rel="noopener">' + esc(r.url) + '</a></div><div class="muted">' + esc(r.reason) + ' · ' + day(r.created_at) + '</div></div>' +
          (r.owner && r.site ? '<a class="btn" href="' + siteLink(r.owner, r.site) + '">Open site</a>' : '') + '</li>';
      }).join('') || '<li><span class="muted">No reports.</span></li>') + '</ul></section>' +
      (o.self_hosted ? '<section><h2>Make an account</h2><form class="row" id="make"><input id="m-email" type="email" placeholder="name@example.com" aria-label="Email"><button>Make account</button></form><p class="status" id="m-status" role="status"></p></section>' : '') +
      '<section><h2>New and recent people</h2><div class="scroll"><table><thead><tr><th>Email</th><th>Handle</th><th>Joined</th><th>Sites</th><th>Size</th><th></th></tr></thead><tbody>' +
      (o.people_list || []).map(function (x) {
        return '<tr><td>' + esc(x.email) + '</td><td>' + esc(x.handle) + '</td><td>' + day(x.created_at) + '</td><td>' + num(x.sites) + '</td><td>' + bytes(x.bytes) + '</td>' +
          '<td><button data-id="' + esc(x.id) + '" data-on="' + (x.suspended ? '1' : '') + '" class="' + (x.suspended ? '' : 'danger') + '">' + (x.suspended ? 'Restore' : 'Suspend') + '</button></td></tr>';
      }).join('') + '</tbody></table></div><p class="status" id="p-status" role="status"></p></section>' +
      '<section><h2>Largest sites</h2><ul class="list">' + (o.largest_sites || []).map(function (x) {
        return '<li><div class="grow"><a href="' + esc(x.url) + '" target="_blank" rel="noopener">' + esc(x.name) + '</a> <span class="muted">' + esc(x.owner) + ' · ' + bytes(x.bytes) + '</span></div><a class="btn" href="' + siteLink(x.owner, x.name) + '">Open</a></li>';
      }).join('') + '</ul></section>';
    document.getElementById('open').onsubmit = function (e) {
      e.preventDefault();
      location.href = siteLink(document.getElementById('o-owner').value.trim(), document.getElementById('o-name').value.trim());
    };
    var make = document.getElementById('make');
    if (make) make.onsubmit = function (e) {
      e.preventDefault();
      var st = document.getElementById('m-status');
      api('POST', '/v1/admin/people', { email: document.getElementById('m-email').value.trim() })
        .then(function () { st.textContent = 'Account made. They sign in with that email.'; }, function (err) { st.textContent = err.message; });
    };
    box.querySelectorAll('button[data-id]').forEach(function (b) {
      b.onclick = function () {
        var on = !b.dataset.on, reason = '';
        if (on) { reason = window.prompt('Why? Suspending takes all of their sites down.'); if (reason === null) return; }
        b.disabled = true;
        api('PATCH', '/v1/admin/people/' + encodeURIComponent(b.dataset.id), { suspended: on, reason: reason }).then(load, function (err) {
          document.getElementById('p-status').textContent = err.message; b.disabled = false;
        });
      };
    });
  }
  function load() { api('GET', '/v1/admin/overview').then(render, function (e) { box.innerHTML = '<p class="bad">' + esc(e.message) + '</p>'; }); }
  window.addEventListener('hashchange', tab);
  tab();
  load();
})();
