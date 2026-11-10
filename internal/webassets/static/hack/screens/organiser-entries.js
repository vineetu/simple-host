import { href, resolve } from '../nav.js'
import { fmtBytes } from './_orgB-util.js'
import { wantsSection } from '../platform.js'

export function title() { return 'Entries' }

// Rebuilt for the atlas "entries and participant journey" package
// (2026-11-02, slice 1): counts, the three presets and the per-line rules
// editor (Required / Optional / Off, plus One of on the show-your-work
// lines) with a dry-run-then-confirm Save, and the Incomplete flag with a
// hand Exclude.
//
// CONTRACT (coordinator decision, 2026-11-02 -- see participant-entry.js for
// the full note): the deadline sweep settles an incomplete draft as
// `status: "not_submitted"` -- still judged, flagged Incomplete, never
// hidden. Organisers exclude one by hand, with a confirm, here on each
// flagged row or on organiser-entry-detail.js (POST .../teams/{team}/entry/
// exclude and /include).

const PLAIN = {
  title: 'project name', problem: 'the problem', tagline: 'the solution', audience: "who it's for",
  track: 'a track', tools: 'tools and AI', maturity: 'what works today',
  video_url: 'a video link', site: 'a published website', code_url: 'a code link', screenshot: 'a screenshot', description: 'a description',
  one_of: 'a video or website'
}
function joinAnd (items) {
  if (!items.length) return ''
  if (items.length === 1) return items[0]
  return items.slice(0, -1).join(', ') + ' and ' + items[items.length - 1]
}
function labelList (fields) { return fields.map(f => PLAIN[f] || f) }

export async function render (ctx) {
  const { main, api: client, ui, event, role } = ctx
  const api = client.api
  const esc = ui.esc
  const slug = event.event.slug
  const $ = id => main.querySelector('#' + id)
  const setLive = ui.live(main)

  let ev = event.event
  let teams = [], entries = [], counts = {}
  let usage = null
  const entriesPath = '/v1/hack/events/' + encodeURIComponent(slug) + '/entries'

  async function load () {
    const [tr, er, ur] = await Promise.all([
      api('GET', '/v1/hack/events/' + encodeURIComponent(slug) + '/teams'),
      api('GET', entriesPath),
      api('GET', '/v1/hack/events/' + encodeURIComponent(slug) + '/usage')
    ])
    if (tr.ok) teams = tr.data.teams || []
    if (er.ok) { entries = er.data.entries || []; counts = er.data.counts || {} } else setLive(er.data.error || 'Could not load entries', true)
    if (ur.ok) usage = ur.data

  }

  function teamBySlug (slug2) { return teams.find(t => t.slug === slug2) }
  function extendedTeams () { return teams.filter(t => t.extended) }

  // No single field says "never touched" (build-plan.md's entryListItem
  // doesn't define one) -- derived from every line the form could have
  // filled being empty, the same lines fill-entry-summary-card writes.
  function hasAnyContent (e) {
    return !!(e.title || e.problem || e.tagline || e.audience || e.maturity || (e.tools && e.tools.length) || e.video_url || e.code_url || e.has_screenshot)
  }

  // Coordinator decision (2026-11-02): `status: "not_submitted"` on an
  // entry the deadline sweep settled is the Incomplete case now -- still
  // judged unless `excluded`, never hidden the way build-plan.md's
  // original "not submitted, not judged" rule would have read it.
  function statusPill (e) {
    if (e.excluded) return '<span class="pill bad">Excluded</span>'
    if (e.status === 'submitted') return '<span class="pill ok">Submitted</span>'
    if (e.incomplete) return '<span class="pill warn">Incomplete</span>'
    if (!hasAnyContent(e)) return '<span class="pill">Not started</span>'
    return '<span class="pill warn">Draft</span>'
  }

  function entryRow (e) {
    const bits = []
    if (e.status === 'submitted') bits.push('Submitted' + (e.submitted_at ? ' ' + ui.dateTime(e.submitted_at, ev) : ''))
    else if (e.missing && e.missing.length) bits.push('Missing: ' + labelList(e.missing).join(', '))
    else if (hasAnyContent(e)) bits.push('Nothing missing')
    else bits.push('No entry yet')
    const t = teamBySlug(e.team.slug)
    if (t && t.extended) bits.push('More time until ' + ui.dateTime(t.deadline_override, ev))
    bits.push(e.site_exists ? (e.site_taken_down ? 'Site taken down' : 'Site live') : 'No site yet')
    const detail = href(role, slug, 'entry') + '&team=' + encodeURIComponent(e.team.slug)
    // A flagged (Incomplete) entry is still judged. The organiser can take
    // it out of judging by hand, once they confirm it (coordinator decision
    // 2026-11-02), and put it back the same way.
    const lock = ui.writeLockReason(ev, event.admin_view)
    let act = ''
    if (e.excluded) act = `<button class="btn sm" type="button" data-include="${esc(e.team.slug)}"${lock ? ' disabled' : ''}>Include</button>`
    else if (e.incomplete) act = `<button class="btn danger sm" type="button" data-exclude="${esc(e.team.slug)}" data-name="${esc(e.team.name)}"${lock ? ' disabled' : ''}>Exclude</button>`
    if (!act) return `<a class="r" href="${esc(detail)}"><span class="main"><b>${esc(e.team.name)}</b><span>${esc(bits.join(' · '))}</span></span>${statusPill(e)}</a>`
    return `<div class="r"><a class="main" href="${esc(detail)}"><b>${esc(e.team.name)}</b><span>${esc(bits.join(' · '))}</span></a><span class="r-end">${statusPill(e)}${act}</span></div>`
  }

  function extensionRow (t, locked, archived) {
    const text = esc(t.name) + ' · More time until ' + esc(ui.dateTime(t.deadline_override, ev))
    if (archived) return `<p>${text}</p>`
    return `<p>${text} <button class="btn danger sm" type="button" data-clear-ext="${esc(t.slug)}"${locked ? ' disabled' : ''}>Clear this team’s extension</button></p>`
  }

  // --- "What teams hand in" --------------------------------------------

  // --- Paint --------------------------------------------------------------

  let extensionForm
  function paint () {
    const closedStages = ['closed', 'judging', 'results']
    const lockReason = ui.writeLockReason(ev, event.admin_view)
    const archived = ev.stage === 'archived'
    main.innerHTML = `<div class="ph"><div class="t"><h1>Entries</h1><p>Review entries and give teams more time.</p></div></div>
${lockReason ? `<div class="note warn">${esc(lockReason)}</div>` : ''}
<div class="stat-row">
  <div class="stat"><b>${counts.submitted || 0}</b><span>submitted</span></div>
  <div class="stat"><b>${counts.drafts || 0}</b><span>drafts</span></div>
  <div class="stat"><b>${counts.not_started || 0}</b><span>not started</span></div>
  ${counts.not_submitted ? `<div class="stat"><b>${counts.not_submitted}</b><span>incomplete</span></div>` : ''}
  <div class="stat"><b>${counts.sites_live || 0}</b><span>sites live</span></div>
</div>
<p><a href="${esc(resolve('o.entry-rules', slug).href)}">Entry settings</a> · <a href="${esc(resolve('o.reminders', slug).href)}">Reminders</a></p>
<section class="card" id="ext"><h2>Extensions</h2>
${extendedTeams().length ? extendedTeams().map(t => extensionRow(t, !!lockReason, archived)).join('') : (ev.submission_deadline && !archived ? '<p class="muted">No team has an extension.</p>' : '')}
${archived ? '' : (ev.submission_deadline
    ? `<div class="field-row"><div class="field"><label>Team<select class="select" id="ext-team"${lockReason ? ' disabled' : ''}>${teams.map(t => `<option value="${esc(t.slug)}">${esc(t.name)}</option>`).join('')}</select></label></div>
<div class="field"><label>More time until · ${esc(ev.time_zone || 'UTC')}<input class="input" id="ext-input" type="datetime-local"${lockReason ? ' disabled' : ''}></label></div></div>
<div class="form-actions"><button class="btn" type="button" id="ext-save"${lockReason ? ' disabled' : ''}>Give this team more time</button></div>`
    : '<p class="help">Set the event deadline first. Then you can give one team more time.</p>')}
</section>
<h2 class="section-title">Entries · ${teams.length} team${teams.length === 1 ? '' : 's'}</h2>
${entries.length ? `<div class="list">${entries.map(entryRow).join('')}</div>` : '<div class="list"><div class="empty">No entries yet. They show up here once teams start filling theirs in.</div></div>'}
<section class="card"><h2>Export</h2><div class="form-actions"><button class="btn" type="button" id="export-entries">Download entries (CSV)</button></div><p class="help">This event’s team sites use ${usage ? esc(fmtBytes((usage.saved_data_bytes || 0) + (usage.history_bytes || 0) + (usage.screenshot_bytes || 0) + (usage.file_bytes || 0))) : '…'}</p></section>`
    wire()
    extensionForm = ui.trackForm(ctx, $('ext'))
  }

  function wire () {
    main.querySelectorAll('[data-clear-ext]').forEach(btn => btn.addEventListener('click', () => onClearExtension(btn.getAttribute('data-clear-ext'))))
    const extSave = $('ext-save')
    if (extSave) extSave.addEventListener('click', onGiveExtension)
    main.querySelectorAll('[data-exclude]').forEach(btn => btn.addEventListener('click', () => onExclude(btn.getAttribute('data-exclude'), btn.getAttribute('data-name'))))
    main.querySelectorAll('[data-include]').forEach(btn => btn.addEventListener('click', () => onInclude(btn.getAttribute('data-include'))))
    $('export-entries').addEventListener('click', () => client.downloadFile('/v1/hack/events/' + encodeURIComponent(slug) + '/export/entries.csv', slug + '-entries.csv', $('export-entries')))
  }

  async function onExclude (teamSlug, name) {
    setLive('')
    const ok = await ui.confirm({
      title: 'Exclude ' + (name || 'this entry') + ' from judging?',
      body: 'Judges stop seeing it and its scores stop counting. The team still sees its own entry. You can include it again any time.',
      confirmLabel: 'Exclude',
      danger: true
    })
    if (!ok) return
    let r
    try { r = await api('POST', '/v1/hack/events/' + encodeURIComponent(slug) + '/teams/' + encodeURIComponent(teamSlug) + '/entry/exclude', {}) }
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', true); return }
    if (!r.ok) { setLive(r.data.error || 'Could not exclude this entry', true); return }
    await load()
    paint()
    ui.saved(main)
  }

  async function onInclude (teamSlug) {
    setLive('')
    let r
    try { r = await api('POST', '/v1/hack/events/' + encodeURIComponent(slug) + '/teams/' + encodeURIComponent(teamSlug) + '/entry/include', {}) }
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', true); return }
    if (!r.ok) { setLive(r.data.error || 'Could not include this entry', true); return }
    await load()
    paint()
    ui.saved(main)
  }

  async function onGiveExtension () {
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

  async function onClearExtension (teamSlug) {
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

  // Deep link from organiser-entry-detail.js's "Give more time" (?team=<slug>):
  // preselect that team in Extensions and bring it into view, instead of
  // leaving the organiser to find it again in a long team list.
  const wantTeam = ctx.params.get('team')
  if (wantTeam) {
    const extTeam = $('ext-team')
    if (extTeam && teams.some(t => t.slug === wantTeam)) {
      extTeam.value = wantTeam
      extensionForm.reset()
    }
    const extSection = $('ext')
    if (extSection) extSection.scrollIntoView({ behavior: 'smooth', block: 'start' })
  }

  // Deep link from Overview's "Choose what teams hand in" first step
  // (SIM-382, #handin): bring the card into view instead of leaving the
  // organiser to scroll past Deadline and Reminder times to find it.
  if (wantsSection('handin')) $('handin')?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}
