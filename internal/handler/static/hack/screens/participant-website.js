import { errText, isBlockedCode } from './_partB-util.js'
import { href } from '../nav.js'

export function title () { return 'Website' }

// Features on this screen: get-team-publishing-key, replace-team-publishing-key,
// turn-off-team-publishing-key, upload-team-site-zip, connect-ai-app-to-publish,
// view-team-site-status, team-site-notifications, download-own-team-archive.

const NOTIFY_EVENTS = ['record.added', 'record.changed', 'file.uploaded']
const NOTIFY_EVENT_LABEL = { 'record.added': 'new entries', 'record.changed': 'changed or removed entries', 'file.uploaded': 'uploaded files', 'notification.test': 'test notification' }
const NOTIFY_STATE_LABEL = { active: 'Active', paused: 'Paused', suspended: 'Suspended after failures', pending: 'Waiting for check', expired: 'Expired' }
const NOTIFY_DELIVERY_STATE_LABEL = { pending: 'Waiting for delivery', delivered: 'Delivered', failed: 'Failed', skipped: 'Skipped' }
const NOTIFY_RESULT_LABEL = { secret_unreadable: "Stored secret cannot be read with this server's key; rotate it or add the notification again.", ok: 'Delivered', connection_refused: 'Connection refused', timeout: 'Receiver took too long', tls_error: 'Secure connection failed', http_4xx: 'Receiver rejected the message', http_5xx: 'Receiver had a server error', redirect: 'Receiver redirected the request', refused_address: 'Receiver address is not allowed', dns_error: 'Receiver address could not be found', challenge_failed: 'Receiver check failed', site_unavailable: 'Site unavailable' }

export async function render (ctx) {
  const { main, api: client, ui, event: detail } = ctx
  const api = client.api
  const esc = ui.esc
  const ev = detail.event
  const team = detail.me.team

  main.innerHTML = '<div class="ph"><div class="t"><h1>Website</h1><p>' + esc(ev.title || '') + '</p></div></div><p class="note">Loading…</p>'

  if (!team) {
    main.innerHTML = '<div class="ph"><div class="t"><h1>Website</h1><p>' + esc(ev.title || '') + '</p></div></div><div class="locked"><h2>Join or start a team first</h2><p><a class="btn" href="' + esc(href('participant', ev.slug, 'team')) + '">Go to Team</a></p></div>'
    return
  }

  let r
  try { r = await api('GET', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/key') } catch (e) { if (client.isSignedOut(e)) return; r = { ok: false, data: {} } }
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
    const link = safeHref(site.url)
    const urlText = esc(String(site.url || '').replace(/^https?:\/\//, ''))
    siteCard += '<p>' + (link ? '<a href="' + esc(link) + '" target="_blank" rel="noopener">' + urlText + '</a>' : urlText) + ' <span class="pill ok">Live</span></p>'
    siteCard += '<p class="sub">' + (typeof site.live_version === 'number' ? 'Version ' + site.live_version + '.' : 'Published.') + ' Changes can take a few minutes to show.</p>'
    if (link) siteCard += '<a class="btn" target="_blank" rel="noopener" href="' + esc(link) + '">Open site</a>'
  } else if (frozen || ended) {
    siteCard += '<div class="note">Nothing was published by the deadline.</div>'
  } else {
    siteCard += '<div class="note">Nothing published yet. Connect your AI app below, or upload a zip.</div>'
  }
  siteCard += '</div>'

  // ---- Publish (hidden once closed) ----
  let closedNoteHTML = ''
  let publishHTML = ''
  if (closedForPublish) {
    // Cosmetic #7: the site card right below already says "Nothing was
    // published" (frozen/ended) or shows the "Taken down" state -- this note
    // only needs to explain why Publish disappeared, not repeat that.
    closedNoteHTML = '<div class="note info">Publishing is closed.' + (site.exists ? ' Your site stays live as submitted.' : '') + '</div>'
  } else {
    const mcp = data.mcp_url || (location.origin + '/mcp')
    publishHTML += '<div class="card"><h2>Publish from an AI app</h2><p>Add this address to your AI app. No key needed.</p><div class="copy"><input class="input mono" id="web-mcp" aria-label="Connector address" value="' + esc(mcp) + '" readonly></div></div>'
    publishHTML += '<div class="card"><h2>Upload a zip</h2><p class="sub">Static files only, index.html at the root. ' + esc(data.upload_limits || '') + '</p><div class="field"><label for="web-zip">Zip file</label><input class="input" id="web-zip" type="file" accept=".zip"></div><button type="button" class="btn primary" id="web-zip-go">Upload</button><p class="note" id="web-zip-status" hidden></p></div>'

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
    publishHTML += '<details class="card"><summary>Publish from the command line</summary>' + adv + '</details>'
  }

  // S12 (owner decision 2026-10-30): don't ask for notifications until the
  // screen has some signal the site might offer them -- site.exists is the
  // only one the /key response gives us; there is no "notifications
  // supported" flag to check first. The retest found the generic site
  // route can still 404 for a published team site for server-side reasons
  // this screen has no way to see in advance (acknowledged in the
  // decision: "if it cannot know, keep the quiet hide"). notificationsHTML
  // already treats !r.ok as "nothing to show" with no toast, so that 404
  // stays console-only and the card is simply absent -- this is the
  // intended behaviour, not a bug to chase further here.
  const notifyHTML = site.exists ? await notificationsHTML(ctx, ev, team, state) : ''

  const downloadHTML = '<div class="card"><h2>Keep a copy</h2><p class="sub">Download your published files.</p><button type="button" class="btn" id="web-download"' + (site.exists ? '' : ' disabled') + '>Download site</button></div>'

  main.innerHTML = head + closedNoteHTML + siteCard + publishHTML + notifyHTML + downloadHTML

  const $ = id => main.querySelector('#' + id)
  if (state.revealedKey && $('web-curl')) {
    $('web-curl').textContent = 'curl -X PUT "' + ((data.api_base || location.origin) + '/v1/hack/events/' + ev.slug + '/site?create=1') + '" -H "X-API-Key: ' + state.revealedKey + '" -H "Content-Type: application/zip" --data-binary @site.zip'
  }

  async function reload (next) {
    let r
    try { r = await api('GET', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/key') } catch (e) { if (client.isSignedOut(e)) return; return }
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
      if (!resp.ok) { status.textContent = archiveErrorText(resp, 'Could not publish your site.'); zipGo.disabled = false; return }
      status.textContent = 'Published version ' + (resp.data && resp.data.active_version) + '.'
      await reload({})
    } catch (e) {
      if (!client.isSignedOut(e)) { status.textContent = 'Could not confirm the upload. Check your site before trying again.'; zipGo.disabled = false }
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
    } catch (e) { if (!client.isSignedOut(e)) { ui.toast('Cannot reach the server.', keyGet, true); keyGet.disabled = false } }
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
    } catch (e) { if (!client.isSignedOut(e)) { ui.toast('Cannot reach the server.', keyOff, true); keyOff.disabled = false } }
  })
  const keyDone = $('web-key-done')
  if (keyDone) keyDone.addEventListener('click', () => reload({ revealedKey: '' }))

  const download = $('web-download')
  if (download) download.addEventListener('click', () => client.downloadFile('/v1/hack/events/' + encodeURIComponent(ev.slug) + '/team/export.tar.gz', ev.slug + '-' + team.slug + '-site.tar.gz', download))

  if (state.notifySecret) {
    const box = $('notif-secret-box')
    if (box) box.querySelector('.note').append(ui.copyButton(state.notifySecret, 'Copy'))
  }

  wireNotifications(ctx, main, ev, team, reload)
}

// C10/#16: the server's invalid_archive message is the raw Go error from
// the extractor ("invalid site archive: create gzip reader: gzip: invalid
// header"), there to help us debug, not a participant. blocked_file_type
// already reads as plain English (site.go). missing_index's raw text ("no
// index.html at the root of the archive", lowercase sentence fragment) is
// also a server-debug string, not participant copy -- S11 retest found it
// leaking straight through errText's server-message-first fallback, so it
// gets the same client-side override as invalid_archive, worded to name the
// actual fix (a zip with the site's own folder as its root, not index.html
// at the top). The extractor's text (internal/tarball/extract.go,
// sanitize.go) also distinguishes a corrupt/unreadable archive from one
// that was readable but refused for containing unsafe or duplicate paths
// (zip-slip, symlink-style escapes), and those are different things for a
// participant to fix, so they get different plain text rather than being
// flattened into one generic sentence.
export function archiveErrorText (resp, fallback) {
  const code = resp && resp.data && resp.data.code
  if (code === 'missing_index') {
    return 'Put index.html at the top of the zip, not inside a folder.'
  }
  if (code === 'invalid_archive') {
    const raw = String((resp && resp.data && resp.data.error) || '')
    if (/unsafe path|duplicate (entry|path)/i.test(raw)) {
      return "That zip has file paths Simple Host can't accept (a path outside the archive, or two files at the same path). Fix the archive and try again."
    }
    return "That file isn't a valid .zip."
  }
  return errText(resp, fallback)
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

// A notification row: receiver, what it's subscribed to and its state, plus
// the management actions classic offers (Deliveries always; Send test,
// Pause/Resume and Rotate secret when the caller can manage this
// subscription and it's one they created -- MCP-sourced subscriptions have
// no secret to rotate and no manual test, same restriction as classic).
function notifRowHTML (ui, n, canManage) {
  const esc = ui.esc
  const sub = (n.events || []).map(e => NOTIFY_EVENT_LABEL[e] || e).join(', ') + ' · ' + (n.storage ? esc(n.storage) : 'all saved data') + ' · ' + esc(NOTIFY_STATE_LABEL[n.state] || n.state)
  const last = n.last_attempt_at ? esc(NOTIFY_RESULT_LABEL[n.last_result] || 'No result yet') + ' · ' + esc(ui.dateTime(n.last_attempt_at)) : 'No deliveries yet'
  const actions = ['<button type="button" class="btn sm" data-notif-deliveries="' + esc(n.id) + '">Deliveries</button>']
  if (canManage && n.source === 'owner' && n.state !== 'expired') actions.push('<button type="button" class="btn sm" data-notif-test="' + esc(n.id) + '">Send test</button>')
  if (canManage) actions.push('<button type="button" class="btn sm" data-notif-pause="' + esc(n.id) + '">' + (n.state === 'paused' || n.state === 'suspended' ? 'Resume' : 'Pause') + '</button>')
  if (canManage && n.source === 'owner') actions.push('<button type="button" class="btn sm" data-notif-rotate="' + esc(n.id) + '">Rotate secret</button>')
  actions.push('<button type="button" class="btn danger sm" data-notif-off="' + esc(n.id) + '" data-who="' + esc(receiverText(n)) + '">Remove</button>')
  return '<div class="r"><div class="main"><b>' + esc(receiverText(n)) + '</b><span>' + sub + ' · ' + last + '</span></div><div class="row-flex">' + actions.join('') + '</div></div>' +
    '<div class="list" id="notif-deliv-' + esc(n.id) + '" hidden></div>'
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
  const canManage = limits.can_manage !== false
  let html = '<div class="card"><h2>Notifications</h2><p>Get a signed message when a visitor saves something or uploads a file on your team’s site.</p>'
  if (items.length) {
    html += '<div class="list">' + items.map(n => notifRowHTML(ui, n, canManage)).join('') + '</div>'
  }
  if (state.notifySecret) {
    html += '<div id="notif-secret-box"><div class="note ok">' + (state.notifySecretAction === 'rotated' ? 'Rotated.' : 'Added.') + ' Signing secret (shown once): <span class="mono">' + esc(state.notifySecret) + '</span>' +
      (state.notifySecretPrevious ? ' The previous secret keeps working until ' + esc(ui.dateTime(state.notifySecretPrevious)) + '.' : '') + '</div></div>'
  }
  if (limits.enabled === false) {
    html += '<p class="muted small">Notifications are unavailable on this server.</p>'
  } else if (limits.can_create === false) {
    html += '<p class="muted small">New subscriptions are closed. Existing subscriptions continue after the submission deadline.</p>'
  } else {
    html += '<div class="field"><label for="notif-url">Notification URL</label><input class="input" id="notif-url" type="url" placeholder="https://example.com/notify"></div>' +
      '<div class="field"><label for="notif-storage">Storage (optional)</label><input class="input" id="notif-storage" placeholder="all"></div>' +
      '<div class="field"><label for="notif-label">Label (optional)</label><input class="input" id="notif-label"></div>' +
      '<details><summary>Advanced: authentication header</summary>' +
      '<div class="field"><label for="notif-header-name">Header name (optional)</label><input class="input" id="notif-header-name" maxlength="64"></div>' +
      '<div class="field"><label for="notif-header-value">Header value</label><input class="input" type="password" id="notif-header-value" maxlength="1024"></div>' +
      '</details>' +
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
  const esc = ui.esc
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
    } catch (e) { if (!client.isSignedOut(e)) { ui.toast('Cannot reach the server.', b, true); b.disabled = false } }
  }))

  main.querySelectorAll('[data-notif-pause]').forEach(b => b.addEventListener('click', async () => {
    const id = b.getAttribute('data-notif-pause')
    const paused = b.textContent.trim() !== 'Resume'
    b.disabled = true
    try {
      const r = await api('PATCH', base + '/' + encodeURIComponent(id) + query, { paused })
      if (!r.ok) { ui.toast(errText(r), b, true); b.disabled = false; return }
      await reload({ notifySecret: '' })
    } catch (e) { if (!client.isSignedOut(e)) { ui.toast('Cannot reach the server.', b, true); b.disabled = false } }
  }))

  main.querySelectorAll('[data-notif-test]').forEach(b => b.addEventListener('click', async () => {
    const id = b.getAttribute('data-notif-test')
    b.disabled = true
    try {
      const r = await api('POST', base + '/' + encodeURIComponent(id) + '/test' + query)
      if (!r.ok) { ui.toast(errText(r), b, true); return }
      const d = r.data || {}
      const delivered = !!d.delivered
      const text = (delivered ? 'Test delivered' : (NOTIFY_RESULT_LABEL[d.result] || 'No result yet').replace(/\.$/, '')) + (d.status ? ' (HTTP ' + d.status + ')' : '') + '.'
      ui.toast(text, b, !delivered)
    } catch (e) { if (!client.isSignedOut(e)) ui.toast('Cannot reach the server.', b, true)
    } finally { b.disabled = false }
  }))

  main.querySelectorAll('[data-notif-rotate]').forEach(b => b.addEventListener('click', async () => {
    const id = b.getAttribute('data-notif-rotate')
    const ok = await ui.confirm({ title: 'Rotate the signing secret?', body: 'A new secret replaces it. The old one keeps working for a short overlap, then stops.', confirmLabel: 'Rotate secret' })
    if (!ok) return
    b.disabled = true
    try {
      const r = await api('POST', base + '/' + encodeURIComponent(id) + '/rotate-secret' + query)
      if (!r.ok) { ui.toast(errText(r), b, true); b.disabled = false; return }
      await reload({ notifySecret: r.data.secret || '', notifySecretAction: 'rotated', notifySecretPrevious: r.data.previous_valid_until || '' })
    } catch (e) { if (!client.isSignedOut(e)) { ui.toast('Cannot reach the server.', b, true); b.disabled = false } }
  }))

  main.querySelectorAll('[data-notif-deliveries]').forEach(b => b.addEventListener('click', async () => {
    const id = b.getAttribute('data-notif-deliveries')
    const panel = $('notif-deliv-' + id)
    if (!panel) return
    if (!panel.hidden) { panel.hidden = true; b.textContent = 'Deliveries'; return }
    panel.hidden = false
    b.textContent = 'Hide deliveries'
    panel.innerHTML = '<div class="empty">Loading…</div>'
    try {
      const r = await api('GET', base + '/' + encodeURIComponent(id) + '/deliveries' + query)
      if (!r.ok) { panel.innerHTML = '<div class="empty">' + esc(errText(r)) + '</div>'; return }
      const deliveries = (r.data && r.data.deliveries) || []
      panel.innerHTML = deliveries.length
        ? deliveries.map(v => {
            const when = esc(ui.dateTime(v.created_at))
            const state = esc(NOTIFY_DELIVERY_STATE_LABEL[v.state] || v.state) + (v.result ? ' · ' + esc(NOTIFY_RESULT_LABEL[v.result] || v.result) : '') + (v.last_http ? ' (HTTP ' + v.last_http + ')' : '')
            const attempts = (v.attempts || 0) + ' attempt' + (v.attempts === 1 ? '' : 's')
            return '<div class="r"><div class="main"><b>' + esc(NOTIFY_EVENT_LABEL[v.name] || v.name) + '</b><span>' + when + ' · ' + state + ' · ' + attempts + '</span></div></div>'
          }).join('')
        : '<div class="empty">No deliveries in this period.</div>'
    } catch (e) {
      if (!client.isSignedOut(e)) panel.innerHTML = '<div class="empty">Cannot reach the server.</div>'
    }
  }))

  const add = $('notif-add')
  if (add) add.addEventListener('click', async () => {
    const urlInput = $('notif-url')
    const url = urlInput.value.trim()
    if (!/^https:\/\//i.test(url)) { ui.toast('Use an https URL.', urlInput, true); return }
    const body = { url, events: NOTIFY_EVENTS, storage: $('notif-storage').value.trim(), label: $('notif-label').value.trim() }
    const headerName = $('notif-header-name').value.trim()
    const headerValue = $('notif-header-value').value
    if (headerName || headerValue) body.auth_header = { name: headerName, value: headerValue }
    add.disabled = true
    try {
      const r = await api('POST', base + query, body)
      if (!r.ok) { ui.toast(errText(r), add, true); add.disabled = false; return }
      await reload({ notifySecret: r.data.secret || '', notifySecretAction: 'added', notifySecretPrevious: '' })
    } catch (e) { if (!client.isSignedOut(e)) { ui.toast('Cannot reach the server.', add, true); add.disabled = false } }
  })
}
