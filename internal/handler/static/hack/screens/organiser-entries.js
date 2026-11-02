import { href } from '../nav.js'
import { fmtBytes } from './_orgB-util.js'

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

// Client-side mirror of the server's own table (internal/handler/
// hack_entry_rules.go entryPresets), which follows the atlas's "What do the
// presets set?" decision. Used to show each preset's lines straight away
// when a card is picked and to tell when the lines still match a preset.
// The server is the one place that actually expands and stores a preset.
const PRESETS = {
  build: {
    label: 'AI build', desc: 'Teams build with AI or no-code tools. Video or website.',
    fields: { title: 'required', problem: 'required', tagline: 'required', audience: 'required', track: 'required', tools: 'required', maturity: 'required', description: 'optional', video_url: 'one_of', site: 'one_of', code_url: 'off', screenshot: 'optional' },
    judge_first: 'video'
  },
  code: {
    label: 'Coding', desc: 'Adds a required code link. Judges open the website first.',
    fields: { title: 'required', problem: 'required', tagline: 'required', audience: 'optional', track: 'required', tools: 'required', maturity: 'required', description: 'optional', video_url: 'one_of', site: 'one_of', code_url: 'required', screenshot: 'optional' },
    judge_first: 'site'
  },
  ideas: {
    label: 'Ideas and design', desc: 'No build needed. Video, website or a screenshot.',
    fields: { title: 'required', problem: 'required', tagline: 'required', audience: 'required', track: 'required', tools: 'optional', maturity: 'optional', description: 'optional', video_url: 'one_of', site: 'one_of', code_url: 'off', screenshot: 'one_of' },
    judge_first: 'video'
  }
}
const SUMMARY_ROWS = [
  { id: 'problem', label: 'Problem, in one line' },
  { id: 'tagline', label: 'Solution, in one line', help: 'Also the line on the team sites list' },
  { id: 'audience', label: "Who it's for" },
  { id: 'track', label: 'Track' },
  { id: 'tools', label: 'Tools and AI', help: 'Tags, with suggestions' },
  { id: 'maturity', label: 'What works today', help: 'Demo-ready, partly working or concept' }
]
const SHOW_ROWS = [
  { id: 'video_url', label: 'Video link', help: "Plays on the judge's screen when it's YouTube, Vimeo, Loom or Drive" },
  { id: 'site', label: 'Website', help: 'The team site, published from an AI app or a zip' },
  { id: 'code_url', label: 'Code link', help: 'GitHub or similar' },
  { id: 'screenshot', label: 'Screenshot' }
]
const SUMMARY_OPTS = [{ v: 'required', t: 'Required' }, { v: 'optional', t: 'Optional' }, { v: 'off', t: 'Off' }]
const SHOW_OPTS = [{ v: 'required', t: 'Required' }, { v: 'one_of', t: 'One of' }, { v: 'optional', t: 'Optional' }, { v: 'off', t: 'Off' }]

function fieldsEqual (a, b) {
  const keys = new Set([...Object.keys(a || {}), ...Object.keys(b || {})])
  for (const k of keys) { if ((a[k] || 'optional') !== (b[k] || 'optional')) return false }
  return true
}

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
  let rules = { preset: 'build', fields: {}, judge_first: 'video', summary: '', has_tracks: !!(ev.tracks && ev.tracks.length), editable: true }
  // Remembered only for this page visit: which preset card was last chosen,
  // so a config that still matches it can read "Custom (from X)" instead of
  // a bare "Custom" the moment one line is nudged off it. The server has no
  // field for this lineage, only the bare `preset` id (plan section 4.2).
  let lastPresetChosen = null
  let dirty = false

  const entriesPath = '/v1/hack/events/' + encodeURIComponent(slug) + '/entries'
  const rulesPath = '/v1/hack/events/' + encodeURIComponent(slug) + '/entry-rules'

  async function load () {
    const [tr, er, ur, rr] = await Promise.all([
      api('GET', '/v1/hack/events/' + encodeURIComponent(slug) + '/teams'),
      api('GET', entriesPath),
      api('GET', '/v1/hack/events/' + encodeURIComponent(slug) + '/usage'),
      api('GET', rulesPath)
    ])
    if (tr.ok) teams = tr.data.teams || []
    if (er.ok) { entries = er.data.entries || []; counts = er.data.counts || {} } else setLive(er.data.error || 'Could not load entries', true)
    if (ur.ok) usage = ur.data
    if (rr.ok) { rules = rr.data; lastPresetChosen = rules.preset !== 'custom' ? rules.preset : lastPresetChosen }
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
    if (e.status === 'not_submitted') return '<span class="pill warn">Incomplete</span>'
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
    else if (e.status === 'not_submitted') act = `<button class="btn danger sm" type="button" data-exclude="${esc(e.team.slug)}" data-name="${esc(e.team.name)}"${lock ? ' disabled' : ''}>Exclude</button>`
    if (!act) return `<a class="r" href="${esc(detail)}"><span class="main"><b>${esc(e.team.name)}</b><span>${esc(bits.join(' · '))}</span></span>${statusPill(e)}</a>`
    return `<div class="r"><a class="main" href="${esc(detail)}"><b>${esc(e.team.name)}</b><span>${esc(bits.join(' · '))}</span></a>${statusPill(e)}${act}</div>`
  }

  function extensionRow (t, locked, archived) {
    const text = esc(t.name) + ' · More time until ' + esc(ui.dateTime(t.deadline_override, ev))
    if (archived) return `<p>${text}</p>`
    return `<p>${text} <button class="btn danger sm" type="button" data-clear-ext="${esc(t.slug)}"${locked ? ' disabled' : ''}>Clear this team’s extension</button></p>`
  }

  // --- "What teams hand in" --------------------------------------------

  function currentPresetLabel () {
    if (rules.preset !== 'custom') return (PRESETS[rules.preset] || {}).label || rules.preset
    const base = lastPresetChosen && PRESETS[lastPresetChosen]
    return base ? `Custom (from ${base.label})` : 'Custom'
  }

  function segHTML (row, opts, mode) {
    const spans = opts.map(o => `<span role="radio" tabindex="${o.v === mode ? '0' : '-1'}" aria-checked="${o.v === mode ? 'true' : 'false'}" class="${o.v === mode ? 'on ' : ''}${o.v === 'one_of' ? 'oneof' : ''}" data-val="${o.v}">${esc(o.t)}</span>`).join('')
    return `<div class="rr" data-field="${esc(row.id)}"><div class="t"><b>${esc(row.label)}</b>${row.help ? `<span>${esc(row.help)}</span>` : ''}</div><span class="seg" role="radiogroup" aria-label="${esc(row.label)}">${spans}</span></div>`
  }

  function handInHTML (rulesLockReason) {
    const disabled = !!rulesLockReason
    const mode = f => (rules.fields && rules.fields[f]) || 'optional'
    const summaryRows = SUMMARY_ROWS.filter(r => r.id !== 'track' || rules.has_tracks).map(r => {
      const row = r.id === 'track' ? { ...r, help: (ev.tracks || []).length + ' tracks in this event' } : r
      return segHTML(row, SUMMARY_OPTS, mode(r.id))
    }).join('')
    const showRows = SHOW_ROWS.map(r => segHTML(r, SHOW_OPTS, mode(r.id))).join('')
    const presetCards = Object.keys(PRESETS).map(id => {
      const p = PRESETS[id]
      const on = rules.preset === id
      return `<label class="choice${on ? ' on' : ''}${disabled ? ' off' : ''}"><input type="radio" name="org-preset" value="${id}"${on ? ' checked' : ''}${disabled ? ' disabled' : ''}><span><b>${esc(p.label)}</b><span class="d">${esc(p.desc)}</span></span></label>`
    }).join('')
    const judgeFirstSeg = `<span class="seg" role="radiogroup" aria-label="After the card, first" id="org-judge-first"><span role="radio" tabindex="0" aria-checked="${rules.judge_first !== 'site'}" class="${rules.judge_first !== 'site' ? 'on' : ''}" data-val="video">Video</span><span role="radio" tabindex="-1" aria-checked="${rules.judge_first === 'site'}" class="${rules.judge_first === 'site' ? 'on' : ''}" data-val="site">Website</span></span>`
    return `<section class="card" id="handin"><h2>What teams hand in</h2><p class="sub">Start from the kind of event, then change any line. Teams can't submit until everything Required is there.</p>
${rulesLockReason ? `<div class="note warn">${esc(rulesLockReason)}</div>` : ''}
<div class="choices presets" role="radiogroup" aria-label="What teams hand in">${presetCards}</div>
<div class="rules"><h3>Summary card</h3>
<div class="rr"><div class="t"><b>Project name</b><span>Always required</span></div><span class="pill">Required</span></div>
${summaryRows}
<h3>Show your work</h3>
${showRows}
<p class="help" style="margin-top:10px"><b>One of:</b> teams need at least one of the lines marked One of.${rules.summary ? ' Here: ' + esc(rules.summary) + '.' : ''}</p>
</div>
<div class="field-group"><h3>Judges see</h3><div class="field inline"><span class="lab">After the card, first</span>${judgeFirstSeg}</div><p class="help">Every judge sees the summary card first. Coding events usually open the website first.</p></div>
<div class="form-actions"><button class="btn primary" type="button" id="rules-save"${disabled ? ' disabled' : ''}>Save</button><span class="hint" id="rules-hint">${rules.preset === 'custom' ? esc(currentPresetLabel()) : ''}</span></div>
</section>`
  }

  // --- Paint --------------------------------------------------------------

  function paint () {
    const deadlineInput = ui.localInputValue(ev.submission_deadline, ev)
    const closedStages = ['closed', 'judging', 'results']
    const lockReason = ui.writeLockReason(ev, event.admin_view)
    const archived = ev.stage === 'archived'
    const rulesLockReason = lockReason || (closedStages.includes(ev.stage) ? "Entries are closed, so these can't change. To reopen, move the event back to Building on Overview." : '')
    main.innerHTML = `<div class="ph"><div class="t"><h1>Entries</h1><p>Set the deadline, say what teams hand in, and see who's ready.</p></div></div>
${lockReason ? `<div class="note warn">${esc(lockReason)}</div>` : ''}
<div class="stat-row">
  <div class="stat"><b>${counts.submitted || 0}</b><span>submitted</span></div>
  <div class="stat"><b>${counts.drafts || 0}</b><span>drafts</span></div>
  <div class="stat"><b>${counts.not_started || 0}</b><span>not started</span></div>
  ${counts.not_submitted ? `<div class="stat"><b>${counts.not_submitted}</b><span>incomplete</span></div>` : ''}
  <div class="stat"><b>${counts.sites_live || 0}</b><span>sites live</span></div>
</div>
<section class="card"><h2>Deadline</h2>
${archived
    ? `<p>${ev.submission_deadline ? 'Entries closed ' + esc(ui.dateTime(ev.submission_deadline, ev)) : 'No deadline was set.'}</p>`
    : `<div class="field"><label>Entries close · ${esc(ev.time_zone || 'UTC')}<input class="input" id="deadline-input" type="datetime-local" value="${esc(deadlineInput)}"${lockReason ? ' disabled' : ''}></label><p class="help">${ev.submission_deadline ? 'Entries close ' + esc(ui.dateTime(ev.submission_deadline, ev)) + '.' : 'No deadline yet.'} At the deadline, drafts with nothing missing are submitted for their teams. Drafts still missing something are judged too, marked Incomplete. You can exclude one below.</p></div>
${closedStages.includes(ev.stage) ? '<p>To reopen, move the event back to Building on Overview.</p>' : ''}
<div class="form-actions"><button class="btn primary" type="button" id="deadline-save"${lockReason ? ' disabled' : ''}>Save deadline</button><button class="btn quiet" type="button" id="deadline-clear"${ev.submission_deadline && !lockReason ? '' : ' disabled'}>Clear deadline</button></div>`}
</section>
${handInHTML(rulesLockReason)}
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
    wire(rulesLockReason)
  }

  function wire (rulesLockReason) {
    const saveBtn = $('deadline-save')
    if (saveBtn) saveBtn.addEventListener('click', onSaveDeadline)
    const clearBtn = $('deadline-clear')
    if (clearBtn) clearBtn.addEventListener('click', onClearDeadline)
    main.querySelectorAll('[data-clear-ext]').forEach(btn => btn.addEventListener('click', () => onClearExtension(btn.getAttribute('data-clear-ext'))))
    const extSave = $('ext-save')
    if (extSave) extSave.addEventListener('click', onGiveExtension)
    main.querySelectorAll('[data-exclude]').forEach(btn => btn.addEventListener('click', () => onExclude(btn.getAttribute('data-exclude'), btn.getAttribute('data-name'))))
    main.querySelectorAll('[data-include]').forEach(btn => btn.addEventListener('click', () => onInclude(btn.getAttribute('data-include'))))
    $('export-entries').addEventListener('click', () => client.downloadFile('/v1/hack/events/' + encodeURIComponent(slug) + '/export/entries.csv', slug + '-entries.csv', $('export-entries')))
    if (!rulesLockReason) wireHandIn()
  }

  function wireHandIn () {
    dirty = false
    const handin = $('handin')
    if (!handin) return

    // Preset cards: picking one replaces every line (after a confirm if any
    // line is currently dirty from an edit the preset would discard), the
    // server does the actual expansion.
    handin.querySelectorAll('input[name="org-preset"]').forEach(radio => {
      radio.addEventListener('change', async () => {
        if (!radio.checked) return
        if (dirty) {
          const ok = await ui.confirm({ title: 'Reset every line to ' + PRESETS[radio.value].label + '?', body: 'Your unsaved changes to this card are replaced.', confirmLabel: 'Reset' })
          if (!ok) { paint(); return }
        }
        lastPresetChosen = radio.value
        rules = { ...rules, preset: radio.value, fields: { ...PRESETS[radio.value].fields }, judge_first: PRESETS[radio.value].judge_first }
        paint()
      })
    })

    // Per-line Required/Optional/Off (or One of) segmented controls, with
    // arrow-key roving focus (build-plan.md section 6: ".seg radiogroups
    // with arrow-key support").
    function selectRadio (seg, val) {
      seg.querySelectorAll('[role=radio]').forEach(r => {
        const on = r.getAttribute('data-val') === val
        r.classList.toggle('on', on)
        r.setAttribute('aria-checked', on ? 'true' : 'false')
        r.setAttribute('tabindex', on ? '0' : '-1')
      })
    }
    handin.querySelectorAll('.rr[data-field]').forEach(row => {
      const field = row.getAttribute('data-field')
      const seg = row.querySelector('.seg')
      seg.addEventListener('click', e => {
        const r = e.target.closest('[role=radio]')
        if (!r) return
        rules = { ...rules, fields: { ...rules.fields, [field]: r.getAttribute('data-val') }, preset: fieldsEqual({ ...rules.fields, [field]: r.getAttribute('data-val') }, (PRESETS[rules.preset] || {}).fields || {}) ? rules.preset : 'custom' }
        selectRadio(seg, r.getAttribute('data-val'))
        dirty = true
        const hint = $('rules-hint')
        if (hint) hint.textContent = rules.preset === 'custom' ? currentPresetLabel() : ''
      })
      seg.addEventListener('keydown', e => {
        if (e.key !== 'ArrowRight' && e.key !== 'ArrowLeft' && e.key !== 'ArrowDown' && e.key !== 'ArrowUp') return
        e.preventDefault()
        const list = Array.from(seg.querySelectorAll('[role=radio]'))
        const i = list.indexOf(document.activeElement)
        const dir = (e.key === 'ArrowRight' || e.key === 'ArrowDown') ? 1 : -1
        const next = list[(i + dir + list.length) % list.length]
        next.focus()
        next.click()
      })
    })

    const jf = $('org-judge-first')
    if (jf) jf.addEventListener('click', e => {
      const r = e.target.closest('[role=radio]')
      if (!r) return
      rules = { ...rules, judge_first: r.getAttribute('data-val') }
      selectRadio(jf, r.getAttribute('data-val'))
      dirty = true
    })

    const saveBtn = $('rules-save')
    if (saveBtn) saveBtn.addEventListener('click', onSaveRules)
  }

  async function onSaveRules () {
    setLive('')
    const saveBtn = $('rules-save')
    if (saveBtn) saveBtn.disabled = true
    const base = { fields: rules.fields, judge_first: rules.judge_first }
    let dr
    try { dr = await api('PUT', rulesPath, { ...base, dry_run: true }) }
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', true); if (saveBtn) saveBtn.disabled = false; return }
    if (!dr.ok) { setLive(dr.data.error || 'Could not check the impact of this change', true); if (saveBtn) saveBtn.disabled = false; return }
    const impact = (dr.data && dr.data.impact) || {}
    if ((impact.submitted_missing || 0) > 0 || (impact.drafts_newly_blocked || 0) > 0) {
      const lines = []
      if (impact.submitted_missing) lines.push(impact.submitted_missing + ' submitted ' + (impact.submitted_missing === 1 ? 'entry' : 'entries') + (impact.submitted_missing === 1 ? ' does' : ' do') + ' not meet what you are about to ask for. They stay submitted, and their teams see that you now ask for more.')
      if (impact.drafts_newly_blocked) lines.push(impact.drafts_newly_blocked + ' draft' + (impact.drafts_newly_blocked === 1 ? '' : 's') + ' will need more before ' + (impact.drafts_newly_blocked === 1 ? 'it can' : 'they can') + ' submit.')
      const body = document.createElement('div')
      lines.forEach((t, i) => body.append(Object.assign(document.createElement('p'), { className: i === lines.length - 1 ? 'small muted' : '', textContent: t })))
      const ok = await ui.confirm({ title: 'Save these changes to what teams hand in?', body, confirmLabel: 'Save' })
      if (!ok) { if (saveBtn) saveBtn.disabled = false; return }
    }
    const body2 = { ...base }
    if (rules.preset !== 'custom') body2.preset = rules.preset
    let r
    try { r = await api('PUT', rulesPath, body2) }
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', true); if (saveBtn) saveBtn.disabled = false; return }
    if (!r.ok) { setLive(r.data.error || 'Could not save', true); if (saveBtn) saveBtn.disabled = false; return }
    rules = r.data || rules
    await load()
    paint()
    ui.saved(main)
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

  async function patchEv (body) {
    let r
    try { r = await api('PATCH', '/v1/hack/events/' + encodeURIComponent(slug), body) }
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', true); return null }
    if (!r.ok) { setLive(r.data.error || 'Could not save', true); return null }
    return r.data
  }

  async function onSaveDeadline () {
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

  async function onClearDeadline () {
    const ok = await ui.confirm({ title: 'Clear deadline?', body: 'Teams can keep changing sites and entries with no deadline until you set a new one. Clear it?', confirmLabel: 'Clear deadline', danger: true })
    if (!ok) return
    const data = await patchEv({ submission_deadline: '' })
    if (!data) return
    ev = data.event
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
    if (extTeam && teams.some(t => t.slug === wantTeam)) extTeam.value = wantTeam
    const extSection = $('ext')
    if (extSection) extSection.scrollIntoView({ behavior: 'smooth', block: 'start' })
  }
}
