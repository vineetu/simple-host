import { adminLock } from './_orgA-util.js'
import { href } from '../nav.js'

export function title(ctx) { return 'Settings' }

const NOTIFY_EVENTS = [['record.added', 'A visitor saves something'], ['record.changed', 'A visitor changes something they saved'], ['file.uploaded', 'A visitor uploads a file']]
const NOTIFY_STATES = { active: 'Active', paused: 'Paused', suspended: 'Suspended after failures', pending: 'Waiting for check', expired: 'Expired' }
const NOTIFY_RESULTS = { secret_unreadable: "Stored secret cannot be read with this server's key; rotate it or add the notification again.", ok: 'Delivered', connection_refused: 'Connection refused', timeout: 'Receiver took too long', tls_error: 'Secure connection failed', http_4xx: 'Receiver rejected the message', http_5xx: 'Receiver had a server error', redirect: 'Receiver redirected the request', refused_address: 'Receiver address is not allowed', dns_error: 'Receiver address could not be found', challenge_failed: 'Receiver check failed', site_unavailable: 'Site unavailable' }
const NOTIFY_DELIVERY_STATES = { pending: 'Waiting for delivery', delivered: 'Delivered', failed: 'Failed', skipped: 'Skipped' }

export async function render(ctx) {
  const { main, api: client, ui, event: data } = ctx
  const api = client.api
  const ev = data.event
  const org = data.organiser || {}
  const counts = org.counts || {}
  const admin = !!data.admin_view
  const path = '/v1/hack/events/' + encodeURIComponent(ev.slug)
  const setLive = ui.live(main)

  let notifications = [], notifyLimits = { can_create: false }
  try {
    const n = await api('GET', path + '/website/notifications')
    if (n.ok) { notifications = n.data.notifications || []; notifyLimits = n.data.limits || {} }
  } catch (e) {}

  // Applications card (owner 2026-10-31, D10): the switch lives here, with
  // People -> Sign-up only showing its state and a link back. directory_listed
  // comes from the voting endpoint (hack_voting.go) -- Results already reads
  // it there; this card only displays it, Results stays the one place to
  // change it.
  let directoryListed = false
  try {
    const v = await api('GET', path + '/voting')
    if (v.ok) directoryListed = !!v.data.directory_listed
  } catch (e) {}

  let addOpen = false, deleteOpen = false
  // The one-time secret from create/rotate -- shown next to the notification
  // it belongs to until the organiser copies it or dismisses the panel; the
  // GET that follows a create/rotate never returns it again.
  let newSecret = null // { secret, previous_valid_until, forId }
  let deliveriesFor = null, deliveriesList = null, deliveriesError = null
  const requireTyped = ev.stage === 'archived' || (counts.participants || 0) + (counts.judges || 0) > 0

  function receiverName(n) {
    if (n.source === 'mcp') return n.app_name || 'Connected app'
    if (n.label) return n.label
    try { return new URL(n.url).host } catch (e) { return n.url }
  }
  function secretPanel(s) {
    return `<div class="note ok"><p><b>Signing secret.</b> Copy it now. It will not be shown again.</p>
<div class="row-flex"><input class="input mono" id="notify-secret-${ui.esc(s.forId)}" type="text" readonly value="${ui.esc(s.secret)}"><button class="btn sm" type="button" data-notify-secret-copy="${ui.esc(s.forId)}">Copy</button><button class="btn sm" type="button" data-notify-secret-dismiss>Dismiss</button></div>
${s.previous_valid_until ? `<p class="help">The previous secret keeps working until ${ui.esc(ui.dateTime(s.previous_valid_until, ev))}.</p>` : ''}</div>`
  }
  function deliveriesPanel() {
    if (deliveriesError) return `<div class="note bad">${ui.esc(deliveriesError)}</div>`
    if (!deliveriesList) return '<div class="note">Loading deliveries…</div>'
    if (!deliveriesList.length) return `<div class="note">No deliveries in the last ${ui.esc(notifyLimits.history_days || 7)} days.</div>`
    return `<div class="list">${deliveriesList.map(d => `<div class="r"><div class="main"><b>${ui.esc((NOTIFY_EVENTS.find(x => x[0] === d.name) || [d.name, d.name])[1])}</b><span>${ui.esc(NOTIFY_DELIVERY_STATES[d.state] || d.state)}${d.result ? ' · ' + ui.esc(NOTIFY_RESULTS[d.result] || d.result) : ''}${d.last_http ? ' (HTTP ' + ui.esc(d.last_http) + ')' : ''}</span></div><div class="meta">${ui.esc(ui.dateTime(d.created_at, ev))}</div></div>`).join('') || '<div class="empty">No deliveries in this period.</div>'}</div>`
  }
  function notifyRow(n) {
    const events = (n.events || []).map(e => (NOTIFY_EVENTS.find(x => x[0] === e) || [e, e])[1]).join(', ')
    const state = NOTIFY_STATES[n.state] || n.state
    const result = n.last_attempt_at ? (NOTIFY_RESULTS[n.last_result] || 'No result yet') + ' · ' + ui.dateTime(n.last_attempt_at, ev) : 'No deliveries yet'
    const canManage = notifyLimits.can_manage !== false
    const isOwner = n.source !== 'mcp'
    return `<div class="setting" data-notify="${ui.esc(n.id)}"><div class="t"><b>${ui.esc(receiverName(n))}</b><span>${ui.esc(events)} &middot; ${ui.esc(n.storage || 'all saved data')}</span><span class="small muted">${ui.esc(state)} &middot; ${ui.esc(result)}</span></div>
<div class="row-flex">
<button class="btn sm" type="button" data-notify-history="${ui.esc(n.id)}">${deliveriesFor === n.id ? 'Hide deliveries' : 'Deliveries'}</button>
${canManage && isOwner && n.state !== 'expired' ? `<button class="btn sm" type="button" data-notify-test="${ui.esc(n.id)}">Send test</button>` : ''}
${canManage ? `<button class="btn sm" type="button" data-notify-pause="${ui.esc(n.id)}">${n.state === 'paused' || n.state === 'suspended' ? 'Resume' : 'Pause'}</button>` : ''}
${canManage && isOwner ? `<button class="btn sm" type="button" data-notify-rotate="${ui.esc(n.id)}">Rotate secret</button>` : ''}
<button class="btn danger sm" type="button" data-notify-off="${ui.esc(n.id)}">Remove</button>
</div>
${deliveriesFor === n.id ? deliveriesPanel() : ''}
${newSecret && newSecret.forId === n.id ? secretPanel(newSecret) : ''}
</div>`
  }

  const appsLocked = admin || ev.stage === 'archived'

  function paint() {
    const $ = id => main.querySelector('#' + id)
    const esc = ui.esc
    main.innerHTML = `
<div class="ph"><div class="t"><h1>Settings</h1><p>Your details, notifications and deleting the event.</p></div></div>
<section class="card"><h2>Your details</h2>
<div class="field"><label>Your name<input class="input" id="set-name" type="text" maxlength="100" value="${ui.esc(org.organiser_name || '')}"></label><p class="help">Shown on the event page.</p></div>
<div class="field"><label>Organisation or community<input class="input" id="set-org" type="text" maxlength="120" value="${ui.esc(org.organisation || '')}"></label></div>
<div class="field"><label>Contact email<input class="input" id="set-email" type="email" value="${ui.esc(org.contact_email || '')}"></label></div>
<div class="field"><label>What the event is for<textarea class="input" id="set-purpose" maxlength="1000">${ui.esc(org.purpose || '')}</textarea></label><p class="help">Only you and Simple Hack see this.</p></div>
<div class="field"><label>Expected participants<input class="input" id="set-n" type="number" min="1" max="100000" value="${ui.esc(org.expected_participants ?? '')}"></label></div>
<button class="btn primary" type="button" id="set-save">Save</button></section>

<section class="card" id="applications"><h2>Applications</h2>
<div class="setting"><div class="t"><div class="t-row"><b>Let people apply without your join link</b><span class="saved" id="apps-saved" hidden><svg class="ico" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 10.5l4 4 8-9"/></svg>Saved</span></div><span>When your event is listed in Find a hackathon, an Apply button shows there and on your event page while sign-up is open and during Building. Every application waits for you in People &rarr; Applications.</span></div><div class="ctl"><button class="toggle${ev.applications_open ? ' on' : ''}" type="button" role="switch" aria-checked="${!!ev.applications_open}" id="apps-toggle"${appsLocked ? ' disabled' : ''} aria-label="Let people apply without your join link"></button></div></div>
<p class="small muted">Your join link keeps working either way and stays private. Turning this off stops new applications; ones already waiting stay in People.</p>
<p class="small muted">${directoryListed ? `Listed in Find a hackathon: yes — <a href="${esc(href('organiser', ev.slug, 'results'))}">change in Results</a>.` : `Not listed in Find a hackathon, so nobody can apply yet. Applications start once you <a href="${esc(href('organiser', ev.slug, 'results'))}">list the event in Results</a>.`}</p>
</section>

<section class="card"><h2>Notifications</h2><p class="sub">Get a message when a visitor saves something or uploads a file on your own event website.</p>
${notifications.length ? notifications.map(notifyRow).join('') : '<p class="muted">No notifications set up.</p>'}
${addOpen ? `<form id="notify-form"><div class="field"><label>URL<input class="input" id="notify-url" type="url" placeholder="https://hooks.slack.com/services/..." required></label></div>${NOTIFY_EVENTS.map(([v, l], i) => `<label class="check"><input type="checkbox" id="notify-ev-${i}" data-notify-val="${v}"${i === 0 ? ' checked' : ''}><span>${ui.esc(l)}</span></label>`).join('')}
<div class="field"><label>Label (optional)<input class="input" id="notify-label" type="text" maxlength="100"></label></div>
<div class="field"><label>Storage (optional)<input class="input" id="notify-storage" type="text" maxlength="100" placeholder="all"></label></div>
<details id="notify-advanced"><summary class="small muted">Advanced</summary>
<div class="field"><label>Header name (optional)<input class="input" id="notify-header-name" type="text" maxlength="64"></label></div>
<div class="field"><label>Header value<input class="input" id="notify-header-value" type="password" maxlength="1024"></label></div>
</details>
<div class="row-flex"><button class="btn primary" type="submit">Add notification</button> <button class="btn" type="button" id="notify-cancel">Cancel</button></div></form>`
    : notifyLimits.can_create ? `<button class="btn" type="button" id="notify-add">Add notification</button>` : `<p class="small muted">Available once you put up your own website on Event page.</p>`}
</section>

<section class="danger-zone"><h2>Danger zone</h2>
<div class="setting"><div class="t"><b>Delete this event</b><span>Removes the public page, results, team sites, entries, scores, votes and memberships for good. There is no undo.</span></div><button class="btn danger" type="button" id="delete-open">Delete event</button></div>
${deleteOpen ? `<div class="setting"><div class="t">${requireTyped ? `<p>Type <b>${ui.esc(ev.slug)}</b> to confirm. This cannot be undone.</p><input class="input mono" id="delete-confirm" type="text">` : '<p>This cannot be undone.</p>'}<button class="btn danger" type="button" id="delete-go"${requireTyped ? ' disabled' : ''}>Delete event</button> <button class="btn" type="button" id="delete-cancel">Cancel</button></div></div>` : ''}
</section>`

    adminLock(main, admin)

    function flashSaved(id) {
      const el = $(id)
      el.hidden = false
      clearTimeout(el._t)
      el._t = setTimeout(() => { el.hidden = true }, 3000)
    }

    const appsToggle = $('apps-toggle')
    if (appsToggle) appsToggle.addEventListener('click', async () => {
      const want = !appsToggle.classList.contains('on')
      appsToggle.disabled = true
      try {
        const r = await api('PATCH', path, { applications_open: want })
        if (!r.ok) { setLive(ui.errText(r), 'err', appsToggle); appsToggle.disabled = false; return }
        ev.applications_open = want
        appsToggle.classList.toggle('on', want)
        appsToggle.setAttribute('aria-checked', String(want))
        flashSaved('apps-saved')
        appsToggle.disabled = appsLocked
      } catch (e) { setLive('Cannot reach the server.', 'err', appsToggle); appsToggle.disabled = false }
    })

    $('set-save').addEventListener('click', async () => {
      const btn = $('set-save'); btn.disabled = true
      const nRaw = $('set-n').value.trim()
      if (!/^[1-9]\d*$/.test(nRaw) || Number(nRaw) > 100000) { setLive('Expected participants must be between 1 and 100000.', 'err', btn); btn.disabled = false; return }
      try {
        const r = await api('PATCH', path, {
          organiser_name: $('set-name').value, organisation: $('set-org').value,
          contact_email: $('set-email').value, purpose: $('set-purpose').value,
          expected_participants: Number(nRaw)
        })
        if (!r.ok) { setLive(ui.errText(r), 'err', btn); btn.disabled = false; return }
        await ctx.refresh()
      } catch (e) { setLive('Cannot reach the server.', 'err', btn); btn.disabled = false }
    })

    const notifyAdd = $('notify-add')
    if (notifyAdd) notifyAdd.addEventListener('click', () => { addOpen = true; paint() })
    const notifyCancel = $('notify-cancel')
    if (notifyCancel) notifyCancel.addEventListener('click', () => { addOpen = false; paint() })
    const notifyForm = $('notify-form')
    if (notifyForm) notifyForm.addEventListener('submit', async e => {
      e.preventDefault()
      const events = NOTIFY_EVENTS.map((_, i) => $('notify-ev-' + i)).filter(b => b.checked).map(b => b.dataset.notifyVal)
      if (!events.length) { setLive('Choose at least one event.', 'err'); return }
      const body = { url: $('notify-url').value.trim(), events, storage: $('notify-storage').value.trim(), label: $('notify-label').value.trim() }
      const headerName = $('notify-header-name').value.trim(), headerValue = $('notify-header-value').value
      if (headerName || headerValue) body.auth_header = { name: headerName, value: headerValue }
      const btn = notifyForm.querySelector('button[type=submit]'); btn.disabled = true
      try {
        const r = await api('POST', path + '/website/notifications', body)
        if (!r.ok) { setLive(ui.errText(r), 'err', btn); btn.disabled = false; return }
        const newId = r.data.notification && r.data.notification.id
        const n = await api('GET', path + '/website/notifications')
        if (n.ok) { notifications = n.data.notifications || []; notifyLimits = n.data.limits || {} }
        // The create response's secret is the only time the server ever
        // hands it back in the clear -- keep it until the organiser copies
        // or dismisses it, rather than discarding it with the rest of the response.
        newSecret = r.data.secret ? { secret: r.data.secret, previous_valid_until: null, forId: newId } : null
        addOpen = false
        setLive('Notification added. Copy the signing secret below before it disappears.', 'ok')
        paint()
      } catch (e2) { setLive('Cannot reach the server.', 'err', btn); btn.disabled = false }
    })

    main.querySelectorAll('[data-notify-history]').forEach(btn => btn.addEventListener('click', async () => {
      const id = btn.getAttribute('data-notify-history')
      if (deliveriesFor === id) { deliveriesFor = null; deliveriesList = null; deliveriesError = null; paint(); return }
      deliveriesFor = id; deliveriesList = null; deliveriesError = null
      paint()
      try {
        const r = await api('GET', path + '/website/notifications/' + encodeURIComponent(id) + '/deliveries')
        if (deliveriesFor !== id) return
        if (!r.ok) { deliveriesError = ui.errText(r); paint(); return }
        deliveriesList = r.data.deliveries || []
        paint()
      } catch (e) { if (deliveriesFor === id) { deliveriesError = 'Cannot reach the server.'; paint() } }
    }))
    main.querySelectorAll('[data-notify-test]').forEach(btn => btn.addEventListener('click', async () => {
      const id = btn.getAttribute('data-notify-test'); btn.disabled = true
      try {
        const r = await api('POST', path + '/website/notifications/' + encodeURIComponent(id) + '/test')
        if (!r.ok) { setLive(ui.errText(r), 'err', btn); btn.disabled = false; return }
        const delivered = !!r.data.delivered
        setLive((delivered ? 'Test delivered' : (NOTIFY_RESULTS[r.data.result] || 'Could not deliver the test')).replace(/\.$/, '') + (r.data.status ? ' (HTTP ' + r.data.status + ')' : '') + '.', delivered ? 'ok' : 'err', btn)
        btn.disabled = false
      } catch (e) { setLive('Cannot reach the server.', 'err', btn); btn.disabled = false }
    }))
    main.querySelectorAll('[data-notify-pause]').forEach(btn => btn.addEventListener('click', async () => {
      const id = btn.getAttribute('data-notify-pause')
      const current = notifications.find(x => x.id === id)
      const toPause = !(current && (current.state === 'paused' || current.state === 'suspended'))
      btn.disabled = true
      try {
        const r = await api('PATCH', path + '/website/notifications/' + encodeURIComponent(id), { paused: toPause })
        if (!r.ok) { setLive(ui.errText(r), 'err', btn); btn.disabled = false; return }
        const n = await api('GET', path + '/website/notifications')
        if (n.ok) { notifications = n.data.notifications || []; notifyLimits = n.data.limits || {} }
        paint()
      } catch (e) { setLive('Cannot reach the server.', 'err', btn); btn.disabled = false }
    }))
    main.querySelectorAll('[data-notify-rotate]').forEach(btn => btn.addEventListener('click', async () => {
      const id = btn.getAttribute('data-notify-rotate')
      const ok = await ui.confirm({ title: 'Rotate the signing secret?', body: 'The previous secret keeps working for a short overlap window, then stops. Update your receiver with the new one once you have copied it.', confirmLabel: 'Rotate' })
      if (!ok) return
      btn.disabled = true
      try {
        const r = await api('POST', path + '/website/notifications/' + encodeURIComponent(id) + '/rotate-secret')
        if (!r.ok) { setLive(ui.errText(r), 'err', btn); btn.disabled = false; return }
        newSecret = { secret: r.data.secret, previous_valid_until: r.data.previous_valid_until, forId: id }
        paint()
      } catch (e) { setLive('Cannot reach the server.', 'err', btn); btn.disabled = false }
    }))
    main.querySelectorAll('[data-notify-secret-copy]').forEach(btn => btn.addEventListener('click', async () => {
      const id = btn.getAttribute('data-notify-secret-copy')
      const input = $('notify-secret-' + id)
      try { await navigator.clipboard.writeText(input.value); btn.textContent = 'Copied'; setTimeout(() => { btn.textContent = 'Copy' }, 2000) }
      catch (e) { input.focus(); input.select(); setLive('Select and copy the secret.', 'err', btn) }
    }))
    main.querySelectorAll('[data-notify-secret-dismiss]').forEach(btn => btn.addEventListener('click', () => { newSecret = null; paint() }))
    main.querySelectorAll('[data-notify-off]').forEach(btn => btn.addEventListener('click', async () => {
      const id = btn.getAttribute('data-notify-off')
      const ok = await ui.confirm({ title: 'Remove this notification?', body: 'It stops firing right away.', confirmLabel: 'Remove', danger: true })
      if (!ok) return
      btn.disabled = true
      try {
        const r = await api('DELETE', path + '/website/notifications/' + encodeURIComponent(id))
        if (!r.ok) { setLive(ui.errText(r), 'err', btn); btn.disabled = false; return }
        notifications = notifications.filter(n => n.id !== id)
        if (deliveriesFor === id) { deliveriesFor = null; deliveriesList = null; deliveriesError = null }
        if (newSecret && newSecret.forId === id) newSecret = null
        paint()
      } catch (e) { setLive('Cannot reach the server.', 'err', btn); btn.disabled = false }
    }))

    $('delete-open').addEventListener('click', () => { deleteOpen = true; paint() })
    const delCancel = $('delete-cancel')
    if (delCancel) delCancel.addEventListener('click', () => { deleteOpen = false; paint() })
    const delInput = $('delete-confirm')
    if (delInput) delInput.addEventListener('input', () => { $('delete-go').disabled = delInput.value.trim() !== ev.slug })
    const delGo = $('delete-go')
    if (delGo) delGo.addEventListener('click', async () => {
      if (!requireTyped) {
        const ok = await ui.confirm({ title: 'Delete this event?', body: 'This cannot be undone.', confirmLabel: 'Delete this event', danger: true })
        if (!ok) return
      }
      delGo.disabled = true
      try {
        const r = requireTyped
          ? await api('DELETE', path, { confirm: ev.slug })
          : await api('DELETE', path)
        if (r.status !== 204) { setLive(ui.errText(r), 'err', delGo); delGo.disabled = requireTyped ? (delInput.value.trim() !== ev.slug) : false; return }
        ctx.go('/events')
      } catch (e) { setLive('Cannot reach the server.', 'err', delGo); delGo.disabled = false }
    })
  }

  paint()
}
