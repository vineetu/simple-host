import { errText, adminLock } from './_orgA-util.js'

export function title(ctx) { return 'Settings' }

const NOTIFY_EVENTS = [['record.added', 'A visitor saves something'], ['record.changed', 'A visitor changes something they saved'], ['file.uploaded', 'A visitor uploads a file']]

export async function render(ctx) {
  const { main, api: client, ui, event: data } = ctx
  const api = client.api
  const ev = data.event
  const org = data.organiser || {}
  const counts = org.counts || {}
  const admin = !!data.admin_view
  const path = '/v1/hack/events/' + encodeURIComponent(ev.slug)
  const setLive = (msg, kind, near) => { if (msg) ui.toast(msg, near || main, kind === 'err') }

  let notifications = [], notifyLimits = { can_create: false }
  try {
    const n = await api('GET', path + '/website/notifications')
    if (n.ok) { notifications = n.data.notifications || []; notifyLimits = n.data.limits || {} }
  } catch (e) {}

  let addOpen = false, deleteOpen = false
  const requireTyped = ev.stage === 'archived' || (counts.participants || 0) + (counts.judges || 0) > 0

  function notifyRow(n) {
    return `<div class="setting" data-notify="${ui.esc(n.id)}"><div class="t"><b>${ui.esc(n.url)}</b><span>${ui.esc((n.events || []).map(e => (NOTIFY_EVENTS.find(x => x[0] === e) || [e, e])[1]).join(', '))}</span></div><button class="btn danger" type="button" data-notify-off="${ui.esc(n.id)}">Turn off</button></div>`
  }

  function paint() {
    const $ = id => main.querySelector('#' + id)
    main.innerHTML = `
<div class="ph"><div class="t"><h1>Settings</h1><p>Event identity, integrations and account controls.</p></div></div>
<section class="card"><h2>Organiser</h2>
<div class="field"><label>Your name (shown on the event page)<input class="input" id="set-name" type="text" maxlength="100" value="${ui.esc(org.organiser_name || '')}"></label></div>
<div class="field"><label>Organisation or community<input class="input" id="set-org" type="text" maxlength="120" value="${ui.esc(org.organisation || '')}"></label></div>
<div class="field"><label>Contact email<input class="input" id="set-email" type="email" value="${ui.esc(org.contact_email || '')}"></label></div>
<div class="field"><label>Purpose<textarea class="input" id="set-purpose" maxlength="1000">${ui.esc(org.purpose || '')}</textarea></label></div>
<div class="field"><label>Expected participants<input class="input" id="set-n" type="number" min="1" max="100000" value="${ui.esc(org.expected_participants ?? '')}"></label></div>
<button class="btn primary" type="button" id="set-save">Save</button></section>

<section class="card"><h2>Notifications</h2><p class="sub">Fires when a visitor saves something or uploads a file on your custom event website.</p>
${notifications.length ? notifications.map(notifyRow).join('') : '<p class="muted">No notifications set up.</p>'}
${addOpen ? `<form id="notify-form"><div class="field"><label>URL<input class="input" id="notify-url" type="url" placeholder="https://hooks.slack.com/services/..." required></label></div>${NOTIFY_EVENTS.map(([v, l], i) => `<label class="check"><input type="checkbox" id="notify-ev-${i}" data-notify-val="${v}"${i === 0 ? ' checked' : ''}><span>${ui.esc(l)}</span></label>`).join('')}<button class="btn primary" type="submit">Add notification</button> <button class="btn" type="button" id="notify-cancel">Cancel</button></form>`
    : `<button class="btn" type="button" id="notify-add"${notifyLimits.can_create ? '' : ' disabled title="Available once this event has its own custom website."'}>Add notification</button>`}
</section>

<section class="danger-zone"><h2>Danger zone</h2>
<div class="setting"><div class="t"><b>Delete this event</b><span>Removes the public page, results, team sites, entries, scores, votes and memberships for good. There is no undo.</span></div><button class="btn danger" type="button" id="delete-open">Delete this event</button></div>
${deleteOpen ? `<div class="setting"><div class="t">${requireTyped ? `<p>Type <b>${ui.esc(ev.slug)}</b> to confirm. This cannot be undone.</p><input class="input mono" id="delete-confirm" type="text">` : '<p>This cannot be undone.</p>'}<button class="btn danger" type="button" id="delete-go"${requireTyped ? ' disabled' : ''}>Delete this event</button> <button class="btn" type="button" id="delete-cancel">Cancel</button></div></div>` : ''}
<div class="setting"><div class="t"><b>Leave this event</b><span>Planned, SIM-340, not built yet. Hand the event to another organiser first; the creator can’t leave without doing that.</span></div><button class="btn danger" type="button" disabled>Leave this event</button></div>
</section>`

    adminLock(main, admin)

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
        if (!r.ok) { setLive(errText(r), 'err', btn); btn.disabled = false; return }
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
      const btn = notifyForm.querySelector('button[type=submit]'); btn.disabled = true
      try {
        const r = await api('POST', path + '/website/notifications', { url: $('notify-url').value, events })
        if (!r.ok) { setLive(errText(r), 'err', btn); btn.disabled = false; return }
        const n = await api('GET', path + '/website/notifications')
        if (n.ok) { notifications = n.data.notifications || []; notifyLimits = n.data.limits || {} }
        addOpen = false
        setLive('Notification added', 'ok')
        paint()
      } catch (e2) { setLive('Cannot reach the server.', 'err', btn); btn.disabled = false }
    })
    main.querySelectorAll('[data-notify-off]').forEach(btn => btn.addEventListener('click', async () => {
      const id = btn.getAttribute('data-notify-off')
      const ok = await ui.confirm({ title: 'Turn off this notification?', body: 'It stops firing right away.', confirmLabel: 'Turn off', danger: true })
      if (!ok) return
      btn.disabled = true
      try {
        const r = await api('DELETE', path + '/website/notifications/' + encodeURIComponent(id))
        if (!r.ok) { setLive(errText(r), 'err', btn); btn.disabled = false; return }
        notifications = notifications.filter(n => n.id !== id)
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
        if (r.status !== 204) { setLive(errText(r), 'err', delGo); delGo.disabled = requireTyped ? (delInput.value.trim() !== ev.slug) : false; return }
        ctx.go('/events')
      } catch (e) { setLive('Cannot reach the server.', 'err', delGo); delGo.disabled = false }
    })
  }

  paint()
}
