import { errText, isBlockedCode, downloadFile } from './_partB-util.js'

export function title () { return 'Website' }

// Features on this screen: get-team-publishing-key, replace-team-publishing-key,
// turn-off-team-publishing-key, upload-team-site-zip, connect-ai-app-to-publish,
// view-team-site-status, team-site-notifications, download-own-team-archive.

const NOTIFY_EVENTS = ['record.added', 'record.changed', 'file.uploaded']
const NOTIFY_EVENT_LABEL = { 'record.added': 'new entries', 'record.changed': 'changed or removed entries', 'file.uploaded': 'uploaded files' }
const NOTIFY_STATE_LABEL = { active: 'Active', paused: 'Paused', suspended: 'Suspended after failures', pending: 'Waiting for check', expired: 'Expired' }

export async function render (ctx) {
  const { main, api: client, ui, event: detail } = ctx
  const api = client.api
  const esc = ui.esc
  const ev = detail.event
  const team = detail.me.team

  main.innerHTML = '<div class="ph"><div class="t"><h1>Website</h1><p>' + esc(ev.title || '') + '</p></div></div><p class="note">Loading…</p>'

  if (!team) {
    main.innerHTML = '<div class="ph"><div class="t"><h1>Website</h1><p>' + esc(ev.title || '') + '</p></div></div><div class="locked"><h2>Join or start a team first</h2><p><a class="btn" href="/e/' + esc(encodeURIComponent(ev.slug)) + '/team">Go to Team</a></p></div>'
    return
  }

  let r
  try { r = await api('GET', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/key') } catch (e) { if (e.message === 'signed out') return; r = { ok: false, data: {} } }
  if (!r.ok) {
    main.innerHTML = '<div class="ph"><div class="t"><h1>Website</h1><p>' + esc(ev.title || '') + '</p></div></div><div class="note bad" role="alert">' + esc(errText(r, 'Could not load your team’s site.')) + '</div>'
    return
  }

  await paint(ctx, ev, team, r.data, { revealedKey: '' })
}

async function paint (ctx, ev, team, data, state) {
  const { main, api: client, ui } = ctx
  const api = client.api
  const esc = ui.esc
  const site = data.site || {}
  const frozen = typeof team.frozen === 'boolean' ? team.frozen : ev.stage === 'closed'
  const ended = ev.stage === 'archived'
  const closedForPublish = !!(site.taken_down || frozen || ended)

  const head = '<div class="ph"><div class="t"><h1>Website</h1><p>' + esc(ev.title || '') + '</p></div></div>'

  // ---- Your team site ----
  let siteCard = '<div class="card"><h2>Your team site</h2>'
  if (!site.ready) {
    siteCard += '<div class="note">Your team’s address is being set up. It is usually ready within a few minutes.</div>'
  } else if (site.taken_down) {
    siteCard += '<span class="pill bad">Taken down</span>'
    const reason = String(site.taken_down_reason || '').trim()
    siteCard += '<p class="sub">' + esc(reason || 'Taken down by Simple Host') + '</p>'
    siteCard += '<p class="muted small">Your site and entry can’t change while it is taken down.</p>'
  } else if (site.exists) {
    const href = safeHref(site.url)
    siteCard += '<span class="pill ok">Live</span>'
    if (href) siteCard += '<p>' + esc(String(site.url || '').replace(/^https?:\/\//, '')) + '</p>'
    siteCard += '<p class="sub">' + (typeof site.live_version === 'number' ? 'Published (version ' + site.live_version + ')' : 'Published') + ' · usually ready within a few minutes of publishing.</p>'
    if (href) siteCard += '<a class="btn" target="_blank" rel="noopener" href="' + esc(href) + '">View</a>'
  } else if (frozen || ended) {
    siteCard += '<div class="note">Nothing was published by the deadline.</div>'
  } else {
    siteCard += '<div class="note">Nothing published yet. Connect your AI app below, or upload a zip.</div>'
  }
  siteCard += '</div>'

  // ---- Publish (hidden once closed) ----
  let publishHTML = ''
  if (closedForPublish) {
    publishHTML = '<div class="card"><p class="note info">Publishing is closed. Your site stays live as submitted.</p></div>'
  } else {
    const mcp = data.mcp_url || (location.origin + '/mcp')
    publishHTML += '<div class="card"><h2>Connect your AI app</h2><p>It publishes to your team’s site, no key needed.</p><div class="copy"><input class="input mono" id="web-mcp" aria-label="Connector address" value="' + esc(mcp) + '" readonly></div></div>'
    publishHTML += '<div class="card"><h2>Upload a zip</h2><p class="sub">Static files only, index.html at the root. ' + esc(data.upload_limits || '') + '</p><div class="field"><label for="web-zip">Your site archive</label><input class="input" id="web-zip" type="file" accept=".zip"></div><button type="button" class="btn primary" id="web-zip-go">Upload</button><p class="note" id="web-zip-status" hidden></p></div>'

    const key = data.key
    let adv = '<p>Publish from your command line.</p>'
    if (state.revealedKey) {
      adv += '<div class="field"><label for="web-key-val">Your key — shown once, copy it now</label><div class="copy"><input class="input mono" id="web-key-val" readonly value="' + esc(state.revealedKey) + '"></div></div>' +
        '<p class="mono small" id="web-curl"></p>' +
        '<button type="button" class="btn" id="web-key-done">Done</button>'
    } else {
      adv += '<p class="mono small">curl -X PUT "' + esc((data.api_base || location.origin) + '/v1/hack/events/' + ev.slug + '/site?create=1') + '" -H "X-API-Key: YOUR_KEY" -H "Content-Type: application/zip" --data-binary @site.zip</p>'
      adv += key ? '<p class="small muted">You have a key ending ' + esc(key.last4 || '') + '.</p>' : '<p class="small muted">No key yet.</p>'
      adv += '<button type="button" class="btn" id="web-key-get">' + (key ? 'Replace key' : 'Get my key') + '</button>'
      if (key) adv += '<div class="danger-zone"><h2>Turn off key</h2><div class="setting"><div class="t"><span>Nothing can publish with this key any more.</span></div><button type="button" class="btn danger" id="web-key-off">Turn off key</button></div></div>'
    }
    publishHTML += '<details class="card"><summary>Advanced: command line &amp; API key</summary>' + adv + '</details>'
  }

  const notifyHTML = await notificationsHTML(ctx, ev, team, state)

  const downloadHTML = '<div class="card"><h2>Download our team site</h2><p class="sub">Keep a copy of your published files.</p><button type="button" class="btn" id="web-download"' + (site.exists ? '' : ' disabled') + '>Download our team site</button></div>'

  main.innerHTML = head + siteCard + publishHTML + notifyHTML + downloadHTML

  const $ = id => main.querySelector('#' + id)
  if (state.revealedKey && $('web-curl')) {
    $('web-curl').textContent = 'curl -X PUT "' + ((data.api_base || location.origin) + '/v1/hack/events/' + ev.slug + '/site?create=1') + '" -H "X-API-Key: ' + state.revealedKey + '" -H "Content-Type: application/zip" --data-binary @site.zip'
  }

  async function reload (next) {
    let r
    try { r = await api('GET', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/key') } catch (e) { if (e.message === 'signed out') return; return }
    if (!r.ok) return
    await paint(ctx, ev, team, r.data, Object.assign({}, state, next))
  }

  const mcpInput = $('web-mcp')
  if (mcpInput) mcpInput.parentElement.append(ui.copyButton(mcpInput.value, 'Copy'))

  const zipGo = $('web-zip-go')
  if (zipGo) zipGo.addEventListener('click', async () => {
    const input = $('web-zip')
    const file = input.files && input.files[0]
    const status = $('web-zip-status')
    status.hidden = false
    if (!file) { status.textContent = 'Choose a zip first.'; return }
    zipGo.disabled = true
    status.textContent = 'Uploading and publishing…'
    try {
      const resp = await client.apiBytes('PUT', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/site?create=1', file, 'application/zip')
      if (!resp.ok) { status.textContent = errText(resp, 'Could not publish your site.'); zipGo.disabled = false; return }
      status.textContent = 'Published version ' + (resp.data && resp.data.active_version) + '.'
      await reload({})
    } catch (e) {
      if (e.message !== 'signed out') { status.textContent = 'Could not confirm the upload; check your site before trying again.'; zipGo.disabled = false }
    }
  })

  const keyGet = $('web-key-get')
  if (keyGet) keyGet.addEventListener('click', async () => {
    if (data.key) {
      const ok = await ui.confirm({ title: 'Replace key?', body: 'Your old key stops working. Replace it?', confirmLabel: 'Replace key' })
      if (!ok) return
    }
    keyGet.disabled = true
    try {
      const resp = await api('POST', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/key')
      if (!resp.ok) {
        if (isBlockedCode(resp.data && resp.data.code)) { ui.toast(errText(resp), main, true); await ctx.refresh(); return }
        ui.toast(errText(resp), keyGet, true)
        keyGet.disabled = false
        return
      }
      await reload({ revealedKey: resp.data.key })
    } catch (e) { if (e.message !== 'signed out') { ui.toast('Cannot reach the server.', keyGet, true); keyGet.disabled = false } }
  })
  const keyOff = $('web-key-off')
  if (keyOff) keyOff.addEventListener('click', async () => {
    const ok = await ui.confirm({ title: 'Turn off your key?', body: 'Nothing can publish with this key any more. Turn it off?', confirmLabel: 'Turn off key', danger: true })
    if (!ok) return
    keyOff.disabled = true
    try {
      const resp = await api('DELETE', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/key')
      if (!resp.ok) { ui.toast(errText(resp), keyOff, true); keyOff.disabled = false; return }
      ui.toast('Key turned off', main)
      await reload({ revealedKey: '' })
    } catch (e) { if (e.message !== 'signed out') { ui.toast('Cannot reach the server.', keyOff, true); keyOff.disabled = false } }
  })
  const keyDone = $('web-key-done')
  if (keyDone) keyDone.addEventListener('click', () => reload({ revealedKey: '' }))

  const download = $('web-download')
  if (download) download.addEventListener('click', () => downloadFile(ctx, '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/team/export.tar.gz', ev.slug + '-' + team.slug + '-site.tar.gz', download))

  if (state.notifySecret) {
    const box = $('notif-secret-box')
    if (box) box.querySelector('.note').append(ui.copyButton(state.notifySecret, 'Copy'))
  }

  wireNotifications(ctx, main, ev, team, reload)
}

function safeHref (href) {
  try {
    const u = new URL(href, location.origin)
    return (u.protocol === 'http:' || u.protocol === 'https:') ? u.href : ''
  } catch (e) { return '' }
}

// ---- Notifications (team-site-notifications) ----
// The team's own site is a regular site, named by the team slug; the
// participant manages its notifications through the generic site route,
// scoped to this event with ?event= (old UI: window.hackLoadNotifications).
function notifyBase (ev, team) {
  return '/v1/sites/' + encodeURIComponent(team.slug) + '/notifications'
}
function notifyQuery (ev) { return '?event=' + encodeURIComponent(ev.slug) }

function receiverText (n) {
  if (n.source === 'mcp') return n.app_name || 'Connected app'
  if (n.label) return n.label
  try { return new URL(n.url).host } catch (e) { return n.url }
}

async function notificationsHTML (ctx, ev, team, state) {
  const { api: client, ui } = ctx
  const esc = ui.esc
  let r
  try { r = await client.api('GET', notifyBase(ev, team) + notifyQuery(ev)) } catch (e) { r = { ok: false } }
  if (!r.ok) return ''
  const limits = r.data.limits || {}
  const items = r.data.notifications || []
  if (limits.enabled === false && !items.length) return ''
  let html = '<div class="card"><h2>Notifications</h2><p>Get a signed message when a visitor saves something or uploads a file on your team’s site.</p>'
  if (items.length) {
    html += '<div class="list">' + items.map(n => {
      const sub = (n.events || []).map(e => NOTIFY_EVENT_LABEL[e] || e).join(', ') + ' · ' + (NOTIFY_STATE_LABEL[n.state] || n.state)
      return '<div class="r"><div class="main"><b>' + esc(receiverText(n)) + '</b><span>' + esc(sub) + '</span></div><button type="button" class="btn danger sm" data-notif-off="' + esc(n.id) + '" data-who="' + esc(receiverText(n)) + '">Turn off</button></div>'
    }).join('') + '</div>'
  }
  if (state.notifySecret) {
    html += '<div id="notif-secret-box"><div class="note ok">Added. Signing secret (shown once): <span class="mono">' + esc(state.notifySecret) + '</span></div></div>'
  }
  if (limits.enabled === false) {
    html += '<p class="muted small">Notifications are unavailable on this server.</p>'
  } else if (limits.can_create === false) {
    html += '<p class="muted small">New subscriptions are closed. Existing subscriptions continue after the submission deadline.</p>'
  } else {
    html += '<div class="field"><label for="notif-url">Notification URL</label><input class="input" id="notif-url" type="url" placeholder="https://example.com/notify"></div>' +
      '<button type="button" class="btn" id="notif-add">Add notification</button>' +
      (state.notifySecret ? '' : '<div id="notif-secret-box"></div>')
  }
  html += '</div>'
  return html
}

// reload is paint()'s own reloader (re-fetches /key, re-paints with a patched
// state); reusing it keeps the key-reveal and notify-secret-reveal panels
// consistent instead of needing a second, divergent refresh path.
function wireNotifications (ctx, main, ev, team, reload) {
  const { api: client, ui } = ctx
  const api = client.api
  const base = notifyBase(ev, team)
  const query = notifyQuery(ev)
  const $ = id => main.querySelector('#' + id)

  main.querySelectorAll('[data-notif-off]').forEach(b => b.addEventListener('click', async () => {
    const who = b.getAttribute('data-who')
    const ok = await ui.confirm({ title: 'Turn off this notification?', body: 'Stop sending notifications to ' + who + '.', confirmLabel: 'Turn off', danger: true })
    if (!ok) return
    b.disabled = true
    try {
      const r = await api('DELETE', base + '/' + encodeURIComponent(b.getAttribute('data-notif-off')) + query)
      if (!r.ok) { ui.toast(errText(r), b, true); b.disabled = false; return }
      await reload({ notifySecret: '' })
    } catch (e) { if (e.message !== 'signed out') { ui.toast('Cannot reach the server.', b, true); b.disabled = false } }
  }))

  const add = $('notif-add')
  if (add) add.addEventListener('click', async () => {
    const urlInput = $('notif-url')
    const url = urlInput.value.trim()
    if (!/^https:\/\//i.test(url)) { ui.toast('Use an https URL.', urlInput, true); return }
    add.disabled = true
    try {
      const r = await api('POST', base + query, { url, events: NOTIFY_EVENTS })
      if (!r.ok) { ui.toast(errText(r), add, true); add.disabled = false; return }
      await reload({ notifySecret: r.data.secret || '' })
    } catch (e) { if (e.message !== 'signed out') { ui.toast('Cannot reach the server.', add, true); add.disabled = false } }
  })
}
