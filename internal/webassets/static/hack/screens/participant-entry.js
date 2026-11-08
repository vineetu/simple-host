import { errText, isBlockedCode, safeHref } from './_partB-util.js'
import { href } from '../nav.js'

export function title () { return 'Entry' }

// Features on this screen: get-own-entry, edit-entry-fields,
// fill-entry-summary-card, tag-entry-tools, set-entry-maturity,
// choose-track-from-entry, submit-entry, move-entry-back-to-draft,
// auto-submit-complete-draft, preview-entry-as-judges-see-it,
// entry-edit-conflict-resolution, upload-entry-screenshot,
// remove-entry-screenshot, view-entry-read-only, view-rubric-participant.
//
// Rebuilt for the atlas "entries and participant journey" package
// (2026-11-02, slice 1): fixed summary-card fields (problem, solution/
// tagline, audience, track, tools, maturity), a per-event rules engine
// (Required / Optional / Off, plus One of on the show-your-work lines),
// and an explicit Submit step.
//
// CONTRACT (coordinator decision, 2026-11-02, replacing build-plan.md
// section 11's "an incomplete draft at the deadline is Not submitted and is
// not judged"): a complete draft is still auto-submitted; an incomplete
// draft is now ALSO settled at the deadline, as `status: "not_submitted"`
// -- still judged, unless an organiser excludes it by hand on Entries
// (which this view has no visibility into). There is no separate
// "incomplete" flag: `status === 'not_submitted'` on an entry that has any
// content (hasAnyContent below) IS the Incomplete case; `status ===
// 'not_submitted'` with nothing in it at all is a team that never started
// one. This matches the landed backend (internal/handler/hack_entry_rules.go,
// internal/db/hack_entries.go EventEntry.Status()) read while building this
// screen.

// Audit item 1 (2026-11-02): "How you're judged" card, the rubric a judge
// or organiser already sees -- names, descriptions and max points, never a
// score. Mirrors judge-rubric.js's markup (.card/.crit/.row-flex/.pill/.sub)
// so the two read the same way. Missing or empty is silent (no card): the
// organiser may simply not have written one yet. It sits in the right-hand
// column under "How judges see it" (build-plan.md section 13, atlas
// participant-entry), and under the read-only card once entries close.
function rubricCardHTML (ui, criteria) {
  if (!criteria || !criteria.length) return ''
  const esc = ui.esc
  const total = criteria.reduce((s, c) => s + (Number(c.max_points) || 0), 0)
  const rows = criteria.map(c =>
    '<div class="crit"><div class="row-flex"><b>' + esc(c.name) + '</b><span class="pill">' + (Number(c.max_points) || 0) + ' points</span></div>' +
    (c.description ? '<p class="small muted">' + esc(c.description) + '</p>' : '') +
    '</div>'
  ).join('')
  return '<section class="card" id="en-rubric"><h2>How you’re judged</h2><p class="sub">' + criteria.length + ' ' + (criteria.length === 1 ? 'criterion' : 'criteria') + ' · ' + total + ' points total</p>' + rows + '</section>'
}

const SUMMARY_FIELDS = ['problem', 'tagline', 'audience', 'track', 'tools', 'maturity']
const SHOW_FIELDS = ['video_url', 'site', 'code_url', 'screenshot']
const LABEL = {
  title: 'Project name', problem: 'Problem, in one line', tagline: 'Solution, in one line',
  audience: "Who it's for", track: 'Track', tools: 'Tools and AI you used', maturity: 'What works today',
  video_url: 'Video link', site: 'Website', code_url: 'Code link', screenshot: 'Screenshot',
  description: "Anything else judges should know"
}
const PLAIN = {
  title: 'project name', problem: 'the problem, in one line', tagline: 'the solution, in one line',
  audience: "who it's for", track: 'a track', tools: 'tools and AI', maturity: 'what works today',
  video_url: 'a video link', site: 'a published website', code_url: 'a code link', screenshot: 'a screenshot', description: 'a description'
}
// The missing-items list names fields the way the form labels them, as a
// plain list (full-test fix 2026-11-02): "Missing: Problem, Solution, Track".
const FIELD_NAME = {
  title: 'Project name', problem: 'Problem', tagline: 'Solution', audience: "Who it's for",
  track: 'Track', tools: 'Tools and AI', maturity: 'What works today', description: 'Description',
  video_url: 'Video link', site: 'Website', code_url: 'Code link', screenshot: 'Screenshot'
}
const MAXLEN = { title: 80, problem: 140, tagline: 160, audience: 100, description: 5000 }
const CLOSED_TEXT = {
  submissions_closed: 'Entries are closed',
  team_site_taken_down: 'The organiser has taken your team’s site down.',
  event_taken_down: 'This event has been taken down.',
  event_closed: 'This event has ended.'
}

export async function render (ctx) {
  const { main, api: client, ui, event: detail } = ctx
  const api = client.api
  const esc = ui.esc
  const ev = detail.event

  main.innerHTML = '<div class="ph"><div class="t"><h1>Entry</h1><p>' + esc(ev.title) + '</p></div></div><p class="muted">Loading…</p>'

  let r
  try { r = await api('GET', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/entry') } catch (e) { if (client.isSignedOut(e)) return; r = { ok: false, data: {} } }
  if (!ctx.alive()) return
  if (!r.ok) {
    if (r.data && r.data.code === 'no_team') {
      main.innerHTML = '<div class="ph"><div class="t"><h1>Entry</h1><p>' + esc(ev.title) + '</p></div></div><div class="locked"><h2>Join or start a team first</h2><p><a class="btn" href="' + esc(href('participant', ev.slug, 'team')) + '">Go to Team</a></p></div>'
      return
    }
    main.innerHTML = '<div class="ph"><div class="t"><h1>Entry</h1><p>' + esc(ev.title) + '</p></div></div><div class="note bad" role="alert">' + esc(errText(r, 'Could not load your entry.')) + '</div>'
    return
  }

  // The rubric is a secondary card: if it fails to load, the entry still
  // renders and the card is simply left out. Kept on `main` so every
  // repaint (save, submit, conflict) puts it back without a second fetch.
  main.__enRubric = []
  try {
    const rr = await api('GET', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/rubric')
    if (rr.ok && rr.data && Array.isArray(rr.data.criteria)) main.__enRubric = rr.data.criteria
  } catch (e) { /* omitted, see above */ }
  if (!ctx.alive()) return

  paint(ctx, ev, r.data)
}

// `mode(rules, hasTracks, field)`: Required / Optional / Off / One of for one
// field, honouring "a missing key means Optional" and "title is forced
// Required" (build-plan.md 4.1).
function mode (rules, hasTracks, field) {
  if (field === 'title') return 'required'
  if (field === 'track' && !hasTracks) return 'off'
  const f = (rules && rules.fields) || {}
  return f[field] || 'optional'
}

function paint (ctx, ev, data) {
  const { main, api: client, ui } = ctx
  const api = client.api
  const esc = ui.esc
  const editable = data.editable !== false
  const entry = data.entry || {}
  const rules = data.rules || { fields: {}, summary: '', preset: '', judge_first: 'video' }
  const hasTracks = typeof rules.has_tracks === 'boolean' ? rules.has_tracks : !!(ev.tracks && ev.tracks.length)
  const status = data.status || 'draft'

  if (!editable) {
    paintReadOnly(ctx, ev, data, { rules, hasTracks, status })
    return
  }

  const missing = Array.isArray(data.missing) ? data.missing : []
  const me = ctx.event && ctx.event.me
  const entryDeadline = (me && me.team && me.team.deadline) || ev.submission_deadline
  const deadlineText = entryDeadline ? ui.dateTime(entryDeadline, ev) : ''

  const submittedAtText = data.submitted_at ? ui.dateTime(data.submitted_at, ev) : ''
  const head = '<div class="ph"><div class="t"><h1>Entry</h1><p>' + esc(ev.title) + '</p></div></div>'
  main.innerHTML = head + statusHTML(ui, status, data, { rules, deadlineText, submittedAtText }) + formHTML(ctx, ev, data, rules, hasTracks, missing, status)

  const submitBtn = main.querySelector('#en-submit')
  if (submitBtn) submitBtn.addEventListener('click', () => doSubmit(ctx, ev, data))
  const unsubmitBtn = main.querySelector('#en-unsubmit')
  if (unsubmitBtn) unsubmitBtn.addEventListener('click', () => doUnsubmit(ctx, ev, data))

  wireForm(ctx, ev, data, rules, hasTracks)
}

// --- Status block (Draft / Submitted / Incomplete) ------------------------

function missingLinks(ui, fields) {
  return fields.map(f => '<button type="button" class="field-link" data-field-focus="' + ui.esc(f) + '">' + ui.esc(FIELD_NAME[f] || f) + '</button>').join(', ')
}

function statusHTML (ui, status, data, { rules, deadlineText, submittedAtText }) {
  const esc = ui.esc
  const missing = Array.isArray(data.missing) ? data.missing : []
  const complete = !!data.complete
  const evidenceUnmet = missing.some(f => f === 'video_url' || f === 'site' || f === 'code_url' || f === 'screenshot' || f === 'one_of')
  const summaryUnmet = missing.some(f => SUMMARY_FIELDS.includes(f) || f === 'title' || f === 'description')
  const evidenceLabel = rules.summary || 'show your work'
  // The missing-items list: one line per part of the entry, ticked when
  // done, and naming exactly what is still missing when not.
  const summaryStep = ((data.progress && data.progress.steps) || []).find(x => x.id === 'summary') || {}
  const summaryMissing = missing.filter(f => SUMMARY_FIELDS.includes(f) || f === 'title' || f === 'description')
  const evidenceMissing = missing.filter(f => !summaryMissing.includes(f))
  const summaryText = 'Summary card' + (summaryStep.of ? ' · ' + (summaryStep.filled || 0) + ' of ' + summaryStep.of : '') +
    (summaryMissing.length ? ' · Missing: ' + missingLinks(ui, summaryMissing) : '')
  // The rules sentence already names the one-of group, so only lines it
  // doesn't name (a required code link or screenshot) are spelled out.
  const evidenceExtra = evidenceMissing.filter(f => f !== 'one_of' && !(f === 'code_url' && /code link/.test(evidenceLabel)))
  const evidenceText = esc(ucfirst(evidenceLabel)) + (evidenceExtra.length ? ' · Missing: ' + missingLinks(ui, evidenceExtra) : '')
  const need = '<ul class="need" id="en-need">' +
    '<li class="' + (summaryUnmet ? '' : 'ok') + '">' + summaryText + '</li>' +
    (rules.summary || evidenceMissing.length ? '<li class="' + (evidenceUnmet ? '' : 'ok') + '">' + evidenceText + '</li>' : '') +
    '</ul>'
  if (status === 'submitted') {
    if (!complete) {
      // Rules tightened after this team pressed Submit (build-plan.md 4.1
      // "submitted entries stay submitted..."): not the coordinator's
      // auto-submit-incomplete case, which never reaches this editable
      // branch (it is always past the deadline, read-only).
      return '<div class="entry-status submitted"><div class="t"><b>Submitted</b><span>The organisers now also ask for ' + esc(missing.map(f => PLAIN[f] || f).join(', ')) + '. Your entry stays submitted. Add it if you can.</span></div><button class="btn quiet" type="button" id="en-unsubmit">Move back to draft</button></div>'
    }
    const by = data.submit_auto ? '' : (data.submitted_by ? ' by ' + esc(data.submitted_by) : '')
    const lead = data.submit_auto ? 'Submitted automatically' : ('Submitted' + (submittedAtText ? ' · ' + esc(submittedAtText) : '') + by)
    return '<div class="entry-status submitted"><div class="t"><b>' + lead + '</b><span>Judges will see this. You can still change it until ' + esc(deadlineText || 'the deadline') + '. It stays submitted.</span></div><button class="btn quiet" type="button" id="en-unsubmit">Move back to draft</button></div>'
  }
  const title = complete ? 'Draft · ready to submit' : 'Draft · not submitted yet'
  // Coordinator decision (2026-11-02): at the deadline a complete draft is
  // submitted for the team, and an incomplete one is still judged, marked
  // incomplete. Both lines say so before it happens.
  const lines = ['Submit by ' + esc(deadlineText || 'the deadline') + '.']
  if (complete) lines.push('If you don’t, it is submitted for you at the deadline.')
  else lines.push('Not complete. It will still be judged, marked incomplete.')
  return '<div class="entry-status draft" id="en-status"><div class="t"><b>' + title + '</b><span>' + lines.join(' ') + '</span>' + need + '</div><button class="btn primary" type="button" id="en-submit">Submit entry</button></div>'
}

// --- Form -------------------------------------------------------------

function formHTML (ctx, ev, data, rules, hasTracks, missing, status) {
  const ui = ctx.ui
  const esc = ui.esc
  const entry = data.entry || {}
  const m = f => mode(rules, hasTracks, f)
  const FIELD_HELP = {
    problem: 'What goes wrong today, and for whom.',
    tagline: 'What you built. This line also shows on the team sites list.',
    video_url: 'YouTube, Vimeo, Loom or Google Drive play right on the judges’ screen. Other links open in a new tab.'
  }
  function textField (f, type) {
    if (m(f) === 'off') return ''
    const raw = String(entry[f] || '')
    const val = esc(raw)
    const max = MAXLEN[f]
    const help = FIELD_HELP[f] ? '<p class="help">' + esc(FIELD_HELP[f]) + '</p>' : ''
    return '<div class="field"><div class="lab-row"><label for="en-' + f + '">' + esc(LABEL[f]) + '</label>' +
      (max ? '<span class="counter" id="en-' + f + '-ctr">' + raw.length + ' / ' + max + '</span>' : '') + '</div>' +
      '<input class="input" id="en-' + f + '"' + (type ? ' type="' + type + '"' : '') + (max ? ' maxlength="' + max + '"' : '') + ' value="' + val + '">' +
      help + '<p class="err" id="en-' + f + '-err"></p></div>'
  }

  function trackField () {
    if (m('track') === 'off') return ''
    const opts = (ev.tracks || []).map(t => '<option value="' + esc(t.slug) + '"' + (entry.track && entry.track.slug === t.slug ? ' selected' : '') + '>' + esc(t.name) + '</option>').join('')
    return '<div class="field"><label for="en-track">' + esc(LABEL.track) + '</label><select class="select" id="en-track"><option value="">No track</option>' + opts + '</select><p class="help">Your team’s track. Changing it here changes it for the whole team.</p></div>'
  }

  function toolsField () {
    if (m('tools') === 'off') return ''
    const tools = Array.isArray(entry.tools) ? entry.tools : []
    const tagsHTML = tools.map((t, i) => '<span class="tag' + (ui.AI_SUGGESTIONS.some(a => a.toLowerCase() === String(t).toLowerCase()) ? ' ai' : '') + '" data-tag-i="' + i + '">' + esc(t) + '<button type="button" aria-label="Remove ' + esc(t) + '">×</button></span>').join('')
    const sugg = list => list.filter(s => !tools.some(t => t.toLowerCase() === s.toLowerCase())).map(s => '<button type="button" data-suggest="' + esc(s) + '">+ ' + esc(s) + '</button>').join('')
    return '<div class="field"><span class="lab" id="en-tools-l">' + esc(LABEL.tools) + '</span>' +
      '<div class="tag-input" role="group" aria-labelledby="en-tools-l" id="en-tags">' + tagsHTML + '<input id="en-tools-input" aria-label="Add a tool" placeholder="Type and press Enter"' + (tools.length >= 10 ? ' disabled' : '') + '></div>' +
      '<div class="suggest"><span class="k">AI</span>' + sugg(ui.AI_SUGGESTIONS) + '</div>' +
      '<div class="suggest"><span class="k">Tools</span>' + sugg(ui.TOOL_SUGGESTIONS) + '</div>' +
      '<p class="help">Up to 10. Pick from the suggestions or type your own.</p><p class="err" id="en-tools-err"></p></div>'
  }

  function maturityField () {
    if (m('maturity') === 'off') return ''
    const opts = ['working', 'partial', 'concept']
    const cards = opts.map(k => {
      const mt = ui.MATURITY[k]
      const on = entry.maturity === k
      return '<label class="choice' + (on ? ' on' : '') + '"><input type="radio" name="en-mat" value="' + k + '"' + (on ? ' checked' : '') + '><span><b>' + esc(mt.label) + '</b><span class="d">' + esc(ucfirst(mt.note)) + '</span></span></label>'
    }).join('')
    return '<fieldset class="field"><legend>' + esc(LABEL.maturity) + '</legend><div class="choices three">' + cards + '</div><p class="err" id="en-maturity-err"></p></fieldset>'
  }

  function siteRow () {
    if (m('site') === 'off') return ''
    const site = data.site || {}
    const pill = site.exists && site.live_version ? '<span class="pill ok">Published</span>' : '<span class="pill">Not published yet</span>'
    const btn = site.exists && site.live_version ? '' : '<a class="btn sm" href="' + esc(href('participant', ev.slug, 'website')) + '">Publish your website</a>'
    return '<div class="field"><span class="lab">' + esc(LABEL.site) + '</span><div class="row-flex"><span>' + pill + '</span>' + btn + '</div></div>'
  }

  const summarySection = m('title') === 'off' ? '' : (
    '<section class="card"><h2>Summary card</h2><p class="sub">Judges read this first, in about 20 seconds. Keep each line short.</p>' +
    textField('title') + textField('problem') + textField('tagline') + textField('audience') + trackField() + toolsField() + maturityField() +
    '</section>'
  )

  const oneOfCount = SHOW_FIELDS.filter(f => m(f) === 'one_of').length
  const showIntro = rules.summary ? '<p class="sub">This event asks for <b>' + esc(rules.summary) + '</b>.' + (oneOfCount > 1 && m('code_url') !== 'required' ? ' One is enough.' : '') + '</p>' : ''
  const showSection = SHOW_FIELDS.every(f => m(f) === 'off') ? '' : (
    '<section class="card' + (missing.some(x => x === 'video_url' || x === 'site' || x === 'code_url' || x === 'screenshot' || x === 'one_of') ? ' has-error' : '') + '"><h2>Show your work</h2>' + showIntro +
    '<p class="err" id="en-one_of-err"></p>' +
    textField('video_url', 'url') +
    siteRow() +
    (m('code_url') === 'off' ? '' : textField('code_url', 'url')) +
    (m('screenshot') === 'off' ? '' : shotField()) +
    '</section>'
  )

  function shotField () {
    const suffix = m('screenshot') === 'optional' ? '<span class="opt">(optional)</span>' : ''
    return '<div class="field"><div class="lab-row"><label for="en-shot">' + esc(LABEL.screenshot) + '</label>' + suffix + '</div>' +
      '<div class="site-frame" id="en-shot-frame"' + (entry.has_screenshot ? '' : ' hidden') + '><img id="en-shot-img" alt="Screenshot" hidden></div>' +
      '<input class="input" type="file" id="en-shot" accept="image/png,image/jpeg,image/webp">' +
      '<div class="form-actions"><button type="button" class="btn sm" id="en-shot-remove"' + (entry.has_screenshot ? '' : ' hidden') + '>Remove screenshot</button><span class="hint">Screenshot changes save straight away.</span></div>' +
      '<p class="err" id="en-screenshot-err"></p></div>'
  }

  // `description` is its own rule line server-side (entryRuleFields) --
  // most presets leave it Optional (so it folds, true to the prototype),
  // but a custom config, or the "Ideas and design" preset, can make it
  // Required or Off.
  const descMode = m('description')
  const moreSection = descMode === 'off' ? '' : (
    '<details class="card"' + (descMode === 'required' || entry.description ? ' open' : '') + '><summary>More detail' + (descMode === 'required' ? ' (required)' : ' (optional)') + '</summary><div class="field" style="margin-top:12px"><label for="en-description">' + esc(LABEL.description) + '</label><textarea class="input" id="en-description" maxlength="5000">' + esc(entry.description || '') + '</textarea><p class="err" id="en-description-err"></p></div></details>'
  )

  const consentLine = '<p class="help">After results, your summary card, video and website can appear on the event’s public page if the organisers show your entry.</p>'

  const footHint = status === 'submitted' ? 'Each change is checked against what the event asks for.' : 'Saving a draft never checks anything.'
  const footButtons = status === 'submitted'
    ? '<button type="button" class="btn primary" id="en-save">Save changes</button>'
    : '<button type="button" class="btn" id="en-save">Save draft</button><button type="button" class="btn primary" id="en-submit-foot">Submit entry</button>'
  const foot = '<div class="sticky-foot">' + footButtons + '<span class="small muted hint-wide">' + esc(footHint) + '</span></div>'

  const preview = '<p class="section-title" style="margin-top:0">How judges see it</p><div id="en-preview"></div>' +
    // The "How you're judged" rubric card (audit item 1, on main since
    // 2026-11-02) goes in this slot, under the live preview.
    '<div id="entry-rubric-slot" data-slot="judging-rubric">' + rubricCardHTML(ui, ctx.main.__enRubric) + '</div>'

  return '<div class="split"><div>' + summarySection + showSection + moreSection + consentLine + foot + '</div><aside>' + preview + '</aside></div>'
}
function ucfirst (s) { return s ? s[0].toUpperCase() + s.slice(1) : s }

// --- Wiring -------------------------------------------------------------

function wireForm (ctx, ev, data, rules, hasTracks) {
  const { main, api: client, ui } = ctx
  const api = client.api
  const entry = data.entry || {}
  const $ = id => main.querySelector('#' + id)
  const path = '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/entry'
  const m = f => mode(rules, hasTracks, f)

  let tools = Array.isArray(entry.tools) ? entry.tools.slice() : []

  function team () {
    const me = ctx.event && ctx.event.me
    const t = me && me.team
    if (!t) return { name: '', members: '' }
    return { name: t.name || '', members: (t.members || []).map(x => x.display_name).filter(Boolean).join(', ') }
  }

  function currentTrackName () {
    const sel = $('en-track')
    if (!sel) return entry.track ? entry.track.name : ''
    const slug = sel.value
    const t = (ev.tracks || []).find(x => x.slug === slug)
    return t ? t.name : ''
  }

  function updatePreview () {
    const slot = $('en-preview')
    if (!slot) return
    const matEl = main.querySelector('input[name="en-mat"]:checked')
    const t = team()
    const previewEntry = {
      title: ($('en-title') && $('en-title').value) || entry.title || 'Untitled',
      team_name: t.name,
      team_members: t.members,
      track: currentTrackName() ? { name: currentTrackName() } : null,
      problem: ($('en-problem') && $('en-problem').value) || '',
      tagline: ($('en-tagline') && $('en-tagline').value) || '',
      audience: ($('en-audience') && $('en-audience').value) || '',
      tools,
      maturity: matEl ? matEl.value : (entry.maturity || '')
    }
    slot.innerHTML = ''
    slot.append(ui.entryCard(previewEntry, { compact: true }))
  }

  // Counters + live preview on every text field this event actually shows.
  ;['title', 'problem', 'tagline', 'audience'].forEach(f => {
    const el = $('en-' + f)
    if (!el) return
    el.addEventListener('input', () => {
      const ctr = $('en-' + f + '-ctr')
      if (ctr) ctr.textContent = el.value.length + ' / ' + MAXLEN[f]
      updatePreview()
    })
  })
  const trackSel = $('en-track')
  if (trackSel) trackSel.addEventListener('change', updatePreview)
  main.querySelectorAll('input[name="en-mat"]').forEach(r => r.addEventListener('change', () => {
    main.querySelectorAll('.choices.three .choice').forEach(c => c.classList.toggle('on', c.querySelector('input').checked))
    updatePreview()
  }))

  // Tag input: Enter or comma adds, Backspace on an empty field removes the
  // last tag, a suggestion button adds that tag.
  function renderTags () {
    const group = $('en-tags')
    if (!group) return
    const input = $('en-tools-input')
    group.querySelectorAll('.tag').forEach(n => n.remove())
    tools.forEach((t, i) => {
      const span = document.createElement('span')
      span.className = 'tag' + (ui.AI_SUGGESTIONS.some(a => a.toLowerCase() === t.toLowerCase()) ? ' ai' : '')
      span.textContent = t
      const btn = document.createElement('button')
      btn.type = 'button'
      btn.setAttribute('aria-label', 'Remove ' + t)
      btn.textContent = '×'
      btn.addEventListener('click', () => { tools.splice(i, 1); renderTags(); updatePreview(); main.__enForm?.edited($('en-tools-input')) })
      span.append(btn)
      group.insertBefore(span, input)
    })
    if (input) input.disabled = tools.length >= 10
    renderSuggestions()
    updatePreview()
  }
  function renderSuggestions () {
    main.querySelectorAll('.suggest').forEach((row, idx) => {
      const list = idx === 0 ? ui.AI_SUGGESTIONS : ui.TOOL_SUGGESTIONS
      const k = row.querySelector('.k')
      row.querySelectorAll('button').forEach(b => b.remove())
      list.filter(s => !tools.some(t => t.toLowerCase() === s.toLowerCase())).forEach(s => {
        const b = document.createElement('button')
        b.type = 'button'
        b.textContent = '+ ' + s
        b.addEventListener('click', () => addTag(s))
        row.append(b)
      })
      if (k) row.insertBefore(k, row.firstChild)
    })
  }
  function addTag (raw) {
    const v = String(raw || '').trim()
    if (!v || tools.length >= 10) return
    if (tools.some(t => t.toLowerCase() === v.toLowerCase())) return
    tools.push(v.slice(0, 30))
    renderTags()
    main.__enForm?.edited($('en-tools-input'))
  }
  const toolsInput = $('en-tools-input')
  if (toolsInput) {
    toolsInput.addEventListener('keydown', e => {
      if (e.key === 'Enter' || e.key === ',') {
        e.preventDefault()
        addTag(toolsInput.value)
        toolsInput.value = ''
        main.__enForm?.edited(toolsInput)
      } else if (e.key === 'Backspace' && !toolsInput.value) {
        tools.pop()
        renderTags()
        main.__enForm?.edited(toolsInput)
      }
    })
  }
  if ($('en-tags')) renderTags()

  if (entry.has_screenshot) ui.showImage(ctx, main.querySelector('#en-shot-img'), '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/entry/screenshot')

  updatePreview()

  // A write that finds the team frozen or taken down meanwhile reloads the
  // page so it shows that state instead of a stale form.
  async function blockedRefresh (resp) {
    const code = resp && resp.data && resp.data.code
    if (!isBlockedCode(code)) return false
    ui.toast(errText(resp), main, true)
    await render(ctx)
    return true
  }

  const clearErrors = () => form.clearErrors()
  const fieldErr = (f, msg) => form.fieldError(f, msg)
  function showFieldError (code, msg, sent) {
    if (code === 'invalid_url') {
      const which = ['video_url', 'code_url'].filter(f => sent && Object.prototype.hasOwnProperty.call(sent, f))
      which.forEach(f => fieldErr(f, msg))
      return which.length > 0
    }
    if (code === 'invalid_track') { fieldErr('track', msg); return true }
    if (code === 'image_too_large' || code === 'unsupported_image') { fieldErr('screenshot', msg); return true }
    const field = ['title', 'problem', 'tagline', 'audience', 'tools', 'maturity', 'description'].find(f => code === 'invalid_' + f)
    if (!field) return false
    fieldErr(field, msg)
    return true
  }

  function diffBody () {
    const body = {}
    ;['title', 'problem', 'tagline', 'audience'].forEach(f => { const el = $('en-' + f); if (el && (el.value || '') !== (entry[f] || '')) body[f] = el.value })
    const vEl = $('en-video_url'); if (vEl && (vEl.value.trim() || '') !== (entry.video_url || '')) body.video_url = vEl.value.trim()
    const cEl = $('en-code_url'); if (cEl && (cEl.value.trim() || '') !== (entry.code_url || '')) body.code_url = cEl.value.trim()
    const dEl = $('en-description'); if (dEl && (dEl.value || '') !== (entry.description || '')) body.description = dEl.value
    if (trackSel) { const was = entry.track ? entry.track.slug : ''; if (trackSel.value !== was) body.track = trackSel.value }
    const matEl = main.querySelector('input[name="en-mat"]:checked')
    const matVal = matEl ? matEl.value : ''
    if (matVal !== (entry.maturity || '')) body.maturity = matVal
    const was = Array.isArray(entry.tools) ? entry.tools : []
    if (JSON.stringify(tools) !== JSON.stringify(was)) body.tools = tools
    return body
  }

  async function doSave (btn) {
    clearErrors()
    if (toolsInput?.value.trim()) { addTag(toolsInput.value); toolsInput.value = '' }
    const body = diffBody()
    if (!Object.keys(body).length) { ui.toast('Saved', btn); return { ok: true, data: data } }
    body.updated_at = entry.updated_at || ''
    if (btn) btn.disabled = true
    try {
      const resp = await api('PUT', path, body)
      if (!resp.ok && resp.data && resp.data.code === 'entry_changed') {
        if (btn) btn.disabled = false
        await showConflict(ctx, ev, data, body, resp.data.entry)
        return { ok: false, conflict: true }
      }
      if (!resp.ok) {
        if (await blockedRefresh(resp)) return { ok: false, blocked: true }
        if (resp.data && resp.data.code === 'entry_would_be_incomplete') {
          // A submitted entry can't lose a line the event asks for. Nothing
          // was saved, the form keeps what was typed.
          ui.toast(errText(resp), btn || main, true)
          markMissing(main, Array.isArray(resp.data.missing) ? resp.data.missing : [], data.rules || {})
          if (btn) btn.disabled = false
          return { ok: false }
        }
        const msg = errText(resp)
        if (!showFieldError(resp.data && resp.data.code, msg, body)) ui.toast(msg, btn || main, true)
        if (btn) btn.disabled = false
        return { ok: false }
      }
      return { ok: true, data: resp.data || {} }
    } catch (e) {
      if (client.isSignedOut(e)) return { ok: false, signedOut: true }
      ui.toast('Cannot reach the server.', btn || main, true)
      if (btn) btn.disabled = false
      return { ok: false }
    }
  }

  const saveBtn = $('en-save')
  const form = ui.forms.bind(ctx, saveBtn, async () => {
    const r = await doSave(saveBtn)
    if (r.ok) { await ui.forms.preserve(ctx, () => paint(ctx, ev, r.data)) }
    return r
  }, { root: main, label: 'Entry', snapshot: () => [ui.forms.values(main.querySelectorAll(ui.forms.controls)), tools], restoreFields: true, restore: value => { tools = value[1]; renderTags(); updatePreview() } })
  main.__enForm = form
  ;['track', 'tools', 'maturity', 'screenshot', 'one_of', 'site'].forEach(f => {
    const n = f === 'tools' ? toolsInput : f === 'maturity' ? main.querySelector('[name="en-mat"]') : f === 'screenshot' ? $('en-shot') : f === 'one_of' || f === 'site' ? $('en-video_url') || main.querySelector('a[href*="s=website"]') : $('en-' + f)
    if (n) n.dataset.formField = f === 'site' ? 'one_of' : f
  })
  const submitFoot = $('en-submit-foot')
  if (submitFoot) submitFoot.addEventListener('click', () => doSubmit(ctx, ev, data))

  $('en-shot') && $('en-shot').addEventListener('change', async () => {
    const input = $('en-shot')
    const file = input.files && input.files[0]
    if (!file) return
    clearErrors()
    if (file.size > 2 * 1024 * 1024 || (file.type && ['image/png', 'image/jpeg', 'image/webp'].indexOf(file.type) < 0)) {
      fieldErr('screenshot', 'Use a PNG, JPEG or WebP image, up to 2 MB.')
      input.value = ''
      return
    }
    try {
      const resp = await client.apiBytes('PUT', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/entry/screenshot', file, file.type)
      input.value = ''
      if (!resp.ok) {
        if (await blockedRefresh(resp)) return
        const msg = errText(resp)
        const code = resp.data && resp.data.code
        if (code === 'image_too_large' || code === 'unsupported_image') fieldErr('screenshot', msg)
        else ui.toast(msg, input, true)
        return
      }
      await ui.forms.preserve(ctx, () => paint(ctx, ev, resp.data || {}))
      main.__enForm.status('Screenshot saved')
    } catch (e) {
      input.value = ''
      if (!client.isSignedOut(e)) ui.toast('Cannot reach the server.', input, true)
    }
  })

  const shotRemove = $('en-shot-remove')
  if (shotRemove) shotRemove.addEventListener('click', async () => {
    const ok = await ui.confirm({ title: 'Remove the screenshot?', body: 'It comes off your entry.', confirmLabel: 'Remove', danger: true })
    if (!ok) return
    shotRemove.disabled = true
    try {
      const resp = await api('DELETE', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/entry/screenshot')
      if (!resp.ok) {
        if (await blockedRefresh(resp)) return
        ui.toast(errText(resp), shotRemove, true); shotRemove.disabled = false; return
      }
      await ui.forms.preserve(ctx, () => paint(ctx, ev, resp.data || {}))
      main.__enForm.status('Screenshot removed')
    } catch (e) {
      if (!client.isSignedOut(e)) { ui.toast('Cannot reach the server.', shotRemove, true); shotRemove.disabled = false }
    }
  })

  // Exposed for doSubmit/doUnsubmit below (module-scope functions need the
  // live diff + save, not a stale closure from paint()).
  main.__enDiffBody = diffBody
  main.__enSave = async btn => { let response; await form.save(null, async () => { response = await doSave(btn); return response }); return response || { ok: false } }
}

// Marks every missing line after a refused submit (or a refused save on a
// submitted entry): its .err line says what to add, its field is outlined,
// and focus moves to the first one (atlas participant-entry, "submit
// refused"). "one_of" is the show-your-work group, named once at the top of
// that card. "site" has no input, so its line is named on the group's err.
function markMissing (main, missing, rules) {
  const form = main.__enForm
  if (!form) return
  const ADD = {
    title: 'Add a project name.', problem: 'Add the problem, in one line.', tagline: 'Add the solution, in one line.',
    audience: "Add who it's for.", track: 'Pick a track', tools: 'Add at least one tool.', maturity: 'Choose what works today.',
    description: 'Add a description.', video_url: 'Add a video link.', code_url: 'Add a code link.', screenshot: 'Add a screenshot.',
    one_of: 'Add ' + (rules.summary || 'a video or a website') + '.', site: 'Publish your website.'
  }
  form.clearErrors()
  missing.forEach(f => form.fieldError(f === 'site' ? 'one_of' : f, ADD[f] || 'Fill this in.'))
  form.summary(missing.map(f => ({ field: f === 'site' ? 'one_of' : f, label: PLAIN[f] || f })), 'Add these first: ')
  form.focus(missing[0] === 'site' ? 'one_of' : missing[0])
}

async function doSubmit (ctx, ev, data) {
  const { main, api: client, ui } = ctx
  const api = client.api
  const path = '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/entry'
  const submitBtns = main.querySelectorAll('#en-submit, #en-submit-foot')
  submitBtns.forEach(b => { b.disabled = true })
  try {
    if (main.__enSave) {
      const saved = await main.__enSave(null)
      if (!saved.ok) { submitBtns.forEach(b => { b.disabled = false }); return }
      if (saved.data && saved.data.entry) data = saved.data
      if (main.__enForm?.dirty()) { submitBtns.forEach(b => { b.disabled = false }); main.__enForm.status('Not saved'); return }
    }
    const resp = await api('POST', path + '/submit', {})
    if (resp.ok) { await ui.forms.preserve(ctx, () => paint(ctx, ev, resp.data || {})); return }
    // The server unifies this with the save-time guard under one code
    // (entryWrite, hack_entries.go): 422 entry_would_be_incomplete, with a
    // ready-made sentence in `error` ("Add these first: …") and the raw
    // `missing` ids for focus. `data` here is already the freshly-saved
    // draft (main.__enSave above), so no extra GET is needed to repaint it.
    if (resp.data && (resp.data.code === 'entry_incomplete' || resp.data.code === 'entry_would_be_incomplete')) {
      paint(ctx, ev, data)
      const missing = Array.isArray(resp.data.missing) ? resp.data.missing : []
      markMissing(main, missing, data.rules || {})
      return
    }
    if (resp.data && resp.data.code === 'submissions_closed') { ui.toast(errText(resp), main, true); await render(ctx); return }
    ui.toast(errText(resp, 'Could not submit.'), main, true)
    submitBtns.forEach(b => { b.disabled = false })
  } catch (e) {
    if (!client.isSignedOut(e)) ui.toast('Cannot reach the server.', main, true)
    submitBtns.forEach(b => { b.disabled = false })
  }
}

async function doUnsubmit (ctx, ev, data) {
  const { main, api: client, ui } = ctx
  const api = client.api
  const title = (data.entry && data.entry.title) || 'this entry'
  const ok = await ui.confirm({
    title: 'Move ' + title + ' back to draft?',
    body: 'It stops counting as submitted. At the deadline a complete draft is submitted for you, and an incomplete one is still judged, marked incomplete.',
    confirmLabel: 'Move to draft'
  })
  if (!ok) return
  try {
    const resp = await api('POST', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/entry/unsubmit', {})
    if (!resp.ok) { ui.toast(errText(resp, 'Could not move this entry back to draft.'), main, true); return }
    paint(ctx, ev, resp.data || {})
  } catch (e) { if (!client.isSignedOut(e)) ui.toast('Cannot reach the server.', main, true) }
}

// --- Read-only (after the team's deadline, or stage Closed on) -----------

// Nothing in the saved entry (build-plan.md's entryListItem/ownEntryJSON has
// no separate "exists"/"started" field) -- same test organiser-entries.js
// uses to tell a genuinely untouched entry apart from an attempted one.
function hasAnyContent (entry) {
  return !!(entry.title || entry.problem || entry.tagline || entry.audience || entry.maturity || (entry.tools && entry.tools.length) || entry.video_url || entry.code_url || entry.has_screenshot)
}

function paintReadOnly (ctx, ev, data, { rules, hasTracks, status }) {
  const { main, ui } = ctx
  const esc = ui.esc
  const entry = data.entry || {}
  const head = '<div class="ph"><div class="t"><h1>Entry</h1><p>' + esc(ev.title) + '</p></div></div>'

  if (status === 'submitted') {
    const by = data.submit_auto ? 'Submitted automatically at the deadline' : ('Submitted' + (data.submitted_at ? ' · ' + ui.dateTime(data.submitted_at, ev) : '') + (data.submitted_by ? ' by ' + data.submitted_by : ''))
    const bodyCard = buildReadOnlyCard(ctx, ev, entry, false)
    main.innerHTML = head +
      '<div class="entry-status submitted"><div class="t"><b>' + esc(by) + '</b><span>Entries are closed. This is what the judges see.</span></div></div>' +
      '<div class="split"><div>' + bodyCard + '</div><aside><div class="card"><h2>What happens next</h2><p class="small">The judges score every submitted entry. Results show here and on the event page when the organisers publish them.</p></div>' + rubricCardHTML(ui, main.__enRubric) + '</aside></div>'
    return
  }

  // status === 'not_submitted': the deadline sweep settled this one without
  // it ever being submitted (hack_entry_rules.go SettleEntryIncomplete).
  if (status === 'not_submitted' && hasAnyContent(entry)) {
    // Coordinator decision (2026-11-02): still judged, flagged, not hidden
    // -- exact copy given for both this screen and Home.
    const bodyCard = buildReadOnlyCard(ctx, ev, entry, true)
    main.innerHTML = head +
      '<div class="entry-status incomplete" role="status"><div class="t"><b>Entries closed</b><span>Not complete. It will still be judged, marked incomplete.</span></div></div>' +
      '<div class="split"><div>' + bodyCard + '</div><aside><div class="card"><h2>What happens next</h2><p class="small">The judges score this entry too, with an Incomplete flag. Results show here and on the event page when the organisers publish them.</p></div>' + rubricCardHTML(ui, main.__enRubric) + '</aside></div>'
    return
  }

  // Nothing was ever entered.
  const closedText = CLOSED_TEXT[data.refusal] || CLOSED_TEXT.submissions_closed
  const closedSentence = closedText.replace(/\.+$/, '') + '. This is what the judges see.'
  const body = '<section class="card"><h2>Your entry</h2><p class="muted">Nothing was entered by the deadline.</p></section>'
  main.innerHTML = head + '<div class="note info">' + esc(closedSentence) + '</div>' + body + rubricCardHTML(ui, main.__enRubric)
}

function buildReadOnlyCard (ctx, ev, entry, incomplete) {
  const { ui } = ctx
  const esc = ui.esc
  const me = ctx.event && ctx.event.me
  const names = me && me.team ? (me.team.members || []).map(x => x.display_name).filter(Boolean).join(', ') : ''
  const card = ui.entryCard({
    title: entry.title, team_name: me && me.team ? me.team.name : '', team_members: names,
    track: entry.track, problem: entry.problem, tagline: entry.tagline, audience: entry.audience,
    tools: entry.tools, maturity: entry.maturity
  }, { incomplete })
  const wrap = document.createElement('div')
  wrap.append(card)
  let extra = '<section class="card"><h2>Show your work</h2><dl>'
  const videoLink = entry.video_url ? safeHref(entry.video_url) : ''
  extra += '<dt>Video</dt><dd>' + (videoLink ? '<a href="' + esc(videoLink) + '" target="_blank" rel="noopener">' + esc(entry.video_url) + '</a>' : '(none)') + '</dd>'
  const codeLink = entry.code_url ? safeHref(entry.code_url) : ''
  extra += '<dt>Code</dt><dd>' + (codeLink ? '<a href="' + esc(codeLink) + '" target="_blank" rel="noopener">' + esc(entry.code_url) + '</a>' : '(none)') + '</dd>'
  extra += '<dt>Screenshot</dt><dd>' + (entry.has_screenshot ? '<span id="en-ro-shot">Loading…</span>' : '(none)') + '</dd>'
  extra += '</dl></section>'
  wrap.innerHTML += extra
  if (entry.has_screenshot) {
    setTimeout(() => {
      const slot = document.getElementById('en-ro-shot')
      if (!slot) return
      slot.textContent = ''
      const img = document.createElement('img')
      img.alt = 'Screenshot'
      img.style.maxWidth = '100%'
      img.hidden = true
      slot.append(img)
      ui.showImage(ctx, img, '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/entry/screenshot')
    }, 0)
  }
  return wrap.innerHTML
}

// A teammate saved over this entry while it was open here. Optimistic
// concurrency (spec: entry-edit-conflict-resolution) -- a real choice, not a
// toast, with both versions shown side by side.
async function showConflict (ctx, ev, data, myBody, newer) {
  const { ui } = ctx
  const entry = data.entry || {}
  function mineOf (f) { return Object.prototype.hasOwnProperty.call(myBody, f) ? myBody[f] : (entry[f] || '') }
  const FIELDS = ['title', 'problem', 'tagline', 'audience', 'video_url', 'code_url', 'description', 'maturity']
  const LABELS = { title: 'title', problem: 'problem', tagline: 'solution', audience: "who it's for", video_url: 'video link', code_url: 'code link', description: 'description', maturity: 'what works today' }
  const changed = FIELDS.filter(f => (mineOf(f) || '') !== (newer[f] || ''))
  const grid = document.createElement('div')
  grid.className = 'grid2'
  if (changed.length) {
    for (const f of changed) {
      const mine = mineOf(f)
      const mineCard = document.createElement('div')
      mineCard.className = 'card'
      mineCard.append(Object.assign(document.createElement('h2'), { textContent: 'Your ' + LABELS[f] }))
      mineCard.append(Object.assign(document.createElement('p'), { textContent: mine || '(empty)' }))
      const theirCard = document.createElement('div')
      theirCard.className = 'card'
      theirCard.append(Object.assign(document.createElement('h2'), { textContent: 'Their ' + LABELS[f] }))
      theirCard.append(Object.assign(document.createElement('p'), { textContent: newer[f] || '(empty)' }))
      grid.append(mineCard, theirCard)
    }
  } else {
    grid.append(Object.assign(document.createElement('p'), { textContent: 'Nothing in your edit is different from theirs.' }))
  }
  return ui.confirm({
    title: 'A teammate changed this entry while you were editing',
    body: grid,
    confirmLabel: 'Keep mine',
    cancelLabel: 'Take theirs',
    focus: 'confirm'
  }).then(async keep => {
    const api = ctx.api.api
    const path = '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/entry'
    if (!keep) {
      const r = await api('GET', path)
      if (r.ok) paint(ctx, ev, r.data || {})
      return
    }
    const body = Object.assign({}, myBody, { updated_at: newer.updated_at || '' })
    try {
      const r = await api('PUT', path, body)
      if (!r.ok) { ctx.ui.toast(errText(r, 'Could not save. Try again.'), ctx.main, true); return }
      ctx.ui.toast('Saved', ctx.main)
      paint(ctx, ev, r.data || {})
    } catch (e) { if (!ctx.api.isSignedOut(e)) ctx.ui.toast('Cannot reach the server.', ctx.main, true) }
  })
}
