(function (root) {
  'use strict';
  // All site and entry strings go through this one text-only constructor.
  function node(document, tag, className, text) {
    const el = document.createElement(tag);
    if (className) el.className = className;
    if (text !== undefined) el.textContent = String(text);
    return el;
  }
  function relative(value, now = Date.now()) {
    const stamp = Date.parse(value);
    if (!Number.isFinite(stamp)) return 'time unavailable';
    const minutes = Math.max(0, Math.floor((now - stamp) / 60000));
    if (minutes < 1) return 'just now';
    if (minutes < 60) return minutes + (minutes === 1 ? ' minute ago' : ' minutes ago');
    const hours = Math.floor(minutes / 60);
    if (hours < 24) return hours + (hours === 1 ? ' hour ago' : ' hours ago');
    const days = Math.floor(hours / 24);
    return days === 1 ? 'yesterday' : days + ' days ago';
  }
  function badge(site) {
    if (site.offline) return 'Offline';
    if (site.access === 'passcode') return 'Passcode';
    if (site.access === 'viewers') return 'Named viewers';
    if (site.access === 'open') return 'Anyone';
    return 'Access unknown';
  }
  function objects(result) {
    return (result.rows || []).map(row => Object.fromEntries(result.columns.map((key, i) => [key, row[i]])));
  }
  const identifier = value => '"' + String(value).replaceAll('"', '""') + '"';
  // Entries live in tables whose rules let visitors add rows (an inbox, records).
  const ownerOnly = expression => /^\s*auth\.role\s*==\s*"owner"\s*$/.test(expression ?? '');
  const visitorsAdd = rules => (rules?.policies || []).some(policy =>
    !policy.restrictive && ['add', 'all'].includes(policy.command) && !ownerOnly(policy.check ?? policy.using));
  function renderEntry(document, entry) {
    const row = node(document, 'div', 'inquiry');
    row.append(node(document, 'strong', '', entry.name || entry.full_name || entry.email || 'Entry'));
    const summary = entry.message || entry.summary || entry.details || entry.subject || entry.title || entry.description ||
      Object.entries(entry).filter(([key, value]) => !['id','visitor_id','created_at','updated_at','name','email','resource'].includes(key) && ['string','number'].includes(typeof value)).map(([, value]) => value).join(' · ');
    row.append(node(document, 'p', '', summary || 'New entry'));
    row.append(node(document, 'time', '', relative(entry.created_at)));
    return row;
  }
  root.SHSites = {node, relative, badge, objects, identifier, renderEntry};
  if (typeof document === 'undefined') return;
  const $ = id => document.getElementById(id), app = root.SHApp;
  let sites = [], selected = null, generation = 0, scoped = false, listLoaded = false, args = {};
  const address = url => String(url || '').replace(/^https:\/\//, '').replace(/\/$/, '');
  function renderSites() {
    $('rows').replaceChildren();
    const words = $('search').value.toLowerCase().trim().split(/\s+/);
    const filtered = sites.filter(site => words.every(word => [site.name, site.owner, site.url].join(' ').toLowerCase().includes(word)));
    for (const site of filtered) {
      const row = node(document, 'button', 'site-row'); row.type = 'button';
      const tile = node(document, 'span', 'site-thumb', (site.name || '?').slice(0, 1).toUpperCase());
      tile.setAttribute('aria-hidden', 'true');
      const info = node(document, 'span', 'row-info');
      info.append(node(document, 'strong', '', site.name));
      info.append(node(document, 'span', 'row-description', address(site.url) || site.name));
      const meta = node(document, 'span', 'row-meta');
      meta.append(node(document, 'span', '', 'updated ' + relative(site.changed_at)));
      meta.append(node(document, 'span', 'badge', badge(site)));
      info.append(meta);
      const chevron = node(document, 'span', 'arrow', '›'); chevron.setAttribute('aria-hidden', 'true');
      row.append(tile, info, chevron);
      row.addEventListener('click', () => {
        if (app.hostCapabilities?.serverTools) showDashboard(site);
        else app.openLink(site.url).catch(() => { $('list-status').textContent = 'Could not open this site. Try again.'; });
      });
      $('rows').append(row);
    }
    $('view-label').textContent = scoped ? 'Showing ' + sites.length + ' sites' : 'My sites';
    $('list-status').textContent = filtered.length ? '' : sites.length ? 'No matching sites.' : 'No sites yet.';
  }
  const which = site => site.owner ? {site: site.name, owner: site.owner} : {site: site.name};
  // The last seven UTC days, today last; days without visits count 0.
  function week(days) {
    const byDay = Object.fromEntries((days || []).map(day => [day.day, day.people || 0])), out = [];
    for (let i = 6; i >= 0; i--) {
      const day = new Date(Date.now() - i * 86400000).toISOString().slice(0, 10);
      out.push({day, people: byDay[day] || 0});
    }
    return out;
  }
  async function loadAnalytics(site, current, stats) {
    try {
      const result = stats || (await app.callToolResult('site_stats', {...which(site), days: 7})).structuredContent;
      if (current !== generation) return;
      if (!Array.isArray(result?.days)) { $('visitor-count').textContent = 'Unavailable'; $('analytics-error').textContent = 'Daily visitors are unavailable.'; return; }
      const daily = week(result.days);
      const total = daily.reduce((sum, day) => sum + day.people, 0);
      $('visitor-count').textContent = total.toLocaleString();
      const max = Math.max(1, ...daily.map(day => day.people));
      const labels = [];
      for (const day of daily) {
        const count = day.people;
        const label = new Date(day.day + 'T00:00:00Z').toLocaleDateString(undefined, {weekday: 'short', timeZone: 'UTC'});
        labels.push(day.day + ': ' + count + ' visitors');
        const column = node(document, 'div', 'chart-column'); column.title = labels.at(-1);
        const bar = node(document, 'span', 'chart-bar'); bar.style.height = (count / max * 110) + 'px';
        column.append(node(document, 'span', 'chart-count', count), bar, node(document, 'span', 'chart-day', label)); $('chart').append(column);
      }
      $('chart').setAttribute('aria-label', labels.join(', '));
    } catch (_) { if (current === generation) { $('visitor-count').textContent = 'Unavailable'; $('analytics-error').textContent = 'Could not load visitors. Reopen this view to try again.'; } }
  }
  async function loadEntries(site, current) {
    try {
      const info = await app.callTool('get_site', which(site));
      if (current !== generation) return;
      if (info.url) selected.url = info.url;
      const entries = [], sources = [], failed = [];
      for (const space of info.namespaces || []) {
        const query = async sql => app.callTool('run_sql', {...which(site), namespace: space.name, sql, params: []});
        for (const name of space.tables || []) {
          if (current !== generation) return;
          if (name.startsWith('_') || !visitorsAdd(space.rules?.[name])) continue;
          const source = space.name + (name === space.name ? '' : ' / ' + name);
          try {
            const schema = await query('SELECT * FROM ' + identifier(name) + ' LIMIT 0');
            const columns = schema.columns.map(column => column.toLowerCase());
            const order = [columns.includes('created_at') && 'created_at DESC', columns.includes('id') && 'id DESC'].filter(Boolean).join(', ') || 'rowid DESC';
            const rows = objects(await query('SELECT * FROM ' + identifier(name) + ' ORDER BY ' + order + ' LIMIT 3'));
            sources.push(source); entries.push(...rows.map(row => ({...row, resource: source})));
          } catch { failed.push(source); }
        }
      }
      if (current !== generation) return;
      if (failed.length) $('entries-error').textContent = 'Could not load: ' + failed.join(', ') + '.';
      if (!sources.length) return;
      entries.sort((a, b) => (Date.parse(b.created_at) || 0) - (Date.parse(a.created_at) || 0) || Number(b.id) - Number(a.id));
      $('entries').hidden = false;
      $('entries-title').textContent = 'Latest entries · ' + sources.join(', ');
      for (const entry of entries.slice(0, 3)) $('entry-rows').append(renderEntry(document, entry));
      if (!entries.length) $('entry-rows').append(node(document, 'p', 'subtle', 'No entries yet.'));
    } catch (_) { if (current === generation) $('entries-error').textContent = 'Could not load entries. Reopen this view to try again.'; }
  }
  function showDashboard(site, stats) {
    selected = site; const current = ++generation;
    $('site-list').hidden = true; $('dashboard').hidden = false; $('view-label').textContent = 'Weekly overview';
    $('site-name').textContent = site.name; $('visitor-count').textContent = '…';
    for (const id of ['chart','entry-rows','analytics-error','entries-error','link-error']) $(id).replaceChildren();
    $('entries').hidden = true; if (!stats) $('back').focus();
    loadAnalytics(site, current, stats);
    if (app.hostCapabilities?.serverTools) loadEntries(site, current);
  }
  $('search').addEventListener('input', renderSites);
  $('back').addEventListener('click', () => {
    ++generation; selected = null; $('dashboard').hidden = true; $('site-list').hidden = false;
    $('view-label').textContent = 'My sites'; $('search').hidden = false; renderSites(); $('search').focus();
    if (!listLoaded) app.callTool('list_sites').then(result => { listLoaded = true; sites = result.sites || []; if (!selected) renderSites(); })
      .catch(() => { $('list-status').textContent = 'Could not load sites. Use the chat to try again.'; });
  });
  $('open-site').addEventListener('click', () => (selected.url ? app.openLink(selected.url) : Promise.reject()).catch(() => { $('link-error').textContent = 'Could not open this site. Try again.'; }));
  app.on('tool-input', params => {
    args = params.arguments || {};
    scoped = Object.hasOwn(args, 'q') || args.bin === true;
  });
  function listFailed(message) {
    if (listLoaded) return;
    sites = []; $('rows').replaceChildren();
    $('list-status').textContent = message;
  }
  app.on('tool-cancelled', () => listFailed(document.body.dataset.view === 'dashboard' ? 'Loading visitor numbers was cancelled. Use the chat to try again.' : 'Loading sites was cancelled. Use the chat to try again.'));
  app.on('tool-result', result => {
    // Visitor numbers: site_stats opens this screen on one site's numbers.
    if (document.body.dataset.view === 'dashboard') {
      if (result.isError || typeof args.site !== 'string') { listFailed('Could not load visitor numbers. Use the chat to try again.'); return; }
      showDashboard({name: args.site, owner: args.owner}, (args.days ?? 30) >= 7 ? result.structuredContent : null);
      return;
    }
    if (result.isError) { listFailed('Could not load sites. Use the chat to try again.'); return; }
    if (!Array.isArray(result.structuredContent?.sites)) return;
    listLoaded = true;
    sites = result.structuredContent.sites;
    if (!selected) renderSites();
  });
  if (document.body.dataset.view === 'dashboard') { $('search').hidden = true; $('list-status').textContent = 'Loading visitor numbers…'; }
  app.start().catch(() => { $('list-status').textContent = 'Could not connect. Use the site links in the chat.'; });
})(globalThis);
