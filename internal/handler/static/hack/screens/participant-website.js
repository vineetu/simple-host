import { errText, isBlockedCode } from './_partB-util.js'
import { href } from '../nav.js'

export function title () { return 'Website' }

// Features on this screen: get-team-publishing-key, replace-team-publishing-key,
// turn-off-team-publishing-key, upload-team-site-zip, connect-ai-app-to-publish,
// view-team-site-status, team-site-notifications, download-own-team-archive,
// guided-website-publishing (state strip, app steps from one AI app, polling).

const NOTIFY_EVENTS = ['record.added', 'record.changed', 'file.uploaded']
const NOTIFY_EVENT_LABEL = { 'record.added': 'new entries', 'record.changed': 'changed or removed entries', 'file.uploaded': 'uploaded files', 'notification.test': 'test notification' }
const NOTIFY_STATE_LABEL = { active: 'Active', paused: 'Paused', suspended: 'Suspended after failures', pending: 'Waiting for check', expired: 'Expired' }
const NOTIFY_DELIVERY_STATE_LABEL = { pending: 'Waiting for delivery', delivered: 'Delivered', failed: 'Failed', skipped: 'Skipped' }
const NOTIFY_RESULT_LABEL = { secret_unreadable: "Stored secret cannot be read with this server's key; rotate it or add the notification again.", ok: 'Delivered', connection_refused: 'Connection refused', timeout: 'Receiver took too long', tls_error: 'Secure connection failed', http_4xx: 'Receiver rejected the message', http_5xx: 'Receiver had a server error', redirect: 'Receiver redirected the request', refused_address: 'Receiver address is not allowed', dns_error: 'Receiver address could not be found', challenge_failed: 'Receiver check failed', site_unavailable: 'Site unavailable' }

// The default app, when nobody has connected yet.
const DEFAULT_APP = 'claude'
// Poll .../site every 5s for up to 10 minutes while unpublished and visible.
const POLL_MS = 5000
const POLL_LIMIT_MS = 10 * 60 * 1000

// hack-connect-steps.json is static and shared with /get-started; fetched
// once per page load and cached at module scope.
let connectStepsPromise = null
function loadConnectSteps () {
  if (!connectStepsPromise) {
    connectStepsPromise = fetch('/hack-connect-steps.json').then(r => r.ok ? r.json() : null).catch(() => null)
  }
  return connectStepsPromise
}

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

  const steps = await loadConnectSteps()
  let entry = null
  try {
    const er = await api('GET', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/entry')
    if (er.ok) entry = er.data.entry
  } catch (e) { if (client.isSignedOut(e)) return }

  const state = { tab: '', revealedKey: '', prevSnapshot: null, pollStartedAt: 0, pollTimer: 0, pollExpired: false }

  async function load () {
    let keyR, siteR
    try {
      [keyR, siteR] = await Promise.all([
        api('GET', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/key'),
        api('GET', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/site')
      ])
    } catch (e) {
      if (client.isSignedOut(e)) return null
      return null
    }
    if (!keyR.ok || !siteR.ok) return null
    return { keyData: keyR.data, siteData: siteR.data }
  }

  const first = await load()
  if (!first) {
    main.innerHTML = '<div class="ph"><div class="t"><h1>Website</h1><p>' + esc(ev.title || '') + '</p></div></div><div class="note bad" role="alert">Could not load your team’s site.</div>'
    return
  }
  await paint(ctx, ev, team, first.keyData, first.siteData, entry, steps, state, load)
}

function hostOnly (url) { return String(url || '').replace(/^https?:\/\//, '') }

function agoText (iso) {
  if (!iso) return ''
  const diff = Date.now() - new Date(iso).getTime()
  if (!Number.isFinite(diff)) return ''
  if (diff < 60000) return 'just now'
  const m = Math.floor(diff / 60000)
  if (m < 60) return m + ' minute' + (m === 1 ? '' : 's') + ' ago'
  const h = Math.floor(m / 60)
  if (h < 24) return h + ' hour' + (h === 1 ? '' : 's') + ' ago'
  const d = Math.floor(h / 24)
  return d + ' day' + (d === 1 ? '' : 's') + ' ago'
}

function teammateText (esc, names) {
  const n = names.map(x => esc(x))
  if (n.length === 1) return n[0] + ' is connected too'
  if (n.length === 2) return n[0] + ' and ' + n[1] + ' are connected too'
  return n.slice(0, -1).join(', ') + ' and ' + n[n.length - 1] + ' are connected too'
}

// sentenceEnd adds a closing period only when the text doesn't already end
// with its own punctuation.
function sentenceEnd (text) { return /[.!?]$/.test(text) ? text : text + '.' }

// buildPrompt builds the ready-to-paste text (atlas participant-website
// "Prompt" section) from GET /entry and the event/team names.
function buildPrompt (ev, team, entry) {
  const lines = ['Build a one-page website for our hackathon project and publish it as our team site for ' +
    (ev.title || 'our hackathon') + ' (team ' + (team.name || '') + ') on Simple Hack.']
  const problem = entry && String(entry.problem || '').trim()
  const solution = entry && String(entry.tagline || '').trim()
  if (problem) lines.push('Problem: ' + sentenceEnd(problem))
  if (solution) lines.push('Solution: ' + sentenceEnd(solution))
  return lines.join('\n')
}

function connStripHTML (ui, esc, site, conn, closedForPublish) {
  let appCls = '', appLabel, appSub
  if (conn.state === 'connected') {
    appCls = 'on'
    appLabel = 'Connected in ' + esc(conn.app || 'your AI app')
    const mates = conn.teammates_connected || []
    appSub = 'Last used ' + agoText(conn.last_used_at) + (mates.length ? ' · ' + teammateText(esc, mates) : '')
  } else if (conn.state === 'other_team') {
    appCls = 'wait'
    appLabel = 'Connected in ' + esc(conn.app || 'your AI app') + ', set to another team'
    appSub = conn.other_team ? ('Currently "' + esc(conn.other_team.team) + '" in ' + esc(conn.other_team.event) + '.') : 'Set to another team.'
  } else if (conn.state === 'key') {
    appCls = 'on'
    appLabel = 'Publishing with a key'
    appSub = conn.last_used_at ? ('Last used ' + agoText(conn.last_used_at) + '.') : 'Your key is in use.'
  } else {
    appLabel = 'Not connected yet'
    appSub = 'Follow the steps below once. It takes about two minutes.'
  }

  let webCls = '', webLabel, webSub
  if (!site.ready) {
    webLabel = 'Setting up'
    webSub = 'Your address is being set up. It is usually ready within a few minutes.'
  } else if (site.taken_down) {
    webLabel = 'Taken down'
    webSub = esc(String(site.taken_down_reason || '').trim() || 'Taken down by Simple Host.')
  } else if (site.exists) {
    webCls = 'on'
    webLabel = 'Published' + (typeof site.live_version === 'number' ? ' · version ' + site.live_version : '')
    const link = safeHref(site.url)
    webSub = agoText(site.published_at) + (link ? ' · <a href="' + esc(link) + '" target="_blank" rel="noopener">Open ' + esc(hostOnly(site.url)) + ' ↗</a>' : '')
  } else if (closedForPublish) {
    if (site.pinned_version) {
      webLabel = 'Submitted version ' + site.pinned_version
      webSub = site.pinned_at ? ('Pinned ' + ui.dateTime(site.pinned_at)) : ''
    } else {
      webLabel = 'Not published'
      webSub = 'Nothing was published by the deadline.'
    }
  } else {
    webCls = 'wait'
    webLabel = 'Not published yet'
    webSub = 'Your address is ready: ' + esc(hostOnly(site.url))
  }

  return '<div class="conn">' +
    '<div class="' + appCls + '"><span class="k">AI app</span><b>' + appLabel + '</b><span class="s">' + appSub + '</span></div>' +
    '<div class="' + webCls + '"><span class="k">Website</span><b>' + webLabel + '</b><span class="s">' + webSub + '</span></div>' +
    '</div>'
}

// publishCardHTML is the "Publish from your AI app" card (unpublished) or
// its folded "Publish again…" form (published). apps is the parsed
// hack-connect-steps.json (null if it failed to load -- the card still
// shows a minimal fallback rather than breaking the page).
function publishCardHTML (ui, esc, doc, state, conn, email, promptText, published) {
  const all = (doc && doc.apps) || []
  const list = all.filter(a => a.tab)
  const otherApps = all.filter(a => !a.tab)
  const tabIDs = list.map(a => a.id).concat(['other'])
  let tab = state.tab || (conn.app ? conn.app.toLowerCase() : '') || DEFAULT_APP
  if (!tabIDs.includes(tab)) tab = DEFAULT_APP

  const tabsHTML = '<nav class="app-tabs" aria-label="How you publish">' +
    list.map(a => '<a href="#" data-app-tab="' + esc(a.id) + '" aria-current="' + (a.id === tab ? 'true' : 'false') + '">' + esc(a.label) + '</a>').join('') +
    '<a href="#" data-app-tab="other" aria-current="' + (tab === 'other' ? 'true' : 'false') + '">Other AI apps</a>' +
    '</nav>'

  if (tab === 'other') {
    const links = otherApps.map(a => '<a class="btn sm" href="/get-started#' + esc(a.id) + '" target="_blank" rel="noopener">' + esc(a.label) + '</a>').join(' ')
    const body = '<div class="form-actions">' + links + '</div><p class="help">Add Simple Hack to that app, then come back here and describe your idea in a new chat.</p>'
    if (published) return '<details class="card"><summary>Publish again, or from another AI app</summary>' + tabsHTML + body + '</details>'
    return '<section class="card"><h2>Publish from your AI app</h2><p class="sub">Describe your idea in the AI app you already use. It builds the page and publishes it here. No key, no code.</p>' + tabsHTML + body + '</section>'
  }

  const app = list.find(a => a.id === tab)
  if (!app) {
    const fallback = '<p class="help">Steps for this app are not available right now. <a href="/get-started">See the full setup guide</a>.</p>'
    if (published) return '<details class="card"><summary>Publish again, or from another AI app</summary>' + tabsHTML + fallback + '</details>'
    return '<section class="card"><h2>Publish from your AI app</h2>' + tabsHTML + fallback + '</section>'
  }

  // Ticked when this tab's app is the one actually connected (connected or
  // other_team: either way the connector itself is added and signed in).
  const appConnected = (conn.state === 'connected' || conn.state === 'other_team') && (conn.app || '').toLowerCase() === app.label.toLowerCase()

  // Shown whenever this tab's app is connected but pointed at a different
  // team -- including once published, since "Publish again" would otherwise
  // silently target the wrong team until the person switches.
  const switchNote = (conn.state === 'other_team' && appConnected)
    ? '<div class="note info">Paste this to switch: <span class="mono">Use my team "' + esc(state.teamName || '') + '" in ' + esc((conn.other_team && conn.other_team.event) || '') + ' on Simple Hack.</span></div>'
    : ''

  if (published) {
    return '<details class="card"><summary>Publish again, or from another AI app</summary>' + tabsHTML + switchNote +
      '<p class="help">Ask ' + esc(app.label) + ' to change the site and publish again. Each publish makes a new version; the newest is live.</p></details>'
  }

  // The connect steps are the same ones /get-started shows (one source:
  // hack-connect-steps.json, trusted markup). One address row per screen.
  let addressUsed = false
  const addressRow = () => {
    if (addressUsed) return ''
    addressUsed = true
    return '<div class="copy" data-mcp-row><input class="input mono" id="web-app-mcp" aria-label="Connector address" value="" readonly></div>'
  }
  const fill = t => String(t || '').split('{app}').join(esc(app.label)).split('{email}').join('<b>' + esc(email || 'your email') + '</b>')
  const connect = app.connect || []
  let lis = ''
  connect.forEach((step, n) => {
    let extra = step.address ? addressRow() : ''
    if (step.copy) extra += '<div class="copy"><input class="input mono" aria-label="' + esc(step.copy.label) + '" value="' + esc(step.copy.value) + '" readonly></div>'
    if (n === connect.length - 1) {
      if (app.fallback) extra += '<p class="help">' + app.fallback + '</p>' + addressRow()
      if (doc.signin_hint) extra += '<p class="help">' + fill(doc.signin_hint) + '</p>'
    }
    lis += stepLI(step.html + (step.after || ''), appConnected, extra)
  })
  ;(doc.publish || []).forEach(step => {
    const extra = step.prompt ? '<div class="prompt" id="web-prompt-box">' + esc(promptText) + '</div><div class="form-actions" data-prompt-copy></div>' : ''
    lis += stepLI(fill(step.html), false, extra)
  })

  const lede = conn.state === 'connected'
    ? "You're connected. Paste the prompt into " + esc(app.label) + ' and it publishes here.'
    : 'Describe your idea in ' + esc(app.label) + '. It builds the page and publishes it here. No key, no code.'

  return '<section class="card"><h2>Publish from your AI app</h2><p class="sub">' + lede + '</p>' + tabsHTML + switchNote +
    '<ol class="howto">' + lis + '</ol>' +
    '</section>'

  // stepLI builds one valid <li>, with extra block-level content (an
  // address row, a prompt box) nested inside it.
  function stepLI (text, done, extra) {
    return '<li class="' + (done ? 'done' : '') + '">' + text + extra + '</li>'
  }
}

async function paint (ctx, ev, team, keyData, siteData, entry, steps, state, load) {
  const { main, api: client, ui } = ctx
  const api = client.api
  const esc = ui.esc
  const site = siteData.site || {}
  const conn = siteData.connection || { state: 'none', teammates_connected: [] }
  state.teamName = team.name

  const frozen = typeof team.frozen === 'boolean' ? team.frozen : ev.stage === 'closed'
  const ended = ev.stage === 'archived'
  const closedForPublish = !!(site.taken_down || frozen || ended)

  // Toast on a state change this page observed itself (poll or a reload
  // after an action) -- never on the very first paint.
  if (state.prevSnapshot) {
    if (state.prevSnapshot.connState !== 'connected' && conn.state === 'connected') {
      ui.toast('Connected in ' + (conn.app || 'your AI app') + '.', main)
    }
    if (!state.prevSnapshot.published && site.exists) {
      ui.toast('Published' + (typeof site.live_version === 'number' ? ' · version ' + site.live_version : '') + '.', main)
    }
  }
  state.prevSnapshot = { connState: conn.state, published: !!site.exists }

  const head = '<div class="ph"><div class="t"><h1>Website</h1><p>' + esc(ev.title || '') + '</p></div></div>'
  const strip = connStripHTML(ui, esc, site, conn, closedForPublish)

  // ---- Your team site (once published) ----
  let siteCard = ''
  if (site.exists) {
    siteCard = '<section class="card"><h2>Your team site</h2><div class="site-row">'
    const link = safeHref(site.url)
    siteCard += '<div><p>' + (site.taken_down ? '<span class="pill bad">Taken down</span> ' : '<span class="pill ok">Live</span> ') +
      (link ? '<a href="' + esc(link) + '" target="_blank" rel="noopener">' + esc(hostOnly(site.url)) + '</a>' : esc(hostOnly(site.url))) + '</p>'
    const bits = []
    if (typeof site.live_version === 'number') bits.push('Version ' + site.live_version)
    if (site.published_via) bits.push('published via ' + site.published_via)
    const when = agoText(site.published_at)
    let sub = bits.join(', ')
    if (when) sub += (sub ? ', ' : '') + when
    sub += (sub ? '. ' : '') + 'Judges see the version that is live at your deadline.'
    siteCard += '<p class="sub">' + esc(sub) + '</p>'
    if (link) siteCard += '<div class="form-actions"><a class="btn" target="_blank" rel="noopener" href="' + esc(link) + '">Open website ↗</a></div>'
    siteCard += '</div><div class="site-frame"><span class="small">' + esc(team.name || '') + '</span></div></div></section>'
  } else if (!site.ready) {
    siteCard = '<div class="note">Your team’s address is being set up. It is usually ready within a few minutes.</div>'
  } else if (closedForPublish) {
    siteCard = '<div class="note">Nothing was published by the deadline.</div>'
  }

  // ---- Publish (hidden once closed for publish) ----
  let closedNoteHTML = ''
  let publishHTML = ''
  const email = (ctx.me && (ctx.me.email || ctx.me.username)) || ''
  const promptText = buildPrompt(ev, team, entry)
  if (closedForPublish) {
    closedNoteHTML = '<div class="note info">Publishing is closed.' + (site.exists ? ' Your site stays live as submitted.' : '') + '</div>'
  } else {
    publishHTML += publishCardHTML(ui, esc, steps, state, conn, email, promptText, site.exists)
    publishHTML += '<section class="card"><h2>Or upload a zip</h2><p class="sub">Static files only, index.html at the root. ' + esc(keyData.upload_limits || '') + '</p><div class="control-row"><input class="input" id="web-zip" type="file" accept=".zip" aria-label="Your site archive"><button type="button" class="btn" id="web-zip-go">Upload</button></div><p class="note" id="web-zip-status" hidden></p></section>'

    const key = keyData.key
    let adv = '<p>Publish from your command line.</p>'
    if (state.revealedKey) {
      adv += '<div class="field"><label for="web-key-val">Your key — shown once, copy it now</label><div class="copy"><input class="input mono" id="web-key-val" readonly value="' + esc(state.revealedKey) + '"></div></div>' +
        '<p class="mono small" id="web-curl"></p>' +
        '<div class="form-actions"><button type="button" class="btn" id="web-key-done">Done</button></div>'
    } else {
      adv += '<p class="mono small">curl -X PUT "' + esc((keyData.api_base || location.origin) + '/v1/hack/events/' + ev.slug + '/site?create=1') + '" -H "X-API-Key: YOUR_KEY" -H "Content-Type: application/zip" --data-binary @site.zip</p>'
      adv += key ? '<p class="help">You have a key ending ' + esc(key.last4 || '') + '.</p>' : '<p class="help">No key yet.</p>'
      adv += '<div class="form-actions"><button type="button" class="btn" id="web-key-get">' + (key ? 'Replace key' : 'Get my key') + '</button></div>'
      if (key) adv += '<div class="danger-zone"><h2>Turn off key</h2><div class="setting"><div class="t"><span>Nothing can publish with this key any more.</span></div><button type="button" class="btn danger" id="web-key-off">Turn off key</button></div></div>'
    }
    publishHTML += '<details class="card"><summary>Advanced: command line and API key</summary>' + adv + '</details>'
  }

  const notifyHTML = site.exists ? await notificationsHTML(ctx, ev, team, state) : ''
  const downloadHTML = site.exists ? '<div class="card"><h2>Keep a copy</h2><p class="sub">Download your published files.</p><button type="button" class="btn" id="web-download">Download our team site</button></div>' : ''

  main.innerHTML = head + strip + siteCard + closedNoteHTML + publishHTML + notifyHTML + downloadHTML

  const $ = id => main.querySelector('#' + id)
  if (state.revealedKey && $('web-curl')) {
    $('web-curl').textContent = 'curl -X PUT "' + ((keyData.api_base || location.origin) + '/v1/hack/events/' + ev.slug + '/site?create=1') + '" -H "X-API-Key: ' + state.revealedKey + '" -H "Content-Type: application/zip" --data-binary @site.zip'
  }

  async function reload (next) {
    Object.assign(state, next)
    const got = await load()
    if (!got) return
    await paint(ctx, ev, team, got.keyData, got.siteData, entry, steps, state, load)
  }

  // ---- app tabs ----
  main.querySelectorAll('[data-app-tab]').forEach(a => a.addEventListener('click', e => {
    e.preventDefault()
    reload({ tab: a.getAttribute('data-app-tab') })
  }))

  // ---- connector address (filled client-side so it always matches this origin) ----
  const mcpRow = main.querySelector('[data-mcp-row]')
  if (mcpRow) {
    const input = mcpRow.querySelector('#web-app-mcp')
    const mcp = keyData.mcp_url || (location.origin + '/mcp')
    input.value = mcp
    mcpRow.append(ui.copyButton(mcp, 'Copy'))
  }
  const promptCopy = main.querySelector('[data-prompt-copy]')
  if (promptCopy) promptCopy.append(ui.copyButton(promptText, 'Copy prompt'))

  // ---- zip upload ----
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
    if (keyData.key) {
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

  // ---- polling: while unpublished and not closed, check again every 5s for
  // up to 10 minutes, only while the tab is visible (build plan §6/§8). ----
  const shouldPoll = !site.exists && !closedForPublish
  if (!shouldPoll) {
    if (state.pollTimer) { clearInterval(state.pollTimer); state.pollTimer = 0 }
  } else if (!state.pollTimer && !state.pollExpired) {
    state.pollStartedAt = state.pollStartedAt || Date.now()
    state.pollTimer = setInterval(() => {
      if (!ctx.alive || (ctx.alive && !ctx.alive()) || !main.isConnected) {
        clearInterval(state.pollTimer); state.pollTimer = 0
        return
      }
      if (Date.now() - state.pollStartedAt > POLL_LIMIT_MS) {
        clearInterval(state.pollTimer); state.pollTimer = 0; state.pollExpired = true
        reload({})
        return
      }
      if (document.visibilityState !== 'visible') return
      reload({})
    }, POLL_MS)
  }
  if (shouldPoll && state.pollExpired) {
    const checkAgain = document.createElement('button')
    checkAgain.type = 'button'
    checkAgain.className = 'btn sm'
    checkAgain.textContent = 'Check again'
    checkAgain.addEventListener('click', () => { state.pollExpired = false; reload({}) })
    const target = main.querySelector('.conn > div:last-child')
    if (target) target.append(document.createElement('br'), checkAgain)
  }
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
// the management actions available (Deliveries always; Send test,
// Pause/Resume and Rotate secret when the caller can manage this
// subscription and it's one they created -- MCP-sourced subscriptions have
// no secret to rotate and no manual test).
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
  let html = '<details class="card"><summary>Notifications</summary><p>Get a signed message when a visitor saves something or uploads a file on your team’s site.</p>'
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
      '<div class="form-actions"><button type="button" class="btn" id="notif-add">Add notification</button></div>' +
      (state.notifySecret ? '' : '<div id="notif-secret-box"></div>')
  }
  html += '</details>'
  return html
}

// reload is paint()'s own reloader (re-fetches /key and /site, re-paints
// with a patched state); reusing it keeps the key-reveal and
// notify-secret-reveal panels consistent instead of needing a second,
// divergent refresh path.
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
      if (!r.ok) { panel.innerHTML = '<div class="empty">' + ui.esc(errText(r)) + '</div>'; return }
      const deliveries = (r.data && r.data.deliveries) || []
      panel.innerHTML = deliveries.length
        ? deliveries.map(v => {
            const when = ui.esc(ui.dateTime(v.created_at))
            const state = ui.esc(NOTIFY_DELIVERY_STATE_LABEL[v.state] || v.state) + (v.result ? ' · ' + ui.esc(NOTIFY_RESULT_LABEL[v.result] || v.result) : '') + (v.last_http ? ' (HTTP ' + v.last_http + ')' : '')
            const attempts = (v.attempts || 0) + ' attempt' + (v.attempts === 1 ? '' : 's')
            return '<div class="r"><div class="main"><b>' + ui.esc(NOTIFY_EVENT_LABEL[v.name] || v.name) + '</b><span>' + when + ' · ' + state + ' · ' + attempts + '</span></div></div>'
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
