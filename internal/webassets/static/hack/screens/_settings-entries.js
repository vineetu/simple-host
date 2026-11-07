import { href, resolve } from '../nav.js'

// Client-side mirror of the server's own table (internal/rules/hackentries/
// entries.go Presets), which follows the atlas's "What do the presets set?"
// decision. Used to show each preset's lines straight away when a card is
// picked and to tell when the lines still match a preset. The server is the
// one place that actually expands and stores a preset.
//
// SIM-382 (owner, 2026-11-05/06): exactly these two now, both naming what
// teams hand in. AI build, Coding, Ideas and design and Custom are gone from
// this picker -- an event already on one of them keeps its own rules (read
// from `rules.fields` below, same as always) and isn't shown as a selected
// card here; picking one of these two switches it over.
const PRESETS = {
  video_or_website: {
    label: 'Video or website', desc: 'Teams show a demo video or a published website -- at least one of the two.',
    fields: { title: 'required', problem: 'required', tagline: 'required', audience: 'required', track: 'required', tools: 'required', maturity: 'required', description: 'optional', video_url: 'one_of', site: 'one_of', code_url: 'off', screenshot: 'optional' },
    judge_first: 'video'
  },
  video_website_code: {
    label: 'Video, website and code link', desc: 'Teams show a demo video, a published website and a code link -- all three required.',
    fields: { title: 'required', problem: 'required', tagline: 'required', audience: 'required', track: 'required', tools: 'required', maturity: 'required', description: 'optional', video_url: 'required', site: 'required', code_url: 'required', screenshot: 'optional' },
    judge_first: 'site'
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

// Reminder times (notices contract 2026-11-05, CHANGE 2026-11-05): one
// offsets_minutes list drives both the entry-reminder emails to teams and
// the judging-reminder emails to judges. "3 days", "1 day", "2 hours",
// "90 minutes" -- minutes only when the value isn't a whole hour or day,
// so the common 1440/60 pair still reads as "1 day"/"1 hour".
function offsetLabel (m) {
  if (m >= 1440 && m % 1440 === 0) { const d = m / 1440; return d + (d === 1 ? ' day' : ' days') }
  if (m >= 60 && m % 60 === 0) { const h = m / 60; return h + (h === 1 ? ' hour' : ' hours') }
  return m + (m === 1 ? ' minute' : ' minutes')
}

// Existing entry/deadline and reminder editors; save/confirmation semantics are unchanged.
export async function render(ctx, section) {
 const { main, api: client, ui, event, role } = ctx
 const api = client.api, slug = event.event.slug, esc = ui.esc
 const $ = id => main.querySelector('#' + id), setLive = ui.live(main)
 let ev = event.event
 let rules = { fields: {}, editable: false }, lastPresetChosen = null, dirty = false
 let reminders = { offsets_minutes: [] }, reminderDraft = []
 const rulesPath = '/v1/hack/events/' + encodeURIComponent(slug) + '/entry-rules'
 const remindersPath = '/v1/hack/events/' + encodeURIComponent(slug) + '/reminders'
 async function load() {
  const reminderDraftWasDirty = JSON.stringify(reminderDraft) !== JSON.stringify(reminders.offsets_minutes || [])
  const [rr, remr] = await Promise.all([api('GET', rulesPath), api('GET', remindersPath)])
  if (!ctx.alive()) return
  if (rr.ok) { rules = rr.data; lastPresetChosen = rules.preset !== 'custom' ? rules.preset : lastPresetChosen }
  if (remr.ok) { reminders = remr.data; if (!reminderDraftWasDirty) reminderDraft = (reminders.offsets_minutes || []).slice() }
 }
 function paint() {
  if (!ctx.alive()) return
  const deadlineInput = ui.localInputValue(ev.submission_deadline, ev)
  const closedStages = ['closed', 'judging', 'results']
  const lockReason = ui.writeLockReason(ev, event.admin_view), archived = ev.stage === 'archived'
  const rulesLockReason = lockReason || (rules.editable === false ? "Entries are closed, so these can't change. To reopen, move the event back to Building on Home." : '')
  if (section === 'reminders') {
   main.innerHTML = remindersHTML(lockReason)
   wireReminders(lockReason)
  } else {
   main.innerHTML = `<section class="card" id="deadline"><h2>Deadline</h2>
${archived
    ? `<p>${ev.submission_deadline ? 'Entries closed ' + esc(ui.dateTime(ev.submission_deadline, ev)) : 'No deadline was set.'}</p>`
    : `<div class="field"><label>Entries close · ${esc(ev.time_zone || 'UTC')}<input class="input" id="deadline-input" type="datetime-local" value="${esc(deadlineInput)}"${lockReason ? ' disabled' : ''}></label><p class="help">${ev.submission_deadline ? 'Entries close ' + esc(ui.dateTime(ev.submission_deadline, ev)) + '.' : 'No deadline yet.'} At the deadline, drafts with nothing missing are submitted for their teams. Drafts still missing something are judged too, marked Incomplete. You can exclude one below.</p></div>
${closedStages.includes(ev.stage) ? '<p>To reopen, move the event back to Building on Overview.</p>' : ''}
<div class="form-actions"><button class="btn primary" type="button" id="deadline-save"${lockReason ? ' disabled' : ''}>Save deadline</button><button class="btn quiet" type="button" id="deadline-clear"${ev.submission_deadline && !lockReason ? '' : ' disabled'}>Clear deadline</button></div>`}
</section>
<div id="entry-rules">${handInHTML(rulesLockReason)}</div>`
   $('deadline-save')?.addEventListener('click', onSaveDeadline)
   $('deadline-clear')?.addEventListener('click', onClearDeadline)
   if (!rulesLockReason) wireHandIn()
  }
 }
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
<div class="field-group"><h3>Judges see</h3><div class="field inline"><span class="lab">After the card, first</span>${judgeFirstSeg}</div><p class="help">Every judge sees the summary card first. Video, website and code link events usually open the website first.</p></div>
<div class="form-actions"><button class="btn primary" type="button" id="rules-save"${disabled ? ' disabled' : ''}>Save</button><span class="hint" id="rules-hint">${rules.preset === 'custom' ? esc(currentPresetLabel()) : ''}</span></div>
</section>`
  }

  // --- Reminder times -----------------------------------------------------

  function remindersHTML (lockReason) {
    const chips = reminderDraft.map(m => `<span class="tag" data-off="${m}">${esc(offsetLabel(m))} before<button type="button" aria-label="Remove the ${esc(offsetLabel(m))} reminder" data-rem-off="${m}"${lockReason ? ' disabled' : ''}>×</button></span>`).join('')
    const judgingLine = reminders.judging_ends_at
      ? 'Judging closes ' + esc(ui.dateTime(reminders.judging_ends_at, ev)) + '.'
      : `Set when judging closes on <a href="${esc(resolve('o.judging-closes', slug).href)}">Settings → Judging</a> to remind judges.`
    return `<section class="card" id="reminders"><h2>Reminder times</h2>
<p class="help">Teams without a submitted entry are emailed this long before their deadline. Judges with teams left to score are reminded this long before judging closes.</p>
<div class="tag-input" id="rem-chips" role="group" aria-label="Reminder times">${chips || '<span class="muted">No reminders set.</span>'}</div>
${lockReason ? '' : `<div class="field-row" style="margin-top:12px">
<div class="field"><label for="rem-n">Add a reminder</label><input class="input" id="rem-n" type="number" min="1" inputmode="numeric" style="max-width:110px"></div>
<div class="field"><label for="rem-unit">Unit</label><select class="select" id="rem-unit"><option value="days" selected>days before</option><option value="hours">hours before</option><option value="minutes">minutes before</option></select></div>
</div>
<div class="form-actions"><button class="btn" type="button" id="rem-add-btn"${reminderDraft.length >= reminders.max_count ? ' disabled' : ''}>Add</button></div>
<p class="err" id="rem-err"></p>
<p class="help">Up to ${reminders.max_count}, each between ${reminders.min_minutes} minutes and 14 days before the deadline.</p>`}
<p class="help">${judgingLine}</p>
${lockReason ? '' : `<div class="card-foot"><span class="hint" id="rem-hint">No unsaved changes</span><button class="btn quiet" type="button" id="rem-discard" disabled>Discard</button><button class="btn primary" type="button" id="rem-save" disabled>Save</button></div>`}
</section>`
  }

  // paintReminders repaints only the Reminder times card, in place -- a
  // full paint() would rebuild #deadline-input's value from `ev` and
  // throw away whatever the organiser was mid-typing there (point 8, the
  // other direction from load()'s own fix above). Falls back to a full
  // paint() if the card isn't on the page for some reason.
  function paintReminders () {
    const card = $('reminders')
    if (!card) { paint(); return }
    const lockReason = ui.writeLockReason(ev, event.admin_view)
    card.outerHTML = remindersHTML(lockReason)
    wireReminders(lockReason)
  }

  function wireReminders (lockReason) {
    if (lockReason) return
    const err = $('rem-err')
    function sortedCopy (arr) { return arr.slice().sort((a, b) => b - a) }
    function syncDirty () {
      const dirty2 = JSON.stringify(sortedCopy(reminderDraft)) !== JSON.stringify(sortedCopy(reminders.offsets_minutes || []))
      $('rem-hint').textContent = dirty2 ? 'Unsaved changes' : 'No unsaved changes'
      $('rem-hint').classList.toggle('dirty', dirty2)
      $('rem-discard').disabled = !dirty2
      $('rem-save').disabled = !dirty2
    }
    main.querySelectorAll('[data-rem-off]').forEach(btn => btn.addEventListener('click', () => {
      const m = Number(btn.getAttribute('data-rem-off'))
      reminderDraft = reminderDraft.filter(x => x !== m)
      paintReminders()
    }))
    const addBtn = $('rem-add-btn')
    if (addBtn) addBtn.addEventListener('click', () => {
      err.textContent = ''
      const nRaw = $('rem-n').value.trim()
      const unit = $('rem-unit').value
      const n = Number(nRaw)
      if (!nRaw || !Number.isInteger(n) || n <= 0) { err.textContent = 'Enter a whole number.'; return }
      const mult = unit === 'days' ? 1440 : unit === 'hours' ? 60 : 1
      const minutes = n * mult
      if (minutes < reminders.min_minutes || minutes > reminders.max_minutes) { err.textContent = 'Each reminder must be between ' + reminders.min_minutes + ' minutes and 14 days before the deadline.'; return }
      if (reminderDraft.includes(minutes)) { err.textContent = 'Already have a reminder at that time.'; return }
      if (reminderDraft.length >= reminders.max_count) { err.textContent = 'Up to ' + reminders.max_count + ' reminders.'; return }
      reminderDraft = reminderDraft.concat(minutes).sort((a, b) => b - a)
      paintReminders()
    })
    const discardBtn = $('rem-discard')
    if (discardBtn) discardBtn.addEventListener('click', () => { reminderDraft = (reminders.offsets_minutes || []).slice(); paintReminders() })
    const saveBtn = $('rem-save')
    if (saveBtn) saveBtn.addEventListener('click', onSaveReminders)
    syncDirty()
  }

  async function onSaveReminders () {
    const btn = $('rem-save')
    btn.disabled = true
    let r
    try { r = await api('PUT', remindersPath, { offsets_minutes: reminderDraft }) }
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', true); btn.disabled = false; return }
    if (!r.ok) { setLive(ui.errText(r, { fallback: 'Could not save reminder times.' }), true); btn.disabled = false; return }
    reminders = r.data
    reminderDraft = (reminders.offsets_minutes || []).slice()
    paintReminders()
    ui.saved(main)
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
    // SIM-382: preset is sent only when it's one of the two current ones
    // (the PRESETS card keys) -- never a retired preset (build/code/ideas)
    // or custom, both of which the server now refuses outright. An event
    // still on a retired preset that the organiser hasn't switched away
    // from saves fields/judge_first alone, same as a custom config always
    // has; the stored preset stays whatever it already was unless the
    // fields now differ from its own table (server-side demotion, as
    // before). dry_run uses this exact same body so it previews -- and
    // would be refused by -- exactly what the real save sends.
    const base = { fields: rules.fields, judge_first: rules.judge_first }
    if (Object.prototype.hasOwnProperty.call(PRESETS, rules.preset)) base.preset = rules.preset
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
    let r
    try { r = await api('PUT', rulesPath, base) }
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', true); if (saveBtn) saveBtn.disabled = false; return }
    if (!r.ok) { setLive(r.data.error || 'Could not save', true); if (saveBtn) saveBtn.disabled = false; return }
    rules = r.data || rules
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


 await load()
 paint()
}
