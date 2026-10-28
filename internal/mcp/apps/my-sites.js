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
    if (site.display?.passcode_protected) return 'Passcode';
    if (site.display?.access === 'specific') return 'Named viewers';
    if (site.display?.access === 'anyone') return 'Anyone';
    return 'Access unknown';
  }
  function objects(result) {
    return (result.rows || []).map(row => Object.fromEntries(result.columns.map((key, i) => [key, row[i]])));
  }
  const identifier = value => '"' + String(value).replaceAll('"', '""') + '"';
  const entryPreset = policy => ['inbox', 'records'].includes(policy?.preset) || ['inbox', 'records'].includes(policy?.based_on);
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
  const hack = root.SHHackSites?.(app, document.body.dataset.mode === 'events');
  let sites = [], selected = null, generation = 0, scoped = false, listLoaded = false, listGeneration = 0;
  function renderSites() {
    $('rows').replaceChildren();
    const words = $('search').value.toLowerCase().trim().split(/\s+/);
    const filtered = sites.filter(site => words.every(word => [site.name, site.summary?.title, site.summary?.description, site.url].join(' ').toLowerCase().includes(word)));
    for (const site of filtered) {
      const row = node(document, 'button', 'site-row'); row.type = 'button';
      const tile = node(document, 'span', 'site-thumb', (site.summary?.title || site.name || '?').slice(0, 1).toUpperCase());
      tile.setAttribute('aria-hidden', 'true');
      const info = node(document, 'span', 'row-info');
      info.append(node(document, 'strong', '', site.summary?.title || site.name));
      info.append(node(document, 'span', 'row-description', site.label || site.summary?.description || site.summary?.text || site.name));
      const meta = node(document, 'span', 'row-meta');
      if (!hack || site.display?.updated_at) meta.append(node(document, 'span', '', 'updated ' + relative(site.display?.updated_at)));
      if (!hack) meta.append(node(document, 'span', 'badge', badge(site)));
      info.append(meta);
      const chevron = node(document, 'span', 'arrow', '›'); chevron.setAttribute('aria-hidden', 'true');
      row.append(tile, info, chevron);
      row.addEventListener('click', () => {
        if (document.body.dataset.dashboard === 'true' && app.hostCapabilities?.serverTools && (!hack || hack.canDashboard(site))) showDashboard(site);
        else app.openLink(site.url).catch(() => { $('list-status').textContent = 'Could not open this site. Try again.'; });
      });
      $('rows').append(row);
    }
    $('view-label').textContent = scoped ? 'Showing ' + sites.length + ' sites' : 'My sites';
    $('list-status').textContent = filtered.length ? '' : sites.length ? 'No matching sites.' : 'No sites yet.';
  }
  async function callOperation(method, name, args) {
    try { return await app[method](name, args); }
    catch (error) {
      if (!/unknown[ _-]tool\b/i.test(error.message || '')) throw error;
      return app[method]('run_tool', {name, arguments: args});
    }
  }
  const advanced = (name, args) => callOperation('callTool', name, args);
  async function loadAnalytics(site, current) {
    try {
      const result = await (hack ? hack.analytics(site) : callOperation('callToolResult', 'site_analytics', {site: site.name, days: 7}));
      if (current !== generation) return;
      const total = result.structuredContent?.totals?.person?.visitors;
      $('visitor-count').textContent = Number.isFinite(total) ? total.toLocaleString() : 'Unavailable';
      const daily = result._meta?.['simple-host/daily'];
      if (!Array.isArray(daily) || daily.length !== 7) { $('analytics-error').textContent = 'Daily visitors are unavailable.'; return; }
      const max = Math.max(1, ...daily.map(day => day.person?.visitors || 0));
      const labels = [];
      for (const day of daily) {
        const count = day.person?.visitors || 0;
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
      const listing = await (hack ? hack.storage(site, 'storage_list_resources') : advanced('storage_list_resources', {site: site.name}));
      const entries = [], sources = [], failed = [];
      for (const resource of listing.response?.resources || []) {
        if (current !== generation) return;
        if (resource.kind !== 'sqlite' || !(entryPreset(resource) || Object.values(resource.tables || {}).some(entryPreset))) continue;
        const query = async sql => (await (hack ? hack.storage(site, 'storage_sql_query', {name: resource.name, sql, params: []}) : app.callTool('storage_sql_query', {site: site.name, name: resource.name, sql, params: []}))).response;
        // Tables inherit the resource preset unless they have an explicit override.
        let tables;
        try { tables = objects(await query("SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT GLOB 'sqlite_*' ORDER BY name")); }
        catch { failed.push(resource.name); continue; }
        for (const {name} of tables) {
          if (current !== generation) return;
          const key = name.toLowerCase();
          const policy = Object.hasOwn(resource.tables || {}, key) ? resource.tables[key] : resource;
          if (!entryPreset(policy)) continue;
          const source = resource.name + (name === resource.name ? '' : ' / ' + name);
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
      if (!sources.length) {
        if (hack && !failed.length) {
          $('entries').hidden = false; $('entries-title').textContent = 'Latest entries';
          $('entry-rows').append(node(document, 'p', 'subtle', 'No entries yet.'));
        }
        return;
      }
      entries.sort((a, b) => (Date.parse(b.created_at) || 0) - (Date.parse(a.created_at) || 0) || Number(b.id) - Number(a.id));
      $('entries').hidden = false;
      $('entries-title').textContent = 'Latest entries · ' + sources.join(', ');
      for (const entry of entries.slice(0, 3)) $('entry-rows').append(renderEntry(document, entry));
      if (!entries.length) $('entry-rows').append(node(document, 'p', 'subtle', 'No entries yet.'));
    } catch (_) { if (current === generation) $('entries-error').textContent = 'Could not load entries. Reopen this view to try again.'; }
  }
  async function showDashboard(site) {
    selected = site; const current = ++generation;
    $('site-list').hidden = true; $('dashboard').hidden = false; $('view-label').textContent = 'Weekly overview';
    $('site-name').textContent = site.summary?.title || site.name; $('visitor-count').textContent = '…';
    for (const id of ['chart','entry-rows','analytics-error','entries-error','link-error']) $(id).replaceChildren();
    $('entries').hidden = true; $('back').focus();
    $('analytics').hidden = !!hack && !hack.hasAnalytics(site);
    if (hack) {
      try { await hack.check(site); }
      catch (error) { if (current === generation) { $('analytics').hidden = true; $('entries-error').textContent = error.message; } return; }
      if (current !== generation) return;
    }
    if (!hack || hack.hasAnalytics(site)) loadAnalytics(site, current);
    loadEntries(site, current);
  }
  $('search').addEventListener('input', renderSites);
  $('back').addEventListener('click', () => {
    ++generation; selected = null; $('dashboard').hidden = true; $('site-list').hidden = false;
    $('view-label').textContent = 'My sites'; renderSites(); $('search').focus();
  });
  $('open-site').addEventListener('click', () => app.openLink(selected.url).catch(() => { $('link-error').textContent = 'Could not open this site. Try again.'; }));
  app.on('tool-input', params => {
    scoped = ['query', 'filters', 'created_after', 'created_before', 'updated_after', 'updated_before', 'limit', 'offset'].some(key => Object.hasOwn(params.arguments || {}, key));
  });
  function listFailed(message) {
    if (listLoaded) return;
    sites = []; $('rows').replaceChildren();
    $('list-status').textContent = message;
  }
  app.on('tool-cancelled', () => { ++listGeneration; listFailed('Loading sites was cancelled. Use the chat to try again.'); });
  app.on('tool-result', async result => {
    if (hack && app.hostCapabilities?.serverTools) {
      if (!Array.isArray(result.structuredContent?.sites) && (!result.isError || listLoaded)) return;
      const current = ++listGeneration;
      try {
        const listing = await hack.list(result.structuredContent?.sites || [], scoped);
        if (current !== listGeneration) return;
        ++generation; selected = null; $('dashboard').hidden = true; $('site-list').hidden = false;
        sites = listing.sites; listLoaded = true; $('team-status').textContent = listing.status; renderSites();
      } catch (_) { if (current === listGeneration) listFailed('Could not load teams. Use the chat to try again.'); }
      return;
    }
    if (result.isError) { listFailed('Could not load sites. Use the chat to try again.'); return; }
    if (!Array.isArray(result.structuredContent?.sites)) return;
    listLoaded = true;
    sites = result.structuredContent.sites;
    if (!selected) renderSites();
  });
  app.start().catch(() => { $('list-status').textContent = 'Could not connect. Use the site links in the chat.'; });
})(globalThis);
