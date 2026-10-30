import { href } from '../nav.js'
import { labelList, fmtBytes } from './_orgB-util.js'

const OPTIONAL_FIELDS = ['tagline', 'description', 'video_url', 'code_url', 'screenshot']

export function title() { return 'Entries' }

export async function render(ctx) {
  const { main, api: client, ui, event, role } = ctx
  const api = client.api
  const slug = event.event.slug
  const $ = id => main.querySelector('#' + id)
  const setLive = ui.live(main)

  let ev = event.event
  let teams = [], entries = [], required = []
  let usage = null

  async function load() {
    const [tr, er, ur] = await Promise.all([
      api('GET', '/v1/hack/events/' + encodeURIComponent(slug) + '/teams'),
      api('GET', '/v1/hack/events/' + encodeURIComponent(slug) + '/entries'),
      api('GET', '/v1/hack/events/' + encodeURIComponent(slug) + '/usage')
    ])
    if (tr.ok) teams = tr.data.teams || []
    if (er.ok) { entries = er.data.entries || []; required = er.data.required || [] }
    else setLive(er.data.error || 'Could not load entries', true)
    if (ur.ok) usage = ur.data
  }

  function teamBySlug(slug2) { return teams.find(t => t.slug === slug2) }

  function extendedTeams() { return teams.filter(t => t.extended) }

  function entryRow(e) {
    const bits = []
    bits.push(e.complete ? 'Complete' : 'Missing: ' + labelList(e.missing).join(', '))
    const t = teamBySlug(e.team.slug)
    if (t && t.extended) bits.push('More time until ' + ui.dateTime(t.deadline_override, ev))
    bits.push(e.site_exists ? (e.site_taken_down ? 'Site taken down' : 'Site live') : 'No site yet')
    return `<a class="r" href="${ui.esc(href(role, slug, 'entry') + '&team=' + encodeURIComponent(e.team.slug))}"><span class="main"><b>${ui.esc(e.team.name)}</b><span>${ui.esc(bits.join(' · '))}</span></span><span class="meta">View entry</span></a>`
  }

  function extensionRow(t, locked, archived) {
    const text = ui.esc(t.name) + ' · More time until ' + ui.esc(ui.dateTime(t.deadline_override, ev))
    if (archived) return `<p>${text}</p>`
    return `<p>${text} <button class="btn danger sm" type="button" data-clear-ext="${ui.esc(t.slug)}"${locked ? ' disabled' : ''}>Clear this team’s extension</button></p>`
  }

  function paint() {
    const deadlineInput = ui.localInputValue(ev.submission_deadline, ev)
    const closedStages = ['closed', 'judging', 'results']
    const lockReason = ui.writeLockReason(ev, event.admin_view)
    const archived = ev.stage === 'archived'
    main.innerHTML = `<div class="ph"><div class="t"><h1>Entries</h1><p>Set the deadline and see who’s ready.</p></div></div>
${lockReason ? `<div class="note warn">${ui.esc(lockReason)}</div>` : ''}
<section class="card"><h2>Deadline</h2>
${archived
    ? `<p>${ev.submission_deadline ? 'Entries closed ' + ui.esc(ui.dateTime(ev.submission_deadline, ev)) : 'No deadline was set.'}</p>`
    : `<div class="field"><label>Entries close · ${ui.esc(ev.time_zone || 'UTC')}<input class="input" id="deadline-input" type="datetime-local" value="${ui.esc(deadlineInput)}"${lockReason ? ' disabled' : ''}></label></div>
<p class="${ev.submission_deadline ? 'help' : 'small muted'}">${ev.submission_deadline ? ui.esc(ui.dateTime(ev.submission_deadline, ev)) : 'No deadline yet.'}</p>
${closedStages.includes(ev.stage) ? '<p>To reopen, move the event back to Building on Overview.</p>' : ''}
<div class="row-flex"><button class="btn primary" type="button" id="deadline-save"${lockReason ? ' disabled' : ''}>Save deadline</button><button class="btn quiet" type="button" id="deadline-clear"${ev.submission_deadline && !lockReason ? '' : ' disabled'}>Clear deadline</button></div>`}
</section>
<h2 class="section-title">Entries</h2>
${entries.length ? `<div class="list">${entries.map(entryRow).join('')}</div>` : '<div class="list"><div class="empty">No entries yet.</div></div>'}
<section class="card"><h2>What a complete entry needs</h2>
<label class="check"><input type="checkbox" checked disabled>Title (always required)</label>
${OPTIONAL_FIELDS.map(f => `<label class="check"><input type="checkbox" data-req="${f}"${required.includes(f) ? ' checked' : ''}${lockReason ? ' disabled' : ''}>${ui.esc(labelList([f])[0])}</label>`).join('')}
<p class="help">Changes save straight away.</p>
</section>
<section class="card"><h2>Extensions</h2>
${extendedTeams().length ? extendedTeams().map(t => extensionRow(t, !!lockReason, archived)).join('') : (ev.submission_deadline && !archived ? '<p class="muted">No team has an extension.</p>' : '')}
${archived ? '' : (ev.submission_deadline
    ? `<div class="field"><label>Team<select class="select" id="ext-team"${lockReason ? ' disabled' : ''}>${teams.map(t => `<option value="${ui.esc(t.slug)}">${ui.esc(t.name)}</option>`).join('')}</select></label></div>
<div class="field"><label>More time until · ${ui.esc(ev.time_zone || 'UTC')}<input class="input" id="ext-input" type="datetime-local"${lockReason ? ' disabled' : ''}></label></div>
<button class="btn" type="button" id="ext-save"${lockReason ? ' disabled' : ''}>Give this team more time</button>`
    : '<p class="help">Set the event deadline first. Then you can give one team more time.</p>')}
</section>
<section class="card"><h2>Export</h2><button class="btn" type="button" id="export-entries">Download entries (CSV)</button><p class="small muted">This event’s team sites use ${usage ? ui.esc(fmtBytes((usage.saved_data_bytes || 0) + (usage.history_bytes || 0) + (usage.screenshot_bytes || 0) + (usage.file_bytes || 0))) : '…'}</p></section>`
    wire()
  }

  function wire() {
    const saveBtn = $('deadline-save')
    if (saveBtn) saveBtn.addEventListener('click', onSaveDeadline)
    const clearBtn = $('deadline-clear')
    if (clearBtn) clearBtn.addEventListener('click', onClearDeadline)
    main.querySelectorAll('[data-req]').forEach(cb => cb.addEventListener('change', () => onToggleRequired(cb)))
    main.querySelectorAll('[data-clear-ext]').forEach(btn => btn.addEventListener('click', () => onClearExtension(btn.getAttribute('data-clear-ext'))))
    const extSave = $('ext-save')
    if (extSave) extSave.addEventListener('click', onGiveExtension)
    $('export-entries').addEventListener('click', () => client.downloadFile('/v1/hack/events/' + encodeURIComponent(slug) + '/export/entries.csv', slug + '-entries.csv', $('export-entries')))
  }

  async function patchEv(body) {
    let r
    try { r = await api('PATCH', '/v1/hack/events/' + encodeURIComponent(slug), body) }
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', true); return null }
    if (!r.ok) { setLive(r.data.error || 'Could not save', true); return null }
    return r.data
  }

  async function onSaveDeadline() {
    setLive('')
    const value = $('deadline-input').value
    if (!value) { setLive('Choose a date and time.', true); return }
    const data = await patchEv({ submission_deadline: value })
    if (!data) return
    ev = data.event
    await load()
    paint()
    ui.saved(main)
  }

  async function onClearDeadline() {
    const ok = await ui.confirm({ title: 'Clear deadline?', body: 'Teams can keep changing sites and entries with no deadline until you set a new one. Clear it?', confirmLabel: 'Clear deadline', danger: true })
    if (!ok) return
    const data = await patchEv({ submission_deadline: '' })
    if (!data) return
    ev = data.event
    await load()
    paint()
    ui.saved(main)
  }

  async function onToggleRequired(cb) {
    const checked = new Set(['title'])
    main.querySelectorAll('[data-req]').forEach(x => { if (x === cb ? cb.checked : x.checked) checked.add(x.getAttribute('data-req')) })
    const data = await patchEv({ entry_required: Array.from(checked) })
    if (!data) { cb.checked = !cb.checked; return }
    ev = data.event
    required = ev.entry_required || []
    ui.saved(main)
  }

  async function onGiveExtension() {
    setLive('')
    const teamSlug = $('ext-team').value
    const value = $('ext-input').value
    if (!value) { setLive('Choose a date and time.', true); return }
    let r
    try { r = await api('PUT', '/v1/hack/events/' + encodeURIComponent(slug) + '/teams/' + encodeURIComponent(teamSlug) + '/deadline', { deadline: value }) }
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', true); return }
    if (!r.ok) { setLive(r.data.error || 'Could not set that team’s extension', true); return }
    await load()
    paint()
    ui.saved(main)
  }

  async function onClearExtension(teamSlug) {
    const t = teamBySlug(teamSlug)
    const name = t ? t.name : 'This team'
    const ok = await ui.confirm({ title: 'Clear the extension?', body: name + ' reverts to the event’s general deadline, which may already have passed. Clear the extension?', confirmLabel: 'Clear extension', danger: true })
    if (!ok) return
    let r
    try { r = await api('PUT', '/v1/hack/events/' + encodeURIComponent(slug) + '/teams/' + encodeURIComponent(teamSlug) + '/deadline', { deadline: '' }) }
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', true); return }
    if (!r.ok) { setLive(r.data.error || 'Could not clear the extension', true); return }
    await load()
    paint()
    ui.saved(main)
  }

  await load()
  paint()
}
