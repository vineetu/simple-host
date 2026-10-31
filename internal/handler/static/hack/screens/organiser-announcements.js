// Atlas 2026-10-30: Announcements moves off Event page onto its own
// sidebar section (spec.json "organiser-announcements") -- posting is the
// live-weekend action, often from a phone, and the old screen buried it
// between Schedule and Sponsors. Keeps every capability the old block had:
// post, edit, delete, the "email every participant too" toggle. New here:
// the Post button itself reads "Post and email N people" while the box is
// ticked, with no separate send confirmation, Edit opens a dialog instead
// of an inline form, and Delete keeps a confirm dialog -- an announcement
// already reached everyone's feed/inbox, which Undo can't take back,
// unlike a schedule/sponsor/FAQ row, which the Event page's Remove+Undo
// covers.
import { rowMenuHTML, wireRowMenus, formDialog } from './_orgA-util.js'

export function title() { return 'Announcements' }

export async function render(ctx) {
  const { main, api: client, ui, event: data } = ctx
  const api = client.api
  const ev = data.event
  const admin = !!data.admin_view
  const ended = ev.stage === 'archived'
  const readOnly = admin || ended
  const path = '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/announcements'
  const setLive = ui.live(main)
  const peopleCount = (data.organiser && data.organiser.counts && data.organiser.counts.participants) || 0

  wireRowMenus()

  let announcements = []
  let loadError = false
  try {
    const r = await api('GET', path)
    if (!ctx.alive()) return
    if (r.ok) announcements = r.data.announcements || []
    else loadError = true
  } catch (e) { loadError = true }
  if (!ctx.alive()) return

  // R12: `db.EventAnnouncement` has only id, title, body and created_at --
  // the API never returns email_participants/emailed, so a pill that
  // depended on it never showed, and the delete confirm's warning (below)
  // never fired either. The "Emailed" pill is dropped rather than built on
  // a field the server doesn't send.
  function rowHTML(a) {
    const meta = `<span class="meta"><span>${ui.esc(ui.dateTime(a.created_at, ev))}</span></span>`
    if (readOnly) return `<div class="r ann"><span class="main"><b>${ui.esc(a.title)}</b><span class="clamp2">${ui.esc(a.body)}</span></span>${meta}</div>`
    const menu = rowMenuHTML(a.title, [{ label: 'Edit', act: 'edit' }, { label: 'Delete', act: 'delete', danger: true }])
    return `<div class="r ann" data-id="${ui.esc(a.id)}"><a class="main" href="#" data-act="edit"><b>${ui.esc(a.title)}</b><span class="clamp2">${ui.esc(a.body)}</span></a>${meta}<div class="row-acts"><button class="btn quiet sm" type="button" data-act="edit" aria-label="Edit ${ui.esc(a.title)}">Edit</button>${menu}</div></div>`
  }

  main.innerHTML = `<div class="ph"><div class="t"><h1>Announcements</h1><p>Updates for everyone in the event, on your public page and in participants&#39; app.</p></div></div>
<div class="page narrow" style="padding:0">
${admin ? '<div class="note warn" role="alert">Viewing as platform admin. Everything here is read only.</div>' : ''}
${ended && !admin ? '<div class="note">This event has ended. The list below stays readable; nothing can be posted or changed.</div>' : ''}
${loadError ? '<div class="note bad" role="alert">Could not load announcements.</div>' : ''}
${readOnly ? '' : `<form class="card" aria-labelledby="ann-h" id="ann-form"><h2 id="ann-h" style="margin:0 0 14px">New announcement</h2>
<div class="field"><div class="lab-row"><label for="ann-title">Title</label><span class="counter" id="ann-title-ct">0 / 120</span></div><input class="input" id="ann-title" maxlength="120"></div>
<div class="field"><label for="ann-body">Message</label><textarea class="input" id="ann-body" rows="4" maxlength="3000"></textarea></div>
<label class="check"><input type="checkbox" id="ann-email"><div><b>Also email every participant</b><span>${peopleCount} people. Editing it later never sends another email.</span></div></label>
<div class="card-foot"><span class="hint">Shows on your page and in the app straight away.</span><button class="btn primary" type="submit" id="ann-post">Post</button></div>
</form>`}
<p class="section-title">${announcements.length ? `Posted · ${announcements.length}` : 'Posted'}</p>
${announcements.length ? `<div class="list editable" aria-label="Posted announcements" id="ann-list">${announcements.map(rowHTML).join('')}</div>` :
    `<div class="empty-state"><svg class="ico" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3 8v4h3l6 4V4L6 8zm12-1.5a4 4 0 0 1 0 7"/></svg><h3>Nothing posted yet</h3><p style="margin-bottom:0">Your first announcement shows here, on your page and in participants' app.</p></div>`}
</div>`

  if (readOnly) return

  const titleInput = main.querySelector('#ann-title')
  const titleCt = main.querySelector('#ann-title-ct')
  const emailBox = main.querySelector('#ann-email')
  const postBtn = main.querySelector('#ann-post')
  titleInput.addEventListener('input', () => { titleCt.textContent = titleInput.value.length + ' / 120' })
  emailBox.addEventListener('change', () => { postBtn.textContent = emailBox.checked ? `Post and email ${peopleCount} people` : 'Post' })

  main.querySelector('#ann-form').addEventListener('submit', async e => {
    e.preventDefault()
    postBtn.disabled = true
    try {
      const r = await api('POST', path, { title: titleInput.value, body: main.querySelector('#ann-body').value, email_participants: emailBox.checked })
      if (!r.ok) { setLive(ui.errText(r), 'err', postBtn); postBtn.disabled = false; return }
      const emailed = emailBox.checked
      setLive(emailed ? `Posted and emailed to ${peopleCount} people` : 'Posted', 'ok')
      await ctx.refresh()
    } catch (e2) { setLive('Cannot reach the server.', 'err', postBtn); postBtn.disabled = false }
  })

  main.querySelectorAll('[data-act="edit"]').forEach(el => el.addEventListener('click', async e => {
    e.preventDefault()
    const id = el.closest('[data-id]').dataset.id
    const a = announcements.find(x => x.id === id)
    if (!a) return
    const body = document.createElement('div')
    body.innerHTML = `<p class="muted" style="margin:0 0 12px">Saving changes doesn't email anyone again.</p>
<div class="field"><label for="ea-title">Title</label><input class="input" id="ea-title" maxlength="120" value="${ui.esc(a.title)}" autofocus></div>
<div class="field"><label for="ea-body">Message</label><textarea class="input" id="ea-body" rows="4" maxlength="3000">${ui.esc(a.body)}</textarea></div>`
    const q = sel => body.querySelector(sel)
    const saved = await formDialog(ui, { title: 'Edit announcement', body, saveLabel: 'Save' }, async () => {
      body.querySelectorAll('.err').forEach(n => n.remove())
      const t = q('#ea-title').value.trim(), b = q('#ea-body').value.trim()
      if (!t) { q('#ea-title').insertAdjacentHTML('afterend', '<p class="err">Title is required.</p>'); return false }
      if (!b) { q('#ea-body').insertAdjacentHTML('afterend', '<p class="err">Message is required.</p>'); return false }
      try {
        const r = await api('PUT', path + '/' + encodeURIComponent(id), { title: t, body: b })
        if (!r.ok) { body.insertAdjacentHTML('beforeend', `<p class="err">${ui.esc(ui.errText(r))}</p>`); return false }
        return true
      } catch (e3) { body.insertAdjacentHTML('beforeend', '<p class="err">Cannot reach the server.</p>'); return false }
    })
    if (saved) await ctx.refresh()
  }))

  main.querySelectorAll('[data-act="delete"]').forEach(el => el.addEventListener('click', async () => {
    const id = el.closest('[data-id]').dataset.id
    const a = announcements.find(x => x.id === id)
    if (!a) return
    // R12: the API has no way to tell whether this one was emailed (see
    // rowHTML's own note), so the warning is unconditional instead of
    // depending on a field that's never there -- an announcement that
    // genuinely was emailed must never delete without this warning.
    const body = "It disappears from your page and from everyone's app. Emails already sent stay sent."
    const ok = await ui.confirm({ title: `Delete “${a.title}”?`, body, confirmLabel: 'Delete announcement', danger: true })
    if (!ok) return
    try {
      const r = await api('DELETE', path + '/' + encodeURIComponent(id))
      if (!r.ok) { setLive(ui.errText(r), 'err'); return }
      await ctx.refresh()
    } catch (e) { setLive('Cannot reach the server.', 'err') }
  }))
}
