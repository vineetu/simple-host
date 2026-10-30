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

  function extensionRow(t, locked) {
    return `<p>${ui.esc(t.name)} · More time until ${ui.esc(ui.dateTime(t.deadline_override, ev))} <button class="btn danger sm" type="button" data-clear-ext="${ui.esc(t.slug)}"${locked ? ' disabled' : ''}>Clear this team’s extension</button></p>`
  }

  function paint() {
    const deadlineInput = ui.localInputValue(ev.submission_deadline, ev)
    const closedStages = ['closed', 'judging', 'results']
    const lockReason = ui.writeLockReason(ev, event.admin_view)
    main.innerHTML = `<div class="ph"><div class="t"><h1>Entries</h1><p>Set the deadline and see who’s ready.</p></div></div>
${lockReason ? `<div class="note warn">${ui.esc(lockReason)}</div>` : ''}
<section class="card"><h2>Deadline</h2>
<div class="field"><label>Entries close · ${ui.esc(ev.time_zone || 'UTC')}<input class="input" id="deadline-input" type="datetime-local" value="${ui.esc(deadlineInput)}"${lockReason ? ' disabled' : ''}></label></div>
<p class="help">${ev.submission_deadline ? ui.esc(ui.dateTime(ev.submission_deadline, ev)) : 'No deadline set.'}</p>
${closedStages.includes(ev.stage) ? '<p>To reopen, move the event back to Building on Overview.</p>' : ''}
<button class="btn primary" type="button" id="deadline-save"${lockReason ? ' disabled' : ''}>Save deadline</button>
<section class="danger-zone"><h2>Danger zone</h2><div class="setting"><div class="t"><button class="btn danger" type="button" id="deadline-clear"${ev.submission_deadline && !lockReason ? '' : ' disabled'}>Clear deadline</button></div></div></section>
</section>
<section class="card"><h2>What a complete entry needs</h2>
<label class="check"><input type="checkbox" checked disabled>Title (required, always on)</label>
${OPTIONAL_FIELDS.map(f => `<label class="check"><input type="checkbox" data-req="${f}"${required.includes(f) ? ' checked' : ''}${lockReason ? ' disabled' : ''}>${ui.esc(labelList([f])[0])}</label>`).join('')}
<p class="help">Changes save as you tick.</p>
</section>
<section class="card"><h2>Extensions</h2>
${extendedTeams().length ? extendedTeams().map(t => extensionRow(t, !!lockReason)).join('') : '<p class="muted">No team has an extension.</p>'}
<div class="field"><label>Team<select class="select" id="ext-team"${lockReason ? ' disabled' : ''}>${teams.map(t => `<option value="${ui.esc(t.slug)}">${ui.esc(t.name)}</option>`).join('')}</select></label></div>
<div class="field"><label>More time until · ${ui.esc(ev.time_zone || 'UTC')}<input class="input" id="ext-input" type="datetime-local"${ev.submission_deadline && !lockReason ? '' : ' disabled'}></label></div>
<button class="btn" type="button" id="ext-save"${ev.submission_deadline && !lockReason ? '' : ' disabled'}>Give this team more time</button>
${ev.submission_deadline ? '' : '<p class="help">Set the event deadline first; a team’s own deadline extends it.</p>'}
</section>
<h2 class="section-title">Entries</h2>
${entries.length ? `<div class="list">${entries.map(entryRow).join('')}</div>` : '<div class="note">No entries yet. They’ll show up here once teams start filling theirs in.</div>'}
<section class="card"><h2>Storage</h2><p>This event’s team sites use ${usage ? ui.esc(fmtBytes((usage.saved_data_bytes || 0) + (usage.history_bytes || 0) + (usage.screenshot_bytes || 0) + (usage.file_bytes || 0))) : '…'}</p></section>
<section class="card"><h2>Export</h2><button class="btn" type="button" id="export-entries">Download entries CSV</button></section>`
    wire()
  }

  function wire() {
    $('deadline-save').addEventListener('click', onSaveDeadline)
    $('deadline-clear').addEventListener('click', onClearDeadline)
    main.querySelectorAll('[data-req]').forEach(cb => cb.addEventListener('change', () => onToggleRequired(cb)))
    main.querySelectorAll('[data-clear-ext]').forEach(btn => btn.addEventListener('click', () => onClearExtension(btn.getAttribute('data-clear-ext'))))
    $('ext-save').addEventListener('click', onGiveExtension)
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
    const ok = await ui.confirm({ title: 'Remove the extension?', body: name + ' reverts to the event’s general deadline, which may already have passed. Clear the extension?', confirmLabel: 'Clear extension', danger: true })
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
