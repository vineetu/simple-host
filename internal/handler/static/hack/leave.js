// Leave this event (SIM-340): one flow, every entry point. Used by
// screens/participant-home.js, screens/judge-home.js, the pending-applicant
// locked view in app.js, screens/organiser-settings.js and
// screens/landing-your-events.js's Home row menu. See
// docs/ux-atlas/spec.json (leave-event, withdraw-application,
// hand-over-event, close-event-on-leave) and the atlas copy for the exact
// text every branch below reproduces.
//
// openLeave always fetches a fresh preview itself (never trusts a preview a
// caller fetched earlier for its own row text) -- another organiser or
// teammate can change things while the dialog sits open, which the server
// catches as 409 leave_state_changed/last_organiser/successor_required/
// invalid_successor/submissions_closed/archive_needs_participants; on any
// of those this reloads the preview and redraws with a note, same dialog
// shape, instead of failing silently.
import { href } from './nav.js'

const FLASH_KEY = 'hack-flash'

function leavePath(slug) { return '/v1/hack/events/' + encodeURIComponent(slug) + '/leave' }

// GET the preview -- exported so a screen that wants to show case-specific
// copy on its own danger-zone row (participant-home, judge-home,
// organiser-settings) can fetch it once at render time. openLeave fetches
// its own copy again right before drawing the dialog; the two calls are
// independent on purpose (see the module note above).
export function previewLeave(api, slug) {
  return api.api('GET', leavePath(slug))
}

function flash(text) {
  try { sessionStorage.setItem(FLASH_KEY, text) } catch (e) {}
}
// Read-and-clear, for landing-your-events.js's render to show the toast
// left by whichever screen the person just left from.
export function takeFlash() {
  let text = ''
  try { text = sessionStorage.getItem(FLASH_KEY) || ''; sessionStorage.removeItem(FLASH_KEY) } catch (e) {}
  return text
}

function para(text, cls) {
  const p = document.createElement('p')
  if (cls) p.className = cls
  p.textContent = text
  return p
}
function warnNote(text) {
  const d = document.createElement('div')
  d.className = 'note warn'
  d.setAttribute('role', 'alert')
  d.textContent = text
  return d
}
// "Ayu and Nadia", "Ayu, Nadia and 2 others" -- the join rule every name
// list on this screen (teammates, organisers staying) uses.
function joinNames(names) {
  const list = (names || []).filter(Boolean)
  if (!list.length) return 'They'
  if (list.length === 1) return list[0]
  return list.slice(0, -1).join(', ') + ' and ' + list[list.length - 1]
}
// team.others (max 3 names) + team.others_count (the real total) -- append
// "N others" for the names the preview didn't include before joining.
function teammatesPhrase(team) {
  const names = (team.others || []).slice()
  const shown = names.length
  const extra = Math.max(0, (team.others_count || shown) - shown)
  if (extra > 0) names.push(extra === 1 ? '1 other' : extra + ' others')
  return joinNames(names)
}

// ---- the simple (ui.confirm) cases: participant, applicant, judge, co_organiser ----

function confirmParticipant(ui, preview, title, warn) {
  const team = preview.team
  let body, confirmLabel
  if (team) {
    if (team.deleted_on_leave) {
      body = [
        para(`You're the only member of ${team.name}, so the team, its entry and its website will be deleted for good.`),
        para("You'll lose access to the event. You can join again while sign-up is open.", 'muted')
      ]
      confirmLabel = 'Delete team and leave'
    } else {
      body = [
        para(`You'll leave ${team.name} and lose access to the event. ${teammatesPhrase(team)} keep the team, its entry and its website.`),
        para(`You can join again while sign-up is open, but not as part of ${team.name} unless they let you back in.`, 'muted')
      ]
      confirmLabel = 'Leave event'
    }
  } else {
    const open = preview.rejoin === 'join'
    body = [para(open ? "You'll lose access to the event. You can join again while sign-up is open." : "You'll lose access to the event. Sign-up has closed, so you can't join again.")]
    confirmLabel = 'Leave event'
  }
  if (warn) body.unshift(warnNote(warn))
  return ui.confirm({ title: `Leave ${title}?`, body, confirmLabel, danger: true })
}

function confirmApplicant(ui, title, warn) {
  const body = [para(`Your application to ${title} and your answers will be removed. You can apply again while sign-up is open.`)]
  if (warn) body.unshift(warnNote(warn))
  return ui.confirm({ title: 'Withdraw your application?', body, confirmLabel: 'Withdraw application', danger: true })
}

function confirmJudge(ui, preview, title, warn) {
  const n = preview.scored_teams || 0
  const body = [para(n > 0
    ? `You'll lose access to judging. Your scores for ${n} team${n === 1 ? '' : 's'} stay and still count in the results.`
    : "You'll lose access to judging.")]
  if (preview.rejoin === 'judge_link') body.push(para('You can come back with the judge link until results are published.', 'muted'))
  if (warn) body.unshift(warnNote(warn))
  return ui.confirm({ title: `Leave ${title}?`, body, confirmLabel: 'Leave event', danger: true })
}

function confirmCoOrganiser(ui, preview, title, warn) {
  const names = joinNames(preview.organisers_staying)
  const body = [
    para(`You'll stop being an organiser and lose access to everything here. ${names} keep running it.`),
    para('To come back, ask an organiser for a new co-organiser invite.', 'muted')
  ]
  if (warn) body.unshift(warnNote(warn))
  return ui.confirm({ title: `Leave ${title}?`, body, confirmLabel: 'Leave event', danger: true })
}

// ---- the form-sheet cases: creator (hand over), last_organiser (end/delete) ----
// Hand-rolled, like _orgA-util.js's stagePicker, rather than built on
// ui.dialog: both need a live footer (button label/enabled state changes as
// a radio or the typed confirm field changes) that ui.dialog's fixed button
// list has no hook for.

function focusablesIn(box) {
  return Array.from(box.querySelectorAll('a[href],button,input,select,textarea,[tabindex]')).filter(el => !el.disabled && el.tabIndex !== -1 && !el.hidden)
}
function openSheet(labelledBy, build) {
  return new Promise(resolve => {
    const previous = document.activeElement
    const scrim = document.createElement('div')
    scrim.className = 'scrim sheet'
    const box = document.createElement('div')
    box.className = 'dialog form'
    box.setAttribute('role', 'dialog')
    box.setAttribute('aria-modal', 'true')
    box.setAttribute('aria-labelledby', labelledBy)
    scrim.append(box)
    function done(value) { document.removeEventListener('keydown', keydown, true); scrim.remove(); if (previous && previous.focus) previous.focus(); resolve(value) }
    function keydown(e) {
      if (e.key === 'Escape') { e.preventDefault(); done(null); return }
      if (e.key === 'Tab') {
        const items = focusablesIn(box)
        if (!items.length) return
        e.preventDefault()
        const i = items.indexOf(document.activeElement)
        items[(i + (e.shiftKey ? items.length - 1 : 1) + items.length) % items.length].focus()
      }
    }
    scrim.addEventListener('click', e => { if (e.target === scrim) done(null) })
    document.addEventListener('keydown', keydown, true)
    build(box, done)
    document.body.append(scrim)
    const first = focusablesIn(box)[0]
    if (first) first.focus()
  })
}
// Radio cards, one column -- `.choices`/`.choice` markup, same as the atlas
// prototype's handover/last-organiser dialogs.
function choicesNode(ui, items, name, selectedIndex) {
  const wrap = document.createElement('div')
  wrap.className = 'choices'
  wrap.style.gridTemplateColumns = '1fr'
  wrap.setAttribute('role', 'radiogroup')
  items.forEach((it, i) => {
    const label = document.createElement('label')
    const on = i === selectedIndex
    label.className = 'choice' + (on ? ' on' : '') + (it.disabled ? ' off' : '')
    const input = document.createElement('input')
    input.type = 'radio'
    input.name = name
    input.value = it.id
    if (on) input.checked = true
    if (it.disabled) input.disabled = true
    const span = document.createElement('span')
    const b = document.createElement('b')
    b.textContent = it.title
    const d = document.createElement('span')
    d.className = 'd'
    d.textContent = it.detail
    span.append(b, d)
    label.append(input, span)
    input.addEventListener('change', () => {
      wrap.querySelectorAll('.choice').forEach(c => c.classList.remove('on'))
      label.classList.add('on')
    })
    wrap.appendChild(label)
  })
  return wrap
}

// Creator: must hand over to another organiser first. Resolves
// `{successor_user_id}` or null on cancel.
function dialogForCreator(ui, preview, title, warn) {
  return openSheet('lv-h', (box, done) => {
    box.innerHTML = `<div class="dlg-head"><h2 id="lv-h">Hand over ${ui.esc(title)}</h2>`
      + `<p>You created this event, so choose who takes it over before you leave. They become its main organiser, and nobody can remove them from it.</p></div>`
      + `<div class="dlg-body" id="lv-body"></div>`
      + `<div class="acts"><button class="btn quiet" type="button" data-cancel>Cancel</button><button class="btn danger" type="button" data-go>Hand over and leave</button></div>`
    const bodyEl = box.querySelector('#lv-body')
    if (warn) bodyEl.append(warnNote(warn))
    const successors = preview.successors || []
    const choices = choicesNode(ui, successors.map(s => ({ id: s.user_id, title: s.display_name, detail: 'Co-organiser' + (s.email ? ' · ' + s.email : '') })), 'successor', 0)
    bodyEl.append(choices)
    bodyEl.insertAdjacentHTML('beforeend', "<p class=\"small muted\">You'll lose access to managing the event. To come back, ask them for a co-organiser invite.</p>")
    const go = box.querySelector('[data-go]')
    go.disabled = !successors.length
    box.querySelector('[data-cancel]').addEventListener('click', () => done(null))
    go.addEventListener('click', () => {
      const picked = choices.querySelector('input[name=successor]:checked')
      if (!picked) return
      done({ successor_user_id: picked.value })
    })
  })
}

// Last organiser: leaving closes the event, End or Delete. Resolves
// `{close: 'end'|'delete'}` or null on cancel.
function dialogForLastOrganiser(ui, preview, title, warn) {
  return openSheet('lv-h', (box, done) => {
    const endBlocked = !!(preview.close_options && preview.close_options.end && preview.close_options.end.blocked)
    box.innerHTML = `<div class="dlg-head"><h2 id="lv-h">You're the only organiser</h2>`
      + `<p>Nobody would be left to run ${ui.esc(title)} if you leave, so leaving closes it. Choose how.</p></div>`
      + `<div class="dlg-body" id="lv-body"></div>`
      + `<div class="acts"><button class="btn quiet" type="button" data-cancel>Cancel</button><button class="btn danger" type="button" data-go>End event and leave</button></div>`
    const bodyEl = box.querySelector('#lv-body')
    if (warn) bodyEl.append(warnNote(warn))
    const items = [
      { id: 'end', title: 'End the event', disabled: endBlocked, detail: endBlocked ? "Nobody joined, so there's nothing to keep. Delete it instead." : "The public page and results stay up as a record. Nothing can change after this, and you can't come back to manage it." },
      { id: 'delete', title: 'Delete the event', detail: 'The public page, results, team sites, entries, scores and votes go for good. There is no undo.' }
    ]
    const choices = choicesNode(ui, items, 'close-mode', endBlocked ? 1 : 0)
    bodyEl.append(choices)
    const field = document.createElement('div')
    field.className = 'field'
    field.hidden = true // sync() below sets the real state from the default selection
    field.innerHTML = `<label for="lv-confirm-slug">Type ${ui.esc(preview.slug)} to confirm</label><input class="input mono" id="lv-confirm-slug" type="text">`
    bodyEl.append(field)
    bodyEl.insertAdjacentHTML('beforeend', `<p class="small muted">Want someone else to run it? Cancel and invite a co-organiser from <a href="${ui.esc(href('organiser', preview.slug, 'people'))}">People</a> first.</p>`)
    const go = box.querySelector('[data-go]')
    const input = field.querySelector('#lv-confirm-slug')
    function sync() {
      const delSel = choices.querySelector('input[value=delete]').checked
      field.hidden = !delSel
      go.textContent = delSel ? 'Delete event and leave' : 'End event and leave'
      go.disabled = delSel ? input.value.trim() !== preview.slug : false
    }
    choices.querySelectorAll('input[type=radio]').forEach(r => r.addEventListener('change', sync))
    input.addEventListener('input', sync)
    sync()
    box.querySelector('[data-cancel]').addEventListener('click', () => done(null))
    go.addEventListener('click', () => {
      const delSel = choices.querySelector('input[value=delete]').checked
      if (delSel && input.value.trim() !== preview.slug) return
      done({ close: delSel ? 'delete' : 'end' })
    })
  })
}

const STALE_CODES = ['leave_state_changed', 'last_organiser', 'successor_required', 'invalid_successor', 'submissions_closed', 'archive_needs_participants']

async function attempt(api, ui, slug, title, warn) {
  let r
  try { r = await api.api('GET', leavePath(slug)) }
  catch (e) {
    if (api.isSignedOut(e)) return
    ui.toast('Cannot reach the server.', null, true)
    return
  }
  if (!r.ok) { ui.toast(ui.errText(r), null, true); return }
  const preview = r.data || {}
  if (preview.blocked) {
    await ui.dialog({ title: `Leave ${title}?`, body: preview.blocked.message || "This event can't be left right now.", buttons: [{ label: 'OK', value: true, className: 'btn primary' }] })
    return
  }
  let proceed = false, extra = {}
  switch (preview.case) {
    case 'participant':
      proceed = await confirmParticipant(ui, preview, title, warn)
      break
    case 'applicant':
      proceed = await confirmApplicant(ui, title, warn)
      break
    case 'judge':
      proceed = await confirmJudge(ui, preview, title, warn)
      break
    case 'co_organiser':
      proceed = await confirmCoOrganiser(ui, preview, title, warn)
      break
    case 'creator': {
      const result = await dialogForCreator(ui, preview, title, warn)
      if (!result) return
      extra = { successor_user_id: result.successor_user_id }
      proceed = true
      break
    }
    case 'last_organiser': {
      const result = await dialogForLastOrganiser(ui, preview, title, warn)
      if (!result) return
      extra = { close: result.close }
      proceed = true
      break
    }
    default:
      ui.toast("This event can't be left right now.", null, true)
      return
  }
  if (!proceed) return
  let post
  try { post = await api.api('POST', leavePath(slug), Object.assign({ confirm: slug }, extra)) }
  catch (e) {
    if (api.isSignedOut(e)) return
    ui.toast('Cannot reach the server.', null, true)
    return
  }
  if (!post.ok) {
    const code = post.data && post.data.code
    if (STALE_CODES.includes(code)) return attempt(api, ui, slug, title, 'Something changed while this was open. Check the details again.')
    ui.toast(ui.errText(post), null, true)
    return
  }
  const data = post.data || {}
  let text
  if (preview.case === 'applicant') text = `You withdrew your application to ${title}.`
  else if (preview.case === 'creator' && data.successor) text = `You handed ${title} to ${data.successor.display_name} and left it.`
  else if (data.event === 'ended') text = `You ended ${title} and left it.`
  else if (data.event === 'deleted') text = `${title} was deleted.`
  else text = `You left ${title}.`
  flash(text)
  // A full load, not ctx.go(): the event switcher and the cached /v1/hack/events
  // list both need to drop the event this person just left.
  location.assign('/events')
}

// The one entry point every screen calls. `api` is the ctx.api module
// (api.api/isSignedOut), `ui` is ctx.ui, `slug`/`title` the event's, `from`
// a short label for where this was opened from (not currently branched on,
// kept for future diagnostics/analytics).
export async function openLeave({ api, ui, slug, title, from }) {
  return attempt(api, ui, slug, title, null)
}
