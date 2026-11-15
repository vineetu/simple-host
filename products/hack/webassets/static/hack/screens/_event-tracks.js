import { slugify } from './_orgB-util.js'
import { rowMenuHTML, wireRowMenus, formDialog, undoToast, adminLock } from './_orgA-util.js'

// Existing track editor, shared with the Event page tab.
export async function render(ctx) {
 const { main, api: client, ui, event } = ctx
 const api = client.api, ev = event.event, slug = ev.slug, esc = ui.esc
 const setLive = ui.live(main)
 let tracks = (ev.tracks || []).map(t => ({ ...t })), teams = []
 async function load() {
  const r = await api('GET', '/v1/hack/events/' + encodeURIComponent(slug) + '/teams')
  if (!ctx.alive()) return
  if (r.ok) teams = r.data.teams || []
 }
 async function paint() {
  if (!ctx.alive()) return
  main.innerHTML = '<section class="sp"></section>'
  paintTracks(main.querySelector('.sp'))
  adminLock(main, !!event.admin_view, ev)
 }
 wireRowMenus()
  function trackRow(t, i) {
    const n = teams.filter(team => team.track && team.track.slug === t.slug).length
    const menu = rowMenuHTML(t.name || 'this track', [{ label: 'Edit', act: 'edit' }, { label: 'Remove', act: 'remove', danger: true }])
    return `<div class="r" data-track="${i}"><div class="main"><b>${esc(t.name)}</b><span>${esc(t.challenge || '')}${t.prize ? ' · Prize: ' + esc(t.prize) : ''}</span></div><span class="meta">${n} team${n === 1 ? '' : 's'}</span><div class="row-acts"><button class="btn quiet sm" type="button" data-edit-track="${i}">Edit</button>${menu}</div></div>`
  }

  function paintTracks(sp) {
    sp.innerHTML = `<div class="sp-head"><h2 id="sp-h" tabindex="-1">Tracks</h2><p>Challenges teams can enter. Each team picks its own on its Team page.</p><button class="btn primary" type="button" id="track-add"><svg class="ico" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M10 4v12M4 10h12"/></svg>Add track</button></div>
${ev.stage === 'archived' ? `<div class="note warn">This event has ended. Tracks can’t change any more.</div>` : ''}
${tracks.length ? `<div class="list editable">${tracks.map(trackRow).join('')}</div>` : `<div class="empty-state"><h3>No tracks yet</h3><p>Add tracks if teams should pick a challenge. Without tracks, everyone competes together.</p></div>`}`
    sp.querySelector('#track-add').addEventListener('click', () => onEditTrack(null))
    sp.querySelectorAll('[data-edit-track]').forEach(btn => btn.addEventListener('click', () => onEditTrack(Number(btn.getAttribute('data-edit-track')))))
    sp.querySelectorAll('[data-track]').forEach(row => {
      const i = Number(row.getAttribute('data-track'))
      row.querySelectorAll('.row-menu button').forEach(item => {
        item.addEventListener('click', () => {
          const act = item.getAttribute('data-act')
          if (act === 'edit') onEditTrack(i)
          else if (act === 'remove') onRemoveTrack(i)
        })
      })
    })
  }

  async function onEditTrack(i) {
    const existing = i == null ? null : tracks[i]
    const body = document.createElement('div')
    body.innerHTML = `<div class="field"><label for="trk-name">Track name</label><input class="input" id="trk-name" maxlength="80" value="${esc(existing ? existing.name : '')}"></div>
<div class="field"><label for="trk-challenge">Challenge</label><textarea class="input" id="trk-challenge">${esc(existing ? existing.challenge : '')}</textarea></div>
<div class="field"><label for="trk-prize">Prize</label><input class="input" id="trk-prize" maxlength="80" value="${esc(existing ? existing.prize : '')}"></div>`
    const saved = await formDialog(ui, { title: existing ? 'Edit track' : 'Add track', body, saveLabel: existing ? 'Save track' : 'Add track' }, async () => {
      const name = body.querySelector('#trk-name').value.trim()
      if (!name) { setLive('Enter a track name.', true, body); return false }
      const next = { slug: existing ? existing.slug : slugify(name), name, challenge: body.querySelector('#trk-challenge').value.trim(), prize: body.querySelector('#trk-prize').value.trim() }
      const list = tracks.slice()
      if (existing) list[i] = next
      else list.push(next)
      return saveTracks(list)
    })
    if (saved) { await load(); await paint() }
  }

  async function onRemoveTrack(i) {
    const t = tracks[i]
    const n = teams.filter(team => team.track && t.slug && team.track.slug === t.slug).length
    const body = n
      ? `${n} team${n === 1 ? '' : 's'} chose ${t.name || 'this track'}. They’ll have no track. Remove it?`
      : `Remove ${t.name || 'this track'}?`
    const ok = await ui.confirm({ title: 'Remove track?', body, confirmLabel: 'Remove', danger: true })
    if (!ok) return
    const next = tracks.slice(0, i).concat(tracks.slice(i + 1))
    const name = t.name || 'Track'
    const before = tracks.slice()
    if (await saveTracks(next)) {
      await load()
      await paint()
      const sp = main.querySelector('.sp')
      undoToast(sp, 'Removed ' + name, async () => { if (await saveTracks(before)) { await load(); await paint() } })
    }
  }

  async function saveTracks(list) {
    const payload = list.map(t => ({ slug: t.slug || slugify(t.name), name: t.name, challenge: t.challenge, prize: t.prize }))
    let r
    try { r = await api('PUT', '/v1/hack/events/' + encodeURIComponent(slug) + '/tracks', { tracks: payload }) }
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', true); return false }
    if (!r.ok) { setLive(ui.errText(r, { fallback: 'Could not save tracks' }), true); return false }
    tracks = (r.data || []).map(t => ({ ...t }))
    ev.tracks = tracks
    return true
  }


 await load()
 await paint()
}
